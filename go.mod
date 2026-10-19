module github.com/patchd/patchd

go 1.27.0

require (
	github.com/microsoft/typescript-go/shim/ast v0.0.0
	github.com/microsoft/typescript-go/shim/bundled v0.0.0
	github.com/microsoft/typescript-go/shim/compiler v0.0.0
	github.com/microsoft/typescript-go/shim/core v0.0.0
	github.com/microsoft/typescript-go/shim/tsoptions v0.0.0
	github.com/microsoft/typescript-go/shim/tspath v0.0.0
	github.com/microsoft/typescript-go/shim/vfs/cachedvfs v0.0.0
	github.com/microsoft/typescript-go/shim/vfs/osvfs v0.0.0
	github.com/spf13/cobra v1.10.2
)

require (
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.10 // indirect
	github.com/microsoft/typescript-go v0.0.0-20260708042240-2bd066d87f5b // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)

replace github.com/microsoft/typescript-go/shim/ast => ./engine/tsbridge/shim/ast

replace github.com/microsoft/typescript-go/shim/bundled => ./engine/tsbridge/shim/bundled

replace github.com/microsoft/typescript-go/shim/checker => ./engine/tsbridge/shim/checker

replace github.com/microsoft/typescript-go/shim/compiler => ./engine/tsbridge/shim/compiler

replace github.com/microsoft/typescript-go/shim/core => ./engine/tsbridge/shim/core

replace github.com/microsoft/typescript-go/shim/scanner => ./engine/tsbridge/shim/scanner

replace github.com/microsoft/typescript-go/shim/tsoptions => ./engine/tsbridge/shim/tsoptions

replace github.com/microsoft/typescript-go/shim/tspath => ./engine/tsbridge/shim/tspath

replace github.com/microsoft/typescript-go/shim/vfs => ./engine/tsbridge/shim/vfs

replace github.com/microsoft/typescript-go/shim/vfs/osvfs => ./engine/tsbridge/shim/vfs/osvfs

replace github.com/microsoft/typescript-go/shim/vfs/cachedvfs => ./engine/tsbridge/shim/vfs/cachedvfs
