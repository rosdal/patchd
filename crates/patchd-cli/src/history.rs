//! `patchd history`: past runs, read back from the run logs.

use std::fmt::Write as _;
use std::path::Path;

use clap::Args;
use comfy_table::{Cell, ContentArrangement, Table, presets};
use patchd_core::root::Root;
use patchd_core::runlog::{self, Event, Record, RunSummary};

const EXIT_OK: u8 = 0;
const EXIT_ERROR: u8 = 1;

/// How many output lines `history <run_id>` shows per step.
const TAIL: usize = 10;

#[derive(Args)]
pub struct HistoryArgs {
    /// Show one run in detail; a unique prefix of its ID is enough.
    run_id: Option<String>,

    /// How many runs to list.
    #[arg(long, default_value_t = 20)]
    limit: usize,

    /// Print JSON: a list of run summaries, or one run's events.
    #[arg(long)]
    json: bool,

    /// Read run logs under this directory instead of `/`.
    #[arg(long, hide = true)]
    root: Option<std::path::PathBuf>,
}

pub fn run(args: &HistoryArgs) -> u8 {
    let root = args.root.clone().map_or_else(Root::system, Root::at);
    let dir = runlog::runs_dir(&root);
    let result = match &args.run_id {
        Some(id) => detail(&dir, id, args.json),
        None => list(&dir, args.limit, args.json),
    };
    match result {
        Ok(text) => {
            print!("{text}");
            EXIT_OK
        }
        Err(e) => {
            eprintln!("error: {e}");
            EXIT_ERROR
        }
    }
}

fn access_hint(dir: &Path, e: &std::io::Error) -> String {
    if e.kind() == std::io::ErrorKind::PermissionDenied {
        format!(
            "{}: permission denied; run logs are readable by root, try sudo",
            dir.display()
        )
    } else {
        format!("{}: {e}", dir.display())
    }
}

fn list(dir: &Path, limit: usize, json: bool) -> Result<String, String> {
    let runs = runlog::history(dir, limit).map_err(|e| access_hint(dir, &e))?;
    if json {
        return Ok(format!(
            "{}\n",
            serde_json::to_string_pretty(&runs).expect("serialize history")
        ));
    }
    if runs.is_empty() {
        return Ok("no runs yet\n".to_owned());
    }
    Ok(table(&runs))
}

pub fn table(runs: &[RunSummary]) -> String {
    let mut t = Table::new();
    t.load_style(presets::NOTHING)
        .set_content_arrangement(ContentArrangement::Disabled)
        .force_no_tty()
        .set_header(vec![
            "STARTED",
            "DURATION",
            "FILTER",
            "INSTALLED",
            "RESULT",
            "REBOOT",
            "RUN",
        ]);
    for r in runs {
        t.add_row(vec![
            Cell::new(r.started_at.strftime("%Y-%m-%d %H:%M:%S UTC")),
            Cell::new(r.duration_ms.map_or_else(|| "—".to_owned(), duration)),
            Cell::new(r.filter.map_or("—", patchd_core::plan::Filter::as_str)),
            Cell::new(r.installed),
            Cell::new(r.result.as_str()),
            Cell::new(if r.reboot_required { "required" } else { "" }),
            Cell::new(&r.run_id.to_string()[..13]),
        ]);
    }
    for i in 0..7 {
        if let Some(col) = t.column_mut(i) {
            col.set_padding((0, 2));
        }
    }
    t.lines().fold(String::new(), |mut out, l| {
        let _ = writeln!(out, "{}", l.trim_end());
        out
    })
}

pub fn duration(ms: u64) -> String {
    match ms / 1000 {
        s if s < 60 => format!("{s}s"),
        s if s < 3600 => format!("{}m{:02}s", s / 60, s % 60),
        s => format!("{}h{:02}m", s / 3600, (s % 3600) / 60),
    }
}

fn detail(dir: &Path, id: &str, json: bool) -> Result<String, String> {
    let path = runlog::find_run(dir, id)
        .map_err(|e| access_hint(dir, &e))?
        .ok_or_else(|| format!("no run with an ID starting {id:?}"))?;
    let records = runlog::read_run(&path).map_err(|e| format!("{}: {e}", path.display()))?;
    if json {
        return Ok(format!(
            "{}\n",
            serde_json::to_string_pretty(&records).expect("serialize run")
        ));
    }
    Ok(render_detail(&path, &records))
}

/// One run, step by step, with the tail of each step's output.
#[allow(clippy::too_many_lines)] // one arm per event type
pub fn render_detail(path: &Path, records: &[Record]) -> String {
    let mut out = String::new();
    if let Some(s) = runlog::summarize(path, records) {
        let _ = writeln!(out, "run      {}", s.run_id);
        let _ = writeln!(
            out,
            "started  {}",
            s.started_at.strftime("%Y-%m-%d %H:%M:%S UTC")
        );
        let _ = writeln!(out, "result   {}", s.result.as_str());
        let _ = writeln!(out, "log      {}", path.display());
    }
    let mut tail: Vec<String> = Vec::new();
    for r in records {
        match &r.event {
            Event::Preflight { checks } => {
                let _ = writeln!(out, "\npreflight");
                for c in checks {
                    let status = format!("{:?}", c.status).to_lowercase();
                    let _ = writeln!(out, "  {status:<4}  {:<20} {}", c.name, c.detail);
                }
            }
            Event::Plan { plan } => {
                let skipped: usize = plan.providers.iter().map(|p| p.held_skipped.len()).sum();
                let _ = writeln!(
                    out,
                    "\nplan {} (filter {}): {} to install, {skipped} skipped",
                    plan.id,
                    plan.filter,
                    plan.updates().count()
                );
                for u in plan.updates() {
                    let _ = writeln!(
                        out,
                        "  {}  {} → {}",
                        u.name,
                        u.from_version.as_deref().unwrap_or("—"),
                        u.to_version
                    );
                }
            }
            Event::StepStarted { .. } => tail.clear(),
            Event::StepOutput { lines, .. } => {
                tail.extend(lines.iter().cloned());
                let excess = tail.len().saturating_sub(TAIL);
                tail.drain(..excess);
            }
            Event::StepFinished {
                step,
                command,
                exit_code,
                duration_ms,
                error,
                ..
            } => {
                let exit = exit_code.map_or_else(|| "—".to_owned(), |c| c.to_string());
                let _ = writeln!(
                    out,
                    "\nstep {step}: exit {exit}, {}",
                    duration(*duration_ms)
                );
                let _ = writeln!(out, "  $ {command}");
                if let Some(e) = error {
                    let _ = writeln!(out, "  error: {e}");
                }
                for l in tail.drain(..) {
                    let _ = writeln!(out, "  | {l}");
                }
            }
            Event::Verify { ok, missed, audit } => {
                let _ = writeln!(out, "\nverify: {}", if *ok { "ok" } else { "FAILED" });
                for c in missed {
                    let _ = writeln!(out, "  missed {} (wanted {})", c.name, c.to);
                }
                for a in audit {
                    let _ = writeln!(out, "  audit: {a}");
                }
            }
            Event::RebootStatus { status } => {
                if status.required {
                    let _ = writeln!(out, "\nreboot required: {}", status.reasons.join("; "));
                }
            }
            Event::Warning { message } => {
                let _ = writeln!(out, "\nwarning: {message}");
            }
            Event::RunFinished {
                result,
                installed,
                message,
                duration_ms,
                ..
            } => {
                let _ = writeln!(
                    out,
                    "\nfinished: {} after {}, {} installed",
                    result.as_str(),
                    duration(*duration_ms),
                    installed.len()
                );
                if let Some(m) = message {
                    let _ = writeln!(out, "  {m}");
                }
            }
            Event::RunStarted { .. } => {}
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn durations() {
        assert_eq!(duration(4_200), "4s");
        assert_eq!(duration(185_000), "3m05s");
        assert_eq!(duration(3_900_000), "1h05m");
    }
}
