package mysqli

import (
	"strings"
)

// translator réécrit des jetons MySQL en texte SQLite3.
type translator struct {
	sysvar  func(name string) (string, bool)
	uservar func(name string) cell
	// upsert active la réécriture de VALUES(col) en excluded.col (clause ON DUPLICATE KEY UPDATE).
	upsert bool
	err    *sqlError
}

func (tr *translator) fail(errno int, state, msg string) {
	if tr.err == nil {
		tr.err = newSQLError(errno, state, msg)
	}
}

// unclosedParen consigne l'erreur de syntaxe MySQL d'une parenthèse ouvrante sans fermante.
func (tr *translator) unclosedParen() {
	tr.fail(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '' at line 1")
}

// closeParen rend l'indice de la parenthèse fermante associée à toks[i] ; une parenthèse non refermée
// consigne l'erreur de syntaxe de MySQL et rend faux, au lieu d'être refermée silencieusement.
func (tr *translator) closeParen(toks []token, i int) (int, bool) {
	end := matchParen(toks, i)
	if end >= len(toks) {
		tr.unclosedParen()
		return end, false
	}
	return end, true
}

func prevSig(toks []token, i int) (token, bool) {
	for k := i - 1; k >= 0; k-- {
		if toks[k].sig() {
			return toks[k], true
		}
	}
	return token{}, false
}

// expr traduit une suite de jetons : littéraux, opérateurs, fonctions et modificateurs MySQL.
func (tr *translator) expr(toks []token) string {
	var b strings.Builder
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.kind {
		case tkString:
			b.WriteString(sqliteQuote(t.val))
		case tkIdent:
			b.WriteString(quoteIdent(t.val))
		case tkVar:
			tr.writeVar(&b, toks, i)
		case tkPunct:
			switch t.text {
			case "||":
				b.WriteString(" OR ")
			case "&&":
				b.WriteString(" AND ")
			case "<=>":
				b.WriteString(" IS ")
			default:
				b.WriteString(t.text)
			}
		case tkWord:
			i = tr.word(&b, toks, i)
		default:
			b.WriteString(t.text)
		}
	}
	return b.String()
}

// word traduit le mot en position i et retourne l'indice du dernier jeton consommé.
func (tr *translator) word(b *strings.Builder, toks []token, i int) int {
	t := toks[i]
	up := strings.ToUpper(t.text)
	j := sigIndex(toks, i+1)
	if j < len(toks) && toks[j].punct("(") {
		k := matchParen(toks, j)
		if k >= len(toks) {
			tr.unclosedParen()
			b.WriteString(t.text)
			return i
		}
		if out, ok := tr.function(up, toks[j+1:k]); ok {
			b.WriteString(out)
			return k
		}
	}
	switch up {
	case "RLIKE":
		b.WriteString("REGEXP")
	case "LIKE":
		b.WriteString("LIKE")
		if j >= len(toks) {
			return i
		}
		b.WriteString(joinTokens(toks[i+1 : j]))
		out, next := tr.operand(toks, j)
		b.WriteString(out)
		if n := sigIndex(toks, next); n >= len(toks) || !toks[n].is("ESCAPE") {
			b.WriteString(` ESCAPE '\'`)
		}
		return next - 1
	case "DIV":
		b.WriteString("/")
	case "SQL_CALC_FOUND_ROWS", "SQL_NO_CACHE", "SQL_CACHE", "HIGH_PRIORITY", "SQL_SMALL_RESULT", "SQL_BIG_RESULT", "SQL_BUFFER_RESULT":
	case "DISTINCTROW":
		b.WriteString("DISTINCT")
	case "STRAIGHT_JOIN":
		if p, ok := prevSig(toks, i); ok && p.is("SELECT") {
			return i
		}
		b.WriteString("JOIN")
	case "BINARY":
		// Opérateur de comparaison binaire retiré : SQLite compare octet par octet hors COLLATE NOCASE.
	case "USE", "FORCE", "IGNORE":
		if j < len(toks) && (toks[j].is("INDEX") || toks[j].is("KEY")) {
			k := sigIndex(toks, j+1)
			if k < len(toks) && toks[k].is("FOR") {
				k = sigIndex(toks, k+1)
				k = sigIndex(toks, k+1)
				if k < len(toks) && toks[k].is("BY") {
					k = sigIndex(toks, k+1)
				}
			}
			if k < len(toks) && toks[k].punct("(") {
				end, _ := tr.closeParen(toks, k)
				return end
			}
		}
		b.WriteString(t.text)
	case "FOR":
		if j < len(toks) && (toks[j].is("UPDATE") || toks[j].is("SHARE")) {
			return len(toks) - 1
		}
		b.WriteString(t.text)
	case "LOCK":
		if j < len(toks) && toks[j].is("IN") {
			return len(toks) - 1
		}
		b.WriteString(t.text)
	case "FROM":
		if j < len(toks) && toks[j].is("DUAL") {
			return j
		}
		b.WriteString(t.text)
	case "LOCALTIME", "LOCALTIMESTAMP":
		b.WriteString("datetime('now')")
	default:
		b.WriteString(t.text)
	}
	return i
}

// operand traduit l'opérande primaire commençant en j (littéral, paramètre, groupe ou appel de fonction).
func (tr *translator) operand(toks []token, j int) (string, int) {
	t := toks[j]
	if t.punct("(") {
		k := matchParen(toks, j)
		if k >= len(toks) {
			tr.unclosedParen()
			return "(" + tr.expr(toks[j+1:]), len(toks)
		}
		return "(" + tr.expr(toks[j+1:k]) + ")", k + 1
	}
	if t.is("BINARY") {
		n := sigIndex(toks, j+1)
		if n < len(toks) {
			return tr.operand(toks, n)
		}
	}
	if t.kind == tkWord {
		n := sigIndex(toks, j+1)
		if n < len(toks) && toks[n].punct("(") {
			k := matchParen(toks, n)
			if k >= len(toks) {
				tr.unclosedParen()
				return tr.expr(toks[j:]), len(toks)
			}
			return tr.expr(toks[j : k+1]), k + 1
		}
	}
	return tr.expr(toks[j : j+1]), j + 1
}

func (tr *translator) writeVar(b *strings.Builder, toks []token, i int) {
	t := toks[i]
	if !strings.HasPrefix(t.val, "@@") {
		name := strings.ToLower(strings.TrimPrefix(t.val, "@"))
		v := cell{kind: cellNull}
		if tr.uservar != nil {
			v = tr.uservar(name)
		}
		b.WriteString(cellLiteral(v))
		return
	}
	name := strings.ToLower(strings.TrimPrefix(t.val, "@@"))
	for _, scope := range []string{"session.", "global.", "local."} {
		name = strings.TrimPrefix(name, scope)
	}
	val, ok := "", false
	if tr.sysvar != nil {
		val, ok = tr.sysvar(name)
	}
	if !ok {
		tr.fail(1193, "HY000", "Unknown system variable '"+name+"'")
		b.WriteString("NULL")
		return
	}
	b.WriteString(sqliteQuote(val))
	p, okp := prevSig(toks, i)
	n := sigIndex(toks, i+1)
	if okp && (p.is("SELECT") || p.punct(",")) && (n >= len(toks) || toks[n].punct(",") || toks[n].is("FROM")) {
		b.WriteString(" AS " + quoteIdent(t.text))
	}
}

// cellLiteral produit le littéral SQLite d'une valeur Go.
func cellLiteral(c cell) string {
	switch c.kind {
	case cellNull:
		return "NULL"
	case cellInt, cellUint, cellFloat:
		return c.text()
	default:
		return sqliteQuote(c.s)
	}
}

// function traduit un appel de fonction MySQL sans équivalent direct ; ok vaut false sinon.
func (tr *translator) function(name string, inner []token) (string, bool) {
	empty := sigIndex(inner, 0) >= len(inner)
	switch name {
	case "NOW", "SYSDATE", "LOCALTIME", "LOCALTIMESTAMP", "CURRENT_TIMESTAMP", "UTC_TIMESTAMP":
		return "datetime('now')", true
	case "CURDATE", "CURRENT_DATE", "UTC_DATE":
		return "date('now')", true
	case "CURTIME", "CURRENT_TIME", "UTC_TIME":
		return "time('now')", true
	case "UNIX_TIMESTAMP":
		if empty {
			return "CAST(strftime('%s','now') AS INTEGER)", true
		}
		return "", false
	case "CONCAT":
		args := splitTop(inner, ",")
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = "(" + strings.TrimSpace(tr.expr(a)) + ")"
		}
		return "(" + strings.Join(parts, " || ") + ")", true
	case "IF":
		args := splitTop(inner, ",")
		if len(args) != 3 {
			tr.fail(1582, "42000", "Incorrect parameter count in the call to native function 'IF'")
			return "NULL", true
		}
		return "(CASE WHEN " + strings.TrimSpace(tr.expr(args[0])) + " THEN " + strings.TrimSpace(tr.expr(args[1])) + " ELSE " + strings.TrimSpace(tr.expr(args[2])) + " END)", true
	case "DATE_ADD", "ADDDATE", "DATE_SUB", "SUBDATE":
		args := splitTop(inner, ",")
		if len(args) != 2 {
			return "", false
		}
		sign := ""
		if name == "DATE_SUB" || name == "SUBDATE" {
			sign = "-"
		}
		amount, unit := tr.interval(args[1])
		return "datetime(" + tr.expr(args[0]) + ", printf('%+d " + unit + "', " + sign + "(" + amount + ")))", true
	case "CAST":
		return tr.cast(inner, "AS")
	case "CONVERT":
		ss := sigTokens(inner)
		for k, t := range ss {
			if t.is("USING") {
				return "(" + tr.expr(ss[:k]) + ")", true
			}
		}
		args := splitTop(inner, ",")
		if len(args) == 2 {
			return "CAST(" + tr.expr(args[0]) + " AS " + castType(args[1]) + ")", true
		}
		return "", false
	case "GROUP_CONCAT":
		depth := 0
		for k, t := range inner {
			switch {
			case t.punct("("):
				depth++
			case t.punct(")"):
				depth--
			case depth == 0 && t.is("SEPARATOR"):
				sepIdx := sigIndex(inner, k+1)
				sep := ","
				if sepIdx < len(inner) {
					sep = inner[sepIdx].val
				}
				return "group_concat(" + tr.expr(inner[:k]) + ", " + sqliteQuote(sep) + ")", true
			}
		}
		return "group_concat(" + tr.expr(inner) + ")", true
	case "VALUES":
		if tr.upsert {
			ss := sigTokens(inner)
			if len(ss) == 1 {
				return "excluded." + quoteIdent(ss[0].name()), true
			}
		}
		return "", false
	}
	return "", false
}

// interval lit « INTERVAL expr UNITÉ » et retourne la quantité et l'unité d'un modificateur SQLite.
func (tr *translator) interval(toks []token) (string, string) {
	ss := sigTokens(toks)
	if len(ss) >= 3 && ss[0].is("INTERVAL") {
		unit := strings.ToUpper(ss[len(ss)-1].text)
		amount := tr.expr(ss[1 : len(ss)-1])
		switch unit {
		case "SECOND":
			return amount, "seconds"
		case "MINUTE":
			return amount, "minutes"
		case "HOUR":
			return amount, "hours"
		case "WEEK":
			return "(" + amount + ")*7", "days"
		case "MONTH":
			return amount, "months"
		case "YEAR":
			return amount, "years"
		default:
			return amount, "days"
		}
	}
	return tr.expr(toks), "days"
}

func (tr *translator) cast(inner []token, kw string) (string, bool) {
	depth := 0
	for k, t := range inner {
		switch {
		case t.punct("("):
			depth++
		case t.punct(")"):
			depth--
		case depth == 0 && t.is(kw):
			return "CAST(" + strings.TrimSpace(tr.expr(inner[:k])) + " AS " + castType(inner[k+1:]) + ")", true
		}
	}
	return "", false
}

// castType traduit un type cible de CAST MySQL en type SQLite.
func castType(toks []token) string {
	ss := sigTokens(toks)
	if len(ss) == 0 {
		return "TEXT"
	}
	switch strings.ToUpper(ss[0].text) {
	case "SIGNED", "UNSIGNED", "INT", "INTEGER":
		return "INTEGER"
	case "DECIMAL", "NUMERIC":
		return "NUMERIC"
	case "FLOAT", "DOUBLE", "REAL":
		return "REAL"
	case "BINARY":
		return "BLOB"
	default:
		return "TEXT"
	}
}

// translateInsert traduit INSERT/REPLACE : IGNORE, REPLACE, forme SET et ON DUPLICATE KEY UPDATE.
func (tr *translator) translateInsert(toks []token) (sql string, table string, upsert bool) {
	ss := sigTokens(toks)
	var b strings.Builder
	k := 0
	if ss[0].is("REPLACE") {
		b.WriteString("INSERT OR REPLACE")
	} else {
		b.WriteString("INSERT")
	}
	k = 1
	for k < len(ss) && (ss[k].is("LOW_PRIORITY") || ss[k].is("DELAYED") || ss[k].is("HIGH_PRIORITY") || ss[k].is("IGNORE")) {
		if ss[k].is("IGNORE") {
			b.WriteString(" OR IGNORE")
		}
		k++
	}
	b.WriteString(" INTO ")
	if k < len(ss) && ss[k].is("INTO") {
		k++
	}
	if k < len(ss) {
		table = ss[k].name()
		b.WriteString(quoteIdent(table))
		k++
	}
	rest := ss[k:]
	dup := -1
	for i := 0; i+3 < len(rest); i++ {
		if rest[i].is("ON") && rest[i+1].is("DUPLICATE") && rest[i+2].is("KEY") && rest[i+3].is("UPDATE") {
			dup = i
			break
		}
	}
	head := rest
	var tail []token
	if dup >= 0 {
		head, tail = rest[:dup], rest[dup+4:]
	}
	if len(head) > 0 && head[0].is("SET") {
		cols := make([]string, 0, 8)
		vals := make([]string, 0, 8)
		for _, asg := range splitTop(head[1:], ",") {
			for e, t := range asg {
				if t.punct("=") {
					cols = append(cols, quoteIdent(asg[0].name()))
					vals = append(vals, tr.expr(spaced(asg[e+1:])))
					break
				}
			}
		}
		b.WriteString(" (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(vals, ", ") + ")")
	} else {
		b.WriteString(" " + tr.expr(spaced(head)))
		if dup >= 0 && containsTop(head, "SELECT") && !containsTop(head, "WHERE") {
			b.WriteString(" WHERE true")
		}
	}
	if dup >= 0 {
		tr.upsert = true
		b.WriteString(" ON CONFLICT DO UPDATE SET " + tr.expr(spaced(tail)))
		tr.upsert = false
		upsert = true
	}
	return b.String(), table, upsert
}

// spaced réinsère un espace entre des jetons significatifs, sauf autour des parenthèses, virgules et points.
func spaced(ss []token) []token {
	out := make([]token, 0, 2*len(ss))
	for i, t := range ss {
		if i > 0 {
			prev := ss[i-1]
			glued := prev.punct("(") || prev.punct(".") || t.punct(")") || t.punct(",") || t.punct(".")
			if !glued {
				out = append(out, token{kind: tkSpace, text: " "})
			}
		}
		out = append(out, t)
	}
	return out
}

func containsTop(ss []token, word string) bool {
	depth := 0
	for _, t := range ss {
		switch {
		case t.punct("("):
			depth++
		case t.punct(")"):
			depth--
		case depth == 0 && t.is(word):
			return true
		}
	}
	return false
}

// translateUpdate retire LOW_PRIORITY et traduit IGNORE en OR IGNORE.
func (tr *translator) translateUpdate(toks []token) string {
	ss := sigTokens(toks)
	var b strings.Builder
	b.WriteString("UPDATE")
	k := 1
	for k < len(ss) && (ss[k].is("LOW_PRIORITY") || ss[k].is("IGNORE")) {
		if ss[k].is("IGNORE") {
			b.WriteString(" OR IGNORE")
		}
		k++
	}
	return b.String() + " " + tr.expr(spaced(ss[k:]))
}

// deleteTarget décrit une table visée par un DELETE multi-tables.
type deleteTarget struct {
	alias string
	table string
}

// translateDelete traduit DELETE ; un DELETE multi-tables retourne ses cibles, sa clause FROM et son WHERE.
func (tr *translator) translateDelete(toks []token) (sql string, targets []deleteTarget, fromWhere string) {
	ss := sigTokens(toks)
	k := 1
	for k < len(ss) && (ss[k].is("LOW_PRIORITY") || ss[k].is("QUICK") || ss[k].is("IGNORE")) {
		k++
	}
	if k < len(ss) && ss[k].is("FROM") {
		rest := ss[k+1:]
		if using := indexTop(rest, "USING"); using >= 0 {
			for _, t := range splitTop(rest[:using], ",") {
				if len(t) > 0 {
					targets = append(targets, deleteTarget{alias: strings.TrimSuffix(t[0].name(), ".*")})
				}
			}
			return "", resolveTargets(targets, rest[using+1:]), tr.expr(spaced(rest[using+1:]))
		}
		return "DELETE FROM " + tr.expr(spaced(rest)), nil, ""
	}
	from := indexTop(ss[k:], "FROM")
	if from < 0 {
		return "DELETE " + tr.expr(spaced(ss[k:])), nil, ""
	}
	for _, t := range splitTop(ss[k:k+from], ",") {
		if len(t) > 0 {
			targets = append(targets, deleteTarget{alias: t[0].name()})
		}
	}
	rest := ss[k+from+1:]
	return "", resolveTargets(targets, rest), tr.expr(spaced(rest))
}

func indexTop(ss []token, word string) int {
	depth := 0
	for i, t := range ss {
		switch {
		case t.punct("("):
			depth++
		case t.punct(")"):
			depth--
		case depth == 0 && t.is(word):
			return i
		}
	}
	return -1
}

// resolveTargets associe chaque alias de DELETE multi-tables à sa table dans la clause FROM.
func resolveTargets(targets []deleteTarget, from []token) []deleteTarget {
	for i := range targets {
		targets[i].table = targets[i].alias
		for j := range from {
			if from[j].kind != tkIdent && from[j].kind != tkWord {
				continue
			}
			k := j + 1
			if k < len(from) && from[k].is("AS") {
				k++
			}
			if k < len(from) && (from[k].kind == tkIdent || from[k].kind == tkWord) && strings.EqualFold(from[k].name(), targets[i].alias) {
				targets[i].table = from[j].name()
				break
			}
		}
	}
	return targets
}
