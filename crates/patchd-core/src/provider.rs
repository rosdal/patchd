//! The `Provider` trait every update source implements (ADR 0002).

use async_trait::async_trait;

use crate::apply::{ApplyCtx, ApplyReport};
use crate::host::HostInfo;
use crate::model::{Package, ProviderId, Update};
use crate::plan::{Exclusions, Filter, ProviderPlan, select};
use crate::preflight::Check;
use crate::reboot::RebootStatus;
use crate::runlog::EventSink;
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

    /// Refreshes the package lists. Needs root; the caller checks. Each command it runs
    /// is logged to `events`.
    async fn refresh(&self, _events: &dyn EventSink) -> Result<(), ProviderError> {
        Ok(())
    }

    /// The available updates with the filter, holds and exclusions applied. Read-only:
    /// `patchd plan` is this, without root.
    async fn plan(
        &self,
        filter: Filter,
        exclude: &Exclusions,
    ) -> Result<ProviderPlan, ProviderError> {
        let available = self.available().await?;
        let mut plan = select(self.id(), &available.updates, filter, exclude);
        plan.removals = available.removals;
        Ok(plan)
    }

    /// Provider-specific pre-flight checks, run just before [`Provider::apply`]: package
    /// database health and a simulation of the exact install. Runs even when the plan is
    /// empty, so a broken package database never looks like a quiet success. Nothing
    /// here may change installed packages, except the repair the config explicitly
    /// allows.
    async fn preflight(&self, _plan: &ProviderPlan, _ctx: &ApplyCtx<'_>) -> Vec<Check> {
        Vec::new()
    }

    /// Downloads and installs exactly `plan`, then verifies the result.
    async fn apply(
        &self,
        plan: &ProviderPlan,
        ctx: &ApplyCtx<'_>,
    ) -> Result<ApplyReport, ProviderError>;

    /// Whether the host needs a reboot, and why.
    async fn reboot_required(&self) -> Result<RebootStatus, ProviderError>;
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
