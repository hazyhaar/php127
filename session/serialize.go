package session

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// SerializePHP encode une Value en chaîne au format serialize() de PHP 8.3.
func SerializePHP(z *zone.Zone, v types.Value) (string, error) {
	var b strings.Builder
	if err := serializeValue(z, &b, v); err != nil {
		return "", err
	}
	return b.String(), nil
}

func serializeValue(z *zone.Zone, b *strings.Builder, v types.Value) error {
	v = types.Deref(z, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		b.WriteString("N;")
	case types.TypeFalse:
		b.WriteString("b:0;")
	case types.TypeTrue:
		b.WriteString("b:1;")
	case types.TypeLong:
		b.WriteString("i:")
		b.WriteString(strconv.FormatInt(v.ToLong(), 10))
		b.WriteByte(';')
	case types.TypeDouble:
		b.WriteString("d:")
		f := v.ToDouble()
		switch {
		case math.IsNaN(f):
			b.WriteString("NAN")
		case math.IsInf(f, 1):
			b.WriteString("INF")
		case math.IsInf(f, -1):
			b.WriteString("-INF")
		default:
			b.WriteString(strconv.FormatFloat(f, 'G', 17, 64))
		}
		b.WriteByte(';')
	case types.TypeString:
		s := types.StringData(z, v)
		b.WriteString("s:")
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteString(`:"`)
		b.WriteString(s)
		b.WriteString(`";`)
	case types.TypeArray:
		n := types.ArrayLen(z, v)
		b.WriteString("a:")
		b.WriteString(strconv.FormatUint(uint64(n), 10))
		b.WriteString(":{")
		var serErr error
		types.ArrayForEach(z, v, func(k, elem types.Value) bool {
			if err := serializeValue(z, b, k); err != nil {
				serErr = err
				return false
			}
			if err := serializeValue(z, b, elem); err != nil {
				serErr = err
				return false
			}
			return true
		})
		if serErr != nil {
			return serErr
		}
		b.WriteByte('}')
	default:
		b.WriteString("N;")
	}
	return nil
}

// UnserializePHP décode une chaîne au format serialize() de PHP 8.3 vers une Value allouée dans z.
func UnserializePHP(z *zone.Zone, data string) (types.Value, int, error) {
	return unserializeAt(z, data, 0)
}

func unserializeAt(z *zone.Zone, data string, pos int) (types.Value, int, error) {
	if pos >= len(data) {
		return types.Value{}, pos, fmt.Errorf("session: unexpected end of serialized data")
	}
	tag := data[pos]
	switch tag {
	case 'N':
		if pos+2 > len(data) || data[pos+1] != ';' {
			return types.Value{}, pos, fmt.Errorf("session: malformed null at %d", pos)
		}
		return types.NewNull(), pos + 2, nil
	case 'b':
		if pos+4 > len(data) || data[pos+1] != ':' || data[pos+3] != ';' {
			return types.Value{}, pos, fmt.Errorf("session: malformed bool at %d", pos)
		}
		switch data[pos+2] {
		case '0':
			return types.NewBool(false), pos + 4, nil
		case '1':
			return types.NewBool(true), pos + 4, nil
		default:
			return types.Value{}, pos, fmt.Errorf("session: invalid bool digit at %d", pos)
		}
	case 'i':
		if pos+2 >= len(data) || data[pos+1] != ':' {
			return types.Value{}, pos, fmt.Errorf("session: malformed int at %d", pos)
		}
		semi := strings.IndexByte(data[pos+2:], ';')
		if semi < 0 {
			return types.Value{}, pos, fmt.Errorf("session: unterminated int at %d", pos)
		}
		numStr := data[pos+2 : pos+2+semi]
		n, err := strconv.ParseInt(numStr, 10, 64)
		if err != nil {
			return types.Value{}, pos, err
		}
		return types.NewLong(n), pos + 2 + semi + 1, nil
	case 'd':
		if pos+2 >= len(data) || data[pos+1] != ':' {
			return types.Value{}, pos, fmt.Errorf("session: malformed double at %d", pos)
		}
		semi := strings.IndexByte(data[pos+2:], ';')
		if semi < 0 {
			return types.Value{}, pos, fmt.Errorf("session: unterminated double at %d", pos)
		}
		numStr := data[pos+2 : pos+2+semi]
		var f float64
		switch numStr {
		case "NAN":
			f = math.NaN()
		case "INF":
			f = math.Inf(1)
		case "-INF":
			f = math.Inf(-1)
		default:
			var err error
			f, err = strconv.ParseFloat(numStr, 64)
			if err != nil {
				return types.Value{}, pos, err
			}
		}
		return types.NewDouble(f), pos + 2 + semi + 1, nil
	case 's':
		if pos+2 >= len(data) || data[pos+1] != ':' {
			return types.Value{}, pos, fmt.Errorf("session: malformed string at %d", pos)
		}
		colon := strings.IndexByte(data[pos+2:], ':')
		if colon < 0 {
			return types.Value{}, pos, fmt.Errorf("session: missing string colon at %d", pos)
		}
		slen, err := strconv.Atoi(data[pos+2 : pos+2+colon])
		if err != nil || slen < 0 {
			return types.Value{}, pos, fmt.Errorf("session: invalid string length at %d", pos)
		}
		qStart := pos + 2 + colon + 1
		if qStart+1+slen+2 > len(data) || data[qStart] != '"' || data[qStart+1+slen] != '"' || data[qStart+1+slen+1] != ';' {
			return types.Value{}, pos, fmt.Errorf("session: malformed string body at %d", pos)
		}
		strVal, err := types.NewString(z, data[qStart+1:qStart+1+slen], 0)
		if err != nil {
			return types.Value{}, pos, err
		}
		return strVal, qStart + 1 + slen + 2, nil
	case 'a':
		if pos+2 >= len(data) || data[pos+1] != ':' {
			return types.Value{}, pos, fmt.Errorf("session: malformed array at %d", pos)
		}
		colon := strings.IndexByte(data[pos+2:], ':')
		if colon < 0 {
			return types.Value{}, pos, fmt.Errorf("session: missing array colon at %d", pos)
		}
		count, err := strconv.Atoi(data[pos+2 : pos+2+colon])
		if err != nil || count < 0 {
			return types.Value{}, pos, fmt.Errorf("session: invalid array count at %d", pos)
		}
		brace := pos + 2 + colon + 1
		if brace >= len(data) || data[brace] != '{' {
			return types.Value{}, pos, fmt.Errorf("session: missing array opening brace at %d", pos)
		}
		arr, err := types.NewArray(z)
		if err != nil {
			return types.Value{}, pos, err
		}
		cur := brace + 1
		for range count {
			k, nextCur, err := unserializeAt(z, data, cur)
			if err != nil {
				types.ReleaseArray(z, &arr)
				return types.Value{}, pos, err
			}
			v, nextCur2, err := unserializeAt(z, data, nextCur)
			if err != nil {
				types.ReleaseValue(z, &k)
				types.ReleaseArray(z, &arr)
				return types.Value{}, pos, err
			}
			err = types.ArraySet(z, &arr, k, v, nil)
			types.ReleaseValue(z, &k)
			types.ReleaseValue(z, &v)
			if err != nil {
				types.ReleaseArray(z, &arr)
				return types.Value{}, pos, err
			}
			cur = nextCur2
		}
		if cur >= len(data) || data[cur] != '}' {
			types.ReleaseArray(z, &arr)
			return types.Value{}, pos, fmt.Errorf("session: missing array closing brace at %d", cur)
		}
		return arr, cur + 1, nil
	default:
		return types.Value{}, pos, fmt.Errorf("session: unsupported serialize tag %q at %d", tag, pos)
	}
}

func keyToSessionName(z *zone.Zone, k types.Value) string {
	k = types.Deref(z, k)
	if k.Type == types.TypeString {
		return types.StringData(z, k)
	}
	if k.Type == types.TypeLong {
		return strconv.FormatInt(k.ToLong(), 10)
	}
	return ""
}

// EncodeSessionData encode le tableau $_SESSION selon le format "php" ou "php_serialize".
func EncodeSessionData(z *zone.Zone, sessionArr types.Value, handler string) (string, error) {
	sessionArr = types.Deref(z, sessionArr)
	if sessionArr.Type != types.TypeArray {
		return "", nil
	}
	if handler == "php_serialize" {
		return SerializePHP(z, sessionArr)
	}
	// Format par défaut "php" : <key>|<serialized_val>...
	var b strings.Builder
	var encErr error
	types.ArrayForEach(z, sessionArr, func(k, v types.Value) bool {
		keyName := keyToSessionName(z, k)
		if strings.ContainsAny(keyName, "|!") {
			encErr = fmt.Errorf("session: key %q contains reserved character", keyName)
			return false
		}
		b.WriteString(keyName)
		b.WriteByte('|')
		if err := serializeValue(z, &b, v); err != nil {
			encErr = err
			return false
		}
		return true
	})
	if encErr != nil {
		return "", encErr
	}
	return b.String(), nil
}

// DecodeSessionData décode une chaîne de session ("php" ou "php_serialize") dans le tableau cible.
func DecodeSessionData(z *zone.Zone, dstArr *types.Value, raw string, handler string) error {
	if raw == "" {
		return nil
	}
	if handler == "php_serialize" || (strings.HasPrefix(raw, "a:") && !strings.Contains(raw[:min(len(raw), 16)], "|")) {
		decoded, end, err := UnserializePHP(z, raw)
		if err != nil {
			return err
		}
		defer types.ReleaseValue(z, &decoded)
		if end != len(raw) || decoded.Type != types.TypeArray {
			return fmt.Errorf("session: invalid php_serialize payload")
		}
		var copyErr error
		types.ArrayForEach(z, decoded, func(k, v types.Value) bool {
			if err := types.ArraySet(z, dstArr, k, v, nil); err != nil {
				copyErr = err
				return false
			}
			return true
		})
		return copyErr
	}

	pos := 0
	for pos < len(raw) {
		pipeRel := strings.IndexByte(raw[pos:], '|')
		if pipeRel <= 0 {
			return fmt.Errorf("session: missing '|' separator at %d", pos)
		}
		keyStr := raw[pos : pos+pipeRel]
		valStart := pos + pipeRel + 1
		val, nextPos, err := unserializeAt(z, raw, valStart)
		if err != nil {
			return err
		}
		kv, err := types.NewString(z, keyStr, 0)
		if err != nil {
			types.ReleaseValue(z, &val)
			return err
		}
		setErr := types.ArraySet(z, dstArr, kv, val, nil)
		types.ReleaseString(z, &kv)
		types.ReleaseValue(z, &val)
		if setErr != nil {
			return setErr
		}
		pos = nextPos
	}
	return nil
}
