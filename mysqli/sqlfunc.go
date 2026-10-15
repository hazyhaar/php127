package mysqli

/*
#include <sqlite3.h>
#include <stdint.h>
*/
import "C"

import (
	"crypto/md5"
	"encoding/hex"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Identifiants des fonctions SQL MySQL servies par Go dans le moteur SQLite.
const (
	fnRegexp uintptr = iota + 1
	fnSubstringIndex
	fnFindInSet
	fnLeft
	fnRight
	fnLower
	fnUpper
	fnMD5
	fnUnixTimestamp
	fnFromUnixtime
	fnDateFormat
	fnYear
	fnMonth
	fnDay
	fnHour
	fnMinute
	fnSecond
	fnField
	fnLocate
	fnGreatest
	fnLeast
	fnFoundRows
	fnLastInsertID
	fnDatabase
	fnVersion
	fnCharLength
	fnRand
	fnIsNull
	fnConnectionID
)

type sqlFuncDef struct {
	name          string
	id            uintptr
	nargs         int
	deterministic bool
}

var sqlFuncDefs = []sqlFuncDef{
	{"regexp", fnRegexp, 2, true},
	{"substring_index", fnSubstringIndex, 3, true},
	{"find_in_set", fnFindInSet, 2, true},
	{"left", fnLeft, 2, true},
	{"right", fnRight, 2, true},
	{"lcase", fnLower, 1, true},
	{"ucase", fnUpper, 1, true},
	{"md5", fnMD5, 1, true},
	{"unix_timestamp", fnUnixTimestamp, -1, false},
	{"from_unixtime", fnFromUnixtime, -1, true},
	{"date_format", fnDateFormat, 2, true},
	{"year", fnYear, 1, true},
	{"month", fnMonth, 1, true},
	{"dayofmonth", fnDay, 1, true},
	{"day", fnDay, 1, true},
	{"hour", fnHour, 1, true},
	{"minute", fnMinute, 1, true},
	{"second", fnSecond, 1, true},
	{"field", fnField, -1, true},
	{"locate", fnLocate, -1, true},
	{"greatest", fnGreatest, -1, true},
	{"least", fnLeast, -1, true},
	{"found_rows", fnFoundRows, 0, false},
	{"last_insert_id", fnLastInsertID, -1, false},
	{"database", fnDatabase, 0, false},
	{"schema", fnDatabase, 0, false},
	{"version", fnVersion, 0, true},
	{"char_length", fnCharLength, 1, true},
	{"character_length", fnCharLength, 1, true},
	{"rand", fnRand, -1, false},
	{"isnull", fnIsNull, 1, true},
	{"connection_id", fnConnectionID, 0, false},
}

// Registre des connexions SQLite consultées par FOUND_ROWS(), LAST_INSERT_ID() et DATABASE().
var (
	sqlConnMu sync.Mutex
	sqlConns  = make(map[uint32]*sqliteBackend)
	sqlConnID uint32
)

func registerSQLConn(b *sqliteBackend) uint32 {
	sqlConnMu.Lock()
	defer sqlConnMu.Unlock()
	sqlConnID++
	sqlConns[sqlConnID] = b
	return sqlConnID
}

func unregisterSQLConn(id uint32) {
	sqlConnMu.Lock()
	delete(sqlConns, id)
	sqlConnMu.Unlock()
}

func lookupSQLConn(id uint32) *sqliteBackend {
	sqlConnMu.Lock()
	defer sqlConnMu.Unlock()
	return sqlConns[id]
}

var (
	regexpMu    sync.Mutex
	regexpCache = make(map[string]*regexp.Regexp)
)

func compileMySQLRegexp(pattern string) (*regexp.Regexp, error) {
	regexpMu.Lock()
	defer regexpMu.Unlock()
	if re, ok := regexpCache[pattern]; ok {
		return re, nil
	}
	// La collation par défaut de MySQL est insensible à la casse : REGEXP l'est aussi.
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, err
	}
	if len(regexpCache) > 256 {
		clear(regexpCache)
	}
	regexpCache[pattern] = re
	return re, nil
}

// mysqliGoTableLock rend la décision de l'autorisateur de LOCK TABLES pour la connexion key : SQLITE_OK si
// l'accès kind (lockAccessRead, lockAccessWrite, lockAccessSchema) à table est admis, SQLITE_DENY sinon.
//
//export mysqliGoTableLock
func mysqliGoTableLock(key C.uintptr_t, table *C.char, kind C.int) C.int {
	b := lookupSQLConn(uint32(key))
	if b == nil || b.allowLockedAccess(C.GoString(table), int(kind)) {
		return C.SQLITE_OK
	}
	return C.SQLITE_DENY
}

//export mysqliGoSQLFunc
func mysqliGoSQLFunc(ctx *C.sqlite3_context, argc C.int, argv **C.sqlite3_value) {
	key := sqlFuncKey(ctx)
	id := key & 0xff
	connID := uint32(key >> 8)
	args := make([]cell, int(argc))
	for i := range args {
		args[i] = sqlFuncArg(argv, i)
	}
	res, errMsg := evalSQLFunc(id, connID, args)
	if errMsg != "" {
		sqlFuncError(ctx, errMsg)
		return
	}
	sqlFuncResult(ctx, res)
}

func anyNull(args []cell) bool {
	for _, a := range args {
		if a.kind == cellNull {
			return true
		}
	}
	return false
}

func cellString(c cell) string {
	if c.kind == cellNull {
		return ""
	}
	return c.text()
}

func cellInt64(c cell) int64 {
	switch c.kind {
	case cellInt, cellUint:
		return c.i
	case cellFloat:
		return int64(c.f)
	case cellText, cellBlob:
		s := strings.TrimSpace(c.s)
		end := 0
		for end < len(s) && (s[end] == '-' || s[end] == '+' || (s[end] >= '0' && s[end] <= '9')) {
			end++
		}
		n, _ := strconv.ParseInt(s[:end], 10, 64)
		return n
	}
	return 0
}

func cellFloat64(c cell) float64 {
	switch c.kind {
	case cellInt, cellUint:
		return float64(c.i)
	case cellFloat:
		return c.f
	case cellText, cellBlob:
		f, _ := strconv.ParseFloat(strings.TrimSpace(c.s), 64)
		return f
	}
	return 0
}

func textCell(s string) cell { return cell{kind: cellText, s: s} }
func intCell(n int64) cell   { return cell{kind: cellInt, i: n} }

var nullCell = cell{kind: cellNull}

// parseMySQLTime lit une valeur DATE, DATETIME ou un horodatage numérique.
func parseMySQLTime(c cell) (time.Time, bool) {
	if c.kind == cellNull {
		return time.Time{}, false
	}
	s := strings.TrimSpace(cellString(c))
	for _, layout := range []string{"2006-01-02 15:04:05.999999", "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02", "15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func evalSQLFunc(id uintptr, connID uint32, args []cell) (cell, string) {
	switch id {
	case fnRegexp:
		if anyNull(args) {
			return nullCell, ""
		}
		re, err := compileMySQLRegexp(cellString(args[0]))
		if err != nil {
			return nullCell, "Got error '" + err.Error() + "' from regexp"
		}
		if re.MatchString(cellString(args[1])) {
			return intCell(1), ""
		}
		return intCell(0), ""
	case fnSubstringIndex:
		if anyNull(args) {
			return nullCell, ""
		}
		return textCell(substringIndex(cellString(args[0]), cellString(args[1]), cellInt64(args[2]))), ""
	case fnFindInSet:
		if anyNull(args) {
			return nullCell, ""
		}
		needle := strings.ToLower(cellString(args[0]))
		list := cellString(args[1])
		if list == "" {
			return intCell(0), ""
		}
		for i, item := range strings.Split(list, ",") {
			if strings.ToLower(item) == needle {
				return intCell(int64(i + 1)), ""
			}
		}
		return intCell(0), ""
	case fnLeft, fnRight:
		if anyNull(args) {
			return nullCell, ""
		}
		r := []rune(cellString(args[0]))
		n := max(cellInt64(args[1]), 0)
		if n > int64(len(r)) {
			n = int64(len(r))
		}
		if id == fnLeft {
			return textCell(string(r[:n])), ""
		}
		return textCell(string(r[int64(len(r))-n:])), ""
	case fnLower:
		if anyNull(args) {
			return nullCell, ""
		}
		return textCell(strings.ToLower(cellString(args[0]))), ""
	case fnUpper:
		if anyNull(args) {
			return nullCell, ""
		}
		return textCell(strings.ToUpper(cellString(args[0]))), ""
	case fnMD5:
		if anyNull(args) {
			return nullCell, ""
		}
		sum := md5.Sum([]byte(cellString(args[0])))
		return textCell(hex.EncodeToString(sum[:])), ""
	case fnUnixTimestamp:
		if len(args) == 0 {
			return intCell(time.Now().Unix()), ""
		}
		if args[0].kind == cellNull {
			return nullCell, ""
		}
		t, ok := parseMySQLTime(args[0])
		if !ok {
			return intCell(0), ""
		}
		return intCell(t.Unix()), ""
	case fnFromUnixtime:
		if len(args) == 0 || anyNull(args) {
			return nullCell, ""
		}
		t := time.Unix(cellInt64(args[0]), 0).UTC()
		if len(args) >= 2 {
			return textCell(mysqlDateFormat(t, cellString(args[1]))), ""
		}
		return textCell(t.Format("2006-01-02 15:04:05")), ""
	case fnDateFormat:
		if anyNull(args) {
			return nullCell, ""
		}
		t, ok := parseMySQLTime(args[0])
		if !ok {
			return nullCell, ""
		}
		return textCell(mysqlDateFormat(t, cellString(args[1]))), ""
	case fnYear, fnMonth, fnDay, fnHour, fnMinute, fnSecond:
		t, ok := parseMySQLTime(args[0])
		if !ok {
			return nullCell, ""
		}
		switch id {
		case fnYear:
			return intCell(int64(t.Year())), ""
		case fnMonth:
			return intCell(int64(t.Month())), ""
		case fnDay:
			return intCell(int64(t.Day())), ""
		case fnHour:
			return intCell(int64(t.Hour())), ""
		case fnMinute:
			return intCell(int64(t.Minute())), ""
		default:
			return intCell(int64(t.Second())), ""
		}
	case fnField:
		if len(args) < 2 || args[0].kind == cellNull {
			return intCell(0), ""
		}
		needle := strings.ToLower(cellString(args[0]))
		for i, a := range args[1:] {
			if a.kind != cellNull && strings.ToLower(cellString(a)) == needle {
				return intCell(int64(i + 1)), ""
			}
		}
		return intCell(0), ""
	case fnLocate:
		if len(args) < 2 || anyNull(args) {
			return nullCell, ""
		}
		sub := []rune(strings.ToLower(cellString(args[0])))
		str := []rune(strings.ToLower(cellString(args[1])))
		start := int64(1)
		if len(args) >= 3 {
			start = cellInt64(args[2])
		}
		if start < 1 || start > int64(len(str))+1 {
			return intCell(0), ""
		}
		idx := strings.Index(string(str[start-1:]), string(sub))
		if idx < 0 {
			return intCell(0), ""
		}
		return intCell(start + int64(utf8.RuneCountInString(string(str[start-1:])[:idx]))), ""
	case fnGreatest, fnLeast:
		if len(args) == 0 || anyNull(args) {
			return nullCell, ""
		}
		best := args[0]
		for _, a := range args[1:] {
			if (id == fnGreatest && compareCells(a, best) > 0) || (id == fnLeast && compareCells(a, best) < 0) {
				best = a
			}
		}
		return best, ""
	case fnCharLength:
		if anyNull(args) {
			return nullCell, ""
		}
		return intCell(int64(utf8.RuneCountInString(cellString(args[0])))), ""
	case fnRand:
		return cell{kind: cellFloat, f: rand.Float64()}, ""
	case fnIsNull:
		if args[0].kind == cellNull {
			return intCell(1), ""
		}
		return intCell(0), ""
	case fnVersion:
		return textCell(sqliteServerVersion), ""
	}
	b := lookupSQLConn(connID)
	if b == nil {
		return nullCell, "mysqli: connection is closed"
	}
	switch id {
	case fnFoundRows:
		return intCell(b.foundRows), ""
	case fnLastInsertID:
		if len(args) == 1 {
			b.lastInsertID = cellInt64(args[0])
			return args[0], ""
		}
		return intCell(b.lastInsertID), ""
	case fnDatabase:
		if b.dbName == "" {
			return nullCell, ""
		}
		return textCell(b.dbName), ""
	case fnConnectionID:
		return intCell(int64(b.sqlID)), ""
	}
	return nullCell, "mysqli: unknown SQL function"
}

func compareCells(a, b cell) int {
	numeric := func(c cell) bool { return c.kind == cellInt || c.kind == cellUint || c.kind == cellFloat }
	if numeric(a) && numeric(b) {
		fa, fb := cellFloat64(a), cellFloat64(b)
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}
		return 0
	}
	return strings.Compare(strings.ToLower(cellString(a)), strings.ToLower(cellString(b)))
}

// substringIndex reproduit SUBSTRING_INDEX(str, delim, count) de MySQL.
func substringIndex(s, delim string, count int64) string {
	if delim == "" || count == 0 {
		return ""
	}
	if count > 0 {
		pos := 0
		for i := int64(0); i < count; i++ {
			j := strings.Index(s[pos:], delim)
			if j < 0 {
				return s
			}
			pos += j + len(delim)
		}
		return s[:pos-len(delim)]
	}
	end := len(s)
	for i := int64(0); i < -count; i++ {
		j := strings.LastIndex(s[:end], delim)
		if j < 0 {
			return s
		}
		end = j
	}
	return s[end+len(delim):]
}

// mysqlDateFormat applique les spécificateurs de DATE_FORMAT de MySQL.
func mysqlDateFormat(t time.Time, format string) string {
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' || i+1 >= len(format) {
			b.WriteByte(c)
			continue
		}
		i++
		switch format[i] {
		case 'Y':
			b.WriteString(t.Format("2006"))
		case 'y':
			b.WriteString(t.Format("06"))
		case 'm':
			b.WriteString(t.Format("01"))
		case 'c':
			b.WriteString(strconv.Itoa(int(t.Month())))
		case 'd':
			b.WriteString(t.Format("02"))
		case 'e':
			b.WriteString(strconv.Itoa(t.Day()))
		case 'H':
			b.WriteString(t.Format("15"))
		case 'k':
			b.WriteString(strconv.Itoa(t.Hour()))
		case 'h', 'I':
			b.WriteString(t.Format("03"))
		case 'l':
			b.WriteString(t.Format("3"))
		case 'i':
			b.WriteString(t.Format("04"))
		case 's', 'S':
			b.WriteString(t.Format("05"))
		case 'f':
			b.WriteString(t.Format("000000"))
		case 'p':
			b.WriteString(t.Format("PM"))
		case 'M':
			b.WriteString(t.Format("January"))
		case 'b':
			b.WriteString(t.Format("Jan"))
		case 'W':
			b.WriteString(t.Format("Monday"))
		case 'a':
			b.WriteString(t.Format("Mon"))
		case 'j':
			b.WriteString(t.Format("002"))
		case 'w':
			b.WriteString(strconv.Itoa(int(t.Weekday())))
		case 'T':
			b.WriteString(t.Format("15:04:05"))
		case 'r':
			b.WriteString(t.Format("03:04:05 PM"))
		default:
			b.WriteByte(format[i])
		}
	}
	return b.String()
}
