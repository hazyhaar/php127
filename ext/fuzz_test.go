package ext

import (
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// fuzzEncodings couvre les noms d'encodage reconnus, un alias, un nom inconnu et l'absence d'argument.
var fuzzEncodings = []string{"UTF-8", "ISO-8859-1", "ASCII", "Windows-1252", "utf8", "8bit", "nope", ""}

// fuzzCall appelle une native et échoue sur toute panique ; une erreur PHP (TypeError, ValueError) est un
// résultat admis.
func fuzzCall(t *testing.T, z *zone.Zone, fn string, args ...types.Value) types.Value {
	t.Helper()
	f, ok := Lookup(fn)
	if !ok {
		t.Fatalf("Lookup(%s)", fn)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s : panique %v", fn, r)
		}
	}()
	v, err := f(z, nil, args)
	if err != nil {
		return types.NewNull()
	}
	return v
}

// resetFuzzZone rembobine la Zone d'une entrée, sans répartiteur comme probebench, puis exige qu'aucun
// octet n'y reste vivant et que son slot d'état d'ext soit vide. L'état vivant dans la Zone, une Zone
// abandonnée ne laisse rien derrière elle (la table globale d'avant le slot retenait chaque Zone de
// fuzzing jusqu'à l'OOM du scope à 8 Go).
func resetFuzzZone(t *testing.T, z *zone.Zone) {
	t.Helper()
	z.Reset(nil)
	if n := z.LiveBytes(); n != 0 {
		t.Fatalf("%d octets vivants après z.Reset", n)
	}
	if z.Slot(zone.SlotExt) != nil {
		t.Fatal("état ext retenu après z.Reset")
	}
}

func fuzzStr(t *testing.T, z *zone.Zone, s string) types.Value {
	t.Helper()
	v, err := newStr(z, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func FuzzIconvMimeDecode(f *testing.F) {
	f.Add([]byte("=?UTF-8?B?w6l0w6k=?="), int64(0), uint8(0))
	f.Add([]byte("Subject: =?ISO-8859-1?Q?caf=E9?= suite"), int64(2), uint8(0))
	f.Add([]byte("=?UTF-8?Q?a_b?= =?UTF-8?Q?c?="), int64(1), uint8(1))
	f.Add([]byte("=?\xff?B?====?="), int64(0), uint8(6))
	f.Add([]byte("=?UTF-8?B?"), int64(3), uint8(7))
	f.Add([]byte("=?utf-8?q?=C3=A9=?="), int64(-1), uint8(3))
	z := zone.New(1 << 20)
	f.Fuzz(func(t *testing.T, data []byte, mode int64, enc uint8) {
		defer resetFuzzZone(t, z)
		args := []types.Value{fuzzStr(t, z, string(data)), types.NewLong(mode)}
		if name := fuzzEncodings[int(enc)%len(fuzzEncodings)]; name != "" {
			args = append(args, fuzzStr(t, z, name))
		}
		out := fuzzCall(t, z, "iconv_mime_decode", args...)
		if out.Type != types.TypeString && out.Type != types.TypeFalse && out.Type != types.TypeNull {
			t.Fatalf("iconv_mime_decode rend le type %d", out.Type)
		}
	})
}

func FuzzMbFunctions(f *testing.F) {
	f.Add([]byte("aé你🙂b"), []byte("你"), int64(1), int64(2), uint8(0))
	f.Add([]byte("\x80\x81abc\xe2\x82"), []byte("\x80"), int64(-3), int64(-1), uint8(0))
	f.Add([]byte("\xf4\x8f\xbf\xbfz\xf5\x80"), []byte("z"), int64(2), int64(4), uint8(1))
	f.Add([]byte(strings.Repeat("abcdefgh", 9)+"你好"), []byte("h你"), int64(70), int64(9), uint8(4))
	f.Add([]byte("ÉtéÉTÉ"), []byte("é"), int64(0), int64(-100), uint8(2))
	f.Add([]byte(""), []byte(""), int64(0), int64(0), uint8(7))
	z := zone.New(1 << 21)
	f.Fuzz(func(t *testing.T, sb, nb []byte, start, length int64, enc uint8) {
		defer resetFuzzZone(t, z)
		s, needle := string(sb), string(nb)
		hv, nv := fuzzStr(t, z, s), fuzzStr(t, z, needle)
		st, ln := types.NewLong(start), types.NewLong(length)
		withEnc := func(args ...types.Value) []types.Value {
			if name := fuzzEncodings[int(enc)%len(fuzzEncodings)]; name != "" {
				return append(args, fuzzStr(t, z, name))
			}
			return args
		}
		fuzzCall(t, z, "mb_strlen", withEnc(hv)...)
		fuzzCall(t, z, "mb_substr", withEnc(hv, st, ln)...)
		cut := fuzzCall(t, z, "mb_strcut", withEnc(hv, st, ln)...)
		if cut.Type == types.TypeString && !strings.Contains(s, types.StringData(z, cut)) {
			t.Fatalf("mb_strcut(%q, %d, %d) = %q, absent de la chaîne", s, start, length, types.StringData(z, cut))
		}
		off := types.NewLong(start % (int64(len(s)) + 1))
		for _, fn := range []string{"mb_strpos", "mb_strrpos", "mb_stripos", "mb_strripos"} {
			fuzzCall(t, z, fn, withEnc(hv, nv, off)...)
		}
		for _, fn := range []string{"mb_strstr", "mb_stristr", "mb_strrchr", "mb_strrichr"} {
			fuzzCall(t, z, fn, withEnc(hv, nv, types.NewBool(start%2 == 0))...)
		}
		fuzzCall(t, z, "mb_substr_count", withEnc(hv, nv)...)
		fuzzCall(t, z, "mb_strwidth", withEnc(hv)...)
		fuzzCall(t, z, "mb_strimwidth", withEnc(hv, st, ln, nv)...)
		fuzzCall(t, z, "mb_convert_case", withEnc(hv, types.NewLong(start&7))...)
		fuzzCall(t, z, "mb_check_encoding", withEnc(hv)...)
		fuzzCall(t, z, "mb_detect_encoding", hv, fuzzStr(t, z, "ASCII,UTF-8,ISO-8859-1"))
		if name := fuzzEncodings[int(enc)%len(fuzzEncodings)]; name != "" {
			fuzzCall(t, z, "mb_convert_encoding", hv, fuzzStr(t, z, name), fuzzStr(t, z, "UTF-8,ISO-8859-1"))
		}
		ref, err := types.NewReference(z, hv)
		if err != nil {
			t.Fatal(err)
		}
		fuzzCall(t, z, "mb_convert_variables", fuzzStr(t, z, "UTF-8"), fuzzStr(t, z, "ASCII,UTF-8,Windows-1252"), ref)
	})
}

func FuzzDateParseFromFormat(f *testing.F) {
	f.Add("Y-m-d H:i:s.u P", "2024-01-02 03:04:05.123456 +02:00")
	f.Add("D, d M Y G:i A e", "Fri, 15 Nov 2024 9:05 PM Europe/Paris")
	f.Add("!z Y U", "59 2024 -1700000000")
	f.Add(`\*#?+|X x v T O`, "x;y-0044 +12 5 EST GMT+02:00")
	f.Add("S l F y", "  week IX 99")
	z := zone.New(1 << 20)
	f.Fuzz(func(t *testing.T, format, s string) {
		defer resetFuzzZone(t, z)
		out := fuzzCall(t, z, "date_parse_from_format", fuzzStr(t, z, format), fuzzStr(t, z, s))
		if out.Type != types.TypeArray && out.Type != types.TypeNull {
			t.Fatalf("date_parse_from_format rend le type %d", out.Type)
		}
	})
}
