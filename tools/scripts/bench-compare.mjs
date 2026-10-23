// Compares two bench-results.json files and raises the alarm: it exits 1 when
// any reference repo's full load and check, or its lowering to IR, got more
// than 15% slower, or its peak memory more than 20% higher, unless
// PERF_ACCEPTED is set (the perf-accepted label on the PR, with a reason in
// its description).
//
// Usage: node bench-compare.mjs <base.json> <head.json> [--out table.md]
import { readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

export const thresholds = { time: 0.15, memory: 0.2 };

export function compare(base, head) {
  const rows = [];
  for (const [repo, h] of Object.entries(head.repos)) {
    const b = base.repos[repo];
    if (!b) {
      rows.push({ repo, time: h.check.ns, lower: h.lower?.ns, memory: h.peakRssMB, new: true });
      continue;
    }
    const dt = b.check.ns ? h.check.ns / b.check.ns - 1 : 0;
    const dm = b.peakRssMB ? h.peakRssMB / b.peakRssMB - 1 : 0;
    // Results from before the IR existed have no lowering time.
    const dl = b.lower?.ns && h.lower?.ns ? h.lower.ns / b.lower.ns - 1 : undefined;
    rows.push({
      repo,
      baseTime: b.check.ns,
      time: h.check.ns,
      dt,
      baseLower: b.lower?.ns,
      lower: h.lower?.ns,
      dl,
      baseMemory: b.peakRssMB,
      memory: h.peakRssMB,
      dm,
      regressed: dt > thresholds.time || (dl ?? 0) > thresholds.time || dm > thresholds.memory,
    });
  }
  // A repo main measured that the PR didn't is a failure, not a pass: its
  // benchmark probably crashed or timed out.
  for (const repo of Object.keys(base.repos)) {
    if (!head.repos[repo]) rows.push({ repo, missing: true, regressed: true });
  }
  return rows;
}

const pct = (x) => (x === undefined ? "new" : `${x >= 0 ? "+" : ""}${(x * 100).toFixed(1)}%`);
const s = (ns) => (ns === undefined ? "–" : `${(ns / 1e9).toFixed(2)} s`);
const mb = (m) => (m === undefined ? "–" : `${m.toFixed(0)} MB`);

export function table(rows) {
  return [
    "| Repo | Load + check (main → PR) | Δ | Lower to IR (main → PR) | Δ | Peak RSS (main → PR) | Δ | |",
    "| --- | --- | ---: | --- | ---: | --- | ---: | --- |",
    ...rows.map((r) =>
      r.missing
        ? `| ${r.repo} | missing from this run | | | | | | regression |`
        : `| ${r.repo} | ${s(r.baseTime)} → ${s(r.time)} | ${pct(r.dt)} | ${s(r.baseLower)} → ${s(r.lower)} | ${pct(r.dl)} | ${mb(r.baseMemory)} → ${mb(r.memory)} | ${pct(r.dm)} | ${r.regressed ? "regression" : ""} |`,
    ),
    "",
    `Alarm: load + check or lowering more than ${thresholds.time * 100}% slower, or peak memory more than ${thresholds.memory * 100}% higher, on any repo.`,
  ].join("\n");
}

function main() {
  const args = process.argv.slice(2);
  const outIdx = args.indexOf("--out");
  const out = outIdx >= 0 ? args.splice(outIdx, 2)[1] : undefined;
  const [basePath, headPath] = args;
  const rows = compare(
    JSON.parse(readFileSync(basePath, "utf8")),
    JSON.parse(readFileSync(headPath, "utf8")),
  );
  const md = table(rows);
  if (out) writeFileSync(out, md + "\n");
  console.log(md);
  const regressed = rows.filter((r) => r.regressed);
  if (regressed.length > 0) {
    if (process.env.PERF_ACCEPTED) {
      console.log(
        `regressions in ${regressed.map((r) => r.repo).join(", ")} accepted (perf-accepted)`,
      );
    } else {
      console.error(
        `performance regression in ${regressed.map((r) => r.repo).join(", ")}; add the perf-accepted label with a reason to accept it`,
      );
      process.exitCode = 1;
    }
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main();
