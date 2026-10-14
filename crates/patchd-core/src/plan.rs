//! A plan is a scan with a filter applied, frozen into a list of exact versions
//! (ADR 0009). `patchd plan` prints it; `patchd apply` installs exactly it.

use std::fmt;

use globset::{Glob, GlobSet, GlobSetBuilder};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

use crate::model::{ProviderId, Update, UpdateKind};

/// Which updates a plan selects.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum Filter {
    /// Security updates, plus the new packages they pull in.
    Security,
    /// Every pending update.
    All,
}

impl Filter {
    #[must_use]
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Security => "security",
            Self::All => "all",
        }
    }
}

impl fmt::Display for Filter {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

/// What `apply` will install, across providers.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct Plan {
    /// A `UUIDv7`, so plan IDs sort by creation time.
    pub id: uuid::Uuid,
    #[schemars(with = "String", extend("format" = "date-time"))]
    pub created_at: jiff::Timestamp,
    pub filter: Filter,
    pub providers: Vec<ProviderPlan>,
}

impl Plan {
    #[must_use]
    pub fn new(filter: Filter, providers: Vec<ProviderPlan>) -> Self {
        Self {
            id: uuid::Uuid::now_v7(),
            created_at: jiff::Timestamp::now(),
            filter,
            providers,
        }
    }

    /// Updates to install, across providers.
    pub fn updates(&self) -> impl Iterator<Item = &Update> {
        self.providers.iter().flat_map(|p| p.updates.iter())
    }

    #[must_use]
    pub fn is_empty(&self) -> bool {
        self.updates().next().is_none()
    }
}

/// One provider's part of a plan.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct ProviderPlan {
    pub id: ProviderId,
    /// Each pinned to its `to_version`.
    pub updates: Vec<Update>,
    /// Updates the filter selected but the plan leaves out: held or excluded.
    pub held_skipped: Vec<SkippedUpdate>,
    /// Packages installing the plan would remove. `apply` refuses unless
    /// `allow_removals` is set.
    pub removals: Vec<String>,
    /// Anything that will make `apply` abort or behave unexpectedly.
    pub warnings: Vec<String>,
}

impl ProviderPlan {
    #[must_use]
    pub fn empty(id: ProviderId) -> Self {
        Self {
            id,
            updates: Vec::new(),
            held_skipped: Vec::new(),
            removals: Vec::new(),
            warnings: Vec::new(),
        }
    }

    /// Whether any update was left out because of an exclusion glob.
    #[must_use]
    pub fn has_exclusions(&self) -> bool {
        self.held_skipped
            .iter()
            .any(|s| s.reason == SkipReason::Excluded)
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct SkippedUpdate {
    #[serde(flatten)]
    pub update: Update,
    pub reason: SkipReason,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum SkipReason {
    /// Pinned with `apt-mark hold` or `dnf versionlock`.
    Held,
    /// Matched an `apply.exclude` glob.
    Excluded,
}

impl SkipReason {
    #[must_use]
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Held => "held",
            Self::Excluded => "excluded",
        }
    }
}

/// The `apply.exclude` globs, compiled.
#[derive(Debug, Clone)]
pub struct Exclusions {
    set: GlobSet,
}

impl Exclusions {
    /// # Errors
    ///
    /// When a pattern is not a valid glob.
    pub fn new(patterns: &[String]) -> Result<Self, globset::Error> {
        let mut builder = GlobSetBuilder::new();
        for p in patterns {
            builder.add(Glob::new(p)?);
        }
        Ok(Self {
            set: builder.build()?,
        })
    }

    #[must_use]
    pub fn none() -> Self {
        Self {
            set: GlobSet::empty(),
        }
    }

    /// Matches the package name with and without its `:arch` suffix, so `libc6*` also
    /// excludes `libc6:i386`.
    #[must_use]
    pub fn matches(&self, name: &str) -> bool {
        let base = name.split_once(':').map_or(name, |(n, _)| n);
        self.set.is_match(name) || self.set.is_match(base)
    }
}

/// Applies the filter, holds and exclusions to a provider's available updates.
///
/// New dependencies (`from_version: None`) count under `All`. Under `Security` they are
/// left for the provider to add, because only a simulation of the security updates can
/// tell which new packages those pull in.
#[must_use]
pub fn select(
    id: ProviderId,
    updates: &[Update],
    filter: Filter,
    exclude: &Exclusions,
) -> ProviderPlan {
    let mut plan = ProviderPlan::empty(id);
    for u in updates {
        let wanted = match filter {
            Filter::All => true,
            Filter::Security => u.kind == UpdateKind::Security && u.from_version.is_some(),
        };
        if !wanted {
            continue;
        }
        let skip = if u.held {
            Some(SkipReason::Held)
        } else if exclude.matches(&u.name) {
            Some(SkipReason::Excluded)
        } else {
            None
        };
        match skip {
            Some(reason) => plan.held_skipped.push(SkippedUpdate {
                update: u.clone(),
                reason,
            }),
            None => plan.updates.push(u.clone()),
        }
    }
    plan
}

/// Package names that rebuild the initramfs or touch the boot loader, so `/boot` needs
/// room before they install.
#[must_use]
pub fn touches_boot(name: &str) -> bool {
    const PREFIXES: [&str; 12] = [
        "linux-image",
        "linux-modules",
        "linux-signed",
        "pve-kernel",
        "proxmox-kernel",
        "raspberrypi-kernel",
        "kernel",
        "initramfs-tools",
        "dracut",
        "grub",
        "shim",
        "systemd-boot",
    ];
    let name = name.split_once(':').map_or(name, |(n, _)| n);
    PREFIXES.iter().any(|p| name.starts_with(p))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn update(name: &str, kind: UpdateKind, from: Option<&str>, held: bool) -> Update {
        Update {
            provider: ProviderId::APT,
            name: name.to_owned(),
            arch: None,
            from_version: from.map(str::to_owned),
            to_version: "2.0".to_owned(),
            kind,
            origins: Vec::new(),
            held,
        }
    }

    fn sample() -> Vec<Update> {
        vec![
            update("libssl3", UpdateKind::Security, Some("1.0"), false),
            update("htop", UpdateKind::Major, Some("1.0"), false),
            update("base-files", UpdateKind::Minor, Some("1.0"), false),
            update("linux-image-6.1.0-27-amd64", UpdateKind::Minor, None, false),
            update("zfsutils-linux", UpdateKind::Security, Some("1.0"), true),
            update("pve-kernel-6.8", UpdateKind::Security, Some("1.0"), false),
            update("libc6:i386", UpdateKind::Minor, Some("1.0"), false),
        ]
    }

    fn names(us: &[Update]) -> Vec<&str> {
        us.iter().map(|u| u.name.as_str()).collect()
    }

    #[test]
    fn security_filter_takes_only_security_upgrades() {
        let plan = select(
            ProviderId::APT,
            &sample(),
            Filter::Security,
            &Exclusions::none(),
        );
        assert_eq!(names(&plan.updates), ["libssl3", "pve-kernel-6.8"]);
        // The held security update is reported, the held non-security ones are not.
        assert_eq!(plan.held_skipped.len(), 1);
        assert_eq!(plan.held_skipped[0].update.name, "zfsutils-linux");
        assert_eq!(plan.held_skipped[0].reason, SkipReason::Held);
    }

    #[test]
    fn all_filter_takes_everything_not_held_including_new_packages() {
        let plan = select(ProviderId::APT, &sample(), Filter::All, &Exclusions::none());
        assert_eq!(plan.updates.len(), 6);
        assert!(names(&plan.updates).contains(&"linux-image-6.1.0-27-amd64"));
        assert!(!names(&plan.updates).contains(&"zfsutils-linux"));
    }

    #[test]
    fn exclusions_are_skipped_with_a_reason() {
        let ex = Exclusions::new(&["pve-kernel-*".to_owned(), "libc6".to_owned()]).unwrap();
        let plan = select(ProviderId::APT, &sample(), Filter::All, &ex);
        let skipped: Vec<_> = plan
            .held_skipped
            .iter()
            .map(|s| (s.update.name.as_str(), s.reason))
            .collect();
        assert_eq!(
            skipped,
            [
                ("zfsutils-linux", SkipReason::Held),
                ("pve-kernel-6.8", SkipReason::Excluded),
                ("libc6:i386", SkipReason::Excluded),
            ]
        );
        assert!(plan.has_exclusions());
    }

    #[test]
    fn held_wins_over_excluded() {
        let ex = Exclusions::new(&["zfs*".to_owned()]).unwrap();
        let plan = select(ProviderId::APT, &sample(), Filter::Security, &ex);
        assert_eq!(plan.held_skipped[0].reason, SkipReason::Held);
        assert!(!plan.has_exclusions());
    }

    #[test]
    fn empty_input_is_an_empty_plan() {
        let plan = select(ProviderId::APT, &[], Filter::All, &Exclusions::none());
        assert!(plan.updates.is_empty() && plan.held_skipped.is_empty());
        assert!(Plan::new(Filter::All, vec![plan]).is_empty());
    }

    #[test]
    fn glob_syntax() {
        let ex = Exclusions::new(&["docker-ce*".to_owned(), "linux-image-?.*".to_owned()]).unwrap();
        assert!(ex.matches("docker-ce"));
        assert!(ex.matches("docker-ce-cli"));
        assert!(ex.matches("linux-image-6.1"));
        assert!(!ex.matches("linux-image-generic"));
        assert!(Exclusions::new(&["[".to_owned()]).is_err());
    }

    #[test]
    fn plan_round_trips_as_json() {
        let plan = Plan::new(
            Filter::Security,
            vec![select(
                ProviderId::APT,
                &sample(),
                Filter::Security,
                &Exclusions::none(),
            )],
        );
        let json = serde_json::to_string(&plan).unwrap();
        assert!(json.contains(r#""reason":"held""#), "{json}");
        let back: Plan = serde_json::from_str(&json).unwrap();
        assert_eq!(back, plan);
    }

    #[test]
    fn boot_packages() {
        for name in [
            "linux-image-6.1.0-27-amd64",
            "proxmox-kernel-6.8.12-5-pve-signed",
            "pve-kernel-6.2",
            "linux-image-rpi-v8",
            "initramfs-tools",
            "grub-efi-amd64-signed",
            "kernel-core",
        ] {
            assert!(touches_boot(name), "{name}");
        }
        for name in ["libssl3", "openssh-server", "linux-libc-dev"] {
            assert!(!touches_boot(name), "{name}");
        }
    }
}
