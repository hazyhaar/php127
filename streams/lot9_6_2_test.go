package streams

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Lot 9.6-2 : politique TLS exacte (S9-B1), sockets persistantes et asynchrones (S9-M5), répertoire courant
// et masque de création portés par la Zone (S9-M7).

// selfSignedCert fabrique un certificat autosigné pour names, valide de notBefore à notAfter, avec les
// extensions supplémentaires extra.
func selfSignedCert(t *testing.T, names []string, notBefore, notAfter time.Time, extra ...pkix.Extension) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: names[0]},
		DNSNames:              names,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		ExtraExtensions:       extra,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// tlsEcho sert en TLS selon cfg et renvoie chaque ligne préfixée par « tls: » ; rend l'adresse tls://.
func tlsEcho(t *testing.T, cfg *tls.Config) string {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	echoServer(t, ln, "tls:", nil)
	return "tls://" + ln.Addr().String()
}

// TestTLSPolicyExactEnforcement lève S9-B1 : verify_peer=false n'efface plus verify_peer_name, allow_self_signed
// ne lève que l'autorité inconnue d'un certificat autosigné en cours de validité, et un masque crypto_method
// discontinu refuse la version intermédiaire que l'intervalle [min, max] laissait passer.
func TestTLSPolicyExactEnforcement(t *testing.T) {
	now := time.Now()
	valid := selfSignedCert(t, []string{"localhost"}, now.Add(-time.Hour), now.Add(time.Hour))
	expired := selfSignedCert(t, []string{"localhost"}, now.Add(-2*time.Hour), now.Add(-time.Hour))
	future := selfSignedCert(t, []string{"localhost"}, now.Add(time.Hour), now.Add(2*time.Hour))
	// Une extension critique inconnue fait échouer x509.Verify sur UnhandledCriticalExtension, et non sur
	// l'autorité inconnue : allow_self_signed ne la lève pas, comme X509_V_ERR_UNHANDLED_CRITICAL_EXTENSION.
	critical := selfSignedCert(t, []string{"localhost"}, now.Add(-time.Hour), now.Add(time.Hour),
		pkix.Extension{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1}, Critical: true, Value: []byte{0x05, 0x00}})
	validAddr := tlsEcho(t, &tls.Config{Certificates: []tls.Certificate{valid}})
	expiredAddr := tlsEcho(t, &tls.Config{Certificates: []tls.Certificate{expired}})
	futureAddr := tlsEcho(t, &tls.Config{Certificates: []tls.Certificate{future}})
	criticalAddr := tlsEcho(t, &tls.Config{Certificates: []tls.Certificate{critical}})
	tls11Addr := tlsEcho(t, &tls.Config{Certificates: []tls.Certificate{valid}, MinVersion: tls.VersionTLS11, MaxVersion: tls.VersionTLS11})

	z := zone.New(1 << 18)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	en, es := newRef(t, z, types.NewNull()), newRef(t, z, types.NewNull())
	for _, c := range []struct {
		name string
		addr string
		ssl  []any
		ok   bool
		msg  string
	}{
		{"verify_peer false, nom par défaut étranger", validAddr, []any{"verify_peer", false, "peer_name", "autre.example"}, false, "autre.example"},
		{"verify_peer false, verify_peer_name true étranger", validAddr, []any{"verify_peer", false, "verify_peer_name", true, "peer_name", "autre.example"}, false, "autre.example"},
		{"verify_peer false, nom conforme", validAddr, []any{"verify_peer", false, "verify_peer_name", true, "peer_name", "localhost"}, true, ""},
		{"verify_peer false, verify_peer_name false", validAddr, []any{"verify_peer", false, "verify_peer_name", false, "peer_name", "autre.example"}, true, ""},
		{"allow_self_signed, autosigné valide", validAddr, []any{"allow_self_signed", true, "peer_name", "localhost"}, true, ""},
		{"allow_self_signed, autosigné expiré", expiredAddr, []any{"allow_self_signed", true, "peer_name", "localhost"}, false, "expired"},
		{"allow_self_signed, autosigné pas encore valide", futureAddr, []any{"allow_self_signed", true, "peer_name", "localhost"}, false, "not yet valid"},
		{"allow_self_signed, autosigné à extension critique inconnue", criticalAddr, []any{"allow_self_signed", true, "peer_name", "localhost"}, false, "unhandled critical extension"},
		{"allow_self_signed, autosigné valide au nom étranger", validAddr, []any{"allow_self_signed", true, "peer_name", "autre.example"}, false, "autre.example"},
		{"masque TLSv1.0|TLSv1.2 contre un serveur TLS 1.1", tls11Addr, []any{"verify_peer", false, "verify_peer_name", false, "crypto_method", 9 | 33}, false, "TLS 1.1"},
		{"masque TLSv1.1 contre un serveur TLS 1.1", tls11Addr, []any{"verify_peer", false, "verify_peer_name", false, "crypto_method", 17}, true, ""},
		{"masque TLSv1.0|1.1|1.2 contre un serveur TLS 1.1", tls11Addr, []any{"verify_peer", false, "verify_peer_name", false, "crypto_method", 9 | 17 | 33}, true, ""},
		{"masque TLSv1.2 contre un serveur TLS 1.1", tls11Addr, []any{"verify_peer", false, "verify_peer_name", false, "crypto_method", 33}, false, ""},
	} {
		ctx := callNative(t, z, nil, "stream_context_create", phpArr(t, z, "ssl", phpArr(t, z, c.ssl...)))
		v := callNative(t, z, nil, "stream_socket_client", s(c.addr), en, es, types.NewDouble(2), types.NewLong(STREAM_CLIENT_CONNECT), ctx)
		errstr := types.StringData(z, deref(z, es))
		if (v.Type == types.TypeResource) != c.ok {
			t.Errorf("%s : stream_socket_client = %v (%q)", c.name, toAny(z, v), errstr)
			continue
		}
		if !c.ok {
			if !strings.Contains(errstr, c.msg) {
				t.Errorf("%s : errstr %q ne cite pas %q", c.name, errstr, c.msg)
			}
			continue
		}
		callNative(t, z, nil, "fwrite", v, s("ping\n"))
		if got := types.StringData(z, callNative(t, z, nil, "fgets", v)); got != "tls:ping\n" {
			t.Errorf("%s : fgets = %q", c.name, got)
		}
		callNative(t, z, nil, "fclose", v)
	}
}

// singleAccept n'accepte qu'une connexion TCP puis ferme l'écoute : toute nouvelle connexion est refusée.
// Chaque ligne reçue est renvoyée préfixée par « echo: » ; done se ferme quand le client a fermé la connexion.
func singleAccept(t *testing.T) (*net.TCPAddr, <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := ln.Accept()
		_ = ln.Close()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			if _, err := io.WriteString(c, "echo:"+line); err != nil {
				return
			}
		}
	}()
	return ln.Addr().(*net.TCPAddr), done
}

// TestPersistentAndAsyncClientSockets lève S9-M5 : une socket ouverte avec STREAM_CLIENT_PERSISTENT ou par
// pfsockopen survit à fclose et sert la séquence suivante de la même Zone sur la même connexion TCP (le serveur
// n'accepte qu'une connexion puis refuse), le Reset de la Zone la ferme réellement, et STREAM_CLIENT_ASYNC_CONNECT,
// seul ou avec STREAM_CLIENT_CONNECT, ouvre la socket en mode non bloquant.
func TestPersistentAndAsyncClientSockets(t *testing.T) {
	waitClosed := func(t *testing.T, name string, done <-chan struct{}) {
		t.Helper()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s : connexion persistante restée ouverte après le Reset de la Zone", name)
		}
	}
	exchange := func(t *testing.T, z *zone.Zone, name string, sock types.Value, word string) {
		t.Helper()
		if sock.Type != types.TypeResource {
			t.Fatalf("%s : ouverture = %v", name, toAny(z, sock))
		}
		callNative(t, z, nil, "fwrite", sock, mustStr(t, z, word+"\n"))
		if got := types.StringData(z, callNative(t, z, nil, "fgets", sock)); got != "echo:"+word+"\n" {
			t.Fatalf("%s : fgets = %q", name, got)
		}
	}

	t.Run("stream_socket_client", func(t *testing.T) {
		addr, done := singleAccept(t)
		z := zone.New(1 << 18)
		en, es := newRef(t, z, types.NewNull()), newRef(t, z, types.NewNull())
		for i, word := range []string{"un", "deux", "trois"} {
			sock := callNative(t, z, nil, "stream_socket_client", mustStr(t, z, "tcp://"+addr.String()), en, es,
				types.NewDouble(2), types.NewLong(STREAM_CLIENT_CONNECT|STREAM_CLIENT_PERSISTENT))
			exchange(t, z, "séquence "+strconv.Itoa(i), sock, word)
			if v := callNative(t, z, nil, "fclose", sock); v.Type != types.TypeTrue {
				t.Fatalf("fclose = %v", toAny(z, v))
			}
			if n := OpenStreamCount(z); n != 0 {
				t.Fatalf("après fclose, %d flux restent enregistrés", n)
			}
		}
		z.Reset(DispatchCleanup)
		waitClosed(t, "stream_socket_client", done)
	})

	t.Run("pfsockopen", func(t *testing.T) {
		addr, done := singleAccept(t)
		z := zone.New(1 << 18)
		en, es := newRef(t, z, types.NewNull()), newRef(t, z, types.NewNull())
		open := func() types.Value {
			return callNative(t, z, nil, "pfsockopen", mustStr(t, z, "127.0.0.1"), types.NewLong(int64(addr.Port)), en, es, types.NewDouble(2))
		}
		p1 := open()
		exchange(t, z, "pfsockopen 1", p1, "un")
		// Une seconde ouverture pendant que la première est en service rend le même flux, comme PHP.
		p2 := open()
		if p2.Type != types.TypeResource || p2.DataSlot != p1.DataSlot {
			t.Fatalf("pfsockopen en service : %v, attendu le slot %d", toAny(z, p2), p1.DataSlot)
		}
		exchange(t, z, "pfsockopen 2", p2, "deux")
		callNative(t, z, nil, "fclose", p1)
		p3 := open()
		exchange(t, z, "pfsockopen 3", p3, "trois")
		callNative(t, z, nil, "fclose", p3)
		z.Reset(DispatchCleanup)
		waitClosed(t, "pfsockopen", done)
	})

	t.Run("async", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		conns := make(chan net.Conn, 4)
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
		en, es := newRef(t, z, types.NewNull()), newRef(t, z, types.NewNull())
		for _, c := range []struct {
			flags   int64
			blocked bool
		}{
			{STREAM_CLIENT_ASYNC_CONNECT, false},
			{STREAM_CLIENT_CONNECT | STREAM_CLIENT_ASYNC_CONNECT, false},
			{STREAM_CLIENT_CONNECT, true},
		} {
			sock := callNative(t, z, nil, "stream_socket_client", mustStr(t, z, "tcp://"+ln.Addr().String()), en, es, types.NewDouble(2), types.NewLong(c.flags))
			if sock.Type != types.TypeResource {
				t.Fatalf("drapeaux %d : %v (%v)", c.flags, toAny(z, sock), toAny(z, es))
			}
			srv := <-conns
			defer srv.Close()
			meta := toAny(z, callNative(t, z, nil, "stream_get_meta_data", sock)).(map[string]any)
			if meta["blocked"] != c.blocked {
				t.Fatalf("drapeaux %d : blocked=%v, attendu %v", c.flags, meta["blocked"], c.blocked)
			}
			if c.blocked {
				continue
			}
			start := time.Now()
			got := callNative(t, z, nil, "fread", sock, types.NewLong(16))
			if elapsed := time.Since(start); elapsed > 2*time.Second || types.StringData(z, got) != "" {
				t.Fatalf("drapeaux %d : fread sans donnée a rendu %q en %v", c.flags, types.StringData(z, got), elapsed)
			}
		}

		// Serveur TLS muet (accepte TCP mais n'envoie jamais ServerHello) : l'ouverture synchrone expire au bout du
		// délai, tandis que STREAM_CLIENT_ASYNC_CONNECT rend immédiatement le flux sans bloquer sur la poignée de main.
		stalledLn, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer stalledLn.Close()
		go func() {
			for {
				c, err := stalledLn.Accept()
				if err != nil {
					return
				}
				t.Cleanup(func() { _ = c.Close() })
			}
		}()
		startAsync := time.Now()
		asyncTLS := callNative(t, z, nil, "stream_socket_client", mustStr(t, z, "tls://"+stalledLn.Addr().String()), en, es, types.NewDouble(2), types.NewLong(STREAM_CLIENT_ASYNC_CONNECT))
		if elapsed := time.Since(startAsync); asyncTLS.Type != types.TypeResource || elapsed > 500*time.Millisecond {
			t.Fatalf("tls STREAM_CLIENT_ASYNC_CONNECT sur pair muet : %v en %v, ressource immédiate attendue", toAny(z, asyncTLS), elapsed)
		}
	})
}

// TestCwdAndUmaskSharedWithZone lève S9-M7 : chdir et umask écrivent l'état de la Zone, lu par les autres
// paquets (stdlib) ; un répertoire ou un masque posé sur la Zone est vu par getcwd, umask, fopen et la création
// de fichiers ; le Reset de la Zone rend les valeurs initiales.
func TestCwdAndUmaskSharedWithZone(t *testing.T) {
	z := zone.New(1 << 16)
	defer z.Reset(DispatchCleanup)
	s := func(v string) types.Value { return mustStr(t, z, v) }
	first, second := t.TempDir(), t.TempDir()

	if v := callNative(t, z, nil, "chdir", s(first)); v.Type != types.TypeTrue {
		t.Fatalf("chdir = %v", toAny(z, v))
	}
	if got := z.Cwd(); got != first {
		t.Fatalf("z.Cwd() après chdir = %q, attendu %q", got, first)
	}
	z.SetCwd(second)
	if got := types.StringData(z, callNative(t, z, nil, "getcwd")); got != second {
		t.Fatalf("getcwd après z.SetCwd = %q, attendu %q", got, second)
	}

	if old := callNative(t, z, nil, "umask", types.NewLong(0o077)); old.ToLong() != zone.DefaultUmask {
		t.Fatalf("umask(077) rend %o, attendu %o", old.ToLong(), zone.DefaultUmask)
	}
	if got := z.Umask(); got != 0o077 {
		t.Fatalf("z.Umask() après umask(077) = %o", got)
	}
	z.SetUmask(0o027)
	if got := callNative(t, z, nil, "umask").ToLong(); got != 0o027 {
		t.Fatalf("umask() après z.SetUmask(027) = %o", got)
	}
	f := callNative(t, z, nil, "fopen", s("rel.txt"), s("w"))
	if f.Type != types.TypeResource {
		t.Fatalf("fopen relatif = %v", toAny(z, f))
	}
	callNative(t, z, nil, "fclose", f)
	fi, err := os.Stat(filepath.Join(second, "rel.txt"))
	if err != nil {
		t.Fatalf("fichier relatif absent du répertoire de la Zone : %v", err)
	}
	if mode := fi.Mode().Perm(); mode != 0o640 {
		t.Fatalf("mode de création %o, attendu 640 sous le masque 027 de la Zone", mode)
	}

	z.Reset(DispatchCleanup)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := types.StringData(z, callNative(t, z, nil, "getcwd")); got != wd {
		t.Fatalf("getcwd après Reset = %q, attendu %q", got, wd)
	}
	if got := callNative(t, z, nil, "umask").ToLong(); got != zone.DefaultUmask {
		t.Fatalf("umask() après Reset = %o", got)
	}
}
