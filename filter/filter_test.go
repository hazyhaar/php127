package filter_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/filter"
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

func TestFilterConstantsAndLookupNoAlloc(t *testing.T) {
	consts := filter.Constants()
	if consts["FILTER_VALIDATE_INT"] != 257 || consts["FILTER_NULL_ON_FAILURE"] != 134217728 {
		t.Fatalf("unexpected filter constants: %+v", consts)
	}
	for _, name := range []string{"filter_var", "filter_var_array", "filter_id", "filter_list"} {
		fn, ok := filter.Lookup(name)
		if !ok || fn == nil {
			t.Fatalf("Lookup(%q) failed", name)
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		if _, ok := filter.Lookup("FILTER_VAR_ARRAY"); !ok {
			t.Fatal("expected match")
		}
	})
	if allocs != 0 {
		t.Fatalf("Lookup allocated %v times, want 0", allocs)
	}
}

func TestFilterDifferentialVsPHP(t *testing.T) {
	phpScript := `
$out = [];
$out["list"] = filter_list();
$ids = [];
foreach ($out["list"] as $name) {
    $ids[$name] = filter_id($name);
}
$out["ids"] = $ids;

$out["v_int_dec"] = filter_var("  -42 ", FILTER_VALIDATE_INT);
$out["v_int_bad"] = filter_var("042", FILTER_VALIDATE_INT);
$out["v_int_oct"] = filter_var("0755", FILTER_VALIDATE_INT, FILTER_FLAG_ALLOW_OCTAL);
$out["v_int_hex"] = filter_var("0x1a", FILTER_VALIDATE_INT, FILTER_FLAG_ALLOW_HEX);
$out["v_int_range_ok"] = filter_var("15", FILTER_VALIDATE_INT, ["options" => ["min_range" => 10, "max_range" => 20]]);
$out["v_int_range_fail"] = filter_var("25", FILTER_VALIDATE_INT, ["options" => ["min_range" => 10, "max_range" => 20, "default" => -1]]);

$out["v_bool_yes"] = filter_var("yes", FILTER_VALIDATE_BOOLEAN);
$out["v_bool_no"] = filter_var("off", FILTER_VALIDATE_BOOLEAN);
$out["v_bool_null"] = filter_var("maybe", FILTER_VALIDATE_BOOLEAN, FILTER_NULL_ON_FAILURE);

$out["v_float_th"] = filter_var("1,234.5", FILTER_VALIDATE_FLOAT, FILTER_FLAG_ALLOW_THOUSAND);
$out["v_email_ok"] = filter_var("user.name+tag@example.com", FILTER_VALIDATE_EMAIL);
$out["v_email_bad"] = filter_var("user..bad@example.com", FILTER_VALIDATE_EMAIL);
$out["v_url_ok"] = filter_var("https://example.com/a/b?x=1", FILTER_VALIDATE_URL, FILTER_FLAG_PATH_REQUIRED | FILTER_FLAG_QUERY_REQUIRED);
$out["v_url_noquery"] = filter_var("https://example.com/a/b", FILTER_VALIDATE_URL, FILTER_FLAG_QUERY_REQUIRED);
$out["v_ip4_ok"] = filter_var("8.8.8.8", FILTER_VALIDATE_IP, FILTER_FLAG_IPV4 | FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE);
$out["v_ip4_priv"] = filter_var("192.168.1.1", FILTER_VALIDATE_IP, FILTER_FLAG_IPV4 | FILTER_FLAG_NO_PRIV_RANGE);
$out["v_ip6_ok"] = filter_var("2001:4860:4860::8888", FILTER_VALIDATE_IP, FILTER_FLAG_IPV6);
$out["v_mac_ok"] = filter_var("01:23:45:67:89:ab", FILTER_VALIDATE_MAC);
$out["v_domain_ok"] = filter_var("sub.example.org", FILTER_VALIDATE_DOMAIN);
$out["v_regexp_ok"] = filter_var("abc-123", FILTER_VALIDATE_REGEXP, ["options" => ["regexp" => "/^[a-z]+-[0-9]+$/"]]);

$out["s_email"] = filter_var("a(b)c@example.com", FILTER_SANITIZE_EMAIL);
$out["s_num_int"] = filter_var("id: +42-99px", FILTER_SANITIZE_NUMBER_INT);
$out["s_num_float"] = filter_var("val=-12.34e+5$", FILTER_SANITIZE_NUMBER_FLOAT, FILTER_FLAG_ALLOW_FRACTION | FILTER_FLAG_ALLOW_SCIENTIFIC);
$out["s_spec"] = filter_var("a<b>c\"d'e&f", FILTER_SANITIZE_SPECIAL_CHARS);
$out["s_full_spec"] = filter_var("a<b>c\"d'e&f", FILTER_SANITIZE_FULL_SPECIAL_CHARS);
$out["s_slashes"] = filter_var("a'b\"c\\d", FILTER_SANITIZE_ADD_SLASHES);

echo json_encode($out);
`
	cmd := exec.CommandContext(t.Context(), "/usr/bin/php", "-n", "-r", phpScript)
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
	defer z.Reset(nil)

	call := func(name string, args ...types.Value) types.Value {
		t.Helper()
		fn, ok := filter.Lookup(name)
		if !ok {
			t.Fatalf("missing %s", name)
		}
		v, err := fn(z, nil, args)
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
		return v
	}

	// 1. filter_list & filter_id
	listVal := call("filter_list")
	defer types.ReleaseValue(z, &listVal)
	wantList := want["list"].([]any)
	wantIDs := want["ids"].(map[string]any)
	if int(types.ArrayLen(z, listVal)) != len(wantList) {
		t.Fatalf("filter_list len = %d, want %d", types.ArrayLen(z, listVal), len(wantList))
	}
	for i, w := range wantList {
		name := w.(string)
		elem, ok, _ := types.ArrayGet(z, listVal, types.NewLong(int64(i)), nil)
		if !ok || types.StringData(z, elem) != name {
			t.Fatalf("filter_list[%d] = %v, want %q", i, elem, name)
		}
		ns := mustStr(t, z, name)
		idVal := call("filter_id", ns)
		types.ReleaseValue(z, &ns)
		if float64(idVal.ToLong()) != wantIDs[name].(float64) {
			t.Fatalf("filter_id(%q) = %d, want %v", name, idVal.ToLong(), wantIDs[name])
		}
	}

	checkVarStr := func(key, input string, fID, flags int64) {
		t.Helper()
		inV := mustStr(t, z, input)
		defer types.ReleaseValue(z, &inV)
		got := call("filter_var", inV, types.NewLong(fID), types.NewLong(flags))
		defer types.ReleaseValue(z, &got)
		expected := want[key]
		switch exp := expected.(type) {
		case nil:
			if !got.IsNull() {
				t.Fatalf("%s: got %v, want null", key, got)
			}
		case bool:
			if !got.IsBool() || got.ToBool() != exp {
				t.Fatalf("%s: got %v, want %v", key, got, exp)
			}
		case float64:
			if got.Type == types.TypeLong {
				if float64(got.ToLong()) != exp {
					t.Fatalf("%s: got %d, want %v", key, got.ToLong(), exp)
				}
			} else if got.Type == types.TypeDouble {
				if got.ToDouble() != exp {
					t.Fatalf("%s: got %v, want %v", key, got.ToDouble(), exp)
				}
			} else {
				t.Fatalf("%s: unexpected type %v", key, got)
			}
		case string:
			if got.Type != types.TypeString || types.StringData(z, got) != exp {
				t.Fatalf("%s: got %v, want %q", key, got, exp)
			}
		}
	}

	checkVarStr("v_int_dec", "  -42 ", filter.FILTER_VALIDATE_INT, 0)
	checkVarStr("v_int_bad", "042", filter.FILTER_VALIDATE_INT, 0)
	checkVarStr("v_int_oct", "0755", filter.FILTER_VALIDATE_INT, filter.FILTER_FLAG_ALLOW_OCTAL)
	checkVarStr("v_int_hex", "0x1a", filter.FILTER_VALIDATE_INT, filter.FILTER_FLAG_ALLOW_HEX)
	checkVarStr("v_bool_yes", "yes", filter.FILTER_VALIDATE_BOOLEAN, 0)
	checkVarStr("v_bool_no", "off", filter.FILTER_VALIDATE_BOOLEAN, 0)
	checkVarStr("v_bool_null", "maybe", filter.FILTER_VALIDATE_BOOLEAN, filter.FILTER_NULL_ON_FAILURE)
	checkVarStr("v_float_th", "1,234.5", filter.FILTER_VALIDATE_FLOAT, filter.FILTER_FLAG_ALLOW_THOUSAND)
	checkVarStr("v_email_ok", "user.name+tag@example.com", filter.FILTER_VALIDATE_EMAIL, 0)
	checkVarStr("v_email_bad", "user..bad@example.com", filter.FILTER_VALIDATE_EMAIL, 0)
	checkVarStr("v_url_ok", "https://example.com/a/b?x=1", filter.FILTER_VALIDATE_URL, filter.FILTER_FLAG_PATH_REQUIRED|filter.FILTER_FLAG_QUERY_REQUIRED)
	checkVarStr("v_url_noquery", "https://example.com/a/b", filter.FILTER_VALIDATE_URL, filter.FILTER_FLAG_QUERY_REQUIRED)
	checkVarStr("v_ip4_ok", "8.8.8.8", filter.FILTER_VALIDATE_IP, filter.FILTER_FLAG_IPV4|filter.FILTER_FLAG_NO_PRIV_RANGE|filter.FILTER_FLAG_NO_RES_RANGE)
	checkVarStr("v_ip4_priv", "192.168.1.1", filter.FILTER_VALIDATE_IP, filter.FILTER_FLAG_IPV4|filter.FILTER_FLAG_NO_PRIV_RANGE)
	checkVarStr("v_ip6_ok", "2001:4860:4860::8888", filter.FILTER_VALIDATE_IP, filter.FILTER_FLAG_IPV6)
	checkVarStr("v_mac_ok", "01:23:45:67:89:ab", filter.FILTER_VALIDATE_MAC, 0)
	checkVarStr("v_domain_ok", "sub.example.org", filter.FILTER_VALIDATE_DOMAIN, 0)

	checkVarStr("s_email", "a(b)c@example.com", filter.FILTER_SANITIZE_EMAIL, 0)
	checkVarStr("s_num_int", "id: +42-99px", filter.FILTER_SANITIZE_NUMBER_INT, 0)
	checkVarStr("s_num_float", "val=-12.34e+5$", filter.FILTER_SANITIZE_NUMBER_FLOAT, filter.FILTER_FLAG_ALLOW_FRACTION|filter.FILTER_FLAG_ALLOW_SCIENTIFIC)
	checkVarStr("s_spec", "a<b>c\"d'e&f", filter.FILTER_SANITIZE_SPECIAL_CHARS, 0)
	checkVarStr("s_full_spec", "a<b>c\"d'e&f", filter.FILTER_SANITIZE_FULL_SPECIAL_CHARS, 0)
	checkVarStr("s_slashes", "a'b\"c\\d", filter.FILTER_SANITIZE_ADD_SLASHES, 0)

	// Test filter_var_array.
	inArr, _ := types.NewArray(z)
	defer types.ReleaseArray(z, &inArr)
	kAge := mustStr(t, z, "age")
	defer types.ReleaseValue(z, &kAge)
	vAge := mustStr(t, z, "28")
	defer types.ReleaseValue(z, &vAge)
	_ = types.ArraySet(z, &inArr, kAge, vAge, nil)

	outArr := call("filter_var_array", inArr, types.NewLong(filter.FILTER_VALIDATE_INT))
	defer types.ReleaseValue(z, &outArr)
	gotAge, ok, _ := types.ArrayGet(z, outArr, kAge, nil)
	if !ok || gotAge.ToLong() != 28 {
		t.Fatalf("filter_var_array[age] = %v, want 28", gotAge)
	}
}
