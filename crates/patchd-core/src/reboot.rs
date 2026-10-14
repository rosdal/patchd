//! Reboot detection (ADR 0011). No single signal works everywhere, so the checks are
//! layered and each one that fires adds a reason.

use std::cmp::Ordering;
use std::path::Path;
use std::time::SystemTime;

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

use crate::root::Root;
use crate::runner::{Cmd, Runner};
use crate::version::DebVersion;

#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct RebootStatus {
    pub required: bool,
    /// Why, in words: `kernel 6.8.12-5-pve installed, 6.8.12-4-pve running`.
    pub reasons: Vec<String>,
    /// Services running outdated libraries, from needrestart. They need a restart, not a
    /// reboot.
    pub services_to_restart: Vec<String>,
}

impl RebootStatus {
    pub fn add_reason(&mut self, reason: impl Into<String>) {
        let reason = reason.into();
        if !self.reasons.contains(&reason) {
            self.reasons.push(reason);
        }
        self.required = true;
    }

    /// Combines the results of several checks or providers.
    pub fn merge(&mut self, other: Self) {
        for r in other.reasons {
            self.add_reason(r);
        }
        for s in other.services_to_restart {
            if !self.services_to_restart.contains(&s) {
                self.services_to_restart.push(s);
            }
        }
        self.required |= other.required;
    }
}

/// Check 1: `/run/reboot-required`, the Ubuntu convention, with the packages that asked
/// for it from `/run/reboot-required.pkgs`.
#[must_use]
pub fn reboot_required_file(root: &Root) -> RebootStatus {
    let mut status = RebootStatus::default();
    if root.join("/run/reboot-required").exists() {
        let pkgs =
            std::fs::read_to_string(root.join("/run/reboot-required.pkgs")).unwrap_or_default();
        let mut pkgs: Vec<&str> = pkgs.split_whitespace().collect();
        pkgs.dedup();
        if pkgs.is_empty() {
            status.add_reason("/run/reboot-required exists");
        } else {
            status.add_reason(format!("/run/reboot-required by {}", pkgs.join(", ")));
        }
    }
    status
}

/// Check 2: `needrestart -b -r l`, when needrestart is installed. Batch mode, list only;
/// it restarts nothing.
pub async fn needrestart(root: &Root, runner: &dyn Runner) -> RebootStatus {
    let installed = ["/usr/sbin/needrestart", "/usr/bin/needrestart"]
        .iter()
        .any(|p| root.join(p).is_file());
    if !installed {
        return RebootStatus::default();
    }
    match runner.run(&needrestart_cmd()).await {
        Ok(out) => parse_needrestart(&out.stdout),
        Err(e) => {
            tracing::debug!(error = %e, "needrestart failed");
            RebootStatus::default()
        }
    }
}

#[must_use]
pub fn needrestart_cmd() -> Cmd {
    Cmd::new("needrestart", ["-b", "-r", "l"]).env("NEEDRESTART_SUSPEND", "1")
}

/// `NEEDRESTART-KSTA: 2` or `3` means the running kernel is outdated (0 unknown, 1 current).
#[must_use]
pub fn parse_needrestart(out: &str) -> RebootStatus {
    let mut status = RebootStatus::default();
    let (mut kcur, mut kexp) = (None, None);
    for line in out.lines() {
        let Some((key, value)) = line.split_once(": ") else {
            continue;
        };
        let value = value.trim();
        match key {
            "NEEDRESTART-KCUR" => kcur = Some(value),
            "NEEDRESTART-KEXP" => kexp = Some(value),
            "NEEDRESTART-KSTA" if matches!(value, "2" | "3") => {
                status.required = true;
            }
            "NEEDRESTART-SVC" if !status.services_to_restart.iter().any(|s| s == value) => {
                status.services_to_restart.push(value.to_owned());
            }
            _ => {}
        }
    }
    if status.required {
        status.reasons.push(match (kcur, kexp) {
            (Some(cur), Some(exp)) => format!("needrestart: kernel {exp} expected, {cur} running"),
            _ => "needrestart: running kernel is outdated".to_owned(),
        });
    }
    status
}

/// Check 3: the running kernel is no longer the newest one installed. Works on Debian,
/// Proxmox and the Pi, which don't reliably create `/run/reboot-required`.
///
/// Compares the running release (`/proc/sys/kernel/osrelease`, as `uname -r`) with the
/// directories in `/lib/modules`, within the same flavour: a Pi has `-rpi-v8` and
/// `-rpi-2712` kernels side by side, and neither replaces the other.
#[must_use]
pub fn kernel_check(root: &Root) -> RebootStatus {
    let mut status = RebootStatus::default();
    let Some(running) = std::fs::read_to_string(root.join("/proc/sys/kernel/osrelease"))
        .ok()
        .map(|s| s.trim().to_owned())
        .filter(|s| !s.is_empty())
    else {
        return status;
    };
    let modules = root.join("/lib/modules");
    let Ok(entries) = std::fs::read_dir(&modules) else {
        return status;
    };
    let installed: Vec<(String, Option<SystemTime>)> = entries
        .filter_map(Result::ok)
        .filter(|e| e.path().is_dir())
        .map(|e| {
            let mtime = e.metadata().ok().and_then(|m| m.modified().ok());
            (e.file_name().to_string_lossy().into_owned(), mtime)
        })
        .collect();
    if installed.is_empty() {
        return status;
    }

    let running_dir = installed.iter().find(|(name, _)| *name == running);
    let Some((_, running_mtime)) = running_dir else {
        status.add_reason(format!(
            "running kernel {running} has no {} directory; it was removed or replaced",
            Path::new("/lib/modules").join(&running).display()
        ));
        return status;
    };
    let flavour = kernel_flavour(&running);
    let newer = installed
        .iter()
        .filter(|(name, _)| *name != running && kernel_flavour(name) == flavour)
        .filter(|(name, mtime)| match compare_kernels(name, &running) {
            Some(Ordering::Greater) => true,
            Some(_) => false,
            None => match (mtime, running_mtime) {
                (Some(a), Some(b)) => a > b,
                _ => false,
            },
        })
        .max_by(|a, b| compare_kernels(&a.0, &b.0).unwrap_or(Ordering::Equal));
    if let Some((name, _)) = newer {
        status.add_reason(format!("kernel {name} installed, {running} running"));
    }
    status
}

/// The part of a kernel release that names the build flavour: `pve` in `6.8.12-4-pve`,
/// `amd64` in `6.1.0-26-amd64`, `rpi-v8` in `6.12.25+rpt-rpi-v8`. Empty for releases
/// with no flavour suffix, such as Fedora's `6.11.4-301.fc41.x86_64`.
fn kernel_flavour(release: &str) -> &str {
    if let Some(i) = release.rfind("-rpi-") {
        return &release[i + 1..];
    }
    match release.rsplit_once('-') {
        Some((_, last)) if !last.starts_with(|c: char| c.is_ascii_digit()) => last,
        _ => "",
    }
}

/// Orders two releases of the same flavour, or `None` when that can't be decided.
fn compare_kernels(a: &str, b: &str) -> Option<Ordering> {
    let strip = |s: &str| {
        let flavour = kernel_flavour(s);
        s.strip_suffix(flavour)
            .map_or(s, |v| v.trim_end_matches(['-', '+']))
            .to_owned()
    };
    let (a, b) = (strip(a), strip(b));
    // Kernel releases have no epoch, and a dash is a separator, not a Debian revision.
    let a = DebVersion::parse(&a.replace(['-', ':'], ".")).ok()?;
    let b = DebVersion::parse(&b.replace(['-', ':'], ".")).ok()?;
    Some(a.cmp(&b))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fake_root(running: &str, modules: &[&str]) -> (tempfile::TempDir, Root) {
        let dir = tempfile::tempdir().unwrap();
        let root = Root::at(dir.path());
        std::fs::create_dir_all(root.join("/proc/sys/kernel")).unwrap();
        std::fs::write(
            root.join("/proc/sys/kernel/osrelease"),
            format!("{running}\n"),
        )
        .unwrap();
        for m in modules {
            std::fs::create_dir_all(root.join("/lib/modules").join(m)).unwrap();
        }
        (dir, root)
    }

    #[test]
    fn proxmox_newer_kernel() {
        let (_d, root) = fake_root(
            "6.8.12-4-pve",
            &["6.8.12-4-pve", "6.8.12-5-pve", "6.5.13-6-pve"],
        );
        let s = kernel_check(&root);
        assert!(s.required);
        assert_eq!(
            s.reasons,
            ["kernel 6.8.12-5-pve installed, 6.8.12-4-pve running"]
        );
    }

    #[test]
    fn proxmox_running_newest() {
        let (_d, root) = fake_root("6.8.12-5-pve", &["6.8.12-4-pve", "6.8.12-5-pve"]);
        assert!(!kernel_check(&root).required);
    }

    #[test]
    fn version_compare_is_numeric_not_lexical() {
        let (_d, root) = fake_root("6.8.12-9-pve", &["6.8.12-9-pve", "6.8.12-10-pve"]);
        assert!(kernel_check(&root).required);
    }

    #[test]
    fn raspberry_pi_flavours_do_not_replace_each_other() {
        let (_d, root) = fake_root(
            "6.12.25+rpt-rpi-v8",
            &[
                "6.12.25+rpt-rpi-v8",
                "6.12.25+rpt-rpi-2712",
                "6.12.34+rpt-rpi-2712",
            ],
        );
        assert!(!kernel_check(&root).required, "only the 2712 flavour moved");

        let (_d, root) = fake_root(
            "6.12.25+rpt-rpi-v8",
            &[
                "6.12.25+rpt-rpi-v8",
                "6.12.34+rpt-rpi-v8",
                "6.12.34+rpt-rpi-2712",
            ],
        );
        let s = kernel_check(&root);
        assert_eq!(
            s.reasons,
            ["kernel 6.12.34+rpt-rpi-v8 installed, 6.12.25+rpt-rpi-v8 running"]
        );
    }

    #[test]
    fn debian_and_ubuntu_names() {
        let (_d, root) = fake_root("6.1.0-26-amd64", &["6.1.0-26-amd64", "6.1.0-27-amd64"]);
        assert!(kernel_check(&root).required);
        let (_d, root) = fake_root(
            "6.8.0-49-generic",
            &["6.8.0-49-generic", "6.8.0-50-generic"],
        );
        assert!(kernel_check(&root).required);
    }

    #[test]
    fn missing_running_modules_dir() {
        let (_d, root) = fake_root("6.8.12-4-pve", &["6.8.12-5-pve"]);
        let s = kernel_check(&root);
        assert!(s.required);
        assert!(
            s.reasons[0].contains("6.8.12-4-pve has no"),
            "{:?}",
            s.reasons
        );
    }

    #[test]
    fn no_modules_at_all_is_not_a_reboot() {
        // Containers often have no /lib/modules.
        let (_d, root) = fake_root("6.8.0-49-generic", &[]);
        assert!(!kernel_check(&root).required);
    }

    #[test]
    fn flavours() {
        assert_eq!(kernel_flavour("6.8.12-4-pve"), "pve");
        assert_eq!(kernel_flavour("6.12.25+rpt-rpi-v8"), "rpi-v8");
        assert_eq!(kernel_flavour("6.1.0-rpi7-rpi-2712"), "rpi-2712");
        assert_eq!(kernel_flavour("6.11.4-301.fc41.x86_64"), "");
    }

    #[test]
    fn reboot_required_file_lists_packages() {
        let (_d, root) = fake_root("x", &[]);
        assert!(!reboot_required_file(&root).required);
        std::fs::create_dir_all(root.join("/run")).unwrap();
        std::fs::write(
            root.join("/run/reboot-required"),
            "*** System restart required ***\n",
        )
        .unwrap();
        assert_eq!(
            reboot_required_file(&root).reasons,
            ["/run/reboot-required exists"]
        );
        std::fs::write(
            root.join("/run/reboot-required.pkgs"),
            "linux-image-6.8.0-50-generic\nlibc6\nlibc6\n",
        )
        .unwrap();
        assert_eq!(
            reboot_required_file(&root).reasons,
            ["/run/reboot-required by linux-image-6.8.0-50-generic, libc6"]
        );
    }

    /// Captured from `needrestart -b -r l` (3.6-4+deb12u3) in a `debian:12` container, where
    /// the kernel status is unknown, including its stray Perl warning.
    const NEEDRESTART_CAPTURED: &str = r#"NEEDRESTART-VER: 3.6
NEEDRESTART-KCUR: 6.6.87.2-microsoft-standard-WSL2
NEEDRESTART-KSTA: 0
NEEDRESTART-UCSTA: 1
NEEDRESTART-UCCUR: 0xffffffff
NEEDRESTART-UCEXP:
Use of uninitialized value $ucode_vars{"AVAIL"} in concatenation (.) or string at /usr/sbin/needrestart line 952.
"#;

    /// The same format with an outdated kernel and services to restart, written from
    /// needrestart's batch-mode documentation (`KSTA` 3: an ABI-compatible upgrade is
    /// pending).
    const NEEDRESTART: &str = "\
NEEDRESTART-VER: 3.6
NEEDRESTART-KCUR: 6.1.0-26-amd64
NEEDRESTART-KEXP: 6.1.0-27-amd64
NEEDRESTART-KSTA: 3
NEEDRESTART-SVC: cron.service
NEEDRESTART-SVC: ssh.service
";

    #[test]
    fn needrestart_output() {
        let s = parse_needrestart(NEEDRESTART);
        assert!(s.required);
        assert_eq!(
            s.reasons,
            ["needrestart: kernel 6.1.0-27-amd64 expected, 6.1.0-26-amd64 running"]
        );
        assert_eq!(s.services_to_restart, ["cron.service", "ssh.service"]);

        let current = parse_needrestart("NEEDRESTART-VER: 3.6\nNEEDRESTART-KSTA: 1\n");
        assert!(!current.required);
        assert_eq!(
            parse_needrestart(NEEDRESTART_CAPTURED),
            RebootStatus::default()
        );
    }

    #[test]
    fn merge_dedups() {
        let mut a = RebootStatus::default();
        a.add_reason("x");
        let mut b = parse_needrestart(NEEDRESTART);
        b.add_reason("x");
        a.merge(b);
        assert_eq!(a.reasons.len(), 2);
        assert_eq!(a.services_to_restart.len(), 2);
    }
}
