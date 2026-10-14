# 0005. Async providers on tokio

Status: Accepted · 2026-10-14

## Context

Every provider spends its time waiting on external processes: `dpkg-query`, `apt-get`, and later `dnf` and the Windows Update Agent. The CLI runs one scan and exits, but the v0.3 agent is a long-running scheduler that has to time out a hung `apt-get`, cancel a scan when it shuts down, and keep a heartbeat going while an update runs. The `Provider` trait is implemented by every update source, so changing its shape later means touching all of them.

## Decision

`Provider` and `Runner` are async traits, written with the `async-trait` crate and run on tokio (`rt`, `macros`, `process`, `time` features). Commands run through `tokio::process::Command` with `kill_on_drop(true)` inside `tokio::time::timeout`, so a timeout or a dropped future kills the child process.

`async-trait` is needed because the scanner holds providers as `Box<dyn Provider>`, and plain `async fn` in traits is not object-safe.

## Consequences

- Timeouts and cancellation come from the runtime instead of hand-written thread and signal handling.
- The CLI starts a single-threaded runtime for one scan. That costs little, and the CLI and the agent share one code path.
- `async-trait` boxes each returned future. That is one allocation per provider call, next to a process spawn that costs far more.
- When `async fn` in `dyn` traits stabilises, `async-trait` can be removed without changing any implementation.

## Alternatives considered

- **Sync traits now, with `spawn_blocking` in the agent later.** Simpler for v0.2, but a blocking `wait()` cannot be cancelled: a hung `apt-get` would pin a thread until it exits on its own, and every timeout would need a watchdog thread that kills the process by PID. Switching to async in v0.3 would also change every provider at once.
- **Native `async fn` in traits without `async-trait`.** Not usable through `dyn Provider` on the pinned toolchain.
- **A different runtime (smol, async-std).** tokio has the most mature process and timer support, and it is what the v0.6 HTTP client will need anyway.
