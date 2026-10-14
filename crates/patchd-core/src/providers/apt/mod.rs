//! The apt provider for Debian, Ubuntu and derivatives (ADR 0006).
//!
//! Scanning and planning are read-only: everything comes from `dpkg-query` and
//! `apt-get -s`, which need neither root nor the dpkg lock. `refresh` writes the package
//! lists; `apply` (in `apply.rs`) installs.

mod apply;
mod available;
mod inventory;

use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, SystemTime};

use async_trait::async_trait;

pub use available::classify;

use crate::apply::{ApplyCtx, ApplyReport, step};
use crate::host::HostInfo;
use crate::model::{ProviderId, Update};
use crate::plan::{Exclusions, Filter, ProviderPlan, select};
use crate::preflight::Check;
use crate::provider::{Available, Inventory, Provider, ProviderError};
use crate::reboot::{self, RebootStatus};
use crate::root::Root;
use crate::runlog::EventSink;
use crate::runner::{Cmd, CmdOutput, FixtureRunner, RESOLVE_TIMEOUT, Runner};

/// Where `apt-get update` stores the package lists.
pub const LISTS_DIR: &str = "/var/lib/apt/lists";

/// Package lists older than this get a warning.
const STALE_AFTER: Duration = Duration::from_secs(24 * 60 * 60);

/// `apt-get update` downloads, so it gets longer than the resolver.
const REFRESH_TIMEOUT: Duration = Duration::from_secs(300);

pub struct AptProvider {
    runner: Arc<dyn Runner>,
    lists_dir: Option<PathBuf>,
    check_binaries: bool,
    ubuntu: bool,
    root: Root,
}

impl AptProvider {
    /// A provider for the local system.
    #[must_use]
    pub fn new(runner: Arc<dyn Runner>) -> Self {
        Self {
            runner,
            lists_dir: Some(PathBuf::from(LISTS_DIR)),
            check_binaries: true,
            ubuntu: false,
            root: Root::system(),
        }
    }

    /// Whether the host is Ubuntu-like, which changes how held-back packages are
    /// explained (phased updates).
    #[must_use]
    pub fn ubuntu(mut self, ubuntu: bool) -> Self {
        self.ubuntu = ubuntu;
        self
    }

    /// A provider that replays the files in a fixture directory (see
    /// `tests/fixtures/apt/README.md`) instead of running commands.
    ///
    /// # Errors
    ///
    /// When a required fixture file cannot be read.
    pub fn from_fixtures(dir: &Path) -> std::io::Result<(Self, Arc<FixtureRunner>)> {
        let runner = Arc::new(fixture_runner(dir)?);
        let provider = Self {
            runner: runner.clone(),
            lists_dir: None,
            check_binaries: false,
            ubuntu: false,
            root: Root::system(),
        };
        Ok((provider, runner))
    }

    /// A provider that reads system files (`/run/reboot-required`, `/lib/modules`) under
    /// `root` instead of `/`.
    #[must_use]
    pub fn with_root(mut self, root: Root) -> Self {
        self.root = root;
        self
    }

    /// A provider driven by an arbitrary runner, for tests.
    #[must_use]
    pub fn with_runner(runner: Arc<dyn Runner>) -> Self {
        Self {
            runner,
            lists_dir: None,
            check_binaries: false,
            ubuntu: false,
            root: Root::system(),
        }
    }

    /// Held packages that have a newer candidate, as `held: true` updates, plus the
    /// names of everything held.
    async fn held_updates(&self) -> Result<(Vec<String>, Vec<Update>), ProviderError> {
        let held = parse_held(&self.runner.run(&showhold_cmd()).await?.stdout);
        if held.is_empty() {
            return Ok((held, Vec::new()));
        }
        let policy = self.runner.run(&policy_cmd(&held)).await?;
        let updates = available::held_updates(available::parse_policy(&policy.stdout));
        Ok((held, updates))
    }
}

/// `apt-mark showhold` prints one package per line.
fn parse_held(stdout: &str) -> Vec<String> {
    stdout.split_whitespace().map(str::to_owned).collect()
}

fn dpkg_query_cmd() -> Cmd {
    Cmd::new("dpkg-query", ["-W", inventory::FORMAT])
}

/// `apt-get update`. Without `Error-Mode=any` a broken repository only warns, and the
/// plan silently misses its updates.
fn refresh_cmd() -> Cmd {
    Cmd::new(
        "apt-get",
        ["update", "-q", "-o", "APT::Update::Error-Mode=any"],
    )
    .timeout(REFRESH_TIMEOUT)
}

fn simulate_cmd() -> Cmd {
    Cmd::new("apt-get", ["-s", "-q", "dist-upgrade"]).timeout(RESOLVE_TIMEOUT)
}

fn showhold_cmd() -> Cmd {
    Cmd::new("apt-mark", ["showhold"])
}

fn policy_cmd(held: &[String]) -> Cmd {
    Cmd::new(
        "apt-cache",
        std::iter::once("policy".to_owned()).chain(held.iter().cloned()),
    )
}

#[async_trait]
impl Provider for AptProvider {
    fn id(&self) -> ProviderId {
        ProviderId::APT
    }

    /// Checks for the binaries rather than the distro, so derivatives (Mint, Pop!_OS,
    /// Proxmox VE) work without a list.
    async fn detect(&self, _host: &HostInfo) -> bool {
        !self.check_binaries || (on_path("dpkg-query") && on_path("apt-get"))
    }

    async fn inventory(&self) -> Result<Inventory, ProviderError> {
        let out = self.runner.run(&dpkg_query_cmd()).await?;
        inventory::parse(&out.stdout)
    }

    async fn available(&self) -> Result<Available, ProviderError> {
        let simulate = simulate_cmd();
        let (sim, held) = tokio::join!(self.runner.run(&simulate), self.held_updates());
        let sim = available::parse_simulation(&sim?.stdout)?;

        let mut warnings = Vec::new();
        let mut updates = sim.updates;
        // Held packages only add to the count, so failing to list them is a warning, not a
        // reason to throw away the simulation.
        let held = match held {
            Ok((held, held_updates)) => {
                for u in held_updates {
                    if !updates.iter().any(|x| x.name == u.name) {
                        updates.push(u);
                    }
                }
                held
            }
            Err(e) => {
                warnings.push(format!("could not check held packages: {e}"));
                Vec::new()
            }
        };

        if !sim.removals.is_empty() {
            warnings.push(format!(
                "dist-upgrade would remove {} package(s): {}; check the repository setup before upgrading (on Proxmox VE this often means a missing or wrong repository)",
                sim.removals.len(),
                inventory::name_list(&sim.removals)
            ));
        }
        if !sim.phased.is_empty() {
            warnings.push(format!(
                "{} update(s) deferred by Ubuntu's phased rollout: {}",
                sim.phased.len(),
                inventory::name_list(&sim.phased)
            ));
        }
        let kept_back: Vec<String> = sim
            .kept_back
            .into_iter()
            .filter(|name| !held.contains(name))
            .collect();
        if !kept_back.is_empty() {
            let why = if self.ubuntu {
                "most likely Ubuntu phased updates"
            } else {
                "check `apt-get -s dist-upgrade` for why"
            };
            warnings.push(format!(
                "{} package(s) kept back by apt ({why}): {}",
                kept_back.len(),
                inventory::name_list(&kept_back)
            ));
        }

        let index_age_secs = match &self.lists_dir {
            Some(dir) => {
                let age = lists_age(dir);
                match age {
                    None => warnings.push(format!(
                        "no package lists in {}; run `apt-get update`",
                        dir.display()
                    )),
                    Some(age) if age > STALE_AFTER => warnings.push(format!(
                        "package lists are {}h old; run `sudo patchd scan --refresh` or `apt-get update`",
                        age.as_secs() / 3600
                    )),
                    Some(_) => {}
                }
                age.map(|a| a.as_secs())
            }
            None => None,
        };

        Ok(Available {
            updates,
            removals: sim.removals,
            index_age_secs,
            warnings,
        })
    }

    async fn refresh(&self, events: &dyn EventSink) -> Result<(), ProviderError> {
        step(events, self.runner.as_ref(), "refresh", &refresh_cmd()).await?;
        Ok(())
    }

    /// Under the security filter, or with anything excluded, the install names the
    /// planned packages, so only a simulation of that command can say which new
    /// packages it pulls in, and whether it needs anything outside the plan.
    async fn plan(
        &self,
        filter: Filter,
        exclude: &Exclusions,
    ) -> Result<ProviderPlan, ProviderError> {
        let available = self.available().await?;
        let mut plan = select(self.id(), &available.updates, filter, exclude);
        let mode = apply::Mode::for_plan(filter, &plan);
        if mode == apply::Mode::DistUpgrade {
            plan.removals = available.removals;
            return Ok(plan);
        }
        plan.updates.retain(|u| u.from_version.is_some());
        if plan.updates.is_empty() {
            return Ok(plan);
        }
        let out = self
            .runner
            .run(&apply::simulate_install_cmd(mode, &plan.updates))
            .await?;
        let sim = available::parse_simulation(&out.stdout)?;
        let check = apply::check_simulation(&sim, &plan.updates, exclude);
        plan.updates.extend(check.new_deps);
        plan.removals = check.removals;
        for m in check.mismatches {
            plan.warnings.push(format!("apply will abort: {m}"));
        }
        Ok(plan)
    }

    async fn preflight(&self, plan: &ProviderPlan, ctx: &ApplyCtx<'_>) -> Vec<Check> {
        apply::preflight(self.runner.as_ref(), plan, ctx).await
    }

    async fn apply(
        &self,
        plan: &ProviderPlan,
        ctx: &ApplyCtx<'_>,
    ) -> Result<ApplyReport, ProviderError> {
        let started = std::time::Instant::now();
        let mut report = apply::apply(self.runner.as_ref(), plan, ctx).await;
        report.duration_ms = u64::try_from(started.elapsed().as_millis()).unwrap_or(u64::MAX);
        Ok(report)
    }

    /// `/run/reboot-required`, needrestart when installed, and the running kernel
    /// against `/lib/modules`.
    async fn reboot_required(&self) -> Result<RebootStatus, ProviderError> {
        let mut status = reboot::reboot_required_file(&self.root);
        status.merge(reboot::needrestart(&self.root, self.runner.as_ref()).await);
        status.merge(reboot::kernel_check(&self.root));
        Ok(status)
    }
}

/// Time since `apt-get update` last ran, or `None` when there are no package lists.
///
/// The list files themselves are no clock: apt stamps each one with the mirror's
/// Last-Modified time, so a list can be weeks old right after an update. apt does write
/// to `partial/` on every update, even when nothing changed upstream, so its mtime is
/// used; the newest list is only the fallback when `partial/` is missing.
fn lists_age(dir: &Path) -> Option<Duration> {
    let newest_list = std::fs::read_dir(dir)
        .ok()?
        .filter_map(Result::ok)
        .filter(|e| e.file_name().to_string_lossy().contains("_Packages"))
        .filter_map(|e| e.metadata().ok()?.modified().ok())
        .max()?;
    let updated = std::fs::metadata(dir.join("partial"))
        .and_then(|m| m.modified())
        .unwrap_or(newest_list);
    Some(
        SystemTime::now()
            .duration_since(updated)
            .unwrap_or(Duration::ZERO),
    )
}

fn on_path(program: &str) -> bool {
    std::env::var_os("PATH")
        .is_some_and(|path| std::env::split_paths(&path).any(|dir| dir.join(program).is_file()))
}

/// Maps the files of a fixture directory to the commands the provider runs.
fn fixture_runner(dir: &Path) -> std::io::Result<FixtureRunner> {
    let read = |name: &str| std::fs::read_to_string(dir.join(name));
    let optional = |name: &str| match read(name) {
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(String::new()),
        other => other,
    };

    let held_out = optional("apt-mark-showhold.txt")?;
    let held = parse_held(&held_out);
    let mut runner = FixtureRunner::new()
        .with(
            dpkg_query_cmd().command_line(),
            CmdOutput::ok(read("dpkg-query.txt")?),
        )
        .with(
            simulate_cmd().command_line(),
            CmdOutput::ok(read("apt-get-s-dist-upgrade.txt")?),
        )
        .with(showhold_cmd().command_line(), CmdOutput::ok(held_out));
    if !held.is_empty() {
        runner = runner.with(
            policy_cmd(&held).command_line(),
            CmdOutput::ok(read("apt-cache-policy.txt")?),
        );
    }
    Ok(runner)
}

#[cfg(test)]
mod tests;
