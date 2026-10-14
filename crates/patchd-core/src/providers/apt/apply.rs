//! apt apply in three passes: simulate the exact command, download everything, and only
//! then install (ADR 0009). Network failures and plan mismatches stop the run while
//! dpkg is still untouched.

use std::collections::HashSet;
use std::time::Duration;

use crate::apply::{ApplyCtx, ApplyReport, Change, RunResult, inhibited, step};
use crate::model::Update;
use crate::plan::{Exclusions, Filter, ProviderPlan};
use crate::preflight::Check;
use crate::runlog::Event;
use crate::runner::{Cmd, Runner};
use crate::version::DebVersion;

use super::available::{Simulation, parse_simulation};
use super::inventory;

/// Each variable silences one prompt that would otherwise hang a nightly run.
const ENV: [(&str, &str); 4] = [
    // debconf questions
    ("DEBIAN_FRONTEND", "noninteractive"),
    // ucf-managed config files (grub, some ssh configs), which bypass dpkg's own flags
    ("UCF_FORCE_CONFFOLD", "1"),
    // Ubuntu's needrestart dialog; Patchd reports restarts itself
    ("NEEDRESTART_SUSPEND", "1"),
    // apt-listchanges pager and mail
    ("APT_LISTCHANGES_FRONTEND", "none"),
];

/// `-y -q`, keep local config files, and wait up to 10 minutes for the dpkg lock instead
/// of failing when `apt-daily` holds it (apt ≥ 1.9.11).
const OPTIONS: [&str; 8] = [
    "-y",
    "-q",
    "-o",
    "Dpkg::Options::=--force-confdef",
    "-o",
    "Dpkg::Options::=--force-confold",
    "-o",
    "DPkg::Lock::Timeout=600",
];

const DOWNLOAD_TIMEOUT: Duration = Duration::from_secs(30 * 60);
const INSTALL_TIMEOUT: Duration = Duration::from_secs(60 * 60);
const REPAIR_TIMEOUT: Duration = Duration::from_secs(30 * 60);

/// How the install is spelled.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) enum Mode {
    /// `apt-get dist-upgrade`: the `all` filter with nothing excluded. The simulation
    /// proves it equals the plan.
    DistUpgrade,
    /// `apt-get install --only-upgrade pkg=ver …`: the security filter, or anything
    /// excluded, which `dist-upgrade` cannot honour.
    Pinned,
}

impl Mode {
    pub(super) fn for_plan(filter: Filter, plan: &ProviderPlan) -> Self {
        if filter == Filter::All && !plan.has_exclusions() {
            Self::DistUpgrade
        } else {
            Self::Pinned
        }
    }
}

/// An `apt-get` command with the non-interactive environment and options.
pub(super) fn apt_get<I, S>(args: I) -> Cmd
where
    I: IntoIterator<Item = S>,
    S: Into<String>,
{
    let mut cmd = Cmd::new(
        "apt-get",
        OPTIONS
            .iter()
            .copied()
            .map(str::to_owned)
            .chain(args.into_iter().map(Into::into)),
    );
    for (k, v) in ENV {
        cmd = cmd.env(k, v);
    }
    cmd
}

/// The install arguments for a plan: `dist-upgrade`, or `install --only-upgrade` with
/// every upgraded package pinned. New packages are never named; they come in as
/// dependencies, which the simulation checks.
pub(super) fn install_args(mode: Mode, updates: &[Update]) -> Vec<String> {
    match mode {
        Mode::DistUpgrade => vec!["dist-upgrade".to_owned()],
        Mode::Pinned => ["install", "--only-upgrade"]
            .into_iter()
            .map(str::to_owned)
            .chain(
                updates
                    .iter()
                    .filter(|u| u.from_version.is_some())
                    .map(|u| format!("{}={}", u.name, u.to_version)),
            )
            .collect(),
    }
}

pub(super) fn simulate_install_cmd(mode: Mode, updates: &[Update]) -> Cmd {
    apt_get(std::iter::once("-s".to_owned()).chain(install_args(mode, updates)))
        .timeout(crate::runner::RESOLVE_TIMEOUT)
}

pub(super) fn audit_cmd() -> Cmd {
    Cmd::new("dpkg", ["--audit"]).ok_codes(&[0, 1])
}

pub(super) fn verify_cmd(updates: &[Update]) -> Cmd {
    // dpkg-query exits 1 when a name is unknown; the comparison reports it.
    Cmd::new(
        "dpkg-query",
        ["-W".to_owned(), VERIFY_FORMAT.to_owned()]
            .into_iter()
            .chain(updates.iter().map(|u| u.name.clone())),
    )
    .ok_codes(&[0, 1])
}

const VERIFY_FORMAT: &str = r"-f=${binary:Package}\t${Version}\t${db:Status-Abbrev}\n";

/// What the simulation of a plan's install says about it.
#[derive(Debug, Default, PartialEq, Eq)]
pub(super) struct SimCheck {
    /// New packages the install pulls in as dependencies.
    pub new_deps: Vec<Update>,
    /// Reasons the simulation does not match the plan.
    pub mismatches: Vec<String>,
    pub removals: Vec<String>,
}

/// Compares a simulation with the plan: every `Inst` must be a planned package at its
/// planned version, or a new dependency that is not excluded.
pub(super) fn check_simulation(
    sim: &Simulation,
    planned: &[Update],
    exclude: &Exclusions,
) -> SimCheck {
    let mut check = SimCheck {
        removals: sim.removals.clone(),
        ..SimCheck::default()
    };
    for inst in &sim.updates {
        let plan_entry = planned.iter().find(|u| same_package(&u.name, &inst.name));
        match plan_entry {
            Some(p) if versions_equal(&p.to_version, &inst.to_version) => {}
            Some(p) => check.mismatches.push(format!(
                "{} would install {} instead of the planned {}",
                inst.name, inst.to_version, p.to_version
            )),
            None if exclude.matches(&inst.name) => check.mismatches.push(format!(
                "{} is excluded, but the install would change it",
                inst.name
            )),
            None if inst.from_version.is_none() => check.new_deps.push(inst.clone()),
            None => check.mismatches.push(format!(
                "{} would be upgraded {} → {}, which is not in the plan",
                inst.name,
                inst.from_version.as_deref().unwrap_or("?"),
                inst.to_version
            )),
        }
    }
    for p in planned {
        if !sim.updates.iter().any(|i| same_package(&p.name, &i.name)) {
            check.mismatches.push(format!(
                "{} {} is planned, but the install would not change it",
                p.name, p.to_version
            ));
        }
    }
    check
}

/// `libc6` and `libc6:amd64` are the same package on an amd64 host; apt drops the native
/// suffix in some places and dpkg-query adds it in others.
fn same_package(a: &str, b: &str) -> bool {
    a == b
        || match (a.split_once(':'), b.split_once(':')) {
            (Some((a, _)), None) => a == b,
            (None, Some((b, _))) => a == b,
            _ => false,
        }
}

fn versions_equal(a: &str, b: &str) -> bool {
    match (DebVersion::parse(a), DebVersion::parse(b)) {
        (Ok(a), Ok(b)) => a == b,
        _ => a == b,
    }
}

/// `dpkg --audit` prints nothing on a healthy system.
fn audit_problems(out: &str) -> Vec<String> {
    out.lines()
        .map(str::trim_end)
        .filter(|l| !l.trim().is_empty())
        .map(str::to_owned)
        .collect()
}

pub(super) async fn preflight(
    runner: &dyn Runner,
    plan: &ProviderPlan,
    ctx: &ApplyCtx<'_>,
) -> Vec<Check> {
    let mut checks = vec![dpkg_health(runner, ctx).await];

    // Pass 1: simulate the exact command.
    let mode = Mode::for_plan(ctx.filter, plan);
    let cmd = simulate_install_cmd(mode, &plan.updates);
    let sim = match step(ctx.events, runner, "simulate", &cmd).await {
        Err(e) => Err(format!("the simulation failed: {e}")),
        Ok(out) => {
            parse_simulation(&out.stdout).map_err(|e| format!("cannot read the simulation: {e}"))
        }
    };
    let sim = match sim {
        Ok(sim) => sim,
        Err(e) => {
            checks.push(Check::fail("simulate", e));
            return checks;
        }
    };
    let sc = check_simulation(&sim, &plan.updates, ctx.exclude);
    if sc.mismatches.is_empty() {
        let extra = if sc.new_deps.is_empty() {
            String::new()
        } else {
            let names: Vec<String> = sc.new_deps.iter().map(|u| u.name.clone()).collect();
            format!(", plus new dependencies {}", inventory::name_list(&names))
        };
        checks.push(Check::pass(
            "simulate",
            format!(
                "the install matches the plan ({} packages{extra})",
                plan.updates.len()
            ),
        ));
    } else {
        checks.push(Check::fail("simulate", sc.mismatches.join("; ")));
    }
    let removals = inventory::name_list(&sc.removals);
    checks.push(match (sc.removals.is_empty(), ctx.allow_removals) {
        (true, _) => Check::pass("removals", "nothing would be removed"),
        (false, true) => Check::warn(
            "removals",
            format!("would remove {removals} (allow_removals = true)"),
        ),
        (false, false) => Check::fail(
            "removals",
            format!("would remove {removals}; set allow_removals = true to allow it"),
        ),
    });
    checks
}

/// `dpkg --audit` must be clean. With `repair = true`, `dpkg --configure -a` gets one
/// chance to make it so.
async fn dpkg_health(runner: &dyn Runner, ctx: &ApplyCtx<'_>) -> Check {
    let events = ctx.events;
    match step(events, runner, "dpkg-audit", &audit_cmd()).await {
        Ok(out) if audit_problems(&out.stdout).is_empty() => {
            Check::pass("dpkg-audit", "dpkg --audit is clean")
        }
        Ok(out) if ctx.repair => {
            let first = audit_problems(&out.stdout);
            let repair = Cmd::new(
                "dpkg",
                ["--configure", "-a", "--force-confdef", "--force-confold"],
            )
            .env("DEBIAN_FRONTEND", "noninteractive")
            .env("UCF_FORCE_CONFFOLD", "1")
            .env("NEEDRESTART_SUSPEND", "1")
            .own_process_group()
            .timeout(REPAIR_TIMEOUT);
            let repaired = step(events, runner, "dpkg-repair", &repair).await.is_ok();
            let after = step(events, runner, "dpkg-audit", &audit_cmd()).await;
            match after {
                Ok(out) if repaired && audit_problems(&out.stdout).is_empty() => Check::warn(
                    "dpkg-audit",
                    format!("repaired with dpkg --configure -a: {}", summarize(&first)),
                ),
                _ => Check::fail(
                    "dpkg-audit",
                    format!("dpkg --configure -a did not fix: {}", summarize(&first)),
                ),
            }
        }
        Ok(out) => Check::fail(
            "dpkg-audit",
            format!(
                "dpkg --audit reports problems: {}; run `dpkg --configure -a`, or set repair = true",
                summarize(&audit_problems(&out.stdout))
            ),
        ),
        Err(e) => Check::fail("dpkg-audit", format!("dpkg --audit failed: {e}")),
    }
}

fn summarize(lines: &[String]) -> String {
    const MAX: usize = 5;
    let mut s: Vec<&str> = lines.iter().take(MAX).map(|l| l.trim()).collect();
    if lines.len() > MAX {
        s.push("…");
    }
    s.join(" / ")
}

#[allow(clippy::too_many_lines)] // the passes in order, each with its own failure handling
pub(super) async fn apply(
    runner: &dyn Runner,
    plan: &ProviderPlan,
    ctx: &ApplyCtx<'_>,
) -> ApplyReport {
    let events = ctx.events;
    let mode = Mode::for_plan(ctx.filter, plan);
    let args = install_args(mode, &plan.updates);
    let mut report = ApplyReport::new(RunResult::Success);
    report.skipped.clone_from(&plan.held_skipped);

    // Naming a package in `apt-get install` marks it as manually installed, so
    // `autoremove` would never clean it up. Remember which ones were automatic.
    let auto_before: Option<HashSet<String>> = if mode == Mode::Pinned {
        match step(
            events,
            runner,
            "showauto",
            &Cmd::new("apt-mark", ["showauto"]),
        )
        .await
        {
            Ok(out) => Some(out.stdout.split_whitespace().map(str::to_owned).collect()),
            Err(e) => {
                report.message = Some(format!("apt-mark showauto failed: {e}"));
                report.result = RunResult::Failed;
                return report;
            }
        }
    } else {
        None
    };

    // Pass 2: download everything first.
    let download = apt_get(std::iter::once("--download-only".to_owned()).chain(args.clone()))
        .timeout(DOWNLOAD_TIMEOUT);
    if let Err(e) = step(events, runner, "download", &download).await {
        report.result = RunResult::Failed;
        report.message = Some(format!("download failed, nothing installed: {e}"));
        return report;
    }
    if ctx.is_interrupted() {
        report.result = RunResult::Interrupted;
        report.message = Some("interrupted after the download; nothing installed".to_owned());
        return report;
    }

    // Pass 3: the real thing. In its own process group, so a signal to patchd never
    // reaches dpkg; SIGTERM only sets the interrupted flag.
    let mut install = apt_get(args).timeout(INSTALL_TIMEOUT).own_process_group();
    if ctx.inhibit {
        install = inhibited(install);
    }
    let install_error = step(events, runner, "install", &install).await.err();

    if let Some(auto_before) = auto_before {
        let restore: Vec<String> = plan
            .updates
            .iter()
            .filter(|u| u.from_version.is_some())
            .map(|u| u.name.clone())
            .filter(|n| auto_before.contains(n))
            .collect();
        if !restore.is_empty() {
            let cmd = Cmd::new(
                "apt-mark",
                std::iter::once("auto".to_owned()).chain(restore),
            );
            if let Err(e) = step(events, runner, "apt-mark-auto", &cmd).await {
                report
                    .warnings
                    .push(format!("restoring automatic-install marks failed: {e}"));
            }
        }
    }

    // Verify: every planned package at its planned version, and dpkg healthy.
    let mut missed = Vec::new();
    match step(events, runner, "verify", &verify_cmd(&plan.updates)).await {
        Ok(out) => {
            let installed = parse_verify(&out.stdout);
            for u in &plan.updates {
                let change = Change {
                    name: u.name.clone(),
                    from: u.from_version.clone(),
                    to: u.to_version.clone(),
                };
                let ok = installed.iter().any(|(n, v, st)| {
                    same_package(&u.name, n)
                        && st.starts_with("ii")
                        && versions_equal(v, &u.to_version)
                });
                if ok {
                    report.installed.push(change);
                } else {
                    missed.push(change);
                }
            }
        }
        Err(e) => {
            report
                .warnings
                .push(format!("verification query failed: {e}"));
            missed = plan
                .updates
                .iter()
                .map(|u| Change {
                    name: u.name.clone(),
                    from: u.from_version.clone(),
                    to: u.to_version.clone(),
                })
                .collect();
        }
    }
    let audit = match step(events, runner, "dpkg-audit", &audit_cmd()).await {
        Ok(out) => audit_problems(&out.stdout),
        Err(e) => vec![format!("dpkg --audit failed: {e}")],
    };
    let verified = missed.is_empty() && audit.is_empty();
    events.emit(Event::Verify {
        ok: verified,
        missed: missed.clone(),
        audit: audit.clone(),
    });
    report.failed = missed;

    if ctx.is_interrupted() {
        report.result = RunResult::Interrupted;
        report.message = Some("interrupted; the install was allowed to finish".to_owned());
        return report;
    }

    // Cleanup: old .debs only. No autoremove in v0.3.
    if let Err(e) = step(events, runner, "autoclean", &apt_get(["autoclean"])).await {
        report
            .warnings
            .push(format!("apt-get autoclean failed: {e}"));
    }

    if !verified || install_error.is_some() {
        report.result = RunResult::Partial;
        let mut why = Vec::new();
        if let Some(e) = install_error {
            why.push(format!("install failed: {e}"));
        }
        if !report.failed.is_empty() {
            why.push(format!(
                "not at the planned version: {}",
                report
                    .failed
                    .iter()
                    .map(|c| c.name.as_str())
                    .collect::<Vec<_>>()
                    .join(", ")
            ));
        }
        if !audit.is_empty() {
            why.push(format!("dpkg --audit: {}", summarize(&audit)));
        }
        report.message = Some(why.join("; "));
    }
    report
}

/// `name \t version \t status` rows.
fn parse_verify(out: &str) -> Vec<(String, String, String)> {
    out.lines()
        .filter_map(|l| {
            let mut f = l.split('\t');
            Some((
                f.next()?.to_owned(),
                f.next()?.to_owned(),
                f.next().unwrap_or("").to_owned(),
            ))
        })
        .collect()
}

#[cfg(test)]
mod tests;
