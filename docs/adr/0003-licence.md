# 0003. Licence: Apache-2.0 agent, AGPL-3.0 server

Status: Accepted · 2026-10-14

## Context

The repository is public from its first commit, so the licence had to be chosen before any code was pushed. Two goals pull in different directions: the agent should be easy for anyone, companies included, to install and embed, while a possible hosted Patchd offering should not be undercut by a closed fork of the server run as a service.

## Decision

Patchd uses a split licence:

- **Apache-2.0** ([LICENSE-APACHE](../../LICENSE-APACHE)) for the agent, the CLI and the shared crates under `crates/`, and for the protocol definition under `protocol/`.
- **AGPL-3.0-only** ([LICENSE-AGPL](../../LICENSE-AGPL)) for the server under `server/`.

The README states which licence applies where. Every Rust crate declares `license = "Apache-2.0"` through the workspace.

## Consequences

- Anyone can install, modify and embed the agent with no obligation beyond attribution, and Apache-2.0 includes an explicit patent grant.
- Anyone who offers a modified server to others, including over a network, must publish their changes.
- Some companies forbid AGPL software. They can still use the agent, but may avoid self-hosting the server.
- Contributions are licensed according to the directory they touch, so contributors need to know which part they are changing. Relicensing later needs every contributor's agreement.

## Alternatives considered

- **Apache-2.0 everywhere.** Easiest adoption, but no protection for a hosted offering.
- **AGPL-3.0 everywhere.** Strongest protection, but an AGPL agent on every machine would put off exactly the users the agent is for.
- **MIT.** Similar to Apache-2.0, without the explicit patent grant.
