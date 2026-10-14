//! Debian package version parsing and comparison, matching `dpkg --compare-versions`.

use std::cmp::Ordering;
use std::fmt;

/// A Debian version string, `[epoch:]upstream[-revision]`.
#[derive(Debug, Clone)]
pub struct DebVersion {
    epoch: u64,
    upstream: String,
    revision: String,
}

#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum VersionError {
    #[error("empty version")]
    Empty,
    #[error("epoch {0:?} is not a number")]
    BadEpoch(String),
    #[error("version {0:?} has no upstream part")]
    NoUpstream(String),
}

impl DebVersion {
    /// Parses a version. The epoch is everything before the first `:`, the revision
    /// everything after the last `-`, and the upstream version is the part in between.
    ///
    /// # Errors
    ///
    /// Fails when the string is empty, the epoch is not a number, or the upstream part
    /// is empty.
    pub fn parse(s: &str) -> Result<Self, VersionError> {
        let s = s.trim();
        if s.is_empty() {
            return Err(VersionError::Empty);
        }
        let (epoch, rest) = match s.split_once(':') {
            Some((e, rest)) => {
                let epoch = e
                    .parse::<u64>()
                    .map_err(|_| VersionError::BadEpoch(e.to_owned()))?;
                (epoch, rest)
            }
            None => (0, s),
        };
        let (upstream, revision) = rest.rsplit_once('-').unwrap_or((rest, ""));
        if upstream.is_empty() {
            return Err(VersionError::NoUpstream(s.to_owned()));
        }
        Ok(Self {
            epoch,
            upstream: upstream.to_owned(),
            revision: revision.to_owned(),
        })
    }

    #[must_use]
    pub fn epoch(&self) -> u64 {
        self.epoch
    }

    #[must_use]
    pub fn upstream(&self) -> &str {
        &self.upstream
    }

    #[must_use]
    pub fn revision(&self) -> &str {
        &self.revision
    }

    /// The first numeric run of the upstream version: `2` for `2.30`, `3` for `v3.1`.
    #[must_use]
    pub fn upstream_major(&self) -> Option<u64> {
        let start = self.upstream.find(|c: char| c.is_ascii_digit())?;
        let digits: String = self.upstream[start..]
            .chars()
            .take_while(char::is_ascii_digit)
            .collect();
        digits.parse().ok()
    }
}

impl fmt::Display for DebVersion {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        if self.epoch != 0 {
            write!(f, "{}:", self.epoch)?;
        }
        f.write_str(&self.upstream)?;
        if !self.revision.is_empty() {
            write!(f, "-{}", self.revision)?;
        }
        Ok(())
    }
}

impl Ord for DebVersion {
    fn cmp(&self, other: &Self) -> Ordering {
        self.epoch
            .cmp(&other.epoch)
            .then_with(|| verrevcmp(&self.upstream, &other.upstream))
            .then_with(|| verrevcmp(&self.revision, &other.revision))
    }
}

impl PartialOrd for DebVersion {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

/// Equality follows the ordering, so `1.01 == 1.1` as in dpkg.
impl PartialEq for DebVersion {
    fn eq(&self, other: &Self) -> bool {
        self.cmp(other) == Ordering::Equal
    }
}

impl Eq for DebVersion {}

/// dpkg's character weight inside a non-digit run: `~` sorts before everything (even
/// the end of the string), then the end of the string, then letters, then the rest.
fn order(c: u8) -> i32 {
    match c {
        0 => 0,
        b'~' => -1,
        c if c.is_ascii_digit() => 0,
        c if c.is_ascii_alphabetic() => i32::from(c),
        c => i32::from(c) + 256,
    }
}

/// A port of dpkg's `verrevcmp`: alternating non-digit and digit runs.
fn verrevcmp(a: &str, b: &str) -> Ordering {
    let (a, b) = (a.as_bytes(), b.as_bytes());
    let at = |i: usize| a.get(i).copied().unwrap_or(0);
    let bt = |j: usize| b.get(j).copied().unwrap_or(0);
    let (mut i, mut j) = (0, 0);

    while i < a.len() || j < b.len() {
        while (i < a.len() && !at(i).is_ascii_digit()) || (j < b.len() && !bt(j).is_ascii_digit()) {
            let (ac, bc) = (order(at(i)), order(bt(j)));
            if ac != bc {
                return ac.cmp(&bc);
            }
            i += 1;
            j += 1;
        }
        while at(i) == b'0' {
            i += 1;
        }
        while bt(j) == b'0' {
            j += 1;
        }
        let mut first_diff = Ordering::Equal;
        while at(i).is_ascii_digit() && bt(j).is_ascii_digit() {
            if first_diff == Ordering::Equal {
                first_diff = at(i).cmp(&bt(j));
            }
            i += 1;
            j += 1;
        }
        if at(i).is_ascii_digit() {
            return Ordering::Greater;
        }
        if bt(j).is_ascii_digit() {
            return Ordering::Less;
        }
        if first_diff != Ordering::Equal {
            return first_diff;
        }
    }
    Ordering::Equal
}

#[cfg(test)]
mod tests {
    use super::*;
    use Ordering::{Equal, Greater, Less};

    fn v(s: &str) -> DebVersion {
        DebVersion::parse(s).unwrap()
    }

    /// Each row was checked by hand with `dpkg --compare-versions A lt B`.
    #[test]
    fn compares_like_dpkg() {
        let table = [
            ("1.0~rc1", "1.0", Less),
            ("1.0~~", "1.0~", Less),
            ("1.0", "1.0a", Less),
            ("1.0", "1.0+b1", Less),
            ("2.30-2", "2.30-2ubuntu1", Less),
            ("1.0-1", "1.0-10", Less),
            ("1:0.1", "2.0", Greater),
            ("1.01", "1.1", Equal),
        ];
        for (a, b, expected) in table {
            assert_eq!(v(a).cmp(&v(b)), expected, "{a} vs {b}");
            assert_eq!(v(b).cmp(&v(a)), expected.reverse(), "{b} vs {a}");
        }
    }

    #[test]
    fn missing_revision_equals_zero_revision() {
        assert_eq!(v("1.0"), v("1.0-0"));
        assert_eq!(v("0:1.0"), v("1.0"));
    }

    #[test]
    fn splits_epoch_at_first_colon_and_revision_at_last_dash() {
        let x = v("1:2.3:4-a-b");
        assert_eq!(x.epoch(), 1);
        assert_eq!(x.upstream(), "2.3:4-a");
        assert_eq!(x.revision(), "b");
    }

    #[test]
    fn rejects_bad_input() {
        assert_eq!(DebVersion::parse(""), Err(VersionError::Empty));
        assert!(matches!(
            DebVersion::parse("x:1.0"),
            Err(VersionError::BadEpoch(_))
        ));
        assert!(matches!(
            DebVersion::parse("1:-1"),
            Err(VersionError::NoUpstream(_))
        ));
    }

    #[test]
    fn upstream_major_is_first_numeric_run() {
        assert_eq!(v("2.30-2").upstream_major(), Some(2));
        assert_eq!(v("1:3.0.13-0ubuntu3.5").upstream_major(), Some(3));
        assert_eq!(v("v10.1").upstream_major(), Some(10));
        assert_eq!(v("abc").upstream_major(), None);
    }

    #[test]
    fn display_round_trips() {
        for s in ["1.0", "1:2.3-4", "2.30-2ubuntu1", "1.0~rc1+dfsg-1~bpo12+1"] {
            assert_eq!(v(s).to_string(), s);
            assert_eq!(v(&v(s).to_string()), v(s));
        }
    }
}
