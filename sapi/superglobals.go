package sapi

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// ServerConfig contient les métadonnées de serveur injectées dans $_SERVER.
type ServerConfig struct {
	DocumentRoot   string
	ScriptName     string
	ScriptFilename string
	ServerName     string
	ServerPort     string
}

func upperSnakeHeader(name string) string {
	var b strings.Builder
	b.Grow(5 + len(name))
	b.WriteString("HTTP_")
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' {
			b.WriteByte(c - ('a' - 'A'))
		} else if c == '-' {
			b.WriteByte('_')
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// PopulateSuperglobals construit $_SERVER, $_GET, $_POST, $_COOKIE et $_REQUEST
// dans la Zone z de la requête et remplit reqCtx.
func PopulateSuperglobals(z *zone.Zone, r *http.Request, cfg ServerConfig, reqCtx *RequestContext) error {
	now := time.Now()
	srv, err := types.NewArray(z)
	if err != nil {
		return err
	}
	setSrvStr := func(k, v string) error {
		sv, err := newStr(z, v)
		if err != nil {
			return err
		}
		defer types.ReleaseValue(z, &sv)
		return setStrKey(z, &srv, k, sv)
	}

	method := r.Method
	if method == "" {
		method = "GET"
	}
	reqURI := r.RequestURI
	if reqURI == "" && r.URL != nil {
		reqURI = r.URL.RequestURI()
	}
	queryStr := ""
	if r.URL != nil {
		queryStr = r.URL.RawQuery
	}
	proto := r.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	scriptName := cfg.ScriptName
	if scriptName == "" && r.URL != nil {
		scriptName = r.URL.Path
		if scriptName == "" {
			scriptName = "/index.php"
		}
	}
	docRoot := cfg.DocumentRoot
	if docRoot == "" {
		docRoot = "/var/www/html"
	}
	scriptFilename := cfg.ScriptFilename
	if scriptFilename == "" {
		scriptFilename = strings.TrimRight(docRoot, "/") + "/" + strings.TrimLeft(scriptName, "/")
	}
	srvName := cfg.ServerName
	srvPort := cfg.ServerPort
	if r.Host != "" {
		if h, p, err := net.SplitHostPort(r.Host); err == nil {
			if srvName == "" {
				srvName = h
			}
			if srvPort == "" {
				srvPort = p
			}
		} else if srvName == "" {
			srvName = r.Host
		}
	}
	if srvName == "" {
		srvName = "localhost"
	}
	if srvPort == "" {
		srvPort = "80"
	}

	remoteAddr, remotePort := "127.0.0.1", "0"
	if r.RemoteAddr != "" {
		if h, p, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			remoteAddr, remotePort = h, p
		} else {
			remoteAddr = r.RemoteAddr
		}
	}

	for _, pair := range [][2]string{
		{"REQUEST_METHOD", method},
		{"REQUEST_URI", reqURI},
		{"QUERY_STRING", queryStr},
		{"SCRIPT_NAME", scriptName},
		{"SCRIPT_FILENAME", scriptFilename},
		{"DOCUMENT_ROOT", docRoot},
		{"SERVER_NAME", srvName},
		{"SERVER_PORT", srvPort},
		{"SERVER_PROTOCOL", proto},
		{"REMOTE_ADDR", remoteAddr},
		{"REMOTE_PORT", remotePort},
	} {
		if err := setSrvStr(pair[0], pair[1]); err != nil {
			return err
		}
	}

	if ct := r.Header.Get("Content-Type"); ct != "" {
		if err := setSrvStr("CONTENT_TYPE", ct); err != nil {
			return err
		}
	}
	if cl := r.Header.Get("Content-Length"); cl != "" {
		if err := setSrvStr("CONTENT_LENGTH", cl); err != nil {
			return err
		}
	} else if r.ContentLength >= 0 {
		if err := setSrvStr("CONTENT_LENGTH", strconv.FormatInt(r.ContentLength, 10)); err != nil {
			return err
		}
	}

	if err := setStrKey(z, &srv, "REQUEST_TIME", types.NewLong(now.Unix())); err != nil {
		return err
	}
	if err := setStrKey(z, &srv, "REQUEST_TIME_FLOAT", types.NewDouble(float64(now.UnixNano())/1e9)); err != nil {
		return err
	}

	if r.Host != "" {
		if err := setSrvStr("HTTP_HOST", r.Host); err != nil {
			return err
		}
	}
	for name, vals := range r.Header {
		if len(vals) == 0 {
			continue
		}
		key := upperSnakeHeader(name)
		if err := setSrvStr(key, strings.Join(vals, ", ")); err != nil {
			return err
		}
	}
	reqCtx.Server = srv

	// $_GET
	getArr, err := ParseStr(z, queryStr)
	if err != nil {
		return err
	}
	reqCtx.Get = getArr

	// Lecture du corps brut php://input et construction de $_POST
	var rawBody []byte
	if r.Body != nil {
		rawBody, _ = io.ReadAll(r.Body)
	}
	reqCtx.RawInput = rawBody

	postArr, err := types.NewArray(z)
	if err != nil {
		return err
	}
	ct := r.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(ct)
	switch strings.ToLower(mediaType) {
	case "application/x-www-form-urlencoded":
		if err := ParseStrInto(z, string(rawBody), &postArr); err != nil {
			return err
		}
	case "multipart/form-data":
		if boundary := params["boundary"]; boundary != "" {
			mr := multipart.NewReader(bytes.NewReader(rawBody), boundary)
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				formName := part.FormName()
				fileName := part.FileName()
				partBytes, _ := io.ReadAll(part)
				_ = part.Close()
				if formName != "" && fileName == "" {
					if err := insertParsedPair(z, &postArr, formName, string(partBytes)); err != nil {
						return err
					}
				}
			}
		}
	}
	reqCtx.Post = postArr

	// $_COOKIE
	cookieArr, err := types.NewArray(z)
	if err != nil {
		return err
	}
	for _, c := range r.Cookies() {
		decVal := phpURLDecode(c.Value)
		if err := insertParsedPair(z, &cookieArr, c.Name, decVal); err != nil {
			return err
		}
	}
	reqCtx.Cookie = cookieArr

	// $_REQUEST : fusion GPC (GET, puis POST, puis COOKIE)
	reqArr, err := types.NewArray(z)
	if err != nil {
		return err
	}
	for _, src := range []types.Value{getArr, postArr, cookieArr} {
		var copyErr error
		types.ArrayForEach(z, src, func(k, v types.Value) bool {
			if err := types.ArraySet(z, &reqArr, k, v, nil); err != nil {
				copyErr = err
				return false
			}
			return true
		})
		if copyErr != nil {
			return copyErr
		}
	}
	reqCtx.RequestVars = reqArr
	return nil
}
