package vm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/compiler"
	"code.hazyhaar.fr/devhoros/php127/types"
)

type cachedSource struct {
	data    string
	modTime int64
	size    int64
}

var (
	sourceCacheMu sync.RWMutex
	sourceCache   = make(map[string]cachedSource, 32)
)

func readSourceFile(filename string) (string, error) {
	info, err := os.Stat(filename)
	if err != nil {
		return "", err
	}
	mtime := info.ModTime().UnixNano()
	sz := info.Size()
	sourceCacheMu.RLock()
	cached, ok := sourceCache[filename]
	sourceCacheMu.RUnlock()
	if ok && cached.modTime == mtime && cached.size == sz {
		return cached.data, nil
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	src := string(data)
	sourceCacheMu.Lock()
	if len(sourceCache) >= 512 {
		clear(sourceCache)
	}
	sourceCache[filename] = cachedSource{data: src, modTime: mtime, size: sz}
	sourceCacheMu.Unlock()
	return src, nil
}

func (vm *VM) opSource(in bytecode.Instr) {
	input := vm.pop()
	derefIn := types.Deref(vm.Zone, input)
	tainted := (input.Flags|derefIn.Flags)&types.FlagTainted != 0
	s, owned := vm.stringify(input)
	if s.Flags&types.FlagTainted != 0 {
		tainted = true
	}
	source := types.StringData(vm.Zone, s)
	if owned {
		types.ReleaseValue(vm.Zone, &s)
	}
	types.ReleaseValue(vm.Zone, &input)
	if vm.stop || vm.control {
		return
	}
	if tainted && vm.Context != nil {
		vm.Context.TaintedSinks++
		if vm.Context.BlockTaintedSinks {
			vm.raiseBuiltin(bytecode.ClassValueError, "Tainted input rejected at "+in.Op.String(), 0)
			return
		}
	}
	filename := fmt.Sprintf("%s(%d) : eval()'d code", vm.file(), in.Line)
	isInclude := in.Op == bytecode.OpInclude
	if isInclude {
		kind := vm.frame().fn.Strings[in.Op1]
		filename = source
		if !filepath.IsAbs(filename) {
			if _, err := os.Stat(filename); err != nil {
				filename = filepath.Join(filepath.Dir(vm.file()), filename)
			}
		}
		absolute, err := filepath.Abs(filename)
		if err != nil {
			vm.fatalFeature("chemin d'inclusion: " + err.Error())
			return
		}
		filename = absolute
		if canonical, err := filepath.EvalSymlinks(filename); err == nil {
			filename = canonical
		}
		if strings.HasSuffix(kind, "_once") && vm.included[filename] {
			vm.push(types.NewBool(true))
			vm.bump()
			return
		}
		src, err := readSourceFile(filename)
		if err != nil {
			vm.fatalFeature("inclusion: " + err.Error())
			return
		}
		source = src
	} else {
		source = "<?php " + source
	}
	prog, err := compiler.CompileSource(source, filename)
	if err != nil {
		fmt.Fprintf(vm.Out, "\n%s\n", err)
		vm.exit, vm.stop = 255, true
		return
	}
	if len(prog.Classes) != 0 || len(prog.Traits) != 0 {
		vm.fatalFeature("declarations de classes dans une compilation dynamique")
		return
	}
	for _, fn := range prog.Funcs {
		if fn.Name == "{closure}" {
			continue
		}
		for _, existing := range vm.Program.Funcs {
			if strings.EqualFold(fn.Name, existing.Name) {
				vm.fatalFeature("redeclaration de fonction " + fn.Name)
				return
			}
		}
	}
	if (prog.NeedsSuperglobals || !prog.Main.CVMetaResolved) && !vm.superglobalsInit {
		vm.initSuperglobals()
		vm.superglobalsInit = true
	}
	if !vm.progCloned && vm.Program != nil {
		copyProgram := *vm.Program
		copyProgram.Funcs = append([]*bytecode.OpArray(nil), vm.Program.Funcs...)
		vm.Program = &copyProgram
		vm.progCloned = true
	}
	baseFunc := uint32(len(vm.Program.Funcs))
	for _, fn := range append([]*bytecode.OpArray{prog.Main}, prog.Funcs...) {
		for i := range fn.Code {
			op := &fn.Code[i]
			if op.Op == bytecode.OpBindClosure || op.Op == bytecode.OpInitCall && op.Flags&(bytecode.FlagNative|bytecode.FlagClosure) == 0 {
				op.Op1 += baseFunc
			}
		}
	}
	nextID := uint32(2)
	if vm.Program.Main != nil && vm.Program.Main.FuncID >= nextID {
		nextID = vm.Program.Main.FuncID + 1
	}
	for _, fn := range vm.Program.Funcs {
		if fn != nil && fn.FuncID >= nextID {
			nextID = fn.FuncID + 1
		}
	}
	for _, c := range vm.Program.Classes {
		if c != nil {
			for _, m := range c.Methods {
				if m.Func != nil && m.Func.FuncID >= nextID {
					nextID = m.Func.FuncID + 1
				}
			}
		}
	}
	for _, fn := range prog.Funcs {
		if fn != nil {
			fn.FuncID = nextID
			nextID++
		}
	}
	vm.Program.Funcs = append(vm.Program.Funcs, prog.Funcs...)
	if isInclude {
		if vm.included == nil {
			vm.included = make(map[string]bool)
		}
		vm.included[filename] = true
		last := &prog.Main.Code[len(prog.Main.Code)-1]
		last.Flags = 0
		last.Op = bytecode.OpConst
		last.Op1 = uint32(len(prog.Main.Consts))
		prog.Main.Consts = append(prog.Main.Consts, types.NewLong(1))
		prog.Main.Code = append(prog.Main.Code, bytecode.Instr{Op: bytecode.OpReturn, Line: last.Line})
		prog.Main.MaxStack = max(prog.Main.MaxStack, 1)
		if err := bytecode.Verify(prog.Main); err != nil {
			vm.fatalFeature(err.Error())
			return
		}
	}
	callerIndex := len(vm.frames) - 1
	caller := vm.frame()
	if caller.dynamicLocals == nil {
		caller.dynamicLocals = make(map[string]types.Value)
	}
	locals := caller.dynamicLocals
	for i, name := range caller.fn.CVNames {
		if strings.ContainsRune(name, 0) || isSuperglobal(name) || name == "GLOBALS" {
			continue
		}
		slot := &vm.slots[caller.cvBase+i]
		if slot.Type != types.TypeReference {
			ref, err := types.NewReference(vm.Zone, *slot)
			if err != nil {
				vm.fatalFeature("reference de portee")
				return
			}
			types.ReleaseValue(vm.Zone, slot)
			*slot = ref
		}
		if old, ok := locals[name]; ok {
			types.ReleaseValue(vm.Zone, &old)
		}
		locals[name] = types.RetainReference(vm.Zone, *slot)
	}
	if !vm.pushFrame(prog.Main, caller.calledClass, caller.scopeClass, caller.this, caller.hasThis, in.Line) {
		return
	}
	for i, name := range prog.Main.CVNames {
		if strings.ContainsRune(name, 0) || isSuperglobal(name) || name == "GLOBALS" {
			continue
		}
		ref, ok := locals[name]
		if !ok {
			ref, err = types.NewReference(vm.Zone, types.NewNull())
			if err != nil {
				vm.fatalFeature("reference de portee")
				return
			}
			locals[name] = ref
		}
		slot := &vm.slots[vm.frame().cvBase+i]
		if slot.Type != types.TypeUndef {
			types.ReleaseValue(vm.Zone, slot)
		}
		*slot = types.RetainReference(vm.Zone, ref)
	}
	vm.execFrames(callerIndex + 1)
	val, threw := vm.takeReturn()
	if vm.stop || threw {
		return
	}
	vm.control = false
	vm.push(val)
	vm.bump()
}
