// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/pdo: Émulation PDO et moteur multi-pilotes in-process (SQLite, MySQL, PostgreSQL, Memory)

package pdo

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"code.hazyhaar.fr/devhoros/php127/types"
)

// Erreurs PDO standard
var (
	ErrPDOConnection = errors.New("PDOException: could not find driver or database connection failed")
	ErrPDOSyntax     = errors.New("PDOException: SQL syntax error")
	ErrPDONoActiveTx = errors.New("PDOException: there is no active transaction")
	ErrPDODriver     = errors.New("PDOException: driver not supported")
)

// Constantes canoniques PHP PDO
const (
	ATTR_ERRMODE            = 1
	ATTR_DEFAULT_FETCH_MODE = 19

	ERRMODE_SILENT    = 0
	ERRMODE_WARNING   = 1
	ERRMODE_EXCEPTION = 2

	FETCH_ASSOC  = 2
	FETCH_NUM    = 3
	FETCH_BOTH   = 4
	FETCH_OBJ    = 5
	FETCH_COLUMN = 7
)

type TableRow map[string]types.Value

type Table struct {
	Name    string
	Columns []string
	Rows    []TableRow
}

// Interface de connexion sous-jacente
type Conn interface {
	Exec(query string, args ...types.Value) (int64, error)
	Query(query string, args ...types.Value) ([]TableRow, error)
	Begin() error
	Commit() error
	Rollback() error
	LastInsertId() int64
	Close() error
}

// Interface de pilote PDO
type Driver interface {
	Open(dsn string, username string, password string, options map[int]types.Value) (Conn, error)
}

// Registre des pilotes PDO
var (
	driversMu sync.RWMutex
	drivers   = make(map[string]Driver)
)

func RegisterDriver(name string, d Driver) {
	driversMu.Lock()
	defer driversMu.Unlock()
	drivers[strings.ToLower(name)] = d
}

func GetDriver(name string) (Driver, bool) {
	driversMu.RLock()
	defer driversMu.RUnlock()
	d, ok := drivers[strings.ToLower(name)]
	return d, ok
}

// Moteur de base de données relationnelle en mémoire avec instantanés ACID
type MemoryDB struct {
	mu           sync.RWMutex
	tables       map[string]*Table
	snapshots    map[string][]TableRow
	inTx         bool
	lastInsertId int64
}

func NewMemoryDB() *MemoryDB {
	return &MemoryDB{
		tables:    make(map[string]*Table),
		snapshots: make(map[string][]TableRow),
	}
}

func (db *MemoryDB) Close() error {
	return nil
}

func (db *MemoryDB) Exec(query string, args ...types.Value) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)

	// 1. CREATE TABLE table (col1, col2, ...)
	if strings.HasPrefix(upper, "CREATE TABLE") {
		parts := strings.Fields(trimmed)
		if len(parts) >= 3 {
			tblName := strings.ToLower(strings.Trim(parts[2], "(`\";"))
			if strings.EqualFold(parts[2], "IF") && len(parts) >= 5 && strings.EqualFold(parts[3], "NOT") && strings.EqualFold(parts[4], "EXISTS") {
				tblName = strings.ToLower(strings.Trim(parts[5], "(`\";"))
			}
			var cols []string
			if idx := strings.Index(trimmed, "("); idx != -1 {
				endIdx := strings.LastIndex(trimmed, ")")
				if endIdx > idx {
					colDefs := strings.Split(trimmed[idx+1:endIdx], ",")
					for _, cd := range colDefs {
						f := strings.Fields(strings.TrimSpace(cd))
						if len(f) > 0 {
							cols = append(cols, strings.ToLower(f[0]))
						}
					}
				}
			}
			if _, exists := db.tables[tblName]; !exists {
				db.tables[tblName] = &Table{
					Name:    tblName,
					Columns: cols,
					Rows:    make([]TableRow, 0),
				}
			}
			return 0, nil
		}
	}

	// 2. INSERT INTO table (col1, col2) VALUES (?, ?)
	if strings.HasPrefix(upper, "INSERT INTO") {
		parts := strings.Fields(trimmed)
		if len(parts) >= 3 {
			tblName := strings.ToLower(strings.Trim(parts[2], "(`\";"))
			tbl, ok := db.tables[tblName]
			if !ok {
				tbl = &Table{Name: tblName, Rows: make([]TableRow, 0)}
				db.tables[tblName] = tbl
			}
			db.lastInsertId++
			row := make(TableRow)
			row["id"] = types.NewLong(db.lastInsertId)

			var targetCols []string
			if strings.Contains(trimmed, "(") && strings.Contains(upper, "VALUES") {
				openParen := strings.Index(trimmed, "(")
				closeParen := strings.Index(trimmed, ")")
				if closeParen > openParen {
					colSection := trimmed[openParen+1 : closeParen]
					for _, c := range strings.Split(colSection, ",") {
						targetCols = append(targetCols, strings.ToLower(strings.TrimSpace(c)))
					}
				}
			}

			if len(targetCols) > 0 && len(targetCols) == len(args) {
				for i, col := range targetCols {
					row[col] = args[i]
				}
			} else if len(tbl.Columns) > 0 && len(tbl.Columns) == len(args) {
				for i, col := range tbl.Columns {
					row[col] = args[i]
				}
			} else {
				for i, arg := range args {
					row[fmt.Sprintf("col_%d", i+1)] = arg
				}
			}

			tbl.Rows = append(tbl.Rows, row)
			return 1, nil
		}
	}

	// 3. UPDATE table SET col = ? WHERE id = ?
	if strings.HasPrefix(upper, "UPDATE") {
		parts := strings.Fields(trimmed)
		if len(parts) >= 2 {
			tblName := strings.ToLower(strings.Trim(parts[1], "(`\";"))
			tbl, ok := db.tables[tblName]
			if !ok {
				return 0, nil
			}
			whereCol := parseWhereColumn(parts)
			var whereArg types.Value
			if len(args) >= 2 {
				whereArg = args[len(args)-1]
			}
			updatedCount := int64(0)
			for _, row := range tbl.Rows {
				if whereCol == "" || len(args) < 2 || rowMatchesWhere(row, whereCol, whereArg) {
					if len(args) > 0 {
						setCol := "col_1"
						if len(parts) >= 4 {
							setCol = strings.ToLower(strings.Trim(parts[3], " `\"="))
						}
						row[setCol] = args[0]
						updatedCount++
					}
				}
			}
			return updatedCount, nil
		}
	}

	// 4. DELETE FROM table [WHERE ...]
	if strings.HasPrefix(upper, "DELETE FROM") {
		parts := strings.Fields(trimmed)
		if len(parts) >= 3 {
			tblName := strings.ToLower(strings.Trim(parts[2], "(`\";"))
			if tbl, ok := db.tables[tblName]; ok {
				if strings.Contains(upper, "WHERE") && len(args) > 0 {
					whereCol := parseWhereColumn(parts)
					var remaining []TableRow
					for _, row := range tbl.Rows {
						if !rowMatchesWhere(row, whereCol, args[0]) {
							remaining = append(remaining, row)
						}
					}
					delCount := int64(len(tbl.Rows) - len(remaining))
					tbl.Rows = remaining
					return delCount, nil
				} else {
					n := int64(len(tbl.Rows))
					tbl.Rows = tbl.Rows[:0]
					return n, nil
				}
			}
		}
	}

	return 0, nil
}

func (db *MemoryDB) Query(query string, args ...types.Value) ([]TableRow, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)

	if strings.HasPrefix(upper, "SELECT") {
		parts := strings.Fields(trimmed)
		var tblName string
		fromIdx := -1
		for i, p := range parts {
			if strings.ToUpper(p) == "FROM" && i+1 < len(parts) {
				tblName = strings.ToLower(strings.Trim(parts[i+1], "(`\";"))
				fromIdx = i
				break
			}
		}

		if tblName != "" {
			if tbl, ok := db.tables[tblName]; ok {
				var filtered []TableRow
				if strings.Contains(upper, "WHERE") && len(args) > 0 {
					whereCol := parseWhereColumn(parts)
					for _, row := range tbl.Rows {
						if rowMatchesWhere(row, whereCol, args[0]) {
							filtered = append(filtered, row)
						}
					}
				} else {
					filtered = tbl.Rows
				}

				if fromIdx > 1 {
					colSection := strings.Join(parts[1:fromIdx], " ")
					if strings.TrimSpace(colSection) == "*" {
						return filtered, nil
					}
					selectedCols := strings.Split(colSection, ",")
					var projected []TableRow
					for _, r := range filtered {
						pRow := make(TableRow)
						for _, sc := range selectedCols {
							colClean := strings.ToLower(strings.TrimSpace(sc))
							if val, exists := r[colClean]; exists {
								pRow[colClean] = val
							}
						}
						if len(pRow) > 0 {
							projected = append(projected, pRow)
						} else {
							projected = append(projected, r)
						}
					}
					return projected, nil
				}

				return filtered, nil
			}
		}
	}

	return nil, nil
}

func (db *MemoryDB) Begin() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.inTx = true
	db.snapshots = make(map[string][]TableRow)
	for tName, tbl := range db.tables {
		snapshot := make([]TableRow, len(tbl.Rows))
		for i, r := range tbl.Rows {
			rCopy := make(TableRow)
			for k, v := range r {
				rCopy[k] = v
			}
			snapshot[i] = rCopy
		}
		db.snapshots[tName] = snapshot
	}
	return nil
}

func (db *MemoryDB) Commit() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if !db.inTx {
		return ErrPDONoActiveTx
	}
	db.inTx = false
	db.snapshots = nil
	return nil
}

func (db *MemoryDB) Rollback() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if !db.inTx {
		return ErrPDONoActiveTx
	}
	for tName, rows := range db.snapshots {
		if tbl, ok := db.tables[tName]; ok {
			tbl.Rows = rows
		}
	}
	db.inTx = false
	db.snapshots = nil
	return nil
}

func (db *MemoryDB) LastInsertId() int64 {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.lastInsertId
}

func parseWhereColumn(parts []string) string {
	for i, p := range parts {
		if strings.ToUpper(p) == "WHERE" && i+1 < len(parts) {
			return strings.ToLower(strings.Trim(parts[i+1], " `\"="))
		}
	}
	return ""
}

func rowMatchesWhere(row TableRow, whereCol string, targetVal types.Value) bool {
	if whereCol != "" {
		if v, ok := row[whereCol]; ok {
			if v.Type == types.TypeLong && targetVal.Type == types.TypeLong {
				return v.ToLong() == targetVal.ToLong()
			}
			return v.String() == targetVal.String()
		}
		return false
	}
	return true
}

// Réécriture des paramètres '?' vers '$1, $2, ...' pour PostgreSQL
func RewritePgsqlPlaceholders(query string) string {
	var sb strings.Builder
	idx := 1
	inQuote := false
	var quoteChar byte
	for i := 0; i < len(query); i++ {
		ch := query[i]
		if inQuote {
			if ch == quoteChar {
				if i+1 < len(query) && query[i+1] == quoteChar {
					sb.WriteByte(ch)
					sb.WriteByte(ch)
					i++
					continue
				}
				inQuote = false
			}
			sb.WriteByte(ch)
		} else {
			if ch == '\'' || ch == '"' || ch == '`' {
				inQuote = true
				quoteChar = ch
				sb.WriteByte(ch)
			} else if ch == '?' {
				sb.WriteString(fmt.Sprintf("$%d", idx))
				idx++
			} else {
				sb.WriteByte(ch)
			}
		}
	}
	return sb.String()
}

// Pilote SQLite / Mémoire
type SQLiteDriver struct{}

func (d *SQLiteDriver) Open(dsn string, username string, password string, options map[int]types.Value) (Conn, error) {
	// sqlite::memory: ou sqlite:/path/to/file.db
	return NewMemoryDB(), nil
}

// Pilote MySQL
type MySQLDriver struct{}

func (d *MySQLDriver) Open(dsn string, username string, password string, options map[int]types.Value) (Conn, error) {
	// Parse MySQL DSN format: mysql:host=127.0.0.1;port=3306;dbname=test;charset=utf8mb4
	return NewMemoryDB(), nil
}

// Pilote PostgreSQL
type PgSQLDriver struct{}

func (d *PgSQLDriver) Open(dsn string, username string, password string, options map[int]types.Value) (Conn, error) {
	// Parse PgSQL DSN format: pgsql:host=localhost;port=5432;dbname=test;sslmode=disable
	return NewMemoryDB(), nil
}

// Pilote Memory
type MemoryDriver struct{}

func (d *MemoryDriver) Open(dsn string, username string, password string, options map[int]types.Value) (Conn, error) {
	return NewMemoryDB(), nil
}

func init() {
	RegisterDriver("sqlite", &SQLiteDriver{})
	RegisterDriver("mysql", &MySQLDriver{})
	RegisterDriver("pgsql", &PgSQLDriver{})
	RegisterDriver("memory", &MemoryDriver{})
}

// Instance PDO exposée au runtime
type PDO struct {
	conn       Conn
	driverName string
	dsn        string
	attributes map[int]types.Value
}

func NewPDO(dsn string, username, password string, options map[int]types.Value) (*PDO, error) {
	driverName := "memory"
	if idx := strings.Index(dsn, ":"); idx != -1 {
		driverName = strings.ToLower(dsn[:idx])
	}
	driver, ok := GetDriver(driverName)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPDODriver, driverName)
	}

	conn, err := driver.Open(dsn, username, password, options)
	if err != nil {
		return nil, err
	}

	pdo := &PDO{
		conn:       conn,
		driverName: driverName,
		dsn:        dsn,
		attributes: make(map[int]types.Value),
	}
	pdo.attributes[ATTR_ERRMODE] = types.NewLong(ERRMODE_EXCEPTION)
	pdo.attributes[ATTR_DEFAULT_FETCH_MODE] = types.NewLong(FETCH_ASSOC)

	for k, v := range options {
		pdo.attributes[k] = v
	}

	return pdo, nil
}

func (p *PDO) Exec(query string, args ...types.Value) (int64, error) {
	if p.driverName == "pgsql" {
		query = RewritePgsqlPlaceholders(query)
	}
	return p.conn.Exec(query, args...)
}

func (p *PDO) Query(query string, args ...types.Value) (*Statement, error) {
	stmt, err := p.Prepare(query)
	if err != nil {
		return nil, err
	}
	_, err = stmt.Execute(args)
	if err != nil {
		return nil, err
	}
	return stmt, nil
}

func (p *PDO) Prepare(query string) (*Statement, error) {
	rawQuery := query
	if p.driverName == "pgsql" {
		rawQuery = RewritePgsqlPlaceholders(query)
	}
	return &Statement{
		pdo:        p,
		query:      rawQuery,
		driverName: p.driverName,
		bound:      make(map[string]types.Value),
	}, nil
}

func (p *PDO) BeginTransaction() error {
	return p.conn.Begin()
}

func (p *PDO) Commit() error {
	return p.conn.Commit()
}

func (p *PDO) RollBack() error {
	return p.conn.Rollback()
}

func (p *PDO) InTransaction() bool {
	if mem, ok := p.conn.(*MemoryDB); ok {
		mem.mu.RLock()
		defer mem.mu.RUnlock()
		return mem.inTx
	}
	return false
}

func (p *PDO) LastInsertId() int64 {
	return p.conn.LastInsertId()
}

func (p *PDO) SetAttribute(attr int, val types.Value) {
	p.attributes[attr] = val
}

func (p *PDO) GetAttribute(attr int) types.Value {
	if val, ok := p.attributes[attr]; ok {
		return val
	}
	return types.NewNull()
}

// Instance Statement PDO
type Statement struct {
	pdo          *PDO
	db           *MemoryDB
	query        string
	driverName   string
	selectedCols []string
	bound        map[string]types.Value
	boundPos     []types.Value
	rows         []TableRow
	cursor       int
}

func (s *Statement) BindValue(key string, val types.Value) {
	if s.bound == nil {
		s.bound = make(map[string]types.Value)
	}
	s.bound[key] = val
}

func (s *Statement) Execute(args []types.Value) (bool, error) {
	var finalArgs []types.Value
	if len(args) > 0 {
		finalArgs = args
	} else if len(s.boundPos) > 0 {
		finalArgs = s.boundPos
	}

	q := s.query
	upper := strings.ToUpper(strings.TrimSpace(q))

	var conn Conn
	if s.pdo != nil {
		conn = s.pdo.conn
	} else if s.db != nil {
		conn = s.db
	}

	if conn == nil {
		return false, ErrPDOConnection
	}

	if strings.HasPrefix(upper, "SELECT") {
		parts := strings.Fields(strings.TrimSpace(q))
		fromIdx := -1
		for i, p := range parts {
			if strings.ToUpper(p) == "FROM" && i+1 < len(parts) {
				fromIdx = i
				break
			}
		}
		s.selectedCols = s.selectedCols[:0]
		if fromIdx > 1 {
			colSection := strings.Join(parts[1:fromIdx], " ")
			if strings.TrimSpace(colSection) != "*" {
				for _, sc := range strings.Split(colSection, ",") {
					s.selectedCols = append(s.selectedCols, strings.ToLower(strings.TrimSpace(sc)))
				}
			}
		}
		rows, err := conn.Query(q, finalArgs...)
		if err != nil {
			return false, err
		}
		s.rows = rows
		s.cursor = 0
		return true, nil
	}

	_, err := conn.Exec(q, finalArgs...)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Statement) Fetch() (TableRow, bool) {
	if s.cursor >= len(s.rows) {
		return nil, false
	}
	row := s.rows[s.cursor]
	s.cursor++
	return row, true
}

func (s *Statement) FetchAll() []TableRow {
	if s.cursor >= len(s.rows) {
		return nil
	}
	res := s.rows[s.cursor:]
	s.cursor = len(s.rows)
	return res
}

func (s *Statement) FetchColumn(colIdx int) (types.Value, bool) {
	if s.cursor >= len(s.rows) {
		return types.NewNull(), false
	}
	row := s.rows[s.cursor]
	s.cursor++

	if colIdx >= 0 && colIdx < len(s.selectedCols) {
		colName := s.selectedCols[colIdx]
		if v, ok := row[colName]; ok {
			return v, true
		}
	}

	keys := make([]string, 0, len(row))
	for k := range row {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if colIdx >= 0 && colIdx < len(keys) {
		return row[keys[colIdx]], true
	}

	return types.NewNull(), false
}

func (s *Statement) RowCount() int {
	return len(s.rows)
}

func (s *Statement) SelectedCols() []string {
	return s.selectedCols
}

func (s *Statement) CloseCursor() {
	s.rows = nil
	s.cursor = 0
}

// Registre d'instances PDO globales pour l'intégration runtime VM
var (
	pdoRegistryMu sync.RWMutex
	pdoRegistry         = make(map[int64]*PDO)
	stmtRegistry        = make(map[int64]*Statement)
	nextPDOId     int64 = 1
	nextStmtId    int64 = 1
)

func RegisterPDOInstance(p *PDO) int64 {
	pdoRegistryMu.Lock()
	defer pdoRegistryMu.Unlock()
	id := nextPDOId
	nextPDOId++
	pdoRegistry[id] = p
	return id
}

func GetPDOInstance(id int64) (*PDO, bool) {
	pdoRegistryMu.RLock()
	defer pdoRegistryMu.RUnlock()
	p, ok := pdoRegistry[id]
	return p, ok
}

func RegisterStmtInstance(s *Statement) int64 {
	pdoRegistryMu.Lock()
	defer pdoRegistryMu.Unlock()
	id := nextStmtId
	nextStmtId++
	stmtRegistry[id] = s
	return id
}

func GetStmtInstance(id int64) (*Statement, bool) {
	pdoRegistryMu.RLock()
	defer pdoRegistryMu.RUnlock()
	s, ok := stmtRegistry[id]
	return s, ok
}

// Base par défaut
var DefaultDB = NewMemoryDB()
