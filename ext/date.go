package ext

import (
	"io"
	"strconv"
	"strings"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func localZone() *time.Location { return time.Local }

func nativeTime(_ *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("time", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewLong(time.Now().Unix()), nil
}

func nativeMicrotime(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("microtime", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	now := time.Now()
	if optBool(z, args, 0) {
		return types.NewDouble(float64(now.UnixNano()) / 1e9), nil
	}
	frac := now.Nanosecond() / 10
	return newStr(z, "0."+pad(frac, 8)+" "+strconv.FormatInt(now.Unix(), 10))
}

func nativeHrtime(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("hrtime", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	ns := max(time.Since(hrtimeBase).Nanoseconds(), 0)
	if optBool(z, args, 0) {
		return types.NewLong(ns), nil
	}
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	if err = appendVal(z, &a, types.NewLong(ns/1e9)); err != nil {
		return types.Value{}, err
	}
	if err = appendVal(z, &a, types.NewLong(ns%1e9)); err != nil {
		return types.Value{}, err
	}
	return a, nil
}

var hrtimeBase = time.Now()

func nativeDate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return formatDate(z, args, "date", localZone())
}

func nativeGMDate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return formatDate(z, args, "gmdate", time.UTC)
}

func formatDate(z *zone.Zone, args []types.Value, name string, loc *time.Location) (types.Value, error) {
	if err := arity(name, len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	format, err := asString(z, args[0], name, "1", "format")
	if err != nil {
		return types.Value{}, err
	}
	ts := time.Now().Unix()
	if len(args) >= 2 && !args[1].IsUndef() && !deref(z, args[1]).IsNull() {
		ts, err = asInt(z, args[1], name, "2", "timestamp")
		if err != nil {
			return types.Value{}, err
		}
	}
	return newStr(z, phpFormat(time.Unix(ts, 0).In(loc), format))
}

func pad(n, width int) string {
	s := strconv.Itoa(n)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

func pad64(n int64, width int) string {
	s := strconv.FormatInt(n, 10)
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	for len(s) < width {
		s = "0" + s
	}
	if neg {
		return "-" + s
	}
	return s
}

var (
	dayShort = [...]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	dayLong  = [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	monShort = [...]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	monLong  = [...]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}
)

func phpFormat(t time.Time, format string) string {
	var b strings.Builder
	b.Grow(len(format) + 16)
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c == '\\' {
			if i+1 < len(format) {
				i++
				b.WriteByte(format[i])
			} else {
				b.WriteByte('\\')
			}
			continue
		}
		b.WriteString(phpSpecifier(t, c))
	}
	return b.String()
}

func phpSpecifier(t time.Time, c byte) string {
	switch c {
	case 'Y':
		return pad64(int64(t.Year()), 4)
	case 'y':
		return pad(t.Year()%100, 2)
	case 'm':
		return pad(int(t.Month()), 2)
	case 'n':
		return strconv.Itoa(int(t.Month()))
	case 'd':
		return pad(t.Day(), 2)
	case 'j':
		return strconv.Itoa(t.Day())
	case 'H':
		return pad(t.Hour(), 2)
	case 'G':
		return strconv.Itoa(t.Hour())
	case 'h':
		return pad(hour12(t), 2)
	case 'g':
		return strconv.Itoa(hour12(t))
	case 'i':
		return pad(t.Minute(), 2)
	case 's':
		return pad(t.Second(), 2)
	case 'A':
		if t.Hour() < 12 {
			return "AM"
		}
		return "PM"
	case 'a':
		if t.Hour() < 12 {
			return "am"
		}
		return "pm"
	case 'D':
		return dayShort[t.Weekday()]
	case 'l':
		return dayLong[t.Weekday()]
	case 'F':
		return monLong[t.Month()-1]
	case 'M':
		return monShort[t.Month()-1]
	case 'w':
		return strconv.Itoa(int(t.Weekday()))
	case 'N':
		n := int(t.Weekday())
		if n == 0 {
			return "7"
		}
		return strconv.Itoa(n)
	case 'z':
		return strconv.Itoa(t.YearDay() - 1)
	case 'L':
		if isLeap(t.Year()) {
			return "1"
		}
		return "0"
	case 'U':
		return strconv.FormatInt(t.Unix(), 10)
	case 'c':
		return phpFormat(t, "Y-m-d\\TH:i:sP")
	case 'r':
		return phpFormat(t, "D, d M Y H:i:s O")
	case 'P':
		return offsetColon(t)
	case 'O':
		return offsetCompact(t)
	case 'T':
		name, off := t.Zone()
		if off == 0 && (name == "UTC" || name == "" || name == "GMT") {
			return "GMT"
		}
		return name
	case 'Z':
		_, off := t.Zone()
		return strconv.Itoa(off)
	case 'v':
		return pad(t.Nanosecond()/1e6, 3)
	default:
		return string(c)
	}
}

func hour12(t time.Time) int {
	h := t.Hour() % 12
	if h == 0 {
		return 12
	}
	return h
}

func isLeap(y int) bool {
	return y%4 == 0 && (y%100 != 0 || y%400 == 0)
}

func offsetColon(t time.Time) string {
	_, off := t.Zone()
	sign := "+"
	if off < 0 {
		sign = "-"
		off = -off
	}
	h, m := off/3600, (off%3600)/60
	return sign + pad(h, 2) + ":" + pad(m, 2)
}

func offsetCompact(t time.Time) string {
	_, off := t.Zone()
	sign := "+"
	if off < 0 {
		sign = "-"
		off = -off
	}
	h, m := off/3600, (off%3600)/60
	return sign + pad(h, 2) + pad(m, 2)
}

func nativeMktime(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return makeTime(z, args, "mktime", localZone())
}

func nativeGMMktime(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return makeTime(z, args, "gmmktime", time.UTC)
}

func makeTime(z *zone.Zone, args []types.Value, name string, loc *time.Location) (types.Value, error) {
	if err := arity(name, len(args), 1, 6); err != nil {
		return types.Value{}, err
	}
	now := time.Now().In(loc)
	hour, err := asInt(z, args[0], name, "1", "hour")
	if err != nil {
		return types.Value{}, err
	}
	minute := int64(now.Minute())
	second := int64(now.Second())
	month := int64(now.Month())
	day := int64(now.Day())
	year := int64(now.Year())
	if len(args) >= 2 && !args[1].IsUndef() && !deref(z, args[1]).IsNull() {
		minute, err = asInt(z, args[1], name, "2", "minute")
		if err != nil {
			return types.Value{}, err
		}
	}
	if len(args) >= 3 && !args[2].IsUndef() && !deref(z, args[2]).IsNull() {
		second, err = asInt(z, args[2], name, "3", "second")
		if err != nil {
			return types.Value{}, err
		}
	}
	if len(args) >= 4 && !args[3].IsUndef() && !deref(z, args[3]).IsNull() {
		month, err = asInt(z, args[3], name, "4", "month")
		if err != nil {
			return types.Value{}, err
		}
	}
	if len(args) >= 5 && !args[4].IsUndef() && !deref(z, args[4]).IsNull() {
		day, err = asInt(z, args[4], name, "5", "day")
		if err != nil {
			return types.Value{}, err
		}
	}
	if len(args) >= 6 && !args[5].IsUndef() && !deref(z, args[5]).IsNull() {
		year, err = asInt(z, args[5], name, "6", "year")
		if err != nil {
			return types.Value{}, err
		}
	}
	y := phpYear(int(year))
	tm := time.Date(y, time.Month(month), int(day), int(hour), int(minute), int(second), 0, loc)
	return types.NewLong(tm.Unix()), nil
}

func phpYear(y int) int {
	if y >= 0 && y <= 69 {
		return 2000 + y
	}
	if y >= 70 && y <= 99 {
		return 1900 + y
	}
	return y
}

func nativeStrtotime(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("strtotime", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	text, err := asString(z, args[0], "strtotime", "1", "datetime")
	if err != nil {
		return types.Value{}, err
	}
	loc := localZone()
	base := time.Now().In(loc)
	if len(args) >= 2 && !args[1].IsUndef() && !deref(z, args[1]).IsNull() {
		ts, err := asInt(z, args[1], "strtotime", "2", "baseTimestamp")
		if err != nil {
			return types.Value{}, err
		}
		base = time.Unix(ts, 0).In(loc)
	}
	got, ok := parseStrtotime(strings.TrimSpace(text), base, loc)
	if !ok {
		return types.NewBool(false), nil
	}
	return types.NewLong(got.Unix()), nil
}

func parseStrtotime(text string, base time.Time, loc *time.Location) (time.Time, bool) {
	if text == "" {
		return time.Time{}, false
	}
	if tm, ok := parseAbsolute(text, loc); ok {
		return tm, true
	}
	return applyRelative(text, base)
}

func parseAbsolute(text string, loc *time.Location) (time.Time, bool) {
	zoneLayouts := []string{
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05Z0700",
		"2006-01-02T15:04:05Z0700",
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 MST",
		"02 Jan 2006 15:04:05 -0700",
		time.RFC1123Z,
		time.RFC1123,
	}
	for _, layout := range zoneLayouts {
		if tm, err := time.Parse(layout, text); err == nil {
			return tm, true
		}
	}
	localLayouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02",
		"20060102150405",
		"20060102",
	}
	for _, layout := range localLayouts {
		if tm, err := time.ParseInLocation(layout, text, loc); err == nil {
			return tm, true
		}
	}
	return time.Time{}, false
}

func applyRelative(text string, base time.Time) (time.Time, bool) {
	s := strings.TrimSpace(text)
	if strings.EqualFold(s, "now") {
		return base, true
	}
	advanced := false
	for len(s) > 0 {
		s = strings.TrimSpace(s)
		if s == "" {
			break
		}
		sign := 1
		if s[0] == '+' {
			s = s[1:]
		} else if s[0] == '-' {
			sign = -1
			s = s[1:]
		}
		s = strings.TrimSpace(s)
		i := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == 0 {
			return time.Time{}, false
		}
		n, err := strconv.Atoi(s[:i])
		if err != nil {
			return time.Time{}, false
		}
		n *= sign
		s = strings.TrimSpace(s[i:])
		j := 0
		for j < len(s) && ((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
			j++
		}
		if j == 0 {
			return time.Time{}, false
		}
		unit := strings.ToLower(s[:j])
		s = s[j:]
		next, ok := addUnit(base, n, unit)
		if !ok {
			return time.Time{}, false
		}
		base = next
		advanced = true
	}
	if !advanced {
		return time.Time{}, false
	}
	return base, true
}

func addUnit(base time.Time, n int, unit string) (time.Time, bool) {
	switch unit {
	case "sec", "secs", "second", "seconds":
		return base.Add(time.Duration(n) * time.Second), true
	case "min", "mins", "minute", "minutes":
		return base.Add(time.Duration(n) * time.Minute), true
	case "hour", "hours":
		return base.Add(time.Duration(n) * time.Hour), true
	case "day", "days":
		return base.AddDate(0, 0, n), true
	case "week", "weeks":
		return base.AddDate(0, 0, 7*n), true
	case "month", "months":
		return base.AddDate(0, n, 0), true
	case "year", "years":
		return base.AddDate(n, 0, 0), true
	default:
		return time.Time{}, false
	}
}
