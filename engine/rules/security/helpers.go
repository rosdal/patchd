package security

import (
	"github.com/patchd/patchd/engine/ir"
	"github.com/patchd/patchd/engine/rules"
)

// def returns the instruction defining v, if any.
func def(f *ir.Func, v ir.ValueID) *ir.Instr {
	val := f.Value(v)
	if val == nil || val.Kind != ir.ValueInstr {
		return nil
	}
	return f.Instr(val.Def)
}

// objectProperty returns the value of a static key in an object literal.
func objectProperty(f *ir.Func, obj *ir.Instr, key string) (ir.ValueID, bool) {
	args := f.Args(obj)
	ai := 0
	for _, p := range f.Parts(obj) {
		switch p.Kind {
		case ir.PartExclude:
			continue
		case ir.PartComputed:
			ai += 2
			continue
		}
		if ai >= len(args) {
			break
		}
		if p.Kind == ir.PartKey && p.Name.String() == key {
			return args[ai], true
		}
		ai++
	}
	return ir.NoValue, false
}

// constTruthy reports whether v is a truthy literal: true, a non-empty
// string (shell: "/bin/sh"), a non-zero number.
func constTruthy(f *ir.Func, v ir.ValueID) bool {
	in := def(f, v)
	if in == nil || in.Op != ir.OpConst {
		return false
	}
	text := in.Aux.Name.String()
	switch in.Aux.Flags.Lit() {
	case ir.LitBool:
		return text == "true"
	case ir.LitString:
		return text != ""
	case ir.LitNumber:
		return text != "0"
	}
	return false
}

// built reports whether the path builds a string from its tainted part: a
// concatenation, a template literal, or a string-building call.
func built(c *rules.Candidate) bool {
	f := c.Func()
	for _, st := range c.Path {
		in := f.Instr(st.Instr)
		if in == nil {
			continue
		}
		switch in.Op {
		case ir.OpTemplate:
			return true
		case ir.OpBinOp:
			if in.Aux.Name.String() == "+" {
				return true
			}
		case ir.OpCall:
			switch in.Aux.Name.String() {
			case "concat", "join", "replace", "replaceAll", "format":
				return true
			}
		}
	}
	return false
}
