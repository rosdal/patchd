//! `patchd config check` and `patchd config show`.

use std::path::Path;

use clap::Subcommand;
use patchd_core::config::{Config, DEFAULT_PATH};

const EXIT_OK: u8 = 0;
const EXIT_INVALID: u8 = 1;

#[derive(Subcommand)]
pub enum ConfigCommand {
    /// Parse and validate the config file; print the errors with line numbers.
    ///
    /// Exit codes: 0 valid, 1 invalid.
    Check,
    /// Print the effective config, after defaults and `PATCHD_*` overrides, as TOML.
    Show,
}

pub fn run(cmd: &ConfigCommand, path: Option<&Path>) -> u8 {
    let shown = path.map_or_else(|| DEFAULT_PATH.to_owned(), |p| p.display().to_string());
    match (cmd, Config::load(path, std::env::vars())) {
        (_, Err(e)) => {
            eprintln!("error: {e}");
            EXIT_INVALID
        }
        (ConfigCommand::Check, Ok(_)) => {
            println!("{shown}: ok");
            EXIT_OK
        }
        (ConfigCommand::Show, Ok(config)) => {
            println!("# effective config: {shown}, defaults and PATCHD_* overrides\n");
            print!("{}", config.to_toml());
            EXIT_OK
        }
    }
}
