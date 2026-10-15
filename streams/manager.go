package streams

import (
	"io"
	"os"
	"path/filepath"
	"sync"

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

// ZoneStreams détient les descripteurs de flux et de répertoires ouverts dans une Zone.
type ZoneStreams struct {
	mu      sync.Mutex
	zone    *zone.Zone
	zoneID  uint32
	nextID  uint32
	streams map[uint32]*streamHandle
	dirs    map[uint32]*dirHandle
	lastDir uint32
	cwd     string
	input   []byte
	stdout  io.Writer
	stderr  io.Writer
	pending int
}

var (
	mgrMu      sync.Mutex
	nextZoneID uint32 = 1
	byZone            = make(map[*zone.Zone]*ZoneStreams)
	byZoneID          = make(map[uint32]*ZoneStreams)
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

func getZoneStreams(z *zone.Zone) *ZoneStreams {
	mgrMu.Lock()
	defer mgrMu.Unlock()
	if zs, ok := byZone[z]; ok {
		return zs
	}
	id := nextZoneID
	nextZoneID++
	wd, _ := os.Getwd()
	zs := &ZoneStreams{
		zone:    z,
		zoneID:  id,
		nextID:  1,
		streams: make(map[uint32]*streamHandle),
		dirs:    make(map[uint32]*dirHandle),
		cwd:     wd,
		stderr:  os.Stderr,
	}
	byZone[z] = zs
	byZoneID[id] = zs
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
	zs.pending++
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
	zs.pending++
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
	h.closed = true
	delete(zs.streams, slotID)
	zs.mu.Unlock()
	_ = h.closeOS()
	return true
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

// DispatchCleanup exécute la fermeture automatique d'une ressource de flux ou de répertoire lors de Zone.Reset.
func DispatchCleanup(item zone.CleanupItem) {
	if item.Kind != zone.CleanupStreamLock {
		return
	}
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
	zs.mu.Lock()
	zs.pending--
	empty := zs.pending <= 0 && len(zs.streams) == 0 && len(zs.dirs) == 0
	zs.mu.Unlock()
	if empty {
		mgrMu.Lock()
		delete(byZone, zs.zone)
		delete(byZoneID, zs.zoneID)
		mgrMu.Unlock()
	}
}

// DispatchZoneCleanup exécute la fermeture automatique d'une ressource de flux pour une Zone.
func DispatchZoneCleanup(_ *zone.Zone, item zone.CleanupItem) {
	DispatchCleanup(item)
}

// CloseAll ferme immédiatement tous les descripteurs ouverts dans la Zone donnée.
func CloseAll(z *zone.Zone) {
	mgrMu.Lock()
	zs := byZone[z]
	if zs != nil {
		delete(byZone, z)
		delete(byZoneID, zs.zoneID)
	}
	mgrMu.Unlock()
	if zs == nil {
		return
	}
	zs.mu.Lock()
	streams := make([]*streamHandle, 0, len(zs.streams))
	for _, h := range zs.streams {
		if !h.closed {
			h.closed = true
			streams = append(streams, h)
		}
	}
	clear(zs.streams)
	clear(zs.dirs)
	zs.lastDir = 0
	zs.mu.Unlock()
	for _, h := range streams {
		_ = h.closeOS()
	}
}

// OpenStreamCount retourne le nombre de flux actuellement ouverts dans la Zone.
func OpenStreamCount(z *zone.Zone) int {
	mgrMu.Lock()
	zs := byZone[z]
	mgrMu.Unlock()
	if zs == nil {
		return 0
	}
	zs.mu.Lock()
	defer zs.mu.Unlock()
	return len(zs.streams)
}

// OpenDirCount retourne le nombre de descripteurs de répertoires ouverts dans la Zone.
func OpenDirCount(z *zone.Zone) int {
	mgrMu.Lock()
	zs := byZone[z]
	mgrMu.Unlock()
	if zs == nil {
		return 0
	}
	zs.mu.Lock()
	defer zs.mu.Unlock()
	return len(zs.dirs)
}
