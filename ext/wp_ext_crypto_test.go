package ext

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// fixture rend le chemin absolu d'un certificat ou d'une clé de test produits par openssl(1).
func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "openssl", name))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func strOf(t *testing.T, z *zone.Zone, v types.Value) string {
	t.Helper()
	v = deref(z, v)
	if v.Type != types.TypeString {
		t.Fatalf("chaîne attendue, %s reçu", ser(z, v))
	}
	return types.StringData(z, v)
}

// hashSample produit des octets déterministes non périodiques sur les longueurs mesurées.
func hashSample(n int) []byte {
	b := make([]byte, n)
	x := uint32(0x9e3779b9)
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return b
}

var hashLengths = func() []int {
	var l []int
	for n := 0; n <= 300; n++ {
		l = append(l, n)
	}
	return append(l, 511, 512, 513, 1023, 1024, 1025, 1026, 1087, 1088, 1089, 1090, 2047, 2048, 2049, 2112, 4096, 4100, 10000)
}()

// Chaque algorithme servi rend, en un appel (hash) puis en flux découpé avec hash_copy à mi-parcours
// (hash_init, hash_update, hash_final), le condensat de /usr/bin/php sur toutes les longueurs de
// hashLengths, dont les frontières de bande et de bloc de xxh3 (240, 1024, 1088). Les listes
// hash_algos() et hash_hmac_algos() suivent l'ordre de PHP restreint aux algorithmes servis.
func TestHashAlgosPHPParity(t *testing.T) {
	data := hashSample(hashLengths[len(hashLengths)-1])
	bin := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(bin, data, 0o644); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range hashAlgos {
		names = append(names, a.name)
	}
	var script strings.Builder
	script.WriteString("<?php\n$d = file_get_contents(" + strconv.Quote(bin) + ");\n")
	script.WriteString("echo implode(',', hash_algos()), \"\\n\", implode(',', hash_hmac_algos()), \"\\n\";\n")
	script.WriteString("foreach ([" + "'" + strings.Join(names, "','") + "'" + "] as $a) foreach ([" + intsCSV(hashLengths) + "] as $n) {\n")
	script.WriteString("  $line = hash($a, substr($d, 0, $n));\n")
	script.WriteString("  if (in_array($a, hash_hmac_algos(), true)) $line .= ' ' . hash_hmac($a, substr($d, 0, $n), 'clé secrète');\n")
	script.WriteString("  echo $line, \"\\n\";\n}\n")
	lines := phpOracle(t, script.String())
	if len(lines) != 2+len(names)*len(hashLengths) {
		t.Fatalf("%d lignes PHP", len(lines))
	}
	served := func(list string, hmacOnly bool) []string {
		var out []string
		for n := range strings.SplitSeq(list, ",") {
			if a, ok := findAlgo(n); ok && (!hmacOnly || a.hmac) {
				out = append(out, n)
			}
		}
		return out
	}
	z := zone.New(1 << 22)
	defer z.Reset(nil)
	gotAlgos := listOf(t, z, mustCall(t, z, "hash_algos"))
	if want := served(lines[0], false); !slices.Equal(gotAlgos, want) || len(want) != len(hashAlgos) {
		t.Fatalf("hash_algos() = %v\nPHP restreint = %v", gotAlgos, want)
	}
	if got, want := listOf(t, z, mustCall(t, z, "hash_hmac_algos")), served(lines[1], true); !slices.Equal(got, want) {
		t.Fatalf("hash_hmac_algos() = %v\nPHP restreint = %v", got, want)
	}
	chunks := []int{1, 7, 64, 1000}
	i := 2
	for _, a := range hashAlgos {
		for k, n := range hashLengths {
			want := strings.Fields(lines[i])
			i++
			z.Reset(nil)
			in := string(data[:n])
			if got := strOf(t, z, mustCall(t, z, "hash", a.name, in)); got != want[0] {
				t.Fatalf("hash(%s, %d octets) = %s, PHP %s", a.name, n, got, want[0])
			}
			if a.hmac {
				if got := strOf(t, z, mustCall(t, z, "hash_hmac", a.name, in, "clé secrète")); got != want[1] {
					t.Fatalf("hash_hmac(%s, %d octets) = %s, PHP %s", a.name, n, got, want[1])
				}
			}
			// Flux : morceaux de taille variable, copie à mi-parcours poursuivie séparément.
			step := chunks[k%len(chunks)]
			ctx := mustCall(t, z, "hash_init", a.name)
			var dup types.Value
			for off := 0; off < n; off += step {
				if dup.Type == types.TypeUndef && off >= n/2 {
					dup = mustCall(t, z, "hash_copy", ctx)
				}
				mustCall(t, z, "hash_update", ctx, in[off:min(off+step, n)])
			}
			if got := strOf(t, z, mustCall(t, z, "hash_final", ctx)); got != want[0] {
				t.Fatalf("hash_init/update/final(%s, %d octets, pas %d) = %s, PHP %s", a.name, n, step, got, want[0])
			}
			if dup.Type != types.TypeUndef {
				// La copie a reçu la première moitié ; la compléter doit redonner le même condensat.
				done := (n/2 + step - 1) / step * step
				mustCall(t, z, "hash_update", dup, in[min(done, n):])
				if got := strOf(t, z, mustCall(t, z, "hash_final", dup)); got != want[0] {
					t.Fatalf("hash_copy(%s, %d octets) = %s, PHP %s", a.name, n, got, want[0])
				}
			}
			if a.hmac && n%37 == 0 {
				hctx := mustCall(t, z, "hash_init", a.name, hashHMACFlag, "clé secrète")
				mustCall(t, z, "hash_update", hctx, in)
				if got := strOf(t, z, mustCall(t, z, "hash_final", hctx)); got != want[1] {
					t.Fatalf("hash_init(%s, HASH_HMAC, %d octets) = %s, PHP %s", a.name, n, got, want[1])
				}
			}
		}
	}
}

func intsCSV(l []int) string {
	s := make([]string, len(l))
	for i, n := range l {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}

func listOf(t *testing.T, z *zone.Zone, v types.Value) []string {
	t.Helper()
	var out []string
	types.ArrayForEach(z, deref(z, v), func(_, item types.Value) bool {
		out = append(out, types.StringData(z, deref(z, item)))
		return true
	})
	return out
}

// Les mêmes opérations sur les contextes, jouées par PHP et par ext, rendent les mêmes lignes : copie
// indépendante, sortie binaire, contexte finalisé refusé, HMAC, erreurs de hash_init.
func TestHashContextPHPParity(t *testing.T) {
	script := `<?php
function e($f) { try { $r = $f(); echo is_string($r) ? bin2hex($r) : var_export($r, true), "\n"; } catch (\Throwable $x) { echo get_class($x), ': ', $x->getMessage(), "\n"; } }
$c = hash_init('sha256'); hash_update($c, 'ab'); $d = hash_copy($c); hash_update($c, 'c');
e(fn() => hash_final($c)); e(fn() => hash_update($d, 'c')); e(fn() => hash_final($d, true));
e(fn() => hash_update($c, 'x')); e(fn() => hash_final($c)); e(fn() => hash_copy($d));
$h = hash_init('ripemd160', HASH_HMAC, 'k'); hash_update($h, 'message'); $h2 = hash_copy($h); hash_update($h2, '!');
e(fn() => hash_final($h)); e(fn() => hash_final($h2));
$x = hash_init('xxh3'); hash_update($x, str_repeat('z', 1500)); $x2 = hash_copy($x); hash_update($x2, str_repeat('y', 700));
e(fn() => hash_final($x)); e(fn() => hash_final($x2));
e(fn() => hash_init('nope')); e(fn() => hash_init('crc32b', HASH_HMAC, 'k')); e(fn() => hash_init('md5', HASH_HMAC));
e(fn() => hash_update('x', 'y')); e(fn() => hash_final(42));
`
	lines := phpOracle(t, script)
	z := zone.New(1 << 20)
	defer z.Reset(nil)
	var got []string
	e := func(v types.Value, err error) {
		switch {
		case err != nil:
			got = append(got, ExceptionClass(err)+": "+err.Error())
		case v.Type == types.TypeString:
			got = append(got, hex.EncodeToString([]byte(types.StringData(z, v))))
		case v.Type == types.TypeTrue:
			got = append(got, "true")
		default:
			got = append(got, ser(z, v))
		}
	}
	call := func(fn string, args ...any) (types.Value, error) { return extCall(t, z, fn, args...) }
	c := mustCall(t, z, "hash_init", "sha256")
	mustCall(t, z, "hash_update", c, "ab")
	d := mustCall(t, z, "hash_copy", c)
	mustCall(t, z, "hash_update", c, "c")
	e(call("hash_final", c))
	e(call("hash_update", d, "c"))
	e(call("hash_final", d, true))
	e(call("hash_update", c, "x"))
	e(call("hash_final", c))
	e(call("hash_copy", d))
	h := mustCall(t, z, "hash_init", "ripemd160", hashHMACFlag, "k")
	mustCall(t, z, "hash_update", h, "message")
	h2 := mustCall(t, z, "hash_copy", h)
	mustCall(t, z, "hash_update", h2, "!")
	e(call("hash_final", h))
	e(call("hash_final", h2))
	x := mustCall(t, z, "hash_init", "xxh3")
	mustCall(t, z, "hash_update", x, strings.Repeat("z", 1500))
	x2 := mustCall(t, z, "hash_copy", x)
	mustCall(t, z, "hash_update", x2, strings.Repeat("y", 700))
	e(call("hash_final", x))
	e(call("hash_final", x2))
	e(call("hash_init", "nope"))
	e(call("hash_init", "crc32b", hashHMACFlag, "k"))
	e(call("hash_init", "md5", hashHMACFlag))
	e(call("hash_update", "x", "y"))
	e(call("hash_final", 42))
	if !slices.Equal(got, lines) {
		t.Fatalf("ext :\n%s\nPHP :\n%s", strings.Join(got, "\n"), strings.Join(lines, "\n"))
	}
}

func passwordCases() []call {
	bcrypt := "$2y$10$" + strings.Repeat("a", 53)
	argon := "$argon2id$v=19$m=65536,t=4,p=1$c2FsdHNhbHQ$aGFzaGhhc2g"
	return []call{
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lnull()}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lstr("2y")}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lstr("2y"), larr(kv(lstr("cost"), lint(10)))}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lstr("2y"), larr(kv(lstr("cost"), lstr("12")))}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lint(1)}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lint(0)}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lint(2)}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lstr("argon2id")}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lstr("nope")}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lstr("2Y")}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lint(99)}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lbool(true)}},
		{fn: "password_needs_rehash", args: []lit{lstr("xx"), lstr("2y")}},
		{fn: "password_needs_rehash", args: []lit{lstr(""), lstr("2y")}},
		{fn: "password_needs_rehash", args: []lit{lstr("$2y$10$short"), lstr("2y")}},
		{fn: "password_needs_rehash", args: []lit{lstr("$2y$12$" + strings.Repeat("a", 53)), lstr("2y")}},
		{fn: "password_needs_rehash", args: []lit{lstr("$2y$12$" + strings.Repeat("a", 53)), lstr("2y"), larr(kv(lstr("cost"), lint(12)))}},
		{fn: "password_needs_rehash", args: []lit{lstr("$2y$1x$" + strings.Repeat("a", 53)), lstr("2y"), larr(kv(lstr("cost"), lint(1)))}},
		{fn: "password_needs_rehash", args: []lit{lstr("$2a$10$" + strings.Repeat("a", 53)), lstr("2y")}},
		{fn: "password_needs_rehash", args: []lit{lstr(argon), lstr("argon2id")}},
		{fn: "password_needs_rehash", args: []lit{lstr(argon), lstr("argon2id"), larr(kv(lstr("memory_cost"), lint(1024)))}},
		{fn: "password_needs_rehash", args: []lit{lstr(argon), lstr("argon2id"), larr(kv(lstr("time_cost"), lint(3)))}},
		{fn: "password_needs_rehash", args: []lit{lstr(argon), lstr("argon2id"), larr(kv(lstr("threads"), lstr("2")))}},
		{fn: "password_needs_rehash", args: []lit{lstr(argon), lstr("argon2i")}},
		{fn: "password_needs_rehash", args: []lit{lstr(argon), lstr("2y")}},
		{fn: "password_needs_rehash", args: []lit{lstr("$argon2id$v=19$m=65536,t=4,p=1"), lstr("argon2id")}},
		{fn: "password_needs_rehash", args: []lit{lstr("$argon2i$v=19$m=65536,t=4,p=1$a$b"), lstr("argon2id")}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), larr()}},
		{fn: "password_needs_rehash", args: []lit{lstr(bcrypt), lstr("2y"), lstr("x")}},
	}
}

func ctypePrintCases() []call {
	var cs []call
	for _, v := range []lit{lstr(""), lstr("abc"), lstr("a\tb"), lstr("~ "), lint(65), lint(32), lint(31), lint(256),
		lint(-1), lint(-96), lint(1000), lnull(), lbool(true), larr(), lstr("\x7f"), lstr("é")} {
		cs = append(cs, call{fn: "ctype_print", args: []lit{v}})
	}
	return cs
}

func mbWP94Cases() []call {
	scrubIn := []string{"a\xffb", "\xe2\x82", "x\xc3\xa9\x80\x81y", "\xf0\x9f\x98", "", "ok"}
	// Lectures d'abord : PHP enchaîne les cas dans un seul processus, les réglages qui suivent persistent.
	cs := []call{{fn: "mb_substitute_character"}, {fn: "mb_substitute_character", args: []lit{lnull()}}}
	for _, s := range scrubIn {
		for _, enc := range []string{"UTF-8", "ASCII", "ISO-8859-1", "Windows-1252", "8bit", "7bit", "utf8"} {
			cs = append(cs, call{fn: "mb_scrub", args: []lit{lstr(s), lstr(enc)}})
		}
		cs = append(cs, call{fn: "mb_scrub", args: []lit{lstr(s)}})
	}
	cs = append(cs, call{fn: "mb_scrub", args: []lit{lstr("a"), lstr("nope")}})
	for _, v := range []lit{lstr("none"), lstr("LONG"), lstr("Entity"), lint(0xfffd), lint(0), lint(-1), lint(0x110000),
		lint(0xd800), lstr("foo"), lstr(""), lbool(true), larr()} {
		cs = append(cs, call{fn: "mb_substitute_character", args: []lit{v}})
	}
	for _, s := range []string{
		"=?UTF-8?B?w6l0w6k=?=", "=?UTF-8?Q?caf=C3=A9_cr=C3=A8me?=", "a =?UTF-8?Q?x?= =?UTF-8?Q?y?= b",
		"=?ISO-8859-1?Q?caf=E9?=", "=?utf-8?b?SGVsbG8=?=\r\n =?utf-8?b?V29ybGQ=?=", "plain text", "=?UTF-8?X?abc?=",
		"=?UNKNOWN?Q?abc?=", "Subject:\r\n folded line", "=?UTF-8?Q?a?=b", "a\tb  c", "=?UTF-8?Q?=E2=82?=",
		"=?windows-1252?Q?=80?=", "=?UTF-8*fr?Q?abc?=", "=?UTF-8?Q?abc", "x=?UTF-8?Q?abc?=y", "=?UTF-8?B?w6k?=",
		"=?us-ascii?Q?A=FF?=", "\r\n x", "a\r\nb", "=?UTF-8?Q?a_b?=  \t =?UTF-8?Q?c?=", "=?UTF-8?Q?x?= =?UTF-8?Q?y?=",
		"=?UTF-8?Q?x?=\r\n\t=?UTF-8?Q?y?=", "=?UTF-8?Q?x?=\r\n  =?UTF-8?Q?y?=", "x\r\n\r\n y", "abc\r\n =?UTF-8?Q?y?=",
		"=?UTF-8?Q?x?=\r\n abc", "=?UTF-8?Q?x?=\n =?UTF-8?Q?y?=", "a\nb", "a\rb", "=?UTF-8?Q?x?=  =?UTF-8?Q?y?=",
		"=?UTF-8?Q?x?= \r\n =?UTF-8?Q?y?=", "=??Q?a?=", "=?UTF-8??a?=", "=?UTF-8?Q??=", "=?UTF-8?q?a=3Fb?=",
		"=?UTF-8?Q?a=3?=", "=?UTF-8?Q?a=zz?=", "=?UTF-8?B?SGVsbG8gV29ybGQ?=", "=?UTF-8?B?SGVs bG8=?=", "?=", "=?",
		"=?UTF-8?", "=?UTF-8?Q", "=?UTF-8?Q?", "a=?b", "=?latin1?Q?=E9?=", "=?ISO8859-1?Q?=E9?=", "=?ASCII?Q?a?=",
		"=?UTF8?Q?=C3=A9?=", "=?UTF-8?Q?a?==?UTF-8?Q?b?=", "=?UTF-8?Q?a?=\r\n", "=?UTF-8?Q?a?= ", " =?UTF-8?Q?a?=",
	} {
		cs = append(cs, call{fn: "mb_decode_mimeheader", args: []lit{lstr(s)}})
	}
	return cs
}

func hashWP94Cases() []call {
	var cs []call
	for _, a := range hashAlgos {
		cs = append(cs, call{fn: "hash", args: []lit{lstr(a.name), lstr("abc")}})
		cs = append(cs, call{fn: "hash", args: []lit{lstr(strings.ToUpper(a.name)), lstr("abc"), lbool(true)}})
	}
	return append(cs,
		call{fn: "hash_hmac", args: []lit{lstr("sha3-256"), lstr("data"), lstr("key")}},
		call{fn: "hash_hmac", args: []lit{lstr("ripemd160"), lstr("data"), lstr("key")}},
		call{fn: "hash_hmac", args: []lit{lstr("xxh3"), lstr("data"), lstr("key")}},
		call{fn: "hash_init", args: []lit{lstr("nope")}},
		call{fn: "hash_init", args: []lit{lstr("crc32b"), lint(1), lstr("k")}},
		call{fn: "hash_init", args: []lit{lstr("md5"), lint(1)}},
		call{fn: "hash_init", args: []lit{lstr("md5"), lint(1), lstr("")}},
		call{fn: "hash_update", args: []lit{lstr("x"), lstr("y")}},
		call{fn: "hash_final", args: []lit{lint(42)}},
		call{fn: "hash_copy", args: []lit{larr()}},
	)
}

func opensslWP94Cases(t *testing.T) []call {
	f := func(n string) lit { return lstr("file://" + fixture(t, n)) }
	var cs []call
	for _, crt := range []string{"ca.crt", "leaf.crt", "tsa.crt", "ec.crt", "v1.crt"} {
		cs = append(cs, call{fn: "openssl_x509_parse", args: []lit{f(crt)}})
		cs = append(cs, call{fn: "openssl_x509_parse", args: []lit{f(crt), lbool(false)}})
	}
	pemCA, err := os.ReadFile(fixture(t, "leaf.crt"))
	if err != nil {
		t.Fatal(err)
	}
	cs = append(cs,
		call{fn: "openssl_x509_parse", args: []lit{lstr(string(pemCA))}},
		call{fn: "openssl_x509_parse", args: []lit{lstr("garbage")}},
	)
	for _, algo := range []lit{lstr("sha256WithRSAEncryption"), lint(opensslAlgoSHA1), lint(opensslAlgoSHA256), lint(opensslAlgoSHA512),
		lint(opensslAlgoMD5), lint(opensslAlgoSHA224), lint(opensslAlgoSHA384), lint(opensslAlgoRMD160), lstr("RSA-SHA384"),
		lstr("md5"), lstr("ripemd160"), lstr("sha3-256"), lstr("SHA512-256"), lstr("nope")} {
		cs = append(cs, call{fn: "openssl_sign", args: []lit{lstr("hello"), lnull(), f("rsa.key"), algo}, refs: []int{1}})
	}
	return append(cs,
		call{fn: "openssl_sign", args: []lit{lstr("hello"), lnull(), f("rsa_trad.key")}, refs: []int{1}},
		call{fn: "openssl_sign", args: []lit{lstr("hello"), lnull(), larr(ap(f("rsa_legacy_aes.key")), ap(lstr("secret"))), lint(opensslAlgoSHA256)}, refs: []int{1}},
		call{fn: "openssl_sign", args: []lit{lstr("hello"), lnull(), larr(ap(f("rsa_pk8_enc.key")), ap(lstr("secret"))), lint(opensslAlgoSHA256)}, refs: []int{1}},
		call{fn: "openssl_sign", args: []lit{lstr("hello"), lnull(), larr(ap(f("rsa_pk8_3des.key")), ap(lstr("secret"))), lint(opensslAlgoSHA256)}, refs: []int{1}},
		call{fn: "openssl_sign", args: []lit{lstr("hello"), lnull(), larr(ap(f("rsa_legacy_3des.key")), ap(lstr("secret"))), lint(opensslAlgoSHA256)}, refs: []int{1}},
		call{fn: "openssl_sign", args: []lit{lstr("x"), lnull(), lint(42)}, refs: []int{1}},
		call{fn: "openssl_sign", args: []lit{lstr("x"), lnull(), lstr("notakey")}, refs: []int{1}},
		call{fn: "openssl_pkey_free", args: []lit{lstr("x")}},
		call{fn: "openssl_pkey_get_private", args: []lit{lstr("garbage")}},
		call{fn: "openssl_pkey_get_private", args: []lit{f("rsa_legacy_aes.key"), lstr("wrong")}},
	)
}

// Différentiel contre /usr/bin/php des natives ajoutées pour WordPress, appel par appel sur une Zone neuve.
func TestPHPDifferentialWPExtensions(t *testing.T) {
	runDifferential(t, []family{
		{"hash_wp", hashWP94Cases()},
		{"password", passwordCases()},
		{"ctype_print", ctypePrintCases()},
		{"mbstring_wp", mbWP94Cases()},
		{"openssl_wp", opensslWP94Cases(t)},
	}, 200)
}

// Le réglage mb_substitute_character d'une Zone gouverne mb_scrub et mb_convert_encoding comme sous PHP.
func TestMBSubstituteSequencePHPParity(t *testing.T) {
	modes := []any{nil, "none", "long", "entity", 0xfffd, 0x3f, 0x41, 0x2603, 0xe9, 0}
	inputs := []string{"a\xffb", "\xe2\x82", "x\xc3\xa9\x80\x81y", "\xf0\x9f\x98"}
	conv := "\xe2\x82\xac\xc3\xa9\xf0\x9f\x98\x80\xff\x01"
	var script strings.Builder
	script.WriteString("<?php\n")
	for _, m := range modes {
		switch v := m.(type) {
		case string:
			script.WriteString("mb_substitute_character('" + v + "');\n")
		case int:
			script.WriteString("mb_substitute_character(" + strconv.Itoa(v) + ");\n")
		}
		script.WriteString("echo var_export(mb_substitute_character(), true), \"\\n\";\n")
		for _, in := range inputs {
			for _, enc := range []string{"UTF-8", "ASCII", "ISO-8859-1", "Windows-1252", "7bit"} {
				script.WriteString("echo bin2hex(mb_scrub(hex2bin('" + hex.EncodeToString([]byte(in)) + "'), '" + enc + "')), \"\\n\";\n")
			}
		}
		for _, enc := range []string{"ASCII", "ISO-8859-1", "Windows-1252", "UTF-8"} {
			script.WriteString("echo bin2hex(mb_convert_encoding(hex2bin('" + hex.EncodeToString([]byte(conv)) + "'), '" + enc + "', 'UTF-8')), \"\\n\";\n")
		}
	}
	lines := phpOracle(t, script.String())
	z := zone.New(1 << 20)
	defer z.Reset(nil)
	var got []string
	for _, m := range modes {
		if m != nil {
			mustCall(t, z, "mb_substitute_character", m)
		}
		v := mustCall(t, z, "mb_substitute_character")
		if v.Type == types.TypeLong {
			got = append(got, strconv.FormatInt(v.ToLong(), 10))
		} else {
			got = append(got, "'"+types.StringData(z, v)+"'")
		}
		for _, in := range inputs {
			for _, enc := range []string{"UTF-8", "ASCII", "ISO-8859-1", "Windows-1252", "7bit"} {
				got = append(got, hex.EncodeToString([]byte(strOf(t, z, mustCall(t, z, "mb_scrub", in, enc)))))
			}
		}
		for _, enc := range []string{"ASCII", "ISO-8859-1", "Windows-1252", "UTF-8"} {
			got = append(got, hex.EncodeToString([]byte(strOf(t, z, mustCall(t, z, "mb_convert_encoding", conv, enc, "UTF-8")))))
		}
	}
	if len(got) != len(lines) {
		t.Fatalf("%d lignes ext, %d PHP", len(got), len(lines))
	}
	for i := range got {
		if got[i] != lines[i] {
			t.Errorf("ligne %d : ext %s, PHP %s", i, got[i], lines[i])
		}
	}
	// L'encodage interne de la Zone est la cible de mb_decode_mimeheader et la source par défaut de mb_scrub.
	mustCall(t, z, "mb_internal_encoding", "ISO-8859-1")
	if got := strOf(t, z, mustCall(t, z, "mb_decode_mimeheader", "=?UTF-8?B?w6l0w6k=?= =?UTF-8?Q?=E2=82=AC?=")); got != "\xe9t\xe9 ?" {
		t.Errorf("mb_decode_mimeheader vers ISO-8859-1 = %q", got)
	}
}

// normCorpus mêle des points de code isolés de la table, des suites aléatoires de lettres, de marques
// combinantes (dont des ordres non canoniques), de jamos et de syllabes hangûl et de caractères de compatibilité.
func normCorpus() []string {
	normOnce.Do(loadNormTables)
	var out []string
	for cp := range norm.compat {
		out = append(out, string(cp), "a"+string(cp)+"\u0301", string(cp)+"\u0323\u0302")
	}
	pool := []rune("aAeEiIoOuUnNcCsSzZyYαΑωΩиИ" +
		"\u0300\u0301\u0302\u0303\u0304\u0306\u0307\u0308\u030a\u030b\u030c\u0323\u0327\u0328\u0331\u0345\u0316\u031b\u0335\u0338\u05b0\u0e38\u0f71" +
		"\u1100\u1161\u11a8\u1112\u1175\u11c2\uac00\ud4db\uac01" +
		"ﬁ①ｶｱﾞ\u00bd\u2126\u212b\u1e9b\u0958\u2000x ")
	r := rand.New(rand.NewPCG(1, 94))
	for range 20000 {
		n := 1 + r.IntN(8)
		var b strings.Builder
		for range n {
			b.WriteRune(pool[r.IntN(len(pool))])
		}
		out = append(out, b.String())
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// NFC, NFD, NFKC et NFKD rendent, sur tout le corpus, les chaînes de unicodedata.normalize de python3,
// source de la table embarquée ; normalizer_is_normalized en est la comparaison.
func TestNormalizerPythonParity(t *testing.T) {
	corpus := normCorpus()
	in := filepath.Join(t.TempDir(), "corpus.jsonl")
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, s := range corpus {
		if err := enc.Encode(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(in, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	py := `import json, sys, unicodedata
for line in open(sys.argv[1], encoding="utf-8"):
    s = json.loads(line)
    print(json.dumps([unicodedata.normalize(f, s) for f in ("NFD", "NFKD", "NFC", "NFKC")]))
`
	cmd := exec.CommandContext(t.Context(), "/usr/bin/python3", "-I", "-c", py, in)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("python3 : %v", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 1<<20)
	forms := []int{normFormD, normFormKD, normFormC, normFormKC}
	z := zone.New(1 << 20)
	defer z.Reset(nil)
	bad := 0
	for i := 0; sc.Scan(); i++ {
		var want []string
		if err := json.Unmarshal(sc.Bytes(), &want); err != nil {
			t.Fatal(err)
		}
		for k, form := range forms {
			got, ok := normalizeString(corpus[i], form)
			if !ok || got != want[k] {
				if bad++; bad <= 20 {
					t.Errorf("forme %d de %+q : %+q, python3 %+q", form, corpus[i], got, want[k])
				}
			}
		}
		if i%97 == 0 {
			v := mustCall(t, z, "normalizer_is_normalized", corpus[i])
			if (v.Type == types.TypeTrue) != (corpus[i] == want[2]) {
				t.Errorf("normalizer_is_normalized(%+q) = %s", corpus[i], ser(z, v))
			}
			z.Reset(nil)
		}
	}
	if bad > 0 {
		t.Fatalf("%d écarts sur %d chaînes", bad, len(corpus))
	}
}

// La table embarquée est la sortie exacte de son générateur.
func TestNormalizerTableRegeneration(t *testing.T) {
	out := filepath.Join(t.TempDir(), "normalizer_table_gen.go")
	cmd := exec.CommandContext(t.Context(), "/usr/bin/python3", "-I", "testdata/gen_normalizer_table.py", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("générateur : %v\n%s", err, b)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("normalizer_table_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("normalizer_table_gen.go diffère de la sortie de testdata/gen_normalizer_table.py")
	}
}

func TestNormalizerNatives(t *testing.T) {
	z := zone.New(1 << 16)
	defer z.Reset(nil)
	if got := strOf(t, z, mustCall(t, z, "normalizer_normalize", "cafe\u0301")); got != "café" {
		t.Errorf("NFC = %q", got)
	}
	if got := strOf(t, z, mustCall(t, z, "normalizer_normalize", "café", normFormD)); got != "cafe\u0301" {
		t.Errorf("NFD = %q", got)
	}
	if got := strOf(t, z, mustCall(t, z, "normalizer_normalize", "ﬁ", normFormKC)); got != "fi" {
		t.Errorf("NFKC = %q", got)
	}
	for _, c := range []struct {
		fn   string
		args []any
	}{
		{"normalizer_normalize", []any{"a\xff"}},
		{"normalizer_normalize", []any{"a", 48}},
		{"normalizer_is_normalized", []any{"a\xff"}},
		{"normalizer_is_normalized", []any{"cafe\u0301"}},
	} {
		if v := mustCall(t, z, c.fn, c.args...); v.Type != types.TypeFalse {
			t.Errorf("%s%q = %s, false attendu", c.fn, c.args, ser(z, v))
		}
	}
	if v := mustCall(t, z, "normalizer_is_normalized", "plain ascii"); v.Type != types.TypeTrue {
		t.Errorf("normalizer_is_normalized(ASCII) = %s", ser(z, v))
	}
}

// Vecteurs calculés par golang.org/x/net/idna v0.61.0 (profil MapForLookup, transitionnel puis non
// transitionnel), oracle indépendant faute d'extension intl dans /usr/bin/php ; les vecteurs (A) et (B)
// de la RFC 3492 §7.1 y figurent.
func TestIdnToASCII(t *testing.T) {
	vectors := []struct{ in, trans, nontrans string }{
		{"bücher.example", "xn--bcher-kva.example", "xn--bcher-kva.example"},
		{"MÜNCHEN.de", "xn--mnchen-3ya.de", "xn--mnchen-3ya.de"},
		{"faß.de", "fass.de", "xn--fa-hia.de"},
		{"straße.de", "strasse.de", "xn--strae-oqa.de"},
		{"ελληνικά.gr", "xn--hxargifdar.gr", "xn--hxargifdar.gr"},
		{"日本語.jp", "xn--wgv71a119e.jp", "xn--wgv71a119e.jp"},
		{"他们为什么不说中文", "xn--ihqwcrb4cv8a8dqg056pqjye", "xn--ihqwcrb4cv8a8dqg056pqjye"},
		{"ليهمابتكلموشعربي؟", "xn--egbpdaj6bu4bxfgehfvwxn", "xn--egbpdaj6bu4bxfgehfvwxn"},
		{"example.com", "example.com", "example.com"},
		{"xn--bcher-kva.example", "xn--bcher-kva.example", "xn--bcher-kva.example"},
		{"Ⅷ.example", "viii.example", "viii.example"},
		{"café.fr", "xn--caf-dma.fr", "xn--caf-dma.fr"},
		{"cafe\u0301.fr", "xn--caf-dma.fr", "xn--caf-dma.fr"},
		{"a.b.c.d", "a.b.c.d", "a.b.c.d"},
		{"例え。テスト", "xn--r8jz45g.xn--zckzah", "xn--r8jz45g.xn--zckzah"},
		{"ｅｘａｍｐｌｅ．ｃｏｍ", "example.com", "example.com"},
		{"ς.gr", "xn--4xa.gr", "xn--3xa.gr"},
	}
	z := zone.New(1 << 16)
	defer z.Reset(nil)
	for _, v := range vectors {
		if got := strOf(t, z, mustCall(t, z, "idn_to_ascii", v.in)); got != v.trans {
			t.Errorf("idn_to_ascii(%q) = %q, attendu %q", v.in, got, v.trans)
		}
		got := strOf(t, z, mustCall(t, z, "idn_to_ascii", v.in, idnaNontransitionalToASCII|idnaUseSTD3Rules, intlIDNAVariantUTS46))
		if got != v.nontrans {
			t.Errorf("idn_to_ascii(%q, NONTRANSITIONAL) = %q, attendu %q", v.in, got, v.nontrans)
		}
	}
	// Erreurs UTS #46 (x/net/idna les relève aussi pour les trois premières) : false.
	for _, in := range []string{"ab--c.de", "-abc.de", "abc-.de", "a..b", "a_b.de", "", strings.Repeat("a", 64) + ".de"} {
		flags := 0
		if in == "a_b.de" {
			flags = idnaUseSTD3Rules
		}
		if v := mustCall(t, z, "idn_to_ascii", in, flags); v.Type != types.TypeFalse {
			t.Errorf("idn_to_ascii(%q) = %s, false attendu", in, ser(z, v))
		}
	}
	if _, err := extCall(t, z, "idn_to_ascii", "a.de", 0, 0); ExceptionClass(err) != "ValueError" {
		t.Errorf("variante 2003 : %v, ValueError attendue", err)
	}
	ref, err := types.NewReference(z, types.NewNull())
	if err != nil {
		t.Fatal(err)
	}
	mustCall(t, z, "idn_to_ascii", "faß.de", idnaNontransitionalToASCII, intlIDNAVariantUTS46, ref)
	if got := ser(z, ref); got != ser(z, idnaInfoValue(t, z, "xn--fa-hia.de", true, 0)) {
		t.Errorf("$idna_info = %s", got)
	}
}

func idnaInfoValue(t *testing.T, z *zone.Zone, res string, diff bool, errs uint32) types.Value {
	t.Helper()
	v, err := idnaInfo(z, res, diff, errs)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Une clé chargée par openssl_pkey_get_private est une ressource de la Zone : elle signe comme sa source
// PEM, ne survit ni à openssl_pkey_free ni à z.Reset ; une phrase de passe fausse dépose les messages
// d'OpenSSL mesurés sur PHP 8.3.6.
func TestOpensslPkeyHandles(t *testing.T) {
	z := zone.New(1 << 20)
	defer z.Reset(nil)
	sigOf := func(key any, algo any) string {
		t.Helper()
		ref, err := types.NewReference(z, types.NewNull())
		if err != nil {
			t.Fatal(err)
		}
		if v := mustCall(t, z, "openssl_sign", "hello", ref, key, algo); v.Type != types.TypeTrue {
			t.Fatalf("openssl_sign(%v) = %s", key, ser(z, v))
		}
		return strOf(t, z, ref)
	}
	want := sigOf("file://"+fixture(t, "rsa_trad.key"), opensslAlgoSHA256)
	for _, c := range []struct{ file, pass string }{
		{"rsa.key", ""}, {"rsa_trad.key", ""}, {"rsa_legacy_aes.key", "secret"}, {"rsa_legacy_3des.key", "secret"},
		{"rsa_pk8_enc.key", "secret"}, {"rsa_pk8_3des.key", "secret"},
	} {
		pemData, err := os.ReadFile(fixture(t, c.file))
		if err != nil {
			t.Fatal(err)
		}
		k := mustCall(t, z, "openssl_pkey_get_private", string(pemData), c.pass)
		if k.Type != types.TypeResource || k.Extra != resourceKindPKey {
			t.Fatalf("openssl_pkey_get_private(%s) = %s", c.file, ser(z, k))
		}
		if got := sigOf(k, "sha256WithRSAEncryption"); got != want {
			t.Errorf("signature par la clé %s différente", c.file)
		}
		mustCall(t, z, "openssl_pkey_free", k)
		ref, _ := types.NewReference(z, types.NewNull())
		if v := mustCall(t, z, "openssl_sign", "hello", ref, k); v.Type != types.TypeFalse {
			t.Errorf("signature par une clé libérée : %s", ser(z, v))
		}
	}
	if v := mustCall(t, z, "openssl_pkey_get_private", "file://"+fixture(t, "rsa_legacy_aes.key"), "wrong"); v.Type != types.TypeFalse {
		t.Fatalf("phrase de passe fausse : %s", ser(z, v))
	}
	var errs []string
	for {
		v := mustCall(t, z, "openssl_error_string")
		if v.Type == types.TypeFalse {
			break
		}
		errs = append(errs, types.StringData(z, v))
	}
	if !slices.Equal(errs, []string{sslErrBadDecrypt, sslErrPEMBadDecrypt}) {
		t.Errorf("file d'erreurs = %q", errs)
	}
	k := mustCall(t, z, "openssl_pkey_get_private", "file://"+fixture(t, "ec.key"))
	z.Reset(nil)
	ref, _ := types.NewReference(z, types.NewNull())
	if v := mustCall(t, z, "openssl_sign", "hello", ref, k); v.Type != types.TypeFalse {
		t.Errorf("clé d'avant z.Reset encore utilisable : %s", ser(z, v))
	}
}

// Les signatures ECDSA (aléatoires) d'ext sont vérifiées par openssl_verify de PHP ; Ed25519 par crypto/ed25519.
func TestOpensslSignECDSAVerifiedByPHP(t *testing.T) {
	z := zone.New(1 << 20)
	defer z.Reset(nil)
	var script strings.Builder
	script.WriteString("<?php\n$pub = openssl_pkey_get_public(file_get_contents(" + strconv.Quote(fixture(t, "ec.crt")) + "));\n")
	for _, key := range []string{"ec.key", "ec_pk8.key"} {
		for _, algo := range []int{opensslAlgoSHA256, opensslAlgoSHA384, opensslAlgoSHA1} {
			ref, _ := types.NewReference(z, types.NewNull())
			if v := mustCall(t, z, "openssl_sign", "données à signer", ref, "file://"+fixture(t, key), algo); v.Type != types.TypeTrue {
				t.Fatalf("openssl_sign(%s, %d) = %s", key, algo, ser(z, v))
			}
			sig := hex.EncodeToString([]byte(strOf(t, z, ref)))
			script.WriteString("echo openssl_verify('données à signer', hex2bin('" + sig + "'), $pub, " + strconv.Itoa(algo) + "), \"\\n\";\n")
		}
	}
	for i, l := range phpOracle(t, script.String()) {
		if l != "1" {
			t.Errorf("signature %d refusée par openssl_verify : %s", i, l)
		}
	}
	pemData, err := os.ReadFile(fixture(t, "ed25519.key"))
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(pemData)
	priv, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := types.NewReference(z, types.NewNull())
	mustCall(t, z, "openssl_sign", "message", ref, string(pemData))
	if !ed25519.Verify(priv.(ed25519.PrivateKey).Public().(ed25519.PublicKey), []byte("message"), []byte(strOf(t, z, ref))) {
		t.Error("signature Ed25519 invalide")
	}
}

// openssl_get_cipher_methods ne liste que des chiffres servis par openssl_encrypt, tous connus de PHP.
func TestOpensslCipherMethodsServed(t *testing.T) {
	lines := phpOracle(t, "<?php echo implode(',', openssl_get_cipher_methods(true)), \"\\n\";")
	php := strings.Split(lines[0], ",")
	z := zone.New(1 << 16)
	defer z.Reset(nil)
	for _, aliases := range []bool{false, true} {
		for _, m := range listOf(t, z, mustCall(t, z, "openssl_get_cipher_methods", aliases)) {
			if !slices.Contains(php, m) {
				t.Errorf("%s absent de PHP", m)
			}
			if _, ok := lookupCipher(m); !ok && !strings.HasPrefix(m, "id-aes") {
				t.Errorf("%s non servi par openssl_encrypt", m)
			}
		}
	}
	if !slices.Contains(listOf(t, z, mustCall(t, z, "openssl_get_cipher_methods")), "aes-256-gcm") {
		t.Error("aes-256-gcm absent (sodium_compat le teste)")
	}
}

// Pilier 1 : toute chaîne dérivée d'une entrée souillée l'est aussi ; une entrée saine ne l'est pas.
func TestTaintPropagation(t *testing.T) {
	z := zone.New(1 << 16)
	defer z.Reset(nil)
	for _, c := range []struct{ fn, in string }{
		{"mb_scrub", "a\xffb"}, {"mb_decode_mimeheader", "=?UTF-8?Q?caf=C3=A9?="},
		{"idn_to_ascii", "bücher.example"}, {"normalizer_normalize", "cafe\u0301"}, {"normalizer_normalize", "x"},
	} {
		for _, tainted := range []bool{false, true} {
			in, err := newStr(z, c.in)
			if err != nil {
				t.Fatal(err)
			}
			if tainted {
				in.Flags |= types.FlagTainted
			}
			out := mustCall(t, z, c.fn, in)
			if got := out.Flags&types.FlagTainted != 0; got != tainted {
				t.Errorf("%s(%q) souillure %v, entrée %v", c.fn, c.in, got, tainted)
			}
		}
	}
}

// Pilier 1 : l'état retenu par ext ne référence pas l'arène ; réécrire l'arène après z.Reset ne le change pas.
func TestExtStateRetainsNoArenaBytes(t *testing.T) {
	z := zone.New(1 << 16)
	mustCall(t, z, "iconv_set_encoding", "input_encoding", "latin1")
	mustCall(t, z, "mb_internal_encoding", "ISO-8859-1")
	h := mustCall(t, z, "hash_init", "sha1", hashHMACFlag, "clé-hmac")
	mustCall(t, z, "hash_update", h, "abc")
	st := extStateFor(z)
	z.Reset(nil)
	for range 64 {
		if _, err := newStr(z, strings.Repeat("\xaa", 512)); err != nil {
			break
		}
	}
	if st.iconvInputEncoding != "latin1" || st.mbInternalEncoding != "ISO-8859-1" {
		t.Fatalf("état altéré par l'arène : %q %q", st.iconvInputEncoding, st.mbInternalEncoding)
	}
	got := hex.EncodeToString(st.hashCtxs[h.DataSlot].h.Sum(nil))
	z2 := zone.New(1 << 16)
	defer z2.Reset(nil)
	if want := strOf(t, z2, mustCall(t, z2, "hash_hmac", "sha1", "abc", "clé-hmac")); got != want {
		t.Fatalf("contexte HMAC altéré : %s, attendu %s", got, want)
	}
	z.Reset(nil)
}
