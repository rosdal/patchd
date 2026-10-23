# Third-party notices

Patchd includes or is derived from the following third-party software. Dependencies that are only linked (Go modules in `go.mod`, npm packages in `pnpm-lock.yaml`) carry their own licences and are listed in the SBOM attached to every release.

## typescript-go

- Source: https://github.com/microsoft/typescript-go, pinned in [`engine/tsbridge/TSGO_VERSION`](engine/tsbridge/TSGO_VERSION)
- Licence: Apache License 2.0, Copyright (c) Microsoft Corporation
- Used by: `engine/tsbridge` and the generated shims in `engine/tsbridge/shim/`, which re-export typescript-go's internal packages
- Licence and notice: [`engine/tsbridge/shim/LICENSE-typescript-go`](engine/tsbridge/shim/LICENSE-typescript-go), [`engine/tsbridge/shim/NOTICE-typescript-go.txt`](engine/tsbridge/shim/NOTICE-typescript-go.txt)

## Reference-repo snippets in IR tests

- Source: the reference repos pinned in [`eval/targets/reference-repos.yaml`](eval/targets/reference-repos.yaml): OWASP NodeGoat (Apache License 2.0), OWASP Juice Shop, zod, execa, Vercel Commerce, create-t3-turbo, Visual Studio Code, Express and pnpm (MIT)
- Used by: [`engine/ir/testdata/realworld/`](engine/ir/testdata/realworld), unmodified copies of 40 small source files used as golden test inputs; each file's header names its repository, path, commit and licence

## tsgolint

- Source: https://github.com/oxc-project/tsgolint (`tools/gen_shims`)
- Licence: MIT, Copyright (c) 2025 VoidZero Inc., typescript-eslint and contributors
- Used by: [`engine/tsbridge/shim/gen`](engine/tsbridge/shim/gen/main.go), adapted from tsgolint's shim generator; attribution is in the file

## SARIF 2.1.0 JSON schema

- Source: https://docs.oasis-open.org/sarif/sarif/v2.1.0/errata01/os/schemas/sarif-schema-2.1.0.json
- Licence: Copyright (c) OASIS Open 2020, distributed under the OASIS IPR Policy, which permits copying and distributing the specification and its schemas unmodified
- Used by: [`engine/report/testdata/sarif-schema-2.1.0.json`](engine/report/testdata/sarif-schema-2.1.0.json), an unmodified copy the SARIF output is validated against in tests

## OWASP Juice Shop handler in a taint fixture

- Source: https://github.com/juice-shop/juice-shop `routes/keyServer.ts` at the commit pinned in [`eval/targets/reference-repos.yaml`](eval/targets/reference-repos.yaml)
- Licence: MIT, Copyright (c) 2014-2026 Bjoern Kimminich & the OWASP Juice Shop contributors
- Used by: `serveKeyFiles` in [`engine/testdata/fixtures/path-traversal/positive/send_file.ts`](engine/testdata/fixtures/path-traversal/positive/send_file.ts), adapted as a path-traversal test case
