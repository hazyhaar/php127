package stdlib

import (
	"io"
	"strconv"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func nativeVarDump(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("var_dump", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	var buf []byte
	for _, a := range args {
		buf = appendDump(buf, z, deref(z, a), 0)
	}
	writeOut(out, buf)
	return types.NewNull(), nil
}

func nativeVarExport(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("var_export", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	ret := false
	if len(args) == 2 {
		ret = asBool(z, args[1])
	}
	buf := appendExport(nil, z, deref(z, args[0]), 0)
	if ret {
		return newStr(z, string(buf))
	}
	writeOut(out, buf)
	return types.NewNull(), nil
}

func nativePrintR(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("print_r", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	ret := false
	if len(args) == 2 {
		ret = asBool(z, args[1])
	}
	buf := appendPrintR(nil, z, deref(z, args[0]), 0)
	if ret {
		return newStr(z, string(buf))
	}
	writeOut(out, buf)
	return types.NewBool(true), nil
}

func writeOut(out io.Writer, b []byte) {
	if out == nil || len(b) == 0 {
		return
	}
	_, _ = out.Write(b)
}

func appendDump(dst []byte, z *zone.Zone, v types.Value, indent int) []byte {
	pad := func(n int) []byte {
		if n <= 0 {
			return nil
		}
		p := make([]byte, n)
		for i := range p {
			p[i] = ' '
		}
		return p
	}
	dst = append(dst, pad(indent*2)...)
	if types.IsSharedReference(z, v) {
		dst = append(dst, '&')
	}
	v = deref(z, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		dst = append(dst, "NULL\n"...)
	case types.TypeTrue:
		dst = append(dst, "bool(true)\n"...)
	case types.TypeFalse:
		dst = append(dst, "bool(false)\n"...)
	case types.TypeLong:
		dst = append(dst, "int("...)
		dst = strconv.AppendInt(dst, v.ToLong(), 10)
		dst = append(dst, ")\n"...)
	case types.TypeDouble:
		dst = append(dst, "float("...)
		dst = append(dst, formatFloat(v.ToDouble(), floatDump)...)
		dst = append(dst, ")\n"...)
	case types.TypeString:
		s := types.StringData(z, v)
		dst = append(dst, "string("...)
		dst = strconv.AppendInt(dst, int64(len(s)), 10)
		dst = append(dst, ") \""...)
		dst = append(dst, s...)
		dst = append(dst, "\"\n"...)
	case types.TypeArray:
		keys, vals := arrayItems(z, v)
		dst = append(dst, "array("...)
		dst = strconv.AppendInt(dst, int64(len(vals)), 10)
		dst = append(dst, ") {\n"...)
		for i := range vals {
			dst = append(dst, pad(indent*2+2)...)
			dst = appendDumpKey(dst, z, keys[i])
			dst = append(dst, "=>\n"...)
			dst = appendDump(dst, z, vals[i], indent+1)
		}
		dst = append(dst, pad(indent*2)...)
		dst = append(dst, "}\n"...)
	case types.TypeObject:
		name := safeClass(z, v)
		if name == "" {
			name = "object"
		}
		dst = append(dst, "object("...)
		dst = append(dst, name...)
		dst = append(dst, ")#1 (0) {\n}\n"...)
	default:
		dst = append(dst, "UNKNOWN\n"...)
	}
	return dst
}

func appendDumpKey(dst []byte, z *zone.Zone, k types.Value) []byte {
	dst = append(dst, '[')
	if k.IsLong() {
		dst = strconv.AppendInt(dst, k.ToLong(), 10)
	} else {
		dst = append(dst, '"')
		if k.Type == types.TypeString {
			dst = append(dst, types.StringData(z, k)...)
		}
		dst = append(dst, '"')
	}
	dst = append(dst, ']')
	return dst
}

func appendExport(dst []byte, z *zone.Zone, v types.Value, indent int) []byte {
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		dst = append(dst, "NULL"...)
	case types.TypeTrue:
		dst = append(dst, "true"...)
	case types.TypeFalse:
		dst = append(dst, "false"...)
	case types.TypeLong:
		dst = strconv.AppendInt(dst, v.ToLong(), 10)
	case types.TypeDouble:
		dst = append(dst, formatFloat(v.ToDouble(), floatExport)...)
	case types.TypeString:
		dst = appendExportString(dst, types.StringData(z, v))
	case types.TypeArray:
		dst = append(dst, "array (\n"...)
		keys, vals := arrayItems(z, v)
		pad := indent + 2
		for i := range vals {
			dst = appendSpaces(dst, pad)
			if keys[i].IsLong() {
				dst = strconv.AppendInt(dst, keys[i].ToLong(), 10)
			} else if keys[i].Type == types.TypeString {
				dst = appendExportString(dst, types.StringData(z, keys[i]))
			}
			dst = append(dst, " => "...)
			dst = appendExport(dst, z, deref(z, vals[i]), pad)
			dst = append(dst, ",\n"...)
		}
		dst = appendSpaces(dst, indent)
		dst = append(dst, ')')
	default:
		dst = append(dst, "NULL"...)
	}
	return dst
}

func appendExportString(dst []byte, s string) []byte {
	dst = append(dst, '\'')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' || c == '\'' {
			dst = append(dst, '\\')
		}
		dst = append(dst, c)
	}
	dst = append(dst, '\'')
	return dst
}

func appendSpaces(dst []byte, n int) []byte {
	for range n {
		dst = append(dst, ' ')
	}
	return dst
}

func appendPrintR(dst []byte, z *zone.Zone, v types.Value, indent int) []byte {
	if v.Type == types.TypeArray {
		if indent == 0 {
			dst = append(dst, "Array\n"...)
		}
		dst = appendSpaces(dst, indent)
		dst = append(dst, "(\n"...)
		keys, vals := arrayItems(z, v)
		for i := range vals {
			dst = appendSpaces(dst, indent+4)
			dst = append(dst, '[')
			if keys[i].IsLong() {
				dst = strconv.AppendInt(dst, keys[i].ToLong(), 10)
			} else if keys[i].Type == types.TypeString {
				dst = append(dst, types.StringData(z, keys[i])...)
			}
			dst = append(dst, "] => "...)
			val := deref(z, vals[i])
			if val.Type == types.TypeArray {
				dst = append(dst, "Array\n"...)
				dst = appendPrintR(dst, z, val, indent+8)
			} else {
				s, err := castString(z, val)
				if err != nil {
					s = ""
				}
				dst = append(dst, s...)
				dst = append(dst, '\n')
			}
		}
		dst = appendSpaces(dst, indent)
		dst = append(dst, ")\n"...)
		return dst
	}
	s, err := castString(z, v)
	if err != nil {
		s = ""
	}
	dst = append(dst, s...)
	return dst
}

func nativeGettype(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("gettype", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	v := deref(z, args[0])
	var name string
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		name = "NULL"
	case types.TypeFalse, types.TypeTrue:
		name = "boolean"
	case types.TypeLong:
		name = "integer"
	case types.TypeDouble:
		name = "double"
	case types.TypeString:
		name = "string"
	case types.TypeArray:
		name = "array"
	case types.TypeObject:
		name = "object"
	case types.TypeResource:
		if _, ok := opaqueResourceClass(v); ok {
			name = "object"
		} else {
			name = "resource"
		}
	default:
		name = "unknown type"
	}
	return newStr(z, name)
}

func nativeGetDebugType(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("get_debug_type", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	v := deref(z, args[0])
	var name string
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		name = "null"
	case types.TypeFalse, types.TypeTrue:
		name = "bool"
	case types.TypeLong:
		name = "int"
	case types.TypeDouble:
		name = "float"
	case types.TypeString:
		name = "string"
	case types.TypeArray:
		name = "array"
	case types.TypeObject:
		name = safeClass(z, v)
		if name == "" {
			name = "object"
		}
	case types.TypeResource:
		if cls, ok := opaqueResourceClass(v); ok {
			name = cls
		} else {
			name = "resource"
		}
	default:
		name = "unknown"
	}
	return newStr(z, name)
}

func nativeIsNull(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_null", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	v := deref(z, args[0])
	return types.NewBool(v.IsNull() || v.IsUndef()), nil
}

func nativeIsBool(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_bool", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	return types.NewBool(deref(z, args[0]).IsBool()), nil
}

func nativeIsInt(name string) NativeFunc {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		return types.NewBool(deref(z, args[0]).IsLong()), nil
	}
}

func nativeIsFloat(name string) NativeFunc {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		return types.NewBool(deref(z, args[0]).IsDouble()), nil
	}
}

func nativeIsString(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_string", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	return types.NewBool(deref(z, args[0]).Type == types.TypeString), nil
}

func nativeIsArray(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_array", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	return types.NewBool(deref(z, args[0]).Type == types.TypeArray), nil
}

func nativeIsObject(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_object", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	v := deref(z, args[0])
	if v.Type == types.TypeObject {
		return types.NewBool(true), nil
	}
	_, ok := opaqueResourceClass(v)
	return types.NewBool(ok), nil
}

func nativeIsScalar(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_scalar", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	v := deref(z, args[0])
	switch v.Type {
	case types.TypeFalse, types.TypeTrue, types.TypeLong, types.TypeDouble, types.TypeString:
		return types.NewBool(true), nil
	default:
		return types.NewBool(false), nil
	}
}

func nativeIsNumeric(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_numeric", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	v := deref(z, args[0])
	switch v.Type {
	case types.TypeLong, types.TypeDouble:
		return types.NewBool(true), nil
	case types.TypeString:
		return types.NewBool(isNumericString(types.StringData(z, v))), nil
	default:
		return types.NewBool(false), nil
	}
}

func nativeIntval(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("intval", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	base := int64(10)
	var err error
	if len(args) == 2 {
		base, err = asIntParam(z, args[1], "intval", "2", "base")
		if err != nil {
			return types.Value{}, err
		}
	}
	v := deref(z, args[0])
	if v.Type == types.TypeString {
		return types.NewLong(parseIntVal(types.StringData(z, v), int(base))), nil
	}
	n, err := asIntLoose(z, v)
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(n), nil
}

func nativeFloatval(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("floatval", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	v := deref(z, args[0])
	switch v.Type {
	case types.TypeDouble:
		return v, nil
	case types.TypeLong:
		return types.NewDouble(float64(v.ToLong())), nil
	case types.TypeTrue:
		return types.NewDouble(1), nil
	case types.TypeFalse, types.TypeNull, types.TypeUndef:
		return types.NewDouble(0), nil
	case types.TypeString:
		return types.NewDouble(parseFloatPrefix(types.StringData(z, v))), nil
	case types.TypeArray:
		if types.ArrayLen(z, v) == 0 {
			return types.NewDouble(0), nil
		}
		return types.NewDouble(1), nil
	default:
		return types.Value{}, Error{"Object of class " + safeClass(z, v) + " could not be converted to float"}
	}
}

func nativeStrval(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strval", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := castString(z, args[0])
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, s)
}

func nativeBoolval(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("boolval", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	return types.NewBool(asBool(z, args[0])), nil
}
