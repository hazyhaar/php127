package mysqli

import (
	"encoding/hex"
	"strings"
	"unsafe"
)

// Nature d'un jeton SQL du dialecte MySQL.
const (
	tkSpace uint8 = iota
	tkComment
	tkString
	tkIdent
	tkNumber
	tkWord
	tkPunct
	tkParam
	tkVar
	tkRaw
)

// token est un jeton lexical ; val porte la valeur décodée d'une chaîne ou d'un identifiant.
type token struct {
	kind uint8
	text string
	val  string
}

func (t token) sig() bool { return t.kind != tkSpace && t.kind != tkComment }

// is compare un mot-clé sans tenir compte de la casse.
func (t token) is(word string) bool {
	return t.kind == tkWord && strings.EqualFold(t.text, word)
}

func (t token) punct(p string) bool { return t.kind == tkPunct && t.text == p }

// name retourne le nom désigné par un identifiant ou un mot.
func (t token) name() string {
	if t.kind == tkIdent {
		return t.val
	}
	if t.kind == tkString {
		return t.val
	}
	return t.text
}

func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// strBytes expose les octets d'une chaîne sans copie aux noyaux sgoiter de c_src/sql_scan.c, qui ne font que lire.
func strBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// lexSQL découpe une requête MySQL ; les commentaires exécutables « /*! ... */ » sont dépliés.
func lexSQL(sql string) []token {
	var toks []token
	inExec := false
	i := 0
	n := len(sql)
	src := strBytes(sql)
	end := uint64(n)
	for i < n {
		c := sql[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			j := int(C2_sql_skip_spaces(src, uint64(i), end))
			toks = append(toks, token{kind: tkSpace, text: sql[i:j]})
			i = j
		case inExec && c == '*' && i+1 < n && sql[i+1] == '/':
			inExec = false
			toks = append(toks, token{kind: tkSpace, text: " "})
			i += 2
		case c == '/' && i+2 < n && sql[i+1] == '*' && sql[i+2] == '!':
			j := i + 3
			for j < n && sql[j] >= '0' && sql[j] <= '9' {
				j++
			}
			inExec = true
			toks = append(toks, token{kind: tkSpace, text: " "})
			i = j
		case c == '/' && i+1 < n && sql[i+1] == '*':
			end := strings.Index(sql[i+2:], "*/")
			j := n
			if end >= 0 {
				j = i + 2 + end + 2
			}
			toks = append(toks, token{kind: tkComment, text: sql[i:j]})
			i = j
		case c == '#' || (c == '-' && i+2 < n && sql[i+1] == '-' && (sql[i+2] == ' ' || sql[i+2] == '\t' || sql[i+2] == '\n')) || (c == '-' && i+2 == n && sql[i+1] == '-'):
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				j = n
			} else {
				j += i
			}
			toks = append(toks, token{kind: tkComment, text: sql[i:j]})
			i = j
		case c == '\'' || c == '"':
			val, j := lexQuoted(sql, i)
			toks = append(toks, token{kind: tkString, text: sql[i:j], val: val})
			i = j
		case c == '`':
			var b strings.Builder
			j := i + 1
			for j < n {
				if sql[j] == '`' {
					if j+1 < n && sql[j+1] == '`' {
						b.WriteByte('`')
						j += 2
						continue
					}
					j++
					break
				}
				b.WriteByte(sql[j])
				j++
			}
			toks = append(toks, token{kind: tkIdent, text: sql[i:j], val: b.String()})
			i = j
		case (c == 'x' || c == 'X') && i+1 < n && sql[i+1] == '\'':
			j := strings.IndexByte(sql[i+2:], '\'')
			if j < 0 {
				j = n
			} else {
				j = i + 2 + j + 1
			}
			raw, _ := hex.DecodeString(strings.Trim(sql[i+1:j], "'"))
			toks = append(toks, token{kind: tkString, text: sql[i:j], val: string(raw)})
			i = j
		case c >= '0' && c <= '9' || (c == '.' && i+1 < n && sql[i+1] >= '0' && sql[i+1] <= '9'):
			j := i
			if c == '0' && i+1 < n && (sql[i+1] == 'x' || sql[i+1] == 'X') {
				j += 2
				for j < n && strings.IndexByte("0123456789abcdefABCDEF", sql[j]) >= 0 {
					j++
				}
			} else {
				for j < n && (sql[j] >= '0' && sql[j] <= '9' || sql[j] == '.') {
					j++
				}
				if j < n && (sql[j] == 'e' || sql[j] == 'E') {
					k := j + 1
					if k < n && (sql[k] == '+' || sql[k] == '-') {
						k++
					}
					if k < n && sql[k] >= '0' && sql[k] <= '9' {
						j = k
						for j < n && sql[j] >= '0' && sql[j] <= '9' {
							j++
						}
					}
				}
			}
			if j < n && isWordByte(sql[j]) {
				j = int(C2_sql_scan_word(src, uint64(j), end))
				toks = append(toks, token{kind: tkWord, text: sql[i:j]})
			} else {
				toks = append(toks, token{kind: tkNumber, text: sql[i:j]})
			}
			i = j
		case isWordByte(c):
			j := int(C2_sql_scan_word(src, uint64(i), end))
			toks = append(toks, token{kind: tkWord, text: sql[i:j]})
			i = j
		case c == '@':
			j := i + 1
			if j < n && sql[j] == '@' {
				j++
			}
			for j < n && (isWordByte(sql[j]) || sql[j] == '.') {
				j++
			}
			if j < n && j == i+1 && (sql[j] == '\'' || sql[j] == '"' || sql[j] == '`') {
				val, k := lexQuoted(sql, j)
				if sql[j] == '`' {
					k = strings.IndexByte(sql[j+1:], '`')
					if k < 0 {
						k = n
					} else {
						k = j + 1 + k + 1
					}
					val = strings.Trim(sql[j:k], "`")
				}
				toks = append(toks, token{kind: tkVar, text: sql[i:k], val: "@" + val})
				i = k
				continue
			}
			toks = append(toks, token{kind: tkVar, text: sql[i:j], val: sql[i:j]})
			i = j
		case c == '?':
			toks = append(toks, token{kind: tkParam, text: "?"})
			i++
		default:
			op := string(c)
			for _, cand := range []string{"<=>", "<>", "!=", "<=", ">=", "||", "&&", ":=", "<<", ">>", "->>", "->"} {
				if strings.HasPrefix(sql[i:], cand) {
					op = cand
					break
				}
			}
			toks = append(toks, token{kind: tkPunct, text: op})
			i += len(op)
		}
	}
	return toks
}

// lexQuoted décode une chaîne MySQL entre apostrophes ou guillemets, échappements « \ » compris.
func lexQuoted(sql string, i int) (string, int) {
	q := sql[i]
	var b strings.Builder
	j := i + 1
	n := len(sql)
	for j < n {
		c := sql[j]
		if c == '\\' && j+1 < n {
			e := sql[j+1]
			switch e {
			case '0':
				b.WriteByte(0)
			case 'b':
				b.WriteByte('\b')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'Z':
				b.WriteByte(0x1a)
			case '%', '_':
				b.WriteByte('\\')
				b.WriteByte(e)
			default:
				b.WriteByte(e)
			}
			j += 2
			continue
		}
		if c == q {
			if j+1 < n && sql[j+1] == q {
				b.WriteByte(q)
				j += 2
				continue
			}
			return b.String(), j + 1
		}
		b.WriteByte(c)
		j++
	}
	return b.String(), n
}

// sqliteQuote produit un littéral chaîne SQLite ; un octet NUL impose la forme hexadécimale.
func sqliteQuote(s string) string {
	if strings.IndexByte(s, 0) >= 0 {
		return "CAST(X'" + hex.EncodeToString([]byte(s)) + "' AS TEXT)"
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// quoteIdent produit un identifiant SQLite entre guillemets.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// sigIndex retourne l'indice du prochain jeton significatif à partir de i, ou len(toks).
func sigIndex(toks []token, i int) int {
	for i < len(toks) && !toks[i].sig() {
		i++
	}
	return i
}

// sigTokens retourne les jetons significatifs.
func sigTokens(toks []token) []token {
	out := make([]token, 0, len(toks))
	for _, t := range toks {
		if t.sig() {
			out = append(out, t)
		}
	}
	return out
}

// matchParen retourne l'indice de la parenthèse fermante associée à l'ouvrante en i, ou len(toks)
// si elle manque : toks[i+1:k] reste alors une tranche valide et k < len(toks) teste la fermeture.
func matchParen(toks []token, i int) int {
	depth := 0
	for j := i; j < len(toks); j++ {
		if toks[j].punct("(") {
			depth++
		} else if toks[j].punct(")") {
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return len(toks)
}

// splitTop découpe une suite de jetons sur un séparateur de niveau zéro.
func splitTop(toks []token, sep string) [][]token {
	var parts [][]token
	depth := 0
	start := 0
	for i, t := range toks {
		switch {
		case t.punct("("):
			depth++
		case t.punct(")"):
			depth--
		case depth == 0 && t.punct(sep):
			parts = append(parts, toks[start:i])
			start = i + 1
		}
	}
	return append(parts, toks[start:])
}

// splitStatements découpe une requête multiple sur les « ; » de niveau zéro et retire les instructions vides.
func splitStatements(toks []token) [][]token {
	var out [][]token
	for _, part := range splitTop(toks, ";") {
		if sigIndex(part, 0) < len(part) {
			out = append(out, part)
		}
	}
	return out
}

func joinTokens(toks []token) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteString(t.text)
	}
	return b.String()
}

func countParams(toks []token) int {
	n := 0
	for _, t := range toks {
		if t.kind == tkParam {
			n++
		}
	}
	return n
}

// mysqlLike évalue un motif LIKE MySQL (insensible à la casse, « \ » comme échappement).
func mysqlLike(pattern, s string) bool {
	p := []rune(strings.ToLower(pattern))
	r := []rune(strings.ToLower(s))
	var match func(pi, si int) bool
	match = func(pi, si int) bool {
		for pi < len(p) {
			switch p[pi] {
			case '%':
				for pi < len(p) && p[pi] == '%' {
					pi++
				}
				if pi == len(p) {
					return true
				}
				for k := si; k <= len(r); k++ {
					if match(pi, k) {
						return true
					}
				}
				return false
			case '_':
				if si >= len(r) {
					return false
				}
				pi++
				si++
			case '\\':
				if pi+1 < len(p) {
					pi++
				}
				fallthrough
			default:
				if si >= len(r) || r[si] != p[pi] {
					return false
				}
				pi++
				si++
			}
		}
		return si == len(r)
	}
	return match(0, 0)
}
