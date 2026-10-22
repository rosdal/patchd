package ir

import (
	"fmt"
	"io"
	"strings"
)

// WriteDot writes a function's control-flow graph in Graphviz dot format:
// one node per block listing its instructions, solid edges for normal
// successors (branches labelled T and F) and dashed edges to handlers.
func WriteDot(out io.Writer, m *Module, f *Func) error {
	var sb, w strings.Builder
	p := &printer{w: &sb, m: m, f: f}
	fmt.Fprintf(&w, "digraph %q {\n", fmt.Sprintf("f%d %s", f.ID, f.Name))
	w.WriteString("  node [shape=box fontname=\"monospace\" fontsize=10];\n")
	for b := 1; b < len(f.Blocks); b++ {
		blk := &f.Blocks[b]
		sb.Reset()
		for _, id := range blk.Instrs {
			p.instr(&f.Instrs[id], blk)
		}
		label := p.blockName(blk.ID) + `:\l` + strings.ReplaceAll(dotEscape(sb.String()), "\n", `\l`)
		style := ""
		if blk.Dead {
			style = " style=dashed"
		}
		fmt.Fprintf(&w, "  b%d [label=\"%s\"%s];\n", blk.ID, label, style)
		for i, s := range blk.Succs {
			attr := ""
			if len(blk.Succs) == 2 {
				attr = []string{` [label="T"]`, ` [label="F"]`}[i]
			}
			fmt.Fprintf(&w, "  b%d -> b%d%s;\n", blk.ID, s, attr)
		}
		if blk.Handler != NoBlock {
			fmt.Fprintf(&w, "  b%d -> b%d [style=dashed label=\"exception\"];\n", blk.ID, blk.Handler)
		}
	}
	w.WriteString("}\n")
	_, err := io.WriteString(out, w.String())
	return err
}

func dotEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}
