// Package compiler traduit un arbre PHP en bytecode verifie.
package compiler

import (
	"code.hazyhaar.fr/devhoros/php127/ast"
	bc "code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/parser"
	"code.hazyhaar.fr/devhoros/php127/types"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type compilation struct {
	file        *ast.File
	prog        *bc.Program
	funcs       map[string]uint32
	classes     map[string]*bc.ClassProto
	nextClassID uint32
}

func (c *compilation) allocClassID() uint32 {
	if c.nextClassID < bc.ClassUser {
		c.nextClassID = bc.ClassUser
	}
	id := c.nextClassID
	c.nextClassID++
	return id
}

type loop struct {
	breaks, continues []int
	iter              int
}

type gotoPatch struct {
	name string
	pc   int
}

type emitter struct {
	*compilation
	fn           *bc.OpArray
	cvs          map[string]uint32
	loops        []*loop
	line         uint32
	temps, iters int
	class        *bc.ClassProto
	namespace    string
	labels       map[string]uint32
	gotos        []gotoPatch
}

func CompileSource(src string, filename string) (*bc.Program, error) {
	file, err := parser.Parse(src, filename)
	if err != nil {
		return nil, err
	}
	return Compile(file)
}
func Compile(file *ast.File) (prog *bc.Program, err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*parser.CompileError); ok {
				prog = nil
				err = e
			} else {
				panic(r)
			}
		}
	}()
	if file == nil {
		return nil, fmt.Errorf("compiler: arbre absent")
	}
	c := &compilation{file: file, prog: &bc.Program{}, funcs: make(map[string]uint32), classes: make(map[string]*bc.ClassProto), nextClassID: bc.ClassUser}
	main := c.emitter("{main}")
	c.prog.Main = main.fn

	type funcEntry struct {
		decl *ast.FunctionDecl
		ns   string
	}
	var funcDecls []funcEntry
	var collectFuncs func(stmts []ast.Stmt, ns string, topLevel bool)
	collectFuncs = func(stmts []ast.Stmt, ns string, topLevel bool) {
		currentNS := ns
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.NamespaceDecl:
				if len(s.Body) > 0 {
					collectFuncs(s.Body, s.Name, topLevel)
				} else {
					currentNS = s.Name
				}
			case *ast.DeclareStmt:
				collectFuncs(s.Body, currentNS, topLevel)
			case *ast.IfStmt:
				collectFuncs(s.Body, currentNS, false)
				for _, cl := range s.ElseIfs {
					collectFuncs(cl.Body, currentNS, false)
				}
				collectFuncs(s.Else, currentNS, false)
			case *ast.BlockStmt:
				collectFuncs(s.Body, currentNS, false)
			case *ast.SwitchStmt:
				for _, cs := range s.Cases {
					collectFuncs(cs.Body, currentNS, false)
				}
			case *ast.TryStmt:
				collectFuncs(s.Body, currentNS, false)
				for _, ct := range s.Catches {
					collectFuncs(ct.Body, currentNS, false)
				}
				collectFuncs(s.Finally, currentNS, false)
			case *ast.FunctionDecl:
				name := strings.ToLower(s.Name)
				if _, exists := c.funcs[name]; exists {
					if topLevel {
						main.fail("redeclaration de fonction " + s.Name)
					}
					collectFuncs(s.Body, currentNS, false)
					continue
				}
				c.funcs[name] = uint32(len(c.prog.Funcs))
				c.prog.Funcs = append(c.prog.Funcs, nil)
				funcDecls = append(funcDecls, funcEntry{decl: s, ns: currentNS})
				collectFuncs(s.Body, currentNS, false)
			}
		}
	}
	collectFuncs(file.Stmts, "", true)
	main.declareClasses()
	for _, fe := range funcDecls {
		f := fe.decl
		e := c.emitter(f.Name)
		e.namespace = fe.ns
		e.line = uint32(f.Line)
		e.fn.ReturnByRef = f.ByRef
		e.params(f.Params)
		e.fn.ReturnType = typeName(f.ReturnType)
		e.fn.Attributes = e.attributes(f.Attrs)
		e.stmts(f.Body)
		e.end()
		c.prog.Funcs[c.funcs[strings.ToLower(f.Name)]] = e.fn
	}
	for _, stmt := range file.Stmts {
		switch stmt.(type) {
		case *ast.FunctionDecl, *ast.ClassDecl, *ast.InterfaceDecl, *ast.TraitDecl, *ast.EnumDecl:
		default:
			main.stmt(stmt)
		}
	}
	main.end()
	functions := append([]*bc.OpArray{c.prog.Main}, c.prog.Funcs...)
	for _, cl := range c.classes {
		for _, m := range cl.Methods {
			if m.Func != nil {
				functions = append(functions, m.Func)
			}
		}
	}
	for _, fn := range functions {
		if fn == nil {
			continue
		}
		if err := stackSize(fn); err != nil {
			return nil, err
		}
		if err := bc.Verify(fn); err != nil {
			return nil, err
		}
	}
	if err := bc.Link(c.prog); err != nil {
		return nil, err
	}
	return c.prog, nil
}
func (c *compilation) emitter(name string) *emitter {
	return &emitter{compilation: c, fn: &bc.OpArray{Name: name, Filename: c.file.Filename, StrictTypes: c.file.StrictTypes}, cvs: make(map[string]uint32), line: 1}
}
func (e *emitter) fail(feature string) {
	panic(&parser.CompileError{Message: "fonctionnalite absente: " + feature, Filename: e.file.Filename, Line: int(e.line)})
}
func (e *emitter) cv(name string) uint32 {
	if idx, ok := e.cvs[name]; ok {
		return idx
	}
	idx := uint32(len(e.fn.CVNames))
	e.cvs[name] = idx
	e.fn.CVNames = append(e.fn.CVNames, name)
	e.fn.NumCVs++
	return idx
}
func (e *emitter) temp() uint32 { e.temps++; return e.cv("\x00tmp" + strconv.Itoa(e.temps)) }
func (e *emitter) str(s string) uint32 {
	for i, v := range e.fn.Strings {
		if v == s {
			return uint32(i)
		}
	}
	e.fn.Strings = append(e.fn.Strings, s)
	return uint32(len(e.fn.Strings) - 1)
}
func (e *emitter) emit(op bc.Opcode, a, b uint32, flags uint16) int {
	pc := len(e.fn.Code)
	e.fn.Code = append(e.fn.Code, bc.Instr{Op: op, Op1: a, Op2: b, Flags: flags, Line: e.line})
	e.fn.Aux = append(e.fn.Aux, bc.NoCV)
	return pc
}
func (e *emitter) pc() uint32   { return uint32(len(e.fn.Code)) }
func (e *emitter) patch(pc int) { e.fn.Code[pc].Op1 = e.pc() }
func (e *emitter) value(v types.Value) {
	for i, c := range e.fn.Consts {
		if c == v {
			e.emit(bc.OpConst, uint32(i), 0, 0)
			return
		}
	}
	i := uint32(len(e.fn.Consts))
	e.fn.Consts = append(e.fn.Consts, v)
	e.emit(bc.OpConst, i, 0, 0)
}
func (e *emitter) text(s string) { e.emit(bc.OpConst, e.str(s), 0, bc.FlagStringConst) }
func (e *emitter) end() {
	for _, g := range e.gotos {
		if target, ok := e.labels[g.name]; ok {
			e.fn.Code[g.pc].Op1 = target
		} else {
			e.fail("label absent " + g.name)
		}
	}
	e.gotos = nil
	e.emit(bc.OpReturn, 0, 0, bc.FlagReturnVoid)
}
func (e *emitter) attributes(attrs []ast.Attribute) []bc.AttributeProto {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]bc.AttributeProto, len(attrs))
	for i, a := range attrs {
		ap := bc.AttributeProto{Name: a.Name}
		for _, arg := range a.Args {
			ap.Args = append(ap.Args, bc.AttributeArg{Name: arg.Name, Value: e.constant(arg.Value)})
		}
		out[i] = ap
	}
	return out
}
func (e *emitter) params(params []ast.Param) {
	for _, p := range params {
		e.cv(p.Name)
		info := bc.ParamInfo{
			Name:       p.Name,
			ByRef:      p.ByRef,
			Variadic:   p.Variadic,
			Promoted:   p.Modifiers&(ast.Public|ast.Protected|ast.Private) != 0,
			TypeName:   typeName(p.Type),
			Attributes: e.attributes(p.Attrs),
		}
		if p.Default != nil {
			c := e.constant(p.Default)
			info.DefaultConst = c
			info.HasDefault = true
			if c.IsString {
				info.DefaultString = c.Text
				info.HasDefaultString = true
			} else if !c.IsArray {
				info.Default = c.Value
			}
		}
		e.fn.Params = append(e.fn.Params, info)
	}
}
func (e *emitter) literal(expr ast.Expr) (types.Value, string, bool) {
	switch x := expr.(type) {
	case *ast.IntLit:
		return types.NewLong(x.Value), "", false
	case *ast.FloatLit:
		return types.NewDouble(x.Value), "", false
	case *ast.StringLit:
		return types.Value{}, x.Value, true
	case *ast.ConstFetchExpr:
		switch strings.ToLower(strings.TrimPrefix(x.Name, `\`)) {
		case "null":
			return types.NewNull(), "", false
		case "true":
			return types.NewBool(true), "", false
		case "false":
			return types.NewBool(false), "", false
		case "php_int_max":
			return types.NewLong(math.MaxInt64), "", false
		case "php_int_min":
			return types.NewLong(math.MinInt64), "", false
		case "php_int_size":
			return types.NewLong(8), "", false
		case "php_eol":
			return types.Value{}, "\n", true
		case "directory_separator":
			return types.Value{}, "/", true
		case "path_separator":
			return types.Value{}, ":", true
		case "nan":
			return types.NewDouble(math.NaN()), "", false
		case "inf":
			return types.NewDouble(math.Inf(1)), "", false
		default:
			return types.Value{}, x.Name, true
		}
	}
	return types.NewNull(), "", false
}
func (e *emitter) variable(expr ast.Expr) uint32 {
	if x, ok := expr.(*ast.VarExpr); ok {
		if x.Dynamic != nil {
			return e.cv("$" + e.name(x.Dynamic))
		}
		return e.cv(x.Name)
	}
	return e.temp()
}
func (e *emitter) stmts(stmts []ast.Stmt) {
	for _, s := range stmts {
		e.stmt(s)
	}
}
func (e *emitter) discard(exprs []ast.Expr) {
	for _, x := range exprs {
		e.discardExpr(x)
	}
}
func (e *emitter) discardExpr(expr ast.Expr) {
	if a, ok := expr.(*ast.AssignExpr); ok && !a.ByRef {
		if isSimpleForeachVar(a.Left) && !isGlobals(a.Left) {
			if a.Op == "=" {
				line := e.line
				e.line = uint32(a.Location().Line)
				idx := e.variable(a.Left)
				e.expr(a.Right)
				e.emit(bc.OpStoreCV, idx, 0, 0)
				e.line = line
				return
			}
			if op, ok := binaryOps[strings.TrimSuffix(a.Op, "=")]; ok {
				line := e.line
				e.line = uint32(a.Location().Line)
				idx := e.variable(a.Left)
				e.emit(bc.OpLoadCV, idx, 0, 0)
				e.expr(a.Right)
				e.emit(op, 0, 0, 0)
				e.emit(bc.OpStoreCV, idx, 0, 0)
				e.line = line
				return
			}
		}
		if d, ok := a.Left.(*ast.DimFetchExpr); ok {
			if v, ok := isFastDimCV(d.Var); ok {
				if a.Op == "=" {
					line := e.line
					e.line = uint32(a.Location().Line)
					idx := e.cv(v.Name)
					if d.Dim != nil {
						e.expr(d.Dim)
						e.expr(a.Right)
						e.emit(bc.OpAssignDim, idx, 0, bc.FlagAssignDimCV|bc.FlagDiscard)
					} else {
						e.expr(a.Right)
						e.emit(bc.OpAppendArrayElement, idx, 0, bc.FlagAssignDimCV|bc.FlagDiscard)
					}
					e.line = line
					return
				}
				if d.Dim != nil {
					if op, ok := binaryOps[strings.TrimSuffix(a.Op, "=")]; ok && isCompoundAssignOp(op) {
						line := e.line
						e.line = uint32(a.Location().Line)
						idx := e.cv(v.Name)
						e.expr(d.Dim)
						e.expr(a.Right)
						e.emit(bc.OpAssignDimOp, idx, uint32(op), bc.FlagAssignDimCV|bc.FlagDiscard)
						e.line = line
						return
					}
				}
			}
		}
		if p, ok := a.Left.(*ast.PropFetchExpr); ok {
			if a.Op == "=" {
				line := e.line
				e.line = uint32(a.Location().Line)
				e.expr(p.Object)
				e.expr(a.Right)
				e.emit(bc.OpAssignProp, e.str(e.memberName(p.Property)), 0, bc.FlagDiscard)
				e.line = line
				return
			}
			if op, ok := binaryOps[strings.TrimSuffix(a.Op, "=")]; ok && isCompoundAssignOp(op) {
				line := e.line
				e.line = uint32(a.Location().Line)
				e.expr(p.Object)
				e.expr(a.Right)
				e.emit(bc.OpAssignPropOp, e.str(e.memberName(p.Property)), uint32(op), bc.FlagDiscard)
				e.line = line
				return
			}
		}
	}
	if x, ok := expr.(*ast.IncDecExpr); ok {
		if _, simple := x.Expr.(*ast.VarExpr); simple {
			line := e.line
			e.line = uint32(x.Location().Line)
			op := bc.OpPreIncCV
			if x.Op == "--" {
				op = bc.OpPreDecCV
			}
			if x.Post {
				if x.Op == "--" {
					op = bc.OpPostDecCV
				} else {
					op = bc.OpPostIncCV
				}
			}
			e.emit(op, e.variable(x.Expr), 0, bc.FlagDiscard)
			e.line = line
			return
		}
	}
	e.expr(expr)
	e.emit(bc.OpPop, 0, 0, 0)
}
func (e *emitter) openLoop() *loop { l := &loop{iter: -1}; e.loops = append(e.loops, l); return l }
func (e *emitter) closeLoop(l *loop, cont uint32) {
	for _, p := range l.breaks {
		e.patch(p)
	}
	for _, p := range l.continues {
		e.fn.Code[p].Op1 = cont
	}
	e.loops = e.loops[:len(e.loops)-1]
}
func (e *emitter) transfer(level ast.Expr, cont bool) {
	n := 1
	if level != nil {
		x, ok := level.(*ast.IntLit)
		if !ok {
			e.fail("niveau de boucle non entier")
		}
		n = int(x.Value)
	}
	if n < 1 || n > len(e.loops) {
		e.fail("niveau de boucle invalide")
	}
	idx := len(e.loops) - n
	for i := len(e.loops) - 1; i >= idx; i-- {
		if cont && i == idx {
			break
		}
		if e.loops[i].iter >= 0 {
			e.emit(bc.OpFeFree, 0, uint32(e.loops[i].iter), 0)
		}
	}
	p := e.emit(bc.OpJmp, 0, 0, 0)
	l := e.loops[idx]
	if cont {
		l.continues = append(l.continues, p)
	} else {
		l.breaks = append(l.breaks, p)
	}
}

func (e *emitter) stmt(stmt ast.Stmt) {
	if stmt == nil {
		return
	}
	e.line = uint32(stmt.Location().Line)
	switch s := stmt.(type) {
	case *ast.NamespaceDecl:
		prevNS := e.namespace
		e.namespace = s.Name
		if len(s.Body) > 0 {
			e.stmts(s.Body)
			e.namespace = prevNS
		}
	case *ast.UseDecl, *ast.HaltCompilerStmt, *ast.ClassDecl, *ast.InterfaceDecl, *ast.TraitDecl, *ast.EnumDecl:
	case *ast.FunctionDecl:
		name := strings.ToLower(s.Name)
		if _, exists := e.funcs[name]; !exists {
			idx := uint32(len(e.prog.Funcs))
			e.funcs[name] = idx
			e.prog.Funcs = append(e.prog.Funcs, nil)
			child := e.compilation.emitter(s.Name)
			child.namespace = e.namespace
			child.line = uint32(s.Line)
			child.fn.ReturnByRef = s.ByRef
			child.params(s.Params)
			child.fn.ReturnType = typeName(s.ReturnType)
			child.fn.Attributes = child.attributes(s.Attrs)
			child.stmts(s.Body)
			child.end()
			e.prog.Funcs[idx] = child.fn
		}
	case *ast.ConstDecl:
		for _, ci := range s.Consts {
			e.emit(bc.OpInitCall, e.str("define"), 0, bc.FlagNative)
			e.text(ci.Name)
			e.emit(bc.OpSendVal, 0, 0, 0)
			e.expr(ci.Value)
			e.emit(bc.OpSendVal, 0, 0, 0)
			e.emit(bc.OpDoCall, 0, 0, 0)
			e.emit(bc.OpPop, 0, 0, 0)
		}
	case *ast.LabelStmt:
		if e.labels == nil {
			e.labels = make(map[string]uint32)
		}
		e.labels[s.Label] = e.pc()
	case *ast.GotoStmt:
		pc := e.emit(bc.OpJmp, 0, 0, 0)
		e.gotos = append(e.gotos, gotoPatch{name: s.Label, pc: pc})
	case *ast.GlobalStmt:
		for _, v := range s.Vars {
			e.emit(bc.OpBindGlobal, e.variable(v), 0, 0)
		}
	case *ast.StaticVarStmt:
		for _, v := range s.Vars {
			idx := e.cv(v.Name)
			check := e.emit(bc.OpStaticCheck, idx, 0, 0)
			e.expr(v.Default)
			e.emit(bc.OpBindStatic, idx, 0, 0)
			e.fn.Code[check].Op2 = e.pc()
		}
	case *ast.NopStmt:
	case *ast.BlockStmt:
		e.stmts(s.Body)
	case *ast.DeclareStmt:
		e.stmts(s.Body)
	case *ast.EchoStmt:
		for _, x := range s.Exprs {
			e.expr(x)
			e.emit(bc.OpEcho, 0, 0, 0)
		}
	case *ast.InlineHTMLStmt:
		e.text(s.Text)
		e.emit(bc.OpEcho, 0, 0, 0)
	case *ast.ExprStmt:
		e.discardExpr(s.Expr)
	case *ast.ReturnStmt:
		if s.Expr == nil {
			e.emit(bc.OpReturn, 0, 0, bc.FlagReturnVoid)
		} else {
			e.expr(s.Expr)
			e.emit(bc.OpReturn, 0, 0, 0)
		}
	case *ast.ThrowStmt:
		e.expr(s.Expr)
		e.emit(bc.OpThrow, 0, 0, 0)
	case *ast.IfStmt:
		clauses := append([]ast.ElseIf{{Cond: s.Cond, Body: s.Body}}, s.ElseIfs...)
		var ends []int
		for _, cl := range clauses {
			e.expr(cl.Cond)
			skip := e.emit(bc.OpJmpZ, 0, 0, 0)
			e.stmts(cl.Body)
			ends = append(ends, e.emit(bc.OpJmp, 0, 0, 0))
			e.patch(skip)
		}
		e.stmts(s.Else)
		for _, p := range ends {
			e.patch(p)
		}
	case *ast.WhileStmt:
		l := e.openLoop()
		start := e.pc()
		e.expr(s.Cond)
		done := e.emit(bc.OpJmpZ, 0, 0, 0)
		e.stmts(s.Body)
		e.emit(bc.OpJmp, start, 0, 0)
		e.patch(done)
		e.closeLoop(l, start)
	case *ast.DoWhileStmt:
		l := e.openLoop()
		start := e.pc()
		e.stmts(s.Body)
		cont := e.pc()
		e.expr(s.Cond)
		e.emit(bc.OpJmpNZ, start, 0, 0)
		e.closeLoop(l, cont)
	case *ast.ForStmt:
		e.discard(s.Init)
		l := e.openLoop()
		start := e.pc()
		done := -1
		if len(s.Cond) > 0 {
			e.discard(s.Cond[:len(s.Cond)-1])
			e.expr(s.Cond[len(s.Cond)-1])
			done = e.emit(bc.OpJmpZ, 0, 0, 0)
		}
		e.stmts(s.Body)
		cont := e.pc()
		e.discard(s.Loop)
		e.emit(bc.OpJmp, start, 0, 0)
		if done >= 0 {
			e.patch(done)
		}
		e.closeLoop(l, cont)
	case *ast.BreakStmt:
		e.transfer(s.Level, false)
	case *ast.ContinueStmt:
		e.transfer(s.Level, true)
	case *ast.SwitchStmt:
		e.expr(s.Cond)
		tmp := e.temp()
		e.emit(bc.OpStoreCV, tmp, 0, 0)
		l := e.openLoop()
		jumps := make([]int, len(s.Cases))
		def := -1
		for i, c := range s.Cases {
			jumps[i] = -1
			if c.Cond == nil {
				def = i
				continue
			}
			e.emit(bc.OpLoadCV, tmp, 0, 0)
			e.expr(c.Cond)
			e.emit(bc.OpIsEqual, 0, 0, 0)
			jumps[i] = e.emit(bc.OpJmpNZ, 0, 0, 0)
		}
		fallback := e.emit(bc.OpJmp, 0, 0, 0)
		for i, c := range s.Cases {
			if i == def {
				e.patch(fallback)
			}
			if jumps[i] >= 0 {
				e.patch(jumps[i])
			}
			e.stmts(c.Body)
		}
		if def < 0 {
			e.patch(fallback)
		}
		e.closeLoop(l, e.pc())
	case *ast.ForeachStmt:
		e.foreach(s)
	case *ast.TryStmt:
		e.tryStmt(s)
	case *ast.UnsetStmt:
		for _, x := range s.Vars {
			switch v := x.(type) {
			case *ast.VarExpr:
				e.emit(bc.OpUnsetCV, e.variable(v), 0, 0)
			case *ast.PropFetchExpr:
				e.expr(v.Object)
				e.emit(bc.OpUnsetProp, e.str(e.memberName(v.Property)), 0, 0)
			case *ast.StaticPropFetchExpr:
				id, flags := e.classOperand(v.Class)
				e.value(types.NewNull())
				e.emit(bc.OpAssignStaticProp, e.str(e.staticName(v.Property)), id, flags)
				e.emit(bc.OpPop, 0, 0, 0)
			case *ast.DimFetchExpr:
				if isGlobals(v.Var) {
					e.expr(v.Dim)
					e.emit(bc.OpGlobalUnset, 0, 0, 0)
					continue
				}
				if simpleVar, ok := v.Var.(*ast.VarExpr); ok && simpleVar.Dynamic == nil && simpleVar.Name != "this" {
					root := e.variable(v.Var)
					e.expr(v.Var)
					e.expr(v.Dim)
					e.emit(bc.OpUnsetDim, 0, 0, 0)
					e.emit(bc.OpStoreCV, root, 0, 0)
					continue
				}
				base, temps := e.stableTarget(v.Var)
				e.expr(base)
				e.expr(v.Dim)
				e.emit(bc.OpUnsetDim, 0, 0, 0)
				tmp := e.temp()
				e.emit(bc.OpStoreCV, tmp, 0, 0)
				e.assign(base, &ast.VarExpr{Name: e.fn.CVNames[tmp]})
				e.emit(bc.OpPop, 0, 0, 0)
				for _, idx := range append(temps, tmp) {
					e.emit(bc.OpUnsetCV, idx, 0, 0)
				}
			default:
				e.expr(x)
				e.emit(bc.OpPop, 0, 0, 0)
			}
		}
	default:
		e.fail(fmt.Sprintf("instruction %T", stmt))
	}
}
func isSimpleForeachVar(expr ast.Expr) bool {
	v, ok := expr.(*ast.VarExpr)
	return ok && v.Dynamic == nil && v.Name != "this"
}
func (e *emitter) foreach(s *ast.ForeachStmt) {
	var val uint32
	complexVal := !isSimpleForeachVar(s.Val)
	if complexVal {
		val = e.temp()
	} else {
		val = e.variable(s.Val)
	}
	key := bc.NoCV
	complexKey := false
	if s.Key != nil {
		if isSimpleForeachVar(s.Key) {
			key = e.variable(s.Key)
		} else {
			key = e.temp()
			complexKey = true
		}
	}
	l := e.openLoop()
	l.iter = e.iters
	e.iters++
	id := uint32(l.iter)
	var reset int
	var arr uint32
	if s.ByRef {
		if isSimpleForeachVar(s.Expr) {
			arr = e.variable(s.Expr)
		} else {
			e.expr(s.Expr)
			arr = e.temp()
			e.emit(bc.OpStoreCV, arr, 0, 0)
		}
		reset = e.emit(bc.OpFeResetRW, id, arr, 0)
	} else {
		e.expr(s.Expr)
		arr = e.temp()
		e.emit(bc.OpAssignCV, arr, 0, 0)
		reset = e.emit(bc.OpFeResetR, 0, id, 0)
		e.emit(bc.OpPop, 0, 0, 0)
	}
	cont := e.pc()
	op := bc.OpFeFetchRW
	if !s.ByRef {
		e.emit(bc.OpLoadCV, arr, 0, 0)
		op = bc.OpFeFetchR
	}
	fetch := e.emit(op, 0, id, 0)
	e.fn.Code[fetch].Result = val
	if key != bc.NoCV {
		e.fn.Code[fetch].Flags = bc.FlagHasKey
		e.fn.Aux[fetch] = key
	}
	if !s.ByRef {
		e.emit(bc.OpPop, 0, 0, 0)
	}
	if complexKey {
		e.assignTarget(s.Key, &ast.VarExpr{Name: e.fn.CVNames[key]})
		e.emit(bc.OpPop, 0, 0, 0)
	}
	if complexVal {
		e.assignTarget(s.Val, &ast.VarExpr{Name: e.fn.CVNames[val]})
		e.emit(bc.OpPop, 0, 0, 0)
	}
	e.stmts(s.Body)
	e.emit(bc.OpJmp, cont, 0, 0)
	if s.ByRef {
		e.fn.Code[reset].Result = e.pc()
	} else {
		e.patch(reset)
	}
	e.patch(fetch)
	e.emit(bc.OpFeFree, 0, id, 0)
	e.closeLoop(l, cont)
}
func (e *emitter) classID(name string) uint32 {
	clean := strings.TrimPrefix(name, `\`)
	if id, ok := bc.BuiltinClassID(clean); ok {
		return id
	}
	key := strings.ToLower(clean)
	if cl := e.classes[key]; cl != nil {
		return cl.ID
	}
	p := &bc.ClassProto{Name: clean, ID: e.allocClassID()}
	e.classes[key] = p
	e.prog.Classes = append(e.prog.Classes, p)
	return p.ID
}
func (e *emitter) tryStmt(s *ast.TryStmt) {
	idx := len(e.fn.TryTable)
	e.fn.TryTable = append(e.fn.TryTable, bc.TryCatchEntry{})
	entry := bc.TryCatchEntry{StartPC: e.pc(), FinallyPC: bc.NoPC, FinallyEndPC: bc.NoPC}
	e.emit(bc.OpNop, 0, 0, 0)
	e.stmts(s.Body)
	out := []int{e.emit(bc.OpJmp, 0, 0, 0)}
	entry.EndPC = e.pc()
	for _, c := range s.Catches {
		target := bc.CatchTarget{CV: -1, PC: e.pc()}
		if c.Var != "" {
			target.CV = int32(e.cv(c.Var))
		}
		for _, name := range c.Types {
			target.Classes = append(target.Classes, e.classID(name))
		}
		entry.CatchTargets = append(entry.CatchTargets, target)
		e.emit(bc.OpEnterCatch, uint32(target.CV), 0, 0)
		e.stmts(c.Body)
		out = append(out, e.emit(bc.OpJmp, 0, 0, 0))
	}
	if s.Finally != nil {
		entry.FinallyPC = e.pc()
		e.emit(bc.OpEnterFinally, uint32(idx), 0, 0)
		e.stmts(s.Finally)
		e.emit(bc.OpLeaveFinally, uint32(idx), 0, 0)
		entry.FinallyEndPC = e.pc()
	}
	for _, pc := range out {
		e.patch(pc)
	}
	e.fn.TryTable[idx] = entry
}
