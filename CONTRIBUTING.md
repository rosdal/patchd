# Contributing

Patchd is pre-alpha, so expect things to move. Small, focused contributions are welcome.

- **One change per PR.** A bug fix and a refactor are two PRs.
- **Conventional commits.** PR titles must follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `chore:`, `docs:` …). PRs are squash-merged, so the title becomes the commit message on `main`.
- **Run `just ci` before opening a PR.** It runs the same formatting, lint, test and supply-chain checks as CI. You need [`just`](https://github.com/casey/just), [`cargo-deny`](https://github.com/EmbarkStudios/cargo-deny) and pnpm.

Security issues go through [SECURITY.md](SECURITY.md), never a public issue.
