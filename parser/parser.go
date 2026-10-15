// Package parser construit un arbre PHP a partir du flux lexical.
package parser

import (
	"fmt"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/ast"
	"code.hazyhaar.fr/devhoros/php127/lexer"
)

type SyntaxError struct {
	Message, Filename string
	Line              int
}

func (e *SyntaxError) Category() string { return "Parse error" }
func (e *SyntaxError) FormatCLI() string {
	return fmt.Sprintf("Parse error: %s in %s on line %d", e.Message, e.Filename, e.Line)
}
func (e *SyntaxError) Error() string { return e.FormatCLI() }

type CompileError struct {
	Message, Filename string
	Line              int
}

func (e *CompileError) Category() string { return "Fatal error" }
func (e *CompileError) FormatCLI() string {
	return fmt.Sprintf("Fatal error: %s in %s on line %d", e.Message, e.Filename, e.Line)
}
func (e *CompileError) Error() string { return e.FormatCLI() }

type parser struct {
	tokens        []lexer.Token
	cursor        int
	filename, src string
	functions     int
	grouped       map[ast.Expr]bool
}

func Parse(src string, filename string) (file *ast.File, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			switch e := failure.(type) {
			case *SyntaxError:
				file, err = nil, e
			case *CompileError:
				file, err = nil, e
			default:
				panic(failure)
			}
		}
	}()
	p := newParser(src, filename)
	file = &ast.File{Line: 1, Filename: filename}
	file.Stmts = p.statements(lexer.EOF)
	for i, stmt := range file.Stmts {
		if d, ok := stmt.(*ast.DeclareStmt); ok {
			for _, c := range d.Directives {
				if lower(c.Name) == "strict_types" {
					value, ok := c.Value.(*ast.IntLit)
					if !ok {
						p.fatal(d.Position, "declare(strict_types) value must be a literal")
					}
					if value.Value != 0 && value.Value != 1 {
						p.fatal(d.Position, "strict_types declaration must have 0 or 1 as its value")
					}
					if i != 0 {
						p.fatal(d.Position, "strict_types declaration must be the very first statement in the script")
					}
				}
			}
		}
	}
	v := validator{p: p, classes: make(map[string]classInfo)}
	v.statements(file.Stmts, "", nil)
	v.resolveGotos()
	for _, s := range file.Stmts {
		if d, ok := s.(*ast.DeclareStmt); ok {
			for _, c := range d.Directives {
				if c.Name == "strict_types" {
					if v, ok := c.Value.(*ast.IntLit); ok {
						file.StrictTypes = v.Value == 1
					}
				}
			}
		}
	}
	return file, nil
}
func CheckSyntax(src string, filename string) error { _, err := Parse(src, filename); return err }
func newParser(src, filename string) *parser {
	p := &parser{filename: filename, src: src, grouped: make(map[ast.Expr]bool)}
	l := lexer.New(src)
	for {
		tok := l.Next()
		switch tok.ID {
		case lexer.T_COMMENT, lexer.T_DOC_COMMENT:
			if strings.HasPrefix(tok.Text, "/*") && (len(tok.Text) < 4 || !strings.HasSuffix(tok.Text, "*/")) {
				panic(&SyntaxError{
					Message:  fmt.Sprintf("Unterminated comment starting line %d", tok.Line),
					Filename: filename,
					Line:     tok.Line,
				})
			}
			continue
		case lexer.T_WHITESPACE, lexer.T_OPEN_TAG:
			continue
		case lexer.T_CLOSE_TAG:
			tok.ID = ';'
		case lexer.T_OPEN_TAG_WITH_ECHO:
			tok.ID = lexer.T_ECHO
		}
		p.tokens = append(p.tokens, tok)
		if tok.ID == lexer.EOF {
			break
		}
	}
	return p
}
func position(t lexer.Token) ast.Position { return ast.Position{Line: t.Line, Pos: t.Pos} }
func sb(t lexer.Token) ast.StmtBase       { return ast.StmtBase{Position: position(t)} }
func eb(t lexer.Token) ast.ExprBase       { return ast.ExprBase{Position: position(t)} }
func mb(t lexer.Token) ast.MemberBase     { return ast.MemberBase{Position: position(t)} }
func (p *parser) tok() lexer.Token        { return p.tokens[p.cursor] }
func (p *parser) id() lexer.ID            { return p.tok().ID }
func (p *parser) peek(n int) lexer.ID     { return p.tokens[min(p.cursor+n, len(p.tokens)-1)].ID }
func (p *parser) next() lexer.Token {
	t := p.tok()
	if t.ID != lexer.EOF {
		before := p.cursor
		p.cursor++
		p.progress(before)
	}
	return t
}
func (p *parser) eat(id lexer.ID) bool {
	if p.id() == id {
		p.next()
		return true
	}
	return false
}
func (p *parser) expect(id lexer.ID) lexer.Token {
	if p.id() != id {
		p.unexpected(fmt.Sprintf("%q", id.String()))
	}
	return p.next()
}
func (p *parser) unexpected(expected string) {
	t := p.tok()
	text := fmt.Sprintf("token %q", t.Text)
	switch t.ID {
	case lexer.EOF:
		var openings []lexer.Token
		for _, tok := range p.tokens[:p.cursor] {
			switch tok.ID {
			case '{', '(', '[':
				openings = append(openings, tok)
			case lexer.T_ATTRIBUTE:
				tok.ID = '['
				openings = append(openings, tok)
			case lexer.T_CURLY_OPEN, lexer.T_DOLLAR_OPEN_CURLY_BRACES:
				tok.ID = '{'
				openings = append(openings, tok)
			case '}', ')', ']':
				if len(openings) > 0 {
					openings = openings[:len(openings)-1]
				}
			}
		}
		if len(openings) > 0 {
			op := openings[len(openings)-1]
			msg := fmt.Sprintf("Unclosed '%s'", op.ID)
			if op.Line != t.Line {
				msg += fmt.Sprintf(" on line %d", op.Line)
			}
			panic(&SyntaxError{Message: msg, Filename: p.filename, Line: t.Line})
		}
		text = "end of file"
	case lexer.T_STRING:
		text = fmt.Sprintf("identifier %q", t.Text)
	case lexer.T_VARIABLE:
		text = fmt.Sprintf("variable %q", t.Text)
	case lexer.T_LNUMBER:
		text = fmt.Sprintf("integer %q", t.Text)
	case lexer.T_START_HEREDOC:
		t.Line += strings.Count(t.Text, "\n")
		text = "heredoc start \"" + strings.TrimRight(t.Text, "\r\n\"") + "\""
	case lexer.T_ENCAPSED_AND_WHITESPACE:
		text = fmt.Sprintf("string content %q", t.Text)
	}
	msg := "syntax error, unexpected " + text
	if expected != "" {
		msg += ", expecting " + expected
	}
	panic(&SyntaxError{Message: msg, Filename: p.filename, Line: t.Line})
}
func (p *parser) fatal(pos ast.Position, message string) {
	panic(&CompileError{Message: message, Filename: p.filename, Line: pos.Line})
}
func (p *parser) progress(before int) {
	if p.cursor <= before {
		p.unexpected("")
	}
}
func (p *parser) statements(stops ...lexer.ID) []ast.Stmt {
	var result []ast.Stmt
	for {
		for _, stop := range stops {
			if p.id() == stop {
				return result
			}
		}
		before := p.cursor
		result = append(result, p.statement())
		p.progress(before)
	}
}
func (p *parser) semi() {
	if !p.eat(';') {
		p.unexpected("")
	}
}
func (p *parser) block() []ast.Stmt {
	p.expect('{')
	body := p.statements('}')
	p.expect('}')
	return body
}
func (p *parser) body() []ast.Stmt {
	if p.id() == '{' {
		return p.block()
	}
	return []ast.Stmt{p.statement()}
}
func (p *parser) parens() ast.Expr { p.expect('('); e := p.expr(0); p.expect(')'); return e }
func nameToken(id lexer.ID) bool {
	return id == lexer.T_STRING || id == lexer.T_NAME_FULLY_QUALIFIED || id == lexer.T_NAME_QUALIFIED || id == lexer.T_NAME_RELATIVE
}
func identifierToken(id lexer.ID) bool {
	return id == lexer.T_STRING || id >= lexer.T_INCLUDE && id <= lexer.T_NS_C && id != lexer.T_YIELD_FROM && id != lexer.T_HALT_COMPILER
}
func (p *parser) name() string {
	if !nameToken(p.id()) {
		p.unexpected("")
	}
	return p.next().Text
}
func (p *parser) identifier() string {
	if !identifierToken(p.id()) {
		p.unexpected("identifier")
	}
	return p.next().Text
}
func (p *parser) amp() bool {
	return p.eat(lexer.T_AMPERSAND_FOLLOWED_BY_VAR_OR_VARARG) || p.eat(lexer.T_AMPERSAND_NOT_FOLLOWED_BY_VAR_OR_VARARG)
}
func (p *parser) exprList(end lexer.ID) []ast.Expr {
	var list []ast.Expr
	if p.id() == end {
		return list
	}
	for {
		list = append(list, p.expr(0))
		if !p.eat(',') {
			break
		}
	}
	return list
}
func lower(s string) string { return strings.ToLower(s) }
