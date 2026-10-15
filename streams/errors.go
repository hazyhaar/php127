package streams

import (
	"math"
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

// ExceptionClass retourne le nom de la classe d'exception PHP correspondant à err.
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
		if minN == maxN {
			return ArgumentCountError{sprintfCount(name, "exactly", maxN, n)}
		}
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

func deref(z *zone.Zone, v types.Value) types.Value {
	return types.Deref(z, v)
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
		return "object"
	case types.TypeResource:
		return "resource"
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
		f := v.ToDouble()
		if math.IsNaN(f) {
			return "NAN", nil
		}
		if math.IsInf(f, 1) {
			return "INF", nil
		}
		if math.IsInf(f, -1) {
			return "-INF", nil
		}
		return strconv.FormatFloat(f, 'G', 14, 64), nil
	case types.TypeString:
		// Point d'entrée de toutes les chaînes reçues par le paquet : la copie empêche toute chaîne
		// conservée (descripteur, contexte, chemin, erreur) de référencer l'arène de la Zone.
		return strings.Clone(types.StringData(z, v)), nil
	default:
		return "", TypeError{"conversion en chaîne impossible"}
	}
}

func asStringParam(z *zone.Zone, v types.Value, fn, idx, param string) (string, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeArray, types.TypeObject, types.TypeResource:
		return "", typeErr(fn, idx, param, "string", typeKind(z, v))
	default:
		s, err := castString(z, v)
		if err != nil {
			return "", typeErr(fn, idx, param, "string", typeKind(z, v))
		}
		return s, nil
	}
}

func asIntParam(z *zone.Zone, v types.Value, fn, idx, param string) (int64, error) {
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
		s := strings.TrimSpace(types.StringData(z, v))
		if s == "" {
			return 0, typeErr(fn, idx, param, "int", "string")
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, nil
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(f), nil
		}
		return 0, typeErr(fn, idx, param, "int", "string")
	default:
		return 0, typeErr(fn, idx, param, "int", typeKind(z, v))
	}
}

func asBool(z *zone.Zone, v types.Value) bool {
	v = deref(z, v)
	if v.Type == types.TypeResource {
		return true
	}
	return types.ToBoolZ(z, v)
}

func warnDrop(types.ArrayWarning) error { return nil }

func setArrayKey(z *zone.Zone, a *types.Value, key string, val types.Value) error {
	k, err := newStr(z, key)
	if err != nil {
		return err
	}
	defer types.ReleaseValue(z, &k)
	return types.ArraySet(z, a, k, val, warnDrop)
}

func setArrayIntKey(z *zone.Zone, a *types.Value, key int64, val types.Value) error {
	return types.ArraySet(z, a, types.NewLong(key), val, warnDrop)
}
