package tsbridge

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/collections"
	"github.com/microsoft/typescript-go/shim/core"
	"github.com/microsoft/typescript-go/shim/tsoptions"
	"github.com/microsoft/typescript-go/shim/tspath"
	"github.com/microsoft/typescript-go/shim/vfs"
)

// Compiler diagnostic codes config parsing reacts to.
const (
	codeFileNotFound    = 6053  // File '{0}' not found. (only reported for extends)
	codeCannotReadFile  = 5083  // Cannot read file '{0}'. (an extends target that does not exist)
	codeFilesListEmpty  = 18002 // The 'files' list in config file '{0}' is empty.
	codeNoInputsFound   = 18003 // No inputs were found in config file '{0}'.
	maxConfigProblemMsg = 3
)

// configHost is the tsoptions.ParseConfigHost discovery parses configs with.
type configHost struct {
	fs  vfs.FS
	cwd string
}

func (h configHost) FS() vfs.FS                  { return h.fs }
func (h configHost) GetCurrentDirectory() string { return h.cwd }

// parseConfig parses one tsconfig/jsconfig through typescript-go's own parser,
// so options mean what they mean to tsgo. It returns nil when the config can't
// be used at all (TSB002); missing extends targets (TSB003) and other problems
// (TSB010) are warnings, and options TypeScript 7 removed are translated
// (TSB009).
func parseConfig(host tsoptions.ParseConfigHost, configPath, root string) (*tsoptions.ParsedCommandLine, []Warning) {
	rel := relPath(root, configPath)
	text, ok := host.FS().ReadFile(configPath)
	if !ok {
		return nil, []Warning{{Code: WarnInvalidConfig, File: rel, Message: "cannot read the config; its files fall back to a synthesised project"}}
	}
	path := tspath.ToPath(configPath, host.GetCurrentDirectory(), host.FS().UseCaseSensitiveFileNames())
	if _, errs := tsoptions.ParseConfigFileTextToJson(configPath, path, text); len(errs) > 0 {
		return nil, []Warning{{
			Code: WarnInvalidConfig, File: rel, Range: diagRange(errs[0]),
			Message: fmt.Sprintf("invalid JSON (%s); its files fall back to a synthesised project", errs[0].String()),
		}}
	}
	cfg, diags := tsoptions.GetParsedCommandLineOfConfigFile(configPath, &core.CompilerOptions{}, nil, host, nil)
	if cfg == nil {
		msg := "the compiler could not parse the config"
		if len(diags) > 0 {
			msg = diags[0].String()
		}
		return nil, []Warning{{Code: WarnInvalidConfig, File: rel, Message: msg + "; its files fall back to a synthesised project"}}
	}

	var warnings []Warning
	var problems []string
	seen := map[string]bool{}
	for _, d := range append(diags, cfg.Errors...) {
		msg := d.String()
		if seen[msg] {
			continue
		}
		seen[msg] = true
		code := d.Code()
		if code == codeCannotReadFile && len(d.MessageArgs()) > 0 && d.MessageArgs()[0] == configPath {
			code = 0 // the config itself, not an extends target
		}
		switch code {
		case codeFileNotFound, codeCannotReadFile:
			target := ""
			if args := d.MessageArgs(); len(args) > 0 {
				target = args[0]
			}
			warnings = append(warnings, Warning{
				Code: WarnExtendsNotFound, File: rel, Range: diagRange(d),
				Message: fmt.Sprintf("extends target %q not found (dependencies not installed?); using the config without it", target),
			})
		case codeFilesListEmpty, codeNoInputsFound:
			// A solution-style config or one whose files all live elsewhere: not a problem.
		default:
			problems = append(problems, msg)
		}
	}
	if len(problems) > 0 {
		msg := strings.Join(problems[:min(len(problems), maxConfigProblemMsg)], "; ")
		if len(problems) > maxConfigProblemMsg {
			msg += fmt.Sprintf("; and %d more", len(problems)-maxConfigProblemMsg)
		}
		warnings = append(warnings, Warning{Code: WarnConfigProblem, File: rel, Message: msg})
	}
	warnings = append(warnings, translateRemovedOptions(cfg.CompilerOptions(), rel)...)
	return cfg, warnings
}

// translateRemovedOptions rewrites TypeScript 5-era options TypeScript 7
// removed, where the meaning is clear, so old configs still resolve modules.
func translateRemovedOptions(opts *core.CompilerOptions, rel string) []Warning {
	var warnings []Warning
	if opts.BaseUrl != "" { //nolint:staticcheck // translating the removed option is what reads it
		// TS 5: non-relative imports resolved against baseUrl, and paths values
		// were relative to it. TS 7 dropped baseUrl; "*": ["./*"] relative to
		// the same directory keeps the old behaviour.
		paths := &collections.OrderedMap[string, []string]{}
		hasStar := false
		if opts.Paths != nil {
			for k, vs := range opts.Paths.Entries() {
				fixed := make([]string, len(vs))
				for i, v := range vs {
					fixed[i] = relativeSpec(v)
				}
				paths.Set(k, fixed)
				hasStar = hasStar || k == "*"
			}
		}
		if !hasStar {
			paths.Set("*", []string{"./*"})
		}
		opts.Paths = paths
		opts.PathsBasePath = opts.BaseUrl //nolint:staticcheck // see above
		warnings = append(warnings, Warning{
			Code: WarnRemovedOption, File: rel,
			Message: fmt.Sprintf(`"baseUrl" was removed in TypeScript 7; translated to "paths" relative to %s`, opts.PathsBasePath),
		})
		opts.BaseUrl = "" //nolint:staticcheck // see above
	}
	switch opts.ModuleResolution {
	case core.ModuleResolutionKindNode10, core.ModuleResolutionKindClassic:
		warnings = append(warnings, Warning{
			Code: WarnRemovedOption, File: rel,
			Message: fmt.Sprintf(`"moduleResolution": %q was removed in TypeScript 7; resolving as %q`,
				resolutionName(opts.ModuleResolution), resolutionName(opts.GetModuleResolutionKind())),
		})
	}
	return warnings
}

func relativeSpec(v string) string {
	if strings.HasPrefix(v, "./") || strings.HasPrefix(v, "../") || strings.HasPrefix(v, "/") || tspath.IsRootedDiskPath(v) {
		return v
	}
	return "./" + v
}

func resolutionName(k core.ModuleResolutionKind) string {
	switch k {
	case core.ModuleResolutionKindClassic:
		return "classic"
	case core.ModuleResolutionKindNode10:
		return "node10"
	case core.ModuleResolutionKindNode16:
		return "node16"
	case core.ModuleResolutionKindNodeNext:
		return "nodenext"
	case core.ModuleResolutionKindBundler:
		return "bundler"
	}
	return "unknown"
}

// synthesisedConfigName is the name of the in-memory config file of a
// synthesised project. It never exists on disk.
const synthesisedConfigName = "tsconfig.patchd-synthesised.json"

// synthesisedConfig is the config for files no config covers: JavaScript
// friendly, unchecked JS, lenient TS, modern target and Node module semantics.
func synthesisedConfig(files []string) string {
	cfg := map[string]any{
		"compilerOptions": map[string]any{
			"allowJs":              true,
			"checkJs":              false,
			"strict":               false,
			"target":               "es2024",
			"module":               "nodenext",
			"moduleResolution":     "nodenext",
			"jsx":                  "preserve",
			"skipLibCheck":         true,
			"noEmit":               true,
			"maxNodeModuleJsDepth": 0,
		},
		"files": files,
	}
	b, _ := json.Marshal(cfg) // cannot fail: maps of strings, bools and string slices
	return string(b)
}

func diagRange(d *ast.Diagnostic) *Range {
	if d.File() == nil {
		return nil
	}
	return &Range{Pos: Pos(d.Pos()), End: Pos(d.End())}
}

// relPath returns p relative to root, or p itself when it is outside root.
func relPath(root, p string) string {
	if root == "" {
		return p
	}
	if p == root {
		return "."
	}
	if strings.HasPrefix(p, root+"/") {
		return p[len(root)+1:]
	}
	return p
}
