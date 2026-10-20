// The build version shared by the Go binary and the web app: `git describe`,
// or v0.0.0-dev outside a git checkout.
import { execFileSync } from "node:child_process";

export function git(...args) {
  try {
    return execFileSync("git", args, {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    }).trim();
  } catch {
    return "";
  }
}

export function buildVersion() {
  return git("describe", "--tags", "--always", "--dirty") || "v0.0.0-dev";
}
