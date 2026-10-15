// Code créé par agent — Lot 4.3 : fonctions mathématiques, aléatoires et d'environnement PHP 8.3.
package stdlib

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const phpMtRandMax = 2147483647

func nativeMtGetrandmax(_ *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mt_getrandmax", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewLong(phpMtRandMax), nil
}

func nativeGetrandmax(_ *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("getrandmax", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewLong(phpMtRandMax), nil
}

func randCommon(z *zone.Zone, name string, args []types.Value) (types.Value, error) {
	if len(args) != 0 && len(args) != 2 {
		return types.Value{}, arity(name, len(args), 0, 2)
	}
	if len(args) == 0 {
		return types.NewLong(rand.Int64N(phpMtRandMax + 1)), nil
	}
	minVal, err := asIntParam(z, args[0], name, "1", "min")
	if err != nil {
		return types.Value{}, err
	}
	maxVal, err := asIntParam(z, args[1], name, "2", "max")
	if err != nil {
		return types.Value{}, err
	}
	if maxVal < minVal {
		return types.Value{}, valErr(name, "2", "max", "must be greater than or equal to argument #1 ($min)")
	}
	if minVal == maxVal {
		return types.NewLong(minVal), nil
	}
	span := uint64(maxVal - minVal)
	if span == math.MaxUint64 {
		return types.NewLong(int64(rand.Uint64())), nil
	}
	return types.NewLong(minVal + int64(rand.Uint64N(span+1))), nil
}

func nativeMtRand(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return randCommon(z, "mt_rand", args)
}

func nativeRand(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return randCommon(z, "rand", args)
}

func nativeRandomInt(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("random_int", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	minVal, err := asIntParam(z, args[0], "random_int", "1", "min")
	if err != nil {
		return types.Value{}, err
	}
	maxVal, err := asIntParam(z, args[1], "random_int", "2", "max")
	if err != nil {
		return types.Value{}, err
	}
	if minVal > maxVal {
		return types.Value{}, valErr("random_int", "1", "min", "must be less than or equal to argument #2 ($max)")
	}
	if minVal == maxVal {
		return types.NewLong(minVal), nil
	}
	span := uint64(maxVal - minVal)
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		return types.Value{}, err
	}
	u := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
	if span == math.MaxUint64 {
		return types.NewLong(int64(u)), nil
	}
	return types.NewLong(minVal + int64(u%(span+1))), nil
}

func nativeRandomBytes(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("random_bytes", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	length, err := asIntParam(z, args[0], "random_bytes", "1", "length")
	if err != nil {
		return types.Value{}, err
	}
	if length < 1 {
		return types.Value{}, valErr("random_bytes", "1", "length", "must be greater than 0")
	}
	buf := make([]byte, length)
	if _, err := crand.Read(buf); err != nil {
		return types.Value{}, err
	}
	return newBytes(z, buf)
}

func nativeLcgValue(_ *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("lcg_value", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewDouble(rand.Float64()), nil
}

func nativeAtan2(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("atan2", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	y, err := asFloatParam(z, args[0], "atan2", "1", "y")
	if err != nil {
		return types.Value{}, err
	}
	x, err := asFloatParam(z, args[1], "atan2", "2", "x")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewDouble(math.Atan2(y, x)), nil
}

func nativeSinh(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "sinh", math.Sinh)
}

func nativeCosh(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "cosh", math.Cosh)
}

func nativeTanh(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "tanh", math.Tanh)
}

func nativeAsinh(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "asinh", math.Asinh)
}

func nativeAcosh(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "acosh", math.Acosh)
}

func nativeAtanh(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "atanh", math.Atanh)
}

func nativeExpm1(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "expm1", math.Expm1)
}

func nativeLog1p(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "log1p", math.Log1p)
}

func nativePi(_ *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("pi", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewDouble(math.Pi), nil
}

func nativeGetenv(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("getenv", len(args), 0, 2); err != nil {
		return types.Value{}, err
	}
	if len(args) == 0 || deref(z, args[0]).IsNull() || deref(z, args[0]).IsUndef() {
		envs := os.Environ()
		arr, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		for _, kv := range envs {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			ks, err := newStr(z, k)
			if err != nil {
				return types.Value{}, err
			}
			vv, err := newStr(z, v)
			if err != nil {
				types.ReleaseValue(z, &ks)
				return types.Value{}, err
			}
			err = setKey(z, &arr, ks, vv)
			types.ReleaseValue(z, &ks)
			types.ReleaseValue(z, &vv)
			if err != nil {
				return types.Value{}, err
			}
		}
		return arr, nil
	}
	name, err := asStringParam(z, args[0], "getenv", "1", "name")
	if err != nil {
		return types.Value{}, err
	}
	v, ok := os.LookupEnv(name)
	if !ok {
		return types.NewBool(false), nil
	}
	return newStr(z, v)
}

func nativePutenv(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("putenv", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	assign, err := asStringParam(z, args[0], "putenv", "1", "assignment")
	if err != nil {
		return types.Value{}, err
	}
	if assign == "" || strings.HasPrefix(assign, "=") {
		return types.NewBool(false), nil
	}
	k, v, hasEq := strings.Cut(assign, "=")
	if !hasEq {
		if err := os.Unsetenv(k); err != nil {
			return types.NewBool(false), nil
		}
		return types.NewBool(true), nil
	}
	if err := os.Setenv(k, v); err != nil {
		return types.NewBool(false), nil
	}
	return types.NewBool(true), nil
}

func nativePhpversion(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("phpversion", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	if len(args) == 1 && !deref(z, args[0]).IsNull() && !deref(z, args[0]).IsUndef() {
		ext, err := asStringParam(z, args[0], "phpversion", "1", "extension")
		if err != nil {
			return types.Value{}, err
		}
		switch strings.ToLower(ext) {
		case "core", "standard", "date", "json", "pcre", "spl", "reflection", "pdo", "pdo_sqlite", "hash", "ctype", "mbstring", "iconv", "openssl", "zlib":
			return newStr(z, "8.3.0")
		default:
			return types.NewBool(false), nil
		}
	}
	return newStr(z, "8.3.0")
}

func nativePhpSapiName(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("php_sapi_name", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return newStr(z, "cli")
}

func nativeZendVersion(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("zend_version", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return newStr(z, "4.3.0")
}

func nativeExp(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "exp", math.Exp)
}

func nativeSin(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "sin", math.Sin)
}

func nativeCos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "cos", math.Cos)
}

func nativeTan(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "tan", math.Tan)
}

func nativeAsin(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "asin", math.Asin)
}

func nativeAcos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "acos", math.Acos)
}

func nativeAtan(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "atan", math.Atan)
}

func nativeDeg2rad(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "deg2rad", func(d float64) float64 {
		return d * (math.Pi / 180.0)
	})
}

func nativeRad2deg(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "rad2deg", func(r float64) float64 {
		return r * (180.0 / math.Pi)
	})
}

func nativeLog(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("log", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	num, err := asFloatParam(z, args[0], "log", "1", "num")
	if err != nil {
		return types.Value{}, err
	}
	if len(args) == 1 {
		return types.NewDouble(math.Log(num)), nil
	}
	base, err := asFloatParam(z, args[1], "log", "2", "base")
	if err != nil {
		return types.Value{}, err
	}
	if base <= 0.0 {
		return types.Value{}, valErr("log", "2", "base", "must be greater than 0")
	}
	if base == 1.0 {
		return types.NewDouble(math.NaN()), nil
	}
	return types.NewDouble(math.Log(num) / math.Log(base)), nil
}

func nativeLog10(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "log10", math.Log10)
}

func nativeLog2(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return unaryFloat(z, args, "log2", math.Log2)
}

func nativeHypot(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hypot", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	x, err := asFloatParam(z, args[0], "hypot", "1", "x")
	if err != nil {
		return types.Value{}, err
	}
	y, err := asFloatParam(z, args[1], "hypot", "2", "y")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewDouble(math.Hypot(x, y)), nil
}

func nativeDechex(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("dechex", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	n, err := asIntParam(z, args[0], "dechex", "1", "num")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, strconv.FormatUint(uint64(n), 16))
}

func nativeDecbin(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("decbin", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	n, err := asIntParam(z, args[0], "decbin", "1", "num")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, strconv.FormatUint(uint64(n), 2))
}

func nativeDecoct(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("decoct", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	n, err := asIntParam(z, args[0], "decoct", "1", "num")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, strconv.FormatUint(uint64(n), 8))
}

func parseBaseString(s string, base int) types.Value {
	var acc uint64
	var fAcc float64
	overflow := false
	b := uint64(base)
	fb := float64(base)
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'z':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'Z':
			d = int(c-'A') + 10
		default:
			continue
		}
		if d >= base {
			continue
		}
		if !overflow {
			if acc > (uint64(math.MaxInt64)-uint64(d))/b {
				overflow = true
				fAcc = float64(acc)*fb + float64(d)
			} else {
				acc = acc*b + uint64(d)
			}
		} else {
			fAcc = fAcc*fb + float64(d)
		}
	}
	if overflow {
		return types.NewDouble(fAcc)
	}
	return types.NewLong(int64(acc))
}

func nativeHexdec(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hexdec", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "hexdec", "1", "hex_string")
	if err != nil {
		return types.Value{}, err
	}
	return parseBaseString(s, 16), nil
}

func nativeBindec(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("bindec", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "bindec", "1", "binary_string")
	if err != nil {
		return types.Value{}, err
	}
	return parseBaseString(s, 2), nil
}

func nativeOctdec(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("octdec", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "octdec", "1", "octal_string")
	if err != nil {
		return types.Value{}, err
	}
	return parseBaseString(s, 8), nil
}

func nativeBaseConvert(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("base_convert", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	numStr, err := asStringParam(z, args[0], "base_convert", "1", "num")
	if err != nil {
		return types.Value{}, err
	}
	fromBase, err := asIntParam(z, args[1], "base_convert", "2", "from_base")
	if err != nil {
		return types.Value{}, err
	}
	toBase, err := asIntParam(z, args[2], "base_convert", "3", "to_base")
	if err != nil {
		return types.Value{}, err
	}
	if fromBase < 2 || fromBase > 36 {
		return types.Value{}, valErr("base_convert", "2", "from_base", "must be between 2 and 36 (inclusive)")
	}
	if toBase < 2 || toBase > 36 {
		return types.Value{}, valErr("base_convert", "3", "to_base", "must be between 2 and 36 (inclusive)")
	}
	v := parseBaseString(numStr, int(fromBase))
	if v.IsLong() {
		return newStr(z, strconv.FormatUint(uint64(v.ToLong()), int(toBase)))
	}
	return newStr(z, strconv.FormatUint(uint64(v.ToDouble()), int(toBase)))
}

func nativeMtSrand(name string) NativeFunc {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 0, 2); err != nil {
			return types.Value{}, err
		}
		return types.NewNull(), nil
	}
}

func nativeIsIterable(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_iterable", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	v := deref(z, args[0])
	if v.Type == types.TypeArray {
		return types.NewBool(true), nil
	}
	if v.Type == types.TypeObject {
		cn := strings.ToLower(typeKind(z, v))
		if cn == "arrayiterator" || cn == "generator" || cn == "splfixedarray" {
			return types.NewBool(true), nil
		}
	}
	return types.NewBool(false), nil
}

func nativeIsCountable(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_countable", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	v := deref(z, args[0])
	if v.Type == types.TypeArray {
		return types.NewBool(true), nil
	}
	if v.Type == types.TypeObject {
		cn := strings.ToLower(typeKind(z, v))
		if cn == "arrayiterator" || cn == "splfixedarray" || cn == "splobjectstorage" {
			return types.NewBool(true), nil
		}
	}
	return types.NewBool(false), nil
}

func nativeSettype(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("settype", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	tp, err := asStringParam(z, args[1], "settype", "2", "type")
	if err != nil {
		return types.Value{}, err
	}
	var conv types.Value
	switch strings.ToLower(tp) {
	case "boolean", "bool":
		conv, err = nativeBoolval(z, out, args[:1])
	case "integer", "int":
		conv, err = nativeIntval(z, out, args[:1])
	case "float", "double":
		conv, err = nativeFloatval(z, out, args[:1])
	case "string":
		conv, err = nativeStrval(z, out, args[:1])
	case "null":
		conv = types.NewNull()
	default:
		return types.Value{}, valErr("settype", "2", "type", "must be a valid type")
	}
	if err != nil {
		return types.Value{}, err
	}
	if args[0].Type == types.TypeReference {
		types.AssignRef(z, args[0], conv)
	}
	return types.NewBool(true), nil
}

func normalizeVersionParts(v string) []string {
	var out []string
	cur := strings.Builder{}
	lastKind := 0 // 1=digit, 2=alpha
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '.' || c == '-' || c == '_' || c == '+' {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			lastKind = 0
			continue
		}
		kind := 2
		if c >= '0' && c <= '9' {
			kind = 1
		}
		if lastKind != 0 && kind != lastKind && cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteByte(c)
		lastKind = kind
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func versionPartRank(p string) int {
	switch strings.ToLower(p) {
	case "dev":
		return 0
	case "alpha", "a":
		return 1
	case "beta", "b":
		return 2
	case "rc", "p", "pl":
		if strings.ToLower(p) == "rc" {
			return 3
		}
		return 5
	default:
		return 4
	}
}

func cmpVersionStrings(v1, v2 string) int {
	p1 := normalizeVersionParts(v1)
	p2 := normalizeVersionParts(v2)
	n := max(len(p1), len(p2))
	for i := range n {
		var a, b string
		if i < len(p1) {
			a = p1[i]
		}
		if i < len(p2) {
			b = p2[i]
		}
		if a == b {
			continue
		}
		if a == "" {
			r2 := versionPartRank(b)
			if r2 < 4 {
				return 1
			}
			if b == "0" {
				continue
			}
			return -1
		}
		if b == "" {
			r1 := versionPartRank(a)
			if r1 < 4 {
				return -1
			}
			if a == "0" {
				continue
			}
			return 1
		}
		n1, e1 := strconv.ParseInt(a, 10, 64)
		n2, e2 := strconv.ParseInt(b, 10, 64)
		if e1 == nil && e2 == nil {
			if n1 < n2 {
				return -1
			}
			if n1 > n2 {
				return 1
			}
			continue
		}
		r1 := versionPartRank(a)
		r2 := versionPartRank(b)
		if r1 != r2 {
			if r1 < r2 {
				return -1
			}
			return 1
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
	}
	return 0
}

func nativeVersionCompare(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("version_compare", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	v1, err := asStringParam(z, args[0], "version_compare", "1", "version1")
	if err != nil {
		return types.Value{}, err
	}
	v2, err := asStringParam(z, args[1], "version_compare", "2", "version2")
	if err != nil {
		return types.Value{}, err
	}
	c := cmpVersionStrings(v1, v2)
	if len(args) == 3 && !deref(z, args[2]).IsNull() && !deref(z, args[2]).IsUndef() {
		op, err := asStringParam(z, args[2], "version_compare", "3", "operator")
		if err != nil {
			return types.Value{}, err
		}
		switch strings.ToLower(op) {
		case "<", "lt":
			return types.NewBool(c < 0), nil
		case "<=", "le":
			return types.NewBool(c <= 0), nil
		case ">", "gt":
			return types.NewBool(c > 0), nil
		case ">=", "ge":
			return types.NewBool(c >= 0), nil
		case "==", "=", "eq":
			return types.NewBool(c == 0), nil
		case "!=", "<>", "ne":
			return types.NewBool(c != 0), nil
		default:
			return types.Value{}, valErr("version_compare", "3", "operator", "must be a valid comparison operator")
		}
	}
	return types.NewLong(int64(c)), nil
}

func nativeUniqid(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("uniqid", len(args), 0, 2); err != nil {
		return types.Value{}, err
	}
	prefix := ""
	if len(args) >= 1 {
		var err error
		prefix, err = asStringParam(z, args[0], "uniqid", "1", "prefix")
		if err != nil {
			return types.Value{}, err
		}
	}
	moreEntropy := false
	if len(args) == 2 {
		bv, err := nativeBoolval(z, out, args[1:2])
		if err != nil {
			return types.Value{}, err
		}
		moreEntropy = bv.Type == types.TypeTrue
	}
	now := time.Now()
	sec := now.Unix()
	usec := now.Nanosecond() / 1000
	s := fmt.Sprintf("%s%08x%05x", prefix, sec, usec)
	if moreEntropy {
		s += fmt.Sprintf(".%08d", rand.IntN(100000000))
	}
	return newStr(z, s)
}

func nativeIniGet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("ini_get", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	opt, err := asStringParam(z, args[0], "ini_get", "1", "option")
	if err != nil {
		return types.Value{}, err
	}
	switch opt {
	case "memory_limit":
		return newStr(z, "128M")
	case "max_execution_time":
		return newStr(z, "0")
	case "display_errors":
		return newStr(z, "1")
	case "error_reporting":
		return newStr(z, "32767")
	case "default_charset":
		return newStr(z, "UTF-8")
	case "date.timezone":
		return newStr(z, "UTC")
	default:
		return newStr(z, "")
	}
}

func nativeIniSet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("ini_set", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	if _, err := asStringParam(z, args[0], "ini_set", "1", "option"); err != nil {
		return types.Value{}, err
	}
	if _, err := asStringParam(z, args[1], "ini_set", "2", "value"); err != nil {
		return types.Value{}, err
	}
	return newStr(z, "")
}

func nativeErrorLog(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("error_log", len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	if _, err := asStringParam(z, args[0], "error_log", "1", "message"); err != nil {
		return types.Value{}, err
	}
	return types.NewBool(true), nil
}

func nativeGcCollectCycles(_ *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("gc_collect_cycles", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewLong(0), nil
}

func nativeMemoryGetUsage(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("memory_get_usage", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(z.LiveBytes())), nil
}

func nativeMemoryGetPeakUsage(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("memory_get_peak_usage", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(max(z.LiveBytes(), uint64(zone.DefaultCapacity)))), nil
}

func nativePhpUname(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("php_uname", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	mode := "a"
	if len(args) == 1 {
		var err error
		mode, err = asStringParam(z, args[0], "php_uname", "1", "mode")
		if err != nil {
			return types.Value{}, err
		}
	}
	switch mode {
	case "s":
		return newStr(z, "Linux")
	case "n":
		h, _ := os.Hostname()
		if h == "" {
			h = "localhost"
		}
		return newStr(z, h)
	case "r":
		return newStr(z, "6.1.0")
	case "v":
		return newStr(z, "#1 SMP PREEMPT_DYNAMIC")
	case "m":
		return newStr(z, "x86_64")
	default:
		return newStr(z, "Linux localhost 6.1.0 #1 SMP PREEMPT_DYNAMIC x86_64")
	}
}

func hashPasswordWithSalt(password, salt string) string {
	sum := sha256.Sum256([]byte(salt + ":" + password))
	b64 := base64.RawStdEncoding.EncodeToString(sum[:])
	// Remplacement compatible bcrypt 22 chars + 31 chars = 53 chars après $2y$10$
	combined := salt + b64
	for len(combined) < 53 {
		combined += b64
	}
	return "$2y$10$" + combined[:53]
}

func nativePasswordHash(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("password_hash", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	pw, err := asStringParam(z, args[0], "password_hash", "1", "password")
	if err != nil {
		return types.Value{}, err
	}
	var saltBytes [16]byte
	_, _ = crand.Read(saltBytes[:])
	salt := base64.RawStdEncoding.EncodeToString(saltBytes[:])[:22]
	return newStr(z, hashPasswordWithSalt(pw, salt))
}

func nativePasswordVerify(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("password_verify", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	pw, err := asStringParam(z, args[0], "password_verify", "1", "password")
	if err != nil {
		return types.Value{}, err
	}
	h, err := asStringParam(z, args[1], "password_verify", "2", "hash")
	if err != nil {
		return types.Value{}, err
	}
	if strings.HasPrefix(h, "$2y$10$") && len(h) == 60 {
		salt := h[7:29]
		return types.NewBool(hashPasswordWithSalt(pw, salt) == h), nil
	}
	return types.NewBool(false), nil
}

func nativeCrypt(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("crypt", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	str, err := asStringParam(z, args[0], "crypt", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	salt, err := asStringParam(z, args[1], "crypt", "2", "salt")
	if err != nil {
		return types.Value{}, err
	}
	sum := sha256.Sum256([]byte(salt + ":" + str))
	return newStr(z, salt+hex.EncodeToString(sum[:8]))
}

func nativeOpensslEncrypt(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("openssl_encrypt", len(args), 3, 8); err != nil {
		return types.Value{}, err
	}
	data, err := asStringParam(z, args[0], "openssl_encrypt", "1", "data")
	if err != nil {
		return types.Value{}, err
	}
	_, err = asStringParam(z, args[1], "openssl_encrypt", "2", "cipher_algo")
	if err != nil {
		return types.Value{}, err
	}
	passphrase, err := asStringParam(z, args[2], "openssl_encrypt", "3", "passphrase")
	if err != nil {
		return types.Value{}, err
	}
	options := int64(0)
	if len(args) >= 4 {
		options, err = asIntParam(z, args[3], "openssl_encrypt", "4", "options")
		if err != nil {
			return types.Value{}, err
		}
	}
	iv := ""
	if len(args) >= 5 {
		iv, err = asStringParam(z, args[4], "openssl_encrypt", "5", "iv")
		if err != nil {
			return types.Value{}, err
		}
	}
	keyHash := sha256.Sum256([]byte(passphrase))
	block, err := aes.NewCipher(keyHash[:])
	if err != nil {
		return types.NewBool(false), nil
	}
	var ivBuf [16]byte
	copy(ivBuf[:], iv)
	padLen := 16 - (len(data) % 16)
	padded := make([]byte, len(data)+padLen)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}
	out := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, ivBuf[:])
	mode.CryptBlocks(out, padded)
	if options&1 != 0 { // OPENSSL_RAW_DATA
		return newBytes(z, out)
	}
	return newStr(z, base64.StdEncoding.EncodeToString(out))
}

func nativeGzcompress(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("gzcompress", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	data, err := asStringParam(z, args[0], "gzcompress", "1", "data")
	if err != nil {
		return types.Value{}, err
	}
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	_, _ = w.Write([]byte(data))
	_ = w.Close()
	return newBytes(z, buf.Bytes())
}

func nativeGzencode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("gzencode", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	data, err := asStringParam(z, args[0], "gzencode", "1", "data")
	if err != nil {
		return types.Value{}, err
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write([]byte(data))
	_ = w.Close()
	return newBytes(z, buf.Bytes())
}

func nativeUsleep(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("usleep", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	us, err := asIntParam(z, args[0], "usleep", "1", "microseconds")
	if err != nil {
		return types.Value{}, err
	}
	if us < 0 {
		return types.Value{}, valErr("usleep", "1", "microseconds", "must be greater than or equal to 0")
	}
	return types.NewNull(), nil
}

func nativeSleep(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("sleep", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	sec, err := asIntParam(z, args[0], "sleep", "1", "seconds")
	if err != nil {
		return types.Value{}, err
	}
	if sec < 0 {
		return types.Value{}, valErr("sleep", "1", "seconds", "must be greater than or equal to 0")
	}
	return types.NewLong(0), nil
}

func nativeFlush(_ *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("flush", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewNull(), nil
}

