mod apply;
mod config_cmd;
mod history;
mod plan;
mod render;
mod scan;
mod status;

use std::io::IsTerminal;
use std::path::{Path, PathBuf};
use std::process::ExitCode;
use std::sync::Arc;

use clap::{ArgAction, Parser, Subcommand};
use patchd_core::config::Config;
use patchd_core::host::{self, HostInfo};
use patchd_core::providers::apt::AptProvider;
use patchd_core::runner::SystemRunner;
use tracing_subscriber::EnvFilter;

/// Self-hosted patch management for Linux and Windows.
#[derive(Parser)]
#[command(name = "patchd", version = patchd_core::build::LONG_VERSION)]
struct Cli {
    /// Log more to stderr (-v debug, -vv trace). `RUST_LOG` overrides it.
    #[arg(short, long, action = ArgAction::Count, global = true)]
    verbose: u8,

    /// Read this config file instead of /etc/patchd/config.toml.
    #[arg(long, global = true, value_name = "PATH")]
    config: Option<PathBuf>,

    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// List installed packages and pending updates. Read-only.
    ///
    /// Exit codes: 0 nothing to update, 10 updates available, 11 security updates
    /// available, 1 a provider failed, 2 usage error.
    Scan(scan::ScanArgs),
    /// Show exactly what `apply` would install. Read-only; this is the dry run.
    ///
    /// Exit codes: 0 nothing to install, 10 the plan installs something, 1 error.
    Plan(plan::PlanArgs),
    /// Install the plan: simulate, download, install, verify. Needs root.
    ///
    /// Exit codes: 0 success, 20 success but a reboot is required, 1 partial, failed or
    /// interrupted, 3 another run is active, 4 a pre-flight check refused the run.
    Apply(apply::ApplyArgs),
    /// List past runs, or show one run in detail.
    History(history::HistoryArgs),
    /// The last run, the next scheduled run, reboot status and pending updates.
    Status(status::StatusArgs),
    /// Check or show the configuration.
    #[command(subcommand)]
    Config(config_cmd::ConfigCommand),
}

fn main() -> ExitCode {
    let cli = Cli::parse();
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("build tokio runtime");
    let code = match cli.command {
        Command::Scan(args) => {
            init_logging(cli.verbose, "warn");
            runtime.block_on(scan::run(&args))
        }
        Command::Plan(args) => {
            init_logging(cli.verbose, "warn");
            runtime.block_on(plan::run(&args, cli.config.as_deref()))
        }
        Command::Apply(args) => {
            runtime.block_on(apply::run(&args, cli.config.as_deref(), cli.verbose))
        }
        Command::History(args) => {
            init_logging(cli.verbose, "warn");
            history::run(&args)
        }
        Command::Status(args) => {
            init_logging(cli.verbose, "warn");
            runtime.block_on(status::run(&args))
        }
        Command::Config(cmd) => {
            init_logging(cli.verbose, "warn");
            config_cmd::run(&cmd, cli.config.as_deref())
        }
    };
    ExitCode::from(code)
}

/// `-v` and `-vv` win over `default`; `RUST_LOG` wins over both.
fn init_logging(verbose: u8, default: &str) {
    let level = match verbose {
        0 => default,
        1 => "debug",
        _ => "trace",
    };
    tracing_subscriber::fmt()
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new(level)),
        )
        .with_writer(std::io::stderr)
        .with_ansi(std::io::stderr().is_terminal())
        .init();
}

/// The config file plus `PATCHD_*` overrides.
fn load_config(path: Option<&Path>) -> Result<Config, String> {
    Config::load(path, std::env::vars()).map_err(|e| e.to_string())
}

/// The real host and its apt provider, and whether we run in a container.
fn system_apt() -> (HostInfo, AptProvider, bool) {
    let host = HostInfo::detect();
    let apt = AptProvider::new(Arc::new(SystemRunner)).ubuntu(host.os.is_ubuntu_like());
    (host, apt, host::in_container())
}

/// The host and apt provider replayed from a fixture directory (debug builds only).
#[cfg(debug_assertions)]
fn fixture_apt(dir: &Path) -> Result<(HostInfo, AptProvider), String> {
    let os_release = std::fs::read_to_string(dir.join("os-release"))
        .map_err(|e| format!("{}: {e}", dir.display()))?;
    let host = HostInfo {
        hostname: "example".to_owned(),
        os: host::OsInfo::from_os_release(&os_release),
        arch: "x86_64".to_owned(),
        kernel: None,
    };
    let (apt, _) =
        AptProvider::from_fixtures(dir).map_err(|e| format!("{}: {e}", dir.display()))?;
    let apt = apt.ubuntu(host.os.is_ubuntu_like());
    Ok((host, apt))
}

/// Colour only on a terminal, and never with `NO_COLOR`.
fn use_color() -> bool {
    std::io::stdout().is_terminal() && std::env::var_os("NO_COLOR").is_none_or(|v| v.is_empty())
}
