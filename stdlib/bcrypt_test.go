package stdlib

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// bcryptPHP exécute un script par /usr/bin/php et rend sa sortie ; le test est sauté sans PHP de référence.
func bcryptPHP(t *testing.T, script string, args ...string) string {
	t.Helper()
	const php = "/usr/bin/php"
	if _, err := os.Stat(php); err != nil {
		t.Skip("/usr/bin/php absent")
	}
	out, err := exec.CommandContext(t.Context(), php, append([]string{"-r", script, "--"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("php : %v\n%s", err, out)
	}
	return string(out)
}

// password_hash, password_verify et crypt calculent le vrai bcrypt : vecteurs de référence OpenBSD/PHP
// (dont la contre-mesure « $2a$ » sur un octet 8 bits), interopérabilité dans les deux sens avec PHP 8.3,
// option « cost » et refus des coûts hors de 4..31.
func TestBcryptParityWithPHP83AndVectors(t *testing.T) {
	z := zone.New(1 << 20)
	defer z.Reset(nil)
	str := func(s string) types.Value { return lot95Str(t, z, s, false) }
	verify := func(pw, h string) bool {
		t.Helper()
		v, err := lot95Call(t, z, "password_verify", str(pw), str(h))
		if err != nil {
			t.Fatalf("password_verify : %v", err)
		}
		return v.Type == types.TypeTrue
	}
	crypt := func(pw, salt string) string {
		t.Helper()
		v, err := lot95Call(t, z, "crypt", str(pw), str(salt))
		if err != nil {
			t.Fatalf("crypt : %v", err)
		}
		return types.StringData(z, v)
	}
	hash := func(pw string, args ...types.Value) (string, error) {
		t.Helper()
		v, err := lot95Call(t, z, "password_hash", append([]types.Value{str(pw)}, args...)...)
		if err != nil {
			return "", err
		}
		return types.StringData(z, v), nil
	}
	options := func(cost int64) types.Value {
		t.Helper()
		a, err := newArray(z)
		if err != nil {
			t.Fatal(err)
		}
		if err := setKey(z, &a, str("cost"), types.NewLong(cost)); err != nil {
			t.Fatal(err)
		}
		return a
	}

	const rasmus = "$2a$07$usesomesillystringfore2uDLvp1Ii2e./U9C8sBjqp8I90dH6hi"
	if !verify("rasmuslerdorf", rasmus) {
		t.Fatal("password_verify(\"rasmuslerdorf\", vecteur de référence) = false")
	}
	if verify("rasmuslerdorF", rasmus) {
		t.Fatal("password_verify accepte un mot de passe faux")
	}
	if got := crypt("rasmuslerdorf", "$2a$07$usesomesillystringforsalt$"); got != rasmus {
		t.Fatalf("crypt = %q ; %q attendu", got, rasmus)
	}
	// Vecteurs relevés sur PHP 8.3.6 : contre-mesure « $2a$ » (« \xff\xff\xa3 » diffère entre « $2a$ » et « $2y$ »),
	// extension de signe de « $2x$ », sous-types « $2b$ » et « $2y$ », réglages refusés.
	for _, c := range []struct{ pw, salt, want string }{
		{"\xa3", "$2a$05$/OK.fbVrR/bpIqNJ5ianF.Sa7shbm4.OzKpvFnX1pQLmQW96oUlCq", "$2a$05$/OK.fbVrR/bpIqNJ5ianF.Sa7shbm4.OzKpvFnX1pQLmQW96oUlCq"},
		{"\xff\xff\xa3", "$2a$05$/OK.fbVrR/bpIqNJ5ianF.", "$2a$05$/OK.fbVrR/bpIqNJ5ianF.nqd1wy.pTMdcvrRWxyiGL2eMz.2a85."},
		{"\xff\xff\xa3", "$2y$05$/OK.fbVrR/bpIqNJ5ianF.", "$2y$05$/OK.fbVrR/bpIqNJ5ianF.CE5elHaaO4EbggVDjb8P19RukzXSM3e"},
		{"\xd1\x91", "$2x$05$/OK.fbVrR/bpIqNJ5ianF.", "$2x$05$/OK.fbVrR/bpIqNJ5ianF.0yXpIXmnfqDjOFVOF43llu.gQI61.F6"},
		{"\xd1\x91", "$2b$05$/OK.fbVrR/bpIqNJ5ianF.", "$2b$05$/OK.fbVrR/bpIqNJ5ianF.E737eUK7jOqGXQUPcu5iAm8pR815Cru"},
		{"\xff\xa3abc", "$2b$04$usesomesillystringforsalt$", "$2b$04$usesomesillystringforeZ20BZHnUqKdBoojr4zQCoNHfBl1xs46"},
		{"x", "$2y$04$usesomesillystringfors", "$2y$04$usesomesillystringforeSQy1OSD57qcV.sz00WNOaZXUTmm8OK6"},
		{"x", "$2y$04$usesomesillystringfor", "*0"},
		{"x", "$2y$03$usesomesillystringfors", "*0"},
		{"x", "$2y$32$usesomesillystringfors", "*0"},
		{"x", "$2c$04$usesomesillystringfors", "*0"},
		{"x", "$2y$04$usesomesillystringfo?s", "*0"},
	} {
		if got := crypt(c.pw, c.salt); got != c.want {
			t.Fatalf("crypt(%q, %q) = %q ; %q attendu", c.pw, c.salt, got, c.want)
		}
	}
	if crypt("a\x00b", "$2y$04$usesomesillystringfors") != crypt("a", "$2y$04$usesomesillystringfors") {
		t.Fatal("crypt ne s'arrête pas au premier octet nul de la clé")
	}
	if crypt(strings.Repeat("a", 72), "$2y$04$usesomesillystringfors") != crypt(strings.Repeat("a", 80), "$2y$04$usesomesillystringfors") {
		t.Fatal("bcrypt lit la clé au-delà de 72 octets")
	}

	h4, err := hash("secret127!", types.NewLong(1), options(4))
	if err != nil || !strings.HasPrefix(h4, "$2y$04$") || len(h4) != 60 {
		t.Fatalf("password_hash coût 4 = %q, %v", h4, err)
	}
	h10, err := hash("secret127!", str("2y"))
	if err != nil || !strings.HasPrefix(h10, "$2y$10$") || len(h10) != 60 {
		t.Fatalf("password_hash coût par défaut = %q, %v", h10, err)
	}
	h5, err := hash("secret127!", types.NewNull(), options(5))
	if err != nil || !strings.HasPrefix(h5, "$2y$05$") {
		t.Fatalf("password_hash ['cost' => 5] = %q, %v", h5, err)
	}
	for _, h := range []string{h4, h10, h5} {
		if !verify("secret127!", h) || verify("secret127?", h) {
			t.Fatalf("password_verify sur %q incohérent", h)
		}
	}
	for _, cost := range []int64{3, 32} {
		_, err := hash("x", types.NewLong(1), options(cost))
		want := "Invalid bcrypt cost parameter specified: " + map[int64]string{3: "3", 32: "32"}[cost]
		if !errors.As(err, new(ValueError)) || err.Error() != want {
			t.Fatalf("password_hash coût %d : %v ; ValueError %q attendue", cost, err, want)
		}
	}
	if _, err := hash("x", types.NewLong(2)); !errors.As(err, new(ValueError)) ||
		err.Error() != "password_hash(): Argument #2 ($algo) must be a valid password hashing algorithm" {
		t.Fatalf("password_hash algorithme 2 : %v", err)
	}
	if _, err := hash("a\x00b", types.NewLong(1)); !errors.As(err, new(ValueError)) ||
		err.Error() != "Bcrypt password must not contain null character" {
		t.Fatalf("password_hash avec octet nul : %v", err)
	}

	// Dans les deux sens avec PHP 8.3 : condensats de php127 vérifiés par PHP, condensat de PHP vérifié ici.
	got := bcryptPHP(t, `foreach (array_slice($argv, 1) as $h) { echo var_export(password_verify("secret127!", $h), true), "\n"; }`, h4, h10)
	if got != "true\ntrue\n" {
		t.Fatalf("password_verify de PHP sur les condensats php127 : %q", got)
	}
	phpHash := bcryptPHP(t, `echo password_hash("secret127!", PASSWORD_BCRYPT, ["cost" => 5]);`)
	if !strings.HasPrefix(phpHash, "$2y$05$") || !verify("secret127!", phpHash) {
		t.Fatalf("password_verify de php127 sur le condensat PHP %q = false", phpHash)
	}
	if phpCrypt := bcryptPHP(t, `echo crypt("secret127!", $argv[1]);`, h5); phpCrypt != h5 {
		t.Fatalf("crypt de PHP recalcule %q ; %q attendu", phpCrypt, h5)
	}

	// password_get_info : comparaison directe avec PHP 8.3 sur bcrypt ($2y$), variantes ($2a$, $2b$, $2x$), argon2 et inconnu.
	for _, sample := range []string{
		h4,
		h10,
		"$2a$07$usesomesillystringfore2uDLvp1Ii2e./U9C8sBjqp8I90dH6hi",
		"$2b$12$usesomesillystringfore2uDLvp1Ii2e./U9C8sBjqp8I90dH6hi",
		"$2y$xx$" + strings.Repeat("a", 53),
		"$argon2id$v=19$m=65536,t=4,p=1$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG",
		"$argon2i$v=19$m=1024,t=2,p=3$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG",
		"not-a-hash",
		"",
	} {
		infoVal, err := lot95Call(t, z, "password_get_info", str(sample))
		if err != nil {
			t.Fatalf("password_get_info(%q) : %v", sample, err)
		}
		gotJSON, err := lot95Call(t, z, "json_encode", infoVal)
		if err != nil {
			t.Fatalf("json_encode(password_get_info(%q)) : %v", sample, err)
		}
		wantJSON := bcryptPHP(t, `echo json_encode(password_get_info($argv[1]));`, sample)
		if types.StringData(z, gotJSON) != wantJSON {
			t.Fatalf("password_get_info(%q) = %s ; PHP 8.3 = %s", sample, types.StringData(z, gotJSON), wantJSON)
		}
	}
}
