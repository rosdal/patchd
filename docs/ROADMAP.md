# Patchd Code — ROADMAP

Oct 19, 2026 · @Mark

## How to read this roadmap

Patchd goes from an empty folder (v0.0) to a paid EU launch (v1.0) in about 42 weeks, then to the complete multi-language product (v3.0) over roughly three years. Every version ends with exit criteria measured on real code, and the next version doesn't start until they're met.

&#91;embedded content: Patchd roadmap · 8 phases, 7 gates\]

- **Versions:** `v0.x` is pre-launch and TypeScript-only; `v1.x` is the launched TypeScript product; `v2.x` adds languages; `v3.0` is the complete product from the spec. Git tags follow SemVer (`v0.4.0`, `v0.4.1`…).
- **Milestones M1–M5** match the technical design's build order: M1 = v0.4, M2 = v0.6, M3 = v0.8, M4 = v0.9, M5 = v1.0.
- **Timing** assumes a small team (2–4 engineers) and is an estimate; the exit criteria, not the dates, define done.
- **Checklists** are the work. Tick them here or copy them into issues; each box should become one or a few PRs.
- The **v0.0 Development TODO** doc has the step-by-step for the very first version.

## Phase 0 · Foundation (v0.0 → v0.1)

Phase 0 turns an empty folder into a repo where Go and TypeScript code builds, tests and lints in CI with one command, and where `patchd --version` runs. No product code yet. Estimated: 1–2 weeks for one engineer.

### v0.0 · Empty folder

The starting point: no files, no `git init`, nothing installed. The detailed checklist to leave v0.0 is the separate **v0.0 Development TODO** doc.

### v0.1 · Repo foundation

**Goal:** a clean, reproducible monorepo that every later version builds on.

**Status (2026-10-20):** tagged `v0.1.0`. The repo is public, so `main` is protected by a ruleset (pull requests, required `ci` checks, linear history, signed commits, no force-push or deletion), and secret scanning, push protection and private vulnerability reporting are on. The Go tools are pinned as `go tool` directives in a separate `tools/go.mod`, which keeps their dependencies out of the main module, and the product spec is in `docs/PRODUCT_SPEC.md`. `go.mod` pins Go through its `go 1.27.0` line, because Go drops a `toolchain` line that matches it. The large spike repo was microsoft/vscode, a single large program rather than a monorepo (see ADR 0007).

**Toolchain and workspace**

- [x] Git repo initialised, `main` branch, remote on GitHub (private), branch protection on `main`.
- [x] Node.js LTS pinned in `.nvmrc` / `.node-version`; pnpm pinned through the `packageManager` field in the root `package.json`.
- [x] pnpm workspace (`pnpm-workspace.yaml`) covering `web/`, `ide/vscode/` and `tools/*`.
- [x] Root `package.json` scripts act as the task runner for the whole repo, Go included: `pnpm dev`, `pnpm build`, `pnpm test`, `pnpm lint`, `pnpm gen`, `pnpm eval`.
- [x] Go module `github.com/patchd/patchd` with the Go version pinned in `go.mod` (`toolchain` line).
- [x] Go tools pinned with `go tool` directives in `go.mod` (sqlc, goose, buf plugins, golangci-lint where supported).

**Skeleton code**

- [x] Folder layout from the technical design created with `doc.go` placeholders so every package compiles.
- [x] `cmd/patchd` with Cobra: `version`, plus stub subcommands `serve`, `worker`, `sandboxd`, `scan`, `review`, `fix`, `lsp`, `mcp` that print "not implemented".
- [x] Build info (version, commit, date) injected with `-ldflags`.
- [x] `web/` scaffolded with Vite + React + TypeScript, rendering a placeholder page.
- [x] `ide/vscode/` scaffolded with a minimal extension that activates and logs.

**Quality gates**

- [x] `golangci-lint` config with `gofumpt`, `govet`, `staticcheck`, `errcheck`, `revive` and a `depguard` rule: only `engine/tsbridge` may import `typescript-go`.
- [x] oxlint + Prettier for TypeScript; `tsc --noEmit` (or `tsgo --noEmit`) per TS package.
- [x] EditorConfig, `.gitattributes`, `.gitignore`.
- [x] Conventional Commits enforced by a commit-msg hook (lefthook) and in CI on PR titles.
- [x] Pre-commit hooks (lefthook): format, lint changed files.

**CI/CD (GitHub Actions)**

- [x] `ci.yml`: install pnpm and Go with caches → lint → unit tests → build binary → build web.
- [x] Matrix: Linux and macOS (developers' machines); Windows later.
- [x] Dependabot or Renovate for Go modules, npm packages and Actions.
- [x] OpenSSF Scorecard and secret scanning enabled on the repo.
- [x] `release.yml` skeleton with GoReleaser in snapshot mode (no publishing yet).

**Docs and governance**

- [x] `README.md` (what Patchd is, how to build), `CONTRIBUTING.md`, `SECURITY.md`, `LICENSE` (decision: AGPL-3.0 or source-available; see open questions).
- [x] `docs/adr/` with the first Architecture Decision Records: Go + typescript-go, Postgres-only, pnpm as task runner, own agent loop.
- [x] Product spec and technical design linked from the README.

**Spikes (time-boxed, 1–2 days each)**

- [x] Import `typescript-go` and type-check one small project from Go, studying how Oxc's tsgolint shims internal packages. Output: an ADR on the shim approach.
- [x] Measure memory and time to type-check 3 real repos (small, medium, large monorepo).

**Exit criteria for v0.1**

- A fresh clone + `pnpm install` + `pnpm build` + `pnpm test` succeeds on Linux and macOS.
- CI is green and required on `main`.
- `./bin/patchd version` prints version and commit.
- The typescript-go spike type-checks a real project from Go.

## Phase 1 · Engine core (v0.2 → v0.4)

Phase 1 builds the bottom of the analysis engine and ends with `patchd scan` finding real injection bugs in a deliberately vulnerable app. Estimated: weeks 2–8.

### v0.2 · tsbridge: load and type-check projects

**Goal:** Go code can load any TypeScript or JavaScript project and ask the TypeScript 7 checker about it.

**Status (2026-10-22):** built on branch `feat/tsbridge-v0.2`; tag `v0.2.0` follows the merge. The 10 reference repos in `eval/targets/reference-repos.yaml` load and type-check without panics or hangs; against TypeScript 6 on 500 sampled positions each, the real-match rate is 98.4–100% per repo. Deviations from this list: the files are `discover.go`, `load.go`, `syntax.go`, `types.go` and `resolve.go` (see [docs/engine/tsbridge.md](engine/tsbridge.md)); imports from packages that are not installed are `any`, as the checker types them, with a `TSB001` warning; benchmarks cover all 10 repos; and the debug command is checked by the type oracle on 5,000 positions instead of 50 against VS Code.

- [x] `engine/tsbridge/shim`: re-export the `typescript-go` internals we need (parser, binder, checker, program, compiler options), pinned to one commit.
- [x] `project.go`: find and load `tsconfig.json` / `jsconfig.json`, follow `extends` and project references, handle monorepos (pnpm, npm, Yarn workspaces, Turborepo, Nx layouts).
- [x] JavaScript-only projects: synthesise a default config (`allowJs`, `checkJs: false`) so plain JS repos still get types from inference and `@types`.
- [x] `ast.go`: a stable, Patchd-owned view of syntax nodes (kind, children, source range) so nothing outside `tsbridge` sees compiler types.
- [x] `types.go`: symbol of a node, type of an expression, call signature resolution, type-to-string, "is this type assignable to `X`".
- [x] `resolve.go`: resolve a call or property access to its declaration, including through re-exports, aliases and `import type`.
- [x] Handle missing `node_modules` gracefully: analysis still runs, types from unresolved packages become `unknown`, and a warning is recorded.
- [x] Parallel type-checking across projects in a monorepo with a configurable worker count.
- [x] `patchd debug types <file>:<line>:<col>` developer command printing the type at a position.
- [x] Benchmarks: type-check time and peak memory on 3 reference repos, tracked in CI.

**Exit criteria:** loads and type-checks 10 popular open-source TypeScript repos without crashing; the debug command matches what VS Code shows for 50 sampled positions.

### v0.3 · Patchd IR

**Goal:** every function in a project becomes a typed control-flow graph in SSA form.

- [ ] `ir.go`: core types: `Module`, `Func`, `Block`, `Instr`, `Value`, each value carrying a type handle and source range.
- [ ] Instruction set: `call`, `new`, `load`, `store`, `assign`, `return`, `throw`, `await`, `yield`, `phi`, `branch`, `jump`, plus `unknown` for anything not yet lowered (never crash on new syntax).
- [ ] Lowering for expressions and statements: variables, operators, conditionals, loops, `switch`, `try`/`catch`/`finally`, labelled breaks.
- [ ] Lowering for functions: declarations, arrows, methods, closures with captured variables, default parameters, rest and spread.
- [ ] Lowering for objects and classes: object literals, property access, computed keys, class fields, constructors, getters and setters, `super`, static members.
- [ ] Lowering for modern syntax: destructuring (nested, with defaults), optional chaining, nullish coalescing, template literals and tagged templates, `async`/`await`, generators.
- [ ] Module-level code and imports/exports, including default, named, namespace and re-exports.
- [ ] SSA construction with phi nodes; dominator tree.
- [ ] `print.go`: a readable text form of the IR; golden-file snapshot tests (`.ts` → `.ir.golden`) for every construct above.
- [ ] `patchd debug ir <file>` developer command.
- [ ] Coverage report: share of syntax nodes lowered to real instructions vs `unknown`, measured on the 10 reference repos.

**Exit criteria:** above 98% of syntax nodes lowered on the reference repos; all snapshot tests pass; lowering a 500k-line repo takes under a minute on a laptop.

### v0.4 · First taint analysis and `patchd scan` (milestone M1)

**Goal:** find real injection vulnerabilities inside single functions and report them in SARIF.

- [ ] `dataflow/`: lattice, worklist solver, reaching definitions over the IR.
- [ ] `taint/spec.go`: source, sink, sanitizer and propagator types; taint kinds (sql, command, path, url, html, code).
- [ ] Intraprocedural taint: propagation through assignments, string concatenation, template literals, array and object operations, common string methods.
- [ ] Hard-coded models (moved to YAML in v0.6): Express `req.*` as sources; `child_process.exec`, `fs.*` paths, `pg` `query` with a string argument as sinks.
- [ ] First rules: command injection, path traversal, SQL injection (`pg`).
- [ ] `taint/trace.go`: every finding carries the step-by-step path with source ranges.
- [ ] Finding fingerprints that stay stable when unrelated lines move.
- [ ] `patchd scan <path>`: outputs SARIF 2.1.0 (`--format sarif`), a readable terminal report (`--format pretty`), and JSON.
- [ ] Fixture harness: `.ts` files with `// expect:` and `// expect-none` comments, run by `go test ./engine/...`.
- [ ] Integration targets pinned: OWASP Juice Shop, OWASP NodeGoat, DVNA; expected findings recorded.

**Exit criteria:** finds the known command-injection, path-traversal and SQL-injection issues in NodeGoat and DVNA that live inside one function; zero findings on a set of 20 safe fixtures; SARIF uploads cleanly to GitHub code scanning.

## Phase 2 · Engine depth (v0.5 → v0.6)

Phase 2 makes the engine follow data across functions and files, adds the launch frameworks and rules, and measures precision and recall on real historical vulnerabilities. Estimated: weeks 9–16.

### v0.5 · Interprocedural analysis

**Goal:** taint flows across function calls, modules and callbacks, with traces that cross files.

- [ ] `callgraph/`: resolve direct calls, method calls by receiver type, interface and abstract dispatch, overloads, and dynamic imports where the specifier is a literal.
- [ ] `pointsto.go`: lightweight points-to for function values: callbacks, higher-order functions (`map`, `forEach`, `then`), event handlers, middleware arrays.
- [ ] Function summaries: for each function, which parameters flow to the return value, to `this`, to properties, and to sinks; computed bottom-up, iterating on cycles (recursion) to a fixed point.
- [ ] Field sensitivity: track taint per property path (`req.body.user.name`), with a depth limit.
- [ ] Context sensitivity: one level of call-site sensitivity by default; configurable.
- [ ] Promises and `async`: taint through `await`, `.then`, `Promise.all`.
- [ ] Cross-file traces rendered as numbered steps with file and line.
- [ ] Performance budget: whole-program analysis of a 500k-line repo under 10 minutes and 8 GB RAM.
- [ ] Analysis timeouts per function and per project, with partial results instead of failure.

**Exit criteria:** finds the cross-function injection bugs in Juice Shop, NodeGoat and DVNA that v0.4 missed; fixture suite extended to 300+ cases, all passing.

### v0.6 · Framework models, launch rule set, cache, gold set (milestone M2)

**Goal:** production-grade TypeScript security analysis, measured.

**Framework models (YAML + Go hooks)**

- [ ] `models/loader.go`: load and validate model files against a schema; hot-reload in development.
- [ ] Move the v0.4 hard-coded models into `node-core.yaml`, `express.yaml`, `pg.yaml`.
- [ ] Web frameworks: Fastify, Koa, Hono, NestJS (decorators: `@Body`, `@Query`, `@Param`), Next.js App Router (route handlers, server actions, `searchParams`) and Pages Router (`getServerSideProps`, API routes).
- [ ] Data libraries: `mysql2`, Prisma (`$queryRawUnsafe`, `$executeRawUnsafe`), Knex (`raw`), TypeORM (`query`), Sequelize (`query`), Drizzle (`sql.raw`), Mongoose and MongoDB driver (`$where`, operator injection).
- [ ] HTTP clients as SSRF sinks: `fetch`, `axios`, `got`, `undici`, `node:http(s)`.
- [ ] Rendering: React `dangerouslySetInnerHTML`, `res.send` with HTML, template engines (EJS, Pug, Handlebars) in unescaped mode.
- [ ] Sanitizers and validators: zod, yup, joi, valibot, validator.js, DOMPurify, `path.resolve` + prefix checks, URL allowlist comparisons.
- [ ] `docs/writing-models.md` so anyone can add a framework.

**Launch rule set**

- [ ] Injection: SQL, NoSQL, command, code (`eval`, `Function`, `vm`), template.
- [ ] Path traversal, SSRF, open redirect, XSS.
- [ ] Prototype pollution (recursive merge, `__proto__` keys from user input).
- [ ] ReDoS on user-controlled input against vulnerable regex patterns.
- [ ] JWT misuse: `jsonwebtoken.verify` without algorithms, `decode` used as verify, `none` algorithm.
- [ ] Weak crypto: MD5/SHA-1 for passwords, `Math.random` for tokens, hard-coded keys and IVs.
- [ ] Insecure deserialization (`node-serialize`, YAML unsafe load).
- [ ] Correctness: floating promises, null and undefined dereference, unreachable code, `await` in loops where clearly unintended.
- [ ] Each rule has: CWE, default severity, description, fix guidance, 10+ positive and 10+ negative fixtures.

**Cache and incremental analysis**

- [ ] `cache/`: content hash per file + engine version → IR and summaries, stored on local disk with an object-storage backend interface.
- [ ] Incremental mode: given a diff, re-lower changed files and recompute summaries for changed functions and their callers until summaries stop changing.
- [ ] `patchd scan --diff <base>..<head>` reports only findings introduced by the change.

**Security gold set v1**

- [ ] `eval/goldsets/security-npm`: 150+ historical npm-ecosystem vulnerabilities from GitHub Security Advisories / OSV with a known fixing commit, stored as manifests (repo, vulnerable commit, fixed commit, CWE, location).
- [ ] `eval/harness`: clone, scan both commits, score: found on vulnerable, silent on fixed.
- [ ] Nightly CI job publishing precision, recall and runtime per rule.

**Metrics**

- [ ] Cognitive complexity and duplication (token hashing) per function and file, reported in `patchd scan --metrics`.

**Exit criteria (M2):** recall and precision measured and published internally per rule; every launch rule at ≥ 80% precision on the gold set; incremental re-analysis of a typical PR under 30 seconds with a warm cache.

## Phase 3 · Review product (v0.7 → v0.8)

Phase 3 wraps the engine in a GitHub App that reviews pull requests, adds the AI agent layer and codebase map, and puts the trust layer in front of developers. Estimated: weeks 17–26.

### v0.7 · GitHub App and review pipeline

**Goal:** opening a PR on a connected repo produces a Patchd summary comment and engine findings on changed lines.

**Backend foundation**

- [ ] Postgres schema v1 (`store/migrations`): orgs, users, installations, repos, snapshots, pull requests, reviews, findings, evidence, settings.
- [ ] `sqlc` queries and generated code; `goose` migrations run on startup.
- [ ] Row-level security policies keyed by `org_id`.
- [ ] River job queue: `review`, `scan`, `index` jobs with retries, timeouts and deduplication per PR head SHA.
- [ ] `patchd serve`: chi router, health and readiness endpoints, webhook endpoint, Connect API stub.
- [ ] `patchd worker`: pulls jobs, runs the pipeline, reports progress.
- [ ] Config via env vars + optional file; secrets never logged.
- [ ] OpenTelemetry tracing per review; Prometheus metrics (review duration, queue depth, errors).

**GitHub integration**

- [ ] GitHub App manifest: permissions (contents read, pull requests write, checks write, metadata read), events (pull\_request, push, issue\_comment, installation).
- [ ] Webhook signature verification; installation token minting and caching.
- [ ] Clone at PR head with a shallow fetch of the base; reuse a per-repo bare cache.
- [ ] Post a summary comment (updated in place on new pushes), inline review comments, and a `patchd/gate` check run.
- [ ] Rate-limit and secondary-limit handling with backoff.

**Pipeline**

- [ ] `review/semdiff.go`: map changed hunks to changed functions, classes and exports.
- [ ] `review/triage.go`: risk score (touched auth, payments, migrations, public API, size) and review depth; AI-author detection (co-author trailers, branch prefixes, bot accounts).
- [ ] Incremental re-review: only new commits since the last reviewed SHA; resolve findings that disappeared.
- [ ] Quality gate v1: fail on new critical or high findings; configurable in `patchd.yaml`.
- [ ] `patchd.yaml` v1 parsing and schema validation, with errors posted as a PR comment.
- [ ] `patchd review` CLI: run the same pipeline locally on a branch, no GitHub needed.

**Exit criteria:** Patchd reviews its own PRs; summary and inline comments appear within 3 minutes for a typical PR; re-reviews on push work; the gate blocks a PR that adds a seeded SQL injection.

### v0.8 · Agents, codebase map, trust layer (milestone M3)

**Goal:** Patchd finds bugs rules can't, explains them, and only posts what it can back with evidence.

**LLM layer**

- [ ] `llm/provider.go` interface; Anthropic, OpenAI (and OpenAI-compatible) and Google implementations.
- [ ] `llm/router.go`: task class → model mapping from config; EU-region endpoints by default.
- [ ] Prompt caching support; token and cost accounting per review stored in Postgres.
- [ ] Retries, timeouts, fallbacks to a second provider.

**Codebase map**

- [ ] `kb/graph.go`: symbols and edges (calls, references, imports, tests-to-code) from the engine into Postgres, per snapshot, incremental.
- [ ] `kb/summarize.go`: natural-language summaries per function, embedded with pgvector.
- [ ] `kb/knowledge.go`: per-repo knowledge base (architecture overview, modules, conventions), regenerated on merges to the default branch.
- [ ] Convention ingestion: linter configs, `CLAUDE.md`, `AGENTS.md`, `.cursorrules`, `CONTRIBUTING.md`, CODEOWNERS.
- [ ] `kb/search.go`: hybrid keyword + vector + graph-expansion search.

**Agents**

- [ ] `agents/loop.go`: tool-calling loop with budgets, structured output, and full transcripts stored for debugging.
- [ ] Tools: engine queries (`taint_paths`, `callers`, `callees`, `type_of`), code read and search, knowledge base lookup, git history and blame, rules lookup.
- [ ] Orchestrator: diff + engine findings + context → list of hypotheses.
- [ ] Investigators in parallel, one per hypothesis, each ending in `confirmed` / `refuted` / `inconclusive` with evidence.
- [ ] Agent triage of engine findings: mark likely false positives (hidden, never deleted).
- [ ] Authorization-check hunter: route handlers that read or write user-owned data without an ownership or role check.
- [ ] Ticket intent check: read linked GitHub Issues / Linear tickets and flag unimplemented acceptance criteria.
- [ ] PR summary and walkthrough written by the model from the semantic diff; Mermaid sequence diagram for flows across 3+ files.

**Trust layer**

- [ ] `trust/evidence.go`: inline comments require an evidence record (trace, graph fact or rule citation; execution proof arrives in v0.9).
- [ ] `trust/dedupe.go`: merge engine, linter and agent findings by location and meaning.
- [ ] `trust/agreement.go`: cross-check high-severity findings with a second model family.
- [ ] `trust/rank.go`: score = confidence × severity × historical acceptance; comment cap (default 7).
- [ ] Feedback capture: reactions, reply commands (`@patchd wrong`, `@patchd not-important`), and whether flagged lines changed before merge.
- [ ] `trust/detectors.go`: acted-on rate per detector; quarantine below the threshold.
- [ ] `suggest` delivery mode: GitHub suggestion blocks for single-hunk patches (unverified until v0.9, labelled as such).
- [ ] `@patchd` chat in PR threads: questions about the code, "explain this finding", "don't flag this again" (creates a memory).

**Review gold set v1**

- [ ] 300+ real TypeScript PRs that later needed a bug fix or revert, as manifests; scored with three LLM judges + human spot checks.

**Exit criteria (M3):** reviewing Patchd's own repo and 3 design partners' repos; acted-on rate ≥ 50%; median 1–3 inline comments per PR; review gold set F1 measured and tracked.

## Phase 4 · Proof and patches (v0.9)

Phase 4 adds the sandbox, so security findings arrive with a reproduction test and every patch is verified before anyone sees it. This is the version that makes the name "Patchd" true. Estimated: weeks 27–34.

### v0.9 · Sandbox, reproduction tests, verified patches, delivery modes (milestone M4)

**Sandbox (`sandbox/`, `patchd sandboxd`)**

- [ ] `sandboxd` service API: create, `write_file`, `run` (with limits), `read_output`, snapshot, destroy.
- [ ] Firecracker driver: rootfs image with Node LTS, pnpm, npm, Yarn and git; jailer; per-VM CPU, memory, disk and time limits.
- [ ] gVisor (`runsc`) driver for Self-Hosted; Docker driver as the explicitly weaker fallback.
- [ ] Network: default deny; install-time egress through `netproxy` allowing only the npm registry or a configured private registry.
- [ ] No secrets inside: repo copied in by `sandboxd`; no Git or model tokens ever enter the VM.
- [ ] Warm per-repo snapshots after a successful install; invalidated on lockfile change.
- [ ] Install with `--ignore-scripts` first; enable lifecycle scripts only when tests need them.
- [ ] Security review of the sandbox by an external party before v1.0.

**Test recipes**

- [ ] `sandbox/recipe.go`: detect package manager, Node version, install command and test command from `package.json`, lockfiles and `.github/workflows`; cache the working recipe per repo.
- [ ] Support Vitest, Jest, Mocha, `node:test`, Playwright (unit-level only), and monorepo filtering (`pnpm --filter`, Nx, Turborepo).
- [ ] Graceful degradation: when tests can't run, review continues without execution evidence and says so.

**Proof of vulnerability**

- [ ] Investigator tool `sandbox.run_test`: write a minimal test that demonstrates the bug against the project's own code, run it, capture output.
- [ ] Proof rules: the test must fail (show the unsafe behaviour) on the PR head; it is a minimal test, never a weaponised exploit or anything aimed at live systems.
- [ ] `fast-check` for property-based inputs when a single example isn't enough.
- [ ] Evidence type `execution` stored with test code, command, output and exit code in object storage.
- [ ] Findings with execution proof rank above all others; security findings marked "reproduced".

**Verified patches (`patches/`)**

- [ ] Patcher agent writes a root-cause fix.
- [ ] Verification in the sandbox: apply the patch, `tsgo --noEmit` clean, proof test now passes, the existing test suite still passes, engine re-scan shows no new findings.
- [ ] One automatic rework attempt on failure; then the finding is posted without a patch.
- [ ] Variant check: after a fix, search the repo for the same pattern and fix siblings in the same patch where safe.
- [ ] The proof test is offered as a regression test alongside the patch.

**Delivery modes and settings**

- [ ] `patches/policy.go`: resolve the mode from org ceiling → repo settings → `patchd.yaml` → `patches.rules` (category, severity, path, author type, files changed).
- [ ] `deliver/suggest.go`: GitHub suggestion blocks (single hunk) or a collapsible diff (multi-hunk).
- [ ] `deliver/commit.go`: commit to the PR branch as the GitHub App (signed), `Co-authored-by` trailer, configurable message template, squash option, `max_per_pr`.
- [ ] `deliver/fixpr.go`: separate PR targeting the PR branch, or the default branch for full-repo scans.
- [ ] Commands: `@patchd apply`, `@patchd fix-pr`, `@patchd suggest`, `@patchd revert`.
- [ ] Built-in safety: never push to protected branches, never force-push, fork PRs fall back to `suggest` unless maintainer edits are allowed, failed verification downgrades `commit` to `suggest`.
- [ ] `patchd fix` CLI: apply verified patches locally.

**Exit criteria (M4):** on the security gold set, ≥ 60% of true findings arrive with a passing execution proof; ≥ 90% of shown patches pass verification on first or second attempt; all four delivery modes work end to end on a test org; external sandbox review has no open critical findings.

## Phase 5 · Launch (v1.0)

Phase 5 adds everything a paying EU customer needs around the core: the web app, team learning, supply-chain checks, CRA features, billing, the EU Cloud region and the free Self-Hosted edition. Estimated: weeks 35–42.

### v1.0 · Paid launch in the EU (milestone M5)

**Web app (`web/`)**

- [ ] Auth: sign in with GitHub (OIDC), session handling, org switcher, roles (owner, admin, member).
- [ ] Onboarding: install the GitHub App, pick repos, first indexing progress, optional backtest of the last 20 merged PRs.
- [ ] Inbox: my PRs, findings waiting on me, patches ready.
- [ ] Review view: diff grouped by intent, findings beside the code, evidence panel.
- [ ] Finding detail: taint trace as a step graph (React Flow), sandbox proof output, patch diff, feedback history.
- [ ] Repo overview: gate status, findings on new code over time, hotspots.
- [ ] Security screen: vulnerabilities by severity and status, reachability, suppressed-by-filter list.
- [ ] Rules and memories: list, edit, promote memory to rule, delete; acted-on rate per entry.
- [ ] Settings: repos, gate, patch delivery modes (org ceiling, repo defaults), model settings, notifications, data retention.
- [ ] Detector health (admin): acted-on rate per detector, quarantined detectors.
- [ ] Accessibility (WCAG 2.2 AA) and dark mode.

**Team learning**

- [ ] `trust/teamfilter.go`: embed every comment with its outcome; suppress new candidates similar to 3+ downvoted ones, boost those similar to accepted ones.
- [ ] Memories created from feedback, with their origin shown and editable.
- [ ] Quiet mode for the first week on a new repo (blocking and high-confidence only).

**Supply chain**

- [ ] Lockfile parsing for pnpm, npm and Yarn via `osv-scanner`; match against OSV.
- [ ] Reachability: use the call graph to tell whether the vulnerable function of a dependency is actually called.
- [ ] Secrets scanning on diffs and full scans (gitleaks library).
- [ ] Dependency upgrade patches through the same delivery modes.

**CRA and compliance features**

- [ ] SBOM export (CycloneDX, SPDX) per repo and per release tag.
- [ ] Vulnerability-handling log per product (found, triaged, fixed, released), exportable as audit evidence.
- [ ] "Actively exploited" flag for dependencies using known-exploited lists, with a CRA reporting reminder and a drafted report the customer submits to ENISA.
- [ ] `SECURITY.md` and coordinated disclosure policy generator.

**Billing and plans**

- [ ] Stripe Billing (EU entity): Cloud plan per active developer, EUR pricing, EU VAT, invoices.
- [ ] Active-developer counting (opened a PR in the month); usage dashboard.
- [ ] Free plan for public repos (open-source maintainers get Cloud free).
- [ ] Trial: 14 days of Cloud.

**EU Cloud region**

- [ ] Provider chosen (EU-owned; Amsterdam or Paris); OpenTofu for all infra.
- [ ] Kubernetes for `serve` and `worker`; dedicated bare-metal hosts for `sandboxd`.
- [ ] Managed Postgres with pgvector, point-in-time recovery, encrypted backups to a second EU region.
- [ ] Object storage with lifecycle rules (30-day artifact retention).
- [ ] Model access via EU-region endpoints only; zero-retention agreements signed.
- [ ] Status page, on-call rotation, incident runbooks, uptime target 99.9%.
- [ ] Penetration test of the Cloud platform by an external firm.

**Self-Hosted edition (free, BYOK)**

- [ ] Single container image running all three roles + Postgres via Docker Compose.
- [ ] BYOK model settings: provider, key, task-class mapping, spend caps per review, repo and month.
- [ ] Monthly rule and vulnerability-pattern pack releases.
- [ ] Upgrade guide and automatic database migrations.
- [ ] `docs/self-hosting.md`.

**Legal and company**

- [ ] Terms of service, privacy policy, DPA, subprocessor list (EU-only), cookie-free marketing site.
- [ ] ISO 27001 project started (certification after launch).
- [ ] Self-Hosted licence decided and applied.

**Launch**

- [ ] Marketing site (EU-first messaging: data sovereignty, CRA readiness, "find it, prove it, patch it").
- [ ] Docs site: getting started, `patchd.yaml` reference, rules reference, self-hosting.
- [ ] Public changelog.
- [ ] Launch to design partners first, then public.

**Exit criteria (v1.0):** 10+ paying EU teams; acted-on rate ≥ 70%; median cost per Cloud review under €0.50; zero open critical security issues; Self-Hosted installs from a single `docker compose up`.

## Phase 6 · Expand (v1.1 → v1.5)

Phase 6 widens where Patchd works, starts the open-source vulnerability program, deepens security analysis and adds enterprise features, all still TypeScript-only. Estimated: months 11–18 after the v0.1 start.

### v1.1 · More surfaces

- [ ] GitLab: app/integration, merge request comments, suggestions, commits, pipelines status, self-managed GitLab support.
- [ ] VS Code extension: diagnostics from `patchd lsp`, inline evidence, apply verified patch, "review my branch" command.
- [ ] MCP server (`patchd mcp`): `review_diff`, `explain_finding`, `get_conventions`, `taint_paths` for coding agents (Claude Code, Cursor, Codex).
- [ ] Pre-push hook template using `patchd review`.
- [ ] Slack and Microsoft Teams notifications for blocking findings.

**Exit:** a coding agent can call Patchd before opening a PR and fix its own findings.

### v1.2 · Patchd for Open Source pilot

- [ ] Free maintainer installs advertised; OSS onboarding flow.
- [ ] Research mode (staff only): project queue ranked by dependents, downloads and exposure; batch scanning on dedicated capacity.
- [ ] Triage console for Patchd security researchers: reproduce, confirm severity, review patch, approve report.
- [ ] Disclosure workflow engine: private report via `SECURITY.md` contact or GitHub private vulnerability reporting, follow-up reminders, 90-day deadline tracking, escalation to CERT/CC or distro security teams.
- [ ] Patch delivery into GitHub's private security-advisory forks only.
- [ ] Advisory drafting in GHSA and OSV formats (CWE, CVSS v4, affected and fixed versions); CVE requests via GitHub's CNA.
- [ ] Downstream alerts: dependents and forks carrying the same code.
- [ ] Public trust page: disclosed vulnerabilities, CVEs, time to fix, maintainer-confirmed rate.
- [ ] Legal review and published safe-harbour / research policy.
- [ ] Pilot on 20 critical TypeScript/JavaScript projects.

**Exit:** first CVEs published with maintainers; maintainer-confirmed rate ≥ 95%.

### v1.3 · Deep security

- [ ] Variant search across the ecosystem: turn every new npm CVE fix into a pattern and search customers' code (Cloud: daily) and the OSS queue.
- [ ] Cross-family model inversion for AI-authored PRs.
- [ ] Deep review tier for high-risk PRs: more investigators, longer budgets, mandatory execution proof.
- [ ] Coverage-guided fuzzing evaluation for JavaScript parsers and protocol code; ship if it finds real bugs on the gold set.
- [ ] Business-logic and authorization hunters improved with per-repo access-control models learned from the codebase.
- [ ] Security gold set v2: 500+ cases, including AI-agent-written vulnerable code.

### v1.4 · Enterprise

- [ ] SAML SSO, SCIM provisioning, fine-grained roles, audit log export.
- [ ] Helm chart for Self-Hosted on Kubernetes; air-gapped installs with local models (vLLM).
- [ ] Single-tenant Cloud option in the EU.
- [ ] Org-wide policies: gate templates, patch-mode ceilings, required rules across repos.
- [ ] ISO 27001 certification; SOC 2 Type II started.
- [ ] Self-Hosted Enterprise licence if decided (support, SLAs, daily intelligence feed).

### v1.5 · Insights and scale

- [ ] Org insights: acted-on rate, escaped bugs, review latency, security posture trend, AI-authored share of PRs.
- [ ] Weekly digest email for leads.
- [ ] Architecture rules: declare allowed module dependencies; violations as findings.
- [ ] Performance work: monorepos of 5M+ lines; distributed engine cache.
- [ ] Public, reproducible benchmark results (Martian Code Review Bench submission and our own published harness).

## Phase 7 · Complete product (v2.0 → v3.0)

Phase 7 takes the proven TypeScript engine to more languages and turns Patchd into the full product from the spec. Each new language repeats the Phase 1–2 pattern: a bridge, IR lowering, framework models, rules, a gold set. Estimated: months 18–36.

### v2.0 · Second language

- [ ] Choose the language from customer demand (likely Python: Django, Flask, FastAPI).
- [ ] Language bridge for parsing and type information; IR lowering into the shared Patchd IR.
- [ ] Framework models and launch rules for that language; test recipes for its test runners (pytest).
- [ ] Security and review gold sets for the language.
- [ ] Cross-language flows inside one repo (TypeScript front end calling a Python API) as an agent-assisted feature.
- [ ] Become a CVE Numbering Authority once OSS report volume justifies it.

### v2.1 – v2.4 · Breadth

- [ ] Go, Java/Kotlin, C#, then C/C++ and Rust (with sanitizer-backed fuzzing for memory-safety bugs).
- [ ] Bitbucket and Azure DevOps.
- [ ] JetBrains IDE plugin.
- [ ] Infrastructure as code checks (Terraform/OpenTofu, Kubernetes manifests, Dockerfiles).
- [ ] Promotion of repeatedly accepted agent findings into deterministic engine rules (LLM-drafted, engine-verified).

### v2.5 · Second region

- [ ] US region as a separate deployment with no data shared with the EU region.
- [ ] Region choice per org at sign-up.

### v3.0 · The complete product

- [ ] 8+ languages with first-class engines; one IR, one trust layer.
- [ ] Org-wide knowledge: architecture drift detection, ownership-aware routing of findings.
- [ ] Ticket-intent review at depth: specs and acceptance criteria traced to code and tests.
- [ ] Patchd for Open Source running continuously across the most critical OSS projects with a public track record of CVEs found and patched.
- [ ] Full CRA toolkit for EU manufacturers: SBOMs, vulnerability handling, disclosure, reporting drafts across all their products.
- [ ] Independent benchmark leadership on precision and acted-on rate.

## Cross-cutting tracks

These run through every phase rather than belonging to one version.

| Track | What it means in practice | Starts |
| --- | --- | --- |
| Evaluation | Fixtures from v0.4, security gold set from v0.6, review gold set from v0.8; nightly runs; no change ships if precision drops | v0.4 |
| Dogfooding | Patchd scans its own repo from v0.4 and reviews its own PRs from v0.7 | v0.4 |
| Security of Patchd itself | Threat model per release, dependency scanning, signed releases, external pen tests before v1.0 and yearly | v0.1 |
| Performance budgets | Type-check, lowering, analysis and review latency tracked in CI against reference repos | v0.2 |
| Cost | Model tokens and sandbox minutes per review tracked from the first LLM call | v0.8 |
| Docs | ADRs from v0.1; user docs from v0.7; public docs at v1.0 | v0.1 |
| Design partners | 3 by v0.8, 10 by v1.0; weekly feedback calls | v0.7 |
| EU compliance | GDPR-by-design data model, EU-only subprocessors, CRA features, ISO 27001 | v0.7 |
