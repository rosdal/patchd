//! Parses `apt-get -s dist-upgrade` and `apt-cache policy` into updates.

use std::sync::LazyLock;

use regex::Regex;

use crate::model::{ProviderId, Update, UpdateKind};
use crate::provider::ProviderError;
use crate::version::DebVersion;

/// `Inst name [old] (new Origin, Origin [arch])`, optionally followed by `[...]` notes.
/// `[old]` is absent for a new dependency.
static INST: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(
        r"^Inst (?<name>\S+)(?: \[(?<old>[^\]]*)\])? \((?<new>\S+)(?: (?<origins>.*?))?(?: \[(?<arch>[^\]]+)\])?\)(?: \[[^\]]*\])*\s*$",
    )
    .unwrap()
});

static REMV: LazyLock<Regex> = LazyLock::new(|| Regex::new(r"^Remv (?<name>\S+)").unwrap());

/// A version line in `apt-cache policy`'s version table: ` *** 1.0-1 500`.
static POLICY_VERSION: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"^ (?:\*\*\*| {3}) (?<version>\S+) -?\d+$").unwrap());

#[derive(Debug, Default, PartialEq, Eq)]
pub(super) struct Simulation {
    pub updates: Vec<Update>,
    pub removals: Vec<String>,
    /// Upgradable, but apt held it back: held packages, and on Ubuntu 22.04 phased ones.
    pub kept_back: Vec<String>,
    /// Upgrades apt explicitly deferred for Ubuntu's phased rollout (apt 2.5+).
    pub phased: Vec<String>,
}

#[derive(Clone, Copy)]
enum Section {
    KeptBack,
    Phased,
    Other,
}

pub(super) fn parse_simulation(output: &str) -> Result<Simulation, ProviderError> {
    let mut sim = Simulation::default();
    let mut section = None;

    for (i, line) in output.lines().enumerate() {
        let line = line.strip_suffix('\r').unwrap_or(line);

        if line.starts_with(' ') {
            match section {
                Some(Section::KeptBack) => {
                    sim.kept_back
                        .extend(line.split_whitespace().map(str::to_owned));
                }
                Some(Section::Phased) => {
                    sim.phased
                        .extend(line.split_whitespace().map(str::to_owned));
                }
                Some(Section::Other) | None => {}
            }
            continue;
        }
        section = line.ends_with(':').then(|| section_for(line));

        if line.starts_with("Inst ") {
            let caps = INST.captures(line).ok_or_else(|| ProviderError::Parse {
                line: i + 1,
                msg: format!("unrecognised Inst line: {line}"),
            })?;
            let from = caps.name("old").map(|m| m.as_str().to_owned());
            let to = caps["new"].to_owned();
            let origins: Vec<String> = caps
                .name("origins")
                .map(|m| m.as_str().split(", ").map(str::to_owned).collect())
                .unwrap_or_default();
            let security = origins.iter().any(|o| inst_origin_is_security(o));
            sim.updates.push(Update {
                provider: ProviderId::APT,
                name: caps["name"].to_owned(),
                arch: caps.name("arch").map(|m| m.as_str().to_owned()),
                kind: classify(from.as_deref(), &to, security),
                from_version: from,
                to_version: to,
                origins,
                held: false,
            });
        } else if line.starts_with("Remv ") {
            let caps = REMV.captures(line).ok_or_else(|| ProviderError::Parse {
                line: i + 1,
                msg: format!("unrecognised Remv line: {line}"),
            })?;
            sim.removals.push(caps["name"].to_owned());
        }
        // Everything else (Conf lines, progress, "NOTE: This is only a simulation!") is
        // ignored.
    }
    Ok(sim)
}

fn section_for(header: &str) -> Section {
    let h = header.to_ascii_lowercase();
    if h.contains("phasing") {
        Section::Phased
    } else if h.contains("kept back") || h.starts_with("not upgrading") {
        Section::KeptBack
    } else {
        Section::Other
    }
}

/// An `Inst` origin is `Label:Version/Suite`, `Label:Suite`, or a bare site name.
/// It is a security origin when the suite ends in `-security` or the label is
/// `Debian-Security`.
fn inst_origin_is_security(origin: &str) -> bool {
    let label = origin.split_once(':').map(|(l, _)| l);
    let suite = origin
        .rsplit_once('/')
        .map(|(_, s)| s)
        .or_else(|| origin.split_once(':').map(|(_, s)| s))
        .unwrap_or(origin);
    suite.ends_with("-security") || label == Some("Debian-Security")
}

/// The apt `UpdateKind` rule: security if any origin is a security suite, major if the
/// upstream major version changes, minor otherwise.
#[must_use]
pub fn classify(from: Option<&str>, to: &str, security: bool) -> UpdateKind {
    if security {
        return UpdateKind::Security;
    }
    let major = |v: &str| DebVersion::parse(v).ok().and_then(|v| v.upstream_major());
    match from {
        Some(from) if major(from).is_some() && major(from) != major(to) => UpdateKind::Major,
        _ => UpdateKind::Minor,
    }
}

#[derive(Debug, Default, PartialEq, Eq)]
pub(super) struct Policy {
    pub name: String,
    pub installed: Option<String>,
    pub candidate: Option<String>,
    /// Sources of the candidate version, as `URL suite/component`.
    pub candidate_origins: Vec<String>,
    pub candidate_security: bool,
}

/// Parses `apt-cache policy <pkg>…`.
pub(super) fn parse_policy(output: &str) -> Vec<Policy> {
    let mut entries: Vec<Policy> = Vec::new();
    let mut in_candidate = false;

    for line in output.lines() {
        let line = line.strip_suffix('\r').unwrap_or(line);
        if !line.starts_with(' ') {
            if let Some(name) = line.strip_suffix(':') {
                entries.push(Policy {
                    name: name.to_owned(),
                    ..Policy::default()
                });
                in_candidate = false;
            }
            continue;
        }
        let Some(entry) = entries.last_mut() else {
            continue;
        };
        let trimmed = line.trim();
        if let Some(v) = trimmed.strip_prefix("Installed: ") {
            entry.installed = none_if_absent(v);
        } else if let Some(v) = trimmed.strip_prefix("Candidate: ") {
            entry.candidate = none_if_absent(v);
        } else if let Some(caps) = POLICY_VERSION.captures(line) {
            in_candidate = entry.candidate.as_deref() == Some(&caps["version"]);
        } else if in_candidate {
            // `500 http://security.ubuntu.com/ubuntu noble-security/main amd64 Packages`
            let mut parts = trimmed.split_whitespace().skip(1);
            if let (Some(url), Some(dist)) = (parts.next(), parts.next()) {
                let suite = dist.split('/').next().unwrap_or(dist);
                entry.candidate_security |= suite.ends_with("-security");
                entry.candidate_origins.push(format!("{url} {dist}"));
            }
        }
    }
    entries
}

fn none_if_absent(v: &str) -> Option<String> {
    let v = v.trim();
    (v != "(none)" && !v.is_empty()).then(|| v.to_owned())
}

/// Held packages with a newer candidate, as updates with `held: true`.
pub(super) fn held_updates(policies: Vec<Policy>) -> Vec<Update> {
    policies
        .into_iter()
        .filter_map(|p| {
            let (installed, candidate) = (p.installed?, p.candidate?);
            let newer = match (DebVersion::parse(&installed), DebVersion::parse(&candidate)) {
                (Ok(i), Ok(c)) => c > i,
                _ => candidate != installed,
            };
            newer.then(|| Update {
                provider: ProviderId::APT,
                kind: classify(Some(&installed), &candidate, p.candidate_security),
                name: p.name,
                arch: None,
                from_version: Some(installed),
                to_version: candidate,
                origins: p.candidate_origins,
                held: true,
            })
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn inst(line: &str) -> Update {
        let sim = parse_simulation(line).unwrap();
        assert_eq!(sim.updates.len(), 1, "{line}");
        sim.updates.into_iter().next().unwrap()
    }

    #[test]
    fn ubuntu_update_listed_under_updates_and_security_is_security() {
        let u = inst(
            "Inst libssl3t64 [3.0.13-0ubuntu3.4] (3.0.13-0ubuntu3.5 Ubuntu:24.04/noble-updates, Ubuntu:24.04/noble-security [amd64])",
        );
        assert_eq!(u.name, "libssl3t64");
        assert_eq!(u.from_version.as_deref(), Some("3.0.13-0ubuntu3.4"));
        assert_eq!(u.to_version, "3.0.13-0ubuntu3.5");
        assert_eq!(
            u.origins,
            ["Ubuntu:24.04/noble-updates", "Ubuntu:24.04/noble-security"]
        );
        assert_eq!(u.arch.as_deref(), Some("amd64"));
        assert_eq!(u.kind, UpdateKind::Security);
    }

    #[test]
    fn debian_point_release_is_minor() {
        let u = inst("Inst base-files [12.4+deb12u7] (12.4+deb12u8 Debian:12.8/stable [amd64])");
        assert_eq!(u.kind, UpdateKind::Minor);
        assert_eq!(u.origins, ["Debian:12.8/stable"]);
    }

    #[test]
    fn debian_security_label_is_security() {
        let u = inst(
            "Inst libgnutls30 [3.7.9-2+deb12u3] (3.7.9-2+deb12u4 Debian-Security:12/stable-security [arm64])",
        );
        assert_eq!(u.kind, UpdateKind::Security);
        assert_eq!(u.arch.as_deref(), Some("arm64"));
    }

    #[test]
    fn missing_old_version_is_new_dependency() {
        let u = inst(
            "Inst linux-image-6.8.0-49-generic (6.8.0-49.49 Ubuntu:24.04/noble-updates [amd64])",
        );
        assert_eq!(u.from_version, None);
        assert_eq!(u.to_version, "6.8.0-49.49");
        assert_eq!(u.kind, UpdateKind::Minor);
    }

    #[test]
    fn labels_with_spaces_and_trailing_notes() {
        let u = inst(
            "Inst docker-ce [5:27.3.1-1~debian.12~bookworm] (5:27.4.0-1~debian.12~bookworm Docker CE:bookworm/stable [amd64]) []",
        );
        assert_eq!(u.origins, ["Docker CE:bookworm/stable"]);
        assert_eq!(u.to_version, "5:27.4.0-1~debian.12~bookworm");
    }

    #[test]
    fn multi_arch_name_and_epoch() {
        let u = inst("Inst libc6:i386 [1:2.36-9] (1:2.36-9+deb12u9 Debian:12.9/stable [i386])");
        assert_eq!(u.name, "libc6:i386");
        assert_eq!(u.arch.as_deref(), Some("i386"));
    }

    #[test]
    fn origin_without_arch() {
        let u = inst("Inst foo [1.0] (1.1 localhost)");
        assert_eq!(u.origins, ["localhost"]);
        assert_eq!(u.arch, None);
    }

    #[test]
    fn removals_and_sections() {
        let out = "\
NOTE: This is only a simulation!
Reading package lists...
The following packages will be REMOVED:
  pve-firmware
The following packages have been kept back:
  libfoo libbar
The following upgrades have been deferred due to phasing:
  libphased
Remv pve-firmware [3.14-1]
Conf libbaz (2.0 Debian:12/stable [amd64])
";
        let sim = parse_simulation(out).unwrap();
        assert_eq!(sim.removals, ["pve-firmware"]);
        assert_eq!(sim.kept_back, ["libfoo", "libbar"]);
        assert_eq!(sim.phased, ["libphased"]);
        assert!(sim.updates.is_empty());
    }

    #[test]
    fn apt3_section_headers() {
        let out = "\
Not upgrading yet due to phasing:
  libphased
Not upgrading:
  libheld
Summary:
  Upgrading: 0, Installing: 0, Removing: 0, Not Upgrading: 2
";
        let sim = parse_simulation(out).unwrap();
        assert_eq!(sim.phased, ["libphased"]);
        assert_eq!(sim.kept_back, ["libheld"]);
    }

    #[test]
    fn bad_inst_line_reports_its_line_number() {
        let err = parse_simulation("Reading package lists...\nInst garbage\n").unwrap_err();
        assert!(matches!(err, ProviderError::Parse { line: 2, .. }), "{err}");
    }

    #[test]
    fn classify_rule() {
        assert_eq!(classify(Some("1.0"), "1.1", true), UpdateKind::Security);
        assert_eq!(classify(Some("2.9-1"), "3.0-1", true), UpdateKind::Security);
        assert_eq!(
            classify(Some("2.9-1"), "3.0-1~bpo12+1", false),
            UpdateKind::Major
        );
        assert_eq!(classify(Some("1:2.9"), "1:3.0", false), UpdateKind::Major);
        assert_eq!(
            classify(Some("2.30-2"), "2.30-2ubuntu1", false),
            UpdateKind::Minor
        );
        assert_eq!(
            classify(Some("12.4+deb12u7"), "12.4+deb12u8", false),
            UpdateKind::Minor
        );
        assert_eq!(classify(None, "6.8.0-49.49", false), UpdateKind::Minor);
    }

    #[test]
    fn security_origin_rule() {
        assert!(inst_origin_is_security("Ubuntu:24.04/noble-security"));
        assert!(inst_origin_is_security(
            "Debian-Security:12/stable-security"
        ));
        assert!(inst_origin_is_security("Debian-Security:bookworm"));
        assert!(inst_origin_is_security("Debian:bookworm-security"));
        assert!(!inst_origin_is_security("Ubuntu:24.04/noble-updates"));
        assert!(!inst_origin_is_security(
            "Proxmox:bookworm/pve-no-subscription"
        ));
        assert!(!inst_origin_is_security("localhost"));
    }

    #[test]
    fn policy_held_with_newer_security_candidate() {
        let out = "\
libssl3t64:
  Installed: 3.0.13-0ubuntu3.15
  Candidate: 3.0.13-0ubuntu3.16
  Version table:
     3.0.13-0ubuntu3.16 500
        500 http://archive.ubuntu.com/ubuntu noble-updates/main amd64 Packages
        500 http://security.ubuntu.com/ubuntu noble-security/main amd64 Packages
 *** 3.0.13-0ubuntu3.15 100
        100 /var/lib/dpkg/status
uptodate:
  Installed: 1.0
  Candidate: 1.0
  Version table:
 *** 1.0 500
        500 http://deb.debian.org/debian bookworm/main amd64 Packages
gone:
  Installed: (none)
  Candidate: 2.0
";
        let updates = held_updates(parse_policy(out));
        assert_eq!(updates.len(), 1);
        let u = &updates[0];
        assert_eq!(u.name, "libssl3t64");
        assert!(u.held);
        assert_eq!(u.kind, UpdateKind::Security);
        assert_eq!(u.from_version.as_deref(), Some("3.0.13-0ubuntu3.15"));
        assert_eq!(
            u.origins,
            [
                "http://archive.ubuntu.com/ubuntu noble-updates/main",
                "http://security.ubuntu.com/ubuntu noble-security/main"
            ]
        );
    }
}
