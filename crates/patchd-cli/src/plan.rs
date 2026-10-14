//! `patchd plan`: exactly what `apply` would install. Read-only, so it is also the dry
//! run.

use std::path::Path;

use clap::Args;
use patchd_core::host::HostInfo;
use patchd_core::plan::{Filter, Plan};
use patchd_core::provider::Provider;
use patchd_core::providers::apt::AptProvider;

use crate::render;

/// Nothing to install.
const EXIT_EMPTY: u8 = 0;
const EXIT_ERROR: u8 = 1;
/// The plan installs something.
const EXIT_NON_EMPTY: u8 = 10;

#[derive(Args)]
pub struct PlanArgs {
    /// Only security updates, plus the new packages they pull in.
    #[arg(long, conflicts_with = "all")]
    pub security: bool,

    /// Every pending update.
    #[arg(long)]
    pub all: bool,

    /// Print the plan as JSON.
    #[arg(long)]
    json: bool,

    /// Replay recorded command output from this directory instead of running commands.
    #[cfg(debug_assertions)]
    #[arg(long, hide = true)]
    fixtures: Option<std::path::PathBuf>,
}

/// `--security` or `--all`, else the config's filter.
pub fn filter_override(security: bool, all: bool) -> Option<Filter> {
    match (security, all) {
        (true, _) => Some(Filter::Security),
        (_, true) => Some(Filter::All),
        _ => None,
    }
}

pub async fn run(args: &PlanArgs, config_path: Option<&Path>) -> u8 {
    let config = match crate::load_config(config_path) {
        Ok(c) => c,
        Err(e) => {
            eprintln!("error: {e}");
            return EXIT_ERROR;
        }
    };
    let filter = filter_override(args.security, args.all).unwrap_or(config.apply.filter);
    let exclude = config.exclusions();

    let (host, apt) = match setup(args) {
        Ok(x) => x,
        Err(e) => {
            eprintln!("error: {e}");
            return EXIT_ERROR;
        }
    };

    let mut providers = Vec::new();
    if apt.detect(&host).await {
        match apt.plan(filter, &exclude).await {
            Ok(p) => providers.push(p),
            Err(e) => {
                eprintln!("error: apt: {e}");
                return EXIT_ERROR;
            }
        }
    } else {
        eprintln!("error: no supported package manager found on this host");
        return EXIT_ERROR;
    }
    let plan = Plan::new(filter, providers);

    if args.json {
        println!(
            "{}",
            serde_json::to_string_pretty(&plan).expect("serialize plan")
        );
    } else {
        print!("{}", render::plan_table(&plan, crate::use_color()));
        for p in &plan.providers {
            for w in &p.warnings {
                eprintln!("warning: {}: {w}", p.id);
            }
        }
    }
    if plan.is_empty() {
        EXIT_EMPTY
    } else {
        EXIT_NON_EMPTY
    }
}

fn setup(args: &PlanArgs) -> Result<(HostInfo, AptProvider), String> {
    #[cfg(debug_assertions)]
    if let Some(dir) = &args.fixtures {
        return crate::fixture_apt(dir);
    }
    let _ = args;
    let (host, apt, _) = crate::system_apt();
    Ok((host, apt))
}
