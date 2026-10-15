package parser

import (
	"fmt"
	"slices"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/ast"
)

type classInfo struct {
	name, kind, parent string
	interfaces         []string
	members            []ast.ClassMember
	mods               ast.Modifiers
	pos                ast.Position
}
type validator struct {
	p           *parser
	classes     map[string]classInfo
	function    *functionScope
	labels      map[string][]int
	gotos       []*ast.GotoStmt
	paths       map[*ast.GotoStmt][]int
	loops       []int
	nextLoop    int
	namespace   string
	aliases     map[string]string
	functions   map[string]int
	conditional int
}
type functionScope struct {
	byRef     bool
	result    ast.TypeRef
	generator bool
	returns   []*ast.ReturnStmt
	pos       ast.Position
}

func (v *validator) functionBody(stmts []ast.Stmt, scope string, result ast.TypeRef, pos ast.Position, byRef bool) {
	old, labels, gotos := v.function, v.labels, v.gotos
	loops, paths := v.loops, v.paths
	v.loops = nil
	v.paths = nil
	v.function = &functionScope{result: result, pos: pos, byRef: byRef}
	v.labels = nil
	v.gotos = nil
	v.statements(stmts, scope, nil)
	v.resolveGotos()
	name := lower(result.Name)
	if v.function.generator && typeName(result) != "" && name != "generator" && name != "iterator" && name != "traversable" && name != "iterable" && name != "mixed" && name != "object" {
		v.fail(pos, "Generator return type must be a supertype of Generator, "+typeName(result)+" given")
	}
	for _, s := range v.function.returns {
		if name == "never" {
			v.fail(s.Position, "A never-returning function must not return")
		}
		if name == "void" && s.Expr != nil {
			v.fail(s.Position, "A void function must not return a value")
		}
		if name != "void" && typeName(result) != "" && s.Expr == nil && !v.function.generator {
			v.fail(s.Position, "A function with return type must return a value")
		}
	}
	v.function, v.labels, v.gotos = old, labels, gotos
	v.loops, v.paths = loops, paths
}
func (v *validator) resolveGotos() {
	for _, g := range v.gotos {
		target, exists := v.labels[g.Label]
		if !exists {
			v.fail(g.Position, "'goto' to undefined label '"+g.Label+"'")
		}
		origin := v.paths[g]
		if len(target) > len(origin) || !slices.Equal(target, origin[:min(len(origin), len(target))]) {
			v.fail(g.Position, "'goto' into loop or switch statement is disallowed")
		}
	}
}

func (v *validator) enterLoop() { v.nextLoop++; v.loops = append(v.loops, v.nextLoop) }
func (v *validator) loopBody(stmts []ast.Stmt, scope string, statics map[string]bool) {
	v.enterLoop()
	v.conditional++
	v.statements(stmts, scope, statics)
	v.conditional--
	v.loops = v.loops[:len(v.loops)-1]
}
func (v *validator) jump(level ast.Expr, pos ast.Position, op string) {
	n := int64(1)
	if level != nil {
		if value, ok := level.(*ast.IntLit); ok {
			n = value.Value
		} else {
			v.fail(pos, fmt.Sprintf("'%s' operator with non-integer operand is no longer supported", op))
		}
	}
	if n <= 0 {
		v.fail(pos, fmt.Sprintf("'%s' operator accepts only positive integers", op))
	}
	if len(v.loops) == 0 {
		v.fail(pos, fmt.Sprintf("'%s' not in the 'loop' or 'switch' context", op))
	}
	if n > int64(len(v.loops)) {
		v.fail(pos, fmt.Sprintf("Cannot '%s' %d levels", op, n))
	}
}
func (v *validator) qualify(name string) string {
	if absolute, ok := strings.CutPrefix(name, "\\"); ok {
		return absolute
	}
	if relative, ok := strings.CutPrefix(name, "namespace\\"); ok {
		name = relative
	} else {
		first, rest, found := strings.Cut(name, "\\")
		if alias, ok := v.aliases[lower(first)]; ok {
			if found {
				return alias + "\\" + rest
			}
			return alias
		}
	}
	if v.namespace != "" {
		return v.namespace + "\\" + name
	}
	return name
}

func typeName(t ast.TypeRef) string {
	if t.Name != "" {
		if t.Nullable {
			return "?" + t.Name
		}
		return t.Name
	}
	items, sep := t.Union, "|"
	if len(items) == 0 {
		items, sep = t.Intersection, "&"
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		name := typeName(item)
		if sep == "|" && len(item.Intersection) > 0 {
			name = "(" + name + ")"
		}
		names = append(names, name)
	}
	if sep == "|" {
		order := []string{"static", "callable", "object", "array", "string", "int", "float", "bool", "false", "true", "null"}
		slices.SortStableFunc(names, func(a, b string) int { return slices.Index(order, lower(a)) - slices.Index(order, lower(b)) })
	}
	return strings.Join(names, sep)
}
func (v *validator) fail(pos ast.Position, message string) { v.p.fatal(pos, message) }
func (v *validator) statements(stmts []ast.Stmt, scope string, statics map[string]bool) {
	if statics == nil {
		statics = make(map[string]bool)
	}
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ExprStmt:
			v.expr(s.Expr, false)
		case *ast.EchoStmt:
			for _, e := range s.Exprs {
				v.expr(e, false)
			}
		case *ast.ReturnStmt:
			if v.function != nil {
				v.function.returns = append(v.function.returns, s)
			}
			v.expr(s.Expr, false)
		case *ast.LabelStmt:
			if v.labels == nil {
				v.labels = make(map[string][]int)
			}
			if _, exists := v.labels[s.Label]; exists {
				v.fail(s.Position, "Label '"+s.Label+"' already defined")
			}
			v.labels[s.Label] = slices.Clone(v.loops)
		case *ast.GotoStmt:
			v.gotos = append(v.gotos, s)
			if v.paths == nil {
				v.paths = make(map[*ast.GotoStmt][]int)
			}
			v.paths[s] = slices.Clone(v.loops)
		case *ast.BreakStmt:
			v.jump(s.Level, s.Position, "break")
		case *ast.ContinueStmt:
			v.jump(s.Level, s.Position, "continue")
		case *ast.ThrowStmt:
			v.expr(s.Expr, false)
		case *ast.BlockStmt:
			v.statements(s.Body, scope, statics)
		case *ast.IfStmt:
			v.expr(s.Cond, false)
			v.conditional++
			v.statements(s.Body, scope, statics)
			for _, branch := range s.ElseIfs {
				v.expr(branch.Cond, false)
				v.statements(branch.Body, scope, statics)
			}
			v.statements(s.Else, scope, statics)
			v.conditional--
		case *ast.WhileStmt:
			v.expr(s.Cond, false)
			v.loopBody(s.Body, scope, statics)
		case *ast.DoWhileStmt:
			v.loopBody(s.Body, scope, statics)
			v.expr(s.Cond, false)
		case *ast.ForStmt:
			for _, list := range [][]ast.Expr{s.Init, s.Cond, s.Loop} {
				for _, e := range list {
					v.expr(e, false)
				}
			}
			v.loopBody(s.Body, scope, statics)
		case *ast.ForeachStmt:
			v.expr(s.Expr, false)
			if s.Key != nil {
				v.write(s.Key)
			}
			v.write(s.Val)
			v.loopBody(s.Body, scope, statics)
		case *ast.SwitchStmt:
			v.expr(s.Cond, false)
			v.enterLoop()
			v.conditional++
			for _, c := range s.Cases {
				v.expr(c.Cond, false)
				v.statements(c.Body, scope, statics)
			}
			v.loops = v.loops[:len(v.loops)-1]
			v.conditional--
		case *ast.TryStmt:
			v.conditional++
			v.statements(s.Body, scope, statics)
			for _, c := range s.Catches {
				v.statements(c.Body, scope, statics)
			}
			v.statements(s.Finally, scope, statics)
			v.conditional--
		case *ast.FunctionDecl:
			if v.function == nil && v.conditional == 0 {
				if v.functions == nil {
					v.functions = make(map[string]int)
				}
				name := lower(v.qualify(s.Name))
				if prior, ok := v.functions[name]; ok {
					v.fail(s.Position, fmt.Sprintf("Cannot redeclare %s() (previously declared in %s:%d)", v.qualify(s.Name), v.p.filename, prior))
				}
				v.functions[name] = s.Line
			}
			v.attributes(s.Attrs, "function", s.Position, scope)
			v.params(s.Params, "", s.Position)
			v.validateType(s.ReturnType, "return", s.Position)
			v.functionBody(s.Body, "", s.ReturnType, s.Position, s.ByRef)
		case *ast.ClassDecl:
			c := classInfo{name: s.Name, kind: "class", parent: s.Extends, interfaces: s.Implements, members: s.Members, mods: s.Modifiers, pos: s.Position}
			v.class(c, s.Attrs)
		case *ast.InterfaceDecl:
			c := classInfo{name: s.Name, kind: "interface", interfaces: s.Extends, members: s.Members, pos: s.Position}
			v.class(c, s.Attrs)
		case *ast.TraitDecl:
			c := classInfo{name: s.Name, kind: "trait", members: s.Members, pos: s.Position}
			v.class(c, s.Attrs)
		case *ast.EnumDecl:
			if s.ScalarType != "" && s.ScalarType != "int" && s.ScalarType != "string" {
				v.fail(s.Position, "Enum backing type must be int or string, "+s.ScalarType+" given")
			}
			for _, m := range s.Members {
				if c, ok := m.(*ast.EnumCaseDecl); ok {
					if s.ScalarType != "" && c.Value == nil {
						v.fail(c.Position, "Case "+c.Name+" of backed enum "+s.Name+" must have a value")
					}
					if s.ScalarType == "" && c.Value != nil {
						v.fail(c.Position, "Case "+c.Name+" of non-backed enum "+s.Name+" must not have a value")
					}
				}
			}
			c := classInfo{name: s.Name, kind: "enum", interfaces: s.Implements, members: s.Members, pos: s.Position}
			v.class(c, s.Attrs)
		case *ast.NamespaceDecl:
			namespace, aliases := v.namespace, v.aliases
			v.namespace = s.Name
			v.aliases = nil
			v.statements(s.Body, scope, statics)
			v.namespace, v.aliases = namespace, aliases
		case *ast.UseDecl:
			if v.aliases == nil {
				v.aliases = make(map[string]string)
			}
			for _, item := range s.Items {
				if item.Kind != "" {
					continue
				}
				alias := item.Alias
				if alias == "" {
					alias = item.Name[strings.LastIndex(item.Name, "\\")+1:]
				}
				v.aliases[lower(alias)] = strings.TrimPrefix(item.Name, "\\")
			}
		case *ast.ConstDecl:
			for _, c := range s.Consts {
				v.constant(c.Value, c.Position, false)
			}
		case *ast.GlobalStmt:
			for _, e := range s.Vars {
				v.write(e)
			}
		case *ast.StaticVarStmt:
			for _, item := range s.Vars {
				if statics[item.Name] {
					v.fail(item.Position, "Duplicate declaration of static variable $"+item.Name)
				}
				statics[item.Name] = true
				v.expr(item.Default, false)
			}
		case *ast.UnsetStmt:
			for _, e := range s.Vars {
				if prop, ok := e.(*ast.PropFetchExpr); ok {
					if _, temporary := prop.Object.(*ast.ClassConstFetchExpr); temporary {
						v.fail(e.Location(), "Cannot use temporary expression in write context")
					}
				}
				v.write(e)
			}
		case *ast.DeclareStmt:
			for _, c := range s.Directives {
				v.constant(c.Value, c.Position, false)
			}
			v.statements(s.Body, scope, statics)
		}
	}
}

func (v *validator) class(c classInfo, attrs []ast.Attribute) {
	if v.namespace != "" {
		c.name = v.namespace + "\\" + c.name
	}
	if c.parent != "" {
		c.parent = v.qualify(c.parent)
	}
	c.interfaces = slices.Clone(c.interfaces)
	for i, name := range c.interfaces {
		c.interfaces[i] = v.qualify(name)
	}
	if c.mods&(ast.Final|ast.Abstract) == ast.Final|ast.Abstract {
		v.fail(c.pos, "Cannot use the final modifier on an abstract class")
	}
	v.attributes(attrs, "class", c.pos, c.name)
	if c.mods&ast.Readonly != 0 {
		for _, a := range attrs {
			if lower(strings.TrimPrefix(a.Name, "\\")) == "allowdynamicproperties" {
				v.fail(c.pos, "Cannot apply #[AllowDynamicProperties] to readonly class "+c.name)
			}
		}
	}
	parent, known := v.classes[lower(c.parent)]
	deferredBinding := v.conditional > 0 || v.function != nil
	if deferredBinding {
		known = false
	}
	if known {
		if parent.mods&ast.Final != 0 {
			v.fail(c.pos, "Class "+c.name+" cannot extend final class "+parent.name)
		}
		if (c.mods&ast.Readonly != 0) != (parent.mods&ast.Readonly != 0) {
			if c.mods&ast.Readonly != 0 {
				v.fail(c.pos, "Readonly class "+c.name+" cannot extend non-readonly class "+parent.name)
			} else {
				v.fail(c.pos, "Non-readonly class "+c.name+" cannot extend readonly class "+parent.name)
			}
		}
	}
	prior, existed := v.classes[lower(c.name)]
	v.classes[lower(c.name)] = c
	if deferredBinding {
		defer func() {
			if existed {
				v.classes[lower(c.name)] = prior
			} else {
				delete(v.classes, lower(c.name))
			}
		}()
	}
	methods := make(map[string]bool)
	for _, member := range c.members {
		switch m := member.(type) {
		case *ast.MethodDecl:
			if m.Modifiers&ast.Abstract != 0 && m.HasBody {
				v.fail(m.Position, "Abstract function "+c.name+"::"+m.Name+"() cannot contain body")
			}
			if c.kind == "interface" && m.HasBody {
				v.fail(m.Position, "Interface function "+c.name+"::"+m.Name+"() cannot contain body")
			}
			if c.kind != "interface" && m.Modifiers&ast.Abstract == 0 && !m.HasBody {
				v.fail(m.Position, "Non-abstract method "+c.name+"::"+m.Name+"() must contain body")
			}
			if lower(m.Name) != "__construct" {
				for _, p := range m.Params {
					if p.Modifiers != 0 {
						v.fail(m.Position, "Cannot declare promoted property outside a constructor")
					}
				}
			}
			if m.Modifiers&ast.Readonly != 0 {
				v.fail(m.Position, "Cannot use the readonly modifier on a method")
			}
			method := lower(m.Name)
			if methods[method] {
				v.fail(m.Position, "Cannot redeclare "+c.name+"::"+m.Name+"()")
			}
			methods[method] = true
			v.attributes(m.Attrs, "method", m.Position, c.name)
			v.params(m.Params, c.name, m.Position)
			v.validateType(m.ReturnType, "return", m.Position)
			v.functionBody(m.Body, c.name, m.ReturnType, m.Position, m.ByRef)
			deferred := deferredBinding || c.kind == "trait" || c.kind == "enum"
			for _, member := range c.members {
				if _, ok := member.(*ast.TraitUseDecl); ok {
					deferred = true
				}
			}
			if !deferred {
				for _, a := range m.Attrs {
					if lower(strings.TrimPrefix(a.Name, "\\")) == "override" && !v.parentMethod(c, method, make(map[string]bool)) {
						// Une dependance inconnue reporte la liaison a l'execution, comme Zend.
						unresolved := c.parent != "" && !known
						for _, name := range c.interfaces {
							if _, ok := v.classes[lower(name)]; !ok {
								unresolved = true
							}
						}
						if !unresolved {
							v.fail(m.Position, c.name+"::"+m.Name+"() has #[\\Override] attribute, but no matching parent method exists")
						}
					}
				}
			}
		case *ast.PropertyDecl:
			if m.Modifiers&ast.Final != 0 {
				v.fail(m.Position, "Cannot use the final modifier on a property")
			}
			if m.Modifiers&ast.Abstract != 0 {
				v.fail(m.Position, "Properties cannot be declared abstract")
			}
			if c.kind == "enum" {
				v.fail(m.Position, "Enum "+c.name+" cannot include properties")
			}
			for _, item := range m.Props {
				if typ := lower(m.Type.Name); typ == "callable" || typ == "void" || typ == "never" {
					v.fail(m.Position, "Property "+c.name+"::$"+item.Name+" cannot have type "+typ)
				}
				readonly := (m.Modifiers|c.mods)&ast.Readonly != 0
				if readonly {
					label := c.name + "::$" + item.Name
					if m.Type.Name == "" && len(m.Type.Union) == 0 && len(m.Type.Intersection) == 0 {
						v.fail(m.Position, "Readonly property "+label+" must have type")
					}
					if m.Modifiers&ast.Static != 0 {
						v.fail(m.Position, "Static property "+label+" cannot be readonly")
					}
					if item.Default != nil {
						v.fail(m.Position, "Readonly property "+label+" cannot have default value")
					}
				}
				if known {
					for _, prior := range parent.members {
						if pm, ok := prior.(*ast.PropertyDecl); ok && pm.Modifiers&ast.Private == 0 {
							for _, pi := range pm.Props {
								if pi.Name == item.Name && readonly != ((pm.Modifiers|parent.mods)&ast.Readonly != 0) {
									before, after := "readonly", "non-readonly"
									if readonly {
										before, after = after, before
									}
									v.fail(c.pos, "Cannot redeclare "+before+" property "+parent.name+"::$"+item.Name+" as "+after+" "+c.name+"::$"+item.Name)
								}
							}
						}
					}
				}
				v.constant(item.Default, m.Position, false)
			}
			v.validateType(m.Type, "property", m.Position)
			v.attributes(m.Attrs, "property", m.Position, c.name)
		case *ast.ClassConstDecl:
			if m.Modifiers&ast.Static != 0 {
				v.fail(m.Position, "Cannot use the static modifier on a class constant")
			}
			if m.Modifiers&ast.Readonly != 0 {
				v.fail(m.Position, "Cannot use the readonly modifier on a class constant")
			}
			v.attributes(m.Attrs, "class constant", m.Position, c.name)
			for _, item := range m.Consts {
				if lower(item.Name) == "class" {
					v.fail(m.Position, "A class constant must not be called 'class'; it is reserved for class name fetching")
				}
				v.constant(item.Value, m.Position, false)
			}
		case *ast.EnumCaseDecl:
			if c.kind != "enum" {
				v.fail(m.Position, "Case can only be used in enums")
			}
			v.attributes(m.Attrs, "class constant", m.Position, c.name)
			v.constant(m.Value, m.Position, false)
		case *ast.TraitUseDecl:
			for _, a := range m.Adaptations {
				if a.Modifiers&ast.Readonly != 0 {
					v.fail(m.Position, "Cannot use the readonly modifier on a method")
				}
			}
		}
	}
}
func (v *validator) parentMethod(c classInfo, name string, seen map[string]bool) bool {
	if seen[lower(c.name)] {
		return false
	}
	seen[lower(c.name)] = true
	bases := append([]string{c.parent}, c.interfaces...)
	for _, base := range bases {
		if info, ok := v.classes[lower(base)]; ok {
			for _, member := range info.members {
				if method, ok := member.(*ast.MethodDecl); ok && lower(method.Name) == name && method.Modifiers&ast.Private == 0 && (name != "__construct" || !method.HasBody) {
					return true
				}
			}
			if v.parentMethod(info, name, seen) {
				return true
			}
		}
	}
	return false
}
func (v *validator) params(params []ast.Param, class string, pos ast.Position) {
	for i, p := range params {
		if p.Modifiers != 0 && class == "" {
			v.fail(pos, "Cannot declare promoted property outside a constructor")
		}
		if p.Variadic {
			if i != len(params)-1 {
				v.fail(pos, "Only the last parameter can be variadic")
			}
			if p.Default != nil {
				v.fail(pos, "Variadic parameter cannot have a default value")
			}
		}
		readonly := p.Modifiers&ast.Readonly != 0 || p.Modifiers&(ast.Public|ast.Protected|ast.Private) != 0 && v.classes[lower(class)].mods&ast.Readonly != 0
		if readonly && typeName(p.Type) == "" {
			v.fail(pos, "Readonly property "+class+"::$"+p.Name+" must have type")
		}
		v.attributes(p.Attrs, "parameter", p.Position, class)
		v.validateType(p.Type, "parameter", pos)
		v.constant(p.Default, pos, true)
		if p.Default != nil && p.Type.Name != "" {
			literalType := ""
			switch p.Default.(type) {
			case *ast.IntLit:
				literalType = "int"
			case *ast.FloatLit:
				literalType = "float"
			case *ast.StringLit:
				literalType = "string"
			case *ast.ArrayExpr:
				literalType = "array"
			}
			typ := lower(p.Type.Name)
			if literalType != "" && literalType != typ && typ != "mixed" && !(literalType == "int" && typ == "float") && !(literalType == "array" && typ == "iterable") {
				v.fail(pos, "Cannot use "+literalType+" as default value for parameter $"+p.Name+" of type "+typeName(p.Type))
			}
		}
	}
}
func (v *validator) validateType(t ast.TypeRef, context string, pos ast.Position) {
	name := lower(t.Name)
	if t.Nullable && name == "mixed" {
		v.fail(pos, "Type mixed cannot be marked as nullable since mixed already includes null")
	}
	seen := make(map[string]bool)
	if context != "return" && (lower(t.Name) == "void" || lower(t.Name) == "never") {
		v.fail(pos, lower(t.Name)+" cannot be used as a "+context+" type")
	}
	for _, item := range t.Union {
		n := lower(item.Name)
		if n != "" && seen[n] {
			v.fail(pos, "Duplicate type "+n+" is redundant")
		}
		seen[n] = true
		switch lower(item.Name) {
		case "void", "never", "mixed":
			name := lower(item.Name)
			name = strings.ToUpper(name[:1]) + name[1:]
			v.fail(pos, name+" can only be used as a standalone type")
		}
		v.validateType(item, context, pos)
	}
	if seen["bool"] && (seen["false"] || seen["true"]) {
		n := "false"
		if !seen[n] {
			n = "true"
		}
		v.fail(pos, "Duplicate type "+n+" is redundant")
	}
	for _, item := range t.Intersection {
		switch lower(item.Name) {
		case "int", "float", "string", "array", "object", "callable", "iterable", "void", "never", "mixed", "null", "false", "true", "bool", "static", "self", "parent":
			v.fail(pos, "Type "+item.Name+" cannot be part of an intersection type")
		}
		v.validateType(item, context, pos)
	}
}

func (v *validator) attributes(attrs []ast.Attribute, target string, pos ast.Position, scope string) {
	seen := make(map[string]bool)
	for _, a := range attrs {
		name := v.qualify(a.Name)
		canonical := lower(name)
		for _, arg := range a.Args {
			if arg.Unpack {
				v.fail(pos, "Cannot use unpacking in attribute argument list")
			}
			v.constant(arg.Value, pos, true)
		}
		internal := canonical == "attribute" || canonical == "override" || canonical == "allowdynamicproperties" || canonical == "returntypewillchange" || canonical == "sensitiveparameter"
		if !internal {
			continue
		}
		allowed := "class"
		if canonical == "override" || canonical == "returntypewillchange" {
			allowed = "method"
		}
		if canonical == "sensitiveparameter" {
			allowed = "parameter"
		}
		if target != allowed {
			v.fail(pos, fmt.Sprintf("Attribute %q cannot target %s (allowed targets: %s)", name, target, allowed))
		}
		if seen[canonical] {
			v.fail(pos, fmt.Sprintf("Attribute %q must not be repeated", name))
		}
		seen[canonical] = true
		if canonical == "attribute" && len(a.Args) > 0 {
			switch arg := a.Args[0].Value.(type) {
			case *ast.StringLit:
				v.fail(pos, "Attribute::__construct(): Argument #1 ($flags) must be of type int, string given")
			case *ast.IntLit:
				if arg.Value < 0 || arg.Value&^127 != 0 {
					v.fail(pos, "Invalid attribute flags specified")
				}
			case *ast.UnaryExpr:
				if n, ok := arg.Expr.(*ast.IntLit); ok && arg.Op == "-" && n.Value > 0 {
					v.fail(pos, "Invalid attribute flags specified")
				}
			case *ast.ClassConstFetchExpr:
				if class, ok := arg.Class.(*ast.ConstFetchExpr); ok {
					n := strings.TrimPrefix(class.Name, "\\")
					if lower(n) == "parent" || lower(n) == "self" {
						v.fail(pos, "Cannot access \""+lower(n)+"\" when no class scope is active")
					}
					if lower(n) != "attribute" {
						v.fail(pos, "Class \""+n+"\" not found")
					}
				}
			}
		}
	}
}

func (v *validator) constant(e ast.Expr, pos ast.Position, allowNew bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.StringLit, *ast.ConstFetchExpr, *ast.MagicConstExpr:
		return
	case *ast.UnaryExpr:
		v.constant(n.Expr, pos, allowNew)
	case *ast.BinaryExpr:
		v.constant(n.Left, pos, allowNew)
		v.constant(n.Right, pos, allowNew)
	case *ast.TernaryExpr:
		v.constant(n.Cond, pos, allowNew)
		v.constant(n.If, pos, allowNew)
		v.constant(n.Else, pos, allowNew)
	case *ast.ArrayExpr:
		for _, item := range n.Items {
			if item.ByRef {
				v.fail(pos, "Constant expression contains invalid operations")
			}
			v.constant(item.Key, pos, allowNew)
			v.constant(item.Value, pos, allowNew)
		}
	case *ast.DimFetchExpr:
		v.constant(n.Var, pos, allowNew)
		if n.Dim == nil {
			v.fail(pos, "Cannot use [] for reading")
		}
		v.constant(n.Dim, pos, allowNew)
	case *ast.ClassConstFetchExpr:
		if _, ok := n.Class.(*ast.ConstFetchExpr); !ok {
			v.fail(pos, "Dynamic class names are not allowed in compile-time class constant references")
		}
		v.constant(n.Const, pos, allowNew)
	case *ast.NewExpr:
		if !allowNew {
			v.fail(pos, "New expressions are not supported in this context")
		}
		if n.Anonymous != nil {
			v.fail(pos, "Cannot use anonymous class in constant expression")
		}
		for _, a := range n.Args {
			v.constant(a.Value, pos, allowNew)
		}
	default:
		v.fail(pos, "Constant expression contains invalid operations")
	}
}

func (v *validator) write(e ast.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.UnaryExpr:
		if n.Op != "@" {
			v.fail(n.Position, "Cannot use temporary expression in write context")
		}
		v.write(n.Expr)
	case *ast.VarExpr:
		if n.Name == "this" {
			v.fail(n.Position, "Cannot re-assign $this")
		}
		v.expr(n.Dynamic, false)
	case *ast.DimFetchExpr:
		v.write(n.Var)
		v.expr(n.Dim, false)
	case *ast.PropFetchExpr:
		if n.Nullsafe {
			v.fail(n.Position, "Can't use nullsafe operator in write context")
		}
		v.expr(n.Object, false)
		v.expr(n.Property, false)
	case *ast.StaticPropFetchExpr:
		v.expr(n.Class, false)
		v.expr(n.Property, false)
	case *ast.ArrayExpr:
		for _, item := range n.Items {
			v.expr(item.Key, false)
			v.write(item.Value)
		}
	default:
		v.fail(e.Location(), "Cannot use temporary expression in write context")
	}
}
func (v *validator) args(args []ast.Arg) {
	named, unpacked := false, false
	for _, a := range args {
		if a.Unpack {
			if named {
				v.fail(a.Position, "Cannot use argument unpacking after named arguments")
			}
			unpacked = true
		} else if a.Name != "" {
			named = true
		} else {
			if named {
				v.fail(a.Position, "Cannot use positional argument after named argument")
			}
			if unpacked {
				v.fail(a.Position, "Cannot use positional argument after argument unpacking")
			}
		}
		if d, ok := a.Value.(*ast.DimFetchExpr); ok && d.Dim == nil && !a.Unpack {
			v.write(d)
			continue
		}
		v.expr(a.Value, false)
	}
}
func (v *validator) expr(e ast.Expr, write bool) {
	if e == nil {
		return
	}
	if write {
		v.write(e)
		return
	}
	switch n := e.(type) {
	case *ast.VarExpr:
		v.expr(n.Dynamic, false)
	case *ast.AssignExpr:
		v.write(n.Left)
		if n.ByRef {
			switch n.Right.(type) {
			case *ast.VarExpr, *ast.CallExpr, *ast.MethodCallExpr, *ast.StaticCallExpr:
				v.expr(n.Right, false)
			default:
				v.write(n.Right)
			}
		} else {
			v.expr(n.Right, false)
		}
	case *ast.ListAssignExpr:
		v.list(n.Items, n.Position)
		for _, item := range n.Items {
			v.expr(item.Key, false)
			v.write(item.Value)
		}
		v.expr(n.Expr, false)
	case *ast.DimFetchExpr:
		if n.Dim == nil {
			v.fail(n.Position, "Cannot use [] for reading")
		}
		v.expr(n.Var, false)
		v.expr(n.Dim, false)
	case *ast.PropFetchExpr:
		v.expr(n.Object, false)
		v.expr(n.Property, false)
	case *ast.StaticPropFetchExpr:
		v.expr(n.Class, false)
		v.expr(n.Property, false)
	case *ast.ClassConstFetchExpr:
		v.expr(n.Class, false)
		v.expr(n.Const, false)
	case *ast.BinaryExpr:
		v.expr(n.Left, false)
		v.expr(n.Right, false)
	case *ast.UnaryExpr:
		v.expr(n.Expr, false)
	case *ast.IncDecExpr:
		v.write(n.Expr)
	case *ast.TernaryExpr:
		v.expr(n.Cond, false)
		v.expr(n.If, false)
		v.expr(n.Else, false)
	case *ast.MatchExpr:
		v.expr(n.Cond, false)
		defaults := 0
		for _, arm := range n.Arms {
			if arm.Default {
				defaults++
				if defaults > 1 {
					v.fail(arm.Position, "Match expressions may only contain one default arm")
				}
			}
			for _, cond := range arm.Conds {
				v.expr(cond, false)
			}
			v.expr(arm.Value, false)
		}
	case *ast.ArrayExpr:
		for _, item := range n.Items {
			if item.Value == nil {
				v.fail(item.Position, "Cannot use empty array elements in arrays")
			}
			v.expr(item.Key, false)
			v.expr(item.Value, false)
		}
	case *ast.EncapsedStringExpr:
		for _, part := range n.Parts {
			v.expr(part, false)
		}
	case *ast.CallExpr:
		v.expr(n.Func, false)
		v.args(n.Args)
	case *ast.MethodCallExpr:
		v.expr(n.Object, false)
		v.expr(n.Method, false)
		v.args(n.Args)
	case *ast.StaticCallExpr:
		v.expr(n.Class, false)
		v.expr(n.Method, false)
		v.args(n.Args)
	case *ast.NewExpr:
		v.expr(n.Class, false)
		v.args(n.Args)
		if n.Anonymous != nil {
			v.statements([]ast.Stmt{n.Anonymous}, "", nil)
		}
	case *ast.ClosureExpr:
		v.attributes(n.Attrs, "function", n.Position, "")
		v.params(n.Params, "", n.Position)
		v.functionBody(n.Body, "", n.ReturnType, n.Position, n.ByRef)
	case *ast.ArrowFuncExpr:
		v.attributes(n.Attrs, "function", n.Position, "")
		v.params(n.Params, "", n.Position)
		v.functionBody([]ast.Stmt{&ast.ReturnStmt{Position: n.Position, Expr: n.Expr}}, "", n.ReturnType, n.Position, n.ByRef)
	case *ast.YieldExpr:
		if v.function == nil {
			v.fail(n.Position, `The "yield" expression can only be used inside a function`)
		}
		v.function.generator = true
		v.expr(n.Key, false)
		v.expr(n.Value, false)
	case *ast.YieldFromExpr:
		if v.function == nil {
			v.fail(n.Position, `The "yield" expression can only be used inside a function`)
		}
		v.function.generator = true
		if v.function.byRef {
			v.fail(n.Position, `Cannot use "yield from" inside a by-reference generator`)
		}
		v.expr(n.Expr, false)
	case *ast.PrintExpr:
		v.expr(n.Expr, false)
	case *ast.IncludeExpr:
		v.expr(n.Expr, false)
	case *ast.EvalExpr:
		v.expr(n.Expr, false)
	case *ast.EmptyExpr:
		v.expr(n.Expr, false)
	case *ast.IssetExpr:
		for _, item := range n.Exprs {
			if !isVariable(item) {
				v.fail(n.Position, `Cannot use isset() on the result of an expression (you can use "null !== expression" instead)`)
			}
			v.expr(item, false)
		}
	case *ast.ExitExpr:
		v.expr(n.Expr, false)
	case *ast.ThrowExpr:
		v.expr(n.Expr, false)
	}
}

func (v *validator) list(items []ast.ArrayItem, pos ast.Position) {
	keyed, unkeyed, values := false, false, 0
	for _, item := range items {
		if item.Value == nil {
			continue
		}
		values++
		if item.Key != nil {
			keyed = true
		} else {
			unkeyed = true
		}
		if nested, ok := item.Value.(*ast.ArrayExpr); ok {
			v.list(nested.Items, nested.Position)
		}
	}
	if values == 0 {
		v.fail(pos, "Cannot use empty list")
	}
	if keyed && unkeyed {
		v.fail(pos, "Cannot mix keyed and unkeyed array entries in assignments")
	}
}
