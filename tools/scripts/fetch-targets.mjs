// Clones each reference repo at its pinned commit into .patchd/targets/<id> and
// installs its dependencies with lifecycle scripts disabled. The repos are only
// analysed, never run. Usage: pnpm targets:fetch [id...]
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { selectTargets, targetsDir } from "./lib/targets.mjs";

// pnpm is a .cmd shim on Windows, which only runs through a shell; the
// arguments come from reference-repos.yaml, never from user input.
const run = (cmd, args, cwd) =>
  process.platform === "win32" && cmd !== "git"
    ? execFileSync([cmd, ...args].join(" "), { cwd, stdio: "inherit", shell: true })
    : execFileSync(cmd, args, { cwd, stdio: "inherit" });
const out = (cmd, args, cwd) =>
  execFileSync(cmd, args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim();

mkdirSync(targetsDir, { recursive: true });
for (const t of selectTargets(process.argv.slice(2))) {
  const dir = join(targetsDir, t.id);
  if (!existsSync(join(dir, ".git"))) {
    run("git", ["init", "--quiet", dir]);
    run("git", ["remote", "add", "origin", t.url], dir);
  }
  if (tryOut("git", ["rev-parse", "HEAD"], dir) === t.commit) {
    console.log(`${t.id}: at ${t.commit.slice(0, 12)}`);
  } else {
    console.log(`${t.id}: fetching ${t.commit.slice(0, 12)}`);
    run("git", ["fetch", "--quiet", "--depth", "1", "origin", t.commit], dir);
    run(
      "git",
      ["-c", "advice.detachedHead=false", "checkout", "--quiet", "--force", "FETCH_HEAD"],
      dir,
    );
  }
  const installed = join(dir, "node_modules", ".patchd-installed");
  if (t.install !== "none" && !existsSync(installed)) {
    console.log(`${t.id}: ${t.install}`);
    const steps = t.install.split("&&").map((step) => step.trim().split(/\s+/));
    for (const [cmd, ...args] of steps) {
      if (cmd !== "pnpm") throw new Error(`${t.id}: install steps must use pnpm, got ${cmd}`);
      const installs = ["install", "i", "import"].includes(args[0]);
      if (args[0] !== "import" && installs && !args.includes("--ignore-scripts"))
        throw new Error(`${t.id}: pnpm install must pass --ignore-scripts`);
      // Targets live inside this repo: without its own workspace file, pnpm would
      // walk up and install into Patchd's workspace instead.
      if (
        !existsSync(join(dir, "pnpm-workspace.yaml")) &&
        !args.includes("--ignore-workspace") &&
        installs
      ) {
        throw new Error(
          `${t.id}: pnpm install without pnpm-workspace.yaml must pass --ignore-workspace`,
        );
      }
    }
    try {
      for (const [cmd, ...args] of steps) run(cmd, args, dir);
      writeFileSync(installed, t.install + "\n");
    } catch {
      // A failed install is a realistic input (TSB001): keep going, tsbridge loads it anyway.
      console.warn(`${t.id}: install failed; continuing without complete node_modules`);
    }
  }
}

function tryOut(cmd, args, cwd) {
  try {
    return out(cmd, args, cwd);
  } catch {
    return "";
  }
}
