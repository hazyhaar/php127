// Code créé par agent — Lot 4.3 : fonctions de chaînes PHP 8.3 complémentaires.
package stdlib

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func valErr(fn, idx, param, msg string) ValueError {
	return ValueError{fn + "(): Argument #" + idx + " ($" + param + ") " + msg}
}

func nativeStrtr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strtr", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "strtr", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	if len(args) == 2 {
		v1 := deref(z, args[1])
		if v1.Type != types.TypeArray {
			return types.Value{}, typeErr("strtr", "2", "from", "array when $to is not provided", typeKind(z, v1))
		}
		keys, vals := arrayItems(z, v1)
		type pair struct {
			from string
			to   string
		}
		var pairs []pair
		for i, k := range keys {
			var kStr string
			if k.IsLong() {
				kStr = strconv.FormatInt(k.ToLong(), 10)
			} else {
				kStr = types.StringData(z, k)
			}
			if kStr == "" {
				return newStr(z, s)
			}
			toStr, err := castString(z, vals[i])
			if err != nil {
				return types.Value{}, err
			}
			pairs = append(pairs, pair{from: kStr, to: toStr})
		}
		if len(pairs) == 0 || s == "" {
			return newStr(z, s)
		}
		for i := 1; i < len(pairs); i++ {
			for j := i; j > 0 && len(pairs[j].from) > len(pairs[j-1].from); j-- {
				pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
			}
		}
		var b strings.Builder
		b.Grow(len(s))
		i := 0
		for i < len(s) {
			matched := false
			for _, p := range pairs {
				if strings.HasPrefix(s[i:], p.from) {
					b.WriteString(p.to)
					i += len(p.from)
					matched = true
					break
				}
			}
			if !matched {
				b.WriteByte(s[i])
				i++
			}
		}
		return newStr(z, b.String())
	}

	from, err := asStringParam(z, args[1], "strtr", "2", "from")
	if err != nil {
		return types.Value{}, err
	}
	to, err := asStringParam(z, args[2], "strtr", "3", "to")
	if err != nil {
		return types.Value{}, err
	}
	n := min(len(from), len(to))
	if n == 0 || s == "" {
		return newStr(z, s)
	}
	var table [256]byte
	for i := range 256 {
		table[i] = byte(i)
	}
	for i := range n {
		table[from[i]] = to[i]
	}
	out := make([]byte, len(s))
	for i := range len(s) {
		out[i] = table[s[i]]
	}
	return newBytes(z, out)
}

func nativeSubstrReplace(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("substr_replace", len(args), 3, 4); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "substr_replace", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	repl, err := asStringParam(z, args[1], "substr_replace", "2", "replace")
	if err != nil {
		return types.Value{}, err
	}
	off64, err := asIntParam(z, args[2], "substr_replace", "3", "offset")
	if err != nil {
		return types.Value{}, err
	}
	hasLen := false
	var len64 int64
	if len(args) == 4 && !deref(z, args[3]).IsNull() && !deref(z, args[3]).IsUndef() {
		l, err := asIntParam(z, args[3], "substr_replace", "4", "length")
		if err != nil {
			return types.Value{}, err
		}
		hasLen = true
		len64 = l
	}
	n := len(s)
	off := int(off64)
	if off < 0 {
		off = max(n+off, 0)
	} else if off > n {
		off = n
	}
	var end int
	if !hasLen {
		end = n
	} else {
		l := int(len64)
		if l < 0 {
			end = max(n+l, off)
		} else {
			end = min(off+l, n)
		}
	}
	return newStr(z, s[:off]+repl+s[end:])
}

func nativeSubstrCompare(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("substr_compare", len(args), 3, 5); err != nil {
		return types.Value{}, err
	}
	h, err := asStringParam(z, args[0], "substr_compare", "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asStringParam(z, args[1], "substr_compare", "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	off64, err := asIntParam(z, args[2], "substr_compare", "3", "offset")
	if err != nil {
		return types.Value{}, err
	}
	off := int(off64)
	if off < 0 {
		off = len(h) + off
		if off < 0 {
			return types.Value{}, valErr("substr_compare", "3", "offset", "must be contained in argument #1 ($haystack)")
		}
	}
	if off > len(h) {
		return types.Value{}, valErr("substr_compare", "3", "offset", "must be contained in argument #1 ($haystack)")
	}
	length := max(len(needle), len(h)-off)
	if len(args) >= 4 && !deref(z, args[3]).IsNull() && !deref(z, args[3]).IsUndef() {
		l64, err := asIntParam(z, args[3], "substr_compare", "4", "length")
		if err != nil {
			return types.Value{}, err
		}
		if l64 < 0 {
			return types.Value{}, valErr("substr_compare", "4", "length", "must be greater than or equal to 0")
		}
		length = int(l64)
	}
	ci := false
	if len(args) == 5 {
		ci = asBool(z, args[4])
	}
	if length == 0 {
		return types.NewLong(0), nil
	}
	subH := h[off:]
	if len(subH) > length {
		subH = subH[:length]
	}
	subN := needle
	if len(subN) > length {
		subN = subN[:length]
	}
	if ci {
		subH = strings.ToLower(subH)
		subN = strings.ToLower(subN)
	}
	return types.NewLong(int64(strings.Compare(subH, subN))), nil
}

func nativeStrRot13(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("str_rot13", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "str_rot13", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	out := make([]byte, len(s))
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
			out[i] = 'a' + (c-'a'+13)%26
		case c >= 'A' && c <= 'Z':
			out[i] = 'A' + (c-'A'+13)%26
		default:
			out[i] = c
		}
	}
	return newBytes(z, out)
}

func nativeStrShuffle(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("str_shuffle", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "str_shuffle", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	out := []byte(s)
	rand.Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return newBytes(z, out)
}

func nativeStrWordCount(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("str_word_count", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "str_word_count", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	format := int64(0)
	if len(args) >= 2 {
		f, err := asIntParam(z, args[1], "str_word_count", "2", "format")
		if err != nil {
			return types.Value{}, err
		}
		if f != 0 && f != 1 && f != 2 {
			return types.Value{}, valErr("str_word_count", "2", "format", "must be a valid format value")
		}
		format = f
	}
	extra := ""
	if len(args) == 3 && !deref(z, args[2]).IsNull() && !deref(z, args[2]).IsUndef() {
		ex, err := asStringParam(z, args[2], "str_word_count", "3", "characters")
		if err != nil {
			return types.Value{}, err
		}
		extra = ex
	}
	isChar := func(b byte) bool {
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') {
			return true
		}
		return strings.IndexByte(extra, b) >= 0
	}
	type wordSpan struct {
		pos  int
		word string
	}
	var words []wordSpan
	i := 0
	n := len(s)
	for i < n {
		for i < n && !isChar(s[i]) && s[i] != '\'' {
			i++
		}
		if i >= n {
			break
		}
		start := i
		for i < n {
			if isChar(s[i]) || s[i] == '\'' {
				i++
			} else if s[i] == '-' && i > start && i+1 < n && (isChar(s[i+1]) || s[i+1] == '\'') {
				i++
			} else {
				break
			}
		}
		w := s[start:i]
		if w != "'" {
			words = append(words, wordSpan{pos: start, word: w})
		}
	}
	if format == 0 {
		return types.NewLong(int64(len(words))), nil
	}
	arr, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for idx, w := range words {
		ws, err := newStr(z, w.word)
		if err != nil {
			return types.Value{}, err
		}
		if format == 1 {
			err = setKey(z, &arr, types.NewLong(int64(idx)), ws)
		} else {
			err = setKey(z, &arr, types.NewLong(int64(w.pos)), ws)
		}
		types.ReleaseValue(z, &ws)
		if err != nil {
			return types.Value{}, err
		}
	}
	return arr, nil
}

func nativeStrpbrk(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strpbrk", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "strpbrk", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	chars, err := asStringParam(z, args[1], "strpbrk", "2", "characters")
	if err != nil {
		return types.Value{}, err
	}
	if chars == "" {
		return types.Value{}, valErr("strpbrk", "2", "characters", "cannot be empty")
	}
	idx := strings.IndexAny(s, chars)
	if idx < 0 {
		return types.NewBool(false), nil
	}
	return newStr(z, s[idx:])
}

var strtokStr string
var strtokPos int

func nativeStrtok(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strtok", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	var delim string
	if len(args) == 2 {
		s, err := asStringParam(z, args[0], "strtok", "1", "string")
		if err != nil {
			return types.Value{}, err
		}
		d, err := asStringParam(z, args[1], "strtok", "2", "token")
		if err != nil {
			return types.Value{}, err
		}
		strtokStr = s
		strtokPos = 0
		delim = d
	} else {
		d, err := asStringParam(z, args[0], "strtok", "1", "string")
		if err != nil {
			return types.Value{}, err
		}
		delim = d
	}
	s := strtokStr
	pos := strtokPos
	n := len(s)
	for pos < n && strings.IndexByte(delim, s[pos]) >= 0 {
		pos++
	}
	if pos >= n {
		strtokPos = n
		return types.NewBool(false), nil
	}
	start := pos
	for pos < n && strings.IndexByte(delim, s[pos]) < 0 {
		pos++
	}
	strtokPos = pos
	return newStr(z, s[start:pos])
}

func resolveSpnBounds(s string, off, length int64, hasLen bool) (string, bool) {
	n := int64(len(s))
	if off < 0 {
		off = max(n+off, 0)
	}
	if off > n {
		return "", false
	}
	end := n
	if hasLen {
		if length < 0 {
			end = max(n+length, off)
		} else {
			end = min(off+length, n)
		}
	}
	return s[off:end], true
}

func nativeStrcspn(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strcspn", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "strcspn", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	chars, err := asStringParam(z, args[1], "strcspn", "2", "characters")
	if err != nil {
		return types.Value{}, err
	}
	off := int64(0)
	if len(args) >= 3 {
		off, err = asIntParam(z, args[2], "strcspn", "3", "offset")
		if err != nil {
			return types.Value{}, err
		}
	}
	hasLen := false
	length := int64(0)
	if len(args) == 4 && !deref(z, args[3]).IsNull() && !deref(z, args[3]).IsUndef() {
		length, err = asIntParam(z, args[3], "strcspn", "4", "length")
		if err != nil {
			return types.Value{}, err
		}
		hasLen = true
	}
	sub, ok := resolveSpnBounds(s, off, length, hasLen)
	if !ok {
		return types.NewLong(0), nil
	}
	for i := range len(sub) {
		if strings.IndexByte(chars, sub[i]) >= 0 {
			return types.NewLong(int64(i)), nil
		}
	}
	return types.NewLong(int64(len(sub))), nil
}

func nativeStrspn(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strspn", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "strspn", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	chars, err := asStringParam(z, args[1], "strspn", "2", "characters")
	if err != nil {
		return types.Value{}, err
	}
	off := int64(0)
	if len(args) >= 3 {
		off, err = asIntParam(z, args[2], "strspn", "3", "offset")
		if err != nil {
			return types.Value{}, err
		}
	}
	hasLen := false
	length := int64(0)
	if len(args) == 4 && !deref(z, args[3]).IsNull() && !deref(z, args[3]).IsUndef() {
		length, err = asIntParam(z, args[3], "strspn", "4", "length")
		if err != nil {
			return types.Value{}, err
		}
		hasLen = true
	}
	sub, ok := resolveSpnBounds(s, off, length, hasLen)
	if !ok {
		return types.NewLong(0), nil
	}
	for i := range len(sub) {
		if strings.IndexByte(chars, sub[i]) < 0 {
			return types.NewLong(int64(i)), nil
		}
	}
	return types.NewLong(int64(len(sub))), nil
}

func strnatcmpGo(a, b string, foldCase bool) int {
	ia, ib := 0, 0
	na, nb := len(a), len(b)
	isDigit := func(c byte) bool {
		return c >= '0' && c <= '9'
	}
	fold := func(c byte) byte {
		if foldCase && c >= 'A' && c <= 'Z' {
			return c + ('a' - 'A')
		}
		return c
	}

	for {
		for ia < na && isWS(a[ia]) {
			ia++
		}
		for ib < nb && isWS(b[ib]) {
			ib++
		}
		if ia >= na && ib >= nb {
			return 0
		}
		if ia >= na {
			return -1
		}
		if ib >= nb {
			return 1
		}
		ca, cb := a[ia], b[ib]
		if isDigit(ca) && isDigit(cb) {
			if ca == '0' || cb == '0' {
				for {
					da := ia < na && isDigit(a[ia])
					db := ib < nb && isDigit(b[ib])
					if !da && !db {
						break
					}
					if !da {
						return -1
					}
					if !db {
						return 1
					}
					if a[ia] < b[ib] {
						return -1
					}
					if a[ia] > b[ib] {
						return 1
					}
					ia++
					ib++
				}
			} else {
				bias := 0
				for {
					da := ia < na && isDigit(a[ia])
					db := ib < nb && isDigit(b[ib])
					if !da && !db {
						if bias != 0 {
							return bias
						}
						break
					}
					if !da {
						return -1
					}
					if !db {
						return 1
					}
					if a[ia] < b[ib] && bias == 0 {
						bias = -1
					} else if a[ia] > b[ib] && bias == 0 {
						bias = 1
					}
					ia++
					ib++
				}
			}
			continue
		}
		fa, fb := fold(ca), fold(cb)
		if fa < fb {
			return -1
		}
		if fa > fb {
			return 1
		}
		ia++
		ib++
	}
}

func nativeStrnatcmp(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strnatcmp", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	a, err := asStringParam(z, args[0], "strnatcmp", "1", "string1")
	if err != nil {
		return types.Value{}, err
	}
	b, err := asStringParam(z, args[1], "strnatcmp", "2", "string2")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(strnatcmpGo(a, b, false))), nil
}

func nativeStrnatcasecmp(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strnatcasecmp", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	a, err := asStringParam(z, args[0], "strnatcasecmp", "1", "string1")
	if err != nil {
		return types.Value{}, err
	}
	b, err := asStringParam(z, args[1], "strnatcasecmp", "2", "string2")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(strnatcmpGo(a, b, true))), nil
}

func nativeStrncasecmp(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strncasecmp", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	a, err := asStringParam(z, args[0], "strncasecmp", "1", "string1")
	if err != nil {
		return types.Value{}, err
	}
	b, err := asStringParam(z, args[1], "strncasecmp", "2", "string2")
	if err != nil {
		return types.Value{}, err
	}
	n, err := asIntParam(z, args[2], "strncasecmp", "3", "length")
	if err != nil {
		return types.Value{}, err
	}
	if n < 0 {
		return types.Value{}, valErr("strncasecmp", "3", "length", "must be greater than or equal to 0")
	}
	if int64(len(a)) > n {
		a = a[:n]
	}
	if int64(len(b)) > n {
		b = b[:n]
	}
	la := strings.ToLower(a)
	lb := strings.ToLower(b)
	lim := min(len(la), len(lb))
	for i := range lim {
		if la[i] != lb[i] {
			return types.NewLong(int64(la[i]) - int64(lb[i])), nil
		}
	}
	return types.NewLong(int64(len(la)) - int64(len(lb))), nil
}

func nativeChunkSplit(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("chunk_split", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "chunk_split", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	length := int64(76)
	if len(args) >= 2 {
		length, err = asIntParam(z, args[1], "chunk_split", "2", "length")
		if err != nil {
			return types.Value{}, err
		}
		if length <= 0 {
			return types.Value{}, valErr("chunk_split", "2", "length", "must be greater than 0")
		}
	}
	sep := "\r\n"
	if len(args) == 3 {
		sep, err = asStringParam(z, args[2], "chunk_split", "3", "separator")
		if err != nil {
			return types.Value{}, err
		}
	}
	if s == "" {
		return newStr(z, sep)
	}
	var b strings.Builder
	step := int(length)
	for i := 0; i < len(s); i += step {
		end := min(i+step, len(s))
		b.WriteString(s[i:end])
		b.WriteString(sep)
	}
	return newStr(z, b.String())
}

func nativeWordwrap(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("wordwrap", len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "wordwrap", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	width := int64(75)
	if len(args) >= 2 {
		width, err = asIntParam(z, args[1], "wordwrap", "2", "width")
		if err != nil {
			return types.Value{}, err
		}
	}
	brk := "\n"
	if len(args) >= 3 {
		brk, err = asStringParam(z, args[2], "wordwrap", "3", "break")
		if err != nil {
			return types.Value{}, err
		}
		if brk == "" {
			return types.Value{}, valErr("wordwrap", "3", "break", "cannot be empty")
		}
	}
	cut := false
	if len(args) == 4 {
		cut = asBool(z, args[3])
	}
	if width <= 0 && cut {
		return types.Value{}, valErr("wordwrap", "4", "cut_long_words", "cannot be true when argument #2 ($width) is less than or equal to 0")
	}
	if s == "" || width <= 0 {
		return newStr(z, s)
	}
	w := int(width)
	lines := strings.Split(s, brk)
	var outLines []string
	for _, line := range lines {
		var b strings.Builder
		words := strings.Split(line, " ")
		col := 0
		for wi, word := range words {
			for cut && len(word) > w {
				if col > 0 {
					b.WriteString(brk)
					col = 0
				}
				b.WriteString(word[:w])
				word = word[w:]
				if len(word) > 0 {
					b.WriteString(brk)
				}
			}
			if wi == 0 || col == 0 {
				b.WriteString(word)
				col = len(word)
			} else if col+1+len(word) > w {
				b.WriteString(brk)
				b.WriteString(word)
				col = len(word)
			} else {
				b.WriteByte(' ')
				b.WriteString(word)
				col += 1 + len(word)
			}
		}
		outLines = append(outLines, b.String())
	}
	return newStr(z, strings.Join(outLines, brk))
}

func nativeQuotemeta(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("quotemeta", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "quotemeta", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	if s == "" {
		return newStr(z, "")
	}
	var b strings.Builder
	b.Grow(len(s) * 2)
	for i := range len(s) {
		c := s[i]
		switch c {
		case '.', '\\', '+', '*', '?', '[', '^', ']', '(', '$', ')':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return newStr(z, b.String())
}

func nativeAddcslashes(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("addcslashes", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "addcslashes", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	chars, err := asStringParam(z, args[1], "addcslashes", "2", "characters")
	if err != nil {
		return types.Value{}, err
	}
	var mask [256]bool
	for i := 0; i < len(chars); i++ {
		if i+3 < len(chars) && chars[i+1] == '.' && chars[i+2] == '.' {
			start := chars[i]
			end := chars[i+3]
			if start <= end {
				for c := int(start); c <= int(end); c++ {
					mask[c] = true
				}
			}
			i += 3
		} else {
			mask[chars[i]] = true
		}
	}
	var b strings.Builder
	b.Grow(len(s) * 2)
	for i := range len(s) {
		c := s[i]
		if !mask[c] {
			b.WriteByte(c)
			continue
		}
		switch c {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\a':
			b.WriteString(`\a`)
		case '\t':
			b.WriteString(`\t`)
		case '\v':
			b.WriteString(`\v`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if c < 32 || c > 126 {
				fmt.Fprintf(&b, "\\%03o", c)
			} else {
				b.WriteByte('\\')
				b.WriteByte(c)
			}
		}
	}
	return newStr(z, b.String())
}

func nativeStripcslashes(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("stripcslashes", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "stripcslashes", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		c := s[i]
		switch c {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'a':
			b.WriteByte('\a')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case '\\':
			b.WriteByte('\\')
		case 'x', 'X':
			if i+1 < len(s) {
				val := 0
				digits := 0
				for digits < 2 && i+1 < len(s) {
					d := s[i+1]
					var hv int
					switch {
					case d >= '0' && d <= '9':
						hv = int(d - '0')
					case d >= 'a' && d <= 'f':
						hv = int(d-'a') + 10
					case d >= 'A' && d <= 'F':
						hv = int(d-'A') + 10
					default:
						hv = -1
					}
					if hv < 0 {
						break
					}
					val = val*16 + hv
					i++
					digits++
				}
				if digits > 0 {
					b.WriteByte(byte(val))
				} else {
					b.WriteByte(c)
				}
			} else {
				b.WriteByte(c)
			}
		default:
			if c >= '0' && c <= '7' {
				val := int(c - '0')
				digits := 1
				for digits < 3 && i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '7' {
					val = val*8 + int(s[i+1]-'0')
					i++
					digits++
				}
				b.WriteByte(byte(val))
			} else {
				b.WriteByte(c)
			}
		}
	}
	return newStr(z, b.String())
}

func nativeNl2br(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("nl2br", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "nl2br", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	xhtml := true
	if len(args) == 2 {
		xhtml = asBool(z, args[1])
	}
	tag := "<br />"
	if !xhtml {
		tag = "<br>"
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' {
			b.WriteString(tag)
			if i+1 < len(s) && s[i+1] == '\n' {
				b.WriteString("\r\n")
				i++
			} else {
				b.WriteByte('\r')
			}
		} else if s[i] == '\n' {
			b.WriteString(tag)
			if i+1 < len(s) && s[i+1] == '\r' {
				b.WriteString("\n\r")
				i++
			} else {
				b.WriteByte('\n')
			}
		} else {
			b.WriteByte(s[i])
		}
	}
	return newStr(z, b.String())
}

func nativeStripTags(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strip_tags", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "strip_tags", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	allowed := make(map[string]bool)
	if len(args) == 2 && !deref(z, args[1]).IsNull() && !deref(z, args[1]).IsUndef() {
		v1 := deref(z, args[1])
		if v1.Type == types.TypeArray {
			_, vals := arrayItems(z, v1)
			for _, itVal := range vals {
				ts, err := castString(z, itVal)
				if err != nil {
					return types.Value{}, err
				}
				ts = strings.TrimSpace(strings.ToLower(ts))
				ts = strings.TrimPrefix(ts, "<")
				ts = strings.TrimSuffix(ts, ">")
				ts = strings.TrimPrefix(ts, "/")
				if ts != "" {
					allowed[ts] = true
				}
			}
		} else {
			as, err := asStringParam(z, args[1], "strip_tags", "2", "allowed_tags")
			if err != nil {
				return types.Value{}, err
			}
			for {
				lt := strings.IndexByte(as, '<')
				if lt < 0 {
					break
				}
				gt := strings.IndexByte(as[lt+1:], '>')
				if gt < 0 {
					break
				}
				name := strings.ToLower(strings.TrimSpace(as[lt+1 : lt+1+gt]))
				name = strings.TrimPrefix(name, "/")
				if sp := strings.IndexAny(name, " \t\r\n/"); sp >= 0 {
					name = name[:sp]
				}
				if name != "" {
					allowed[name] = true
				}
				as = as[lt+1+gt+1:]
			}
		}
	}

	var b strings.Builder
	b.Grow(len(s))
	i := 0
	n := len(s)
	for i < n {
		if s[i] != '<' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if strings.HasPrefix(s[i:], "<!--") {
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				break
			}
			i += 4 + end + 3
			continue
		}
		if strings.HasPrefix(s[i:], "<?") {
			end := strings.Index(s[i+2:], "?>")
			if end < 0 {
				break
			}
			i += 2 + end + 2
			continue
		}
		j := i + 1
		inQuote := byte(0)
		for j < n {
			if inQuote != 0 {
				if s[j] == inQuote {
					inQuote = 0
				}
			} else {
				if s[j] == '"' || s[j] == '\'' {
					inQuote = s[j]
				} else if s[j] == '>' {
					break
				}
			}
			j++
		}
		if j >= n {
			break
		}
		tagContent := strings.TrimSpace(s[i+1 : j])
		tagName := strings.TrimPrefix(tagContent, "/")
		tagName = strings.TrimSpace(tagName)
		if sp := strings.IndexAny(tagName, " \t\r\n/"); sp >= 0 {
			tagName = tagName[:sp]
		}
		tagName = strings.ToLower(tagName)
		if tagName != "" && allowed[tagName] {
			b.WriteString(s[i : j+1])
		}
		i = j + 1
	}
	return newStr(z, b.String())
}

var htmlEntitiesForward = map[rune]string{
	'&':  "&amp;",
	'"':  "&quot;",
	'\'': "&apos;",
	'<':  "&lt;",
	'>':  "&gt;",
	0xA0: "&nbsp;",
	'¡':  "&iexcl;",
	'¢':  "&cent;",
	'£':  "&pound;",
	'¤':  "&curren;",
	'¥':  "&yen;",
	'¦':  "&brvbar;",
	'§':  "&sect;",
	'¨':  "&uml;",
	'©':  "&copy;",
	'ª':  "&ordf;",
	'«':  "&laquo;",
	'¬':  "&not;",
	0xAD: "&shy;",
	'®':  "&reg;",
	'¯':  "&macr;",
	'°':  "&deg;",
	'±':  "&plusmn;",
	'²':  "&sup2;",
	'³':  "&sup3;",
	'´':  "&acute;",
	'µ':  "&micro;",
	'¶':  "&para;",
	'·':  "&middot;",
	'¸':  "&cedil;",
	'¹':  "&sup1;",
	'º':  "&ordm;",
	'»':  "&raquo;",
	'¼':  "&frac14;",
	'½':  "&frac12;",
	'¾':  "&frac34;",
	'¿':  "&iquest;",
	'À':  "&Agrave;",
	'Á':  "&Aacute;",
	'Â':  "&Acirc;",
	'Ã':  "&Atilde;",
	'Ä':  "&Auml;",
	'Å':  "&Aring;",
	'Æ':  "&AElig;",
	'Ç':  "&Ccedil;",
	'È':  "&Egrave;",
	'É':  "&Eacute;",
	'Ê':  "&Ecirc;",
	'Ë':  "&Euml;",
	'Ì':  "&Igrave;",
	'Í':  "&Iacute;",
	'Î':  "&Icirc;",
	'Ï':  "&Iuml;",
	'Ð':  "&ETH;",
	'Ñ':  "&Ntilde;",
	'Ò':  "&Ograve;",
	'Ó':  "&Oacute;",
	'Ô':  "&Ocirc;",
	'Õ':  "&Otilde;",
	'Ö':  "&Ouml;",
	'×':  "&times;",
	'Ø':  "&Oslash;",
	'Ù':  "&Ugrave;",
	'Ú':  "&Uacute;",
	'Û':  "&Ucirc;",
	'Ü':  "&Uuml;",
	'Ý':  "&Yacute;",
	'Þ':  "&THORN;",
	'ß':  "&szlig;",
	'à':  "&agrave;",
	'á':  "&aacute;",
	'â':  "&acirc;",
	'ã':  "&atilde;",
	'ä':  "&auml;",
	'å':  "&aring;",
	'æ':  "&aelig;",
	'ç':  "&ccedil;",
	'è':  "&egrave;",
	'é':  "&eacute;",
	'ê':  "&ecirc;",
	'ë':  "&euml;",
	'ì':  "&igrave;",
	'í':  "&iacute;",
	'î':  "&icirc;",
	'ï':  "&iuml;",
	'ð':  "&eth;",
	'ñ':  "&ntilde;",
	'ò':  "&ograve;",
	'ó':  "&oacute;",
	'ô':  "&ocirc;",
	'õ':  "&otilde;",
	'ö':  "&ouml;",
	'÷':  "&divide;",
	'ø':  "&oslash;",
	'ù':  "&ugrave;",
	'ú':  "&uacute;",
	'û':  "&ucirc;",
	'ü':  "&uuml;",
	'ý':  "&yacute;",
	'þ':  "&thorn;",
	'ÿ':  "&yuml;",
	'€':  "&euro;",
	'Œ':  "&OElig;",
	'œ':  "&oelig;",
}

func nativeHtmlentities(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("htmlentities", len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "htmlentities", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	flags := int64(3)
	if len(args) >= 2 {
		flags, err = asIntParam(z, args[1], "htmlentities", "2", "flags")
		if err != nil {
			return types.Value{}, err
		}
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for _, r := range s {
		if r == '"' && (flags&1) == 0 && (flags&2) == 0 {
			b.WriteRune(r)
			continue
		}
		if r == '\'' {
			if (flags & 2) != 0 {
				if (flags & (16 | 32 | 48)) == 0 {
					b.WriteString("&#039;")
				} else {
					b.WriteString("&apos;")
				}
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if ent, ok := htmlEntitiesForward[r]; ok {
			b.WriteString(ent)
		} else {
			b.WriteRune(r)
		}
	}
	return newStr(z, b.String())
}

func nativeHtmlEntityDecode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("html_entity_decode", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "html_entity_decode", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	flags := int64(3)
	if len(args) >= 2 {
		flags, err = asIntParam(z, args[1], "html_entity_decode", "2", "flags")
		if err != nil {
			return types.Value{}, err
		}
	}
	reverse := make(map[string]rune, len(htmlEntitiesForward)+2)
	for r, ent := range htmlEntitiesForward {
		reverse[ent] = r
	}
	reverse["&#039;"] = '\''
	reverse["&#39;"] = '\''

	var b strings.Builder
	b.Grow(len(s))
	i := 0
	n := len(s)
	for i < n {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		semi := strings.IndexByte(s[i:], ';')
		if semi <= 1 || semi > 12 {
			b.WriteByte(s[i])
			i++
			continue
		}
		ent := s[i : i+semi+1]
		if strings.HasPrefix(ent, "&#x") || strings.HasPrefix(ent, "&#X") {
			code, err := strconv.ParseInt(ent[3:len(ent)-1], 16, 32)
			if err == nil && utf8.ValidRune(rune(code)) {
				r := rune(code)
				if (r == '"' && (flags&3) == 0) || (r == '\'' && (flags&2) == 0) {
					b.WriteString(ent)
				} else {
					b.WriteRune(r)
				}
				i += semi + 1
				continue
			}
		} else if strings.HasPrefix(ent, "&#") {
			code, err := strconv.ParseInt(ent[2:len(ent)-1], 10, 32)
			if err == nil && utf8.ValidRune(rune(code)) {
				r := rune(code)
				if (r == '"' && (flags&3) == 0) || (r == '\'' && (flags&2) == 0) {
					b.WriteString(ent)
				} else {
					b.WriteRune(r)
				}
				i += semi + 1
				continue
			}
		} else if r, ok := reverse[ent]; ok {
			if (r == '"' && (flags&3) == 0) || (r == '\'' && (flags&2) == 0) {
				b.WriteString(ent)
			} else {
				b.WriteRune(r)
			}
			i += semi + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return newStr(z, b.String())
}

func nativeNumberFormat(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("number_format", len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	num, err := asFloatParam(z, args[0], "number_format", "1", "num")
	if err != nil {
		return types.Value{}, err
	}
	decimals := int64(0)
	if len(args) >= 2 && !deref(z, args[1]).IsNull() && !deref(z, args[1]).IsUndef() {
		decimals, err = asIntParam(z, args[1], "number_format", "2", "decimals")
		if err != nil {
			return types.Value{}, err
		}
	}
	decSep := "."
	if len(args) >= 3 && !deref(z, args[2]).IsNull() && !deref(z, args[2]).IsUndef() {
		decSep, err = asStringParam(z, args[2], "number_format", "3", "decimal_separator")
		if err != nil {
			return types.Value{}, err
		}
	}
	thouSep := ","
	if len(args) == 4 && !deref(z, args[3]).IsNull() && !deref(z, args[3]).IsUndef() {
		thouSep, err = asStringParam(z, args[3], "number_format", "4", "thousands_separator")
		if err != nil {
			return types.Value{}, err
		}
	}
	neg := num < 0
	absNum := math.Abs(num)
	dec := max(int(decimals), 0)
	pow := math.Pow10(dec)
	if !math.IsInf(pow, 0) && pow > 0 {
		absNum = math.Round(absNum*pow) / pow
	}
	formatted := strconv.FormatFloat(absNum, 'f', dec, 64)
	intPart := formatted
	fracPart := ""
	if before, after, ok := strings.Cut(formatted, "."); ok {
		intPart = before
		fracPart = after
	}
	var b strings.Builder
	if neg && (intPart != "0" || strings.Trim(fracPart, "0") != "") {
		b.WriteByte('-')
	}
	n := len(intPart)
	for i := range n {
		if i > 0 && (n-i)%3 == 0 {
			b.WriteString(thouSep)
		}
		b.WriteByte(intPart[i])
	}
	if dec > 0 {
		b.WriteString(decSep)
		b.WriteString(fracPart)
	}
	return newStr(z, b.String())
}

func nativeSscanf(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("sscanf", len(args), 2, -1); err != nil {
		return types.Value{}, err
	}
	str, err := asStringParam(z, args[0], "sscanf", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	format, err := asStringParam(z, args[1], "sscanf", "2", "format")
	if err != nil {
		return types.Value{}, err
	}

	var results []types.Value
	releaseResults := func() {
		for i := range results {
			types.ReleaseValue(z, &results[i])
		}
	}
	defer releaseResults()

	si, fi := 0, 0
	sn, fn := len(str), len(format)

	for fi < fn {
		if isWS(format[fi]) {
			for fi < fn && isWS(format[fi]) {
				fi++
			}
			for si < sn && isWS(str[si]) {
				si++
			}
			continue
		}
		if format[fi] != '%' {
			if si >= sn || str[si] != format[fi] {
				break
			}
			si++
			fi++
			continue
		}
		fi++
		if fi < fn && format[fi] == '%' {
			if si >= sn || str[si] != '%' {
				break
			}
			si++
			fi++
			continue
		}
		suppress := false
		if fi < fn && format[fi] == '*' {
			suppress = true
			fi++
		}
		width := 0
		for fi < fn && format[fi] >= '0' && format[fi] <= '9' {
			width = width*10 + int(format[fi]-'0')
			fi++
		}
		if fi >= fn {
			break
		}
		spec := format[fi]
		fi++

		if spec != 'c' && spec != '[' && spec != 'n' {
			for si < sn && isWS(str[si]) {
				si++
			}
		}
		if spec == 'n' {
			if !suppress {
				results = append(results, types.NewLong(int64(si)))
			}
			continue
		}
		if si >= sn {
			break
		}
		maxRead := sn - si
		if width > 0 && width < maxRead {
			maxRead = width
		}
		sub := str[si : si+maxRead]

		switch spec {
		case 'd', 'i', 'u':
			j := 0
			if j < len(sub) && (sub[j] == '+' || sub[j] == '-') {
				j++
			}
			base := 10
			if spec == 'i' && j < len(sub) && sub[j] == '0' {
				if j+1 < len(sub) && (sub[j+1] == 'x' || sub[j+1] == 'X') {
					base = 16
					j += 2
				} else {
					base = 8
				}
			}
			digits := 0
			for j < len(sub) {
				c := sub[j]
				ok := (c >= '0' && c <= '9') || (base == 16 && ((c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')))
				if !ok {
					break
				}
				j++
				digits++
			}
			if digits == 0 {
				goto doneScan
			}
			v, _ := strconv.ParseInt(sub[:j], 0, 64)
			if base == 10 {
				v, _ = strconv.ParseInt(sub[:j], 10, 64)
			}
			si += j
			if !suppress {
				results = append(results, types.NewLong(v))
			}
		case 'x', 'X':
			j := 0
			if j+1 < len(sub) && sub[j] == '0' && (sub[j+1] == 'x' || sub[j+1] == 'X') {
				j += 2
			}
			digits := 0
			for j < len(sub) {
				c := sub[j]
				if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
					j++
					digits++
				} else {
					break
				}
			}
			if digits == 0 {
				goto doneScan
			}
			token := strings.TrimPrefix(strings.TrimPrefix(sub[:j], "0x"), "0X")
			v, _ := strconv.ParseInt(token, 16, 64)
			si += j
			if !suppress {
				results = append(results, types.NewLong(v))
			}
		case 'o':
			j := 0
			for j < len(sub) && sub[j] >= '0' && sub[j] <= '7' {
				j++
			}
			if j == 0 {
				goto doneScan
			}
			v, _ := strconv.ParseInt(sub[:j], 8, 64)
			si += j
			if !suppress {
				results = append(results, types.NewLong(v))
			}
		case 'f', 'e', 'E', 'g', 'G':
			j := 0
			if j < len(sub) && (sub[j] == '+' || sub[j] == '-') {
				j++
			}
			digits := 0
			for j < len(sub) && sub[j] >= '0' && sub[j] <= '9' {
				j++
				digits++
			}
			if j < len(sub) && sub[j] == '.' {
				j++
				for j < len(sub) && sub[j] >= '0' && sub[j] <= '9' {
					j++
					digits++
				}
			}
			if digits == 0 {
				goto doneScan
			}
			if j < len(sub) && (sub[j] == 'e' || sub[j] == 'E') {
				k := j + 1
				if k < len(sub) && (sub[k] == '+' || sub[k] == '-') {
					k++
				}
				ed := 0
				for k < len(sub) && sub[k] >= '0' && sub[k] <= '9' {
					k++
					ed++
				}
				if ed > 0 {
					j = k
				}
			}
			f, _ := strconv.ParseFloat(sub[:j], 64)
			si += j
			if !suppress {
				results = append(results, types.NewDouble(f))
			}
		case 's':
			j := 0
			for j < len(sub) && !isWS(sub[j]) {
				j++
			}
			if j == 0 {
				goto doneScan
			}
			si += j
			if !suppress {
				sv, err := newStr(z, sub[:j])
				if err != nil {
					return types.Value{}, err
				}
				results = append(results, sv)
			}
		case 'c':
			nRead := 1
			if width > 0 {
				nRead = min(width, len(sub))
			}
			si += nRead
			if !suppress {
				sv, err := newStr(z, sub[:nRead])
				if err != nil {
					return types.Value{}, err
				}
				results = append(results, sv)
			}
		case '[':
			neg := false
			if fi < fn && format[fi] == '^' {
				neg = true
				fi++
			}
			var set [256]bool
			first := true
			for fi < fn && (format[fi] != ']' || first) {
				first = false
				if fi+2 < fn && format[fi+1] == '-' && format[fi+2] != ']' {
					for c := int(format[fi]); c <= int(format[fi+2]); c++ {
						set[c] = true
					}
					fi += 3
				} else {
					set[format[fi]] = true
					fi++
				}
			}
			if fi < fn && format[fi] == ']' {
				fi++
			}
			j := 0
			for j < len(sub) {
				inSet := set[sub[j]]
				if neg {
					inSet = !inSet
				}
				if !inSet {
					break
				}
				j++
			}
			if j == 0 {
				goto doneScan
			}
			si += j
			if !suppress {
				sv, err := newStr(z, sub[:j])
				if err != nil {
					return types.Value{}, err
				}
				results = append(results, sv)
			}
		}
	}
doneScan:
	if len(args) > 2 {
		for idx, refArg := range args[2:] {
			if refArg.Type == types.TypeReference {
				if idx < len(results) {
					types.AssignRef(z, refArg, results[idx])
				} else {
					types.AssignRef(z, refArg, types.NewNull())
				}
			}
		}
		if sn == 0 {
			return types.NewLong(-1), nil
		}
		return types.NewLong(int64(len(results))), nil
	}
	if sn == 0 {
		return types.NewNull(), nil
	}
	arr, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for i, r := range results {
		if err := setKey(z, &arr, types.NewLong(int64(i)), r); err != nil {
			return types.Value{}, err
		}
	}
	return arr, nil
}

func nativeLevenshtein(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("levenshtein", len(args), 2, 5); err != nil {
		return types.Value{}, err
	}
	s1, err := asStringParam(z, args[0], "levenshtein", "1", "string1")
	if err != nil {
		return types.Value{}, err
	}
	s2, err := asStringParam(z, args[1], "levenshtein", "2", "string2")
	if err != nil {
		return types.Value{}, err
	}
	costIns, costRep, costDel := int64(1), int64(1), int64(1)
	if len(args) >= 3 {
		if err := arity("levenshtein", len(args), 5, 5); err != nil {
			return types.Value{}, err
		}
		costIns, err = asIntParam(z, args[2], "levenshtein", "3", "insertion_cost")
		if err != nil {
			return types.Value{}, err
		}
		costRep, err = asIntParam(z, args[3], "levenshtein", "4", "replacement_cost")
		if err != nil {
			return types.Value{}, err
		}
		costDel, err = asIntParam(z, args[4], "levenshtein", "5", "deletion_cost")
		if err != nil {
			return types.Value{}, err
		}
	}
	n1, n2 := len(s1), len(s2)
	if n1 == 0 {
		return types.NewLong(int64(n2) * costIns), nil
	}
	if n2 == 0 {
		return types.NewLong(int64(n1) * costDel), nil
	}
	prev := make([]int64, n2+1)
	curr := make([]int64, n2+1)
	for j := 0; j <= n2; j++ {
		prev[j] = int64(j) * costIns
	}
	for i := 1; i <= n1; i++ {
		curr[0] = int64(i) * costDel
		c1 := s1[i-1]
		for j := 1; j <= n2; j++ {
			rep := int64(0)
			if c1 != s2[j-1] {
				rep = costRep
			}
			curr[j] = min(prev[j]+costDel, curr[j-1]+costIns, prev[j-1]+rep)
		}
		prev, curr = curr, prev
	}
	return types.NewLong(prev[n2]), nil
}

func similarTextChar(s1, s2 string) int {
	maxLen, pos1, pos2 := 0, 0, 0
	for p := 0; p < len(s1); p++ {
		for q := 0; q < len(s2); q++ {
			l := 0
			for p+l < len(s1) && q+l < len(s2) && s1[p+l] == s2[q+l] {
				l++
			}
			if l > maxLen {
				maxLen = l
				pos1 = p
				pos2 = q
			}
		}
	}
	sum := maxLen
	if sum > 0 {
		if pos1 > 0 && pos2 > 0 {
			sum += similarTextChar(s1[:pos1], s2[:pos2])
		}
		if pos1+maxLen < len(s1) && pos2+maxLen < len(s2) {
			sum += similarTextChar(s1[pos1+maxLen:], s2[pos2+maxLen:])
		}
	}
	return sum
}

func nativeSimilarText(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("similar_text", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	s1, err := asStringParam(z, args[0], "similar_text", "1", "string1")
	if err != nil {
		return types.Value{}, err
	}
	s2, err := asStringParam(z, args[1], "similar_text", "2", "string2")
	if err != nil {
		return types.Value{}, err
	}
	total := len(s1) + len(s2)
	sim := 0
	pct := 0.0
	if total > 0 {
		sim = similarTextChar(s1, s2)
		pct = float64(sim*200) / float64(total)
	}
	if len(args) == 3 && args[2].Type == types.TypeReference {
		types.AssignRef(z, args[2], types.NewDouble(pct))
	}
	return types.NewLong(int64(sim)), nil
}

var soundexTable = [26]byte{
	'0', '1', '2', '3', '0', '1', '2', '0', '0', '2', '2', '4', '5',
	'5', '0', '1', '2', '6', '2', '3', '0', '1', '0', '2', '0', '2',
}

func nativeSoundex(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("soundex", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "soundex", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	var out [4]byte
	out[0], out[1], out[2], out[3] = '0', '0', '0', '0'
	small := 0
	var last byte
	for i := 0; i < len(s) && small < 4; i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c < 'A' || c > 'Z' {
			continue
		}
		code := soundexTable[c-'A']
		if small == 0 {
			out[0] = c
			small = 1
			last = code
		} else if code != last {
			if code != '0' {
				out[small] = code
				small++
			}
			last = code
		}
	}
	return newBytes(z, out[:])
}

func nativePack(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("pack", len(args), 1, -1); err != nil {
		return types.Value{}, err
	}
	format, err := asStringParam(z, args[0], "pack", "1", "format")
	if err != nil {
		return types.Value{}, err
	}
	vals := args[1:]
	vi := 0
	var out []byte
	fi := 0
	fn := len(format)

	for fi < fn {
		code := format[fi]
		fi++
		if unicode.IsSpace(rune(code)) {
			continue
		}
		isStar := false
		count := 1
		if fi < fn && format[fi] == '*' {
			isStar = true
			fi++
		} else if fi < fn && format[fi] >= '0' && format[fi] <= '9' {
			count = 0
			for fi < fn && format[fi] >= '0' && format[fi] <= '9' {
				count = count*10 + int(format[fi]-'0')
				fi++
			}
		}

		switch code {
		case 'a', 'A', 'Z':
			if vi >= len(vals) {
				return types.Value{}, valErr("pack", "1", "format", fmt.Sprintf("Type %c: not enough arguments", code))
			}
			s, err := castString(z, vals[vi])
			if err != nil {
				return types.Value{}, err
			}
			vi++
			pad := byte(0)
			if code == 'A' {
				pad = ' '
			}
			if isStar {
				out = append(out, s...)
				if code == 'Z' {
					out = append(out, 0)
				}
			} else if code == 'Z' {
				if count > 0 {
					take := min(len(s), count-1)
					out = append(out, s[:take]...)
					for range count - take {
						out = append(out, 0)
					}
				}
			} else {
				if len(s) >= count {
					out = append(out, s[:count]...)
				} else {
					out = append(out, s...)
					for range count - len(s) {
						out = append(out, pad)
					}
				}
			}
		case 'h', 'H':
			if vi >= len(vals) {
				return types.Value{}, valErr("pack", "1", "format", fmt.Sprintf("Type %c: not enough arguments", code))
			}
			s, err := castString(z, vals[vi])
			if err != nil {
				return types.Value{}, err
			}
			vi++
			nibbles := count
			if isStar || nibbles > len(s) {
				nibbles = len(s)
			}
			for i := 0; i < nibbles; i += 2 {
				n1, _ := strconv.ParseUint(s[i:i+1], 16, 8)
				n2 := uint64(0)
				if i+1 < nibbles {
					n2, _ = strconv.ParseUint(s[i+1:i+2], 16, 8)
				}
				if code == 'H' {
					out = append(out, byte((n1<<4)|n2))
				} else {
					out = append(out, byte((n2<<4)|n1))
				}
			}
		case 'x':
			for range count {
				out = append(out, 0)
			}
		case 'X':
			if count > len(out) {
				return types.Value{}, valErr("pack", "1", "format", "Type X: outside of string")
			}
			out = out[:len(out)-count]
		case '@':
			if count > len(out) {
				for range count - len(out) {
					out = append(out, 0)
				}
			} else {
				out = out[:count]
			}
		default:
			reps := count
			if isStar {
				reps = len(vals) - vi
			}
			for range reps {
				if vi >= len(vals) {
					return types.Value{}, valErr("pack", "1", "format", fmt.Sprintf("Type %c: not enough arguments", code))
				}
				arg := vals[vi]
				vi++
				switch code {
				case 'c', 'C':
					v, err := asIntLoose(z, arg)
					if err != nil {
						return types.Value{}, err
					}
					out = append(out, byte(v))
				case 's', 'S', 'v':
					v, err := asIntLoose(z, arg)
					if err != nil {
						return types.Value{}, err
					}
					var b [2]byte
					binary.LittleEndian.PutUint16(b[:], uint16(v))
					out = append(out, b[:]...)
				case 'n':
					v, err := asIntLoose(z, arg)
					if err != nil {
						return types.Value{}, err
					}
					var b [2]byte
					binary.BigEndian.PutUint16(b[:], uint16(v))
					out = append(out, b[:]...)
				case 'i', 'I', 'l', 'L', 'V':
					v, err := asIntLoose(z, arg)
					if err != nil {
						return types.Value{}, err
					}
					var b [4]byte
					binary.LittleEndian.PutUint32(b[:], uint32(v))
					out = append(out, b[:]...)
				case 'N':
					v, err := asIntLoose(z, arg)
					if err != nil {
						return types.Value{}, err
					}
					var b [4]byte
					binary.BigEndian.PutUint32(b[:], uint32(v))
					out = append(out, b[:]...)
				case 'q', 'Q', 'P':
					v, err := asIntLoose(z, arg)
					if err != nil {
						return types.Value{}, err
					}
					var b [8]byte
					binary.LittleEndian.PutUint64(b[:], uint64(v))
					out = append(out, b[:]...)
				case 'J':
					v, err := asIntLoose(z, arg)
					if err != nil {
						return types.Value{}, err
					}
					var b [8]byte
					binary.BigEndian.PutUint64(b[:], uint64(v))
					out = append(out, b[:]...)
				case 'f', 'g':
					fv, err := asFloatParam(z, arg, "pack", "2", "values")
					if err != nil {
						return types.Value{}, err
					}
					var b [4]byte
					binary.LittleEndian.PutUint32(b[:], math.Float32bits(float32(fv)))
					out = append(out, b[:]...)
				case 'G':
					fv, err := asFloatParam(z, arg, "pack", "2", "values")
					if err != nil {
						return types.Value{}, err
					}
					var b [4]byte
					binary.BigEndian.PutUint32(b[:], math.Float32bits(float32(fv)))
					out = append(out, b[:]...)
				case 'd', 'e':
					fv, err := asFloatParam(z, arg, "pack", "2", "values")
					if err != nil {
						return types.Value{}, err
					}
					var b [8]byte
					binary.LittleEndian.PutUint64(b[:], math.Float64bits(fv))
					out = append(out, b[:]...)
				case 'E':
					fv, err := asFloatParam(z, arg, "pack", "2", "values")
					if err != nil {
						return types.Value{}, err
					}
					var b [8]byte
					binary.BigEndian.PutUint64(b[:], math.Float64bits(fv))
					out = append(out, b[:]...)
				default:
					return types.Value{}, valErr("pack", "1", "format", fmt.Sprintf("Type %c: unknown format code", code))
				}
			}
		}
	}
	return newBytes(z, out)
}

func nativeUnpack(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("unpack", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	format, err := asStringParam(z, args[0], "unpack", "1", "format")
	if err != nil {
		return types.Value{}, err
	}
	data, err := asStringParam(z, args[1], "unpack", "2", "string")
	if err != nil {
		return types.Value{}, err
	}
	off := int64(0)
	if len(args) == 3 {
		off, err = asIntParam(z, args[2], "unpack", "3", "offset")
		if err != nil {
			return types.Value{}, err
		}
	}
	if off < 0 || off > int64(len(data)) {
		return types.Value{}, valErr("unpack", "3", "offset", "must be contained in argument #2 ($string)")
	}
	pos := int(off)
	arr, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}

	putEntry := func(name string, idx int, multi bool, val types.Value) error {
		defer types.ReleaseValue(z, &val)
		key := name
		if multi {
			if name == "" {
				return setKey(z, &arr, types.NewLong(int64(idx)), val)
			}
			key = name + strconv.Itoa(idx)
		} else if key == "" {
			return setKey(z, &arr, types.NewLong(1), val)
		}
		kv, err := newStr(z, key)
		if err != nil {
			return err
		}
		defer types.ReleaseValue(z, &kv)
		return setKey(z, &arr, kv, val)
	}

	parts := strings.SplitSeq(format, "/")
	for part := range parts {
		if part == "" {
			continue
		}
		code := part[0]
		rest := part[1:]
		isStar := false
		count := 1
		multi := false
		ri := 0
		if ri < len(rest) && rest[ri] == '*' {
			isStar = true
			multi = true
			ri++
		} else if ri < len(rest) && rest[ri] >= '0' && rest[ri] <= '9' {
			count = 0
			for ri < len(rest) && rest[ri] >= '0' && rest[ri] <= '9' {
				count = count*10 + int(rest[ri]-'0')
				ri++
			}
			if count > 1 {
				multi = true
			}
		}
		name := rest[ri:]

		switch code {
		case 'a', 'A', 'Z':
			nBytes := count
			if isStar {
				nBytes = len(data) - pos
			}
			if pos+nBytes > len(data) {
				types.ReleaseValue(z, &arr)
				return types.NewBool(false), nil
			}
			sub := data[pos : pos+nBytes]
			pos += nBytes
			switch code {
			case 'A':
				sub = strings.TrimRight(sub, " \t\r\n\x00")
			case 'Z':
				if nul := strings.IndexByte(sub, 0); nul >= 0 {
					sub = sub[:nul]
				}
			}
			sv, err := newStr(z, sub)
			if err != nil {
				return types.Value{}, err
			}
			if err := putEntry(name, 1, false, sv); err != nil {
				return types.Value{}, err
			}
		case 'h', 'H':
			nibbles := count
			if isStar {
				nibbles = (len(data) - pos) * 2
			}
			nBytes := (nibbles + 1) / 2
			if pos+nBytes > len(data) {
				types.ReleaseValue(z, &arr)
				return types.NewBool(false), nil
			}
			raw := data[pos : pos+nBytes]
			pos += nBytes
			var b strings.Builder
			for i := range nBytes {
				hi := raw[i] >> 4
				lo := raw[i] & 0x0f
				if code == 'h' {
					hi, lo = lo, hi
				}
				b.WriteString(hex.EncodeToString([]byte{(hi << 4) | lo}))
			}
			s := b.String()
			if len(s) > nibbles {
				s = s[:nibbles]
			}
			sv, err := newStr(z, s)
			if err != nil {
				return types.Value{}, err
			}
			if err := putEntry(name, 1, false, sv); err != nil {
				return types.Value{}, err
			}
		case 'x':
			if isStar {
				pos = len(data)
			} else {
				pos += count
			}
		case 'X':
			pos -= count
		case '@':
			pos = int(off) + count
		default:
			elemSize := 1
			switch code {
			case 'c', 'C':
				elemSize = 1
			case 's', 'S', 'n', 'v':
				elemSize = 2
			case 'i', 'I', 'l', 'L', 'N', 'V', 'f', 'g', 'G':
				elemSize = 4
			case 'q', 'Q', 'J', 'P', 'd', 'e', 'E':
				elemSize = 8
			}
			reps := count
			if isStar {
				reps = (len(data) - pos) / elemSize
			}
			for i := 1; i <= reps; i++ {
				if pos+elemSize > len(data) {
					types.ReleaseValue(z, &arr)
					return types.NewBool(false), nil
				}
				chunk := []byte(data[pos : pos+elemSize])
				pos += elemSize
				var val types.Value
				switch code {
				case 'c':
					val = types.NewLong(int64(int8(chunk[0])))
				case 'C':
					val = types.NewLong(int64(chunk[0]))
				case 's':
					val = types.NewLong(int64(int16(binary.LittleEndian.Uint16(chunk))))
				case 'S', 'v':
					val = types.NewLong(int64(binary.LittleEndian.Uint16(chunk)))
				case 'n':
					val = types.NewLong(int64(binary.BigEndian.Uint16(chunk)))
				case 'i', 'l':
					val = types.NewLong(int64(int32(binary.LittleEndian.Uint32(chunk))))
				case 'I', 'L', 'V':
					val = types.NewLong(int64(binary.LittleEndian.Uint32(chunk)))
				case 'N':
					val = types.NewLong(int64(binary.BigEndian.Uint32(chunk)))
				case 'q', 'Q', 'P':
					val = types.NewLong(int64(binary.LittleEndian.Uint64(chunk)))
				case 'J':
					val = types.NewLong(int64(binary.BigEndian.Uint64(chunk)))
				case 'f', 'g':
					val = types.NewDouble(float64(math.Float32frombits(binary.LittleEndian.Uint32(chunk))))
				case 'G':
					val = types.NewDouble(float64(math.Float32frombits(binary.BigEndian.Uint32(chunk))))
				case 'd', 'e':
					val = types.NewDouble(math.Float64frombits(binary.LittleEndian.Uint64(chunk)))
				case 'E':
					val = types.NewDouble(math.Float64frombits(binary.BigEndian.Uint64(chunk)))
				}
				if err := putEntry(name, i, multi, val); err != nil {
					return types.Value{}, err
				}
			}
		}
	}
	return arr, nil
}

func nativeStrcmp(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strcmp", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	a, err := asStringParam(z, args[0], "strcmp", "1", "string1")
	if err != nil {
		return types.Value{}, err
	}
	b, err := asStringParam(z, args[1], "strcmp", "2", "string2")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(strings.Compare(a, b))), nil
}

func nativeStrcasecmp(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strcasecmp", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	a, err := asStringParam(z, args[0], "strcasecmp", "1", "string1")
	if err != nil {
		return types.Value{}, err
	}
	b, err := asStringParam(z, args[1], "strcasecmp", "2", "string2")
	if err != nil {
		return types.Value{}, err
	}
	la := asciiLowerString(a)
	lb := asciiLowerString(b)
	lim := min(len(la), len(lb))
	for i := range lim {
		if la[i] != lb[i] {
			return types.NewLong(int64(la[i]) - int64(lb[i])), nil
		}
	}
	return types.NewLong(int64(len(la)) - int64(len(lb))), nil
}

func nativeStrncmp(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strncmp", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	a, err := asStringParam(z, args[0], "strncmp", "1", "string1")
	if err != nil {
		return types.Value{}, err
	}
	b, err := asStringParam(z, args[1], "strncmp", "2", "string2")
	if err != nil {
		return types.Value{}, err
	}
	n, err := asIntParam(z, args[2], "strncmp", "3", "length")
	if err != nil {
		return types.Value{}, err
	}
	if n < 0 {
		return types.Value{}, valErr("strncmp", "3", "length", "must be greater than or equal to 0")
	}
	if int64(len(a)) > n {
		a = a[:n]
	}
	if int64(len(b)) > n {
		b = b[:n]
	}
	return types.NewLong(int64(strings.Compare(a, b))), nil
}

func nativeStrSplit(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("str_split", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "str_split", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	length := int64(1)
	if len(args) == 2 {
		length, err = asIntParam(z, args[1], "str_split", "2", "length")
		if err != nil {
			return types.Value{}, err
		}
		if length < 1 {
			return types.Value{}, valErr("str_split", "2", "length", "must be greater than 0")
		}
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	if s == "" {
		return out, nil
	}
	step := int(length)
	idx := int64(0)
	for i := 0; i < len(s); i += step {
		end := min(i+step, len(s))
		sv, err := newStr(z, s[i:end])
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		err = setKey(z, &out, types.NewLong(idx), sv)
		types.ReleaseValue(z, &sv)
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		idx++
	}
	return out, nil
}

func nativeMbStrSplit(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_str_split", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "mb_str_split", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	length := int64(1)
	if len(args) >= 2 && !deref(z, args[1]).IsNull() && !deref(z, args[1]).IsUndef() {
		length, err = asIntParam(z, args[1], "mb_str_split", "2", "length")
		if err != nil {
			return types.Value{}, err
		}
		if length < 1 {
			return types.Value{}, valErr("mb_str_split", "2", "length", "must be greater than 0")
		}
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	if s == "" {
		return out, nil
	}
	runes := []rune(s)
	step := int(length)
	idx := int64(0)
	for i := 0; i < len(runes); i += step {
		end := min(i+step, len(runes))
		sv, err := newStr(z, string(runes[i:end]))
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		err = setKey(z, &out, types.NewLong(idx), sv)
		types.ReleaseValue(z, &sv)
		if err != nil {
			types.ReleaseValue(z, &out)
			return types.Value{}, err
		}
		idx++
	}
	return out, nil
}

func nativeMbTrim(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_trim", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "mb_trim", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	if len(args) >= 2 && !deref(z, args[1]).IsNull() && !deref(z, args[1]).IsUndef() {
		chars, err := asStringParam(z, args[1], "mb_trim", "2", "characters")
		if err != nil {
			return types.Value{}, err
		}
		return newStr(z, strings.Trim(s, chars))
	}
	return newStr(z, strings.TrimSpace(s))
}

func nativeStrrev(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strrev", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "strrev", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	n := len(s)
	if n <= 1 {
		return newStr(z, s)
	}
	b := make([]byte, n)
	for i := range n {
		b[i] = s[n-1-i]
	}
	return newBytes(z, b)
}

func nativeSubstrCount(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("substr_count", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	hay, err := asStringParam(z, args[0], "substr_count", "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asStringParam(z, args[1], "substr_count", "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	if needle == "" {
		return types.Value{}, valErr("substr_count", "2", "needle", "cannot be empty")
	}
	off := int64(0)
	if len(args) >= 3 {
		off, err = asIntParam(z, args[2], "substr_count", "3", "offset")
		if err != nil {
			return types.Value{}, err
		}
	}
	start, ok := offsetIn(len(hay), off)
	if !ok {
		return types.Value{}, valErr("substr_count", "3", "offset", "must be contained in argument #1 ($haystack)")
	}
	end := len(hay)
	if len(args) == 4 && !deref(z, args[3]).IsNull() && !deref(z, args[3]).IsUndef() {
		l64, err := asIntParam(z, args[3], "substr_count", "4", "length")
		if err != nil {
			return types.Value{}, err
		}
		if l64 < 0 {
			end = len(hay) + int(l64)
			if end < start {
				return types.Value{}, valErr("substr_count", "4", "length", "must be contained in argument #1 ($haystack)")
			}
		} else {
			if start+int(l64) > len(hay) {
				return types.Value{}, valErr("substr_count", "4", "length", "must be contained in argument #1 ($haystack)")
			}
			end = start + int(l64)
		}
	}
	return types.NewLong(int64(strings.Count(hay[start:end], needle))), nil
}

func strstrCommon(z *zone.Zone, name string, args []types.Value, ci bool) (types.Value, error) {
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
	before := false
	if len(args) == 3 {
		before = asBool(z, args[2])
	}
	if needle == "" {
		if before {
			return newStr(z, "")
		}
		return newStr(z, hay)
	}
	var idx int
	if ci {
		idx = strings.Index(asciiLowerString(hay), asciiLowerString(needle))
	} else {
		idx = strings.Index(hay, needle)
	}
	if idx < 0 {
		return types.NewBool(false), nil
	}
	if before {
		return newStr(z, hay[:idx])
	}
	return newStr(z, hay[idx:])
}

func nativeStrstr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return strstrCommon(z, "strstr", args, false)
}

func nativeStrchr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return strstrCommon(z, "strchr", args, false)
}

func nativeStristr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return strstrCommon(z, "stristr", args, true)
}

func nativeStrrchr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strrchr", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	hay, err := asStringParam(z, args[0], "strrchr", "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asStringParam(z, args[1], "strrchr", "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	before := false
	if len(args) == 3 {
		before = asBool(z, args[2])
	}
	if needle == "" {
		return types.NewBool(false), nil
	}
	c := needle[0]
	idx := strings.LastIndexByte(hay, c)
	if idx < 0 {
		return types.NewBool(false), nil
	}
	if before {
		return newStr(z, hay[:idx])
	}
	return newStr(z, hay[idx:])
}

func replaceFoldAll(s, old, repl string) string {
	if old == "" {
		return s
	}
	lowS := asciiLowerString(s)
	lowOld := asciiLowerString(old)
	var b strings.Builder
	pos := 0
	for {
		idx := strings.Index(lowS[pos:], lowOld)
		if idx < 0 {
			b.WriteString(s[pos:])
			break
		}
		b.WriteString(s[pos : pos+idx])
		b.WriteString(repl)
		pos += idx + len(old)
	}
	return b.String()
}

func replaceStringFold(z *zone.Zone, search, repl types.Value, subject string) (string, error) {
	if search.Type == types.TypeArray {
		_, sv := arrayItems(z, search)
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
			subject = replaceFoldAll(subject, ss, rs)
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
	return replaceFoldAll(subject, ss, rs), nil
}

func nativeStrIreplace(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("str_ireplace", len(args), 3, 4); err != nil {
		return types.Value{}, err
	}
	search := deref(z, args[0])
	repl := deref(z, args[1])
	subj := deref(z, args[2])
	if subj.Type == types.TypeArray {
		out, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		keys, vals := arrayItems(z, subj)
		for i := range vals {
			s, err := castString(z, vals[i])
			if err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
			rep, err := replaceStringFold(z, search, repl, s)
			if err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
			sv, err := newStr(z, rep)
			if err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
			err = setKey(z, &out, keys[i], sv)
			types.ReleaseValue(z, &sv)
			if err != nil {
				types.ReleaseValue(z, &out)
				return types.Value{}, err
			}
		}
		return out, nil
	}
	s, err := asStringParam(z, subj, "str_ireplace", "3", "subject")
	if err != nil {
		return types.Value{}, err
	}
	out, err := replaceStringFold(z, search, repl, s)
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, out)
}

func nativeUcwords(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("ucwords", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "ucwords", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	sep := " \t\r\n\f\v"
	if len(args) == 2 {
		sep, err = asStringParam(z, args[1], "ucwords", "2", "separators")
		if err != nil {
			return types.Value{}, err
		}
	}
	if s == "" {
		return newStr(z, "")
	}
	b := []byte(s)
	atWordStart := true
	for i := range b {
		if strings.IndexByte(sep, b[i]) >= 0 {
			atWordStart = true
			continue
		}
		if atWordStart && b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
		}
		atWordStart = false
	}
	return newBytes(z, b)
}

func nativeAddslashes(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("addslashes", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "addslashes", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := range len(s) {
		switch s[i] {
		case '\'':
			b.WriteString(`\'`)
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case 0:
			b.WriteString(`\0`)
		default:
			b.WriteByte(s[i])
		}
	}
	return newStr(z, b.String())
}

func nativeStripslashes(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("stripslashes", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "stripslashes", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			if i+1 < len(s) {
				i++
				if s[i] == '0' {
					b.WriteByte(0)
				} else {
					b.WriteByte(s[i])
				}
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return newStr(z, b.String())
}

func nativeHtmlspecialchars(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("htmlspecialchars", len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "htmlspecialchars", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	flags := int64(3)
	if len(args) >= 2 {
		flags, err = asIntParam(z, args[1], "htmlspecialchars", "2", "flags")
		if err != nil {
			return types.Value{}, err
		}
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := range len(s) {
		c := s[i]
		switch c {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			if (flags & 3) != 0 {
				b.WriteString("&quot;")
			} else {
				b.WriteByte('"')
			}
		case '\'':
			if (flags & 2) != 0 {
				if (flags & (16 | 32 | 48)) == 0 {
					b.WriteString("&#039;")
				} else {
					b.WriteString("&apos;")
				}
			} else {
				b.WriteByte('\'')
			}
		default:
			b.WriteByte(c)
		}
	}
	return newStr(z, b.String())
}

func nativeHtmlspecialcharsDecode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("htmlspecialchars_decode", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "htmlspecialchars_decode", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	flags := int64(3)
	if len(args) == 2 {
		flags, err = asIntParam(z, args[1], "htmlspecialchars_decode", "2", "flags")
		if err != nil {
			return types.Value{}, err
		}
	}
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	if (flags & 3) != 0 {
		s = strings.ReplaceAll(s, "&quot;", "\"")
	}
	if (flags & 2) != 0 {
		s = strings.ReplaceAll(s, "&#039;", "'")
		s = strings.ReplaceAll(s, "&#39;", "'")
		s = strings.ReplaceAll(s, "&apos;", "'")
	}
	s = strings.ReplaceAll(s, "&amp;", "&")
	return newStr(z, s)
}

func nativeVsprintf(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("vsprintf", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	format, err := asStringParam(z, args[0], "vsprintf", "1", "format")
	if err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[1], "vsprintf", "2", "values")
	if err != nil {
		return types.Value{}, err
	}
	_, vals := arrayItems(z, arr)
	out, err := phpSprintf(z, format, vals)
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, out)
}

func nativeVprintf(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("vprintf", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	format, err := asStringParam(z, args[0], "vprintf", "1", "format")
	if err != nil {
		return types.Value{}, err
	}
	arr, err := asArray(z, args[1], "vprintf", "2", "values")
	if err != nil {
		return types.Value{}, err
	}
	_, vals := arrayItems(z, arr)
	text, err := phpSprintf(z, format, vals)
	if err != nil {
		return types.Value{}, err
	}
	writeOut(out, []byte(text))
	return types.NewLong(int64(len(text))), nil
}

func nativeFprintf(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fprintf", len(args), 2, -1); err != nil {
		return types.Value{}, err
	}
	format, err := asStringParam(z, args[1], "fprintf", "2", "format")
	if err != nil {
		return types.Value{}, err
	}
	text, err := phpSprintf(z, format, args[2:])
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(len(text))), nil
}

func nativeMetaphone(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("metaphone", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "metaphone", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	maxPhonemes := int64(0)
	if len(args) == 2 {
		maxPhonemes, err = asIntParam(z, args[1], "metaphone", "2", "max_phonemes")
		if err != nil {
			return types.Value{}, err
		}
		if maxPhonemes < 0 {
			return types.Value{}, valErr("metaphone", "2", "max_phonemes", "must be greater than or equal to 0")
		}
	}
	var clean []byte
	for i := range len(s) {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c >= 'A' && c <= 'Z' {
			clean = append(clean, c)
		}
	}
	if len(clean) == 0 {
		return newStr(z, "")
	}
	isVowel := func(c byte) bool {
		return c == 'A' || c == 'E' || c == 'I' || c == 'O' || c == 'U'
	}
	var out []byte
	i := 0
	n := len(clean)
	if n >= 2 {
		pair := string(clean[:2])
		if pair == "KN" || pair == "GN" || pair == "PN" || pair == "AE" || pair == "WR" {
			i = 1
		}
	}
	if clean[0] == 'X' {
		out = append(out, 'S')
		i = 1
	} else if n >= 2 && clean[0] == 'W' && clean[1] == 'H' {
		out = append(out, 'W')
		i = 2
	}
	for ; i < n; i++ {
		if maxPhonemes > 0 && int64(len(out)) >= maxPhonemes {
			break
		}
		c := clean[i]
		if c != 'C' && i > 0 && clean[i-1] == c {
			continue
		}
		if isVowel(c) {
			if i == 0 {
				out = append(out, c)
			}
			continue
		}
		prev := byte(0)
		if i > 0 {
			prev = clean[i-1]
		}
		next := byte(0)
		if i+1 < n {
			next = clean[i+1]
		}
		switch c {
		case 'B':
			if i != n-1 || prev != 'M' {
				out = append(out, 'B')
			}
		case 'C':
			if next == 'I' || next == 'E' || next == 'Y' {
				if next == 'I' && i+2 < n && clean[i+2] == 'A' {
					out = append(out, 'X')
				} else if prev != 'S' {
					out = append(out, 'S')
				}
			} else if next == 'H' {
				out = append(out, 'X')
				i++
			} else {
				out = append(out, 'K')
			}
		case 'D':
			if next == 'G' && i+2 < n && (clean[i+2] == 'E' || clean[i+2] == 'Y' || clean[i+2] == 'I') {
				out = append(out, 'J')
				i += 2
			} else {
				out = append(out, 'T')
			}
		case 'F', 'J', 'L', 'M', 'N', 'R':
			out = append(out, c)
		case 'G':
			if next == 'H' && (i+2 >= n || !isVowel(clean[i+2])) {
				continue
			}
			if i > 0 && ((i+1 == n && next == 'N') || (i+3 == n && next == 'N' && clean[i+2] == 'E' && clean[i+3] == 'D')) {
				continue
			}
			if next == 'I' || next == 'E' || next == 'Y' {
				if prev != 'G' {
					out = append(out, 'J')
				}
			} else {
				out = append(out, 'K')
			}
		case 'H':
			if isVowel(next) && prev != 'C' && prev != 'S' && prev != 'P' && prev != 'T' && prev != 'G' {
				out = append(out, 'H')
			}
		case 'K':
			if prev != 'C' {
				out = append(out, 'K')
			}
		case 'P':
			if next == 'H' {
				out = append(out, 'F')
				i++
			} else {
				out = append(out, 'P')
			}
		case 'Q':
			out = append(out, 'K')
		case 'S':
			if next == 'H' || (next == 'I' && i+2 < n && (clean[i+2] == 'O' || clean[i+2] == 'A')) {
				out = append(out, 'X')
			} else {
				out = append(out, 'S')
			}
		case 'T':
			if next == 'I' && i+2 < n && (clean[i+2] == 'O' || clean[i+2] == 'A') {
				out = append(out, 'X')
			} else if next == 'H' {
				out = append(out, '0')
				i++
			} else if !(next == 'C' && i+2 < n && clean[i+2] == 'H') {
				out = append(out, 'T')
			}
		case 'V':
			out = append(out, 'F')
		case 'W', 'Y':
			if isVowel(next) {
				out = append(out, c)
			}
		case 'X':
			out = append(out, 'K', 'S')
		case 'Z':
			out = append(out, 'S')
		}
	}
	if maxPhonemes > 0 && int64(len(out)) > maxPhonemes {
		out = out[:maxPhonemes]
	}
	return newBytes(z, out)
}

func nativeUtf8Encode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("utf8_encode", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "utf8_encode", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	var b strings.Builder
	b.Grow(len(s) * 2)
	for i := range len(s) {
		b.WriteRune(rune(s[i]))
	}
	return newStr(z, b.String())
}

func nativeMbConvertEncoding(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_convert_encoding", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "mb_convert_encoding", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, s)
}

func nativeMbCheckEncoding(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_check_encoding", len(args), 0, 2); err != nil {
		return types.Value{}, err
	}
	if len(args) == 0 || deref(z, args[0]).IsNull() || deref(z, args[0]).IsUndef() {
		return types.NewBool(true), nil
	}
	s, err := asStringParam(z, args[0], "mb_check_encoding", "1", "value")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewBool(utf8.ValidString(s)), nil
}

func nativeIconv(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("iconv", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[2], "iconv", "3", "string")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, s)
}

func nativeNlLanginfo(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("nl_langinfo", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	return newStr(z, "UTF-8")
}

func nativeSetlocale(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("setlocale", len(args), 2, -1); err != nil {
		return types.Value{}, err
	}
	return newStr(z, "C")
}

func nativeLocaleconv(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("localeconv", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	k, _ := newStr(z, "decimal_point")
	v, _ := newStr(z, ".")
	_ = setKey(z, &out, k, v)
	types.ReleaseValue(z, &k)
	types.ReleaseValue(z, &v)
	return out, nil
}

