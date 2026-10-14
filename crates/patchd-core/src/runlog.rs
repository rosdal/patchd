//! The run log: one append-only JSON-lines file per run, in
//! `/var/log/patchd/runs/<started_at>_<run_id>.jsonl`.
//!
//! The v0.6 agent will upload the same events, so they are designed as the future wire
//! format: every line is one object with `run_id`, `ts` and `event`, and consumers ignore
//! fields they don't know. The schema is `protocol/schemas/run-event.json`.

use std::fs::File;
use std::io::{self, Write as _};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::{Duration, SystemTime};

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

use crate::apply::{Change, RunResult};
use crate::host::HostInfo;
use crate::plan::{Filter, Plan};
use crate::preflight::Check;
use crate::reboot::RebootStatus;
use crate::root::Root;

/// Where run logs live, under the root.
pub const RUNS_DIR: &str = "/var/log/patchd/runs";

/// Retention never deletes the most recent runs, however old.
pub const KEEP_AT_LEAST: usize = 20;

/// One line of a run log.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub struct Record {
    pub run_id: uuid::Uuid,
    #[schemars(with = "String", extend("format" = "date-time"))]
    pub ts: jiff::Timestamp,
    #[serde(flatten)]
    pub event: Event,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(tag = "event", rename_all = "snake_case")]
pub enum Event {
    RunStarted {
        /// The `patchd` build.
        agent_version: String,
        host: HostInfo,
        filter: Filter,
        /// What started the run: `apply`.
        command: String,
    },
    /// Every pre-flight check result, passed or not.
    Preflight {
        checks: Vec<Check>,
    },
    Plan {
        plan: Plan,
    },
    StepStarted {
        step: String,
        command: String,
    },
    /// Output lines, batched.
    StepOutput {
        step: String,
        stream: OutputStream,
        lines: Vec<String>,
    },
    StepFinished {
        step: String,
        command: String,
        /// `None` when the command could not run or timed out.
        exit_code: Option<i32>,
        duration_ms: u64,
        error: Option<String>,
    },
    Verify {
        ok: bool,
        /// Planned packages not at their planned version afterwards.
        missed: Vec<Change>,
        /// Lines from `dpkg --audit` (or the dnf equivalent) after the install.
        audit: Vec<String>,
    },
    RebootStatus {
        status: RebootStatus,
    },
    /// Something worth knowing that is not a check result.
    Warning {
        message: String,
    },
    RunFinished {
        result: RunResult,
        installed: Vec<Change>,
        failed: Vec<Change>,
        reboot_required: bool,
        duration_ms: u64,
        /// Why a run did not succeed, in one line.
        message: Option<String>,
    },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "snake_case")]
pub enum OutputStream {
    Stdout,
    Stderr,
}

impl Event {
    /// The one-line summary mirrored to stderr, and so to the journal under systemd.
    #[must_use]
    pub fn summary(&self) -> String {
        match self {
            Self::RunStarted {
                filter, command, ..
            } => format!("{command} started (filter {filter})"),
            Self::Preflight { checks } => {
                let bad: Vec<String> = checks
                    .iter()
                    .filter(|c| c.status != crate::preflight::CheckStatus::Pass)
                    .map(|c| format!("{} {:?}: {}", c.name, c.status, c.detail).to_lowercase())
                    .collect();
                if bad.is_empty() {
                    format!("preflight: {} checks passed", checks.len())
                } else {
                    format!("preflight: {}", bad.join("; "))
                }
            }
            Self::Plan { plan } => {
                let skipped: usize = plan.providers.iter().map(|p| p.held_skipped.len()).sum();
                format!(
                    "plan {}: {} to install, {skipped} skipped",
                    plan.id,
                    plan.updates().count()
                )
            }
            Self::StepStarted { step, command } => format!("{step}: {command}"),
            Self::StepOutput { step, lines, .. } => format!("{step}: {} output lines", lines.len()),
            Self::StepFinished {
                step,
                exit_code,
                duration_ms,
                error,
                ..
            } => match error {
                Some(e) => format!("{step}: failed after {duration_ms} ms: {e}"),
                None => format!(
                    "{step}: exit {} after {duration_ms} ms",
                    exit_code.unwrap_or(-1)
                ),
            },
            Self::Verify { ok, missed, .. } => {
                if *ok {
                    "verify: every planned package is at its planned version".to_owned()
                } else {
                    format!("verify: {} package(s) missed", missed.len())
                }
            }
            Self::RebootStatus { status } => {
                if status.required {
                    format!("reboot required: {}", status.reasons.join("; "))
                } else {
                    "no reboot required".to_owned()
                }
            }
            Self::Warning { message } => format!("warning: {message}"),
            Self::RunFinished {
                result,
                installed,
                message,
                ..
            } => match message {
                Some(m) => format!(
                    "finished: {} ({} installed): {m}",
                    result.as_str(),
                    installed.len()
                ),
                None => format!(
                    "finished: {} ({} installed)",
                    result.as_str(),
                    installed.len()
                ),
            },
        }
    }
}

/// Receives run events. The run log writes them; tests collect them.
pub trait EventSink: Send + Sync {
    fn emit(&self, event: Event);
}

/// Discards events, for commands that keep no run log (`scan --refresh`).
#[derive(Debug, Default, Clone, Copy)]
pub struct NullSink;

impl EventSink for NullSink {
    fn emit(&self, _event: Event) {}
}

/// Collects events in memory, for tests and dry runs.
#[derive(Debug, Default)]
pub struct MemorySink(pub Mutex<Vec<Event>>);

impl MemorySink {
    /// # Panics
    ///
    /// If the lock is poisoned.
    #[must_use]
    pub fn events(&self) -> Vec<Event> {
        self.0.lock().unwrap().clone()
    }
}

impl EventSink for MemorySink {
    fn emit(&self, event: Event) {
        self.0.lock().unwrap().push(event);
    }
}

/// A run's log file. Each event is written with `O_APPEND` and flushed immediately, so a
/// crash leaves a readable file that just lacks `run_finished`.
pub struct RunLog {
    run_id: uuid::Uuid,
    path: PathBuf,
    file: Mutex<File>,
}

impl RunLog {
    /// Creates the directory (mode 0750) and the run's file (mode 0640).
    ///
    /// # Errors
    ///
    /// When the directory or the file cannot be created.
    pub fn create(dir: &Path, run_id: uuid::Uuid, started: jiff::Timestamp) -> io::Result<Self> {
        create_private_dir(dir)?;
        let name = format!("{}_{run_id}.jsonl", started.strftime("%Y%m%dT%H%M%SZ"));
        let path = dir.join(name);
        let mut options = File::options();
        options.append(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o640);
        }
        let file = options.open(&path)?;
        Ok(Self {
            run_id,
            path,
            file: Mutex::new(file),
        })
    }

    #[must_use]
    pub fn path(&self) -> &Path {
        &self.path
    }
}

impl EventSink for RunLog {
    fn emit(&self, event: Event) {
        match &event {
            Event::StepOutput { .. } => tracing::debug!("{}", event.summary()),
            Event::Warning { .. } => tracing::warn!("{}", event.summary()),
            _ => tracing::info!("{}", event.summary()),
        }
        let record = Record {
            run_id: self.run_id,
            ts: jiff::Timestamp::now(),
            event,
        };
        let mut line = serde_json::to_string(&record).expect("event serializes");
        line.push('\n');
        let mut file = self
            .file
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        // A full disk must not stop a dpkg run halfway; the journal still has the summary.
        if let Err(e) = file.write_all(line.as_bytes()).and_then(|()| file.flush()) {
            tracing::error!(path = %self.path.display(), error = %e, "cannot write run log");
        }
    }
}

/// Mirrors events to another sink as well, such as the terminal.
pub struct Tee<'a>(pub &'a dyn EventSink, pub &'a dyn EventSink);

impl EventSink for Tee<'_> {
    fn emit(&self, event: Event) {
        self.1.emit(event.clone());
        self.0.emit(event);
    }
}

pub(crate) fn create_private_dir(dir: &Path) -> io::Result<()> {
    let mut builder = std::fs::DirBuilder::new();
    builder.recursive(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::DirBuilderExt;
        builder.mode(0o750);
    }
    builder.create(dir)
}

/// Run log files in `dir`, newest first (the names start with the start time).
fn run_files(dir: &Path) -> io::Result<Vec<PathBuf>> {
    let mut files: Vec<PathBuf> = std::fs::read_dir(dir)?
        .filter_map(Result::ok)
        .map(|e| e.path())
        .filter(|p| p.extension().is_some_and(|e| e == "jsonl"))
        .collect();
    files.sort();
    files.reverse();
    Ok(files)
}

/// Deletes run logs older than `retention_days`, always keeping the newest
/// [`KEEP_AT_LEAST`]. Returns how many it deleted.
///
/// # Errors
///
/// When the directory cannot be read. A file that cannot be deleted is skipped.
pub fn apply_retention(dir: &Path, retention_days: u32, now: SystemTime) -> io::Result<usize> {
    let files = match run_files(dir) {
        Ok(files) => files,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(0),
        Err(e) => return Err(e),
    };
    let max_age = Duration::from_secs(u64::from(retention_days) * 86_400);
    let mut deleted = 0;
    for path in files.iter().skip(KEEP_AT_LEAST) {
        let old = std::fs::metadata(path)
            .and_then(|m| m.modified())
            .is_ok_and(|m| now.duration_since(m).unwrap_or_default() > max_age);
        if old && std::fs::remove_file(path).is_ok() {
            deleted += 1;
        }
    }
    Ok(deleted)
}

/// One run, read back from its log for `patchd history`.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub struct RunSummary {
    pub run_id: uuid::Uuid,
    pub started_at: jiff::Timestamp,
    pub duration_ms: Option<u64>,
    pub filter: Option<Filter>,
    pub installed: usize,
    /// `crashed` when the log ends without `run_finished`.
    pub result: RunResult,
    pub reboot_required: bool,
    pub path: PathBuf,
}

/// Reads one run log. Lines that don't parse (a torn last line after a crash, or an
/// event from a newer version) are skipped.
///
/// # Errors
///
/// When the file cannot be read.
pub fn read_run(path: &Path) -> io::Result<Vec<Record>> {
    let text = std::fs::read_to_string(path)?;
    Ok(text
        .lines()
        .filter_map(|l| serde_json::from_str(l).ok())
        .collect())
}

#[must_use]
pub fn summarize(path: &Path, records: &[Record]) -> Option<RunSummary> {
    let first = records.first()?;
    let mut summary = RunSummary {
        run_id: first.run_id,
        started_at: first.ts,
        duration_ms: None,
        filter: None,
        installed: 0,
        result: RunResult::Crashed,
        reboot_required: false,
        path: path.to_path_buf(),
    };
    for r in records {
        match &r.event {
            Event::RunStarted { filter, .. } => summary.filter = Some(*filter),
            Event::RebootStatus { status } => summary.reboot_required = status.required,
            Event::RunFinished {
                result,
                installed,
                reboot_required,
                duration_ms,
                ..
            } => {
                summary.result = *result;
                summary.installed = installed.len();
                summary.reboot_required = *reboot_required;
                summary.duration_ms = Some(*duration_ms);
            }
            _ => {}
        }
    }
    Some(summary)
}

/// The newest `limit` runs in `dir`, newest first.
///
/// # Errors
///
/// When the directory cannot be read. A missing directory means no runs yet.
pub fn history(dir: &Path, limit: usize) -> io::Result<Vec<RunSummary>> {
    let files = match run_files(dir) {
        Ok(files) => files,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(Vec::new()),
        Err(e) => return Err(e),
    };
    Ok(files
        .iter()
        .filter_map(|p| summarize(p, &read_run(p).ok()?))
        .take(limit)
        .collect())
}

/// Marks a run `running` instead of `crashed` when it is the run holding the lock
/// ([`crate::lock::holder`]): its log lacks `run_finished` because it has not finished.
/// Matching the run ID matters: `scan --refresh` takes the same lock and writes no log.
pub fn mark_running(runs: &mut [RunSummary], holder: Option<&str>) {
    for run in runs {
        if run.result == RunResult::Crashed && holder == Some(run.run_id.to_string().as_str()) {
            run.result = RunResult::Running;
        }
    }
}

/// The log file of the run whose ID starts with `prefix`.
///
/// # Errors
///
/// When the directory cannot be read.
pub fn find_run(dir: &Path, prefix: &str) -> io::Result<Option<PathBuf>> {
    let files = match run_files(dir) {
        Ok(files) => files,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(e) => return Err(e),
    };
    Ok(files.into_iter().find(|p| {
        p.file_stem()
            .and_then(|s| s.to_str())
            .and_then(|s| s.split_once('_'))
            .is_some_and(|(_, id)| id.starts_with(prefix))
    }))
}

/// The runs directory under `root`.
#[must_use]
pub fn runs_dir(root: &Root) -> PathBuf {
    root.join(RUNS_DIR)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn host() -> HostInfo {
        HostInfo {
            hostname: "example".into(),
            os: crate::host::OsInfo::from_os_release(""),
            arch: "x86_64".into(),
            kernel: None,
        }
    }

    fn started() -> Event {
        Event::RunStarted {
            agent_version: "test".into(),
            host: host(),
            filter: Filter::Security,
            command: "apply".into(),
        }
    }

    fn finished(result: RunResult) -> Event {
        Event::RunFinished {
            result,
            installed: vec![Change {
                name: "libssl3".into(),
                from: Some("1".into()),
                to: "2".into(),
            }],
            failed: Vec::new(),
            reboot_required: true,
            duration_ms: 1234,
            message: None,
        }
    }

    #[test]
    fn writes_one_object_per_line_and_reads_back() {
        let dir = tempfile::tempdir().unwrap();
        let id = uuid::Uuid::now_v7();
        let log = RunLog::create(dir.path(), id, jiff::Timestamp::now()).unwrap();
        log.emit(started());
        log.emit(finished(RunResult::SuccessRebootRequired));

        let text = std::fs::read_to_string(log.path()).unwrap();
        let lines: Vec<serde_json::Value> = text
            .lines()
            .map(|l| serde_json::from_str(l).unwrap())
            .collect();
        assert_eq!(lines.len(), 2);
        assert_eq!(lines[0]["event"], "run_started");
        assert_eq!(lines[0]["run_id"], id.to_string());
        assert!(lines[0]["ts"].is_string());
        assert_eq!(lines[1]["result"], "success_reboot_required");

        let runs = history(dir.path(), 20).unwrap();
        assert_eq!(runs.len(), 1);
        assert_eq!(runs[0].result, RunResult::SuccessRebootRequired);
        assert_eq!(runs[0].installed, 1);
        assert_eq!(runs[0].duration_ms, Some(1234));
        assert_eq!(runs[0].filter, Some(Filter::Security));
        assert_eq!(
            find_run(dir.path(), &id.to_string()[..8])
                .unwrap()
                .as_deref(),
            Some(log.path())
        );
    }

    #[test]
    fn a_log_without_run_finished_is_crashed() {
        let dir = tempfile::tempdir().unwrap();
        let log = RunLog::create(dir.path(), uuid::Uuid::now_v7(), jiff::Timestamp::now()).unwrap();
        log.emit(started());
        // A torn last line, as a crash mid-write would leave.
        std::fs::OpenOptions::new()
            .append(true)
            .open(log.path())
            .unwrap()
            .write_all(b"{\"run_id\":")
            .unwrap();
        let runs = history(dir.path(), 20).unwrap();
        assert_eq!(runs[0].result, RunResult::Crashed);
        assert_eq!(runs[0].duration_ms, None);

        let id = runs[0].run_id.to_string();
        let mut live = runs.clone();
        mark_running(&mut live, Some(&id));
        assert_eq!(live[0].result, RunResult::Running);
        // Another holder, such as `scan --refresh`, says nothing about this run.
        let mut other = runs.clone();
        mark_running(&mut other, Some("scan --refresh"));
        assert_eq!(other[0].result, RunResult::Crashed);
        let mut done = runs;
        mark_running(&mut done, None);
        assert_eq!(done[0].result, RunResult::Crashed);
    }

    #[cfg(unix)]
    #[test]
    fn modes() {
        use std::os::unix::fs::PermissionsExt;
        let dir = tempfile::tempdir().unwrap();
        let runs = dir.path().join("runs");
        let log = RunLog::create(&runs, uuid::Uuid::now_v7(), jiff::Timestamp::now()).unwrap();
        let mode = |p: &Path| std::fs::metadata(p).unwrap().permissions().mode() & 0o777;
        // The umask can only remove bits.
        assert_eq!(mode(&runs) & !0o750, 0);
        assert_eq!(mode(log.path()) & !0o640, 0);
    }

    #[test]
    fn retention_keeps_the_newest_twenty() {
        let dir = tempfile::tempdir().unwrap();
        let old = SystemTime::now() - Duration::from_secs(200 * 86_400);
        for i in 0..25 {
            let p = dir
                .path()
                .join(format!("2026010{}T000000Z_{i:02}.jsonl", i / 10));
            std::fs::write(&p, "").unwrap();
            File::options()
                .write(true)
                .open(&p)
                .unwrap()
                .set_modified(old)
                .unwrap();
        }
        std::fs::write(dir.path().join("notes.txt"), "").unwrap();
        assert_eq!(
            apply_retention(dir.path(), 90, SystemTime::now()).unwrap(),
            5
        );
        assert_eq!(run_files(dir.path()).unwrap().len(), KEEP_AT_LEAST);
        assert!(dir.path().join("notes.txt").exists());
        // Nothing older than the retention is left beyond the 20, so a second pass is a no-op.
        assert_eq!(
            apply_retention(dir.path(), 90, SystemTime::now()).unwrap(),
            0
        );
    }

    #[test]
    fn retention_keeps_recent_files() {
        let dir = tempfile::tempdir().unwrap();
        for i in 0..30 {
            std::fs::write(dir.path().join(format!("20260101T0000{i:02}Z_x.jsonl")), "").unwrap();
        }
        assert_eq!(
            apply_retention(dir.path(), 90, SystemTime::now()).unwrap(),
            0
        );
        assert_eq!(
            apply_retention(&dir.path().join("missing"), 90, SystemTime::now()).unwrap(),
            0
        );
    }
}
