package tsbridge

import (
	"sync"
	"sync/atomic"

	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/bundled"
	"github.com/microsoft/typescript-go/shim/compiler"
	"github.com/microsoft/typescript-go/shim/vfs"
	"github.com/microsoft/typescript-go/shim/vfs/cachedvfs"
)

// parseCache shares parsed files between the programs of one LoadAll, the
// way typescript-go's language server shares them between projects: lib
// files, node_modules declarations, and source files that several programs
// include (a monorepo's referenced projects). Parsed files are immutable and bind once
// (ast.SourceFile.BindOnce), and checkers keep their state off the nodes, so
// sharing is safe. Files are keyed by their parse options, which include
// everything that changes how a file parses.
type parseCache struct {
	fs    *readOnlyFS // shared by every program of the LoadAll
	files vfs.FS      // fs with lookups cached and lib files served

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
	fs := newReadOnlyFS()
	return &parseCache{
		fs:      fs,
		files:   bundled.WrapFS(cachedvfs.From(fs)),
		entries: map[ast.SourceFileParseOptions]*parseEntry{},
	}
}

// shareable reports whether a file may be shared between programs. Every
// file is: files don't change during a LoadAll, and the key holds all parse
// options. Overlay configs never reach GetSourceFile.
func shareable(string) bool { return true }

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
