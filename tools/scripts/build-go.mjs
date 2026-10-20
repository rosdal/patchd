// Builds ./cmd/patchd into bin/patchd with version info injected via -ldflags.
import { execFileSync } from "node:child_process";
import { buildVersion, git } from "./version.mjs";

const commit = git("rev-parse", "--short", "HEAD") || "none";
const date = new Date().toISOString();
const pkg = "github.com/patchd/patchd/internal/buildinfo";
const ldflags = [
  `-X ${pkg}.Version=${buildVersion()}`,
  `-X ${pkg}.Commit=${commit}`,
  `-X ${pkg}.Date=${date}`,
].join(" ");
const out = process.platform === "win32" ? "bin/patchd.exe" : "bin/patchd";

execFileSync("go", ["build", "-trimpath", "-ldflags", ldflags, "-o", out, "./cmd/patchd"], {
  stdio: "inherit",
});
