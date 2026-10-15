package pdo

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdlib.h>

static int pdo_sqlite_bind_text(sqlite3_stmt *stmt, int idx, const char *val, int n) {
    return sqlite3_bind_text(stmt, idx, val, n, SQLITE_TRANSIENT);
}

static int pdo_sqlite_bind_blob(sqlite3_stmt *stmt, int idx, const void *val, int n) {
    return sqlite3_bind_blob(stmt, idx, val, n, SQLITE_TRANSIENT);
}
*/
import "C"

import (
	"unsafe"
)

const (
	sqliteOK         = int(C.SQLITE_OK)
	sqliteRow        = int(C.SQLITE_ROW)
	sqliteDone       = int(C.SQLITE_DONE)
	sqliteConstraint = int(C.SQLITE_CONSTRAINT)

	sqliteInteger = int(C.SQLITE_INTEGER)
	sqliteFloat   = int(C.SQLITE_FLOAT)
	sqliteText    = int(C.SQLITE_TEXT)
	sqliteBlob    = int(C.SQLITE_BLOB)
	sqliteNull    = int(C.SQLITE_NULL)
)

type sqliteDB struct {
	ptr *C.sqlite3
}

type sqliteStmt struct {
	ptr *C.sqlite3_stmt
}

type rawCell struct {
	kind  int
	ival  int64
	fval  float64
	bytes []byte
}

func sqliteOpen(path string) (*sqliteDB, int, string) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var db *C.sqlite3
	flags := C.int(C.SQLITE_OPEN_READWRITE | C.SQLITE_OPEN_CREATE | C.SQLITE_OPEN_URI | C.SQLITE_OPEN_FULLMUTEX)
	rc := int(C.sqlite3_open_v2(cpath, &db, flags, nil))
	if rc != sqliteOK {
		msg := "unable to open database"
		if db != nil {
			msg = C.GoString(C.sqlite3_errmsg(db))
			C.sqlite3_close_v2(db)
		}
		return nil, rc, msg
	}
	return &sqliteDB{ptr: db}, sqliteOK, ""
}

func (db *sqliteDB) close() {
	if db == nil || db.ptr == nil {
		return
	}
	C.sqlite3_close_v2(db.ptr)
	db.ptr = nil
}

func (db *sqliteDB) errInfo() (int, string) {
	if db == nil || db.ptr == nil {
		return 1, "database is closed"
	}
	return int(C.sqlite3_errcode(db.ptr)), C.GoString(C.sqlite3_errmsg(db.ptr))
}

func (db *sqliteDB) inTransaction() bool {
	if db == nil || db.ptr == nil {
		return false
	}
	return C.sqlite3_get_autocommit(db.ptr) == 0
}

func (db *sqliteDB) changes() int64 {
	if db == nil || db.ptr == nil {
		return 0
	}
	return int64(C.sqlite3_changes(db.ptr))
}

func (db *sqliteDB) lastInsertRowID() int64 {
	if db == nil || db.ptr == nil {
		return 0
	}
	return int64(C.sqlite3_last_insert_rowid(db.ptr))
}

func (db *sqliteDB) execRaw(sql string) (int64, int, string) {
	if db == nil || db.ptr == nil {
		return 0, 1, "database is closed"
	}
	csql := C.CString(sql)
	defer C.free(unsafe.Pointer(csql))
	var errmsg *C.char
	rc := int(C.sqlite3_exec(db.ptr, csql, nil, nil, &errmsg))
	if rc != sqliteOK {
		var msg string
		if errmsg != nil {
			msg = C.GoString(errmsg)
			C.sqlite3_free(unsafe.Pointer(errmsg))
		} else {
			_, msg = db.errInfo()
		}
		return 0, rc, msg
	}
	return db.changes(), sqliteOK, ""
}

func (db *sqliteDB) prepare(sql string) (*sqliteStmt, int, string) {
	if db == nil || db.ptr == nil {
		return nil, 1, "database is closed"
	}
	csql := C.CString(sql)
	defer C.free(unsafe.Pointer(csql))
	var stmt *C.sqlite3_stmt
	rc := int(C.sqlite3_prepare_v2(db.ptr, csql, C.int(len(sql)), &stmt, nil))
	if rc != sqliteOK {
		code, msg := db.errInfo()
		if code == sqliteOK {
			code = rc
		}
		return nil, code, msg
	}
	if stmt == nil {
		return &sqliteStmt{ptr: nil}, sqliteOK, ""
	}
	return &sqliteStmt{ptr: stmt}, sqliteOK, ""
}

func (st *sqliteStmt) finalize() {
	if st == nil || st.ptr == nil {
		return
	}
	C.sqlite3_finalize(st.ptr)
	st.ptr = nil
}

func (st *sqliteStmt) reset() {
	if st == nil || st.ptr == nil {
		return
	}
	C.sqlite3_reset(st.ptr)
}

func (st *sqliteStmt) clearBindings() {
	if st == nil || st.ptr == nil {
		return
	}
	C.sqlite3_clear_bindings(st.ptr)
}

func (st *sqliteStmt) paramCount() int {
	if st == nil || st.ptr == nil {
		return 0
	}
	return int(C.sqlite3_bind_parameter_count(st.ptr))
}

func (st *sqliteStmt) paramIndex(name string) int {
	if st == nil || st.ptr == nil {
		return 0
	}
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	return int(C.sqlite3_bind_parameter_index(st.ptr, cname))
}

func (st *sqliteStmt) bindNull(idx int) int {
	if st == nil || st.ptr == nil {
		return sqliteOK
	}
	return int(C.sqlite3_bind_null(st.ptr, C.int(idx)))
}

func (st *sqliteStmt) bindInt64(idx int, v int64) int {
	if st == nil || st.ptr == nil {
		return sqliteOK
	}
	return int(C.sqlite3_bind_int64(st.ptr, C.int(idx), C.sqlite3_int64(v)))
}

func (st *sqliteStmt) bindDouble(idx int, v float64) int {
	if st == nil || st.ptr == nil {
		return sqliteOK
	}
	return int(C.sqlite3_bind_double(st.ptr, C.int(idx), C.double(v)))
}

func (st *sqliteStmt) bindText(idx int, s string) int {
	if st == nil || st.ptr == nil {
		return sqliteOK
	}
	if len(s) == 0 {
		empty := C.CString("")
		defer C.free(unsafe.Pointer(empty))
		return int(C.pdo_sqlite_bind_text(st.ptr, C.int(idx), empty, 0))
	}
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	return int(C.pdo_sqlite_bind_text(st.ptr, C.int(idx), cs, C.int(len(s))))
}

func (st *sqliteStmt) bindBlob(idx int, b []byte) int {
	if st == nil || st.ptr == nil {
		return sqliteOK
	}
	if len(b) == 0 {
		var dummy byte
		return int(C.pdo_sqlite_bind_blob(st.ptr, C.int(idx), unsafe.Pointer(&dummy), 0))
	}
	return int(C.pdo_sqlite_bind_blob(st.ptr, C.int(idx), unsafe.Pointer(&b[0]), C.int(len(b))))
}

func (st *sqliteStmt) columnMeta() (names []string, tables []string) {
	if st == nil || st.ptr == nil {
		return nil, nil
	}
	n := int(C.sqlite3_column_count(st.ptr))
	if n <= 0 {
		return nil, nil
	}
	names = make([]string, n)
	tables = make([]string, n)
	for i := range n {
		if cn := C.sqlite3_column_name(st.ptr, C.int(i)); cn != nil {
			names[i] = C.GoString(cn)
		}
		if tn := C.sqlite3_column_table_name(st.ptr, C.int(i)); tn != nil {
			tables[i] = C.GoString(tn)
		}
	}
	return names, tables
}

func (st *sqliteStmt) stepAndCollect() (cols []string, tables []string, rows [][]rawCell, rc int) {
	if st == nil || st.ptr == nil {
		return nil, nil, nil, sqliteDone
	}
	cols, tables = st.columnMeta()
	ncols := len(cols)
	for {
		stepRC := int(C.sqlite3_step(st.ptr))
		if stepRC == sqliteRow {
			if ncols == 0 {
				cols, tables = st.columnMeta()
				ncols = len(cols)
			}
			row := make([]rawCell, ncols)
			for i := range ncols {
				ct := int(C.sqlite3_column_type(st.ptr, C.int(i)))
				switch ct {
				case sqliteInteger:
					row[i] = rawCell{
						kind: sqliteInteger,
						ival: int64(C.sqlite3_column_int64(st.ptr, C.int(i))),
					}
				case sqliteFloat:
					row[i] = rawCell{
						kind: sqliteFloat,
						fval: float64(C.sqlite3_column_double(st.ptr, C.int(i))),
					}
				case sqliteText:
					nbytes := int(C.sqlite3_column_bytes(st.ptr, C.int(i)))
					ptr := C.sqlite3_column_text(st.ptr, C.int(i))
					var data []byte
					if ptr != nil && nbytes > 0 {
						data = C.GoBytes(unsafe.Pointer(ptr), C.int(nbytes))
					} else {
						data = []byte{}
					}
					row[i] = rawCell{kind: sqliteText, bytes: data}
				case sqliteBlob:
					nbytes := int(C.sqlite3_column_bytes(st.ptr, C.int(i)))
					ptr := C.sqlite3_column_blob(st.ptr, C.int(i))
					var data []byte
					if ptr != nil && nbytes > 0 {
						data = C.GoBytes(ptr, C.int(nbytes))
					} else {
						data = []byte{}
					}
					row[i] = rawCell{kind: sqliteBlob, bytes: data}
				default:
					row[i] = rawCell{kind: sqliteNull}
				}
			}
			rows = append(rows, row)
			continue
		}
		return cols, tables, rows, stepRC
	}
}
