package streams

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	ResourceKindStream uint16 = 1
	ResourceKindDir    uint16 = 2

	SEEK_SET = 0
	SEEK_CUR = 1
	SEEK_END = 2

	FILE_USE_INCLUDE_PATH = 1
	FILE_IGNORE_NEW_LINES = 2
	FILE_SKIP_EMPTY_LINES = 4
	FILE_APPEND           = 8
	LOCK_SH               = 1
	LOCK_EX               = 2
	LOCK_UN               = 3
	LOCK_NB               = 4

	SCANDIR_SORT_ASCENDING  = 0
	SCANDIR_SORT_DESCENDING = 1
	SCANDIR_SORT_NONE       = 2

	PATHINFO_DIRNAME   = 1
	PATHINFO_BASENAME  = 2
	PATHINFO_EXTENSION = 4
	PATHINFO_FILENAME  = 8
	PATHINFO_ALL       = 15

	GLOB_MARK     = 2
	GLOB_NOSORT   = 4
	GLOB_NOCHECK  = 16
	GLOB_NOESCAPE = 64
	GLOB_BRACE    = 1024
	GLOB_ONLYDIR  = 8192
	GLOB_ERR      = 1
)

// NewResource construit un descripteur types.TypeResource sans pointeur Go.
func NewResource(_ *zone.Zone, kind uint16, slotID uint32) types.Value {
	return types.Value{
		Type:     types.TypeResource,
		Flags:    types.FlagZoneRequest,
		Extra:    kind,
		DataSlot: slotID,
		Scalar:   uint64(slotID),
	}
}

type dirHandle struct {
	slotID  uint32
	path    string
	entries []string
	pos     int
	closed  bool
}

// ZoneStreams détient les descripteurs de flux et de répertoires ouverts dans une Zone, ainsi que
// l'état de requête du système de fichiers (répertoire courant, masque de création, enveloppes déclarées).
// Toutes les chaînes sont des copies Go : aucune ne référence l'arène de la Zone.
type ZoneStreams struct {
	mu      sync.Mutex
	zone    *zone.Zone
	zoneID  uint32
	nextID  uint32
	streams map[uint32]*streamHandle
	// persistent retient, par clé PHP (« pfsockopen__hôte:port », « stream_socket_client__adresse »), les
	// sockets persistantes de la Zone, enregistrées ou non comme flux ; seul le nettoyage de la Zone les ferme.
	persistent map[string]*streamHandle
	dirs       map[uint32]*dirHandle
	lastDir    uint32
	// cwd et umask ne servent que la Zone nil : une Zone réelle porte son répertoire courant et son masque
	// (zone.Zone.Cwd, zone.Zone.Umask), partagés avec stdlib.
	cwd    string
	umask  int
	input  []byte
	stdout io.Writer
	stderr io.Writer
	// userWrappers retient, dans l'ordre d'enregistrement, chaque protocole déclaré par
	// stream_wrapper_register et la classe qui le sert ; opener instancie cette classe.
	userWrappers []userWrapper
	opener       WrapperOpener
}

// cleanupKindZoneStreams est la sorte de l'élément de nettoyage qui libère l'état de flux d'une Zone.
// Enregistré à la création de l'état, il est dépilé après les éléments de chaque flux (pile LIFO).
const cleanupKindZoneStreams zone.CleanupKind = 199

// Le *ZoneStreams de la requête vit dans le slot zone.SlotStreams : la recherche par Zone se fait
// en O(1), sans verrou global ni table indexée par pointeur de Zone.

// defaultUmask est le masque de création initial de l'état de la Zone nil ; une Zone réelle porte le sien.
const defaultUmask = zone.DefaultUmask

// mgrMu protège l'allocation des identifiants d'état, la table byZoneID lue par DispatchCleanup
// (qui ne reçoit que l'identifiant) et l'état de la Zone nil ; la recherche par Zone passe par le slot.
var (
	mgrMu      sync.Mutex
	nextZoneID uint32 = 1
	byZoneID          = make(map[uint32]*ZoneStreams)
	nilZone    *ZoneStreams
	constants  map[string]types.Value
)

func init() {
	constants = map[string]types.Value{
		"SEEK_SET":                types.NewLong(SEEK_SET),
		"SEEK_CUR":                types.NewLong(SEEK_CUR),
		"SEEK_END":                types.NewLong(SEEK_END),
		"FILE_USE_INCLUDE_PATH":   types.NewLong(FILE_USE_INCLUDE_PATH),
		"FILE_IGNORE_NEW_LINES":   types.NewLong(FILE_IGNORE_NEW_LINES),
		"FILE_SKIP_EMPTY_LINES":   types.NewLong(FILE_SKIP_EMPTY_LINES),
		"FILE_APPEND":             types.NewLong(FILE_APPEND),
		"LOCK_SH":                 types.NewLong(LOCK_SH),
		"LOCK_EX":                 types.NewLong(LOCK_EX),
		"LOCK_UN":                 types.NewLong(LOCK_UN),
		"LOCK_NB":                 types.NewLong(LOCK_NB),
		"SCANDIR_SORT_ASCENDING":  types.NewLong(SCANDIR_SORT_ASCENDING),
		"SCANDIR_SORT_DESCENDING": types.NewLong(SCANDIR_SORT_DESCENDING),
		"SCANDIR_SORT_NONE":       types.NewLong(SCANDIR_SORT_NONE),
		"PATHINFO_DIRNAME":        types.NewLong(PATHINFO_DIRNAME),
		"PATHINFO_BASENAME":       types.NewLong(PATHINFO_BASENAME),
		"PATHINFO_EXTENSION":      types.NewLong(PATHINFO_EXTENSION),
		"PATHINFO_FILENAME":       types.NewLong(PATHINFO_FILENAME),
		"PATHINFO_ALL":            types.NewLong(PATHINFO_ALL),
		"GLOB_MARK":               types.NewLong(GLOB_MARK),
		"GLOB_NOSORT":             types.NewLong(GLOB_NOSORT),
		"GLOB_NOCHECK":            types.NewLong(GLOB_NOCHECK),
		"GLOB_NOESCAPE":           types.NewLong(GLOB_NOESCAPE),
		"GLOB_BRACE":              types.NewLong(GLOB_BRACE),
		"GLOB_ONLYDIR":            types.NewLong(GLOB_ONLYDIR),
		"GLOB_ERR":                types.NewLong(GLOB_ERR),
	}
	sep, err := types.InternProcessString(string(filepath.Separator))
	if err == nil {
		constants["DIRECTORY_SEPARATOR"] = sep
	}
	psep, err := types.InternProcessString(string(os.PathListSeparator))
	if err == nil {
		constants["PATH_SEPARATOR"] = psep
	}
}

// Constants retourne la table des constantes PHP de flux et de fichiers.
func Constants() map[string]types.Value {
	return constants
}

// lookupZoneStreams rend l'état de flux de la Zone s'il existe, sans le créer.
func lookupZoneStreams(z *zone.Zone) *ZoneStreams {
	if z != nil {
		zs, _ := z.Slot(zone.SlotStreams).(*ZoneStreams)
		return zs
	}
	mgrMu.Lock()
	defer mgrMu.Unlock()
	return nilZone
}

func getZoneStreams(z *zone.Zone) *ZoneStreams {
	if z != nil {
		if v := z.Slot(zone.SlotStreams); v != nil {
			return v.(*ZoneStreams)
		}
	}
	mgrMu.Lock()
	defer mgrMu.Unlock()
	if z == nil && nilZone != nil {
		return nilZone
	}
	id := nextZoneID
	nextZoneID++
	wd, _ := os.Getwd()
	zs := &ZoneStreams{
		zone:       z,
		zoneID:     id,
		nextID:     1,
		streams:    make(map[uint32]*streamHandle),
		persistent: make(map[string]*streamHandle),
		dirs:       make(map[uint32]*dirHandle),
		cwd:        wd,
		umask:      defaultUmask,
		stderr:     os.Stderr,
	}
	byZoneID[id] = zs
	if z == nil {
		nilZone = zs
		return zs
	}
	z.SetSlot(zone.SlotStreams, zs)
	z.RegisterCleanup(zone.CleanupItem{Kind: cleanupKindZoneStreams, Target: id})
	return zs
}

// SetInput configure le contenu lu par php://input pour la Zone donnée.
func SetInput(z *zone.Zone, data []byte) {
	zs := getZoneStreams(z)
	zs.mu.Lock()
	zs.input = append(zs.input[:0], data...)
	zs.mu.Unlock()
}

// SetOutput configure l'écrivain de php://output et php://stdout pour la Zone donnée.
func SetOutput(z *zone.Zone, w io.Writer) {
	zs := getZoneStreams(z)
	zs.mu.Lock()
	zs.stdout = w
	zs.mu.Unlock()
}

// SetStderr configure l'écrivain de php://stderr pour la Zone donnée.
func SetStderr(z *zone.Zone, w io.Writer) {
	zs := getZoneStreams(z)
	zs.mu.Lock()
	zs.stderr = w
	zs.mu.Unlock()
}

func (zs *ZoneStreams) registerStream(h *streamHandle) types.Value {
	zs.mu.Lock()
	slotID := zs.nextID
	zs.nextID++
	h.slotID = slotID
	zs.streams[slotID] = h
	zs.mu.Unlock()

	if zs.zone != nil {
		zs.zone.RegisterCleanup(zone.CleanupItem{
			Kind:   zone.CleanupStreamLock,
			Target: zs.zoneID,
			Extra:  slotID,
		})
	}
	return NewResource(zs.zone, ResourceKindStream, slotID)
}

func (zs *ZoneStreams) registerDir(d *dirHandle) types.Value {
	zs.mu.Lock()
	slotID := zs.nextID
	zs.nextID++
	d.slotID = slotID
	zs.dirs[slotID] = d
	zs.lastDir = slotID
	zs.mu.Unlock()

	if zs.zone != nil {
		zs.zone.RegisterCleanup(zone.CleanupItem{
			Kind:   zone.CleanupStreamLock,
			Target: zs.zoneID,
			Extra:  slotID | (1 << 31),
		})
	}
	return NewResource(zs.zone, ResourceKindDir, slotID)
}

func (zs *ZoneStreams) lookupStream(v types.Value) (*streamHandle, bool) {
	if v.Type != types.TypeResource || v.Extra != ResourceKindStream {
		return nil, false
	}
	zs.mu.Lock()
	defer zs.mu.Unlock()
	h, ok := zs.streams[v.DataSlot]
	if !ok || h.closed {
		return nil, false
	}
	return h, true
}

func (zs *ZoneStreams) closeStream(slotID uint32) bool {
	zs.mu.Lock()
	h, ok := zs.streams[slotID]
	if !ok || h.closed {
		zs.mu.Unlock()
		return false
	}
	delete(zs.streams, slotID)
	// Une socket persistante quitte les flux de la requête sans être fermée : pfsockopen ou
	// STREAM_CLIENT_PERSISTENT la rendent de nouveau dans la même Zone, closeAll la ferme.
	if h.persistent {
		h.slotID = 0
		zs.mu.Unlock()
		return true
	}
	h.closed = true
	zs.mu.Unlock()
	_ = h.closeOS()
	return true
}

// takePersistent rend la socket persistante de clé pkey si elle est encore utilisable, sinon la ferme et
// l'oublie. Comme PHP_STREAM_OPTION_CHECK_LIVENESS, une socket dont le pair a fermé est écartée avant
// réutilisation. Une socket déjà enregistrée comme flux garde son slot ; sinon slotID vaut zéro.
func (zs *ZoneStreams) takePersistent(pkey string) (*streamHandle, bool) {
	zs.mu.Lock()
	h, ok := zs.persistent[pkey]
	zs.mu.Unlock()
	if !ok {
		return nil, false
	}
	if !h.closed && !h.eof && h.conn != nil && h.alive() {
		return h, true
	}
	zs.mu.Lock()
	if zs.persistent[pkey] == h {
		delete(zs.persistent, pkey)
	}
	if h.slotID != 0 && zs.streams[h.slotID] == h {
		delete(zs.streams, h.slotID)
	}
	stale := !h.closed
	h.closed = true
	zs.mu.Unlock()
	if stale {
		_ = h.closeOS()
	}
	return nil, false
}

// alive sonde une socket sans rien consommer : un octet tamponné ou une attente d'une milliseconde sans
// réponse la disent vivante ; une fin de flux ou une erreur la disent morte et marquent eof.
func (h *streamHandle) alive() bool {
	if h.rd.Buffered() > 0 {
		return true
	}
	_ = h.conn.SetReadDeadline(time.Now().Add(time.Millisecond))
	_, err := h.rd.Peek(1)
	_ = h.conn.SetReadDeadline(time.Time{})
	if err == nil || isTimeout(err) {
		return true
	}
	h.eof = true
	return false
}

// keepPersistent inscrit une socket fraîchement connectée dans la table persistante de la Zone.
func (zs *ZoneStreams) keepPersistent(pkey string, h *streamHandle) {
	h.persistent, h.persistKey = true, pkey
	zs.mu.Lock()
	zs.persistent[pkey] = h
	zs.mu.Unlock()
}

func (zs *ZoneStreams) closeDir(slotID uint32) bool {
	zs.mu.Lock()
	d, ok := zs.dirs[slotID]
	if !ok || d.closed {
		zs.mu.Unlock()
		return false
	}
	d.closed = true
	delete(zs.dirs, slotID)
	if zs.lastDir == slotID {
		zs.lastDir = 0
	}
	zs.mu.Unlock()
	return true
}

// DispatchCleanup exécute la fermeture automatique d'une ressource de flux ou de répertoire lors de Zone.Reset,
// puis, sur l'élément cleanupKindZoneStreams, ferme le reste et retire l'état de flux de la Zone.
func DispatchCleanup(item zone.CleanupItem) {
	switch item.Kind {
	case zone.CleanupStreamLock:
		mgrMu.Lock()
		zs := byZoneID[item.Target]
		mgrMu.Unlock()
		if zs == nil {
			return
		}
		if item.Extra&(1<<31) != 0 {
			zs.closeDir(item.Extra &^ (1 << 31))
		} else {
			zs.closeStream(item.Extra)
		}
	case cleanupKindZoneStreams:
		mgrMu.Lock()
		zs := byZoneID[item.Target]
		if zs != nil {
			delete(byZoneID, item.Target)
		}
		mgrMu.Unlock()
		if zs != nil {
			zs.detach()
			zs.closeAll()
		}
	}
}

// DispatchZoneCleanup exécute la fermeture automatique d'une ressource de flux pour une Zone.
func DispatchZoneCleanup(_ *zone.Zone, item zone.CleanupItem) {
	DispatchCleanup(item)
}

// CloseAll ferme immédiatement tous les descripteurs ouverts dans la Zone donnée et retire son état de flux.
func CloseAll(z *zone.Zone) {
	zs := lookupZoneStreams(z)
	if zs == nil {
		return
	}
	mgrMu.Lock()
	if byZoneID[zs.zoneID] == zs {
		delete(byZoneID, zs.zoneID)
	}
	mgrMu.Unlock()
	zs.detach()
	zs.closeAll()
}

// detach retire l'état de son slot de Zone (ou de l'emplacement de la Zone nil) s'il l'occupe encore.
func (zs *ZoneStreams) detach() {
	if zs.zone == nil {
		mgrMu.Lock()
		if nilZone == zs {
			nilZone = nil
		}
		mgrMu.Unlock()
		return
	}
	if zs.zone.Slot(zone.SlotStreams) == zs {
		zs.zone.SetSlot(zone.SlotStreams, nil)
	}
}

func (zs *ZoneStreams) closeAll() {
	zs.mu.Lock()
	streams := make([]*streamHandle, 0, len(zs.streams))
	for _, h := range zs.streams {
		if !h.closed {
			h.closed = true
			streams = append(streams, h)
		}
	}
	for _, h := range zs.persistent {
		if !h.closed {
			h.closed = true
			streams = append(streams, h)
		}
	}
	for _, d := range zs.dirs {
		d.closed = true
	}
	clear(zs.streams)
	clear(zs.persistent)
	clear(zs.dirs)
	zs.lastDir = 0
	zs.userWrappers = nil
	zs.opener = nil
	zs.mu.Unlock()
	for _, h := range streams {
		_ = h.closeOS()
	}
}

// OpenStreamCount retourne le nombre de flux actuellement ouverts dans la Zone.
func OpenStreamCount(z *zone.Zone) int {
	zs := lookupZoneStreams(z)
	if zs == nil {
		return 0
	}
	zs.mu.Lock()
	defer zs.mu.Unlock()
	return len(zs.streams)
}

// OpenDirCount retourne le nombre de descripteurs de répertoires ouverts dans la Zone.
func OpenDirCount(z *zone.Zone) int {
	zs := lookupZoneStreams(z)
	if zs == nil {
		return 0
	}
	zs.mu.Lock()
	defer zs.mu.Unlock()
	return len(zs.dirs)
}
