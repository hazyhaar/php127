package ext

import (
	"encoding/binary"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"code.hazyhaar.fr/devhoros/php127/datetime"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// localZone rend le fuseau par défaut de la requête, tenu par le paquet datetime (date_default_timezone_set).
func localZone(z *zone.Zone) *time.Location {
	name := datetime.DefaultTimezoneGet(z)
	switch name {
	case "UTC", "Z":
		return zoneUTC
	case "GMT":
		return zoneGMT
	}
	if loc, ok := zoneCache.Load(name); ok {
		return loc.(*time.Location)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	zoneCache.Store(strings.Clone(name), loc)
	return loc
}

// zoneUTC et zoneGMT nomment le fuseau nul de date() ; time.UTC reste réservé à gmdate(), dont « T » vaut GMT.
var (
	zoneUTC = time.FixedZone("UTC", 0)
	zoneGMT = time.FixedZone("GMT", 0)
)

// zoneCache évite une relecture de la base tz à chaque appel ; une *time.Location est immuable.
var zoneCache sync.Map

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
	return formatDate(z, args, "date", localZone(z))
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
		if t.Location() == time.UTC {
			return "GMT"
		}
		name, _ := t.Zone()
		return name
	case 'Z':
		_, off := t.Zone()
		return strconv.Itoa(off)
	case 'v':
		return pad(t.Nanosecond()/1e6, 3)
	case 'u':
		return pad(t.Nanosecond()/1e3, 6)
	case 'e':
		return t.Location().String()
	case 'I':
		if t.IsDST() {
			return "1"
		}
		return "0"
	case 'S':
		return ordinalSuffix(t.Day())
	case 't':
		return strconv.Itoa(time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day())
	case 'W':
		_, w := t.ISOWeek()
		return pad(w, 2)
	case 'o':
		y, _ := t.ISOWeek()
		return strconv.Itoa(y)
	case 'p':
		if _, off := t.Zone(); off == 0 {
			return "Z"
		}
		return offsetColon(t)
	default:
		return string(c)
	}
}

func ordinalSuffix(day int) string {
	if day >= 11 && day <= 13 {
		return "th"
	}
	switch day % 10 {
	case 1:
		return "st"
	case 2:
		return "nd"
	case 3:
		return "rd"
	default:
		return "th"
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
	return makeTime(z, args, "mktime", localZone(z))
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
	loc := localZone(z)
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

func nativeStrftime(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return strftimeNative(z, args, "strftime", false)
}

func nativeGMStrftime(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return strftimeNative(z, args, "gmstrftime", true)
}

func strftimeNative(z *zone.Zone, args []types.Value, name string, gmt bool) (types.Value, error) {
	if err := arity(name, len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	format, err := asString(z, args[0], name, "1", "format")
	if err != nil {
		return types.Value{}, err
	}
	ts := time.Now().Unix()
	if len(args) == 2 && !missing(deref(z, args[1])) {
		if ts, err = asInt(z, args[1], name, "2", "timestamp"); err != nil {
			return types.Value{}, err
		}
	}
	if format == "" {
		return types.NewBool(false), nil
	}
	loc := time.UTC
	if !gmt {
		loc = localZone(z)
	}
	return newStr(z, cStrftime(time.Unix(ts, 0).In(loc), format, gmt))
}

// cStrftime suit strftime(3) de la glibc en locale C.
func cStrftime(t time.Time, format string, gmt bool) string {
	var b strings.Builder
	b.Grow(len(format) + 16)
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' || i+1 == len(format) {
			b.WriteByte(c)
			continue
		}
		i++
		b.WriteString(strftimeSpec(t, format[i], gmt))
	}
	return b.String()
}

func spacePad(n int) string {
	if n < 10 {
		return " " + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

func strftimeSpec(t time.Time, c byte, gmt bool) string {
	switch c {
	case 'a':
		return dayShort[t.Weekday()]
	case 'A':
		return dayLong[t.Weekday()]
	case 'b', 'h':
		return monShort[t.Month()-1]
	case 'B':
		return monLong[t.Month()-1]
	case 'c':
		return cStrftime(t, "%a %b %e %H:%M:%S %Y", gmt)
	case 'C':
		return pad(t.Year()/100, 2)
	case 'd':
		return pad(t.Day(), 2)
	case 'D', 'x':
		return cStrftime(t, "%m/%d/%y", gmt)
	case 'e':
		return spacePad(t.Day())
	case 'F':
		return cStrftime(t, "%Y-%m-%d", gmt)
	case 'g':
		y, _ := t.ISOWeek()
		return pad(y%100, 2)
	case 'G':
		y, _ := t.ISOWeek()
		return strconv.Itoa(y)
	case 'H':
		return pad(t.Hour(), 2)
	case 'I':
		return pad(hour12(t), 2)
	case 'j':
		return pad(t.YearDay(), 3)
	case 'k':
		return spacePad(t.Hour())
	case 'l':
		return spacePad(hour12(t))
	case 'm':
		return pad(int(t.Month()), 2)
	case 'M':
		return pad(t.Minute(), 2)
	case 'n':
		return "\n"
	case 'p':
		if t.Hour() < 12 {
			return "AM"
		}
		return "PM"
	case 'P':
		if t.Hour() < 12 {
			return "am"
		}
		return "pm"
	case 'r':
		return cStrftime(t, "%I:%M:%S %p", gmt)
	case 'R':
		return cStrftime(t, "%H:%M", gmt)
	case 's':
		return strconv.FormatInt(t.Unix(), 10)
	case 'S':
		return pad(t.Second(), 2)
	case 't':
		return "\t"
	case 'T', 'X':
		return cStrftime(t, "%H:%M:%S", gmt)
	case 'u':
		if t.Weekday() == time.Sunday {
			return "7"
		}
		return strconv.Itoa(int(t.Weekday()))
	case 'U':
		return pad((t.YearDay()+6-int(t.Weekday()))/7, 2)
	case 'V':
		_, w := t.ISOWeek()
		return pad(w, 2)
	case 'w':
		return strconv.Itoa(int(t.Weekday()))
	case 'W':
		return pad((t.YearDay()+6-(int(t.Weekday())+6)%7)/7, 2)
	case 'y':
		return pad(t.Year()%100, 2)
	case 'Y':
		return strconv.Itoa(t.Year())
	case 'z':
		return offsetCompact(t)
	case 'Z':
		if gmt {
			return "GMT"
		}
		name, _ := t.Zone()
		return name
	case '%':
		return "%"
	default:
		return "%" + string(c)
	}
}

// ---- fuseaux : timezone_transitions_get, timezone_location_get ----

// tzDir est la base tz du système, celle que lisent PHP 8.3 (correctif de la base système de Debian et
// Ubuntu) et time.LoadLocation.
const tzDir = "/usr/share/zoneinfo/"

// Bornes du parcours des transitions : goTimeMin et goTimeMax tiennent dans time.Time sans débordement,
// très en deçà et au-delà de toute transition tz. tzPosixHorizon (2100-01-01T00:00:00Z) borne l'extension
// par la règle POSIX : PHP 8.3 la poursuit jusqu'à la fin demandée et épuise sa mémoire pour une fin de
// l'ordre de PHP_INT_MAX. tzDefaultEnd est la fin que PHP 8.3 applique quand l'argument est omis (INT32_MAX
// dans php_date.c, non PHP_INT_MAX comme l'annonce la signature).
const (
	goTimeMin      = -1 << 59
	goTimeMax      = 1 << 59
	tzPosixHorizon = 4102444800
	tzDefaultEnd   = math.MaxInt32
)

// tzOperand résout l'argument DateTimeZone|string. isID distingue un fuseau à identifiant (zone_type 3) d'un
// décalage fixe (« +02:00 ») ou d'une abréviation (« GMT », « EST »), pour lesquels PHP rend false ; comme
// timelib_parse_zone, seul « UTC » écrit exactement ainsi est un identifiant parmi les abréviations.
// ok est faux pour une chaîne vide.
func tzOperand(z *zone.Zone, v types.Value, fn string) (name string, isID, ok bool, err error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeObject:
		tz, terr := datetime.TimeZoneFromObject(z, v)
		if terr != nil {
			return "", false, false, typeErr(fn, "1", "object", "DateTimeZone", typeKind(z, v))
		}
		name = tz.GetName()
	case types.TypeString:
		name = types.StringData(z, v)
	default:
		return "", false, false, typeErr(fn, "1", "object", "DateTimeZone", typeKind(z, v))
	}
	if name == "" {
		return "", false, false, nil
	}
	if name[0] == '+' || name[0] == '-' {
		return name, false, true, nil
	}
	if _, abbr := lookupTZAbbreviation(name); abbr && name != "UTC" {
		return name, false, true, nil
	}
	return name, true, true, nil
}

// loadZone charge un fuseau par identifiant, avec le même cache que localZone.
func loadZone(name string) (*time.Location, bool) {
	if loc, ok := zoneCache.Load(name); ok {
		return loc.(*time.Location), true
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}
	zoneCache.Store(strings.Clone(name), loc)
	return loc, true
}

// tzifLastTransitions garde, par identifiant, l'instant de la dernière transition de la table du fichier TZif.
var tzifLastTransitions sync.Map

type tzifLast struct {
	ts int64
	ok bool
}

// tzifLastTransition lit la dernière transition de la table du fichier TZif de name ; au-delà, PHP 8.3
// prolonge la liste par la règle POSIX du fichier. ok est faux si le fichier est illisible ou sans transition.
func tzifLastTransition(name string) (int64, bool) {
	if v, found := tzifLastTransitions.Load(name); found {
		e := v.(tzifLast)
		return e.ts, e.ok
	}
	var e tzifLast
	if filepath.IsLocal(name) {
		if data, err := os.ReadFile(tzDir + name); err == nil {
			e.ts, e.ok = parseTZifLast(data)
		}
	}
	tzifLastTransitions.Store(strings.Clone(name), e)
	return e.ts, e.ok
}

// parseTZifLast suit RFC 8536 : en-tête de 44 octets, bloc de données v1 (instants sur 32 bits), puis pour
// une version 2 ou plus un second en-tête et un bloc aux instants sur 64 bits.
func parseTZifLast(data []byte) (int64, bool) {
	const hdr = 44
	counts := func(off int) (isut, isstd, leap, timecnt, typecnt, charcnt int, ok bool) {
		if len(data) < off+hdr || string(data[off:off+4]) != "TZif" {
			return 0, 0, 0, 0, 0, 0, false
		}
		c := func(i int) int { return int(binary.BigEndian.Uint32(data[off+20+4*i:])) }
		return c(0), c(1), c(2), c(3), c(4), c(5), true
	}
	isut, isstd, leap, timecnt, typecnt, charcnt, ok := counts(0)
	if !ok {
		return 0, false
	}
	if data[4] < '2' {
		if timecnt == 0 || len(data) < hdr+4*timecnt {
			return 0, false
		}
		return int64(int32(binary.BigEndian.Uint32(data[hdr+4*(timecnt-1):]))), true
	}
	off := hdr + 5*timecnt + 6*typecnt + charcnt + 8*leap + isstd + isut
	if _, _, _, timecnt, _, _, ok = counts(off); !ok || timecnt == 0 {
		return 0, false
	}
	start := off + hdr
	if len(data) < start+8*timecnt {
		return 0, false
	}
	return int64(binary.BigEndian.Uint64(data[start+8*(timecnt-1):])), true
}

// isoLargeYear formate un instant Unix en UTC comme le format « X-m-d\TH:i:sP » de PHP, sur toute
// l'étendue de int64 : le calendrier grégorien proleptique se calcule par jours civils (algorithme de
// H. Hinnant), sans passer par time.Time qui déborde aux extrémités.
func isoLargeYear(ts int64) string {
	days := ts / 86400
	sec := ts % 86400
	if sec < 0 {
		sec += 86400
		days--
	}
	y, m, d := civilFromDays(days)
	var b strings.Builder
	switch {
	case y < 0:
		b.WriteByte('-')
		b.WriteString(pad64(-y, 4))
	case y > 9999:
		b.WriteByte('+')
		b.WriteString(pad64(y, 4))
	default:
		b.WriteString(pad64(y, 4))
	}
	b.WriteByte('-')
	b.WriteString(pad(int(m), 2))
	b.WriteByte('-')
	b.WriteString(pad(int(d), 2))
	b.WriteByte('T')
	b.WriteString(pad(int(sec/3600), 2))
	b.WriteByte(':')
	b.WriteString(pad(int(sec/60%60), 2))
	b.WriteByte(':')
	b.WriteString(pad(int(sec%60), 2))
	b.WriteString("+00:00")
	return b.String()
}

// civilFromDays convertit un nombre de jours depuis 1970-01-01 en année, mois et jour grégoriens.
func civilFromDays(days int64) (y, m, d int64) {
	days += 719468
	era := days / 146097
	if days < 0 && days%146097 != 0 {
		era--
	}
	doe := days - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y = yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d = doy - (153*mp+2)/5 + 1
	m = mp + 3
	if m > 12 {
		m -= 12
	}
	if m <= 2 {
		y++
	}
	return y, m, d
}

func clampGoTime(ts int64) int64 {
	return min(max(ts, goTimeMin), goTimeMax)
}

// nativeTimezoneTransitionsGet suit timezone_transitions_get de PHP 8.3 : une entrée initiale à
// timestampBegin (l'entrée nominale du fuseau pour PHP_INT_MIN), puis les transitions de la table du
// fichier TZif strictement comprises entre timestampBegin et timestampEnd, puis celles de la règle POSIX
// jusqu'à timestampEnd inclus. Les instants viennent de time.Time.ZoneBounds, qui rend le changement
// suivant exact, table et règle POSIX comprises ; au-delà de la table, ZoneBounds borne aussi chaque année
// civile, et ces bornes sans changement de zone sont sautées. Une chaîne d'identifiant est acceptée à la
// place d'un objet DateTimeZone.
func nativeTimezoneTransitionsGet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "timezone_transitions_get"
	if err := arity(fn, len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	name, isID, ok, err := tzOperand(z, args[0], fn)
	if err != nil {
		return types.Value{}, err
	}
	begin, err := optInt(z, args, 1, math.MinInt64, fn, "2", "timestampBegin")
	if err != nil {
		return types.Value{}, err
	}
	end, err := optInt(z, args, 2, tzDefaultEnd, fn, "3", "timestampEnd")
	if err != nil {
		return types.Value{}, err
	}
	if !ok || !isID {
		return types.NewBool(false), nil
	}
	loc, ok := loadZone(name)
	if !ok {
		return types.NewBool(false), nil
	}
	tableEnd, hasTable := tzifLastTransition(name)
	if !hasTable {
		tableEnd = math.MaxInt64
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	cursor := clampGoTime(begin)
	prev := time.Unix(cursor, 0).In(loc)
	if err = appendTransition(z, &out, begin, prev); err != nil {
		return types.Value{}, err
	}
	for steps := 0; steps < 1<<20; steps++ {
		_, next := time.Unix(cursor, 0).In(loc).ZoneBounds()
		if next.IsZero() {
			break
		}
		ts := next.Unix()
		if ts <= cursor {
			// Hors table, dans le dernier jour d'une année civile, ZoneBounds rend une fin antérieure à
			// l'instant sondé (mesuré : Europe/Paris, 2240524801 → fin 2240524800). Le parcours sonde alors
			// un jour plus loin et reprend au début, exact, de la zone qui y règne.
			probe := cursor + 86400
			if probe > tzPosixHorizon {
				break
			}
			start, _ := time.Unix(probe, 0).In(loc).ZoneBounds()
			if start.Unix() <= cursor {
				cursor = probe
				continue
			}
			ts = start.Unix()
			next = start
		}
		if ts > tzPosixHorizon {
			break
		}
		cursor = ts
		at := next.In(loc)
		if ts <= tableEnd {
			if ts >= end {
				break
			}
		} else {
			if ts > end {
				break
			}
			if sameZone(prev, at) {
				continue
			}
		}
		if err = appendTransition(z, &out, ts, at); err != nil {
			return types.Value{}, err
		}
		prev = at
	}
	return out, nil
}

func sameZone(a, b time.Time) bool {
	an, ao := a.Zone()
	bn, bo := b.Zone()
	return an == bn && ao == bo && a.IsDST() == b.IsDST()
}

func appendTransition(z *zone.Zone, out *types.Value, ts int64, at time.Time) error {
	abbr, offset := at.Zone()
	e, err := newArray(z)
	if err != nil {
		return err
	}
	if err = setStrKey(z, &e, "ts", types.NewLong(ts)); err != nil {
		return err
	}
	tv, err := newStr(z, isoLargeYear(ts))
	if err != nil {
		return err
	}
	if err = setStrKey(z, &e, "time", tv); err != nil {
		return err
	}
	if err = setStrKey(z, &e, "offset", types.NewLong(int64(offset))); err != nil {
		return err
	}
	if err = setStrKey(z, &e, "isdst", types.NewBool(at.IsDST())); err != nil {
		return err
	}
	av, err := newStr(z, abbr)
	if err != nil {
		return err
	}
	if err = setStrKey(z, &e, "abbr", av); err != nil {
		return err
	}
	return appendVal(z, out, e)
}

type zoneLocation struct {
	country  string
	lat, lon float64
	comments string
}

// zoneTab lit une fois zone.tab, la table dont PHP 8.3 tire timezone_location_get sous la base système
// (un seul code pays par ligne) ; zone1970.tab, à codes multiples, sert de repli en ne gardant que le premier.
var zoneTab = sync.OnceValue(func() map[string]zoneLocation {
	out := make(map[string]zoneLocation)
	for _, file := range []string{"zone.tab", "zone1970.tab"} {
		data, err := os.ReadFile(tzDir + file)
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			if line == "" || line[0] == '#' {
				continue
			}
			f := strings.Split(line, "\t")
			if len(f) < 3 {
				continue
			}
			lat, lon, ok := parseISO6709(f[1])
			if !ok {
				continue
			}
			cc, _, _ := strings.Cut(f[0], ",")
			loc := zoneLocation{country: cc, lat: lat, lon: lon}
			if len(f) > 3 {
				loc.comments = f[3]
			}
			out[f[2]] = loc
		}
		if len(out) > 0 {
			break
		}
	}
	return out
})

// parseISO6709 lit ±DDMM[SS]±DDDMM[SS] ; chaque coordonnée est tronquée au cent-millième de degré, comme
// la base système de PHP (mesuré bit à bit sur les 418 lignes de zone.tab).
func parseISO6709(s string) (lat, lon float64, ok bool) {
	i := strings.IndexAny(s[min(1, len(s)):], "+-") + 1
	if i <= 0 {
		return 0, 0, false
	}
	lat, ok1 := iso6709Coord(s[:i], 2)
	lon, ok2 := iso6709Coord(s[i:], 3)
	return lat, lon, ok1 && ok2
}

func iso6709Coord(s string, degDigits int) (float64, bool) {
	if len(s) != 1+degDigits+2 && len(s) != 1+degDigits+4 {
		return 0, false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	num := func(a, b int) float64 {
		n, _ := strconv.Atoi(s[a:b])
		return float64(n)
	}
	deg := num(1, 1+degDigits) + num(1+degDigits, 3+degDigits)/60
	if len(s) > 3+degDigits {
		deg += num(3+degDigits, 5+degDigits) / 3600
	}
	v := float64(int64(deg*100000)) / 100000
	if s[0] == '-' {
		v = -v
	}
	return v, s[0] == '+' || s[0] == '-'
}

// nativeTimezoneLocationGet rend l'emplacement de zone.tab, ou la fiche « ?? » de PHP pour un fuseau
// absent de la table (UTC, Etc/GMT+5) ; false pour un décalage fixe. Une chaîne d'identifiant est acceptée
// à la place d'un objet DateTimeZone.
func nativeTimezoneLocationGet(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "timezone_location_get"
	if err := arity(fn, len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	name, isID, ok, err := tzOperand(z, args[0], fn)
	if err != nil {
		return types.Value{}, err
	}
	if !ok || !isID {
		return types.NewBool(false), nil
	}
	if _, ok = loadZone(name); !ok {
		return types.NewBool(false), nil
	}
	loc, found := zoneTab()[name]
	if !found {
		loc = zoneLocation{country: "??", comments: "?"}
	}
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	cc, err := newStr(z, loc.country)
	if err != nil {
		return types.Value{}, err
	}
	if err = setStrKey(z, &out, "country_code", cc); err != nil {
		return types.Value{}, err
	}
	if err = setStrKey(z, &out, "latitude", types.NewDouble(loc.lat)); err != nil {
		return types.Value{}, err
	}
	if err = setStrKey(z, &out, "longitude", types.NewDouble(loc.lon)); err != nil {
		return types.Value{}, err
	}
	cm, err := newStr(z, loc.comments)
	if err != nil {
		return types.Value{}, err
	}
	if err = setStrKey(z, &out, "comments", cm); err != nil {
		return types.Value{}, err
	}
	return out, nil
}

// ---- date_parse_from_format ----

// tzAbbreviation porte, pour une abréviation de fuseau, le décalage et le drapeau d'heure d'été que
// date_parse_from_format rend sous zone_type 2 : le décalage est celui de l'heure normale (décalage de la
// table de timelib moins une heure si l'abréviation est d'été).
type tzAbbreviation struct {
	name string
	zone int64
	dst  bool
}

// tzAbbreviations reprend, pour chaque clé de timezone_abbreviations_list() de PHP 8.3, la première
// entrée que la recherche de timelib retient (date_parse_from_format("T", clé) sous PHP 8.3), triée par nom.
var tzAbbreviations = [...]tzAbbreviation{
	{"a", 3600, false},
	{"acdt", 34200, true},
	{"acst", 34200, false},
	{"addt", -10800, true},
	{"adt", -14400, true},
	{"aedt", 36000, true},
	{"aest", 36000, false},
	{"ahdt", -36000, true},
	{"ahst", -36000, false},
	{"akdt", -32400, true},
	{"akst", -32400, false},
	{"amt", -13840, false},
	{"apt", -14400, true},
	{"ast", -14400, false},
	{"awdt", 28800, true},
	{"awst", 28800, false},
	{"awt", -14400, true},
	{"b", 7200, false},
	{"bdst", 3600, true},
	{"bdt", -39600, true},
	{"bmt", -14309, false},
	{"bst", 0, true},
	{"c", 10800, false},
	{"cast", 34200, false},
	{"cat", 7200, false},
	{"cddt", -18000, true},
	{"cdt", -21600, true},
	{"cemt", 7200, true},
	{"cest", 3600, true},
	{"cet", 3600, false},
	{"chst", 36000, false},
	{"cmt", -15408, false},
	{"cpt", -21600, true},
	{"cst", -21600, false},
	{"cwt", -21600, true},
	{"d", 14400, false},
	{"dmt", -1521, false},
	{"e", 18000, false},
	{"eat", 10800, false},
	{"eddt", -14400, true},
	{"edt", -18000, true},
	{"eest", 7200, true},
	{"eet", 7200, false},
	{"emt", -26248, false},
	{"ept", -18000, true},
	{"est", -18000, false},
	{"ewt", -18000, true},
	{"f", 21600, false},
	{"ffmt", -14660, false},
	{"fmt", -4056, false},
	{"g", 25200, false},
	{"gdt", 36000, true},
	{"gmt", 0, false},
	{"gst", 36000, false},
	{"h", 28800, false},
	{"hdt", -37800, true},
	{"hkst", 28800, true},
	{"hkt", 28800, false},
	{"hmt", -19776, false},
	{"hpt", -37800, true},
	{"hst", -36000, false},
	{"hwt", -37800, true},
	{"i", 32400, false},
	{"iddt", 10800, true},
	{"idt", 7200, true},
	{"imt", 25025, false},
	{"ist", 7200, false},
	{"jdt", 32400, true},
	{"jmt", 8440, false},
	{"jst", 32400, false},
	{"k", 36000, false},
	{"kdt", 32400, true},
	{"kmt", 5736, false},
	{"kst", 30600, false},
	{"l", 39600, false},
	{"lst", 5794, true},
	{"m", 43200, false},
	{"mddt", -21600, true},
	{"mdst", 12679, true},
	{"mdt", -25200, true},
	{"mest", 3600, true},
	{"met", 3600, false},
	{"mmt", 9017, false},
	{"mpt", -25200, true},
	{"msd", 10800, true},
	{"msk", 10800, false},
	{"mst", -25200, false},
	{"mwt", -25200, true},
	{"n", -3600, false},
	{"nddt", -9000, true},
	{"ndt", -12652, true},
	{"npt", -12600, true},
	{"nst", -12600, false},
	{"nwt", -12600, true},
	{"nzdt", 43200, true},
	{"nzmt", 41400, false},
	{"nzst", 43200, false},
	{"o", -7200, false},
	{"p", -10800, false},
	{"pddt", -25200, true},
	{"pdt", -28800, true},
	{"pkst", 18000, true},
	{"pkt", 18000, false},
	{"plmt", 25590, false},
	{"pmt", -13236, false},
	{"ppmt", -17340, false},
	{"ppt", -28800, true},
	{"pst", -28800, false},
	{"pwt", -28800, true},
	{"q", -14400, false},
	{"qmt", -18840, false},
	{"r", -18000, false},
	{"rmt", 5794, false},
	{"s", -21600, false},
	{"sast", 7200, false},
	{"sdmt", -16800, false},
	{"sjmt", -20173, false},
	{"smt", -13884, false},
	{"sst", -39600, false},
	{"t", -25200, false},
	{"tbmt", 10751, false},
	{"tmt", 12344, false},
	{"u", -28800, false},
	{"uct", 0, false},
	{"utc", 0, false},
	{"v", -32400, false},
	{"w", -36000, false},
	{"wast", 3600, true},
	{"wat", 3600, false},
	{"wemt", 3600, true},
	{"west", 0, true},
	{"wet", 0, false},
	{"wib", 25200, false},
	{"wit", 32400, false},
	{"wita", 28800, false},
	{"wmt", 5040, false},
	{"x", -39600, false},
	{"y", -43200, false},
	{"yddt", -28800, true},
	{"ydt", -32400, true},
	{"ypt", -32400, true},
	{"yst", -32400, false},
	{"ywt", -32400, true},
	{"z", 0, false},
}

func lookupTZAbbreviation(word string) (tzAbbreviation, bool) {
	w := strings.ToLower(word)
	lo, hi := 0, len(tzAbbreviations)
	for lo < hi {
		mid := (lo + hi) >> 1
		switch {
		case tzAbbreviations[mid].name < w:
			lo = mid + 1
		case tzAbbreviations[mid].name > w:
			hi = mid
		default:
			return tzAbbreviations[mid], true
		}
	}
	return tzAbbreviation{}, false
}

// tzIDIndex recense, en minuscules, les identifiants de la base système acceptés par PHP 8.3 : chaque
// fichier TZif sous tzDir hors des arbres posix/ et right/. La recherche d'identifiant de PHP ignore la casse.
var tzIDIndex = sync.OnceValue(func() map[string]bool {
	out := make(map[string]bool)
	root := os.DirFS(tzDir)
	_ = fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == "posix" || path == "right" {
				return fs.SkipDir
			}
			return nil
		}
		f, err := root.Open(path)
		if err != nil {
			return nil
		}
		var magic [4]byte
		n, _ := io.ReadFull(f, magic[:])
		_ = f.Close()
		if n == 4 && string(magic[:]) == "TZif" {
			out[strings.ToLower(path)] = true
		}
		return nil
	})
	return out
})

func knownTZID(word string) bool {
	return word != "" && tzIDIndex()[strings.ToLower(word)]
}

const (
	pffUnset     = math.MinInt64
	pffZoneNone  = 0
	pffZoneOff   = 1
	pffZoneAbbr  = 2
	pffZoneID    = 3
	pffMaxSigned = 24
)

// pffMessages garde les messages d'avertissement ou d'erreur indexés par position, dans l'ordre de
// première insertion ; une seconde insertion à la même position remplace le message, comme add_index_string.
type pffMessages struct {
	count int
	pos   []int
	text  map[int]string
}

func (m *pffMessages) add(pos int, msg string) {
	m.count++
	if m.text == nil {
		m.text = make(map[int]string)
	}
	if _, ok := m.text[pos]; !ok {
		m.pos = append(m.pos, pos)
	}
	m.text[pos] = msg
}

// pffState suit timelib_time et le conteneur d'erreurs de timelib_parse_from_format (PHP 8.3).
type pffState struct {
	s                    string
	p                    int
	y, m, d, h, i, sec   int64
	us                   int64
	z                    int64
	dst                  bool
	isLocal              bool
	zoneType             int
	tzAbbr, tzID         string
	haveRel, haveWeekday bool
	relWeekday           int64
	warnings, errors     pffMessages
}

func (st *pffState) at(i int) byte {
	if i < len(st.s) {
		return st.s[i]
	}
	return 0
}

func (st *pffState) cur() byte { return st.at(st.p) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// getNr suit timelib_get_nr_ex : saute tout ce qui n'est pas un chiffre, puis lit au plus maxLen chiffres.
func (st *pffState) getNr(maxLen int) (int64, int) {
	for !isDigit(st.cur()) {
		if st.cur() == 0 {
			return pffUnset, 0
		}
		st.p++
	}
	begin := st.p
	for isDigit(st.cur()) && st.p-begin < maxLen {
		st.p++
	}
	n, _ := strconv.ParseInt(st.s[begin:st.p], 10, 64)
	return n, st.p - begin
}

// getSignedNr suit timelib_get_signed_nr : le dernier signe rencontré avant le premier chiffre s'applique.
func (st *pffState) getSignedNr(maxLen int) int64 {
	neg := false
	for !isDigit(st.cur()) {
		switch st.cur() {
		case 0:
			st.errors.add(0, "Found unexpected data")
			return 0
		case '+':
			neg = false
		case '-':
			neg = true
		}
		st.p++
	}
	start := st.p
	for isDigit(st.cur()) && st.p-start < maxLen {
		st.p++
	}
	text := st.s[start:st.p]
	if neg {
		text = "-" + text
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		st.errors.add(0, "Number out of range")
		return 0
	}
	return n
}

func (st *pffState) checkNumber(begin int, signed bool) {
	c := st.cur()
	if !isDigit(c) && (!signed || (c != '+' && c != '-')) {
		st.errors.add(begin, "Unexpected data found.")
	}
}

// relUnits reprend timelib_relunit_lookup : D et l acceptent toute unité relative et en retiennent le multiplicateur.
var relUnits = map[string]int64{
	"ms": 1000, "msec": 1000, "msecs": 1000, "millisecond": 1000, "milliseconds": 1000,
	"µs": 1, "usec": 1, "usecs": 1, "µsec": 1, "µsecs": 1, "microsecond": 1, "microseconds": 1,
	"sec": 1, "secs": 1, "second": 1, "seconds": 1, "min": 1, "mins": 1, "minute": 1, "minutes": 1,
	"hour": 1, "hours": 1, "day": 1, "days": 1, "week": 7, "weeks": 7,
	"fortnight": 14, "fortnights": 14, "forthnight": 14, "forthnights": 14,
	"month": 1, "months": 1, "year": 1, "years": 1,
	"mondays": 1, "monday": 1, "mon": 1, "tuesdays": 2, "tuesday": 2, "tue": 2,
	"wednesdays": 3, "wednesday": 3, "wed": 3, "thursdays": 4, "thursday": 4, "thu": 4,
	"fridays": 5, "friday": 5, "fri": 5, "saturdays": 6, "saturday": 6, "sat": 6,
	"sundays": 0, "sunday": 0, "sun": 0, "weekday": 1, "weekdays": 1,
}

// monthNames reprend timelib_month_lookup : noms complets, abréviations et chiffres romains.
var monthNames = map[string]int64{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12,
	"january": 1, "february": 2, "march": 3, "april": 4, "june": 6, "july": 7, "august": 8, "september": 9,
	"october": 10, "november": 11, "december": 12,
	"i": 1, "ii": 2, "iii": 3, "iv": 4, "v": 5, "vi": 6, "vii": 7, "viii": 8, "ix": 9, "x": 10, "xi": 11, "xii": 12,
}

func (st *pffState) lookupRelunit() (int64, bool) {
	begin := st.p
	for {
		c := st.cur()
		if c == 0 || strings.IndexByte(" ,\t;:/.-()", c) >= 0 {
			break
		}
		st.p++
	}
	v, ok := relUnits[strings.ToLower(st.s[begin:st.p])]
	return v, ok
}

func (st *pffState) lookupMonth() (int64, bool) {
	begin := st.p
	for c := st.cur(); (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'); c = st.cur() {
		st.p++
	}
	v, ok := monthNames[strings.ToLower(st.s[begin:st.p])]
	return v, ok
}

// meridian suit timelib_meridian_with_check : avance jusqu'à la première lettre a ou p, puis exige « m »
// ou « .m. » ; rend le correctif à ajouter à l'heure.
func (st *pffState) meridian(h int64) (int64, bool) {
	for st.cur() != 0 && strings.IndexByte("AaPp", st.cur()) < 0 {
		st.p++
	}
	c := st.cur()
	if c == 0 {
		return 0, false
	}
	var delta int64
	if c == 'a' || c == 'A' {
		if h == 12 {
			delta = -12
		}
	} else if h != 12 {
		delta = 12
	}
	st.p++
	switch st.cur() {
	case '.':
		st.p++
		if c := st.cur(); c != 'm' && c != 'M' {
			return 0, false
		}
		st.p++
		if st.cur() != '.' {
			return 0, false
		}
		st.p++
	case 'm', 'M':
		st.p++
	default:
		return 0, false
	}
	return delta, true
}

// eatSpaces suit timelib_eat_spaces : espaces, tabulations, espaces insécables U+00A0 et U+202F.
func (st *pffState) eatSpaces() {
	for {
		switch {
		case st.cur() == ' ' || st.cur() == '\t':
			st.p++
		case st.at(st.p) == 0xe2 && st.at(st.p+1) == 0x80 && st.at(st.p+2) == 0xaf:
			st.p += 3
		case st.at(st.p) == 0xc2 && st.at(st.p+1) == 0xa0:
			st.p += 2
		default:
			return
		}
	}
}

// parseTZCorrection suit timelib_parse_tz_cor : H, HH, H:M, H:MM, HH:M, HHMM, HH:MM, HHMMSS, HH:MM:SS.
func (st *pffState) parseTZCorrection() (int64, bool) {
	begin := st.p
	for isDigit(st.cur()) || st.cur() == ':' {
		st.p++
	}
	t := st.s[begin:st.p]
	lead := func(s string) int64 {
		k := 0
		for k < len(s) && isDigit(s[k]) {
			k++
		}
		n, _ := strconv.ParseInt(s[:k], 10, 64)
		return n
	}
	switch len(t) {
	case 1, 2:
		return lead(t) * 3600, true
	case 3, 4:
		switch {
		case t[1] == ':':
			return lead(t)*3600 + lead(t[2:])*60, true
		case t[2] == ':':
			return lead(t)*3600 + lead(t[3:])*60, true
		default:
			n := lead(t)
			return n/100*3600 + n%100*60, true
		}
	case 5:
		if t[2] == ':' {
			return lead(t)*3600 + lead(t[3:])*60, true
		}
	case 6:
		n := lead(t)
		return n/10000*3600 + n/100%100*60 + n%100, true
	case 8:
		if t[2] == ':' && t[5] == ':' {
			return lead(t)*3600 + lead(t[3:])*60 + lead(t[6:]), true
		}
	}
	return 0, false
}

// parseZone suit timelib_parse_zone : décalage signé, abréviation de la table, puis identifiant de la base.
func (st *pffState) parseZone() bool {
	for c := st.cur(); c == ' ' || c == '\t' || c == '('; c = st.cur() {
		st.p++
	}
	if st.at(st.p) == 'G' && st.at(st.p+1) == 'M' && st.at(st.p+2) == 'T' && (st.at(st.p+3) == '+' || st.at(st.p+3) == '-') {
		st.p += 3
	}
	var found bool
	switch st.cur() {
	case '+', '-':
		neg := st.cur() == '-'
		st.p++
		st.isLocal, st.zoneType, st.dst = true, pffZoneOff, false
		var off int64
		off, found = st.parseTZCorrection()
		if neg {
			off = -off
		}
		st.z = off
	default:
		st.isLocal = true
		begin := st.p
		for c := st.cur(); (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || isDigit(c) || c == '/' || c == '_' || c == '-' || c == '+'; c = st.cur() {
			st.p++
		}
		word := st.s[begin:st.p]
		abbr, isAbbr := lookupTZAbbreviation(word)
		if isAbbr {
			st.zoneType, st.dst, st.z = pffZoneAbbr, abbr.dst, abbr.zone
			st.tzAbbr = strings.ToUpper(word)
		}
		found = isAbbr
		if (!isAbbr || word == "UTC") && knownTZID(word) {
			st.zoneType, st.tzID, found = pffZoneID, word, true
		}
	}
	for st.cur() == ')' {
		st.p++
	}
	return found
}

func (st *pffState) resetFields() {
	st.y, st.m, st.d, st.h, st.i, st.sec, st.us = 1970, 1, 1, 0, 0, 0, 0
}

func (st *pffState) resetUnsetFields() {
	for _, f := range []struct {
		p   *int64
		def int64
	}{{&st.y, 1970}, {&st.m, 1}, {&st.d, 1}, {&st.h, 0}, {&st.i, 0}, {&st.sec, 0}, {&st.us, 0}} {
		if *f.p == pffUnset {
			*f.p = f.def
		}
	}
}

// unixToGMT suit timelib_unixtime2gmt.
func (st *pffState) unixToGMT(ts int64) {
	days := ts / 86400
	rem := ts % 86400
	if rem < 0 {
		rem += 86400
		days--
	}
	st.y, st.m, st.d = civilFromDays(days)
	st.h, st.i, st.sec = rem/3600, rem/60%60, rem%60
}

// parseFromFormat suit timelib_parse_from_format de PHP 8.3 (format et chaîne déjà débarrassés d'octet nul).
func parseFromFormat(format, s string) *pffState {
	st := &pffState{s: s, y: pffUnset, m: pffUnset, d: pffUnset, h: pffUnset, i: pffUnset, sec: pffUnset, us: pffUnset, z: pffUnset}
	allowExtra := false
	f := 0
	for f < len(format) && st.cur() != 0 {
		begin := st.p
		switch c := format[f]; c {
		case 'D', 'l':
			if mult, ok := st.lookupRelunit(); ok {
				st.haveRel, st.haveWeekday, st.relWeekday = true, true, mult
			} else {
				st.errors.add(begin, "A textual day could not be found")
			}
		case 'd', 'j':
			st.checkNumber(begin, false)
			if st.d, _ = st.getNr(2); st.d == pffUnset {
				st.errors.add(begin, "A two digit day could not be found")
			}
		case 'S':
			if c := st.cur(); c == ' ' || (c >= '\t' && c <= '\r') {
				break
			}
			if len(st.s)-st.p >= 2 {
				switch strings.ToLower(st.s[st.p : st.p+2]) {
				case "nd", "rd", "st", "th":
					st.p += 2
				}
			}
		case 'z':
			st.checkNumber(begin, false)
			if st.y == pffUnset {
				st.errors.add(begin, "A 'day of year' can only come after a year has been found")
			}
			n, _ := st.getNr(3)
			if n == pffUnset {
				st.errors.add(begin, "A three digit day-of-year could not be found")
				break
			}
			if st.y != pffUnset {
				t := time.Date(int(st.y), time.January, int(n)+1, 0, 0, 0, 0, time.UTC)
				st.y, st.m, st.d = int64(t.Year()), int64(t.Month()), int64(t.Day())
			}
		case 'm', 'n':
			st.checkNumber(begin, false)
			if st.m, _ = st.getNr(2); st.m == pffUnset {
				st.errors.add(begin, "A two digit month could not be found")
			}
		case 'M', 'F':
			if n, ok := st.lookupMonth(); ok {
				st.m = n
			} else {
				st.errors.add(begin, "A textual month could not be found")
			}
		case 'y':
			st.checkNumber(begin, false)
			n, length := st.getNr(2)
			if n == pffUnset {
				st.y = n
				st.errors.add(begin, "A two digit year could not be found")
				break
			}
			if length < 4 && n < 100 {
				if n < 70 {
					n += 2000
				} else {
					n += 1900
				}
			}
			st.y = n
		case 'Y':
			st.checkNumber(begin, false)
			if st.y, _ = st.getNr(4); st.y == pffUnset {
				st.errors.add(begin, "A four digit year could not be found")
			}
		case 'X', 'x':
			st.checkNumber(begin, true)
			st.y = st.getSignedNr(19)
		case 'g', 'h':
			st.checkNumber(begin, false)
			if st.h, _ = st.getNr(2); st.h == pffUnset {
				st.errors.add(begin, "A two digit hour could not be found")
				break
			}
			if st.h > 12 {
				st.errors.add(begin, "Hour cannot be higher than 12")
			}
		case 'G', 'H':
			st.checkNumber(begin, false)
			if st.h, _ = st.getNr(2); st.h == pffUnset {
				st.errors.add(begin, "A two digit hour could not be found")
			}
		case 'a', 'A':
			// Sans heure, PHP 8.3 consigne l'erreur puis lit tout de même le méridien, qui avance la lecture.
			hourSet := st.h != pffUnset
			if !hourSet {
				st.errors.add(begin, "Meridian can only come after an hour has been found")
			}
			delta, ok := st.meridian(st.h)
			if !ok {
				st.errors.add(begin, "A meridian could not be found")
				break
			}
			if hourSet {
				st.h += delta
			}
		case 'i':
			st.checkNumber(begin, false)
			if n, length := st.getNr(2); n == pffUnset || length != 2 {
				st.errors.add(begin, "A two digit minute could not be found")
			} else {
				st.i = n
			}
		case 's':
			st.checkNumber(begin, false)
			if n, length := st.getNr(2); n == pffUnset || length != 2 {
				st.errors.add(begin, "A two digit second could not be found")
			} else {
				st.sec = n
			}
		case 'v', 'u':
			st.checkNumber(begin, false)
			width, msg := 6, "A six digit microsecond could not be found"
			if c == 'v' {
				width, msg = 3, "A three digit millisecond could not be found"
			}
			n, _ := st.getNr(width)
			scanned := st.p - begin
			if n == pffUnset || scanned < 1 {
				st.errors.add(begin, msg)
				break
			}
			f := float64(n) * math.Pow(10, float64(width-scanned))
			if c == 'v' {
				f *= 1000
			}
			st.us = int64(f)
		case ' ':
			st.eatSpaces()
		case 'U':
			st.checkNumber(begin, true)
			st.unixToGMT(st.getSignedNr(pffMaxSigned))
			st.isLocal, st.zoneType, st.z, st.dst = true, pffZoneOff, 0, false
		case 'e', 'P', 'p', 'T', 'O':
			if !st.parseZone() {
				st.errors.add(begin, "The timezone could not be found in the database")
			}
		case '#':
			if strings.IndexByte(";:/.,-()", st.cur()) >= 0 {
				st.p++
			} else {
				st.errors.add(begin, "The separation symbol ([;:/.,-]) could not be found")
			}
		case ';', ':', '/', '.', ',', '-', '(', ')':
			if st.cur() == c {
				st.p++
			} else {
				st.errors.add(begin, "The separation symbol could not be found")
			}
		case '!':
			st.resetFields()
		case '|':
			st.resetUnsetFields()
		case '?':
			st.p++
		case '\\':
			if f+1 >= len(format) {
				st.errors.add(begin, "Escaped character expected")
				break
			}
			f++
			if st.cur() == format[f] {
				st.p++
			} else {
				st.errors.add(begin, "The escaped character could not be found")
			}
		case '*':
			st.p++
			for c := st.cur(); c != 0 && strings.IndexByte(" \t.,:;/-0123456789", c) < 0; c = st.cur() {
				st.p++
			}
		case '+':
			allowExtra = true
		default:
			if c != st.cur() {
				st.errors.add(begin, "The format separator does not match")
			}
			st.p++
		}
		f++
	}
	if st.cur() != 0 {
		if allowExtra {
			st.warnings.add(st.p, "Trailing data")
		} else {
			st.errors.add(st.p, "Trailing data")
		}
	}
tail:
	for ; f < len(format); f++ {
		switch format[f] {
		case '!':
			st.resetFields()
		case '|':
			st.resetUnsetFields()
		case '+':
		default:
			st.errors.add(st.p, "Not enough data available to satisfy format")
			break tail
		}
	}
	if st.h != pffUnset || st.i != pffUnset || st.sec != pffUnset || st.us != pffUnset {
		for _, p := range []*int64{&st.h, &st.i, &st.sec, &st.us} {
			if *p == pffUnset {
				*p = 0
			}
		}
	}
	if st.h != pffUnset && st.i != pffUnset && st.sec != pffUnset &&
		(st.h < 0 || st.h > 23 || st.i < 0 || st.i > 59 || st.sec < 0 || st.sec > 59) {
		st.warnings.add(st.p, "The parsed time was invalid")
	}
	if st.y != pffUnset && st.m != pffUnset && st.d != pffUnset &&
		(st.m < 1 || st.m > 12 || st.d < 1 || st.d > int64(daysIn(st.y, st.m))) {
		st.warnings.add(st.p, "The parsed date was invalid")
	}
	return st
}

func daysIn(y, m int64) int {
	return time.Date(int(y), time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func nativeDateParseFromFormat(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "date_parse_from_format"
	if err := arity(fn, len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	format, err := asString(z, args[0], fn, "1", "format")
	if err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[1], fn, "2", "datetime")
	if err != nil {
		return types.Value{}, err
	}
	if strings.IndexByte(s, 0) >= 0 {
		return types.Value{}, ValueError{fn + "(): Argument #2 ($datetime) must not contain any null bytes"}
	}
	if k := strings.IndexByte(format, 0); k >= 0 {
		format = format[:k]
	}
	return parsedTimeArray(z, parseFromFormat(format, s))
}

// parsedTimeArray suit php_date_do_return_parsed_time de PHP 8.3.
func parsedTimeArray(z *zone.Zone, st *pffState) (types.Value, error) {
	out, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	set := func(key string, v types.Value) {
		if err == nil {
			err = setStrKey(z, &out, key, v)
		}
	}
	field := func(n int64) types.Value {
		if n == pffUnset {
			return types.NewBool(false)
		}
		return types.NewLong(n)
	}
	setStr := func(key, text string) {
		if err != nil {
			return
		}
		var v types.Value
		if v, err = newStr(z, text); err == nil {
			set(key, v)
		}
	}
	messages := func(m *pffMessages) types.Value {
		a, aerr := newArray(z)
		if aerr != nil {
			err = aerr
			return types.NewNull()
		}
		for _, pos := range m.pos {
			v, verr := newStr(z, m.text[pos])
			if verr == nil {
				verr = types.ArraySet(z, &a, types.NewLong(int64(pos)), v, nil)
			}
			if verr != nil {
				err = verr
				break
			}
		}
		return a
	}
	set("year", field(st.y))
	set("month", field(st.m))
	set("day", field(st.d))
	set("hour", field(st.h))
	set("minute", field(st.i))
	set("second", field(st.sec))
	if st.us == pffUnset {
		set("fraction", types.NewBool(false))
	} else {
		set("fraction", types.NewDouble(float64(st.us)/1000000.0))
	}
	set("warning_count", types.NewLong(int64(st.warnings.count)))
	set("warnings", messages(&st.warnings))
	set("error_count", types.NewLong(int64(st.errors.count)))
	set("errors", messages(&st.errors))
	set("is_localtime", types.NewBool(st.isLocal))
	if st.isLocal {
		set("zone_type", types.NewLong(int64(st.zoneType)))
		switch st.zoneType {
		case pffZoneOff:
			set("zone", field(st.z))
			set("is_dst", types.NewBool(st.dst))
		case pffZoneID:
			if st.tzAbbr != "" {
				setStr("tz_abbr", st.tzAbbr)
			}
			setStr("tz_id", st.tzID)
		case pffZoneAbbr:
			set("zone", field(st.z))
			set("is_dst", types.NewBool(st.dst))
			setStr("tz_abbr", st.tzAbbr)
		}
	}
	if st.haveRel {
		rel, rerr := newArray(z)
		if rerr != nil {
			return types.Value{}, rerr
		}
		for _, k := range []string{"year", "month", "day", "hour", "minute", "second"} {
			if rerr = setStrKey(z, &rel, k, types.NewLong(0)); rerr != nil {
				return types.Value{}, rerr
			}
		}
		if st.haveWeekday {
			if rerr = setStrKey(z, &rel, "weekday", types.NewLong(st.relWeekday)); rerr != nil {
				return types.Value{}, rerr
			}
		}
		set("relative", rel)
	}
	if err != nil {
		return types.Value{}, err
	}
	return out, nil
}
