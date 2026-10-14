//! Repository tasks: `cargo xtask <task>`.

use std::path::{Path, PathBuf};
use std::process::ExitCode;

use patchd_core::model::ScanReport;

const USAGE: &str = "usage: cargo xtask schema";

fn main() -> ExitCode {
    if std::env::args().nth(1).as_deref() != Some("schema") {
        eprintln!("{USAGE}");
        return ExitCode::from(2);
    }
    match schema() {
        Ok(path) => {
            println!("wrote {}", path.display());
            ExitCode::SUCCESS
        }
        Err(e) => {
            eprintln!("error: {e}");
            ExitCode::FAILURE
        }
    }
}

/// Writes the JSON Schema of the scan report, generated from the Rust types (ADR 0007).
fn schema() -> std::io::Result<PathBuf> {
    let root = Path::new(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .expect("xtask lives in the workspace root");
    let path = root.join("protocol/schemas/scan-report.json");
    let schema = schemars::schema_for!(ScanReport);
    let json = serde_json::to_string_pretty(&schema).map_err(std::io::Error::other)?;
    std::fs::create_dir_all(path.parent().expect("schema path has a parent"))?;
    std::fs::write(&path, format!("{json}\n"))?;
    Ok(path)
}
