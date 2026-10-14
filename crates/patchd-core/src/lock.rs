//! The run lock, `/run/patchd/lock`: an exclusive, non-blocking `flock`. The kernel
//! releases it when the process exits, so a crash never leaves a stale lock.
//!
//! Only `apply` and `scan --refresh` take it. A plain `scan`, `plan`, `status` or
//! `history` never waits for it.

use std::fs::File;
use std::io::{self, Read as _, Seek as _, Write as _};
use std::path::{Path, PathBuf};

use crate::root::Root;

pub const LOCK_PATH: &str = "/run/patchd/lock";

#[derive(Debug, thiserror::Error)]
pub enum LockError {
    #[error("another patchd run is active{}", holder_text(*.pid, .run_id.as_deref()))]
    Held {
        pid: Option<u32>,
        run_id: Option<String>,
    },
    #[error("{path}: {source}")]
    Io { path: PathBuf, source: io::Error },
}

fn holder_text(pid: Option<u32>, run_id: Option<&str>) -> String {
    match (pid, run_id) {
        (Some(pid), Some(id)) => format!(" (pid {pid}, run {id})"),
        (Some(pid), None) => format!(" (pid {pid})"),
        _ => String::new(),
    }
}

/// Held for as long as it lives.
#[derive(Debug)]
pub struct RunLock {
    _file: File,
}

impl RunLock {
    /// Takes the lock and writes the PID and run ID into it, so a second run can say who
    /// holds it.
    ///
    /// # Errors
    ///
    /// [`LockError::Held`] when another process holds it; [`LockError::Io`] when the
    /// file cannot be created, typically without root.
    pub fn acquire(root: &Root, run_id: &str) -> Result<Self, LockError> {
        let path = root.join(LOCK_PATH);
        let io_err = |source| LockError::Io {
            path: path.clone(),
            source,
        };
        if let Some(dir) = path.parent() {
            crate::runlog::create_private_dir(dir).map_err(io_err)?;
        }
        let mut file = File::options()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .open(&path)
            .map_err(io_err)?;
        if !try_lock(&file).map_err(io_err)? {
            let mut text = String::new();
            let _ = file.read_to_string(&mut text);
            let (pid, run_id) = parse_holder(&text);
            return Err(LockError::Held { pid, run_id });
        }
        file.set_len(0).map_err(io_err)?;
        file.rewind().map_err(io_err)?;
        writeln!(file, "{}\n{run_id}", std::process::id()).map_err(io_err)?;
        file.flush().map_err(io_err)?;
        Ok(Self { _file: file })
    }
}

fn parse_holder(text: &str) -> (Option<u32>, Option<String>) {
    let mut lines = text.lines();
    let pid = lines.next().and_then(|l| l.trim().parse().ok());
    let run_id = lines
        .next()
        .map(str::trim)
        .filter(|s| !s.is_empty())
        .map(str::to_owned);
    (pid, run_id)
}

#[cfg(unix)]
fn try_lock(file: &File) -> io::Result<bool> {
    use rustix::fs::{FlockOperation, flock};
    match flock(file, FlockOperation::NonBlockingLockExclusive) {
        Ok(()) => Ok(true),
        Err(e) if e == rustix::io::Errno::WOULDBLOCK => Ok(false),
        Err(e) => Err(e.into()),
    }
}

#[cfg(not(unix))]
fn try_lock(_file: &File) -> io::Result<bool> {
    Err(io::Error::other("the run lock is only supported on Linux"))
}

/// The lock path under `root`.
#[must_use]
pub fn lock_path(root: &Root) -> PathBuf {
    root.join(LOCK_PATH)
}

/// For tests: whether some process holds the lock right now. It takes the lock for an
/// instant to find out, so never call it where a run could be starting; use [`holder`].
#[must_use]
pub fn is_held(path: &Path) -> bool {
    File::open(path).is_ok_and(|f| matches!(try_lock(&f), Ok(false)))
}

/// The run ID in the lock file, when the process that wrote it is still a running
/// patchd. Reads only: it never touches the lock, so `status` cannot get in the way of
/// a run that is starting. The file outlives its holder, hence the process check.
#[must_use]
pub fn holder(root: &Root) -> Option<String> {
    let text = std::fs::read_to_string(lock_path(root)).ok()?;
    let (pid, run_id) = parse_holder(&text);
    let comm = std::fs::read_to_string(root.join(format!("/proc/{}/comm", pid?))).ok()?;
    (comm.trim() == "patchd").then_some(run_id).flatten()
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;

    #[test]
    fn second_acquire_names_the_holder() {
        let dir = tempfile::tempdir().unwrap();
        let root = Root::at(dir.path());
        let first = RunLock::acquire(&root, "run-1").unwrap();
        assert!(is_held(&lock_path(&root)));
        let err = RunLock::acquire(&root, "run-2").unwrap_err();
        let LockError::Held { pid, run_id } = &err else {
            panic!("{err}")
        };
        assert_eq!(*pid, Some(std::process::id()));
        assert_eq!(run_id.as_deref(), Some("run-1"));
        assert!(
            err.to_string()
                .contains("another patchd run is active (pid"),
            "{err}"
        );

        drop(first);
        assert!(!is_held(&lock_path(&root)));
        let _again = RunLock::acquire(&root, "run-3").unwrap();
        let text = std::fs::read_to_string(lock_path(&root)).unwrap();
        assert!(text.ends_with("\nrun-3\n"), "{text:?}");
    }

    #[test]
    fn holder_needs_a_live_patchd_process() {
        let dir = tempfile::tempdir().unwrap();
        let root = Root::at(dir.path());
        assert_eq!(holder(&root), None);
        std::fs::create_dir_all(root.join("/run/patchd")).unwrap();
        std::fs::write(lock_path(&root), "4242\nrun-7\n").unwrap();
        assert_eq!(holder(&root), None, "no such process");
        std::fs::create_dir_all(root.join("/proc/4242")).unwrap();
        std::fs::write(root.join("/proc/4242/comm"), "bash\n").unwrap();
        assert_eq!(holder(&root), None, "the PID was reused by something else");
        std::fs::write(root.join("/proc/4242/comm"), "patchd\n").unwrap();
        assert_eq!(holder(&root).as_deref(), Some("run-7"));
    }

    #[test]
    fn holder_parsing() {
        assert_eq!(parse_holder("42\nabc\n"), (Some(42), Some("abc".into())));
        assert_eq!(parse_holder(""), (None, None));
    }
}
