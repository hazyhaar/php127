package stdlib

import (
	"math"
	"strconv"
	"strings"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func itoa(n int) string { return strconv.Itoa(n) }

func deref(z *zone.Zone, v types.Value) types.Value { return types.Deref(z, v) }

func opaqueResourceClass(v types.Value) (string, bool) {
	if v.Type != types.TypeResource {
		return "", false
	}
	switch v.Extra {
	case 0x4503:
		return "GdImage", true
	case 0x4504:
		return "finfo", true
	case 0x4505:
		return "CurlHandle", true
	case 0x4506:
		return "XMLParser", true
	default:
		return "", false
	}
}

func typeKind(z *zone.Zone, v types.Value) string {
	v = deref(z, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		return "null"
	case types.TypeFalse, types.TypeTrue:
		return "bool"
	case types.TypeLong:
		return "int"
	case types.TypeDouble:
		return "float"
	case types.TypeString:
		return "string"
	case types.TypeArray:
		return "array"
	case types.TypeObject:
		name := objectClassName(z, v)
		if name == "" {
			return "object"
		}
		return name
	case types.TypeResource:
		if name, ok := opaqueResourceClass(v); ok {
			return name
		}
		return "resource"
	default:
		return "unknown"
	}
}

func objectClassName(z *zone.Zone, v types.Value) string {
	return safeClass(z, v)
}

func newStr(z *zone.Zone, text string) (types.Value, error) {
	if len(text) <= 1 {
		return types.NewStringLiteral(z, text)
	}
	return types.NewString(z, text, 0)
}

func newBytes(z *zone.Zone, b []byte) (types.Value, error) {
	if len(b) == 0 {
		return types.NewStringLiteral(z, "")
	}
	return newStr(z, unsafe.String(unsafe.SliceData(b), len(b)))
}

const (
	floatCast = iota
	floatDump
	floatExport
	floatJSON
	floatPrint
)

func formatFloat(f float64, style int) string {
	switch {
	case math.IsNaN(f):
		return "NAN"
	case math.IsInf(f, 1):
		return "INF"
	case math.IsInf(f, -1):
		return "-INF"
	}
	prec := 17
	if style == floatDump {
		prec = -1
	} else if style == floatCast || style == floatPrint {
		prec = 14
	}
	s := strconv.FormatFloat(f, 'G', prec, 64)
	s = rewriteExponent(s, style == floatJSON)
	if style == floatExport && !strings.ContainsAny(s, ".Ee") {
		s += ".0"
	}
	return s
}

func rewriteExponent(s string, lower bool) string {
	i := strings.IndexAny(s, "eE")
	if i < 0 {
		return s
	}
	mant, exp := s[:i], s[i+1:]
	if !strings.Contains(mant, ".") {
		mant += ".0"
	}
	sign := byte('+')
	if len(exp) > 0 && (exp[0] == '+' || exp[0] == '-') {
		sign = exp[0]
		exp = exp[1:]
	}
	for len(exp) > 1 && exp[0] == '0' {
		exp = exp[1:]
	}
	if exp == "" {
		exp = "0"
	}
	mark := byte('E')
	if lower {
		mark = 'e'
	}
	return mant + string(mark) + string(sign) + exp
}

func castString(z *zone.Zone, v types.Value) (string, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull, types.TypeFalse:
		return "", nil
	case types.TypeTrue:
		return "1", nil
	case types.TypeLong:
		return strconv.FormatInt(int64(v.Scalar), 10), nil
	case types.TypeDouble:
		return formatFloat(v.ToDouble(), floatCast), nil
	case types.TypeString:
		return types.StringData(z, v), nil
	case types.TypeArray:
		return "Array", nil
	case types.TypeObject:
		name := "object"
		if n := safeClass(z, v); n != "" {
			name = n
		}
		return "", Error{"Object of class " + name + " could not be converted to string"}
	default:
		return "", Error{"conversion en chaîne impossible"}
	}
}

func safeClass(z *zone.Zone, v types.Value) (name string) {
	defer func() {
		if recover() != nil {
			name = ""
		}
	}()
	id := types.ObjectInfo(z, v).ClassID
	if id == classStdClass {
		return "stdClass"
	}
	return ""
}

func asStringParam(z *zone.Zone, v types.Value, fn, idx, param string) (string, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeArray, types.TypeObject:
		return "", typeErr(fn, idx, param, "string", typeKind(z, v))
	default:
		s, err := castString(z, v)
		if err != nil {
			return "", typeErr(fn, idx, param, "string", typeKind(z, v))
		}
		return s, nil
	}
}

func isWS(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func trimWS(s string) string {
	i := 0
	for i < len(s) && isWS(s[i]) {
		i++
	}
	j := len(s)
	for j > i && isWS(s[j-1]) {
		j--
	}
	return s[i:j]
}

func isNumericString(s string) bool {
	s = trimWS(s)
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i++
	}
	digits, dot := 0, false
	for i < len(s) {
		c := s[i]
		if c >= '0' && c <= '9' {
			digits++
			i++
			continue
		}
		if c == '.' && !dot {
			dot = true
			i++
			continue
		}
		break
	}
	if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exp := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			exp++
			i++
		}
		if exp == 0 {
			return false
		}
	}
	return i == len(s)
}

func parseIntVal(s string, base int) int64 {
	i := 0
	for i < len(s) && isWS(s[i]) {
		i++
	}
	if i >= len(s) {
		return 0
	}
	sign := int64(1)
	if s[i] == '+' || s[i] == '-' {
		if s[i] == '-' {
			sign = -1
		}
		i++
	}
	if base == 0 {
		base = 10
		if i < len(s) && s[i] == '0' {
			if i+1 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X') {
				base = 16
				i += 2
			} else if i+1 < len(s) && (s[i+1] == 'b' || s[i+1] == 'B') {
				base = 2
				i += 2
			} else {
				base = 8
			}
		}
	}
	if base < 2 || base > 36 {
		return 0
	}
	if base == 10 && hasFloatSyntax(s[i:]) {
		return floatToInt(parseFloatPrefix(s))
	}
	var n uint64
	any := false
	overflow := false
	limit := uint64(math.MaxInt64)
	if sign < 0 {
		limit++
	}
	for i < len(s) {
		d := digitVal(s[i])
		if d < 0 || d >= base {
			break
		}
		any = true
		if n > (limit-uint64(d))/uint64(base) {
			overflow = true
		} else {
			n = n*uint64(base) + uint64(d)
		}
		i++
	}
	if !any {
		return 0
	}
	if overflow {
		if sign < 0 {
			return math.MinInt64
		}
		return math.MaxInt64
	}
	if sign < 0 {
		return -int64(n)
	}
	return int64(n)
}

func hasFloatSyntax(s string) bool {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < len(s) && s[i] == '.' {
		return true
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		return true
	}
	return false
}

func floatToInt(f float64) int64 {
	if math.IsNaN(f) {
		return 0
	}
	if f >= 9223372036854775808 {
		return math.MaxInt64
	}
	if f <= -9223372036854775808 {
		return math.MinInt64
	}
	return int64(f)
}

func digitVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	default:
		return -1
	}
}

func parseFloatPrefix(s string) float64 {
	i := 0
	for i < len(s) && isWS(s[i]) {
		i++
	}
	if i >= len(s) {
		return 0
	}
	start := i
	if s[i] == '+' || s[i] == '-' {
		i++
	}
	saw := false
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		saw = true
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			saw = true
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	if !saw {
		return 0
	}
	f, err := strconv.ParseFloat(s[start:i], 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return f
		}
		return 0
	}
	return f
}

func numericValue(s string) (types.Value, bool) {
	if !isNumericString(s) {
		return types.Value{}, false
	}
	t := trimWS(s)
	pure := true
	body := t
	if len(body) > 0 && (body[0] == '+' || body[0] == '-') {
		body = body[1:]
	}
	for i := 0; i < len(body); i++ {
		if body[i] == '.' || body[i] == 'e' || body[i] == 'E' {
			pure = false
			break
		}
	}
	if pure {
		body := t
		sign := 1
		if len(body) > 0 && (body[0] == '+' || body[0] == '-') {
			if body[0] == '-' {
				sign = -1
			}
			body = body[1:]
		}
		body = strings.TrimLeft(body, "0")
		if body == "" {
			return types.NewLong(0), true
		}
		if len(body) < 19 || (len(body) == 19 && (sign > 0 && body <= "9223372036854775807" || sign < 0 && body <= "9223372036854775808")) {
			n, err := strconv.ParseInt(t, 10, 64)
			if err == nil {
				return types.NewLong(n), true
			}
		}
	}
	return types.NewDouble(parseFloatPrefix(t)), true
}

func asIntLoose(z *zone.Zone, v types.Value) (int64, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeLong:
		return int64(v.Scalar), nil
	case types.TypeTrue:
		return 1, nil
	case types.TypeFalse, types.TypeNull, types.TypeUndef:
		return 0, nil
	case types.TypeDouble:
		return v.ToLong(), nil
	case types.TypeString:
		return parseIntVal(types.StringData(z, v), 10), nil
	case types.TypeArray:
		if types.ArrayLen(z, v) == 0 {
			return 0, nil
		}
		return 1, nil
	default:
		name := safeClass(z, v)
		if name == "" {
			name = typeKind(z, v)
		}
		return 0, Error{"Object of class " + name + " could not be converted to int"}
	}
}

func asIntParam(z *zone.Zone, v types.Value, fn, idx, param string) (int64, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeArray, types.TypeObject:
		return 0, typeErr(fn, idx, param, "int", typeKind(z, v))
	case types.TypeString:
		s := types.StringData(z, v)
		if s != "" && !isNumericString(s) {
			return 0, typeErr(fn, idx, param, "int", "string")
		}
		return parseIntVal(s, 10), nil
	default:
		return asIntLoose(z, v)
	}
}

func asFloatParam(z *zone.Zone, v types.Value, fn, idx, param string) (float64, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeDouble:
		return v.ToDouble(), nil
	case types.TypeLong:
		return float64(int64(v.Scalar)), nil
	case types.TypeTrue:
		return 1, nil
	case types.TypeFalse, types.TypeNull, types.TypeUndef:
		return 0, nil
	case types.TypeString:
		s := types.StringData(z, v)
		nv, ok := numericValue(s)
		if !ok {
			return 0, typeErr(fn, idx, param, "float", "string")
		}
		if nv.IsLong() {
			return float64(nv.ToLong()), nil
		}
		return nv.ToDouble(), nil
	default:
		return 0, typeErr(fn, idx, param, "float", typeKind(z, v))
	}
}

func asNumber(z *zone.Zone, v types.Value, fn, idx, param string) (types.Value, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeLong, types.TypeDouble:
		return v, nil
	case types.TypeTrue:
		return types.NewLong(1), nil
	case types.TypeFalse, types.TypeNull, types.TypeUndef:
		return types.NewLong(0), nil
	case types.TypeString:
		nv, ok := numericValue(types.StringData(z, v))
		if !ok {
			return types.Value{}, typeErr(fn, idx, param, "int|float", "string")
		}
		return nv, nil
	default:
		return types.Value{}, typeErr(fn, idx, param, "int|float", typeKind(z, v))
	}
}

func asArray(z *zone.Zone, v types.Value, fn, idx, param string) (types.Value, error) {
	v = deref(z, v)
	if v.Type != types.TypeArray {
		return types.Value{}, typeErr(fn, idx, param, "array", typeKind(z, v))
	}
	return v, nil
}

func asBool(z *zone.Zone, v types.Value) bool { return types.ToBoolZ(z, v) }

func warnDrop(types.ArrayWarning) error { return nil }

func newArray(z *zone.Zone) (types.Value, error) { return types.NewArray(z) }

func setKey(z *zone.Zone, a *types.Value, key, val types.Value) error {
	return types.ArraySet(z, a, key, val, warnDrop)
}

func appendKey(z *zone.Zone, a *types.Value, val types.Value) error {
	return types.ArrayAppend(z, a, val)
}

func arrayItems(z *zone.Zone, a types.Value) (keys, vals []types.Value) {
	types.ArrayForEach(z, a, func(k, v types.Value) bool {
		keys = append(keys, k)
		vals = append(vals, v)
		return true
	})
	return keys, vals
}

func isList(z *zone.Zone, a types.Value) bool {
	i := int64(0)
	ok := true
	types.ArrayForEach(z, a, func(k, _ types.Value) bool {
		if !k.IsLong() || k.ToLong() != i {
			ok = false
			return false
		}
		i++
		return true
	})
	return ok
}

func phpCmp(z *zone.Zone, a, b types.Value) int {
	a, b = deref(z, a), deref(z, b)
	if a.Type == types.TypeArray || b.Type == types.TypeArray {
		if a.Type != types.TypeArray {
			return -1
		}
		if b.Type != types.TypeArray {
			return 1
		}
		ak, av := arrayItems(z, a)
		bk, bv := arrayItems(z, b)
		_ = ak
		_ = bk
		n := min(len(bv), len(av))
		for i := range n {
			c := phpCmp(z, av[i], bv[i])
			if c != 0 {
				return c
			}
		}
		if len(av) < len(bv) {
			return -1
		}
		if len(av) > len(bv) {
			return 1
		}
		return 0
	}
	if a.Type == types.TypeString && b.Type == types.TypeString {
		return types.CompareStringsPHP(z, a, b)
	}
	if a.Type == types.TypeString || b.Type == types.TypeString {
		swap := false
		if a.Type != types.TypeString {
			a, b = b, a
			swap = true
		}
		text := types.StringData(z, a)
		if nv, ok := numericValue(text); ok && (b.Type == types.TypeLong || b.Type == types.TypeDouble || b.IsBool() || b.IsNull() || b.IsUndef()) {
			c := cmpNum(nv, scalarNum(b))
			if swap {
				return -c
			}
			return c
		}
		bs, _ := castString(z, b)
		c := strings.Compare(text, bs)
		if swap {
			return -c
		}
		return c
	}
	return cmpNum(scalarNum(a), scalarNum(b))
}

func scalarNum(v types.Value) types.Value {
	switch v.Type {
	case types.TypeDouble, types.TypeLong:
		return v
	case types.TypeTrue:
		return types.NewLong(1)
	default:
		return types.NewLong(0)
	}
}

func cmpNum(a, b types.Value) int {
	if a.IsLong() && b.IsLong() {
		x, y := a.ToLong(), b.ToLong()
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
		return 0
	}
	x, y := a.ToDouble(), b.ToDouble()
	if x < y {
		return -1
	}
	if x > y {
		return 1
	}
	if math.IsNaN(x) || math.IsNaN(y) {
		return 1
	}
	return 0
}
