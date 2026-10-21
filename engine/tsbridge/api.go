package tsbridge

import (
	"fmt"
	"sync"
	"time"
)

// This file is the whole public surface of tsbridge (ADR 0008). Only Patchd
// types cross it: no exported function, method or field takes or returns a
// typescript-go type. Implementations live in unexported code in this package.

// Workspace is everything Discover found under a repo root.
type Workspace struct {
	Root      string          // absolute, forward slashes
	Layouts   []string        // workspace tools detected: "pnpm", "npm", "yarn", "turbo", "nx", "lerna"
	Packages  []Package       // workspace packages (directories with a package.json listed by a layout)
	Projects  []ProjectConfig // one per config that owns files, plus synthesised projects
	Uncovered []string        // source files no config covers; they are in synthesised projects
	Warnings  []Warning
	Elapsed   time.Duration
}

// Package is a workspace package or an installed npm package.
type Package struct {
	Name    string
	Version string
	Dir     string // absolute
	Config  string // workspace packages: config path of the project owning the package's files, if any
}

// ProjectConfig is one project to load: a tsconfig/jsconfig, or a synthesised
// config for files no config covers.
type ProjectConfig struct {
	Name        string   // config path relative to the workspace root, or "synthesised:<dir>"
	ConfigPath  string   // absolute; empty for synthesised projects
	Dir         string   // absolute directory the project's relative paths resolve against
	Synthesised bool     // true when Patchd made up the config
	Files       []string // absolute paths of the source files this project analyses (each file in one project)
	References  []string // absolute config paths of project references
	Extends     []string // absolute config paths in the extends chain, nearest first
	Root        string   // workspace root
}

// DiscoverOptions tune Discover. The zero value is the default.
type DiscoverOptions struct {
	// Projects, when set, lists the configs to use (paths relative to the root),
	// replacing auto-discovery. Overrides typescript.projects in patchd.yaml.
	Projects []string
	// Ignore lists extra gitignore-style patterns to skip. Added to
	// typescript.ignore in patchd.yaml.
	Ignore []string
	// NoRepoConfig skips reading <root>/patchd.yaml.
	NoRepoConfig bool
}

// LoadOptions tune Load. The zero value is the default.
type LoadOptions struct {
	MaxFiles     int           // analyse at most this many project files (0: no limit); see TSB005
	Timeout      time.Duration // cancel loading and checking after this long (0: no limit); see TSB006
	SkipLibCheck *bool         // default true: do not type-check .d.ts files
	IncludeTests *bool         // default true: security bugs hide in test helpers too
	Checkers     int           // typescript-go checkers per program (0: typescript-go's default of 4)
	NoCheck      bool          // parse and bind only; types are still computed on demand
	// Priority files are analysed first when MaxFiles cuts a project (for
	// example the files a pull request changes). Absolute paths.
	Priority []string
	// cache shares parsed declaration files between programs; set by LoadAll.
	cache *parseCache
}

// FileID identifies a file within one Program. The zero FileID is invalid.
type FileID uint32

// NodeID identifies a syntax node within one Program. The zero NodeID is
// invalid. NodeIDs are small values, cheap to store in maps, and meaningless
// outside the Program that produced them.
type NodeID uint64

// SymbolID identifies a symbol within one Program. The zero SymbolID is invalid.
type SymbolID uint32

// TypeID identifies a type within one Program. The zero TypeID is invalid.
type TypeID uint32

// File returns the file a node belongs to.
func (n NodeID) File() FileID { return FileID(n >> 32) }

// Pos is a byte offset into a file's text.
type Pos int

// Range is a half-open byte range [Pos, End) in one file.
type Range struct {
	Pos Pos `json:"pos"`
	End Pos `json:"end"`
}

// Contains reports whether p is inside r.
func (r Range) Contains(p Pos) bool { return p >= r.Pos && p < r.End }

// Location is a range in a named file.
type Location struct {
	File    FileID   `json:"-"`
	Path    string   `json:"path"`
	Range   Range    `json:"range"`
	Line    int      `json:"line"`              // 1-based
	Column  int      `json:"column"`            // 1-based, UTF-16 code units
	Package *Package `json:"package,omitempty"` // set when the file is inside an npm package
}

// File is one source file of a Program.
type File struct {
	ID          FileID
	Path        string   // absolute, forward slashes; bundled lib files start with "bundled:///"
	Text        string   // the file's text as parsed
	Root        bool     // the project analyses this file; other files are dependencies
	Declaration bool     // a .d.ts file
	JS          bool     // a JavaScript file
	Lib         bool     // a lib.*.d.ts file bundled with the compiler
	Package     *Package // the npm package the file belongs to, if it is under node_modules
	linesOnce   sync.Once
	lines       lineMap
}

// Diagnostic is a compiler diagnostic: a type error, syntax error or config problem.
type Diagnostic struct {
	File     FileID             `json:"-"`
	Path     string             `json:"path,omitempty"`
	Range    Range              `json:"range"`
	Line     int                `json:"line,omitempty"`
	Column   int                `json:"column,omitempty"`
	Code     int                `json:"code"`
	Category DiagnosticCategory `json:"category"`
	Message  string             `json:"message"`
}

// DiagnosticCategory is the severity of a Diagnostic.
type DiagnosticCategory string

// Diagnostic categories.
const (
	DiagnosticError      DiagnosticCategory = "error"
	DiagnosticWarning    DiagnosticCategory = "warning"
	DiagnosticSuggestion DiagnosticCategory = "suggestion"
	DiagnosticMessage    DiagnosticCategory = "message"
)

// Warning records something tsbridge skipped, fell back on or could not do.
// Analysis continues; warnings say where it is blind.
type Warning struct {
	Code    WarningCode `json:"code"`
	Message string      `json:"message"`
	File    string      `json:"file,omitempty"`
	Range   *Range      `json:"range,omitempty"`
}

func (w Warning) String() string {
	if w.File != "" {
		return fmt.Sprintf("%s %s: %s", w.Code, w.File, w.Message)
	}
	return fmt.Sprintf("%s %s", w.Code, w.Message)
}

// WarningCode identifies a kind of Warning. See docs/engine/tsbridge.md.
type WarningCode string

// Warning codes.
const (
	WarnPackageNotInstalled WarningCode = "TSB001" // node_modules missing or a package not installed
	WarnInvalidConfig       WarningCode = "TSB002" // a tsconfig is not valid JSON; its files fall back to a synthesised project
	WarnExtendsNotFound     WarningCode = "TSB003" // an extends target is missing; the config is used without it
	WarnSyntaxErrors        WarningCode = "TSB004" // a file has syntax errors; the partial tree is analysed
	WarnMaxFiles            WarningCode = "TSB005" // a project has more files than MaxFiles; the rest are skipped
	WarnTimeout             WarningCode = "TSB006" // loading or checking exceeded Timeout; results are partial
	WarnCompilerPanic       WarningCode = "TSB007" // the compiler panicked; the project failed, others continue
	WarnUnsupportedFile     WarningCode = "TSB008" // .vue, .svelte, .astro files are skipped
	WarnRemovedOption       WarningCode = "TSB009" // a compiler option TypeScript 7 removed was translated or dropped
	WarnConfigProblem       WarningCode = "TSB010" // a config has errors the compiler reported; it is used as far as it parses
	WarnUntypedPackages     WarningCode = "TSB011" // installed packages ship no types; their imports are any
)

// Stats are counters and timings for one Program.
type Stats struct {
	Files     int           `json:"files"`      // all files in the program
	RootFiles int           `json:"root_files"` // files the project analyses
	Lines     int           `json:"lines"`      // lines in root files
	Parse     time.Duration `json:"parse_ns"`   // config parsing, file parsing and module resolution
	Bind      time.Duration `json:"bind_ns"`
	Check     time.Duration `json:"check_ns"`
	Checkers  int           `json:"checkers"`
	// Cache counters for TypeOf and SymbolOf memoisation.
	TypeOfCalls, TypeOfHits     int64
	SymbolOfCalls, SymbolOfHits int64
}

// Signature is the signature a call resolved to, after overload resolution
// and generic instantiation.
type Signature struct {
	Text        string      `json:"text"` // as the compiler displays it, e.g. "(a: string) => void"
	Parameters  []Parameter `json:"parameters"`
	Return      TypeID      `json:"-"`
	Declaration *Location   `json:"declaration,omitempty"` // the chosen overload, when it has one
}

// Parameter is one parameter of a Signature.
type Parameter struct {
	Name     string `json:"name"`
	Type     TypeID `json:"-"`
	Optional bool   `json:"optional,omitempty"`
	Rest     bool   `json:"rest,omitempty"`
}

// Callee is what a call, new expression, tagged template, decorator or JSX
// element resolved to.
type Callee struct {
	Symbol        SymbolID   `json:"-"`
	Name          string     `json:"name,omitempty"`
	QualifiedName string     `json:"qualified_name,omitempty"`
	Declarations  []Location `json:"declarations,omitempty"`
	// Reason says why resolution failed when ok is false.
	Reason UnresolvedReason `json:"reason,omitempty"`
}

// UnresolvedReason says why a call did not resolve.
type UnresolvedReason string

// Reasons a call does not resolve.
const (
	ReasonNotACall            UnresolvedReason = "not-a-call"
	ReasonAnyReceiver         UnresolvedReason = "any-receiver"          // the callee or its receiver is typed any
	ReasonDynamicProperty     UnresolvedReason = "dynamic-property"      // obj[expr] with a non-literal key
	ReasonPackageNotInstalled UnresolvedReason = "package-not-installed" // the callee comes from a package (or @types/node) that is not installed
	ReasonUntypedPackage      UnresolvedReason = "untyped-package"       // the callee comes from an installed package with no types
	ReasonUnresolvedName      UnresolvedReason = "unresolved-name"       // the callee or its receiver names something the compiler can't find (an undeclared global)
	ReasonNoSymbol            UnresolvedReason = "no-symbol"             // the callee expression has no symbol (e.g. a call result)
	ReasonNoDeclaration       UnresolvedReason = "no-declaration"        // the symbol has no declarations
	ReasonFailed              UnresolvedReason = "failed"                // the compiler failed (recovered panic)
)

// TypeFlags classify a type. A type can have several (a literal is also a
// string, number, boolean or bigint).
type TypeFlags uint32

// Type flags.
const (
	TypeString TypeFlags = 1 << iota
	TypeNumber
	TypeBoolean
	TypeBigInt
	TypeNull
	TypeUndefined
	TypeVoid
	TypeNever
	TypeAny
	TypeUnknown
	TypeLiteral
	TypeUnion
	TypeIntersection
	TypeObject
	TypeFunction
	TypeTypeParameter
	TypeEnum
	TypeESSymbol
	TypeError // the compiler's error type: an unresolved name or package (also TypeAny)
)

var typeFlagNames = []string{
	"string", "number", "boolean", "bigint", "null", "undefined", "void", "never", "any", "unknown",
	"literal", "union", "intersection", "object", "function", "type-parameter", "enum", "symbol", "error",
}

// Strings returns the names of the set flags.
func (f TypeFlags) Strings() []string {
	var out []string
	for i, n := range typeFlagNames {
		if f&(1<<i) != 0 {
			out = append(out, n)
		}
	}
	return out
}
