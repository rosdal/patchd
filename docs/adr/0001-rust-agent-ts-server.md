# 0001. Rust for the agent, TypeScript for the server

Status: Accepted · 2026-10-14

## Context

Patchd has two very different components. The agent runs on every managed machine, as root on Linux and as LocalSystem on Windows. It must be small, have no runtime to install, and talk to Windows Update and WinGet through their COM APIs. The server is an ordinary web application: an HTTP API, background workers, a Postgres database and a React UI.

## Decision

- The agent and CLI are written in **Rust**. They ship as a single static binary (musl on Linux, static CRT on Windows) and use the Microsoft-maintained `windows` crate for COM.
- The server, workers and web UI are written in **TypeScript**, the maintainer's existing day-to-day stack.

## Consequences

- No interpreter or runtime has to be installed on managed machines, and a process that runs with full privileges gets memory safety from the language.
- Two languages and two toolchains live in one repository. Types shared between them must come from a single protocol definition (see [0004](0004-monorepo-layout.md)) rather than being written twice.
- Rust is slower to write than the alternatives; the agent is kept deliberately thin to limit that cost.

## Alternatives considered

- **Go for the agent.** Faster to write and also produces static binaries. Rejected because COM bindings (go-ole) are clumsy, and WUA and WinGet are COM-first APIs.
- **Rust for the server too.** One language, but a much slower path to a working web app than the existing TypeScript stack.
