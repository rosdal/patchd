//! `patchd plan` against recorded fixtures, and `patchd config`.
#![cfg(debug_assertions)]

use std::path::{Path, PathBuf};
use std::process::{Command, Output};

fn fixture(name: &str) -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../patchd-core/tests/fixtures/apt")
        .join(name)
}

fn patchd(args: &[&str], config: &Path) -> Output {
    Command::new(env!("CARGO_BIN_EXE_patchd"))
        .arg("--config")
        .arg(config)
        .args(args)
        .env_remove("RUST_LOG")
        .env_remove("PATCHD_APPLY_FILTER")
        .output()
        .unwrap()
}

fn config(text: &str) -> (tempfile::TempDir, PathBuf) {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("config.toml");
    std::fs::write(&path, text).unwrap();
    (dir, path)
}

fn plan(fixture_name: &str, cfg: &str, extra: &[&str]) -> Output {
    let (_dir, path) = config(cfg);
    let dir = fixture(fixture_name);
    let mut args = vec!["plan", "--fixtures", dir.to_str().unwrap()];
    args.extend(extra);
    patchd(&args, &path)
}

#[test]
fn plan_all_table() {
    // `--all` with nothing excluded plans from the dist-upgrade simulation alone.
    let out = plan("edge-cases", "", &["--all"]);
    assert_eq!(out.status.code(), Some(10));
    let stdout = String::from_utf8(out.stdout).unwrap();
    let stdout: String = stdout.lines().skip(1).collect::<Vec<_>>().join("\n");
    insta::assert_snapshot!("plan_all_edge_cases", stdout);
}

#[test]
fn plan_json_is_a_plan() {
    let out = plan("debian-12", "[apply]\nfilter = \"all\"\n", &["--json"]);
    assert_eq!(out.status.code(), Some(10));
    let json: serde_json::Value = serde_json::from_slice(&out.stdout).unwrap();
    assert_eq!(json["filter"], "all");
    assert!(json["id"].is_string());
    assert_eq!(json["providers"][0]["id"], "apt");
}

#[test]
fn empty_plan_exits_0() {
    let out = plan("no-updates", "", &["--all"]);
    assert_eq!(out.status.code(), Some(0));
    assert!(
        String::from_utf8(out.stdout)
            .unwrap()
            .contains("will install 0, skip 0 held, 0 removals")
    );
}

#[test]
fn config_check_reports_line_numbers() {
    let (_d, good) = config("[apply]\nfilter = \"all\"\n");
    let out = patchd(&["config", "check"], &good);
    assert_eq!(out.status.code(), Some(0));

    let (_d, bad) = config("[apply]\nfilter = \"all\"\nallow_removal = true\n");
    let out = patchd(&["config", "check"], &bad);
    assert_eq!(out.status.code(), Some(1));
    let stderr = String::from_utf8(out.stderr).unwrap();
    assert!(stderr.contains("line 3"), "{stderr}");
}

#[test]
fn config_show_is_the_effective_config() {
    let (_d, path) = config("[reboot]\nmode = \"if-required\"\n");
    let out = Command::new(env!("CARGO_BIN_EXE_patchd"))
        .arg("--config")
        .arg(&path)
        .args(["config", "show"])
        .env("PATCHD_APPLY_FILTER", "all")
        .output()
        .unwrap();
    assert_eq!(out.status.code(), Some(0));
    let stdout = String::from_utf8(out.stdout).unwrap();
    assert!(stdout.contains("filter = \"all\""), "{stdout}");
    assert!(stdout.contains("mode = \"if-required\""), "{stdout}");
    assert!(stdout.contains("retention_days = 90"), "{stdout}");
}

#[cfg(unix)]
#[test]
fn apply_without_root_is_refused_before_anything_runs() {
    if patchd_is_root() {
        eprintln!("running as root; skipping, apply would really run");
        return;
    }
    let (_d, path) = config("");
    let out = patchd(&["apply", "--yes"], &path);
    assert_eq!(out.status.code(), Some(4));
    assert!(String::from_utf8(out.stderr).unwrap().contains("sudo"));
}

#[cfg(unix)]
fn patchd_is_root() -> bool {
    std::fs::read_to_string("/proc/self/status").is_ok_and(|s| {
        s.lines()
            .find(|l| l.starts_with("Uid:"))
            .and_then(|l| l.split_whitespace().nth(2))
            == Some("0")
    })
}
