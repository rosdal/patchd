package ir

import (
	"github.com/patchd/patchd/engine/tsbridge"
)

// elems returns the children of n that carry runtime meaning: no tokens,
// modifiers, decorators, types or JSDoc.
func (fb *fnBuilder) elems(n tsbridge.NodeID) []tsbridge.NodeID {
	t := fb.t
	kids := t.Children(n)
	clean := true
	for _, c := range kids {
		if !isValueChild(t.Kind(c)) {
			clean = false
			break
		}
	}
	if clean {
		return kids
	}
	out := make([]tsbridge.NodeID, 0, len(kids))
	for _, c := range kids {
		if isValueChild(t.Kind(c)) {
			out = append(out, c)
		}
	}
	return out
}

func isValueChild(k tsbridge.Kind) bool {
	switch k {
	case tsbridge.KindToken, tsbridge.KindModifier, tsbridge.KindTypeNode, tsbridge.KindJSDoc, tsbridge.KindTypeParameter,
		tsbridge.KindDecorator:
		return false
	}
	return true
}

// mark records that n was lowered, for the coverage report.
func (fb *fnBuilder) mark(n tsbridge.NodeID) {
	if i := fb.t.Index(n); i >= 0 && fb.b.marks[i] == markNone {
		fb.b.marks[i] = markLowered
	}
}

// markFunc marks a function-like node's own syntax lowered where its
// closure is created; its body is marked when its Func is lowered.
func (fb *fnBuilder) markFunc(n tsbridge.NodeID) { fb.mark(n) }

// markUnknown records that n and the syntax under it (except nested
// functions, which are lowered on their own) were not lowered.
func (fb *fnBuilder) markUnknown(n tsbridge.NodeID) {
	t := fb.t
	var visit func(n tsbridge.NodeID, top bool)
	visit = func(n tsbridge.NodeID, top bool) {
		i := t.Index(n)
		if i < 0 {
			return
		}
		if !top {
			if _, ok := fb.b.fnOf[n]; ok {
				return
			}
			if _, ok := fb.b.classes[n]; ok {
				return
			}
		}
		if fb.b.marks[i] == markNone || top {
			fb.b.marks[i] = markUnknown
		}
		for _, c := range t.Children(n) {
			visit(c, false)
		}
	}
	visit(n, true)
}

// emitUnknown lowers syntax the builder does not support: an unknown
// instruction naming the syntax kind, which analyses treat as "anything".
func (fb *fnBuilder) emitUnknown(n tsbridge.NodeID, args ...ValueID) ValueID {
	fb.markUnknown(n)
	kind := fb.t.CompilerKind(n)
	fb.b.warn(WarnUnknown, n, "not lowered: "+kind)
	return fb.emitArgs(OpUnknown, n, Aux{Name: fb.name(kind)}, args)
}

// lowerStmts lowers a statement list.
func (fb *fnBuilder) lowerStmts(stmts []tsbridge.NodeID) {
	for _, s := range stmts {
		fb.lowerStmt(s)
	}
}

// lowerBlock lowers a block: its scope, then its statements.
func (fb *fnBuilder) lowerBlock(n tsbridge.NodeID) {
	fb.mark(n)
	fb.enterScope(n)
	fb.lowerStmts(fb.t.Children(n))
}

func (fb *fnBuilder) lowerStmt(n tsbridge.NodeID) {
	t := fb.t
	if fb.b.skipped(n) {
		return
	}
	k := t.Kind(n)
	switch k {
	case tsbridge.KindBlock:
		fb.lowerBlock(n)
		return
	case tsbridge.KindFunctionDeclaration:
		fb.mark(n) // instantiated when its scope is entered
		return
	}
	fb.mark(n)
	switch k {
	case tsbridge.KindVariableStatement:
		for _, c := range t.Children(n) {
			if t.Kind(c) == tsbridge.KindVariableDeclarationList {
				fb.lowerDeclList(c)
			}
		}
	case tsbridge.KindVariableDeclarationList:
		fb.lowerDeclList(n)
	case tsbridge.KindExpressionStatement:
		for _, c := range fb.elems(n) {
			fb.lowerExpr(c)
		}
	case tsbridge.KindEmptyStatement, tsbridge.KindDebuggerStatement, tsbridge.KindSemicolonClassElement:
	case tsbridge.KindIfStatement:
		fb.lowerIf(n)
	case tsbridge.KindReturnStatement:
		fb.lowerReturn(n)
	case tsbridge.KindThrowStatement:
		v := fb.undef
		if e := fb.elems(n); len(e) > 0 {
			v = fb.lowerExpr(e[0])
		}
		fb.terminate(OpThrow, n, []ValueID{v})
	case tsbridge.KindWhileStatement, tsbridge.KindDoStatement, tsbridge.KindForStatement,
		tsbridge.KindForInStatement, tsbridge.KindForOfStatement:
		fb.lowerLoop(n, nil)
	case tsbridge.KindBreakStatement, tsbridge.KindContinueStatement:
		fb.lowerJumpStmt(n)
	case tsbridge.KindSwitchStatement:
		fb.lowerSwitch(n, nil)
	case tsbridge.KindLabeledStatement:
		fb.lowerLabeled(n)
	case tsbridge.KindTryStatement:
		fb.lowerTry(n)
	case tsbridge.KindWithStatement:
		fb.lowerWith(n)
	case tsbridge.KindClassDeclaration:
		fb.lowerClass(n)
	case tsbridge.KindEnumDeclaration:
		fb.lowerEnum(n)
	case tsbridge.KindModuleDeclaration:
		fb.lowerNamespace(n)
	case tsbridge.KindImportDeclaration:
		fb.lowerImport(n)
	case tsbridge.KindImportEqualsDeclaration:
		fb.lowerImportEquals(n)
	case tsbridge.KindExportDeclaration:
		fb.lowerExportDecl(n)
	case tsbridge.KindExportAssignment:
		fb.lowerExportAssignment(n)
	default:
		if isExpressionKind(k) && k != tsbridge.KindOther {
			fb.lowerExpr(n)
			return
		}
		fb.emitUnknown(n)
	}
}

// lowerDeclList lowers var/let/const declarations: each initializer, then
// its binding. A let without an initializer is set to undefined, so a
// binding redeclared in a loop does not carry the last iteration's value.
func (fb *fnBuilder) lowerDeclList(n tsbridge.NodeID) {
	t := fb.t
	fb.mark(n)
	isVar := t.Flags(n)&(tsbridge.FlagLet|tsbridge.FlagConst|tsbridge.FlagUsing|tsbridge.FlagAwaitUsing) == 0
	for _, d := range t.Children(n) {
		if t.Kind(d) != tsbridge.KindVariableDeclaration {
			continue
		}
		fb.mark(d)
		e := fb.elems(d)
		if len(e) == 0 {
			continue
		}
		name := e[0]
		if len(e) > 1 {
			v := fb.lowerExpr(e[1])
			fb.bindTarget(name, v, v, "")
			continue
		}
		if !isVar {
			fb.bindTarget(name, fb.undef, fb.undef, "")
		} else {
			fb.mark(name)
		}
	}
}

func (fb *fnBuilder) lowerIf(n tsbridge.NodeID) {
	e := fb.elems(n)
	if len(e) < 2 {
		fb.emitUnknown(n)
		return
	}
	cond := fb.lowerExpr(e[0])
	then := fb.newBlock("if.then")
	done := fb.newBlock("if.done")
	els := done
	if len(e) > 2 {
		els = fb.newBlock("if.else")
	}
	fb.branch(n, cond, then, els)
	fb.seal(then)
	fb.cur = then
	fb.lowerStmt(e[1])
	fb.jump(done)
	if len(e) > 2 {
		fb.seal(els)
		fb.cur = els
		fb.lowerStmt(e[2])
		fb.jump(done)
	}
	fb.seal(done)
	fb.cur = done
}

func (fb *fnBuilder) lowerReturn(n tsbridge.NodeID) {
	v := fb.undef
	if e := fb.elems(n); len(e) > 0 {
		v = fb.lowerExpr(e[0])
	}
	fb.exitRegions(0)
	if fb.reachable() {
		fb.terminate(OpReturn, n, []ValueID{v})
	}
}

// exitRegions runs, innermost first, the finally bodies of the try
// statements a jump leaves: all regions at depth >= depth.
func (fb *fnBuilder) exitRegions(depth int) {
	for r := len(fb.regions) - 1; r >= depth && fb.reachable(); r-- {
		fin := fb.regions[r].finally
		if fin == 0 {
			continue
		}
		savedR, savedT := fb.regions, fb.targets
		fb.regions = fb.regions[:r:r]
		depth := savedR[r].targets
		fb.targets = fb.targets[:depth:depth]
		fb.lowerBlock(fin)
		fb.regions, fb.targets = savedR, savedT
	}
}

// lowerLoop lowers the five loop statements. labels are the labels written
// on the loop (continue label targets it).
func (fb *fnBuilder) lowerLoop(n tsbridge.NodeID, labels []string) {
	fb.mark(n)
	switch fb.t.Kind(n) {
	case tsbridge.KindWhileStatement:
		fb.lowerWhile(n, labels)
	case tsbridge.KindDoStatement:
		fb.lowerDo(n, labels)
	case tsbridge.KindForStatement:
		fb.lowerFor(n, labels)
	default:
		fb.lowerForInOf(n, labels)
	}
}

func (fb *fnBuilder) pushTarget(labels []string, brk, cont BlockID, loop bool) {
	fb.targets = append(fb.targets, jumpTarget{labels: labels, brk: brk, cont: cont, loop: loop, regions: len(fb.regions)})
}

func (fb *fnBuilder) popTarget() { fb.targets = fb.targets[:len(fb.targets)-1] }

func (fb *fnBuilder) lowerWhile(n tsbridge.NodeID, labels []string) {
	e := fb.elems(n)
	if len(e) < 2 {
		fb.emitUnknown(n)
		return
	}
	head := fb.newBlock("while.head")
	body := fb.newBlock("while.body")
	done := fb.newBlock("while.done")
	fb.jump(head)
	fb.cur = head
	cond := fb.lowerExpr(e[0])
	fb.branch(n, cond, body, done)
	fb.seal(body)
	fb.cur = body
	fb.pushTarget(labels, done, head, true)
	fb.lowerStmt(e[1])
	fb.popTarget()
	fb.jump(head)
	fb.seal(head)
	fb.seal(done)
	fb.cur = done
}

func (fb *fnBuilder) lowerDo(n tsbridge.NodeID, labels []string) {
	e := fb.elems(n)
	if len(e) < 2 {
		fb.emitUnknown(n)
		return
	}
	body := fb.newBlock("do.body")
	test := fb.newBlock("do.test")
	done := fb.newBlock("do.done")
	fb.jump(body)
	fb.cur = body
	fb.pushTarget(labels, done, test, true)
	fb.lowerStmt(e[0])
	fb.popTarget()
	fb.jump(test)
	fb.seal(test)
	fb.cur = test
	cond := fb.lowerExpr(e[1])
	fb.branch(n, cond, body, done)
	fb.seal(body)
	fb.seal(done)
	fb.cur = done
}

func (fb *fnBuilder) lowerFor(n tsbridge.NodeID, labels []string) {
	t := fb.t
	var init, cond, incr, stmt tsbridge.NodeID
	for _, c := range t.Children(n) {
		switch t.Role(c) {
		case tsbridge.RoleInitializer:
			init = c
		case tsbridge.RoleCondition:
			cond = c
		case tsbridge.RoleIncrementor:
			incr = c
		case tsbridge.RoleBody:
			stmt = c
		}
	}
	fb.enterScope(n)
	if init != 0 {
		if t.Kind(init) == tsbridge.KindVariableDeclarationList {
			fb.lowerDeclList(init)
		} else {
			fb.lowerExpr(init)
		}
	}
	perIter := fb.perIterationCells(n)
	fb.copyCells(perIter, n)
	head := fb.newBlock("for.head")
	body := fb.newBlock("for.body")
	update := fb.newBlock("for.update")
	done := fb.newBlock("for.done")
	fb.jump(head)
	fb.cur = head
	if cond != 0 {
		c := fb.lowerExpr(cond)
		fb.branch(n, c, body, done)
	} else {
		fb.jump(body)
	}
	fb.seal(body)
	fb.cur = body
	fb.pushTarget(labels, done, update, true)
	if stmt != 0 {
		fb.lowerStmt(stmt)
	}
	fb.popTarget()
	fb.jump(update)
	fb.seal(update)
	fb.cur = update
	if len(fb.f.Blocks[update].Preds) == 0 {
		fb.f.Blocks[update].Dead = true
	}
	fb.copyCells(perIter, n)
	if incr != 0 {
		fb.lowerExpr(incr)
	}
	fb.jump(head)
	fb.seal(head)
	fb.seal(done)
	fb.cur = done
}

// perIterationCells returns the captured let bindings of a for statement's
// head: each iteration gets a fresh copy, so closures created in one
// iteration keep that iteration's value.
func (fb *fnBuilder) perIterationCells(n tsbridge.NodeID) []int32 {
	si := fb.b.scopes[n]
	if si == nil {
		return nil
	}
	var out []int32
	for _, v := range si.vars {
		vi := &fb.b.vars[v]
		if vi.captured && !vi.global && vi.kind != declVar && fb.b.fns[vi.owner] == fb.info {
			out = append(out, v)
		}
	}
	return out
}

func (fb *fnBuilder) copyCells(vars []int32, n tsbridge.NodeID) {
	for _, v := range vars {
		if !fb.reachable() {
			return
		}
		old := fb.readVariable(v, fb.cur)
		val := fb.emit(OpLoad, n, Aux{Flags: AuxCell}, old)
		c := fb.newCell(v, n)
		fb.emit(OpStore, n, Aux{Flags: AuxCell}, c, val)
	}
}

// lowerForInOf lowers for…in, for…of and for await…of. The head's branch
// has no condition: the iteration protocol decides. Each iteration reads the
// next key (load v[in]) or element (load v[of]), awaited for for await.
func (fb *fnBuilder) lowerForInOf(n tsbridge.NodeID, labels []string) {
	t := fb.t
	e := fb.elems(n)
	if len(e) < 3 {
		fb.emitUnknown(n)
		return
	}
	head, expr, stmt := e[0], e[1], e[2]
	obj := fb.lowerExpr(expr)
	kind := "forof"
	flag := AuxIterValue
	if t.Kind(n) == tsbridge.KindForInStatement {
		kind, flag = "forin", AuxIterKey
	}
	hd := fb.newBlock(kind + ".head")
	body := fb.newBlock(kind + ".body")
	done := fb.newBlock(kind + ".done")
	fb.jump(hd)
	fb.cur = hd
	fb.terminate(OpBranch, n, nil, body, done)
	fb.seal(body)
	fb.cur = body
	fb.enterScope(n)
	elem := fb.emit(OpLoad, head, Aux{Flags: flag}, obj)
	if t.Flags(n)&tsbridge.FlagAwait != 0 {
		elem = fb.emit(OpAwait, head, Aux{}, elem)
	}
	if t.Kind(head) == tsbridge.KindVariableDeclarationList {
		fb.mark(head)
		for _, d := range t.Children(head) {
			if t.Kind(d) == tsbridge.KindVariableDeclaration {
				fb.mark(d)
				if de := fb.elems(d); len(de) > 0 {
					fb.typeOf(elem, d) // the declared element type, also for a pattern
					fb.bindTarget(de[0], elem, elem, "")
				}
			}
		}
	} else {
		fb.assignTarget(head, elem, elem, "")
	}
	fb.pushTarget(labels, done, hd, true)
	fb.lowerStmt(stmt)
	fb.popTarget()
	fb.jump(hd)
	fb.seal(hd)
	fb.seal(done)
	fb.cur = done
}

func (fb *fnBuilder) lowerJumpStmt(n tsbridge.NodeID) {
	t := fb.t
	label := ""
	for _, c := range t.Children(n) {
		if t.Kind(c) == tsbridge.KindIdentifier {
			label = t.Value(c)
		}
	}
	isBreak := t.Kind(n) == tsbridge.KindBreakStatement
	for i := len(fb.targets) - 1; i >= 0; i-- {
		tg := fb.targets[i]
		var to BlockID
		switch {
		case isBreak && label == "" && tg.loop:
			to = tg.brk
		case isBreak && label != "" && hasLabel(tg.labels, label):
			to = tg.brk
		case !isBreak && tg.cont != NoBlock && (label == "" || hasLabel(tg.labels, label)):
			to = tg.cont
		}
		if to == NoBlock {
			continue
		}
		fb.exitRegions(tg.regions)
		if fb.reachable() {
			fb.terminate(OpJump, n, nil, to)
		}
		return
	}
	// A jump with no target is a syntax error; nothing after it runs.
	fb.terminate(OpUnreachable, n, nil)
}

func hasLabel(labels []string, l string) bool {
	for _, x := range labels {
		if x == l {
			return true
		}
	}
	return false
}

// lowerLabeled lowers a labelled statement: labels on a loop or switch let
// break/continue target it; on anything else they make a block break can
// leave.
func (fb *fnBuilder) lowerLabeled(n tsbridge.NodeID) {
	t := fb.t
	var labels []string
	stmt := n
	for t.Kind(stmt) == tsbridge.KindLabeledStatement {
		fb.mark(stmt)
		var next tsbridge.NodeID
		for _, c := range t.Children(stmt) {
			if t.Kind(c) == tsbridge.KindIdentifier && next == 0 && len(labels) < 1000 {
				labels = append(labels, t.Value(c))
				next = c
				continue
			}
			next = c
		}
		if next == 0 || next == stmt {
			return
		}
		stmt = next
	}
	switch t.Kind(stmt) {
	case tsbridge.KindWhileStatement, tsbridge.KindDoStatement, tsbridge.KindForStatement,
		tsbridge.KindForInStatement, tsbridge.KindForOfStatement:
		fb.lowerLoop(stmt, labels)
		return
	case tsbridge.KindSwitchStatement:
		fb.mark(stmt)
		fb.lowerSwitch(stmt, labels)
		return
	}
	done := fb.newBlock("label.done")
	fb.pushTarget(labels, done, NoBlock, false)
	fb.lowerStmt(stmt)
	fb.popTarget()
	fb.jump(done)
	fb.seal(done)
	fb.cur = done
}

// lowerSwitch tests the cases in order (===) and lays the bodies out in
// source order so control falls through; default is where the last failed
// test goes, wherever it is written.
func (fb *fnBuilder) lowerSwitch(n tsbridge.NodeID, labels []string) {
	t := fb.t
	e := fb.elems(n)
	if len(e) < 2 {
		fb.emitUnknown(n)
		return
	}
	disc := fb.lowerExpr(e[0])
	cb := e[1]
	fb.mark(cb)
	fb.enterScope(cb)
	var clauses []tsbridge.NodeID
	for _, c := range t.Children(cb) {
		if k := t.Kind(c); k == tsbridge.KindCaseClause || k == tsbridge.KindDefaultClause {
			clauses = append(clauses, c)
		}
	}
	done := fb.newBlock("switch.done")
	bodies := make([]BlockID, len(clauses))
	def := done
	for i, c := range clauses {
		if t.Kind(c) == tsbridge.KindDefaultClause {
			bodies[i] = fb.newBlock("switch.default")
			def = bodies[i]
		} else {
			bodies[i] = fb.newBlock("switch.case")
		}
	}
	stmts := make([][]tsbridge.NodeID, len(clauses))
	for i, c := range clauses {
		fb.mark(c)
		ce := fb.elems(c)
		if t.Kind(c) == tsbridge.KindCaseClause && len(ce) > 0 {
			v := fb.lowerExpr(ce[0])
			eq := fb.emit(OpBinOp, c, Aux{Name: fb.name("===")}, disc, v)
			next := fb.newBlock("switch.test")
			fb.branch(c, eq, bodies[i], next)
			fb.seal(next)
			fb.cur = next
			ce = ce[1:]
		}
		stmts[i] = ce
	}
	fb.jump(def)
	fb.pushTarget(labels, done, NoBlock, true)
	for i := range clauses {
		fb.seal(bodies[i])
		fb.cur = bodies[i]
		fb.lowerStmts(stmts[i])
		if i+1 < len(clauses) {
			fb.jump(bodies[i+1])
		} else {
			fb.jump(done)
		}
	}
	fb.popTarget()
	fb.seal(done)
	fb.cur = done
}

// lowerTry lowers try/catch/finally. Can-throw instructions in the try body
// get exceptional edges to the catch block (or, without a catch, to a
// finally.throw block that runs finally and rethrows); finally is
// duplicated on every way out: normal exit, return, break/continue and
// exception.
func (fb *fnBuilder) lowerTry(n tsbridge.NodeID) {
	t := fb.t
	var tryB, catchC, fin tsbridge.NodeID
	for _, c := range t.Children(n) {
		switch t.Kind(c) {
		case tsbridge.KindBlock:
			if tryB == 0 {
				tryB = c
			} else {
				fin = c
			}
		case tsbridge.KindCatchClause:
			catchC = c
		}
	}
	if tryB == 0 {
		fb.emitUnknown(n)
		return
	}
	done := fb.newBlock("try.done")
	var catchB, finThrow BlockID
	if fin != 0 {
		finThrow = fb.newBlock("finally.throw")
	}
	if catchC != 0 {
		catchB = fb.newBlock("catch")
	}
	h := catchB
	if h == NoBlock {
		h = finThrow
	}
	body := fb.newBlock("try")
	fb.jump(body)
	fb.seal(body)
	fb.cur = body
	fb.regions = append(fb.regions, region{handler: h, finally: fin, targets: len(fb.targets)})
	fb.lowerBlock(tryB)
	fb.regions = fb.regions[:len(fb.regions)-1]
	if fb.reachable() && fin != 0 {
		fb.lowerBlock(fin)
	}
	fb.jump(done)
	if catchC != 0 {
		fb.mark(catchC)
		fb.seal(catchB)
		fb.cur = catchB
		if len(fb.f.Blocks[catchB].Preds) == 0 {
			fb.f.Blocks[catchB].Dead = true
		}
		if fin != 0 {
			fb.regions = append(fb.regions, region{handler: finThrow, finally: fin, targets: len(fb.targets)})
		}
		exc := fb.emit(OpCatchVal, catchC, Aux{})
		fb.enterScope(catchC)
		var block tsbridge.NodeID
		for _, c := range t.Children(catchC) {
			switch t.Kind(c) {
			case tsbridge.KindVariableDeclaration:
				fb.mark(c)
				if e := fb.elems(c); len(e) > 0 {
					if t.Kind(e[0]) == tsbridge.KindIdentifier {
						fb.typeOf(exc, e[0])
					}
					fb.bindTarget(e[0], exc, exc, "")
				}
			case tsbridge.KindBlock:
				block = c
			}
		}
		if block != 0 {
			fb.lowerBlock(block)
		}
		if fin != 0 {
			fb.regions = fb.regions[:len(fb.regions)-1]
			if fb.reachable() {
				fb.lowerBlock(fin)
			}
		}
		fb.jump(done)
	}
	if fin != 0 {
		fb.seal(finThrow)
		fb.cur = finThrow
		if len(fb.f.Blocks[finThrow].Preds) == 0 {
			fb.f.Blocks[finThrow].Dead = true
			fb.terminate(OpUnreachable, fin, nil)
		} else {
			exc := fb.emit(OpCatchVal, fin, Aux{})
			fb.lowerBlock(fin)
			if fb.reachable() {
				fb.terminate(OpThrow, fin, []ValueID{exc})
			}
		}
	}
	fb.seal(done)
	fb.cur = done
}

// lowerWith lowers a with statement: names in its body may read the
// object's properties, which the IR can't express, so an unknown records
// the scope and the body is lowered as if it weren't there.
func (fb *fnBuilder) lowerWith(n tsbridge.NodeID) {
	e := fb.elems(n)
	if len(e) < 2 {
		fb.emitUnknown(n)
		return
	}
	obj := fb.lowerExpr(e[0])
	fb.b.warn(WarnWith, n, "with statement: names in its body may read the object's properties")
	fb.emitArgs(OpUnknown, n, Aux{Name: fb.name("WithStatement")}, []ValueID{obj})
	if i := fb.t.Index(n); i >= 0 {
		fb.b.marks[i] = markUnknown
	}
	fb.lowerStmt(e[1])
}
