//! Fixture tests against output captured by `scripts/capture-dnf-fixtures.sh` from
//! `fedora:latest` (dnf5), `almalinux:10` and `almalinux:9` (dnf4).

use std::path::PathBuf;
use std::sync::atomic::AtomicBool;

use super::*;
use crate::model::UpdateKind;
use crate::preflight::CheckStatus;
use crate::runlog::MemorySink;
use crate::scan::provider_report;

const FIXTURES: [&str; 3] = ["fedora-44", "almalinux-10", "almalinux-9"];

fn dir(name: &str) -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("tests/fixtures/dnf")
        .join(name)
}

fn read(name: &str, file: &str) -> String {
    std::fs::read_to_string(dir(name).join(file)).unwrap()
}

#[tokio::test]
async fn generation_per_distro() {
    for (name, expected) in [
        ("fedora-44", Generation::Dnf5),
        ("almalinux-10", Generation::Dnf4),
        ("almalinux-9", Generation::Dnf4),
    ] {
        let (p, _) = DnfProvider::from_fixtures(&dir(name)).unwrap();
        assert_eq!(p.generation().await.unwrap(), expected, "{name}");
    }
}

#[tokio::test]
async fn reports_match_the_fixtures() {
    for name in FIXTURES {
        let (p, runner) = DnfProvider::from_fixtures(&dir(name)).unwrap();
        let report = provider_report(&p).await;
        assert_eq!(report.error, None, "{name}");
        assert!(report.packages.len() > 50, "{name}");
        assert!(
            report.packages.iter().all(|p| p.name != "gpg-pubkey"),
            "{name}"
        );
        let upgrades = read(name, "repoquery-upgrades.txt")
            .lines()
            .filter(|l| !l.is_empty())
            .count();
        let security = read(name, "repoquery-security.txt")
            .lines()
            .filter(|l| !l.is_empty())
            .count();
        assert_eq!(report.updates.len(), upgrades, "{name}");
        assert_eq!(
            report
                .updates
                .iter()
                .filter(|u| u.kind == UpdateKind::Security)
                .count(),
            security,
            "{name}"
        );
        // Every update knows what it replaces.
        assert!(
            report.updates.iter().all(|u| u.from_version.is_some()),
            "{name}"
        );
        // Read-only: rpm and repoquery, nothing else.
        assert!(
            runner
                .seen()
                .iter()
                .all(|c| c.starts_with("rpm -qa") || c.starts_with("dnf -q repoquery"))
        );
        insta::assert_json_snapshot!(format!("dnf-{name}"), report.updates);
    }
}

#[test]
fn transaction_dnf4() {
    let tx = parse::parse_transaction(&read("almalinux-10", "upgrade-assumeno.txt"));
    assert_eq!(tx.changes.len(), 16);
    assert!(tx.removals.is_empty());
    let ssl = tx
        .changes
        .iter()
        .find(|p| p.name == "openssl-libs")
        .unwrap();
    assert_eq!(ssl.evr, "1:3.5.8-2.el10_2.alma.1");
    let curl = tx.changes.iter().find(|p| p.name == "curl").unwrap();
    assert_eq!(
        curl.evr, "0:8.12.1-4.el10_2.7",
        "dnf4 leaves 0: off; it is added"
    );
    assert!(!curl.new);
}

#[test]
fn transaction_dnf5() {
    let tx = parse::parse_transaction(&read("fedora-44", "upgrade-assumeno.txt"));
    let upgrades = tx.changes.iter().filter(|p| !p.new).count();
    assert_eq!(upgrades, 32);
    // `replacing …` rows are the old versions, not changes.
    assert!(tx.changes.iter().all(|p| p.evr != "0:8.18.0-8.fc44"));
    // Weak dependencies are new packages.
    assert!(tx.changes.iter().any(|p| p.new));
}

#[test]
fn transaction_with_wrapped_names_and_removals() {
    let out = "\
Dependencies resolved.
================================================================================
 Package                         Arch    Version            Repository    Size
================================================================================
Installing:
 kernel                          x86_64  6.12.0-55.el10     baseos        1.1 M
Upgrading:
 python3-a-really-long-package-name-that-wraps
                                 noarch  1.2-3.el10         appstream      20 k
Removing:
 old-thing                       x86_64  1.0-1.el10         @baseos        10 k
Removing dependent packages:
 old-dependent                   noarch  1.0-1.el10         @baseos        10 k

Transaction Summary
================================================================================
Install  1 Package
";
    let tx = parse::parse_transaction(out);
    assert_eq!(tx.changes.len(), 2);
    assert!(tx.changes[0].new && tx.changes[0].name == "kernel");
    assert_eq!(
        tx.changes[1].name,
        "python3-a-really-long-package-name-that-wraps"
    );
    assert_eq!(tx.changes[1].arch, "noarch");
    assert_eq!(tx.removals, ["old-thing", "old-dependent"]);
}

#[test]
fn nevra_spelling() {
    assert_eq!(
        nevra("curl", "0:8.18.0-14.fc44", "x86_64"),
        "curl-0:8.18.0-14.fc44.x86_64"
    );
    assert_eq!(
        nevra("tzdata", "2026e-1.el10", "noarch"),
        "tzdata-0:2026e-1.el10.noarch"
    );
}

#[test]
fn qf_formats_use_real_tabs() {
    // dnf5 prints `\t` literally, so the escapes must never reach it.
    assert!(!parse::REPOQUERY_QF.contains("\\t"));
    assert!(parse::REPOQUERY_QF.contains('\t'));
    assert!(parse::RPM_QF.ends_with('\n'));
}

fn security_updates(name: &str) -> Vec<Update> {
    let installed = parse::parse_rpm_qa(&read(name, "rpm-qa.txt")).unwrap();
    let up = parse::parse_repoquery(&read(name, "repoquery-upgrades.txt")).unwrap();
    let sec = parse::parse_repoquery(&read(name, "repoquery-security.txt")).unwrap();
    parse::updates(&installed, &up, &sec)
}

#[test]
fn transaction_check_against_the_plan() {
    let all = security_updates("almalinux-10");
    let tx = parse::parse_transaction(&read("almalinux-10", "upgrade-assumeno.txt"));
    let (new, mismatches) = check_transaction(&tx, &all, &Exclusions::none());
    assert!(new.is_empty());
    assert!(mismatches.is_empty(), "{mismatches:?}");

    // The NEVRA fixture asked for curl and glibc; dnf also upgrades their dependencies.
    let two: Vec<Update> = all
        .iter()
        .filter(|u| u.name == "curl" || u.name == "glibc")
        .cloned()
        .collect();
    let tx = parse::parse_transaction(&read("almalinux-10", "upgrade-assumeno-nevra.txt"));
    let (_, mismatches) = check_transaction(&tx, &two, &Exclusions::none());
    assert!(
        mismatches
            .iter()
            .any(|m| m.contains("glibc-common would be upgraded")),
        "{mismatches:?}"
    );

    let ex = Exclusions::new(&["vim-*".to_owned()]).unwrap();
    let without_vim: Vec<Update> = all
        .iter()
        .filter(|u| !u.name.starts_with("vim-"))
        .cloned()
        .collect();
    let tx = parse::parse_transaction(&read("almalinux-10", "upgrade-assumeno.txt"));
    let (_, mismatches) = check_transaction(&tx, &without_vim, &ex);
    assert!(
        mismatches
            .iter()
            .any(|m| m.contains("vim-data is excluded")),
        "{mismatches:?}"
    );
}

#[tokio::test]
async fn plan_and_preflight_with_the_captured_transaction() {
    let name = "almalinux-10";
    let (_, base) = DnfProvider::from_fixtures(&dir(name)).unwrap();
    drop(base);
    let all = security_updates(name);
    let runner = std::sync::Arc::new(
        FixtureRunner::new()
            .with(
                rpm_qa_cmd().command_line(),
                CmdOutput::ok(read(name, "rpm-qa.txt")),
            )
            .with(
                repoquery_cmd(false).command_line(),
                CmdOutput::ok(read(name, "repoquery-upgrades.txt")),
            )
            .with(
                repoquery_cmd(true).command_line(),
                CmdOutput::ok(read(name, "repoquery-security.txt")),
            )
            .with(
                simulate_cmd(&all).command_line(),
                CmdOutput {
                    status: 1,
                    stdout: read(name, "upgrade-assumeno.txt"),
                    stderr: "Operation aborted.\n".into(),
                },
            ),
    );
    let p = DnfProvider::with_runner(runner);
    let plan = p.plan(Filter::All, &Exclusions::none()).await.unwrap();
    assert_eq!(plan.updates.len(), 16);
    assert!(plan.warnings.is_empty(), "{:?}", plan.warnings);

    let sink = MemorySink::default();
    let interrupted = AtomicBool::new(false);
    let ex = Exclusions::none();
    let root = Root::system();
    let ctx = ApplyCtx {
        filter: Filter::All,
        allow_removals: false,
        repair: false,
        exclude: &ex,
        root: &root,
        events: &sink,
        interrupted: &interrupted,
        inhibit: false,
    };
    let checks = p.preflight(&plan, &ctx).await;
    assert!(
        checks.iter().all(|c| c.status == CheckStatus::Pass),
        "{checks:?}"
    );
}

#[tokio::test]
async fn reboot_detection() {
    let required = FixtureRunner::new().with(
        needs_restarting_cmd().command_line(),
        CmdOutput {
            status: 1,
            stdout: "Core libraries or services have been updated since boot-up:\n  * kernel\n\nReboot is required to fully utilize these updates.\nMore information: https://access.redhat.com/solutions/27943\n".into(),
            stderr: String::new(),
        },
    );
    let p = DnfProvider::with_runner(std::sync::Arc::new(required));
    assert!(p.reboot_required().await.unwrap().required);

    let captured = FixtureRunner::new().with(
        needs_restarting_cmd().command_line(),
        CmdOutput::ok(read("fedora-44", "needs-restarting.txt")),
    );
    let p = DnfProvider::with_runner(std::sync::Arc::new(captured));
    assert!(!p.reboot_required().await.unwrap().required);

    // dnf4 without dnf-plugins-core: exit 1, but not a reboot.
    let missing = FixtureRunner::new().with(
        needs_restarting_cmd().command_line(),
        CmdOutput {
            status: 1,
            stdout: String::new(),
            stderr: "No such command: needs-restarting.\n".into(),
        },
    );
    let p =
        DnfProvider::with_runner(std::sync::Arc::new(missing)).with_root(Root::at("/nonexistent"));
    assert!(!p.reboot_required().await.unwrap().required);
}
