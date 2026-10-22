// Regenerates engine/tsbridge/shim/*/shim.go from the pinned typescript-go
// (ADR 0007). The generator is engine/tsbridge/shim/gen; this wrapper exists so
// `pnpm gen` and `pnpm tsgo:update` can run it from the repo root. The output is
// committed exactly as generated; gofumpt and golangci-lint skip generated files.
import { execFileSync } from "node:child_process";
import { join } from "node:path";
import { repoRoot } from "./lib/targets.mjs";

const shimDir = join(repoRoot, "engine", "tsbridge", "shim");
execFileSync("go", ["run", "."], { cwd: join(shimDir, "gen"), stdio: "inherit" });
console.log("gen-tsgo-shim: shims regenerated");
