# Patchd Code — Product Design Spec

Oct 19, 2026 · @Mark

## Product summary

Patchd Code is a security-first code reviewer and vulnerability finder: it only reports a problem it can prove, and ships a verified patch with each finding. It runs on a team's private repos and, through Patchd for Open Source, on public projects, where it finds vulnerabilities, patches them and discloses them responsibly to maintainers. It combines three things no single tool does today: a deterministic analysis engine with quality gates (like SonarQube), a living map of the codebase (like Greptile), and AI agents that investigate and test suspected bugs in a sandbox (like CodeRabbit). One trust layer sits over all three and decides what is worth a developer's attention.

**One-line promise:** find the vulnerability, prove it, patch it, before anyone else does.

**Who it's for:** software teams of 5 to 500 engineers on GitHub or GitLab, especially teams where a growing share of code is written by AI agents and human reviewers can't keep up.

**Two ways to run it:** **Patchd Self-Hosted** is free, runs on your own infrastructure and uses your own model API keys. **Patchd Cloud** is paid, hosted by Patchd, includes the models, and adds deeper reviews, daily vulnerability intelligence and team features.

**Design principles**

1. **Evidence or silence.** A comment without a trace, failing test, graph fact or rule citation is not posted. Fewer, better comments beat more comments.
2. **Deterministic first, AI second.** Anything a rule can catch is caught by a rule: cheaper, faster, repeatable. AI handles intent, logic and cross-file reasoning.
3. **Measured by action, not volume.** The north-star metric is the share of comments developers act on. Any detector below 90% gets switched off automatically.
4. **Learns each team.** Every thumbs-down, ignored comment and accepted fix tunes what Patchd says next, visibly and editably.
5. **Patch, don't just flag.** The name is the product: findings arrive with a ready-to-apply change that was re-scanned and re-tested.
6. **New code first.** Gates apply to what changed, so legacy debt never blocks a merge.
7. **One engine, every surface.** PR bot, CI gate, CLI, IDE and coding agents (via MCP) all call the same engine and get the same answer.

## Security and vulnerability discovery

Security is Patchd's first job: every review and scan hunts for exploitable vulnerabilities before anything else, and nothing is called a vulnerability until Patchd has reproduced it in a sandbox.

*Diagram (in the source doc): Vulnerability pipeline · 5 hunters, 4 stages.*

**The five hunters**

| Hunter | What it finds | How |
| --- | --- | --- |
| Taint analysis | Injection (SQL, command, template), SSRF, path traversal, XSS, unsafe deserialization | Cross-file dataflow from untrusted sources to dangerous sinks, with framework models (Express, Django, Spring, Rails, Go net/http…) |
| Variant search | New instances of known bug patterns | Every published CVE fix in the ecosystem (from OSV and GitHub advisories) is turned into a pattern; Patchd searches for the same mistake elsewhere. Every bug Patchd itself finds triggers a variant search too |
| Fuzzing | Memory-safety bugs, crashes, hangs, parser bugs in C, C++, Rust, Go | Agents write fuzz harnesses for exposed entry points and run them with sanitizers; integrates with existing OSS-Fuzz harnesses |
| Agent audit | Bugs rules can't see: broken authorization, IDOR, auth bypass, business-logic flaws, race conditions, crypto misuse | Hypothesis agents read the code around security boundaries and test each suspicion in the sandbox |
| Supply chain | Vulnerable dependencies the code actually reaches, malicious or typosquatted packages, leaked secrets | Lockfile scan against OSV plus call-graph reachability, so an unreachable CVE is low priority, not a red alert |

**Proof of vulnerability**

- A finding becomes a *vulnerability* only with a reproduction: a test that shows the unsafe behaviour (for example, a request that reads another user's record), or a sanitizer crash from a fuzz input.
- The proof is a minimal test, not a weaponised exploit. Patchd demonstrates that the bug is real and reachable; it does not build attack chains, payloads for live systems or anything usable outside the sandbox.
- Proofs are confidential until the fix ships, and are added to the project's test suite with the patch as a regression test.

**Scoring**

- Each vulnerability gets a CWE, a CVSS v4 score, and a reachability verdict (reachable from a public entry point, internal only, or not reachable).
- Affected versions come from finding the commit that introduced the bug; fixed versions from the patch.
- Severity is computed from these facts, never from the model's opinion.

**Patching**

- The patch fixes the root cause, not the symptom: parameterised queries rather than string escaping, an authorization check at the boundary rather than in one handler.
- Verified three ways before anyone sees it: the proof test now passes, the engine re-scan is clean, and the existing test suite still passes.
- Patchd also runs a variant search on the patched pattern, so sibling bugs are fixed in the same change.

**For private repos:** any reproduced high or critical vulnerability on new code fails the `patchd/gate` check. Findings show in the Security screen with the full evidence, and a draft security advisory can be generated for the team's own products.

## Patchd for Open Source

Patchd for Open Source finds vulnerabilities in public projects and hands maintainers a confirmed bug with a ready patch, privately, so the fix ships before the bug is public. Its reputation depends on never wasting a maintainer's time.

*Diagram (in the source doc): Coordinated disclosure flow · 8 steps, 1 escalation path.*

**Two modes**

- **Maintainer-installed (free):** a maintainer installs Patchd on their project and gets everything a paying team gets: PR review, security gate, full-repo scans, findings privately in their dashboard.
- **Patchd research:** Patchd scans projects that haven't installed it, chosen from a queue ranked by how much depends on them (dependents, downloads, OpenSSF criticality score) and how exposed they are (parsers, network-facing code, auth, crypto). Findings go through the disclosure flow above.

**Disclosure rules**

1. **Human triage on every report.** A Patchd security researcher confirms the reproduction, the severity and the patch before any maintainer is contacted. No automated reports, ever.
2. **Private channel first.** Use the project's `SECURITY.md` contact or GitHub private vulnerability reporting; never a public issue or public PR for an unfixed vulnerability.
3. **Arrive with the fix.** Every report includes the root cause, affected versions, the reproduction test and a patch that passes the project's own tests. The maintainer can merge it, change it or write their own.
4. **90-day deadline,** the common industry norm, extendable when the maintainer is actively working on it. If there's no reply after follow-ups, escalate to CERT/CC or the relevant distro security team before publishing.
5. **Credit goes to the maintainers too.** Advisories credit Patchd for the finding and the maintainers for the fix.
6. **Rate limits per project.** Batch related findings into one report; no more than a few open reports per project at once, so a small team isn't buried.

**CVE and advisory handling**

- Request CVE IDs through GitHub's CNA (for GitHub-hosted projects) or the project's own CNA at first; apply to become a CVE Numbering Authority once report volume justifies it.
- Generate the advisory in GitHub Security Advisory and OSV formats: CWE, CVSS v4, affected and fixed versions, patch commit.
- After release, alert downstream: dependents through OSV and GitHub's advisory database, and forks or vendored copies Patchd can find that carry the same code.

**Avoiding "AI slop" reports.** Maintainers, including curl's, have publicly complained about floods of AI-generated security reports that turn out to be bogus. Patchd's answer is structural: no report without a reproduction that passes human review, a public quality record (share of Patchd reports confirmed by maintainers), and a one-click "not valid" that feeds straight back into the hunters.

**Public trust page.** A public list of disclosed vulnerabilities, CVEs, time to fix and confirmed rate. This doubles as Patchd's strongest marketing: real CVEs in real projects, patched.

**Dual-use safeguards**

- Research mode is run only by Patchd staff, not offered as a self-serve "scan any repo" feature.
- Customers can scan public repos only for dependencies they actually use, and results on third-party code are routed into the same disclosure flow rather than handed out raw.
- Proofs are minimal tests, kept confidential until the fix is public, and never include working exploits for live systems.

## Users and jobs to be done

Five roles use Patchd, but the PR author is the one whose trust decides whether the product survives.

| Role | Main job | Where they meet Patchd | What "good" looks like |
| --- | --- | --- | --- |
| PR author | Get my change merged without shipping a bug | PR comments, CLI before push, IDE | 1–3 comments per PR, each worth fixing; one-click patches |
| Reviewer | Spend my review time on design, not on catching null checks | PR summary, risk map, "what to look at" list | Knows where the risky lines are within 30 seconds |
| Tech lead | Keep the codebase healthy and conventions consistent | Dashboard, rules and memories, quality gates | Debt trending down on new code; team rules enforced without nagging |
| Security engineer | Catch real vulnerabilities, not 400 false alarms | Security view, taint traces, triage queue | Every high-severity finding has a source-to-sink trace |
| Coding agent (Claude Code, Cursor, Codex) | Self-check work before opening a PR | MCP server, CLI | Agent fixes its own issues; PR arrives clean |

The open-source program adds two more: the **OSS maintainer**, who wants a confirmed bug with a patch in a private report, not a vague alarm in a public issue; and the **Patchd security researcher** (staff), who triages every finding before it reaches a maintainer.

**Core use cases**

- Review every PR automatically within 3 minutes of opening, and again on each new push (only the new commits).
- Block merges that introduce high-severity bugs or vulnerabilities on new code.
- Explain a large or unfamiliar PR: summary, walkthrough, risk map, sequence diagram.
- Answer questions in the PR thread ("what else calls this?", "write a test for this case").
- Fix findings: apply a suggested patch, or ask Patchd to push a fix commit.
- Scan a whole repo on demand (onboarding, audits) without flooding anyone with legacy findings.
- Let AI coding agents run the same review locally before a human ever sees the code.

## How it works end to end

A PR goes from webhook to posted review in about 3 minutes, and every developer reaction feeds back into what Patchd posts next.

*Diagram (in the source doc): PR review lifecycle · 8 steps, 1 feedback loop.*

1. **PR event.** A webhook for a new PR or push lands in a durable job queue. On a push, only the new commits are reviewed.
2. **Triage.** Patchd maps changed lines to changed functions and classes (the semantic diff), scores risk from what's touched (auth, payments, migrations, public APIs, size), and detects whether an AI agent wrote the PR. Risk decides review depth.
3. **Gather context.** From the codebase map: callers, callees, tests and owners of every changed symbol, plus the repo's knowledge base, the linked ticket, team rules and memories.
4. **Investigate, in parallel.** The analysis engine and linters run on the change and report facts and findings. Meanwhile an orchestrator turns the diff and those findings into narrow hypotheses ("this retry can double-charge") and sends one agent per hypothesis to prove or disprove it in the sandbox, reading code, querying the graph and running tests.
5. **Verify.** The trust layer drops anything without evidence, merges duplicates, scores agreement, applies the team filter and ranks what's left.
6. **Verify patches.** Each surviving finding's fix is applied in the sandbox, re-scanned and tested. Failing patches are reworked once, then dropped (the finding stays, without a patch).
7. **Publish.** One summary comment, up to the cap of inline comments, and the `patchd/gate` status check.
8. **Developer acts.** Applying, editing, fixing or downvoting is recorded. That updates the team filter and each detector's acted-on rate before the next review.

## System architecture

Patchd is five layers over shared stores; each layer adds evidence, and only the trust layer talks to developers.

*Diagram (in the source doc): Patchd architecture · 5 layers, shared stores, surfaces.*

| Layer | Responsibility | Built with |
| --- | --- | --- |
| 1 · Ingestion and triage | Receive events, compute the semantic diff, score risk, pick review depth | Webhook service, durable workflow queue (Temporal or Hatchet) |
| 2 · Analysis engine | Deterministic findings and facts: dataflow, taint, complexity, duplication, secrets, gates | Tree-sitter plus compiler frontends; orchestrated Semgrep, ESLint, ruff, golangci-lint and others; output normalised to SARIF; own taint engine from v2 |
| 3 · Codebase map | Who calls what, where things live, what the team's conventions are | SCIP indexers where a build exists, tree-sitter resolution where not; function-level natural-language summaries, embedded; per-repo knowledge base |
| 4 · Agent layer | Investigate hypotheses the rules can't settle | Orchestrator plus parallel agents with tools (graph query, grep, ast-grep, file read, git history, engine query, test runner); cheap models compress, reasoning models investigate; reviewed by a different model family than the PR author |
| 5 · Trust layer | Decide what gets posted and how | Evidence check, dedupe, agreement scoring, team filter, calibrated ranking, patch verification |

The layers feed each other in four ways: engine findings become agent hypotheses; agents triage engine findings to hide likely false positives; agents draft patches that the engine and sandbox re-check; and agent findings that keep getting accepted are compiled into deterministic rules, so they become cheap and repeatable.

## Feature set

Patchd ships seven feature areas; the PR review and the trust layer are the product, everything else feeds or extends them.

**1. PR review (the core)**

- **Summary comment** at the top of every PR: what changed in plain words, a risk score (low / medium / high), the files a human should look at first, and the quality gate result.
- **Walkthrough:** changes grouped by intent ("adds retry logic", "renames config keys") rather than by file, with a sequence diagram for flows that cross 3+ files.
- **Inline findings** on changed lines only, each with severity, evidence, explanation and a suggested patch. Capped at 7 per review by default.
- **Intent check:** compares the diff against the linked ticket (Jira, Linear, GitHub Issues) and flags acceptance criteria that look unimplemented.
- **Incremental re-review:** on each push, reviews only new commits and auto-resolves findings that the push fixed.

**2. Quality gates and CI**

- A required status check (`patchd/gate`) that passes or fails on new code: no new blocker or critical findings, no new secrets, coverage on new lines above a threshold, duplication below a threshold.
- Default gate is strict on security and correctness and silent on style. Teams can tune it per repo or path.

**3. Patches (autofix)**

- Every finding that can be fixed comes with a patch shown as a GitHub suggestion block.
- Before it's shown, the patch is applied in the sandbox, re-scanned by the engine, and the affected tests are run. Patches that fail are reworked or dropped.
- "Fix all" pushes one commit with every accepted patch, or opens a stacked PR.

**4. Conversation**

- Mention `@patchd` in any PR thread to ask questions, request a test, ask for a fix, or say "don't flag this again" (which becomes a memory).

**5. Codebase map**

- A per-repo knowledge base: architecture summary, modules and owners, conventions, entry points, how data flows. Kept current on every merge to main.
- Browsable in the web app and used by every review as context.

**6. Dashboards and reports**

- Org, team and repo views: findings over time, acted-on rate, debt on new code, security posture, review latency.
- Weekly digest email to tech leads: what Patchd caught, what it got wrong, rules it learned.

**7. Every surface, one engine**

- **GitHub and GitLab apps** (Bitbucket and Azure DevOps later).
- **CLI** (`patchd review`, `patchd scan`, `patchd fix`) for local runs and pre-push hooks.
- **IDE extension** for VS Code and JetBrains, using the same engine via a language server.
- **MCP server** so coding agents can call `review_diff`, `explain_finding` and `get_conventions` before opening a PR.

## Comment model and trust system

Every candidate finding passes the same five gates before it reaches a PR; most candidates never make it, by design.

**What a finding is**

Each finding is a structured record, not free text: location, category, severity, confidence, evidence (one or more), explanation, patch, and the detectors that raised it. The PR comment is rendered from that record.

**Evidence types (at least one required to post inline)**

| Evidence | Produced by | Example | Strength |
| --- | --- | --- | --- |
| Execution proof | Sandbox | A test or repro script that fails before the fix and passes after | Strongest |
| Analysis trace | Engine | Taint path from `req.query.id` to a SQL call, 6 steps across 3 files | Strong |
| Graph fact | Codebase map | "4 callers still pass the old 2-argument form" | Strong |
| Rule citation | Team rules / memories | "Team rule: no direct `fetch` in components, use `apiClient`" | Medium |
| Model reasoning only | Agent | A logic argument with no artifact | Not enough on its own |

**The five gates**

1. **Evidence check:** no evidence, no inline comment. Reasoning-only findings drop to the collapsed "worth a look" section, or are discarded.
2. **Deduplication:** findings from linters, the engine and agents are merged by location and meaning, so one problem means one comment.
3. **Agreement:** findings raised by independent passes or different models score higher; one-off findings from a single pass score lower.
4. **Team filter:** a new finding similar to 3+ comments this team downvoted or ignored is suppressed; similar to accepted ones is boosted. Memories ("we allow `any` in tests") apply here.
5. **Ranking and cap:** score = confidence × severity × historical acceptance for that category in this repo. Top findings post inline, up to the cap; the rest go to the summary.

**Confidence is calibrated, not self-reported.** The model never sets its own severity or confidence. Confidence comes from evidence type, agreement and each detector's measured acceptance rate.

**Comment tiers**

- **Blocking:** fails the gate. Only high-severity, evidence-backed bugs and vulnerabilities.
- **Inline:** likely bugs and risky changes, with patches.
- **Collapsed:** suggestions and lower-confidence observations, inside the summary.
- **Never:** formatting and style. Formatters and linters own that, and Patchd can run them for you.

**Self-policing detectors.** Each detector (a rule, a linter check, an agent hypothesis type) has a live acted-on rate. Below 90% over a rolling window, it's quarantined: it still runs and is measured, but it stops posting until it improves.

**Feedback signals captured**

- Thumbs up / down and a reason picker (wrong, not important, intended, style) on each comment.
- Whether the flagged lines changed before merge (acted on).
- Whether a patch was applied as is, edited, or ignored.
- Bug-fix commits after merge that touch lines Patchd saw but didn't flag (misses).

## Configuration and onboarding

Patchd works with zero config on install; configuration exists to tune, never to get started.

**Onboarding (target: first useful review in under 10 minutes)**

1. Install the GitHub or GitLab app and pick repos.
2. Patchd indexes each repo (minutes for most repos, under an hour for large monorepos) and builds the codebase map.
3. It reads existing conventions automatically: linter configs, `CLAUDE.md`, `AGENTS.md`, `.cursorrules`, `CONTRIBUTING.md`, CODEOWNERS.
4. Optional **backtest**: Patchd reviews the last 20 merged PRs silently and shows what it would have caught, including bugs that were later fixed. This is the sales demo and the calibration run in one.
5. Reviews start on new PRs. Week 1 runs in a quieter mode (blocking and high-confidence only) while the team filter learns.

**`patchd.yaml` (optional, in repo root)**

```yaml
profile: balanced          # quiet | balanced | thorough
max_inline_comments: 7
languages: auto

gate:
  block_on: [critical, high]
  new_code:
    coverage_min: 70
    duplication_max: 3

paths:
  ignore: ["**/generated/**", "vendor/**"]
  rules:
    - path: "src/payments/**"
      instructions: "All amounts are integer cents. Flag any float math."
      profile: thorough
    - path: "**/*.test.ts"
      profile: quiet

rules:
  - id: no-direct-fetch
    when: "React components call fetch() directly"
    say: "Use apiClient so auth and retries apply."
    severity: medium

tools:
  semgrep: on
  eslint: use-repo-config
  secrets: on

integrations:
  tickets: linear
```

**Rules and memories**

- **Rules** are written by people, in plain language or as a pattern. Patchd compiles a plain-language rule into a deterministic check where it can, and falls back to an agent check where it can't.
- **Memories** are learned from feedback ("team allows `console.log` in scripts/"). Each shows where it came from and can be edited, promoted to a rule, or deleted.
- Rules and memories can be set at org, repo or path level; the most specific wins.
- Config can also be managed in the web app; the yaml file, when present, wins and is shown as the source.

## Web app screens and UX

Most developers never open the web app: the PR is the main interface. The web app is for leads, security and admins, plus anyone who wants to dig into a finding.

| Screen | For | What's on it |
| --- | --- | --- |
| Home / Inbox | Everyone | My open PRs and their review status, findings waiting on me, patches ready to apply |
| PR review view | Author, reviewer | Semantic diff grouped by intent, findings beside the code, evidence panel (trace, test output, graph), apply-patch buttons, chat |
| Finding detail | Author, security | Full evidence: step-by-step taint path, sandbox logs and test output, related findings, feedback history for this detector |
| Repo overview | Tech lead | Gate status per branch, findings on new code over time, debt and hotspots, top files by risk |
| Codebase map | Everyone, new hires | Module graph, owners, architecture summary, conventions, "how does X work" search |
| Security | Security engineer | Vulnerabilities by severity and status, triage queue, suppressed-by-filter list (never hidden silently), secrets |
| Rules and memories | Tech lead | All active rules and learned memories, where each came from, its acted-on rate, edit / promote / delete |
| Detector health | Admin, Patchd team | Acted-on rate per detector and category, quarantined detectors, false-positive reports |
| Insights | Leads, managers | Acted-on rate, review latency, bugs caught before merge vs. escaped, AI-authored share of PRs |
| Settings | Admin | Repos, gates, integrations, models (incl. bring-your-own), data retention, SSO, billing |

**UX rules**

- **Every claim is one click from its proof.** Any finding opens straight to the evidence.
- **Feedback is one click.** Thumbs and a reason picker on every comment, in the PR and in the app.
- **Nothing is hidden silently.** Anything the team filter suppresses is listed and can be restored.
- **Same words everywhere.** A finding reads the same in the PR, IDE, CLI and app.

## Data model

The finding and its feedback are the two records everything else is built around.

| Entity | Key fields | Notes |
| --- | --- | --- |
| Org | id, plan, SSO config, data region, model policy | Top-level tenant |
| Repo | id, org, provider, default branch, languages, index version | One per connected repository |
| Snapshot | repo, commit SHA, index status, knowledge base version | Incremental: only changed files re-indexed |
| Symbol / Edge | snapshot, kind (function, class…), file, range, summary; edges: calls, references, imports, tests | The codebase map; Postgres tables |
| PullRequest | repo, number, author, author agent (human, Claude Code, Cursor…), linked tickets, risk score |  |
| Review | PR, commit range, status, tier (base / deep), cost, duration | One per review run |
| Finding | review, file, line range, category, severity, confidence, fingerprint, status (open, fixed, dismissed, suppressed) | Fingerprint keeps identity across pushes |
| Evidence | finding, type (execution, trace, graph, rule, reasoning), payload, artifact URL | One or more per finding |
| Patch | finding, diff, verification result (scan, tests), applied state |  |
| Detector | id, source (rule, linter, engine, agent), category, acted-on rate, state (active, quarantined) | Self-policing lives here |
| Feedback | finding, user, signal (up, down, acted, ignored, patch applied), reason | Feeds the team filter |
| Rule / Memory | scope (org, repo, path), text, compiled form, origin, acted-on rate | People write rules; memories are learned |
| GateResult | review, conditions checked, pass / fail | Posted as a status check |

**Storage:** Postgres for records and the code graph, a vector index (pgvector or LanceDB) for summaries and past comments, object storage for artifacts (logs, screenshots, traces), and a cache keyed by file content hash.

## Security, privacy and deployment

Patchd runs untrusted code from customer repos, so the sandbox is treated as hostile by default.

**Sandbox**

- One disposable microVM per review (Firecracker or gVisor), destroyed when the review ends.
- Only a short-lived, read-only repo token inside; no other secrets, no access to Patchd's internal network.
- Network egress off by default; package registries allowed through a proxy when tests need dependencies.
- Per-repo warm snapshots (dependencies installed) so tests start in seconds.

**Data handling**

- Code is never used to train models. Zero-retention agreements with every model provider.
- Source code is not stored after the review except the index and summaries needed for the codebase map; customers can turn that off.
- EU and US data regions (EU first matters for European buyers).
- SOC 2 Type II and ISO 27001 targeted for year one; GDPR data-processing agreement standard.

**Deployment options**

|  | Patchd Self-Hosted (free) | Patchd Cloud (paid) |
| --- | --- | --- |
| Who runs it | The customer, on their own servers or cloud account | Patchd, multi-tenant with isolated sandboxes; single-tenant for large customers |
| Install | Docker Compose for one machine; Helm chart for Kubernetes; works air-gapped | Install the GitHub or GitLab app; nothing to run |
| Models | Bring your own key (BYOK): Anthropic, OpenAI, Google, Azure, Bedrock, or local models through an OpenAI-compatible endpoint (vLLM, Ollama) | Included; Patchd picks and routes models per task |
| Sandbox | gVisor or Firecracker on the customer's machines; plain Docker as a weaker fallback, with a warning | Managed microVMs with warm per-repo snapshots |
| Data | Never leaves the customer's network, except calls to the model provider they chose | EU or US region, zero retention at model providers, never used for training |
| Updates | Customer upgrades; rule and vulnerability-pattern packs released monthly | Continuous; vulnerability patterns updated daily |

## Pricing and packaging

Patchd comes in two options: **Self-Hosted is free** and you bring your own model keys; **Cloud is paid**, Patchd hosts it, supplies the models and adds features that only work at scale. The core engine, security hunters and PR review are the same code in both, so the free product is genuinely good, and Cloud sells convenience, depth and shared intelligence. Prices are starting proposals to test.

|  | Self-Hosted (free) | Cloud (paid) |
| --- | --- | --- |
| Price | $0; you pay your model provider directly | Proposed $30 per active developer / month, model usage included |
| PR review, summaries, inline findings, chat | Yes | Yes |
| Security hunters: taint, supply chain, secrets, agent audit | Yes | Yes |
| Fuzzing with AI-written harnesses | Yes, on your compute | Yes, managed, with more compute per scan |
| Quality gates, verified patches, CLI, IDE, MCP | Yes | Yes |
| Model choice | Any model you have a key for; you configure routing | Best model per task, routed automatically, including cross-family review (AI-written code reviewed by a different model family) |
| Deep reviews on high-risk PRs (more agents, execution proof) | Yes, limited by your own budget settings | Included, scaled by PR risk |
| Team learning (filter, memories) | Per install | Per team, plus calibration from anonymised acceptance data across all customers |
| Vulnerability intelligence | Monthly pattern packs | Daily variant patterns from new CVEs, and alerts when a new CVE pattern matches your code |
| Codebase map, dashboards, insights | Basic | Full, including org-wide insights and weekly digests |
| SSO, audit log, roles | Community SSO (OIDC) | SAML SSO, SCIM, audit log, fine-grained roles |
| Support | Community (forum, GitHub issues) | Email and chat; SLAs on larger contracts |

**Open-source maintainers get Cloud free.** Public projects get the full hosted product, models included, at no cost, so maintainers never need their own API keys. This is funded as marketing and through partnerships with foundations and security funds such as OpenSSF.

**How costs work in each option**

- **Self-Hosted:** a spend cap per review, per repo and per month, set in the admin settings, with a running cost estimate per PR. Patchd still uses risk scoring, so low-risk PRs get a cheap review and the budget goes to risky ones. Prompt caching and cheap models for context compression are on by default to keep the customer's bill low.
- **Cloud:** each PR gets a risk score; low-risk PRs get a fast, cheap review and only high-risk ones (auth, payments, migrations, large cross-module changes) get the deep, execution-backed review. "Active developer" means someone who opened a PR that month, so seats track real use. Bulk model pricing and caching across reviews are where Cloud's margin comes from.

**Upgrade path:** Self-Hosted users switch to Cloud by connecting the same repos; config files, rules and memories export and import unchanged.

## Metrics and evaluation

The north-star metric is the **acted-on rate**: the share of posted comments where the developer changed the flagged code or applied the patch before merge.

| Metric | Target at v1 | Why |
| --- | --- | --- |
| Acted-on rate (all inline comments) | ≥ 70% | Trust; Google's Tricorder rule is ≥ 90% per check |
| Acted-on rate per detector | ≥ 90%, or quarantined | Keeps noisy checks off PRs |
| Inline comments per PR (median) | 1–3 | Noise ceiling |
| Time to first review (median) | ≤ 3 minutes | Faster than a human picks up the PR |
| Patch applied as is | ≥ 50% of patches shown | Fixes must be usable |
| Escaped bugs | Falling quarter over quarter | Bug-fix commits on lines Patchd reviewed without comment |
| Cost per review (median) | Below $0.50 | Margin at per-seat pricing |

**Security metrics (tracked separately, reported on the public trust page for OSS)**

- **Confirmed rate:** share of vulnerability reports maintainers confirm as real. Target ≥ 95%; this is the number that protects Patchd's reputation.
- **Patch acceptance:** share of reports where the maintainer merged Patchd's patch, as is or edited.
- **Time to fix:** days from private report to fixed release, median.
- **Severity mix:** share of reports rated high or critical, so volume can't be padded with trivia.
- **Security recall:** share of known CVEs in the gold set (historical vulnerable commits) that Patchd finds and reproduces.

**Evaluation harness (built before the product, run on every change)**

- **Gold set:** 300+ real PRs from open-source repos that later needed a bug fix or a revert, with the bug labelled. Split by language and bug type, plus a slice of AI-agent-written PRs.
- **Scoring:** precision, recall and F1 against the gold set, judged by three different LLMs plus human spot-checks.
- **Regression gate:** no model, prompt, rule or detector change ships if F1 or precision drops on the gold set.
- **Live A/B tests:** new review strategies run on a share of real traffic, compared on acted-on rate, cost and latency.
- **Public results:** submit to Martian's independent Code Review Bench and publish the harness so claims can be checked.

## Roadmap

Ship a narrow, trusted reviewer first; add depth only once the acted-on rate holds above 70%.

1. **MVP, months 0–4:** GitHub app; TypeScript, Python, Go; tree-sitter and SCIP code map; security hunters first (taint analysis, supply-chain reachability, secrets) with sandbox reproduction tests; orchestrated linters and Semgrep; one agentic reviewer with evidence requirement and comment cap; thumbs feedback; summary comment; backtest onboarding; eval harness and acted-on dashboard. *Exit gate: 10 design-partner teams, acted-on rate ≥ 60%, first confirmed vulnerabilities found in design-partner code.*
2. **v1, months 4–9:** team filter and editable memories; parallel hypothesis agents with model routing (review with a different model family than the one that wrote the code); quality gates and `patchd/gate` check; verified patches; GitLab; CLI and MCP server; Cloud paid launch; Patchd Self-Hosted free edition (Docker Compose and Helm, BYOK, monthly pattern packs); Patchd for Open Source pilot (free maintainer installs, research mode on 20 critical projects, disclosure process and trust page live). *Exit gate: acted-on ≥ 70%, cost per review below $0.50, first CVEs published with maintainers.*
3. **v2, months 9–15:** Patchd's own cross-file taint engine for top frameworks; AI-written fuzz harnesses for C, C++, Rust and Go; variant search across the OSS ecosystem; apply to become a CVE Numbering Authority; air-gapped Self-Hosted with local models; IDE extensions; Java, C# and Rust; LLM findings promoted into deterministic rules; SOC 2.
4. **v3, month 15+:** architecture rules and drift detection; ticket-intent checks at depth; org-wide insights; Bitbucket and Azure DevOps; public benchmark results.

## Open questions

- [ ] Build our own analysis engine early, or rely on Semgrep and CodeQL longer? CodeQL's license limits commercial use on private code.
- [ ] Which three languages first? Proposed: TypeScript, Python, Go.
- [ ] Should Patchd push fix commits on its own, or only suggest? Proposed: suggest by default, push on request.
- [ ] Per-seat price points: test $24 / $40 with design partners.
- [ ] License for Self-Hosted: open source (e.g. AGPL, which keeps competitors from reselling it as a hosted service) or source-available? And which model providers do we certify for BYOK at launch?
- [ ] Lead with the EU (data residency) or the US market?

* [ ] Legal review of research-mode scanning: safe-harbour terms, jurisdictions, how to handle projects with no security contact.
* [ ] How many security researchers do we need on staff to triage OSS findings at pilot volume?
* [ ] Become a CVE Numbering Authority in year one, or rely on GitHub's CNA longer?

- [ ] Large companies may self-host for free forever: do we add a paid Self-Hosted Enterprise licence (support, SLAs, SAML, daily intelligence feed), or keep exactly two options?
- [ ] Is the free Self-Hosted edition limited in any way (seats, repos), or fully unlimited?
