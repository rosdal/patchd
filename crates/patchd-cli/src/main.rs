mod render;

use std::io::{IsTerminal, Write};
use std::process::ExitCode;
use std::sync::Arc;

use clap::{ArgAction, Args, Parser, Subcommand};
use patchd_core::host::{self, HostInfo};
use patchd_core::model::{ScanReport, UpdateKind};
use patchd_core::provider::Provider;
use patchd_core::providers::apt::AptProvider;
use patchd_core::runner::SystemRunner;
use tracing_subscriber::EnvFilter;

/// Scan OK, nothing to update.
const EXIT_OK: u8 = 0;
/// A provider failed; the report still has what succeeded.
const EXIT_PROVIDER_FAILED: u8 = 1;
/// Updates available, none of them security.
const EXIT_UPDATES: u8 = 10;
/// Security updates available.
const EXIT_SECURITY: u8 = 11;

/// Self-hosted patch management for Linux and Windows.
#[derive(Parser)]
#[command(name = "patchd", version = patchd_core::build::LONG_VERSION)]
struct Cli {
    /// Log more to stderr (-v debug, -vv trace). `RUST_LOG` overrides it.
    #[arg(short, long, action = ArgAction::Count, global = true)]
    verbose: u8,

    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// List installed packages and pending updates. Read-only.
    ///
    /// Exit codes: 0 nothing to update, 10 updates available, 11 security updates
    /// available, 1 a provider failed, 2 usage error.
    Scan(ScanArgs),
}

#[derive(Args)]
#[allow(clippy::struct_excessive_bools)] // one bool per CLI flag
struct ScanArgs {
    /// Print the full report as JSON.
    #[arg(long, conflicts_with = "quiet")]
    json: bool,

    /// Print nothing; only the exit code matters.
    #[arg(long, short)]
    quiet: bool,

    /// Only run this provider.
    #[arg(long, value_parser = ["apt"])]
    provider: Option<String>,

    /// Run `apt-get update` first. Needs root.
    #[arg(long)]
    refresh: bool,

    /// Also list every installed package.
    #[arg(long)]
    packages: bool,

    /// Replay recorded command output from this directory instead of running commands.
    #[cfg(debug_assertions)]
    #[arg(long, hide = true)]
    fixtures: Option<std::path::PathBuf>,
}

fn main() -> ExitCode {
    let cli = Cli::parse();
    init_logging(cli.verbose);

    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .expect("build tokio runtime");
    let code = match cli.command {
        Command::Scan(args) => runtime.block_on(scan(&args)),
    };
    ExitCode::from(code)
}

fn init_logging(verbose: u8) {
    let level = match verbose {
        0 => "warn",
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

async fn scan(args: &ScanArgs) -> u8 {
    let (host, apt, in_container) = match setup(args) {
        Ok(setup) => setup,
        Err(msg) => {
            eprintln!("error: {msg}");
            return EXIT_PROVIDER_FAILED;
        }
    };

    if args.refresh {
        if in_fixture_mode(args) {
            eprintln!("error: --refresh cannot be replayed from fixtures");
            return EXIT_PROVIDER_FAILED;
        }
        if !host::is_root() {
            eprintln!(
                "error: --refresh runs `apt-get update`, which needs root; run `sudo patchd scan --refresh`"
            );
            return EXIT_PROVIDER_FAILED;
        }
        if apt.detect(&host).await {
            if let Err(e) = apt.refresh().await {
                eprintln!("error: refreshing package lists failed: {e}");
                return EXIT_PROVIDER_FAILED;
            }
        }
    }

    let providers: Vec<Box<dyn Provider>> = vec![Box::new(apt)];
    let mut report =
        patchd_core::scan::scan(host, &providers, args.provider.as_deref(), in_container).await;
    if report.providers.is_empty() {
        report
            .warnings
            .push("no supported package manager found on this host".to_owned());
    }

    let code = exit_code(&report);
    if args.json {
        let json = serde_json::to_string_pretty(&report).expect("serialize report");
        println!("{json}");
    } else if !args.quiet {
        let color = std::io::stdout().is_terminal()
            && std::env::var_os("NO_COLOR").is_none_or(|v| v.is_empty());
        print!("{}", render::table(&report, args.packages, color));
        let mut stderr = std::io::stderr().lock();
        for w in &report.warnings {
            let _ = writeln!(stderr, "warning: {w}");
        }
        for p in &report.providers {
            for w in &p.warnings {
                let _ = writeln!(stderr, "warning: {}: {w}", p.id);
            }
            if let Some(e) = &p.error {
                let _ = writeln!(stderr, "error: {}: {e}", p.id);
            }
        }
    }
    code
}

/// The host, the apt provider, and whether we run in a container.
fn setup(args: &ScanArgs) -> Result<(HostInfo, AptProvider, bool), String> {
    #[cfg(debug_assertions)]
    if let Some(dir) = &args.fixtures {
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
        return Ok((host, apt, false));
    }
    let _ = args;
    let host = HostInfo::detect();
    let apt = AptProvider::new(Arc::new(SystemRunner)).ubuntu(host.os.is_ubuntu_like());
    Ok((host, apt, host::in_container()))
}

fn in_fixture_mode(args: &ScanArgs) -> bool {
    #[cfg(debug_assertions)]
    return args.fixtures.is_some();
    #[cfg(not(debug_assertions))]
    {
        let _ = args;
        false
    }
}

/// Held packages are reported but don't count: holding is a deliberate choice.
fn exit_code(report: &ScanReport) -> u8 {
    if report.has_errors() || report.providers.is_empty() {
        return EXIT_PROVIDER_FAILED;
    }
    let mut pending = report.updates().filter(|u| !u.held).peekable();
    if pending.peek().is_none() {
        EXIT_OK
    } else if pending.any(|u| u.kind == UpdateKind::Security) {
        EXIT_SECURITY
    } else {
        EXIT_UPDATES
    }
}
