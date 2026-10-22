// Moves the pinned typescript-go to another commit (steps 1-3 of
// docs/engine/tsgo-upgrades.md):
//
//   1. go get the commit in every shim module and the root module, and record
//      it in engine/tsbridge/TSGO_VERSION;
//   2. regenerate the shims and the kind table (pnpm gen);
//   3. go build ./engine/... and run the tsbridge tests.
//
// Steps 4-5 (oracle, benchmarks, PR) run in CI on the upgrade PR.
//
// Usage: pnpm tsgo:update <commit|tag> [--npm typescript@7.x.y]
import { execFileSync } from "node:child_process";
import { readdirSync, statSync, writeFileSync } from "node:fs";
import { join, relative } from "node:path";
import { repoRoot } from "./lib/targets.mjs";

const mod = "github.com/microsoft/typescript-go";
const args = process.argv.slice(2);
const npmIdx = args.indexOf("--npm");
const npm = npmIdx >= 0 ? args.splice(npmIdx, 2)[1] : "";
const [ref] = args;
if (!ref) {
  console.error("usage: pnpm tsgo:update <commit|tag> [--npm typescript@7.x.y]");
  process.exit(2);
}

const sh = (cmd, cmdArgs, cwd = repoRoot) => {
  console.log(
    `$ ${cmd} ${cmdArgs.join(" ")}${cwd === repoRoot ? "" : `   (in ${relative(repoRoot, cwd)})`}`,
  );
  return execFileSync(cmd, cmdArgs, {
    cwd,
    stdio: ["ignore", "pipe", "inherit"],
    encoding: "utf8",
  });
};

// Resolve the ref to a module version and commit through the Go proxy.
const info = JSON.parse(sh("go", ["list", "-m", "-json", `${mod}@${ref}`]));
const version = info.Version;
const commit = info.Origin?.Hash ?? "";
console.log(`typescript-go ${ref} -> ${version} (${commit || "commit unknown"})`);

// 1. Every shim module, then the root.
const shimRoot = join(repoRoot, "engine", "tsbridge", "shim");
const shimModules = [];
(function walk(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (!statSync(p).isDirectory()) continue;
    if (name !== "gen") {
      try {
        statSync(join(p, "go.mod"));
        shimModules.push(p);
      } catch {
        // not a module
      }
    }
    walk(p);
  }
})(shimRoot);
for (const dir of shimModules) {
  sh("go", ["get", `${mod}@${version}`], dir);
  sh("go", ["mod", "tidy"], dir);
}
sh("go", ["get", `${mod}@${version}`]);
sh("go", ["mod", "tidy"]);

let tag = "";
try {
  const tags = sh("git", ["ls-remote", "--tags", `https://${mod}`]);
  tag =
    tags
      .split("\n")
      .filter((l) => commit && l.startsWith(commit) && !l.endsWith("^{}"))
      .map((l) => l.split("refs/tags/")[1])
      .find(Boolean) ?? "";
} catch {
  // offline: leave the tag empty
}
writeFileSync(
  join(repoRoot, "engine", "tsbridge", "TSGO_VERSION"),
  [
    "# The typescript-go commit tsbridge is built against (ADR 0007).",
    "# Change it only with `pnpm tsgo:update <commit>`; see docs/engine/tsgo-upgrades.md.",
    `commit=${commit}`,
    `module=${version}`,
    `tag=${tag}`,
    `npm=${npm || (tag.startsWith("typescript/v") ? `typescript@${tag.slice("typescript/v".length)}` : "")}`,
    "",
  ].join("\n"),
);

// 2. Regenerate.
execFileSync(process.execPath, [join(repoRoot, "tools", "scripts", "gen.mjs")], {
  cwd: repoRoot,
  stdio: "inherit",
});

// 3. Build and test.
sh("go", ["build", "./engine/..."]);
execFileSync("go", ["test", "./engine/tsbridge/..."], { cwd: repoRoot, stdio: "inherit" });
console.log(`
typescript-go is now ${version}. Next (docs/engine/tsgo-upgrades.md):
  4. pnpm oracle and pnpm bench on the reference repos (CI runs both on the PR)
  5. open "chore(engine): bump typescript-go to ${tag || version}" with the before/after numbers`);
