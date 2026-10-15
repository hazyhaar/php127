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
	{"chgrp", nativeChown("chgrp", true)},
	{"chmod", nativeChmod},
	{"chown", nativeChown("chown", false)},
	{"clearstatcache", nativeClearstatcache},
	{"closedir", nativeClosedir},
	{"copy", nativeCopy},
	{"dirname", nativeDirname},
	{"disk_free_space", nativeDiskSpace("disk_free_space", false)},
	{"disk_total_space", nativeDiskSpace("disk_total_space", true)},
	{"diskfreespace", nativeDiskSpace("diskfreespace", false)},
	{"fclose", nativeFclose("fclose")},
	{"feof", nativeFeof("feof")},
	{"fflush", nativeFflush},
	{"fgetc", nativeFgetc},
	{"fgetcsv", nativeFgetcsv},
	{"fgets", nativeFgets("fgets")},
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
	{"flock", nativeFlock},
	{"fopen", nativeFopen},
	{"fpassthru", nativeFpassthru("fpassthru")},
	{"fputcsv", nativeFputcsv},
	{"fputs", nativeFwrite("fputs")},
	{"fread", nativeFread("fread")},
	{"fseek", nativeFseek("fseek")},
	{"fsockopen", nativeSockopen("fsockopen")},
	{"fstat", nativeFstat},
	{"ftell", nativeFtell("ftell")},
	{"ftruncate", nativeFtruncate},
	{"fwrite", nativeFwrite("fwrite")},
	{"getcwd", nativeGetcwd},
	{"getimagesize", nativeGetimagesize},
	{"glob", nativeGlob},
	{"gzclose", nativeFclose("gzclose")},
	{"gzeof", nativeFeof("gzeof")},
	{"gzgets", nativeFgets("gzgets")},
	{"gzopen", nativeGzopen},
	{"gzpassthru", nativeFpassthru("gzpassthru")},
	{"gzputs", nativeFwrite("gzputs")},
	{"gzread", nativeFread("gzread")},
	{"gzseek", nativeFseek("gzseek")},
	{"gztell", nativeFtell("gztell")},
	{"gzwrite", nativeFwrite("gzwrite")},
	{"is_dir", nativeIsDir},
	{"is_file", nativeIsFile},
	{"is_link", nativeIsLink},
	{"is_readable", nativeIsReadable},
	{"is_writable", nativeIsWritable("is_writable")},
	{"is_writeable", nativeIsWritable("is_writeable")},
	{"link", nativeLink},
	{"lstat", nativeStat(true)},
	{"mkdir", nativeMkdir},
	{"opendir", nativeOpendir},
	{"parse_ini_file", nativeParseIniFile},
	{"pathinfo", nativePathinfo},
	{"pfsockopen", nativeSockopen("pfsockopen")},
	{"readdir", nativeReaddir},
	{"readfile", nativeReadfile},
	{"readlink", nativeReadlink},
	{"realpath", nativeRealpath},
	{"rename", nativeRename},
	{"rewind", nativeRewind},
	{"rewinddir", nativeRewinddir},
	{"rmdir", nativeRmdir},
	{"scandir", nativeScandir},
	{"socket_get_status", nativeStreamGetMetaData("socket_get_status")},
	{"stat", nativeStat(false)},
	{"stream_context_create", nativeStreamContextCreate},
	{"stream_context_get_options", nativeStreamContextGetOptions},
	{"stream_context_get_params", nativeStreamContextGetParams},
	{"stream_context_set_option", nativeStreamContextSetOption},
	{"stream_context_set_options", nativeStreamContextSetOptions},
	{"stream_context_set_params", nativeStreamContextSetParams},
	{"stream_copy_to_stream", nativeStreamCopyToStream},
	{"stream_get_contents", nativeStreamGetContents},
	{"stream_get_meta_data", nativeStreamGetMetaData("stream_get_meta_data")},
	{"stream_get_wrappers", nativeStreamGetWrappers},
	{"stream_register_wrapper", nativeStreamWrapperRegister},
	{"stream_select", nativeStreamSelect},
	{"stream_set_blocking", nativeStreamSetBlocking},
	{"stream_set_read_buffer", nativeStreamSetBuffer("stream_set_read_buffer", 0)},
	{"stream_set_timeout", nativeStreamSetTimeout},
	{"stream_set_write_buffer", nativeStreamSetBuffer("stream_set_write_buffer", -1)},
	{"stream_socket_client", nativeStreamSocketClient},
	{"stream_socket_enable_crypto", nativeStreamSocketEnableCrypto},
	{"stream_wrapper_register", nativeStreamWrapperRegister},
	{"symlink", nativeSymlink},
	{"sys_get_temp_dir", nativeSysGetTempDir},
	{"tempnam", nativeTempnam},
	{"touch", nativeTouch},
	{"umask", nativeUmask},
	{"unlink", nativeUnlink},
}
