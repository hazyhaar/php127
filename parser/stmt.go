package parser

import (
	"code.hazyhaar.fr/devhoros/php127/ast"
	"code.hazyhaar.fr/devhoros/php127/lexer"
)

func (p *parser) statement() ast.Stmt {
	t := p.tok()
	b := sb(t)
	switch t.ID {
	case ';':
		p.next()
		return &ast.NopStmt{StmtBase: b}
	case '{':
		return &ast.BlockStmt{StmtBase: b, Body: p.block()}
	case lexer.T_INLINE_HTML:
		p.next()
		return &ast.InlineHTMLStmt{StmtBase: b, Text: t.Text}
	case lexer.T_ECHO:
		p.next()
		if p.id() == ';' {
			p.unexpected("")
		}
		exprs := p.exprList(';')
		p.semi()
		return &ast.EchoStmt{StmtBase: b, Exprs: exprs}
	case lexer.T_RETURN:
		p.next()
		var e ast.Expr
		if p.id() != ';' {
			e = p.expr(0)
		}
		p.semi()
		return &ast.ReturnStmt{StmtBase: b, Expr: e}
	case lexer.T_THROW:
		p.next()
		e := p.expr(0)
		p.semi()
		return &ast.ThrowStmt{StmtBase: b, Expr: e}
	case lexer.T_BREAK, lexer.T_CONTINUE:
		p.next()
		var level ast.Expr
		if p.id() != ';' {
			level = p.expr(0)
		}
		p.semi()
		if t.ID == lexer.T_BREAK {
			return &ast.BreakStmt{StmtBase: b, Level: level}
		}
		return &ast.ContinueStmt{StmtBase: b, Level: level}
	case lexer.T_GOTO:
		p.next()
		label := p.name()
		p.semi()
		return &ast.GotoStmt{StmtBase: b, Label: label}
	case lexer.T_IF:
		p.next()
		s := &ast.IfStmt{StmtBase: b, Cond: p.parens()}
		alternate := p.eat(':')
		if alternate {
			s.Body = p.statements(lexer.T_ELSEIF, lexer.T_ELSE, lexer.T_ENDIF)
		} else {
			s.Body = p.body()
		}
		for p.id() == lexer.T_ELSEIF {
			et := p.next()
			e := ast.ElseIf{Position: position(et), Cond: p.parens()}
			if alternate {
				p.expect(':')
				e.Body = p.statements(lexer.T_ELSEIF, lexer.T_ELSE, lexer.T_ENDIF)
			} else {
				e.Body = p.body()
			}
			s.ElseIfs = append(s.ElseIfs, e)
		}
		if p.eat(lexer.T_ELSE) {
			if alternate {
				p.expect(':')
				s.Else = p.statements(lexer.T_ENDIF)
			} else {
				s.Else = p.body()
			}
		}
		if alternate {
			p.expect(lexer.T_ENDIF)
			p.semi()
		}
		return s
	case lexer.T_WHILE:
		p.next()
		s := &ast.WhileStmt{StmtBase: b, Cond: p.parens()}
		s.Body = p.loopBody(lexer.T_ENDWHILE)
		return s
	case lexer.T_DO:
		p.next()
		body := p.body()
		p.expect(lexer.T_WHILE)
		cond := p.parens()
		p.semi()
		return &ast.DoWhileStmt{StmtBase: b, Cond: cond, Body: body}
	case lexer.T_FOR:
		p.next()
		p.expect('(')
		s := &ast.ForStmt{StmtBase: b, Init: p.exprList(';')}
		p.semi()
		s.Cond = p.exprList(';')
		p.semi()
		s.Loop = p.exprList(')')
		p.expect(')')
		s.Body = p.loopBody(lexer.T_ENDFOR)
		return s
	case lexer.T_FOREACH:
		p.next()
		p.expect('(')
		s := &ast.ForeachStmt{StmtBase: b, Expr: p.expr(0)}
		p.expect(lexer.T_AS)
		s.ByRef = p.amp()
		s.Val = p.expr(0)
		if p.eat(lexer.T_DOUBLE_ARROW) {
			if s.ByRef {
				p.fatal(position(t), "Key element cannot be a reference")
			}
			s.Key = s.Val
			s.ByRef = p.amp()
			s.Val = p.expr(0)
		}
		p.expect(')')
		s.Body = p.loopBody(lexer.T_ENDFOREACH)
		return s
	case lexer.T_SWITCH:
		p.next()
		s := &ast.SwitchStmt{StmtBase: b, Cond: p.parens()}
		end := lexer.ID('}')
		if p.eat(':') {
			end = lexer.T_ENDSWITCH
		} else {
			p.expect('{')
		}
		p.eat(';')
		for p.id() != end {
			ct := p.tok()
			c := ast.SwitchCase{Position: position(ct)}
			if p.eat(lexer.T_CASE) {
				c.Cond = p.expr(0)
			} else {
				p.expect(lexer.T_DEFAULT)
			}
			if !p.eat(':') {
				p.expect(';')
			}
			c.Body = p.statements(lexer.T_CASE, lexer.T_DEFAULT, end)
			s.Cases = append(s.Cases, c)
		}
		p.next()
		if end == lexer.T_ENDSWITCH {
			p.semi()
		}
		return s
	case lexer.T_TRY:
		p.next()
		s := &ast.TryStmt{StmtBase: b, Body: p.block()}
		for p.id() == lexer.T_CATCH {
			ct := p.next()
			p.expect('(')
			c := ast.CatchClause{Position: position(ct)}
			for {
				c.Types = append(c.Types, p.name())
				if !p.eat('|') {
					break
				}
			}
			if p.id() == lexer.T_VARIABLE {
				c.Var = p.next().Text[1:]
			}
			p.expect(')')
			c.Body = p.block()
			s.Catches = append(s.Catches, c)
		}
		if p.eat(lexer.T_FINALLY) {
			s.Finally = p.block()
		} else if len(s.Catches) == 0 {
			p.fatal(position(t), "Cannot use try without catch or finally")
		}
		return s
	case lexer.T_GLOBAL:
		p.next()
		var vars []ast.Expr
		for {
			if p.id() != lexer.T_VARIABLE && p.id() != '$' {
				p.unexpected(`variable or "$"`)
			}
			vars = append(vars, p.prefix())
			if !p.eat(',') {
				break
			}
		}
		p.semi()
		return &ast.GlobalStmt{StmtBase: b, Vars: vars}
	case lexer.T_UNSET:
		p.next()
		p.expect('(')
		vars := []ast.Expr{p.expr(0)}
		for p.eat(',') {
			if p.id() == ')' {
				break
			}
			vars = append(vars, p.expr(0))
		}
		p.expect(')')
		p.semi()
		return &ast.UnsetStmt{StmtBase: b, Vars: vars}
	case lexer.T_STATIC:
		if p.peek(1) == lexer.T_VARIABLE {
			p.next()
			vars := p.properties()
			p.semi()
			return &ast.StaticVarStmt{StmtBase: b, Vars: vars}
		}
	case lexer.T_FUNCTION:
		offset := 1
		if p.peek(offset) == lexer.T_AMPERSAND_NOT_FOLLOWED_BY_VAR_OR_VARARG || p.peek(offset) == lexer.T_AMPERSAND_FOLLOWED_BY_VAR_OR_VARARG {
			offset++
		}
		if p.peek(offset) != '(' {
			return p.functionDecl(nil)
		}
	case lexer.T_CLASS, lexer.T_INTERFACE, lexer.T_TRAIT, lexer.T_ENUM, lexer.T_ABSTRACT, lexer.T_FINAL, lexer.T_READONLY:
		if t.ID != lexer.T_READONLY || p.peek(1) != '(' {
			return p.classDecl(nil)
		}
	case lexer.T_ATTRIBUTE:
		attrs := p.attributes()
		if p.id() == lexer.T_FUNCTION && p.peek(1) != '(' {
			return p.functionDecl(attrs)
		}
		if p.id() == lexer.T_CLASS || p.id() == lexer.T_INTERFACE || p.id() == lexer.T_TRAIT || p.id() == lexer.T_ENUM || p.id() == lexer.T_READONLY || p.id() == lexer.T_ABSTRACT || p.id() == lexer.T_FINAL {
			return p.classDecl(attrs)
		}
		static := p.eat(lexer.T_STATIC)
		e := p.closure(static, attrs)
		p.semi()
		return &ast.ExprStmt{StmtBase: b, Expr: e}
	case lexer.T_CONST:
		p.next()
		consts := p.constants(false)
		p.semi()
		return &ast.ConstDecl{StmtBase: b, Consts: consts}
	case lexer.T_NAMESPACE:
		p.next()
		s := &ast.NamespaceDecl{StmtBase: b}
		if p.id() != '{' {
			if identifierToken(p.id()) {
				s.Name = p.identifier()
			} else {
				s.Name = p.name()
			}
		}
		if p.id() == '{' {
			s.Bracketed = true
			s.Body = p.block()
		} else {
			p.semi()
			s.Body = p.statements(lexer.T_NAMESPACE, lexer.EOF)
		}
		return s
	case lexer.T_USE:
		return p.useDecl()
	case lexer.T_DECLARE:
		p.next()
		p.expect('(')
		s := &ast.DeclareStmt{StmtBase: b, Directives: p.constants(false)}
		p.expect(')')
		s.Body = p.loopBody(lexer.T_ENDDECLARE)
		return s
	case lexer.T_HALT_COMPILER:
		if p.functions > 0 {
			p.fatal(position(t), "__HALT_COMPILER() can only be used from the outermost scope")
		}
		p.next()
		p.expect('(')
		p.expect(')')
		last := p.expect(';')
		offset := last.Pos + len(last.Text)
		p.cursor = len(p.tokens) - 1
		return &ast.HaltCompilerStmt{StmtBase: b, Offset: offset, Data: p.src[offset:]}
	case lexer.T_STRING:
		if p.peek(1) == ':' {
			p.next()
			p.next()
			return &ast.LabelStmt{StmtBase: b, Label: t.Text}
		}
	}
	e := p.expr(0)
	p.semi()
	return &ast.ExprStmt{StmtBase: b, Expr: e}
}
func (p *parser) loopBody(end lexer.ID) []ast.Stmt {
	if p.eat(':') {
		body := p.statements(end)
		p.expect(end)
		p.semi()
		return body
	}
	return p.body()
}

func (p *parser) useDecl() ast.Stmt {
	t := p.expect(lexer.T_USE)
	s := &ast.UseDecl{StmtBase: sb(t)}
	kind := ""
	if p.id() == lexer.T_FUNCTION || p.id() == lexer.T_CONST {
		kind = lower(p.next().Text)
	}
	for {
		start := p.tok()
		name := p.name()
		if p.eat(lexer.T_NS_SEPARATOR) {
			name += "\\"
		}
		if p.eat('{') {
			for {
				item := ast.UseItem{Position: position(p.tok()), Kind: kind}
				if p.id() == lexer.T_FUNCTION || p.id() == lexer.T_CONST {
					item.Kind = lower(p.next().Text)
				}
				item.Name = name + p.name()
				if p.eat(lexer.T_AS) {
					item.Alias = p.name()
				}
				s.Items = append(s.Items, item)
				if !p.eat(',') || p.id() == '}' {
					break
				}
			}
			p.expect('}')
		} else {
			item := ast.UseItem{Position: position(start), Name: name, Kind: kind}
			if p.eat(lexer.T_AS) {
				item.Alias = p.name()
			}
			s.Items = append(s.Items, item)
		}
		if !p.eat(',') {
			break
		}
	}
	p.semi()
	return s
}
