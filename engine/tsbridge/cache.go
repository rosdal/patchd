package tsbridge

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/compiler"
)

// parseCache shares parsed declaration files from node_modules and the
// bundled lib files between programs, the way typescript-go's language server
// shares them between projects. Parsed files are immutable and bind once
// (ast.SourceFile.BindOnce), and checkers keep their state off the nodes, so
// sharing is safe. Files are keyed by their parse options, which include
// everything that changes how a file parses.
type parseCache struct {
	mu      sync.Mutex
	entries map[ast.SourceFileParseOptions]*parseEntry
	hits    atomic.Int64
	misses  atomic.Int64
}

type parseEntry struct {
	once sync.Once
	file *ast.SourceFile
}

func newParseCache() *parseCache {
	return &parseCache{entries: map[ast.SourceFileParseOptions]*parseEntry{}}
}

// shareable reports whether a file is the same for every program: compiler
// lib files and declaration files of installed packages.
func shareable(name string) bool {
	return strings.HasPrefix(name, "bundled:///") || (strings.Contains(name, "/node_modules/") && isDeclarationFile(name))
}

func (c *parseCache) get(opts ast.SourceFileParseOptions, parse func() *ast.SourceFile) *ast.SourceFile {
	c.mu.Lock()
	e, ok := c.entries[opts]
	if !ok {
		e = &parseEntry{}
		c.entries[opts] = e
	}
	c.mu.Unlock()
	hit := true
	e.once.Do(func() {
		hit = false
		e.file = parse()
	})
	if hit {
		c.hits.Add(1)
	} else {
		c.misses.Add(1)
	}
	return e.file
}

// cachingHost is a compiler host that serves shareable files from a parseCache.
type cachingHost struct {
	compiler.CompilerHost
	cache *parseCache
}

func (h *cachingHost) GetSourceFile(opts ast.SourceFileParseOptions) *ast.SourceFile {
	if !shareable(opts.FileName) {
		return h.CompilerHost.GetSourceFile(opts)
	}
	return h.cache.get(opts, func() *ast.SourceFile { return h.CompilerHost.GetSourceFile(opts) })
}
