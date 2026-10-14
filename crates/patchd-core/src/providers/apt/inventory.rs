//! Parses `dpkg-query -W` output into installed packages.

use std::fmt::Write as _;

use crate::model::{Package, ProviderId};
use crate::provider::{Inventory, ProviderError};

/// The `-f` format: name, version, architecture and the 3-character status.
pub(super) const FORMAT: &str =
    r"-f=${binary:Package}\t${Version}\t${Architecture}\t${db:Status-Abbrev}\n";

/// How many package names a warning lists before it says "and N more".
const WARN_LIST_MAX: usize = 10;

struct Row<'a> {
    name: &'a str,
    version: &'a str,
    arch: &'a str,
    desired: char,
    status: char,
    error: char,
}

pub(super) fn parse(output: &str) -> Result<Inventory, ProviderError> {
    let mut rows = Vec::new();
    for (i, line) in output.lines().enumerate() {
        let line = line.strip_suffix('\r').unwrap_or(line);
        if line.trim().is_empty() {
            continue;
        }
        let fields: Vec<&str> = line.split('\t').collect();
        let [name, version, arch, abbrev] = fields[..] else {
            return Err(ProviderError::Parse {
                line: i + 1,
                msg: format!("expected 4 tab-separated fields, got {}", fields.len()),
            });
        };
        // Desired / status / error flag. Editors strip the trailing space of "ii ", so a
        // short field is padded rather than rejected.
        let mut flags = abbrev.chars().chain(std::iter::repeat(' '));
        let (desired, status, error) = (
            flags.next().unwrap_or(' '),
            flags.next().unwrap_or(' '),
            flags.next().unwrap_or(' '),
        );
        if name.is_empty() || !desired.is_ascii_alphabetic() || !status.is_ascii_alphabetic() {
            return Err(ProviderError::Parse {
                line: i + 1,
                msg: format!("bad package name or status {abbrev:?}"),
            });
        }
        rows.push(Row {
            name,
            version,
            arch,
            desired,
            status,
            error,
        });
    }

    // `${binary:Package}` qualifies every Multi-Arch: same package (`libc6:amd64`), but
    // apt-get, apt-mark and apt-cache leave the suffix off for the native architecture.
    // dpkg itself is always native, so its architecture tells us which suffix to drop.
    let native = rows
        .iter()
        .find(|r| r.name == "dpkg" || r.name.starts_with("dpkg:"))
        .map(|r| r.arch);

    let mut packages = Vec::new();
    let mut broken = Vec::new();
    for row in &rows {
        let name = match native {
            Some(native) => row
                .name
                .strip_suffix(native)
                .and_then(|n| n.strip_suffix(':'))
                .unwrap_or(row.name),
            None => row.name,
        };
        if row.error != ' ' || (row.desired == 'i' && matches!(row.status, 'U' | 'F' | 'H')) {
            broken.push(name.to_owned());
        }
        if row.status != 'i' {
            continue;
        }
        packages.push(Package {
            provider: ProviderId::APT,
            name: name.to_owned(),
            version: row.version.to_owned(),
            arch: (!row.arch.is_empty()).then(|| row.arch.to_owned()),
            held: row.desired == 'h',
        });
    }

    let mut warnings = Vec::new();
    if !broken.is_empty() {
        warnings.push(format!(
            "dpkg reports {} package(s) half-installed or in an error state: {}; fix with `dpkg --configure -a` before applying updates",
            broken.len(),
            name_list(&broken)
        ));
    }
    Ok(Inventory { packages, warnings })
}

pub(super) fn name_list(names: &[String]) -> String {
    let mut s = names
        .iter()
        .take(WARN_LIST_MAX)
        .cloned()
        .collect::<Vec<_>>()
        .join(", ");
    if names.len() > WARN_LIST_MAX {
        let _ = write!(s, " and {} more", names.len() - WARN_LIST_MAX);
    }
    s
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn keeps_installed_and_marks_held() {
        let out = "dpkg\t1.22.6\tamd64\tii \n\
                   libc6:amd64\t2.39-0ubuntu8\tamd64\tii \n\
                   libc6:i386\t2.39-0ubuntu8\ti386\tii \n\
                   libssl3t64:amd64\t3.0.13\tamd64\thi \n\
                   old-thing\t1.0\tall\trc \n";
        let inv = parse(out).unwrap();
        let names: Vec<_> = inv.packages.iter().map(|p| p.name.as_str()).collect();
        assert_eq!(names, ["dpkg", "libc6", "libc6:i386", "libssl3t64"]);
        assert!(inv.packages[3].held);
        assert!(!inv.packages[1].held);
        assert_eq!(inv.packages[2].arch.as_deref(), Some("i386"));
        assert!(inv.warnings.is_empty());
    }

    #[test]
    fn tolerates_stripped_trailing_space() {
        let inv = parse("dpkg\t1.22.6\tamd64\tii\n").unwrap();
        assert_eq!(inv.packages.len(), 1);
    }

    #[test]
    fn warns_on_broken_state() {
        let out = "dpkg\t1.22.6\tamd64\tii \n\
                   a\t1\tamd64\tiU \n\
                   b\t1\tamd64\tiF \n\
                   c\t1\tamd64\tiiR\n";
        let inv = parse(out).unwrap();
        assert_eq!(inv.packages.len(), 2, "only fully installed packages count");
        assert_eq!(inv.warnings.len(), 1);
        assert!(
            inv.warnings[0].contains("3 package(s)"),
            "{}",
            inv.warnings[0]
        );
        assert!(inv.warnings[0].contains("a, b, c"));
    }

    #[test]
    fn parse_error_carries_line_number() {
        let err = parse("dpkg\t1\tamd64\tii \nbroken line\n").unwrap_err();
        assert!(matches!(err, ProviderError::Parse { line: 2, .. }), "{err}");
    }

    #[test]
    fn long_name_lists_are_truncated() {
        let names: Vec<String> = (0..12).map(|i| format!("p{i}")).collect();
        assert!(name_list(&names).ends_with("p9 and 2 more"));
    }
}
