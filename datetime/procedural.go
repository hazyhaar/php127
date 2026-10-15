package datetime

import (
	"fmt"
	"io"
	"time"

	"code.hazyhaar.fr/devhoros/php127/stdlib"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

type nativeEntry struct {
	name string
	fn   stdlib.NativeFunc
}

var natives = [...]nativeEntry{
	{"checkdate", nativeCheckdate},
	{"date_add", nativeDateAdd},
	{"date_create", nativeDateCreate},
	{"date_create_from_format", nativeDateCreateFromFormat},
	{"date_create_immutable", nativeDateCreateImmutable},
	{"date_create_immutable_from_format", nativeDateCreateImmutableFromFormat},
	{"date_date_set", nativeDateDateSet},
	{"date_default_timezone_get", nativeDateDefaultTimezoneGet},
	{"date_default_timezone_set", nativeDateDefaultTimezoneSet},
	{"date_diff", nativeDateDiff},
	{"date_format", nativeDateFormat},
	{"date_interval_create_from_date_string", nativeDateIntervalCreateFromDateString},
	{"date_interval_format", nativeDateIntervalFormat},
	{"date_isodate_set", nativeDateISODateSet},
	{"date_modify", nativeDateModify},
	{"date_offset_get", nativeDateOffsetGet},
	{"date_sub", nativeDateSub},
	{"date_time_set", nativeDateTimeSet},
	{"date_timestamp_get", nativeDateTimestampGet},
	{"date_timestamp_set", nativeDateTimestampSet},
	{"date_timezone_get", nativeDateTimezoneGet},
	{"date_timezone_set", nativeDateTimezoneSet},
	{"getdate", nativeGetdate},
	{"idate", nativeIdate},
	{"localtime", nativeLocaltime},
	{"timezone_identifiers_list", nativeTimezoneIdentifiersList},
	{"timezone_name_get", nativeTimezoneNameGet},
	{"timezone_offset_get", nativeTimezoneOffsetGet},
	{"timezone_open", nativeTimezoneOpen},
}

func init() {
	for i := 1; i < len(natives); i++ {
		if natives[i-1].name >= natives[i].name {
			panic("datetime: table natives non triée à l'entrée " + natives[i].name)
		}
	}
}

// Lookup résout une fonction procédurale de date/heure par recherche dichotomique insensible à la casse ASCII.
func Lookup(name string) (stdlib.NativeFunc, bool) {
	lo, hi := 0, len(natives)
	for lo < hi {
		mid := (lo + hi) >> 1
		c := cmpFold(name, natives[mid].name)
		if c == 0 {
			return natives[mid].fn, true
		}
		if c < 0 {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return nil, false
}

func cmpFold(name, canon string) int {
	n, m := len(name), len(canon)
	lim := min(m, n)
	for i := range lim {
		a := name[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		b := canon[i]
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
	}
	if n < m {
		return -1
	}
	if n > m {
		return 1
	}
	return 0
}

func argString(z *zone.Zone, fn string, pos int, v types.Value) (string, error) {
	v = types.Deref(z, v)
	if v.Type == types.TypeString {
		return types.StringData(z, v), nil
	}
	return "", DateError{
		Class:   "TypeError",
		Message: fmt.Sprintf("%s(): Argument #%d must be of type string", fn, pos),
	}
}

func optionalTZArg(z *zone.Zone, args []types.Value, idx int) (*DateTimeZone, error) {
	if idx >= len(args) {
		return nil, nil
	}
	v := types.Deref(z, args[idx])
	if v.IsNull() || v.IsUndef() {
		return nil, nil
	}
	return TimeZoneFromObject(z, v)
}

func nativeDateCreate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_create() expects at most 2 arguments"}
	}
	dtStr := "now"
	if len(args) >= 1 && !types.Deref(z, args[0]).IsNull() {
		var err error
		dtStr, err = argString(z, "date_create", 1, args[0])
		if err != nil {
			return types.NewNull(), err
		}
	}
	tz, err := optionalTZArg(z, args, 1)
	if err != nil {
		return types.NewNull(), err
	}
	obj, _, err := NewDateTime(z, dtStr, tz)
	if err != nil {
		return types.NewBool(false), nil
	}
	return obj, nil
}

func nativeDateCreateImmutable(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_create_immutable() expects at most 2 arguments"}
	}
	dtStr := "now"
	if len(args) >= 1 && !types.Deref(z, args[0]).IsNull() {
		var err error
		dtStr, err = argString(z, "date_create_immutable", 1, args[0])
		if err != nil {
			return types.NewNull(), err
		}
	}
	tz, err := optionalTZArg(z, args, 1)
	if err != nil {
		return types.NewNull(), err
	}
	obj, _, err := NewDateTimeImmutable(z, dtStr, tz)
	if err != nil {
		return types.NewBool(false), nil
	}
	return obj, nil
}

func nativeDateCreateFromFormat(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_create_from_format() expects 2 or 3 arguments"}
	}
	format, err := argString(z, "date_create_from_format", 1, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	dtStr, err := argString(z, "date_create_from_format", 2, args[1])
	if err != nil {
		return types.NewNull(), err
	}
	tz, err := optionalTZArg(z, args, 2)
	if err != nil {
		return types.NewNull(), err
	}
	obj, _, err := CreateFromFormat(z, format, dtStr, tz)
	if err != nil {
		return types.NewBool(false), nil
	}
	return obj, nil
}

func nativeDateCreateImmutableFromFormat(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_create_immutable_from_format() expects 2 or 3 arguments"}
	}
	format, err := argString(z, "date_create_immutable_from_format", 1, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	dtStr, err := argString(z, "date_create_immutable_from_format", 2, args[1])
	if err != nil {
		return types.NewNull(), err
	}
	tz, err := optionalTZArg(z, args, 2)
	if err != nil {
		return types.NewNull(), err
	}
	obj, _, err := CreateImmutableFromFormat(z, format, dtStr, tz)
	if err != nil {
		return types.NewBool(false), nil
	}
	return obj, nil
}

func nativeDateFormat(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_format() expects exactly 2 arguments"}
	}
	dti, err := DateTimeInterfaceFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	format, err := argString(z, "date_format", 2, args[1])
	if err != nil {
		return types.NewNull(), err
	}
	return types.NewString(z, dti.Format(format), 0)
}

func nativeDateModify(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_modify() expects exactly 2 arguments"}
	}
	dt, err := DateTimeFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	mod, err := argString(z, "date_modify", 2, args[1])
	if err != nil {
		return types.NewNull(), err
	}
	if _, err := dt.Modify(mod); err != nil {
		return types.NewBool(false), nil
	}
	return types.RetainObject(z, dt.Object()), nil
}

func nativeDateAdd(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_add() expects exactly 2 arguments"}
	}
	dt, err := DateTimeFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	iv, err := IntervalFromObject(z, args[1])
	if err != nil {
		return types.NewNull(), err
	}
	if _, err := dt.Add(iv); err != nil {
		return types.NewNull(), err
	}
	return types.RetainObject(z, dt.Object()), nil
}

func nativeDateSub(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_sub() expects exactly 2 arguments"}
	}
	dt, err := DateTimeFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	iv, err := IntervalFromObject(z, args[1])
	if err != nil {
		return types.NewNull(), err
	}
	if _, err := dt.Sub(iv); err != nil {
		return types.NewNull(), err
	}
	return types.RetainObject(z, dt.Object()), nil
}

func nativeDateTimezoneGet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 1 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_timezone_get() expects exactly 1 argument"}
	}
	dti, err := DateTimeInterfaceFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	obj, _, err := dti.GetTimezone(z)
	if err != nil {
		return types.NewBool(false), nil
	}
	return obj, nil
}

func nativeDateTimezoneSet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_timezone_set() expects exactly 2 arguments"}
	}
	dt, err := DateTimeFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	tz, err := TimeZoneFromObject(z, args[1])
	if err != nil {
		return types.NewNull(), err
	}
	if _, err := dt.SetTimezone(tz); err != nil {
		return types.NewNull(), err
	}
	return types.RetainObject(z, dt.Object()), nil
}

func nativeDateOffsetGet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 1 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_offset_get() expects exactly 1 argument"}
	}
	dti, err := DateTimeInterfaceFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	return types.NewLong(dti.GetOffset()), nil
}

func nativeDateDiff(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) < 2 || len(args) > 3 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_diff() expects 2 or 3 arguments"}
	}
	base, err := DateTimeInterfaceFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	target, err := DateTimeInterfaceFromObject(z, args[1])
	if err != nil {
		return types.NewNull(), err
	}
	abs := false
	if len(args) == 3 {
		abs = types.ToBoolZ(z, args[2])
	}
	obj, _, err := base.Diff(z, target, abs)
	return obj, err
}

func nativeDateTimestampGet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 1 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_timestamp_get() expects exactly 1 argument"}
	}
	dti, err := DateTimeInterfaceFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	return types.NewLong(dti.GetTimestamp()), nil
}

func nativeDateTimestampSet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_timestamp_set() expects exactly 2 arguments"}
	}
	dt, err := DateTimeFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	ts := types.Deref(z, args[1]).ToLong()
	dt.SetTimestamp(ts)
	return types.RetainObject(z, dt.Object()), nil
}

func nativeDateDateSet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 4 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_date_set() expects exactly 4 arguments"}
	}
	dt, err := DateTimeFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	y := int(types.Deref(z, args[1]).ToLong())
	m := int(types.Deref(z, args[2]).ToLong())
	d := int(types.Deref(z, args[3]).ToLong())
	dt.SetDate(y, m, d)
	return types.RetainObject(z, dt.Object()), nil
}

func nativeDateTimeSet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) < 3 || len(args) > 5 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_time_set() expects between 3 and 5 arguments"}
	}
	dt, err := DateTimeFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	hr := int(types.Deref(z, args[1]).ToLong())
	min := int(types.Deref(z, args[2]).ToLong())
	sec := 0
	if len(args) >= 4 {
		sec = int(types.Deref(z, args[3]).ToLong())
	}
	us := 0
	if len(args) == 5 {
		us = int(types.Deref(z, args[4]).ToLong())
	}
	dt.SetTime(hr, min, sec, us)
	return types.RetainObject(z, dt.Object()), nil
}

func nativeDateISODateSet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) < 3 || len(args) > 4 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_isodate_set() expects 3 or 4 arguments"}
	}
	dt, err := DateTimeFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	y := int(types.Deref(z, args[1]).ToLong())
	w := int(types.Deref(z, args[2]).ToLong())
	dow := 1
	if len(args) == 4 {
		dow = int(types.Deref(z, args[3]).ToLong())
	}
	dt.SetISODate(y, w, dow)
	return types.RetainObject(z, dt.Object()), nil
}

func nativeTimezoneOpen(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 1 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "timezone_open() expects exactly 1 argument"}
	}
	tzStr, err := argString(z, "timezone_open", 1, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	obj, _, err := NewDateTimeZone(z, tzStr)
	if err != nil {
		return types.NewBool(false), nil
	}
	return obj, nil
}

func nativeTimezoneNameGet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 1 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "timezone_name_get() expects exactly 1 argument"}
	}
	tz, err := TimeZoneFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	return types.NewString(z, tz.GetName(), 0)
}

func nativeTimezoneOffsetGet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "timezone_offset_get() expects exactly 2 arguments"}
	}
	tz, err := TimeZoneFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	dti, err := DateTimeInterfaceFromObject(z, args[1])
	if err != nil {
		return types.NewNull(), err
	}
	return types.NewLong(tz.GetOffset(dti)), nil
}

func nativeTimezoneIdentifiersList(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "timezone_identifiers_list() expects at most 2 arguments"}
	}
	return ListIdentifiers(z)
}

func nativeDateIntervalCreateFromDateString(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 1 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_interval_create_from_date_string() expects exactly 1 argument"}
	}
	rel, err := argString(z, "date_interval_create_from_date_string", 1, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	obj, _, err := DateIntervalCreateFromDateString(z, rel)
	if err != nil {
		return types.NewBool(false), nil
	}
	return obj, nil
}

func nativeDateIntervalFormat(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_interval_format() expects exactly 2 arguments"}
	}
	iv, err := IntervalFromObject(z, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	fmtStr, err := argString(z, "date_interval_format", 2, args[1])
	if err != nil {
		return types.NewNull(), err
	}
	return types.NewString(z, iv.Format(fmtStr), 0)
}

func nativeDateDefaultTimezoneGet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 0 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_default_timezone_get() expects exactly 0 arguments"}
	}
	return types.NewString(z, DefaultTimezoneGet(z), 0)
}

func nativeDateDefaultTimezoneSet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 1 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "date_default_timezone_set() expects exactly 1 argument"}
	}
	tzID, err := argString(z, "date_default_timezone_set", 1, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	return types.NewBool(DefaultTimezoneSet(z, tzID)), nil
}

func nativeCheckdate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 3 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "checkdate() expects exactly 3 arguments"}
	}
	m := int(types.Deref(z, args[0]).ToLong())
	d := int(types.Deref(z, args[1]).ToLong())
	y := int(types.Deref(z, args[2]).ToLong())
	return types.NewBool(checkdateValid(m, d, y)), nil
}

func resolveTimestampInZone(z *zone.Zone, args []types.Value, idx int) (time.Time, int64) {
	_, loc, err := loadPHPLocation(DefaultTimezoneGet(z))
	if err != nil {
		loc = time.UTC
	}
	var ts int64
	if idx < len(args) && !types.Deref(z, args[idx]).IsNull() && !types.Deref(z, args[idx]).IsUndef() {
		ts = types.Deref(z, args[idx]).ToLong()
	} else {
		ts = time.Now().Unix()
	}
	return time.Unix(ts, 0).In(loc), ts
}

func setStringKey(z *zone.Zone, arr *types.Value, key string, val types.Value) error {
	kv, err := types.NewString(z, key, 0)
	if err != nil {
		return err
	}
	defer types.ReleaseString(z, &kv)
	return types.ArraySet(z, arr, kv, val, nil)
}

func nativeGetdate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 1 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "getdate() expects at most 1 argument"}
	}
	t, ts := resolveTimestampInZone(z, args, 0)
	y, m, d := t.Date()
	hr, min, sec := t.Clock()
	wday := int(t.Weekday())
	yday := t.YearDay() - 1

	arr, err := types.NewArray(z)
	if err != nil {
		return types.NewNull(), err
	}
	intEntries := [][2]any{
		{"seconds", int64(sec)},
		{"minutes", int64(min)},
		{"hours", int64(hr)},
		{"mday", int64(d)},
		{"wday", int64(wday)},
		{"mon", int64(m)},
		{"year", int64(y)},
		{"yday", int64(yday)},
	}
	for _, entry := range intEntries {
		if err := setStringKey(z, &arr, entry[0].(string), types.NewLong(entry[1].(int64))); err != nil {
			types.ReleaseArray(z, &arr)
			return types.NewNull(), err
		}
	}
	wdStr, err := types.NewString(z, longWeekdays[wday], 0)
	if err != nil {
		types.ReleaseArray(z, &arr)
		return types.NewNull(), err
	}
	err = setStringKey(z, &arr, "weekday", wdStr)
	types.ReleaseString(z, &wdStr)
	if err != nil {
		types.ReleaseArray(z, &arr)
		return types.NewNull(), err
	}

	monStr, err := types.NewString(z, longMonths[int(m)-1], 0)
	if err != nil {
		types.ReleaseArray(z, &arr)
		return types.NewNull(), err
	}
	err = setStringKey(z, &arr, "month", monStr)
	types.ReleaseString(z, &monStr)
	if err != nil {
		types.ReleaseArray(z, &arr)
		return types.NewNull(), err
	}

	if err := types.ArraySet(z, &arr, types.NewLong(0), types.NewLong(ts), nil); err != nil {
		types.ReleaseArray(z, &arr)
		return types.NewNull(), err
	}
	return arr, nil
}

func nativeIdate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "idate() expects 1 or 2 arguments"}
	}
	fmtStr, err := argString(z, "idate", 1, args[0])
	if err != nil {
		return types.NewNull(), err
	}
	if len(fmtStr) != 1 {
		return types.NewNull(), DateError{Class: "ValueError", Message: "idate(): Argument #1 ($format) must be a single character"}
	}
	t, _ := resolveTimestampInZone(z, args, 1)
	res, err := computeIDate(t, fmtStr[0])
	if err != nil {
		return types.NewNull(), err
	}
	return types.NewLong(res), nil
}

func nativeLocaltime(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 2 {
		return types.NewNull(), DateError{Class: "ArgumentCountError", Message: "localtime() expects at most 2 arguments"}
	}
	t, _ := resolveTimestampInZone(z, args, 0)
	assoc := false
	if len(args) == 2 && !types.Deref(z, args[1]).IsNull() {
		assoc = types.ToBoolZ(z, args[1])
	}
	y, m, d := t.Date()
	hr, min, sec := t.Clock()
	wday := int(t.Weekday())
	yday := t.YearDay() - 1
	isdst := int64(0)
	if isDSTActive(t) {
		isdst = 1
	}

	arr, err := types.NewArray(z)
	if err != nil {
		return types.NewNull(), err
	}
	vals := [9]int64{
		int64(sec),
		int64(min),
		int64(hr),
		int64(d),
		int64(m) - 1,
		int64(y - 1900),
		int64(wday),
		int64(yday),
		isdst,
	}
	if !assoc {
		for _, v := range vals {
			if err := types.ArrayAppend(z, &arr, types.NewLong(v)); err != nil {
				types.ReleaseArray(z, &arr)
				return types.NewNull(), err
			}
		}
		return arr, nil
	}
	keys := [9]string{
		"tm_sec",
		"tm_min",
		"tm_hour",
		"tm_mday",
		"tm_mon",
		"tm_year",
		"tm_wday",
		"tm_yday",
		"tm_isdst",
	}
	for i, k := range keys {
		if err := setStringKey(z, &arr, k, types.NewLong(vals[i])); err != nil {
			types.ReleaseArray(z, &arr)
			return types.NewNull(), err
		}
	}
	return arr, nil
}
