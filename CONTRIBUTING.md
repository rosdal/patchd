# Contributing

## Setup

See the Development section of the README.

## Commits

Use [Conventional Commits](https://www.conventionalcommits.org), for example
`feat(engine): lower optional chaining`.

## Rules of the road

- Run `task test` and `task lint` before opening a PR.
- Engine changes need a fixture in `engine/testdata/fixtures/`: a `.ts` file
  annotated with `// expect: <rule>` or `// expect-none`.
- Only `engine/tsbridge` may import typescript-go; depguard enforces this.
- Don't edit generated code in `api/gen/`, `store/gen/` or `web/src/api/`; run
  `task gen`.
