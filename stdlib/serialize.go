package stdlib

import (
	"io"
	"math"
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func nativeSerialize(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("serialize", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	var b strings.Builder
	if err := encodeSerialize(&b, z, deref(z, args[0])); err != nil {
		return types.Value{}, err
	}
	return newStr(z, b.String())
}

func encodeSerialize(b *strings.Builder, z *zone.Zone, v types.Value) error {
	v = deref(z, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		b.WriteString("N;")
	case types.TypeTrue:
		b.WriteString("b:1;")
	case types.TypeFalse:
		b.WriteString("b:0;")
	case types.TypeLong:
		b.WriteString("i:")
		b.WriteString(strconv.FormatInt(v.ToLong(), 10))
		b.WriteByte(';')
	case types.TypeDouble:
		b.WriteString("d:")
		b.WriteString(formatFloat(v.ToDouble(), floatDump))
		b.WriteByte(';')
	case types.TypeString:
		s := types.StringData(z, v)
		b.WriteString("s:")
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteString(`:"`)
		b.WriteString(s)
		b.WriteString(`";`)
	case types.TypeArray:
		keys, vals := arrayItems(z, v)
		b.WriteString("a:")
		b.WriteString(strconv.Itoa(len(vals)))
		b.WriteString(":{")
		for i := range keys {
			if keys[i].IsLong() {
				b.WriteString("i:")
				b.WriteString(strconv.FormatInt(keys[i].ToLong(), 10))
				b.WriteByte(';')
			} else {
				ks := types.StringData(z, keys[i])
				b.WriteString("s:")
				b.WriteString(strconv.Itoa(len(ks)))
				b.WriteString(`:"`)
				b.WriteString(ks)
				b.WriteString(`";`)
			}
			if err := encodeSerialize(b, z, vals[i]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	case types.TypeObject:
		className := safeClass(z, v)
		if className == "" {
			className = "stdClass"
		}
		props := objectProps(z, v)
		b.WriteString("O:")
		b.WriteString(strconv.Itoa(len(className)))
		b.WriteString(`:"`)
		b.WriteString(className)
		b.WriteString(`":`)
		b.WriteString(strconv.Itoa(len(props)))
		b.WriteString(":{")
		for _, p := range props {
			b.WriteString("s:")
			b.WriteString(strconv.Itoa(len(p.key)))
			b.WriteString(`:"`)
			b.WriteString(p.key)
			b.WriteString(`";`)
			if err := encodeSerialize(b, z, p.val); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		b.WriteString("i:0;")
	}
	return nil
}

func nativeUnserialize(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("unserialize", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	data, err := asStringParam(z, args[0], "unserialize", "1", "data")
	if err != nil {
		return types.Value{}, err
	}
	if len(args) == 2 && !deref(z, args[1]).IsNull() {
		if _, err := asArray(z, args[1], "unserialize", "2", "options"); err != nil {
			return types.Value{}, err
		}
	}
	if data == "" {
		return types.NewBool(false), nil
	}
	p := &unserializer{z: z, src: data}
	val, ok, uerr := p.parseValue()
	if uerr != nil {
		return types.Value{}, uerr
	}
	if !ok {
		return types.NewBool(false), nil
	}
	return val, nil
}

type unserializer struct {
	z   *zone.Zone
	src string
	pos int
}

func (u *unserializer) readUntil(delim byte) (string, bool) {
	idx := strings.IndexByte(u.src[u.pos:], delim)
	if idx < 0 {
		return "", false
	}
	s := u.src[u.pos : u.pos+idx]
	u.pos += idx + 1
	return s, true
}

func (u *unserializer) readQuotedBytes(n int) (string, bool) {
	if n < 0 || u.pos >= len(u.src) || u.src[u.pos] != '"' {
		return "", false
	}
	u.pos++
	if u.pos+n > len(u.src) {
		return "", false
	}
	s := u.src[u.pos : u.pos+n]
	u.pos += n
	if u.pos >= len(u.src) || u.src[u.pos] != '"' {
		return "", false
	}
	u.pos++
	return s, true
}

func (u *unserializer) parseValue() (types.Value, bool, error) {
	if u.pos >= len(u.src) {
		return types.Value{}, false, nil
	}
	tag := u.src[u.pos]
	u.pos++
	switch tag {
	case 'N':
		if u.pos >= len(u.src) || u.src[u.pos] != ';' {
			return types.Value{}, false, nil
		}
		u.pos++
		return types.NewNull(), true, nil
	case 'b':
		if u.pos >= len(u.src) || u.src[u.pos] != ':' {
			return types.Value{}, false, nil
		}
		u.pos++
		raw, ok := u.readUntil(';')
		if !ok || (raw != "0" && raw != "1") {
			return types.Value{}, false, nil
		}
		return types.NewBool(raw == "1"), true, nil
	case 'i':
		if u.pos >= len(u.src) || u.src[u.pos] != ':' {
			return types.Value{}, false, nil
		}
		u.pos++
		raw, ok := u.readUntil(';')
		if !ok {
			return types.Value{}, false, nil
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return types.Value{}, false, nil
		}
		return types.NewLong(n), true, nil
	case 'd':
		if u.pos >= len(u.src) || u.src[u.pos] != ':' {
			return types.Value{}, false, nil
		}
		u.pos++
		raw, ok := u.readUntil(';')
		if !ok {
			return types.Value{}, false, nil
		}
		switch raw {
		case "NAN":
			return types.NewDouble(math.NaN()), true, nil
		case "INF":
			return types.NewDouble(math.Inf(1)), true, nil
		case "-INF":
			return types.NewDouble(math.Inf(-1)), true, nil
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			if ne, isRange := err.(*strconv.NumError); !isRange || ne.Err != strconv.ErrRange {
				return types.Value{}, false, nil
			}
		}
		return types.NewDouble(f), true, nil
	case 's':
		if u.pos >= len(u.src) || u.src[u.pos] != ':' {
			return types.Value{}, false, nil
		}
		u.pos++
		lenStr, ok := u.readUntil(':')
		if !ok {
			return types.Value{}, false, nil
		}
		n, err := strconv.Atoi(lenStr)
		if err != nil || n < 0 {
			return types.Value{}, false, nil
		}
		s, ok := u.readQuotedBytes(n)
		if !ok || u.pos >= len(u.src) || u.src[u.pos] != ';' {
			return types.Value{}, false, nil
		}
		u.pos++
		sv, err := newStr(u.z, s)
		if err != nil {
			return types.Value{}, false, err
		}
		return sv, true, nil
	case 'a':
		if u.pos >= len(u.src) || u.src[u.pos] != ':' {
			return types.Value{}, false, nil
		}
		u.pos++
		lenStr, ok := u.readUntil(':')
		if !ok {
			return types.Value{}, false, nil
		}
		count, err := strconv.Atoi(lenStr)
		if err != nil || count < 0 {
			return types.Value{}, false, nil
		}
		if u.pos >= len(u.src) || u.src[u.pos] != '{' {
			return types.Value{}, false, nil
		}
		u.pos++
		arr, err := newArray(u.z)
		if err != nil {
			return types.Value{}, false, err
		}
		for range count {
			k, kok, kerr := u.parseValue()
			if kerr != nil || !kok || (k.Type != types.TypeLong && k.Type != types.TypeString) {
				types.ReleaseValue(u.z, &arr)
				return types.Value{}, false, kerr
			}
			v, vok, verr := u.parseValue()
			if verr != nil || !vok {
				types.ReleaseValue(u.z, &k)
				types.ReleaseValue(u.z, &arr)
				return types.Value{}, false, verr
			}
			if err := setKey(u.z, &arr, k, v); err != nil {
				types.ReleaseValue(u.z, &k)
				types.ReleaseValue(u.z, &v)
				types.ReleaseValue(u.z, &arr)
				return types.Value{}, false, err
			}
			types.ReleaseValue(u.z, &k)
			types.ReleaseValue(u.z, &v)
		}
		if u.pos >= len(u.src) || u.src[u.pos] != '}' {
			types.ReleaseValue(u.z, &arr)
			return types.Value{}, false, nil
		}
		u.pos++
		return arr, true, nil
	case 'O':
		if u.pos >= len(u.src) || u.src[u.pos] != ':' {
			return types.Value{}, false, nil
		}
		u.pos++
		clenStr, ok := u.readUntil(':')
		if !ok {
			return types.Value{}, false, nil
		}
		clen, err := strconv.Atoi(clenStr)
		if err != nil || clen < 0 {
			return types.Value{}, false, nil
		}
		_, ok = u.readQuotedBytes(clen)
		if !ok || u.pos >= len(u.src) || u.src[u.pos] != ':' {
			return types.Value{}, false, nil
		}
		u.pos++
		pcountStr, ok := u.readUntil(':')
		if !ok {
			return types.Value{}, false, nil
		}
		pcount, err := strconv.Atoi(pcountStr)
		if err != nil || pcount < 0 {
			return types.Value{}, false, nil
		}
		if u.pos >= len(u.src) || u.src[u.pos] != '{' {
			return types.Value{}, false, nil
		}
		u.pos++
		obj, err := types.NewObject(u.z, classStdClass, 0, false)
		if err != nil {
			return types.Value{}, false, err
		}
		props := make([]prop, 0, pcount)
		for range pcount {
			k, kok, kerr := u.parseValue()
			if kerr != nil || !kok || (k.Type != types.TypeString && k.Type != types.TypeLong) {
				types.ReleaseValue(u.z, &obj)
				return types.Value{}, false, kerr
			}
			v, vok, verr := u.parseValue()
			if verr != nil || !vok {
				types.ReleaseValue(u.z, &k)
				types.ReleaseValue(u.z, &obj)
				return types.Value{}, false, verr
			}
			var keyStr string
			if k.Type == types.TypeLong {
				keyStr = strconv.FormatInt(k.ToLong(), 10)
			} else {
				keyStr = types.StringData(u.z, k)
			}
			types.ReleaseValue(u.z, &k)
			found := false
			for i := range props {
				if props[i].key == keyStr {
					props[i].val = v
					found = true
					break
				}
			}
			if !found {
				props = append(props, prop{key: keyStr, val: v})
			}
		}
		if u.pos >= len(u.src) || u.src[u.pos] != '}' {
			types.ReleaseValue(u.z, &obj)
			return types.Value{}, false, nil
		}
		u.pos++
		setObjectProps(u.z, obj, props)
		return obj, true, nil
	default:
		return types.Value{}, false, nil
	}
}
