//! `patchd apply`: take the lock, open the run log, and run the plan (ADR 0009).

use std::io::{BufRead, IsTerminal, Write};
use std::path::Path;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use clap::Args;
use patchd_core::apply::{self, ApplyOptions, Outcome, RunResult};
use patchd_core::lock::{LockError, RunLock};
use patchd_core::preflight::{self, Check};
use patchd_core::provider::Provider;
use patchd_core::root::Root;
use patchd_core::runlog::{self, Event, EventSink, RunLog};
use patchd_core::runner::SystemRunner;

/// Everything planned was installed.
const EXIT_OK: u8 = 0;
/// Partial, failed or interrupted.
const EXIT_FAILED: u8 = 1;
/// Another run holds the lock.
const EXIT_LOCKED: u8 = 3;
/// A pre-flight check refused the run; nothing was changed.
const EXIT_PREFLIGHT: u8 = 4;
/// Success, and a reboot is needed to finish.
const EXIT_REBOOT_REQUIRED: u8 = 20;

#[derive(Args)]
pub struct ApplyArgs {
    /// Only security updates, plus the new packages they pull in.
    #[arg(long, conflicts_with = "all")]
    security: bool,

    /// Every pending update.
    #[arg(long)]
    all: bool,

    /// Don't ask for confirmation. Required when stdin is not a terminal.
    #[arg(long, short)]
    yes: bool,
}

#[allow(clippy::too_many_lines)] // the run in order: config, root, lock, log, run
pub async fn run(args: &ApplyArgs, config_path: Option<&Path>, verbose: u8) -> u8 {
    let config = match crate::load_config(config_path) {
        Ok(c) => c,
        Err(e) => {
            crate::init_logging(verbose, "warn");
            eprintln!("error: {e}");
            return EXIT_FAILED;
        }
    };
    crate::init_logging(verbose, &config.log.level);
    let filter =
        crate::plan::filter_override(args.security, args.all).unwrap_or(config.apply.filter);

    if !cfg!(target_os = "linux") {
        eprintln!("error: patchd apply supports Linux only in this version");
        return EXIT_FAILED;
    }
    if !preflight::is_root() {
        eprintln!("error: {}", preflight::root_check(false).detail);
        return EXIT_PREFLIGHT;
    }
    if !args.yes && !confirm(filter.as_str()) {
        return EXIT_FAILED;
    }

    let root = Root::system();
    let run_id = uuid::Uuid::now_v7();
    let _lock = match RunLock::acquire(&root, &run_id.to_string()) {
        Ok(lock) => lock,
        Err(e @ LockError::Held { .. }) => {
            eprintln!("error: {e}");
            return EXIT_LOCKED;
        }
        Err(e) => {
            eprintln!("error: cannot take the run lock: {e}");
            return EXIT_FAILED;
        }
    };

    let runs = runlog::runs_dir(&root);
    if let Err(e) = runlog::apply_retention(
        &runs,
        config.log.retention_days,
        std::time::SystemTime::now(),
    ) {
        tracing::warn!(error = %e, "run log retention failed");
    }
    let log = match RunLog::create(&runs, run_id, jiff::Timestamp::now()) {
        Ok(log) => log,
        Err(e) => {
            eprintln!(
                "error: cannot create the run log in {}: {e}",
                runs.display()
            );
            return EXIT_FAILED;
        }
    };

    let (host, apt, in_container) = crate::system_apt();
    log.emit(Event::RunStarted {
        agent_version: patchd_core::build::LONG_VERSION.to_owned(),
        host: host.clone(),
        filter,
        command: "apply".to_owned(),
    });
    if in_container {
        log.emit(Event::Warning {
            message:
                "running inside a container; patching a container is almost never what you want"
                    .to_owned(),
        });
    }

    let interrupted = Arc::new(AtomicBool::new(false));
    watch_signals(&interrupted);

    let mut providers: Vec<&dyn Provider> = Vec::new();
    if apt.detect(&host).await {
        providers.push(&apt);
    }
    let checks = vec![
        preflight::root_check(true),
        Check::pass("lock", format!("holding {}", patchd_core::lock::LOCK_PATH)),
    ];
    let opts = ApplyOptions {
        filter,
        refresh: config.apply.refresh,
        allow_removals: config.apply.allow_removals,
        repair: config.apply.repair,
        exclude: config.exclusions(),
        reboot_mode: config.reboot.mode,
        reboot_delay_minutes: config.reboot.delay_minutes,
        inhibit: can_inhibit(),
    };
    let outcome = if providers.is_empty() {
        no_provider(&log)
    } else {
        apply::run(
            &providers,
            &opts,
            &root,
            &SystemRunner,
            &log,
            &interrupted,
            checks,
        )
        .await
    };

    print_outcome(&outcome, log.path());
    exit_code(&outcome)
}

fn no_provider(log: &RunLog) -> Outcome {
    let message = "no supported package manager found on this host".to_owned();
    log.emit(Event::RunFinished {
        result: RunResult::Failed,
        installed: Vec::new(),
        failed: Vec::new(),
        reboot_required: false,
        duration_ms: 0,
        message: Some(message.clone()),
    });
    Outcome {
        result: RunResult::Failed,
        plan: None,
        installed: Vec::new(),
        failed: Vec::new(),
        reboot: patchd_core::reboot::RebootStatus::default(),
        message: Some(message),
    }
}

fn exit_code(outcome: &Outcome) -> u8 {
    match outcome.result {
        RunResult::Success => EXIT_OK,
        RunResult::SuccessRebootRequired => EXIT_REBOOT_REQUIRED,
        RunResult::AbortedPreflight => EXIT_PREFLIGHT,
        RunResult::Partial | RunResult::Failed | RunResult::Interrupted | RunResult::Crashed => {
            EXIT_FAILED
        }
    }
}

fn print_outcome(outcome: &Outcome, log_path: &Path) {
    let mut out = std::io::stdout().lock();
    let _ = writeln!(out, "result: {}", outcome.result.as_str());
    if let Some(m) = &outcome.message {
        let _ = writeln!(out, "  {m}");
    }
    for c in &outcome.installed {
        let _ = writeln!(
            out,
            "installed  {}  {} → {}",
            c.name,
            c.from.as_deref().unwrap_or("—"),
            c.to
        );
    }
    for c in &outcome.failed {
        let _ = writeln!(out, "missed     {}  wanted {}", c.name, c.to);
    }
    if outcome.reboot.required {
        let _ = writeln!(
            out,
            "reboot required: {}",
            outcome.reboot.reasons.join("; ")
        );
    }
    if !outcome.reboot.services_to_restart.is_empty() {
        let _ = writeln!(
            out,
            "services to restart: {}",
            outcome.reboot.services_to_restart.join(", ")
        );
    }
    let _ = writeln!(out, "log: {}", log_path.display());
}

/// Asks on the terminal; anything but `y` declines. Without a terminal, `--yes` is
/// required.
fn confirm(filter: &str) -> bool {
    if !std::io::stdin().is_terminal() {
        eprintln!("error: stdin is not a terminal; pass --yes to apply without confirmation");
        return false;
    }
    eprint!("Install {filter} updates now? Run `patchd plan` to see them first. [y/N] ");
    let _ = std::io::stderr().flush();
    let mut answer = String::new();
    let _ = std::io::stdin().lock().read_line(&mut answer);
    matches!(answer.trim(), "y" | "Y" | "yes")
}

/// SIGTERM (systemd stopping the unit) and SIGINT (Ctrl-C) only set the flag. The install
/// runs in its own process group, so neither reaches dpkg; patchd lets it finish and
/// skips the remaining steps.
fn watch_signals(flag: &Arc<AtomicBool>) {
    #[cfg(unix)]
    {
        use tokio::signal::unix::{SignalKind, signal};
        for kind in [SignalKind::terminate(), SignalKind::interrupt()] {
            let flag = flag.clone();
            match signal(kind) {
                Ok(mut s) => {
                    tokio::spawn(async move {
                        while s.recv().await.is_some() {
                            if !flag.swap(true, Ordering::SeqCst) {
                                tracing::warn!(
                                    "signal received; letting the current step finish, then stopping"
                                );
                            }
                        }
                    });
                }
                Err(e) => tracing::warn!(error = %e, "cannot watch for signals"),
            }
        }
    }
    #[cfg(not(unix))]
    let _ = (flag, Ordering::SeqCst);
}

/// `systemd-inhibit` works only where systemd is the running init.
fn can_inhibit() -> bool {
    Path::new("/run/systemd/system").is_dir()
        && std::env::var_os("PATH").is_some_and(|path| {
            std::env::split_paths(&path).any(|dir| dir.join("systemd-inhibit").is_file())
        })
}
