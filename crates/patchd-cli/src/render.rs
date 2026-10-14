//! The human table for `patchd scan`.

use std::fmt::Write as _;

use comfy_table::{Attribute, Cell, Color, ContentArrangement, Table, presets};
use patchd_core::model::{ScanReport, Update, UpdateKind};
use patchd_core::plan::{Plan, SkipReason, SkippedUpdate};

pub fn table(report: &ScanReport, packages: bool, color: bool) -> String {
    let mut out = String::new();
    let host = &report.host;
    let mut head = vec![
        host.hostname.clone(),
        host.os.pretty_name.clone(),
        host.arch.clone(),
    ];
    if let Some(age) = report.providers.iter().find_map(|p| p.index_age_secs) {
        head.push(format!("lists {} old", age_str(age)));
    }
    let _ = writeln!(out, "host  {}", head.join(" · "));

    if packages {
        let mut t = new_table(color, &["PACKAGE", "VERSION", "ARCH", ""]);
        for p in report.providers.iter().flat_map(|p| &p.packages) {
            t.add_row(vec![
                Cell::new(&p.name),
                Cell::new(&p.version),
                Cell::new(p.arch.as_deref().unwrap_or("")),
                Cell::new(if p.held { "held" } else { "" }),
            ]);
        }
        out.push('\n');
        out.push_str(&trimmed(&t));
    }

    let mut updates: Vec<&Update> = report.updates().collect();
    updates.sort_by(|a, b| rank(a).cmp(&rank(b)).then_with(|| a.name.cmp(&b.name)));
    if !updates.is_empty() {
        let mut t = new_table(color, &["KIND", "PACKAGE", "FROM", "TO", "ORIGIN"]);
        for u in &updates {
            let label = label(u);
            let mut kind = Cell::new(label);
            if color {
                kind = match label {
                    "security" => kind.fg(Color::Red).add_attribute(Attribute::Bold),
                    "major" => kind.fg(Color::Yellow),
                    "held" => kind.add_attribute(Attribute::Dim),
                    _ => kind,
                };
            }
            t.add_row(vec![
                kind,
                Cell::new(&u.name),
                Cell::new(u.from_version.as_deref().unwrap_or("—")),
                Cell::new(&u.to_version),
                Cell::new(origin(u)),
            ]);
        }
        out.push('\n');
        out.push_str(&trimmed(&t));
    }

    let pending: Vec<&&Update> = updates.iter().filter(|u| !u.held).collect();
    let security = pending
        .iter()
        .filter(|u| u.kind == UpdateKind::Security)
        .count();
    let held_count = updates.len() - pending.len();
    let removals: usize = report.providers.iter().map(|p| p.removals.len()).sum();
    let _ = writeln!(
        out,
        "\n{} {} ({security} security) · {held_count} held · {removals} {}",
        pending.len(),
        plural(pending.len(), "update", "updates"),
        plural(removals, "removal", "removals"),
    );
    out
}

/// The table for `patchd plan`: the same columns as `scan`, planned updates first, then
/// what the plan skips, then the footer.
pub fn plan_table(plan: &Plan, color: bool) -> String {
    let mut out = String::new();
    let _ = writeln!(out, "plan  {} · filter {}", plan.id, plan.filter);

    let mut planned: Vec<&Update> = plan.updates().collect();
    planned.sort_by(|a, b| rank(a).cmp(&rank(b)).then_with(|| a.name.cmp(&b.name)));
    let skipped: Vec<&SkippedUpdate> = plan
        .providers
        .iter()
        .flat_map(|p| &p.held_skipped)
        .collect();
    if !planned.is_empty() || !skipped.is_empty() {
        let mut t = new_table(color, &["KIND", "PACKAGE", "FROM", "TO", "ORIGIN"]);
        let rows = planned
            .iter()
            .map(|u| (label(u), *u))
            .chain(skipped.iter().map(|s| (s.reason.as_str(), &s.update)));
        for (label, u) in rows {
            let mut kind = Cell::new(label);
            if color {
                kind = match label {
                    "security" => kind.fg(Color::Red).add_attribute(Attribute::Bold),
                    "major" => kind.fg(Color::Yellow),
                    "held" | "excluded" => kind.add_attribute(Attribute::Dim),
                    _ => kind,
                };
            }
            t.add_row(vec![
                kind,
                Cell::new(&u.name),
                Cell::new(u.from_version.as_deref().unwrap_or("—")),
                Cell::new(&u.to_version),
                Cell::new(origin(u)),
            ]);
        }
        out.push('\n');
        out.push_str(&trimmed(&t));
    }

    let count = |reason: SkipReason| skipped.iter().filter(|s| s.reason == reason).count();
    let removals: usize = plan.providers.iter().map(|p| p.removals.len()).sum();
    let mut footer = format!(
        "will install {}, skip {} held",
        planned.len(),
        count(SkipReason::Held)
    );
    let excluded = count(SkipReason::Excluded);
    if excluded > 0 {
        let _ = write!(footer, ", skip {excluded} excluded");
    }
    let _ = writeln!(
        out,
        "\n{footer}, {removals} {}",
        plural(removals, "removal", "removals")
    );
    out
}

fn new_table(color: bool, header: &[&str]) -> Table {
    let mut t = Table::new();
    t.load_style(presets::NOTHING)
        .set_content_arrangement(ContentArrangement::Disabled)
        .set_header(header.to_vec());
    if color {
        t.enforce_styling();
    } else {
        t.force_no_tty();
    }
    for i in 0..header.len() {
        if let Some(col) = t.column_mut(i) {
            col.set_padding((0, 2));
        }
    }
    t
}

/// The table with the padding's trailing spaces removed.
fn trimmed(t: &Table) -> String {
    t.lines().fold(String::new(), |mut out, l| {
        let _ = writeln!(out, "{}", l.trim_end());
        out
    })
}

/// What the KIND column says: held and new dependencies are worth calling out, except
/// that a security fix always says so.
fn label(u: &Update) -> &'static str {
    match (u.held, u.kind, &u.from_version) {
        (true, _, _) => "held",
        (_, UpdateKind::Security, _) => "security",
        (_, _, None) => "new",
        (_, kind, _) => kind.as_str(),
    }
}

fn rank(u: &Update) -> u8 {
    match label(u) {
        "security" => 0,
        "major" => 1,
        "minor" => 2,
        "new" => 3,
        _ => 4,
    }
}

/// The origin that explains the kind: the security one for a security update.
fn origin(u: &Update) -> &str {
    let security = u
        .origins
        .iter()
        .find(|o| u.kind == UpdateKind::Security && o.contains("-security"));
    security
        .or_else(|| u.origins.first())
        .map_or("", String::as_str)
}

fn age_str(secs: u64) -> String {
    match secs {
        s if s < 3600 => format!("{}m", s / 60),
        s if s < 48 * 3600 => format!("{}h", s / 3600),
        s => format!("{}d", s / 86_400),
    }
}

fn plural<'a>(n: usize, one: &'a str, many: &'a str) -> &'a str {
    if n == 1 { one } else { many }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ages() {
        assert_eq!(age_str(59), "0m");
        assert_eq!(age_str(3 * 3600 + 5), "3h");
        assert_eq!(age_str(47 * 3600), "47h");
        assert_eq!(age_str(3 * 86_400), "3d");
    }
}
