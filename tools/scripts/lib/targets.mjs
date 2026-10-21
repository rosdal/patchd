// Reads eval/targets/reference-repos.yaml. The file is a flat list of string
// maps, so a small line parser is enough and keeps the root free of a YAML dependency.
import { readFileSync } from "node:fs";
import { join } from "node:path";

export const repoRoot = new URL("../../../", import.meta.url).pathname.replace(/^\/(\w:)/, "$1");
export const targetsDir = join(repoRoot, ".patchd", "targets");

export function readTargets(file = join(repoRoot, "eval", "targets", "reference-repos.yaml")) {
  const targets = [];
  let current;
  for (const [i, raw] of readFileSync(file, "utf8").split(/\r?\n/).entries()) {
    const line = raw.replace(/\s+#.*$/, "").trimEnd();
    if (line === "" || line.trimStart().startsWith("#")) continue;
    const m = /^(- |  )(\w+):\s*(.*)$/.exec(line);
    if (!m) throw new Error(`${file}:${i + 1}: cannot parse ${JSON.stringify(raw)}`);
    if (m[1] === "- ") targets.push((current = {}));
    if (!current) throw new Error(`${file}:${i + 1}: field outside a list item`);
    current[m[2]] = m[3];
  }
  for (const t of targets) {
    for (const k of ["id", "url", "commit", "shape", "install", "tsconfig"]) {
      if (!t[k]) throw new Error(`${file}: target ${t.id ?? "?"} is missing ${k}`);
    }
    if (!/^[0-9a-f]{40}$/.test(t.commit))
      throw new Error(`${file}: ${t.id}: commit must be a full SHA`);
  }
  return targets;
}

// selectTargets filters by ids given on the command line; no ids means all.
export function selectTargets(ids) {
  const all = readTargets();
  if (ids.length === 0) return all;
  const unknown = ids.filter((id) => !all.some((t) => t.id === id));
  if (unknown.length > 0) throw new Error(`unknown target(s): ${unknown.join(", ")}`);
  return all.filter((t) => ids.includes(t.id));
}
