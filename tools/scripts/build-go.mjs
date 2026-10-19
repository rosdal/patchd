// Builds ./cmd/patchd into bin/patchd with version info injected via -ldflags.
import { execFileSync } from "node:child_process";

const git = (...args) => {
  try {
    return execFileSync("git", args, {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    }).trim();
  } catch {
    return "";
  }
};

const version = git("describe", "--tags", "--always", "--dirty") || "v0.0.0-dev";
const commit = git("rev-parse", "--short", "HEAD") || "none";
const date = new Date().toISOString();
const pkg = "github.com/patchd/patchd/internal/buildinfo";
const ldflags = [
  `-X ${pkg}.Version=${version}`,
  `-X ${pkg}.Commit=${commit}`,
  `-X ${pkg}.Date=${date}`,
].join(" ");
const out = process.platform === "win32" ? "bin/patchd.exe" : "bin/patchd";

execFileSync("go", ["build", "-trimpath", "-ldflags", ldflags, "-o", out, "./cmd/patchd"], {
  stdio: "inherit",
});
