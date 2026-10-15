package parser

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"code.hazyhaar.fr/devhoros/php127/ast"
	"code.hazyhaar.fr/devhoros/php127/lexer"
)

func binding(id lexer.ID) (int, bool, bool) {
	switch id {
	case lexer.T_LOGICAL_OR:
		return 10, false, false
	case lexer.T_LOGICAL_XOR:
		return 20, false, false
	case lexer.T_LOGICAL_AND:
		return 30, false, false
	case '=', lexer.T_PLUS_EQUAL, lexer.T_MINUS_EQUAL, lexer.T_MUL_EQUAL, lexer.T_DIV_EQUAL, lexer.T_CONCAT_EQUAL, lexer.T_MOD_EQUAL, lexer.T_AND_EQUAL, lexer.T_OR_EQUAL, lexer.T_XOR_EQUAL, lexer.T_SL_EQUAL, lexer.T_SR_EQUAL, lexer.T_POW_EQUAL, lexer.T_COALESCE_EQUAL:
		return 50, true, true
	case '?':
		return 60, false, false
	case lexer.T_COALESCE:
		return 70, true, false
	case lexer.T_BOOLEAN_OR:
		return 80, false, false
	case lexer.T_BOOLEAN_AND:
		return 90, false, false
	case '|':
		return 100, false, false
	case '^':
		return 110, false, false
	case lexer.T_AMPERSAND_FOLLOWED_BY_VAR_OR_VARARG, lexer.T_AMPERSAND_NOT_FOLLOWED_BY_VAR_OR_VARARG:
		return 120, false, false
	case lexer.T_IS_EQUAL, lexer.T_IS_NOT_EQUAL, lexer.T_IS_IDENTICAL, lexer.T_IS_NOT_IDENTICAL, lexer.T_SPACESHIP:
		return 130, false, false
	case '<', '>', lexer.T_IS_SMALLER_OR_EQUAL, lexer.T_IS_GREATER_OR_EQUAL:
		return 140, false, false
	case '.':
		return 150, false, false
	case lexer.T_SL, lexer.T_SR:
		return 160, false, false
	case '+', '-':
		return 170, false, false
	case '*', '/', '%':
		return 180, false, false
	case lexer.T_INSTANCEOF:
		return 200, false, false
	case lexer.T_POW:
		return 220, true, false
	}
	return 0, false, false
}

func (p *parser) expr(minimum int) ast.Expr {
	left := p.prefix()
	left = p.postfix(left, true)
	previous := 0
	for {
		precedence, right, assign := binding(p.id())
		if precedence == 0 || precedence < minimum {
			if assign && isVariable(left) {
				precedence = 50
			} else {
				break
			}
		}
		if (precedence == 130 || precedence == 140) && previous == precedence {
			p.unexpected("")
		}
		previous = precedence
		if assign {
			_, array := left.(*ast.ArrayExpr)
			if (!isVariable(left) && !array) || p.grouped[left] {
				p.unexpected("")
			}
		}
		t := p.next()
		if t.ID == '?' {
			var yes ast.Expr
			if p.id() != ':' {
				yes = p.expr(0)
			}
			p.expect(':')
			no := p.expr(precedence + 1)
			if prior, ok := left.(*ast.TernaryExpr); ok && !p.grouped[left] && (prior.If != nil || yes != nil) {
				first, second := "a ? b : c", "? d : e"
				if prior.If == nil {
					first, second = "a ?: b", "? c : d"
				} else if yes == nil {
					second = "?: d"
				}
				right := "a ? b : (c " + second + ")"
				if prior.If == nil {
					right = "a ?: (b " + second + ")"
				}
				p.fatal(prior.Cond.Location(), "Unparenthesized `"+first+" "+second+"` is not supported. Use either `("+first+") "+second+"` or `"+right+"`")
			}
			left = &ast.TernaryExpr{ExprBase: eb(t), Cond: left, If: yes, Else: no}
			continue
		}
		next := precedence + 1
		if right {
			next = precedence
		}
		byRef := assign && t.ID == '=' && p.amp()
		if byRef && p.id() != lexer.T_VARIABLE && p.id() != '$' && !nameToken(p.id()) && p.id() != lexer.T_STATIC {
			p.unexpected("")
		}
		if t.ID == lexer.T_INSTANCEOF && p.id() != '(' && p.id() != lexer.T_VARIABLE && p.id() != '$' && !nameToken(p.id()) && p.id() != lexer.T_STATIC {
			p.unexpected("")
		}
		rhs := p.expr(next)
		if assign {
			if a, ok := left.(*ast.ArrayExpr); ok && t.ID == '=' {
				left = &ast.ListAssignExpr{ExprBase: eb(t), Items: a.Items, Expr: rhs}
			} else {
				left = &ast.AssignExpr{ExprBase: eb(t), Op: t.Text, Left: left, Right: rhs, ByRef: byRef}
			}
		} else {
			left = &ast.BinaryExpr{ExprBase: eb(t), Op: lower(t.Text), Left: left, Right: rhs}
		}
	}
	return left
}

func (p *parser) prefix() ast.Expr {
	t := p.next()
	b := eb(t)
	switch t.ID {
	case lexer.T_LNUMBER, lexer.T_DNUMBER, lexer.T_NUM_STRING:
		raw := strings.ReplaceAll(t.Text, "_", "")
		if len(raw) > 1 && raw[0] == '0' && raw[1] >= '0' && raw[1] <= '9' && !strings.ContainsAny(raw, ".eE") && strings.ContainsAny(raw, "89") {
			panic(&SyntaxError{Message: "Invalid numeric literal", Filename: p.filename, Line: t.Line})
		}
		if t.ID == lexer.T_LNUMBER || t.ID == lexer.T_NUM_STRING {
			v, err := strconv.ParseInt(raw, 0, 64)
			if err == nil {
				return &ast.IntLit{ExprBase: b, Value: v, Raw: t.Text}
			}
		}
		var v float64
		base, digits := 0, raw
		if len(raw) > 1 && raw[0] == '0' {
			switch raw[1] {
			case 'x', 'X':
				base, digits = 16, raw[2:]
			case 'b', 'B':
				base, digits = 2, raw[2:]
			case 'o', 'O':
				base, digits = 8, raw[2:]
			default:
				if !strings.ContainsAny(raw, ".eE") {
					base, digits = 8, raw[1:]
				}
			}
		}
		if base != 0 {
			for _, c := range digits {
				d := strings.IndexRune("0123456789abcdef", c)
				if c >= 'A' && c <= 'F' {
					d = int(c-'A') + 10
				}
				v = v*float64(base) + float64(d)
			}
		} else {
			v, _ = strconv.ParseFloat(raw, 64)
		}
		return &ast.FloatLit{ExprBase: b, Value: v, Raw: t.Text}
	case lexer.T_CONSTANT_ENCAPSED_STRING:
		raw := t.Text
		if raw[0] == 'b' || raw[0] == 'B' {
			raw = raw[1:]
		}
		return &ast.StringLit{ExprBase: b, Value: p.decode(raw[1:len(raw)-1], raw[0], position(t)), Raw: t.Text, Quote: raw[0]}
	case lexer.T_VARIABLE:
		return &ast.VarExpr{ExprBase: b, Name: t.Text[1:]}
	case '$':
		var dynamic ast.Expr
		if p.eat('{') {
			dynamic = p.expr(0)
			p.expect('}')
		} else {
			dynamic = p.prefix()
		}
		return &ast.VarExpr{ExprBase: b, Dynamic: dynamic}
	case lexer.T_STRING, lexer.T_NAME_QUALIFIED, lexer.T_NAME_FULLY_QUALIFIED, lexer.T_NAME_RELATIVE, lexer.T_READONLY:
		return &ast.ConstFetchExpr{ExprBase: b, Name: t.Text}
	case lexer.T_STATIC:
		if p.id() == lexer.T_FUNCTION || p.id() == lexer.T_FN {
			return p.closure(true, nil)
		}
		return &ast.ConstFetchExpr{ExprBase: b, Name: t.Text}
	case lexer.T_LINE, lexer.T_FILE, lexer.T_DIR, lexer.T_CLASS_C, lexer.T_TRAIT_C, lexer.T_METHOD_C, lexer.T_FUNC_C, lexer.T_NS_C:
		return &ast.MagicConstExpr{ExprBase: b, Name: t.Text}
	case '(':
		e := p.expr(0)
		p.expect(')')
		p.grouped[e] = true
		return e
	case '[':
		return p.array(t, ']', false)
	case lexer.T_ARRAY, lexer.T_LIST:
		p.expect('(')
		return p.array(t, ')', t.ID == lexer.T_LIST)
	case '+', '-', '~', '@', lexer.T_INT_CAST, lexer.T_DOUBLE_CAST, lexer.T_STRING_CAST, lexer.T_BOOL_CAST, lexer.T_ARRAY_CAST, lexer.T_OBJECT_CAST, lexer.T_UNSET_CAST:
		return &ast.UnaryExpr{ExprBase: b, Op: lower(t.Text), Expr: p.expr(210)}
	case '!':
		return &ast.UnaryExpr{ExprBase: b, Op: t.Text, Expr: p.expr(190)}
	case lexer.T_CLONE:
		return &ast.UnaryExpr{ExprBase: b, Op: lower(t.Text), Expr: p.expr(230)}
	case lexer.T_INC, lexer.T_DEC:
		return &ast.IncDecExpr{ExprBase: b, Op: t.Text, Expr: p.expr(230)}
	case lexer.T_PRINT:
		return &ast.PrintExpr{ExprBase: b, Expr: p.expr(40)}
	case lexer.T_INCLUDE, lexer.T_INCLUDE_ONCE, lexer.T_REQUIRE, lexer.T_REQUIRE_ONCE:
		return &ast.IncludeExpr{ExprBase: b, Kind: lower(t.Text), Expr: p.expr(0)}
	case lexer.T_EVAL:
		return &ast.EvalExpr{ExprBase: b, Expr: p.parens()}
	case lexer.T_EMPTY:
		return &ast.EmptyExpr{ExprBase: b, Expr: p.parens()}
	case lexer.T_ISSET:
		p.expect('(')
		if p.id() == ')' {
			p.unexpected("")
		}
		args := p.exprList(')')
		p.expect(')')
		return &ast.IssetExpr{ExprBase: b, Exprs: args}
	case lexer.T_EXIT:
		var e ast.Expr
		if p.eat('(') {
			if p.id() != ')' {
				e = p.expr(0)
			}
			p.expect(')')
		}
		return &ast.ExitExpr{ExprBase: b, Expr: e}
	case lexer.T_THROW:
		return &ast.ThrowExpr{ExprBase: b, Expr: p.expr(0)}
	case lexer.T_YIELD_FROM:
		return &ast.YieldFromExpr{ExprBase: b, Expr: p.expr(45)}
	case lexer.T_YIELD:
		e := &ast.YieldExpr{ExprBase: b}
		if p.id() != ';' && p.id() != ')' && p.id() != ',' && p.id() != ']' {
			e.Value = p.expr(45)
			if p.eat(lexer.T_DOUBLE_ARROW) {
				e.Key = e.Value
				e.Value = p.expr(45)
			}
		}
		return e
	case lexer.T_MATCH:
		m := &ast.MatchExpr{ExprBase: b, Cond: p.parens()}
		p.expect('{')
		for p.id() != '}' {
			a := ast.MatchArm{Position: position(p.tok())}
			if p.eat(lexer.T_DEFAULT) {
				a.Default = true
				p.eat(',')
			} else {
				for {
					a.Conds = append(a.Conds, p.expr(0))
					if !p.eat(',') || p.id() == lexer.T_DOUBLE_ARROW {
						break
					}
				}
			}
			p.expect(lexer.T_DOUBLE_ARROW)
			a.Value = p.expr(0)
			m.Arms = append(m.Arms, a)
			if !p.eat(',') {
				break
			}
		}
		p.expect('}')
		return m
	case lexer.T_NEW:
		n := &ast.NewExpr{ExprBase: b}
		attrs := p.attributes()
		var mods ast.Modifiers
		if p.id() == lexer.T_READONLY || p.id() == lexer.T_ABSTRACT || p.id() == lexer.T_FINAL {
			mods = p.modifiers()
		}
		if p.eat(lexer.T_CLASS) {
			c := &ast.ClassDecl{StmtBase: sb(t), Attrs: attrs, Modifiers: mods}
			if p.id() == '(' {
				var first bool
				n.Args, first = p.arguments()
				if first {
					p.fatal(position(t), "Cannot create Closure for new expression")
				}
			}
			p.classTail(c)
			n.Anonymous = c
		} else {
			if p.id() != '(' && p.id() != lexer.T_VARIABLE && p.id() != '$' && !nameToken(p.id()) && p.id() != lexer.T_STATIC {
				p.unexpected(`"class"`)
			}
			n.Class = p.postfix(p.prefix(), false)
			if p.id() == '(' {
				var first bool
				n.Args, first = p.arguments()
				if first {
					p.fatal(position(t), "Cannot create Closure for new expression")
				}
			}
		}
		return n
	case lexer.T_FUNCTION, lexer.T_FN:
		p.cursor--
		return p.closure(false, nil)
	case lexer.T_ATTRIBUTE:
		p.cursor--
		attrs := p.attributes()
		static := p.eat(lexer.T_STATIC)
		return p.closure(static, attrs)
	case '"', '`', lexer.T_START_HEREDOC:
		return p.encapsed(t)
	default:
		if t.ID == '/' && p.id() == lexer.EOF {
			p.cursor--
			p.unexpected("end of file")
		}
		if t.ID != lexer.EOF {
			p.cursor--
		}
		p.unexpected("")
	}
	return nil
}

func (p *parser) postfix(left ast.Expr, calls bool) ast.Expr {
	for {
		t := p.tok()
		b := eb(t)
		if !p.grouped[left] {
			switch left.(type) {
			case *ast.NewExpr, *ast.IntLit, *ast.FloatLit:
				if t.ID == '(' || t.ID == '[' || t.ID == lexer.T_OBJECT_OPERATOR || t.ID == lexer.T_NULLSAFE_OBJECT_OPERATOR || t.ID == lexer.T_DOUBLE_COLON {
					p.unexpected("")
				}
			}
		}
		switch t.ID {
		case '(':
			if !calls {
				return left
			}
			args, first := p.arguments()
			switch e := left.(type) {
			case *ast.PropFetchExpr:
				if first && e.Nullsafe {
					p.fatal(e.Position, "Cannot combine nullsafe operator with Closure creation")
				}
				left = &ast.MethodCallExpr{ExprBase: b, Object: e.Object, Method: e.Property, Nullsafe: e.Nullsafe, Args: args, FirstClass: first}
			case *ast.ClassConstFetchExpr:
				left = &ast.StaticCallExpr{ExprBase: b, Class: e.Class, Method: e.Const, Args: args, FirstClass: first}
			case *ast.StaticPropFetchExpr:
				left = &ast.StaticCallExpr{ExprBase: b, Class: e.Class, Method: e.Property, Args: args, FirstClass: first}
			default:
				left = &ast.CallExpr{ExprBase: b, Func: left, Args: args, FirstClass: first}
			}
		case '[':
			p.next()
			var dim ast.Expr
			if p.id() != ']' {
				dim = p.expr(0)
			}
			p.expect(']')
			left = &ast.DimFetchExpr{ExprBase: b, Var: left, Dim: dim}
		case lexer.T_OBJECT_OPERATOR, lexer.T_NULLSAFE_OBJECT_OPERATOR, lexer.T_DOUBLE_COLON:
			p.next()
			var property ast.Expr
			variable := p.id() == lexer.T_VARIABLE || p.id() == '$'
			if p.eat('{') {
				property = p.expr(0)
				p.expect('}')
			} else if variable {
				property = p.prefix()
			} else {
				pt := p.tok()
				if !identifierToken(p.id()) {
					if t.ID == lexer.T_DOUBLE_COLON {
						p.unexpected("")
					}
					p.unexpected(`identifier or variable or "{" or "$"`)
				}
				property = &ast.ConstFetchExpr{ExprBase: eb(pt), Name: p.identifier()}
			}
			if t.ID == lexer.T_DOUBLE_COLON {
				if variable {
					left = &ast.StaticPropFetchExpr{ExprBase: b, Class: left, Property: property}
				} else {
					left = &ast.ClassConstFetchExpr{ExprBase: b, Class: left, Const: property}
				}
			} else {
				left = &ast.PropFetchExpr{ExprBase: b, Object: left, Property: property, Nullsafe: t.ID == lexer.T_NULLSAFE_OBJECT_OPERATOR}
			}
		case lexer.T_INC, lexer.T_DEC:
			if !calls {
				return left
			}
			p.next()
			left = &ast.IncDecExpr{ExprBase: b, Op: t.Text, Expr: left, Post: true}
		default:
			return left
		}
	}
}

func isVariable(e ast.Expr) bool {
	switch e.(type) {
	case *ast.VarExpr, *ast.DimFetchExpr, *ast.PropFetchExpr, *ast.StaticPropFetchExpr, *ast.CallExpr, *ast.MethodCallExpr, *ast.StaticCallExpr:
		return true
	}
	return false
}
func (p *parser) arguments() ([]ast.Arg, bool) {
	p.expect('(')
	var args []ast.Arg
	if p.id() == lexer.T_ELLIPSIS && p.peek(1) == ')' {
		p.next()
		p.next()
		return nil, true
	}
	for p.id() != ')' {
		a := ast.Arg{Position: position(p.tok())}
		a.Unpack = p.eat(lexer.T_ELLIPSIS)
		if !a.Unpack && identifierToken(p.id()) && p.peek(1) == ':' {
			a.Name = p.next().Text
			p.next()
		}
		a.Value = p.expr(0)
		args = append(args, a)
		if !p.eat(',') {
			break
		}
	}
	p.expect(')')
	return args, false
}
func (p *parser) array(t lexer.Token, end lexer.ID, list bool) ast.Expr {
	a := &ast.ArrayExpr{ExprBase: eb(t), List: list}
	for p.id() != end {
		item := ast.ArrayItem{Position: position(p.tok())}
		if p.eat(',') {
			a.Items = append(a.Items, item)
			continue
		}
		item.Unpack = p.eat(lexer.T_ELLIPSIS)
		item.ByRef = p.amp()
		item.Value = p.expr(0)
		if p.eat(lexer.T_DOUBLE_ARROW) {
			item.Key = item.Value
			item.ByRef = p.amp()
			item.Value = p.expr(0)
		}
		a.Items = append(a.Items, item)
		if !p.eat(',') {
			break
		}
	}
	p.expect(end)
	return a
}

func (p *parser) decode(raw string, quote byte, pos ast.Position) string {
	var result strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '\\' || i+1 == len(raw) {
			result.WriteByte(c)
			continue
		}
		next := raw[i+1]
		if quote == '\'' {
			if next == '\'' || next == '\\' {
				i++
				result.WriteByte(next)
			} else {
				result.WriteByte(c)
			}
			continue
		}
		i++
		switch next {
		case 'n':
			result.WriteByte('\n')
		case 'r':
			result.WriteByte('\r')
		case 't':
			result.WriteByte('\t')
		case 'v':
			result.WriteByte('\v')
		case 'e':
			result.WriteByte(27)
		case 'f':
			result.WriteByte('\f')
		case '\\', '$':
			result.WriteByte(next)
		case '"', '`':
			if next == quote {
				result.WriteByte(next)
			} else {
				result.WriteByte('\\')
				result.WriteByte(next)
			}
		case 'x':
			start := i + 1
			end := start
			for end < len(raw) && end < start+2 && strings.ContainsRune("0123456789abcdefABCDEF", rune(raw[end])) {
				end++
			}
			if end == start {
				result.WriteString("\\x")
			} else {
				v, _ := strconv.ParseUint(raw[start:end], 16, 8)
				result.WriteByte(byte(v))
				i = end - 1
			}
		case 'u':
			if i+1 < len(raw) && raw[i+1] == '{' {
				end := strings.IndexByte(raw[i+2:], '}')
				if end < 0 {
					panic(&SyntaxError{Message: "Invalid UTF-8 codepoint escape sequence", Filename: p.filename, Line: pos.Line})
				}
				end += i + 2
				v, err := strconv.ParseUint(raw[i+2:end], 16, 32)
				if v > utf8.MaxRune {
					panic(&SyntaxError{Message: "Invalid UTF-8 codepoint escape sequence: Codepoint too large", Filename: p.filename, Line: pos.Line})
				}
				if err != nil {
					panic(&SyntaxError{Message: "Invalid UTF-8 codepoint escape sequence", Filename: p.filename, Line: pos.Line})
				}
				if v >= 0xd800 && v <= 0xdfff {
					// PHP conserve aussi les points de code reserves aux substituts UTF-16.
					result.WriteByte(0xe0 | byte(v>>12))
					result.WriteByte(0x80 | byte(v>>6)&0x3f)
					result.WriteByte(0x80 | byte(v)&0x3f)
				} else {
					result.WriteRune(rune(v))
				}
				i = end
			} else {
				result.WriteString("\\u")
			}
		default:
			if next >= '0' && next <= '7' {
				start := i
				for i+1 < len(raw) && i < start+2 && raw[i+1] >= '0' && raw[i+1] <= '7' {
					i++
				}
				v, _ := strconv.ParseUint(raw[start:i+1], 8, 16)
				result.WriteByte(byte(v))
			} else {
				result.WriteByte('\\')
				result.WriteByte(next)
			}
		}
	}
	return result.String()
}

func (p *parser) encapsed(start lexer.Token) ast.Expr {
	end := start.ID
	kind := "double"
	quote := byte('"')
	nowdoc := false
	if start.ID == lexer.T_START_HEREDOC {
		end = lexer.T_END_HEREDOC
		kind = "heredoc"
		quote = 0
		nowdoc = strings.Contains(start.Text, "'")
		if nowdoc {
			kind = "nowdoc"
		}
	}
	if start.ID == '`' {
		kind = "backtick"
		quote = '`'
	}
	e := &ast.EncapsedStringExpr{ExprBase: eb(start), Kind: kind}
	indent := ""
	if end == lexer.T_END_HEREDOC {
		depth := 1
		for i := p.cursor; i < len(p.tokens); i++ {
			t := p.tokens[i]
			if t.ID == lexer.T_START_HEREDOC {
				depth++
			}
			if t.ID == lexer.T_END_HEREDOC {
				depth--
				if depth == 0 {
					indent = t.Text[:len(t.Text)-len(strings.TrimLeft(t.Text, " \t"))]
					break
				}
			}
		}
		if strings.Contains(indent, " ") && strings.Contains(indent, "\t") {
			panic(&SyntaxError{Message: "Invalid indentation - tabs and spaces cannot be mixed", Filename: p.filename, Line: start.Line + 1})
		}
		if len(indent) > 0 && (p.id() == lexer.T_VARIABLE || p.id() == lexer.T_CURLY_OPEN || p.id() == lexer.T_DOLLAR_OPEN_CURLY_BRACES) {
			panic(&SyntaxError{Message: fmt.Sprintf("Invalid body indentation level (expecting an indentation level of at least %d)", len(indent)), Filename: p.filename, Line: 0})
		}
	}
	for p.id() != end {
		before := p.cursor
		t := p.tok()
		switch t.ID {
		case lexer.T_ENCAPSED_AND_WHITESPACE:
			p.next()
			value := t.Text
			if indent != "" {
				value = p.stripIndent(t, indent)
			}
			if !nowdoc {
				value = p.decode(value, quote, position(t))
			}
			e.Parts = append(e.Parts, &ast.StringLit{ExprBase: eb(t), Raw: t.Text, Value: value, Quote: quote})
		case lexer.T_CURLY_OPEN:
			p.next()
			e.Parts = append(e.Parts, p.expr(0))
			p.expect('}')
		case lexer.T_DOLLAR_OPEN_CURLY_BRACES:
			p.next()
			var v ast.Expr
			if p.id() == lexer.T_STRING_VARNAME {
				nt := p.next()
				v = p.postfix(&ast.VarExpr{ExprBase: eb(nt), Name: nt.Text}, true)
			} else {
				v = &ast.VarExpr{ExprBase: eb(t), Dynamic: p.expr(0)}
			}
			p.expect('}')
			e.Parts = append(e.Parts, v)
		case lexer.T_VARIABLE:
			p.next()
			var v ast.Expr = &ast.VarExpr{ExprBase: eb(t), Name: t.Text[1:]}
			if p.eat('[') {
				nt := p.tok()
				var dim ast.Expr
				if p.id() == lexer.T_STRING {
					p.next()
					dim = &ast.StringLit{ExprBase: eb(nt), Raw: nt.Text, Value: nt.Text}
				} else if p.id() == lexer.T_NUM_STRING {
					p.next()
					n, _ := strconv.ParseInt(nt.Text, 10, 64)
					dim = &ast.IntLit{ExprBase: eb(nt), Raw: nt.Text, Value: n}
				} else {
					if p.id() != '-' && p.id() != lexer.T_VARIABLE {
						p.unexpected(`"-" or identifier or variable or number`)
					}
					dim = p.expr(0)
				}
				p.expect(']')
				v = &ast.DimFetchExpr{ExprBase: eb(t), Var: v, Dim: dim}
			} else if p.eat(lexer.T_OBJECT_OPERATOR) {
				nt := p.tok()
				v = &ast.PropFetchExpr{ExprBase: eb(t), Object: v, Property: &ast.ConstFetchExpr{ExprBase: eb(nt), Name: p.identifier()}}
			}
			e.Parts = append(e.Parts, v)
		default:
			if end == lexer.T_END_HEREDOC && len(e.Parts) > 0 {
				p.unexpected(`variable or heredoc end or "${" or "{$"`)
			}
			p.unexpected("")
		}
		p.progress(before)
	}
	last := p.expect(end)
	e.Raw = p.src[start.Pos : last.Pos+len(last.Text)]
	if kind != "backtick" && len(e.Parts) == 0 {
		return &ast.StringLit{ExprBase: eb(start), Raw: e.Raw, Quote: quote}
	}
	if end == lexer.T_END_HEREDOC && len(e.Parts) > 0 {
		if tail, ok := e.Parts[len(e.Parts)-1].(*ast.StringLit); ok {
			tail.Value = strings.TrimSuffix(strings.TrimSuffix(tail.Value, "\n"), "\r")
		}
	}
	if kind != "backtick" && len(e.Parts) == 1 {
		if lit, ok := e.Parts[0].(*ast.StringLit); ok {
			lit.ExprBase = eb(start)
			lit.Raw = e.Raw
			return lit
		}
	}
	return e
}

func (p *parser) stripIndent(t lexer.Token, indent string) string {
	var out strings.Builder
	raw := t.Text
	line := t.Line
	startLine := t.Pos == 0 || p.src[t.Pos-1] == '\n' || p.src[t.Pos-1] == '\r'
	for i := 0; i <= len(raw); {
		if startLine {
			for skipped := 0; skipped < len(indent); skipped++ {
				if i < len(raw) && (raw[i] == '\r' || raw[i] == '\n') {
					break
				}
				if i == len(raw) || raw[i] != ' ' && raw[i] != '\t' {
					panic(&SyntaxError{Message: fmt.Sprintf("Invalid body indentation level (expecting an indentation level of at least %d)", len(indent)), Filename: p.filename, Line: line})
				}
				if raw[i] != indent[0] {
					panic(&SyntaxError{Message: "Invalid indentation - tabs and spaces cannot be mixed", Filename: p.filename, Line: line})
				}
				i++
			}
		}
		if i == len(raw) {
			break
		}
		startLine = false
		for i < len(raw) {
			c := raw[i]
			out.WriteByte(c)
			i++
			if c == '\r' || c == '\n' {
				if c == '\r' && i < len(raw) && raw[i] == '\n' {
					out.WriteByte(raw[i])
					i++
				}
				line++
				startLine = true
				break
			}
		}
		if i == len(raw) && p.id() == lexer.T_END_HEREDOC {
			break
		}
	}
	return out.String()
}
