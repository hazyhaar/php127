package ext

import (
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// extCall appelle la native fn sur z ; les arguments chaîne deviennent des chaînes de la Zone.
func extCall(t *testing.T, z *zone.Zone, fn string, args ...any) (types.Value, error) {
	t.Helper()
	f, ok := Lookup(fn)
	if !ok {
		t.Fatalf("Lookup(%s)", fn)
	}
	vals := make([]types.Value, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case string:
			s, err := newStr(z, v)
			if err != nil {
				t.Fatal(err)
			}
			vals[i] = s
		case int:
			vals[i] = types.NewLong(int64(v))
		case bool:
			vals[i] = types.NewBool(v)
		case nil:
			vals[i] = types.NewNull()
		case types.Value:
			vals[i] = v
		default:
			t.Fatalf("argument %T", a)
		}
	}
	return f(z, nil, vals)
}

func mustCall(t *testing.T, z *zone.Zone, fn string, args ...any) types.Value {
	t.Helper()
	v, err := extCall(t, z, fn, args...)
	if err != nil {
		t.Fatalf("%s : %v", fn, err)
	}
	return v
}

// S8-M3 : l'état d'ext vit dans z.Slot(zone.SlotExt), sans nettoyage enregistré ; z.Reset(nil), appelé par
// probebench sans répartiteur, ne panique pas et rend une Zone aux valeurs par défaut de PHP.
func TestExtStateUsesZoneSlotAndResetsWithNilDispatcher(t *testing.T) {
	z := zone.New(1 << 16)
	mustCall(t, z, "preg_match", "/a/u", "\xff")
	mustCall(t, z, "mb_internal_encoding", "ISO-8859-1")
	mustCall(t, z, "mb_substitute_character", "none")
	mustCall(t, z, "openssl_decrypt", "", "aes-128-cbc", "k")
	h := mustCall(t, z, "hash_init", "sha256")
	mustCall(t, z, "hash_update", h, "abc")
	if _, ok := z.Slot(zone.SlotExt).(*extState); !ok {
		t.Fatalf("z.Slot(%d) = %T, état d'ext attendu", zone.SlotExt, z.Slot(zone.SlotExt))
	}
	if n := z.PendingCleanups(); n != 0 {
		t.Fatalf("%d nettoyages enregistrés, aucun attendu", n)
	}
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("z.Reset(nil) panique : %v", p)
			}
		}()
		z.Reset(nil)
	}()
	if v := z.Slot(zone.SlotExt); v != nil {
		t.Fatalf("z.Slot(%d) = %T après Reset", zone.SlotExt, v)
	}
	if v := mustCall(t, z, "preg_last_error"); v.ToLong() != pregNoError {
		t.Errorf("preg_last_error() = %d après Reset", v.ToLong())
	}
	if v := mustCall(t, z, "mb_internal_encoding"); types.StringData(z, v) != "UTF-8" {
		t.Errorf("mb_internal_encoding() = %q après Reset", types.StringData(z, v))
	}
	if v := mustCall(t, z, "mb_substitute_character"); v.Type != types.TypeLong || v.ToLong() != '?' {
		t.Errorf("mb_substitute_character() = %s après Reset", ser(z, v))
	}
	if v := mustCall(t, z, "openssl_error_string"); v.Type != types.TypeFalse {
		t.Errorf("openssl_error_string() = %s après Reset", ser(z, v))
	}
	if _, err := extCall(t, z, "hash_update", h, "x"); err == nil || ExceptionClass(err) != "TypeError" {
		t.Errorf("hash_update sur un contexte d'avant Reset : %v, TypeError attendue", err)
	}
	z.Reset(nil)
}
