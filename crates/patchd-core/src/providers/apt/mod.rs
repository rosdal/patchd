//! The apt provider for Debian, Ubuntu and derivatives (ADR 0006).
//!
//! Read-only: everything comes from `dpkg-query` and `apt-get -s dist-upgrade`, which
//! need neither root nor the dpkg lock. Only [`AptProvider::refresh`] writes, and only
//! to the package lists.

mod available;
mod inventory;

use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, SystemTime};

use async_trait::async_trait;

pub use available::classify;

use crate::host::HostInfo;
use crate::model::{ProviderId, Update};
use crate::provider::{Available, Inventory, Provider, ProviderError};
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
        };
        Ok((provider, runner))
    }

    /// Runs `apt-get update`. Needs root; the caller checks.
    ///
    /// # Errors
    ///
    /// When `apt-get update` fails or times out.
    pub async fn refresh(&self) -> Result<(), ProviderError> {
        self.runner
            .run(&Cmd::new("apt-get", ["update", "-q"]).timeout(REFRESH_TIMEOUT))
            .await?;
        Ok(())
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
}

/// Time since the newest `*_Packages` file in the lists directory was written.
fn lists_age(dir: &Path) -> Option<Duration> {
    let newest = std::fs::read_dir(dir)
        .ok()?
        .filter_map(Result::ok)
        .filter(|e| e.file_name().to_string_lossy().contains("_Packages"))
        .filter_map(|e| e.metadata().ok()?.modified().ok())
        .max()?;
    Some(
        SystemTime::now()
            .duration_since(newest)
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
