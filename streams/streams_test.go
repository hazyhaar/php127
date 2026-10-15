package streams

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func callNative(t *testing.T, z *zone.Zone, out *bytes.Buffer, name string, args ...types.Value) types.Value {
	t.Helper()
	fn, ok := Lookup(name)
	if !ok {
		t.Fatalf("Lookup(%q) failed", name)
	}
	// Un *bytes.Buffer nil converti en io.Writer serait une interface non nil : le passer tel quel ferait paniquer tout avertissement.
	var w io.Writer
	if out != nil {
		w = out
	}
	v, err := fn(z, w, args)
	if err != nil {
		t.Fatalf("%s returned unexpected error: %v", name, err)
	}
	return v
}

func mustStr(t *testing.T, z *zone.Zone, s string) types.Value {
	t.Helper()
	v, err := newStr(z, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestZoneResetClosesAllStreamsAndDirs(t *testing.T) {
	z := zone.New(65536)
	dir := t.TempDir()
	fpath := filepath.Join(dir, "leak_check.txt")

	sPath := mustStr(t, z, fpath)
	sMode := mustStr(t, z, "w+")
	res := callNative(t, z, nil, "fopen", sPath, sMode)
	if res.Type != types.TypeResource {
		t.Fatalf("expected resource, got %v", res)
	}
	memURI := mustStr(t, z, "php://temp/maxmemory:4")
	resTemp := callNative(t, z, nil, "fopen", memURI, sMode)
	payload := mustStr(t, z, "0123456789abcdef")
	callNative(t, z, nil, "fwrite", resTemp, payload)

	sDir := mustStr(t, z, dir)
	dirRes := callNative(t, z, nil, "opendir", sDir)
	if dirRes.Type != types.TypeResource {
		t.Fatalf("expected dir resource, got %v", dirRes)
	}

	if got := OpenStreamCount(z); got != 2 {
		t.Fatalf("OpenStreamCount before Reset = %d; want 2", got)
	}
	if got := OpenDirCount(z); got != 1 {
		t.Fatalf("OpenDirCount before Reset = %d; want 1", got)
	}

	z.Reset(DispatchCleanup)

	if got := OpenStreamCount(z); got != 0 {
		t.Fatalf("OpenStreamCount after Reset = %d; want 0", got)
	}
	if got := OpenDirCount(z); got != 0 {
		t.Fatalf("OpenDirCount after Reset = %d; want 0", got)
	}
}

func TestStreamSchemesAndModes(t *testing.T) {
	z := zone.New(131072)
	defer z.Reset(DispatchCleanup)

	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")

	// 1. Modes x and x+ (exclusive creation)
	sFileA := mustStr(t, z, fileA)
	sModeX := mustStr(t, z, "x+")
	fp := callNative(t, z, nil, "fopen", sFileA, sModeX)
	if fp.Type != types.TypeResource {
		t.Fatalf("fopen(x+) failed: %v", fp)
	}
	hello := mustStr(t, z, "hello\nworld")
	n := callNative(t, z, nil, "fwrite", fp, hello)
	if n.ToLong() != 11 {
		t.Fatalf("fwrite = %d; want 11", n.ToLong())
	}
	// Second x+ on existing file must return false
	fpDup := callNative(t, z, nil, "fopen", sFileA, sModeX)
	if fpDup.Type != types.TypeFalse {
		t.Fatalf("fopen(x+) on existing file = %v; want false", fpDup)
	}

	callNative(t, z, nil, "rewind", fp)
	if pos := callNative(t, z, nil, "ftell", fp); pos.ToLong() != 0 {
		t.Fatalf("ftell after rewind = %d; want 0", pos.ToLong())
	}
	line1 := callNative(t, z, nil, "fgets", fp)
	if got := types.StringData(z, line1); got != "hello\n" {
		t.Fatalf("fgets = %q; want %q", got, "hello\n")
	}
	ch := callNative(t, z, nil, "fgetc", fp)
	if got := types.StringData(z, ch); got != "w" {
		t.Fatalf("fgetc = %q; want 'w'", got)
	}
	rest := callNative(t, z, nil, "stream_get_contents", fp)
	if got := types.StringData(z, rest); got != "orld" {
		t.Fatalf("stream_get_contents = %q; want 'orld'", got)
	}
	if eof := callNative(t, z, nil, "feof", fp); !eof.ToBool() {
		t.Fatal("feof should be true after stream_get_contents")
	}
	if ok := callNative(t, z, nil, "ftruncate", fp, types.NewLong(5)); !ok.ToBool() {
		t.Fatal("ftruncate failed")
	}
	callNative(t, z, nil, "fseek", fp, types.NewLong(0), types.NewLong(SEEK_SET))
	truncated := callNative(t, z, nil, "fread", fp, types.NewLong(100))
	if got := types.StringData(z, truncated); got != "hello" {
		t.Fatalf("after ftruncate: %q; want 'hello'", got)
	}
	st := callNative(t, z, nil, "fstat", fp)
	if st.Type != types.TypeArray || types.ArrayLen(z, st) != 26 {
		t.Fatalf("fstat array len = %d; want 26", types.ArrayLen(z, st))
	}
	callNative(t, z, nil, "fflush", fp)
	callNative(t, z, nil, "fclose", fp)

	// 2. Modes c, a, r with file:// URI
	fileURI := mustStr(t, z, "file://"+fileA)
	sModeA := mustStr(t, z, "ab")
	fpA := callNative(t, z, nil, "fopen", fileURI, sModeA)
	bang := mustStr(t, z, "!")
	callNative(t, z, nil, "fputs", fpA, bang)
	callNative(t, z, nil, "fclose", fpA)

	sModeC := mustStr(t, z, "c+")
	fpC := callNative(t, z, nil, "fopen", fileURI, sModeC)
	allC := callNative(t, z, nil, "stream_get_contents", fpC)
	if got := types.StringData(z, allC); got != "hello!" {
		t.Fatalf("mode c+ read = %q; want 'hello!'", got)
	}
	callNative(t, z, nil, "fclose", fpC)

	// 3. php://memory & stream_copy_to_stream
	memURI := mustStr(t, z, "php://memory")
	sModeRW := mustStr(t, z, "r+")
	m1 := callNative(t, z, nil, "fopen", memURI, sModeRW)
	m2 := callNative(t, z, nil, "fopen", memURI, sModeRW)
	callNative(t, z, nil, "fwrite", m1, mustStr(t, z, "copy-payload"))
	callNative(t, z, nil, "rewind", m1)
	copied := callNative(t, z, nil, "stream_copy_to_stream", m1, m2)
	if copied.ToLong() != 12 {
		t.Fatalf("stream_copy_to_stream = %d; want 12", copied.ToLong())
	}
	callNative(t, z, nil, "rewind", m2)
	var outBuf bytes.Buffer
	pass := callNative(t, z, &outBuf, "fpassthru", m2)
	if pass.ToLong() != 12 || outBuf.String() != "copy-payload" {
		t.Fatalf("fpassthru = %d (%q); want 12 ('copy-payload')", pass.ToLong(), outBuf.String())
	}
	meta := callNative(t, z, nil, "stream_get_meta_data", m2)
	if meta.Type != types.TypeArray {
		t.Fatal("stream_get_meta_data did not return array")
	}
	callNative(t, z, nil, "fclose", m1)
	callNative(t, z, nil, "fclose", m2)

	// 4. php://input, php://output, php://stdout, php://stderr
	SetInput(z, []byte("raw-request-body"))
	var stdoutBuf, stderrBuf bytes.Buffer
	SetOutput(z, &stdoutBuf)
	SetStderr(z, &stderrBuf)

	inTxt := callNative(t, z, nil, "file_get_contents", mustStr(t, z, "php://input"))
	if got := types.StringData(z, inTxt); got != "raw-request-body" {
		t.Fatalf("php://input = %q; want 'raw-request-body'", got)
	}
	callNative(t, z, nil, "file_put_contents", mustStr(t, z, "php://output"), mustStr(t, z, "out1-"))
	callNative(t, z, nil, "file_put_contents", mustStr(t, z, "php://stdout"), mustStr(t, z, "out2"))
	callNative(t, z, nil, "file_put_contents", mustStr(t, z, "php://stderr"), mustStr(t, z, "err1"))
	if stdoutBuf.String() != "out1-out2" {
		t.Fatalf("stdout = %q; want 'out1-out2'", stdoutBuf.String())
	}
	if stderrBuf.String() != "err1" {
		t.Fatalf("stderr = %q; want 'err1'", stderrBuf.String())
	}

	// 5. data:// (RFC 2397 base64 and plain)
	dataB64 := mustStr(t, z, "data://text/plain;base64,SGVsbG8gPHP=")
	d1 := callNative(t, z, nil, "file_get_contents", dataB64)
	if got := types.StringData(z, d1); got != "Hello \x3c\x73" {
		t.Fatalf("data:// base64 = %q", got)
	}
	dataPlain := mustStr(t, z, "data:,Hello%20World")
	d2 := callNative(t, z, nil, "file_get_contents", dataPlain)
	if got := types.StringData(z, d2); got != "Hello World" {
		t.Fatalf("data:, plain = %q; want 'Hello World'", got)
	}
}

func TestCSVAndFilesystemOperations(t *testing.T) {
	z := zone.New(262144)
	defer z.Reset(DispatchCleanup)
	SetOutput(z, nil)

	// CSV roundtrip
	mem := callNative(t, z, nil, "fopen", mustStr(t, z, "php://memory"), mustStr(t, z, "r+"))
	row, err := types.NewArray(z)
	if err != nil {
		t.Fatal(err)
	}
	for _, cell := range []string{"alpha", "beta,gamma", `quote"here`, "space here"} {
		_ = types.ArrayAppend(z, &row, mustStr(t, z, cell))
	}
	written := callNative(t, z, nil, "fputcsv", mem, row)
	if written.ToLong() <= 0 {
		t.Fatalf("fputcsv returned %d", written.ToLong())
	}
	callNative(t, z, nil, "rewind", mem)
	parsed := callNative(t, z, nil, "fgetcsv", mem)
	if parsed.Type != types.TypeArray || types.ArrayLen(z, parsed) != 4 {
		t.Fatalf("fgetcsv len = %d; want 4", types.ArrayLen(z, parsed))
	}
	eofRow := callNative(t, z, nil, "fgetcsv", mem)
	if eofRow.Type != types.TypeFalse {
		t.Fatalf("fgetcsv at EOF = %v; want false", eofRow)
	}
	callNative(t, z, nil, "fclose", mem)

	// Filesystem operations
	dir := t.TempDir()
	subDir := filepath.Join(dir, "nested", "inner")
	if ok := callNative(t, z, nil, "mkdir", mustStr(t, z, subDir), types.NewLong(0o755), types.NewBool(true)); !ok.ToBool() {
		t.Fatal("mkdir recursive failed")
	}
	if !callNative(t, z, nil, "is_dir", mustStr(t, z, subDir)).ToBool() {
		t.Fatal("is_dir failed")
	}

	f1 := filepath.Join(subDir, "one.txt")
	f2 := filepath.Join(subDir, "two.txt")
	fLink := filepath.Join(subDir, "link.txt")

	callNative(t, z, nil, "file_put_contents", mustStr(t, z, f1), mustStr(t, z, "line1\n\nline2\n"))
	callNative(t, z, nil, "file_put_contents", mustStr(t, z, f1), mustStr(t, z, "line3"), types.NewLong(FILE_APPEND|LOCK_EX))

	lines := callNative(t, z, nil, "file", mustStr(t, z, f1), types.NewLong(FILE_IGNORE_NEW_LINES|FILE_SKIP_EMPTY_LINES))
	if types.ArrayLen(z, lines) != 3 {
		t.Fatalf("file() lines = %d; want 3", types.ArrayLen(z, lines))
	}

	var rfBuf bytes.Buffer
	rfCount := callNative(t, z, &rfBuf, "readfile", mustStr(t, z, f1))
	if rfCount.ToLong() != 18 || rfBuf.String() != "line1\n\nline2\nline3" {
		t.Fatalf("readfile = %d (%q)", rfCount.ToLong(), rfBuf.String())
	}

	if !callNative(t, z, nil, "file_exists", mustStr(t, z, f1)).ToBool() {
		t.Fatal("file_exists false")
	}
	if !callNative(t, z, nil, "is_file", mustStr(t, z, f1)).ToBool() {
		t.Fatal("is_file false")
	}
	if !callNative(t, z, nil, "is_readable", mustStr(t, z, f1)).ToBool() {
		t.Fatal("is_readable false")
	}
	if !callNative(t, z, nil, "is_writable", mustStr(t, z, f1)).ToBool() {
		t.Fatal("is_writable false")
	}
	if !callNative(t, z, nil, "is_writeable", mustStr(t, z, f1)).ToBool() {
		t.Fatal("is_writeable false")
	}
	if sz := callNative(t, z, nil, "filesize", mustStr(t, z, f1)); sz.ToLong() != 18 {
		t.Fatalf("filesize = %d; want 18", sz.ToLong())
	}

	if !callNative(t, z, nil, "touch", mustStr(t, z, f1), types.NewLong(1700000000), types.NewLong(1700000100)).ToBool() {
		t.Fatal("touch failed")
	}
	if mt := callNative(t, z, nil, "filemtime", mustStr(t, z, f1)); mt.ToLong() != 1700000000 {
		t.Fatalf("filemtime = %d; want 1700000000", mt.ToLong())
	}
	if at := callNative(t, z, nil, "fileatime", mustStr(t, z, f1)); at.ToLong() != 1700000100 {
		t.Fatalf("fileatime = %d; want 1700000100", at.ToLong())
	}
	if ct := callNative(t, z, nil, "filectime", mustStr(t, z, f1)); ct.ToLong() <= 0 {
		t.Fatalf("filectime = %d", ct.ToLong())
	}
	if !callNative(t, z, nil, "chmod", mustStr(t, z, f1), types.NewLong(0o640)).ToBool() {
		t.Fatal("chmod failed")
	}
	if perms := callNative(t, z, nil, "fileperms", mustStr(t, z, f1)); perms.ToLong()&0o777 != 0o640 {
		t.Fatalf("fileperms = %o", perms.ToLong())
	}
	if ft := callNative(t, z, nil, "filetype", mustStr(t, z, f1)); types.StringData(z, ft) != "file" {
		t.Fatalf("filetype = %q; want 'file'", types.StringData(z, ft))
	}

	if !callNative(t, z, nil, "copy", mustStr(t, z, f1), mustStr(t, z, f2)).ToBool() {
		t.Fatal("copy failed")
	}
	if err := os.Symlink(f2, fLink); err != nil {
		t.Fatal(err)
	}
	if !callNative(t, z, nil, "is_link", mustStr(t, z, fLink)).ToBool() {
		t.Fatal("is_link false")
	}
	if ft := callNative(t, z, nil, "filetype", mustStr(t, z, fLink)); types.StringData(z, ft) != "link" {
		t.Fatalf("filetype(link) = %q; want 'link'", types.StringData(z, ft))
	}
	st := callNative(t, z, nil, "stat", mustStr(t, z, fLink))
	lst := callNative(t, z, nil, "lstat", mustStr(t, z, fLink))
	if st.Type != types.TypeArray || lst.Type != types.TypeArray {
		t.Fatal("stat/lstat failed")
	}
	rp := callNative(t, z, nil, "realpath", mustStr(t, z, fLink))
	if types.StringData(z, rp) != f2 {
		t.Fatalf("realpath = %q; want %q", types.StringData(z, rp), f2)
	}

	// scandir, glob, opendir/readdir/rewinddir/closedir
	scAsc := callNative(t, z, nil, "scandir", mustStr(t, z, subDir), types.NewLong(SCANDIR_SORT_ASCENDING))
	scDesc := callNative(t, z, nil, "scandir", mustStr(t, z, subDir), types.NewLong(SCANDIR_SORT_DESCENDING))
	if types.ArrayLen(z, scAsc) != 5 || types.ArrayLen(z, scDesc) != 5 {
		t.Fatalf("scandir len = %d", types.ArrayLen(z, scAsc))
	}
	gl := callNative(t, z, nil, "glob", mustStr(t, z, filepath.Join(subDir, "*.txt")))
	if types.ArrayLen(z, gl) != 3 {
		t.Fatalf("glob len = %d; want 3", types.ArrayLen(z, gl))
	}

	dh := callNative(t, z, nil, "opendir", mustStr(t, z, subDir))
	var count int
	for {
		ent := callNative(t, z, nil, "readdir", dh)
		if ent.Type == types.TypeFalse {
			break
		}
		count++
	}
	if count != 5 {
		t.Fatalf("readdir count = %d; want 5", count)
	}
	callNative(t, z, nil, "rewinddir", dh)
	first := callNative(t, z, nil, "readdir", dh)
	if first.Type != types.TypeString {
		t.Fatal("readdir after rewinddir failed")
	}
	callNative(t, z, nil, "closedir", dh)

	// chdir & getcwd
	if !callNative(t, z, nil, "chdir", mustStr(t, z, subDir)).ToBool() {
		t.Fatal("chdir failed")
	}
	cwdVal := callNative(t, z, nil, "getcwd")
	if types.StringData(z, cwdVal) != subDir {
		t.Fatalf("getcwd = %q; want %q", types.StringData(z, cwdVal), subDir)
	}

	// rename, unlink, rmdir, tempnam, sys_get_temp_dir
	f3 := filepath.Join(subDir, "three.txt")
	if !callNative(t, z, nil, "rename", mustStr(t, z, f2), mustStr(t, z, f3)).ToBool() {
		t.Fatal("rename failed")
	}
	for _, p := range []string{f1, f3, fLink} {
		if !callNative(t, z, nil, "unlink", mustStr(t, z, p)).ToBool() {
			t.Fatalf("unlink(%s) failed", p)
		}
	}
	if !callNative(t, z, nil, "rmdir", mustStr(t, z, subDir)).ToBool() {
		t.Fatal("rmdir failed")
	}

	tmpDir := callNative(t, z, nil, "sys_get_temp_dir")
	if types.StringData(z, tmpDir) == "" {
		t.Fatal("sys_get_temp_dir empty")
	}
	tmpFile := callNative(t, z, nil, "tempnam", mustStr(t, z, dir), mustStr(t, z, "pfx_"))
	if tmpFile.Type != types.TypeString {
		t.Fatal("tempnam failed")
	}
	callNative(t, z, nil, "unlink", tmpFile)
}

func TestPHPDifferentialStreams(t *testing.T) {
	if _, err := exec.LookPath("/usr/bin/php"); err != nil {
		t.Skip("/usr/bin/php not available")
	}
	phpScript := `
$out = [];
$out["dirname_1"] = dirname("/a/b/c", 1);
$out["dirname_2"] = dirname("/a/b/c", 2);
$out["dirname_3"] = dirname("/a/b/c", 3);
$out["dirname_rel"] = dirname("c.txt");
$out["basename_1"] = basename("/a/b/c.txt", ".txt");
$out["basename_2"] = basename("/a/b/c.txt", "txt");
$out["basename_3"] = basename("/a/b/");
$out["pathinfo_all"] = pathinfo("/var/www/index.html");
$out["pathinfo_noext"] = pathinfo("/var/www/README");
$out["pathinfo_ext"] = pathinfo("/var/www/index.html", PATHINFO_EXTENSION);
$out["pathinfo_fname"] = pathinfo("/var/www/index.html", PATHINFO_FILENAME);

$fp = fopen("php://memory", "r+");
fputcsv($fp, ["id", "name,role", "quote\"val"]);
rewind($fp);
$out["csv_raw"] = stream_get_contents($fp);
rewind($fp);
$out["csv_parsed"] = fgetcsv($fp);
fclose($fp);

$out["data_b64"] = file_get_contents("data://text/plain;base64,VGVzdDEyMw==");
echo json_encode($out);
`
	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-r", phpScript)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("/usr/bin/php failed: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	z := zone.New(262144)
	defer z.Reset(DispatchCleanup)

	checkStr := func(key string, got types.Value) {
		t.Helper()
		if got.Type != types.TypeString {
			t.Fatalf("%s: expected string, got %v", key, got)
		}
		if s := types.StringData(z, got); s != want[key].(string) {
			t.Fatalf("%s: got %q, want %q", key, s, want[key])
		}
	}

	checkStr("dirname_1", callNative(t, z, nil, "dirname", mustStr(t, z, "/a/b/c"), types.NewLong(1)))
	checkStr("dirname_2", callNative(t, z, nil, "dirname", mustStr(t, z, "/a/b/c"), types.NewLong(2)))
	checkStr("dirname_3", callNative(t, z, nil, "dirname", mustStr(t, z, "/a/b/c"), types.NewLong(3)))
	checkStr("dirname_rel", callNative(t, z, nil, "dirname", mustStr(t, z, "c.txt")))
	checkStr("basename_1", callNative(t, z, nil, "basename", mustStr(t, z, "/a/b/c.txt"), mustStr(t, z, ".txt")))
	checkStr("basename_2", callNative(t, z, nil, "basename", mustStr(t, z, "/a/b/c.txt"), mustStr(t, z, "txt")))
	checkStr("basename_3", callNative(t, z, nil, "basename", mustStr(t, z, "/a/b/")))
	checkStr("pathinfo_ext", callNative(t, z, nil, "pathinfo", mustStr(t, z, "/var/www/index.html"), types.NewLong(PATHINFO_EXTENSION)))
	checkStr("pathinfo_fname", callNative(t, z, nil, "pathinfo", mustStr(t, z, "/var/www/index.html"), types.NewLong(PATHINFO_FILENAME)))
	checkStr("data_b64", callNative(t, z, nil, "file_get_contents", mustStr(t, z, "data://text/plain;base64,VGVzdDEyMw==")))

	fp := callNative(t, z, nil, "fopen", mustStr(t, z, "php://memory"), mustStr(t, z, "r+"))
	arr, _ := types.NewArray(z)
	for _, s := range []string{"id", "name,role", "quote\"val"} {
		_ = types.ArrayAppend(z, &arr, mustStr(t, z, s))
	}
	callNative(t, z, nil, "fputcsv", fp, arr)
	callNative(t, z, nil, "rewind", fp)
	checkStr("csv_raw", callNative(t, z, nil, "stream_get_contents", fp))
	callNative(t, z, nil, "fclose", fp)
}

// Lot 7.3 : fichiers, contextes, verrous et sockets clientes, avec oracle /usr/bin/php -n.

func callErr(t *testing.T, z *zone.Zone, name string, args ...types.Value) (types.Value, error) {
	t.Helper()
	fn, ok := Lookup(name)
	if !ok {
		t.Fatalf("Lookup(%q) failed", name)
	}
	return fn(z, nil, args)
}

func phpJSON(t *testing.T, script string, args ...string) map[string]any {
	t.Helper()
	if _, err := exec.LookPath("/usr/bin/php"); err != nil {
		t.Skip("/usr/bin/php not available")
	}
	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", append([]string{"-n", "-r", script}, args...)...)
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("/usr/bin/php: %v (%s)", err, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json: %v (%s)", err, raw)
	}
	return out
}

// toAny convertit une valeur de Zone dans la forme que json_decode donne côté PHP.
func toAny(z *zone.Zone, v types.Value) any {
	v = deref(z, v)
	switch v.Type {
	case types.TypeNull, types.TypeUndef:
		return nil
	case types.TypeTrue:
		return true
	case types.TypeFalse:
		return false
	case types.TypeLong:
		return float64(v.ToLong())
	case types.TypeDouble:
		return v.ToDouble()
	case types.TypeString:
		// Copie : la chaîne de Zone peut être libérée dès que la référence est réassignée.
		return strings.Clone(types.StringData(z, v))
	case types.TypeArray:
		list := true
		i := int64(0)
		m := map[string]any{}
		var l []any
		types.ArrayForEach(z, v, func(k, e types.Value) bool {
			if k.Type != types.TypeLong || k.ToLong() != i {
				list = false
			}
			i++
			ks := ""
			if k.Type == types.TypeLong {
				ks = strconv.FormatInt(k.ToLong(), 10)
			} else {
				ks = types.StringData(z, k)
			}
			m[ks] = toAny(z, e)
			l = append(l, toAny(z, e))
			return true
		})
		if list {
			if l == nil {
				return []any{}
			}
			return l
		}
		return m
	default:
		return "resource"
	}
}

func sameJSON(t *testing.T, key string, got any, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if !bytes.Equal(g, w) {
		t.Errorf("%s: got %s want %s", key, g, w)
	}
}

func TestPHPDifferentialWPStreams(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "lock.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ini := filepath.Join(dir, "conf.ini")
	if err := os.WriteFile(ini, []byte("[db]\nhost = localhost\nport = 3306\ndebug = on\nlist[] = a\nlist[] = b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := phpJSON(t, `
$f = $argv[1]; $ini = $argv[2]; $dir = $argv[3];
$o = [];
$o["clearstatcache"] = clearstatcache();
$o["clearstatcache2"] = clearstatcache(true, $f);
$c = stream_context_create(["http"=>["method"=>"POST","timeout"=>5]], ["notification"=>null, "options"=>["ssl"=>["verify_peer"=>false]]]);
$o["ctx_opts"] = stream_context_get_options($c);
$o["ctx_set1"] = stream_context_set_option($c, "http", "header", "X: y");
$o["ctx_set2"] = stream_context_set_option($c, ["ssl"=>["cafile"=>"/a"], "ftp"=>["overwrite"=>true]]);
$o["ctx_opts2"] = stream_context_get_options($c);
$o["ctx_empty"] = stream_context_get_options(stream_context_create());
foreach (["ctx_bad1"=>["http"=>"x"], "ctx_bad2"=>[1=>["a"=>1]]] as $k=>$bad) {
  try { stream_context_create($bad); $o[$k] = "ok"; } catch (Throwable $e) { $o[$k] = get_class($e).": ".$e->getMessage(); }
}
$h = fopen($f, "r+"); $m = fopen("php://memory", "r+");
$o["file_opts"] = stream_context_get_options($h);
foreach (["file"=>$h, "mem"=>$m] as $k=>$s) {
  $o[$k."_set"] = [stream_set_timeout($s, 1), stream_set_blocking($s, false), stream_set_write_buffer($s, 0), stream_set_read_buffer($s, 0)];
  $o[$k."_lock"] = [flock($s, LOCK_SH, $wb), $wb, flock($s, LOCK_EX | LOCK_NB, $wb), $wb, flock($s, LOCK_UN), flock($s, 7)];
}
try { flock($h, 0); } catch (Throwable $e) { $o["flock_bad"] = get_class($e).": ".$e->getMessage(); }
$o["ini"] = parse_ini_file($ini, true);
$o["ini_typed"] = parse_ini_file($ini, false, INI_SCANNER_TYPED);
$o["ini_missing"] = @parse_ini_file($dir."/absent.ini");
try { parse_ini_file(""); } catch (Throwable $e) { $o["ini_empty"] = get_class($e).": ".$e->getMessage(); }
$o["disk_total"] = disk_total_space($dir);
$o["disk_bad"] = @disk_free_space($dir."/absent");
$o["readlink_bad"] = @readlink($dir."/absent");
$o["chown_bad"] = @chown($f, "utilisateur_inexistant_php127");
$o["getimagesize_missing"] = @getimagesize($dir."/absent.png");
echo json_encode($o);
`, file, ini, dir)

	z := zone.New(1 << 20)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	arr := func(kv ...any) types.Value {
		a, _ := types.NewArray(z)
		for i := 0; i < len(kv); i += 2 {
			var val types.Value
			switch x := kv[i+1].(type) {
			case string:
				val = s(x)
			case int:
				val = types.NewLong(int64(x))
			case bool:
				val = types.NewBool(x)
			case types.Value:
				val = x
			case nil:
				val = types.NewNull()
			}
			switch k := kv[i].(type) {
			case string:
				_ = types.ArraySet(z, &a, s(k), val, warnDrop)
			case int:
				_ = types.ArraySet(z, &a, types.NewLong(int64(k)), val, warnDrop)
			}
		}
		return a
	}
	got := map[string]any{}
	got["clearstatcache"] = toAny(z, callNative(t, z, nil, "clearstatcache"))
	got["clearstatcache2"] = toAny(z, callNative(t, z, nil, "clearstatcache", types.NewBool(true), s(file)))
	c := callNative(t, z, nil, "stream_context_create",
		arr("http", arr("method", "POST", "timeout", 5)),
		arr("notification", nil, "options", arr("ssl", arr("verify_peer", false))))
	got["ctx_opts"] = toAny(z, callNative(t, z, nil, "stream_context_get_options", c))
	got["ctx_set1"] = toAny(z, callNative(t, z, nil, "stream_context_set_option", c, s("http"), s("header"), s("X: y")))
	got["ctx_set2"] = toAny(z, callNative(t, z, nil, "stream_context_set_option", c, arr("ssl", arr("cafile", "/a"), "ftp", arr("overwrite", true))))
	got["ctx_opts2"] = toAny(z, callNative(t, z, nil, "stream_context_get_options", c))
	got["ctx_empty"] = toAny(z, callNative(t, z, nil, "stream_context_get_options", callNative(t, z, nil, "stream_context_create")))
	for k, bad := range map[string]types.Value{"ctx_bad1": arr("http", "x"), "ctx_bad2": arr(1, arr("a", 1))} {
		if _, err := callErr(t, z, "stream_context_create", bad); err != nil {
			got[k] = ExceptionClass(err) + ": " + err.Error()
		} else {
			got[k] = "ok"
		}
	}
	h := callNative(t, z, nil, "fopen", s(file), s("r+"))
	m := callNative(t, z, nil, "fopen", s("php://memory"), s("r+"))
	got["file_opts"] = toAny(z, callNative(t, z, nil, "stream_context_get_options", h))
	for k, st := range map[string]types.Value{"file": h, "mem": m} {
		got[k+"_set"] = []any{
			toAny(z, callNative(t, z, nil, "stream_set_timeout", st, types.NewLong(1))),
			toAny(z, callNative(t, z, nil, "stream_set_blocking", st, types.NewBool(false))),
			toAny(z, callNative(t, z, nil, "stream_set_write_buffer", st, types.NewLong(0))),
			toAny(z, callNative(t, z, nil, "stream_set_read_buffer", st, types.NewLong(0))),
		}
		wb, _ := types.NewReference(z, types.NewNull())
		l1 := toAny(z, callNative(t, z, nil, "flock", st, types.NewLong(LOCK_SH), wb))
		w1 := toAny(z, wb)
		l2 := toAny(z, callNative(t, z, nil, "flock", st, types.NewLong(LOCK_EX|LOCK_NB), wb))
		w2 := toAny(z, wb)
		got[k+"_lock"] = []any{l1, w1, l2, w2,
			toAny(z, callNative(t, z, nil, "flock", st, types.NewLong(LOCK_UN))),
			toAny(z, callNative(t, z, nil, "flock", st, types.NewLong(7)))}
	}
	if _, err := callErr(t, z, "flock", h, types.NewLong(0)); err != nil {
		got["flock_bad"] = ExceptionClass(err) + ": " + err.Error()
	}
	got["ini"] = toAny(z, callNative(t, z, nil, "parse_ini_file", s(ini), types.NewBool(true)))
	got["ini_typed"] = toAny(z, callNative(t, z, nil, "parse_ini_file", s(ini), types.NewBool(false), types.NewLong(2)))
	got["ini_missing"] = toAny(z, callNative(t, z, nil, "parse_ini_file", s(filepath.Join(dir, "absent.ini"))))
	if _, err := callErr(t, z, "parse_ini_file", s("")); err != nil {
		got["ini_empty"] = ExceptionClass(err) + ": " + err.Error()
	}
	got["disk_total"] = toAny(z, callNative(t, z, nil, "disk_total_space", s(dir)))
	got["disk_bad"] = toAny(z, callNative(t, z, nil, "disk_free_space", s(filepath.Join(dir, "absent"))))
	got["readlink_bad"] = toAny(z, callNative(t, z, nil, "readlink", s(filepath.Join(dir, "absent"))))
	got["chown_bad"] = toAny(z, callNative(t, z, nil, "chown", s(file), s("utilisateur_inexistant_php127")))
	got["getimagesize_missing"] = toAny(z, callNative(t, z, nil, "getimagesize", s(filepath.Join(dir, "absent.png"))))
	for k, w := range want {
		sameJSON(t, k, got[k], w)
	}
	if len(got) != len(want) {
		t.Errorf("clés : %d obtenues, %d attendues", len(got), len(want))
	}
}

func TestWPStreamsFilesystem(t *testing.T) {
	z := zone.New(1 << 18)
	defer z.Reset(DispatchCleanup)
	dir := t.TempDir()
	target := filepath.Join(dir, "cible.txt")
	if err := os.WriteFile(target, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := func(v string) types.Value { return mustStr(t, z, v) }
	if v := callNative(t, z, nil, "symlink", s("cible.txt"), s(filepath.Join(dir, "lien"))); v.Type != types.TypeTrue {
		t.Fatalf("symlink = %v", v)
	}
	if got := types.StringData(z, callNative(t, z, nil, "readlink", s(filepath.Join(dir, "lien")))); got != "cible.txt" {
		t.Fatalf("readlink = %q", got)
	}
	if v := callNative(t, z, nil, "link", s(target), s(filepath.Join(dir, "dur"))); v.Type != types.TypeTrue {
		t.Fatalf("link = %v", v)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "dur")); err != nil || string(b) != "data" {
		t.Fatalf("lien dur illisible : %v", err)
	}
	if v := callNative(t, z, nil, "link", s(target), s(filepath.Join(dir, "dur"))); v.Type != types.TypeFalse {
		t.Fatalf("link sur cible existante = %v", v)
	}
	if v := callNative(t, z, nil, "chown", s(target), types.NewLong(int64(os.Getuid()))); v.Type != types.TypeTrue {
		t.Fatalf("chown(uid) = %v", v)
	}
	if v := callNative(t, z, nil, "chgrp", s(target), types.NewLong(int64(os.Getgid()))); v.Type != types.TypeTrue {
		t.Fatalf("chgrp(gid) = %v", v)
	}
	if v := callNative(t, z, nil, "chgrp", s(target), s("groupe_inexistant_php127")); v.Type != types.TypeFalse {
		t.Fatalf("chgrp(inconnu) = %v", v)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		t.Fatal(err)
	}
	free := callNative(t, z, nil, "disk_free_space", s(dir))
	alias := callNative(t, z, nil, "diskfreespace", s(dir))
	if free.Type != types.TypeDouble || alias.Type != types.TypeDouble || free.ToDouble() <= 0 {
		t.Fatalf("disk_free_space = %v, diskfreespace = %v", free, alias)
	}
	if total := callNative(t, z, nil, "disk_total_space", s(dir)).ToDouble(); total != float64(st.Blocks)*float64(st.Frsize) {
		t.Fatalf("disk_total_space = %v", total)
	}
	old := callNative(t, z, nil, "umask")
	prev := callNative(t, z, nil, "umask", types.NewLong(0o027))
	if prev.ToLong() != old.ToLong() {
		t.Fatalf("umask(027) rend %o, attendu %o", prev.ToLong(), old.ToLong())
	}
	if cur := callNative(t, z, nil, "umask", types.NewNull()); cur.ToLong() != 0o027 {
		t.Fatalf("umask() après pose = %o", cur.ToLong())
	}
	callNative(t, z, nil, "umask", old)
	w := callNative(t, z, nil, "stream_get_wrappers")
	if types.ArrayLen(z, w) != uint32(len(builtinWrappers)) {
		t.Fatalf("stream_get_wrappers: %d", types.ArrayLen(z, w))
	}
	if v := callNative(t, z, nil, "stream_wrapper_register", s("FILE"), s("X")); v.Type != types.TypeFalse {
		t.Fatalf("stream_wrapper_register(file) = %v", v)
	}
	if v := callNative(t, z, nil, "stream_wrapper_register", s("s3"), s("S3Wrapper")); v.Type != types.TypeTrue {
		t.Fatalf("stream_wrapper_register(s3) = %v", v)
	}
	if v := callNative(t, z, nil, "stream_wrapper_register", s("s3"), s("S3Wrapper")); v.Type != types.TypeFalse {
		t.Fatalf("stream_wrapper_register(s3) répété = %v", v)
	}
	if types.ArrayLen(z, callNative(t, z, nil, "stream_get_wrappers")) != uint32(len(builtinWrappers))+1 {
		t.Fatal("protocole enregistré absent de stream_get_wrappers")
	}
	for _, name := range []string{"IMAGETYPE_PNG", "STREAM_CLIENT_CONNECT"} {
		if _, ok := Constants()[name]; !ok {
			t.Fatalf("constante %s absente", name)
		}
	}
}

// TestWrappersSubsetOfPHP vérifie que chaque enveloppe annoncée existe aussi dans PHP 8.3.
func TestWrappersSubsetOfPHP(t *testing.T) {
	want := phpJSON(t, `echo json_encode(["w" => stream_get_wrappers()]);`)
	php := map[string]bool{}
	for _, w := range want["w"].([]any) {
		php[w.(string)] = true
	}
	z := zone.New(1 << 16)
	defer z.Reset(DispatchCleanup)
	for _, w := range toAny(z, callNative(t, z, nil, "stream_get_wrappers")).([]any) {
		if !php[w.(string)] {
			t.Errorf("enveloppe %q inconnue de PHP", w)
		}
	}
}

func TestWPSocketClient(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 2 {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			_, _ = c.Write([]byte("echo:" + line + "fin"))
			_ = c.Close()
		}
	}()
	port := int64(ln.Addr().(*net.TCPAddr).Port)

	z := zone.New(1 << 18)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	en, _ := types.NewReference(z, types.NewNull())
	es, _ := types.NewReference(z, types.NewNull())
	fp := callNative(t, z, nil, "fsockopen", s("127.0.0.1"), types.NewLong(port), en, es, types.NewDouble(2))
	if fp.Type != types.TypeResource || deref(z, en).ToLong() != 0 || types.StringData(z, deref(z, es)) != "" {
		t.Fatalf("fsockopen = %v errno=%v", fp, deref(z, en))
	}
	callNative(t, z, nil, "fwrite", fp, s("ping\n"))
	if got := types.StringData(z, callNative(t, z, nil, "fgets", fp)); got != "echo:ping\n" {
		t.Fatalf("fgets = %q", got)
	}
	if got := types.StringData(z, callNative(t, z, nil, "stream_get_contents", fp)); got != "fin" {
		t.Fatalf("stream_get_contents = %q", got)
	}
	if v := callNative(t, z, nil, "feof", fp); v.Type != types.TypeTrue {
		t.Fatalf("feof = %v", v)
	}
	meta := toAny(z, callNative(t, z, nil, "stream_get_meta_data", fp)).(map[string]any)
	if meta["stream_type"] != "tcp_socket/ssl" || meta["mode"] != "r+" || meta["seekable"] != false || meta["uri"] != "127.0.0.1:"+strconv.FormatInt(port, 10) {
		t.Fatalf("stream_get_meta_data = %v", meta)
	}
	if v := callNative(t, z, nil, "stream_set_timeout", fp, types.NewLong(1)); v.Type != types.TypeTrue {
		t.Fatalf("stream_set_timeout(socket) = %v", v)
	}
	if v := callNative(t, z, nil, "flock", fp, types.NewLong(LOCK_SH)); v.Type != types.TypeFalse {
		t.Fatalf("flock(socket) = %v", v)
	}
	callNative(t, z, nil, "fclose", fp)

	fp2 := callNative(t, z, nil, "stream_socket_client", s("tcp://127.0.0.1:"+strconv.FormatInt(port, 10)), en, es, types.NewDouble(2))
	if fp2.Type != types.TypeResource {
		t.Fatalf("stream_socket_client = %v", fp2)
	}
	callNative(t, z, nil, "fwrite", fp2, s("b\n"))
	if got := types.StringData(z, callNative(t, z, nil, "fread", fp2, types.NewLong(100))); got == "" {
		t.Fatal("fread sur socket vide")
	}
	meta2 := toAny(z, callNative(t, z, nil, "stream_get_meta_data", fp2)).(map[string]any)
	if meta2["uri"] != "tcp://127.0.0.1:"+strconv.FormatInt(port, 10) {
		t.Fatalf("uri = %v", meta2["uri"])
	}
	<-done
}

// TestPHPDifferentialSocketRefused compare errno et message pour un port local fermé.
func TestPHPDifferentialSocketRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	_ = ln.Close()
	want := phpJSON(t, `
$p = (int)$argv[1];
$a = @fsockopen("127.0.0.1", $p, $en, $es, 1); $o["fsock"] = [$a, $en, $es];
$a = @stream_socket_client("tcp://127.0.0.1:".$p, $en, $es, 1); $o["client"] = [$a, $en, $es];
$a = @fsockopen("", 80, $en, $es, 1); $o["empty"] = [$a];
$a = @fsockopen("nonexistent.invalid", 80, $en, $es, 1); $o["dns"] = [$a, $en, $es];
echo json_encode($o);
`, port)
	z := zone.New(1 << 18)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	en, _ := types.NewReference(z, types.NewNull())
	es, _ := types.NewReference(z, types.NewNull())
	p, _ := strconv.ParseInt(port, 10, 64)
	got := map[string]any{}
	r := callNative(t, z, nil, "fsockopen", s("127.0.0.1"), types.NewLong(p), en, es, types.NewDouble(1))
	got["fsock"] = []any{toAny(z, r), toAny(z, en), toAny(z, es)}
	r = callNative(t, z, nil, "stream_socket_client", s("tcp://127.0.0.1:"+port), en, es, types.NewDouble(1))
	got["client"] = []any{toAny(z, r), toAny(z, en), toAny(z, es)}
	r = callNative(t, z, nil, "fsockopen", s(""), types.NewLong(80), en, es, types.NewDouble(1))
	got["empty"] = []any{toAny(z, r)}
	r = callNative(t, z, nil, "fsockopen", s("nonexistent.invalid"), types.NewLong(80), en, es, types.NewDouble(1))
	got["dns"] = []any{toAny(z, r), toAny(z, en), toAny(z, es)}
	for k, w := range want {
		sameJSON(t, k, got[k], w)
	}
}

func zoneStreamsRegistered(z *zone.Zone) (byZ bool, byID int) {
	byZ = z.Slot(zone.SlotStreams) != nil
	mgrMu.Lock()
	defer mgrMu.Unlock()
	for _, zs := range byZoneID {
		if zs.zone == z {
			byID++
		}
	}
	return byZ, byID
}

// z.Reset retire l'état de flux même lorsqu'aucune ressource n'est ouverte : chdir, umask et
// stream_wrapper_register seuls, ou un flux déjà fermé par fclose.
func TestZoneStreamsReleasedOnResetWithoutOpenResources(t *testing.T) {
	dir := t.TempDir()
	procWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"chdir", "wrapper", "umask", "fclose"} {
		z := zone.New(1 << 16)
		switch scenario {
		case "chdir":
			callNative(t, z, nil, "chdir", mustStr(t, z, dir))
		case "wrapper":
			callNative(t, z, nil, "stream_wrapper_register", mustStr(t, z, "maproto"), mustStr(t, z, "C"))
		case "umask":
			callNative(t, z, nil, "umask", types.NewLong(0o077))
		case "fclose":
			h := callNative(t, z, nil, "fopen", mustStr(t, z, filepath.Join(dir, "f.txt")), mustStr(t, z, "w"))
			callNative(t, z, nil, "fclose", h)
		}
		if byZ, _ := zoneStreamsRegistered(z); !byZ {
			t.Fatalf("%s : état de flux absent avant Reset", scenario)
		}
		z.Reset(DispatchCleanup)
		if byZ, byID := zoneStreamsRegistered(z); byZ || byID != 0 {
			t.Fatalf("%s : état de flux retenu après z.Reset (parZone=%v, parID=%d)", scenario, byZ, byID)
		}
		if got := types.StringData(z, callNative(t, z, nil, "getcwd")); got != procWD {
			t.Fatalf("%s : getcwd après z.Reset = %q, attendu %q", scenario, got, procWD)
		}
		if n := types.ArrayLen(z, callNative(t, z, nil, "stream_get_wrappers")); n != uint32(len(builtinWrappers)) {
			t.Fatalf("%s : %d enveloppes après z.Reset", scenario, n)
		}
		if m := callNative(t, z, nil, "umask").ToLong(); m != 0o022 {
			t.Fatalf("%s : umask après z.Reset = %o", scenario, m)
		}
		z.Reset(DispatchCleanup)
	}
}

// stream_wrapper_register copie le protocole hors de l'arène.
func TestStreamWrapperRegisterCopiesProtocol(t *testing.T) {
	z := zone.New(1 << 16)
	defer z.Reset(DispatchCleanup)
	p := mustStr(t, z, "copieproto")
	callNative(t, z, nil, "stream_wrapper_register", p, mustStr(t, z, "C"))
	b := types.StringBytes(z, p)
	for i := range b {
		b[i] = 'X'
	}
	w := callNative(t, z, nil, "stream_get_wrappers")
	last := types.ArrayLen(z, w) - 1
	v, ok, err := types.ArrayGet(z, w, types.NewLong(int64(last)), nil)
	if err != nil || !ok || types.StringData(z, v) != "copieproto" {
		t.Fatalf("enveloppe aliasée sur l'arène : %q", types.StringData(z, v))
	}
}

func processUmask() int {
	old := syscall.Umask(0)
	syscall.Umask(old)
	return old
}

// umask est propre à chaque Zone : le masque du processus reste intact et les créations de fichiers
// et de répertoires suivent le masque de la Zone appelante.
func TestUmaskIsolatedPerZone(t *testing.T) {
	proc := processUmask()
	dir := t.TempDir()
	z1, z2 := zone.New(1<<16), zone.New(1<<16)
	defer z1.Reset(DispatchCleanup)
	defer z2.Reset(DispatchCleanup)
	if prev := callNative(t, z1, nil, "umask", types.NewLong(0o077)).ToLong(); prev != 0o022 {
		t.Fatalf("umask(077) rend %o, attendu %o", prev, 0o022)
	}
	callNative(t, z2, nil, "umask", types.NewLong(0))
	if got := processUmask(); got != proc {
		t.Fatalf("masque du processus modifié : %o, attendu %o", got, proc)
	}
	if m := callNative(t, z1, nil, "umask").ToLong(); m != 0o077 {
		t.Fatalf("umask() de la Zone 1 = %o", m)
	}
	if m := callNative(t, z2, nil, "umask").ToLong(); m != 0 {
		t.Fatalf("umask() de la Zone 2 = %o", m)
	}
	mode := func(p string) os.FileMode {
		t.Helper()
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode().Perm()
	}
	cases := []struct {
		z    *zone.Zone
		name string
		do   func(z *zone.Zone, p string)
		want os.FileMode
	}{
		{z1, "d1", func(z *zone.Zone, p string) { callNative(t, z, nil, "mkdir", mustStr(t, z, p)) }, 0o700},
		{z2, "d2", func(z *zone.Zone, p string) { callNative(t, z, nil, "mkdir", mustStr(t, z, p)) }, 0o777},
		{z1, "r1/a/b", func(z *zone.Zone, p string) {
			callNative(t, z, nil, "mkdir", mustStr(t, z, p), types.NewLong(0o755), types.NewBool(true))
		}, 0o700},
		{z1, "f1", func(z *zone.Zone, p string) {
			h := callNative(t, z, nil, "fopen", mustStr(t, z, p), mustStr(t, z, "w"))
			callNative(t, z, nil, "fclose", h)
		}, 0o600},
		{z2, "f2", func(z *zone.Zone, p string) {
			callNative(t, z, nil, "file_put_contents", mustStr(t, z, p), mustStr(t, z, "x"))
		}, 0o666},
		{z1, "t1", func(z *zone.Zone, p string) { callNative(t, z, nil, "touch", mustStr(t, z, p)) }, 0o600},
	}
	for _, c := range cases {
		p := filepath.Join(dir, c.name)
		c.do(c.z, p)
		if got := mode(p); got != c.want {
			t.Errorf("%s : droits %o, attendu %o", c.name, got, c.want)
		}
	}
	if got := mode(filepath.Join(dir, "r1")); got != 0o700 {
		t.Errorf("ascendant r1 créé par mkdir récursif : droits %o, attendu 0700", got)
	}
	z1.Reset(DispatchCleanup)
	if m := callNative(t, z1, nil, "umask").ToLong(); m != 0o022 {
		t.Fatalf("umask après z.Reset = %o", m)
	}
}

// TestCreateModeNeverExceedsZoneMask relance le binaire de test sous strace et lit le mode que chaque création
// transmet au noyau : avec le masque de Zone 077, fopen, file_put_contents, touch et mkdir créent directement
// en 0600 ou 0700, avec O_EXCL pour les fichiers, de sorte qu'aucun instant ne publie des droits plus larges.
func TestCreateModeNeverExceedsZoneMask(t *testing.T) {
	if os.Getenv("PHP127_STREAMS_CREATE_HELPER") != "" {
		dir := os.Getenv("PHP127_STREAMS_CREATE_HELPER")
		z := zone.New(1 << 16)
		defer z.Reset(DispatchCleanup)
		callNative(t, z, nil, "umask", types.NewLong(0o077))
		h := callNative(t, z, nil, "fopen", mustStr(t, z, filepath.Join(dir, "f1")), mustStr(t, z, "w"))
		callNative(t, z, nil, "fclose", h)
		callNative(t, z, nil, "file_put_contents", mustStr(t, z, filepath.Join(dir, "f2")), mustStr(t, z, "x"))
		callNative(t, z, nil, "touch", mustStr(t, z, filepath.Join(dir, "f3")))
		callNative(t, z, nil, "mkdir", mustStr(t, z, filepath.Join(dir, "d1")))
		callNative(t, z, nil, "mkdir", mustStr(t, z, filepath.Join(dir, "r1", "a")), types.NewLong(0o755), types.NewBool(true))
		return
	}
	strace, err := exec.LookPath("strace")
	if err != nil {
		t.Fatalf("strace absent : l'épreuve du mode de création exige strace : %v", err)
	}
	dir := t.TempDir()
	trace := filepath.Join(t.TempDir(), "trace.txt")
	cmd := exec.CommandContext(t.Context(), strace, "-f", "-e", "trace=openat,mkdirat,mkdir", "-o", trace,
		os.Args[0], "-test.run=^TestCreateModeNeverExceedsZoneMask$", "-test.count=1")
	cmd.Env = append(os.Environ(), "PHP127_STREAMS_CREATE_HELPER="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("processus tracé en échec : %v\n%s", err, out)
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"f1": "0600", "f2": "0600", "f3": "0600", "d1": "0700", "r1": "0700", "r1/a": "0700"}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		for name, mode := range want {
			if !strings.Contains(line, `"`+filepath.Join(dir, name)+`"`) {
				continue
			}
			isFile := strings.Contains(line, "openat(")
			if isFile && !strings.Contains(line, "O_CREAT") {
				continue
			}
			seen[name] = true
			if !strings.Contains(line, ", "+mode+")") {
				t.Errorf("création de %s avec un autre mode que %s : %s", name, mode, line)
			}
			if isFile && !strings.Contains(line, "O_EXCL") {
				t.Errorf("création de %s sans O_EXCL : %s", name, line)
			}
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("aucune création tracée pour %s :\n%s", name, data)
		}
	}
}

// TestOpenExistingFileKeepsPermissions vérifie qu'une ouverture en écriture d'un fichier créé par une autre requête ne modifie pas ses droits.
func TestOpenExistingFileKeepsPermissions(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "autre")
	if err := os.WriteFile(p, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	z := zone.New(1 << 16)
	defer z.Reset(DispatchCleanup)
	callNative(t, z, nil, "umask", types.NewLong(0o077))
	for _, mode := range []string{"w", "a", "c"} {
		h := callNative(t, z, nil, "fopen", mustStr(t, z, p), mustStr(t, z, mode))
		callNative(t, z, nil, "fclose", h)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o644 {
			t.Fatalf("fopen(%q) a modifié les droits du fichier existant : %o", mode, fi.Mode().Perm())
		}
	}
	h := callNative(t, z, nil, "fopen", mustStr(t, z, p), mustStr(t, z, "x"))
	if h.Type != types.TypeFalse {
		t.Fatalf("fopen(x) sur un fichier existant doit échouer, obtenu type %v", h.Type)
	}
}

// TestStreamHandlesCopyZoneStrings écrase dans l'arène les octets du nom de fichier, du mode et du répertoire
// après fopen, data:, php://memory et opendir : stream_get_meta_data et readdir relisent les valeurs d'origine.
func TestStreamHandlesCopyZoneStrings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garde.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	z := zone.New(1 << 16)
	defer z.Reset(DispatchCleanup)
	// Une chaîne d'au plus un octet est un littéral partagé : seules les chaînes plus longues, propres à la Zone, s'écrasent.
	scribble := func(v types.Value) {
		if types.Strlen(v) <= 1 {
			t.Fatalf("écrasement d'un littéral partagé %q", types.StringData(z, v))
		}
		b := types.StringBytes(z, v)
		for i := range b {
			b[i] = 'X'
		}
	}
	meta := func(h types.Value, key string) string {
		t.Helper()
		m := callNative(t, z, nil, "stream_get_meta_data", h)
		v, ok, err := types.ArrayGet(z, m, mustStr(t, z, key), nil)
		if err != nil || !ok {
			t.Fatalf("stream_get_meta_data sans %q", key)
		}
		return types.StringData(z, v)
	}
	for _, c := range []struct{ uri, mode string }{
		{path, "rb"},
		{"data://text/plain;base64,SGVsbG8=", "rb"},
		{"php://memory", "w+b"},
	} {
		u, m := mustStr(t, z, c.uri), mustStr(t, z, c.mode)
		h := callNative(t, z, nil, "fopen", u, m)
		if h.Type == types.TypeFalse {
			t.Fatalf("fopen(%q) a échoué", c.uri)
		}
		scribble(u)
		scribble(m)
		if got := meta(h, "uri"); got != c.uri {
			t.Errorf("uri aliasée sur l'arène : %q, attendu %q", got, c.uri)
		}
		if got := meta(h, "mode"); got != c.mode && c.uri == path {
			t.Errorf("mode aliasé sur l'arène : %q, attendu %q", got, c.mode)
		}
		callNative(t, z, nil, "fclose", h)
	}
	d := mustStr(t, z, dir)
	dh := callNative(t, z, nil, "opendir", d)
	scribble(d)
	callNative(t, z, nil, "rewinddir", dh)
	seen := false
	for {
		e := callNative(t, z, nil, "readdir", dh)
		if e.Type == types.TypeFalse {
			break
		}
		seen = seen || types.StringData(z, e) == "garde.txt"
	}
	if !seen {
		t.Fatal("readdir après écrasement du chemin : garde.txt absent")
	}
}

// TestStreamSetBlockingAffectsSocketRead vérifie que stream_set_blocking($s, false) change la lecture d'une
// socket : fread rend aussitôt une chaîne vide quand rien n'est arrivé, sans timed_out, puis les octets reçus.
func TestStreamSetBlockingAffectsSocketRead(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	z := zone.New(1 << 16)
	defer z.Reset(DispatchCleanup)
	h := callNative(t, z, nil, "stream_socket_client", mustStr(t, z, "tcp://"+ln.Addr().String()))
	if h.Type == types.TypeFalse {
		t.Fatal("stream_socket_client a échoué")
	}
	srv := <-accepted
	defer srv.Close()
	if v := callNative(t, z, nil, "stream_set_blocking", h, types.NewBool(false)); v.Type != types.TypeTrue {
		t.Fatal("stream_set_blocking a échoué")
	}
	start := time.Now()
	got := callNative(t, z, nil, "fread", h, types.NewLong(16))
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("fread non bloquant a attendu %v", elapsed)
	}
	if types.StringData(z, got) != "" {
		t.Fatalf("fread sans donnée : %q", types.StringData(z, got))
	}
	meta := toAny(z, callNative(t, z, nil, "stream_get_meta_data", h)).(map[string]any)
	if meta["blocked"] != false || meta["timed_out"] != false {
		t.Fatalf("méta-données après lecture non bloquante : blocked=%v timed_out=%v", meta["blocked"], meta["timed_out"])
	}
	if _, err := srv.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	var data string
	for deadline := time.Now().Add(5 * time.Second); data == "" && time.Now().Before(deadline); {
		data = types.StringData(z, callNative(t, z, nil, "fread", h, types.NewLong(16)))
	}
	if data != "abc" {
		t.Fatalf("fread après envoi : %q", data)
	}
}

// TestStreamsZeroArenaRetentionWithPoison libère les arguments dès l'ouverture, dans une Zone dont Free remplit
// les blocs libérés d'octets 0xAA : l'uri, le mode et les entrées de répertoire doivent rester intacts.
func TestStreamsZeroArenaRetentionWithPoison(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "poison_garde.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	z := zone.New(1 << 16)
	z.SetPoisonOnFree(true)
	defer z.Reset(DispatchCleanup)
	// release libère la valeur et vérifie que l'ancienne vue sur l'arène lit bien le poison.
	release := func(v types.Value) {
		t.Helper()
		view := types.StringBytes(z, v)
		types.ReleaseValue(z, &v)
		for i, b := range view {
			if b != 0xAA {
				t.Fatalf("octet %d de la chaîne libérée = %#x : le poison n'a pas été posé", i, b)
			}
		}
	}
	meta := func(h types.Value, key string) string {
		t.Helper()
		m := callNative(t, z, nil, "stream_get_meta_data", h)
		v, ok, err := types.ArrayGet(z, m, mustStr(t, z, key), nil)
		if err != nil || !ok {
			t.Fatalf("stream_get_meta_data sans %q", key)
		}
		return types.StringData(z, v)
	}
	for _, c := range []struct{ uri, mode string }{
		{path, "rb"},
		{"data://text/plain;base64,SGVsbG8=", "rb"},
		{"php://memory", "w+b"},
	} {
		u, m := mustStr(t, z, c.uri), mustStr(t, z, c.mode)
		h := callNative(t, z, nil, "fopen", u, m)
		if h.Type == types.TypeFalse {
			t.Fatalf("fopen(%q) a échoué", c.uri)
		}
		release(m)
		release(u)
		if got := meta(h, "uri"); got != c.uri || strings.ContainsRune(got, 0xAA) {
			t.Errorf("uri après libération : %q, attendu %q", got, c.uri)
		}
		if got := meta(h, "mode"); c.uri == path && got != c.mode {
			t.Errorf("mode après libération : %q, attendu %q", got, c.mode)
		}
		callNative(t, z, nil, "fclose", h)
	}
	d := mustStr(t, z, dir)
	dh := callNative(t, z, nil, "opendir", d)
	release(d)
	callNative(t, z, nil, "rewinddir", dh)
	seen := false
	for {
		e := callNative(t, z, nil, "readdir", dh)
		if e.Type == types.TypeFalse {
			break
		}
		name := types.StringData(z, e)
		if strings.IndexByte(name, 0xAA) >= 0 {
			t.Fatalf("entrée de répertoire empoisonnée : %q", name)
		}
		seen = seen || name == "poison_garde.txt"
	}
	if !seen {
		t.Fatal("readdir après libération du chemin : poison_garde.txt absent")
	}
}

// TestCastStringCopiesOutOfArena vérifie que castString rend une copie Go : la libération empoisonnée
// de la chaîne source ne change pas le résultat.
func TestCastStringCopiesOutOfArena(t *testing.T) {
	z := zone.New(1 << 12)
	z.SetPoisonOnFree(true)
	defer z.Reset(DispatchCleanup)
	v := mustStr(t, z, "chemin/vers/fichier")
	got, err := castString(z, v)
	if err != nil {
		t.Fatal(err)
	}
	types.ReleaseValue(z, &v)
	if got != "chemin/vers/fichier" {
		t.Fatalf("castString aliasé sur l'arène : %q", got)
	}
}

// TestGetZoneStreamsUsesZoneSlot vérifie que l'état de flux vit dans le slot de la Zone, qu'il est retiré
// par CloseAll et par Reset, et que deux Zones ont deux états distincts.
func TestGetZoneStreamsUsesZoneSlot(t *testing.T) {
	z1, z2 := zone.New(1<<12), zone.New(1<<12)
	zs1 := getZoneStreams(z1)
	if z1.Slot(zone.SlotStreams) != zs1 || getZoneStreams(z1) != zs1 {
		t.Fatal("état de flux absent du slot de la Zone")
	}
	if getZoneStreams(z2) == zs1 {
		t.Fatal("deux Zones partagent un état de flux")
	}
	CloseAll(z1)
	if z1.Slot(zone.SlotStreams) != nil {
		t.Fatal("CloseAll laisse l'état de flux dans le slot")
	}
	z1.Reset(DispatchCleanup)
	z2.Reset(DispatchCleanup)
	if z2.Slot(zone.SlotStreams) != nil {
		t.Fatal("Reset laisse l'état de flux dans le slot")
	}
	if byZ, byID := zoneStreamsRegistered(z2); byZ || byID != 0 {
		t.Fatalf("état retenu après Reset (slot=%v, parID=%d)", byZ, byID)
	}
}

// TestPfsockopen ouvre une connexion TCP réelle par pfsockopen, puis compare à PHP le refus de connexion.
func TestPfsockopen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		if err != nil {
			return
		}
		line, _ := bufio.NewReader(c).ReadString('\n')
		_, _ = c.Write([]byte("pecho:" + line))
		_ = c.Close()
	}()
	port := int64(ln.Addr().(*net.TCPAddr).Port)
	z := zone.New(1 << 18)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	en, _ := types.NewReference(z, types.NewNull())
	es, _ := types.NewReference(z, types.NewNull())
	fp := callNative(t, z, nil, "pfsockopen", s("127.0.0.1"), types.NewLong(port), en, es, types.NewDouble(2))
	if fp.Type != types.TypeResource || deref(z, en).ToLong() != 0 || types.StringData(z, deref(z, es)) != "" {
		t.Fatalf("pfsockopen = %v errno=%v", fp, deref(z, en))
	}
	callNative(t, z, nil, "fwrite", fp, s("ping\n"))
	if got := types.StringData(z, callNative(t, z, nil, "fgets", fp)); got != "pecho:ping\n" {
		t.Fatalf("fgets = %q", got)
	}
	callNative(t, z, nil, "fclose", fp)
	<-done
	_ = ln.Close()

	refused := strconv.FormatInt(port, 10)
	want := phpJSON(t, `$a = @pfsockopen("127.0.0.1", (int)$argv[1], $en, $es, 1); echo json_encode(["p" => [$a, $en, $es]]);`, refused)
	r := callNative(t, z, nil, "pfsockopen", s("127.0.0.1"), types.NewLong(port), en, es, types.NewDouble(1))
	sameJSON(t, "p", []any{toAny(z, r), toAny(z, en), toAny(z, es)}, want["p"])
}

// TestStreamContextParams compare stream_context_set_params et stream_context_get_params à PHP 8.3.
func TestStreamContextParams(t *testing.T) {
	want := phpJSON(t, `
$c = stream_context_create(["ssl" => ["verify_peer" => false]]);
$o["empty"] = stream_context_get_params(stream_context_create());
$o["set"] = stream_context_set_params($c, ["notification" => "mon_rappel", "autre" => 1, "options" => ["http" => ["method" => "POST"]]]);
$o["get"] = stream_context_get_params($c);
$o["opts"] = stream_context_get_options($c);
stream_context_set_params($c, ["notification" => null]);
$o["get_null"] = stream_context_get_params($c);
try { stream_context_set_params($c, ["options" => 5]); } catch (Throwable $e) { $o["bad_opts"] = $e->getMessage(); }
try { stream_context_set_params($c, ["options" => ["http" => 5]]); } catch (Throwable $e) { $o["bad_form"] = $e->getMessage(); }
try { stream_context_set_params(5, []); } catch (Throwable $e) { $o["bad_ctx"] = $e->getMessage(); }
echo json_encode($o);
`)
	z := zone.New(1 << 18)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	arr := func(kv ...any) types.Value {
		a, _ := types.NewArray(z)
		for i := 0; i < len(kv); i += 2 {
			var val types.Value
			switch x := kv[i+1].(type) {
			case string:
				val = s(x)
			case int:
				val = types.NewLong(int64(x))
			case bool:
				val = types.NewBool(x)
			case types.Value:
				val = x
			case nil:
				val = types.NewNull()
			}
			_ = types.ArraySet(z, &a, s(kv[i].(string)), val, warnDrop)
		}
		return a
	}
	errText := func(name string, args ...types.Value) string {
		t.Helper()
		_, err := callErr(t, z, name, args...)
		if err == nil {
			t.Fatalf("%s sans erreur", name)
		}
		return err.Error()
	}
	got := map[string]any{}
	c := callNative(t, z, nil, "stream_context_create", arr("ssl", arr("verify_peer", false)))
	got["empty"] = toAny(z, callNative(t, z, nil, "stream_context_get_params", callNative(t, z, nil, "stream_context_create")))
	// ArraySet retient ce qu'il stocke : le seul propriétaire restant du rappel est le tableau. Sa libération
	// dans une Zone empoisonnée efface la chaîne ; get_params doit rendre la copie.
	z.SetPoisonOnFree(true)
	rappel := s("mon_rappel")
	params := arr("notification", rappel, "autre", 1, "options", arr("http", arr("method", "POST")))
	view := types.StringBytes(z, rappel)
	types.ReleaseValue(z, &rappel)
	got["set"] = toAny(z, callNative(t, z, nil, "stream_context_set_params", c, params))
	types.ReleaseValue(z, &params)
	if view[0] != 0xAA {
		t.Fatalf("chaîne du rappel non libérée : %q", view)
	}
	z.SetPoisonOnFree(false)
	got["get"] = toAny(z, callNative(t, z, nil, "stream_context_get_params", c))
	got["opts"] = toAny(z, callNative(t, z, nil, "stream_context_get_options", c))
	callNative(t, z, nil, "stream_context_set_params", c, arr("notification", nil))
	got["get_null"] = toAny(z, callNative(t, z, nil, "stream_context_get_params", c))
	got["bad_opts"] = errText("stream_context_set_params", c, arr("options", 5))
	got["bad_form"] = errText("stream_context_set_params", c, arr("options", arr("http", 5)))
	got["bad_ctx"] = errText("stream_context_set_params", types.NewLong(5), arr())
	for k, w := range want {
		sameJSON(t, k, got[k], w)
	}
	if len(got) != len(want) {
		t.Fatalf("%d clés comparées, PHP en rend %d", len(got), len(want))
	}
}

// TestStreamWrapperRegisterValidation vérifie le refus d'un protocole invalide, natif ou déjà enregistré,
// l'avertissement écrit, et l'absence de http et https dans stream_get_wrappers.
func TestStreamWrapperRegisterValidation(t *testing.T) {
	z := zone.New(1 << 16)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	for _, c := range []struct {
		proto string
		ok    bool
		warn  string
	}{
		{"a b", false, "Invalid protocol scheme specified. Unable to register wrapper class C to a b://"},
		{"", false, "Invalid protocol scheme specified. Unable to register wrapper class C to ://"},
		{"s3/x", false, "Invalid protocol scheme specified"},
		{"ftp", false, "Protocol ftp:// is already defined."},
		{"zlib", false, "Protocol zlib:// is already defined."},
		{"HTTPS", false, "Protocol HTTPS:// is already defined."},
		{"glob", false, "Protocol glob:// is already defined."},
		{"x+y.z-1", true, ""},
		{"X+Y.Z-1", false, "Protocol X+Y.Z-1:// is already defined."},
	} {
		var out bytes.Buffer
		v := callNative(t, z, &out, "stream_wrapper_register", s(c.proto), s("C"))
		if (v.Type == types.TypeTrue) != c.ok {
			t.Errorf("stream_wrapper_register(%q) = %v, attendu %v", c.proto, v.Type == types.TypeTrue, c.ok)
		}
		if c.warn == "" && out.Len() != 0 || c.warn != "" && !strings.Contains(out.String(), "Warning: stream_wrapper_register(): "+c.warn) {
			t.Errorf("stream_wrapper_register(%q) : avertissement %q", c.proto, out.String())
		}
	}
	names := map[string]bool{}
	for _, w := range toAny(z, callNative(t, z, nil, "stream_get_wrappers")).([]any) {
		names[w.(string)] = true
	}
	for _, absent := range []string{"http", "https", "a b", ""} {
		if names[absent] {
			t.Errorf("stream_get_wrappers annonce %q", absent)
		}
	}
	for _, present := range []string{"php", "file", "data", "x+y.z-1"} {
		if !names[present] {
			t.Errorf("stream_get_wrappers sans %q", present)
		}
	}
}
