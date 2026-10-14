//! `patchd scan`.

use std::io::Write;

use clap::Args;
use patchd_core::host::HostInfo;
use patchd_core::lock::RunLock;
use patchd_core::model::{ScanReport, UpdateKind};
use patchd_core::preflight;
use patchd_core::root::Root;
use patchd_core::runlog::NullSink;

use crate::render;

/// Scan OK, nothing to update.
const EXIT_OK: u8 = 0;
/// A provider failed; the report still has what succeeded.
const EXIT_PROVIDER_FAILED: u8 = 1;
/// Updates available, none of them security.
const EXIT_UPDATES: u8 = 10;
/// Security updates available.
const EXIT_SECURITY: u8 = 11;

#[derive(Args)]
#[allow(clippy::struct_excessive_bools)] // one bool per CLI flag
pub struct ScanArgs {
    /// Print the full report as JSON.
    #[arg(long, conflicts_with = "quiet")]
    json: bool,

    /// Print nothing; only the exit code matters.
    #[arg(long, short)]
    quiet: bool,

    /// Only run this provider.
    #[arg(long, value_parser = ["apt", "dnf"])]
    provider: Option<String>,

    /// Refresh the package lists first (`apt-get update`, `dnf makecache`). Needs root,
    /// and fails while another patchd run holds the lock.
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

pub async fn run(args: &ScanArgs) -> u8 {
    let (host, providers, in_container) = match setup(args) {
        Ok(setup) => setup,
        Err(msg) => return fail(args, &msg),
    };

    if args.refresh {
        if in_fixture_mode(args) {
            return fail(args, "--refresh cannot be replayed from fixtures");
        }
        if !preflight::is_root() {
            return fail(
                args,
                "--refresh refreshes the package lists, which needs root; run `sudo patchd scan --refresh`",
            );
        }
        // Held only while the lists are refreshed; the read-only scan needs no lock.
        let lock = match RunLock::acquire(&Root::system(), "scan --refresh") {
            Ok(lock) => lock,
            Err(e) => return fail(args, &e.to_string()),
        };
        for p in crate::detected(&host, &providers).await {
            if args
                .provider
                .as_deref()
                .is_some_and(|only| only != p.id().as_str())
            {
                continue;
            }
            if let Err(e) = p.refresh(&NullSink).await {
                return fail(
                    args,
                    &format!("{}: refreshing package lists failed: {e}", p.id()),
                );
            }
        }
        drop(lock);
    }

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
        print!(
            "{}",
            render::table(&report, args.packages, crate::use_color())
        );
        print_warnings(&report);
    }
    code
}

pub fn print_warnings(report: &ScanReport) {
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

/// Reports an error that stops the scan; `--quiet` means nothing is printed, ever.
fn fail(args: &ScanArgs, msg: &str) -> u8 {
    if !args.quiet {
        eprintln!("error: {msg}");
    }
    EXIT_PROVIDER_FAILED
}

fn setup(args: &ScanArgs) -> Result<(HostInfo, crate::Providers, bool), String> {
    #[cfg(debug_assertions)]
    if let Some(dir) = &args.fixtures {
        let (host, providers) = crate::fixture_providers(dir)?;
        return Ok((host, providers, false));
    }
    let _ = args;
    Ok(crate::system_providers())
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
pub fn exit_code(report: &ScanReport) -> u8 {
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
