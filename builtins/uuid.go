// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/builtins: Implémentation native ramsey/uuid et symfony/uid (UUID v4, v7, ULID)

package builtins

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"

	"code.hazyhaar.fr/devhoros/php127/types"
)

// UuidV4 génère un identifiant UUID v4 conforme RFC 4122
func UuidV4() types.Value {
	var buf [16]byte
	_, _ = rand.Read(buf[:])

	// Version 4 (bits 4-7 de octet 6 = 0100)
	buf[6] = (buf[6] & 0x0f) | 0x40
	// Variant 1 (bits 6-7 de octet 8 = 10)
	buf[8] = (buf[8] & 0x3f) | 0x80

	uuidStr := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4],
		buf[4:6],
		buf[6:8],
		buf[8:10],
		buf[10:16],
	)
	return types.NewString(uuidStr)
}

// UuidV7 génère un identifiant UUID v7 ordonnancé dans le temps conforme RFC 9562
func UuidV7() types.Value {
	var buf [16]byte
	_, _ = rand.Read(buf[:])

	// Timestamp Unix millisecondes (48 bits = 6 octets)
	ms := uint64(time.Now().UnixMilli())
	buf[0] = byte(ms >> 40)
	buf[1] = byte(ms >> 32)
	buf[2] = byte(ms >> 24)
	buf[3] = byte(ms >> 16)
	buf[4] = byte(ms >> 8)
	buf[5] = byte(ms)

	// Version 7
	buf[6] = (buf[6] & 0x0f) | 0x70
	// Variant 1
	buf[8] = (buf[8] & 0x3f) | 0x80

	uuidStr := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4],
		buf[4:6],
		buf[6:8],
		buf[8:10],
		buf[10:16],
	)
	return types.NewString(uuidStr)
}

// Crockford Base32 alphabet for ULID
const crockford32 = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Ulid génère un ULID 128-bit ordonnancé
func Ulid() types.Value {
	var entropy [10]byte
	_, _ = rand.Read(entropy[:])

	ms := uint64(time.Now().UnixMilli())
	var timeBytes [6]byte
	timeBytes[0] = byte(ms >> 40)
	timeBytes[1] = byte(ms >> 32)
	timeBytes[2] = byte(ms >> 24)
	timeBytes[3] = byte(ms >> 16)
	timeBytes[4] = byte(ms >> 8)
	timeBytes[5] = byte(ms)

	// Encode 16 bytes into 26 Crockford Base32 chars
	var raw [16]byte
	copy(raw[0:6], timeBytes[:])
	copy(raw[6:16], entropy[:])

	res := make([]byte, 26)
	// 10 chars for time (50 bits, ms is 48 bits)
	res[0] = crockford32[(raw[0]&224)>>5]
	res[1] = crockford32[raw[0]&31]
	res[2] = crockford32[(raw[1]&248)>>3]
	res[3] = crockford32[((raw[1]&7)<<2)|((raw[2]&192)>>6)]
	res[4] = crockford32[(raw[2]&62)>>1]
	res[5] = crockford32[((raw[2]&1)<<4)|((raw[3]&240)>>4)]
	res[6] = crockford32[((raw[3]&15)<<1)|((raw[4]&128)>>7)]
	res[7] = crockford32[(raw[4]&124)>>2]
	res[8] = crockford32[((raw[4]&3)<<3)|((raw[5]&224)>>5)]
	res[9] = crockford32[raw[5]&31]

	// 16 chars for 80 bits entropy
	res[10] = crockford32[(raw[6]&248)>>3]
	res[11] = crockford32[((raw[6]&7)<<2)|((raw[7]&192)>>6)]
	res[12] = crockford32[(raw[7]&62)>>1]
	res[13] = crockford32[((raw[7]&1)<<4)|((raw[8]&240)>>4)]
	res[14] = crockford32[((raw[8]&15)<<1)|((raw[9]&128)>>7)]
	res[15] = crockford32[(raw[9]&124)>>2]
	res[16] = crockford32[((raw[9]&3)<<3)|((raw[10]&224)>>5)]
	res[17] = crockford32[raw[10]&31]
	res[18] = crockford32[(raw[11]&248)>>3]
	res[19] = crockford32[((raw[11]&7)<<2)|((raw[12]&192)>>6)]
	res[20] = crockford32[(raw[12]&62)>>1]
	res[21] = crockford32[((raw[12]&1)<<4)|((raw[13]&240)>>4)]
	res[22] = crockford32[((raw[13]&15)<<1)|((raw[14]&128)>>7)]
	res[23] = crockford32[(raw[14]&124)>>2]
	res[24] = crockford32[((raw[14]&3)<<3)|((raw[15]&224)>>5)]
	res[25] = crockford32[raw[15]&31]

	_ = hex.EncodeToString
	_ = binary.BigEndian
	return types.NewString(string(res))
}
