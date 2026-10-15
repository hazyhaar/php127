package mysqli

import (
	"strconv"
	"strings"
)

// Nature d'une cellule de résultat ou d'un paramètre lié.
const (
	cellNull uint8 = iota
	cellText
	cellInt
	cellUint
	cellFloat
	cellBlob
)

// cell porte une valeur SQL côté Go, hors de la Zone.
type cell struct {
	kind uint8
	s    string
	i    int64
	f    float64
}

// text retourne la représentation du protocole texte MySQL d'une cellule non NULL.
func (c cell) text() string {
	switch c.kind {
	case cellInt:
		return strconv.FormatInt(c.i, 10)
	case cellUint:
		return strconv.FormatUint(uint64(c.i), 10)
	case cellFloat:
		return formatMySQLFloat(c.f)
	default:
		return c.s
	}
}

// formatMySQLFloat reproduit l'écriture d'un DOUBLE par le serveur MySQL (1e20, 0.1, 3).
func formatMySQLFloat(f float64) string {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if before, after, ok := strings.Cut(s, "e"); ok {
		mant, exp := before, after
		neg := strings.HasPrefix(exp, "-")
		exp = strings.TrimLeft(strings.TrimPrefix(strings.TrimPrefix(exp, "+"), "-"), "0")
		if exp == "" {
			exp = "0"
		}
		if neg {
			exp = "-" + exp
		}
		return mant + "e" + exp
	}
	return s
}

// field décrit une colonne de résultat au format de mysqli_fetch_field.
type field struct {
	name      string
	orgname   string
	table     string
	orgtable  string
	db        string
	length    uint32
	charsetnr uint16
	flags     uint16
	typ       uint8
	decimals  uint8
}

// resultSet est un jeu de lignes entièrement lu (mode MYSQLI_STORE_RESULT).
type resultSet struct {
	fields []field
	rows   [][]cell
	// binary indique un résultat du protocole binaire (requête préparée) : entiers et flottants natifs.
	binary bool
}

// execResult est le compte rendu d'une instruction exécutée.
type execResult struct {
	rs       *resultSet
	affected int64
	insertID int64
	warnings int
	info     string
}

// sqlError porte un code d'erreur client ou serveur MySQL.
type sqlError struct {
	errno    int
	sqlstate string
	msg      string
}

func (e *sqlError) Error() string { return e.msg }

func newSQLError(errno int, sqlstate, msg string) *sqlError {
	if sqlstate == "" {
		sqlstate = "HY000"
	}
	return &sqlError{errno: errno, sqlstate: sqlstate, msg: msg}
}

// backend est un moteur d'exécution : SQLite embarqué ou serveur MySQL TCP.
type backend interface {
	// query exécute une instruction (multi = false) ou une suite d'instructions séparées par « ; ».
	// Les résultats obtenus avant une erreur sont retournés avec cette erreur.
	query(sql string, multi bool) ([]execResult, *sqlError)
	prepare(sql string) (stmtBackend, *sqlError)
	selectDB(db string) *sqlError
	setCharset(cs string) *sqlError
	ping() bool
	close()
	serverInfo() string
	serverVersion() int
	hostInfo() string
	protocolVersion() int
	threadID() int64
	stat() string
}

// stmtBackend est une requête préparée côté moteur.
type stmtBackend interface {
	paramCount() int
	fields() []field
	execute(params []cell) (execResult, *sqlError)
	close()
}

// Codes d'erreur client (libmysqlclient / mysqlnd).
const (
	crConnectionError  = 2002
	crConnHostError    = 2003
	crUnknownHost      = 2005
	crServerGone       = 2006
	crServerLost       = 2013
	crCommandsOutSync  = 2014
	crParamsNotBound   = 2031
	crNoPrepareStmt    = 2030
	crInvalidParameter = 2034
)

// Codes de type MySQL (MYSQLI_TYPE_*).
const (
	typeDecimal    = 0
	typeTiny       = 1
	typeShort      = 2
	typeLong       = 3
	typeFloat      = 4
	typeDouble     = 5
	typeNull       = 6
	typeTimestamp  = 7
	typeLongLong   = 8
	typeInt24      = 9
	typeDate       = 10
	typeTime       = 11
	typeDateTime   = 12
	typeYear       = 13
	typeNewDate    = 14
	typeVarchar    = 15
	typeBit        = 16
	typeJSON       = 245
	typeNewDecimal = 246
	typeEnum       = 247
	typeSet        = 248
	typeTinyBlob   = 249
	typeMediumBlob = 250
	typeLongBlob   = 251
	typeBlob       = 252
	typeVarString  = 253
	typeString     = 254
	typeGeometry   = 255
)

// Indicateurs de colonne (MYSQLI_*_FLAG).
const (
	flagNotNull       = 1
	flagPriKey        = 2
	flagUniqueKey     = 4
	flagMultipleKey   = 8
	flagBlob          = 16
	flagUnsigned      = 32
	flagZerofill      = 64
	flagBinary        = 128
	flagEnum          = 256
	flagAutoIncrement = 512
	flagTimestamp     = 1024
	flagSet           = 2048
	flagNum           = 32768
	flagPartKey       = 16384
)

// Jeux de caractères MySQL usuels (charsetnr).
const (
	charsetUTF8MB4 = 255
	charsetUTF8    = 33
	charsetBinary  = 63
	charsetLatin1  = 8
)

func isNumericType(t uint8) bool {
	switch t {
	case typeDecimal, typeTiny, typeShort, typeLong, typeFloat, typeDouble,
		typeLongLong, typeInt24, typeYear, typeNewDecimal:
		return true
	}
	return false
}
