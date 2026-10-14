//! Every external command goes through a [`Runner`], so the locale, timeouts and logging
//! are the same everywhere and tests can swap in recorded output.

use std::collections::HashMap;
use std::process::Stdio;
use std::sync::Mutex;
use std::time::{Duration, Instant};

use async_trait::async_trait;
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
        }
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

#[async_trait]
pub trait Runner: Send + Sync {
    async fn run(&self, cmd: &Cmd) -> Result<CmdOutput, RunError>;
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
            let child = tokio::process::Command::new(cmd.program)
                .args(&cmd.args)
                .env("LC_ALL", "C")
                .env("LANG", "C")
                .env_remove("LANGUAGE")
                .stdin(Stdio::null())
                .stdout(Stdio::piped())
                .stderr(Stdio::piped())
                .kill_on_drop(true)
                .spawn()
                .map_err(|e| match e.kind() {
                    std::io::ErrorKind::NotFound => RunError::NotFound(cmd.program),
                    _ => RunError::Io(e),
                })?;

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
}

/// Replays recorded output. Panics on any command it was not given, so a test also
/// proves the code runs only what it means to.
#[derive(Debug, Default)]
pub struct FixtureRunner {
    outputs: HashMap<String, CmdOutput>,
    seen: Mutex<Vec<String>>,
}

impl FixtureRunner {
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Records the output for `command_line` (`program arg1 arg2`).
    #[must_use]
    pub fn with(mut self, command_line: impl Into<String>, output: CmdOutput) -> Self {
        self.outputs.insert(command_line.into(), output);
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
        let Some(out) = self.outputs.get(&line) else {
            panic!("FixtureRunner: unexpected command `{line}`");
        };
        self.seen.lock().unwrap().push(line);
        check_status(cmd, out.clone())
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
    #[should_panic(expected = "unexpected command `tool --other`")]
    async fn fixture_runner_panics_on_unexpected_command() {
        let _ = FixtureRunner::new()
            .run(&Cmd::new("tool", ["--other"]))
            .await;
    }
}
