package compiler

import (
	"code.hazyhaar.fr/devhoros/php127/ast"
	bc "code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
	"fmt"
	"reflect"
	"strings"
)

var binaryOps = map[string]bc.Opcode{"+": bc.OpAdd, "-": bc.OpSub, "*": bc.OpMul, "/": bc.OpDiv, "%": bc.OpMod, "**": bc.OpPow, "<<": bc.OpSL, ">>": bc.OpSR, ".": bc.OpConcat, "|": bc.OpBWOr, "&": bc.OpBWAnd, "^": bc.OpBWXor, "xor": bc.OpBoolXor, "===": bc.OpIsIdentical, "!==": bc.OpIsNotIdentical, "==": bc.OpIsEqual, "!=": bc.OpIsNotEqual, "<>": bc.OpIsNotEqual, "<": bc.OpIsSmaller, "<=": bc.OpIsSmallerOrEqual, "<=>": bc.OpSpaceship}

func (e *emitter) expr(expr ast.Expr) {
	if expr == nil {
		e.value(types.NewNull())
		return
	}
	line := e.line
	e.line = uint32(expr.Location().Line)
	defer func() { e.line = line }()
	switch x := expr.(type) {
	case *ast.MagicConstExpr:
		iv, sv, isStr := e.magicConstText(x.Name, x.Line)
		if isStr {
			e.text(sv)
		} else {
			e.value(types.NewLong(iv))
		}
	case *ast.YieldExpr:
		e.fn.IsGenerator = true
		flags := uint16(0)
		if x.Key != nil {
			e.expr(x.Key)
			flags = bc.FlagHasKey
		}
		e.expr(x.Value)
		e.emit(bc.OpYield, 0, 0, flags)
	case *ast.YieldFromExpr:
		e.fn.IsGenerator = true
		e.expr(x.Expr)
		e.emit(bc.OpYieldFrom, 0, 0, 0)
	case *ast.EvalExpr:
		e.expr(x.Expr)
		e.emit(bc.OpEval, 0, 0, 0)
	case *ast.IncludeExpr:
		e.expr(x.Expr)
		e.emit(bc.OpInclude, e.str(x.Kind), 0, 0)
	case *ast.IntLit:
		e.value(types.NewLong(x.Value))
	case *ast.FloatLit:
		e.value(types.NewDouble(x.Value))
	case *ast.StringLit:
		e.text(x.Value)
	case *ast.EncapsedStringExpr:
		if x.Kind == "backtick" {
			e.fail("execution de commande")
		}
		e.text("")
		for _, part := range x.Parts {
			e.expr(part)
			e.emit(bc.OpConcat, 0, 0, 0)
		}
	case *ast.ConstFetchExpr:
		switch strings.ToLower(x.Name) {
		case "null":
			e.value(types.NewNull())
		case "true":
			e.value(types.NewBool(true))
		case "false":
			e.value(types.NewBool(false))
		default:
			e.emit(bc.OpConst, e.str(x.Name), 0, bc.FlagNamedConst)
		}
	case *ast.VarExpr:
		if x.Name == "this" && x.Dynamic == nil {
			e.emit(bc.OpLoadCV, e.cv("this"), 0, bc.FlagUseThis)
			break
		}
		e.emit(bc.OpLoadCV, e.variable(x), 0, 0)
	case *ast.BinaryExpr:
		switch x.Op {
		case "instanceof":
			e.expr(x.Left)
			id, _ := e.classOperand(x.Right)
			e.emit(bc.OpInstanceof, id, 0, 0)
		case "&&", "and", "||", "or":
			e.expr(x.Left)
			op := bc.OpJmpZ
			truth := false
			if x.Op == "||" || x.Op == "or" {
				op = bc.OpJmpNZ
				truth = true
			}
			p := e.emit(op, 0, 0, 0)
			e.expr(x.Right)
			e.emit(bc.OpCast, bc.CastBool, 0, 0)
			end := e.emit(bc.OpJmp, 0, 0, 0)
			e.patch(p)
			e.value(types.NewBool(truth))
			e.patch(end)
		case "??":
			e.exprCoalesce(x.Left)
			p := e.emit(bc.OpJmpNull, 0, 0, 0)
			end := e.emit(bc.OpJmp, 0, 0, 0)
			e.patch(p)
			e.expr(x.Right)
			e.patch(end)
		case ">", ">=":
			e.expr(x.Left)
			a := e.temp()
			e.emit(bc.OpStoreCV, a, 0, 0)
			e.expr(x.Right)
			e.emit(bc.OpLoadCV, a, 0, 0)
			op := bc.OpIsSmaller
			if x.Op == ">=" {
				op = bc.OpIsSmallerOrEqual
			}
			e.emit(op, 0, 0, 0)
		default:
			op, ok := binaryOps[x.Op]
			if !ok {
				e.fail("operateur " + x.Op)
			}
			e.expr(x.Left)
			e.expr(x.Right)
			e.emit(op, 0, 0, 0)
		}
	case *ast.UnaryExpr:
		if x.Op == "@" {
			e.expr(x.Expr)
			break
		}
		if x.Op == "(unset)" {
			e.expr(x.Expr)
			e.emit(bc.OpPop, 0, 0, 0)
			e.value(types.NewNull())
			break
		}
		e.expr(x.Expr)
		ops := map[string]bc.Opcode{"!": bc.OpBoolNot, "~": bc.OpBWNot, "+": bc.OpUnaryPlus, "-": bc.OpUnaryMinus, "clone": bc.OpClone}
		if op, ok := ops[x.Op]; ok {
			e.emit(op, 0, 0, 0)
		} else {
			casts := map[string]uint32{
				"(int)":     bc.CastInt,
				"(integer)": bc.CastInt,
				"(float)":   bc.CastFloat,
				"(double)":  bc.CastFloat,
				"(real)":    bc.CastFloat,
				"(string)":  bc.CastString,
				"(binary)":  bc.CastString,
				"(bool)":    bc.CastBool,
				"(boolean)": bc.CastBool,
				"(array)":   bc.CastArray,
				"(object)":  bc.CastObject,
			}
			if cast, ok := casts[x.Op]; ok {
				e.emit(bc.OpCast, cast, 0, 0)
			} else {
				e.fail("operateur unaire " + x.Op)
			}
		}
	case *ast.IncDecExpr:
		if _, simple := x.Expr.(*ast.VarExpr); !simple {
			left, temps := e.stableTarget(x.Expr)
			e.expr(left)
			current := e.temp()
			e.emit(bc.OpStoreCV, current, 0, 0)
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
			e.emit(op, current, 0, 0)
			result := e.temp()
			e.emit(bc.OpStoreCV, result, 0, 0)
			e.assign(left, &ast.VarExpr{Name: e.fn.CVNames[current]})
			e.emit(bc.OpPop, 0, 0, 0)
			e.emit(bc.OpLoadCV, result, 0, 0)
			for _, idx := range append(temps, current, result) {
				e.emit(bc.OpUnsetCV, idx, 0, 0)
			}
			break
		}
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
		e.emit(op, e.variable(x.Expr), 0, 0)
	case *ast.AssignExpr:
		if x.ByRef {
			if isSimpleForeachVar(x.Left) && isSimpleForeachVar(x.Right) {
				a, b := e.variable(x.Left), e.variable(x.Right)
				e.emit(bc.OpAssignRefCV, a, b, 0)
				e.emit(bc.OpLoadCV, a, 0, 0)
			} else if isSimpleForeachVar(x.Left) && e.exprRef(x.Right) {
				a := e.variable(x.Left)
				e.emit(bc.OpAssignCV, a, 0, bc.FlagLoadRef)
			} else {
				e.assignTarget(x.Left, x.Right)
			}
			break
		}
		if x.Op == "??=" {
			if isSimpleForeachVar(x.Left) {
				idx := e.variable(x.Left)
				e.emit(bc.OpLoadCV, idx, 0, 0)
				p := e.emit(bc.OpJmpNull, 0, 0, 0)
				end := e.emit(bc.OpJmp, 0, 0, 0)
				e.patch(p)
				e.expr(x.Right)
				e.emit(bc.OpAssignCV, idx, 0, 0)
				e.patch(end)
			} else {
				left, temps := e.stableTarget(x.Left)
				e.expr(left)
				p := e.emit(bc.OpJmpNull, 0, 0, 0)
				end := e.emit(bc.OpJmp, 0, 0, 0)
				e.patch(p)
				e.assign(left, x.Right)
				e.patch(end)
				for _, idx := range temps {
					e.emit(bc.OpUnsetCV, idx, 0, 0)
				}
			}
			break
		}
		if x.Op != "=" {
			op, ok := binaryOps[strings.TrimSuffix(x.Op, "=")]
			if !ok {
				e.fail("affectation " + x.Op)
			}
			if isSimpleForeachVar(x.Left) && !isGlobals(x.Left) {
				idx := e.variable(x.Left)
				e.emit(bc.OpLoadCV, idx, 0, 0)
				e.expr(x.Right)
				e.emit(op, 0, 0, 0)
				e.emit(bc.OpAssignCV, idx, 0, 0)
				break
			}
			if isCompoundAssignOp(op) {
				if d, ok := x.Left.(*ast.DimFetchExpr); ok && d.Dim != nil {
					if v, ok := isFastDimCV(d.Var); ok {
						idx := e.cv(v.Name)
						e.expr(d.Dim)
						e.expr(x.Right)
						e.emit(bc.OpAssignDimOp, idx, uint32(op), bc.FlagAssignDimCV)
						break
					}
				}
				if p, ok := x.Left.(*ast.PropFetchExpr); ok {
					e.expr(p.Object)
					e.expr(x.Right)
					e.emit(bc.OpAssignPropOp, e.str(e.memberName(p.Property)), uint32(op), 0)
					break
				}
			}
			left, temps := e.stableTarget(x.Left)
			e.expr(left)
			e.expr(x.Right)
			e.emit(op, 0, 0, 0)
			result := e.temp()
			e.emit(bc.OpStoreCV, result, 0, 0)
			e.assign(left, &ast.VarExpr{Name: e.fn.CVNames[result]})
			for _, idx := range append(temps, result) {
				e.emit(bc.OpUnsetCV, idx, 0, 0)
			}
		} else {
			e.assignTarget(x.Left, x.Right)
		}
	case *ast.TernaryExpr:
		e.expr(x.Cond)
		if x.If == nil {
			e.emit(bc.OpDup, 0, 0, 0)
		}
		other := e.emit(bc.OpJmpZ, 0, 0, 0)
		if x.If != nil {
			e.expr(x.If)
		}
		end := e.emit(bc.OpJmp, 0, 0, 0)
		e.patch(other)
		if x.If == nil {
			e.emit(bc.OpPop, 0, 0, 0)
		}
		e.expr(x.Else)
		e.patch(end)
	case *ast.ListAssignExpr:
		e.destructureItems(x.Items, x.Expr)
	case *ast.ArrayExpr:
		e.emit(bc.OpInitArray, 0, 0, 0)
		for _, item := range x.Items {
			if item.Value == nil {
				continue
			}
			op := bc.OpAppendArrayElement
			if item.Key != nil {
				e.expr(item.Key)
				op = bc.OpAddArrayElement
			}
			e.expr(item.Value)
			e.emit(op, 0, 0, 0)
		}
	case *ast.DimFetchExpr:
		if x.Dim == nil {
			e.assign(x, &ast.ConstFetchExpr{Name: "null"})
			break
		}
		if isGlobals(x.Var) {
			e.expr(x.Dim)
			e.emit(bc.OpGlobalGet, 0, 0, 0)
			break
		}
		e.expr(x.Var)
		e.expr(x.Dim)
		e.emit(bc.OpFetchDimR, 0, 0, 0)
	case *ast.IssetExpr:
		var stops []int
		for _, item := range x.Exprs {
			e.isset(item, false)
			stops = append(stops, e.emit(bc.OpJmpZ, 0, 0, 0))
		}
		e.value(types.NewBool(true))
		end := e.emit(bc.OpJmp, 0, 0, 0)
		for _, pc := range stops {
			e.patch(pc)
		}
		e.value(types.NewBool(false))
		e.patch(end)
	case *ast.EmptyExpr:
		e.isset(x.Expr, true)
	case *ast.PrintExpr:
		e.expr(x.Expr)
		e.emit(bc.OpPrint, 0, 0, 0)
	case *ast.ExitExpr:
		if x.Expr == nil {
			e.emit(bc.OpExit, 0, 0, bc.FlagExitVoid)
		} else {
			e.expr(x.Expr)
			e.emit(bc.OpExit, 0, 0, 0)
		}
	case *ast.ThrowExpr:
		e.expr(x.Expr)
		e.emit(bc.OpThrow, 0, 0, 0)
	case *ast.CallExpr:
		if x.FirstClass {
			e.closure(nil, nil, nil, ast.TypeRef{})
			break
		}
		var params []bc.ParamInfo
		if name, ok := x.Func.(*ast.ConstFetchExpr); ok {
			idx, ok := e.funcs[strings.ToLower(name.Name)]
			if !ok {
				e.emit(bc.OpInitCall, e.str(name.Name), 0, bc.FlagNative)
				e.args(x.Args, nativeParams(name.Name))
				e.emit(bc.OpDoCall, 0, 0, 0)
				break
			}
			e.emit(bc.OpInitCall, idx, 0, 0)
			if e.prog.Funcs[idx] != nil {
				params = e.prog.Funcs[idx].Params
			} else {
				for _, s := range e.file.Stmts {
					if f, ok := s.(*ast.FunctionDecl); ok && strings.EqualFold(f.Name, name.Name) {
						for _, p := range f.Params {
							params = append(params, bc.ParamInfo{Name: p.Name, ByRef: p.ByRef})
						}
					}
				}
			}
		} else {
			e.expr(x.Func)
			e.emit(bc.OpInitCall, 0, 0, bc.FlagClosure)
		}
		e.args(x.Args, params)
		e.emit(bc.OpDoCall, 0, 0, 0)
	case *ast.NewExpr:
		if x.Anonymous != nil {
			e.emit(bc.OpInitNew, bc.ClassStdClass, 0, 0)
			e.args(x.Args, nil)
			e.emit(bc.OpDoCall, 0, 0, 0)
			break
		}
		id, flags := e.classOperand(x.Class)
		e.emit(bc.OpInitNew, id, 0, flags)
		e.args(x.Args, nil)
		e.emit(bc.OpDoCall, 0, 0, 0)
	case *ast.MethodCallExpr:
		if x.FirstClass {
			e.closure(nil, nil, nil, ast.TypeRef{})
			break
		}
		mname := e.memberName(x.Method)
		e.expr(x.Object)
		e.emit(bc.OpInitMethodCall, e.str(mname), 0, 0)
		e.args(x.Args, nativeMethodParams(mname))
		e.emit(bc.OpDoCall, 0, 0, 0)
	case *ast.StaticCallExpr:
		if x.FirstClass {
			e.closure(nil, nil, nil, ast.TypeRef{})
			break
		}
		id, flags := e.classOperand(x.Class)
		e.emit(bc.OpInitStaticMethodCall, e.str(e.memberName(x.Method)), id, flags)
		e.args(x.Args, nil)
		e.emit(bc.OpDoCall, 0, 0, 0)
	case *ast.PropFetchExpr:
		e.expr(x.Object)
		e.emit(bc.OpFetchPropR, e.str(e.memberName(x.Property)), 0, 0)
	case *ast.ClassConstFetchExpr:
		id, flags := e.classOperand(x.Class)
		name := e.name(x.Const)
		if name == "class" && flags == 0 {
			e.text(strings.TrimPrefix(e.name(x.Class), `\`))
		} else {
			e.emit(bc.OpFetchClassConst, e.str(name), id, flags)
		}
	case *ast.StaticPropFetchExpr:
		id, flags := e.classOperand(x.Class)
		e.emit(bc.OpFetchStaticPropR, e.str(e.staticName(x.Property)), id, flags)
	case *ast.ClosureExpr:
		e.closureFull(x.Params, x.Uses, x.Body, x.ReturnType, x.ByRef, x.Attrs)
	case *ast.ArrowFuncExpr:
		seen := make(map[string]bool)
		for _, p := range x.Params {
			seen[p.Name] = true
		}
		var uses []ast.ClosureUse
		var visit func(reflect.Value)
		visit = func(v reflect.Value) {
			if !v.IsValid() {
				return
			}
			if v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
				if v.IsNil() {
					return
				}
				if v.CanInterface() {
					if variable, ok := v.Interface().(*ast.VarExpr); ok && !seen[variable.Name] {
						seen[variable.Name] = true
						uses = append(uses, ast.ClosureUse{Name: variable.Name})
					}
				}
				visit(v.Elem())
				return
			}
			switch v.Kind() {
			case reflect.Struct:
				for _, f := range v.Fields() {
					visit(f)
				}
			case reflect.Slice:
				for i := 0; i < v.Len(); i++ {
					visit(v.Index(i))
				}
			}
		}
		visit(reflect.ValueOf(x.Expr))
		e.closureFull(x.Params, uses, []ast.Stmt{&ast.ReturnStmt{Expr: x.Expr}}, x.ReturnType, x.ByRef, x.Attrs)
	case *ast.MatchExpr:
		e.match(x)
	default:
		e.fail(fmt.Sprintf("expression %T", expr))
	}
}

func (e *emitter) destructureItems(items []ast.ArrayItem, rhs ast.Expr) {
	e.expr(rhs)
	result := e.temp()
	e.emit(bc.OpStoreCV, result, 0, 0)
	for i, item := range items {
		if item.Value == nil {
			continue
		}
		key := item.Key
		if key == nil {
			key = &ast.IntLit{Value: int64(i)}
		}
		e.assignTarget(item.Value, &ast.DimFetchExpr{Var: &ast.VarExpr{Name: e.fn.CVNames[result]}, Dim: key})
		e.emit(bc.OpPop, 0, 0, 0)
	}
	e.emit(bc.OpLoadCV, result, 0, 0)
	e.emit(bc.OpUnsetCV, result, 0, 0)
}

func (e *emitter) assignTarget(left, right ast.Expr) {
	switch l := left.(type) {
	case *ast.ArrayExpr:
		e.destructureItems(l.Items, right)
	case *ast.ListAssignExpr:
		e.destructureItems(l.Items, right)
	default:
		e.assign(left, right)
	}
}

// Les expressions de localisation sont figees avant la lecture et l'ecriture.
func (e *emitter) stableTarget(expr ast.Expr) (ast.Expr, []uint32) {
	switch x := expr.(type) {
	case *ast.VarExpr:
		return x, nil
	case *ast.StaticPropFetchExpr:
		return x, nil
	case *ast.DimFetchExpr:
		base, temps := e.stableTarget(x.Var)
		if x.Dim == nil {
			return &ast.DimFetchExpr{ExprBase: x.ExprBase, Var: base}, temps
		}
		e.expr(x.Dim)
		idx := e.temp()
		e.emit(bc.OpStoreCV, idx, 0, 0)
		return &ast.DimFetchExpr{ExprBase: x.ExprBase, Var: base, Dim: &ast.VarExpr{Name: e.fn.CVNames[idx]}}, append(temps, idx)
	case *ast.PropFetchExpr:
		e.expr(x.Object)
		idx := e.temp()
		e.emit(bc.OpStoreCV, idx, 0, 0)
		return &ast.PropFetchExpr{ExprBase: x.ExprBase, Object: &ast.VarExpr{Name: e.fn.CVNames[idx]}, Property: x.Property}, []uint32{idx}
	default:
		e.expr(expr)
		idx := e.temp()
		e.emit(bc.OpStoreCV, idx, 0, 0)
		return &ast.VarExpr{Name: e.fn.CVNames[idx]}, []uint32{idx}
	}
}
func (e *emitter) exprCoalesce(expr ast.Expr) {
	switch x := expr.(type) {
	case *ast.DimFetchExpr:
		if x.Dim != nil && !isGlobals(x.Var) {
			e.exprCoalesce(x.Var)
			e.expr(x.Dim)
			e.emit(bc.OpFetchDimIs, 0, 0, bc.FlagIsset)
			return
		}
	case *ast.PropFetchExpr:
		e.exprCoalesce(x.Object)
		e.emit(bc.OpFetchPropR, e.str(e.memberName(x.Property)), 0, bc.FlagIsset)
		return
	}
	e.expr(expr)
}

func nativeParams(name string) []bc.ParamInfo {
	switch strings.ToLower(strings.TrimPrefix(name, `\`)) {
	case "preg_match", "preg_match_all":
		return []bc.ParamInfo{{Name: "pattern"}, {Name: "subject"}, {Name: "matches", ByRef: true}, {Name: "flags"}, {Name: "offset"}}
	case "preg_replace", "preg_replace_callback":
		return []bc.ParamInfo{{Name: "pattern"}, {Name: "replacement"}, {Name: "subject"}, {Name: "limit"}, {Name: "count", ByRef: true}}
	case "str_replace", "str_ireplace":
		return []bc.ParamInfo{{Name: "search"}, {Name: "replace"}, {Name: "subject"}, {Name: "count", ByRef: true}}
	case "parse_str":
		return []bc.ParamInfo{{Name: "string"}, {Name: "result", ByRef: true}}
	case "headers_sent":
		return []bc.ParamInfo{{Name: "filename", ByRef: true}, {Name: "line", ByRef: true}}
	case "natsort", "natcasesort", "shuffle":
		return []bc.ParamInfo{{Name: "array", ByRef: true}, {Name: "flags"}}
	case "settype":
		return []bc.ParamInfo{{Name: "var", ByRef: true}, {Name: "type"}}
	case "usort", "uasort", "uksort", "array_walk", "array_walk_recursive":
		return []bc.ParamInfo{{Name: "array", ByRef: true}, {Name: "callback"}, {Name: "arg"}}
	case "array_multisort":
		return []bc.ParamInfo{{Name: "array", ByRef: true}, {Name: "rest", ByRef: true, Variadic: true}}
	case "similar_text":
		return []bc.ParamInfo{{Name: "string1"}, {Name: "string2"}, {Name: "percent", ByRef: true}}
	case "sscanf":
		return []bc.ParamInfo{{Name: "string"}, {Name: "format"}, {Name: "vars", ByRef: true, Variadic: true}}
	case "exec":
		return []bc.ParamInfo{{Name: "command"}, {Name: "output", ByRef: true}, {Name: "result_code", ByRef: true}}
	default:
		return nil
	}
}

func nativeMethodParams(name string) []bc.ParamInfo {
	if strings.EqualFold(name, "bindParam") {
		return []bc.ParamInfo{{Name: "param"}, {Name: "var", ByRef: true}, {Name: "type"}, {Name: "maxLength"}, {Name: "driverOptions"}}
	}
	return nil
}

func (e *emitter) args(args []ast.Arg, params []bc.ParamInfo) {
	for i, arg := range args {
		if arg.Name != "" {
			i = len(params)
			for j, p := range params {
				if p.Name == arg.Name {
					i = j
					break
				}
			}
		}
		op := bc.OpSendVal
		if arg.Unpack {
			op = bc.OpSendUnpack
		}
		byRef := false
		if i < len(params) {
			byRef = params[i].ByRef
		} else if len(params) > 0 && params[len(params)-1].Variadic {
			byRef = params[len(params)-1].ByRef
		}
		if d, ok := arg.Value.(*ast.DimFetchExpr); ok && d.Dim == nil {
			e.assign(d, &ast.ConstFetchExpr{Name: "null"})
		} else if byRef {
			if e.exprRef(arg.Value) {
				op = bc.OpSendRef
			} else {
				e.expr(arg.Value)
			}
		} else {
			e.expr(arg.Value)
		}
		if arg.Name != "" {
			e.emit(bc.OpSendNamed, e.str(arg.Name), 0, 0)
		} else {
			e.emit(op, 0, 0, 0)
		}
	}
}

func (e *emitter) exprRef(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.VarExpr:
		e.emit(bc.OpLoadCV, e.variable(x), 0, bc.FlagLoadRef)
		return true
	case *ast.DimFetchExpr:
		if isGlobals(x.Var) {
			return false
		}
		if !e.exprRef(x.Var) {
			e.expr(x.Var)
		}
		if x.Dim != nil {
			e.expr(x.Dim)
			e.emit(bc.OpFetchDimW, 0, 0, 0)
			return true
		}
		return false
	case *ast.PropFetchExpr:
		e.expr(x.Object)
		e.emit(bc.OpFetchPropW, e.str(e.memberName(x.Property)), 0, 0)
		return true
	default:
		return false
	}
}
func (e *emitter) closure(params []ast.Param, uses []ast.ClosureUse, body []ast.Stmt, result ast.TypeRef) {
	e.closureFull(params, uses, body, result, false, nil)
}
func (e *emitter) closureFull(params []ast.Param, uses []ast.ClosureUse, body []ast.Stmt, result ast.TypeRef, byRef bool, attrs []ast.Attribute) {
	child := e.compilation.emitter("{closure}")
	child.namespace = e.namespace
	child.class = e.class
	child.fn.ReturnType = typeName(result)
	child.fn.ReturnByRef = byRef
	child.fn.Attributes = e.attributes(attrs)
	child.params(params)
	for _, u := range uses {
		cv := child.cv(u.Name)
		child.fn.Uses = append(child.fn.Uses, bc.UseVar{CV: cv, ByRef: u.ByRef})
		flags := uint16(0)
		if u.ByRef {
			flags = bc.FlagLoadRef
		}
		e.emit(bc.OpLoadCV, e.cv(u.Name), 0, flags)
	}
	idx := uint32(len(e.prog.Funcs))
	e.prog.Funcs = append(e.prog.Funcs, child.fn)
	child.stmts(body)
	child.end()
	e.emit(bc.OpBindClosure, idx, uint32(len(uses)), 0)
}
func (e *emitter) isset(expr ast.Expr, empty bool) {
	switch x := expr.(type) {
	case *ast.VarExpr:
		op := bc.OpIssetCV
		if empty {
			op = bc.OpEmptyCV
		}
		e.emit(op, e.variable(x), 0, 0)
	case *ast.PropFetchExpr:
		e.exprCoalesce(x.Object)
		op := bc.OpIssetProp
		if empty {
			op = bc.OpEmptyProp
		}
		e.emit(op, e.str(e.memberName(x.Property)), 0, 0)
	case *ast.DimFetchExpr:
		if isGlobals(x.Var) {
			e.expr(x.Dim)
			if empty {
				e.emit(bc.OpGlobalGet, 0, 0, 0)
				e.emit(bc.OpBoolNot, 0, 0, 0)
			} else {
				e.emit(bc.OpGlobalIsset, 0, 0, 0)
			}
			break
		}
		e.exprCoalesce(x.Var)
		e.expr(x.Dim)
		op := bc.OpIssetDim
		if empty {
			op = bc.OpEmptyDim
		}
		e.emit(op, 0, 0, 0)
	default:
		if empty {
			e.expr(expr)
			e.emit(bc.OpBoolNot, 0, 0, 0)
			break
		}
		e.expr(expr)
		isNull := e.emit(bc.OpJmpNull, 0, 0, 0)
		e.emit(bc.OpPop, 0, 0, 0)
		e.value(types.NewBool(true))
		done := e.emit(bc.OpJmp, 0, 0, 0)
		e.patch(isNull)
		e.value(types.NewBool(false))
		e.patch(done)
	}
}

// Les dimensions sont evaluees une seule fois, puis reconstruites de bas en haut.
func (e *emitter) assign(left, right ast.Expr) {
	if d, ok := left.(*ast.DimFetchExpr); ok && isGlobals(d.Var) {
		e.expr(d.Dim)
		e.expr(right)
		e.emit(bc.OpGlobalSet, 0, 0, 0)
		return
	}
	if d, ok := left.(*ast.DimFetchExpr); ok {
		if v, ok := isFastDimCV(d.Var); ok {
			idx := e.cv(v.Name)
			if d.Dim != nil {
				e.expr(d.Dim)
				e.expr(right)
				e.emit(bc.OpAssignDim, idx, 0, bc.FlagAssignDimCV)
			} else {
				e.expr(right)
				e.emit(bc.OpAppendArrayElement, idx, 0, bc.FlagAssignDimCV)
			}
			return
		}
	}
	if p, ok := left.(*ast.StaticPropFetchExpr); ok {
		id, flags := e.classOperand(p.Class)
		e.expr(right)
		e.emit(bc.OpAssignStaticProp, e.str(e.staticName(p.Property)), id, flags)
		return
	}
	if p, ok := left.(*ast.PropFetchExpr); ok {
		e.expr(p.Object)
		e.expr(right)
		e.emit(bc.OpAssignProp, e.str(e.memberName(p.Property)), 0, 0)
		return
	}
	if v, ok := left.(*ast.VarExpr); ok {
		idx := e.variable(v)
		e.expr(right)
		e.emit(bc.OpAssignCV, idx, 0, 0)
		return
	}
	var dims []*ast.DimFetchExpr
	root := left
	for {
		d, ok := root.(*ast.DimFetchExpr)
		if !ok || isGlobals(d.Var) {
			break
		}
		dims = append(dims, d)
		root = d.Var
	}
	if len(dims) == 0 {
		e.expr(right)
		return
	}
	root, rootTemps := e.stableTarget(root)
	arrays := make([]uint32, len(dims))
	keys := make([]uint32, len(dims))
	for i := len(dims) - 1; i >= 0; i-- {
		keys[i] = bc.NoCV
		if dims[i].Dim != nil {
			e.expr(dims[i].Dim)
			keys[i] = e.temp()
			e.emit(bc.OpStoreCV, keys[i], 0, 0)
		}
	}
	e.expr(right)
	result := e.temp()
	e.emit(bc.OpStoreCV, result, 0, 0)
	e.expr(root)
	for i := len(dims) - 1; i >= 0; i-- {
		arrays[i] = e.temp()
		e.emit(bc.OpStoreCV, arrays[i], 0, 0)
		if i > 0 {
			if keys[i] == bc.NoCV {
				e.emit(bc.OpInitArray, 0, 0, 0)
			} else {
				e.emit(bc.OpLoadCV, arrays[i], 0, 0)
				e.emit(bc.OpLoadCV, keys[i], 0, 0)
				e.emit(bc.OpFetchDimR, 0, 0, 0)
			}
		}
	}
	val := result
	var values []uint32
	for i := 0; i < len(dims); i++ {
		e.emit(bc.OpLoadCV, arrays[i], 0, 0)
		op := bc.OpAppendArrayElement
		if keys[i] != bc.NoCV {
			e.emit(bc.OpLoadCV, keys[i], 0, 0)
			op = bc.OpAssignDim
		}
		e.emit(bc.OpLoadCV, val, 0, 0)
		e.emit(op, 0, 0, 0)
		val = e.temp()
		values = append(values, val)
		e.emit(bc.OpStoreCV, val, 0, 0)
	}
	e.assign(root, &ast.VarExpr{Name: e.fn.CVNames[val]})
	e.emit(bc.OpPop, 0, 0, 0)
	e.emit(bc.OpLoadCV, result, 0, 0)
	for _, cv := range append(append(values, arrays...), result) {
		e.emit(bc.OpUnsetCV, cv, 0, 0)
	}
	for _, cv := range keys {
		if cv != bc.NoCV {
			e.emit(bc.OpUnsetCV, cv, 0, 0)
		}
	}
	for _, cv := range rootTemps {
		e.emit(bc.OpUnsetCV, cv, 0, 0)
	}
}

func isGlobals(expr ast.Expr) bool {
	v, ok := expr.(*ast.VarExpr)
	return ok && v.Dynamic == nil && v.Name == "GLOBALS"
}

func isFastDimCV(expr ast.Expr) (*ast.VarExpr, bool) {
	v, ok := expr.(*ast.VarExpr)
	if !ok || v.Dynamic != nil || v.Name == "this" || v.Name == "GLOBALS" || bc.IsSuperglobalName(v.Name) {
		return nil, false
	}
	return v, true
}

func isCompoundAssignOp(op bc.Opcode) bool {
	return op == bc.OpAdd || op == bc.OpSub || op == bc.OpMul || op == bc.OpConcat
}
func (e *emitter) match(x *ast.MatchExpr) {
	e.expr(x.Cond)
	tmp := e.temp()
	e.emit(bc.OpStoreCV, tmp, 0, 0)
	jumps := make([][]int, len(x.Arms))
	def := -1
	for i, arm := range x.Arms {
		if arm.Default {
			def = i
			continue
		}
		for _, cond := range arm.Conds {
			e.emit(bc.OpLoadCV, tmp, 0, 0)
			e.expr(cond)
			e.emit(bc.OpIsIdentical, 0, 0, 0)
			jumps[i] = append(jumps[i], e.emit(bc.OpJmpNZ, 0, 0, 0))
		}
	}
	fallback := e.emit(bc.OpJmp, 0, 0, 0)
	var ends []int
	for i, arm := range x.Arms {
		if def == i {
			e.patch(fallback)
		}
		for _, p := range jumps[i] {
			e.patch(p)
		}
		e.expr(arm.Value)
		ends = append(ends, e.emit(bc.OpJmp, 0, 0, 0))
	}
	if def < 0 {
		e.patch(fallback)
		e.emit(bc.OpLoadCV, tmp, 0, 0)
		idx := uint32(len(e.fn.Tables))
		e.fn.Tables = append(e.fn.Tables, bc.JumpTable{Default: bc.NoPC})
		e.emit(bc.OpMatch, idx, 0, 0)
	}
	for _, p := range ends {
		e.patch(p)
	}
}
