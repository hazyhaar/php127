package streams

import (
	"bufio"
	"compress/gzip"
	"errors"
	"io"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Flux gzip de gzopen : un *streamHandle ordinaire de ZoneStreams, dont src (lecture) ou sink
// (écriture) passe par compress/gzip au-dessus d'un fichier ouvert par openStream. fread, fwrite,
// fgets, feof, ftell, fseek et fclose le servent comme tout flux ; z.Reset le ferme par
// DispatchCleanup, ce qui écrit la fin du membre gzip d'un flux d'écriture resté ouvert.

// handleIO présente un descripteur ouvert par openStream comme lecteur, écrivain et fermeture Go.
type handleIO struct{ h *streamHandle }

func (u handleIO) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	data, err := u.h.readBytes(len(p))
	n := copy(p, data)
	if err != nil {
		return n, err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

func (u handleIO) Write(p []byte) (int, error) { return u.h.writeBytes(p) }
func (u handleIO) Close() error                { return u.h.closeOS() }

// gzipMagic ouvre un membre gzip ; un fichier sans cet en-tête est lu tel quel, comme zlib.
var gzipMagic = [2]byte{0x1f, 0x8b}

// gzSource rend le lecteur décompressé du fichier sous-jacent, positionné au début.
func gzSource(under *streamHandle) (*bufio.Reader, error) {
	br := bufio.NewReader(handleIO{under})
	if head, _ := br.Peek(2); len(head) == 2 && head[0] == gzipMagic[0] && head[1] == gzipMagic[1] {
		gr, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		return bufio.NewReader(gr), nil
	}
	return br, nil
}

// gzWriter tient le compresseur, le nombre d'octets non compressés écrits et le saut en attente :
// comme zlib, un gzseek en avant ne matérialise ses zéros qu'à l'écriture ou à la fermeture suivante,
// et un déplacement refusé annule le saut en attente.
type gzWriter struct {
	gw      *gzip.Writer
	written int64
	pending int64
}

func (g *gzWriter) flushPending() error {
	var zeros [512]byte
	for g.pending > 0 {
		n, err := g.gw.Write(zeros[:min(g.pending, int64(len(zeros)))])
		g.written += int64(n)
		g.pending -= int64(n)
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *gzWriter) Write(p []byte) (int, error) {
	if err := g.flushPending(); err != nil {
		return 0, err
	}
	n, err := g.gw.Write(p)
	g.written += int64(n)
	return n, err
}

func (g *gzWriter) Close() error {
	err := g.flushPending()
	if cerr := g.gw.Close(); err == nil {
		err = cerr
	}
	return err
}

// gzSeekTarget normalise un déplacement ; SEEK_END est refusé par zlib.
func gzSeekTarget(pos, offset int64, whence int) (int64, int) {
	switch whence {
	case SEEK_SET:
		return offset, 0
	case SEEK_CUR:
		return pos + offset, 0
	case SEEK_END:
		return 0, seekEndUnsupported
	default:
		return 0, -1
	}
}

// gzOpenMode décode le mode de gzopen : premier caractère r, w ou a, chiffre facultatif de niveau.
func gzOpenMode(mode string) (read bool, fmode string, level int, ok bool) {
	level = gzip.DefaultCompression
	if mode == "" {
		return false, "", 0, false
	}
	switch mode[0] {
	case 'r':
		read, fmode = true, "rb"
	case 'w':
		fmode = "wb"
	case 'a':
		fmode = "ab"
	default:
		return false, "", 0, false
	}
	for i := 1; i < len(mode); i++ {
		if c := mode[i]; '0' <= c && c <= '9' {
			level = int(c - '0')
		}
	}
	return read, fmode, level, true
}

func nativeGzopen(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	const fn = "gzopen"
	if err := arity(fn, len(args), 2, 3); err != nil {
		return types.Value{}, err
	}
	filename, err := asStringParam(z, args[0], fn, "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	mode, err := asStringParam(z, args[1], fn, "2", "mode")
	if err != nil {
		return types.Value{}, err
	}
	if len(args) == 3 {
		if _, err := asIntParam(z, args[2], fn, "3", "use_include_path"); err != nil {
			return types.Value{}, err
		}
	}
	if strings.ContainsRune(mode, '+') {
		warn(out, fn+"()", "Cannot open a zlib stream for reading and writing at the same time!")
		return types.NewBool(false), nil
	}
	read, fmode, level, ok := gzOpenMode(mode)
	if !ok {
		warn(out, fn+"("+filename+")", "Failed to open stream: `"+mode+"' is not a valid mode for fopen")
		return types.NewBool(false), nil
	}
	if filename == "" {
		return types.Value{}, ValueError{"Path cannot be empty"}
	}
	zs := getZoneStreams(z)
	under, err := openStream(zs, out, filename, fmode, 0)
	if err != nil {
		if _, user := errors.AsType[wrapperError](err); user {
			warnOpenFailure(out, fn, filename, err)
		} else {
			warn(out, fn+"("+filename+")", "Failed to open stream: "+errnoText(err))
		}
		return types.NewBool(false), nil
	}
	// zlib exige un descripteur de fichier : un flux mémoire, de données ou utilisateur est refusé comme par PHP.
	if under.kind != backendFile {
		_ = under.closeOS()
		warn(out, fn+"()", "Cannot represent a stream of type "+under.streamType+" as a File Descriptor")
		return types.NewBool(false), nil
	}
	h := &streamHandle{
		kind:       backendGzip,
		mode:       strings.Clone(mode),
		streamType: "ZLIB",
		readable:   read,
		writable:   !read,
		seekable:   true,
	}
	if read {
		src, err := gzSource(under)
		if err != nil {
			_ = under.closeOS()
			warn(out, fn+"("+filename+")", "Failed to open stream: "+err.Error())
			return types.NewBool(false), nil
		}
		h.src = src
		h.closers = []io.Closer{handleIO{under}}
		h.seekFn = func(offset int64, whence int) int {
			target, r := gzSeekTarget(h.pos, offset, whence)
			if r != 0 {
				return r
			}
			if target < 0 {
				h.pos = -1
				return -1
			}
			if target < h.pos {
				if under.seek(0, SEEK_SET) != 0 {
					return -1
				}
				src, err := gzSource(under)
				if err != nil {
					return -1
				}
				h.src, h.pos = src, 0
			}
			n, _ := io.CopyN(io.Discard, h.src, target-h.pos)
			// zlib rend la position demandée même au-delà de la fin ; la lecture suivante constate EOF.
			h.pos = max(h.pos+n, target)
			return 0
		}
	} else {
		gw, err := gzip.NewWriterLevel(handleIO{under}, level)
		if err != nil {
			_ = under.closeOS()
			return types.NewBool(false), nil
		}
		g := &gzWriter{gw: gw}
		h.sink = g
		h.closers = []io.Closer{g, handleIO{under}}
		h.seekFn = func(offset int64, whence int) int {
			var off int64
			switch whence {
			case SEEK_SET:
				off = offset - g.written
			case SEEK_CUR:
				off = offset + g.pending
			case SEEK_END:
				return seekEndUnsupported
			default:
				return -1
			}
			g.pending = 0
			if off < 0 {
				h.pos = -1
				return -1
			}
			g.pending = off
			h.pos = g.written + off
			return 0
		}
	}
	return zs.registerStream(h), nil
}
