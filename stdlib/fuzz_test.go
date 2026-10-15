// Code créé par agent — Lot 8.5 : fuzzing des analyseurs parse_ini_string et getimagesizefromstring.
package stdlib

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// fuzzZoneSize borne l'arène d'une entrée ; une entrée qui ne tient pas rend une erreur, jamais une panique.
const fuzzZoneSize = 1 << 20

// resetFuzzZone exige qu'aucun nettoyage ne reste en attente, rembobine la Zone par z.Reset(nil) et
// vérifie qu'aucun octet n'y reste vivant.
func resetFuzzZone(t *testing.T, z *zone.Zone, what string) {
	t.Helper()
	if n := z.PendingCleanups(); n != 0 {
		t.Fatalf("%s : %d nettoyages en attente avant z.Reset(nil)", what, n)
	}
	z.Reset(nil)
	if n := z.LiveBytes(); n != 0 {
		t.Fatalf("%s : %d octets vivants après z.Reset(nil)", what, n)
	}
}

// FuzzParseINI exécute parse_ini_string sur une entrée arbitraire dans les trois modes d'analyse,
// sections désactivées puis activées.
func FuzzParseINI(f *testing.F) {
	for _, src := range iniCorpus {
		f.Add(src)
	}
	for _, src := range iniLongCorpus() {
		f.Add(src)
	}
	z := zone.New(fuzzZoneSize)
	f.Fuzz(func(t *testing.T, src string) {
		for _, mode := range []int64{iniScannerNormal, iniScannerRaw, iniScannerTyped} {
			for _, sections := range []bool{false, true} {
				s, err := newStr(z, src)
				if err != nil {
					resetFuzzZone(t, z, "newStr")
					return
				}
				v, err := nativeParseIniString(z, nil, []types.Value{s, types.NewBool(sections), types.NewLong(mode)})
				if err == nil && v.Type != types.TypeArray && v.Type != types.TypeFalse {
					t.Fatalf("parse_ini_string(mode=%d, sections=%v) rend le type %d", mode, sections, v.Type)
				}
				resetFuzzZone(t, z, "parse_ini_string")
			}
		}
	})
}

// imageSeeds rend des en-têtes PNG, GIF et JPEG encodés par la bibliothèque standard, des en-têtes
// WebP (VP8, VP8L, VP8X), BMP et ICO construits octet à octet, puis chacun de leurs préfixes tronqués.
func imageSeeds(f *testing.F) [][]byte {
	f.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 7, 3))
	pal := image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{color.Black, color.White})
	var whole [][]byte
	enc := func(fn func(*bytes.Buffer) error) {
		var b bytes.Buffer
		if err := fn(&b); err != nil {
			f.Fatal(err)
		}
		whole = append(whole, b.Bytes())
	}
	enc(func(b *bytes.Buffer) error { return png.Encode(b, img) })
	enc(func(b *bytes.Buffer) error { return gif.Encode(b, pal, nil) })
	enc(func(b *bytes.Buffer) error { return jpeg.Encode(b, img, nil) })
	le32 := func(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	riff := func(chunk string, payload []byte) []byte {
		body := cat([]byte("WEBP"+chunk), le32(uint32(len(payload))), payload)
		return cat([]byte("RIFF"), le32(uint32(len(body))), body)
	}
	whole = append(whole,
		// VP8 : étiquette de trame, code de départ 9d 01 2a, largeur 7 et hauteur 3 sur 14 bits.
		riff("VP8 ", []byte{0x50, 0x02, 0x00, 0x9d, 0x01, 0x2a, 7, 0, 3, 0}),
		// VP8L : signature 0x2f, largeur-1 et hauteur-1 sur 14 bits chacune.
		riff("VP8L", []byte{0x2f, 6, 0x80, 0, 0}),
		// VP8X : drapeaux, largeur-1 et hauteur-1 sur 24 bits.
		riff("VP8X", []byte{0x10, 0, 0, 0, 6, 0, 0, 2, 0, 0}),
		// BMP : en-tête de fichier de 14 octets puis BITMAPINFOHEADER de 40 octets.
		cat([]byte("BM"), le32(70), le32(0), le32(54), le32(40), le32(7), le32(3), []byte{1, 0, 24, 0}, make([]byte, 24)),
		// ICO : réservé, type 1, une image 16×16 en 32 bits.
		cat([]byte{0, 0, 1, 0, 1, 0, 16, 16, 0, 0, 1, 0, 32, 0}, le32(40), le32(22)),
	)
	var out [][]byte
	for _, b := range whole {
		for n := range min(len(b), 64) + 1 {
			out = append(out, b[:n])
		}
		out = append(out, b, b[:len(b)-1])
	}
	return out
}

// FuzzGetImageSize exécute getimagesizefromstring sur des en-têtes d'image arbitraires ou tronqués,
// avec et sans le tableau image_info passé par référence.
func FuzzGetImageSize(f *testing.F) {
	for _, b := range imageSeeds(f) {
		f.Add(b)
	}
	z := zone.New(fuzzZoneSize)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzZoneSize/4 {
			return
		}
		for _, withInfo := range []bool{false, true} {
			s, err := newStr(z, string(data))
			if err != nil {
				resetFuzzZone(t, z, "newStr")
				return
			}
			args := []types.Value{s}
			if withInfo {
				ref, err := types.NewReference(z, types.NewNull())
				if err != nil {
					t.Fatal(err)
				}
				args = append(args, ref)
			}
			v, err := nativeGetimagesizefromstring(z, nil, args)
			if err != nil {
				t.Fatalf("getimagesizefromstring : %v", err)
			}
			if v.Type != types.TypeArray && v.Type != types.TypeFalse {
				t.Fatalf("getimagesizefromstring rend le type %d", v.Type)
			}
			resetFuzzZone(t, z, "getimagesizefromstring")
		}
	})
}
