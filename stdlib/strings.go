package stdlib

import (
	"io"
	"strconv"
	"strings"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	STR_PAD_LEFT  = 0
	STR_PAD_RIGHT = 1
	STR_PAD_BOTH  = 2
)

func nativeStrlen(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strlen", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "strlen", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(len(s))), nil
}

func offsetIn(n int, off int64) (int, bool) {
	if off < 0 {
		off += int64(n)
	}
	if off < 0 || off > int64(n) {
		return 0, false
	}
	return int(off), true
}

func nativeStrpos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return strposNamed(z, args, "strpos", false)
}

func nativeStripos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("stripos", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	hay, err := asStringParam(z, args[0], "stripos", "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asStringParam(z, args[1], "stripos", "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	off := int64(0)
	if len(args) == 3 {
		off, err = asIntParam(z, args[2], "stripos", "3", "offset")
		if err != nil {
			return types.Value{}, err
		}
	}
	start, ok := offsetIn(len(hay), off)
	if !ok {
		return types.Value{}, ValueError{"stripos(): Argument #3 ($offset) must be contained in argument #1 ($haystack)"}
	}
	if needle == "" {
		return types.NewLong(int64(start)), nil
	}
	if start > len(hay) {
		return types.NewBool(false), nil
	}
	lh := asciiLowerString(hay[start:])
	ln := asciiLowerString(needle)
	hb := unsafe.Slice(unsafe.StringData(lh), len(lh))
	nb := unsafe.Slice(unsafe.StringData(ln), len(ln))
	rel := C2_strpos(hb, uint64(len(hb)), nb, uint64(len(nb)))
	if rel < 0 {
		return types.NewBool(false), nil
	}
	return types.NewLong(int64(start) + rel), nil
}

func asciiLowerString(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

func nativeStrrpos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return strposNamed(z, args, "strrpos", true)
}

func strposNamed(z *zone.Zone, args []types.Value, name string, reverse bool) (types.Value, error) {
	if err := arity(name, len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	hay, err := asStringParam(z, args[0], name, "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asStringParam(z, args[1], name, "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	off := int64(0)
	if len(args) == 3 {
		off, err = asIntParam(z, args[2], name, "3", "offset")
		if err != nil {
			return types.Value{}, err
		}
	}
	start, ok := offsetIn(len(hay), off)
	if !ok {
		return types.Value{}, ValueError{name + "(): Argument #3 ($offset) must be contained in argument #1 ($haystack)"}
	}
	if reverse {
		return foundOrFalse(lastIndexFrom(hay, needle, off)), nil
	}
	if needle == "" {
		return types.NewLong(int64(start)), nil
	}
	if start > len(hay) {
		return types.NewBool(false), nil
	}
	hb := unsafe.Slice(unsafe.StringData(hay), len(hay))
	nb := unsafe.Slice(unsafe.StringData(needle), len(needle))
	rel := C2_strpos(hb[start:], uint64(len(hay)-start), nb, uint64(len(nb)))
	if rel < 0 {
		return types.NewBool(false), nil
	}
	return types.NewLong(int64(start) + rel), nil
}

// lastIndexFrom transpose zend_memnrstr sur la fenêtre que strrpos et strripos dérivent de off, déjà
// validé par offsetIn : off positif ouvre la fenêtre à off ; off négatif la ferme de sorte que
// l'occurrence commence au plus tard à len(hay)+off. Une aiguille vide rend la fin de la fenêtre.
// Retourne -1 sans occurrence.
func lastIndexFrom(hay, needle string, off int64) int64 {
	lo, hi := 0, len(hay)
	if off >= 0 {
		lo = int(off)
	} else if int(-off) >= len(needle) {
		hi = len(hay) + int(off) + len(needle)
	}
	if len(needle) > hi-lo {
		return -1
	}
	i := strings.LastIndex(hay[lo:hi], needle)
	if i < 0 {
		return -1
	}
	return int64(lo + i)
}

func foundOrFalse(i int64) types.Value {
	if i < 0 {
		return types.NewBool(false)
	}
	return types.NewLong(i)
}

func nativeStrripos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strripos", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	hay, err := asStringParam(z, args[0], "strripos", "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asStringParam(z, args[1], "strripos", "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	off := int64(0)
	if len(args) == 3 {
		off, err = asIntParam(z, args[2], "strripos", "3", "offset")
		if err != nil {
			return types.Value{}, err
		}
	}
	if _, ok := offsetIn(len(hay), off); !ok {
		return types.Value{}, ValueError{"strripos(): Argument #3 ($offset) must be contained in argument #1 ($haystack)"}
	}
	return foundOrFalse(lastIndexFrom(asciiLowerString(hay), asciiLowerString(needle), off)), nil
}

func nativeContains(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("str_contains", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	hay, err := asStringParam(z, args[0], "str_contains", "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asStringParam(z, args[1], "str_contains", "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	if needle == "" {
		return types.NewBool(true), nil
	}
	hb := unsafe.Slice(unsafe.StringData(hay), len(hay))
	nb := unsafe.Slice(unsafe.StringData(needle), len(needle))
	hit := C2_strpos(hb, uint64(len(hay)), nb, uint64(len(needle)))
	return types.NewBool(hit >= 0), nil
}

func nativeStarts(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("str_starts_with", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	hay, err := asStringParam(z, args[0], "str_starts_with", "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asStringParam(z, args[1], "str_starts_with", "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	if len(needle) > len(hay) {
		return types.NewBool(false), nil
	}
	return types.NewBool(hay[:len(needle)] == needle), nil
}

func nativeEnds(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("str_ends_with", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	hay, err := asStringParam(z, args[0], "str_ends_with", "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asStringParam(z, args[1], "str_ends_with", "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	if len(needle) > len(hay) {
		return types.NewBool(false), nil
	}
	return types.NewBool(hay[len(hay)-len(needle):] == needle), nil
}

func mapCase(z *zone.Zone, s string, upper bool) (types.Value, error) {
	if s == "" {
		return newStr(z, "")
	}
	src := unsafe.Slice(unsafe.StringData(s), len(s))
	var stackBuf [256]byte
	var dst []byte
	if len(src) <= len(stackBuf) {
		dst = stackBuf[:len(src)]
	} else {
		dst = make([]byte, len(src))
	}
	if upper {
		C2_strtoupper(dst, src, uint64(len(src)))
	} else {
		C2_strtolower(dst, src, uint64(len(src)))
	}
	return newBytes(z, dst)
}

func nativeStrtolower(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strtolower", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "strtolower", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	return mapCase(z, s, false)
}

func nativeStrtoupper(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strtoupper", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "strtoupper", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	return mapCase(z, s, true)
}

func nativeUcfirst(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return caseFirst(z, args, "ucfirst", true)
}

func nativeLcfirst(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return caseFirst(z, args, "lcfirst", false)
}

func caseFirst(z *zone.Zone, args []types.Value, name string, upper bool) (types.Value, error) {
	if err := arity(name, len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], name, "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	if s == "" {
		return newStr(z, "")
	}
	var stackBuf [256]byte
	var b []byte
	if len(s) <= len(stackBuf) {
		b = stackBuf[:len(s)]
		copy(b, s)
	} else {
		b = []byte(s)
	}
	var dst [1]byte
	if upper {
		C2_strtoupper(dst[:], b[:1], 1)
	} else {
		C2_strtolower(dst[:], b[:1], 1)
	}
	b[0] = dst[0]
	return newBytes(z, b)
}

func nativeSubstr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("substr", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	v0 := deref(z, args[0])
	if v0.Type == types.TypeString {
		off, err := asIntParam(z, args[1], "substr", "2", "offset")
		if err != nil {
			return types.Value{}, err
		}
		length := int64(9223372036854775807)
		if len(args) == 3 {
			v2 := deref(z, args[2])
			if v2.Type != types.TypeNull && !v2.IsUndef() {
				length, err = asIntParam(z, v2, "substr", "3", "length")
				if err != nil {
					return types.Value{}, err
				}
			}
		}
		return types.Substr(z, v0, off, length)
	}
	s, err := asStringParam(z, v0, "substr", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	off, err := asIntParam(z, args[1], "substr", "2", "offset")
	if err != nil {
		return types.Value{}, err
	}
	length := int64(9223372036854775807)
	if len(args) == 3 {
		v2 := deref(z, args[2])
		if v2.Type != types.TypeNull && !v2.IsUndef() {
			length, err = asIntParam(z, v2, "substr", "3", "length")
			if err != nil {
				return types.Value{}, err
			}
		}
	}
	sv, err := newStr(z, s)
	if err != nil {
		return types.Value{}, err
	}
	out, err := types.Substr(z, sv, off, length)
	types.ReleaseValue(z, &sv)
	return out, err
}

func defaultCutset() string { return " \n\r\t\v\x00" }

func cutBitmap(bm *[32]byte, chars string) {
	for i := 0; i < len(chars); i++ {
		c := chars[i]
		bm[c>>3] |= 1 << (c & 7)
	}
}

func nativeTrim(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	return trimNamed(z, args, "trim", true, true)
}

func nativeLtrim(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return trimNamed(z, args, "ltrim", true, false)
}

func nativeRtrim(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return trimNamed(z, args, "rtrim", false, true)
}

func trimNamed(z *zone.Zone, args []types.Value, name string, left, right bool) (types.Value, error) {
	if err := arity(name, len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], name, "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	chars := defaultCutset()
	if len(args) == 2 {
		chars, err = asStringParam(z, args[1], name, "2", "characters")
		if err != nil {
			return types.Value{}, err
		}
	}
	if s == "" {
		return newStr(z, "")
	}
	var bm [32]byte
	cutBitmap(&bm, chars)
	src := unsafe.Slice(unsafe.StringData(s), len(s))
	lo := uint64(0)
	hi := uint64(len(src))
	if left {
		lo = C2_ltrim_offset(src, uint64(len(src)), bm[:])
	}
	if right {
		hi = C2_rtrim_len(src, uint64(len(src)), bm[:])
	}
	if lo > hi {
		lo = hi
	}
	return newBytes(z, src[lo:hi])
}

func nativeRepeat(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("str_repeat", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "str_repeat", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	times, err := asIntParam(z, args[1], "str_repeat", "2", "times")
	if err != nil {
		return types.Value{}, err
	}
	if times < 0 {
		return types.Value{}, ValueError{"str_repeat(): Argument #2 ($times) must be greater than or equal to 0"}
	}
	if times == 0 || s == "" {
		return newStr(z, "")
	}
	if times > 1<<20 || int64(len(s)) > (8<<20)/times {
		return types.Value{}, Error{"str_repeat(): result too large"}
	}
	return newStr(z, strings.Repeat(s, int(times)))
}

func nativePad(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("str_pad", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "str_pad", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	length, err := asIntParam(z, args[1], "str_pad", "2", "length")
	if err != nil {
		return types.Value{}, err
	}
	pad := " "
	if len(args) >= 3 {
		pad, err = asStringParam(z, args[2], "str_pad", "3", "pad_string")
		if err != nil {
			return types.Value{}, err
		}
	}
	kind := int64(STR_PAD_RIGHT)
	if len(args) == 4 {
		kind, err = asIntParam(z, args[3], "str_pad", "4", "pad_type")
		if err != nil {
			return types.Value{}, err
		}
	}
	if pad == "" {
		return types.Value{}, ValueError{"str_pad(): Argument #3 ($pad_string) must be a non-empty string"}
	}
	if kind != STR_PAD_LEFT && kind != STR_PAD_RIGHT && kind != STR_PAD_BOTH {
		return types.Value{}, ValueError{"str_pad(): Argument #4 ($pad_type) must be STR_PAD_LEFT, STR_PAD_RIGHT, or STR_PAD_BOTH"}
	}
	if length <= int64(len(s)) {
		return newStr(z, s)
	}
	need := int(length) - len(s)
	var left, right int
	switch kind {
	case STR_PAD_LEFT:
		left = need
	case STR_PAD_BOTH:
		left = need / 2
		right = need - left
	default:
		right = need
	}
	return newStr(z, cycle(pad, left)+s+cycle(pad, right))
}

func cycle(pad string, n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(n)
	for b.Len() < n {
		b.WriteString(pad)
	}
	return b.String()[:n]
}

func nativeReplace(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("str_replace", len(args), 3, 4); err != nil {
		return types.Value{}, err
	}
	search := deref(z, args[0])
	repl := deref(z, args[1])
	subj := deref(z, args[2])
	var count int64
	if subj.Type == types.TypeArray {
		out, err := replaceArray(z, search, repl, subj, &count)
		if err == nil {
			storeReplaceCount(z, args, count)
		}
		return out, err
	}
	s, err := asStringParam(z, subj, "str_replace", "3", "subject")
	if err != nil {
		return types.Value{}, err
	}
	out, err := replaceString(z, search, repl, s, &count)
	if err != nil {
		return types.Value{}, err
	}
	storeReplaceCount(z, args, count)
	return newStr(z, out)
}

// storeReplaceCount écrit le nombre de remplacements dans le quatrième argument de str_replace ou str_ireplace, transmis par référence.
func storeReplaceCount(z *zone.Zone, args []types.Value, count int64) {
	if len(args) >= 4 && args[3].Type == types.TypeReference {
		types.AssignRef(z, args[3], types.NewLong(count))
	}
}

func replaceArray(z *zone.Zone, search, repl, subj types.Value, count *int64) (types.Value, error) {
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	keys, vals := arrayItems(z, subj)
	for i := range vals {
		// Comme php_str_replace_in_subject, un élément tableau est converti en "Array" puis remplacé, sans descente.
		s, err := castString(z, vals[i])
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		rep, err := replaceString(z, search, repl, s, count)
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		next, err := newStr(z, rep)
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		err = setKey(z, &out, keys[i], next)
		types.ReleaseValue(z, &next)
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
	}
	return out, nil
}

func replaceString(z *zone.Zone, search, repl types.Value, subject string, count *int64) (string, error) {
	if search.Type == types.TypeArray {
		sk, sv := arrayItems(z, search)
		_ = sk
		var rv []types.Value
		if repl.Type == types.TypeArray {
			_, rv = arrayItems(z, repl)
		}
		for i := range sv {
			ss, err := castString(z, sv[i])
			if err != nil {
				return "", err
			}
			rs := ""
			if repl.Type == types.TypeArray {
				if i < len(rv) {
					rs, err = castString(z, rv[i])
					if err != nil {
						return "", err
					}
				}
			} else {
				rs, err = castString(z, repl)
				if err != nil {
					return "", err
				}
			}
			if ss == "" {
				continue
			}
			*count += int64(strings.Count(subject, ss))
			subject = strings.ReplaceAll(subject, ss, rs)
		}
		return subject, nil
	}
	ss, err := castString(z, search)
	if err != nil {
		return "", err
	}
	rs, err := castString(z, repl)
	if err != nil {
		return "", err
	}
	if ss == "" {
		return subject, nil
	}
	*count += int64(strings.Count(subject, ss))
	return strings.ReplaceAll(subject, ss, rs), nil
}

func nativeImplode(name string) NativeFunc {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 1, 2); err != nil {
			return types.Value{}, err
		}
		var sep string
		var arr types.Value
		var err error
		if len(args) == 1 {
			arr, err = asArray(z, args[0], name, "1", "array")
			if err != nil {
				return types.Value{}, err
			}
		} else {
			sep, err = asStringParam(z, args[0], name, "1", "separator")
			if err != nil {
				return types.Value{}, err
			}
			arr, err = asArray(z, args[1], name, "2", "array")
			if err != nil {
				return types.Value{}, err
			}
		}
		_, vals := arrayItems(z, arr)
		parts := make([]string, len(vals))
		for i := range vals {
			parts[i], err = castString(z, vals[i])
			if err != nil {
				return types.Value{}, err
			}
		}
		return newStr(z, strings.Join(parts, sep))
	}
}

func nativeExplode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("explode", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	sep, err := asStringParam(z, args[0], "explode", "1", "separator")
	if err != nil {
		return types.Value{}, err
	}
	if sep == "" {
		return types.Value{}, ValueError{"explode(): Argument #1 ($separator) cannot be empty"}
	}
	s, err := asStringParam(z, args[1], "explode", "2", "string")
	if err != nil {
		return types.Value{}, err
	}
	limit := int64(9223372036854775807)
	if len(args) == 3 {
		limit, err = asIntParam(z, args[2], "explode", "3", "limit")
		if err != nil {
			return types.Value{}, err
		}
	}
	var parts []string
	switch {
	case limit == 0 || limit == 1:
		parts = []string{s}
	case limit < 0:
		all := strings.Split(s, sep)
		drop := int(-limit)
		if drop >= len(all) {
			parts = []string{}
		} else {
			parts = all[:len(all)-drop]
		}
	default:
		parts = strings.SplitN(s, sep, int(limit))
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, p := range parts {
		sv, err := newStr(z, p)
		if err != nil {
			return types.Value{}, err
		}
		if err = appendKey(z, &out, sv); err != nil {
			return types.Value{}, err
		}
	}
	return out, nil
}

func nativeChr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("chr", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	n, err := asIntParam(z, args[0], "chr", "1", "codepoint")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, string([]byte{byte(n)}))
}

func nativeOrd(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("ord", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "ord", "1", "character")
	if err != nil {
		return types.Value{}, err
	}
	if s == "" {
		return types.NewLong(0), nil
	}
	return types.NewLong(int64(s[0])), nil
}

func nativeBin2hex(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("bin2hex", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "bin2hex", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	const hexdigits = "0123456789abcdef"
	dst := make([]byte, len(s)*2)
	for i := 0; i < len(s); i++ {
		dst[i*2] = hexdigits[s[i]>>4]
		dst[i*2+1] = hexdigits[s[i]&0x0f]
	}
	return newBytes(z, dst)
}

func nativeHex2bin(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hex2bin", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "hex2bin", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	if len(s)%2 != 0 {
		return types.NewBool(false), nil
	}
	dst := make([]byte, len(s)/2)
	for i := range dst {
		hi := digitVal(s[i*2])
		lo := digitVal(s[i*2+1])
		if hi < 0 || hi > 15 || lo < 0 || lo > 15 {
			return types.NewBool(false), nil
		}
		dst[i] = byte(hi<<4 | lo)
	}
	return newBytes(z, dst)
}

func nativeSprintf(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("sprintf", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	format, err := asStringParam(z, args[0], "sprintf", "1", "format")
	if err != nil {
		return types.Value{}, err
	}
	out, err := phpSprintf(z, format, args[1:])
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, out)
}

func nativePrintf(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("printf", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	format, err := asStringParam(z, args[0], "printf", "1", "format")
	if err != nil {
		return types.Value{}, err
	}
	text, err := phpSprintf(z, format, args[1:])
	if err != nil {
		return types.Value{}, err
	}
	writeOut(out, []byte(text))
	return types.NewLong(int64(len(text))), nil
}

func phpSprintf(z *zone.Zone, format string, args []types.Value) (string, error) {
	var b strings.Builder
	ai := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			b.WriteByte(format[i])
			continue
		}
		if i+1 < len(format) && format[i+1] == '%' {
			b.WriteByte('%')
			i++
			continue
		}
		i++
		argnum := -1
		if j := i; j < len(format) && format[j] >= '0' && format[j] <= '9' {
			n := 0
			for j < len(format) && format[j] >= '0' && format[j] <= '9' {
				if n < 1<<31 {
					n = n*10 + int(format[j]-'0')
				}
				j++
			}
			if j < len(format) && format[j] == '$' {
				if n <= 0 || n >= 1<<31-1 {
					return "", ValueError{"Argument number specifier must be greater than zero and less than 2147483647"}
				}
				argnum = n - 1
				i = j + 1
			}
		}
		left, plus, space, zero := false, false, false, false
		customFill := byte(0)
		hasCustomFill := false
		for i < len(format) {
			switch format[i] {
			case '-':
				left = true
				i++
			case '+':
				plus = true
				i++
			case ' ':
				space = true
				i++
			case '0':
				zero = true
				i++
			case '\'':
				if i+1 < len(format) {
					customFill = format[i+1]
					hasCustomFill = true
					i += 2
				} else {
					goto flagsDone
				}
			default:
				goto flagsDone
			}
		}
	flagsDone:
		width := 0
		for i < len(format) && format[i] >= '0' && format[i] <= '9' {
			width = width*10 + int(format[i]-'0')
			i++
		}
		prec := -1
		if i < len(format) && format[i] == '.' {
			i++
			prec = 0
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				prec = prec*10 + int(format[i]-'0')
				i++
			}
		}
		if i >= len(format) {
			break
		}
		spec := format[i]
		cur := argnum
		if cur < 0 {
			cur = ai
			ai++
		}
		if cur >= len(args) {
			return "", ArgumentCountError{"sprintf(): Argument #" + itoa(cur+2) + " is missing"}
		}
		piece, err := formatSpec(z, spec, args[cur], prec, plus, space)
		if err != nil {
			return "", err
		}
		b.WriteString(padPiece(piece, width, left, zero && !left, hasCustomFill, customFill, spec))
	}
	return b.String(), nil
}

func padPiece(s string, width int, left, zero, hasCustomFill bool, customFill, spec byte) string {
	if width <= len(s) {
		return s
	}
	n := width - len(s)
	fill := byte(' ')
	if hasCustomFill {
		fill = customFill
	} else if zero && spec != 's' {
		fill = '0'
	}
	pad := strings.Repeat(string(fill), n)
	if left {
		return s + pad
	}
	if (zero && !hasCustomFill || hasCustomFill && customFill == '0') && len(s) > 0 && (s[0] == '-' || s[0] == '+' || s[0] == ' ') {
		return s[:1] + pad + s[1:]
	}
	return pad + s
}

func formatSpec(z *zone.Zone, spec byte, v types.Value, prec int, plus, space bool) (string, error) {
	switch spec {
	case 's':
		s, err := castString(z, v)
		if err != nil {
			return "", err
		}
		if prec >= 0 && prec < len(s) {
			s = s[:prec]
		}
		return s, nil
	case 'd', 'i':
		n, err := asIntLoose(z, v)
		if err != nil {
			return "", err
		}
		s := strconv.FormatInt(n, 10)
		return signPrefix(s, plus, space), nil
	case 'u':
		n, err := asIntLoose(z, v)
		if err != nil {
			return "", err
		}
		return strconv.FormatUint(uint64(n), 10), nil
	case 'x', 'X':
		n, err := asIntLoose(z, v)
		if err != nil {
			return "", err
		}
		s := strconv.FormatUint(uint64(n), 16)
		if spec == 'X' {
			s = strings.ToUpper(s)
		}
		return s, nil
	case 'o':
		n, err := asIntLoose(z, v)
		if err != nil {
			return "", err
		}
		return strconv.FormatUint(uint64(n), 8), nil
	case 'b':
		n, err := asIntLoose(z, v)
		if err != nil {
			return "", err
		}
		return strconv.FormatUint(uint64(n), 2), nil
	case 'c':
		n, err := asIntLoose(z, v)
		if err != nil {
			return "", err
		}
		return string([]byte{byte(n)}), nil
	case 'f', 'F':
		f, err := asFloatParam(z, v, "sprintf", "2", "values")
		if err != nil {
			f = floatFromLoose(z, v)
		}
		if prec < 0 {
			prec = 6
		}
		s := strconv.FormatFloat(f, 'f', prec, 64)
		if spec == 'F' {
			s = strings.ReplaceAll(s, "inf", "INF")
			s = strings.ReplaceAll(s, "nan", "NAN")
		}
		return signPrefix(s, plus, space), nil
	case 'e', 'E':
		f := floatFromLoose(z, v)
		if prec < 0 {
			prec = 6
		}
		s := strconv.FormatFloat(f, spec, prec, 64)
		s = trimExpLeadingZero(s)
		return signPrefix(s, plus, space), nil
	case 'g', 'G':
		f := floatFromLoose(z, v)
		if prec < 0 {
			prec = 6
		}
		s := strconv.FormatFloat(f, spec, prec, 64)
		s = trimExpLeadingZero(s)
		return signPrefix(s, plus, space), nil
	default:
		return "%" + string(spec), nil
	}
}

func trimExpLeadingZero(s string) string {
	idx := strings.IndexAny(s, "eE")
	if idx < 0 || idx+3 >= len(s) {
		return s
	}
	if (s[idx+1] == '+' || s[idx+1] == '-') && len(s)-(idx+2) == 2 && s[idx+2] == '0' {
		return s[:idx+2] + s[idx+3:]
	}
	return s
}

func signPrefix(s string, plus, space bool) string {
	if s == "" || s[0] == '-' {
		return s
	}
	if plus {
		return "+" + s
	}
	if space {
		return " " + s
	}
	return s
}

func floatFromLoose(z *zone.Zone, v types.Value) float64 {
	v = deref(z, v)
	switch v.Type {
	case types.TypeDouble:
		return v.ToDouble()
	case types.TypeLong:
		return float64(v.ToLong())
	case types.TypeTrue:
		return 1
	case types.TypeString:
		return parseFloatPrefix(types.StringData(z, v))
	default:
		return 0
	}
}

// taintOf retourne le bit FlagTainted porté par v, par la valeur qu'il référence ou par l'en-tête de
// la chaîne ; zéro hors chaîne.
func taintOf(z *zone.Zone, v types.Value) types.Flags {
	d := deref(z, v)
	if d.Type != types.TypeString {
		return 0
	}
	return (v.Flags | d.Flags | types.Flags(types.StringInfo(z, d).Flags)) & types.FlagTainted
}

// withTaint reporte sur la chaîne rendue out le marquage de l'argument src.
func withTaint(z *zone.Zone, src, out types.Value, err error) (types.Value, error) {
	if err == nil && out.Type == types.TypeString {
		out.Flags |= taintOf(z, src)
	}
	return out, err
}

// chop est l'alias de rtrim ; le marquage de l'argument est reporté sur le résultat.
func nativeChop(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	out, err := trimNamed(z, args, "chop", false, true)
	if err != nil {
		return types.Value{}, err
	}
	return withTaint(z, args[0], out, nil)
}

func nativeCountChars(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("count_chars", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "count_chars", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	mode := int64(0)
	if len(args) == 2 {
		mode, err = asIntParam(z, args[1], "count_chars", "2", "mode")
		if err != nil {
			return types.Value{}, err
		}
	}
	if mode < 0 || mode > 4 {
		return types.Value{}, ValueError{"count_chars(): Argument #2 ($mode) must be between 0 and 4 (inclusive)"}
	}
	var counts [256]int64
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	if mode >= 3 {
		buf := make([]byte, 0, 256)
		for c := range 256 {
			if (counts[c] > 0) == (mode == 3) {
				buf = append(buf, byte(c))
			}
		}
		out, err := newBytes(z, buf)
		return withTaint(z, args[0], out, err)
	}
	arr, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for c := range 256 {
		if mode == 1 && counts[c] == 0 || mode == 2 && counts[c] != 0 {
			continue
		}
		if err := setKey(z, &arr, types.NewLong(int64(c)), types.NewLong(counts[c])); err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
	}
	return arr, nil
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// byteAt rend s[i], ou l'octet nul terminal des chaînes C au-delà de la fin.
func byteAt(s string, i int) byte {
	if i < len(s) {
		return s[i]
	}
	return 0
}

// nativeQuotedPrintableDecode transpose PHP_FUNCTION(quoted_printable_decode) de ext/standard/quot_print.c
// (PHP 8.3) : la lecture s'arrête au premier octet nul, « =XX » hexadécimal est décodé, « = » suivi
// d'espaces ou tabulations puis d'une fin de ligne (CRLF, CR, LF ou fin de chaîne) est un saut doux.
func nativeQuotedPrintableDecode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("quoted_printable_decode", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "quoted_printable_decode", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '=' {
			out = append(out, c)
			i++
			continue
		}
		hi, okHi := hexDigit(byteAt(s, i+1))
		lo, okLo := hexDigit(byteAt(s, i+2))
		if okHi && okLo {
			out = append(out, hi<<4|lo)
			i += 3
			continue
		}
		k := 1
		for byteAt(s, i+k) == ' ' || byteAt(s, i+k) == '\t' {
			k++
		}
		switch next := byteAt(s, i+k); {
		case next == 0:
			i += k
		case next == '\r' && byteAt(s, i+k+1) == '\n':
			i += k + 2
		case next == '\r' || next == '\n':
			i += k + 1
		default:
			out = append(out, c)
			i++
		}
	}
	res, err := newBytes(z, out)
	return withTaint(z, args[0], res, err)
}

// qprintMaxLine est PHP_QPRINT_MAXL : longueur au-delà de laquelle un saut doux « =\r\n » est inséré.
const qprintMaxLine = 75

// nativeQuotedPrintableEncode transpose php_quot_print_encode (PHP 8.3) : CRLF conservé, octets de
// contrôle, non ASCII, « = » et espace précédant CR encodés « =XX », saut doux avant dépassement de
// qprintMaxLine sans couper une séquence UTF-8 annoncée par son octet de tête.
func nativeQuotedPrintableEncode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("quoted_printable_encode", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "quoted_printable_encode", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	const hexUpper = "0123456789ABCDEF"
	out := make([]byte, 0, len(s)+len(s)/2)
	lp := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\r' && byteAt(s, i+1) == '\n' {
			out = append(out, '\r', '\n')
			i++
			lp = 0
			continue
		}
		if c < 0x20 || c == 0x7f || c&0x80 != 0 || c == '=' || c == ' ' && byteAt(s, i+1) == '\r' {
			lp += 3
			if lp > qprintMaxLine && c <= 0x7f ||
				c > 0x7f && c <= 0xdf && lp+3 > qprintMaxLine ||
				c > 0xdf && c <= 0xef && lp+6 > qprintMaxLine ||
				c > 0xef && c <= 0xf4 && lp+9 > qprintMaxLine {
				out = append(out, '=', '\r', '\n')
				lp = 3
			}
			out = append(out, '=', hexUpper[c>>4], hexUpper[c&0xf])
			continue
		}
		lp++
		if lp > qprintMaxLine {
			out = append(out, '=', '\r', '\n')
			lp = 1
		}
		out = append(out, c)
	}
	res, err := newBytes(z, out)
	return withTaint(z, args[0], res, err)
}

// glibcMblen transpose mblen(3) de la glibc sous la locale C.UTF-8 que PHP 8.3 installe au démarrage
// quel que soit l'environnement : séquences de un à six octets jusqu'à 0x7FFFFFFF, formes non
// minimales et demi-codets UTF-16 refusés. Retourne la longueur de la séquence en tête de s non vide,
// ou -1 si elle est invalide ou tronquée.
func glibcMblen(s string) int {
	c := s[0]
	n, r := 0, uint32(0)
	switch {
	case c < 0x80:
		return 1
	case c < 0xc2:
		return -1
	case c < 0xe0:
		n, r = 2, uint32(c&0x1f)
	case c < 0xf0:
		n, r = 3, uint32(c&0x0f)
	case c < 0xf8:
		n, r = 4, uint32(c&0x07)
	case c < 0xfc:
		n, r = 5, uint32(c&0x03)
	case c < 0xfe:
		n, r = 6, uint32(c&0x01)
	default:
		return -1
	}
	if len(s) < n {
		return -1
	}
	for i := 1; i < n; i++ {
		if s[i]&0xc0 != 0x80 {
			return -1
		}
		r = r<<6 | uint32(s[i]&0x3f)
	}
	if r < [...]uint32{2: 0x80, 3: 0x800, 4: 0x10000, 5: 0x200000, 6: 0x4000000}[n] || r >= 0xd800 && r <= 0xdfff {
		return -1
	}
	return n
}

func noNullBytes(s, fn, idx, param string) error {
	if strings.IndexByte(s, 0) >= 0 {
		return ValueError{fn + "(): Argument #" + idx + " ($" + param + ") must not contain any null bytes"}
	}
	return nil
}

// nativeEscapeshellarg transpose php_escape_shell_arg (PHP 8.3, Unix) : la chaîne est entourée
// d'apostrophes, chaque apostrophe devient apostrophe, barre oblique inverse, apostrophe, apostrophe ;
// les séquences multioctets valides sont recopiées et les octets que glibcMblen refuse sont omis.
func nativeEscapeshellarg(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("escapeshellarg", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "escapeshellarg", "1", "arg")
	if err != nil {
		return types.Value{}, err
	}
	if err := noNullBytes(s, "escapeshellarg", "1", "arg"); err != nil {
		return types.Value{}, err
	}
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); {
		switch n := glibcMblen(s[i:]); {
		case n < 0:
			i++
		case n > 1:
			out = append(out, s[i:i+n]...)
			i += n
		default:
			if s[i] == '\'' {
				out = append(out, '\'', '\\', '\'')
			}
			out = append(out, s[i])
			i++
		}
	}
	out = append(out, '\'')
	res, err := newBytes(z, out)
	return withTaint(z, args[0], res, err)
}

// nativeEscapeshellcmd transpose php_escape_shell_cmd (PHP 8.3, Unix) : les métacaractères du shell
// et le saut de ligne sont précédés d'une barre oblique inverse, un guillemet ou une apostrophe n'est
// laissé intact que s'il est apparié plus loin dans la chaîne ; les séquences multioctets valides sont
// recopiées et les octets que glibcMblen refuse sont omis (0xFF, échappé par la table C, est toujours omis).
func nativeEscapeshellcmd(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("escapeshellcmd", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "escapeshellcmd", "1", "command")
	if err != nil {
		return types.Value{}, err
	}
	if err := noNullBytes(s, "escapeshellcmd", "1", "command"); err != nil {
		return types.Value{}, err
	}
	out := make([]byte, 0, 2*len(s))
	pending := -1 // position du guillemet fermant attendu, -1 sans guillemet ouvert
	for i := 0; i < len(s); {
		n := glibcMblen(s[i:])
		if n < 0 {
			i++
			continue
		}
		if n > 1 {
			out = append(out, s[i:i+n]...)
			i += n
			continue
		}
		c := s[i]
		switch c {
		case '"', '\'':
			if pending < 0 {
				if j := strings.IndexByte(s[i+1:], c); j >= 0 {
					pending = i + 1 + j
				} else {
					out = append(out, '\\')
				}
			} else if s[pending] == c {
				pending = -1
			} else {
				out = append(out, '\\')
			}
		case '#', '&', ';', '`', '|', '*', '?', '~', '<', '>', '^', '(', ')', '[', ']', '{', '}', '$', '\\', '\n':
			out = append(out, '\\')
		}
		out = append(out, c)
		i++
	}
	res, err := newBytes(z, out)
	return withTaint(z, args[0], res, err)
}
