package ext

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"hash"
	"hash/crc32"
	"io"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

type hashAlgo struct {
	name string
	new  func() hash.Hash
	hmac bool
}

var hashAlgos = [...]hashAlgo{
	{"md5", md5.New, true},
	{"sha1", sha1.New, true},
	{"sha256", sha256.New, true},
	{"sha384", sha512.New384, true},
	{"sha512", sha512.New, true},
	{"crc32b", func() hash.Hash { return crc32.NewIEEE() }, false},
}

func findAlgo(name string) (hashAlgo, bool) {
	lo := strings.ToLower(name)
	for _, a := range hashAlgos {
		if a.name == lo {
			return a, true
		}
	}
	return hashAlgo{}, false
}

func digest(h hash.Hash, data string, binary bool, z *zone.Zone) (types.Value, error) {
	_, _ = io.WriteString(h, data)
	sum := h.Sum(nil)
	if binary {
		return newStr(z, string(sum))
	}
	return newStr(z, hex.EncodeToString(sum))
}

func nativeHash(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	algo, err := asString(z, args[0], "hash", "1", "algo")
	if err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[1], "hash", "2", "data")
	if err != nil {
		return types.Value{}, err
	}
	a, ok := findAlgo(algo)
	if !ok {
		return types.Value{}, ValueError{"hash(): Argument #1 ($algo) must be a valid hashing algorithm"}
	}
	return digest(a.new(), data, optBool(z, args, 2), z)
}

func nativeHashHMAC(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash_hmac", len(args), 3, 4); err != nil {
		return types.Value{}, err
	}
	algo, err := asString(z, args[0], "hash_hmac", "1", "algo")
	if err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[1], "hash_hmac", "2", "data")
	if err != nil {
		return types.Value{}, err
	}
	key, err := asString(z, args[2], "hash_hmac", "3", "key")
	if err != nil {
		return types.Value{}, err
	}
	a, ok := findAlgo(algo)
	if !ok || !a.hmac {
		return types.Value{}, ValueError{"hash_hmac(): Argument #1 ($algo) must be a valid cryptographic hashing algorithm"}
	}
	mac := hmac.New(a.new, []byte(key))
	return digest(mac, data, optBool(z, args, 3), z)
}

func nativeHashEquals(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash_equals", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	known, err := asStrictString(z, args[0], "hash_equals", "1", "known_string")
	if err != nil {
		return types.Value{}, err
	}
	user, err := asStrictString(z, args[1], "hash_equals", "2", "user_string")
	if err != nil {
		return types.Value{}, err
	}
	if len(known) != len(user) {
		return types.NewBool(false), nil
	}
	return types.NewBool(subtle.ConstantTimeCompare([]byte(known), []byte(user)) == 1), nil
}

func nativeHashAlgos(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hash_algos", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, algo := range hashAlgos {
		s, err := newStr(z, algo.name)
		if err != nil {
			return types.Value{}, err
		}
		if err = appendVal(z, &a, s); err != nil {
			return types.Value{}, err
		}
	}
	return a, nil
}

func nativeMD5(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return namedDigest(z, args, "md5", "string", md5.New)
}

func nativeSHA1(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return namedDigest(z, args, "sha1", "string", sha1.New)
}

func namedDigest(z *zone.Zone, args []types.Value, name, param string, newH func() hash.Hash) (types.Value, error) {
	if err := arity(name, len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[0], name, "1", param)
	if err != nil {
		return types.Value{}, err
	}
	return digest(newH(), data, optBool(z, args, 1), z)
}

func nativeCRC32(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("crc32", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[0], "crc32", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(crc32.ChecksumIEEE([]byte(data)))), nil
}

func nativeBase64Encode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("base64_encode", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "base64_encode", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, base64Encode(s))
}

func nativeBase64Decode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("base64_decode", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], "base64_decode", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	out, ok := base64Decode(s, optBool(z, args, 1))
	if !ok {
		return types.NewBool(false), nil
	}
	return newStr(z, out)
}

func base64Encode(s string) string {
	const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	n := len(s)
	var b strings.Builder
	b.Grow((n + 2) / 3 * 4)
	i := 0
	for i+3 <= n {
		v := uint32(s[i])<<16 | uint32(s[i+1])<<8 | uint32(s[i+2])
		b.WriteByte(alpha[(v>>18)&63])
		b.WriteByte(alpha[(v>>12)&63])
		b.WriteByte(alpha[(v>>6)&63])
		b.WriteByte(alpha[v&63])
		i += 3
	}
	rem := n - i
	if rem == 1 {
		v := uint32(s[i]) << 16
		b.WriteByte(alpha[(v>>18)&63])
		b.WriteByte(alpha[(v>>12)&63])
		b.WriteString("==")
	} else if rem == 2 {
		v := uint32(s[i])<<16 | uint32(s[i+1])<<8
		b.WriteByte(alpha[(v>>18)&63])
		b.WriteByte(alpha[(v>>12)&63])
		b.WriteByte(alpha[(v>>6)&63])
		b.WriteByte('=')
	}
	return b.String()
}

func b64Val(c byte) int {
	switch {
	case c >= 'A' && c <= 'Z':
		return int(c - 'A')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 26
	case c >= '0' && c <= '9':
		return int(c-'0') + 52
	case c == '+':
		return 62
	case c == '/':
		return 63
	default:
		return -1
	}
}

func isB64WS(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func base64Decode(s string, strict bool) (string, bool) {
	alpha := make([]byte, 0, len(s))
	pads := 0
	sawPad := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isB64WS(c) {
			continue
		}
		if c == '=' {
			if strict {
				sawPad = true
				pads++
				if pads > 2 {
					return "", false
				}
				continue
			}
			continue
		}
		if strict && sawPad {
			return "", false
		}
		if b64Val(c) < 0 {
			if strict {
				return "", false
			}
			continue
		}
		if strict && sawPad {
			return "", false
		}
		alpha = append(alpha, c)
	}
	if strict {
		if pads > 0 && (len(alpha)+pads)%4 != 0 {
			return "", false
		}
		if pads == 0 && len(alpha)%4 == 1 {
			return "", false
		}
	}
	return decodeAlphabet(alpha), true
}

func decodeAlphabet(alpha []byte) string {
	if len(alpha)%4 == 1 {
		alpha = alpha[:len(alpha)-1]
	}
	var b strings.Builder
	b.Grow(len(alpha) * 3 / 4)
	i := 0
	for i+4 <= len(alpha) {
		v0, v1, v2, v3 := b64Val(alpha[i]), b64Val(alpha[i+1]), b64Val(alpha[i+2]), b64Val(alpha[i+3])
		v := v0<<18 | v1<<12 | v2<<6 | v3
		b.WriteByte(byte(v >> 16))
		b.WriteByte(byte(v >> 8))
		b.WriteByte(byte(v))
		i += 4
	}
	rem := alpha[i:]
	if len(rem) == 2 {
		v := b64Val(rem[0])<<18 | b64Val(rem[1])<<12
		b.WriteByte(byte(v >> 16))
	} else if len(rem) == 3 {
		v := b64Val(rem[0])<<18 | b64Val(rem[1])<<12 | b64Val(rem[2])<<6
		b.WriteByte(byte(v >> 16))
		b.WriteByte(byte(v >> 8))
	}
	return b.String()
}

func nativeURLEncode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return urlMap(z, args, "urlencode", false)
}

func nativeRawURLEncode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return urlMap(z, args, "rawurlencode", true)
}

func urlMap(z *zone.Zone, args []types.Value, name string, raw bool) (types.Value, error) {
	if err := arity(name, len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], name, "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, phpURLEncode(s, raw))
}

func phpURLEncode(s string, raw bool) string {
	const hexdigits = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isURLUnreserved(c, raw) {
			b.WriteByte(c)
			continue
		}
		if c == ' ' && !raw {
			b.WriteByte('+')
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hexdigits[c>>4])
		b.WriteByte(hexdigits[c&0x0f])
	}
	return b.String()
}

func isURLUnreserved(c byte, raw bool) bool {
	if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
		return true
	}
	if c == '-' || c == '_' || c == '.' {
		return true
	}
	return raw && c == '~'
}

func nativeURLDecode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return urlUnmap(z, args, "urldecode", false)
}

func nativeRawURLDecode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return urlUnmap(z, args, "rawurldecode", true)
}

func urlUnmap(z *zone.Zone, args []types.Value, name string, raw bool) (types.Value, error) {
	if err := arity(name, len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], name, "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, phpURLDecode(s, raw))
}

func phpURLDecode(s string, raw bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '+' && !raw {
			b.WriteByte(' ')
			continue
		}
		if c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}
