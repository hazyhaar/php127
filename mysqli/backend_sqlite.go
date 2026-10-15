package mysqli

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Identité de serveur annoncée par le moteur SQLite : wpdb::db_version() en extrait « 8.0.36 ».
const (
	sqliteServerVersion    = "8.0.36-php127-SQLite"
	sqliteServerVersionNum = 80036
)

// sqliteBackend exécute le dialecte MySQL sur une base SQLite3 réelle par traduction.
type sqliteBackend struct {
	db           *sqliteDB
	path         string
	dbName       string
	sqlID        uint32
	vars         map[string]string
	userVars     map[string]cell
	foundRows    int64
	lastInsertID int64
	autocommit   bool
	questions    int64
	started      time.Time
	// Caches de métadonnées par nom de table replié en ASCII minuscule, vidés après toute instruction DDL
	// et toute annulation de transaction.
	autoIncCache map[string]bool
	colMetaCache map[string]map[string]colMeta
	// Verrou de LOCK TABLES : une transaction SQLite (BEGIN IMMEDIATE si une table est verrouillée en
	// écriture, BEGIN DEFERRED suivi d'une lecture sinon) tenue jusqu'à UNLOCK TABLES, un nouveau LOCK TABLES,
	// START TRANSACTION ou la fermeture. lockedTables associe chaque table verrouillée, par son nom réel
	// replié en ASCII minuscule, à son mode d'écriture (nil : toute table est lisible) ; globalReadLock signale
	// le verrou de FLUSH TABLES WITH READ LOCK, qui refuse toute écriture jusqu'à UNLOCK TABLES ; lockDenied
	// retient le refus consigné par l'autorisateur.
	tablesLocked   bool
	lockWrite      bool
	lockedTables   map[string]bool
	globalReadLock bool
	lockDenied     *sqlError
}

// colMeta retient la lecture de sqlite3_table_column_metadata pour une colonne ; ok est faux si SQLite
// ne connaît pas la colonne dans le schéma main.
type colMeta struct {
	decltype   string
	notNull    bool
	primaryKey bool
	autoInc    bool
	ok         bool
}

func defaultSessionVars() map[string]string {
	return map[string]string{
		"sql_mode":                 "STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION",
		"version":                  sqliteServerVersion,
		"version_comment":          "php127 mysqli, SQLite3 " + sqliteLibVersion(),
		"version_compile_os":       "Linux",
		"character_set_client":     "utf8mb4",
		"character_set_connection": "utf8mb4",
		"character_set_results":    "utf8mb4",
		"character_set_database":   "utf8mb4",
		"character_set_server":     "utf8mb4",
		"character_set_system":     "utf8mb3",
		"collation_connection":     "utf8mb4_0900_ai_ci",
		"collation_database":       "utf8mb4_0900_ai_ci",
		"collation_server":         "utf8mb4_0900_ai_ci",
		"time_zone":                "SYSTEM",
		"system_time_zone":         "UTC",
		"autocommit":               "1",
		"max_allowed_packet":       "67108864",
		"sql_big_selects":          "1",
		"sql_safe_updates":         "0",
		"sql_auto_is_null":         "0",
		"sql_select_limit":         "18446744073709551615",
		"wait_timeout":             "28800",
		"interactive_timeout":      "28800",
		"lower_case_table_names":   "0",
		"foreign_key_checks":       "1",
		"unique_checks":            "1",
		"transaction_isolation":    "SERIALIZABLE",
		"tx_isolation":             "SERIALIZABLE",
		"default_storage_engine":   "SQLite",
		"storage_engine":           "SQLite",
		"group_concat_max_len":     "1024",
		"max_connections":          "151",
		"have_ssl":                 "DISABLED",
		"innodb_large_prefix":      "ON",
		"innodb_file_format":       "Barracuda",
		"innodb_file_per_table":    "ON",
		"ft_min_word_len":          "4",
		"long_query_time":          "10.000000",
		"net_write_timeout":        "60",
		"net_read_timeout":         "30",
		"max_execution_time":       "0",
		"sort_buffer_size":         "262144",
		"query_cache_type":         "OFF",
		"port":                     "0",
		"hostname":                 "localhost",
		"protocol_version":         "10",
		"warning_count":            "0",
		"error_count":              "0",
	}
}

// openSQLiteBackend ouvre la base et y enregistre les fonctions SQL MySQL servies par Go.
func openSQLiteBackend(path, dbName string) (*sqliteBackend, *sqlError) {
	db, rc, msg := sqliteOpen(path)
	if rc != sqliteOK {
		return nil, newSQLError(crConnectionError, "HY000", "Unable to open SQLite database '"+path+"': "+msg)
	}
	b := &sqliteBackend{
		db:         db,
		path:       path,
		dbName:     dbName,
		vars:       defaultSessionVars(),
		userVars:   make(map[string]cell),
		autocommit: true,
		started:    time.Now(),
	}
	b.invalidateMeta()
	b.sqlID = registerSQLConn(b)
	for _, def := range sqlFuncDefs {
		key := uintptr(b.sqlID)<<8 | def.id
		if rc := db.createFunction(def.name, def.nargs, def.deterministic, key); rc != sqliteOK {
			_, m := db.errInfo()
			b.close()
			return nil, newSQLError(crConnectionError, "HY000", "Unable to register SQL function "+def.name+": "+m)
		}
	}
	if rc := db.setForeignKeys(true); rc != sqliteOK {
		_, m := db.errInfo()
		b.close()
		return nil, newSQLError(crConnectionError, "HY000", "Unable to enable foreign keys: "+m)
	}
	return b, nil
}

func (b *sqliteBackend) close() {
	if b.db == nil {
		return
	}
	// Sous LOCK TABLES en autocommit, MySQL a déjà validé chaque instruction : la fermeture valide la
	// transaction du verrou ; toute autre transaction ouverte est annulée.
	if b.tablesLocked && b.autocommit {
		_ = b.unlockTables()
	}
	if b.db.inTransaction() {
		_, _, _ = b.run("ROLLBACK", nil)
	}
	unregisterSQLConn(b.sqlID)
	b.db.close()
	b.db = nil
}

func (b *sqliteBackend) serverInfo() string   { return sqliteServerVersion }
func (b *sqliteBackend) serverVersion() int   { return sqliteServerVersionNum }
func (b *sqliteBackend) hostInfo() string     { return "SQLite3 via file " + b.path }
func (b *sqliteBackend) protocolVersion() int { return 10 }
func (b *sqliteBackend) threadID() int64      { return int64(b.sqlID) }

func (b *sqliteBackend) stat() string {
	up := int64(time.Since(b.started).Seconds())
	return "Uptime: " + strconv.FormatInt(up, 10) + "  Threads: 1  Questions: " + strconv.FormatInt(b.questions, 10) +
		"  Slow queries: 0  Opens: 1  Flush tables: 1  Open tables: 1  Queries per second avg: 0.000"
}

func (b *sqliteBackend) ping() bool {
	if b.db == nil {
		return false
	}
	_, _, err := b.run("SELECT 1", nil)
	return err == nil
}

func (b *sqliteBackend) selectDB(db string) *sqlError {
	if b.db == nil {
		return newSQLError(crServerGone, "HY000", "MySQL server has gone away")
	}
	b.dbName = db
	return nil
}

// setCharset fixe le jeu de caractères de session ; SQLite stocke toujours de l'UTF-8.
func (b *sqliteBackend) setCharset(cs string) *sqlError {
	lc := strings.ToLower(cs)
	switch lc {
	case "utf8mb4", "utf8", "utf8mb3", "latin1", "ascii", "binary":
	default:
		return newSQLError(2019, "HY000", "Can't initialize character set "+cs+" (path: compiled_in)")
	}
	for _, k := range []string{"character_set_client", "character_set_connection", "character_set_results"} {
		b.vars[k] = lc
	}
	_, coll := tableOptions([]token{{kind: tkWord, text: "CHARSET"}, {kind: tkWord, text: lc}})
	b.vars["collation_connection"] = coll
	return nil
}

func (b *sqliteBackend) translator() *translator {
	return &translator{
		sysvar: func(name string) (string, bool) {
			switch name {
			case "identity", "last_insert_id":
				return strconv.FormatInt(b.lastInsertID, 10), true
			case "autocommit":
				if b.autocommit {
					return "1", true
				}
				return "0", true
			}
			v, ok := b.vars[name]
			return v, ok
		},
		uservar: func(name string) cell {
			if v, ok := b.userVars[name]; ok {
				return v
			}
			return nullCell
		},
	}
}

// run prépare, lie et exécute une instruction SQLite unique.
func (b *sqliteBackend) run(sql string, params []cell) ([]sqliteColumn, [][]cell, *sqlError) {
	if b.db == nil {
		return nil, nil, newSQLError(crServerGone, "HY000", "MySQL server has gone away")
	}
	b.lockDenied = nil
	st, _, rc, msg := b.db.prepare(sql)
	if rc != sqliteOK {
		return nil, nil, b.mapError(rc, msg, sql)
	}
	if st.empty() {
		return nil, nil, nil
	}
	defer st.finalize()
	if n := st.paramCount(); n != len(params) {
		if n > len(params) {
			return nil, nil, newSQLError(crParamsNotBound, "HY000", "No data supplied for parameters in prepared statement")
		}
		params = params[:n]
	}
	for i, p := range params {
		if rc := st.bind(i+1, p); rc != sqliteOK {
			code, m := b.db.errInfo()
			return nil, nil, b.mapError(code, m, sql)
		}
	}
	cols := st.columns()
	rows, rc := st.step()
	if rc != sqliteDone {
		code, m := b.db.errInfo()
		return nil, nil, b.mapError(code, m, sql)
	}
	return cols, rows, nil
}

// runAll exécute plusieurs instructions SQLite de façon atomique (SAVEPOINT).
func (b *sqliteBackend) runAll(stmts []string) *sqlError {
	if len(stmts) == 1 {
		_, _, err := b.run(stmts[0], nil)
		return err
	}
	if _, _, err := b.run("SAVEPOINT mysqli_stmt", nil); err != nil {
		return err
	}
	for _, s := range stmts {
		if _, _, err := b.run(s, nil); err != nil {
			_, _, _ = b.run("ROLLBACK TO mysqli_stmt", nil)
			_, _, _ = b.run("RELEASE mysqli_stmt", nil)
			return err
		}
	}
	_, _, err := b.run("RELEASE mysqli_stmt", nil)
	return err
}

func (b *sqliteBackend) schemaName() string {
	if b.dbName == "" {
		return "main"
	}
	return b.dbName
}

// mapError traduit un code et un message SQLite en erreur serveur MySQL (errno, SQLSTATE).
func (b *sqliteBackend) mapError(rc int, msg, sql string) *sqlError {
	if err := b.lockDenied; err != nil {
		b.lockDenied = nil
		return err
	}
	after := func(prefix string) string {
		if _, after, ok := strings.Cut(msg, prefix); ok {
			return strings.TrimSpace(after)
		}
		return ""
	}
	switch {
	case strings.Contains(msg, "UNIQUE constraint failed") || rc == 2067 || rc == 1555:
		return newSQLError(1062, "23000", "Duplicate entry for key '"+after("failed:")+"'")
	case strings.Contains(msg, "NOT NULL constraint failed"):
		col := after("failed:")
		if _, c, ok := strings.Cut(col, "."); ok {
			col = c
		}
		return newSQLError(1048, "23000", "Column '"+col+"' cannot be null")
	case strings.Contains(msg, "FOREIGN KEY constraint failed"):
		return newSQLError(1452, "23000", "Cannot add or update a child row: a foreign key constraint fails")
	case strings.Contains(msg, "CHECK constraint failed"):
		return newSQLError(3819, "HY000", "Check constraint '"+after("failed:")+"' is violated.")
	case strings.HasPrefix(msg, "no such table:"):
		return newSQLError(1146, "42S02", "Table '"+b.schemaName()+"."+after("no such table:")+"' doesn't exist")
	case strings.HasPrefix(msg, "no such column:"):
		return newSQLError(1054, "42S22", "Unknown column '"+after("no such column:")+"' in 'field list'")
	case strings.HasPrefix(msg, "table ") && strings.HasSuffix(msg, " already exists"):
		name := strings.TrimSuffix(strings.TrimPrefix(msg, "table "), " already exists")
		return newSQLError(1050, "42S01", "Table '"+strings.Trim(name, `"`)+"' already exists")
	case strings.HasPrefix(msg, "index ") && strings.HasSuffix(msg, " already exists"):
		name := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(msg, "index "), " already exists"), `"`)
		return newSQLError(1061, "42000", "Duplicate key name '"+mysqlIndexName("", name)+"'")
	case strings.HasPrefix(msg, "no such index:"):
		return newSQLError(1091, "42000", "Can't DROP '"+mysqlIndexName("", after("no such index:"))+"'; check that column/key exists")
	case strings.HasPrefix(msg, "duplicate column name:"):
		return newSQLError(1060, "42S21", "Duplicate column name '"+after("duplicate column name:")+"'")
	case strings.Contains(msg, "syntax error") || strings.Contains(msg, "incomplete input") || strings.Contains(msg, "unrecognized token"):
		near := after("near ")
		near, _, _ = strings.Cut(near, ": syntax error")
		return newSQLError(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near "+near+" at line 1 (SQLite: "+msg+")")
	case strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked"):
		return newSQLError(1205, "HY000", "Lock wait timeout exceeded; try restarting transaction")
	case strings.HasPrefix(msg, "no such function:"):
		return newSQLError(1305, "42000", "FUNCTION "+b.schemaName()+"."+after("no such function:")+" does not exist")
	}
	return newSQLError(1105, "HY000", msg)
}

// ensureTx ouvre la transaction implicite d'une session en autocommit désactivé.
func (b *sqliteBackend) ensureTx() *sqlError {
	if b.tablesLocked && b.db != nil && !b.db.inTransaction() {
		return b.beginLockTx()
	}
	if !b.autocommit && b.db != nil && !b.db.inTransaction() {
		_, _, err := b.run("BEGIN", nil)
		return err
	}
	return nil
}

func (b *sqliteBackend) query(sql string, multi bool) ([]execResult, *sqlError) {
	toks := lexSQL(sql)
	stmts := splitStatements(toks)
	if len(stmts) == 0 {
		return nil, newSQLError(1065, "42000", "Query was empty")
	}
	if !multi && len(stmts) > 1 {
		return nil, newSQLError(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '"+strings.TrimSpace(joinTokens(stmts[1]))+"' at line 1")
	}
	out := make([]execResult, 0, len(stmts))
	for _, st := range stmts {
		res, err := b.execStatement(st, nil)
		if err != nil {
			return out, err
		}
		out = append(out, res)
	}
	return out, nil
}

func firstWords(toks []token, n int) []string {
	out := make([]string, 0, n)
	for _, t := range toks {
		if len(out) == n {
			break
		}
		if t.sig() {
			out = append(out, strings.ToUpper(t.text))
		}
	}
	for len(out) < n {
		out = append(out, "")
	}
	return out
}

// mysqlStatementHeads est la liste blanche des verbes de tête acceptés par le moteur SQLite : toute autre
// instruction est une erreur de syntaxe MySQL et n'atteint jamais SQLite. « ( » ouvre une requête
// parenthésée ; GRANT, REVOKE et CHECKSUM sont interceptés par execStatement.
var mysqlStatementHeads = map[string]bool{
	"SELECT": true, "WITH": true, "VALUES": true, "(": true, "SHOW": true, "DESCRIBE": true, "DESC": true,
	"EXPLAIN": true, "INSERT": true, "REPLACE": true, "UPDATE": true, "DELETE": true, "CREATE": true,
	"ALTER": true, "DROP": true, "TRUNCATE": true, "RENAME": true, "SET": true, "BEGIN": true, "START": true,
	"COMMIT": true, "ROLLBACK": true, "SAVEPOINT": true, "RELEASE": true, "USE": true, "DO": true,
	"LOCK": true, "UNLOCK": true, "FLUSH": true, "RESET": true, "PURGE": true, "ANALYZE": true,
	"CHECK": true, "OPTIMIZE": true, "REPAIR": true, "GRANT": true, "REVOKE": true, "CHECKSUM": true,
}

// statementHeadError refuse par l'erreur MySQL 1064 une instruction dont le verbe de tête est un verbe
// propre à SQLite (PRAGMA, ATTACH, DETACH, VACUUM, REINDEX) ou absent de mysqlStatementHeads.
func statementHeadError(toks []token) *sqlError {
	k := sigIndex(toks, 0)
	if k >= len(toks) {
		return nil
	}
	head := toks[k].text
	if toks[k].kind == tkWord || toks[k].kind == tkPunct {
		if mysqlStatementHeads[strings.ToUpper(head)] {
			return nil
		}
	}
	return newSQLError(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '"+head+"'")
}

// isDDLHead signale une instruction qui modifie le schéma et périme les caches de métadonnées.
func isDDLHead(w string) bool {
	switch w {
	case "CREATE", "ALTER", "DROP", "TRUNCATE", "RENAME":
		return true
	}
	return false
}

// invalidateMeta vide les caches de métadonnées de table.
func (b *sqliteBackend) invalidateMeta() {
	b.autoIncCache = make(map[string]bool)
	b.colMetaCache = make(map[string]map[string]colMeta)
}

// foldASCII replie les lettres ASCII en minuscules, comme SQLite compare les noms de table et de colonne.
func foldASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// execStatement exécute une instruction MySQL unique, interceptée ou traduite.
func (b *sqliteBackend) execStatement(toks []token, params []cell) (execResult, *sqlError) {
	if b.db == nil {
		return execResult{}, newSQLError(crServerGone, "HY000", "MySQL server has gone away")
	}
	b.questions++
	if err := statementHeadError(toks); err != nil {
		return execResult{}, err
	}
	w := firstWords(toks, 3)
	if isDDLHead(w[0]) || w[0] == "ROLLBACK" {
		defer b.invalidateMeta()
	}
	tr := b.translator()
	switch w[0] {
	case "START", "BEGIN":
		// Ouvrir une transaction valide la transaction courante et relâche les verrous de LOCK TABLES ; le
		// verrou de FLUSH TABLES WITH READ LOCK est conservé, porté par la nouvelle transaction.
		global := b.globalReadLock
		if err := b.unlockTables(); err != nil {
			return execResult{}, err
		}
		if b.db.inTransaction() {
			if _, _, err := b.run("COMMIT", nil); err != nil {
				return execResult{}, err
			}
		}
		if _, _, err := b.run("BEGIN", nil); err != nil {
			return execResult{}, err
		}
		if global {
			return execResult{}, b.holdLock(nil, false, true)
		}
		return execResult{}, nil
	case "COMMIT", "ROLLBACK":
		ss := sigTokens(toks)
		if w[0] == "ROLLBACK" && (w[1] == "TO" || (w[1] == "WORK" && w[2] == "TO")) {
			_, _, err := b.run("ROLLBACK TO "+quoteIdent(ss[len(ss)-1].name()), nil)
			return execResult{}, err
		}
		if b.tablesLocked {
			return execResult{}, b.endLockedTx(w[0])
		}
		if !b.db.inTransaction() {
			return execResult{}, nil
		}
		_, _, err := b.run(w[0], nil)
		return execResult{}, err
	case "SAVEPOINT":
		if err := b.ensureTx(); err != nil {
			return execResult{}, err
		}
		_, _, err := b.run(tr.expr(toks), nil)
		return execResult{}, err
	case "RELEASE":
		_, _, err := b.run(tr.expr(toks), nil)
		return execResult{}, err
	case "SET":
		return b.execSet(toks)
	case "SHOW":
		return b.execShow(toks)
	case "DESCRIBE", "DESC":
		ss := sigTokens(toks)
		if len(ss) < 2 {
			return execResult{}, newSQLError(1064, "42000", "You have an error in your SQL syntax near 'DESCRIBE'")
		}
		like := ""
		if len(ss) > 2 {
			like = ss[2].name()
		}
		return b.showColumns(ss[1].name(), false, like)
	case "EXPLAIN":
		ss := sigTokens(toks)
		if len(ss) >= 2 && ss[1].kind != tkIdent && (ss[1].is("SELECT") || ss[1].is("INSERT") || ss[1].is("UPDATE") || ss[1].is("DELETE") || ss[1].is("REPLACE") || ss[1].is("WITH")) {
			return b.execSelect(append([]token{{kind: tkRaw, text: "EXPLAIN QUERY PLAN "}}, toks[sigIndex(toks, 0)+1:]...), params)
		}
		if len(ss) >= 2 {
			return b.showColumns(ss[1].name(), false, "")
		}
	case "USE":
		ss := sigTokens(toks)
		if len(ss) < 2 {
			return execResult{}, newSQLError(1046, "3D000", "No database selected")
		}
		return execResult{}, b.selectDB(ss[1].name())
	case "DO":
		if err := b.ensureTx(); err != nil {
			return execResult{}, err
		}
		_, _, err := b.run("SELECT "+tr.expr(toks[sigIndex(toks, 0)+1:]), params)
		if err == nil && tr.err != nil {
			err = tr.err
		}
		return execResult{}, err
	case "GRANT", "REVOKE":
		// SQLite n'a ni comptes ni privilèges : un droit demandé ne peut pas être accordé.
		return execResult{}, newSQLError(1235, "42000", "This version of MySQL doesn't yet support '"+w[0]+" on the SQLite backend'")
	case "LOCK":
		return execResult{}, b.lockTables(toks)
	case "UNLOCK":
		if w[1] != "TABLES" && w[1] != "TABLE" {
			return execResult{}, newSQLError(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '"+strings.TrimSpace(joinTokens(toks))+"' at line 1")
		}
		return execResult{}, b.unlockTables()
	case "FLUSH":
		return execResult{}, b.flush(toks)
	case "RESET":
		return execResult{}, b.reset(toks)
	case "PURGE":
		return execResult{}, purge(toks)
	case "OPTIMIZE", "ANALYZE", "REPAIR", "CHECK", "CHECKSUM":
		return b.maintenance(toks, w[0])
	case "TRUNCATE":
		return b.truncate(toks)
	}
	if err := b.ensureTx(); err != nil {
		return execResult{}, err
	}
	switch w[0] {
	case "SELECT", "WITH", "VALUES", "(":
		return b.execSelect(toks, params)
	case "INSERT", "REPLACE":
		sql, table, upsert := tr.translateInsert(toks)
		if tr.err != nil {
			return execResult{}, tr.err
		}
		return b.execInsert(sql, table, upsert, params)
	case "UPDATE":
		sql := tr.translateUpdate(toks)
		if tr.err != nil {
			return execResult{}, tr.err
		}
		return b.execWrite(sql, params)
	case "DELETE":
		sql, targets, fromWhere := tr.translateDelete(toks)
		if tr.err != nil {
			return execResult{}, tr.err
		}
		if len(targets) > 0 {
			return b.multiDelete(targets, fromWhere, params)
		}
		return b.execWrite(sql, params)
	case "CREATE":
		var stmts []string
		switch {
		case w[1] == "DATABASE" || w[1] == "SCHEMA":
			if b.globalReadLock {
				return execResult{}, readLockConflict()
			}
			return execResult{affected: 1}, nil
		case w[1] == "TABLE" || w[1] == "TEMPORARY":
			stmts = tr.translateCreateTable(toks)
		case w[1] == "INDEX" || w[1] == "UNIQUE" || w[1] == "FULLTEXT" || w[1] == "SPATIAL":
			stmts = tr.translateCreateIndex(toks)
		default:
			stmts = []string{tr.expr(toks)}
		}
		if tr.err != nil {
			return execResult{}, tr.err
		}
		if len(stmts) == 0 {
			return execResult{}, nil
		}
		return execResult{}, b.runAll(stmts)
	case "ALTER":
		if w[1] == "DATABASE" || w[1] == "SCHEMA" {
			if b.globalReadLock {
				return execResult{}, readLockConflict()
			}
			return execResult{}, nil
		}
		steps, warnings := tr.translateAlterTable(toks)
		if tr.err != nil {
			return execResult{}, tr.err
		}
		res := execResult{warnings: warnings}
		if len(steps) == 0 {
			return res, nil
		}
		return res, b.runAlter(steps)
	case "DROP":
		if w[1] == "DATABASE" || w[1] == "SCHEMA" {
			if b.globalReadLock {
				return execResult{}, readLockConflict()
			}
			return execResult{}, nil
		}
		stmts := tr.translateDrop(toks)
		if tr.err != nil {
			return execResult{}, tr.err
		}
		return execResult{}, b.runAll(stmts)
	}
	sql := tr.expr(toks)
	if tr.err != nil {
		return execResult{}, tr.err
	}
	cols, rows, err := b.run(sql, params)
	if err != nil {
		return execResult{}, err
	}
	if len(cols) > 0 {
		return execResult{rs: &resultSet{fields: b.buildFields(cols, rows), rows: rows}, affected: int64(len(rows))}, nil
	}
	return execResult{}, nil
}

// execSelect exécute une lecture ; SQL_CALC_FOUND_ROWS déclenche le comptage sans LIMIT.
func (b *sqliteBackend) execSelect(toks []token, params []cell) (execResult, *sqlError) {
	tr := b.translator()
	calc := false
	limitAt := -1
	depth := 0
	for i, t := range toks {
		switch {
		case t.punct("("):
			depth++
		case t.punct(")"):
			depth--
		case depth == 0 && t.is("SQL_CALC_FOUND_ROWS"):
			calc = true
		case depth == 0 && t.is("LIMIT"):
			limitAt = i
		}
	}
	sql := tr.expr(toks)
	if tr.err != nil {
		return execResult{}, tr.err
	}
	cols, rows, err := b.run(sql, params)
	if err != nil {
		return execResult{}, err
	}
	b.foundRows = int64(len(rows))
	if calc && limitAt >= 0 {
		head := toks[:limitAt]
		k := min(countParams(head), len(params))
		ctr := b.translator()
		countSQL := "SELECT COUNT(*) FROM (" + ctr.expr(head) + ")"
		_, crow, cerr := b.run(countSQL, params[:k])
		if cerr != nil {
			return execResult{}, cerr
		}
		if len(crow) == 1 && len(crow[0]) == 1 {
			b.foundRows = crow[0][0].i
		}
	}
	return execResult{rs: &resultSet{fields: b.buildFields(cols, rows), rows: rows}, affected: int64(len(rows))}, nil
}

func (b *sqliteBackend) execWrite(sql string, params []cell) (execResult, *sqlError) {
	if _, _, err := b.run(sql, params); err != nil {
		return execResult{}, err
	}
	return execResult{affected: b.db.changes()}, nil
}

// execInsert calcule insert_id (table AUTO_INCREMENT seulement) et affected_rows (2 pour une mise à jour par upsert).
func (b *sqliteBackend) execInsert(sql, table string, upsert bool, params []cell) (execResult, *sqlError) {
	before := b.db.lastInsertRowID()
	if _, _, err := b.run(sql, params); err != nil {
		return execResult{}, err
	}
	res := execResult{affected: b.db.changes()}
	after := b.db.lastInsertRowID()
	updated := upsert && res.affected == 1 && after == before
	if updated {
		res.affected = 2
		return res, nil
	}
	if res.affected > 0 && b.hasAutoIncrement(table) {
		res.insertID = after
		b.lastInsertID = after
	}
	return res, nil
}

// hasAutoIncrement lit dans sqlite_master si la table déclare AUTOINCREMENT ; la réponse est retenue
// dans autoIncCache jusqu'à la prochaine instruction DDL.
func (b *sqliteBackend) hasAutoIncrement(table string) bool {
	key := foldASCII(table)
	if v, ok := b.autoIncCache[key]; ok {
		return v
	}
	_, rows, err := b.run("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ? COLLATE NOCASE", []cell{textCell(table)})
	if err != nil {
		return false
	}
	v := len(rows) > 0 && strings.Contains(strings.ToUpper(rows[0][0].s), "AUTOINCREMENT")
	b.autoIncCache[key] = v
	return v
}

// columnMeta lit les contraintes déclarées d'une colonne, retenues dans colMetaCache jusqu'à la prochaine
// instruction DDL.
func (b *sqliteBackend) columnMeta(table, column string) colMeta {
	tk, ck := foldASCII(table), foldASCII(column)
	cols, ok := b.colMetaCache[tk]
	if ok {
		if m, ok := cols[ck]; ok {
			return m
		}
	} else {
		cols = make(map[string]colMeta)
		b.colMetaCache[tk] = cols
	}
	var m colMeta
	m.decltype, m.notNull, m.primaryKey, m.autoInc, m.ok = b.db.columnFlags(table, column)
	cols[ck] = m
	return m
}

// multiDelete exécute DELETE a, b FROM ... : les rowid visés sont lus avant toute suppression.
func (b *sqliteBackend) multiDelete(targets []deleteTarget, fromWhere string, params []cell) (execResult, *sqlError) {
	ids := make([][]string, len(targets))
	for i, t := range targets {
		_, rows, err := b.run("SELECT DISTINCT "+quoteIdent(t.alias)+".rowid FROM "+fromWhere, params)
		if err != nil {
			return execResult{}, err
		}
		for _, r := range rows {
			ids[i] = append(ids[i], r[0].text())
		}
	}
	if _, _, err := b.run("SAVEPOINT mysqli_stmt", nil); err != nil {
		return execResult{}, err
	}
	var total int64
	for i, t := range targets {
		for start := 0; start < len(ids[i]); start += 500 {
			end := min(start+500, len(ids[i]))
			sql := "DELETE FROM " + quoteIdent(t.table) + " WHERE rowid IN (" + strings.Join(ids[i][start:end], ",") + ")"
			if _, _, err := b.run(sql, nil); err != nil {
				_, _, _ = b.run("ROLLBACK TO mysqli_stmt", nil)
				_, _, _ = b.run("RELEASE mysqli_stmt", nil)
				return execResult{}, err
			}
			total += b.db.changes()
		}
	}
	if _, _, err := b.run("RELEASE mysqli_stmt", nil); err != nil {
		return execResult{}, err
	}
	return execResult{affected: total}, nil
}

func (b *sqliteBackend) truncate(toks []token) (execResult, *sqlError) {
	ss := sigTokens(toks)
	k := 1
	if k < len(ss) && ss[k].is("TABLE") {
		k++
	}
	if k >= len(ss) {
		return execResult{}, newSQLError(1064, "42000", "You have an error in your SQL syntax near 'TRUNCATE'")
	}
	table := ss[k].name()
	if err := b.ensureTx(); err != nil {
		return execResult{}, err
	}
	stmts := []string{"DELETE FROM " + quoteIdent(table)}
	if _, rows, err := b.run("SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'sqlite_sequence'", nil); err == nil && len(rows) > 0 {
		stmts = append(stmts, "DELETE FROM sqlite_sequence WHERE name = "+sqliteQuote(table))
	}
	return execResult{}, b.runAll(stmts)
}

func (b *sqliteBackend) maintenance(toks []token, op string) (execResult, *sqlError) {
	ss := sigTokens(toks)
	k := 1
	for k < len(ss) && (ss[k].is("NO_WRITE_TO_BINLOG") || ss[k].is("LOCAL") || ss[k].is("TABLE")) {
		k++
	}
	var rows [][]cell
	for _, part := range splitTop(ss[k:], ",") {
		if len(part) == 0 {
			continue
		}
		table := part[0].name()
		if _, r, err := b.run("SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?", []cell{textCell(table)}); err != nil || len(r) == 0 {
			rows = append(rows, []cell{textCell(b.schemaName() + "." + table), textCell(strings.ToLower(op)), textCell("Error"), textCell("Table '" + b.schemaName() + "." + table + "' doesn't exist")})
			continue
		}
		if op == "ANALYZE" {
			if _, _, err := b.run("ANALYZE "+quoteIdent(table), nil); err != nil {
				return execResult{}, err
			}
		}
		rows = append(rows, []cell{textCell(b.schemaName() + "." + table), textCell(strings.ToLower(op)), textCell("status"), textCell("OK")})
	}
	return execResult{rs: textResult([]string{"Table", "Op", "Msg_type", "Msg_text"}, rows)}, nil
}

// execSet applique SET NAMES, SET CHARACTER SET, les variables système et les variables utilisateur.
func (b *sqliteBackend) execSet(toks []token) (execResult, *sqlError) {
	ss := sigTokens(toks)
	if len(ss) < 2 {
		return execResult{}, newSQLError(1064, "42000", "You have an error in your SQL syntax near 'SET'")
	}
	switch {
	case ss[1].is("NAMES") || ss[1].is("CHARSET") || (ss[1].is("CHARACTER") && len(ss) > 2 && ss[2].is("SET")):
		k := 2
		if ss[1].is("CHARACTER") {
			k = 3
		}
		if k >= len(ss) {
			return execResult{}, newSQLError(1064, "42000", "You have an error in your SQL syntax near 'SET NAMES'")
		}
		if ss[k].is("DEFAULT") {
			return execResult{}, nil
		}
		if err := b.setCharset(ss[k].name()); err != nil {
			return execResult{}, newSQLError(1115, "42000", "Unknown character set: '"+ss[k].name()+"'")
		}
		if k+2 < len(ss) && ss[k+1].is("COLLATE") {
			b.vars["collation_connection"] = strings.ToLower(ss[k+2].name())
		}
		return execResult{}, nil
	case ss[1].is("TRANSACTION") || ss[1].is("PASSWORD") || ((ss[1].is("SESSION") || ss[1].is("GLOBAL")) && len(ss) > 2 && ss[2].is("TRANSACTION")):
		return execResult{}, nil
	}
	for _, asg := range splitTop(ss[1:], ",") {
		if len(asg) == 0 {
			continue
		}
		global := false
		if asg[0].is("SESSION") || asg[0].is("LOCAL") || asg[0].is("GLOBAL") || asg[0].is("PERSIST") {
			global = asg[0].is("GLOBAL") || asg[0].is("PERSIST")
			asg = asg[1:]
		}
		eq := -1
		for i, t := range asg {
			if t.punct("=") || t.punct(":=") {
				eq = i
				break
			}
		}
		if eq < 1 {
			return execResult{}, newSQLError(1064, "42000", "You have an error in your SQL syntax near '"+joinTokens(spaced(asg))+"'")
		}
		target := asg[0]
		value := asg[eq+1:]
		if target.kind == tkVar && !strings.HasPrefix(target.val, "@@") {
			v, err := b.evalValue(value, false)
			if err != nil {
				return execResult{}, err
			}
			b.userVars[strings.ToLower(strings.TrimPrefix(target.val, "@"))] = v
			continue
		}
		name := strings.ToLower(strings.TrimPrefix(target.name(), "@@"))
		if target.kind == tkVar {
			name = strings.ToLower(strings.TrimPrefix(target.val, "@@"))
			if strings.HasPrefix(name, "global.") {
				global = true
			}
		}
		for _, scope := range []string{"session.", "global.", "local.", "persist."} {
			name = strings.TrimPrefix(name, scope)
		}
		if _, ok := b.vars[name]; !ok {
			return execResult{}, newSQLError(1193, "HY000", "Unknown system variable '"+name+"'")
		}
		v, err := b.evalValue(value, true)
		if err != nil {
			return execResult{}, err
		}
		text := v.text()
		if v.kind == cellNull {
			text = ""
		}
		if len(value) == 1 && value[0].is("DEFAULT") {
			text = defaultSessionVars()[name]
		}
		if err := b.applyVar(name, text); err != nil {
			return execResult{}, err
		}
		_ = global
	}
	return execResult{}, nil
}

// evalValue évalue la valeur d'une affectation SET ; un mot nu (ON, OFF, utf8mb4) vaut sa propre chaîne.
func (b *sqliteBackend) evalValue(value []token, bareWords bool) (cell, *sqlError) {
	if len(value) == 1 && bareWords && value[0].kind == tkWord {
		return textCell(value[0].text), nil
	}
	tr := b.translator()
	sql := "SELECT " + tr.expr(spaced(value))
	if tr.err != nil {
		return nullCell, tr.err
	}
	_, rows, err := b.run(sql, nil)
	if err != nil {
		return nullCell, err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return nullCell, nil
	}
	return rows[0][0], nil
}

func boolVar(text string) (bool, bool) {
	switch strings.ToUpper(text) {
	case "1", "ON", "TRUE":
		return true, true
	case "0", "OFF", "FALSE":
		return false, true
	}
	return false, false
}

func (b *sqliteBackend) applyVar(name, text string) *sqlError {
	switch name {
	case "autocommit":
		on, ok := boolVar(text)
		if !ok {
			return newSQLError(1231, "42000", "Variable 'autocommit' can't be set to the value of '"+text+"'")
		}
		return b.setAutocommit(on)
	case "foreign_key_checks":
		on, ok := boolVar(text)
		if !ok {
			return newSQLError(1231, "42000", "Variable 'foreign_key_checks' can't be set to the value of '"+text+"'")
		}
		text = "0"
		if on {
			text = "1"
		}
		if rc := b.db.setForeignKeys(on); rc != sqliteOK {
			code, m := b.db.errInfo()
			return b.mapError(code, m, "")
		}
	case "sql_mode":
		text = strings.ToUpper(text)
	}
	b.vars[name] = text
	return nil
}

// setAutocommit applique l'autocommit MySQL : son rétablissement valide la transaction ouverte.
func (b *sqliteBackend) setAutocommit(on bool) *sqlError {
	if on && !b.autocommit && b.db.inTransaction() {
		if _, _, err := b.run("COMMIT", nil); err != nil {
			return err
		}
	}
	b.autocommit = on
	if b.tablesLocked && !b.db.inTransaction() {
		if err := b.beginLockTx(); err != nil {
			return err
		}
	}
	if on {
		b.vars["autocommit"] = "1"
	} else {
		b.vars["autocommit"] = "0"
	}
	return nil
}

// textResult construit un résultat synthétique dont toutes les colonnes sont des chaînes.
func textResult(names []string, rows [][]cell) *resultSet {
	fields := make([]field, len(names))
	for i, n := range names {
		fields[i] = field{name: n, orgname: n, length: 256, charsetnr: charsetUTF8MB4, typ: typeVarString}
	}
	for _, r := range rows {
		for i := range r {
			if r[i].kind != cellNull {
				r[i] = textCell(r[i].text())
			}
		}
	}
	return &resultSet{fields: fields, rows: rows}
}

// buildFields dérive les métadonnées MySQL des colonnes SQLite (type déclaré, contraintes, valeurs).
func (b *sqliteBackend) buildFields(cols []sqliteColumn, rows [][]cell) []field {
	fields := make([]field, len(cols))
	for i, c := range cols {
		f := field{name: c.name, orgname: c.origin, table: c.table, orgtable: c.table, charsetnr: charsetUTF8MB4}
		if c.table != "" {
			f.db = b.schemaName()
		}
		if c.decltype != "" {
			f.typ, f.length, f.decimals, f.flags, f.charsetnr = mapDecltype(c.decltype)
		} else {
			f.typ, f.length, f.charsetnr = typeVarString, 0, charsetUTF8MB4
			for _, r := range rows {
				if i >= len(r) || r[i].kind == cellNull {
					continue
				}
				switch r[i].kind {
				case cellInt:
					f.typ, f.length, f.charsetnr, f.flags = typeLongLong, 21, charsetBinary, flagNum|flagBinary
				case cellFloat:
					f.typ, f.length, f.charsetnr, f.decimals, f.flags = typeDouble, 23, charsetBinary, 31, flagNum|flagBinary
				case cellBlob:
					f.typ, f.charsetnr, f.flags = typeBlob, charsetBinary, flagBlob|flagBinary
				}
				break
			}
			if len(rows) == 0 || allNull(rows, i) {
				if len(rows) > 0 {
					f.typ, f.charsetnr, f.flags = typeNull, charsetBinary, flagBinary
				}
			}
		}
		if c.table != "" && c.origin != "" {
			if m := b.columnMeta(c.table, c.origin); m.ok {
				if m.notNull {
					f.flags |= flagNotNull
				}
				if m.primaryKey {
					f.flags |= flagPriKey | flagNotNull
				}
				if m.autoInc {
					f.flags |= flagAutoIncrement | flagNotNull | flagPriKey
					f.typ, f.length = typeLongLong, 20
				}
			}
		}
		fields[i] = f
	}
	return fields
}

func allNull(rows [][]cell, i int) bool {
	for _, r := range rows {
		if i < len(r) && r[i].kind != cellNull {
			return false
		}
	}
	return true
}

// mapDecltype traduit un type déclaré (« bigint unsigned(20) », « varchar(191) ») en métadonnées MySQL.
func mapDecltype(decl string) (typ uint8, length uint32, decimals uint8, flags uint16, charset uint16) {
	d := strings.ToLower(strings.TrimSpace(decl))
	args := ""
	if i := strings.IndexByte(d, '('); i >= 0 {
		args = strings.TrimSuffix(d[i+1:], ")")
		d = strings.TrimSpace(d[:i])
	}
	words := strings.Fields(d)
	base := ""
	if len(words) > 0 {
		base = words[0]
	}
	for _, w := range words[1:] {
		if w == "unsigned" {
			flags |= flagUnsigned
		}
		if w == "zerofill" {
			flags |= flagZerofill
		}
	}
	n, _ := strconv.Atoi(strings.Split(args, ",")[0])
	charset = charsetBinary
	numeric := func(t uint8, l uint32) {
		typ, length = t, l
		flags |= flagNum | flagBinary
	}
	switch base {
	case "tinyint":
		numeric(typeTiny, 4)
	case "smallint":
		numeric(typeShort, 6)
	case "mediumint":
		numeric(typeInt24, 9)
	case "int":
		numeric(typeLong, 11)
	case "integer", "bigint":
		numeric(typeLongLong, 20)
	case "float":
		numeric(typeFloat, 12)
		decimals = 31
	case "double", "real":
		numeric(typeDouble, 22)
		decimals = 31
	case "decimal", "numeric":
		numeric(typeNewDecimal, uint32(n)+2)
		if parts := strings.Split(args, ","); len(parts) == 2 {
			dd, _ := strconv.Atoi(parts[1])
			decimals = uint8(dd)
		}
	case "date":
		typ, length, flags = typeDate, 10, flags|flagBinary
	case "datetime":
		typ, length, flags = typeDateTime, 19, flags|flagBinary
	case "timestamp":
		typ, length, flags = typeTimestamp, 19, flags|flagBinary|flagTimestamp
	case "time":
		typ, length, flags = typeTime, 10, flags|flagBinary
	case "year":
		numeric(typeYear, 4)
		flags |= flagUnsigned | flagZerofill
	case "char":
		typ, length, charset = typeString, uint32(max(n, 1))*4, charsetUTF8MB4
	case "varchar":
		typ, length, charset = typeVarString, uint32(n)*4, charsetUTF8MB4
	case "tinytext":
		typ, length, charset, flags = typeBlob, 1020, charsetUTF8MB4, flags|flagBlob
	case "text":
		typ, length, charset, flags = typeBlob, 262140, charsetUTF8MB4, flags|flagBlob
	case "mediumtext":
		typ, length, charset, flags = typeBlob, 67108860, charsetUTF8MB4, flags|flagBlob
	case "longtext":
		typ, length, charset, flags = typeBlob, 4294967295, charsetUTF8MB4, flags|flagBlob
	case "tinyblob":
		typ, length, flags = typeBlob, 255, flags|flagBlob|flagBinary
	case "blob":
		typ, length, flags = typeBlob, 65535, flags|flagBlob|flagBinary
	case "mediumblob":
		typ, length, flags = typeBlob, 16777215, flags|flagBlob|flagBinary
	case "longblob":
		typ, length, flags = typeBlob, 4294967295, flags|flagBlob|flagBinary
	case "binary":
		typ, length, flags = typeString, uint32(n), flags|flagBinary
	case "varbinary":
		typ, length, flags = typeVarString, uint32(n), flags|flagBinary
	case "json":
		typ, length, flags = typeJSON, 4294967295, flags|flagBlob|flagBinary
	case "bit":
		typ, length = typeBit, uint32(max(n, 1))
	default:
		typ, length, charset = typeVarString, 1020, charsetUTF8MB4
	}
	return typ, length, decimals, flags, charset
}

// mysqlTypeFromDecl reformate un type déclaré SQLite en type affiché par SHOW COLUMNS.
func mysqlTypeFromDecl(decl string) string {
	d := strings.ToLower(strings.TrimSpace(decl))
	i := strings.IndexByte(d, '(')
	if i < 0 {
		return d
	}
	words := strings.Fields(d[:i])
	if len(words) == 0 {
		return d
	}
	return strings.TrimSpace(words[0] + d[i:] + " " + strings.Join(words[1:], " "))
}

// columnMarkers lit les marqueurs « /*mysql:type|collation*/ » d'une définition de table stockée.
func columnMarkers(sql string) map[string][2]string {
	out := make(map[string][2]string)
	toks := lexSQL(sql)
	open := -1
	for i, t := range toks {
		if t.punct("(") {
			open = i
			break
		}
	}
	if open < 0 {
		return out
	}
	end := matchParen(toks, open)
	for _, item := range splitTop(toks[open+1:end], ",") {
		k := sigIndex(item, 0)
		if k >= len(item) {
			continue
		}
		name := strings.ToLower(item[k].name())
		for _, t := range item {
			if t.kind == tkComment && strings.HasPrefix(t.text, mysqlMarker) {
				body := strings.TrimSuffix(strings.TrimPrefix(t.text, mysqlMarker), "*/")
				typ, coll, _ := strings.Cut(body, "|")
				out[name] = [2]string{typ, coll}
			}
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// lockSyntaxError rend l'erreur de syntaxe MySQL d'une instruction LOCK TABLES, FLUSH, RESET ou PURGE mal
// formée.
func lockSyntaxError(near []token) *sqlError {
	return newSQLError(1064, "42000", "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server version for the right syntax to use near '"+joinTokens(spaced(near))+"' at line 1")
}

// lockTables applique LOCK TABLES t [[AS] alias] {READ [LOCAL] | [LOW_PRIORITY] WRITE} [, ...] : le verrou
// précédent est relâché et la transaction en cours validée, comme en MySQL, chaque table doit exister, puis
// une transaction SQLite tient le verrou et l'autorisateur de la connexion refuse toute autre table.
func (b *sqliteBackend) lockTables(toks []token) *sqlError {
	ss := sigTokens(toks)
	if len(ss) < 2 || (!ss[1].is("TABLES") && !ss[1].is("TABLE")) {
		return lockSyntaxError(ss)
	}
	locked := make(map[string]bool)
	var tables []string
	write := false
	parts := splitTop(ss[2:], ",")
	for _, part := range parts {
		if len(part) < 2 || (part[0].kind != tkWord && part[0].kind != tkIdent) {
			return lockSyntaxError(ss)
		}
		// L'alias éventuel est validé puis écarté : SQLite le résout vers la table réelle.
		k := 1
		if part[k].is("AS") {
			k++
			if k >= len(part) {
				return lockSyntaxError(part)
			}
			k++
		} else if part[k].kind == tkIdent || (part[k].kind == tkWord && !part[k].is("READ") && !part[k].is("WRITE") && !part[k].is("LOW_PRIORITY")) {
			k++
		}
		mode := part[k:]
		w := false
		switch {
		case len(mode) == 1 && mode[0].is("READ"), len(mode) == 2 && mode[0].is("READ") && mode[1].is("LOCAL"):
		case len(mode) == 1 && mode[0].is("WRITE"), len(mode) == 2 && mode[0].is("LOW_PRIORITY") && mode[1].is("WRITE"):
			w = true
		default:
			return lockSyntaxError(part)
		}
		table := part[0].name()
		tables = append(tables, table)
		// Seul le nom réel entre dans l'ensemble verrouillé : l'autorisateur reçoit de SQLite la table
		// résolue, jamais l'alias, et un alias homonyme d'une autre table ne doit pas la déverrouiller.
		// Clé détachée du texte de la requête, que le verrou survit.
		key := strings.Clone(foldASCII(table))
		locked[key] = locked[key] || w
		write = write || w
	}
	global := b.globalReadLock
	if global && write {
		return readLockConflict()
	}
	if err := b.unlockTables(); err != nil {
		return err
	}
	if global {
		// FLUSH TABLES WITH READ LOCK survit à LOCK TABLES : un échec rétablit le verrou global seul.
		defer func() {
			if !b.tablesLocked {
				_ = b.holdLock(nil, false, true)
			}
		}()
	}
	if b.db.inTransaction() {
		if _, _, err := b.run("COMMIT", nil); err != nil {
			return err
		}
	}
	for _, table := range tables {
		_, rows, err := b.run("SELECT 1 FROM sqlite_master WHERE type IN ('table', 'view') AND name = ? COLLATE NOCASE", []cell{textCell(table)})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return newSQLError(1146, "42S02", "Table '"+b.schemaName()+"."+table+"' doesn't exist")
		}
	}
	return b.holdLock(locked, write, global)
}

// holdLock prend le verrou de la connexion : la transaction SQLite qui le porte est ouverte si aucune ne
// l'est, puis l'autorisateur de LOCK TABLES est installé. locked est l'ensemble des tables verrouillées (nil :
// toute table est lisible) ; global signale le verrou de lecture de FLUSH TABLES WITH READ LOCK, qui refuse
// toute écriture.
func (b *sqliteBackend) holdLock(locked map[string]bool, write, global bool) *sqlError {
	b.lockWrite = write
	if !b.db.inTransaction() {
		if err := b.beginLockTx(); err != nil {
			return err
		}
	}
	if rc := b.db.setLockAuthorizer(b.sqlID); rc != sqliteOK {
		_, _, _ = b.run("ROLLBACK", nil)
		code, m := b.db.errInfo()
		return b.mapError(code, m, "")
	}
	b.lockedTables = locked
	b.globalReadLock = global
	b.tablesLocked = true
	return nil
}

// readLockConflict rend l'erreur MySQL d'une écriture tentée sous FLUSH TABLES WITH READ LOCK.
func readLockConflict() *sqlError {
	return newSQLError(1223, "HY000", "Can't execute the query because you have a conflicting read lock")
}

// flushOptions associe chaque option de FLUSH MySQL hors TABLES à son mot suivant obligatoire ("" s'il n'y
// en a pas).
var flushOptions = map[string]string{
	"PRIVILEGES": "", "STATUS": "", "HOSTS": "", "OPTIMIZER_COSTS": "", "USER_RESOURCES": "", "LOGS": "",
	"BINARY": "LOGS", "ENGINE": "LOGS", "ERROR": "LOGS", "GENERAL": "LOGS", "RELAY": "LOGS", "SLOW": "LOGS",
}

// flush applique FLUSH [NO_WRITE_TO_BINLOG | LOCAL] : TABLES [liste] WITH READ LOCK (ou FOR EXPORT) prend
// un verrou de lecture, global sans liste, sur les tables nommées sinon ; toute autre forme valide libère la
// mémoire de cache de la connexion SQLite, qui n'a ni cache de tables MySQL, ni privilèges, ni journaux.
func (b *sqliteBackend) flush(toks []token) *sqlError {
	ss := sigTokens(toks)
	k := 1
	if k < len(ss) && (ss[k].is("NO_WRITE_TO_BINLOG") || ss[k].is("LOCAL")) {
		k++
	}
	if k >= len(ss) {
		return lockSyntaxError(ss)
	}
	if ss[k].is("TABLES") || ss[k].is("TABLE") {
		rest := ss[k+1:]
		lock := false
		switch n := len(rest); {
		case n >= 3 && rest[n-3].is("WITH") && rest[n-2].is("READ") && rest[n-1].is("LOCK"):
			lock, rest = true, rest[:n-3]
		case n >= 2 && rest[n-2].is("FOR") && rest[n-1].is("EXPORT"):
			if n == 2 {
				return lockSyntaxError(ss)
			}
			lock, rest = true, rest[:n-2]
		}
		var tables []string
		if len(rest) > 0 {
			for _, part := range splitTop(rest, ",") {
				if len(part) != 1 || (part[0].kind != tkWord && part[0].kind != tkIdent) {
					return lockSyntaxError(ss)
				}
				tables = append(tables, part[0].name())
			}
		}
		if lock {
			return b.flushReadLock(tables)
		}
		b.invalidateMeta()
		b.db.releaseMemory()
		return nil
	}
	for _, part := range splitTop(ss[k:], ",") {
		if len(part) == 0 {
			return lockSyntaxError(ss)
		}
		next, ok := flushOptions[strings.ToUpper(part[0].name())]
		if !ok || part[0].kind != tkWord {
			return lockSyntaxError(ss)
		}
		n := 1
		if next != "" {
			if len(part) < 2 || !part[1].is(next) {
				return lockSyntaxError(ss)
			}
			n = 2
		}
		if part[0].is("RELAY") && len(part) == 5 && part[2].is("FOR") && part[3].is("CHANNEL") {
			n = 5
		}
		if len(part) != n {
			return lockSyntaxError(ss)
		}
	}
	b.invalidateMeta()
	b.db.releaseMemory()
	return nil
}

// flushReadLock applique FLUSH TABLES [liste] WITH READ LOCK : la transaction courante est validée, puis une
// transaction de lecture tient le verrou jusqu'à UNLOCK TABLES. Sans liste, toute écriture et toute
// modification de schéma sont refusées par 1223 ; avec une liste, les tables nommées sont verrouillées en
// lecture comme par LOCK TABLES ... READ. MySQL refuse l'instruction sous LOCK TABLES.
func (b *sqliteBackend) flushReadLock(tables []string) *sqlError {
	if b.tablesLocked {
		if b.globalReadLock && b.lockedTables == nil && len(tables) == 0 {
			return nil
		}
		return newSQLError(1192, "HY000", "Can't execute the given command because you have active locked tables or an active transaction")
	}
	if b.db.inTransaction() {
		if _, _, err := b.run("COMMIT", nil); err != nil {
			return err
		}
	}
	if len(tables) == 0 {
		return b.holdLock(nil, false, true)
	}
	locked := make(map[string]bool, len(tables))
	for _, table := range tables {
		_, rows, err := b.run("SELECT 1 FROM sqlite_master WHERE type IN ('table', 'view') AND name = ? COLLATE NOCASE", []cell{textCell(table)})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return newSQLError(1146, "42S02", "Table '"+b.schemaName()+"."+table+"' doesn't exist")
		}
		locked[strings.Clone(foldASCII(table))] = false
	}
	return b.holdLock(locked, false, false)
}

// reset applique RESET : QUERY CACHE et PERSIST vident les caches de métadonnées et la mémoire de cache de
// la connexion ; MASTER, SLAVE et REPLICA sont refusés par l'erreur MySQL 1235 (ER_NOT_SUPPORTED_YET),
// SQLite n'ayant ni journal binaire ni réplication ; toute autre cible est une erreur de syntaxe.
func (b *sqliteBackend) reset(toks []token) *sqlError {
	ss := sigTokens(toks)
	switch {
	case len(ss) == 3 && ss[1].is("QUERY") && ss[2].is("CACHE"),
		len(ss) >= 2 && ss[1].is("PERSIST") && (len(ss) == 2 || len(ss) == 3 || (len(ss) == 5 && ss[2].is("IF") && ss[3].is("EXISTS"))):
		b.invalidateMeta()
		b.db.releaseMemory()
		return nil
	case len(ss) == 2 && ss[1].is("MASTER"),
		len(ss) >= 2 && len(ss) <= 3 && (ss[1].is("SLAVE") || ss[1].is("REPLICA")) && (len(ss) == 2 || ss[2].is("ALL")):
		return newSQLError(1235, "42000", "This version of MySQL doesn't yet support 'RESET "+strings.ToUpper(ss[1].name())+" on the SQLite backend'")
	}
	return lockSyntaxError(ss)
}

// purge valide la syntaxe de PURGE {BINARY | MASTER} LOGS {TO 'journal' | BEFORE expr} et refuse
// l'opération par l'erreur MySQL 1235 (ER_NOT_SUPPORTED_YET), SQLite n'ayant pas de journal binaire.
func purge(toks []token) *sqlError {
	ss := sigTokens(toks)
	if len(ss) < 5 || (!ss[1].is("BINARY") && !ss[1].is("MASTER")) || !ss[2].is("LOGS") ||
		(!ss[3].is("TO") && !ss[3].is("BEFORE")) || (ss[3].is("TO") && (len(ss) != 5 || ss[4].kind != tkString)) {
		return lockSyntaxError(ss)
	}
	return newSQLError(1235, "42000", "This version of MySQL doesn't yet support 'PURGE LOGS on the SQLite backend'")
}

// beginLockTx ouvre la transaction SQLite qui porte le verrou : BEGIN IMMEDIATE prend le verrou d'écriture
// du fichier, BEGIN DEFERRED suivi d'une lecture du schéma prend le verrou partagé.
func (b *sqliteBackend) beginLockTx() *sqlError {
	if b.lockWrite {
		_, _, err := b.run("BEGIN IMMEDIATE", nil)
		return err
	}
	if _, _, err := b.run("BEGIN DEFERRED", nil); err != nil {
		return err
	}
	if _, _, err := b.run("SELECT count(*) FROM sqlite_master", nil); err != nil {
		_, _, _ = b.run("ROLLBACK", nil)
		return err
	}
	return nil
}

// unlockTables relâche le verrou de LOCK TABLES : la transaction qui le porte est validée, comme UNLOCK
// TABLES valide en MySQL la transaction ouverte sous verrou, et l'autorisateur de base est rétabli.
func (b *sqliteBackend) unlockTables() *sqlError {
	if !b.tablesLocked {
		return nil
	}
	b.tablesLocked = false
	b.lockedTables = nil
	b.globalReadLock = false
	b.db.setLockAuthorizer(0)
	if b.db.inTransaction() {
		_, _, err := b.run("COMMIT", nil)
		return err
	}
	return nil
}

// endLockedTx applique COMMIT ou ROLLBACK sous LOCK TABLES : MySQL ne relâche pas les verrous, la
// transaction du verrou est donc rouverte. En autocommit, chaque instruction est déjà validée par MySQL et
// ROLLBACK n'a rien à annuler : les écritures sont conservées.
func (b *sqliteBackend) endLockedTx(op string) *sqlError {
	if op == "ROLLBACK" && b.autocommit {
		return nil
	}
	if b.db.inTransaction() {
		if _, _, err := b.run(op, nil); err != nil {
			return err
		}
	}
	return b.beginLockTx()
}

// Accès soumis à l'autorisateur de LOCK TABLES : lecture d'une table, écriture d'une table, création ou
// suppression d'un objet de schéma (table, vue, déclencheur) hors de l'ensemble verrouillé.
const (
	lockAccessRead   = 0
	lockAccessWrite  = 1
	lockAccessSchema = 2
)

// allowLockedAccess, appelé par l'autorisateur sous LOCK TABLES, admet l'accès kind à table ; un refus est
// consigné dans lockDenied avec l'erreur MySQL (1223 sous FLUSH TABLES WITH READ LOCK, 1100 ou 1099 sinon).
// Hors du verrou global, un objet de schéma créé ou supprimé n'est pas soumis à l'ensemble verrouillé.
func (b *sqliteBackend) allowLockedAccess(table string, kind int) bool {
	if b.globalReadLock && kind != lockAccessRead {
		if b.lockDenied == nil {
			b.lockDenied = readLockConflict()
		}
		return false
	}
	if b.lockedTables == nil || kind == lockAccessSchema {
		return true
	}
	write := kind == lockAccessWrite
	w, ok := b.lockedTables[foldASCII(table)]
	switch {
	case !ok:
		if b.lockDenied == nil {
			b.lockDenied = newSQLError(1100, "HY000", "Table '"+table+"' was not locked with LOCK TABLES")
		}
		return false
	case write && !w:
		if b.lockDenied == nil {
			b.lockDenied = newSQLError(1099, "HY000", "Table '"+table+"' was locked with a READ lock and can't be updated")
		}
		return false
	}
	return true
}

// runAlter applique les étapes d'ALTER TABLE de façon atomique (SAVEPOINT) : une erreur annule toutes les
// étapes, reconstruction de table comprise.
func (b *sqliteBackend) runAlter(steps []alterStep) *sqlError {
	if len(steps) == 1 && steps[0].redef == nil {
		_, _, err := b.run(steps[0].sql, nil)
		return err
	}
	if _, _, err := b.run("SAVEPOINT mysqli_alter", nil); err != nil {
		return err
	}
	for _, st := range steps {
		var err *sqlError
		if st.redef != nil {
			err = b.rebuildColumn(st.redef)
		} else {
			_, _, err = b.run(st.sql, nil)
		}
		if err != nil {
			_, _, _ = b.run("ROLLBACK TO mysqli_alter", nil)
			_, _, _ = b.run("RELEASE mysqli_alter", nil)
			return err
		}
	}
	_, _, err := b.run("RELEASE mysqli_alter", nil)
	return err
}

// isPrimaryItem signale un élément de définition de table qui déclare la clé primaire de la table
// (« PRIMARY KEY (...) » ou « CONSTRAINT nom PRIMARY KEY (...) ») et rend ses colonnes.
func isPrimaryItem(item []token) ([]string, bool) {
	ss := sigTokens(item)
	if len(ss) > 0 && ss[0].is("CONSTRAINT") {
		ss = ss[min(2, len(ss)):]
	}
	if len(ss) < 3 || !ss[0].is("PRIMARY") || !ss[1].is("KEY") || !ss[2].punct("(") {
		return nil, false
	}
	end := matchParen(ss, 2)
	var cols []string
	for _, part := range splitTop(ss[3:end], ",") {
		if len(part) > 0 {
			cols = append(cols, part[0].name())
		}
	}
	return cols, true
}

// hasWordPair signale la suite de mots « a b » au niveau zéro d'une définition de colonne, avant toute
// clause REFERENCES ou CHECK.
func hasWordPair(ss []token, a, b string) bool {
	for i, t := range ss {
		if t.is("REFERENCES") || t.is("CHECK") {
			return false
		}
		if t.is(a) && (b == "" || (i+1 < len(ss) && ss[i+1].is(b))) {
			return true
		}
	}
	return false
}

// withKeyWords insère des mots-clés dans une définition de colonne MySQL, avant toute clause REFERENCES ou
// CHECK, qui consomme la fin de la définition.
func withKeyWords(def []token, words ...string) []token {
	at := len(def)
	for i, t := range def {
		if t.is("REFERENCES") || t.is("CHECK") {
			at = i
			break
		}
	}
	out := append([]token(nil), def[:at]...)
	for _, w := range words {
		out = append(out, token{kind: tkWord, text: w})
	}
	return append(out, def[at:]...)
}

// rebuildColumn applique une redéfinition de colonne (MODIFY, CHANGE, ALTER COLUMN SET ou DROP DEFAULT) que
// SQLite n'exprime pas, par la reconstruction documentée par SQLite : la définition de la table est relue dans
// sqlite_master, l'élément de la colonne y est remplacé par sa nouvelle traduction, ou par sa définition en
// place à la seule clause DEFAULT près, une table __php127_alter_<table> reçoit les lignes,
// l'ancienne table est supprimée puis remplacée par la nouvelle, et ses index, déclencheurs et vues sont
// recréés. Le contrôle des clés étrangères est suspendu pendant l'échange, afin que la suppression de
// l'ancienne table ne détruise ni ne refuse les lignes qui la référencent. L'appelant tient un SAVEPOINT : une
// contrainte violée par les lignes existantes annule toute la reconstruction.
func (b *sqliteBackend) rebuildColumn(r *columnRedef) *sqlError {
	_, rows, err := b.run("SELECT name, sql FROM sqlite_master WHERE type = 'table' AND name = ? COLLATE NOCASE", []cell{textCell(r.table)})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		if _, tmp, err := b.run("SELECT 1 FROM sqlite_temp_master WHERE type = 'table' AND name = ? COLLATE NOCASE", []cell{textCell(r.table)}); err == nil && len(tmp) > 0 {
			return newSQLError(1235, "42000", "ALTER TABLE MODIFY, CHANGE or ALTER COLUMN on a temporary table is not supported by the SQLite translator")
		}
		return newSQLError(1146, "42S02", "Table '"+b.schemaName()+"."+r.table+"' doesn't exist")
	}
	table, createSQL := rows[0][0].s, rows[0][1].s
	toks := lexSQL(createSQL)
	open := 0
	for open < len(toks) && !toks[open].punct("(") {
		open++
	}
	end := matchParen(toks, open)
	if end >= len(toks) {
		return newSQLError(1105, "HY000", "Unable to read the definition of table '"+table+"'")
	}
	items := splitTop(toks[open+1:end], ",")
	column := r.column
	if !r.alterDefaultOnly {
		column = r.def[0].name()
	}
	target := -1
	otherPK := false
	tablePK := -1
	var pkCols []string
	for i, item := range items {
		if cols, ok := isPrimaryItem(item); ok {
			tablePK, pkCols = i, cols
			continue
		}
		ss := sigTokens(item)
		if len(ss) == 0 {
			continue
		}
		if strings.EqualFold(ss[0].name(), column) {
			target = i
		} else if hasWordPair(ss, "PRIMARY", "KEY") {
			otherPK = true
		}
	}
	if target < 0 {
		return newSQLError(1054, "42S22", "Unknown column '"+column+"' in '"+table+"'")
	}
	var colSQL string
	if r.alterDefaultOnly {
		// Type, nullabilité, clé, auto-incrément, collation et marqueur MySQL restent ceux de la définition
		// SQLite en place : seule la clause DEFAULT est retirée, puis remplacée si une valeur est donnée.
		colSQL = withDefault(items[target], r.deflt)
	} else {
		var err *sqlError
		if items, target, colSQL, err = b.redefinedColumn(r, items, target, tablePK, pkCols, otherPK); err != nil {
			return err
		}
	}
	tmp := "__php127_alter_" + table
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = joinTokens(item)
	}
	parts[target] = "\n  " + colSQL
	newSQL := "CREATE TABLE " + quoteIdent(tmp) + " (" + strings.Join(parts, ",") + ")" + joinTokens(toks[end+1:])
	return b.swapTable(table, tmp, newSQL)
}

// withDefault rend la définition SQLite d'une colonne item, débarrassée de sa clause DEFAULT de premier
// niveau, augmentée de clause si elle n'est pas vide ; la clause est insérée avant le marqueur de type MySQL,
// qui reste le dernier élément de la colonne, et le reste du texte est conservé tel quel.
func withDefault(item []token, clause string) string {
	var b strings.Builder
	pending := clause != ""
	for i := 0; i < len(item); i++ {
		t := item[i]
		if t.kind == tkComment && strings.HasPrefix(t.text, mysqlMarker) && pending {
			b.WriteString(clause + " ")
			pending = false
		}
		if t.is("DEFAULT") {
			if j := sigIndex(item, i+1); j < len(item) {
				switch v := item[j]; {
				case v.punct("("):
					j = matchParen(item, j)
				case v.punct("-") || v.punct("+"):
					j = sigIndex(item, j+1)
				}
				i = j
				continue
			}
		}
		b.WriteString(t.text)
	}
	if pending {
		b.WriteString(" " + clause)
	}
	return strings.TrimSpace(b.String())
}

// redefinedColumn traduit la nouvelle définition MySQL de MODIFY ou CHANGE contre la définition en place de
// la colonne items[target] et rend les éléments de table, l'indice de la colonne et sa traduction SQLite.
func (b *sqliteBackend) redefinedColumn(r *columnRedef, items [][]token, target, tablePK int, pkCols []string, otherPK bool) ([][]token, int, string, *sqlError) {
	column := r.def[0].name()
	old := sigTokens(items[target])
	coll := "utf8mb4_0900_ai_ci"
	if m, ok := columnMarkers("CREATE TABLE t (" + joinTokens(items[target]) + ")")[foldASCII(column)]; ok && m[1] != "" {
		coll = m[1]
	}
	// MODIFY et CHANGE ne retirent pas les index d'une colonne : une clé primaire ou une contrainte UNIQUE
	// déclarée sur la colonne elle-même est reportée sur la nouvelle définition.
	def := r.def
	tr := b.translator()
	probe := tr.translateColumn(def, coll)
	if tr.err != nil {
		return nil, 0, "", tr.err
	}
	if hasWordPair(old, "PRIMARY", "KEY") && !probe.pk && !probe.autoInc {
		def = withKeyWords(def, "PRIMARY", "KEY")
	}
	if hasWordPair(old, "UNIQUE", "") && !probe.unique {
		def = withKeyWords(def, "UNIQUE")
	}
	tr = b.translator()
	col := tr.translateColumn(def, coll)
	if tr.err != nil {
		return nil, 0, "", tr.err
	}
	if col.pk || col.autoInc {
		singlePK := tablePK >= 0 && len(pkCols) == 1 && strings.EqualFold(pkCols[0], column)
		if otherPK || (tablePK >= 0 && !singlePK) {
			return nil, 0, "", newSQLError(1068, "42000", "Multiple primary key defined")
		}
		if singlePK {
			items = append(items[:tablePK:tablePK], items[tablePK+1:]...)
			if target > tablePK {
				target--
			}
		}
	}
	return items, target, col.sql, nil
}

// swapTable remplace table par une table de même nom créée par newSQL sous le nom provisoire tmp : les lignes
// y sont copiées, l'ancienne table est supprimée puis la nouvelle renommée, et index, déclencheurs, vues et
// compteur d'auto-incrément sont rétablis.
func (b *sqliteBackend) swapTable(table, tmp, newSQL string) *sqlError {
	_, saved, err := b.run("SELECT type, name, sql FROM sqlite_master WHERE sql IS NOT NULL AND (type IN ('view', 'trigger') OR (type = 'index' AND tbl_name = ?)) ORDER BY rowid", []cell{textCell(table)})
	if err != nil {
		return err
	}
	_, info, err := b.run("PRAGMA table_info("+quoteIdent(table)+")", nil)
	if err != nil {
		return err
	}
	names := make([]string, len(info))
	for i, c := range info {
		names[i] = quoteIdent(c[1].s)
	}
	var seq int64
	if _, s, err := b.run("SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'sqlite_sequence'", nil); err == nil && len(s) > 0 {
		if _, s, err := b.run("SELECT seq FROM sqlite_sequence WHERE name = ?", []cell{textCell(table)}); err == nil && len(s) > 0 {
			seq = s[0][0].i
		}
	}

	if on, _ := b.db.configFlag(sqliteConfigEnableFKey, -1); on {
		b.db.setForeignKeys(false)
		defer b.db.setForeignKeys(true)
	}
	if b.lockedTables != nil {
		b.lockedTables[foldASCII(tmp)] = true
		defer delete(b.lockedTables, foldASCII(tmp))
	}
	for _, s := range saved {
		if s[0].s != "index" {
			if _, _, err := b.run("DROP "+strings.ToUpper(s[0].s)+" IF EXISTS "+quoteIdent(s[1].s), nil); err != nil {
				return err
			}
		}
	}
	if _, _, err := b.run(newSQL, nil); err != nil {
		return err
	}
	cols := strings.Join(names, ", ")
	if _, _, err := b.run("INSERT INTO "+quoteIdent(tmp)+" ("+cols+") SELECT "+cols+" FROM "+quoteIdent(table), nil); err != nil {
		if err.errno == 1048 {
			// MySQL refuse en mode strict le passage en NOT NULL d'une colonne qui contient NULL.
			return newSQLError(1138, "22004", "Invalid use of NULL value")
		}
		return err
	}
	for _, sql := range []string{"DROP TABLE " + quoteIdent(table), "ALTER TABLE " + quoteIdent(tmp) + " RENAME TO " + quoteIdent(table)} {
		if _, _, err := b.run(sql, nil); err != nil {
			return err
		}
	}
	for _, s := range saved {
		if _, _, err := b.run(s[2].s, nil); err != nil {
			return err
		}
	}
	if seq > 0 && strings.Contains(strings.ToUpper(newSQL), "AUTOINCREMENT") {
		if _, cur, err := b.run("SELECT seq FROM sqlite_sequence WHERE name = ?", []cell{textCell(table)}); err != nil {
			return err
		} else if len(cur) == 0 {
			_, _, err = b.run("INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)", []cell{textCell(table), intCell(seq)})
			if err != nil {
				return err
			}
		} else if cur[0][0].i < seq {
			if _, _, err := b.run("UPDATE sqlite_sequence SET seq = ? WHERE name = ?", []cell{intCell(seq), textCell(table)}); err != nil {
				return err
			}
		}
	}
	return nil
}
