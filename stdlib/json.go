package stdlib

import (
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	classStdClass          uint32 = 0x5c1a55
	JSON_HEX_TAG                  = 1
	JSON_HEX_AMP                  = 2
	JSON_HEX_APOS                 = 4
	JSON_HEX_QUOT                 = 8
	JSON_FORCE_OBJECT             = 16
	JSON_UNESCAPED_SLASHES        = 64
	JSON_PRETTY_PRINT             = 128
	JSON_UNESCAPED_UNICODE        = 256
	JSON_THROW_ON_ERROR           = 4194304
	jsonDefaultDepth              = 512

	jsonErrorNone        = 0
	jsonErrorDepth       = 1
	jsonErrorSyntax      = 4
	jsonErrorUTF8        = 5
	jsonErrorInfNaN      = 7
	jsonErrorUnsupported = 8
)

// jsonErrorMessages reprend les libellés de json_last_error_msg de PHP 8.3.
var jsonErrorMessages = map[int64]string{
	jsonErrorNone:        "No error",
	jsonErrorDepth:       "Maximum stack depth exceeded",
	jsonErrorSyntax:      "Syntax error",
	jsonErrorUTF8:        "Malformed UTF-8 characters, possibly incorrectly encoded",
	jsonErrorInfNaN:      "Inf and NaN cannot be JSON encoded",
	jsonErrorUnsupported: "Type is not supported",
}

// setJSONError consigne le code de la dernière opération JSON de la Zone.
func setJSONError(z *zone.Zone, code int64) {
	sysStateFor(z).jsonErr = code
}

func nativeJSONLastError(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("json_last_error", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	code := sysStateFor(z).jsonErr
	return types.NewLong(code), nil
}

func nativeJSONLastErrorMsg(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("json_last_error_msg", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	code := sysStateFor(z).jsonErr
	msg, ok := jsonErrorMessages[code]
	if !ok {
		msg = "Unknown error"
	}
	return newStr(z, msg)
}

// jsonEncodeErrorCode rend le code PHP d'un échec d'encodage d'après son libellé.
func jsonEncodeErrorCode(err error) int64 {
	for code, msg := range jsonErrorMessages {
		if code != jsonErrorNone && err.Error() == msg {
			return code
		}
	}
	return jsonErrorUnsupported
}

type prop struct {
	key string
	val types.Value
}

type objKey struct {
	z    uintptr
	slot uint32
	gen  uint16
}

var (
	objMu    sync.Mutex
	objProps = map[objKey][]prop{}
)

func init() {
	types.RegisterObjectClassName(classStdClass, "stdClass")
}

func zoneKey(z *zone.Zone, v types.Value) objKey {
	return objKey{z: uintptr(unsafe.Pointer(z)), slot: v.DataSlot, gen: v.Extra}
}

func objectProps(z *zone.Zone, v types.Value) []prop {
	objMu.Lock()
	defer objMu.Unlock()
	return objProps[zoneKey(z, v)]
}

func setObjectProps(z *zone.Zone, v types.Value, props []prop) {
	objMu.Lock()
	objProps[zoneKey(z, v)] = props
	objMu.Unlock()
}

func nativeJSONEncode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("json_encode", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	flags := int64(0)
	depth := int64(jsonDefaultDepth)
	var err error
	if len(args) >= 2 {
		flags, err = asIntParam(z, args[1], "json_encode", "2", "flags")
		if err != nil {
			return types.Value{}, err
		}
	}
	if len(args) >= 3 {
		depth, err = asIntParam(z, args[2], "json_encode", "3", "depth")
		if err != nil {
			return types.Value{}, err
		}
	}
	var b strings.Builder
	if err = encodeJSON(&b, z, deref(z, args[0]), int(flags), int(depth), 1); err != nil {
		if flags&JSON_THROW_ON_ERROR != 0 {
			return types.Value{}, JsonException{err.Error()}
		}
		setJSONError(z, jsonEncodeErrorCode(err))
		return types.NewBool(false), nil
	}
	if flags&JSON_THROW_ON_ERROR == 0 {
		setJSONError(z, jsonErrorNone)
	}
	return newStr(z, b.String())
}

func encodeJSON(b *strings.Builder, z *zone.Zone, v types.Value, flags, maxDepth, depth int) error {
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		b.WriteString("null")
	case types.TypeTrue:
		b.WriteString("true")
	case types.TypeFalse:
		b.WriteString("false")
	case types.TypeLong:
		b.WriteString(strconv.FormatInt(v.ToLong(), 10))
	case types.TypeDouble:
		f := v.ToDouble()
		if mathIsBad(f) {
			return Error{"Inf and NaN cannot be JSON encoded"}
		}
		b.WriteString(formatFloat(f, floatJSON))
	case types.TypeString:
		s := types.StringData(z, v)
		if flags&JSON_UNESCAPED_UNICODE == 0 && !utf8.ValidString(s) {
			return Error{"Malformed UTF-8 characters, possibly incorrectly encoded"}
		}
		writeJSONString(b, s, flags)
	case types.TypeArray:
		if depth > maxDepth {
			return Error{"Maximum stack depth exceeded"}
		}
		force := flags&JSON_FORCE_OBJECT != 0
		if !force && isList(z, v) {
			b.WriteByte('[')
			first := true
			var encErr error
			types.ArrayForEach(z, v, func(_, val types.Value) bool {
				if !first {
					b.WriteByte(',')
				}
				first = false
				encErr = encodeJSON(b, z, deref(z, val), flags, maxDepth, depth+1)
				return encErr == nil
			})
			if encErr != nil {
				return encErr
			}
			b.WriteByte(']')
			return nil
		}
		b.WriteByte('{')
		first := true
		var encErr error
		types.ArrayForEach(z, v, func(k, val types.Value) bool {
			if !first {
				b.WriteByte(',')
			}
			first = false
			ks, err := castString(z, k)
			if err != nil {
				encErr = err
				return false
			}
			writeJSONString(b, ks, flags)
			b.WriteByte(':')
			if err = encodeJSON(b, z, deref(z, val), flags, maxDepth, depth+1); err != nil {
				encErr = err
				return false
			}
			return true
		})
		if encErr != nil {
			return encErr
		}
		b.WriteByte('}')
	case types.TypeObject:
		if depth > maxDepth {
			return Error{"Maximum stack depth exceeded"}
		}
		b.WriteByte('{')
		props := objectProps(z, v)
		for i, p := range props {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSONString(b, p.key, flags)
			b.WriteByte(':')
			if err := encodeJSON(b, z, deref(z, p.val), flags, maxDepth, depth+1); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return Error{"Type is not supported"}
	}
	return nil
}

func mathIsBad(f float64) bool {
	return math.IsNaN(f) || math.IsInf(f, 0)
}

func writeJSONString(b *strings.Builder, s string, flags int) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		c := s[i]
		switch c {
		case '"':
			if flags&JSON_HEX_QUOT != 0 {
				b.WriteString(`\u0022`)
			} else {
				b.WriteString(`\"`)
			}
			i++
		case '\\':
			b.WriteString(`\\`)
			i++
		case '/':
			if flags&JSON_UNESCAPED_SLASHES != 0 {
				b.WriteByte('/')
			} else {
				b.WriteString(`\/`)
			}
			i++
		case '\b':
			b.WriteString(`\b`)
			i++
		case '\f':
			b.WriteString(`\f`)
			i++
		case '\n':
			b.WriteString(`\n`)
			i++
		case '\r':
			b.WriteString(`\r`)
			i++
		case '\t':
			b.WriteString(`\t`)
			i++
		case '<':
			if flags&JSON_HEX_TAG != 0 {
				b.WriteString(`\u003C`)
			} else {
				b.WriteByte(c)
			}
			i++
		case '&':
			if flags&JSON_HEX_AMP != 0 {
				b.WriteString(`\u0026`)
			} else {
				b.WriteByte(c)
			}
			i++
		case '\'':
			if flags&JSON_HEX_APOS != 0 {
				b.WriteString(`\u0027`)
			} else {
				b.WriteByte(c)
			}
			i++
		default:
			if c < 0x20 {
				b.WriteString(`\u00`)
				const hex = "0123456789abcdef"
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&0x0f])
				i++
				continue
			}
			if flags&JSON_UNESCAPED_UNICODE != 0 || c < 0x80 {
				b.WriteByte(c)
				i++
				continue
			}
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				b.WriteString(`\u00`)
				const hex = "0123456789abcdef"
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&0x0f])
				i++
				continue
			}
			writeRuneEscape(b, r)
			i += size
		}
	}
	b.WriteByte('"')
}

func writeRuneEscape(b *strings.Builder, r rune) {
	if r > 0xFFFF {
		r -= 0x10000
		hi := 0xD800 + (r >> 10)
		lo := 0xDC00 + (r & 0x3FF)
		writeU4(b, uint16(hi))
		writeU4(b, uint16(lo))
		return
	}
	writeU4(b, uint16(r))
}

func writeU4(b *strings.Builder, u uint16) {
	const hex = "0123456789abcdef"
	b.WriteString(`\u`)
	b.WriteByte(hex[u>>12])
	b.WriteByte(hex[(u>>8)&0xF])
	b.WriteByte(hex[(u>>4)&0xF])
	b.WriteByte(hex[u&0xF])
}

func nativeJSONDecode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("json_decode", len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "json_decode", "1", "json")
	if err != nil {
		return types.Value{}, err
	}
	assoc := false
	if len(args) >= 2 {
		assoc = asBool(z, args[1])
	}
	depth := int64(jsonDefaultDepth)
	if len(args) >= 3 && deref(z, args[2]).Type != types.TypeNull && !deref(z, args[2]).IsUndef() {
		depth, err = asIntParam(z, args[2], "json_decode", "3", "depth")
		if err != nil {
			return types.Value{}, err
		}
	}
	flags := int64(0)
	if len(args) >= 4 {
		flags, err = asIntParam(z, args[3], "json_decode", "4", "flags")
		if err != nil {
			return types.Value{}, err
		}
	}
	if flags&jsonObjectAsArray != 0 && (len(args) < 2 || deref(z, args[1]).Type == types.TypeNull) {
		assoc = true
	}
	d := &jdec{s: s, z: z, assoc: assoc, max: int(depth)}
	d.skip()
	if d.i >= len(d.s) {
		return jsonFail(z, flags, jsonErrorSyntax)
	}
	v, err := d.value(1)
	if err != nil {
		code := int64(jsonErrorSyntax)
		if e, ok := err.(Error); ok && e.text == "depth" {
			code = jsonErrorDepth
		}
		return jsonFail(z, flags, code)
	}
	d.skip()
	if d.i != len(d.s) {
		types.ReleaseValue(z, &v)
		return jsonFail(z, flags, jsonErrorSyntax)
	}
	if !utf8.ValidString(s) {
		types.ReleaseValue(z, &v)
		return jsonFail(z, flags, jsonErrorUTF8)
	}
	if flags&JSON_THROW_ON_ERROR == 0 {
		setJSONError(z, jsonErrorNone)
	}
	return v, nil
}

const jsonObjectAsArray = 1

// jsonFail lève JsonException sous JSON_THROW_ON_ERROR, sinon consigne le code pour json_last_error et rend null.
func jsonFail(z *zone.Zone, flags, code int64) (types.Value, error) {
	if flags&JSON_THROW_ON_ERROR != 0 {
		return types.Value{}, JsonException{jsonErrorMessages[code]}
	}
	setJSONError(z, code)
	return types.NewNull(), nil
}

type jdec struct {
	s     string
	i     int
	z     *zone.Zone
	assoc bool
	max   int
}

func (d *jdec) skip() {
	for d.i < len(d.s) {
		switch d.s[d.i] {
		case ' ', '\t', '\n', '\r':
			d.i++
		default:
			return
		}
	}
}

func (d *jdec) value(depth int) (types.Value, error) {
	if depth > d.max {
		return types.Value{}, Error{"depth"}
	}
	d.skip()
	if d.i >= len(d.s) {
		return types.Value{}, Error{"eof"}
	}
	switch d.s[d.i] {
	case 'n':
		if !d.consume("null") {
			return types.Value{}, Error{"null"}
		}
		return types.NewNull(), nil
	case 't':
		if !d.consume("true") {
			return types.Value{}, Error{"true"}
		}
		return types.NewBool(true), nil
	case 'f':
		if !d.consume("false") {
			return types.Value{}, Error{"false"}
		}
		return types.NewBool(false), nil
	case '"':
		s, err := d.str()
		if err != nil {
			return types.Value{}, err
		}
		return newStr(d.z, s)
	case '[':
		return d.array(depth)
	case '{':
		return d.object(depth)
	default:
		return d.number()
	}
}

func (d *jdec) consume(lit string) bool {
	if d.i+len(lit) > len(d.s) || d.s[d.i:d.i+len(lit)] != lit {
		return false
	}
	d.i += len(lit)
	return true
}

func (d *jdec) array(depth int) (types.Value, error) {
	d.i++
	out, err := newArray(d.z)
	if err != nil {
		return types.Value{}, err
	}
	d.skip()
	if d.i < len(d.s) && d.s[d.i] == ']' {
		d.i++
		return out, nil
	}
	for {
		v, err := d.value(depth + 1)
		if err != nil {
			return types.Value{}, err
		}
		if err = appendKey(d.z, &out, v); err != nil {
			return types.Value{}, err
		}
		d.skip()
		if d.i >= len(d.s) {
			return types.Value{}, Error{"array"}
		}
		if d.s[d.i] == ']' {
			d.i++
			return out, nil
		}
		if d.s[d.i] != ',' {
			return types.Value{}, Error{"comma"}
		}
		d.i++
	}
}

func (d *jdec) object(depth int) (types.Value, error) {
	d.i++
	type pair struct {
		k string
		v types.Value
	}
	var pairs []pair
	d.skip()
	if d.i < len(d.s) && d.s[d.i] == '}' {
		d.i++
	} else {
		for {
			d.skip()
			if d.i >= len(d.s) || d.s[d.i] != '"' {
				return types.Value{}, Error{"key"}
			}
			k, err := d.str()
			if err != nil {
				return types.Value{}, err
			}
			d.skip()
			if d.i >= len(d.s) || d.s[d.i] != ':' {
				return types.Value{}, Error{"colon"}
			}
			d.i++
			v, err := d.value(depth + 1)
			if err != nil {
				return types.Value{}, err
			}
			pairs = append(pairs, pair{k, v})
			d.skip()
			if d.i >= len(d.s) {
				return types.Value{}, Error{"object"}
			}
			if d.s[d.i] == '}' {
				d.i++
				break
			}
			if d.s[d.i] != ',' {
				return types.Value{}, Error{"comma"}
			}
			d.i++
		}
	}
	if d.assoc {
		out, err := newArray(d.z)
		if err != nil {
			return types.Value{}, err
		}
		for _, p := range pairs {
			ks, err := newStr(d.z, p.k)
			if err != nil {
				return types.Value{}, err
			}
			if err = setKey(d.z, &out, ks, p.v); err != nil {
				return types.Value{}, err
			}
		}
		return out, nil
	}
	obj, err := types.NewObject(d.z, classStdClass, 0, false)
	if err != nil {
		return types.Value{}, err
	}
	props := make([]prop, len(pairs))
	for i, p := range pairs {
		props[i] = prop{key: p.k, val: p.v}
	}
	setObjectProps(d.z, obj, props)
	return obj, nil
}

func (d *jdec) number() (types.Value, error) {
	start := d.i
	if d.i < len(d.s) && (d.s[d.i] == '-' || d.s[d.i] == '+') {
		d.i++
	}
	if d.i >= len(d.s) || d.s[d.i] < '0' || d.s[d.i] > '9' {
		return types.Value{}, Error{"num"}
	}
	for d.i < len(d.s) && d.s[d.i] >= '0' && d.s[d.i] <= '9' {
		d.i++
	}
	floatish := false
	if d.i < len(d.s) && d.s[d.i] == '.' {
		floatish = true
		d.i++
		for d.i < len(d.s) && d.s[d.i] >= '0' && d.s[d.i] <= '9' {
			d.i++
		}
	}
	if d.i < len(d.s) && (d.s[d.i] == 'e' || d.s[d.i] == 'E') {
		floatish = true
		d.i++
		if d.i < len(d.s) && (d.s[d.i] == '+' || d.s[d.i] == '-') {
			d.i++
		}
		for d.i < len(d.s) && d.s[d.i] >= '0' && d.s[d.i] <= '9' {
			d.i++
		}
	}
	tok := d.s[start:d.i]
	if !floatish {
		n, err := strconv.ParseInt(tok, 10, 64)
		if err == nil {
			return types.NewLong(n), nil
		}
	}
	f, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return types.Value{}, err
	}
	return types.NewDouble(f), nil
}

func (d *jdec) str() (string, error) {
	if d.i >= len(d.s) || d.s[d.i] != '"' {
		return "", Error{"str"}
	}
	d.i++
	var b strings.Builder
	for d.i < len(d.s) {
		c := d.s[d.i]
		if c == '"' {
			d.i++
			return b.String(), nil
		}
		if c == '\\' {
			d.i++
			if d.i >= len(d.s) {
				return "", Error{"esc"}
			}
			switch d.s[d.i] {
			case '"', '\\', '/':
				b.WriteByte(d.s[d.i])
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				r, err := d.u4()
				if err != nil {
					return "", err
				}
				if r >= 0xD800 && r <= 0xDBFF {
					if d.i+1 < len(d.s) && d.s[d.i] == '\\' && d.s[d.i+1] == 'u' {
						d.i += 2
						lo, err := d.u4raw()
						if err != nil {
							return "", err
						}
						if lo < 0xDC00 || lo > 0xDFFF {
							return "", Error{"surrogate"}
						}
						r = 0x10000 + ((r - 0xD800) << 10) + (lo - 0xDC00)
					}
				}
				b.WriteRune(r)
				continue
			default:
				return "", Error{"esc"}
			}
			d.i++
			continue
		}
		if c < 0x20 {
			return "", Error{"ctrl"}
		}
		b.WriteByte(c)
		d.i++
	}
	return "", Error{"str"}
}

func (d *jdec) u4() (rune, error) {
	d.i++
	return d.u4raw()
}

func (d *jdec) u4raw() (rune, error) {
	if d.i+4 > len(d.s) {
		return 0, Error{"u"}
	}
	var r rune
	for range 4 {
		h := digitVal(d.s[d.i])
		if h < 0 || h > 15 {
			return 0, Error{"u"}
		}
		r = r<<4 | rune(h)
		d.i++
	}
	return r, nil
}
