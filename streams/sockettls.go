package streams

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"slices"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"code.hazyhaar.fr/devhoros/php127/types"
	"code.hazyhaar.fr/devhoros/php127/zone"
)

// Lot 9.2 : drapeaux et contexte de stream_socket_client, chiffrement d'une socket ouverte,
// attente de disponibilité par stream_select.

const (
	STREAM_CLIENT_PERSISTENT    = 1
	STREAM_CLIENT_ASYNC_CONNECT = 2
	STREAM_CLIENT_CONNECT       = 4

	// Bits des méthodes STREAM_CRYPTO_METHOD_* de PHP 8.3 : le bit 0 marque un client, les bits 1 à 6
	// désignent SSLv2, SSLv3, TLS 1.0, 1.1, 1.2 et 1.3.
	cryptoClientBit = 1
)

var cryptoVersionBits = [...]struct {
	bit     int64
	version uint16
}{
	{1 << 3, tls.VersionTLS10},
	{1 << 4, tls.VersionTLS11},
	{1 << 5, tls.VersionTLS12},
	{1 << 6, tls.VersionTLS13},
}

func init() {
	for name, v := range map[string]int64{
		"STREAM_CRYPTO_METHOD_SSLv2_CLIENT":   3,
		"STREAM_CRYPTO_METHOD_SSLv3_CLIENT":   5,
		"STREAM_CRYPTO_METHOD_SSLv23_CLIENT":  57,
		"STREAM_CRYPTO_METHOD_ANY_CLIENT":     127,
		"STREAM_CRYPTO_METHOD_TLS_CLIENT":     121,
		"STREAM_CRYPTO_METHOD_TLSv1_0_CLIENT": 9,
		"STREAM_CRYPTO_METHOD_TLSv1_1_CLIENT": 17,
		"STREAM_CRYPTO_METHOD_TLSv1_2_CLIENT": 33,
		"STREAM_CRYPTO_METHOD_TLSv1_3_CLIENT": 65,
		"STREAM_CRYPTO_METHOD_SSLv2_SERVER":   2,
		"STREAM_CRYPTO_METHOD_SSLv3_SERVER":   4,
		"STREAM_CRYPTO_METHOD_SSLv23_SERVER":  120,
		"STREAM_CRYPTO_METHOD_ANY_SERVER":     126,
		"STREAM_CRYPTO_METHOD_TLS_SERVER":     120,
		"STREAM_CRYPTO_METHOD_TLSv1_0_SERVER": 8,
		"STREAM_CRYPTO_METHOD_TLSv1_1_SERVER": 16,
		"STREAM_CRYPTO_METHOD_TLSv1_2_SERVER": 32,
		"STREAM_CRYPTO_METHOD_TLSv1_3_SERVER": 64,
		"STREAM_CRYPTO_PROTO_SSLv3":           4,
		"STREAM_CRYPTO_PROTO_TLSv1_0":         8,
		"STREAM_CRYPTO_PROTO_TLSv1_1":         16,
		"STREAM_CRYPTO_PROTO_TLSv1_2":         32,
		"STREAM_CRYPTO_PROTO_TLSv1_3":         64,
	} {
		constants[name] = types.NewLong(v)
	}
}

// contextOption rend la valeur ["wrapper"]["option"] du contexte, sans la retenir.
func contextOption(z *zone.Zone, ctx *streamHandle, wrapper, option string) (types.Value, bool) {
	if ctx == nil || ctx.options.Type != types.TypeArray {
		return types.Value{}, false
	}
	wk, err := newStr(z, wrapper)
	if err != nil {
		return types.Value{}, false
	}
	defer types.ReleaseValue(z, &wk)
	sub, found, err := types.ArrayGet(z, ctx.options, wk, warnDrop)
	if err != nil || !found {
		return types.Value{}, false
	}
	if sub = deref(z, sub); sub.Type != types.TypeArray {
		return types.Value{}, false
	}
	ok, err := newStr(z, option)
	if err != nil {
		return types.Value{}, false
	}
	defer types.ReleaseValue(z, &ok)
	v, found, err := types.ArrayGet(z, sub, ok, warnDrop)
	if err != nil || !found {
		return types.Value{}, false
	}
	return deref(z, v), true
}

func contextBool(z *zone.Zone, ctx *streamHandle, wrapper, option string, def bool) bool {
	if v, ok := contextOption(z, ctx, wrapper, option); ok {
		return asBool(z, v)
	}
	return def
}

// contextString rend une copie Go de l'option : la politique TLS survit à la libération du contexte.
func contextString(z *zone.Zone, ctx *streamHandle, wrapper, option string) string {
	if v, ok := contextOption(z, ctx, wrapper, option); ok {
		if s, err := castString(z, v); err == nil {
			return s
		}
	}
	return ""
}

// tlsPolicy rassemble les options « ssl » d'un contexte PHP, copiées hors de l'arène.
type tlsPolicy struct {
	verifyPeer      bool
	verifyPeerName  bool
	allowSelfSigned bool
	sni             bool
	peerName        string
	cafile          string
	// method porte les bits STREAM_CRYPTO_METHOD_* ; zéro garde les versions par défaut de crypto/tls.
	method int64
}

// readTLSPolicy lit les options « ssl » du contexte ; host est le nom de pair par défaut.
func readTLSPolicy(z *zone.Zone, zs *ZoneStreams, ctx *streamHandle, host string) tlsPolicy {
	p := tlsPolicy{
		verifyPeer:      contextBool(z, ctx, "ssl", "verify_peer", true),
		verifyPeerName:  contextBool(z, ctx, "ssl", "verify_peer_name", true),
		allowSelfSigned: contextBool(z, ctx, "ssl", "allow_self_signed", false),
		sni:             contextBool(z, ctx, "ssl", "SNI_enabled", true),
		peerName:        contextString(z, ctx, "ssl", "peer_name"),
		cafile:          contextString(z, ctx, "ssl", "cafile"),
	}
	if p.peerName == "" {
		p.peerName = host
	}
	if p.cafile != "" {
		p.cafile = zs.resolvePath(p.cafile)
	}
	if v, ok := contextOption(z, ctx, "ssl", "crypto_method"); ok {
		p.method = v.ToLong()
	}
	return p
}

// config traduit la politique en *tls.Config. La vérification passe par VerifyConnection afin de
// séparer, comme PHP, la chaîne (verify_peer), le nom (verify_peer_name, peer_name), l'indication
// SNI (SNI_enabled) et le masque exact des versions (crypto_method) ; crypto/tls n'effectue alors
// aucune vérification implicite. verify_peer=false ne désactive que la chaîne : le nom et le masque
// restent opposés, comme php_openssl_apply_peer_verification.
func (p tlsPolicy) config() (*tls.Config, error) {
	cfg := &tls.Config{InsecureSkipVerify: true}
	if p.sni && net.ParseIP(p.peerName) == nil {
		cfg.ServerName = p.peerName
	}
	// allowed retient les versions présentes dans le masque ; l'intervalle [min, max] ne fait que borner la
	// négociation, une version intermédiaire absente du masque est refusée par VerifyConnection.
	var allowed []uint16
	if p.method != 0 {
		if p.method&cryptoClientBit == 0 {
			return nil, errors.New("server crypto methods cannot be used on a client socket")
		}
		for _, b := range cryptoVersionBits {
			if p.method&b.bit != 0 {
				allowed = append(allowed, b.version)
			}
		}
		if len(allowed) == 0 {
			return nil, errors.New("only TLS 1.0 to 1.3 crypto methods are supported")
		}
		cfg.MinVersion, cfg.MaxVersion = allowed[0], allowed[len(allowed)-1]
	}
	verifyName := ""
	if p.verifyPeerName {
		if p.peerName == "" {
			return nil, errors.New("unable to determine the peer name to verify")
		}
		verifyName = p.peerName
	}
	if !p.verifyPeer && verifyName == "" && allowed == nil {
		return cfg, nil
	}
	var roots *x509.CertPool
	if p.verifyPeer && p.cafile != "" {
		pem, err := os.ReadFile(p.cafile)
		if err != nil {
			return nil, errors.New("unable to set verify locations `" + p.cafile + "'")
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("unable to set verify locations `" + p.cafile + "'")
		}
	}
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if allowed != nil && !slices.Contains(allowed, cs.Version) {
			return errors.New("negotiated " + tls.VersionName(cs.Version) + " is not allowed by crypto_method")
		}
		if !p.verifyPeer && verifyName == "" {
			return nil
		}
		certs := cs.PeerCertificates
		if len(certs) == 0 {
			return errors.New("peer did not present a certificate")
		}
		if !p.verifyPeer {
			return certs[0].VerifyHostname(verifyName)
		}
		return p.verifyChain(certs, roots, verifyName)
	}
	return cfg, nil
}

// verifyChain vérifie la chaîne présentée contre roots (racines du système si nil) et, si verifyName est
// non vide, le nom du certificat feuille. allow_self_signed ne lève qu'une autorité inconnue, comme
// X509_V_ERR_DEPTH_ZERO_SELF_SIGNED_CERT côté PHP : le certificat seul présenté doit être signé par sa propre
// clé, en cours de validité et, si le nom est vérifié, émis pour ce nom ; une expiration reste un refus.
func (p tlsPolicy) verifyChain(certs []*x509.Certificate, roots *x509.CertPool, verifyName string) error {
	leaf := certs[0]
	opts := x509.VerifyOptions{Roots: roots, Intermediates: x509.NewCertPool(), DNSName: verifyName}
	for _, c := range certs[1:] {
		opts.Intermediates.AddCert(c)
	}
	_, err := leaf.Verify(opts)
	if err == nil || !p.allowSelfSigned || len(certs) != 1 {
		return err
	}
	if _, unknown := errors.AsType[x509.UnknownAuthorityError](err); !unknown {
		return err
	}
	if leaf.CheckSignatureFrom(leaf) != nil {
		return err
	}
	if now := time.Now(); now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return err
	}
	if verifyName != "" {
		return leaf.VerifyHostname(verifyName)
	}
	return nil
}

// dialOptions porte les options de contexte appliquées à l'ouverture d'une socket.
type dialOptions struct {
	bindto       string
	tls          *tlsPolicy
	asyncConnect bool
}

func readDialOptions(z *zone.Zone, zs *ZoneStreams, ctx *streamHandle) dialOptions {
	o := dialOptions{bindto: contextString(z, ctx, "socket", "bindto")}
	if ctx != nil {
		p := readTLSPolicy(z, zs, ctx, "")
		o.tls = &p
	}
	return o
}

func tcpSockaddr(addr *net.TCPAddr) (int, syscall.Sockaddr) {
	if ip4 := addr.IP.To4(); ip4 != nil {
		sa := &syscall.SockaddrInet4{Port: addr.Port}
		copy(sa.Addr[:], ip4)
		return syscall.AF_INET, sa
	}
	sa := &syscall.SockaddrInet6{Port: addr.Port}
	copy(sa.Addr[:], addr.IP.To16())
	return syscall.AF_INET6, sa
}

// dialAsyncTCP initie un connect(2) non bloquant (SOCK_NONBLOCK) sans attendre l'achèvement de la poignée de main.
func dialAsyncTCP(address, bindto string) (net.Conn, error) {
	raddr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, err
	}
	family, remoteSA := tcpSockaddr(raddr)
	fd, err := syscall.Socket(family, syscall.SOCK_STREAM|syscall.SOCK_NONBLOCK|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if bindto != "" {
		laddr, err := net.ResolveTCPAddr("tcp", bindto)
		if err != nil {
			_ = syscall.Close(fd)
			return nil, err
		}
		_, localSA := tcpSockaddr(laddr)
		if err := syscall.Bind(fd, localSA); err != nil {
			_ = syscall.Close(fd)
			return nil, err
		}
	}
	if err := syscall.Connect(fd, remoteSA); err != nil && !errors.Is(err, syscall.EINPROGRESS) && !errors.Is(err, syscall.EALREADY) {
		_ = syscall.Close(fd)
		return nil, err
	}
	f := os.NewFile(uintptr(fd), address)
	if f == nil {
		_ = syscall.Close(fd)
		return nil, syscall.EBADF
	}
	conn, err := net.FileConn(f)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// contextHandle rend le contexte attaché à un flux, s'il existe encore.
func (zs *ZoneStreams) contextHandle(ctxID uint32) (*streamHandle, bool) {
	if ctxID == 0 {
		return nil, false
	}
	zs.mu.Lock()
	defer zs.mu.Unlock()
	c, ok := zs.streams[ctxID]
	if !ok || c.closed || c.kind != backendContext {
		return nil, false
	}
	return c, true
}

func nativeStreamContextSetOptions(z *zone.Zone, _ io.Writer, args []types.Value) (types.Value, error) {
	const fn = "stream_context_set_options"
	if err := arity(fn, len(args), 2, 2); err != nil {
		return types.Value{}, err
	}
	if first := deref(z, args[0]); first.Type != types.TypeResource {
		return types.Value{}, typeErr(fn, "1", "context", "resource", typeKind(z, first))
	}
	opts := deref(z, args[1])
	if opts.Type != types.TypeArray {
		return types.Value{}, typeErr(fn, "2", "options", "array", typeKind(z, opts))
	}
	h, err := contextOrStream(z, args[0], fn)
	if err != nil {
		return types.Value{}, err
	}
	if h == nil {
		return types.NewBool(false), nil
	}
	if err := mergeContextOptions(z, h, opts); err != nil {
		return types.Value{}, err
	}
	return types.NewBool(true), nil
}

// prefixConn rejoue devant la connexion les octets déjà tamponnés en clair avant la négociation TLS.
type prefixConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func nativeStreamSocketEnableCrypto(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	const fn = "stream_socket_enable_crypto"
	if err := arity(fn, len(args), 2, 4); err != nil {
		return types.Value{}, err
	}
	zs, h, err := requireStreamArg(z, args[0], fn, "1", "stream")
	if err != nil {
		return types.Value{}, err
	}
	enable := asBool(z, args[1])
	ctx, _ := zs.contextHandle(h.ctxID)
	method := int64(0)
	if len(args) >= 3 && !deref(z, args[2]).IsNull() {
		if method, err = asIntParam(z, args[2], fn, "3", "crypto_method"); err != nil {
			return types.Value{}, err
		}
	} else if enable {
		v, ok := contextOption(z, ctx, "ssl", "crypto_method")
		if !ok {
			return types.Value{}, ValueError{fn + "(): Argument #3 ($crypto_method) must be specified when enabling encryption"}
		}
		method = v.ToLong()
	}
	if len(args) == 4 && !deref(z, args[3]).IsNull() {
		// La reprise de session est une optimisation de négociation : le flux fourni est validé, non réutilisé.
		if _, _, err := requireStreamArg(z, args[3], fn, "4", "session_stream"); err != nil {
			return types.Value{}, err
		}
	}
	if h.kind != backendSocket || h.conn == nil {
		warn(out, fn+"()", "This stream does not support SSL/crypto")
		return types.NewBool(!enable), nil
	}
	if !enable {
		tc, ok := h.conn.(*tls.Conn)
		if !h.tlsOn || !ok {
			return types.NewBool(false), nil
		}
		_ = tc.CloseWrite()
		h.conn = tc.NetConn()
		h.rd = bufio.NewReader(h.conn)
		h.tlsOn = false
		return types.NewBool(true), nil
	}
	if h.tlsOn {
		warn(out, fn+"()", "SSL/TLS already set-up for this stream")
		return types.NewBool(false), nil
	}
	p := readTLSPolicy(z, zs, ctx, h.host)
	p.method = method
	cfg, err := p.config()
	if err != nil {
		warn(out, fn+"()", err.Error())
		return types.NewBool(false), nil
	}
	raw := h.conn
	under := raw
	if n := h.rd.Buffered(); n > 0 {
		pending, _ := h.rd.Peek(n)
		under = &prefixConn{Conn: raw, r: io.MultiReader(bytes.NewReader(bytes.Clone(pending)), raw)}
	}
	tc := tls.Client(under, cfg)
	if h.timeout > 0 {
		_ = raw.SetDeadline(time.Now().Add(h.timeout))
	}
	err = tc.Handshake()
	_ = raw.SetDeadline(time.Time{})
	if err != nil {
		warn(out, fn+"()", "SSL operation failed: "+err.Error())
		return types.NewBool(false), nil
	}
	h.conn = tc
	h.rd = bufio.NewReader(tc)
	h.tlsOn = true
	return types.NewBool(true), nil
}

// selectEntry est un élément de tableau passé à stream_select, retenu avec sa clé d'origine.
type selectEntry struct {
	key, val types.Value
	h        *streamHandle
	ready    bool
}

func isTimeout(err error) bool {
	ne, ok := errors.AsType[net.Error](err)
	return ok && ne.Timeout()
}

// probeReadable attend qu'une des sockets de lecture ait un octet à lire, une fin de flux ou une erreur,
// ou qu'une des sockets d'écriture ou d'exception en attente devienne prête, au plus jusqu'à l'échéance
// (aucune si infinite). Peek ne consomme rien : l'octet reste dans le tampon du flux.
// Une goroutine sonde chaque socket de lecture distincte et une goroutine interroge les sockets d'écriture
// et d'exception ; dès la première prête, l'échéance des sockets de lecture est ramenée à maintenant, et
// toutes les goroutines sont attendues avant le retour : aucune n'accède au flux après l'appel natif.
func probeReadable(pending []*selectEntry, pendingWrite []*selectEntry, pendingExcept []*selectEntry, wait time.Duration, infinite bool) {
	var socks []*streamHandle
	for _, e := range pending {
		if !slices.Contains(socks, e.h) {
			socks = append(socks, e.h)
		}
	}
	if len(socks) == 0 {
		return
	}
	var deadline time.Time
	if !infinite {
		deadline = time.Now().Add(max(wait, time.Millisecond))
	}
	type result struct {
		h     *streamHandle
		ready bool
	}
	results := make(chan result, len(socks))
	for _, h := range socks {
		_ = h.conn.SetReadDeadline(deadline)
		go func() {
			_, err := h.rd.Peek(1)
			results <- result{h, !isTimeout(err)}
		}()
	}
	var pollStop chan struct{}
	var pollDone chan struct{}
	if len(pendingWrite) > 0 || len(pendingExcept) > 0 {
		pollStop = make(chan struct{})
		pollDone = make(chan struct{})
		go func() {
			defer close(pollDone)
			for {
				auxReady := false
				for _, e := range pendingWrite {
					if !e.ready && pollSocketConn(e.h.conn, pollOut, 0)&(pollOut|pollErr|pollHup) != 0 {
						e.ready = true
						auxReady = true
					}
				}
				for _, e := range pendingExcept {
					if !e.ready && pollSocketConn(e.h.conn, pollPri, 0)&pollPri != 0 {
						e.ready = true
						auxReady = true
					}
				}
				if auxReady {
					now := time.Now()
					for _, h := range socks {
						_ = h.conn.SetReadDeadline(now)
					}
					return
				}
				if !infinite && !time.Now().Before(deadline) {
					return
				}
				step := 5 * time.Millisecond
				if !infinite {
					if rem := time.Until(deadline); rem < step {
						step = max(rem, time.Millisecond)
					}
				}
				select {
				case <-pollStop:
					return
				case <-time.After(step):
				}
			}
		}()
	}
	cancelled := false
	for range socks {
		r := <-results
		if !r.ready {
			continue
		}
		for _, e := range pending {
			if e.h == r.h {
				e.ready = true
			}
		}
		if !cancelled {
			cancelled = true
			now := time.Now()
			for _, h := range socks {
				_ = h.conn.SetReadDeadline(now)
			}
		}
	}
	if pollStop != nil {
		close(pollStop)
		<-pollDone
	}
	for _, h := range socks {
		_ = h.conn.SetReadDeadline(time.Time{})
	}
}

const (
	pollPri = 0x0002
	pollOut = 0x0004
	pollErr = 0x0008
	pollHup = 0x0010
)

func pollSocketConn(c net.Conn, events int16, timeoutMs int) int16 {
	for c != nil {
		switch u := c.(type) {
		case *tls.Conn:
			c = u.NetConn()
		case *prefixConn:
			c = u.Conn
		default:
			sc, ok := c.(syscall.Conn)
			if !ok {
				return pollOut
			}
			rc, err := sc.SyscallConn()
			if err != nil {
				return 0
			}
			var revents int16
			_ = rc.Control(func(fd uintptr) {
				pfd := struct {
					fd      int32
					events  int16
					revents int16
				}{fd: int32(fd), events: events}
				_, _, _ = syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&pfd)), 1, uintptr(timeoutMs))
				revents = pfd.revents
			})
			return revents
		}
	}
	return 0
}

// selectReady marque les éléments prêts des ensembles lecture, écriture et exception, et rend leur nombre.
// Un fichier est toujours prêt en lecture et en écriture ; une socket est sondée par poll(2) en écriture
// (POLLOUT) et en condition exceptionnelle (POLLPRI), et en lecture si aucun octet n'est déjà tamponné
// ni la fin du flux constatée. Tant qu'aucun descripteur n'est prêt, l'attente se poursuit jusqu'à l'échéance.
func selectReady(entries [3][]*selectEntry, wait time.Duration, infinite bool) int {
	ready := 0
	var pendingRead, pendingWrite, pendingExcept []*selectEntry
	for _, e := range entries[0] {
		if e.h.kind == backendFile || e.h.eof || e.h.rd.Buffered() > 0 {
			e.ready = true
			ready++
		} else {
			pendingRead = append(pendingRead, e)
		}
	}
	for _, e := range entries[1] {
		if e.h.kind == backendFile || pollSocketConn(e.h.conn, pollOut, 0)&(pollOut|pollErr|pollHup) != 0 {
			e.ready = true
			ready++
		} else {
			pendingWrite = append(pendingWrite, e)
		}
	}
	for _, e := range entries[2] {
		if e.h.kind == backendSocket {
			if pollSocketConn(e.h.conn, pollPri, 0)&pollPri != 0 {
				e.ready = true
				ready++
			} else {
				pendingExcept = append(pendingExcept, e)
			}
		}
	}
	if ready > 0 {
		wait, infinite = 0, false
	}
	if len(pendingRead) > 0 {
		probeReadable(pendingRead, pendingWrite, pendingExcept, wait, infinite)
		for _, e := range pendingRead {
			if e.ready {
				ready++
			}
		}
		for _, e := range pendingWrite {
			if !e.ready && pollSocketConn(e.h.conn, pollOut, 0)&(pollOut|pollErr|pollHup) != 0 {
				e.ready = true
			}
			if e.ready {
				ready++
			}
		}
		for _, e := range pendingExcept {
			if !e.ready && pollSocketConn(e.h.conn, pollPri, 0)&pollPri != 0 {
				e.ready = true
			}
			if e.ready {
				ready++
			}
		}
		return ready
	}
	if (len(pendingWrite) > 0 || len(pendingExcept) > 0) && (infinite || wait > 0) {
		var deadline time.Time
		if !infinite {
			deadline = time.Now().Add(wait)
		}
		for {
			for _, e := range pendingWrite {
				if !e.ready && pollSocketConn(e.h.conn, pollOut, 0)&(pollOut|pollErr|pollHup) != 0 {
					e.ready = true
					ready++
				}
			}
			for _, e := range pendingExcept {
				if !e.ready && pollSocketConn(e.h.conn, pollPri, 0)&pollPri != 0 {
					e.ready = true
					ready++
				}
			}
			if ready > 0 || (!infinite && !time.Now().Before(deadline)) {
				break
			}
			step := 5 * time.Millisecond
			if !infinite {
				if rem := time.Until(deadline); rem < step {
					step = max(rem, time.Millisecond)
				}
			}
			time.Sleep(step)
		}
	}
	return ready
}

func nativeStreamSelect(z *zone.Zone, out io.Writer, args []types.Value) (types.Value, error) {
	const fn = "stream_select"
	if err := arity(fn, len(args), 4, 5); err != nil {
		return types.Value{}, err
	}
	names := [3]string{"read", "write", "except"}
	var sets [3]types.Value
	var present [3]bool
	for i := range sets {
		v := deref(z, args[i])
		if v.IsNull() {
			continue
		}
		if v.Type != types.TypeArray {
			return types.Value{}, typeErr(fn, strconv.Itoa(i+1), names[i], "?array", typeKind(z, v))
		}
		sets[i], present[i] = v, true
	}
	zs := getZoneStreams(z)
	var entries [3][]*selectEntry
	castable := 0
	// Comme PHP 8.3, un élément qui n'est pas un flux ouvert n'interrompt pas le parcours : il lève une
	// TypeError rendue à la fin, sauf si aucun flux n'est sélectionnable (ValueError, levée en dernier).
	var elemErr error
	for i, set := range sets {
		if !present[i] {
			continue
		}
		types.ArrayForEach(z, set, func(k, v types.Value) bool {
			dv := deref(z, v)
			h, ok := zs.lookupStream(dv)
			if !ok {
				if dv.Type == types.TypeResource {
					elemErr = TypeError{fn + "(): supplied resource is not a valid stream resource"}
				} else {
					elemErr = TypeError{fn + "(): supplied argument is not a valid stream resource"}
				}
				return true
			}
			if selectable := h.kind == backendFile && h.file != nil || h.kind == backendSocket && h.conn != nil; !selectable {
				warn(out, fn+"()", "Cannot represent a stream of type "+h.streamType+" as a select()able descriptor")
				return true
			}
			castable++
			entries[i] = append(entries[i], &selectEntry{key: k, val: dv, h: h})
			return true
		})
	}
	if castable == 0 {
		return types.Value{}, ValueError{"No stream arrays were passed"}
	}
	// PHP 8.3 valide le délai après la recherche des flux, avant de rendre les erreurs d'éléments.
	infinite := deref(z, args[3]).IsNull()
	var sec, usec int64
	var err error
	if !infinite {
		if sec, err = asIntParam(z, args[3], fn, "4", "seconds"); err != nil {
			return types.Value{}, err
		}
		if sec < 0 {
			return types.Value{}, ValueError{fn + "(): Argument #4 ($seconds) must be greater than or equal to 0"}
		}
	}
	if len(args) == 5 && !deref(z, args[4]).IsNull() {
		if infinite {
			return types.Value{}, ValueError{fn + "(): Argument #5 ($microseconds) must be null when argument #4 ($seconds) is null"}
		}
		if usec, err = asIntParam(z, args[4], fn, "5", "microseconds"); err != nil {
			return types.Value{}, err
		}
		if usec < 0 {
			return types.Value{}, ValueError{fn + "(): Argument #5 ($microseconds) must be greater than or equal to 0"}
		}
	}
	if elemErr != nil {
		return types.Value{}, elemErr
	}
	ready := selectReady(entries, time.Duration(sec)*time.Second+time.Duration(usec)*time.Microsecond, infinite)
	// Les tableaux filtrés sont tous construits avant la première affectation : les clés retenues appartiennent
	// aux tableaux d'origine, que l'affectation par référence peut libérer.
	var filtered [3]types.Value
	release := func() {
		for j := range filtered {
			if present[j] && filtered[j].Type == types.TypeArray {
				types.ReleaseValue(z, &filtered[j])
			}
		}
	}
	for i := range sets {
		if !present[i] {
			continue
		}
		if filtered[i], err = types.NewArray(z); err != nil {
			release()
			return types.Value{}, err
		}
		for _, e := range entries[i] {
			if e.ready {
				if err := types.ArraySet(z, &filtered[i], e.key, e.val, warnDrop); err != nil {
					release()
					return types.Value{}, err
				}
			}
		}
	}
	for i := range sets {
		if present[i] {
			assignRefArg(z, args, i, filtered[i])
		}
	}
	return types.NewLong(int64(ready)), nil
}
