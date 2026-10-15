package ext

import (
	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/types"
)

type nativeEntry struct {
	name string
	fn   stdlib.NativeFunc
}

func Lookup(name string) (stdlib.NativeFunc, bool) {
	lo, hi := 0, len(natives)
	for lo < hi {
		mid := (lo + hi) >> 1
		c := cmpFold(name, natives[mid].name)
		if c == 0 {
			return natives[mid].fn, true
		}
		if c < 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return nil, false
}

func cmpFold(name, canon string) int {
	n, m := len(name), len(canon)
	lim := min(m, n)
	for i := range lim {
		a := name[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		b := canon[i]
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
	}
	if n < m {
		return -1
	}
	if n > m {
		return 1
	}
	return 0
}

func Constants() map[string]types.Value { return constants }

var constants map[string]types.Value

func init() {
	for i := 1; i < len(natives); i++ {
		if c := cmpFold(natives[i-1].name, natives[i].name); c >= 0 {
			panic("ext: natives not sorted: " + natives[i-1].name + " >= " + natives[i].name)
		}
	}
	constants = map[string]types.Value{
		"PREG_PATTERN_ORDER":        types.NewLong(1),
		"PREG_SET_ORDER":            types.NewLong(2),
		"PREG_OFFSET_CAPTURE":       types.NewLong(256),
		"PREG_UNMATCHED_AS_NULL":    types.NewLong(512),
		"PREG_SPLIT_NO_EMPTY":       types.NewLong(1),
		"PREG_SPLIT_DELIM_CAPTURE":  types.NewLong(2),
		"PREG_SPLIT_OFFSET_CAPTURE": types.NewLong(4),
		"PREG_GREP_INVERT":          types.NewLong(1),
	}
	for name, text := range dateFormats {
		v, err := types.InternProcessString(text)
		if err != nil {
			panic(err)
		}
		constants[name] = v
	}
}

var dateFormats = map[string]string{
	"DATE_ATOM":             `Y-m-d\TH:i:sP`,
	"DATE_COOKIE":           "l, d-M-Y H:i:s T",
	"DATE_ISO8601":          `Y-m-d\TH:i:sO`,
	"DATE_RFC822":           "D, d M y H:i:s O",
	"DATE_RFC850":           "l, d-M-y H:i:s T",
	"DATE_RFC1036":          "D, d M y H:i:s O",
	"DATE_RFC1123":          "D, d M Y H:i:s O",
	"DATE_RFC2822":          "D, d M Y H:i:s O",
	"DATE_RFC3339":          `Y-m-d\TH:i:sP`,
	"DATE_RFC3339_EXTENDED": `Y-m-d\TH:i:s.vP`,
	"DATE_RFC7231":          `D, d M Y H:i:s \G\M\T`,
	"DATE_RSS":              "D, d M Y H:i:s O",
	"DATE_W3C":              `Y-m-d\TH:i:sP`,
}

var natives = [...]nativeEntry{
	{"base64_decode", nativeBase64Decode},
	{"base64_encode", nativeBase64Encode},
	{"crc32", nativeCRC32},
	{"date", nativeDate},
	{"gmdate", nativeGMDate},
	{"gmmktime", nativeGMMktime},
	{"hash", nativeHash},
	{"hash_algos", nativeHashAlgos},
	{"hash_equals", nativeHashEquals},
	{"hash_hmac", nativeHashHMAC},
	{"hrtime", nativeHrtime},
	{"mb_check_encoding", nativeMBCheckEncoding},
	{"mb_strlen", nativeMBStrlen},
	{"mb_strpos", nativeMBStrpos},
	{"mb_strrpos", nativeMBStrrpos},
	{"mb_strtolower", nativeMBStrtolower},
	{"mb_strtoupper", nativeMBStrtoupper},
	{"mb_substr", nativeMBSubstr},
	{"md5", nativeMD5},
	{"microtime", nativeMicrotime},
	{"mktime", nativeMktime},
	{"preg_grep", nativePregGrep},
	{"preg_match", nativePregMatch},
	{"preg_match_all", nativePregMatchAll},
	{"preg_quote", nativePregQuote},
	{"preg_replace", nativePregReplace},
	{"preg_split", nativePregSplit},
	{"rawurldecode", nativeRawURLDecode},
	{"rawurlencode", nativeRawURLEncode},
	{"sha1", nativeSHA1},
	{"strtotime", nativeStrtotime},
	{"time", nativeTime},
	{"urldecode", nativeURLDecode},
	{"urlencode", nativeURLEncode},
}
