package ext

import (
	"encoding/binary"
	"hash"
	"math/bits"
)

// Condensats de l'extension hash de PHP 8.3 absents de la bibliothèque standard de Go. Chacun
// implémente hash.Cloner pour hash_copy ; l'ordre des octets de Sum suit l'affichage de PHP.

// ---- crc32 (polynôme de bzip2, non réfléchi ; condensat petit-boutiste comme PHP_CRC32LEFinal) ----

var crc32BZip2Table = func() (t [256]uint32) {
	for i := range t {
		c := uint32(i) << 24
		for range 8 {
			if c&0x80000000 != 0 {
				c = c<<1 ^ 0x04c11db7
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return t
}()

type crc32BZip2 struct{ state uint32 }

func newCRC32BZip2() hash.Hash { return &crc32BZip2{state: ^uint32(0)} }

func (d *crc32BZip2) Write(p []byte) (int, error) {
	s := d.state
	for _, c := range p {
		s = s<<8 ^ crc32BZip2Table[byte(s>>24)^c]
	}
	d.state = s
	return len(p), nil
}

func (d *crc32BZip2) Sum(b []byte) []byte { return binary.LittleEndian.AppendUint32(b, ^d.state) }
func (d *crc32BZip2) Reset()              { d.state = ^uint32(0) }
func (d *crc32BZip2) Size() int           { return 4 }
func (d *crc32BZip2) BlockSize() int      { return 4 }
func (d *crc32BZip2) Clone() (hash.Cloner, error) {
	c := *d
	return &c, nil
}

// ---- joaat (Jenkins one-at-a-time) ----

type joaat struct{ state uint32 }

func newJoaat() hash.Hash { return &joaat{} }

func (d *joaat) Write(p []byte) (int, error) {
	h := d.state
	for _, c := range p {
		h += uint32(c)
		h += h << 10
		h ^= h >> 6
	}
	d.state = h
	return len(p), nil
}

func (d *joaat) Sum(b []byte) []byte {
	h := d.state
	h += h << 3
	h ^= h >> 11
	h += h << 15
	return binary.BigEndian.AppendUint32(b, h)
}

func (d *joaat) Reset()         { d.state = 0 }
func (d *joaat) Size() int      { return 4 }
func (d *joaat) BlockSize() int { return 4 }
func (d *joaat) Clone() (hash.Cloner, error) {
	c := *d
	return &c, nil
}

// ---- ripemd160 ----

type ripemd160 struct {
	s   [5]uint32
	buf [64]byte
	nx  int
	len uint64
}

func newRIPEMD160() hash.Hash {
	d := &ripemd160{}
	d.Reset()
	return d
}

func (d *ripemd160) Reset() {
	d.s = [5]uint32{0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0}
	d.nx, d.len = 0, 0
}

func (d *ripemd160) Size() int      { return 20 }
func (d *ripemd160) BlockSize() int { return 64 }
func (d *ripemd160) Clone() (hash.Cloner, error) {
	c := *d
	return &c, nil
}

func (d *ripemd160) Write(p []byte) (int, error) {
	n := len(p)
	d.len += uint64(n)
	if d.nx > 0 {
		k := copy(d.buf[d.nx:], p)
		d.nx += k
		p = p[k:]
		if d.nx < 64 {
			return n, nil
		}
		ripemd160Block(&d.s, d.buf[:])
		d.nx = 0
	}
	for len(p) >= 64 {
		ripemd160Block(&d.s, p[:64])
		p = p[64:]
	}
	d.nx = copy(d.buf[:], p)
	return n, nil
}

func (d *ripemd160) Sum(b []byte) []byte {
	c := *d
	var pad [72]byte
	pad[0] = 0x80
	k := 56 - c.nx
	if k <= 0 {
		k += 64
	}
	binary.LittleEndian.PutUint64(pad[k:], c.len<<3)
	_, _ = c.Write(pad[:k+8])
	for _, w := range c.s {
		b = binary.LittleEndian.AppendUint32(b, w)
	}
	return b
}

var (
	rmdR = [80]uint8{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		7, 4, 13, 1, 10, 6, 15, 3, 12, 0, 9, 5, 2, 14, 11, 8,
		3, 10, 14, 4, 9, 15, 8, 1, 2, 7, 0, 6, 13, 11, 5, 12,
		1, 9, 11, 10, 0, 8, 12, 4, 13, 3, 7, 15, 14, 5, 6, 2,
		4, 0, 5, 9, 7, 12, 2, 10, 14, 1, 3, 8, 11, 6, 15, 13,
	}
	rmdRP = [80]uint8{
		5, 14, 7, 0, 9, 2, 11, 4, 13, 6, 15, 8, 1, 10, 3, 12,
		6, 11, 3, 7, 0, 13, 5, 10, 14, 15, 8, 12, 4, 9, 1, 2,
		15, 5, 1, 3, 7, 14, 6, 9, 11, 8, 12, 2, 10, 0, 4, 13,
		8, 6, 4, 1, 3, 11, 15, 0, 5, 12, 2, 13, 9, 7, 10, 14,
		12, 15, 10, 4, 1, 5, 8, 7, 6, 2, 13, 14, 0, 3, 9, 11,
	}
	rmdS = [80]uint8{
		11, 14, 15, 12, 5, 8, 7, 9, 11, 13, 14, 15, 6, 7, 9, 8,
		7, 6, 8, 13, 11, 9, 7, 15, 7, 12, 15, 9, 11, 7, 13, 12,
		11, 13, 6, 7, 14, 9, 13, 15, 14, 8, 13, 6, 5, 12, 7, 5,
		11, 12, 14, 15, 14, 15, 9, 8, 9, 14, 5, 6, 8, 6, 5, 12,
		9, 15, 5, 11, 6, 8, 13, 12, 5, 12, 13, 14, 11, 8, 5, 6,
	}
	rmdSP = [80]uint8{
		8, 9, 9, 11, 13, 15, 15, 5, 7, 7, 8, 11, 14, 14, 12, 6,
		9, 13, 15, 7, 12, 8, 9, 11, 7, 7, 12, 7, 6, 15, 13, 11,
		9, 7, 15, 11, 8, 6, 6, 14, 12, 13, 5, 14, 13, 13, 7, 5,
		15, 5, 8, 11, 14, 14, 6, 14, 6, 9, 12, 9, 12, 5, 15, 8,
		8, 5, 12, 9, 12, 5, 14, 6, 8, 13, 6, 5, 15, 13, 11, 11,
	}
	rmdK  = [5]uint32{0x00000000, 0x5a827999, 0x6ed9eba1, 0x8f1bbcdc, 0xa953fd4e}
	rmdKP = [5]uint32{0x50a28be6, 0x5c4dd124, 0x6d703ef3, 0x7a6d76e9, 0x00000000}
)

// rmdF est la fonction booléenne de la ronde r (0 à 4) de RIPEMD-160.
func rmdF(r int, x, y, z uint32) uint32 {
	switch r {
	case 0:
		return x ^ y ^ z
	case 1:
		return x&y | ^x&z
	case 2:
		return (x | ^y) ^ z
	case 3:
		return x&z | y&^z
	default:
		return x ^ (y | ^z)
	}
}

func ripemd160Block(s *[5]uint32, p []byte) {
	var x [16]uint32
	for i := range x {
		x[i] = binary.LittleEndian.Uint32(p[4*i:])
	}
	a, b, c, d, e := s[0], s[1], s[2], s[3], s[4]
	ap, bp, cp, dp, ep := a, b, c, d, e
	for j := range 80 {
		r := j / 16
		t := bits.RotateLeft32(a+rmdF(r, b, c, d)+x[rmdR[j]]+rmdK[r], int(rmdS[j])) + e
		a, e, d, c, b = e, d, bits.RotateLeft32(c, 10), b, t
		t = bits.RotateLeft32(ap+rmdF(4-r, bp, cp, dp)+x[rmdRP[j]]+rmdKP[r], int(rmdSP[j])) + ep
		ap, ep, dp, cp, bp = ep, dp, bits.RotateLeft32(cp, 10), bp, t
	}
	t := s[1] + c + dp
	s[1] = s[2] + d + ep
	s[2] = s[3] + e + ap
	s[3] = s[4] + a + bp
	s[4] = s[0] + b + cp
	s[0] = t
}

// ---- xxh32 et xxh64 (graine nulle, condensat gros-boutiste) ----

const (
	xxP32_1 uint32 = 0x9e3779b1
	xxP32_2 uint32 = 0x85ebca77
	xxP32_3 uint32 = 0xc2b2ae3d
	xxP32_4 uint32 = 0x27d4eb2f
	xxP32_5 uint32 = 0x165667b1
	xxP64_1 uint64 = 0x9e3779b185ebca87
	xxP64_2 uint64 = 0xc2b2ae3d27d4eb4f
	xxP64_3 uint64 = 0x165667b19e3779f9
	xxP64_4 uint64 = 0x85ebca77c2b2ae63
	xxP64_5 uint64 = 0x27d4eb2f165667c5
)

type xxh32 struct {
	v     [4]uint32
	buf   [16]byte
	nx    int
	total uint64
}

func newXXH32() hash.Hash {
	d := &xxh32{}
	d.Reset()
	return d
}

func (d *xxh32) Reset() {
	p1, p2 := xxP32_1, xxP32_2
	d.v = [4]uint32{p1 + p2, p2, 0, -p1}
	d.nx, d.total = 0, 0
}

func (d *xxh32) Size() int      { return 4 }
func (d *xxh32) BlockSize() int { return 16 }
func (d *xxh32) Clone() (hash.Cloner, error) {
	c := *d
	return &c, nil
}

func xxh32Round(acc, in uint32) uint32 {
	return bits.RotateLeft32(acc+in*xxP32_2, 13) * xxP32_1
}

func (d *xxh32) stripe(p []byte) {
	for i := range d.v {
		d.v[i] = xxh32Round(d.v[i], binary.LittleEndian.Uint32(p[4*i:]))
	}
}

func (d *xxh32) Write(p []byte) (int, error) {
	n := len(p)
	d.total += uint64(n)
	if d.nx > 0 {
		k := copy(d.buf[d.nx:], p)
		d.nx += k
		p = p[k:]
		if d.nx < 16 {
			return n, nil
		}
		d.stripe(d.buf[:])
		d.nx = 0
	}
	for len(p) >= 16 {
		d.stripe(p)
		p = p[16:]
	}
	d.nx = copy(d.buf[:], p)
	return n, nil
}

func (d *xxh32) Sum(b []byte) []byte {
	var h uint32
	if d.total >= 16 {
		h = bits.RotateLeft32(d.v[0], 1) + bits.RotateLeft32(d.v[1], 7) + bits.RotateLeft32(d.v[2], 12) + bits.RotateLeft32(d.v[3], 18)
	} else {
		h = xxP32_5
	}
	h += uint32(d.total)
	p := d.buf[:d.nx]
	for ; len(p) >= 4; p = p[4:] {
		h = bits.RotateLeft32(h+binary.LittleEndian.Uint32(p)*xxP32_3, 17) * xxP32_4
	}
	for _, c := range p {
		h = bits.RotateLeft32(h+uint32(c)*xxP32_5, 11) * xxP32_1
	}
	h ^= h >> 15
	h *= xxP32_2
	h ^= h >> 13
	h *= xxP32_3
	h ^= h >> 16
	return binary.BigEndian.AppendUint32(b, h)
}

type xxh64 struct {
	v     [4]uint64
	buf   [32]byte
	nx    int
	total uint64
}

func newXXH64() hash.Hash {
	d := &xxh64{}
	d.Reset()
	return d
}

func (d *xxh64) Reset() {
	p1, p2 := xxP64_1, xxP64_2
	d.v = [4]uint64{p1 + p2, p2, 0, -p1}
	d.nx, d.total = 0, 0
}

func (d *xxh64) Size() int      { return 8 }
func (d *xxh64) BlockSize() int { return 32 }
func (d *xxh64) Clone() (hash.Cloner, error) {
	c := *d
	return &c, nil
}

func xxh64Round(acc, in uint64) uint64 {
	return bits.RotateLeft64(acc+in*xxP64_2, 31) * xxP64_1
}

func xxh64Merge(acc, v uint64) uint64 {
	acc ^= xxh64Round(0, v)
	return acc*xxP64_1 + xxP64_4
}

func xxh64Avalanche(h uint64) uint64 {
	h ^= h >> 33
	h *= xxP64_2
	h ^= h >> 29
	h *= xxP64_3
	h ^= h >> 32
	return h
}

func (d *xxh64) stripe(p []byte) {
	for i := range d.v {
		d.v[i] = xxh64Round(d.v[i], binary.LittleEndian.Uint64(p[8*i:]))
	}
}

func (d *xxh64) Write(p []byte) (int, error) {
	n := len(p)
	d.total += uint64(n)
	if d.nx > 0 {
		k := copy(d.buf[d.nx:], p)
		d.nx += k
		p = p[k:]
		if d.nx < 32 {
			return n, nil
		}
		d.stripe(d.buf[:])
		d.nx = 0
	}
	for len(p) >= 32 {
		d.stripe(p)
		p = p[32:]
	}
	d.nx = copy(d.buf[:], p)
	return n, nil
}

func (d *xxh64) Sum(b []byte) []byte {
	var h uint64
	if d.total >= 32 {
		h = bits.RotateLeft64(d.v[0], 1) + bits.RotateLeft64(d.v[1], 7) + bits.RotateLeft64(d.v[2], 12) + bits.RotateLeft64(d.v[3], 18)
		for _, v := range d.v {
			h = xxh64Merge(h, v)
		}
	} else {
		h = xxP64_5
	}
	h += d.total
	p := d.buf[:d.nx]
	for ; len(p) >= 8; p = p[8:] {
		h ^= xxh64Round(0, binary.LittleEndian.Uint64(p))
		h = bits.RotateLeft64(h, 27)*xxP64_1 + xxP64_4
	}
	if len(p) >= 4 {
		h ^= uint64(binary.LittleEndian.Uint32(p)) * xxP64_1
		h = bits.RotateLeft64(h, 23)*xxP64_2 + xxP64_3
		p = p[4:]
	}
	for _, c := range p {
		h ^= uint64(c) * xxP64_5
		h = bits.RotateLeft64(h, 11) * xxP64_1
	}
	return binary.BigEndian.AppendUint64(b, xxh64Avalanche(h))
}

// ---- xxh3 (64 bits, graine nulle, secret par défaut) ----

var xxh3Secret = [192]byte{
	0xb8, 0xfe, 0x6c, 0x39, 0x23, 0xa4, 0x4b, 0xbe, 0x7c, 0x01, 0x81, 0x2c, 0xf7, 0x21, 0xad, 0x1c,
	0xde, 0xd4, 0x6d, 0xe9, 0x83, 0x90, 0x97, 0xdb, 0x72, 0x40, 0xa4, 0xa4, 0xb7, 0xb3, 0x67, 0x1f,
	0xcb, 0x79, 0xe6, 0x4e, 0xcc, 0xc0, 0xe5, 0x78, 0x82, 0x5a, 0xd0, 0x7d, 0xcc, 0xff, 0x72, 0x21,
	0xb8, 0x08, 0x46, 0x74, 0xf7, 0x43, 0x24, 0x8e, 0xe0, 0x35, 0x90, 0xe6, 0x81, 0x3a, 0x26, 0x4c,
	0x3c, 0x28, 0x52, 0xbb, 0x91, 0xc3, 0x00, 0xcb, 0x88, 0xd0, 0x65, 0x8b, 0x1b, 0x53, 0x2e, 0xa3,
	0x71, 0x64, 0x48, 0x97, 0xa2, 0x0d, 0xf9, 0x4e, 0x38, 0x19, 0xef, 0x46, 0xa9, 0xde, 0xac, 0xd8,
	0xa8, 0xfa, 0x76, 0x3f, 0xe3, 0x9c, 0x34, 0x3f, 0xf9, 0xdc, 0xbb, 0xc7, 0xc7, 0x0b, 0x4f, 0x1d,
	0x8a, 0x51, 0xe0, 0x4b, 0xcd, 0xb4, 0x59, 0x31, 0xc8, 0x9f, 0x7e, 0xc9, 0xd9, 0x78, 0x73, 0x64,
	0xea, 0xc5, 0xac, 0x83, 0x34, 0xd3, 0xeb, 0xc3, 0xc5, 0x81, 0xa0, 0xff, 0xfa, 0x13, 0x63, 0xeb,
	0x17, 0x0d, 0xdd, 0x51, 0xb7, 0xf0, 0xda, 0x49, 0xd3, 0x16, 0x55, 0x26, 0x29, 0xd4, 0x68, 0x9e,
	0x2b, 0x16, 0xbe, 0x58, 0x7d, 0x47, 0xa1, 0xfc, 0x8f, 0xf8, 0xb8, 0xd1, 0x7a, 0xd0, 0x31, 0xce,
	0x45, 0xcb, 0x3a, 0x8f, 0x95, 0x16, 0x04, 0x28, 0xaf, 0xd7, 0xfb, 0xca, 0xbb, 0x4b, 0x40, 0x7e,
}

const (
	xxPMX1            uint64 = 0x165667919e3779f9
	xxPMX2            uint64 = 0x9fb21c651e98df25
	xxh3Stripe               = 64
	xxh3Block                = 1024 // (192 - 64) / 8 bandes de 64 octets par bloc
	xxh3StripesPer           = 16
	xxh3MidMax               = 240
	xxh3MidStart             = 3
	xxh3LastRound            = 17
	xxh3SecretMin            = 136
	xxh3LastAccOff           = 7
	xxh3MergeAccOff          = 11
	xxh3SecretLastS          = 192 - xxh3Stripe
	xxh3SecretLastA          = xxh3SecretLastS - xxh3LastAccOff
	xxh3SecretMidLast        = xxh3SecretMin - xxh3LastRound
)

func le64(p []byte) uint64 { return binary.LittleEndian.Uint64(p) }
func le32(p []byte) uint32 { return binary.LittleEndian.Uint32(p) }

func mulFold64(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	return hi ^ lo
}

func xxh3Avalanche(h uint64) uint64 {
	h ^= h >> 37
	h *= xxPMX1
	h ^= h >> 32
	return h
}

func xxh3Mix16(p, sec []byte) uint64 {
	return mulFold64(le64(p)^le64(sec), le64(p[8:])^le64(sec[8:]))
}

// xxh3Short calcule XXH3_64bits pour une entrée d'au plus 240 octets.
func xxh3Short(p []byte) uint64 {
	n := uint64(len(p))
	s := xxh3Secret[:]
	switch {
	case n == 0:
		return xxh64Avalanche(le64(s[56:]) ^ le64(s[64:]))
	case n <= 3:
		combined := uint32(p[0])<<16 | uint32(p[n>>1])<<24 | uint32(p[n-1]) | uint32(n)<<8
		return xxh64Avalanche(uint64(combined) ^ uint64(le32(s)^le32(s[4:])))
	case n <= 8:
		in64 := uint64(le32(p[n-4:])) + uint64(le32(p))<<32
		h := in64 ^ (le64(s[8:]) ^ le64(s[16:]))
		h ^= bits.RotateLeft64(h, 49) ^ bits.RotateLeft64(h, 24)
		h *= xxPMX2
		h ^= h>>35 + n
		h *= xxPMX2
		return h ^ h>>28
	case n <= 16:
		lo := le64(p) ^ (le64(s[24:]) ^ le64(s[32:]))
		hi := le64(p[n-8:]) ^ (le64(s[40:]) ^ le64(s[48:]))
		return xxh3Avalanche(n + bits.ReverseBytes64(lo) + hi + mulFold64(lo, hi))
	case n <= 128:
		acc := n * xxP64_1
		if n > 32 {
			if n > 64 {
				if n > 96 {
					acc += xxh3Mix16(p[48:], s[96:]) + xxh3Mix16(p[n-64:], s[112:])
				}
				acc += xxh3Mix16(p[32:], s[64:]) + xxh3Mix16(p[n-48:], s[80:])
			}
			acc += xxh3Mix16(p[16:], s[32:]) + xxh3Mix16(p[n-32:], s[48:])
		}
		acc += xxh3Mix16(p, s) + xxh3Mix16(p[n-16:], s[16:])
		return xxh3Avalanche(acc)
	default:
		acc := n * xxP64_1
		rounds := int(n / 16)
		for i := range 8 {
			acc += xxh3Mix16(p[16*i:], s[16*i:])
		}
		acc = xxh3Avalanche(acc)
		for i := 8; i < rounds; i++ {
			acc += xxh3Mix16(p[16*i:], s[16*(i-8)+xxh3MidStart:])
		}
		acc += xxh3Mix16(p[n-16:], s[xxh3SecretMidLast:])
		return xxh3Avalanche(acc)
	}
}

func xxh3Accumulate512(acc *[8]uint64, p, sec []byte) {
	for i := range 8 {
		v := le64(p[8*i:])
		k := v ^ le64(sec[8*i:])
		acc[i^1] += v
		acc[i] += uint64(uint32(k)) * (k >> 32)
	}
}

func xxh3Scramble(acc *[8]uint64) {
	sec := xxh3Secret[xxh3SecretLastS:]
	for i := range 8 {
		a := acc[i]
		a ^= a >> 47
		a ^= le64(sec[8*i:])
		acc[i] = a * uint64(xxP32_1)
	}
}

// xxh3 tient l'état du chemin long : blocs entiers déjà absorbés, entrée non absorbée et les 64 derniers
// octets absorbés, nécessaires à la dernière bande qui chevauche la fin du dernier bloc.
type xxh3 struct {
	acc    [8]uint64
	blocks uint64
	buf    []byte
	tail   [xxh3Stripe]byte
}

func newXXH3() hash.Hash {
	d := &xxh3{}
	d.Reset()
	return d
}

func (d *xxh3) Reset() {
	d.acc = [8]uint64{uint64(xxP32_3), xxP64_1, xxP64_2, xxP64_3, xxP64_4, uint64(xxP32_2), xxP64_5, uint64(xxP32_1)}
	d.blocks = 0
	d.buf = d.buf[:0]
}

func (d *xxh3) Size() int      { return 8 }
func (d *xxh3) BlockSize() int { return xxh3Stripe }
func (d *xxh3) Clone() (hash.Cloner, error) {
	c := *d
	c.buf = append([]byte(nil), d.buf...)
	return &c, nil
}

// Write n'absorbe un bloc que lorsqu'au moins un octet le suit, comme nb_blocks = (len-1)/1024 en un seul appel.
func (d *xxh3) Write(p []byte) (int, error) {
	d.buf = append(d.buf, p...)
	off := 0
	for len(d.buf)-off > xxh3Block {
		blk := d.buf[off : off+xxh3Block]
		for s := range xxh3StripesPer {
			xxh3Accumulate512(&d.acc, blk[s*xxh3Stripe:], xxh3Secret[8*s:])
		}
		xxh3Scramble(&d.acc)
		copy(d.tail[:], blk[xxh3Block-xxh3Stripe:])
		d.blocks++
		off += xxh3Block
	}
	if off > 0 {
		d.buf = append(d.buf[:0], d.buf[off:]...)
	}
	return len(p), nil
}

func (d *xxh3) Sum(b []byte) []byte {
	total := d.blocks*xxh3Block + uint64(len(d.buf))
	if total <= xxh3MidMax {
		return binary.BigEndian.AppendUint64(b, xxh3Short(d.buf))
	}
	acc := d.acc
	r := d.buf
	stripes := (len(r) - 1) / xxh3Stripe
	for s := range stripes {
		xxh3Accumulate512(&acc, r[s*xxh3Stripe:], xxh3Secret[8*s:])
	}
	var last [xxh3Stripe]byte
	if len(r) >= xxh3Stripe {
		copy(last[:], r[len(r)-xxh3Stripe:])
	} else {
		k := copy(last[:], d.tail[len(r):])
		copy(last[k:], r)
	}
	xxh3Accumulate512(&acc, last[:], xxh3Secret[xxh3SecretLastA:])
	h := total * xxP64_1
	for i := range 4 {
		sec := xxh3Secret[xxh3MergeAccOff+16*i:]
		h += mulFold64(acc[2*i]^le64(sec), acc[2*i+1]^le64(sec[8:]))
	}
	return binary.BigEndian.AppendUint64(b, xxh3Avalanche(h))
}
