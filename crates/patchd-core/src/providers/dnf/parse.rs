//! Parsers for rpm and dnf output. Built on `rpm --qf` and `repoquery --qf`, which both
//! dnf generations support, and on the transaction table `--assumeno` prints.

use crate::model::{Package, ProviderId, Update, UpdateKind};
use crate::provider::ProviderError;
use crate::version::RpmVersion;

/// `--qf` for `rpm -qa`: real tab and newline characters, not escapes, because dnf5
/// prints `\t` literally where dnf4 and rpm expand it.
pub(super) const RPM_QF: &str = "%{NAME}\t%{EPOCHNUM}:%{VERSION}-%{RELEASE}\t%{ARCH}\n";
/// `--qf` for `dnf repoquery`. dnf4 adds its own newline after each record, so the
/// output has empty lines; they are skipped.
pub(super) const REPOQUERY_QF: &str =
    "%{name}\t%{epoch}:%{version}-%{release}\t%{arch}\t%{repoid}\n";

pub(super) fn dnf_id() -> ProviderId {
    ProviderId::new("dnf")
}

/// Which dnf: dnf5 on Fedora 41+, dnf4 on RHEL/AlmaLinux/Rocky 9 and 10.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Generation {
    Dnf4,
    Dnf5,
}

/// `dnf --version`: dnf5 prints `dnf5 version 5.4.3.0`, dnf4 a bare `4.20.0`.
#[must_use]
pub fn generation(version_output: &str) -> Generation {
    if version_output.trim_start().starts_with("dnf5") {
        Generation::Dnf5
    } else {
        Generation::Dnf4
    }
}

/// `rpm -qa` with [`RPM_QF`]. `gpg-pubkey` entries are imported keys, not packages.
pub(super) fn parse_rpm_qa(out: &str) -> Result<Vec<Package>, ProviderError> {
    let mut packages = Vec::new();
    for (i, line) in out.lines().enumerate() {
        if line.trim().is_empty() {
            continue;
        }
        let fields: Vec<&str> = line.split('\t').collect();
        let [name, evr, arch] = fields[..] else {
            return Err(ProviderError::Parse {
                line: i + 1,
                msg: format!("expected 3 tab-separated fields, got {}", fields.len()),
            });
        };
        if name == "gpg-pubkey" {
            continue;
        }
        packages.push(Package {
            provider: dnf_id(),
            name: name.to_owned(),
            version: evr.to_owned(),
            arch: Some(arch.to_owned()),
            held: false,
        });
    }
    packages.sort_by(|a, b| a.name.cmp(&b.name).then_with(|| a.arch.cmp(&b.arch)));
    Ok(packages)
}

/// One `repoquery` row.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(super) struct Candidate {
    pub name: String,
    pub evr: String,
    pub arch: String,
    pub repo: String,
}

pub(super) fn parse_repoquery(out: &str) -> Result<Vec<Candidate>, ProviderError> {
    let mut rows = Vec::new();
    for (i, line) in out.lines().enumerate() {
        if line.trim().is_empty() {
            continue;
        }
        let fields: Vec<&str> = line.split('\t').collect();
        let [name, evr, arch, repo] = fields[..] else {
            return Err(ProviderError::Parse {
                line: i + 1,
                msg: format!("expected 4 tab-separated fields, got {}", fields.len()),
            });
        };
        rows.push(Candidate {
            name: name.to_owned(),
            evr: evr.to_owned(),
            arch: arch.to_owned(),
            repo: repo.to_owned(),
        });
    }
    Ok(rows)
}

/// Upgrades from `repoquery --upgrades`, classified: security when the same package and
/// version is in the `--security` result, otherwise major or minor by the version.
pub(super) fn updates(
    installed: &[Package],
    upgrades: &[Candidate],
    security: &[Candidate],
) -> Vec<Update> {
    upgrades
        .iter()
        .map(|c| {
            // Several versions can be installed (kernels); the newest is what upgrades.
            let from = installed
                .iter()
                .filter(|p| p.name == c.name && p.arch.as_deref() == Some(c.arch.as_str()))
                .filter_map(|p| RpmVersion::parse(&p.version).ok().map(|v| (v, &p.version)))
                .max_by(|a, b| a.0.cmp(&b.0))
                .map(|(_, v)| v.clone());
            let is_security = security
                .iter()
                .any(|s| s.name == c.name && s.arch == c.arch && same_evr(&s.evr, &c.evr));
            Update {
                provider: dnf_id(),
                kind: classify(from.as_deref(), &c.evr, is_security),
                name: c.name.clone(),
                arch: Some(c.arch.clone()),
                from_version: from,
                to_version: c.evr.clone(),
                origins: vec![c.repo.clone()],
                held: false,
            }
        })
        .collect()
}

fn classify(from: Option<&str>, to: &str, security: bool) -> UpdateKind {
    if security {
        return UpdateKind::Security;
    }
    let major = |v: &str| RpmVersion::parse(v).ok().and_then(|v| v.major());
    match (from.and_then(major), major(to)) {
        // Date-like versions (`2026e`, `20260116`) change their "major" every release.
        (Some(a), Some(b)) if a != b && a < 1000 && b < 1000 => UpdateKind::Major,
        _ => UpdateKind::Minor,
    }
}

pub(super) fn same_evr(a: &str, b: &str) -> bool {
    match (RpmVersion::parse(a), RpmVersion::parse(b)) {
        (Ok(a), Ok(b)) => a == b,
        _ => a == b,
    }
}

/// `name-epoch:version-release.arch`, the spelling `dnf upgrade` and `rpm -q` accept.
#[must_use]
pub fn nevra(name: &str, evr: &str, arch: &str) -> String {
    let evr = if evr.contains(':') {
        evr.to_owned()
    } else {
        format!("0:{evr}")
    };
    format!("{name}-{evr}.{arch}")
}

/// What a transaction table says it would do.
#[derive(Debug, Default, PartialEq, Eq)]
pub(super) struct Transaction {
    /// Installs, upgrades, downgrades and reinstalls, with `from_version: None` for a
    /// package that is not installed yet.
    pub changes: Vec<TxPackage>,
    /// Packages removed for good. An install-only package (kernels) replaced by a newer
    /// copy of itself, as `installonly_limit` does on every kernel update, is not one.
    pub removals: Vec<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub(super) struct TxPackage {
    pub name: String,
    pub arch: String,
    /// Always with an epoch: dnf4 leaves `0:` off.
    pub evr: String,
    pub new: bool,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Section {
    Upgrade,
    Install,
    Remove,
    Other,
}

fn section(header: &str) -> Section {
    let h = header.trim_end_matches(':').to_ascii_lowercase();
    if h.starts_with("upgrading") || h.starts_with("downgrading") || h.starts_with("reinstalling") {
        Section::Upgrade
    } else if h.starts_with("installing") {
        Section::Install
    } else if h.starts_with("removing") || h.starts_with("erasing") {
        Section::Remove
    } else {
        Section::Other
    }
}

/// Parses the table `dnf upgrade --assumeno` prints, in both generations' layouts:
/// section headers at column 0 (`Upgrading:`, `Installing dependencies:`,
/// `Removing:`), one indented row per package (`name arch version repo size`), and in
/// dnf5 an extra-indented `replacing …` row under each upgrade. dnf4 wraps a long name
/// onto its own line, with the other columns on the next.
pub(super) fn parse_transaction(out: &str) -> Transaction {
    let mut tx = Transaction::default();
    let mut current = Section::Other;
    let mut wrapped: Option<String> = None;
    for line in out.lines() {
        let line = line.trim_end();
        if line.is_empty() {
            continue;
        }
        if !line.starts_with(' ') {
            if line.starts_with("Transaction Summary") {
                break;
            }
            current = if line.ends_with(':') {
                section(line)
            } else {
                Section::Other
            };
            wrapped = None;
            continue;
        }
        if current == Section::Other {
            continue;
        }
        let trimmed = line.trim_start();
        if trimmed.starts_with("replacing ") || trimmed.starts_with("obsoleting ") {
            continue;
        }
        let mut fields: Vec<&str> = trimmed.split_whitespace().collect();
        let joined;
        if let Some(name) = wrapped.take() {
            joined = name;
            fields.insert(0, &joined);
        }
        if fields.len() == 1 {
            wrapped = Some(fields[0].to_owned());
            continue;
        }
        let [name, arch, version, ..] = fields[..] else {
            continue;
        };
        let evr = if version.contains(':') {
            version.to_owned()
        } else {
            format!("0:{version}")
        };
        match current {
            Section::Remove => tx.removals.push(name.to_owned()),
            Section::Upgrade | Section::Install => tx.changes.push(TxPackage {
                name: name.to_owned(),
                arch: arch.to_owned(),
                evr,
                new: current == Section::Install,
            }),
            Section::Other => {}
        }
    }
    let rotated: Vec<String> = tx
        .changes
        .iter()
        .filter(|p| p.new)
        .map(|p| p.name.clone())
        .collect();
    tx.removals.retain(|name| !rotated.contains(name));
    tx
}
