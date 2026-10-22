import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import { classify, normalizeType } from "./compare.mjs";

const expected = JSON.parse(readFileSync(new URL("expected.json", import.meta.url), "utf8"));
const root = "/repo";

test("normalizeType ignores printing order and qualifiers", () => {
  const same = [
    ["{ a: string; b: number; }", "{ b: number; a: string; }"],
    ["A | B | (C & D)", "B | (D & C) | A"],
    ["{ (x: A | B): void; }", "{ (x: B | A): void; }"],
    ["(a: string) => void | X", "(a: string) => X | void"],
    ['import("/x/y").Foo', "Foo"],
    ['Omit<P, "ref" | ... 5 more ... | "srcSet">', 'Omit<P, "alt" | ... 5 more ... | "width">'],
  ];
  for (const [a, b] of same) assert.equal(normalizeType(a), normalizeType(b), `${a} vs ${b}`);
  assert.notEqual(normalizeType("string"), normalizeType("number"));
  assert.notEqual(normalizeType("(a: string) => void"), normalizeType("(a: number) => void"));
});

test("classify", () => {
  const ts6 = { file: "a.ts", type: "string", symbol: "x", declaration: { file: "a.ts", line: 1 } };
  const ts7 = { type: "string", symbol: "x", declarations: [{ path: "/repo/a.ts", line: 1 }] };
  assert.equal(classify(ts6, ts7, root, expected).kind, "match");
  assert.equal(
    classify({ ...ts6, type: "A | B" }, { ...ts7, type: "B | A" }, root, expected).kind,
    "cosmetic",
  );
  assert.equal(classify(ts6, { ...ts7, type: "number" }, root, expected).kind, "real");
  assert.equal(
    classify(ts6, { ...ts7, declarations: [{ path: "/repo/b.ts", line: 1 }] }, root, expected).why,
    "declaration",
  );
  assert.equal(classify({ ...ts6, error: "x" }, ts7, root, expected).kind, "unanswered");
  // Lib declarations compare by file name only: line numbers differ between versions.
  const lib6 = { ...ts6, declaration: { file: "lib.es5.d.ts", line: 10, lib: true } };
  const lib7 = { ...ts7, declarations: [{ path: "bundled:///libs/lib.es5.d.ts", line: 99 }] };
  assert.equal(classify(lib6, lib7, root, expected).kind, "match");
});

test("expected TypeScript 6 vs 7 differences", () => {
  const ctor6 = {
    file: "a.js",
    type: "typeof Dao",
    symbol: "Dao",
    declaration: { file: "a.js", line: 1 },
  };
  const ctor7 = {
    type: "(db: any) => void",
    symbol: "Dao",
    declarations: [{ path: "/repo/a.js", line: 1 }],
  };
  assert.deepEqual(classify(ctor6, ctor7, root, expected), {
    kind: "expected",
    why: "js-constructor-functions",
  });
  // The same type change in a TypeScript file is a real mismatch.
  assert.equal(classify({ ...ctor6, file: "a.ts" }, ctor7, root, expected).kind, "real");
});
