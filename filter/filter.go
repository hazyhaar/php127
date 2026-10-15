// Package filter implémente l'extension filter de PHP 8.3 (constantes FILTER_*,
// filter_var, filter_var_array, filter_id et filter_list).
package filter

import (
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// NativeFunc est l'alias de signature compatible avec stdlib.NativeFunc.
type NativeFunc = func(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error)

// Constantes de l'extension filter de PHP 8.3.
const (
	INPUT_POST   int64 = 0
	INPUT_GET    int64 = 1
	INPUT_COOKIE int64 = 2
	INPUT_ENV    int64 = 4
	INPUT_SERVER int64 = 5

	FILTER_VALIDATE_INT     int64 = 257
	FILTER_VALIDATE_BOOLEAN int64 = 258
	FILTER_VALIDATE_BOOL    int64 = 258
	FILTER_VALIDATE_FLOAT   int64 = 259
	FILTER_VALIDATE_REGEXP  int64 = 272
	FILTER_VALIDATE_URL     int64 = 273
	FILTER_VALIDATE_EMAIL   int64 = 274
	FILTER_VALIDATE_IP      int64 = 275
	FILTER_VALIDATE_MAC     int64 = 276
	FILTER_VALIDATE_DOMAIN  int64 = 277

	FILTER_DEFAULT                     int64 = 516
	FILTER_UNSAFE_RAW                  int64 = 516
	FILTER_SANITIZE_STRING             int64 = 513
	FILTER_SANITIZE_STRIPPED           int64 = 513
	FILTER_SANITIZE_ENCODED            int64 = 514
	FILTER_SANITIZE_SPECIAL_CHARS      int64 = 515
	FILTER_SANITIZE_EMAIL              int64 = 517
	FILTER_SANITIZE_URL                int64 = 518
	FILTER_SANITIZE_NUMBER_INT         int64 = 519
	FILTER_SANITIZE_NUMBER_FLOAT       int64 = 520
	FILTER_SANITIZE_FULL_SPECIAL_CHARS int64 = 522
	FILTER_SANITIZE_ADD_SLASHES        int64 = 523
	FILTER_CALLBACK                    int64 = 1024

	FILTER_FLAG_NONE             int64 = 0
	FILTER_FLAG_ALLOW_OCTAL      int64 = 1
	FILTER_FLAG_ALLOW_HEX        int64 = 2
	FILTER_FLAG_STRIP_LOW        int64 = 4
	FILTER_FLAG_STRIP_HIGH       int64 = 8
	FILTER_FLAG_ENCODE_LOW       int64 = 16
	FILTER_FLAG_ENCODE_HIGH      int64 = 32
	FILTER_FLAG_ENCODE_AMP       int64 = 64
	FILTER_FLAG_NO_ENCODE_QUOTES int64 = 128
	FILTER_FLAG_STRIP_BACKTICK   int64 = 512
	FILTER_FLAG_ALLOW_FRACTION   int64 = 4096
	FILTER_FLAG_ALLOW_THOUSAND   int64 = 8192
	FILTER_FLAG_ALLOW_SCIENTIFIC int64 = 16384
	FILTER_FLAG_PATH_REQUIRED    int64 = 262144
	FILTER_FLAG_QUERY_REQUIRED   int64 = 524288
	FILTER_FLAG_IPV4             int64 = 1048576
	FILTER_FLAG_IPV6             int64 = 2097152
	FILTER_FLAG_NO_RES_RANGE     int64 = 4194304
	FILTER_FLAG_NO_PRIV_RANGE    int64 = 8388608
	FILTER_REQUIRE_ARRAY         int64 = 16777216
	FILTER_REQUIRE_SCALAR        int64 = 33554432
	FILTER_FORCE_ARRAY           int64 = 67108864
	FILTER_NULL_ON_FAILURE       int64 = 134217728
)

var filterConstants = map[string]int64{
	"INPUT_POST":                         INPUT_POST,
	"INPUT_GET":                          INPUT_GET,
	"INPUT_COOKIE":                       INPUT_COOKIE,
	"INPUT_ENV":                          INPUT_ENV,
	"INPUT_SERVER":                       INPUT_SERVER,
	"FILTER_VALIDATE_INT":                FILTER_VALIDATE_INT,
	"FILTER_VALIDATE_BOOLEAN":            FILTER_VALIDATE_BOOLEAN,
	"FILTER_VALIDATE_BOOL":               FILTER_VALIDATE_BOOL,
	"FILTER_VALIDATE_FLOAT":              FILTER_VALIDATE_FLOAT,
	"FILTER_VALIDATE_REGEXP":             FILTER_VALIDATE_REGEXP,
	"FILTER_VALIDATE_DOMAIN":             FILTER_VALIDATE_DOMAIN,
	"FILTER_VALIDATE_URL":                FILTER_VALIDATE_URL,
	"FILTER_VALIDATE_EMAIL":              FILTER_VALIDATE_EMAIL,
	"FILTER_VALIDATE_IP":                 FILTER_VALIDATE_IP,
	"FILTER_VALIDATE_MAC":                FILTER_VALIDATE_MAC,
	"FILTER_DEFAULT":                     FILTER_DEFAULT,
	"FILTER_UNSAFE_RAW":                  FILTER_UNSAFE_RAW,
	"FILTER_SANITIZE_STRING":             FILTER_SANITIZE_STRING,
	"FILTER_SANITIZE_STRIPPED":           FILTER_SANITIZE_STRIPPED,
	"FILTER_SANITIZE_ENCODED":            FILTER_SANITIZE_ENCODED,
	"FILTER_SANITIZE_SPECIAL_CHARS":      FILTER_SANITIZE_SPECIAL_CHARS,
	"FILTER_SANITIZE_FULL_SPECIAL_CHARS": FILTER_SANITIZE_FULL_SPECIAL_CHARS,
	"FILTER_SANITIZE_EMAIL":              FILTER_SANITIZE_EMAIL,
	"FILTER_SANITIZE_URL":                FILTER_SANITIZE_URL,
	"FILTER_SANITIZE_NUMBER_INT":         FILTER_SANITIZE_NUMBER_INT,
	"FILTER_SANITIZE_NUMBER_FLOAT":       FILTER_SANITIZE_NUMBER_FLOAT,
	"FILTER_SANITIZE_ADD_SLASHES":        FILTER_SANITIZE_ADD_SLASHES,
	"FILTER_CALLBACK":                    FILTER_CALLBACK,
	"FILTER_FLAG_NONE":                   FILTER_FLAG_NONE,
	"FILTER_FLAG_ALLOW_OCTAL":            FILTER_FLAG_ALLOW_OCTAL,
	"FILTER_FLAG_ALLOW_HEX":              FILTER_FLAG_ALLOW_HEX,
	"FILTER_FLAG_STRIP_LOW":              FILTER_FLAG_STRIP_LOW,
	"FILTER_FLAG_STRIP_HIGH":             FILTER_FLAG_STRIP_HIGH,
	"FILTER_FLAG_STRIP_BACKTICK":         FILTER_FLAG_STRIP_BACKTICK,
	"FILTER_FLAG_ENCODE_LOW":             FILTER_FLAG_ENCODE_LOW,
	"FILTER_FLAG_ENCODE_HIGH":            FILTER_FLAG_ENCODE_HIGH,
	"FILTER_FLAG_ENCODE_AMP":             FILTER_FLAG_ENCODE_AMP,
	"FILTER_FLAG_NO_ENCODE_QUOTES":       FILTER_FLAG_NO_ENCODE_QUOTES,
	"FILTER_FLAG_ALLOW_FRACTION":         FILTER_FLAG_ALLOW_FRACTION,
	"FILTER_FLAG_ALLOW_THOUSAND":         FILTER_FLAG_ALLOW_THOUSAND,
	"FILTER_FLAG_ALLOW_SCIENTIFIC":       FILTER_FLAG_ALLOW_SCIENTIFIC,
	"FILTER_FLAG_PATH_REQUIRED":          FILTER_FLAG_PATH_REQUIRED,
	"FILTER_FLAG_QUERY_REQUIRED":         FILTER_FLAG_QUERY_REQUIRED,
	"FILTER_FLAG_IPV4":                   FILTER_FLAG_IPV4,
	"FILTER_FLAG_IPV6":                   FILTER_FLAG_IPV6,
	"FILTER_FLAG_NO_RES_RANGE":           FILTER_FLAG_NO_RES_RANGE,
	"FILTER_FLAG_NO_PRIV_RANGE":          FILTER_FLAG_NO_PRIV_RANGE,
	"FILTER_NULL_ON_FAILURE":             FILTER_NULL_ON_FAILURE,
	"FILTER_REQUIRE_SCALAR":              FILTER_REQUIRE_SCALAR,
	"FILTER_REQUIRE_ARRAY":               FILTER_REQUIRE_ARRAY,
	"FILTER_FORCE_ARRAY":                 FILTER_FORCE_ARRAY,
}

// Constants retourne une copie de la table des constantes de l'extension filter.
func Constants() map[string]int64 {
	return maps.Clone(filterConstants)
}

// FilterError représente une exception levée par les fonctions filter_*.
type FilterError struct {
	Class   string
	Message string
}

func (e FilterError) Error() string {
	return e.Class + ": " + e.Message
}

var filterListOrder = []struct {
	name string
	id   int64
}{
	{"int", FILTER_VALIDATE_INT},
	{"boolean", FILTER_VALIDATE_BOOLEAN},
	{"float", FILTER_VALIDATE_FLOAT},
	{"validate_regexp", FILTER_VALIDATE_REGEXP},
	{"validate_domain", FILTER_VALIDATE_DOMAIN},
	{"validate_url", FILTER_VALIDATE_URL},
	{"validate_email", FILTER_VALIDATE_EMAIL},
	{"validate_ip", FILTER_VALIDATE_IP},
	{"validate_mac", FILTER_VALIDATE_MAC},
	{"string", FILTER_SANITIZE_STRING},
	{"stripped", FILTER_SANITIZE_STRIPPED},
	{"encoded", FILTER_SANITIZE_ENCODED},
	{"special_chars", FILTER_SANITIZE_SPECIAL_CHARS},
	{"full_special_chars", FILTER_SANITIZE_FULL_SPECIAL_CHARS},
	{"unsafe_raw", FILTER_UNSAFE_RAW},
	{"email", FILTER_SANITIZE_EMAIL},
	{"url", FILTER_SANITIZE_URL},
	{"number_int", FILTER_SANITIZE_NUMBER_INT},
	{"number_float", FILTER_SANITIZE_NUMBER_FLOAT},
	{"add_slashes", FILTER_SANITIZE_ADD_SLASHES},
	{"callback", FILTER_CALLBACK},
}

type nativeEntry struct {
	name string
	fn   NativeFunc
}

var natives = [...]nativeEntry{
	{"filter_id", nativeFilterID},
	{"filter_list", nativeFilterList},
	{"filter_var", nativeFilterVar},
	{"filter_var_array", nativeFilterVarArray},
}

func init() {
	for i := 1; i < len(natives); i++ {
		if natives[i-1].name >= natives[i].name {
			panic("filter: table natives non triée à l'entrée " + natives[i].name)
		}
	}
}

// Lookup résout une fonction native de l'extension filter par recherche dichotomique insensible à la casse ASCII.
func Lookup(name string) (NativeFunc, bool) {
	lo, hi := 0, len(natives)
	for lo < hi {
		mid := (lo + hi) >> 1
		c := cmpFold(name, natives[mid].name)
		if c == 0 {
			return natives[mid].fn, true
		}
		if c < 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return nil, false
}

func cmpFold(name, canon string) int {
	n, m := len(name), len(canon)
	lim := min(m, n)
	for i := range lim {
		a := name[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		b := canon[i]
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
	}
	if n < m {
		return -1
	}
	if n > m {
		return 1
	}
	return 0
}

func nativeFilterList(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 0 {
		return types.NewNull(), FilterError{Class: "ArgumentCountError", Message: "filter_list() expects exactly 0 arguments"}
	}
	arr, err := types.NewArray(z)
	if err != nil {
		return types.NewNull(), err
	}
	for _, entry := range filterListOrder {
		sv, err := types.NewString(z, entry.name, 0)
		if err != nil {
			types.ReleaseArray(z, &arr)
			return types.NewNull(), err
		}
		err = types.ArrayAppend(z, &arr, sv)
		types.ReleaseString(z, &sv)
		if err != nil {
			types.ReleaseArray(z, &arr)
			return types.NewNull(), err
		}
	}
	return arr, nil
}

func nativeFilterID(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 1 {
		return types.NewNull(), FilterError{Class: "ArgumentCountError", Message: "filter_id() expects exactly 1 argument"}
	}
	v := types.Deref(z, args[0])
	if v.Type != types.TypeString {
		return types.NewNull(), FilterError{Class: "TypeError", Message: "filter_id(): Argument #1 ($name) must be of type string"}
	}
	name := types.StringData(z, v)
	for _, entry := range filterListOrder {
		if entry.name == name {
			return types.NewLong(entry.id), nil
		}
	}
	return types.NewBool(false), nil
}

type parsedFilterOptions struct {
	flags      int64
	hasDefault bool
	defaultVal types.Value
	hasMin     bool
	minRange   int64
	hasMax     bool
	maxRange   int64
	regexp     string
	decimal    string
}

func parseOptionsArg(z *zone.Zone, optVal types.Value) parsedFilterOptions {
	var out parsedFilterOptions
	out.decimal = "."
	optVal = types.Deref(z, optVal)
	if optVal.IsUndef() || optVal.IsNull() {
		return out
	}
	if optVal.Type != types.TypeArray {
		out.flags = optVal.ToLong()
		return out
	}
	types.ArrayForEach(z, optVal, func(k, v types.Value) bool {
		if k.Type != types.TypeString {
			return true
		}
		key := types.StringData(z, k)
		v = types.Deref(z, v)
		switch key {
		case "flags":
			out.flags = v.ToLong()
		case "options":
			if v.Type == types.TypeArray {
				types.ArrayForEach(z, v, func(okKey, okVal types.Value) bool {
					if okKey.Type != types.TypeString {
						return true
					}
					subKey := types.StringData(z, okKey)
					okVal = types.Deref(z, okVal)
					switch subKey {
					case "default":
						out.hasDefault = true
						out.defaultVal = okVal
					case "min_range":
						out.hasMin = true
						out.minRange = okVal.ToLong()
					case "max_range":
						out.hasMax = true
						out.maxRange = okVal.ToLong()
					case "regexp":
						if okVal.Type == types.TypeString {
							out.regexp = types.StringData(z, okVal)
						}
					case "decimal":
						if okVal.Type == types.TypeString {
							out.decimal = types.StringData(z, okVal)
						}
					}
					return true
				})
			}
		}
		return true
	})
	return out
}

func failureResult(z *zone.Zone, opts parsedFilterOptions) types.Value {
	if opts.hasDefault {
		return types.RetainValue(z, opts.defaultVal)
	}
	if opts.flags&FILTER_NULL_ON_FAILURE != 0 {
		return types.NewNull()
	}
	return types.NewBool(false)
}

func nativeFilterVar(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return types.NewNull(), FilterError{Class: "ArgumentCountError", Message: "filter_var() expects between 1 and 3 arguments"}
	}
	val := types.Deref(z, args[0])
	filterID := FILTER_DEFAULT
	if len(args) >= 2 && !types.Deref(z, args[1]).IsNull() {
		filterID = types.Deref(z, args[1]).ToLong()
	}
	var optArg types.Value
	if len(args) == 3 {
		optArg = args[2]
	}
	opts := parseOptionsArg(z, optArg)
	return applyFilterValue(z, val, filterID, opts)
}

func applyFilterValue(z *zone.Zone, val types.Value, filterID int64, opts parsedFilterOptions) (types.Value, error) {
	val = types.Deref(z, val)
	if opts.flags&FILTER_REQUIRE_ARRAY != 0 && val.Type != types.TypeArray {
		return failureResult(z, opts), nil
	}
	if opts.flags&FILTER_REQUIRE_SCALAR != 0 && val.Type == types.TypeArray && opts.flags&FILTER_FORCE_ARRAY == 0 {
		return failureResult(z, opts), nil
	}
	if val.Type == types.TypeArray {
		outArr, err := types.NewArray(z)
		if err != nil {
			return types.NewNull(), err
		}
		elemOpts := opts
		elemOpts.flags &^= FILTER_REQUIRE_ARRAY | FILTER_FORCE_ARRAY
		var iterErr error
		types.ArrayForEach(z, val, func(k, elem types.Value) bool {
			filtered, err := applyFilterScalar(z, elem, filterID, elemOpts)
			if err != nil {
				iterErr = err
				return false
			}
			err = types.ArraySet(z, &outArr, k, filtered, nil)
			types.ReleaseValue(z, &filtered)
			if err != nil {
				iterErr = err
				return false
			}
			return true
		})
		if iterErr != nil {
			types.ReleaseArray(z, &outArr)
			return types.NewNull(), iterErr
		}
		return outArr, nil
	}
	res, err := applyFilterScalar(z, val, filterID, opts)
	if err != nil {
		return types.NewNull(), err
	}
	if opts.flags&FILTER_FORCE_ARRAY != 0 {
		arr, err := types.NewArray(z)
		if err != nil {
			types.ReleaseValue(z, &res)
			return types.NewNull(), err
		}
		err = types.ArrayAppend(z, &arr, res)
		types.ReleaseValue(z, &res)
		if err != nil {
			types.ReleaseArray(z, &arr)
			return types.NewNull(), err
		}
		return arr, nil
	}
	return res, nil
}

func scalarToFilterString(z *zone.Zone, v types.Value) (string, bool) {
	v = types.Deref(z, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull, types.TypeFalse:
		return "", true
	case types.TypeTrue:
		return "1", true
	case types.TypeLong:
		return strconv.FormatInt(v.ToLong(), 10), true
	case types.TypeDouble:
		return strconv.FormatFloat(v.ToDouble(), 'G', 14, 64), true
	case types.TypeString:
		return types.StringData(z, v), true
	default:
		return "", false
	}
}

func applyFilterScalar(z *zone.Zone, val types.Value, filterID int64, opts parsedFilterOptions) (types.Value, error) {
	val = types.Deref(z, val)
	if val.Type == types.TypeArray || val.Type == types.TypeObject {
		return failureResult(z, opts), nil
	}

	switch filterID {
	case FILTER_VALIDATE_INT:
		return filterValidateInt(z, val, opts), nil
	case FILTER_VALIDATE_BOOLEAN:
		return filterValidateBool(z, val, opts), nil
	case FILTER_VALIDATE_FLOAT:
		return filterValidateFloat(z, val, opts), nil
	case FILTER_VALIDATE_EMAIL:
		return filterValidateEmail(z, val, opts)
	case FILTER_VALIDATE_URL:
		return filterValidateURL(z, val, opts)
	case FILTER_VALIDATE_IP:
		return filterValidateIP(z, val, opts)
	case FILTER_VALIDATE_MAC:
		return filterValidateMAC(z, val, opts)
	case FILTER_VALIDATE_DOMAIN:
		return filterValidateDomain(z, val, opts)
	case FILTER_VALIDATE_REGEXP:
		return filterValidateRegexp(z, val, opts)
	case FILTER_DEFAULT, FILTER_SANITIZE_STRING:
		return filterSanitizeRaw(z, val, opts)
	case FILTER_SANITIZE_EMAIL:
		return filterSanitizeEmail(z, val, opts)
	case FILTER_SANITIZE_URL:
		return filterSanitizeURL(z, val, opts)
	case FILTER_SANITIZE_NUMBER_INT:
		return filterSanitizeNumberInt(z, val, opts)
	case FILTER_SANITIZE_NUMBER_FLOAT:
		return filterSanitizeNumberFloat(z, val, opts)
	case FILTER_SANITIZE_SPECIAL_CHARS:
		return filterSanitizeSpecialChars(z, val, opts)
	case FILTER_SANITIZE_FULL_SPECIAL_CHARS:
		return filterSanitizeFullSpecialChars(z, val, opts)
	case FILTER_SANITIZE_ENCODED:
		return filterSanitizeEncoded(z, val, opts)
	case FILTER_SANITIZE_ADD_SLASHES:
		return filterSanitizeAddSlashes(z, val, opts)
	default:
		return failureResult(z, opts), nil
	}
}

func checkIntRange(n int64, opts parsedFilterOptions) bool {
	if opts.hasMin && n < opts.minRange {
		return false
	}
	if opts.hasMax && n > opts.maxRange {
		return false
	}
	return true
}

func filterValidateInt(z *zone.Zone, val types.Value, opts parsedFilterOptions) types.Value {
	if val.Type == types.TypeLong {
		n := val.ToLong()
		if !checkIntRange(n, opts) {
			return failureResult(z, opts)
		}
		return types.NewLong(n)
	}
	if val.Type == types.TypeTrue {
		if !checkIntRange(1, opts) {
			return failureResult(z, opts)
		}
		return types.NewLong(1)
	}
	if val.Type == types.TypeDouble {
		f := val.ToDouble()
		n := int64(f)
		if float64(n) == f && !math.IsNaN(f) && !math.IsInf(f, 0) && checkIntRange(n, opts) {
			return types.NewLong(n)
		}
		return failureResult(z, opts)
	}
	if val.Type != types.TypeString {
		return failureResult(z, opts)
	}
	s := strings.TrimSpace(types.StringData(z, val))
	if s == "" {
		return failureResult(z, opts)
	}
	if opts.flags&FILTER_FLAG_ALLOW_HEX != 0 && (strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
		n, err := strconv.ParseInt(s[2:], 16, 64)
		if err == nil && checkIntRange(n, opts) {
			return types.NewLong(n)
		}
		return failureResult(z, opts)
	}
	if opts.flags&FILTER_FLAG_ALLOW_OCTAL != 0 && strings.HasPrefix(s, "0") && len(s) > 1 {
		n, err := strconv.ParseInt(s[1:], 8, 64)
		if err == nil && checkIntRange(n, opts) {
			return types.NewLong(n)
		}
		return failureResult(z, opts)
	}
	// Entier décimal canonique (sans zéro en tête sauf "0" / "+0" / "-0").
	check := s
	if strings.HasPrefix(check, "+") || strings.HasPrefix(check, "-") {
		check = check[1:]
	}
	if len(check) > 1 && check[0] == '0' {
		return failureResult(z, opts)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || !checkIntRange(n, opts) {
		return failureResult(z, opts)
	}
	return types.NewLong(n)
}

func filterValidateBool(z *zone.Zone, val types.Value, opts parsedFilterOptions) types.Value {
	if val.Type == types.TypeTrue {
		return types.NewBool(true)
	}
	if val.Type == types.TypeFalse || val.Type == types.TypeNull || val.Type == types.TypeUndef {
		return types.NewBool(false)
	}
	s, ok := scalarToFilterString(z, val)
	if !ok {
		return failureResult(z, opts)
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "on", "yes":
		return types.NewBool(true)
	case "0", "false", "off", "no", "":
		return types.NewBool(false)
	default:
		return failureResult(z, opts)
	}
}

func filterValidateFloat(z *zone.Zone, val types.Value, opts parsedFilterOptions) types.Value {
	if val.Type == types.TypeLong || val.Type == types.TypeDouble || val.Type == types.TypeTrue {
		f := val.ToDouble()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return failureResult(z, opts)
		}
		return types.NewDouble(f)
	}
	if val.Type != types.TypeString {
		return failureResult(z, opts)
	}
	s := strings.TrimSpace(types.StringData(z, val))
	if s == "" {
		return failureResult(z, opts)
	}
	if opts.flags&FILTER_FLAG_ALLOW_THOUSAND != 0 {
		s = strings.ReplaceAll(s, ",", "")
	}
	if opts.decimal != "" && opts.decimal != "." {
		s = strings.ReplaceAll(s, opts.decimal, ".")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return failureResult(z, opts)
	}
	return types.NewDouble(f)
}

var emailRegexp = regexp.MustCompile(`^[a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$`)

func filterValidateEmail(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok || len(s) > 320 || !emailRegexp.MatchString(s) || strings.Contains(s, "..") {
		return failureResult(z, opts), nil
	}
	return types.NewString(z, s, 0)
}

func filterValidateURL(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok || s == "" {
		return failureResult(z, opts), nil
	}
	for i := range len(s) {
		if s[i] <= 0x20 || s[i] >= 0x7f {
			return failureResult(z, opts), nil
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || (u.Host == "" && u.Scheme != "mailto" && u.Scheme != "news" && u.Scheme != "file") {
		return failureResult(z, opts), nil
	}
	if opts.flags&FILTER_FLAG_PATH_REQUIRED != 0 && (u.Path == "" || u.Path == "/") && !strings.HasSuffix(s, "/") {
		return failureResult(z, opts), nil
	}
	if opts.flags&FILTER_FLAG_QUERY_REQUIRED != 0 && u.RawQuery == "" {
		return failureResult(z, opts), nil
	}
	return types.NewString(z, s, 0)
}

func filterValidateIP(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok || s == "" {
		return failureResult(z, opts), nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return failureResult(z, opts), nil
	}
	isV4 := ip.To4() != nil && !strings.Contains(s, ":")
	if opts.flags&FILTER_FLAG_IPV4 != 0 && opts.flags&FILTER_FLAG_IPV6 == 0 && !isV4 {
		return failureResult(z, opts), nil
	}
	if opts.flags&FILTER_FLAG_IPV6 != 0 && opts.flags&FILTER_FLAG_IPV4 == 0 && isV4 {
		return failureResult(z, opts), nil
	}
	if opts.flags&FILTER_FLAG_NO_PRIV_RANGE != 0 && ip.IsPrivate() {
		return failureResult(z, opts), nil
	}
	if opts.flags&FILTER_FLAG_NO_RES_RANGE != 0 && (ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsMulticast()) {
		return failureResult(z, opts), nil
	}
	return types.NewString(z, s, 0)
}

var macRegexp = regexp.MustCompile(`^([0-9A-Fa-f]{2}[:-]){5}([0-9A-Fa-f]{2})$|^([0-9A-Fa-f]{4}\.){2}([0-9A-Fa-f]{4})$`)

func filterValidateMAC(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok || !macRegexp.MatchString(s) {
		return failureResult(z, opts), nil
	}
	return types.NewString(z, s, 0)
}

var domainLabelRegexp = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func filterValidateDomain(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok || s == "" || len(s) > 253 {
		return failureResult(z, opts), nil
	}
	trimmed := strings.TrimSuffix(s, ".")
	for p := range strings.SplitSeq(trimmed, ".") {
		if !domainLabelRegexp.MatchString(p) {
			return failureResult(z, opts), nil
		}
	}
	return types.NewString(z, s, 0)
}

func filterValidateRegexp(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	if opts.regexp == "" {
		return failureResult(z, opts), nil
	}
	s, ok := scalarToFilterString(z, val)
	if !ok {
		return failureResult(z, opts), nil
	}
	pattern := opts.regexp
	if len(pattern) >= 2 && (pattern[0] == '/' || pattern[0] == '#' || pattern[0] == '~') {
		delim := pattern[0]
		if last := strings.LastIndexByte(pattern[1:], delim); last >= 0 {
			flagsPart := pattern[1+last+1:]
			body := pattern[1 : 1+last]
			if strings.IndexByte(flagsPart, 'i') >= 0 {
				body = "(?i)" + body
			}
			pattern = body
		}
	}
	re, err := regexp.Compile(pattern)
	if err != nil || !re.MatchString(s) {
		return failureResult(z, opts), nil
	}
	return types.NewString(z, s, 0)
}

func applyStripEncodeFlags(s string, flags int64) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if flags&FILTER_FLAG_STRIP_LOW != 0 && c < 32 {
			continue
		}
		if flags&FILTER_FLAG_STRIP_HIGH != 0 && c > 127 {
			continue
		}
		if flags&FILTER_FLAG_STRIP_BACKTICK != 0 && c == '`' {
			continue
		}
		if flags&FILTER_FLAG_ENCODE_LOW != 0 && c < 32 {
			fmt.Fprintf(&b, "&#%d;", c)
			continue
		}
		if flags&FILTER_FLAG_ENCODE_HIGH != 0 && c > 127 {
			fmt.Fprintf(&b, "&#%d;", c)
			continue
		}
		if flags&FILTER_FLAG_ENCODE_AMP != 0 && c == '&' {
			b.WriteString("&#38;")
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func filterSanitizeRaw(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok {
		return failureResult(z, opts), nil
	}
	return types.NewString(z, applyStripEncodeFlags(s, opts.flags), 0)
}

func filterSanitizeEmail(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok {
		return failureResult(z, opts), nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			strings.IndexByte("!#$%&'*+-=?^_`{|}~@.[]", c) >= 0 {
			b.WriteByte(c)
		}
	}
	return types.NewString(z, b.String(), 0)
}

func filterSanitizeURL(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok {
		return failureResult(z, opts), nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if c > 0x20 && c < 0x7f && c != '<' && c != '>' && c != '"' {
			b.WriteByte(c)
		}
	}
	return types.NewString(z, b.String(), 0)
}

func filterSanitizeNumberInt(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok {
		return failureResult(z, opts), nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '+' || c == '-' {
			b.WriteByte(c)
		}
	}
	return types.NewString(z, b.String(), 0)
}

func filterSanitizeNumberFloat(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok {
		return failureResult(z, opts), nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := range len(s) {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '+' || c == '-' {
			b.WriteByte(c)
			continue
		}
		if opts.flags&FILTER_FLAG_ALLOW_FRACTION != 0 && c == '.' {
			b.WriteByte(c)
			continue
		}
		if opts.flags&FILTER_FLAG_ALLOW_THOUSAND != 0 && c == ',' {
			b.WriteByte(c)
			continue
		}
		if opts.flags&FILTER_FLAG_ALLOW_SCIENTIFIC != 0 && (c == 'e' || c == 'E') {
			b.WriteByte(c)
			continue
		}
	}
	return types.NewString(z, b.String(), 0)
}

func filterSanitizeSpecialChars(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok {
		return failureResult(z, opts), nil
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	noQuotes := opts.flags&FILTER_FLAG_NO_ENCODE_QUOTES != 0
	for i := range len(s) {
		c := s[i]
		if c < 32 {
			if opts.flags&FILTER_FLAG_STRIP_LOW != 0 {
				continue
			}
			fmt.Fprintf(&b, "&#%d;", c)
			continue
		}
		if c > 127 {
			if opts.flags&FILTER_FLAG_STRIP_HIGH != 0 {
				continue
			}
			if opts.flags&FILTER_FLAG_ENCODE_HIGH != 0 {
				fmt.Fprintf(&b, "&#%d;", c)
				continue
			}
		}
		switch c {
		case '<':
			b.WriteString("&#60;")
		case '>':
			b.WriteString("&#62;")
		case '&':
			b.WriteString("&#38;")
		case '"':
			if noQuotes {
				b.WriteByte('"')
			} else {
				b.WriteString("&#34;")
			}
		case '\'':
			if noQuotes {
				b.WriteByte('\'')
			} else {
				b.WriteString("&#39;")
			}
		default:
			b.WriteByte(c)
		}
	}
	return types.NewString(z, b.String(), 0)
}

func filterSanitizeFullSpecialChars(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok {
		return failureResult(z, opts), nil
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	noQuotes := opts.flags&FILTER_FLAG_NO_ENCODE_QUOTES != 0
	for i := range len(s) {
		c := s[i]
		switch c {
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		case '"':
			if noQuotes {
				b.WriteByte('"')
			} else {
				b.WriteString("&quot;")
			}
		case '\'':
			if noQuotes {
				b.WriteByte('\'')
			} else {
				b.WriteString("&#039;")
			}
		default:
			b.WriteByte(c)
		}
	}
	return types.NewString(z, b.String(), 0)
}

func filterSanitizeEncoded(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok {
		return failureResult(z, opts), nil
	}
	return types.NewString(z, url.QueryEscape(s), 0)
}

func filterSanitizeAddSlashes(z *zone.Zone, val types.Value, opts parsedFilterOptions) (types.Value, error) {
	s, ok := scalarToFilterString(z, val)
	if !ok {
		return failureResult(z, opts), nil
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := range len(s) {
		c := s[i]
		switch c {
		case '\'', '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case 0:
			b.WriteString(`\0`)
		default:
			b.WriteByte(c)
		}
	}
	return types.NewString(z, b.String(), 0)
}

func nativeFilterVarArray(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return types.NewNull(), FilterError{Class: "ArgumentCountError", Message: "filter_var_array() expects between 1 and 3 arguments"}
	}
	dataArr := types.Deref(z, args[0])
	if dataArr.Type != types.TypeArray {
		return types.NewNull(), FilterError{Class: "TypeError", Message: "filter_var_array(): Argument #1 ($array) must be of type array"}
	}
	addEmpty := true
	if len(args) == 3 && !types.Deref(z, args[2]).IsNull() {
		addEmpty = types.ToBoolZ(z, args[2])
	}

	outArr, err := types.NewArray(z)
	if err != nil {
		return types.NewNull(), err
	}

	// Si options est un entier ou omis, applique ce filtre à chaque entrée de dataArr.
	if len(args) == 1 || types.Deref(z, args[1]).Type != types.TypeArray {
		filterID := FILTER_DEFAULT
		if len(args) >= 2 && !types.Deref(z, args[1]).IsNull() {
			filterID = types.Deref(z, args[1]).ToLong()
		}
		var iterErr error
		types.ArrayForEach(z, dataArr, func(k, v types.Value) bool {
			filtered, err := applyFilterValue(z, v, filterID, parsedFilterOptions{decimal: "."})
			if err != nil {
				iterErr = err
				return false
			}
			err = types.ArraySet(z, &outArr, k, filtered, nil)
			types.ReleaseValue(z, &filtered)
			if err != nil {
				iterErr = err
				return false
			}
			return true
		})
		if iterErr != nil {
			types.ReleaseArray(z, &outArr)
			return types.NewNull(), iterErr
		}
		return outArr, nil
	}

	defArr := types.Deref(z, args[1])
	var defErr error
	types.ArrayForEach(z, defArr, func(fieldKey, specVal types.Value) bool {
		rawVal, exists, err := types.ArrayGet(z, dataArr, fieldKey, nil)
		if err != nil {
			defErr = err
			return false
		}
		if !exists {
			if addEmpty {
				if err := types.ArraySet(z, &outArr, fieldKey, types.NewNull(), nil); err != nil {
					defErr = err
					return false
				}
			}
			return true
		}

		specVal = types.Deref(z, specVal)
		filterID := FILTER_DEFAULT
		var opts parsedFilterOptions
		opts.decimal = "."
		if specVal.Type == types.TypeArray {
			fk, err := types.NewString(z, "filter", 0)
			if err != nil {
				defErr = err
				return false
			}
			if fv, ok, _ := types.ArrayGet(z, specVal, fk, nil); ok {
				filterID = types.Deref(z, fv).ToLong()
			}
			types.ReleaseString(z, &fk)
			opts = parseOptionsArg(z, specVal)
		} else {
			filterID = specVal.ToLong()
		}

		filtered, err := applyFilterValue(z, rawVal, filterID, opts)
		if err != nil {
			defErr = err
			return false
		}
		err = types.ArraySet(z, &outArr, fieldKey, filtered, nil)
		types.ReleaseValue(z, &filtered)
		if err != nil {
			defErr = err
			return false
		}
		return true
	})
	if defErr != nil {
		types.ReleaseArray(z, &outArr)
		return types.NewNull(), defErr
	}
	return outArr, nil
}
