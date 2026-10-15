package sapi

import (
	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/types"
)

type nativeEntry struct {
	name string
	fn   stdlib.NativeFunc
}

// Lookup recherche une fonction native HTTP ou URL dans le registre SAPI.
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

// Constants retourne les constantes PHP exposées par le paquet SAPI (URL et requêtes).
func Constants() map[string]types.Value { return constants }

var constants = map[string]types.Value{
	"PHP_URL_SCHEME":    types.NewLong(PHP_URL_SCHEME),
	"PHP_URL_HOST":      types.NewLong(PHP_URL_HOST),
	"PHP_URL_PORT":      types.NewLong(PHP_URL_PORT),
	"PHP_URL_USER":      types.NewLong(PHP_URL_USER),
	"PHP_URL_PASS":      types.NewLong(PHP_URL_PASS),
	"PHP_URL_PATH":      types.NewLong(PHP_URL_PATH),
	"PHP_URL_QUERY":     types.NewLong(PHP_URL_QUERY),
	"PHP_URL_FRAGMENT":  types.NewLong(PHP_URL_FRAGMENT),
	"PHP_QUERY_RFC1738": types.NewLong(PHP_QUERY_RFC1738),
	"PHP_QUERY_RFC3986": types.NewLong(PHP_QUERY_RFC3986),
}

func init() {
	for i := 1; i < len(natives); i++ {
		if c := cmpFold(natives[i-1].name, natives[i].name); c >= 0 {
			panic("sapi: natives not sorted: " + natives[i-1].name + " >= " + natives[i].name)
		}
	}
}

var natives = [...]nativeEntry{
	{"header", nativeHeader},
	{"header_remove", nativeHeaderRemove},
	{"headers_list", nativeHeadersList},
	{"headers_sent", nativeHeadersSent},
	{"http_build_query", nativeHTTPBuildQuery},
	{"http_response_code", nativeHTTPResponseCode},
	{"parse_str", nativeParseStr},
	{"parse_url", nativeParseURL},
	{"setcookie", nativeSetCookie(false)},
	{"setrawcookie", nativeSetCookie(true)},
}
