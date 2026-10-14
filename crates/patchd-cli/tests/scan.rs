//! `patchd scan` against recorded fixtures, through the hidden debug-only `--fixtures` flag.
#![cfg(debug_assertions)]

use std::path::{Path, PathBuf};
use std::process::{Command, Output};

fn fixture(name: &str) -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../patchd-core/tests/fixtures/apt")
        .join(name)
}

fn scan(fixture_name: &str, extra: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_patchd"))
        .arg("scan")
        .arg("--fixtures")
        .arg(fixture(fixture_name))
        .args(extra)
        .env_remove("RUST_LOG")
        .output()
        .unwrap()
}

fn stdout(out: &Output) -> String {
    String::from_utf8(out.stdout.clone()).unwrap()
}

/// The report with the fields that change per build or run replaced.
fn stable_json(out: &Output) -> serde_json::Value {
    let mut json: serde_json::Value = serde_json::from_slice(&out.stdout).unwrap();
    json["scanned_at"] = "[scanned_at]".into();
    json["agent_version"] = "[agent_version]".into();
    json
}

#[test]
fn table_golden_edge_cases() {
    let out = scan("edge-cases", &[]);
    assert_eq!(out.status.code(), Some(11));
    insta::assert_snapshot!("table_edge_cases", stdout(&out));
    insta::assert_snapshot!(
        "table_edge_cases_stderr",
        String::from_utf8(out.stderr).unwrap()
    );
}

#[test]
fn table_golden_held() {
    let out = scan("ubuntu-24.04-held", &[]);
    insta::assert_snapshot!("table_ubuntu_24_04_held", stdout(&out));
}

#[test]
fn table_golden_packages() {
    let out = scan("debian-13-held", &["--packages"]);
    insta::assert_snapshot!("table_debian_13_held_packages", stdout(&out));
}

#[test]
fn json_golden_edge_cases() {
    let out = scan("edge-cases", &["--json"]);
    assert_eq!(out.status.code(), Some(11));
    assert!(
        out.stderr.is_empty(),
        "{}",
        String::from_utf8_lossy(&out.stderr)
    );
    insta::assert_json_snapshot!("json_edge_cases", stable_json(&out));
}

#[test]
fn json_is_one_object_and_nothing_else() {
    let out = scan("ubuntu-24.04", &["--json"]);
    let text = stdout(&out);
    let json: serde_json::Value = serde_json::from_str(&text).unwrap();
    assert!(json.is_object());
    assert_eq!(json["schema_version"], 1);
    assert!(text.ends_with("}\n"));
}

#[test]
fn json_validates_against_generated_schema() {
    let schema_path =
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../../protocol/schemas/scan-report.json");
    let schema: serde_json::Value =
        serde_json::from_str(&std::fs::read_to_string(schema_path).unwrap()).unwrap();
    let validator = jsonschema::validator_for(&schema).unwrap();
    for name in [
        "edge-cases",
        "ubuntu-24.04-held",
        "debian-12",
        "broken",
        "no-updates",
    ] {
        let out = scan(name, &["--json"]);
        let report: serde_json::Value = serde_json::from_slice(&out.stdout).unwrap();
        let errors: Vec<String> = validator
            .iter_errors(&report)
            .map(|e| e.to_string())
            .collect();
        assert!(errors.is_empty(), "{name}: {errors:?}");
    }
}

#[test]
fn quiet_prints_nothing() {
    let out = scan("debian-12", &["--quiet"]);
    assert_eq!(out.status.code(), Some(11));
    assert!(out.stdout.is_empty());
    assert!(out.stderr.is_empty());
}

#[test]
fn quiet_hides_errors_that_stop_the_scan() {
    let out = scan("debian-12", &["--quiet", "--refresh"]);
    assert_eq!(out.status.code(), Some(1));
    assert!(out.stdout.is_empty());
    assert!(out.stderr.is_empty());
    let loud = scan("debian-12", &["--refresh"]);
    assert!(!loud.stderr.is_empty());
}

#[test]
fn exit_codes() {
    let cases = [
        ("no-updates", 0),
        ("ubuntu-24.04-held", 10), // the held security update doesn't count
        ("debian-12", 11),
        ("edge-cases", 11),
        ("broken", 1),
    ];
    for (name, code) in cases {
        for mode in [&[][..], &["--json"], &["--quiet"]] {
            assert_eq!(
                scan(name, mode).status.code(),
                Some(code),
                "{name} {mode:?}"
            );
        }
    }
}

#[test]
fn usage_errors_exit_2() {
    assert_eq!(scan("debian-12", &["--bogus"]).status.code(), Some(2));
    assert_eq!(
        scan("debian-12", &["--provider", "dnf"]).status.code(),
        Some(2)
    );
    assert_eq!(
        scan("debian-12", &["--json", "--quiet"]).status.code(),
        Some(2)
    );
}

#[test]
fn provider_filter() {
    assert_eq!(
        scan("debian-12", &["--provider", "apt"]).status.code(),
        Some(11)
    );
}

#[test]
fn no_color_when_piped() {
    let out = scan("edge-cases", &[]);
    assert!(!stdout(&out).contains('\u{1b}'));
}
