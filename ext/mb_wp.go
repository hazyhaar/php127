package ext

import (
	"io"
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// mbSubstMode reproduit current_filter_illegal_mode de mbstring ; la valeur nulle est le mode caractère.
type mbSubstMode uint8

const (
	mbSubstChar mbSubstMode = iota
	mbSubstNone
	mbSubstLong
	mbSubstEntity
)

// mbSubst est le réglage mb_substitute_character d'une Zone, copié par valeur.
type mbSubst struct {
	mode mbSubstMode
	ch   rune
}

func zoneSubst(z *zone.Zone) mbSubst {
	st := extStateFor(z)
	return mbSubst{st.mbSubstMode, st.mbSubstChar}
}

// invalid remplace une séquence d'octets invalide : le caractère de substitution (ou « ? » s'il n'est pas
// représentable dans to), rien en mode none, « ? » en modes long et entity (mesuré sur PHP 8.3.6).
func (s mbSubst) invalid(b *strings.Builder, to charset) {
	switch s.mode {
	case mbSubstNone:
	case mbSubstChar:
		if !encodeRune(b, s.ch, to) {
			b.WriteByte('?')
		}
	default:
		b.WriteByte('?')
	}
}

// unrepresentable remplace un point de code valide absent de to : « U+20AC » en mode long, « &#x20AC; » en mode entity.
func (s mbSubst) unrepresentable(b *strings.Builder, r rune, to charset) {
	switch s.mode {
	case mbSubstNone:
	case mbSubstLong:
		b.WriteString("U+")
		b.WriteString(strings.ToUpper(strconv.FormatInt(int64(r), 16)))
	case mbSubstEntity:
		b.WriteString("&#x")
		b.WriteString(strings.ToUpper(strconv.FormatInt(int64(r), 16)))
		b.WriteByte(';')
	default:
		s.invalid(b, to)
	}
}

func validCodepoint(n int64) bool {
	return n >= 0 && n < 0x110000 && (n < 0xd800 || n > 0xdfff)
}

// nativeMBSubstituteCharacter lit ou pose le réglage de la Zone ; les noms sont comparés sans casse,
// un booléen ou un flottant devient un entier comme sous Z_PARAM_STR_OR_LONG_OR_NULL.
func nativeMBSubstituteCharacter(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "mb_substitute_character"
	if err := arity(fn, len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	st := extStateFor(z)
	var v types.Value
	if len(args) == 1 {
		v = deref(z, args[0])
	}
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		switch st.mbSubstMode {
		case mbSubstNone:
			return newStr(z, "none")
		case mbSubstLong:
			return newStr(z, "long")
		case mbSubstEntity:
			return newStr(z, "entity")
		}
		return types.NewLong(int64(st.mbSubstChar)), nil
	case types.TypeString:
		switch strings.ToLower(types.StringData(z, v)) {
		case "none":
			st.mbSubstMode = mbSubstNone
		case "long":
			st.mbSubstMode = mbSubstLong
		case "entity":
			st.mbSubstMode = mbSubstEntity
		default:
			return types.Value{}, ValueError{fn + `(): Argument #1 ($substitute_character) must be "none", "long", "entity" or a valid codepoint`}
		}
		return types.NewBool(true), nil
	case types.TypeLong, types.TypeDouble, types.TypeFalse, types.TypeTrue:
		n, err := asInt(z, v, fn, "1", "substitute_character")
		if err != nil {
			return types.Value{}, err
		}
		if !validCodepoint(n) {
			return types.Value{}, ValueError{fn + "(): Argument #1 ($substitute_character) is not a valid codepoint"}
		}
		st.mbSubstMode, st.mbSubstChar = mbSubstChar, rune(n)
		return types.NewBool(true), nil
	default:
		return types.Value{}, typeErr(fn, "1", "substitute_character", "string|int|null", typeKind(z, v))
	}
}

// mbScrubEncoding résout l'encodage de mb_scrub ; « 8bit » (et ses alias) ne connaît aucune séquence invalide.
func mbScrubEncoding(z *zone.Zone, args []types.Value, i int, fn string) (cs charset, identity bool, err error) {
	name := extStateFor(z).mbInternalEncoding
	if len(args) > i && !missing(deref(z, args[i])) {
		if name, err = asString(z, args[i], fn, idxOf(i), "encoding"); err != nil {
			return 0, false, err
		}
	}
	canon, ok := mbInternalNames[foldEnc(name)]
	if !ok {
		return 0, false, ValueError{fn + "(): Argument #" + idxOf(i) + " ($encoding) must be a valid encoding, \"" + name + "\" given"}
	}
	switch canon {
	case "8bit":
		return 0, true, nil
	case "7bit":
		return csASCII, false, nil
	}
	cs, _ = lookupCharset(canon)
	return cs, false, nil
}

// taint reporte sur out la provenance non fiable de in : une chaîne dérivée d'une entrée souillée l'est aussi.
func taint(out, in types.Value) types.Value {
	out.Flags |= in.Flags & types.FlagTainted
	return out
}

// nativeMBScrub est mb_convert_encoding($string, $encoding, $encoding) : chaque séquence invalide reçoit
// le caractère de substitution de la Zone.
func nativeMBScrub(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_scrub", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	in := deref(z, args[0])
	s, err := asString(z, in, "mb_scrub", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	cs, identity, err := mbScrubEncoding(z, args, 1, "mb_scrub")
	if err != nil {
		return types.Value{}, err
	}
	if !identity {
		s = mbConvertString(s, cs, []charset{cs}, zoneSubst(z))
	}
	out, err := newStr(z, s)
	if err != nil {
		return types.Value{}, err
	}
	return taint(out, in), nil
}

// nativeMBDecodeMimeheader décode les mots encodés RFC 2047 vers l'encodage interne de la Zone, selon les
// règles mesurées sur PHP 8.3.6 : un saut de ligne suivi d'un blanc disparaît, un saut de ligne isolé devient
// une espace, le pliage entre deux mots encodés disparaît avec ses blancs, un mot non terminé court jusqu'à la
// fin, un mot de jeu de caractères inconnu reste tel quel ; les octets invalides deviennent « ? ».
func nativeMBDecodeMimeheader(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mb_decode_mimeheader", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	in := deref(z, args[0])
	s, err := asString(z, in, "mb_decode_mimeheader", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	to, ok := lookupCharset(extStateFor(z).mbInternalEncoding)
	if !ok {
		to = csUTF8
	}
	out, err := newStr(z, mimeHeaderDecode(s, to))
	if err != nil {
		return types.Value{}, err
	}
	return taint(out, in), nil
}

func isNL(c byte) bool  { return c == '\r' || c == '\n' }
func isWSP(c byte) bool { return c == ' ' || c == '\t' }

func mimeHeaderDecode(s string, to charset) string {
	var b strings.Builder
	b.Grow(len(s))
	question := mbSubst{mode: mbSubstChar, ch: '?'}
	for i := 0; i < len(s); {
		c := s[i]
		if c == '=' && i+1 < len(s) && s[i+1] == '?' {
			if text, cs, n := mimeEncodedWord(s[i:]); n > 0 {
				b.WriteString(mbConvertString(text, to, []charset{cs}, question))
				i += n
				// Pliage entre deux mots encodés : sauts de ligne, puis blancs, puis « =? ».
				j := i
				for j < len(s) && isNL(s[j]) {
					j++
				}
				if j > i && j < len(s) && isWSP(s[j]) {
					for j < len(s) && isWSP(s[j]) {
						j++
					}
					if _, _, m := mimeEncodedWord(s[j:]); m > 0 {
						i = j
					}
				}
				continue
			}
		}
		if isNL(c) {
			j := i
			for j < len(s) && isNL(s[j]) {
				j++
			}
			if j < len(s) && !isWSP(s[j]) {
				b.WriteByte(' ')
			}
			i = j
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// mimeEncodedWord lit « =?charset?B|Q?texte?= » en tête de s ; n vaut zéro si la forme ou le jeu de
// caractères n'est pas reconnu. Sans « ?= » final, le texte court jusqu'à la fin de s.
func mimeEncodedWord(s string) (text string, cs charset, n int) {
	if len(s) < 2 || s[0] != '=' || s[1] != '?' {
		return "", 0, 0
	}
	q1 := strings.IndexByte(s[2:], '?')
	if q1 <= 0 || 2+q1+2 >= len(s) || s[2+q1+2] != '?' {
		return "", 0, 0
	}
	cs, ok := lookupCharset(s[2 : 2+q1])
	if !ok {
		return "", 0, 0
	}
	mode := s[2+q1+1]
	start := 2 + q1 + 3
	payload, end := s[start:], len(s)
	if k := strings.Index(payload, "?="); k >= 0 {
		payload, end = payload[:k], start+k+2
	}
	switch mode {
	case 'B', 'b':
		return mimeBase64(payload), cs, end
	case 'Q', 'q':
		return mimeQ(payload), cs, end
	}
	return "", 0, 0
}

// mimeBase64 décode sans exiger de bourrage et ignore tout caractère hors de l'alphabet.
func mimeBase64(p string) string {
	alpha := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		if b64Val(p[i]) >= 0 {
			alpha = append(alpha, p[i])
		}
	}
	return decodeAlphabet(alpha)
}

// mimeQ décode l'encodage Q : « _ » vaut une espace ; « =XY » non hexadécimal rend « = » et saute XY ;
// un « = » à moins de deux octets de la fin reste littéral.
func mimeQ(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case c == '_':
			b.WriteByte(' ')
		case c == '=' && i+2 < len(p):
			if isHex(p[i+1]) && isHex(p[i+2]) {
				b.WriteByte(unhex(p[i+1])<<4 | unhex(p[i+2]))
			} else {
				b.WriteByte('=')
			}
			i += 2
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
