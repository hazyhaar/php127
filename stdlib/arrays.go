package stdlib

import (
	"io"
	"math"
	"slices"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func nativeCount(name string) NativeFunc {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 1, 2); err != nil {
			return types.Value{}, err
		}
		v := deref(z, args[0])
		if v.Type != types.TypeArray {
			return types.Value{}, typeErr(name, "1", "value", "Countable|array", typeKind(z, v))
		}
		mode := int64(0)
		if len(args) == 2 {
			var err error
			mode, err = asIntParam(z, args[1], name, "2", "mode")
			if err != nil {
				return types.Value{}, err
			}
		}
		if mode == 1 {
			return types.NewLong(int64(countRecursive(z, v))), nil
		}
		return types.NewLong(int64(types.ArrayLen(z, v))), nil
	}
}

func countRecursive(z *zone.Zone, a types.Value) int {
	n := 0
	types.ArrayForEach(z, a, func(_, v types.Value) bool {
		n++
		if deref(z, v).Type == types.TypeArray {
			n += countRecursive(z, v)
		}
		return true
	})
	return n
}

func nativeArrayKeys(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_keys", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	a, err := asArray(z, args[0], "array_keys", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	keys, _ := arrayItems(z, a)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, k := range keys {
		stored := k
		if k.Type == types.TypeString {
			stored, err = newStr(z, types.StringData(z, k))
			if err != nil {
				return types.Value{}, err
			}
		}
		if err = appendKey(z, &out, stored); err != nil {
			return types.Value{}, err
		}
	}
	return out, nil
}

func nativeArrayValues(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_values", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	a, err := asArray(z, args[0], "array_values", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	_, vals := arrayItems(z, a)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, v := range vals {
		if err = appendKey(z, &out, v); err != nil {
			return types.Value{}, err
		}
	}
	return out, nil
}

func nativeKeyExists(name string) NativeFunc {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 2, 2); err != nil {
			return types.Value{}, err
		}
		a, err := asArray(z, args[1], name, "2", "array")
		if err != nil {
			return types.Value{}, err
		}
		ok, err := types.ArrayKeyExists(z, a, args[0], warnDrop)
		if err != nil {
			return types.Value{}, err
		}
		return types.NewBool(ok), nil
	}
}

func nativeInArray(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("in_array", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	hay, err := asArray(z, args[1], "in_array", "2", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	strict := false
	if len(args) == 3 {
		strict = asBool(z, args[2])
	}
	needle := deref(z, args[0])
	found := false
	types.ArrayForEach(z, hay, func(_, v types.Value) bool {
		v = deref(z, v)
		if strict {
			found = types.IdenticalZ(z, needle, v)
		} else {
			found = types.EqualZ(z, needle, v)
		}
		return !found
	})
	return types.NewBool(found), nil
}

func nativeArraySearch(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_search", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	hay, err := asArray(z, args[1], "array_search", "2", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	strict := false
	if len(args) == 3 {
		strict = asBool(z, args[2])
	}
	needle := deref(z, args[0])
	var key types.Value
	found := false
	types.ArrayForEach(z, hay, func(k, v types.Value) bool {
		v = deref(z, v)
		if strict {
			found = types.IdenticalZ(z, needle, v)
		} else {
			found = types.EqualZ(z, needle, v)
		}
		if found {
			key = k
			return false
		}
		return true
	})
	if !found {
		return types.NewBool(false), nil
	}
	if key.Type == types.TypeString {
		return newStr(z, types.StringData(z, key))
	}
	return key, nil
}

func nativeArrayMerge(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_merge", len(args), 0, -1); err != nil {
		return types.Value{}, err
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for i, a := range args {
		arr, err := asArray(z, a, "array_merge", itoa(i+1), "arrays")
		if err != nil {
			return types.Value{}, err
		}
		keys, vals := arrayItems(z, arr)
		for j := range keys {
			if keys[j].IsLong() {
				if err = appendKey(z, &out, vals[j]); err != nil {
					return types.Value{}, err
				}
			} else if err = setKey(z, &out, keys[j], vals[j]); err != nil {
				return types.Value{}, err
			}
		}
	}
	return out, nil
}

func nativeArraySlice(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_slice", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	a, err := asArray(z, args[0], "array_slice", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	off, err := asIntParam(z, args[1], "array_slice", "2", "offset")
	if err != nil {
		return types.Value{}, err
	}
	keys, vals := arrayItems(z, a)
	n := int64(len(vals))
	if off < 0 {
		off += n
		if off < 0 {
			off = 0
		}
	}
	if off > n {
		off = n
	}
	end := n
	if len(args) >= 3 && deref(z, args[2]).Type != types.TypeNull && !deref(z, args[2]).IsUndef() {
		length, err := asIntParam(z, args[2], "array_slice", "3", "length")
		if err != nil {
			return types.Value{}, err
		}
		if length < 0 {
			end = max(n+length, off)
		} else {
			end = min(off+length, n)
		}
	}
	preserve := false
	if len(args) == 4 {
		preserve = asBool(z, args[3])
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	next := int64(0)
	for i := off; i < end; i++ {
		k := keys[i]
		if preserve || !k.IsLong() {
			if err = setKey(z, &out, k, vals[i]); err != nil {
				return types.Value{}, err
			}
		} else {
			if err = setKey(z, &out, types.NewLong(next), vals[i]); err != nil {
				return types.Value{}, err
			}
			next++
		}
	}
	return out, nil
}

func nativeArrayReverse(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_reverse", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	a, err := asArray(z, args[0], "array_reverse", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	preserve := false
	if len(args) == 2 {
		preserve = asBool(z, args[1])
	}
	keys, vals := arrayItems(z, a)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	next := int64(0)
	for i, val := range slices.Backward(vals) {
		k := keys[i]
		if preserve || !k.IsLong() {
			if err = setKey(z, &out, k, val); err != nil {
				return types.Value{}, err
			}
		} else {
			if err = setKey(z, &out, types.NewLong(next), val); err != nil {
				return types.Value{}, err
			}
			next++
		}
	}
	return out, nil
}

func nativeArrayFill(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_fill", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	start, err := asIntParam(z, args[0], "array_fill", "1", "start_index")
	if err != nil {
		return types.Value{}, err
	}
	count, err := asIntParam(z, args[1], "array_fill", "2", "count")
	if err != nil {
		return types.Value{}, err
	}
	if count < 0 {
		return types.Value{}, ValueError{"array_fill(): Argument #2 ($count) must be greater than or equal to 0"}
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	val := deref(z, args[2])
	for i := range count {
		if err = setKey(z, &out, types.NewLong(start+i), val); err != nil {
			return types.Value{}, err
		}
	}
	return out, nil
}

func nativeArrayCombine(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_combine", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	keysA, err := asArray(z, args[0], "array_combine", "1", "keys")
	if err != nil {
		return types.Value{}, err
	}
	valsA, err := asArray(z, args[1], "array_combine", "2", "values")
	if err != nil {
		return types.Value{}, err
	}
	_, keys := arrayItems(z, keysA)
	_, vals := arrayItems(z, valsA)
	if len(keys) != len(vals) {
		return types.Value{}, ValueError{"array_combine(): Argument #1 ($keys) and argument #2 ($values) must have the same number of elements"}
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for i := range keys {
		if err = setKey(z, &out, keys[i], vals[i]); err != nil {
			return types.Value{}, err
		}
	}
	return out, nil
}

func nativeRange(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("range", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	start, end := deref(z, args[0]), deref(z, args[1])
	stepGiven := len(args) == 3
	stepV := types.NewLong(1)
	if stepGiven {
		stepV = deref(z, args[2])
	}
	if isCharRange(z, start, end) && !stepGiven {
		return charRange(z, start, end, 1)
	}
	sf, ef, step, asString, err := rangeNumbers(z, start, end, stepV, stepGiven)
	if err != nil {
		return types.Value{}, err
	}
	if step == 0 {
		return types.Value{}, ValueError{"range(): Argument #3 ($step) cannot be 0"}
	}
	if !stepGiven && sf > ef {
		step = -1
	}
	if (ef-sf)*step < 0 && stepGiven {
		return types.Value{}, ValueError{"range(): Argument #3 ($step) is invalid for this range"}
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	if asString && start.Type == types.TypeString && end.Type == types.TypeString && isSingleByteChar(z, start) && isSingleByteChar(z, end) && !isNumericString(types.StringData(z, start)) {
		return charRange(z, start, end, int(step))
	}
	cur := sf
	for range 100000 {
		var item types.Value
		if asString {
			item, err = newStr(z, formatRangeString(cur, start))
		} else if start.IsDouble() || end.IsDouble() || stepV.IsDouble() || step != math.Trunc(step) {
			item = types.NewDouble(cur)
		} else {
			item = types.NewLong(int64(cur))
		}
		if err != nil {
			return types.Value{}, err
		}
		if err = appendKey(z, &out, item); err != nil {
			return types.Value{}, err
		}
		next := cur + step
		if step > 0 && next > ef || step < 0 && next < ef || next == cur {
			break
		}
		cur = next
	}
	return out, nil
}

func isSingleByteChar(z *zone.Zone, v types.Value) bool {
	return v.Type == types.TypeString && len(types.StringData(z, v)) == 1
}

func isCharRange(z *zone.Zone, a, b types.Value) bool {
	if !isSingleByteChar(z, a) || !isSingleByteChar(z, b) {
		return false
	}
	as := types.StringData(z, a)
	bs := types.StringData(z, b)
	return !isNumericString(as) && !isNumericString(bs)
}

func charRange(z *zone.Zone, start, end types.Value, step int) (types.Value, error) {
	a := int(types.StringData(z, start)[0])
	b := int(types.StringData(z, end)[0])
	if step == 0 {
		return types.Value{}, ValueError{"range(): Argument #3 ($step) cannot be 0"}
	}
	if step > 0 && a > b {
		step = -step
	}
	if step < 0 && a < b {
		step = -step
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for c := a; ; c += step {
		sv, err := newStr(z, string([]byte{byte(c)}))
		if err != nil {
			return types.Value{}, err
		}
		if err = appendKey(z, &out, sv); err != nil {
			return types.Value{}, err
		}
		if c == b || (step > 0 && c > b) || (step < 0 && c < b) {
			break
		}
	}
	return out, nil
}

func rangeNumbers(z *zone.Zone, start, end, step types.Value, stepGiven bool) (float64, float64, float64, bool, error) {
	asStr := start.Type == types.TypeString && end.Type == types.TypeString
	sf, err := rangeFloat(z, start)
	if err != nil {
		return 0, 0, 0, false, err
	}
	ef, err := rangeFloat(z, end)
	if err != nil {
		return 0, 0, 0, false, err
	}
	st := 1.0
	if stepGiven {
		st, err = rangeFloat(z, step)
		if err != nil {
			return 0, 0, 0, false, err
		}
	}
	return sf, ef, st, asStr, nil
}

func rangeFloat(z *zone.Zone, v types.Value) (float64, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeLong:
		return float64(v.ToLong()), nil
	case types.TypeDouble:
		return v.ToDouble(), nil
	case types.TypeString:
		s := types.StringData(z, v)
		if nv, ok := numericValue(s); ok {
			if nv.IsLong() {
				return float64(nv.ToLong()), nil
			}
			return nv.ToDouble(), nil
		}
		if len(s) == 1 {
			return float64(s[0]), nil
		}
		return parseFloatPrefix(s), nil
	case types.TypeTrue:
		return 1, nil
	case types.TypeFalse, types.TypeNull:
		return 0, nil
	default:
		return 0, typeErr("range", "1", "start", "string|int|float", typeKind(z, v))
	}
}

func formatRangeString(f float64, sample types.Value) string {
	if f == math.Trunc(f) && !math.IsInf(f, 0) {
		return itoa64(int64(f))
	}
	return formatFloat(f, floatCast)
}

func itoa64(n int64) string {
	return itoa(int(n))
}
