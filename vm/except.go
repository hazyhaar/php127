package vm

import (
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/types"
)

func (vm *VM) file() string {
	if len(vm.frames) == 0 || vm.frame().fn == nil || vm.frame().fn.Filename == "" {
		return "Command line code"
	}
	return vm.frame().fn.Filename
}

func (vm *VM) fatalFeature(name string) {
	if vm.stop {
		return
	}
	vm.control = true
	var b strings.Builder
	b.WriteString("\nFatal error: fonctionnalité absente: ")
	b.WriteString(name)
	b.WriteString(" in ")
	b.WriteString(vm.file())
	b.WriteString(" on line ")
	b.WriteString(strconv.FormatUint(uint64(vm.line), 10))
	b.WriteByte('\n')
	if vm.Zone != nil {
		stdlib.RecordError(vm.Zone, eError, "fonctionnalité absente: "+name, vm.file(), int64(vm.line))
	}
	_, _ = vm.Out.Write([]byte(b.String()))
	vm.exit = 255
	vm.stop = true
}

func (vm *VM) raiseBuiltin(classID uint32, message string, code int64, trace ...traceItem) {
	if vm.stop {
		return
	}
	obj := vm.makeException(classID, message, code, vm.file(), vm.line)
	pc := 0
	if len(vm.frames) > 0 {
		pc = vm.frame().pc
	}
	vm.excTrace = trace
	vm.throwAt(pc, obj)
}

func (vm *VM) makeException(classID uint32, message string, code int64, file string, line uint32) types.Value {
	props := uint32(4)
	if classID >= bytecode.ClassUser {
		props = max(vm.instancePropCount(classID), 4)
	}
	obj, err := types.NewObject(vm.Zone, classID, props, false)
	if err != nil {
		vm.fatalFeature("exception")
		return types.NewNull()
	}
	vm.setExceptionProps(obj, message, code, file, line)
	return obj
}

func (vm *VM) noteTry(pc int) {
	fr := vm.frame()
	for i, e := range fr.fn.TryTable {
		if int(e.StartPC) == pc && fr.tryDepth[i] < 0 {
			fr.tryDepth[i] = fr.evalTop - fr.evalBase
		}
	}
}

func (vm *VM) opReturn(in bytecode.Instr) {
	var val types.Value
	if in.Flags&bytecode.FlagReturnVoid != 0 {
		val = types.NewNull()
	} else {
		val = vm.pop()
		if vm.stop {
			return
		}
	}
	pc := vm.frame().pc
	fr := vm.frame()
	if fr.fn.ReturnType == "never" {
		types.ReleaseValue(vm.Zone, &val)
		vm.raiseBuiltin(bytecode.ClassTypeError, fr.fn.Name+"(): never-returning function must not implicitly return", 0)
		return
	}
	if !fr.fn.IsGenerator && fr.fn.ReturnType != "" && !(fr.fn.ReturnType == "void" && in.Flags&bytecode.FlagReturnVoid != 0) {
		given := vm.valueType(val)
		missing := in.Flags&bytecode.FlagReturnVoid != 0
		if missing {
			given = "none"
		}
		if missing || !vm.coerceType(&val, fr.fn.ReturnType, fr.fn.StrictTypes, fr.scopeClass, fr.calledClass) {
			types.ReleaseValue(vm.Zone, &val)
			name := fr.fn.ReturnType
			if base, ok := strings.CutSuffix(name, "|null"); ok && !strings.ContainsAny(base, "|&") {
				name = "?" + base
			}
			vm.raiseBuiltin(bytecode.ClassTypeError, fr.fn.Name+"(): Return value must be of type "+name+", "+given+" returned", 0)
			return
		}
	}
	if vm.inActiveFinally() {
		top := &vm.frame().fin[len(vm.frame().fin)-1]
		types.ReleaseValue(vm.Zone, &top.val)
		top.kind = contReturn
		top.val = val
		top.active = false
		vm.finishFinally()
		return
	}
	if idx, fpc, ok := vm.finallyFor(pc); ok {
		fr := vm.frame()
		fr.fin = append(fr.fin, finState{tryIndex: idx, kind: contReturn, val: val, origin: pc, active: true})
		fr.pc = fpc
		return
	}
	vm.doReturn(val)
}

func (vm *VM) opExit(in bytecode.Instr) {
	code := 0
	var val types.Value
	if in.Flags&bytecode.FlagExitVoid == 0 {
		val = vm.pop()
		if val.Type == types.TypeLong {
			code = int(int64(val.Scalar))
		} else if val.Type == types.TypeString {
			vm.echo(val)
		}
		types.ReleaseValue(vm.Zone, &val)
	}
	pc := 0
	if len(vm.frames) > 0 {
		pc = vm.frame().pc
	}
	if idx, fpc, ok := vm.finallyFor(pc); ok {
		fr := vm.frame()
		fr.fin = append(fr.fin, finState{tryIndex: idx, kind: contExit, val: types.NewLong(int64(code)), origin: pc, active: true})
		fr.pc = fpc
		return
	}
	vm.exit = code
	vm.stop = true
}

func (vm *VM) doReturn(val types.Value) {
	vm.control = true
	if len(vm.frames) == 1 {
		types.ReleaseValue(vm.Zone, &val)
		vm.stop = true
		vm.exit = 0
		vm.mainReturned = true
		return
	}
	depth := len(vm.frames)
	fr := &vm.frames[depth-1]
	inline := fr.inlineFrame
	fr.pc = len(fr.fn.Code)
	vm.releaseFrameValues()
	if vm.stop || vm.hasThrow || len(vm.frames) < depth {
		types.ReleaseValue(vm.Zone, &val)
		return
	}
	vm.popFrame()
	if inline {
		vm.push(val)
		return
	}
	vm.retVal = val
	vm.hasRet = true
}

func (vm *VM) jump(target int) {
	if len(vm.frames) == 0 {
		return
	}
	vm.control = true
	pc := vm.frame().pc
	if idx, fpc, ok := vm.finallyCrossed(pc, target); ok {
		fr := vm.frame()
		fr.fin = append(fr.fin, finState{tryIndex: idx, kind: contJump, target: target, origin: pc, active: true})
		fr.pc = fpc
		return
	}
	vm.frame().pc = target
}

func (vm *VM) opCond(op bytecode.Opcode, target int) {
	v := vm.pop()
	if vm.stop {
		return
	}
	var truth bool
	switch v.Type {
	case types.TypeFalse, types.TypeNull, types.TypeUndef:
		truth = false
	case types.TypeTrue:
		truth = true
	case types.TypeLong:
		truth = v.Scalar != 0
	default:
		truth = types.ToBoolZ(vm.Zone, v)
		vmRelease(vm.Zone, &v)
		if vm.stop || vm.control {
			return
		}
	}
	if op == bytecode.OpJmpZ {
		truth = !truth
	}
	if truth {
		if len(vm.frame().fn.TryTable) == 0 {
			vm.control = true
			vm.frame().pc = target
			return
		}
		vm.jump(target)
		return
	}
	vm.bump()
}

func (vm *VM) opJmpNull(target int) {
	fr := vm.frame()
	if fr.evalTop <= fr.evalBase {
		vm.fatalFeature("sous-dépassement de pile")
		return
	}
	v := vm.slots[fr.evalTop-1]
	if types.Deref(vm.Zone, v).IsNull() || types.Deref(vm.Zone, v).IsUndef() {
		dropped := vm.pop()
		types.ReleaseValue(vm.Zone, &dropped)
		vm.jump(target)
		return
	}
	vm.bump()
}

func (vm *VM) opSwitch(op bytecode.Opcode, in bytecode.Instr) {
	v := vm.pop()
	if vm.stop {
		return
	}
	defer types.ReleaseValue(vm.Zone, &v)
	table := vm.frame().fn.Tables[in.Op1]
	for _, c := range table.Cases {
		if vm.caseMatch(op, v, c) {
			vm.jump(int(c.Target))
			return
		}
	}
	if table.Default != bytecode.NoPC {
		vm.jump(int(table.Default))
		return
	}
	if op == bytecode.OpMatch {
		label := "Unhandled match case"
		if types.Deref(vm.Zone, v).Type == types.TypeLong {
			label = "Unhandled match case " + strconv.FormatInt(int64(v.Scalar), 10)
		}
		vm.raiseBuiltin(bytecode.ClassUnhandledMatchError, label, 0)
		return
	}
	vm.bump()
}

func (vm *VM) caseMatch(op bytecode.Opcode, v types.Value, c bytecode.JumpCase) bool {
	fn := vm.frame().fn
	switch c.Kind {
	case bytecode.CaseString:
		s := vm.materialize(fn, c.Index)
		defer types.ReleaseValue(vm.Zone, &s)
		if op == bytecode.OpSwitchString || op == bytecode.OpMatch {
			return types.IdenticalZ(vm.Zone, v, s)
		}
		return types.EqualZ(vm.Zone, v, s)
	default:
		other := fn.Consts[c.Index]
		if op == bytecode.OpMatch || op == bytecode.OpSwitchLong {
			return types.IdenticalZ(vm.Zone, v, other)
		}
		return types.EqualZ(vm.Zone, v, other)
	}
}

func (vm *VM) opEnterCatch(cv uint32) {
	fr := vm.frame()
	if !fr.hasPending {
		vm.bump()
		return
	}
	obj := fr.pending
	fr.hasPending = false
	fr.pending = types.Value{}
	if cv == bytecode.NoCV {
		types.ReleaseValue(vm.Zone, &obj)
	} else {
		vm.storeCVMove(cv, obj)
	}
	vm.bump()
}

func (vm *VM) throwAt(pc int, obj types.Value) {
	vm.control = true
	if vm.stop {
		types.ReleaseValue(vm.Zone, &obj)
		return
	}
	if len(vm.frames) == 0 {
		vm.uncaught(obj)
		return
	}
	if vm.inActiveFinally() {
		top := &vm.frame().fin[len(vm.frame().fin)-1]
		types.ReleaseValue(vm.Zone, &top.val)
		top.kind = contThrow
		top.val = obj
		top.active = false
		vm.finishFinally()
		return
	}
	if catch, ok := vm.findCatch(pc, obj); ok {
		vm.excTrace = nil
		vm.unwindEval(catch.tryIndex)
		fr := vm.frame()
		fr.pending = obj
		fr.hasPending = true
		fr.pc = catch.pc
		return
	}
	if idx, fpc, ok := vm.finallyFor(pc); ok {
		fr := vm.frame()
		fr.fin = append(fr.fin, finState{tryIndex: idx, kind: contThrow, val: obj, origin: pc, active: true})
		fr.pc = fpc
		return
	}
	vm.propagate(obj)
}

func (vm *VM) propagate(obj types.Value) {
	vm.control = true
	if len(vm.frames) > 1 {
		depth := len(vm.frames)
		fr := &vm.frames[depth-1]
		caller := vm.frames[depth-2]
		name := fr.fn.Name
		if name == "" {
			name = "{closure}"
		}
		if fr.scopeClass != 0 {
			sep := "::"
			if fr.hasThis {
				sep = "->"
			}
			name = vm.className(fr.scopeClass) + sep + name
		}
		file := ""
		if caller.fn != nil {
			file = caller.fn.Filename
		}
		line := fr.callLine
		if fr.fn.Name == "__destruct" {
			file = "[internal function]"
			line = 0
		}
		var argsStr string
		if len(fr.args) > 0 {
			argsStr = vm.traceArgs(fr.args)
		} else if len(fr.fn.Params) > 0 {
			var paramVals []types.Value
			for i := range fr.fn.Params {
				if !fr.fn.Params[i].Variadic && fr.cvBase+i < len(vm.slots) {
					paramVals = append(paramVals, vm.slots[fr.cvBase+i])
				}
			}
			argsStr = vm.traceArgs(paramVals)
		}
		vm.excTrace = append(vm.excTrace, traceItem{file: file, line: line, name: name, args: argsStr})
		inline := fr.inlineFrame
		fr.pc = len(fr.fn.Code)
		vm.releaseFrameValues()
		if vm.stop || vm.hasThrow || len(vm.frames) < depth {
			types.ReleaseValue(vm.Zone, &obj)
			return
		}
		vm.popFrame()
		if inline {
			vm.throwAt(max(vm.frame().pc-1, 0), obj)
			return
		}
		vm.thrown = obj
		vm.hasThrow = true
		return
	}
	vm.uncaught(obj)
}

func (vm *VM) uncaught(obj types.Value) {
	if vm.stop {
		types.ReleaseValue(vm.Zone, &obj)
		return
	}
	text, file, line := vm.formatUncaught(obj)
	if vm.Zone != nil {
		// error_get_last() d'une fonction d'arrêt rend, comme PHP, le texte affiché sans son préfixe ni son lieu final.
		msg := strings.TrimPrefix(text, "\nFatal error: ")
		msg = strings.TrimSuffix(msg, " in "+file+" on line "+strconv.FormatInt(line, 10)+"\n")
		stdlib.RecordError(vm.Zone, eError, msg, file, line)
	}
	_, _ = vm.Out.Write([]byte(text))
	types.ReleaseValue(vm.Zone, &obj)
	vm.exit = 255
	vm.stop = true
}

// formatUncaught rend le texte affiché pour une exception non interceptée, avec le fichier et la ligne de sa levée.
func (vm *VM) formatUncaught(obj types.Value) (string, string, int64) {
	d := types.Deref(vm.Zone, obj)
	name := "Exception"
	msg := ""
	file := vm.file()
	line := int64(vm.line)
	if d.Type == types.TypeObject {
		name = vm.className(types.ObjectInfo(vm.Zone, d).ClassID)
		if types.ObjectInfo(vm.Zone, d).PropCount >= 4 {
			if m := types.ObjectGet(vm.Zone, d, 0); m.Type == types.TypeString {
				msg = types.StringData(vm.Zone, m)
			}
			if f := types.ObjectGet(vm.Zone, d, 2); f.Type == types.TypeString {
				file = types.StringData(vm.Zone, f)
			}
			if ln := types.ObjectGet(vm.Zone, d, 3); ln.Type == types.TypeLong {
				line = int64(ln.Scalar)
			}
		}
	}
	var b strings.Builder
	b.WriteString("\nFatal error: Uncaught ")
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(msg)
	b.WriteString(" in ")
	b.WriteString(file)
	b.WriteByte(':')
	b.WriteString(strconv.FormatInt(line, 10))
	b.WriteString("\nStack trace:\n")
	if len(vm.excTrace) == 0 {
		b.WriteString("#0 {main}\n")
	} else {
		for i, tr := range vm.excTrace {
			b.WriteByte('#')
			b.WriteString(strconv.Itoa(i))
			b.WriteByte(' ')
			b.WriteString(tr.file)
			if tr.file != "[internal function]" {
				b.WriteByte('(')
				b.WriteString(strconv.FormatUint(uint64(tr.line), 10))
				b.WriteByte(')')
			}
			b.WriteString(": ")
			b.WriteString(tr.name)
			b.WriteByte('(')
			b.WriteString(tr.args)
			b.WriteString(")\n")
		}
		b.WriteByte('#')
		b.WriteString(strconv.Itoa(len(vm.excTrace)))
		b.WriteString(" {main}\n")
	}
	b.WriteString("  thrown in ")
	b.WriteString(file)
	b.WriteString(" on line ")
	b.WriteString(strconv.FormatInt(line, 10))
	b.WriteByte('\n')
	return b.String(), file, line
}

type traceItem struct {
	file string
	line uint32
	name string
	args string
}

type catchHit struct {
	pc       int
	tryIndex int
	cv       int32
}

func (vm *VM) findCatch(pc int, obj types.Value) (catchHit, bool) {
	fr := vm.frame()
	best := -1
	bestSpan := int(^uint(0) >> 1)
	var hit catchHit
	classID := uint32(0)
	d := types.Deref(vm.Zone, obj)
	if d.Type == types.TypeObject {
		classID = types.ObjectInfo(vm.Zone, d).ClassID
	}
	for i, e := range fr.fn.TryTable {
		if pc < int(e.StartPC) || pc >= int(e.EndPC) {
			continue
		}
		span := int(e.EndPC) - int(e.StartPC)
		if span >= bestSpan {
			continue
		}
		for _, c := range e.CatchTargets {
			if vm.catchMatch(classID, c.Classes) {
				best = i
				bestSpan = span
				hit = catchHit{pc: int(c.PC), tryIndex: i, cv: c.CV}
				break
			}
		}
	}
	return hit, best >= 0
}

func (vm *VM) catchMatch(classID uint32, classes []uint32) bool {
	if classID == 0 {
		return false
	}
	for _, id := range classes {
		if vm.isInstance(classID, id) {
			return true
		}
	}
	return false
}

func (vm *VM) unwindEval(tryIndex int) {
	fr := vm.frame()
	depth := 0
	if tryIndex >= 0 && tryIndex < len(fr.tryDepth) && fr.tryDepth[tryIndex] >= 0 {
		depth = fr.tryDepth[tryIndex]
	}
	for fr.evalTop-fr.evalBase > depth {
		v := vm.pop()
		types.ReleaseValue(vm.Zone, &v)
	}
}

func (vm *VM) finallyFor(pc int) (int, int, bool) {
	fr := vm.frame()
	best := -1
	bestSpan := int(^uint(0) >> 1)
	bestPC := 0
	for i, e := range fr.fn.TryTable {
		if e.FinallyPC == bytecode.NoPC {
			continue
		}
		if !protectedContains(e, pc) {
			continue
		}
		span := protectedSpan(e)
		if span < bestSpan {
			best = i
			bestSpan = span
			bestPC = int(e.FinallyPC)
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, bestPC, true
}

func (vm *VM) finallyCrossed(pc, target int) (int, int, bool) {
	fr := vm.frame()
	best := -1
	bestSpan := int(^uint(0) >> 1)
	bestPC := 0
	for i, e := range fr.fn.TryTable {
		if e.FinallyPC == bytecode.NoPC {
			continue
		}
		if !protectedContains(e, pc) {
			continue
		}
		if target >= int(e.FinallyPC) && (e.FinallyEndPC == bytecode.NoPC || target < int(e.FinallyEndPC)) {
			continue
		}
		if protectedContains(e, target) {
			continue
		}
		span := protectedSpan(e)
		if span < bestSpan {
			best = i
			bestSpan = span
			bestPC = int(e.FinallyPC)
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, bestPC, true
}

func protectedContains(e bytecode.TryCatchEntry, pc int) bool {
	hi := int(e.EndPC)
	if e.FinallyPC != bytecode.NoPC && int(e.FinallyPC) > hi {
		hi = int(e.FinallyPC)
	}
	return pc >= int(e.StartPC) && pc < hi
}

func protectedSpan(e bytecode.TryCatchEntry) int {
	hi := int(e.EndPC)
	if e.FinallyPC != bytecode.NoPC && int(e.FinallyPC) > hi {
		hi = int(e.FinallyPC)
	}
	return hi - int(e.StartPC)
}

func (vm *VM) inActiveFinally() bool {
	if len(vm.frames) == 0 {
		return false
	}
	fr := vm.frame()
	if len(fr.fin) == 0 || !fr.fin[len(fr.fin)-1].active {
		return false
	}
	top := fr.fin[len(fr.fin)-1]
	if top.tryIndex < 0 || top.tryIndex >= len(fr.fn.TryTable) {
		return false
	}
	e := fr.fn.TryTable[top.tryIndex]
	pc := fr.pc
	if e.FinallyEndPC == bytecode.NoPC {
		return pc >= int(e.FinallyPC)
	}
	return pc >= int(e.FinallyPC) && pc < int(e.FinallyEndPC)
}

func (vm *VM) finishFinally() {
	vm.control = true
	fr := vm.frame()
	if len(fr.fin) == 0 {
		vm.bump()
		return
	}
	top := fr.fin[len(fr.fin)-1]
	if outer, fpc, ok := vm.outerFinally(top.origin, top.tryIndex); ok {
		fr.fin[len(fr.fin)-1].tryIndex = outer
		fr.fin[len(fr.fin)-1].active = true
		fr.pc = fpc
		return
	}
	fr.fin = fr.fin[:len(fr.fin)-1]
	switch top.kind {
	case contReturn:
		vm.doReturn(top.val)
	case contThrow:
		vm.propagate(top.val)
	case contJump:
		fr.pc = top.target
	case contExit:
		vm.exit = int(top.val.ToLong())
		vm.stop = true
	default:
		types.ReleaseValue(vm.Zone, &top.val)
		vm.bump()
	}
}

func (vm *VM) outerFinally(origin, done int) (int, int, bool) {
	fr := vm.frame()
	if done < 0 || done >= len(fr.fn.TryTable) {
		return 0, 0, false
	}
	doneSpan := protectedSpan(fr.fn.TryTable[done])
	best := -1
	bestSpan := int(^uint(0) >> 1)
	bestPC := 0
	for i, e := range fr.fn.TryTable {
		if i == done || e.FinallyPC == bytecode.NoPC {
			continue
		}
		if !protectedContains(e, origin) {
			continue
		}
		span := protectedSpan(e)
		if span <= doneSpan {
			continue
		}
		if span < bestSpan {
			best = i
			bestSpan = span
			bestPC = int(e.FinallyPC)
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, bestPC, true
}

func (vm *VM) fastCallFinally(finallyPC, retPC int) {
	vm.control = true
	fr := vm.frame()
	idx := -1
	for i, e := range fr.fn.TryTable {
		if int(e.FinallyPC) == finallyPC {
			idx = i
			break
		}
	}
	fr.fin = append(fr.fin, finState{tryIndex: idx, kind: contJump, target: retPC, origin: fr.pc, active: true})
	fr.pc = finallyPC
}
