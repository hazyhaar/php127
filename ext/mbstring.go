package ext

import (
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
	"unsafe"

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
	case "ascii", "us-ascii", "7bit", "8bit", "binary", "pass",
		"iso-8859-1", "iso8859-1", "latin1", "l1", "windows-1252", "cp1252":
		return encBytes, true
	default:
		return 0, false
	}
}

func encodingArg(z *zone.Zone, args []types.Value, i int, fn string) (int, error) {
	if len(args) <= i || args[i].IsUndef() || deref(z, args[i]).IsNull() {
		// Encodage omis : celui de mb_internal_encoding() pour la Zone.
		if kind, ok := resolveEncoding(readExtState(z, func(st *extState) string { return st.mbInternalEncoding })); ok {
			return kind, nil
		}
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

// strBytes expose les octets d'une chaîne sans copie ; les noyaux appelés dessus ne font que lire.
func strBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// cpOffset rend le décalage en octets du point de code de rang k de s, len(s) au-delà, sans allocation.
// Le découpage est celui de mbSegment (noyau c2_utf8_char_to_byte, ext/c_src/utf8_scan.c).
func cpOffset(s string, k int) int {
	if k <= 0 {
		return 0
	}
	return int(C2_utf8_char_to_byte(strBytes(s), uint64(len(s)), uint64(k)))
}

// cpStep rend la longueur en octets du point de code qui commence à s[i], zéro en fin de chaîne.
func cpStep(s string, i int) int {
	return cpOffset(s[i:], 1)
}

func charLen(s string, kind int) int {
	if kind == encBytes {
		return len(s)
	}
	raw := strBytes(s)
	if stdlib.C2_utf8_validate(raw, uint64(len(raw))) == 1 {
		return int(stdlib.C2_utf8_count(raw, uint64(len(raw))))
	}
	return mbInvalidUTF8Count(s)
}

func mbInvalidUTF8Count(s string) int {
	n := 0
	for i := 0; i < len(s); n++ {
		k, _ := mbSegment(s, i)
		i += k
	}
	return n
}

// mbSegment retourne la longueur en octets (au moins 1) du point de code commençant à s[i] et sa validité.
// Une séquence invalide forme un seul segment, sa plus longue sous-partie valide, comme le décodeur UTF-8
// de mbstring (PHP 8.3) : mb_strlen("\xe2\x82") vaut 1, mb_strlen("\x80\x81") vaut 2.
func mbSegment(s string, i int) (int, bool) {
	b0 := s[i]
	cont := func(k int) bool { return i+k < len(s) && s[i+k]&0xc0 == 0x80 }
	switch {
	case b0 < 0x80:
		return 1, true
	case b0 < 0xc2 || b0 > 0xf4:
		return 1, false
	case b0 < 0xe0:
		if cont(1) {
			return 2, true
		}
		return 1, false
	case b0 < 0xf0:
		if !cont(1) {
			return 1, false
		}
		b1 := s[i+1]
		if (b0 == 0xe0 && b1 < 0xa0) || (b0 == 0xed && b1 >= 0xa0) {
			return 1, false
		}
		if cont(2) {
			return 3, true
		}
		return 2, false
	default:
		if !cont(1) {
			return 1, false
		}
		b1 := s[i+1]
		if (b0 == 0xf0 && b1 < 0x90) || (b0 == 0xf4 && b1 >= 0x90) {
			return 1, false
		}
		if !cont(2) {
			return 2, false
		}
		if cont(3) {
			return 4, true
		}
		return 3, false
	}
}

// mbScrub remplace chaque segment UTF-8 invalide par '?', comme la sortie de mb_substr sous PHP 8.3.
func mbScrub(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		n, ok := mbSegment(s, i)
		if ok {
			b.WriteString(s[i : i+n])
		} else {
			b.WriteByte('?')
		}
		i += n
	}
	return b.String()
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
	n := charLen(s, kind)
	if start < 0 {
		start += n
	}
	if start < 0 {
		start = 0
	}
	if start > n {
		return ""
	}
	a := cpOffset(s, start)
	if length == nil {
		return s[a:]
	}
	ln := *length
	end := min(start+ln, n)
	if ln < 0 {
		end = n + ln
		if end < start {
			return ""
		}
	}
	return s[a : a+cpOffset(s[a:], end-start)]
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
	out := sliceChars(s, int(start), length, kind)
	if kind != encBytes {
		out = mbScrub(out)
	}
	return newStr(z, out)
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
	return mbPosCore(hay, needle, off, kind, name, reverse)
}

func mbPosCore(hay, needle string, off int64, kind int, name string, reverse bool) (types.Value, error) {
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

// cpPos compare needle à la fenêtre hay[a:b] de ncp points de code qui glisse d'un point de code par pas ;
// les deux bornes avancent par le noyau c2_utf8_char_to_byte, sans table de décalages.
func cpPos(hay, needle string, start int, neg bool, n int, reverse bool) (types.Value, error) {
	if needle == "" {
		if reverse && !neg {
			return types.NewLong(int64(n)), nil
		}
		return types.NewLong(int64(start)), nil
	}
	ncp := charLen(needle, encUTF8)
	from, limit := start, n
	if reverse && neg {
		from, limit = 0, start+1
	}
	if from+ncp > n {
		return types.NewBool(false), nil
	}
	a := cpOffset(hay, from)
	b := a + cpOffset(hay[a:], ncp)
	found := -1
	for i := from; i < limit && i+ncp <= n; i++ {
		if hay[a:b] == needle {
			if !reverse {
				return types.NewLong(int64(i)), nil
			}
			found = i
		}
		a += cpStep(hay, a)
		b += cpStep(hay, b)
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

// ---- extensions mbstring pour WordPress ----

// Les fonctions de configuration conservent leur valeur dans l'état de la Zone (extState, wp_ext.go),
// remis aux valeurs par défaut de PHP par z.Reset.

func validMBEncoding(name string) bool {
	if _, ok := resolveEncoding(name); ok {
		return true
	}
	_, ok := lookupCharset(name)
	return ok
}

// mbInternalNames et mbRegexNames donnent le nom canonique rendu par mb_internal_encoding() et
// mb_regex_encoding() pour chaque nom accepté, mesurés sur PHP 8.3.6 (mbstring.so) ; un nom absent est refusé.
var mbInternalNames = map[string]string{
	"utf-8": "UTF-8", "utf8": "UTF-8",
	"ascii": "ASCII", "us-ascii": "ASCII",
	"7bit": "7bit", "8bit": "8bit", "binary": "8bit",
	"iso-8859-1": "ISO-8859-1", "iso8859-1": "ISO-8859-1", "latin1": "ISO-8859-1",
	"windows-1252": "Windows-1252", "cp1252": "Windows-1252",
}

var mbRegexNames = map[string]string{
	"utf-8": "UTF-8", "utf8": "UTF-8",
	"ascii": "ASCII", "us-ascii": "ASCII",
	"iso-8859-1": "ISO-8859-1", "iso8859-1": "ISO-8859-1",
}

func nativeMBInternalEncoding(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return mbEncodingSetting(z, args, "mb_internal_encoding", mbInternalNames,
		func(st *extState) *string { return &st.mbInternalEncoding })
}

func nativeMBRegexEncoding(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return mbEncodingSetting(z, args, "mb_regex_encoding", mbRegexNames,
		func(st *extState) *string { return &st.mbRegexEncoding })
}

func mbEncodingSetting(z *zone.Zone, args []types.Value, name string, names map[string]string, field func(*extState) *string) (types.Value, error) {
	if err := arity(name, len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	if len(args) == 0 || missing(deref(z, args[0])) {
		return newStr(z, readExtState(z, func(st *extState) string { return *field(st) }))
	}
	enc, err := asString(z, args[0], name, "1", "encoding")
	if err != nil {
		return types.Value{}, err
	}
	canon, ok := names[foldEnc(enc)]
	if !ok {
		return types.Value{}, ValueError{name + "(): Argument #1 ($encoding) must be a valid encoding, \"" + enc + "\" given"}
	}
	writeExtState(z, func(st *extState) { *field(st) = canon })
	return types.NewBool(true), nil
}

// mbLanguages associe chaque nom accepté par mb_language au nom canonique rendu par mb_language().
var mbLanguages = [...]struct{ alias, canon string }{
	{"neutral", "neutral"}, {"uni", "uni"},
	{"ja", "Japanese"}, {"japanese", "Japanese"}, {"en", "English"}, {"english", "English"},
	{"de", "German"}, {"german", "German"}, {"ko", "Korean"}, {"korean", "Korean"},
	{"ru", "Russian"}, {"russian", "Russian"}, {"hy", "Armenian"}, {"armenian", "Armenian"},
	{"tr", "Turkish"}, {"turkish", "Turkish"}, {"ua", "Ukrainian"}, {"ukrainian", "Ukrainian"},
	{"zh-cn", "Simplified Chinese"}, {"simplified chinese", "Simplified Chinese"},
	{"zh-tw", "Traditional Chinese"}, {"traditional chinese", "Traditional Chinese"},
}

func nativeMBLanguage(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_language", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	if len(args) == 0 || missing(deref(z, args[0])) {
		return newStr(z, readExtState(z, func(st *extState) string { return st.mbLanguage }))
	}
	lang, err := asString(z, args[0], "mb_language", "1", "language")
	if err != nil {
		return types.Value{}, err
	}
	for _, l := range mbLanguages {
		if strings.EqualFold(lang, l.alias) {
			writeExtState(z, func(st *extState) { st.mbLanguage = l.canon })
			return types.NewBool(true), nil
		}
	}
	return types.Value{}, ValueError{"mb_language(): Argument #1 ($language) must be a valid language, \"" + lang + "\" given"}
}

var defaultDetectOrder = [...]charset{csASCII, csUTF8}

// zoneDetectOrder rend l'ordre de détection courant de la Zone en jeux de caractères.
func zoneDetectOrder(z *zone.Zone) []charset {
	names := readExtState(z, func(st *extState) []string { return slices.Clone(st.mbDetectOrder) })
	out := make([]charset, 0, len(names))
	for _, name := range names {
		if cs, ok := lookupCharset(name); ok {
			out = append(out, cs)
		}
	}
	if len(out) == 0 {
		return defaultDetectOrder[:]
	}
	return out
}

// encodingList lit un argument array|string|null en liste de jeux de caractères ; null rend l'ordre posé par mb_detect_order pour la Zone.
func encodingList(z *zone.Zone, args []types.Value, i int, fn, idx, param string) ([]charset, error) {
	if len(args) <= i || missing(deref(z, args[i])) {
		return zoneDetectOrder(z), nil
	}
	var names []string
	v := deref(z, args[i])
	if v.Type == types.TypeArray {
		var ferr error
		types.ArrayForEach(z, v, func(_, item types.Value) bool {
			s, err := asString(z, item, fn, idx, param)
			if err != nil {
				ferr = err
				return false
			}
			names = append(names, s)
			return true
		})
		if ferr != nil {
			return nil, ferr
		}
	} else {
		s, err := asString(z, v, fn, idx, param)
		if err != nil {
			return nil, err
		}
		if s != "" {
			names = strings.Split(s, ",")
		}
	}
	var out []charset
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if strings.EqualFold(name, "auto") {
			out = append(out, defaultDetectOrder[:]...)
			continue
		}
		cs, ok := lookupCharset(name)
		if !ok {
			return nil, ValueError{fn + "(): Argument #" + idx + " ($" + param + ") contains invalid encoding \"" + name + "\""}
		}
		out = append(out, cs)
	}
	if len(out) == 0 {
		return nil, ValueError{fn + "(): Argument #" + idx + " ($" + param + ") must specify at least one encoding"}
	}
	return out, nil
}

func nativeMBDetectOrder(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_detect_order", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	if len(args) == 1 && !missing(deref(z, args[0])) {
		list, err := encodingList(z, args, 0, "mb_detect_order", "1", "encoding")
		if err != nil {
			return types.Value{}, err
		}
		order := make([]string, len(list))
		for i, cs := range list {
			order[i] = charsetNames[cs]
		}
		writeExtState(z, func(st *extState) { st.mbDetectOrder = order })
		return types.NewBool(true), nil
	}
	order := readExtState(z, func(st *extState) []string { return st.mbDetectOrder })
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, name := range order {
		s, err := newStr(z, name)
		if err != nil {
			return types.Value{}, err
		}
		if err = appendVal(z, &a, s); err != nil {
			return types.Value{}, err
		}
	}
	return a, nil
}

// detectDemerit suit l'estimation de mbstring 8.3 : point de code astral 40, ponctuation 0x21-0x2F 6, contrôle rare 30, sinon 1.
func detectDemerit(r rune) int {
	switch {
	case r == badRune:
		return 1000
	case r > 0xffff:
		return 40
	case r >= 0x21 && r <= 0x2f:
		return 6
	case r < 0x20 && r != '\t' && r != '\n' && r != '\r', r >= 0x7f && r < 0xa0:
		return 30
	default:
		return 1
	}
}

// detectCharset choisit, dans l'ordre donné, l'encodage de moindre démérite ; strict écarte toute candidate invalide.
func detectCharset(s string, list []charset, strict bool) (charset, bool) {
	return detectCharsetMulti([]string{s}, list, strict)
}

// detectCharsetMulti cumule, pour chaque candidate, le démérite de toutes les chaînes décodées séparément.
func detectCharsetMulti(strs []string, list []charset, strict bool) (charset, bool) {
	best, bestScore := charset(0), -1
next:
	for _, cs := range list {
		score := 0
		for _, s := range strs {
			runes := mbDecode(s, cs)
			if strict && badCount(runes) > 0 {
				continue next
			}
			for _, r := range runes {
				score += detectDemerit(r)
			}
		}
		if bestScore < 0 || score < bestScore {
			best, bestScore = cs, score
		}
	}
	return best, bestScore >= 0
}

func nativeMBDetectEncoding(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_detect_encoding", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "mb_detect_encoding", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	list, err := encodingList(z, args, 1, "mb_detect_encoding", "2", "encodings")
	if err != nil {
		return types.Value{}, err
	}
	cs, ok := detectCharset(s, list, optBool(z, args, 2))
	if !ok {
		return types.NewBool(false), nil
	}
	return newStr(z, charsetNames[cs])
}

func nativeMBConvertEncoding(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_convert_encoding", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	toName, err := asString(z, args[1], "mb_convert_encoding", "2", "to_encoding")
	if err != nil {
		return types.Value{}, err
	}
	to, ok := lookupCharset(toName)
	if !ok {
		return types.Value{}, ValueError{"mb_convert_encoding(): Argument #2 ($to_encoding) must be a valid encoding, \"" + toName + "\" given"}
	}
	from := []charset{csUTF8}
	if len(args) == 3 && !missing(deref(z, args[2])) {
		if from, err = encodingList(z, args, 2, "mb_convert_encoding", "3", "from_encoding"); err != nil {
			return types.Value{}, err
		}
	}
	return mbConvertValue(z, args[0], to, from)
}

// mbConvertString convertit s ; les séquences invalides et les points de code non représentables dans to
// sont remplacés selon le réglage mb_substitute_character de la Zone (sub).
func mbConvertString(s string, to charset, from []charset, sub mbSubst) string {
	src := from[0]
	if len(from) > 1 {
		src, _ = detectCharset(s, from, false)
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range mbDecode(s, src) {
		switch {
		case r == badRune:
			sub.invalid(&b, to)
		case to == csCP1252 && r >= 0x80 && r < 0xa0 && cp1252High[r-0x80] == 0:
			b.WriteByte(byte(r))
		case !encodeRune(&b, r, to):
			sub.unrepresentable(&b, r, to)
		}
	}
	return b.String()
}

// mbDecode suit mbstring : un octet non défini de Windows-1252 devient le point de code C1 de même valeur, là où iconv le rejette.
func mbDecode(s string, cs charset) []rune {
	runes := decodeCharset(s, cs)
	if cs == csCP1252 {
		for i, r := range runes {
			if r == badRune {
				runes[i] = rune(s[i])
			}
		}
	}
	return runes
}

func mbConvertValue(z *zone.Zone, v types.Value, to charset, from []charset) (types.Value, error) {
	v = deref(z, v)
	if v.Type != types.TypeArray {
		s, err := asString(z, v, "mb_convert_encoding", "1", "string")
		if err != nil {
			return types.Value{}, err
		}
		return newStr(z, mbConvertString(s, to, from, zoneSubst(z)))
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	var ferr error
	types.ArrayForEach(z, v, func(k, item types.Value) bool {
		k = deref(z, k)
		if k.Type == types.TypeString {
			if k, ferr = newStr(z, mbConvertString(types.StringData(z, k), to, from, zoneSubst(z))); ferr != nil {
				return false
			}
		}
		var conv types.Value
		if conv, ferr = mbConvertValue(z, item, to, from); ferr != nil {
			return false
		}
		ferr = types.ArraySet(z, &out, k, conv, nil)
		return ferr == nil
	})
	if ferr != nil {
		return types.Value{}, ferr
	}
	return out, nil
}

const (
	mbCaseUpper = iota
	mbCaseLower
	mbCaseTitle
	mbCaseFold
	mbCaseUpperSimple
	mbCaseLowerSimple
	mbCaseTitleSimple
	mbCaseFoldSimple
)

func nativeMBConvertCase(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_convert_case", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "mb_convert_case", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	mode, err := asInt(z, args[1], "mb_convert_case", "2", "mode")
	if err != nil {
		return types.Value{}, err
	}
	kind, err := encodingArg(z, args, 2, "mb_convert_case")
	if err != nil {
		return types.Value{}, err
	}
	if mode < mbCaseUpper || mode > mbCaseFoldSimple {
		return types.Value{}, ValueError{"mb_convert_case(): Argument #2 ($mode) must be one of the MB_CASE_* constants"}
	}
	if kind == encBytes {
		return newStr(z, asciiCase(s, mode))
	}
	return newStr(z, convertCase(s, mode))
}

func asciiCase(s string, mode int64) string {
	upper := mode == mbCaseUpper || mode == mbCaseUpperSimple
	title := mode == mbCaseTitle || mode == mbCaseTitleSimple
	b := []byte(s)
	inWord := false
	for i, c := range b {
		lower := c >= 'a' && c <= 'z'
		up := c >= 'A' && c <= 'Z'
		switch {
		case upper || title && !inWord:
			if lower {
				b[i] = c - 32
			}
		default:
			if up {
				b[i] = c + 32
			}
		}
		if c != '\'' && c != '.' && c != ':' {
			inWord = lower || up
		}
	}
	return string(b)
}

func convertCase(s string, mode int64) string {
	switch mode {
	case mbCaseUpper:
		return mapCase(s, true)
	case mbCaseLower:
		return mapCase(s, false)
	case mbCaseTitle:
		return titleCase(s, true)
	case mbCaseFold:
		return mapRunes(s, func(b *strings.Builder, r rune) {
			if alt, ok := fullFold(r); ok {
				b.WriteString(alt)
			} else {
				b.WriteRune(foldRune(r))
			}
		})
	case mbCaseUpperSimple:
		return mapRunes(s, func(b *strings.Builder, r rune) { b.WriteRune(unicode.ToUpper(r)) })
	case mbCaseLowerSimple:
		return mapRunes(s, func(b *strings.Builder, r rune) { b.WriteRune(unicode.ToLower(r)) })
	case mbCaseTitleSimple:
		return titleCase(s, false)
	default:
		return foldSimple(s)
	}
}

// mapRunes applique f à chaque point de code valide ; un octet invalide est recopié tel quel.
func mapRunes(s string, f func(*strings.Builder, rune)) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteByte(s[i])
		} else {
			f(&b, r)
		}
		i += size
	}
	return b.String()
}

func foldRune(r rune) rune { return unicode.ToLower(unicode.ToUpper(r)) }

func foldSimple(s string) string {
	return mapRunes(s, func(b *strings.Builder, r rune) { b.WriteRune(foldRune(r)) })
}

func fullFold(r rune) (string, bool) {
	switch r {
	case 'ß', 'ẞ':
		return "ss", true
	case 'İ':
		return "i̇", true
	case 'ﬀ':
		return "ff", true
	case 'ﬁ':
		return "fi", true
	case 'ﬂ':
		return "fl", true
	case 'ﬃ':
		return "ffi", true
	case 'ﬄ':
		return "ffl", true
	case 'ﬅ', 'ﬆ':
		return "st", true
	}
	return "", false
}

func fullTitle(r rune) (string, bool) {
	switch r {
	case 'ß':
		return "Ss", true
	case 'ﬀ':
		return "Ff", true
	case 'ﬁ':
		return "Fi", true
	case 'ﬂ':
		return "Fl", true
	case 'ﬃ':
		return "Ffi", true
	case 'ﬄ':
		return "Ffl", true
	case 'ﬅ', 'ﬆ':
		return "St", true
	}
	return "", false
}

func isCased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) ||
		unicode.Is(unicode.Other_Lowercase, r) || unicode.Is(unicode.Other_Uppercase, r)
}

// isCaseIgnorable suit la propriété Unicode Case_Ignorable : Mn, Me, Cf, Lm, Sk et les ponctuations MidLetter, MidNumLet et Single_Quote.
func isCaseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', 0xb7, 0x387, 0x55f, 0x5f4, 0x2018, 0x2019, 0x2024, 0x2027, 0xfe13, 0xfe52, 0xfe55, 0xff07, 0xff0e, 0xff1a:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// titleCase met en casse de titre toute lettre casée qui ne suit pas une lettre casée, en ignorant les caractères Case_Ignorable.
func titleCase(s string, full bool) string {
	inWord := false
	return mapRunes(s, func(b *strings.Builder, r rune) {
		switch {
		case inWord:
			if alt, ok := specialCase(r, false); ok && full {
				b.WriteString(alt)
			} else {
				b.WriteRune(unicode.ToLower(r))
			}
		case isCased(r):
			if alt, ok := fullTitle(r); ok && full {
				b.WriteString(alt)
			} else {
				b.WriteRune(unicode.ToTitle(r))
			}
		default:
			b.WriteRune(r)
		}
		if !isCaseIgnorable(r) {
			inWord = isCased(r)
		}
	})
}

func nativeMBStripos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return mbIPos(z, args, "mb_stripos", false)
}

func nativeMBStrripos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return mbIPos(z, args, "mb_strripos", true)
}

func mbIPos(z *zone.Zone, args []types.Value, name string, reverse bool) (types.Value, error) {
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
	off, err := optInt(z, args, 2, 0, name, "3", "offset")
	if err != nil {
		return types.Value{}, err
	}
	kind, err := encodingArg(z, args, 3, name)
	if err != nil {
		return types.Value{}, err
	}
	if kind == encBytes {
		return mbPosCore(asciiCase(hay, mbCaseLower), asciiCase(needle, mbCaseLower), off, kind, name, reverse)
	}
	return mbPosCore(foldSimple(hay), foldSimple(needle), off, kind, name, reverse)
}

func nativeMBStrstr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return mbStrstr(z, args, "mb_strstr", false, false)
}

func nativeMBStristr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return mbStrstr(z, args, "mb_stristr", true, false)
}

func nativeMBStrrchr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return mbStrstr(z, args, "mb_strrchr", false, true)
}

func mbStrstr(z *zone.Zone, args []types.Value, name string, fold, reverse bool) (types.Value, error) {
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
	before := optBool(z, args, 2)
	kind, err := encodingArg(z, args, 3, name)
	if err != nil {
		return types.Value{}, err
	}
	h, n := hay, needle
	if fold && kind == encBytes {
		h, n = asciiCase(hay, mbCaseLower), asciiCase(needle, mbCaseLower)
	} else if fold {
		h, n = foldSimple(hay), foldSimple(needle)
	}
	var i int
	if reverse {
		i = strings.LastIndex(h, n)
	} else {
		i = strings.Index(h, n)
	}
	if i < 0 {
		return types.NewBool(false), nil
	}
	if fold && kind != encBytes {
		// Le pliage simple conserve le nombre de points de code, pas leur longueur en octets.
		i = cpOffset(hay, charLen(h[:i], kind))
	}
	if before {
		return newStr(z, hay[:i])
	}
	return newStr(z, hay[i:])
}

func nativeMBSubstrCount(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_substr_count", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	hay, err := asString(z, args[0], "mb_substr_count", "1", "haystack")
	if err != nil {
		return types.Value{}, err
	}
	needle, err := asString(z, args[1], "mb_substr_count", "2", "needle")
	if err != nil {
		return types.Value{}, err
	}
	if _, err = encodingArg(z, args, 2, "mb_substr_count"); err != nil {
		return types.Value{}, err
	}
	if needle == "" {
		return types.Value{}, ValueError{"mb_substr_count(): Argument #2 ($needle) must not be empty"}
	}
	return types.NewLong(int64(strings.Count(hay, needle))), nil
}

// wideRanges liste les plages East_Asian_Width W et F comptées deux colonnes par mb_strwidth.
var wideRanges = [...][2]rune{
	{0x1100, 0x115f}, {0x231a, 0x231b}, {0x2329, 0x232a}, {0x23e9, 0x23ec}, {0x23f0, 0x23f0},
	{0x23f3, 0x23f3}, {0x25fd, 0x25fe}, {0x2614, 0x2615}, {0x2648, 0x2653}, {0x267f, 0x267f},
	{0x2693, 0x2693}, {0x26a1, 0x26a1}, {0x26aa, 0x26ab}, {0x26bd, 0x26be}, {0x26c4, 0x26c5},
	{0x26ce, 0x26ce}, {0x26d4, 0x26d4}, {0x26ea, 0x26ea}, {0x26f2, 0x26f3}, {0x26f5, 0x26f5},
	{0x26fa, 0x26fa}, {0x26fd, 0x26fd}, {0x2705, 0x2705}, {0x270a, 0x270b}, {0x2728, 0x2728},
	{0x274c, 0x274c}, {0x274e, 0x274e}, {0x2753, 0x2755}, {0x2757, 0x2757}, {0x2795, 0x2797},
	{0x27b0, 0x27b0}, {0x27bf, 0x27bf}, {0x2b1b, 0x2b1c}, {0x2b50, 0x2b50}, {0x2b55, 0x2b55},
	{0x2e80, 0x303e}, {0x3041, 0x33ff}, {0x3400, 0x4dbf}, {0x4e00, 0x9fff}, {0xa000, 0xa4cf},
	{0xa960, 0xa97f}, {0xac00, 0xd7a3}, {0xf900, 0xfaff}, {0xfe10, 0xfe19}, {0xfe30, 0xfe6f},
	{0xff00, 0xff60}, {0xffe0, 0xffe6}, {0x16fe0, 0x16fe4}, {0x17000, 0x18aff}, {0x1b000, 0x1b2ff},
	{0x1f004, 0x1f004}, {0x1f0cf, 0x1f0cf}, {0x1f18e, 0x1f18e}, {0x1f191, 0x1f19a}, {0x1f200, 0x1f251},
	{0x1f300, 0x1f320}, {0x1f32d, 0x1f335}, {0x1f337, 0x1f37c}, {0x1f37e, 0x1f393}, {0x1f3a0, 0x1f3ca},
	{0x1f3cf, 0x1f3d3}, {0x1f3e0, 0x1f3f0}, {0x1f3f4, 0x1f3f4}, {0x1f3f8, 0x1f43e}, {0x1f440, 0x1f440},
	{0x1f442, 0x1f4fc}, {0x1f4ff, 0x1f53d}, {0x1f54b, 0x1f54e}, {0x1f550, 0x1f567}, {0x1f57a, 0x1f57a},
	{0x1f595, 0x1f596}, {0x1f5a4, 0x1f5a4}, {0x1f5fb, 0x1f64f}, {0x1f680, 0x1f6c5}, {0x1f6cc, 0x1f6cc},
	{0x1f6d0, 0x1f6d2}, {0x1f6d5, 0x1f6d7}, {0x1f6dc, 0x1f6df}, {0x1f6eb, 0x1f6ec}, {0x1f6f4, 0x1f6fc},
	{0x1f7e0, 0x1f7eb}, {0x1f7f0, 0x1f7f0}, {0x1f90c, 0x1f93a}, {0x1f93c, 0x1f945}, {0x1f947, 0x1f9ff},
	{0x1fa70, 0x1faff}, {0x20000, 0x2fffd}, {0x30000, 0x3fffd},
}

func runeWidth(r rune) int {
	lo, hi := 0, len(wideRanges)
	for lo < hi {
		mid := (lo + hi) >> 1
		switch {
		case r < wideRanges[mid][0]:
			hi = mid
		case r > wideRanges[mid][1]:
			lo = mid + 1
		default:
			return 2
		}
	}
	return 1
}

func strWidth(s string, kind int) int {
	if kind == encBytes {
		return len(s)
	}
	w := 0
	for i := 0; i < len(s); {
		r, size := nextUTF8(s, i)
		if r == badRune {
			w++
		} else {
			w += runeWidth(r)
		}
		i += size
	}
	return w
}

func nativeMBStrwidth(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_strwidth", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "mb_strwidth", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	kind, err := encodingArg(z, args, 1, "mb_strwidth")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(strWidth(s, kind))), nil
}

func nativeMBStrimwidth(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_strimwidth", len(args), 3, 5); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "mb_strimwidth", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	start, err := asInt(z, args[1], "mb_strimwidth", "2", "start")
	if err != nil {
		return types.Value{}, err
	}
	width, err := asInt(z, args[2], "mb_strimwidth", "3", "width")
	if err != nil {
		return types.Value{}, err
	}
	marker, err := optString(z, args, 3, "mb_strimwidth", "4", "trim_marker")
	if err != nil {
		return types.Value{}, err
	}
	kind, err := encodingArg(z, args, 4, "mb_strimwidth")
	if err != nil {
		return types.Value{}, err
	}
	n := int64(charLen(s, kind))
	if start < 0 {
		start += n
	}
	if start < 0 || start > n {
		return types.Value{}, ValueError{"mb_strimwidth(): Argument #2 ($start) is out of range"}
	}
	off := int(start)
	if kind != encBytes {
		off = cpOffset(s, int(start))
	}
	rest := s[off:]
	total := int64(strWidth(rest, kind))
	if width < 0 {
		width += total
		if width < 0 {
			return types.Value{}, ValueError{"mb_strimwidth(): Argument #3 ($width) is out of range"}
		}
	}
	if total <= width {
		return newStr(z, rest)
	}
	avail := width - int64(strWidth(marker, kind))
	if avail <= 0 {
		return newStr(z, marker)
	}
	acc := int64(0)
	end := off
	for end < len(s) {
		step := 1
		if kind != encBytes {
			step = cpStep(s, end)
		}
		w := int64(strWidth(s[end:end+step], kind))
		if acc+w > avail {
			break
		}
		acc += w
		end += step
	}
	return newStr(z, s[off:end]+marker)
}

func nativeMBListEncodings(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_list_encodings", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, name := range charsetNames {
		s, err := newStr(z, name)
		if err != nil {
			return types.Value{}, err
		}
		if err = appendVal(z, &a, s); err != nil {
			return types.Value{}, err
		}
	}
	return a, nil
}

// nativeMBStrcut découpe en octets comme mbfl_strcut de PHP 8.3 : sous UTF-8, le début et la fin reculent
// jusqu'au début du caractère qui les contient, d'après la seule table des longueurs par octet de tête
// (noyau c2_utf8_cut_floor) ; sous un encodage d'un octet par caractère, le découpage est brut.
func nativeMBStrcut(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_strcut", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "mb_strcut", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	from, err := asInt(z, args[1], "mb_strcut", "2", "start")
	if err != nil {
		return types.Value{}, err
	}
	n := int64(len(s))
	length := n
	lengthSet := len(args) >= 3 && !args[2].IsUndef() && !deref(z, args[2]).IsNull()
	if lengthSet {
		if length, err = asInt(z, args[2], "mb_strcut", "3", "length"); err != nil {
			return types.Value{}, err
		}
	}
	kind, err := encodingArg(z, args, 3, "mb_strcut")
	if err != nil {
		return types.Value{}, err
	}
	if from < 0 {
		from = max(n+from, 0)
	}
	if lengthSet && length < 0 {
		length = max(n-from+length, 0)
	}
	if from > n {
		return newStr(z, "")
	}
	return newStr(z, strcut(s, from, length, kind))
}

// strcut applique le découpage de mbfl_strcut à 0 <= from <= len(s) et length >= 0.
func strcut(s string, from, length int64, kind int) string {
	n := int64(len(s))
	if kind == encBytes {
		if length >= n-from {
			return s[from:]
		}
		return s[from : from+length]
	}
	raw := strBytes(s)
	start := int64(C2_utf8_cut_floor(raw, 0, uint64(from)))
	if length >= n-start {
		return s[start:]
	}
	end := int64(C2_utf8_cut_floor(raw, uint64(start), uint64(start+length)))
	return s[start:end]
}

func nativeMBStrrichr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return mbStrstr(z, args, "mb_strrichr", true, true)
}

// nativeMBConvertVariables convertit en place chaque variable transmise par référence (chaînes, et valeurs des
// tableaux à toute profondeur ; les clés restent inchangées, comme sous PHP 8.3). L'encodage source est détecté
// une seule fois sur l'ensemble des chaînes, dans l'ordre de parcours, puis rendu par son nom canonique.
func nativeMBConvertVariables(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) < 3 {
		return types.Value{}, ArgumentCountError{"mb_convert_variables() expects at least 3 arguments, " + strconv.Itoa(len(args)) + " given"}
	}
	toName, err := asString(z, args[0], "mb_convert_variables", "1", "to_encoding")
	if err != nil {
		return types.Value{}, err
	}
	to, ok := lookupCharset(toName)
	if !ok {
		return types.Value{}, ValueError{"mb_convert_variables(): Argument #1 ($to_encoding) must be a valid encoding, \"" + toName + "\" given"}
	}
	if deref(z, args[1]).Type != types.TypeArray {
		if _, err = asStrictString(z, args[1], "mb_convert_variables", "2", "from_encoding"); err != nil {
			return types.Value{}, err
		}
	}
	from, err := encodingList(z, args, 1, "mb_convert_variables", "2", "from_encoding")
	if err != nil {
		return types.Value{}, err
	}
	src := from[0]
	if len(from) > 1 {
		var strs []string
		for _, v := range args[2:] {
			collectStrings(z, deref(z, v), &strs, 0)
		}
		if src, ok = detectCharsetMulti(strs, from, false); !ok {
			return types.NewBool(false), nil
		}
	}
	single := []charset{src}
	for _, v := range args[2:] {
		if v.Type != types.TypeReference {
			continue
		}
		conv, err := convertVariable(z, deref(z, v), to, single, 0)
		if err != nil {
			return types.Value{}, err
		}
		types.AssignRef(z, v, conv)
		types.ReleaseValue(z, &conv)
	}
	return newStr(z, charsetNames[src])
}

// maxConvertDepth borne la descente dans les tableaux imbriqués ; PHP refuse au même endroit les références récursives.
const maxConvertDepth = 256

func collectStrings(z *zone.Zone, v types.Value, out *[]string, depth int) {
	switch v.Type {
	case types.TypeString:
		*out = append(*out, types.StringData(z, v))
	case types.TypeArray:
		if depth >= maxConvertDepth {
			return
		}
		types.ArrayForEach(z, v, func(_, item types.Value) bool {
			collectStrings(z, deref(z, item), out, depth+1)
			return true
		})
	}
}

// convertVariable rend une copie de v où chaque chaîne est convertie ; les autres valeurs sont reprises telles quelles.
func convertVariable(z *zone.Zone, v types.Value, to charset, from []charset, depth int) (types.Value, error) {
	switch v.Type {
	case types.TypeString:
		return newStr(z, mbConvertString(types.StringData(z, v), to, from, zoneSubst(z)))
	case types.TypeArray:
		if depth >= maxConvertDepth {
			return types.Value{}, Error{"mb_convert_variables(): Cannot handle recursive references"}
		}
		out, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		var ferr error
		types.ArrayForEach(z, v, func(k, item types.Value) bool {
			var conv types.Value
			if conv, ferr = convertVariable(z, deref(z, item), to, from, depth+1); ferr != nil {
				return false
			}
			ferr = types.ArraySet(z, &out, deref(z, k), conv, nil)
			types.ReleaseValue(z, &conv)
			return ferr == nil
		})
		if ferr != nil {
			return types.Value{}, ferr
		}
		return out, nil
	default:
		return types.RetainValue(z, v), nil
	}
}
