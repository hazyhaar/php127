package compiler

import (
	"code.hazyhaar.fr/devhoros/php127/ast"
	bc "code.hazyhaar.fr/devhoros/php127/bytecode"
	"reflect"
	"strconv"
	"strings"
)

func (e *emitter) declareClasses() {
	type declaration struct {
		proto     *bc.ClassProto
		members   []ast.ClassMember
		namespace string
		uses      map[string]string
	}
	var decls []declaration
	prevNS, prevUses := e.namespace, e.uses
	var collectStmts func(stmts []ast.Stmt, ns string, uses map[string]string, topLevel bool)
	collectStmts = func(stmts []ast.Stmt, ns string, uses map[string]string, topLevel bool) {
		currentNS, currentUses := ns, uses
		for _, stmt := range stmts {
			// Les noms déclarés et les noms de parents, d'interfaces et de traits sont résolus dans la portée de la déclaration.
			e.namespace, e.uses = currentNS, currentUses
			var p *bc.ClassProto
			var members []ast.ClassMember
			switch s := stmt.(type) {
			case *ast.NamespaceDecl:
				if len(s.Body) > 0 {
					collectStmts(s.Body, s.Name, e.nsUses[s], topLevel)
				} else {
					currentNS, currentUses = s.Name, e.nsUses[s]
				}
				continue
			case *ast.IfStmt:
				collectStmts(s.Body, currentNS, currentUses, false)
				for _, cl := range s.ElseIfs {
					collectStmts(cl.Body, currentNS, currentUses, false)
				}
				collectStmts(s.Else, currentNS, currentUses, false)
				continue
			case *ast.BlockStmt:
				collectStmts(s.Body, currentNS, currentUses, false)
				continue
			case *ast.DeclareStmt:
				collectStmts(s.Body, currentNS, currentUses, topLevel)
				continue
			case *ast.SwitchStmt:
				for _, c := range s.Cases {
					collectStmts(c.Body, currentNS, currentUses, false)
				}
				continue
			case *ast.TryStmt:
				collectStmts(s.Body, currentNS, currentUses, false)
				for _, c := range s.Catches {
					collectStmts(c.Body, currentNS, currentUses, false)
				}
				collectStmts(s.Finally, currentNS, currentUses, false)
				continue
			case *ast.FunctionDecl:
				collectStmts(s.Body, currentNS, currentUses, false)
				continue
			case *ast.EnumDecl:
				p = &bc.ClassProto{Name: e.qualifyDecl(s.Name), Kind: bc.KindEnum, BackingType: s.ScalarType, Interfaces: append([]string{"UnitEnum"}, e.resolveClassNames(s.Implements)...), Attributes: e.attributes(s.Attrs)}
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
				p = &bc.ClassProto{Name: e.qualifyDecl(s.Name), Flags: flags, ParentName: e.resolveClassName(s.Extends), Interfaces: e.resolveClassNames(s.Implements), Attributes: e.attributes(s.Attrs)}
				members = s.Members
			case *ast.InterfaceDecl:
				p = &bc.ClassProto{Name: e.qualifyDecl(s.Name), Kind: bc.KindInterface, Interfaces: e.resolveClassNames(s.Extends), Attributes: e.attributes(s.Attrs)}
				members = s.Members
			case *ast.TraitDecl:
				p = &bc.ClassProto{Name: e.qualifyDecl(s.Name), Kind: bc.KindTrait, Attributes: e.attributes(s.Attrs)}
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
			decls = append(decls, declaration{p, members, currentNS, currentUses})
			if p.Kind == bc.KindTrait {
				e.prog.Traits = append(e.prog.Traits, p)
			} else {
				e.prog.Classes = append(e.prog.Classes, p)
			}
		}
	}
	collectStmts(e.file.Stmts, "", e.globalUses, true)
	// Les classes anonymes sont déclarées comme les classes nommées, avant la compilation de tout corps : leurs
	// identifiants précèdent ceux des noms seulement référencés, et leurs membres passent par les mêmes étapes.
	var collectAnonymous func(stmts []ast.Stmt, ns string, uses map[string]string)
	collectAnonymous = func(stmts []ast.Stmt, ns string, uses map[string]string) {
		currentNS, currentUses := ns, uses
		for _, stmt := range stmts {
			if s, ok := stmt.(*ast.NamespaceDecl); ok {
				if len(s.Body) > 0 {
					collectAnonymous(s.Body, s.Name, e.nsUses[s])
				} else {
					currentNS, currentUses = s.Name, e.nsUses[s]
				}
				continue
			}
			e.namespace, e.uses = currentNS, currentUses
			forEachAnonymousClass(reflect.ValueOf(stmt), func(c *ast.ClassDecl) {
				decls = append(decls, declaration{e.anonymousClass(c), c.Members, currentNS, currentUses})
			})
		}
	}
	collectAnonymous(e.file.Stmts, "", e.globalUses)
	e.namespace, e.uses = prevNS, prevUses
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
		prevClass, prevNS, prevUses := e.class, e.namespace, e.uses
		e.class, e.namespace, e.uses = d.proto, d.namespace, d.uses
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
		e.class, e.namespace, e.uses = prevClass, prevNS, prevUses
	}
	for _, d := range decls {
		prevClass, prevNS, prevUses := e.class, e.namespace, e.uses
		e.class, e.namespace, e.uses = d.proto, d.namespace, d.uses
		for _, member := range d.members {
			switch m := member.(type) {
			case *ast.EnumCaseDecl, *ast.ClassConstDecl:
			case *ast.MethodDecl:
				child := e.compilation.emitter(m.Name)
				child.class = d.proto
				child.namespace, child.uses = d.namespace, d.uses
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
								TypeName:   e.typeName(prm.Type),
								ClassID:    d.proto.ID,
								Attributes: e.attributes(prm.Attrs),
							}
							d.proto.Props = append(d.proto.Props, info)
						}
					}
				}
				child.params(m.Params)
				child.fn.ReturnType = child.typeName(m.ReturnType)
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
						TypeName:   e.typeName(m.Type),
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
				d.proto.Traits = append(d.proto.Traits, e.resolveClassNames(m.Traits)...)
			default:
				e.fail("membre de classe")
			}
		}
		e.class, e.namespace, e.uses = prevClass, prevNS, prevUses
	}
}

// anonymousClass déclare la classe d'une expression new class sous le nom interne de PHP : parent, sinon première
// interface, sinon class, suivi de @anonymous, d'un octet nul, du fichier, de la ligne et d'un compteur.
func (e *emitter) anonymousClass(c *ast.ClassDecl) *bc.ClassProto {
	flags := bc.ClassAnonymous
	if c.Modifiers&ast.Readonly != 0 {
		flags |= bc.ClassReadonly
	}
	if c.Modifiers&ast.Final != 0 {
		flags |= bc.ClassFinal
	}
	p := &bc.ClassProto{Flags: flags, ParentName: e.resolveClassName(c.Extends), Interfaces: e.resolveClassNames(c.Implements), Attributes: e.attributes(c.Attrs)}
	prefix := "class"
	if p.ParentName != "" {
		prefix = p.ParentName
	} else if len(p.Interfaces) > 0 {
		prefix = p.Interfaces[0]
	}
	p.Name = prefix + "@anonymous\x00" + e.file.Filename + ":" + strconv.Itoa(c.Location().Line) + "$" + strconv.FormatInt(int64(e.anonCount), 16)
	e.anonCount++
	p.ID = e.allocClassID()
	e.classes[strings.ToLower(p.Name)] = p
	e.prog.Classes = append(e.prog.Classes, p)
	if e.anonClasses == nil {
		e.anonClasses = make(map[*ast.ClassDecl]*bc.ClassProto)
	}
	e.anonClasses[c] = p
	return p
}

// forEachAnonymousClass parcourt un nœud de l'arbre syntaxique et visite, dans l'ordre du source, chaque classe
// d'une expression new class, y compris celles imbriquées dans le corps d'une autre classe anonyme.
func forEachAnonymousClass(v reflect.Value, visit func(*ast.ClassDecl)) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return
		}
		if v.CanInterface() {
			if n, ok := v.Interface().(*ast.NewExpr); ok && n.Anonymous != nil {
				visit(n.Anonymous)
			}
		}
		forEachAnonymousClass(v.Elem(), visit)
	case reflect.Struct:
		for _, f := range v.Fields() {
			forEachAnonymousClass(f, visit)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			forEachAnonymousClass(v.Index(i), visit)
		}
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
	if cv, dynamic := e.dynamicClassCV(expr); dynamic {
		return cv, bc.FlagDynamicClass
	}
	name := e.name(expr)
	switch strings.ToLower(name) {
	case "self":
		return 0, bc.FlagSelf
	case "parent":
		return 0, bc.FlagParent
	case "static":
		return 0, bc.FlagStatic
	}
	return e.classID(e.className(expr)), 0
}

// dynamicClassCV distingue une désignation de classe connue seulement à l'exécution d'un nom écrit dans le
// source. Une variable simple est lue dans sa variable compilée ; toute autre expression ($this->prop, $a[$i],
// appel) est évaluée à cet endroit, avant les arguments, dans une variable compilée temporaire.
func (e *emitter) dynamicClassCV(expr ast.Expr) (uint32, bool) {
	switch n := expr.(type) {
	case *ast.ConstFetchExpr, *ast.StringLit:
		return 0, false
	case *ast.VarExpr:
		if n.Dynamic == nil && n.Name != "" && n.Name != "this" {
			return e.cv(n.Name), true
		}
	}
	e.expr(expr)
	idx := e.temp()
	e.emit(bc.OpStoreCV, idx, 0, 0)
	return idx, true
}

// className rend le nom complet d'une classe désignée dans le source : un identifiant est résolu dans l'espace de noms courant, une chaîne littérale est déjà un nom complet.
func (e *emitter) className(expr ast.Expr) string {
	if n, ok := expr.(*ast.ConstFetchExpr); ok {
		return e.resolveClassName(n.Name)
	}
	return strings.TrimPrefix(e.name(expr), `\`)
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
