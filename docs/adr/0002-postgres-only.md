# 0002. Use Postgres as the only stateful dependency

- **Status:** Accepted
- **Date:** 2026-10-19

## Context

Patchd needs relational data (orgs, repos, reviews, findings, settings), a code graph, vector search over summaries and past comments, and a durable job queue. Self-Hosted customers run and back up whatever we depend on, so every extra service is a cost they pay.

## Decision

Postgres 17 is the only database and queue:

- Core data through `pgx` and `sqlc`, migrations with `goose`; no ORM.
- The code graph as plain tables with recursive CTEs, partitioned by repo.
- Embeddings in `pgvector` with HNSW indexes.
- Jobs, retries and schedules in River, which is Postgres-backed.

Large artifacts (sandbox logs, traces, SBOMs, cache blobs) go to S3-compatible object storage.

Rejected: Redis (cache and queue), Kafka (events), a graph database for the code graph, and a separate vector database. Each solves a problem we don't have at launch scale and adds a service to operate.

## Consequences

- A Self-Hosted install is one container plus Postgres.
- Tenancy is enforced in one place: `org_id` on every table plus row-level security.
- Graph queries are written as recursive SQL, which is more work than a graph query language.
- Revisit if code-graph queries or job throughput measurably outgrow Postgres on real customer repos.
