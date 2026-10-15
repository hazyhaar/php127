package sapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/bytecode"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func valueToJSON(z *zone.Zone, v types.Value) any {
	v = types.Deref(z, v)
	switch v.Type {
	case types.TypeUndef, types.TypeNull:
		return nil
	case types.TypeFalse:
		return false
	case types.TypeTrue:
		return true
	case types.TypeLong:
		return v.ToLong()
	case types.TypeDouble:
		return v.ToDouble()
	case types.TypeString:
		return strings.Clone(types.StringData(z, v))
	case types.TypeArray:
		info := types.ArrayInfo(z, v)
		if info.Len == 0 {
			return []any{}
		}
		isList := true
		expected := int64(0)
		types.ArrayForEach(z, v, func(k, _ types.Value) bool {
			if k.Type != types.TypeLong || k.ToLong() != expected {
				isList = false
				return false
			}
			expected++
			return true
		})
		if isList {
			out := make([]any, 0, info.Len)
			types.ArrayForEach(z, v, func(_, val types.Value) bool {
				out = append(out, valueToJSON(z, val))
				return true
			})
			return out
		}
		out := make(map[string]any, info.Len)
		types.ArrayForEach(z, v, func(k, val types.Value) bool {
			var ks string
			if k.Type == types.TypeLong {
				ks = strconv.FormatInt(k.ToLong(), 10)
			} else {
				ks = strings.Clone(types.StringData(z, k))
			}
			out[ks] = valueToJSON(z, val)
			return true
		})
		return out
	default:
		return nil
	}
}

func TestParseURLDifferential(t *testing.T) {
	urls := []string{
		"http://username:password@hostname:9090/path?arg=value#anchor",
		"https://example.com",
		"https://example.com/",
		"//example.com/path/to/file?foo=bar#baz",
		"/relative/path?query=1#frag",
		"path/only",
		"?only_query=1",
		"#only_frag",
		"http://[::1]:8080/ipv6",
		"mailto:user@example.com",
		"http://:80",
		"http:///path",
		"http://user:@host/",
		"http://:pass@host/",
		"http://host:0/",
		"http://host:65535/",
		"http://host:65536/",
		"http://host:/",
		"http://host:abc/",
		"http://host?q=1",
		"http://host#f",
		"://host",
		"1http://host",
		"http+unix://host/path",
		"a-b.c://host",
		"http://",
		"//",
		"///path",
		"http://user@host",
		"http://@host",
		"http://:@host",
		"?",
		"#",
		"?#",
		"",
	}

	urlsJSON, err := json.Marshal(urls)
	if err != nil {
		t.Fatal(err)
	}
	phpCode := fmt.Sprintf(`
$urls = json_decode(%q, true);
$out = [];
foreach ($urls as $u) {
    $row = [parse_url($u)];
    for ($c = 0; $c <= 7; $c++) {
        $row[] = parse_url($u, $c);
    }
    $out[] = $row;
}
echo json_encode($out);
`, string(urlsJSON))

	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", phpCode)
	phpOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("php oracle failed: %v\n%s", err, phpOut)
	}
	var wantRows [][]any
	if err := json.Unmarshal(phpOut, &wantRows); err != nil {
		t.Fatalf("unmarshal php output: %v\n%s", err, phpOut)
	}

	z := zone.New(zone.DefaultCapacity)
	defer z.Reset(nil)
	fn, ok := Lookup("parse_url")
	if !ok {
		t.Fatal("parse_url not found in Lookup")
	}

	for i, u := range urls {
		uVal, err := newStr(z, u)
		if err != nil {
			t.Fatal(err)
		}
		var gotRow []any
		resAll, err := fn(z, io.Discard, []types.Value{uVal})
		if err != nil {
			t.Fatalf("url %q: unexpected error: %v", u, err)
		}
		gotRow = append(gotRow, valueToJSON(z, resAll))
		types.ReleaseValue(z, &resAll)

		for c := int64(0); c <= 7; c++ {
			resComp, err := fn(z, io.Discard, []types.Value{uVal, types.NewLong(c)})
			if err != nil {
				t.Fatalf("url %q comp %d: unexpected error: %v", u, c, err)
			}
			gotRow = append(gotRow, valueToJSON(z, resComp))
			types.ReleaseValue(z, &resComp)
		}
		types.ReleaseValue(z, &uVal)

		gotJSON, _ := json.Marshal(gotRow)
		wantJSON, _ := json.Marshal(wantRows[i])
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Fatalf("parse_url(%q) mismatch:\ngot:  %s\nwant: %s", u, gotJSON, wantJSON)
		}
	}

	// Vérification de ValueError sur composant invalide
	uVal, _ := newStr(z, "http://example.com")
	_, err = fn(z, io.Discard, []types.Value{uVal, types.NewLong(99)})
	types.ReleaseValue(z, &uVal)
	if ExceptionClass(err) != "ValueError" {
		t.Fatalf("expected ValueError on invalid component, got %v", err)
	}
}

func TestParseStrAndHTTPBuildQueryDifferential(t *testing.T) {
	queries := []string{
		"",
		"a=1&b=2",
		"a=1&a=2",
		"a[]=1&a[]=2",
		"a[b][]=1&a[b][]=2",
		"a[b][c]=x&a[b][d]=y",
		"a.b=1&c d=2&e[f.g]=3",
		" a.b =1&[x=2&x[y=3&x[y]z=4",
		"a=1&a[]=2",
		"a[]=1&a=2",
		"foo+bar=baz%20qux&empty=&noval",
		"user[name]=Bob+Smith&user[age]=47&user[tags][0]=php&user[tags][1]=go&tilde=~test",
	}

	qJSON, err := json.Marshal(queries)
	if err != nil {
		t.Fatal(err)
	}
	phpCode := fmt.Sprintf(`
$queries = json_decode(%q, true);
$out = [];
foreach ($queries as $q) {
    $arr = [];
    parse_str($q, $arr);
    $b1 = http_build_query($arr, "n_", "&", PHP_QUERY_RFC1738);
    $b2 = http_build_query($arr, "n_", "&amp;", PHP_QUERY_RFC3986);
    $out[] = [$arr, $b1, $b2];
}
echo json_encode($out);
`, string(qJSON))

	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", phpCode)
	phpOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("php oracle failed: %v\n%s", err, phpOut)
	}
	var wantRows [][]any
	if err := json.Unmarshal(phpOut, &wantRows); err != nil {
		t.Fatalf("unmarshal php output: %v\n%s", err, phpOut)
	}

	z := zone.New(zone.DefaultCapacity)
	defer z.Reset(nil)

	parseStrFn, _ := Lookup("parse_str")
	buildQueryFn, _ := Lookup("http_build_query")

	for i, q := range queries {
		qVal, _ := newStr(z, q)
		targetRef, _ := types.NewReference(z, types.NewNull())
		if _, err := parseStrFn(z, io.Discard, []types.Value{qVal, targetRef}); err != nil {
			t.Fatalf("parse_str(%q) error: %v", q, err)
		}
		arr := types.Deref(z, targetRef)

		np, _ := newStr(z, "n_")
		sep1, _ := newStr(z, "&")
		sep2, _ := newStr(z, "&amp;")

		b1Val, err := buildQueryFn(z, io.Discard, []types.Value{arr, np, sep1, types.NewLong(PHP_QUERY_RFC1738)})
		if err != nil {
			t.Fatalf("http_build_query RFC1738(%q) error: %v", q, err)
		}
		b2Val, err := buildQueryFn(z, io.Discard, []types.Value{arr, np, sep2, types.NewLong(PHP_QUERY_RFC3986)})
		if err != nil {
			t.Fatalf("http_build_query RFC3986(%q) error: %v", q, err)
		}

		gotRow := []any{
			valueToJSON(z, arr),
			types.StringData(z, b1Val),
			types.StringData(z, b2Val),
		}
		gotJSON, _ := json.Marshal(gotRow)
		wantJSON, _ := json.Marshal(wantRows[i])
		if !bytes.Equal(gotJSON, wantJSON) {
			t.Fatalf("query %q mismatch:\ngot:  %s\nwant: %s", q, gotJSON, wantJSON)
		}

		types.ReleaseValue(z, &b2Val)
		types.ReleaseValue(z, &b1Val)
		types.ReleaseValue(z, &sep2)
		types.ReleaseValue(z, &sep1)
		types.ReleaseValue(z, &np)
		types.ReleaseValue(z, &targetRef)
		types.ReleaseValue(z, &qVal)
	}
}

func TestZoneReuse1000Requests(t *testing.T) {
	var resetChecked atomic.Uint64
	var activeZonePtr uintptr

	handler := HandlerFunc(func(z *zone.Zone, req *RequestContext, out io.Writer) error {
		headerFn, _ := Lookup("header")
		setCookieFn, _ := Lookup("setcookie")
		codeFn, _ := Lookup("http_response_code")

		hLine, _ := newStr(z, "X-Worker-Check: ok")
		_, _ = headerFn(z, out, []types.Value{hLine})
		types.ReleaseValue(z, &hLine)

		cName, _ := newStr(z, "sid")
		cVal, _ := newStr(z, "val token")
		_, _ = setCookieFn(z, out, []types.Value{cName, cVal})
		types.ReleaseValue(z, &cVal)
		types.ReleaseValue(z, &cName)

		_, _ = codeFn(z, out, []types.Value{types.NewLong(201)})

		// Lecture d'une valeur dans $_GET['a']['b'][0] et $_POST['user']['role']
		kA, _ := newStr(z, "a")
		kB, _ := newStr(z, "b")
		vA, okA, _ := types.ArrayGet(z, req.Get, kA, nil)
		if !okA {
			t.Errorf("missing $_GET['a']")
		} else if vB, okB, _ := types.ArrayGet(z, vA, kB, nil); !okB {
			t.Errorf("missing $_GET['a']['b']")
		} else if v0, ok0, _ := types.ArrayGet(z, vB, types.NewLong(0), nil); !ok0 || types.StringData(z, v0) != "1" {
			t.Errorf("unexpected $_GET['a']['b'][0]")
		}
		types.ReleaseValue(z, &kB)
		types.ReleaseValue(z, &kA)

		_, _ = io.WriteString(out, "ok:")
		_, _ = out.Write(req.RawInput)
		return nil
	})

	srv := NewServer(handler, Options{
		ZoneCapacity: 512 << 10,
		MaxWorkers:   1,
		ServerConfig: ServerConfig{
			DocumentRoot: "/var/www/app",
			ScriptName:   "/index.php",
			ServerName:   "php127.local",
			ServerPort:   "8080",
		},
		AfterReset: func(z *zone.Zone) {
			if used := z.LiveBytes(); used != 0 {
				t.Errorf("Zone.LiveBytes() after Reset = %d, want 0", used)
			}
			if roots := z.BufferedRoots(); roots != 0 {
				t.Errorf("Zone.BufferedRoots() after Reset = %d, want 0", roots)
			}
			if pending := z.PendingCleanups(); pending != 0 {
				t.Errorf("Zone.PendingCleanups() after Reset = %d, want 0", pending)
			}
			resetChecked.Add(1)
		},
	})
	_ = activeZonePtr

	for i := range 1000 {
		body := fmt.Sprintf("user[role]=admin&iter=%d", i)
		req := httptest.NewRequest(http.MethodPost, "http://php127.local:8080/index.php?a[b][]=1&a[b][]=2", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", "TestAgent/1.0")
		req.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})

		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != 201 {
			t.Fatalf("req %d: status=%d want 201", i, rec.Code)
		}
		if got := rec.Header().Get("X-Worker-Check"); got != "ok" {
			t.Fatalf("req %d: X-Worker-Check=%q want ok", i, got)
		}
		if got := rec.Header().Get("Set-Cookie"); !strings.Contains(got, "sid=val+token") {
			t.Fatalf("req %d: Set-Cookie=%q", i, got)
		}
		if got := rec.Body.String(); got != "ok:"+body {
			t.Fatalf("req %d: body=%q", i, got)
		}
	}

	if got := srv.ZonesCreated(); got != 1 {
		t.Fatalf("ZonesCreated=%d want 1", got)
	}
	if got := srv.RequestsHandled(); got != 1000 {
		t.Fatalf("RequestsHandled=%d want 1000", got)
	}
	if got := resetChecked.Load(); got != 1000 {
		t.Fatalf("resetChecked=%d want 1000", got)
	}
}

func TestSuperglobalsAndMultipartEndToEnd(t *testing.T) {
	prog := &bytecode.Program{Main: &bytecode.OpArray{Name: "main"}}
	runner := NewProgramRunner(prog, func(z *zone.Zone, p *bytecode.Program, req *RequestContext, out io.Writer) error {
		if p != prog {
			t.Fatal("program mismatch")
		}
		headerFn, _ := Lookup("header")
		headerRemoveFn, _ := Lookup("header_remove")
		headersListFn, _ := Lookup("headers_list")
		headersSentFn, _ := Lookup("headers_sent")

		// Location met automatiquement le code à 302 si code == 200
		h1, _ := newStr(z, "X-Temp: remove-me")
		h2, _ := newStr(z, "Location: /redirect")
		_, _ = headerFn(z, out, []types.Value{h1})
		_, _ = headerFn(z, out, []types.Value{h2})
		rmName, _ := newStr(z, "x-temp")
		_, _ = headerRemoveFn(z, out, []types.Value{rmName})

		listVal, _ := headersListFn(z, out, nil)
		sentVal, _ := headersSentFn(z, out, nil)

		payload := map[string]any{
			"server":       valueToJSON(z, req.Server),
			"get":          valueToJSON(z, req.Get),
			"post":         valueToJSON(z, req.Post),
			"cookie":       valueToJSON(z, req.Cookie),
			"request":      valueToJSON(z, req.RequestVars),
			"headers_list": valueToJSON(z, listVal),
			"headers_sent": valueToJSON(z, sentVal),
		}
		types.ReleaseValue(z, &listVal)
		types.ReleaseValue(z, &rmName)
		types.ReleaseValue(z, &h2)
		types.ReleaseValue(z, &h1)

		enc := json.NewEncoder(out)
		return enc.Encode(payload)
	})

	srv := NewServer(runner, Options{MaxWorkers: 2})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	var mpBody bytes.Buffer
	mw := multipart.NewWriter(&mpBody)
	_ = mw.WriteField("nested[field]", "mp_val")
	_ = mw.WriteField("shared", "from_post")
	_ = mw.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/test.php?filter[a][]=10&shared=from_get", &mpBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Custom-Token", "secret123")
	req.AddCookie(&http.Cookie{Name: "shared", Value: "from_cookie"})
	req.AddCookie(&http.Cookie{Name: "c_only", Value: "cookie_val"})

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status=%d want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/redirect" {
		t.Fatalf("Location=%q want /redirect", loc)
	}
	if resp.Header.Get("X-Temp") != "" {
		t.Fatalf("X-Temp was not removed")
	}

	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	srvMap := decoded["server"].(map[string]any)
	if srvMap["REQUEST_METHOD"] != "POST" || srvMap["HTTP_X_CUSTOM_TOKEN"] != "secret123" {
		t.Fatalf("unexpected $_SERVER: %+v", srvMap)
	}
	reqMap := decoded["request"].(map[string]any)
	// Ordre GPC : COOKIE écrase POST qui écrase GET
	if reqMap["shared"] != "from_cookie" || reqMap["c_only"] != "cookie_val" {
		t.Fatalf("unexpected $_REQUEST GPC GPC merge: %+v", reqMap)
	}
}

func TestHeaderAndCookieSemantics(t *testing.T) {
	z := zone.New(zone.DefaultCapacity)
	defer z.Reset(nil)

	ctx := &RequestContext{StatusCode: http.StatusOK}
	BindContext(z, ctx)
	defer BindContext(z, nil)

	headerFn, _ := Lookup("header")
	headersListFn, _ := Lookup("headers_list")
	headersSentFn, _ := Lookup("headers_sent")
	headerRemoveFn, _ := Lookup("header_remove")
	codeFn, _ := Lookup("http_response_code")
	setCookieFn, _ := Lookup("setcookie")
	setRawCookieFn, _ := Lookup("setrawcookie")

	// 1. HTTP/1.1 404 Not Found
	s404, _ := newStr(z, "HTTP/1.1 404 Not Found")
	if _, err := headerFn(z, io.Discard, []types.Value{s404}); err != nil {
		t.Fatal(err)
	}
	types.ReleaseValue(z, &s404)
	if ctx.StatusCode != 404 {
		t.Fatalf("status=%d want 404", ctx.StatusCode)
	}

	// 2. http_response_code(200) retourne l'ancien code (404)
	prevCode, err := codeFn(z, io.Discard, []types.Value{types.NewLong(200)})
	if err != nil || prevCode.ToLong() != 404 || ctx.StatusCode != 200 {
		t.Fatalf("http_response_code got prev=%v status=%d err=%v", prevCode, ctx.StatusCode, err)
	}

	// 3. Remplacement vs cumul d'en-têtes
	hx1, _ := newStr(z, "X-Multi: first")
	hx2, _ := newStr(z, "X-Multi: second")
	hx3, _ := newStr(z, "X-Multi: third")
	_, _ = headerFn(z, io.Discard, []types.Value{hx1, types.NewBool(true)})
	_, _ = headerFn(z, io.Discard, []types.Value{hx2, types.NewBool(true)})
	_, _ = headerFn(z, io.Discard, []types.Value{hx3, types.NewBool(false)})
	types.ReleaseValue(z, &hx3)
	types.ReleaseValue(z, &hx2)
	types.ReleaseValue(z, &hx1)

	// 4. setcookie avec tableau d'options et setrawcookie
	cName, _ := newStr(z, "prefs")
	cVal, _ := newStr(z, "a b")
	opts, _ := types.NewArray(z)
	pStr, _ := newStr(z, "/app")
	dStr, _ := newStr(z, "example.com")
	ssStr, _ := newStr(z, "Lax")
	_ = setStrKey(z, &opts, "path", pStr)
	_ = setStrKey(z, &opts, "domain", dStr)
	_ = setStrKey(z, &opts, "secure", types.NewBool(true))
	_ = setStrKey(z, &opts, "httponly", types.NewBool(true))
	_ = setStrKey(z, &opts, "samesite", ssStr)
	types.ReleaseValue(z, &ssStr)
	types.ReleaseValue(z, &dStr)
	types.ReleaseValue(z, &pStr)

	if okVal, err := setCookieFn(z, io.Discard, []types.Value{cName, cVal, opts}); err != nil || !okVal.ToBool() {
		t.Fatalf("setcookie options failed: %v", err)
	}
	types.ReleaseValue(z, &opts)
	types.ReleaseValue(z, &cVal)
	types.ReleaseValue(z, &cName)

	rName, _ := newStr(z, "rawc")
	rVal, _ := newStr(z, "raw_value_123")
	if okVal, err := setRawCookieFn(z, io.Discard, []types.Value{rName, rVal}); err != nil || !okVal.ToBool() {
		t.Fatalf("setrawcookie failed: %v", err)
	}
	types.ReleaseValue(z, &rVal)
	types.ReleaseValue(z, &rName)

	listVal, err := headersListFn(z, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	listSlice := valueToJSON(z, listVal).([]any)
	types.ReleaseValue(z, &listVal)
	if len(listSlice) != 4 {
		t.Fatalf("headers_list count=%d want 4: %+v", len(listSlice), listSlice)
	}
	if listSlice[0] != "X-Multi: second" || listSlice[1] != "X-Multi: third" {
		t.Fatalf("unexpected X-Multi headers: %+v", listSlice)
	}
	if !strings.Contains(listSlice[2].(string), "prefs=a+b; path=/app; domain=example.com; secure; HttpOnly; SameSite=Lax") {
		t.Fatalf("unexpected setcookie header: %v", listSlice[2])
	}
	if listSlice[3] != "Set-Cookie: rawc=raw_value_123" {
		t.Fatalf("unexpected setrawcookie header: %v", listSlice[3])
	}

	// 5. header_remove() sans argument vide tout
	_, _ = headerRemoveFn(z, io.Discard, nil)
	if len(ctx.Headers) != 0 {
		t.Fatalf("expected empty headers after header_remove(), got %d", len(ctx.Headers))
	}

	// 6. headers_sent(&$file, &$line)
	ctx.HeadersSent = true
	ctx.SentFilename = "/var/www/index.php"
	ctx.SentLine = 42
	fRef, _ := types.NewReference(z, types.NewNull())
	lRef, _ := types.NewReference(z, types.NewNull())
	sentRes, err := headersSentFn(z, io.Discard, []types.Value{fRef, lRef})
	if err != nil || !sentRes.ToBool() {
		t.Fatalf("headers_sent failed: %v", err)
	}
	if gotF := types.StringData(z, types.Deref(z, fRef)); gotF != "/var/www/index.php" {
		t.Fatalf("headers_sent file=%q", gotF)
	}
	if gotL := types.Deref(z, lRef).ToLong(); gotL != 42 {
		t.Fatalf("headers_sent line=%d", gotL)
	}
	types.ReleaseValue(z, &lRef)
	types.ReleaseValue(z, &fRef)

	// 7. Rejet d'injection CRLF dans header()
	badH, _ := newStr(z, "X-Test: a\r\nX-Injected: b")
	_, err = headerFn(z, io.Discard, []types.Value{badH})
	types.ReleaseValue(z, &badH)
	if ExceptionClass(err) != "ValueError" {
		t.Fatalf("expected ValueError on CRLF header injection, got %v", err)
	}
}

