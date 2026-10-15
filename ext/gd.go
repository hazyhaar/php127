package ext

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"code.hazyhaar.fr/devhoros/php127/streams"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

const (
	ResourceKindGD    uint16 = 0x4503
	ResourceKindFinfo uint16 = 0x4504
)

var gdLongConstants = map[string]int64{
	"IMG_GIF":                1,
	"IMG_JPG":                2,
	"IMG_JPEG":               2,
	"IMG_PNG":                4,
	"IMG_WBMP":               8,
	"IMG_XPM":                16,
	"IMG_WEBP":               32,
	"IMG_BMP":                64,
	"IMG_TGA":                128,
	"IMG_AVIF":               256,
	"IMG_GD2":                1024,
	"IMG_FLIP_HORIZONTAL":    1,
	"IMG_FLIP_VERTICAL":      2,
	"IMG_FLIP_BOTH":          3,
	"FILEINFO_NONE":          0,
	"FILEINFO_SYMLINK":       2,
	"FILEINFO_DEVICES":       8,
	"FILEINFO_CONTINUE":      32,
	"FILEINFO_PRESERVE_ATIME": 128,
	"FILEINFO_RAW":           256,
	"FILEINFO_MIME_TYPE":     16,
	"FILEINFO_MIME_ENCODING": 1024,
	"FILEINFO_MIME":          1040,
}

type gdImage struct {
	img           *image.NRGBA
	trueColor     bool
	palette       []color.NRGBA
	alphaBlending bool
	saveAlpha     bool
	interlace     bool
	transparent   int
}

type finfoHandle struct {
	flags  int64
	closed bool
}

func storeGDImage(z *zone.Zone, g *gdImage) types.Value {
	st := extStateFor(z)
	if st.handles == nil {
		st.handles = make(map[uint32]any)
	}
	id := st.newHandle()
	st.handles[id] = g
	return streams.NewResource(z, ResourceKindGD, id)
}

func gdImageArg(z *zone.Zone, v types.Value, fn, idx, param string) (uint32, *gdImage, error) {
	v = deref(z, v)
	if v.Type != types.TypeResource || v.Extra != ResourceKindGD {
		got := typeKind(z, v)
		if v.Type == types.TypeResource {
			got = "resource"
		}
		return 0, nil, typeErr(fn, idx, param, "GdImage", got)
	}
	st := extStateFor(z)
	if st.handles == nil {
		return 0, nil, TypeError{fn + "(): Argument #" + idx + " ($" + param + ") must be a valid GdImage"}
	}
	g, ok := st.handles[v.DataSlot].(*gdImage)
	if !ok || g == nil || g.img == nil {
		return 0, nil, TypeError{fn + "(): Argument #" + idx + " ($" + param + ") must be a valid GdImage"}
	}
	return v.DataSlot, g, nil
}

func HasGDImage(z *zone.Zone, slot uint32) bool {
	if z == nil {
		return false
	}
	st := extStateFor(z)
	if st.handles == nil {
		return false
	}
	g, ok := st.handles[slot].(*gdImage)
	return ok && g != nil && g.img != nil
}

func HasFinfo(z *zone.Zone, slot uint32) bool {
	if z == nil {
		return false
	}
	st := extStateFor(z)
	if st.handles == nil {
		return false
	}
	f, ok := st.handles[slot].(*finfoHandle)
	return ok && f != nil && !f.closed
}

func storeFinfo(z *zone.Zone, f *finfoHandle) types.Value {
	st := extStateFor(z)
	if st.handles == nil {
		st.handles = make(map[uint32]any)
	}
	id := st.newHandle()
	st.handles[id] = f
	return streams.NewResource(z, ResourceKindFinfo, id)
}

func finfoArg(z *zone.Zone, v types.Value, fn, idx, param string) (uint32, *finfoHandle, error) {
	v = deref(z, v)
	if v.Type != types.TypeResource || v.Extra != ResourceKindFinfo {
		got := typeKind(z, v)
		if v.Type == types.TypeResource {
			got = "resource"
		}
		return 0, nil, typeErr(fn, idx, param, "finfo", got)
	}
	st := extStateFor(z)
	if st.handles == nil {
		return 0, nil, TypeError{fn + "(): Argument #" + idx + " ($" + param + ") must be a valid finfo resource"}
	}
	f, ok := st.handles[v.DataSlot].(*finfoHandle)
	if !ok || f == nil || f.closed {
		return 0, nil, TypeError{fn + "(): Argument #" + idx + " ($" + param + ") must be a valid finfo resource"}
	}
	return v.DataSlot, f, nil
}

func gdColorInt(r, g, b, gdAlpha int64) int64 {
	return ((gdAlpha & 0x7f) << 24) | ((r & 0xff) << 16) | ((g & 0xff) << 8) | (b & 0xff)
}

func nrgbaFromGDColor(c int64) color.NRGBA {
	gdAlpha := (c >> 24) & 0x7f
	r := uint8((c >> 16) & 0xff)
	g := uint8((c >> 8) & 0xff)
	b := uint8(c & 0xff)
	a := uint8(255 - (gdAlpha*255)/127)
	return color.NRGBA{R: r, G: g, B: b, A: a}
}

func gdColorFromNRGBA(c color.NRGBA) int64 {
	gdAlpha := int64(((255-int(c.A))*127 + 127) / 255)
	return gdColorInt(int64(c.R), int64(c.G), int64(c.B), gdAlpha)
}

const (
	gdMaxAxis   = 8192
	gdMaxPixels = 16_777_216
)

func validGDDims(w, h int64) bool {
	return w > 0 && h > 0 && w <= gdMaxAxis && h <= gdMaxAxis && w*h <= gdMaxPixels
}

func newOpaqueNRGBA(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	return img
}

func toNRGBA(src image.Image) *image.NRGBA {
	if nrgba, ok := src.(*image.NRGBA); ok && nrgba.Bounds().Min == (image.Point{}) {
		return nrgba
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func cloneNRGBA(src *image.NRGBA) *image.NRGBA {
	dst := image.NewNRGBA(src.Bounds())
	copy(dst.Pix, src.Pix)
	return dst
}

func putGDPixel(g *gdImage, x, y int, src color.NRGBA) {
	b := g.img.Bounds()
	if x < 0 || y < 0 || x >= b.Dx() || y >= b.Dy() {
		return
	}
	if !g.alphaBlending || src.A == 255 {
		g.img.SetNRGBA(x, y, src)
		return
	}
	if src.A == 0 {
		return
	}
	dst := g.img.NRGBAAt(x, y)
	if dst.A == 0 {
		g.img.SetNRGBA(x, y, src)
		return
	}
	sa := float64(src.A) / 255.0
	da := float64(dst.A) / 255.0
	outA := sa + da*(1.0-sa)
	if outA <= 0 {
		g.img.SetNRGBA(x, y, color.NRGBA{})
		return
	}
	outR := (float64(src.R)*sa + float64(dst.R)*da*(1.0-sa)) / outA
	outG := (float64(src.G)*sa + float64(dst.G)*da*(1.0-sa)) / outA
	outB := (float64(src.B)*sa + float64(dst.B)*da*(1.0-sa)) / outA
	g.img.SetNRGBA(x, y, color.NRGBA{
		R: uint8(outR + 0.5),
		G: uint8(outG + 0.5),
		B: uint8(outB + 0.5),
		A: uint8(outA*255.0 + 0.5),
	})
}

func readGDFileBytes(z *zone.Zone, out io.Writer, filename string) ([]byte, bool) {
	if filename == "" {
		return nil, false
	}
	fgc, ok := streams.Lookup("file_get_contents")
	if !ok {
		return nil, false
	}
	pathVal, err := newStr(z, filename)
	if err != nil {
		return nil, false
	}
	got, err := fgc(z, out, []types.Value{pathVal})
	types.ReleaseValue(z, &pathVal)
	if err != nil {
		return nil, false
	}
	got = deref(z, got)
	if got.Type != types.TypeString {
		types.ReleaseValue(z, &got)
		return nil, false
	}
	data := []byte(types.StringData(z, got))
	types.ReleaseValue(z, &got)
	return data, true
}

func writeGDOutput(z *zone.Zone, out io.Writer, to types.Value, fn string, data []byte) (types.Value, error) {
	if to.IsUndef() || deref(z, to).IsNull() {
		if out != nil {
			if _, err := out.Write(data); err != nil {
				return types.NewBool(false), nil
			}
		}
		return types.NewBool(true), nil
	}
	to = deref(z, to)
	if to.Type == types.TypeResource {
		fw, ok := streams.Lookup("fwrite")
		if !ok {
			return types.NewBool(false), nil
		}
		sVal, err := newStr(z, string(data))
		if err != nil {
			return types.Value{}, err
		}
		res, err := fw(z, out, []types.Value{to, sVal})
		types.ReleaseValue(z, &sVal)
		if err != nil {
			return types.NewBool(false), err
		}
		res = deref(z, res)
		okWrite := res.Type == types.TypeLong && res.ToLong() == int64(len(data))
		types.ReleaseValue(z, &res)
		return types.NewBool(okWrite), nil
	}
	path, err := asString(z, to, fn, "2", "to")
	if err != nil {
		return types.Value{}, err
	}
	if path == "" {
		return types.NewBool(false), nil
	}
	fpc, ok := streams.Lookup("file_put_contents")
	if !ok {
		return types.NewBool(false), nil
	}
	pathVal, err := newStr(z, path)
	if err != nil {
		return types.Value{}, err
	}
	sVal, err := newStr(z, string(data))
	if err != nil {
		types.ReleaseValue(z, &pathVal)
		return types.Value{}, err
	}
	res, err := fpc(z, out, []types.Value{pathVal, sVal})
	types.ReleaseValue(z, &pathVal)
	types.ReleaseValue(z, &sVal)
	if err != nil {
		return types.NewBool(false), err
	}
	res = deref(z, res)
	okWrite := res.Type == types.TypeLong
	types.ReleaseValue(z, &res)
	return types.NewBool(okWrite), nil
}

func nativeGDInfo(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("gd_info", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	ver, err := newStr(z, "bundled (2.1.0 compatible)")
	if err != nil {
		types.ReleaseArray(z, &a)
		return types.Value{}, err
	}
	err = setStrKey(z, &a, "GD Version", ver)
	types.ReleaseValue(z, &ver)
	if err != nil {
		types.ReleaseArray(z, &a)
		return types.Value{}, err
	}
	type flagEntry struct {
		key string
		val bool
	}
	entries := [...]flagEntry{
		{"FreeType Support", false},
		{"GIF Read Support", true},
		{"GIF Create Support", true},
		{"JPEG Support", true},
		{"PNG Support", true},
		{"WBMP Support", false},
		{"XPM Support", false},
		{"XBM Support", false},
		{"WebP Support", false},
		{"BMP Support", false},
		{"TGA Read Support", false},
		{"AVIF Support", false},
		{"JIS-mapped Japanese Font Support", false},
	}
	for _, e := range entries {
		if err := setStrKey(z, &a, e.key, types.NewBool(e.val)); err != nil {
			types.ReleaseArray(z, &a)
			return types.Value{}, err
		}
	}
	return a, nil
}

func nativeImagetypes(_ *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagetypes", len(args), 0, 0); err != nil {
		return types.Value{}, err
	}
	return types.NewLong(1 | 2 | 4), nil
}

func nativeImagecreatefromstring(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagecreatefromstring", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[0], "imagecreatefromstring", "1", "data")
	if err != nil {
		return types.Value{}, err
	}
	if data == "" {
		return types.NewBool(false), nil
	}
	raw := []byte(data)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || !validGDDims(int64(cfg.Width), int64(cfg.Height)) {
		return types.NewBool(false), nil
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return types.NewBool(false), nil
	}
	b := img.Bounds()
	if !validGDDims(int64(b.Dx()), int64(b.Dy())) {
		return types.NewBool(false), nil
	}
	return storeGDImage(z, &gdImage{
		img:           toNRGBA(img),
		trueColor:     true,
		alphaBlending: true,
		transparent:   -1,
	}), nil
}

func nativeImagecreatefromjpeg(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	return decodeGDFile(z, out, args, "imagecreatefromjpeg", jpeg.DecodeConfig, jpeg.Decode)
}

func nativeImagecreatefrompng(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	return decodeGDFile(z, out, args, "imagecreatefrompng", png.DecodeConfig, png.Decode)
}

func nativeImagecreatefromgif(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	return decodeGDFile(z, out, args, "imagecreatefromgif", gif.DecodeConfig, gif.Decode)
}

func decodeGDFile(z *zone.Zone, out io.Writer, args []types.Value, fn string, decCfg func(io.Reader) (image.Config, error), dec func(io.Reader) (image.Image, error)) (types.Value, error) {
	if err := arity(fn, len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	filename, err := asString(z, args[0], fn, "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	data, ok := readGDFileBytes(z, out, filename)
	if !ok || len(data) == 0 {
		return types.NewBool(false), nil
	}
	cfg, err := decCfg(bytes.NewReader(data))
	if err != nil || !validGDDims(int64(cfg.Width), int64(cfg.Height)) {
		return types.NewBool(false), nil
	}
	img, err := dec(bytes.NewReader(data))
	if err != nil {
		return types.NewBool(false), nil
	}
	b := img.Bounds()
	if !validGDDims(int64(b.Dx()), int64(b.Dy())) {
		return types.NewBool(false), nil
	}
	return storeGDImage(z, &gdImage{
		img:           toNRGBA(img),
		trueColor:     true,
		alphaBlending: true,
		transparent:   -1,
	}), nil
}

func nativeImagecreatetruecolor(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagecreatetruecolor", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	w, err := asInt(z, args[0], "imagecreatetruecolor", "1", "width")
	if err != nil {
		return types.Value{}, err
	}
	h, err := asInt(z, args[1], "imagecreatetruecolor", "2", "height")
	if err != nil {
		return types.Value{}, err
	}
	if !validGDDims(w, h) {
		return types.NewBool(false), nil
	}
	return storeGDImage(z, &gdImage{
		img:           newOpaqueNRGBA(int(w), int(h)),
		trueColor:     true,
		alphaBlending: true,
		transparent:   -1,
	}), nil
}

func nativeImagecreate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagecreate", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	w, err := asInt(z, args[0], "imagecreate", "1", "width")
	if err != nil {
		return types.Value{}, err
	}
	h, err := asInt(z, args[1], "imagecreate", "2", "height")
	if err != nil {
		return types.Value{}, err
	}
	if !validGDDims(w, h) {
		return types.NewBool(false), nil
	}
	return storeGDImage(z, &gdImage{
		img:           newOpaqueNRGBA(int(w), int(h)),
		trueColor:     false,
		palette:       make([]color.NRGBA, 0, 256),
		alphaBlending: false,
		transparent:   -1,
	}), nil
}

func nativeImagesx(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagesx", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagesx", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(g.img.Bounds().Dx())), nil
}

func nativeImagesy(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagesy", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagesy", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewLong(int64(g.img.Bounds().Dy())), nil
}

func nativeImageistruecolor(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imageistruecolor", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imageistruecolor", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	return types.NewBool(g.trueColor), nil
}

func nativeImagealphablending(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagealphablending", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagealphablending", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	g.alphaBlending = asBool(z, args[1])
	return types.NewBool(true), nil
}

func nativeImagesavealpha(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagesavealpha", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagesavealpha", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	g.saveAlpha = asBool(z, args[1])
	return types.NewBool(true), nil
}

func nativeImageantialias(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imageantialias", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	if _, _, err := gdImageArg(z, args[0], "imageantialias", "1", "image"); err != nil {
		return types.Value{}, err
	}
	_ = asBool(z, args[1])
	return types.NewBool(true), nil
}

func nativeImageinterlace(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imageinterlace", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imageinterlace", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	if len(args) >= 2 && !missing(args[1]) {
		g.interlace = asBool(z, args[1])
	}
	if g.interlace {
		return types.NewLong(1), nil
	}
	return types.NewLong(0), nil
}

func nativeImagecolorallocate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagecolorallocate", len(args), 4, 4); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagecolorallocate", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	r, err := asInt(z, args[1], "imagecolorallocate", "2", "red")
	if err != nil {
		return types.Value{}, err
	}
	gr, err := asInt(z, args[2], "imagecolorallocate", "3", "green")
	if err != nil {
		return types.Value{}, err
	}
	b, err := asInt(z, args[3], "imagecolorallocate", "4", "blue")
	if err != nil {
		return types.Value{}, err
	}
	c := gdColorInt(r, gr, b, 0)
	nrgba := nrgbaFromGDColor(c)
	if !g.trueColor && len(g.palette) < 256 {
		if len(g.palette) == 0 {
			fillAllNRGBA(g.img, nrgba)
		}
		g.palette = append(g.palette, nrgba)
	}
	return types.NewLong(c), nil
}

func nativeImagecolorallocatealpha(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagecolorallocatealpha", len(args), 5, 5); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagecolorallocatealpha", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	r, err := asInt(z, args[1], "imagecolorallocatealpha", "2", "red")
	if err != nil {
		return types.Value{}, err
	}
	gr, err := asInt(z, args[2], "imagecolorallocatealpha", "3", "green")
	if err != nil {
		return types.Value{}, err
	}
	b, err := asInt(z, args[3], "imagecolorallocatealpha", "4", "blue")
	if err != nil {
		return types.Value{}, err
	}
	a, err := asInt(z, args[4], "imagecolorallocatealpha", "5", "alpha")
	if err != nil {
		return types.Value{}, err
	}
	c := gdColorInt(r, gr, b, a)
	nrgba := nrgbaFromGDColor(c)
	if !g.trueColor && len(g.palette) < 256 {
		if len(g.palette) == 0 {
			fillAllNRGBA(g.img, nrgba)
		}
		g.palette = append(g.palette, nrgba)
	}
	return types.NewLong(c), nil
}

func fillAllNRGBA(img *image.NRGBA, c color.NRGBA) {
	for i := 0; i+3 < len(img.Pix); i += 4 {
		img.Pix[i] = c.R
		img.Pix[i+1] = c.G
		img.Pix[i+2] = c.B
		img.Pix[i+3] = c.A
	}
}

func nativeImagecolortransparent(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagecolortransparent", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagecolortransparent", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	if len(args) == 2 && !missing(args[1]) {
		c, err := asInt(z, args[1], "imagecolortransparent", "2", "color")
		if err != nil {
			return types.Value{}, err
		}
		g.transparent = int(c)
	}
	return types.NewLong(int64(g.transparent)), nil
}

func nativeImagecolorat(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagecolorat", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagecolorat", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	x, err := asInt(z, args[1], "imagecolorat", "2", "x")
	if err != nil {
		return types.Value{}, err
	}
	y, err := asInt(z, args[2], "imagecolorat", "3", "y")
	if err != nil {
		return types.Value{}, err
	}
	b := g.img.Bounds()
	if int(x) < 0 || int(y) < 0 || int(x) >= b.Dx() || int(y) >= b.Dy() {
		return types.NewBool(false), nil
	}
	return types.NewLong(gdColorFromNRGBA(g.img.NRGBAAt(int(x), int(y)))), nil
}

func nativeImagecolorsforindex(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagecolorsforindex", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	if _, _, err := gdImageArg(z, args[0], "imagecolorsforindex", "1", "image"); err != nil {
		return types.Value{}, err
	}
	c, err := asInt(z, args[1], "imagecolorsforindex", "2", "color")
	if err != nil {
		return types.Value{}, err
	}
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	if err := setStrKey(z, &a, "red", types.NewLong((c>>16)&0xff)); err != nil {
		return types.Value{}, err
	}
	if err := setStrKey(z, &a, "green", types.NewLong((c>>8)&0xff)); err != nil {
		return types.Value{}, err
	}
	if err := setStrKey(z, &a, "blue", types.NewLong(c&0xff)); err != nil {
		return types.Value{}, err
	}
	if err := setStrKey(z, &a, "alpha", types.NewLong((c>>24)&0x7f)); err != nil {
		return types.Value{}, err
	}
	return a, nil
}

func nativeImagecolorstotal(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagecolorstotal", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagecolorstotal", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	if g.trueColor {
		return types.NewLong(0), nil
	}
	return types.NewLong(int64(len(g.palette))), nil
}

func nativeImagetruecolortopalette(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagetruecolortopalette", len(args), 3, 3); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagetruecolortopalette", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	_ = asBool(z, args[1])
	ncolors, err := asInt(z, args[2], "imagetruecolortopalette", "3", "num_colors")
	if err != nil {
		return types.Value{}, err
	}
	if ncolors <= 0 || ncolors > 256 {
		return types.NewBool(false), nil
	}
	g.trueColor = false
	pal := make([]color.NRGBA, 0, int(ncolors))
	seen := make(map[color.NRGBA]struct{}, int(ncolors))
	b := g.img.Bounds()
	for y := 0; y < b.Dy() && len(pal) < int(ncolors); y++ {
		for x := 0; x < b.Dx() && len(pal) < int(ncolors); x++ {
			c := g.img.NRGBAAt(x, y)
			if _, ok := seen[c]; !ok {
				seen[c] = struct{}{}
				pal = append(pal, c)
			}
		}
	}
	if len(pal) == 0 {
		pal = append(pal, color.NRGBA{A: 255})
	}
	g.palette = pal
	return types.NewBool(true), nil
}

func nativeImagesetpixel(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagesetpixel", len(args), 4, 4); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagesetpixel", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	x, err := asInt(z, args[1], "imagesetpixel", "2", "x")
	if err != nil {
		return types.Value{}, err
	}
	y, err := asInt(z, args[2], "imagesetpixel", "3", "y")
	if err != nil {
		return types.Value{}, err
	}
	c, err := asInt(z, args[3], "imagesetpixel", "4", "color")
	if err != nil {
		return types.Value{}, err
	}
	putGDPixel(g, int(x), int(y), nrgbaFromGDColor(c))
	return types.NewBool(true), nil
}

func nativeImagefilledrectangle(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagefilledrectangle", len(args), 6, 6); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagefilledrectangle", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	x1, err := asInt(z, args[1], "imagefilledrectangle", "2", "x1")
	if err != nil {
		return types.Value{}, err
	}
	y1, err := asInt(z, args[2], "imagefilledrectangle", "3", "y1")
	if err != nil {
		return types.Value{}, err
	}
	x2, err := asInt(z, args[3], "imagefilledrectangle", "4", "x2")
	if err != nil {
		return types.Value{}, err
	}
	y2, err := asInt(z, args[4], "imagefilledrectangle", "5", "y2")
	if err != nil {
		return types.Value{}, err
	}
	c, err := asInt(z, args[5], "imagefilledrectangle", "6", "color")
	if err != nil {
		return types.Value{}, err
	}
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	b := g.img.Bounds()
	w, h := b.Dx(), b.Dy()
	startX := max(0, int(x1))
	endX := min(w-1, int(x2))
	startY := max(0, int(y1))
	endY := min(h-1, int(y2))
	src := nrgbaFromGDColor(c)
	for py := startY; py <= endY; py++ {
		for px := startX; px <= endX; px++ {
			putGDPixel(g, px, py, src)
		}
	}
	return types.NewBool(true), nil
}

func nativeImagefill(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagefill", len(args), 4, 4); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagefill", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	x, err := asInt(z, args[1], "imagefill", "2", "x")
	if err != nil {
		return types.Value{}, err
	}
	y, err := asInt(z, args[2], "imagefill", "3", "y")
	if err != nil {
		return types.Value{}, err
	}
	c, err := asInt(z, args[3], "imagefill", "4", "color")
	if err != nil {
		return types.Value{}, err
	}
	b := g.img.Bounds()
	w, h := b.Dx(), b.Dy()
	sx, sy := int(x), int(y)
	if sx < 0 || sy < 0 || sx >= w || sy >= h {
		return types.NewBool(true), nil
	}
	target := g.img.NRGBAAt(sx, sy)
	fillCol := nrgbaFromGDColor(c)
	visited := make([]bool, w*h)
	queue := make([]image.Point, 0, 64)
	queue = append(queue, image.Point{X: sx, Y: sy})
	visited[sy*w+sx] = true
	for len(queue) > 0 {
		pt := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if !g.alphaBlending || fillCol.A == 0 {
			g.img.SetNRGBA(pt.X, pt.Y, fillCol)
		} else {
			putGDPixel(g, pt.X, pt.Y, fillCol)
		}
		neighbors := [4]image.Point{
			{X: pt.X - 1, Y: pt.Y},
			{X: pt.X + 1, Y: pt.Y},
			{X: pt.X, Y: pt.Y - 1},
			{X: pt.X, Y: pt.Y + 1},
		}
		for _, nb := range neighbors {
			if nb.X < 0 || nb.Y < 0 || nb.X >= w || nb.Y >= h {
				continue
			}
			idx := nb.Y*w + nb.X
			if visited[idx] {
				continue
			}
			if g.img.NRGBAAt(nb.X, nb.Y) == target {
				visited[idx] = true
				queue = append(queue, nb)
			}
		}
	}
	return types.NewBool(true), nil
}

func nativeImagecopy(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagecopy", len(args), 8, 8); err != nil {
		return types.Value{}, err
	}
	_, dst, err := gdImageArg(z, args[0], "imagecopy", "1", "dst_image")
	if err != nil {
		return types.Value{}, err
	}
	_, src, err := gdImageArg(z, args[1], "imagecopy", "2", "src_image")
	if err != nil {
		return types.Value{}, err
	}
	dstX, err := asInt(z, args[2], "imagecopy", "3", "dst_x")
	if err != nil {
		return types.Value{}, err
	}
	dstY, err := asInt(z, args[3], "imagecopy", "4", "dst_y")
	if err != nil {
		return types.Value{}, err
	}
	srcX, err := asInt(z, args[4], "imagecopy", "5", "src_x")
	if err != nil {
		return types.Value{}, err
	}
	srcY, err := asInt(z, args[5], "imagecopy", "6", "src_y")
	if err != nil {
		return types.Value{}, err
	}
	srcW, err := asInt(z, args[6], "imagecopy", "7", "src_width")
	if err != nil {
		return types.Value{}, err
	}
	srcH, err := asInt(z, args[7], "imagecopy", "8", "src_height")
	if err != nil {
		return types.Value{}, err
	}
	if srcW <= 0 || srcH <= 0 {
		return types.NewBool(false), nil
	}
	srcImg := src.img
	if dst == src {
		srcImg = cloneNRGBA(src.img)
	}
	sb := srcImg.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	for dy := 0; dy < int(srcH); dy++ {
		sy := int(srcY) + dy
		if sy < 0 || sy >= sh {
			continue
		}
		for dx := 0; dx < int(srcW); dx++ {
			sx := int(srcX) + dx
			if sx < 0 || sx >= sw {
				continue
			}
			c := srcImg.NRGBAAt(sx, sy)
			if src.transparent >= 0 && int(gdColorFromNRGBA(c)) == src.transparent {
				continue
			}
			putGDPixel(dst, int(dstX)+dx, int(dstY)+dy, c)
		}
	}
	return types.NewBool(true), nil
}

func nativeImagecopyresized(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return gdScaleCopy(z, args, "imagecopyresized", false)
}

func nativeImagecopyresampled(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	return gdScaleCopy(z, args, "imagecopyresampled", true)
}

func gdScaleCopy(z *zone.Zone, args []types.Value, fn string, bilinear bool) (types.Value, error) {
	if err := arity(fn, len(args), 10, 10); err != nil {
		return types.Value{}, err
	}
	_, dst, err := gdImageArg(z, args[0], fn, "1", "dst_image")
	if err != nil {
		return types.Value{}, err
	}
	_, src, err := gdImageArg(z, args[1], fn, "2", "src_image")
	if err != nil {
		return types.Value{}, err
	}
	dstX, err := asInt(z, args[2], fn, "3", "dst_x")
	if err != nil {
		return types.Value{}, err
	}
	dstY, err := asInt(z, args[3], fn, "4", "dst_y")
	if err != nil {
		return types.Value{}, err
	}
	srcX, err := asInt(z, args[4], fn, "5", "src_x")
	if err != nil {
		return types.Value{}, err
	}
	srcY, err := asInt(z, args[5], fn, "6", "src_y")
	if err != nil {
		return types.Value{}, err
	}
	dstW, err := asInt(z, args[6], fn, "7", "dst_width")
	if err != nil {
		return types.Value{}, err
	}
	dstH, err := asInt(z, args[7], fn, "8", "dst_height")
	if err != nil {
		return types.Value{}, err
	}
	srcW, err := asInt(z, args[8], fn, "9", "src_width")
	if err != nil {
		return types.Value{}, err
	}
	srcH, err := asInt(z, args[9], fn, "10", "src_height")
	if err != nil {
		return types.Value{}, err
	}
	if dstW <= 0 || dstH <= 0 || srcW == 0 || srcH == 0 {
		return types.NewBool(false), nil
	}
	flipX, flipY := false, false
	if srcW < 0 {
		flipX = true
		srcX = srcX + srcW + 1
		srcW = -srcW
	}
	if srcH < 0 {
		flipY = true
		srcY = srcY + srcH + 1
		srcH = -srcH
	}
	srcImg := src.img
	if dst == src {
		srcImg = cloneNRGBA(src.img)
	}
	sb := srcImg.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 {
		return types.NewBool(false), nil
	}
	if !bilinear {
		for dy := 0; dy < int(dstH); dy++ {
			effDY := dy
			if flipY {
				effDY = int(dstH) - 1 - dy
			}
			sy := int(srcY) + (effDY*int(srcH))/int(dstH)
			if sy < 0 || sy >= sh {
				continue
			}
			for dx := 0; dx < int(dstW); dx++ {
				effDX := dx
				if flipX {
					effDX = int(dstW) - 1 - dx
				}
				sx := int(srcX) + (effDX*int(srcW))/int(dstW)
				if sx < 0 || sx >= sw {
					continue
				}
				c := srcImg.NRGBAAt(sx, sy)
				if src.transparent >= 0 && int(gdColorFromNRGBA(c)) == src.transparent {
					continue
				}
				putGDPixel(dst, int(dstX)+dx, int(dstY)+dy, c)
			}
		}
		return types.NewBool(true), nil
	}

	minSX := clampFloat(float64(srcX), 0, float64(sw-1))
	maxSX := clampFloat(float64(srcX+srcW-1), 0, float64(sw-1))
	minSY := clampFloat(float64(srcY), 0, float64(sh-1))
	maxSY := clampFloat(float64(srcY+srcH-1), 0, float64(sh-1))

	for dy := 0; dy < int(dstH); dy++ {
		effDY := dy
		if flipY {
			effDY = int(dstH) - 1 - dy
		}
		fy := float64(srcY) + (float64(effDY)+0.5)*float64(srcH)/float64(dstH) - 0.5
		fy = clampFloat(fy, minSY, maxSY)
		y0 := int(math.Floor(fy))
		y1 := min(y0+1, int(maxSY))
		wy := fy - float64(y0)
		for dx := 0; dx < int(dstW); dx++ {
			effDX := dx
			if flipX {
				effDX = int(dstW) - 1 - dx
			}
			fx := float64(srcX) + (float64(effDX)+0.5)*float64(srcW)/float64(dstW) - 0.5
			fx = clampFloat(fx, minSX, maxSX)
			x0 := int(math.Floor(fx))
			x1 := min(x0+1, int(maxSX))
			wx := fx - float64(x0)

			c00 := srcImg.NRGBAAt(x0, y0)
			c10 := srcImg.NRGBAAt(x1, y0)
			c01 := srcImg.NRGBAAt(x0, y1)
			c11 := srcImg.NRGBAAt(x1, y1)

			w00 := (1.0 - wx) * (1.0 - wy)
			w10 := wx * (1.0 - wy)
			w01 := (1.0 - wx) * wy
			w11 := wx * wy

			a := float64(c00.A)*w00 + float64(c10.A)*w10 + float64(c01.A)*w01 + float64(c11.A)*w11
			r := float64(c00.R)*w00 + float64(c10.R)*w10 + float64(c01.R)*w01 + float64(c11.R)*w11
			gr := float64(c00.G)*w00 + float64(c10.G)*w10 + float64(c01.G)*w01 + float64(c11.G)*w11
			b := float64(c00.B)*w00 + float64(c10.B)*w10 + float64(c01.B)*w01 + float64(c11.B)*w11

			putGDPixel(dst, int(dstX)+dx, int(dstY)+dy, color.NRGBA{
				R: uint8(r + 0.5),
				G: uint8(gr + 0.5),
				B: uint8(b + 0.5),
				A: uint8(a + 0.5),
			})
		}
	}
	return types.NewBool(true), nil
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func asFloat(z *zone.Zone, v types.Value, fn, idx, param string) (float64, error) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeDouble:
		return v.ToDouble(), nil
	case types.TypeLong:
		return float64(v.ToLong()), nil
	case types.TypeTrue:
		return 1, nil
	case types.TypeFalse, types.TypeNull, types.TypeUndef:
		return 0, nil
	case types.TypeString:
		s := strings.TrimSpace(types.StringData(z, v))
		if s == "" {
			return 0, nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, typeErr(fn, idx, param, "float", "string")
		}
		return f, nil
	default:
		return 0, typeErr(fn, idx, param, "float", typeKind(z, v))
	}
}

func nativeImagerotate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagerotate", len(args), 3, 4); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagerotate", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	angle, err := asFloat(z, args[1], "imagerotate", "2", "angle")
	if err != nil {
		return types.Value{}, err
	}
	bgColor, err := asInt(z, args[2], "imagerotate", "3", "background_color")
	if err != nil {
		return types.Value{}, err
	}
	if len(args) == 4 && !missing(args[3]) {
		if _, err := asInt(z, args[3], "imagerotate", "4", "ignore_transparent"); err != nil {
			return types.Value{}, err
		}
	}

	norm := math.Mod(angle, 360.0)
	if norm < 0 {
		norm += 360.0
	}
	b := g.img.Bounds()
	w, h := b.Dx(), b.Dy()
	var dst *image.NRGBA
	switch {
	case math.Abs(norm) < 1e-6 || math.Abs(norm-360.0) < 1e-6:
		dst = cloneNRGBA(g.img)
	case math.Abs(norm-90.0) < 1e-6:
		dst = image.NewNRGBA(image.Rect(0, 0, h, w))
		for dy := 0; dy < w; dy++ {
			for dx := 0; dx < h; dx++ {
				dst.SetNRGBA(dx, dy, g.img.NRGBAAt(w-1-dy, dx))
			}
		}
	case math.Abs(norm-180.0) < 1e-6:
		dst = image.NewNRGBA(image.Rect(0, 0, w, h))
		for dy := 0; dy < h; dy++ {
			for dx := 0; dx < w; dx++ {
				dst.SetNRGBA(dx, dy, g.img.NRGBAAt(w-1-dx, h-1-dy))
			}
		}
	case math.Abs(norm-270.0) < 1e-6:
		dst = image.NewNRGBA(image.Rect(0, 0, h, w))
		for dy := 0; dy < w; dy++ {
			for dx := 0; dx < h; dx++ {
				dst.SetNRGBA(dx, dy, g.img.NRGBAAt(dy, h-1-dx))
			}
		}
	default:
		rad := norm * math.Pi / 180.0
		cosA := math.Cos(rad)
		sinA := math.Sin(rad)
		newW := max(1, int(math.Ceil(float64(w)*math.Abs(cosA)+float64(h)*math.Abs(sinA))))
		newH := max(1, int(math.Ceil(float64(h)*math.Abs(cosA)+float64(w)*math.Abs(sinA))))
		if !validGDDims(int64(newW), int64(newH)) {
			return types.NewBool(false), nil
		}
		dst = image.NewNRGBA(image.Rect(0, 0, newW, newH))
		bg := nrgbaFromGDColor(bgColor)
		cxSrc := float64(w-1) / 2.0
		cySrc := float64(h-1) / 2.0
		cxDst := float64(newW-1) / 2.0
		cyDst := float64(newH-1) / 2.0
		for dy := 0; dy < newH; dy++ {
			ry := float64(dy) - cyDst
			for dx := 0; dx < newW; dx++ {
				rx := float64(dx) - cxDst
				sx := int(math.Round(rx*cosA - ry*sinA + cxSrc))
				sy := int(math.Round(rx*sinA + ry*cosA + cySrc))
				if sx >= 0 && sx < w && sy >= 0 && sy < h {
					dst.SetNRGBA(dx, dy, g.img.NRGBAAt(sx, sy))
				} else {
					dst.SetNRGBA(dx, dy, bg)
				}
			}
		}
	}

	return storeGDImage(z, &gdImage{
		img:           dst,
		trueColor:     true,
		alphaBlending: g.alphaBlending,
		saveAlpha:     g.saveAlpha,
		transparent:   -1,
	}), nil
}

func nativeImageflip(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imageflip", len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imageflip", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	mode, err := asInt(z, args[1], "imageflip", "2", "mode")
	if err != nil {
		return types.Value{}, err
	}
	b := g.img.Bounds()
	w, h := b.Dx(), b.Dy()
	switch mode {
	case 1: // IMG_FLIP_HORIZONTAL
		for y := 0; y < h; y++ {
			for x := 0; x < w/2; x++ {
				c1 := g.img.NRGBAAt(x, y)
				c2 := g.img.NRGBAAt(w-1-x, y)
				g.img.SetNRGBA(x, y, c2)
				g.img.SetNRGBA(w-1-x, y, c1)
			}
		}
	case 2: // IMG_FLIP_VERTICAL
		for y := 0; y < h/2; y++ {
			for x := 0; x < w; x++ {
				c1 := g.img.NRGBAAt(x, y)
				c2 := g.img.NRGBAAt(x, h-1-y)
				g.img.SetNRGBA(x, y, c2)
				g.img.SetNRGBA(x, h-1-y, c1)
			}
		}
	case 3: // IMG_FLIP_BOTH
		total := w * h
		for i := 0; i < total/2; i++ {
			x1, y1 := i%w, i/w
			x2, y2 := w-1-x1, h-1-y1
			c1 := g.img.NRGBAAt(x1, y1)
			c2 := g.img.NRGBAAt(x2, y2)
			g.img.SetNRGBA(x1, y1, c2)
			g.img.SetNRGBA(x2, y2, c1)
		}
	default:
		return types.NewBool(false), nil
	}
	return types.NewBool(true), nil
}

func nativeImagejpeg(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagejpeg", len(args), 1, 3); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagejpeg", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	qual, err := optInt(z, args, 2, -1, "imagejpeg", "3", "quality")
	if err != nil {
		return types.Value{}, err
	}
	q := 75
	if qual >= 0 {
		q = int(qual)
		if q < 1 {
			q = 1
		} else if q > 100 {
			q = 100
		}
	}
	to := arg(args, 1)
	if to.IsUndef() || deref(z, to).IsNull() {
		dst := out
		if dst == nil {
			dst = io.Discard
		}
		if err := jpeg.Encode(dst, g.img, &jpeg.Options{Quality: q}); err != nil {
			return types.NewBool(false), nil
		}
		return types.NewBool(true), nil
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, g.img, &jpeg.Options{Quality: q}); err != nil {
		return types.NewBool(false), nil
	}
	return writeGDOutput(z, out, to, "imagejpeg", buf.Bytes())
}

func nativeImagepng(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagepng", len(args), 1, 4); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagepng", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	qual, err := optInt(z, args, 2, -1, "imagepng", "3", "quality")
	if err != nil {
		return types.Value{}, err
	}
	if _, err := optInt(z, args, 3, -1, "imagepng", "4", "filters"); err != nil {
		return types.Value{}, err
	}
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if qual == 0 {
		enc.CompressionLevel = png.NoCompression
	} else if qual >= 1 && qual <= 3 {
		enc.CompressionLevel = png.BestSpeed
	} else if qual >= 7 && qual <= 9 {
		enc.CompressionLevel = png.BestCompression
	}
	to := arg(args, 1)
	if to.IsUndef() || deref(z, to).IsNull() {
		dst := out
		if dst == nil {
			dst = io.Discard
		}
		if err := enc.Encode(dst, g.img); err != nil {
			return types.NewBool(false), nil
		}
		return types.NewBool(true), nil
	}
	var buf bytes.Buffer
	if err := enc.Encode(&buf, g.img); err != nil {
		return types.NewBool(false), nil
	}
	return writeGDOutput(z, out, to, "imagepng", buf.Bytes())
}

func nativeImagegif(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagegif", len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	_, g, err := gdImageArg(z, args[0], "imagegif", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	to := arg(args, 1)
	if to.IsUndef() || deref(z, to).IsNull() {
		dst := out
		if dst == nil {
			dst = io.Discard
		}
		if err := gif.Encode(dst, g.img, nil); err != nil {
			return types.NewBool(false), nil
		}
		return types.NewBool(true), nil
	}
	var buf bytes.Buffer
	if err := gif.Encode(&buf, g.img, nil); err != nil {
		return types.NewBool(false), nil
	}
	return writeGDOutput(z, out, to, "imagegif", buf.Bytes())
}

func nativeImagedestroy(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("imagedestroy", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	id, g, err := gdImageArg(z, args[0], "imagedestroy", "1", "image")
	if err != nil {
		return types.Value{}, err
	}
	g.img = nil
	delete(extStateFor(z).handles, id)
	return types.NewBool(true), nil
}

// ---- fileinfo ----

func nativeFinfoOpen(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("finfo_open", len(args), 0, 2); err != nil {
		return types.Value{}, err
	}
	flags, err := optInt(z, args, 0, 0, "finfo_open", "1", "flags")
	if err != nil {
		return types.Value{}, err
	}
	if len(args) >= 2 && !missing(args[1]) {
		if _, err := asString(z, args[1], "finfo_open", "2", "magic_database"); err != nil {
			return types.Value{}, err
		}
	}
	return storeFinfo(z, &finfoHandle{flags: flags}), nil
}

func nativeFinfoClose(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("finfo_close", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	id, f, err := finfoArg(z, args[0], "finfo_close", "1", "finfo")
	if err != nil {
		return types.Value{}, err
	}
	f.closed = true
	delete(extStateFor(z).handles, id)
	return types.NewBool(true), nil
}

func nativeFinfoBuffer(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("finfo_buffer", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	_, f, err := finfoArg(z, args[0], "finfo_buffer", "1", "finfo")
	if err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[1], "finfo_buffer", "2", "string")
	if err != nil {
		return types.Value{}, err
	}
	flags, err := optInt(z, args, 2, 0, "finfo_buffer", "3", "flags")
	if err != nil {
		return types.Value{}, err
	}
	if flags == 0 {
		flags = f.flags
	}
	return newStr(z, detectFinfo([]byte(data), "", flags))
}

func nativeFinfoFile(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("finfo_file", len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	_, f, err := finfoArg(z, args[0], "finfo_file", "1", "finfo")
	if err != nil {
		return types.Value{}, err
	}
	filename, err := asString(z, args[1], "finfo_file", "2", "filename")
	if err != nil {
		return types.Value{}, err
	}
	flags, err := optInt(z, args, 2, 0, "finfo_file", "3", "flags")
	if err != nil {
		return types.Value{}, err
	}
	if flags == 0 {
		flags = f.flags
	}
	if filename == "" {
		return types.NewBool(false), nil
	}
	data, ok := readGDFileBytes(z, out, filename)
	if !ok {
		return types.NewBool(false), nil
	}
	return newStr(z, detectFinfo(data, filename, flags))
}

func nativeMimeContentType(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("mime_content_type", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	v := deref(z, args[0])
	if v.Type == types.TypeResource {
		sgc, ok := streams.Lookup("stream_get_contents")
		if !ok {
			return types.NewBool(false), nil
		}
		got, err := sgc(z, out, []types.Value{v, types.NewLong(-1), types.NewLong(0)})
		if err != nil {
			return types.NewBool(false), err
		}
		got = deref(z, got)
		if got.Type != types.TypeString {
			types.ReleaseValue(z, &got)
			got, err = sgc(z, out, []types.Value{v})
			if err != nil {
				return types.NewBool(false), err
			}
			got = deref(z, got)
			if got.Type != types.TypeString {
				types.ReleaseValue(z, &got)
				return types.NewBool(false), nil
			}
		}
		data := []byte(types.StringData(z, got))
		types.ReleaseValue(z, &got)
		return newStr(z, detectFinfo(data, "", 16))
	}
	filename, err := asString(z, v, "mime_content_type", "1", "filename")
	if err != nil {
		return types.Value{}, err
	}
	if filename == "" {
		return types.NewBool(false), nil
	}
	data, ok := readGDFileBytes(z, out, filename)
	if !ok {
		return types.NewBool(false), nil
	}
	return newStr(z, detectFinfo(data, filename, 16))
}

func detectFinfo(data []byte, filename string, flags int64) string {
	mimeType, encoding := classifyBytes(data, filename)
	if flags&1040 == 1040 {
		return mimeType + "; charset=" + encoding
	}
	if flags&1024 != 0 {
		return encoding
	}
	return mimeType
}

func classifyBytes(data []byte, filename string) (mimeType, encoding string) {
	if len(data) == 0 {
		return "application/x-empty", "binary"
	}
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", "binary"
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image/jpeg", "binary"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif", "binary"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp", "binary"
	case bytes.HasPrefix(data, []byte("BM")) && len(data) >= 14:
		return "image/bmp", "binary"
	case bytes.HasPrefix(data, []byte("\x00\x00\x01\x00")):
		return "image/vnd.microsoft.icon", "binary"
	case bytes.HasPrefix(data, []byte("%PDF-")):
		return "application/pdf", "binary"
	case bytes.HasPrefix(data, []byte("PK\x03\x04")), bytes.HasPrefix(data, []byte("PK\x05\x06")), bytes.HasPrefix(data, []byte("PK\x07\x08")):
		return "application/zip", "binary"
	case bytes.HasPrefix(data, []byte("\x1f\x8b")):
		return "application/gzip", "binary"
	case bytes.HasPrefix(data, []byte("wOF2")):
		return "font/woff2", "binary"
	case bytes.HasPrefix(data, []byte("wOFF")):
		return "font/woff", "binary"
	case bytes.HasPrefix(data, []byte("\x00asm")):
		return "application/wasm", "binary"
	case bytes.HasPrefix(data, []byte("OggS")):
		return "application/ogg", "binary"
	case bytes.HasPrefix(data, []byte("ID3")), bytes.HasPrefix(data, []byte("\xff\xfb")), bytes.HasPrefix(data, []byte("\xff\xf3")):
		return "audio/mpeg", "binary"
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		return "video/mp4", "binary"
	}

	if bytes.IndexByte(data, 0) < 0 && utf8.Valid(data) {
		enc := "us-ascii"
		for _, b := range data {
			if b >= 0x80 {
				enc = "utf-8"
				break
			}
		}
		trimmed := bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
		lowerHead := bytes.ToLower(trimmed[:min(len(trimmed), 64)])
		switch {
		case bytes.HasPrefix(lowerHead, []byte("<?php")):
			mimeType = "text/x-php"
		case bytes.HasPrefix(lowerHead, []byte("<?xml")):
			if bytes.Contains(bytes.ToLower(trimmed[:min(len(trimmed), 512)]), []byte("<svg")) {
				mimeType = "image/svg+xml"
			} else {
				mimeType = "text/xml"
			}
		case bytes.HasPrefix(lowerHead, []byte("<svg")):
			mimeType = "image/svg+xml"
		case bytes.HasPrefix(lowerHead, []byte("<!doctype html")), bytes.HasPrefix(lowerHead, []byte("<html")):
			mimeType = "text/html"
		case len(trimmed) >= 2 && ((trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}') || (trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']')):
			mimeType = "application/json"
		default:
			mimeType = "text/plain"
		}
		if filename != "" {
			switch strings.ToLower(filepath.Ext(filename)) {
			case ".css":
				mimeType = "text/css"
			case ".js", ".mjs":
				mimeType = "application/javascript"
			case ".svg":
				mimeType = "image/svg+xml"
			case ".woff2":
				return "font/woff2", "binary"
			case ".json":
				mimeType = "application/json"
			case ".xml":
				mimeType = "text/xml"
			case ".csv":
				mimeType = "text/csv"
			case ".txt":
				mimeType = "text/plain"
			case ".html", ".htm":
				mimeType = "text/html"
			case ".php":
				mimeType = "text/x-php"
			}
		}
		return mimeType, enc
	}

	ct := http.DetectContentType(data)
	base, param, hasParam := strings.Cut(ct, ";")
	mimeType = strings.TrimSpace(base)
	encoding = "binary"
	if hasParam {
		if _, after, ok := strings.Cut(strings.ToLower(param), "charset="); ok {
			encoding = strings.TrimSpace(after)
		}
	}
	return mimeType, encoding
}
