//! The dnf provider for Fedora (dnf5) and RHEL, `AlmaLinux` and Rocky Linux (dnf4).
//!
//! The two generations' human output differs, so everything read-only goes through
//! `rpm -qa --qf` and `dnf repoquery --qf`, which both support. Apply uses the same three
//! passes as apt (ADR 0009): simulate with `--assumeno`, download with `--downloadonly`,
//! then install, always naming the planned NEVRAs.

mod parse;

use std::path::Path;
use std::sync::Arc;
use std::time::{Duration, Instant};

use async_trait::async_trait;

pub use parse::{Generation, generation, nevra};

use crate::apply::{ApplyCtx, ApplyReport, Change, RunResult, inhibited, step};
use crate::host::HostInfo;
use crate::model::{ProviderId, Update};
use crate::plan::{Exclusions, Filter, ProviderPlan, select};
use crate::preflight::Check;
use crate::provider::{Available, Inventory, Provider, ProviderError};
use crate::reboot::{self, RebootStatus};
use crate::root::Root;
use crate::runlog::{Event, EventSink};
use crate::runner::{Cmd, CmdOutput, FixtureRunner, RESOLVE_TIMEOUT, Runner};

use parse::{TxPackage, dnf_id, parse_repoquery, parse_rpm_qa, parse_transaction, same_evr};

const REFRESH_TIMEOUT: Duration = Duration::from_secs(300);
const DOWNLOAD_TIMEOUT: Duration = Duration::from_secs(30 * 60);
const INSTALL_TIMEOUT: Duration = Duration::from_secs(60 * 60);

pub struct DnfProvider {
    runner: Arc<dyn Runner>,
    check_binaries: bool,
    root: Root,
    /// Found on first use with `dnf --version`.
    generation: tokio::sync::OnceCell<Generation>,
}

impl DnfProvider {
    #[must_use]
    pub fn new(runner: Arc<dyn Runner>) -> Self {
        Self {
            runner,
            check_binaries: true,
            root: Root::system(),
            generation: tokio::sync::OnceCell::new(),
        }
    }

    /// A provider driven by an arbitrary runner, for tests.
    #[must_use]
    pub fn with_runner(runner: Arc<dyn Runner>) -> Self {
        Self {
            check_binaries: false,
            ..Self::new(runner)
        }
    }

    #[must_use]
    pub fn with_root(mut self, root: Root) -> Self {
        self.root = root;
        self
    }

    /// A provider that replays a fixture directory written by
    /// `scripts/capture-dnf-fixtures.sh`.
    ///
    /// # Errors
    ///
    /// When a fixture file cannot be read.
    pub fn from_fixtures(dir: &Path) -> std::io::Result<(Self, Arc<FixtureRunner>)> {
        let read = |f: &str| std::fs::read_to_string(dir.join(f));
        let runner = Arc::new(
            FixtureRunner::new()
                .with(
                    version_cmd().command_line(),
                    CmdOutput::ok(read("dnf-version.txt")?),
                )
                .with(
                    rpm_qa_cmd().command_line(),
                    CmdOutput::ok(read("rpm-qa.txt")?),
                )
                .with(
                    repoquery_cmd(false).command_line(),
                    CmdOutput::ok(read("repoquery-upgrades.txt")?),
                )
                .with(
                    repoquery_cmd(true).command_line(),
                    CmdOutput::ok(read("repoquery-security.txt")?),
                ),
        );
        Ok((Self::with_runner(runner.clone()), runner))
    }

    /// dnf4 or dnf5.
    ///
    /// # Errors
    ///
    /// When `dnf --version` fails.
    pub async fn generation(&self) -> Result<Generation, ProviderError> {
        self.generation
            .get_or_try_init(|| async {
                let out = self.runner.run(&version_cmd()).await?;
                Ok::<_, ProviderError>(generation(&out.stdout))
            })
            .await
            .copied()
    }
}

fn version_cmd() -> Cmd {
    Cmd::new("dnf", ["--version"])
}

fn rpm_qa_cmd() -> Cmd {
    Cmd::new("rpm", ["-qa", "--qf", parse::RPM_QF])
}

fn repoquery_cmd(security: bool) -> Cmd {
    let mut args = vec!["-q", "repoquery", "--upgrades"];
    if security {
        args.push("--security");
    }
    args.extend(["--latest-limit=1", "--qf", parse::REPOQUERY_QF]);
    Cmd::new("dnf", args).timeout(RESOLVE_TIMEOUT)
}

fn nevras(updates: &[Update]) -> Vec<String> {
    updates
        .iter()
        .filter(|u| u.from_version.is_some())
        .map(|u| {
            nevra(
                &u.name,
                &u.to_version,
                u.arch.as_deref().unwrap_or("noarch"),
            )
        })
        .collect()
}

fn simulate_cmd(updates: &[Update]) -> Cmd {
    // `--assumeno` prints the transaction and declines it, exiting 1.
    Cmd::new(
        "dnf",
        ["upgrade".to_owned(), "--assumeno".to_owned()]
            .into_iter()
            .chain(nevras(updates)),
    )
    .ok_codes(&[0, 1])
    .timeout(RESOLVE_TIMEOUT)
}

fn download_cmd(updates: &[Update]) -> Cmd {
    Cmd::new(
        "dnf",
        ["-y", "upgrade", "--downloadonly", "--setopt=keepcache=1"]
            .into_iter()
            .map(str::to_owned)
            .chain(nevras(updates)),
    )
    .timeout(DOWNLOAD_TIMEOUT)
}

fn install_cmd(updates: &[Update]) -> Cmd {
    Cmd::new(
        "dnf",
        ["-y", "-q", "upgrade", "--setopt=keepcache=1"]
            .into_iter()
            .map(str::to_owned)
            .chain(nevras(updates)),
    )
    .timeout(INSTALL_TIMEOUT)
    .protected()
}

/// `rpm -q` exits with the number of packages that are not installed.
fn verify_cmd(updates: &[Update]) -> Cmd {
    let all: Vec<String> = updates
        .iter()
        .map(|u| {
            nevra(
                &u.name,
                &u.to_version,
                u.arch.as_deref().unwrap_or("noarch"),
            )
        })
        .collect();
    Cmd::new("rpm", std::iter::once("-q".to_owned()).chain(all)).ok_codes(&ANY_EXIT_CODE)
}

/// rpm -q exits with the number of missing packages, so any count is an answer.
const ANY_EXIT_CODE: [i32; 256] = {
    let mut codes = [0; 256];
    let mut i = 0;
    while i < codes.len() {
        #[allow(clippy::cast_possible_truncation, clippy::cast_possible_wrap)] // i < 256
        {
            codes[i] = i as i32;
        }
        i += 1;
    }
    codes
};

fn needs_restarting_cmd() -> Cmd {
    // Exit 1 means a reboot is needed; dnf4 needs dnf-plugins-core for it.
    Cmd::new("dnf", ["needs-restarting", "-r"]).ok_codes(&[0, 1])
}

/// Compares a transaction with the plan: every change must be a planned package at its
/// planned version, or a new package that is not excluded.
fn check_transaction(
    tx: &parse::Transaction,
    planned: &[Update],
    exclude: &Exclusions,
) -> (Vec<TxPackage>, Vec<String>) {
    let mut new_deps = Vec::new();
    let mut mismatches = Vec::new();
    let is = |u: &Update, p: &TxPackage| {
        u.name == p.name && u.arch.as_deref().is_none_or(|a| a == p.arch)
    };
    for p in &tx.changes {
        match planned.iter().find(|u| is(u, p)) {
            Some(u) if same_evr(&u.to_version, &p.evr) => {}
            Some(u) => mismatches.push(format!(
                "{} would install {} instead of the planned {}",
                p.name, p.evr, u.to_version
            )),
            None if exclude.matches(&p.name) => mismatches.push(format!(
                "{} is excluded, but the transaction would change it",
                p.name
            )),
            None if p.new => new_deps.push(p.clone()),
            None => mismatches.push(format!(
                "{} would be upgraded to {}, which is not in the plan",
                p.name, p.evr
            )),
        }
    }
    for u in planned {
        if !tx.changes.iter().any(|p| is(u, p)) {
            mismatches.push(format!(
                "{} {} is planned, but the transaction would not change it",
                u.name, u.to_version
            ));
        }
    }
    (new_deps, mismatches)
}

#[async_trait]
impl Provider for DnfProvider {
    fn id(&self) -> ProviderId {
        dnf_id()
    }

    async fn detect(&self, _host: &HostInfo) -> bool {
        !self.check_binaries || (on_path("dnf") && on_path("rpm"))
    }

    async fn inventory(&self) -> Result<Inventory, ProviderError> {
        let out = self.runner.run(&rpm_qa_cmd()).await?;
        Ok(Inventory {
            packages: parse_rpm_qa(&out.stdout)?,
            warnings: Vec::new(),
        })
    }

    async fn available(&self) -> Result<Available, ProviderError> {
        // One after another: two dnf processes at once contend for the metadata cache.
        let installed = self.runner.run(&rpm_qa_cmd()).await;
        let upgrades = self.runner.run(&repoquery_cmd(false)).await;
        let security = self.runner.run(&repoquery_cmd(true)).await;
        let installed = parse_rpm_qa(&installed?.stdout)?;
        let upgrades = parse_repoquery(&upgrades?.stdout)?;
        let mut warnings = Vec::new();
        let security = match security {
            Ok(out) => parse_repoquery(&out.stdout)?,
            Err(e) => {
                warnings.push(format!(
                    "could not list security updates, so none is marked security: {e}"
                ));
                Vec::new()
            }
        };
        Ok(Available {
            updates: parse::updates(&installed, &upgrades, &security),
            removals: Vec::new(),
            index_age_secs: None,
            warnings,
        })
    }

    async fn refresh(&self, events: &dyn EventSink) -> Result<(), ProviderError> {
        let cmd = Cmd::new("dnf", ["-q", "makecache", "--refresh"]).timeout(REFRESH_TIMEOUT);
        step(events, self.runner.as_ref(), "refresh", &cmd).await?;
        Ok(())
    }

    /// The install names the planned NEVRAs, so a simulation of exactly that says which
    /// new packages it pulls in and whether it needs anything outside the plan.
    async fn plan(
        &self,
        filter: Filter,
        exclude: &Exclusions,
    ) -> Result<ProviderPlan, ProviderError> {
        let available = self.available().await?;
        let mut plan = select(self.id(), &available.updates, filter, exclude);
        plan.warnings = available.warnings;
        if plan.updates.is_empty() {
            return Ok(plan);
        }
        let out = self.runner.run(&simulate_cmd(&plan.updates)).await?;
        let tx = parse_transaction(&out.stdout);
        let (new_deps, mismatches) = check_transaction(&tx, &plan.updates, exclude);
        plan.updates.extend(new_deps.into_iter().map(|p| Update {
            provider: dnf_id(),
            name: p.name,
            arch: Some(p.arch),
            from_version: None,
            to_version: p.evr,
            kind: crate::model::UpdateKind::Minor,
            origins: Vec::new(),
            held: false,
        }));
        plan.removals = tx.removals;
        plan.warnings.extend(
            mismatches
                .into_iter()
                .map(|m| format!("apply will abort: {m}")),
        );
        Ok(plan)
    }

    async fn preflight(&self, plan: &ProviderPlan, ctx: &ApplyCtx<'_>) -> Vec<Check> {
        if plan.updates.is_empty() {
            return Vec::new();
        }
        let mut checks = Vec::new();
        let tx = match step(
            ctx.events,
            self.runner.as_ref(),
            "simulate",
            &simulate_cmd(&plan.updates),
        )
        .await
        {
            Ok(out) => parse_transaction(&out.stdout),
            Err(e) => {
                checks.push(Check::fail(
                    "simulate",
                    format!("the simulation failed: {}", e.brief()),
                ));
                return checks;
            }
        };
        let (_, mismatches) = check_transaction(&tx, &plan.updates, ctx.exclude);
        checks.push(if mismatches.is_empty() {
            Check::pass(
                "simulate",
                format!(
                    "the transaction matches the plan ({} packages)",
                    plan.updates.len()
                ),
            )
        } else {
            Check::fail("simulate", mismatches.join("; "))
        });
        let removals = tx.removals.join(", ");
        checks.push(match (tx.removals.is_empty(), ctx.allow_removals) {
            (true, _) => Check::pass("removals", "nothing would be removed"),
            (false, true) => Check::warn(
                "removals",
                format!("would remove {removals} (allow_removals = true)"),
            ),
            (false, false) => Check::fail(
                "removals",
                format!("would remove {removals}; set allow_removals = true to allow it"),
            ),
        });
        checks
    }

    async fn apply(
        &self,
        plan: &ProviderPlan,
        ctx: &ApplyCtx<'_>,
    ) -> Result<ApplyReport, ProviderError> {
        let started = Instant::now();
        let runner = self.runner.as_ref();
        let events = ctx.events;
        let mut report = ApplyReport::new(RunResult::Success);
        report.skipped.clone_from(&plan.held_skipped);

        if let Err(e) = step(events, runner, "download", &download_cmd(&plan.updates)).await {
            report.result = RunResult::Failed;
            report.message = Some(format!("download failed, nothing installed: {}", e.brief()));
            return Ok(report);
        }
        if ctx.is_interrupted() {
            report.result = RunResult::Interrupted;
            report.message = Some("interrupted after the download; nothing installed".to_owned());
            return Ok(report);
        }
        let mut install = install_cmd(&plan.updates);
        if ctx.inhibit {
            install = inhibited(install);
        }
        let install = step(events, runner, "install", &install).await;
        // dnf4 treats a failed scriptlet as a warning and exits 0; dnf5 fails the
        // transaction. Either way the package may not be set up, so it is not a success.
        let scriptlet_errors = match &install {
            Ok(out) => scriptlet_failures(&out.stdout, &out.stderr),
            Err(crate::runner::RunError::Failed { stderr, .. }) => scriptlet_failures("", stderr),
            Err(_) => Vec::new(),
        };
        let install_error = install.err();

        let missing: Vec<String> =
            match step(events, runner, "verify", &verify_cmd(&plan.updates)).await {
                Ok(out) => out
                    .stdout
                    .lines()
                    .filter_map(|l| {
                        l.strip_prefix("package ")?
                            .strip_suffix(" is not installed")
                    })
                    .map(str::to_owned)
                    .collect(),
                Err(e) => vec![format!("rpm -q failed: {}", e.brief())],
            };
        for u in &plan.updates {
            let change = Change {
                name: u.name.clone(),
                from: u.from_version.clone(),
                to: u.to_version.clone(),
            };
            let nevra = nevra(
                &u.name,
                &u.to_version,
                u.arch.as_deref().unwrap_or("noarch"),
            );
            if missing
                .iter()
                .any(|m| *m == nevra || m.starts_with("rpm -q failed"))
            {
                report.failed.push(change);
            } else {
                report.installed.push(change);
            }
        }
        events.emit(Event::Verify {
            ok: report.failed.is_empty(),
            missed: report.failed.clone(),
            audit: Vec::new(),
        });

        let interrupted = ctx.is_interrupted();
        if interrupted {
            report.result = RunResult::Interrupted;
            report.message = Some("interrupted; the install was allowed to finish".to_owned());
        }
        // A failed install outranks the interruption: it is what needs attention.
        if install_error.is_some() || !report.failed.is_empty() || !scriptlet_errors.is_empty() {
            report.result = RunResult::Partial;
            let mut why = Vec::new();
            if let Some(e) = install_error {
                why.push(format!("install failed: {}", e.brief()));
            }
            if !scriptlet_errors.is_empty() {
                why.push(scriptlet_errors.join("; "));
            }
            if !report.failed.is_empty() {
                why.push(format!(
                    "not at the planned version: {}",
                    report
                        .failed
                        .iter()
                        .map(|c| c.name.as_str())
                        .collect::<Vec<_>>()
                        .join(", ")
                ));
            }
            if interrupted {
                why.push("then interrupted".to_owned());
            }
            report.message = Some(why.join("; "));
        }
        report.duration_ms = u64::try_from(started.elapsed().as_millis()).unwrap_or(u64::MAX);
        Ok(report)
    }

    /// `dnf needs-restarting -r`, falling back to the `/lib/modules` comparison when the
    /// command is missing (dnf4 without dnf-plugins-core).
    async fn reboot_required(&self) -> Result<RebootStatus, ProviderError> {
        match self.runner.run(&needs_restarting_cmd()).await {
            Ok(out)
                if out.status == 1
                    && out
                        .stdout
                        .to_ascii_lowercase()
                        .contains("reboot is required") =>
            {
                let mut status = RebootStatus::default();
                status.add_reason("dnf needs-restarting: core libraries or services were updated");
                Ok(status)
            }
            Ok(out) if out.status == 0 => Ok(RebootStatus::default()),
            _ => Ok(reboot::kernel_check(&self.root)),
        }
    }
}

/// `%post(pt-fail-2-1.noarch) scriptlet failed, exit status 1`, from rpm in both dnf
/// generations' output, deduplicated.
fn scriptlet_failures(stdout: &str, stderr: &str) -> Vec<String> {
    let mut out: Vec<String> = Vec::new();
    for line in stdout.lines().chain(stderr.lines()) {
        // rpm: `%post(pt-fail-2-1.noarch) scriptlet failed, exit status 1`; dnf4 with
        // `-q`: `Error in POSTIN scriptlet in rpm package pt-fail`.
        let msg = match line.find('%') {
            Some(i) if line.contains("scriptlet failed") => line[i..].trim(),
            _ if line.starts_with("Error in ") && line.contains(" scriptlet in rpm package ") => {
                line.trim()
            }
            _ => continue,
        };
        if !out.iter().any(|m| m == msg) {
            out.push(msg.to_owned());
        }
    }
    out
}

fn on_path(program: &str) -> bool {
    std::env::var_os("PATH")
        .is_some_and(|path| std::env::split_paths(&path).any(|dir| dir.join(program).is_file()))
}

#[cfg(test)]
mod tests;
