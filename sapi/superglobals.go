package sapi

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/streams"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// ServerConfig contient les métadonnées de serveur injectées dans $_SERVER
// ainsi que les limites de taille des téléversements HTTP.
type ServerConfig struct {
	DocumentRoot      string
	ScriptName        string
	ScriptFilename    string
	ServerName        string
	ServerPort        string
	UploadMaxFileSize int64
	PostMaxSize       int64
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

	// Lecture du corps brut php://input et construction de $_POST / $_FILES
	var rawBody []byte
	var readBodyErr error
	if r.Body != nil {
		rawBody, readBodyErr = io.ReadAll(r.Body)
	}
	streams.SetInput(z, rawBody)

	postArr, err := types.NewArray(z)
	if err != nil {
		return err
	}
	filesArr, err := types.NewArray(z)
	if err != nil {
		return err
	}
	ct := r.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(ct)
	if cfg.PostMaxSize > 0 && int64(len(rawBody)) > cfg.PostMaxSize {
		// Au-delà de post_max_size, PHP laisse $_POST et $_FILES vides.
	} else {
		switch strings.ToLower(mediaType) {
		case "application/x-www-form-urlencoded":
			if readBodyErr != nil {
				return readBodyErr
			}
			if err := ParseStrInto(z, string(rawBody), &postArr); err != nil {
				return err
			}
		case "multipart/form-data":
			if boundary := params["boundary"]; boundary != "" {
				mr := multipart.NewReader(bytes.NewReader(rawBody), boundary)
				var maxFormFileSize int64
				for {
					part, err := mr.NextPart()
					if err != nil {
						break
					}
					formName := part.FormName()
					fileName := part.FileName()
					_, dispParams, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
					_, hasFileParam := dispParams["filename"]
					partType := part.Header.Get("Content-Type")
					if partType == "" {
						partType = "application/octet-stream"
					}
					partBytes, partReadErr := io.ReadAll(part)
					_ = part.Close()
					if formName == "" {
						continue
					}
					if !hasFileParam && fileName == "" {
						if partReadErr != nil || readBodyErr != nil {
							continue
						}
						valStr := string(partBytes)
						if strings.EqualFold(formName, "MAX_FILE_SIZE") {
							if v, perr := strconv.ParseInt(strings.TrimSpace(valStr), 10, 64); perr == nil && v > 0 {
								maxFormFileSize = v
							}
						}
						if err := insertParsedPair(z, &postArr, formName, valStr); err != nil {
							return err
						}
						continue
					}

					tmpPath := ""
					uploadErr := int64(0)
					fileSize := int64(len(partBytes))
					switch {
					case partReadErr != nil || readBodyErr != nil:
						uploadErr = 3 // UPLOAD_ERR_PARTIAL
						fileSize = 0
					case fileName == "":
						uploadErr = 4 // UPLOAD_ERR_NO_FILE
						fileSize = 0
						partType = ""
					case cfg.UploadMaxFileSize > 0 && fileSize > cfg.UploadMaxFileSize:
						uploadErr = 1 // UPLOAD_ERR_INI_SIZE
						fileSize = 0
						partType = ""
					case maxFormFileSize > 0 && fileSize > maxFormFileSize:
						uploadErr = 2 // UPLOAD_ERR_FORM_SIZE
						fileSize = 0
						partType = ""
					default:
						tmpFile, terr := os.CreateTemp("", "php127_upload_*")
						if terr != nil {
							uploadErr = 6 // UPLOAD_ERR_NO_TMP_DIR
							fileSize = 0
						} else {
							tmpPath = tmpFile.Name()
							_, werr := tmpFile.Write(partBytes)
							cerr := tmpFile.Close()
							if werr != nil || cerr != nil {
								uploadErr = 7 // UPLOAD_ERR_CANT_WRITE
								_ = os.Remove(tmpPath)
								tmpPath = ""
								fileSize = 0
							} else {
								stdlib.RegisterUploadedFile(z, tmpPath)
							}
						}
					}
					if err := insertUploadedFileEntry(z, &filesArr, formName, fileName, partType, tmpPath, uploadErr, fileSize); err != nil {
						return err
					}
				}
			}
		}
	}
	reqCtx.Post = postArr
	reqCtx.Files = filesArr

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

func insertUploadedFileEntry(z *zone.Zone, filesArr *types.Value, formName, fileName, partType, tmpPath string, uploadErr, fileSize int64) error {
	trimmed := strings.TrimLeft(formName, " ")
	if trimmed == "" || trimmed[0] == '[' {
		return nil
	}
	openIdx := strings.IndexByte(trimmed, '[')
	if openIdx > 0 && strings.IndexByte(trimmed[openIdx+1:], ']') >= 0 {
		rootName := trimmed[:openIdx]
		restBrackets := trimmed[openIdx:]
		for _, kv := range [][2]string{
			{"name", fileName},
			{"full_path", fileName},
			{"type", partType},
			{"tmp_name", tmpPath},
		} {
			sv, err := newStr(z, kv[1])
			if err != nil {
				return err
			}
			err = insertParsedValue(z, filesArr, rootName+"["+kv[0]+"]"+restBrackets, sv)
			types.ReleaseValue(z, &sv)
			if err != nil {
				return err
			}
		}
		if err := insertParsedValue(z, filesArr, rootName+"[error]"+restBrackets, types.NewLong(uploadErr)); err != nil {
			return err
		}
		return insertParsedValue(z, filesArr, rootName+"[size]"+restBrackets, types.NewLong(fileSize))
	}

	item, err := types.NewArray(z)
	if err != nil {
		return err
	}
	defer types.ReleaseValue(z, &item)
	for _, kv := range [][2]string{
		{"name", fileName},
		{"full_path", fileName},
		{"type", partType},
		{"tmp_name", tmpPath},
	} {
		sv, err := newStr(z, kv[1])
		if err != nil {
			return err
		}
		err = setStrKey(z, &item, kv[0], sv)
		types.ReleaseValue(z, &sv)
		if err != nil {
			return err
		}
	}
	if err := setStrKey(z, &item, "error", types.NewLong(uploadErr)); err != nil {
		return err
	}
	if err := setStrKey(z, &item, "size", types.NewLong(fileSize)); err != nil {
		return err
	}
	return insertParsedValue(z, filesArr, trimmed, item)
}

