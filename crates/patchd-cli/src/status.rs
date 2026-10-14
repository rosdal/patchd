//! `patchd status`: last run, next run, reboot status, pending updates.

use std::io::Write as _;

use clap::Args;
use patchd_core::model::UpdateKind;
use patchd_core::root::Root;
use patchd_core::runlog;
use patchd_core::runner::{Cmd, Runner, SystemRunner};
use serde::Serialize;

const EXIT_OK: u8 = 0;

#[derive(Args)]
pub struct StatusArgs {
    /// Print the status as JSON.
    #[arg(long)]
    json: bool,
}

#[derive(Serialize)]
struct Status {
    last_run: Option<runlog::RunSummary>,
    /// Why the last run could not be read, such as missing root.
    last_run_error: Option<String>,
    /// From `systemctl show patchd-apply.timer`; `None` when the timer is not active.
    next_run: Option<String>,
    reboot: patchd_core::reboot::RebootStatus,
    pending_updates: usize,
    pending_security: usize,
    scan_error: Option<String>,
}

pub async fn run(args: &StatusArgs) -> u8 {
    let root = Root::system();
    let (last_run, last_run_error) = match runlog::history(&runlog::runs_dir(&root), 1) {
        Ok(mut runs) => {
            let held = patchd_core::lock::is_held(&patchd_core::lock::lock_path(&root));
            runlog::mark_running(&mut runs, held);
            (runs.pop(), None)
        }
        Err(e) => (None, Some(e.to_string())),
    };
    let (host, providers, in_container) = crate::system_providers();
    let mut reboot = patchd_core::reboot::RebootStatus::default();
    for p in crate::detected(&host, &providers).await {
        if let Ok(s) = p.reboot_required().await {
            reboot.merge(s);
        }
    }
    let report = patchd_core::scan::scan(host, &providers, None, in_container).await;
    let pending: Vec<_> = report.updates().filter(|u| !u.held).collect();
    let status = Status {
        last_run,
        last_run_error,
        next_run: next_run().await,
        reboot,
        pending_updates: pending.len(),
        pending_security: pending
            .iter()
            .filter(|u| u.kind == UpdateKind::Security)
            .count(),
        scan_error: report
            .providers
            .iter()
            .filter_map(|p| p.error.as_ref().map(|e| format!("{}: {e}", p.id)))
            .reduce(|a, b| format!("{a}; {b}")),
    };

    let mut out = std::io::stdout().lock();
    if args.json {
        let _ = writeln!(
            out,
            "{}",
            serde_json::to_string_pretty(&status).expect("serialize status")
        );
        return EXIT_OK;
    }
    match (&status.last_run, &status.last_run_error) {
        (Some(r), _) => {
            let _ = writeln!(
                out,
                "last run   {} · {} · {} installed · {}",
                r.started_at.strftime("%Y-%m-%d %H:%M UTC"),
                r.result.as_str(),
                r.installed,
                &r.run_id.to_string()[..13]
            );
        }
        (None, Some(e)) => {
            let _ = writeln!(out, "last run   unknown ({e}; try sudo)");
        }
        (None, None) => {
            let _ = writeln!(out, "last run   none yet");
        }
    }
    let _ = writeln!(
        out,
        "next run   {}",
        status
            .next_run
            .as_deref()
            .unwrap_or("not scheduled (patchd-apply.timer is not active)")
    );
    if status.reboot.required {
        let _ = writeln!(
            out,
            "reboot     required: {}",
            status.reboot.reasons.join("; ")
        );
    } else {
        let _ = writeln!(out, "reboot     not required");
    }
    if !status.reboot.services_to_restart.is_empty() {
        let _ = writeln!(
            out,
            "restart    {}",
            status.reboot.services_to_restart.join(", ")
        );
    }
    match &status.scan_error {
        Some(e) => {
            let _ = writeln!(out, "pending    unknown: {e}");
        }
        None => {
            let _ = writeln!(
                out,
                "pending    {} update(s), {} security",
                status.pending_updates, status.pending_security
            );
        }
    }
    EXIT_OK
}

/// The timer's next trigger, or `None` when systemd has none (not installed, not
/// enabled, or no systemd).
async fn next_run() -> Option<String> {
    let cmd = Cmd::new(
        "systemctl",
        [
            "show",
            "patchd-apply.timer",
            "-p",
            "NextElapseUSecRealtime",
            "--value",
        ],
    );
    let out = SystemRunner.run(&cmd).await.ok()?;
    let v = out.stdout.trim();
    (!v.is_empty() && v != "n/a" && v != "0").then(|| v.to_owned())
}
