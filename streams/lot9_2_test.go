package streams

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Lot 9.2 : coutures S8-M5 côté streams, options de contexte, stream_select, chiffrement et flux gzip.

// phpArr construit un tableau PHP à partir de paires clé, valeur (chaîne, entier, booléen, nil ou Value).
func phpArr(t *testing.T, z *zone.Zone, kv ...any) types.Value {
	t.Helper()
	a, err := types.NewArray(z)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(kv); i += 2 {
		var val types.Value
		switch x := kv[i+1].(type) {
		case string:
			val = mustStr(t, z, x)
		case int:
			val = types.NewLong(int64(x))
		case bool:
			val = types.NewBool(x)
		case nil:
			val = types.NewNull()
		case types.Value:
			val = x
		}
		var key types.Value
		switch k := kv[i].(type) {
		case string:
			key = mustStr(t, z, k)
		case int:
			key = types.NewLong(int64(k))
		}
		if err := types.ArraySet(z, &a, key, val, warnDrop); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func newRef(t *testing.T, z *zone.Zone, v types.Value) types.Value {
	t.Helper()
	r, err := types.NewReference(z, v)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// memWrapper est une instance d'enveloppe utilisateur servie en mémoire.
type memWrapper struct {
	r      *strings.Reader
	w      bytes.Buffer
	closed bool
}

func (m *memWrapper) Read(p []byte) (int, error)  { return m.r.Read(p) }
func (m *memWrapper) Write(p []byte) (int, error) { return m.w.Write(p) }
func (m *memWrapper) Close() error                { m.closed = true; return nil }

// TestStreamWrapperRegisterStoresClassAndRejectsSilentFileFallback lève S8-M5 (streams/stream.go:1858) :
// la classe est conservée, hors de l'arène ; sans gestionnaire d'instance, toute URI du protocole est
// refusée avec un avertissement au lieu d'ouvrir le fichier local cwd/myproto:/data.txt (leurre posé) ;
// avec un gestionnaire, fopen, fread, fwrite, fclose et z.Reset passent par l'instance.
func TestStreamWrapperRegisterStoresClassAndRejectsSilentFileFallback(t *testing.T) {
	dir := t.TempDir()
	decoyDir := filepath.Join(dir, "myproto:")
	if err := os.MkdirAll(decoyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decoyDir, "data.txt"), []byte("LOCAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	z := zone.New(1 << 18)
	z.SetPoisonOnFree(true)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	callNative(t, z, nil, "chdir", s(dir))

	cls := s("MyWrapper")
	if v := callNative(t, z, nil, "stream_wrapper_register", s("myproto"), cls); v.Type != types.TypeTrue {
		t.Fatalf("stream_wrapper_register = %v", v)
	}
	view := types.StringBytes(z, cls)
	types.ReleaseValue(z, &cls)
	if view[0] != 0xAA {
		t.Fatal("le nom de classe n'a pas été libéré dans l'arène empoisonnée")
	}
	if got, ok := UserWrapperClass(z, "MYPROTO"); !ok || got != "MyWrapper" {
		t.Fatalf("UserWrapperClass = %q, %v", got, ok)
	}

	wantWarn := `Failed to open stream: user wrapper class "MyWrapper" registered for myproto:// requires an instance handler`
	for _, c := range []struct {
		fn   string
		args []types.Value
	}{
		{"fopen", []types.Value{s("myproto://data.txt"), s("r")}},
		{"file_get_contents", []types.Value{s("myproto://data.txt")}},
		{"file", []types.Value{s("myproto://data.txt")}},
		{"file_put_contents", []types.Value{s("myproto://neuf.txt"), s("x")}},
	} {
		var out bytes.Buffer
		if v := callNative(t, z, &out, c.fn, c.args...); v.Type != types.TypeFalse {
			t.Errorf("%s(myproto://…) = %v : repli sur le fichier local", c.fn, toAny(z, v))
		}
		if !strings.Contains(out.String(), "Warning: "+c.fn+"(myproto://") || !strings.Contains(out.String(), wantWarn) {
			t.Errorf("%s : avertissement %q", c.fn, out.String())
		}
	}
	for _, fn := range []string{"file_exists", "is_file", "is_readable"} {
		if v := callNative(t, z, nil, fn, s("myproto://data.txt")); v.Type != types.TypeFalse {
			t.Errorf("%s(myproto://data.txt) = %v : chemin local résolu", fn, toAny(z, v))
		}
	}
	if _, err := os.Stat(filepath.Join(decoyDir, "neuf.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file_put_contents a écrit le fichier local : %v", err)
	}

	type call struct {
		class, uri, mode string
		ctx              types.Value
	}
	var calls []call
	var instances []*memWrapper
	SetWrapperOpener(z, func(_ *zone.Zone, class, uri, mode string, ctx types.Value) (WrapperStream, error) {
		calls = append(calls, call{class, uri, mode, ctx})
		m := &memWrapper{r: strings.NewReader("SERVI PAR L'INSTANCE")}
		instances = append(instances, m)
		return m, nil
	})
	ctx := callNative(t, z, nil, "stream_context_create")
	fp := callNative(t, z, nil, "fopen", s("myproto://data.txt"), s("r+"), types.NewBool(false), ctx)
	if fp.Type != types.TypeResource {
		t.Fatalf("fopen avec gestionnaire = %v", toAny(z, fp))
	}
	if len(calls) != 1 || calls[0].class != "MyWrapper" || calls[0].uri != "myproto://data.txt" || calls[0].mode != "r+" ||
		calls[0].ctx.Type != types.TypeResource || calls[0].ctx.Extra != ResourceKindContext || calls[0].ctx.DataSlot != ctx.DataSlot {
		t.Fatalf("appel du gestionnaire = %+v", calls)
	}
	if got := types.StringData(z, callNative(t, z, nil, "fread", fp, types.NewLong(5))); got != "SERVI" {
		t.Fatalf("fread = %q", got)
	}
	if n := callNative(t, z, nil, "fwrite", fp, s("écrit")).ToLong(); n != int64(len("écrit")) || instances[0].w.String() != "écrit" {
		t.Fatalf("fwrite = %d, instance %q", n, instances[0].w.String())
	}
	meta := toAny(z, callNative(t, z, nil, "stream_get_meta_data", fp)).(map[string]any)
	if meta["wrapper_type"] != "user-space" || meta["uri"] != "myproto://data.txt" {
		t.Fatalf("stream_get_meta_data = %v", meta)
	}
	if got := types.StringData(z, callNative(t, z, nil, "file_get_contents", s("myproto://data.txt"))); got != "SERVI PAR L'INSTANCE" {
		t.Fatalf("file_get_contents = %q", got)
	}
	callNative(t, z, nil, "fclose", fp)
	if !instances[0].closed || !instances[1].closed {
		t.Fatal("fclose ou file_get_contents n'a pas fermé l'instance")
	}
	callNative(t, z, nil, "fopen", s("myproto://reste.txt"), s("r"))
	z.Reset(DispatchCleanup)
	if !instances[2].closed {
		t.Fatal("z.Reset n'a pas fermé le flux d'enveloppe utilisateur")
	}
	if _, ok := UserWrapperClass(z, "myproto"); ok {
		t.Fatal("protocole utilisateur retenu après z.Reset")
	}
}

// testCert produit un certificat autosigné pour localhost et 127.0.0.1 et l'écrit en PEM dans dir.
func testCert(t *testing.T, dir string) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, path
}

// echoServer accepte des connexions et renvoie chaque ligne préfixée ; sni reçoit le nom présenté en TLS.
func echoServer(t *testing.T, ln net.Listener, prefix string, sni chan<- string) {
	t.Helper()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				if tc, ok := c.(*tls.Conn); ok {
					if tc.Handshake() != nil {
						return
					}
					if sni != nil {
						sni <- tc.ConnectionState().ServerName
					}
				}
				line, err := bufio.NewReader(c).ReadString('\n')
				if err == nil {
					_, _ = io.WriteString(c, prefix+line)
				}
			}()
		}
	}()
}

// TestStreamSocketClientAppliesFlagsAndContext lève S8-M5 (streams/stream.go:2084) : le contexte du sixième
// argument est attaché au flux et relu par stream_context_get_options, socket.bindto fixe le port local vu par
// le serveur, les options « ssl » (cafile, peer_name, verify_peer, allow_self_signed) décident de l'issue TLS,
// et des drapeaux sans STREAM_CLIENT_CONNECT ni STREAM_CLIENT_ASYNC_CONNECT, ou hors des trois bits connus, sont refusés.
func TestStreamSocketClientAppliesFlagsAndContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	remotes := make(chan net.Addr, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			remotes <- c.RemoteAddr()
			defer c.Close()
		}
	}()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	localPort := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	z := zone.New(1 << 18)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	en, es := newRef(t, z, types.NewNull()), newRef(t, z, types.NewNull())
	bindto := "127.0.0.1:" + strconv.Itoa(localPort)
	ctx := callNative(t, z, nil, "stream_context_create",
		phpArr(t, z, "socket", phpArr(t, z, "bindto", bindto), "ssl", phpArr(t, z, "verify_peer", false)))
	sock := callNative(t, z, nil, "stream_socket_client", s("tcp://"+ln.Addr().String()), en, es, types.NewDouble(2), types.NewLong(STREAM_CLIENT_CONNECT), ctx)
	if sock.Type != types.TypeResource {
		t.Fatalf("stream_socket_client = %v (%v)", toAny(z, sock), toAny(z, es))
	}
	select {
	case ra := <-remotes:
		if ra.(*net.TCPAddr).Port != localPort {
			t.Fatalf("port local %d, socket.bindto demandait %d", ra.(*net.TCPAddr).Port, localPort)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("aucune connexion acceptée")
	}
	wantOpts := map[string]any{"socket": map[string]any{"bindto": bindto}, "ssl": map[string]any{"verify_peer": false}}
	sameJSON(t, "stream_context_get_options($sock)", toAny(z, callNative(t, z, nil, "stream_context_get_options", sock)), wantOpts)
	sameJSON(t, "stream_context_get_params($sock)", toAny(z, callNative(t, z, nil, "stream_context_get_params", sock)), map[string]any{"options": wantOpts})

	var out bytes.Buffer
	if v := callNative(t, z, &out, "stream_socket_client", s("tcp://"+ln.Addr().String()), en, es, types.NewDouble(2), types.NewLong(0)); v.Type != types.TypeFalse {
		t.Fatalf("stream_socket_client sans STREAM_CLIENT_CONNECT = %v", toAny(z, v))
	}
	if !strings.Contains(out.String(), "STREAM_CLIENT_CONNECT or STREAM_CLIENT_ASYNC_CONNECT is required") || !strings.Contains(types.StringData(z, deref(z, es)), "STREAM_CLIENT_CONNECT") {
		t.Fatalf("refus sans avertissement ni errstr : %q / %q", out.String(), types.StringData(z, deref(z, es)))
	}
	if _, err := callErr(t, z, "stream_socket_client", s("tcp://"+ln.Addr().String()), en, es, types.NewDouble(2), types.NewLong(8|STREAM_CLIENT_CONNECT)); err == nil || ExceptionClass(err) != "ValueError" {
		t.Fatalf("drapeau inconnu accepté : %v", err)
	}
	all := STREAM_CLIENT_CONNECT | STREAM_CLIENT_ASYNC_CONNECT | STREAM_CLIENT_PERSISTENT
	if v := callNative(t, z, nil, "stream_socket_client", s("tcp://"+ln.Addr().String()), en, es, types.NewDouble(2), types.NewLong(int64(all))); v.Type != types.TypeResource {
		t.Fatalf("drapeaux connus refusés : %v", toAny(z, v))
	}

	dir := t.TempDir()
	cert, caPath := testCert(t, dir)
	tln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer tln.Close()
	sni := make(chan string, 8)
	echoServer(t, tln, "tls:", sni)
	tlsAddr := "tls://" + tln.Addr().String()
	for _, c := range []struct {
		name string
		ssl  []any
		ok   bool
		sni  string
	}{
		{"sans contexte : autorité inconnue", nil, false, ""},
		{"cafile et peer_name", []any{"cafile", caPath, "peer_name", "localhost"}, true, "localhost"},
		{"peer_name étranger", []any{"cafile", caPath, "peer_name", "autre.example"}, false, ""},
		{"verify_peer_name false", []any{"cafile", caPath, "peer_name", "autre.example", "verify_peer_name", false}, true, "autre.example"},
		{"verify_peer false", []any{"verify_peer", false}, true, ""},
		{"allow_self_signed", []any{"allow_self_signed", true, "peer_name", "localhost"}, true, "localhost"},
		{"SNI_enabled false", []any{"cafile", caPath, "peer_name", "localhost", "SNI_enabled", false}, true, ""},
	} {
		args := []types.Value{s(tlsAddr), en, es, types.NewDouble(2), types.NewLong(STREAM_CLIENT_CONNECT)}
		if c.ssl != nil {
			args = append(args, callNative(t, z, nil, "stream_context_create", phpArr(t, z, "ssl", phpArr(t, z, c.ssl...))))
		}
		v := callNative(t, z, nil, "stream_socket_client", args...)
		if (v.Type == types.TypeResource) != c.ok {
			t.Errorf("%s : stream_socket_client = %v (%v)", c.name, toAny(z, v), toAny(z, es))
			continue
		}
		if !c.ok {
			continue
		}
		if got := <-sni; got != c.sni {
			t.Errorf("%s : SNI %q, attendu %q", c.name, got, c.sni)
		}
		callNative(t, z, nil, "fwrite", v, s("ping\n"))
		if got := types.StringData(z, callNative(t, z, nil, "fgets", v)); got != "tls:ping\n" {
			t.Errorf("%s : fgets = %q", c.name, got)
		}
		callNative(t, z, nil, "fclose", v)
	}
}

// TestStreamSocketEnableCrypto négocie TLS sur une socket TCP ouverte en clair (STARTTLS, comme
// PHPMailer/SMTP.php:508) avec les options « ssl » du contexte attaché, puis vérifie les refus mesurés sur PHP 8.3.
func TestStreamSocketEnableCrypto(t *testing.T) {
	dir := t.TempDir()
	cert, caPath := testCert(t, dir)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	type state struct {
		version uint16
		sni     string
		err     error
	}
	states := make(chan state, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				if line, err := br.ReadString('\n'); err != nil || line != "STARTTLS\n" {
					return
				}
				_, _ = io.WriteString(c, "220 pret\n")
				tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})
				err := tc.Handshake()
				states <- state{tc.ConnectionState().Version, tc.ConnectionState().ServerName, err}
				if err != nil {
					return
				}
				line, err := bufio.NewReader(tc).ReadString('\n')
				if err == nil {
					_, _ = io.WriteString(tc, "chiffre:"+line)
				}
			}()
		}
	}()
	z := zone.New(1 << 18)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	en, es := newRef(t, z, types.NewNull()), newRef(t, z, types.NewNull())
	method := types.NewLong(121 | 33 | 17) // TLS_CLIENT | TLSv1_2_CLIENT | TLSv1_1_CLIENT, comme PHPMailer
	starttls := func(ssl ...any) types.Value {
		t.Helper()
		ctx := callNative(t, z, nil, "stream_context_create", phpArr(t, z, "ssl", phpArr(t, z, ssl...)))
		sock := callNative(t, z, nil, "stream_socket_client", s("tcp://"+ln.Addr().String()), en, es, types.NewDouble(2), types.NewLong(STREAM_CLIENT_CONNECT), ctx)
		if sock.Type != types.TypeResource {
			t.Fatalf("stream_socket_client = %v", toAny(z, sock))
		}
		callNative(t, z, nil, "fwrite", sock, s("STARTTLS\n"))
		if got := types.StringData(z, callNative(t, z, nil, "fgets", sock)); got != "220 pret\n" {
			t.Fatalf("réponse STARTTLS = %q", got)
		}
		return sock
	}

	sock := starttls("cafile", caPath, "peer_name", "localhost")
	if v := callNative(t, z, nil, "stream_socket_enable_crypto", sock, types.NewBool(false)); v.Type != types.TypeFalse {
		t.Fatalf("désactivation sur socket en clair = %v", toAny(z, v))
	}
	var out bytes.Buffer
	if v := callNative(t, z, &out, "stream_socket_enable_crypto", sock, types.NewBool(true), method); v.Type != types.TypeTrue {
		t.Fatalf("stream_socket_enable_crypto = %v (%s)", toAny(z, v), out.String())
	}
	if st := <-states; st.err != nil || st.version < tls.VersionTLS12 || st.sni != "localhost" {
		t.Fatalf("négociation serveur : %+v", st)
	}
	callNative(t, z, nil, "fwrite", sock, s("ping\n"))
	if got := types.StringData(z, callNative(t, z, nil, "fgets", sock)); got != "chiffre:ping\n" {
		t.Fatalf("échange chiffré : %q", got)
	}
	out.Reset()
	if v := callNative(t, z, &out, "stream_socket_enable_crypto", sock, types.NewBool(true), method); v.Type != types.TypeFalse || !strings.Contains(out.String(), "already set-up") {
		t.Fatalf("seconde activation = %v (%q)", toAny(z, v), out.String())
	}

	// La méthode absente est lue dans ssl.crypto_method du contexte attaché.
	sock = starttls("cafile", caPath, "peer_name", "localhost", "crypto_method", 33)
	if v := callNative(t, z, nil, "stream_socket_enable_crypto", sock, types.NewBool(true)); v.Type != types.TypeTrue {
		t.Fatalf("crypto_method du contexte ignoré : %v", toAny(z, v))
	}
	if st := <-states; st.err != nil || st.version != tls.VersionTLS12 {
		t.Fatalf("crypto_method 33 a négocié %+v", st)
	}

	// Chaîne non vérifiable : le contexte sans cafile fait échouer la négociation avec un avertissement.
	sock = starttls("peer_name", "localhost")
	out.Reset()
	if v := callNative(t, z, &out, "stream_socket_enable_crypto", sock, types.NewBool(true), method); v.Type != types.TypeFalse || !strings.Contains(out.String(), "SSL operation failed") {
		t.Fatalf("certificat inconnu accepté : %v (%q)", toAny(z, v), out.String())
	}
	<-states

	if _, err := callErr(t, z, "stream_socket_enable_crypto", sock, types.NewBool(true)); err == nil ||
		err.Error() != "stream_socket_enable_crypto(): Argument #3 ($crypto_method) must be specified when enabling encryption" {
		t.Fatalf("méthode absente : %v", err)
	}
	mem := callNative(t, z, nil, "fopen", s("php://memory"), s("r+"))
	for _, c := range []struct {
		enable bool
		want   types.Type
	}{{true, types.TypeFalse}, {false, types.TypeTrue}} {
		out.Reset()
		v := callNative(t, z, &out, "stream_socket_enable_crypto", mem, types.NewBool(c.enable), method)
		if v.Type != c.want || !strings.Contains(out.String(), "This stream does not support SSL/crypto") {
			t.Fatalf("flux mémoire, enable=%v : %v (%q)", c.enable, toAny(z, v), out.String())
		}
	}
}

// TestStreamSelectMatchesPHP compare à PHP 8.3 les refus d'arguments de stream_select qui ne dépendent pas
// d'un tableau de ressources : types des ensembles, bornes du délai, ensembles vides ou sans flux.
func TestStreamSelectMatchesPHP(t *testing.T) {
	want := phpJSON(t, `
$w = null; $e = null; $f = fopen("php://memory", "r");
foreach (["none" => [null, 0, null], "empty" => [[], 0, null], "ints" => [[7, "x"], 0, null],
  "negs" => [[7], -1, null], "negu" => [[7], 0, -1], "nullsec" => [[7], null, 5]] as $k => [$rr, $sec, $us]) {
  $r = $rr; try { @stream_select($r, $w, $e, $sec, $us); $o[$k] = "ok"; } catch (Throwable $x) { $o[$k] = get_class($x).": ".$x->getMessage(); }
}
$r = 5; try { stream_select($r, $w, $e, 0); } catch (Throwable $x) { $o["int"] = get_class($x).": ".$x->getMessage(); }
$r = null; $w2 = $f; try { stream_select($r, $w2, $e, 0); } catch (Throwable $x) { $o["res"] = get_class($x).": ".$x->getMessage(); }
echo json_encode($o);
`)
	z := zone.New(1 << 18)
	defer z.Reset(DispatchCleanup)
	null := func() types.Value { return newRef(t, z, types.NewNull()) }
	f := callNative(t, z, nil, "fopen", mustStr(t, z, "php://memory"), mustStr(t, z, "r"))
	got := map[string]any{}
	for k, c := range map[string][4]types.Value{
		"none":    {null(), types.NewNull(), types.NewLong(0), types.NewNull()},
		"empty":   {newRef(t, z, phpArr(t, z)), types.NewNull(), types.NewLong(0), types.NewNull()},
		"ints":    {newRef(t, z, phpArr(t, z, 0, 7, 1, "x")), types.NewNull(), types.NewLong(0), types.NewNull()},
		"negs":    {newRef(t, z, phpArr(t, z, 0, 7)), types.NewNull(), types.NewLong(-1), types.NewNull()},
		"negu":    {newRef(t, z, phpArr(t, z, 0, 7)), types.NewNull(), types.NewLong(0), types.NewLong(-1)},
		"nullsec": {newRef(t, z, phpArr(t, z, 0, 7)), types.NewNull(), types.NewNull(), types.NewLong(5)},
		"int":     {newRef(t, z, types.NewLong(5)), types.NewNull(), types.NewLong(0), types.NewNull()},
		// types refuse aussi une ressource comme cible de référence : la valeur est passée directement.
		"res": {null(), f, types.NewLong(0), types.NewNull()},
	} {
		w := c[1]
		if w.IsNull() {
			w = null()
		}
		_, err := callErr(t, z, "stream_select", c[0], w, null(), c[2], c[3])
		got[k] = "ok"
		if err != nil {
			got[k] = ExceptionClass(err) + ": " + err.Error()
		}
	}
	for k, w := range want {
		sameJSON(t, k, got[k], w)
	}
	if len(got) != len(want) {
		t.Fatalf("%d clés comparées, PHP en rend %d", len(got), len(want))
	}
}

// TestStreamSelectWithPHPArraysAndByRefFiltering lève S9-M2 : stream_select reçoit de vrais tableaux PHP de
// ressources (stream_socket_client, fopen) à clés entières et chaînes, passés par référence ; chaque tableau
// est réécrit dans sa référence avec les seuls éléments prêts, clés et ordre conservés, comme PHP 8.3 rejoué
// sur le même scénario : $read garde la socket qui a un octet et le fichier, $write la socket écrivable,
// $except est vidé.
func TestStreamSelectWithPHPArraysAndByRefFiltering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := phpJSON(t, `
$srv = stream_socket_server("tcp://127.0.0.1:0"); $n = stream_socket_get_name($srv, false);
$a = stream_socket_client("tcp://$n"); $pa = stream_socket_accept($srv);
$b = stream_socket_client("tcp://$n"); $pb = stream_socket_accept($srv);
$f = fopen($argv[1], "r");
fwrite($pa, "x");
$r = [$a]; $w = null; $e = null; stream_select($r, $w, $e, null);
$lab = function ($arr) use ($a, $b, $f) { $o = []; foreach ($arr as $k => $v) { $o[] = $k . "=" . ($v === $a ? "a" : ($v === $b ? "b" : ($v === $f ? "f" : "?"))); } return $o; };
$r = [7 => $b, "prête" => $a, "f" => $f]; $w = ["w" => $b]; $e = [$a];
$n = stream_select($r, $w, $e, 1);
echo json_encode(["read" => $lab($r), "write" => $lab($w), "except" => $lab($e), "n" => $n, "sel" => [$r === null, $w === null]]);
`, path)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	conns := make(chan net.Conn, 2)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns <- c
		}
	}()
	z := zone.New(1 << 18)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	a := callNative(t, z, nil, "stream_socket_client", s("tcp://"+ln.Addr().String()))
	pa := <-conns
	defer pa.Close()
	b := callNative(t, z, nil, "stream_socket_client", s("tcp://"+ln.Addr().String()))
	pb := <-conns
	defer pb.Close()
	f := callNative(t, z, nil, "fopen", s(path), s("r"))
	for _, r := range []types.Value{a, b, f} {
		if r.Type != types.TypeResource {
			t.Fatalf("ouverture : %v", toAny(z, r))
		}
	}
	if _, err := pa.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	null := func() types.Value { return newRef(t, z, types.NewNull()) }
	// Attente de l'octet : un premier select sans borne sur [$a] le tamponne sans le consommer.
	pre, err := types.NewArray(z)
	if err != nil {
		t.Fatal(err)
	}
	if err := types.ArrayAppend(z, &pre, a); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeStreamSelect(z, nil, []types.Value{newRef(t, z, pre), null(), null(), types.NewNull()}); err != nil {
		t.Fatal(err)
	}

	read, err := types.NewArray(z)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range []struct {
		k types.Value
		v types.Value
	}{{types.NewLong(7), b}, {s("prête"), a}, {s("f"), f}} {
		if err := types.ArraySet(z, &read, kv.k, kv.v, warnDrop); err != nil {
			t.Fatal(err)
		}
	}
	write := phpArr(t, z, "w", b)
	except, err := types.NewArray(z)
	if err != nil {
		t.Fatal(err)
	}
	if err := types.ArrayAppend(z, &except, a); err != nil {
		t.Fatal(err)
	}
	rr, rw, re := newRef(t, z, read), newRef(t, z, write), newRef(t, z, except)
	n, err := nativeStreamSelect(z, nil, []types.Value{rr, rw, re, types.NewLong(1)})
	if err != nil {
		t.Fatal(err)
	}
	label := func(ref types.Value) []any {
		arr := deref(z, ref)
		if arr.Type != types.TypeArray {
			t.Fatalf("référence réécrite en %v, tableau attendu", toAny(z, arr))
		}
		out := []any{}
		types.ArrayForEach(z, arr, func(k, v types.Value) bool {
			ks := ""
			if k.Type == types.TypeLong {
				ks = strconv.FormatInt(k.ToLong(), 10)
			} else {
				ks = types.StringData(z, k)
			}
			lab := "?"
			for name, r := range map[string]types.Value{"a": a, "b": b, "f": f} {
				if dv := deref(z, v); dv.Type == types.TypeResource && dv.DataSlot == r.DataSlot {
					lab = name
				}
			}
			out = append(out, ks+"="+lab)
			return true
		})
		return out
	}
	got := map[string]any{
		"read": label(rr), "write": label(rw), "except": label(re), "n": float64(n.ToLong()),
		"sel": []any{deref(z, rr).IsNull(), deref(z, rw).IsNull()},
	}
	for k, w := range want {
		sameJSON(t, k, got[k], w)
	}
	sameJSON(t, "read", got["read"], []any{"prête=a", "f=f"})
	sameJSON(t, "write", got["write"], []any{"w=b"})
	sameJSON(t, "except", got["except"], []any{})
	// La ressource prête reste lisible : le sondage n'a rien consommé.
	if got := types.StringData(z, callNative(t, z, nil, "fread", a, types.NewLong(1))); got != "x" {
		t.Fatalf("octet sondé perdu : %q", got)
	}

	// Tampon d'émission TCP saturé : stream_select interroge réellement POLLOUT et ne déclare pas la socket
	// inscriptible tant que le pair n'a pas drainé le tampon ; une fois drainé, la socket redevient prête.
	zs := getZoneStreams(z)
	bh, ok := zs.lookupStream(b)
	if !ok {
		t.Fatal("flux b introuvable")
	}
	if tc, ok := bh.conn.(*net.TCPConn); ok {
		_ = tc.SetWriteBuffer(4096)
	}
	if tcpPeer, ok := pb.(*net.TCPConn); ok {
		_ = tcpPeer.SetReadBuffer(4096)
	}
	chunk := bytes.Repeat([]byte("W"), 32768)
	totalWritten := 0
	if sc, ok := bh.conn.(syscall.Conn); ok {
		if rc, err := sc.SyscallConn(); err == nil {
			_ = rc.Control(func(fd uintptr) {
				for range 256 {
					nw, err := syscall.Write(int(fd), chunk)
					if nw > 0 {
						totalWritten += nw
					}
					if err != nil {
						break
					}
				}
			})
		}
	}
	if pollSocketConn(bh.conn, pollOut, 0)&pollOut != 0 {
		t.Fatal("le tampon d'émission TCP de b devrait être saturé")
	}
	wSat, err := types.NewArray(z)
	if err != nil {
		t.Fatal(err)
	}
	if err := types.ArrayAppend(z, &wSat, b); err != nil {
		t.Fatal(err)
	}
	rwSat := newRef(t, z, wSat)
	t0 := time.Now()
	nSat, err := nativeStreamSelect(z, nil, []types.Value{null(), rwSat, null(), types.NewLong(0), types.NewLong(30000)})
	if err != nil {
		t.Fatal(err)
	}
	if nSat.ToLong() != 0 || len(label(rwSat)) != 0 || time.Since(t0) < 20*time.Millisecond {
		t.Fatalf("stream_select sur socket saturée : n=%d write=%v durée=%v ; 0 et attente >=20ms attendus", nSat.ToLong(), label(rwSat), time.Since(t0))
	}
	// S9R2-M1 : attente simultanée d'une socket de lecture silencieuse (a) et d'une socket d'écriture
	// initialement saturée (b) drainée après 25 ms : stream_select doit se réveiller dès que b redevient
	// inscriptible, bien avant l'échéance de lecture de 2 s.
	rIdle, err := types.NewArray(z)
	if err != nil {
		t.Fatal(err)
	}
	if err := types.ArrayAppend(z, &rIdle, a); err != nil {
		t.Fatal(err)
	}
	wDrained, err := types.NewArray(z)
	if err != nil {
		t.Fatal(err)
	}
	if err := types.ArrayAppend(z, &wDrained, b); err != nil {
		t.Fatal(err)
	}
	rrIdle := newRef(t, z, rIdle)
	rwDrained := newRef(t, z, wDrained)
	drainErr := make(chan error, 1)
	go func() {
		time.Sleep(25 * time.Millisecond)
		_, err := io.ReadFull(pb, make([]byte, totalWritten))
		drainErr <- err
	}()
	t1 := time.Now()
	nDrained, err := nativeStreamSelect(z, nil, []types.Value{rrIdle, rwDrained, null(), types.NewLong(3), types.NewLong(0)})
	elapsed := time.Since(t1)
	if err != nil {
		t.Fatal(err)
	}
	if derr := <-drainErr; derr != nil {
		t.Fatalf("drainage de %d octets : %v", totalWritten, derr)
	}
	if nDrained.ToLong() != 1 || len(label(rrIdle)) != 0 || len(label(rwDrained)) != 1 || elapsed >= 1500*time.Millisecond {
		t.Fatalf("stream_select simultané lecture muette + écriture drainée : n=%d read=%v write=%v durée=%v ; n=1 write=[b] durée<1.5s (échéance 3s) attendus",
			nDrained.ToLong(), label(rrIdle), label(rwDrained), elapsed)
	}
}

// selectSet construit un ensemble de stream_select à partir de ressources de flux, sans tableau PHP.
func selectSet(t *testing.T, z *zone.Zone, res ...types.Value) []*selectEntry {
	t.Helper()
	zs := getZoneStreams(z)
	var out []*selectEntry
	for _, r := range res {
		h, ok := zs.lookupStream(r)
		if !ok {
			t.Fatalf("ressource %v sans flux", r)
		}
		out = append(out, &selectEntry{val: r, h: h})
	}
	return out
}

// TestStreamSelectReadinessCore éprouve le cœur de stream_select sur des sockets réelles : attente bornée,
// disponibilité en lecture sans consommation, socket répétée sondée une fois, écriture toujours prête, fichier
// toujours prêt, attente sans borne et fin de flux lisible.
func TestStreamSelectReadinessCore(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	conns := make(chan net.Conn, 2)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns <- c
		}
	}()
	z := zone.New(1 << 18)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	sock := callNative(t, z, nil, "stream_socket_client", s("tcp://"+ln.Addr().String()))
	srv := <-conns
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := callNative(t, z, nil, "fopen", s(path), s("r"))
	run := func(sets [3][]*selectEntry, wait time.Duration, infinite bool) (int, time.Duration) {
		t.Helper()
		start := time.Now()
		return selectReady(sets, wait, infinite), time.Since(start)
	}

	r := selectSet(t, z, sock, sock)
	if n, d := run([3][]*selectEntry{r}, 200*time.Millisecond, false); n != 0 || d < 150*time.Millisecond || d > 3*time.Second || r[0].ready {
		t.Fatalf("socket sans donnée : %d en %v", n, d)
	}
	if _, err := srv.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	r = selectSet(t, z, sock, sock)
	if n, _ := run([3][]*selectEntry{r}, 2*time.Second, false); n != 2 || !r[0].ready || !r[1].ready {
		t.Fatalf("socket avec donnée, deux fois dans l'ensemble : %d", n)
	}
	if got := types.StringData(z, callNative(t, z, nil, "fread", sock, types.NewLong(10))); got != "abc" {
		t.Fatalf("octets consommés par la sonde : fread = %q", got)
	}
	r, w := selectSet(t, z, sock), selectSet(t, z, sock)
	if n, d := run([3][]*selectEntry{r, w}, 5*time.Second, false); n != 1 || d > 2*time.Second || r[0].ready || !w[0].ready {
		t.Fatalf("écriture prête sans attendre la lecture : %d en %v", n, d)
	}
	r = selectSet(t, z, file, sock)
	if n, d := run([3][]*selectEntry{r}, 5*time.Second, false); n != 1 || d > 2*time.Second || !r[0].ready || r[1].ready {
		t.Fatalf("fichier toujours prêt : %d en %v", n, d)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = srv.Write([]byte("z"))
	}()
	r = selectSet(t, z, sock)
	if n, _ := run([3][]*selectEntry{r}, 0, true); n != 1 {
		t.Fatalf("attente sans borne : %d", n)
	}
	callNative(t, z, nil, "fread", sock, types.NewLong(10))
	_ = srv.Close()
	r = selectSet(t, z, sock)
	if n, _ := run([3][]*selectEntry{r}, 2*time.Second, false); n != 1 {
		t.Fatalf("fin de flux non lisible : %d", n)
	}
	if got := types.StringData(z, callNative(t, z, nil, "fread", sock, types.NewLong(10))); got != "" || callNative(t, z, nil, "feof", sock).Type != types.TypeTrue {
		t.Fatalf("après fin de flux : fread %q", got)
	}
}

// TestStreamContextSetOptionsAndSocketGetStatusMatchPHP compare à PHP 8.3 stream_context_set_options
// (exactement deux arguments) et l'alias socket_get_status.
func TestStreamContextSetOptionsAndSocketGetStatusMatchPHP(t *testing.T) {
	want := phpJSON(t, `
$c = stream_context_create(["ssl" => ["verify_peer" => true]]);
$o["set"] = stream_context_set_options($c, ["ssl" => ["verify_peer" => false, "peer_name" => "x"], "http" => ["method" => "POST"]]);
$o["get"] = stream_context_get_options($c);
foreach (["four" => fn() => stream_context_set_options($c, "ssl", "x", 1), "int" => fn() => stream_context_set_options($c, 5),
  "notres" => fn() => stream_context_set_options(5, []), "form" => fn() => stream_context_set_options($c, ["ssl" => 5])] as $k => $f) {
  try { $f(); $o[$k] = "ok"; } catch (Throwable $e) { $o[$k] = get_class($e).": ".$e->getMessage(); }
}
$f = fopen("php://memory", "r+"); $o["meta"] = socket_get_status($f); $o["same"] = socket_get_status($f) === stream_get_meta_data($f);
try { socket_get_status(5); } catch (Throwable $e) { $o["badarg"] = $e->getMessage(); }
echo json_encode($o);
`)
	z := zone.New(1 << 18)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	got := map[string]any{}
	c := callNative(t, z, nil, "stream_context_create", phpArr(t, z, "ssl", phpArr(t, z, "verify_peer", true)))
	got["set"] = toAny(z, callNative(t, z, nil, "stream_context_set_options", c,
		phpArr(t, z, "ssl", phpArr(t, z, "verify_peer", false, "peer_name", "x"), "http", phpArr(t, z, "method", "POST"))))
	got["get"] = toAny(z, callNative(t, z, nil, "stream_context_get_options", c))
	for k, args := range map[string][]types.Value{
		"four":   {c, s("ssl"), s("x"), types.NewLong(1)},
		"int":    {c, types.NewLong(5)},
		"notres": {types.NewLong(5), phpArr(t, z)},
		"form":   {c, phpArr(t, z, "ssl", 5)},
	} {
		_, err := callErr(t, z, "stream_context_set_options", args...)
		got[k] = "ok"
		if err != nil {
			got[k] = ExceptionClass(err) + ": " + err.Error()
		}
	}
	f := callNative(t, z, nil, "fopen", s("php://memory"), s("r+"))
	got["meta"] = toAny(z, callNative(t, z, nil, "socket_get_status", f))
	sameJSON(t, "same", got["meta"], toAny(z, callNative(t, z, nil, "stream_get_meta_data", f)))
	got["same"] = true
	if _, err := callErr(t, z, "socket_get_status", types.NewLong(5)); err != nil {
		got["badarg"] = err.Error()
	}
	for k, w := range want {
		sameJSON(t, k, got[k], w)
	}
	if len(got) != len(want) {
		t.Fatalf("%d clés comparées, PHP en rend %d", len(got), len(want))
	}
}

// gzScript est la séquence gz rejouée à l'identique par PHP 8.3 et par le paquet ; $argv[1] est le répertoire.
const gzScript = `
$d = $argv[1]; $o = [];
$g = gzopen("$d/a.gz", "wb9");
$o["w"] = [gzwrite($g, "ligne1\nligne2\n"), gzputs($g, "abcdef", 3), gztell($g), gzwrite($g, ""), gzseek($g, 2, SEEK_CUR), gztell($g), gzwrite($g, "Z")];
$o["meta"] = stream_get_meta_data($g);
$o["close"] = gzclose($g);
$o["plain"] = gzdecode(file_get_contents("$d/a.gz"));
$g = gzopen("$d/a.gz", "rb");
$o["r"] = [gzgets($g), gzgets($g, 4), gzread($g, 3), gztell($g), gzeof($g), gzseek($g, 2), gztell($g), gzread($g, 100), gzeof($g), gzread($g, 10), gzeof($g)];
$o["seekend"] = @gzseek($g, 0, SEEK_END);
$o["back"] = [gzseek($g, -1, SEEK_CUR), gztell($g), rewind($g), fread($g, 3), feof($g), ftell($g), fgets($g)];
$o["far"] = [gzseek($g, 50), gztell($g), gzread($g, 5), gzeof($g), gztell($g)];
$o["neg"] = [gzseek($g, -1), gztell($g)];
fclose($g);
$g = gzopen("$d/a.gz", "r"); ob_start(); $n = gzpassthru($g); $o["pass"] = [$n, ob_get_clean()]; gzclose($g);
file_put_contents("$d/plain.txt", "pas gzip\n"); $g = gzopen("$d/plain.txt", "r"); $o["raw"] = [gzread($g, 100), gzeof($g)]; gzclose($g);
$o["absent"] = @gzopen("$d/absent.gz", "r");
$o["rw"] = @gzopen("$d/a.gz", "r+");
$o["badmode"] = @gzopen("$d/a.gz", "q");
$o["mem"] = @gzopen("php://memory", "w");
$g = gzopen("$d/b.gz", "w"); fwrite($g, "via fwrite"); $o["wneg"] = [gzseek($g, 3), gztell($g)]; fclose($g);
$g = gzopen("$d/b.gz", "a"); fputs($g, "+suite"); gzseek($g, 2, SEEK_CUR); gzseek($g, 1, SEEK_CUR); gzwrite($g, "z"); gzseek($g, 2, SEEK_CUR); gzclose($g);
$g = gzopen("$d/b.gz", "r"); $o["append"] = gzread($g, 100); gzclose($g);
try { gzclose($g); } catch (Throwable $e) { $o["closed"] = get_class($e).": ".$e->getMessage(); }
try { gzread(gzopen("$d/b.gz", "r"), 0); } catch (Throwable $e) { $o["zero"] = get_class($e).": ".$e->getMessage(); }
echo json_encode($o);
`

func gunzipFile(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(gr)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestGzStreamsMatchPHP rejoue gzScript dans le paquet et le compare à PHP 8.3 : écriture, lecture,
// déplacements (saut paresseux en écriture, retour arrière en lecture, SEEK_END refusé), fichier non gzip,
// refus d'ouverture, ajout d'un membre et diagnostics ; puis vérifie les avertissements émis.
func TestGzStreamsMatchPHP(t *testing.T) {
	want := phpJSON(t, gzScript, t.TempDir())
	d := t.TempDir()
	z := zone.New(1 << 20)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	l := func(n int64) types.Value { return types.NewLong(n) }
	call := func(name string, args ...types.Value) any {
		t.Helper()
		return toAny(z, callNative(t, z, nil, name, args...))
	}
	got := map[string]any{}
	g := callNative(t, z, nil, "gzopen", s(d+"/a.gz"), s("wb9"))
	got["w"] = []any{call("gzwrite", g, s("ligne1\nligne2\n")), call("gzputs", g, s("abcdef"), l(3)), call("gztell", g),
		call("gzwrite", g, s("")), call("gzseek", g, l(2), l(SEEK_CUR)), call("gztell", g), call("gzwrite", g, s("Z"))}
	got["meta"] = call("stream_get_meta_data", g)
	got["close"] = call("gzclose", g)
	got["plain"] = gunzipFile(t, d+"/a.gz")
	g = callNative(t, z, nil, "gzopen", s(d+"/a.gz"), s("rb"))
	got["r"] = []any{call("gzgets", g), call("gzgets", g, l(4)), call("gzread", g, l(3)), call("gztell", g), call("gzeof", g),
		call("gzseek", g, l(2)), call("gztell", g), call("gzread", g, l(100)), call("gzeof", g), call("gzread", g, l(10)), call("gzeof", g)}
	var out bytes.Buffer
	got["seekend"] = toAny(z, callNative(t, z, &out, "gzseek", g, l(0), l(SEEK_END)))
	if !strings.Contains(out.String(), "Warning: gzseek(): SEEK_END is not supported") {
		t.Errorf("avertissement SEEK_END : %q", out.String())
	}
	got["back"] = []any{call("gzseek", g, l(-1), l(SEEK_CUR)), call("gztell", g), call("rewind", g), call("fread", g, l(3)),
		call("feof", g), call("ftell", g), call("fgets", g)}
	got["far"] = []any{call("gzseek", g, l(50)), call("gztell", g), call("gzread", g, l(5)), call("gzeof", g), call("gztell", g)}
	got["neg"] = []any{call("gzseek", g, l(-1)), call("gztell", g)}
	call("fclose", g)
	g = callNative(t, z, nil, "gzopen", s(d+"/a.gz"), s("r"))
	out.Reset()
	n := toAny(z, callNative(t, z, &out, "gzpassthru", g))
	got["pass"] = []any{n, out.String()}
	call("gzclose", g)
	if err := os.WriteFile(d+"/plain.txt", []byte("pas gzip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g = callNative(t, z, nil, "gzopen", s(d+"/plain.txt"), s("r"))
	got["raw"] = []any{call("gzread", g, l(100)), call("gzeof", g)}
	call("gzclose", g)
	for k, c := range map[string]struct{ path, mode, warn string }{
		"absent":  {d + "/absent.gz", "r", "Warning: gzopen(" + d + "/absent.gz): Failed to open stream: No such file or directory"},
		"rw":      {d + "/a.gz", "r+", "Warning: gzopen(): Cannot open a zlib stream for reading and writing at the same time!"},
		"badmode": {d + "/a.gz", "q", "Warning: gzopen(" + d + "/a.gz): Failed to open stream: `q' is not a valid mode for fopen"},
		"mem":     {"php://memory", "w", "Warning: gzopen(): Cannot represent a stream of type MEMORY as a File Descriptor"},
	} {
		out.Reset()
		got[k] = toAny(z, callNative(t, z, &out, "gzopen", s(c.path), s(c.mode)))
		if !strings.Contains(out.String(), c.warn) {
			t.Errorf("%s : avertissement %q, attendu %q", k, out.String(), c.warn)
		}
	}
	g = callNative(t, z, nil, "gzopen", s(d+"/b.gz"), s("w"))
	call("fwrite", g, s("via fwrite"))
	got["wneg"] = []any{call("gzseek", g, l(3)), call("gztell", g)}
	call("fclose", g)
	g = callNative(t, z, nil, "gzopen", s(d+"/b.gz"), s("a"))
	call("fputs", g, s("+suite"))
	call("gzseek", g, l(2), l(SEEK_CUR))
	call("gzseek", g, l(1), l(SEEK_CUR))
	call("gzwrite", g, s("z"))
	call("gzseek", g, l(2), l(SEEK_CUR))
	call("gzclose", g)
	g = callNative(t, z, nil, "gzopen", s(d+"/b.gz"), s("r"))
	got["append"] = call("gzread", g, l(100))
	call("gzclose", g)
	if _, err := callErr(t, z, "gzclose", g); err != nil {
		got["closed"] = ExceptionClass(err) + ": " + err.Error()
	}
	if _, err := callErr(t, z, "gzread", callNative(t, z, nil, "gzopen", s(d+"/b.gz"), s("r")), l(0)); err != nil {
		got["zero"] = ExceptionClass(err) + ": " + err.Error()
	}
	for k, w := range want {
		sameJSON(t, k, got[k], w)
	}
	if len(got) != len(want) {
		t.Fatalf("%d clés comparées, PHP en rend %d", len(got), len(want))
	}
}

// TestGzStreamClosedByZoneReset vérifie que z.Reset ferme un flux gzip d'écriture laissé ouvert : la fin du
// membre (CRC et taille) est écrite, le fichier se décompresse, et aucun flux ne reste ouvert. Le chemin, libéré
// dans une arène empoisonnée dès l'ouverture, ne doit pas être relu depuis l'arène.
func TestGzStreamClosedByZoneReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reset.gz")
	z := zone.New(1 << 18)
	z.SetPoisonOnFree(true)
	p, m := mustStr(t, z, path), mustStr(t, z, "w")
	g := callNative(t, z, nil, "gzopen", p, m)
	types.ReleaseValue(z, &p)
	types.ReleaseValue(z, &m)
	callNative(t, z, nil, "gzwrite", g, mustStr(t, z, strings.Repeat("contenu ", 64)))
	if OpenStreamCount(z) != 1 {
		t.Fatalf("OpenStreamCount = %d", OpenStreamCount(z))
	}
	z.Reset(DispatchCleanup)
	if OpenStreamCount(z) != 0 {
		t.Fatalf("flux gzip ouvert après z.Reset")
	}
	if got := gunzipFile(t, path); got != strings.Repeat("contenu ", 64) {
		t.Fatalf("contenu après z.Reset : %q", got)
	}
}
