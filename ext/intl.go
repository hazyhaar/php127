package ext

import (
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Formes de Normalizer (valeurs de PHP intl) : FORM_D = NFD, FORM_KD = NFKD, FORM_C = NFC, FORM_KC = NFKC.
const (
	normFormD  = 2
	normFormKD = 3
	normFormC  = 4
	normFormKC = 5
)

// normTables est la forme décodée de normalizer_table_gen.go, construite au premier usage.
type normTables struct {
	canon   map[rune][]rune // décomposition canonique d'un niveau
	compat  map[rune][]rune // décomposition de compatibilité d'un niveau (canoniques comprises)
	ccc     map[rune]uint8
	compose map[[2]rune]rune
}

var (
	normOnce sync.Once
	norm     normTables
)

func parseHexRune(s string) rune {
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		panic("ext: table de normalisation : " + s)
	}
	return rune(n)
}

func loadNormTables() {
	norm = normTables{
		canon:   make(map[rune][]rune, 2100),
		compat:  make(map[rune][]rune, 5800),
		ccc:     make(map[rune]uint8, 950),
		compose: make(map[[2]rune]rune, 1000),
	}
	excluded := make(map[rune]bool, 100)
	for line := range strings.SplitSeq(normExcl, "\n") {
		excluded[parseHexRune(line)] = true
	}
	for line := range strings.SplitSeq(normDecomp, "\n") {
		f := strings.SplitN(line, ";", 3)
		cp := parseHexRune(f[0])
		var d []rune
		for p := range strings.FieldsSeq(f[2]) {
			d = append(d, parseHexRune(p))
		}
		norm.compat[cp] = d
		if f[1] == "C" {
			norm.canon[cp] = d
			if len(d) == 2 && !excluded[cp] {
				norm.compose[[2]rune{d[0], d[1]}] = cp
			}
		}
	}
	for line := range strings.SplitSeq(normCCC, "\n") {
		f := strings.SplitN(line, ";", 2)
		c, _ := strconv.Atoi(f[1])
		norm.ccc[parseHexRune(f[0])] = uint8(c)
	}
}

// Syllabes hangûl, décomposées et composées par calcul (Unicode §3.12).
const (
	hangulS = 0xac00
	hangulL = 0x1100
	hangulV = 0x1161
	hangulT = 0x11a7
	hangulN = 21 * 28
	hangulC = 19 * hangulN
)

func decomposeRune(dst []rune, r rune, compat bool) []rune {
	if r >= hangulS && r < hangulS+hangulC {
		s := r - hangulS
		dst = append(dst, hangulL+s/hangulN, hangulV+(s%hangulN)/28)
		if t := s % 28; t != 0 {
			dst = append(dst, hangulT+t)
		}
		return dst
	}
	table := norm.canon
	if compat {
		table = norm.compat
	}
	d, ok := table[r]
	if !ok {
		return append(dst, r)
	}
	for _, x := range d {
		dst = decomposeRune(dst, x, compat)
	}
	return dst
}

// normalizeRunes applique la forme : décomposition complète, ordre canonique, puis composition pour C et KC.
func normalizeRunes(in []rune, form int) []rune {
	normOnce.Do(loadNormTables)
	compat := form == normFormKD || form == normFormKC
	out := make([]rune, 0, len(in))
	for _, r := range in {
		out = decomposeRune(out, r, compat)
	}
	// Ordre canonique : tri stable de chaque suite de marques de classe non nulle.
	for i := 0; i < len(out); {
		if norm.ccc[out[i]] == 0 {
			i++
			continue
		}
		j := i
		for j < len(out) && norm.ccc[out[j]] != 0 {
			j++
		}
		slices.SortStableFunc(out[i:j], func(a, b rune) int { return int(norm.ccc[a]) - int(norm.ccc[b]) })
		i = j
	}
	if form == normFormD || form == normFormKD {
		return out
	}
	return composeRunes(out)
}

func composePair(a, b rune) (rune, bool) {
	if a >= hangulL && a < hangulL+19 && b >= hangulV && b < hangulV+21 {
		return hangulS + ((a-hangulL)*21+(b-hangulV))*28, true
	}
	if a >= hangulS && a < hangulS+hangulC && (a-hangulS)%28 == 0 && b > hangulT && b < hangulT+28 {
		return a + (b - hangulT), true
	}
	c, ok := norm.compose[[2]rune{a, b}]
	return c, ok
}

// composeRunes suit l'algorithme de composition canonique : une marque se compose avec le dernier initial
// si aucune marque intermédiaire de classe nulle ou supérieure ou égale ne la bloque.
func composeRunes(s []rune) []rune {
	if len(s) == 0 {
		return s
	}
	starter := -1
	if norm.ccc[s[0]] == 0 {
		starter = 0
	}
	out := s[:1]
	lastCCC := int(norm.ccc[s[0]])
	if starter == 0 {
		lastCCC = 0
	}
	for _, r := range s[1:] {
		cc := int(norm.ccc[r])
		if starter >= 0 {
			blocked := len(out)-1 > starter && (lastCCC == 0 || lastCCC >= cc)
			if !blocked {
				if c, ok := composePair(out[starter], r); ok {
					out[starter] = c
					continue
				}
			}
		}
		if cc == 0 {
			starter = len(out)
		}
		lastCCC = cc
		out = append(out, r)
	}
	return out
}

// normalizeString rend s normalisée ; faux si s n'est pas de l'UTF-8 valide.
func normalizeString(s string, form int) (string, bool) {
	if !utf8.ValidString(s) {
		return "", false
	}
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s, true
	}
	return string(normalizeRunes([]rune(s), form)), true
}

func normFormArg(z *zone.Zone, args []types.Value, fn string) (int, bool, error) {
	form, err := optInt(z, args, 1, normFormC, fn, "2", "form")
	if err != nil {
		return 0, false, err
	}
	switch form {
	case normFormD, normFormKD, normFormC, normFormKC:
		return int(form), true, nil
	}
	return 0, false, nil
}

// nativeNormalizerNormalize rend false pour de l'UTF-8 invalide ou une forme non servie (dont FORM_KC_CF).
func nativeNormalizerNormalize(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("normalizer_normalize", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	in := deref(z, args[0])
	s, err := asString(z, in, "normalizer_normalize", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	form, ok, err := normFormArg(z, args, "normalizer_normalize")
	if err != nil || !ok {
		return types.NewBool(false), err
	}
	out, ok := normalizeString(s, form)
	if !ok {
		return types.NewBool(false), nil
	}
	v, err := newStr(z, out)
	if err != nil {
		return types.Value{}, err
	}
	return taint(v, in), nil
}

func nativeNormalizerIsNormalized(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("normalizer_is_normalized", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "normalizer_is_normalized", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	form, ok, err := normFormArg(z, args, "normalizer_is_normalized")
	if err != nil || !ok {
		return types.NewBool(false), err
	}
	out, ok := normalizeString(s, form)
	return types.NewBool(ok && out == s), nil
}

// ---- IDNA (UTS #46) ----

// Constantes IDNA_* et INTL_IDNA_VARIANT_UTS46 de PHP intl.
const (
	idnaDefault                = 0
	idnaAllowUnassigned        = 1
	idnaUseSTD3Rules           = 2
	idnaCheckBidi              = 4
	idnaCheckContextJ          = 8
	idnaNontransitionalToASCII = 16
	idnaNontransitionalToUni   = 32
	intlIDNAVariantUTS46       = 1
)

// Erreurs UIDNA_ERROR_* rapportées dans $idna_info['errors'].
const (
	idnaErrEmptyLabel     = 0x1
	idnaErrLabelTooLong   = 0x2
	idnaErrDomainTooLong  = 0x4
	idnaErrLeadingHyphen  = 0x8
	idnaErrTrailingHyphen = 0x10
	idnaErrHyphen34       = 0x20
	idnaErrLeadingMark    = 0x40
	idnaErrDisallowed     = 0x80
	idnaErrPunycode       = 0x100
	idnaErrInvalidACE     = 0x400
	idnaErrBidi           = 0x800
	idnaErrContextJ       = 0x1000
)

// Paramètres de Punycode, RFC 3492 §5.
const (
	punyBase, punyTMin, punyTMax, punySkew, punyDamp = 36, 1, 26, 38, 700
	punyInitialBias, punyInitialN                    = 72, 128
)

func punycodeAdapt(delta, numPoints int, first bool) int {
	if first {
		delta /= punyDamp
	} else {
		delta /= 2
	}
	delta += delta / numPoints
	k := 0
	for delta > ((punyBase-punyTMin)*punyTMax)/2 {
		delta /= punyBase - punyTMin
		k += punyBase
	}
	return k + (punyBase-punyTMin+1)*delta/(delta+punySkew)
}

func punycodeThreshold(k, bias int) int {
	return min(max(k-bias, punyTMin), punyTMax)
}

// punycodeEncode suit RFC 3492 §6.3 ; faux en cas de débordement.
func punycodeEncode(in []rune) (string, bool) {
	digit := func(d int) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
	var b strings.Builder
	for _, r := range in {
		if r < 0x80 {
			b.WriteByte(byte(r))
		}
	}
	h := b.Len()
	basic := h
	if basic > 0 {
		b.WriteByte('-')
	}
	n, delta, bias := punyInitialN, 0, punyInitialBias
	for h < len(in) {
		m := int(unicode.MaxRune) + 1
		for _, r := range in {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		if (m - n) > (1<<31-1-delta)/(h+1) {
			return "", false
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, r := range in {
			if int(r) < n {
				delta++
			}
			if int(r) == n {
				q := delta
				for k := punyBase; ; k += punyBase {
					t := punycodeThreshold(k, bias)
					if q < t {
						break
					}
					b.WriteByte(digit(t + (q-t)%(punyBase-t)))
					q = (q - t) / (punyBase - t)
				}
				b.WriteByte(digit(q))
				bias = punycodeAdapt(delta, h+1, h == basic)
				delta = 0
				h++
			}
		}
		delta++
		n++
	}
	return b.String(), true
}

// punycodeDecode suit RFC 3492 §6.2 : les points de code avant le dernier délimiteur sont recopiés, la
// suite est décodée par insertions. Faux pour une entrée non ASCII, un chiffre invalide, une entrée
// tronquée, un débordement ou un point de code hors Unicode.
func punycodeDecode(s string) ([]rune, bool) {
	const maxInt32 = 1<<31 - 1
	var out []rune
	in := 0
	if b := strings.LastIndexByte(s, '-'); b > 0 {
		for i := range b {
			if s[i] >= 0x80 {
				return nil, false
			}
			out = append(out, rune(s[i]))
		}
		in = b + 1
	}
	n, i, bias := punyInitialN, 0, punyInitialBias
	for in < len(s) {
		oldi, w := i, 1
		for k := punyBase; ; k += punyBase {
			if in >= len(s) {
				return nil, false
			}
			c := s[in]
			in++
			var d int
			switch {
			case c >= 'a' && c <= 'z':
				d = int(c - 'a')
			case c >= 'A' && c <= 'Z':
				d = int(c - 'A')
			case c >= '0' && c <= '9':
				d = int(c-'0') + 26
			default:
				return nil, false
			}
			if d > (maxInt32-i)/w {
				return nil, false
			}
			i += d * w
			t := punycodeThreshold(k, bias)
			if d < t {
				break
			}
			if w > maxInt32/(punyBase-t) {
				return nil, false
			}
			w *= punyBase - t
		}
		count := len(out) + 1
		bias = punycodeAdapt(i-oldi, count, oldi == 0)
		if i/count > maxInt32-n {
			return nil, false
		}
		n += i / count
		i %= count
		if n > unicode.MaxRune || n >= 0xd800 && n <= 0xdfff {
			return nil, false
		}
		out = slices.Insert(out, i, rune(n))
		i++
	}
	return out, true
}

// idnaMapLabel approche la correspondance d'UTS #46 par minuscules et NFKC (« Ⅷ » → « viii », pleine
// chasse → ASCII), puis, en mode transitionnel, ß → ss, ς → σ et suppression de ZWJ/ZWNJ, et rend la
// forme NFC. La table de correspondance complète d'UTS #46 n'est pas embarquée.
func idnaMapLabel(label string, nontransitional bool) []rune {
	folded := normalizeRunes([]rune(strings.ToLower(label)), normFormKC)
	rs := make([]rune, 0, len(folded))
	for _, r := range folded {
		r = unicode.ToLower(r)
		if !nontransitional {
			switch r {
			case 'ß':
				rs = append(rs, 's', 's')
				continue
			case 'ς':
				r = 'σ'
			case 0x200c, 0x200d:
				continue
			}
		}
		rs = append(rs, r)
	}
	return normalizeRunes(rs, normFormC)
}

// idnaToASCII rend la forme ASCII et le masque d'erreurs UIDNA_ERROR_*. Un label qui commence, après
// correspondance, par « xn-- » est décodé et validé sous sa forme Unicode, puis rendu inchangé.
func idnaToASCII(domain string, flags int64) (string, uint32) {
	var errs uint32
	if !utf8.ValidString(domain) {
		return domain, idnaErrDisallowed
	}
	// Séparateurs de libellés d'UTS #46 : point idéographique et points pleine chasse.
	domain = strings.NewReplacer("。", ".", "．", ".", "｡", ".").Replace(domain)
	labels := strings.Split(domain, ".")
	out := make([]string, len(labels))
	bidiDomain, bidiOK := false, true
	for i, label := range labels {
		if label == "" {
			if i != len(labels)-1 || len(labels) == 1 {
				errs |= idnaErrEmptyLabel
			}
			continue
		}
		rs := idnaMapLabel(label, flags&idnaNontransitionalToASCII != 0)
		u := string(rs)
		var enc string
		if strings.HasPrefix(u, "xn--") {
			dec, ok := punycodeDecode(u[4:])
			if !ok || isASCIIRunes(dec) {
				errs |= idnaErrPunycode
				out[i] = u
				continue
			}
			if !slices.Equal(normalizeRunes(dec, normFormC), dec) {
				errs |= idnaErrInvalidACE
			}
			rs, enc = dec, u
		}
		errs |= idnaLabelErrors(rs, flags)
		if flags&idnaCheckBidi != 0 {
			rtl, ok := idnaLabelBidi(rs)
			bidiDomain = bidiDomain || rtl
			bidiOK = bidiOK && ok
		}
		if enc == "" {
			enc = u
			if !isASCIIRunes(rs) {
				p, ok := punycodeEncode(rs)
				if !ok {
					errs |= idnaErrPunycode
				}
				enc = "xn--" + p
			}
		}
		if len(enc) > 63 {
			errs |= idnaErrLabelTooLong
		}
		out[i] = enc
	}
	if bidiDomain && !bidiOK {
		errs |= idnaErrBidi
	}
	res := strings.Join(out, ".")
	if n := len(strings.TrimSuffix(res, ".")); n > 253 {
		errs |= idnaErrDomainTooLong
	}
	return res, errs
}

func isASCIIRunes(rs []rune) bool {
	for _, r := range rs {
		if r >= 0x80 {
			return false
		}
	}
	return true
}

// idnaLabelErrors applique à un label sous forme Unicode les critères de validité d'UTS #46 §4.1 :
// règles STD3, tirets, marque initiale et, sur drapeau, ContextJ (RFC 5892, annexes A.1 et A.2).
func idnaLabelErrors(rs []rune, flags int64) uint32 {
	var errs uint32
	if flags&idnaUseSTD3Rules != 0 {
		for _, r := range rs {
			if r < 0x80 && !isLDH(r) {
				errs |= idnaErrDisallowed
			}
		}
	}
	if len(rs) > 0 && unicode.In(rs[0], unicode.Mn, unicode.Mc, unicode.Me) {
		errs |= idnaErrLeadingMark
	}
	if len(rs) > 0 && rs[0] == '-' {
		errs |= idnaErrLeadingHyphen
	}
	if len(rs) > 0 && rs[len(rs)-1] == '-' {
		errs |= idnaErrTrailingHyphen
	}
	if len(rs) >= 4 && rs[2] == '-' && rs[3] == '-' {
		errs |= idnaErrHyphen34
	}
	if flags&idnaCheckContextJ != 0 && !idnaContextJOK(rs) {
		errs |= idnaErrContextJ
	}
	return errs
}

// Classes directionnelles (Bidi_Class) retenues par RFC 5893 ; bidiOther couvre B, S, WS et les
// contrôles d'enchâssement et d'isolement, admis dans aucun label.
const (
	bidiL uint8 = iota
	bidiR
	bidiAL
	bidiEN
	bidiES
	bidiCS
	bidiET
	bidiAN
	bidiON
	bidiBN
	bidiNSM
	bidiOther
)

var bidiClassNames = map[string]uint8{
	"R": bidiR, "AL": bidiAL, "EN": bidiEN, "ES": bidiES, "CS": bidiCS, "ET": bidiET, "AN": bidiAN,
	"ON": bidiON, "BN": bidiBN, "NSM": bidiNSM,
}

// idnaRange associe une valeur de propriété à une plage de points de code.
type idnaRange struct {
	lo, hi rune
	v      uint8
}

var (
	idnaTablesOnce sync.Once
	idnaBidi       []idnaRange // Bidi_Class hors L
	idnaJoining    []idnaRange // Joining_Type hors U, v étant la lettre D, R, L, C ou T
)

func parseIdnaRanges(src string, value func(string) uint8) []idnaRange {
	var out []idnaRange
	for line := range strings.SplitSeq(src, "\n") {
		f := strings.SplitN(line, ";", 3)
		out = append(out, idnaRange{parseHexRune(f[0]), parseHexRune(f[1]), value(f[2])})
	}
	return out
}

func loadIdnaTables() {
	idnaBidi = parseIdnaRanges(idnaBidiRanges, func(c string) uint8 {
		if v, ok := bidiClassNames[c]; ok {
			return v
		}
		return bidiOther
	})
	idnaJoining = parseIdnaRanges(idnaJoiningRanges, func(t string) uint8 { return t[0] })
}

// lookupIdnaRange rend la valeur de la plage qui contient r, ou def hors de toute plage.
func lookupIdnaRange(table []idnaRange, r rune, def uint8) uint8 {
	i, found := slices.BinarySearchFunc(table, r, func(e idnaRange, r rune) int {
		switch {
		case e.hi < r:
			return -1
		case e.lo > r:
			return 1
		}
		return 0
	})
	if !found {
		return def
	}
	return table[i].v
}

func bidiClassOf(r rune) uint8 {
	idnaTablesOnce.Do(loadIdnaTables)
	return lookupIdnaRange(idnaBidi, r, bidiL)
}

// joiningTypeOf rend le Joining_Type de r (D, R, L, C ou T), U pour une rune non joignante.
func joiningTypeOf(r rune) uint8 {
	idnaTablesOnce.Do(loadIdnaTables)
	return lookupIdnaRange(idnaJoining, r, 'U')
}

// isVirama reconnaît une rune de classe combinatoire canonique Virama (9).
func isVirama(r rune) bool {
	normOnce.Do(loadNormTables)
	return norm.ccc[r] == 9
}

// idnaContextJOK applique les règles contextuelles de RFC 5892 : U+200D suit un Virama ; U+200C suit un
// Virama ou s'inscrit dans (Joining_Type:{L,D})(Joining_Type:T)* U+200C (Joining_Type:T)*(Joining_Type:{R,D}).
func idnaContextJOK(rs []rune) bool {
	for i, r := range rs {
		switch r {
		case 0x200d:
			if i == 0 || !isVirama(rs[i-1]) {
				return false
			}
		case 0x200c:
			if i > 0 && isVirama(rs[i-1]) {
				continue
			}
			j := i - 1
			for j >= 0 && joiningTypeOf(rs[j]) == 'T' {
				j--
			}
			if j < 0 {
				return false
			}
			if t := joiningTypeOf(rs[j]); t != 'L' && t != 'D' {
				return false
			}
			j = i + 1
			for j < len(rs) && joiningTypeOf(rs[j]) == 'T' {
				j++
			}
			if j == len(rs) {
				return false
			}
			if t := joiningTypeOf(rs[j]); t != 'R' && t != 'D' {
				return false
			}
		}
	}
	return true
}

// idnaLabelBidi rend rtl vrai si le label contient une rune R, AL ou AN (label RTL de RFC 5893), et ok
// vrai si le label satisfait les six conditions de RFC 5893 §2. Comme dans ICU, un label dont la première
// rune n'est pas L suit les conditions des labels RTL.
func idnaLabelBidi(rs []rune) (rtl, ok bool) {
	if len(rs) == 0 {
		return false, true
	}
	const (
		mL, mR, mAL, mEN, mAN, mNSM = 1 << bidiL, 1 << bidiR, 1 << bidiAL, 1 << bidiEN, 1 << bidiAN, 1 << bidiNSM
		rtlMask                     = mR | mAL | mAN
		ltrAllowed                  = mL | mEN | 1<<bidiES | 1<<bidiCS | 1<<bidiET | 1<<bidiON | 1<<bidiBN | mNSM
		rtlAllowed                  = mR | mAL | mAN | mEN | 1<<bidiES | 1<<bidiCS | 1<<bidiET | 1<<bidiON | 1<<bidiBN | mNSM
	)
	var mask uint32
	for _, r := range rs {
		mask |= 1 << bidiClassOf(r)
	}
	first := uint32(1) << bidiClassOf(rs[0])
	last := first
	for i := len(rs) - 1; i > 0; i-- {
		if c := uint32(1) << bidiClassOf(rs[i]); c != mNSM {
			last = c
			break
		}
	}
	ok = first&(mL|mR|mAL) != 0
	if first == mL {
		ok = ok && last&(mL|mEN) != 0 && mask&^ltrAllowed == 0
	} else {
		ok = ok && last&(mR|mAL|mEN|mAN) != 0 && mask&^rtlAllowed == 0 && mask&(mEN|mAN) != mEN|mAN
	}
	return mask&rtlMask != 0, ok
}

// nativeIdnToASCII rend false si le domaine est vide ou si une erreur UTS #46 est relevée ; $idna_info,
// passé par référence, reçoit result, isTransitionalDifferent et errors. IDNA_CHECK_BIDI applique RFC 5893
// à un domaine qui contient un label RTL, IDNA_CHECK_CONTEXTJ applique RFC 5892 à U+200C et U+200D.
func nativeIdnToASCII(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "idn_to_ascii"
	if err := arity(fn, len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	in := deref(z, args[0])
	domain, err := asString(z, in, fn, "1", "domain")
	if err != nil {
		return types.Value{}, err
	}
	flags, err := optInt(z, args, 1, idnaDefault, fn, "2", "flags")
	if err != nil {
		return types.Value{}, err
	}
	variant, err := optInt(z, args, 2, intlIDNAVariantUTS46, fn, "3", "variant")
	if err != nil {
		return types.Value{}, err
	}
	if variant != intlIDNAVariantUTS46 {
		return types.Value{}, ValueError{fn + "(): Argument #3 ($variant) must be INTL_IDNA_VARIANT_UTS46"}
	}
	if domain == "" {
		return types.NewBool(false), nil
	}
	res, errs := idnaToASCII(domain, flags)
	if info := arg(args, 3); info.Type == types.TypeReference {
		trans, transErrs := idnaToASCII(domain, flags^idnaNontransitionalToASCII)
		a, err := idnaInfo(z, res, trans != res || transErrs != errs, errs, in)
		if err != nil {
			return types.Value{}, err
		}
		writeRef(z, info, a)
		types.ReleaseValue(z, &a)
	}
	if errs != 0 {
		return types.NewBool(false), nil
	}
	v, err := newStr(z, res)
	if err != nil {
		return types.Value{}, err
	}
	return taint(v, in), nil
}

func idnaInfo(z *zone.Zone, res string, transDiff bool, errs uint32, sources ...types.Value) (types.Value, error) {
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	r, err := newStr(z, res)
	if err != nil {
		return types.Value{}, err
	}
	for _, src := range sources {
		r = taint(r, src)
	}
	if err = setStrKey(z, &a, "result", r); err != nil {
		return types.Value{}, err
	}
	if err = setStrKey(z, &a, "isTransitionalDifferent", types.NewBool(transDiff)); err != nil {
		return types.Value{}, err
	}
	if err = setStrKey(z, &a, "errors", types.NewLong(int64(errs))); err != nil {
		return types.Value{}, err
	}
	return a, nil
}

// isLDH reconnaît les lettres minuscules, chiffres et trait d'union admis par les règles STD3.
func isLDH(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-'
}
