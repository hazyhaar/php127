package mysqli

import (
	"io"
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func (r *registry) resultArg(z *zone.Zone, fn string, args []types.Value, i int) (*resultHandle, error) {
	if i >= len(args) {
		return nil, ArgumentCountError{fn + "() expects at least " + strconv.Itoa(i+1) + " arguments, " + strconv.Itoa(len(args)) + " given"}
	}
	id, ok := objectHandle(z, args[i], ClassIDMysqliResult)
	if !ok {
		return nil, TypeError{fn + "(): Argument #" + strconv.Itoa(i+1) + " ($result) must be of type mysqli_result, " + typeName(z, args[i]) + " given"}
	}
	h := r.results[id]
	if h == nil || h.freed {
		return nil, Error{"mysqli_result object is already closed"}
	}
	return h, nil
}

// rowValue construit le tableau PHP d'une ligne selon MYSQLI_ASSOC, MYSQLI_NUM ou MYSQLI_BOTH.
func (h *resultHandle) rowValue(z *zone.Zone, row []cell, mode int64) (types.Value, error) {
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for i, c := range row {
		f := h.rs.fields[i]
		v, err := cellValue(z, c, f, h.native)
		if err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
		if mode&modeNum != 0 {
			err = types.ArraySet(z, &arr, types.NewLong(int64(i)), v, warnDrop)
		}
		if err == nil && mode&modeAssoc != 0 {
			err = setArrayKey(z, &arr, f.name, v)
		}
		types.ReleaseValue(z, &v)
		if err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
	}
	return arr, nil
}

func (h *resultHandle) next() ([]cell, bool) {
	if h.cursor >= len(h.rs.rows) {
		return nil, false
	}
	row := h.rs.rows[h.cursor]
	h.cursor++
	h.lastRow = row
	return row, true
}

func fetchMode(fn string, mode int64) error {
	if mode != modeAssoc && mode != modeNum && mode != modeBoth {
		return ValueError{fn + "(): Argument #2 ($mode) must be one of MYSQLI_NUM, MYSQLI_ASSOC, or MYSQLI_BOTH"}
	}
	return nil
}

func fetchWith(name string, fixed int64) func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		maxArgs := 1
		if fixed == 0 {
			maxArgs = 2
		}
		if err := arity(name, len(args), 1, maxArgs); err != nil {
			return types.Value{}, err
		}
		h, err := getRegistry(z).resultArg(z, name, args, 0)
		if err != nil {
			return types.Value{}, err
		}
		mode := fixed
		if fixed == 0 {
			mode = optInt(z, args, 1, modeBoth)
			if err := fetchMode(name, mode); err != nil {
				return types.Value{}, err
			}
		}
		row, ok := h.next()
		if !ok {
			return types.NewNull(), nil
		}
		return h.rowValue(z, row, mode)
	}
}

func nativeFetchObject(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_fetch_object", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	h, err := getRegistry(z).resultArg(z, "mysqli_fetch_object", args, 0)
	if err != nil {
		return types.Value{}, err
	}
	if class, ok := optString(z, args, 1); ok && !strings.EqualFold(strings.TrimPrefix(class, `\`), "stdClass") {
		return types.Value{}, Error{"mysqli_fetch_object(): class \"" + class + "\" is not supported by the native bridge (stdClass only)"}
	}
	row, ok := h.next()
	if !ok {
		return types.NewNull(), nil
	}
	assoc, err := h.rowValue(z, row, modeAssoc)
	if err != nil {
		return types.Value{}, err
	}
	obj, err := types.NewObject(z, ClassIDStdClass, 0, false)
	if err != nil {
		types.ReleaseValue(z, &assoc)
		return types.Value{}, err
	}
	attachDynamicArray(z, obj, assoc)
	return obj, nil
}

func nativeFetchAll(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_fetch_all", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	h, err := getRegistry(z).resultArg(z, "mysqli_fetch_all", args, 0)
	if err != nil {
		return types.Value{}, err
	}
	mode := optInt(z, args, 1, modeNum)
	if err := fetchMode("mysqli_fetch_all", mode); err != nil {
		return types.Value{}, err
	}
	out, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for {
		row, ok := h.next()
		if !ok {
			return out, nil
		}
		v, err := h.rowValue(z, row, mode)
		if err == nil {
			err = types.ArrayAppend(z, &out, v)
			types.ReleaseValue(z, &v)
		}
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
	}
}

func nativeFetchColumn(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_fetch_column", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	h, err := getRegistry(z).resultArg(z, "mysqli_fetch_column", args, 0)
	if err != nil {
		return types.Value{}, err
	}
	col := optInt(z, args, 1, 0)
	if col < 0 || col >= int64(len(h.rs.fields)) {
		return types.Value{}, ValueError{"mysqli_fetch_column(): Argument #2 ($column) must be greater than or equal to 0 and less than the number of fields"}
	}
	row, ok := h.next()
	if !ok {
		return types.NewBool(false), nil
	}
	return cellValue(z, row[col], h.rs.fields[col], h.native)
}

// fieldObject construit l'objet stdClass décrit par mysqli_fetch_field.
func fieldObject(z *zone.Zone, f field) (types.Value, error) {
	names := []string{"name", "orgname", "table", "orgtable", "def", "db", "catalog", "max_length", "length", "charsetnr", "flags", "type", "decimals"}
	strs := []string{f.name, f.orgname, f.table, f.orgtable, "", f.db, "def"}
	vals := make([]types.Value, 0, len(names))
	defer func() {
		for i := range vals {
			types.ReleaseValue(z, &vals[i])
		}
	}()
	for _, s := range strs {
		v, err := newStr(z, s)
		if err != nil {
			return types.Value{}, err
		}
		vals = append(vals, v)
	}
	vals = append(vals, types.NewLong(0), types.NewLong(int64(f.length)), types.NewLong(int64(f.charsetnr)),
		types.NewLong(int64(f.flags)), types.NewLong(int64(f.typ)), types.NewLong(int64(f.decimals)))
	return newStdObject(z, names, vals)
}

func nativeFetchField(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_fetch_field", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	h, err := getRegistry(z).resultArg(z, "mysqli_fetch_field", args, 0)
	if err != nil {
		return types.Value{}, err
	}
	if h.fieldCursor >= len(h.rs.fields) {
		return types.NewBool(false), nil
	}
	f := h.rs.fields[h.fieldCursor]
	h.fieldCursor++
	return fieldObject(z, f)
}

func nativeFetchFields(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_fetch_fields", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	h, err := getRegistry(z).resultArg(z, "mysqli_fetch_fields", args, 0)
	if err != nil {
		return types.Value{}, err
	}
	return fieldsArray(z, h.rs.fields)
}

func fieldsArray(z *zone.Zone, fields []field) (types.Value, error) {
	out, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, f := range fields {
		v, err := fieldObject(z, f)
		if err == nil {
			err = types.ArrayAppend(z, &out, v)
			types.ReleaseValue(z, &v)
		}
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
	}
	return out, nil
}

func nativeFetchFieldDirect(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_fetch_field_direct", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	h, err := getRegistry(z).resultArg(z, "mysqli_fetch_field_direct", args, 0)
	if err != nil {
		return types.Value{}, err
	}
	i := castInt(z, args[1])
	if i < 0 || i >= int64(len(h.rs.fields)) {
		return types.Value{}, ValueError{"mysqli_fetch_field_direct(): Argument #2 ($index) must be greater than or equal to 0 and less than the number of fields"}
	}
	return fieldObject(z, h.rs.fields[i])
}

func nativeFieldSeek(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_field_seek", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	h, err := getRegistry(z).resultArg(z, "mysqli_field_seek", args, 0)
	if err != nil {
		return types.Value{}, err
	}
	i := castInt(z, args[1])
	if i < 0 || i >= int64(len(h.rs.fields)) {
		return types.Value{}, ValueError{"mysqli_field_seek(): Argument #2 ($index) must be greater than or equal to 0 and less than the number of fields"}
	}
	h.fieldCursor = int(i)
	return types.NewBool(true), nil
}

func resultInt(name string, get func(h *resultHandle) int64) func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		h, err := getRegistry(z).resultArg(z, name, args, 0)
		if err != nil {
			return types.Value{}, err
		}
		return types.NewLong(get(h)), nil
	}
}

func nativeFetchLengths(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_fetch_lengths", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	h, err := getRegistry(z).resultArg(z, "mysqli_fetch_lengths", args, 0)
	if err != nil {
		return types.Value{}, err
	}
	return lengthsValue(z, h)
}

func lengthsValue(z *zone.Zone, h *resultHandle) (types.Value, error) {
	if h.lastRow == nil {
		return types.NewBool(false), nil
	}
	out, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, c := range h.lastRow {
		n := 0
		if c.kind != cellNull {
			n = len(c.text())
		}
		_ = types.ArrayAppend(z, &out, types.NewLong(int64(n)))
	}
	return out, nil
}

func nativeDataSeek(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_data_seek", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	h, err := getRegistry(z).resultArg(z, "mysqli_data_seek", args, 0)
	if err != nil {
		return types.Value{}, err
	}
	off := castInt(z, args[1])
	if off < 0 {
		return types.Value{}, ValueError{"mysqli_data_seek(): Argument #2 ($offset) must be greater than or equal to 0"}
	}
	if off >= int64(len(h.rs.rows)) {
		return types.NewBool(false), nil
	}
	h.cursor = int(off)
	return types.NewBool(true), nil
}

func nativeFreeResult(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mysqli_free_result", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	r := getRegistry(z)
	h, err := r.resultArg(z, "mysqli_free_result", args, 0)
	if err != nil {
		return types.Value{}, err
	}
	h.freed = true
	h.rs = nil
	delete(r.results, h.id)
	return types.NewNull(), nil
}
