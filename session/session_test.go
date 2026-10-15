package session_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"code.hazyhaar.fr/devhoros/php127/session"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func mustStr(t *testing.T, z *zone.Zone, s string) types.Value {
	t.Helper()
	v, err := types.NewString(z, s, 0)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

type memSessionHandler struct {
	store     map[string]string
	openCalls int
	gcCalls   int
}

func (m *memSessionHandler) Open(_, _ string) bool {
	m.openCalls++
	if m.store == nil {
		m.store = make(map[string]string)
	}
	return true
}

func (m *memSessionHandler) Close() bool { return true }

func (m *memSessionHandler) Read(id string) (string, bool) {
	return m.store[id], true
}

func (m *memSessionHandler) Write(id, data string) bool {
	m.store[id] = data
	return true
}

func (m *memSessionHandler) Destroy(id string) bool {
	delete(m.store, id)
	return true
}

func (m *memSessionHandler) GC(_ int64) (int64, bool) {
	m.gcCalls++
	return 2, true
}

func TestSessionLookupNoAllocAndLifecycle(t *testing.T) {
	names := []string{
		"session_start",
		"session_write_close",
		"session_commit",
		"session_abort",
		"session_reset",
		"session_destroy",
		"session_unset",
		"session_status",
		"session_id",
		"session_create_id",
		"session_regenerate_id",
		"session_name",
		"session_save_path",
		"session_encode",
		"session_decode",
		"session_get_cookie_params",
		"session_set_cookie_params",
		"session_cache_limiter",
		"session_cache_expire",
		"session_gc",
	}
	for _, n := range names {
		fn, ok := session.Lookup(n)
		if !ok || fn == nil {
			t.Fatalf("Lookup(%q) failed", n)
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		if _, ok := session.Lookup("SESSION_GET_COOKIE_PARAMS"); !ok {
			t.Fatal("expected match")
		}
	})
	if allocs != 0 {
		t.Fatalf("Lookup allocated %v times, want 0", allocs)
	}

	tmpDir := t.TempDir()

	// Request 1: start session, populate $_SESSION, and let z.Reset auto-flush via CleanupDispatcher!
	z1 := zone.New(zone.DefaultCapacity)
	call1 := func(name string, args ...types.Value) types.Value {
		t.Helper()
		fn, _ := session.Lookup(name)
		v, err := fn(z1, nil, args)
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
		return v
	}

	sPath := mustStr(t, z1, tmpDir)
	oldPath := call1("session_save_path", sPath)
	types.ReleaseValue(z1, &oldPath)
	types.ReleaseValue(z1, &sPath)

	sID := mustStr(t, z1, "req1sessid")
	oldID := call1("session_id", sID)
	types.ReleaseValue(z1, &oldID)
	types.ReleaseValue(z1, &sID)

	if st := call1("session_status"); st.ToLong() != session.PHP_SESSION_NONE {
		t.Fatalf("initial status = %d, want %d", st.ToLong(), session.PHP_SESSION_NONE)
	}

	started := call1("session_start")
	if !started.ToBool() {
		t.Fatal("session_start failed")
	}
	if st := call1("session_status"); st.ToLong() != session.PHP_SESSION_ACTIVE {
		t.Fatalf("active status = %d, want %d", st.ToLong(), session.PHP_SESSION_ACTIVE)
	}

	sAlice := mustStr(t, z1, "alice")
	if err := session.SetVar(z1, "user", sAlice); err != nil {
		t.Fatal(err)
	}
	types.ReleaseValue(z1, &sAlice)
	if err := session.SetVar(z1, "visits", types.NewLong(5)); err != nil {
		t.Fatal(err)
	}

	// Do NOT call session_write_close manually: verify that z1.Reset persists and closes the session!
	z1.Reset(session.CleanupDispatcher(z1))

	if _, err := os.Stat(filepath.Join(tmpDir, "sess_req1sessid")); err != nil {
		t.Fatalf("expected session file persisted on Zone.Reset: %v", err)
	}

	// Request 2: reload the same session ID, test reset/abort/unset/destroy/gc.
	z2 := zone.New(zone.DefaultCapacity)
	defer z2.Reset(session.CleanupDispatcher(z2))
	call2 := func(name string, args ...types.Value) types.Value {
		t.Helper()
		fn, _ := session.Lookup(name)
		v, err := fn(z2, nil, args)
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
		return v
	}

	sPath2 := mustStr(t, z2, tmpDir)
	r := call2("session_save_path", sPath2)
	types.ReleaseValue(z2, &r)
	types.ReleaseValue(z2, &sPath2)

	sID2 := mustStr(t, z2, "req1sessid")
	r = call2("session_id", sID2)
	types.ReleaseValue(z2, &r)
	types.ReleaseValue(z2, &sID2)

	if ok := call2("session_start"); !ok.ToBool() {
		t.Fatal("session_start on z2 failed")
	}
	uVal, ok, err := session.GetVar(z2, "user")
	if err != nil || !ok || types.StringData(z2, uVal) != "alice" {
		t.Fatalf("reloaded $_SESSION[user] = (%v, %v, %v)", uVal, ok, err)
	}

	// Modify then reset: must restore "alice".
	sBob := mustStr(t, z2, "bob")
	_ = session.SetVar(z2, "user", sBob)
	types.ReleaseValue(z2, &sBob)
	if ok := call2("session_reset"); !ok.ToBool() {
		t.Fatal("session_reset failed")
	}
	uVal, ok, _ = session.GetVar(z2, "user")
	if !ok || types.StringData(z2, uVal) != "alice" {
		t.Fatalf("after session_reset, user = %q, want alice", types.StringData(z2, uVal))
	}

	// Create an expired session file and run session_gc.
	staleFile := filepath.Join(tmpDir, "sess_expired999")
	if err := os.WriteFile(staleFile, []byte("x|i:1;"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(staleFile, past, past)
	gcCount := call2("session_gc")
	if gcCount.ToLong() != 1 {
		t.Fatalf("session_gc deleted %d files, want 1", gcCount.ToLong())
	}

	// Custom SessionHandlerInterface test on z3.
	z3 := zone.New(zone.DefaultCapacity)
	defer z3.Reset(session.CleanupDispatcher(z3))
	memH := &memSessionHandler{}
	if !session.SetSaveHandler(z3, memH) {
		t.Fatal("SetSaveHandler failed")
	}
	_ = session.SetSerializeHandler(z3, "php_serialize")
	if ok, err := session.Start(z3, types.NewNull()); err != nil || !ok {
		t.Fatalf("Start with custom handler failed: %v", err)
	}
	_ = session.SetVar(z3, "k", types.NewLong(77))
	if !session.WriteClose(z3) {
		t.Fatal("WriteClose failed")
	}
	if memH.openCalls != 1 || len(memH.store) != 1 {
		t.Fatalf("custom handler not invoked properly: %+v", memH)
	}
}

func TestSessionDifferentialVsPHP(t *testing.T) {
	tmpDir := t.TempDir()
	phpScript := `
$dir = $argv[1];
session_save_path($dir);
session_id("diffsess01");
session_start(["use_cookies" => 0, "cache_limiter" => ""]);
$_SESSION["user"] = "claire";
$_SESSION["role"] = "admin";
$_SESSION["nums"] = [10, 20, "k" => "v"];
$enc_php = session_encode();
session_write_close();

ini_set("session.serialize_handler", "php_serialize");
session_id("diffsess02");
session_start(["use_cookies" => 0, "cache_limiter" => ""]);
$_SESSION["user"] = "claire";
$_SESSION["role"] = "admin";
$_SESSION["nums"] = [10, 20, "k" => "v"];
$enc_ser = session_encode();
session_write_close();

echo json_encode([
    "enc_php" => $enc_php,
    "enc_ser" => $enc_ser,
    "cookie"  => session_get_cookie_params(),
]);
`
	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", phpScript, tmpDir)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("/usr/bin/php failed: %v", err)
	}
	var want map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &want); err != nil {
		t.Fatalf("unmarshal php json: %v", err)
	}

	z := zone.New(zone.DefaultCapacity)
	defer z.Reset(session.CleanupDispatcher(z))

	call := func(name string, args ...types.Value) types.Value {
		t.Helper()
		fn, _ := session.Lookup(name)
		v, err := fn(z, nil, args)
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
		return v
	}

	// Read the session file written by /usr/bin/php ("diffsess01" in "php" format) and re-encode it!
	sPath := mustStr(t, z, tmpDir)
	r := call("session_save_path", sPath)
	types.ReleaseValue(z, &r)
	types.ReleaseValue(z, &sPath)

	sID1 := mustStr(t, z, "diffsess01")
	r = call("session_id", sID1)
	types.ReleaseValue(z, &r)
	types.ReleaseValue(z, &sID1)

	if ok := call("session_start"); !ok.ToBool() {
		t.Fatal("session_start(diffsess01) failed")
	}
	encPHPVal := call("session_encode")
	defer types.ReleaseValue(z, &encPHPVal)
	if got := types.StringData(z, encPHPVal); got != want["enc_php"].(string) {
		t.Fatalf("enc_php = %q, want %q", got, want["enc_php"])
	}
	call("session_write_close")

	// Read the session file written by /usr/bin/php ("diffsess02" in "php_serialize" format) and re-encode it!
	_ = session.SetSerializeHandler(z, "php_serialize")
	sID2 := mustStr(t, z, "diffsess02")
	r = call("session_id", sID2)
	types.ReleaseValue(z, &r)
	types.ReleaseValue(z, &sID2)

	if ok := call("session_start"); !ok.ToBool() {
		t.Fatal("session_start(diffsess02) failed")
	}
	encSerVal := call("session_encode")
	defer types.ReleaseValue(z, &encSerVal)
	if got := types.StringData(z, encSerVal); got != want["enc_ser"].(string) {
		t.Fatalf("enc_ser = %q, want %q", got, want["enc_ser"])
	}
	call("session_write_close")
}
