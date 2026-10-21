package tsbridge

import (
	"strings"
	"sync"
	"time"

	"github.com/microsoft/typescript-go/shim/vfs"
	"github.com/microsoft/typescript-go/shim/vfs/osvfs"
)

// readOnlyFS is the file system every compiler host sees. It reads from disk,
// refuses every write (Patchd never writes into the analysed repo), and serves
// an in-memory overlay of synthesised and repaired config files that shadow or
// add to what is on disk. Overlay files exist only in memory.
type readOnlyFS struct {
	base    vfs.FS
	mu      sync.RWMutex
	overlay map[string]string // absolute path -> contents
}

func newReadOnlyFS() *readOnlyFS {
	return &readOnlyFS{base: osvfs.FS(), overlay: map[string]string{}}
}

func (f *readOnlyFS) set(path, contents string) {
	f.mu.Lock()
	f.overlay[path] = contents
	f.mu.Unlock()
}

func (f *readOnlyFS) lookup(path string) (string, bool) {
	f.mu.RLock()
	s, ok := f.overlay[path]
	f.mu.RUnlock()
	return s, ok
}

func (f *readOnlyFS) UseCaseSensitiveFileNames() bool { return f.base.UseCaseSensitiveFileNames() }

func (f *readOnlyFS) FileExists(path string) bool {
	if _, ok := f.lookup(path); ok {
		return true
	}
	return f.base.FileExists(path)
}

func (f *readOnlyFS) ReadFile(path string) (string, bool) {
	if s, ok := f.lookup(path); ok {
		return s, true
	}
	return f.base.ReadFile(path)
}

func (f *readOnlyFS) WriteFile(string, string) error             { return vfs.ErrPermission }
func (f *readOnlyFS) AppendFile(string, string) error            { return vfs.ErrPermission }
func (f *readOnlyFS) Remove(string) error                        { return vfs.ErrPermission }
func (f *readOnlyFS) Chtimes(string, time.Time, time.Time) error { return vfs.ErrPermission }

func (f *readOnlyFS) DirectoryExists(path string) bool { return f.base.DirectoryExists(path) }

func (f *readOnlyFS) GetAccessibleEntries(path string) vfs.Entries {
	return f.base.GetAccessibleEntries(path)
}

func (f *readOnlyFS) Stat(path string) vfs.FileInfo { return f.base.Stat(path) }

func (f *readOnlyFS) WalkDir(root string, walkFn vfs.WalkDirFunc) error {
	return f.base.WalkDir(root, walkFn)
}

func (f *readOnlyFS) Realpath(path string) string {
	if _, ok := f.lookup(path); ok {
		return path
	}
	return f.base.Realpath(path)
}

// toSlash normalises a path the way the compiler does: forward slashes.
func toSlash(p string) string { return strings.ReplaceAll(p, "\\", "/") }
