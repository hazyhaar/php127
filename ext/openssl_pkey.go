package ext

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/des"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"hash"
	"io"
	"math/big"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"code.hazyhaar.fr/devhoros/php127/streams"
	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Constantes OPENSSL_ALGO_* de PHP 8.3 (OPENSSL_ALGO_MD4 n'est pas servi par openssl_sign).
const (
	opensslAlgoSHA1   = 1
	opensslAlgoMD5    = 2
	opensslAlgoMD4    = 3
	opensslAlgoSHA224 = 6
	opensslAlgoSHA256 = 7
	opensslAlgoSHA384 = 8
	opensslAlgoSHA512 = 9
	opensslAlgoRMD160 = 10
)

// Messages de la file d'erreurs d'OpenSSL 3 relayés par PHP 8.3 après un échec de lecture de clé.
const (
	sslErrDecoderUnsupported = "error:1E08010C:DECODER routines::unsupported"
	sslErrPEMBadDecrypt      = "error:04800065:PEM routines::bad decrypt"
)

// opensslCipherMethods est la liste des chiffres servis par openssl_encrypt et openssl_decrypt (lookupCipher),
// dans l'ordre trié de PHP ; aliases ajoute les identifiants id-aes*-gcm qu'OpenSSL 3 y joint.
var opensslCipherMethods = [...]string{
	"aes-128-cbc", "aes-128-ctr", "aes-128-ecb", "aes-128-gcm",
	"aes-192-cbc", "aes-192-ctr", "aes-192-ecb", "aes-192-gcm",
	"aes-256-cbc", "aes-256-ctr", "aes-256-ecb", "aes-256-gcm",
}

func nativeOpensslGetCipherMethods(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("openssl_get_cipher_methods", len(args), 0, 1); err != nil {
		return types.Value{}, err
	}
	names := opensslCipherMethods[:]
	if optBool(z, args, 0) {
		names = append(slices.Clone(names), "id-aes128-gcm", "id-aes192-gcm", "id-aes256-gcm")
	}
	return stringList(z, names)
}

func stringList(z *zone.Zone, names []string) (types.Value, error) {
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	for _, n := range names {
		s, err := newStr(z, n)
		if err != nil {
			return types.Value{}, err
		}
		if err = appendVal(z, &a, s); err != nil {
			return types.Value{}, err
		}
	}
	return a, nil
}

// readKeyMaterial lit un argument PEM ; « file:// » désigne un fichier, comme php_openssl_pkey_from_zval.
func readKeyMaterial(s string) (string, bool) {
	path, ok := strings.CutPrefix(s, "file://")
	if !ok {
		return s, true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// ---- clés privées ----

func storePKey(z *zone.Zone, key crypto.Signer) types.Value {
	st := extStateFor(z)
	if st.pkeys == nil {
		st.pkeys = make(map[uint32]crypto.Signer)
	}
	id := st.newHandle()
	st.pkeys[id] = key
	return streams.NewResource(z, resourceKindPKey, id)
}

// parsePrivateKey décode la première clé privée de data (PEM, sinon DER) ; sslErr porte les messages
// qu'OpenSSL déposerait dans sa file en cas d'échec.
func parsePrivateKey(data, pass string) (crypto.Signer, []string) {
	rest := []byte(data)
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		der := blk.Bytes
		var err error
		switch {
		case blk.Type == "ENCRYPTED PRIVATE KEY":
			if der, err = decryptPKCS8(der, pass); err != nil {
				return nil, []string{sslErrBadDecrypt, sslErrPEMBadDecrypt}
			}
		case blk.Headers["Proc-Type"] == "4,ENCRYPTED":
			if der, err = decryptLegacyPEM(der, blk.Headers["DEK-Info"], pass); err != nil {
				return nil, []string{sslErrBadDecrypt, sslErrPEMBadDecrypt}
			}
		case !strings.HasSuffix(blk.Type, "PRIVATE KEY"):
			continue
		}
		if k := parsePrivateDER(der); k != nil {
			return k, nil
		}
		return nil, []string{sslErrDecoderUnsupported}
	}
	if k := parsePrivateDER([]byte(data)); k != nil {
		return k, nil
	}
	return nil, []string{sslErrDecoderUnsupported}
}

func parsePrivateDER(der []byte) crypto.Signer {
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		if s, ok := k.(crypto.Signer); ok {
			return s
		}
	}
	if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return k
	}
	if k, err := x509.ParseECPrivateKey(der); err == nil {
		return k
	}
	return nil
}

var errBadDecrypt = errors.New("bad decrypt")

// cbcDecrypt déchiffre en CBC et retire le bourrage PKCS#7.
func cbcDecrypt(block cipher.Block, iv, data []byte) ([]byte, error) {
	bs := block.BlockSize()
	if len(iv) != bs || len(data) == 0 || len(data)%bs != 0 {
		return nil, errBadDecrypt
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	p := int(out[len(out)-1])
	if p == 0 || p > bs {
		return nil, errBadDecrypt
	}
	for _, c := range out[len(out)-p:] {
		if int(c) != p {
			return nil, errBadDecrypt
		}
	}
	return out[:len(out)-p], nil
}

// legacyCiphers associe les noms de DEK-Info servis à la longueur de clé et au constructeur de bloc.
var legacyCiphers = map[string]struct {
	keyLen int
	block  func([]byte) (cipher.Block, error)
}{
	"AES-128-CBC":  {16, aes.NewCipher},
	"AES-192-CBC":  {24, aes.NewCipher},
	"AES-256-CBC":  {32, aes.NewCipher},
	"DES-EDE3-CBC": {24, des.NewTripleDESCipher},
	"DES-CBC":      {8, des.NewCipher},
}

// decryptLegacyPEM suit PEM_do_header d'OpenSSL : clé tirée par EVP_BytesToKey(MD5, 1 tour, sel = 8 premiers octets de l'IV).
func decryptLegacyPEM(der []byte, dekInfo, pass string) ([]byte, error) {
	name, ivHex, ok := strings.Cut(dekInfo, ",")
	spec, known := legacyCiphers[strings.ToUpper(strings.TrimSpace(name))]
	iv, err := hex.DecodeString(strings.TrimSpace(ivHex))
	if !ok || !known || err != nil || len(iv) < 8 {
		return nil, errBadDecrypt
	}
	var key, prev []byte
	for len(key) < spec.keyLen {
		h := md5.New()
		h.Write(prev)
		h.Write([]byte(pass))
		h.Write(iv[:8])
		prev = h.Sum(nil)
		key = append(key, prev...)
	}
	block, err := spec.block(key[:spec.keyLen])
	if err != nil {
		return nil, errBadDecrypt
	}
	return cbcDecrypt(block, iv, der)
}

var (
	oidPBES2       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 13}
	oidPBKDF2      = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 12}
	oidHMACSHA1    = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 7}
	oidHMACSHA224  = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 8}
	oidHMACSHA256  = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 9}
	oidHMACSHA384  = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 10}
	oidHMACSHA512  = asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 11}
	oidAES128CBC   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 2}
	oidAES192CBC   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 22}
	oidAES256CBC   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42}
	oidDESEDE3CBC  = asn1.ObjectIdentifier{1, 2, 840, 113549, 3, 7}
	errUnsupported = errors.New("unsupported")
)

type pkixAlgo struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

// decryptPKCS8 déchiffre un EncryptedPrivateKeyInfo PBES2 (PBKDF2, AES-CBC ou DES-EDE3-CBC).
func decryptPKCS8(der []byte, pass string) ([]byte, error) {
	var epki struct {
		Algo pkixAlgo
		Data []byte
	}
	if _, err := asn1.Unmarshal(der, &epki); err != nil || !epki.Algo.Algorithm.Equal(oidPBES2) {
		return nil, errUnsupported
	}
	var params struct {
		KDF pkixAlgo
		Enc pkixAlgo
	}
	if _, err := asn1.Unmarshal(epki.Algo.Parameters.FullBytes, &params); err != nil || !params.KDF.Algorithm.Equal(oidPBKDF2) {
		return nil, errUnsupported
	}
	var kdf struct {
		Salt   []byte
		Iter   int
		KeyLen int      `asn1:"optional"`
		PRF    pkixAlgo `asn1:"optional"`
	}
	if _, err := asn1.Unmarshal(params.KDF.Parameters.FullBytes, &kdf); err != nil {
		return nil, errUnsupported
	}
	prf := sha1.New
	switch {
	case kdf.PRF.Algorithm == nil || kdf.PRF.Algorithm.Equal(oidHMACSHA1):
	case kdf.PRF.Algorithm.Equal(oidHMACSHA224):
		prf = sha256.New224
	case kdf.PRF.Algorithm.Equal(oidHMACSHA256):
		prf = sha256.New
	case kdf.PRF.Algorithm.Equal(oidHMACSHA384):
		prf = sha512.New384
	case kdf.PRF.Algorithm.Equal(oidHMACSHA512):
		prf = sha512.New
	default:
		return nil, errUnsupported
	}
	var keyLen int
	newBlock := aes.NewCipher
	switch e := params.Enc.Algorithm; {
	case e.Equal(oidAES128CBC):
		keyLen = 16
	case e.Equal(oidAES192CBC):
		keyLen = 24
	case e.Equal(oidAES256CBC):
		keyLen = 32
	case e.Equal(oidDESEDE3CBC):
		keyLen, newBlock = 24, des.NewTripleDESCipher
	default:
		return nil, errUnsupported
	}
	var iv []byte
	if _, err := asn1.Unmarshal(params.Enc.Parameters.FullBytes, &iv); err != nil {
		return nil, errUnsupported
	}
	key, err := pbkdf2.Key(func() hash.Hash { return prf() }, pass, kdf.Salt, kdf.Iter, keyLen)
	if err != nil {
		return nil, errBadDecrypt
	}
	block, err := newBlock(key)
	if err != nil {
		return nil, errBadDecrypt
	}
	return cbcDecrypt(block, iv, epki.Data)
}

// pkeyArg résout une clé privée : handle de openssl_pkey_get_private, chaîne PEM (ou « file:// »),
// ou tableau [clé, phrase de passe]. ok est faux si l'argument ne désigne pas une clé lisible.
func pkeyArg(z *zone.Zone, v types.Value, pass string) (crypto.Signer, bool) {
	v = deref(z, v)
	switch v.Type {
	case types.TypeResource:
		if v.Extra != resourceKindPKey {
			return nil, false
		}
		k := extStateFor(z).pkeys[v.DataSlot]
		return k, k != nil
	case types.TypeArray:
		if types.ArrayLen(z, v) != 2 {
			return nil, false
		}
		var key, pw types.Value
		i := 0
		types.ArrayForEach(z, v, func(_, item types.Value) bool {
			if i == 0 {
				key = item
			} else {
				pw = item
			}
			i++
			return true
		})
		p, err := asString(z, pw, "", "", "")
		if err != nil {
			return nil, false
		}
		return pkeyArg(z, key, p)
	case types.TypeString:
		data, ok := readKeyMaterial(types.StringData(z, v))
		if !ok {
			return nil, false
		}
		k, sslErrs := parsePrivateKey(data, pass)
		for _, e := range sslErrs {
			pushOpensslError(z, e)
		}
		return k, k != nil
	}
	return nil, false
}

func nativeOpensslPkeyGetPrivate(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "openssl_pkey_get_private"
	if err := arity(fn, len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	pass, err := optString(z, args, 1, fn, "2", "passphrase")
	if err != nil {
		return types.Value{}, err
	}
	if v := deref(z, args[0]); v.Type != types.TypeString && v.Type != types.TypeArray && v.Type != types.TypeResource {
		return types.Value{}, typeErr(fn, "1", "private_key", "OpenSSLAsymmetricKey|OpenSSLCertificate|array|string", typeKind(z, v))
	}
	k, ok := pkeyArg(z, args[0], pass)
	if !ok {
		return types.NewBool(false), nil
	}
	return storePKey(z, k), nil
}

// nativeOpensslPkeyFree retire la clé de la Zone ; PHP 8 la déclare obsolète et sans effet.
func nativeOpensslPkeyFree(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	if err := arity("openssl_pkey_free", len(args), 1, 1); err != nil {
		return types.Value{}, err
	}
	v := deref(z, args[0])
	if v.Type != types.TypeResource || v.Extra != resourceKindPKey {
		return types.Value{}, typeErr("openssl_pkey_free", "1", "key", "OpenSSLAsymmetricKey", typeKind(z, v))
	}
	delete(extStateFor(z).pkeys, v.DataSlot)
	return types.NewNull(), nil
}

// signDigest résout l'algorithme d'openssl_sign : constante OPENSSL_ALGO_* ou nom de condensat OpenSSL,
// sans casse (« sha256 », « RSA-SHA256 », « sha256WithRSAEncryption »).
func signDigest(z *zone.Zone, v types.Value) (crypto.Hash, bool) {
	v = deref(z, v)
	if v.Type != types.TypeString {
		n, err := asInt(z, v, "", "", "")
		if err != nil {
			return 0, false
		}
		switch n {
		case opensslAlgoSHA1:
			return crypto.SHA1, true
		case opensslAlgoMD5:
			return crypto.MD5, true
		case opensslAlgoSHA224:
			return crypto.SHA224, true
		case opensslAlgoSHA256:
			return crypto.SHA256, true
		case opensslAlgoSHA384:
			return crypto.SHA384, true
		case opensslAlgoSHA512:
			return crypto.SHA512, true
		case opensslAlgoRMD160:
			return crypto.RIPEMD160, true
		}
		return 0, false
	}
	name := strings.ToLower(types.StringData(z, v))
	name = strings.TrimPrefix(name, "rsa-")
	name = strings.TrimSuffix(name, "withrsaencryption")
	switch name {
	case "md5", "ssl3-md5":
		return crypto.MD5, true
	case "sha1", "ssl3-sha1":
		return crypto.SHA1, true
	case "sha224", "sha2-224":
		return crypto.SHA224, true
	case "sha256", "sha2-256":
		return crypto.SHA256, true
	case "sha384", "sha2-384":
		return crypto.SHA384, true
	case "sha512", "sha2-512":
		return crypto.SHA512, true
	case "sha512-224", "sha512/224", "sha2-512/224":
		return crypto.SHA512_224, true
	case "sha512-256", "sha512/256", "sha2-512/256":
		return crypto.SHA512_256, true
	case "sha3-224":
		return crypto.SHA3_224, true
	case "sha3-256":
		return crypto.SHA3_256, true
	case "sha3-384":
		return crypto.SHA3_384, true
	case "sha3-512":
		return crypto.SHA3_512, true
	case "ripemd160", "rmd160", "ripemd", "ripemd-160":
		return crypto.RIPEMD160, true
	}
	return 0, false
}

// ripemd160DigestInfo est l'en-tête DER de DigestInfo{ripemd160 (1.3.36.3.2.1), NULL} suivi d'OCTET STRING(20).
var ripemd160DigestInfo = []byte{0x30, 0x21, 0x30, 0x09, 0x06, 0x05, 0x2b, 0x24, 0x03, 0x02, 0x01, 0x05, 0x00, 0x04, 0x14}

// digestOf calcule le condensat de data ; RIPEMD-160 vient de hash_algos.go.
func digestOf(h crypto.Hash, data string) []byte {
	var w hash.Hash
	if h == crypto.RIPEMD160 {
		w = newRIPEMD160()
	} else {
		w = h.New()
	}
	_, _ = io.WriteString(w, data)
	return w.Sum(nil)
}

// nativeOpensslSign écrit dans $signature la signature PKCS#1 v1.5 (RSA), ASN.1 (ECDSA) ou Ed25519 de $data.
func nativeOpensslSign(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "openssl_sign"
	if err := arity(fn, len(args), 3, 4); err != nil {
		return types.Value{}, err
	}
	data, err := asString(z, args[0], fn, "1", "data")
	if err != nil {
		return types.Value{}, err
	}
	key, ok := pkeyArg(z, args[2], "")
	if !ok {
		return types.NewBool(false), nil
	}
	h := crypto.SHA1
	if len(args) == 4 && !args[3].IsUndef() {
		if h, ok = signDigest(z, args[3]); !ok {
			return types.NewBool(false), nil
		}
	}
	var sig []byte
	switch k := key.(type) {
	case *rsa.PrivateKey:
		if h == crypto.RIPEMD160 {
			// crypto/rsa préfixe RIPEMD-160 par l'OID ISO 1.0.10118.3.0.49 ; OpenSSL emploie l'OID
			// TeleTrusT 1.3.36.3.2.1 : le DigestInfo d'OpenSSL est donc signé tel quel.
			sig, err = rsa.SignPKCS1v15(nil, k, 0, append(slices.Clone(ripemd160DigestInfo), digestOf(h, data)...))
		} else {
			sig, err = rsa.SignPKCS1v15(nil, k, h, digestOf(h, data))
		}
	case *ecdsa.PrivateKey:
		sig, err = ecdsa.SignASN1(rand.Reader, k, digestOf(h, data))
	case ed25519.PrivateKey:
		sig = ed25519.Sign(k, []byte(data))
	default:
		err = errUnsupported
	}
	if err != nil {
		return types.NewBool(false), nil
	}
	sv, err := newStr(z, string(sig))
	if err != nil {
		return types.Value{}, err
	}
	writeRef(z, args[1], sv)
	types.ReleaseValue(z, &sv)
	return types.NewBool(true), nil
}

// ---- certificats X.509 ----

// x509Attr donne le nom court et le nom long OpenSSL d'un type d'attribut de nom distinctif.
var x509Attrs = map[string][2]string{
	"2.5.4.6":                    {"C", "countryName"},
	"2.5.4.8":                    {"ST", "stateOrProvinceName"},
	"2.5.4.7":                    {"L", "localityName"},
	"2.5.4.10":                   {"O", "organizationName"},
	"2.5.4.11":                   {"OU", "organizationalUnitName"},
	"2.5.4.3":                    {"CN", "commonName"},
	"2.5.4.5":                    {"serialNumber", "serialNumber"},
	"2.5.4.9":                    {"street", "streetAddress"},
	"2.5.4.12":                   {"title", "title"},
	"2.5.4.42":                   {"GN", "givenName"},
	"2.5.4.4":                    {"SN", "surname"},
	"2.5.4.17":                   {"postalCode", "postalCode"},
	"2.5.4.97":                   {"organizationIdentifier", "organizationIdentifier"},
	"1.2.840.113549.1.9.1":       {"emailAddress", "emailAddress"},
	"0.9.2342.19200300.100.1.25": {"DC", "domainComponent"},
	"0.9.2342.19200300.100.1.1":  {"UID", "userId"},
}

func attrName(oid asn1.ObjectIdentifier, short bool) string {
	n, ok := x509Attrs[oid.String()]
	if !ok {
		return oid.String()
	}
	if short {
		return n[0]
	}
	return n[1]
}

func attrValue(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	}
	return ""
}

// x509NameValue construit le tableau d'un nom ; une clé répétée devient une liste, comme add_assoc_name_entry.
func x509NameValue(z *zone.Zone, rdns pkix.RDNSequence, short bool) (types.Value, error) {
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	type entry struct {
		key  string
		vals []string
	}
	var entries []*entry
	for _, rdn := range rdns {
		for _, atv := range rdn {
			key := attrName(atv.Type, short)
			i := slices.IndexFunc(entries, func(e *entry) bool { return e.key == key })
			if i < 0 {
				entries = append(entries, &entry{key: key})
				i = len(entries) - 1
			}
			entries[i].vals = append(entries[i].vals, attrValue(atv.Value))
		}
	}
	for _, e := range entries {
		var v types.Value
		if len(e.vals) == 1 {
			v, err = newStr(z, e.vals[0])
		} else {
			v, err = stringList(z, e.vals)
		}
		if err != nil {
			return types.Value{}, err
		}
		if err = setStrKey(z, &a, e.key, v); err != nil {
			return types.Value{}, err
		}
	}
	return a, nil
}

// x509Oneline suit X509_NAME_oneline : « /C=FR/O=…/CN=… » en noms courts.
func x509Oneline(rdns pkix.RDNSequence) string {
	var b strings.Builder
	for _, rdn := range rdns {
		for _, atv := range rdn {
			b.WriteByte('/')
			b.WriteString(attrName(atv.Type, true))
			b.WriteByte('=')
			b.WriteString(attrValue(atv.Value))
		}
	}
	return b.String()
}

// x509NameHash suit X509_NAME_hash_ex : SHA-1 de l'encodage canonique (valeurs en UTF8String, blancs
// réduits, ASCII en minuscules, ensembles RDN concaténés sans l'en-tête de SEQUENCE), quatre premiers
// octets lus en petit-boutiste.
func x509NameHash(rdns pkix.RDNSequence) string {
	var canon []byte
	for _, rdn := range rdns {
		var members [][]byte
		for _, atv := range rdn {
			val := strings.Join(strings.Fields(attrValue(atv.Value)), " ")
			val = strings.Map(func(r rune) rune {
				if r >= 'A' && r <= 'Z' {
					return r + 'a' - 'A'
				}
				return r
			}, val)
			enc, err := asn1.Marshal(struct {
				Type  asn1.ObjectIdentifier
				Value asn1.RawValue
			}{atv.Type, asn1.RawValue{Tag: asn1.TagUTF8String, Bytes: []byte(val)}})
			if err != nil {
				return ""
			}
			members = append(members, enc)
		}
		slices.SortFunc(members, bytes.Compare)
		set, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSet, IsCompound: true, Bytes: bytes.Join(members, nil)})
		if err != nil {
			return ""
		}
		canon = append(canon, set...)
	}
	sum := sha1.Sum(canon)
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], binary.LittleEndian.Uint32(sum[:4]))
	return hex.EncodeToString(h[:])
}

// x509Time suit le format ASN.1 du certificat : UTCTime de 1950 à 2049, GeneralizedTime sinon (RFC 5280).
func x509Time(t time.Time) string {
	t = t.UTC()
	if t.Year() >= 1950 && t.Year() < 2050 {
		return t.Format("060102150405Z")
	}
	return t.Format("20060102150405Z")
}

// x509SerialDecimal suit i2s_ASN1_INTEGER : décimal sous 128 bits, « 0x » et hexadécimal majuscule au-delà.
func x509SerialDecimal(n *big.Int) string {
	if n.BitLen() < 128 {
		return n.String()
	}
	sign := ""
	if n.Sign() < 0 {
		sign = "-"
	}
	return sign + "0x" + strings.ToUpper(hex.EncodeToString(new(big.Int).Abs(n).Bytes()))
}

func x509SerialHex(n *big.Int) string {
	b := new(big.Int).Abs(n).Bytes()
	if len(b) == 0 {
		return "00"
	}
	return strings.ToUpper(hex.EncodeToString(b))
}

var x509SigAlgos = map[x509.SignatureAlgorithm]struct {
	sn, ln string
	nid    int64
}{
	x509.MD5WithRSA:       {"RSA-MD5", "md5WithRSAEncryption", 8},
	x509.SHA1WithRSA:      {"RSA-SHA1", "sha1WithRSAEncryption", 65},
	x509.SHA256WithRSA:    {"RSA-SHA256", "sha256WithRSAEncryption", 668},
	x509.SHA384WithRSA:    {"RSA-SHA384", "sha384WithRSAEncryption", 669},
	x509.SHA512WithRSA:    {"RSA-SHA512", "sha512WithRSAEncryption", 670},
	x509.ECDSAWithSHA1:    {"ecdsa-with-SHA1", "ecdsa-with-SHA1", 416},
	x509.ECDSAWithSHA256:  {"ecdsa-with-SHA256", "ecdsa-with-SHA256", 794},
	x509.ECDSAWithSHA384:  {"ecdsa-with-SHA384", "ecdsa-with-SHA384", 795},
	x509.ECDSAWithSHA512:  {"ecdsa-with-SHA512", "ecdsa-with-SHA512", 796},
	x509.SHA256WithRSAPSS: {"RSASSA-PSS", "rsassaPss", 912},
	x509.SHA384WithRSAPSS: {"RSASSA-PSS", "rsassaPss", 912},
	x509.SHA512WithRSAPSS: {"RSASSA-PSS", "rsassaPss", 912},
	x509.PureEd25519:      {"ED25519", "ED25519", 1087},
}

// Bits de ex_kusage et ex_xkusage d'OpenSSL (x509v3.h).
const (
	kuDigitalSignature = 0x0080
	kuNonRepudiation   = 0x0040
	kuKeyEncipherment  = 0x0020
	kuKeyAgreement     = 0x0008
	kuKeyCertSign      = 0x0004
	kuCRLSign          = 0x0002
	xkuSSLServer       = 0x1
	xkuSSLClient       = 0x2
	xkuSMIME           = 0x4
	xkuCodeSign        = 0x8
	xkuSGC             = 0x10
	xkuOCSPSign        = 0x20
	xkuTimestamp       = 0x40
	xkuDVCS            = 0x80
	xkuAnyEKU          = 0x100
)

var (
	oidExtKeyUsage    = asn1.ObjectIdentifier{2, 5, 29, 15}
	oidExtExtKeyUsage = asn1.ObjectIdentifier{2, 5, 29, 37}
	oidExtBasicCons   = asn1.ObjectIdentifier{2, 5, 29, 19}
)

// x509Flags rassemble les champs de x509v3_cache_extensions qu'utilisent les contrôles d'usage.
type x509Flags struct {
	hasKU, hasXKU, hasBC, isCA, v1Root bool
	ku, xku                            int
	xkuCritical                        bool
}

func x509CacheFlags(c *x509.Certificate) x509Flags {
	var f x509Flags
	for _, e := range c.Extensions {
		switch {
		case e.Id.Equal(oidExtKeyUsage):
			f.hasKU = true
		case e.Id.Equal(oidExtExtKeyUsage):
			f.hasXKU, f.xkuCritical = true, e.Critical
		case e.Id.Equal(oidExtBasicCons):
			f.hasBC = true
		}
	}
	f.isCA = c.BasicConstraintsValid && c.IsCA
	goKU := []struct {
		g x509.KeyUsage
		o int
	}{
		{x509.KeyUsageDigitalSignature, kuDigitalSignature}, {x509.KeyUsageContentCommitment, kuNonRepudiation},
		{x509.KeyUsageKeyEncipherment, kuKeyEncipherment}, {x509.KeyUsageDataEncipherment, 0x0010},
		{x509.KeyUsageKeyAgreement, kuKeyAgreement}, {x509.KeyUsageCertSign, kuKeyCertSign},
		{x509.KeyUsageCRLSign, kuCRLSign}, {x509.KeyUsageEncipherOnly, 0x0001}, {x509.KeyUsageDecipherOnly, 0x8000},
	}
	for _, m := range goKU {
		if c.KeyUsage&m.g != 0 {
			f.ku |= m.o
		}
	}
	for _, u := range c.ExtKeyUsage {
		switch u {
		case x509.ExtKeyUsageServerAuth:
			f.xku |= xkuSSLServer
		case x509.ExtKeyUsageClientAuth:
			f.xku |= xkuSSLClient
		case x509.ExtKeyUsageEmailProtection:
			f.xku |= xkuSMIME
		case x509.ExtKeyUsageCodeSigning:
			f.xku |= xkuCodeSign
		case x509.ExtKeyUsageNetscapeServerGatedCrypto, x509.ExtKeyUsageMicrosoftServerGatedCrypto:
			f.xku |= xkuSGC
		case x509.ExtKeyUsageOCSPSigning:
			f.xku |= xkuOCSPSign
		case x509.ExtKeyUsageTimeStamping:
			f.xku |= xkuTimestamp
		case x509.ExtKeyUsageAny:
			f.xku |= xkuAnyEKU
		}
	}
	selfSigned := bytes.Equal(c.RawSubject, c.RawIssuer) && c.CheckSignatureFrom(c) == nil
	f.v1Root = c.Version == 1 && selfSigned
	return f
}

func (f x509Flags) kuReject(u int) bool  { return f.hasKU && f.ku&u == 0 }
func (f x509Flags) xkuReject(u int) bool { return f.hasXKU && f.xku&u == 0 }

// checkCA suit check_ca d'OpenSSL 3 ; le type de certificat Netscape n'est pas lu.
func (f x509Flags) checkCA() bool {
	if f.kuReject(kuKeyCertSign) {
		return false
	}
	if f.hasBC {
		return f.isCA
	}
	return f.v1Root || f.hasKU
}

// x509Purpose suit X509_check_purpose(cert, id, ca) pour les identifiants 1 à 9 d'OpenSSL 3.
func x509Purpose(f x509Flags, id int, ca bool) bool {
	smime := func() bool {
		if f.xkuReject(xkuSMIME) {
			return false
		}
		if ca {
			return f.checkCA()
		}
		return true
	}
	switch id {
	case 1: // sslclient
		if f.xkuReject(xkuSSLClient) {
			return false
		}
		if ca {
			return f.checkCA()
		}
		return !f.kuReject(kuDigitalSignature | kuKeyAgreement)
	case 2, 3: // sslserver, nssslserver
		if f.xkuReject(xkuSSLServer | xkuSGC) {
			return false
		}
		if ca {
			return f.checkCA()
		}
		if f.kuReject(kuDigitalSignature | kuKeyEncipherment | kuKeyAgreement) {
			return false
		}
		return id == 2 || !f.kuReject(kuKeyEncipherment)
	case 4: // smimesign
		return smime() && (ca || !f.kuReject(kuDigitalSignature|kuNonRepudiation))
	case 5: // smimeencrypt
		return smime() && (ca || !f.kuReject(kuKeyEncipherment))
	case 6: // crlsign
		if ca {
			return f.checkCA()
		}
		return !f.kuReject(kuCRLSign)
	case 7: // any
		return true
	case 8: // ocsphelper
		return !ca || f.checkCA()
	case 9: // timestampsign
		if ca {
			return f.checkCA()
		}
		if f.hasKU && (f.ku&^(kuDigitalSignature|kuNonRepudiation) != 0 || f.ku&(kuDigitalSignature|kuNonRepudiation) == 0) {
			return false
		}
		return f.hasXKU && f.xku == xkuTimestamp && f.xkuCritical
	}
	return false
}

// x509PurposeNames donne, par identifiant, le nom court et le nom long (short_names = false) d'OpenSSL.
var x509PurposeNames = [...][2]string{
	1: {"sslclient", "SSL client"}, 2: {"sslserver", "SSL server"}, 3: {"nssslserver", "Netscape SSL server"},
	4: {"smimesign", "S/MIME signing"}, 5: {"smimeencrypt", "S/MIME encryption"}, 6: {"crlsign", "CRL signing"},
	7: {"any", "Any Purpose"}, 8: {"ocsphelper", "OCSP helper"}, 9: {"timestampsign", "Time Stamp signing"},
}

func hexColon(b []byte) string {
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = strings.ToUpper(hex.EncodeToString([]byte{c}))
	}
	return strings.Join(parts, ":")
}

var kuNames = []struct {
	g    x509.KeyUsage
	name string
}{
	{x509.KeyUsageDigitalSignature, "Digital Signature"}, {x509.KeyUsageContentCommitment, "Non Repudiation"},
	{x509.KeyUsageKeyEncipherment, "Key Encipherment"}, {x509.KeyUsageDataEncipherment, "Data Encipherment"},
	{x509.KeyUsageKeyAgreement, "Key Agreement"}, {x509.KeyUsageCertSign, "Certificate Sign"},
	{x509.KeyUsageCRLSign, "CRL Sign"}, {x509.KeyUsageEncipherOnly, "Encipher Only"}, {x509.KeyUsageDecipherOnly, "Decipher Only"},
}

var ekuNames = map[x509.ExtKeyUsage]string{
	x509.ExtKeyUsageServerAuth:      "TLS Web Server Authentication",
	x509.ExtKeyUsageClientAuth:      "TLS Web Client Authentication",
	x509.ExtKeyUsageCodeSigning:     "Code Signing",
	x509.ExtKeyUsageEmailProtection: "E-mail Protection",
	x509.ExtKeyUsageTimeStamping:    "Time Stamping",
	x509.ExtKeyUsageOCSPSigning:     "OCSP Signing",
	x509.ExtKeyUsageAny:             "Any Extended Key Usage",
}

// x509Extension rend le nom court et le texte de X509V3_EXT_print pour les extensions servies ; ok est faux
// pour une extension dont l'impression n'est pas reproduite (elle est alors omise).
func x509Extension(c *x509.Certificate, e pkix.Extension) (name, text string, ok bool) {
	switch e.Id.String() {
	case "2.5.29.14":
		return "subjectKeyIdentifier", hexColon(c.SubjectKeyId), true
	case "2.5.29.35":
		return "authorityKeyIdentifier", hexColon(c.AuthorityKeyId), true
	case "2.5.29.19":
		t := "CA:FALSE"
		if c.IsCA {
			t = "CA:TRUE"
			if c.MaxPathLen > 0 || c.MaxPathLenZero {
				t += ", pathlen:" + strconv.Itoa(c.MaxPathLen)
			}
		}
		return "basicConstraints", t, true
	case "2.5.29.15":
		var parts []string
		for _, k := range kuNames {
			if c.KeyUsage&k.g != 0 {
				parts = append(parts, k.name)
			}
		}
		return "keyUsage", strings.Join(parts, ", "), true
	case "2.5.29.37":
		var parts []string
		for _, u := range c.ExtKeyUsage {
			if n, ok := ekuNames[u]; ok {
				parts = append(parts, n)
			}
		}
		for _, o := range c.UnknownExtKeyUsage {
			parts = append(parts, o.String())
		}
		return "extendedKeyUsage", strings.Join(parts, ", "), true
	case "2.5.29.17":
		text, ok := x509SANText(e.Value)
		return "subjectAltName", text, ok
	}
	return "", "", false
}

// x509SANText imprime les GeneralName dans l'ordre de l'extension, comme i2v_GENERAL_NAMES.
func x509SANText(der []byte) (string, bool) {
	var names []asn1.RawValue
	if rest, err := asn1.Unmarshal(der, &names); err != nil || len(rest) != 0 {
		return "", false
	}
	parts := make([]string, 0, len(names))
	for _, n := range names {
		if n.Class != asn1.ClassContextSpecific {
			return "", false
		}
		switch n.Tag {
		case 1:
			parts = append(parts, "email:"+string(n.Bytes))
		case 2:
			parts = append(parts, "DNS:"+string(n.Bytes))
		case 6:
			parts = append(parts, "URI:"+string(n.Bytes))
		case 7:
			switch len(n.Bytes) {
			case 4:
				parts = append(parts, "IP Address:"+net.IP(n.Bytes).String())
			case 16:
				parts = append(parts, "IP Address:"+strings.ToUpper(expandIPv6(n.Bytes)))
			default:
				parts = append(parts, "IP Address:<invalid>")
			}
		case 8:
			var oid asn1.ObjectIdentifier
			if _, err := asn1.Unmarshal(append([]byte{asn1.TagOID, byte(len(n.Bytes))}, n.Bytes...), &oid); err != nil {
				return "", false
			}
			parts = append(parts, "Registered ID:"+oid.String())
		case 4:
			var rdns pkix.RDNSequence
			if _, err := asn1.Unmarshal(n.Bytes, &rdns); err != nil {
				return "", false
			}
			parts = append(parts, "DirName:"+x509Oneline(rdns))
		default:
			parts = append(parts, "othername:<unsupported>")
		}
	}
	return strings.Join(parts, ", "), true
}

// expandIPv6 écrit les huit groupes sans abréviation, comme i2v_GENERAL_NAME.
func expandIPv6(ip []byte) string {
	ip16 := make([]byte, 16)
	copy(ip16, ip)
	parts := make([]string, 8)
	for i := range parts {
		parts[i] = strconv.FormatUint(uint64(binary.BigEndian.Uint16(ip16[2*i:])), 16)
	}
	return strings.Join(parts, ":")
}

// parseCertificateArg lit un certificat PEM, ou un fichier désigné par « file:// ».
func parseCertificateArg(s string) (*x509.Certificate, bool) {
	data, ok := readKeyMaterial(s)
	if !ok {
		return nil, false
	}
	rest := []byte(data)
	for {
		var blk *pem.Block
		if blk, rest = pem.Decode(rest); blk == nil {
			return nil, false
		}
		if blk.Type == "CERTIFICATE" || blk.Type == "TRUSTED CERTIFICATE" || blk.Type == "X509 CERTIFICATE" {
			c, err := x509.ParseCertificate(blk.Bytes)
			return c, err == nil
		}
	}
}

// nativeOpensslX509Parse rend le tableau de PHP 8.3 (même ordre de clés) ; seules les extensions
// subjectKeyIdentifier, authorityKeyIdentifier, basicConstraints, keyUsage, extendedKeyUsage et
// subjectAltName sont imprimées.
func nativeOpensslX509Parse(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "openssl_x509_parse"
	if err := arity(fn, len(args), 1, 2); err != nil {
		return types.Value{}, err
	}
	s, err := asString(z, args[0], fn, "1", "certificate")
	if err != nil {
		return types.Value{}, err
	}
	short := true
	if len(args) == 2 && !missing(deref(z, args[1])) {
		short = asBool(z, args[1])
	}
	c, ok := parseCertificateArg(s)
	if !ok {
		return types.NewBool(false), nil
	}
	var subject, issuer pkix.RDNSequence
	if _, err := asn1.Unmarshal(c.RawSubject, &subject); err != nil {
		return types.NewBool(false), nil
	}
	if _, err := asn1.Unmarshal(c.RawIssuer, &issuer); err != nil {
		return types.NewBool(false), nil
	}
	a, err := newArray(z)
	if err != nil {
		return types.Value{}, err
	}
	set := func(key string, v types.Value, e error) {
		if err == nil && e != nil {
			err = e
		}
		if err == nil {
			err = setStrKey(z, &a, key, v)
		}
	}
	str := func(key, text string) {
		v, e := newStr(z, text)
		set(key, v, e)
	}
	str("name", x509Oneline(subject))
	sv, e := x509NameValue(z, subject, short)
	set("subject", sv, e)
	str("hash", x509NameHash(subject))
	iv, e := x509NameValue(z, issuer, short)
	set("issuer", iv, e)
	set("version", types.NewLong(int64(c.Version-1)), nil)
	str("serialNumber", x509SerialDecimal(c.SerialNumber))
	str("serialNumberHex", x509SerialHex(c.SerialNumber))
	str("validFrom", x509Time(c.NotBefore))
	str("validTo", x509Time(c.NotAfter))
	set("validFrom_time_t", types.NewLong(c.NotBefore.Unix()), nil)
	set("validTo_time_t", types.NewLong(c.NotAfter.Unix()), nil)
	sig, known := x509SigAlgos[c.SignatureAlgorithm]
	if !known {
		sig.sn, sig.ln = "UNDEF", "undefined"
	}
	str("signatureTypeSN", sig.sn)
	str("signatureTypeLN", sig.ln)
	set("signatureTypeNID", types.NewLong(sig.nid), nil)
	flags := x509CacheFlags(c)
	purposes, e := newArray(z)
	if e != nil {
		return types.Value{}, e
	}
	for id := 1; id < len(x509PurposeNames); id++ {
		p, e := newArray(z)
		if e == nil {
			e = appendVal(z, &p, types.NewBool(x509Purpose(flags, id, false)))
		}
		if e == nil {
			e = appendVal(z, &p, types.NewBool(x509Purpose(flags, id, true)))
		}
		var name types.Value
		if e == nil {
			pn := x509PurposeNames[id][1]
			if short {
				pn = x509PurposeNames[id][0]
			}
			name, e = newStr(z, pn)
		}
		if e == nil {
			e = appendVal(z, &p, name)
		}
		if e == nil {
			e = types.ArraySet(z, &purposes, types.NewLong(int64(id)), p, nil)
		}
		if e != nil {
			return types.Value{}, e
		}
	}
	set("purposes", purposes, nil)
	exts, e := newArray(z)
	if e != nil {
		return types.Value{}, e
	}
	for _, ext := range c.Extensions {
		name, text, ok := x509Extension(c, ext)
		if !ok {
			continue
		}
		v, e := newStr(z, text)
		if e == nil {
			e = setStrKey(z, &exts, name, v)
		}
		if e != nil {
			return types.Value{}, e
		}
	}
	set("extensions", exts, nil)
	if err != nil {
		return types.Value{}, err
	}
	return a, nil
}
