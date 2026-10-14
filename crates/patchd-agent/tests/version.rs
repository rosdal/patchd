use std::process::Command;

#[test]
fn prints_version() {
    let out = Command::new(env!("CARGO_BIN_EXE_patchd-agent"))
        .arg("--version")
        .output()
        .unwrap();
    assert!(out.status.success());
    let stdout = String::from_utf8(out.stdout).unwrap();
    assert!(stdout.starts_with(concat!("patchd-agent ", env!("CARGO_PKG_VERSION"))));
}

#[test]
fn exits_cleanly_without_arguments() {
    let out = Command::new(env!("CARGO_BIN_EXE_patchd-agent"))
        .output()
        .unwrap();
    assert!(out.status.success());
    let stderr = String::from_utf8(out.stderr).unwrap();
    assert!(stderr.contains("agent not implemented yet"));
}
