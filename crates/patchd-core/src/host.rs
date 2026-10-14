//! Host and OS detection. Reads files instead of running commands: faster, and it works in
//! minimal containers without `lsb_release` or `hostnamectl`.

use std::collections::BTreeMap;
use std::path::Path;

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct HostInfo {
    pub hostname: String,
    pub os: OsInfo,
    /// The machine architecture, such as `x86_64` or `aarch64`.
    pub arch: String,
    /// Kernel release, such as `6.8.0-49-generic`.
    pub kernel: Option<String>,
}

/// From `os-release(5)`.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct OsInfo {
    /// `ID`, such as `ubuntu` or `debian`. Proxmox VE reports `debian`.
    pub id: String,
    pub id_like: Vec<String>,
    pub version_id: Option<String>,
    pub codename: Option<String>,
    pub pretty_name: String,
}

impl OsInfo {
    /// Parses the contents of an os-release file. Missing keys get the defaults from the
    /// spec (`ID=linux`, `PRETTY_NAME=Linux`).
    #[must_use]
    pub fn from_os_release(text: &str) -> Self {
        let mut vars = parse_os_release(text);
        let codename = vars
            .remove("VERSION_CODENAME")
            .or_else(|| vars.remove("UBUNTU_CODENAME"))
            .filter(|s| !s.is_empty());
        Self {
            id: vars.remove("ID").unwrap_or_else(|| "linux".to_owned()),
            id_like: vars
                .remove("ID_LIKE")
                .map(|s| s.split_whitespace().map(str::to_owned).collect())
                .unwrap_or_default(),
            version_id: vars.remove("VERSION_ID"),
            codename,
            pretty_name: vars
                .remove("PRETTY_NAME")
                .unwrap_or_else(|| "Linux".to_owned()),
        }
    }

    /// Reads `/etc/os-release`, falling back to `/usr/lib/os-release`.
    #[must_use]
    pub fn detect() -> Option<Self> {
        ["/etc/os-release", "/usr/lib/os-release"]
            .iter()
            .find_map(|p| std::fs::read_to_string(p).ok())
            .map(|text| Self::from_os_release(&text))
    }

    /// Ubuntu or an Ubuntu derivative, which matters for phased updates.
    #[must_use]
    pub fn is_ubuntu_like(&self) -> bool {
        self.id == "ubuntu" || self.id_like.iter().any(|s| s == "ubuntu")
    }
}

impl HostInfo {
    /// Detects the current host.
    #[must_use]
    pub fn detect() -> Self {
        Self {
            hostname: gethostname::gethostname().to_string_lossy().into_owned(),
            os: OsInfo::detect().unwrap_or_else(|| OsInfo::from_os_release("")),
            arch: std::env::consts::ARCH.to_owned(),
            kernel: std::fs::read_to_string("/proc/sys/kernel/osrelease")
                .ok()
                .map(|s| s.trim().to_owned())
                .filter(|s| !s.is_empty()),
        }
    }
}

/// Whether this process runs inside a container. Patching inside one is almost never
/// what someone wants.
#[must_use]
pub fn in_container() -> bool {
    in_container_at(Path::new("/"))
}

fn in_container_at(root: &Path) -> bool {
    if root.join(".dockerenv").exists() || root.join("run/.containerenv").exists() {
        return true;
    }
    // Unreadable without root on most systems; that just means "can't tell".
    std::fs::read(root.join("proc/1/environ")).is_ok_and(|environ| {
        environ
            .split(|&b| b == 0)
            .any(|var| var.starts_with(b"container="))
    })
}

/// Whether the effective user is root, from `/proc/self/status`.
#[must_use]
pub fn is_root() -> bool {
    std::fs::read_to_string("/proc/self/status").is_ok_and(|s| effective_uid(&s) == Some(0))
}

fn effective_uid(status: &str) -> Option<u32> {
    let line = status.lines().find(|l| l.starts_with("Uid:"))?;
    // Uid: real effective saved filesystem
    line.split_whitespace().nth(2)?.parse().ok()
}

/// Parses `KEY=value` lines. Values may be double-quoted (with backslash escapes) or
/// single-quoted (literal). Comments and blank lines are skipped.
fn parse_os_release(text: &str) -> BTreeMap<String, String> {
    text.lines()
        .map(str::trim)
        .filter(|l| !l.is_empty() && !l.starts_with('#'))
        .filter_map(|l| l.split_once('='))
        .filter(|(k, _)| !k.is_empty())
        .map(|(k, v)| (k.trim().to_owned(), unquote(v.trim())))
        .collect()
}

fn unquote(v: &str) -> String {
    if let Some(inner) = v.strip_prefix('\'') {
        return inner.strip_suffix('\'').unwrap_or(inner).to_owned();
    }
    let Some(inner) = v.strip_prefix('"') else {
        return v.to_owned();
    };
    let mut out = String::with_capacity(inner.len());
    let mut chars = inner.chars();
    while let Some(c) = chars.next() {
        match c {
            '\\' => match chars.next() {
                Some(next) => out.push(next),
                None => out.push('\\'),
            },
            '"' => break,
            c => out.push(c),
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture(name: &str) -> OsInfo {
        let path = format!(
            "{}/tests/fixtures/os-release/{name}",
            env!("CARGO_MANIFEST_DIR")
        );
        OsInfo::from_os_release(&std::fs::read_to_string(&path).unwrap())
    }

    #[test]
    fn ubuntu_22_04() {
        let os = fixture("ubuntu-22.04");
        assert_eq!(os.id, "ubuntu");
        assert_eq!(os.id_like, ["debian"]);
        assert_eq!(os.version_id.as_deref(), Some("22.04"));
        assert_eq!(os.codename.as_deref(), Some("jammy"));
        assert!(os.is_ubuntu_like());
    }

    #[test]
    fn ubuntu_24_04() {
        let os = fixture("ubuntu-24.04");
        assert_eq!(os.id, "ubuntu");
        assert_eq!(os.version_id.as_deref(), Some("24.04"));
        assert_eq!(os.codename.as_deref(), Some("noble"));
        assert!(os.pretty_name.starts_with("Ubuntu 24.04"));
    }

    #[test]
    fn ubuntu_26_04() {
        let os = fixture("ubuntu-26.04");
        assert_eq!(os.id, "ubuntu");
        assert_eq!(os.version_id.as_deref(), Some("26.04"));
        assert_eq!(os.codename.as_deref(), Some("resolute"));
    }

    #[test]
    fn debian_12() {
        let os = fixture("debian-12");
        assert_eq!(os.id, "debian");
        assert!(os.id_like.is_empty());
        assert_eq!(os.version_id.as_deref(), Some("12"));
        assert_eq!(os.codename.as_deref(), Some("bookworm"));
        assert_eq!(os.pretty_name, "Debian GNU/Linux 12 (bookworm)");
        assert!(!os.is_ubuntu_like());
    }

    #[test]
    fn debian_13() {
        let os = fixture("debian-13");
        assert_eq!(os.id, "debian");
        assert_eq!(os.version_id.as_deref(), Some("13"));
        assert_eq!(os.codename.as_deref(), Some("trixie"));
    }

    #[test]
    fn raspberry_pi_os_64_bit_reports_debian() {
        let os = fixture("raspios-bookworm-arm64");
        assert_eq!(os.id, "debian");
        assert_eq!(os.codename.as_deref(), Some("bookworm"));
    }

    /// Proxmox VE ships Debian's os-release unchanged. That is expected: the apt
    /// provider detects binaries, not distros, so there is nothing to "fix" here.
    #[test]
    fn proxmox_ve_reports_plain_debian_by_design() {
        let os = fixture("proxmox-ve-8");
        assert_eq!(os.id, "debian");
        assert_eq!(os.pretty_name, "Debian GNU/Linux 12 (bookworm)");
    }

    #[test]
    fn quoting_and_comments() {
        let text = "# comment\n\nID=plain\nNAME='single \"q\"'\nPRETTY_NAME=\"a \\\"b\\\" \\$c \\\\ d\"\nID_LIKE=\"x  y\"\nBROKEN\n";
        let vars = parse_os_release(text);
        assert_eq!(vars["ID"], "plain");
        assert_eq!(vars["NAME"], "single \"q\"");
        assert_eq!(vars["PRETTY_NAME"], "a \"b\" $c \\ d");
        assert_eq!(OsInfo::from_os_release(text).id_like, ["x", "y"]);
        assert!(!vars.contains_key("BROKEN"));
    }

    #[test]
    fn empty_os_release_uses_spec_defaults() {
        let os = OsInfo::from_os_release("");
        assert_eq!(os.id, "linux");
        assert_eq!(os.pretty_name, "Linux");
    }

    #[test]
    fn reads_effective_uid() {
        let status = "Name:\tcat\nUid:\t1000\t0\t0\t0\nGid:\t1000\t1000\t1000\t1000\n";
        assert_eq!(effective_uid(status), Some(0));
        assert_eq!(effective_uid("Name: x\n"), None);
    }

    #[test]
    fn detects_container_markers() {
        let dir = std::env::temp_dir().join(format!("patchd-host-{}", std::process::id()));
        std::fs::create_dir_all(dir.join("proc/1")).unwrap();
        assert!(!in_container_at(&dir));
        std::fs::write(dir.join("proc/1/environ"), b"PATH=/bin\0container=podman\0").unwrap();
        assert!(in_container_at(&dir));
        std::fs::remove_dir_all(&dir).unwrap();
    }
}
