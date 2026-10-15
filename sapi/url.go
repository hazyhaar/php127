package sapi

import (
	"io"
	"strconv"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	PHP_URL_SCHEME   = 0
	PHP_URL_HOST     = 1
	PHP_URL_PORT     = 2
	PHP_URL_USER     = 3
	PHP_URL_PASS     = 4
	PHP_URL_PATH     = 5
	PHP_URL_QUERY    = 6
	PHP_URL_FRAGMENT = 7

	PHP_QUERY_RFC1738 = 1
	PHP_QUERY_RFC3986 = 2
)

type parsedURL struct {
	hasScheme   bool
	scheme      string
	hasHost     bool
	host        string
	hasPort     bool
	port        int64
	hasUser     bool
	user        string
	hasPass     bool
	pass        string
	hasPath     bool
	path        string
	hasQuery    bool
	query       string
	hasFragment bool
	fragment    string
}

func isSchemeChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.'
}

func parsePHPURL(raw string) (parsedURL, bool) {
	var out parsedURL
	if raw == "" {
		out.hasPath = true
		out.path = ""
		return out, true
	}

	rest := raw
	if fragIdx := strings.IndexByte(rest, '#'); fragIdx >= 0 {
		out.hasFragment = true
		out.fragment = rest[fragIdx+1:]
		rest = rest[:fragIdx]
	}
	if qIdx := strings.IndexByte(rest, '?'); qIdx >= 0 {
		out.hasQuery = true
		out.query = rest[qIdx+1:]
		rest = rest[:qIdx]
	}

	if colon := strings.IndexByte(rest, ':'); colon > 0 {
		validScheme := true
		for i := range colon {
			if !isSchemeChar(rest[i]) {
				validScheme = false
				break
			}
		}
		if validScheme {
			after := rest[colon+1:]
			if strings.HasPrefix(after, "//") {
				out.hasScheme = true
				out.scheme = rest[:colon]
				rest = after
			} else if !strings.Contains(after, "/") {
				out.hasScheme = true
				out.scheme = rest[:colon]
				if after != "" {
					out.hasPath = true
					out.path = after
				}
				return out, true
			} else {
				// scheme:path/subpath without //
				out.hasScheme = true
				out.scheme = rest[:colon]
				out.hasPath = true
				out.path = after
				return out, true
			}
		}
	}

	if strings.HasPrefix(rest, "//") {
		rest = rest[2:]
		authEnd := len(rest)
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			authEnd = slash
		}
		auth := rest[:authEnd]
		pathPart := rest[authEnd:]
		if pathPart != "" {
			out.hasPath = true
			out.path = pathPart
		}
		if auth == "" {
			return parsedURL{}, false
		}
		hostPort := auth
		if at := strings.LastIndexByte(auth, '@'); at >= 0 {
			userInfo := auth[:at]
			hostPort = auth[at+1:]
			out.hasUser = true
			if before, after, ok := strings.Cut(userInfo, ":"); ok {
				out.user = before
				out.hasPass = true
				out.pass = after
			} else {
				out.user = userInfo
			}
		}
		if hostPort == "" {
			return parsedURL{}, false
		}
		var host, portStr string
		hasColon := false
		if strings.HasPrefix(hostPort, "[") {
			closeBr := strings.IndexByte(hostPort, ']')
			if closeBr < 0 {
				return parsedURL{}, false
			}
			host = hostPort[:closeBr+1]
			rem := hostPort[closeBr+1:]
			if rem != "" {
				if rem[0] != ':' {
					return parsedURL{}, false
				}
				hasColon = true
				portStr = rem[1:]
			}
		} else {
			if pColon := strings.LastIndexByte(hostPort, ':'); pColon >= 0 {
				host = hostPort[:pColon]
				hasColon = true
				portStr = hostPort[pColon+1:]
			} else {
				host = hostPort
			}
		}
		if host == "" {
			return parsedURL{}, false
		}
		out.hasHost = true
		out.host = host
		if hasColon && portStr != "" {
			for i := 0; i < len(portStr); i++ {
				if portStr[i] < '0' || portStr[i] > '9' {
					return parsedURL{}, false
				}
			}
			pVal, err := strconv.ParseInt(portStr, 10, 64)
			if err != nil || pVal < 0 || pVal > 65535 {
				return parsedURL{}, false
			}
			out.hasPort = true
			out.port = pVal
		}
		return out, true
	}

	if rest != "" {
		out.hasPath = true
		out.path = rest
	}
	return out, true
}

func nativeParseURL(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("parse_url", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	raw, err := asString(z, args[0], "parse_url", "1", "url")
	if err != nil {
		return types.Value{}, err
	}
	comp, err := optInt(z, args, 1, -1, "parse_url", "2", "component")
	if err != nil {
		return types.Value{}, err
	}
	if comp < -1 || comp > PHP_URL_FRAGMENT {
		return types.Value{}, ValueError{"parse_url(): Argument #2 ($component) must be a valid URL component identifier, " + strconv.FormatInt(comp, 10) + " given"}
	}
	// Clone raw so slices inside u do not borrow z's mutable memory across allocations.
	u, ok := parsePHPURL(strings.Clone(raw))
	if !ok {
		return types.NewBool(false), nil
	}
	switch comp {
	case -1:
		arr, err := types.NewArray(z)
		if err != nil {
			return types.Value{}, err
		}
		setStr := func(k, val string) error {
			sv, err := newStr(z, val)
			if err != nil {
				return err
			}
			err = setStrKey(z, &arr, k, sv)
			types.ReleaseValue(z, &sv)
			return err
		}
		if u.hasScheme {
			if err := setStr("scheme", u.scheme); err != nil {
				types.ReleaseArray(z, &arr)
				return types.Value{}, err
			}
		}
		if u.hasHost {
			if err := setStr("host", u.host); err != nil {
				types.ReleaseArray(z, &arr)
				return types.Value{}, err
			}
		}
		if u.hasPort {
			if err := setStrKey(z, &arr, "port", types.NewLong(u.port)); err != nil {
				types.ReleaseArray(z, &arr)
				return types.Value{}, err
			}
		}
		if u.hasUser {
			if err := setStr("user", u.user); err != nil {
				types.ReleaseArray(z, &arr)
				return types.Value{}, err
			}
		}
		if u.hasPass {
			if err := setStr("pass", u.pass); err != nil {
				types.ReleaseArray(z, &arr)
				return types.Value{}, err
			}
		}
		if u.hasPath {
			if err := setStr("path", u.path); err != nil {
				types.ReleaseArray(z, &arr)
				return types.Value{}, err
			}
		}
		if u.hasQuery {
			if err := setStr("query", u.query); err != nil {
				types.ReleaseArray(z, &arr)
				return types.Value{}, err
			}
		}
		if u.hasFragment {
			if err := setStr("fragment", u.fragment); err != nil {
				types.ReleaseArray(z, &arr)
				return types.Value{}, err
			}
		}
		return arr, nil
	case PHP_URL_SCHEME:
		if !u.hasScheme {
			return types.NewNull(), nil
		}
		return newStr(z, u.scheme)
	case PHP_URL_HOST:
		if !u.hasHost {
			return types.NewNull(), nil
		}
		return newStr(z, u.host)
	case PHP_URL_PORT:
		if !u.hasPort {
			return types.NewNull(), nil
		}
		return types.NewLong(u.port), nil
	case PHP_URL_USER:
		if !u.hasUser {
			return types.NewNull(), nil
		}
		return newStr(z, u.user)
	case PHP_URL_PASS:
		if !u.hasPass {
			return types.NewNull(), nil
		}
		return newStr(z, u.pass)
	case PHP_URL_PATH:
		if !u.hasPath {
			return types.NewNull(), nil
		}
		return newStr(z, u.path)
	case PHP_URL_QUERY:
		if !u.hasQuery {
			return types.NewNull(), nil
		}
		return newStr(z, u.query)
	case PHP_URL_FRAGMENT:
		if !u.hasFragment {
			return types.NewNull(), nil
		}
		return newStr(z, u.fragment)
	default:
		return types.NewNull(), nil
	}
}

func phpURLDecode(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '+' {
			b.WriteByte(' ')
		} else if c == '%' && i+2 < len(s) {
			h1, ok1 := fromHex(s[i+1])
			h2, ok2 := fromHex(s[i+2])
			if ok1 && ok2 {
				b.WriteByte(h1<<4 | h2)
				i += 2
				continue
			}
			b.WriteByte(c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func fromHex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

const hexUpper = "0123456789ABCDEF"

func phpURLEncode(s string, encType int64) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || (encType == PHP_QUERY_RFC3986 && c == '~') {
			b.WriteByte(c)
		} else if c == ' ' && encType == PHP_QUERY_RFC1738 {
			b.WriteByte('+')
		} else {
			b.WriteByte('%')
			b.WriteByte(hexUpper[c>>4])
			b.WriteByte(hexUpper[c&0x0F])
		}
	}
	return b.String()
}

// ParseStr décode une chaîne de requête HTTP selon les règles exactes de PHP 8.3
// (support des clés imbriquées a[b][], mangling des espaces/points en '_' sur la clé racine)
// et retourne un tableau types.Value (TypeArray) alloué dans z.
func ParseStr(z *zone.Zone, query string) (types.Value, error) {
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	if err := ParseStrInto(z, query, &arr); err != nil {
		types.ReleaseArray(z, &arr)
		return types.Value{}, err
	}
	return arr, nil
}

// ParseStrInto peuple le tableau existant dst à partir de la chaîne de requête query.
func ParseStrInto(z *zone.Zone, query string, dst *types.Value) error {
	if query == "" {
		return nil
	}
	query = strings.Clone(query)
	for pair := range strings.SplitSeq(query, "&") {
		if pair == "" {
			continue
		}
		var rawKey, rawVal string
		if before, after, ok := strings.Cut(pair, "="); ok {
			rawKey = before
			rawVal = after
		} else {
			rawKey = pair
			rawVal = ""
		}
		key := phpURLDecode(rawKey)
		valStr := phpURLDecode(rawVal)
		if err := insertParsedPair(z, dst, key, valStr); err != nil {
			return err
		}
	}
	return nil
}

func insertParsedPair(z *zone.Zone, root *types.Value, key, valStr string) error {
	val, err := newTaintedStr(z, valStr)
	if err != nil {
		return err
	}
	defer types.ReleaseValue(z, &val)
	return insertParsedValue(z, root, key, val)
}

func insertParsedValue(z *zone.Zone, root *types.Value, key string, val types.Value) error {
	// Retirer les espaces de tête
	i := 0
	for i < len(key) && key[i] == ' ' {
		i++
	}
	key = key[i:]
	if key == "" || key[0] == '[' {
		// En PHP, un nom de variable vide avant '[' (ex. "[x=2" ou "[]=next") est ignoré.
		return nil
	}

	// Extraire le nom de variable racine et les éventuels segments [k1][k2]...
	var top string
	var brackets []string

	openIdx := strings.IndexByte(key, '[')
	if openIdx > 0 {
		closeIdx := strings.IndexByte(key[openIdx+1:], ']')
		if closeIdx < 0 {
			// Crochet ouvrant non fermé : mangling complet en nom plat
			var sb strings.Builder
			sb.Grow(len(key))
			for j := 0; j < len(key); j++ {
				c := key[j]
				if c == ' ' || c == '.' || c == '[' {
					sb.WriteByte('_')
				} else {
					sb.WriteByte(c)
				}
			}
			top = sb.String()
		} else {
			// Mangle uniquement la partie avant le premier '['
			var sb strings.Builder
			sb.Grow(openIdx)
			for j := range openIdx {
				c := key[j]
				if c == ' ' || c == '.' || c == '[' {
					sb.WriteByte('_')
				} else {
					sb.WriteByte(c)
				}
			}
			top = sb.String()
			rem := key[openIdx:]
			for len(rem) > 0 && rem[0] == '[' {
				cIdx := strings.IndexByte(rem[1:], ']')
				if cIdx < 0 {
					break
				}
				brackets = append(brackets, rem[1:1+cIdx])
				rem = rem[1+cIdx+1:]
			}
		}
	} else {
		var sb strings.Builder
		sb.Grow(len(key))
		for j := 0; j < len(key); j++ {
			c := key[j]
			if c == ' ' || c == '.' || c == '[' {
				sb.WriteByte('_')
			} else {
				sb.WriteByte(c)
			}
		}
		top = sb.String()
	}

	if top == "" {
		return nil
	}

	if len(brackets) == 0 {
		return setStrKey(z, root, top, val)
	}
	return setNestedPath(z, root, top, brackets, val)
}

func setNestedPath(z *zone.Zone, parent *types.Value, stepKey string, rest []string, leaf types.Value) error {
	var kVal types.Value
	var err error
	isAppend := stepKey == ""
	if !isAppend {
		kVal, err = newStr(z, stepKey)
		if err != nil {
			return err
		}
		defer types.ReleaseValue(z, &kVal)
	}

	if len(rest) == 0 {
		if isAppend {
			return types.ArrayAppend(z, parent, leaf)
		}
		return types.ArraySet(z, parent, kVal, leaf, nil)
	}

	var child types.Value
	hasChild := false
	if !isAppend {
		if existing, ok, err := types.ArrayGet(z, *parent, kVal, nil); err == nil && ok {
			existing = deref(z, existing)
			if existing.Type == types.TypeArray {
				child = types.RetainValue(z, existing)
				hasChild = true
			}
		}
	}
	if !hasChild {
		child, err = types.NewArray(z)
		if err != nil {
			return err
		}
	}
	defer types.ReleaseValue(z, &child)

	if err := setNestedPath(z, &child, rest[0], rest[1:], leaf); err != nil {
		return err
	}
	if isAppend {
		return types.ArrayAppend(z, parent, child)
	}
	return types.ArraySet(z, parent, kVal, child, nil)
}

func nativeParseStr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("parse_str", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	str, err := asString(z, args[0], "parse_str", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	arr, err := ParseStr(z, str)
	if err != nil {
		return types.Value{}, err
	}
	defer types.ReleaseValue(z, &arr)
	writeRef(z, args[1], arr)
	return types.NewNull(), nil
}

// HTTPBuildQuery encode récursivement un tableau PHP en chaîne de requête URL.
func HTTPBuildQuery(z *zone.Zone, data types.Value, numericPrefix, argSep string, encType int64) (string, error) {
	data = deref(z, data)
	if data.Type != types.TypeArray {
		return "", typeErr("http_build_query", "1", "data", "array", typeKind(z, data))
	}
	if encType != PHP_QUERY_RFC1738 && encType != PHP_QUERY_RFC3986 {
		return "", ValueError{"http_build_query(): Argument #4 ($encoding_type) must be either PHP_QUERY_RFC1738 or PHP_QUERY_RFC3986"}
	}
	var parts []string
	var buildErr error
	types.ArrayForEach(z, data, func(k, v types.Value) bool {
		v = deref(z, v)
		if v.IsNull() || v.IsUndef() {
			return true
		}
		var prefix string
		if k.Type == types.TypeLong {
			prefix = numericPrefix + strconv.FormatInt(k.ToLong(), 10)
		} else {
			prefix = types.StringData(z, k)
		}
		if err := buildQueryRecursive(z, prefix, v, encType, &parts); err != nil {
			buildErr = err
			return false
		}
		return true
	})
	if buildErr != nil {
		return "", buildErr
	}
	return strings.Join(parts, argSep), nil
}

func buildQueryRecursive(z *zone.Zone, prefix string, v types.Value, encType int64, parts *[]string) error {
	v = deref(z, v)
	switch v.Type {
	case types.TypeNull, types.TypeUndef:
		return nil
	case types.TypeArray:
		var subErr error
		types.ArrayForEach(z, v, func(k, child types.Value) bool {
			child = deref(z, child)
			if child.IsNull() || child.IsUndef() {
				return true
			}
			var keyStr string
			if k.Type == types.TypeLong {
				keyStr = strconv.FormatInt(k.ToLong(), 10)
			} else {
				keyStr = types.StringData(z, k)
			}
			nextPrefix := prefix + "[" + keyStr + "]"
			if err := buildQueryRecursive(z, nextPrefix, child, encType, parts); err != nil {
				subErr = err
				return false
			}
			return true
		})
		return subErr
	case types.TypeFalse:
		*parts = append(*parts, phpURLEncode(prefix, encType)+"=0")
		return nil
	case types.TypeTrue:
		*parts = append(*parts, phpURLEncode(prefix, encType)+"=1")
		return nil
	default:
		valStr, err := asString(z, v, "http_build_query", "1", "data")
		if err != nil {
			return err
		}
		*parts = append(*parts, phpURLEncode(prefix, encType)+"="+phpURLEncode(valStr, encType))
		return nil
	}
}

func nativeHTTPBuildQuery(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("http_build_query", len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	numPrefix, err := optString(z, args, 1, "", "http_build_query", "2", "numeric_prefix")
	if err != nil {
		return types.Value{}, err
	}
	argSep, err := optString(z, args, 2, "&", "http_build_query", "3", "arg_separator")
	if err != nil {
		return types.Value{}, err
	}
	encType, err := optInt(z, args, 3, PHP_QUERY_RFC1738, "http_build_query", "4", "encoding_type")
	if err != nil {
		return types.Value{}, err
	}
	res, err := HTTPBuildQuery(z, args[0], numPrefix, argSep, encType)
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, res)
}
