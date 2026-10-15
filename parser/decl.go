package parser

import (
	"code.hazyhaar.fr/devhoros/php127/ast"
	"code.hazyhaar.fr/devhoros/php127/lexer"
)

func (p *parser) attributes() []ast.Attribute {
	var attrs []ast.Attribute
	for p.eat(lexer.T_ATTRIBUTE) {
		for {
			a := ast.Attribute{Position: position(p.tok()), Name: p.name()}
			if p.id() == '(' {
				var first bool
				a.Args, first = p.arguments()
				if first {
					p.fatal(a.Position, "Cannot create Closure as attribute argument")
				}
			}
			attrs = append(attrs, a)
			if !p.eat(',') || p.id() == ']' {
				break
			}
		}
		p.expect(']')
	}
	return attrs
}
func modifier(id lexer.ID) ast.Modifiers {
	switch id {
	case lexer.T_PUBLIC, lexer.T_VAR:
		return ast.Public
	case lexer.T_PROTECTED:
		return ast.Protected
	case lexer.T_PRIVATE:
		return ast.Private
	case lexer.T_STATIC:
		return ast.Static
	case lexer.T_ABSTRACT:
		return ast.Abstract
	case lexer.T_FINAL:
		return ast.Final
	case lexer.T_READONLY:
		return ast.Readonly
	}
	return 0
}
func (p *parser) modifiers() ast.Modifiers {
	var mods ast.Modifiers
	for m := modifier(p.id()); m != 0; m = modifier(p.id()) {
		t := p.next()
		if m&(ast.Public|ast.Protected|ast.Private) != 0 && mods&(ast.Public|ast.Protected|ast.Private) != 0 {
			p.fatal(position(t), "Multiple access type modifiers are not allowed")
		}
		if mods&m != 0 {
			p.fatal(position(t), "Multiple "+lower(t.Text)+" modifiers are not allowed")
		}
		mods |= m
	}
	return mods
}
func (p *parser) typeRef() ast.TypeRef {
	start := p.tok()
	if p.eat('?') {
		result := p.typeAtom()
		result.Nullable = true
		result.Position = position(start)
		return result
	}
	result, grouped := p.typeElement()
	if len(result.Intersection) > 0 && !grouped {
		return result
	}
	if grouped && p.id() != '|' {
		p.unexpected(`"|"`)
	}
	if p.id() == '|' {
		items := []ast.TypeRef{result}
		for p.eat('|') {
			item, group := p.typeElement()
			if len(item.Intersection) > 0 && !group {
				p.unexpected("variable")
			}
			items = append(items, item)
		}
		result = ast.TypeRef{Position: position(start), Union: items}
	}
	return result
}
func (p *parser) typeAtom() ast.TypeRef {
	t := p.tok()
	if !nameToken(p.id()) && p.id() != lexer.T_ARRAY && p.id() != lexer.T_CALLABLE && p.id() != lexer.T_STATIC {
		p.unexpected("")
	}
	p.next()
	return ast.TypeRef{Position: position(t), Name: t.Text}
}
func (p *parser) typeElement() (ast.TypeRef, bool) {
	grouped := p.eat('(')
	result := p.typeAtom()
	if p.id() == lexer.T_AMPERSAND_NOT_FOLLOWED_BY_VAR_OR_VARARG {
		items := []ast.TypeRef{result}
		for p.eat(lexer.T_AMPERSAND_NOT_FOLLOWED_BY_VAR_OR_VARARG) {
			items = append(items, p.typeAtom())
		}
		result = ast.TypeRef{Position: result.Position, Intersection: items}
	}
	if grouped {
		if len(result.Intersection) == 0 {
			p.unexpected("amp")
		}
		p.expect(')')
	}
	return result, grouped
}
func (p *parser) params() []ast.Param {
	p.expect('(')
	var params []ast.Param
	for p.id() != ')' {
		param := ast.Param{Position: position(p.tok()), Attrs: p.attributes()}
		param.Modifiers = p.modifiers()
		if p.id() != lexer.T_VARIABLE && p.id() != lexer.T_ELLIPSIS && p.id() != lexer.T_AMPERSAND_FOLLOWED_BY_VAR_OR_VARARG {
			param.Type = p.typeRef()
		}
		param.ByRef = p.amp()
		param.Variadic = p.eat(lexer.T_ELLIPSIS)
		param.Name = p.expect(lexer.T_VARIABLE).Text[1:]
		if p.eat('=') {
			param.Default = p.expr(0)
		}
		for _, prior := range params {
			if prior.Name == param.Name {
				p.fatal(param.Position, "Redefinition of parameter $"+param.Name)
			}
		}
		params = append(params, param)
		if !p.eat(',') {
			break
		}
	}
	p.expect(')')
	return params
}
func (p *parser) returnType() ast.TypeRef {
	if p.eat(':') {
		return p.typeRef()
	}
	return ast.TypeRef{}
}
func (p *parser) functionBody() []ast.Stmt {
	p.functions++
	body := p.block()
	p.functions--
	return body
}
func (p *parser) functionDecl(attrs []ast.Attribute) ast.Stmt {
	t := p.expect(lexer.T_FUNCTION)
	byRef := p.amp()
	name := ""
	if p.id() == lexer.T_READONLY {
		name = p.next().Text
	} else {
		name = p.name()
	}
	s := &ast.FunctionDecl{StmtBase: sb(t), Attrs: attrs, ByRef: byRef, Name: name, Params: p.params()}
	s.ReturnType = p.returnType()
	s.Body = p.functionBody()
	return s
}
func (p *parser) closure(static bool, attrs []ast.Attribute) ast.Expr {
	t := p.tok()
	if t.ID != lexer.T_FUNCTION && t.ID != lexer.T_FN {
		p.unexpected("function")
	}
	p.next()
	byRef := p.amp()
	params := p.params()
	if t.ID == lexer.T_FN {
		result := p.returnType()
		p.expect(lexer.T_DOUBLE_ARROW)
		return &ast.ArrowFuncExpr{ExprBase: eb(t), Static: static, ByRef: byRef, Params: params, ReturnType: result, Expr: p.expr(0), Attrs: attrs}
	}
	c := &ast.ClosureExpr{ExprBase: eb(t), Static: static, ByRef: byRef, Params: params, Attrs: attrs}
	if p.eat(lexer.T_USE) {
		p.expect('(')
		for {
			u := ast.ClosureUse{Position: position(p.tok()), ByRef: p.amp(), Name: p.expect(lexer.T_VARIABLE).Text[1:]}
			c.Uses = append(c.Uses, u)
			if !p.eat(',') || p.id() == ')' {
				break
			}
		}
		p.expect(')')
	}
	c.ReturnType = p.returnType()
	c.Body = p.functionBody()
	return c
}
func (p *parser) constants(contextual bool) []ast.ConstItem {
	var items []ast.ConstItem
	for {
		item := ast.ConstItem{Position: position(p.tok())}
		if contextual {
			item.Name = p.identifier()
		} else {
			item.Name = p.name()
		}
		p.expect('=')
		item.Value = p.expr(0)
		items = append(items, item)
		if !p.eat(',') {
			break
		}
	}
	return items
}
func (p *parser) properties() []ast.PropertyItem {
	var items []ast.PropertyItem
	for {
		t := p.expect(lexer.T_VARIABLE)
		item := ast.PropertyItem{Position: position(t), Name: t.Text[1:]}
		if p.eat('=') {
			item.Default = p.expr(0)
		}
		items = append(items, item)
		if !p.eat(',') {
			break
		}
	}
	return items
}
func (p *parser) names() []string {
	var names []string
	for {
		names = append(names, p.name())
		if !p.eat(',') {
			break
		}
	}
	return names
}
func (p *parser) classDecl(attrs []ast.Attribute) ast.Stmt {
	start := p.tok()
	mods := p.modifiers()
	if mods != 0 && p.id() != lexer.T_CLASS {
		p.unexpected(`"abstract" or "final" or "readonly" or "class"`)
	}
	t := p.next()
	name := p.name()
	switch t.ID {
	case lexer.T_CLASS:
		c := &ast.ClassDecl{StmtBase: sb(start), Name: name, Modifiers: mods, Attrs: attrs}
		p.classTail(c)
		return c
	case lexer.T_INTERFACE:
		c := &ast.InterfaceDecl{StmtBase: sb(start), Name: name, Attrs: attrs}
		if p.eat(lexer.T_EXTENDS) {
			c.Extends = p.names()
		}
		c.Members = p.members()
		return c
	case lexer.T_TRAIT:
		return &ast.TraitDecl{StmtBase: sb(start), Name: name, Attrs: attrs, Members: p.members()}
	case lexer.T_ENUM:
		c := &ast.EnumDecl{StmtBase: sb(start), Name: name, Attrs: attrs}
		if p.eat(':') {
			typ := p.typeRef()
			c.ScalarType = typeName(typ)
		}
		if p.eat(lexer.T_IMPLEMENTS) {
			c.Implements = p.names()
		}
		c.Members = p.members()
		return c
	default:
		p.unexpected("class")
	}
	return nil
}
func (p *parser) classTail(c *ast.ClassDecl) {
	if p.eat(lexer.T_EXTENDS) {
		c.Extends = p.name()
	}
	if p.eat(lexer.T_IMPLEMENTS) {
		c.Implements = p.names()
	}
	c.Members = p.members()
}
func (p *parser) members() []ast.ClassMember {
	p.expect('{')
	var members []ast.ClassMember
	for p.id() != '}' {
		before := p.cursor
		attrs := p.attributes()
		start := p.tok()
		mods := p.modifiers()
		b := mb(start)
		switch {
		case p.eat(lexer.T_FUNCTION):
			m := &ast.MethodDecl{MemberBase: b, Attrs: attrs, Modifiers: mods, ByRef: p.amp(), Name: p.identifier(), Params: p.params()}
			m.ReturnType = p.returnType()
			if !p.eat(';') {
				m.HasBody = true
				m.Body = p.functionBody()
			}
			members = append(members, m)
		case p.eat(lexer.T_CONST):
			m := &ast.ClassConstDecl{MemberBase: b, Modifiers: mods, Attrs: attrs}
			if p.peek(1) != '=' {
				m.Type = p.typeRef()
			}
			m.Consts = p.constants(true)
			p.semi()
			members = append(members, m)
		case p.eat(lexer.T_CASE):
			m := &ast.EnumCaseDecl{MemberBase: b, Name: p.identifier(), Attrs: attrs}
			if p.eat('=') {
				m.Value = p.expr(0)
			}
			p.semi()
			members = append(members, m)
		case p.eat(lexer.T_USE):
			m := &ast.TraitUseDecl{MemberBase: b, Traits: p.names()}
			if p.eat('{') {
				for p.id() != '}' {
					a := ast.TraitAdaptation{Position: position(p.tok()), Method: p.identifier()}
					if p.eat(lexer.T_DOUBLE_COLON) {
						a.Trait = a.Method
						a.Method = p.identifier()
					}
					if p.eat(lexer.T_INSTEADOF) {
						a.InsteadOf = p.names()
					} else {
						p.expect(lexer.T_AS)
						a.Modifiers = p.modifiers()
						if p.id() != ';' {
							a.Alias = p.identifier()
						}
					}
					p.semi()
					m.Adaptations = append(m.Adaptations, a)
				}
				p.expect('}')
			} else {
				p.semi()
			}
			members = append(members, m)
		default:
			if mods == 0 {
				p.unexpected(`"function"`)
			}
			m := &ast.PropertyDecl{MemberBase: b, Modifiers: mods, Attrs: attrs}
			if p.id() != lexer.T_VARIABLE {
				m.Type = p.typeRef()
			}
			m.Props = p.properties()
			p.semi()
			members = append(members, m)
		}
		p.progress(before)
	}
	p.expect('}')
	return members
}
