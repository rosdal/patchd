//! Every external command goes through a [`Runner`], so the locale, timeouts and logging
//! are the same everywhere and tests can swap in recorded output.

use std::collections::{HashMap, VecDeque};
use std::process::Stdio;
use std::sync::Mutex;
use std::time::{Duration, Instant};

use async_trait::async_trait;
use tokio::io::{AsyncBufReadExt, AsyncRead, BufReader};
use tracing::Instrument;

/// Default timeout for `dpkg-query`.
pub const QUERY_TIMEOUT: Duration = Duration::from_secs(30);
/// Default timeout for `apt-get -s` and other resolver runs.
pub const RESOLVE_TIMEOUT: Duration = Duration::from_secs(120);

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Cmd {
    pub program: &'static str,
    pub args: Vec<String>,
    pub timeout: Duration,
    /// Exit codes that count as success. Anything else is [`RunError::Failed`].
    pub ok_codes: &'static [i32],
    /// Extra environment variables, on top of the C locale.
    pub env: Vec<(&'static str, String)>,
    /// For dpkg and rpm runs, which must never be interrupted: the child gets its own
    /// process group, so a Ctrl-C in the terminal or a signal to patchd's group never
    /// reaches it, and it is never killed, not at its timeout and not when patchd stops
    /// waiting for it. The timeout only logs that it is taking long.
    pub protected: bool,
}

impl Cmd {
    #[must_use]
    pub fn new<I, S>(program: &'static str, args: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        Self {
            program,
            args: args.into_iter().map(Into::into).collect(),
            timeout: QUERY_TIMEOUT,
            ok_codes: &[0],
            env: Vec::new(),
            protected: false,
        }
    }

    #[must_use]
    pub fn env(mut self, key: &'static str, value: impl Into<String>) -> Self {
        self.env.push((key, value.into()));
        self
    }

    #[must_use]
    pub fn protected(mut self) -> Self {
        self.protected = true;
        self
    }

    #[must_use]
    pub fn timeout(mut self, timeout: Duration) -> Self {
        self.timeout = timeout;
        self
    }

    #[must_use]
    pub fn ok_codes(mut self, codes: &'static [i32]) -> Self {
        self.ok_codes = codes;
        self
    }

    /// `program arg1 arg2`, the key [`FixtureRunner`] looks commands up by.
    #[must_use]
    pub fn command_line(&self) -> String {
        std::iter::once(self.program)
            .chain(self.args.iter().map(String::as_str))
            .collect::<Vec<_>>()
            .join(" ")
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CmdOutput {
    pub status: i32,
    pub stdout: String,
    pub stderr: String,
}

impl CmdOutput {
    #[must_use]
    pub fn ok(stdout: impl Into<String>) -> Self {
        Self {
            status: 0,
            stdout: stdout.into(),
            stderr: String::new(),
        }
    }
}

/// Which output stream a line came from.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum Stream {
    Stdout,
    Stderr,
}

/// Receives output lines as a command produces them.
pub type LineSink<'a> = &'a (dyn Fn(Stream, &str) + Send + Sync);

#[async_trait]
pub trait Runner: Send + Sync {
    async fn run(&self, cmd: &Cmd) -> Result<CmdOutput, RunError>;

    /// Like [`Runner::run`], but hands each output line to `on_line` as it arrives, so a
    /// long install shows up in the run log while it runs. The full output is still
    /// returned.
    ///
    /// The default runs the command to completion and then replays its output.
    async fn run_streaming(&self, cmd: &Cmd, on_line: LineSink<'_>) -> Result<CmdOutput, RunError> {
        let result = self.run(cmd).await;
        match &result {
            Ok(out) => {
                out.stdout.lines().for_each(|l| on_line(Stream::Stdout, l));
                out.stderr.lines().for_each(|l| on_line(Stream::Stderr, l));
            }
            Err(RunError::Failed { stderr, .. }) => {
                stderr.lines().for_each(|l| on_line(Stream::Stderr, l));
            }
            Err(_) => {}
        }
        result
    }
}

#[derive(Debug, thiserror::Error)]
pub enum RunError {
    #[error("{0} not found")]
    NotFound(&'static str),
    #[error("{program} timed out after {secs}s")]
    Timeout { program: &'static str, secs: u64 },
    #[error("{program} exited {code}: {stderr}")]
    Failed {
        program: &'static str,
        code: i32,
        stderr: String,
    },
    #[error(transparent)]
    Io(#[from] std::io::Error),
}

impl RunError {
    /// One line for a run log message: a failed command's last stderr line, which is
    /// where apt and dnf put the actual error. The full output is in the step log.
    #[must_use]
    pub fn brief(&self) -> String {
        match self {
            Self::Failed {
                program,
                code,
                stderr,
            } => {
                let last = stderr
                    .lines()
                    .rev()
                    .find(|l| !l.trim().is_empty())
                    .unwrap_or("");
                format!("{program} exited {code}: {last}")
            }
            other => other.to_string(),
        }
    }
}

/// Fails with [`RunError::Failed`] unless the exit code is one the caller expects.
fn check_status(cmd: &Cmd, out: CmdOutput) -> Result<CmdOutput, RunError> {
    if cmd.ok_codes.contains(&out.status) {
        Ok(out)
    } else {
        Err(RunError::Failed {
            program: cmd.program,
            code: out.status,
            stderr: out.stderr.trim().to_owned(),
        })
    }
}

/// Runs real processes with the C locale, so tool output is English and parseable.
#[derive(Debug, Default, Clone, Copy)]
pub struct SystemRunner;

#[async_trait]
impl Runner for SystemRunner {
    async fn run(&self, cmd: &Cmd) -> Result<CmdOutput, RunError> {
        let span = tracing::debug_span!("cmd", program = cmd.program, args = ?cmd.args);
        async {
            let started = Instant::now();
            let child = spawn(cmd)?;

            let output = tokio::time::timeout(cmd.timeout, child.wait_with_output())
                .await
                .map_err(|_| RunError::Timeout {
                    program: cmd.program,
                    secs: cmd.timeout.as_secs(),
                })??;

            // A process killed by a signal has no exit code.
            let status = output.status.code().unwrap_or(-1);
            let out = CmdOutput {
                status,
                stdout: String::from_utf8_lossy(&output.stdout).into_owned(),
                stderr: String::from_utf8_lossy(&output.stderr).into_owned(),
            };
            tracing::debug!(
                exit = status,
                elapsed_ms = u64::try_from(started.elapsed().as_millis()).unwrap_or(u64::MAX),
                "command finished"
            );
            tracing::trace!(stdout = %out.stdout, stderr = %out.stderr);
            check_status(cmd, out)
        }
        .instrument(span)
        .await
    }

    async fn run_streaming(&self, cmd: &Cmd, on_line: LineSink<'_>) -> Result<CmdOutput, RunError> {
        let span = tracing::debug_span!("cmd", program = cmd.program, args = ?cmd.args);
        async {
            let run = stream(cmd, on_line);
            tokio::pin!(run);
            let out = match tokio::time::timeout(cmd.timeout, &mut run).await {
                Ok(out) => out?,
                Err(_) if cmd.protected => {
                    let msg = format!(
                        "patchd: {} is still running after {}s; waiting for it rather than interrupting it",
                        cmd.program,
                        cmd.timeout.as_secs()
                    );
                    tracing::warn!("{msg}");
                    on_line(Stream::Stderr, &msg);
                    run.await?
                }
                Err(_) => {
                    return Err(RunError::Timeout {
                        program: cmd.program,
                        secs: cmd.timeout.as_secs(),
                    });
                }
            };
            tracing::debug!(exit = out.status, "command finished");
            check_status(cmd, out)
        }
        .instrument(span)
        .await
    }
}

fn spawn(cmd: &Cmd) -> Result<tokio::process::Child, RunError> {
    let mut command = tokio::process::Command::new(cmd.program);
    command
        .args(&cmd.args)
        .env("LC_ALL", "C")
        .env("LANG", "C")
        .env_remove("LANGUAGE")
        .envs(cmd.env.iter().map(|(k, v)| (k, v)))
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(!cmd.protected);
    #[cfg(unix)]
    if cmd.protected {
        command.process_group(0);
    }
    command.spawn().map_err(|e| match e.kind() {
        std::io::ErrorKind::NotFound => RunError::NotFound(cmd.program),
        _ => RunError::Io(e),
    })
}

/// How long to keep reading output after the process has exited. A daemon started by a
/// maintainer script can inherit the pipes and hold them open for ever.
const DRAIN_AFTER_EXIT: Duration = Duration::from_secs(5);

/// Reads stdout and stderr line by line as they arrive, until the process has exited and
/// both pipes are closed, or [`DRAIN_AFTER_EXIT`] after the exit. Bytes that are not
/// UTF-8 are replaced, never an error: a read error must not abandon a running dpkg.
async fn stream(cmd: &Cmd, on_line: LineSink<'_>) -> Result<CmdOutput, RunError> {
    let mut child = spawn(cmd)?;
    let mut stdout = Lines::new(child.stdout.take().expect("piped stdout"));
    let mut stderr = Lines::new(child.stderr.take().expect("piped stderr"));
    let (mut out, mut err) = (String::new(), String::new());
    let mut exited: Option<std::process::ExitStatus> = None;
    let drain = tokio::time::sleep(Duration::MAX);
    tokio::pin!(drain);
    loop {
        if exited.is_some() && !stdout.open && !stderr.open {
            break;
        }
        tokio::select! {
            line = stdout.next(), if stdout.open => if let Some(l) = line {
                on_line(Stream::Stdout, &l);
                out.push_str(&l);
                out.push('\n');
            },
            line = stderr.next(), if stderr.open => if let Some(l) = line {
                on_line(Stream::Stderr, &l);
                err.push_str(&l);
                err.push('\n');
            },
            status = child.wait(), if exited.is_none() => {
                exited = Some(status?);
                drain.as_mut().reset(tokio::time::Instant::now() + DRAIN_AFTER_EXIT);
            },
            () = &mut drain, if exited.is_some() => break,
        }
    }
    let status = exited.and_then(|s| s.code()).unwrap_or(-1);
    Ok(CmdOutput {
        status,
        stdout: out,
        stderr: err,
    })
}

/// Lines from a pipe, with invalid UTF-8 replaced. `next` is cancel-safe: a partly read
/// line stays in the buffer for the next call.
struct Lines<R> {
    reader: BufReader<R>,
    buf: Vec<u8>,
    open: bool,
}

impl<R: AsyncRead + Unpin> Lines<R> {
    fn new(reader: R) -> Self {
        Self {
            reader: BufReader::new(reader),
            buf: Vec::new(),
            open: true,
        }
    }

    /// The next line, or `None` (and `open` false) at end of file or on a read error.
    async fn next(&mut self) -> Option<String> {
        match self.reader.read_until(b'\n', &mut self.buf).await {
            Ok(0) | Err(_) => {
                self.open = false;
                // A last line without a newline.
                (!self.buf.is_empty()).then(|| self.take())
            }
            Ok(_) => Some(self.take()),
        }
    }

    fn take(&mut self) -> String {
        let mut line = String::from_utf8_lossy(&self.buf).into_owned();
        self.buf.clear();
        while line.ends_with(['\n', '\r']) {
            line.pop();
        }
        line
    }
}

/// Replays recorded output. Panics on any command it was not given, so a test also
/// proves the code runs only what it means to.
#[derive(Debug, Default)]
pub struct FixtureRunner {
    outputs: Mutex<HashMap<String, VecDeque<CmdOutput>>>,
    seen: Mutex<Vec<String>>,
}

impl FixtureRunner {
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Records the output for `command_line` (`program arg1 arg2`). Recording the same
    /// command again queues an output for its next run; the last one repeats.
    ///
    /// # Panics
    ///
    /// If the lock is poisoned by a panicking test.
    #[must_use]
    pub fn with(self, command_line: impl Into<String>, output: CmdOutput) -> Self {
        self.outputs
            .lock()
            .unwrap()
            .entry(command_line.into())
            .or_default()
            .push_back(output);
        self
    }

    /// The command lines run so far, in order.
    ///
    /// # Panics
    ///
    /// If the lock is poisoned by a panicking test.
    #[must_use]
    pub fn seen(&self) -> Vec<String> {
        self.seen.lock().unwrap().clone()
    }
}

#[async_trait]
impl Runner for FixtureRunner {
    async fn run(&self, cmd: &Cmd) -> Result<CmdOutput, RunError> {
        let line = cmd.command_line();
        let out = {
            let mut outputs = self.outputs.lock().unwrap();
            let Some(queue) = outputs.get_mut(&line) else {
                panic!("FixtureRunner: unexpected command `{line}`");
            };
            if queue.len() > 1 {
                queue.pop_front().expect("queue is not empty")
            } else {
                queue.front().expect("output was recorded").clone()
            }
        };
        self.seen.lock().unwrap().push(line);
        check_status(cmd, out)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn missing_binary_is_not_found() {
        let err = SystemRunner
            .run(&Cmd::new("patchd-no-such-binary", Vec::<String>::new()))
            .await
            .unwrap_err();
        assert!(matches!(err, RunError::NotFound("patchd-no-such-binary")));
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn timeout_fires() {
        let started = Instant::now();
        let err = SystemRunner
            .run(&Cmd::new("sleep", ["5"]).timeout(Duration::from_millis(100)))
            .await
            .unwrap_err();
        assert!(matches!(
            err,
            RunError::Timeout {
                program: "sleep",
                ..
            }
        ));
        assert!(started.elapsed() < Duration::from_secs(2));
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn locale_is_c() {
        let out = SystemRunner
            .run(&Cmd::new("env", Vec::<String>::new()))
            .await
            .unwrap();
        let vars: Vec<&str> = out.stdout.lines().collect();
        assert!(vars.contains(&"LC_ALL=C"));
        assert!(vars.contains(&"LANG=C"));
        assert!(!vars.iter().any(|v| v.starts_with("LANGUAGE=")));
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn unexpected_exit_code_fails_and_expected_one_does_not() {
        let err = SystemRunner
            .run(&Cmd::new("sh", ["-c", "echo oops >&2; exit 3"]))
            .await
            .unwrap_err();
        assert!(
            matches!(err, RunError::Failed { code: 3, ref stderr, .. } if stderr == "oops"),
            "{err:?}"
        );
        let out = SystemRunner
            .run(&Cmd::new("sh", ["-c", "exit 3"]).ok_codes(&[0, 3]))
            .await
            .unwrap();
        assert_eq!(out.status, 3);
    }

    #[tokio::test]
    async fn fixture_runner_replays_and_checks_status() {
        let runner = FixtureRunner::new()
            .with("tool --list", CmdOutput::ok("a\nb\n"))
            .with(
                "tool --fail",
                CmdOutput {
                    status: 100,
                    stdout: String::new(),
                    stderr: "E: broken".into(),
                },
            );
        let out = runner.run(&Cmd::new("tool", ["--list"])).await.unwrap();
        assert_eq!(out.stdout, "a\nb\n");
        assert!(runner.run(&Cmd::new("tool", ["--fail"])).await.is_err());
        assert_eq!(runner.seen(), ["tool --list", "tool --fail"]);
    }

    #[tokio::test]
    async fn fixture_runner_queues_outputs() {
        let runner = FixtureRunner::new()
            .with("tool", CmdOutput::ok("first"))
            .with("tool", CmdOutput::ok("second"));
        let cmd = Cmd::new("tool", Vec::<String>::new());
        assert_eq!(runner.run(&cmd).await.unwrap().stdout, "first");
        assert_eq!(runner.run(&cmd).await.unwrap().stdout, "second");
        assert_eq!(runner.run(&cmd).await.unwrap().stdout, "second");
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn streaming_delivers_lines_from_both_streams() {
        let lines = Mutex::new(Vec::new());
        let cmd = Cmd::new("sh", ["-c", r#"echo one; echo two >&2; echo "$PATCHD_X""#])
            .env("PATCHD_X", "three")
            .protected();
        let out = SystemRunner
            .run_streaming(&cmd, &|s, l| lines.lock().unwrap().push((s, l.to_owned())))
            .await
            .unwrap();
        let mut lines = lines.into_inner().unwrap();
        lines.sort();
        assert_eq!(
            lines,
            [
                (Stream::Stdout, "one".to_owned()),
                (Stream::Stdout, "three".to_owned()),
                (Stream::Stderr, "two".to_owned()),
            ]
        );
        assert_eq!(out.stdout, "one\nthree\n");
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn streaming_checks_status_and_timeout() {
        let err = SystemRunner
            .run_streaming(&Cmd::new("sh", ["-c", "exit 2"]), &|_, _| {})
            .await
            .unwrap_err();
        assert!(matches!(err, RunError::Failed { code: 2, .. }), "{err:?}");
        let slow = Cmd::new("sleep", ["5"]).timeout(Duration::from_millis(100));
        let err = SystemRunner
            .run_streaming(&slow, &|_, _| {})
            .await
            .unwrap_err();
        assert!(matches!(err, RunError::Timeout { .. }), "{err:?}");
    }

    /// Latin-1 from a maintainer script must not end the read loop.
    #[cfg(unix)]
    #[tokio::test]
    async fn streaming_survives_invalid_utf8() {
        let cmd = Cmd::new("sh", ["-c", r"printf 'caf\351\nnext\n'"]);
        let out = SystemRunner.run_streaming(&cmd, &|_, _| {}).await.unwrap();
        assert_eq!(out.stdout, "caf\u{fffd}\nnext\n");
    }

    /// A daemon that inherits stdout keeps the pipe open after the command exits.
    #[cfg(unix)]
    #[tokio::test]
    async fn streaming_returns_after_exit_even_if_the_pipe_stays_open() {
        let started = Instant::now();
        let cmd = Cmd::new("sh", ["-c", "echo started; sleep 30 & exit 0"])
            .timeout(Duration::from_secs(20));
        let out = SystemRunner.run_streaming(&cmd, &|_, _| {}).await.unwrap();
        assert_eq!(out.stdout, "started\n");
        assert!(
            started.elapsed() < DRAIN_AFTER_EXIT + Duration::from_secs(3),
            "{:?}",
            started.elapsed()
        );
    }

    /// A protected command is waited for past its timeout, never killed.
    #[cfg(unix)]
    #[tokio::test]
    async fn protected_command_outlives_its_timeout() {
        let lines = Mutex::new(Vec::new());
        let cmd = Cmd::new("sh", ["-c", "sleep 1; echo done"])
            .timeout(Duration::from_millis(200))
            .protected();
        let out = SystemRunner
            .run_streaming(&cmd, &|_, l| lines.lock().unwrap().push(l.to_owned()))
            .await
            .unwrap();
        assert_eq!(out.stdout, "done\n");
        assert!(
            lines
                .into_inner()
                .unwrap()
                .iter()
                .any(|l| l.contains("still running"))
        );
    }

    #[tokio::test]
    #[should_panic(expected = "unexpected command `tool --other`")]
    async fn fixture_runner_panics_on_unexpected_command() {
        let _ = FixtureRunner::new()
            .run(&Cmd::new("tool", ["--other"]))
            .await;
    }
}
