# 0006. Launch Patchd Cloud in the EU only

- **Status:** Accepted
- **Date:** 2026-10-19

## Context

Patchd processes customers' source code. EU companies increasingly require that data stays in the EU under EU jurisdiction, outside the reach of the US CLOUD Act. The Cyber Resilience Act's reporting obligations apply from 11 September 2026 and the rest of the act from 11 December 2027, so EU software companies now need continuous vulnerability handling, which is what Patchd does.

## Decision

- Cloud launches on an EU-owned provider (shortlist: Scaleway, OVHcloud, Hetzner) in one EU region, Amsterdam or Paris, with a second EU region for backups and failover.
- Subprocessors are EU-only and published.
- Model calls go only to EU-region endpoints with zero-retention terms; a provider without one is off by default in Cloud. Self-Hosted uses the customer's own keys or a local model.
- No US region at launch. A later US region is a separate deployment that never shares data with the EU one.

## Consequences

- EU data residency and CRA readiness are selling points from day one.
- Model choice in Cloud is limited to what runs on EU endpoints.
- The provider must offer managed Postgres with pgvector, S3-compatible storage and bare-metal KVM hosts for Firecracker; which one does is still open.
- Non-EU customers see higher latency until a second deployment exists.
