package sapi

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// HeaderEntry représente une ligne d'en-tête HTTP émise par header() ou setcookie().
type HeaderEntry struct {
	Name  string
	Value string
	Raw   string
}

// RequestContext porte l'état HTTP associé à une Zone de requête pendant ServeHTTP.
type RequestContext struct {
	Request      *http.Request
	RawInput     []byte
	Server       types.Value
	Get          types.Value
	Post         types.Value
	Cookie       types.Value
	RequestVars  types.Value
	StatusCode   int
	Headers      []HeaderEntry
	HeadersSent  bool
	SentFilename string
	SentLine     int64
}

var (
	ctxMu  sync.RWMutex
	ctxMap = make(map[uintptr]*RequestContext)
)

// BindContext associe un RequestContext à la Zone z pour la durée de la requête.
func BindContext(z *zone.Zone, ctx *RequestContext) {
	if z == nil {
		return
	}
	key := uintptr(unsafe.Pointer(z))
	ctxMu.Lock()
	if ctx == nil {
		delete(ctxMap, key)
	} else {
		ctxMap[key] = ctx
	}
	ctxMu.Unlock()
}

// ActiveContext retourne le RequestContext actif associé à la Zone z, ou un
// contexte local rattaché à z s'il n'en existe pas encore.
func ActiveContext(z *zone.Zone) *RequestContext {
	if z == nil {
		return nil
	}
	key := uintptr(unsafe.Pointer(z))
	ctxMu.RLock()
	ctx := ctxMap[key]
	ctxMu.RUnlock()
	if ctx != nil {
		return ctx
	}
	ctxMu.Lock()
	defer ctxMu.Unlock()
	if ctx = ctxMap[key]; ctx == nil {
		ctx = &RequestContext{StatusCode: http.StatusOK}
		ctxMap[key] = ctx
	}
	return ctx
}

func nativeHeader(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("header", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	line, err := asString(z, args[0], "header", "1", "header")
	if err != nil {
		return types.Value{}, err
	}
	replace := optBool(z, args, 1, true)
	respCode, err := optInt(z, args, 2, 0, "header", "3", "response_code")
	if err != nil {
		return types.Value{}, err
	}
	if strings.ContainsAny(line, "\r\n\x00") {
		return types.Value{}, ValueError{"Header may not contain more than a single header, new line detected"}
	}
	ctx := ActiveContext(z)
	if ctx == nil || ctx.HeadersSent {
		return types.NewNull(), nil
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return types.NewNull(), nil
	}
	if len(trimmed) >= 5 && strings.EqualFold(trimmed[:5], "HTTP/") {
		fields := strings.Fields(trimmed)
		if len(fields) >= 2 {
			if code, err := strconv.Atoi(fields[1]); err == nil && code >= 100 && code <= 599 {
				ctx.StatusCode = code
			}
		}
		if respCode > 0 {
			ctx.StatusCode = int(respCode)
		}
		return types.NewNull(), nil
	}

	colon := strings.IndexByte(trimmed, ':')
	if colon <= 0 {
		if respCode > 0 {
			ctx.StatusCode = int(respCode)
		}
		return types.NewNull(), nil
	}
	name := strings.TrimSpace(trimmed[:colon])
	val := strings.TrimSpace(trimmed[colon+1:])
	raw := name + ": " + val

	isSetCookie := strings.EqualFold(name, "Set-Cookie")
	if replace && !isSetCookie {
		replaced := false
		out := ctx.Headers[:0]
		for _, h := range ctx.Headers {
			if strings.EqualFold(h.Name, name) {
				if !replaced {
					out = append(out, HeaderEntry{Name: name, Value: val, Raw: raw})
					replaced = true
				}
				continue
			}
			out = append(out, h)
		}
		if !replaced {
			out = append(out, HeaderEntry{Name: name, Value: val, Raw: raw})
		}
		ctx.Headers = out
	} else {
		ctx.Headers = append(ctx.Headers, HeaderEntry{Name: name, Value: val, Raw: raw})
	}

	if respCode > 0 {
		ctx.StatusCode = int(respCode)
	} else if strings.EqualFold(name, "Location") && val != "" {
		if ctx.StatusCode == http.StatusOK || ctx.StatusCode == 0 {
			ctx.StatusCode = http.StatusFound
		}
	}
	return types.NewNull(), nil
}

func nativeHeadersList(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("headers_list", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	ctx := ActiveContext(z)
	if ctx == nil {
		return arr, nil
	}
	for _, h := range ctx.Headers {
		sv, err := newStr(z, h.Raw)
		if err != nil {
			types.ReleaseArray(z, &arr)
			return types.Value{}, err
		}
		err = types.ArrayAppend(z, &arr, sv)
		types.ReleaseValue(z, &sv)
		if err != nil {
			types.ReleaseArray(z, &arr)
			return types.Value{}, err
		}
	}
	return arr, nil
}

func nativeHeadersSent(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("headers_sent", len(args), 0, 2); err != nil {
		return types.Value{}, err
	}
	ctx := ActiveContext(z)
	sent := ctx != nil && ctx.HeadersSent
	fname := ""
	line := int64(0)
	if sent {
		fname = ctx.SentFilename
		line = ctx.SentLine
	}
	if len(args) >= 1 && args[0].Type == types.TypeReference {
		fv, err := newStr(z, fname)
		if err != nil {
			return types.Value{}, err
		}
		writeRef(z, args[0], fv)
		types.ReleaseValue(z, &fv)
	}
	if len(args) >= 2 && args[1].Type == types.TypeReference {
		writeRef(z, args[1], types.NewLong(line))
	}
	return types.NewBool(sent), nil
}

func nativeHeaderRemove(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("header_remove", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	ctx := ActiveContext(z)
	if ctx == nil || ctx.HeadersSent {
		return types.NewNull(), nil
	}
	if len(args) == 0 || missing(deref(z, args[0])) {
		ctx.Headers = ctx.Headers[:0]
		return types.NewNull(), nil
	}
	name, err := asString(z, args[0], "header_remove", "1", "name")
	if err != nil {
		return types.Value{}, err
	}
	out := ctx.Headers[:0]
	for _, h := range ctx.Headers {
		if !strings.EqualFold(h.Name, name) {
			out = append(out, h)
		}
	}
	ctx.Headers = out
	return types.NewNull(), nil
}

func nativeHTTPResponseCode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("http_response_code", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	ctx := ActiveContext(z)
	if ctx == nil {
		return types.NewBool(false), nil
	}
	if ctx.StatusCode == 0 {
		ctx.StatusCode = http.StatusOK
	}
	prev := ctx.StatusCode
	if len(args) == 1 && !missing(deref(z, args[0])) {
		code, err := asInt(z, args[0], "http_response_code", "1", "response_code")
		if err != nil {
			return types.Value{}, err
		}
		if code > 0 {
			ctx.StatusCode = int(code)
		}
	}
	return types.NewLong(int64(prev)), nil
}

func nativeSetCookie(raw bool) func(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	fnName := "setcookie"
	if raw {
		fnName = "setrawcookie"
	}
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(fnName, len(args), 1, 7); err != nil {
			return types.Value{}, err
		}
		name, err := asString(z, args[0], fnName, "1", "name")
		if err != nil {
			return types.Value{}, err
		}
		if name == "" || strings.ContainsAny(name, "=,; \t\r\n\x0b\x0c") {
			return types.Value{}, ValueError{fnName + "(): Cookie names cannot contain any of the following '=,; \\t\\r\\n\\013\\014'"}
		}
		value, err := optString(z, args, 1, "", fnName, "2", "value")
		if err != nil {
			return types.Value{}, err
		}
		var expires int64
		var path, domain, sameSite string
		var secure, httpOnly bool

		if len(args) >= 3 && deref(z, args[2]).Type == types.TypeArray {
			if len(args) > 3 {
				return types.Value{}, ValueError{fnName + "(): Cannot pass arguments after the options array"}
			}
			opts := deref(z, args[2])
			var optErr error
			types.ArrayForEach(z, opts, func(k, v types.Value) bool {
				if k.Type != types.TypeString {
					return true
				}
				kName := types.StringData(z, k)
				switch kName {
				case "expires":
					expires, optErr = asInt(z, v, fnName, "3", "expires")
				case "path":
					path, optErr = asString(z, v, fnName, "3", "path")
				case "domain":
					domain, optErr = asString(z, v, fnName, "3", "domain")
				case "secure":
					secure = asBool(z, v)
				case "httponly":
					httpOnly = asBool(z, v)
				case "samesite":
					sameSite, optErr = asString(z, v, fnName, "3", "samesite")
				}
				return optErr == nil
			})
			if optErr != nil {
				return types.Value{}, optErr
			}
		} else {
			expires, err = optInt(z, args, 2, 0, fnName, "3", "expires_or_options")
			if err != nil {
				return types.Value{}, err
			}
			path, err = optString(z, args, 3, "", fnName, "4", "path")
			if err != nil {
				return types.Value{}, err
			}
			domain, err = optString(z, args, 4, "", fnName, "5", "domain")
			if err != nil {
				return types.Value{}, err
			}
			secure = optBool(z, args, 5, false)
			httpOnly = optBool(z, args, 6, false)
		}

		ctx := ActiveContext(z)
		if ctx == nil || ctx.HeadersSent {
			return types.NewBool(false), nil
		}

		var sb strings.Builder
		sb.WriteString(name)
		sb.WriteByte('=')
		if value == "" {
			sb.WriteString("deleted; expires=Thu, 01 Jan 1970 00:00:01 GMT; Max-Age=0")
		} else {
			if raw {
				if strings.ContainsAny(value, ",; \t\r\n\x0b\x0c") {
					return types.Value{}, ValueError{fnName + "(): Cookie values cannot contain any of the following ',; \\t\\r\\n\\013\\014'"}
				}
				sb.WriteString(value)
			} else {
				sb.WriteString(phpURLEncode(value, PHP_QUERY_RFC1738))
			}
			if expires > 0 {
				expTime := time.Unix(expires, 0).UTC()
				sb.WriteString("; expires=")
				sb.WriteString(expTime.Format("Mon, 02 Jan 2006 15:04:05 GMT"))
				maxAge := max(expires-time.Now().Unix(), 0)
				sb.WriteString(fmt.Sprintf("; Max-Age=%d", maxAge))
			}
		}
		if path != "" {
			sb.WriteString("; path=")
			sb.WriteString(path)
		}
		if domain != "" {
			sb.WriteString("; domain=")
			sb.WriteString(domain)
		}
		if secure {
			sb.WriteString("; secure")
		}
		if httpOnly {
			sb.WriteString("; HttpOnly")
		}
		if sameSite != "" {
			sb.WriteString("; SameSite=")
			sb.WriteString(sameSite)
		}

		val := sb.String()
		ctx.Headers = append(ctx.Headers, HeaderEntry{
			Name:  "Set-Cookie",
			Value: val,
			Raw:   "Set-Cookie: " + val,
		})
		return types.NewBool(true), nil
	}
}
