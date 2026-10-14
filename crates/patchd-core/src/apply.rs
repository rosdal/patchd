//! `patchd apply`: the run that changes the system (ADR 0009).
//!
//! The orchestration is provider-independent: refresh, plan, pre-flight, install, verify,
//! reboot detection. Each provider supplies the commands. Everything that can stop a run
//! happens before the provider's install step, so a refused run leaves dpkg untouched.

use std::sync::Mutex;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::{Duration, Instant};

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

use crate::config::RebootMode;
use crate::plan::{Exclusions, Filter, Plan, SkippedUpdate, touches_boot};
use crate::preflight::{self, Check};
use crate::provider::Provider;
use crate::reboot::RebootStatus;
use crate::root::Root;
use crate::runlog::{Event, EventSink, OutputStream};
use crate::runner::{Cmd, CmdOutput, RunError, Runner, Stream};

/// A package that changed, or was meant to.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct Change {
    pub name: String,
    /// `None` for a newly installed package.
    pub from: Option<String>,
    pub to: String,
}

/// How a run ended.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum RunResult {
    Success,
    SuccessRebootRequired,
    /// The install ran, but not every planned package reached its planned version.
    Partial,
    /// A step failed before the install started, such as the download; dpkg untouched.
    Failed,
    /// A pre-flight check refused the run; dpkg untouched.
    AbortedPreflight,
    /// Stopped by SIGTERM or Ctrl-C. A running install was allowed to finish.
    Interrupted,
    /// Never written. `patchd history` reports it for a log that ends without
    /// `run_finished`.
    Crashed,
}

impl RunResult {
    #[must_use]
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Success => "success",
            Self::SuccessRebootRequired => "success_reboot_required",
            Self::Partial => "partial",
            Self::Failed => "failed",
            Self::AbortedPreflight => "aborted_preflight",
            Self::Interrupted => "interrupted",
            Self::Crashed => "crashed",
        }
    }

    #[must_use]
    pub fn is_success(self) -> bool {
        matches!(self, Self::Success | Self::SuccessRebootRequired)
    }

    /// The worse of two results, for combining providers.
    #[must_use]
    pub fn worst(self, other: Self) -> Self {
        let rank = |r: Self| match r {
            Self::Success | Self::SuccessRebootRequired => 0,
            Self::Interrupted => 1,
            Self::Partial => 2,
            Self::Failed => 3,
            Self::AbortedPreflight => 4,
            Self::Crashed => 5,
        };
        if rank(other) > rank(self) {
            other
        } else {
            self
        }
    }
}

/// What one provider's install did.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ApplyReport {
    pub installed: Vec<Change>,
    pub failed: Vec<Change>,
    pub skipped: Vec<SkippedUpdate>,
    pub warnings: Vec<String>,
    pub duration_ms: u64,
    /// `Success`, `Partial`, `Failed` or `Interrupted`; reboots are decided afterwards.
    pub result: RunResult,
    pub message: Option<String>,
}

impl ApplyReport {
    #[must_use]
    pub fn new(result: RunResult) -> Self {
        Self {
            installed: Vec::new(),
            failed: Vec::new(),
            skipped: Vec::new(),
            warnings: Vec::new(),
            duration_ms: 0,
            result,
            message: None,
        }
    }
}

/// Everything a provider needs while it applies a plan.
pub struct ApplyCtx<'a> {
    pub filter: Filter,
    pub allow_removals: bool,
    pub repair: bool,
    pub exclude: &'a Exclusions,
    pub root: &'a Root,
    pub events: &'a dyn EventSink,
    /// Set by SIGTERM or Ctrl-C. Checked between steps; never used to kill an install.
    pub interrupted: &'a AtomicBool,
    /// Wrap the install step in `systemd-inhibit`, so shutdown and sleep wait for dpkg.
    pub inhibit: bool,
}

impl ApplyCtx<'_> {
    #[must_use]
    pub fn is_interrupted(&self) -> bool {
        self.interrupted.load(Ordering::SeqCst)
    }
}

/// Output is batched into `step_output` events of up to this many lines…
const BATCH_LINES: usize = 50;
/// …or whatever arrived in this long, so a crash loses little.
const BATCH_AGE: Duration = Duration::from_secs(2);

struct Batch {
    stream: OutputStream,
    lines: Vec<String>,
    since: Instant,
}

/// Runs one command as a logged step: `step_started`, batched `step_output` while it
/// runs, and `step_finished` with the exit code and duration.
///
/// # Errors
///
/// The command's own error, after it has been logged.
pub async fn step(
    events: &dyn EventSink,
    runner: &dyn Runner,
    name: &str,
    cmd: &Cmd,
) -> Result<CmdOutput, RunError> {
    let command = cmd.command_line();
    events.emit(Event::StepStarted {
        step: name.to_owned(),
        command: command.clone(),
    });
    let started = Instant::now();
    let batch = Mutex::new(Batch {
        stream: OutputStream::Stdout,
        lines: Vec::new(),
        since: Instant::now(),
    });
    let flush = |b: &mut Batch| {
        if !b.lines.is_empty() {
            events.emit(Event::StepOutput {
                step: name.to_owned(),
                stream: b.stream,
                lines: std::mem::take(&mut b.lines),
            });
        }
        b.since = Instant::now();
    };
    let on_line = |stream: Stream, line: &str| {
        let stream = match stream {
            Stream::Stdout => OutputStream::Stdout,
            Stream::Stderr => OutputStream::Stderr,
        };
        let mut b = batch
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        if b.stream != stream {
            flush(&mut b);
            b.stream = stream;
        }
        b.lines.push(line.to_owned());
        if b.lines.len() >= BATCH_LINES || b.since.elapsed() >= BATCH_AGE {
            flush(&mut b);
        }
    };
    let result = runner.run_streaming(cmd, &on_line).await;
    flush(
        &mut batch
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner),
    );

    let duration_ms = u64::try_from(started.elapsed().as_millis()).unwrap_or(u64::MAX);
    let (exit_code, error) = match &result {
        Ok(out) => (Some(out.status), None),
        Err(RunError::Failed { code, .. }) => (Some(*code), Some(result_error(&result))),
        Err(_) => (None, Some(result_error(&result))),
    };
    events.emit(Event::StepFinished {
        step: name.to_owned(),
        command,
        exit_code,
        duration_ms,
        error,
    });
    result
}

fn result_error(result: &Result<CmdOutput, RunError>) -> String {
    match result {
        Ok(_) => String::new(),
        // stderr is already in the step output; keep the summary to its last line.
        Err(RunError::Failed {
            program,
            code,
            stderr,
        }) => {
            let last = stderr.lines().last().unwrap_or("");
            format!("{program} exited {code}: {last}")
        }
        Err(e) => e.to_string(),
    }
}

/// What `run` was asked to do.
#[allow(clippy::struct_excessive_bools)] // one bool per config key
pub struct ApplyOptions {
    pub filter: Filter,
    pub refresh: bool,
    pub allow_removals: bool,
    pub repair: bool,
    pub exclude: Exclusions,
    pub reboot_mode: RebootMode,
    pub reboot_delay_minutes: u32,
    pub inhibit: bool,
}

/// What a run did, for the exit code.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Outcome {
    pub result: RunResult,
    pub plan: Option<Plan>,
    pub installed: Vec<Change>,
    pub failed: Vec<Change>,
    pub reboot: RebootStatus,
    pub message: Option<String>,
}

/// Runs a whole apply after the caller has taken the lock and opened the run log.
/// `checks` are the results the caller already has (root, lock); they go into the one
/// `preflight` event with the rest.
#[allow(clippy::too_many_lines)] // one linear sequence of steps reads best in one place
pub async fn run(
    providers: &[&dyn Provider],
    opts: &ApplyOptions,
    root: &Root,
    runner: &dyn Runner,
    events: &dyn EventSink,
    interrupted: &AtomicBool,
    mut checks: Vec<Check>,
) -> Outcome {
    let started = Instant::now();
    let ctx = ApplyCtx {
        filter: opts.filter,
        allow_removals: opts.allow_removals,
        repair: opts.repair,
        exclude: &opts.exclude,
        root,
        events,
        interrupted,
        inhibit: opts.inhibit,
    };
    let mut outcome = Outcome {
        result: RunResult::Success,
        plan: None,
        installed: Vec::new(),
        failed: Vec::new(),
        reboot: RebootStatus::default(),
        message: None,
    };
    let finish = |outcome: Outcome| {
        events.emit(Event::RunFinished {
            result: outcome.result,
            installed: outcome.installed.clone(),
            failed: outcome.failed.clone(),
            reboot_required: outcome.reboot.required,
            duration_ms: u64::try_from(started.elapsed().as_millis()).unwrap_or(u64::MAX),
            message: outcome.message.clone(),
        });
        outcome
    };
    if let Some(c) = preflight::unattended_upgrades_check(root) {
        checks.push(c);
    }

    if opts.refresh {
        for p in providers {
            if let Err(e) = p.refresh(events).await {
                outcome.result = RunResult::Failed;
                outcome.message = Some(format!("{}: refreshing package lists failed: {e}", p.id()));
                events.emit(Event::Preflight { checks });
                return finish(outcome);
            }
        }
    }

    let mut provider_plans = Vec::new();
    for p in providers {
        match p.plan(opts.filter, &opts.exclude).await {
            Ok(plan) => provider_plans.push(plan),
            Err(e) => {
                outcome.result = RunResult::Failed;
                outcome.message = Some(format!("{}: planning failed: {e}", p.id()));
                events.emit(Event::Preflight { checks });
                return finish(outcome);
            }
        }
    }
    let plan = Plan::new(opts.filter, provider_plans);
    events.emit(Event::Plan { plan: plan.clone() });
    outcome.plan = Some(plan.clone());

    if !plan.is_empty() {
        checks.extend(preflight::disk_checks(
            root,
            plan.updates().any(|u| touches_boot(&u.name)),
        ));
        for (p, pp) in providers.iter().zip(&plan.providers) {
            if !pp.updates.is_empty() {
                checks.extend(p.preflight(pp, &ctx).await);
            }
        }
    }
    let failed: Vec<String> = checks
        .iter()
        .filter(|c| c.failed())
        .map(|c| format!("{}: {}", c.name, c.detail))
        .collect();
    events.emit(Event::Preflight { checks });
    if !failed.is_empty() {
        outcome.result = RunResult::AbortedPreflight;
        outcome.message = Some(failed.join("; "));
        return finish(outcome);
    }
    if ctx.is_interrupted() {
        outcome.result = RunResult::Interrupted;
        outcome.message = Some("interrupted before the install started".to_owned());
        return finish(outcome);
    }

    for (p, pp) in providers.iter().zip(&plan.providers) {
        if pp.updates.is_empty() {
            continue;
        }
        let report = match p.apply(pp, &ctx).await {
            Ok(r) => r,
            Err(e) => ApplyReport {
                message: Some(format!("{}: {e}", p.id())),
                ..ApplyReport::new(RunResult::Failed)
            },
        };
        for w in &report.warnings {
            events.emit(Event::Warning { message: w.clone() });
        }
        outcome.result = outcome.result.worst(report.result);
        outcome.installed.extend(report.installed);
        outcome.failed.extend(report.failed);
        if let Some(m) = report.message {
            outcome.message = Some(match outcome.message.take() {
                Some(prev) => format!("{prev}; {m}"),
                None => m,
            });
        }
        if ctx.is_interrupted() {
            break;
        }
    }

    for p in providers {
        match p.reboot_required().await {
            Ok(s) => outcome.reboot.merge(s),
            Err(e) => events.emit(Event::Warning {
                message: format!("{}: reboot detection failed: {e}", p.id()),
            }),
        }
    }
    events.emit(Event::RebootStatus {
        status: outcome.reboot.clone(),
    });
    if outcome.result == RunResult::Success && outcome.reboot.required {
        outcome.result = RunResult::SuccessRebootRequired;
        if opts.reboot_mode == RebootMode::IfRequired && !ctx.is_interrupted() {
            let cmd = reboot_cmd(opts.reboot_delay_minutes);
            match step(events, runner, "reboot", &cmd).await {
                Ok(_) => {
                    outcome.message = Some(format!(
                        "rebooting in {} minute(s); cancel with `shutdown -c`",
                        opts.reboot_delay_minutes
                    ));
                }
                Err(e) => events.emit(Event::Warning {
                    message: format!("scheduling the reboot failed: {e}"),
                }),
            }
        }
    }
    finish(outcome)
}

/// `shutdown -r +N`, which warns logged-in users and can be cancelled with
/// `shutdown -c`.
#[must_use]
pub fn reboot_cmd(delay_minutes: u32) -> Cmd {
    Cmd::new(
        "shutdown",
        [
            "-r".to_owned(),
            format!("+{delay_minutes}"),
            "Patchd: rebooting to finish updates".to_owned(),
        ],
    )
}

/// Wraps a command in `systemd-inhibit`, so shutdown and sleep are blocked while it runs.
#[must_use]
pub fn inhibited(cmd: Cmd) -> Cmd {
    let mut args = vec![
        "--what=shutdown:sleep".to_owned(),
        "--who=patchd".to_owned(),
        "--why=Installing updates".to_owned(),
        "--mode=block".to_owned(),
        cmd.program.to_owned(),
    ];
    args.extend(cmd.args);
    Cmd {
        program: "systemd-inhibit",
        args,
        ..cmd
    }
}

#[cfg(test)]
mod tests {
    use std::fmt::Write as _;

    use super::*;
    use crate::runlog::MemorySink;
    use crate::runner::FixtureRunner;

    #[tokio::test]
    async fn step_logs_start_output_and_finish() {
        let sink = MemorySink::default();
        let runner = FixtureRunner::new().with(
            "tool go",
            CmdOutput {
                status: 0,
                stdout: (0..120).fold(String::new(), |mut s, i| {
                    let _ = writeln!(s, "line {i}");
                    s
                }),
                stderr: "warn\n".into(),
            },
        );
        step(&sink, &runner, "go", &Cmd::new("tool", ["go"]))
            .await
            .unwrap();
        let events = sink.events();
        assert!(
            matches!(&events[0], Event::StepStarted { step, command } if step == "go" && command == "tool go")
        );
        let sizes: Vec<usize> = events
            .iter()
            .filter_map(|e| match e {
                Event::StepOutput { lines, .. } => Some(lines.len()),
                _ => None,
            })
            .collect();
        assert_eq!(sizes, [50, 50, 20, 1]);
        assert!(matches!(
            events.last().unwrap(),
            Event::StepFinished {
                exit_code: Some(0),
                error: None,
                ..
            }
        ));
    }

    #[tokio::test]
    async fn failed_step_records_exit_code_and_error() {
        let sink = MemorySink::default();
        let runner = FixtureRunner::new().with(
            "tool",
            CmdOutput {
                status: 100,
                stdout: String::new(),
                stderr: "E: first\nE: last\n".into(),
            },
        );
        assert!(
            step(&sink, &runner, "t", &Cmd::new("tool", Vec::<String>::new()))
                .await
                .is_err()
        );
        let Event::StepFinished {
            exit_code, error, ..
        } = sink.events().pop().unwrap()
        else {
            panic!()
        };
        assert_eq!(exit_code, Some(100));
        assert_eq!(error.as_deref(), Some("tool exited 100: E: last"));
    }

    #[test]
    fn worst_result() {
        use RunResult::*;
        assert_eq!(Success.worst(Partial), Partial);
        assert_eq!(Partial.worst(Success), Partial);
        assert_eq!(Interrupted.worst(Partial), Partial);
        assert_eq!(Success.worst(Interrupted), Interrupted);
    }

    #[test]
    fn inhibit_wraps_the_command() {
        let cmd = inhibited(Cmd::new("apt-get", ["-y", "dist-upgrade"]).env("A", "b"));
        assert_eq!(cmd.program, "systemd-inhibit");
        assert!(
            cmd.command_line()
                .ends_with("--mode=block apt-get -y dist-upgrade")
        );
        assert_eq!(cmd.env, [("A", "b".to_owned())]);
    }
}
