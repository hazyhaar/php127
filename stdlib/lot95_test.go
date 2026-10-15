package stdlib

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// lot95Call appelle la native name inscrite au registre ; l'absence d'inscription fait échouer le test.
func lot95Call(t *testing.T, z *zone.Zone, name string, args ...types.Value) (types.Value, error) {
	t.Helper()
	fn, ok := Lookup(name)
	if !ok {
		t.Fatalf("Lookup(%q) absent du registre", name)
	}
	return fn(z, nil, args)
}

func lot95Str(t *testing.T, z *zone.Zone, s string, tainted bool) types.Value {
	t.Helper()
	v, err := newStr(z, s)
	if err != nil {
		t.Fatal(err)
	}
	if tainted {
		v.Flags |= types.FlagTainted
	}
	return v
}

func lot95Tainted(z *zone.Zone, v types.Value) bool {
	return types.StringInfo(z, v).Flags&uint32(types.FlagTainted) != 0
}

// L'état système vit dans z.Slot(2) : aucun élément de nettoyage n'est inscrit, z.Reset(nil) ne panique
// pas et efface le slot ; l'état relu après Reset est l'état initial.
func TestSysStateUsesZoneSlotAndResetsWithNilDispatcher(t *testing.T) {
	z := zone.New(1 << 16)
	if _, err := lot95Call(t, z, "ignore_user_abort", types.NewBool(true)); err != nil {
		t.Fatal(err)
	}
	RecordError(z, 2, "avertissement", "f.php", 3)
	setJSONError(z, jsonErrorSyntax)
	if z.Slot(2) == nil {
		t.Fatal("z.Slot(2) vide après inscription de l'état système")
	}
	if n := z.PendingCleanups(); n != 0 {
		t.Fatalf("%d élément(s) de nettoyage inscrit(s) par l'état système, 0 attendu", n)
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("z.Reset(nil) panique : %v", r)
			}
		}()
		z.Reset(nil)
	}()
	if z.Slot(2) != nil {
		t.Fatal("z.Slot(2) non remis à nil par z.Reset")
	}
	if v, _ := lot95Call(t, z, "ignore_user_abort"); v.ToLong() != 0 {
		t.Fatalf("ignore_user_abort après z.Reset = %d, attendu 0", v.ToLong())
	}
	if v, _ := lot95Call(t, z, "error_get_last"); !v.IsNull() {
		t.Fatal("error_get_last après z.Reset non nul")
	}
	if v, _ := lot95Call(t, z, "json_last_error"); v.ToLong() != 0 {
		t.Fatalf("json_last_error après z.Reset = %d, attendu 0", v.ToLong())
	}
}

// Deux Zones tenues par deux goroutines n'échangent aucun état : chacune lit sa propre dernière erreur.
func TestSysStateIsolatedPerZone(t *testing.T) {
	done := make(chan string, 2)
	for _, msg := range []string{"zone-a", "zone-b"} {
		go func() {
			z := zone.New(1 << 16)
			for range 200 {
				RecordError(z, 2, msg, "f.php", 1)
			}
			st, _ := z.Slot(2).(*sysState)
			if st == nil || st.lastErr == nil {
				done <- ""
				return
			}
			done <- st.lastErr.message
		}()
	}
	got := []string{<-done, <-done}
	if !(got[0] == "zone-a" && got[1] == "zone-b" || got[0] == "zone-b" && got[1] == "zone-a") {
		t.Fatalf("états croisés entre Zones : %q", got)
	}
}

func TestLot95StringFunctions(t *testing.T) {
	z := zone.New(1 << 20)
	defer z.Reset(nil)
	str := func(s string) types.Value { return lot95Str(t, z, s, false) }

	t.Run("strripos", func(t *testing.T) {
		for _, c := range []struct {
			hay, needle string
			off         int64
			want        int64 // -1 : false
		}{
			{"aBcAbC", "bc", 0, 4}, {"aBcAbC", "bc", -2, 4}, {"aBcAbC", "BC", -3, 1},
			{"abc", "", -1, 2}, {"abc", "", 0, 3}, {"ABCabc", "A", -4, 0},
			{"abc", "c", 3, -1}, {"abcABC", "B", -5, 1}, {"abc", "abcd", 0, -1},
			{"\xc3\x80BC", "\xc3\xa0b", 0, -1},
		} {
			args := []types.Value{str(c.hay), str(c.needle)}
			if c.off != 0 {
				args = append(args, types.NewLong(c.off))
			}
			v, err := lot95Call(t, z, "strripos", args...)
			if err != nil {
				t.Fatalf("strripos(%q, %q, %d) : %v", c.hay, c.needle, c.off, err)
			}
			got := int64(-1)
			if v.IsLong() {
				got = v.ToLong()
			}
			if got != c.want {
				t.Fatalf("strripos(%q, %q, %d) = %d, attendu %d", c.hay, c.needle, c.off, got, c.want)
			}
		}
		_, err := lot95Call(t, z, "strripos", str("abc"), str("c"), types.NewLong(4))
		if !errors.As(err, new(ValueError)) || !strings.Contains(err.Error(), "strripos(): Argument #3 ($offset) must be contained in argument #1 ($haystack)") {
			t.Fatalf("strripos hors bornes : %v", err)
		}
	})

	t.Run("count_chars", func(t *testing.T) {
		v, err := lot95Call(t, z, "count_chars", str("aab"), types.NewLong(1))
		if err != nil {
			t.Fatal(err)
		}
		keys, vals := arrayItems(z, v)
		if len(keys) != 2 || keys[0].ToLong() != 'a' || vals[0].ToLong() != 2 || keys[1].ToLong() != 'b' || vals[1].ToLong() != 1 {
			t.Fatalf("count_chars mode 1 : %d entrées", len(keys))
		}
		for mode, want := range map[int64]int{0: 256, 2: 254} {
			v, err := lot95Call(t, z, "count_chars", str("aab"), types.NewLong(mode))
			if err != nil {
				t.Fatal(err)
			}
			if n := int(types.ArrayLen(z, v)); n != want {
				t.Fatalf("count_chars mode %d : %d entrées, %d attendues", mode, n, want)
			}
		}
		_, err = lot95Call(t, z, "count_chars", str("a"), types.NewLong(5))
		if !errors.As(err, new(ValueError)) || err.Error() != "count_chars(): Argument #2 ($mode) must be between 0 and 4 (inclusive)" {
			t.Fatalf("count_chars mode 5 : %v", err)
		}
	})

	t.Run("escapeshell null bytes", func(t *testing.T) {
		for name, msg := range map[string]string{
			"escapeshellarg": "escapeshellarg(): Argument #1 ($arg) must not contain any null bytes",
			"escapeshellcmd": "escapeshellcmd(): Argument #1 ($command) must not contain any null bytes",
		} {
			_, err := lot95Call(t, z, name, str("a\x00b"))
			if !errors.As(err, new(ValueError)) || err.Error() != msg {
				t.Fatalf("%s avec octet nul : %v", name, err)
			}
		}
		v, err := lot95Call(t, z, "escapeshellarg", str("it's"))
		if err != nil || types.StringData(z, v) != `'it'\''s'` {
			t.Fatalf("escapeshellarg(it's) = %q, %v", types.StringData(z, v), err)
		}
	})

	// Le bit FlagTainted de l'argument se retrouve sur la chaîne rendue.
	t.Run("taint", func(t *testing.T) {
		for _, c := range []struct {
			name string
			in   string
			more []types.Value
		}{
			{"chop", "ab \n", nil}, {"chop", "x", nil},
			{"quoted_printable_decode", "a=3Db", nil}, {"quoted_printable_encode", "a=b\xc3\xa9", nil},
			{"escapeshellarg", "it's", nil}, {"escapeshellcmd", "a;b", nil},
			{"count_chars", "abca", []types.Value{types.NewLong(3)}},
		} {
			for _, tainted := range []bool{false, true} {
				args := append([]types.Value{lot95Str(t, z, c.in, tainted)}, c.more...)
				v, err := lot95Call(t, z, c.name, args...)
				if err != nil {
					t.Fatalf("%s(%q) : %v", c.name, c.in, err)
				}
				if v.Type != types.TypeString {
					t.Fatalf("%s(%q) : type %d", c.name, c.in, v.Type)
				}
				if got := lot95Tainted(z, v); got != tainted {
					t.Fatalf("%s(%q) marquage = %v, attendu %v", c.name, c.in, got, tainted)
				}
			}
		}
	})
}

// lot95Corpus produit des chaînes déterministes riches en octets significatifs pour l'encodage
// Quoted-Printable, l'échappement shell (guillemets, métacaractères, UTF-8 valide, tronqué, non minimal,
// demi-codets, formes à cinq et six octets) et la recherche insensible à la casse.
func lot95Corpus() []string {
	pieces := []string{
		"a", "A", "b", "B", "z", " ", "=", "\r", "\n", "\r\n", "\t", "'", `"`, "#", "&", ";", "`", "|", "*", "?",
		"~", "<", ">", "^", "(", ")", "[", "]", "{", "}", "$", `\`, "\x00", "\x7f", "\x01", "=3D", "=0a", "=\r\n",
		"= \n", "=zz", "\xc3\xa9", "\xe2\x82\xac", "\xf0\x9f\x98\x80", "\xff", "\xfe", "\xc0\x80", "\xed\xa0\x80",
		"\xf8\x88\x80\x80\x80", "\xfc\x84\x80\x80\x80\x80", "\xf8\x80\x80\x80\x80", "\xe2\x82", "\x80", "\xf4\x90\x80\x80",
	}
	rng := rand.New(rand.NewPCG(9, 5))
	out := []string{"", "x", strings.Repeat("x", 80) + " ", strings.Repeat("\xc3\xa9", 30) + "\n=x \n", "a \r\nb\t\r\n"}
	for range 600 {
		var b strings.Builder
		for range rng.IntN(70) {
			if rng.IntN(10) == 0 {
				b.WriteByte(byte(rng.IntN(256)))
			} else {
				b.WriteString(pieces[rng.IntN(len(pieces))])
			}
		}
		out = append(out, b.String())
	}
	return out
}

// Parité octet à octet avec PHP 8.3 (/usr/bin/php) de quoted_printable_encode, quoted_printable_decode,
// escapeshellarg, escapeshellcmd, chop, count_chars (modes 3 et 4) et strripos sur lot95Corpus.
func TestLot95ParityVsPHP(t *testing.T) {
	const php = "/usr/bin/php"
	if _, err := os.Stat(php); err != nil {
		t.Skip("/usr/bin/php absent")
	}
	corpus := lot95Corpus()
	rng := rand.New(rand.NewPCG(95, 9))
	type probe struct {
		s, needle string
		off       int64
	}
	probes := make([]probe, len(corpus))
	var in bytes.Buffer
	for i, s := range corpus {
		p := probe{s: s}
		if len(s) > 0 && rng.IntN(3) != 0 {
			a := rng.IntN(len(s))
			p.needle = strings.ToUpper(s[a : a+rng.IntN(min(4, len(s)-a)+1)])
		}
		if len(s) > 0 && rng.IntN(2) == 0 {
			p.off = int64(rng.IntN(2*len(s)+1) - len(s))
		}
		probes[i] = p
		in.WriteString(hex.EncodeToString([]byte(p.s)) + " " + hex.EncodeToString([]byte(p.needle)) + "x " + strconv.FormatInt(p.off, 10) + "\n")
	}
	script := `while (($l = fgets(STDIN)) !== false) {
	[$h, $n, $o] = explode(" ", trim($l));
	$s = hex2bin($h); $n = hex2bin(substr($n, 0, -1)); $o = (int)$o;
	$e = function ($f) { try { return bin2hex($f()); } catch (\ValueError $x) { return "E"; } };
	$p = strripos($s, $n, $o);
	echo $e(fn() => quoted_printable_encode($s)), " ", $e(fn() => quoted_printable_decode($s)), " ",
		$e(fn() => escapeshellarg($s)), " ", $e(fn() => escapeshellcmd($s)), " ", $e(fn() => chop($s)), " ",
		$e(fn() => count_chars($s, 3)), " ", $e(fn() => count_chars($s, 4)), " ", $p === false ? "F" : $p, "\n";
}`
	cmd := exec.CommandContext(t.Context(), php, "-n", "-d", "display_errors=stderr", "-r", script)
	cmd.Stdin = &in
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("php : %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != len(probes) {
		for i, l := range lines {
			if strings.Count(l, " ") != 7 {
				t.Fatalf("php : %d lignes pour %d sondes ; ligne %d hors format : %q (précédente %q)", len(lines), len(probes), i, l, lines[max(i-1, 0)])
			}
		}
		t.Fatalf("php : %d lignes pour %d sondes", len(lines), len(probes))
	}
	z := zone.New(1 << 22)
	defer z.Reset(nil)
	enc := func(name string, args ...types.Value) string {
		v, err := lot95Call(t, z, name, args...)
		if errors.As(err, new(ValueError)) {
			return "E"
		}
		if err != nil {
			t.Fatalf("%s : %v", name, err)
		}
		return hex.EncodeToString([]byte(types.StringData(z, v)))
	}
	for i, p := range probes {
		s := lot95Str(t, z, p.s, false)
		v, err := lot95Call(t, z, "strripos", s, lot95Str(t, z, p.needle, false), types.NewLong(p.off))
		pos := "F"
		switch {
		case errors.As(err, new(ValueError)):
			t.Fatalf("strripos(%q, %q, %d) : %v", p.s, p.needle, p.off, err)
		case err != nil:
			t.Fatal(err)
		case v.IsLong():
			pos = strconv.FormatInt(v.ToLong(), 10)
		}
		got := strings.Join([]string{
			enc("quoted_printable_encode", s), enc("quoted_printable_decode", s), enc("escapeshellarg", s),
			enc("escapeshellcmd", s), enc("chop", s), enc("count_chars", s, types.NewLong(3)),
			enc("count_chars", s, types.NewLong(4)), pos,
		}, " ")
		if got != lines[i] {
			t.Fatalf("sonde %d %q (aiguille %q, décalage %d) :\nphp127 %s\nphp    %s", i, p.s, p.needle, p.off, got, lines[i])
		}
	}
}

func TestLot95FileAndImageFunctions(t *testing.T) {
	z := zone.New(1 << 20)
	defer z.Reset(nil)
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.txt")
	exe := filepath.Join(dir, "run.sh")
	png := filepath.Join(dir, "img.bin")
	gif := filepath.Join(dir, "img.gif")
	jpg := filepath.Join(dir, "img.jpg")
	for path, body := range map[string]string{
		plain: "texte", exe: "#!/bin/sh\n", png: "\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR",
		gif: "GIF89a\x01\x00\x01\x00", jpg: "\xff\xd8\xff\xe0\x00\x10JFIF",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	str := func(s string) types.Value { return lot95Str(t, z, s, false) }
	long := func(name string, args ...types.Value) (int64, bool) {
		t.Helper()
		v, err := lot95Call(t, z, name, args...)
		if err != nil {
			t.Fatalf("%s : %v", name, err)
		}
		if v.Type == types.TypeFalse {
			return 0, false
		}
		if !v.IsLong() {
			t.Fatalf("%s : type %d", name, v.Type)
		}
		return v.ToLong(), true
	}
	fi, err := os.Stat(plain)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	if got, ok := long("fileowner", str(plain)); !ok || got != int64(st.Uid) {
		t.Fatalf("fileowner = %d, %v ; attendu %d", got, ok, st.Uid)
	}
	if got, ok := long("filegroup", str(plain)); !ok || got != int64(st.Gid) {
		t.Fatalf("filegroup = %d, %v ; attendu %d", got, ok, st.Gid)
	}
	for _, name := range []string{"fileowner", "filegroup"} {
		for _, p := range []string{filepath.Join(dir, "absent"), "a\x00b", ""} {
			if _, ok := long(name, str(p)); ok {
				t.Fatalf("%s(%q) devrait rendre false", name, p)
			}
		}
	}
	for path, want := range map[string]bool{dir: true, exe: true, plain: false, filepath.Join(dir, "absent"): false, "a\x00b": false} {
		v, err := lot95Call(t, z, "is_executable", str(path))
		if err != nil || v.IsBool() && (v.Type == types.TypeTrue) != want || !v.IsBool() {
			t.Fatalf("is_executable(%q) = %v, %v ; attendu %v", path, v, err, want)
		}
	}
	for path, want := range map[string]int64{png: 3, gif: 1, jpg: 2} {
		if got, ok := long("exif_imagetype", str(path)); !ok || got != want {
			t.Fatalf("exif_imagetype(%s) = %d, %v ; attendu %d", path, got, ok, want)
		}
	}
	for _, p := range []string{plain, dir, filepath.Join(dir, "absent")} {
		if _, ok := long("exif_imagetype", str(p)); ok {
			t.Fatalf("exif_imagetype(%s) devrait rendre false", p)
		}
	}
	if _, err := lot95Call(t, z, "exif_imagetype", str("a\x00b")); !errors.As(err, new(ValueError)) ||
		err.Error() != "exif_imagetype(): Argument #1 ($filename) must not contain any null bytes" {
		t.Fatalf("exif_imagetype avec octet nul : %v", err)
	}
	mimes := map[int64]string{
		-1: "application/octet-stream", 0: "application/octet-stream", 1: "image/gif", 2: "image/jpeg", 3: "image/png",
		4: "application/x-shockwave-flash", 5: "image/psd", 6: "image/bmp", 7: "image/tiff", 8: "image/tiff",
		9: "application/octet-stream", 10: "image/jp2", 11: "application/octet-stream", 12: "application/octet-stream",
		13: "application/x-shockwave-flash", 14: "image/iff", 15: "image/vnd.wap.wbmp", 16: "image/xbm",
		17: "image/vnd.microsoft.icon", 18: "image/webp", 19: "image/avif", 20: "application/octet-stream",
	}
	for typ, want := range mimes {
		v, err := lot95Call(t, z, "image_type_to_mime_type", types.NewLong(typ))
		if err != nil || types.StringData(z, v) != want {
			t.Fatalf("image_type_to_mime_type(%d) = %q, %v ; attendu %q", typ, types.StringData(z, v), err, want)
		}
	}
	if _, err := lot95Call(t, z, "image_type_to_mime_type", str("x")); !errors.As(err, new(TypeError)) {
		t.Fatalf("image_type_to_mime_type(\"x\") : %v", err)
	}
}

// Les sondes réseau n'interrogent aucun serveur DNS : localhost, littéraux IP et refus de syntaxe seulement.
func TestLot95NetFunctions(t *testing.T) {
	z := zone.New(1 << 20)
	defer z.Reset(nil)
	str := func(s string) types.Value { return lot95Str(t, z, s, false) }
	text := func(name string, args ...types.Value) (string, bool) {
		t.Helper()
		v, err := lot95Call(t, z, name, args...)
		if err != nil {
			t.Fatalf("%s : %v", name, err)
		}
		if v.Type == types.TypeFalse {
			return "", false
		}
		return types.StringData(z, v), true
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := text("gethostname"); !ok || got != host {
		t.Fatalf("gethostname = %q, %v ; attendu %q", got, ok, host)
	}
	long := strings.Repeat("a", 256)
	for in, want := range map[string]string{"localhost": "127.0.0.1", "10.1.2.3": "10.1.2.3", long: long} {
		if got, ok := text("gethostbyname", str(in)); !ok || got != want {
			t.Fatalf("gethostbyname(%.20q) = %.20q, %v ; attendu %.20q", in, got, ok, want)
		}
	}
	tainted := lot95Str(t, z, "localhost", true)
	if v, err := lot95Call(t, z, "gethostbyname", tainted); err != nil || !lot95Tainted(z, v) {
		t.Fatalf("gethostbyname perd le marquage : %v", err)
	}
	for _, in := range []string{"nope", "300.1.1.1", "", "fe80::1%eth0"} {
		if got, ok := text("gethostbyaddr", str(in)); ok {
			t.Fatalf("gethostbyaddr(%q) = %q, false attendu", in, got)
		}
	}
	if got, ok := text("gethostbyaddr", str("127.0.0.1")); !ok || got == "" || strings.HasSuffix(got, ".") {
		t.Fatalf("gethostbyaddr(127.0.0.1) = %q, %v", got, ok)
	}
	for name, param := range map[string]string{"gethostbyname": "hostname", "gethostbyaddr": "ip"} {
		_, err := lot95Call(t, z, name, str("a\x00b"))
		if !errors.As(err, new(ValueError)) || err.Error() != name+"(): Argument #1 ($"+param+") must not contain any null bytes" {
			t.Fatalf("%s avec octet nul : %v", name, err)
		}
	}
}

// lot95Dev rend le périphérique (st_dev) de path.
func lot95Dev(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return uint64(fi.Sys().(*syscall.Stat_t).Dev)
}

// Le repli copie de move_uploaded_file crée la destination avec 0666 privé du masque de la Zone (0077),
// non du masque du processus (0022) : 0600 exactement ; à l'inverse, sous un masque processus 0077, le
// masque de Zone 0022 donne 0644 exactement. Une destination existante garde ses droits.
// Le passage par move_uploaded_file franchit un changement de système de fichiers (rename(2) rend EXDEV) ;
// le périphérique de la destination prouve que la copie a servi.
func TestMoveUploadedFileCopyFallbackAppliesZoneUmask(t *testing.T) {
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })
	z := zone.New(1 << 16)
	defer z.Reset(nil)
	z.SetUmask(0o077)
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("octets"), 0o644); err != nil {
		t.Fatal(err)
	}
	perm := func(p string) os.FileMode {
		t.Helper()
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode().Perm()
	}
	dst := filepath.Join(dir, "dst")
	if err := copyFile(z, src, dst); err != nil {
		t.Fatal(err)
	}
	if got := perm(dst); got != 0o600 {
		t.Fatalf("copie créée en %#o, 0600 attendu", got)
	}
	// Masque du processus plus strict que celui de la Zone : seul Chmod rétablit le mode exact 0644.
	syscall.Umask(0o077)
	z.SetUmask(0o022)
	wide := filepath.Join(dir, "large")
	if err := copyFile(z, src, wide); err != nil {
		t.Fatal(err)
	}
	if got := perm(wide); got != 0o644 {
		t.Fatalf("copie sous masque processus 0077 et masque de Zone 0022 créée en %#o, 0644 attendu", got)
	}
	syscall.Umask(0o022)
	z.SetUmask(0o077)
	existing := filepath.Join(dir, "existant")
	if err := os.WriteFile(existing, []byte("ancien contenu plus long"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(z, src, existing); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(existing); err != nil || string(b) != "octets" || perm(existing) != 0o640 {
		t.Fatalf("destination existante : %q, %v, %#o ; contenu tronqué et 0640 attendus", b, err, perm(existing))
	}

	t.Run("move_uploaded_file", func(t *testing.T) {
		shm, err := os.MkdirTemp("/dev/shm", "php127-lot965-")
		if err != nil {
			t.Skipf("/dev/shm indisponible : %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(shm) })
		if lot95Dev(t, shm) == lot95Dev(t, dir) {
			t.Skip("/dev/shm et le répertoire temporaire partagent un système de fichiers : rename(2) ne peut échouer")
		}
		up := filepath.Join(dir, "php-upload")
		if err := os.WriteFile(up, []byte("téléversé"), 0o644); err != nil {
			t.Fatal(err)
		}
		RegisterUploadedFile(z, up)
		to := filepath.Join(shm, "dest")
		v, err := lot95Call(t, z, "move_uploaded_file", lot95Str(t, z, up, false), lot95Str(t, z, to, false))
		if err != nil || v.Type != types.TypeTrue {
			t.Fatalf("move_uploaded_file inter-périphériques = %v, %v ; true attendu", v, err)
		}
		if b, err := os.ReadFile(to); err != nil || string(b) != "téléversé" {
			t.Fatalf("destination : %q, %v", b, err)
		}
		if got := perm(to); got != 0o600 {
			t.Fatalf("destination créée par le repli en %#o, 0600 attendu", got)
		}
		if _, err := os.Stat(up); !os.IsNotExist(err) {
			t.Fatalf("source encore présente : %v", err)
		}

		// S9R2-B1 : destination qui est un répertoire existant sur un autre système de fichiers : move_uploaded_file
		// renvoie false et conserve la source intacte à son chemin d'origine.
		upDirCase := filepath.Join(dir, "php-upload-dir-case")
		if err := os.WriteFile(upDirCase, []byte("ne-pas-perdre"), 0o644); err != nil {
			t.Fatal(err)
		}
		RegisterUploadedFile(z, upDirCase)
		existingDir := filepath.Join(shm, "sub-dir")
		if err := os.Mkdir(existingDir, 0o755); err != nil {
			t.Fatal(err)
		}
		vDir, err := lot95Call(t, z, "move_uploaded_file", lot95Str(t, z, upDirCase, false), lot95Str(t, z, existingDir, false))
		if err != nil || vDir.Type != types.TypeFalse {
			t.Fatalf("move_uploaded_file vers répertoire existant = %v, %v ; false attendu", vDir, err)
		}
		if b, err := os.ReadFile(upDirCase); err != nil || string(b) != "ne-pas-perdre" {
			t.Fatalf("source perdue après échec sur destination-répertoire : %q, %v", b, err)
		}

		// S9R2-B2 : destination en lien symbolique sur un autre système de fichiers sous masque de Zone 0077 :
		// le fichier publié reçoit le mode de la Zone (0600), jamais le 0777 de Lstat sur le lien symbolique.
		upSymCase := filepath.Join(dir, "php-upload-sym-case")
		if err := os.WriteFile(upSymCase, []byte("via-symlink"), 0o644); err != nil {
			t.Fatal(err)
		}
		RegisterUploadedFile(z, upSymCase)
		symDest := filepath.Join(shm, "dest-symlink")
		if err := os.Symlink(filepath.Join(shm, "cible-absente"), symDest); err != nil {
			t.Fatal(err)
		}
		vSym, err := lot95Call(t, z, "move_uploaded_file", lot95Str(t, z, upSymCase, false), lot95Str(t, z, symDest, false))
		if err != nil || vSym.Type != types.TypeTrue {
			t.Fatalf("move_uploaded_file vers lien symbolique = %v, %v ; true attendu", vSym, err)
		}
		if got := perm(symDest); got != 0o600 {
			t.Fatalf("destination remplaçant un lien symbolique créée en %#o, 0600 attendu", got)
		}
	})
}

// Une source impossible à supprimer après copie (répertoire parent en lecture seule : rename(2) échoue,
// la copie réussit, unlink(2) échoue) annule la copie : false, destination absente, chemin encore inscrit.
func TestMoveUploadedFileRemoveFailureUndoesCopy(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignore les droits du répertoire")
	}
	z := zone.New(1 << 16)
	defer z.Reset(nil)
	dir := t.TempDir()
	ro := filepath.Join(dir, "lecture-seule")
	if err := os.Mkdir(ro, 0o755); err != nil {
		t.Fatal(err)
	}
	up := filepath.Join(ro, "up")
	if err := os.WriteFile(up, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o755) })
	RegisterUploadedFile(z, up)
	to := filepath.Join(dir, "dest")
	v, err := lot95Call(t, z, "move_uploaded_file", lot95Str(t, z, up, false), lot95Str(t, z, to, false))
	if err != nil || v.Type != types.TypeFalse {
		t.Fatalf("move_uploaded_file avec source non supprimable = %v, %v ; false attendu", v, err)
	}
	if _, err := os.Stat(to); !os.IsNotExist(err) {
		t.Fatalf("copie laissée en place : %v", err)
	}
	if _, err := os.Stat(up); err != nil {
		t.Fatalf("source perdue : %v", err)
	}
	if _, ok := sysStatePeek(z).uploads[up]; !ok {
		t.Fatal("chemin retiré après échec")
	}

	// Destination préexistante dans un répertoire accessible en écriture : ni écrasée ni dupliquée si la source ne peut être supprimée.
	existing := filepath.Join(dir, "dest-existante")
	if err := os.WriteFile(existing, []byte("contenu-initial"), 0o640); err != nil {
		t.Fatal(err)
	}
	v2, err := lot95Call(t, z, "move_uploaded_file", lot95Str(t, z, up, false), lot95Str(t, z, existing, false))
	if err != nil || v2.Type != types.TypeFalse {
		t.Fatalf("move_uploaded_file vers destination existante = %v, %v ; false attendu", v2, err)
	}
	if b, err := os.ReadFile(existing); err != nil || string(b) != "contenu-initial" {
		t.Fatalf("destination existante altérée : %q, %v", b, err)
	}

	// Destination préexistante accessible en écriture dans un répertoire parent non modifiable (0555) : aucun double exemplaire.
	roDestDir := filepath.Join(dir, "dest-ro")
	if err := os.Mkdir(roDestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	roDest := filepath.Join(roDestDir, "cible")
	if err := os.WriteFile(roDest, []byte("intact"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(roDestDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDestDir, 0o755) })
	v3, err := lot95Call(t, z, "move_uploaded_file", lot95Str(t, z, up, false), lot95Str(t, z, roDest, false))
	if err != nil || v3.Type != types.TypeFalse {
		t.Fatalf("move_uploaded_file vers répertoire destination non modifiable = %v, %v ; false attendu", v3, err)
	}
	if b, err := os.ReadFile(roDest); err != nil || string(b) != "intact" {
		t.Fatalf("destination dans répertoire non modifiable altérée : %q, %v", b, err)
	}

	// S9R4-M1 & S9R4-B1 : épreuve directe de publishOrRestoreStage après suppression effective de la source :
	// 1. La source rwUp est supprimée, son contenu est dans stageFile (même mis en mode 0000), et la
	//    publication finale échoue (to désigne un sous-répertoire existant) : publishOrRestoreStage restaure
	//    rwUp à partir du descripteur ouvert, avec son mode 0640 et ses octets, et supprime stagePath.
	rwSrcDir := filepath.Join(dir, "src-rw")
	if err := os.Mkdir(rwSrcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rwDestDir := filepath.Join(dir, "dest-rw")
	if err := os.Mkdir(rwDestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rwUp := filepath.Join(rwSrcDir, "up-rw")
	if err := os.WriteFile(rwUp, []byte("contenu-rw"), 0o640); err != nil {
		t.Fatal(err)
	}
	stageFile, err := os.CreateTemp(rwDestDir, ".php127-upload-*")
	if err != nil {
		t.Fatal(err)
	}
	stagePath := stageFile.Name()
	if _, err := stageFile.Write([]byte("contenu-rw")); err != nil {
		t.Fatal(err)
	}
	if err := stageFile.Chmod(0o000); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(rwUp); err != nil {
		t.Fatal(err)
	}
	unrenamableDest := filepath.Join(rwDestDir, "sous-dir")
	if err := os.Mkdir(unrenamableDest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := publishOrRestoreStage(stageFile, stagePath, rwUp, unrenamableDest, 0o640); err == nil {
		t.Fatal("publishOrRestoreStage vers un répertoire existant aurait dû échouer")
	}
	if b, err := os.ReadFile(rwUp); err != nil || string(b) != "contenu-rw" {
		t.Fatalf("source restaurée = %q, %v ; 'contenu-rw' attendu", b, err)
	}
	if fi, err := os.Stat(rwUp); err != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode de la source restaurée = %v, %v ; 0640 attendu", fi, err)
	}
	if _, err := os.Stat(stagePath); !os.IsNotExist(err) {
		t.Fatalf("stagePath aurait dû être supprimé après restauration réussie : %v", err)
	}

	// 2. Si la restauration de la source échoue à son tour (par exemple un répertoire a pris la place de from),
	//    publishOrRestoreStage retourne l'erreur jointe et conserve impérativement keepPath qui détient le
	//    dernier exemplaire des octets téléversés.
	stageKeep, err := os.CreateTemp(rwDestDir, ".php127-upload-*")
	if err != nil {
		t.Fatal(err)
	}
	keepPath := stageKeep.Name()
	if _, err := stageKeep.Write([]byte("dernier-exemplaire")); err != nil {
		t.Fatal(err)
	}
	blockedFrom := filepath.Join(rwSrcDir, "dir-bloquant")
	if err := os.Mkdir(blockedFrom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := publishOrRestoreStage(stageKeep, keepPath, blockedFrom, unrenamableDest, 0o640); err == nil {
		t.Fatal("publishOrRestoreStage avec double échec aurait dû retourner une erreur")
	}
	if b, err := os.ReadFile(keepPath); err != nil || string(b) != "dernier-exemplaire" {
		t.Fatalf("dernier exemplaire détruit dans keepPath : %q, %v", b, err)
	}
	_ = os.Remove(keepPath)

	// 3. S9R5-M1 : une erreur de fermeture du descripteur stage après publication n'est pas ignorée :
	//    publishOrRestoreStage retourne une erreur non nulle.
	stageClosed, err := os.CreateTemp(rwDestDir, ".php127-upload-*")
	if err != nil {
		t.Fatal(err)
	}
	closedPath := stageClosed.Name()
	if _, err := stageClosed.Write([]byte("octets-publies")); err != nil {
		t.Fatal(err)
	}
	if err := stageClosed.Close(); err != nil {
		t.Fatal(err)
	}
	pubDest := filepath.Join(rwDestDir, "cible-publiee")
	if err := publishOrRestoreStage(stageClosed, closedPath, rwUp, pubDest, 0o640); err == nil {
		t.Fatal("publishOrRestoreStage avec descripteur déjà fermé aurait dû propager l'erreur de Close()")
	}
	_ = os.Remove(pubDest)

	// 4. S9R6-M1 : si le répertoire destination devient non modifiable (0555) après la création de stage
	//    tandis que le répertoire source permet la restauration, publishOrRestoreStage échoue au renommage,
	//    restaure intégralement la source depuis le descripteur ouvert, et joint l'erreur d'os.Remove(stageName)
	//    au diagnostic retourné (deux *os.PathError distincts : rename et remove).
	if err := os.Remove(rwUp); err != nil {
		t.Fatal(err)
	}
	stageLockedDir := filepath.Join(dir, "dest-locked-after-stage")
	if err := os.Mkdir(stageLockedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stageInLocked, err := os.CreateTemp(stageLockedDir, ".php127-upload-*")
	if err != nil {
		t.Fatal(err)
	}
	lockedStagePath := stageInLocked.Name()
	if _, err := stageInLocked.Write([]byte("octets-restaures")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stageLockedDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stageLockedDir, 0o755) })
	jerr := publishOrRestoreStage(stageInLocked, lockedStagePath, rwUp, filepath.Join(stageLockedDir, "cible"), 0o640)
	if jerr == nil {
		t.Fatal("publishOrRestoreStage dans un répertoire destination 0555 aurait dû échouer")
	}
	if !strings.Contains(jerr.Error(), "remove "+lockedStagePath) {
		t.Fatalf("erreur retournée = %q ; l'erreur de suppression de %s devait être jointe", jerr.Error(), lockedStagePath)
	}
	if b, err := os.ReadFile(rwUp); err != nil || string(b) != "octets-restaures" {
		t.Fatalf("source restaurée après S9R6-M1 = %q, %v", b, err)
	}

	// 5. S9R7-M1 : jointure inconditionnelle des erreurs secondaires dans stageCopyFrom et restoreCopyTo
	//    lorsqu'une copie échoue et que la fermeture / Chmod / Remove échouent également.
	closedSrc, err := os.Open(rwUp)
	if err != nil {
		t.Fatal(err)
	}
	_ = closedSrc.Close()
	stageDst, err := os.CreateTemp(rwDestDir, ".php127-upload-*")
	if err != nil {
		t.Fatal(err)
	}
	stageDstPath := stageDst.Name()
	scErr := stageCopyFrom(stageDst, closedSrc, 0o600)
	_ = stageDst.Close()
	_ = os.Remove(stageDstPath)
	if scErr == nil || strings.Count(scErr.Error(), "file already closed") < 2 {
		t.Fatalf("stageCopyFrom sur descripteur source fermé = %v ; attendu les deux causes jointes (read + close)", scErr)
	}

	closedOut, err := os.CreateTemp(rwSrcDir, ".php127-restore-*")
	if err != nil {
		t.Fatal(err)
	}
	closedOutPath := closedOut.Name()
	_ = closedOut.Close()
	_ = os.Remove(closedOutPath)
	openStage, err := os.Open(rwUp)
	if err != nil {
		t.Fatal(err)
	}
	rcErr := restoreCopyTo(closedOut, openStage, closedOutPath, 0o600)
	_ = openStage.Close()
	if rcErr == nil || strings.Count(rcErr.Error(), "file already closed") < 2 || !strings.Contains(rcErr.Error(), "remove "+closedOutPath) {
		t.Fatalf("restoreCopyTo sur descripteur cible fermé = %v ; attendu les causes jointes (chmod + close + remove)", rcErr)
	}
}

// Les chemins relatifs de fileowner, filegroup, is_executable, exif_imagetype, is_uploaded_file et
// move_uploaded_file se résolvent contre le répertoire courant de la Zone, jamais contre celui du processus.
func TestStdlibFileNativesUseZoneCwd(t *testing.T) {
	if _, err := os.Stat("sub"); !os.IsNotExist(err) {
		t.Fatalf("« sub » existe dans le répertoire courant du processus : la sonde ne discriminerait pas (%v)", err)
	}
	z := zone.New(1 << 20)
	defer z.Reset(nil)
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"f": "#!/bin/sh\n", "img.png": "\x89PNG\r\n\x1a\n\x00\x00\x00\x0dIHDR", "up": "téléversé"} {
		if err := os.WriteFile(filepath.Join(sub, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(sub, "f"), 0o755); err != nil {
		t.Fatal(err)
	}
	z.SetCwd(dir)
	str := func(s string) types.Value { return lot95Str(t, z, s, false) }
	call := func(name string, args ...types.Value) types.Value {
		t.Helper()
		v, err := lot95Call(t, z, name, args...)
		if err != nil {
			t.Fatalf("%s : %v", name, err)
		}
		return v
	}
	fi, err := os.Stat(filepath.Join(sub, "f"))
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	if v := call("fileowner", str("sub/f")); !v.IsLong() || v.ToLong() != int64(st.Uid) {
		t.Fatalf("fileowner(\"sub/f\") = %v ; %d attendu", v, st.Uid)
	}
	if v := call("filegroup", str("sub/f")); !v.IsLong() || v.ToLong() != int64(st.Gid) {
		t.Fatalf("filegroup(\"sub/f\") = %v ; %d attendu", v, st.Gid)
	}
	if v := call("is_executable", str("sub/f")); v.Type != types.TypeTrue {
		t.Fatalf("is_executable(\"sub/f\") = %v ; true attendu", v)
	}
	if v := call("exif_imagetype", str("sub/img.png")); !v.IsLong() || v.ToLong() != 3 {
		t.Fatalf("exif_imagetype(\"sub/img.png\") = %v ; 3 attendu", v)
	}
	RegisterUploadedFile(z, "sub/up")
	for _, p := range []string{"sub/up", "./sub/../sub/up", filepath.Join(sub, "up")} {
		if v := call("is_uploaded_file", str(p)); v.Type != types.TypeTrue {
			t.Fatalf("is_uploaded_file(%q) = %v ; true attendu", p, v)
		}
	}
	if v := call("move_uploaded_file", str("sub/up"), str("sub/dst")); v.Type != types.TypeTrue {
		t.Fatalf("move_uploaded_file(\"sub/up\", \"sub/dst\") = %v ; true attendu", v)
	}
	if b, err := os.ReadFile(filepath.Join(sub, "dst")); err != nil || string(b) != "téléversé" {
		t.Fatalf("destination dans le répertoire de la Zone : %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(sub, "up")); !os.IsNotExist(err) {
		t.Fatalf("source encore présente : %v", err)
	}
	if _, err := os.Stat("sub"); !os.IsNotExist(err) {
		t.Fatalf("une écriture a visé le répertoire courant du processus : %v", err)
	}
}
