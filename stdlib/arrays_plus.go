// Code créé par agent — Lot 4.3 : fonctions de tableaux PHP 8.3 complémentaires.
package stdlib

import (
	"cmp"
	"io"
	"math/rand/v2"
	"slices"
	"strings"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	caseLower    = 0
	caseUpper    = 1
	sortAsc      = 4
	sortDesc     = 3
	sortRegular  = 0
	sortNumeric  = 1
	sortString   = 2
	sortNatural  = 6
	sortFlagCase = 8
)

type kvItem struct {
	key types.Value
	val types.Value
}

func collectItems(z *zone.Zone, a types.Value) []kvItem {
	keys, vals := arrayItems(z, a)
	out := make([]kvItem, len(keys))
	for i := range keys {
		out[i] = kvItem{key: keys[i], val: vals[i]}
	}
	return out
}

func replaceArrayInPlace(z *zone.Zone, target types.Value, replacement types.Value) {
	if target.Type == types.TypeReference {
		types.AssignRef(z, target, replacement)
		types.ReleaseValue(z, &replacement)
		return
	}
	dst := deref(z, target)
	if dst.Type != types.TypeArray || replacement.Type != types.TypeArray || dst.DataSlot == replacement.DataSlot {
		types.ReleaseValue(z, &replacement)
		return
	}
	hdrSize := uint32(unsafe.Sizeof(types.ArrayHeader{}))
	bSize := uint32(unsafe.Sizeof(types.Bucket{}))
	dstBytes, err := z.Bytes(dst.DataSlot, hdrSize)
	if err != nil {
		types.ReleaseValue(z, &replacement)
		return
	}
	srcBytes, err := z.Bytes(replacement.DataSlot, hdrSize)
	if err != nil {
		types.ReleaseValue(z, &replacement)
		return
	}
	dstHdr := (*types.ArrayHeader)(unsafe.Pointer(&dstBytes[0]))
	srcHdr := (*types.ArrayHeader)(unsafe.Pointer(&srcBytes[0]))

	dstB0, err1 := z.Bytes(dstHdr.DataOffset, bSize)
	srcB0, err2 := z.Bytes(srcHdr.DataOffset, bSize)
	if err1 == nil && err2 == nil {
		db := (*types.Bucket)(unsafe.Pointer(&dstB0[0]))
		sb := (*types.Bucket)(unsafe.Pointer(&srcB0[0]))
		db.Pad, sb.Pad = sb.Pad, db.Pad
	}

	dstHdr.Kind, srcHdr.Kind = srcHdr.Kind, dstHdr.Kind
	dstHdr.Len, srcHdr.Len = srcHdr.Len, dstHdr.Len
	dstHdr.Used, srcHdr.Used = srcHdr.Used, dstHdr.Used
	dstHdr.Cap, srcHdr.Cap = srcHdr.Cap, dstHdr.Cap
	dstHdr.DataOffset, srcHdr.DataOffset = srcHdr.DataOffset, dstHdr.DataOffset
	dstHdr.NextIdx, srcHdr.NextIdx = srcHdr.NextIdx, dstHdr.NextIdx

	types.ReleaseValue(z, &replacement)
}

func keyEq(z *zone.Zone, a, b types.Value) bool {
	if a.Type != b.Type {
		return false
	}
	if a.IsLong() {
		return a.ToLong() == b.ToLong()
	}
	return types.StringData(z, a) == types.StringData(z, b)
}

func nativeArrayChangeKeyCase(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_change_key_case", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_change_key_case", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	c := int64(caseLower)
	if len(args) == 2 {
		c, err = asIntParam(z, args[1], "array_change_key_case", "2", "case")
		if err != nil {
			return types.Value{}, err
		}
	}
	keys, vals := arrayItems(z, arr)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for i, k := range keys {
		if k.IsLong() {
			err = setKey(z, &out, k, vals[i])
		} else {
			ks := types.StringData(z, k)
			if c == caseUpper {
				ks = strings.ToUpper(ks)
			} else {
				ks = strings.ToLower(ks)
			}
			kv, errStr := newStr(z, ks)
			if errStr != nil {
				return types.Value{}, errStr
			}
			err = setKey(z, &out, kv, vals[i])
			types.ReleaseValue(z, &kv)
		}
		if err != nil {
			return types.Value{}, err
		}
	}
	return out, nil
}

func extractColumnField(z *zone.Zone, row types.Value, key types.Value) (types.Value, bool) {
	row = deref(z, row)
	key = deref(z, key)
	if row.Type == types.TypeArray {
		v, ok, err := types.ArrayGet(z, row, key, warnDrop)
		if err != nil || !ok {
			return types.Value{}, false
		}
		return v, true
	}
	if row.Type == types.TypeObject {
		props := objectProps(z, row)
		ks, err := castString(z, key)
		if err != nil {
			return types.Value{}, false
		}
		for _, p := range props {
			if p.key == ks {
				return p.val, true
			}
		}
		return types.Value{}, false
	}
	return types.Value{}, false
}

func nativeArrayColumn(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_column", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_column", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	colKey := deref(z, args[1])
	hasIdxKey := false
	var idxKey types.Value
	if len(args) == 3 && !deref(z, args[2]).IsNull() && !deref(z, args[2]).IsUndef() {
		hasIdxKey = true
		idxKey = deref(z, args[2])
	}
	_, rows := arrayItems(z, arr)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, row := range rows {
		var val types.Value
		if colKey.IsNull() || colKey.IsUndef() {
			val = row
		} else {
			v, ok := extractColumnField(z, row, colKey)
			if !ok {
				continue
			}
			val = v
		}
		if hasIdxKey {
			kv, ok := extractColumnField(z, row, idxKey)
			kv = deref(z, kv)
			if ok && !kv.IsNull() && !kv.IsUndef() {
				if kv.IsLong() || kv.Type == types.TypeString || kv.IsBool() {
					if err = setKey(z, &out, kv, val); err != nil {
						return types.Value{}, err
					}
				} else {
					ks, errCast := castString(z, kv)
					if errCast != nil {
						return types.Value{}, errCast
					}
					kStr, errStr := newStr(z, ks)
					if errStr != nil {
						return types.Value{}, errStr
					}
					err = setKey(z, &out, kStr, val)
					types.ReleaseValue(z, &kStr)
					if err != nil {
						return types.Value{}, err
					}
				}
				continue
			}
		}
		if err = appendKey(z, &out, val); err != nil {
			return types.Value{}, err
		}
	}
	return out, nil
}

func nativeArrayCountValues(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_count_values", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_count_values", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	_, vals := arrayItems(z, arr)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, rawV := range vals {
		v := deref(z, rawV)
		if v.IsLong() || v.Type == types.TypeString {
			cur, ok, err := types.ArrayGet(z, out, v, warnDrop)
			if err != nil {
				return types.Value{}, err
			}
			cnt := int64(1)
			if ok {
				cnt = cur.ToLong() + 1
			}
			if err := setKey(z, &out, v, types.NewLong(cnt)); err != nil {
				return types.Value{}, err
			}
		}
	}
	return out, nil
}

func nativeArrayDiffAssoc(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_diff_assoc", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	base, err := asArray(z, args[0], "array_diff_assoc", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	others := make([][]kvItem, len(args)-1)
	for i := 1; i < len(args); i++ {
		a, err := asArray(z, args[i], "array_diff_assoc", itoa(i+1), "arrays")
		if err != nil {
			return types.Value{}, err
		}
		others[i-1] = collectItems(z, a)
	}
	baseItems := collectItems(z, base)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, it := range baseItems {
		s1, err := castString(z, it.val)
		if err != nil {
			return types.Value{}, err
		}
		found := false
		for _, o := range others {
			for _, oit := range o {
				if keyEq(z, it.key, oit.key) {
					s2, err := castString(z, oit.val)
					if err != nil {
						return types.Value{}, err
					}
					if s1 == s2 {
						found = true
						break
					}
				}
			}
			if found {
				break
			}
		}
		if !found {
			if err = setKey(z, &out, it.key, it.val); err != nil {
				return types.Value{}, err
			}
		}
	}
	return out, nil
}

func nativeArrayDiffKey(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_diff_key", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	base, err := asArray(z, args[0], "array_diff_key", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	others := make([]types.Value, len(args)-1)
	for i := 1; i < len(args); i++ {
		a, err := asArray(z, args[i], "array_diff_key", itoa(i+1), "arrays")
		if err != nil {
			return types.Value{}, err
		}
		others[i-1] = a
	}
	baseItems := collectItems(z, base)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, it := range baseItems {
		found := false
		for _, o := range others {
			ok, err := types.ArrayKeyExists(z, o, it.key, warnDrop)
			if err != nil {
				return types.Value{}, err
			}
			if ok {
				found = true
				break
			}
		}
		if !found {
			if err = setKey(z, &out, it.key, it.val); err != nil {
				return types.Value{}, err
			}
		}
	}
	return out, nil
}

func nativeArrayIntersectAssoc(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_intersect_assoc", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	base, err := asArray(z, args[0], "array_intersect_assoc", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	others := make([]types.Value, len(args)-1)
	for i := 1; i < len(args); i++ {
		a, err := asArray(z, args[i], "array_intersect_assoc", itoa(i+1), "arrays")
		if err != nil {
			return types.Value{}, err
		}
		others[i-1] = a
	}
	baseItems := collectItems(z, base)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, it := range baseItems {
		s1, err := castString(z, it.val)
		if err != nil {
			return types.Value{}, err
		}
		inAll := true
		for _, o := range others {
			ov, ok, err := types.ArrayGet(z, o, it.key, warnDrop)
			if err != nil {
				return types.Value{}, err
			}
			if !ok {
				inAll = false
				break
			}
			s2, err := castString(z, ov)
			if err != nil {
				return types.Value{}, err
			}
			if s1 != s2 {
				inAll = false
				break
			}
		}
		if inAll {
			if err = setKey(z, &out, it.key, it.val); err != nil {
				return types.Value{}, err
			}
		}
	}
	return out, nil
}

func nativeArrayIntersectKey(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_intersect_key", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	base, err := asArray(z, args[0], "array_intersect_key", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	others := make([]types.Value, len(args)-1)
	for i := 1; i < len(args); i++ {
		a, err := asArray(z, args[i], "array_intersect_key", itoa(i+1), "arrays")
		if err != nil {
			return types.Value{}, err
		}
		others[i-1] = a
	}
	baseItems := collectItems(z, base)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, it := range baseItems {
		inAll := true
		for _, o := range others {
			ok, err := types.ArrayKeyExists(z, o, it.key, warnDrop)
			if err != nil {
				return types.Value{}, err
			}
			if !ok {
				inAll = false
				break
			}
		}
		if inAll {
			if err = setKey(z, &out, it.key, it.val); err != nil {
				return types.Value{}, err
			}
		}
	}
	return out, nil
}

func toSortFloat(z *zone.Zone, v types.Value) float64 {
	v = deref(z, v)
	switch v.Type {
	case types.TypeLong:
		return float64(v.ToLong())
	case types.TypeDouble:
		return v.ToDouble()
	case types.TypeTrue:
		return 1
	case types.TypeFalse, types.TypeNull, types.TypeUndef:
		return 0
	case types.TypeString:
		return parseFloatPrefix(types.StringData(z, v))
	default:
		return 0
	}
}

func compareWithFlags(z *zone.Zone, a, b types.Value, flags int64) int {
	fold := (flags & sortFlagCase) != 0
	mode := flags &^ sortFlagCase
	switch mode {
	case sortNumeric:
		return cmp.Compare(toSortFloat(z, a), toSortFloat(z, b))
	case sortString:
		sa, _ := castString(z, a)
		sb, _ := castString(z, b)
		if fold {
			sa = strings.ToLower(sa)
			sb = strings.ToLower(sb)
		}
		return strings.Compare(sa, sb)
	case sortNatural:
		sa, _ := castString(z, a)
		sb, _ := castString(z, b)
		return strnatcmpGo(sa, sb, fold)
	default:
		return phpCmp(z, a, b)
	}
}

func nativeArrayMultisort(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_multisort", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	type sortCol struct {
		argIdx int
		items  []kvItem
		order  int64
		flags  int64
	}
	var cols []sortCol
	i := 0
	for i < len(args) {
		v := deref(z, args[i])
		if v.Type != types.TypeArray {
			return types.Value{}, typeErr("array_multisort", itoa(i+1), "array", "array", typeKind(z, v))
		}
		col := sortCol{
			argIdx: i,
			items:  collectItems(z, v),
			order:  sortAsc,
			flags:  sortRegular,
		}
		i++
		for range 2 {
			if i < len(args) {
				fv := deref(z, args[i])
				if fv.IsLong() {
					iv := fv.ToLong()
					if iv == sortAsc || iv == sortDesc {
						col.order = iv
					} else {
						col.flags = iv
					}
					i++
				} else {
					break
				}
			}
		}
		cols = append(cols, col)
	}
	if len(cols) == 0 {
		return types.NewBool(true), nil
	}
	n := len(cols[0].items)
	for _, c := range cols[1:] {
		if len(c.items) != n {
			return types.Value{}, valErr("array_multisort", "1", "array", "Array sizes are inconsistent")
		}
	}
	perm := make([]int, n)
	for idx := range n {
		perm[idx] = idx
	}
	slices.SortStableFunc(perm, func(a, b int) int {
		for _, c := range cols {
			res := compareWithFlags(z, c.items[a].val, c.items[b].val, c.flags)
			if c.order == sortDesc {
				res = -res
			}
			if res != 0 {
				return res
			}
		}
		return 0
	})
	for _, c := range cols {
		rebuilt, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		for _, p := range perm {
			it := c.items[p]
			if !it.key.IsLong() {
				err = setKey(z, &rebuilt, it.key, it.val)
			} else {
				err = appendKey(z, &rebuilt, it.val)
			}
			if err != nil {
				return types.Value{}, err
			}
		}
		replaceArrayInPlace(z, args[c.argIdx], rebuilt)
	}
	return types.NewBool(true), nil
}

func nativeArrayProduct(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_product", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_product", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	_, vals := arrayItems(z, arr)
	intProd := int64(1)
	floatProd := float64(1)
	isFloat := false
	for _, rawV := range vals {
		v := deref(z, rawV)
		var iv int64
		var fv float64
		itemFloat := false
		switch v.Type {
		case types.TypeLong:
			iv = v.ToLong()
			fv = float64(iv)
		case types.TypeDouble:
			fv = v.ToDouble()
			itemFloat = true
		case types.TypeTrue:
			iv = 1
			fv = 1
		case types.TypeFalse, types.TypeNull, types.TypeUndef:
			iv = 0
			fv = 0
		case types.TypeString:
			if nv, ok := numericValue(types.StringData(z, v)); ok {
				if nv.IsLong() {
					iv = nv.ToLong()
					fv = float64(iv)
				} else {
					fv = nv.ToDouble()
					itemFloat = true
				}
			} else {
				iv = 0
				fv = 0
			}
		default:
			iv = 0
			fv = 0
		}
		if !isFloat && !itemFloat {
			prod := float64(intProd) * float64(iv)
			if prod >= -9e18 && prod <= 9e18 {
				intProd *= iv
				floatProd = float64(intProd)
				continue
			}
		}
		isFloat = true
		floatProd *= fv
	}
	if isFloat {
		return types.NewDouble(floatProd), nil
	}
	return types.NewLong(intProd), nil
}

func nativeArrayRand(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_rand", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_rand", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	keys, _ := arrayItems(z, arr)
	if len(keys) == 0 {
		return types.Value{}, valErr("array_rand", "1", "array", "cannot be empty")
	}
	num := int64(1)
	if len(args) == 2 {
		num, err = asIntParam(z, args[1], "array_rand", "2", "num")
		if err != nil {
			return types.Value{}, err
		}
		if num < 1 || num > int64(len(keys)) {
			return types.Value{}, valErr("array_rand", "2", "num", "must be between 1 and the number of elements in argument #1 ($array)")
		}
	}
	cloneKey := func(k types.Value) (types.Value, error) {
		if k.Type == types.TypeString {
			return newStr(z, types.StringData(z, k))
		}
		return k, nil
	}
	if num == 1 {
		idx := rand.IntN(len(keys))
		return cloneKey(keys[idx])
	}
	perm := rand.Perm(len(keys))[:num]
	slices.Sort(perm)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for i, idx := range perm {
		kv, err := cloneKey(keys[idx])
		if err != nil {
			return types.Value{}, err
		}
		err = setKey(z, &out, types.NewLong(int64(i)), kv)
		types.ReleaseValue(z, &kv)
		if err != nil {
			return types.Value{}, err
		}
	}
	return out, nil
}

func nativeArrayReplace(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_replace", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for i, arg := range args {
		a, err := asArray(z, arg, "array_replace", itoa(i+1), "array")
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		for _, it := range collectItems(z, a) {
			if err := setKey(z, &out, it.key, it.val); err != nil {
				return types.Value{}, err
			}
		}
	}
	return out, nil
}

func replaceRecursiveTwo(z *zone.Zone, dst, src types.Value) (types.Value, error) {
	for _, it := range collectItems(z, src) {
		val := deref(z, it.val)
		existing, ok, err := types.ArrayGet(z, dst, it.key, warnDrop)
		if err != nil {
			return types.Value{}, err
		}
		if ok {
			exDeref := deref(z, existing)
			if exDeref.Type == types.TypeArray && val.Type == types.TypeArray {
				baseCopy := types.RetainValue(z, exDeref)
				merged, err := replaceRecursiveTwo(z, baseCopy, val)
				if err != nil {
					types.ReleaseValue(z, &baseCopy)
					return types.Value{}, err
				}
				err = setKey(z, &dst, it.key, merged)
				types.ReleaseValue(z, &merged)
				if err != nil {
					return types.Value{}, err
				}
				continue
			}
		}
		if err := setKey(z, &dst, it.key, val); err != nil {
			return types.Value{}, err
		}
	}
	return dst, nil
}

func nativeArrayReplaceRecursive(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_replace_recursive", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for i, arg := range args {
		a, err := asArray(z, arg, "array_replace_recursive", itoa(i+1), "array")
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		out, err = replaceRecursiveTwo(z, out, a)
		if err != nil {
			return types.Value{}, err
		}
	}
	return out, nil
}

func mergeRecursiveTwo(z *zone.Zone, dst, src types.Value) (types.Value, error) {
	for _, it := range collectItems(z, src) {
		val := deref(z, it.val)
		if it.key.IsLong() {
			if err := appendKey(z, &dst, val); err != nil {
				return types.Value{}, err
			}
			continue
		}
		existing, ok, err := types.ArrayGet(z, dst, it.key, warnDrop)
		if err != nil {
			return types.Value{}, err
		}
		if ok {
			exDeref := deref(z, existing)
			var baseArr types.Value
			if exDeref.Type == types.TypeArray {
				baseArr = types.RetainValue(z, exDeref)
			} else {
				baseArr, err = newArray(z)
				if err != nil {
					return types.Value{}, err
				}
				if err := appendKey(z, &baseArr, exDeref); err != nil {
					return types.Value{}, err
				}
			}
			if val.Type == types.TypeArray {
				baseArr, err = mergeRecursiveTwo(z, baseArr, val)
			} else {
				err = appendKey(z, &baseArr, val)
			}
			if err != nil {
				types.ReleaseValue(z, &baseArr)
				return types.Value{}, err
			}
			err = setKey(z, &dst, it.key, baseArr)
			types.ReleaseValue(z, &baseArr)
			if err != nil {
				return types.Value{}, err
			}
		} else {
			if err := setKey(z, &dst, it.key, val); err != nil {
				return types.Value{}, err
			}
		}
	}
	return dst, nil
}

func nativeArrayMergeRecursive(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for i, arg := range args {
		a, err := asArray(z, arg, "array_merge_recursive", itoa(i+1), "arrays")
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		out, err = mergeRecursiveTwo(z, out, a)
		if err != nil {
			return types.Value{}, err
		}
	}
	return out, nil
}

func natSortCommon(z *zone.Zone, name string, args []types.Value, foldCase bool) (types.Value, error) {
	if err := arity(name, len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], name, "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	items := collectItems(z, arr)
	slices.SortStableFunc(items, func(a, b kvItem) int {
		sa, _ := castString(z, a.val)
		sb, _ := castString(z, b.val)
		return strnatcmpGo(sa, sb, foldCase)
	})
	rebuilt, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, it := range items {
		if err := setKey(z, &rebuilt, it.key, it.val); err != nil {
			return types.Value{}, err
		}
	}
	replaceArrayInPlace(z, args[0], rebuilt)
	return types.NewBool(true), nil
}

func nativeNatsort(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return natSortCommon(z, "natsort", args, false)
}

func nativeNatcasesort(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return natSortCommon(z, "natcasesort", args, true)
}

func nativeShuffle(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("shuffle", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "shuffle", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	_, vals := arrayItems(z, arr)
	rand.Shuffle(len(vals), func(i, j int) {
		vals[i], vals[j] = vals[j], vals[i]
	})
	rebuilt, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for i, v := range vals {
		if err := setKey(z, &rebuilt, types.NewLong(int64(i)), v); err != nil {
			return types.Value{}, err
		}
	}
	replaceArrayInPlace(z, args[0], rebuilt)
	return types.NewBool(true), nil
}

func getArrayCursor(z *zone.Zone, a types.Value) int {
	a = deref(z, a)
	if a.Type != types.TypeArray {
		return 0
	}
	hdrSize := uint32(unsafe.Sizeof(types.ArrayHeader{}))
	bSize := uint32(unsafe.Sizeof(types.Bucket{}))
	b, err := z.Bytes(a.DataSlot, hdrSize)
	if err != nil {
		return 0
	}
	hdr := (*types.ArrayHeader)(unsafe.Pointer(&b[0]))
	if hdr.Cap < 2 {
		return 0
	}
	b1, err := z.Bytes(hdr.DataOffset+bSize, bSize)
	if err != nil {
		return 0
	}
	bucket1 := (*types.Bucket)(unsafe.Pointer(&b1[0]))
	return int(int32(bucket1.Pad))
}

func setArrayCursor(z *zone.Zone, a types.Value, pos int) {
	a = deref(z, a)
	if a.Type != types.TypeArray {
		return
	}
	hdrSize := uint32(unsafe.Sizeof(types.ArrayHeader{}))
	bSize := uint32(unsafe.Sizeof(types.Bucket{}))
	b, err := z.Bytes(a.DataSlot, hdrSize)
	if err != nil {
		return
	}
	hdr := (*types.ArrayHeader)(unsafe.Pointer(&b[0]))
	if hdr.Cap < 2 {
		return
	}
	b1, err := z.Bytes(hdr.DataOffset+bSize, bSize)
	if err != nil {
		return
	}
	bucket1 := (*types.Bucket)(unsafe.Pointer(&b1[0]))
	bucket1.Pad = uint32(int32(pos))
}

func cloneArrayValue(z *zone.Zone, v types.Value) (types.Value, error) {
	v = deref(z, v)
	if v.Type == types.TypeString {
		return newStr(z, types.StringData(z, v))
	}
	return types.RetainValue(z, v), nil
}

func nativeCurrent(name string) NativeFunc {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		arr, err := asArray(z, args[0], name, "1", "array")
		if err != nil {
			return types.Value{}, err
		}
		_, vals := arrayItems(z, arr)
		pos := getArrayCursor(z, arr)
		if pos < 0 || pos >= len(vals) {
			return types.NewBool(false), nil
		}
		return cloneArrayValue(z, vals[pos])
	}
}

func nativeKey(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("key", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "key", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	keys, _ := arrayItems(z, arr)
	pos := getArrayCursor(z, arr)
	if pos < 0 || pos >= len(keys) {
		return types.NewNull(), nil
	}
	return cloneArrayValue(z, keys[pos])
}

func nativeReset(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("reset", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "reset", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	_, vals := arrayItems(z, arr)
	setArrayCursor(z, arr, 0)
	if len(vals) == 0 {
		return types.NewBool(false), nil
	}
	return cloneArrayValue(z, vals[0])
}

func nativeEnd(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("end", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "end", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	_, vals := arrayItems(z, arr)
	if len(vals) == 0 {
		setArrayCursor(z, arr, 0)
		return types.NewBool(false), nil
	}
	last := len(vals) - 1
	setArrayCursor(z, arr, last)
	return cloneArrayValue(z, vals[last])
}

func nativeNext(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("next", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "next", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	_, vals := arrayItems(z, arr)
	pos := min(getArrayCursor(z, arr)+1, len(vals))
	setArrayCursor(z, arr, pos)
	if pos < 0 || pos >= len(vals) {
		return types.NewBool(false), nil
	}
	return cloneArrayValue(z, vals[pos])
}

func nativePrev(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("prev", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "prev", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	_, vals := arrayItems(z, arr)
	pos := max(getArrayCursor(z, arr)-1, -1)
	setArrayCursor(z, arr, pos)
	if pos < 0 || pos >= len(vals) {
		return types.NewBool(false), nil
	}
	return cloneArrayValue(z, vals[pos])
}

func nativeArraySum(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_sum", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_sum", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	_, vals := arrayItems(z, arr)
	intSum := int64(0)
	floatSum := float64(0)
	isFloat := false
	for _, rawV := range vals {
		v := deref(z, rawV)
		var iv int64
		var fv float64
		itemFloat := false
		switch v.Type {
		case types.TypeLong:
			iv = v.ToLong()
			fv = float64(iv)
		case types.TypeDouble:
			fv = v.ToDouble()
			itemFloat = true
		case types.TypeTrue:
			iv = 1
			fv = 1
		case types.TypeFalse, types.TypeNull, types.TypeUndef:
			iv = 0
			fv = 0
		case types.TypeString:
			if nv, ok := numericValue(types.StringData(z, v)); ok {
				if nv.IsLong() {
					iv = nv.ToLong()
					fv = float64(iv)
				} else {
					fv = nv.ToDouble()
					itemFloat = true
				}
			}
		default:
		}
		if !isFloat && !itemFloat {
			sum := float64(intSum) + float64(iv)
			if sum >= -9e18 && sum <= 9e18 {
				intSum += iv
				floatSum = float64(intSum)
				continue
			}
		}
		isFloat = true
		floatSum += fv
	}
	if isFloat {
		return types.NewDouble(floatSum), nil
	}
	return types.NewLong(intSum), nil
}

func nativeArrayPop(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_pop", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_pop", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	items := collectItems(z, arr)
	if len(items) == 0 {
		return types.NewNull(), nil
	}
	lastVal, err := cloneArrayValue(z, items[len(items)-1].val)
	if err != nil {
		return types.Value{}, err
	}
	rebuilt, err := newArray(z)
	if err != nil {
		types.ReleaseValue(z, &lastVal)
		return types.Value{}, err
	}
	for _, it := range items[:len(items)-1] {
		if err := setKey(z, &rebuilt, it.key, it.val); err != nil {
			types.ReleaseValue(z, &lastVal)
			types.ReleaseValue(z, &rebuilt)
			return types.Value{}, err
		}
	}
	replaceArrayInPlace(z, args[0], rebuilt)
	return lastVal, nil
}

func nativeArrayPush(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_push", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_push", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	items := collectItems(z, arr)
	if len(args) == 1 {
		return types.NewLong(int64(len(items))), nil
	}
	rebuilt, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, it := range items {
		if err := setKey(z, &rebuilt, it.key, it.val); err != nil {
			types.ReleaseValue(z, &rebuilt)
			return types.Value{}, err
		}
	}
	for _, extra := range args[1:] {
		if err := appendKey(z, &rebuilt, deref(z, extra)); err != nil {
			types.ReleaseValue(z, &rebuilt)
			return types.Value{}, err
		}
	}
	newLen := int64(types.ArrayLen(z, rebuilt))
	replaceArrayInPlace(z, args[0], rebuilt)
	return types.NewLong(newLen), nil
}

func nativeArrayShift(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_shift", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_shift", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	items := collectItems(z, arr)
	if len(items) == 0 {
		return types.NewNull(), nil
	}
	firstVal, err := cloneArrayValue(z, items[0].val)
	if err != nil {
		return types.Value{}, err
	}
	rebuilt, err := newArray(z)
	if err != nil {
		types.ReleaseValue(z, &firstVal)
		return types.Value{}, err
	}
	nextIdx := int64(0)
	for _, it := range items[1:] {
		if it.key.IsLong() {
			err = setKey(z, &rebuilt, types.NewLong(nextIdx), it.val)
			nextIdx++
		} else {
			err = setKey(z, &rebuilt, it.key, it.val)
		}
		if err != nil {
			types.ReleaseValue(z, &firstVal)
			types.ReleaseValue(z, &rebuilt)
			return types.Value{}, err
		}
	}
	replaceArrayInPlace(z, args[0], rebuilt)
	return firstVal, nil
}

func nativeArrayUnshift(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_unshift", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_unshift", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	items := collectItems(z, arr)
	if len(args) == 1 {
		return types.NewLong(int64(len(items))), nil
	}
	rebuilt, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	nextIdx := int64(0)
	for _, extra := range args[1:] {
		if err := setKey(z, &rebuilt, types.NewLong(nextIdx), deref(z, extra)); err != nil {
			types.ReleaseValue(z, &rebuilt)
			return types.Value{}, err
		}
		nextIdx++
	}
	for _, it := range items {
		if it.key.IsLong() {
			err = setKey(z, &rebuilt, types.NewLong(nextIdx), it.val)
			nextIdx++
		} else {
			err = setKey(z, &rebuilt, it.key, it.val)
		}
		if err != nil {
			types.ReleaseValue(z, &rebuilt)
			return types.Value{}, err
		}
	}
	newLen := int64(types.ArrayLen(z, rebuilt))
	replaceArrayInPlace(z, args[0], rebuilt)
	return types.NewLong(newLen), nil
}

func nativeArraySplice(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_splice", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_splice", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	off64, err := asIntParam(z, args[1], "array_splice", "2", "offset")
	if err != nil {
		return types.Value{}, err
	}
	items := collectItems(z, arr)
	n := int64(len(items))
	off := off64
	if off < 0 {
		off = max(n+off, 0)
	} else if off > n {
		off = n
	}
	end := n
	if len(args) >= 3 && !deref(z, args[2]).IsNull() && !deref(z, args[2]).IsUndef() {
		l64, err := asIntParam(z, args[2], "array_splice", "3", "length")
		if err != nil {
			return types.Value{}, err
		}
		if l64 < 0 {
			end = max(n+l64, off)
		} else {
			end = min(off+l64, n)
		}
	}
	var replVals []types.Value
	if len(args) == 4 {
		r := deref(z, args[3])
		if r.Type == types.TypeArray {
			_, replVals = arrayItems(z, r)
		} else if !r.IsNull() && !r.IsUndef() {
			replVals = []types.Value{r}
		}
	}
	removed, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	remIdx := int64(0)
	for i := off; i < end; i++ {
		it := items[i]
		if it.key.IsLong() {
			err = setKey(z, &removed, types.NewLong(remIdx), it.val)
			remIdx++
		} else {
			err = setKey(z, &removed, it.key, it.val)
		}
		if err != nil {
			types.ReleaseValue(z, &removed)
			return types.Value{}, err
		}
	}
	rebuilt, err := newArray(z)
	if err != nil {
		types.ReleaseValue(z, &removed)
		return types.Value{}, err
	}
	nextIdx := int64(0)
	appendReindexed := func(it kvItem) error {
		if it.key.IsLong() {
			e := setKey(z, &rebuilt, types.NewLong(nextIdx), it.val)
			nextIdx++
			return e
		}
		return setKey(z, &rebuilt, it.key, it.val)
	}
	for i := int64(0); i < off; i++ {
		if err := appendReindexed(items[i]); err != nil {
			types.ReleaseValue(z, &removed)
			types.ReleaseValue(z, &rebuilt)
			return types.Value{}, err
		}
	}
	for _, rv := range replVals {
		if err := setKey(z, &rebuilt, types.NewLong(nextIdx), deref(z, rv)); err != nil {
			types.ReleaseValue(z, &removed)
			types.ReleaseValue(z, &rebuilt)
			return types.Value{}, err
		}
		nextIdx++
	}
	for i := end; i < n; i++ {
		if err := appendReindexed(items[i]); err != nil {
			types.ReleaseValue(z, &removed)
			types.ReleaseValue(z, &rebuilt)
			return types.Value{}, err
		}
	}
	replaceArrayInPlace(z, args[0], rebuilt)
	return removed, nil
}

func nativeArrayUnique(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_unique", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_unique", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	flags := int64(sortString)
	if len(args) == 2 {
		flags, err = asIntParam(z, args[1], "array_unique", "2", "flags")
		if err != nil {
			return types.Value{}, err
		}
	}
	items := collectItems(z, arr)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	var seen []types.Value
	for _, it := range items {
		dup := false
		for _, sv := range seen {
			if compareWithFlags(z, it.val, sv, flags) == 0 {
				dup = true
				break
			}
		}
		if !dup {
			seen = append(seen, it.val)
			if err := setKey(z, &out, it.key, it.val); err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
		}
	}
	return out, nil
}

func nativeArrayDiff(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_diff", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	base, err := asArray(z, args[0], "array_diff", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	exclude := make(map[string]bool)
	for i := 1; i < len(args); i++ {
		a, err := asArray(z, args[i], "array_diff", itoa(i+1), "arrays")
		if err != nil {
			return types.Value{}, err
		}
		_, vals := arrayItems(z, a)
		for _, v := range vals {
			s, err := castString(z, v)
			if err != nil {
				return types.Value{}, err
			}
			exclude[s] = true
		}
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, it := range collectItems(z, base) {
		s, err := castString(z, it.val)
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		if !exclude[s] {
			if err := setKey(z, &out, it.key, it.val); err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
		}
	}
	return out, nil
}

func nativeArrayIntersect(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_intersect", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	base, err := asArray(z, args[0], "array_intersect", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	otherSets := make([]map[string]bool, len(args)-1)
	for i := 1; i < len(args); i++ {
		a, err := asArray(z, args[i], "array_intersect", itoa(i+1), "arrays")
		if err != nil {
			return types.Value{}, err
		}
		m := make(map[string]bool)
		_, vals := arrayItems(z, a)
		for _, v := range vals {
			s, err := castString(z, v)
			if err != nil {
				return types.Value{}, err
			}
			m[s] = true
		}
		otherSets[i-1] = m
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, it := range collectItems(z, base) {
		s, err := castString(z, it.val)
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		inAll := true
		for _, m := range otherSets {
			if !m[s] {
				inAll = false
				break
			}
		}
		if inAll {
			if err := setKey(z, &out, it.key, it.val); err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
		}
	}
	return out, nil
}

func nativeArrayFillKeys(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_fill_keys", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	keysArr, err := asArray(z, args[0], "array_fill_keys", "1", "keys")
	if err != nil {
		return types.Value{}, err
	}
	val := deref(z, args[1])
	_, keys := arrayItems(z, keysArr)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, rawK := range keys {
		k := deref(z, rawK)
		if k.IsLong() || k.Type == types.TypeString {
			if err := setKey(z, &out, k, val); err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
		} else {
			ks, err := castString(z, k)
			if err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
			kv, err := newStr(z, ks)
			if err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
			err = setKey(z, &out, kv, val)
			types.ReleaseValue(z, &kv)
			if err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
		}
	}
	return out, nil
}

func nativeArrayPad(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_pad", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_pad", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	length, err := asIntParam(z, args[1], "array_pad", "2", "length")
	if err != nil {
		return types.Value{}, err
	}
	padVal := deref(z, args[2])
	items := collectItems(z, arr)
	absLen := length
	if absLen < 0 {
		absLen = -absLen
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	nextIdx := int64(0)
	appendItem := func(it kvItem) error {
		if it.key.IsLong() {
			e := setKey(z, &out, types.NewLong(nextIdx), it.val)
			nextIdx++
			return e
		}
		return setKey(z, &out, it.key, it.val)
	}
	if int64(len(items)) >= absLen {
		for _, it := range items {
			if err := appendItem(it); err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
		}
		return out, nil
	}
	need := absLen - int64(len(items))
	if length < 0 {
		for range need {
			if err := setKey(z, &out, types.NewLong(nextIdx), padVal); err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
			nextIdx++
		}
	}
	for _, it := range items {
		if err := appendItem(it); err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
	}
	if length > 0 {
		for range need {
			if err := setKey(z, &out, types.NewLong(nextIdx), padVal); err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
			nextIdx++
		}
	}
	return out, nil
}

func nativeArrayChunk(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_chunk", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_chunk", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	length, err := asIntParam(z, args[1], "array_chunk", "2", "length")
	if err != nil {
		return types.Value{}, err
	}
	if length < 1 {
		return types.Value{}, valErr("array_chunk", "2", "length", "must be greater than 0")
	}
	preserve := false
	if len(args) == 3 {
		preserve = asBool(z, args[2])
	}
	items := collectItems(z, arr)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	step := int(length)
	for i := 0; i < len(items); i += step {
		end := min(i+step, len(items))
		chunk, err := newArray(z)
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		for idx, it := range items[i:end] {
			if preserve {
				err = setKey(z, &chunk, it.key, it.val)
			} else {
				err = setKey(z, &chunk, types.NewLong(int64(idx)), it.val)
			}
			if err != nil {
				types.ReleaseValue(z, &chunk)
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
		}
		err = appendKey(z, &out, chunk)
		types.ReleaseValue(z, &chunk)
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
	}
	return out, nil
}

func nativeArrayKeyFirst(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_key_first", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_key_first", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	keys, _ := arrayItems(z, arr)
	if len(keys) == 0 {
		return types.NewNull(), nil
	}
	return cloneArrayValue(z, keys[0])
}

func nativeArrayKeyLast(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_key_last", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_key_last", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	keys, _ := arrayItems(z, arr)
	if len(keys) == 0 {
		return types.NewNull(), nil
	}
	return cloneArrayValue(z, keys[len(keys)-1])
}

func nativeArrayIsList(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_is_list", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_is_list", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewBool(isList(z, arr)), nil
}

func nativeArrayFlip(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("array_flip", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], "array_flip", "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	items := collectItems(z, arr)
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, it := range items {
		v := deref(z, it.val)
		if v.IsLong() || v.Type == types.TypeString {
			if err := setKey(z, &out, v, it.key); err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
		}
	}
	return out, nil
}

func sortArrayCommon(z *zone.Zone, name string, args []types.Value, byKey, desc, preserveKeys bool) (types.Value, error) {
	if err := arity(name, len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[0], name, "1", "array")
	if err != nil {
		return types.Value{}, err
	}
	flags := int64(sortRegular)
	if len(args) == 2 {
		flags, err = asIntParam(z, args[1], name, "2", "flags")
		if err != nil {
			return types.Value{}, err
		}
	}
	items := collectItems(z, arr)
	slices.SortStableFunc(items, func(a, b kvItem) int {
		var c int
		if byKey {
			c = compareWithFlags(z, a.key, b.key, flags)
		} else {
			c = compareWithFlags(z, a.val, b.val, flags)
		}
		if desc {
			return -c
		}
		return c
	})
	rebuilt, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for i, it := range items {
		if preserveKeys {
			err = setKey(z, &rebuilt, it.key, it.val)
		} else {
			err = setKey(z, &rebuilt, types.NewLong(int64(i)), it.val)
		}
		if err != nil {
			types.ReleaseValue(z, &rebuilt)
			return types.Value{}, err
		}
	}
	replaceArrayInPlace(z, args[0], rebuilt)
	return types.NewBool(true), nil
}

func nativeSort(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return sortArrayCommon(z, "sort", args, false, false, false)
}

func nativeRsort(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return sortArrayCommon(z, "rsort", args, false, true, false)
}

func nativeAsort(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return sortArrayCommon(z, "asort", args, false, false, true)
}

func nativeArsort(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return sortArrayCommon(z, "arsort", args, false, true, true)
}

func nativeKsort(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return sortArrayCommon(z, "ksort", args, true, false, true)
}

func nativeKrsort(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return sortArrayCommon(z, "krsort", args, true, true, true)
}
