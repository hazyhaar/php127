// Package datetime implémente la hiérarchie complète des classes de dates orientées objet
// (DateTimeInterface, DateTime, DateTimeImmutable, DateTimeZone, DateInterval, DatePeriod)
// ainsi que les fonctions procédurales de date et d'heure de PHP 8.3.
package datetime

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Constantes de formats de DateTimeInterface (PHP 8.3).
const (
	ATOM             = `Y-m-d\TH:i:sP`
	COOKIE           = `l, d-M-Y H:i:s T`
	ISO8601          = `Y-m-d\TH:i:sO`
	ISO8601_EXPANDED = `X-m-d\TH:i:sP`
	RFC822           = `D, d M y H:i:s O`
	RFC850           = `l, d-M-y H:i:s T`
	RFC1036          = `D, d M y H:i:s O`
	RFC1123          = `D, d M Y H:i:s O`
	RFC7231          = `D, d M Y H:i:s \G\M\T`
	RFC2822          = `D, d M Y H:i:s O`
	RFC3339          = `Y-m-d\TH:i:sP`
	RFC3339_EXTENDED = `Y-m-d\TH:i:s.vP`
	RSS              = `D, d M Y H:i:s O`
	W3C              = `Y-m-d\TH:i:sP`
)

// Constantes d'options de DatePeriod.
const (
	INCLUDE_START_DATE int64 = 1
	EXCLUDE_START_DATE int64 = 2
	INCLUDE_END_DATE   int64 = 4
)

// Identifiants de classes et de nettoyage dans la Zone.
const (
	ClassIDDateTime          uint32 = 5401
	ClassIDDateTimeImmutable uint32 = 5402
	ClassIDDateTimeZone      uint32 = 5403
	ClassIDDateInterval      uint32 = 5404
	ClassIDDatePeriod        uint32 = 5405

	CleanupDateTime zone.CleanupKind = 54
)

// Indices des propriétés publiques de DateInterval stockées dans l'objet Zone.
const (
	IntervalPropY uint32 = iota
	IntervalPropM
	IntervalPropD
	IntervalPropH
	IntervalPropI
	IntervalPropS
	IntervalPropF
	IntervalPropInvert
	IntervalPropDays
	intervalPropCount
)

// DateError représente une exception PHP du sous-système Date/Time (DateMalformedStringException, DateInvalidTimeZoneException, etc.).
type DateError struct {
	Class   string
	Message string
}

func (e DateError) Error() string {
	return e.Class + ": " + e.Message
}

// DateTimeInterface est l'interface commune implémentée par *DateTime et *DateTimeImmutable.
type DateTimeInterface interface {
	Object() types.Value
	Time() time.Time
	TimeZone() *DateTimeZone
	Format(layout string) string
	GetTimestamp() int64
	GetOffset() int64
	GetTimezone(z *zone.Zone) (types.Value, *DateTimeZone, error)
	Diff(z *zone.Zone, target DateTimeInterface, absolute bool) (types.Value, *DateInterval, error)
	IsImmutable() bool
}

type objKey struct {
	slot uint32
	gen  uint16
}

type zoneRegistry struct {
	id          uint32
	z           *zone.Zone
	defaultTZ   string
	dateTimes   map[objKey]*DateTime
	immutables  map[objKey]*DateTimeImmutable
	timeZones   map[objKey]*DateTimeZone
	intervals   map[objKey]*DateInterval
	periods     map[objKey]*DatePeriod
	cleaned     bool
}

var (
	regMu     sync.Mutex
	nextRegID uint32
	regsByZ   = make(map[*zone.Zone]*zoneRegistry)
	regsByID  = make(map[uint32]*zoneRegistry)
)

func init() {
	EnsureClassNames()
}

// EnsureClassNames enregistre les noms de classes Date/Time dans la table du paquet types si elle n'est pas gelée.
func EnsureClassNames() {
	if !types.IsProcessZoneFrozen() {
		types.RegisterObjectClassName(ClassIDDateTime, "DateTime")
		types.RegisterObjectClassName(ClassIDDateTimeImmutable, "DateTimeImmutable")
		types.RegisterObjectClassName(ClassIDDateTimeZone, "DateTimeZone")
		types.RegisterObjectClassName(ClassIDDateInterval, "DateInterval")
		types.RegisterObjectClassName(ClassIDDatePeriod, "DatePeriod")
	}
}

func getOrCreateRegistry(z *zone.Zone) *zoneRegistry {
	if z == nil {
		panic("datetime: nil zone")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if r, ok := regsByZ[z]; ok {
		return r
	}
	nextRegID++
	id := nextRegID
	r := &zoneRegistry{
		id:         id,
		z:          z,
		defaultTZ:  "UTC",
		dateTimes:  make(map[objKey]*DateTime),
		immutables: make(map[objKey]*DateTimeImmutable),
		timeZones:  make(map[objKey]*DateTimeZone),
		intervals:  make(map[objKey]*DateInterval),
		periods:    make(map[objKey]*DatePeriod),
	}
	regsByZ[z] = r
	regsByID[id] = r
	z.RegisterCleanup(zone.CleanupItem{
		Kind:   CleanupDateTime,
		Target: id,
	})
	return r
}

func lookupRegistry(z *zone.Zone) *zoneRegistry {
	regMu.Lock()
	defer regMu.Unlock()
	return regsByZ[z]
}

// CleanupDispatcher retourne la fonction de rappel compatible avec z.Reset pour le paquet datetime.
func CleanupDispatcher(z *zone.Zone) func(zone.CleanupItem) {
	return func(item zone.CleanupItem) {
		DispatchCleanup(z, item)
	}
}

// DispatchCleanup libère toutes les structures de dates associées à la Zone lors de z.Reset.
func DispatchCleanup(z *zone.Zone, item zone.CleanupItem) {
	switch item.Kind {
	case CleanupDateTime:
		regMu.Lock()
		r := regsByID[item.Target]
		regMu.Unlock()
		if r == nil {
			return
		}
		if z == nil {
			z = r.z
		}
		r.cleanupAll(z)
		regMu.Lock()
		delete(regsByID, r.id)
		if regsByZ[z] == r {
			delete(regsByZ, z)
		}
		regMu.Unlock()
	case zone.CleanupDestructor:
		if z != nil {
			types.DispatchObjectDestructor(z, item)
		}
	}
}

func (r *zoneRegistry) cleanupAll(z *zone.Zone) {
	if r.cleaned {
		return
	}
	r.cleaned = true
	for _, p := range r.periods {
		p.releaseAll(z)
	}
	clear(r.dateTimes)
	clear(r.immutables)
	clear(r.timeZones)
	clear(r.intervals)
	clear(r.periods)
}

// DefaultTimezoneGet retourne le fuseau horaire par défaut de la requête associée à la Zone.
func DefaultTimezoneGet(z *zone.Zone) string {
	reg := getOrCreateRegistry(z)
	if reg.defaultTZ == "" {
		return "UTC"
	}
	return reg.defaultTZ
}

// DefaultTimezoneSet définit le fuseau horaire par défaut de la requête après validation.
func DefaultTimezoneSet(z *zone.Zone, tzID string) bool {
	if _, _, err := loadPHPLocation(tzID); err != nil {
		return false
	}
	reg := getOrCreateRegistry(z)
	reg.defaultTZ = tzID
	return true
}

// DateTimeZone implémente la classe DateTimeZone de PHP 8.3.
type DateTimeZone struct {
	z    *zone.Zone
	obj  types.Value
	name string
	loc  *time.Location
}

func loadPHPLocation(tz string) (string, *time.Location, error) {
	if tz == "" {
		return "", nil, DateError{
			Class:   "DateInvalidTimeZoneException",
			Message: "DateTimeZone::__construct(): Unknown or bad timezone ()",
		}
	}
	if tz == "UTC" || tz == "GMT" || tz == "Z" {
		if tz == "Z" {
			tz = "UTC"
		}
		return tz, time.UTC, nil
	}
	if loc, ok := parseFixedOffsetTZ(tz); ok {
		return tz, loc, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return "", nil, DateError{
			Class:   "DateInvalidTimeZoneException",
			Message: fmt.Sprintf("DateTimeZone::__construct(): Unknown or bad timezone (%s)", tz),
		}
	}
	return tz, loc, nil
}

func parseFixedOffsetTZ(tz string) (*time.Location, bool) {
	if len(tz) < 3 || (tz[0] != '+' && tz[0] != '-') {
		return nil, false
	}
	sign := 1
	if tz[0] == '-' {
		sign = -1
	}
	rest := tz[1:]
	var hh, mm int
	switch len(rest) {
	case 2:
		if !isTwoDigits(rest) {
			return nil, false
		}
		hh = int(rest[0]-'0')*10 + int(rest[1]-'0')
	case 4:
		if !isTwoDigits(rest[:2]) || !isTwoDigits(rest[2:]) {
			return nil, false
		}
		hh = int(rest[0]-'0')*10 + int(rest[1]-'0')
		mm = int(rest[2]-'0')*10 + int(rest[3]-'0')
	case 5:
		if rest[2] != ':' || !isTwoDigits(rest[:2]) || !isTwoDigits(rest[3:]) {
			return nil, false
		}
		hh = int(rest[0]-'0')*10 + int(rest[1]-'0')
		mm = int(rest[3]-'0')*10 + int(rest[4]-'0')
	default:
		return nil, false
	}
	if hh > 23 || mm > 59 {
		return nil, false
	}
	offset := sign * (hh*3600 + mm*60)
	return time.FixedZone(tz, offset), true
}

func isTwoDigits(s string) bool {
	return len(s) == 2 && s[0] >= '0' && s[0] <= '9' && s[1] >= '0' && s[1] <= '9'
}

// NewDateTimeZone crée une instance de DateTimeZone dans la Zone.
func NewDateTimeZone(z *zone.Zone, timezone string) (types.Value, *DateTimeZone, error) {
	canon, loc, err := loadPHPLocation(timezone)
	if err != nil {
		return types.Value{}, nil, err
	}
	obj, err := types.NewObject(z, ClassIDDateTimeZone, 0, false)
	if err != nil {
		return types.Value{}, nil, err
	}
	dtz := &DateTimeZone{
		z:    z,
		obj:  obj,
		name: canon,
		loc:  loc,
	}
	reg := getOrCreateRegistry(z)
	reg.timeZones[objKey{slot: obj.DataSlot, gen: obj.Extra}] = dtz
	return obj, dtz, nil
}

// TimeZoneFromObject retrouve l'instance DateTimeZone depuis une Value objet.
func TimeZoneFromObject(z *zone.Zone, v types.Value) (*DateTimeZone, error) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, DateError{Class: "TypeError", Message: "Expected DateTimeZone object"}
	}
	types.ObjectInfo(z, v)
	reg := lookupRegistry(z)
	if reg == nil {
		return nil, DateError{Class: "Error", Message: "Uninitialized datetime registry"}
	}
	tz, ok := reg.timeZones[objKey{slot: v.DataSlot, gen: v.Extra}]
	if !ok {
		return nil, DateError{Class: "TypeError", Message: "Object is not a DateTimeZone"}
	}
	return tz, nil
}

// Object retourne la Value objet associée au DateTimeZone.
func (tz *DateTimeZone) Object() types.Value {
	return tz.obj
}

// GetName retourne le nom du fuseau horaire.
func (tz *DateTimeZone) GetName() string {
	return tz.name
}

// Location retourne le *time.Location Go sous-jacent.
func (tz *DateTimeZone) Location() *time.Location {
	return tz.loc
}

// GetOffset retourne le décalage en secondes par rapport à UTC pour la date fournie.
func (tz *DateTimeZone) GetOffset(dt DateTimeInterface) int64 {
	t := dt.Time().In(tz.loc)
	_, offset := t.Zone()
	return int64(offset)
}

var defaultTimezoneIdentifiers = []string{
	"Africa/Abidjan",
	"Africa/Cairo",
	"Africa/Casablanca",
	"Africa/Johannesburg",
	"Africa/Lagos",
	"Africa/Nairobi",
	"America/Anchorage",
	"America/Argentina/Buenos_Aires",
	"America/Bogota",
	"America/Chicago",
	"America/Denver",
	"America/Halifax",
	"America/Lima",
	"America/Los_Angeles",
	"America/Mexico_City",
	"America/New_York",
	"America/Phoenix",
	"America/Santiago",
	"America/Sao_Paulo",
	"America/St_Johns",
	"America/Toronto",
	"America/Vancouver",
	"Asia/Bangkok",
	"Asia/Colombo",
	"Asia/Dhaka",
	"Asia/Dubai",
	"Asia/Hong_Kong",
	"Asia/Jakarta",
	"Asia/Jerusalem",
	"Asia/Karachi",
	"Asia/Kathmandu",
	"Asia/Kolkata",
	"Asia/Manila",
	"Asia/Riyadh",
	"Asia/Seoul",
	"Asia/Shanghai",
	"Asia/Singapore",
	"Asia/Taipei",
	"Asia/Tehran",
	"Asia/Tokyo",
	"Atlantic/Azores",
	"Atlantic/Reykjavik",
	"Australia/Adelaide",
	"Australia/Brisbane",
	"Australia/Darwin",
	"Australia/Melbourne",
	"Australia/Perth",
	"Australia/Sydney",
	"Europe/Amsterdam",
	"Europe/Athens",
	"Europe/Belgrade",
	"Europe/Berlin",
	"Europe/Brussels",
	"Europe/Bucharest",
	"Europe/Budapest",
	"Europe/Copenhagen",
	"Europe/Dublin",
	"Europe/Helsinki",
	"Europe/Istanbul",
	"Europe/Kyiv",
	"Europe/Lisbon",
	"Europe/London",
	"Europe/Madrid",
	"Europe/Moscow",
	"Europe/Oslo",
	"Europe/Paris",
	"Europe/Prague",
	"Europe/Rome",
	"Europe/Stockholm",
	"Europe/Vienna",
	"Europe/Warsaw",
	"Europe/Zurich",
	"Indian/Maldives",
	"Indian/Mauritius",
	"Pacific/Auckland",
	"Pacific/Chatham",
	"Pacific/Fiji",
	"Pacific/Guam",
	"Pacific/Honolulu",
	"Pacific/Noumea",
	"Pacific/Pago_Pago",
	"Pacific/Port_Moresby",
	"Pacific/Tahiti",
	"UTC",
}

// ListIdentifiers retourne le tableau PHP des identifiants de fuseaux horaires.
func ListIdentifiers(z *zone.Zone) (types.Value, error) {
	arr, err := types.NewArray(z)
	if err != nil {
		return types.NewNull(), err
	}
	ids := slices.Clone(defaultTimezoneIdentifiers)
	slices.Sort(ids)
	for _, id := range ids {
		sv, err := types.NewString(z, id, 0)
		if err != nil {
			types.ReleaseArray(z, &arr)
			return types.NewNull(), err
		}
		err = types.ArrayAppend(z, &arr, sv)
		types.ReleaseString(z, &sv)
		if err != nil {
			types.ReleaseArray(z, &arr)
			return types.NewNull(), err
		}
	}
	return arr, nil
}

// DateTime implémente la classe mutable DateTime de PHP 8.3.
type DateTime struct {
	z      *zone.Zone
	obj    types.Value
	t      time.Time
	tzName string
	loc    *time.Location
}

// DateTimeImmutable implémente la classe immuable DateTimeImmutable de PHP 8.3.
type DateTimeImmutable struct {
	z      *zone.Zone
	obj    types.Value
	t      time.Time
	tzName string
	loc    *time.Location
}

func resolveConstructTZ(z *zone.Zone, tz *DateTimeZone) (string, *time.Location, error) {
	if tz != nil {
		return tz.name, tz.loc, nil
	}
	defName := DefaultTimezoneGet(z)
	return loadPHPLocation(defName)
}

// NewDateTime crée une instance mutable de DateTime dans la Zone.
func NewDateTime(z *zone.Zone, datetimeStr string, tz *DateTimeZone) (types.Value, *DateTime, error) {
	tzName, loc, err := resolveConstructTZ(z, tz)
	if err != nil {
		return types.Value{}, nil, err
	}
	parsed, parsedTZName, parsedLoc, err := parsePHPDateTimeString(datetimeStr, time.Now().In(loc), tzName, loc)
	if err != nil {
		return types.Value{}, nil, err
	}
	return newDateTimeFromTime(z, parsed, parsedTZName, parsedLoc)
}

func newDateTimeFromTime(z *zone.Zone, t time.Time, tzName string, loc *time.Location) (types.Value, *DateTime, error) {
	obj, err := types.NewObject(z, ClassIDDateTime, 0, false)
	if err != nil {
		return types.Value{}, nil, err
	}
	dt := &DateTime{
		z:      z,
		obj:    obj,
		t:      t.In(loc),
		tzName: tzName,
		loc:    loc,
	}
	reg := getOrCreateRegistry(z)
	reg.dateTimes[objKey{slot: obj.DataSlot, gen: obj.Extra}] = dt
	return obj, dt, nil
}

// NewDateTimeImmutable crée une instance immuable de DateTimeImmutable dans la Zone.
func NewDateTimeImmutable(z *zone.Zone, datetimeStr string, tz *DateTimeZone) (types.Value, *DateTimeImmutable, error) {
	tzName, loc, err := resolveConstructTZ(z, tz)
	if err != nil {
		return types.Value{}, nil, err
	}
	parsed, parsedTZName, parsedLoc, err := parsePHPDateTimeString(datetimeStr, time.Now().In(loc), tzName, loc)
	if err != nil {
		return types.Value{}, nil, err
	}
	return newDateTimeImmutableFromTime(z, parsed, parsedTZName, parsedLoc)
}

func newDateTimeImmutableFromTime(z *zone.Zone, t time.Time, tzName string, loc *time.Location) (types.Value, *DateTimeImmutable, error) {
	obj, err := types.NewObject(z, ClassIDDateTimeImmutable, 0, false)
	if err != nil {
		return types.Value{}, nil, err
	}
	dti := &DateTimeImmutable{
		z:      z,
		obj:    obj,
		t:      t.In(loc),
		tzName: tzName,
		loc:    loc,
	}
	reg := getOrCreateRegistry(z)
	reg.immutables[objKey{slot: obj.DataSlot, gen: obj.Extra}] = dti
	return obj, dti, nil
}

// DateTimeFromObject retrouve l'instance *DateTime depuis une Value objet.
func DateTimeFromObject(z *zone.Zone, v types.Value) (*DateTime, error) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, DateError{Class: "TypeError", Message: "Expected DateTime object"}
	}
	types.ObjectInfo(z, v)
	reg := lookupRegistry(z)
	if reg == nil {
		return nil, DateError{Class: "Error", Message: "Uninitialized datetime registry"}
	}
	dt, ok := reg.dateTimes[objKey{slot: v.DataSlot, gen: v.Extra}]
	if !ok {
		return nil, DateError{Class: "TypeError", Message: "Object is not a DateTime"}
	}
	return dt, nil
}

// DateTimeImmutableFromObject retrouve l'instance *DateTimeImmutable depuis une Value objet.
func DateTimeImmutableFromObject(z *zone.Zone, v types.Value) (*DateTimeImmutable, error) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, DateError{Class: "TypeError", Message: "Expected DateTimeImmutable object"}
	}
	types.ObjectInfo(z, v)
	reg := lookupRegistry(z)
	if reg == nil {
		return nil, DateError{Class: "Error", Message: "Uninitialized datetime registry"}
	}
	dti, ok := reg.immutables[objKey{slot: v.DataSlot, gen: v.Extra}]
	if !ok {
		return nil, DateError{Class: "TypeError", Message: "Object is not a DateTimeImmutable"}
	}
	return dti, nil
}

// DateTimeInterfaceFromObject retrouve une instance DateTimeInterface (*DateTime ou *DateTimeImmutable).
func DateTimeInterfaceFromObject(z *zone.Zone, v types.Value) (DateTimeInterface, error) {
	v = types.Deref(z, v)
	if v.Type != types.TypeObject {
		return nil, DateError{Class: "TypeError", Message: "Expected DateTimeInterface object"}
	}
	types.ObjectInfo(z, v)
	reg := lookupRegistry(z)
	if reg == nil {
		return nil, DateError{Class: "Error", Message: "Uninitialized datetime registry"}
	}
	k := objKey{slot: v.DataSlot, gen: v.Extra}
	if dt, ok := reg.dateTimes[k]; ok {
		return dt, nil
	}
	if dti, ok := reg.immutables[k]; ok {
		return dti, nil
	}
	return nil, DateError{Class: "TypeError", Message: "Object does not implement DateTimeInterface"}
}

// Implémentation de DateTimeInterface et méthodes sur *DateTime :

func (d *DateTime) Object() types.Value      { return d.obj }
func (d *DateTime) Time() time.Time          { return d.t }
func (d *DateTime) IsImmutable() bool        { return false }
func (d *DateTime) TimeZone() *DateTimeZone  { return &DateTimeZone{z: d.z, name: d.tzName, loc: d.loc} }
func (d *DateTime) Format(f string) string   { return formatPHPDate(d.t, d.tzName, f) }
func (d *DateTime) GetTimestamp() int64      { return d.t.Unix() }
func (d *DateTime) GetOffset() int64         { _, off := d.t.Zone(); return int64(off) }

func (d *DateTime) GetTimezone(z *zone.Zone) (types.Value, *DateTimeZone, error) {
	return NewDateTimeZone(z, d.tzName)
}

func (d *DateTime) SetTimezone(tz *DateTimeZone) (*DateTime, error) {
	if tz == nil {
		return nil, DateError{Class: "TypeError", Message: "DateTime::setTimezone(): Argument #1 ($timezone) must be of type DateTimeZone"}
	}
	d.tzName = tz.name
	d.loc = tz.loc
	d.t = d.t.In(tz.loc)
	return d, nil
}

func (d *DateTime) SetTimestamp(ts int64) *DateTime {
	d.t = time.Unix(ts, 0).In(d.loc)
	return d
}

func (d *DateTime) SetDate(year, month, day int) *DateTime {
	hr, min, sec := d.t.Clock()
	d.t = time.Date(year, time.Month(month), day, hr, min, sec, d.t.Nanosecond(), d.loc)
	return d
}

func (d *DateTime) SetTime(hour, minute, second, microsecond int) *DateTime {
	y, m, day := d.t.Date()
	d.t = time.Date(y, m, day, hour, minute, second, microsecond*1000, d.loc)
	return d
}

func (d *DateTime) SetISODate(year, week, dayOfWeek int) *DateTime {
	hr, min, sec := d.t.Clock()
	d.t = isoDateToTime(year, week, dayOfWeek, hr, min, sec, d.t.Nanosecond(), d.loc)
	return d
}

func (d *DateTime) Modify(modifier string) (*DateTime, error) {
	next, nextTZ, nextLoc, err := applyPHPModifier(d.t, d.tzName, d.loc, modifier)
	if err != nil {
		return nil, err
	}
	d.t = next
	d.tzName = nextTZ
	d.loc = nextLoc
	return d, nil
}

func (d *DateTime) Add(iv *DateInterval) (*DateTime, error) {
	if iv == nil {
		return nil, DateError{Class: "TypeError", Message: "DateTime::add(): Argument #1 ($interval) must be of type DateInterval"}
	}
	d.t = applyDateInterval(d.t, iv, false)
	return d, nil
}

func (d *DateTime) Sub(iv *DateInterval) (*DateTime, error) {
	if iv == nil {
		return nil, DateError{Class: "TypeError", Message: "DateTime::sub(): Argument #1 ($interval) must be of type DateInterval"}
	}
	d.t = applyDateInterval(d.t, iv, true)
	return d, nil
}

func (d *DateTime) Diff(z *zone.Zone, target DateTimeInterface, absolute bool) (types.Value, *DateInterval, error) {
	if target == nil {
		return types.Value{}, nil, DateError{Class: "TypeError", Message: "DateTime::diff(): Argument #1 ($targetObject) must be of type DateTimeInterface"}
	}
	return computeDateDiff(z, d.t, target.Time(), absolute)
}

// CreateFromImmutable crée une nouvelle instance mutable de DateTime à partir d'un DateTimeImmutable.
func CreateFromImmutable(z *zone.Zone, imm *DateTimeImmutable) (types.Value, *DateTime, error) {
	if imm == nil {
		return types.Value{}, nil, DateError{Class: "TypeError", Message: "DateTime::createFromImmutable(): Argument #1 ($object) must be of type DateTimeImmutable"}
	}
	return newDateTimeFromTime(z, imm.t, imm.tzName, imm.loc)
}

// CreateFromInterfaceMutable crée une instance mutable de DateTime à partir de tout DateTimeInterface.
func CreateFromInterfaceMutable(z *zone.Zone, src DateTimeInterface) (types.Value, *DateTime, error) {
	if src == nil {
		return types.Value{}, nil, DateError{Class: "TypeError", Message: "DateTime::createFromInterface(): Argument #1 ($object) must be of type DateTimeInterface"}
	}
	tz := src.TimeZone()
	return newDateTimeFromTime(z, src.Time(), tz.name, tz.loc)
}

// Implémentation de DateTimeInterface et méthodes sur *DateTimeImmutable :

func (d *DateTimeImmutable) Object() types.Value     { return d.obj }
func (d *DateTimeImmutable) Time() time.Time         { return d.t }
func (d *DateTimeImmutable) IsImmutable() bool       { return true }
func (d *DateTimeImmutable) TimeZone() *DateTimeZone { return &DateTimeZone{z: d.z, name: d.tzName, loc: d.loc} }
func (d *DateTimeImmutable) Format(f string) string  { return formatPHPDate(d.t, d.tzName, f) }
func (d *DateTimeImmutable) GetTimestamp() int64     { return d.t.Unix() }
func (d *DateTimeImmutable) GetOffset() int64        { _, off := d.t.Zone(); return int64(off) }

func (d *DateTimeImmutable) GetTimezone(z *zone.Zone) (types.Value, *DateTimeZone, error) {
	return NewDateTimeZone(z, d.tzName)
}

func (d *DateTimeImmutable) SetTimezone(z *zone.Zone, tz *DateTimeZone) (types.Value, *DateTimeImmutable, error) {
	if tz == nil {
		return types.Value{}, nil, DateError{Class: "TypeError", Message: "DateTimeImmutable::setTimezone(): Argument #1 ($timezone) must be of type DateTimeZone"}
	}
	return newDateTimeImmutableFromTime(z, d.t.In(tz.loc), tz.name, tz.loc)
}

func (d *DateTimeImmutable) SetTimestamp(z *zone.Zone, ts int64) (types.Value, *DateTimeImmutable, error) {
	return newDateTimeImmutableFromTime(z, time.Unix(ts, 0).In(d.loc), d.tzName, d.loc)
}

func (d *DateTimeImmutable) SetDate(z *zone.Zone, year, month, day int) (types.Value, *DateTimeImmutable, error) {
	hr, min, sec := d.t.Clock()
	next := time.Date(year, time.Month(month), day, hr, min, sec, d.t.Nanosecond(), d.loc)
	return newDateTimeImmutableFromTime(z, next, d.tzName, d.loc)
}

func (d *DateTimeImmutable) SetTime(z *zone.Zone, hour, minute, second, microsecond int) (types.Value, *DateTimeImmutable, error) {
	y, m, day := d.t.Date()
	next := time.Date(y, m, day, hour, minute, second, microsecond*1000, d.loc)
	return newDateTimeImmutableFromTime(z, next, d.tzName, d.loc)
}

func (d *DateTimeImmutable) SetISODate(z *zone.Zone, year, week, dayOfWeek int) (types.Value, *DateTimeImmutable, error) {
	hr, min, sec := d.t.Clock()
	next := isoDateToTime(year, week, dayOfWeek, hr, min, sec, d.t.Nanosecond(), d.loc)
	return newDateTimeImmutableFromTime(z, next, d.tzName, d.loc)
}

func (d *DateTimeImmutable) Modify(z *zone.Zone, modifier string) (types.Value, *DateTimeImmutable, error) {
	next, nextTZ, nextLoc, err := applyPHPModifier(d.t, d.tzName, d.loc, modifier)
	if err != nil {
		return types.Value{}, nil, err
	}
	return newDateTimeImmutableFromTime(z, next, nextTZ, nextLoc)
}

func (d *DateTimeImmutable) Add(z *zone.Zone, iv *DateInterval) (types.Value, *DateTimeImmutable, error) {
	if iv == nil {
		return types.Value{}, nil, DateError{Class: "TypeError", Message: "DateTimeImmutable::add(): Argument #1 ($interval) must be of type DateInterval"}
	}
	next := applyDateInterval(d.t, iv, false)
	return newDateTimeImmutableFromTime(z, next, d.tzName, d.loc)
}

func (d *DateTimeImmutable) Sub(z *zone.Zone, iv *DateInterval) (types.Value, *DateTimeImmutable, error) {
	if iv == nil {
		return types.Value{}, nil, DateError{Class: "TypeError", Message: "DateTimeImmutable::sub(): Argument #1 ($interval) must be of type DateInterval"}
	}
	next := applyDateInterval(d.t, iv, true)
	return newDateTimeImmutableFromTime(z, next, d.tzName, d.loc)
}

func (d *DateTimeImmutable) Diff(z *zone.Zone, target DateTimeInterface, absolute bool) (types.Value, *DateInterval, error) {
	if target == nil {
		return types.Value{}, nil, DateError{Class: "TypeError", Message: "DateTimeImmutable::diff(): Argument #1 ($targetObject) must be of type DateTimeInterface"}
	}
	return computeDateDiff(z, d.t, target.Time(), absolute)
}

// CreateFromMutable crée une nouvelle instance de DateTimeImmutable à partir d'un DateTime mutable.
func CreateFromMutable(z *zone.Zone, mut *DateTime) (types.Value, *DateTimeImmutable, error) {
	if mut == nil {
		return types.Value{}, nil, DateError{Class: "TypeError", Message: "DateTimeImmutable::createFromMutable(): Argument #1 ($object) must be of type DateTime"}
	}
	return newDateTimeImmutableFromTime(z, mut.t, mut.tzName, mut.loc)
}

// CreateFromInterfaceImmutable crée une instance de DateTimeImmutable à partir de tout DateTimeInterface.
func CreateFromInterfaceImmutable(z *zone.Zone, src DateTimeInterface) (types.Value, *DateTimeImmutable, error) {
	if src == nil {
		return types.Value{}, nil, DateError{Class: "TypeError", Message: "DateTimeImmutable::createFromInterface(): Argument #1 ($object) must be of type DateTimeInterface"}
	}
	tz := src.TimeZone()
	return newDateTimeImmutableFromTime(z, src.Time(), tz.name, tz.loc)
}

func isoDateToTime(year, week, dayOfWeek, hr, min, sec, nsec int, loc *time.Location) time.Time {
	// En norme ISO 8601, la semaine 1 est celle qui contient le 4 janvier.
	jan4 := time.Date(year, time.January, 4, hr, min, sec, nsec, loc)
	isoWeekday := int(jan4.Weekday())
	if isoWeekday == 0 {
		isoWeekday = 7
	}
	week1Monday := jan4.AddDate(0, 0, -(isoWeekday - 1))
	return week1Monday.AddDate(0, 0, (week-1)*7+(dayOfWeek-1))
}
