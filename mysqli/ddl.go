package mysqli

import (
	"strings"
)

// Marqueur de commentaire porté par chaque colonne traduite : type et collation MySQL d'origine.
const mysqlMarker = "/*mysql:"

// indexName préfixe un nom d'index MySQL par sa table : les index SQLite partagent un espace de noms global.
func indexName(table, name string) string {
	return table + "__" + name
}

// mysqlIndexName retrouve le nom MySQL d'un index SQLite préfixé.
func mysqlIndexName(table, sqliteName string) string {
	if rest, ok := strings.CutPrefix(sqliteName, table+"__"); ok {
		return rest
	}
	if _, after, ok := strings.Cut(sqliteName, "__"); ok {
		return after
	}
	return sqliteName
}

// tableOptions lit le jeu de caractères et la collation par défaut d'une définition de table.
func tableOptions(opts []token) (charset, collation string) {
	for i := 0; i < len(opts); i++ {
		t := opts[i]
		next := func() string {
			j := i + 1
			if j < len(opts) && opts[j].punct("=") {
				j++
			}
			if j < len(opts) {
				i = j
				return strings.ToLower(opts[j].name())
			}
			return ""
		}
		switch {
		case t.is("COLLATE"):
			collation = next()
		case t.is("CHARSET"):
			charset = next()
		case t.is("CHARACTER") && i+1 < len(opts) && opts[i+1].is("SET"):
			i++
			charset = next()
		}
	}
	if collation == "" {
		switch charset {
		case "", "utf8mb4":
			collation = "utf8mb4_0900_ai_ci"
		case "binary":
			collation = "binary"
		default:
			collation = charset + "_general_ci"
		}
	}
	if charset == "" {
		charset, _, _ = strings.Cut(collation, "_")
	}
	return charset, collation
}

func isCaseInsensitive(collation string) bool {
	return strings.HasSuffix(collation, "_ci")
}

func isTextType(base string) bool {
	switch base {
	case "char", "varchar", "tinytext", "text", "mediumtext", "longtext", "enum", "set":
		return true
	}
	return false
}

// columnSQL est la traduction d'une définition de colonne.
type columnSQL struct {
	name    string
	sql     string
	autoInc bool
	pk      bool
	unique  bool
}

// translateColumn traduit « nom type attributs » ; tableColl fixe la collation par défaut.
func (tr *translator) translateColumn(it []token, tableColl string) columnSQL {
	col := columnSQL{name: it[0].name()}
	if len(it) < 2 {
		tr.fail(1064, "42000", "You have an error in your SQL syntax near '"+col.name+"'")
		return col
	}
	k := 1
	base := strings.ToLower(it[k].text)
	k++
	if base == "double" && k < len(it) && it[k].is("PRECISION") {
		k++
	}
	if base == "character" && k < len(it) && it[k].is("VARYING") {
		base = "varchar"
		k++
	}
	args := ""
	if k < len(it) && it[k].punct("(") {
		end, ok := tr.closeParen(it, k)
		if !ok {
			return col
		}
		parts := make([]string, 0, 4)
		for _, a := range splitTop(it[k+1:end], ",") {
			ss := sigTokens(a)
			if len(ss) == 1 && ss[0].kind == tkString {
				parts = append(parts, sqliteQuote(ss[0].val))
			} else {
				parts = append(parts, strings.ToLower(joinTokens(ss)))
			}
		}
		args = strings.Join(parts, ",")
		k = end + 1
	}
	var mods []string
	for k < len(it) && (it[k].is("UNSIGNED") || it[k].is("SIGNED") || it[k].is("ZEROFILL")) {
		mods = append(mods, strings.ToLower(it[k].text))
		k++
	}
	var mysqlType strings.Builder
	mysqlType.WriteString(base)
	if args != "" {
		mysqlType.WriteString("(" + args + ")")
	}
	for _, m := range mods {
		mysqlType.WriteString(" " + m)
	}
	decl := base
	if base == "enum" || base == "set" {
		decl = "text"
	} else {
		for _, m := range mods {
			decl += " " + m
		}
		if args != "" {
			decl += "(" + args + ")"
		}
	}
	text := isTextType(base)
	collation := ""
	notNull := false
	var deflt, extra []string
	for ; k < len(it); k++ {
		t := it[k]
		switch {
		case t.is("NOT") && k+1 < len(it) && it[k+1].is("NULL"):
			notNull = true
			k++
		case t.is("NULL"):
		case t.is("DEFAULT") && k+1 < len(it):
			clause, end, ok := tr.defaultClause(it, k+1)
			if !ok {
				return col
			}
			deflt = []string{clause}
			k = end
		case t.is("AUTO_INCREMENT"):
			col.autoInc = true
		case t.is("PRIMARY"):
			col.pk = true
			if k+1 < len(it) && it[k+1].is("KEY") {
				k++
			}
		case t.is("KEY"):
			col.pk = true
		case t.is("UNIQUE"):
			col.unique = true
			if k+1 < len(it) && it[k+1].is("KEY") {
				k++
			}
		case t.is("COMMENT") || t.is("COLUMN_FORMAT") || t.is("STORAGE"):
			k++
		case t.is("COLLATE"):
			if k+1 < len(it) {
				collation = strings.ToLower(it[k+1].name())
				k++
			}
		case t.is("CHARSET") || (t.is("CHARACTER") && k+1 < len(it) && it[k+1].is("SET")):
			if t.is("CHARACTER") {
				k++
			}
			if k+1 < len(it) && collation == "" {
				_, collation = tableOptions([]token{{kind: tkWord, text: "CHARSET"}, it[k+1]})
			}
			k++
		case t.is("ON") && k+1 < len(it) && it[k+1].is("UPDATE"):
			k += 2
			if k+1 < len(it) && it[k+1].punct("(") {
				var ok bool
				if k, ok = tr.closeParen(it, k+1); !ok {
					return col
				}
			}
		case t.is("FIRST") || t.is("VISIBLE") || t.is("INVISIBLE"):
		case t.is("AFTER"):
			k++
		case t.is("GENERATED") || t.is("AS"):
			for k < len(it) && !it[k].punct("(") {
				k++
			}
			if k < len(it) {
				end, ok := tr.closeParen(it, k)
				if !ok {
					return col
				}
				gen := "GENERATED ALWAYS AS (" + tr.expr(spaced(it[k+1:end])) + ")"
				k = end
				if k+1 < len(it) && (it[k+1].is("STORED") || it[k+1].is("VIRTUAL")) {
					gen += " " + strings.ToUpper(it[k+1].text)
					k++
				}
				extra = append(extra, gen)
			}
		case t.is("REFERENCES") || t.is("CHECK"):
			extra = append(extra, tr.expr(spaced(it[k:])))
			k = len(it)
		}
	}
	if text && collation == "" {
		collation = tableColl
	}
	if !text {
		collation = ""
	}
	marker := mysqlMarker + strings.ReplaceAll(mysqlType.String(), "*/", "") + "|" + collation + "*/"
	var b strings.Builder
	b.WriteString(quoteIdent(col.name))
	if col.autoInc {
		b.WriteString(" INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL " + marker)
		col.sql = b.String()
		return col
	}
	b.WriteString(" " + decl)
	if notNull {
		b.WriteString(" NOT NULL")
	}
	for _, d := range deflt {
		b.WriteString(" " + d)
	}
	if col.pk {
		b.WriteString(" PRIMARY KEY")
	}
	if col.unique {
		b.WriteString(" UNIQUE")
	}
	if text && isCaseInsensitive(collation) {
		b.WriteString(" COLLATE NOCASE")
	}
	for _, e := range extra {
		b.WriteString(" " + e)
	}
	b.WriteString(" " + marker)
	col.sql = b.String()
	return col
}

// defaultClause traduit la valeur par défaut MySQL qui commence à it[k] en clause SQLite « DEFAULT ... » et
// rend l'indice de son dernier élément ; ok est faux si une parenthèse n'est pas fermée (tr.err est posé).
func (tr *translator) defaultClause(it []token, k int) (clause string, end int, ok bool) {
	v := it[k]
	switch {
	case v.punct("("):
		end, ok := tr.closeParen(it, k)
		if !ok {
			return "", k, false
		}
		return "DEFAULT (" + tr.expr(spaced(it[k+1:end])) + ")", end, true
	case v.is("CURRENT_TIMESTAMP") || v.is("NOW") || v.is("LOCALTIMESTAMP") || v.is("LOCALTIME"):
		if k+1 < len(it) && it[k+1].punct("(") {
			var ok bool
			if k, ok = tr.closeParen(it, k+1); !ok {
				return "", k, false
			}
		}
		return "DEFAULT CURRENT_TIMESTAMP", k, true
	case (v.punct("-") || v.punct("+")) && k+1 < len(it):
		return "DEFAULT " + v.text + it[k+1].text, k + 1, true
	case v.kind == tkString:
		return "DEFAULT " + sqliteQuote(v.val), k, true
	case (v.is("b") || v.is("B")) && k+1 < len(it) && it[k+1].kind == tkString:
		n := int64(0)
		for _, c := range it[k+1].val {
			n = n*2 + int64(c-'0')
		}
		return "DEFAULT " + cellLiteral(intCell(n)), k + 1, true
	case v.is("TRUE"):
		return "DEFAULT 1", k, true
	case v.is("FALSE"):
		return "DEFAULT 0", k, true
	}
	return "DEFAULT " + v.text, k, true
}

// indexDef est un index déclaré dans une définition de table ou par ALTER TABLE.
type indexDef struct {
	name   string
	unique bool
	cols   []string
	plain  []string
}

// parseIndex lit « [nom] [USING x] (col[(n)] [ASC|DESC], ...) » à partir de k ; une liste non refermée
// consigne l'erreur de syntaxe.
func (tr *translator) parseIndex(it []token, k int) (indexDef, bool) {
	var idx indexDef
	for k < len(it) && !it[k].punct("(") {
		if !it[k].is("USING") && !it[k].is("BTREE") && !it[k].is("HASH") && !it[k].is("INDEX") && !it[k].is("KEY") {
			idx.name = it[k].name()
		}
		k++
	}
	if k >= len(it) {
		return idx, false
	}
	end, ok := tr.closeParen(it, k)
	if !ok {
		return idx, false
	}
	for _, part := range splitTop(it[k+1:end], ",") {
		ss := sigTokens(part)
		if len(ss) == 0 {
			continue
		}
		name := ss[0].name()
		col := quoteIdent(name)
		last := ss[len(ss)-1]
		if last.is("DESC") || last.is("ASC") {
			col += " " + strings.ToUpper(last.text)
		}
		idx.cols = append(idx.cols, col)
		idx.plain = append(idx.plain, name)
	}
	return idx, len(idx.cols) > 0
}

func createIndexSQL(table string, idx indexDef, ifNotExists bool) string {
	var b strings.Builder
	b.WriteString("CREATE ")
	if idx.unique {
		b.WriteString("UNIQUE ")
	}
	b.WriteString("INDEX ")
	if ifNotExists {
		b.WriteString("IF NOT EXISTS ")
	}
	b.WriteString(quoteIdent(indexName(table, idx.name)) + " ON " + quoteIdent(table) + " (" + strings.Join(idx.cols, ", ") + ")")
	return b.String()
}

// translateCreateTable traduit CREATE TABLE en une instruction de table suivie de ses index.
func (tr *translator) translateCreateTable(toks []token) []string {
	ss := sigTokens(toks)
	k := 1
	temp := false
	if k < len(ss) && ss[k].is("TEMPORARY") {
		temp = true
		k++
	}
	k++ // TABLE
	ifNot := false
	if k+2 < len(ss) && ss[k].is("IF") && ss[k+1].is("NOT") && ss[k+2].is("EXISTS") {
		ifNot = true
		k += 3
	}
	if k >= len(ss) {
		tr.fail(1064, "42000", "You have an error in your SQL syntax near 'CREATE TABLE'")
		return nil
	}
	table := ss[k].name()
	k++
	head := "CREATE "
	if temp {
		head += "TEMPORARY "
	}
	head += "TABLE "
	if ifNot {
		head += "IF NOT EXISTS "
	}
	head += quoteIdent(table)
	if k >= len(ss) || !ss[k].punct("(") {
		if k < len(ss) && ss[k].is("LIKE") {
			tr.fail(1235, "42000", "This version of MySQL doesn't yet support 'CREATE TABLE ... LIKE' (SQLite translator)")
			return nil
		}
		rest := ss[k:]
		if len(rest) > 0 && rest[0].is("AS") {
			rest = rest[1:]
		}
		return []string{head + " AS " + tr.expr(spaced(rest))}
	}
	end := matchParen(ss, k)
	if end >= len(ss) {
		tr.unclosedParen()
		return nil
	}
	_, tableColl := tableOptions(ss[end+1:])
	var cols []columnSQL
	var pk []string
	var constraints []string
	var indexes []indexDef
	for _, it := range splitTop(ss[k+1:end], ",") {
		if len(it) == 0 {
			continue
		}
		if it[0].is("CONSTRAINT") {
			j := 1
			if j < len(it) && !it[j].is("PRIMARY") && !it[j].is("UNIQUE") && !it[j].is("FOREIGN") && !it[j].is("CHECK") {
				j++
			}
			it = it[j:]
			if len(it) == 0 {
				continue
			}
		}
		switch {
		case it[0].is("PRIMARY"):
			if idx, ok := tr.parseIndex(it, 2); ok {
				pk = idx.plain
				if len(pk) == 0 {
					continue
				}
				indexes = append(indexes, indexDef{name: "PRIMARY", unique: true, cols: idx.cols, plain: idx.plain})
			}
		case it[0].is("UNIQUE"):
			if idx, ok := tr.parseIndex(it, 1); ok {
				idx.unique = true
				indexes = append(indexes, idx)
			}
		case it[0].is("KEY") || it[0].is("INDEX"):
			if idx, ok := tr.parseIndex(it, 1); ok {
				indexes = append(indexes, idx)
			}
		case it[0].is("FULLTEXT") || it[0].is("SPATIAL"):
		case it[0].is("FOREIGN") || it[0].is("CHECK"):
			constraints = append(constraints, tr.expr(spaced(it)))
		default:
			cols = append(cols, tr.translateColumn(it, tableColl))
		}
	}
	autoCol := ""
	for _, c := range cols {
		if c.autoInc {
			autoCol = c.name
		}
	}
	parts := make([]string, 0, len(cols)+2)
	for _, c := range cols {
		parts = append(parts, c.sql)
	}
	var post []string
	used := make(map[string]bool)
	for _, idx := range indexes {
		if idx.name == "PRIMARY" {
			if autoCol != "" && len(idx.plain) == 1 && strings.EqualFold(idx.plain[0], autoCol) {
				continue
			}
			if autoCol == "" {
				parts = append(parts, "PRIMARY KEY ("+strings.Join(idx.cols, ", ")+")")
				continue
			}
		}
		if idx.name == "" {
			idx.name = idx.plain[0]
		}
		base := idx.name
		for n := 2; used[strings.ToLower(idx.name)]; n++ {
			idx.name = base + "_" + itoa(n)
		}
		used[strings.ToLower(idx.name)] = true
		post = append(post, createIndexSQL(table, idx, ifNot))
	}
	parts = append(parts, constraints...)
	out := []string{head + " (\n  " + strings.Join(parts, ",\n  ") + "\n)"}
	return append(out, post...)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// translateCreateIndex traduit CREATE [UNIQUE|FULLTEXT|SPATIAL] INDEX nom ON table (cols).
func (tr *translator) translateCreateIndex(toks []token) []string {
	ss := sigTokens(toks)
	k := 1
	unique := false
	switch {
	case ss[k].is("UNIQUE"):
		unique = true
		k++
	case ss[k].is("FULLTEXT") || ss[k].is("SPATIAL"):
		return nil
	}
	k++ // INDEX
	ifNot := false
	if k+2 < len(ss) && ss[k].is("IF") && ss[k+1].is("NOT") && ss[k+2].is("EXISTS") {
		ifNot = true
		k += 3
	}
	on := indexTop(ss, "ON")
	if on < 0 || on+1 >= len(ss) || k >= len(ss) {
		tr.fail(1064, "42000", "You have an error in your SQL syntax near 'CREATE INDEX'")
		return nil
	}
	name := ss[k].name()
	table := ss[on+1].name()
	idx, ok := tr.parseIndex(ss, on+2)
	if !ok {
		tr.fail(1064, "42000", "You have an error in your SQL syntax near 'CREATE INDEX'")
		return nil
	}
	idx.name = name
	idx.unique = unique
	return []string{createIndexSQL(table, idx, ifNot)}
}

// alterStep est une étape d'ALTER TABLE : une instruction SQLite directe, ou la redéfinition d'une colonne
// que SQLite n'exprime pas et que le moteur applique par reconstruction de la table.
type alterStep struct {
	sql   string
	redef *columnRedef
}

// columnRedef est la nouvelle définition MySQL d'une colonne existante (MODIFY, CHANGE) ; def commence par
// le nom de la colonne après un éventuel renommage. Pour ALTER [COLUMN] col {SET | DROP} DEFAULT,
// alterDefaultOnly est vrai, def est vide, column nomme la colonne et deflt porte la clause SQLite
// « DEFAULT ... » à substituer (vide : la clause est retirée).
type columnRedef struct {
	table            string
	def              []token
	alterDefaultOnly bool
	column           string
	deflt            string
}

// redefineColumn valide la définition MySQL de MODIFY ou CHANGE et la rend comme étape de reconstruction ;
// le moteur retraduit def avec la collation de la colonne existante.
func (tr *translator) redefineColumn(table string, def []token) (alterStep, bool) {
	if tr.translateColumn(def, "utf8mb4_0900_ai_ci"); tr.err != nil {
		return alterStep{}, false
	}
	return alterStep{redef: &columnRedef{table: table, def: def}}, true
}

// translateAlterTable traduit ALTER TABLE en étapes SQLite ; warnings compte les clauses ignorées.
func (tr *translator) translateAlterTable(toks []token) (steps []alterStep, warnings int) {
	ss := sigTokens(toks)
	k := 1
	for k < len(ss) && (ss[k].is("ONLINE") || ss[k].is("IGNORE")) {
		k++
	}
	k++ // TABLE
	if k >= len(ss) {
		tr.fail(1064, "42000", "You have an error in your SQL syntax near 'ALTER TABLE'")
		return nil, 0
	}
	table := ss[k].name()
	qt := quoteIdent(table)
	for _, spec := range splitTop(ss[k+1:], ",") {
		if len(spec) == 0 {
			continue
		}
		w := func(i int, word string) bool { return i < len(spec) && spec[i].is(word) }
		switch {
		case w(0, "ADD"):
			j := 1
			switch {
			case w(j, "INDEX") || w(j, "KEY"):
				if idx, ok := tr.parseIndex(spec, j+1); ok {
					if idx.name == "" {
						idx.name = idx.plain[0]
					}
					steps = append(steps, alterStep{sql: createIndexSQL(table, idx, false)})
				}
			case w(j, "UNIQUE") || w(j, "PRIMARY") || w(j, "CONSTRAINT"):
				if w(j, "CONSTRAINT") {
					j++
					if !w(j, "UNIQUE") && !w(j, "PRIMARY") && !w(j, "FOREIGN") {
						j++
					}
					if w(j, "FOREIGN") {
						tr.fail(1235, "42000", "ALTER TABLE ADD FOREIGN KEY is not supported by the SQLite translator")
						return nil, 0
					}
				}
				primary := w(j, "PRIMARY")
				idx, ok := tr.parseIndex(spec, j+1)
				if !ok {
					tr.fail(1064, "42000", "You have an error in your SQL syntax near 'ADD'")
					return nil, 0
				}
				idx.unique = true
				if primary {
					idx.name = "PRIMARY"
				} else if idx.name == "" {
					idx.name = idx.plain[0]
				}
				steps = append(steps, alterStep{sql: createIndexSQL(table, idx, false)})
			case w(j, "FULLTEXT") || w(j, "SPATIAL"):
			default:
				if w(j, "COLUMN") {
					j++
				}
				defs := [][]token{spec[j:]}
				if j < len(spec) && spec[j].punct("(") {
					end := matchParen(spec, j)
					if end >= len(spec) {
						tr.unclosedParen()
						return nil, 0
					}
					defs = splitTop(spec[j+1:end], ",")
				}
				for _, d := range defs {
					if len(d) == 0 {
						continue
					}
					c := tr.translateColumn(d, "utf8mb4_0900_ai_ci")
					if c.autoInc || c.pk {
						tr.fail(1235, "42000", "ALTER TABLE ADD COLUMN with a key is not supported by the SQLite translator")
						return nil, 0
					}
					steps = append(steps, alterStep{sql: "ALTER TABLE " + qt + " ADD COLUMN " + c.sql})
				}
			}
		case w(0, "DROP"):
			switch {
			case w(1, "INDEX") || w(1, "KEY"):
				if len(spec) > 2 {
					steps = append(steps, alterStep{sql: "DROP INDEX " + quoteIdent(indexName(table, spec[2].name()))})
				}
			case w(1, "PRIMARY"):
				steps = append(steps, alterStep{sql: "DROP INDEX IF EXISTS " + quoteIdent(indexName(table, "PRIMARY"))})
			case w(1, "FOREIGN") || w(1, "CHECK") || w(1, "CONSTRAINT"):
				warnings++
			default:
				j := 1
				if w(j, "COLUMN") {
					j++
				}
				if j < len(spec) {
					steps = append(steps, alterStep{sql: "ALTER TABLE " + qt + " DROP COLUMN " + quoteIdent(spec[j].name())})
				}
			}
		case w(0, "RENAME"):
			switch {
			case w(1, "COLUMN") && len(spec) >= 5:
				steps = append(steps, alterStep{sql: "ALTER TABLE " + qt + " RENAME COLUMN " + quoteIdent(spec[2].name()) + " TO " + quoteIdent(spec[4].name())})
			case w(1, "INDEX") || w(1, "KEY"):
				tr.fail(1235, "42000", "ALTER TABLE RENAME INDEX is not supported by the SQLite translator")
				return nil, 0
			default:
				j := 1
				if w(j, "TO") || w(j, "AS") {
					j++
				}
				if j < len(spec) {
					steps = append(steps, alterStep{sql: "ALTER TABLE " + qt + " RENAME TO " + quoteIdent(spec[j].name())})
				}
			}
		case w(0, "CHANGE"):
			// CHANGE renomme par RENAME COLUMN, qui réécrit aussi index, déclencheurs et vues, puis applique
			// la nouvelle définition sous le nouveau nom.
			j := 1
			if w(j, "COLUMN") {
				j++
			}
			if j+2 >= len(spec) {
				tr.fail(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '"+joinTokens(spaced(spec))+"' at line 1")
				return nil, 0
			}
			oldName, newName := spec[j].name(), spec[j+1].name()
			if oldName != newName {
				steps = append(steps, alterStep{sql: "ALTER TABLE " + qt + " RENAME COLUMN " + quoteIdent(oldName) + " TO " + quoteIdent(newName)})
			}
			step, ok := tr.redefineColumn(table, spec[j+1:])
			if !ok {
				return nil, 0
			}
			steps = append(steps, step)
		case w(0, "MODIFY"):
			j := 1
			if w(j, "COLUMN") {
				j++
			}
			if j+1 >= len(spec) {
				tr.fail(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '"+joinTokens(spaced(spec))+"' at line 1")
				return nil, 0
			}
			step, ok := tr.redefineColumn(table, spec[j:])
			if !ok {
				return nil, 0
			}
			steps = append(steps, step)
		case w(0, "ALTER"):
			// ALTER [COLUMN] col SET DEFAULT valeur | DROP DEFAULT : seule la clause DEFAULT de la colonne
			// change, par reconstruction de la table.
			j := 1
			if w(j, "COLUMN") && j+1 < len(spec) && !w(j+1, "SET") && !w(j+1, "DROP") {
				j++
			}
			redef := &columnRedef{table: table, alterDefaultOnly: true}
			switch {
			case j+3 < len(spec) && w(j+1, "SET") && w(j+2, "DEFAULT"):
				clause, end, ok := tr.defaultClause(spec, j+3)
				if !ok {
					return nil, 0
				}
				if end != len(spec)-1 {
					tr.fail(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '"+joinTokens(spaced(spec[end+1:]))+"' at line 1")
					return nil, 0
				}
				redef.deflt = clause
			case j+3 == len(spec) && w(j+1, "DROP") && w(j+2, "DEFAULT"):
			default:
				tr.fail(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '"+joinTokens(spaced(spec))+"' at line 1")
				return nil, 0
			}
			redef.column = spec[j].name()
			steps = append(steps, alterStep{redef: redef})
		case w(0, "ENGINE") || w(0, "DEFAULT") || w(0, "CHARSET") || w(0, "CHARACTER") || w(0, "COLLATE") || w(0, "CONVERT") || w(0, "AUTO_INCREMENT") || w(0, "COMMENT") || w(0, "ROW_FORMAT") || w(0, "ORDER"):
		default:
			tr.fail(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '"+joinTokens(spaced(spec))+"' at line 1")
			return nil, 0
		}
	}
	return steps, warnings
}

// translateDrop traduit DROP TABLE (plusieurs tables) et DROP INDEX nom ON table.
func (tr *translator) translateDrop(toks []token) []string {
	ss := sigTokens(toks)
	k := 1
	if k < len(ss) && ss[k].is("TEMPORARY") {
		k++
	}
	switch {
	case k < len(ss) && ss[k].is("TABLE"):
		k++
		ifExists := ""
		if k+1 < len(ss) && ss[k].is("IF") && ss[k+1].is("EXISTS") {
			ifExists = "IF EXISTS "
			k += 2
		}
		var out []string
		for _, part := range splitTop(ss[k:], ",") {
			if len(part) == 0 || part[0].is("RESTRICT") || part[0].is("CASCADE") {
				continue
			}
			out = append(out, "DROP TABLE "+ifExists+quoteIdent(part[0].name()))
		}
		return out
	case k < len(ss) && ss[k].is("INDEX"):
		on := indexTop(ss, "ON")
		if on < 0 || on+1 >= len(ss) || k+1 >= len(ss) {
			tr.fail(1064, "42000", "You have an error in your SQL syntax near 'DROP INDEX'")
			return nil
		}
		return []string{"DROP INDEX " + quoteIdent(indexName(ss[on+1].name(), ss[k+1].name()))}
	}
	return []string{tr.expr(toks)}
}
