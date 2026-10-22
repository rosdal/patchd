package tsbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/microsoft/typescript-go/shim/bundled"
	"github.com/microsoft/typescript-go/shim/collections"
	"github.com/microsoft/typescript-go/shim/compiler"
	"github.com/microsoft/typescript-go/shim/tsoptions"
	"github.com/microsoft/typescript-go/shim/vfs/cachedvfs"
)

// skipDirs are never walked: dependencies, VCS data and build output.
var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, ".hg": true, ".svn": true,
	"dist": true, "build": true, ".next": true, "out": true, "coverage": true,
	".patchd": true, ".turbo": true, ".nx": true, ".cache": true, ".svelte-kit": true,
	".output": true, ".vercel": true, ".yarn": true, "bower_components": true,
}

// sourceExtensions are the files tsbridge analyses.
var sourceExtensions = []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}

// unsupportedExtensions are framework files skipped until framework support exists (TSB008).
var unsupportedExtensions = []string{".vue", ".svelte", ".astro"}

// Discover finds the projects under root without type-checking anything. It
// fails only when root can't be read; everything else becomes a Warning.
func Discover(ctx context.Context, root string, opts DiscoverOptions) (ws *Workspace, err error) {
	defer recoverPanic(&err, "discover")
	start := time.Now()
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil {
		return nil, err
	} else if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	d := &discoverer{
		ctx:     ctx,
		root:    toSlash(filepath.Clean(abs)),
		fs:      newReadOnlyFS(),
		files:   map[string]bool{},
		pkgDirs: map[string]bool{},
		ws:      &Workspace{},
	}
	d.ws.Root = d.root
	projects, ignore := opts.Projects, opts.Ignore
	if !opts.NoRepoConfig {
		rc := d.readRepoConfig()
		if len(projects) == 0 {
			projects = rc.TypeScript.Projects
		}
		ignore = append(slices.Clone(rc.TypeScript.Ignore), ignore...)
	}
	d.ignore.addPatterns(ignore)
	if err := d.walk(); err != nil {
		return nil, err
	}
	d.detectLayouts()
	d.parseConfigs(projects)
	d.assign()
	d.checkNodeModules()
	d.ws.Elapsed = time.Since(start)
	return d.ws, nil
}

type discoverer struct {
	ctx    context.Context
	root   string
	fs     *readOnlyFS
	ignore ignoreRules
	ws     *Workspace

	files       map[string]bool // walked source files (absolute)
	fileList    []string
	configPaths []string
	pkgDirs     map[string]bool // directories (absolute) containing a package.json
	unsupported map[string][]string
	configs     []*discoveredConfig
}

type discoveredConfig struct {
	path      string
	parsed    *tsoptions.ParsedCommandLine
	explicit  bool // has its own "files" or "include"
	filesOnly bool // lists "files": entry points, the rest may come through imports
	files     map[string]bool
	owned     []string
}

type repoConfig struct {
	TypeScript struct {
		Projects []string `yaml:"projects"`
		Ignore   []string `yaml:"ignore"`
	} `yaml:"typescript"`
}

func (d *discoverer) readRepoConfig() repoConfig {
	var rc repoConfig
	for _, name := range []string{"patchd.yaml", "patchd.yml", ".patchd.yaml"} {
		b, err := os.ReadFile(filepath.Join(filepath.FromSlash(d.root), name)) //nolint:gosec // the repo config of the workspace being analysed
		if err != nil {
			continue
		}
		if err := yaml.Unmarshal(b, &rc); err != nil {
			d.warn(Warning{Code: WarnConfigProblem, File: name, Message: "cannot read the typescript section: " + err.Error()})
		}
		break
	}
	return rc
}

func (d *discoverer) warn(w Warning) { d.ws.Warnings = append(d.ws.Warnings, w) }

func (d *discoverer) walk() error {
	d.unsupported = map[string][]string{}
	n := 0
	err := filepath.WalkDir(filepath.FromSlash(d.root), func(osPath string, e fs.DirEntry, err error) error {
		if err != nil {
			if osPath == filepath.FromSlash(d.root) {
				return err
			}
			return nil // unreadable entries are skipped, as git would
		}
		if n++; n%1024 == 0 {
			if err := d.ctx.Err(); err != nil {
				return err
			}
		}
		p := toSlash(osPath)
		rel := relPath(d.root, p)
		if e.IsDir() {
			if p == d.root {
				d.readGitignore(p, "")
				return nil
			}
			if skipDirs[e.Name()] || d.ignore.ignored(rel, true) {
				return filepath.SkipDir
			}
			d.readGitignore(p, rel)
			return nil
		}
		if !e.Type().IsRegular() || d.ignore.ignored(rel, false) {
			return nil
		}
		name := e.Name()
		switch {
		case name == "package.json":
			d.pkgDirs[path.Dir(p)] = true
		case isConfigName(name):
			d.configPaths = append(d.configPaths, p)
		case hasExtension(name, sourceExtensions):
			d.files[p] = true
			d.fileList = append(d.fileList, p)
		case hasExtension(name, unsupportedExtensions):
			ext := path.Ext(name)
			d.unsupported[ext] = append(d.unsupported[ext], rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, ext := range slices.Sorted(maps.Keys(d.unsupported)) {
		files := d.unsupported[ext]
		d.warn(Warning{
			Code: WarnUnsupportedFile, File: files[0],
			Message: fmt.Sprintf("%d %s file(s) skipped until framework support exists", len(files), ext),
		})
	}
	return nil
}

func (d *discoverer) readGitignore(dir, rel string) {
	if b, err := os.ReadFile(filepath.Join(filepath.FromSlash(dir), ".gitignore")); err == nil {
		d.ignore.add(rel, string(b))
	}
}

func isConfigName(name string) bool {
	if name == "tsconfig.json" || name == "jsconfig.json" {
		return true
	}
	return strings.HasPrefix(name, "tsconfig.") && strings.HasSuffix(name, ".json")
}

func hasExtension(name string, exts []string) bool {
	for _, ext := range exts {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// detectLayouts finds workspace tools and their packages.
func (d *discoverer) detectLayouts() {
	var globs []string
	rootPkg, _ := readPackageJSON(path.Join(d.root, "package.json"))
	if b, err := os.ReadFile(filepath.Join(filepath.FromSlash(d.root), "pnpm-workspace.yaml")); err == nil {
		var pw struct {
			Packages []string `yaml:"packages"`
		}
		if err := yaml.Unmarshal(b, &pw); err != nil {
			d.warn(Warning{Code: WarnConfigProblem, File: "pnpm-workspace.yaml", Message: err.Error()})
		}
		d.ws.Layouts = append(d.ws.Layouts, "pnpm")
		globs = append(globs, pw.Packages...)
	}
	if ws := rootPkg.workspaceGlobs(); len(ws) > 0 {
		tool := "npm"
		if fileExists(path.Join(d.root, "yarn.lock")) {
			tool = "yarn"
		}
		d.ws.Layouts = append(d.ws.Layouts, tool)
		globs = append(globs, ws...)
	}
	for _, tool := range []string{"turbo", "nx", "lerna"} {
		if fileExists(path.Join(d.root, tool+".json")) {
			d.ws.Layouts = append(d.ws.Layouts, tool)
		}
	}
	if slices.Contains(d.ws.Layouts, "lerna") && len(globs) == 0 {
		var lerna struct {
			Packages []string `json:"packages"`
		}
		if b, err := os.ReadFile(filepath.Join(filepath.FromSlash(d.root), "lerna.json")); err == nil {
			_ = json.Unmarshal(b, &lerna) // a broken lerna.json just means no lerna packages
		}
		if len(lerna.Packages) == 0 {
			lerna.Packages = []string{"packages/*"}
		}
		globs = append(globs, lerna.Packages...)
	}

	isPackage := func(rel string) bool {
		match := false
		for _, g := range globs {
			g = strings.TrimSuffix(strings.TrimPrefix(g, "./"), "/")
			if neg, ok := strings.CutPrefix(g, "!"); ok {
				if globMatch(neg, rel) {
					match = false
				}
			} else if globMatch(g, rel) {
				match = true
			}
		}
		return match
	}
	for _, dir := range slices.Sorted(maps.Keys(d.pkgDirs)) {
		rel := relPath(d.root, dir)
		nxProject := slices.Contains(d.ws.Layouts, "nx") && fileExists(path.Join(dir, "project.json"))
		if dir == d.root || (!isPackage(rel) && !nxProject) {
			continue
		}
		pj, _ := readPackageJSON(path.Join(dir, "package.json"))
		d.ws.Packages = append(d.ws.Packages, Package{Name: pj.Name, Version: pj.Version, Dir: dir})
	}
}

// parseConfigs parses every discovered config (or the configured ones).
func (d *discoverer) parseConfigs(only []string) {
	paths := d.configPaths
	if len(only) > 0 {
		paths = nil
		for _, p := range only {
			abs := toSlash(filepath.Clean(filepath.Join(filepath.FromSlash(d.root), filepath.FromSlash(p))))
			if st, err := os.Stat(abs); err == nil && st.IsDir() {
				abs = path.Join(abs, "tsconfig.json")
			}
			paths = append(paths, abs)
		}
	}
	host := configHost{fs: cachedvfs.From(d.fs), cwd: d.root}
	extended := map[string]bool{}
	for _, p := range paths {
		if d.ctx.Err() != nil {
			return
		}
		parsed, warnings := parseConfig(host, p, d.root)
		d.ws.Warnings = append(d.ws.Warnings, warnings...)
		if parsed == nil {
			continue
		}
		c := &discoveredConfig{path: p, parsed: parsed, files: map[string]bool{}}
		if raw, ok := parsed.Raw.(*collections.OrderedMap[string, any]); ok {
			_, hasFiles := raw.Get("files")
			_, hasInclude := raw.Get("include")
			c.explicit = hasFiles || hasInclude
			c.filesOnly = hasFiles && len(parsed.FileNames()) > 0
		}
		for _, f := range parsed.FileNames() {
			c.files[f] = true
		}
		if c.filesOnly {
			// An entry-point config (Angular's tsconfig.app.json lists
			// src/main.ts and includes only *.d.ts): the files it covers are
			// those its program reaches through imports. Parse, don't check,
			// to find them.
			for _, f := range reachableFiles(host, p, d.root) {
				c.files[f] = true
			}
		}
		for _, e := range parsed.ExtendedSourceFiles() {
			extended[e] = true
		}
		d.configs = append(d.configs, c)
	}
	if len(only) > 0 {
		return
	}
	// A base config that others extend and that names no inputs of its own
	// (tsconfig.base.json) is not a project: by default it would include
	// every file under its directory.
	d.configs = slices.DeleteFunc(d.configs, func(c *discoveredConfig) bool {
		return extended[c.path] && !c.explicit
	})
}

// reachableFiles parses a config's program (without binding or checking)
// and returns its non-declaration files outside node_modules.
func reachableFiles(host configHost, configPath, root string) (files []string) {
	defer func() {
		if recover() != nil {
			files = nil // a compiler panic leaves the config with its listed files
		}
	}()
	ch := compiler.NewCompilerHost(path.Dir(configPath), bundled.WrapFS(host.fs), bundled.LibPath(), nil, nil)
	parsed, _ := parseConfig(ch, configPath, root)
	if parsed == nil {
		return nil
	}
	prog := compiler.NewProgram(compiler.ProgramOptions{Config: parsed, Host: ch, UseSourceOfProjectReference: true})
	for _, sf := range prog.GetSourceFiles() {
		name := sf.FileName()
		if !sf.IsDeclarationFile && !strings.Contains(name, "/node_modules/") && strings.HasPrefix(name, root+"/") {
			files = append(files, name)
		}
	}
	return files
}

// assign gives each walked source file to the most specific config that
// includes it, so each file is analysed once, and puts the rest in
// synthesised projects.
func (d *discoverer) assign() {
	owner := map[string]*discoveredConfig{}
	for _, f := range d.fileList {
		var best *discoveredConfig
		for _, c := range d.configs {
			if c.files[f] && moreSpecific(c, best) {
				best = c
			}
		}
		if best != nil {
			owner[f] = best
			best.owned = append(best.owned, f)
		}
	}
	for _, c := range d.configs {
		if len(c.owned) == 0 {
			continue
		}
		pc := ProjectConfig{
			Name:       relPath(d.root, c.path),
			ConfigPath: c.path,
			Dir:        path.Dir(c.path),
			Files:      c.owned,
			References: c.parsed.ResolvedProjectReferencePaths(),
			Extends:    c.parsed.ExtendedSourceFiles(),
			Root:       d.root,
		}
		d.ws.Projects = append(d.ws.Projects, pc)
	}

	// Uncovered files: one synthesised project per nearest package.json directory.
	groups := map[string][]string{}
	for _, f := range d.fileList {
		if owner[f] != nil || isDeclarationFile(f) {
			continue
		}
		d.ws.Uncovered = append(d.ws.Uncovered, f)
		dir := d.root
		for p := path.Dir(f); len(p) >= len(d.root); p = path.Dir(p) {
			if d.pkgDirs[p] {
				dir = p
				break
			}
			if p == d.root || p == path.Dir(p) {
				break
			}
		}
		groups[dir] = append(groups[dir], f)
	}
	for _, dir := range slices.Sorted(maps.Keys(groups)) {
		d.ws.Projects = append(d.ws.Projects, ProjectConfig{
			Name:        "synthesised:" + relPath(d.root, dir),
			Dir:         dir,
			Synthesised: true,
			Files:       groups[dir],
			Root:        d.root,
		})
	}
	slices.SortStableFunc(d.ws.Projects, func(a, b ProjectConfig) int {
		if a.Synthesised != b.Synthesised {
			if a.Synthesised {
				return 1
			}
			return -1
		}
		return strings.Compare(a.Name, b.Name)
	})

	// Map workspace packages to the project that owns their files.
	for i, pkg := range d.ws.Packages {
		counts := map[string]int{}
		for f, c := range owner {
			if strings.HasPrefix(f, pkg.Dir+"/") {
				counts[c.path]++
			}
		}
		best := ""
		for p, n := range counts {
			if best == "" || n > counts[best] || (n == counts[best] && p < best) {
				best = p
			}
		}
		if best != "" {
			d.ws.Packages[i].Config = relPath(d.root, best)
		}
	}
}

// moreSpecific reports whether c is a better owner than best: a deeper
// directory wins, then the config with fewer files, then the name.
func moreSpecific(c, best *discoveredConfig) bool {
	if best == nil {
		return true
	}
	cd, bd := strings.Count(path.Dir(c.path), "/"), strings.Count(path.Dir(best.path), "/")
	if cd != bd {
		return cd > bd
	}
	if len(c.files) != len(best.files) {
		return len(c.files) < len(best.files)
	}
	return c.path < best.path
}

// checkNodeModules warns (TSB001) for packages with dependencies but no
// installed node_modules in their directory or an ancestor.
func (d *discoverer) checkNodeModules() {
	dirs := []string{d.root}
	for _, p := range d.ws.Packages {
		dirs = append(dirs, p.Dir)
	}
	for _, dir := range dirs {
		pj, ok := readPackageJSON(path.Join(dir, "package.json"))
		if !ok || len(pj.Dependencies)+len(pj.DevDependencies) == 0 {
			continue
		}
		installed := false
		for p := dir; len(p) >= len(d.root); p = path.Dir(p) {
			if dirExists(path.Join(p, "node_modules")) {
				installed = true
				break
			}
			if p == path.Dir(p) {
				break
			}
		}
		if !installed {
			d.warn(Warning{
				Code: WarnPackageNotInstalled, File: relPath(d.root, path.Join(dir, "package.json")),
				Message: "node_modules is missing: imports from packages resolve to unknown",
			})
		}
	}
}

type packageJSON struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Workspaces      json.RawMessage   `json:"workspaces"`
}

func readPackageJSON(p string) (packageJSON, bool) {
	var pj packageJSON
	b, err := os.ReadFile(filepath.FromSlash(p))
	if err != nil {
		return pj, false
	}
	if json.Unmarshal(b, &pj) != nil {
		return packageJSON{}, false
	}
	return pj, true
}

// workspaceGlobs reads "workspaces": either an array or {"packages": [...]}.
func (pj packageJSON) workspaceGlobs() []string {
	if len(pj.Workspaces) == 0 {
		return nil
	}
	var list []string
	if json.Unmarshal(pj.Workspaces, &list) == nil {
		return list
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if json.Unmarshal(pj.Workspaces, &obj) == nil {
		return obj.Packages
	}
	return nil
}

func fileExists(p string) bool {
	st, err := os.Stat(filepath.FromSlash(p))
	return err == nil && st.Mode().IsRegular()
}

func dirExists(p string) bool {
	st, err := os.Stat(filepath.FromSlash(p))
	return err == nil && st.IsDir()
}

func isDeclarationFile(p string) bool {
	return strings.HasSuffix(p, ".d.ts") || strings.HasSuffix(p, ".d.mts") || strings.HasSuffix(p, ".d.cts") ||
		(strings.Contains(path.Base(p), ".d.") && strings.HasSuffix(p, ".ts"))
}
