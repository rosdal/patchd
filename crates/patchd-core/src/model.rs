//! The scan report: what the CLI prints with `--json`, and what the agent will send to the
//! server from v0.6. Field names are a public contract; see `docs/protocol.md`.

use std::borrow::Cow;
use std::fmt;

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

use crate::host::HostInfo;

/// The current [`ScanReport::schema_version`].
pub const SCHEMA_VERSION: u32 = 1;

/// Identifies a provider, serialized as a plain string such as `"apt"`.
#[derive(
    Debug, Clone, PartialEq, Eq, Hash, PartialOrd, Ord, Serialize, Deserialize, JsonSchema,
)]
#[serde(transparent)]
pub struct ProviderId(Cow<'static, str>);

impl ProviderId {
    pub const APT: Self = Self(Cow::Borrowed("apt"));

    #[must_use]
    pub fn new(id: impl Into<String>) -> Self {
        Self(Cow::Owned(id.into()))
    }

    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for ProviderId {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

/// An installed package.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct Package {
    pub provider: ProviderId,
    /// Package name; multi-arch copies keep their suffix (`libc6:i386`).
    pub name: String,
    pub version: String,
    /// `amd64`, `arm64`, `all`.
    pub arch: Option<String>,
    /// Pinned by `apt-mark hold`.
    pub held: bool,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum UpdateKind {
    Security,
    Minor,
    Major,
}

impl UpdateKind {
    #[must_use]
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Security => "security",
            Self::Minor => "minor",
            Self::Major => "major",
        }
    }
}

/// An update the provider can install.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct Update {
    pub provider: ProviderId,
    pub name: String,
    pub arch: Option<String>,
    /// `None` when the update pulls in a new package, typically a new kernel.
    pub from_version: Option<String>,
    pub to_version: String,
    pub kind: UpdateKind,
    /// Where the new version comes from, such as `Ubuntu:24.04/noble-security`.
    pub origins: Vec<String>,
    /// Available, but pinned by `apt-mark hold`, so an upgrade will skip it.
    pub held: bool,
}

/// One scan of one host.
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct ScanReport {
    pub schema_version: u32,
    /// The `patchd` build that produced the report.
    pub agent_version: String,
    #[schemars(with = "String", extend("format" = "date-time"))]
    pub scanned_at: jiff::Timestamp,
    pub host: HostInfo,
    /// Host-level warnings, such as running inside a container.
    pub warnings: Vec<String>,
    pub providers: Vec<ProviderReport>,
}

/// What one provider found.
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct ProviderReport {
    pub id: ProviderId,
    pub packages: Vec<Package>,
    pub updates: Vec<Update>,
    /// Packages the upgrade would remove.
    pub removals: Vec<String>,
    /// Seconds since the package lists were last refreshed.
    pub index_age_secs: Option<u64>,
    pub warnings: Vec<String>,
    /// Set when the provider failed; the other providers still report.
    pub error: Option<String>,
}

impl ScanReport {
    /// All updates across providers.
    pub fn updates(&self) -> impl Iterator<Item = &Update> {
        self.providers.iter().flat_map(|p| p.updates.iter())
    }

    #[must_use]
    pub fn has_errors(&self) -> bool {
        self.providers.iter().any(|p| p.error.is_some())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn provider_id_serializes_as_plain_string() {
        assert_eq!(serde_json::to_string(&ProviderId::APT).unwrap(), r#""apt""#);
        let back: ProviderId = serde_json::from_str(r#""apt""#).unwrap();
        assert_eq!(back, ProviderId::APT);
    }

    #[test]
    fn update_kind_is_snake_case() {
        assert_eq!(
            serde_json::to_string(&UpdateKind::Security).unwrap(),
            r#""security""#
        );
    }
}
