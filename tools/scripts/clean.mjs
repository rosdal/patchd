// Removes build output. Dependencies (node_modules) are left alone.
import { rmSync } from "node:fs";

for (const dir of ["bin", "dist", "web/dist", "ide/vscode/out"]) {
  rmSync(dir, { recursive: true, force: true });
  console.log(`removed ${dir}`);
}
