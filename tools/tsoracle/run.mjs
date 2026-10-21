// pnpm oracle [id...]: for each reference repo, sample positions with
// TypeScript 6, answer the same positions with `patchd debug types --batch`,
// and write .patchd/oracle/<id>.md (and <id>.json with the numbers).
// Fetch the repos first (pnpm targets:fetch) and build patchd (pnpm build:go).
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { repoRoot, selectTargets, targetsDir } from "../scripts/lib/targets.mjs";

const args = process.argv.slice(2);
const flag = (name, def) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 ? args.splice(i, 2)[1] : def;
};
const n = flag("n", "500");
const seed = flag("seed", "1");
const outDir = join(repoRoot, ".patchd", "oracle");
const patchd = join(repoRoot, "bin", process.platform === "win32" ? "patchd.exe" : "patchd");
if (!existsSync(patchd)) throw new Error(`${patchd} not found: run pnpm build:go first`);
mkdirSync(outDir, { recursive: true });

const node = (script, scriptArgs, extra = []) =>
  execFileSync(process.execPath, [...extra, join(import.meta.dirname, script), ...scriptArgs], {
    stdio: "inherit",
  });

const summaries = [];
for (const t of selectTargets(args)) {
  const dir = join(targetsDir, t.id);
  if (!existsSync(dir)) {
    console.warn(`${t.id}: not fetched; run pnpm targets:fetch ${t.id}`);
    continue;
  }
  const work = join(outDir, t.id);
  mkdirSync(work, { recursive: true });
  const projects = join(work, "projects.json");
  const big = { maxBuffer: 1 << 30, encoding: "utf8" };
  writeFileSync(
    projects,
    execFileSync(
      patchd,
      ["debug", "project", dir, "--root", dir, "--no-load", "--format", "json"],
      big,
    ),
  );
  node(
    "sample.mjs",
    [
      "--projects",
      projects,
      "--root",
      dir,
      "--n",
      n,
      "--seed",
      seed,
      "--out",
      join(work, "ts6.json"),
      "--positions",
      join(work, "positions.json"),
    ],
    ["--max-old-space-size=16384"],
  );
  const batch = [
    "debug",
    "types",
    "--batch",
    join(work, "positions.json"),
    "--root",
    dir,
    "--format",
    "json",
  ];
  writeFileSync(join(work, "ts7.json"), execFileSync(patchd, batch, big));
  node("compare.mjs", [
    "--ts6",
    join(work, "ts6.json"),
    "--ts7",
    join(work, "ts7.json"),
    "--root",
    dir,
    "--repo",
    t.id,
    "--out",
    join(outDir, `${t.id}.md`),
  ]);
  summaries.push(JSON.parse(readFileSync(join(outDir, `${t.id}.json`), "utf8")));
}

const lines = [
  "| Repo | Positions | Match | Cosmetic | Expected | Real | Real-match rate |",
  "| --- | ---: | ---: | ---: | ---: | ---: | ---: |",
  ...summaries.map(
    (s) =>
      `| ${s.repo} | ${s.answered} | ${s.match} | ${s.cosmetic} | ${s.expected} | ${s.real} | ${(s.realMatchRate * 100).toFixed(1)}% |`,
  ),
];
writeFileSync(join(outDir, "summary.md"), lines.join("\n") + "\n");
console.log(lines.join("\n"));
const below = summaries.filter((s) => s.realMatchRate < 0.98);
if (below.length > 0) {
  console.error(`below the 98% target: ${below.map((s) => s.repo).join(", ")}`);
  process.exitCode = 1;
}
