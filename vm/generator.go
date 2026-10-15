package vm

import (
	"strings"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
)

// Le cadre et ses valeurs sont deplaces, jamais executes par une goroutine.
type generator struct {
	returned                                           bool
	fr                                                 frame
	slots                                              []types.Value
	pending                                            []callSpec
	key, value, result, sent, delegate                 types.Value
	delegation                                         iterState
	started, done, running, yielded, waitSend, closing bool
	nextKey                                            int64
	yields                                             int
}

func (vm *VM) generatorOf(v types.Value) *generator {
	v = types.Deref(vm.Zone, v)
	if v.Type != types.TypeObject || types.ObjectInfo(vm.Zone, v).ClassID != bytecode.ClassGenerator {
		return nil
	}
	i := int(types.ObjectGet(vm.Zone, v, 0).ToLong())
	if i < 0 || i >= len(vm.generators) {
		return nil
	}
	return vm.generators[i]
}

func (vm *VM) newGenerator() types.Value {
	g := &generator{}
	vm.frame().gen = g
	if vm.frame().hasThis {
		vm.frame().this = types.RetainValue(vm.Zone, vm.frame().this)
	}
	vm.suspendGenerator(g)
	obj, err := types.NewObject(vm.Zone, bytecode.ClassGenerator, 1, true)
	if err != nil {
		vm.releaseGenerator(g)
		vm.fatalFeature(err.Error())
		return types.NewUndef()
	}
	idx := len(vm.generators)
	vm.generators = append(vm.generators, g)
	types.ObjectSet(vm.Zone, obj, 0, types.NewLong(int64(idx)))
	return obj
}

func (vm *VM) suspendGenerator(g *generator) {
	fr := *vm.frame()
	g.slots = append(g.slots[:0], vm.slots[fr.cvBase:fr.cvBase+fr.span]...)
	clear(vm.slots[fr.cvBase : fr.cvBase+fr.span])
	g.fr = fr
	g.fr.evalBase -= fr.cvBase
	g.fr.evalTop -= fr.cvBase
	g.fr.cvBase = 0
	vm.popFrame()
}

func (vm *VM) resumeGenerator(g *generator, sent types.Value, closing bool) bool {
	if g.done {
		return true
	}
	if g.running {
		vm.raiseBuiltin(bytecode.ClassError, "Cannot resume an already running generator", 0)
		return false
	}
	base, pendingBase := len(vm.frames), len(vm.pending)
	if !vm.pushFrame(g.fr.fn, g.fr.calledClass, g.fr.scopeClass, g.fr.this, g.fr.hasThis, g.fr.callLine) {
		return false
	}
	offset := vm.frame().cvBase
	fr := g.fr
	fr.cvBase = offset
	fr.evalBase += offset
	fr.evalTop += offset
	*vm.frame() = fr
	copy(vm.slots[offset:offset+fr.span], g.slots)
	clear(g.slots)
	vm.pending = append(vm.pending, g.pending...)
	g.pending = nil
	g.started, g.running, g.yielded, g.closing = true, true, false, closing
	types.ReleaseValue(vm.Zone, &g.key)
	types.ReleaseValue(vm.Zone, &g.value)
	if closing {
		vm.frame().pc = max(0, vm.frame().pc-1)
		vm.opReturn(bytecode.Instr{Flags: bytecode.FlagReturnVoid})
	} else if g.waitSend {
		vm.push(types.RetainValue(vm.Zone, sent))
		g.waitSend = false
	} else if !g.delegate.IsUndef() {
		g.sent = types.RetainValue(vm.Zone, sent)
	}
	vm.execFrames(base)
	g.running = false
	if len(vm.pending) > pendingBase {
		g.pending = append(g.pending, vm.pending[pendingBase:]...)
		vm.pending = vm.pending[:pendingBase]
	}
	if g.yielded {
		vm.suspendGenerator(g)
		vm.control = false
		return true
	}
	g.done = true
	var threw bool
	g.returned = vm.hasRet && !vm.hasThrow && !vm.stop
	g.result, threw = vm.takeReturn()
	if !threw {
		vm.control = false
	}
	return !threw && !vm.stop
}

func (vm *VM) opYield(in bytecode.Instr) {
	g := vm.frame().gen
	if g == nil {
		vm.fatalFeature("yield hors generateur")
		return
	}
	if g.closing {
		vm.raiseBuiltin(bytecode.ClassError, "Cannot yield from finally in a force-closed generator", 0)
		return
	}
	if in.Op == bytecode.OpYieldFrom {
		if g.delegate.IsUndef() {
			g.delegate = vm.pop()
			if child := vm.generatorOf(g.delegate); child != nil {
				if !child.started && !vm.resumeGenerator(child, types.NewNull(), false) {
					return
				}
			} else if g.delegate.Type == types.TypeArray {
				vm.snapshot(&g.delegation, g.delegate, false, -1)
			} else {
				vm.raiseBuiltin(bytecode.ClassError, "Can use \"yield from\" only with arrays and Traversables", 0)
				return
			}
		} else if child := vm.generatorOf(g.delegate); child != nil {
			if !vm.resumeGenerator(child, g.sent, false) {
				return
			}
		}
		types.ReleaseValue(vm.Zone, &g.sent)
		if child := vm.generatorOf(g.delegate); child != nil {
			if child.done {
				vm.push(types.RetainValue(vm.Zone, child.result))
				types.ReleaseValue(vm.Zone, &g.delegate)
				vm.bump()
				return
			}
			g.key = types.RetainValue(vm.Zone, child.key)
			g.value = types.RetainValue(vm.Zone, child.value)
		} else {
			it := &g.delegation
			if it.index >= len(it.keys) {
				vm.freeIter(it)
				types.ReleaseValue(vm.Zone, &g.delegate)
				vm.push(types.NewNull())
				vm.bump()
				return
			}
			g.key = types.RetainValue(vm.Zone, it.keys[it.index])
			g.value = types.RetainValue(vm.Zone, it.vals[it.index])
			it.index++
		}
	} else {
		g.value = vm.pop()
		if in.Flags&bytecode.FlagHasKey != 0 {
			g.key = vm.pop()
			if g.key.Type == types.TypeLong && g.key.ToLong() >= g.nextKey {
				g.nextKey = g.key.ToLong() + 1
			}
		} else {
			g.key = types.NewLong(g.nextKey)
			g.nextKey++
		}
		g.waitSend = true
		vm.bump()
	}
	g.yielded = true
	g.yields++
}

func (vm *VM) generatorMethod(spec callSpec) bool {
	g := vm.generatorOf(spec.obj)
	if g == nil {
		return false
	}
	defer vm.releaseArgs(spec.args)
	defer vm.releaseSpecObj(spec)
	name := strings.ToLower(spec.method)
	if name == "getreturn" {
		if !g.started && !vm.resumeGenerator(g, types.NewNull(), false) {
			return true
		}
		if !g.returned {
			vm.raiseBuiltin(bytecode.ClassException, "Cannot get return value of a generator that hasn't returned", 0)
			return true
		}
		vm.push(types.RetainValue(vm.Zone, g.result))
		vm.bump()
		return true
	}
	switch name {
	case "current", "key", "valid", "next", "send", "rewind":
	default:
		vm.raiseBuiltin(bytecode.ClassError, "Call to undefined method Generator::"+spec.method+"()", 0)
		return true
	}
	if !g.started && !vm.resumeGenerator(g, types.NewNull(), false) {
		return true
	}
	if name == "rewind" && !vm.rewindGenerator(g) {
		return true
	}
	if name == "next" || name == "send" {
		sent := types.NewNull()
		if name == "send" && len(spec.args) > 0 {
			sent = spec.args[0]
		}
		if !vm.resumeGenerator(g, sent, false) {
			return true
		}
	}
	v := types.NewNull()
	switch name {
	case "current", "send":
		if !g.done {
			v = types.RetainValue(vm.Zone, g.value)
		}
	case "key":
		if !g.done {
			v = types.RetainValue(vm.Zone, g.key)
		}
	case "valid":
		v = types.NewBool(!g.done)
	}
	vm.push(v)
	vm.bump()
	return true
}

func (vm *VM) closeGenerator(g *generator) {
	if g.running {
		return
	}
	if g.started && !g.done && len(vm.frames) > 0 && !vm.stop {
		vm.resumeGenerator(g, types.NewNull(), true)
	}
	vm.releaseGenerator(g)
}

func (vm *VM) rewindGenerator(g *generator) bool {
	if !g.started && !vm.resumeGenerator(g, types.NewNull(), false) {
		return false
	}
	if g.yields > 1 || g.done && g.yields > 0 {
		vm.raiseBuiltin(bytecode.ClassException, "Cannot rewind a generator that was already run", 0)
		return false
	}
	return true
}

func (vm *VM) releaseGenerator(g *generator) {
	vm.releaseArgs(g.slots)
	g.slots = nil
	if !g.done {
		vm.releaseArgs(g.fr.args)
		for _, v := range g.fr.dynamicLocals {
			types.ReleaseValue(vm.Zone, &v)
		}
		for i := range g.fr.iters {
			vm.freeIter(&g.fr.iters[i])
		}
		for i := range g.fr.fin {
			types.ReleaseValue(vm.Zone, &g.fr.fin[i].val)
		}
		if g.fr.hasPending {
			types.ReleaseValue(vm.Zone, &g.fr.pending)
		}
	}
	g.done = true
	for _, spec := range g.pending {
		vm.releaseArgs(spec.args)
		vm.releaseSpecObj(spec)
	}
	g.pending = nil
	types.ReleaseValue(vm.Zone, &g.fr.this)
	types.ReleaseValue(vm.Zone, &g.key)
	types.ReleaseValue(vm.Zone, &g.value)
	types.ReleaseValue(vm.Zone, &g.result)
	types.ReleaseValue(vm.Zone, &g.sent)
	types.ReleaseValue(vm.Zone, &g.delegate)
	vm.freeIter(&g.delegation)
}
