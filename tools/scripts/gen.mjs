// pnpm gen: every code generator in the repo, in dependency order.
// Later: buf generate (API) and sqlc generate (store).
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";

for (const script of ["gen-tsgo-shim.mjs", "gen-kinds.mjs"]) {
  execFileSync(process.execPath, [fileURLToPath(new URL(script, import.meta.url))], {
    stdio: "inherit",
  });
}
