# 0004. Write our own agent loop in Go

- **Status:** Accepted
- **Date:** 2026-10-19

## Context

Agents investigate engine findings and write patches. Patchd's value is that every posted finding carries evidence, so we need exact control over which tools an agent may call, per-hypothesis token and time budgets, prompt-cache layout, provider routing (including EU-region endpoints and BYOK), and the structure of each verdict.

## Decision

The `agents` package implements a small loop in Go. An orchestrator writes narrow hypotheses; each hypothesis gets an investigator with its own budget; every investigator must end with a structured verdict (`confirmed` with evidence, `refuted` with a reason, or `inconclusive`), and free text is rejected. Tools are Go functions typed with JSON Schema. Provider SDKs are used directly behind an `llm` provider interface.

Rejected: third-party agent frameworks. They hide the parts we must control (retries, context assembly, caching, budgets), mostly target Python or TypeScript, and tie us to one vendor's abstractions.

## Consequences

- Budgets, caching and evidence are fully under our control, and cost per review is measurable from the first model call.
- We own retries, streaming, tool-call parsing and provider quirks ourselves.
- Revisit if a Go agent library appears that exposes these controls instead of hiding them.
