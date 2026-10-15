package mysqli

import (
	"io"
	"math"
	"strconv"
	"strings"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// SQLException représente une exception mysqli_sql_exception (mode MYSQLI_REPORT_STRICT).
type SQLException struct {
	Code     int
	SQLState string
	Message  string
}

func (e SQLException) Error() string { return e.Message }

// Erreurs PHP levées par les fonctions natives.
type (
	TypeError          struct{ text string }
	ValueError         struct{ text string }
	ArgumentCountError struct{ text string }
	Error              struct{ text string }
)

func (e TypeError) Error() string          { return e.text }
func (e ValueError) Error() string         { return e.text }
func (e ArgumentCountError) Error() string { return e.text }
func (e Error) Error() string              { return e.text }

// ExceptionClass retourne le nom de la classe PHP correspondant à une erreur du paquet.
func ExceptionClass(err error) string {
	switch err.(type) {
	case SQLException:
		return "mysqli_sql_exception"
	case TypeError:
		return "TypeError"
	case ValueError:
		return "ValueError"
	case ArgumentCountError:
		return "ArgumentCountError"
	case Error:
		return "Error"
	}
	return ""
}

// NewSQLExceptionObject construit l'objet mysqli_sql_exception (0 message, 1 code, 2 file, 3 line, 4 sqlstate).
func NewSQLExceptionObject(z *zone.Zone, ex SQLException) (types.Value, error) {
	obj, err := types.NewObject(z, ClassIDMysqliSqlException, 5, false)
	if err != nil {
		return types.Value{}, err
	}
	for i, s := range []string{ex.Message, "", ex.SQLState} {
		v, err := newStr(z, s)
		if err != nil {
			types.ReleaseValue(z, &obj)
			return types.Value{}, err
		}
		slot := []uint32{0, 2, 4}[i]
		types.ObjectSet(z, obj, slot, v)
		types.ReleaseValue(z, &v)
	}
	types.ObjectSet(z, obj, 1, types.NewLong(int64(ex.Code)))
	types.ObjectSet(z, obj, 3, types.NewLong(0))
	return obj, nil
}

func newStr(z *zone.Zone, text string) (types.Value, error) {
	if len(text) <= 1 {
		return types.NewStringLiteral(z, text)
	}
	return types.NewString(z, text, 0)
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

func typeName(z *zone.Zone, v types.Value) string {
	switch types.Deref(z, v).Type {
	case types.TypeUndef, types.TypeNull:
		return "null"
	case types.TypeTrue, types.TypeFalse:
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
	}
	return "mixed"
}

func castString(z *zone.Zone, v types.Value) (string, bool) {
	v = types.Deref(z, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull, types.TypeFalse:
		return "", true
	case types.TypeTrue:
		return "1", true
	case types.TypeLong:
		return strconv.FormatInt(v.ToLong(), 10), true
	case types.TypeDouble:
		f := v.ToDouble()
		switch {
		case math.IsNaN(f):
			return "NAN", true
		case math.IsInf(f, 1):
			return "INF", true
		case math.IsInf(f, -1):
			return "-INF", true
		}
		return strconv.FormatFloat(f, 'G', 14, 64), true
	case types.TypeString:
		// Copie hors de l'arène : les jetons SQL, la chaîne de types de bind_param et les commandes
		// d'initialisation survivent à la libération de l'argument PHP.
		return strings.Clone(types.StringData(z, v)), true
	}
	return "", false
}

func castInt(z *zone.Zone, v types.Value) int64 {
	v = types.Deref(z, v)
	switch v.Type {
	case types.TypeLong:
		return v.ToLong()
	case types.TypeTrue:
		return 1
	case types.TypeDouble:
		return v.ToLong()
	case types.TypeString:
		return cellInt64(textCell(types.StringData(z, v)))
	}
	return 0
}

func castFloat(z *zone.Zone, v types.Value) float64 {
	v = types.Deref(z, v)
	switch v.Type {
	case types.TypeLong:
		return float64(v.ToLong())
	case types.TypeTrue:
		return 1
	case types.TypeDouble:
		return v.ToDouble()
	case types.TypeString:
		return cellFloat64(textCell(types.StringData(z, v)))
	}
	return 0
}

func isNull(z *zone.Zone, v types.Value) bool {
	t := types.Deref(z, v).Type
	return t == types.TypeNull || t == types.TypeUndef
}

// arity contrôle le nombre d'arguments d'une fonction native ; maxN < 0 signifie variadique.
func arity(fn string, n, minN, maxN int) error {
	word := func(k int) string {
		if k == 1 {
			return "argument"
		}
		return "arguments"
	}
	if n < minN {
		how := "at least"
		if minN == maxN {
			how = "exactly"
		}
		return ArgumentCountError{fn + "() expects " + how + " " + strconv.Itoa(minN) + " " + word(minN) + ", " + strconv.Itoa(n) + " given"}
	}
	if maxN >= 0 && n > maxN {
		how := "at most"
		if minN == maxN {
			how = "exactly"
		}
		return ArgumentCountError{fn + "() expects " + how + " " + strconv.Itoa(maxN) + " " + word(maxN) + ", " + strconv.Itoa(n) + " given"}
	}
	return nil
}

func argString(z *zone.Zone, fn string, args []types.Value, i int, param string) (string, error) {
	s, ok := castString(z, args[i])
	if !ok {
		return "", TypeError{fn + "(): Argument #" + strconv.Itoa(i+1) + " ($" + param + ") must be of type string, " + typeName(z, args[i]) + " given"}
	}
	return s, nil
}

// optString lit un argument chaîne facultatif ou nullable ; present vaut false pour null ou absent.
func optString(z *zone.Zone, args []types.Value, i int) (string, bool) {
	if i >= len(args) || isNull(z, args[i]) {
		return "", false
	}
	s, _ := castString(z, args[i])
	return s, true
}

func optInt(z *zone.Zone, args []types.Value, i int, def int64) int64 {
	if i >= len(args) || isNull(z, args[i]) {
		return def
	}
	return castInt(z, args[i])
}

// objectHandle lit l'identifiant de handle d'un objet de la classe attendue.
func objectHandle(z *zone.Zone, v types.Value, classID uint32) (uint32, bool) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return 0, false
	}
	info := types.ObjectInfo(z, v)
	if info.ClassID != classID || info.PropCount < 1 {
		return 0, false
	}
	idv := types.ObjectGet(z, v, 0)
	if idv.Type != types.TypeLong {
		return 0, false
	}
	return uint32(idv.ToLong()), true
}

func newHandleObject(z *zone.Zone, classID, id uint32) (types.Value, error) {
	obj, err := types.NewObject(z, classID, 1, false)
	if err != nil {
		return types.Value{}, err
	}
	types.ObjectSet(z, obj, 0, types.NewLong(int64(id)))
	return obj, nil
}

// attachDynamicArray transfère un tableau associatif comme table des propriétés dynamiques d'un objet.
func attachDynamicArray(z *zone.Zone, obj types.Value, dynArr types.Value) {
	off, ok := z.ObjectAddress(obj.DataSlot, obj.Extra)
	if !ok {
		return
	}
	raw, err := z.Bytes(off, uint32(unsafe.Sizeof(types.ObjectHeader{})))
	if err != nil {
		return
	}
	h := (*types.ObjectHeader)(unsafe.Pointer(unsafe.SliceData(raw)))
	if h.DynOffset != types.EmptySlot {
		old := types.Value{Type: types.TypeArray, Flags: types.FlagZoneRequest, DataSlot: h.DynOffset}
		types.ReleaseArray(z, &old)
	}
	h.DynOffset = dynArr.DataSlot
}

// ObjectGetProperty lit une propriété dynamique d'un objet stdClass produit par le paquet.
func ObjectGetProperty(z *zone.Zone, obj types.Value, name string) (types.Value, bool) {
	obj = types.Deref(z, obj)
	if obj.Type != types.TypeObject {
		return types.Value{}, false
	}
	info := types.ObjectInfo(z, obj)
	if info.DynOffset == types.EmptySlot {
		return types.Value{}, false
	}
	arr := types.Value{Type: types.TypeArray, Flags: types.FlagZoneRequest, DataSlot: info.DynOffset}
	k, err := newStr(z, name)
	if err != nil {
		return types.Value{}, false
	}
	defer types.ReleaseValue(z, &k)
	v, found, err := types.ArrayGet(z, arr, k, warnDrop)
	if err != nil || !found {
		return types.Value{}, false
	}
	return v, true
}

// newStdObject construit un stdClass à partir de paires nom/valeur ; les valeurs sont empruntées.
func newStdObject(z *zone.Zone, names []string, vals []types.Value) (types.Value, error) {
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for i, n := range names {
		if err := setArrayKey(z, &arr, n, vals[i]); err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
	}
	obj, err := types.NewObject(z, ClassIDStdClass, 0, false)
	if err != nil {
		types.ReleaseValue(z, &arr)
		return types.Value{}, err
	}
	attachDynamicArray(z, obj, arr)
	return obj, nil
}

// cellValue convertit une cellule en valeur PHP : chaîne en protocole texte, type natif en protocole binaire
// ou avec MYSQLI_OPT_INT_AND_FLOAT_NATIVE.
func cellValue(z *zone.Zone, c cell, f field, native bool) (types.Value, error) {
	switch c.kind {
	case cellNull:
		return types.NewNull(), nil
	case cellInt:
		if native {
			return types.NewLong(c.i), nil
		}
	case cellFloat:
		if native {
			if f.typ == typeNewDecimal || f.typ == typeDecimal {
				return newStr(z, c.text())
			}
			return types.NewDouble(c.f), nil
		}
	case cellText:
		if native && isNumericType(f.typ) && f.typ != typeNewDecimal && f.typ != typeDecimal {
			if f.typ == typeFloat || f.typ == typeDouble {
				if v, err := strconv.ParseFloat(c.s, 64); err == nil {
					return types.NewDouble(v), nil
				}
			} else if v, err := strconv.ParseInt(c.s, 10, 64); err == nil {
				return types.NewLong(v), nil
			}
		}
	}
	return newStr(z, c.text())
}

// warn écrit un avertissement PHP (mode MYSQLI_REPORT_ERROR sans STRICT).
func warn(out io.Writer, fn, msg string) {
	if out == nil {
		return
	}
	_, _ = io.WriteString(out, "\nWarning: "+fn+"(): "+msg+"\n")
}

func lower(s string) string { return strings.ToLower(s) }
