package streams

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

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
)

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
}

func (h *streamHandle) closeOS() error {
	var err error
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
	if filepath.IsAbs(p) {
		return p
	}
	zs.mu.Lock()
	cwd := zs.cwd
	zs.mu.Unlock()
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

func openStream(zs *ZoneStreams, out io.Writer, filename, mode string) (*streamHandle, error) {
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

	resolved := zs.resolvePath(filename)
	f, err := os.OpenFile(resolved, flag, 0o666)
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
	zs := getZoneStreams(z)
	h, err := openStream(zs, out, filename, mode)
	if err != nil {
		if ve, ok := errors.AsType[ValueError](err); ok {
			return types.Value{}, ve
		}
		return types.NewBool(false), nil
	}
	return zs.registerStream(h), nil
}

func nativeFclose(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fclose", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	zs, h, err := requireStreamArg(z, args[0], "fclose", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewBool(zs.closeStream(h.slotID)), nil
}

func nativeFread(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fread", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "fread", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	length, err := asIntParam(z, args[1], "fread", "2", "length")
	if err != nil {
		return types.Value{}, err
	}
	if length <= 0 {
		return types.Value{}, ValueError{"fread(): Argument #2 ($length) must be greater than 0"}
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

func nativeFgets(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fgets", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "fgets", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	maxLen := -1
	if len(args) == 2 && !deref(z, args[1]).IsNull() {
		ln, err := asIntParam(z, args[1], "fgets", "2", "length")
		if err != nil {
			return types.Value{}, err
		}
		if ln <= 0 {
			return types.Value{}, ValueError{"fgets(): Argument #2 ($length) must be greater than 0"}
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

func nativeFeof(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("feof", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "feof", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewBool(h.eof), nil
}

func nativeFtell(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("ftell", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "ftell", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	if !h.seekable {
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

func nativeFseek(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fseek", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "fseek", "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	offset, err := asIntParam(z, args[1], "fseek", "2", "offset")
	if err != nil {
		return types.Value{}, err
	}
	whence := int64(SEEK_SET)
	if len(args) == 3 {
		whence, err = asIntParam(z, args[2], "fseek", "3", "whence")
		if err != nil {
			return types.Value{}, err
		}
	}
	return types.NewLong(int64(h.seek(offset, int(whence)))), nil
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
	return types.NewBool(!h.closed), nil
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

func nativeFpassthru(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fpassthru", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	zs, h, err := requireStreamArg(z, args[0], "fpassthru", "1", "stream")
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

func nativeStreamGetMetaData(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("stream_get_meta_data", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, h, err := requireStreamArg(z, args[0], "stream_get_meta_data", "1", "stream")
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
	if h.kind == backendData {
		setStr("mediatype", h.mediatype)
		_ = setArrayKey(z, &arr, "base64", types.NewBool(h.isBase64))
	} else {
		_ = setArrayKey(z, &arr, "timed_out", types.NewBool(false))
		_ = setArrayKey(z, &arr, "blocked", types.NewBool(true))
		_ = setArrayKey(z, &arr, "eof", types.NewBool(h.eof))
	}
	setStr("wrapper_type", h.wrapperType)
	setStr("stream_type", h.streamType)
	setStr("mode", h.mode)
	_ = setArrayKey(z, &arr, "unread_bytes", types.NewLong(0))
	_ = setArrayKey(z, &arr, "seekable", types.NewBool(h.seekable))
	setStr("uri", h.uri)
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
