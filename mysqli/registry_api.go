package mysqli

import (
	"io"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

type nativeFn = func(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error)

var natives map[string]nativeFn

func init() {
	natives = map[string]nativeFn{
		"mysqli_init":                 nativeInit,
		"mysqli_report":               nativeReport,
		"mysqli_connect":              nativeConnect,
		"mysqli_real_connect":         nativeRealConnect,
		"mysqli_select_db":            nativeSelectDB,
		"mysqli_set_charset":          nativeSetCharset,
		"mysqli_character_set_name":   nativeCharacterSetName,
		"mysqli_client_encoding":      nativeCharacterSetName,
		"mysqli_real_escape_string":   nativeRealEscapeString,
		"mysqli_escape_string":        nativeRealEscapeString,
		"mysqli_query":                nativeQuery,
		"mysqli_real_query":           nativeRealQuery,
		"mysqli_multi_query":          nativeMultiQuery,
		"mysqli_more_results":         nativeMoreResults,
		"mysqli_next_result":          nativeNextResult,
		"mysqli_store_result":         nativeStoreResult,
		"mysqli_use_result":           nativeStoreResult,
		"mysqli_prepare":              nativePrepare,
		"mysqli_stmt_init":            nativeStmtInit,
		"mysqli_stmt_prepare":         nativeStmtPrepare,
		"mysqli_stmt_bind_param":      nativeStmtBindParam,
		"mysqli_stmt_execute":         nativeStmtExecute,
		"mysqli_execute":              nativeStmtExecute,
		"mysqli_stmt_get_result":      nativeStmtGetResult,
		"mysqli_stmt_bind_result":     nativeStmtBindResult,
		"mysqli_stmt_fetch":           nativeStmtFetch,
		"mysqli_stmt_close":           nativeStmtClose,
		"mysqli_stmt_affected_rows":   stmtInt("mysqli_stmt_affected_rows", func(s *stmtHandle) int64 { return s.affected }),
		"mysqli_stmt_insert_id":       stmtInt("mysqli_stmt_insert_id", func(s *stmtHandle) int64 { return s.insertID }),
		"mysqli_stmt_errno":           stmtInt("mysqli_stmt_errno", func(s *stmtHandle) int64 { return int64(s.errno) }),
		"mysqli_stmt_error":           nativeStmtError,
		"mysqli_stmt_error_list":      nativeStmtErrorList,
		"mysqli_stmt_sqlstate":        nativeStmtSQLState,
		"mysqli_stmt_num_rows":        stmtInt("mysqli_stmt_num_rows", stmtNumRows),
		"mysqli_stmt_param_count":     stmtInt("mysqli_stmt_param_count", stmtParamCount),
		"mysqli_stmt_field_count":     stmtInt("mysqli_stmt_field_count", stmtFieldCount),
		"mysqli_stmt_store_result":    stmtBool("mysqli_stmt_store_result", func(*stmtHandle) {}),
		"mysqli_stmt_free_result":     stmtBool("mysqli_stmt_free_result", func(s *stmtHandle) { s.result = nil }),
		"mysqli_stmt_reset":           stmtBool("mysqli_stmt_reset", func(s *stmtHandle) { s.result, s.cursor = nil, 0 }),
		"mysqli_stmt_data_seek":       nativeStmtDataSeek,
		"mysqli_stmt_result_metadata": nativeStmtResultMetadata,
		"mysqli_fetch_object":         nativeFetchObject,
		"mysqli_fetch_assoc":          fetchWith("mysqli_fetch_assoc", modeAssoc),
		"mysqli_fetch_row":            fetchWith("mysqli_fetch_row", modeNum),
		"mysqli_fetch_array":          fetchWith("mysqli_fetch_array", 0),
		"mysqli_fetch_all":            nativeFetchAll,
		"mysqli_fetch_column":         nativeFetchColumn,
		"mysqli_fetch_field":          nativeFetchField,
		"mysqli_fetch_fields":         nativeFetchFields,
		"mysqli_fetch_field_direct":   nativeFetchFieldDirect,
		"mysqli_fetch_lengths":        nativeFetchLengths,
		"mysqli_field_seek":           nativeFieldSeek,
		"mysqli_field_tell":           resultInt("mysqli_field_tell", func(h *resultHandle) int64 { return int64(h.fieldCursor) }),
		"mysqli_num_rows":             resultInt("mysqli_num_rows", func(h *resultHandle) int64 { return int64(len(h.rs.rows)) }),
		"mysqli_num_fields":           resultInt("mysqli_num_fields", func(h *resultHandle) int64 { return int64(len(h.rs.fields)) }),
		"mysqli_data_seek":            nativeDataSeek,
		"mysqli_free_result":          nativeFreeResult,
		"mysqli_field_count":          connInt("mysqli_field_count", func(c *connHandle) int64 { return int64(c.fieldCount) }),
		"mysqli_affected_rows":        connInt("mysqli_affected_rows", func(c *connHandle) int64 { return c.affected }),
		"mysqli_insert_id":            connInt("mysqli_insert_id", func(c *connHandle) int64 { return c.insertID }),
		"mysqli_errno":                connInt("mysqli_errno", func(c *connHandle) int64 { return int64(c.errno) }),
		"mysqli_warning_count":        connInt("mysqli_warning_count", func(c *connHandle) int64 { return int64(c.warnings) }),
		"mysqli_thread_id":            connInt("mysqli_thread_id", func(c *connHandle) int64 { return c.be.threadID() }),
		"mysqli_get_server_version":   connInt("mysqli_get_server_version", func(c *connHandle) int64 { return int64(c.be.serverVersion()) }),
		"mysqli_get_proto_info":       connInt("mysqli_get_proto_info", func(c *connHandle) int64 { return int64(c.be.protocolVersion()) }),
		"mysqli_error":                connString("mysqli_error", func(c *connHandle) string { return c.errMsg }),
		"mysqli_sqlstate":             connString("mysqli_sqlstate", func(c *connHandle) string { return c.sqlstate }),
		"mysqli_get_server_info":      connString("mysqli_get_server_info", func(c *connHandle) string { return c.be.serverInfo() }),
		"mysqli_get_host_info":        connString("mysqli_get_host_info", func(c *connHandle) string { return c.be.hostInfo() }),
		"mysqli_stat":                 connString("mysqli_stat", func(c *connHandle) string { return c.be.stat() }),
		"mysqli_info":                 nativeInfo,
		"mysqli_error_list":           nativeErrorList,
		"mysqli_connect_errno":        nativeConnectErrno,
		"mysqli_connect_error":        nativeConnectError,
		"mysqli_get_client_info":      nativeClientInfo,
		"mysqli_get_client_version":   nativeClientVersion,
		"mysqli_ping":                 nativePing,
		"mysqli_options":              nativeOptions,
		"mysqli_set_opt":              nativeOptions,
		"mysqli_begin_transaction":    nativeBeginTransaction,
		"mysqli_commit":               nativeCommit,
		"mysqli_rollback":             nativeRollback,
		"mysqli_autocommit":           nativeAutocommit,
		"mysqli_savepoint":            nativeSavepoint,
		"mysqli_release_savepoint":    nativeReleaseSavepoint,
		"mysqli_close":                nativeClose,
		"mysqli_thread_safe": func(*zone.Zone, io.Writer, []types.Value) (types.Value, error) {
			return types.NewBool(true), nil
		},
	}
}

// Lookup retourne la fonction procédurale mysqli_* de ce nom (insensible à la casse).
func Lookup(name string) (stdlib.NativeFunc, bool) {
	fn, ok := natives[strings.ToLower(strings.TrimPrefix(name, `\`))]
	if !ok {
		return nil, false
	}
	return stdlib.NativeFunc(fn), true
}

// NativeArgIsRef indique les arguments transmis par référence (variables liées).
func NativeArgIsRef(name string, idx int) bool {
	switch strings.ToLower(strings.TrimPrefix(name, `\`)) {
	case "mysqli_stmt_bind_param":
		return idx >= 2
	case "mysqli_stmt_bind_result":
		return idx >= 1
	}
	return false
}

// ClassID retourne l'identifiant d'une classe mysqli par son nom.
func ClassID(name string) (uint32, bool) {
	switch strings.ToLower(strings.TrimPrefix(name, `\`)) {
	case "mysqli":
		return ClassIDMysqli, true
	case "mysqli_result":
		return ClassIDMysqliResult, true
	case "mysqli_stmt":
		return ClassIDMysqliStmt, true
	case "mysqli_driver":
		return ClassIDMysqliDriver, true
	case "mysqli_sql_exception":
		return ClassIDMysqliSqlException, true
	}
	return 0, false
}

var connMethods = map[string]string{
	"autocommit": "mysqli_autocommit", "begin_transaction": "mysqli_begin_transaction",
	"character_set_name": "mysqli_character_set_name", "close": "mysqli_close", "commit": "mysqli_commit",
	"get_client_info": "mysqli_get_client_info", "get_server_info": "mysqli_get_server_info",
	"more_results": "mysqli_more_results", "multi_query": "mysqli_multi_query", "next_result": "mysqli_next_result",
	"options": "mysqli_options", "set_opt": "mysqli_options", "ping": "mysqli_ping", "prepare": "mysqli_prepare",
	"query": "mysqli_query", "real_connect": "mysqli_real_connect", "real_escape_string": "mysqli_real_escape_string",
	"escape_string": "mysqli_real_escape_string", "real_query": "mysqli_real_query",
	"release_savepoint": "mysqli_release_savepoint", "rollback": "mysqli_rollback", "savepoint": "mysqli_savepoint",
	"select_db": "mysqli_select_db", "set_charset": "mysqli_set_charset", "stat": "mysqli_stat",
	"stmt_init": "mysqli_stmt_init", "store_result": "mysqli_store_result", "use_result": "mysqli_store_result",
	"thread_safe": "mysqli_thread_safe",
}

var resultMethods = map[string]string{
	"fetch_all": "mysqli_fetch_all", "fetch_array": "mysqli_fetch_array", "fetch_assoc": "mysqli_fetch_assoc",
	"fetch_column": "mysqli_fetch_column", "fetch_field_direct": "mysqli_fetch_field_direct",
	"fetch_field": "mysqli_fetch_field", "fetch_fields": "mysqli_fetch_fields", "fetch_object": "mysqli_fetch_object",
	"fetch_row": "mysqli_fetch_row", "field_seek": "mysqli_field_seek", "data_seek": "mysqli_data_seek",
	"free": "mysqli_free_result", "close": "mysqli_free_result", "free_result": "mysqli_free_result",
}

var stmtMethods = map[string]string{
	"bind_param": "mysqli_stmt_bind_param", "bind_result": "mysqli_stmt_bind_result", "close": "mysqli_stmt_close",
	"data_seek": "mysqli_stmt_data_seek", "execute": "mysqli_stmt_execute", "fetch": "mysqli_stmt_fetch",
	"free_result": "mysqli_stmt_free_result", "get_result": "mysqli_stmt_get_result", "num_rows": "mysqli_stmt_num_rows",
	"prepare": "mysqli_stmt_prepare", "reset": "mysqli_stmt_reset", "result_metadata": "mysqli_stmt_result_metadata",
	"store_result": "mysqli_stmt_store_result",
}

func objectClass(z *zone.Zone, obj types.Value) (uint32, bool) {
	obj = types.Deref(z, obj)
	if obj.Type != types.TypeObject {
		return 0, false
	}
	return types.ObjectInfo(z, obj).ClassID, true
}

// CallMethod appelle une méthode d'instance de mysqli, mysqli_result ou mysqli_stmt.
func CallMethod(z *zone.Zone, out io.Writer, obj types.Value, method string, args []types.Value) (types.Value, error) {
	cls, ok := objectClass(z, obj)
	if !ok {
		return types.Value{}, Error{"Call to a member function " + method + "() on " + typeName(z, obj)}
	}
	m := strings.ToLower(method)
	var table map[string]string
	className := ""
	switch cls {
	case ClassIDMysqli:
		table, className = connMethods, "mysqli"
		if m == "connect" || m == "__construct" {
			r := getRegistry(z)
			id, _ := objectHandle(z, obj, ClassIDMysqli)
			c := r.conns[id]
			if c == nil {
				return types.Value{}, Error{"mysqli object is already closed"}
			}
			host, user, pass, db, port, socket := connectArgs(z, args, 0)
			if err := r.connect(out, "mysqli::"+m, c, host, user, pass, db, port, socket, 0); err != nil {
				return types.Value{}, err
			}
			if m == "connect" {
				return types.NewBool(c.connected), nil
			}
			return types.NewNull(), nil
		}
	case ClassIDMysqliResult:
		table, className = resultMethods, "mysqli_result"
	case ClassIDMysqliStmt:
		table, className = stmtMethods, "mysqli_stmt"
	default:
		return types.Value{}, Error{"Call to undefined method " + method + "()"}
	}
	fnName, ok := table[m]
	if !ok {
		return types.Value{}, Error{"Call to undefined method " + className + "::" + method + "()"}
	}
	full := make([]types.Value, 0, len(args)+1)
	full = append(full, obj)
	full = append(full, args...)
	return natives[fnName](z, out, full)
}

// Construct instancie mysqli (connexion immédiate), mysqli_stmt ou mysqli_driver.
func Construct(z *zone.Zone, out io.Writer, className string, args []types.Value) (types.Value, error) {
	r := getRegistry(z)
	switch strings.ToLower(strings.TrimPrefix(className, `\`)) {
	case "mysqli":
		if err := arity("mysqli::__construct", len(args), 0, 6); err != nil {
			return types.Value{}, err
		}
		c := r.newConn()
		obj, err := newHandleObject(z, ClassIDMysqli, c.id)
		if err != nil {
			return types.Value{}, err
		}
		host, user, pass, db, port, socket := connectArgs(z, args, 0)
		if err := r.connect(out, "mysqli::__construct", c, host, user, pass, db, port, socket, 0); err != nil {
			types.ReleaseValue(z, &obj)
			return types.Value{}, err
		}
		return obj, nil
	case "mysqli_stmt":
		if err := arity("mysqli_stmt::__construct", len(args), 1, 2); err != nil {
			return types.Value{}, err
		}
		c, err := r.connArg(z, "mysqli_stmt::__construct", args, 0, true)
		if err != nil {
			return types.Value{}, err
		}
		s := r.newStmt(c)
		if q, ok := optString(z, args, 1); ok {
			if e := r.prepareOn(s, q); e != nil {
				if err := r.failStmt(out, "mysqli_stmt::__construct", s, e); err != nil {
					delete(r.stmts, s.id)
					return types.Value{}, err
				}
			}
		}
		return newHandleObject(z, ClassIDMysqliStmt, s.id)
	case "mysqli_driver":
		return types.NewObject(z, ClassIDMysqliDriver, 0, false)
	}
	return types.Value{}, Error{"Cannot instantiate " + className + " through the mysqli bridge"}
}

// GetProperty lit une propriété d'un objet mysqli, mysqli_result, mysqli_stmt ou mysqli_driver ;
// found vaut false pour une propriété inconnue.
func GetProperty(z *zone.Zone, obj types.Value, name string) (v types.Value, found bool, err error) {
	cls, ok := objectClass(z, obj)
	if !ok {
		return types.Value{}, false, nil
	}
	r := getRegistry(z)
	args := []types.Value{obj}
	call := func(fn string) (types.Value, bool, error) {
		v, err := natives[fn](z, nil, args)
		return v, true, err
	}
	switch cls {
	case ClassIDMysqli:
		switch name {
		case "connect_errno":
			return types.NewLong(int64(r.connectErrno)), true, nil
		case "connect_error":
			if r.connectErrno == 0 {
				return types.NewNull(), true, nil
			}
			s, err := newStr(z, r.connectError)
			return s, true, err
		case "client_info":
			s, err := newStr(z, clientInfo)
			return s, true, err
		case "client_version":
			return types.NewLong(clientVersion), true, nil
		}
		props := map[string]string{
			"affected_rows": "mysqli_affected_rows", "errno": "mysqli_errno", "error": "mysqli_error",
			"error_list": "mysqli_error_list", "field_count": "mysqli_field_count", "host_info": "mysqli_get_host_info",
			"info": "mysqli_info", "insert_id": "mysqli_insert_id", "server_info": "mysqli_get_server_info",
			"server_version": "mysqli_get_server_version", "sqlstate": "mysqli_sqlstate",
			"protocol_version": "mysqli_get_proto_info", "thread_id": "mysqli_thread_id", "warning_count": "mysqli_warning_count",
		}
		if fn, ok := props[name]; ok {
			return call(fn)
		}
	case ClassIDMysqliResult:
		h, err := r.resultArg(z, "mysqli_result::$"+name, args, 0)
		if err != nil {
			return types.Value{}, true, err
		}
		switch name {
		case "current_field":
			return types.NewLong(int64(h.fieldCursor)), true, nil
		case "field_count":
			return types.NewLong(int64(len(h.rs.fields))), true, nil
		case "num_rows":
			return types.NewLong(int64(len(h.rs.rows))), true, nil
		case "type":
			return types.NewLong(0), true, nil
		case "lengths":
			v, err := lengthsValue(z, h)
			if err == nil && v.Type == types.TypeFalse {
				v = types.NewNull()
			}
			return v, true, err
		}
	case ClassIDMysqliStmt:
		props := map[string]string{
			"affected_rows": "mysqli_stmt_affected_rows", "insert_id": "mysqli_stmt_insert_id",
			"num_rows": "mysqli_stmt_num_rows", "param_count": "mysqli_stmt_param_count",
			"field_count": "mysqli_stmt_field_count", "errno": "mysqli_stmt_errno", "error": "mysqli_stmt_error",
			"error_list": "mysqli_stmt_error_list", "sqlstate": "mysqli_stmt_sqlstate",
		}
		if fn, ok := props[name]; ok {
			return call(fn)
		}
		if name == "id" {
			id, _ := objectHandle(z, obj, ClassIDMysqliStmt)
			return types.NewLong(int64(id)), true, nil
		}
	case ClassIDMysqliDriver:
		switch name {
		case "client_info":
			s, err := newStr(z, clientInfo)
			return s, true, err
		case "client_version", "driver_version":
			return types.NewLong(clientVersion), true, nil
		case "reconnect":
			return types.NewBool(false), true, nil
		case "report_mode":
			return types.NewLong(r.reportMode), true, nil
		}
	}
	return types.Value{}, false, nil
}

// SetProperty écrit mysqli_driver::$report_mode ; les autres propriétés mysqli sont en lecture seule.
func SetProperty(z *zone.Zone, obj types.Value, name string, val types.Value) error {
	cls, ok := objectClass(z, obj)
	if ok && cls == ClassIDMysqliDriver && name == "report_mode" {
		getRegistry(z).reportMode = castInt(z, val)
		return nil
	}
	return Error{"Cannot write property $" + name + " of a mysqli object"}
}
