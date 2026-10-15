package stdlib

import (
	"io"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func nativeCtype(name string, pred func(byte) bool) NativeFunc {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		text, err := ctypeText(z, args[0])
		if err != nil {
			return types.Value{}, err
		}
		if text == "" {
			return types.NewBool(false), nil
		}
		for i := 0; i < len(text); i++ {
			if !pred(text[i]) {
				return types.NewBool(false), nil
			}
		}
		return types.NewBool(true), nil
	}
}

func ctypeText(z *zone.Zone, v types.Value) (string, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeLong:
		n := v.ToLong()
		if n >= -128 && n <= 255 {
			return string([]byte{byte(n)}), nil
		}
		return itoa64(n), nil
	case types.TypeString:
		return types.StringData(z, v), nil
	case types.TypeTrue:
		return "1", nil
	case types.TypeFalse, types.TypeNull, types.TypeUndef:
		return "", nil
	case types.TypeDouble:
		return formatFloat(v.ToDouble(), floatCast), nil
	default:
		return "", typeErr("ctype", "1", "text", "string", typeKind(z, v))
	}
}

func ctypeAlnum(c byte) bool {
	return ctypeAlpha(c) || ctypeDigit(c)
}
func ctypeAlpha(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}
func ctypeDigit(c byte) bool { return c >= '0' && c <= '9' }
func ctypeLower(c byte) bool { return c >= 'a' && c <= 'z' }
func ctypeUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func ctypeSpace(c byte) bool {
	return c == ' ' || c == '\f' || c == '\n' || c == '\r' || c == '\t' || c == '\v'
}
func ctypeXdigit(c byte) bool {
	return ctypeDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
func ctypePunct(c byte) bool {
	return c > 0x20 && c < 0x7f && !ctypeAlnum(c)
}

