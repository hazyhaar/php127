package mysqli

import (
	"io"
	"strconv"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func (r *registry) stmtArg(z *zone.Zone, fn string, args []types.Value, i int, prepared bool) (*stmtHandle, error) {
	if i >= len(args) {
		return nil, ArgumentCountError{fn + "() expects at least " + strconv.Itoa(i+1) + " arguments, " + strconv.Itoa(len(args)) + " given"}
	}
	id, ok := objectHandle(z, args[i], ClassIDMysqliStmt)
	if !ok {
		return nil, TypeError{fn + "(): Argument #" + strconv.Itoa(i+1) + " ($statement) must be of type mysqli_stmt, " + typeName(z, args[i]) + " given"}
	}
	s := r.stmts[id]
	if s == nil || s.closed {
		return nil, Error{"mysqli_stmt object is already closed"}
	}
	if prepared && s.sb == nil {
		return nil, Error{"mysqli_stmt object is not fully initialized"}
	}
	return s, nil
}

func (s *stmtHandle) setError(e *sqlError) {
	s.errno, s.sqlstate, s.errMsg = e.errno, e.sqlstate, e.msg
}

func (s *stmtHandle) clearError() {
	s.errno, s.sqlstate, s.errMsg = 0, "00000", ""
}

func (r *registry) failStmt(out io.Writer, fn string, s *stmtHandle, e *sqlError) error {
	s.setError(e)
	return r.report(out, fn, e)
}

func (r *registry) newStmt(c *connHandle) *stmtHandle {
	s := &stmtHandle{id: r.newID(), conn: c.id, sqlstate: "00000"}
	r.stmts[s.id] = s
	return s
}

func nativeStmtInit(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_init", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	c, err := r.connArg(z, "mysqli_stmt_init", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	return newHandleObject(z, ClassIDMysqliStmt, r.newStmt(c).id)
}

// prepareOn prépare sql sur la connexion du handle ; l'erreur est portée par la requête.
func (r *registry) prepareOn(s *stmtHandle, sql string) *sqlError {
	c := r.conns[s.conn]
	if c == nil || !c.connected {
		return newSQLError(crServerGone, "HY000", "MySQL server has gone away")
	}
	if s.sb != nil {
		s.sb.close()
		s.sb = nil
	}
	sb, e := c.be.prepare(sql)
	if e != nil {
		return e
	}
	s.sb = sb
	s.result = nil
	s.executed = false
	s.clearError()
	return nil
}

func nativeStmtPrepare(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_prepare", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	s, err := r.stmtArg(z, "mysqli_stmt_prepare", args, 0, false)
	if err != nil {
		return types.Value{}, err
	}
	sql, err := argString(z, "mysqli_stmt_prepare", args, 1, "query")
	if err != nil {
		return types.Value{}, err
	}
	if e := r.prepareOn(s, sql); e != nil {
		return types.NewBool(false), r.failStmt(out, "mysqli_stmt_prepare", s, e)
	}
	return types.NewBool(true), nil
}

func nativePrepare(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_prepare", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	c, err := r.connArg(z, "mysqli_prepare", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	sql, err := argString(z, "mysqli_prepare", args, 1, "query")
	if err != nil {
		return types.Value{}, err
	}
	s := r.newStmt(c)
	if e := r.prepareOn(s, sql); e != nil {
		delete(r.stmts, s.id)
		return types.NewBool(false), r.failConn(out, "mysqli_prepare", c, e)
	}
	c.clearError()
	return newHandleObject(z, ClassIDMysqliStmt, s.id)
}

// nativeStmtBindParam retient les variables liées (références comprises) jusqu'à l'exécution.
func nativeStmtBindParam(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_bind_param", len(args), 3, -1); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	s, err := r.stmtArg(z, "mysqli_stmt_bind_param", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	typesStr, err := argString(z, "mysqli_stmt_bind_param", args, 1, "types")
	if err != nil {
		return types.Value{}, err
	}
	vars := args[2:]
	if len(typesStr) == 0 {
		return types.Value{}, ValueError{"mysqli_stmt_bind_param(): Argument #2 ($types) cannot be empty"}
	}
	if len(typesStr) != len(vars) {
		return types.Value{}, ArgumentCountError{"The number of elements in the type definition string must match the number of bind variables"}
	}
	if len(vars) != s.sb.paramCount() {
		return types.Value{}, ArgumentCountError{"The number of variables must match the number of parameters in the prepared statement"}
	}
	for i := range len(typesStr) {
		switch typesStr[i] {
		case 'i', 'd', 's', 'b':
		default:
			return types.Value{}, ValueError{"mysqli_stmt_bind_param(): Argument #2 ($types) must only contain the \"b\", \"d\", \"i\", \"s\" type specifiers"}
		}
	}
	for i := range s.bound {
		types.ReleaseValue(z, &s.bound[i])
	}
	s.bound = make([]types.Value, len(vars))
	for i, v := range vars {
		s.bound[i] = types.RetainValue(z, v)
	}
	s.types = typesStr
	return types.NewBool(true), nil
}

// paramCell convertit une variable liée selon son spécificateur de type.
func paramCell(z *zone.Zone, v types.Value, spec byte) cell {
	v = types.Deref(z, v)
	if v.Type == types.TypeNull || v.Type == types.TypeUndef {
		return nullCell
	}
	switch spec {
	case 'i':
		return intCell(castInt(z, v))
	case 'd':
		return cell{kind: cellFloat, f: castFloat(z, v)}
	case 'b':
		s, _ := castString(z, v)
		return cell{kind: cellBlob, s: s}
	default:
		s, _ := castString(z, v)
		return textCell(s)
	}
}

func nativeStmtExecute(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_execute", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	s, err := r.stmtArg(z, "mysqli_stmt_execute", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	var params []cell
	if len(args) == 2 && !isNull(z, args[1]) {
		arr := types.Deref(z, args[1])
		if arr.Type != types.TypeArray {
			return types.Value{}, TypeError{"mysqli_stmt_execute(): Argument #2 ($params) must be of type ?array, " + typeName(z, args[1]) + " given"}
		}
		types.ArrayForEach(z, arr, func(_, v types.Value) bool {
			params = append(params, paramCell(z, v, 's'))
			return true
		})
		if len(params) != s.sb.paramCount() {
			return types.Value{}, ValueError{"mysqli_stmt_execute(): Argument #2 ($params) must consist of exactly " + strconv.Itoa(s.sb.paramCount()) + " elements, " + strconv.Itoa(len(params)) + " present"}
		}
	} else {
		if s.sb.paramCount() > 0 && len(s.bound) != s.sb.paramCount() {
			return types.NewBool(false), r.failStmt(out, "mysqli_stmt_execute", s, newSQLError(crParamsNotBound, "HY000", "No data supplied for parameters in prepared statement"))
		}
		params = make([]cell, len(s.bound))
		for i, v := range s.bound {
			params[i] = paramCell(z, v, s.types[i])
		}
	}
	res, e := s.sb.execute(params)
	if e != nil {
		return types.NewBool(false), r.failStmt(out, "mysqli_stmt_execute", s, e)
	}
	s.clearError()
	s.affected, s.insertID = res.affected, res.insertID
	if res.rs != nil {
		res.rs.binary = true
	}
	s.result = res.rs
	s.cursor = 0
	s.executed = true
	if c := r.conns[s.conn]; c != nil {
		c.applyStatus(&res)
	}
	return types.NewBool(true), nil
}

func nativeStmtGetResult(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_get_result", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	s, err := r.stmtArg(z, "mysqli_stmt_get_result", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	if s.result == nil {
		if !s.executed {
			return types.NewBool(false), r.failStmt(out, "mysqli_stmt_get_result", s, newSQLError(crCommandsOutSync, "HY000", "Commands out of sync; you can't run this command now"))
		}
		return types.NewBool(false), nil
	}
	rs := s.result
	s.result = nil
	return r.newResultObject(z, rs, true)
}

func nativeStmtBindResult(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_bind_result", len(args), 2, -1); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	s, err := r.stmtArg(z, "mysqli_stmt_bind_result", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	if n := len(s.sb.fields()); len(args)-1 != n {
		return types.Value{}, ValueError{"mysqli_stmt_bind_result(): Number of bind variables doesn't match number of fields in prepared statement"}
	}
	for i := range s.resultRefs {
		types.ReleaseValue(z, &s.resultRefs[i])
	}
	s.resultRefs = make([]types.Value, len(args)-1)
	for i, v := range args[1:] {
		s.resultRefs[i] = types.RetainValue(z, v)
	}
	return types.NewBool(true), nil
}

// nativeStmtFetch écrit la ligne suivante dans les références liées par mysqli_stmt_bind_result.
func nativeStmtFetch(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_fetch", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	s, err := r.stmtArg(z, "mysqli_stmt_fetch", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	if s.result == nil || s.cursor >= len(s.result.rows) {
		return types.NewNull(), nil
	}
	row := s.result.rows[s.cursor]
	s.cursor++
	for i, ref := range s.resultRefs {
		if ref.Type != types.TypeReference || i >= len(row) {
			continue
		}
		v, err := cellValue(z, row[i], s.result.fields[i], true)
		if err != nil {
			return types.Value{}, err
		}
		types.AssignRef(z, ref, v)
		types.ReleaseValue(z, &v)
	}
	return types.NewBool(true), nil
}

func stmtInt(name string, get func(s *stmtHandle) int64) func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		s, err := getRegistry(z).stmtArg(z, name, args, 0, false)
		if err != nil {
			return types.Value{}, err
		}
		return types.NewLong(get(s)), nil
	}
}

func stmtNumRows(s *stmtHandle) int64 {
	if s.result == nil {
		return 0
	}
	return int64(len(s.result.rows))
}

func stmtParamCount(s *stmtHandle) int64 {
	if s.sb == nil {
		return 0
	}
	return int64(s.sb.paramCount())
}

func stmtFieldCount(s *stmtHandle) int64 {
	if s.sb == nil {
		return 0
	}
	return int64(len(s.sb.fields()))
}

func nativeStmtError(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_error", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := getRegistry(z).stmtArg(z, "mysqli_stmt_error", args, 0, false)
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, s.errMsg)
}

func nativeStmtSQLState(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_sqlstate", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := getRegistry(z).stmtArg(z, "mysqli_stmt_sqlstate", args, 0, false)
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, s.sqlstate)
}

func nativeStmtErrorList(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_error_list", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := getRegistry(z).stmtArg(z, "mysqli_stmt_error_list", args, 0, false)
	if err != nil {
		return types.Value{}, err
	}
	return errorList(z, s.errno, s.sqlstate, s.errMsg)
}

func nativeStmtClose(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_close", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	s, err := r.stmtArg(z, "mysqli_stmt_close", args, 0, false)
	if err != nil {
		return types.Value{}, err
	}
	s.release(z)
	delete(r.stmts, s.id)
	return types.NewBool(true), nil
}

func stmtBool(name string, act func(s *stmtHandle)) func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		s, err := getRegistry(z).stmtArg(z, name, args, 0, true)
		if err != nil {
			return types.Value{}, err
		}
		act(s)
		return types.NewBool(true), nil
	}
}

func nativeStmtDataSeek(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_data_seek", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	s, err := getRegistry(z).stmtArg(z, "mysqli_stmt_data_seek", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	off := castInt(z, args[1])
	if off < 0 {
		return types.Value{}, ValueError{"mysqli_stmt_data_seek(): Argument #2 ($offset) must be greater than or equal to 0"}
	}
	if s.result != nil && off < int64(len(s.result.rows)) {
		s.cursor = int(off)
	}
	return types.NewNull(), nil
}

func nativeStmtResultMetadata(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_stmt_result_metadata", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	s, err := r.stmtArg(z, "mysqli_stmt_result_metadata", args, 0, true)
	if err != nil {
		return types.Value{}, err
	}
	f := s.sb.fields()
	if len(f) == 0 {
		return types.NewBool(false), nil
	}
	return r.newResultObject(z, &resultSet{fields: f, binary: true}, true)
}
