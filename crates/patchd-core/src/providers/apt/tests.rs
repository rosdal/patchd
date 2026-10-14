//! Fixture tests: the parsed report per recorded host, snapshotted with insta.

use std::path::PathBuf;

use super::*;
use crate::model::UpdateKind;
use crate::scan::provider_report;

fn fixture_dir(name: &str) -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("tests/fixtures/apt")
        .join(name)
}

async fn report(name: &str) -> (crate::model::ProviderReport, Vec<String>) {
    let (provider, runner) = AptProvider::from_fixtures(&fixture_dir(name)).unwrap();
    let report = provider_report(&provider).await;
    (report, runner.seen())
}

macro_rules! snapshot_fixture {
    ($test:ident, $dir:literal) => {
        #[tokio::test]
        async fn $test() {
            let (report, _) = report($dir).await;
            insta::assert_json_snapshot!($dir, report);
        }
    };
}

snapshot_fixture!(snapshot_ubuntu_22_04, "ubuntu-22.04");
snapshot_fixture!(snapshot_ubuntu_24_04, "ubuntu-24.04");
snapshot_fixture!(snapshot_ubuntu_24_04_held, "ubuntu-24.04-held");
snapshot_fixture!(snapshot_ubuntu_24_04_phased, "ubuntu-24.04-phased");
snapshot_fixture!(snapshot_ubuntu_26_04, "ubuntu-26.04");
snapshot_fixture!(snapshot_debian_12, "debian-12");
snapshot_fixture!(snapshot_debian_13, "debian-13");
snapshot_fixture!(snapshot_debian_13_held, "debian-13-held");
snapshot_fixture!(snapshot_edge_cases, "edge-cases");

#[tokio::test]
async fn every_distro_has_base_packages() {
    for dir in [
        "ubuntu-22.04",
        "ubuntu-24.04",
        "ubuntu-26.04",
        "debian-12",
        "debian-13",
    ] {
        let (report, seen) = report(dir).await;
        assert_eq!(report.error, None, "{dir}");
        assert!(
            report.packages.len() > 50,
            "{dir}: {}",
            report.packages.len()
        );
        for name in ["base-files", "libc6", "dpkg", "apt"] {
            assert!(
                report.packages.iter().any(|p| p.name == name),
                "{dir} lacks {name}"
            );
        }
        // Read-only: exactly these commands, nothing else.
        assert_eq!(
            seen.iter()
                .map(|s| s.split(' ').next().unwrap())
                .collect::<Vec<_>>(),
            ["dpkg-query", "apt-get", "apt-mark"],
            "{dir}"
        );
    }
}

#[tokio::test]
async fn ubuntu_24_04_counts_and_kinds() {
    let (report, _) = report("ubuntu-24.04").await;
    let kinds: Vec<_> = report
        .updates
        .iter()
        .map(|u| (u.name.as_str(), u.kind))
        .collect();
    assert_eq!(
        kinds,
        [
            ("libaudit-common", UpdateKind::Minor),
            ("libaudit1", UpdateKind::Minor),
            ("libssl3t64", UpdateKind::Security),
        ]
    );
}

#[tokio::test]
async fn held_package_is_reported_from_policy() {
    for (dir, name) in [
        ("ubuntu-24.04-held", "libssl3t64"),
        ("debian-13-held", "liblzma5"),
    ] {
        let (report, seen) = report(dir).await;
        let held: Vec<_> = report.updates.iter().filter(|u| u.held).collect();
        assert_eq!(held.len(), 1, "{dir}");
        assert_eq!(held[0].name, name);
        assert_eq!(held[0].kind, UpdateKind::Security, "{dir}");
        assert!(report.packages.iter().any(|p| p.name == name && p.held));
        assert!(seen.contains(&format!("apt-cache policy {name}")));
        // The held package is explained by `held`, not by a kept-back warning.
        assert!(report.warnings.is_empty(), "{dir}: {:?}", report.warnings);
    }
}

#[tokio::test]
async fn edge_cases() {
    let (report, _) = report("edge-cases").await;
    let find = |name: &str| report.updates.iter().find(|u| u.name == name).unwrap();

    assert_eq!(report.removals, ["pve-firmware-legacy"]);
    assert_eq!(find("libgnutls30").kind, UpdateKind::Security);
    assert_eq!(find("htop").kind, UpdateKind::Major);
    assert_eq!(find("libaudit1").from_version.as_deref(), Some("1:3.0.9-1"));
    assert_eq!(find("libc6:i386").arch.as_deref(), Some("i386"));
    let kernel = find("proxmox-kernel-6.8.12-4-pve-signed");
    assert_eq!(kernel.from_version, None);
    assert!(find("zfsutils-linux").held);

    assert!(report.packages.iter().any(|p| p.name == "libc6"));
    assert!(report.packages.iter().any(|p| p.name == "libc6:i386"));
    assert!(!report.packages.iter().any(|p| p.name == "old-removed-pkg"));

    let warnings = report.warnings.join("\n");
    assert!(warnings.contains("pve-manager"), "{warnings}");
    assert!(warnings.contains("would remove 1 package(s)"), "{warnings}");
    assert!(!warnings.contains("kept back"), "{warnings}");
}

#[tokio::test]
async fn phased_updates_warn() {
    let (report, _) = report("ubuntu-24.04-phased").await;
    assert_eq!(report.warnings.len(), 1);
    assert!(report.warnings[0].contains("phased rollout: libsystemd0, systemd"));
}

#[tokio::test]
async fn parse_failure_becomes_provider_error() {
    let (report, _) = report("broken").await;
    let err = report.error.unwrap();
    assert!(err.contains("line 2"), "{err}");
    // available() still ran and reported.
    assert_eq!(report.updates.len(), 1);
}

#[test]
fn lists_age_reads_newest_packages_file() {
    let dir = std::env::temp_dir().join(format!("patchd-lists-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    assert_eq!(lists_age(&dir), None);
    std::fs::write(dir.join("lock"), "").unwrap();
    assert_eq!(lists_age(&dir), None, "only *_Packages files count");
    std::fs::write(
        dir.join("deb.debian.org_debian_dists_trixie_main_binary-amd64_Packages"),
        "",
    )
    .unwrap();
    assert!(lists_age(&dir).unwrap() < Duration::from_secs(60));
    std::fs::remove_dir_all(&dir).unwrap();
}
