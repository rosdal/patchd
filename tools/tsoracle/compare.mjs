// Compares tsbridge's answers (TypeScript 7) with the oracle's (TypeScript 6)
// position by position, classifies every difference and writes a Markdown
// report.
//
//   match     same type string, symbol and declaration
//   cosmetic  the same type printed differently (import() qualifiers, union
//             order, whitespace, truncation)
//   expected  a known TypeScript 6 vs 7 behaviour difference (expected.json)
//   real      a different type, symbol or declaration: a bug until explained
//
// Usage: node compare.mjs --ts6 ts6.json --ts7 ts7.json --root <dir> --repo <id> --out report.md
import { readFileSync, writeFileSync } from "node:fs";
import { basename, isAbsolute, relative } from "node:path";
import { pathToFileURL } from "node:url";

export function normalizeType(s) {
  if (s === undefined) return "";
  const t = s
    // TypeScript 6 prints the import attributes of a module type it reached
    // from a file in another resolution mode.
    .replace(/(import\("[^"]*"), \{ with: \{ "resolution-mode": "(import|require)" \} \}\)/g, "$1)")
    .replace(/import\("[^"]*"\)\./g, "")
    // A truncated union shows its first and last members, which depend on
    // type IDs: keep only how many members it has.
    .replace(
      /[^|<>(){}[\],;]+ \| \.\.\. (\d+) more \.\.\. \| [^|<>(){}[\],;]+/g,
      (_, n) => ` <union of ${Number(n) + 2}> `,
    )
    .replace(/\s+/g, " ")
    .trim();
  return canonical(t).replace(/\s+/g, " ");
}

// canonical sorts what the two compilers may print in different orders,
// because their type IDs differ: union and intersection members and the
// members of object literal types, at every nesting level.
const OPEN = { "(": ")", "[": "]", "{": "}", "<": ">" };

// splitTop splits s on sep where no bracket is open.
function splitTop(s, sep) {
  const parts = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c === '"' || c === "'" || c === "`") {
      i = skipString(s, i);
      continue;
    }
    if (c in OPEN) depth++;
    else if (")]}>".includes(c) && !(c === ">" && s[i - 1] === "=")) depth--;
    else if (depth === 0 && s.startsWith(sep, i)) {
      parts.push(s.slice(start, i));
      start = i + sep.length;
      i += sep.length - 1;
    }
  }
  parts.push(s.slice(start));
  return parts.map((p) => p.trim());
}

// skipString returns the index of the quote that closes the string literal
// starting at i, so brackets inside literals ("<=") don't count.
function skipString(s, i) {
  const q = s[i];
  for (let j = i + 1; j < s.length; j++) {
    if (s[j] === "\\") j++;
    else if (s[j] === q) return j;
  }
  return s.length;
}

function canonical(t) {
  // A function type's return type extends to the end: (a) => A | B.
  const arrow = splitTop(t, " => ");
  if (arrow.length > 1)
    return `${canonical(arrow[0])} => ${canonical(arrow.slice(1).join(" => "))}`;
  for (const op of [" | ", " & "]) {
    const members = splitTop(t, op);
    if (members.length > 1) return members.map(canonical).toSorted().join(op);
  }
  // Canonicalise every bracket group inside the primary type.
  let out = "";
  for (let i = 0; i < t.length; i++) {
    const c = t[i];
    if (c === '"' || c === "'" || c === "`") {
      const end = skipString(t, i);
      out += t.slice(i, end + 1);
      i = end;
      continue;
    }
    if (!(c in OPEN)) {
      out += c;
      continue;
    }
    let depth = 0;
    let j = i;
    for (; j < t.length; j++) {
      if (t[j] === '"' || t[j] === "'" || t[j] === "`") {
        j = skipString(t, j);
        continue;
      }
      if (t[j] in OPEN) depth++;
      else if (")]}>".includes(t[j]) && !(t[j] === ">" && t[j - 1] === "=")) depth--;
      if (depth === 0) break;
    }
    const inner = t.slice(i + 1, j);
    out += c + group(c, inner) + (OPEN[c] ?? "");
    i = j;
  }
  return out;
}

function group(open, inner) {
  const member = (m) => {
    const parts = splitTop(m, ": ");
    return parts.length > 1
      ? `${canonical(parts[0])}: ${canonical(parts.slice(1).join(": "))}`
      : canonical(m);
  };
  if (open === "{") {
    const members = splitTop(inner, ";").filter((m) => m !== "");
    return ` ${members.map(member).toSorted().join("; ")} `;
  }
  return splitTop(inner, ",").map(member).join(", ");
}

export function declKey(d) {
  if (!d) return "";
  if (d.lib) return `lib:${d.file}`;
  return `${d.file}:${d.line}`;
}

// ts7Decl turns tsbridge's first declaration into the oracle's shape.
export function ts7Decl(a, root) {
  const d = a.declarations?.[0];
  if (!d) return undefined;
  const path = d.path.replaceAll("\\", "/");
  if (path.startsWith("bundled:///")) return { file: basename(path), line: d.line, lib: true };
  const r = isAbsolute(path) ? relative(root, path).replaceAll("\\", "/") : path;
  return { file: r.startsWith("..") ? path : r, line: d.line, lib: false };
}

const test = (re, v) => re === undefined || new RegExp(re).test(v ?? "");

// matchesExpected applies one rule from expected.json. Every field a rule
// sets must match: regexes over each side's type, symbol, declaration and
// file.
export function matchesExpected(e, ts6, ts7, d6, d7) {
  return (
    test(e.file, ts6.file) &&
    test(e.ts6, ts6.type) &&
    test(e.ts7, ts7.type) &&
    test(e.ts6Symbol, ts6.symbol) &&
    test(e.ts7Symbol, ts7.symbol) &&
    test(e.ts6Declaration, d6) &&
    test(e.ts7Declaration, d7)
  );
}

// truncated reports a type string the compiler cut short ("...;" or "...").
const truncated = (t = "") => t.endsWith("...") || t.endsWith("...; }");

export function classify(ts6, ts7, root, expected) {
  if (ts6.error || ts7.error) return { kind: "unanswered", why: ts6.error || ts7.error };
  const d6 = declKey(ts6.declaration);
  const d7 = declKey(ts7Decl(ts7, root));
  const sameSymbol = (ts6.symbol ?? "") === (ts7.symbol ?? "");
  // Declarations compare as sets: a union property's order follows type IDs.
  const set6 = (ts6.declarations ?? (ts6.declaration ? [ts6.declaration] : []))
    .map(declKey)
    .toSorted()
    .join(",");
  const set7 = (ts7.declarations ?? [])
    .map((d) => declKey(ts7Decl({ declarations: [d] }, root)))
    .toSorted()
    .join(",");
  const sameDecl = d6 === d7 || (set6 !== "" && set6 === set7);
  if (ts6.type === ts7.type && sameSymbol && d6 === d7) return { kind: "match" };
  for (const e of expected) {
    if (matchesExpected(e, ts6, ts7, d6, d7)) return { kind: "expected", why: e.id };
  }
  if (normalizeType(ts6.type) === normalizeType(ts7.type) && sameSymbol && sameDecl)
    return { kind: "cosmetic" };
  // Both printed types were cut short, at different members because member
  // order follows type IDs; the same symbol and declaration say they're the same.
  if (truncated(ts6.type) && truncated(ts7.type) && sameSymbol && sameDecl && ts6.symbol)
    return { kind: "cosmetic", why: "truncated" };
  const why = [];
  if (normalizeType(ts6.type) !== normalizeType(ts7.type)) why.push("type");
  if (!sameSymbol) why.push("symbol");
  if (!sameDecl) why.push("declaration");
  return { kind: "real", why: why.join("+") };
}

function main() {
  const args = Object.fromEntries(
    process.argv
      .slice(2)
      .reduce(
        (acc, a, i, all) => (a.startsWith("--") ? [...acc, [a.slice(2), all[i + 1]]] : acc),
        [],
      ),
  );
  const root = args.root.replaceAll("\\", "/");
  const ts6 = JSON.parse(readFileSync(args.ts6, "utf8")).filter((r) => !r.error);
  const ts7 = JSON.parse(readFileSync(args.ts7, "utf8"));
  const expected = JSON.parse(readFileSync(new URL("expected.json", import.meta.url), "utf8"));
  if (ts6.length !== ts7.length)
    throw new Error(`ts6 has ${ts6.length} answers, ts7 ${ts7.length}`);

  const rows = ts6.map((a, i) => ({ ts6: a, ts7: ts7[i], ...classify(a, ts7[i], root, expected) }));
  const count = (k) => rows.filter((r) => r.kind === k).length;
  const answered = rows.length - count("unanswered");
  const realMatch = answered === 0 ? 0 : (answered - count("real")) / answered;
  const summary = {
    repo: args.repo,
    positions: rows.length,
    answered,
    match: count("match"),
    cosmetic: count("cosmetic"),
    expected: count("expected"),
    real: count("real"),
    unanswered: count("unanswered"),
    realMatchRate: realMatch,
  };

  const lines = [
    `# Type oracle: ${args.repo}`,
    "",
    `TypeScript 6 (oracle) vs tsbridge (TypeScript 7) on ${rows.length} sampled positions.`,
    "",
    "| Positions | Match | Cosmetic | Expected | Real | Unanswered | Real-match rate |",
    "| ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    `| ${rows.length} | ${summary.match} | ${summary.cosmetic} | ${summary.expected} | ${summary.real} | ${summary.unanswered} | ${(realMatch * 100).toFixed(1)}% |`,
    "",
  ];
  const section = (kind, title) => {
    const rs = rows.filter((r) => r.kind === kind);
    if (rs.length === 0) return;
    lines.push(
      `## ${title}`,
      "",
      "| Position | Kind | TS 6 | tsbridge | Why |",
      "| --- | --- | --- | --- | --- |",
    );
    for (const r of rs) {
      const cell = (a, d) =>
        `\`${(a.type ?? "").replaceAll("|", "\\|").slice(0, 80)}\` ${a.symbol ?? ""} ${d ? declKey(d) : ""}`;
      lines.push(
        `| ${r.ts6.file}:${r.ts6.line}:${r.ts6.column} | ${r.ts6.kind} | ${cell(r.ts6, r.ts6.declaration)} | ${cell(r.ts7, ts7Decl(r.ts7, root))} | ${r.why ?? ""} |`,
      );
    }
    lines.push("");
  };
  section("real", "Real mismatches");
  section("expected", "Expected TypeScript 6 vs 7 differences");
  section("cosmetic", "Cosmetic differences");
  section("unanswered", "Unanswered");
  writeFileSync(args.out, lines.join("\n"));
  writeFileSync(args.out.replace(/\.md$/, ".json"), JSON.stringify(summary, null, 2) + "\n");
  console.log(
    `${args.repo}: ${summary.match} match, ${summary.cosmetic} cosmetic, ${summary.expected} expected, ${summary.real} real of ${answered}: ${(realMatch * 100).toFixed(1)}%`,
  );
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main();
