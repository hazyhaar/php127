package pdo

import (
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	ClassPDO          uint32 = 0x50444f01
	ClassPDOStatement uint32 = 0x50444f02
	ClassPDOException uint32 = 0x50444f03
	ClassStdClass     uint32 = 0x5c1a55

	FETCH_DEFAULT  = 0
	FETCH_LAZY     = 1
	FETCH_ASSOC    = 2
	FETCH_NUM      = 3
	FETCH_BOTH     = 4
	FETCH_OBJ      = 5
	FETCH_BOUND    = 6
	FETCH_COLUMN   = 7
	FETCH_CLASS    = 8
	FETCH_INTO     = 9
	FETCH_KEY_PAIR = 12
	FETCH_GROUP    = 65536
	FETCH_UNIQUE   = 196608

	PARAM_NULL = 0
	PARAM_INT  = 1
	PARAM_STR  = 2
	PARAM_LOB  = 3
	PARAM_BOOL = 5

	ATTR_AUTOCOMMIT          = 0
	ATTR_ERRMODE             = 3
	ATTR_CASE                = 8
	ATTR_ORACLE_NULLS        = 11
	ATTR_PERSISTENT          = 12
	ATTR_STATEMENT_CLASS     = 13
	ATTR_FETCH_TABLE_NAMES   = 14
	ATTR_FETCH_CATALOG_NAMES = 15
	ATTR_DRIVER_NAME         = 16
	ATTR_STRINGIFY_FETCHES   = 17
	ATTR_DEFAULT_FETCH_MODE  = 19
	ATTR_EMULATE_PREPARES    = 20

	ERRMODE_SILENT    = 0
	ERRMODE_WARNING   = 1
	ERRMODE_EXCEPTION = 2

	CASE_NATURAL = 0
	CASE_LOWER   = 1
	CASE_UPPER   = 2

	NULL_NATURAL      = 0
	NULL_EMPTY_STRING = 1
	NULL_TO_STRING    = 2

	ERR_NONE = "00000"
)

type TypeError struct{ text string }
type ValueError struct{ text string }
type ArgumentCountError struct{ text string }
type Error struct{ text string }

func (e TypeError) Error() string          { return e.text }
func (e ValueError) Error() string         { return e.text }
func (e ArgumentCountError) Error() string { return e.text }
func (e Error) Error() string              { return e.text }

// PDOException représente une erreur PDO SQLSTATE avec code pilote et message.
type PDOException struct {
	SQLState   string
	DriverCode int
	DriverMsg  string
	Message    string
}

func (e PDOException) Error() string { return e.Message }

// ExceptionClass retourne le nom de la classe d'exception PHP correspondant à err.
func ExceptionClass(err error) string {
	if err == nil {
		return ""
	}
	switch err.(type) {
	case PDOException:
		return "PDOException"
	case TypeError:
		return "TypeError"
	case ValueError:
		return "ValueError"
	case ArgumentCountError:
		return "ArgumentCountError"
	case Error:
		return "Error"
	default:
		return ""
	}
}

func newPDOException(sqlState string, driverCode int, driverMsg string) PDOException {
	if sqlState == "" {
		sqlState = "HY000"
	}
	var msg string
	if driverCode != 0 {
		msg = "SQLSTATE[" + sqlState + "]: General error: " + strconv.Itoa(driverCode) + " " + driverMsg
		if sqlState == "23000" {
			msg = "SQLSTATE[23000]: Integrity constraint violation: " + strconv.Itoa(driverCode) + " " + driverMsg
		}
	} else {
		msg = "SQLSTATE[" + sqlState + "]: " + driverMsg
	}
	return PDOException{
		SQLState:   sqlState,
		DriverCode: driverCode,
		DriverMsg:  driverMsg,
		Message:    msg,
	}
}

// NewPDOExceptionObject construit une instance types.TypeObject de PDOException dans la Zone.
func NewPDOExceptionObject(z *zone.Zone, ex PDOException) (types.Value, error) {
	obj, err := types.NewObject(z, ClassPDOException, 5, false)
	if err != nil {
		return types.Value{}, err
	}
	msgVal, err := newStr(z, ex.Message)
	if err != nil {
		types.ReleaseValue(z, &obj)
		return types.Value{}, err
	}
	types.ObjectSet(z, obj, 0, msgVal)
	types.ReleaseValue(z, &msgVal)
	types.ObjectSet(z, obj, 1, types.NewLong(int64(ex.DriverCode)))

	infoArr, err := buildErrorInfoArray(z, ex.SQLState, ex.DriverCode, ex.DriverMsg, true)
	if err == nil {
		types.ObjectSet(z, obj, 4, infoArr)
		types.ReleaseValue(z, &infoArr)
	}
	return obj, nil
}

// MethodFunc est la signature d'une méthode d'instance sur PDO ou PDOStatement.
type MethodFunc func(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error)

type boundParam struct {
	isRef     bool
	refVal    types.Value
	valKind   types.Type
	ival      int64
	fval      float64
	sval      string
	paramType int64
}

type connHandle struct {
	id               uint32
	db               *sqliteDB
	dsn              string
	errMode          int64
	caseMode         int64
	oracleNulls      int64
	stringifyFetches bool
	fetchTableNames  bool
	defaultFetchMode int64
	emulatePrepares  bool
	persistent       bool
	inTx             bool
	sqlState         string
	driverCode       int
	driverMsg        string
	hasError         bool
	closed           bool
}

type stmtHandle struct {
	id         uint32
	conn       *connHandle
	stmt       *sqliteStmt
	query      string
	fetchMode  int64
	fetchCol   int64
	fetchClass string
	colNames   []string
	colTables  []string
	rows       [][]rawCell
	cursor     int
	rowCount   int64
	executed   bool
	byPos      map[int]boundParam
	byName     map[string]boundParam
	sqlState   string
	driverCode int
	driverMsg  string
	hasError   bool
	closed     bool
}

// ZonePDO détient les connexions SQLite et requêtes préparées ouvertes dans une Zone.
type ZonePDO struct {
	mu      sync.Mutex
	zone    *zone.Zone
	zoneID  uint32
	nextID  uint32
	conns   map[uint32]*connHandle
	stmts   map[uint32]*stmtHandle
	pending int
}

var (
	pdoMu       sync.Mutex
	nextZoneID  uint32 = 1
	byZone             = make(map[*zone.Zone]*ZonePDO)
	byZoneID           = make(map[uint32]*ZonePDO)
	classConsts map[string]types.Value
)

func init() {
	safeRegisterClass(ClassPDO, "PDO")
	safeRegisterClass(ClassPDOStatement, "PDOStatement")
	safeRegisterClass(ClassPDOException, "PDOException")
	safeRegisterClass(ClassStdClass, "stdClass")

	classConsts = map[string]types.Value{
		"FETCH_DEFAULT":            types.NewLong(FETCH_DEFAULT),
		"FETCH_LAZY":               types.NewLong(FETCH_LAZY),
		"FETCH_ASSOC":              types.NewLong(FETCH_ASSOC),
		"FETCH_NUM":                types.NewLong(FETCH_NUM),
		"FETCH_BOTH":               types.NewLong(FETCH_BOTH),
		"FETCH_OBJ":                types.NewLong(FETCH_OBJ),
		"FETCH_BOUND":              types.NewLong(FETCH_BOUND),
		"FETCH_COLUMN":             types.NewLong(FETCH_COLUMN),
		"FETCH_CLASS":              types.NewLong(FETCH_CLASS),
		"FETCH_INTO":               types.NewLong(FETCH_INTO),
		"FETCH_KEY_PAIR":           types.NewLong(FETCH_KEY_PAIR),
		"FETCH_GROUP":              types.NewLong(FETCH_GROUP),
		"FETCH_UNIQUE":             types.NewLong(FETCH_UNIQUE),
		"PARAM_NULL":               types.NewLong(PARAM_NULL),
		"PARAM_INT":                types.NewLong(PARAM_INT),
		"PARAM_STR":                types.NewLong(PARAM_STR),
		"PARAM_LOB":                types.NewLong(PARAM_LOB),
		"PARAM_BOOL":               types.NewLong(PARAM_BOOL),
		"ATTR_AUTOCOMMIT":          types.NewLong(ATTR_AUTOCOMMIT),
		"ATTR_ERRMODE":             types.NewLong(ATTR_ERRMODE),
		"ATTR_CASE":                types.NewLong(ATTR_CASE),
		"ATTR_ORACLE_NULLS":        types.NewLong(ATTR_ORACLE_NULLS),
		"ATTR_PERSISTENT":          types.NewLong(ATTR_PERSISTENT),
		"ATTR_STATEMENT_CLASS":     types.NewLong(ATTR_STATEMENT_CLASS),
		"ATTR_FETCH_TABLE_NAMES":   types.NewLong(ATTR_FETCH_TABLE_NAMES),
		"ATTR_FETCH_CATALOG_NAMES": types.NewLong(ATTR_FETCH_CATALOG_NAMES),
		"ATTR_DRIVER_NAME":         types.NewLong(ATTR_DRIVER_NAME),
		"ATTR_STRINGIFY_FETCHES":   types.NewLong(ATTR_STRINGIFY_FETCHES),
		"ATTR_DEFAULT_FETCH_MODE":  types.NewLong(ATTR_DEFAULT_FETCH_MODE),
		"ATTR_EMULATE_PREPARES":    types.NewLong(ATTR_EMULATE_PREPARES),
		"ERRMODE_SILENT":           types.NewLong(ERRMODE_SILENT),
		"ERRMODE_WARNING":          types.NewLong(ERRMODE_WARNING),
		"ERRMODE_EXCEPTION":        types.NewLong(ERRMODE_EXCEPTION),
		"CASE_NATURAL":             types.NewLong(CASE_NATURAL),
		"CASE_LOWER":               types.NewLong(CASE_LOWER),
		"CASE_UPPER":               types.NewLong(CASE_UPPER),
		"NULL_NATURAL":             types.NewLong(NULL_NATURAL),
		"NULL_EMPTY_STRING":        types.NewLong(NULL_EMPTY_STRING),
		"NULL_TO_STRING":           types.NewLong(NULL_TO_STRING),
	}
	if !types.IsProcessZoneFrozen() {
		if v, err := types.InternProcessString(ERR_NONE); err == nil {
			classConsts["ERR_NONE"] = v
		}
	}
}

func safeRegisterClass(id uint32, name string) {
	defer func() { _ = recover() }()
	if !types.IsProcessZoneFrozen() {
		types.RegisterObjectClassName(id, name)
	}
}

// Constants retourne la table des constantes de classe PDO::*.
func Constants() map[string]types.Value {
	return classConsts
}

// LookupClassConstant recherche une constante de classe sur PDO.
func LookupClassConstant(className, constName string) (types.Value, bool) {
	if !strings.EqualFold(className, "PDO") {
		return types.Value{}, false
	}
	v, ok := classConsts[constName]
	return v, ok
}

func getZonePDO(z *zone.Zone) *ZonePDO {
	pdoMu.Lock()
	defer pdoMu.Unlock()
	if zp, ok := byZone[z]; ok {
		return zp
	}
	id := nextZoneID
	nextZoneID++
	zp := &ZonePDO{
		zone:   z,
		zoneID: id,
		nextID: 1,
		conns:  make(map[uint32]*connHandle),
		stmts:  make(map[uint32]*stmtHandle),
	}
	byZone[z] = zp
	byZoneID[id] = zp
	return zp
}

func (zp *ZonePDO) registerConn(c *connHandle) uint32 {
	zp.mu.Lock()
	id := zp.nextID
	zp.nextID++
	c.id = id
	zp.conns[id] = c
	zp.pending++
	zp.mu.Unlock()

	if zp.zone != nil {
		zp.zone.RegisterCleanup(zone.CleanupItem{
			Kind:   zone.CleanupTransaction,
			Target: zp.zoneID,
			Extra:  id,
		})
	}
	return id
}

func (zp *ZonePDO) registerStmt(s *stmtHandle) uint32 {
	zp.mu.Lock()
	id := zp.nextID
	zp.nextID++
	s.id = id
	zp.stmts[id] = s
	zp.pending++
	zp.mu.Unlock()

	if zp.zone != nil {
		zp.zone.RegisterCleanup(zone.CleanupItem{
			Kind:   zone.CleanupTransaction,
			Target: zp.zoneID,
			Extra:  id | (1 << 31),
		})
	}
	return id
}

func (s *stmtHandle) releaseBoundRefs(z *zone.Zone) {
	if z == nil {
		return
	}
	for k, bp := range s.byPos {
		if bp.isRef {
			v := bp.refVal
			types.ReleaseValue(z, &v)
			bp.isRef = false
			bp.refVal = types.Value{}
			s.byPos[k] = bp
		}
	}
	for k, bp := range s.byName {
		if bp.isRef {
			v := bp.refVal
			types.ReleaseValue(z, &v)
			bp.isRef = false
			bp.refVal = types.Value{}
			s.byName[k] = bp
		}
	}
}

func (zp *ZonePDO) closeStmt(id uint32) {
	zp.mu.Lock()
	s, ok := zp.stmts[id]
	if !ok || s.closed {
		zp.mu.Unlock()
		return
	}
	s.closed = true
	delete(zp.stmts, id)
	z := zp.zone
	zp.mu.Unlock()

	s.releaseBoundRefs(z)
	if s.stmt != nil {
		s.stmt.finalize()
		s.stmt = nil
	}
	s.rows = nil
}

func (zp *ZonePDO) closeConn(id uint32) {
	zp.mu.Lock()
	c, ok := zp.conns[id]
	if !ok || c.closed {
		zp.mu.Unlock()
		return
	}
	c.closed = true
	delete(zp.conns, id)
	var childStmts []uint32
	for sid, st := range zp.stmts {
		if st.conn == c && !st.closed {
			childStmts = append(childStmts, sid)
		}
	}
	zp.mu.Unlock()

	for _, sid := range childStmts {
		zp.closeStmt(sid)
	}
	if c.db != nil {
		if c.inTx || c.db.inTransaction() {
			_, _, _ = c.db.execRaw("ROLLBACK")
		}
		c.db.close()
		c.db = nil
	}
}

// DispatchCleanup exécute la fermeture automatique des requêtes et connexions SQLite lors de Zone.Reset.
func DispatchCleanup(item zone.CleanupItem) {
	if item.Kind != zone.CleanupTransaction {
		return
	}
	pdoMu.Lock()
	zp := byZoneID[item.Target]
	pdoMu.Unlock()
	if zp == nil {
		return
	}
	if item.Extra&(1<<31) != 0 {
		zp.closeStmt(item.Extra &^ (1 << 31))
	} else {
		zp.closeConn(item.Extra)
	}
	zp.mu.Lock()
	zp.pending--
	empty := zp.pending <= 0 && len(zp.conns) == 0 && len(zp.stmts) == 0
	zp.mu.Unlock()
	if empty {
		pdoMu.Lock()
		delete(byZone, zp.zone)
		delete(byZoneID, zp.zoneID)
		pdoMu.Unlock()
	}
}

// DispatchZoneCleanup exécute la fermeture automatique d'une ressource PDO pour une Zone.
func DispatchZoneCleanup(_ *zone.Zone, item zone.CleanupItem) {
	DispatchCleanup(item)
}

// CloseAll ferme immédiatement toutes les requêtes et connexions SQLite ouvertes dans la Zone.
func CloseAll(z *zone.Zone) {
	pdoMu.Lock()
	zp := byZone[z]
	if zp != nil {
		delete(byZone, z)
		delete(byZoneID, zp.zoneID)
	}
	pdoMu.Unlock()
	if zp == nil {
		return
	}
	zp.mu.Lock()
	stmtIDs := make([]uint32, 0, len(zp.stmts))
	for id := range zp.stmts {
		stmtIDs = append(stmtIDs, id)
	}
	connIDs := make([]uint32, 0, len(zp.conns))
	for id := range zp.conns {
		connIDs = append(connIDs, id)
	}
	zp.mu.Unlock()

	for _, id := range stmtIDs {
		zp.closeStmt(id)
	}
	for _, id := range connIDs {
		zp.closeConn(id)
	}
}

// OpenConnCount retourne le nombre de connexions SQLite ouvertes dans la Zone.
func OpenConnCount(z *zone.Zone) int {
	pdoMu.Lock()
	zp := byZone[z]
	pdoMu.Unlock()
	if zp == nil {
		return 0
	}
	zp.mu.Lock()
	defer zp.mu.Unlock()
	return len(zp.conns)
}

// OpenStmtCount retourne le nombre de requêtes préparées ouvertes dans la Zone.
func OpenStmtCount(z *zone.Zone) int {
	pdoMu.Lock()
	zp := byZone[z]
	pdoMu.Unlock()
	if zp == nil {
		return 0
	}
	zp.mu.Lock()
	defer zp.mu.Unlock()
	return len(zp.stmts)
}

func sqlStateForCode(rc int) string {
	switch rc {
	case sqliteOK, sqliteRow, sqliteDone:
		return ERR_NONE
	case sqliteConstraint:
		return "23000"
	default:
		if (rc & 0xff) == sqliteConstraint {
			return "23000"
		}
		return "HY000"
	}
}

func (c *connHandle) clearError() {
	c.sqlState = ERR_NONE
	c.driverCode = 0
	c.driverMsg = ""
	c.hasError = false
}

func (c *connHandle) recordError(rc int, msg string) error {
	state := sqlStateForCode(rc)
	c.sqlState = state
	c.driverCode = rc
	c.driverMsg = msg
	c.hasError = true
	if c.errMode == ERRMODE_EXCEPTION {
		return newPDOException(state, rc, msg)
	}
	return nil
}

func (s *stmtHandle) clearError() {
	s.sqlState = ERR_NONE
	s.driverCode = 0
	s.driverMsg = ""
	s.hasError = false
}

func (s *stmtHandle) recordError(rc int, msg string) error {
	state := sqlStateForCode(rc)
	s.sqlState = state
	s.driverCode = rc
	s.driverMsg = msg
	s.hasError = true
	if s.conn != nil {
		s.conn.sqlState = state
		s.conn.driverCode = rc
		s.conn.driverMsg = msg
		s.conn.hasError = true
		if s.conn.errMode == ERRMODE_EXCEPTION {
			return newPDOException(state, rc, msg)
		}
	}
	return nil
}

func deref(z *zone.Zone, v types.Value) types.Value { return types.Deref(z, v) }

func newStr(z *zone.Zone, text string) (types.Value, error) {
	if len(text) <= 1 {
		return types.NewStringLiteral(z, text)
	}
	return types.NewString(z, text, 0)
}

func warnDrop(types.ArrayWarning) error { return nil }

func setArrayKey(z *zone.Zone, a *types.Value, key string, val types.Value) error {
	k, err := newStr(z, key)
	if err != nil {
		return err
	}
	defer types.ReleaseValue(z, &k)
	return types.ArraySet(z, a, k, val, warnDrop)
}

func setArrayIntKey(z *zone.Zone, a *types.Value, key int64, val types.Value) error {
	return types.ArraySet(z, a, types.NewLong(key), val, warnDrop)
}

func castString(z *zone.Zone, v types.Value) (string, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull, types.TypeFalse:
		return "", nil
	case types.TypeTrue:
		return "1", nil
	case types.TypeLong:
		return strconv.FormatInt(int64(v.Scalar), 10), nil
	case types.TypeDouble:
		f := v.ToDouble()
		if math.IsNaN(f) {
			return "NAN", nil
		}
		if math.IsInf(f, 1) {
			return "INF", nil
		}
		if math.IsInf(f, -1) {
			return "-INF", nil
		}
		return strconv.FormatFloat(f, 'G', 14, 64), nil
	case types.TypeString:
		return types.StringData(z, v), nil
	default:
		return "", TypeError{"conversion en chaîne impossible"}
	}
}

func asInt(z *zone.Zone, v types.Value) (int64, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeLong:
		return int64(v.Scalar), nil
	case types.TypeTrue:
		return 1, nil
	case types.TypeFalse, types.TypeNull, types.TypeUndef:
		return 0, nil
	case types.TypeDouble:
		return v.ToLong(), nil
	case types.TypeString:
		s := strings.TrimSpace(types.StringData(z, v))
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, nil
		}
		return 0, TypeError{"entier attendu"}
	default:
		return 0, TypeError{"entier attendu"}
	}
}

func asBool(z *zone.Zone, v types.Value) bool {
	v = deref(z, v)
	if v.Type == types.TypeResource {
		return true
	}
	return types.ToBoolZ(z, v)
}

func arity(name string, n, minN, maxN int) error {
	if n < minN {
		word := "arguments"
		if minN == 1 {
			word = "argument"
		}
		how := "at least"
		if minN == maxN {
			how = "exactly"
		}
		return ArgumentCountError{name + "() expects " + how + " " + strconv.Itoa(minN) + " " + word + ", " + strconv.Itoa(n) + " given"}
	}
	if maxN >= 0 && n > maxN {
		word := "arguments"
		if maxN == 1 {
			word = "argument"
		}
		return ArgumentCountError{name + "() expects at most " + strconv.Itoa(maxN) + " " + word + ", " + strconv.Itoa(n) + " given"}
	}
	return nil
}

func parseSQLiteDSN(dsn string) (string, error) {
	before, after, ok := strings.Cut(dsn, ":")
	if !ok {
		return "", newPDOException("HY000", 0, "invalid data source name")
	}
	driver := strings.ToLower(before)
	rest := after
	if driver != "sqlite" && driver != "sqlite3" {
		return "", newPDOException("HY000", 0, "could not find driver")
	}
	if rest == "" {
		return ":memory:", nil
	}
	return rest, nil
}

func lookupConn(z *zone.Zone, this types.Value) (*ZonePDO, *connHandle, error) {
	this = deref(z, this)
	if this.Type != types.TypeObject {
		return nil, nil, Error{"Call to a member function on non-object"}
	}
	info := types.ObjectInfo(z, this)
	if info.PropCount < 1 {
		return nil, nil, Error{"Invalid PDO object"}
	}
	idVal := types.ObjectGet(z, this, 0)
	if idVal.Type != types.TypeLong {
		return nil, nil, Error{"Uninitialized PDO object"}
	}
	zp := getZonePDO(z)
	zp.mu.Lock()
	c, ok := zp.conns[uint32(idVal.ToLong())]
	zp.mu.Unlock()
	if !ok || c.closed {
		return nil, nil, Error{"PDO connection is closed"}
	}
	return zp, c, nil
}

func lookupStmt(z *zone.Zone, this types.Value) (*ZonePDO, *stmtHandle, error) {
	this = deref(z, this)
	if this.Type != types.TypeObject {
		return nil, nil, Error{"Call to a member function on non-object"}
	}
	info := types.ObjectInfo(z, this)
	if info.PropCount < 2 {
		return nil, nil, Error{"Invalid PDOStatement object"}
	}
	idVal := types.ObjectGet(z, this, 1)
	if idVal.Type != types.TypeLong {
		return nil, nil, Error{"Uninitialized PDOStatement object"}
	}
	zp := getZonePDO(z)
	zp.mu.Lock()
	s, ok := zp.stmts[uint32(idVal.ToLong())]
	zp.mu.Unlock()
	if !ok || s.closed {
		return nil, nil, Error{"PDOStatement is closed"}
	}
	return zp, s, nil
}

// NewPDO ouvre une connexion SQLite réelle dans la Zone et retourne l'objet PDO.
func NewPDO(z *zone.Zone, args ...types.Value) (types.Value, error) {
	obj, err := types.NewObject(z, ClassPDO, 1, false)
	if err != nil {
		return types.Value{}, err
	}
	if _, err := pdoConstruct(z, obj, args); err != nil {
		types.ReleaseValue(z, &obj)
		return types.Value{}, err
	}
	return obj, nil
}

func pdoConstruct(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::__construct", len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	dsn, err := castString(z, args[0])
	if err != nil {
		return types.Value{}, TypeError{"PDO::__construct(): Argument #1 ($dsn) must be of type string"}
	}
	path, err := parseSQLiteDSN(dsn)
	if err != nil {
		return types.Value{}, err
	}
	db, rc, msg := sqliteOpen(path)
	if rc != sqliteOK {
		return types.Value{}, newPDOException("HY000", rc, msg)
	}
	c := &connHandle{
		db:               db,
		dsn:              dsn,
		errMode:          ERRMODE_EXCEPTION,
		caseMode:         CASE_NATURAL,
		oracleNulls:      NULL_NATURAL,
		defaultFetchMode: FETCH_BOTH,
		emulatePrepares:  false,
	}
	if len(args) >= 4 && deref(z, args[3]).Type == types.TypeArray {
		opts := deref(z, args[3])
		types.ArrayForEach(z, opts, func(k, v types.Value) bool {
			if attr, kErr := asInt(z, k); kErr == nil {
				_ = c.applyAttribute(z, attr, v)
			}
			return true
		})
	}
	zp := getZonePDO(z)
	id := zp.registerConn(c)
	types.ObjectSet(z, this, 0, types.NewLong(int64(id)))
	return types.NewNull(), nil
}

func (c *connHandle) applyAttribute(z *zone.Zone, attr int64, val types.Value) bool {
	val = deref(z, val)
	switch attr {
	case ATTR_ERRMODE:
		n, err := asInt(z, val)
		if err != nil || (n != ERRMODE_SILENT && n != ERRMODE_WARNING && n != ERRMODE_EXCEPTION) {
			return false
		}
		c.errMode = n
		return true
	case ATTR_CASE:
		n, err := asInt(z, val)
		if err != nil || (n != CASE_NATURAL && n != CASE_LOWER && n != CASE_UPPER) {
			return false
		}
		c.caseMode = n
		return true
	case ATTR_ORACLE_NULLS:
		n, err := asInt(z, val)
		if err != nil || (n != NULL_NATURAL && n != NULL_EMPTY_STRING && n != NULL_TO_STRING) {
			return false
		}
		c.oracleNulls = n
		return true
	case ATTR_STRINGIFY_FETCHES:
		c.stringifyFetches = asBool(z, val)
		return true
	case ATTR_FETCH_TABLE_NAMES:
		c.fetchTableNames = asBool(z, val)
		return true
	case ATTR_DEFAULT_FETCH_MODE:
		n, err := asInt(z, val)
		if err != nil {
			return false
		}
		c.defaultFetchMode = n
		return true
	case ATTR_EMULATE_PREPARES:
		c.emulatePrepares = asBool(z, val)
		return true
	case ATTR_PERSISTENT:
		c.persistent = asBool(z, val)
		return true
	case ATTR_AUTOCOMMIT:
		return true
	default:
		return false
	}
}

func pdoExec(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::exec", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, c, err := lookupConn(z, this)
	if err != nil {
		return types.Value{}, err
	}
	sql, err := castString(z, args[0])
	if err != nil {
		return types.Value{}, TypeError{"PDO::exec(): Argument #1 ($statement) must be of type string"}
	}
	if strings.TrimSpace(sql) == "" {
		return types.NewLong(0), nil
	}
	changes, rc, msg := c.db.execRaw(sql)
	c.inTx = c.db.inTransaction()
	if rc != sqliteOK {
		if err := c.recordError(rc, msg); err != nil {
			return types.NewBool(false), err
		}
		return types.NewBool(false), nil
	}
	c.clearError()
	return types.NewLong(changes), nil
}

func newStmtObject(z *zone.Zone, zp *ZonePDO, s *stmtHandle) (types.Value, error) {
	obj, err := types.NewObject(z, ClassPDOStatement, 2, false)
	if err != nil {
		return types.Value{}, err
	}
	qStr, err := newStr(z, s.query)
	if err != nil {
		types.ReleaseValue(z, &obj)
		return types.Value{}, err
	}
	id := zp.registerStmt(s)
	types.ObjectSet(z, obj, 0, qStr)
	types.ReleaseValue(z, &qStr)
	types.ObjectSet(z, obj, 1, types.NewLong(int64(id)))
	return obj, nil
}

func pdoPrepare(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::prepare", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	zp, c, err := lookupConn(z, this)
	if err != nil {
		return types.Value{}, err
	}
	sql, err := castString(z, args[0])
	if err != nil {
		return types.Value{}, TypeError{"PDO::prepare(): Argument #1 ($query) must be of type string"}
	}
	st, rc, msg := c.db.prepare(sql)
	if rc != sqliteOK {
		if err := c.recordError(rc, msg); err != nil {
			return types.NewBool(false), err
		}
		return types.NewBool(false), nil
	}
	c.clearError()
	colNames, colTables := st.columnMeta()
	sh := &stmtHandle{
		conn:       c,
		stmt:       st,
		query:      sql,
		fetchMode:  c.defaultFetchMode,
		fetchClass: "stdClass",
		colNames:   colNames,
		colTables:  colTables,
		byPos:      make(map[int]boundParam),
		byName:     make(map[string]boundParam),
	}
	return newStmtObject(z, zp, sh)
}

func pdoQuery(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::query", len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	stmtObj, err := pdoPrepare(z, this, args[:1])
	if err != nil || stmtObj.Type != types.TypeObject {
		return stmtObj, err
	}
	if len(args) >= 2 && !deref(z, args[1]).IsNull() {
		if _, err := stmtSetFetchMode(z, stmtObj, args[1:]); err != nil {
			types.ReleaseValue(z, &stmtObj)
			return types.NewBool(false), err
		}
	}
	okVal, err := stmtExecute(z, stmtObj, nil)
	if err != nil {
		types.ReleaseValue(z, &stmtObj)
		return types.NewBool(false), err
	}
	if okVal.Type == types.TypeFalse {
		types.ReleaseValue(z, &stmtObj)
		return types.NewBool(false), nil
	}
	return stmtObj, nil
}

func pdoBeginTransaction(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::beginTransaction", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	_, c, err := lookupConn(z, this)
	if err != nil {
		return types.Value{}, err
	}
	if c.inTx || c.db.inTransaction() {
		return types.NewBool(false), newPDOException("HY000", 0, "There is already an active transaction")
	}
	_, rc, msg := c.db.execRaw("BEGIN")
	if rc != sqliteOK {
		if err := c.recordError(rc, msg); err != nil {
			return types.NewBool(false), err
		}
		return types.NewBool(false), nil
	}
	c.inTx = true
	c.clearError()
	return types.NewBool(true), nil
}

func pdoCommit(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::commit", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	_, c, err := lookupConn(z, this)
	if err != nil {
		return types.Value{}, err
	}
	if !c.inTx && !c.db.inTransaction() {
		return types.NewBool(false), newPDOException("HY000", 0, "There is no active transaction")
	}
	_, rc, msg := c.db.execRaw("COMMIT")
	c.inTx = c.db.inTransaction()
	if rc != sqliteOK {
		if err := c.recordError(rc, msg); err != nil {
			return types.NewBool(false), err
		}
		return types.NewBool(false), nil
	}
	c.clearError()
	return types.NewBool(true), nil
}

func pdoRollBack(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::rollBack", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	_, c, err := lookupConn(z, this)
	if err != nil {
		return types.Value{}, err
	}
	if !c.inTx && !c.db.inTransaction() {
		return types.NewBool(false), newPDOException("HY000", 0, "There is no active transaction")
	}
	_, rc, msg := c.db.execRaw("ROLLBACK")
	c.inTx = c.db.inTransaction()
	if rc != sqliteOK {
		if err := c.recordError(rc, msg); err != nil {
			return types.NewBool(false), err
		}
		return types.NewBool(false), nil
	}
	c.clearError()
	return types.NewBool(true), nil
}

func pdoInTransaction(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::inTransaction", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	_, c, err := lookupConn(z, this)
	if err != nil {
		return types.Value{}, err
	}
	return types.NewBool(c.inTx || c.db.inTransaction()), nil
}

func pdoLastInsertId(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::lastInsertId", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	_, c, err := lookupConn(z, this)
	if err != nil {
		return types.Value{}, err
	}
	id := c.db.lastInsertRowID()
	return newStr(z, strconv.FormatInt(id, 10))
}

func pdoQuote(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::quote", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	_, _, err := lookupConn(z, this)
	if err != nil {
		return types.Value{}, err
	}
	s, err := castString(z, args[0])
	if err != nil {
		return types.Value{}, TypeError{"PDO::quote(): Argument #1 ($string) must be of type string"}
	}
	if strings.IndexByte(s, 0) >= 0 {
		return types.NewBool(false), nil
	}
	quoted := "'" + strings.ReplaceAll(s, "'", "''") + "'"
	return newStr(z, quoted)
}

func buildErrorInfoArray(z *zone.Zone, sqlState string, driverCode int, driverMsg string, hasErr bool) (types.Value, error) {
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	st := sqlState
	if st == "" {
		st = ERR_NONE
	}
	sv, err := newStr(z, st)
	if err != nil {
		types.ReleaseValue(z, &arr)
		return types.Value{}, err
	}
	_ = types.ArrayAppend(z, &arr, sv)
	types.ReleaseValue(z, &sv)

	if hasErr && st != ERR_NONE {
		_ = types.ArrayAppend(z, &arr, types.NewLong(int64(driverCode)))
		mv, err := newStr(z, driverMsg)
		if err == nil {
			_ = types.ArrayAppend(z, &arr, mv)
			types.ReleaseValue(z, &mv)
		} else {
			_ = types.ArrayAppend(z, &arr, types.NewNull())
		}
	} else {
		_ = types.ArrayAppend(z, &arr, types.NewNull())
		_ = types.ArrayAppend(z, &arr, types.NewNull())
	}
	return arr, nil
}

func pdoErrorCode(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::errorCode", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	_, c, err := lookupConn(z, this)
	if err != nil {
		return types.Value{}, err
	}
	if c.sqlState == "" {
		return types.NewNull(), nil
	}
	return newStr(z, c.sqlState)
}

func pdoErrorInfo(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::errorInfo", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	_, c, err := lookupConn(z, this)
	if err != nil {
		return types.Value{}, err
	}
	return buildErrorInfoArray(z, c.sqlState, c.driverCode, c.driverMsg, c.hasError)
}

func pdoGetAttribute(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::getAttribute", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, c, err := lookupConn(z, this)
	if err != nil {
		return types.Value{}, err
	}
	attr, err := asInt(z, args[0])
	if err != nil {
		return types.Value{}, TypeError{"PDO::getAttribute(): Argument #1 ($attribute) must be of type int"}
	}
	switch attr {
	case ATTR_AUTOCOMMIT:
		return types.NewBool(!c.inTx && !c.db.inTransaction()), nil
	case ATTR_ERRMODE:
		return types.NewLong(c.errMode), nil
	case ATTR_CASE:
		return types.NewLong(c.caseMode), nil
	case ATTR_ORACLE_NULLS:
		return types.NewLong(c.oracleNulls), nil
	case ATTR_PERSISTENT:
		return types.NewBool(c.persistent), nil
	case ATTR_DRIVER_NAME:
		return newStr(z, "sqlite")
	case ATTR_STRINGIFY_FETCHES:
		return types.NewBool(c.stringifyFetches), nil
	case ATTR_FETCH_TABLE_NAMES:
		return types.NewBool(c.fetchTableNames), nil
	case ATTR_DEFAULT_FETCH_MODE:
		return types.NewLong(c.defaultFetchMode), nil
	case ATTR_EMULATE_PREPARES:
		return types.NewBool(c.emulatePrepares), nil
	default:
		return types.NewBool(false), nil
	}
}

func pdoSetAttribute(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDO::setAttribute", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, c, err := lookupConn(z, this)
	if err != nil {
		return types.Value{}, err
	}
	attr, err := asInt(z, args[0])
	if err != nil {
		return types.Value{}, TypeError{"PDO::setAttribute(): Argument #1 ($attribute) must be of type int"}
	}
	return types.NewBool(c.applyAttribute(z, attr, args[1])), nil
}

// GetAvailableDrivers implémente PDO::getAvailableDrivers().
func GetAvailableDrivers(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("PDO::getAvailableDrivers", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	sv, err := newStr(z, "sqlite")
	if err != nil {
		types.ReleaseValue(z, &arr)
		return types.Value{}, err
	}
	_ = types.ArrayAppend(z, &arr, sv)
	types.ReleaseValue(z, &sv)
	return arr, nil
}

func snapshotBoundValue(z *zone.Zone, v types.Value, paramType int64) boundParam {
	v = deref(z, v)
	bp := boundParam{paramType: paramType, valKind: v.Type}
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		bp.valKind = types.TypeNull
	case types.TypeFalse:
		bp.ival = 0
	case types.TypeTrue:
		bp.ival = 1
	case types.TypeLong:
		bp.ival = int64(v.Scalar)
	case types.TypeDouble:
		bp.fval = v.ToDouble()
	case types.TypeString:
		bp.sval = types.StringData(z, v)
	default:
		s, _ := castString(z, v)
		bp.valKind = types.TypeString
		bp.sval = s
	}
	return bp
}

func bindOneToSQLite(z *zone.Zone, st *sqliteStmt, idx int, bp boundParam) int {
	if idx <= 0 || idx > st.paramCount() {
		return sqliteOK
	}
	if bp.isRef {
		resolved := snapshotBoundValue(z, bp.refVal, bp.paramType)
		bp.valKind = resolved.valKind
		bp.ival = resolved.ival
		bp.fval = resolved.fval
		bp.sval = resolved.sval
	}
	if bp.paramType == PARAM_NULL || bp.valKind == types.TypeNull || bp.valKind == types.TypeUndef {
		return st.bindNull(idx)
	}
	switch bp.paramType {
	case PARAM_INT:
		switch bp.valKind {
		case types.TypeLong, types.TypeFalse, types.TypeTrue:
			return st.bindInt64(idx, bp.ival)
		case types.TypeDouble:
			return st.bindInt64(idx, int64(bp.fval))
		case types.TypeString:
			if n, err := strconv.ParseInt(strings.TrimSpace(bp.sval), 10, 64); err == nil {
				return st.bindInt64(idx, n)
			}
			return st.bindText(idx, bp.sval)
		}
	case PARAM_BOOL:
		var b int64
		switch bp.valKind {
		case types.TypeTrue:
			b = 1
		case types.TypeLong:
			if bp.ival != 0 {
				b = 1
			}
		case types.TypeDouble:
			if bp.fval != 0 {
				b = 1
			}
		case types.TypeString:
			if bp.sval != "" && bp.sval != "0" {
				b = 1
			}
		}
		return st.bindInt64(idx, b)
	case PARAM_LOB:
		return st.bindBlob(idx, []byte(bp.sval))
	}
	switch bp.valKind {
	case types.TypeLong:
		if bp.paramType == -1 {
			return st.bindInt64(idx, bp.ival)
		}
		return st.bindText(idx, strconv.FormatInt(bp.ival, 10))
	case types.TypeDouble:
		if bp.paramType == -1 {
			return st.bindDouble(idx, bp.fval)
		}
		return st.bindText(idx, strconv.FormatFloat(bp.fval, 'G', 14, 64))
	case types.TypeFalse:
		return st.bindText(idx, "")
	case types.TypeTrue:
		return st.bindText(idx, "1")
	default:
		return st.bindText(idx, bp.sval)
	}
}

func normalizeParamName(name string) string {
	if strings.HasPrefix(name, ":") || strings.HasPrefix(name, "@") || strings.HasPrefix(name, "$") {
		return name
	}
	return ":" + name
}

func stmtBindValue(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::bindValue", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	pType := int64(PARAM_STR)
	if len(args) == 3 {
		pType, err = asInt(z, args[2])
		if err != nil {
			return types.Value{}, TypeError{"PDOStatement::bindValue(): Argument #3 ($type) must be of type int"}
		}
	}
	bp := snapshotBoundValue(z, args[1], pType)
	param := deref(z, args[0])
	if param.Type == types.TypeLong {
		idx := int(param.ToLong())
		if idx < 1 {
			return types.NewBool(false), nil
		}
		if old, ok := s.byPos[idx]; ok && old.isRef {
			v := old.refVal
			types.ReleaseValue(z, &v)
		}
		s.byPos[idx] = bp
		return types.NewBool(true), nil
	}
	name, err := castString(z, param)
	if err != nil {
		return types.NewBool(false), nil
	}
	key := normalizeParamName(name)
	if old, ok := s.byName[key]; ok && old.isRef {
		v := old.refVal
		types.ReleaseValue(z, &v)
	}
	s.byName[key] = bp
	return types.NewBool(true), nil
}

func stmtBindParam(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::bindParam", len(args), 2, 5); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	pType := int64(PARAM_STR)
	if len(args) >= 3 && !deref(z, args[2]).IsNull() {
		pType, err = asInt(z, args[2])
		if err != nil {
			return types.Value{}, TypeError{"PDOStatement::bindParam(): Argument #3 ($type) must be of type int"}
		}
	}
	var bp boundParam
	if args[1].Type == types.TypeReference {
		bp = boundParam{
			isRef:     true,
			refVal:    types.RetainValue(z, args[1]),
			paramType: pType,
		}
	} else {
		bp = snapshotBoundValue(z, args[1], pType)
	}
	param := deref(z, args[0])
	if param.Type == types.TypeLong {
		idx := int(param.ToLong())
		if idx < 1 {
			if bp.isRef {
				types.ReleaseValue(z, &bp.refVal)
			}
			return types.NewBool(false), nil
		}
		if old, ok := s.byPos[idx]; ok && old.isRef {
			v := old.refVal
			types.ReleaseValue(z, &v)
		}
		s.byPos[idx] = bp
		return types.NewBool(true), nil
	}
	name, err := castString(z, param)
	if err != nil {
		if bp.isRef {
			types.ReleaseValue(z, &bp.refVal)
		}
		return types.NewBool(false), nil
	}
	key := normalizeParamName(name)
	if old, ok := s.byName[key]; ok && old.isRef {
		v := old.refVal
		types.ReleaseValue(z, &v)
	}
	s.byName[key] = bp
	return types.NewBool(true), nil
}

func stmtExecute(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::execute", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	if s.conn == nil || s.conn.closed || s.conn.db == nil {
		return types.NewBool(false), nil
	}
	s.stmt.reset()
	s.stmt.clearBindings()

	if len(args) == 1 && !deref(z, args[0]).IsNull() {
		params := deref(z, args[0])
		if params.Type != types.TypeArray {
			return types.Value{}, TypeError{"PDOStatement::execute(): Argument #1 ($params) must be of type ?array"}
		}
		var bindErr error
		types.ArrayForEach(z, params, func(k, v types.Value) bool {
			bp := snapshotBoundValue(z, v, PARAM_STR)
			if k.Type == types.TypeLong {
				pos := int(k.ToLong())
				if pos >= 0 {
					// En PHP PDO, execute([0 => 'a', 1 => 'b']) lie l'index 0 au paramètre 1.
					target := pos + 1
					if _, hasZero, _ := types.ArrayGet(z, params, types.NewLong(0), warnDrop); !hasZero && pos >= 1 {
						target = pos
					}
					if rc := bindOneToSQLite(z, s.stmt, target, bp); rc != sqliteOK {
						bindErr = s.recordError(rc, "parameter bind failed")
						return false
					}
				}
			} else if k.Type == types.TypeString {
				name := normalizeParamName(types.StringData(z, k))
				idx := s.stmt.paramIndex(name)
				if idx <= 0 {
					bindErr = s.recordError(1, "Invalid parameter number: parameter was not defined")
					return false
				}
				if rc := bindOneToSQLite(z, s.stmt, idx, bp); rc != sqliteOK {
					bindErr = s.recordError(rc, "parameter bind failed")
					return false
				}
			}
			return true
		})
		if bindErr != nil {
			return types.NewBool(false), bindErr
		}
		if s.hasError {
			return types.NewBool(false), nil
		}
	} else {
		for idx, bp := range s.byPos {
			if rc := bindOneToSQLite(z, s.stmt, idx, bp); rc != sqliteOK {
				if err := s.recordError(rc, "parameter bind failed"); err != nil {
					return types.NewBool(false), err
				}
				return types.NewBool(false), nil
			}
		}
		for name, bp := range s.byName {
			idx := s.stmt.paramIndex(name)
			if idx <= 0 {
				if err := s.recordError(1, "Invalid parameter number: parameter was not defined"); err != nil {
					return types.NewBool(false), err
				}
				return types.NewBool(false), nil
			}
			if rc := bindOneToSQLite(z, s.stmt, idx, bp); rc != sqliteOK {
				if err := s.recordError(rc, "parameter bind failed"); err != nil {
					return types.NewBool(false), err
				}
				return types.NewBool(false), nil
			}
		}
	}

	cols, tables, rows, rc := s.stmt.stepAndCollect()
	s.stmt.reset()
	s.conn.inTx = s.conn.db.inTransaction()
	if rc != sqliteDone && rc != sqliteOK {
		code, msg := s.conn.db.errInfo()
		if code == sqliteOK {
			code = rc
		}
		if err := s.recordError(code, msg); err != nil {
			return types.NewBool(false), err
		}
		return types.NewBool(false), nil
	}
	if len(cols) > 0 {
		s.colNames = cols
		s.colTables = tables
	}
	s.rows = rows
	s.cursor = 0
	s.rowCount = s.conn.db.changes()
	s.executed = true
	s.clearError()
	s.conn.clearError()
	return types.NewBool(true), nil
}

func (s *stmtHandle) formatColumnName(idx int) string {
	if idx < 0 || idx >= len(s.colNames) {
		return ""
	}
	name := s.colNames[idx]
	if s.conn != nil {
		switch s.conn.caseMode {
		case CASE_LOWER:
			name = strings.ToLower(name)
		case CASE_UPPER:
			name = strings.ToUpper(name)
		}
		if s.conn.fetchTableNames && idx < len(s.colTables) && s.colTables[idx] != "" {
			name = s.colTables[idx] + "." + name
		}
	}
	return name
}

func (s *stmtHandle) cellToValue(z *zone.Zone, c rawCell) (types.Value, error) {
	stringify := false
	oracleNulls := int64(NULL_NATURAL)
	if s.conn != nil {
		stringify = s.conn.stringifyFetches
		oracleNulls = s.conn.oracleNulls
	}
	switch c.kind {
	case sqliteNull:
		if oracleNulls == NULL_TO_STRING {
			return newStr(z, "")
		}
		return types.NewNull(), nil
	case sqliteInteger:
		if stringify {
			return newStr(z, strconv.FormatInt(c.ival, 10))
		}
		return types.NewLong(c.ival), nil
	case sqliteFloat:
		if stringify {
			return newStr(z, strconv.FormatFloat(c.fval, 'G', 14, 64))
		}
		return types.NewDouble(c.fval), nil
	case sqliteText, sqliteBlob:
		str := string(c.bytes)
		if str == "" && oracleNulls == NULL_EMPTY_STRING {
			return types.NewNull(), nil
		}
		return newStr(z, str)
	default:
		return types.NewNull(), nil
	}
}

func attachDynamicArray(z *zone.Zone, obj types.Value, dynArr types.Value) {
	off, ok := z.ObjectAddress(obj.DataSlot, obj.Extra)
	if !ok {
		return
	}
	raw, err := z.Bytes(off, uint32(unsafe.Sizeof(types.ObjectHeader{})))
	if err != nil {
		return
	}
	h := (*types.ObjectHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
	if h.DynOffset != types.EmptySlot {
		old := types.Value{Type: types.TypeArray, Flags: types.FlagZoneRequest, DataSlot: h.DynOffset}
		types.ReleaseArray(z, &old)
	}
	// Transfert de propriété du tableau dynamique à ObjectHeader.DynOffset.
	h.DynOffset = dynArr.DataSlot
}

// ObjectDynamicArray retourne le tableau des propriétés dynamiques d'un objet (ex: stdClass issu de FETCH_OBJ).
func ObjectDynamicArray(z *zone.Zone, obj types.Value) (types.Value, bool) {
	obj = deref(z, obj)
	if obj.Type != types.TypeObject {
		return types.Value{}, false
	}
	info := types.ObjectInfo(z, obj)
	if info.DynOffset == types.EmptySlot {
		return types.Value{}, false
	}
	return types.Value{Type: types.TypeArray, Flags: types.FlagZoneRequest, DataSlot: info.DynOffset}, true
}

// ObjectGetProperty lit une propriété dynamique par son nom sur un objet retourné par FETCH_OBJ ou fetchObject.
func ObjectGetProperty(z *zone.Zone, obj types.Value, name string) (types.Value, bool) {
	arr, ok := ObjectDynamicArray(z, obj)
	if !ok {
		return types.Value{}, false
	}
	k, err := newStr(z, name)
	if err != nil {
		return types.Value{}, false
	}
	defer types.ReleaseValue(z, &k)
	v, found, err := types.ArrayGet(z, arr, k, warnDrop)
	if err != nil || !found {
		return types.Value{}, false
	}
	return v, true
}

func (s *stmtHandle) formatRow(z *zone.Zone, row []rawCell, mode int64, colArg int64) (types.Value, error) {
	if mode == FETCH_DEFAULT {
		mode = s.fetchMode
		if mode == FETCH_DEFAULT {
			mode = FETCH_BOTH
		}
	}
	baseMode := mode &^ (FETCH_GROUP | FETCH_UNIQUE)
	if baseMode == FETCH_DEFAULT {
		baseMode = FETCH_BOTH
	}
	switch baseMode {
	case FETCH_ASSOC:
		arr, err := types.NewArray(z)
		if err != nil {
			return types.Value{}, err
		}
		for i, cell := range row {
			v, err := s.cellToValue(z, cell)
			if err != nil {
				types.ReleaseValue(z, &arr)
				return types.Value{}, err
			}
			_ = setArrayKey(z, &arr, s.formatColumnName(i), v)
			types.ReleaseValue(z, &v)
		}
		return arr, nil
	case FETCH_NUM:
		arr, err := types.NewArray(z)
		if err != nil {
			return types.Value{}, err
		}
		for _, cell := range row {
			v, err := s.cellToValue(z, cell)
			if err != nil {
				types.ReleaseValue(z, &arr)
				return types.Value{}, err
			}
			_ = types.ArrayAppend(z, &arr, v)
			types.ReleaseValue(z, &v)
		}
		return arr, nil
	case FETCH_BOTH:
		arr, err := types.NewArray(z)
		if err != nil {
			return types.Value{}, err
		}
		for i, cell := range row {
			v, err := s.cellToValue(z, cell)
			if err != nil {
				types.ReleaseValue(z, &arr)
				return types.Value{}, err
			}
			_ = setArrayKey(z, &arr, s.formatColumnName(i), v)
			_ = setArrayIntKey(z, &arr, int64(i), v)
			types.ReleaseValue(z, &v)
		}
		return arr, nil
	case FETCH_COLUMN:
		if colArg < 0 || int(colArg) >= len(row) {
			return types.NewBool(false), ValueError{"PDOStatement::fetch(): Invalid column index"}
		}
		return s.cellToValue(z, row[colArg])
	case FETCH_KEY_PAIR:
		if len(row) != 2 {
			if err := s.recordError(1, "PDO::FETCH_KEY_PAIR fetch mode requires the result set to contain extactly 2 columns."); err != nil {
				return types.NewBool(false), err
			}
			return types.NewBool(false), nil
		}
		arr, err := types.NewArray(z)
		if err != nil {
			return types.Value{}, err
		}
		kVal, err := s.cellToValue(z, row[0])
		if err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
		vVal, err := s.cellToValue(z, row[1])
		if err != nil {
			types.ReleaseValue(z, &kVal)
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
		_ = types.ArraySet(z, &arr, kVal, vVal, warnDrop)
		types.ReleaseValue(z, &kVal)
		types.ReleaseValue(z, &vVal)
		return arr, nil
	case FETCH_OBJ, FETCH_LAZY, FETCH_CLASS, FETCH_INTO:
		assoc, err := s.formatRow(z, row, FETCH_ASSOC, 0)
		if err != nil {
			return types.Value{}, err
		}
		obj, err := types.NewObject(z, ClassStdClass, 0, false)
		if err != nil {
			types.ReleaseValue(z, &assoc)
			return types.Value{}, err
		}
		attachDynamicArray(z, obj, assoc)
		return obj, nil
	case FETCH_BOUND:
		return types.NewBool(true), nil
	default:
		return types.NewBool(false), ValueError{"Invalid fetch mode"}
	}
}

func stmtFetch(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::fetch", len(args), 0, 3); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	mode := int64(FETCH_DEFAULT)
	if len(args) >= 1 && !deref(z, args[0]).IsNull() {
		mode, err = asInt(z, args[0])
		if err != nil {
			return types.Value{}, TypeError{"PDOStatement::fetch(): Argument #1 ($mode) must be of type int"}
		}
	}
	if !s.executed || s.cursor >= len(s.rows) {
		return types.NewBool(false), nil
	}
	row := s.rows[s.cursor]
	s.cursor++
	return s.formatRow(z, row, mode, s.fetchCol)
}

func stmtFetchColumn(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::fetchColumn", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	col := int64(0)
	if len(args) == 1 {
		col, err = asInt(z, args[0])
		if err != nil {
			return types.Value{}, TypeError{"PDOStatement::fetchColumn(): Argument #1 ($column) must be of type int"}
		}
	}
	if !s.executed || s.cursor >= len(s.rows) {
		return types.NewBool(false), nil
	}
	row := s.rows[s.cursor]
	s.cursor++
	if col < 0 || int(col) >= len(row) {
		return types.NewBool(false), ValueError{"Invalid column index"}
	}
	return s.cellToValue(z, row[col])
}

func stmtFetchObject(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::fetchObject", len(args), 0, 2); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	if !s.executed || s.cursor >= len(s.rows) {
		return types.NewBool(false), nil
	}
	row := s.rows[s.cursor]
	s.cursor++
	return s.formatRow(z, row, FETCH_OBJ, 0)
}

func (s *stmtHandle) formatSubRow(z *zone.Zone, row []rawCell, baseMode int64, colArg int64) (types.Value, error) {
	switch baseMode {
	case FETCH_COLUMN:
		targetCol := colArg
		if targetCol == 0 && len(row) > 1 {
			targetCol = 1
		}
		if targetCol < 0 || int(targetCol) >= len(row) {
			return types.NewNull(), nil
		}
		return s.cellToValue(z, row[targetCol])
	case FETCH_ASSOC:
		arr, err := types.NewArray(z)
		if err != nil {
			return types.Value{}, err
		}
		for i := 1; i < len(row); i++ {
			v, err := s.cellToValue(z, row[i])
			if err != nil {
				types.ReleaseValue(z, &arr)
				return types.Value{}, err
			}
			_ = setArrayKey(z, &arr, s.formatColumnName(i), v)
			types.ReleaseValue(z, &v)
		}
		return arr, nil
	case FETCH_NUM:
		arr, err := types.NewArray(z)
		if err != nil {
			return types.Value{}, err
		}
		for i := 1; i < len(row); i++ {
			v, err := s.cellToValue(z, row[i])
			if err != nil {
				types.ReleaseValue(z, &arr)
				return types.Value{}, err
			}
			_ = types.ArrayAppend(z, &arr, v)
			types.ReleaseValue(z, &v)
		}
		return arr, nil
	default:
		arr, err := types.NewArray(z)
		if err != nil {
			return types.Value{}, err
		}
		for i := 1; i < len(row); i++ {
			v, err := s.cellToValue(z, row[i])
			if err != nil {
				types.ReleaseValue(z, &arr)
				return types.Value{}, err
			}
			_ = setArrayKey(z, &arr, s.formatColumnName(i), v)
			_ = setArrayIntKey(z, &arr, int64(i-1), v)
			types.ReleaseValue(z, &v)
		}
		return arr, nil
	}
}

func stmtFetchAll(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::fetchAll", len(args), 0, 3); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	mode := s.fetchMode
	if len(args) >= 1 && !deref(z, args[0]).IsNull() {
		mode, err = asInt(z, args[0])
		if err != nil {
			return types.Value{}, TypeError{"PDOStatement::fetchAll(): Argument #1 ($mode) must be of type int"}
		}
	}
	if mode == FETCH_DEFAULT {
		mode = s.fetchMode
		if mode == FETCH_DEFAULT {
			mode = FETCH_BOTH
		}
	}
	colArg := s.fetchCol
	if len(args) >= 2 && !deref(z, args[1]).IsNull() {
		if c, cErr := asInt(z, args[1]); cErr == nil {
			colArg = c
		}
	}
	result, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	if !s.executed || s.cursor >= len(s.rows) {
		return result, nil
	}
	remaining := s.rows[s.cursor:]
	s.cursor = len(s.rows)

	isUnique := (mode & FETCH_UNIQUE) == FETCH_UNIQUE
	isGroup := (mode&FETCH_GROUP) != 0 && !isUnique
	baseMode := mode &^ (FETCH_GROUP | FETCH_UNIQUE)
	if baseMode == FETCH_DEFAULT {
		baseMode = FETCH_BOTH
	}

	if baseMode == FETCH_KEY_PAIR {
		if len(s.colNames) != 2 {
			types.ReleaseValue(z, &result)
			if err := s.recordError(1, "PDO::FETCH_KEY_PAIR fetch mode requires the result set to contain extactly 2 columns."); err != nil {
				return types.NewBool(false), err
			}
			return types.NewBool(false), nil
		}
		for _, row := range remaining {
			kVal, err := s.cellToValue(z, row[0])
			if err != nil {
				types.ReleaseValue(z, &result)
				return types.Value{}, err
			}
			vVal, err := s.cellToValue(z, row[1])
			if err != nil {
				types.ReleaseValue(z, &kVal)
				types.ReleaseValue(z, &result)
				return types.Value{}, err
			}
			_ = types.ArraySet(z, &result, kVal, vVal, warnDrop)
			types.ReleaseValue(z, &kVal)
			types.ReleaseValue(z, &vVal)
		}
		return result, nil
	}

	if isUnique || isGroup {
		if len(s.colNames) < 2 {
			types.ReleaseValue(z, &result)
			return types.NewBool(false), ValueError{"Invalid column count for FETCH_GROUP/FETCH_UNIQUE"}
		}
		for _, row := range remaining {
			kVal, err := s.cellToValue(z, row[0])
			if err != nil {
				types.ReleaseValue(z, &result)
				return types.Value{}, err
			}
			subVal, err := s.formatSubRow(z, row, baseMode, colArg)
			if err != nil {
				types.ReleaseValue(z, &kVal)
				types.ReleaseValue(z, &result)
				return types.Value{}, err
			}
			if isUnique {
				_ = types.ArraySet(z, &result, kVal, subVal, warnDrop)
			} else {
				existing, found, _ := types.ArrayGet(z, result, kVal, warnDrop)
				var bucket types.Value
				if found && existing.Type == types.TypeArray {
					bucket = types.RetainValue(z, existing)
				} else {
					bucket, err = types.NewArray(z)
					if err != nil {
						types.ReleaseValue(z, &kVal)
						types.ReleaseValue(z, &subVal)
						types.ReleaseValue(z, &result)
						return types.Value{}, err
					}
				}
				_ = types.ArrayAppend(z, &bucket, subVal)
				_ = types.ArraySet(z, &result, kVal, bucket, warnDrop)
				types.ReleaseValue(z, &bucket)
			}
			types.ReleaseValue(z, &kVal)
			types.ReleaseValue(z, &subVal)
		}
		return result, nil
	}

	for _, row := range remaining {
		formatted, err := s.formatRow(z, row, baseMode, colArg)
		if err != nil {
			types.ReleaseValue(z, &result)
			return types.Value{}, err
		}
		_ = types.ArrayAppend(z, &result, formatted)
		types.ReleaseValue(z, &formatted)
	}
	return result, nil
}

func stmtRowCount(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::rowCount", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(s.rowCount), nil
}

func stmtColumnCount(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::columnCount", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(len(s.colNames))), nil
}

func stmtCloseCursor(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::closeCursor", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	s.cursor = len(s.rows)
	if s.stmt != nil {
		s.stmt.reset()
	}
	return types.NewBool(true), nil
}

func stmtErrorCode(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::errorCode", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	if s.sqlState == "" {
		return types.NewNull(), nil
	}
	return newStr(z, s.sqlState)
}

func stmtErrorInfo(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::errorInfo", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	return buildErrorInfoArray(z, s.sqlState, s.driverCode, s.driverMsg, s.hasError)
}

func stmtSetFetchMode(z *zone.Zone, this types.Value, args []types.Value) (types.Value, error) {
	if err := arity("PDOStatement::setFetchMode", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	_, s, err := lookupStmt(z, this)
	if err != nil {
		return types.Value{}, err
	}
	mode, err := asInt(z, args[0])
	if err != nil {
		return types.Value{}, TypeError{"PDOStatement::setFetchMode(): Argument #1 ($mode) must be of type int"}
	}
	s.fetchMode = mode
	if mode == FETCH_COLUMN && len(args) >= 2 {
		col, err := asInt(z, args[1])
		if err != nil || col < 0 {
			return types.Value{}, ValueError{"PDOStatement::setFetchMode(): Invalid column index"}
		}
		s.fetchCol = col
	} else if mode == FETCH_CLASS && len(args) >= 2 {
		cls, err := castString(z, args[1])
		if err == nil && cls != "" {
			s.fetchClass = cls
		}
	}
	return types.NewBool(true), nil
}

// QueryString retourne la propriété publique queryString d'un objet PDOStatement.
func QueryString(z *zone.Zone, stmtObj types.Value) string {
	stmtObj = deref(z, stmtObj)
	if stmtObj.Type != types.TypeObject {
		return ""
	}
	info := types.ObjectInfo(z, stmtObj)
	if info.PropCount < 1 {
		return ""
	}
	q := types.ObjectGet(z, stmtObj, 0)
	if q.Type == types.TypeString {
		return types.StringData(z, q)
	}
	return ""
}

var pdoMethods = map[string]MethodFunc{
	"__construct":      pdoConstruct,
	"begintransaction": pdoBeginTransaction,
	"commit":           pdoCommit,
	"errorcode":        pdoErrorCode,
	"errorinfo":        pdoErrorInfo,
	"exec":             pdoExec,
	"getattribute":     pdoGetAttribute,
	"intransaction":    pdoInTransaction,
	"lastinsertid":     pdoLastInsertId,
	"prepare":          pdoPrepare,
	"query":            pdoQuery,
	"quote":            pdoQuote,
	"rollback":         pdoRollBack,
	"setattribute":     pdoSetAttribute,
}

var stmtMethods = map[string]MethodFunc{
	"bindparam":    stmtBindParam,
	"bindvalue":    stmtBindValue,
	"closecursor":  stmtCloseCursor,
	"columncount":  stmtColumnCount,
	"errorcode":    stmtErrorCode,
	"errorinfo":    stmtErrorInfo,
	"execute":      stmtExecute,
	"fetch":        stmtFetch,
	"fetchall":     stmtFetchAll,
	"fetchcolumn":  stmtFetchColumn,
	"fetchobject":  stmtFetchObject,
	"rowcount":     stmtRowCount,
	"setfetchmode": stmtSetFetchMode,
}

// LookupMethod résout une méthode d'instance sur PDO ou PDOStatement (insensible à la casse ASCII).
func LookupMethod(className, methodName string) (MethodFunc, bool) {
	lower := strings.ToLower(methodName)
	switch strings.ToLower(className) {
	case "pdo":
		fn, ok := pdoMethods[lower]
		return fn, ok
	case "pdostatement":
		fn, ok := stmtMethods[lower]
		return fn, ok
	default:
		return nil, false
	}
}

// LookupStaticMethod résout une méthode statique sur PDO (ex: PDO::getAvailableDrivers).
func LookupStaticMethod(className, methodName string) (stdlib.NativeFunc, bool) {
	if strings.EqualFold(className, "PDO") && strings.EqualFold(methodName, "getAvailableDrivers") {
		return GetAvailableDrivers, true
	}
	return nil, false
}

// CallPDOMethod invoque une méthode d'instance sur un objet PDO.
func CallPDOMethod(z *zone.Zone, this types.Value, method string, args ...types.Value) (types.Value, error) {
	if strings.EqualFold(method, "getAvailableDrivers") {
		return GetAvailableDrivers(z, nil, args)
	}
	fn, ok := LookupMethod("PDO", method)
	if !ok {
		return types.Value{}, Error{"Call to undefined method PDO::" + method + "()"}
	}
	return fn(z, this, args)
}

// CallStmtMethod invoque une méthode d'instance sur un objet PDOStatement.
func CallStmtMethod(z *zone.Zone, this types.Value, method string, args ...types.Value) (types.Value, error) {
	fn, ok := LookupMethod("PDOStatement", method)
	if !ok {
		return types.Value{}, Error{"Call to undefined method PDOStatement::" + method + "()"}
	}
	return fn(z, this, args)
}
