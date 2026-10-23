package ir

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/patchd/patchd/engine/tsbridge"
)

// PrintOptions choose the annotations Print adds.
type PrintOptions struct {
	Types  bool              // the TypeScript type after each value
	Source bool              // the source line before the instructions it produced
	Names  bool              // source variable names of values
	Prog   *tsbridge.Program // needed for Types
	Root   string            // paths are printed relative to this directory, if set
}

// Print writes a module's text form: imports, exports, then every function.
// The output is deterministic: the same input always prints the same text.
func Print(w io.Writer, m *Module, opts PrintOptions) error {
	p := &printer{w: w, m: m, opts: opts}
	p.module()
	return p.err
}

// PrintFunc writes one function.
func PrintFunc(w io.Writer, m *Module, f *Func, opts PrintOptions) error {
	p := &printer{w: w, m: m, opts: opts}
	p.fn(f)
	return p.err
}

// String returns the module's text form with no annotations.
func (m *Module) String() string {
	var sb strings.Builder
	_ = Print(&sb, m, PrintOptions{}) // a strings.Builder never fails
	return sb.String()
}

type printer struct {
	w     io.Writer
	m     *Module
	opts  PrintOptions
	f     *Func
	file  *tsbridge.File
	lines []string
	err   error // the first write error
}

func (p *printer) printf(format string, args ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format, args...)
	}
}

func (p *printer) path(s string) string {
	if p.opts.Root != "" {
		root := strings.TrimSuffix(strings.ReplaceAll(p.opts.Root, "\\", "/"), "/") + "/"
		if strings.HasPrefix(s, root) {
			return s[len(root):]
		}
	}
	return s
}

func (p *printer) module() {
	m := p.m
	p.printf("module %s\n", p.path(m.Path))
	for _, imp := range m.Imports {
		p.printf("  import %s\n", p.importText(imp))
	}
	for _, e := range m.Exports {
		p.printf("  export %s\n", p.exportText(e))
	}
	for _, f := range m.Funcs {
		p.printf("\n")
		p.fn(f)
	}
}

func (p *printer) importText(imp Import) string {
	var sb strings.Builder
	switch {
	case imp.Imported == "":
		sb.WriteString("(side effect)")
	case imp.Local != "" && imp.Local != imp.Imported:
		fmt.Fprintf(&sb, "%s as %s", imp.Imported, imp.Local)
	default:
		sb.WriteString(imp.Imported)
	}
	fmt.Fprintf(&sb, " from %q", imp.Specifier)
	kinds := [...]string{"esm", "require", "dynamic", "import=", "re-export"}
	if int(imp.Kind) < len(kinds) && imp.Kind != ImportESM {
		fmt.Fprintf(&sb, " (%s)", kinds[imp.Kind])
	}
	if imp.TypeOnly {
		sb.WriteString(" (type)")
	}
	if imp.Resolved {
		switch {
		case imp.Target.Package != nil:
			fmt.Fprintf(&sb, " → package %s", imp.Target.Package.Name)
		case imp.Target.Ambient != "":
			fmt.Fprintf(&sb, " → declare module %q", imp.Target.Ambient)
		case strings.HasPrefix(imp.Target.Path, "bundled:///"):
			fmt.Fprintf(&sb, " → %s", imp.Target.Path)
		default:
			fmt.Fprintf(&sb, " → %s", p.relTo(imp.Target.Path))
		}
	}
	return sb.String()
}

// relTo prints a resolved path relative to the module's directory.
func (p *printer) relTo(target string) string {
	dir := p.m.Path[:strings.LastIndex(p.m.Path, "/")+1]
	if strings.HasPrefix(target, dir) {
		return "./" + target[len(dir):]
	}
	return p.path(target)
}

func (p *printer) exportText(e Export) string {
	kinds := [...]string{"declaration", "named", "default", "re-export", "export=", "commonjs"}
	var sb strings.Builder
	sb.WriteString(e.Name)
	switch {
	case e.Import > 0 && e.Import <= len(p.m.Imports):
		imp := p.m.Imports[e.Import-1]
		fmt.Fprintf(&sb, " = %s from %q", imp.Imported, imp.Specifier)
	case e.Local != "" && e.Local != e.Name:
		fmt.Fprintf(&sb, " = %s", e.Local)
	}
	if e.Value != NoValue {
		fmt.Fprintf(&sb, " = v%d", e.Value)
	}
	fmt.Fprintf(&sb, " (%s)", kinds[e.Kind])
	return sb.String()
}

func (p *printer) fn(f *Func) {
	p.f = f
	p.file = nil
	if p.opts.Prog != nil {
		p.file = p.opts.Prog.File(p.m.File)
	}
	if p.opts.Source && p.file != nil && p.lines == nil {
		p.lines = strings.Split(p.file.Text, "\n")
	}
	var params []string
	for i, v := range f.Params {
		name := f.Values[v].Name
		if name == "" {
			name = "v" + strconv.Itoa(int(v))
		}
		if i == len(f.Params)-1 && f.Flags&FuncRest != 0 {
			name = "..." + name
		}
		params = append(params, name)
	}
	p.printf("func f%d %s(%s) %s", f.ID, f.Name, strings.Join(params, ", "), f.Kind)
	for i, n := range funcFlagNames {
		if f.Flags&(1<<i) != 0 && n != "rest" {
			p.printf(" %s", n)
		}
	}
	if f.Parent != NoFunc {
		p.printf(" in f%d", f.Parent)
	}
	if p.file != nil {
		line, col := p.file.Position(f.Source.Pos)
		p.printf("  ; %s:%d:%d", p.path(p.file.Path), line, col)
	}
	if p.opts.Types && f.Type != 0 {
		p.printf("  : %s", p.typeString(f.Type))
	}
	p.printf("\n")
	lastLine := -1
	for b := 1; b < len(f.Blocks); b++ {
		blk := &f.Blocks[b]
		p.printf("%s:", p.blockName(blk.ID))
		var notes []string
		if len(blk.Preds) > 0 {
			var ps []string
			for _, pr := range blk.Preds {
				ps = append(ps, "b"+strconv.Itoa(int(pr)))
			}
			notes = append(notes, "preds "+strings.Join(ps, " "))
		}
		if blk.Dead || (blk.ID != f.Entry && len(blk.Preds) == 0) {
			notes = append(notes, "unreachable")
		}
		if len(notes) > 0 {
			p.printf("  ; %s", strings.Join(notes, ", "))
		}
		p.printf("\n")
		if blk.ID == f.Entry {
			p.entryValues()
		}
		for _, id := range blk.Instrs {
			in := &f.Instrs[id]
			if p.opts.Source && p.lines != nil && p.file != nil && in.Syntax != 0 {
				line, _ := p.file.Position(in.Source.Pos)
				if line != lastLine && line-1 < len(p.lines) {
					p.printf("    ; %d: %s\n", line, strings.TrimSpace(p.lines[line-1]))
					lastLine = line
				}
			}
			p.instr(in, blk)
		}
	}
}

// entryValues prints the function-level values at the top of the entry block.
func (p *printer) entryValues() {
	f := p.f
	for v := 1; v < len(f.Values); v++ {
		val := &f.Values[v]
		var text string
		switch val.Kind {
		case ValueParam:
			text = "param " + val.Name
		case ValueThis:
			text = "this"
		case ValueArguments:
			text = "arguments"
		case ValueNewTarget:
			text = "new.target"
		case ValueSelf:
			text = "self " + val.Name
		case ValueFreeVar:
			text = "free " + val.Name
			for _, fv := range f.FreeVars {
				if fv.Value == val.ID && fv.Kind == FreeCell {
					text += " (cell)"
				}
			}
		default:
			continue
		}
		p.line(fmt.Sprintf("v%d = %s", v, text), val.ID, "")
	}
}

func (p *printer) blockName(b BlockID) string {
	if c := p.f.Blocks[b].Comment; c != "" {
		return "b" + strconv.Itoa(int(b)) + "." + c
	}
	return "b" + strconv.Itoa(int(b))
}

// line prints one instruction line with its annotations.
func (p *printer) line(text string, result ValueID, note string) {
	var ann []string
	if result != NoValue {
		val := &p.f.Values[result]
		if p.opts.Names && val.Name != "" && val.Kind == ValueInstr {
			ann = append(ann, val.Name)
		}
		if path, ok := p.f.PathOf(result); ok && p.opts.Names {
			ann = append(ann, fmt.Sprintf("path v%d%s", path.Base, path.Path))
		}
	}
	if note != "" {
		ann = append(ann, note)
	}
	typ := ""
	if p.opts.Types && result != NoValue {
		if t := p.f.Values[result].Type; t != 0 {
			typ = p.typeString(t)
		}
	}
	out := "  " + text
	if typ != "" {
		out = fmt.Sprintf("%-40s : %s", out, typ)
	}
	if len(ann) > 0 {
		if typ == "" {
			out = fmt.Sprintf("%-40s", out)
		}
		out += "  ; " + strings.Join(ann, ", ")
	}
	p.printf("%s\n", strings.TrimRight(out, " "))
}

func (p *printer) typeString(t tsbridge.TypeID) string {
	if p.opts.Prog == nil {
		return ""
	}
	s := p.opts.Prog.TypeString(t)
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
}

// val prints a value reference.
func (p *printer) val(v ValueID) string {
	if v == NoValue {
		return "<hole>"
	}
	val := p.f.Value(v)
	if val == nil {
		return "v?" + strconv.Itoa(int(v))
	}
	switch val.Kind {
	case ValueUndefined:
		return "undefined"
	case ValueGlobal:
		return "global " + val.Name
	case ValueIntrinsic:
		return val.Name
	}
	return "v" + strconv.Itoa(int(v))
}

// arg prints an argument, with ... for spread values.
func (p *printer) arg(v ValueID) string {
	if val := p.f.Value(v); val != nil && val.Kind == ValueInstr && p.f.Instrs[val.Def].Op == OpSpread {
		return "..." + p.val(v)
	}
	return p.val(v)
}

func (p *printer) args(vs []ValueID) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = p.arg(v)
	}
	return strings.Join(out, ", ")
}

// key prints a static property name as an access: .name or ["odd key"].
func key(name string) string {
	if isIdentName(name) {
		return "." + name
	}
	if _, err := strconv.ParseFloat(name, 64); err == nil {
		return "[" + name + "]"
	}
	return "[" + strconv.Quote(name) + "]"
}

// noPrinter marks an op the printer has no case for; TestOps checks no op
// prints it.
const noPrinter = "<no printer> "

// flagNote lists the flags of an instruction worth showing at the end of
// its line.
func flagNote(in *Instr, show AuxFlags) string {
	var out []string
	for i, n := range auxFlagNames {
		if f := AuxFlags(1) << (i + 3); in.Aux.Flags&f != 0 && show&f != 0 {
			out = append(out, n)
		}
	}
	return strings.Join(out, " ")
}

func (p *printer) instr(in *Instr, blk *Block) {
	f := p.f
	args := f.Args(in)
	a := func(i int) string {
		if i < len(args) {
			return p.val(args[i])
		}
		return "?"
	}
	res := ""
	if in.Result != NoValue {
		res = "v" + strconv.Itoa(int(in.Result)) + " = "
	}
	name := in.Aux.Name.String()
	var text, note string
	switch in.Op {
	case OpConst:
		text = res + "const " + p.literal(in)
	case OpAssign:
		text = res + a(0)
	case OpBinOp:
		text = fmt.Sprintf("%sbinop %s %s %s", res, name, a(0), a(1))
	case OpUnOp:
		text = fmt.Sprintf("%sunop %s %s", res, name, a(0))
	case OpTemplate:
		text = res + "template " + p.templateText(in, args)
	case OpLoad:
		text = res + "load " + p.access(in, args, false)
	case OpStore:
		text = "store " + p.access(in, args, true)
		note = flagNote(in, AuxMethod|AuxGetter|AuxSetter|AuxStatic|AuxProto)
	case OpDelete:
		text = res + "delete " + p.access(in, args, false)
	case OpCall:
		text = res + "call " + p.callText(in, args)
		note = flagNote(in, AuxOptional|AuxTagged|AuxJSX|AuxRequire|AuxImport|AuxEval|AuxIIFE)
		if in.Aux.Flags&AuxDecorator != 0 {
			parts := f.Parts(in)
			target := ""
			if len(parts) > 0 {
				target = " " + parts[0].Name.String()
			}
			note = "decorator @" + name + target
		}
	case OpNew:
		text = fmt.Sprintf("%snew %s(%s)", res, a(0), p.args(args[min(1, len(args)):]))
	case OpObject:
		text = res + "object " + p.objectText(in, args)
	case OpArray:
		text = res + "array [" + p.args(args) + "]"
	case OpClosure:
		target := p.m.Func(in.Aux.Func)
		text = fmt.Sprintf("%sclosure f%d [%s]", res, in.Aux.Func, p.args(args))
		if target != nil {
			note = target.Name
		}
		if fl := flagNote(in, AuxClass|AuxDerived|AuxGetter|AuxSetter); fl != "" {
			note = strings.TrimSpace(note + " " + fl)
		}
	case OpCell:
		text = res + "cell " + name
	case OpSpread:
		text = res + "spread " + a(0)
	case OpAwait:
		text = res + "await " + a(0)
	case OpYield:
		if in.Aux.Flags&AuxDelegate != 0 {
			text = res + "yield* " + a(0)
		} else {
			text = res + "yield " + a(0)
		}
	case OpPhi:
		ops := make([]string, len(args))
		for i, v := range args {
			pred := "b?"
			if i < len(blk.Preds) {
				pred = "b" + strconv.Itoa(int(blk.Preds[i]))
			}
			ops[i] = pred + ": " + p.val(v)
		}
		text = res + "phi [" + strings.Join(ops, ", ") + "]"
	case OpCatchVal:
		text = res + "catchval"
	case OpUnknown:
		text = res + "unknown " + name
		if len(args) > 0 {
			text += " [" + p.args(args) + "]"
		}
	case OpJump:
		text = "jump " + p.succ(blk, 0)
	case OpBranch:
		cond := "*"
		if len(args) > 0 {
			cond = a(0)
		}
		text = fmt.Sprintf("branch %s %s %s", cond, p.succ(blk, 0), p.succ(blk, 1))
	case OpReturn:
		text = "return " + a(0)
	case OpThrow:
		text = "throw " + a(0)
	case OpUnreachable:
		text = "unreachable"
	default:
		text = res + noPrinter + in.Op.String()
	}
	if blk.Handler != NoBlock && in.Op.CanThrow() {
		exc := "exceptional → " + p.blockName(blk.Handler)
		if note != "" {
			note += ", " + exc
		} else {
			note = exc
		}
	}
	p.line(text, in.Result, note)
}

func (p *printer) succ(blk *Block, i int) string {
	if i < len(blk.Succs) {
		return p.blockName(blk.Succs[i])
	}
	return "b?"
}

func (p *printer) literal(in *Instr) string {
	name := in.Aux.Name.String()
	switch in.Aux.Flags.Lit() {
	case LitString:
		return strconv.Quote(name)
	case LitStrings:
		parts := p.f.Parts(in)
		out := make([]string, len(parts))
		for i, pt := range parts {
			out[i] = strconv.Quote(pt.Name.String())
		}
		return "strings [" + strings.Join(out, ", ") + "]"
	}
	return name
}

func (p *printer) templateText(in *Instr, args []ValueID) string {
	parts := p.f.Parts(in)
	var out []string
	for i, pt := range parts {
		if s := pt.Name.String(); s != "" {
			out = append(out, strconv.Quote(s))
		}
		if i < len(args) {
			out = append(out, p.val(args[i]))
		}
	}
	return "[" + strings.Join(out, ", ") + "]"
}

// access prints the place a load, store or delete touches, and for a store
// the value written.
func (p *printer) access(in *Instr, args []ValueID, store bool) string {
	fl := in.Aux.Flags
	name := in.Aux.Name.String()
	v := func(i int) string {
		if i < len(args) {
			return p.val(args[i])
		}
		return "?"
	}
	var place string
	valueAt := 1
	switch {
	case fl&AuxGlobal != 0:
		place = "global " + name
	case fl&AuxCell != 0:
		place = "*" + v(0)
	case fl&AuxSuper != 0:
		place = "super" + key(name) + " this=" + v(0)
	case fl&AuxIterValue != 0:
		place = v(0) + "[of]"
	case fl&AuxIterKey != 0:
		place = v(0) + "[in]"
	case fl&AuxRest != 0:
		place = v(0) + "[" + name + ":]"
	case fl&AuxDynamic != 0:
		place = v(0) + "[" + v(1) + "]"
		valueAt = 2
	default:
		place = v(0) + key(name)
	}
	if store {
		return place + " = " + v(valueAt)
	}
	return place
}

func (p *printer) callText(in *Instr, args []ValueID) string {
	fl := in.Aux.Flags
	name := in.Aux.Name.String()
	v := func(i int) string {
		if i < len(args) {
			return p.val(args[i])
		}
		return "?"
	}
	rest := func(from int) string {
		if from >= len(args) {
			return ""
		}
		return p.args(args[from:])
	}
	switch {
	case fl&AuxSuperCall != 0:
		return fmt.Sprintf("super(%s) this=%s", rest(1), v(0))
	case fl&AuxMethod != 0 && fl&AuxSuper != 0:
		return fmt.Sprintf("super%s(%s) this=%s", key(name), rest(1), v(0))
	case fl&AuxMethod != 0 && fl&AuxDynamic != 0:
		return fmt.Sprintf("%s[%s](%s) this=%s", v(0), v(1), rest(2), v(0))
	case fl&AuxMethod != 0:
		return fmt.Sprintf("%s%s(%s) this=%s", v(0), key(name), rest(1), v(0))
	case fl&AuxWithThis != 0:
		return fmt.Sprintf("%s(%s) this=%s", v(0), rest(2), v(1))
	}
	return fmt.Sprintf("%s(%s)", v(0), rest(1))
}

func (p *printer) objectText(in *Instr, args []ValueID) string {
	parts := p.f.Parts(in)
	var out []string
	ai := 0
	next := func() string {
		if ai < len(args) {
			ai++
			return p.val(args[ai-1])
		}
		return "?"
	}
	for _, pt := range parts {
		name := pt.Name.String()
		keyText := name
		if !isIdentName(name) {
			keyText = strconv.Quote(name)
		}
		switch pt.Kind {
		case PartKey:
			out = append(out, keyText+": "+next())
		case PartComputed:
			k := next()
			out = append(out, "["+k+"]: "+next())
		case PartSpread:
			out = append(out, "..."+next())
		case PartGetter:
			out = append(out, "get "+keyText+": "+next())
		case PartSetter:
			out = append(out, "set "+keyText+": "+next())
		case PartMethod:
			out = append(out, keyText+"(): "+next())
		case PartProto:
			out = append(out, "[[Prototype]]: "+next())
		case PartExclude:
			out = append(out, "-"+keyText)
		}
	}
	return "{" + strings.Join(out, ", ") + "}"
}
