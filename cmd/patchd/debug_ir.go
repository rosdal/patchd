package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/patchd/patchd/engine/ir"
	"github.com/patchd/patchd/engine/tsbridge"
)

type irFlags struct {
	types, source, names, dot, noVerify bool
}

func newDebugIRCmd(f *debugFlags) *cobra.Command {
	fl := &irFlags{}
	cmd := &cobra.Command{
		Use:   "ir <file>[:<function>]",
		Short: "Print a file's Patchd IR, or one function's",
		Long: "Print the Patchd IR of a file: its imports, exports and every function. With :<function>, print only\n" +
			"the functions whose name is or ends with <function> (handler, UserService.find) or whose ID it is (f3).\n" +
			"The IR is validated; violations go to stderr.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, fn := splitFileFunc(args[0])
			p, sf, err := f.loadFile(cmd.Context(), file)
			if err != nil {
				return err
			}
			defer p.Close()
			m, _ := ir.BuildModule(p, sf.ID)
			if m == nil {
				return fmt.Errorf("%s: could not be lowered", file)
			}
			funcs := m.Funcs
			if fn != "" {
				funcs = matchFuncs(m, fn)
				if len(funcs) == 0 {
					return fmt.Errorf("no function %q in %s", fn, file)
				}
			}
			errw := &textWriter{w: cmd.ErrOrStderr()}
			if !fl.noVerify {
				for _, g := range funcs {
					if err := ir.Validate(g); err != nil {
						errw.printf("invalid IR in %s:\n%v\n", g.Name, err)
					}
				}
			}
			w := bufio.NewWriter(cmd.OutOrStdout())
			if fl.dot {
				for _, g := range funcs {
					if err := ir.WriteDot(w, m, g); err != nil {
						return err
					}
				}
				return w.Flush()
			}
			opts := ir.PrintOptions{Types: fl.types, Source: fl.source, Names: fl.names, Prog: p, Root: p.Config().Root}
			if fn == "" {
				if err := ir.Print(w, m, opts); err != nil {
					return err
				}
			} else {
				for i, g := range funcs {
					if i > 0 {
						_ = w.WriteByte('\n') // bufio keeps the first error for Flush
					}
					if err := ir.PrintFunc(w, m, g, opts); err != nil {
						return err
					}
				}
			}
			for _, warn := range m.Warnings {
				errw.printf("%s %s\n", warn.Code, warn.Message)
			}
			if err := w.Flush(); err != nil {
				return err
			}
			return errw.err
		},
	}
	cmd.Flags().BoolVar(&fl.types, "types", false, "show each value's TypeScript type")
	cmd.Flags().BoolVar(&fl.source, "source", false, "show the source line before the instructions it produced")
	cmd.Flags().BoolVar(&fl.names, "names", false, "show source variable names and destructuring paths")
	cmd.Flags().BoolVar(&fl.dot, "dot", false, "print each function's control-flow graph in Graphviz dot format")
	cmd.Flags().BoolVar(&fl.noVerify, "no-verify", false, "skip validating the IR")
	return cmd
}

// splitFileFunc splits <file>[:<function>]. A colon that is part of the
// path (C:\…) or of an existing file name is not a separator.
func splitFileFunc(s string) (file, fn string) {
	if _, err := os.Stat(s); err == nil {
		return s, ""
	}
	i := strings.LastIndex(s, ":")
	if i <= 1 { // no colon, or a drive letter
		return s, ""
	}
	return s[:i], s[i+1:]
}

func matchFuncs(m *ir.Module, name string) []*ir.Func {
	var out []*ir.Func
	for _, f := range m.Funcs {
		id := "f" + strconv.Itoa(int(f.ID))
		if f.Name == name || id == name || strings.HasSuffix(f.Name, "."+name) {
			out = append(out, f)
		}
	}
	return out
}

type irCoverageReport struct {
	Root      string         `json:"root"`
	Projects  int            `json:"projects"`
	Failed    int            `json:"failed_projects"`
	Files     int            `json:"files"`
	Lines     int            `json:"lines"`
	Funcs     int            `json:"funcs"`
	Instrs    int            `json:"instrs"`
	Coverage  ir.Coverage    `json:"coverage"`
	Percent   float64        `json:"percent"`
	Top       []kindReport   `json:"top_unsupported"`
	Verified  int            `json:"verified_funcs,omitempty"`
	Invalid   int            `json:"invalid_funcs"`
	Failures  []string       `json:"verify_failures,omitempty"`
	Warnings  map[string]int `json:"warnings"`
	LoadMS    float64        `json:"load_ms"`  // discover, parse, bind and check, wall
	LowerMS   float64        `json:"lower_ms"` // lowering, summed over projects
	VerifyMS  float64        `json:"verify_ms"`
	WallMS    float64        `json:"wall_ms"`
	PeakHeap  uint64         `json:"peak_heap_bytes"`
	IRBytes   uint64         `json:"ir_bytes"` // heap growth while the IR was alive, summed over projects
	SrcBytes  int            `json:"source_bytes"`
	Threshold float64        `json:"threshold,omitempty"`
}

type kindReport struct {
	Kind     string   `json:"kind"`
	Unknown  int      `json:"unknown"`
	Skipped  int      `json:"skipped"`
	Examples []string `json:"examples"`
}

func newDebugIRCoverageCmd(f *debugFlags) *cobra.Command {
	var verify bool
	var minPercent float64
	cmd := &cobra.Command{
		Use:   "ir-coverage [workspace-root]",
		Short: "Lower a workspace to IR and report how much syntax lowered to real instructions",
		Long: "Lower every source file of every project in a workspace and count, per syntax kind, the nodes\n" +
			"that lowered to real instructions versus unknown (or never reached). Prints the overall\n" +
			"percentage and the top 20 unsupported kinds with example locations. With --verify, also\n" +
			"validates every function. With --min, exits non-zero when coverage is below it or any\n" +
			"function fails validation.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := f.validate(); err != nil {
				return err
			}
			start := time.Now()
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			if f.root != "" {
				root = f.root
			}
			ctx := cmd.Context()
			ws, err := tsbridge.Discover(ctx, root, tsbridge.DiscoverOptions{})
			if err != nil {
				return err
			}
			opts, err := f.loadAllOptions()
			if err != nil {
				return err
			}
			rep := irCoverageReport{Root: ws.Root, Warnings: map[string]int{}, Threshold: minPercent}
			var mu sync.Mutex
			var loadTime time.Duration
			peak := tsbridge.PeakHeap(func() {
				_, err = tsbridge.LoadAll(ctx, ws, opts, func(_ tsbridge.ProjectConfig, p *tsbridge.Program, lerr error) error {
					mu.Lock()
					rep.Projects++
					if lerr != nil {
						rep.Failed++
						mu.Unlock()
						return nil
					}
					mu.Unlock()
					st := p.Stats()
					before := heapInUse()
					t0 := time.Now()
					mods := ir.BuildProgram(ctx, p, ir.BuildOptions{})
					lower := time.Since(t0)
					after := heapInUse()
					irBytes := after - min(before, after)
					var cov ir.Coverage
					var funcs, instrs, invalid, verified, srcBytes int
					var failures []string
					warns := map[string]int{}
					t1 := time.Now()
					for _, m := range mods {
						if m == nil {
							continue
						}
						cov.Add(m.Coverage)
						if sf := p.File(m.File); sf != nil {
							srcBytes += len(sf.Text)
						}
						for _, w := range m.Warnings {
							warns[w.Code]++
						}
						for _, g := range m.Funcs {
							funcs++
							instrs += g.NumInstrs()
							if !verify {
								continue
							}
							verified++
							if err := ir.Validate(g); err != nil {
								invalid++
								if len(failures) < 20 {
									first := strings.SplitN(err.Error(), "\n", 2)[0]
									failures = append(failures, fmt.Sprintf("%s %s: %s", m.Path, g.Name, first))
								}
							}
						}
						if verify {
							if err := ir.ValidateClosures(m); err != nil {
								invalid++
								if len(failures) < 20 {
									failures = append(failures, fmt.Sprintf("%s: %s", m.Path, strings.SplitN(err.Error(), "\n", 2)[0]))
								}
							}
						}
					}
					verifyTime := time.Since(t1)
					mu.Lock()
					defer mu.Unlock()
					rep.Files += len(mods)
					rep.Lines += st.Lines
					rep.Funcs += funcs
					rep.Instrs += instrs
					rep.Coverage.Add(cov)
					rep.Invalid += invalid
					rep.Verified += verified
					rep.SrcBytes += srcBytes
					rep.IRBytes += irBytes
					for _, fl := range failures {
						if len(rep.Failures) < 20 {
							rep.Failures = append(rep.Failures, fl)
						}
					}
					for k, n := range warns {
						rep.Warnings[k] += n
					}
					rep.LowerMS += float64(lower.Microseconds()) / 1000
					rep.VerifyMS += float64(verifyTime.Microseconds()) / 1000
					loadTime += st.Parse + st.Bind + st.Check
					return nil
				})
			})
			if err != nil {
				return err
			}
			rep.PeakHeap = peak
			rep.LoadMS = float64(loadTime.Microseconds()) / 1000
			rep.WallMS = float64(time.Since(start).Microseconds()) / 1000
			rep.Percent = rep.Coverage.Percent()
			for i, k := range rep.Coverage.Missing() {
				if i >= 20 {
					break
				}
				kc := rep.Coverage.Kinds[k]
				ex := slices.Clone(kc.Examples)
				for j := range ex {
					ex[j] = relPath(ws.Root, ex[j])
				}
				rep.Top = append(rep.Top, kindReport{Kind: k, Unknown: kc.Unknown, Skipped: kc.Skipped, Examples: ex})
			}
			out := cmd.OutOrStdout()
			if f.format == "json" {
				if err := writeJSON(out, rep); err != nil {
					return err
				}
			} else if err := printIRCoverage(out, rep, verify); err != nil {
				return err
			}
			if minPercent > 0 {
				if rep.Percent < minPercent {
					return fmt.Errorf("coverage %.2f%% is below %.2f%%", rep.Percent, minPercent)
				}
				if rep.Invalid > 0 {
					return fmt.Errorf("%d function(s) failed validation", rep.Invalid)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&verify, "verify", false, "validate every function's IR")
	cmd.Flags().Float64Var(&minPercent, "min", 0, "fail when coverage is below this percentage or any function is invalid")
	return cmd
}

func relPath(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return p
}

func printIRCoverage(w io.Writer, rep irCoverageReport, verify bool) error {
	tw := &textWriter{w: w}
	c := rep.Coverage
	tw.printf("root: %s\n", rep.Root)
	tw.printf("%d project(s) (%d failed), %d files, %d lines, %d functions, %d instructions\n",
		rep.Projects, rep.Failed, rep.Files, rep.Lines, rep.Funcs, rep.Instrs)
	tw.printf("coverage: %.2f%% of %d syntax nodes lowered (%d unknown, %d never reached)\n", c.Percent(), c.Nodes, c.Unknown, c.Skipped)
	if verify {
		tw.printf("verify: %d of %d functions valid\n", rep.Verified-rep.Invalid, rep.Verified)
		for _, fl := range rep.Failures {
			tw.printf("  %s\n", fl)
		}
	}
	if len(rep.Top) > 0 {
		tw.println("top unsupported kinds:")
		for _, k := range rep.Top {
			tw.printf("  %-36s %6d unknown %6d never reached   %s\n", k.Kind, k.Unknown, k.Skipped, strings.Join(k.Examples, ", "))
		}
	}
	if len(rep.Warnings) > 0 {
		var codes []string
		for k := range rep.Warnings {
			codes = append(codes, k)
		}
		slices.Sort(codes)
		var parts []string
		for _, k := range codes {
			parts = append(parts, fmt.Sprintf("%s ×%d", k, rep.Warnings[k]))
		}
		tw.printf("warnings: %s\n", strings.Join(parts, ", "))
	}
	tw.printf("time: load %s, lower %s, verify %s, wall %s; peak heap %s; IR ~%s (%.1f bytes per source byte)\n",
		msf(rep.LoadMS), msf(rep.LowerMS), msf(rep.VerifyMS), msf(rep.WallMS), formatBytes(rep.PeakHeap), formatBytes(rep.IRBytes),
		float64(rep.IRBytes)/float64(max(1, rep.SrcBytes)))
	if tw.err != nil {
		return tw.err
	}
	return nil
}

func msf(v float64) string { return ms(time.Duration(v * float64(time.Millisecond))) }

// heapInUse returns the bytes of live heap, after a collection.
func heapInUse() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}
