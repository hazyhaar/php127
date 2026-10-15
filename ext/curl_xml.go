package ext

import (
	"bytes"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"code.hazyhaar.fr/devhoros/php127/streams"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	ResourceKindCurl uint16 = 0x4505
	ResourceKindXML  uint16 = 0x4506
)

const (
	curlOptFile              = 10001
	curlOptURL               = 10002
	curlOptUserPwd           = 10005
	curlOptInfile            = 10009
	curlOptPostFields        = 10015
	curlOptReferer           = 10016
	curlOptUserAgent         = 10018
	curlOptCookie            = 10022
	curlOptHTTPHeader        = 10023
	curlOptWriteHeader       = 10029
	curlOptCustomRequest     = 10036
	curlOptCAInfo            = 10065
	curlOptCAPath            = 10097
	curlOptEncoding          = 10102
	curlOptReturnTransfer    = 19913
	curlOptWriteFunction     = 20011
	curlOptHeaderFunction    = 20079
	curlOptTimeout           = 13
	curlOptInfileSize        = 14
	curlOptHeader            = 42
	curlOptNobody            = 44
	curlOptPost              = 47
	curlOptFollowLocation    = 52
	curlOptPut               = 54
	curlOptSSLVerifyPeer     = 64
	curlOptMaxRedirs         = 68
	curlOptConnectTimeout    = 78
	curlOptSSLVerifyHost     = 81
	curlOptHTTPVersion       = 84
	curlOptHTTPAuth          = 107
	curlOptTimeoutMS         = 155
	curlOptConnectTimeoutMS  = 156
	curlOptProtocols         = 181
	curlOptRedirProtocols    = 182
	curlProtoHTTP            = 1
	curlProtoHTTPS           = 2
	curlProtoDefault         = curlProtoHTTP | curlProtoHTTPS
	curlInfoHeaderOut        = 2
	curlInfoEffectiveURL     = 1048577
	curlInfoContentType      = 1048594
	curlInfoRedirectURL      = 1048607
	curlInfoHTTPCode         = 2097154
	curlInfoHeaderSize       = 2097163
	curlInfoRedirectCount    = 2097172
	curlInfoTotalTime        = 3145731
	curlVersionIPV6          = 1
	curlVersionSSL           = 4
	curlVersionLibz          = 8
	curlVersionHTTP2         = 65536
	xmlErrorNone             = 0
	xmlErrorNoMemory         = 1
	xmlErrorSyntax           = 2
	xmlErrorNoElements       = 3
	xmlErrorInvalidToken     = 4
	xmlErrorUnclosedToken    = 5
	xmlErrorPartialChar      = 6
	xmlErrorTagMismatch      = 7
	xmlErrorDuplicateAttr    = 8
	xmlErrorJunkAfterDocElem = 9
	xmlErrorParamEntityRef   = 10
	xmlErrorUndefinedEntity  = 11
	xmlErrorRecursiveEntity  = 12
	xmlErrorAsyncEntity      = 13
	xmlErrorBadCharRef       = 14
	xmlErrorBinaryEntityRef  = 15
	xmlErrorAttrExtEntityRef = 16
	xmlErrorMisplacedXMLPI   = 17
	xmlErrorUnknownEncoding  = 18
	xmlErrorIncorrectEnc     = 19
	xmlErrorUnclosedCDATA    = 20
	xmlErrorExtEntityHandl   = 21
	xmlOptionCaseFolding     = 1
	xmlOptionTargetEncoding  = 2
	xmlOptionSkipTagstart    = 3
	xmlOptionSkipWhite       = 4
)

var curlXMLLongConstants = map[string]int64{
	"CURLOPT_URL":                             curlOptURL,
	"CURLOPT_RETURNTRANSFER":                  curlOptReturnTransfer,
	"CURLOPT_HEADER":                          curlOptHeader,
	"CURLOPT_NOBODY":                          curlOptNobody,
	"CURLOPT_POST":                            curlOptPost,
	"CURLOPT_PUT":                             curlOptPut,
	"CURLOPT_CUSTOMREQUEST":                   curlOptCustomRequest,
	"CURLOPT_POSTFIELDS":                      curlOptPostFields,
	"CURLOPT_HTTPHEADER":                      curlOptHTTPHeader,
	"CURLOPT_USERAGENT":                       curlOptUserAgent,
	"CURLOPT_REFERER":                         curlOptReferer,
	"CURLOPT_COOKIE":                          curlOptCookie,
	"CURLOPT_TIMEOUT":                         curlOptTimeout,
	"CURLOPT_TIMEOUT_MS":                      curlOptTimeoutMS,
	"CURLOPT_CONNECTTIMEOUT":                  curlOptConnectTimeout,
	"CURLOPT_CONNECTTIMEOUT_MS":               curlOptConnectTimeoutMS,
	"CURLOPT_FOLLOWLOCATION":                  curlOptFollowLocation,
	"CURLOPT_MAXREDIRS":                       curlOptMaxRedirs,
	"CURLOPT_SSL_VERIFYPEER":                  curlOptSSLVerifyPeer,
	"CURLOPT_SSL_VERIFYHOST":                  curlOptSSLVerifyHost,
	"CURLOPT_CAINFO":                          curlOptCAInfo,
	"CURLOPT_CAPATH":                          curlOptCAPath,
	"CURLOPT_HTTP_VERSION":                    curlOptHTTPVersion,
	"CURLOPT_ENCODING":                        curlOptEncoding,
	"CURLOPT_PROTOCOLS":                       curlOptProtocols,
	"CURLOPT_REDIR_PROTOCOLS":                 curlOptRedirProtocols,
	"CURLOPT_FILE":                            curlOptFile,
	"CURLOPT_WRITEHEADER":                     curlOptWriteHeader,
	"CURLOPT_INFILE":                          curlOptInfile,
	"CURLOPT_INFILESIZE":                      curlOptInfileSize,
	"CURLOPT_HTTPAUTH":                        curlOptHTTPAuth,
	"CURLOPT_USERPWD":                         curlOptUserPwd,
	"CURLOPT_PROXY":                           10004,
	"CURLOPT_PROXYUSERPWD":                    10006,
	"CURLOPT_PROXYPORT":                       59,
	"CURLOPT_BUFFERSIZE":                      98,
	"CURLOPT_PROXYTYPE":                       101,
	"CURLOPT_PROXYAUTH":                       111,
	"CURLOPT_WRITEFUNCTION":                   20011,
	"CURLOPT_HEADERFUNCTION":                  20079,
	"CURLPROXY_HTTP":                          0,
	"CURLPROXY_SOCKS4":                        4,
	"CURLPROXY_SOCKS5":                        5,
	"CURLINFO_EFFECTIVE_URL":                  curlInfoEffectiveURL,
	"CURLINFO_HTTP_CODE":                      curlInfoHTTPCode,
	"CURLINFO_RESPONSE_CODE":                  curlInfoHTTPCode,
	"CURLINFO_HEADER_SIZE":                    curlInfoHeaderSize,
	"CURLINFO_CONTENT_TYPE":                   curlInfoContentType,
	"CURLINFO_TOTAL_TIME":                     curlInfoTotalTime,
	"CURLINFO_REDIRECT_COUNT":                 curlInfoRedirectCount,
	"CURLINFO_REDIRECT_URL":                   curlInfoRedirectURL,
	"CURLINFO_HEADER_OUT":                     curlInfoHeaderOut,
	"CURL_HTTP_VERSION_NONE":                  0,
	"CURL_HTTP_VERSION_1_0":                   1,
	"CURL_HTTP_VERSION_1_1":                   2,
	"CURL_HTTP_VERSION_2_0":                   3,
	"CURLPROTO_HTTP":                          1,
	"CURLPROTO_HTTPS":                         2,
	"CURLAUTH_BASIC":                          1,
	"CURLAUTH_ANY":                            -17,
	"CURLE_OK":                                0,
	"CURLE_UNSUPPORTED_PROTOCOL":              1,
	"CURLE_COULDNT_RESOLVE_PROXY":             5,
	"CURLE_COULDNT_RESOLVE_HOST":              6,
	"CURLE_COULDNT_CONNECT":                   7,
	"CURLE_HTTP_NOT_FOUND":                    22,
	"CURLE_WRITE_ERROR":                       23,
	"CURLE_READ_ERROR":                        26,
	"CURLE_OUT_OF_MEMORY":                     27,
	"CURLE_OPERATION_TIMEDOUT":                28,
	"CURLE_OPERATION_TIMEOUTED":               28,
	"CURLE_SSL_CONNECT_ERROR":                 35,
	"CURLE_TOO_MANY_REDIRECTS":                47,
	"CURLE_GOT_NOTHING":                       52,
	"CURLE_SEND_ERROR":                        55,
	"CURLE_RECV_ERROR":                        56,
	"CURLE_SSL_CERTPROBLEM":                   58,
	"CURLE_SSL_CIPHER":                        59,
	"CURLE_SSL_CACERT":                        60,
	"CURLE_BAD_CONTENT_ENCODING":              61,
	"CURLM_CALL_MULTI_PERFORM":                -1,
	"CURLM_OK":                                0,
	"CURL_VERSION_SSL":                        curlVersionSSL,
	"CURL_VERSION_IPV6":                       curlVersionIPV6,
	"CURL_VERSION_LIBZ":                       curlVersionLibz,
	"CURL_VERSION_HTTP2":                      curlVersionHTTP2,
	"XML_ERROR_NONE":                          xmlErrorNone,
	"XML_ERROR_NO_MEMORY":                     xmlErrorNoMemory,
	"XML_ERROR_SYNTAX":                        xmlErrorSyntax,
	"XML_ERROR_NO_ELEMENTS":                   xmlErrorNoElements,
	"XML_ERROR_INVALID_TOKEN":                 xmlErrorInvalidToken,
	"XML_ERROR_UNCLOSED_TOKEN":                xmlErrorUnclosedToken,
	"XML_ERROR_PARTIAL_CHAR":                  xmlErrorPartialChar,
	"XML_ERROR_TAG_MISMATCH":                  xmlErrorTagMismatch,
	"XML_ERROR_DUPLICATE_ATTRIBUTE":           xmlErrorDuplicateAttr,
	"XML_ERROR_JUNK_AFTER_DOC_ELEMENT":        xmlErrorJunkAfterDocElem,
	"XML_ERROR_PARAM_ENTITY_REF":              xmlErrorParamEntityRef,
	"XML_ERROR_UNDEFINED_ENTITY":              xmlErrorUndefinedEntity,
	"XML_ERROR_RECURSIVE_ENTITY_REF":          xmlErrorRecursiveEntity,
	"XML_ERROR_ASYNC_ENTITY":                  xmlErrorAsyncEntity,
	"XML_ERROR_BAD_CHAR_REF":                  xmlErrorBadCharRef,
	"XML_ERROR_BINARY_ENTITY_REF":             xmlErrorBinaryEntityRef,
	"XML_ERROR_ATTRIBUTE_EXTERNAL_ENTITY_REF": xmlErrorAttrExtEntityRef,
	"XML_ERROR_MISPLACED_XML_PI":              xmlErrorMisplacedXMLPI,
	"XML_ERROR_UNKNOWN_ENCODING":              xmlErrorUnknownEncoding,
	"XML_ERROR_INCORRECT_ENCODING":            xmlErrorIncorrectEnc,
	"XML_ERROR_UNCLOSED_CDATA_SECTION":        xmlErrorUnclosedCDATA,
	"XML_ERROR_EXTERNAL_ENTITY_HANDLING":      xmlErrorExtEntityHandl,
	"XML_OPTION_CASE_FOLDING":                 xmlOptionCaseFolding,
	"XML_OPTION_TARGET_ENCODING":              xmlOptionTargetEncoding,
	"XML_OPTION_SKIP_TAGSTART":                xmlOptionSkipTagstart,
	"XML_OPTION_SKIP_WHITE":                   xmlOptionSkipWhite,
}

// HasCurl vérifie que le slot désigne un handle cURL actif dans la Zone.
func HasCurl(z *zone.Zone, slot uint32) bool {
	if z == nil {
		return false
	}
	st := extStateFor(z)
	if st.handles == nil {
		return false
	}
	_, ok := st.handles[slot].(*curlHandle)
	return ok
}

// HasXMLParser vérifie que le slot désigne un parseur XML actif dans la Zone.
func HasXMLParser(z *zone.Zone, slot uint32) bool {
	if z == nil {
		return false
	}
	st := extStateFor(z)
	if st.handles == nil {
		return false
	}
	_, ok := st.handles[slot].(*xmlParser)
	return ok
}

type curlHandle struct {
	url             string
	returnTransfer  bool
	includeHeader   bool
	nobody          bool
	post            bool
	put             bool
	customRequest   string
	postFields      string
	hasPostFields   bool
	postContentType string
	headers         []string
	userAgent       string
	referer         string
	cookie          string
	userPwd         string
	timeoutMS       int64
	connectMS       int64
	followLocation  bool
	maxRedirs       int64
	sslVerifyPeer   bool
	sslVerifyHost   int64
	caInfo          string
	caPath          string
	httpVersion     int64
	encoding        string
	protocols       int64
	redirProtocols  int64
	trackHeaderOut  bool
	outStream       types.Value
	headerStream    types.Value
	inStream        types.Value
	inFileSize      int64
	writeFunc       types.Value
	headerFunc      types.Value

	errno         int64
	errstr        string
	effectiveURL  string
	httpCode      int64
	headerSize    int64
	contentType   string
	totalTime     float64
	redirectCount int64
	redirectURL   string
	requestHeader string
}

func newCurlHandle(rawURL string) *curlHandle {
	return &curlHandle{
		url:            strings.Clone(rawURL),
		effectiveURL:   strings.Clone(rawURL),
		maxRedirs:      20,
		sslVerifyPeer:  true,
		sslVerifyHost:  2,
		protocols:      curlProtoDefault,
		redirProtocols: curlProtoDefault,
	}
}

func (h *curlHandle) releaseCallbacks(z *zone.Zone) {
	types.ReleaseValue(z, &h.writeFunc)
	types.ReleaseValue(z, &h.headerFunc)
}

func storeCurlHandle(z *zone.Zone, h *curlHandle) types.Value {
	st := extStateFor(z)
	if st.handles == nil {
		st.handles = make(map[uint32]any)
	}
	id := st.newHandle()
	st.handles[id] = h
	return streams.NewResource(z, ResourceKindCurl, id)
}

func getCurlArg(z *zone.Zone, v types.Value, fn string) (uint32, *curlHandle, error) {
	v = deref(z, v)
	if v.Type != types.TypeResource || v.Extra != ResourceKindCurl {
		got := typeKind(z, v)
		if v.Type == types.TypeResource {
			got = "resource"
		}
		return 0, nil, typeErr(fn, "1", "handle", "CurlHandle", got)
	}
	st := extStateFor(z)
	if st.handles != nil {
		if h, ok := st.handles[v.DataSlot].(*curlHandle); ok && h != nil {
			return v.DataSlot, h, nil
		}
	}
	return 0, nil, TypeError{fn + "(): Argument #1 ($handle) must be a valid CurlHandle"}
}

func nativeCurlInit(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("curl_init", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	var u string
	if len(args) >= 1 && !missing(deref(z, args[0])) {
		var err error
		u, err = asString(z, args[0], "curl_init", "1", "url")
		if err != nil {
			return types.Value{}, err
		}
	}
	return storeCurlHandle(z, newCurlHandle(u)), nil
}

func setCurlStreamOption(z *zone.Zone, dst *types.Value, val types.Value, fn string) error {
	if val.IsNull() || val.IsUndef() {
		*dst = types.Value{}
		return nil
	}
	if val.Type == types.TypeResource && val.Extra == streams.ResourceKindStream {
		*dst = val
		return nil
	}
	return typeErr(fn, "3", "value", "stream resource", typeKind(z, val))
}

func applyCurlOption(z *zone.Zone, h *curlHandle, opt int64, val types.Value, fn string) error {
	val = deref(z, val)
	switch opt {
	case curlOptURL:
		s, err := asString(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.url = strings.Clone(s)
		h.effectiveURL = h.url
	case curlOptReturnTransfer:
		h.returnTransfer = asBool(z, val)
	case curlOptHeader:
		h.includeHeader = asBool(z, val)
	case curlOptNobody:
		h.nobody = asBool(z, val)
	case curlOptPost:
		h.post = asBool(z, val)
	case curlOptPut:
		h.put = asBool(z, val)
	case curlOptCustomRequest:
		if val.IsNull() || val.IsUndef() {
			h.customRequest = ""
		} else {
			s, err := asString(z, val, fn, "3", "value")
			if err != nil {
				return err
			}
			h.customRequest = strings.Clone(s)
		}
	case curlOptPostFields:
		if val.Type == types.TypeArray {
			vals := url.Values{}
			var convErr error
			types.ArrayForEach(z, val, func(k, elem types.Value) bool {
				ks, err := asString(z, k, fn, "3", "value")
				if err != nil {
					convErr = err
					return false
				}
				vs, err := asString(z, elem, fn, "3", "value")
				if err != nil {
					convErr = err
					return false
				}
				vals.Add(ks, vs)
				return true
			})
			if convErr != nil {
				return convErr
			}
			h.postFields = strings.Clone(vals.Encode())
			h.hasPostFields = true
			h.postContentType = "application/x-www-form-urlencoded"
			h.post = true
		} else if val.IsNull() || val.IsUndef() {
			h.postFields = ""
			h.hasPostFields = false
			h.postContentType = ""
		} else {
			s, err := asString(z, val, fn, "3", "value")
			if err != nil {
				return err
			}
			h.postFields = strings.Clone(s)
			h.hasPostFields = true
			h.postContentType = "application/x-www-form-urlencoded"
			h.post = true
		}
	case curlOptHTTPHeader:
		if val.Type != types.TypeArray {
			return typeErr(fn, "3", "value", "array", typeKind(z, val))
		}
		var hdrs []string
		var convErr error
		types.ArrayForEach(z, val, func(_, elem types.Value) bool {
			s, err := asString(z, elem, fn, "3", "value")
			if err != nil {
				convErr = err
				return false
			}
			hdrs = append(hdrs, strings.Clone(s))
			return true
		})
		if convErr != nil {
			return convErr
		}
		h.headers = hdrs
	case curlOptUserAgent:
		s, err := asString(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.userAgent = strings.Clone(s)
	case curlOptReferer:
		s, err := asString(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.referer = strings.Clone(s)
	case curlOptCookie:
		s, err := asString(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.cookie = strings.Clone(s)
	case curlOptUserPwd:
		s, err := asString(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.userPwd = strings.Clone(s)
	case curlOptTimeout:
		n, err := asInt(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.timeoutMS = n * 1000
	case curlOptTimeoutMS:
		n, err := asInt(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.timeoutMS = n
	case curlOptConnectTimeout:
		n, err := asInt(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.connectMS = n * 1000
	case curlOptConnectTimeoutMS:
		n, err := asInt(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.connectMS = n
	case curlOptFollowLocation:
		h.followLocation = asBool(z, val)
	case curlOptMaxRedirs:
		n, err := asInt(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.maxRedirs = n
	case curlOptSSLVerifyPeer:
		h.sslVerifyPeer = asBool(z, val)
	case curlOptSSLVerifyHost:
		n, err := asInt(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.sslVerifyHost = n
	case curlOptCAInfo:
		s, err := asString(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.caInfo = strings.Clone(s)
	case curlOptCAPath:
		s, err := asString(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.caPath = strings.Clone(s)
	case curlOptHTTPVersion:
		n, err := asInt(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.httpVersion = n
	case curlOptEncoding:
		s, err := asString(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.encoding = strings.Clone(s)
	case curlInfoHeaderOut:
		h.trackHeaderOut = asBool(z, val)
	case curlOptFile:
		return setCurlStreamOption(z, &h.outStream, val, fn)
	case curlOptWriteHeader:
		return setCurlStreamOption(z, &h.headerStream, val, fn)
	case curlOptInfile:
		return setCurlStreamOption(z, &h.inStream, val, fn)
	case curlOptInfileSize:
		n, err := asInt(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.inFileSize = n
	case curlOptProtocols:
		n, err := asInt(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.protocols = n
	case curlOptRedirProtocols:
		n, err := asInt(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
		h.redirProtocols = n
	case curlOptHTTPAuth:
		_, err := asInt(z, val, fn, "3", "value")
		if err != nil {
			return err
		}
	case curlOptWriteFunction:
		setParserCallback(z, &h.writeFunc, val)
	case curlOptHeaderFunction:
		setParserCallback(z, &h.headerFunc, val)
	}
	return nil
}

func nativeCurlSetopt(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("curl_setopt", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	_, h, err := getCurlArg(z, args[0], "curl_setopt")
	if err != nil {
		return types.Value{}, err
	}
	opt, err := asInt(z, args[1], "curl_setopt", "2", "option")
	if err != nil {
		return types.Value{}, err
	}
	if err := applyCurlOption(z, h, opt, args[2], "curl_setopt"); err != nil {
		return types.Value{}, err
	}
	return types.NewBool(true), nil
}

func nativeCurlSetoptArray(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("curl_setopt_array", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, h, err := getCurlArg(z, args[0], "curl_setopt_array")
	if err != nil {
		return types.Value{}, err
	}
	opts := deref(z, args[1])
	if opts.Type != types.TypeArray {
		return types.Value{}, typeErr("curl_setopt_array", "2", "options", "array", typeKind(z, opts))
	}
	var optErr error
	types.ArrayForEach(z, opts, func(k, v types.Value) bool {
		opt, err := asInt(z, k, "curl_setopt_array", "2", "options")
		if err != nil {
			optErr = err
			return false
		}
		if err := applyCurlOption(z, h, opt, v, "curl_setopt_array"); err != nil {
			optErr = err
			return false
		}
		return true
	})
	if optErr != nil {
		return types.Value{}, optErr
	}
	return types.NewBool(true), nil
}

func formatResponseHeaderLines(resp *http.Response) ([]string, string) {
	proto := resp.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	status := resp.Status
	if status == "" {
		status = strconv.Itoa(resp.StatusCode)
	}
	lines := make([]string, 0, len(resp.Header)+2)
	lines = append(lines, proto+" "+status+"\r\n")
	keys := make([]string, 0, len(resp.Header))
	for k := range resp.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range resp.Header[k] {
			lines = append(lines, k+": "+v+"\r\n")
		}
	}
	lines = append(lines, "\r\n")
	return lines, strings.Join(lines, "")
}

func formatRequestHeaders(req *http.Request) string {
	var b strings.Builder
	method := req.Method
	if method == "" {
		method = "GET"
	}
	uri := req.URL.RequestURI()
	if uri == "" {
		uri = "/"
	}
	b.WriteString(method)
	b.WriteByte(' ')
	b.WriteString(uri)
	b.WriteString(" HTTP/1.1\r\n")
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	if host != "" {
		b.WriteString("Host: ")
		b.WriteString(host)
		b.WriteString("\r\n")
	}
	for k, vals := range req.Header {
		for _, v := range vals {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\r\n")
		}
	}
	b.WriteString("\r\n")
	return b.String()
}

func writeStreamResource(z *zone.Zone, out io.Writer, streamVal types.Value, data string) error {
	if streamVal.Type != types.TypeResource {
		return nil
	}
	fwriteFn, ok := streams.Lookup("fwrite")
	if !ok {
		return nil
	}
	sVal, err := newStr(z, data)
	if err != nil {
		return err
	}
	res, err := fwriteFn(z, out, []types.Value{streamVal, sVal})
	types.ReleaseValue(z, &sVal)
	types.ReleaseValue(z, &res)
	return err
}

func allowedCurlScheme(scheme string, mask int64) bool {
	switch strings.ToLower(scheme) {
	case "http":
		return (mask & curlProtoHTTP) != 0
	case "https":
		return (mask & curlProtoHTTPS) != 0
	default:
		return false
	}
}

// CurlExecStep décrit le résultat d'un appel HTTP cURL et les callbacks PHP
// (CURLOPT_HEADERFUNCTION / CURLOPT_WRITEFUNCTION) que la VM doit exécuter.
type CurlExecStep struct {
	OK           bool
	HeaderFunc   types.Value
	HeaderLines  []string
	WriteFunc    types.Value
	Body         string
	ReturnString bool
	FullOut      string
}

// HasActiveCurlCallbacks indique si le handle cURL possède un callback
// CURLOPT_WRITEFUNCTION ou CURLOPT_HEADERFUNCTION attaché.
func HasActiveCurlCallbacks(z *zone.Zone, handleVal types.Value) bool {
	_, h, err := getCurlArg(z, handleVal, "curl_exec")
	if err != nil || h == nil {
		return false
	}
	return hasCallback(h.writeFunc) || hasCallback(h.headerFunc)
}

// SetCurlWriteError positionne CURLE_WRITE_ERROR (23) sur le handle cURL lorsque
// CURLOPT_WRITEFUNCTION ou CURLOPT_HEADERFUNCTION interrompt le transfert.
func SetCurlWriteError(z *zone.Zone, handleVal types.Value) {
	_, h, err := getCurlArg(z, handleVal, "curl_exec")
	if err != nil || h == nil {
		return
	}
	h.errno = 23
	h.errstr = "Failed writing body"
}

// StepCurlExec exécute la requête HTTP cURL et retourne les lignes d'en-tête et
// le corps à transmettre aux callbacks PHP, ou écrit directement la sortie.
func StepCurlExec(z *zone.Zone, out io.Writer, handleVal types.Value) (CurlExecStep, error) {
	_, h, err := getCurlArg(z, handleVal, "curl_exec")
	if err != nil {
		return CurlExecStep{}, err
	}
	return execCurlHTTP(z, out, h)
}

func execCurlHTTP(z *zone.Zone, out io.Writer, h *curlHandle) (CurlExecStep, error) {
	h.errno = 0
	h.errstr = ""
	h.httpCode = 0
	h.headerSize = 0
	h.contentType = ""
	h.redirectCount = 0
	h.redirectURL = ""
	h.requestHeader = ""

	if h.url == "" {
		h.errno = 3
		h.errstr = "No URL set"
		return CurlExecStep{OK: false}, nil
	}
	parsedURL, parseErr := url.Parse(h.url)
	if parseErr != nil || parsedURL.Scheme == "" {
		h.errno = 3
		h.errstr = "URL using bad/illegal format or missing URL"
		return CurlExecStep{OK: false}, nil
	}
	if !allowedCurlScheme(parsedURL.Scheme, h.protocols) {
		h.errno = 1
		h.errstr = "Protocol \"" + strings.Clone(parsedURL.Scheme) + "\" not supported or disabled in libcurl"
		return CurlExecStep{OK: false}, nil
	}

	method := "GET"
	if h.post {
		method = "POST"
	} else if h.put {
		method = "PUT"
	} else if h.nobody {
		method = "HEAD"
	}
	if h.customRequest != "" {
		method = h.customRequest
	}

	var bodyReader io.Reader
	if h.hasPostFields && !h.nobody {
		bodyReader = strings.NewReader(h.postFields)
	}

	req, err := http.NewRequest(method, h.url, bodyReader)
	if err != nil {
		h.errno = 3
		h.errstr = strings.Clone(err.Error())
		return CurlExecStep{OK: false}, nil
	}

	hasContentType := false
	for _, line := range h.headers {
		idx := strings.IndexByte(line, ':')
		if idx < 0 {
			continue
		}
		name := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if name == "" {
			continue
		}
		if strings.EqualFold(name, "Host") {
			req.Host = val
		} else {
			req.Header.Add(name, val)
		}
		if strings.EqualFold(name, "Content-Type") {
			hasContentType = true
		}
	}
	if h.hasPostFields && !hasContentType && h.postContentType != "" {
		req.Header.Set("Content-Type", h.postContentType)
	}
	if h.userAgent != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", h.userAgent)
	}
	if h.referer != "" && req.Header.Get("Referer") == "" {
		req.Header.Set("Referer", h.referer)
	}
	if h.cookie != "" && req.Header.Get("Cookie") == "" {
		req.Header.Set("Cookie", h.cookie)
	}
	if h.userPwd != "" {
		user, pass, _ := strings.Cut(h.userPwd, ":")
		req.SetBasicAuth(user, pass)
	}
	if h.trackHeaderOut {
		h.requestHeader = strings.Clone(formatRequestHeaders(req))
	}

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	if !h.sslVerifyPeer || h.sslVerifyHost == 0 {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{
		Transport: tr,
	}
	if h.timeoutMS > 0 {
		client.Timeout = time.Duration(h.timeoutMS) * time.Millisecond
	} else if h.connectMS > 0 {
		client.Timeout = time.Duration(h.connectMS) * time.Millisecond
	}
	var tooManyRedirs, redirProtoErr bool
	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if !h.followLocation {
			return http.ErrUseLastResponse
		}
		if r.URL == nil || !allowedCurlScheme(r.URL.Scheme, h.redirProtocols) {
			redirProtoErr = true
			return errors.New("unsupported redirect protocol")
		}
		if h.maxRedirs >= 0 && int64(len(via)) > h.maxRedirs {
			tooManyRedirs = true
			return errors.New("too many redirects")
		}
		h.redirectCount = int64(len(via))
		return nil
	}

	start := time.Now()
	resp, err := client.Do(req)
	h.totalTime = time.Since(start).Seconds()
	if err != nil {
		switch {
		case redirProtoErr:
			h.errno = 1
			h.errstr = "Unsupported redirect protocol"
		case tooManyRedirs:
			h.errno = 47
			h.errstr = "Maximum (" + strconv.FormatInt(h.maxRedirs, 10) + ") redirects followed"
		case strings.Contains(err.Error(), "Timeout") || strings.Contains(err.Error(), "deadline exceeded"):
			h.errno = 28
			h.errstr = strings.Clone(err.Error())
		default:
			h.errno = 7
			h.errstr = strings.Clone(err.Error())
		}
		return CurlExecStep{OK: false}, nil
	}
	defer resp.Body.Close()

	h.httpCode = int64(resp.StatusCode)
	if resp.Request != nil && resp.Request.URL != nil {
		h.effectiveURL = strings.Clone(resp.Request.URL.String())
	}
	h.contentType = strings.Clone(resp.Header.Get("Content-Type"))
	if loc := resp.Header.Get("Location"); loc != "" && !h.followLocation {
		if u, err := resp.Request.URL.Parse(loc); err == nil {
			h.redirectURL = strings.Clone(u.String())
		} else {
			h.redirectURL = strings.Clone(loc)
		}
	}

	hdrLines, hdrText := formatResponseHeaderLines(resp)
	h.headerSize = int64(len(hdrText))
	if !hasCallback(h.headerFunc) && h.headerStream.Type == types.TypeResource {
		if err := writeStreamResource(z, out, h.headerStream, hdrText); err != nil {
			return CurlExecStep{}, err
		}
	}

	var bodyBytes []byte
	if !h.nobody {
		bodyBytes, err = io.ReadAll(resp.Body)
		if err != nil {
			h.errno = 56
			h.errstr = strings.Clone(err.Error())
			return CurlExecStep{OK: false}, nil
		}
	}

	var fullOut string
	if h.includeHeader {
		fullOut = hdrText + string(bodyBytes)
	} else {
		fullOut = string(bodyBytes)
	}

	step := CurlExecStep{
		OK:           true,
		Body:         string(bodyBytes),
		ReturnString: h.returnTransfer,
		FullOut:      fullOut,
	}
	if hasCallback(h.headerFunc) {
		step.HeaderFunc = types.RetainValue(z, h.headerFunc)
		step.HeaderLines = hdrLines
	}
	if hasCallback(h.writeFunc) {
		step.WriteFunc = types.RetainValue(z, h.writeFunc)
	} else if !h.returnTransfer {
		if h.outStream.Type == types.TypeResource {
			if err := writeStreamResource(z, out, h.outStream, fullOut); err != nil {
				types.ReleaseValue(z, &step.HeaderFunc)
				return CurlExecStep{}, err
			}
		} else if out != nil {
			_, _ = io.WriteString(out, fullOut)
		}
	}
	return step, nil
}

func nativeCurlExec(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("curl_exec", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := getCurlArg(z, args[0], "curl_exec")
	if err != nil {
		return types.Value{}, err
	}
	step, err := execCurlHTTP(z, out, h)
	types.ReleaseValue(z, &step.HeaderFunc)
	types.ReleaseValue(z, &step.WriteFunc)
	if err != nil {
		return types.Value{}, err
	}
	if !step.OK {
		return types.NewBool(false), nil
	}
	if step.ReturnString {
		return newStr(z, step.FullOut)
	}
	return types.NewBool(true), nil
}

func nativeCurlGetinfo(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("curl_getinfo", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	_, h, err := getCurlArg(z, args[0], "curl_getinfo")
	if err != nil {
		return types.Value{}, err
	}
	if len(args) < 2 || missing(deref(z, args[1])) {
		a, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		urlVal, err := newStr(z, h.effectiveURL)
		if err != nil {
			types.ReleaseArray(z, &a)
			return types.Value{}, err
		}
		err = setStrKey(z, &a, "url", urlVal)
		types.ReleaseValue(z, &urlVal)
		if err != nil {
			types.ReleaseArray(z, &a)
			return types.Value{}, err
		}
		var ctVal types.Value
		if h.contentType == "" {
			ctVal = types.NewNull()
		} else {
			ctVal, err = newStr(z, h.contentType)
			if err != nil {
				types.ReleaseArray(z, &a)
				return types.Value{}, err
			}
		}
		err = setStrKey(z, &a, "content_type", ctVal)
		types.ReleaseValue(z, &ctVal)
		if err != nil {
			types.ReleaseArray(z, &a)
			return types.Value{}, err
		}
		if err := setStrKey(z, &a, "http_code", types.NewLong(h.httpCode)); err != nil {
			types.ReleaseArray(z, &a)
			return types.Value{}, err
		}
		if err := setStrKey(z, &a, "header_size", types.NewLong(h.headerSize)); err != nil {
			types.ReleaseArray(z, &a)
			return types.Value{}, err
		}
		if err := setStrKey(z, &a, "total_time", types.NewDouble(h.totalTime)); err != nil {
			types.ReleaseArray(z, &a)
			return types.Value{}, err
		}
		if err := setStrKey(z, &a, "redirect_count", types.NewLong(h.redirectCount)); err != nil {
			types.ReleaseArray(z, &a)
			return types.Value{}, err
		}
		redirVal, err := newStr(z, h.redirectURL)
		if err != nil {
			types.ReleaseArray(z, &a)
			return types.Value{}, err
		}
		err = setStrKey(z, &a, "redirect_url", redirVal)
		types.ReleaseValue(z, &redirVal)
		if err != nil {
			types.ReleaseArray(z, &a)
			return types.Value{}, err
		}
		if h.requestHeader != "" {
			reqHdrVal, err := newStr(z, h.requestHeader)
			if err != nil {
				types.ReleaseArray(z, &a)
				return types.Value{}, err
			}
			err = setStrKey(z, &a, "request_header", reqHdrVal)
			types.ReleaseValue(z, &reqHdrVal)
			if err != nil {
				types.ReleaseArray(z, &a)
				return types.Value{}, err
			}
		}
		return a, nil
	}

	opt, err := asInt(z, args[1], "curl_getinfo", "2", "option")
	if err != nil {
		return types.Value{}, err
	}
	switch opt {
	case curlInfoEffectiveURL:
		return newStr(z, h.effectiveURL)
	case curlInfoHTTPCode:
		return types.NewLong(h.httpCode), nil
	case curlInfoHeaderSize:
		return types.NewLong(h.headerSize), nil
	case curlInfoContentType:
		if h.contentType == "" {
			return types.NewBool(false), nil
		}
		return newStr(z, h.contentType)
	case curlInfoTotalTime:
		return types.NewDouble(h.totalTime), nil
	case curlInfoRedirectCount:
		return types.NewLong(h.redirectCount), nil
	case curlInfoRedirectURL:
		return newStr(z, h.redirectURL)
	case curlInfoHeaderOut:
		return newStr(z, h.requestHeader)
	default:
		return types.NewBool(false), nil
	}
}

func nativeCurlErrno(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("curl_errno", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := getCurlArg(z, args[0], "curl_errno")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(h.errno), nil
}

func nativeCurlError(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("curl_error", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := getCurlArg(z, args[0], "curl_error")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, h.errstr)
}

func nativeCurlReset(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("curl_reset", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	id, h, err := getCurlArg(z, args[0], "curl_reset")
	if err != nil {
		return types.Value{}, err
	}
	h.releaseCallbacks(z)
	extStateFor(z).handles[id] = newCurlHandle("")
	return types.NewNull(), nil
}

func nativeCurlClose(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("curl_close", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := getCurlArg(z, args[0], "curl_close")
	if err != nil {
		return types.Value{}, err
	}
	h.releaseCallbacks(z)
	return types.NewNull(), nil
}

func nativeCurlVersion(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("curl_version", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	if err := setStrKey(z, &a, "version_number", types.NewLong(0x080500)); err != nil {
		types.ReleaseArray(z, &a)
		return types.Value{}, err
	}
	vStr, err := newStr(z, "8.5.0")
	if err != nil {
		types.ReleaseArray(z, &a)
		return types.Value{}, err
	}
	err = setStrKey(z, &a, "version", vStr)
	types.ReleaseValue(z, &vStr)
	if err != nil {
		types.ReleaseArray(z, &a)
		return types.Value{}, err
	}
	sslStr, err := newStr(z, "OpenSSL/3.0.0")
	if err != nil {
		types.ReleaseArray(z, &a)
		return types.Value{}, err
	}
	err = setStrKey(z, &a, "ssl_version", sslStr)
	types.ReleaseValue(z, &sslStr)
	if err != nil {
		types.ReleaseArray(z, &a)
		return types.Value{}, err
	}
	features := int64(curlVersionSSL | curlVersionIPV6 | curlVersionLibz | curlVersionHTTP2)
	if err := setStrKey(z, &a, "features", types.NewLong(features)); err != nil {
		types.ReleaseArray(z, &a)
		return types.Value{}, err
	}
	protos, err := stringList(z, []string{"http", "https"})
	if err != nil {
		types.ReleaseArray(z, &a)
		return types.Value{}, err
	}
	err = setStrKey(z, &a, "protocols", protos)
	types.ReleaseValue(z, &protos)
	if err != nil {
		types.ReleaseArray(z, &a)
		return types.Value{}, err
	}
	return a, nil
}

// ---- XML Parser (ext/xml) ----

// XMLCallbackEvent décrit un appel de callback XML à exécuter par la VM dans l'ordre du flux XML.
type XMLCallbackEvent struct {
	Object  types.Value
	Handler types.Value
	Args    []types.Value
}

type xmlParser struct {
	caseFolding    bool
	targetEncoding string
	skipTagstart   int64
	skipWhite      bool
	ns             bool
	nsSep          string

	errCode   int64
	line      int64
	col       int64
	byteIndex int64

	buf           bytes.Buffer
	completedOnce bool

	object            types.Value
	startElemHandler  types.Value
	endElemHandler    types.Value
	charDataHandler   types.Value
	defaultHandler    types.Value
	startNSHandler    types.Value
	endNSHandler      types.Value
	piHandler         types.Value
	unparsedEntityHdl types.Value
	notationDeclHdl   types.Value
	externalEntityHdl types.Value
}

func newXMLParser(ns bool, sep string) *xmlParser {
	return &xmlParser{
		caseFolding:    true,
		targetEncoding: "UTF-8",
		ns:             ns,
		nsSep:          strings.Clone(sep),
		line:           1,
		col:            1,
	}
}

func storeXMLParser(z *zone.Zone, p *xmlParser) types.Value {
	st := extStateFor(z)
	if st.handles == nil {
		st.handles = make(map[uint32]any)
	}
	id := st.newHandle()
	st.handles[id] = p
	return streams.NewResource(z, ResourceKindXML, id)
}

func getXMLArg(z *zone.Zone, v types.Value, fn string) (uint32, *xmlParser, error) {
	v = deref(z, v)
	if v.Type != types.TypeResource || v.Extra != ResourceKindXML {
		got := typeKind(z, v)
		if v.Type == types.TypeResource {
			got = "resource"
		}
		return 0, nil, typeErr(fn, "1", "parser", "XMLParser", got)
	}
	st := extStateFor(z)
	if st.handles != nil {
		if p, ok := st.handles[v.DataSlot].(*xmlParser); ok && p != nil {
			return v.DataSlot, p, nil
		}
	}
	return 0, nil, TypeError{fn + "(): Argument #1 ($parser) must be a valid XMLParser"}
}

func (p *xmlParser) releaseCallbacks(z *zone.Zone) {
	types.ReleaseValue(z, &p.object)
	types.ReleaseValue(z, &p.startElemHandler)
	types.ReleaseValue(z, &p.endElemHandler)
	types.ReleaseValue(z, &p.charDataHandler)
	types.ReleaseValue(z, &p.defaultHandler)
	types.ReleaseValue(z, &p.startNSHandler)
	types.ReleaseValue(z, &p.endNSHandler)
	types.ReleaseValue(z, &p.piHandler)
	types.ReleaseValue(z, &p.unparsedEntityHdl)
	types.ReleaseValue(z, &p.notationDeclHdl)
	types.ReleaseValue(z, &p.externalEntityHdl)
}

func setParserCallback(z *zone.Zone, dst *types.Value, v types.Value) {
	v = deref(z, v)
	types.ReleaseValue(z, dst)
	if v.IsNull() || v.IsUndef() || (v.Type == types.TypeFalse) || (v.Type == types.TypeString && types.StringData(z, v) == "") {
		*dst = types.Value{}
		return
	}
	*dst = types.RetainValue(z, v)
}

func hasCallback(v types.Value) bool {
	return !v.IsUndef() && !v.IsNull() && v.Type != types.TypeFalse
}

func nativeXMLParserCreate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_parser_create", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	p := newXMLParser(false, "")
	if len(args) >= 1 && !missing(deref(z, args[0])) {
		enc, err := asString(z, args[0], "xml_parser_create", "1", "encoding")
		if err != nil {
			return types.Value{}, err
		}
		if enc != "" {
			p.targetEncoding = strings.Clone(strings.ToUpper(enc))
		}
	}
	return storeXMLParser(z, p), nil
}

func nativeXMLParserCreateNS(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_parser_create_ns", len(args), 0, 2); err != nil {
		return types.Value{}, err
	}
	sep := ":"
	if len(args) >= 2 && !missing(deref(z, args[1])) {
		var err error
		sep, err = asString(z, args[1], "xml_parser_create_ns", "2", "separator")
		if err != nil {
			return types.Value{}, err
		}
	}
	p := newXMLParser(true, sep)
	if len(args) >= 1 && !missing(deref(z, args[0])) {
		enc, err := asString(z, args[0], "xml_parser_create_ns", "1", "encoding")
		if err != nil {
			return types.Value{}, err
		}
		if enc != "" {
			p.targetEncoding = strings.Clone(strings.ToUpper(enc))
		}
	}
	return storeXMLParser(z, p), nil
}

func nativeXMLParserFree(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_parser_free", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	id, p, err := getXMLArg(z, args[0], "xml_parser_free")
	if err != nil {
		return types.Value{}, err
	}
	p.releaseCallbacks(z)
	delete(extStateFor(z).handles, id)
	return types.NewBool(true), nil
}

func nativeXMLParserSetOption(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_parser_set_option", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_parser_set_option")
	if err != nil {
		return types.Value{}, err
	}
	opt, err := asInt(z, args[1], "xml_parser_set_option", "2", "option")
	if err != nil {
		return types.Value{}, err
	}
	switch opt {
	case xmlOptionCaseFolding:
		p.caseFolding = asBool(z, args[2])
	case xmlOptionTargetEncoding:
		s, err := asString(z, args[2], "xml_parser_set_option", "3", "value")
		if err != nil {
			return types.Value{}, err
		}
		p.targetEncoding = strings.Clone(s)
	case xmlOptionSkipTagstart:
		n, err := asInt(z, args[2], "xml_parser_set_option", "3", "value")
		if err != nil {
			return types.Value{}, err
		}
		p.skipTagstart = n
	case xmlOptionSkipWhite:
		p.skipWhite = asBool(z, args[2])
	default:
		return types.Value{}, ValueError{"xml_parser_set_option(): Argument #2 ($option) must be a valid XML_OPTION_* constant"}
	}
	return types.NewBool(true), nil
}

func nativeXMLParserGetOption(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_parser_get_option", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_parser_get_option")
	if err != nil {
		return types.Value{}, err
	}
	opt, err := asInt(z, args[1], "xml_parser_get_option", "2", "option")
	if err != nil {
		return types.Value{}, err
	}
	switch opt {
	case xmlOptionCaseFolding:
		if p.caseFolding {
			return types.NewLong(1), nil
		}
		return types.NewLong(0), nil
	case xmlOptionTargetEncoding:
		return newStr(z, p.targetEncoding)
	case xmlOptionSkipTagstart:
		return types.NewLong(p.skipTagstart), nil
	case xmlOptionSkipWhite:
		if p.skipWhite {
			return types.NewLong(1), nil
		}
		return types.NewLong(0), nil
	default:
		return types.Value{}, ValueError{"xml_parser_get_option(): Argument #2 ($option) must be a valid XML_OPTION_* constant"}
	}
}

func (p *xmlParser) formatTagName(name xml.Name, rawPrefix map[string]string) string {
	var out string
	if p.ns {
		if name.Space != "" {
			out = name.Space + p.nsSep + name.Local
		} else {
			out = name.Local
		}
	} else {
		if name.Space != "" {
			if pref, ok := rawPrefix[name.Space]; ok && pref != "" {
				out = pref + ":" + name.Local
			} else {
				out = name.Space + ":" + name.Local
			}
		} else {
			out = name.Local
		}
	}
	if int64(len(out)) > p.skipTagstart && p.skipTagstart > 0 {
		out = out[p.skipTagstart:]
	}
	if p.caseFolding {
		out = strings.ToUpper(out)
	}
	return out
}

func (p *xmlParser) updatePosition(dec *xml.Decoder, data string) {
	off := dec.InputOffset()
	if off < 0 {
		off = 0
	}
	if int(off) > len(data) {
		off = int64(len(data))
	}
	p.byteIndex = off
	line, col := dec.InputPos()
	if line > 0 {
		p.line = int64(line)
		p.col = int64(col)
	}
}

func classifyXMLError(err error) int64 {
	if err == nil {
		return xmlErrorNone
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "expected element name after </") || strings.Contains(msg, "closed by </"):
		return xmlErrorTagMismatch
	case strings.Contains(msg, "unexpected EOF") || strings.Contains(msg, "unclosed"):
		return xmlErrorUnclosedToken
	case strings.Contains(msg, "invalid character entity"):
		return xmlErrorUndefinedEntity
	default:
		return xmlErrorSyntax
	}
}

type structNode struct {
	tag        string
	typ        string
	level      int64
	attributes []xml.Attr
	hasAttrs   bool
	value      string
	hasValue   bool
}

func nativeXMLParseIntoStruct(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_parse_into_struct", len(args), 3, 4); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_parse_into_struct")
	if err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[1], "xml_parse_into_struct", "2", "data")
	if err != nil {
		return types.Value{}, err
	}
	valSlot := arg(args, 2)
	idxSlot := arg(args, 3)

	p.errCode = xmlErrorNone
	p.line = 1
	p.col = 1
	p.byteIndex = 0

	dec := xml.NewDecoder(strings.NewReader(data))
	dec.Strict = true

	var nodes []structNode
	var openStack []int // indices dans nodes des balises actuellement ouvertes
	rawPrefix := make(map[string]string)
	level := int64(0)
	seenElement := false
	parseOK := true

	for {
		tok, tokErr := dec.Token()
		p.updatePosition(dec, data)
		if tokErr == io.EOF {
			break
		}
		if tokErr != nil {
			p.errCode = classifyXMLError(tokErr)
			parseOK = false
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			seenElement = true
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" {
					rawPrefix[a.Value] = a.Name.Local
				}
			}
			level++
			tagName := p.formatTagName(t.Name, rawPrefix)
			var attrs []xml.Attr
			seenAttr := make(map[string]bool, len(t.Attr))
			dupAttr := false
			for _, a := range t.Attr {
				rawAttrKey := a.Name.Space + "\x00" + a.Name.Local
				if seenAttr[rawAttrKey] {
					dupAttr = true
					break
				}
				seenAttr[rawAttrKey] = true
				if p.ns && (a.Name.Local == "xmlns" || a.Name.Space == "xmlns") {
					continue
				}
				attrName := p.formatTagName(a.Name, rawPrefix)
				attrs = append(attrs, xml.Attr{Name: xml.Name{Local: attrName}, Value: a.Value})
			}
			if dupAttr {
				p.errCode = xmlErrorDuplicateAttr
				parseOK = false
				break
			}
			idx := len(nodes)
			nodes = append(nodes, structNode{
				tag:        tagName,
				typ:        "open",
				level:      level,
				attributes: attrs,
				hasAttrs:   len(attrs) > 0,
			})
			openStack = append(openStack, idx)

		case xml.EndElement:
			if len(openStack) > 0 {
				topIdx := openStack[len(openStack)-1]
				openStack = openStack[:len(openStack)-1]
				if topIdx == len(nodes)-1 && nodes[topIdx].typ == "open" {
					nodes[topIdx].typ = "complete"
				} else {
					tagName := p.formatTagName(t.Name, rawPrefix)
					nodes = append(nodes, structNode{
						tag:   tagName,
						typ:   "close",
						level: level,
					})
				}
			}
			level--

		case xml.CharData:
			text := string(t)
			if p.skipWhite && strings.TrimSpace(text) == "" {
				continue
			}
			if len(openStack) == 0 {
				continue
			}
			topIdx := openStack[len(openStack)-1]
			if topIdx == len(nodes)-1 && nodes[topIdx].typ == "open" {
				nodes[topIdx].value += text
				nodes[topIdx].hasValue = true
			} else {
				nodes = append(nodes, structNode{
					tag:      nodes[topIdx].tag,
					typ:      "cdata",
					level:    level,
					value:    text,
					hasValue: true,
				})
			}
		}
		if !parseOK {
			break
		}
	}

	if parseOK && !seenElement {
		p.errCode = xmlErrorNoElements
		parseOK = false
	}

	valuesArr, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	defer types.ReleaseValue(z, &valuesArr)
	indexMap := make(map[string][]int64)
	var indexOrder []string

	for i, n := range nodes {
		entry, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		tagVal, err := newStr(z, n.tag)
		if err != nil {
			types.ReleaseArray(z, &entry)
			return types.Value{}, err
		}
		err = setStrKey(z, &entry, "tag", tagVal)
		types.ReleaseValue(z, &tagVal)
		if err != nil {
			types.ReleaseArray(z, &entry)
			return types.Value{}, err
		}
		typVal, err := newStr(z, n.typ)
		if err != nil {
			types.ReleaseArray(z, &entry)
			return types.Value{}, err
		}
		err = setStrKey(z, &entry, "type", typVal)
		types.ReleaseValue(z, &typVal)
		if err != nil {
			types.ReleaseArray(z, &entry)
			return types.Value{}, err
		}
		if err := setStrKey(z, &entry, "level", types.NewLong(n.level)); err != nil {
			types.ReleaseArray(z, &entry)
			return types.Value{}, err
		}
		if n.hasAttrs {
			attrArr, err := newArray(z)
			if err != nil {
				types.ReleaseArray(z, &entry)
				return types.Value{}, err
			}
			for _, a := range n.attributes {
				av, err := newStr(z, a.Value)
				if err != nil {
					types.ReleaseArray(z, &attrArr)
					types.ReleaseArray(z, &entry)
					return types.Value{}, err
				}
				err = setStrKey(z, &attrArr, a.Name.Local, av)
				types.ReleaseValue(z, &av)
				if err != nil {
					types.ReleaseArray(z, &attrArr)
					types.ReleaseArray(z, &entry)
					return types.Value{}, err
				}
			}
			err = setStrKey(z, &entry, "attributes", attrArr)
			types.ReleaseValue(z, &attrArr)
			if err != nil {
				types.ReleaseArray(z, &entry)
				return types.Value{}, err
			}
		}
		if n.hasValue {
			vVal, err := newStr(z, n.value)
			if err != nil {
				types.ReleaseArray(z, &entry)
				return types.Value{}, err
			}
			err = setStrKey(z, &entry, "value", vVal)
			types.ReleaseValue(z, &vVal)
			if err != nil {
				types.ReleaseArray(z, &entry)
				return types.Value{}, err
			}
		}
		err = appendVal(z, &valuesArr, entry)
		types.ReleaseValue(z, &entry)
		if err != nil {
			return types.Value{}, err
		}
		if _, exists := indexMap[n.tag]; !exists {
			indexOrder = append(indexOrder, n.tag)
		}
		indexMap[n.tag] = append(indexMap[n.tag], int64(i))
	}

	writeRef(z, valSlot, valuesArr)

	if idxSlot.Type == types.TypeReference {
		idxArr, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		defer types.ReleaseValue(z, &idxArr)
		for _, tag := range indexOrder {
			listArr, err := newArray(z)
			if err != nil {
				return types.Value{}, err
			}
			for _, pos := range indexMap[tag] {
				if err := appendVal(z, &listArr, types.NewLong(pos)); err != nil {
					types.ReleaseArray(z, &listArr)
					return types.Value{}, err
				}
			}
			err = setStrKey(z, &idxArr, tag, listArr)
			types.ReleaseValue(z, &listArr)
			if err != nil {
				return types.Value{}, err
			}
		}
		writeRef(z, idxSlot, idxArr)
	}

	if !parseOK {
		return types.NewLong(0), nil
	}
	return types.NewLong(1), nil
}

// ReleaseXMLEvent libère les valeurs d'arène (Object, Handler, Args) retenues par un XMLCallbackEvent.
func ReleaseXMLEvent(z *zone.Zone, ev *XMLCallbackEvent) {
	if ev == nil {
		return
	}
	types.ReleaseValue(z, &ev.Object)
	types.ReleaseValue(z, &ev.Handler)
	for i := range ev.Args {
		types.ReleaseValue(z, &ev.Args[i])
	}
	ev.Args = nil
}

// HasActiveXMLCallbacks indique si le parseur XML possède au moins un callback utilisateur attaché.
func HasActiveXMLCallbacks(z *zone.Zone, parserVal types.Value) bool {
	_, p, err := getXMLArg(z, parserVal, "xml_parse")
	if err != nil || p == nil {
		return false
	}
	return hasCallback(p.startElemHandler) ||
		hasCallback(p.endElemHandler) ||
		hasCallback(p.charDataHandler) ||
		hasCallback(p.defaultHandler) ||
		hasCallback(p.startNSHandler) ||
		hasCallback(p.endNSHandler) ||
		hasCallback(p.piHandler)
}

// StepXMLParse accumule data dans le parseur et, lorsque isFinal est vrai (ou si data forme un document complet),
// décode les jetons XML et retourne la liste ordonnée des événements XMLCallbackEvent à invoquer par la VM
// ainsi que le code de retour (1 = succès, 0 = erreur XML).
func StepXMLParse(z *zone.Zone, parserVal types.Value, data string, isFinal bool) ([]XMLCallbackEvent, int) {
	_, p, err := getXMLArg(z, parserVal, "xml_parse")
	if err != nil || p == nil {
		return nil, 0
	}
	if p.buf.Len() == 0 && data == "" && isFinal && p.completedOnce {
		p.completedOnce = false
		return nil, 1
	}
	if data != "" {
		p.completedOnce = false
		p.buf.WriteString(data)
	}
	fullData := p.buf.String()

	p.errCode = xmlErrorNone
	p.line = 1
	p.col = 1
	p.byteIndex = 0

	dec := xml.NewDecoder(strings.NewReader(fullData))
	dec.Strict = true

	var events []XMLCallbackEvent
	releaseEvents := func() {
		for i := range events {
			ReleaseXMLEvent(z, &events[i])
		}
		events = nil
	}
	failOOM := func() ([]XMLCallbackEvent, int) {
		releaseEvents()
		p.buf.Reset()
		p.errCode = xmlErrorNoMemory
		return nil, 0
	}
	resVal := deref(z, parserVal)
	pushEvent := func(handler types.Value, extraArgs ...types.Value) {
		args := make([]types.Value, 0, 1+len(extraArgs))
		args = append(args, types.RetainValue(z, resVal))
		args = append(args, extraArgs...)
		events = append(events, XMLCallbackEvent{
			Object:  types.RetainValue(z, p.object),
			Handler: types.RetainValue(z, handler),
			Args:    args,
		})
	}

	rawPrefix := make(map[string]string)
	var nsStack [][]string
	seenElement := false
	depth := 0

	for {
		tok, tokErr := dec.Token()
		p.updatePosition(dec, fullData)
		if tokErr == io.EOF {
			break
		}
		if tokErr != nil {
			if !isFinal {
				releaseEvents()
				return nil, 1
			}
			p.buf.Reset()
			p.errCode = classifyXMLError(tokErr)
			return events, 0
		}
		switch t := tok.(type) {
		case xml.StartElement:
			seenElement = true
			depth++
			seenAttr := make(map[string]bool, len(t.Attr))
			for _, a := range t.Attr {
				rawAttrKey := a.Name.Space + "\x00" + a.Name.Local
				if seenAttr[rawAttrKey] {
					p.buf.Reset()
					p.errCode = xmlErrorDuplicateAttr
					return events, 0
				}
				seenAttr[rawAttrKey] = true
			}
			var declaredPrefixes []string
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" {
					rawPrefix[a.Value] = a.Name.Local
					if p.ns && hasCallback(p.startNSHandler) {
						prefVal, err := newStr(z, a.Name.Local)
						if err != nil {
							return failOOM()
						}
						uriVal, err := newStr(z, a.Value)
						if err != nil {
							types.ReleaseValue(z, &prefVal)
							return failOOM()
						}
						pushEvent(p.startNSHandler, prefVal, uriVal)
						declaredPrefixes = append(declaredPrefixes, a.Name.Local)
					}
				} else if a.Name.Local == "xmlns" && a.Name.Space == "" {
					if p.ns && hasCallback(p.startNSHandler) {
						uriVal, err := newStr(z, a.Value)
						if err != nil {
							return failOOM()
						}
						pushEvent(p.startNSHandler, types.NewBool(false), uriVal)
						declaredPrefixes = append(declaredPrefixes, "")
					}
				}
			}
			nsStack = append(nsStack, declaredPrefixes)

			if hasCallback(p.startElemHandler) {
				tagName := p.formatTagName(t.Name, rawPrefix)
				tagVal, err := newStr(z, tagName)
				if err != nil {
					return failOOM()
				}
				attrArr, err := newArray(z)
				if err != nil {
					types.ReleaseValue(z, &tagVal)
					return failOOM()
				}
				for _, a := range t.Attr {
					if p.ns && (a.Name.Local == "xmlns" || a.Name.Space == "xmlns") {
						continue
					}
					attrName := p.formatTagName(a.Name, rawPrefix)
					av, err := newStr(z, a.Value)
					if err != nil {
						types.ReleaseArray(z, &attrArr)
						types.ReleaseValue(z, &tagVal)
						return failOOM()
					}
					err = setStrKey(z, &attrArr, attrName, av)
					types.ReleaseValue(z, &av)
					if err != nil {
						types.ReleaseArray(z, &attrArr)
						types.ReleaseValue(z, &tagVal)
						return failOOM()
					}
				}
				pushEvent(p.startElemHandler, tagVal, attrArr)
			}

		case xml.EndElement:
			depth--
			if hasCallback(p.endElemHandler) {
				tagName := p.formatTagName(t.Name, rawPrefix)
				tagVal, err := newStr(z, tagName)
				if err != nil {
					return failOOM()
				}
				pushEvent(p.endElemHandler, tagVal)
			}
			if len(nsStack) > 0 {
				top := nsStack[len(nsStack)-1]
				nsStack = nsStack[:len(nsStack)-1]
				if p.ns && hasCallback(p.endNSHandler) {
					for i := len(top) - 1; i >= 0; i-- {
						var prefArg types.Value
						if top[i] == "" {
							prefArg = types.NewBool(false)
						} else {
							var err error
							prefArg, err = newStr(z, top[i])
							if err != nil {
								return failOOM()
							}
						}
						pushEvent(p.endNSHandler, prefArg)
					}
				}
			}

		case xml.CharData:
			if hasCallback(p.charDataHandler) {
				sVal, err := newStr(z, string(t))
				if err != nil {
					return failOOM()
				}
				pushEvent(p.charDataHandler, sVal)
			}

		case xml.ProcInst:
			if strings.EqualFold(t.Target, "xml") {
				continue
			}
			if hasCallback(p.piHandler) {
				targetVal, err := newStr(z, t.Target)
				if err != nil {
					return failOOM()
				}
				instVal, err := newStr(z, string(t.Inst))
				if err != nil {
					types.ReleaseValue(z, &targetVal)
					return failOOM()
				}
				pushEvent(p.piHandler, targetVal, instVal)
			} else if hasCallback(p.defaultHandler) {
				rawVal, err := newStr(z, "<?"+t.Target+" "+string(t.Inst)+"?>")
				if err != nil {
					return failOOM()
				}
				pushEvent(p.defaultHandler, rawVal)
			}

		case xml.Comment:
			if hasCallback(p.defaultHandler) {
				rawVal, err := newStr(z, "<!--"+string(t)+"-->")
				if err != nil {
					return failOOM()
				}
				pushEvent(p.defaultHandler, rawVal)
			}
		}
	}

	if !seenElement || depth != 0 {
		if !isFinal {
			releaseEvents()
			return nil, 1
		}
		p.buf.Reset()
		p.errCode = xmlErrorNoElements
		return events, 0
	}
	p.buf.Reset()
	p.completedOnce = !isFinal
	return events, 1
}

func nativeXMLParse(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_parse", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	if _, _, err := getXMLArg(z, args[0], "xml_parse"); err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[1], "xml_parse", "2", "data")
	if err != nil {
		return types.Value{}, err
	}
	isFinal := true
	if len(args) >= 3 && !missing(deref(z, args[2])) {
		isFinal = asBool(z, args[2])
	}
	events, status := StepXMLParse(z, args[0], data, isFinal)
	for i := range events {
		ReleaseXMLEvent(z, &events[i])
	}
	return types.NewLong(int64(status)), nil
}

func nativeXMLGetErrorCode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_get_error_code", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_get_error_code")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(p.errCode), nil
}

var xmlErrorMessages = map[int64]string{
	xmlErrorNone:             "No error",
	xmlErrorNoMemory:         "Out of memory",
	xmlErrorSyntax:           "Syntax error",
	xmlErrorNoElements:       "No elements found",
	xmlErrorInvalidToken:     "not well-formed (invalid token)",
	xmlErrorUnclosedToken:    "unclosed token",
	xmlErrorPartialChar:      "partial character",
	xmlErrorTagMismatch:      "Mismatched tag",
	xmlErrorDuplicateAttr:    "duplicate attribute",
	xmlErrorJunkAfterDocElem: "junk after document element",
	xmlErrorParamEntityRef:   "illegal parameter entity reference",
	xmlErrorUndefinedEntity:  "undefined entity",
	xmlErrorRecursiveEntity:  "recursive entity reference",
	xmlErrorAsyncEntity:      "asynchronous entity",
	xmlErrorBadCharRef:       "reference to invalid character number",
	xmlErrorBinaryEntityRef:  "reference to binary entity",
	xmlErrorAttrExtEntityRef: "reference to external entity in attribute",
	xmlErrorMisplacedXMLPI:   "XML or text declaration not at start of entity",
	xmlErrorUnknownEncoding:  "unknown encoding",
	xmlErrorIncorrectEnc:     "encoding specified in XML declaration is incorrect",
	xmlErrorUnclosedCDATA:    "unclosed CDATA section",
	xmlErrorExtEntityHandl:   "error in processing external entity reference",
}

func nativeXMLErrorString(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_error_string", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	code, err := asInt(z, args[0], "xml_error_string", "1", "error_code")
	if err != nil {
		return types.Value{}, err
	}
	msg, ok := xmlErrorMessages[code]
	if !ok {
		return types.NewNull(), nil
	}
	return newStr(z, msg)
}

func nativeXMLGetCurrentLineNumber(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_get_current_line_number", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_get_current_line_number")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(p.line), nil
}

func nativeXMLGetCurrentColumnNumber(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_get_current_column_number", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_get_current_column_number")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(p.col), nil
}

func nativeXMLGetCurrentByteIndex(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_get_current_byte_index", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_get_current_byte_index")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(p.byteIndex), nil
}

func nativeXMLSetObject(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_set_object", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_set_object")
	if err != nil {
		return types.Value{}, err
	}
	obj := deref(z, args[1])
	if obj.Type != types.TypeObject {
		return types.Value{}, typeErr("xml_set_object", "2", "object", "object", typeKind(z, obj))
	}
	setParserCallback(z, &p.object, obj)
	return types.NewBool(true), nil
}

func nativeXMLSetElementHandler(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_set_element_handler", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_set_element_handler")
	if err != nil {
		return types.Value{}, err
	}
	setParserCallback(z, &p.startElemHandler, args[1])
	setParserCallback(z, &p.endElemHandler, args[2])
	return types.NewBool(true), nil
}

func nativeXMLSetCharacterDataHandler(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_set_character_data_handler", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_set_character_data_handler")
	if err != nil {
		return types.Value{}, err
	}
	setParserCallback(z, &p.charDataHandler, args[1])
	return types.NewBool(true), nil
}

func nativeXMLSetDefaultHandler(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_set_default_handler", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_set_default_handler")
	if err != nil {
		return types.Value{}, err
	}
	setParserCallback(z, &p.defaultHandler, args[1])
	return types.NewBool(true), nil
}

func nativeXMLSetStartNamespaceDeclHandler(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_set_start_namespace_decl_handler", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_set_start_namespace_decl_handler")
	if err != nil {
		return types.Value{}, err
	}
	setParserCallback(z, &p.startNSHandler, args[1])
	return types.NewBool(true), nil
}

func nativeXMLSetEndNamespaceDeclHandler(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_set_end_namespace_decl_handler", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_set_end_namespace_decl_handler")
	if err != nil {
		return types.Value{}, err
	}
	setParserCallback(z, &p.endNSHandler, args[1])
	return types.NewBool(true), nil
}

func nativeXMLSetProcessingInstructionHandler(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_set_processing_instruction_handler", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_set_processing_instruction_handler")
	if err != nil {
		return types.Value{}, err
	}
	setParserCallback(z, &p.piHandler, args[1])
	return types.NewBool(true), nil
}

func nativeXMLSetUnparsedEntityDeclHandler(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_set_unparsed_entity_decl_handler", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_set_unparsed_entity_decl_handler")
	if err != nil {
		return types.Value{}, err
	}
	setParserCallback(z, &p.unparsedEntityHdl, args[1])
	return types.NewBool(true), nil
}

func nativeXMLSetNotationDeclHandler(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_set_notation_decl_handler", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_set_notation_decl_handler")
	if err != nil {
		return types.Value{}, err
	}
	setParserCallback(z, &p.notationDeclHdl, args[1])
	return types.NewBool(true), nil
}

func nativeXMLSetExternalEntityRefHandler(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("xml_set_external_entity_ref_handler", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, p, err := getXMLArg(z, args[0], "xml_set_external_entity_ref_handler")
	if err != nil {
		return types.Value{}, err
	}
	setParserCallback(z, &p.externalEntityHdl, args[1])
	return types.NewBool(true), nil
}
