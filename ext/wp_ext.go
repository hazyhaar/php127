package ext

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"code.hazyhaar.fr/devhoros/php127/streams"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Jeux de caractères pris en charge par iconv et mb_convert_encoding.
type charset int

const (
	csUTF8 charset = iota
	csASCII
	csLatin1
	csCP1252
)

var charsetNames = [...]string{"UTF-8", "ASCII", "ISO-8859-1", "Windows-1252"}

const badRune rune = -1

func lookupCharset(name string) (charset, bool) {
	switch foldEnc(strings.TrimSpace(name)) {
	case "utf-8", "utf8":
		return csUTF8, true
	case "ascii", "us-ascii":
		return csASCII, true
	case "iso-8859-1", "iso8859-1", "latin1", "l1":
		return csLatin1, true
	case "windows-1252", "cp1252":
		return csCP1252, true
	default:
		return 0, false
	}
}

// cp1252High donne le point de code des octets 0x80 à 0x9F ; zéro marque un octet non défini.
var cp1252High = [32]rune{
	0x20AC, 0, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0, 0x017D, 0,
	0, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0, 0x017E, 0x0178,
}

// nextUTF8 décode un point de code ; une séquence invalide consomme sa plus longue sous-partie admissible.
func nextUTF8(s string, i int) (rune, int) {
	r, n := utf8.DecodeRuneInString(s[i:])
	if r != utf8.RuneError || n > 1 {
		return r, n
	}
	b0 := s[i]
	need := 0
	lo, hi := byte(0x80), byte(0xbf)
	switch {
	case b0 >= 0xc2 && b0 <= 0xdf:
		need = 1
	case b0 == 0xe0:
		need, lo = 2, 0xa0
	case b0 == 0xed:
		need, hi = 2, 0x9f
	case b0 >= 0xe1 && b0 <= 0xef:
		need = 2
	case b0 == 0xf0:
		need, lo = 3, 0x90
	case b0 == 0xf4:
		need, hi = 3, 0x8f
	case b0 >= 0xf1 && b0 <= 0xf3:
		need = 3
	default:
		return badRune, 1
	}
	j := i + 1
	for k := 0; k < need && j < len(s); k++ {
		c := s[j]
		if k == 0 && (c < lo || c > hi) || k > 0 && c&0xc0 != 0x80 {
			break
		}
		j++
	}
	return badRune, j - i
}

func decodeCharset(s string, cs charset) []rune {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch cs {
		case csUTF8:
			r, n := nextUTF8(s, i)
			out = append(out, r)
			i += n
			continue
		case csASCII:
			if c < 0x80 {
				out = append(out, rune(c))
			} else {
				out = append(out, badRune)
			}
		case csLatin1:
			out = append(out, rune(c))
		case csCP1252:
			if c >= 0x80 && c < 0xa0 {
				if r := cp1252High[c-0x80]; r != 0 {
					out = append(out, r)
				} else {
					out = append(out, badRune)
				}
			} else {
				out = append(out, rune(c))
			}
		}
		i++
	}
	return out
}

func badCount(runes []rune) int {
	n := 0
	for _, r := range runes {
		if r == badRune {
			n++
		}
	}
	return n
}

// encodeRune ajoute r encodé dans cs ; faux si cs ne représente pas r.
func encodeRune(b *strings.Builder, r rune, cs charset) bool {
	switch cs {
	case csUTF8:
		b.WriteRune(r)
		return true
	case csASCII:
		if r < 0x80 {
			b.WriteByte(byte(r))
			return true
		}
	case csLatin1:
		if r < 0x100 {
			b.WriteByte(byte(r))
			return true
		}
	case csCP1252:
		if r < 0x80 || r >= 0xa0 && r < 0x100 {
			b.WriteByte(byte(r))
			return true
		}
		for i, h := range cp1252High {
			if h != 0 && h == r {
				b.WriteByte(byte(0x80 + i))
				return true
			}
		}
	}
	return false
}

func encodeRunes(runes []rune, cs charset) string {
	var b strings.Builder
	b.Grow(len(runes))
	for _, r := range runes {
		encodeRune(&b, r, cs)
	}
	return b.String()
}

// ---- zlib ----

const (
	zlibEncodingRaw     = -15
	zlibEncodingGzip    = 31
	zlibEncodingDeflate = 15
	zlibEncodingAny     = 0
)

func nativeGzdeflate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return zlibEncodeNative(z, args, "gzdeflate", zlibEncodingRaw)
}

func nativeGzcompress(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return zlibEncodeNative(z, args, "gzcompress", zlibEncodingDeflate)
}

func nativeGzencode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return zlibEncodeNative(z, args, "gzencode", zlibEncodingGzip)
}

func zlibEncodeNative(z *zone.Zone, args []types.Value, name string, defEnc int64) (types.Value, error) {
	if err := arity(name, len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[0], name, "1", "data")
	if err != nil {
		return types.Value{}, err
	}
	level, err := optInt(z, args, 1, -1, name, "2", "level")
	if err != nil {
		return types.Value{}, err
	}
	enc, err := optInt(z, args, 2, defEnc, name, "3", "encoding")
	if err != nil {
		return types.Value{}, err
	}
	if level < -1 || level > 9 {
		return types.Value{}, zlibLevelErr(name, "2")
	}
	out, ok := zlibCompress(data, int(level), enc)
	if !ok {
		return types.Value{}, zlibEncodingErr(name, "3")
	}
	return newStr(z, out)
}

func nativeZlibEncode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("zlib_encode", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[0], "zlib_encode", "1", "data")
	if err != nil {
		return types.Value{}, err
	}
	enc, err := asInt(z, args[1], "zlib_encode", "2", "encoding")
	if err != nil {
		return types.Value{}, err
	}
	level, err := optInt(z, args, 2, -1, "zlib_encode", "3", "level")
	if err != nil {
		return types.Value{}, err
	}
	if enc != zlibEncodingRaw && enc != zlibEncodingGzip && enc != zlibEncodingDeflate {
		return types.Value{}, zlibEncodingErr("zlib_encode", "2")
	}
	if level < -1 || level > 9 {
		return types.Value{}, zlibLevelErr("zlib_encode", "3")
	}
	out, _ := zlibCompress(data, int(level), enc)
	return newStr(z, out)
}

func zlibLevelErr(name, idx string) error {
	return ValueError{name + "(): Argument #" + idx + " ($level) must be between -1 and 9"}
}

func zlibEncodingErr(name, idx string) error {
	return ValueError{name + "(): Argument #" + idx + " ($encoding) must be one of ZLIB_ENCODING_RAW, ZLIB_ENCODING_GZIP, or ZLIB_ENCODING_DEFLATE"}
}

func zlibCompress(data string, level int, enc int64) (string, bool) {
	var buf bytes.Buffer
	switch enc {
	case zlibEncodingRaw:
		writeDeflate(&buf, data, level)
	case zlibEncodingDeflate:
		w, err := zlib.NewWriterLevel(&buf, level)
		if err != nil {
			return "", false
		}
		_, _ = io.WriteString(w, data)
		_ = w.Close()
	case zlibEncodingGzip:
		// En-tête écrit à la main pour reproduire zlib : mtime nul, XFL selon le niveau, OS = 3.
		xfl := byte(0)
		switch level {
		case 9:
			xfl = 2
		case 0, 1:
			xfl = 4
		}
		buf.Write([]byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, xfl, 3})
		writeDeflate(&buf, data, level)
		var tail [8]byte
		binary.LittleEndian.PutUint32(tail[:4], crc32.ChecksumIEEE([]byte(data)))
		binary.LittleEndian.PutUint32(tail[4:], uint32(len(data)))
		buf.Write(tail[:])
	default:
		return "", false
	}
	return buf.String(), true
}

func writeDeflate(buf *bytes.Buffer, data string, level int) {
	w, err := flate.NewWriter(buf, level)
	if err != nil {
		return
	}
	_, _ = io.WriteString(w, data)
	_ = w.Close()
}

func nativeGzinflate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return zlibDecodeNative(z, args, "gzinflate", zlibEncodingRaw)
}

func nativeGzuncompress(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return zlibDecodeNative(z, args, "gzuncompress", zlibEncodingDeflate)
}

func nativeGzdecode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return zlibDecodeNative(z, args, "gzdecode", zlibEncodingGzip)
}

func nativeZlibDecode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return zlibDecodeNative(z, args, "zlib_decode", zlibEncodingAny)
}

func zlibDecodeNative(z *zone.Zone, args []types.Value, name string, enc int64) (types.Value, error) {
	if err := arity(name, len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[0], name, "1", "data")
	if err != nil {
		return types.Value{}, err
	}
	maxLen, err := optInt(z, args, 1, 0, name, "2", "max_length")
	if err != nil {
		return types.Value{}, err
	}
	if maxLen < 0 {
		return types.Value{}, ValueError{name + "(): Argument #2 ($max_length) must be greater than or equal to 0"}
	}
	out, ok := zlibDecompress(data, enc, maxLen)
	if !ok {
		return types.NewBool(false), nil
	}
	return newStr(z, out)
}

func zlibDecompress(data string, enc, maxLen int64) (string, bool) {
	if enc == zlibEncodingAny {
		switch {
		case len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b:
			enc = zlibEncodingGzip
		case len(data) >= 2 && data[0]&0x0f == 8 && (uint16(data[0])<<8|uint16(data[1]))%31 == 0:
			enc = zlibEncodingDeflate
		default:
			enc = zlibEncodingRaw
		}
	}
	src := strings.NewReader(data)
	var r io.Reader
	switch enc {
	case zlibEncodingRaw:
		r = flate.NewReader(src)
	case zlibEncodingDeflate:
		zr, err := zlib.NewReader(src)
		if err != nil {
			return "", false
		}
		r = zr
	default:
		gr, err := gzip.NewReader(src)
		if err != nil {
			return "", false
		}
		gr.Multistream(false)
		r = gr
	}
	if maxLen > 0 {
		r = io.LimitReader(r, maxLen+1)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return "", false
	}
	if maxLen > 0 && int64(len(out)) > maxLen {
		return "", false
	}
	return string(out), true
}

// ---- iconv ----

// splitIconvName sépare le nom de jeu de caractères de ses suffixes //IGNORE et //TRANSLIT.
func splitIconvName(name string) (base string, ignore, translit bool) {
	parts := strings.Split(name, "//")
	for _, p := range parts[1:] {
		switch strings.ToUpper(strings.TrimSpace(p)) {
		case "IGNORE":
			ignore = true
		case "TRANSLIT":
			translit = true
		}
	}
	return parts[0], ignore, translit
}

func nativeIconv(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("iconv", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	from, err := asString(z, args[0], "iconv", "1", "from_encoding")
	if err != nil {
		return types.Value{}, err
	}
	to, err := asString(z, args[1], "iconv", "2", "to_encoding")
	if err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[2], "iconv", "3", "string")
	if err != nil {
		return types.Value{}, err
	}
	fromName, _, _ := splitIconvName(from)
	toName, ignore, translit := splitIconvName(to)
	fromCs, ok1 := lookupCharset(fromName)
	toCs, ok2 := lookupCharset(toName)
	if !ok1 || !ok2 {
		return types.NewBool(false), nil
	}
	out, ok := iconvConvert(s, fromCs, toCs, ignore, translit)
	if !ok {
		return types.NewBool(false), nil
	}
	return newStr(z, out)
}

func iconvConvert(s string, from, to charset, ignore, translit bool) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range decodeCharset(s, from) {
		if r == badRune {
			if ignore {
				continue
			}
			return "", false
		}
		if encodeRune(&b, r, to) {
			continue
		}
		if translit {
			alt, ok := translitTable[r]
			if !ok {
				alt = "?"
			}
			b.WriteString(alt)
			continue
		}
		if ignore {
			continue
		}
		return "", false
	}
	return b.String(), true
}

// translitTable reproduit la translittération vers l'ASCII mesurée sur glibc par /usr/bin/php ; tout autre point de code devient « ? ».
var translitTable = map[rune]string{
	'À': "A", 'Á': "A", 'Â': "A", 'Ã': "A", 'Ä': "A", 'Å': "A", 'Æ': "AE", 'Ç': "C",
	'È': "E", 'É': "E", 'Ê': "E", 'Ë': "E", 'Ì': "I", 'Í': "I", 'Î': "I", 'Ï': "I",
	'Ð': "D", 'Ñ': "N", 'Ò': "O", 'Ó': "O", 'Ô': "O", 'Õ': "O", 'Ö': "O", '×': "x",
	'Ø': "O", 'Ù': "U", 'Ú': "U", 'Û': "U", 'Ü': "U", 'Ý': "Y", 'Þ': "TH", 'ß': "ss",
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'æ': "ae", 'ç': "c",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ì': "i", 'í': "i", 'î': "i", 'ï': "i",
	'ð': "d", 'ñ': "n", 'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", '÷': "/",
	'ø': "o", 'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ý': "y", 'þ': "th", 'ÿ': "y",
	'€': "EUR", '‘': "'", '’': "'", '‚': ",", '“': "\"", '”': "\"", '„': ",,",
	'–': "-", '—': "--", '…': "...", '•': "o", '™': "(TM)", '©': "(C)", '®': "(R)",
	'«': "<<", '»': ">>", '¡': "!", '¿': "?", '¢': "c", '£': "GBP", '¥': "JPY",
	'±': "+-", '²': "2", '³': "3", 'µ': "u", '·': ".", '¹': "1", 'º': "o",
	'¼': " 1/4 ", '½': " 1/2 ", '¾': " 3/4 ", 'Œ': "OE", 'œ': "oe", 'Š': "S", 'š': "s",
	'Ž': "Z", 'ž': "z", 'Ÿ': "Y", 'ƒ': "f", '†': "+", '‹': "<", '›': ">", 'ˆ': "^",
	'˜': "~", ' ': " ", '¦': "|", 'ª': "a", '¬': "!", '­': "-", '´': "'",
	'¸': ",", ' ': " ",
}

// iconvRunes décode s selon l'encodage optionnel en position i ; faux si l'encodage est inconnu ou l'entrée invalide.
func iconvRunes(z *zone.Zone, args []types.Value, i int, s, name string) ([]rune, charset, bool, error) {
	cs := csUTF8
	if len(args) > i && !missing(deref(z, args[i])) {
		enc, err := asString(z, args[i], name, idxOf(i), "encoding")
		if err != nil {
			return nil, 0, false, err
		}
		var ok bool
		if cs, ok = lookupCharset(enc); !ok {
			return nil, 0, false, nil
		}
	}
	runes := decodeCharset(s, cs)
	if badCount(runes) > 0 {
		return nil, 0, false, nil
	}
	return runes, cs, true, nil
}

func nativeIconvStrlen(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("iconv_strlen", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "iconv_strlen", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	runes, _, ok, err := iconvRunes(z, args, 1, s, "iconv_strlen")
	if err != nil || !ok {
		return types.NewBool(false), err
	}
	return types.NewLong(int64(len(runes))), nil
}

func nativeIconvSubstr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("iconv_substr", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "iconv_substr", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	offset, err := asInt(z, args[1], "iconv_substr", "2", "offset")
	if err != nil {
		return types.Value{}, err
	}
	runes, cs, ok, err := iconvRunes(z, args, 3, s, "iconv_substr")
	if err != nil || !ok {
		return types.NewBool(false), err
	}
	total := int64(len(runes))
	length, err := optInt(z, args, 2, total, "iconv_substr", "3", "length")
	if err != nil {
		return types.Value{}, err
	}
	if offset < 0 {
		offset = max(offset+total, 0)
	} else if offset > total {
		offset = total
	}
	if length < 0 {
		length = max(length+total-offset, 0)
	} else if length > total {
		length = total
	}
	if offset+length > total {
		length = total - offset
	}
	return newStr(z, encodeRunes(runes[offset:offset+length], cs))
}

func runesIndex(hay, needle []rune, from int) int {
	for i := from; i+len(needle) <= len(hay); i++ {
		if runesEqual(hay[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func nativeIconvStrpos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("iconv_strpos", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	hay, err := asString(z, args[0], "iconv_strpos", "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asString(z, args[1], "iconv_strpos", "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	offset, err := optInt(z, args, 2, 0, "iconv_strpos", "3", "offset")
	if err != nil {
		return types.Value{}, err
	}
	h, cs, ok, err := iconvRunes(z, args, 3, hay, "iconv_strpos")
	if err != nil || !ok {
		return types.NewBool(false), err
	}
	outOfBounds := ValueError{"iconv_strpos(): Argument #3 ($offset) must be contained in argument #1 ($haystack)"}
	if offset < 0 {
		if offset+int64(len(h)) < 0 {
			return types.Value{}, outOfBounds
		}
		offset += int64(len(h))
	}
	if needle == "" {
		return types.NewBool(false), nil
	}
	if offset > int64(len(h)) {
		return types.Value{}, outOfBounds
	}
	n := decodeCharset(needle, cs)
	if badCount(n) > 0 {
		return types.NewBool(false), nil
	}
	if i := runesIndex(h, n, int(offset)); i >= 0 {
		return types.NewLong(int64(i)), nil
	}
	return types.NewBool(false), nil
}

func nativeIconvStrrpos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("iconv_strrpos", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	hay, err := asString(z, args[0], "iconv_strrpos", "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asString(z, args[1], "iconv_strrpos", "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	if needle == "" {
		return types.NewBool(false), nil
	}
	h, cs, ok, err := iconvRunes(z, args, 2, hay, "iconv_strrpos")
	if err != nil || !ok {
		return types.NewBool(false), err
	}
	n := decodeCharset(needle, cs)
	if badCount(n) > 0 {
		return types.NewBool(false), nil
	}
	for i := len(h) - len(n); i >= 0; i-- {
		if runesEqual(h[i:i+len(n)], n) {
			return types.NewLong(int64(i)), nil
		}
	}
	return types.NewBool(false), nil
}

var iconvEncodingTypes = [...]string{"input_encoding", "output_encoding", "internal_encoding"}

// iconvEncodingField désigne le champ de extState associé à un type de iconv_get_encoding / iconv_set_encoding.
func iconvEncodingField(st *extState, kind string) *string {
	switch {
	case strings.EqualFold(kind, "input_encoding"):
		return &st.iconvInputEncoding
	case strings.EqualFold(kind, "output_encoding"):
		return &st.iconvOutputEncoding
	case strings.EqualFold(kind, "internal_encoding"):
		return &st.iconvInternalEncoding
	}
	return nil
}

// nativeIconvGetEncoding rend la configuration iconv de la Zone ; une valeur vide se lit "UTF-8" (default_charset).
func nativeIconvGetEncoding(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("iconv_get_encoding", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	kind := "all"
	if len(args) == 1 {
		var err error
		if kind, err = asString(z, args[0], "iconv_get_encoding", "1", "type"); err != nil {
			return types.Value{}, err
		}
	}
	read := func(k string) string {
		v := readExtState(z, func(st *extState) string { return *iconvEncodingField(st, k) })
		if v == "" {
			return "UTF-8"
		}
		return v
	}
	if strings.EqualFold(kind, "all") {
		a, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		for _, k := range iconvEncodingTypes {
			v, err := newStr(z, read(k))
			if err != nil {
				return types.Value{}, err
			}
			if err = setStrKey(z, &a, k, v); err != nil {
				return types.Value{}, err
			}
		}
		return a, nil
	}
	for _, k := range iconvEncodingTypes {
		if strings.EqualFold(kind, k) {
			return newStr(z, read(k))
		}
	}
	return types.NewBool(false), nil
}

// nativeIconvSetEncoding conserve la valeur, copiée hors de l'arène, dans l'état de la Zone.
func nativeIconvSetEncoding(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("iconv_set_encoding", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	kind, err := asString(z, args[0], "iconv_set_encoding", "1", "type")
	if err != nil {
		return types.Value{}, err
	}
	enc, err := asString(z, args[1], "iconv_set_encoding", "2", "encoding")
	if err != nil {
		return types.Value{}, err
	}
	for _, k := range iconvEncodingTypes {
		if strings.EqualFold(kind, k) {
			enc = strings.Clone(enc)
			writeExtState(z, func(st *extState) { *iconvEncodingField(st, k) = enc })
			return types.NewBool(true), nil
		}
	}
	return types.NewBool(false), nil
}

const iconvMimeContinueOnError = 2

func nativeIconvMimeDecode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("iconv_mime_decode", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "iconv_mime_decode", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	mode, err := optInt(z, args, 1, 0, "iconv_mime_decode", "2", "mode")
	if err != nil {
		return types.Value{}, err
	}
	target := csUTF8
	if len(args) > 2 && !missing(deref(z, args[2])) {
		enc, err := asString(z, args[2], "iconv_mime_decode", "3", "encoding")
		if err != nil {
			return types.Value{}, err
		}
		var ok bool
		if target, ok = lookupCharset(enc); !ok {
			return types.NewBool(false), nil
		}
	}
	out, ok := mimeDecode(s, target, mode&iconvMimeContinueOnError != 0)
	if !ok {
		return types.NewBool(false), nil
	}
	return newStr(z, out)
}

// mimeDecode décode les mots encodés RFC 2047 ; l'espace entre deux mots encodés adjacents disparaît.
func mimeDecode(s string, target charset, keepGoing bool) (string, bool) {
	var b strings.Builder
	pendingWS := ""
	lastWord := false
	for i := 0; i < len(s); {
		c := s[i]
		if c == '\r' || c == '\n' {
			j := i
			if c == '\r' && j+1 < len(s) && s[j+1] == '\n' {
				j++
			}
			if j+1 < len(s) && (s[j+1] == ' ' || s[j+1] == '\t') {
				i = j + 1
				continue
			}
		}
		if c == ' ' || c == '\t' {
			pendingWS += string(c)
			i++
			continue
		}
		if strings.HasPrefix(s[i:], "=?") {
			if text, n, cs, ok := parseEncodedWord(s[i:]); n > 0 {
				if !ok {
					if !keepGoing {
						return "", false
					}
					b.WriteString(pendingWS)
					b.WriteString(s[i : i+n])
					pendingWS, lastWord = "", false
					i += n
					continue
				}
				if !lastWord {
					b.WriteString(pendingWS)
				}
				conv, ok := iconvConvert(text, cs, target, false, false)
				if !ok && !keepGoing {
					return "", false
				}
				b.WriteString(conv)
				pendingWS, lastWord = "", true
				i += n
				continue
			}
		}
		b.WriteString(pendingWS)
		b.WriteByte(c)
		pendingWS, lastWord = "", false
		i++
	}
	b.WriteString(pendingWS)
	return b.String(), true
}

// parseEncodedWord lit « =?charset?B|Q?texte?= » ; n vaut zéro si la forme n'est pas reconnue, ok faux si le jeu de caractères ou le contenu est invalide.
func parseEncodedWord(s string) (text string, n int, cs charset, ok bool) {
	rest := s[2:]
	q1 := strings.IndexByte(rest, '?')
	if q1 <= 0 || q1+2 >= len(rest) || rest[q1+2] != '?' {
		return "", 0, 0, false
	}
	end := strings.Index(rest[q1+3:], "?=")
	if end < 0 {
		return "", 0, 0, false
	}
	name := rest[:q1]
	if star := strings.IndexByte(name, '*'); star >= 0 {
		name = name[:star]
	}
	payload := rest[q1+3 : q1+3+end]
	n = 2 + q1 + 3 + end + 2
	cs, ok = lookupCharset(name)
	if !ok {
		return "", n, 0, false
	}
	switch rest[q1+1] {
	case 'B', 'b':
		dec, valid := base64Decode(payload, true)
		if !valid {
			return "", n, 0, false
		}
		return dec, n, cs, true
	case 'Q', 'q':
		var b strings.Builder
		for i := 0; i < len(payload); i++ {
			switch {
			case payload[i] == '_':
				b.WriteByte(' ')
			case payload[i] == '=' && i+2 < len(payload) && isHex(payload[i+1]) && isHex(payload[i+2]):
				b.WriteByte(unhex(payload[i+1])<<4 | unhex(payload[i+2]))
				i += 2
			default:
				b.WriteByte(payload[i])
			}
		}
		return b.String(), n, cs, true
	default:
		return "", n, 0, false
	}
}

// ---- preg ----

// Codes de preg_last_error(), valeurs des constantes PREG_*_ERROR de PHP 8.3.
const (
	pregNoError             int64 = 0
	pregInternalError       int64 = 1
	pregBacktrackLimitError int64 = 2
	pregRecursionLimitError int64 = 3
	pregBadUTF8Error        int64 = 4
	pregBadUTF8OffsetError  int64 = 5
	pregJITStackLimitError  int64 = 6
)

// pregErrorMessages suit pcre_get_error_msg de ext/pcre (PHP 8.3).
var pregErrorMessages = [...]string{
	pregNoError:             "No error",
	pregInternalError:       "Internal error",
	pregBacktrackLimitError: "Backtrack limit exhausted",
	pregRecursionLimitError: "Recursion limit exhausted",
	pregBadUTF8Error:        "Malformed UTF-8 characters, possibly incorrectly encoded",
	pregBadUTF8OffsetError:  "The offset did not correspond to the beginning of a valid UTF-8 code point",
	pregJITStackLimitError:  "JIT stack limit exhausted",
}

// setPregError consigne le code du dernier appel preg_* de la Zone ; chaque appel qui exécute une expression le pose.
func setPregError(z *zone.Zone, code int64) {
	writeExtState(z, func(st *extState) { st.pregError = code })
}

func nativePregLastError(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("preg_last_error", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewLong(readExtState(z, func(st *extState) int64 { return st.pregError })), nil
}

func nativePregLastErrorMsg(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("preg_last_error_msg", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	code := readExtState(z, func(st *extState) int64 { return st.pregError })
	if code < 0 || code >= int64(len(pregErrorMessages)) {
		return newStr(z, "Unknown error")
	}
	return newStr(z, pregErrorMessages[code])
}

// ---- hachage ----

func cryptoAlgo(z *zone.Zone, v types.Value, name string) (hashAlgo, error) {
	algo, err := asString(z, v, name, "1", "algo")
	if err != nil {
		return hashAlgo{}, err
	}
	a, ok := findAlgo(algo)
	if !ok || !a.hmac {
		return hashAlgo{}, ValueError{name + "(): Argument #1 ($algo) must be a valid cryptographic hashing algorithm"}
	}
	return a, nil
}

func nativeHashHKDF(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash_hkdf", len(args), 2, 5); err != nil {
		return types.Value{}, err
	}
	a, err := cryptoAlgo(z, args[0], "hash_hkdf")
	if err != nil {
		return types.Value{}, err
	}
	key, err := asString(z, args[1], "hash_hkdf", "2", "key")
	if err != nil {
		return types.Value{}, err
	}
	length, err := optInt(z, args, 2, 0, "hash_hkdf", "3", "length")
	if err != nil {
		return types.Value{}, err
	}
	info, err := optString(z, args, 3, "hash_hkdf", "4", "info")
	if err != nil {
		return types.Value{}, err
	}
	salt, err := optString(z, args, 4, "hash_hkdf", "5", "salt")
	if err != nil {
		return types.Value{}, err
	}
	size := int64(a.new().Size())
	switch {
	case key == "":
		return types.Value{}, ValueError{"hash_hkdf(): Argument #2 ($key) cannot be empty"}
	case length < 0:
		return types.Value{}, ValueError{"hash_hkdf(): Argument #3 ($length) must be greater than or equal to 0"}
	case length > 255*size:
		return types.Value{}, ValueError{"hash_hkdf(): Argument #3 ($length) must be less than or equal to " + strconv.FormatInt(255*size, 10)}
	}
	if length == 0 {
		length = size
	}
	out, err := hkdf.Key(a.new, []byte(key), []byte(salt), info, int(length))
	if err != nil {
		return types.NewBool(false), nil
	}
	return newStr(z, string(out))
}

func optString(z *zone.Zone, args []types.Value, i int, fn, idx, param string) (string, error) {
	if len(args) <= i || args[i].IsUndef() {
		return "", nil
	}
	return asString(z, args[i], fn, idx, param)
}

func nativeHashPBKDF2(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash_pbkdf2", len(args), 4, 7); err != nil {
		return types.Value{}, err
	}
	a, err := cryptoAlgo(z, args[0], "hash_pbkdf2")
	if err != nil {
		return types.Value{}, err
	}
	password, err := asString(z, args[1], "hash_pbkdf2", "2", "password")
	if err != nil {
		return types.Value{}, err
	}
	salt, err := asString(z, args[2], "hash_pbkdf2", "3", "salt")
	if err != nil {
		return types.Value{}, err
	}
	iter, err := asInt(z, args[3], "hash_pbkdf2", "4", "iterations")
	if err != nil {
		return types.Value{}, err
	}
	length, err := optInt(z, args, 4, 0, "hash_pbkdf2", "5", "length")
	if err != nil {
		return types.Value{}, err
	}
	binaryOut := optBool(z, args, 5)
	if iter <= 0 {
		return types.Value{}, ValueError{"hash_pbkdf2(): Argument #4 ($iterations) must be greater than 0"}
	}
	if length < 0 {
		return types.Value{}, ValueError{"hash_pbkdf2(): Argument #5 ($length) must be greater than or equal to 0"}
	}
	size := int64(a.new().Size())
	if length == 0 {
		length = size
		if !binaryOut {
			length *= 2
		}
	}
	keyLen := length
	if !binaryOut {
		keyLen = (length + 1) / 2
	}
	out, err := pbkdf2.Key(a.new, password, []byte(salt), int(iter), int(keyLen))
	if err != nil {
		return types.NewBool(false), nil
	}
	if binaryOut {
		return newStr(z, string(out))
	}
	return newStr(z, hex.EncodeToString(out)[:length])
}

// readFileArg lit le fichier par file_get_contents du paquet streams, qui porte les enveloppes de flux.
func readFileArg(z *zone.Zone, w io.Writer, v types.Value) (string, bool, error) {
	fgc, ok := streams.Lookup("file_get_contents")
	if !ok {
		return "", false, nil
	}
	got, err := fgc(z, w, []types.Value{v})
	if err != nil {
		return "", false, err
	}
	got = deref(z, got)
	if got.Type != types.TypeString {
		return "", false, nil
	}
	data := types.StringData(z, got)
	types.ReleaseValue(z, &got)
	return data, true, nil
}

func nativeMD5File(z *zone.Zone, w io.Writer, args []types.Value) (types.Value, error) {
	return fileDigest(z, w, args, "md5_file", md5Algo())
}

func nativeSHA1File(z *zone.Zone, w io.Writer, args []types.Value) (types.Value, error) {
	return fileDigest(z, w, args, "sha1_file", sha1Algo())
}

func fileDigest(z *zone.Zone, w io.Writer, args []types.Value, name string, a hashAlgo) (types.Value, error) {
	if err := arity(name, len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	if _, err := asString(z, args[0], name, "1", "filename"); err != nil {
		return types.Value{}, err
	}
	data, ok, err := readFileArg(z, w, args[0])
	if err != nil || !ok {
		return types.NewBool(false), err
	}
	return digest(a.new(), data, optBool(z, args, 1), z)
}

func nativeHashFile(z *zone.Zone, w io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash_file", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	algo, err := asString(z, args[0], "hash_file", "1", "algo")
	if err != nil {
		return types.Value{}, err
	}
	a, ok := findAlgo(algo)
	if !ok {
		return types.Value{}, ValueError{"hash_file(): Argument #1 ($algo) must be a valid hashing algorithm"}
	}
	if _, err = asString(z, args[1], "hash_file", "2", "filename"); err != nil {
		return types.Value{}, err
	}
	data, ok, err := readFileArg(z, w, args[1])
	if err != nil || !ok {
		return types.NewBool(false), err
	}
	return digest(a.new(), data, optBool(z, args, 2), z)
}

func nativeHashHMACFile(z *zone.Zone, w io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash_hmac_file", len(args), 3, 4); err != nil {
		return types.Value{}, err
	}
	a, err := cryptoAlgo(z, args[0], "hash_hmac_file")
	if err != nil {
		return types.Value{}, err
	}
	if _, err = asString(z, args[1], "hash_hmac_file", "2", "filename"); err != nil {
		return types.Value{}, err
	}
	key, err := asString(z, args[2], "hash_hmac_file", "3", "key")
	if err != nil {
		return types.Value{}, err
	}
	data, ok, err := readFileArg(z, w, args[1])
	if err != nil || !ok {
		return types.NewBool(false), err
	}
	return digest(hmac.New(a.new, []byte(key)), data, optBool(z, args, 3), z)
}

// ---- openssl ----

const (
	opensslRawData        = 1
	opensslZeroPadding    = 2
	opensslDontZeroPadKey = 4
)

// Messages de la file d'erreurs d'OpenSSL 3 que PHP 8.3 relaie par openssl_error_string() après un échec
// de openssl_encrypt ou openssl_decrypt ; un chiffre inconnu, un base64 refusé ou une étiquette GCM fausse
// n'en déposent aucun.
const (
	sslErrBadDecrypt     = "error:1C800064:Provider routines::bad decrypt"
	sslErrWrongFinal     = "error:1C80006B:Provider routines::wrong final block length"
	sslErrInvalidKeyLen  = "error:03000082:digital envelope routines::invalid key length"
	sslErrInvalidIVLen   = "error:1C80006D:Provider routines::invalid iv length"
	sslErrInvalidTag     = "error:1C80006E:Provider routines::invalid tag"
	opensslErrorQueueMax = 16
)

// pushOpensslError empile un message dans la file de la Zone ; comme la file d'OpenSSL (ERR_NUM_ERRORS),
// elle ne garde que les plus récents.
func pushOpensslError(z *zone.Zone, msg string) {
	if msg == "" {
		return
	}
	writeExtState(z, func(st *extState) {
		if len(st.opensslErrors) == opensslErrorQueueMax {
			st.opensslErrors = slices.Delete(st.opensslErrors, 0, 1)
		}
		st.opensslErrors = append(st.opensslErrors, msg)
	})
}

// nativeOpensslErrorString dépile le message le plus ancien, ou rend false quand la file est vide.
func nativeOpensslErrorString(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("openssl_error_string", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	var msg string
	writeExtState(z, func(st *extState) {
		if len(st.opensslErrors) > 0 {
			msg = st.opensslErrors[0]
			st.opensslErrors = slices.Delete(st.opensslErrors, 0, 1)
		}
	})
	if msg == "" {
		return types.NewBool(false), nil
	}
	return newStr(z, msg)
}

type cipherSpec struct {
	keyLen int
	ivLen  int
	mode   string
}

func lookupCipher(name string) (cipherSpec, bool) {
	n := strings.ToLower(name)
	var key int
	switch {
	case strings.HasPrefix(n, "aes-128-"):
		key = 16
	case strings.HasPrefix(n, "aes-192-"):
		key = 24
	case strings.HasPrefix(n, "aes-256-"):
		key = 32
	default:
		return cipherSpec{}, false
	}
	switch mode := n[8:]; mode {
	case "cbc", "ctr":
		return cipherSpec{key, 16, mode}, true
	case "ecb":
		return cipherSpec{key, 0, mode}, true
	case "gcm":
		return cipherSpec{key, 12, mode}, true
	default:
		return cipherSpec{}, false
	}
}

func nativeOpensslRandomPseudoBytes(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("openssl_random_pseudo_bytes", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	n, err := asInt(z, args[0], "openssl_random_pseudo_bytes", "1", "length")
	if err != nil {
		return types.Value{}, err
	}
	if n < 1 {
		return types.Value{}, ValueError{"openssl_random_pseudo_bytes(): Argument #1 ($length) must be greater than 0"}
	}
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	if len(args) == 2 {
		writeRef(z, args[1], types.NewBool(true))
	}
	return newStr(z, string(buf))
}

func nativeOpensslCipherIVLength(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("openssl_cipher_iv_length", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	name, err := asString(z, args[0], "openssl_cipher_iv_length", "1", "cipher_algo")
	if err != nil {
		return types.Value{}, err
	}
	if name == "" {
		return types.Value{}, ValueError{"openssl_cipher_iv_length(): Argument #1 ($cipher_algo) cannot be empty"}
	}
	spec, ok := lookupCipher(name)
	if !ok {
		return types.NewBool(false), nil
	}
	return types.NewLong(int64(spec.ivLen)), nil
}

func nativeOpensslDecrypt(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("openssl_decrypt", len(args), 3, 7); err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[0], "openssl_decrypt", "1", "data")
	if err != nil {
		return types.Value{}, err
	}
	algo, err := asString(z, args[1], "openssl_decrypt", "2", "cipher_algo")
	if err != nil {
		return types.Value{}, err
	}
	pass, err := asString(z, args[2], "openssl_decrypt", "3", "passphrase")
	if err != nil {
		return types.Value{}, err
	}
	options, err := optInt(z, args, 3, 0, "openssl_decrypt", "4", "options")
	if err != nil {
		return types.Value{}, err
	}
	iv, err := optString(z, args, 4, "openssl_decrypt", "5", "iv")
	if err != nil {
		return types.Value{}, err
	}
	tag, err := optString(z, args, 5, "openssl_decrypt", "6", "tag")
	if err != nil {
		return types.Value{}, err
	}
	aad, err := optString(z, args, 6, "openssl_decrypt", "7", "aad")
	if err != nil {
		return types.Value{}, err
	}
	spec, ok := lookupCipher(algo)
	if !ok {
		return types.NewBool(false), nil
	}
	if options&opensslRawData == 0 {
		dec, ok := base64Decode(data, false)
		if !ok {
			return types.NewBool(false), nil
		}
		data = dec
	}
	if len(pass) < spec.keyLen && options&opensslDontZeroPadKey != 0 {
		pushOpensslError(z, sslErrInvalidKeyLen)
		return types.NewBool(false), nil
	}
	key := make([]byte, spec.keyLen)
	copy(key, pass)
	out, sslErr, ok := aesDecrypt(spec, key, []byte(data), iv, tag, aad, options&opensslZeroPadding != 0)
	if !ok {
		pushOpensslError(z, sslErr)
		return types.NewBool(false), nil
	}
	return newStr(z, string(out))
}

// nativeOpensslEncrypt chiffre avec la même table de chiffres (lookupCipher), la même préparation de clé
// (bourrage par zéros, refus sous OPENSSL_DONT_ZERO_PAD_KEY) et le même IV que nativeOpensslDecrypt.
// En GCM, l'étiquette de tag_length octets (1 à 16, préfixe de l'étiquette complète) est écrite dans $tag.
func nativeOpensslEncrypt(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("openssl_encrypt", len(args), 3, 8); err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[0], "openssl_encrypt", "1", "data")
	if err != nil {
		return types.Value{}, err
	}
	algo, err := asString(z, args[1], "openssl_encrypt", "2", "cipher_algo")
	if err != nil {
		return types.Value{}, err
	}
	pass, err := asString(z, args[2], "openssl_encrypt", "3", "passphrase")
	if err != nil {
		return types.Value{}, err
	}
	options, err := optInt(z, args, 3, 0, "openssl_encrypt", "4", "options")
	if err != nil {
		return types.Value{}, err
	}
	iv, err := optString(z, args, 4, "openssl_encrypt", "5", "iv")
	if err != nil {
		return types.Value{}, err
	}
	aad, err := optString(z, args, 6, "openssl_encrypt", "7", "aad")
	if err != nil {
		return types.Value{}, err
	}
	tagLen, err := optInt(z, args, 7, 16, "openssl_encrypt", "8", "tag_length")
	if err != nil {
		return types.Value{}, err
	}
	tagSlot := arg(args, 5)
	spec, ok := lookupCipher(algo)
	if !ok {
		return types.NewBool(false), nil
	}
	if len(pass) < spec.keyLen && options&opensslDontZeroPadKey != 0 {
		pushOpensslError(z, sslErrInvalidKeyLen)
		return types.NewBool(false), nil
	}
	key := make([]byte, spec.keyLen)
	copy(key, pass)
	var out []byte
	if spec.mode == "gcm" {
		switch {
		case iv == "":
			pushOpensslError(z, sslErrInvalidIVLen)
			return types.NewBool(false), nil
		case tagLen < 1 || tagLen > 16:
			pushOpensslError(z, sslErrInvalidTag)
			return types.NewBool(false), nil
		case tagSlot.Type != types.TypeReference:
			return types.NewBool(false), nil
		}
		ct, tag, ok := gcmSeal(key, []byte(data), iv, aad)
		if !ok {
			return types.NewBool(false), nil
		}
		tv, err := newStr(z, string(tag[:tagLen]))
		if err != nil {
			return types.Value{}, err
		}
		writeRef(z, tagSlot, tv)
		types.ReleaseValue(z, &tv)
		out = ct
	} else {
		writeRef(z, tagSlot, types.NewNull())
		out, ok = aesEncrypt(spec, key, []byte(data), iv, options&opensslZeroPadding != 0)
		if !ok {
			pushOpensslError(z, sslErrWrongFinal)
			return types.NewBool(false), nil
		}
	}
	if options&opensslRawData == 0 {
		return newStr(z, base64.StdEncoding.EncodeToString(out))
	}
	return newStr(z, string(out))
}

// aesEncrypt est l'inverse exact de aesDecrypt pour les modes CBC, CTR et ECB : IV bourré de zéros
// ou tronqué à spec.ivLen, remplissage PKCS#7 sauf sous OPENSSL_ZERO_PADDING.
func aesEncrypt(spec cipherSpec, key, data []byte, iv string, noPad bool) ([]byte, bool) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, false
	}
	ivBuf := make([]byte, spec.ivLen)
	copy(ivBuf, iv)
	if spec.mode == "ctr" {
		out := make([]byte, len(data))
		cipher.NewCTR(block, ivBuf).XORKeyStream(out, data)
		return out, true
	}
	in := data
	if noPad {
		if len(data)%aes.BlockSize != 0 {
			return nil, false
		}
	} else {
		p := aes.BlockSize - len(data)%aes.BlockSize
		in = make([]byte, len(data)+p)
		copy(in, data)
		for i := len(data); i < len(in); i++ {
			in[i] = byte(p)
		}
	}
	out := make([]byte, len(in))
	if spec.mode == "cbc" {
		cipher.NewCBCEncrypter(block, ivBuf).CryptBlocks(out, in)
		return out, true
	}
	for i := 0; i < len(in); i += aes.BlockSize {
		block.Encrypt(out[i:i+aes.BlockSize], in[i:i+aes.BlockSize])
	}
	return out, true
}

// gcmSeal chiffre en GCM avec un nonce de longueur quelconque non nulle et rend l'étiquette complète de 16 octets.
func gcmSeal(key, data []byte, iv, aad string) (ct, tag []byte, ok bool) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, false
	}
	var aead cipher.AEAD
	if len(iv) == 12 {
		aead, err = cipher.NewGCM(block)
	} else {
		aead, err = cipher.NewGCMWithNonceSize(block, len(iv))
	}
	if err != nil {
		return nil, nil, false
	}
	sealed := aead.Seal(nil, []byte(iv), data, []byte(aad))
	n := len(sealed) - aead.Overhead()
	return sealed[:n], sealed[n:], true
}

// aesDecrypt rend, en cas d'échec, le message que la file d'erreurs d'OpenSSL porterait ("" s'il n'y en a pas).
func aesDecrypt(spec cipherSpec, key, data []byte, iv, tag, aad string, noPad bool) ([]byte, string, bool) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, "", false
	}
	if spec.mode == "gcm" {
		out, ok := gcmOpen(block, data, iv, tag, aad)
		return out, "", ok
	}
	ivBuf := make([]byte, spec.ivLen)
	copy(ivBuf, iv)
	out := make([]byte, len(data))
	switch spec.mode {
	case "ctr":
		cipher.NewCTR(block, ivBuf).XORKeyStream(out, data)
		return out, "", true
	case "cbc":
		if len(data) == 0 || len(data)%aes.BlockSize != 0 {
			return nil, sslErrWrongFinal, false
		}
		cipher.NewCBCDecrypter(block, ivBuf).CryptBlocks(out, data)
	default:
		if len(data) == 0 || len(data)%aes.BlockSize != 0 {
			return nil, sslErrWrongFinal, false
		}
		for i := 0; i < len(data); i += aes.BlockSize {
			block.Decrypt(out[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
		}
	}
	if noPad {
		return out, "", true
	}
	p := int(out[len(out)-1])
	if p == 0 || p > aes.BlockSize {
		return nil, sslErrBadDecrypt, false
	}
	for _, c := range out[len(out)-p:] {
		if int(c) != p {
			return nil, sslErrBadDecrypt, false
		}
	}
	return out[:len(out)-p], "", true
}

// gcmOpen déchiffre en GCM avec une étiquette de 1 à 16 octets, préfixe de l'étiquette complète, comme
// OpenSSL : le flux CTR de GCM étant symétrique, un premier Seal rend le clair, un second l'étiquette
// complète dont le préfixe se compare en temps constant. cipher.NewGCMWithTagSize refuse les étiquettes
// de moins de 12 octets que openssl_encrypt produit pour tag_length < 12.
func gcmOpen(block cipher.Block, data []byte, iv, tag, aad string) ([]byte, bool) {
	if tag == "" || len(tag) > 16 {
		return nil, false
	}
	nonce := []byte(iv)
	if len(nonce) == 0 {
		nonce = make([]byte, 12)
	}
	var aead cipher.AEAD
	var err error
	if len(nonce) == 12 {
		aead, err = cipher.NewGCM(block)
	} else {
		aead, err = cipher.NewGCMWithNonceSize(block, len(nonce))
	}
	if err != nil {
		return nil, false
	}
	plain := aead.Seal(nil, nonce, data, []byte(aad))[:len(data)]
	resealed := aead.Seal(nil, nonce, plain, []byte(aad))
	full := resealed[len(plain):]
	if subtle.ConstantTimeCompare(full[:len(tag)], []byte(tag)) != 1 {
		return nil, false
	}
	return plain, true
}

// ---- état de requête par Zone ----

// CleanupExt est la valeur de zone.CleanupItem.Kind qui libère l'état de configuration d'ext pour une Zone.
const CleanupExt zone.CleanupKind = 59

// extState porte l'état de requête d'ext pour une Zone : configuration posée par le script
// (mb_internal_encoding, iconv_set_encoding, mb_substitute_character, ...), dernière erreur preg, file
// d'erreurs OpenSSL, contextes hash_init et clés openssl_pkey_get_private. Toutes les chaînes sont des
// copies Go ; aucune ne référence l'arène de la Zone. L'état ne tient ni descripteur ni socket : Zone.Reset
// le libère en effaçant ses slots, sans nettoyage enregistré ni répartiteur.
type extState struct {
	mbInternalEncoding    string
	mbRegexEncoding       string
	mbLanguage            string
	mbDetectOrder         []string
	iconvInternalEncoding string
	iconvInputEncoding    string
	iconvOutputEncoding   string
	pregError             int64
	opensslErrors         []string
	mbSubstMode           mbSubstMode
	mbSubstChar           rune
	nextHandle            uint32
	hashCtxs              map[uint32]*hashCtx
	pkeys                 map[uint32]crypto.Signer
	handles               map[uint32]any
}

// extStateFor retourne l'état de la Zone, créé aux valeurs par défaut de PHP dans z.Slot(zone.SlotExt).
// Une Zone n'étant jamais partagée entre goroutines, aucun verrou n'est pris.
func extStateFor(z *zone.Zone) *extState {
	if st, ok := z.Slot(zone.SlotExt).(*extState); ok {
		return st
	}
	st := &extState{
		mbInternalEncoding: "UTF-8",
		mbRegexEncoding:    "UTF-8",
		mbLanguage:         "neutral",
		mbDetectOrder:      []string{charsetNames[csASCII], charsetNames[csUTF8]},
		mbSubstChar:        '?',
	}
	z.SetSlot(zone.SlotExt, st)
	return st
}

// DispatchCleanup efface l'état d'ext d'un élément CleanupExt ; les autres sortes sont ignorées. ext
// n'enregistre plus d'élément CleanupExt : la fonction reste pour la signature attendue par vm/vm.go.
func DispatchCleanup(z *zone.Zone, item zone.CleanupItem) {
	if item.Kind == CleanupExt {
		z.SetSlot(zone.SlotExt, nil)
	}
}

// readExtState lit un champ de l'état de la Zone.
func readExtState[T any](z *zone.Zone, get func(*extState) T) T {
	return get(extStateFor(z))
}

// writeExtState modifie l'état de la Zone.
func writeExtState(z *zone.Zone, set func(*extState)) {
	set(extStateFor(z))
}

// IniGet rend la valeur INI d'une directive iconv.* ; l'état de la Zone en est l'unique source, que
// iconv_set_encoding et ini_set modifient tous deux. Faux pour toute autre directive.
func IniGet(z *zone.Zone, key string) (string, bool) {
	kind, ok := strings.CutPrefix(key, "iconv.")
	if !ok || iconvEncodingField(&extState{}, kind) == nil {
		return "", false
	}
	return readExtState(z, func(st *extState) string { return *iconvEncodingField(st, kind) }), true
}

// IniSet applique ini_set à une directive portée par l'état de la Zone. Pour iconv.*, la valeur devient
// celle de iconv_get_encoding ; pour mbstring.internal_encoding, la valeur de la table INI (tenue par
// l'appelant) fixe l'encodage interne effectif, une valeur vide ou inconnue le ramenant à UTF-8 (default_charset).
// warning porte le message d'un encodage inconnu ; handled est faux pour une directive étrangère.
func IniSet(z *zone.Zone, key, value string) (warning string, handled bool) {
	if kind, ok := strings.CutPrefix(key, "iconv."); ok && iconvEncodingField(&extState{}, kind) != nil {
		value = strings.Clone(value)
		writeExtState(z, func(st *extState) { *iconvEncodingField(st, kind) = value })
		return "", true
	}
	if key != "mbstring.internal_encoding" {
		return "", false
	}
	if value == "" {
		writeExtState(z, func(st *extState) { st.mbInternalEncoding = "UTF-8" })
		return "", true
	}
	canon, ok := mbInternalNames[foldEnc(value)]
	if !ok {
		// PHP retombe alors sur default_charset.
		writeExtState(z, func(st *extState) { st.mbInternalEncoding = "UTF-8" })
		return "Unknown encoding \"" + value + "\" in ini setting", true
	}
	writeExtState(z, func(st *extState) { st.mbInternalEncoding = canon })
	return "", true
}

// ---- mots de passe ----

// Paramètres par défaut de password_hash sous PHP 8.3 : coût bcrypt 10 (celui de stdlib.nativePasswordHash,
// préfixe « $2y$10$ »), argon2 en version 19 avec m=65536, t=4, p=1.
const (
	passwordBcryptCost   = 10
	passwordArgon2Ver    = 19
	passwordArgon2Memory = 65536
	passwordArgon2Time   = 4
	passwordArgon2Thread = 1
)

// passwordAlgoByName suit php_password_algo_find : les identifiants enregistrés, comparés avec la casse.
func passwordAlgoByName(name string) string {
	switch name {
	case "2y", "argon2i", "argon2id":
		return name
	}
	return ""
}

// passwordAlgoOf suit php_password_algo_identify_ex : identifiant entre le premier octet et le « $ » suivant,
// rejeté si le hachage n'a pas la forme exigée par l'algorithme.
func passwordAlgoOf(hash string) string {
	if len(hash) < 3 {
		return ""
	}
	end := strings.IndexByte(hash[1:], '$')
	if end < 0 {
		return ""
	}
	algo := passwordAlgoByName(hash[1 : 1+end])
	if algo == "2y" && (len(hash) != 60 || !strings.HasPrefix(hash, "$2y")) {
		return ""
	}
	return algo
}

// leadingLong lit le préfixe entier de s comme sscanf("%ld") ; ok est faux sans chiffre.
func leadingLong(s string) (int64, bool) {
	i := 0
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		i++
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i {
		return 0, false
	}
	n, err := strconv.ParseInt(s[:j], 10, 64)
	return n, err == nil
}

// zvalLong suit zval_get_long pour une option : préfixe numérique d'une chaîne, troncature d'un flottant.
func zvalLong(z *zone.Zone, v types.Value) int64 {
	v = deref(z, v)
	switch v.Type {
	case types.TypeLong, types.TypeDouble:
		return v.ToLong()
	case types.TypeTrue:
		return 1
	case types.TypeString:
		s := strings.TrimLeft(types.StringData(z, v), " \t\n\r\v\f")
		end := 0
		for end < len(s) && strings.IndexByte("+-0123456789.eE", s[end]) >= 0 {
			end++
		}
		for ; end > 0; end-- {
			if f, err := strconv.ParseFloat(s[:end], 64); err == nil {
				return types.FloatToInt(f)
			}
		}
		return 0
	case types.TypeArray:
		if types.ArrayLen(z, v) > 0 {
			return 1
		}
	}
	return 0
}

// optionLong rend options[key] converti, ou def si la clé est absente.
func optionLong(z *zone.Zone, opts types.Value, key string, def int64) int64 {
	if opts.Type != types.TypeArray {
		return def
	}
	k, err := newStr(z, key)
	if err != nil {
		return def
	}
	v, ok, err := types.ArrayGet(z, opts, k, nil)
	types.ReleaseValue(z, &k)
	if err != nil || !ok {
		return def
	}
	return zvalLong(z, v)
}

func nativePasswordNeedsRehash(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "password_needs_rehash"
	if err := arity(fn, len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	hash, err := asString(z, args[0], fn, "1", "hash")
	if err != nil {
		return types.Value{}, err
	}
	var want string
	switch a := deref(z, args[1]); a.Type {
	case types.TypeUndef, types.TypeNull:
		want = "2y"
	case types.TypeString:
		want = passwordAlgoByName(types.StringData(z, a))
	case types.TypeLong, types.TypeDouble, types.TypeFalse, types.TypeTrue:
		n, _ := asInt(z, a, fn, "2", "algo")
		want = [...]string{"2y", "2y", "argon2i", "argon2id", ""}[min(uint64(n), 4)]
	default:
		return types.Value{}, typeErr(fn, "2", "algo", "string|int|null", typeKind(z, a))
	}
	var opts types.Value
	if len(args) == 3 {
		if opts = deref(z, args[2]); opts.Type != types.TypeArray {
			return types.Value{}, typeErr(fn, "3", "options", "array", typeKind(z, opts))
		}
	}
	if want == "" {
		// Algorithme demandé inconnu : PHP ne propose jamais de rehachage.
		return types.NewBool(false), nil
	}
	if passwordAlgoOf(hash) != want {
		return types.NewBool(true), nil
	}
	if want == "2y" {
		cost := int64(passwordBcryptCost)
		if n, ok := leadingLong(hash[4:]); ok {
			cost = n
		}
		return types.NewBool(cost != optionLong(z, opts, "cost", passwordBcryptCost)), nil
	}
	ver, mem, tim, thr := argon2Params(hash)
	return types.NewBool(ver != passwordArgon2Ver ||
		mem != optionLong(z, opts, "memory_cost", passwordArgon2Memory) ||
		tim != optionLong(z, opts, "time_cost", passwordArgon2Time) ||
		thr != optionLong(z, opts, "threads", passwordArgon2Thread)), nil
}

// argon2Params suit extract_argon2_parameters : « v=V$m=M,t=T,p=P », ou sans « v= » pour la version 0x10.
// Un champ illisible garde sa valeur par défaut.
func argon2Params(hash string) (ver, mem, tim, thr int64) {
	ver, mem, tim, thr = passwordArgon2Ver, passwordArgon2Memory, passwordArgon2Time, passwordArgon2Thread
	p, ok := strings.CutPrefix(hash, "$argon2id$")
	if !ok {
		if p, ok = strings.CutPrefix(hash, "$argon2i$"); !ok {
			return
		}
	}
	fields := []*int64{&ver, &mem, &tim, &thr}
	prefixes := []string{"v=", "$m=", ",t=", ",p="}
	if !strings.HasPrefix(p, "v=") {
		ver = 0x10
		fields, prefixes = fields[1:], prefixes[1:]
	}
	for i, pre := range prefixes {
		rest, ok := strings.CutPrefix(p, pre)
		if !ok {
			return
		}
		n, ok := leadingLong(rest)
		if !ok {
			return
		}
		*fields[i] = n
		k := 0
		if rest != "" && (rest[0] == '-' || rest[0] == '+') {
			k++
		}
		for k < len(rest) && rest[k] >= '0' && rest[k] <= '9' {
			k++
		}
		p = rest[k:]
	}
	return
}

// ---- ctype ----

// nativeCtypePrint suit la coercition de stdlib.nativeCtype : un entier de -128 à 255 désigne un octet
// (négatif : +256), au-delà sa représentation décimale ; tout autre type rend false.
func nativeCtypePrint(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("ctype_print", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	var text string
	switch v := deref(z, args[0]); v.Type {
	case types.TypeLong:
		n := v.ToLong()
		if n >= -128 && n <= 255 {
			text = string([]byte{byte(n)})
		} else {
			text = strconv.FormatInt(n, 10)
		}
	case types.TypeString:
		text = types.StringData(z, v)
	default:
		return types.NewBool(false), nil
	}
	if text == "" {
		return types.NewBool(false), nil
	}
	for i := 0; i < len(text); i++ {
		if text[i] < 0x20 || text[i] > 0x7e {
			return types.NewBool(false), nil
		}
	}
	return types.NewBool(true), nil
}
