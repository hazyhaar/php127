package compiler

import (
	"code.hazyhaar.fr/devhoros/php127/ast"
	bc "code.hazyhaar.fr/devhoros/php127/bytecode"
	"strconv"
	"strings"
)

func (e *emitter) declareClasses() {
	type declaration struct {
		proto     *bc.ClassProto
		members   []ast.ClassMember
		namespace string
	}
	var decls []declaration
	var collectStmts func(stmts []ast.Stmt, ns string, topLevel bool)
	collectStmts = func(stmts []ast.Stmt, ns string, topLevel bool) {
		currentNS := ns
		for _, stmt := range stmts {
			var p *bc.ClassProto
			var members []ast.ClassMember
			switch s := stmt.(type) {
			case *ast.NamespaceDecl:
				if len(s.Body) > 0 {
					collectStmts(s.Body, s.Name, topLevel)
				} else {
					currentNS = s.Name
				}
				continue
			case *ast.IfStmt:
				collectStmts(s.Body, currentNS, false)
				for _, cl := range s.ElseIfs {
					collectStmts(cl.Body, currentNS, false)
				}
				collectStmts(s.Else, currentNS, false)
				continue
			case *ast.BlockStmt:
				collectStmts(s.Body, currentNS, false)
				continue
			case *ast.DeclareStmt:
				collectStmts(s.Body, currentNS, topLevel)
				continue
			case *ast.SwitchStmt:
				for _, c := range s.Cases {
					collectStmts(c.Body, currentNS, false)
				}
				continue
			case *ast.TryStmt:
				collectStmts(s.Body, currentNS, false)
				for _, c := range s.Catches {
					collectStmts(c.Body, currentNS, false)
				}
				collectStmts(s.Finally, currentNS, false)
				continue
			case *ast.FunctionDecl:
				collectStmts(s.Body, currentNS, false)
				continue
			case *ast.EnumDecl:
				p = &bc.ClassProto{Name: s.Name, Kind: bc.KindEnum, BackingType: s.ScalarType, Interfaces: append([]string{"UnitEnum"}, s.Implements...), Attributes: e.attributes(s.Attrs)}
				if s.ScalarType != "" {
					p.Interfaces = append(p.Interfaces, "BackedEnum")
				}
				members = s.Members
			case *ast.ClassDecl:
				var flags uint16
				if s.Modifiers&ast.Abstract != 0 {
					flags |= bc.ClassAbstract
				}
				if s.Modifiers&ast.Final != 0 {
					flags |= bc.ClassFinal
				}
				if s.Modifiers&ast.Readonly != 0 {
					flags |= bc.ClassReadonly
				}
				p = &bc.ClassProto{Name: s.Name, Flags: flags, ParentName: s.Extends, Interfaces: s.Implements, Attributes: e.attributes(s.Attrs)}
				members = s.Members
			case *ast.InterfaceDecl:
				p = &bc.ClassProto{Name: s.Name, Kind: bc.KindInterface, Interfaces: s.Extends, Attributes: e.attributes(s.Attrs)}
				members = s.Members
			case *ast.TraitDecl:
				p = &bc.ClassProto{Name: s.Name, Kind: bc.KindTrait, Attributes: e.attributes(s.Attrs)}
				members = s.Members
			default:
				continue
			}
			name := strings.ToLower(p.Name)
			if e.classes[name] != nil {
				if topLevel {
					e.fail("redeclaration de classe " + p.Name)
				}
				continue
			}
			p.ID = e.allocClassID()
			if p.Kind == bc.KindEnum {
				p.Props = append(p.Props, bc.PropInfo{Name: "name", TypeName: "string", Readonly: true, ClassID: p.ID})
				if p.BackingType != "" {
					p.Props = append(p.Props, bc.PropInfo{Name: "value", TypeName: p.BackingType, Readonly: true, ClassID: p.ID})
				}
			}
			e.classes[name] = p
			decls = append(decls, declaration{p, members, currentNS})
			if p.Kind == bc.KindTrait {
				e.prog.Traits = append(e.prog.Traits, p)
			} else {
				e.prog.Classes = append(e.prog.Classes, p)
			}
		}
	}
	collectStmts(e.file.Stmts, "", true)
	for _, d := range decls {
		seen := make(map[string]bool)
		for p := d.proto; p != nil; p = e.classes[strings.ToLower(p.ParentName)] {
			if seen[p.Name] {
				e.fail("heritage cyclique")
			}
			seen[p.Name] = true
			if p.ParentName == "" {
				break
			}
		}
	}
	// Premiere passe sur les constantes et cas d'enum pour permettre les references croisees.
	for _, d := range decls {
		prevClass, prevNS := e.class, e.namespace
		e.class, e.namespace = d.proto, d.namespace
		for _, member := range d.members {
			switch m := member.(type) {
			case *ast.EnumCaseDecl:
				c := bc.EnumCase{Name: m.Name, Attributes: e.attributes(m.Attrs)}
				if m.Value != nil {
					c.Value = e.constant(m.Value)
				}
				d.proto.Cases = append(d.proto.Cases, c)
			case *ast.ClassConstDecl:
				for _, c := range m.Consts {
					d.proto.Constants = append(d.proto.Constants, bc.ClassConst{
						Name:       c.Name,
						Initial:    e.constant(c.Value),
						Visibility: visibility(m.Modifiers),
						Final:      m.Modifiers&ast.Final != 0,
						Attributes: e.attributes(m.Attrs),
					})
				}
			}
		}
		e.class, e.namespace = prevClass, prevNS
	}
	for _, d := range decls {
		prevClass, prevNS := e.class, e.namespace
		e.class, e.namespace = d.proto, d.namespace
		for _, member := range d.members {
			switch m := member.(type) {
			case *ast.EnumCaseDecl, *ast.ClassConstDecl:
			case *ast.MethodDecl:
				child := e.compilation.emitter(m.Name)
				child.class = d.proto
				child.namespace = d.namespace
				child.line = uint32(m.Line)
				child.fn.ReturnByRef = m.ByRef
				if strings.EqualFold(m.Name, "__construct") {
					for _, prm := range m.Params {
						if prm.Modifiers&(ast.Public|ast.Protected|ast.Private) != 0 {
							info := bc.PropInfo{
								Name:       prm.Name,
								Visibility: visibility(prm.Modifiers),
								Readonly:   prm.Modifiers&ast.Readonly != 0 || d.proto.Flags&bc.ClassReadonly != 0,
								Promoted:   true,
								TypeName:   typeName(prm.Type),
								ClassID:    d.proto.ID,
								Attributes: e.attributes(prm.Attrs),
							}
							d.proto.Props = append(d.proto.Props, info)
						}
					}
				}
				child.params(m.Params)
				child.fn.ReturnType = typeName(m.ReturnType)
				child.fn.Attributes = e.attributes(m.Attrs)
				if m.Modifiers&ast.Static == 0 {
					child.cv("this")
					if strings.EqualFold(m.Name, "__construct") {
						for _, prm := range m.Params {
							if prm.Modifiers&(ast.Public|ast.Protected|ast.Private) != 0 {
								child.emit(bc.OpLoadCV, child.cv("this"), 0, bc.FlagUseThis)
								child.emit(bc.OpLoadCV, child.cv(prm.Name), 0, 0)
								child.emit(bc.OpAssignProp, child.str(prm.Name), 0, 0)
								child.emit(bc.OpPop, 0, 0, 0)
							}
						}
					}
				}
				child.stmts(m.Body)
				child.end()
				d.proto.Methods = append(d.proto.Methods, bc.MethodProto{
					Name:       m.Name,
					Visibility: visibility(m.Modifiers),
					Static:     m.Modifiers&ast.Static != 0,
					Abstract:   !m.HasBody || m.Modifiers&ast.Abstract != 0,
					Final:      m.Modifiers&ast.Final != 0,
					Func:       child.fn,
					ClassID:    d.proto.ID,
					Attributes: e.attributes(m.Attrs),
				})
			case *ast.PropertyDecl:
				for _, p := range m.Props {
					info := bc.PropInfo{
						Name:       p.Name,
						Visibility: visibility(m.Modifiers),
						Readonly:   m.Modifiers&ast.Readonly != 0 || (d.proto.Flags&bc.ClassReadonly != 0 && m.Modifiers&ast.Static == 0),
						Static:     m.Modifiers&ast.Static != 0,
						TypeName:   typeName(m.Type),
						ClassID:    d.proto.ID,
						Attributes: e.attributes(m.Attrs),
					}
					if p.Default != nil {
						info.Initial = e.constant(p.Default)
						info.HasDefault = true
					}
					d.proto.Props = append(d.proto.Props, info)
				}
			case *ast.TraitUseDecl:
				d.proto.Traits = append(d.proto.Traits, m.Traits...)
			default:
				e.fail("membre de classe")
			}
		}
		e.class, e.namespace = prevClass, prevNS
	}
}
func visibility(mod ast.Modifiers) bc.Visibility {
	if mod&ast.Private != 0 {
		return bc.VisPrivate
	}
	if mod&ast.Protected != 0 {
		return bc.VisProtected
	}
	return bc.VisPublic
}
func (e *emitter) name(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.ConstFetchExpr:
		return n.Name
	case *ast.StringLit:
		return n.Value
	case *ast.VarExpr:
		if n.Name != "" {
			return n.Name
		}
		if n.Dynamic != nil {
			return e.name(n.Dynamic)
		}
		return "dyn"
	default:
		return "dyn"
	}
}
func (e *emitter) memberName(expr ast.Expr) string {
	if n, ok := expr.(*ast.VarExpr); ok && n.Dynamic == nil && n.Name != "" {
		return "\x00cv:" + strconv.Itoa(int(e.cv(n.Name)))
	}
	return e.name(expr)
}
func (e *emitter) classOperand(expr ast.Expr) (uint32, uint16) {
	name := e.name(expr)
	switch strings.ToLower(name) {
	case "self":
		return 0, bc.FlagSelf
	case "parent":
		return 0, bc.FlagParent
	case "static":
		return 0, bc.FlagStatic
	}
	return e.classID(name), 0
}

func (e *emitter) staticName(expr ast.Expr) string {
	if v, ok := expr.(*ast.VarExpr); ok {
		if v.Name != "" {
			return v.Name
		}
		if v.Dynamic != nil {
			return e.staticName(v.Dynamic)
		}
		return "dyn"
	}
	return e.name(expr)
}
