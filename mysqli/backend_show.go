package mysqli

import (
	"slices"
	"strconv"
	"strings"
)

// showFilter lit une clause « LIKE 'motif' » ou « WHERE expr » finale.
func showFilter(ss []token, k int) (like string, where []token) {
	for ; k < len(ss); k++ {
		if ss[k].is("LIKE") && k+1 < len(ss) {
			return ss[k+1].val, nil
		}
		if ss[k].is("WHERE") {
			return "", ss[k+1:]
		}
	}
	return "", nil
}

// execShow sert les commandes SHOW utilisées par wpdb, dbDelta et l'outillage d'administration.
func (b *sqliteBackend) execShow(toks []token) (execResult, *sqlError) {
	ss := sigTokens(toks)
	k := 1
	full := false
	for k < len(ss) && (ss[k].is("FULL") || ss[k].is("GLOBAL") || ss[k].is("SESSION") || ss[k].is("EXTENDED")) {
		if ss[k].is("FULL") {
			full = true
		}
		k++
	}
	if k >= len(ss) {
		return execResult{}, newSQLError(1064, "42000", "You have an error in your SQL syntax near 'SHOW'")
	}
	word := strings.ToUpper(ss[k].text)
	switch word {
	case "TABLES":
		return b.showTables(ss, k+1, full)
	case "COLUMNS", "FIELDS":
		if k+2 >= len(ss) {
			break
		}
		like, _ := showFilter(ss, k+3)
		return b.showColumns(ss[k+2].name(), full, like)
	case "INDEX", "INDEXES", "KEYS":
		if k+2 >= len(ss) {
			break
		}
		return b.showIndex(ss[k+2].name())
	case "VARIABLES":
		like, _ := showFilter(ss, k+1)
		var rows [][]cell
		for _, name := range sortedKeys(b.vars) {
			if like == "" || mysqlLike(like, name) {
				rows = append(rows, []cell{textCell(name), textCell(b.vars[name])})
			}
		}
		return execResult{rs: textResult([]string{"Variable_name", "Value"}, rows)}, nil
	case "STATUS":
		return execResult{rs: textResult([]string{"Variable_name", "Value"}, nil)}, nil
	case "WARNINGS", "ERRORS":
		return execResult{rs: textResult([]string{"Level", "Code", "Message"}, nil)}, nil
	case "DATABASES", "SCHEMAS":
		return execResult{rs: textResult([]string{"Database"}, [][]cell{{textCell(b.schemaName())}})}, nil
	case "CREATE":
		if k+2 < len(ss) && ss[k+1].is("TABLE") {
			table := ss[k+2].name()
			_, rows, err := b.run("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", []cell{textCell(table)})
			if err != nil {
				return execResult{}, err
			}
			if len(rows) == 0 {
				return execResult{}, newSQLError(1146, "42S02", "Table '"+b.schemaName()+"."+table+"' doesn't exist")
			}
			return execResult{rs: textResult([]string{"Table", "Create Table"}, [][]cell{{textCell(table), rows[0][0]}})}, nil
		}
	case "TABLE":
		if k+1 < len(ss) && ss[k+1].is("STATUS") {
			like, _ := showFilter(ss, k+2)
			return b.showTableStatus(like)
		}
	}
	return execResult{}, newSQLError(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '"+joinTokens(spaced(ss[k:]))+"' at line 1 (unsupported by the SQLite translator)")
}

func (b *sqliteBackend) showTables(ss []token, k int, full bool) (execResult, *sqlError) {
	if k+1 < len(ss) && (ss[k].is("FROM") || ss[k].is("IN")) {
		k += 2
	}
	like, where := showFilter(ss, k)
	col := quoteIdent("Tables_in_" + b.schemaName())
	sql := "SELECT name AS " + col
	if full {
		sql += ", CASE type WHEN 'view' THEN 'VIEW' ELSE 'BASE TABLE' END AS \"Table_type\""
	}
	sql += ` FROM sqlite_master WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite\_%' ESCAPE '\'`
	var params []cell
	if like != "" {
		sql += ` AND name LIKE ? ESCAPE '\'`
		params = append(params, textCell(like))
	}
	if where != nil {
		tr := b.translator()
		sql = "SELECT * FROM (" + sql + ") WHERE " + tr.expr(spaced(where))
		if tr.err != nil {
			return execResult{}, tr.err
		}
	}
	sql += " ORDER BY 1"
	cols, rows, err := b.run(sql, params)
	if err != nil {
		return execResult{}, err
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.name
	}
	return execResult{rs: textResult(names, rows), affected: int64(len(rows))}, nil
}

type tableColumn struct {
	name    string
	decl    string
	notnull bool
	deflt   cell
	pk      int64
}

func (b *sqliteBackend) tableInfo(table string) (sql string, cols []tableColumn, err *sqlError) {
	_, rows, err := b.run("SELECT sql FROM sqlite_master WHERE type IN ('table', 'view') AND name = ?", []cell{textCell(table)})
	if err != nil {
		return "", nil, err
	}
	if len(rows) == 0 {
		return "", nil, newSQLError(1146, "42S02", "Table '"+b.schemaName()+"."+table+"' doesn't exist")
	}
	sql = rows[0][0].s
	_, info, err := b.run("PRAGMA table_info("+quoteIdent(table)+")", nil)
	if err != nil {
		return "", nil, err
	}
	for _, r := range info {
		cols = append(cols, tableColumn{name: r[1].s, decl: r[2].s, notnull: r[3].i != 0, deflt: r[4], pk: r[5].i})
	}
	return sql, cols, nil
}

type indexInfo struct {
	name   string
	unique bool
	origin string
	cols   []string
}

func (b *sqliteBackend) indexList(table string) ([]indexInfo, *sqlError) {
	_, list, err := b.run("PRAGMA index_list("+quoteIdent(table)+")", nil)
	if err != nil {
		return nil, err
	}
	out := make([]indexInfo, 0, len(list))
	for _, r := range slices.Backward(list) {
		idx := indexInfo{name: r[1].s, unique: r[2].i != 0, origin: r[3].s}
		_, cols, err := b.run("PRAGMA index_info("+quoteIdent(idx.name)+")", nil)
		if err != nil {
			return nil, err
		}
		for _, c := range cols {
			idx.cols = append(idx.cols, c[2].s)
		}
		out = append(out, idx)
	}
	return out, nil
}

// unquoteDefault restitue la valeur par défaut affichée par MySQL à partir du littéral SQLite.
func unquoteDefault(c cell) cell {
	if c.kind == cellNull {
		return c
	}
	s := c.text()
	if strings.EqualFold(s, "NULL") {
		return nullCell
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return textCell(strings.ReplaceAll(s[1:len(s)-1], "''", "'"))
	}
	return textCell(s)
}

// showColumns produit SHOW [FULL] COLUMNS / DESCRIBE au format MySQL.
func (b *sqliteBackend) showColumns(table string, full bool, like string) (execResult, *sqlError) {
	sql, cols, err := b.tableInfo(table)
	if err != nil {
		return execResult{}, err
	}
	markers := columnMarkers(sql)
	indexes, err := b.indexList(table)
	if err != nil {
		return execResult{}, err
	}
	autoinc := strings.Contains(strings.ToUpper(sql), "AUTOINCREMENT")
	keyOf := func(c tableColumn) string {
		if c.pk > 0 {
			return "PRI"
		}
		key := ""
		for _, idx := range indexes {
			if len(idx.cols) == 0 || !strings.EqualFold(idx.cols[0], c.name) {
				continue
			}
			switch {
			case idx.origin == "pk" || strings.HasSuffix(idx.name, "__PRIMARY"):
				return "PRI"
			case idx.unique && len(idx.cols) == 1:
				key = "UNI"
			case key == "":
				key = "MUL"
			}
		}
		return key
	}
	names := []string{"Field", "Type", "Null", "Key", "Default", "Extra"}
	if full {
		names = []string{"Field", "Type", "Collation", "Null", "Key", "Default", "Extra", "Privileges", "Comment"}
	}
	var rows [][]cell
	for _, c := range cols {
		if like != "" && !mysqlLike(like, c.name) {
			continue
		}
		typ := mysqlTypeFromDecl(c.decl)
		coll := nullCell
		if m, ok := markers[strings.ToLower(c.name)]; ok {
			typ = m[0]
			if m[1] != "" {
				coll = textCell(m[1])
			}
		}
		null := "YES"
		if c.notnull || c.pk > 0 {
			null = "NO"
		}
		extra := ""
		if autoinc && c.pk > 0 && strings.EqualFold(c.decl, "INTEGER") {
			extra = "auto_increment"
		}
		row := []cell{textCell(c.name), textCell(typ)}
		if full {
			row = append(row, coll)
		}
		row = append(row, textCell(null), textCell(keyOf(c)), unquoteDefault(c.deflt), textCell(extra))
		if full {
			row = append(row, textCell("select,insert,update,references"), textCell(""))
		}
		rows = append(rows, row)
	}
	return execResult{rs: textResult(names, rows), affected: int64(len(rows))}, nil
}

// showIndex produit SHOW INDEX FROM table au format MySQL.
func (b *sqliteBackend) showIndex(table string) (execResult, *sqlError) {
	_, cols, err := b.tableInfo(table)
	if err != nil {
		return execResult{}, err
	}
	indexes, err := b.indexList(table)
	if err != nil {
		return execResult{}, err
	}
	nullable := make(map[string]bool)
	var pk []tableColumn
	for _, c := range cols {
		nullable[strings.ToLower(c.name)] = !c.notnull && c.pk == 0
		if c.pk > 0 {
			pk = append(pk, c)
		}
	}
	names := []string{"Table", "Non_unique", "Key_name", "Seq_in_index", "Column_name", "Collation", "Cardinality", "Sub_part", "Packed", "Null", "Index_type", "Comment", "Index_comment", "Visible", "Expression"}
	var rows [][]cell
	add := func(key string, unique bool, seq int, col string) {
		nonUnique := "1"
		if unique {
			nonUnique = "0"
		}
		null := ""
		if nullable[strings.ToLower(col)] {
			null = "YES"
		}
		rows = append(rows, []cell{textCell(table), textCell(nonUnique), textCell(key), textCell(strconv.Itoa(seq)), textCell(col),
			textCell("A"), nullCell, nullCell, nullCell, textCell(null), textCell("BTREE"), textCell(""), textCell(""), textCell("YES"), nullCell})
	}
	for len(pk) > 1 {
		sorted := true
		for i := 1; i < len(pk); i++ {
			if pk[i-1].pk > pk[i].pk {
				pk[i-1], pk[i] = pk[i], pk[i-1]
				sorted = false
			}
		}
		if sorted {
			break
		}
	}
	for i, c := range pk {
		add("PRIMARY", true, i+1, c.name)
	}
	for _, idx := range indexes {
		if idx.origin == "pk" {
			continue
		}
		key := mysqlIndexName(table, idx.name)
		if idx.origin == "u" && len(idx.cols) > 0 {
			key = idx.cols[0]
		}
		for i, c := range idx.cols {
			add(key, idx.unique, i+1, c)
		}
	}
	return execResult{rs: textResult(names, rows), affected: int64(len(rows))}, nil
}

// showTableStatus produit SHOW TABLE STATUS (nom, moteur, lignes, AUTO_INCREMENT, collation).
func (b *sqliteBackend) showTableStatus(like string) (execResult, *sqlError) {
	_, tables, err := b.run(`SELECT name, sql FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`, nil)
	if err != nil {
		return execResult{}, err
	}
	hasSeq := false
	if _, r, err := b.run("SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'sqlite_sequence'", nil); err == nil && len(r) > 0 {
		hasSeq = true
	}
	names := []string{"Name", "Engine", "Version", "Row_format", "Rows", "Avg_row_length", "Data_length", "Max_data_length", "Index_length", "Data_free", "Auto_increment", "Create_time", "Update_time", "Check_time", "Collation", "Checksum", "Create_options", "Comment"}
	var rows [][]cell
	for _, t := range tables {
		name := t[0].s
		if like != "" && !mysqlLike(like, name) {
			continue
		}
		_, cnt, err := b.run("SELECT COUNT(*) FROM "+quoteIdent(name), nil)
		if err != nil {
			return execResult{}, err
		}
		auto := nullCell
		if hasSeq && strings.Contains(strings.ToUpper(t[1].s), "AUTOINCREMENT") {
			auto = intCell(1)
			if _, seq, err := b.run("SELECT seq FROM sqlite_sequence WHERE name = ?", []cell{textCell(name)}); err == nil && len(seq) > 0 {
				auto = intCell(seq[0][0].i + 1)
			}
		}
		coll := "utf8mb4_0900_ai_ci"
		for _, m := range columnMarkers(t[1].s) {
			if m[1] != "" {
				coll = m[1]
				break
			}
		}
		rows = append(rows, []cell{textCell(name), textCell("SQLite"), textCell("10"), textCell("Dynamic"), cnt[0][0], intCell(0), intCell(0), intCell(0), intCell(0), intCell(0),
			auto, nullCell, nullCell, nullCell, textCell(coll), nullCell, textCell(""), textCell("")})
	}
	return execResult{rs: textResult(names, rows), affected: int64(len(rows))}, nil
}

// sqliteStmtBackend est une requête préparée MySQL exécutée par le moteur SQLite.
type sqliteStmtBackend struct {
	b       *sqliteBackend
	toks    []token
	nparams int
	flds    []field
}

func (b *sqliteBackend) prepare(sql string) (stmtBackend, *sqlError) {
	if b.db == nil {
		return nil, newSQLError(crServerGone, "HY000", "MySQL server has gone away")
	}
	stmts := splitStatements(lexSQL(sql))
	if len(stmts) == 0 {
		return nil, newSQLError(1065, "42000", "Query was empty")
	}
	if len(stmts) > 1 {
		return nil, newSQLError(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '"+strings.TrimSpace(joinTokens(stmts[1]))+"' at line 1")
	}
	toks := stmts[0]
	if err := statementHeadError(toks); err != nil {
		return nil, err
	}
	st := &sqliteStmtBackend{b: b, toks: toks, nparams: countParams(toks)}
	// Validation à la préparation : syntaxe, tables et colonnes, comme le serveur MySQL.
	tr := b.translator()
	w := firstWords(toks, 1)[0]
	check := ""
	switch w {
	case "SELECT", "WITH", "VALUES", "(":
		check = tr.expr(toks)
	case "INSERT", "REPLACE":
		check, _, _ = tr.translateInsert(toks)
	case "UPDATE":
		check = tr.translateUpdate(toks)
	case "DELETE":
		check, _, _ = tr.translateDelete(toks)
	}
	if tr.err != nil {
		return nil, tr.err
	}
	if check != "" {
		ps, _, rc, msg := b.db.prepare(check)
		if rc != sqliteOK {
			return nil, b.mapError(rc, msg, check)
		}
		st.flds = b.buildFields(ps.columns(), nil)
		ps.finalize()
	}
	return st, nil
}

func (s *sqliteStmtBackend) paramCount() int { return s.nparams }
func (s *sqliteStmtBackend) fields() []field { return s.flds }
func (s *sqliteStmtBackend) close()          { s.toks = nil }

func (s *sqliteStmtBackend) execute(params []cell) (execResult, *sqlError) {
	if s.toks == nil {
		return execResult{}, newSQLError(crNoPrepareStmt, "HY000", "Statement not prepared")
	}
	if len(params) != s.nparams {
		return execResult{}, newSQLError(crParamsNotBound, "HY000", "No data supplied for parameters in prepared statement")
	}
	res, err := s.b.execStatement(s.toks, params)
	if err != nil {
		return res, err
	}
	if res.rs != nil {
		res.rs.binary = true
	}
	return res, nil
}
