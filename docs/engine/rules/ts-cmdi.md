# ts/cmdi: command injection

**CWE-78 · critical · on by default**

Reports user input that reaches a shell command in the same function: `child_process.exec` and `execSync` (argument 0), and `spawn`, `spawnSync`, `execFile` and `execFileSync` (the command, and the argument array only with `shell: true`).

```ts
app.get("/ping", (req, res) => {
  exec(`ping -c 1 ${req.query.host}`); // ts/cmdi
});
```

`exec` runs its argument through `/bin/sh` (or `cmd.exe`), so `;`, `|`, `&&`, `$( )` and backticks in the input run commands of the attacker's choosing with the server's privileges.

## Fix

Run the program directly with an argument array, so no shell parses the input, and validate the arguments:

```ts
execFile("ping", ["-c", "1", String(req.query.host)]);
```

Reject values that start with `-`, which the program would read as options, and prefer an allowlist (`if (!/^[a-z0-9.-]+$/i.test(host)) return res.sendStatus(400)`).

## What is not reported

- `spawn`/`execFile` arguments without `shell: true` (`spawn("git", ["log", ref])`), and options the rule can't read (an options object that isn't a literal).
- Numbers and booleans: `exec("seq " + Number(req.query.n))`.
- A command chosen from a table by input: `COMMANDS[req.query.op]`.
- `process.argv` and `process.env`, unless `--include-environment` is passed (then at low severity).

## Known false positives

- Validation in a branch: `if (!/^[a-z]+$/.test(name)) return; exec("id " + name)` is reported (needs path sensitivity, v0.5).
- One tainted field taints the whole object (field sensitivity, v0.5).

## Known misses

- The command is built in one function and run in another, or passed to a callback (v0.5).
- Aliased sinks the resolver can't name: `const run = exec; run(cmd)` or `const { exec: run } = cp`.
- Libraries other than `child_process` (`execa`, `shelljs`): models in v0.6.

## Precision at v0.4

No findings on the eight non-vulnerable reference repos or on Patchd; the one finding on DVNA is its documented command injection. See [eval/reports/v0.4-reference.md](../../../eval/reports/v0.4-reference.md).
