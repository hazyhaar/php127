// Package session implémente la gestion des sessions PHP 8.3 ($_SESSION, persistance
// fichier par défaut, sérialiseurs "php" et "php_serialize", SessionHandlerInterface
// et fermeture automatique en fin de requête via Zone.RegisterCleanup).
package session

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// NativeFunc est l'alias de signature compatible avec stdlib.NativeFunc.
type NativeFunc = func(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error)

// Constantes d'état de session de PHP 8.3.
const (
	PHP_SESSION_DISABLED int64 = 0
	PHP_SESSION_NONE     int64 = 1
	PHP_SESSION_ACTIVE   int64 = 2

	CleanupSession zone.CleanupKind = 56
)

// SessionError représente une exception ou erreur d'argument des fonctions de session.
type SessionError struct {
	Class   string
	Message string
}

func (e SessionError) Error() string {
	return e.Class + ": " + e.Message
}

// SessionHandlerInterface modélise le contrat SessionHandlerInterface de PHP 8.3.
type SessionHandlerInterface interface {
	Open(savePath, sessionName string) bool
	Close() bool
	Read(id string) (string, bool)
	Write(id, data string) bool
	Destroy(id string) bool
	GC(maxLifetime int64) (int64, bool)
}

// CookieParams regroupe les paramètres du cookie de session retournés par session_get_cookie_params.
type CookieParams struct {
	Lifetime int64
	Path     string
	Domain   string
	Secure   bool
	HTTPOnly bool
	SameSite string
}

// FileSessionHandler implémente le stockage par défaut dans des fichiers sess_<id>.
type FileSessionHandler struct {
	dir string
}

func (h *FileSessionHandler) Open(savePath, _ string) bool {
	if savePath == "" {
		savePath = os.TempDir()
	}
	h.dir = savePath
	if err := os.MkdirAll(h.dir, 0o700); err != nil {
		return false
	}
	return true
}

func (h *FileSessionHandler) Close() bool {
	return true
}

func isValidSessionID(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == ',' {
			continue
		}
		return false
	}
	return true
}

func (h *FileSessionHandler) filePath(id string) (string, bool) {
	if !isValidSessionID(id) {
		return "", false
	}
	dir := h.dir
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "sess_"+id), true
}

func (h *FileSessionHandler) Read(id string) (string, bool) {
	p, ok := h.filePath(id)
	if !ok {
		return "", false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", true
		}
		return "", false
	}
	return string(data), true
}

func (h *FileSessionHandler) Write(id, data string) bool {
	p, ok := h.filePath(id)
	if !ok {
		return false
	}
	return os.WriteFile(p, []byte(data), 0o600) == nil
}

func (h *FileSessionHandler) Destroy(id string) bool {
	p, ok := h.filePath(id)
	if !ok {
		return false
	}
	err := os.Remove(p)
	return err == nil || os.IsNotExist(err)
}

func (h *FileSessionHandler) GC(maxLifetime int64) (int64, bool) {
	dir := h.dir
	if dir == "" {
		dir = os.TempDir()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, false
	}
	cutoff := time.Now().Add(-time.Duration(maxLifetime) * time.Second)
	var deleted int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "sess_") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if os.Remove(filepath.Join(dir, entry.Name())) == nil {
				deleted++
			}
		}
	}
	return deleted, true
}

// State maintient l'état de la session et le tableau $_SESSION d'une Zone.
type State struct {
	regID            uint32
	z                *zone.Zone
	status           int64
	id               string
	name             string
	savePath         string
	serializeHandler string
	cacheLimiter     string
	cacheExpire      int64
	gcMaxLifetime    int64
	cookie           CookieParams
	handler          SessionHandlerInterface
	sessionArr       types.Value
	originalRaw      string
	cleaned          bool
}

var (
	statesMu    sync.Mutex
	nextStateID uint32
	statesByZ   = make(map[*zone.Zone]*State)
	statesByID  = make(map[uint32]*State)
)

func getOrCreateState(z *zone.Zone) *State {
	if z == nil {
		panic("session: nil zone")
	}
	statesMu.Lock()
	defer statesMu.Unlock()
	if st, ok := statesByZ[z]; ok {
		return st
	}
	nextStateID++
	id := nextStateID
	st := &State{
		regID:            id,
		z:                z,
		status:           PHP_SESSION_NONE,
		name:             "PHPSESSID",
		savePath:         os.TempDir(),
		serializeHandler: "php",
		cacheLimiter:     "nocache",
		cacheExpire:      180,
		gcMaxLifetime:    1440,
		cookie: CookieParams{
			Lifetime: 0,
			Path:     "/",
			Domain:   "",
			Secure:   false,
			HTTPOnly: false,
			SameSite: "",
		},
		handler: &FileSessionHandler{},
	}
	statesByZ[z] = st
	statesByID[id] = st
	z.RegisterCleanup(zone.CleanupItem{
		Kind:   CleanupSession,
		Target: id,
	})
	return st
}

// CleanupDispatcher retourne la fonction de rappel compatible avec z.Reset pour le paquet session.
func CleanupDispatcher(z *zone.Zone) func(zone.CleanupItem) {
	return func(item zone.CleanupItem) {
		DispatchCleanup(z, item)
	}
}

// DispatchCleanup persiste et ferme automatiquement toute session active avant la réinitialisation de la Zone.
func DispatchCleanup(z *zone.Zone, item zone.CleanupItem) {
	switch item.Kind {
	case CleanupSession:
		statesMu.Lock()
		st := statesByID[item.Target]
		statesMu.Unlock()
		if st == nil {
			return
		}
		if z == nil {
			z = st.z
		}
		st.cleanup(z)
		statesMu.Lock()
		delete(statesByID, st.regID)
		if statesByZ[z] == st {
			delete(statesByZ, z)
		}
		statesMu.Unlock()
	case zone.CleanupDestructor:
		if z != nil {
			types.DispatchObjectDestructor(z, item)
		}
	}
}

func (st *State) cleanup(z *zone.Zone) {
	if st.cleaned {
		return
	}
	st.cleaned = true
	if st.status == PHP_SESSION_ACTIVE {
		_ = WriteClose(z)
	}
	if !st.sessionArr.IsUndef() {
		types.ReleaseArray(z, &st.sessionArr)
	}
}

func (st *State) ensureSessionArray(z *zone.Zone) error {
	if st.sessionArr.IsUndef() {
		arr, err := types.NewArray(z)
		if err != nil {
			return err
		}
		st.sessionArr = arr
	}
	return nil
}

// Data retourne le tableau $_SESSION courant de la Zone (en l'initialisant si nécessaire).
func Data(z *zone.Zone) (*types.Value, error) {
	st := getOrCreateState(z)
	if err := st.ensureSessionArray(z); err != nil {
		return nil, err
	}
	return &st.sessionArr, nil
}

// SetVar définit une variable dans $_SESSION[key] = val.
func SetVar(z *zone.Zone, key string, val types.Value) error {
	arr, err := Data(z)
	if err != nil {
		return err
	}
	kv, err := types.NewString(z, key, 0)
	if err != nil {
		return err
	}
	defer types.ReleaseString(z, &kv)
	return types.ArraySet(z, arr, kv, types.Deref(z, val), nil)
}

// GetVar lit une variable dans $_SESSION[key].
func GetVar(z *zone.Zone, key string) (types.Value, bool, error) {
	arr, err := Data(z)
	if err != nil {
		return types.NewNull(), false, err
	}
	kv, err := types.NewString(z, key, 0)
	if err != nil {
		return types.NewNull(), false, err
	}
	defer types.ReleaseString(z, &kv)
	return types.ArrayGet(z, *arr, kv, nil)
}

// SetSaveHandler remplace le gestionnaire de persistance de session pour la Zone.
func SetSaveHandler(z *zone.Zone, h SessionHandlerInterface) bool {
	st := getOrCreateState(z)
	if st.status == PHP_SESSION_ACTIVE || h == nil {
		return false
	}
	st.handler = h
	return true
}

// SetSerializeHandler définit le format de sérialisation ("php" ou "php_serialize").
func SetSerializeHandler(z *zone.Zone, name string) bool {
	if name != "php" && name != "php_serialize" {
		return false
	}
	st := getOrCreateState(z)
	st.serializeHandler = name
	return true
}

func generateRandomSessionID(prefix string) (string, error) {
	if prefix != "" && !isValidSessionID(prefix) {
		return "", SessionError{
			Class:   "ValueError",
			Message: "session_create_id(): Argument #1 ($prefix) cannot contain special characters",
		}
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buf[:]), nil
}

// Start implémente session_start(array $options = []): bool.
func Start(z *zone.Zone, options types.Value) (bool, error) {
	st := getOrCreateState(z)
	if st.status == PHP_SESSION_ACTIVE {
		return false, nil
	}
	readAndClose := false
	if !options.IsUndef() && !options.IsNull() {
		options = types.Deref(z, options)
		if options.Type != types.TypeArray {
			return false, SessionError{Class: "TypeError", Message: "session_start(): Argument #1 ($options) must be of type array"}
		}
		types.ArrayForEach(z, options, func(k, v types.Value) bool {
			key := keyToSessionName(z, k)
			v = types.Deref(z, v)
			switch key {
			case "name":
				if v.Type == types.TypeString {
					st.name = strings.Clone(types.StringData(z, v))
				}
			case "save_path":
				if v.Type == types.TypeString {
					st.savePath = strings.Clone(types.StringData(z, v))
				}
			case "serialize_handler":
				if v.Type == types.TypeString {
					_ = SetSerializeHandler(z, strings.Clone(types.StringData(z, v)))
				}
			case "cache_limiter":
				if v.Type == types.TypeString {
					st.cacheLimiter = strings.Clone(types.StringData(z, v))
				}
			case "cache_expire":
				st.cacheExpire = v.ToLong()
			case "gc_maxlifetime":
				st.gcMaxLifetime = v.ToLong()
			case "cookie_lifetime":
				st.cookie.Lifetime = v.ToLong()
			case "cookie_path":
				if v.Type == types.TypeString {
					st.cookie.Path = strings.Clone(types.StringData(z, v))
				}
			case "cookie_domain":
				if v.Type == types.TypeString {
					st.cookie.Domain = strings.Clone(types.StringData(z, v))
				}
			case "cookie_secure":
				st.cookie.Secure = types.ToBoolZ(z, v)
			case "cookie_httponly":
				st.cookie.HTTPOnly = types.ToBoolZ(z, v)
			case "cookie_samesite":
				if v.Type == types.TypeString {
					st.cookie.SameSite = strings.Clone(types.StringData(z, v))
				}
			case "read_and_close":
				readAndClose = types.ToBoolZ(z, v)
			}
			return true
		})
	}

	if st.id == "" {
		id, err := generateRandomSessionID("")
		if err != nil {
			return false, err
		}
		st.id = id
	}
	if !st.handler.Open(st.savePath, st.name) {
		return false, nil
	}
	raw, ok := st.handler.Read(st.id)
	if !ok {
		_ = st.handler.Close()
		return false, nil
	}
	st.originalRaw = raw
	if !st.sessionArr.IsUndef() {
		types.ReleaseArray(z, &st.sessionArr)
	}
	if err := st.ensureSessionArray(z); err != nil {
		_ = st.handler.Close()
		return false, err
	}
	if raw != "" {
		_ = DecodeSessionData(z, &st.sessionArr, raw, st.serializeHandler)
	}
	st.status = PHP_SESSION_ACTIVE
	if readAndClose {
		st.status = PHP_SESSION_NONE
		_ = st.handler.Close()
	}
	return true, nil
}

// WriteClose implémente session_write_close() / session_commit() : bool.
func WriteClose(z *zone.Zone) bool {
	st := getOrCreateState(z)
	if st.status != PHP_SESSION_ACTIVE {
		return false
	}
	var encoded string
	if !st.sessionArr.IsUndef() {
		var err error
		encoded, err = EncodeSessionData(z, st.sessionArr, st.serializeHandler)
		if err != nil {
			st.status = PHP_SESSION_NONE
			_ = st.handler.Close()
			return false
		}
	}
	wOK := st.handler.Write(st.id, encoded)
	cOK := st.handler.Close()
	st.status = PHP_SESSION_NONE
	st.originalRaw = encoded
	return wOK && cOK
}

// Abort implémente session_abort(): bool (ferme la session sans persister les modifications).
func Abort(z *zone.Zone) bool {
	st := getOrCreateState(z)
	if st.status != PHP_SESSION_ACTIVE {
		return false
	}
	_ = st.handler.Close()
	st.status = PHP_SESSION_NONE
	return true
}

// Reset implémente session_reset(): bool (recharge $_SESSION depuis la valeur initiale lue à l'ouverture).
func Reset(z *zone.Zone) bool {
	st := getOrCreateState(z)
	if st.status != PHP_SESSION_ACTIVE {
		return false
	}
	if !st.sessionArr.IsUndef() {
		types.ReleaseArray(z, &st.sessionArr)
	}
	if err := st.ensureSessionArray(z); err != nil {
		return false
	}
	if st.originalRaw != "" {
		if err := DecodeSessionData(z, &st.sessionArr, st.originalRaw, st.serializeHandler); err != nil {
			return false
		}
	}
	return true
}

// Destroy implémente session_destroy(): bool.
func Destroy(z *zone.Zone) bool {
	st := getOrCreateState(z)
	if st.status != PHP_SESSION_ACTIVE {
		return false
	}
	dOK := st.handler.Destroy(st.id)
	cOK := st.handler.Close()
	st.status = PHP_SESSION_NONE
	st.id = ""
	st.originalRaw = ""
	return dOK && cOK
}

// Unset implémente session_unset(): bool.
func Unset(z *zone.Zone) bool {
	st := getOrCreateState(z)
	if st.status != PHP_SESSION_ACTIVE {
		return false
	}
	if !st.sessionArr.IsUndef() {
		types.ReleaseArray(z, &st.sessionArr)
	}
	return st.ensureSessionArray(z) == nil
}

// Status implémente session_status(): int.
func Status(z *zone.Zone) int64 {
	return getOrCreateState(z).status
}

// Encode implémente session_encode(): string|false.
func Encode(z *zone.Zone) (string, bool) {
	st := getOrCreateState(z)
	if st.status != PHP_SESSION_ACTIVE || st.sessionArr.IsUndef() {
		return "", false
	}
	s, err := EncodeSessionData(z, st.sessionArr, st.serializeHandler)
	if err != nil {
		return "", false
	}
	return s, true
}

// Decode implémente session_decode(string $data): bool.
func Decode(z *zone.Zone, data string) bool {
	st := getOrCreateState(z)
	if st.status != PHP_SESSION_ACTIVE {
		return false
	}
	if !st.sessionArr.IsUndef() {
		types.ReleaseArray(z, &st.sessionArr)
	}
	if err := st.ensureSessionArray(z); err != nil {
		return false
	}
	return DecodeSessionData(z, &st.sessionArr, data, st.serializeHandler) == nil
}

// Registre de fonctions natives session_* :

type nativeEntry struct {
	name string
	fn   NativeFunc
}

var natives = [...]nativeEntry{
	{"session_abort", nativeSessionAbort},
	{"session_cache_expire", nativeSessionCacheExpire},
	{"session_cache_limiter", nativeSessionCacheLimiter},
	{"session_commit", nativeSessionWriteClose},
	{"session_create_id", nativeSessionCreateID},
	{"session_decode", nativeSessionDecode},
	{"session_destroy", nativeSessionDestroy},
	{"session_encode", nativeSessionEncode},
	{"session_gc", nativeSessionGC},
	{"session_get_cookie_params", nativeSessionGetCookieParams},
	{"session_id", nativeSessionID},
	{"session_name", nativeSessionName},
	{"session_regenerate_id", nativeSessionRegenerateID},
	{"session_reset", nativeSessionReset},
	{"session_save_path", nativeSessionSavePath},
	{"session_set_cookie_params", nativeSessionSetCookieParams},
	{"session_start", nativeSessionStart},
	{"session_status", nativeSessionStatus},
	{"session_unset", nativeSessionUnset},
	{"session_write_close", nativeSessionWriteClose},
}

func init() {
	for i := 1; i < len(natives); i++ {
		if natives[i-1].name >= natives[i].name {
			panic("session: table natives non triée à l'entrée " + natives[i].name)
		}
	}
}

// Lookup résout une fonction procédurale de session par recherche dichotomique insensible à la casse ASCII.
func Lookup(name string) (NativeFunc, bool) {
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

func nativeSessionStart(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 1 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_start() expects at most 1 argument"}
	}
	var opts types.Value
	if len(args) == 1 {
		opts = args[0]
	}
	ok, err := Start(z, opts)
	if err != nil {
		return types.NewNull(), err
	}
	return types.NewBool(ok), nil
}

func nativeSessionWriteClose(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 0 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_write_close() expects exactly 0 arguments"}
	}
	return types.NewBool(WriteClose(z)), nil
}

func nativeSessionAbort(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 0 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_abort() expects exactly 0 arguments"}
	}
	return types.NewBool(Abort(z)), nil
}

func nativeSessionReset(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 0 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_reset() expects exactly 0 arguments"}
	}
	return types.NewBool(Reset(z)), nil
}

func nativeSessionDestroy(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 0 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_destroy() expects exactly 0 arguments"}
	}
	return types.NewBool(Destroy(z)), nil
}

func nativeSessionUnset(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 0 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_unset() expects exactly 0 arguments"}
	}
	return types.NewBool(Unset(z)), nil
}

func nativeSessionStatus(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 0 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_status() expects exactly 0 arguments"}
	}
	return types.NewLong(Status(z)), nil
}

func nativeSessionID(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 1 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_id() expects at most 1 argument"}
	}
	st := getOrCreateState(z)
	old := st.id
	if len(args) == 1 && !types.Deref(z, args[0]).IsNull() && !types.Deref(z, args[0]).IsUndef() {
		if st.status == PHP_SESSION_ACTIVE {
			return types.NewBool(false), nil
		}
		v := types.Deref(z, args[0])
		if v.Type != types.TypeString {
			return types.NewNull(), SessionError{Class: "TypeError", Message: "session_id(): Argument #1 ($id) must be of type ?string"}
		}
		st.id = strings.Clone(types.StringData(z, v))
	}
	return types.NewString(z, old, 0)
}

func nativeSessionCreateID(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 1 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_create_id() expects at most 1 argument"}
	}
	prefix := ""
	if len(args) == 1 && !types.Deref(z, args[0]).IsNull() {
		v := types.Deref(z, args[0])
		if v.Type != types.TypeString {
			return types.NewNull(), SessionError{Class: "TypeError", Message: "session_create_id(): Argument #1 ($prefix) must be of type string"}
		}
		prefix = strings.Clone(types.StringData(z, v))
	}
	id, err := generateRandomSessionID(prefix)
	if err != nil {
		return types.NewNull(), err
	}
	return types.NewString(z, id, 0)
}

func nativeSessionRegenerateID(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 1 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_regenerate_id() expects at most 1 argument"}
	}
	st := getOrCreateState(z)
	if st.status != PHP_SESSION_ACTIVE {
		return types.NewBool(false), nil
	}
	deleteOld := false
	if len(args) == 1 {
		deleteOld = types.ToBoolZ(z, args[0])
	}
	if deleteOld && st.id != "" {
		_ = st.handler.Destroy(st.id)
	}
	newID, err := generateRandomSessionID("")
	if err != nil {
		return types.NewBool(false), nil
	}
	st.id = newID
	return types.NewBool(true), nil
}

func nativeSessionName(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 1 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_name() expects at most 1 argument"}
	}
	st := getOrCreateState(z)
	old := st.name
	if len(args) == 1 && !types.Deref(z, args[0]).IsNull() && !types.Deref(z, args[0]).IsUndef() {
		if st.status == PHP_SESSION_ACTIVE {
			return types.NewBool(false), nil
		}
		v := types.Deref(z, args[0])
		if v.Type != types.TypeString {
			return types.NewNull(), SessionError{Class: "TypeError", Message: "session_name(): Argument #1 ($name) must be of type ?string"}
		}
		st.name = strings.Clone(types.StringData(z, v))
	}
	return types.NewString(z, old, 0)
}

func nativeSessionSavePath(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 1 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_save_path() expects at most 1 argument"}
	}
	st := getOrCreateState(z)
	old := st.savePath
	if len(args) == 1 && !types.Deref(z, args[0]).IsNull() && !types.Deref(z, args[0]).IsUndef() {
		if st.status == PHP_SESSION_ACTIVE {
			return types.NewBool(false), nil
		}
		v := types.Deref(z, args[0])
		if v.Type != types.TypeString {
			return types.NewNull(), SessionError{Class: "TypeError", Message: "session_save_path(): Argument #1 ($path) must be of type ?string"}
		}
		st.savePath = strings.Clone(types.StringData(z, v))
	}
	return types.NewString(z, old, 0)
}

func nativeSessionEncode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 0 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_encode() expects exactly 0 arguments"}
	}
	s, ok := Encode(z)
	if !ok {
		return types.NewBool(false), nil
	}
	return types.NewString(z, s, 0)
}

func nativeSessionDecode(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 1 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_decode() expects exactly 1 argument"}
	}
	v := types.Deref(z, args[0])
	if v.Type != types.TypeString {
		return types.NewNull(), SessionError{Class: "TypeError", Message: "session_decode(): Argument #1 ($data) must be of type string"}
	}
	return types.NewBool(Decode(z, types.StringData(z, v))), nil
}

func nativeSessionGetCookieParams(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 0 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_get_cookie_params() expects exactly 0 arguments"}
	}
	st := getOrCreateState(z)
	arr, err := types.NewArray(z)
	if err != nil {
		return types.NewNull(), err
	}
	setKV := func(k string, v types.Value) error {
		kv, err := types.NewString(z, k, 0)
		if err != nil {
			return err
		}
		defer types.ReleaseString(z, &kv)
		return types.ArraySet(z, &arr, kv, v, nil)
	}
	sPath, err := types.NewString(z, st.cookie.Path, 0)
	if err != nil {
		types.ReleaseArray(z, &arr)
		return types.NewNull(), err
	}
	defer types.ReleaseString(z, &sPath)
	sDom, err := types.NewString(z, st.cookie.Domain, 0)
	if err != nil {
		types.ReleaseArray(z, &arr)
		return types.NewNull(), err
	}
	defer types.ReleaseString(z, &sDom)
	sSame, err := types.NewString(z, st.cookie.SameSite, 0)
	if err != nil {
		types.ReleaseArray(z, &arr)
		return types.NewNull(), err
	}
	defer types.ReleaseString(z, &sSame)

	for _, op := range []struct {
		k string
		v types.Value
	}{
		{"lifetime", types.NewLong(st.cookie.Lifetime)},
		{"path", sPath},
		{"domain", sDom},
		{"secure", types.NewBool(st.cookie.Secure)},
		{"httponly", types.NewBool(st.cookie.HTTPOnly)},
		{"samesite", sSame},
	} {
		if err := setKV(op.k, op.v); err != nil {
			types.ReleaseArray(z, &arr)
			return types.NewNull(), err
		}
	}
	return arr, nil
}

func nativeSessionSetCookieParams(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) < 1 || len(args) > 5 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_set_cookie_params() expects between 1 and 5 arguments"}
	}
	st := getOrCreateState(z)
	if st.status == PHP_SESSION_ACTIVE {
		return types.NewBool(false), nil
	}
	a0 := types.Deref(z, args[0])
	if a0.Type == types.TypeArray {
		if len(args) > 1 {
			return types.NewNull(), SessionError{Class: "ValueError", Message: "session_set_cookie_params(): Cannot pass arguments after the options array"}
		}
		types.ArrayForEach(z, a0, func(k, v types.Value) bool {
			key := keyToSessionName(z, k)
			v = types.Deref(z, v)
			switch key {
			case "lifetime":
				st.cookie.Lifetime = v.ToLong()
			case "path":
				if v.Type == types.TypeString {
					st.cookie.Path = strings.Clone(types.StringData(z, v))
				}
			case "domain":
				if v.Type == types.TypeString {
					st.cookie.Domain = strings.Clone(types.StringData(z, v))
				}
			case "secure":
				st.cookie.Secure = types.ToBoolZ(z, v)
			case "httponly":
				st.cookie.HTTPOnly = types.ToBoolZ(z, v)
			case "samesite":
				if v.Type == types.TypeString {
					st.cookie.SameSite = strings.Clone(types.StringData(z, v))
				}
			}
			return true
		})
		return types.NewBool(true), nil
	}
	st.cookie.Lifetime = a0.ToLong()
	if len(args) >= 2 && !types.Deref(z, args[1]).IsNull() {
		v := types.Deref(z, args[1])
		if v.Type == types.TypeString {
			st.cookie.Path = strings.Clone(types.StringData(z, v))
		}
	}
	if len(args) >= 3 && !types.Deref(z, args[2]).IsNull() {
		v := types.Deref(z, args[2])
		if v.Type == types.TypeString {
			st.cookie.Domain = strings.Clone(types.StringData(z, v))
		}
	}
	if len(args) >= 4 && !types.Deref(z, args[3]).IsNull() {
		st.cookie.Secure = types.ToBoolZ(z, args[3])
	}
	if len(args) == 5 && !types.Deref(z, args[4]).IsNull() {
		st.cookie.HTTPOnly = types.ToBoolZ(z, args[4])
	}
	return types.NewBool(true), nil
}

func nativeSessionCacheLimiter(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 1 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_cache_limiter() expects at most 1 argument"}
	}
	st := getOrCreateState(z)
	old := st.cacheLimiter
	if len(args) == 1 && !types.Deref(z, args[0]).IsNull() {
		if st.status == PHP_SESSION_ACTIVE {
			return types.NewBool(false), nil
		}
		v := types.Deref(z, args[0])
		if v.Type != types.TypeString {
			return types.NewNull(), SessionError{Class: "TypeError", Message: "session_cache_limiter(): Argument #1 ($value) must be of type ?string"}
		}
		st.cacheLimiter = strings.Clone(types.StringData(z, v))
	}
	return types.NewString(z, old, 0)
}

func nativeSessionCacheExpire(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) > 1 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_cache_expire() expects at most 1 argument"}
	}
	st := getOrCreateState(z)
	old := st.cacheExpire
	if len(args) == 1 && !types.Deref(z, args[0]).IsNull() {
		if st.status == PHP_SESSION_ACTIVE {
			return types.NewBool(false), nil
		}
		st.cacheExpire = types.Deref(z, args[0]).ToLong()
	}
	return types.NewLong(old), nil
}

func nativeSessionGC(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if len(args) != 0 {
		return types.NewNull(), SessionError{Class: "ArgumentCountError", Message: "session_gc() expects exactly 0 arguments"}
	}
	st := getOrCreateState(z)
	if st.status != PHP_SESSION_ACTIVE {
		return types.NewBool(false), nil
	}
	deleted, ok := st.handler.GC(st.gcMaxLifetime)
	if !ok {
		return types.NewBool(false), nil
	}
	return types.NewLong(deleted), nil
}
