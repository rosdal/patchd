//! Compares `rpmvercmp` with rpm's own `rpm.vercmp` on rpm's test vectors and on every
//! version pair in the dnf fixtures. Skips (passes with a note) where rpm is not
//! installed; CI runs it in a Fedora container.

use std::cmp::Ordering;
use std::fmt::Write as _;
use std::path::Path;
use std::process::Command;

use patchd_core::version::rpmvercmp;

/// Consecutive installed versions to compare per fixture.
const PAIRS_PER_INVENTORY: usize = 150;

fn pairs() -> Vec<(String, String)> {
    let mut pairs: Vec<(String, String)> = [
        ("1.0", "1.0"),
        ("1.0", "2.0"),
        ("2.0.1", "2.0"),
        ("2.0.1a", "2.0.1"),
        ("5.5p1", "5.5p2"),
        ("5.5p10", "5.5p2"),
        ("10xyz", "10.1xyz"),
        ("xyz10", "xyz10.1"),
        ("xyz.4", "8"),
        ("1.0~rc1", "1.0"),
        ("1.0~rc1", "1.0~rc2"),
        ("1.0~rc1~git123", "1.0~rc1"),
        ("1.0^", "1.0"),
        ("1.0^git1", "1.0"),
        ("1.0^git1", "1.0.1"),
        ("1.0^git1", "1.0~rc1"),
        ("1.0^git1~pre", "1.0^git1"),
        ("1.0a", "1.0.1"),
        ("1.010", "1.9"),
        ("1.001", "1.1"),
        ("a", "1"),
        ("1.0_1", "1.0.1"),
        ("fc4", "fc.4"),
        ("3.0.0_fc", "3.0.0.fc"),
        ("1b.fc17", "1.fc17"),
    ]
    .iter()
    .map(|(a, b)| ((*a).to_owned(), (*b).to_owned()))
    .collect();

    let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/fixtures/dnf");
    let Ok(dirs) = std::fs::read_dir(&root) else {
        return pairs;
    };
    // Version and release are compared separately by rpm; compare the version-release
    // string as rpmvercmp sees one field.
    let vr = |evr: &str| evr.split_once(':').map_or(evr, |(_, vr)| vr).to_owned();
    for dir in dirs {
        let dir = dir.unwrap().path();
        if let Ok(inv) = std::fs::read_to_string(dir.join("rpm-qa.txt")) {
            let versions: Vec<String> = inv
                .lines()
                .filter_map(|l| l.split('\t').nth(1))
                .map(vr)
                .collect();
            for w in versions.windows(2).take(PAIRS_PER_INVENTORY) {
                pairs.push((w[0].clone(), w[1].clone()));
            }
        }
    }
    pairs
}

#[test]
fn ordering_matches_rpm() {
    if Command::new("rpm").arg("--version").output().is_err() {
        eprintln!("rpm not found; skipping the rpm cross-check");
        return;
    }
    let pairs = pairs();
    // One rpm process for every pair, printing one result per line.
    let mut lua = String::from("%{lua:");
    for (a, b) in &pairs {
        // rpm's Lua `print` adds no newline.
        let _ = write!(lua, "print(rpm.vercmp([[{a}]], [[{b}]]) .. '\\n') ");
    }
    lua.push('}');
    let out = Command::new("rpm")
        .args(["--eval", &lua])
        .output()
        .expect("run rpm");
    let theirs: Vec<i32> = String::from_utf8(out.stdout)
        .unwrap()
        .lines()
        .filter(|l| !l.is_empty())
        .map(|l| l.trim().parse().unwrap())
        .collect();
    assert_eq!(theirs.len(), pairs.len());

    let mismatches: Vec<String> = pairs
        .iter()
        .zip(theirs)
        .filter_map(|((a, b), theirs)| {
            let ours = rpmvercmp(a, b);
            let theirs = theirs.cmp(&0);
            (ours != theirs).then(|| format!("{a} vs {b}: ours {ours:?}, rpm {theirs:?}"))
        })
        .collect();
    assert!(mismatches.is_empty(), "{}", mismatches.join("\n"));
    assert!(pairs.iter().any(|(a, b)| rpmvercmp(a, b) == Ordering::Less));
    eprintln!("{} version pairs agree with rpm", pairs.len());
}
