// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/pdo: Émulation PDO et moteur de base de données in-process souverain

package pdo

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"code.hazyhaar.fr/devhoros/php127/types"
)

var (
	ErrPDOConnection = errors.New("pdo: échec de connexion à la base de données")
	ErrPDOSyntax     = errors.New("pdo: erreur de syntaxe SQL")
	ErrPDONoActiveTx = errors.New("pdo: aucune transaction active")
)

type TableRow map[string]types.Value

type Table struct {
	Name    string
	Columns []string
	Rows    []TableRow
}

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

type Driver interface {
	Exec(query string, args ...types.Value) (int64, error)
	Query(query string, args ...types.Value) ([]TableRow, error)
	Begin() error
	Commit() error
	Rollback() error
	LastInsertId() int64
}

type Statement struct {
	query    string
	db       *MemoryDB
	bound    map[string]types.Value
	boundPos []types.Value
	rows     []TableRow
	cursor   int
}

func (s *Statement) BindValue(key string, val types.Value) {
	if s.bound == nil {
		s.bound = make(map[string]types.Value)
	}
	s.bound[key] = val
}

func (s *Statement) Execute(args []types.Value) (bool, error) {
	q := s.query
	upper := strings.ToUpper(strings.TrimSpace(q))

	// Combine positional and bound args
	var finalArgs []types.Value
	if len(args) > 0 {
		finalArgs = args
	} else if len(s.boundPos) > 0 {
		finalArgs = s.boundPos
	}

	if strings.HasPrefix(upper, "SELECT") {
		rows, err := s.db.Query(q, finalArgs...)
		if err != nil {
			return false, err
		}
		s.rows = rows
		s.cursor = 0
		return true, nil
	}

	_, err := s.db.Exec(q, finalArgs...)
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

func (s *Statement) RowCount() int {
	return len(s.rows)
}

func parseWhereColumn(parts []string) string {
	for i, p := range parts {
		if strings.ToUpper(p) == "WHERE" && i+1 < len(parts) {
			return strings.ToLower(strings.Trim(parts[i+1], " `\""))
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

func (db *MemoryDB) Exec(query string, args ...types.Value) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	trimmed := strings.TrimSpace(query)
	upper := strings.ToUpper(trimmed)

	// 1. CREATE TABLE table (col1, col2, ...)
	if strings.HasPrefix(upper, "CREATE TABLE") {
		parts := strings.Fields(trimmed)
		if len(parts) >= 3 {
			tblName := strings.ToLower(strings.Trim(parts[2], "(`\""))
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
			db.tables[tblName] = &Table{
				Name:    tblName,
				Columns: cols,
				Rows:    make([]TableRow, 0),
			}
			return 0, nil
		}
	}

	// 2. INSERT INTO table (col1, col2) VALUES (?, ?)
	if strings.HasPrefix(upper, "INSERT INTO") {
		parts := strings.Fields(trimmed)
		if len(parts) >= 3 {
			tblName := strings.ToLower(strings.Trim(parts[2], "(`\""))
			tbl, ok := db.tables[tblName]
			if !ok {
				tbl = &Table{Name: tblName, Rows: make([]TableRow, 0)}
				db.tables[tblName] = tbl
			}
			db.lastInsertId++
			row := make(TableRow)
			row["id"] = types.NewLong(db.lastInsertId)

			// Extract explicit columns if specified
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
			tblName := strings.ToLower(strings.Trim(parts[1], "(`\""))
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
						row[parts[3]] = args[0]
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

				// Project columns
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
	// Restore snapshots
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

// Global default DB connection
var DefaultDB = NewMemoryDB()
