use std::sync::Arc;
use std::sync::atomic::AtomicBool;

use super::*;
use crate::model::{ProviderId, UpdateKind};
use crate::plan::SkipReason;
use crate::preflight::CheckStatus;
use crate::provider::Provider;
use crate::root::Root;
use crate::runlog::MemorySink;
use crate::runner::{CmdOutput, FixtureRunner};

use super::super::AptProvider;

fn update(name: &str, from: Option<&str>, to: &str, kind: UpdateKind) -> Update {
    Update {
        provider: ProviderId::APT,
        name: name.to_owned(),
        arch: Some("amd64".to_owned()),
        from_version: from.map(str::to_owned),
        to_version: to.to_owned(),
        kind,
        origins: Vec::new(),
        held: false,
    }
}

fn plan(updates: Vec<Update>) -> ProviderPlan {
    ProviderPlan {
        updates,
        ..ProviderPlan::empty(ProviderId::APT)
    }
}

fn sim(lines: &str) -> Simulation {
    parse_simulation(lines).unwrap()
}

const SSL: &str =
    "Inst libssl3 [3.0.15-1~deb12u1] (3.0.16-1~deb12u1 Debian-Security:12/stable-security [amd64])";
const OPENSSL: &str =
    "Inst openssl [3.0.15-1~deb12u1] (3.0.16-1~deb12u1 Debian-Security:12/stable-security [amd64])";
const KERNEL: &str =
    "Inst linux-image-6.1.0-31-amd64 (6.1.128-1 Debian-Security:12/stable-security [amd64])";

fn security_plan() -> ProviderPlan {
    plan(vec![
        update(
            "libssl3",
            Some("3.0.15-1~deb12u1"),
            "3.0.16-1~deb12u1",
            UpdateKind::Security,
        ),
        update(
            "openssl",
            Some("3.0.15-1~deb12u1"),
            "3.0.16-1~deb12u1",
            UpdateKind::Security,
        ),
    ])
}

#[test]
fn install_args_by_mode() {
    let p = security_plan();
    assert_eq!(
        install_args(Mode::Pinned, &p.updates),
        [
            "install",
            "--only-upgrade",
            "libssl3=3.0.16-1~deb12u1",
            "openssl=3.0.16-1~deb12u1"
        ]
    );
    assert_eq!(
        install_args(Mode::DistUpgrade, &p.updates),
        ["dist-upgrade"]
    );

    // New packages are never named.
    let mut with_new = p.clone();
    with_new.updates.push(update(
        "linux-image-6.1.0-31-amd64",
        None,
        "6.1.128-1",
        UpdateKind::Security,
    ));
    assert_eq!(install_args(Mode::Pinned, &with_new.updates).len(), 4);
}

#[test]
fn mode_follows_filter_and_exclusions() {
    let mut p = security_plan();
    assert_eq!(Mode::for_plan(Filter::Security, &p), Mode::Pinned);
    assert_eq!(Mode::for_plan(Filter::All, &p), Mode::DistUpgrade);
    p.held_skipped.push(crate::plan::SkippedUpdate {
        update: update("docker-ce", Some("1"), "2", UpdateKind::Minor),
        reason: SkipReason::Excluded,
    });
    assert_eq!(Mode::for_plan(Filter::All, &p), Mode::Pinned);
}

#[test]
fn environment_and_options_silence_prompts() {
    let cmd = apt_get(["dist-upgrade"]);
    let line = cmd.command_line();
    assert!(line.starts_with("apt-get -y -q -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold -o DPkg::Lock::Timeout=600 "), "{line}");
    for key in [
        "DEBIAN_FRONTEND",
        "UCF_FORCE_CONFFOLD",
        "NEEDRESTART_SUSPEND",
        "APT_LISTCHANGES_FRONTEND",
    ] {
        assert!(cmd.env.iter().any(|(k, _)| *k == key), "{key}");
    }
}

#[test]
fn simulation_matching_the_plan() {
    let s = sim(&format!(
        "{SSL}\n{OPENSSL}\n{KERNEL}\nConf libssl3 (3.0.16-1~deb12u1 Debian-Security:12/stable-security [amd64])\n"
    ));
    let c = check_simulation(&s, &security_plan().updates, &Exclusions::none());
    assert!(c.mismatches.is_empty(), "{:?}", c.mismatches);
    assert_eq!(c.new_deps.len(), 1);
    assert_eq!(c.new_deps[0].name, "linux-image-6.1.0-31-amd64");
}

#[test]
fn simulation_with_extra_upgrade_mismatch_and_missing() {
    let extra = "Inst libc6 [2.36-9+deb12u8] (2.36-9+deb12u9 Debian:12.9/stable [amd64])";
    let s = sim(&format!("{SSL}\n{extra}\n"));
    let c = check_simulation(&s, &security_plan().updates, &Exclusions::none());
    let all = c.mismatches.join("\n");
    assert!(all.contains("libc6 would be upgraded"), "{all}");
    assert!(
        all.contains("openssl 3.0.16-1~deb12u1 is planned, but"),
        "{all}"
    );

    let other_version = "Inst libssl3 [3.0.15-1~deb12u1] (3.0.17-1 Debian:12/stable [amd64])";
    let c = check_simulation(
        &sim(&format!("{other_version}\n{OPENSSL}\n")),
        &security_plan().updates,
        &Exclusions::none(),
    );
    assert!(
        c.mismatches[0].contains("instead of the planned"),
        "{:?}",
        c.mismatches
    );
}

#[test]
fn excluded_new_dependency_is_a_mismatch() {
    let ex = Exclusions::new(&["linux-image-*".to_owned()]).unwrap();
    let c = check_simulation(
        &sim(&format!("{SSL}\n{OPENSSL}\n{KERNEL}\n")),
        &security_plan().updates,
        &ex,
    );
    assert!(c.new_deps.is_empty());
    assert!(
        c.mismatches[0].contains("is excluded"),
        "{:?}",
        c.mismatches
    );
}

#[test]
fn verify_matches_the_right_architecture() {
    let native = update("libc6", Some("1"), "2", UpdateKind::Minor);
    let foreign = Update {
        name: "libc6:i386".to_owned(),
        arch: Some("i386".to_owned()),
        ..native.clone()
    };
    assert!(installed_as(&native, "libc6"));
    assert!(installed_as(&native, "libc6:amd64"));
    assert!(
        !installed_as(&native, "libc6:i386"),
        "a foreign copy is not the native one"
    );
    assert!(!installed_as(&native, "libc6-dev"));
    assert!(installed_as(&foreign, "libc6:i386"));
    assert!(!installed_as(&foreign, "libc6"));
}

/// A runner that records every apply command for a pinned two-package plan.
fn runner_for(p: &ProviderPlan, overrides: &[(&Cmd, CmdOutput)]) -> FixtureRunner {
    let args = install_args(Mode::Pinned, &p.updates);
    let ok = |s: &str| CmdOutput::ok(s);
    let mut r = FixtureRunner::new();
    for (cmd, out) in overrides {
        r = r.with(cmd.command_line(), out.clone());
    }
    let defaults = [
        (audit_cmd(), ok("")),
        (
            simulate_install_cmd(Mode::Pinned, &p.updates),
            ok(&format!("{SSL}\n{OPENSSL}\n")),
        ),
        (Cmd::new("apt-mark", ["showauto"]), ok("libssl3\nlibfoo\n")),
        (
            apt_get(std::iter::once("--download-only".to_owned()).chain(args.clone())),
            ok(""),
        ),
        (apt_get(args.clone()), ok("Setting up libssl3 ...\n")),
        (
            Cmd::new("apt-mark", ["auto", "libssl3"]),
            ok("libssl3 set to automatically installed.\n"),
        ),
        (
            verify_cmd(&p.updates),
            ok("libssl3:amd64\t3.0.16-1~deb12u1\tii \nopenssl\t3.0.16-1~deb12u1\tii \n"),
        ),
        (apt_get(["autoclean"]), ok("")),
    ];
    for (cmd, out) in defaults {
        if !overrides
            .iter()
            .any(|(c, _)| c.command_line() == cmd.command_line())
        {
            r = r.with(cmd.command_line(), out);
        }
    }
    r
}

struct Harness {
    sink: MemorySink,
    interrupted: AtomicBool,
    exclude: Exclusions,
    root: Root,
}

impl Harness {
    fn new() -> Self {
        Self {
            sink: MemorySink::default(),
            interrupted: AtomicBool::new(false),
            exclude: Exclusions::none(),
            root: Root::system(),
        }
    }

    fn ctx(&self, filter: Filter) -> ApplyCtx<'_> {
        ApplyCtx {
            filter,
            allow_removals: false,
            repair: false,
            exclude: &self.exclude,
            root: &self.root,
            events: &self.sink,
            interrupted: &self.interrupted,
            inhibit: false,
        }
    }
}

#[tokio::test]
async fn security_apply_restores_automatic_marks() {
    let p = security_plan();
    let runner = runner_for(&p, &[]);
    let h = Harness::new();
    let report = apply(&runner, &p, &h.ctx(Filter::Security)).await;
    assert_eq!(report.result, RunResult::Success, "{:?}", report.message);
    assert_eq!(report.installed.len(), 2);
    assert!(report.failed.is_empty());

    let seen = runner.seen();
    // libssl3 was automatic before and is again afterwards; openssl was manual and is
    // left alone.
    assert!(
        seen.contains(&"apt-mark auto libssl3".to_owned()),
        "{seen:#?}"
    );
    // Order: remember marks, download, install, restore marks, verify.
    let pos = |prefix: &str| {
        seen.iter()
            .position(|s| s.starts_with(prefix))
            .unwrap_or_else(|| panic!("{prefix}: {seen:#?}"))
    };
    assert!(
        pos("apt-mark showauto")
            < pos(
                "apt-get -y -q -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold -o DPkg::Lock::Timeout=600 --download-only"
            )
    );
    assert!(
        pos("apt-mark auto")
            > pos(
                "apt-get -y -q -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold -o DPkg::Lock::Timeout=600 install"
            )
    );
    assert!(pos("dpkg-query") > pos("apt-mark auto"));

    let verify = h.sink.events().into_iter().find_map(|e| match e {
        Event::Verify { ok, .. } => Some(ok),
        _ => None,
    });
    assert_eq!(verify, Some(true));
}

#[tokio::test]
async fn dist_upgrade_does_not_touch_marks() {
    let p = security_plan();
    let args = install_args(Mode::DistUpgrade, &p.updates);
    let runner = FixtureRunner::new()
        .with(
            apt_get(std::iter::once("--download-only".to_owned()).chain(args.clone()))
                .command_line(),
            CmdOutput::ok(""),
        )
        .with(apt_get(args).command_line(), CmdOutput::ok(""))
        .with(
            verify_cmd(&p.updates).command_line(),
            CmdOutput::ok("libssl3:amd64\t3.0.16-1~deb12u1\tii \nopenssl\t3.0.16-1~deb12u1\tii \n"),
        )
        .with(audit_cmd().command_line(), CmdOutput::ok(""))
        .with(apt_get(["autoclean"]).command_line(), CmdOutput::ok(""));
    let h = Harness::new();
    let report = apply(&runner, &p, &h.ctx(Filter::All)).await;
    assert_eq!(report.result, RunResult::Success);
    assert!(!runner.seen().iter().any(|s| s.starts_with("apt-mark")));
}

#[tokio::test]
async fn download_failure_installs_nothing() {
    let p = security_plan();
    let args = install_args(Mode::Pinned, &p.updates);
    let download = apt_get(std::iter::once("--download-only".to_owned()).chain(args.clone()));
    let failed = CmdOutput {
        status: 100,
        stdout: String::new(),
        stderr: "E: Failed to fetch file:/srv/repo/security/pt-sec_2_all.deb  File not found\n"
            .into(),
    };
    let runner = runner_for(&p, &[(&download, failed)]);
    let h = Harness::new();
    let report = apply(&runner, &p, &h.ctx(Filter::Security)).await;
    assert_eq!(report.result, RunResult::Failed);
    assert!(report.message.unwrap().contains("download failed"));
    let install = apt_get(args).command_line();
    assert!(!runner.seen().contains(&install));
}

#[tokio::test]
async fn failing_postinst_is_partial() {
    let p = security_plan();
    let install = apt_get(install_args(Mode::Pinned, &p.updates));
    let runner = runner_for(
        &p,
        &[
            (
                &install,
                CmdOutput {
                    status: 100,
                    stdout: String::new(),
                    stderr: "E: Sub-process /usr/bin/dpkg returned an error code (1)\n".into(),
                },
            ),
            (
                &verify_cmd(&p.updates),
                CmdOutput::ok(
                    "libssl3:amd64\t3.0.16-1~deb12u1\tii \nopenssl\t3.0.16-1~deb12u1\tiF \n",
                ),
            ),
            // The audit after the install finds the half-configured package.
            (
                &audit_cmd(),
                CmdOutput::ok(" openssl    Secure Sockets Layer toolkit\n"),
            ),
        ],
    );
    let h = Harness::new();
    let report = apply(&runner, &p, &h.ctx(Filter::Security)).await;
    assert_eq!(report.result, RunResult::Partial);
    assert_eq!(report.failed.len(), 1);
    assert_eq!(report.failed[0].name, "openssl");
    let msg = report.message.unwrap();
    assert!(
        msg.contains("install failed") && msg.contains("openssl") && msg.contains("dpkg --audit"),
        "{msg}"
    );
}

/// Sets the interrupted flag when a given command runs, as a SIGTERM arriving during it
/// would.
struct InterruptDuring<'a> {
    inner: FixtureRunner,
    flag: &'a AtomicBool,
    during: String,
}

#[async_trait::async_trait]
impl Runner for InterruptDuring<'_> {
    async fn run(&self, cmd: &Cmd) -> Result<CmdOutput, crate::runner::RunError> {
        if cmd.command_line() == self.during {
            self.flag.store(true, std::sync::atomic::Ordering::SeqCst);
        }
        self.inner.run(cmd).await
    }
}

#[tokio::test]
async fn sigterm_during_install_lets_it_finish() {
    let p = security_plan();
    let h = Harness::new();
    let install = apt_get(install_args(Mode::Pinned, &p.updates)).protected();
    let runner = InterruptDuring {
        inner: runner_for(&p, &[]),
        flag: &h.interrupted,
        during: install.command_line(),
    };
    let report = apply(&runner, &p, &h.ctx(Filter::Security)).await;
    assert_eq!(report.result, RunResult::Interrupted);
    // The install completed and was verified, marks were restored, cleanup was skipped.
    assert_eq!(report.installed.len(), 2);
    let seen = runner.inner.seen();
    assert!(seen.contains(&"apt-mark auto libssl3".to_owned()));
    assert!(!seen.iter().any(|s| s.ends_with(" autoclean")), "{seen:#?}");
}

#[tokio::test]
async fn sigterm_before_install_installs_nothing() {
    let p = security_plan();
    let runner = runner_for(&p, &[]);
    let h = Harness::new();
    h.interrupted
        .store(true, std::sync::atomic::Ordering::SeqCst);
    let report = apply(&runner, &p, &h.ctx(Filter::Security)).await;
    assert_eq!(report.result, RunResult::Interrupted);
    let install = apt_get(install_args(Mode::Pinned, &p.updates)).command_line();
    assert!(!runner.seen().contains(&install));
}

#[tokio::test]
async fn preflight_checks() {
    let p = security_plan();
    let removal = "Remv pve-firmware [3.14-1]";
    let sim_cmd = simulate_install_cmd(Mode::Pinned, &p.updates);
    let runner = runner_for(
        &p,
        &[(
            &sim_cmd,
            CmdOutput::ok(format!("{SSL}\n{OPENSSL}\n{removal}\n")),
        )],
    );
    let h = Harness::new();
    let checks = preflight(&runner, &p, &h.ctx(Filter::Security)).await;
    let status = |name: &str| checks.iter().find(|c| c.name == name).unwrap().status;
    assert_eq!(status("dpkg-audit"), CheckStatus::Pass);
    assert_eq!(status("simulate"), CheckStatus::Pass);
    assert_eq!(status("removals"), CheckStatus::Fail);
}

#[tokio::test]
async fn broken_dpkg_aborts_unless_repair() {
    let p = security_plan();
    let broken =
        CmdOutput::ok("The following packages are only half configured:\n pt-sec    dummy\n");
    let runner = runner_for(&p, &[(&audit_cmd(), broken.clone())]);
    let h = Harness::new();
    let checks = preflight(&runner, &p, &h.ctx(Filter::Security)).await;
    assert!(checks[0].failed());
    assert!(
        checks[0].detail.contains("repair = true"),
        "{}",
        checks[0].detail
    );
    assert!(!runner.seen().iter().any(|s| s.contains("--configure")));

    let repair_cmd = "dpkg --configure -a --force-confdef --force-confold";
    let runner = FixtureRunner::new()
        .with(audit_cmd().command_line(), broken)
        .with(audit_cmd().command_line(), CmdOutput::ok(""))
        .with(repair_cmd, CmdOutput::ok(""))
        .with(
            simulate_install_cmd(Mode::Pinned, &p.updates).command_line(),
            CmdOutput::ok(format!("{SSL}\n{OPENSSL}\n")),
        );
    let mut ctx = h.ctx(Filter::Security);
    ctx.repair = true;
    let checks = preflight(&runner, &p, &ctx).await;
    assert_eq!(checks[0].status, CheckStatus::Warn, "{checks:?}");
    assert!(runner.seen().contains(&repair_cmd.to_owned()));
}

/// `plan` under the security filter simulates the pinned install to find the new
/// packages the security updates pull in.
#[tokio::test]
async fn security_plan_adds_new_dependencies_from_a_simulation() {
    let available = format!(
        "{SSL}\n{OPENSSL}\nInst base-files [12.4+deb12u8] (12.4+deb12u9 Debian:12.9/stable [amd64])\n{KERNEL}\n"
    );
    let planned = security_plan().updates;
    let runner = Arc::new(
        FixtureRunner::new()
            .with(
                super::super::simulate_cmd().command_line(),
                CmdOutput::ok(&available),
            )
            .with(
                super::super::showhold_cmd().command_line(),
                CmdOutput::ok(""),
            )
            .with(
                simulate_install_cmd(Mode::Pinned, &planned).command_line(),
                CmdOutput::ok(format!("{SSL}\n{OPENSSL}\n{KERNEL}\n")),
            ),
    );
    let provider = AptProvider::with_runner(runner);
    let plan = provider
        .plan(Filter::Security, &Exclusions::none())
        .await
        .unwrap();
    let names: Vec<&str> = plan.updates.iter().map(|u| u.name.as_str()).collect();
    assert_eq!(names, ["libssl3", "openssl", "linux-image-6.1.0-31-amd64"]);
    assert!(plan.warnings.is_empty(), "{:?}", plan.warnings);

    let all = provider
        .plan(Filter::All, &Exclusions::none())
        .await
        .unwrap();
    assert_eq!(all.updates.len(), 4);
}

/// A half-configured package from last night must fail the run even when there is
/// nothing new to install.
#[tokio::test]
async fn empty_plan_still_audits_dpkg() {
    let p = plan(Vec::new());
    let runner = FixtureRunner::new().with(
        audit_cmd().command_line(),
        CmdOutput::ok("The following packages are only half configured:\n pt-fail    dummy\n"),
    );
    let h = Harness::new();
    let checks = preflight(&runner, &p, &h.ctx(Filter::All)).await;
    assert_eq!(checks.len(), 1);
    assert!(checks[0].failed());
    assert_eq!(runner.seen(), ["dpkg --audit"]);
}

/// A failed install is what needs attention, even when a SIGTERM also arrived.
#[tokio::test]
async fn failed_install_outranks_the_interruption() {
    let p = security_plan();
    let h = Harness::new();
    let install = apt_get(install_args(Mode::Pinned, &p.updates)).protected();
    let failed = CmdOutput {
        status: 100,
        stdout: String::new(),
        stderr: "E: Sub-process /usr/bin/dpkg returned an error code (1)\n".into(),
    };
    let runner = InterruptDuring {
        inner: runner_for(&p, &[(&install, failed)]),
        flag: &h.interrupted,
        during: install.command_line(),
    };
    let report = apply(&runner, &p, &h.ctx(Filter::Security)).await;
    assert_eq!(report.result, RunResult::Partial);
    let msg = report.message.unwrap();
    assert!(
        msg.contains("install failed") && msg.ends_with("then interrupted"),
        "{msg}"
    );
}
