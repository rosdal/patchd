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

/// Files in `apt.conf.d` that apt itself skips (`Dir::Ignore-Files-Silently`).
const APT_IGNORED_SUFFIXES: [&str; 6] =
    ["~", ".disabled", ".bak", ".save", ".orig", ".distUpgrade"];

/// Warns when unattended-upgrades will also install updates (ADR 0010: warn, don't
/// abort). That takes all three: the program installed, `apt-daily-upgrade.timer`
/// enabled, and `APT::Periodic::Unattended-Upgrade` non-zero. The timer alone is not
/// enough, because apt enables it on every install; with the setting at its default of
/// `0` it installs nothing.
#[must_use]
pub fn unattended_upgrades_check(root: &Root) -> Option<Check> {
    let dir = root.join("/etc/apt/apt.conf.d");
    let Ok(entries) = std::fs::read_dir(&dir) else {
        // Not an apt system.
        return None;
    };
    let mut files: Vec<_> = entries
        .filter_map(Result::ok)
        .map(|e| e.path())
        .filter(|p| {
            let name = p
                .file_name()
                .map(|n| n.to_string_lossy().into_owned())
                .unwrap_or_default();
            !APT_IGNORED_SUFFIXES.iter().any(|s| name.ends_with(s))
                && !name.contains(".dpkg-")
                && !name.contains(".ucf-")
        })
        .collect();
    files.sort();
    // The last file to set it wins, as in apt.
    let setting = files
        .iter()
        .filter_map(|f| std::fs::read_to_string(f).ok())
        .filter_map(|text| periodic_unattended(&text))
        .next_back();
    let installed = root.join("/usr/bin/unattended-upgrade").is_file();
    let timer = root
        .join("/etc/systemd/system/timers.target.wants/apt-daily-upgrade.timer")
        .exists();
    let enabled = setting.as_deref().is_some_and(|v| v != "0");
    Some(if installed && timer && enabled {
        Check::warn(
            "unattended-upgrades",
            "unattended-upgrades is installed and enabled (APT::Periodic::Unattended-Upgrade and apt-daily-upgrade.timer); it competes with Patchd for the dpkg lock, so disable it (ADR 0010)",
        )
    } else {
        Check::pass("unattended-upgrades", "not active")
    })
}

/// The last value of `APT::Periodic::Unattended-Upgrade` in one apt.conf file, in
/// either syntax: `APT::Periodic::Unattended-Upgrade "1";` or
/// `APT::Periodic { Unattended-Upgrade "1"; };`.
fn periodic_unattended(text: &str) -> Option<String> {
    static FLAT: std::sync::LazyLock<regex::Regex> = std::sync::LazyLock::new(|| {
        regex::Regex::new(r#"APT::Periodic::Unattended-Upgrade\s+"([^"]*)""#).unwrap()
    });
    static BLOCK: std::sync::LazyLock<regex::Regex> = std::sync::LazyLock::new(|| {
        regex::Regex::new(r#"APT::Periodic\s*\{[^}]*?\bUnattended-Upgrade\s+"([^"]*)""#).unwrap()
    });
    let text: String = text
        .lines()
        .map(|l| l.split("//").next().unwrap_or(""))
        .filter(|l| !l.trim_start().starts_with('#'))
        .collect::<Vec<_>>()
        .join("\n");
    FLAT.captures_iter(&text)
        .chain(BLOCK.captures_iter(&text))
        .map(|c| (c.get(1).map_or(0, |m| m.start()), c[1].to_owned()))
        .max_by_key(|(pos, _)| *pos)
        .map(|(_, v)| v)
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

        // Stock Debian: the timer is enabled by apt, but nothing is installed.
        write(
            &root,
            "/etc/apt/apt.conf.d/10periodic",
            "APT::Periodic::Update-Package-Lists \"1\";\n",
        );
        write(
            &root,
            "/etc/systemd/system/timers.target.wants/apt-daily-upgrade.timer",
            "",
        );
        assert_eq!(
            unattended_upgrades_check(&root).unwrap().status,
            CheckStatus::Pass
        );

        write(&root, "/usr/bin/unattended-upgrade", "");
        write(
            &root,
            "/etc/apt/apt.conf.d/20auto-upgrades",
            "APT::Periodic::Unattended-Upgrade \"1\";\n",
        );
        assert_eq!(
            unattended_upgrades_check(&root).unwrap().status,
            CheckStatus::Warn
        );

        // A later file turning it off wins, as in apt; files apt ignores do not count.
        write(
            &root,
            "/etc/apt/apt.conf.d/99patchd",
            "APT::Periodic::Unattended-Upgrade \"0\"; // off\n",
        );
        write(
            &root,
            "/etc/apt/apt.conf.d/99zz.dpkg-old",
            "APT::Periodic::Unattended-Upgrade \"1\";\n",
        );
        assert_eq!(
            unattended_upgrades_check(&root).unwrap().status,
            CheckStatus::Pass
        );

        // Disabling the timer is enough too.
        std::fs::remove_file(root.join("/etc/apt/apt.conf.d/99patchd")).unwrap();
        std::fs::remove_file(
            root.join("/etc/systemd/system/timers.target.wants/apt-daily-upgrade.timer"),
        )
        .unwrap();
        assert_eq!(
            unattended_upgrades_check(&root).unwrap().status,
            CheckStatus::Pass
        );
    }

    #[test]
    fn periodic_setting_syntaxes() {
        assert_eq!(
            periodic_unattended("APT::Periodic::Unattended-Upgrade \"1\";").as_deref(),
            Some("1")
        );
        assert_eq!(
            periodic_unattended("// APT::Periodic::Unattended-Upgrade \"1\";"),
            None
        );
        assert_eq!(
            periodic_unattended(
                "APT::Periodic {\n  Update-Package-Lists \"1\";\n  Unattended-Upgrade \"1\";\n};"
            )
            .as_deref(),
            Some("1")
        );
        assert_eq!(
            periodic_unattended(
                "APT::Periodic::Unattended-Upgrade \"1\";\nAPT::Periodic::Unattended-Upgrade \"0\";"
            )
            .as_deref(),
            Some("0"),
            "the last one in a file wins"
        );
        assert_eq!(
            periodic_unattended("APT::Periodic::Unattended-Upgrade-Foo \"1\";"),
            None
        );
    }
}
