package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/patchd/patchd/engine/tsbridge"
)

// positionQuery is one position to answer; also the --batch input format.
// Lines and columns are 1-based; columns count UTF-16 code units.
type positionQuery struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type typeAnswer struct {
	positionQuery
	Kind          string              `json:"kind,omitempty"`
	CompilerKind  string              `json:"compiler_kind,omitempty"`
	Text          string              `json:"text,omitempty"`
	Symbol        string              `json:"symbol,omitempty"`
	QualifiedName string              `json:"qualified_name,omitempty"`
	Type          string              `json:"type,omitempty"`
	Flags         []string            `json:"flags,omitempty"`
	Declarations  []tsbridge.Location `json:"declarations,omitempty"`
	Error         string              `json:"error,omitempty"`
}

func newDebugTypesCmd(f *debugFlags) *cobra.Command {
	var batch string
	cmd := &cobra.Command{
		Use:   "types <file>:<line>:<col>",
		Short: "Show the node, symbol, qualified name, type and declarations at a position",
		Long: "Show the node, symbol, qualified name, type and declarations at a position.\n" +
			"Lines and columns are 1-based; columns count UTF-16 code units, as GitHub and editors show them.\n" +
			"--batch reads a JSON array of {file, line, column} and answers them all (for the type oracle).",
		Args: func(cmd *cobra.Command, args []string) error {
			if batch != "" {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := f.validate(); err != nil {
				return err
			}
			var queries []positionQuery
			if batch != "" {
				b, err := os.ReadFile(batch) //nolint:gosec // the batch file the user named
				if err != nil {
					return err
				}
				if err := json.Unmarshal(b, &queries); err != nil {
					return fmt.Errorf("%s: %w", batch, err)
				}
				if f.root != "" {
					for i := range queries {
						if !filepath.IsAbs(queries[i].File) {
							queries[i].File = filepath.Join(f.root, queries[i].File)
						}
					}
				}
			} else {
				file, line, col, err := parsePosition(args[0])
				if err != nil {
					return err
				}
				queries = []positionQuery{{File: file, Line: line, Column: col}}
			}
			answers, err := answerTypes(cmd.Context(), f, queries)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			tw := &textWriter{w: out}
			if f.format == "json" {
				if batch == "" {
					return writeJSON(out, answers[0])
				}
				return writeJSON(out, answers)
			}
			for _, a := range answers {
				tw.printf("%s:%d:%d\n", a.File, a.Line, a.Column)
				if a.Error != "" {
					tw.printf("  error: %s\n", a.Error)
					continue
				}
				tw.printf("  node:      %s (%s) %q\n", a.Kind, a.CompilerKind, a.Text)
				tw.printf("  symbol:    %s\n", orDash(a.Symbol))
				tw.printf("  qualified: %s\n", orDash(a.QualifiedName))
				tw.printf("  type:      %s\n", orDash(a.Type))
				tw.printf("  flags:     %s\n", orDash(strings.Join(a.Flags, ", ")))
				for _, d := range a.Declarations {
					pkg := ""
					if d.Package != nil {
						pkg = fmt.Sprintf(" (%s %s)", d.Package.Name, d.Package.Version)
					}
					tw.printf("  declared:  %s:%d:%d%s\n", d.Path, d.Line, d.Column, pkg)
				}
			}
			return tw.err
		},
	}
	cmd.Flags().StringVar(&batch, "batch", "", "answer every position in this JSON file")
	return cmd
}

// answerTypes loads each project the queried files belong to once and
// answers every query, in input order.
func answerTypes(ctx context.Context, f *debugFlags, queries []positionQuery) ([]typeAnswer, error) {
	answers := make([]typeAnswer, len(queries))
	byFile := map[string][]int{}
	var order []string
	for i, q := range queries {
		abs, err := filepath.Abs(q.File)
		if err != nil {
			return nil, err
		}
		key := filepath.ToSlash(abs)
		if _, ok := byFile[key]; !ok {
			order = append(order, key)
		}
		byFile[key] = append(byFile[key], i)
		answers[i].positionQuery = q
	}
	if len(order) == 0 {
		return answers, nil
	}
	root, err := f.workspaceRoot(order[0])
	if err != nil {
		return nil, err
	}
	ws, err := tsbridge.Discover(ctx, root, tsbridge.DiscoverOptions{})
	if err != nil {
		return nil, err
	}
	// Group files by project so each project loads once.
	byProject := map[string][]string{}
	var projects []tsbridge.ProjectConfig
	for _, file := range order {
		cfg, ok := projectFor(ws, file)
		if !ok {
			for _, i := range byFile[file] {
				answers[i].Error = "not a source file of any project under " + ws.Root
			}
			continue
		}
		if _, seen := byProject[cfg.Name]; !seen {
			projects = append(projects, cfg)
		}
		byProject[cfg.Name] = append(byProject[cfg.Name], file)
	}
	opts, err := f.loadAllOptions()
	if err != nil {
		return nil, err
	}
	for _, cfg := range projects {
		p, err := tsbridge.Load(ctx, cfg, opts.LoadOptions)
		for _, file := range byProject[cfg.Name] {
			for _, i := range byFile[file] {
				if err != nil {
					answers[i].Error = err.Error()
					continue
				}
				answers[i] = answerAt(p, file, queries[i])
			}
		}
		if p != nil {
			p.Close()
		}
	}
	return answers, nil
}

func answerAt(p *tsbridge.Program, file string, q positionQuery) typeAnswer {
	a := typeAnswer{positionQuery: q}
	sf := p.FileByPath(file)
	if sf == nil {
		a.Error = "file not in the loaded program"
		return a
	}
	pos, ok := sf.Offset(q.Line, q.Column)
	if !ok {
		a.Error = "position outside the file"
		return a
	}
	n := p.NodeAt(sf.ID, pos)
	a.Kind = p.Kind(n).String()
	a.CompilerKind = p.CompilerKind(n)
	a.Text = truncate(p.Text(n), 80)
	if s, ok := p.SymbolOf(n); ok {
		a.Symbol = p.SymbolName(s)
		a.QualifiedName = p.QualifiedName(s)
		// An alias whose target can't be found has no declaration to show.
		a.Declarations = p.Declarations(p.ResolveAlias(s))
	}
	if t := p.TypeOf(n); t != 0 {
		a.Type = p.TypeString(t)
		a.Flags = p.TypeFlags(t).Strings()
	}
	return a
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
