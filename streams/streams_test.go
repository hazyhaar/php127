package streams

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func callNative(t *testing.T, z *zone.Zone, out *bytes.Buffer, name string, args ...types.Value) types.Value {
	t.Helper()
	fn, ok := Lookup(name)
	if !ok {
		t.Fatalf("Lookup(%q) failed", name)
	}
	v, err := fn(z, out, args)
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
