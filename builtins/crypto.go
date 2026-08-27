// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/builtins: Primitives cryptographiques et hachage PHP 8.4

package builtins

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"strings"

	"code.hazyhaar.fr/devhoros/php127/types"
)

func Md5(v types.Value) types.Value {
	h := md5.Sum(v.Bytes())
	return types.NewString(hex.EncodeToString(h[:]))
}

func Sha256(v types.Value) types.Value {
	h := sha256.Sum256(v.Bytes())
	return types.NewString(hex.EncodeToString(h[:]))
}

func Hash(algoVal, dataVal types.Value) types.Value {
	algo := strings.ToLower(algoVal.String())
	data := dataVal.Bytes()

	switch algo {
	case "md5":
		h := md5.Sum(data)
		return types.NewString(hex.EncodeToString(h[:]))
	case "sha256":
		h := sha256.Sum256(data)
		return types.NewString(hex.EncodeToString(h[:]))
	case "sha512":
		h := sha512.Sum512(data)
		return types.NewString(hex.EncodeToString(h[:]))
	default:
		return types.NewBool(false)
	}
}

func HashHmac(algoVal, dataVal, keyVal types.Value) types.Value {
	algo := strings.ToLower(algoVal.String())
	data := dataVal.Bytes()
	key := keyVal.Bytes()

	switch algo {
	case "sha256":
		mac := hmac.New(sha256.New, key)
		mac.Write(data)
		return types.NewString(hex.EncodeToString(mac.Sum(nil)))
	case "sha512":
		mac := hmac.New(sha512.New, key)
		mac.Write(data)
		return types.NewString(hex.EncodeToString(mac.Sum(nil)))
	case "md5":
		mac := hmac.New(md5.New, key)
		mac.Write(data)
		return types.NewString(hex.EncodeToString(mac.Sum(nil)))
	default:
		return types.NewBool(false)
	}
}

func RandomBytes(lenVal types.Value) types.Value {
	n := lenVal.ToLong()
	if n <= 0 {
		return types.NewString("")
	}
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return types.NewStringBytes(buf)
}

func Bin2Hex(v types.Value) types.Value {
	return types.NewString(hex.EncodeToString(v.Bytes()))
}

func Hex2Bin(v types.Value) types.Value {
	b, err := hex.DecodeString(v.String())
	if err != nil {
		return types.NewBool(false)
	}
	return types.NewStringBytes(b)
}
