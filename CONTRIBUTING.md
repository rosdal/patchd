# Contributing

Patchd is pre-alpha, so expect things to move. Small, focused contributions are welcome.

- **One change per PR.** A bug fix and a refactor are two PRs.
- **Conventional commits.** PR titles must follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `chore:`, `docs:` …). PRs are squash-merged, so the title becomes the commit message on `main`.
- **Run `just ci` before opening a PR.** It runs the same formatting, lint, test and supply-chain checks as CI. You need [`just`](https://github.com/casey/just), [`cargo-deny`](https://github.com/EmbarkStudios/cargo-deny) and pnpm.

## Adding a fixture for a new distro

The apt parser is tested against real command output recorded under `crates/patchd-core/tests/fixtures/apt/`, so support for a new distro or release usually starts with a fixture.

1. Capture it. For a container image, `scripts/capture-fixtures.sh debian:14 debian-14` runs `apt-get update` in a throwaway container and records the commands the provider runs. For a real machine, run `sudo apt-get update` there first, then `scripts/capture-fixtures.sh myhost my-distro`, which connects over ssh, only reads, and replaces the hostname with `example`.
2. Check the files in the new directory for anything private (repository URLs with credentials, internal package names) before committing them.
3. Add a snapshot test for the directory in `crates/patchd-core/src/providers/apt/tests.rs` (one `snapshot_fixture!` line), and copy its `os-release` to `tests/fixtures/os-release/` with a test in `host.rs` if the OS is new.
4. Run `cargo insta test --review` (install with `cargo install cargo-insta`), check the parsed report looks right, accept it, and commit the `.snap` file with the fixture.

A fixture is most useful when it has something to parse: at least one pending update, and ideally a held package (`apt-mark hold <pkg>` before capturing).

For dnf, `scripts/capture-dnf-fixtures.sh fedora:latest fedora-44` does the same in a container and writes `crates/patchd-core/tests/fixtures/dnf/<name>/`, including the `--assumeno` transaction table.

## End-to-end tests

`patchd apply` changes systems, so it is tested against fake repositories of tiny packages, each with one failure mode (a changed conffile, a debconf question, a conflict, a failing or slow postinst). They run as root in a throwaway container, never on your machine:

```sh
# apt: debian:12, debian:13, ubuntu:24.04
docker run --rm -v "$PWD:/src:ro" -v "$PWD/target:/target:ro" debian:13 bash -c \
  'bash /src/tests/e2e/apt/setup-container.sh && PATCHD=/target/x86_64-unknown-linux-musl/release/patchd bats /src/tests/e2e/apt'

# dnf: fedora:latest, almalinux:10
docker run --rm -v "$PWD:/src:ro" -v "$PWD/target:/target:ro" fedora:latest bash -c \
  'bash /src/tests/e2e/dnf/setup-container.sh && PATCHD=/target/x86_64-unknown-linux-musl/release/patchd bats /src/tests/e2e/dnf'
```

Build the binary first with `cargo build --release --target x86_64-unknown-linux-musl -p patchd-cli`.

## Security

Security issues go through [SECURITY.md](SECURITY.md), never a public issue.
