//! Runs the providers that apply to a host and assembles a [`ScanReport`].

use crate::build;
use crate::host::HostInfo;
use crate::model::{ProviderReport, SCHEMA_VERSION, ScanReport};
use crate::provider::Provider;

/// Scans the host with every provider that detects itself, or only with `only`.
///
/// A provider that fails still gets a report with its `error` set, so one broken source
/// never hides the others. Asking for a provider by name that does not apply here is an
/// error too: the caller asked for something the host cannot answer.
pub async fn scan(
    host: HostInfo,
    providers: &[Box<dyn Provider>],
    only: Option<&str>,
    in_container: bool,
) -> ScanReport {
    let mut reports = Vec::new();
    for provider in providers {
        let id = provider.id();
        if only.is_some_and(|only| only != id.as_str()) {
            continue;
        }
        if provider.detect(&host).await {
            reports.push(provider_report(provider.as_ref()).await);
        } else if only.is_some() {
            reports.push(ProviderReport {
                error: Some(format!("{id} is not available on this host")),
                ..empty_report(provider.as_ref())
            });
        }
    }

    let mut warnings = Vec::new();
    if in_container {
        warnings.push(
            "running inside a container; patching a container is almost never what you want, rebuild its image instead"
                .to_owned(),
        );
    }

    ScanReport {
        schema_version: SCHEMA_VERSION,
        agent_version: build::LONG_VERSION.to_owned(),
        scanned_at: jiff::Timestamp::now(),
        host,
        warnings,
        providers: reports,
    }
}

/// Runs one provider's inventory and available-updates queries.
pub async fn provider_report(provider: &dyn Provider) -> ProviderReport {
    let (inventory, available) = tokio::join!(provider.inventory(), provider.available());
    let mut report = empty_report(provider);
    let mut errors = Vec::new();

    match inventory {
        Ok(inv) => {
            report.packages = inv.packages;
            report.warnings.extend(inv.warnings);
        }
        Err(e) => errors.push(format!("inventory: {e}")),
    }
    match available {
        Ok(av) => {
            report.updates = av.updates;
            report.removals = av.removals;
            report.index_age_secs = av.index_age_secs;
            report.warnings.extend(av.warnings);
        }
        Err(e) => errors.push(format!("available updates: {e}")),
    }
    if !errors.is_empty() {
        tracing::debug!(provider = %report.id, errors = ?errors, "provider failed");
        report.error = Some(errors.join("; "));
    }
    report
}

fn empty_report(provider: &dyn Provider) -> ProviderReport {
    ProviderReport {
        id: provider.id(),
        packages: Vec::new(),
        updates: Vec::new(),
        removals: Vec::new(),
        index_age_secs: None,
        warnings: Vec::new(),
        error: None,
    }
}
