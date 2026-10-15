package lexer

import (
	"strings"
	"unsafe"
)

const (
	htmlMode byte = iota
	scriptMode
	quoteMode
	backtickMode
	heredocMode
	nowdocMode
	propertyMode
	offsetMode
	varnameMode
	haltedMode
)

const (
	ccSpace uint8 = 1 << iota
	ccLabelStart
	ccLabelPart
	ccOffsetStop
	ccOffsetPunct
	ccScriptPunct
)

// Table rodata constante de classification d'octets évaluée en 1 cycle (charClass[c]&mask != 0).
var charClass = [256]uint8{
	'\t': ccSpace | ccOffsetStop,
	'\n': ccSpace | ccOffsetStop,
	'\r': ccSpace | ccOffsetStop,
	' ':  ccSpace | ccOffsetStop,
	'!':  ccOffsetPunct | ccScriptPunct,
	'"':  ccOffsetPunct,
	'#':  ccOffsetStop,
	'$':  ccOffsetPunct | ccScriptPunct,
	'%':  ccOffsetPunct | ccScriptPunct,
	'&':  ccOffsetPunct,
	'\'': ccOffsetStop,
	'(':  ccOffsetPunct | ccScriptPunct,
	')':  ccOffsetPunct | ccScriptPunct,
	'*':  ccOffsetPunct | ccScriptPunct,
	'+':  ccOffsetPunct | ccScriptPunct,
	',':  ccOffsetPunct | ccScriptPunct,
	'-':  ccOffsetPunct | ccScriptPunct,
	'.':  ccOffsetPunct | ccScriptPunct,
	'/':  ccOffsetPunct | ccScriptPunct,
	'0':  ccLabelPart, '1': ccLabelPart, '2': ccLabelPart, '3': ccLabelPart, '4': ccLabelPart,
	'5':  ccLabelPart, '6': ccLabelPart, '7': ccLabelPart, '8': ccLabelPart, '9': ccLabelPart,
	':':  ccOffsetPunct | ccScriptPunct,
	';':  ccOffsetPunct | ccScriptPunct,
	'<':  ccOffsetPunct | ccScriptPunct,
	'=':  ccOffsetPunct | ccScriptPunct,
	'>':  ccOffsetPunct | ccScriptPunct,
	'?':  ccOffsetPunct | ccScriptPunct,
	'@':  ccOffsetPunct | ccScriptPunct,
	'A':  ccLabelStart | ccLabelPart, 'B': ccLabelStart | ccLabelPart, 'C': ccLabelStart | ccLabelPart, 'D': ccLabelStart | ccLabelPart,
	'E':  ccLabelStart | ccLabelPart, 'F': ccLabelStart | ccLabelPart, 'G': ccLabelStart | ccLabelPart, 'H': ccLabelStart | ccLabelPart,
	'I':  ccLabelStart | ccLabelPart, 'J': ccLabelStart | ccLabelPart, 'K': ccLabelStart | ccLabelPart, 'L': ccLabelStart | ccLabelPart,
	'M':  ccLabelStart | ccLabelPart, 'N': ccLabelStart | ccLabelPart, 'O': ccLabelStart | ccLabelPart, 'P': ccLabelStart | ccLabelPart,
	'Q':  ccLabelStart | ccLabelPart, 'R': ccLabelStart | ccLabelPart, 'S': ccLabelStart | ccLabelPart, 'T': ccLabelStart | ccLabelPart,
	'U':  ccLabelStart | ccLabelPart, 'V': ccLabelStart | ccLabelPart, 'W': ccLabelStart | ccLabelPart, 'X': ccLabelStart | ccLabelPart,
	'Y':  ccLabelStart | ccLabelPart, 'Z': ccLabelStart | ccLabelPart,
	'[':  ccOffsetPunct | ccScriptPunct,
	'\\': ccOffsetStop,
	']':  ccOffsetPunct | ccScriptPunct,
	'^':  ccOffsetPunct | ccScriptPunct,
	'_':  ccLabelStart | ccLabelPart,
	'`':  ccOffsetPunct,
	'a':  ccLabelStart | ccLabelPart, 'b': ccLabelStart | ccLabelPart, 'c': ccLabelStart | ccLabelPart, 'd': ccLabelStart | ccLabelPart,
	'e':  ccLabelStart | ccLabelPart, 'f': ccLabelStart | ccLabelPart, 'g': ccLabelStart | ccLabelPart, 'h': ccLabelStart | ccLabelPart,
	'i':  ccLabelStart | ccLabelPart, 'j': ccLabelStart | ccLabelPart, 'k': ccLabelStart | ccLabelPart, 'l': ccLabelStart | ccLabelPart,
	'm':  ccLabelStart | ccLabelPart, 'n': ccLabelStart | ccLabelPart, 'o': ccLabelStart | ccLabelPart, 'p': ccLabelStart | ccLabelPart,
	'q':  ccLabelStart | ccLabelPart, 'r': ccLabelStart | ccLabelPart, 's': ccLabelStart | ccLabelPart, 't': ccLabelStart | ccLabelPart,
	'u':  ccLabelStart | ccLabelPart, 'v': ccLabelStart | ccLabelPart, 'w': ccLabelStart | ccLabelPart, 'x': ccLabelStart | ccLabelPart,
	'y':  ccLabelStart | ccLabelPart, 'z': ccLabelStart | ccLabelPart,
	'{':  ccOffsetPunct | ccScriptPunct,
	'|':  ccOffsetPunct | ccScriptPunct,
	'}':  ccOffsetPunct | ccScriptPunct,
	'~':  ccOffsetPunct | ccScriptPunct,
	0x80: ccLabelStart | ccLabelPart, 0x81: ccLabelStart | ccLabelPart, 0x82: ccLabelStart | ccLabelPart, 0x83: ccLabelStart | ccLabelPart,
	0x84: ccLabelStart | ccLabelPart, 0x85: ccLabelStart | ccLabelPart, 0x86: ccLabelStart | ccLabelPart, 0x87: ccLabelStart | ccLabelPart,
	0x88: ccLabelStart | ccLabelPart, 0x89: ccLabelStart | ccLabelPart, 0x8a: ccLabelStart | ccLabelPart, 0x8b: ccLabelStart | ccLabelPart,
	0x8c: ccLabelStart | ccLabelPart, 0x8d: ccLabelStart | ccLabelPart, 0x8e: ccLabelStart | ccLabelPart, 0x8f: ccLabelStart | ccLabelPart,
	0x90: ccLabelStart | ccLabelPart, 0x91: ccLabelStart | ccLabelPart, 0x92: ccLabelStart | ccLabelPart, 0x93: ccLabelStart | ccLabelPart,
	0x94: ccLabelStart | ccLabelPart, 0x95: ccLabelStart | ccLabelPart, 0x96: ccLabelStart | ccLabelPart, 0x97: ccLabelStart | ccLabelPart,
	0x98: ccLabelStart | ccLabelPart, 0x99: ccLabelStart | ccLabelPart, 0x9a: ccLabelStart | ccLabelPart, 0x9b: ccLabelStart | ccLabelPart,
	0x9c: ccLabelStart | ccLabelPart, 0x9d: ccLabelStart | ccLabelPart, 0x9e: ccLabelStart | ccLabelPart, 0x9f: ccLabelStart | ccLabelPart,
	0xa0: ccLabelStart | ccLabelPart, 0xa1: ccLabelStart | ccLabelPart, 0xa2: ccLabelStart | ccLabelPart, 0xa3: ccLabelStart | ccLabelPart,
	0xa4: ccLabelStart | ccLabelPart, 0xa5: ccLabelStart | ccLabelPart, 0xa6: ccLabelStart | ccLabelPart, 0xa7: ccLabelStart | ccLabelPart,
	0xa8: ccLabelStart | ccLabelPart, 0xa9: ccLabelStart | ccLabelPart, 0xaa: ccLabelStart | ccLabelPart, 0xab: ccLabelStart | ccLabelPart,
	0xac: ccLabelStart | ccLabelPart, 0xad: ccLabelStart | ccLabelPart, 0xae: ccLabelStart | ccLabelPart, 0xaf: ccLabelStart | ccLabelPart,
	0xb0: ccLabelStart | ccLabelPart, 0xb1: ccLabelStart | ccLabelPart, 0xb2: ccLabelStart | ccLabelPart, 0xb3: ccLabelStart | ccLabelPart,
	0xb4: ccLabelStart | ccLabelPart, 0xb5: ccLabelStart | ccLabelPart, 0xb6: ccLabelStart | ccLabelPart, 0xb7: ccLabelStart | ccLabelPart,
	0xb8: ccLabelStart | ccLabelPart, 0xb9: ccLabelStart | ccLabelPart, 0xba: ccLabelStart | ccLabelPart, 0xbb: ccLabelStart | ccLabelPart,
	0xbc: ccLabelStart | ccLabelPart, 0xbd: ccLabelStart | ccLabelPart, 0xbe: ccLabelStart | ccLabelPart, 0xbf: ccLabelStart | ccLabelPart,
	0xc0: ccLabelStart | ccLabelPart, 0xc1: ccLabelStart | ccLabelPart, 0xc2: ccLabelStart | ccLabelPart, 0xc3: ccLabelStart | ccLabelPart,
	0xc4: ccLabelStart | ccLabelPart, 0xc5: ccLabelStart | ccLabelPart, 0xc6: ccLabelStart | ccLabelPart, 0xc7: ccLabelStart | ccLabelPart,
	0xc8: ccLabelStart | ccLabelPart, 0xc9: ccLabelStart | ccLabelPart, 0xca: ccLabelStart | ccLabelPart, 0xcb: ccLabelStart | ccLabelPart,
	0xcc: ccLabelStart | ccLabelPart, 0xcd: ccLabelStart | ccLabelPart, 0xce: ccLabelStart | ccLabelPart, 0xcf: ccLabelStart | ccLabelPart,
	0xd0: ccLabelStart | ccLabelPart, 0xd1: ccLabelStart | ccLabelPart, 0xd2: ccLabelStart | ccLabelPart, 0xd3: ccLabelStart | ccLabelPart,
	0xd4: ccLabelStart | ccLabelPart, 0xd5: ccLabelStart | ccLabelPart, 0xd6: ccLabelStart | ccLabelPart, 0xd7: ccLabelStart | ccLabelPart,
	0xd8: ccLabelStart | ccLabelPart, 0xd9: ccLabelStart | ccLabelPart, 0xda: ccLabelStart | ccLabelPart, 0xdb: ccLabelStart | ccLabelPart,
	0xdc: ccLabelStart | ccLabelPart, 0xdd: ccLabelStart | ccLabelPart, 0xde: ccLabelStart | ccLabelPart, 0xdf: ccLabelStart | ccLabelPart,
	0xe0: ccLabelStart | ccLabelPart, 0xe1: ccLabelStart | ccLabelPart, 0xe2: ccLabelStart | ccLabelPart, 0xe3: ccLabelStart | ccLabelPart,
	0xe4: ccLabelStart | ccLabelPart, 0xe5: ccLabelStart | ccLabelPart, 0xe6: ccLabelStart | ccLabelPart, 0xe7: ccLabelStart | ccLabelPart,
	0xe8: ccLabelStart | ccLabelPart, 0xe9: ccLabelStart | ccLabelPart, 0xea: ccLabelStart | ccLabelPart, 0xeb: ccLabelStart | ccLabelPart,
	0xec: ccLabelStart | ccLabelPart, 0xed: ccLabelStart | ccLabelPart, 0xee: ccLabelStart | ccLabelPart, 0xef: ccLabelStart | ccLabelPart,
	0xf0: ccLabelStart | ccLabelPart, 0xf1: ccLabelStart | ccLabelPart, 0xf2: ccLabelStart | ccLabelPart, 0xf3: ccLabelStart | ccLabelPart,
	0xf4: ccLabelStart | ccLabelPart, 0xf5: ccLabelStart | ccLabelPart, 0xf6: ccLabelStart | ccLabelPart, 0xf7: ccLabelStart | ccLabelPart,
	0xf8: ccLabelStart | ccLabelPart, 0xf9: ccLabelStart | ccLabelPart, 0xfa: ccLabelStart | ccLabelPart, 0xfb: ccLabelStart | ccLabelPart,
	0xfc: ccLabelStart | ccLabelPart, 0xfd: ccLabelStart | ccLabelPart, 0xfe: ccLabelStart | ccLabelPart, 0xff: ccLabelStart | ccLabelPart,
}

type state struct {
	mode  byte
	label string
}

// Lexer streams PHP tokens. Construct it with New. ShortTags defaults to the
// installed php -n setting (enabled); set it before Next to disable short tags.
// A Lexer must not be copied while scanning or used concurrently.
type Lexer struct {
	src       string
	pos, line int
	state     state
	stack     []state
	local     [16]state
	depth     int
	halt      int
	ShortTags bool
}

func New(src string) *Lexer { return &Lexer{src: src, line: 1, ShortTags: true} }

// Tokenize returns all tokens without EOF. Text slices retain src.
func Tokenize(src string) []Token {
	l := New(src)
	var tokens []Token
	for tok := l.Next(); tok.ID != EOF; tok = l.Next() {
		tokens = append(tokens, tok)
	}
	return tokens
}

func (l *Lexer) push(mode byte) {
	if l.depth < len(l.local) {
		l.local[l.depth] = l.state
	} else {
		l.stack = append(l.stack, l.state)
	}
	l.depth++
	l.state = state{mode: mode}
}

func (l *Lexer) pop() {
	if l.depth == 0 {
		l.state = state{mode: scriptMode}
		return
	}
	l.depth--
	if l.depth < len(l.local) {
		l.state = l.local[l.depth]
	} else {
		l.state = l.stack[len(l.stack)-1]
		l.stack = l.stack[:len(l.stack)-1]
	}
}

func (l *Lexer) emit(id ID, end int) Token {
	tok := Token{ID: id, Text: l.src[l.pos:end], Line: l.line, Pos: l.pos}
	switch id {
	case T_WHITESPACE, T_OPEN_TAG, T_CLOSE_TAG, T_COMMENT, T_DOC_COMMENT,
		T_CONSTANT_ENCAPSED_STRING, T_ENCAPSED_AND_WHITESPACE, T_START_HEREDOC,
		T_INLINE_HTML, T_YIELD_FROM:
		escaped := id == T_ENCAPSED_AND_WHITESPACE && (l.state.mode == quoteMode || l.state.mode == backtickMode)
		if id == T_CONSTANT_ENCAPSED_STRING {
			quote := l.pos
			if lower(l.src[quote]) == 'b' {
				quote++
			}
			escaped = l.src[quote] == '"'
		}
		for i := l.pos; i < end; i++ {
			if escaped && l.src[i] == '\\' && i+1 < end {
				i++
				// Tokenizer retains the whole raw token on an invalid Unicode escape,
				// but Zend's escape scanner stops updating the line counter there.
				if l.src[i] == 'u' && i+1 < end && l.src[i+1] == '{' {
					p, value := i+2, uint64(0)
					for p < end && digit(l.src[p]) < 16 {
						value = min(value*16+digit(l.src[p]), 0x110000)
						p++
					}
					if p == i+2 || p == end || l.src[p] != '}' || value > 0x10ffff {
						break
					}
				}
			}
			if l.src[i] == '\n' || l.src[i] == '\r' && (i+1 >= len(l.src) || l.src[i+1] != '\n') {
				l.line++
			}
		}
	}
	l.pos = end
	if id == T_HALT_COMPILER {
		l.halt = 3
	} else if l.halt > 0 && id != T_WHITESPACE && id != T_COMMENT && id != T_DOC_COMMENT {
		l.halt--
		if l.halt == 0 {
			l.state = state{mode: haltedMode}
		}
	}
	return tok
}

func labelStart(c byte) bool { return charClass[c]&ccLabelStart != 0 }
func labelPart(c byte) bool  { return charClass[c]&ccLabelPart != 0 }
func space(c byte) bool      { return charClass[c]&ccSpace != 0 }
func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
func equalASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if lower(a[i]) != lower(b[i]) {
			return false
		}
	}
	return true
}

func srcBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

func labelEnd(src string, pos int) int {
	if pos+32 <= len(src) {
		return Php_scan_label(srcBytes(src), pos, len(src))
	}
	for pos < len(src) && charClass[src[pos]]&ccLabelPart != 0 {
		pos++
	}
	return pos
}

func newlineEnd(src string, pos int) int {
	if pos < len(src) && src[pos] == '\r' {
		pos++
		if pos < len(src) && src[pos] == '\n' {
			pos++
		}
		return pos
	}
	if pos < len(src) && src[pos] == '\n' {
		return pos + 1
	}
	return pos
}

// Zend's lookahead requires a newline after line comments and excludes NUL.
func triviaEnd(src string, pos int) int {
	for pos < len(src) {
		if space(src[pos]) {
			pos++
			continue
		}
		if strings.HasPrefix(src[pos:], "/*") {
			end := Php_scan_block_comment(srcBytes(src), pos+2, len(src))
			if end >= len(src) || strings.IndexByte(src[pos:end+2], 0) >= 0 {
				break
			}
			pos = end + 2
			continue
		}
		if strings.HasPrefix(src[pos:], "//") || src[pos] == '#' && !strings.HasPrefix(src[pos:], "#[") {
			end := pos + 1
			for end < len(src) && src[end] != 0 && src[end] != '\n' && src[end] != '\r' {
				end++
			}
			if end == len(src) || src[end] == 0 {
				break
			}
			pos = end + 1
			continue
		}
		break
	}
	return pos
}

func (l *Lexer) openTag(pos int) (ID, int) {
	s := l.src[pos:]
	if strings.HasPrefix(s, "<?=") {
		return T_OPEN_TAG_WITH_ECHO, pos + 3
	}
	if len(s) >= 5 && equalASCII(s[:5], "<?php") && (len(s) == 5 || space(s[5])) {
		end := pos + 5
		if end < len(l.src) {
			if l.src[end] == ' ' || l.src[end] == '\t' {
				end++
			} else {
				end = newlineEnd(l.src, end)
			}
		}
		return T_OPEN_TAG, end
	}
	if l.ShortTags && strings.HasPrefix(s, "<?") {
		return T_OPEN_TAG, pos + 2
	}
	return EOF, pos
}

// Next follows token_get_all without TOKEN_PARSE. Malformed input is retained
// as tokens, including T_BAD_CHARACTER, rather than validated as PHP syntax.
func (l *Lexer) Next() Token {
	s := l.src
	p := l.pos
	if p == len(s) {
		return Token{ID: EOF, Line: l.line, Pos: p}
	}
	if l.state.mode == haltedMode {
		return l.emit(T_INLINE_HTML, len(s))
	}
	if l.state.mode == htmlMode {
		if id, end := l.openTag(p); id != EOF {
			l.state.mode = scriptMode
			return l.emit(id, end)
		}
		b := srcBytes(s)
		end := p + 1
		for end < len(s) {
			end = Php_scan_inline_html(b, end, len(s))
			if end < len(s) {
				if id, _ := l.openTag(end); id != EOF {
					break
				}
				end++
			}
		}
		return l.emit(T_INLINE_HTML, end)
	}
	if l.state.mode == propertyMode {
		if space(s[p]) {
			end := p + 1
			for end < len(s) && space(s[end]) {
				end++
			}
			return l.emit(T_WHITESPACE, end)
		}
		if strings.HasPrefix(s[p:], "?->") {
			return l.emit(T_NULLSAFE_OBJECT_OPERATOR, p+3)
		}
		if strings.HasPrefix(s[p:], "->") {
			return l.emit(T_OBJECT_OPERATOR, p+2)
		}
		if labelStart(s[p]) {
			l.pop()
			return l.emit(T_STRING, labelEnd(s, p+1))
		}
		l.pop()
		return l.Next()
	}
	if l.state.mode == varnameMode {
		l.state.mode = scriptMode
		if labelStart(s[p]) {
			end := labelEnd(s, p+1)
			if end < len(s) && (s[end] == '[' || s[end] == '}') {
				return l.emit(T_STRING_VARNAME, end)
			}
		}
	}
	if l.state.mode == offsetMode {
		if s[p] == ']' {
			l.pop()
			return l.emit(']', p+1)
		}
		if charClass[s[p]]&ccOffsetStop != 0 {
			l.pop()
			return l.emit(T_ENCAPSED_AND_WHITESPACE, p)
		}
		if s[p] >= '0' && s[p] <= '9' {
			end, _ := number(s, p, false)
			return l.emit(T_NUM_STRING, end)
		}
		if labelStart(s[p]) {
			return l.emit(T_STRING, labelEnd(s, p+1))
		}
		if s[p] == '$' && p+1 < len(s) && labelStart(s[p+1]) {
			return l.emit(T_VARIABLE, labelEnd(s, p+2))
		}
		if charClass[s[p]]&ccOffsetPunct != 0 {
			return l.emit(ID(s[p]), p+1)
		}
		return l.emit(T_BAD_CHARACTER, p+1)
	}
	if l.state.mode >= quoteMode && l.state.mode <= nowdocMode {
		return l.encapsed()
	}
	if space(s[p]) {
		end := p + 1
		for end < len(s) && space(s[end]) {
			end++
		}
		return l.emit(T_WHITESPACE, end)
	}
	if strings.HasPrefix(s[p:], "?>") {
		l.state.mode = htmlMode
		return l.emit(T_CLOSE_TAG, newlineEnd(s, p+2))
	}
	if strings.HasPrefix(s[p:], "#[") {
		return l.emit(T_ATTRIBUTE, p+2)
	}
	if strings.HasPrefix(s[p:], "//") || s[p] == '#' {
		b := srcBytes(s)
		end := p + 1
		for end < len(s) {
			end = Php_scan_line_comment(b, end, len(s))
			if end >= len(s) || s[end] == '\r' || s[end] == '\n' || strings.HasPrefix(s[end:], "?>") {
				break
			}
			end++
		}
		return l.emit(T_COMMENT, end)
	}
	if strings.HasPrefix(s[p:], "/*") {
		id := T_COMMENT
		if p+3 < len(s) && s[p+2] == '*' && space(s[p+3]) {
			id = T_DOC_COMMENT
		}
		end := Php_scan_block_comment(srcBytes(s), p+2, len(s))
		if end < 0 {
			return l.emit(id, len(s))
		}
		return l.emit(id, end+2)
	}
	start := p
	if lower(s[p]) == 'b' && p+1 < len(s) {
		start++
	}
	if s[start] == '\'' {
		b := srcBytes(s)
		end := start + 1
		for end < len(s) {
			end = Php_scan_single_quote(b, end, len(s))
			if end >= len(s) {
				break
			}
			if s[end] == '\'' {
				return l.emit(T_CONSTANT_ENCAPSED_STRING, end+1)
			}
			if end+1 < len(s) {
				end += 2
			} else {
				end++
			}
		}
		return l.emit(T_ENCAPSED_AND_WHITESPACE, len(s))
	}
	if s[start] == '"' {
		b := srcBytes(s)
		end := start + 1
		for end < len(s) {
			end = Php_scan_double_quote(b, end, len(s))
			if end >= len(s) {
				break
			}
			if s[end] == '"' {
				return l.emit(T_CONSTANT_ENCAPSED_STRING, end+1)
			}
			if interpolation(s, end) {
				break
			}
			if s[end] == '\\' && end+1 < len(s) {
				end += 2
			} else {
				end++
			}
		}
		l.state.mode = quoteMode
		return l.emit('"', start+1)
	}
	if strings.HasPrefix(s[start:], "<<<") {
		end := start + 3
		for end < len(s) && (s[end] == ' ' || s[end] == '\t') {
			end++
		}
		quote := byte(0)
		if end < len(s) && (s[end] == '\'' || s[end] == '"') {
			quote = s[end]
			end++
		}
		label := end
		if end < len(s) && labelStart(s[end]) {
			end = labelEnd(s, end+1)
			name := s[label:end]
			valid := true
			if quote != 0 {
				valid = end < len(s) && s[end] == quote
				if valid {
					end++
				}
			}
			if next := newlineEnd(s, end); valid && next > end {
				l.state = state{mode: heredocMode, label: name}
				if quote == '\'' {
					l.state.mode = nowdocMode
				}
				return l.emit(T_START_HEREDOC, next)
			}
		}
	}
	if s[p] == '`' {
		l.state.mode = backtickMode
		return l.emit('`', p+1)
	}
	if s[p] == '$' && p+1 < len(s) && labelStart(s[p+1]) {
		return l.emit(T_VARIABLE, labelEnd(s, p+2))
	}
	if labelStart(s[p]) || s[p] == '\\' && p+1 < len(s) && labelStart(s[p+1]) {
		end := p
		if s[p] == '\\' {
			end++
		}
		end = labelEnd(s, end+1)
		first := end
		for end+1 < len(s) && s[end] == '\\' && labelStart(s[end+1]) {
			end = labelEnd(s, end+2)
		}
		if s[p] == '\\' {
			return l.emit(T_NAME_FULLY_QUALIFIED, end)
		}
		if end != first {
			id := T_NAME_QUALIFIED
			if equalASCII(s[p:first], "namespace") {
				id = T_NAME_RELATIVE
			}
			return l.emit(id, end)
		}
		id := keywordAt(s, p, end)
		if equalASCII(s[p:end], "enum") {
			next := triviaEnd(s, end)
			if next > end && next < len(s) && labelStart(s[next]) {
				word := s[next:labelEnd(s, next+1)]
				if !(len(word) >= 7 && equalASCII(word[:7], "extends")) && !(len(word) >= 10 && equalASCII(word[:10], "implements")) {
					id = T_ENUM
				}
			}
		}
		if id == T_YIELD {
			next := triviaEnd(s, end)
			if next > end && next+4 <= len(s) && equalASCII(s[next:next+4], "from") && (next+4 == len(s) || !labelPart(s[next+4])) {
				id, end = T_YIELD_FROM, next+4
			}
		}
		return l.emit(id, end)
	}
	if s[p] >= '0' && s[p] <= '9' || s[p] == '.' && p+1 < len(s) && s[p+1] >= '0' && s[p+1] <= '9' {
		end, id := number(s, p, true)
		return l.emit(id, end)
	}
	if s[p] == '(' {
		end := p + 1
		for end < len(s) && (s[end] == ' ' || s[end] == '\t') {
			end++
		}
		start := end
		for end < len(s) && labelStart(s[end]) {
			end++
		}
		id := castAt(s, start, end)
		for end < len(s) && (s[end] == ' ' || s[end] == '\t') {
			end++
		}
		if id != EOF && end < len(s) && s[end] == ')' {
			return l.emit(id, end+1)
		}
	}
	if id, n := operatorAt(s, p); id != EOF {
		if id == T_OBJECT_OPERATOR || id == T_NULLSAFE_OBJECT_OPERATOR {
			l.push(propertyMode)
		}
		return l.emit(id, p+n)
	}
	if s[p] == '&' {
		next := triviaEnd(s, p+1)
		id := T_AMPERSAND_NOT_FOLLOWED_BY_VAR_OR_VARARG
		if next < len(s) && (s[next] == '$' || strings.HasPrefix(s[next:], "...")) {
			id = T_AMPERSAND_FOLLOWED_BY_VAR_OR_VARARG
		}
		return l.emit(id, p+1)
	}
	if s[p] == '\\' {
		return l.emit(T_NS_SEPARATOR, p+1)
	}
	if s[p] == '{' {
		l.push(scriptMode)
	}
	if s[p] == '}' {
		l.pop()
	}
	if charClass[s[p]]&ccScriptPunct != 0 {
		return l.emit(ID(s[p]), p+1)
	}
	return l.emit(T_BAD_CHARACTER, p+1)
}

func interpolation(s string, p int) bool {
	return p+1 < len(s) && (s[p] == '$' && (labelStart(s[p+1]) || s[p+1] == '{') || s[p] == '{' && s[p+1] == '$')
}

func (l *Lexer) heredocEnd(p int) int {
	s := l.src
	if p != 0 && s[p-1] != '\n' && s[p-1] != '\r' {
		return p
	}
	end := p
	for end < len(s) && (s[end] == ' ' || s[end] == '\t') {
		end++
	}
	if strings.HasPrefix(s[end:], l.state.label) {
		end += len(l.state.label)
		if end < len(s) && !labelPart(s[end]) {
			return end
		}
	}
	return p
}

func (l *Lexer) encapsed() Token {
	s, p := l.src, l.pos
	mode := l.state.mode
	doc := mode == heredocMode || mode == nowdocMode
	if doc {
		if end := l.heredocEnd(p); end > p {
			l.state = state{mode: scriptMode}
			return l.emit(T_END_HEREDOC, end)
		}
	} else if mode == quoteMode && s[p] == '"' || mode == backtickMode && s[p] == '`' {
		l.state.mode = scriptMode
		return l.emit(ID(s[p]), p+1)
	}
	if mode != nowdocMode {
		if strings.HasPrefix(s[p:], "${") {
			l.push(varnameMode)
			return l.emit(T_DOLLAR_OPEN_CURLY_BRACES, p+2)
		}
		if strings.HasPrefix(s[p:], "{$") {
			l.push(scriptMode)
			return l.emit(T_CURLY_OPEN, p+1)
		}
		if s[p] == '$' && p+1 < len(s) && labelStart(s[p+1]) {
			end := labelEnd(s, p+2)
			if end < len(s) && s[end] == '[' {
				l.push(offsetMode)
			} else {
				next := end
				if strings.HasPrefix(s[end:], "?->") {
					next += 3
				} else if strings.HasPrefix(s[end:], "->") {
					next += 2
				}
				if next > end && next < len(s) && labelStart(s[next]) {
					l.push(propertyMode)
				}
			}
			return l.emit(T_VARIABLE, end)
		}
	}
	end := p
	if mode == quoteMode {
		b := srcBytes(s)
		for end < len(s) {
			end = Php_scan_double_quote(b, end, len(s))
			if end >= len(s) || s[end] == '"' || interpolation(s, end) {
				break
			}
			if s[end] == '\\' && end+1 < len(s) {
				end += 2
			} else {
				end++
			}
		}
		return l.emit(T_ENCAPSED_AND_WHITESPACE, end)
	}
	for end < len(s) {
		if doc && end > p && l.heredocEnd(end) > end {
			break
		}
		if mode != nowdocMode {
			if interpolation(s, end) || mode == backtickMode && s[end] == '`' {
				break
			}
			if s[end] == '\\' && end+1 < len(s) && (!doc || s[end+1] != '\r' && s[end+1] != '\n') {
				end++
			}
		}
		end++
	}
	return l.emit(T_ENCAPSED_AND_WHITESPACE, end)
}
