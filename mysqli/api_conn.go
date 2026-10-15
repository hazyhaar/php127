package mysqli

import (
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Variables d'environnement désignant la base SQLite de repli quand aucun serveur MySQL ne répond.
const (
	EnvSQLitePath = "PHP127_MYSQLI_SQLITE_PATH"
	EnvWPSQLite   = "PHP127_WP_SQLITE"
)

const defaultSocket = "/var/run/mysqld/mysqld.sock"

func fallbackSQLitePath() (string, bool) {
	for _, k := range []string{EnvSQLitePath, EnvWPSQLite} {
		if v := os.Getenv(k); v != "" {
			return v, true
		}
	}
	return "", false
}

// sqliteTarget reconnaît un hôte désignant une base SQLite : « sqlite:chemin », « :memory: », fichier .db/.sqlite ou chemin local.
func sqliteTarget(host string) (string, bool) {
	lh := strings.ToLower(host)
	switch {
	case strings.HasPrefix(lh, "sqlite:"):
		if rest := host[len("sqlite:"):]; rest != "" {
			return rest, true
		}
		return ":memory:", true
	case host == ":memory:":
		return host, true
	case strings.HasSuffix(lh, ".db") || strings.HasSuffix(lh, ".sqlite") || strings.HasSuffix(lh, ".sqlite3"):
		return host, true
	case strings.HasPrefix(host, "/") || strings.HasPrefix(host, "./") || strings.HasPrefix(lh, "file:"):
		return host, true
	}
	return "", false
}

// sqliteHostSocket reconnaît une base SQLite portée par le couple hôte et socket, tel que le produit
// le découpage de DB_HOST par WordPress : « sqlite:/chemin » donne l'hôte sqlite et le socket /chemin,
// « localhost:/chemin.sqlite » donne le socket /chemin.sqlite. Un socket UNIX sans suffixe de base
// SQLite reste un socket MySQL.
func sqliteHostSocket(host, socket string) (string, bool) {
	if strings.EqualFold(host, "sqlite") {
		if socket == "" {
			return ":memory:", true
		}
		return socket, true
	}
	if socket == "" {
		return "", false
	}
	ls := strings.ToLower(socket)
	switch {
	case strings.HasPrefix(ls, "sqlite:"), socket == ":memory:",
		strings.HasSuffix(ls, ".db"), strings.HasSuffix(ls, ".sqlite"), strings.HasSuffix(ls, ".sqlite3"):
		return sqliteTarget(socket)
	}
	return "", false
}

func (r *registry) newConn() *connHandle {
	c := &connHandle{id: r.newID(), connectTimeout: 10 * time.Second, charset: "utf8mb4", sqlstate: "00000"}
	r.conns[c.id] = c
	return c
}

func (r *registry) report(out io.Writer, fn string, e *sqlError) error {
	if r.reportMode&ReportError == 0 {
		return nil
	}
	if r.reportMode&ReportStrict != 0 {
		return SQLException{Code: e.errno, SQLState: e.sqlstate, Message: e.msg}
	}
	warn(out, fn, "("+e.sqlstate+"/"+strconv.Itoa(e.errno)+"): "+e.msg)
	return nil
}

func (c *connHandle) setError(e *sqlError) {
	c.errno, c.sqlstate, c.errMsg = e.errno, e.sqlstate, e.msg
}

func (c *connHandle) clearError() {
	c.errno, c.sqlstate, c.errMsg = 0, "00000", ""
}

func (r *registry) failConn(out io.Writer, fn string, c *connHandle, e *sqlError) error {
	c.setError(e)
	c.affected = -1
	return r.report(out, fn, e)
}

// connect établit la session : base SQLite désignée, sinon serveur MySQL (socket UNIX ou TCP), puis repli SQLite configuré.
func (r *registry) connect(out io.Writer, fn string, c *connHandle, host, user, pass, db string, port int64, socket string, flags int64) error {
	if c.be != nil {
		c.be.close()
		c.be = nil
	}
	c.connected = false
	host = strings.TrimPrefix(host, "p:")
	if host == "" {
		host = "localhost"
	}
	if port <= 0 {
		port = 3306
	}
	var be backend
	var serr *sqlError
	if path, ok := sqliteHostSocket(host, socket); ok {
		be, serr = openSQLiteBackend(path, db)
	} else if path, ok := sqliteTarget(host); ok {
		be, serr = openSQLiteBackend(path, db)
	} else {
		network, addr := "tcp", net.JoinHostPort(strings.Trim(host, "[]"), strconv.FormatInt(port, 10))
		if strings.EqualFold(host, "localhost") {
			network, addr = "unix", socket
			if addr == "" {
				addr = defaultSocket
			}
		}
		var tb *tcpBackend
		tb, serr = dialMySQL(network, addr, host, user, pass, db, flags, c.connectTimeout, c.readTimeout)
		if serr == nil {
			be = tb
		} else if serr.errno == crConnectionError {
			if path, ok := fallbackSQLitePath(); ok {
				be, serr = openSQLiteBackend(path, db)
			}
		}
	}
	if serr != nil {
		c.connectErrno, c.connectError = serr.errno, serr.msg
		r.connectErrno, r.connectError = serr.errno, serr.msg
		c.setError(serr)
		if r.reportMode&ReportStrict != 0 {
			return SQLException{Code: serr.errno, SQLState: serr.sqlstate, Message: serr.msg}
		}
		if r.reportMode&ReportError != 0 {
			warn(out, fn, "("+serr.sqlstate+"/"+strconv.Itoa(serr.errno)+"): "+serr.msg)
		}
		return nil
	}
	c.be = be
	c.connected = true
	c.closed = false
	c.charset = "utf8mb4"
	c.connectErrno, c.connectError = 0, ""
	r.connectErrno, r.connectError = 0, ""
	c.clearError()
	for _, cmd := range c.initCommands {
		if _, e := be.query(cmd, false); e != nil {
			be.close()
			c.be = nil
			c.connected = false
			c.connectErrno, c.connectError = e.errno, e.msg
			r.connectErrno, r.connectError = e.errno, e.msg
			return r.report(out, fn, e)
		}
	}
	return nil
}

// connArg résout l'argument mysqli ; connected exige une session établie.
func (r *registry) connArg(z *zone.Zone, fn string, args []types.Value, i int, connected bool) (*connHandle, error) {
	if i >= len(args) {
		return nil, ArgumentCountError{fn + "() expects at least " + strconv.Itoa(i+1) + " arguments, " + strconv.Itoa(len(args)) + " given"}
	}
	id, ok := objectHandle(z, args[i], ClassIDMysqli)
	if !ok {
		return nil, TypeError{fn + "(): Argument #" + strconv.Itoa(i+1) + " ($mysql) must be of type mysqli, " + typeName(z, args[i]) + " given"}
	}
	c := r.conns[id]
	if c == nil || c.closed {
		return nil, Error{"mysqli object is already closed"}
	}
	if connected && !c.connected {
		return nil, Error{"mysqli object is not fully initialized"}
	}
	return c, nil
}

func nativeInit(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_init", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	return newHandleObject(z, ClassIDMysqli, r.newConn().id)
}

func nativeReport(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_report", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	getRegistry(z).reportMode = castInt(z, args[0])
	return types.NewBool(true), nil
}

func connectArgs(z *zone.Zone, args []types.Value, off int) (host, user, pass, db string, port int64, socket string) {
	host, _ = optString(z, args, off)
	user, _ = optString(z, args, off+1)
	pass, _ = optString(z, args, off+2)
	db, _ = optString(z, args, off+3)
	port = optInt(z, args, off+4, 3306)
	socket, _ = optString(z, args, off+5)
	return
}

func nativeConnect(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_connect", len(args), 0, 6); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	c := r.newConn()
	host, user, pass, db, port, socket := connectArgs(z, args, 0)
	if err := r.connect(out, "mysqli_connect", c, host, user, pass, db, port, socket, 0); err != nil {
		delete(r.conns, c.id)
		return types.Value{}, err
	}
	if !c.connected {
		delete(r.conns, c.id)
		return types.NewBool(false), nil
	}
	return newHandleObject(z, ClassIDMysqli, c.id)
}

func nativeRealConnect(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_real_connect", len(args), 1, 8); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	c, err := r.connArg(z, "mysqli_real_connect", args, 0, false)
	if err != nil {
		return types.Value{}, err
	}
	host, user, pass, db, port, socket := connectArgs(z, args, 1)
	if err := r.connect(out, "mysqli_real_connect", c, host, user, pass, db, port, socket, optInt(z, args, 7, 0)); err != nil {
		return types.Value{}, err
	}
	return types.NewBool(c.connected), nil
}

func nativeSelectDB(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_select_db", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	c, err := r.connArg(z, "mysqli_select_db", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	db, err := argString(z, "mysqli_select_db", args, 1, "database")
	if err != nil {
		return types.Value{}, err
	}
	if e := c.be.selectDB(db); e != nil {
		return types.NewBool(false), r.failConn(out, "mysqli_select_db", c, e)
	}
	c.clearError()
	return types.NewBool(true), nil
}

func nativeSetCharset(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_set_charset", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	c, err := r.connArg(z, "mysqli_set_charset", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	cs, err := argString(z, "mysqli_set_charset", args, 1, "charset")
	if err != nil {
		return types.Value{}, err
	}
	if e := c.be.setCharset(cs); e != nil {
		return types.NewBool(false), r.failConn(out, "mysqli_set_charset", c, e)
	}
	c.charset = lower(cs)
	c.clearError()
	return types.NewBool(true), nil
}

func nativeCharacterSetName(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_character_set_name", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	c, err := getRegistry(z).connArg(z, "mysqli_character_set_name", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, c.charset)
}

// escapeString applique l'échappement de mysql_real_escape_string ; C2_sql_find_escape (noyau sgoiter de
// c_src/sql_scan.c) désigne l'octet suivant à réécrire et les tranches saines intermédiaires sont copiées d'un bloc.
func escapeString(s string) string {
	src := strBytes(s)
	end := uint64(len(s))
	i := C2_sql_find_escape(src, 0, end)
	if i == end {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	start := uint64(0)
	for i < end {
		b.WriteString(s[start:i])
		switch s[i] {
		case 0:
			b.WriteString(`\0`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		case '"':
			b.WriteString(`\"`)
		case 0x1a:
			b.WriteString(`\Z`)
		}
		start = i + 1
		i = C2_sql_find_escape(src, start, end)
	}
	b.WriteString(s[start:])
	return b.String()
}

func nativeRealEscapeString(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_real_escape_string", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	if _, err := getRegistry(z).connArg(z, "mysqli_real_escape_string", args, 0, true); err != nil {
		return types.Value{}, err
	}
	s, err := argString(z, "mysqli_real_escape_string", args, 1, "string")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, escapeString(s))
}

// applyStatus reporte le compte rendu d'une instruction sur la connexion.
func (c *connHandle) applyStatus(res *execResult) {
	c.affected = res.affected
	c.insertID = res.insertID
	c.warnings = res.warnings
	c.info = res.info
	c.fieldCount = 0
	if res.rs != nil {
		c.fieldCount = len(res.rs.fields)
	}
}

func (c *connHandle) outOfSync() bool {
	return len(c.pending) > 0 || c.pendingErr != nil || (c.current != nil && c.current.rs != nil)
}

func (r *registry) newResultObject(z *zone.Zone, rs *resultSet, native bool) (types.Value, error) {
	h := &resultHandle{id: r.newID(), rs: rs, native: native || rs.binary}
	r.results[h.id] = h
	return newHandleObject(z, ClassIDMysqliResult, h.id)
}

func nativeQuery(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_query", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	c, err := r.connArg(z, "mysqli_query", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	sql, err := argString(z, "mysqli_query", args, 1, "query")
	if err != nil {
		return types.Value{}, err
	}
	mode := optInt(z, args, 2, 0)
	if mode&^(1|8|16) != 0 {
		return types.Value{}, ValueError{"mysqli_query(): Argument #3 ($result_mode) must be either MYSQLI_USE_RESULT, or MYSQLI_STORE_RESULT with MYSQLI_ASYNC as an optional bitmask flag"}
	}
	if c.outOfSync() {
		return types.NewBool(false), r.failConn(out, "mysqli_query", c, newSQLError(crCommandsOutSync, "HY000", "Commands out of sync; you can't run this command now"))
	}
	results, e := c.be.query(sql, false)
	if e != nil {
		return types.NewBool(false), r.failConn(out, "mysqli_query", c, e)
	}
	c.clearError()
	res := results[0]
	if len(results) > 1 {
		c.pending = results[1:]
	}
	c.applyStatus(&res)
	if res.rs == nil {
		return types.NewBool(true), nil
	}
	return r.newResultObject(z, res.rs, c.nativeTypes)
}

func nativeRealQuery(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_real_query", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	return multiQuery(z, out, args, "mysqli_real_query", false)
}

func nativeMultiQuery(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_multi_query", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	return multiQuery(z, out, args, "mysqli_multi_query", true)
}

func multiQuery(z *zone.Zone, out io.Writer, args []types.Value, fn string, multi bool) (types.Value, error) {
	r := getRegistry(z)
	c, err := r.connArg(z, fn, args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	sql, err := argString(z, fn, args, 1, "query")
	if err != nil {
		return types.Value{}, err
	}
	if c.outOfSync() {
		return types.NewBool(false), r.failConn(out, fn, c, newSQLError(crCommandsOutSync, "HY000", "Commands out of sync; you can't run this command now"))
	}
	results, e := c.be.query(sql, multi)
	if len(results) == 0 {
		if e == nil {
			e = newSQLError(1065, "42000", "Query was empty")
		}
		return types.NewBool(false), r.failConn(out, fn, c, e)
	}
	c.clearError()
	first := results[0]
	c.current = &first
	c.pending = results[1:]
	c.pendingErr = e
	c.applyStatus(&first)
	return types.NewBool(true), nil
}

func nativeMoreResults(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_more_results", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	c, err := getRegistry(z).connArg(z, "mysqli_more_results", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	return types.NewBool(len(c.pending) > 0 || c.pendingErr != nil), nil
}

func nativeNextResult(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_next_result", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	c, err := r.connArg(z, "mysqli_next_result", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	c.current = nil
	if len(c.pending) > 0 {
		next := c.pending[0]
		c.pending = c.pending[1:]
		c.current = &next
		c.applyStatus(&next)
		c.clearError()
		return types.NewBool(true), nil
	}
	if e := c.pendingErr; e != nil {
		c.pendingErr = nil
		return types.NewBool(false), r.failConn(out, "mysqli_next_result", c, e)
	}
	return types.NewBool(false), nil
}

func nativeStoreResult(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_store_result", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	c, err := r.connArg(z, "mysqli_store_result", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	cur := c.current
	if cur == nil || cur.rs == nil {
		return types.NewBool(false), nil
	}
	rs := cur.rs
	cur.rs = nil
	return r.newResultObject(z, rs, c.nativeTypes)
}

func connInt(name string, get func(c *connHandle) int64) func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		c, err := getRegistry(z).connArg(z, name, args, 0, true)
		if err != nil {
			return types.Value{}, err
		}
		return types.NewLong(get(c)), nil
	}
}

func connString(name string, get func(c *connHandle) string) func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		c, err := getRegistry(z).connArg(z, name, args, 0, true)
		if err != nil {
			return types.Value{}, err
		}
		return newStr(z, get(c))
	}
}

func nativeInfo(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_info", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	c, err := getRegistry(z).connArg(z, "mysqli_info", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	if c.info == "" {
		return types.NewNull(), nil
	}
	return newStr(z, c.info)
}

func errorList(z *zone.Zone, errno int, sqlstate, msg string) (types.Value, error) {
	arr, err := types.NewArray(z)
	if err != nil || errno == 0 {
		return arr, err
	}
	entry, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	_ = setArrayKey(z, &entry, "errno", types.NewLong(int64(errno)))
	for k, v := range map[string]string{"sqlstate": sqlstate, "error": msg} {
		s, _ := newStr(z, v)
		_ = setArrayKey(z, &entry, k, s)
		types.ReleaseValue(z, &s)
	}
	_ = types.ArrayAppend(z, &arr, entry)
	types.ReleaseValue(z, &entry)
	return arr, nil
}

func nativeErrorList(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_error_list", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	c, err := getRegistry(z).connArg(z, "mysqli_error_list", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	return errorList(z, c.errno, c.sqlstate, c.errMsg)
}

func nativeConnectErrno(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_connect_errno", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(getRegistry(z).connectErrno)), nil
}

func nativeConnectError(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_connect_error", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	if r.connectErrno == 0 {
		return types.NewNull(), nil
	}
	return newStr(z, r.connectError)
}

func nativeClientInfo(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_get_client_info", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	return newStr(z, clientInfo)
}

func nativeClientVersion(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_get_client_version", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewLong(clientVersion), nil
}

func nativePing(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_ping", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	c, err := r.connArg(z, "mysqli_ping", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	if !c.be.ping() {
		return types.NewBool(false), r.failConn(out, "mysqli_ping", c, newSQLError(crServerGone, "HY000", "MySQL server has gone away"))
	}
	return types.NewBool(true), nil
}

func nativeOptions(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_options", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	c, err := getRegistry(z).connArg(z, "mysqli_options", args, 0, false)
	if err != nil {
		return types.Value{}, err
	}
	switch castInt(z, args[1]) {
	case optConnectTimeout:
		c.connectTimeout = time.Duration(castInt(z, args[2])) * time.Second
	case optReadTimeout:
		c.readTimeout = time.Duration(castInt(z, args[2])) * time.Second
	case optIntAndFloatNative:
		c.nativeTypes = types.ToBoolZ(z, types.Deref(z, args[2]))
	case optInitCommand:
		s, _ := castString(z, args[2])
		c.initCommands = append(c.initCommands, s)
	case optLocalInfile, optSSLVerifyServerCrt, 202, 203, 4, 5, 35:
	default:
		return types.NewBool(false), nil
	}
	return types.NewBool(true), nil
}

// txQuery exécute une instruction de contrôle transactionnel.
func txQuery(z *zone.Zone, out io.Writer, args []types.Value, fn string, minN, maxN int, build func(c *connHandle) string) (types.Value, error) {
	if err := arity(fn, len(args), minN, maxN); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	c, err := r.connArg(z, fn, args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	if _, e := c.be.query(build(c), false); e != nil {
		return types.NewBool(false), r.failConn(out, fn, c, e)
	}
	c.clearError()
	return types.NewBool(true), nil
}

func nativeBeginTransaction(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	flags := optInt(z, args, 1, 0)
	return txQuery(z, out, args, "mysqli_begin_transaction", 1, 3, func(*connHandle) string {
		var mods []string
		if flags&1 != 0 {
			mods = append(mods, "WITH CONSISTENT SNAPSHOT")
		}
		if flags&2 != 0 {
			mods = append(mods, "READ WRITE")
		}
		if flags&4 != 0 {
			mods = append(mods, "READ ONLY")
		}
		return strings.TrimSpace("START TRANSACTION " + strings.Join(mods, ", "))
	})
}

func nativeCommit(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	return txQuery(z, out, args, "mysqli_commit", 1, 3, func(*connHandle) string { return "COMMIT" })
}

func nativeRollback(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	return txQuery(z, out, args, "mysqli_rollback", 1, 3, func(*connHandle) string { return "ROLLBACK" })
}

func nativeAutocommit(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	on := len(args) > 1 && types.ToBoolZ(z, types.Deref(z, args[1]))
	return txQuery(z, out, args, "mysqli_autocommit", 2, 2, func(*connHandle) string {
		if on {
			return "SET autocommit = 1"
		}
		return "SET autocommit = 0"
	})
}

func nativeSavepoint(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	name, _ := optString(z, args, 1)
	return txQuery(z, out, args, "mysqli_savepoint", 2, 2, func(*connHandle) string { return "SAVEPOINT `" + name + "`" })
}

func nativeReleaseSavepoint(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	name, _ := optString(z, args, 1)
	return txQuery(z, out, args, "mysqli_release_savepoint", 2, 2, func(*connHandle) string { return "RELEASE SAVEPOINT `" + name + "`" })
}

func nativeClose(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_close", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	c, err := r.connArg(z, "mysqli_close", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	for id, s := range r.stmts {
		if s.conn == c.id {
			s.release(z)
			delete(r.stmts, id)
		}
	}
	c.shutdown()
	return types.NewBool(true), nil
}
