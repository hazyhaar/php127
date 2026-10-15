package datetime

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

var shortWeekdays = [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
var longWeekdays = [7]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
var shortMonths = [12]string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
var longMonths = [12]string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}

func isLeapYear(y int) bool {
	return (y%4 == 0 && y%100 != 0) || y%400 == 0
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

func swatchBeats(t time.Time) int {
	sec := (t.Unix() + 3600) % 86400
	if sec < 0 {
		sec += 86400
	}
	return int((sec * 10) / 864)
}

func isDSTActive(t time.Time) bool {
	return t.IsDST()
}

func formatOffset(offsetSec int, colon bool, zulu bool) string {
	if zulu && offsetSec == 0 {
		return "Z"
	}
	sign := '+'
	if offsetSec < 0 {
		sign = '-'
		offsetSec = -offsetSec
	}
	hh := offsetSec / 3600
	mm := (offsetSec % 3600) / 60
	if colon {
		return fmt.Sprintf("%c%02d:%02d", sign, hh, mm)
	}
	return fmt.Sprintf("%c%02d%02d", sign, hh, mm)
}

func formatPHPDate(t time.Time, tzName string, layout string) string {
	var b strings.Builder
	b.Grow(len(layout) + 24)

	y, m, d := t.Date()
	hr, min, sec := t.Clock()
	nsec := t.Nanosecond()
	wday := int(t.Weekday())
	isoWday := wday
	if isoWday == 0 {
		isoWday = 7
	}
	isoYear, isoWeek := t.ISOWeek()
	zoneAbbr, offsetSec := t.Zone()
	if zoneAbbr == "" {
		zoneAbbr = tzName
	}

	for i := 0; i < len(layout); i++ {
		ch := layout[i]
		if ch == '\\' {
			if i+1 < len(layout) {
				i++
				b.WriteByte(layout[i])
			}
			continue
		}
		switch ch {
		case 'd':
			fmt.Fprintf(&b, "%02d", d)
		case 'D':
			b.WriteString(shortWeekdays[wday])
		case 'j':
			fmt.Fprintf(&b, "%d", d)
		case 'l':
			b.WriteString(longWeekdays[wday])
		case 'N':
			fmt.Fprintf(&b, "%d", isoWday)
		case 'S':
			b.WriteString(ordinalSuffix(d))
		case 'w':
			fmt.Fprintf(&b, "%d", wday)
		case 'z':
			fmt.Fprintf(&b, "%d", t.YearDay()-1)
		case 'W':
			fmt.Fprintf(&b, "%02d", isoWeek)
		case 'F':
			b.WriteString(longMonths[int(m)-1])
		case 'm':
			fmt.Fprintf(&b, "%02d", int(m))
		case 'M':
			b.WriteString(shortMonths[int(m)-1])
		case 'n':
			fmt.Fprintf(&b, "%d", int(m))
		case 't':
			fmt.Fprintf(&b, "%d", daysInMonth(y, int(m)))
		case 'L':
			if isLeapYear(y) {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		case 'o':
			fmt.Fprintf(&b, "%04d", isoYear)
		case 'X':
			if y < 0 {
				fmt.Fprintf(&b, "-%04d", -y)
			} else {
				fmt.Fprintf(&b, "+%04d", y)
			}
		case 'x':
			if y < 0 {
				fmt.Fprintf(&b, "-%04d", -y)
			} else if y >= 10000 {
				fmt.Fprintf(&b, "+%04d", y)
			} else {
				fmt.Fprintf(&b, "%04d", y)
			}
		case 'Y':
			if y < 0 {
				fmt.Fprintf(&b, "-%04d", -y)
			} else {
				fmt.Fprintf(&b, "%04d", y)
			}
		case 'y':
			yy := y % 100
			if yy < 0 {
				yy = -yy
			}
			fmt.Fprintf(&b, "%02d", yy)
		case 'a':
			if hr < 12 {
				b.WriteString("am")
			} else {
				b.WriteString("pm")
			}
		case 'A':
			if hr < 12 {
				b.WriteString("AM")
			} else {
				b.WriteString("PM")
			}
		case 'B':
			fmt.Fprintf(&b, "%03d", swatchBeats(t))
		case 'g':
			h12 := hr % 12
			if h12 == 0 {
				h12 = 12
			}
			fmt.Fprintf(&b, "%d", h12)
		case 'G':
			fmt.Fprintf(&b, "%d", hr)
		case 'h':
			h12 := hr % 12
			if h12 == 0 {
				h12 = 12
			}
			fmt.Fprintf(&b, "%02d", h12)
		case 'H':
			fmt.Fprintf(&b, "%02d", hr)
		case 'i':
			fmt.Fprintf(&b, "%02d", min)
		case 's':
			fmt.Fprintf(&b, "%02d", sec)
		case 'u':
			fmt.Fprintf(&b, "%06d", nsec/1000)
		case 'v':
			fmt.Fprintf(&b, "%03d", nsec/1000000)
		case 'e':
			if tzName != "" {
				b.WriteString(tzName)
			} else {
				b.WriteString(zoneAbbr)
			}
		case 'I':
			if isDSTActive(t) {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		case 'O':
			b.WriteString(formatOffset(offsetSec, false, false))
		case 'P':
			b.WriteString(formatOffset(offsetSec, true, false))
		case 'p':
			b.WriteString(formatOffset(offsetSec, true, true))
		case 'T':
			if strings.HasPrefix(zoneAbbr, "+") || strings.HasPrefix(zoneAbbr, "-") {
				b.WriteString("GMT" + zoneAbbr)
			} else {
				b.WriteString(zoneAbbr)
			}
		case 'Z':
			fmt.Fprintf(&b, "%d", offsetSec)
		case 'c':
			fmt.Fprintf(&b, "%04d-%02d-%02dT%02d:%02d:%02d%s", y, int(m), d, hr, min, sec, formatOffset(offsetSec, true, false))
		case 'r':
			fmt.Fprintf(&b, "%s, %02d %s %04d %02d:%02d:%02d %s", shortWeekdays[wday], d, shortMonths[int(m)-1], y, hr, min, sec, formatOffset(offsetSec, false, false))
		case 'U':
			fmt.Fprintf(&b, "%d", t.Unix())
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// CreateFromFormat implémente DateTime::createFromFormat(string $format, string $datetime, ?DateTimeZone $timezone = null).
func CreateFromFormat(z *zone.Zone, format, datetimeStr string, tz *DateTimeZone) (types.Value, *DateTime, error) {
	tzName, loc, err := resolveConstructTZ(z, tz)
	if err != nil {
		return types.Value{}, nil, err
	}
	parsed, outTZName, outLoc, err := parsePHPFormat(format, datetimeStr, time.Now().In(loc), tzName, loc)
	if err != nil {
		return types.Value{}, nil, err
	}
	return newDateTimeFromTime(z, parsed, outTZName, outLoc)
}

// CreateImmutableFromFormat implémente DateTimeImmutable::createFromFormat(string $format, string $datetime, ?DateTimeZone $timezone = null).
func CreateImmutableFromFormat(z *zone.Zone, format, datetimeStr string, tz *DateTimeZone) (types.Value, *DateTimeImmutable, error) {
	tzName, loc, err := resolveConstructTZ(z, tz)
	if err != nil {
		return types.Value{}, nil, err
	}
	parsed, outTZName, outLoc, err := parsePHPFormat(format, datetimeStr, time.Now().In(loc), tzName, loc)
	if err != nil {
		return types.Value{}, nil, err
	}
	return newDateTimeImmutableFromTime(z, parsed, outTZName, outLoc)
}

func parsePHPFormat(format, input string, now time.Time, defaultTZName string, defaultLoc *time.Location) (time.Time, string, *time.Location, error) {
	year, month, day := now.Date()
	y := year
	m := int(month)
	d := day
	hr, min, sec := now.Clock()
	nsec := 0
	tzName := defaultTZName
	loc := defaultLoc

	hasTimeField := false
	resetPipe := false
	isPM := -1
	has12Hour := false
	var unixTS *int64

	pos := 0
	for fi := 0; fi < len(format); fi++ {
		fc := format[fi]
		if fc == '\\' {
			if fi+1 >= len(format) || pos >= len(input) || input[pos] != format[fi+1] {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse time string from format"}
			}
			fi++
			pos++
			continue
		}
		switch fc {
		case '!':
			y, m, d = 1970, 1, 1
			hr, min, sec, nsec = 0, 0, 0, 0
		case '|':
			resetPipe = true
		case '+':
			// Ignore trailing data.
			return finalizeParsedTime(y, m, d, hr, min, sec, nsec, hasTimeField, resetPipe, has12Hour, isPM, unixTS, tzName, loc), tzName, loc, nil
		case ' ':
			if pos >= len(input) || !isASCIISpaceByte(input[pos]) {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse time string from format"}
			}
			for pos < len(input) && isASCIISpaceByte(input[pos]) {
				pos++
			}
		case '#', ';', ':', '/', '.', ',', '-', '(', ')':
			if pos >= len(input) {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse time string from format"}
			}
			pos++
		case '?':
			if pos >= len(input) {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse time string from format"}
			}
			pos++
		case '*':
			for pos < len(input) && !isASCIISpaceByte(input[pos]) && input[pos] != '-' && input[pos] != '/' && input[pos] != ':' {
				pos++
			}
		case 'Y':
			val, nextPos, ok := scanDigits(input, pos, 1, 4)
			if !ok {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse year"}
			}
			y, pos = val, nextPos
		case 'y':
			val, nextPos, ok := scanDigits(input, pos, 1, 2)
			if !ok {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse 2-digit year"}
			}
			if val >= 70 {
				y = 1900 + val
			} else {
				y = 2000 + val
			}
			pos = nextPos
		case 'm', 'n':
			val, nextPos, ok := scanDigits(input, pos, 1, 2)
			if !ok || val < 1 || val > 12 {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse month"}
			}
			m, pos = val, nextPos
		case 'M', 'F':
			val, nextPos, ok := scanMonthName(input, pos)
			if !ok {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse month name"}
			}
			m, pos = val, nextPos
		case 'd', 'j':
			val, nextPos, ok := scanDigits(input, pos, 1, 2)
			if !ok || val < 1 || val > 31 {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse day"}
			}
			d, pos = val, nextPos
		case 'H', 'G':
			val, nextPos, ok := scanDigits(input, pos, 1, 2)
			if !ok || val < 0 || val > 24 {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse hour"}
			}
			hr, pos, hasTimeField = val, nextPos, true
		case 'h', 'g':
			val, nextPos, ok := scanDigits(input, pos, 1, 2)
			if !ok || val < 1 || val > 12 {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse 12-hour"}
			}
			hr, pos, hasTimeField, has12Hour = val, nextPos, true, true
		case 'i':
			val, nextPos, ok := scanDigits(input, pos, 1, 2)
			if !ok || val < 0 || val > 59 {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse minute"}
			}
			min, pos, hasTimeField = val, nextPos, true
		case 's':
			val, nextPos, ok := scanDigits(input, pos, 1, 2)
			if !ok || val < 0 || val > 60 {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse second"}
			}
			sec, pos, hasTimeField = val, nextPos, true
		case 'u':
			start := pos
			_, nextPos, ok := scanDigits(input, pos, 1, 6)
			if !ok {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse microseconds"}
			}
			frac := input[start:nextPos]
			for len(frac) < 6 {
				frac += "0"
			}
			us, _ := strconv.Atoi(frac)
			nsec = us * 1000
			pos, hasTimeField = nextPos, true
		case 'v':
			start := pos
			_, nextPos, ok := scanDigits(input, pos, 1, 3)
			if !ok {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse milliseconds"}
			}
			frac := input[start:nextPos]
			for len(frac) < 3 {
				frac += "0"
			}
			ms, _ := strconv.Atoi(frac)
			nsec = ms * 1000000
			pos, hasTimeField = nextPos, true
		case 'a', 'A':
			if pos+2 > len(input) {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse meridiem"}
			}
			mer := strings.ToLower(input[pos : pos+2])
			switch mer {
			case "am":
				isPM = 0
			case "pm":
				isPM = 1
			default:
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse meridiem"}
			}
			pos += 2
		case 'U':
			neg := false
			if pos < len(input) && (input[pos] == '-' || input[pos] == '+') {
				neg = input[pos] == '-'
				pos++
			}
			start := pos
			for pos < len(input) && input[pos] >= '0' && input[pos] <= '9' {
				pos++
			}
			if pos == start {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse timestamp"}
			}
			tsVal, err := strconv.ParseInt(input[start:pos], 10, 64)
			if err != nil {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse timestamp"}
			}
			if neg {
				tsVal = -tsVal
			}
			unixTS = &tsVal
		case 'e', 'O', 'P', 'p', 'T':
			start := pos
			for pos < len(input) && !isASCIISpaceByte(input[pos]) {
				pos++
			}
			token := input[start:pos]
			tzn, l, err := loadPHPLocation(token)
			if err != nil {
				return time.Time{}, "", nil, err
			}
			tzName, loc = tzn, l
		default:
			if pos >= len(input) || input[pos] != fc {
				return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Failed to parse literal character in format"}
			}
			pos++
		}
	}
	if pos != len(input) {
		return time.Time{}, "", nil, DateError{Class: "ValueError", Message: "Trailing data in datetime string"}
	}
	return finalizeParsedTime(y, m, d, hr, min, sec, nsec, hasTimeField, resetPipe, has12Hour, isPM, unixTS, tzName, loc), tzName, loc, nil
}

func finalizeParsedTime(y, m, d, hr, min, sec, nsec int, hasTimeField, resetPipe, has12Hour bool, isPM int, unixTS *int64, tzName string, loc *time.Location) time.Time {
	_ = tzName
	if unixTS != nil {
		return time.Unix(*unixTS, int64(nsec)).In(loc)
	}
	if resetPipe && !hasTimeField {
		hr, min, sec, nsec = 0, 0, 0, 0
	}
	if has12Hour && isPM >= 0 {
		if isPM == 1 && hr < 12 {
			hr += 12
		} else if isPM == 0 && hr == 12 {
			hr = 0
		}
	}
	return time.Date(y, time.Month(m), d, hr, min, sec, nsec, loc)
}

func scanDigits(s string, pos, minLen, maxLen int) (int, int, bool) {
	end := pos
	for end < len(s) && end-pos < maxLen && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end-pos < minLen {
		return 0, pos, false
	}
	val, err := strconv.Atoi(s[pos:end])
	if err != nil {
		return 0, pos, false
	}
	return val, end, true
}

func scanMonthName(s string, pos int) (int, int, bool) {
	rest := s[pos:]
	for i, name := range longMonths {
		if len(rest) >= len(name) && strings.EqualFold(rest[:len(name)], name) {
			return i + 1, pos + len(name), true
		}
	}
	for i, name := range shortMonths {
		if len(rest) >= len(name) && strings.EqualFold(rest[:len(name)], name) {
			return i + 1, pos + len(name), true
		}
	}
	return 0, pos, false
}

func isASCIISpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

var goDateLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.999999-0700",
	"2006-01-02T15:04:05-0700",
	"2006-01-02 15:04:05.999999 -07:00",
	"2006-01-02 15:04:05.999999 -0700",
	"2006-01-02 15:04:05 -07:00",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05.999999",
	"2006-01-02T15:04:05.999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006/01/02 15:04:05",
	"2006/01/02",
	"15:04:05",
	"15:04",
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	time.RFC850,
}

func parsePHPDateTimeString(input string, base time.Time, defaultTZName string, defaultLoc *time.Location) (time.Time, string, *time.Location, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" || strings.EqualFold(trimmed, "now") {
		return base.In(defaultLoc), defaultTZName, defaultLoc, nil
	}
	if rest, ok := strings.CutPrefix(trimmed, "@"); ok {
		if secPart, fracPart, ok := strings.Cut(rest, "."); ok {
			secVal, err := strconv.ParseInt(secPart, 10, 64)
			if err != nil {
				return time.Time{}, "", nil, DateError{
					Class:   "DateMalformedStringException",
					Message: fmt.Sprintf("Failed to parse time string (%s)", input),
				}
			}
			for len(fracPart) < 9 {
				fracPart += "0"
			}
			nsecVal, _ := strconv.ParseInt(fracPart[:9], 10, 64)
			if secVal < 0 {
				nsecVal = -nsecVal
			}
			return time.Unix(secVal, nsecVal).In(time.UTC), "+00:00", time.UTC, nil
		}
		ts, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			return time.Time{}, "", nil, DateError{
				Class:   "DateMalformedStringException",
				Message: fmt.Sprintf("Failed to parse time string (%s)", input),
			}
		}
		return time.Unix(ts, 0).In(time.UTC), "+00:00", time.UTC, nil
	}

	// Tentative avec les formats standards ISO / RFC.
	for _, layout := range goDateLayouts {
		hasTZ := strings.Contains(layout, "07") || strings.Contains(layout, "MST") || layout == time.RFC3339 || layout == time.RFC3339Nano
		if hasTZ {
			if t, err := time.Parse(layout, trimmed); err == nil {
				name, off := t.Zone()
				if name == "" || strings.HasPrefix(name, "+") || strings.HasPrefix(name, "-") {
					name = formatOffset(off, true, false)
				}
				if strings.HasSuffix(trimmed, "Z") {
					return t.In(time.UTC), "Z", time.UTC, nil
				}
				return t, name, t.Location(), nil
			}
		} else {
			if t, err := time.ParseInLocation(layout, trimmed, defaultLoc); err == nil {
				if layout == "15:04:05" || layout == "15:04" {
					y, m, d := base.Date()
					hr, min, sec := t.Clock()
					return time.Date(y, m, d, hr, min, sec, 0, defaultLoc), defaultTZName, defaultLoc, nil
				}
				return t, defaultTZName, defaultLoc, nil
			}
		}
	}

	// Gestion d'une date suivie d'un nom de fuseau (ex: "2024-05-01 12:00:00 Europe/Paris").
	if lastSpace := strings.LastIndexByte(trimmed, ' '); lastSpace > 0 {
		maybeTZ := trimmed[lastSpace+1:]
		if tzn, tzLoc, err := loadPHPLocation(maybeTZ); err == nil {
			prefix := strings.TrimSpace(trimmed[:lastSpace])
			if t, _, _, err := parsePHPDateTimeString(prefix, base.In(tzLoc), tzn, tzLoc); err == nil {
				return t, tzn, tzLoc, nil
			}
		}
	}

	// Sinon, application comme modificateur relatif sur base.
	return applyPHPModifier(base, defaultTZName, defaultLoc, trimmed)
}

func applyPHPModifier(base time.Time, tzName string, loc *time.Location, modifier string) (time.Time, string, *time.Location, error) {
	s := strings.TrimSpace(modifier)
	if s == "" {
		return time.Time{}, "", nil, DateError{
			Class:   "DateMalformedStringException",
			Message: "DateTime::modify(): Failed to parse time string ()",
		}
	}
	lower := strings.ToLower(s)
	y, m, d := base.Date()
	hr, min, sec := base.Clock()
	nsec := base.Nanosecond()

	switch lower {
	case "now":
		return base, tzName, loc, nil
	case "today", "midnight":
		return time.Date(y, m, d, 0, 0, 0, 0, loc), tzName, loc, nil
	case "noon":
		return time.Date(y, m, d, 12, 0, 0, 0, loc), tzName, loc, nil
	case "tomorrow":
		return time.Date(y, m, d+1, 0, 0, 0, 0, loc), tzName, loc, nil
	case "yesterday":
		return time.Date(y, m, d-1, 0, 0, 0, 0, loc), tzName, loc, nil
	case "first day of this month":
		return time.Date(y, m, 1, hr, min, sec, nsec, loc), tzName, loc, nil
	case "last day of this month":
		return time.Date(y, m, daysInMonth(y, int(m)), hr, min, sec, nsec, loc), tzName, loc, nil
	case "first day of next month":
		return time.Date(y, m+1, 1, hr, min, sec, nsec, loc), tzName, loc, nil
	case "last day of next month":
		tNext := time.Date(y, m+1, 1, hr, min, sec, nsec, loc)
		return time.Date(tNext.Year(), tNext.Month(), daysInMonth(tNext.Year(), int(tNext.Month())), hr, min, sec, nsec, loc), tzName, loc, nil
	case "first day of last month", "first day of previous month":
		return time.Date(y, m-1, 1, hr, min, sec, nsec, loc), tzName, loc, nil
	case "last day of last month", "last day of previous month":
		tPrev := time.Date(y, m-1, 1, hr, min, sec, nsec, loc)
		return time.Date(tPrev.Year(), tPrev.Month(), daysInMonth(tPrev.Year(), int(tPrev.Month())), hr, min, sec, nsec, loc), tzName, loc, nil
	}

	if after, ok := strings.CutPrefix(lower, "next "); ok {
		if wd, ok := parseWeekdayName(strings.TrimSpace(after)); ok {
			delta := (int(wd) - int(base.Weekday()) + 7) % 7
			if delta == 0 {
				delta = 7
			}
			return time.Date(y, m, d+delta, 0, 0, 0, 0, loc), tzName, loc, nil
		}
	}
	if after, ok := strings.CutPrefix(lower, "last "); ok {
		if wd, ok := parseWeekdayName(strings.TrimSpace(after)); ok {
			delta := (int(base.Weekday()) - int(wd) + 7) % 7
			if delta == 0 {
				delta = 7
			}
			return time.Date(y, m, d-delta, 0, 0, 0, 0, loc), tzName, loc, nil
		}
	}

	iv, err := parseRelativeDateInterval(s)
	if err != nil {
		return time.Time{}, "", nil, DateError{
			Class:   "DateMalformedStringException",
			Message: fmt.Sprintf("Failed to parse time string (%s)", modifier),
		}
	}
	return applyDateInterval(base, iv, false), tzName, loc, nil
}

func parseWeekdayName(name string) (time.Weekday, bool) {
	for i, w := range longWeekdays {
		if strings.EqualFold(name, w) || strings.EqualFold(name, shortWeekdays[i]) {
			return time.Weekday(i), true
		}
	}
	return 0, false
}

func parseRelativeDateInterval(rel string) (*DateInterval, error) {
	s := strings.TrimSpace(rel)
	if s == "" {
		return nil, DateError{
			Class:   "DateMalformedIntervalStringException",
			Message: "Empty relative date interval string",
		}
	}
	tokens := tokenizeRelative(s)
	if len(tokens) == 0 {
		return nil, DateError{
			Class:   "DateMalformedIntervalStringException",
			Message: fmt.Sprintf("Unknown or bad format (%s)", rel),
		}
	}

	iv := &DateInterval{}
	sign := 1
	matched := false

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch tok {
		case "+", "and", ",":
			sign = 1
			continue
		case "-":
			sign = -1
			continue
		case "ago":
			iv.Y = -iv.Y
			iv.M = -iv.M
			iv.D = -iv.D
			iv.H = -iv.H
			iv.I = -iv.I
			iv.S = -iv.S
			continue
		}

		numSign := sign
		numStr := tok
		if strings.HasPrefix(numStr, "+") {
			numSign = 1
			numStr = numStr[1:]
		} else if strings.HasPrefix(numStr, "-") {
			numSign = -1
			numStr = numStr[1:]
		}
		n, err := strconv.Atoi(numStr)
		if err != nil || i+1 >= len(tokens) {
			return nil, DateError{
				Class:   "DateMalformedIntervalStringException",
				Message: fmt.Sprintf("Unknown or bad format (%s)", rel),
			}
		}
		i++
		unit := strings.ToLower(strings.TrimSuffix(tokens[i], ","))
		val := numSign * n
		sign = 1
		matched = true

		switch unit {
		case "year", "years", "yr", "yrs":
			iv.Y += val
		case "month", "months", "mon", "mons":
			iv.M += val
		case "week", "weeks", "wk", "wks":
			iv.D += val * 7
		case "day", "days":
			iv.D += val
		case "hour", "hours", "hr", "hrs":
			iv.H += val
		case "minute", "minutes", "min", "mins":
			iv.I += val
		case "second", "seconds", "sec", "secs":
			iv.S += val
		default:
			return nil, DateError{
				Class:   "DateMalformedIntervalStringException",
				Message: fmt.Sprintf("Unknown unit %q in (%s)", unit, rel),
			}
		}
	}
	if !matched {
		return nil, DateError{
			Class:   "DateMalformedIntervalStringException",
			Message: fmt.Sprintf("Unknown or bad format (%s)", rel),
		}
	}
	return iv, nil
}

func tokenizeRelative(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isASCIISpaceByte(c) || c == ',' {
			flush()
			continue
		}
		if (c == '+' || c == '-') && cur.Len() == 0 {
			if i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9' {
				cur.WriteByte(c)
			} else {
				out = append(out, string(c))
			}
			continue
		}
		cur.WriteByte(c)
	}
	flush()
	return out
}

func checkdateValid(month, day, year int) bool {
	if year < 1 || year > 32767 || month < 1 || month > 12 || day < 1 {
		return false
	}
	return day <= daysInMonth(year, month)
}

func computeIDate(t time.Time, spec byte) (int64, error) {
	y, m, d := t.Date()
	hr, min, sec := t.Clock()
	wday := int(t.Weekday())
	isoWday := wday
	if isoWday == 0 {
		isoWday = 7
	}
	_, isoWeek := t.ISOWeek()
	_, offsetSec := t.Zone()

	switch spec {
	case 'B':
		return int64(swatchBeats(t)), nil
	case 'd':
		return int64(d), nil
	case 'h', 'g':
		h12 := hr % 12
		if h12 == 0 {
			h12 = 12
		}
		return int64(h12), nil
	case 'H', 'G':
		return int64(hr), nil
	case 'i':
		return int64(min), nil
	case 'I':
		if isDSTActive(t) {
			return 1, nil
		}
		return 0, nil
	case 'L':
		if isLeapYear(y) {
			return 1, nil
		}
		return 0, nil
	case 'm', 'n':
		return int64(m), nil
	case 'N':
		return int64(isoWday), nil
	case 's':
		return int64(sec), nil
	case 't':
		return int64(daysInMonth(y, int(m))), nil
	case 'U':
		return t.Unix(), nil
	case 'w':
		return int64(wday), nil
	case 'W':
		return int64(isoWeek), nil
	case 'y':
		return int64(y % 100), nil
	case 'Y':
		return int64(y), nil
	case 'z':
		return int64(t.YearDay() - 1), nil
	case 'Z':
		return int64(offsetSec), nil
	default:
		return 0, DateError{Class: "ValueError", Message: "idate(): Unrecognized date format token"}
	}
}
