// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/builtins: Tests des 10 cibles prioritaires (Dotenv, UUID, Monolog, cURL)

package builtins

import (
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
)

func TestDotenvParser(t *testing.T) {
	content := `
	APP_NAME="Laravel 11 In-Process"
	APP_ENV=production
	DB_HOST=127.0.0.1
	DB_PORT=3306
	FULL_URL="http://${DB_HOST}:${DB_PORT}"
	`

	res := ParseDotenv(content)
	if res["APP_NAME"] != "Laravel 11 In-Process" {
		t.Fatalf("Attendu APP_NAME 'Laravel 11 In-Process', got: %s", res["APP_NAME"])
	}
	if res["FULL_URL"] != "http://127.0.0.1:3306" {
		t.Fatalf("Attendu FULL_URL 'http://127.0.0.1:3306', got: %s", res["FULL_URL"])
	}

	Putenv(types.NewString("CUSTOM_VAR=Php127Secret"))
	val := Getenv(types.NewString("CUSTOM_VAR"))
	if val.String() != "Php127Secret" {
		t.Fatalf("Getenv inattendu: %s", val.String())
	}
}

func TestUuidAndUlidGeneration(t *testing.T) {
	u4 := UuidV4().String()
	if len(u4) != 36 || u4[14] != '4' {
		t.Fatalf("UUID v4 invalide: %s", u4)
	}

	u7 := UuidV7().String()
	if len(u7) != 36 || u7[14] != '7' {
		t.Fatalf("UUID v7 invalide: %s", u7)
	}

	ul := Ulid().String()
	if len(ul) != 26 {
		t.Fatalf("ULID invalide (longueur %d != 26): %s", len(ul), ul)
	}
}

func TestMonologFormatting(t *testing.T) {
	out := LogMessage(types.NewString("INFO"), types.NewString("Order processed successfully"), types.NewString("billing"))
	outStr := out.String()
	if !strings.Contains(outStr, "billing.INFO: Order processed successfully") {
		t.Fatalf("Formatage Monolog incorrect: %s", outStr)
	}
}

func TestCurlEmulation(t *testing.T) {
	h := CurlInit(types.NewString("https://api.example.com/v1/users"))
	if h.Type != types.TypeObject {
		t.Fatalf("CurlInit n'a pas retourné de handle")
	}

	ok := CurlSetopt(h, types.NewLong(CURLOPT_RETURNTRANSFER), types.NewBool(true))
	if !ok.ToBool() {
		t.Fatalf("CurlSetopt a échoué")
	}

	info := CurlGetinfo(h, types.NewLong(CURLINFO_HTTP_CODE))
	if info.Type != types.TypeLong {
		t.Fatalf("CurlGetinfo a échoué")
	}

	CurlClose(h)
}
