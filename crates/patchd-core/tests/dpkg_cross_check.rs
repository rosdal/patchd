//! Compares `DebVersion` ordering with `dpkg --compare-versions` on every version pair in
//! the fixtures. Skips (passes with a note) where dpkg is not installed.

use std::cmp::Ordering;
use std::path::Path;
use std::process::Command;

use patchd_core::version::DebVersion;

/// Consecutive installed versions to compare per fixture; enough variety, bounded runtime.
const PAIRS_PER_INVENTORY: usize = 150;

fn dpkg(a: &str, op: &str, b: &str) -> bool {
    let status = Command::new("dpkg")
        .args(["--compare-versions", a, op, b])
        .status()
        .expect("run dpkg");
    status.success()
}

fn dpkg_cmp(a: &str, b: &str) -> Ordering {
    if dpkg(a, "lt", b) {
        Ordering::Less
    } else if dpkg(a, "eq", b) {
        Ordering::Equal
    } else {
        Ordering::Greater
    }
}

fn fixture_pairs() -> Vec<(String, String)> {
    let mut pairs: Vec<(String, String)> = [
        ("1.0~rc1", "1.0"),
        ("1.0~~", "1.0~"),
        ("1.0", "1.0a"),
        ("1.0", "1.0+b1"),
        ("2.30-2", "2.30-2ubuntu1"),
        ("1.0-1", "1.0-10"),
        ("1:0.1", "2.0"),
        ("1.01", "1.1"),
        ("1.0", "1.0-0"),
        ("1.0-1~bpo12+1", "1.0-1"),
        ("2:1.0", "1:9.9"),
        ("1.0.0", "1.0"),
        ("a", "b"),
        ("1.0-a.b", "1.0-a+b"),
    ]
    .iter()
    .map(|(a, b)| ((*a).to_owned(), (*b).to_owned()))
    .collect();

    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures/apt");
    for dir in std::fs::read_dir(&root).unwrap() {
        let dir = dir.unwrap().path();
        if let Ok(sim) = std::fs::read_to_string(dir.join("apt-get-s-dist-upgrade.txt")) {
            for line in sim.lines().filter(|l| l.starts_with("Inst ")) {
                // Inst name [old] (new ...)
                let old = line.split_once(" [").and_then(|(_, r)| r.split_once(']'));
                let new = line
                    .split_once(" (")
                    .and_then(|(_, r)| r.split_whitespace().next());
                if let (Some((old, _)), Some(new)) = (old, new) {
                    pairs.push((old.to_owned(), new.to_owned()));
                }
            }
        }
        if let Ok(inv) = std::fs::read_to_string(dir.join("dpkg-query.txt")) {
            let versions: Vec<&str> = inv.lines().filter_map(|l| l.split('\t').nth(1)).collect();
            for w in versions.windows(2).take(PAIRS_PER_INVENTORY) {
                pairs.push((w[0].to_owned(), w[1].to_owned()));
            }
        }
    }
    pairs
}

#[test]
fn ordering_matches_dpkg() {
    if Command::new("dpkg").arg("--version").output().is_err() {
        eprintln!("dpkg not found; skipping the dpkg cross-check");
        return;
    }
    let pairs = fixture_pairs();
    assert!(pairs.len() > 100, "only {} pairs", pairs.len());

    let mismatches: Vec<String> = pairs
        .iter()
        .filter_map(|(a, b)| {
            let ours = DebVersion::parse(a)
                .unwrap()
                .cmp(&DebVersion::parse(b).unwrap());
            let theirs = dpkg_cmp(a, b);
            (ours != theirs).then(|| format!("{a} vs {b}: ours {ours:?}, dpkg {theirs:?}"))
        })
        .collect();
    assert!(mismatches.is_empty(), "{}", mismatches.join("\n"));
    eprintln!("{} version pairs agree with dpkg", pairs.len());
}
