# patchd scan

Scans the TypeScript and JavaScript projects under a directory for injection vulnerabilities and reports them in the terminal, as SARIF, or as JSON.

```sh
patchd scan [path] [--format pretty|sarif|json] [--output file] \
  [--rules id,id] [--severity-threshold high] [--fail-on high] \
  [--jobs N] [--max-memory 8GB] [--timeout 10m] [--include-environment] [--verbose]
```

`path` defaults to the current directory. Findings go to stdout (or `--output`); progress, timings and warnings go to stderr, so `patchd scan --format json > out.json` stays valid JSON.

## Flags

| Flag                    | Default                  | Meaning                                                                                                              |
| ----------------------- | ------------------------ | -------------------------------------------------------------------------------------------------------------------- |
| `--format`              | `pretty`                 | `pretty` (grouped by file, numbered trace steps with code), `sarif` (SARIF 2.1.0) or `json` (`patchd.findings/v1`)   |
| `--output`, `-o`        | stdout                   | Write the findings to a file                                                                                         |
| `--rules`               | every rule on by default | Comma-separated rule IDs; naming a rule that is off by default runs it (`--rules ts/cmdi,ts/sqli,ts/path-traversal`) |
| `--severity-threshold`  | `low`                    | Report only findings at or above `critical`, `high`, `medium` or `low`                                               |
| `--fail-on`             | `low`                    | Exit 1 when a reported finding is at or above this severity                                                          |
| `--jobs`                | one per CPU              | Functions analysed at once                                                                                           |
| `--max-memory`          | 75% of available memory  | Memory budget for loading projects, e.g. `8GB`                                                                       |
| `--timeout`             | none                     | Stop after this long (`10m`); results so far are still written, with exit code 3                                     |
| `--include-environment` | off                      | Also treat `process.argv` and `process.env` as sources (low severity), for CLI tools                                 |
| `--verbose`, `-v`       | off                      | Per-project progress, plus fingerprints, confidence and tags in the pretty output                                    |

`patchd rules list` prints the rules, their severity, CWE and whether they are on by default. At v0.4: `ts/cmdi` and `ts/sqli` (on), `ts/path-traversal` (off: below the precision gate, see [its docs](../engine/rules/ts-path-traversal.md)).

Colours follow the terminal: they are off when stdout is not a terminal or `NO_COLOR` is set.

## Exit codes

| Code | Meaning                                                                                               |
| ---- | ----------------------------------------------------------------------------------------------------- |
| 0    | No reported finding at or above `--fail-on`                                                           |
| 1    | Findings at or above `--fail-on`                                                                      |
| 2    | Usage error: bad flag, unknown rule, missing path                                                     |
| 3    | Analysis failed (every project failed to load, or `--timeout` hit); partial results are still written |

## What it scans

Project discovery is tsbridge's ([docs/engine/tsbridge.md](../engine/tsbridge.md)): every `tsconfig.json`/`jsconfig.json`, synthesised projects for plain JavaScript, `node_modules`, build output and `.gitignore`d files skipped. `patchd.yaml` can narrow it:

```yaml
typescript:
  ignore: ["test/fixtures/**", "**/*.generated.ts"]
```

Install the repo's dependencies first: calls into packages that aren't installed can't be resolved, and the summary warns how many projects lacked `node_modules`.

## Output formats

**pretty**

```text
src/routes/ping.ts

  CRITICAL ts/cmdi Command injection  9:3
  User input from `req.query.host` (line 8) reaches `exec` (line 9) without escaping.
    1. 8:16  read `req.query.host` (HTTP query string)
       │ const host = req.query.host as string;
    2. 9:8  inserted into a template literal
       │ exec(`ping -c 1 ${host}`);
    3. 9:3  passed as argument 1 to `exec`
       │ exec(`ping -c 1 ${host}`);

1 finding (1 critical) in 412 files, 3.2s
```

**sarif:** one run whose driver lists the rules that ran (with Markdown help, CWE tags and `security-severity`), and one result per finding with its location, the trace as a code flow (`taint-source`, `taint-propagate`, `taint-sink` steps), the fingerprint in `partialFingerprints["patchdFingerprint/v1"]`, and `security-severity` so GitHub shows critical/high/medium/low. Paths are relative to the scanned directory with `uriBaseId` `%SRCROOT%`. Tests validate the output against the official SARIF 2.1.0 schema.

**json:** `{ "schema": "patchd.findings/v1", "tool", "findings", "rules", "stats", "warnings" }`. Each finding has `rule`, `severity`, `cwe`, `message`, `location`, `trace` (steps with `location`, `kind`, `note`, `snippet`), `fingerprint`, `confidence`, `tags` and `other_sources` (more sources reaching the same call). Fingerprints hash the rule, file, enclosing function, sink call and source access, not line numbers, so they survive unrelated edits.

## GitHub code scanning

Scan from the repository root so paths in the SARIF match the repository:

```yaml
jobs:
  patchd:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      security-events: write
    steps:
      - uses: actions/checkout@v5
      - run: npm ci --ignore-scripts # resolve calls into dependencies
      - run: patchd scan . --format sarif --output patchd.sarif --fail-on critical
      - uses: github/codeql-action/upload-sarif@v4
        if: always()
        with:
          sarif_file: patchd.sarif
          category: patchd
```

Or from a terminal: `gh api repos/OWNER/REPO/code-scanning/sarifs -f commit_sha=$(git rev-parse HEAD) -f ref=refs/heads/main -f sarif=$(gzip -c patchd.sarif | base64 -w0)`.

Patchd scans itself this way in its `ci` workflow (job `patchd-scan`).
