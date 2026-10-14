use std::{env, process::Command};

fn main() {
    let sha = env::var("PATCHD_GIT_SHA")
        .ok()
        .filter(|s| !s.trim().is_empty())
        .or_else(|| {
            Command::new("git")
                .args(["rev-parse", "HEAD"])
                .output()
                .ok()
                .filter(|o| o.status.success())
                .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_owned())
        })
        .map_or_else(|| "unknown".to_owned(), |s| s.chars().take(10).collect());

    println!("cargo:rustc-env=PATCHD_GIT_SHA={sha}");
    println!(
        "cargo:rustc-env=PATCHD_TARGET={}",
        env::var("TARGET").unwrap()
    );
    println!("cargo:rerun-if-env-changed=PATCHD_GIT_SHA");
    println!("cargo:rerun-if-changed=../../.git/HEAD");
    println!("cargo:rerun-if-changed=../../.git/refs/heads");
}
