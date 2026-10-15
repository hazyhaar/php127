package streams

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

type backendKind uint8

const (
	backendFile backendKind = iota
	backendMemory
	backendTemp
	backendOutput
	backendData
	backendSocket
	backendContext
	backendGzip
	backendUser
)

// seekEndUnsupported est rendu par un seekFn qui refuse SEEK_END : fseek et gzseek émettent alors
// l'avertissement de PHP et rendent -1.
const seekEndUnsupported = -2

type streamHandle struct {
	slotID      uint32
	kind        backendKind
	uri         string
	mode        string
	wrapperType string
	streamType  string
	mediatype   string
	isBase64    bool
	readable    bool
	writable    bool
	appendMode  bool
	seekable    bool
	eof         bool
	closed      bool

	file      *os.File
	tempSpill string
	maxMem    int64
	buf       []byte
	pos       int64
	writer    io.Writer

	conn     net.Conn
	rd       *bufio.Reader
	timeout  time.Duration
	timedOut bool
	// nonBlocking, posé par stream_set_blocking($s, false) sur une socket, borne chaque lecture à une
	// attente d'une milliseconde : la lecture rend ce qui est disponible, sans marquer timed_out.
	nonBlocking bool
	options     types.Value
	// notification est le paramètre « notification » posé par stream_context_set_params ; seule
	// clé conservée hors « options », comme PHP 8.3 ; une chaîne y est copiée hors de l'arène.
	notification contextParam

	// ctxID est le slot du contexte attaché au flux par fopen ou stream_socket_client ; zéro sans contexte.
	ctxID uint32
	// host est l'hôte de connexion d'une socket, nom de pair TLS par défaut ; tlsOn marque le chiffrement actif.
	host  string
	tlsOn bool
	// persistent marque une socket de pfsockopen ou de STREAM_CLIENT_PERSISTENT : fclose la retire des flux
	// de la Zone sans la fermer, et persistKey la retrouve dans ZoneStreams.persistent jusqu'au Reset.
	persistent bool
	persistKey string

	// src et sink servent les flux dont les octets passent par un décodeur ou par une instance utilisateur
	// (gzip, enveloppe utilisateur) ; closers se ferment dans l'ordre, seekFn remplace le déplacement générique.
	src     *bufio.Reader
	sink    io.Writer
	closers []io.Closer
	seekFn  func(offset int64, whence int) int
}

// contextParam retient un paramètre de contexte : chaîne copiée hors de l'arène, sinon valeur retenue.
type contextParam struct {
	set   bool
	isStr bool
	str   string
	val   types.Value
}

func (p *contextParam) release(z *zone.Zone) {
	if p.set && !p.isStr {
		types.ReleaseValue(z, &p.val)
	}
	*p = contextParam{}
}

func (p *contextParam) value(z *zone.Zone) (types.Value, error) {
	if p.isStr {
		return newStr(z, p.str)
	}
	return types.RetainValue(z, p.val), nil
}

func (h *streamHandle) closeOS() error {
	var err error
	for _, c := range h.closers {
		if cerr := c.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	h.closers, h.src, h.sink, h.seekFn = nil, nil, nil, nil
	if h.conn != nil {
		err = h.conn.Close()
		h.conn = nil
	}
	if h.file != nil {
		err = h.file.Close()
		h.file = nil
	}
	if h.tempSpill != "" {
		_ = os.Remove(h.tempSpill)
		h.tempSpill = ""
	}
	h.buf = nil
	return err
}

func parseOpenMode(mode string) (flag int, readable, writable, appendMode, ok bool) {
	clean := strings.Map(func(r rune) rune {
		if r == 'b' || r == 't' || r == 'e' {
			return -1
		}
		return r
	}, mode)
	switch clean {
	case "r":
		return os.O_RDONLY, true, false, false, true
	case "r+":
		return os.O_RDWR, true, true, false, true
	case "w":
		return os.O_WRONLY | os.O_CREATE | os.O_TRUNC, false, true, false, true
	case "w+":
		return os.O_RDWR | os.O_CREATE | os.O_TRUNC, true, true, false, true
	case "a":
		return os.O_WRONLY | os.O_CREATE | os.O_APPEND, false, true, true, true
	case "a+":
		return os.O_RDWR | os.O_CREATE | os.O_APPEND, true, true, true, true
	case "x":
		return os.O_WRONLY | os.O_CREATE | os.O_EXCL, false, true, false, true
	case "x+":
		return os.O_RDWR | os.O_CREATE | os.O_EXCL, true, true, false, true
	case "c":
		return os.O_WRONLY | os.O_CREATE, false, true, false, true
	case "c+":
		return os.O_RDWR | os.O_CREATE, true, true, false, true
	default:
		return 0, false, false, false, false
	}
}

func (zs *ZoneStreams) resolvePath(p string) string {
	if p == "" {
		return ""
	}
	if after, ok := strings.CutPrefix(p, "file://"); ok {
		if after2, ok2 := strings.CutPrefix(after, "localhost/"); ok2 {
			p = "/" + after2
		} else {
			p = after
		}
	}
	cwd := zs.currentDir()
	zs.mu.Lock()
	_, user := zs.userWrapperLocked(p)
	zs.mu.Unlock()
	// Un protocole utilisateur n'est jamais un chemin local : le chemin vide fait échouer toute
	// opération du système de fichiers au lieu d'un repli silencieux sur cwd/proto:/....
	if user {
		return ""
	}
	if filepath.IsAbs(p) {
		return p
	}
	if cwd == "" {
		return p
	}
	return filepath.Join(cwd, p)
}

func parseDataURI(uri string) (data []byte, mediatype string, isBase64 bool, ok bool) {
	var rest string
	if after, found := strings.CutPrefix(uri, "data://"); found {
		rest = after
	} else if after, found := strings.CutPrefix(uri, "data:"); found {
		rest = after
	} else {
		return nil, "", false, false
	}
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return nil, "", false, false
	}
	meta := rest[:comma]
	payload := rest[comma+1:]
	mediatype = "text/plain"
	if meta != "" {
		parts := strings.Split(meta, ";")
		var params []string
		for i, p := range parts {
			if i == 0 && strings.Contains(p, "/") {
				mediatype = p
				continue
			}
			if p == "base64" {
				isBase64 = true
				continue
			}
			if p != "" {
				params = append(params, p)
			}
		}
		if len(params) > 0 {
			mediatype += ";" + strings.Join(params, ";")
		}
	}
	if isBase64 {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(payload, "="))
			if err != nil {
				return nil, "", false, false
			}
		}
		return decoded, mediatype, true, true
	}
	unescaped, err := url.QueryUnescape(payload)
	if err != nil {
		unescaped = payload
	}
	return []byte(unescaped), mediatype, false, true
}

// openStream ouvre un flux ; le descripteur, enregistré au-delà de l'appel natif, conserve des copies de
// filename et mode, jamais des vues sur l'arène de la Zone.
// ctxID désigne le contexte transmis à une enveloppe utilisateur ; zéro sans contexte.
func openStream(zs *ZoneStreams, out io.Writer, filename, mode string, ctxID uint32) (*streamHandle, error) {
	filename, mode = strings.Clone(filename), strings.Clone(mode)
	if filename == "" {
		return nil, ValueError{"Path cannot be empty"}
	}
	flag, readable, writable, appendMode, ok := parseOpenMode(mode)
	if !ok {
		return nil, ValueError{"fopen(): Argument #2 ($mode) must be a valid mode"}
	}

	if after, ok0 := strings.CutPrefix(filename, "php://"); ok0 {
		sub := after
		switch {
		case sub == "memory":
			return &streamHandle{
				kind:        backendMemory,
				uri:         filename,
				mode:        "w+b",
				wrapperType: "PHP",
				streamType:  "MEMORY",
				readable:    true,
				writable:    true,
				seekable:    true,
			}, nil
		case sub == "temp" || strings.HasPrefix(sub, "temp/"):
			maxMem := int64(2 * 1024 * 1024)
			if after, hasMax := strings.CutPrefix(sub, "temp/maxmemory:"); hasMax {
				n, err := strconv.ParseInt(after, 10, 64)
				if err != nil || n < 0 {
					return nil, errors.New("invalid maxmemory")
				}
				maxMem = n
			} else if sub != "temp" {
				return nil, errors.New("invalid php://temp path")
			}
			return &streamHandle{
				kind:        backendTemp,
				uri:         filename,
				mode:        "w+b",
				wrapperType: "PHP",
				streamType:  "TEMP",
				readable:    true,
				writable:    true,
				seekable:    true,
				maxMem:      maxMem,
			}, nil
		case sub == "input":
			zs.mu.Lock()
			inCopy := append([]byte(nil), zs.input...)
			zs.mu.Unlock()
			return &streamHandle{
				kind:        backendMemory,
				uri:         filename,
				mode:        "rb",
				wrapperType: "PHP",
				streamType:  "Input",
				readable:    true,
				writable:    false,
				seekable:    true,
				buf:         inCopy,
			}, nil
		case sub == "output" || sub == "stdout":
			zs.mu.Lock()
			w := zs.stdout
			zs.mu.Unlock()
			if w == nil {
				w = out
			}
			if w == nil {
				w = io.Discard
			}
			stType := "Output"
			if sub == "stdout" {
				stType = "STDIO"
			}
			return &streamHandle{
				kind:        backendOutput,
				uri:         filename,
				mode:        "wb",
				wrapperType: "PHP",
				streamType:  stType,
				readable:    false,
				writable:    true,
				seekable:    false,
				writer:      w,
			}, nil
		case sub == "stderr":
			zs.mu.Lock()
			w := zs.stderr
			zs.mu.Unlock()
			if w == nil {
				w = os.Stderr
			}
			return &streamHandle{
				kind:        backendOutput,
				uri:         filename,
				mode:        "wb",
				wrapperType: "PHP",
				streamType:  "STDIO",
				readable:    false,
				writable:    true,
				seekable:    false,
				writer:      w,
			}, nil
		default:
			return nil, errors.New("unsupported php:// stream")
		}
	}

	if strings.HasPrefix(filename, "data://") || strings.HasPrefix(filename, "data:") {
		data, mediatype, isB64, valid := parseDataURI(filename)
		if !valid {
			return nil, errors.New("invalid data:// URI")
		}
		return &streamHandle{
			kind:        backendData,
			uri:         filename,
			mode:        "r",
			wrapperType: "RFC2397",
			streamType:  "RFC2397",
			mediatype:   mediatype,
			isBase64:    isB64,
			readable:    true,
			writable:    false,
			seekable:    true,
			buf:         data,
		}, nil
	}

	if h, user, err := zs.openUserStream(filename, mode, readable, writable, appendMode, ctxID); user {
		return h, err
	}

	resolved := zs.resolvePath(filename)
	f, err := zs.openFile(resolved, flag)
	if err != nil {
		return nil, err
	}
	if info, statErr := f.Stat(); statErr == nil && info.IsDir() && writable {
		_ = f.Close()
		return nil, syscall.EISDIR
	}
	return &streamHandle{
		kind:        backendFile,
		uri:         filename,
		mode:        mode,
		wrapperType: "plainfile",
		streamType:  "STDIO",
		readable:    readable,
		writable:    writable,
		appendMode:  appendMode,
		seekable:    true,
		file:        f,
	}, nil
}

func (h *streamHandle) spillIfNeeded(extra int) error {
	if h.kind != backendTemp || h.file != nil {
		return nil
	}
	if int64(len(h.buf)+extra) <= h.maxMem {
		return nil
	}
	tf, err := os.CreateTemp("", "php127-temp-*")
	if err != nil {
		return err
	}
	if len(h.buf) > 0 {
		if _, err := tf.Write(h.buf); err != nil {
			_ = tf.Close()
			_ = os.Remove(tf.Name())
			return err
		}
	}
	if _, err := tf.Seek(h.pos, io.SeekStart); err != nil {
		_ = tf.Close()
		_ = os.Remove(tf.Name())
		return err
	}
	h.file = tf
	h.tempSpill = tf.Name()
	h.buf = nil
	return nil
}

func (h *streamHandle) readBytes(maxLen int) ([]byte, error) {
	if !h.readable {
		return nil, errors.New("not readable")
	}
	if maxLen <= 0 {
		return []byte{}, nil
	}
	if h.conn != nil {
		return h.socketRead(maxLen)
	}
	if h.src != nil {
		buf := make([]byte, maxLen)
		n, err := io.ReadFull(h.src, buf)
		h.pos += int64(n)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			h.eof = true
			return buf[:n], nil
		}
		if h.kind == backendUser && h.src.Buffered() == 0 && len(h.closers) > 0 {
			if eofer, ok := h.closers[0].(interface{ EOF() bool }); ok && eofer.EOF() {
				h.eof = true
			}
		}
		return buf[:n], err
	}
	if h.file != nil {
		buf := make([]byte, maxLen)
		n, err := h.file.Read(buf)
		h.pos += int64(n)
		if err == io.EOF {
			if n == 0 {
				h.eof = true
			}
			return buf[:n], nil
		}
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
	if h.pos >= int64(len(h.buf)) {
		h.eof = true
		return []byte{}, nil
	}
	avail := int64(len(h.buf)) - h.pos
	n := min(int64(maxLen), avail)
	out := make([]byte, n)
	copy(out, h.buf[h.pos:h.pos+n])
	h.pos += n
	return out, nil
}

func (h *streamHandle) readAll() ([]byte, error) {
	if !h.readable {
		return nil, errors.New("not readable")
	}
	if h.conn != nil {
		return h.socketReadAll()
	}
	if h.src != nil {
		data, err := io.ReadAll(h.src)
		h.pos += int64(len(data))
		h.eof = err == nil
		return data, err
	}
	if h.file != nil {
		data, err := io.ReadAll(h.file)
		h.pos += int64(len(data))
		if err == nil {
			h.eof = true
		}
		return data, err
	}
	if h.pos >= int64(len(h.buf)) {
		h.eof = true
		return []byte{}, nil
	}
	out := make([]byte, int64(len(h.buf))-h.pos)
	copy(out, h.buf[h.pos:])
	h.pos = int64(len(h.buf))
	h.eof = true
	return out, nil
}

func (h *streamHandle) readLine(maxBytes int) ([]byte, bool, error) {
	if !h.readable {
		return nil, false, errors.New("not readable")
	}
	limit := -1
	if maxBytes > 0 {
		limit = maxBytes - 1
	}
	if h.conn != nil {
		return h.socketReadLine(limit)
	}
	if h.src != nil {
		var line []byte
		for limit < 0 || len(line) < limit {
			c, err := h.src.ReadByte()
			if err == io.EOF {
				h.eof = true
				break
			}
			if err != nil {
				return nil, false, err
			}
			h.pos++
			line = append(line, c)
			if c == '\n' {
				break
			}
		}
		if len(line) == 0 {
			return nil, false, nil
		}
		return line, true, nil
	}
	if h.file != nil {
		var line []byte
		var one [1]byte
		for limit < 0 || len(line) < limit {
			n, err := h.file.Read(one[:])
			if n > 0 {
				h.pos++
				line = append(line, one[0])
				if one[0] == '\n' {
					break
				}
			}
			if err == io.EOF {
				if len(line) == 0 {
					h.eof = true
					return nil, false, nil
				}
				h.eof = true
				break
			}
			if err != nil {
				return nil, false, err
			}
		}
		return line, true, nil
	}
	if h.pos >= int64(len(h.buf)) {
		h.eof = true
		return nil, false, nil
	}
	start := h.pos
	end := int64(len(h.buf))
	if limit >= 0 && start+int64(limit) < end {
		end = start + int64(limit)
	}
	idx := bytes.IndexByte(h.buf[start:end], '\n')
	if idx >= 0 {
		end = start + int64(idx) + 1
	} else if end == int64(len(h.buf)) {
		h.eof = true
	}
	out := make([]byte, end-start)
	copy(out, h.buf[start:end])
	h.pos = end
	return out, true, nil
}

func (h *streamHandle) writeBytes(data []byte) (int, error) {
	if !h.writable {
		return 0, nil
	}
	if h.kind == backendOutput {
		if h.writer == nil {
			return len(data), nil
		}
		return h.writer.Write(data)
	}
	if h.conn != nil {
		return h.socketWrite(data)
	}
	if h.sink != nil {
		n, err := h.sink.Write(data)
		h.pos += int64(n)
		return n, err
	}
	if err := h.spillIfNeeded(len(data)); err != nil {
		return 0, err
	}
	if h.file != nil {
		n, err := h.file.Write(data)
		if h.appendMode {
			if pos, seekErr := h.file.Seek(0, io.SeekCurrent); seekErr == nil {
				h.pos = pos
			}
		} else {
			h.pos += int64(n)
		}
		return n, err
	}
	if h.appendMode {
		h.pos = int64(len(h.buf))
	}
	end := h.pos + int64(len(data))
	if end > int64(len(h.buf)) {
		grown := make([]byte, end)
		copy(grown, h.buf)
		h.buf = grown
	}
	copy(h.buf[h.pos:end], data)
	h.pos = end
	return len(data), nil
}

func (h *streamHandle) seek(offset int64, whence int) int {
	if !h.seekable {
		return -1
	}
	if h.seekFn != nil {
		r := h.seekFn(offset, whence)
		if r == 0 {
			h.eof = false
		}
		return r
	}
	if h.file != nil {
		var w int
		switch whence {
		case SEEK_SET:
			w = io.SeekStart
		case SEEK_CUR:
			w = io.SeekCurrent
		case SEEK_END:
			w = io.SeekEnd
		default:
			return -1
		}
		pos, err := h.file.Seek(offset, w)
		if err != nil {
			return -1
		}
		h.pos = pos
		h.eof = false
		return 0
	}
	var target int64
	switch whence {
	case SEEK_SET:
		target = offset
	case SEEK_CUR:
		target = h.pos + offset
	case SEEK_END:
		target = int64(len(h.buf)) + offset
	default:
		return -1
	}
	if target < 0 {
		return -1
	}
	h.pos = target
	h.eof = false
	return 0
}

func (h *streamHandle) truncate(size int64) bool {
	if !h.writable || size < 0 {
		return false
	}
	if h.file != nil {
		return h.file.Truncate(size) == nil
	}
	if h.kind == backendMemory || h.kind == backendTemp {
		if size <= int64(len(h.buf)) {
			h.buf = h.buf[:size]
		} else {
			grown := make([]byte, size)
			copy(grown, h.buf)
			h.buf = grown
		}
		return true
	}
	return false
}

func requireStreamArg(z *zone.Zone, v types.Value, fn, idx, param string) (*ZoneStreams, *streamHandle, error) {
	v = deref(z, v)
	if v.Type != types.TypeResource || v.Extra != ResourceKindStream {
		return nil, nil, typeErr(fn, idx, param, "resource", typeKind(z, v))
	}
	zs := getZoneStreams(z)
	h, ok := zs.lookupStream(v)
	if !ok {
		return nil, nil, TypeError{fn + "(): supplied resource is not a valid stream resource"}
	}
	return zs, h, nil
}

func nativeFopen(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fopen", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "fopen", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	mode, err := asStringParam(z, args[1], "fopen", "2", "mode")
	if err != nil {
		return types.Value{}, err
	}
	var ctxID uint32
	if len(args) == 4 && !deref(z, args[3]).IsNull() {
		ctx, ok := lookupContext(z, args[3])
		if !ok {
			return types.Value{}, typeErr("fopen", "4", "context", "resource", typeKind(z, deref(z, args[3])))
		}
		ctxID = ctx.slotID
	}
	zs := getZoneStreams(z)
	h, err := openStream(zs, out, filename, mode, ctxID)
	if err != nil {
		if ve, ok := errors.AsType[ValueError](err); ok {
			return types.Value{}, ve
		}
		warnOpenFailure(out, "fopen", filename, err)
		return types.NewBool(false), nil
	}
	h.ctxID = ctxID
	return zs.registerStream(h), nil
}

// nativeFclose sert fclose et gzclose : le nom porté par les diagnostics est celui de l'appel.
func nativeFclose(fn string) func(*zone.Zone, io.Writer, []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(fn, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		zs, h, err := requireStreamArg(z, args[0], fn, "1", "stream")
		if err != nil {
			return types.Value{}, err
		}
		return types.NewBool(zs.closeStream(h.slotID)), nil
	}
}

func nativeFread(fn string) func(*zone.Zone, io.Writer, []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		return fread(z, fn, args)
	}
}

func fread(z *zone.Zone, fn string, args []types.Value) (types.Value, error) {
	if err := arity(fn, len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], fn, "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	length, err := asIntParam(z, args[1], fn, "2", "length")
	if err != nil {
		return types.Value{}, err
	}
	if length <= 0 {
		return types.Value{}, ValueError{fn + "(): Argument #2 ($length) must be greater than 0"}
	}
	data, err := h.readBytes(int(length))
	if err != nil {
		return types.NewBool(false), nil
	}
	return newStr(z, string(data))
}

func nativeFwrite(fnName string) func(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(fnName, len(args), 2, 3); err != nil {
			return types.Value{}, err
		}
		_, h, err := requireStreamArg(z, args[0], fnName, "1", "stream")
		if err != nil {
			return types.Value{}, err
		}
		data, err := asStringParam(z, args[1], fnName, "2", "data")
		if err != nil {
			return types.Value{}, err
		}
		raw := []byte(data)
		if len(args) == 3 && !deref(z, args[2]).IsNull() {
			maxLen, err := asIntParam(z, args[2], fnName, "3", "length")
			if err != nil {
				return types.Value{}, err
			}
			if maxLen < 0 {
				raw = nil
			} else if int64(len(raw)) > maxLen {
				raw = raw[:maxLen]
			}
		}
		n, err := h.writeBytes(raw)
		if err != nil {
			return types.NewBool(false), nil
		}
		return types.NewLong(int64(n)), nil
	}
}

func nativeFgets(fn string) func(*zone.Zone, io.Writer, []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		return fgets(z, fn, args)
	}
}

func fgets(z *zone.Zone, fn string, args []types.Value) (types.Value, error) {
	if err := arity(fn, len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], fn, "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	maxLen := -1
	if len(args) == 2 && !deref(z, args[1]).IsNull() {
		ln, err := asIntParam(z, args[1], fn, "2", "length")
		if err != nil {
			return types.Value{}, err
		}
		if ln <= 0 {
			return types.Value{}, ValueError{fn + "(): Argument #2 ($length) must be greater than 0"}
		}
		maxLen = int(ln)
	}
	line, ok, err := h.readLine(maxLen)
	if err != nil || !ok {
		return types.NewBool(false), nil
	}
	return newStr(z, string(line))
}

func nativeFgetc(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fgetc", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "fgetc", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	data, err := h.readBytes(1)
	if err != nil || len(data) == 0 {
		h.eof = true
		return types.NewBool(false), nil
	}
	return newStr(z, string(data))
}

func nativeFeof(fn string) func(*zone.Zone, io.Writer, []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(fn, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		_, h, err := requireStreamArg(z, args[0], fn, "1", "stream")
		if err != nil {
			return types.Value{}, err
		}
		return types.NewBool(h.eof), nil
	}
}

func nativeFtell(fn string) func(*zone.Zone, io.Writer, []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		return ftell(z, fn, args)
	}
}

func ftell(z *zone.Zone, fn string, args []types.Value) (types.Value, error) {
	if err := arity(fn, len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], fn, "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	// Un déplacement gzip refusé laisse la position à -1 : ftell rend alors false, comme PHP sur zlib.
	if !h.seekable || h.pos < 0 {
		return types.NewBool(false), nil
	}
	if h.file != nil {
		pos, err := h.file.Seek(0, io.SeekCurrent)
		if err != nil {
			return types.NewBool(false), nil
		}
		h.pos = pos
	}
	return types.NewLong(h.pos), nil
}

func nativeFseek(fn string) func(*zone.Zone, io.Writer, []types.Value) (types.Value, error) {
	return func(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(fn, len(args), 2, 3); err != nil {
			return types.Value{}, err
		}
		_, h, err := requireStreamArg(z, args[0], fn, "1", "stream")
		if err != nil {
			return types.Value{}, err
		}
		offset, err := asIntParam(z, args[1], fn, "2", "offset")
		if err != nil {
			return types.Value{}, err
		}
		whence := int64(SEEK_SET)
		if len(args) == 3 {
			whence, err = asIntParam(z, args[2], fn, "3", "whence")
			if err != nil {
				return types.Value{}, err
			}
		}
		r := h.seek(offset, int(whence))
		if r == seekEndUnsupported {
			warn(out, fn+"()", "SEEK_END is not supported")
			r = -1
		}
		return types.NewLong(int64(r)), nil
	}
}

func nativeRewind(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("rewind", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "rewind", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewBool(h.seek(0, SEEK_SET) == 0), nil
}

func nativeFflush(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fflush", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "fflush", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	if h.closed {
		return types.NewBool(false), nil
	}
	if h.kind == backendUser && len(h.closers) > 0 {
		if flusher, ok := h.closers[0].(interface{ Flush() bool }); ok {
			return types.NewBool(flusher.Flush()), nil
		}
	}
	return types.NewBool(true), nil
}

func nativeFtruncate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("ftruncate", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "ftruncate", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	size, err := asIntParam(z, args[1], "ftruncate", "2", "size")
	if err != nil {
		return types.Value{}, err
	}
	if size < 0 {
		return types.Value{}, ValueError{"ftruncate(): Argument #2 ($size) must be greater than or equal to 0"}
	}
	return types.NewBool(h.truncate(size)), nil
}

func buildStatArray(z *zone.Zone, fi os.FileInfo) (types.Value, error) {
	var dev, ino, mode, nlink, uid, gid, rdev, size, atime, mtime, ctime, blksize, blocks int64
	size = fi.Size()
	mtime = fi.ModTime().Unix()
	atime = mtime
	ctime = mtime
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st != nil {
		dev = int64(st.Dev)
		ino = int64(st.Ino)
		mode = int64(st.Mode)
		nlink = int64(st.Nlink)
		uid = int64(st.Uid)
		gid = int64(st.Gid)
		rdev = int64(st.Rdev)
		size = st.Size
		atime = st.Atim.Sec
		mtime = st.Mtim.Sec
		ctime = st.Ctim.Sec
		blksize = st.Blksize
		blocks = st.Blocks
	}
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	vals := [13]int64{dev, ino, mode, nlink, uid, gid, rdev, size, atime, mtime, ctime, blksize, blocks}
	names := [13]string{"dev", "ino", "mode", "nlink", "uid", "gid", "rdev", "size", "atime", "mtime", "ctime", "blksize", "blocks"}
	for i, v := range vals {
		if err := setArrayIntKey(z, &arr, int64(i), types.NewLong(v)); err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
	}
	for i, name := range names {
		if err := setArrayKey(z, &arr, name, types.NewLong(vals[i])); err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
	}
	return arr, nil
}

func nativeFstat(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fstat", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "fstat", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	if h.file != nil {
		fi, err := h.file.Stat()
		if err != nil {
			return types.NewBool(false), nil
		}
		return buildStatArray(z, fi)
	}
	if h.kind == backendMemory || h.kind == backendTemp || h.kind == backendData {
		arr, err := types.NewArray(z)
		if err != nil {
			return types.Value{}, err
		}
		vals := [13]int64{0, 0, 0o100666, 1, 0, 0, 0, int64(len(h.buf)), 0, 0, 0, -1, -1}
		names := [13]string{"dev", "ino", "mode", "nlink", "uid", "gid", "rdev", "size", "atime", "mtime", "ctime", "blksize", "blocks"}
		for i, v := range vals {
			_ = setArrayIntKey(z, &arr, int64(i), types.NewLong(v))
		}
		for i, name := range names {
			_ = setArrayKey(z, &arr, name, types.NewLong(vals[i]))
		}
		return arr, nil
	}
	return types.NewBool(false), nil
}

func nativeFpassthru(fn string) func(*zone.Zone, io.Writer, []types.Value) (types.Value, error) {
	return func(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
		return fpassthru(z, out, fn, args)
	}
}

func fpassthru(z *zone.Zone, out io.Writer, fn string, args []types.Value) (types.Value, error) {
	if err := arity(fn, len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	zs, h, err := requireStreamArg(z, args[0], fn, "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	data, err := h.readAll()
	if err != nil {
		return types.NewLong(0), nil
	}
	zs.mu.Lock()
	w := zs.stdout
	zs.mu.Unlock()
	if w == nil {
		w = out
	}
	if w != nil && len(data) > 0 {
		_, _ = w.Write(data)
	}
	return types.NewLong(int64(len(data))), nil
}

func nativeStreamGetContents(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("stream_get_contents", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "stream_get_contents", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	maxLen := int64(-1)
	if len(args) >= 2 && !deref(z, args[1]).IsNull() {
		maxLen, err = asIntParam(z, args[1], "stream_get_contents", "2", "maxlength")
		if err != nil {
			return types.Value{}, err
		}
		if maxLen < -1 {
			return types.Value{}, ValueError{"stream_get_contents(): Argument #2 ($maxlength) must be greater than or equal to -1"}
		}
	}
	if len(args) == 3 && !deref(z, args[2]).IsNull() {
		offset, err := asIntParam(z, args[2], "stream_get_contents", "3", "offset")
		if err != nil {
			return types.Value{}, err
		}
		if offset >= 0 {
			if h.seek(offset, SEEK_SET) != 0 {
				return types.NewBool(false), nil
			}
		}
	}
	var data []byte
	if maxLen == 0 {
		data = []byte{}
	} else if maxLen > 0 {
		data, err = h.readBytes(int(maxLen))
	} else {
		data, err = h.readAll()
	}
	if err != nil {
		return types.NewBool(false), nil
	}
	return newStr(z, string(data))
}

func nativeStreamCopyToStream(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("stream_copy_to_stream", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	_, from, err := requireStreamArg(z, args[0], "stream_copy_to_stream", "1", "from")
	if err != nil {
		return types.Value{}, err
	}
	_, to, err := requireStreamArg(z, args[1], "stream_copy_to_stream", "2", "to")
	if err != nil {
		return types.Value{}, err
	}
	maxLen := int64(-1)
	if len(args) >= 3 && !deref(z, args[2]).IsNull() {
		maxLen, err = asIntParam(z, args[2], "stream_copy_to_stream", "3", "maxlength")
		if err != nil {
			return types.Value{}, err
		}
	}
	if len(args) == 4 && !deref(z, args[3]).IsNull() {
		offset, err := asIntParam(z, args[3], "stream_copy_to_stream", "4", "offset")
		if err != nil {
			return types.Value{}, err
		}
		if offset > 0 {
			if from.seek(offset, SEEK_SET) != 0 {
				return types.NewBool(false), nil
			}
		}
	}
	var data []byte
	if maxLen == 0 {
		data = nil
	} else if maxLen > 0 {
		data, err = from.readBytes(int(maxLen))
	} else {
		data, err = from.readAll()
	}
	if err != nil {
		return types.NewBool(false), nil
	}
	if len(data) == 0 {
		return types.NewLong(0), nil
	}
	n, err := to.writeBytes(data)
	if err != nil {
		return types.NewBool(false), nil
	}
	return types.NewLong(int64(n)), nil
}

// nativeStreamGetMetaData sert stream_get_meta_data et son alias socket_get_status.
func nativeStreamGetMetaData(fn string) func(*zone.Zone, io.Writer, []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		return streamGetMetaData(z, fn, args)
	}
}

func streamGetMetaData(z *zone.Zone, fn string, args []types.Value) (types.Value, error) {
	if err := arity(fn, len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], fn, "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	setStr := func(k, v string) {
		sv, err := newStr(z, v)
		if err == nil {
			_ = setArrayKey(z, &arr, k, sv)
			types.ReleaseValue(z, &sv)
		}
	}
	if h.kind == backendSocket {
		_ = setArrayKey(z, &arr, "timed_out", types.NewBool(h.timedOut))
		_ = setArrayKey(z, &arr, "blocked", types.NewBool(!h.nonBlocking))
		_ = setArrayKey(z, &arr, "eof", types.NewBool(h.eof))
		setStr("stream_type", h.streamType)
		setStr("mode", h.mode)
		_ = setArrayKey(z, &arr, "unread_bytes", types.NewLong(int64(h.rd.Buffered())))
		_ = setArrayKey(z, &arr, "seekable", types.NewBool(false))
		setStr("uri", h.uri)
		return arr, nil
	}
	if h.kind == backendData {
		setStr("mediatype", h.mediatype)
		_ = setArrayKey(z, &arr, "base64", types.NewBool(h.isBase64))
	} else {
		_ = setArrayKey(z, &arr, "timed_out", types.NewBool(false))
		_ = setArrayKey(z, &arr, "blocked", types.NewBool(true))
		_ = setArrayKey(z, &arr, "eof", types.NewBool(h.eof))
	}
	// Un flux zlib de gzopen n'annonce ni wrapper_type ni uri, comme PHP 8.3.
	if h.kind != backendGzip {
		setStr("wrapper_type", h.wrapperType)
	}
	setStr("stream_type", h.streamType)
	setStr("mode", h.mode)
	_ = setArrayKey(z, &arr, "unread_bytes", types.NewLong(0))
	_ = setArrayKey(z, &arr, "seekable", types.NewBool(h.seekable))
	if h.kind != backendGzip {
		setStr("uri", h.uri)
	}
	return arr, nil
}

func parseCSVLine(h *streamHandle, maxLen int, sep, encl, esc byte, hasEsc bool) ([]string, bool, error) {
	line, ok, err := h.readLine(maxLen)
	if err != nil || !ok {
		return nil, false, err
	}
	var fields []string
	var cur bytes.Buffer
	inQuotes := false
	for {
		for i := 0; i < len(line); i++ {
			c := line[i]
			if inQuotes {
				if hasEsc && esc != encl && c == esc && i+1 < len(line) {
					i++
					cur.WriteByte(line[i])
					continue
				}
				if c == encl {
					if i+1 < len(line) && line[i+1] == encl {
						i++
						cur.WriteByte(encl)
						continue
					}
					inQuotes = false
					continue
				}
				cur.WriteByte(c)
				continue
			}
			if c == '\r' && i+1 < len(line) && line[i+1] == '\n' {
				continue
			}
			if c == '\n' {
				continue
			}
			if c == encl && cur.Len() == 0 {
				inQuotes = true
				continue
			}
			if c == sep {
				fields = append(fields, cur.String())
				cur.Reset()
				continue
			}
			cur.WriteByte(c)
		}
		if !inQuotes {
			break
		}
		more, moreOK, moreErr := h.readLine(maxLen)
		if moreErr != nil || !moreOK {
			break
		}
		line = more
	}
	fields = append(fields, cur.String())
	return fields, true, nil
}

func nativeFgetcsv(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fgetcsv", len(args), 1, 5); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "fgetcsv", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	maxLen := -1
	if len(args) >= 2 && !deref(z, args[1]).IsNull() {
		ln, err := asIntParam(z, args[1], "fgetcsv", "2", "length")
		if err != nil {
			return types.Value{}, err
		}
		if ln < 0 {
			return types.Value{}, ValueError{"fgetcsv(): Argument #2 ($length) must be greater than or equal to 0"}
		}
		if ln > 0 {
			maxLen = int(ln)
		}
	}
	sep := byte(',')
	if len(args) >= 3 {
		s, err := asStringParam(z, args[2], "fgetcsv", "3", "separator")
		if err != nil {
			return types.Value{}, err
		}
		if len(s) != 1 {
			return types.Value{}, ValueError{"fgetcsv(): Argument #3 ($separator) must be a single character"}
		}
		sep = s[0]
	}
	encl := byte('"')
	if len(args) >= 4 {
		s, err := asStringParam(z, args[3], "fgetcsv", "4", "enclosure")
		if err != nil {
			return types.Value{}, err
		}
		if len(s) != 1 {
			return types.Value{}, ValueError{"fgetcsv(): Argument #4 ($enclosure) must be a single character"}
		}
		encl = s[0]
	}
	esc := byte('\\')
	hasEsc := true
	if len(args) == 5 {
		s, err := asStringParam(z, args[4], "fgetcsv", "5", "escape")
		if err != nil {
			return types.Value{}, err
		}
		if len(s) == 0 {
			hasEsc = false
		} else if len(s) == 1 {
			esc = s[0]
		} else {
			return types.Value{}, ValueError{"fgetcsv(): Argument #5 ($escape) must be empty or a single character"}
		}
	}
	fields, ok, err := parseCSVLine(h, maxLen, sep, encl, esc, hasEsc)
	if err != nil || !ok {
		return types.NewBool(false), nil
	}
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, f := range fields {
		sv, err := newStr(z, f)
		if err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
		_ = types.ArrayAppend(z, &arr, sv)
		types.ReleaseValue(z, &sv)
	}
	return arr, nil
}

func nativeFputcsv(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fputcsv", len(args), 2, 6); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "fputcsv", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	arr := deref(z, args[1])
	if arr.Type != types.TypeArray {
		return types.Value{}, typeErr("fputcsv", "2", "fields", "array", typeKind(z, arr))
	}
	sep := ","
	if len(args) >= 3 {
		s, err := asStringParam(z, args[2], "fputcsv", "3", "separator")
		if err != nil {
			return types.Value{}, err
		}
		if len(s) != 1 {
			return types.Value{}, ValueError{"fputcsv(): Argument #3 ($separator) must be a single character"}
		}
		sep = s
	}
	encl := "\""
	if len(args) >= 4 {
		s, err := asStringParam(z, args[3], "fputcsv", "4", "enclosure")
		if err != nil {
			return types.Value{}, err
		}
		if len(s) != 1 {
			return types.Value{}, ValueError{"fputcsv(): Argument #4 ($enclosure) must be a single character"}
		}
		encl = s
	}
	if len(args) >= 5 {
		s, err := asStringParam(z, args[4], "fputcsv", "5", "escape")
		if err != nil {
			return types.Value{}, err
		}
		if len(s) > 1 {
			return types.Value{}, ValueError{"fputcsv(): Argument #5 ($escape) must be empty or a single character"}
		}
	}
	eol := "\n"
	if len(args) == 6 {
		var err error
		eol, err = asStringParam(z, args[5], "fputcsv", "6", "eol")
		if err != nil {
			return types.Value{}, err
		}
	}
	var b strings.Builder
	idx := 0
	var convErr error
	types.ArrayForEach(z, arr, func(_, v types.Value) bool {
		if idx > 0 {
			b.WriteString(sep)
		}
		idx++
		s, err := castString(z, v)
		if err != nil {
			convErr = err
			return false
		}
		needsEncl := strings.Contains(s, sep) || strings.Contains(s, encl) || strings.ContainsAny(s, " \t\r\n")
		if needsEncl {
			b.WriteString(encl)
			b.WriteString(strings.ReplaceAll(s, encl, encl+encl))
			b.WriteString(encl)
		} else {
			b.WriteString(s)
		}
		return true
	})
	if convErr != nil {
		return types.Value{}, convErr
	}
	b.WriteString(eol)
	n, err := h.writeBytes([]byte(b.String()))
	if err != nil {
		return types.NewBool(false), nil
	}
	return types.NewLong(int64(n)), nil
}

// Lot 7.3 : sockets clientes, contextes de flux, verrous et réglages de tampon.

// ResourceKindContext désigne une ressource stream-context ; elle partage la table des flux de la Zone
// afin d'être libérée par le même nettoyage de Zone.
const ResourceKindContext uint16 = 3

const defaultSocketTimeout = 60 * time.Second

func init() {
	// manager.go précède ce fichier dans l'ordre d'initialisation : la table existe déjà.
	for name, v := range map[string]int64{
		"STREAM_CLIENT_PERSISTENT":    STREAM_CLIENT_PERSISTENT,
		"STREAM_CLIENT_ASYNC_CONNECT": STREAM_CLIENT_ASYNC_CONNECT,
		"STREAM_CLIENT_CONNECT":       STREAM_CLIENT_CONNECT,
		"STREAM_IS_URL":               1,
		"IMAGETYPE_UNKNOWN":           0,
		"IMAGETYPE_GIF":               1,
		"IMAGETYPE_JPEG":              2,
		"IMAGETYPE_PNG":               3,
		"IMAGETYPE_SWF":               4,
		"IMAGETYPE_PSD":               5,
		"IMAGETYPE_BMP":               6,
		"IMAGETYPE_TIFF_II":           7,
		"IMAGETYPE_TIFF_MM":           8,
		"IMAGETYPE_JPC":               9,
		"IMAGETYPE_JPEG2000":          9,
		"IMAGETYPE_JP2":               10,
		"IMAGETYPE_JPX":               11,
		"IMAGETYPE_JB2":               12,
		"IMAGETYPE_SWC":               13,
		"IMAGETYPE_IFF":               14,
		"IMAGETYPE_WBMP":              15,
		"IMAGETYPE_XBM":               16,
		"IMAGETYPE_ICO":               17,
		"IMAGETYPE_WEBP":              18,
		"IMAGETYPE_AVIF":              19,
		"IMAGETYPE_COUNT":             20,
	} {
		constants[name] = types.NewLong(v)
	}
}

func (h *streamHandle) deadline() {
	if h.nonBlocking {
		_ = h.conn.SetReadDeadline(time.Now().Add(time.Millisecond))
		return
	}
	if h.timeout > 0 {
		_ = h.conn.SetDeadline(time.Now().Add(h.timeout))
	}
}

// socketErr classe une erreur de lecture : délai dépassé et fin de flux ne sont pas des échecs PHP.
func (h *streamHandle) socketErr(err error) error {
	if err == nil {
		return nil
	}
	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
		h.timedOut = !h.nonBlocking
		return nil
	}
	h.eof = true
	if err == io.EOF {
		return nil
	}
	return err
}

func (h *streamHandle) socketRead(maxLen int) ([]byte, error) {
	h.deadline()
	h.timedOut = false
	buf := make([]byte, maxLen)
	n, err := h.rd.Read(buf)
	return buf[:n], h.socketErr(err)
}

func (h *streamHandle) socketReadAll() ([]byte, error) {
	h.timedOut = false
	var out []byte
	buf := make([]byte, 8192)
	for !h.eof && !h.timedOut {
		h.deadline()
		n, err := h.rd.Read(buf)
		out = append(out, buf[:n]...)
		if err = h.socketErr(err); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (h *streamHandle) socketReadLine(limit int) ([]byte, bool, error) {
	h.timedOut = false
	var line []byte
	for limit < 0 || len(line) < limit {
		h.deadline()
		c, err := h.rd.ReadByte()
		if err != nil {
			if err = h.socketErr(err); err != nil {
				return nil, false, err
			}
			break
		}
		line = append(line, c)
		if c == '\n' {
			break
		}
	}
	if len(line) == 0 {
		return nil, false, nil
	}
	return line, true, nil
}

func (h *streamHandle) socketWrite(data []byte) (int, error) {
	h.deadline()
	return h.conn.Write(data)
}

func assignRefArg(z *zone.Zone, args []types.Value, i int, v types.Value) {
	if len(args) > i && args[i].Type == types.TypeReference {
		types.AssignRef(z, args[i], v)
	}
	types.ReleaseValue(z, &v)
}

func assignRefStr(z *zone.Zone, args []types.Value, i int, s string) {
	if len(args) <= i || args[i].Type != types.TypeReference {
		return
	}
	if v, err := newStr(z, s); err == nil {
		assignRefArg(z, args, i, v)
	}
}

func nativeFlock(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("flock", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "flock", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	op, err := asIntParam(z, args[1], "flock", "2", "operation")
	if err != nil {
		return types.Value{}, err
	}
	act := op & LOCK_UN
	if act < 1 {
		return types.Value{}, ValueError{"flock(): Argument #2 ($operation) must be one of LOCK_SH, LOCK_EX, or LOCK_UN"}
	}
	assignRefArg(z, args, 2, types.NewLong(0))
	if h.file == nil {
		return types.NewBool(false), nil
	}
	how := map[int64]int{LOCK_SH: syscall.LOCK_SH, LOCK_EX: syscall.LOCK_EX, LOCK_UN: syscall.LOCK_UN}[act]
	if op&LOCK_NB != 0 {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(h.file.Fd()), how); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			assignRefArg(z, args, 2, types.NewLong(1))
		}
		return types.NewBool(false), nil
	}
	return types.NewBool(true), nil
}

func optionsFormErr() error {
	return ValueError{`Options should have the form ["wrappername"]["optionname"] = $value`}
}

// mergeContextOptions applique un tableau ["wrapper"]["option"] = valeur aux options du contexte.
func mergeContextOptions(z *zone.Zone, h *streamHandle, opts types.Value) error {
	var ferr error
	types.ArrayForEach(z, opts, func(wk, wv types.Value) bool {
		wv = deref(z, wv)
		if wk.Type != types.TypeString || wv.Type != types.TypeArray {
			ferr = optionsFormErr()
			return false
		}
		types.ArrayForEach(z, wv, func(ok, ov types.Value) bool {
			ferr = setContextOption(z, h, wk, ok, ov)
			return ferr == nil
		})
		return ferr == nil
	})
	return ferr
}

func setContextOption(z *zone.Zone, h *streamHandle, wrapper, option, val types.Value) error {
	sub, found, err := types.ArrayGet(z, h.options, wrapper, warnDrop)
	if err != nil {
		return err
	}
	if found && deref(z, sub).Type == types.TypeArray {
		sub = types.RetainValue(z, deref(z, sub))
	} else if sub, err = types.NewArray(z); err != nil {
		return err
	}
	if err = types.ArraySet(z, &sub, option, deref(z, val), warnDrop); err == nil {
		err = types.ArraySet(z, &h.options, wrapper, sub, warnDrop)
	}
	types.ReleaseValue(z, &sub)
	return err
}

func lookupContext(z *zone.Zone, v types.Value) (*streamHandle, bool) {
	v = deref(z, v)
	if v.Type != types.TypeResource || v.Extra != ResourceKindContext {
		return nil, false
	}
	zs := getZoneStreams(z)
	zs.mu.Lock()
	defer zs.mu.Unlock()
	h, ok := zs.streams[v.DataSlot]
	if !ok || h.closed || h.kind != backendContext {
		return nil, false
	}
	return h, true
}

func nativeStreamContextCreate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("stream_context_create", len(args), 0, 2); err != nil {
		return types.Value{}, err
	}
	opts, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	h := &streamHandle{kind: backendContext, options: opts}
	for i, name := range []string{"options", "params"} {
		if len(args) <= i || deref(z, args[i]).IsNull() {
			continue
		}
		a := deref(z, args[i])
		if a.Type != types.TypeArray {
			types.ReleaseValue(z, &h.options)
			return types.Value{}, typeErr("stream_context_create", strconv.Itoa(i+1), name, "?array", typeKind(z, a))
		}
		if i == 1 {
			key, kerr := newStr(z, "options")
			if kerr != nil {
				return types.Value{}, kerr
			}
			inner, found, gerr := types.ArrayGet(z, a, key, warnDrop)
			types.ReleaseValue(z, &key)
			if gerr != nil || !found || deref(z, inner).Type != types.TypeArray {
				continue
			}
			a = deref(z, inner)
		}
		if err := mergeContextOptions(z, h, a); err != nil {
			types.ReleaseValue(z, &h.options)
			return types.Value{}, err
		}
	}
	res := getZoneStreams(z).registerStream(h)
	res.Extra = ResourceKindContext
	return res, nil
}

// contextOrStream rend le contexte désigné, ou celui attaché au flux désigné ; nil pour un flux sans contexte.
func contextOrStream(z *zone.Zone, v types.Value, fn string) (*streamHandle, error) {
	if h, ok := lookupContext(z, v); ok {
		return h, nil
	}
	zs, h, err := requireStreamArg(z, v, fn, "1", "stream_or_context")
	if err != nil {
		return nil, err
	}
	if c, ok := zs.contextHandle(h.ctxID); ok {
		return c, nil
	}
	return nil, nil
}

func nativeStreamContextGetOptions(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("stream_context_get_options", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	h, err := contextOrStream(z, args[0], "stream_context_get_options")
	if err != nil {
		return types.Value{}, err
	}
	if h == nil {
		return types.NewArray(z)
	}
	return types.RetainValue(z, h.options), nil
}

func nativeStreamContextSetOption(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "stream_context_set_option"
	if err := arity(fn, len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	h, err := contextOrStream(z, args[0], fn)
	if err != nil {
		return types.Value{}, err
	}
	if h == nil {
		return types.NewBool(false), nil
	}
	second := deref(z, args[1])
	if second.Type == types.TypeArray {
		if len(args) > 2 && !deref(z, args[2]).IsNull() {
			return types.Value{}, ArgumentCountError{fn + "(): Argument #3 ($option_name) must be null when argument #2 ($wrapper_or_options) is an array"}
		}
		if err := mergeContextOptions(z, h, second); err != nil {
			return types.Value{}, err
		}
		return types.NewBool(true), nil
	}
	if len(args) < 3 || deref(z, args[2]).IsNull() {
		return types.Value{}, ValueError{fn + "(): Argument #3 ($option_name) cannot be null when argument #2 ($wrapper_or_options) is a string"}
	}
	wrapper, err := asStringParam(z, args[1], fn, "2", "wrapper_or_options")
	if err != nil {
		return types.Value{}, err
	}
	option, err := asStringParam(z, args[2], fn, "3", "option_name")
	if err != nil {
		return types.Value{}, err
	}
	val := types.NewNull()
	if len(args) == 4 {
		val = args[3]
	}
	wk, err := newStr(z, wrapper)
	if err != nil {
		return types.Value{}, err
	}
	defer types.ReleaseValue(z, &wk)
	ok, err := newStr(z, option)
	if err != nil {
		return types.Value{}, err
	}
	defer types.ReleaseValue(z, &ok)
	if err := setContextOption(z, h, wk, ok, val); err != nil {
		return types.Value{}, err
	}
	return types.NewBool(true), nil
}

// builtinWrappers est la liste des enveloppes de flux annoncées par stream_get_wrappers ; http et https
// en sont absents tant que openStream ne les ouvre pas.
var builtinWrappers = [...]string{"php", "file", "glob", "data"}

// reservedWrappers sont les protocoles natifs que stream_wrapper_register refuse de redéfinir.
var reservedWrappers = [...]string{"file", "php", "data", "http", "https", "ftp", "zlib"}

// validProtocol accepte un nom non vide composé de [a-zA-Z0-9+.-].
func validProtocol(p string) bool {
	if p == "" {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '+' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

func nativeStreamContextSetParams(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "stream_context_set_params"
	if err := arity(fn, len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	first := deref(z, args[0])
	if first.Type != types.TypeResource {
		return types.Value{}, typeErr(fn, "1", "context", "resource", typeKind(z, first))
	}
	params := deref(z, args[1])
	if params.Type != types.TypeArray {
		return types.Value{}, typeErr(fn, "2", "params", "array", typeKind(z, params))
	}
	h, err := contextOrStream(z, args[0], fn)
	if err != nil {
		return types.Value{}, err
	}
	if h == nil {
		// Un flux sans contexte propre : PHP accepte l'appel, le paquet ne modélise pas son contexte.
		return types.NewBool(true), nil
	}
	var ferr error
	types.ArrayForEach(z, params, func(k, v types.Value) bool {
		if k.Type != types.TypeString {
			return true
		}
		v = deref(z, v)
		switch types.StringData(z, k) {
		case "notification":
			h.notification.release(z)
			h.notification.set = true
			if v.Type == types.TypeString {
				h.notification.isStr = true
				h.notification.str = strings.Clone(types.StringData(z, v))
			} else {
				h.notification.val = types.RetainValue(z, v)
			}
		case "options":
			if v.Type != types.TypeArray {
				ferr = TypeError{"Invalid stream/context parameter"}
				return false
			}
			ferr = mergeContextOptions(z, h, v)
			return ferr == nil
		}
		return true
	})
	if ferr != nil {
		return types.Value{}, ferr
	}
	return types.NewBool(true), nil
}

func nativeStreamContextGetParams(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "stream_context_get_params"
	if err := arity(fn, len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	first := deref(z, args[0])
	if first.Type != types.TypeResource {
		return types.Value{}, typeErr(fn, "1", "context", "resource", typeKind(z, first))
	}
	h, err := contextOrStream(z, args[0], fn)
	if err != nil {
		return types.Value{}, err
	}
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	set := func(key string, v types.Value) error {
		k, err := newStr(z, key)
		if err != nil {
			types.ReleaseValue(z, &v)
			return err
		}
		err = types.ArraySet(z, &arr, k, v, warnDrop)
		types.ReleaseValue(z, &k)
		types.ReleaseValue(z, &v)
		return err
	}
	if h != nil && h.notification.set {
		v, err := h.notification.value(z)
		if err == nil {
			err = set("notification", v)
		}
		if err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
	}
	var opts types.Value
	if h != nil {
		opts = types.RetainValue(z, h.options)
	} else if opts, err = types.NewArray(z); err != nil {
		types.ReleaseValue(z, &arr)
		return types.Value{}, err
	}
	if err := set("options", opts); err != nil {
		types.ReleaseValue(z, &arr)
		return types.Value{}, err
	}
	return arr, nil
}

func nativeStreamGetWrappers(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("stream_get_wrappers", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	zs.mu.Lock()
	names := builtinWrappers[:len(builtinWrappers):len(builtinWrappers)]
	for _, w := range zs.userWrappers {
		names = append(names, w.proto)
	}
	zs.mu.Unlock()
	for _, w := range names {
		v, err := newStr(z, w)
		if err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
		err = types.ArrayAppend(z, &arr, v)
		types.ReleaseValue(z, &v)
		if err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
	}
	return arr, nil
}

// stream_wrapper_register consigne le protocole et sa classe. openStream aiguille toute URI de ce
// protocole vers le gestionnaire d'instance branché par SetWrapperOpener, ou la refuse avec un
// avertissement ; resolvePath ne la traduit jamais en chemin local.
func nativeStreamWrapperRegister(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("stream_wrapper_register", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	proto, err := asStringParam(z, args[0], "stream_wrapper_register", "1", "protocol")
	if err != nil {
		return types.Value{}, err
	}
	class, err := asStringParam(z, args[1], "stream_wrapper_register", "2", "class")
	if err != nil {
		return types.Value{}, err
	}
	refuse := func(msg string) (types.Value, error) {
		warn(out, "stream_wrapper_register()", msg)
		return types.NewBool(false), nil
	}
	if !validProtocol(proto) {
		return refuse("Invalid protocol scheme specified. Unable to register wrapper class " + class + " to " + proto + "://")
	}
	defined := func(names []string) bool {
		for _, w := range names {
			if strings.EqualFold(w, proto) {
				return true
			}
		}
		return false
	}
	zs := getZoneStreams(z)
	zs.mu.Lock()
	defer zs.mu.Unlock()
	if _, user := zs.findUserWrapperLocked(proto); user || defined(reservedWrappers[:]) || defined(builtinWrappers[:]) {
		return refuse("Protocol " + proto + ":// is already defined.")
	}
	// asStringParam rend déjà des copies ; le clonage explicite garde l'invariant si l'extraction change.
	zs.userWrappers = append(zs.userWrappers, userWrapper{proto: strings.Clone(proto), class: strings.Clone(class)})
	return types.NewBool(true), nil
}

func nativeStreamSetTimeout(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("stream_set_timeout", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "stream_set_timeout", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	sec, err := asIntParam(z, args[1], "stream_set_timeout", "2", "seconds")
	if err != nil {
		return types.Value{}, err
	}
	usec := int64(0)
	if len(args) == 3 {
		if usec, err = asIntParam(z, args[2], "stream_set_timeout", "3", "microseconds"); err != nil {
			return types.Value{}, err
		}
	}
	if h.conn == nil {
		return types.NewBool(false), nil
	}
	h.timeout = time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond
	return types.NewBool(true), nil
}

func nativeStreamSetBlocking(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("stream_set_blocking", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "stream_set_blocking", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	// Seules les sockets bloquent : sur un fichier ou un flux mémoire, le mode reste sans objet comme en PHP.
	if h.kind == backendSocket {
		h.nonBlocking = !asBool(z, args[1])
	}
	return types.NewBool(true), nil
}

func nativeStreamSetBuffer(fn string, result int64) func(*zone.Zone, io.Writer, []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(fn, len(args), 2, 2); err != nil {
			return types.Value{}, err
		}
		if _, _, err := requireStreamArg(z, args[0], fn, "1", "stream"); err != nil {
			return types.Value{}, err
		}
		if _, err := asIntParam(z, args[1], fn, "2", "size"); err != nil {
			return types.Value{}, err
		}
		return types.NewLong(result), nil
	}
}

// dialError traduit une erreur Go dans le couple (errno, message) que PHP renvoie par référence.
func dialError(host string, err error) (int64, string) {
	if de, ok := errors.AsType[*net.DNSError](err); ok {
		msg := de.Err
		if de.IsNotFound {
			msg = "Name or service not known"
		}
		return 0, "php_network_getaddresses: getaddrinfo for " + host + " failed: " + msg
	}
	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
		return int64(syscall.ETIMEDOUT), "Connection timed out"
	}
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		msg := errno.Error()
		if msg != "" {
			msg = strings.ToUpper(msg[:1]) + msg[1:]
		}
		return int64(errno), msg
	}
	return 0, err.Error()
}

// dialStream ouvre une socket cliente « transport://hôte:port » et la présente comme un flux ; opts porte
// l'adresse locale (socket.bindto) et la politique TLS (options « ssl ») lues dans le contexte.
func dialStream(address string, timeout time.Duration, opts dialOptions) (*streamHandle, int64, string) {
	// L'adresse peut être une vue sur l'arène : le descripteur en retient une copie.
	address = strings.Clone(address)
	scheme, rest, hasScheme := strings.Cut(address, "://")
	if !hasScheme {
		scheme, rest = "tcp", address
	}
	scheme = strings.ToLower(scheme)
	network, streamType, useTLS := "tcp", "tcp_socket/ssl", false
	var schemeMethod int64
	switch scheme {
	case "tcp":
	case "udp":
		network, streamType = "udp", "udp_socket"
	case "unix":
		network, streamType = "unix", "unix_socket"
	case "udg":
		network, streamType = "unixgram", "udg_socket"
	case "ssl", "tls":
		useTLS = true
	case "tlsv1.0", "tlsv1.1", "tlsv1.2", "tlsv1.3":
		useTLS = true
		schemeMethod = map[string]int64{"tlsv1.0": 9, "tlsv1.1": 17, "tlsv1.2": 33, "tlsv1.3": 65}[scheme]
	default:
		return nil, 0, `Unable to find the socket transport "` + scheme + `" - did you forget to enable it when you configured PHP?`
	}
	host := rest
	if network == "tcp" || network == "udp" {
		h, _, err := net.SplitHostPort(rest)
		if err != nil {
			return nil, 0, `Failed to parse address "` + rest + `"`
		}
		host = h
	}
	dialer := &net.Dialer{Timeout: timeout}
	if opts.bindto != "" && (network == "tcp" || network == "udp") {
		local, err := net.ResolveTCPAddr("tcp", opts.bindto)
		if err != nil {
			return nil, 0, `Failed to parse address "` + opts.bindto + `"`
		}
		if network == "udp" {
			dialer.LocalAddr = &net.UDPAddr{IP: local.IP, Port: local.Port, Zone: local.Zone}
		} else {
			dialer.LocalAddr = local
		}
	}
	var conn net.Conn
	var err error
	if useTLS {
		p := tlsPolicy{verifyPeer: true, verifyPeerName: true, sni: true}
		if opts.tls != nil {
			p = *opts.tls
		}
		if p.peerName == "" {
			p.peerName = host
		}
		if p.method == 0 {
			p.method = schemeMethod
		}
		cfg, cerr := p.config()
		if cerr != nil {
			return nil, 0, cerr.Error()
		}
		if opts.asyncConnect {
			rawConn, aerr := dialAsyncTCP(rest, opts.bindto)
			if aerr != nil {
				err = aerr
			} else {
				conn = tls.Client(rawConn, cfg)
			}
		} else {
			conn, err = tls.DialWithDialer(dialer, "tcp", rest, cfg)
		}
	} else if opts.asyncConnect && network == "tcp" {
		conn, err = dialAsyncTCP(rest, opts.bindto)
	} else {
		conn, err = dialer.Dial(network, rest)
	}
	if err != nil {
		code, msg := dialError(host, err)
		return nil, code, msg
	}
	uri := rest
	if hasScheme {
		uri = scheme + "://" + rest
	}
	return &streamHandle{
		kind:        backendSocket,
		uri:         uri,
		mode:        "r+",
		streamType:  streamType,
		readable:    true,
		writable:    true,
		conn:        conn,
		rd:          bufio.NewReader(conn),
		timeout:     defaultSocketTimeout,
		host:        host,
		tlsOn:       useTLS,
		nonBlocking: opts.asyncConnect,
	}, 0, ""
}

func socketTimeout(z *zone.Zone, args []types.Value, i int, fn string) (time.Duration, error) {
	if len(args) <= i || deref(z, args[i]).IsNull() {
		return defaultSocketTimeout, nil
	}
	f, err := asFloatParam(z, args[i], fn, strconv.Itoa(i+1), "timeout")
	if err != nil {
		return 0, err
	}
	if f <= 0 {
		return defaultSocketTimeout, nil
	}
	return time.Duration(f * float64(time.Second)), nil
}

// openSocket ouvre la socket et l'enregistre ; ctx, s'il est non nil, est attaché au flux rendu. Une clé
// pkey non vide rend la socket persistante de la Zone si elle est encore utilisable (le même flux si elle
// est en service), sinon la connexion ouverte y est inscrite. STREAM_CLIENT_ASYNC_CONNECT dans flags initie
// un connect(2) non bloquant et rend immédiatement le flux en mode non bloquant.
func openSocket(z *zone.Zone, args []types.Value, address string, errIdx int, timeout time.Duration, ctx *streamHandle, flags int64, pkey string) types.Value {
	zs := getZoneStreams(z)
	if pkey != "" {
		if h, ok := zs.takePersistent(pkey); ok {
			assignRefArg(z, args, errIdx, types.NewLong(0))
			assignRefStr(z, args, errIdx+1, "")
			if flags&STREAM_CLIENT_ASYNC_CONNECT != 0 {
				h.nonBlocking = true
			}
			if h.slotID != 0 {
				return NewResource(zs.zone, ResourceKindStream, h.slotID)
			}
			h.ctxID = 0
			if ctx != nil {
				h.ctxID = ctx.slotID
			}
			return zs.registerStream(h)
		}
	}
	opts := readDialOptions(z, zs, ctx)
	opts.asyncConnect = flags&STREAM_CLIENT_ASYNC_CONNECT != 0
	h, code, msg := dialStream(address, timeout, opts)
	assignRefArg(z, args, errIdx, types.NewLong(code))
	assignRefStr(z, args, errIdx+1, msg)
	if h == nil {
		return types.NewBool(false)
	}
	if ctx != nil {
		h.ctxID = ctx.slotID
	}
	if pkey != "" {
		zs.keepPersistent(pkey, h)
	}
	return zs.registerStream(h)
}

// nativeSockopen sert fsockopen et pfsockopen : sans réserve de connexions entre requêtes, la variante
// persistante garde sa connexion TCP ou UNIX dans la Zone au-delà de fclose, sous la clé PHP
// « pfsockopen__hôte:port », et ne la ferme qu'au Reset.
func nativeSockopen(fn string) func(*zone.Zone, io.Writer, []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		return sockopen(z, fn, args)
	}
}

func sockopen(z *zone.Zone, fn string, args []types.Value) (types.Value, error) {
	if err := arity(fn, len(args), 1, 5); err != nil {
		return types.Value{}, err
	}
	host, err := asStringParam(z, args[0], fn, "1", "hostname")
	if err != nil {
		return types.Value{}, err
	}
	port := int64(-1)
	if len(args) >= 2 {
		if port, err = asIntParam(z, args[1], fn, "2", "port"); err != nil {
			return types.Value{}, err
		}
	}
	timeout, err := socketTimeout(z, args, 4, fn)
	if err != nil {
		return types.Value{}, err
	}
	if host == "" {
		assignRefArg(z, args, 2, types.NewLong(0))
		assignRefStr(z, args, 3, "")
		return types.NewBool(false), nil
	}
	address := host
	if port > 0 && !strings.HasPrefix(strings.ToLower(host), "unix://") && !strings.HasPrefix(strings.ToLower(host), "udg://") {
		address = host + ":" + strconv.FormatInt(port, 10)
	}
	pkey := ""
	if fn == "pfsockopen" {
		// La concaténation produit une chaîne Go neuve : la clé ne référence pas l'arène.
		pkey = "pfsockopen__" + host + ":" + strconv.FormatInt(port, 10)
	}
	return openSocket(z, args, address, 2, timeout, nil, STREAM_CLIENT_CONNECT, pkey), nil
}

// nativeStreamSocketClient applique ses drapeaux et son contexte : STREAM_CLIENT_CONNECT ou ASYNC_CONNECT est
// exigé (une socket non connectée ne serait d'aucun usage ici), ASYNC_CONNECT connecte de façon synchrone puis
// rend la socket non bloquante, PERSISTENT garde la connexion dans la Zone au-delà de fclose, sous la clé PHP
// « stream_socket_client__adresse » ; le contexte fournit socket.bindto et les options « ssl », puis reste
// attaché au flux pour stream_context_get_options et stream_socket_enable_crypto.
func nativeStreamSocketClient(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	const fn = "stream_socket_client"
	if err := arity(fn, len(args), 1, 6); err != nil {
		return types.Value{}, err
	}
	address, err := asStringParam(z, args[0], fn, "1", "address")
	if err != nil {
		return types.Value{}, err
	}
	timeout, err := socketTimeout(z, args, 3, fn)
	if err != nil {
		return types.Value{}, err
	}
	flags := int64(STREAM_CLIENT_CONNECT)
	if len(args) >= 5 {
		if flags, err = asIntParam(z, args[4], fn, "5", "flags"); err != nil {
			return types.Value{}, err
		}
		if flags&^(STREAM_CLIENT_CONNECT|STREAM_CLIENT_ASYNC_CONNECT|STREAM_CLIENT_PERSISTENT) != 0 {
			return types.Value{}, ValueError{fn + "(): Argument #5 ($flags) must be a combination of STREAM_CLIENT_CONNECT, STREAM_CLIENT_ASYNC_CONNECT and STREAM_CLIENT_PERSISTENT"}
		}
	}
	var ctx *streamHandle
	if len(args) == 6 && !deref(z, args[5]).IsNull() {
		c, ok := lookupContext(z, args[5])
		if !ok {
			return types.Value{}, typeErr(fn, "6", "context", "resource", typeKind(z, deref(z, args[5])))
		}
		ctx = c
	}
	if flags&(STREAM_CLIENT_CONNECT|STREAM_CLIENT_ASYNC_CONNECT) == 0 {
		msg := "Unable to connect to " + address + " (STREAM_CLIENT_CONNECT or STREAM_CLIENT_ASYNC_CONNECT is required: unconnected client sockets are not supported)"
		warn(out, fn+"()", msg)
		assignRefArg(z, args, 1, types.NewLong(0))
		assignRefStr(z, args, 2, msg)
		return types.NewBool(false), nil
	}
	pkey := ""
	if flags&STREAM_CLIENT_PERSISTENT != 0 {
		pkey = "stream_socket_client__" + address
	}
	return openSocket(z, args, address, 1, timeout, ctx, flags, pkey), nil
}

func asFloatParam(z *zone.Zone, v types.Value, fn, idx, param string) (float64, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeDouble:
		return v.ToDouble(), nil
	case types.TypeLong, types.TypeTrue, types.TypeFalse, types.TypeNull, types.TypeUndef:
		return float64(v.ToLong()), nil
	case types.TypeString:
		f, err := strconv.ParseFloat(strings.TrimSpace(types.StringData(z, v)), 64)
		if err != nil {
			return 0, typeErr(fn, idx, param, "?float", "string")
		}
		return f, nil
	default:
		return 0, typeErr(fn, idx, param, "?float", typeKind(z, v))
	}
}
