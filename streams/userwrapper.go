package streams

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"syscall"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// userWrapper associe un protocole déclaré par stream_wrapper_register à la classe qui le sert.
// Les deux chaînes sont des copies Go, jamais des vues sur l'arène de la Zone.
type userWrapper struct {
	proto string
	class string
}

// WrapperStream est le flux servi par une instance de classe d'enveloppe utilisateur : Read relaie
// stream_read, Write relaie stream_write, Close relaie stream_close.
type WrapperStream interface {
	io.Reader
	io.Writer
	io.Closer
}

// WrapperOpener instancie la classe enregistrée pour un protocole utilisateur et appelle son
// stream_open(uri, mode) ; context est la ressource de contexte transmise à fopen, ou null.
// Le moteur d'exécution, seul à savoir instancier une classe PHP, le branche par SetWrapperOpener.
type WrapperOpener func(z *zone.Zone, class, uri, mode string, context types.Value) (WrapperStream, error)

// SetWrapperOpener branche, pour la Zone donnée, le gestionnaire d'instance des enveloppes utilisateur.
// L'état est retiré avec le reste de l'état de flux au Reset de la Zone.
func SetWrapperOpener(z *zone.Zone, fn WrapperOpener) {
	zs := getZoneStreams(z)
	zs.mu.Lock()
	zs.opener = fn
	zs.mu.Unlock()
}

// UserWrapperClass rend la classe enregistrée pour proto dans la Zone donnée.
func UserWrapperClass(z *zone.Zone, proto string) (string, bool) {
	zs := lookupZoneStreams(z)
	if zs == nil {
		return "", false
	}
	return zs.UserWrapperClass(proto)
}

// UserWrapperClass rend la classe enregistrée pour proto ; la casse du protocole est indifférente.
func (zs *ZoneStreams) UserWrapperClass(proto string) (string, bool) {
	zs.mu.Lock()
	defer zs.mu.Unlock()
	w, ok := zs.findUserWrapperLocked(proto)
	return w.class, ok
}

func (zs *ZoneStreams) findUserWrapperLocked(proto string) (userWrapper, bool) {
	for _, w := range zs.userWrappers {
		if strings.EqualFold(w.proto, proto) {
			return w, true
		}
	}
	return userWrapper{}, false
}

// userWrapperLocked rend l'enveloppe utilisateur désignée par le schéma « proto:// » de uri.
func (zs *ZoneStreams) userWrapperLocked(uri string) (userWrapper, bool) {
	proto, _, found := strings.Cut(uri, "://")
	if !found || !validProtocol(proto) || len(zs.userWrappers) == 0 {
		return userWrapper{}, false
	}
	return zs.findUserWrapperLocked(proto)
}

func (zs *ZoneStreams) isUserScheme(uri string) bool {
	zs.mu.Lock()
	defer zs.mu.Unlock()
	_, ok := zs.userWrapperLocked(uri)
	return ok
}

// ErrWrapperSilenced indique que l'ouverture du protocole utilisateur a échoué sous silence (@).
var ErrWrapperSilenced = errors.New("wrapper open silenced")

// wrapperError est le refus d'ouverture d'une URI de protocole utilisateur ; il porte le texte de
// l'avertissement « Failed to open stream » émis par la fonction appelante.
type wrapperError struct{ msg string }

func (e wrapperError) Error() string { return e.msg }

// openUserStream ouvre uri par la classe enregistrée pour son protocole. user est faux si uri ne
// relève d'aucun protocole utilisateur ; sinon le flux est servi par le gestionnaire d'instance, ou
// refusé s'il n'y en a pas : jamais de repli sur le système de fichiers local.
func (zs *ZoneStreams) openUserStream(uri, mode string, readable, writable, appendMode bool, ctxID uint32) (*streamHandle, bool, error) {
	zs.mu.Lock()
	w, ok := zs.userWrapperLocked(uri)
	opener := zs.opener
	var ctx types.Value
	if ok && opener != nil {
		if ctxID != 0 {
			if c, found := zs.streams[ctxID]; found && c.kind == backendContext && !c.closed {
				ctx = NewResource(zs.zone, ResourceKindContext, ctxID)
			}
		}
		if ctx.IsUndef() {
			if opts, err := types.NewArray(zs.zone); err == nil {
				id := zs.nextID
				zs.nextID++
				zs.streams[id] = &streamHandle{kind: backendContext, slotID: id, options: opts}
				ctx = NewResource(zs.zone, ResourceKindContext, id)
			}
		}
	}
	zs.mu.Unlock()
	if !ok {
		return nil, false, nil
	}
	if opener == nil {
		return nil, true, wrapperError{`user wrapper class "` + w.class + `" registered for ` + w.proto + `:// requires an instance handler, none is attached`}
	}
	ws, err := opener(zs.zone, w.class, uri, mode, ctx)
	if errors.Is(err, ErrWrapperSilenced) {
		return nil, true, err
	}
	if err != nil || ws == nil {
		msg := `"` + w.class + `::stream_open" call failed`
		if err != nil {
			msg += ": " + err.Error()
		}
		return nil, true, wrapperError{msg}
	}
	h := &streamHandle{
		kind:        backendUser,
		uri:         uri,
		mode:        mode,
		wrapperType: "user-space",
		streamType:  "user-space",
		readable:    readable,
		writable:    writable,
		appendMode:  appendMode,
		closers:     []io.Closer{ws},
	}
	if readable {
		h.src = bufio.NewReaderSize(ws, 8192)
	}
	if writable {
		h.sink = ws
	}
	if seeker, ok := ws.(io.Seeker); ok {
		h.seekable = true
		h.seekFn = func(offset int64, whence int) int {
			var w int
			switch whence {
			case SEEK_SET:
				w = io.SeekStart
			case SEEK_CUR:
				w = io.SeekCurrent
				if h.src != nil {
					offset -= int64(h.src.Buffered())
				}
			case SEEK_END:
				w = io.SeekEnd
			default:
				return -1
			}
			pos, err := seeker.Seek(offset, w)
			if err != nil {
				return -1
			}
			if h.src != nil {
				h.src.Reset(ws)
			}
			h.pos = pos
			return 0
		}
	}
	return h, true, nil
}

// warn écrit un avertissement PHP sur la sortie de la requête, au format des autres natives du paquet.
func warn(out io.Writer, fn, msg string) {
	if out != nil {
		_, _ = io.WriteString(out, "\nWarning: "+fn+": "+msg+"\n")
	}
}

// warnOpenFailure signale le refus d'une URI de protocole utilisateur ; les autres échecs d'ouverture
// gardent leur comportement antérieur (false sans avertissement).
func warnOpenFailure(out io.Writer, fn, uri string, err error) {
	if we, ok := errors.AsType[wrapperError](err); ok {
		warn(out, fn+"("+uri+")", "Failed to open stream: "+we.msg)
	}
}

// errnoText rend le message d'une erreur système avec une majuscule initiale, comme strerror dans PHP.
func errnoText(err error) string {
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		err = errno
	}
	msg := err.Error()
	if msg != "" {
		msg = strings.ToUpper(msg[:1]) + msg[1:]
	}
	return msg
}
