// Package mysqli implémente l'extension mysqli de PHP 8.3 (API procédurale, classes mysqli,
// mysqli_result, mysqli_stmt, mysqli_driver et mysqli_sql_exception, constantes MYSQLI_*).
// Deux moteurs réels sont servis : un client du protocole MySQL (texte et binaire) vers un
// serveur MySQL ou MariaDB, et une base SQLite3 embarquée derrière un traducteur du dialecte
// MySQL utilisé par wpdb. L'état Go des connexions, résultats et requêtes préparées vit dans
// un registre par Zone indexé par handle ; les objets PHP ne portent que l'identifiant scalaire.
// Le registre est libéré à z.Reset par l'élément de nettoyage CleanupMysqli.
package mysqli

import (
	"sync"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Identifiants de nettoyage et de classes.
const (
	CleanupMysqli uint16 = 57
	// CleanupKind est la valeur de zone.CleanupItem.Kind enregistrée par le paquet.
	CleanupKind = zone.CleanupKind(CleanupMysqli)

	ClassIDMysqli             uint32 = 5701
	ClassIDMysqliResult       uint32 = 5702
	ClassIDMysqliStmt         uint32 = 5703
	ClassIDMysqliDriver       uint32 = 5704
	ClassIDMysqliSqlException uint32 = 5705
	// ClassIDStdClass est l'identifiant de stdClass partagé avec stdlib et pdo.
	ClassIDStdClass uint32 = 0x5c1a55
)

// Modes de rapport d'erreurs (MYSQLI_REPORT_*).
const (
	ReportOff    int64 = 0
	ReportError  int64 = 1
	ReportStrict int64 = 2
	ReportIndex  int64 = 4
	ReportAll    int64 = 255
)

// Modes de lecture et options.
const (
	modeAssoc = 1
	modeNum   = 2
	modeBoth  = 3

	optConnectTimeout     = 0
	optReadTimeout        = 11
	optIntAndFloatNative  = 201
	optInitCommand        = 3
	optLocalInfile        = 8
	optSSLVerifyServerCrt = 21
)

// Version du client annoncée par mysqli_get_client_info.
const (
	clientInfo    = "mysqlnd 8.3.0 (php127)"
	clientVersion = 80300
)

func init() {
	EnsureClassNames()
}

// EnsureClassNames enregistre les noms des classes mysqli dans la table du paquet types si elle n'est pas gelée.
func EnsureClassNames() {
	defer func() { _ = recover() }()
	if types.IsProcessZoneFrozen() {
		return
	}
	types.RegisterObjectClassName(ClassIDMysqli, "mysqli")
	types.RegisterObjectClassName(ClassIDMysqliResult, "mysqli_result")
	types.RegisterObjectClassName(ClassIDMysqliStmt, "mysqli_stmt")
	types.RegisterObjectClassName(ClassIDMysqliDriver, "mysqli_driver")
	types.RegisterObjectClassName(ClassIDMysqliSqlException, "mysqli_sql_exception")
	types.RegisterObjectClassName(ClassIDStdClass, "stdClass")
}

// connHandle est l'état Go d'un objet mysqli.
type connHandle struct {
	id        uint32
	be        backend
	connected bool
	closed    bool
	charset   string

	connectTimeout time.Duration
	readTimeout    time.Duration
	nativeTypes    bool
	initCommands   []string

	connectErrno int
	connectError string
	errno        int
	sqlstate     string
	errMsg       string

	affected   int64
	insertID   int64
	warnings   int
	info       string
	fieldCount int

	// File de résultats d'une requête multiple (mysqli_multi_query).
	current    *execResult
	pending    []execResult
	pendingErr *sqlError
}

// resultHandle est l'état Go d'un objet mysqli_result.
type resultHandle struct {
	id          uint32
	rs          *resultSet
	native      bool
	cursor      int
	fieldCursor int
	lastRow     []cell
	freed       bool
}

// stmtHandle est l'état Go d'un objet mysqli_stmt.
type stmtHandle struct {
	id     uint32
	conn   uint32
	sb     stmtBackend
	closed bool

	types      string
	bound      []types.Value
	resultRefs []types.Value

	errno    int
	sqlstate string
	errMsg   string
	affected int64
	insertID int64

	result   *resultSet
	cursor   int
	executed bool
}

// registry regroupe l'état mysqli d'une Zone (une requête PHP).
type registry struct {
	id      uint32
	z       *zone.Zone
	nextID  uint32
	conns   map[uint32]*connHandle
	results map[uint32]*resultHandle
	stmts   map[uint32]*stmtHandle

	reportMode   int64
	connectErrno int
	connectError string
}

var (
	regMu     sync.Mutex
	nextRegID uint32
	regsByZ   = make(map[*zone.Zone]*registry)
	regsByID  = make(map[uint32]*registry)
)

// getRegistry retourne le registre de la Zone et enregistre son nettoyage à la création.
func getRegistry(z *zone.Zone) *registry {
	if z == nil {
		panic("mysqli: nil zone")
	}
	regMu.Lock()
	if r, ok := regsByZ[z]; ok {
		regMu.Unlock()
		return r
	}
	nextRegID++
	r := &registry{
		id:         nextRegID,
		z:          z,
		nextID:     1,
		conns:      make(map[uint32]*connHandle),
		results:    make(map[uint32]*resultHandle),
		stmts:      make(map[uint32]*stmtHandle),
		reportMode: ReportError | ReportStrict,
	}
	regsByZ[z] = r
	regsByID[r.id] = r
	regMu.Unlock()
	z.RegisterCleanup(zone.CleanupItem{Kind: CleanupKind, Target: r.id})
	return r
}

func lookupRegistry(z *zone.Zone) *registry {
	regMu.Lock()
	defer regMu.Unlock()
	return regsByZ[z]
}

func (r *registry) newID() uint32 {
	id := r.nextID
	r.nextID++
	return id
}

// DispatchCleanup ferme les requêtes préparées, résultats et connexions (SQLite et TCP) du registre slot.
func DispatchCleanup(z *zone.Zone, kind uint16, slot uint32) {
	if kind != CleanupMysqli {
		return
	}
	regMu.Lock()
	r := regsByID[slot]
	if r != nil {
		delete(regsByID, slot)
		if regsByZ[r.z] == r {
			delete(regsByZ, r.z)
		}
	}
	regMu.Unlock()
	if r == nil {
		return
	}
	if z == nil {
		z = r.z
	}
	r.closeAll(z)
}

// DispatchZoneCleanup adapte DispatchCleanup à la signature des répartiteurs de nettoyage du dépôt.
func DispatchZoneCleanup(z *zone.Zone, item zone.CleanupItem) {
	DispatchCleanup(z, uint16(item.Kind), item.Target)
}

// CleanupDispatcher retourne la fonction de rappel compatible avec z.Reset pour le paquet mysqli.
func CleanupDispatcher(z *zone.Zone) func(zone.CleanupItem) {
	return func(item zone.CleanupItem) {
		DispatchZoneCleanup(z, item)
	}
}

func (r *registry) closeAll(z *zone.Zone) {
	for id, s := range r.stmts {
		s.release(z)
		delete(r.stmts, id)
	}
	for id := range r.results {
		delete(r.results, id)
	}
	for id, c := range r.conns {
		c.shutdown()
		delete(r.conns, id)
	}
}

func (s *stmtHandle) release(z *zone.Zone) {
	if s.sb != nil {
		s.sb.close()
		s.sb = nil
	}
	if z != nil {
		for i := range s.bound {
			types.ReleaseValue(z, &s.bound[i])
		}
		for i := range s.resultRefs {
			types.ReleaseValue(z, &s.resultRefs[i])
		}
	}
	s.bound = nil
	s.resultRefs = nil
	s.result = nil
	s.closed = true
}

func (c *connHandle) shutdown() {
	if c.be != nil {
		c.be.close()
		c.be = nil
	}
	c.connected = false
	c.closed = true
	c.current = nil
	c.pending = nil
	c.pendingErr = nil
}

// OpenConnCount retourne le nombre de connexions ouvertes dans la Zone.
func OpenConnCount(z *zone.Zone) int {
	r := lookupRegistry(z)
	if r == nil {
		return 0
	}
	n := 0
	for _, c := range r.conns {
		if c.be != nil {
			n++
		}
	}
	return n
}

// OpenStmtCount retourne le nombre de requêtes préparées ouvertes dans la Zone.
func OpenStmtCount(z *zone.Zone) int {
	r := lookupRegistry(z)
	if r == nil {
		return 0
	}
	n := 0
	for _, s := range r.stmts {
		if !s.closed {
			n++
		}
	}
	return n
}

// OpenSQLiteHandles retourne le nombre de bases SQLite ouvertes par le paquet dans tout le processus.
func OpenSQLiteHandles() int {
	sqlConnMu.Lock()
	defer sqlConnMu.Unlock()
	return len(sqlConns)
}

// Constants retourne la table des constantes MYSQLI_*.
func Constants() map[string]types.Value {
	return constTable
}

// LookupConstant recherche une constante MYSQLI_* par son nom exact.
func LookupConstant(name string) (types.Value, bool) {
	v, ok := constTable[name]
	return v, ok
}

var constTable = func() map[string]types.Value {
	ints := map[string]int64{
		"MYSQLI_REPORT_OFF": ReportOff, "MYSQLI_REPORT_ERROR": ReportError, "MYSQLI_REPORT_STRICT": ReportStrict,
		"MYSQLI_REPORT_INDEX": ReportIndex, "MYSQLI_REPORT_ALL": ReportAll,
		"MYSQLI_ASSOC": modeAssoc, "MYSQLI_NUM": modeNum, "MYSQLI_BOTH": modeBoth,
		"MYSQLI_STORE_RESULT": 0, "MYSQLI_USE_RESULT": 1, "MYSQLI_ASYNC": 8, "MYSQLI_STORE_RESULT_COPY_DATA": 16,
		"MYSQLI_CLIENT_COMPRESS": 32, "MYSQLI_CLIENT_FOUND_ROWS": capFoundRows, "MYSQLI_CLIENT_IGNORE_SPACE": capIgnoreSpace,
		"MYSQLI_CLIENT_INTERACTIVE": capInteractive, "MYSQLI_CLIENT_SSL": 2048, "MYSQLI_CLIENT_NO_SCHEMA": 16,
		"MYSQLI_CLIENT_SSL_DONT_VERIFY_SERVER_CERT": 64, "MYSQLI_CLIENT_SSL_VERIFY_SERVER_CERT": 1073741824,
		"MYSQLI_CLIENT_CAN_HANDLE_EXPIRED_PASSWORDS": 4194304,
		"MYSQLI_OPT_CONNECT_TIMEOUT":                 optConnectTimeout, "MYSQLI_OPT_READ_TIMEOUT": optReadTimeout,
		"MYSQLI_OPT_INT_AND_FLOAT_NATIVE": optIntAndFloatNative, "MYSQLI_INIT_COMMAND": optInitCommand,
		"MYSQLI_OPT_LOCAL_INFILE": optLocalInfile, "MYSQLI_OPT_SSL_VERIFY_SERVER_CERT": optSSLVerifyServerCrt,
		"MYSQLI_OPT_NET_CMD_BUFFER_SIZE": 202, "MYSQLI_OPT_NET_READ_BUFFER_SIZE": 203,
		"MYSQLI_READ_DEFAULT_FILE": 4, "MYSQLI_READ_DEFAULT_GROUP": 5, "MYSQLI_SERVER_PUBLIC_KEY": 35,
		"MYSQLI_NOT_NULL_FLAG": flagNotNull, "MYSQLI_PRI_KEY_FLAG": flagPriKey, "MYSQLI_UNIQUE_KEY_FLAG": flagUniqueKey,
		"MYSQLI_MULTIPLE_KEY_FLAG": flagMultipleKey, "MYSQLI_BLOB_FLAG": flagBlob, "MYSQLI_UNSIGNED_FLAG": flagUnsigned,
		"MYSQLI_ZEROFILL_FLAG": flagZerofill, "MYSQLI_BINARY_FLAG": flagBinary, "MYSQLI_ENUM_FLAG": flagEnum,
		"MYSQLI_AUTO_INCREMENT_FLAG": flagAutoIncrement, "MYSQLI_TIMESTAMP_FLAG": flagTimestamp, "MYSQLI_SET_FLAG": flagSet,
		"MYSQLI_NUM_FLAG": flagNum, "MYSQLI_PART_KEY_FLAG": flagPartKey, "MYSQLI_GROUP_FLAG": 32768,
		"MYSQLI_NO_DEFAULT_VALUE_FLAG": 4096, "MYSQLI_ON_UPDATE_NOW_FLAG": 8192,
		"MYSQLI_TYPE_DECIMAL": typeDecimal, "MYSQLI_TYPE_TINY": typeTiny, "MYSQLI_TYPE_CHAR": typeTiny,
		"MYSQLI_TYPE_SHORT": typeShort, "MYSQLI_TYPE_LONG": typeLong, "MYSQLI_TYPE_FLOAT": typeFloat,
		"MYSQLI_TYPE_DOUBLE": typeDouble, "MYSQLI_TYPE_NULL": typeNull, "MYSQLI_TYPE_TIMESTAMP": typeTimestamp,
		"MYSQLI_TYPE_LONGLONG": typeLongLong, "MYSQLI_TYPE_INT24": typeInt24, "MYSQLI_TYPE_DATE": typeDate,
		"MYSQLI_TYPE_TIME": typeTime, "MYSQLI_TYPE_DATETIME": typeDateTime, "MYSQLI_TYPE_YEAR": typeYear,
		"MYSQLI_TYPE_NEWDATE": typeNewDate, "MYSQLI_TYPE_VARCHAR": typeVarchar, "MYSQLI_TYPE_BIT": typeBit,
		"MYSQLI_TYPE_JSON": typeJSON, "MYSQLI_TYPE_NEWDECIMAL": typeNewDecimal, "MYSQLI_TYPE_ENUM": typeEnum,
		"MYSQLI_TYPE_SET": typeSet, "MYSQLI_TYPE_TINY_BLOB": typeTinyBlob, "MYSQLI_TYPE_MEDIUM_BLOB": typeMediumBlob,
		"MYSQLI_TYPE_LONG_BLOB": typeLongBlob, "MYSQLI_TYPE_BLOB": typeBlob, "MYSQLI_TYPE_VAR_STRING": typeVarString,
		"MYSQLI_TYPE_STRING": typeString, "MYSQLI_TYPE_GEOMETRY": typeGeometry, "MYSQLI_TYPE_INTERVAL": typeEnum,
		"MYSQLI_NO_DATA": 100, "MYSQLI_DATA_TRUNCATED": 101,
		"MYSQLI_TRANS_START_READ_ONLY": 4, "MYSQLI_TRANS_START_READ_WRITE": 2, "MYSQLI_TRANS_START_WITH_CONSISTENT_SNAPSHOT": 1,
		"MYSQLI_TRANS_COR_AND_CHAIN": 1, "MYSQLI_TRANS_COR_AND_NO_CHAIN": 2, "MYSQLI_TRANS_COR_RELEASE": 4, "MYSQLI_TRANS_COR_NO_RELEASE": 8,
		"MYSQLI_STMT_ATTR_UPDATE_MAX_LENGTH": 0, "MYSQLI_STMT_ATTR_CURSOR_TYPE": 1, "MYSQLI_STMT_ATTR_PREFETCH_ROWS": 2,
		"MYSQLI_CURSOR_TYPE_NO_CURSOR": 0, "MYSQLI_CURSOR_TYPE_READ_ONLY": 1,
		"MYSQLI_REFRESH_GRANT": 1, "MYSQLI_REFRESH_LOG": 2, "MYSQLI_REFRESH_TABLES": 4, "MYSQLI_REFRESH_HOSTS": 8,
		"MYSQLI_REFRESH_STATUS": 16, "MYSQLI_REFRESH_THREADS": 32, "MYSQLI_REFRESH_REPLICA": 64, "MYSQLI_REFRESH_SLAVE": 64,
		"MYSQLI_REFRESH_MASTER": 128, "MYSQLI_REFRESH_BACKUP_LOG": 2097152,
		"MYSQLI_SET_CHARSET_DIR": 6, "MYSQLI_SET_CHARSET_NAME": 7, "MYSQLI_DEBUG_TRACE_ENABLED": 0,
		"MYSQLI_SERVER_QUERY_NO_GOOD_INDEX_USED": 16, "MYSQLI_SERVER_QUERY_NO_INDEX_USED": 32,
		"MYSQLI_SERVER_QUERY_WAS_SLOW": 2048, "MYSQLI_SERVER_PS_OUT_PARAMS": 4096,
		"MYSQLI_IS_MARIADB": 0,
	}
	out := make(map[string]types.Value, len(ints))
	for k, v := range ints {
		out[k] = types.NewLong(v)
	}
	out["MYSQLI_IS_MARIADB"] = types.NewBool(false)
	return out
}()
