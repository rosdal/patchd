use clap::Parser;
use tracing_subscriber::EnvFilter;

/// The Patchd agent service.
#[derive(Parser)]
#[command(name = "patchd-agent", version = patchd_core::build::LONG_VERSION)]
struct Cli {}

fn main() {
    let _cli = Cli::parse();

    tracing_subscriber::fmt()
        .with_env_filter(
            EnvFilter::try_from_default_env().unwrap_or_else(|_| EnvFilter::new("info")),
        )
        .with_writer(std::io::stderr)
        .init();

    tracing::info!("agent not implemented yet");
}
