package datetime_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/datetime"
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

func TestLookupAllProceduralAndNoAlloc(t *testing.T) {
	names := []string{
		"date_create",
		"date_create_immutable",
		"date_create_from_format",
		"date_create_immutable_from_format",
		"date_format",
		"date_modify",
		"date_add",
		"date_sub",
		"date_timezone_get",
		"date_timezone_set",
		"date_offset_get",
		"date_diff",
		"date_timestamp_get",
		"date_timestamp_set",
		"date_date_set",
		"date_time_set",
		"date_isodate_set",
		"timezone_open",
		"timezone_name_get",
		"timezone_offset_get",
		"timezone_identifiers_list",
		"date_interval_create_from_date_string",
		"date_interval_format",
		"date_default_timezone_get",
		"date_default_timezone_set",
		"checkdate",
		"getdate",
		"idate",
		"localtime",
	}
	for _, name := range names {
		fn, ok := datetime.Lookup(name)
		if !ok || fn == nil {
			t.Fatalf("Lookup(%q) failed", name)
		}
	}
	allocs := testing.AllocsPerRun(100, func() {
		_, ok := datetime.Lookup("DATE_CREATE_IMMUTABLE_FROM_FORMAT")
		if !ok {
			t.Fatal("expected case-insensitive lookup match")
		}
	})
	if allocs != 0 {
		t.Fatalf("Lookup allocated %v times, want 0", allocs)
	}
}

func TestDateTimeHierarchyAndIntervals(t *testing.T) {
	z := zone.New(zone.DefaultCapacity)
	defer z.Reset(datetime.CleanupDispatcher(z))

	if got := datetime.DefaultTimezoneGet(z); got != "UTC" {
		t.Fatalf("DefaultTimezoneGet = %q, want UTC", got)
	}
	if !datetime.DefaultTimezoneSet(z, "Europe/Paris") {
		t.Fatal("DefaultTimezoneSet(Europe/Paris) failed")
	}
	if datetime.DefaultTimezoneSet(z, "Invalid/Zone_Name") {
		t.Fatal("expected false on invalid timezone")
	}
	_ = datetime.DefaultTimezoneSet(z, "UTC")

	tzObj, tz, err := datetime.NewDateTimeZone(z, "Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &tzObj)
	if tz.GetName() != "Europe/Paris" {
		t.Fatalf("GetName = %q, want Europe/Paris", tz.GetName())
	}

	dtObj, dt, err := datetime.NewDateTime(z, "2024-01-15 10:20:30", tz)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &dtObj)

	if dt.GetOffset() != 3600 {
		t.Fatalf("Europe/Paris winter offset = %d, want 3600", dt.GetOffset())
	}

	// Conversion mutable <-> immutable.
	immObj, imm, err := datetime.CreateFromMutable(z, dt)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &immObj)

	mut2Obj, mut2, err := datetime.CreateFromImmutable(z, imm)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &mut2Obj)
	if mut2.GetTimestamp() != dt.GetTimestamp() {
		t.Fatal("CreateFromImmutable timestamp mismatch")
	}

	// Modify on immutable returns a new instance without changing original.
	imm2Obj, imm2, err := imm.Modify(z, "+1 day")
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &imm2Obj)
	if imm.Format("Y-m-d") != "2024-01-15" || imm2.Format("Y-m-d") != "2024-01-16" {
		t.Fatalf("immutable Modify mutated original or wrong result: %s -> %s", imm.Format("Y-m-d"), imm2.Format("Y-m-d"))
	}

	// DateInterval ISO 8601 and P2W.
	ivObj, iv, err := datetime.NewDateInterval(z, "P1Y2M3DT4H5M6S")
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &ivObj)
	if got := iv.Format("%Y-%M-%D %H:%I:%S %a"); got != "01-02-03 04:05:06 (unknown)" {
		t.Fatalf("iv.Format = %q", got)
	}

	ivWObj, ivW, err := datetime.NewDateInterval(z, "P2W")
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &ivWObj)
	dProp, err := ivW.GetProperty(z, "d")
	if err != nil || dProp.ToLong() != 14 {
		t.Fatalf("P2W d property = %v, want 14", dProp)
	}
	if err := ivW.SetProperty(z, "d", types.NewLong(21)); err != nil {
		t.Fatal(err)
	}
	if ivW.Format("%d") != "21" {
		t.Fatalf("after SetProperty(d, 21), Format(%%d) = %q, want 21", ivW.Format("%d"))
	}

	if _, _, err := datetime.NewDateInterval(z, "BAD_INTERVAL"); err == nil {
		t.Fatal("expected DateError on invalid interval")
	} else if de, ok := errors.AsType[datetime.DateError](err); !ok || de.Class != "DateMalformedIntervalStringException" {
		t.Fatalf("unexpected error: %v", err)
	}

	// DatePeriod with INCLUDE_START_DATE and EXCLUDE_START_DATE.
	stepObj, stepIV, err := datetime.NewDateInterval(z, "P1D")
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &stepObj)

	pStartObj, pStart, err := datetime.NewDateTimeImmutable(z, "2024-05-01", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &pStartObj)

	pIncObj, pInc, err := datetime.NewDatePeriodWithRecurrences(z, pStart, stepIV, 2, datetime.INCLUDE_START_DATE)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &pIncObj)

	var incDates []string
	for pInc.Rewind(); pInc.Valid(); pInc.Next() {
		item, err := datetime.DateTimeInterfaceFromObject(z, pInc.Current())
		if err != nil {
			t.Fatal(err)
		}
		incDates = append(incDates, item.Format("Y-m-d"))
	}
	if !reflect.DeepEqual(incDates, []string{"2024-05-01", "2024-05-02", "2024-05-03"}) {
		t.Fatalf("DatePeriod INCLUDE_START_DATE = %v", incDates)
	}

	pExcObj, pExc, err := datetime.NewDatePeriodWithRecurrences(z, pStart, stepIV, 2, datetime.EXCLUDE_START_DATE)
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &pExcObj)

	var excDates []string
	for pExc.Rewind(); pExc.Valid(); pExc.Next() {
		item, err := datetime.DateTimeInterfaceFromObject(z, pExc.Current())
		if err != nil {
			t.Fatal(err)
		}
		excDates = append(excDates, item.Format("Y-m-d"))
	}
	if !reflect.DeepEqual(excDates, []string{"2024-05-02", "2024-05-03"}) {
		t.Fatalf("DatePeriod EXCLUDE_START_DATE = %v", excDates)
	}
}

func TestDateTimeDifferentialVsPHP(t *testing.T) {
	phpScript := `
date_default_timezone_set("UTC");
$out = [];

$d1 = date_create_immutable("2024-01-15 10:20:30");
$d2 = date_create_immutable("2024-03-20 14:25:36");
$diff = date_diff($d1, $d2);
$out["diff_fmt"] = date_interval_format($diff, "%R %Y-%M-%D %H:%I:%S (%a) %y/%m/%d");
$diffRev = date_diff($d2, $d1);
$out["diff_rev"] = date_interval_format($diffRev, "%R|%r|%a");
$diffAbs = date_diff($d2, $d1, true);
$out["diff_abs"] = date_interval_format($diffAbs, "%R|%r|%a");

$m = date_create("2024-02-29 12:00:00");
date_add($m, new DateInterval("P1Y"));
$out["leap_add_1y"] = date_format($m, "Y-m-d H:i:s");
date_sub($m, date_interval_create_from_date_string("10 days + 2 hours"));
$out["after_sub"] = date_format($m, "Y-m-d H:i:s");
date_date_set($m, 2025, 12, 31);
date_time_set($m, 23, 59, 58);
$out["after_set"] = date_format($m, DateTimeInterface::ATOM);
date_isodate_set($m, 2024, 1, 1);
$out["iso_2024_w1_1"] = date_format($m, "Y-m-d");

$parsed = date_create_from_format("!Y-m-d H:i:s", "2023-11-14 22:13:20");
$out["parsed_ts"] = date_timestamp_get($parsed);

$tz = timezone_open("Europe/Paris");
$out["tz_name"] = timezone_name_get($tz);
$out["tz_offset"] = timezone_offset_get($tz, $parsed);

$out["checkdate_2024_02_29"] = checkdate(2, 29, 2024);
$out["checkdate_2023_02_29"] = checkdate(2, 29, 2023);
$out["getdate"] = getdate(1700000000);
$out["localtime_idx"] = localtime(1700000000, false);
$out["localtime_assoc"] = localtime(1700000000, true);

$idateMap = [];
foreach (["B","d","h","H","i","I","L","m","s","t","U","w","W","y","Y","z","Z","N"] as $tok) {
    $idateMap[$tok] = idate($tok, 1700000000);
}
$out["idate"] = $idateMap;

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
	defer z.Reset(datetime.CleanupDispatcher(z))

	call := func(name string, args ...types.Value) types.Value {
		t.Helper()
		fn, ok := datetime.Lookup(name)
		if !ok {
			t.Fatalf("missing procedural function %s", name)
		}
		v, err := fn(z, nil, args)
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
		return v
	}

	sUTC := mustStr(t, z, "UTC")
	defer types.ReleaseValue(z, &sUTC)
	call("date_default_timezone_set", sUTC)

	s1 := mustStr(t, z, "2024-01-15 10:20:30")
	defer types.ReleaseValue(z, &s1)
	s2 := mustStr(t, z, "2024-03-20 14:25:36")
	defer types.ReleaseValue(z, &s2)

	d1 := call("date_create_immutable", s1)
	defer types.ReleaseValue(z, &d1)
	d2 := call("date_create_immutable", s2)
	defer types.ReleaseValue(z, &d2)

	diff := call("date_diff", d1, d2)
	defer types.ReleaseValue(z, &diff)
	fmt1 := mustStr(t, z, "%R %Y-%M-%D %H:%I:%S (%a) %y/%m/%d")
	defer types.ReleaseValue(z, &fmt1)
	diffFormatted := call("date_interval_format", diff, fmt1)
	defer types.ReleaseValue(z, &diffFormatted)
	if got := types.StringData(z, diffFormatted); got != want["diff_fmt"].(string) {
		t.Fatalf("diff_fmt = %q, want %q", got, want["diff_fmt"])
	}

	fmt2 := mustStr(t, z, "%R|%r|%a")
	defer types.ReleaseValue(z, &fmt2)
	diffRev := call("date_diff", d2, d1)
	defer types.ReleaseValue(z, &diffRev)
	revStr := call("date_interval_format", diffRev, fmt2)
	defer types.ReleaseValue(z, &revStr)
	if got := types.StringData(z, revStr); got != want["diff_rev"].(string) {
		t.Fatalf("diff_rev = %q, want %q", got, want["diff_rev"])
	}

	diffAbs := call("date_diff", d2, d1, types.NewBool(true))
	defer types.ReleaseValue(z, &diffAbs)
	absStr := call("date_interval_format", diffAbs, fmt2)
	defer types.ReleaseValue(z, &absStr)
	if got := types.StringData(z, absStr); got != want["diff_abs"].(string) {
		t.Fatalf("diff_abs = %q, want %q", got, want["diff_abs"])
	}

	sLeap := mustStr(t, z, "2024-02-29 12:00:00")
	defer types.ReleaseValue(z, &sLeap)
	m := call("date_create", sLeap)
	defer types.ReleaseValue(z, &m)

	iv1Y, _, err := datetime.NewDateInterval(z, "P1Y")
	if err != nil {
		t.Fatal(err)
	}
	defer types.ReleaseValue(z, &iv1Y)
	mAdd := call("date_add", m, iv1Y)
	types.ReleaseValue(z, &mAdd)

	sYmdHis := mustStr(t, z, "Y-m-d H:i:s")
	defer types.ReleaseValue(z, &sYmdHis)
	leapFmt := call("date_format", m, sYmdHis)
	defer types.ReleaseValue(z, &leapFmt)
	if got := types.StringData(z, leapFmt); got != want["leap_add_1y"].(string) {
		t.Fatalf("leap_add_1y = %q, want %q", got, want["leap_add_1y"])
	}

	sRel := mustStr(t, z, "10 days + 2 hours")
	defer types.ReleaseValue(z, &sRel)
	ivRel := call("date_interval_create_from_date_string", sRel)
	defer types.ReleaseValue(z, &ivRel)
	mSub := call("date_sub", m, ivRel)
	types.ReleaseValue(z, &mSub)
	afterSubFmt := call("date_format", m, sYmdHis)
	defer types.ReleaseValue(z, &afterSubFmt)
	if got := types.StringData(z, afterSubFmt); got != want["after_sub"].(string) {
		t.Fatalf("after_sub = %q, want %q", got, want["after_sub"])
	}

	r1 := call("date_date_set", m, types.NewLong(2025), types.NewLong(12), types.NewLong(31))
	types.ReleaseValue(z, &r1)
	r2 := call("date_time_set", m, types.NewLong(23), types.NewLong(59), types.NewLong(58))
	types.ReleaseValue(z, &r2)
	sAtom := mustStr(t, z, datetime.ATOM)
	defer types.ReleaseValue(z, &sAtom)
	afterSetFmt := call("date_format", m, sAtom)
	defer types.ReleaseValue(z, &afterSetFmt)
	if got := types.StringData(z, afterSetFmt); got != want["after_set"].(string) {
		t.Fatalf("after_set = %q, want %q", got, want["after_set"])
	}

	r3 := call("date_isodate_set", m, types.NewLong(2024), types.NewLong(1), types.NewLong(1))
	types.ReleaseValue(z, &r3)
	sYmd := mustStr(t, z, "Y-m-d")
	defer types.ReleaseValue(z, &sYmd)
	isoFmt := call("date_format", m, sYmd)
	defer types.ReleaseValue(z, &isoFmt)
	if got := types.StringData(z, isoFmt); got != want["iso_2024_w1_1"].(string) {
		t.Fatalf("iso_2024_w1_1 = %q, want %q", got, want["iso_2024_w1_1"])
	}

	sBangFmt := mustStr(t, z, "!Y-m-d H:i:s")
	defer types.ReleaseValue(z, &sBangFmt)
	sTarget := mustStr(t, z, "2023-11-14 22:13:20")
	defer types.ReleaseValue(z, &sTarget)
	parsed := call("date_create_from_format", sBangFmt, sTarget)
	defer types.ReleaseValue(z, &parsed)
	ts := call("date_timestamp_get", parsed)
	if float64(ts.ToLong()) != want["parsed_ts"].(float64) {
		t.Fatalf("parsed_ts = %d, want %v", ts.ToLong(), want["parsed_ts"])
	}

	sParis := mustStr(t, z, "Europe/Paris")
	defer types.ReleaseValue(z, &sParis)
	tzVal := call("timezone_open", sParis)
	defer types.ReleaseValue(z, &tzVal)
	tzNameVal := call("timezone_name_get", tzVal)
	defer types.ReleaseValue(z, &tzNameVal)
	if types.StringData(z, tzNameVal) != want["tz_name"].(string) {
		t.Fatalf("tz_name = %q, want %v", types.StringData(z, tzNameVal), want["tz_name"])
	}
	tzOff := call("timezone_offset_get", tzVal, parsed)
	if float64(tzOff.ToLong()) != want["tz_offset"].(float64) {
		t.Fatalf("tz_offset = %d, want %v", tzOff.ToLong(), want["tz_offset"])
	}

	c1 := call("checkdate", types.NewLong(2), types.NewLong(29), types.NewLong(2024))
	c2 := call("checkdate", types.NewLong(2), types.NewLong(29), types.NewLong(2023))
	if c1.ToBool() != want["checkdate_2024_02_29"].(bool) || c2.ToBool() != want["checkdate_2023_02_29"].(bool) {
		t.Fatal("checkdate mismatch")
	}

	gd := call("getdate", types.NewLong(1700000000))
	defer types.ReleaseValue(z, &gd)
	wantGD := want["getdate"].(map[string]any)
	for _, k := range []string{"seconds", "minutes", "hours", "mday", "wday", "mon", "year", "yday"} {
		kv := mustStr(t, z, k)
		v, ok, _ := types.ArrayGet(z, gd, kv, nil)
		types.ReleaseValue(z, &kv)
		if !ok || float64(v.ToLong()) != wantGD[k].(float64) {
			t.Fatalf("getdate[%s] = %v, want %v", k, v, wantGD[k])
		}
	}

	ltIdx := call("localtime", types.NewLong(1700000000), types.NewBool(false))
	defer types.ReleaseValue(z, &ltIdx)
	wantLTIdx := want["localtime_idx"].([]any)
	for i, w := range wantLTIdx {
		v, ok, _ := types.ArrayGet(z, ltIdx, types.NewLong(int64(i)), nil)
		if !ok || float64(v.ToLong()) != w.(float64) {
			t.Fatalf("localtime_idx[%d] = %v, want %v", i, v, w)
		}
	}

	ltAssoc := call("localtime", types.NewLong(1700000000), types.NewBool(true))
	defer types.ReleaseValue(z, &ltAssoc)
	wantLTAssoc := want["localtime_assoc"].(map[string]any)
	for k, w := range wantLTAssoc {
		kv := mustStr(t, z, k)
		v, ok, _ := types.ArrayGet(z, ltAssoc, kv, nil)
		types.ReleaseValue(z, &kv)
		if !ok || float64(v.ToLong()) != w.(float64) {
			t.Fatalf("localtime_assoc[%s] = %v, want %v", k, v, w)
		}
	}

	wantIdate := want["idate"].(map[string]any)
	for tok, w := range wantIdate {
		tv := mustStr(t, z, tok)
		got := call("idate", tv, types.NewLong(1700000000))
		types.ReleaseValue(z, &tv)
		if float64(got.ToLong()) != w.(float64) {
			t.Fatalf("idate(%s) = %d, want %v", tok, got.ToLong(), w)
		}
	}
}
