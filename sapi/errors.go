package sapi

import (
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

type TypeError struct{ text string }
type ValueError struct{ text string }
type ArgumentCountError struct{ text string }
type Error struct{ text string }

func (e TypeError) Error() string          { return e.text }
func (e ValueError) Error() string         { return e.text }
func (e ArgumentCountError) Error() string { return e.text }
func (e Error) Error() string              { return e.text }

func ExceptionClass(err error) string {
	if err == nil {
		return ""
	}
	switch err.(type) {
	case TypeError:
		return "TypeError"
	case ValueError:
		return "ValueError"
	case ArgumentCountError:
		return "ArgumentCountError"
	case Error:
		return "Error"
	default:
		return ""
	}
}

func argWord(n int) string {
	if n == 1 {
		return "argument"
	}
	return "arguments"
}

func arity(name string, n, minN, maxN int) error {
	if n < minN {
		if minN == maxN {
			return ArgumentCountError{sprintfCount(name, "exactly", minN, n)}
		}
		return ArgumentCountError{sprintfCount(name, "at least", minN, n)}
	}
	if maxN >= 0 && n > maxN {
		return ArgumentCountError{sprintfCount(name, "at most", maxN, n)}
	}
	return nil
}

func sprintfCount(name, how string, want, got int) string {
	return name + "() expects " + how + " " + strconv.Itoa(want) + " " + argWord(want) + ", " + strconv.Itoa(got) + " given"
}

func typeErr(fn, idx, param, want, got string) TypeError {
	return TypeError{fn + "(): Argument #" + idx + " ($" + param + ") must be of type " + want + ", " + got + " given"}
}

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

func newTaintedStr(z *zone.Zone, text string) (types.Value, error) {
	v, err := newStr(z, text)
	if err != nil {
		return types.Value{}, err
	}
	v.Flags |= types.FlagTainted
	return v, nil
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

func missing(v types.Value) bool {
	return v.IsUndef() || v.Type == types.TypeNull
}

func asString(z *zone.Zone, v types.Value, fn, idx, param string) (string, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeString:
		return strings.Clone(types.StringData(z, v)), nil
	case types.TypeNull, types.TypeUndef, types.TypeFalse:
		return "", nil
	case types.TypeTrue:
		return "1", nil
	case types.TypeLong:
		return strconv.FormatInt(v.ToLong(), 10), nil
	case types.TypeDouble:
		return strconv.FormatFloat(v.ToDouble(), 'G', 14, 64), nil
	default:
		return "", typeErr(fn, idx, param, "string", typeKind(z, v))
	}
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

func optBool(z *zone.Zone, args []types.Value, i int, def bool) bool {
	if len(args) <= i || missing(args[i]) {
		return def
	}
	return asBool(z, args[i])
}

func optInt(z *zone.Zone, args []types.Value, i int, def int64, fn, idx, param string) (int64, error) {
	if len(args) <= i || args[i].IsUndef() || deref(z, args[i]).IsNull() {
		return def, nil
	}
	return asInt(z, args[i], fn, idx, param)
}

func optString(z *zone.Zone, args []types.Value, i int, def, fn, idx, param string) (string, error) {
	if len(args) <= i || args[i].IsUndef() || deref(z, args[i]).IsNull() {
		return def, nil
	}
	return asString(z, args[i], fn, idx, param)
}
