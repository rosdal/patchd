# Architecture decision records

An architecture decision record (ADR) captures one significant decision: the situation that forced it, what was decided, and what follows from it. They exist so that later contributors can find out *why* something is the way it is without asking.

ADRs are numbered and never rewritten. To change a decision, write a new ADR that supersedes the old one and update the old one's status line.

## Template

Keep each ADR to about one page.

```markdown
# NNNN. Title

Status: Accepted · YYYY-MM-DD

## Context

What situation or constraint forces a decision?

## Decision

What we decided, stated plainly.

## Consequences

What becomes easier, what becomes harder, and what we now have to live with.

## Alternatives considered

The other options and why they lost.
```

## Index

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-rust-agent-ts-server.md) | Rust for the agent, TypeScript for the server | Accepted |
| [0002](0002-providers-wrap-native-tools.md) | Providers wrap native update tools | Accepted |
| [0003](0003-licence.md) | Licence: Apache-2.0 agent, AGPL-3.0 server | Accepted |
| [0004](0004-monorepo-layout.md) | Monorepo layout with a single protocol definition | Accepted, amended by 0007 |
| [0005](0005-async-providers.md) | Async providers on tokio | Accepted |
| [0006](0006-apt-get-dist-upgrade-simulation.md) | apt updates come from an `apt-get -s dist-upgrade` simulation | Accepted |
| [0007](0007-agent-reported-schemas-rust-first.md) | Agent-reported schemas are generated from Rust types | Accepted |
| [0008](0008-systemd-timer-before-daemon.md) | A systemd timer schedules runs until the agent needs its own scheduler | Accepted |
| [0009](0009-apply-exactly-the-plan.md) | `apply` installs exactly the plan | Accepted |
| [0010](0010-replace-unattended-upgrades.md) | Patchd replaces unattended-upgrades | Accepted |
| [0011](0011-reboot-detect-dont-act.md) | Detect reboots, act only when asked | Accepted |
