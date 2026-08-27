// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/compiler: Parseur récursif descendant PHP 8.4

package compiler

import (
	"fmt"
	"strings"
)

type Parser struct {
	lexer   *Lexer
	curTok  Token
	peekTok Token
	errors  []string
}

func NewParser(lexer *Lexer) *Parser {
	p := &Parser{lexer: lexer}
	p.nextToken()
	p.nextToken()
	return p
}

func (p *Parser) nextToken() {
	p.curTok = p.peekTok
	p.peekTok = p.lexer.NextToken()
}

func (p *Parser) expect(t TokenType) bool {
	if p.curTok.Type == t {
		p.nextToken()
		return true
	}
	p.addError(fmt.Sprintf("attendu token %v, reçu %v (%q) ligne %d", t, p.curTok.Type, p.curTok.Lexeme, p.curTok.Line))
	return false
}

func (p *Parser) addError(msg string) {
	p.errors = append(p.errors, msg)
}

func (p *Parser) ParseProgram() ([]Node, error) {
	var stmts []Node
	for p.curTok.Type != TokEOF && p.curTok.Type != TokError {
		stmt := p.parseStatement()
		if stmt != nil {
			stmts = append(stmts, stmt)
		} else {
			p.nextToken()
		}
	}
	if len(p.errors) > 0 {
		return nil, fmt.Errorf("erreurs de compilation: %s", p.errors[0])
	}
	return stmts, nil
}

func (p *Parser) parseStatement() Node {
	switch p.curTok.Type {
	case TokEcho:
		return p.parseEchoStatement()
	case TokPrint:
		return p.parsePrintStatement()
	case TokReturn:
		return p.parseReturnStatement()
	case TokIf:
		return p.parseIfStatement()
	case TokWhile:
		return p.parseWhileStatement()
	case TokFor:
		return p.parseForStatement()
	case TokForeach:
		return p.parseForeachStatement()
	case TokSwitch:
		return p.parseSwitchStatement()
	case TokThrow:
		return p.parseThrowStatement()
	case TokTry:
		return p.parseTryStatement()
	case TokGlobal:
		return p.parseGlobalStatement()
	case TokAttrStart:
		attrs := p.parseAttributes()
		stmt := p.parseStatement()
		if cDecl, ok := stmt.(ClassDeclStmt); ok {
			cDecl.Attributes = append(attrs, cDecl.Attributes...)
			return cDecl
		}
		if fDecl, ok := stmt.(FuncDeclStmt); ok {
			fDecl.Attributes = append(attrs, fDecl.Attributes...)
			return fDecl
		}
		return stmt
	case TokNamespace:
		return p.parseNamespaceStatement()
	case TokUse:
		return p.parseUseStatement()
	case TokEnum:
		return p.parseEnumStatement()
	case TokFunction:
		return p.parseFunctionStatement()
	case TokClass, TokAbstract, TokFinal, TokInterface, TokTrait, TokReadonly:
		return p.parseClassStatement()
	case TokBreak:
		p.nextToken()
		if p.curTok.Type == TokSemi {
			p.nextToken()
		}
		return BreakStmt{}
	case TokContinue:
		p.nextToken()
		if p.curTok.Type == TokSemi {
			p.nextToken()
		}
		return ContinueStmt{}
	case TokLBrace:
		return p.parseBlock()
	case TokSemi:
		p.nextToken()
		return nil
	default:
		return p.parseExpressionOrAssignStatement()
	}
}

func (p *Parser) parseEchoStatement() Node {
	p.nextToken() // skip 'echo'
	var exprs []Node
	for {
		expr := p.parseExpression(0)
		if expr != nil {
			exprs = append(exprs, expr)
		}
		if p.curTok.Type == TokComma {
			p.nextToken()
		} else {
			break
		}
	}
	if p.curTok.Type == TokSemi {
		p.nextToken()
	}
	return EchoStmt{Exprs: exprs}
}

func (p *Parser) parsePrintStatement() Node {
	p.nextToken() // skip 'print'
	expr := p.parseExpression(0)
	if p.curTok.Type == TokSemi {
		p.nextToken()
	}
	return PrintStmt{Expr: expr}
}

func (p *Parser) parseReturnStatement() Node {
	p.nextToken() // skip 'return'
	var val Node
	if p.curTok.Type != TokSemi && p.curTok.Type != TokRBrace && p.curTok.Type != TokEOF {
		val = p.parseExpression(0)
	}
	if p.curTok.Type == TokSemi {
		p.nextToken()
	}
	return ReturnStmt{Val: val}
}

func (p *Parser) parseIfStatement() Node {
	p.nextToken() // skip 'if'
	p.expect(TokLParen)
	cond := p.parseExpression(0)
	p.expect(TokRParen)

	var body []Node
	if p.curTok.Type == TokLBrace {
		body = p.parseBlockStatements()
	} else {
		stmt := p.parseStatement()
		if stmt != nil {
			body = append(body, stmt)
		}
	}

	var elseBody []Node
	if p.curTok.Type == TokElse {
		p.nextToken() // skip 'else'
		if p.curTok.Type == TokIf {
			elseBody = append(elseBody, p.parseIfStatement())
		} else if p.curTok.Type == TokLBrace {
			elseBody = p.parseBlockStatements()
		} else {
			stmt := p.parseStatement()
			if stmt != nil {
				elseBody = append(elseBody, stmt)
			}
		}
	}

	return IfStmt{Cond: cond, Body: body, ElseBody: elseBody}
}

func (p *Parser) parseWhileStatement() Node {
	p.nextToken() // skip 'while'
	p.expect(TokLParen)
	cond := p.parseExpression(0)
	p.expect(TokRParen)

	var body []Node
	if p.curTok.Type == TokLBrace {
		body = p.parseBlockStatements()
	} else {
		stmt := p.parseStatement()
		if stmt != nil {
			body = append(body, stmt)
		}
	}

	return WhileStmt{Cond: cond, Body: body}
}

func (p *Parser) parseForStatement() Node {
	p.nextToken() // skip 'for'
	p.expect(TokLParen)

	var inits []Node
	if p.curTok.Type != TokSemi {
		inits = append(inits, p.parseExpressionOrAssign())
	}
	p.expect(TokSemi)

	var cond Node
	if p.curTok.Type != TokSemi {
		cond = p.parseExpression(0)
	}
	p.expect(TokSemi)

	var posts []Node
	if p.curTok.Type != TokRParen {
		posts = append(posts, p.parseExpressionOrAssign())
	}
	p.expect(TokRParen)

	var body []Node
	if p.curTok.Type == TokLBrace {
		body = p.parseBlockStatements()
	} else {
		stmt := p.parseStatement()
		if stmt != nil {
			body = append(body, stmt)
		}
	}

	return ForStmt{Init: inits, Cond: cond, Post: posts, Body: body}
}

func (p *Parser) parseForeachStatement() Node {
	p.nextToken() // skip 'foreach'
	p.expect(TokLParen)
	arr := p.parseExpression(0)
	p.expect(TokAs)

	var keyVar string
	var valVar string

	firstVar := p.curTok.Lexeme
	p.expect(TokVar)

	if p.curTok.Type == TokDoubleArrow {
		p.nextToken() // skip =>
		keyVar = firstVar
		valVar = p.curTok.Lexeme
		p.expect(TokVar)
	} else {
		valVar = firstVar
	}

	p.expect(TokRParen)

	var body []Node
	if p.curTok.Type == TokLBrace {
		body = p.parseBlockStatements()
	} else {
		stmt := p.parseStatement()
		if stmt != nil {
			body = append(body, stmt)
		}
	}

	return ForeachStmt{Array: arr, KeyVar: keyVar, ValVar: valVar, Body: body}
}

func (p *Parser) parseSwitchStatement() Node {
	p.nextToken() // skip 'switch'
	p.expect(TokLParen)
	expr := p.parseExpression(0)
	p.expect(TokRParen)
	p.expect(TokLBrace)

	var cases []SwitchCase
	for p.curTok.Type != TokRBrace && p.curTok.Type != TokEOF {
		if p.curTok.Type == TokCase {
			p.nextToken() // skip 'case'
			val := p.parseExpression(0)
			p.expect(TokColon)
			var body []Node
			for p.curTok.Type != TokCase && p.curTok.Type != TokDefault && p.curTok.Type != TokRBrace && p.curTok.Type != TokEOF {
				stmt := p.parseStatement()
				if stmt != nil {
					body = append(body, stmt)
				}
			}
			cases = append(cases, SwitchCase{Values: []Node{val}, Body: body})
		} else if p.curTok.Type == TokDefault {
			p.nextToken() // skip 'default'
			p.expect(TokColon)
			var body []Node
			for p.curTok.Type != TokCase && p.curTok.Type != TokDefault && p.curTok.Type != TokRBrace && p.curTok.Type != TokEOF {
				stmt := p.parseStatement()
				if stmt != nil {
					body = append(body, stmt)
				}
			}
			cases = append(cases, SwitchCase{Values: nil, Body: body})
		} else {
			p.nextToken()
		}
	}
	p.expect(TokRBrace)

	return SwitchStmt{Expr: expr, Cases: cases}
}

func (p *Parser) parseFunctionStatement() Node {
	p.nextToken() // skip 'function'
	name := p.curTok.Lexeme
	p.expect(TokIdent)

	p.expect(TokLParen)
	var params []string
	for p.curTok.Type == TokVar {
		params = append(params, p.curTok.Lexeme)
		p.nextToken()
		if p.curTok.Type == TokComma {
			p.nextToken()
		} else {
			break
		}
	}
	p.expect(TokRParen)

	body := p.parseBlockStatements()
	return FuncDeclStmt{Name: name, Params: params, Body: body}
}

func (p *Parser) parseThrowStatement() Node {
	p.nextToken() // skip 'throw'
	expr := p.parseExpression(0)
	p.expect(TokSemi)
	return ThrowStmt{Expr: expr}
}

func (p *Parser) parseTryStatement() Node {
	p.nextToken() // skip 'try'
	tryBody := p.parseBlockStatements()
	var catches []CatchClause

	for p.curTok.Type == TokCatch {
		p.nextToken() // skip 'catch'
		p.expect(TokLParen)
		var types []string
		for {
			typeName := p.curTok.Lexeme
			p.nextToken()
			types = append(types, typeName)
			if p.curTok.Type == TokBitOr {
				p.nextToken()
			} else {
				break
			}
		}
		var varName string
		if p.curTok.Type == TokVar {
			varName = p.curTok.Lexeme
			p.nextToken()
		}
		p.expect(TokRParen)
		catchBody := p.parseBlockStatements()
		catches = append(catches, CatchClause{
			Types: types,
			Var:   varName,
			Body:  catchBody,
		})
	}

	var finallyBody []Node
	if p.curTok.Type == TokFinally {
		p.nextToken() // skip 'finally'
		finallyBody = p.parseBlockStatements()
	}

	return TryCatchStmt{
		TryBody:     tryBody,
		Catches:     catches,
		FinallyBody: finallyBody,
	}
}

func (p *Parser) parseGlobalStatement() Node {
	p.nextToken() // skip 'global'
	var vars []string
	for p.curTok.Type == TokVar {
		vars = append(vars, p.curTok.Lexeme)
		p.nextToken()
		if p.curTok.Type == TokComma {
			p.nextToken()
		} else {
			break
		}
	}
	p.expect(TokSemi)
	return GlobalStmt{Vars: vars}
}

func (p *Parser) parseQualifiedName() string {
	var sb strings.Builder
	if p.curTok.Type == TokBackslash {
		sb.WriteString("\\")
		p.nextToken()
	}
	for {
		if p.curTok.Type == TokIdent || p.curTok.Type == TokNamespace || p.curTok.Type == TokClass {
			sb.WriteString(p.curTok.Lexeme)
			p.nextToken()
		} else {
			break
		}
		if p.curTok.Type == TokBackslash {
			sb.WriteString("\\")
			p.nextToken()
		} else {
			break
		}
	}
	return sb.String()
}

func (p *Parser) parseNamespaceStatement() Node {
	p.nextToken() // skip 'namespace'
	nsName := p.parseQualifiedName()
	if p.curTok.Type == TokSemi {
		p.nextToken()
		return NamespaceStmt{Name: nsName}
	}
	if p.curTok.Type == TokLBrace {
		stmts := p.parseBlockStatements()
		return NamespaceStmt{Name: nsName, Stmts: stmts}
	}
	return NamespaceStmt{Name: nsName}
}

func (p *Parser) parseUseStatement() Node {
	p.nextToken() // skip 'use'
	var imports []UseImport
	for {
		name := p.parseQualifiedName()
		alias := ""
		if p.curTok.Type == TokAs {
			p.nextToken()
			alias = p.curTok.Lexeme
			p.expect(TokIdent)
		} else {
			parts := strings.Split(name, "\\")
			alias = parts[len(parts)-1]
		}
		imports = append(imports, UseImport{Name: name, Alias: alias})
		if p.curTok.Type == TokComma {
			p.nextToken()
		} else {
			break
		}
	}
	if p.curTok.Type == TokSemi {
		p.nextToken()
	}
	return UseStmt{Imports: imports}
}

func (p *Parser) parseEnumStatement() Node {
	p.nextToken() // skip 'enum'
	name := p.curTok.Lexeme
	p.expect(TokIdent)
	backedType := ""
	if p.curTok.Type == TokColon {
		p.nextToken()
		backedType = p.curTok.Lexeme
		p.expect(TokIdent)
	}
	p.expect(TokLBrace)
	var cases []EnumCase
	var methods []MethodDecl
	for p.curTok.Type != TokRBrace && p.curTok.Type != TokEOF {
		if p.curTok.Type == TokCase {
			p.nextToken() // skip 'case'
			cName := p.curTok.Lexeme
			p.expect(TokIdent)
			var val Node
			if p.curTok.Type == TokAssign {
				p.nextToken()
				val = p.parseExpression(0)
			}
			if p.curTok.Type == TokSemi {
				p.nextToken()
			}
			cases = append(cases, EnumCase{Name: cName, Val: val})
		} else {
			p.nextToken()
		}
	}
	p.expect(TokRBrace)
	return EnumDeclStmt{Name: name, BackedType: backedType, Cases: cases, Methods: methods}
}

func (p *Parser) parseAttributes() []AttributeDecl {
	var attrs []AttributeDecl
	for p.curTok.Type == TokAttrStart {
		p.nextToken() // skip '#['
		for p.curTok.Type != TokRBracket && p.curTok.Type != TokEOF {
			name := p.parseQualifiedName()
			var args []Node
			if p.curTok.Type == TokLParen {
				p.nextToken() // skip '('
				if p.curTok.Type != TokRParen {
					for {
						arg := p.parseExpression(0)
						if arg != nil {
							args = append(args, arg)
						}
						if p.curTok.Type == TokComma {
							p.nextToken()
						} else {
							break
						}
					}
				}
				p.expect(TokRParen)
			}
			attrs = append(attrs, AttributeDecl{Name: name, Args: args})
			if p.curTok.Type == TokComma {
				p.nextToken()
			} else {
				break
			}
		}
		p.expect(TokRBracket)
	}
	return attrs
}

func (p *Parser) parseClassStatement() Node {
	isAbstract := false
	isFinal := false
	isInterface := false
	isTrait := false

	for {
		if p.curTok.Type == TokAbstract {
			isAbstract = true
			p.nextToken()
		} else if p.curTok.Type == TokFinal {
			isFinal = true
			p.nextToken()
		} else {
			break
		}
	}

	if p.curTok.Type == TokInterface {
		isInterface = true
		p.nextToken()
	} else if p.curTok.Type == TokTrait {
		isTrait = true
		p.nextToken()
	} else if p.curTok.Type == TokClass {
		p.nextToken()
	}

	className := p.curTok.Lexeme
	p.expect(TokIdent)

	parent := ""
	if p.curTok.Type == TokExtends {
		p.nextToken()
		parent = p.curTok.Lexeme
		p.expect(TokIdent)
	}

	var implements []string
	if p.curTok.Type == TokImplements {
		p.nextToken()
		for {
			implements = append(implements, p.curTok.Lexeme)
			p.expect(TokIdent)
			if p.curTok.Type == TokComma {
				p.nextToken()
			} else {
				break
			}
		}
	}

	p.expect(TokLBrace)
	var props []PropertyDecl
	var methods []MethodDecl
	var consts []ClassConstDecl
	var traits []string

	for p.curTok.Type != TokRBrace && p.curTok.Type != TokEOF {
		vis := TokPublic
		isStatic := false
		isReadonly := false
		isMethAbstract := false
		isMethFinal := false

		for {
			if p.curTok.Type == TokPublic || p.curTok.Type == TokProtected || p.curTok.Type == TokPrivate {
				vis = p.curTok.Type
				p.nextToken()
			} else if p.curTok.Type == TokStatic {
				isStatic = true
				p.nextToken()
			} else if p.curTok.Type == TokReadonly {
				isReadonly = true
				p.nextToken()
			} else if p.curTok.Type == TokAbstract {
				isMethAbstract = true
				p.nextToken()
			} else if p.curTok.Type == TokFinal {
				isMethFinal = true
				p.nextToken()
			} else {
				break
			}
		}

		if p.curTok.Type == TokFunction {
			p.nextToken() // skip function
			mName := p.curTok.Lexeme
			p.expect(TokIdent)
			p.expect(TokLParen)
			var params []string
			var paramDecls []ParamDecl
			for p.curTok.Type != TokRParen && p.curTok.Type != TokEOF {
				pVis := "public"
				pPromoted := false
				pReadonly := false
				if p.curTok.Type == TokPublic || p.curTok.Type == TokProtected || p.curTok.Type == TokPrivate {
					pVis = p.curTok.Lexeme
					pPromoted = true
					p.nextToken()
				}
				if p.curTok.Type == TokReadonly {
					pReadonly = true
					p.nextToken()
				}
				pType := ""
				if p.curTok.Type == TokIdent {
					pType = p.curTok.Lexeme
					p.nextToken()
				}
				if p.curTok.Type == TokVar {
					pName := p.curTok.Lexeme
					p.nextToken()
					var defVal Node
					if p.curTok.Type == TokAssign {
						p.nextToken()
						defVal = p.parseExpression(0)
					}
					params = append(params, pName)
					paramDecls = append(paramDecls, ParamDecl{
						Name:       pName,
						TypeHint:   pType,
						DefaultVal: defVal,
						IsPromoted: pPromoted,
						Visibility: pVis,
						IsReadonly: pReadonly,
					})
					if pPromoted {
						props = append(props, PropertyDecl{Name: pName, DefaultVal: defVal})
					}
				}
				if p.curTok.Type == TokComma {
					p.nextToken()
				} else {
					break
				}
			}
			p.expect(TokRParen)

			var body []Node
			if p.curTok.Type == TokSemi || isMethAbstract || isInterface {
				if p.curTok.Type == TokSemi {
					p.nextToken()
				}
			} else {
				body = p.parseBlockStatements()
			}
			methods = append(methods, MethodDecl{
				Name:       mName,
				Visibility: vis,
				IsStatic:   isStatic,
				IsAbstract: isMethAbstract,
				IsFinal:    isMethFinal,
				Params:     params,
				ParamDecls: paramDecls,
				Body:       body,
			})
		} else if p.curTok.Type == TokConst {
			p.nextToken() // skip const
			cName := p.curTok.Lexeme
			p.expect(TokIdent)
			p.expect(TokAssign)
			cVal := p.parseExpression(0)
			if p.curTok.Type == TokSemi {
				p.nextToken()
			}
			consts = append(consts, ClassConstDecl{Name: cName, Val: cVal})
		} else if p.curTok.Type == TokUse {
			p.nextToken() // skip use
			for {
				tName := p.curTok.Lexeme
				p.nextToken()
				traits = append(traits, tName)
				if p.curTok.Type == TokComma {
					p.nextToken()
				} else {
					break
				}
			}
			if p.curTok.Type == TokLBrace {
				p.nextToken() // skip '{'
				for p.curTok.Type != TokRBrace && p.curTok.Type != TokEOF {
					for p.curTok.Type != TokSemi && p.curTok.Type != TokRBrace && p.curTok.Type != TokEOF {
						p.nextToken()
					}
					if p.curTok.Type == TokSemi {
						p.nextToken()
					}
				}
				p.expect(TokRBrace)
			} else if p.curTok.Type == TokSemi {
				p.nextToken()
			}
		} else if p.curTok.Type == TokVar {
			propName := p.curTok.Lexeme
			p.nextToken()
			var defVal Node
			if p.curTok.Type == TokAssign {
				p.nextToken()
				defVal = p.parseExpression(0)
			}
			if p.curTok.Type == TokSemi {
				p.nextToken()
			}
			props = append(props, PropertyDecl{
				Name:       propName,
				Visibility: vis,
				IsStatic:   isStatic,
				IsReadonly: isReadonly,
				DefaultVal: defVal,
			})
		} else {
			p.nextToken()
		}
	}
	p.expect(TokRBrace)

	return ClassDeclStmt{
		Name:        className,
		Parent:      parent,
		Implements:  implements,
		IsAbstract:  isAbstract,
		IsFinal:     isFinal,
		IsInterface: isInterface,
		IsTrait:     isTrait,
		Traits:      traits,
		Constants:   consts,
		Properties:  props,
		Methods:     methods,
	}
}

func (p *Parser) parseBlock() Node {
	stmts := p.parseBlockStatements()
	return BlockStmt{Stmts: stmts}
}

func (p *Parser) parseBlockStatements() []Node {
	p.expect(TokLBrace)
	var stmts []Node
	for p.curTok.Type != TokRBrace && p.curTok.Type != TokEOF {
		stmt := p.parseStatement()
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
	}
	p.expect(TokRBrace)
	return stmts
}

func (p *Parser) parseExpressionOrAssignStatement() Node {
	node := p.parseExpressionOrAssign()
	if p.curTok.Type == TokSemi {
		p.nextToken()
	}
	return ExprStmt{Expr: node}
}

func (p *Parser) parseExpressionOrAssign() Node {
	expr := p.parseExpression(0)
	if p.curTok.Type == TokAssign || p.curTok.Type == TokPlusAssign || p.curTok.Type == TokMinusAssign || p.curTok.Type == TokMulAssign || p.curTok.Type == TokDivAssign || p.curTok.Type == TokDotAssign {
		op := p.curTok.Type
		p.nextToken()
		val := p.parseExpression(0)
		return AssignStmt{Target: expr, Op: op, Val: val}
	}
	return expr
}

// Precedence levels
const (
	precLowest = iota
	precAssign
	precTernary
	precOr
	precAnd
	precBitOr
	precBitXor
	precBitAnd
	precEquality
	precRelational
	precShift
	precAdditive
	precMultiplicative
	precPower
	precUnary
)

func tokenPrecedence(t TokenType) int {
	switch t {
	case TokQuestion:
		return precTernary
	case TokOr:
		return precOr
	case TokAnd:
		return precAnd
	case TokBitOr:
		return precBitOr
	case TokBitXor:
		return precBitXor
	case TokBitAnd:
		return precBitAnd
	case TokEqual, TokNotEqual, TokIdentical, TokNotIdent:
		return precEquality
	case TokLt, TokLte, TokGt, TokGte, TokSpaceship:
		return precRelational
	case TokShiftLeft, TokShiftRight:
		return precShift
	case TokPlus, TokMinus, TokDot:
		return precAdditive
	case TokMul, TokDiv, TokMod:
		return precMultiplicative
	case TokPow:
		return precPower
	default:
		return precLowest
	}
}

func (p *Parser) parseExpression(minPrec int) Node {
	left := p.parsePrimary()
	if left == nil {
		return nil
	}

	for {
		tok := p.curTok.Type
		prec := tokenPrecedence(tok)
		if prec <= minPrec {
			break
		}

		if tok == TokQuestion {
			// Ternary cond ? a : b or short ternary cond ?: b
			p.nextToken() // skip '?'
			var trueBranch Node
			if p.curTok.Type == TokColon {
				// Short ternary: cond ?: falseBranch
				trueBranch = nil
			} else {
				trueBranch = p.parseExpression(0)
			}
			p.expect(TokColon)
			falseBranch := p.parseExpression(precTernary - 1)
			left = TernaryExpr{Cond: left, TrueBranch: trueBranch, FalseBranch: falseBranch}
			continue
		}

		p.nextToken()
		nextMinPrec := prec
		if tok == TokPow {
			nextMinPrec = prec - 1 // Right associative
		}
		right := p.parseExpression(nextMinPrec)
		left = BinaryExpr{Op: tok, Left: left, Right: right}
	}

	return left
}

func (p *Parser) parsePrimary() Node {
	var node Node

	switch p.curTok.Type {
	case TokInt:
		val := p.curTok.IntVal
		p.nextToken()
		node = IntLitExpr{Val: val}

	case TokFloat:
		val := p.curTok.FltVal
		p.nextToken()
		node = FloatLitExpr{Val: val}

	case TokString:
		val := p.curTok.Lexeme
		p.nextToken()
		node = StringLitExpr{Val: val}

	case TokTrue:
		p.nextToken()
		node = BoolLitExpr{Val: true}

	case TokFalse:
		p.nextToken()
		node = BoolLitExpr{Val: false}

	case TokNull:
		p.nextToken()
		node = NullLitExpr{}

	case TokVar:
		varName := p.curTok.Lexeme
		p.nextToken()
		if p.curTok.Type == TokLParen {
			p.nextToken() // skip (
			var args []Node
			if p.curTok.Type != TokRParen {
				for {
					arg := p.parseExpression(0)
					if arg != nil {
						args = append(args, arg)
					}
					if p.curTok.Type == TokComma {
						p.nextToken()
					} else {
						break
					}
				}
			}
			p.expect(TokRParen)
			node = CallExpr{Callee: "$" + varName, Args: args}
		} else {
			node = VarExpr{Name: varName}
		}

	case TokIdent, TokBackslash:
		ident := p.parseQualifiedName()
		if p.curTok.Type == TokLParen {
			p.nextToken() // skip (
			var args []Node
			if p.curTok.Type != TokRParen {
				for {
					arg := p.parseExpression(0)
					if arg != nil {
						args = append(args, arg)
					}
					if p.curTok.Type == TokComma {
						p.nextToken()
					} else {
						break
					}
				}
			}
			p.expect(TokRParen)
			node = CallExpr{Callee: ident, Args: args}
		} else {
			node = VarExpr{Name: ident}
		}

	case TokNew:
		p.nextToken() // skip 'new'
		clsName := p.parseQualifiedName()
		var args []Node
		if p.curTok.Type == TokLParen {
			p.nextToken()
			if p.curTok.Type != TokRParen {
				for {
					arg := p.parseExpression(0)
					if arg != nil {
						args = append(args, arg)
					}
					if p.curTok.Type == TokComma {
						p.nextToken()
					} else {
						break
					}
				}
			}
			p.expect(TokRParen)
		}
		node = NewExpr{ClassName: clsName, Args: args}

	case TokInclude, TokRequire, TokIncludeOnce, TokRequireOnce:
		isReq := p.curTok.Type == TokRequire || p.curTok.Type == TokRequireOnce
		isOnce := p.curTok.Type == TokIncludeOnce || p.curTok.Type == TokRequireOnce
		p.nextToken()
		path := p.parseExpression(0)
		return IncludeExpr{Path: path, IsRequire: isReq, IsOnce: isOnce}

	case TokMatch:
		return p.parseMatchExpression()

	case TokFunction:
		p.nextToken() // skip 'function'
		p.expect(TokLParen)
		var params []string
		for p.curTok.Type == TokVar {
			params = append(params, p.curTok.Lexeme)
			p.nextToken()
			if p.curTok.Type == TokComma {
				p.nextToken()
			} else {
				break
			}
		}
		p.expect(TokRParen)
		var uses []string
		if p.curTok.Type == TokUse {
			p.nextToken() // skip 'use'
			p.expect(TokLParen)
			for p.curTok.Type == TokVar || p.curTok.Type == TokBitAnd {
				if p.curTok.Type == TokBitAnd {
					p.nextToken()
				}
				if p.curTok.Type == TokVar {
					uses = append(uses, p.curTok.Lexeme)
					p.nextToken()
				}
				if p.curTok.Type == TokComma {
					p.nextToken()
				} else {
					break
				}
			}
			p.expect(TokRParen)
		}
		body := p.parseBlockStatements()
		return ClosureExpr{Params: params, Uses: uses, Body: body}

	case TokFn:
		p.nextToken() // skip 'fn'
		p.expect(TokLParen)
		var params []string
		for p.curTok.Type == TokVar {
			params = append(params, p.curTok.Lexeme)
			p.nextToken()
			if p.curTok.Type == TokComma {
				p.nextToken()
			} else {
				break
			}
		}
		p.expect(TokRParen)
		p.expect(TokDoubleArrow) // =>
		bodyExpr := p.parseExpression(0)
		return ArrowFuncExpr{Params: params, Expr: bodyExpr}

	case TokLBracket:
		node = p.parseArrayLiteral()

	case TokLParen:
		p.nextToken()
		expr := p.parseExpression(0)
		p.expect(TokRParen)
		node = expr

	case TokPlus, TokMinus, TokNot, TokBitNot:
		op := p.curTok.Type
		p.nextToken()
		sub := p.parsePrimary()
		return UnaryExpr{Op: op, Expr: sub}

	default:
		p.addError(fmt.Sprintf("expression inattendue: %v (%q) ligne %d", p.curTok.Type, p.curTok.Lexeme, p.curTok.Line))
		return nil
	}

	// Postfix chaining: $obj->prop, $obj->method(), $arr[idx], Class::method(), $obj instanceof Class
	for node != nil {
		if p.curTok.Type == TokLBracket {
			p.nextToken() // skip [
			var idx Node
			if p.curTok.Type != TokRBracket {
				idx = p.parseExpression(0)
			}
			p.expect(TokRBracket)
			node = ArrayAccessExpr{Target: node, Index: idx}
		} else if p.curTok.Type == TokArrow || p.curTok.Type == TokNullsafe {
			isNullsafe := p.curTok.Type == TokNullsafe
			p.nextToken() // skip -> or ?->
			member := p.curTok.Lexeme
			if p.curTok.Type == TokIdent || p.curTok.Type == TokVar {
				p.nextToken()
			} else {
				p.expect(TokIdent)
			}
			if p.curTok.Type == TokLParen {
				p.nextToken() // skip (
				var args []Node
				if p.curTok.Type != TokRParen {
					for {
						arg := p.parseExpression(0)
						if arg != nil {
							args = append(args, arg)
						}
						if p.curTok.Type == TokComma {
							p.nextToken()
						} else {
							break
						}
					}
				}
				p.expect(TokRParen)
				node = MethodCallExpr{Target: node, Method: member, Args: args, IsNullsafe: isNullsafe}
			} else {
				node = PropertyAccessExpr{Target: node, Property: member, IsNullsafe: isNullsafe}
			}
		} else if p.curTok.Type == TokDoubleColon {
			p.nextToken() // skip ::
			var className string
			if v, ok := node.(VarExpr); ok {
				className = v.Name
			}
			if p.curTok.Type == TokVar {
				prop := p.curTok.Lexeme
				p.nextToken()
				node = StaticPropertyExpr{ClassName: className, Property: prop}
			} else {
				method := p.curTok.Lexeme
				p.expect(TokIdent)
				p.expect(TokLParen)
				var args []Node
				if p.curTok.Type != TokRParen {
					for {
						arg := p.parseExpression(0)
						if arg != nil {
							args = append(args, arg)
						}
						if p.curTok.Type == TokComma {
							p.nextToken()
						} else {
							break
						}
					}
				}
				p.expect(TokRParen)
				node = StaticCallExpr{ClassName: className, Method: method, Args: args}
			}
		} else if p.curTok.Type == TokInstanceOf {
			p.nextToken() // skip instanceof
			cls := p.curTok.Lexeme
			p.expect(TokIdent)
			node = InstanceOfExpr{Target: node, ClassName: cls}
		} else {
			break
		}
	}

	return node
}

func (p *Parser) parseArrayLiteral() Node {
	p.expect(TokLBracket)
	var elements []ArrayElement

	for p.curTok.Type != TokRBracket && p.curTok.Type != TokEOF {
		keyOrVal := p.parseExpression(0)
		if p.curTok.Type == TokDoubleArrow {
			p.nextToken() // skip =>
			val := p.parseExpression(0)
			elements = append(elements, ArrayElement{Key: keyOrVal, Val: val})
		} else {
			elements = append(elements, ArrayElement{Key: nil, Val: keyOrVal})
		}
		if p.curTok.Type == TokComma {
			p.nextToken()
		} else {
			break
		}
	}

	p.expect(TokRBracket)
	return ArrayLitExpr{Elements: elements}
}

func (p *Parser) parseMatchExpression() Node {
	p.nextToken() // skip 'match'
	p.expect(TokLParen)
	subject := p.parseExpression(0)
	p.expect(TokRParen)
	p.expect(TokLBrace)

	var arms []MatchArm
	for p.curTok.Type != TokRBrace && p.curTok.Type != TokEOF {
		var conds []Node
		if p.curTok.Type == TokDefault {
			p.nextToken() // skip default
			conds = nil
		} else {
			for {
				cond := p.parseExpression(0)
				conds = append(conds, cond)
				if p.curTok.Type == TokComma {
					p.nextToken()
					if p.curTok.Type == TokDoubleArrow {
						break
					}
				} else {
					break
				}
			}
		}
		p.expect(TokDoubleArrow)
		body := p.parseExpression(0)
		arms = append(arms, MatchArm{Conds: conds, Body: body})

		if p.curTok.Type == TokComma {
			p.nextToken()
		}
	}
	p.expect(TokRBrace)

	return MatchExpr{Expr: subject, Arms: arms}
}
