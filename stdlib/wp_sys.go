// Code créé par agent — Lot 7.3 : fonctions système, réseau et utilitaires sollicités par WordPress.
package stdlib

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// État d'exécution propre à une Zone, porté par z.Slot(zone.SlotStdlib) : chaînes et scalaires Go,
// aucune référence dans l'arène. Une Zone n'est tenue que par une goroutine : aucun verrou. Zone.Reset
// efface ce slot après le drainage des nettoyages, quel que soit le répartiteur fourni.
type sysState struct {
	ignoreAbort bool
	lastErr     *lastError
	jsonErr     int64
	uploads     map[string]struct{}
}

type lastError struct {
	typ     int64
	message string
	file    string
	line    int64
}

// CleanupSysState est la sorte de zone.CleanupItem reconnue par DispatchCleanup. L'état système n'en
// inscrit plus aucun ; la constante reste réservée pour la compatibilité des répartiteurs.
const CleanupSysState zone.CleanupKind = 58

// sysStateFor retourne l'état de la Zone, créé au premier accès. Pour une Zone nil, l'état rendu
// n'est pas conservé entre deux appels.
func sysStateFor(z *zone.Zone) *sysState {
	if st, ok := z.Slot(zone.SlotStdlib).(*sysState); ok {
		return st
	}
	st := &sysState{}
	z.SetSlot(zone.SlotStdlib, st)
	return st
}

// sysStatePeek retourne l'état de la Zone sans le créer, ou nil.
func sysStatePeek(z *zone.Zone) *sysState {
	st, _ := z.Slot(zone.SlotStdlib).(*sysState)
	return st
}

// DispatchCleanup libère l'état système sur un élément CleanupSysState ; les autres sortes sont ignorées.
func DispatchCleanup(z *zone.Zone, item zone.CleanupItem) {
	if item.Kind != CleanupSysState {
		return
	}
	z.SetSlot(zone.SlotStdlib, nil)
}

// ResetSysState efface l'état de requête (ignore_user_abort, dernière erreur, erreur JSON, fichiers
// téléversés) d'une Zone.
func ResetSysState(z *zone.Zone) {
	z.SetSlot(zone.SlotStdlib, nil)
}

// RecordError consigne la dernière erreur levée pour error_get_last ; l'appelant est le moteur d'exécution.
// Les chaînes sont copiées hors de l'arène : l'appelant peut libérer les siennes au retour.
func RecordError(z *zone.Zone, typ int64, message, file string, line int64) {
	sysStateFor(z).lastErr = &lastError{typ: typ, message: strings.Clone(message), file: strings.Clone(file), line: line}
}

// RegisterUploadedFile inscrit path parmi les fichiers téléversés de la requête portée par z ; seul un
// chemin inscrit satisfait is_uploaded_file et move_uploaded_file. Le chemin est résolu contre le
// répertoire courant de la Zone puis normalisé, ce qui le copie hors de l'arène.
func RegisterUploadedFile(z *zone.Zone, path string) {
	st := sysStateFor(z)
	if st.uploads == nil {
		st.uploads = make(map[string]struct{})
	}
	st.uploads[uploadKey(z, path)] = struct{}{}
}

// CleanupUploadedFiles supprime du disque et retire du registre de la Zone tous les fichiers
// temporaires téléversés qui n'ont pas été déplacés par move_uploaded_file.
func CleanupUploadedFiles(z *zone.Zone) {
	st := sysStatePeek(z)
	if st == nil {
		return
	}
	for p := range st.uploads {
		_ = os.Remove(p)
		delete(st.uploads, p)
	}
}

// uploadKey est la clé d'un chemin dans l'ensemble des fichiers téléversés : chemin résolu contre le
// répertoire courant de la Zone et normalisé, en copie indépendante de l'arène.
func uploadKey(z *zone.Zone, path string) string {
	return strings.Clone(filepath.Clean(z.ResolvePath(path)))
}

// Constants retourne les constantes PHP propres aux fonctions de ce paquet.
func Constants() map[string]types.Value { return sysConstants }

var sysConstants = map[string]types.Value{
	"CONNECTION_NORMAL":  types.NewLong(0),
	"CONNECTION_ABORTED": types.NewLong(1),
	"CONNECTION_TIMEOUT": types.NewLong(2),
	"HTML_SPECIALCHARS":  types.NewLong(0),
	"HTML_ENTITIES":      types.NewLong(1),
	"ENT_COMPAT":         types.NewLong(2),
	"ENT_QUOTES":         types.NewLong(3),
	"ENT_NOQUOTES":       types.NewLong(0),
	"ENT_IGNORE":         types.NewLong(4),
	"ENT_SUBSTITUTE":     types.NewLong(8),
	"ENT_DISALLOWED":     types.NewLong(128),
	"ENT_HTML401":        types.NewLong(0),
	"ENT_XML1":           types.NewLong(16),
	"ENT_XHTML":          types.NewLong(32),
	"ENT_HTML5":          types.NewLong(48),
	"INI_SCANNER_NORMAL": types.NewLong(iniScannerNormal),
	"INI_SCANNER_RAW":    types.NewLong(iniScannerRaw),
	"INI_SCANNER_TYPED":  types.NewLong(iniScannerTyped),
	"INI_USER":           types.NewLong(1),
	"INI_PERDIR":         types.NewLong(2),
	"INI_SYSTEM":         types.NewLong(4),
	"INI_ALL":            types.NewLong(7),

	"JSON_ERROR_NONE":                  types.NewLong(jsonErrorNone),
	"JSON_ERROR_DEPTH":                 types.NewLong(jsonErrorDepth),
	"JSON_ERROR_STATE_MISMATCH":        types.NewLong(2),
	"JSON_ERROR_CTRL_CHAR":             types.NewLong(3),
	"JSON_ERROR_SYNTAX":                types.NewLong(jsonErrorSyntax),
	"JSON_ERROR_UTF8":                  types.NewLong(jsonErrorUTF8),
	"JSON_ERROR_RECURSION":             types.NewLong(6),
	"JSON_ERROR_INF_OR_NAN":            types.NewLong(jsonErrorInfNaN),
	"JSON_ERROR_UNSUPPORTED_TYPE":      types.NewLong(jsonErrorUnsupported),
	"JSON_ERROR_INVALID_PROPERTY_NAME": types.NewLong(9),
	"JSON_ERROR_UTF16":                 types.NewLong(10),
	"JSON_OBJECT_AS_ARRAY":             types.NewLong(1),
	"JSON_BIGINT_AS_STRING":            types.NewLong(2),
	"JSON_NUMERIC_CHECK":               types.NewLong(32),
	"JSON_PRESERVE_ZERO_FRACTION":      types.NewLong(1024),
	"JSON_PARTIAL_OUTPUT_ON_ERROR":     types.NewLong(512),
	"JSON_UNESCAPED_LINE_TERMINATORS":  types.NewLong(2048),
	"JSON_INVALID_UTF8_IGNORE":         types.NewLong(1048576),
	"JSON_INVALID_UTF8_SUBSTITUTE":     types.NewLong(2097152),
}

// Extensions déclarées chargées par php127, dans la casse canonique de PHP.
var loadedExtensions = [...]string{
	"Core", "date", "openssl", "pcre", "zlib", "ctype", "fileinfo", "filter", "hash",
	"iconv", "json", "mbstring", "SPL", "PDO", "pdo_sqlite", "session", "Reflection", "standard",
	"mysqli", "tokenizer", "xml", "random", "gd", "curl",
}

func isLoadedExtension(name string) bool {
	for _, e := range loadedExtensions {
		if strings.EqualFold(e, name) {
			return true
		}
	}
	return false
}

func isNullArg(z *zone.Zone, args []types.Value, i int) bool {
	if len(args) <= i {
		return true
	}
	v := deref(z, args[i])
	return v.IsNull() || v.IsUndef()
}

func asBoolParam(z *zone.Zone, v types.Value, fn, idx, param, want string) (bool, error) {
	v = deref(z, v)
	if v.Type == types.TypeArray || v.Type == types.TypeObject {
		return false, typeErr(fn, idx, param, want, typeKind(z, v))
	}
	return asBool(z, v), nil
}

// assignOut écrit une valeur dans un argument passé par référence, puis libère la copie locale.
func assignOut(z *zone.Zone, args []types.Value, i int, v types.Value) {
	if len(args) > i && args[i].Type == types.TypeReference {
		types.AssignRef(z, args[i], v)
	}
	types.ReleaseValue(z, &v)
}

func nativeSetTimeLimit(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("set_time_limit", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	if _, err := asIntParam(z, args[0], "set_time_limit", "1", "seconds"); err != nil {
		return types.Value{}, err
	}
	return types.NewBool(true), nil
}

func nativeIgnoreUserAbort(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("ignore_user_abort", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	st := sysStateFor(z)
	prev := st.ignoreAbort
	if !isNullArg(z, args, 0) {
		b, err := asBoolParam(z, args[0], "ignore_user_abort", "1", "enable", "?bool")
		if err != nil {
			return types.Value{}, err
		}
		st.ignoreAbort = b
	}
	if prev {
		return types.NewLong(1), nil
	}
	return types.NewLong(0), nil
}

func nativeConnectionAborted(_ *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("connection_aborted", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewLong(0), nil
}

func nativeConnectionStatus(_ *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("connection_status", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewLong(0), nil
}

func nativeExtensionLoaded(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("extension_loaded", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	name, err := asStringParam(z, args[0], "extension_loaded", "1", "extension")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewBool(isLoadedExtension(name)), nil
}

func nativeGetLoadedExtensions(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("get_loaded_extensions", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	zendExt := false
	if len(args) == 1 {
		var err error
		if zendExt, err = asBoolParam(z, args[0], "get_loaded_extensions", "1", "zend_extensions", "bool"); err != nil {
			return types.Value{}, err
		}
	}
	arr, err := newArray(z)
	if err != nil || zendExt {
		return arr, err
	}
	for _, e := range loadedExtensions {
		if err := appendString(z, &arr, e); err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
	}
	return arr, nil
}

func appendString(z *zone.Zone, arr *types.Value, s string) error {
	v, err := newStr(z, s)
	if err != nil {
		return err
	}
	err = appendKey(z, arr, v)
	types.ReleaseValue(z, &v)
	return err
}

func setStringKey(z *zone.Zone, arr *types.Value, key string, val types.Value) error {
	k, err := newStr(z, key)
	if err != nil {
		return err
	}
	err = setKey(z, arr, k, val)
	types.ReleaseValue(z, &k)
	return err
}

func setStringKeyStr(z *zone.Zone, arr *types.Value, key, val string) error {
	v, err := newStr(z, val)
	if err != nil {
		return err
	}
	err = setStringKey(z, arr, key, v)
	types.ReleaseValue(z, &v)
	return err
}

func nativeGetCfgVar(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("get_cfg_var", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	if _, err := asStringParam(z, args[0], "get_cfg_var", "1", "option"); err != nil {
		return types.Value{}, err
	}
	return types.NewBool(false), nil
}

// sys_getloadavg lit sysinfo(2), dont la précision (1/65536) est celle de getloadavg(3) observée sous PHP.
func nativeSysGetloadavg(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("sys_getloadavg", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	var loads [3]float64
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err == nil {
		for i := range loads {
			loads[i] = float64(info.Loads[i]) / 65536
		}
	} else {
		raw, rerr := os.ReadFile("/proc/loadavg")
		if rerr != nil {
			return types.NewBool(false), nil
		}
		fields := strings.Fields(string(raw))
		if len(fields) < 3 {
			return types.NewBool(false), nil
		}
		for i := range loads {
			f, perr := strconv.ParseFloat(fields[i], 64)
			if perr != nil {
				return types.NewBool(false), nil
			}
			loads[i] = f
		}
	}
	arr, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, l := range loads {
		if err := appendKey(z, &arr, types.NewDouble(l)); err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
	}
	return arr, nil
}

func nativeFalse0(name string) NativeFunc {
	return func(_ *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 0, 0); err != nil {
			return types.Value{}, err
		}
		return types.NewBool(false), nil
	}
}

func nativeProcID(name string, id func() int) NativeFunc {
	return func(_ *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 0, 0); err != nil {
			return types.Value{}, err
		}
		return types.NewLong(int64(id())), nil
	}
}

func nativeGetCurrentUser(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("get_current_user", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	u, err := user.LookupId(strconv.Itoa(os.Getuid()))
	if err != nil {
		return newStr(z, "")
	}
	return newStr(z, u.Username)
}

func nativeErrorGetLast(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("error_get_last", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	le := sysStateFor(z).lastErr
	if le == nil {
		return types.NewNull(), nil
	}
	arr, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	if err = setStringKey(z, &arr, "type", types.NewLong(le.typ)); err == nil {
		if err = setStringKeyStr(z, &arr, "message", le.message); err == nil {
			if err = setStringKeyStr(z, &arr, "file", le.file); err == nil {
				err = setStringKey(z, &arr, "line", types.NewLong(le.line))
			}
		}
	}
	if err != nil {
		types.ReleaseValue(z, &arr)
		return types.Value{}, err
	}
	return arr, nil
}

func nativeErrorClearLast(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("error_clear_last", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	sysStateFor(z).lastErr = nil
	return types.NewNull(), nil
}

// utf8Next décode un point de code selon php_next_utf8_char (ext/standard/html.c) : en cas d'échec,
// l'avance suit la règle MB_FAILURE de PHP afin que le nombre de '?' produits coïncide.
func utf8Next(s string, pos int) (rune, int, bool) {
	n := len(s)
	c := s[pos]
	lead := func(b byte) bool { return b < 0x80 || (b >= 0xc2 && b <= 0xf4) }
	trail := func(b byte) bool { return b >= 0x80 && b <= 0xbf }
	avail := n - pos
	switch {
	case c < 0x80:
		return rune(c), 1, true
	case c < 0xc2:
		return 0, 1, false
	case c < 0xe0:
		if avail < 2 {
			return 0, 1, false
		}
		if !trail(s[pos+1]) {
			if lead(s[pos+1]) {
				return 0, 1, false
			}
			return 0, 2, false
		}
		return rune(c&0x1f)<<6 | rune(s[pos+1]&0x3f), 2, true
	case c < 0xf0:
		if avail < 3 || !trail(s[pos+1]) || !trail(s[pos+2]) {
			switch {
			case avail < 2 || lead(s[pos+1]):
				return 0, 1, false
			case avail < 3 || lead(s[pos+2]):
				return 0, 2, false
			default:
				return 0, 3, false
			}
		}
		r := rune(c&0x0f)<<12 | rune(s[pos+1]&0x3f)<<6 | rune(s[pos+2]&0x3f)
		if r < 0x800 || (r >= 0xd800 && r <= 0xdfff) {
			return 0, 3, false
		}
		return r, 3, true
	case c < 0xf5:
		if avail < 4 || !trail(s[pos+1]) || !trail(s[pos+2]) || !trail(s[pos+3]) {
			switch {
			case avail < 2 || lead(s[pos+1]):
				return 0, 1, false
			case avail < 3 || lead(s[pos+2]):
				return 0, 2, false
			case avail < 4 || lead(s[pos+3]):
				return 0, 3, false
			default:
				return 0, 4, false
			}
		}
		r := rune(c&0x07)<<18 | rune(s[pos+1]&0x3f)<<12 | rune(s[pos+2]&0x3f)<<6 | rune(s[pos+3]&0x3f)
		if r < 0x10000 || r > 0x10ffff {
			return 0, 4, false
		}
		return r, 4, true
	default:
		return 0, 1, false
	}
}

func nativeUtf8Decode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("utf8_decode", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "utf8_decode", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	out := make([]byte, 0, len(s))
	for pos := 0; pos < len(s); {
		r, adv, ok := utf8Next(s, pos)
		pos += adv
		if !ok || r > 0xff {
			out = append(out, '?')
		} else {
			out = append(out, byte(r))
		}
	}
	return newBytes(z, out)
}

// html401Entities est la table des 252 entités nommées de HTML 4.01 (DTD W3C), triée par point de code ;
// les quatre caractères spéciaux ASCII y figurent, l'apostrophe est traitée à part selon le doctype.
var html401Entities = [...]struct {
	r    rune
	name string
}{
	{'"', "quot"}, {'&', "amp"}, {'<', "lt"}, {'>', "gt"},
	{160, "nbsp"}, {161, "iexcl"}, {162, "cent"}, {163, "pound"}, {164, "curren"}, {165, "yen"},
	{166, "brvbar"}, {167, "sect"}, {168, "uml"}, {169, "copy"}, {170, "ordf"}, {171, "laquo"},
	{172, "not"}, {173, "shy"}, {174, "reg"}, {175, "macr"}, {176, "deg"}, {177, "plusmn"},
	{178, "sup2"}, {179, "sup3"}, {180, "acute"}, {181, "micro"}, {182, "para"}, {183, "middot"},
	{184, "cedil"}, {185, "sup1"}, {186, "ordm"}, {187, "raquo"}, {188, "frac14"}, {189, "frac12"},
	{190, "frac34"}, {191, "iquest"}, {192, "Agrave"}, {193, "Aacute"}, {194, "Acirc"}, {195, "Atilde"},
	{196, "Auml"}, {197, "Aring"}, {198, "AElig"}, {199, "Ccedil"}, {200, "Egrave"}, {201, "Eacute"},
	{202, "Ecirc"}, {203, "Euml"}, {204, "Igrave"}, {205, "Iacute"}, {206, "Icirc"}, {207, "Iuml"},
	{208, "ETH"}, {209, "Ntilde"}, {210, "Ograve"}, {211, "Oacute"}, {212, "Ocirc"}, {213, "Otilde"},
	{214, "Ouml"}, {215, "times"}, {216, "Oslash"}, {217, "Ugrave"}, {218, "Uacute"}, {219, "Ucirc"},
	{220, "Uuml"}, {221, "Yacute"}, {222, "THORN"}, {223, "szlig"}, {224, "agrave"}, {225, "aacute"},
	{226, "acirc"}, {227, "atilde"}, {228, "auml"}, {229, "aring"}, {230, "aelig"}, {231, "ccedil"},
	{232, "egrave"}, {233, "eacute"}, {234, "ecirc"}, {235, "euml"}, {236, "igrave"}, {237, "iacute"},
	{238, "icirc"}, {239, "iuml"}, {240, "eth"}, {241, "ntilde"}, {242, "ograve"}, {243, "oacute"},
	{244, "ocirc"}, {245, "otilde"}, {246, "ouml"}, {247, "divide"}, {248, "oslash"}, {249, "ugrave"},
	{250, "uacute"}, {251, "ucirc"}, {252, "uuml"}, {253, "yacute"}, {254, "thorn"}, {255, "yuml"},
	{338, "OElig"}, {339, "oelig"}, {352, "Scaron"}, {353, "scaron"}, {376, "Yuml"}, {402, "fnof"},
	{710, "circ"}, {732, "tilde"},
	{913, "Alpha"}, {914, "Beta"}, {915, "Gamma"}, {916, "Delta"}, {917, "Epsilon"}, {918, "Zeta"},
	{919, "Eta"}, {920, "Theta"}, {921, "Iota"}, {922, "Kappa"}, {923, "Lambda"}, {924, "Mu"},
	{925, "Nu"}, {926, "Xi"}, {927, "Omicron"}, {928, "Pi"}, {929, "Rho"}, {931, "Sigma"},
	{932, "Tau"}, {933, "Upsilon"}, {934, "Phi"}, {935, "Chi"}, {936, "Psi"}, {937, "Omega"},
	{945, "alpha"}, {946, "beta"}, {947, "gamma"}, {948, "delta"}, {949, "epsilon"}, {950, "zeta"},
	{951, "eta"}, {952, "theta"}, {953, "iota"}, {954, "kappa"}, {955, "lambda"}, {956, "mu"},
	{957, "nu"}, {958, "xi"}, {959, "omicron"}, {960, "pi"}, {961, "rho"}, {962, "sigmaf"},
	{963, "sigma"}, {964, "tau"}, {965, "upsilon"}, {966, "phi"}, {967, "chi"}, {968, "psi"},
	{969, "omega"}, {977, "thetasym"}, {978, "upsih"}, {982, "piv"},
	{8194, "ensp"}, {8195, "emsp"}, {8201, "thinsp"}, {8204, "zwnj"}, {8205, "zwj"}, {8206, "lrm"},
	{8207, "rlm"}, {8211, "ndash"}, {8212, "mdash"}, {8216, "lsquo"}, {8217, "rsquo"}, {8218, "sbquo"},
	{8220, "ldquo"}, {8221, "rdquo"}, {8222, "bdquo"}, {8224, "dagger"}, {8225, "Dagger"}, {8226, "bull"},
	{8230, "hellip"}, {8240, "permil"}, {8242, "prime"}, {8243, "Prime"}, {8249, "lsaquo"}, {8250, "rsaquo"},
	{8254, "oline"}, {8260, "frasl"}, {8364, "euro"}, {8465, "image"}, {8472, "weierp"}, {8476, "real"},
	{8482, "trade"}, {8501, "alefsym"}, {8592, "larr"}, {8593, "uarr"}, {8594, "rarr"}, {8595, "darr"},
	{8596, "harr"}, {8629, "crarr"}, {8656, "lArr"}, {8657, "uArr"}, {8658, "rArr"}, {8659, "dArr"},
	{8660, "hArr"}, {8704, "forall"}, {8706, "part"}, {8707, "exist"}, {8709, "empty"}, {8711, "nabla"},
	{8712, "isin"}, {8713, "notin"}, {8715, "ni"}, {8719, "prod"}, {8721, "sum"}, {8722, "minus"},
	{8727, "lowast"}, {8730, "radic"}, {8733, "prop"}, {8734, "infin"}, {8736, "ang"}, {8743, "and"},
	{8744, "or"}, {8745, "cap"}, {8746, "cup"}, {8747, "int"}, {8756, "there4"}, {8764, "sim"},
	{8773, "cong"}, {8776, "asymp"}, {8800, "ne"}, {8801, "equiv"}, {8804, "le"}, {8805, "ge"},
	{8834, "sub"}, {8835, "sup"}, {8836, "nsub"}, {8838, "sube"}, {8839, "supe"}, {8853, "oplus"},
	{8855, "otimes"}, {8869, "perp"}, {8901, "sdot"}, {8968, "lceil"}, {8969, "rceil"}, {8970, "lfloor"},
	{8971, "rfloor"}, {9001, "lang"}, {9002, "rang"}, {9674, "loz"}, {9824, "spades"}, {9827, "clubs"},
	{9829, "hearts"}, {9830, "diams"},
}

const (
	entHTMLQuoteSingle = 1
	entHTMLQuoteDouble = 2
	entDoctypeMask     = 48
	entXML1            = 16
	entXHTML           = 32
)

func nativeGetHTMLTranslationTable(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "get_html_translation_table"
	if err := arity(fn, len(args), 0, 3); err != nil {
		return types.Value{}, err
	}
	table, flags := int64(0), int64(3|8)
	var err error
	if len(args) >= 1 {
		if table, err = asIntParam(z, args[0], fn, "1", "table"); err != nil {
			return types.Value{}, err
		}
	}
	if len(args) >= 2 {
		if flags, err = asIntParam(z, args[1], fn, "2", "flags"); err != nil {
			return types.Value{}, err
		}
	}
	latin1 := false
	if len(args) == 3 {
		enc, err := asStringParam(z, args[2], fn, "3", "encoding")
		if err != nil {
			return types.Value{}, err
		}
		switch strings.ToUpper(enc) {
		case "ISO-8859-1", "ISO8859-1":
			latin1 = true
		}
	}
	doctype := flags & entDoctypeMask
	all := table != 0 && doctype != entXML1
	// Mesuré sur PHP 8.3 : &#039; en HTML 4.01, et en XHTML pour la seule table complète.
	apos := "&apos;"
	if doctype == 0 || (doctype == entXHTML && all) {
		apos = "&#039;"
	}
	arr, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	add := func(r rune, ent string) error {
		var key string
		if latin1 {
			key = string([]byte{byte(r)})
		} else {
			key = string(r)
		}
		return setStringKeyStr(z, &arr, key, ent)
	}
	for _, e := range html401Entities {
		if e.r > '>' && !all {
			break
		}
		if latin1 && e.r > 0xff {
			break
		}
		if e.r == '"' && flags&entHTMLQuoteDouble == 0 {
			continue
		}
		if e.r == '&' && flags&entHTMLQuoteSingle != 0 {
			if err := add('&', "&amp;"); err != nil {
				types.ReleaseValue(z, &arr)
				return types.Value{}, err
			}
			err = add('\'', apos)
		} else {
			err = add(e.r, "&"+e.name+";")
		}
		if err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
	}
	return arr, nil
}

// parseInet suit inet_pton(3) de la glibc : IPv6 si la chaîne contient « : », IPv4 si elle contient « . ».
func parseInet(s string) ([]byte, bool) {
	switch {
	case strings.IndexByte(s, ':') >= 0:
		ip := net.ParseIP(s)
		if ip == nil || strings.IndexByte(s, '%') >= 0 {
			return nil, false
		}
		return []byte(ip.To16()), true
	case strings.IndexByte(s, '.') >= 0:
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, false
		}
		v4 := ip.To4()
		if v4 == nil {
			return nil, false
		}
		return []byte(v4), true
	default:
		return nil, false
	}
}

func nativeInetPton(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("inet_pton", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "inet_pton", "1", "ip")
	if err != nil {
		return types.Value{}, err
	}
	b, ok := parseInet(s)
	if !ok {
		return types.NewBool(false), nil
	}
	return newBytes(z, b)
}

// formatInet6 reproduit inet_ntop6 de la glibc : plus longue suite de mots nuls (au moins deux),
// première en cas d'égalité, et forme IPv4 encapsulée pour ::a.b.c.d et ::ffff:a.b.c.d.
func formatInet6(b []byte) string {
	var words [8]uint16
	for i := range words {
		words[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
	}
	bestBase, bestLen, curBase, curLen := -1, 0, -1, 0
	for i := range words {
		if words[i] == 0 {
			if curBase == -1 {
				curBase, curLen = i, 1
			} else {
				curLen++
			}
			continue
		}
		if curBase != -1 {
			if bestBase == -1 || curLen > bestLen {
				bestBase, bestLen = curBase, curLen
			}
			curBase = -1
		}
	}
	if curBase != -1 && (bestBase == -1 || curLen > bestLen) {
		bestBase, bestLen = curBase, curLen
	}
	if bestBase != -1 && bestLen < 2 {
		bestBase = -1
	}
	var sb strings.Builder
	for i := range 8 {
		if bestBase != -1 && i >= bestBase && i < bestBase+bestLen {
			if i == bestBase {
				sb.WriteByte(':')
			}
			continue
		}
		if i != 0 {
			sb.WriteByte(':')
		}
		if i == 6 && bestBase == 0 && (bestLen == 6 || (bestLen == 5 && words[5] == 0xffff)) {
			sb.WriteString(net.IP(b[12:16]).String())
			break
		}
		sb.WriteString(strconv.FormatUint(uint64(words[i]), 16))
	}
	if bestBase != -1 && bestBase+bestLen == 8 {
		sb.WriteByte(':')
	}
	return sb.String()
}

func nativeInetNtop(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("inet_ntop", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "inet_ntop", "1", "ip")
	if err != nil {
		return types.Value{}, err
	}
	switch len(s) {
	case 4:
		return newStr(z, net.IP([]byte(s)).String())
	case 16:
		return newStr(z, formatInet6([]byte(s)))
	default:
		return types.NewBool(false), nil
	}
}

func nativeIP2Long(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("ip2long", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "ip2long", "1", "ip")
	if err != nil {
		return types.Value{}, err
	}
	if s == "" || strings.IndexByte(s, ':') >= 0 {
		return types.NewBool(false), nil
	}
	b, ok := parseInet(s)
	if !ok || len(b) != 4 {
		return types.NewBool(false), nil
	}
	return types.NewLong(int64(binary.BigEndian.Uint32(b))), nil
}

func nativeLong2IP(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("long2ip", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	n, err := asIntParam(z, args[0], "long2ip", "1", "ip")
	if err != nil {
		return types.Value{}, err
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(n))
	return newStr(z, net.IP(b[:]).String())
}

// Analyseur INI : transposition des règles de zend_ini_scanner.l (plus longue correspondance, première
// règle en cas d'égalité, mots-clés insensibles à la casse) et de la grammaire de zend_ini_parser.y.

const (
	iniScannerNormal = 0
	iniScannerRaw    = 1
	iniScannerTyped  = 2
)

type iniTokKind uint8

const (
	itEOF iniTokKind = iota
	itEOL
	itSection
	itOffset
	itLabel
	itAssign
	itRBracket
	itQuote
	itDollarCurly
	itVarname
	itRBrace
	itBoolTrue
	itBoolFalse
	itNull
	itConstant
	itNumber
	itString
	itRaw
	itQuoted
	itWhitespace
	itOp
	itOther
)

type iniValKind uint8

const (
	ivString iniValKind = iota
	ivLong
	ivDouble
	ivBool
	ivNull
)

type iniVal struct {
	kind iniValKind
	s    string
	i    int64
	f    float64
	b    bool
}

func (v iniVal) str() string {
	switch v.kind {
	case ivLong:
		return strconv.FormatInt(v.i, 10)
	case ivDouble:
		return formatFloat(v.f, floatCast)
	case ivBool:
		if v.b {
			return "1"
		}
		return ""
	case ivNull:
		return ""
	default:
		return v.s
	}
}

type iniTok struct {
	kind iniTokKind
	text string
	val  iniVal
	op   byte
}

type iniState uint8

const (
	isInitial iniState = iota
	isValue
	isRaw
	isSectionValue
	isSectionRaw
	isOffset
	isDoubleQuotes
	isVarname
)

type iniScanner struct {
	src   string
	buf   []byte // octets de src, lus sans copie par les noyaux de c_src/ini_scan.c
	pos   int
	mode  int64
	state iniState
	stack []iniState
	done  bool
}

func (sc *iniScanner) at(i int) byte {
	if i < len(sc.src) {
		return sc.src[i]
	}
	return 0
}

func (sc *iniScanner) tabs(i int) int {
	return int(C2_ini_skip_hspace(sc.buf, uint64(i), uint64(len(sc.buf)))) - i
}

func (sc *iniScanner) newline(i int) int {
	switch sc.at(i) {
	case '\r':
		if sc.at(i+1) == '\n' {
			return 2
		}
		return 1
	case '\n':
		return 1
	}
	return 0
}

// labelLen mesure {LABEL}, [^=\n\r\t;&|^$~(){}!"\[\]\x00]+ ; l'espace appartient à la clé.
func (sc *iniScanner) labelLen(i int) int {
	return int(C2_ini_scan_key(sc.buf, uint64(i), uint64(len(sc.buf)))) - i
}

// keywords mesure, pour chaque code de C2_ini_kw_code (1 : true/on/yes, 2 : false/off/no/none,
// 3 : null), le plus long mot-clé insensible à la casse qui préfixe src[i:], suivi de ses espaces
// ou tabulations ; 0 si aucun mot-clé du code ne préfixe src[i:].
func (sc *iniScanner) keywords(i int) (kw [4]int) {
	for n := 2; n <= min(5, len(sc.buf)-i); n++ {
		if code := C2_ini_kw_code(sc.buf[i:], uint64(n)); code != 0 {
			kw[code] = n
		}
	}
	for code := 1; code < len(kw); code++ {
		if kw[code] > 0 {
			kw[code] += sc.tabs(i + kw[code])
		}
	}
	return kw
}

// commentLen mesure {TABS}*;[^\r\n]*{NEWLINE}, la fin de flux tenant lieu de fin de ligne.
func (sc *iniScanner) commentLen(i int) int {
	t := sc.tabs(i)
	if sc.at(i+t) != ';' || i+t >= len(sc.src) {
		return 0
	}
	j := int(C2_ini_find_eol(sc.buf, uint64(i+t+1), uint64(len(sc.buf))))
	return j - i + sc.newline(j)
}

func (sc *iniScanner) tabsNewline(i int) int {
	t := sc.tabs(i)
	if nl := sc.newline(i + t); nl > 0 {
		return t + nl
	}
	return 0
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (sc *iniScanner) numberLen(i int) int {
	j := i
	if sc.at(j) == '-' {
		j++
	}
	d := 0
	for isDigit(sc.at(j + d)) {
		d++
	}
	lnum := 0
	if d > 0 {
		lnum = j + d - i
	}
	a := 0
	for isDigit(sc.at(i + a)) {
		a++
	}
	dnum := 0
	if sc.at(i+a) == '.' {
		b := 0
		for isDigit(sc.at(i + a + 1 + b)) {
			b++
		}
		if a > 0 || b > 0 {
			dnum = a + 1 + b
		}
	}
	return max(lnum, dnum)
}

func (sc *iniScanner) constantLen(i int) int {
	c := sc.at(i)
	if !(c == '_' || (c|0x20) >= 'a' && (c|0x20) <= 'z') {
		return 0
	}
	n := 1
	for {
		c = sc.at(i + n)
		if c == '_' || isDigit(c) || (c|0x20) >= 'a' && (c|0x20) <= 'z' {
			n++
			continue
		}
		return n
	}
}

// literalDollar mesure "$"([^{\000]|"\\"{ANY_CHAR}) en retenant l'alternative la plus longue.
func (sc *iniScanner) literalDollar(i int) int {
	if i+1 >= len(sc.src) {
		return 0
	}
	c := sc.src[i+1]
	if c == '{' || c == 0 {
		return 0
	}
	if c == '\\' && i+2 < len(sc.src) {
		return 3
	}
	return 2
}

func (sc *iniScanner) valueCharsLen(i int) int {
	j := i
	for j < len(sc.src) {
		c := sc.src[j]
		if c == '$' {
			n := sc.literalDollar(j)
			if n == 0 {
				break
			}
			j += n
			continue
		}
		if c == 0 || strings.IndexByte("= \t\n\r;&|^~()!\"'", c) >= 0 {
			break
		}
		j++
	}
	return j - i
}

func (sc *iniScanner) sectionValueCharsLen(i int) int {
	j := i
	for j < len(sc.src) {
		c := sc.src[j]
		switch {
		case c == '$':
			n := sc.literalDollar(j)
			if n == 0 {
				return j - i
			}
			j += n
		case c == '\\':
			if j+1 >= len(sc.src) {
				return j - i
			}
			j += 2
		case strings.IndexByte("\n\r;\"']", c) >= 0:
			return j - i
		default:
			j++
		}
	}
	return j - i
}

func (sc *iniScanner) singleQuotedLen(i int) int {
	if sc.at(i) != '\'' {
		return 0
	}
	j := i + 1
	for j < len(sc.src) && sc.src[j] != '\'' {
		j++
	}
	if j >= len(sc.src) || j == i+1 {
		return 0
	}
	return j + 1 - i
}

func (sc *iniScanner) push(s iniState) {
	sc.stack = append(sc.stack, sc.state)
	sc.state = s
}

func (sc *iniScanner) pop() {
	if n := len(sc.stack); n > 0 {
		sc.state = sc.stack[n-1]
		sc.stack = sc.stack[:n-1]
	} else {
		sc.state = isInitial
	}
}

func trimTabs(s string) string { return strings.Trim(s, " \t") }

// cand retient la plus longue correspondance ; à longueur égale, la première règle déclarée l'emporte.
type iniCand struct {
	n    int
	rule int
}

func (c *iniCand) offer(rule, n int) {
	if n > c.n {
		c.n, c.rule = n, rule
	}
}

// typed applique la conversion du mode typé aux jetons émis dans l'état ST_VALUE.
func (sc *iniScanner) typed(t iniTok) iniTok {
	t.val = iniVal{kind: ivString, s: t.text}
	if sc.mode != iniScannerTyped || sc.state != isValue {
		return t
	}
	switch t.kind {
	case itBoolTrue, itBoolFalse:
		t.val = iniVal{kind: ivBool, b: t.kind == itBoolTrue}
	case itNull:
		t.val = iniVal{kind: ivNull}
	case itNumber:
		if !strings.Contains(t.text, ".") {
			if n, err := strconv.ParseInt(t.text, 10, 64); err == nil {
				t.val = iniVal{kind: ivLong, i: n}
			}
		} else if f, err := strconv.ParseFloat(t.text, 64); err == nil {
			t.val = iniVal{kind: ivDouble, f: f}
		}
	}
	return t
}

func (sc *iniScanner) next() iniTok {
	if sc.done {
		return iniTok{kind: itEOF}
	}
	for {
		i := sc.pos
		if i >= len(sc.src) {
			if sc.state == isValue || sc.state == isRaw {
				sc.state = isInitial
				return iniTok{kind: itEOL}
			}
			sc.done = true
			return iniTok{kind: itEOF}
		}
		switch sc.state {
		case isInitial:
			if t, ok := sc.scanInitial(i); ok {
				return t
			}
		case isValue:
			return sc.scanValue(i)
		case isRaw:
			if t, ok := sc.scanRaw(i); ok {
				return t
			}
		case isSectionValue, isOffset:
			return sc.scanSectionOrOffset(i)
		case isSectionRaw:
			return sc.scanSectionRaw(i)
		case isDoubleQuotes:
			return sc.scanDoubleQuotes(i)
		case isVarname:
			return sc.scanVarname(i)
		}
	}
}

func (sc *iniScanner) fail() iniTok {
	sc.done = true
	return iniTok{kind: itEOF}
}

func (sc *iniScanner) scanInitial(i int) (iniTok, bool) {
	var c iniCand
	if sc.src[i] == '[' {
		c.offer(1, 1)
	}
	if l := sc.labelLen(i); l > 0 && sc.at(i+l) == '[' {
		c.offer(4, l+1+sc.tabs(i+l+1))
	}
	kw := sc.keywords(i)
	c.offer(9, kw[1])
	c.offer(10, kw[2])
	c.offer(11, kw[3])
	c.offer(12, sc.labelLen(i))
	if t := sc.tabs(i); sc.at(i+t) == '=' && i+t < len(sc.src) {
		c.offer(13, t+1+sc.tabs(i+t+1))
	}
	if strings.IndexByte(":,.[]\"'()&|^+-/*=%$!~<>?@{}", sc.src[i]) >= 0 {
		c.offer(19, 1)
	}
	c.offer(28, sc.tabs(i))
	c.offer(29, sc.tabsNewline(i))
	c.offer(30, sc.commentLen(i))
	if c.n == 0 {
		return sc.fail(), true
	}
	text := sc.src[i : i+c.n]
	sc.pos = i + c.n
	switch c.rule {
	case 1:
		if sc.mode == iniScannerRaw {
			sc.state = isSectionRaw
		} else {
			sc.state = isSectionValue
		}
		return iniTok{kind: itSection}, true
	case 4:
		sc.state = isOffset
		return iniTok{kind: itOffset, text: trimTabs(strings.TrimRight(trimTabs(text), "["))}, true
	case 9:
		return sc.typed(iniTok{kind: itBoolTrue, text: "1"}), true
	case 10:
		return sc.typed(iniTok{kind: itBoolFalse}), true
	case 11:
		return sc.typed(iniTok{kind: itNull}), true
	case 12:
		return iniTok{kind: itLabel, text: trimTabs(text)}, true
	case 13:
		if sc.mode == iniScannerRaw {
			sc.state = isRaw
		} else {
			sc.state = isValue
		}
		return iniTok{kind: itAssign}, true
	case 19:
		return iniTok{kind: itOther, op: text[0]}, true
	case 28:
		return iniTok{}, false
	default:
		return iniTok{kind: itEOL}, true
	}
}

func (sc *iniScanner) scanValue(i int) iniTok {
	var c iniCand
	c.offer(2, sc.singleQuotedLen(i))
	if sc.src[i] == '$' && sc.at(i+1) == '{' {
		c.offer(6, 2)
	}
	kw := sc.keywords(i)
	c.offer(9, kw[1])
	c.offer(10, kw[2])
	c.offer(11, kw[3])
	c.offer(16, sc.tabsNewline(i))
	c.offer(17, sc.constantLen(i))
	c.offer(18, sc.numberLen(i))
	if strings.IndexByte("&|^~()!", sc.src[i]) >= 0 {
		c.offer(20, 1+sc.tabs(i+1))
	}
	if sc.src[i] == '=' {
		c.offer(21, 1)
	}
	c.offer(22, sc.valueCharsLen(i))
	if t := sc.tabs(i); sc.at(i+t) == '"' && i+t < len(sc.src) {
		c.offer(24, t+1)
	}
	c.offer(27, sc.tabs(i))
	c.offer(30, sc.commentLen(i))
	if c.n == 0 {
		return sc.fail()
	}
	text := sc.src[i : i+c.n]
	sc.pos = i + c.n
	switch c.rule {
	case 2:
		return sc.typed(iniTok{kind: itRaw, text: text[1 : len(text)-1]})
	case 6:
		sc.push(isVarname)
		return iniTok{kind: itDollarCurly}
	case 9:
		return sc.typed(iniTok{kind: itBoolTrue, text: "1"})
	case 10:
		return sc.typed(iniTok{kind: itBoolFalse})
	case 11:
		return sc.typed(iniTok{kind: itNull})
	case 16, 30:
		sc.state = isInitial
		return iniTok{kind: itEOL}
	case 17:
		return sc.typed(iniTok{kind: itConstant, text: text})
	case 18:
		return sc.typed(iniTok{kind: itNumber, text: text})
	case 20:
		return iniTok{kind: itOp, op: text[0]}
	case 21:
		sc.pos = i
		sc.state = isInitial
		return iniTok{kind: itEOL}
	case 22:
		return sc.typed(iniTok{kind: itString, text: text})
	case 24:
		sc.push(isDoubleQuotes)
		return iniTok{kind: itQuote}
	default:
		return sc.typed(iniTok{kind: itWhitespace, text: text})
	}
}

// scanRaw transpose la règle ST_RAW : commentaire au premier « ; » hors guillemets ouvrants,
// espaces finaux retirés, guillemets englobants ôtés.
func (sc *iniScanner) scanRaw(i int) (iniTok, bool) {
	if t := sc.tabsNewline(i); t > 0 {
		sc.pos = i + t
		sc.state = isInitial
		return iniTok{kind: itEOL}, true
	}
	if t := sc.commentLen(i); t > 0 {
		sc.pos = i + t
		sc.state = isInitial
		return iniTok{kind: itEOL}, true
	}
	if t := sc.tabs(i); t > 0 {
		sc.pos = i + t
		return iniTok{}, false
	}
	if sc.src[i] == 0 {
		return sc.fail(), true
	}
	start, j, sc0 := i, i, -1
	for j < len(sc.src) {
		c := sc.src[j]
		if c == '\n' || c == '\r' {
			break
		}
		if c == ';' && sc0 < 0 {
			sc0 = j
		}
		if c == '"' && sc.src[start] == '"' {
			sc0 = -1
		}
		j++
	}
	end := j
	if sc0 >= 0 {
		end = sc0
	}
	sc.pos = end
	text := strings.TrimRight(sc.src[start:end], " \t")
	if len(text) > 1 && text[0] == '"' && text[len(text)-1] == '"' {
		text = text[1 : len(text)-1]
	}
	return iniTok{kind: itRaw, text: text, val: iniVal{s: text}}, true
}

func (sc *iniScanner) closeBracketLen(i int) int {
	if sc.src[i] != ']' {
		return 0
	}
	n := 1 + sc.tabs(i+1)
	return n + sc.newline(i+n)
}

func (sc *iniScanner) scanSectionOrOffset(i int) iniTok {
	var c iniCand
	c.offer(2, sc.singleQuotedLen(i))
	if sc.state == isSectionValue {
		c.offer(3, sc.closeBracketLen(i))
	} else if t := sc.tabs(i); sc.at(i+t) == ']' && i+t < len(sc.src) {
		c.offer(5, t+1)
	}
	if sc.src[i] == '$' && sc.at(i+1) == '{' {
		c.offer(6, 2)
	}
	c.offer(17, sc.constantLen(i))
	c.offer(18, sc.numberLen(i))
	c.offer(23, sc.sectionValueCharsLen(i))
	if t := sc.tabs(i); sc.at(i+t) == '"' && i+t < len(sc.src) {
		c.offer(24, t+1)
	}
	c.offer(27, sc.tabs(i))
	if c.n == 0 {
		return sc.fail()
	}
	text := sc.src[i : i+c.n]
	sc.pos = i + c.n
	switch c.rule {
	case 2:
		return iniTok{kind: itRaw, text: text[1 : len(text)-1], val: iniVal{s: text[1 : len(text)-1]}}
	case 3, 5:
		sc.state = isInitial
		return iniTok{kind: itRBracket}
	case 6:
		sc.push(isVarname)
		return iniTok{kind: itDollarCurly}
	case 17:
		return iniTok{kind: itConstant, text: text, val: iniVal{s: text}}
	case 18:
		return iniTok{kind: itNumber, text: text, val: iniVal{s: text}}
	case 24:
		sc.push(isDoubleQuotes)
		return iniTok{kind: itQuote}
	case 27:
		return iniTok{kind: itWhitespace, text: text, val: iniVal{s: text}}
	default:
		return iniTok{kind: itString, text: text, val: iniVal{s: text}}
	}
}

func (sc *iniScanner) scanSectionRaw(i int) iniTok {
	if n := sc.closeBracketLen(i); n > 0 {
		sc.pos = i + n
		sc.state = isInitial
		return iniTok{kind: itRBracket}
	}
	j := i
	for j < len(sc.src) && sc.src[j] != ']' && sc.src[j] != '\n' && sc.src[j] != '\r' {
		j++
	}
	if j == i {
		return sc.fail()
	}
	sc.pos = j
	text := sc.src[i:j]
	if len(text) > 1 && text[0] == '"' && text[len(text)-1] == '"' {
		text = text[1 : len(text)-1]
	}
	return iniTok{kind: itRaw, text: text, val: iniVal{s: text}}
}

// iniEscape transpose zend_ini_escape_string pour les chaînes entre guillemets doubles.
func iniEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			b.WriteByte('\\')
			break
		}
		switch s[i] {
		case '"', '\\', '$':
			b.WriteByte(s[i])
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func (sc *iniScanner) scanDoubleQuotes(i int) iniTok {
	if sc.src[i] == '"' {
		sc.pos = i + 1 + sc.tabs(i+1)
		sc.pop()
		return iniTok{kind: itQuote}
	}
	if sc.src[i] == '$' && sc.at(i+1) == '{' {
		sc.pos = i + 2
		sc.push(isVarname)
		return iniTok{kind: itDollarCurly}
	}
	s, n := i, len(sc.src)
	for s < n {
		c := sc.src[s]
		s++
		if c == '"' {
			s--
			break
		}
		if c == '$' {
			if s < n && sc.src[s] == '{' {
				s--
				break
			}
			continue
		}
		if c == '\\' && s < n {
			esc := sc.src[s]
			s++
			if esc == '"' && (s >= n || sc.src[s] == '\n' || sc.src[s] == '\r') {
				s--
				break
			}
		}
	}
	sc.pos = s
	text := iniEscape(sc.src[i:s])
	return iniTok{kind: itQuoted, text: text, val: iniVal{s: text}}
}

func (sc *iniScanner) scanVarname(i int) iniTok {
	if sc.src[i] == '}' {
		sc.pos = i + 1
		sc.pop()
		return iniTok{kind: itRBrace}
	}
	l := sc.labelLen(i)
	if l == 0 {
		return sc.fail()
	}
	sc.pos = i + l
	text := trimTabs(sc.src[i : i+l])
	return iniTok{kind: itVarname, text: text, val: iniVal{s: text}}
}

// iniConstants est le jeu de constantes que l'analyseur INI substitue ; les constantes définies
// par le script (define) ne lui sont pas visibles depuis ce paquet.
var iniConstants = map[string]string{
	"E_ERROR": "1", "E_WARNING": "2", "E_PARSE": "4", "E_NOTICE": "8", "E_CORE_ERROR": "16",
	"E_CORE_WARNING": "32", "E_COMPILE_ERROR": "64", "E_COMPILE_WARNING": "128", "E_USER_ERROR": "256",
	"E_USER_WARNING": "512", "E_USER_NOTICE": "1024", "E_STRICT": "2048", "E_RECOVERABLE_ERROR": "4096",
	"E_DEPRECATED": "8192", "E_USER_DEPRECATED": "16384", "E_ALL": "32767",
	"PHP_EOL": "\n", "PHP_OS": "Linux", "PHP_OS_FAMILY": "Linux", "PHP_INT_SIZE": "8",
	"PHP_INT_MAX": "9223372036854775807", "PHP_INT_MIN": "-9223372036854775808",
	"DIRECTORY_SEPARATOR": "/", "PATH_SEPARATOR": ":",
	"INI_SCANNER_NORMAL": "0", "INI_SCANNER_RAW": "1", "INI_SCANNER_TYPED": "2",
}

type iniKey struct {
	isInt bool
	i     int64
	s     string
}

type iniNode struct {
	val iniVal
	arr *iniArr
}

type iniArr struct {
	keys    []iniKey
	vals    map[iniKey]*iniNode
	next    int64
	hasNext bool
}

func newIniArr() *iniArr { return &iniArr{vals: make(map[iniKey]*iniNode)} }

// phpKey applique la règle de clé de tableau de PHP : un entier décimal canonique devient une clé entière.
func phpKey(s string) iniKey {
	if s == "0" {
		return iniKey{isInt: true}
	}
	t := s
	if strings.HasPrefix(t, "-") {
		t = t[1:]
	}
	if t == "" || t[0] < '1' || t[0] > '9' {
		return iniKey{s: s}
	}
	for i := 0; i < len(t); i++ {
		if !isDigit(t[i]) {
			return iniKey{s: s}
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return iniKey{s: s}
	}
	return iniKey{isInt: true, i: n}
}

func (a *iniArr) set(k iniKey, n *iniNode) {
	if _, ok := a.vals[k]; !ok {
		a.keys = append(a.keys, k)
	}
	a.vals[k] = n
	if k.isInt && (!a.hasNext || k.i >= a.next) {
		a.next, a.hasNext = k.i+1, true
	}
}

func (a *iniArr) append(n *iniNode) {
	if !a.hasNext {
		a.next, a.hasNext = 0, true
	}
	a.set(iniKey{isInt: true, i: a.next}, n)
}

type iniParser struct {
	toks []iniTok
	pos  int
	mode int64
}

type iniParseError struct{}

func (p *iniParser) peek() iniTok { return p.toks[p.pos] }

func (p *iniParser) take() iniTok {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *iniParser) expect(k iniTokKind) {
	if p.take().kind != k {
		panic(iniParseError{})
	}
}

// iniPart garde trace des jetons blancs pour la règle du mode typé : un nombre suivi
// seulement de blancs reste un nombre (comportement mesuré sur PHP 8.3).
type iniPart struct {
	val iniVal
	ws  bool
}

func concatParts(parts []iniPart, typed bool) iniVal {
	if len(parts) == 1 {
		return parts[0].val
	}
	if typed && (parts[0].val.kind == ivLong || parts[0].val.kind == ivDouble) {
		onlyWS := true
		for _, pt := range parts[1:] {
			onlyWS = onlyWS && pt.ws
		}
		if onlyWS {
			return parts[0].val
		}
	}
	var b strings.Builder
	for _, pt := range parts {
		b.WriteString(pt.val.str())
	}
	return iniVal{kind: ivString, s: b.String()}
}

func (p *iniParser) varRef() iniVal {
	p.expect(itDollarCurly)
	t := p.take()
	if t.kind != itVarname {
		panic(iniParseError{})
	}
	p.expect(itRBrace)
	return iniVal{kind: ivString, s: os.Getenv(t.text)}
}

func (p *iniParser) encapsed() iniVal {
	p.expect(itQuote)
	var b strings.Builder
	for {
		switch p.peek().kind {
		case itDollarCurly:
			b.WriteString(p.varRef().s)
		case itQuoted:
			b.WriteString(p.take().text)
		case itQuote:
			p.take()
			return iniVal{kind: ivString, s: b.String()}
		default:
			panic(iniParseError{})
		}
	}
}

// stringList lit var_string_list (expand vrai : constantes substituées) ou var_string_list_section.
func (p *iniParser) stringList(expand, allowEmpty bool) iniVal {
	var parts []iniPart
	for {
		t := p.peek()
		switch t.kind {
		case itDollarCurly:
			parts = append(parts, iniPart{val: p.varRef()})
		case itQuote:
			parts = append(parts, iniPart{val: p.encapsed()})
		case itConstant:
			p.take()
			v := t.val
			if expand {
				if c, ok := iniConstants[t.text]; ok {
					v = iniVal{kind: ivString, s: c}
				} else if c, ok := sysConstants[t.text]; ok {
					v = iniVal{kind: ivString, s: strconv.FormatInt(c.ToLong(), 10)}
				}
			}
			parts = append(parts, iniPart{val: v})
		case itRaw, itNumber, itString:
			p.take()
			parts = append(parts, iniPart{val: t.val})
		case itWhitespace:
			p.take()
			parts = append(parts, iniPart{val: t.val, ws: true})
		default:
			if len(parts) == 0 {
				if allowEmpty {
					return iniVal{kind: ivString}
				}
				panic(iniParseError{})
			}
			return concatParts(parts, p.mode == iniScannerTyped)
		}
	}
}

// iniAtoi transpose atoi : strtol saturé sur 64 bits puis troncature vers un int C de 32 bits.
func iniAtoi(v iniVal) int32 {
	switch v.kind {
	case ivLong:
		return int32(v.i)
	case ivDouble:
		return int32(int64(v.f))
	}
	s := strings.TrimLeft(v.s, " \t\n\r\v\f")
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	var n uint64
	sat := false
	for i := 0; i < len(s) && isDigit(s[i]); i++ {
		if n > (1<<63)/10 {
			sat = true
			break
		}
		n = n*10 + uint64(s[i]-'0')
	}
	var r int64
	switch {
	case neg && (sat || n >= 1<<63):
		r = -1 << 63
	case sat || n >= 1<<63:
		r = 1<<63 - 1
	case neg:
		r = -int64(n)
	default:
		r = int64(n)
	}
	return int32(r)
}

func (p *iniParser) opResult(r int32) iniVal {
	if p.mode == iniScannerTyped {
		return iniVal{kind: ivLong, i: int64(r)}
	}
	return iniVal{kind: ivString, s: strconv.FormatInt(int64(r), 10)}
}

func (p *iniParser) unary() iniVal {
	t := p.peek()
	if t.kind == itOp {
		switch t.op {
		case '~':
			p.take()
			return p.opResult(^iniAtoi(p.unary()))
		case '!':
			p.take()
			if iniAtoi(p.unary()) == 0 {
				return p.opResult(1)
			}
			return p.opResult(0)
		case '(':
			p.take()
			v := p.expr()
			if t := p.take(); t.kind != itOp || t.op != ')' {
				panic(iniParseError{})
			}
			return v
		}
		panic(iniParseError{})
	}
	return p.stringList(true, false)
}

func (p *iniParser) expr() iniVal {
	v := p.unary()
	for {
		t := p.peek()
		if t.kind != itOp || strings.IndexByte("|&^", t.op) < 0 {
			return v
		}
		p.take()
		a, b := iniAtoi(v), iniAtoi(p.unary())
		switch t.op {
		case '|':
			v = p.opResult(a | b)
		case '&':
			v = p.opResult(a & b)
		default:
			v = p.opResult(a ^ b)
		}
	}
}

func (p *iniParser) value() iniVal {
	switch t := p.peek(); t.kind {
	case itBoolTrue, itBoolFalse, itNull:
		p.take()
		if p.mode == iniScannerTyped {
			return t.val
		}
		return iniVal{kind: ivString, s: t.text}
	case itEOL, itEOF:
		p.take()
		return iniVal{kind: ivString}
	}
	return p.expr()
}

// parseINI renvoie l'arbre ordonné du document, ou ok faux sur erreur de syntaxe.
func parseINI(src string, sections bool, mode int64) (root *iniArr, ok bool) {
	sc := &iniScanner{src: src, buf: unsafe.Slice(unsafe.StringData(src), len(src)), mode: mode}
	var toks []iniTok
	for {
		t := sc.next()
		toks = append(toks, t)
		if t.kind == itEOF {
			break
		}
	}
	p := &iniParser{toks: toks, mode: mode}
	root = newIniArr()
	active := root
	defer func() {
		if r := recover(); r != nil {
			if _, isParse := r.(iniParseError); !isParse {
				panic(r)
			}
			root, ok = nil, false
		}
	}()
	for {
		t := p.take()
		switch t.kind {
		case itEOF:
			return root, true
		case itEOL:
		case itSection:
			name := p.stringList(false, true)
			p.expect(itRBracket)
			if sections {
				active = newIniArr()
				root.set(phpKey(name.str()), &iniNode{arr: active})
			}
		case itLabel:
			if p.peek().kind != itAssign {
				continue
			}
			p.take()
			v := p.value()
			active.set(phpKey(t.text), &iniNode{val: v})
		case itOffset:
			off := p.stringList(true, true)
			p.expect(itRBracket)
			p.expect(itAssign)
			v := p.value()
			k := phpKey(t.text)
			n, exists := active.vals[k]
			if !exists || n.arr == nil {
				n = &iniNode{arr: newIniArr()}
				active.set(k, n)
			}
			if s := off.str(); s == "" {
				n.arr.append(&iniNode{val: v})
			} else {
				n.arr.set(phpKey(s), &iniNode{val: v})
			}
		default:
			return nil, false
		}
	}
}

func iniToValue(z *zone.Zone, a *iniArr) (types.Value, error) {
	arr, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, k := range a.keys {
		n := a.vals[k]
		var v types.Value
		if n.arr != nil {
			v, err = iniToValue(z, n.arr)
		} else {
			switch n.val.kind {
			case ivLong:
				v = types.NewLong(n.val.i)
			case ivDouble:
				v = types.NewDouble(n.val.f)
			case ivBool:
				v = types.NewBool(n.val.b)
			case ivNull:
				v = types.NewNull()
			default:
				v, err = newStr(z, n.val.s)
			}
		}
		if err == nil {
			if k.isInt {
				err = setKey(z, &arr, types.NewLong(k.i), v)
			} else {
				err = setStringKey(z, &arr, k.s, v)
			}
			types.ReleaseValue(z, &v)
		}
		if err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
	}
	return arr, nil
}

// ParseINI analyse un document INI selon parse_ini_string ; false est rendu sur erreur de syntaxe
// ou mode d'analyse inconnu.
func ParseINI(z *zone.Zone, src string, sections bool, mode int64) (types.Value, error) {
	if mode != iniScannerNormal && mode != iniScannerRaw && mode != iniScannerTyped {
		return types.NewBool(false), nil
	}
	root, ok := parseINI(src, sections, mode)
	if !ok {
		return types.NewBool(false), nil
	}
	return iniToValue(z, root)
}

// INIArgs décode les deuxième et troisième arguments communs à parse_ini_string et parse_ini_file.
func INIArgs(z *zone.Zone, fn string, args []types.Value) (sections bool, mode int64, err error) {
	if len(args) >= 2 {
		if sections, err = asBoolParam(z, args[1], fn, "2", "process_sections", "bool"); err != nil {
			return false, 0, err
		}
	}
	if len(args) >= 3 {
		if mode, err = asIntParam(z, args[2], fn, "3", "scanner_mode"); err != nil {
			return false, 0, err
		}
	}
	return sections, mode, nil
}

func nativeParseIniString(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("parse_ini_string", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	src, err := asStringParam(z, args[0], "parse_ini_string", "1", "ini_string")
	if err != nil {
		return types.Value{}, err
	}
	sections, mode, err := INIArgs(z, "parse_ini_string", args)
	if err != nil {
		return types.Value{}, err
	}
	return ParseINI(z, src, sections, mode)
}

// Analyse d'en-têtes d'image transposée de ext/standard/image.c (PHP 8.3) pour GIF, JPEG et PNG.

const (
	imageTypeGIF  = 1
	imageTypeJPEG = 2
	imageTypePNG  = 3
)

type imageInfo struct {
	width, height, bits, channels int64
}

type imageApp struct {
	name string
	data []byte
}

type imageReader struct {
	b   []byte
	pos int
}

func (r *imageReader) getc() int {
	if r.pos >= len(r.b) {
		return -1
	}
	c := r.b[r.pos]
	r.pos++
	return int(c)
}

func (r *imageReader) read2() int64 {
	if len(r.b)-r.pos < 2 {
		r.pos = len(r.b)
		return 0
	}
	v := int64(r.b[r.pos])<<8 | int64(r.b[r.pos+1])
	r.pos += 2
	return v
}

func (r *imageReader) skip(n int64) bool {
	if n < 0 || int64(len(r.b)-r.pos) < n {
		r.pos = len(r.b)
		return false
	}
	r.pos += int(n)
	return true
}

func (r *imageReader) nextMarker(ffRead bool) int {
	if !ffRead {
		for {
			c := r.getc()
			if c == -1 {
				return 0xd9
			}
			if c == 0xff {
				break
			}
		}
	}
	a := 1
	var m int
	for {
		m = r.getc()
		if m == -1 {
			return 0xd9
		}
		a++
		if m != 0xff {
			break
		}
	}
	if a < 2 {
		return 0xd9
	}
	return m
}

// jpegInfo parcourt les marqueurs comme php_handle_jpeg : premier SOFn pour les dimensions,
// segments APPn recueillis si l'appelant demande $image_info, arrêt sur SOS ou EOI.
func jpegInfo(b []byte, wantInfo bool) (*imageInfo, []imageApp) {
	r := &imageReader{b: b, pos: 3}
	var res *imageInfo
	var apps []imageApp
	ffRead := true
	for {
		m := r.nextMarker(ffRead)
		ffRead = false
		switch {
		case m >= 0xc0 && m <= 0xcf && m != 0xc4 && m != 0xc8 && m != 0xcc:
			if res != nil {
				l := r.read2()
				if l < 2 || !r.skip(l-2) {
					return res, apps
				}
				continue
			}
			res = &imageInfo{}
			l := r.read2()
			res.bits = int64(byte(r.getc()))
			res.height = r.read2()
			res.width = r.read2()
			res.channels = int64(byte(r.getc()))
			if !wantInfo || l < 8 || !r.skip(l-8) {
				return res, apps
			}
		case m >= 0xe0 && m <= 0xef:
			l := r.read2()
			if l < 2 {
				return res, apps
			}
			if !wantInfo {
				if !r.skip(l - 2) {
					return res, apps
				}
				continue
			}
			l -= 2
			if int64(len(r.b)-r.pos) < l {
				return res, apps
			}
			data := r.b[r.pos : r.pos+int(l)]
			r.pos += int(l)
			name := "APP" + strconv.Itoa(m-0xe0)
			if !slices.ContainsFunc(apps, func(a imageApp) bool { return a.name == name }) {
				apps = append(apps, imageApp{name: name, data: data})
			}
		case m == 0xda || m == 0xd9:
			return res, apps
		default:
			l := r.read2()
			if l < 2 || !r.skip(l-2) {
				return res, apps
			}
		}
	}
}

func imageSize(b []byte, wantInfo bool) (int, *imageInfo, []imageApp) {
	switch {
	case len(b) >= 3 && string(b[:3]) == "GIF":
		if len(b) < 11 {
			return imageTypeGIF, nil, nil
		}
		d := b[6:11]
		bits := int64(0)
		if d[4]&0x80 != 0 {
			bits = int64(d[4]&0x07) + 1
		}
		return imageTypeGIF, &imageInfo{
			width: int64(d[0]) | int64(d[1])<<8, height: int64(d[2]) | int64(d[3])<<8,
			bits: bits, channels: 3,
		}, nil
	case len(b) >= 3 && string(b[:3]) == "\xff\xd8\xff":
		info, apps := jpegInfo(b, wantInfo)
		return imageTypeJPEG, info, apps
	case len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		if len(b) < 25 {
			return imageTypePNG, nil, nil
		}
		return imageTypePNG, &imageInfo{
			width:  int64(binary.BigEndian.Uint32(b[16:20])),
			height: int64(binary.BigEndian.Uint32(b[20:24])),
			bits:   int64(b[24]),
		}, nil
	}
	return 0, nil, nil
}

// imageTypeMimes transpose php_image_type_to_mime_type (PHP 8.3) pour IMAGETYPE_GIF (1) à IMAGETYPE_AVIF (19) ;
// une entrée vide vaut application/octet-stream.
var imageTypeMimes = [...]string{
	1: "image/gif", 2: "image/jpeg", 3: "image/png", 4: "application/x-shockwave-flash", 5: "image/psd",
	6: "image/bmp", 7: "image/tiff", 8: "image/tiff", 10: "image/jp2", 13: "application/x-shockwave-flash",
	14: "image/iff", 15: "image/vnd.wap.wbmp", 16: "image/xbm", 17: "image/vnd.microsoft.icon",
	18: "image/webp", 19: "image/avif",
}

func imageTypeMime(typ int64) string {
	if typ > 0 && typ < int64(len(imageTypeMimes)) && imageTypeMimes[typ] != "" {
		return imageTypeMimes[typ]
	}
	return "application/octet-stream"
}

// ImageSize construit le résultat de getimagesize pour le contenu b ; si infoArg est une référence,
// elle reçoit le tableau des segments APPn (vide hors JPEG).
func ImageSize(z *zone.Zone, b []byte, args []types.Value, infoIdx int) (types.Value, error) {
	wantInfo := len(args) > infoIdx
	typ, info, apps := imageSize(b, wantInfo)
	if wantInfo {
		arr, err := newArray(z)
		if err != nil {
			return types.Value{}, err
		}
		for _, a := range apps {
			if err := setStringKeyStr(z, &arr, a.name, string(a.data)); err != nil {
				types.ReleaseValue(z, &arr)
				return types.Value{}, err
			}
		}
		assignOut(z, args, infoIdx, arr)
	}
	if info == nil {
		return types.NewBool(false), nil
	}
	arr, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	err = appendKey(z, &arr, types.NewLong(info.width))
	if err == nil {
		err = appendKey(z, &arr, types.NewLong(info.height))
	}
	if err == nil {
		err = appendKey(z, &arr, types.NewLong(int64(typ)))
	}
	if err == nil {
		err = appendString(z, &arr, "width=\""+strconv.FormatInt(int64(int32(info.width)), 10)+"\" height=\""+strconv.FormatInt(int64(int32(info.height)), 10)+"\"")
	}
	if err == nil && info.bits != 0 {
		err = setStringKey(z, &arr, "bits", types.NewLong(info.bits))
	}
	if err == nil && info.channels != 0 {
		err = setStringKey(z, &arr, "channels", types.NewLong(info.channels))
	}
	if err == nil {
		err = setStringKeyStr(z, &arr, "mime", imageTypeMime(int64(typ)))
	}
	if err != nil {
		types.ReleaseValue(z, &arr)
		return types.Value{}, err
	}
	return arr, nil
}

func nativeGetimagesizefromstring(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("getimagesizefromstring", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asStringParam(z, args[0], "getimagesizefromstring", "1", "string")
	if err != nil {
		return types.Value{}, err
	}
	return ImageSize(z, []byte(s), args, 1)
}

func nativeImageTypeToMimeType(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("image_type_to_mime_type", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	typ, err := asIntParam(z, args[0], "image_type_to_mime_type", "1", "image_type")
	if err != nil {
		return types.Value{}, err
	}
	return newStr(z, imageTypeMime(typ))
}

// nativeExifImagetype lit les huit premiers octets du fichier et les soumet au détecteur de getimagesize ;
// false si le fichier est illisible ou le type inconnu.
func nativeExifImagetype(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("exif_imagetype", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	path, err := asStringParam(z, args[0], "exif_imagetype", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	if err := noNullBytes(path, "exif_imagetype", "1", "filename"); err != nil {
		return types.Value{}, err
	}
	f, err := os.Open(z.ResolvePath(path))
	if err != nil {
		return types.NewBool(false), nil
	}
	defer f.Close()
	var head [8]byte
	n, _ := io.ReadFull(f, head[:])
	typ, _, _ := imageSize(head[:n], false)
	if typ == 0 {
		return types.NewBool(false), nil
	}
	return types.NewLong(int64(typ)), nil
}

// nativeStatID rend l'uid (fileowner) ou le gid (filegroup) de stat(2) sur le chemin, false en cas d'échec.
// Un chemin relatif se résout contre le répertoire courant de la Zone.
func nativeStatID(name string, group bool) NativeFunc {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(name, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		path, err := asStringParam(z, args[0], name, "1", "filename")
		if err != nil {
			return types.Value{}, err
		}
		fi, err := os.Stat(z.ResolvePath(path))
		if err != nil {
			return types.NewBool(false), nil
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return types.NewBool(false), nil
		}
		if group {
			return types.NewLong(int64(st.Gid)), nil
		}
		return types.NewLong(int64(st.Uid)), nil
	}
}

// accessExecute est X_OK de access(2).
const accessExecute = 1

// nativeIsExecutable suit PHP 8.3 sur le gestionnaire de fichiers local : access(2) avec X_OK, de sorte
// qu'un répertoire traversable est exécutable.
func nativeIsExecutable(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_executable", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	path, err := asStringParam(z, args[0], "is_executable", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return types.NewBool(false), nil
	}
	return types.NewBool(syscall.Access(z.ResolvePath(path), accessExecute) == nil), nil
}

func nativeIsUploadedFile(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_uploaded_file", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	path, err := asStringParam(z, args[0], "is_uploaded_file", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	if err := noNullBytes(path, "is_uploaded_file", "1", "filename"); err != nil {
		return types.Value{}, err
	}
	st := sysStatePeek(z)
	if st == nil {
		return types.NewBool(false), nil
	}
	_, ok := st.uploads[filepath.Clean(z.ResolvePath(path))]
	return types.NewBool(ok), nil
}

// nativeMoveUploadedFile déplace un fichier inscrit par RegisterUploadedFile : rename(2), puis copie
// et suppression de la source si le renommage échoue ; les deux chemins se résolvent contre le répertoire
// courant de la Zone. Une source impossible à supprimer après copie annule la copie et rend false. Le
// chemin source est retiré de l'ensemble en cas de succès.
func nativeMoveUploadedFile(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("move_uploaded_file", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	fromArg, err := asStringParam(z, args[0], "move_uploaded_file", "1", "from")
	if err != nil {
		return types.Value{}, err
	}
	toArg, err := asStringParam(z, args[1], "move_uploaded_file", "2", "to")
	if err != nil {
		return types.Value{}, err
	}
	if err := noNullBytes(toArg, "move_uploaded_file", "2", "to"); err != nil {
		return types.Value{}, err
	}
	st := sysStatePeek(z)
	if st == nil {
		return types.NewBool(false), nil
	}
	from := filepath.Clean(z.ResolvePath(fromArg))
	if _, ok := st.uploads[from]; !ok {
		return types.NewBool(false), nil
	}
	to := filepath.Clean(z.ResolvePath(toArg))
	if os.Rename(from, to) != nil {
		if err := moveCopyFallback(z, from, to); err != nil {
			return types.NewBool(false), nil
		}
	}
	delete(st.uploads, from)
	return types.NewBool(true), nil
}

// moveCopyFallback déplace from vers to lorsque rename(2) direct a échoué (typiquement EXDEV).
// 1. Si to est un répertoire, le remplacement est refusé immédiatement (syscall.EISDIR).
// 2. Un fichier temporaire stage est créé dans filepath.Dir(to) (descripteur ouvert en lecture/écriture :
//    sous POSIX, les permissions d'inode s'évaluent à open(2) et un Chmod(mode) ultérieur, même 0000,
//    ne retire jamais le droit de lecture du descripteur déjà ouvert). stage reçoit les octets de from,
//    puis son mode final exact (permissions de to s'il s'agit d'un fichier régulier existant, ou
//    z.CreateMode(0o666) sinon, jamais le 0777 de Lstat sur un symlink) et stage.Sync() sont appliqués
//    et vérifiés AVANT toute suppression de from.
// 3. from est supprimé par os.Remove(from) AVANT la publication de stageName sur to : si le répertoire
//    parent de from n'est pas modifiable (0555), os.Remove(from) échoue, stageName est supprimé et ni
//    from ni to ne sont modifiés.
// 4. stageName est ensuite renommé sur to par os.Rename(stageName, to). Si ce renommage échoue, from
//    est restauré à partir du descripteur stage déjà ouvert (sans réouverture affectée par un mode 0000),
//    et stageName n'est supprimé QUE si la restauration de from a intégralement réussi, garantissant
//    qu'aucun échec ne détruit jamais le dernier exemplaire des octets téléversés.
func moveCopyFallback(z *zone.Zone, from, to string) error {
	lfi, lerr := os.Lstat(to)
	if lerr != nil && !os.IsNotExist(lerr) {
		return lerr
	}
	if lerr == nil && lfi.IsDir() {
		return syscall.EISDIR
	}
	srcFi, err := os.Stat(from)
	if err != nil {
		return err
	}
	srcMode := srcFi.Mode().Perm()
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	stage, err := os.CreateTemp(filepath.Dir(to), ".php127-upload-*")
	if err != nil {
		return errors.Join(err, src.Close())
	}
	stageName := stage.Name()
	mode := z.CreateMode(0o666)
	if lerr == nil && lfi.Mode().IsRegular() {
		mode = lfi.Mode().Perm()
	}
	err = stageCopyFrom(stage, src, mode)
	if err != nil {
		return errors.Join(err, stage.Close(), os.Remove(stageName))
	}
	if err := os.Remove(from); err != nil {
		return errors.Join(err, stage.Close(), os.Remove(stageName))
	}
	return publishOrRestoreStage(stage, stageName, from, to, srcMode)
}

func stageCopyFrom(stage, src *os.File, mode os.FileMode) error {
	_, err := io.Copy(stage, src)
	err = errors.Join(err, src.Close())
	if err == nil {
		err = stage.Chmod(mode)
	}
	if err == nil {
		err = stage.Sync()
	}
	return err
}

func publishOrRestoreStage(stage *os.File, stageName, from, to string, srcMode os.FileMode) error {
	if err := os.Rename(stageName, to); err != nil {
		rerr := restoreFileFromOpenStage(stage, from, srcMode)
		cerr := stage.Close()
		if rerr != nil {
			// La restauration de from a échoué : stageName détient le dernier exemplaire des octets et ne doit pas être supprimé.
			return errors.Join(err, rerr, cerr)
		}
		return errors.Join(err, cerr, os.Remove(stageName))
	}
	return stage.Close()
}

func restoreFileFromOpenStage(stage *os.File, from string, perm os.FileMode) error {
	if _, err := stage.Seek(0, io.SeekStart); err != nil {
		return err
	}
	out, err := os.OpenFile(from, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	return restoreCopyTo(out, stage, from, perm)
}

func restoreCopyTo(out, stage *os.File, from string, perm os.FileMode) error {
	_, cerr := io.Copy(out, stage)
	cerr = errors.Join(cerr, out.Chmod(perm), out.Close())
	if cerr != nil {
		return errors.Join(cerr, os.Remove(from))
	}
	return nil
}

// copyFile copie from vers to. Une destination absente est créée avec 0666 privé du masque de la Zone,
// mode passé au noyau puis rétabli exactement par Chmod quel que soit le masque du processus ; O_EXCL
// désigne sans ambiguïté le créateur. Une destination existante est ouverte et tronquée sans changer ses
// droits, comme par la copie de PHP. Tout échec après ouverture supprime to.
func copyFile(z *zone.Zone, from, to string) error {
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	mode := z.CreateMode(0o666)
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err == nil {
		err = dst.Chmod(mode)
	} else if errors.Is(err, fs.ErrExist) {
		dst, err = os.OpenFile(to, os.O_WRONLY|os.O_TRUNC, 0)
	}
	if dst == nil {
		return err
	}
	if err == nil {
		_, err = io.Copy(dst, src)
	}
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if rerr := os.Remove(to); rerr != nil {
			return errors.Join(err, rerr)
		}
	}
	return err
}

func nativeGethostname(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("gethostname", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	host, err := os.Hostname()
	if err != nil {
		return types.NewBool(false), nil
	}
	return newStr(z, host)
}

// maxFQDNLen est MAXFQDNLEN de PHP : un nom plus long est rendu tel quel sans résolution.
const maxFQDNLen = 255

// nativeGethostbyname rend la première adresse IPv4 du nom, « 127.0.0.1 » pour localhost sans
// résolveur, et le nom inchangé en cas d'échec ; le marquage de l'argument est reporté sur le résultat.
func nativeGethostbyname(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("gethostbyname", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	host, err := asStringParam(z, args[0], "gethostbyname", "1", "hostname")
	if err != nil {
		return types.Value{}, err
	}
	if err := noNullBytes(host, "gethostbyname", "1", "hostname"); err != nil {
		return types.Value{}, err
	}
	res := host
	switch {
	case len(host) > maxFQDNLen:
	case host == "localhost":
		res = "127.0.0.1"
	default:
		if ips, err := net.DefaultResolver.LookupIP(context.Background(), "ip4", host); err == nil && len(ips) > 0 {
			res = ips[0].String()
		}
	}
	out, err := newStr(z, res)
	return withTaint(z, args[0], out, err)
}

// nativeGethostbyaddr rend le nom du premier enregistrement PTR de l'adresse, l'adresse elle-même sans
// enregistrement, false si l'argument n'est ni IPv4 ni IPv6.
func nativeGethostbyaddr(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("gethostbyaddr", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	ip, err := asStringParam(z, args[0], "gethostbyaddr", "1", "ip")
	if err != nil {
		return types.Value{}, err
	}
	if err := noNullBytes(ip, "gethostbyaddr", "1", "ip"); err != nil {
		return types.Value{}, err
	}
	if net.ParseIP(ip) == nil {
		return types.NewBool(false), nil
	}
	res := ip
	if names, err := net.DefaultResolver.LookupAddr(context.Background(), ip); err == nil && len(names) > 0 {
		if name := strings.TrimSuffix(names[0], "."); name != "" {
			res = name
		}
	}
	out, err := newStr(z, res)
	return withTaint(z, args[0], out, err)
}
