package pdo

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func mustStr(t *testing.T, z *zone.Zone, s string) types.Value {
	t.Helper()
	v, err := newStr(z, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func mustCallPDO(t *testing.T, z *zone.Zone, db types.Value, method string, args ...types.Value) types.Value {
	t.Helper()
	v, err := CallPDOMethod(z, db, method, args...)
	if err != nil {
		t.Fatalf("PDO::%s failed: %v", method, err)
	}
	return v
}

func mustCallStmt(t *testing.T, z *zone.Zone, st types.Value, method string, args ...types.Value) types.Value {
	t.Helper()
	v, err := CallStmtMethod(z, st, method, args...)
	if err != nil {
		t.Fatalf("PDOStatement::%s failed: %v", method, err)
	}
	return v
}

func TestZoneResetClosesConnectionsAndRollsBack(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "reset_rollback.db")

	z := zone.New(131072)
	dsn := mustStr(t, z, "sqlite:"+dbFile)
	db, err := NewPDO(z, dsn)
	if err != nil {
		t.Fatal(err)
	}
	mustCallPDO(t, z, db, "exec", mustStr(t, z, "CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)"))
	mustCallPDO(t, z, db, "exec", mustStr(t, z, "INSERT INTO items (name) VALUES ('committed')"))

	// Open a transaction and a prepared statement without closing or committing
	mustCallPDO(t, z, db, "beginTransaction")
	stmt := mustCallPDO(t, z, db, "prepare", mustStr(t, z, "INSERT INTO items (name) VALUES (:name)"))
	ref, err := types.NewReference(z, mustStr(t, z, "uncommitted"))
	if err != nil {
		t.Fatal(err)
	}
	mustCallStmt(t, z, stmt, "bindParam", mustStr(t, z, ":name"), ref)
	mustCallStmt(t, z, stmt, "execute")

	if got := OpenConnCount(z); got != 1 {
		t.Fatalf("OpenConnCount before Reset = %d; want 1", got)
	}
	if got := OpenStmtCount(z); got != 1 {
		t.Fatalf("OpenStmtCount before Reset = %d; want 1", got)
	}

	z.Reset(DispatchCleanup)

	if got := OpenConnCount(z); got != 0 {
		t.Fatalf("OpenConnCount after Reset = %d; want 0", got)
	}
	if got := OpenStmtCount(z); got != 0 {
		t.Fatalf("OpenStmtCount after Reset = %d; want 0", got)
	}

	// Verify on disk that uncommitted row was rolled back
	z2 := zone.New(65536)
	defer z2.Reset(DispatchCleanup)
	db2, err := NewPDO(z2, mustStr(t, z2, "sqlite:"+dbFile))
	if err != nil {
		t.Fatal(err)
	}
	q := mustCallPDO(t, z2, db2, "query", mustStr(t, z2, "SELECT COUNT(*) FROM items"))
	cnt := mustCallStmt(t, z2, q, "fetchColumn", types.NewLong(0))
	if cnt.ToLong() != 1 {
		t.Fatalf("row count after rollback on Reset = %d; want 1", cnt.ToLong())
	}
}

func TestACIDTransactionsIncludingDDLAndDiskOracle(t *testing.T) {
	dir := t.TempDir()
	dbFile := filepath.Join(dir, "acid.db")

	z := zone.New(262144)
	defer z.Reset(DispatchCleanup)

	db, err := NewPDO(z, mustStr(t, z, "sqlite:"+dbFile))
	if err != nil {
		t.Fatal(err)
	}
	mustCallPDO(t, z, db, "exec", mustStr(t, z, "CREATE TABLE accounts (id INTEGER PRIMARY KEY, holder TEXT NOT NULL, balance INTEGER NOT NULL)"))

	// 1. Commit transaction
	if mustCallPDO(t, z, db, "inTransaction").ToBool() {
		t.Fatal("should not be inTransaction initially")
	}
	if !mustCallPDO(t, z, db, "beginTransaction").ToBool() {
		t.Fatal("beginTransaction failed")
	}
	if !mustCallPDO(t, z, db, "inTransaction").ToBool() {
		t.Fatal("should be inTransaction after beginTransaction")
	}
	// Nested beginTransaction must fail with PDOException
	if _, err := CallPDOMethod(z, db, "beginTransaction"); err == nil || ExceptionClass(err) != "PDOException" {
		t.Fatalf("nested beginTransaction error = %v; want PDOException", err)
	}
	mustCallPDO(t, z, db, "exec", mustStr(t, z, "INSERT INTO accounts (holder, balance) VALUES ('Alice', 1000), ('Bob', 500)"))
	if !mustCallPDO(t, z, db, "commit").ToBool() {
		t.Fatal("commit failed")
	}
	if mustCallPDO(t, z, db, "inTransaction").ToBool() {
		t.Fatal("should not be inTransaction after commit")
	}

	// 2. Rollback transaction (including DDL CREATE TABLE inside transaction!)
	mustCallPDO(t, z, db, "beginTransaction")
	mustCallPDO(t, z, db, "exec", mustStr(t, z, "UPDATE accounts SET balance = balance - 200 WHERE holder = 'Alice'"))
	mustCallPDO(t, z, db, "exec", mustStr(t, z, "CREATE TABLE ephemeral_audit (id INT)"))
	if !mustCallPDO(t, z, db, "rollBack").ToBool() {
		t.Fatal("rollBack failed")
	}
	// Verify ephemeral_audit does not exist and Alice still has 1000
	if _, err := CallPDOMethod(z, db, "query", mustStr(t, z, "SELECT * FROM ephemeral_audit")); err == nil {
		t.Fatal("ephemeral_audit table survived ROLLBACK")
	}
	st := mustCallPDO(t, z, db, "query", mustStr(t, z, "SELECT balance FROM accounts WHERE holder = 'Alice'"))
	bal := mustCallStmt(t, z, st, "fetchColumn")
	if bal.ToLong() != 1000 {
		t.Fatalf("Alice balance after rollback = %d; want 1000", bal.ToLong())
	}

	// 3. Verify real SQLite file with /usr/bin/sqlite3 CLI oracle
	if _, lookErr := exec.LookPath("/usr/bin/sqlite3"); lookErr == nil {
		cmd := exec.CommandContext(t.Context(), "/usr/bin/sqlite3", dbFile, "SELECT holder || ':' || balance FROM accounts ORDER BY id;")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("/usr/bin/sqlite3 failed: %v (%s)", err, out)
		}
		got := strings.TrimSpace(string(out))
		want := "Alice:1000\nBob:500"
		if got != want {
			t.Fatalf("sqlite3 CLI oracle = %q; want %q", got, want)
		}
	}
}

func TestPreparedStatementsBindingsAndFetchModes(t *testing.T) {
	z := zone.New(524288)
	defer z.Reset(DispatchCleanup)

	db, err := NewPDO(z, mustStr(t, z, "sqlite::memory:"))
	if err != nil {
		t.Fatal(err)
	}
	mustCallPDO(t, z, db, "exec", mustStr(t, z, "CREATE TABLE users (id INTEGER PRIMARY KEY, dept TEXT, name TEXT, score REAL)"))

	// 1. Positional ? parameters via execute([0 => ..., 1 => ..., 2 => ...])
	insPos := mustCallPDO(t, z, db, "prepare", mustStr(t, z, "INSERT INTO users (dept, name, score) VALUES (?, ?, ?)"))
	if got := QueryString(z, insPos); got != "INSERT INTO users (dept, name, score) VALUES (?, ?, ?)" {
		t.Fatalf("queryString = %q", got)
	}
	params1, _ := types.NewArray(z)
	_ = types.ArrayAppend(z, &params1, mustStr(t, z, "R&D"))
	_ = types.ArrayAppend(z, &params1, mustStr(t, z, "Ada"))
	_ = types.ArrayAppend(z, &params1, types.NewDouble(98.5))
	mustCallStmt(t, z, insPos, "execute", params1)
	if rc := mustCallStmt(t, z, insPos, "rowCount"); rc.ToLong() != 1 {
		t.Fatalf("rowCount = %d; want 1", rc.ToLong())
	}
	if lid := mustCallPDO(t, z, db, "lastInsertId"); types.StringData(z, lid) != "1" {
		t.Fatalf("lastInsertId = %q; want '1'", types.StringData(z, lid))
	}

	// 2. Named parameters (:dept, name without colon) via execute($params)
	insNamed := mustCallPDO(t, z, db, "prepare", mustStr(t, z, "INSERT INTO users (dept, name, score) VALUES (:dept, :name, :score)"))
	params2, _ := types.NewArray(z)
	_ = setArrayKey(z, &params2, ":dept", mustStr(t, z, "R&D"))
	_ = setArrayKey(z, &params2, "name", mustStr(t, z, "Grace"))
	_ = setArrayKey(z, &params2, ":score", types.NewDouble(99.0))
	mustCallStmt(t, z, insNamed, "execute", params2)

	// 3. bindValue and bindParam (mutating reference before execute)
	deptRef, err := types.NewReference(z, mustStr(t, z, "Initial"))
	if err != nil {
		t.Fatal(err)
	}
	mustCallStmt(t, z, insNamed, "bindParam", mustStr(t, z, ":dept"), deptRef, types.NewLong(PARAM_STR))
	mustCallStmt(t, z, insNamed, "bindValue", mustStr(t, z, "name"), mustStr(t, z, "Linus"), types.NewLong(PARAM_STR))
	mustCallStmt(t, z, insNamed, "bindValue", mustStr(t, z, ":score"), types.NewLong(95), types.NewLong(PARAM_INT))
	// Mutate $deptRef before calling execute()
	types.AssignRef(z, deptRef, mustStr(t, z, "Ops"))
	mustCallStmt(t, z, insNamed, "execute")

	// 4. Fetch modes: FETCH_ASSOC, FETCH_NUM, FETCH_BOTH, FETCH_COLUMN, FETCH_KEY_PAIR, FETCH_OBJ, FETCH_GROUP, FETCH_UNIQUE
	q := mustCallPDO(t, z, db, "query", mustStr(t, z, "SELECT id, dept, name, score FROM users ORDER BY id"))
	if cc := mustCallStmt(t, z, q, "columnCount"); cc.ToLong() != 4 {
		t.Fatalf("columnCount = %d; want 4", cc.ToLong())
	}

	row1Assoc := mustCallStmt(t, z, q, "fetch", types.NewLong(FETCH_ASSOC))
	if types.ArrayLen(z, row1Assoc) != 4 {
		t.Fatalf("FETCH_ASSOC len = %d; want 4", types.ArrayLen(z, row1Assoc))
	}
	row2Num := mustCallStmt(t, z, q, "fetch", types.NewLong(FETCH_NUM))
	if types.ArrayLen(z, row2Num) != 4 {
		t.Fatalf("FETCH_NUM len = %d; want 4", types.ArrayLen(z, row2Num))
	}
	row3Both := mustCallStmt(t, z, q, "fetch", types.NewLong(FETCH_BOTH))
	if types.ArrayLen(z, row3Both) != 8 {
		t.Fatalf("FETCH_BOTH len = %d; want 8", types.ArrayLen(z, row3Both))
	}
	rowEOF := mustCallStmt(t, z, q, "fetch")
	if rowEOF.Type != types.TypeFalse {
		t.Fatalf("fetch at EOF = %v; want false", rowEOF)
	}

	// FETCH_COLUMN via fetchAll
	qCol := mustCallPDO(t, z, db, "query", mustStr(t, z, "SELECT name FROM users ORDER BY id"))
	allNames := mustCallStmt(t, z, qCol, "fetchAll", types.NewLong(FETCH_COLUMN), types.NewLong(0))
	if types.ArrayLen(z, allNames) != 3 {
		t.Fatalf("FETCH_COLUMN len = %d; want 3", types.ArrayLen(z, allNames))
	}

	// FETCH_KEY_PAIR via fetchAll
	qKP := mustCallPDO(t, z, db, "query", mustStr(t, z, "SELECT name, dept FROM users ORDER BY id"))
	kp := mustCallStmt(t, z, qKP, "fetchAll", types.NewLong(FETCH_KEY_PAIR))
	if types.ArrayLen(z, kp) != 3 {
		t.Fatalf("FETCH_KEY_PAIR len = %d; want 3", types.ArrayLen(z, kp))
	}
	linusDept, found, _ := types.ArrayGet(z, kp, mustStr(t, z, "Linus"), warnDrop)
	if !found || types.StringData(z, linusDept) != "Ops" {
		t.Fatalf("FETCH_KEY_PAIR['Linus'] = %v (found=%v); want 'Ops'", linusDept, found)
	}

	// FETCH_GROUP via fetchAll
	qGrp := mustCallPDO(t, z, db, "query", mustStr(t, z, "SELECT dept, name FROM users ORDER BY id"))
	grp := mustCallStmt(t, z, qGrp, "fetchAll", types.NewLong(FETCH_GROUP|FETCH_COLUMN))
	rdList, found, _ := types.ArrayGet(z, grp, mustStr(t, z, "R&D"), warnDrop)
	if !found || types.ArrayLen(z, rdList) != 2 {
		t.Fatalf("FETCH_GROUP['R&D'] len = %d", types.ArrayLen(z, rdList))
	}

	// FETCH_OBJ & fetchObject
	qObj := mustCallPDO(t, z, db, "query", mustStr(t, z, "SELECT id, name FROM users ORDER BY id"))
	obj1 := mustCallStmt(t, z, qObj, "fetch", types.NewLong(FETCH_OBJ))
	if obj1.Type != types.TypeObject {
		t.Fatalf("FETCH_OBJ returned %v", obj1)
	}
	nameProp, ok := ObjectGetProperty(z, obj1, "name")
	if !ok || types.StringData(z, nameProp) != "Ada" {
		t.Fatalf("obj1->name = %v (ok=%v); want 'Ada'", nameProp, ok)
	}
	obj2 := mustCallStmt(t, z, qObj, "fetchObject")
	nameProp2, ok := ObjectGetProperty(z, obj2, "name")
	if !ok || types.StringData(z, nameProp2) != "Grace" {
		t.Fatalf("obj2->name = %v (ok=%v); want 'Grace'", nameProp2, ok)
	}
	mustCallStmt(t, z, qObj, "closeCursor")
	if afterClose := mustCallStmt(t, z, qObj, "fetch"); afterClose.Type != types.TypeFalse {
		t.Fatal("fetch after closeCursor must return false")
	}
}

func TestAttributesErrorModesAndQuote(t *testing.T) {
	z := zone.New(262144)
	defer z.Reset(DispatchCleanup)

	drivers, err := GetAvailableDrivers(z, nil, nil)
	if err != nil || types.ArrayLen(z, drivers) != 1 {
		t.Fatalf("getAvailableDrivers failed: %v", err)
	}

	db, err := NewPDO(z, mustStr(t, z, "sqlite::memory:"))
	if err != nil {
		t.Fatal(err)
	}
	drv := mustCallPDO(t, z, db, "getAttribute", types.NewLong(ATTR_DRIVER_NAME))
	if types.StringData(z, drv) != "sqlite" {
		t.Fatalf("ATTR_DRIVER_NAME = %q; want 'sqlite'", types.StringData(z, drv))
	}

	quoted := mustCallPDO(t, z, db, "quote", mustStr(t, z, "O'Reilly"))
	if types.StringData(z, quoted) != "'O''Reilly'" {
		t.Fatalf("quote = %q; want \"'O''Reilly'\"", types.StringData(z, quoted))
	}

	// Default ERRMODE_EXCEPTION raises PDOException on invalid SQL
	_, err = CallPDOMethod(z, db, "exec", mustStr(t, z, "SELECT * FROM nonexistent_table"))
	if err == nil || ExceptionClass(err) != "PDOException" {
		t.Fatalf("expected PDOException on invalid SQL, got %v", err)
	}
	errCode := mustCallPDO(t, z, db, "errorCode")
	if types.StringData(z, errCode) != "HY000" {
		t.Fatalf("errorCode = %q; want 'HY000'", types.StringData(z, errCode))
	}
	errInfo := mustCallPDO(t, z, db, "errorInfo")
	if types.ArrayLen(z, errInfo) != 3 {
		t.Fatalf("errorInfo len = %d; want 3", types.ArrayLen(z, errInfo))
	}

	// ERRMODE_SILENT returns false without returning an error
	mustCallPDO(t, z, db, "setAttribute", types.NewLong(ATTR_ERRMODE), types.NewLong(ERRMODE_SILENT))
	res, err := CallPDOMethod(z, db, "exec", mustStr(t, z, "INVALID SQL SYNTAX"))
	if err != nil || res.Type != types.TypeFalse {
		t.Fatalf("ERRMODE_SILENT: res=%v err=%v; want false, nil", res, err)
	}

	// ATTR_CASE (CASE_UPPER) and ATTR_STRINGIFY_FETCHES
	mustCallPDO(t, z, db, "setAttribute", types.NewLong(ATTR_ERRMODE), types.NewLong(ERRMODE_EXCEPTION))
	mustCallPDO(t, z, db, "setAttribute", types.NewLong(ATTR_CASE), types.NewLong(CASE_UPPER))
	mustCallPDO(t, z, db, "setAttribute", types.NewLong(ATTR_STRINGIFY_FETCHES), types.NewBool(true))
	mustCallPDO(t, z, db, "exec", mustStr(t, z, "CREATE TABLE t (num INTEGER); INSERT INTO t VALUES (42);"))
	q := mustCallPDO(t, z, db, "query", mustStr(t, z, "SELECT num FROM t"))
	row := mustCallStmt(t, z, q, "fetch", types.NewLong(FETCH_ASSOC))
	val, found, _ := types.ArrayGet(z, row, mustStr(t, z, "NUM"), warnDrop)
	if !found || val.Type != types.TypeString || types.StringData(z, val) != "42" {
		t.Fatalf("CASE_UPPER + STRINGIFY_FETCHES: found=%v val=%v", found, val)
	}
}
