package datetime

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// DateInterval implémente la classe DateInterval de PHP 8.3.
type DateInterval struct {
	z       *zone.Zone
	obj     types.Value
	Y       int
	M       int
	D       int
	H       int
	I       int
	S       int
	F       float64
	Invert  int
	Days    int
	HasDays bool
}

// NewDateInterval crée une instance de DateInterval à partir d'une spécification ISO 8601 (ex. P1Y2M3DT4H5M6S, P2W).
func NewDateInterval(z *zone.Zone, spec string) (types.Value, *DateInterval, error) {
	iv, err := parseISO8601Interval(spec)
	if err != nil {
		return types.Value{}, nil, err
	}
	return registerDateInterval(z, iv)
}

// DateIntervalCreateFromDateString implémente DateInterval::createFromDateString(string $datetime).
func DateIntervalCreateFromDateString(z *zone.Zone, rel string) (types.Value, *DateInterval, error) {
	iv, err := parseRelativeDateInterval(rel)
	if err != nil {
		return types.Value{}, nil, err
	}
	return registerDateInterval(z, iv)
}

func registerDateInterval(z *zone.Zone, iv *DateInterval) (types.Value, *DateInterval, error) {
	obj, err := types.NewObject(z, ClassIDDateInterval, intervalPropCount, false)
	if err != nil {
		return types.Value{}, nil, err
	}
	iv.z = z
	iv.obj = obj
	iv.syncToObject(z)
	reg := getOrCreateRegistry(z)
	reg.intervals[objKey{slot: obj.DataSlot, gen: obj.Extra}] = iv
	return obj, iv, nil
}

// IntervalFromObject retrouve l'instance *DateInterval depuis une Value objet.
func IntervalFromObject(z *zone.Zone, v types.Value) (*DateInterval, error) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, DateError{Class: "TypeError", Message: "Expected DateInterval object"}
	}
	types.ObjectInfo(z, v)
	reg := lookupRegistry(z)
	if reg == nil {
		return nil, DateError{Class: "Error", Message: "Uninitialized datetime registry"}
	}
	iv, ok := reg.intervals[objKey{slot: v.DataSlot, gen: v.Extra}]
	if !ok {
		return nil, DateError{Class: "TypeError", Message: "Object is not a DateInterval"}
	}
	iv.syncFromObject(z)
	return iv, nil
}

func (iv *DateInterval) syncToObject(z *zone.Zone) {
	if iv.obj.IsUndef() {
		return
	}
	types.ObjectSet(z, iv.obj, IntervalPropY, types.NewLong(int64(iv.Y)))
	types.ObjectSet(z, iv.obj, IntervalPropM, types.NewLong(int64(iv.M)))
	types.ObjectSet(z, iv.obj, IntervalPropD, types.NewLong(int64(iv.D)))
	types.ObjectSet(z, iv.obj, IntervalPropH, types.NewLong(int64(iv.H)))
	types.ObjectSet(z, iv.obj, IntervalPropI, types.NewLong(int64(iv.I)))
	types.ObjectSet(z, iv.obj, IntervalPropS, types.NewLong(int64(iv.S)))
	types.ObjectSet(z, iv.obj, IntervalPropF, types.NewDouble(iv.F))
	types.ObjectSet(z, iv.obj, IntervalPropInvert, types.NewLong(int64(iv.Invert)))
	if iv.HasDays {
		types.ObjectSet(z, iv.obj, IntervalPropDays, types.NewLong(int64(iv.Days)))
	} else {
		types.ObjectSet(z, iv.obj, IntervalPropDays, types.NewBool(false))
	}
}

func (iv *DateInterval) syncFromObject(z *zone.Zone) {
	if iv.obj.IsUndef() {
		return
	}
	iv.Y = int(types.ObjectGet(z, iv.obj, IntervalPropY).ToLong())
	iv.M = int(types.ObjectGet(z, iv.obj, IntervalPropM).ToLong())
	iv.D = int(types.ObjectGet(z, iv.obj, IntervalPropD).ToLong())
	iv.H = int(types.ObjectGet(z, iv.obj, IntervalPropH).ToLong())
	iv.I = int(types.ObjectGet(z, iv.obj, IntervalPropI).ToLong())
	iv.S = int(types.ObjectGet(z, iv.obj, IntervalPropS).ToLong())
	iv.F = types.ObjectGet(z, iv.obj, IntervalPropF).ToDouble()
	iv.Invert = int(types.ObjectGet(z, iv.obj, IntervalPropInvert).ToLong())
	daysVal := types.ObjectGet(z, iv.obj, IntervalPropDays)
	if daysVal.Type == types.TypeLong {
		iv.HasDays = true
		iv.Days = int(daysVal.ToLong())
	} else {
		iv.HasDays = false
		iv.Days = 0
	}
}

// Object retourne la Value objet associée au DateInterval.
func (iv *DateInterval) Object() types.Value {
	return iv.obj
}

// GetProperty lit une propriété publique de DateInterval ("y", "m", "d", "h", "i", "s", "f", "invert", "days").
func (iv *DateInterval) GetProperty(z *zone.Zone, prop string) (types.Value, error) {
	iv.syncFromObject(z)
	switch prop {
	case "y":
		return types.NewLong(int64(iv.Y)), nil
	case "m":
		return types.NewLong(int64(iv.M)), nil
	case "d":
		return types.NewLong(int64(iv.D)), nil
	case "h":
		return types.NewLong(int64(iv.H)), nil
	case "i":
		return types.NewLong(int64(iv.I)), nil
	case "s":
		return types.NewLong(int64(iv.S)), nil
	case "f":
		return types.NewDouble(iv.F), nil
	case "invert":
		return types.NewLong(int64(iv.Invert)), nil
	case "days":
		if iv.HasDays {
			return types.NewLong(int64(iv.Days)), nil
		}
		return types.NewBool(false), nil
	default:
		return types.NewNull(), DateError{
			Class:   "Error",
			Message: fmt.Sprintf("Undefined property: DateInterval::$%s", prop),
		}
	}
}

// SetProperty modifie une propriété publique de DateInterval ("y", "m", "d", "h", "i", "s", "f", "invert", "days").
func (iv *DateInterval) SetProperty(z *zone.Zone, prop string, val types.Value) error {
	val = types.Deref(z, val)
	switch prop {
	case "y":
		iv.Y = int(val.ToLong())
	case "m":
		iv.M = int(val.ToLong())
	case "d":
		iv.D = int(val.ToLong())
	case "h":
		iv.H = int(val.ToLong())
	case "i":
		iv.I = int(val.ToLong())
	case "s":
		iv.S = int(val.ToLong())
	case "f":
		iv.F = val.ToDouble()
	case "invert":
		if val.ToLong() != 0 {
			iv.Invert = 1
		} else {
			iv.Invert = 0
		}
	case "days":
		if val.IsBool() && !val.ToBool() {
			iv.HasDays = false
			iv.Days = 0
		} else {
			iv.HasDays = true
			iv.Days = int(val.ToLong())
		}
	default:
		return DateError{
			Class:   "Error",
			Message: fmt.Sprintf("Cannot create dynamic property DateInterval::$%s", prop),
		}
	}
	iv.syncToObject(z)
	return nil
}

// Format formate le DateInterval selon les spécificateurs PHP (%Y, %y, %M, %m, %D, %d, %a, %H, %h, %I, %i, %S, %s, %F, %f, %R, %r, %%).
func (iv *DateInterval) Format(format string) string {
	if iv.z != nil && !iv.obj.IsUndef() {
		iv.syncFromObject(iv.z)
	}
	var b strings.Builder
	b.Grow(len(format) + 16)
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])
			continue
		}
		i++
		spec := format[i]
		switch spec {
		case '%':
			b.WriteByte('%')
		case 'Y':
			fmt.Fprintf(&b, "%02d", iv.Y)
		case 'y':
			fmt.Fprintf(&b, "%d", iv.Y)
		case 'M':
			fmt.Fprintf(&b, "%02d", iv.M)
		case 'm':
			fmt.Fprintf(&b, "%d", iv.M)
		case 'D':
			fmt.Fprintf(&b, "%02d", iv.D)
		case 'd':
			fmt.Fprintf(&b, "%d", iv.D)
		case 'a':
			if iv.HasDays {
				fmt.Fprintf(&b, "%d", iv.Days)
			} else {
				b.WriteString("(unknown)")
			}
		case 'H':
			fmt.Fprintf(&b, "%02d", iv.H)
		case 'h':
			fmt.Fprintf(&b, "%d", iv.H)
		case 'I':
			fmt.Fprintf(&b, "%02d", iv.I)
		case 'i':
			fmt.Fprintf(&b, "%d", iv.I)
		case 'S':
			fmt.Fprintf(&b, "%02d", iv.S)
		case 's':
			fmt.Fprintf(&b, "%d", iv.S)
		case 'F':
			us := int64(math.Round(math.Abs(iv.F) * 1e6))
			fmt.Fprintf(&b, "%06d", us)
		case 'f':
			us := int64(math.Round(math.Abs(iv.F) * 1e6))
			fmt.Fprintf(&b, "%d", us)
		case 'R':
			if iv.Invert != 0 {
				b.WriteByte('-')
			} else {
				b.WriteByte('+')
			}
		case 'r':
			if iv.Invert != 0 {
				b.WriteByte('-')
			}
		default:
			b.WriteByte('%')
			b.WriteByte(spec)
		}
	}
	return b.String()
}

func parseISO8601Interval(spec string) (*DateInterval, error) {
	rest, ok := strings.CutPrefix(spec, "P")
	if !ok || len(rest) == 0 {
		return nil, DateError{
			Class:   "DateMalformedIntervalStringException",
			Message: fmt.Sprintf("DateInterval::__construct(): Unknown or bad format (%s)", spec),
		}
	}
	iv := &DateInterval{}
	inTime := false
	hasAny := false

	for len(rest) > 0 {
		if rest[0] == 'T' {
			if inTime {
				return nil, DateError{
					Class:   "DateMalformedIntervalStringException",
					Message: fmt.Sprintf("DateInterval::__construct(): Unknown or bad format (%s)", spec),
				}
			}
			inTime = true
			rest = rest[1:]
			if len(rest) == 0 {
				return nil, DateError{
					Class:   "DateMalformedIntervalStringException",
					Message: fmt.Sprintf("DateInterval::__construct(): Unknown or bad format (%s)", spec),
				}
			}
			continue
		}
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j == 0 || j >= len(rest) {
			return nil, DateError{
				Class:   "DateMalformedIntervalStringException",
				Message: fmt.Sprintf("DateInterval::__construct(): Unknown or bad format (%s)", spec),
			}
		}
		n, err := strconv.Atoi(rest[:j])
		if err != nil {
			return nil, DateError{
				Class:   "DateMalformedIntervalStringException",
				Message: fmt.Sprintf("DateInterval::__construct(): Unknown or bad format (%s)", spec),
			}
		}
		unit := rest[j]
		rest = rest[j+1:]
		hasAny = true

		if !inTime {
			switch unit {
			case 'Y':
				iv.Y += n
			case 'M':
				iv.M += n
			case 'W':
				iv.D += n * 7
			case 'D':
				iv.D += n
			default:
				return nil, DateError{
					Class:   "DateMalformedIntervalStringException",
					Message: fmt.Sprintf("DateInterval::__construct(): Unknown or bad format (%s)", spec),
				}
			}
		} else {
			switch unit {
			case 'H':
				iv.H += n
			case 'M':
				iv.I += n
			case 'S':
				iv.S += n
			default:
				return nil, DateError{
					Class:   "DateMalformedIntervalStringException",
					Message: fmt.Sprintf("DateInterval::__construct(): Unknown or bad format (%s)", spec),
				}
			}
		}
	}
	if !hasAny {
		return nil, DateError{
			Class:   "DateMalformedIntervalStringException",
			Message: fmt.Sprintf("DateInterval::__construct(): Unknown or bad format (%s)", spec),
		}
	}
	return iv, nil
}

func applyDateInterval(t time.Time, iv *DateInterval, sub bool) time.Time {
	if iv.z != nil && !iv.obj.IsUndef() {
		iv.syncFromObject(iv.z)
	}
	sign := 1
	if sub {
		sign = -1
	}
	if iv.Invert != 0 {
		sign = -sign
	}
	next := t.AddDate(sign*iv.Y, sign*iv.M, sign*iv.D)
	dur := time.Duration(sign) * (time.Duration(iv.H)*time.Hour +
		time.Duration(iv.I)*time.Minute +
		time.Duration(iv.S)*time.Second +
		time.Duration(math.Round(iv.F*1e9))*time.Nanosecond)
	return next.Add(dur)
}

func computeDateDiff(z *zone.Zone, a, b time.Time, absolute bool) (types.Value, *DateInterval, error) {
	invert := 0
	start, end := a, b
	if start.After(end) {
		if !absolute {
			invert = 1
		}
		start, end = end, start
	}
	// Calcul du nombre total de jours calendaires.
	startUTC := time.Date(start.Year(), start.Month(), start.Day(), start.Hour(), start.Minute(), start.Second(), start.Nanosecond(), time.UTC)
	endUTC := time.Date(end.Year(), end.Month(), end.Day(), end.Hour(), end.Minute(), end.Second(), end.Nanosecond(), time.UTC)
	totalDays := int(endUTC.Sub(startUTC) / (24 * time.Hour))

	// Décomposition calendaire année / mois / jour / heure / minute / seconde.
	endInStartLoc := end.In(start.Location())
	y1, m1, d1 := start.Date()
	h1, i1, s1 := start.Clock()
	ns1 := start.Nanosecond()

	y2, m2, d2 := endInStartLoc.Date()
	h2, i2, s2 := endInStartLoc.Clock()
	ns2 := endInStartLoc.Nanosecond()

	years := y2 - y1
	months := int(m2) - int(m1)
	days := d2 - d1
	hours := h2 - h1
	minutes := i2 - i1
	seconds := s2 - s1
	nsec := ns2 - ns1

	if nsec < 0 {
		nsec += 1e9
		seconds--
	}
	if seconds < 0 {
		seconds += 60
		minutes--
	}
	if minutes < 0 {
		minutes += 60
		hours--
	}
	if hours < 0 {
		hours += 24
		days--
	}
	if days < 0 {
		prevMonthDays := daysInMonth(y2, int(m2)-1)
		days += prevMonthDays
		months--
	}
	if months < 0 {
		months += 12
		years--
	}

	iv := &DateInterval{
		Y:       years,
		M:       months,
		D:       days,
		H:       hours,
		I:       minutes,
		S:       seconds,
		F:       float64(nsec) / 1e9,
		Invert:  invert,
		Days:    totalDays,
		HasDays: true,
	}
	return registerDateInterval(z, iv)
}

func daysInMonth(year, month int) int {
	for month <= 0 {
		month += 12
		year--
	}
	for month > 12 {
		month -= 12
		year++
	}
	return time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// DatePeriod implémente la classe itérable DatePeriod de PHP 8.3.
type DatePeriod struct {
	z           *zone.Zone
	obj         types.Value
	start       DateTimeInterface
	end         DateTimeInterface
	interval    *DateInterval
	recurrences int
	hasEnd      bool
	options     int64
	items       []types.Value
	cursor      int
}

// NewDatePeriodWithEnd crée un DatePeriod borné par une date de fin.
func NewDatePeriodWithEnd(z *zone.Zone, start DateTimeInterface, interval *DateInterval, end DateTimeInterface, options int64) (types.Value, *DatePeriod, error) {
	if start == nil || interval == nil || end == nil {
		return types.Value{}, nil, DateError{Class: "TypeError", Message: "DatePeriod::__construct(): invalid arguments"}
	}
	return buildDatePeriod(z, start, interval, end, 0, true, options)
}

// NewDatePeriodWithRecurrences crée un DatePeriod borné par un nombre de récurrences.
func NewDatePeriodWithRecurrences(z *zone.Zone, start DateTimeInterface, interval *DateInterval, recurrences int, options int64) (types.Value, *DatePeriod, error) {
	if start == nil || interval == nil {
		return types.Value{}, nil, DateError{Class: "TypeError", Message: "DatePeriod::__construct(): invalid arguments"}
	}
	if recurrences < 0 {
		return types.Value{}, nil, DateError{Class: "ValueError", Message: "DatePeriod::__construct(): Recurrences must be non-negative"}
	}
	return buildDatePeriod(z, start, interval, nil, recurrences, false, options)
}

func buildDatePeriod(z *zone.Zone, start DateTimeInterface, interval *DateInterval, end DateTimeInterface, recurrences int, hasEnd bool, options int64) (types.Value, *DatePeriod, error) {
	obj, err := types.NewObject(z, ClassIDDatePeriod, 0, false)
	if err != nil {
		return types.Value{}, nil, err
	}
	p := &DatePeriod{
		z:           z,
		obj:         obj,
		start:       start,
		end:         end,
		interval:    interval,
		recurrences: recurrences,
		hasEnd:      hasEnd,
		options:     options,
		cursor:      0,
	}
	if err := p.populateItems(z); err != nil {
		types.ReleaseObject(z, &obj)
		return types.Value{}, nil, err
	}
	reg := getOrCreateRegistry(z)
	reg.periods[objKey{slot: obj.DataSlot, gen: obj.Extra}] = p
	return obj, p, nil
}

func (p *DatePeriod) populateItems(z *zone.Zone) error {
	excludeStart := p.options&EXCLUDE_START_DATE != 0
	includeEnd := p.options&INCLUDE_END_DATE != 0
	immutable := p.start.IsImmutable()
	tz := p.start.TimeZone()

	cur := p.start.Time()
	if p.hasEnd {
		endT := p.end.Time()
		for step := range 100000 {
			if step == 0 && excludeStart {
				cur = applyDateInterval(cur, p.interval, false)
				continue
			}
			if cur.After(endT) || (!includeEnd && cur.Equal(endT)) {
				break
			}
			val, err := p.allocDateItem(z, cur, tz, immutable)
			if err != nil {
				p.releaseAll(z)
				return err
			}
			p.items = append(p.items, val)
			next := applyDateInterval(cur, p.interval, false)
			if !next.After(cur) {
				break
			}
			cur = next
		}
		return nil
	}

	// Mode récurrences : en PHP 8.3, recurrences désigne le nombre de pas après la date de début.
	for step := 0; step <= p.recurrences; step++ {
		if step == 0 && excludeStart {
			cur = applyDateInterval(cur, p.interval, false)
			continue
		}
		val, err := p.allocDateItem(z, cur, tz, immutable)
		if err != nil {
			p.releaseAll(z)
			return err
		}
		p.items = append(p.items, val)
		cur = applyDateInterval(cur, p.interval, false)
	}
	return nil
}

func (p *DatePeriod) allocDateItem(z *zone.Zone, t time.Time, tz *DateTimeZone, immutable bool) (types.Value, error) {
	if immutable {
		v, _, err := newDateTimeImmutableFromTime(z, t, tz.name, tz.loc)
		return v, err
	}
	v, _, err := newDateTimeFromTime(z, t, tz.name, tz.loc)
	return v, err
}

func (p *DatePeriod) releaseAll(z *zone.Zone) {
	for i := range p.items {
		if !p.items[i].IsUndef() {
			types.ReleaseValue(z, &p.items[i])
		}
	}
	p.items = nil
}

// PeriodFromObject retrouve l'instance *DatePeriod depuis une Value objet.
func PeriodFromObject(z *zone.Zone, v types.Value) (*DatePeriod, error) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, DateError{Class: "TypeError", Message: "Expected DatePeriod object"}
	}
	types.ObjectInfo(z, v)
	reg := lookupRegistry(z)
	if reg == nil {
		return nil, DateError{Class: "Error", Message: "Uninitialized datetime registry"}
	}
	p, ok := reg.periods[objKey{slot: v.DataSlot, gen: v.Extra}]
	if !ok {
		return nil, DateError{Class: "TypeError", Message: "Object is not a DatePeriod"}
	}
	return p, nil
}

// Object retourne la Value objet associée au DatePeriod.
func (p *DatePeriod) Object() types.Value { return p.obj }

// GetStartDate retourne la date de début du DatePeriod.
func (p *DatePeriod) GetStartDate() DateTimeInterface { return p.start }

// GetEndDate retourne la date de fin du DatePeriod (ou nil si défini par récurrences).
func (p *DatePeriod) GetEndDate() DateTimeInterface { return p.end }

// GetDateInterval retourne l'intervalle du DatePeriod.
func (p *DatePeriod) GetDateInterval() *DateInterval { return p.interval }

// GetRecurrences retourne le nombre de récurrences configuré (0 si défini par date de fin).
func (p *DatePeriod) GetRecurrences() int { return p.recurrences }

// Rewind remet le curseur d'itération au début de la période.
func (p *DatePeriod) Rewind() { p.cursor = 0 }

// Valid indique si le curseur d'itération pointe sur une date valide.
func (p *DatePeriod) Valid() bool { return p.cursor >= 0 && p.cursor < len(p.items) }

// Current retourne l'objet DateTime/DateTimeImmutable courant, ou null si hors bornes.
func (p *DatePeriod) Current() types.Value {
	if !p.Valid() {
		return types.NewNull()
	}
	return p.items[p.cursor]
}

// Key retourne l'indice courant de l'itération.
func (p *DatePeriod) Key() int { return p.cursor }

// Next avance le curseur d'itération.
func (p *DatePeriod) Next() { p.cursor++ }
