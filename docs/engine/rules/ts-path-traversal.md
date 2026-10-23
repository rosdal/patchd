# ts/path-traversal: path traversal

**CWE-22 · high · off by default in v0.4** (enable with `patchd scan --rules ts/path-traversal`)

Reports user input used as a file system path in the same function: the path arguments of `fs` and `fs/promises` (`readFile`, `writeFile`, `createReadStream`, `unlink`, `stat`, `readdir`, `rm`, `rename`, `copyFile`, … and their `Sync` forms) and Express's `res.sendFile` and `res.download`.

```ts
app.get("/files/:name", (req, res) => {
  res.sendFile(path.join(UPLOADS, req.params.name)); // ts/path-traversal
});
```

With `../` segments or an absolute path the request reads, overwrites or deletes files outside the directory the code meant to serve. `path.join` and `path.resolve` don't prevent it.

## Why it is off by default

v0.4 ships a rule on by default only at 80% true positives or more on real code. On the reference repos this rule found no real issue and three false positives (a vscode dev server joining `new URL(req.url).pathname`, which the URL parser has already normalised); on Juice Shop it found the three documented issues in scope and six false positives, all validation that needs path sensitivity. The triage is in [eval/triage/v0.4.yaml](../../../eval/triage/v0.4.yaml). It turns on by default once v0.5's path sensitivity removes the branch-validation cases.

## Fix

Reduce the input to a file name, or resolve it and check that it stays inside the base directory:

```ts
res.sendFile(path.join(UPLOADS, path.basename(req.params.name)));

const file = path.resolve(UPLOADS, req.params.name);
if (!file.startsWith(UPLOADS + path.sep)) return res.sendStatus(403);
res.sendFile(file);
```

## What is not reported

- A value that passed through `path.basename`.
- Input in arguments that aren't paths: the data of `writeFile`, the options of `sendFile`.
- Numbers: `` `/data/${Number(req.params.id)}.json` ``.
- A path picked from a table by input (`SERVED[req.path]`), or returned by a local helper.

## Known false positives

- `startsWith(base)` after `path.resolve`, followed by a `return` (the second fix above): reported until v0.5.
- Allowlist checks in a branch or ternary (`EXTS.includes(ext) ? ext : "jpg"`).
- Validation by lookup (`if (!known.has(key)) return 404`), common in Juice Shop's code-challenge routes.
- `new URL(req.url).pathname` joined onto a root: normalised by the URL parser, still reported.

## Known misses

- The path reaches the file system in a function the handler calls (Juice Shop's `servePublicFiles` → `verify`, v0.5).
- `res.render` with a user-controlled view or layout (Juice Shop's Local File Read, v0.6), and file libraries other than `fs`.
