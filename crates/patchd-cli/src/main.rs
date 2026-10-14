use clap::Parser;

/// Self-hosted patch management for Linux and Windows.
#[derive(Parser)]
#[command(name = "patchd", version = patchd_core::build::LONG_VERSION)]
struct Cli {}

fn main() {
    let _cli = Cli::parse();
}
