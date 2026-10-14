//! The `Provider` trait every update source implements (ADR 0002).

use async_trait::async_trait;

use crate::host::HostInfo;
use crate::model::{Package, ProviderId, Update};
use crate::runner::RunError;

#[async_trait]
pub trait Provider: Send + Sync {
    fn id(&self) -> ProviderId;

    /// Whether this provider applies to the host.
    async fn detect(&self, host: &HostInfo) -> bool;

    /// What is installed.
    async fn inventory(&self) -> Result<Inventory, ProviderError>;

    /// What can be updated.
    async fn available(&self) -> Result<Available, ProviderError>;
}

/// The result of [`Provider::inventory`].
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Inventory {
    pub packages: Vec<Package>,
    /// Problems worth surfacing, such as packages in a broken dpkg state.
    pub warnings: Vec<String>,
}

/// The result of [`Provider::available`].
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Available {
    pub updates: Vec<Update>,
    pub removals: Vec<String>,
    pub index_age_secs: Option<u64>,
    pub warnings: Vec<String>,
}

#[derive(Debug, thiserror::Error)]
pub enum ProviderError {
    #[error(transparent)]
    Command(#[from] RunError),
    /// `line` is 1-based, so a bug report can point at the exact output line.
    #[error("cannot parse output line {line}: {msg}")]
    Parse { line: usize, msg: String },
    #[error("unsupported: {0}")]
    Unsupported(String),
}
