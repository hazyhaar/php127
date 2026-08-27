// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/compiler: Lexer pour PHP 8.4

package compiler

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type TokenType int

const (
	TokEOF TokenType = iota
	TokError

	// Literals & Identifiers
	TokInt
	TokFloat
	TokString
	TokIdent
	TokVar

	// Keywords
	TokFunction
	TokReturn
	TokEcho
	TokPrint
	TokIf
	TokElse
	TokWhile
	TokFor
	TokForeach
	TokAs
	TokSwitch
	TokCase
	TokDefault
	TokBreak
	TokContinue
	TokMatch
	TokTrue
	TokFalse
	TokNull
	TokClass
	TokInterface
	TokTrait
	TokExtends
	TokImplements
	TokNew
	TokInstanceOf
	TokPublic
	TokProtected
	TokPrivate
	TokStatic
	TokAbstract
	TokFinal
	TokReadonly
	TokEnum
	TokSelf
	TokParent
	TokFn
	TokInclude
	TokRequire
	TokIncludeOnce
	TokRequireOnce
	TokUse
	TokNamespace
	TokTry
	TokCatch
	TokFinally
	TokThrow
	TokConst
	TokInsteadof
	TokGlobal

	// Operators
	TokPlus        // +
	TokMinus       // -
	TokMul         // *
	TokDiv         // /
	TokMod         // %
	TokPow         // **
	TokDot         // .
	TokEqual       // ==
	TokIdentical   // ===
	TokNotEqual    // != or <>
	TokNotIdent    // !==
	TokLt          // <
	TokLte         // <=
	TokGt          // >
	TokGte         // >=
	TokSpaceship   // <=>
	TokBitAnd      // &
	TokBitOr       // |
	TokBitXor      // ^
	TokBitNot      // ~
	TokShiftLeft   // <<
	TokShiftRight  // >>
	TokAnd         // &&
	TokOr          // ||
	TokNot         // !
	TokQuestion    // ?
	TokColon       // :
	TokDoubleColon // ::
	TokArrow       // ->
	TokNullsafe    // ?->
	TokCoalesce    // ??
	TokDoubleArrow // =>
	TokAssign      // =
	TokPlusAssign  // +=
	TokMinusAssign // -=
	TokMulAssign   // *=
	TokDivAssign   // /=
	TokDotAssign   // .=

	// Delimiters
	TokLParen    // (
	TokRParen    // )
	TokLBrace    // {
	TokRBrace    // }
	TokLBracket  // [
	TokRBracket  // ]
	TokSemi      // ;
	TokComma     // ,
	TokBackslash // \
	TokAttrStart // #[
)

type Token struct {
	Type   TokenType
	Lexeme string
	Line   int
	Col    int
	IntVal int64
	FltVal float64
}

type Lexer struct {
	src    string
	pos    int
	length int
	line   int
	col    int
}

func NewLexer(src string) *Lexer {
	// Strip optional <?php opening tag at the beginning
	trimmed := strings.TrimSpace(src)
	if strings.HasPrefix(trimmed, "<?php") {
		idx := strings.Index(src, "<?php") + 5
		return &Lexer{src: src[idx:], pos: 0, length: len(src[idx:]), line: 1, col: 1}
	}
	return &Lexer{src: src, pos: 0, length: len(src), line: 1, col: 1}
}

func (l *Lexer) NextToken() Token {
	l.skipWhitespaceAndComments()

	if l.pos >= l.length {
		return Token{Type: TokEOF, Line: l.line, Col: l.col}
	}

	startLine := l.line
	startCol := l.col
	ch := l.src[l.pos]

	// Variables: $name
	if ch == '$' {
		l.pos++
		l.col++
		ident := l.readIdent()
		return Token{Type: TokVar, Lexeme: ident, Line: startLine, Col: startCol}
	}

	// Numbers
	if unicode.IsDigit(rune(ch)) {
		return l.readNumber(startLine, startCol)
	}

	// Identifiers and Keywords
	if unicode.IsLetter(rune(ch)) || ch == '_' {
		ident := l.readIdent()
		tokType := l.keywordType(ident)
		return Token{Type: tokType, Lexeme: ident, Line: startLine, Col: startCol}
	}

	// Strings
	if ch == '"' || ch == '\'' {
		return l.readString(ch, startLine, startCol)
	}

	// Multi-character and single-character operators
	l.pos++
	l.col++

	switch ch {
	case '+':
		if l.peek() == '=' {
			l.pos++
			l.col++
			return Token{Type: TokPlusAssign, Lexeme: "+=", Line: startLine, Col: startCol}
		}
		return Token{Type: TokPlus, Lexeme: "+", Line: startLine, Col: startCol}

	case '-':
		if l.peek() == '>' {
			l.pos++
			l.col++
			return Token{Type: TokArrow, Lexeme: "->", Line: startLine, Col: startCol}
		}
		if l.peek() == '=' {
			l.pos++
			l.col++
			return Token{Type: TokMinusAssign, Lexeme: "-=", Line: startLine, Col: startCol}
		}
		return Token{Type: TokMinus, Lexeme: "-", Line: startLine, Col: startCol}

	case '*':
		if l.peek() == '*' {
			l.pos++
			l.col++
			return Token{Type: TokPow, Lexeme: "**", Line: startLine, Col: startCol}
		}
		if l.peek() == '=' {
			l.pos++
			l.col++
			return Token{Type: TokMulAssign, Lexeme: "*=", Line: startLine, Col: startCol}
		}
		return Token{Type: TokMul, Lexeme: "*", Line: startLine, Col: startCol}

	case '/':
		if l.peek() == '=' {
			l.pos++
			l.col++
			return Token{Type: TokDivAssign, Lexeme: "/=", Line: startLine, Col: startCol}
		}
		return Token{Type: TokDiv, Lexeme: "/", Line: startLine, Col: startCol}

	case '%':
		return Token{Type: TokMod, Lexeme: "%", Line: startLine, Col: startCol}

	case '.':
		if l.peek() == '=' {
			l.pos++
			l.col++
			return Token{Type: TokDotAssign, Lexeme: ".=", Line: startLine, Col: startCol}
		}
		return Token{Type: TokDot, Lexeme: ".", Line: startLine, Col: startCol}

	case '=':
		if l.peek() == '=' {
			l.pos++
			l.col++
			if l.peek() == '=' {
				l.pos++
				l.col++
				return Token{Type: TokIdentical, Lexeme: "===", Line: startLine, Col: startCol}
			}
			return Token{Type: TokEqual, Lexeme: "==", Line: startLine, Col: startCol}
		}
		if l.peek() == '>' {
			l.pos++
			l.col++
			return Token{Type: TokDoubleArrow, Lexeme: "=>", Line: startLine, Col: startCol}
		}
		return Token{Type: TokAssign, Lexeme: "=", Line: startLine, Col: startCol}

	case '!':
		if l.peek() == '=' {
			l.pos++
			l.col++
			if l.peek() == '=' {
				l.pos++
				l.col++
				return Token{Type: TokNotIdent, Lexeme: "!==", Line: startLine, Col: startCol}
			}
			return Token{Type: TokNotEqual, Lexeme: "!=", Line: startLine, Col: startCol}
		}
		return Token{Type: TokNot, Lexeme: "!", Line: startLine, Col: startCol}

	case '<':
		if l.peek() == '=' {
			l.pos++
			l.col++
			if l.peek() == '>' {
				l.pos++
				l.col++
				return Token{Type: TokSpaceship, Lexeme: "<=>", Line: startLine, Col: startCol}
			}
			return Token{Type: TokLte, Lexeme: "<=", Line: startLine, Col: startCol}
		}
		if l.peek() == '<' {
			l.pos++
			l.col++
			return Token{Type: TokShiftLeft, Lexeme: "<<", Line: startLine, Col: startCol}
		}
		if l.peek() == '>' {
			l.pos++
			l.col++
			return Token{Type: TokNotEqual, Lexeme: "<>", Line: startLine, Col: startCol}
		}
		return Token{Type: TokLt, Lexeme: "<", Line: startLine, Col: startCol}

	case '>':
		if l.peek() == '=' {
			l.pos++
			l.col++
			return Token{Type: TokGte, Lexeme: ">=", Line: startLine, Col: startCol}
		}
		if l.peek() == '>' {
			l.pos++
			l.col++
			return Token{Type: TokShiftRight, Lexeme: ">>", Line: startLine, Col: startCol}
		}
		return Token{Type: TokGt, Lexeme: ">", Line: startLine, Col: startCol}

	case '&':
		if l.peek() == '&' {
			l.pos++
			l.col++
			return Token{Type: TokAnd, Lexeme: "&&", Line: startLine, Col: startCol}
		}
		return Token{Type: TokBitAnd, Lexeme: "&", Line: startLine, Col: startCol}

	case '|':
		if l.peek() == '|' {
			l.pos++
			l.col++
			return Token{Type: TokOr, Lexeme: "||", Line: startLine, Col: startCol}
		}
		return Token{Type: TokBitOr, Lexeme: "|", Line: startLine, Col: startCol}

	case '^':
		return Token{Type: TokBitXor, Lexeme: "^", Line: startLine, Col: startCol}

	case '~':
		return Token{Type: TokBitNot, Lexeme: "~", Line: startLine, Col: startCol}

	case '?':
		if l.peek() == '?' {
			l.pos++
			l.col++
			return Token{Type: TokCoalesce, Lexeme: "??", Line: startLine, Col: startCol}
		}
		if l.peek() == '-' && l.pos+1 < l.length && l.src[l.pos+1] == '>' {
			l.pos += 2
			l.col += 2
			return Token{Type: TokNullsafe, Lexeme: "?->", Line: startLine, Col: startCol}
		}
		return Token{Type: TokQuestion, Lexeme: "?", Line: startLine, Col: startCol}

	case ':':
		if l.peek() == ':' {
			l.pos++
			l.col++
			return Token{Type: TokDoubleColon, Lexeme: "::", Line: startLine, Col: startCol}
		}
		return Token{Type: TokColon, Lexeme: ":", Line: startLine, Col: startCol}

	case '(':
		return Token{Type: TokLParen, Lexeme: "(", Line: startLine, Col: startCol}
	case ')':
		return Token{Type: TokRParen, Lexeme: ")", Line: startLine, Col: startCol}
	case '{':
		return Token{Type: TokLBrace, Lexeme: "{", Line: startLine, Col: startCol}
	case '}':
		return Token{Type: TokRBrace, Lexeme: "}", Line: startLine, Col: startCol}
	case '[':
		return Token{Type: TokLBracket, Lexeme: "[", Line: startLine, Col: startCol}
	case ']':
		return Token{Type: TokRBracket, Lexeme: "]", Line: startLine, Col: startCol}
	case ';':
		return Token{Type: TokSemi, Lexeme: ";", Line: startLine, Col: startCol}
	case ',':
		return Token{Type: TokComma, Lexeme: ",", Line: startLine, Col: startCol}
	case '\\':
		return Token{Type: TokBackslash, Lexeme: "\\", Line: startLine, Col: startCol}
	case '#':
		if l.peek() == '[' {
			l.pos++
			l.col++
			return Token{Type: TokAttrStart, Lexeme: "#[", Line: startLine, Col: startCol}
		}
		return Token{Type: TokError, Lexeme: "#", Line: startLine, Col: startCol}

	default:
		return Token{Type: TokError, Lexeme: string(ch), Line: startLine, Col: startCol}
	}
}

func (l *Lexer) peek() byte {
	if l.pos < l.length {
		return l.src[l.pos]
	}
	return 0
}

func (l *Lexer) skipWhitespaceAndComments() {
	for l.pos < l.length {
		ch := l.src[l.pos]
		if ch == ' ' || ch == '\t' || ch == '\r' {
			l.pos++
			l.col++
		} else if ch == '\n' {
			l.pos++
			l.line++
			l.col = 1
		} else if ch == '/' && l.pos+1 < l.length && l.src[l.pos+1] == '/' {
			// Line comment //
			for l.pos < l.length && l.src[l.pos] != '\n' {
				l.pos++
			}
		} else if ch == '#' {
			if l.pos+1 < l.length && l.src[l.pos+1] == '[' {
				// Attribute start #[ -> do not skip
				break
			}
			// Line comment #
			for l.pos < l.length && l.src[l.pos] != '\n' {
				l.pos++
			}
		} else if ch == '/' && l.pos+1 < l.length && l.src[l.pos+1] == '*' {
			// Block comment /* ... */
			l.pos += 2
			l.col += 2
			for l.pos+1 < l.length && !(l.src[l.pos] == '*' && l.src[l.pos+1] == '/') {
				if l.src[l.pos] == '\n' {
					l.line++
					l.col = 1
				} else {
					l.col++
				}
				l.pos++
			}
			if l.pos+1 < l.length {
				l.pos += 2
				l.col += 2
			}
		} else {
			break
		}
	}
}

func (l *Lexer) readIdent() string {
	start := l.pos
	for l.pos < l.length {
		ch := l.src[l.pos]
		if unicode.IsLetter(rune(ch)) || unicode.IsDigit(rune(ch)) || ch == '_' {
			l.pos++
			l.col++
		} else {
			break
		}
	}
	return l.src[start:l.pos]
}

func (l *Lexer) readNumber(startLine, startCol int) Token {
	start := l.pos
	isFloat := false

	// Hex 0x...
	if l.src[l.pos] == '0' && l.pos+1 < l.length && (l.src[l.pos+1] == 'x' || l.src[l.pos+1] == 'X') {
		l.pos += 2
		l.col += 2
		for l.pos < l.length && (unicode.IsDigit(rune(l.src[l.pos])) || (l.src[l.pos] >= 'a' && l.src[l.pos] <= 'f') || (l.src[l.pos] >= 'A' && l.src[l.pos] <= 'F')) {
			l.pos++
			l.col++
		}
		raw := l.src[start:l.pos]
		val, _ := strconv.ParseInt(raw, 0, 64)
		return Token{Type: TokInt, Lexeme: raw, Line: startLine, Col: startCol, IntVal: val}
	}

	for l.pos < l.length {
		ch := l.src[l.pos]
		if unicode.IsDigit(rune(ch)) {
			l.pos++
			l.col++
		} else if ch == '.' && !isFloat && l.pos+1 < l.length && unicode.IsDigit(rune(l.src[l.pos+1])) {
			isFloat = true
			l.pos++
			l.col++
		} else {
			break
		}
	}

	raw := l.src[start:l.pos]
	if isFloat {
		fVal, _ := strconv.ParseFloat(raw, 64)
		return Token{Type: TokFloat, Lexeme: raw, Line: startLine, Col: startCol, FltVal: fVal}
	}
	iVal, _ := strconv.ParseInt(raw, 10, 64)
	return Token{Type: TokInt, Lexeme: raw, Line: startLine, Col: startCol, IntVal: iVal}
}

func (l *Lexer) readString(quote byte, startLine, startCol int) Token {
	l.pos++ // Skip opening quote
	l.col++
	var sb strings.Builder

	for l.pos < l.length {
		ch := l.src[l.pos]
		if ch == quote {
			l.pos++
			l.col++
			return Token{Type: TokString, Lexeme: sb.String(), Line: startLine, Col: startCol}
		}
		if ch == '\\' && l.pos+1 < l.length {
			l.pos++
			l.col++
			esc := l.src[l.pos]
			switch esc {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '\\':
				sb.WriteByte('\\')
			case '\'':
				sb.WriteByte('\'')
			case '"':
				sb.WriteByte('"')
			default:
				sb.WriteByte(esc)
			}
			l.pos++
			l.col++
			continue
		}
		if ch == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		sb.WriteByte(ch)
		l.pos++
	}

	return Token{Type: TokError, Lexeme: fmt.Sprintf("chaîne non fermée à la ligne %d", startLine), Line: startLine, Col: startCol}
}

func (l *Lexer) keywordType(ident string) TokenType {
	lower := strings.ToLower(ident)
	switch lower {
	case "function":
		return TokFunction
	case "return":
		return TokReturn
	case "echo":
		return TokEcho
	case "print":
		return TokPrint
	case "if":
		return TokIf
	case "else":
		return TokElse
	case "while":
		return TokWhile
	case "for":
		return TokFor
	case "foreach":
		return TokForeach
	case "as":
		return TokAs
	case "switch":
		return TokSwitch
	case "case":
		return TokCase
	case "default":
		return TokDefault
	case "break":
		return TokBreak
	case "continue":
		return TokContinue
	case "match":
		return TokMatch
	case "true":
		return TokTrue
	case "false":
		return TokFalse
	case "null":
		return TokNull
	case "class":
		return TokClass
	case "interface":
		return TokInterface
	case "trait":
		return TokTrait
	case "extends":
		return TokExtends
	case "implements":
		return TokImplements
	case "new":
		return TokNew
	case "instanceof":
		return TokInstanceOf
	case "public":
		return TokPublic
	case "protected":
		return TokProtected
	case "private":
		return TokPrivate
	case "static":
		return TokStatic
	case "abstract":
		return TokAbstract
	case "final":
		return TokFinal
	case "readonly":
		return TokReadonly
	case "enum":
		return TokEnum
	case "self":
		return TokSelf
	case "parent":
		return TokParent
	case "fn":
		return TokFn
	case "include":
		return TokInclude
	case "require":
		return TokRequire
	case "include_once":
		return TokIncludeOnce
	case "require_once":
		return TokRequireOnce
	case "use":
		return TokUse
	case "namespace":
		return TokNamespace
	case "try":
		return TokTry
	case "catch":
		return TokCatch
	case "finally":
		return TokFinally
	case "throw":
		return TokThrow
	case "const":
		return TokConst
	case "insteadof":
		return TokInsteadof
	case "global":
		return TokGlobal
	default:
		return TokIdent
	}
}
