// Samples positions in a repo and answers them with TypeScript 6's JavaScript
// API: the oracle side of the comparison. It loads each project the way
// tsbridge does (same root files, same option fixes) and, for N random
// identifiers and call expressions (seeded, reproducible), records the type
// string, symbol name and declaration file and line.
//
// Usage: node sample.mjs --projects projects.json --root <dir> --n 500 --seed 1
//          --out ts6.json --positions positions.json
// projects.json is the output of `patchd debug project <dir> --no-load --format json`.
import { readFileSync, writeFileSync } from "node:fs";
import { basename, isAbsolute, join, relative } from "node:path";
import ts6 from "@typescript/typescript6";

const ts = ts6.default ?? ts6;

const args = Object.fromEntries(
  process.argv
    .slice(2)
    .reduce(
      (acc, a, i, all) => (a.startsWith("--") ? [...acc, [a.slice(2), all[i + 1]]] : acc),
      [],
    ),
);
const root = slash(args.root);
const n = Number(args.n ?? 500);
const workspace = JSON.parse(readFileSync(args.projects, "utf8"));
const random = mulberry32(Number(args.seed ?? 1));

// The synthesised config tsbridge uses for files no config covers.
const synthesisedOptions = {
  allowJs: true,
  checkJs: false,
  strict: false,
  target: ts.ScriptTarget.ES2024 ?? ts.ScriptTarget.ESNext,
  module: ts.ModuleKind.NodeNext,
  moduleResolution: ts.ModuleResolutionKind.NodeNext,
  jsx: ts.JsxEmit.Preserve,
  skipLibCheck: true,
  noEmit: true,
  maxNodeModuleJsDepth: 0,
};

// Pick positions: a random root file, then a random candidate node in it.
const projects = workspace.projects.filter((p) => p.files.length > 0);
const allFiles = projects.flatMap((p) => p.files.map((f) => ({ project: p, file: abs(f) })));
if (allFiles.length === 0) throw new Error("no files to sample");

const picks = new Map(); // project name -> [{file, slot}]
for (let i = 0; i < n; i++) {
  const { project, file } = allFiles[Math.floor(random() * allFiles.length)];
  if (!picks.has(project.name)) picks.set(project.name, []);
  picks.get(project.name).push({ file, slot: random(), order: i });
}

const results = [];
for (const project of projects) {
  const wanted = picks.get(project.name);
  if (!wanted) continue;
  const program = createProgram(project);
  const checker = program.getTypeChecker();
  for (const { file, slot, order } of wanted) {
    const sf =
      program.getSourceFile(file) ??
      program.getSourceFiles().find((s) => sameFile(s.fileName, file));
    if (!sf) {
      results.push({ order, file: rel(file), error: "file not in the TypeScript 6 program" });
      continue;
    }
    const candidates = [];
    const visit = (node) => {
      if (ts.isIdentifier(node)) candidates.push({ node, pos: node.getStart(sf) });
      // A call is queried at its argument list's "(": the innermost node there is the call itself.
      if (ts.isCallExpression(node)) {
        const open = node.arguments.pos - 1;
        if (sf.text[open] === "(") candidates.push({ node, pos: open });
      }
      ts.forEachChild(node, visit);
    };
    visit(sf);
    if (candidates.length === 0) {
      results.push({ order, file: rel(file), error: "no candidates" });
      continue;
    }
    const { pos } = candidates[Math.floor(slot * candidates.length)];
    results.push({ order, ...answer(sf, checker, pos) });
  }
}
results.sort((a, b) => a.order - b.order);
for (const r of results) delete r.order;

writeFileSync(args.out, JSON.stringify(results, null, 2) + "\n");
writeFileSync(
  args.positions,
  JSON.stringify(
    results
      .filter((r) => !r.error)
      .map(({ file, line, column }) => ({ file: join(root, file), line, column })),
    null,
    2,
  ) + "\n",
);
console.log(
  `sampled ${results.filter((r) => !r.error).length} positions in ${picks.size} project(s)`,
);

function answer(sf, checker, pos) {
  const node = innermost(sf, pos);
  const lc = sf.getLineAndCharacterOfPosition(pos);
  const out = {
    file: rel(sf.fileName),
    line: lc.line + 1,
    column: lc.character + 1,
    kind: ts.SyntaxKind[node.kind],
  };
  try {
    out.type = checker.typeToString(checker.getTypeAtLocation(node));
    let sym = checker.getSymbolAtLocation(node);
    if (!sym && ts.isCallExpression(node)) {
      // tsbridge's SymbolOf on a call is what the call resolved to: the
      // chosen signature's declaration.
      const decl = checker.getResolvedSignature(node)?.declaration;
      sym =
        decl && !ts.isJSDocSignature?.(decl)
          ? (decl.symbol ?? checker.getSymbolAtLocation(decl.name))
          : undefined;
    }
    if (sym) {
      out.symbol = sym.name;
      if (sym.flags & ts.SymbolFlags.Alias) sym = checker.getAliasedSymbol(sym);
      const decl = sym.declarations?.[0];
      if (decl) {
        const dsf = decl.getSourceFile();
        out.declaration = {
          file: declFile(dsf.fileName),
          line: dsf.getLineAndCharacterOfPosition(decl.getStart(dsf)).line + 1,
          lib: isLib(dsf.fileName),
        };
        // All of them: for a union's property, the order follows type IDs.
        out.declarations = sym.declarations.map((d) => {
          const f = d.getSourceFile();
          return {
            file: declFile(f.fileName),
            line: f.getLineAndCharacterOfPosition(d.getStart(f)).line + 1,
            lib: isLib(f.fileName),
          };
        });
      }
    }
  } catch (e) {
    out.error = `TypeScript 6 threw: ${e.message}`;
  }
  return out;
}

// innermost mirrors tsbridge's NodeAt: descend into the last child whose
// range (without leading trivia) contains pos.
function innermost(sf, pos) {
  let cur = sf;
  for (;;) {
    let next;
    ts.forEachChild(cur, (c) => {
      if (c.pos <= pos && pos < c.end && c.getStart(sf) <= pos) next = c;
    });
    if (!next) return cur;
    cur = next;
  }
}

function createProgram(project) {
  let references;
  let options;
  let rootNames;
  if (project.synthesised) {
    // tsbridge's synthesised config lives (in memory) in the project's
    // directory, which is where @types are looked up from.
    const dir = project.name.slice("synthesised:".length);
    options = {
      ...synthesisedOptions,
      configFilePath: abs(join(dir, "tsconfig.patchd-synthesised.json")),
    };
    rootNames = project.files.map(abs);
  } else {
    const configPath = abs(project.config);
    const parsed = ts.getParsedCommandLineOfConfigFile(
      configPath,
      {},
      {
        ...ts.sys,
        onUnRecoverableConfigFileDiagnostic: () => {},
      },
    );
    if (!parsed) throw new Error(`TypeScript 6 cannot parse ${configPath}`);
    options = parsed.options;
    references = parsed.projectReferences;
    const owned = new Set(project.files.map(abs).map(norm));
    rootNames = parsed.fileNames.filter((f) => owned.has(norm(f)) || /\.d\.[mc]?ts$/.test(f));
    for (const f of project.files.map(abs))
      if (!rootNames.some((r) => sameFile(r, f))) rootNames.push(f);
  }
  // The same fixes tsbridge applies (see engine/tsbridge/load.go and config.go).
  if (options.types === undefined) options.types = ["*"];
  options.noEmit = true;
  options.skipLibCheck = true;
  options.ignoreDeprecations = "6.0";
  options.traceResolution = false;
  options.extendedDiagnostics = false;
  // Like tsbridge, imports into referenced projects use their source, not
  // their (usually unbuilt) outputs. createProgram reads this from the host.
  const host = ts.createCompilerHost(options);
  host.useSourceOfProjectReferenceRedirect = () => true;
  return ts.createProgram({ rootNames, options, host, projectReferences: references });
}

function abs(p) {
  return slash(isAbsolute(p) ? p : join(root, p));
}
function rel(p) {
  return slash(relative(root, p));
}
function declFile(p) {
  if (isLib(p)) return basename(p);
  const r = rel(p);
  return r.startsWith("..") ? slash(p) : r;
}
function isLib(p) {
  return /[\\/]lib\.[^\\/]*\.d\.ts$/.test(p) && /typescript6|typescript[\\/]lib/.test(p);
}
function slash(p) {
  return p.replaceAll("\\", "/");
}
function norm(p) {
  return slash(p).toLowerCase();
}
function sameFile(a, b) {
  return norm(a) === norm(b);
}
function mulberry32(seed) {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
