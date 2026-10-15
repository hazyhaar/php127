package mysqli

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Indicateurs de capacité du protocole client/serveur MySQL.
const (
	capLongPassword     = 0x00000001
	capFoundRows        = 0x00000002
	capLongFlag         = 0x00000004
	capConnectWithDB    = 0x00000008
	capIgnoreSpace      = 0x00000100
	capProtocol41       = 0x00000200
	capInteractive      = 0x00000400
	capSSL              = 0x00000800
	capTransactions     = 0x00002000
	capSecureConnection = 0x00008000
	capMultiResults     = 0x00020000
	capPSMultiResults   = 0x00040000
	capPluginAuth       = 0x00080000
)

// Commandes du protocole.
const (
	comQuit        = 0x01
	comInitDB      = 0x02
	comQuery       = 0x03
	comStatistics  = 0x09
	comPing        = 0x0e
	comStmtPrepare = 0x16
	comStmtExecute = 0x17
	comStmtClose   = 0x19
	comSetOption   = 0x1b
)

const (
	statusMoreResults = 0x0008
	maxPacket         = 0xffffff
	// utf8mb4_general_ci : identifiant commun à MySQL et MariaDB.
	handshakeCharset = 45
)

// tcpBackend parle le protocole MySQL (texte et binaire) à un serveur MySQL ou MariaDB réel.
type tcpBackend struct {
	conn        net.Conn
	r           *bufio.Reader
	seq         byte
	host        string
	server      string
	connID      uint32
	caps        uint32
	readTimeout time.Duration
	dbName      string
}

// connectError traduit une erreur réseau en erreur client mysqlnd (2002).
func connectError(host string, err error) *sqlError {
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return newSQLError(crConnectionError, "HY000", "Connection refused")
	case errors.Is(err, syscall.ENOENT) || errors.Is(err, os.ErrNotExist):
		return newSQLError(crConnectionError, "HY000", "No such file or directory")
	case errors.As(err, &dnsErr):
		return newSQLError(crConnectionError, "HY000", "php_network_getaddresses: getaddrinfo for "+host+" failed: Name or service not known")
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return newSQLError(crConnectionError, "HY000", "Connection timed out")
	}
	return newSQLError(crConnectionError, "HY000", err.Error())
}

// dialMySQL ouvre une session authentifiée ; network vaut « tcp » ou « unix ».
func dialMySQL(network, addr, hostLabel, user, pass, db string, clientFlags int64, connectTimeout, readTimeout time.Duration) (*tcpBackend, *sqlError) {
	conn, err := net.DialTimeout(network, addr, connectTimeout)
	if err != nil {
		return nil, connectError(hostLabel, err)
	}
	b := &tcpBackend{conn: conn, r: bufio.NewReaderSize(conn, 16384), readTimeout: readTimeout, dbName: db}
	if network == "unix" {
		b.host = "Localhost via UNIX socket"
	} else {
		b.host = hostLabel + " via TCP/IP"
	}
	_ = conn.SetDeadline(time.Now().Add(connectTimeout))
	if serr := b.handshake(user, pass, db, clientFlags); serr != nil {
		_ = conn.Close()
		return nil, serr
	}
	_ = conn.SetDeadline(time.Time{})
	return b, nil
}

func (b *tcpBackend) lost(err error) *sqlError {
	if b.conn != nil {
		_ = b.conn.Close()
		b.conn = nil
	}
	if errors.Is(err, io.EOF) {
		return newSQLError(crServerGone, "HY000", "MySQL server has gone away")
	}
	return newSQLError(crServerLost, "HY000", "Lost connection to MySQL server during query: "+err.Error())
}

func (b *tcpBackend) readPacket() ([]byte, error) {
	if b.conn == nil {
		return nil, io.EOF
	}
	if b.readTimeout > 0 {
		_ = b.conn.SetReadDeadline(time.Now().Add(b.readTimeout))
	}
	var payload []byte
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(b.r, hdr[:]); err != nil {
			return nil, err
		}
		n := int(uint32(hdr[0]) | uint32(hdr[1])<<8 | uint32(hdr[2])<<16)
		b.seq = hdr[3] + 1
		chunk := make([]byte, n)
		if _, err := io.ReadFull(b.r, chunk); err != nil {
			return nil, err
		}
		payload = append(payload, chunk...)
		if n < maxPacket {
			return payload, nil
		}
	}
}

func (b *tcpBackend) writePacket(payload []byte) error {
	if b.conn == nil {
		return io.EOF
	}
	for {
		n := min(len(payload), maxPacket)
		buf := make([]byte, 4+n)
		buf[0], buf[1], buf[2], buf[3] = byte(n), byte(n>>8), byte(n>>16), b.seq
		copy(buf[4:], payload[:n])
		if _, err := b.conn.Write(buf); err != nil {
			return err
		}
		b.seq++
		payload = payload[n:]
		if n < maxPacket {
			return nil
		}
	}
}

func (b *tcpBackend) command(cmd byte, data []byte) error {
	b.seq = 0
	return b.writePacket(append([]byte{cmd}, data...))
}

// Lecture des types du protocole.

// readLenEncInt lit un entier à longueur codée ; k est le nombre d'octets consommés, null signale 0xfb (NULL)
// et ok est faux si l'entrée est vide ou tronquée.
func readLenEncInt(p []byte) (v uint64, k int, null, ok bool) {
	if len(p) == 0 {
		return 0, 0, false, false
	}
	switch p[0] {
	case 0xfb:
		return 0, 1, true, true
	case 0xfc:
		if len(p) < 3 {
			return 0, 0, false, false
		}
		return uint64(binary.LittleEndian.Uint16(p[1:])), 3, false, true
	case 0xfd:
		if len(p) < 4 {
			return 0, 0, false, false
		}
		return uint64(p[1]) | uint64(p[2])<<8 | uint64(p[3])<<16, 4, false, true
	case 0xfe:
		if len(p) < 9 {
			return 0, 0, false, false
		}
		return binary.LittleEndian.Uint64(p[1:]), 9, false, true
	}
	return uint64(p[0]), 1, false, true
}

// readLenEncString lit une chaîne à longueur codée ; ok est faux si l'entête ou la charge annoncée dépasse p.
func readLenEncString(p []byte) (s string, k int, null, ok bool) {
	n, k, null, ok := readLenEncInt(p)
	if !ok || null {
		return "", k, null, ok
	}
	if n > uint64(len(p)-k) {
		return "", 0, false, false
	}
	end := k + int(n)
	return string(p[k:end]), end, false, true
}

func appendLenEnc(buf []byte, n uint64) []byte {
	switch {
	case n < 251:
		return append(buf, byte(n))
	case n < 1<<16:
		return append(buf, 0xfc, byte(n), byte(n>>8))
	case n < 1<<24:
		return append(buf, 0xfd, byte(n), byte(n>>8), byte(n>>16))
	}
	buf = append(buf, 0xfe)
	return binary.LittleEndian.AppendUint64(buf, n)
}

func parseErrPacket(p []byte) *sqlError {
	if len(p) < 3 {
		return newSQLError(crServerLost, "HY000", "Malformed error packet")
	}
	code := int(binary.LittleEndian.Uint16(p[1:]))
	rest := p[3:]
	state := "HY000"
	if len(rest) >= 6 && rest[0] == '#' {
		state = string(rest[1:6])
		rest = rest[6:]
	}
	return newSQLError(code, state, string(rest))
}

type okPacket struct {
	affected uint64
	insertID uint64
	status   uint16
	warnings uint16
	info     string
}

// parseOK décode un paquet OK du protocole 4.1 : deux entiers à longueur codée puis l'état et le nombre
// d'avertissements sur quatre octets. Un paquet tronqué avant ces quatre octets est refusé.
func parseOK(p []byte) (okPacket, bool) {
	var ok okPacket
	i := 1
	var k int
	var valid bool
	if ok.affected, k, _, valid = readLenEncInt(p[min(i, len(p)):]); !valid {
		return ok, false
	}
	i += k
	if ok.insertID, k, _, valid = readLenEncInt(p[min(i, len(p)):]); !valid {
		return ok, false
	}
	i += k
	if i+4 > len(p) {
		return ok, false
	}
	ok.status = binary.LittleEndian.Uint16(p[i:])
	ok.warnings = binary.LittleEndian.Uint16(p[i+2:])
	i += 4
	if i < len(p) {
		ok.info = string(p[i:])
	}
	return ok, true
}

// isEOF reconnaît un paquet EOF du protocole 4.1 : le marqueur 0xfe, deux octets d'avertissements et deux
// octets d'état, soit cinq octets au moins ; à neuf octets ou plus, 0xfe ouvre un entier à huit octets d'une
// ligne de résultat.
func isEOF(p []byte) bool { return len(p) >= 5 && p[0] == 0xfe && len(p) < 9 }

// truncatedEOF signale un paquet de marqueur 0xfe trop court pour une ligne et refusé par isEOF : ni fin de
// résultat valide, ni ligne, il est mal formé.
func truncatedEOF(p []byte) bool { return len(p) > 0 && len(p) < 9 && p[0] == 0xfe && !isEOF(p) }

func eofStatus(p []byte) uint16 {
	if len(p) >= 5 {
		return binary.LittleEndian.Uint16(p[3:])
	}
	return 0
}

// Authentification.

func scrambleNative(nonce []byte, pass string) []byte {
	if pass == "" {
		return nil
	}
	h1 := sha1.Sum([]byte(pass))
	h2 := sha1.Sum(h1[:])
	h := sha1.New()
	h.Write(nonce[:min(20, len(nonce))])
	h.Write(h2[:])
	h3 := h.Sum(nil)
	for i := range h3 {
		h3[i] ^= h1[i]
	}
	return h3
}

func scrambleSHA256(nonce []byte, pass string) []byte {
	if pass == "" {
		return nil
	}
	h1 := sha256.Sum256([]byte(pass))
	h2 := sha256.Sum256(h1[:])
	h := sha256.New()
	h.Write(h2[:])
	h.Write(nonce[:min(20, len(nonce))])
	h3 := h.Sum(nil)
	for i := range h3 {
		h3[i] ^= h1[i]
	}
	return h3
}

func authResponse(plugin string, nonce []byte, pass string) ([]byte, *sqlError) {
	switch plugin {
	case "", "mysql_native_password":
		return scrambleNative(nonce, pass), nil
	case "caching_sha2_password":
		return scrambleSHA256(nonce, pass), nil
	case "mysql_clear_password":
		return append([]byte(pass), 0), nil
	}
	return nil, newSQLError(2059, "HY000", "Authentication plugin '"+plugin+"' cannot be loaded")
}

func (b *tcpBackend) handshake(user, pass, db string, clientFlags int64) *sqlError {
	p, err := b.readPacket()
	if err != nil {
		return b.lost(err)
	}
	if len(p) > 0 && p[0] == 0xff {
		return parseErrPacket(p)
	}
	if len(p) < 1 || p[0] != 10 {
		return newSQLError(2027, "HY000", "Malformed packet: unsupported protocol version")
	}
	i := 1
	end := strings.IndexByte(string(p[i:]), 0)
	if end < 0 {
		return newSQLError(2027, "HY000", "Malformed packet")
	}
	b.server = string(p[i : i+end])
	i += end + 1
	// Identifiant de connexion (4 octets), première moitié du nonce et son NUL (9), capacités basses (2).
	if i+15 > len(p) {
		return newSQLError(2027, "HY000", "Malformed packet")
	}
	b.connID = binary.LittleEndian.Uint32(p[i:])
	i += 4
	nonce := append([]byte(nil), p[i:i+8]...)
	i += 9
	serverCaps := uint32(binary.LittleEndian.Uint16(p[i:]))
	i += 2
	plugin := ""
	if i+16 <= len(p) {
		serverCaps |= uint32(binary.LittleEndian.Uint16(p[i+3:])) << 16
		authLen := int(p[i+5])
		i += 16
		n := max(13, authLen-8)
		if i+n <= len(p) {
			nonce = append(nonce, p[i:i+n-1]...)
			i += n
		}
		if i < len(p) {
			name := p[i:]
			if z := strings.IndexByte(string(name), 0); z >= 0 {
				name = name[:z]
			}
			plugin = string(name)
		}
	}
	if uint32(clientFlags)&capSSL != 0 {
		// La négociation TLS du protocole n'est pas implémentée : une demande de chiffrement échoue
		// explicitement au lieu d'ouvrir une session en clair.
		return newSQLError(2026, "HY000", "SSL connection error: TLS is not supported by the php127 mysqli client")
	}
	caps := uint32(capLongPassword | capLongFlag | capProtocol41 | capTransactions | capSecureConnection |
		capMultiResults | capPSMultiResults | capPluginAuth)
	if db != "" {
		caps |= capConnectWithDB
	}
	caps |= uint32(clientFlags) & (capFoundRows | capIgnoreSpace | capInteractive)
	caps &= serverCaps | capProtocol41
	b.caps = caps
	auth, serr := authResponse(plugin, nonce, pass)
	if serr != nil {
		return serr
	}
	resp := binary.LittleEndian.AppendUint32(nil, caps)
	resp = binary.LittleEndian.AppendUint32(resp, 1<<24)
	resp = append(resp, handshakeCharset)
	resp = append(resp, make([]byte, 23)...)
	resp = append(resp, []byte(user)...)
	resp = append(resp, 0, byte(len(auth)))
	resp = append(resp, auth...)
	if caps&capConnectWithDB != 0 {
		resp = append(resp, []byte(db)...)
		resp = append(resp, 0)
	}
	if caps&capPluginAuth != 0 {
		resp = append(resp, []byte(plugin)...)
		resp = append(resp, 0)
	}
	if err := b.writePacket(resp); err != nil {
		return b.lost(err)
	}
	return b.authLoop(plugin, nonce, pass)
}

func (b *tcpBackend) authLoop(plugin string, nonce []byte, pass string) *sqlError {
	for range 8 {
		p, err := b.readPacket()
		if err != nil {
			return b.lost(err)
		}
		if len(p) == 0 {
			return newSQLError(2027, "HY000", "Malformed packet")
		}
		switch p[0] {
		case 0x00:
			// Le paquet OK qui conclut l'authentification se décode comme celui d'un résultat : un paquet
			// tronqué avant ses quatre octets d'état est refusé, jamais pris pour un succès.
			if _, valid := parseOK(p); !valid {
				return b.malformed()
			}
			return nil
		case 0xff:
			return parseErrPacket(p)
		case 0xfe:
			rest := p[1:]
			z := strings.IndexByte(string(rest), 0)
			if z < 0 {
				return newSQLError(2027, "HY000", "Malformed auth switch packet")
			}
			plugin = string(rest[:z])
			nonce = append([]byte(nil), rest[z+1:]...)
			if n := len(nonce); n > 0 && nonce[n-1] == 0 {
				nonce = nonce[:n-1]
			}
			auth, serr := authResponse(plugin, nonce, pass)
			if serr != nil {
				return serr
			}
			if err := b.writePacket(auth); err != nil {
				return b.lost(err)
			}
		case 0x01:
			if plugin != "caching_sha2_password" || len(p) < 2 {
				return newSQLError(2027, "HY000", "Unexpected auth data packet")
			}
			switch p[1] {
			case 3:
				continue
			case 4:
				if err := b.writePacket([]byte{2}); err != nil {
					return b.lost(err)
				}
				keyPkt, err := b.readPacket()
				if err != nil {
					return b.lost(err)
				}
				if len(keyPkt) < 2 || keyPkt[0] != 0x01 {
					return newSQLError(2061, "HY000", "Authentication requires secure connection")
				}
				enc, serr := encryptPassword(keyPkt[1:], nonce, pass)
				if serr != nil {
					return serr
				}
				if err := b.writePacket(enc); err != nil {
					return b.lost(err)
				}
			default:
				return newSQLError(2027, "HY000", "Unexpected caching_sha2_password state")
			}
		default:
			return newSQLError(2027, "HY000", "Unexpected authentication packet")
		}
	}
	return newSQLError(2027, "HY000", "Authentication exchange did not converge")
}

// encryptPassword chiffre le mot de passe pour caching_sha2_password par la clé RSA publique du serveur.
func encryptPassword(pemKey, nonce []byte, pass string) ([]byte, *sqlError) {
	block, _ := pem.Decode(pemKey)
	if block == nil {
		return nil, newSQLError(2061, "HY000", "Invalid RSA public key from server")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, newSQLError(2061, "HY000", "Invalid RSA public key from server: "+err.Error())
	}
	rsaKey, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, newSQLError(2061, "HY000", "Server public key is not RSA")
	}
	plain := append([]byte(pass), 0)
	for i := range plain {
		if len(nonce) > 0 {
			plain[i] ^= nonce[i%len(nonce)]
		}
	}
	out, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, rsaKey, plain, nil)
	if err != nil {
		return nil, newSQLError(2061, "HY000", err.Error())
	}
	return out, nil
}

// Résultats.

// parseColumnDef décode une définition de colonne ; ok est faux si une chaîne ou l'entête fixe est tronqué.
func parseColumnDef(p []byte) (field, bool) {
	var f field
	i := 0
	strs := make([]string, 6)
	for k := range strs {
		s, n, _, ok := readLenEncString(p[i:])
		if !ok {
			return f, false
		}
		strs[k] = s
		i += n
	}
	f.db, f.table, f.orgtable, f.name, f.orgname = strs[1], strs[2], strs[3], strs[4], strs[5]
	_, n, _, ok := readLenEncInt(p[i:])
	if !ok {
		return f, false
	}
	i += n
	// Les dix octets fixes (jeu de caractères, longueur, type, drapeaux, décimales) sont obligatoires.
	if i+10 > len(p) {
		return f, false
	}
	f.charsetnr = binary.LittleEndian.Uint16(p[i:])
	f.length = binary.LittleEndian.Uint32(p[i+2:])
	f.typ = p[i+6]
	f.flags = binary.LittleEndian.Uint16(p[i+7:])
	f.decimals = p[i+9]
	return f, true
}

func (b *tcpBackend) readFields(n int) ([]field, *sqlError) {
	fields := make([]field, n)
	for i := range n {
		p, err := b.readPacket()
		if err != nil {
			return nil, b.lost(err)
		}
		f, ok := parseColumnDef(p)
		if !ok {
			return nil, b.malformed()
		}
		fields[i] = f
	}
	if n > 0 {
		p, err := b.readPacket()
		if err != nil {
			return nil, b.lost(err)
		}
		if truncatedEOF(p) {
			return nil, b.malformed()
		}
		if !isEOF(p) {
			return nil, newSQLError(2027, "HY000", "Malformed packet: EOF expected after column definitions")
		}
	}
	return fields, nil
}

// readResult lit un résultat (OK, ERR ou jeu de lignes) ; more signale un résultat suivant.
func (b *tcpBackend) readResult(binaryRows bool) (res execResult, more bool, serr *sqlError) {
	p, err := b.readPacket()
	if err != nil {
		return res, false, b.lost(err)
	}
	if len(p) == 0 {
		return res, false, newSQLError(2027, "HY000", "Malformed packet")
	}
	switch p[0] {
	case 0x00:
		ok, valid := parseOK(p)
		if !valid {
			return res, false, b.malformed()
		}
		return execResult{affected: int64(ok.affected), insertID: int64(ok.insertID), warnings: int(ok.warnings), info: ok.info}, ok.status&statusMoreResults != 0, nil
	case 0xff:
		return res, false, parseErrPacket(p)
	case 0xfb:
		_ = b.writePacket(nil)
		_, _, _ = b.readResult(false)
		return res, false, newSQLError(2068, "HY000", "LOAD DATA LOCAL INFILE is forbidden")
	}
	n, _, _, ok := readLenEncInt(p)
	if !ok || n > maxColumns {
		return res, false, b.malformed()
	}
	fields, serr := b.readFields(int(n))
	if serr != nil {
		return res, false, serr
	}
	rs := &resultSet{fields: fields, binary: binaryRows}
	for {
		p, err := b.readPacket()
		if err != nil {
			return res, false, b.lost(err)
		}
		if len(p) == 0 {
			return res, false, b.malformed()
		}
		if truncatedEOF(p) {
			return res, false, b.malformed()
		}
		if isEOF(p) {
			res.rs = rs
			res.affected = int64(len(rs.rows))
			return res, eofStatus(p)&statusMoreResults != 0, nil
		}
		if p[0] == 0xff {
			return res, false, parseErrPacket(p)
		}
		var row []cell
		var ok bool
		if binaryRows {
			row, ok = decodeBinaryRow(p, fields)
		} else {
			row, ok = decodeTextRow(p, len(fields))
		}
		if !ok {
			return res, false, b.malformed()
		}
		rs.rows = append(rs.rows, row)
	}
}

// maxColumns est le nombre maximal de colonnes d'un résultat MySQL.
const maxColumns = 4096

// malformed ferme la connexion, dont le flux de paquets n'est plus synchronisé, et rend CR_MALFORMED_PACKET.
func (b *tcpBackend) malformed() *sqlError {
	if b.conn != nil {
		_ = b.conn.Close()
		b.conn = nil
	}
	return newSQLError(2027, "HY000", "Malformed packet")
}

// decodeTextRow décode une ligne du protocole texte ; ok est faux si une colonne dépasse le paquet.
func decodeTextRow(p []byte, n int) ([]cell, bool) {
	row := make([]cell, n)
	i := 0
	for k := range n {
		s, m, null, ok := readLenEncString(p[i:])
		if !ok {
			return nil, false
		}
		i += m
		if null {
			row[k] = nullCell
		} else {
			row[k] = textCell(s)
		}
	}
	return row, true
}

// decodeBinaryRow décode une ligne du protocole binaire (COM_STMT_EXECUTE) ; chaque lecture est précédée
// d'un contrôle de borne et ok est faux si le paquet est tronqué.
func decodeBinaryRow(p []byte, fields []field) ([]cell, bool) {
	row := make([]cell, len(fields))
	nullLen := (len(fields) + 7 + 2) / 8
	i := 1 + nullLen
	if i > len(p) {
		return nil, false
	}
	need := func(w int) bool { return w >= 0 && w <= len(p)-i }
	for k, f := range fields {
		bit := k + 2
		if p[1+bit/8]&(1<<(bit%8)) != 0 {
			row[k] = nullCell
			continue
		}
		unsigned := f.flags&flagUnsigned != 0
		switch f.typ {
		case typeTiny:
			if !need(1) {
				return nil, false
			}
			v := int64(int8(p[i]))
			if unsigned {
				v = int64(p[i])
			}
			row[k] = intCell(v)
			i++
		case typeShort, typeYear:
			if !need(2) {
				return nil, false
			}
			u := binary.LittleEndian.Uint16(p[i:])
			v := int64(int16(u))
			if unsigned || f.typ == typeYear {
				v = int64(u)
			}
			row[k] = intCell(v)
			i += 2
		case typeLong, typeInt24:
			if !need(4) {
				return nil, false
			}
			u := binary.LittleEndian.Uint32(p[i:])
			v := int64(int32(u))
			if unsigned {
				v = int64(u)
			}
			row[k] = intCell(v)
			i += 4
		case typeLongLong:
			if !need(8) {
				return nil, false
			}
			u := binary.LittleEndian.Uint64(p[i:])
			if unsigned && u > math.MaxInt64 {
				row[k] = cell{kind: cellUint, i: int64(u)}
			} else {
				row[k] = intCell(int64(u))
			}
			i += 8
		case typeFloat:
			if !need(4) {
				return nil, false
			}
			f32 := math.Float32frombits(binary.LittleEndian.Uint32(p[i:]))
			v, _ := strconv.ParseFloat(strconv.FormatFloat(float64(f32), 'g', -1, 32), 64)
			row[k] = cell{kind: cellFloat, f: v}
			i += 4
		case typeDouble:
			if !need(8) {
				return nil, false
			}
			row[k] = cell{kind: cellFloat, f: math.Float64frombits(binary.LittleEndian.Uint64(p[i:]))}
			i += 8
		case typeDate, typeDateTime, typeTimestamp, typeNewDate, typeTime:
			if !need(1) {
				return nil, false
			}
			n := int(p[i])
			if !need(1 + n) {
				return nil, false
			}
			d := p[i+1 : i+1+n]
			i += 1 + n
			if f.typ == typeTime {
				row[k] = textCell(formatBinaryTime(d, f))
			} else {
				row[k] = textCell(formatBinaryDate(d, f))
			}
		default:
			s, m, _, ok := readLenEncString(p[i:])
			if !ok {
				return nil, false
			}
			i += m
			row[k] = textCell(s)
		}
	}
	return row, true
}

func pad(n int, w int) string {
	s := strconv.Itoa(n)
	for len(s) < w {
		s = "0" + s
	}
	return s
}

func fraction(micro uint32, decimals uint8) string {
	if decimals == 0 || decimals > 6 {
		return ""
	}
	return "." + pad(int(micro), 6)[:decimals]
}

func formatBinaryDate(d []byte, f field) string {
	var y, mo, da, h, mi, s int
	var micro uint32
	if len(d) >= 4 {
		y, mo, da = int(binary.LittleEndian.Uint16(d)), int(d[2]), int(d[3])
	}
	if len(d) >= 7 {
		h, mi, s = int(d[4]), int(d[5]), int(d[6])
	}
	if len(d) >= 11 {
		micro = binary.LittleEndian.Uint32(d[7:])
	}
	date := pad(y, 4) + "-" + pad(mo, 2) + "-" + pad(da, 2)
	if f.typ == typeDate || f.typ == typeNewDate {
		return date
	}
	return date + " " + pad(h, 2) + ":" + pad(mi, 2) + ":" + pad(s, 2) + fraction(micro, f.decimals)
}

func formatBinaryTime(d []byte, f field) string {
	if len(d) < 8 {
		return "00:00:00"
	}
	sign := ""
	if d[0] == 1 {
		sign = "-"
	}
	days := int(binary.LittleEndian.Uint32(d[1:]))
	h := days*24 + int(d[5])
	var micro uint32
	if len(d) >= 12 {
		micro = binary.LittleEndian.Uint32(d[8:])
	}
	return sign + pad(h, 2) + ":" + pad(int(d[6]), 2) + ":" + pad(int(d[7]), 2) + fraction(micro, f.decimals)
}

// Interface backend.

func (b *tcpBackend) setOption(on bool) *sqlError {
	opt := []byte{1, 0}
	if on {
		opt = []byte{0, 0}
	}
	if err := b.command(comSetOption, opt); err != nil {
		return b.lost(err)
	}
	p, err := b.readPacket()
	if err != nil {
		return b.lost(err)
	}
	if len(p) > 0 && p[0] == 0xff {
		return parseErrPacket(p)
	}
	return nil
}

func (b *tcpBackend) query(sql string, multi bool) ([]execResult, *sqlError) {
	if multi {
		if err := b.setOption(true); err != nil {
			return nil, err
		}
		defer func() { _ = b.setOption(false) }()
	}
	if err := b.command(comQuery, []byte(sql)); err != nil {
		return nil, b.lost(err)
	}
	var out []execResult
	for {
		res, more, err := b.readResult(false)
		if err != nil {
			return out, err
		}
		out = append(out, res)
		if !more {
			return out, nil
		}
	}
}

func (b *tcpBackend) simple(cmd byte, data []byte) *sqlError {
	if err := b.command(cmd, data); err != nil {
		return b.lost(err)
	}
	_, _, err := b.readResult(false)
	return err
}

func (b *tcpBackend) selectDB(db string) *sqlError {
	if err := b.simple(comInitDB, []byte(db)); err != nil {
		return err
	}
	b.dbName = db
	return nil
}

func (b *tcpBackend) setCharset(cs string) *sqlError {
	_, err := b.query("SET NAMES "+sqliteQuote(cs), false)
	if err != nil && err.errno == 1115 {
		return newSQLError(2019, "HY000", "Can't initialize character set "+cs+" (path: compiled_in)")
	}
	return err
}

func (b *tcpBackend) ping() bool { return b.conn != nil && b.simple(comPing, nil) == nil }

func (b *tcpBackend) close() {
	if b.conn == nil {
		return
	}
	_ = b.command(comQuit, nil)
	_ = b.conn.Close()
	b.conn = nil
}

func (b *tcpBackend) serverInfo() string   { return b.server }
func (b *tcpBackend) hostInfo() string     { return b.host }
func (b *tcpBackend) protocolVersion() int { return 10 }
func (b *tcpBackend) threadID() int64      { return int64(b.connID) }

// serverVersion convertit « 8.0.36 » ou « 5.5.5-11.4.2-MariaDB » en entier (80036, 110402).
func (b *tcpBackend) serverVersion() int {
	v := strings.TrimPrefix(b.server, "5.5.5-")
	parts := strings.SplitN(v, ".", 3)
	nums := [3]int{}
	for i := range min(3, len(parts)) {
		digits := parts[i]
		for j := range len(digits) {
			if digits[j] < '0' || digits[j] > '9' {
				digits = digits[:j]
				break
			}
		}
		nums[i], _ = strconv.Atoi(digits)
	}
	return nums[0]*10000 + nums[1]*100 + nums[2]
}

func (b *tcpBackend) stat() string {
	if err := b.command(comStatistics, nil); err != nil {
		return ""
	}
	p, err := b.readPacket()
	if err != nil {
		return ""
	}
	return string(p)
}

// tcpStmt est une requête préparée côté serveur (COM_STMT_PREPARE).
type tcpStmt struct {
	b       *tcpBackend
	id      uint32
	nparams int
	flds    []field
}

func (b *tcpBackend) prepare(sql string) (stmtBackend, *sqlError) {
	if err := b.command(comStmtPrepare, []byte(sql)); err != nil {
		return nil, b.lost(err)
	}
	p, err := b.readPacket()
	if err != nil {
		return nil, b.lost(err)
	}
	if len(p) > 0 && p[0] == 0xff {
		return nil, parseErrPacket(p)
	}
	if len(p) < 9 {
		return nil, newSQLError(2027, "HY000", "Malformed prepare response")
	}
	st := &tcpStmt{b: b, id: binary.LittleEndian.Uint32(p[1:])}
	ncols := int(binary.LittleEndian.Uint16(p[5:]))
	st.nparams = int(binary.LittleEndian.Uint16(p[7:]))
	if _, serr := b.readFields(st.nparams); serr != nil {
		return nil, serr
	}
	flds, serr := b.readFields(ncols)
	if serr != nil {
		return nil, serr
	}
	st.flds = flds
	return st, nil
}

func (s *tcpStmt) paramCount() int { return s.nparams }
func (s *tcpStmt) fields() []field { return s.flds }

func (s *tcpStmt) close() {
	if s.b.conn != nil {
		_ = s.b.command(comStmtClose, binary.LittleEndian.AppendUint32(nil, s.id))
	}
}

func (s *tcpStmt) execute(params []cell) (execResult, *sqlError) {
	if len(params) != s.nparams {
		return execResult{}, newSQLError(crParamsNotBound, "HY000", "No data supplied for parameters in prepared statement")
	}
	buf := binary.LittleEndian.AppendUint32(nil, s.id)
	buf = append(buf, 0)
	buf = binary.LittleEndian.AppendUint32(buf, 1)
	if s.nparams > 0 {
		bitmap := make([]byte, (s.nparams+7)/8)
		types := make([]byte, 0, 2*s.nparams)
		var values []byte
		for i, p := range params {
			switch p.kind {
			case cellNull:
				bitmap[i/8] |= 1 << (i % 8)
				types = append(types, typeNull, 0)
			case cellInt:
				types = append(types, typeLongLong, 0)
				values = binary.LittleEndian.AppendUint64(values, uint64(p.i))
			case cellUint:
				types = append(types, typeLongLong, 0x80)
				values = binary.LittleEndian.AppendUint64(values, uint64(p.i))
			case cellFloat:
				types = append(types, typeDouble, 0)
				values = binary.LittleEndian.AppendUint64(values, math.Float64bits(p.f))
			case cellBlob:
				types = append(types, typeBlob, 0)
				values = appendLenEnc(values, uint64(len(p.s)))
				values = append(values, p.s...)
			default:
				types = append(types, typeVarString, 0)
				values = appendLenEnc(values, uint64(len(p.s)))
				values = append(values, p.s...)
			}
		}
		buf = append(buf, bitmap...)
		buf = append(buf, 1)
		buf = append(buf, types...)
		buf = append(buf, values...)
	}
	if err := s.b.command(comStmtExecute, buf); err != nil {
		return execResult{}, s.b.lost(err)
	}
	res, more, serr := s.b.readResult(true)
	for more && serr == nil {
		_, more, serr = s.b.readResult(true)
	}
	return res, serr
}
