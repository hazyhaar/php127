package ext

import (
	"strconv"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func deref(z *zone.Zone, v types.Value) types.Value { return types.Deref(z, v) }

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
		return "object"
	case types.TypeResource:
		switch v.Extra {
		case ResourceKindGD:
			return "GdImage"
		case ResourceKindFinfo:
			return "finfo"
		case ResourceKindCurl:
			return "CurlHandle"
		case ResourceKindXML:
			return "XMLParser"
		default:
			return "resource"
		}
	default:
		return "unknown"
	}
}

func newStr(z *zone.Zone, text string) (types.Value, error) {
	if len(text) <= 1 {
		return types.NewStringLiteral(z, text)
	}
	return types.NewString(z, text, 0)
}

func newArray(z *zone.Zone) (types.Value, error) { return types.NewArray(z) }

func appendVal(z *zone.Zone, a *types.Value, v types.Value) error {
	return types.ArrayAppend(z, a, v)
}

func setStrKey(z *zone.Zone, a *types.Value, key string, v types.Value) error {
	k, err := newStr(z, key)
	if err != nil {
		return err
	}
	err = types.ArraySet(z, a, k, v, nil)
	types.ReleaseValue(z, &k)
	return err
}

func writeRef(z *zone.Zone, slot, v types.Value) {
	if slot.Type != types.TypeReference {
		return
	}
	types.AssignRef(z, slot, v)
}

func arg(args []types.Value, i int) types.Value {
	if i < 0 || i >= len(args) {
		return types.NewUndef()
	}
	return args[i]
}

func missing(v types.Value) bool {
	return v.IsUndef() || v.Type == types.TypeNull
}

func asString(z *zone.Zone, v types.Value, fn, idx, param string) (string, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeString:
		return types.StringData(z, v), nil
	case types.TypeNull, types.TypeUndef:
		return "", nil
	case types.TypeFalse:
		return "", nil
	case types.TypeTrue:
		return "1", nil
	case types.TypeLong:
		return strconv.FormatInt(v.ToLong(), 10), nil
	case types.TypeDouble:
		return strconv.FormatFloat(v.ToDouble(), 'G', -1, 64), nil
	case types.TypeArray, types.TypeObject:
		return "", typeErr(fn, idx, param, "string", typeKind(z, v))
	default:
		return "", typeErr(fn, idx, param, "string", typeKind(z, v))
	}
}

func asStrictString(z *zone.Zone, v types.Value, fn, idx, param string) (string, error) {
	v = deref(z, v)
	if v.Type != types.TypeString {
		return "", typeErr(fn, idx, param, "string", typeKind(z, v))
	}
	return types.StringData(z, v), nil
}

func asInt(z *zone.Zone, v types.Value, fn, idx, param string) (int64, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeLong:
		return v.ToLong(), nil
	case types.TypeTrue:
		return 1, nil
	case types.TypeFalse, types.TypeNull, types.TypeUndef:
		return 0, nil
	case types.TypeDouble:
		return v.ToLong(), nil
	case types.TypeString:
		s := types.StringData(z, v)
		if s == "" {
			return 0, nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, typeErr(fn, idx, param, "int", "string")
		}
		return n, nil
	default:
		return 0, typeErr(fn, idx, param, "int", typeKind(z, v))
	}
}

func asBool(z *zone.Zone, v types.Value) bool { return types.ToBoolZ(z, v) }

func optBool(z *zone.Zone, args []types.Value, i int) bool {
	if len(args) <= i || missing(args[i]) {
		return false
	}
	return asBool(z, args[i])
}

func optInt(z *zone.Zone, args []types.Value, i int, def int64, fn, idx, param string) (int64, error) {
	if len(args) <= i || args[i].IsUndef() || deref(z, args[i]).IsNull() {
		return def, nil
	}
	return asInt(z, args[i], fn, idx, param)
}
