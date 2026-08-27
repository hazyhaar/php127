// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/builtins: Tests unitaires de la bibliothèque standard

package builtins

import (
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
)

func TestBuiltinsCrypto(t *testing.T) {
	// MD5
	m := Md5(types.NewString("hello world"))
	if m.String() != "5eb63bbbe01eeed093cb22bb8f5acdc3" {
		t.Fatalf("MD5 invalide: %s", m.String())
	}

	// SHA256
	s := Sha256(types.NewString("hello"))
	if s.String() != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("SHA256 invalide: %s", s.String())
	}

	// Hash HMAC
	hm := HashHmac(types.NewString("sha256"), types.NewString("data"), types.NewString("key"))
	if len(hm.String()) != 64 {
		t.Fatalf("HMAC length invalide: %d", len(hm.String()))
	}

	// Hex conversions
	b2h := Bin2Hex(types.NewString("abc"))
	if b2h.String() != "616263" {
		t.Fatalf("Bin2Hex attendu '616263', got: %s", b2h.String())
	}
	h2b := Hex2Bin(b2h)
	if h2b.String() != "abc" {
		t.Fatalf("Hex2Bin attendu 'abc', got: %s", h2b.String())
	}
}

func TestBuiltinsStrings(t *testing.T) {
	// Trim
	tr := Trim(types.NewString("  \t\nhello \r "), types.NewNull())
	if tr.String() != "hello" {
		t.Fatalf("Trim invalide: %q", tr.String())
	}

	// Case conversion
	lo := Strtolower(types.NewString("HELLO PHP 127"))
	if lo.String() != "hello php 127" {
		t.Fatalf("Strtolower invalide: %s", lo.String())
	}
	up := Strtoupper(lo)
	if up.String() != "HELLO PHP 127" {
		t.Fatalf("Strtoupper invalide: %s", up.String())
	}

	// StrReplace
	rep := StrReplace(types.NewString("world"), types.NewString("PHP 8.4"), types.NewString("Hello world!"))
	if rep.String() != "Hello PHP 8.4!" {
		t.Fatalf("StrReplace invalide: %s", rep.String())
	}

	// StrRepeat
	rpt := StrRepeat(types.NewString("xyz"), types.NewLong(3))
	if rpt.String() != "xyzxyzxyz" {
		t.Fatalf("StrRepeat invalide: %s", rpt.String())
	}
}

func TestBuiltinsDateTime(t *testing.T) {
	d := Date(types.NewString("Y-m-d H:i:s"), types.NewLong(1700000000))
	if len(d.String()) != 19 {
		t.Fatalf("Date formatée longueur invalide: %q", d.String())
	}

	st := Strtotime(types.NewString("2023-11-14 22:13:20"))
	if st.ToLong() <= 0 {
		t.Fatalf("Strtotime invalide: %d", st.ToLong())
	}
}

func TestBuiltinsJson(t *testing.T) {
	enc := JsonEncode(types.NewLong(123456))
	if enc.String() != "123456" {
		t.Fatalf("JsonEncode long invalide: %s", enc.String())
	}

	dec := JsonDecode(types.NewString("987654"), false)
	if dec.ToLong() != 987654 {
		t.Fatalf("JsonDecode invalide: %d", dec.ToLong())
	}
}
