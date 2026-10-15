package mysqli

/*
#cgo LDFLAGS: -lsqlite3
#include <sqlite3.h>
#include <stdint.h>
#include <stdlib.h>

extern void mysqliGoSQLFunc(sqlite3_context *ctx, int argc, sqlite3_value **argv);
extern int mysqliGoTableLock(uintptr_t key, char *table, int kind);

static int mysqli_sqlite_bind_text(sqlite3_stmt *stmt, int idx, const char *val, int n) {
    return sqlite3_bind_text(stmt, idx, val, n, SQLITE_TRANSIENT);
}

static int mysqli_sqlite_bind_blob(sqlite3_stmt *stmt, int idx, const void *val, int n) {
    return sqlite3_bind_blob(stmt, idx, val, n, SQLITE_TRANSIENT);
}

static void mysqli_sqlite_result_text(sqlite3_context *ctx, const char *val, int n) {
    sqlite3_result_text(ctx, val, n, SQLITE_TRANSIENT);
}

static void mysqli_sqlite_result_error(sqlite3_context *ctx, const char *msg, int n) {
    sqlite3_result_error(ctx, msg, n);
}

static int mysqli_sqlite_create_function(sqlite3 *db, const char *name, int nargs, int deterministic, uintptr_t key) {
    int flags = SQLITE_UTF8;
    if (deterministic) {
        flags |= SQLITE_DETERMINISTIC;
    }
    return sqlite3_create_function_v2(db, name, nargs, flags, (void *)key, mysqliGoSQLFunc, NULL, NULL, NULL);
}

static uintptr_t mysqli_sqlite_user_key(sqlite3_context *ctx) {
    return (uintptr_t)sqlite3_user_data(ctx);
}

static sqlite3_value *mysqli_sqlite_arg(sqlite3_value **argv, int i) {
    return argv[i];
}

// p127_fetch_row lit toutes les colonnes de la ligne courante, après sqlite3_step == SQLITE_ROW, en un
// seul franchissement cgo : type, longueur, entier, flottant et adresse du texte ou du blob par colonne.
// Les adresses restent valides jusqu'au prochain sqlite3_step, sqlite3_reset ou sqlite3_finalize.
static void p127_fetch_row(sqlite3_stmt *stmt, int ncols, int *types, int *lens, sqlite3_int64 *ints,
                           double *dbls, const char **ptrs) {
    int i;
    for (i = 0; i < ncols; i++) {
        int t = sqlite3_column_type(stmt, i);
        types[i] = t;
        lens[i] = 0;
        ints[i] = 0;
        dbls[i] = 0;
        ptrs[i] = NULL;
        switch (t) {
        case SQLITE_INTEGER:
            ints[i] = sqlite3_column_int64(stmt, i);
            break;
        case SQLITE_FLOAT:
            dbls[i] = sqlite3_column_double(stmt, i);
            break;
        case SQLITE_TEXT:
            ptrs[i] = (const char *)sqlite3_column_text(stmt, i);
            lens[i] = sqlite3_column_bytes(stmt, i);
            break;
        case SQLITE_BLOB:
            ptrs[i] = (const char *)sqlite3_column_blob(stmt, i);
            lens[i] = sqlite3_column_bytes(stmt, i);
            break;
        }
    }
}

// mysqli_sqlite_db_config_int enveloppe sqlite3_db_config, variadique en C, pour une option entière ;
// value vaut 1 ou 0 pour fixer l'option, -1 pour la lire, et *out reçoit l'état qui en résulte.
static int mysqli_sqlite_db_config_int(sqlite3 *db, int op, int value, int *out) {
    return sqlite3_db_config(db, op, value, out);
}

// mysqli_sqlite_authorizer refuse ATTACH, DETACH et tout PRAGMA hors de l'introspection en lecture seule
// lue par backend_show.go : une requête mysqli_query n'obtient ainsi ni fichier attaché ni réglage du moteur.
static int mysqli_sqlite_authorizer(void *user, int action, const char *arg1, const char *arg2,
                                    const char *dbname, const char *trigger) {
    static const char *const allowed[] = {
        "table_info", "table_xinfo", "index_list", "index_info", "foreign_key_list", "database_list",
    };
    size_t i;
    (void)user;
    (void)arg2;
    (void)dbname;
    (void)trigger;
    switch (action) {
    case SQLITE_ATTACH:
    case SQLITE_DETACH:
        return SQLITE_DENY;
    case SQLITE_PRAGMA:
        if (arg1 == NULL) {
            return SQLITE_DENY;
        }
        for (i = 0; i < sizeof allowed / sizeof allowed[0]; i++) {
            if (sqlite3_stricmp(arg1, allowed[i]) == 0) {
                return SQLITE_OK;
            }
        }
        return SQLITE_DENY;
    }
    return SQLITE_OK;
}

// mysqli_sqlite_authorizer_locked ajoute, tant que LOCK TABLES ou FLUSH TABLES WITH READ LOCK tient un
// verrou, le contrôle des accès : chaque lecture (0), écriture (1) d'une table nommée par l'instruction
// elle-même, ou création et suppression d'un objet de schéma (2), hors déclencheur, hors vue et hors tables
// internes sqlite_*, est soumise à mysqliGoTableLock, qui consigne le refus MySQL et rend SQLITE_DENY.
static int mysqli_sqlite_authorizer_locked(void *user, int action, const char *arg1, const char *arg2,
                                           const char *dbname, const char *trigger) {
    int rc = mysqli_sqlite_authorizer(user, action, arg1, arg2, dbname, trigger);
    const char *table = NULL;
    int kind = 0;
    if (rc != SQLITE_OK || trigger != NULL) {
        return rc;
    }
    switch (action) {
    case SQLITE_READ:
        table = arg1;
        break;
    case SQLITE_INSERT:
    case SQLITE_UPDATE:
    case SQLITE_DELETE:
    case SQLITE_DROP_TABLE:
        table = arg1;
        kind = 1;
        break;
    case SQLITE_ALTER_TABLE:
    case SQLITE_CREATE_INDEX:
    case SQLITE_DROP_INDEX:
        table = arg2;
        kind = 1;
        break;
    case SQLITE_CREATE_TABLE:
    case SQLITE_CREATE_TEMP_TABLE:
    case SQLITE_DROP_TEMP_TABLE:
    case SQLITE_CREATE_VIEW:
    case SQLITE_CREATE_TEMP_VIEW:
    case SQLITE_DROP_VIEW:
    case SQLITE_DROP_TEMP_VIEW:
    case SQLITE_CREATE_VTABLE:
    case SQLITE_DROP_VTABLE:
        table = arg1;
        kind = 2;
        break;
    case SQLITE_CREATE_TRIGGER:
    case SQLITE_CREATE_TEMP_TRIGGER:
    case SQLITE_DROP_TRIGGER:
    case SQLITE_DROP_TEMP_TRIGGER:
    case SQLITE_CREATE_TEMP_INDEX:
    case SQLITE_DROP_TEMP_INDEX:
        table = arg2;
        kind = 2;
        break;
    }
    if (table == NULL || sqlite3_strnicmp(table, "sqlite_", 7) == 0) {
        return SQLITE_OK;
    }
    return mysqliGoTableLock((uintptr_t)user, (char *)table, kind);
}

// mysqli_sqlite_set_lock_authorizer installe l'autorisateur de LOCK TABLES pour la connexion key, ou
// rétablit l'autorisateur de base si key vaut zéro.
static int mysqli_sqlite_set_lock_authorizer(sqlite3 *db, uintptr_t key) {
    if (key == 0) {
        return sqlite3_set_authorizer(db, mysqli_sqlite_authorizer, NULL);
    }
    return sqlite3_set_authorizer(db, mysqli_sqlite_authorizer_locked, (void *)key);
}

static int mysqli_sqlite_harden(sqlite3 *db) {
    int out = 0;
    int rc = mysqli_sqlite_db_config_int(db, SQLITE_DBCONFIG_DEFENSIVE, 1, &out);
    if (rc != SQLITE_OK) {
        return rc;
    }
    return sqlite3_set_authorizer(db, mysqli_sqlite_authorizer, NULL);
}

static int mysqli_sqlite_column_meta(sqlite3 *db, const char *table, const char *column,
                                     const char **decl, int *notnull, int *pk, int *autoinc) {
    const char *coll = NULL;
    return sqlite3_table_column_metadata(db, "main", table, column, decl, &coll, notnull, pk, autoinc);
}
*/
import "C"

import (
	"strings"
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

	sqliteConfigDefensive  = int(C.SQLITE_DBCONFIG_DEFENSIVE)
	sqliteConfigEnableFKey = int(C.SQLITE_DBCONFIG_ENABLE_FKEY)
)

type sqliteDB struct {
	ptr *C.sqlite3
}

type sqliteStmt struct {
	ptr *C.sqlite3_stmt
}

// sqliteColumn décrit une colonne d'une requête SQLite préparée.
type sqliteColumn struct {
	name     string
	decltype string
	table    string
	origin   string
}

func sqliteOpen(path string) (*sqliteDB, int, string) {
	// Une libsqlite3 compilée avec SQLITE_USE_URI (Ubuntu 24.04) lit un nom « file:... » comme une URI même
	// sans SQLITE_OPEN_URI ; le préfixe « ./ » désigne le même fichier relatif et n'est jamais une URI.
	if len(path) >= 5 && strings.EqualFold(path[:5], "file:") {
		path = "./" + path
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	var db *C.sqlite3
	flags := C.int(C.SQLITE_OPEN_READWRITE | C.SQLITE_OPEN_CREATE | C.SQLITE_OPEN_NOMUTEX)
	rc := int(C.sqlite3_open_v2(cpath, &db, flags, nil))
	if rc != sqliteOK {
		msg := "unable to open database"
		if db != nil {
			msg = C.GoString(C.sqlite3_errmsg(db))
			C.sqlite3_close_v2(db)
		}
		return nil, rc, msg
	}
	if rc := int(C.mysqli_sqlite_harden(db)); rc != sqliteOK {
		msg := C.GoString(C.sqlite3_errmsg(db))
		C.sqlite3_close_v2(db)
		return nil, rc, msg
	}
	C.sqlite3_extended_result_codes(db, 1)
	C.sqlite3_busy_timeout(db, 5000)
	return &sqliteDB{ptr: db}, sqliteOK, ""
}

func sqliteLibVersion() string {
	return C.GoString(C.sqlite3_libversion())
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
	return int(C.sqlite3_extended_errcode(db.ptr)), C.GoString(C.sqlite3_errmsg(db.ptr))
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

// releaseMemory libère la mémoire de cache de la connexion, comme PRAGMA shrink_memory que l'autorisateur
// ferme au chemin SQL.
func (db *sqliteDB) releaseMemory() int {
	if db == nil || db.ptr == nil {
		return 1
	}
	return int(C.sqlite3_db_release_memory(db.ptr))
}

// configFlag fixe (value 1 ou 0) ou lit (value -1) une option booléenne SQLITE_DBCONFIG_* et rend son état.
func (db *sqliteDB) configFlag(op, value int) (on bool, rc int) {
	if db == nil || db.ptr == nil {
		return false, 1
	}
	var out C.int
	rc = int(C.mysqli_sqlite_db_config_int(db.ptr, C.int(op), C.int(value), &out))
	return out != 0, rc
}

// setForeignKeys applique l'équivalent de PRAGMA foreign_keys par SQLITE_DBCONFIG_ENABLE_FKEY, hors du
// chemin SQL que l'autorisateur ferme ; contrairement au pragma, l'option prend effet dans une transaction
// ouverte (mesuré par TestAlterTableRebuildKeepsForeignKeys).
func (db *sqliteDB) setForeignKeys(on bool) int {
	v := 0
	if on {
		v = 1
	}
	_, rc := db.configFlag(sqliteConfigEnableFKey, v)
	return rc
}

// setLockAuthorizer installe l'autorisateur de LOCK TABLES de la connexion enregistrée sous id, ou rétablit
// l'autorisateur de base si id vaut zéro.
func (db *sqliteDB) setLockAuthorizer(id uint32) int {
	if db == nil || db.ptr == nil {
		return 1
	}
	return int(C.mysqli_sqlite_set_lock_authorizer(db.ptr, C.uintptr_t(id)))
}

// createFunction enregistre une fonction SQL scalaire servie par mysqliGoSQLFunc ;
// key encode l'identifiant de fonction et la connexion, jamais un pointeur Go.
func (db *sqliteDB) createFunction(name string, nargs int, deterministic bool, key uintptr) int {
	if db == nil || db.ptr == nil {
		return 1
	}
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	det := C.int(0)
	if deterministic {
		det = 1
	}
	return int(C.mysqli_sqlite_create_function(db.ptr, cname, C.int(nargs), det, C.uintptr_t(key)))
}

// columnFlags lit le type déclaré et les contraintes d'une colonne de table (NOT NULL, clé primaire,
// AUTOINCREMENT).
func (db *sqliteDB) columnFlags(table, column string) (decltype string, notnull, pk, autoinc bool, ok bool) {
	if db == nil || db.ptr == nil || table == "" || column == "" {
		return "", false, false, false, false
	}
	ct := C.CString(table)
	defer C.free(unsafe.Pointer(ct))
	cc := C.CString(column)
	defer C.free(unsafe.Pointer(cc))
	var decl *C.char
	var nn, p, ai C.int
	if int(C.mysqli_sqlite_column_meta(db.ptr, ct, cc, &decl, &nn, &p, &ai)) != sqliteOK {
		return "", false, false, false, false
	}
	if decl != nil {
		decltype = C.GoString(decl)
	}
	return decltype, nn != 0, p != 0, ai != 0, true
}

// prepare compile une instruction ; tail reçoit le texte non consommé.
func (db *sqliteDB) prepare(sql string) (st *sqliteStmt, tail string, rc int, msg string) {
	if db == nil || db.ptr == nil {
		return nil, "", 1, "database is closed"
	}
	csql := C.CString(sql)
	defer C.free(unsafe.Pointer(csql))
	var stmt *C.sqlite3_stmt
	var ctail *C.char
	rc = int(C.sqlite3_prepare_v2(db.ptr, csql, C.int(len(sql)), &stmt, &ctail))
	if rc != sqliteOK {
		code, m := db.errInfo()
		if code == sqliteOK {
			code = rc
		}
		return nil, "", code, m
	}
	if ctail != nil {
		consumed := int(uintptr(unsafe.Pointer(ctail)) - uintptr(unsafe.Pointer(csql)))
		if consumed >= 0 && consumed <= len(sql) {
			tail = sql[consumed:]
		}
	}
	return &sqliteStmt{ptr: stmt}, tail, sqliteOK, ""
}

func (st *sqliteStmt) empty() bool { return st == nil || st.ptr == nil }

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
	C.sqlite3_clear_bindings(st.ptr)
}

func (st *sqliteStmt) paramCount() int {
	if st == nil || st.ptr == nil {
		return 0
	}
	return int(C.sqlite3_bind_parameter_count(st.ptr))
}

func (st *sqliteStmt) readonly() bool {
	if st == nil || st.ptr == nil {
		return true
	}
	return C.sqlite3_stmt_readonly(st.ptr) != 0
}

func (st *sqliteStmt) bind(idx int, c cell) int {
	if st == nil || st.ptr == nil {
		return sqliteOK
	}
	switch c.kind {
	case cellNull:
		return int(C.sqlite3_bind_null(st.ptr, C.int(idx)))
	case cellInt:
		return int(C.sqlite3_bind_int64(st.ptr, C.int(idx), C.sqlite3_int64(c.i)))
	case cellFloat:
		return int(C.sqlite3_bind_double(st.ptr, C.int(idx), C.double(c.f)))
	case cellBlob:
		if len(c.s) == 0 {
			var dummy byte
			return int(C.mysqli_sqlite_bind_blob(st.ptr, C.int(idx), unsafe.Pointer(&dummy), 0))
		}
		cs := C.CString(c.s)
		defer C.free(unsafe.Pointer(cs))
		return int(C.mysqli_sqlite_bind_blob(st.ptr, C.int(idx), unsafe.Pointer(cs), C.int(len(c.s))))
	default:
		cs := C.CString(c.s)
		defer C.free(unsafe.Pointer(cs))
		return int(C.mysqli_sqlite_bind_text(st.ptr, C.int(idx), cs, C.int(len(c.s))))
	}
}

func (st *sqliteStmt) columns() []sqliteColumn {
	if st == nil || st.ptr == nil {
		return nil
	}
	n := int(C.sqlite3_column_count(st.ptr))
	if n <= 0 {
		return nil
	}
	cols := make([]sqliteColumn, n)
	for i := range n {
		ci := C.int(i)
		if p := C.sqlite3_column_name(st.ptr, ci); p != nil {
			cols[i].name = C.GoString(p)
		}
		if p := C.sqlite3_column_decltype(st.ptr, ci); p != nil {
			cols[i].decltype = C.GoString(p)
		}
		if p := C.sqlite3_column_table_name(st.ptr, ci); p != nil {
			cols[i].table = C.GoString(p)
		}
		if p := C.sqlite3_column_origin_name(st.ptr, ci); p != nil {
			cols[i].origin = C.GoString(p)
		}
	}
	return cols
}

// rowBuffers reçoit d'un appel p127_fetch_row les valeurs de toutes les colonnes d'une ligne.
type rowBuffers struct {
	types []C.int
	lens  []C.int
	ints  []C.sqlite3_int64
	dbls  []C.double
	ptrs  []*C.char
}

func newRowBuffers(n int) *rowBuffers {
	return &rowBuffers{
		types: make([]C.int, n),
		lens:  make([]C.int, n),
		ints:  make([]C.sqlite3_int64, n),
		dbls:  make([]C.double, n),
		ptrs:  make([]*C.char, n),
	}
}

// step exécute l'instruction jusqu'à SQLITE_DONE et collecte toutes les lignes ; chaque ligne coûte deux
// franchissements cgo, sqlite3_step et p127_fetch_row, quel que soit le nombre de colonnes.
func (st *sqliteStmt) step() (rows [][]cell, rc int) {
	if st == nil || st.ptr == nil {
		return nil, sqliteDone
	}
	var buf *rowBuffers
	for {
		stepRC := int(C.sqlite3_step(st.ptr))
		if stepRC != sqliteRow {
			return rows, stepRC
		}
		if buf == nil {
			// Le nombre de colonnes est lu après la première ligne : une recompilation automatique
			// (SQLITE_SCHEMA) pendant ce premier pas peut l'avoir changé.
			buf = newRowBuffers(int(C.sqlite3_column_count(st.ptr)))
		}
		n := len(buf.types)
		row := make([]cell, n)
		if n > 0 {
			C.p127_fetch_row(st.ptr, C.int(n), &buf.types[0], &buf.lens[0], &buf.ints[0], &buf.dbls[0], &buf.ptrs[0])
		}
		for i := range n {
			switch int(buf.types[i]) {
			case sqliteInteger:
				row[i] = cell{kind: cellInt, i: int64(buf.ints[i])}
			case sqliteFloat:
				row[i] = cell{kind: cellFloat, f: float64(buf.dbls[i])}
			case sqliteText, sqliteBlob:
				var s string
				if p, nb := buf.ptrs[i], buf.lens[i]; p != nil && nb > 0 {
					s = C.GoStringN(p, nb)
				}
				kind := cellText
				if int(buf.types[i]) == sqliteBlob {
					kind = cellBlob
				}
				row[i] = cell{kind: kind, s: s}
			default:
				row[i] = cell{kind: cellNull}
			}
		}
		rows = append(rows, row)
	}
}

// Accès aux arguments et résultats des fonctions SQL servies par Go.

func sqlFuncKey(ctx *C.sqlite3_context) uintptr {
	return uintptr(C.mysqli_sqlite_user_key(ctx))
}

func sqlFuncArg(argv **C.sqlite3_value, i int) cell {
	v := C.mysqli_sqlite_arg(argv, C.int(i))
	switch int(C.sqlite3_value_type(v)) {
	case sqliteInteger:
		return cell{kind: cellInt, i: int64(C.sqlite3_value_int64(v))}
	case sqliteFloat:
		return cell{kind: cellFloat, f: float64(C.sqlite3_value_double(v))}
	case sqliteText, sqliteBlob:
		nb := C.sqlite3_value_bytes(v)
		p := C.sqlite3_value_text(v)
		var s string
		if p != nil && nb > 0 {
			s = C.GoStringN((*C.char)(unsafe.Pointer(p)), nb)
		}
		return cell{kind: cellText, s: s}
	default:
		return cell{kind: cellNull}
	}
}

func sqlFuncResult(ctx *C.sqlite3_context, c cell) {
	switch c.kind {
	case cellNull:
		C.sqlite3_result_null(ctx)
	case cellInt:
		C.sqlite3_result_int64(ctx, C.sqlite3_int64(c.i))
	case cellFloat:
		C.sqlite3_result_double(ctx, C.double(c.f))
	default:
		cs := C.CString(c.s)
		defer C.free(unsafe.Pointer(cs))
		C.mysqli_sqlite_result_text(ctx, cs, C.int(len(c.s)))
	}
}

func sqlFuncError(ctx *C.sqlite3_context, msg string) {
	cs := C.CString(msg)
	defer C.free(unsafe.Pointer(cs))
	C.mysqli_sqlite_result_error(ctx, cs, C.int(len(msg)))
}
