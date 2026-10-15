package streams

import (
	"code.hazyhaar.fr/devhoros/php127/stdlib"
)

type nativeEntry struct {
	name string
	fn   stdlib.NativeFunc
}

// Lookup recherche une fonction native de flux ou de fichier par son nom insensible à la casse ASCII.
func Lookup(name string) (stdlib.NativeFunc, bool) {
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

func init() {
	for i := 1; i < len(natives); i++ {
		if cmpFold(natives[i-1].name, natives[i].name) >= 0 {
			panic("streams: natives not sorted: " + natives[i].name)
		}
	}
}

var natives = [...]nativeEntry{
	{"basename", nativeBasename},
	{"chdir", nativeChdir},
	{"chmod", nativeChmod},
	{"closedir", nativeClosedir},
	{"copy", nativeCopy},
	{"dirname", nativeDirname},
	{"fclose", nativeFclose},
	{"feof", nativeFeof},
	{"fflush", nativeFflush},
	{"fgetc", nativeFgetc},
	{"fgetcsv", nativeFgetcsv},
	{"fgets", nativeFgets},
	{"file", nativeFile},
	{"file_exists", nativeFileExists},
	{"file_get_contents", nativeFileGetContents},
	{"file_put_contents", nativeFilePutContents},
	{"fileatime", nativeFileatime},
	{"filectime", nativeFilectime},
	{"filemtime", nativeFilemtime},
	{"fileperms", nativeFileperms},
	{"filesize", nativeFilesize},
	{"filetype", nativeFiletype},
	{"fopen", nativeFopen},
	{"fpassthru", nativeFpassthru},
	{"fputcsv", nativeFputcsv},
	{"fputs", nativeFwrite("fputs")},
	{"fread", nativeFread},
	{"fseek", nativeFseek},
	{"fstat", nativeFstat},
	{"ftell", nativeFtell},
	{"ftruncate", nativeFtruncate},
	{"fwrite", nativeFwrite("fwrite")},
	{"getcwd", nativeGetcwd},
	{"glob", nativeGlob},
	{"is_dir", nativeIsDir},
	{"is_file", nativeIsFile},
	{"is_link", nativeIsLink},
	{"is_readable", nativeIsReadable},
	{"is_writable", nativeIsWritable("is_writable")},
	{"is_writeable", nativeIsWritable("is_writeable")},
	{"lstat", nativeStat(true)},
	{"mkdir", nativeMkdir},
	{"opendir", nativeOpendir},
	{"pathinfo", nativePathinfo},
	{"readdir", nativeReaddir},
	{"readfile", nativeReadfile},
	{"realpath", nativeRealpath},
	{"rename", nativeRename},
	{"rewind", nativeRewind},
	{"rewinddir", nativeRewinddir},
	{"rmdir", nativeRmdir},
	{"scandir", nativeScandir},
	{"stat", nativeStat(false)},
	{"stream_copy_to_stream", nativeStreamCopyToStream},
	{"stream_get_contents", nativeStreamGetContents},
	{"stream_get_meta_data", nativeStreamGetMetaData},
	{"sys_get_temp_dir", nativeSysGetTempDir},
	{"tempnam", nativeTempnam},
	{"touch", nativeTouch},
	{"unlink", nativeUnlink},
}
