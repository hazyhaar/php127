package ext

import (
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	encUTF8 = iota
	encBytes
)

func foldEnc(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c == '_' {
			c = '-'
		}
		b.WriteByte(c)
	}
	return b.String()
}

func resolveEncoding(name string) (int, bool) {
	switch foldEnc(name) {
	case "utf-8", "utf8":
		return encUTF8, true
	case "ascii", "us-ascii", "7bit", "8bit", "binary", "pass":
		return encBytes, true
	default:
		return 0, false
	}
}

func encodingArg(z *zone.Zone, args []types.Value, i int, fn string) (int, error) {
	if len(args) <= i || args[i].IsUndef() || deref(z, args[i]).IsNull() {
		return encUTF8, nil
	}
	s, err := asString(z, args[i], fn, idxOf(i), "encoding")
	if err != nil {
		return 0, err
	}
	kind, ok := resolveEncoding(s)
	if !ok {
		return 0, ValueError{fn + "(): Argument #" + idxOf(i) + " ($encoding) must be a valid encoding, \"" + s + "\" given"}
	}
	return kind, nil
}

func idxOf(i int) string {
	return string(rune('1' + i))
}

func cpIndex(s string) []int {
	idx := make([]int, 0, len(s)+1)
	for i := 0; i < len(s); i++ {
		if s[i]&0xc0 != 0x80 {
			idx = append(idx, i)
		}
	}
	idx = append(idx, len(s))
	return idx
}

func charLen(s string, kind int) int {
	if kind == encBytes {
		return len(s)
	}
	raw := []byte(s)
	if stdlib.C2_utf8_validate(raw, uint64(len(raw))) == 1 {
		return int(stdlib.C2_utf8_count(raw, uint64(len(raw))))
	}
	return mbInvalidUTF8Count(s)
}

func mbInvalidUTF8Count(s string) int {
	n := 0
	for i := 0; i < len(s); {
		b0 := s[i]
		if b0 < 0x80 {
			i++
			n++
			continue
		}
		if b0 < 0xc2 || b0 > 0xf4 {
			i++
			n++
			continue
		}
		if b0 < 0xe0 {
			if i+1 < len(s) && s[i+1]&0xc0 == 0x80 {
				i += 2
			} else {
				i++
			}
			n++
			continue
		}
		if b0 < 0xf0 {
			if i+1 < len(s) {
				b1 := s[i+1]
				valid1 := b1&0xc0 == 0x80 && (b0 != 0xe0 || b1 >= 0xa0) && (b0 != 0xed || b1 < 0xa0)
				if valid1 {
					if i+2 < len(s) && s[i+2]&0xc0 == 0x80 {
						i += 3
					} else {
						i += 2
					}
					n++
					continue
				}
			}
			i++
			n++
			continue
		}
		if i+1 < len(s) {
			b1 := s[i+1]
			valid1 := b1&0xc0 == 0x80 && (b0 != 0xf0 || b1 >= 0x90) && (b0 != 0xf4 || b1 < 0x90)
			if valid1 {
				if i+2 < len(s) && s[i+2]&0xc0 == 0x80 {
					if i+3 < len(s) && s[i+3]&0xc0 == 0x80 {
						i += 4
					} else {
						i += 3
					}
				} else {
					i += 2
				}
				n++
				continue
			}
		}
		i++
		n++
	}
	return n
}

func nativeMBStrlen(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_strlen", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "mb_strlen", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	kind, err := encodingArg(z, args, 1, "mb_strlen")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(charLen(s, kind))), nil
}

func sliceChars(s string, start int, length *int, kind int) string {
	if kind == encBytes {
		n := len(s)
		if start < 0 {
			start += n
		}
		if start < 0 {
			start = 0
		}
		if start > n {
			return ""
		}
		if length == nil {
			return s[start:]
		}
		ln := *length
		if ln < 0 {
			end := n + ln
			if end < start {
				return ""
			}
			return s[start:end]
		}
		end := min(start+ln, n)
		return s[start:end]
	}
	idx := cpIndex(s)
	n := len(idx) - 1
	if start < 0 {
		start += n
	}
	if start < 0 {
		start = 0
	}
	if start > n {
		return ""
	}
	if length == nil {
		return s[idx[start]:]
	}
	ln := *length
	if ln < 0 {
		end := n + ln
		if end < start {
			return ""
		}
		return s[idx[start]:idx[end]]
	}
	end := min(start+ln, n)
	return s[idx[start]:idx[end]]
}

func nativeMBSubstr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_substr", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "mb_substr", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	start, err := asInt(z, args[1], "mb_substr", "2", "start")
	if err != nil {
		return types.Value{}, err
	}
	var length *int
	if len(args) >= 3 && !args[2].IsUndef() && !deref(z, args[2]).IsNull() {
		n, err := asInt(z, args[2], "mb_substr", "3", "length")
		if err != nil {
			return types.Value{}, err
		}
		ln := int(n)
		length = &ln
	}
	kind, err := encodingArg(z, args, 3, "mb_substr")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, sliceChars(s, int(start), length, kind))
}

func offsetContained(n int, off int64) (int, bool) {
	if off > int64(n) || off < -int64(n) {
		return 0, false
	}
	if off < 0 {
		off += int64(n)
	}
	return int(off), true
}

func nativeMBStrpos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return mbPos(z, args, "mb_strpos", false)
}

func nativeMBStrrpos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return mbPos(z, args, "mb_strrpos", true)
}

func mbPos(z *zone.Zone, args []types.Value, name string, reverse bool) (types.Value, error) {
	if err := arity(name, len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	hay, err := asString(z, args[0], name, "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asString(z, args[1], name, "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	off := int64(0)
	if len(args) >= 3 && !args[2].IsUndef() && !deref(z, args[2]).IsNull() {
		off, err = asInt(z, args[2], name, "3", "offset")
		if err != nil {
			return types.Value{}, err
		}
	}
	kind, err := encodingArg(z, args, 3, name)
	if err != nil {
		return types.Value{}, err
	}
	n := charLen(hay, kind)
	start, ok := offsetContained(n, off)
	if !ok {
		return types.Value{}, ValueError{name + "(): Argument #3 ($offset) must be contained in argument #1 ($haystack)"}
	}
	if kind == encBytes {
		return bytePos(hay, needle, start, off < 0, reverse)
	}
	return cpPos(hay, needle, start, off < 0, n, reverse)
}

func bytePos(hay, needle string, start int, neg, reverse bool) (types.Value, error) {
	if needle == "" {
		if reverse && !neg {
			return types.NewLong(int64(len(hay))), nil
		}
		return types.NewLong(int64(start)), nil
	}
	if !reverse {
		i := strings.Index(hay[start:], needle)
		if i < 0 {
			return types.NewBool(false), nil
		}
		return types.NewLong(int64(start + i)), nil
	}
	if neg {
		limit := min(start+1, len(hay))
		found := -1
		for i := 0; i < limit && i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				found = i
			}
		}
		if found < 0 {
			return types.NewBool(false), nil
		}
		return types.NewLong(int64(found)), nil
	}
	found := -1
	for i := start; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			found = i
		}
	}
	if found < 0 {
		return types.NewBool(false), nil
	}
	return types.NewLong(int64(found)), nil
}

func cpPos(hay, needle string, start int, neg bool, n int, reverse bool) (types.Value, error) {
	idx := cpIndex(hay)
	if needle == "" {
		if reverse && !neg {
			return types.NewLong(int64(n)), nil
		}
		return types.NewLong(int64(start)), nil
	}
	nidx := cpIndex(needle)
	ncp := len(nidx) - 1
	if !reverse {
		for i := start; i+ncp <= n; i++ {
			a, b := idx[i], idx[i+ncp]
			if hay[a:b] == needle {
				return types.NewLong(int64(i)), nil
			}
		}
		return types.NewBool(false), nil
	}
	limit := n
	from := start
	if neg {
		limit = start + 1
		from = 0
	}
	found := -1
	for i := from; i < limit && i+ncp <= n; i++ {
		a, b := idx[i], idx[i+ncp]
		if hay[a:b] == needle {
			found = i
		}
	}
	if found < 0 {
		return types.NewBool(false), nil
	}
	return types.NewLong(int64(found)), nil
}

func nativeMBStrtolower(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return mbCase(z, args, "mb_strtolower", false)
}

func nativeMBStrtoupper(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return mbCase(z, args, "mb_strtoupper", true)
}

func mbCase(z *zone.Zone, args []types.Value, name string, upper bool) (types.Value, error) {
	if err := arity(name, len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], name, "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	if _, err = encodingArg(z, args, 1, name); err != nil {
		return types.Value{}, err
	}
	return newStr(z, mapCase(s, upper))
}

func mapCase(s string, upper bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteByte(s[i])
			i++
			continue
		}
		if mapped, ok := specialCase(r, upper); ok {
			b.WriteString(mapped)
		} else if upper {
			b.WriteRune(unicode.ToUpper(r))
		} else {
			b.WriteRune(unicode.ToLower(r))
		}
		i += size
	}
	return b.String()
}

func specialCase(r rune, upper bool) (string, bool) {
	if upper {
		switch r {
		case 'ß':
			return "SS", true
		case 'ı':
			return "I", true
		case 'ﬁ':
			return "FI", true
		case 'ﬂ':
			return "FL", true
		case 'ﬀ':
			return "FF", true
		case 'ﬃ':
			return "FFI", true
		case 'ﬄ':
			return "FFL", true
		case 'ﬅ', 'ﬆ':
			return "ST", true
		}
		return "", false
	}
	if r == 'İ' {
		return "i\u0307", true
	}
	return "", false
}

func nativeMBCheckEncoding(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_check_encoding", len(args), 0, 2); err != nil {
		return types.Value{}, err
	}
	if len(args) == 0 || args[0].IsUndef() || deref(z, args[0]).IsNull() {
		return types.NewBool(true), nil
	}
	s, err := asString(z, args[0], "mb_check_encoding", "1", "value")
	if err != nil {
		return types.Value{}, err
	}
	kind := encUTF8
	if len(args) >= 2 && !args[1].IsUndef() && !deref(z, args[1]).IsNull() {
		kind, err = encodingArg(z, args, 1, "mb_check_encoding")
		if err != nil {
			return types.Value{}, err
		}
	}
	if kind == encBytes {
		return types.NewBool(true), nil
	}
	return types.NewBool(stdlib.C2_utf8_validate([]byte(s), uint64(len(s))) == 1), nil
}
