// pnpm bench [id...]: runs the engine benchmarks (engine/tsbridge/bench_test.go,
// engine/ir/bench_test.go and engine/bench_test.go) on each fetched reference repo, one process per
// repo and package so the peak RSS is that run's, and writes
// bench-results.json (numbers per repo) and bench-results.txt (raw
// `go test -bench` output, the input benchstat reads).
//
// Options: --count N (repeat each benchmark; the median is recorded).
import { execFileSync } from "node:child_process";
import { existsSync, writeFileSync } from "node:fs";
import { cpus, totalmem } from "node:os";
import { join } from "node:path";
import { repoRoot, selectTargets, targetsDir } from "./lib/targets.mjs";

const args = process.argv.slice(2);
const countIdx = args.indexOf("--count");
const count = countIdx >= 0 ? args.splice(countIdx, 2)[1] : "1";
const exe = process.platform === "win32" ? ".test.exe" : ".test";
const packages = [
  { pkg: "tsbridge", bin: join(repoRoot, "bin", "tsbridge" + exe) },
  { pkg: "ir", bin: join(repoRoot, "bin", "ir" + exe) },
  { pkg: "", bin: join(repoRoot, "bin", "engine" + exe) },
];

for (const { pkg, bin } of packages) {
  execFileSync("go", ["test", "-c", "-o", bin, pkg ? `./engine/${pkg}` : "./engine"], {
    cwd: repoRoot,
    stdio: "inherit",
  });
}

let raw = "";
const repos = {};
for (const t of selectTargets(args)) {
  if (!existsSync(join(targetsDir, t.id))) {
    console.warn(`${t.id}: not fetched; skipped (pnpm targets:fetch ${t.id})`);
    continue;
  }
  console.log(`${t.id}: benchmarking`);
  const rows = [];
  for (const { pkg, bin } of packages) {
    const out = execFileSync(
      bin,
      [
        "-test.run",
        "^$",
        "-test.bench",
        ".",
        "-test.benchtime",
        "1x",
        "-test.benchmem",
        "-test.count",
        count,
        "-test.timeout",
        "60m",
      ],
      {
        cwd: join(repoRoot, "engine", pkg),
        env: { ...process.env, PATCHD_TARGETS: targetsDir, PATCHD_BENCH_REPOS: t.id },
        encoding: "utf8",
        maxBuffer: 1 << 26,
      },
    );
    raw += out;
    rows.push(...parse(out));
  }
  repos[t.id] = summarise(rows);
  const r = repos[t.id];
  console.log(
    `${t.id}: load+check ${(r.check.ns / 1e9).toFixed(2)} s, lower ${(r.lower.ns / 1e9).toFixed(2)} s, taint ${(r.taint.ns / 1e9).toFixed(2)} s, scan ${(r.scan.ns / 1e9).toFixed(2)} s, peak RSS ${r.peakRssMB.toFixed(0)} MB`,
  );
}

const results = {
  machine: {
    os: process.platform,
    arch: process.arch,
    cpus: cpus().length,
    cpu: cpus()[0]?.model,
    memoryGB: Math.round(totalmem() / 2 ** 30),
  },
  repos,
};
writeFileSync(join(repoRoot, "bench-results.json"), JSON.stringify(results, null, 2) + "\n");
writeFileSync(join(repoRoot, "bench-results.txt"), raw);
console.log("wrote bench-results.json and bench-results.txt");

// parse reads `go test -bench` lines: name, iterations, then value/unit pairs.
function parse(out) {
  const rows = [];
  for (const line of out.split(/\r?\n/)) {
    const m = /^Benchmark(\w+)\/([\w.-]+?)(-\d+)?\s+\d+\s+(.*)$/.exec(line);
    if (!m) continue;
    const metrics = {};
    const fields = m[4].trim().split(/\s+/);
    for (let i = 0; i + 1 < fields.length; i += 2) metrics[fields[i + 1]] = Number(fields[i]);
    rows.push({ bench: m[1], repo: m[2], metrics });
  }
  return rows;
}

function median(xs) {
  const s = xs.toSorted((a, b) => a - b);
  return s.length === 0 ? 0 : s[Math.floor((s.length - 1) / 2)];
}

function summarise(rows) {
  const by = {};
  for (const r of rows) (by[r.bench] ??= []).push(r.metrics);
  const pick = (bench, extra = []) => {
    const ms = by[bench] ?? [];
    const out = {
      ns: median(ms.map((m) => m["ns/op"])),
      bytes: median(ms.map((m) => m["B/op"])),
      allocs: median(ms.map((m) => m["allocs/op"])),
      ...(ms[0]?.files !== undefined ? { files: ms[0].files } : {}),
      ...(ms[0]?.["peak-heap-MB"] !== undefined
        ? { peakHeapMB: median(ms.map((m) => m["peak-heap-MB"])) }
        : {}),
    };
    for (const [unit, key] of extra) {
      if (ms[0]?.[unit] !== undefined) out[key] = median(ms.map((m) => m[unit]));
    }
    return out;
  };
  const rss = rows.map((r) => r.metrics["peak-rss-MB"]).filter((x) => x !== undefined);
  return {
    discover: pick("Discover"),
    load: pick("Load"),
    check: pick("Check"),
    typeOf: pick("TypeOf"),
    lower: pick("Lower", [
      ["lines", "lines"],
      ["ns/kline", "nsPerKLine"],
      ["ir-B/src-B", "irBytesPerSourceByte"],
      ["extra-heap-MB", "extraHeapMB"],
    ]),
    taint: pick("Taint", [["functions-analysed", "functionsAnalysed"]]),
    scan: pick("Scan", [["findings", "findings"]]),
    peakRssMB: rss.length ? Math.max(...rss) : 0,
  };
}
