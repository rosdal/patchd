# Patchd Code — Technical Design

Oct 19, 2026 · @Mark

## Decisions at a glance

Patchd is built in **Go** for the analysis engine, backend and CLI, on top of Microsoft's Go-native TypeScript compiler, with **TypeScript** only for the web app and IDE extension. Launch scope is TypeScript and JavaScript projects only, hosted in the EU.

| Area | Decision | Main reason |
| --- | --- | --- |
| Analysis engine | Own engine, written in Go, built on `typescript-go` (the TypeScript 7 compiler) | Real TypeScript types at native speed; same approach Oxc's tsgolint uses in production |
| Backend, workers, API, CLI | Go | One language with the engine; a single static binary makes Self-Hosted easy |
| Web app | TypeScript, React, Vite (a static app served by the Go API) | Best UI ecosystem; ships inside the same binary for Self-Hosted |
| IDE extension | TypeScript (VS Code first) talking to `patchd lsp` | VS Code extensions are TypeScript; the engine stays in Go |
| Supported code | TypeScript and JavaScript (Node, browser, Next.js, Express, NestJS…) | Depth on one language before breadth |
| Database and queue | Postgres only (pgvector for embeddings, River for jobs) | One dependency to run and back up, which matters most for Self-Hosted |
| Sandbox | Firecracker microVMs (Cloud), gVisor (Self-Hosted) | Strong isolation for running untrusted repos |
| Models | Provider-agnostic; EU-region endpoints by default in Cloud; BYOK in Self-Hosted | EU data residency and the free BYOK edition |
| Patch delivery | Configurable per org, repo, path and severity: off, suggest, commit, or separate fix PR | Your requirement: teams decide how much Patchd may change |
| Hosting | EU-owned provider, Amsterdam or Paris region first | EU-first launch; data stays in the EU under EU jurisdiction |

## System overview

Patchd is one Go binary running in three roles (API, worker, sandboxd) around a single Postgres database.

&#91;embedded content: System overview · 3 Patchd roles, Postgres, external services\]

- **`patchd serve`** receives webhooks, serves the Connect API and the web app, and enqueues jobs. It holds no long-running work.
- **`patchd worker`** runs the review pipeline: triage, engine, agents, trust layer, publishing. Workers scale horizontally and pick jobs from River.
- **`patchd sandboxd`** runs on bare-metal or KVM hosts and manages sandboxes. It's the only place repository code is executed.
- **Self-Hosted small install:** all three roles in one container plus Postgres, via Docker Compose. Large installs and Cloud run them separately on Kubernetes, with sandboxd on dedicated hosts.

## Implementation languages and why

Go wins because the best TypeScript type checker now exists as Go code. [TypeScript 7.0, the native compiler ported to Go, reached general availability on July 8, 2026](https://coddy.tech/docs/typescript/typescript-7), with full builds [8–12× faster than the JavaScript compiler](https://infoq.com/news/2026/08/typescript-7-released/). Taint analysis in TypeScript is only precise with real type information (which `query` method is this, what does this generic resolve to), so the engine should sit directly on that checker.

**Options considered**

| Option | Type information | Speed | Risk | Verdict |
| --- | --- | --- | --- | --- |
| Go on `typescript-go` | Full, the real TypeScript 7 checker in-process | Native, parallel | TS 7 has no stable programmatic API yet; we link internal packages through a shim, as tsgolint does | **Chosen** |
| TypeScript on the TS 6 API (`@typescript/typescript6`) | Full | Slow on large repos; single-threaded | TS 6 is the last JavaScript release and is effectively frozen | Fallback only |
| Rust on Oxc (parser + semantic) | Scopes and symbols, no type checker | Fastest parsing | Must call out to tsgo for types; two processes and two languages | Rejected for v1 |
| Wait for the TS 7.1 API | Full | Native | Microsoft targets 7.1 for a new API, [at least several months away](https://ecorpit.com/typescript-7-migration-readiness-eslint-astro-blockers-2026/) | Adopt when it ships; keep our shim thin |

**Precedent:** [Oxc's tsgolint builds TypeScript programs with typescript-go and runs type-aware rules on them](https://oxc.rs/docs/guide/usage/linter/type-aware.md), and its [stable v7 release tracks TypeScript 7.0.2](https://oxc.rs/blog/2026-07-22-type-aware-linting-stable). That proves the shim approach works in production.

**How we contain the API risk**

- All access to `typescript-go` goes through one internal package, `engine/tsbridge`. Nothing else imports compiler internals.
- `tsbridge` exposes only what we need: load a project from `tsconfig.json`, walk syntax trees, get symbols, types and signatures, resolve calls.
- We pin an exact `typescript-go` commit and upgrade deliberately, with the full engine test suite as the gate.
- When TS 7.1 ships its public API, `tsbridge` moves onto it; the rest of the engine doesn't change.

**Why not Go everywhere:** the web app and VS Code extension are TypeScript, because that's where the UI and editor ecosystems are. They talk to the Go backend over HTTP and the Language Server Protocol.

**Language split:** Go for about 85% of the code (engine, workers, API, CLI, agents), TypeScript for about 15% (web, IDE). Rule and framework models are data files (YAML) plus Go, not a third language.

## Own analysis engine for TypeScript

The engine turns a TypeScript project into a typed intermediate representation (IR), then runs dataflow and taint analysis over it; the TypeScript compiler does parsing and types, and everything after that is ours.

&#91;embedded content: Analysis engine pipeline · 8 stages, framework models and cache\]

**Patchd IR**

- One control-flow graph (CFG) per function, in SSA form (every variable assigned once), with each value carrying its TypeScript type.
- A small instruction set: `call`, `new`, `load`/`store` (property and index), `assign`, `return`, `throw`, `await`, `yield`, `phi`, plus branch and jump.
- TypeScript features lowered explicitly: `async`/`await` and promises, closures and captured variables, destructuring, spread, optional chaining, getters and setters, class fields, decorators, template literals (critical for injection), and module imports and exports.
- Every IR value keeps a pointer to its source range, so traces map back to exact lines.

**Analyses**

| Analysis | What it does | Approach |
| --- | --- | --- |
| Call graph | Which functions each call can reach | Type-based resolution from the checker (receiver types, interfaces, overloads), plus points-to for callbacks and higher-order functions |
| Function summaries | What flows from parameters to return values, side effects and sinks | Computed bottom-up on the call graph, cached by content hash |
| Taint analysis | Untrusted data reaching dangerous operations | Summary-based interprocedural search; field-sensitive for object properties; context-sensitive one call level deep by default |
| Sanitizer and validator modelling | Knows when data has been made safe | Models for zod, yup, validator.js, DOMPurify, path checks, allowlist comparisons |
| Correctness rules | Non-security bugs | Unhandled promise rejections, null and undefined dereference, unreachable code, wrong `await`, resource leaks |
| Metrics | Maintainability signals | Cognitive complexity, duplication (token hashing), size |

**Launch rule set (security first, TypeScript and JavaScript)**

SQL and NoSQL injection, command injection, path traversal, SSRF, XSS (including `dangerouslySetInnerHTML` and server-rendered HTML), open redirect, prototype pollution, unsafe deserialization and `eval`, ReDoS, insecure JWT verification, weak crypto, hard-coded secrets, missing authorization checks on route handlers (agent-assisted), and Next.js server-action and route-handler input validation.

**Framework models** describe sources, sinks and sanitizers as data, so adding a framework rarely needs engine code:

```yaml
model: express
sources:
  - "express.Request.query"
  - "express.Request.body"
  - "express.Request.params"
  - "express.Request.headers"
sinks:
  - call: "express.Response.redirect"
    arg: 0
    kind: open-redirect
  - call: "express.Response.send"
    arg: 0
    kind: xss
    when: { content_type: html }
sanitizers:
  - call: "encodeURIComponent"
    clears: [open-redirect]
```

Launch models: Node core, Express, Fastify, Koa, Hono, NestJS, Next.js (App and Pages router), React, and data libraries `pg`, `mysql2`, Prisma (`$queryRawUnsafe`), Knex, TypeORM, Sequelize, Drizzle (`sql.raw`), Mongoose, axios, `fetch`, `child_process`, `fs`.

**Incremental analysis**

- Files hashed by content; IR and summaries cached per hash and engine version.
- On a PR, only changed files are re-lowered; summaries are recomputed for changed functions and their callers, up to the point where a summary stops changing.
- Target: under 30 seconds of engine time for a typical PR on a 500k-line repo with a warm cache.

**Query API for agents and the IDE.** The engine runs as a long-lived service per review and answers typed queries (`taint_paths`, `callers`, `type_of`, `reaching_definitions`) over an internal Connect API. Agents use it to prove or disprove hypotheses; the IDE uses the same API through `patchd lsp`.

**Expanding to more languages later:** the IR, dataflow, taint and rules layers are language-neutral. A new language needs a new bridge (parser plus type information) and its own framework models.

## Packages and dependencies

The rule is to own the analysis and the trust layer, and to use proven libraries for everything else. Versions are pinned in `go.mod` and `pnpm-lock.yaml`; every dependency is itself scanned by Patchd.

**Go backend and engine**

| Job | Package | Notes |
| --- | --- | --- |
| TypeScript parsing, binding, type checking | `github.com/microsoft/typescript-go` | Pinned commit, wrapped by `engine/tsbridge` |
| HTTP routing | `github.com/go-chi/chi/v5` | Webhooks, health, static web app |
| Typed API between web, CLI and backend | `connectrpc.com/connect` + `buf` | Protobuf schemas; gRPC-compatible |
| GitHub App | `github.com/google/go-github`, `github.com/bradleyfalzon/ghinstallation/v2` | App auth, checks, review comments, suggestions, commits |
| GitLab | `gitlab.com/gitlab-org/api/client-go` | v1 |
| Git operations | `git` CLI via `os/exec`; `github.com/sourcegraph/go-diff` for diff parsing | The real git binary is the most reliable for clones, worktrees and commits |
| Postgres | `github.com/jackc/pgx/v5`, `sqlc` (codegen), `github.com/pressly/goose` (migrations) | Type-safe SQL, no ORM |
| Vector search | `pgvector` extension + `github.com/pgvector/pgvector-go` | HNSW index for summaries and past comments |
| Job queue and workflows | `github.com/riverqueue/river` | Postgres-backed; no Redis or Kafka needed |
| LLM providers | `github.com/anthropics/anthropic-sdk-go`, `github.com/openai/openai-go`, `google.golang.org/genai` | `openai-go` also covers OpenAI-compatible APIs (Mistral, Azure, vLLM, Ollama) for BYOK |
| MCP server | `github.com/modelcontextprotocol/go-sdk` | Exposes review and engine queries to coding agents |
| Language server (IDE) | `github.com/tliron/glsp` | `patchd lsp` |
| Sandbox | `github.com/firecracker-microvm/firecracker-go-sdk`; gVisor `runsc` via containerd | Firecracker in Cloud, gVisor in Self-Hosted |
| Dependency vulnerabilities | `github.com/google/osv-scanner/v2` + OSV data | Lockfile parsing and advisory matching; we add reachability on top |
| Secrets | `github.com/zricethezav/gitleaks/v8` | Used as a library |
| SARIF output | `github.com/owenrumney/go-sarif/v2` | Interop with GitHub code scanning and other tools |
| Config | `github.com/goccy/go-yaml`, `github.com/santhosh-tekuri/jsonschema/v6` | `patchd.yaml` parsing and validation against a published schema |
| CLI | `github.com/spf13/cobra` | `patchd review`, `scan`, `fix`, `lsp`, `mcp`, `serve` |
| Auth | `github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`, `github.com/crewjam/saml` | OIDC everywhere, SAML in Cloud |
| Observability | `log/slog`, `go.opentelemetry.io/otel`, `github.com/prometheus/client_golang` | Traces per review, cost per review as a metric |
| Billing (Cloud) | `github.com/stripe/stripe-go` | Stripe's EU entity; Mollie (Amsterdam) as an alternative |

**Tools Patchd runs inside the sandbox (TypeScript projects)**

| Tool | Purpose |
| --- | --- |
| `oxlint` + `oxlint-tsgolint` | Fast lint and type-aware rules, as extra signal and for style the engine ignores |
| The repo's own ESLint, Prettier and test runner (Vitest, Jest, Mocha, `node:test`) | Respect existing config; run tests for proofs and patch checks |
| `tsgo --noEmit` | Type-check patched code before it's shown |
| `fast-check` | Property-based inputs for reproduction tests |

**Web app and IDE (TypeScript)**

| Job | Package |
| --- | --- |
| UI framework and build | React 19, Vite |
| Routing and data | TanStack Router, TanStack Query, `@connectrpc/connect-web` |
| Styling and components | Tailwind CSS, shadcn/ui (Radix primitives) |
| Code and diff rendering | Shiki for highlighting; own diff view grouped by intent |
| Graphs (codebase map, taint traces) | React Flow |
| Charts | Recharts |
| VS Code extension | `vscode-languageclient` |
| End-to-end tests | Playwright |

**Deliberately not used:** an ORM, Redis, Kafka, Kubernetes for Self-Hosted small installs, and a separate graph database. Postgres with recursive queries holds the code graph until scale proves otherwise.

## Agent and model layer

Agents are a small loop we write ourselves in Go, not a third-party agent framework: we need exact control over tools, budgets, caching and evidence.

**The agent loop (`agents/` package)**

- An **orchestrator** reads the semantic diff, engine findings and context, and writes a list of narrow hypotheses ("user-controlled `redirect` reaches `res.redirect` without an allowlist").
- Each hypothesis gets its own **investigator** agent, run in parallel with a token and time budget.
- An investigator must end with a structured verdict: `confirmed` with evidence, `refuted` with reason, or `inconclusive`. Free-text answers are rejected.
- A **patcher** agent writes the fix for confirmed findings; the patch then goes back through the engine and sandbox.

**Tools investigators can call** (all Go functions, typed with JSON Schema)

| Tool | What it does |
| --- | --- |
| `engine.taint_paths(source, sink)` | Ask our engine for dataflow paths |
| `engine.callers(symbol)` / `engine.callees(symbol)` | Call graph queries with resolved types |
| `engine.type_of(expr)` | Type of an expression at a location |
| `code.read(file, range)` / `code.search(query)` | Read files; ripgrep-style and semantic search |
| `kb.lookup(topic)` | Repo knowledge base: architecture, conventions |
| `git.history(file)` / `git.blame(range)` | Why the code looks like this |
| `sandbox.run_test(code)` | Write and run a reproduction test in the sandbox |
| `rules.lookup(path)` | Team rules and memories that apply |

**Model routing**

| Task | Model class | Why |
| --- | --- | --- |
| Code summaries for the knowledge base, context compression | Small, cheap model | High volume, simple task |
| Hypothesis generation, investigation, patch writing | Frontier reasoning model | Quality matters most here |
| Cross-check of high-severity findings | Frontier model from a different family than the investigator (and than the PR's AI author, if any) | Independent second opinion |
| Embeddings | Provider embedding model, or a local model in Self-Hosted | Retrieval only |

**Providers**

- **Cloud (EU-first):** only EU-region endpoints by default. Candidates: Mistral (EU-based provider), Anthropic models through EU regions of AWS Bedrock or Google Vertex AI, OpenAI models through Azure EU data zones. Zero-retention terms with each.
- **Self-Hosted (BYOK):** any of the above with the customer's key, or a local model over an OpenAI-compatible endpoint (vLLM, Ollama). A `models:` block in admin settings maps each task class to a model.
- **Prompt caching** on every provider that supports it: stable prefix (system prompt, repo knowledge base, rules), then the changing diff.

## Sandbox and execution

Every review that runs code gets a fresh, disposable sandbox; the engine itself runs outside the sandbox because it only reads code and never executes it.

**What runs where**

| Runs outside the sandbox (trusted workers) | Runs inside the sandbox (untrusted) |
| --- | --- |
| Parsing, type checking, taint analysis (our engine) | `npm ci` / `pnpm install` / `yarn install` |
| Agent orchestration, model calls | The repo's tests, reproduction tests, patch checks |
| Database, queue, GitHub API | Repo linters and formatters (ESLint plugins can run arbitrary code) |

**Sandbox design**

- **Cloud:** Firecracker microVMs on bare-metal hosts, managed by a `sandboxd` service written in Go. Per-repo **warm snapshots** (repo checked out, dependencies installed) are taken after the first successful install and refreshed when the lockfile changes, so later runs start in seconds.
- **Self-Hosted:** gVisor (`runsc`) containers by default; Firecracker where the host supports KVM; plain Docker only with an explicit "weaker isolation" setting.
- **Network:** off by default. During install only, egress goes through a proxy that allows the npm registry (or the customer's private registry) and nothing else.
- **Secrets:** none inside. The repo is copied in; no Git token, no model key. Agents talk to the sandbox through a narrow `sandboxd` API (`write_file`, `run`, `read_output`).
- **Limits:** CPU, memory, disk and wall-clock caps per run; output truncated; the VM is destroyed after the review.
- **Lifecycle scripts:** `npm install --ignore-scripts` first; scripts are only enabled when tests need them, and always inside the sandbox.

**Resolving how to run a project's tests** is its own small agent task: read `package.json`, CI config (`.github/workflows`) and lockfile, pick the package manager and test command, and cache the recipe per repo. When it can't, Patchd still reviews; it just can't attach execution proof.

## Commit vs suggest

Every verified patch is delivered in one of four modes, chosen by settings: the default is `suggest`, and Patchd never pushes code unless a team has turned that on.

| Mode | What happens | Good for |
| --- | --- | --- |
| `off` | Finding only, no patch shown | Teams that want to write every fix themselves |
| `suggest` (default) | GitHub suggestion block / GitLab suggestion on the line; author clicks "commit suggestion" | Most teams |
| `commit` | Patchd pushes a commit to the PR branch | Trusted fix types, AI-agent-authored PRs, bots like Renovate |
| `fix_pr` | Patchd opens a separate PR targeting the PR branch (or the default branch for full-repo scans) | Larger fixes, multi-file patches, findings outside the diff |

A person can always override the mode on one finding by commenting `@patchd apply` (commit it now), `@patchd fix-pr`, or `@patchd suggest`, and `@patchd revert` undoes a Patchd commit.

**Configuration**

```yaml
patches:
  mode: suggest              # default for everything below
  require:
    tests_pass: true         # patch must pass the repo's tests in the sandbox
    typecheck: true          # tsgo --noEmit clean
  rules:
    - when: { category: security, severity: [critical, high] }
      mode: fix_pr           # security fixes get their own reviewable PR
    - when: { author: agent }        # PRs written by Claude Code, Cursor, Codex…
      mode: commit
    - when: { files_changed_gt: 3 }
      mode: fix_pr
    - when: { path: "src/payments/**" }
      mode: suggest          # never auto-commit here
  commit:
    max_per_pr: 3
    message: "fix({category}): {title} [patchd]"
    squash: true             # one commit for all accepted patches in a review
```

**Precedence:** org policy → repo settings in the web app → `patchd.yaml` → path and category rules, most specific wins. One exception: an org admin can set a **ceiling** (for example "never above `suggest`"), and nothing below can exceed it.

**Safety rules built in, not configurable**

- Never push to protected branches, never force-push, never rewrite history.
- Fork PRs: push only if the author allowed maintainer edits; otherwise fall back to `suggest`.
- Commits are made by the Patchd GitHub App, so they're signed and verified, with a `Co-authored-by` trailer crediting the PR author.
- A `commit`-mode patch that fails tests or type checking is downgraded to `suggest` with a note, never pushed.
- Patchd for Open Source never pushes to public repos; patches go only into the private security-advisory fork.

## EU-first hosting and compliance

Patchd Cloud launches in the EU only, on an EU-owned provider, and sells EU data sovereignty plus Cyber Resilience Act readiness as core features, not add-ons.

**Hosting**

- **Provider:** an EU-owned cloud so customer code is outside US CLOUD Act reach. Shortlist: Scaleway (Paris, Amsterdam), OVHcloud (France), Hetzner (Germany, Finland). Requirement: managed Postgres, S3-compatible object storage, and bare-metal hosts with KVM for Firecracker.
- **Region:** one EU region at launch (Amsterdam or Paris), with a second EU region for backups and failover.
- **Subprocessors:** EU-only, published list. Model calls go only to EU-region endpoints (see the agent section); any provider without one is off by default in Cloud.
- **No US region at launch.** A US region comes later as a separate deployment, never sharing data with the EU one.

**Why the Cyber Resilience Act is a launch feature.** [From 11 September 2026, manufacturers must report actively exploited vulnerabilities and severe incidents through ENISA's Single Reporting Platform](https://digital-strategy.ec.europa.eu/en/policies/cra-reporting), and [these obligations apply to products already on the market, regardless of when they were sold](https://www.pwc.ch/en/insights/regulation/eu-cyber-resilience-act.html). [The rest of the CRA applies from 11 December 2027](https://www.mondaq.com/compliance/1843102/its-live-the-cyber-resilience-act-reporting-is-mandatory-as-of-today-11-september-2026). Every EU software company now needs continuous vulnerability monitoring and a vulnerability-handling process, which is exactly what Patchd does.

**CRA features to ship**

- SBOM export (CycloneDX and SPDX) per release, generated from lockfiles.
- A vulnerability-handling log per product: found, triaged, fixed, released, with timestamps, as audit evidence.
- A coordinated vulnerability disclosure policy template and `SECURITY.md` generator.
- "Actively exploited" flagging for dependencies (from CISA KEV and EU sources), with a reminder of the CRA reporting clock. Patchd drafts the report content; the customer submits it to ENISA.

**GDPR and contracts**

- Data processing agreement as standard; Patchd is a processor for customer code.
- Data minimisation: code is held only for the review, plus the index the customer opts into; deletion on request within 30 days.
- Prices and invoices in EUR with EU VAT handling.
- Certifications targeted: ISO 27001 first (stronger signal in Europe), SOC 2 Type II after.

## Data and storage

Postgres holds everything except large artifacts, which go to S3-compatible object storage; the engine keeps a content-addressed cache so unchanged files are never re-analysed.

| Store | Holds | Technology |
| --- | --- | --- |
| Postgres: core | Orgs, repos, PRs, reviews, findings, evidence metadata, patches, feedback, rules, memories, detectors, settings | Postgres 17, `pgx` + `sqlc`, `goose` migrations |
| Postgres: code graph | Symbols, edges (calls, references, imports, tests), per snapshot | Plain tables + recursive CTEs; partitioned by repo |
| Postgres: vectors | Function summaries, knowledge-base pages, past comments for the team filter | `pgvector`, HNSW indexes |
| Postgres: jobs | Review and scan jobs, retries, schedules | River |
| Object storage | Sandbox logs, test output, taint-trace JSON, SBOMs, engine cache blobs | S3-compatible (provider's in Cloud; any S3 API in Self-Hosted) |
| Engine cache | Per-file IR and per-function summaries, keyed by content hash + engine version | Local disk on workers, backed by object storage |

**Tenancy:** every table carries `org_id`, enforced with Postgres row-level security as a second line of defence behind the application checks.

**Retention defaults:** source code is not stored at rest beyond the review except in the engine cache and code graph, both deletable per repo; sandbox artifacts kept 30 days; findings and feedback kept while the repo is connected.

## Repo layout and developer workflow

One monorepo, one Go module for the backend, and a pnpm workspace for the TypeScript parts; `patchd` builds as a single binary that can run every role.

```text
patchd/
├── cmd/patchd/              # single binary: serve, worker, sandboxd, review, scan, fix, lsp, mcp
├── engine/
│   ├── tsbridge/            # the ONLY package that touches typescript-go
│   ├── ir/                  # Patchd IR: CFG, SSA, instructions
│   ├── callgraph/           # type-based call resolution
│   ├── dataflow/            # intra- and interprocedural analysis, summaries
│   ├── taint/               # sources, sinks, sanitizers, path search
│   ├── models/              # framework models as YAML + Go hooks (express, next, prisma…)
│   ├── rules/               # security and correctness rules
│   ├── metrics/             # complexity, duplication
│   └── cache/               # content-addressed IR and summary cache
├── agents/                  # orchestrator, investigators, patcher, tools
├── trust/                   # evidence check, dedupe, team filter, ranking, detector health
├── review/                  # review pipeline wiring: triage → context → engine → agents → trust → publish
├── patches/                 # patch verification and delivery modes
├── sandbox/                 # sandboxd, Firecracker and gVisor drivers
├── scm/                     # github/, gitlab/
├── kb/                      # code graph, summaries, knowledge base
├── supplychain/             # OSV, reachability, SBOM
├── store/                   # sqlc queries, migrations
├── api/proto/               # protobuf definitions (buf)
├── web/                     # React + Vite app (pnpm)
├── ide/vscode/              # VS Code extension (pnpm)
├── eval/                    # gold set, harness, scorers
└── deploy/                  # docker-compose, helm, Cloud infra (OpenTofu)
```

**Workflow**

- `pnpm dev` starts Postgres, the API, a worker and the web app locally. pnpm root scripts are the single task runner for Go and TypeScript (pnpm build, test, lint, gen), with Docker Compose for Postgres.
- Go 1.25+, `golangci-lint`, `gofumpt`; pnpm, oxlint and Prettier for TypeScript.
- CI on every PR: unit tests, engine fixture tests, a fast slice of the eval harness, and Patchd reviewing its own PRs (dogfooding from week one).
- Releases: GoReleaser builds the binary and container images; signed with Sigstore cosign; SBOM attached to every release.
- Infrastructure as code with OpenTofu.

## Testing and evaluation

The engine is tested like a compiler (thousands of small fixtures with expected output), and the whole product is tested against real historical vulnerabilities.

**Engine fixtures**

- Small TypeScript files annotated with expectations in comments, for example `// expect: sql-injection from req.query.id` on the sink line, and `// expect-none` for safe code.
- Snapshot tests of the IR and call graph for each language feature (async/await, generics, closures, destructuring, optional chaining, decorators, classes, modules).
- Every false positive and false negative reported by a customer becomes a new fixture.

**Security gold set (TypeScript and JavaScript)**

- Historical npm-ecosystem vulnerabilities from GitHub Security Advisories and OSV that have a fixing commit: check out the vulnerable commit, expect Patchd to find it; check out the fix, expect silence.
- Deliberately vulnerable apps as integration targets: OWASP Juice Shop, OWASP NodeGoat, DVNA.
- Academic datasets of JavaScript vulnerabilities (for example SecBench.js) to evaluate.
- Scored on recall (found and reproduced), precision (reports that are real), and patch correctness (proof test passes, existing tests pass).

**Review gold set:** 300+ real merged TypeScript PRs that later needed a bug fix or revert, as in the product spec, scored by precision, recall and F1.

**Gates in CI:** engine fixtures must all pass; the eval slice may not drop precision or recall beyond a set tolerance; cost per review and latency are tracked per run.

## Build order

Build the engine and the eval harness first, then wrap them in the product; each milestone ends with something that runs on real repos.

1. **M1 · Engine skeleton (weeks 1–6).** `tsbridge` loading real `tsconfig` projects; IR and CFG for functions; intraprocedural taint for Express and Node core (`child_process`, `fs`, `pg`); fixture harness; `patchd scan` CLI printing SARIF. *Done when:* finds known injection bugs in Juice Shop and NodeGoat from the command line.
2. **M2 · Interprocedural engine (weeks 7–12).** Call graph, function summaries, cross-file taint with traces; models for Next.js, NestJS, Fastify, Prisma, Knex, Drizzle, axios/fetch; content-hash cache; security gold set v1. *Done when:* measured recall and precision on the gold set, with incremental re-analysis under 30 seconds on a typical PR.
3. **M3 · Review pipeline (weeks 13–18).** GitHub App, semantic diff, risk triage, codebase map, orchestrator and investigators, trust layer with evidence requirement, summary and inline comments, `suggest` mode. *Done when:* reviewing Patchd's own PRs and 3 design partners' repos.
4. **M4 · Proof and patches (weeks 19–24).** `sandboxd` with Firecracker, test-recipe detection, reproduction tests, verified patches, all four delivery modes and their settings, `patchd.yaml` schema. *Done when:* security findings arrive with a passing proof test and a verified patch.
5. **M5 · Launch (weeks 25–30).** Web app, team filter and feedback, supply-chain reachability, SBOM and CRA log, Stripe billing, EU Cloud region, Self-Hosted Docker Compose. *Done when:* paid launch in the EU.

After launch: GitLab, VS Code extension, MCP server, Patchd for Open Source pilot, then the second language.

## Open questions

- [ ] How much of `typescript-go`'s internals do we need, and can we share shim maintenance with the Oxc team or upstream?
- [ ] Move `tsbridge` to the TypeScript 7.1 public API when it ships, or stay on internals if the API lacks what taint analysis needs?
- [ ] Final EU hosting provider: confirm bare-metal KVM availability, managed Postgres with pgvector, and Amsterdam vs Paris.
- [ ] Confirm which frontier models are available on EU-region endpoints with zero-retention terms at launch.
- [ ] Which framework models first after Express and Next.js, based on design partners' stacks?
- [ ] Fuzzing for TypeScript: property-based (`fast-check`) only at launch, or evaluate a coverage-guided JavaScript fuzzer?
- [ ] Self-Hosted licence (AGPL vs source-available) decides whether the engine is public from day one.

**Sources**

- [TypeScript 7 release and API status (Coddy)](https://coddy.tech/docs/typescript/typescript-7)
- [Microsoft Releases TypeScript 7.0 (InfoQ)](https://infoq.com/news/2026/08/typescript-7-released/)
- [TypeScript 7.0 adoption notes (ecorpit)](https://ecorpit.com/typescript-7-migration-readiness-eslint-astro-blockers-2026/)
- [Oxlint type-aware linting docs](https://oxc.rs/docs/guide/usage/linter/type-aware.md)
- [Type-Aware Linting Stable (Oxc blog)](https://oxc.rs/blog/2026-07-22-type-aware-linting-stable)
- [CRA reporting obligations (European Commission)](https://digital-strategy.ec.europa.eu/en/policies/cra-reporting)
- [EU Cyber Resilience Act (PwC Switzerland)](https://www.pwc.ch/en/insights/regulation/eu-cyber-resilience-act.html)
- [CRA reporting is live (Mondaq)](https://www.mondaq.com/compliance/1843102/its-live-the-cyber-resilience-act-reporting-is-mandatory-as-of-today-11-september-2026)
