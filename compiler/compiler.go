// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/compiler: Compilateur AST vers Bytecode Flatstack127

package compiler

import (
	"fmt"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/vm"
)

type Compiler struct {
	varMap            map[string]uint16
	nextVar           uint16
	funcMap           map[string]uint16
	functions         []vm.BytecodeProgram
	classes           []vm.ClassDef
	classMap          map[string]uint16
	currentClass      *vm.ClassDef
	instructions      []vm.Instruction
	constants         []types.Value
	stringPool        []string
	isFunction        bool
	loopStarts        []int
	loopEnds          []int
	exceptionHandlers []vm.ExceptionHandler
}

func NewCompiler() *Compiler {
	return &Compiler{
		varMap:   make(map[string]uint16),
		funcMap:  make(map[string]uint16),
		classMap: make(map[string]uint16),
	}
}

func Compile(src string) (*vm.BytecodeProgram, error) {
	lexer := NewLexer(src)
	parser := NewParser(lexer)
	ast, err := parser.ParseProgram()
	if err != nil {
		return nil, err
	}

	c := NewCompiler()
	return c.CompileAST(ast)
}

func (c *Compiler) CompileAST(stmts []Node) (*vm.BytecodeProgram, error) {
	c.registerBuiltinClasses()

	// First pass: collect class declarations
	for _, stmt := range stmts {
		if cDecl, ok := stmt.(ClassDeclStmt); ok {
			clsIdx := uint16(len(c.classes))
			clsNameLower := strings.ToLower(cDecl.Name)
			c.classMap[clsNameLower] = clsIdx

			clsDef := vm.ClassDef{
				Name:        cDecl.Name,
				Parent:      cDecl.Parent,
				Implements:  cDecl.Implements,
				Methods:     make(map[string]uint16),
				PropOffsets: make(map[string]int),
			}

			// Inherit properties from parent
			if cDecl.Parent != "" {
				if pIdx, ok := c.classMap[strings.ToLower(cDecl.Parent)]; ok {
					parentDef := c.classes[pIdx]
					clsDef.PropNames = append(clsDef.PropNames, parentDef.PropNames...)
					clsDef.PropDefaults = append(clsDef.PropDefaults, parentDef.PropDefaults...)
					for pName, offset := range parentDef.PropOffsets {
						clsDef.PropOffsets[pName] = offset
					}
				}
			}

			// Map properties
			for _, prop := range cDecl.Properties {
				if _, exists := clsDef.PropOffsets[prop.Name]; !exists {
					pOffset := len(clsDef.PropNames)
					clsDef.PropNames = append(clsDef.PropNames, prop.Name)
					clsDef.PropOffsets[prop.Name] = pOffset
					var defVal types.Value = types.NewNull()
					if prop.DefaultVal != nil {
						if intLit, ok := prop.DefaultVal.(IntLitExpr); ok {
							defVal = types.NewLong(intLit.Val)
						} else if strLit, ok := prop.DefaultVal.(StringLitExpr); ok {
							defVal = types.NewString(strLit.Val)
						} else if boolLit, ok := prop.DefaultVal.(BoolLitExpr); ok {
							defVal = types.NewBool(boolLit.Val)
						} else if fltLit, ok := prop.DefaultVal.(FloatLitExpr); ok {
							defVal = types.NewDouble(fltLit.Val)
						}
					}
					clsDef.PropDefaults = append(clsDef.PropDefaults, defVal)
				}
			}

			// Reserve method slots in c.functions
			for _, m := range cDecl.Methods {
				mFuncIdx := uint16(len(c.functions))
				mNameLower := strings.ToLower(m.Name)
				clsDef.Methods[mNameLower] = mFuncIdx
				c.funcMap[clsNameLower+"::"+mNameLower] = mFuncIdx
				c.functions = append(c.functions, vm.BytecodeProgram{})
			}

			c.classes = append(c.classes, clsDef)
		}
	}

	// First pass: collect standalone function declarations
	for _, stmt := range stmts {
		if fDecl, ok := stmt.(FuncDeclStmt); ok {
			fIdx := uint16(len(c.functions))
			c.funcMap[strings.ToLower(fDecl.Name)] = fIdx
			c.functions = append(c.functions, vm.BytecodeProgram{})
		}
	}

	// First pass (b): Inherit methods from parent classes & flatten traits
	for _, stmt := range stmts {
		if cDecl, ok := stmt.(ClassDeclStmt); ok {
			clsIdx := c.classMap[strings.ToLower(cDecl.Name)]
			clsDef := &c.classes[clsIdx]

			if cDecl.Parent != "" {
				if pIdx, ok := c.classMap[strings.ToLower(cDecl.Parent)]; ok {
					parentDef := c.classes[pIdx]
					for mName, fIdx := range parentDef.Methods {
						if _, overridden := clsDef.Methods[mName]; !overridden {
							clsDef.Methods[mName] = fIdx
						}
					}
				}
			}

			for _, tName := range cDecl.Traits {
				if tIdx, ok := c.classMap[strings.ToLower(tName)]; ok {
					traitDef := c.classes[tIdx]
					for mName, fIdx := range traitDef.Methods {
						if _, overridden := clsDef.Methods[mName]; !overridden {
							clsDef.Methods[mName] = fIdx
						}
					}
				}
			}
		}
	}

	// Second pass: compile class methods
	for _, stmt := range stmts {
		if cDecl, ok := stmt.(ClassDeclStmt); ok {
			clsIdx := c.classMap[strings.ToLower(cDecl.Name)]
			clsDef := &c.classes[clsIdx]

			for _, m := range cDecl.Methods {
				mFuncIdx := clsDef.Methods[strings.ToLower(m.Name)]
				subCompiler := NewCompiler()
				subCompiler.isFunction = true
				subCompiler.currentClass = clsDef
				subCompiler.classes = c.classes
				subCompiler.classMap = c.classMap
				subCompiler.funcMap = c.funcMap
				subCompiler.functions = c.functions

				// Variable 0 is '$this'
				_ = subCompiler.getVarSlot("this")

				// Parameters map to Arg indices via OpGetArg
				argOffset := uint16(1)
				if m.Name == "__construct" || m.IsStatic {
					argOffset = 0
				}
				for i, pName := range m.Params {
					pVar := subCompiler.getVarSlot(pName)
					subCompiler.emit(vm.OpGetArg, uint16(i)+argOffset, 0, 0, types.NewNull())
					subCompiler.emit(vm.OpStoreVar, pVar, 0, 0, types.NewNull())
				}

				// Initialize promoted constructor properties
				if m.Name == "__construct" {
					for _, pDecl := range m.ParamDecls {
						if pDecl.IsPromoted {
							pSlot := subCompiler.getVarSlot(pDecl.Name)
							if propOffset, ok := clsDef.PropOffsets[pDecl.Name]; ok {
								thisSlot := subCompiler.getVarSlot("this")
								subCompiler.emit(vm.OpLoadVar, thisSlot, 0, 0, types.NewNull())
								subCompiler.emit(vm.OpLoadVar, pSlot, 0, 0, types.NewNull())
								subCompiler.emit(vm.OpSetProp, uint16(propOffset), 0, 0, types.NewNull())
							}
						}
					}
				}

				for _, s := range m.Body {
					if err := subCompiler.compileNode(s); err != nil {
						return nil, err
					}
				}

				// Propagate nested functions/closures back to c.functions
				if len(subCompiler.functions) > len(c.functions) {
					c.functions = append(c.functions, subCompiler.functions[len(c.functions):]...)
				}

				// Ensure trailing return in method
				if len(subCompiler.instructions) == 0 || subCompiler.instructions[len(subCompiler.instructions)-1].Op != vm.OpRetFrame {
					subCompiler.emit(vm.OpLoadConst, 0, 0, 0, types.NewNull())
					subCompiler.emit(vm.OpRetFrame, 0, 0, 0, types.NewNull())
				}

				c.functions[mFuncIdx] = vm.BytecodeProgram{
					Instructions:      subCompiler.instructions,
					Constants:         subCompiler.constants,
					StringPool:        subCompiler.stringPool,
					Classes:           c.classes,
					ClassMap:          c.classMap,
					ExceptionHandlers: subCompiler.exceptionHandlers,
					NumVars:           subCompiler.nextVar,
				}
			}
		}
	}

	// Second pass: compile standalone function bodies
	for _, stmt := range stmts {
		if fDecl, ok := stmt.(FuncDeclStmt); ok {
			fIdx := c.funcMap[strings.ToLower(fDecl.Name)]
			subCompiler := NewCompiler()
			subCompiler.isFunction = true
			subCompiler.classes = c.classes
			subCompiler.classMap = c.classMap
			subCompiler.funcMap = c.funcMap
			subCompiler.functions = c.functions

			// Parameters map to Arg indices via OpGetArg
			for i, pName := range fDecl.Params {
				pVar := subCompiler.getVarSlot(pName)
				subCompiler.emit(vm.OpGetArg, uint16(i), 0, 0, types.NewNull())
				subCompiler.emit(vm.OpStoreVar, pVar, 0, 0, types.NewNull())
			}

			for _, s := range fDecl.Body {
				if err := subCompiler.compileNode(s); err != nil {
					return nil, err
				}
			}

			// Ensure trailing return in function
			if len(subCompiler.instructions) == 0 || subCompiler.instructions[len(subCompiler.instructions)-1].Op != vm.OpRetFrame {
				subCompiler.emit(vm.OpLoadConst, 0, 0, 0, types.NewNull())
				subCompiler.emit(vm.OpRetFrame, 0, 0, 0, types.NewNull())
			}

			c.functions[fIdx] = vm.BytecodeProgram{
				Instructions:      subCompiler.instructions,
				Constants:         subCompiler.constants,
				StringPool:        subCompiler.stringPool,
				Classes:           c.classes,
				ClassMap:          c.classMap,
				ExceptionHandlers: subCompiler.exceptionHandlers,
				NumVars:           subCompiler.nextVar,
			}
		}
	}

	// Third pass: compile main statements
	for _, stmt := range stmts {
		if _, isFunc := stmt.(FuncDeclStmt); isFunc {
			continue
		}
		if _, isClass := stmt.(ClassDeclStmt); isClass {
			continue
		}
		if err := c.compileNode(stmt); err != nil {
			return nil, err
		}
	}

	// Ensure trailing return in main program
	if len(c.instructions) == 0 || c.instructions[len(c.instructions)-1].Op != vm.OpReturn {
		c.emit(vm.OpReturn, 0, 0, 0, types.NewNull())
	}

	return &vm.BytecodeProgram{
		Instructions:      c.instructions,
		Constants:         c.constants,
		StringPool:        c.stringPool,
		Functions:         c.functions,
		Classes:           c.classes,
		ClassMap:          c.classMap,
		ExceptionHandlers: c.exceptionHandlers,
		NumVars:           c.nextVar,
	}, nil
}

func (c *Compiler) emit(op vm.Opcode, a, b, cIdx uint16, val types.Value) int {
	pos := len(c.instructions)
	c.instructions = append(c.instructions, vm.Instruction{
		Op:    op,
		A:     a,
		B:     b,
		C:     cIdx,
		Const: val,
	})
	return pos
}

func (c *Compiler) getVarSlot(name string) uint16 {
	if slot, exists := c.varMap[name]; exists {
		return slot
	}
	slot := c.nextVar
	c.varMap[name] = slot
	c.nextVar++
	return slot
}

func (c *Compiler) addString(s string) uint16 {
	for i, str := range c.stringPool {
		if str == s {
			return uint16(i)
		}
	}
	idx := uint16(len(c.stringPool))
	c.stringPool = append(c.stringPool, s)
	return idx
}

func (c *Compiler) findPropSlot(name string) uint16 {
	if c.currentClass != nil {
		if offset, ok := c.currentClass.PropOffsets[name]; ok {
			return uint16(offset)
		}
	}
	for _, cls := range c.classes {
		if offset, ok := cls.PropOffsets[name]; ok {
			return uint16(offset)
		}
	}
	return 0
}

func (c *Compiler) compileNode(node Node) error {
	if node == nil {
		return nil
	}

	switch n := node.(type) {
	case NewExpr:
		clsIdx := uint16(0)
		if idx, ok := c.classMap[strings.ToLower(n.ClassName)]; ok {
			clsIdx = idx
		}
		for _, arg := range n.Args {
			if err := c.compileNode(arg); err != nil {
				return err
			}
		}
		c.emit(vm.OpNewObject, clsIdx, uint16(len(n.Args)), 0, types.NewNull())

	case PropertyAccessExpr:
		if err := c.compileNode(n.Target); err != nil {
			return err
		}
		propSlot := c.findPropSlot(n.Property)
		c.emit(vm.OpGetProp, propSlot, 0, 0, types.NewNull())

	case MethodCallExpr:
		if err := c.compileNode(n.Target); err != nil {
			return err
		}
		for _, arg := range n.Args {
			if err := c.compileNode(arg); err != nil {
				return err
			}
		}
		mNameLower := strings.ToLower(n.Method)
		mFuncIdx := uint16(0)
		if c.currentClass != nil {
			if fIdx, ok := c.currentClass.Methods[mNameLower]; ok {
				mFuncIdx = fIdx
			}
		} else {
			for _, cls := range c.classes {
				if fIdx, ok := cls.Methods[mNameLower]; ok {
					mFuncIdx = fIdx
					break
				}
			}
		}
		nullsafe := uint16(0)
		if n.IsNullsafe {
			nullsafe = 1
		}
		c.emit(vm.OpCallMethod, mFuncIdx, uint16(len(n.Args)), nullsafe, types.NewNull())

	case StaticCallExpr:
		mNameLower := strings.ToLower(n.Method)
		clsLower := strings.ToLower(n.ClassName)
		mFuncIdx := uint16(0)
		if fIdx, ok := c.funcMap[clsLower+"::"+mNameLower]; ok {
			mFuncIdx = fIdx
		}
		for _, arg := range n.Args {
			if err := c.compileNode(arg); err != nil {
				return err
			}
		}
		c.emit(vm.OpCallStatic, mFuncIdx, uint16(len(n.Args)), 0, types.NewNull())

	case InstanceOfExpr:
		if err := c.compileNode(n.Target); err != nil {
			return err
		}
		strIdx := c.addString(n.ClassName)
		c.emit(vm.OpInstanceOf, strIdx, 0, 0, types.NewNull())

	case ClassConstExpr:
		clsLower := strings.ToLower(n.ClassName)
		if clsIdx, ok := c.classMap[clsLower]; ok {
			cls := c.classes[clsIdx]
			if cls.Constants != nil {
				if val, found := cls.Constants[n.ConstName]; found {
					c.emit(vm.OpLoadConst, 0, 0, 0, val)
					return nil
				}
			}
			constStrIdx := c.addString(n.ConstName)
			c.emit(vm.OpGetConst, clsIdx, constStrIdx, 0, types.NewNull())
			return nil
		}
		c.emit(vm.OpLoadConst, 0, 0, 0, types.NewNull())
		return nil

	case IntLitExpr:
		c.emit(vm.OpLoadConst, 0, 0, 0, types.NewLong(n.Val))

	case FloatLitExpr:
		c.emit(vm.OpLoadConst, 0, 0, 0, types.NewDouble(n.Val))

	case StringLitExpr:
		idx := uint16(len(c.stringPool))
		c.stringPool = append(c.stringPool, n.Val)
		c.emit(vm.OpLoadConstStr, idx, 0, 0, types.NewNull())

	case BoolLitExpr:
		c.emit(vm.OpLoadConst, 0, 0, 0, types.NewBool(n.Val))

	case NullLitExpr:
		c.emit(vm.OpLoadConst, 0, 0, 0, types.NewNull())

	case VarExpr:
		slot := c.getVarSlot(n.Name)
		c.emit(vm.OpLoadVar, slot, 0, 0, types.NewNull())

	case ArrayAccessExpr:
		if err := c.compileNode(n.Target); err != nil {
			return err
		}
		if err := c.compileNode(n.Index); err != nil {
			return err
		}
		c.emit(vm.OpArrayGet, 0, 0, 0, types.NewNull())

	case ArrayLitExpr:
		c.emit(vm.OpArrayInit, 0, 0, 0, types.NewNull())
		for i, el := range n.Elements {
			c.emit(vm.OpDup, 0, 0, 0, types.NewNull()) // duplicate array ref
			if el.Key != nil {
				if err := c.compileNode(el.Key); err != nil {
					return err
				}
			} else {
				c.emit(vm.OpLoadConst, 0, 0, 0, types.NewLong(int64(i)))
			}
			if err := c.compileNode(el.Val); err != nil {
				return err
			}
			c.emit(vm.OpArraySet, 0, 0, 0, types.NewNull())
		}

	case UnaryExpr:
		if n.Op == TokAt {
			c.emit(vm.OpSilenceBegin, 0, 0, 0, types.NewNull())
			if err := c.compileNode(n.Expr); err != nil {
				return err
			}
			c.emit(vm.OpSilenceEnd, 0, 0, 0, types.NewNull())
			return nil
		}
		if err := c.compileNode(n.Expr); err != nil {
			return err
		}
		switch n.Op {
		case TokMinus:
			c.emit(vm.OpNeg, 0, 0, 0, types.NewNull())
		case TokNot:
			c.emit(vm.OpNot, 0, 0, 0, types.NewNull())
		case TokBitNot:
			c.emit(vm.OpBitNot, 0, 0, 0, types.NewNull())
		default:
			return fmt.Errorf("opérateur unaire non supporté: %v", n.Op)
		}

	case BinaryExpr:
		// Handle logical short-circuit operators
		if n.Op == TokAnd {
			if err := c.compileNode(n.Left); err != nil {
				return err
			}
			c.emit(vm.OpDup, 0, 0, 0, types.NewNull())
			jmpIdx := c.emit(vm.OpJmpZ, 0, 0, 0, types.NewNull())
			c.emit(vm.OpPop, 0, 0, 0, types.NewNull())
			if err := c.compileNode(n.Right); err != nil {
				return err
			}
			c.instructions[jmpIdx].A = uint16(len(c.instructions))
			return nil
		}
		if n.Op == TokOr {
			if err := c.compileNode(n.Left); err != nil {
				return err
			}
			c.emit(vm.OpDup, 0, 0, 0, types.NewNull())
			jmpIdx := c.emit(vm.OpJmpNZ, 0, 0, 0, types.NewNull())
			c.emit(vm.OpPop, 0, 0, 0, types.NewNull())
			if err := c.compileNode(n.Right); err != nil {
				return err
			}
			c.instructions[jmpIdx].A = uint16(len(c.instructions))
			return nil
		}

		if err := c.compileNode(n.Left); err != nil {
			return err
		}
		if err := c.compileNode(n.Right); err != nil {
			return err
		}

		switch n.Op {
		case TokPlus:
			c.emit(vm.OpAdd, 0, 0, 0, types.NewNull())
		case TokMinus:
			c.emit(vm.OpSub, 0, 0, 0, types.NewNull())
		case TokMul:
			c.emit(vm.OpMul, 0, 0, 0, types.NewNull())
		case TokDiv:
			c.emit(vm.OpDiv, 0, 0, 0, types.NewNull())
		case TokMod:
			c.emit(vm.OpMod, 0, 0, 0, types.NewNull())
		case TokPow:
			c.emit(vm.OpPow, 0, 0, 0, types.NewNull())
		case TokDot:
			c.emit(vm.OpConcat, 0, 0, 0, types.NewNull())
		case TokEqual:
			c.emit(vm.OpEq, 0, 0, 0, types.NewNull())
		case TokNotEqual:
			c.emit(vm.OpNotEq, 0, 0, 0, types.NewNull())
		case TokIdentical:
			c.emit(vm.OpIdentical, 0, 0, 0, types.NewNull())
		case TokNotIdent:
			c.emit(vm.OpNotIdentical, 0, 0, 0, types.NewNull())
		case TokLt:
			c.emit(vm.OpLt, 0, 0, 0, types.NewNull())
		case TokLte:
			c.emit(vm.OpLte, 0, 0, 0, types.NewNull())
		case TokGt:
			c.emit(vm.OpGt, 0, 0, 0, types.NewNull())
		case TokGte:
			c.emit(vm.OpGte, 0, 0, 0, types.NewNull())
		case TokSpaceship:
			c.emit(vm.OpSpaceship, 0, 0, 0, types.NewNull())
		case TokBitAnd:
			c.emit(vm.OpBitAnd, 0, 0, 0, types.NewNull())
		case TokBitOr:
			c.emit(vm.OpBitOr, 0, 0, 0, types.NewNull())
		case TokBitXor:
			c.emit(vm.OpBitXor, 0, 0, 0, types.NewNull())
		case TokShiftLeft:
			c.emit(vm.OpShiftLeft, 0, 0, 0, types.NewNull())
		case TokShiftRight:
			c.emit(vm.OpShiftRight, 0, 0, 0, types.NewNull())
		default:
			return fmt.Errorf("opérateur binaire non supporté: %v", n.Op)
		}

	case TernaryExpr:
		if err := c.compileNode(n.Cond); err != nil {
			return err
		}
		if n.TrueBranch == nil {
			// Short ternary: cond ?: falseBranch
			c.emit(vm.OpDup, 0, 0, 0, types.NewNull())
			jmpTrue := c.emit(vm.OpJmpNZ, 0, 0, 0, types.NewNull())
			c.emit(vm.OpPop, 0, 0, 0, types.NewNull())
			if err := c.compileNode(n.FalseBranch); err != nil {
				return err
			}
			c.instructions[jmpTrue].A = uint16(len(c.instructions))
		} else {
			jmpElse := c.emit(vm.OpJmpZ, 0, 0, 0, types.NewNull())
			if err := c.compileNode(n.TrueBranch); err != nil {
				return err
			}
			jmpEnd := c.emit(vm.OpJmp, 0, 0, 0, types.NewNull())
			c.instructions[jmpElse].A = uint16(len(c.instructions))
			if err := c.compileNode(n.FalseBranch); err != nil {
				return err
			}
			c.instructions[jmpEnd].A = uint16(len(c.instructions))
		}

	case CallExpr:
		return c.compileCall(n)

	case MatchExpr:
		return c.compileMatch(n)

	case ExprStmt:
		if err := c.compileNode(n.Expr); err != nil {
			return err
		}
		// If the expression leaves a value on stack and is not a return/call, pop it
		if _, ok := n.Expr.(CallExpr); !ok {
			// keep clean stack
		}

	case AssignStmt:
		return c.compileAssign(n)

	case EchoStmt:
		for _, expr := range n.Exprs {
			if err := c.compileNode(expr); err != nil {
				return err
			}
			c.emit(vm.OpEcho, 0, 0, 0, types.NewNull())
		}

	case PrintStmt:
		if err := c.compileNode(n.Expr); err != nil {
			return err
		}
		c.emit(vm.OpPrint, 0, 0, 0, types.NewNull())

	case ReturnStmt:
		if n.Val != nil {
			if err := c.compileNode(n.Val); err != nil {
				return err
			}
		} else {
			c.emit(vm.OpLoadConst, 0, 0, 0, types.NewNull())
		}
		if c.isFunction {
			c.emit(vm.OpRetFrame, 0, 0, 0, types.NewNull())
		} else {
			c.emit(vm.OpReturn, 0, 0, 0, types.NewNull())
		}

	case IfStmt:
		if err := c.compileNode(n.Cond); err != nil {
			return err
		}
		jmpElse := c.emit(vm.OpJmpZ, 0, 0, 0, types.NewNull())
		for _, stmt := range n.Body {
			if err := c.compileNode(stmt); err != nil {
				return err
			}
		}
		if len(n.ElseBody) > 0 {
			jmpEnd := c.emit(vm.OpJmp, 0, 0, 0, types.NewNull())
			c.instructions[jmpElse].A = uint16(len(c.instructions))
			for _, stmt := range n.ElseBody {
				if err := c.compileNode(stmt); err != nil {
					return err
				}
			}
			c.instructions[jmpEnd].A = uint16(len(c.instructions))
		} else {
			c.instructions[jmpElse].A = uint16(len(c.instructions))
		}

	case WhileStmt:
		start := len(c.instructions)
		c.loopStarts = append(c.loopStarts, start)
		if err := c.compileNode(n.Cond); err != nil {
			return err
		}
		jmpEnd := c.emit(vm.OpJmpZ, 0, 0, 0, types.NewNull())
		c.loopEnds = append(c.loopEnds, jmpEnd)

		for _, stmt := range n.Body {
			if err := c.compileNode(stmt); err != nil {
				return err
			}
		}

		c.emit(vm.OpJmp, uint16(start), 0, 0, types.NewNull())
		c.instructions[jmpEnd].A = uint16(len(c.instructions))
		c.loopStarts = c.loopStarts[:len(c.loopStarts)-1]
		c.loopEnds = c.loopEnds[:len(c.loopEnds)-1]

	case ForStmt:
		for _, init := range n.Init {
			if err := c.compileNode(init); err != nil {
				return err
			}
		}
		start := len(c.instructions)
		c.loopStarts = append(c.loopStarts, start)

		var jmpEnd int = -1
		if n.Cond != nil {
			if err := c.compileNode(n.Cond); err != nil {
				return err
			}
			jmpEnd = c.emit(vm.OpJmpZ, 0, 0, 0, types.NewNull())
			c.loopEnds = append(c.loopEnds, jmpEnd)
		}

		for _, stmt := range n.Body {
			if err := c.compileNode(stmt); err != nil {
				return err
			}
		}

		for _, post := range n.Post {
			if err := c.compileNode(post); err != nil {
				return err
			}
		}

		c.emit(vm.OpJmp, uint16(start), 0, 0, types.NewNull())
		if jmpEnd >= 0 {
			c.instructions[jmpEnd].A = uint16(len(c.instructions))
			c.loopEnds = c.loopEnds[:len(c.loopEnds)-1]
		}
		c.loopStarts = c.loopStarts[:len(c.loopStarts)-1]

	case ForeachStmt:
		// 1. Evaluate array
		if err := c.compileNode(n.Array); err != nil {
			return err
		}
		// 2. OpArrayIterInit
		c.emit(vm.OpArrayIterInit, 0, 0, 0, types.NewNull())
		start := len(c.instructions)

		// 3. OpArrayIterNext (placeholder jump to end)
		jmpEnd := c.emit(vm.OpArrayIterNext, 0, 0, 0, types.NewNull())

		// 4. Store values
		valSlot := c.getVarSlot(n.ValVar)
		c.emit(vm.OpStoreVar, valSlot, 0, 0, types.NewNull())
		if n.KeyVar != "" {
			keySlot := c.getVarSlot(n.KeyVar)
			c.emit(vm.OpStoreVar, keySlot, 0, 0, types.NewNull())
		} else {
			c.emit(vm.OpPop, 0, 0, 0, types.NewNull()) // discard key
		}

		// 5. Loop body
		for _, stmt := range n.Body {
			if err := c.compileNode(stmt); err != nil {
				return err
			}
		}

		// 6. Jump back to start
		c.emit(vm.OpJmp, uint16(start), 0, 0, types.NewNull())
		c.instructions[jmpEnd].A = uint16(len(c.instructions))

	case SwitchStmt:
		return c.compileSwitch(n)

	case BlockStmt:
		for _, s := range n.Stmts {
			if err := c.compileNode(s); err != nil {
				return err
			}
		}

	case ThrowStmt:
		if err := c.compileNode(n.Expr); err != nil {
			return err
		}
		c.emit(vm.OpThrow, 0, 0, 0, types.NewNull())

	case TryCatchStmt:
		return c.compileTryCatch(n)

	case GlobalStmt:
		// Registers variables in frame
		for _, vName := range n.Vars {
			_ = c.getVarSlot(vName)
		}

	case ClosureExpr:
		return c.compileClosure(n.Params, n.Uses, n.Body)

	case ArrowFuncExpr:
		return c.compileClosure(n.Params, nil, []Node{ReturnStmt{Val: n.Expr}})

	case IncludeExpr:
		// Evaluate path expression
		if err := c.compileNode(n.Path); err != nil {
			return err
		}
		c.emit(vm.OpBuiltinCall, 100, 1, 0, types.NewNull()) // Include builtin handler

	case BreakStmt:
		if len(c.loopEnds) > 0 {
			c.emit(vm.OpJmp, uint16(c.loopEnds[len(c.loopEnds)-1]), 0, 0, types.NewNull())
		}

	case ContinueStmt:
		if len(c.loopStarts) > 0 {
			c.emit(vm.OpJmp, uint16(c.loopStarts[len(c.loopStarts)-1]), 0, 0, types.NewNull())
		}
	}

	return nil
}

func (c *Compiler) compileAssign(n AssignStmt) error {
	switch target := n.Target.(type) {
	case VarExpr:
		slot := c.getVarSlot(target.Name)
		if n.Op == TokAssign {
			if err := c.compileNode(n.Val); err != nil {
				return err
			}
		} else {
			// Compound assignment: $a += expr -> load $a, expr, add, store $a
			c.emit(vm.OpLoadVar, slot, 0, 0, types.NewNull())
			if err := c.compileNode(n.Val); err != nil {
				return err
			}
			switch n.Op {
			case TokPlusAssign:
				c.emit(vm.OpAdd, 0, 0, 0, types.NewNull())
			case TokMinusAssign:
				c.emit(vm.OpSub, 0, 0, 0, types.NewNull())
			case TokMulAssign:
				c.emit(vm.OpMul, 0, 0, 0, types.NewNull())
			case TokDivAssign:
				c.emit(vm.OpDiv, 0, 0, 0, types.NewNull())
			case TokDotAssign:
				c.emit(vm.OpConcat, 0, 0, 0, types.NewNull())
			}
		}
		c.emit(vm.OpStoreVar, slot, 0, 0, types.NewNull())

	case ArrayAccessExpr:
		if err := c.compileNode(target.Target); err != nil {
			return err
		}
		if err := c.compileNode(target.Index); err != nil {
			return err
		}
		if err := c.compileNode(n.Val); err != nil {
			return err
		}
		c.emit(vm.OpArraySet, 0, 0, 0, types.NewNull())

	case PropertyAccessExpr:
		if err := c.compileNode(target.Target); err != nil {
			return err
		}
		if err := c.compileNode(n.Val); err != nil {
			return err
		}
		propSlot := c.findPropSlot(target.Property)
		c.emit(vm.OpSetProp, propSlot, 0, 0, types.NewNull())
	}
	return nil
}

func (c *Compiler) compileCall(n CallExpr) error {
	lower := strings.ToLower(n.Callee)

	// Built-in compiler functions
	switch lower {
	case "ob_start":
		c.emit(vm.OpObStart, 0, 0, 0, types.NewNull())
		return nil

	case "ob_get_clean":
		c.emit(vm.OpObGetClean, 0, 0, 0, types.NewNull())
		return nil

	case "ob_get_contents":
		c.emit(vm.OpObGetContents, 0, 0, 0, types.NewNull())
		return nil

	case "ob_end_flush":
		c.emit(vm.OpObEndFlush, 0, 0, 0, types.NewNull())
		return nil

	case "strlen":
		if len(n.Args) >= 1 {
			if err := c.compileNode(n.Args[0]); err != nil {
				return err
			}
			c.emit(vm.OpStrLen, 0, 0, 0, types.NewNull())
		}
		return nil

	case "strpos":
		if len(n.Args) >= 2 {
			if err := c.compileNode(n.Args[0]); err != nil { // haystack
				return err
			}
			if err := c.compileNode(n.Args[1]); err != nil { // needle
				return err
			}
			c.emit(vm.OpStrPos, 0, 0, 0, types.NewNull())
		}
		return nil

	case "substr":
		if len(n.Args) >= 2 {
			if err := c.compileNode(n.Args[0]); err != nil { // str
				return err
			}
			if err := c.compileNode(n.Args[1]); err != nil { // start
				return err
			}
			if len(n.Args) >= 3 {
				if err := c.compileNode(n.Args[2]); err != nil { // length
					return err
				}
			} else {
				c.emit(vm.OpLoadConst, 0, 0, 0, types.NewLong(1000000000))
			}
			c.emit(vm.OpSubstr, 0, 0, 0, types.NewNull())
		}
		return nil

	case "preg_match":
		if len(n.Args) >= 2 {
			if err := c.compileNode(n.Args[0]); err != nil { // pattern
				return err
			}
			if err := c.compileNode(n.Args[1]); err != nil { // text
				return err
			}
			c.emit(vm.OpPregMatch, 0, 0, 0, types.NewNull())
		}
		return nil

	case "blueteam_entropy":
		if len(n.Args) >= 1 {
			if err := c.compileNode(n.Args[0]); err != nil {
				return err
			}
			c.emit(vm.OpBlueteamEntropy, 0, 0, 0, types.NewNull())
		}
		return nil

	case "blueteam_classify":
		if len(n.Args) >= 1 {
			if err := c.compileNode(n.Args[0]); err != nil {
				return err
			}
			c.emit(vm.OpBlueteamClassify, 0, 0, 0, types.NewNull())
		}
		return nil

	case "security_check":
		if len(n.Args) >= 2 {
			if err := c.compileNode(n.Args[0]); err != nil { // sinkName
				return err
			}
			if err := c.compileNode(n.Args[1]); err != nil { // arg
				return err
			}
			c.emit(vm.OpSecurityCheck, 0, 0, 0, types.NewNull())
		}
		return nil

	case "count":
		if len(n.Args) >= 1 {
			if err := c.compileNode(n.Args[0]); err != nil {
				return err
			}
			c.emit(vm.OpArrayLen, 0, 0, 0, types.NewNull())
		}
		return nil

	case "array_pop":
		if len(n.Args) >= 1 {
			if err := c.compileNode(n.Args[0]); err != nil {
				return err
			}
			c.emit(vm.OpArrayPop, 0, 0, 0, types.NewNull())
		}
		return nil

	// Builtins standard SPL
	case "json_encode":
		return c.compileBuiltin(1, n.Args)
	case "json_decode":
		return c.compileBuiltin(2, n.Args)
	case "md5":
		return c.compileBuiltin(3, n.Args)
	case "sha256":
		return c.compileBuiltin(4, n.Args)
	case "hash":
		return c.compileBuiltin(5, n.Args)
	case "hash_hmac":
		return c.compileBuiltin(6, n.Args)
	case "random_bytes":
		return c.compileBuiltin(7, n.Args)
	case "bin2hex":
		return c.compileBuiltin(8, n.Args)
	case "hex2bin":
		return c.compileBuiltin(9, n.Args)
	case "time":
		return c.compileBuiltin(10, n.Args)
	case "microtime":
		return c.compileBuiltin(11, n.Args)
	case "date":
		return c.compileBuiltin(12, n.Args)
	case "strtotime":
		return c.compileBuiltin(13, n.Args)
	case "trim":
		return c.compileBuiltin(14, n.Args)
	case "ltrim":
		return c.compileBuiltin(15, n.Args)
	case "rtrim":
		return c.compileBuiltin(16, n.Args)
	case "strtolower":
		return c.compileBuiltin(17, n.Args)
	case "strtoupper":
		return c.compileBuiltin(18, n.Args)
	case "str_replace":
		return c.compileBuiltin(19, n.Args)
	case "str_repeat":
		return c.compileBuiltin(20, n.Args)
	case "is_callable":
		return c.compileBuiltin(21, n.Args)
	case "file_get_contents":
		return c.compileBuiltin(30, n.Args)
	case "file_put_contents":
		return c.compileBuiltin(31, n.Args)
	case "file_exists":
		return c.compileBuiltin(32, n.Args)
	case "dirname":
		return c.compileBuiltin(33, n.Args)
	case "basename":
		return c.compileBuiltin(34, n.Args)
	case "pdo_exec":
		return c.compileBuiltin(35, n.Args)
	case "pdo_query":
		return c.compileBuiltin(36, n.Args)
	case "pdo_last_id":
		return c.compileBuiltin(37, n.Args)
	case "fiber_create":
		return c.compileBuiltin(40, n.Args)
	case "fiber_start":
		return c.compileBuiltin(41, n.Args)
	case "fiber_suspend":
		return c.compileBuiltin(42, n.Args)
	case "fiber_resume":
		return c.compileBuiltin(43, n.Args)
	case "fiber_is_suspended":
		return c.compileBuiltin(44, n.Args)
	case "fiber_is_running":
		return c.compileBuiltin(45, n.Args)
	case "fiber_is_terminated":
		return c.compileBuiltin(46, n.Args)
	case "getenv":
		return c.compileBuiltin(50, n.Args)
	case "putenv":
		return c.compileBuiltin(51, n.Args)
	case "parse_dotenv":
		return c.compileBuiltin(52, n.Args)
	case "uuid_v4":
		return c.compileBuiltin(53, n.Args)
	case "uuid_v7":
		return c.compileBuiltin(54, n.Args)
	case "ulid":
		return c.compileBuiltin(55, n.Args)
	case "monolog_log":
		return c.compileBuiltin(56, n.Args)
	case "curl_init":
		return c.compileBuiltin(57, n.Args)
	case "curl_setopt":
		return c.compileBuiltin(58, n.Args)
	case "curl_exec":
		return c.compileBuiltin(59, n.Args)
	case "curl_getinfo":
		return c.compileBuiltin(60, n.Args)
	case "curl_close":
		return c.compileBuiltin(61, n.Args)
	case "curl_error":
		return c.compileBuiltin(62, n.Args)
	case "curl_errno":
		return c.compileBuiltin(63, n.Args)
	}

	// Dynamic closure/variable call: $fn(...)
	vName := strings.TrimPrefix(n.Callee, "$")
	if vSlot, ok := c.varMap[vName]; ok {
		for _, arg := range n.Args {
			if err := c.compileNode(arg); err != nil {
				return err
			}
		}
		c.emit(vm.OpLoadVar, vSlot, 0, 0, types.NewNull())
		c.emit(vm.OpCallDynamic, uint16(len(n.Args)), 0, 0, types.NewNull())
		return nil
	}

	// User function call or Fallback Foreign Call
	funcIdx, exists := c.funcMap[lower]
	if !exists {
		for _, arg := range n.Args {
			if err := c.compileNode(arg); err != nil {
				return err
			}
		}
		strIdx := c.addString(n.Callee)
		c.emit(vm.OpBuiltinCall, 255, uint16(len(n.Args)), strIdx, types.NewNull())
		return nil
	}

	for _, arg := range n.Args {
		if err := c.compileNode(arg); err != nil {
			return err
		}
	}
	c.emit(vm.OpCall, funcIdx, uint16(len(n.Args)), 0, types.NewNull())
	return nil
}

func (c *Compiler) compileMatch(n MatchExpr) error {
	// Evaluate subject and store in temporary slot
	tempSlot := c.nextVar
	c.nextVar++
	if err := c.compileNode(n.Expr); err != nil {
		return err
	}
	c.emit(vm.OpStoreVar, tempSlot, 0, 0, types.NewNull())

	var endJumps []int

	for _, arm := range n.Arms {
		if arm.Conds == nil {
			// Default arm
			if err := c.compileNode(arm.Body); err != nil {
				return err
			}
			break
		}

		var nextArmJmps []int
		for _, cond := range arm.Conds {
			c.emit(vm.OpLoadVar, tempSlot, 0, 0, types.NewNull())
			if err := c.compileNode(cond); err != nil {
				return err
			}
			c.emit(vm.OpIdentical, 0, 0, 0, types.NewNull())
			jmpMatch := c.emit(vm.OpJmpNZ, 0, 0, 0, types.NewNull())
			nextArmJmps = append(nextArmJmps, jmpMatch)
		}

		// If no condition matched, jump to next arm
		jmpNext := c.emit(vm.OpJmp, 0, 0, 0, types.NewNull())

		// Patch match jumps to execute body
		bodyPos := len(c.instructions)
		for _, jmp := range nextArmJmps {
			c.instructions[jmp].A = uint16(bodyPos)
		}

		if err := c.compileNode(arm.Body); err != nil {
			return err
		}
		endJumps = append(endJumps, c.emit(vm.OpJmp, 0, 0, 0, types.NewNull()))
		c.instructions[jmpNext].A = uint16(len(c.instructions))
	}

	endPos := len(c.instructions)
	for _, jmp := range endJumps {
		c.instructions[jmp].A = uint16(endPos)
	}

	return nil
}

func (c *Compiler) compileSwitch(n SwitchStmt) error {
	tempSlot := c.nextVar
	c.nextVar++
	if err := c.compileNode(n.Expr); err != nil {
		return err
	}
	c.emit(vm.OpStoreVar, tempSlot, 0, 0, types.NewNull())

	var endJumps []int

	for _, sc := range n.Cases {
		if sc.Values == nil {
			// Default case
			for _, stmt := range sc.Body {
				if err := c.compileNode(stmt); err != nil {
					return err
				}
			}
			break
		}

		var matchJmps []int
		for _, val := range sc.Values {
			c.emit(vm.OpLoadVar, tempSlot, 0, 0, types.NewNull())
			if err := c.compileNode(val); err != nil {
				return err
			}
			c.emit(vm.OpEq, 0, 0, 0, types.NewNull())
			matchJmps = append(matchJmps, c.emit(vm.OpJmpNZ, 0, 0, 0, types.NewNull()))
		}

		jmpNext := c.emit(vm.OpJmp, 0, 0, 0, types.NewNull())
		bodyPos := len(c.instructions)
		for _, jmp := range matchJmps {
			c.instructions[jmp].A = uint16(bodyPos)
		}

		for _, stmt := range sc.Body {
			if err := c.compileNode(stmt); err != nil {
				return err
			}
		}
		endJumps = append(endJumps, c.emit(vm.OpJmp, 0, 0, 0, types.NewNull()))
		c.instructions[jmpNext].A = uint16(len(c.instructions))
	}

	endPos := len(c.instructions)
	for _, jmp := range endJumps {
		c.instructions[jmp].A = uint16(endPos)
	}

	return nil
}

func (c *Compiler) compileBuiltin(builtinId uint16, args []Node) error {
	for _, arg := range args {
		if err := c.compileNode(arg); err != nil {
			return err
		}
	}
	c.emit(vm.OpBuiltinCall, builtinId, uint16(len(args)), 0, types.NewNull())
	return nil
}

func (c *Compiler) compileTryCatch(n TryCatchStmt) error {
	startPC := len(c.instructions)

	for _, stmt := range n.TryBody {
		if err := c.compileNode(stmt); err != nil {
			return err
		}
	}

	jmpEndTry := c.emit(vm.OpJmp, 0, 0, 0, types.NewNull())
	endPC := len(c.instructions)

	var endJumps []int

	for _, catch := range n.Catches {
		catchPC := len(c.instructions)
		varSlot := c.getVarSlot(catch.Var)

		if len(catch.Types) == 0 {
			c.exceptionHandlers = append(c.exceptionHandlers, vm.ExceptionHandler{
				StartPC:   startPC,
				EndPC:     endPC,
				CatchPC:   catchPC,
				CatchType: "Throwable",
				CatchVar:  varSlot,
			})
		} else {
			for _, tName := range catch.Types {
				c.exceptionHandlers = append(c.exceptionHandlers, vm.ExceptionHandler{
					StartPC:   startPC,
					EndPC:     endPC,
					CatchPC:   catchPC,
					CatchType: tName,
					CatchVar:  varSlot,
				})
			}
		}

		for _, stmt := range catch.Body {
			if err := c.compileNode(stmt); err != nil {
				return err
			}
		}

		endJumps = append(endJumps, c.emit(vm.OpJmp, 0, 0, 0, types.NewNull()))
	}

	endPos := len(c.instructions)
	c.instructions[jmpEndTry].A = uint16(endPos)
	for _, jmp := range endJumps {
		c.instructions[jmp].A = uint16(endPos)
	}

	if len(n.FinallyBody) > 0 {
		for _, stmt := range n.FinallyBody {
			if err := c.compileNode(stmt); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Compiler) compileClosure(params []string, uses []string, body []Node) error {
	fIdx := uint16(len(c.functions))
	subCompiler := NewCompiler()
	subCompiler.isFunction = true
	subCompiler.classes = c.classes
	subCompiler.classMap = c.classMap
	subCompiler.funcMap = c.funcMap
	subCompiler.functions = c.functions

	// Arguments mapped via OpGetArg
	for i, pName := range params {
		pVar := subCompiler.getVarSlot(pName)
		subCompiler.emit(vm.OpGetArg, uint16(i), 0, 0, types.NewNull())
		subCompiler.emit(vm.OpStoreVar, pVar, 0, 0, types.NewNull())
	}

	for _, stmt := range body {
		if err := subCompiler.compileNode(stmt); err != nil {
			return err
		}
	}

	if len(subCompiler.instructions) == 0 || subCompiler.instructions[len(subCompiler.instructions)-1].Op != vm.OpRetFrame {
		subCompiler.emit(vm.OpLoadConst, 0, 0, 0, types.NewNull())
		subCompiler.emit(vm.OpRetFrame, 0, 0, 0, types.NewNull())
	}

	c.functions = append(c.functions, vm.BytecodeProgram{
		Instructions:      subCompiler.instructions,
		Constants:         subCompiler.constants,
		StringPool:        subCompiler.stringPool,
		Classes:           c.classes,
		ClassMap:          c.classMap,
		ExceptionHandlers: subCompiler.exceptionHandlers,
		NumVars:           subCompiler.nextVar,
	})

	// Pushes closure function index as a callable Long/Object
	c.emit(vm.OpLoadConst, 0, 0, 0, types.NewLong(int64(fIdx)))
	return nil
}

func (c *Compiler) registerBuiltinClasses() {
	builtins := []struct {
		name       string
		parent     string
		implements []string
		methods    []string
		props      []string
		constants  map[string]types.Value
	}{
		{name: "Throwable", parent: "", methods: []string{"getMessage", "getCode", "getFile", "getLine", "getTraceAsString"}},
		{name: "Exception", parent: "", implements: []string{"Throwable"}, props: []string{"message", "code", "file", "line"}, methods: []string{"__construct", "getMessage", "getCode"}},
		{name: "RuntimeException", parent: "Exception", implements: []string{"Throwable"}, props: []string{"message", "code", "file", "line"}, methods: []string{"__construct", "getMessage"}},
		{name: "LogicException", parent: "Exception", implements: []string{"Throwable"}, props: []string{"message", "code", "file", "line"}, methods: []string{"__construct", "getMessage"}},
		{name: "InvalidArgumentException", parent: "LogicException", implements: []string{"Throwable"}, props: []string{"message", "code", "file", "line"}, methods: []string{"__construct", "getMessage"}},
		{name: "PDOException", parent: "RuntimeException", implements: []string{"Throwable"}, props: []string{"message", "code", "file", "line"}, methods: []string{"__construct", "getMessage"}},
		{name: "Error", parent: "", implements: []string{"Throwable"}, props: []string{"message", "code", "file", "line"}, methods: []string{"__construct", "getMessage", "getCode"}},
		{name: "TypeError", parent: "Error", implements: []string{"Throwable"}, props: []string{"message", "code", "file", "line"}, methods: []string{"__construct", "getMessage"}},
		{name: "ValueError", parent: "Error", implements: []string{"Throwable"}, props: []string{"message", "code", "file", "line"}, methods: []string{"__construct", "getMessage"}},
		{name: "DivisionByZeroError", parent: "Error", implements: []string{"Throwable"}, props: []string{"message", "code", "file", "line"}, methods: []string{"__construct", "getMessage"}},
		{name: "ParseError", parent: "Error", implements: []string{"Throwable"}, props: []string{"message", "code", "file", "line"}, methods: []string{"__construct", "getMessage"}},
		{name: "ArithmeticError", parent: "Error", implements: []string{"Throwable"}, props: []string{"message", "code", "file", "line"}, methods: []string{"__construct", "getMessage"}},
		{name: "UnhandledMatchError", parent: "Error", implements: []string{"Throwable"}, props: []string{"message", "code", "file", "line"}, methods: []string{"__construct", "getMessage"}},
		{
			name:   "PDO",
			parent: "",
			props:  []string{"_pdo_id", "dsn"},
			constants: map[string]types.Value{
				"ATTR_ERRMODE":            types.NewLong(1),
				"ATTR_DEFAULT_FETCH_MODE": types.NewLong(19),
				"ERRMODE_SILENT":          types.NewLong(0),
				"ERRMODE_WARNING":         types.NewLong(1),
				"ERRMODE_EXCEPTION":       types.NewLong(2),
				"FETCH_ASSOC":             types.NewLong(2),
				"FETCH_NUM":               types.NewLong(3),
				"FETCH_BOTH":              types.NewLong(4),
				"FETCH_OBJ":               types.NewLong(5),
				"FETCH_COLUMN":            types.NewLong(7),
			},
			methods: []string{"__construct", "exec", "query", "prepare", "beginTransaction", "commit", "rollBack", "inTransaction", "lastInsertId", "setAttribute", "getAttribute"},
		},
		{
			name:    "PDOStatement",
			parent:  "",
			props:   []string{"_stmt_id", "queryString"},
			methods: []string{"execute", "fetch", "fetchAll", "fetchColumn", "rowCount", "bindValue", "closeCursor"},
		},
	}

	for _, b := range builtins {
		nameLower := strings.ToLower(b.name)
		if _, exists := c.classMap[nameLower]; exists {
			continue
		}
		clsIdx := uint16(len(c.classes))
		c.classMap[nameLower] = clsIdx

		clsDef := vm.ClassDef{
			Name:        b.name,
			Parent:      b.parent,
			Implements:  b.implements,
			Methods:     make(map[string]uint16),
			Constants:   b.constants,
			PropOffsets: make(map[string]int),
		}

		for _, pName := range b.props {
			pOffset := len(clsDef.PropNames)
			clsDef.PropNames = append(clsDef.PropNames, pName)
			clsDef.PropOffsets[pName] = pOffset
			clsDef.PropDefaults = append(clsDef.PropDefaults, types.NewNull())
		}

		for _, mName := range b.methods {
			mFuncIdx := uint16(len(c.functions))
			mNameLower := strings.ToLower(mName)
			clsDef.Methods[mNameLower] = mFuncIdx
			c.funcMap[nameLower+"::"+mNameLower] = mFuncIdx
			c.functions = append(c.functions, vm.BytecodeProgram{
				Instructions: []vm.Instruction{
					{Op: vm.OpRetFrame, Const: types.NewNull()},
				},
			})
		}

		c.classes = append(c.classes, clsDef)
	}
}
