# Third-party notices

Patchd includes or is derived from the following third-party software. Dependencies that are only linked (Go modules in `go.mod`, npm packages in `pnpm-lock.yaml`) carry their own licences and are listed in the SBOM attached to every release.

## typescript-go

- Source: https://github.com/microsoft/typescript-go, pinned in [`engine/tsbridge/TSGO_VERSION`](engine/tsbridge/TSGO_VERSION)
- Licence: Apache License 2.0, Copyright (c) Microsoft Corporation
- Used by: `engine/tsbridge` and the generated shims in `engine/tsbridge/shim/`, which re-export typescript-go's internal packages
- Licence and notice: [`engine/tsbridge/shim/LICENSE-typescript-go`](engine/tsbridge/shim/LICENSE-typescript-go), [`engine/tsbridge/shim/NOTICE-typescript-go.txt`](engine/tsbridge/shim/NOTICE-typescript-go.txt)

## tsgolint

- Source: https://github.com/oxc-project/tsgolint (`tools/gen_shims`)
- Licence: MIT, Copyright (c) 2025 VoidZero Inc., typescript-eslint and contributors
- Used by: [`engine/tsbridge/shim/gen`](engine/tsbridge/shim/gen/main.go), adapted from tsgolint's shim generator; attribution is in the file
