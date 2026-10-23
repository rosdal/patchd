package engine

import (
	"fmt"
	"strings"

	"github.com/patchd/patchd/engine/ir"
	"github.com/patchd/patchd/engine/rules"
	"github.com/patchd/patchd/engine/taint"
	"github.com/patchd/patchd/engine/tsbridge"
)

// build turns a kept candidate into a Finding: location, trace, message,
// severity, confidence, tags and the fingerprint before collision handling.
func (p *projectScan) build(rule *rules.Rule, c *rules.Candidate) Finding {
	r := c.Result
	f := r.Func
	origin := r.Origin(c.Taint.Label)
	file := p.prog.File(r.Module.File)
	call := f.Instr(c.Taint.Call)

	steps := p.traceSteps(r, origin, c.Path)
	sinkLoc := p.location(file, call.Source)
	sinkName := calleeText(p.prog, call)
	steps = append(steps, TraceStep{
		Location: sinkLoc,
		Kind:     StepSink,
		Note:     fmt.Sprintf("passed as argument %d to `%s`", c.Taint.Arg+1, sinkName),
		Snippet:  snippet(file, call.Source.Pos),
	})

	source := steps[0]
	sourceText := strings.TrimSuffix(strings.TrimPrefix(source.Note, "read `"), "`")
	if i := strings.Index(sourceText, "` ("); i >= 0 {
		sourceText = sourceText[:i]
	}
	env := origin.Kind == taint.KindEnvironment
	fd := Finding{
		RuleID:     rule.ID,
		Severity:   rule.Severity,
		CWE:        rule.CWE,
		Message:    rule.Message(sourceText, source.Location.StartLine, sinkName, sinkLoc.StartLine, env),
		Location:   sinkLoc,
		Trace:      steps,
		Confidence: ConfidenceHigh,
		Tags:       []string{TagIntraprocedural},
	}
	if env {
		fd.Severity = Low
		fd.Tags = append(fd.Tags, TagEnvironment)
	}
	if origin.Heuristic {
		fd.Confidence = ConfidenceMedium
		fd.Tags = append(fd.Tags, TagUntypedHandler)
	}
	if c.Taint.Label.Numeric {
		fd.Tags = append(fd.Tags, TagNumericPruned)
	}
	fd.Fingerprint = fingerprintBase(rule.ID, sinkLoc.Path, f.Name, p.prog.Text(call.Syntax), p.originText(r, origin))
	return fd
}

// rawStep is a path step with its instruction resolved.
type rawStep struct {
	v      ir.ValueID
	in     *ir.Instr // nil for a parameter source
	source bool
	text   string // for merged property-read chains: the whole access, "req.query.id"
}

// traceSteps turns a taint path into readable steps: chains of property
// reads collapse into one ("read `req.query.id`"), plain copies and phis
// disappear, and each step gets a note.
func (p *projectScan) traceSteps(r *taint.FuncResult, origin *taint.Origin, path []taint.PathStep) []TraceStep {
	f := r.Func
	raw := make([]rawStep, 0, len(path))
	for i, st := range path {
		raw = append(raw, rawStep{v: st.Value, in: f.Instr(st.Instr), source: i == 0})
	}
	// Collapse property-read chains: a read of the previous step's value
	// replaces it (keeping its source role).
	var merged []rawStep
	for _, s := range raw {
		if n := len(merged); n > 0 && isStaticLoad(s.in) && len(f.Args(s.in)) > 0 && f.Args(s.in)[0] == merged[n-1].v && (merged[n-1].in == nil || isStaticLoad(merged[n-1].in) || merged[n-1].source) {
			prev := merged[n-1]
			s.source = s.source || prev.source
			s.text = p.chainText(f, prev, s)
			merged[n-1] = s
			continue
		}
		merged = append(merged, s)
	}
	file := p.prog.File(r.Module.File)
	var out []TraceStep
	for i, s := range merged {
		if !s.source && i < len(merged)-1 && s.in != nil {
			switch s.in.Op {
			case ir.OpPhi, ir.OpAssign, ir.OpSpread:
				continue
			}
		}
		step := TraceStep{Kind: StepPropagate}
		var rng tsbridge.Range
		switch {
		case s.in != nil:
			rng = s.in.Source
		default:
			if v := f.Value(s.v); v != nil && v.Syntax != 0 {
				rng = p.prog.Range(v.Syntax)
			}
		}
		step.Location = p.location(file, rng)
		step.Snippet = snippet(file, rng.Pos)
		if s.source {
			step.Kind = StepSource
			text := p.valueText(f, s)
			step.Note = fmt.Sprintf("read `%s`", text)
			if origin.Source.Desc != "" {
				step.Note += " (" + origin.Source.Desc + ")"
			}
		} else {
			step.Note, step.Kind = p.note(r, s.in)
		}
		// Steps that say the same thing in a row ("concatenated into a
		// string" for each + of a chain) collapse into the last one.
		if n := len(out); n > 0 && out[n-1].Note == step.Note && out[n-1].Kind == step.Kind {
			out[n-1] = step
			continue
		}
		out = append(out, step)
	}
	return out
}

func isStaticLoad(in *ir.Instr) bool {
	return in != nil && in.Op == ir.OpLoad && in.Aux.Flags&(ir.AuxDynamic|ir.AuxGlobal|ir.AuxCell|ir.AuxSuper|ir.AuxIterKey|ir.AuxIterValue|ir.AuxRest) == 0
}

// note describes one propagation step.
func (p *projectScan) note(r *taint.FuncResult, in *ir.Instr) (string, StepKind) {
	if in == nil {
		return "flows on", StepPropagate
	}
	f := r.Func
	switch in.Op {
	case ir.OpLoad:
		switch {
		case in.Aux.Flags.Has(ir.AuxGlobal), in.Aux.Flags.Has(ir.AuxCell):
			name := in.Aux.Name.String()
			if v := f.Value(in.Result); v != nil && v.Name != "" {
				name = v.Name
			}
			if name == "" {
				return "read from a captured variable", StepPropagate
			}
			return fmt.Sprintf("read variable `%s`", name), StepPropagate
		case in.Aux.Flags.Has(ir.AuxIterValue):
			return "read an element in a for…of loop", StepPropagate
		case in.Aux.Flags.Has(ir.AuxIterKey):
			return "read a key in a for…in loop", StepPropagate
		}
		return fmt.Sprintf("read `%s`", shorten(p.prog.Text(in.Syntax))), StepPropagate
	case ir.OpBinOp:
		return "concatenated into a string", StepPropagate
	case ir.OpTemplate:
		return "inserted into a template literal", StepPropagate
	case ir.OpStore:
		lhs := p.prog.Text(in.Syntax)
		if i := strings.Index(lhs, "="); i > 0 {
			lhs = strings.TrimSpace(lhs[:i])
		}
		return fmt.Sprintf("stored in `%s`", shorten(lhs)), StepPropagate
	case ir.OpCall, ir.OpNew:
		name := calleeText(p.prog, in)
		if qn := r.CallName(in.ID); qn != "" {
			if san := p.spec.SanitizersFor(qn); len(san) > 0 {
				return fmt.Sprintf("passed through `%s`, which clears %s only", name, kindList(san[0].Clears)), StepSanitizer
			}
		}
		return fmt.Sprintf("passed through `%s`", name), StepPropagate
	case ir.OpAwait:
		return "awaited", StepPropagate
	case ir.OpObject:
		return "put in an object literal", StepPropagate
	case ir.OpArray:
		return "put in an array", StepPropagate
	case ir.OpUnknown:
		return "used by syntax the engine does not model", StepPropagate
	}
	return "flows on", StepPropagate
}

func kindList(ks []taint.Kind) string {
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = string(k)
	}
	return strings.Join(parts, ", ")
}

// chainText names a property read of prev's value: the read's own text when
// it spells out the chain (req.query.id), else prev's text and the property
// (destructuring: const { id } = req.query).
func (p *projectScan) chainText(f *ir.Func, prev, s rawStep) string {
	base := p.valueText(f, prev)
	own := shorten(p.prog.Text(s.in.Syntax))
	if strings.HasPrefix(own, base) {
		return own
	}
	return base + "." + s.in.Aux.Name.String()
}

// valueText is the source text a step's value stands for.
func (p *projectScan) valueText(f *ir.Func, s rawStep) string {
	if s.text != "" {
		return s.text
	}
	if s.in != nil && s.in.Syntax != 0 {
		return shorten(p.prog.Text(s.in.Syntax))
	}
	if v := f.Value(s.v); v != nil {
		if v.Name != "" {
			return v.Name
		}
		if v.Syntax != 0 {
			return shorten(p.prog.Text(v.Syntax))
		}
	}
	return "input"
}

// originText is the source access as written, for fingerprints.
func (p *projectScan) originText(r *taint.FuncResult, o *taint.Origin) string {
	if in := r.Func.Instr(o.Instr); in != nil {
		return p.prog.Text(in.Syntax)
	}
	if v := r.Func.Value(o.Value); v != nil {
		return v.Name
	}
	return o.QualifiedName
}

// calleeText is how a call names what it calls: "pool.query", "exec",
// "new URL".
func calleeText(prog *tsbridge.Program, in *ir.Instr) string {
	text := prog.Text(in.Syntax)
	if i := strings.IndexAny(text, "(`"); i > 0 {
		text = text[:i]
	}
	text = strings.Join(strings.Fields(text), "")
	text = strings.TrimSuffix(text, "?.")
	// In a chain (a.split(".").slice(-1)) the text before the first
	// parenthesis is an earlier call; name the method itself.
	if name := in.Aux.Name.String(); in.Op == ir.OpCall && in.Aux.Flags.Has(ir.AuxMethod) && name != "" && !strings.HasSuffix(text, name) {
		return "." + name + "()"
	}
	if in.Op == ir.OpNew {
		text = "new " + strings.TrimPrefix(text, "new")
	}
	if text == "" || len(text) > 60 {
		if n := in.Aux.Name.String(); n != "" {
			return n
		}
		return "a call"
	}
	return text
}

// shorten collapses whitespace and cuts long text.
func shorten(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 80 {
		return string(r[:79]) + "…"
	}
	return s
}

// location converts a range in a file to a Location relative to the scanned
// root.
func (p *projectScan) location(file *tsbridge.File, rng tsbridge.Range) Location {
	if file == nil {
		return Location{}
	}
	sl, sc := file.Position(rng.Pos)
	el, ec := file.Position(rng.End)
	return Location{Path: relPath(p.ws.Root, file.Path), StartLine: sl, StartColumn: sc, EndLine: el, EndColumn: ec}
}

func relPath(root, path string) string {
	root = strings.TrimSuffix(root, "/")
	if rel, ok := strings.CutPrefix(path, root+"/"); ok {
		return rel
	}
	return path
}

// snippet returns the trimmed source line containing pos.
func snippet(file *tsbridge.File, pos tsbridge.Pos) string {
	if file == nil || int(pos) > len(file.Text) || pos < 0 {
		return ""
	}
	start := strings.LastIndexByte(file.Text[:pos], '\n') + 1
	end := strings.IndexByte(file.Text[pos:], '\n')
	if end < 0 {
		end = len(file.Text)
	} else {
		end += int(pos)
	}
	line := strings.TrimSpace(strings.TrimRight(file.Text[start:end], "\r"))
	if r := []rune(line); len(r) > 200 {
		line = string(r[:199]) + "…"
	}
	return line
}
