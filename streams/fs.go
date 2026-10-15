package streams

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

func readSourceBytes(zs *ZoneStreams, out io.Writer, filename string) ([]byte, error) {
	if strings.HasPrefix(filename, "php://") || strings.HasPrefix(filename, "data://") || strings.HasPrefix(filename, "data:") {
		h, err := openStream(zs, out, filename, "rb")
		if err != nil {
			return nil, err
		}
		defer func() { _ = h.closeOS() }()
		return h.readAll()
	}
	resolved := zs.resolvePath(filename)
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, syscall.EISDIR
	}
	return os.ReadFile(resolved)
}

func nativeFileGetContents(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("file_get_contents", len(args), 1, 5); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "file_get_contents", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	offset := int64(0)
	if len(args) >= 4 && !deref(z, args[3]).IsNull() {
		offset, err = asIntParam(z, args[3], "file_get_contents", "4", "offset")
		if err != nil {
			return types.Value{}, err
		}
	}
	hasLen := false
	maxLen := int64(0)
	if len(args) == 5 && !deref(z, args[4]).IsNull() {
		maxLen, err = asIntParam(z, args[4], "file_get_contents", "5", "length")
		if err != nil {
			return types.Value{}, err
		}
		if maxLen < 0 {
			return types.Value{}, ValueError{"file_get_contents(): Argument #5 ($length) must be greater than or equal to 0"}
		}
		hasLen = true
	}
	zs := getZoneStreams(z)
	data, err := readSourceBytes(zs, out, filename)
	if err != nil {
		return types.NewBool(false), nil
	}
	size := int64(len(data))
	if offset < 0 {
		offset = size + offset
		if offset < 0 {
			return types.NewBool(false), nil
		}
	}
	if offset > size {
		return types.NewBool(false), nil
	}
	sub := data[offset:]
	if hasLen {
		if maxLen < int64(len(sub)) {
			sub = sub[:maxLen]
		}
	}
	return newStr(z, string(sub))
}

func nativeFilePutContents(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("file_put_contents", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "file_put_contents", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	flags := int64(0)
	if len(args) >= 3 && !deref(z, args[2]).IsNull() {
		flags, err = asIntParam(z, args[2], "file_put_contents", "3", "flags")
		if err != nil {
			return types.Value{}, err
		}
	}
	zs := getZoneStreams(z)
	dataVal := deref(z, args[1])
	var payload []byte
	switch dataVal.Type {
	case types.TypeArray:
		var b bytes.Buffer
		var convErr error
		types.ArrayForEach(z, dataVal, func(_, elem types.Value) bool {
			s, err := castString(z, elem)
			if err != nil {
				convErr = err
				return false
			}
			b.WriteString(s)
			return true
		})
		if convErr != nil {
			return types.Value{}, convErr
		}
		payload = b.Bytes()
	case types.TypeResource:
		_, sh, err := requireStreamArg(z, dataVal, "file_put_contents", "2", "data")
		if err != nil {
			return types.Value{}, err
		}
		payload, err = sh.readAll()
		if err != nil {
			return types.NewBool(false), nil
		}
	default:
		s, err := castString(z, dataVal)
		if err != nil {
			return types.Value{}, err
		}
		payload = []byte(s)
	}

	if strings.HasPrefix(filename, "php://") {
		h, err := openStream(zs, out, filename, "wb")
		if err != nil {
			return types.NewBool(false), nil
		}
		defer func() { _ = h.closeOS() }()
		n, err := h.writeBytes(payload)
		if err != nil {
			return types.NewBool(false), nil
		}
		return types.NewLong(int64(n)), nil
	}

	resolved := zs.resolvePath(filename)
	openFlags := os.O_WRONLY | os.O_CREATE
	if flags&FILE_APPEND != 0 {
		openFlags |= os.O_APPEND
	} else {
		openFlags |= os.O_TRUNC
	}
	f, err := os.OpenFile(resolved, openFlags, 0o666)
	if err != nil {
		return types.NewBool(false), nil
	}
	defer func() { _ = f.Close() }()
	if flags&LOCK_EX != 0 {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	}
	n, err := f.Write(payload)
	if err != nil {
		return types.NewBool(false), nil
	}
	return types.NewLong(int64(n)), nil
}

func nativeFile(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("file", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "file", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	flags := int64(0)
	if len(args) >= 2 && !deref(z, args[1]).IsNull() {
		flags, err = asIntParam(z, args[1], "file", "2", "flags")
		if err != nil {
			return types.Value{}, err
		}
	}
	zs := getZoneStreams(z)
	data, err := readSourceBytes(zs, out, filename)
	if err != nil {
		return types.NewBool(false), nil
	}
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	if len(data) == 0 {
		return arr, nil
	}
	ignoreNL := flags&FILE_IGNORE_NEW_LINES != 0
	skipEmpty := flags&FILE_SKIP_EMPTY_LINES != 0
	start := 0
	for i := range data {
		if data[i] == '\n' {
			line := string(data[start : i+1])
			start = i + 1
			if ignoreNL {
				line = strings.TrimSuffix(line, "\n")
				line = strings.TrimSuffix(line, "\r")
			}
			if skipEmpty && (line == "" || line == "\n" || line == "\r\n") {
				continue
			}
			sv, err := newStr(z, line)
			if err != nil {
				types.ReleaseValue(z, &arr)
				return types.Value{}, err
			}
			_ = types.ArrayAppend(z, &arr, sv)
			types.ReleaseValue(z, &sv)
		}
	}
	if start < len(data) {
		line := string(data[start:])
		if ignoreNL {
			line = strings.TrimSuffix(line, "\r")
		}
		if !(skipEmpty && line == "") {
			sv, err := newStr(z, line)
			if err != nil {
				types.ReleaseValue(z, &arr)
				return types.Value{}, err
			}
			_ = types.ArrayAppend(z, &arr, sv)
			types.ReleaseValue(z, &sv)
		}
	}
	return arr, nil
}

func nativeReadfile(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("readfile", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "readfile", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	data, err := readSourceBytes(zs, out, filename)
	if err != nil {
		return types.NewBool(false), nil
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

func nativeFileExists(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("file_exists", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "file_exists", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	if filename == "" {
		return types.NewBool(false), nil
	}
	zs := getZoneStreams(z)
	_, statErr := os.Stat(zs.resolvePath(filename))
	return types.NewBool(statErr == nil), nil
}

func nativeIsFile(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_file", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "is_file", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	if filename == "" {
		return types.NewBool(false), nil
	}
	zs := getZoneStreams(z)
	fi, statErr := os.Stat(zs.resolvePath(filename))
	return types.NewBool(statErr == nil && fi.Mode().IsRegular()), nil
}

func nativeIsDir(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_dir", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "is_dir", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	if filename == "" {
		return types.NewBool(false), nil
	}
	zs := getZoneStreams(z)
	fi, statErr := os.Stat(zs.resolvePath(filename))
	return types.NewBool(statErr == nil && fi.IsDir()), nil
}

func nativeIsLink(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_link", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "is_link", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	if filename == "" {
		return types.NewBool(false), nil
	}
	zs := getZoneStreams(z)
	fi, statErr := os.Lstat(zs.resolvePath(filename))
	return types.NewBool(statErr == nil && fi.Mode()&os.ModeSymlink != 0), nil
}

func nativeIsReadable(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("is_readable", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "is_readable", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	if filename == "" {
		return types.NewBool(false), nil
	}
	zs := getZoneStreams(z)
	resolved := zs.resolvePath(filename)
	return types.NewBool(syscall.Access(resolved, 0x4) == nil), nil
}

func nativeIsWritable(fnName string) func(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(fnName, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		filename, err := asStringParam(z, args[0], fnName, "1", "filename")
		if err != nil {
			return types.Value{}, err
		}
		if filename == "" {
			return types.NewBool(false), nil
		}
		zs := getZoneStreams(z)
		resolved := zs.resolvePath(filename)
		return types.NewBool(syscall.Access(resolved, 0x2) == nil), nil
	}
}

func nativeFilesize(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("filesize", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "filesize", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	fi, err := os.Stat(zs.resolvePath(filename))
	if err != nil {
		return types.NewBool(false), nil
	}
	return types.NewLong(fi.Size()), nil
}

func nativeFilemtime(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("filemtime", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "filemtime", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	fi, err := os.Stat(zs.resolvePath(filename))
	if err != nil {
		return types.NewBool(false), nil
	}
	return types.NewLong(fi.ModTime().Unix()), nil
}

func nativeFilectime(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("filectime", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "filectime", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	fi, err := os.Stat(zs.resolvePath(filename))
	if err != nil {
		return types.NewBool(false), nil
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st != nil {
		return types.NewLong(st.Ctim.Sec), nil
	}
	return types.NewLong(fi.ModTime().Unix()), nil
}

func nativeFileatime(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fileatime", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "fileatime", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	fi, err := os.Stat(zs.resolvePath(filename))
	if err != nil {
		return types.NewBool(false), nil
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st != nil {
		return types.NewLong(st.Atim.Sec), nil
	}
	return types.NewLong(fi.ModTime().Unix()), nil
}

func nativeFileperms(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("fileperms", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "fileperms", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	fi, err := os.Stat(zs.resolvePath(filename))
	if err != nil {
		return types.NewBool(false), nil
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st != nil {
		return types.NewLong(int64(st.Mode)), nil
	}
	return types.NewLong(int64(fi.Mode().Perm())), nil
}

func nativeFiletype(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("filetype", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "filetype", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	fi, err := os.Lstat(zs.resolvePath(filename))
	if err != nil {
		return types.NewBool(false), nil
	}
	m := fi.Mode()
	switch {
	case m&os.ModeSymlink != 0:
		return newStr(z, "link")
	case m.IsDir():
		return newStr(z, "dir")
	case m.IsRegular():
		return newStr(z, "file")
	case m&os.ModeNamedPipe != 0:
		return newStr(z, "fifo")
	case m&os.ModeCharDevice != 0:
		return newStr(z, "char")
	case m&os.ModeDevice != 0:
		return newStr(z, "block")
	case m&os.ModeSocket != 0:
		return newStr(z, "socket")
	default:
		return newStr(z, "unknown")
	}
}

func nativeStat(lstat bool) func(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	fnName := "stat"
	if lstat {
		fnName = "lstat"
	}
	return func(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
		if err := arity(fnName, len(args), 1, 1); err != nil {
			return types.Value{}, err
		}
		filename, err := asStringParam(z, args[0], fnName, "1", "filename")
		if err != nil {
			return types.Value{}, err
		}
		zs := getZoneStreams(z)
		var fi os.FileInfo
		if lstat {
			fi, err = os.Lstat(zs.resolvePath(filename))
		} else {
			fi, err = os.Stat(zs.resolvePath(filename))
		}
		if err != nil {
			return types.NewBool(false), nil
		}
		return buildStatArray(z, fi)
	}
}

func nativeUnlink(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("unlink", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "unlink", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	resolved := zs.resolvePath(filename)
	fi, err := os.Lstat(resolved)
	if err != nil || fi.IsDir() {
		return types.NewBool(false), nil
	}
	return types.NewBool(os.Remove(resolved) == nil), nil
}

func nativeRename(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("rename", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	from, err := asStringParam(z, args[0], "rename", "1", "from")
	if err != nil {
		return types.Value{}, err
	}
	to, err := asStringParam(z, args[1], "rename", "2", "to")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	return types.NewBool(os.Rename(zs.resolvePath(from), zs.resolvePath(to)) == nil), nil
}

func nativeCopy(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("copy", len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	from, err := asStringParam(z, args[0], "copy", "1", "from")
	if err != nil {
		return types.Value{}, err
	}
	to, err := asStringParam(z, args[1], "copy", "2", "to")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	data, err := readSourceBytes(zs, out, from)
	if err != nil {
		return types.NewBool(false), nil
	}
	return types.NewBool(os.WriteFile(zs.resolvePath(to), data, 0o666) == nil), nil
}

func nativeMkdir(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mkdir", len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	dir, err := asStringParam(z, args[0], "mkdir", "1", "directory")
	if err != nil {
		return types.Value{}, err
	}
	perm := int64(0o777)
	if len(args) >= 2 && !deref(z, args[1]).IsNull() {
		perm, err = asIntParam(z, args[1], "mkdir", "2", "permissions")
		if err != nil {
			return types.Value{}, err
		}
	}
	recursive := false
	if len(args) >= 3 {
		recursive = asBool(z, args[2])
	}
	zs := getZoneStreams(z)
	resolved := zs.resolvePath(dir)
	if recursive {
		if fi, statErr := os.Stat(resolved); statErr == nil && fi.IsDir() {
			return types.NewBool(false), nil
		}
		return types.NewBool(os.MkdirAll(resolved, os.FileMode(perm)&0o777) == nil), nil
	}
	return types.NewBool(os.Mkdir(resolved, os.FileMode(perm)&0o777) == nil), nil
}

func nativeRmdir(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("rmdir", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	dir, err := asStringParam(z, args[0], "rmdir", "1", "directory")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	resolved := zs.resolvePath(dir)
	fi, err := os.Lstat(resolved)
	if err != nil || !fi.IsDir() {
		return types.NewBool(false), nil
	}
	return types.NewBool(os.Remove(resolved) == nil), nil
}

func nativeScandir(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("scandir", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	dir, err := asStringParam(z, args[0], "scandir", "1", "directory")
	if err != nil {
		return types.Value{}, err
	}
	order := int64(SCANDIR_SORT_ASCENDING)
	if len(args) >= 2 && !deref(z, args[1]).IsNull() {
		order, err = asIntParam(z, args[1], "scandir", "2", "sorting_order")
		if err != nil {
			return types.Value{}, err
		}
	}
	if dir == "" {
		return types.Value{}, ValueError{"scandir(): Argument #1 ($directory) cannot be empty"}
	}
	zs := getZoneStreams(z)
	resolved := zs.resolvePath(dir)
	f, err := os.Open(resolved)
	if err != nil {
		return types.NewBool(false), nil
	}
	names, err := f.Readdirnames(-1)
	_ = f.Close()
	if err != nil {
		return types.NewBool(false), nil
	}
	entries := make([]string, 0, len(names)+2)
	entries = append(entries, ".", "..")
	entries = append(entries, names...)
	switch order {
	case SCANDIR_SORT_ASCENDING:
		slices.Sort(entries)
	case SCANDIR_SORT_DESCENDING:
		slices.SortFunc(entries, func(a, b string) int {
			return strings.Compare(b, a)
		})
	case SCANDIR_SORT_NONE:
	default:
		slices.Sort(entries)
	}
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, name := range entries {
		sv, err := newStr(z, name)
		if err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
		_ = types.ArrayAppend(z, &arr, sv)
		types.ReleaseValue(z, &sv)
	}
	return arr, nil
}

func expandBraces(pattern string) []string {
	open := strings.IndexByte(pattern, '{')
	if open < 0 {
		return []string{pattern}
	}
	depth := 0
	closeIdx := -1
	for i := open; i < len(pattern); i++ {
		if pattern[i] == '{' {
			depth++
		} else if pattern[i] == '}' {
			depth--
			if depth == 0 {
				closeIdx = i
				break
			}
		}
	}
	if closeIdx < 0 {
		return []string{pattern}
	}
	prefix := pattern[:open]
	suffix := pattern[closeIdx+1:]
	body := pattern[open+1 : closeIdx]
	var parts []string
	start := 0
	d := 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{':
			d++
		case '}':
			d--
		case ',':
			if d == 0 {
				parts = append(parts, body[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, body[start:])
	var out []string
	for _, part := range parts {
		out = append(out, expandBraces(prefix+part+suffix)...)
	}
	return out
}

func nativeGlob(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("glob", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	pattern, err := asStringParam(z, args[0], "glob", "1", "pattern")
	if err != nil {
		return types.Value{}, err
	}
	flags := int64(0)
	if len(args) == 2 && !deref(z, args[1]).IsNull() {
		flags, err = asIntParam(z, args[1], "glob", "2", "flags")
		if err != nil {
			return types.Value{}, err
		}
	}
	zs := getZoneStreams(z)
	patterns := []string{pattern}
	if flags&GLOB_BRACE != 0 {
		patterns = expandBraces(pattern)
	}
	var matches []string
	for _, pat := range patterns {
		isRel := !filepath.IsAbs(pat)
		searchPat := pat
		baseDir := ""
		if isRel {
			zs.mu.Lock()
			baseDir = zs.cwd
			zs.mu.Unlock()
			if baseDir != "" {
				searchPat = filepath.Join(baseDir, pat)
			}
		}
		m, globErr := filepath.Glob(searchPat)
		if globErr != nil {
			return types.NewBool(false), nil
		}
		for _, item := range m {
			if flags&GLOB_ONLYDIR != 0 {
				fi, statErr := os.Stat(item)
				if statErr != nil || !fi.IsDir() {
					continue
				}
			}
			outItem := item
			if isRel && baseDir != "" {
				if rel, relErr := filepath.Rel(baseDir, item); relErr == nil {
					if strings.HasPrefix(pat, "./") && !strings.HasPrefix(rel, "./") {
						rel = "./" + rel
					}
					outItem = rel
				}
			}
			if flags&GLOB_MARK != 0 {
				if fi, statErr := os.Stat(item); statErr == nil && fi.IsDir() && !strings.HasSuffix(outItem, "/") {
					outItem += "/"
				}
			}
			matches = append(matches, outItem)
		}
	}
	if flags&GLOB_NOSORT == 0 {
		slices.Sort(matches)
	}
	if len(matches) == 0 && flags&GLOB_NOCHECK != 0 {
		matches = append(matches, pattern)
	}
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, m := range matches {
		sv, err := newStr(z, m)
		if err != nil {
			types.ReleaseValue(z, &arr)
			return types.Value{}, err
		}
		_ = types.ArrayAppend(z, &arr, sv)
		types.ReleaseValue(z, &sv)
	}
	return arr, nil
}

func phpDirnameSingle(p string) string {
	if p == "" {
		return ""
	}
	end := len(p)
	for end > 1 && p[end-1] == '/' {
		end--
	}
	trimmed := p[:end]
	slash := strings.LastIndexByte(trimmed, '/')
	if slash < 0 {
		return "."
	}
	if slash == 0 {
		return "/"
	}
	for slash > 1 && trimmed[slash-1] == '/' {
		slash--
	}
	return trimmed[:slash]
}

func nativeDirname(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("dirname", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	p, err := asStringParam(z, args[0], "dirname", "1", "path")
	if err != nil {
		return types.Value{}, err
	}
	levels := int64(1)
	if len(args) == 2 {
		levels, err = asIntParam(z, args[1], "dirname", "2", "levels")
		if err != nil {
			return types.Value{}, err
		}
		if levels < 1 {
			return types.Value{}, ValueError{"dirname(): Argument #2 ($levels) must be greater than or equal to 1"}
		}
	}
	cur := p
	for i := int64(0); i < levels; i++ {
		cur = phpDirnameSingle(cur)
	}
	return newStr(z, cur)
}

func phpBasename(p, suffix string) string {
	if p == "" {
		return ""
	}
	end := len(p)
	for end > 0 && p[end-1] == '/' {
		end--
	}
	if end == 0 {
		return ""
	}
	trimmed := p[:end]
	slash := strings.LastIndexByte(trimmed, '/')
	base := trimmed
	if slash >= 0 {
		base = trimmed[slash+1:]
	}
	if suffix != "" && len(base) > len(suffix) && strings.HasSuffix(base, suffix) {
		base = base[:len(base)-len(suffix)]
	}
	return base
}

func nativeBasename(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("basename", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	p, err := asStringParam(z, args[0], "basename", "1", "path")
	if err != nil {
		return types.Value{}, err
	}
	suffix := ""
	if len(args) == 2 {
		suffix, err = asStringParam(z, args[1], "basename", "2", "suffix")
		if err != nil {
			return types.Value{}, err
		}
	}
	return newStr(z, phpBasename(p, suffix))
}

func nativePathinfo(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("pathinfo", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	p, err := asStringParam(z, args[0], "pathinfo", "1", "path")
	if err != nil {
		return types.Value{}, err
	}
	flags := int64(PATHINFO_ALL)
	if len(args) == 2 {
		flags, err = asIntParam(z, args[1], "pathinfo", "2", "flags")
		if err != nil {
			return types.Value{}, err
		}
	}
	dir := phpDirnameSingle(p)
	base := phpBasename(p, "")
	dot := strings.LastIndexByte(base, '.')
	hasExt := dot >= 0
	ext := ""
	fname := base
	if hasExt {
		ext = base[dot+1:]
		fname = base[:dot]
	}
	switch flags {
	case PATHINFO_DIRNAME:
		return newStr(z, dir)
	case PATHINFO_BASENAME:
		return newStr(z, base)
	case PATHINFO_EXTENSION:
		return newStr(z, ext)
	case PATHINFO_FILENAME:
		return newStr(z, fname)
	}
	arr, err := types.NewArray(z)
	if err != nil {
		return types.Value{}, err
	}
	setStr := func(k, v string) {
		sv, sErr := newStr(z, v)
		if sErr == nil {
			_ = setArrayKey(z, &arr, k, sv)
			types.ReleaseValue(z, &sv)
		}
	}
	if flags&PATHINFO_DIRNAME != 0 && p != "" {
		setStr("dirname", dir)
	}
	if flags&PATHINFO_BASENAME != 0 {
		setStr("basename", base)
	}
	if flags&PATHINFO_EXTENSION != 0 && hasExt {
		setStr("extension", ext)
	}
	if flags&PATHINFO_FILENAME != 0 {
		setStr("filename", fname)
	}
	return arr, nil
}

func nativeRealpath(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("realpath", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	p, err := asStringParam(z, args[0], "realpath", "1", "path")
	if err != nil {
		return types.Value{}, err
	}
	if p == "" {
		return types.NewBool(false), nil
	}
	zs := getZoneStreams(z)
	resolved := zs.resolvePath(p)
	real, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return types.NewBool(false), nil
	}
	abs, err := filepath.Abs(real)
	if err != nil {
		return types.NewBool(false), nil
	}
	if _, err := os.Stat(abs); err != nil {
		return types.NewBool(false), nil
	}
	return newStr(z, abs)
}

func nativeTempnam(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("tempnam", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	dir, err := asStringParam(z, args[0], "tempnam", "1", "directory")
	if err != nil {
		return types.Value{}, err
	}
	prefix, err := asStringParam(z, args[1], "tempnam", "2", "prefix")
	if err != nil {
		return types.Value{}, err
	}
	prefix = filepath.Base(prefix)
	if prefix == "." || prefix == "/" {
		prefix = ""
	}
	zs := getZoneStreams(z)
	targetDir := zs.resolvePath(dir)
	if fi, statErr := os.Stat(targetDir); statErr != nil || !fi.IsDir() {
		targetDir = os.TempDir()
	}
	f, err := os.CreateTemp(targetDir, prefix+"*")
	if err != nil {
		f, err = os.CreateTemp(os.TempDir(), prefix+"*")
		if err != nil {
			return types.NewBool(false), nil
		}
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Chmod(name, 0o600)
	return newStr(z, name)
}

func nativeSysGetTempDir(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("sys_get_temp_dir", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return newStr(z, os.TempDir())
}

func nativeGetcwd(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("getcwd", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	zs.mu.Lock()
	cwd := zs.cwd
	zs.mu.Unlock()
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return types.NewBool(false), nil
		}
	}
	return newStr(z, cwd)
}

func nativeChdir(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("chdir", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	dir, err := asStringParam(z, args[0], "chdir", "1", "directory")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	resolved := zs.resolvePath(dir)
	fi, err := os.Stat(resolved)
	if err != nil || !fi.IsDir() {
		return types.NewBool(false), nil
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return types.NewBool(false), nil
	}
	zs.mu.Lock()
	zs.cwd = abs
	zs.mu.Unlock()
	return types.NewBool(true), nil
}

func resolveDirArg(z *zone.Zone, args []types.Value, fn string) (*ZoneStreams, *dirHandle, error) {
	zs := getZoneStreams(z)
	var slotID uint32
	if len(args) == 0 || deref(z, args[0]).IsNull() {
		zs.mu.Lock()
		slotID = zs.lastDir
		zs.mu.Unlock()
		if slotID == 0 {
			return zs, nil, nil
		}
	} else {
		v := deref(z, args[0])
		if v.Type != types.TypeResource || v.Extra != ResourceKindDir {
			return nil, nil, typeErr(fn, "1", "dir_handle", "resource", typeKind(z, v))
		}
		slotID = v.DataSlot
	}
	zs.mu.Lock()
	d, ok := zs.dirs[slotID]
	zs.mu.Unlock()
	if !ok || d.closed {
		return nil, nil, TypeError{fn + "(): supplied resource is not a valid Directory resource"}
	}
	return zs, d, nil
}

func nativeOpendir(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("opendir", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	dir, err := asStringParam(z, args[0], "opendir", "1", "directory")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	resolved := zs.resolvePath(dir)
	f, err := os.Open(resolved)
	if err != nil {
		return types.NewBool(false), nil
	}
	names, err := f.Readdirnames(-1)
	_ = f.Close()
	if err != nil {
		return types.NewBool(false), nil
	}
	entries := make([]string, 0, len(names)+2)
	entries = append(entries, ".", "..")
	entries = append(entries, names...)
	d := &dirHandle{
		path:    resolved,
		entries: entries,
	}
	return zs.registerDir(d), nil
}

func nativeReaddir(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("readdir", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	_, d, err := resolveDirArg(z, args, "readdir")
	if err != nil {
		return types.Value{}, err
	}
	if d == nil || d.pos >= len(d.entries) {
		return types.NewBool(false), nil
	}
	entry := d.entries[d.pos]
	d.pos++
	return newStr(z, entry)
}

func nativeRewinddir(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("rewinddir", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	_, d, err := resolveDirArg(z, args, "rewinddir")
	if err != nil {
		return types.Value{}, err
	}
	if d != nil {
		d.pos = 0
	}
	return types.NewNull(), nil
}

func nativeClosedir(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("closedir", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	zs, d, err := resolveDirArg(z, args, "closedir")
	if err != nil {
		return types.Value{}, err
	}
	if d != nil {
		zs.closeDir(d.slotID)
	}
	return types.NewNull(), nil
}

func nativeTouch(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("touch", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "touch", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	now := time.Now()
	mtime := now
	if len(args) >= 2 && !deref(z, args[1]).IsNull() {
		ts, err := asIntParam(z, args[1], "touch", "2", "mtime")
		if err != nil {
			return types.Value{}, err
		}
		mtime = time.Unix(ts, 0)
	}
	atime := mtime
	if len(args) == 3 && !deref(z, args[2]).IsNull() {
		ts, err := asIntParam(z, args[2], "touch", "3", "atime")
		if err != nil {
			return types.Value{}, err
		}
		atime = time.Unix(ts, 0)
	}
	zs := getZoneStreams(z)
	resolved := zs.resolvePath(filename)
	if _, statErr := os.Stat(resolved); statErr != nil {
		f, createErr := os.OpenFile(resolved, os.O_CREATE|os.O_WRONLY, 0o666)
		if createErr != nil {
			return types.NewBool(false), nil
		}
		_ = f.Close()
	}
	if err := os.Chtimes(resolved, atime, mtime); err != nil {
		return types.NewBool(false), nil
	}
	return types.NewBool(true), nil
}

func nativeChmod(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("chmod", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], "chmod", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	perm, err := asIntParam(z, args[1], "chmod", "2", "permissions")
	if err != nil {
		return types.Value{}, err
	}
	zs := getZoneStreams(z)
	resolved := zs.resolvePath(filename)
	return types.NewBool(os.Chmod(resolved, os.FileMode(perm)&0o777) == nil), nil
}
