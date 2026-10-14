//! Checks that run before any package is touched. A refused run costs nothing; an
//! interrupted dpkg run can cost an evening.
//!
//! Provider-specific checks (dpkg health, the simulation) live with the provider; this
//! module has the shared ones and the [`Check`] type they all report.

use std::path::Path;

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

use crate::root::Root;

/// Free space `/` needs, where `/var/cache/apt/archives` and dnf's cache live.
pub const ROOT_MIN_FREE: u64 = 1024 * 1024 * 1024;
/// Free space `/boot` (and a separate `/boot/efi`) needs when the plan touches the boot.
pub const BOOT_MIN_FREE: u64 = 200 * 1024 * 1024;

/// The result of one check. Every check result goes to the run log.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct Check {
    pub name: String,
    pub status: CheckStatus,
    pub detail: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum CheckStatus {
    Pass,
    /// Worth reporting, but the run goes ahead.
    Warn,
    /// Aborts the run.
    Fail,
}

impl Check {
    #[must_use]
    pub fn pass(name: &str, detail: impl Into<String>) -> Self {
        Self::new(name, CheckStatus::Pass, detail)
    }

    #[must_use]
    pub fn warn(name: &str, detail: impl Into<String>) -> Self {
        Self::new(name, CheckStatus::Warn, detail)
    }

    #[must_use]
    pub fn fail(name: &str, detail: impl Into<String>) -> Self {
        Self::new(name, CheckStatus::Fail, detail)
    }

    fn new(name: &str, status: CheckStatus, detail: impl Into<String>) -> Self {
        Self {
            name: name.to_owned(),
            status,
            detail: detail.into(),
        }
    }

    #[must_use]
    pub fn failed(&self) -> bool {
        self.status == CheckStatus::Fail
    }
}

/// Whether the effective user is root.
#[must_use]
pub fn is_root() -> bool {
    #[cfg(unix)]
    return rustix::process::geteuid().is_root();
    #[cfg(not(unix))]
    false
}

#[must_use]
pub fn root_check(is_root: bool) -> Check {
    if is_root {
        Check::pass("root", "running as root")
    } else {
        Check::fail(
            "root",
            "installing updates needs root; run with sudo (`sudo patchd apply`)",
        )
    }
}

/// Free space on `/`, and on `/boot` and `/boot/efi` when they are separate filesystems
/// and the plan touches the boot.
#[must_use]
pub fn disk_checks(root: &Root, touches_boot: bool) -> Vec<Check> {
    let mut checks = vec![space_check("disk /", &root.join("/"), ROOT_MIN_FREE)];
    if touches_boot {
        let mut parent = root.join("/");
        for mount in ["/boot", "/boot/efi"] {
            let path = root.join(mount);
            if path.is_dir() && separate_fs(&path, &parent) {
                checks.push(space_check(&format!("disk {mount}"), &path, BOOT_MIN_FREE));
            }
            parent = path;
        }
    }
    checks
}

fn space_check(name: &str, path: &Path, need: u64) -> Check {
    match free_bytes(path) {
        Ok(free) => evaluate_space(name, path, free, need),
        Err(e) => Check::warn(
            name,
            format!("cannot read free space on {}: {e}", path.display()),
        ),
    }
}

fn evaluate_space(name: &str, path: &Path, free: u64, need: u64) -> Check {
    let detail = format!(
        "{} free on {}, need {}",
        mib(free),
        path.display(),
        mib(need)
    );
    if free >= need {
        Check::pass(name, detail)
    } else {
        Check::fail(name, detail)
    }
}

fn mib(bytes: u64) -> String {
    format!("{} MiB", bytes / (1024 * 1024))
}

#[cfg(unix)]
fn free_bytes(path: &Path) -> std::io::Result<u64> {
    let st = rustix::fs::statvfs(path)?;
    Ok(st.f_bavail.saturating_mul(st.f_frsize))
}

#[cfg(not(unix))]
fn free_bytes(_path: &Path) -> std::io::Result<u64> {
    Err(std::io::Error::other("not supported on this platform"))
}

#[cfg(unix)]
fn separate_fs(path: &Path, parent: &Path) -> bool {
    use std::os::unix::fs::MetadataExt;
    match (std::fs::metadata(path), std::fs::metadata(parent)) {
        (Ok(a), Ok(b)) => a.dev() != b.dev(),
        _ => false,
    }
}

#[cfg(not(unix))]
fn separate_fs(_path: &Path, _parent: &Path) -> bool {
    false
}

/// Warns when unattended-upgrades will also run: `APT::Periodic::Unattended-Upgrade`
/// set to a non-zero value in `/etc/apt/apt.conf.d` (the last file to set it wins, as in
/// apt), or `apt-daily-upgrade.timer` enabled. ADR 0010: warn, don't abort.
#[must_use]
pub fn unattended_upgrades_check(root: &Root) -> Option<Check> {
    let dir = root.join("/etc/apt/apt.conf.d");
    let Ok(entries) = std::fs::read_dir(&dir) else {
        // Not an apt system.
        return None;
    };
    let mut files: Vec<_> = entries.filter_map(Result::ok).map(|e| e.path()).collect();
    files.sort();
    let mut setting = None;
    for file in files {
        if let Ok(text) = std::fs::read_to_string(&file) {
            for line in text.lines() {
                if let Some(v) = periodic_unattended(line) {
                    setting = Some(v.to_owned());
                }
            }
        }
    }
    let timer = root
        .join("/etc/systemd/system/timers.target.wants/apt-daily-upgrade.timer")
        .exists();
    let periodic = setting.is_some_and(|v| v != "0");
    let mut why = Vec::new();
    if periodic {
        why.push("APT::Periodic::Unattended-Upgrade is enabled");
    }
    if timer {
        why.push("apt-daily-upgrade.timer is enabled");
    }
    Some(if why.is_empty() {
        Check::pass("unattended-upgrades", "not active")
    } else {
        Check::warn(
            "unattended-upgrades",
            format!(
                "{}; it competes with Patchd for the dpkg lock, so disable it (ADR 0010)",
                why.join(" and ")
            ),
        )
    })
}

/// The value in `APT::Periodic::Unattended-Upgrade "1";`, ignoring `//` comments.
fn periodic_unattended(line: &str) -> Option<&str> {
    let line = line.split("//").next()?.trim();
    let rest = line.strip_prefix("APT::Periodic::Unattended-Upgrade")?;
    let value = rest.trim().strip_prefix('"')?;
    value.split('"').next()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fake_root() -> (tempfile::TempDir, Root) {
        let dir = tempfile::tempdir().unwrap();
        let root = Root::at(dir.path());
        (dir, root)
    }

    fn write(root: &Root, path: &str, text: &str) {
        let path = root.join(path);
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(path, text).unwrap();
    }

    #[test]
    fn root_check_message() {
        assert!(!root_check(true).failed());
        let c = root_check(false);
        assert!(c.failed());
        assert!(c.detail.contains("sudo"));
    }

    #[test]
    fn space_thresholds() {
        let p = Path::new("/");
        assert!(!evaluate_space("disk /", p, ROOT_MIN_FREE, ROOT_MIN_FREE).failed());
        let c = evaluate_space("disk /", p, ROOT_MIN_FREE - 1, ROOT_MIN_FREE);
        assert!(c.failed());
        assert!(c.detail.contains("need 1024 MiB"), "{}", c.detail);
    }

    #[cfg(unix)]
    #[test]
    fn disk_checks_read_the_fake_root() {
        let (_dir, root) = fake_root();
        std::fs::create_dir_all(root.join("/boot")).unwrap();
        let checks = disk_checks(&root, true);
        // /boot in a tempdir is on the same filesystem, so only / is checked.
        assert_eq!(checks.len(), 1);
        assert_eq!(checks[0].name, "disk /");
        assert_ne!(checks[0].status, CheckStatus::Warn, "{:?}", checks[0]);
    }

    #[test]
    fn unattended_upgrades_detection() {
        let (_dir, root) = fake_root();
        assert_eq!(unattended_upgrades_check(&root), None, "no apt, no check");

        write(
            &root,
            "/etc/apt/apt.conf.d/10periodic",
            "APT::Periodic::Update-Package-Lists \"1\";\n",
        );
        assert_eq!(
            unattended_upgrades_check(&root).unwrap().status,
            CheckStatus::Pass
        );

        write(
            &root,
            "/etc/apt/apt.conf.d/20auto-upgrades",
            "APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n",
        );
        let c = unattended_upgrades_check(&root).unwrap();
        assert_eq!(c.status, CheckStatus::Warn);
        assert!(c.detail.contains("Unattended-Upgrade"), "{}", c.detail);

        // A later file turning it off wins, as in apt.
        write(
            &root,
            "/etc/apt/apt.conf.d/99patchd",
            "APT::Periodic::Unattended-Upgrade \"0\"; // off\n",
        );
        assert_eq!(
            unattended_upgrades_check(&root).unwrap().status,
            CheckStatus::Pass
        );

        write(
            &root,
            "/etc/systemd/system/timers.target.wants/apt-daily-upgrade.timer",
            "",
        );
        let c = unattended_upgrades_check(&root).unwrap();
        assert_eq!(c.status, CheckStatus::Warn);
        assert!(c.detail.contains("apt-daily-upgrade.timer"), "{}", c.detail);
    }

    #[test]
    fn commented_setting_is_ignored() {
        assert_eq!(
            periodic_unattended("// APT::Periodic::Unattended-Upgrade \"1\";"),
            None
        );
        assert_eq!(
            periodic_unattended("APT::Periodic::Unattended-Upgrade \"1\";"),
            Some("1")
        );
        assert_eq!(
            periodic_unattended("APT::Periodic::Unattended-Upgrade-Foo \"1\";"),
            None
        );
    }
}
