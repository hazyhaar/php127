// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/security: Bouclier Anti-SSRF Déterministe (RFC 1918, Link-Local, Cloud Metadata & Obfuscations)

package security

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

type SSRFGuard struct {
	BlockedIPNets  []*net.IPNet
	AllowedSchemes []string
}

func NewSSRFGuard() *SSRFGuard {
	sg := &SSRFGuard{
		AllowedSchemes: []string{"http", "https"},
	}

	// CIDRs prohibés : RFC 1918, Loopback, Link-Local, CGNAT, Cloud Metadata
	cidrs := []string{
		"127.0.0.0/8",    // Loopback IPv4
		"10.0.0.0/8",     // RFC 1918 Privé
		"172.16.0.0/12",  // RFC 1918 Privé
		"192.168.0.0/16", // RFC 1918 Privé
		"169.254.0.0/16", // Link-Local & AWS/GCP/Azure Metadata (169.254.169.254)
		"100.64.0.0/10",  // CGNAT (RFC 6598)
		"0.0.0.0/8",      // "This host on this network"
		"224.0.0.0/4",    // Multicast
		"240.0.0.0/4",    // Reserved
		"::1/128",        // Loopback IPv6
		"::/128",         // Unspecified IPv6
		"fe80::/10",      // Link-Local IPv6
		"fc00::/7",       // Unique Local IPv6 (ULA)
		"::ffff:0:0/96",  // IPv4-mapped IPv6
		"64:ff9b::/96",   // IPv4/IPv6 translation
	}

	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			sg.BlockedIPNets = append(sg.BlockedIPNets, ipNet)
		}
	}

	return sg
}

// InspectURL valide une URL et bloque toute destination privée ou masquée
func (sg *SSRFGuard) InspectURL(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ErrSecuritySSRFViolation
	}

	// Parsing URL
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ErrSecuritySSRFViolation
	}

	// Vérification du protocole / scheme
	if sg.AllowedSchemes != nil {
		allowedScheme := false
		for _, s := range sg.AllowedSchemes {
			if strings.EqualFold(parsed.Scheme, s) {
				allowedScheme = true
				break
			}
		}
		if !allowedScheme {
			return ErrSecuritySSRFViolation
		}
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		hostname = parsed.Host
	}
	hostname = strings.ToLower(strings.TrimSpace(hostname))

	// Détection des alias de localhost et métadonnées cloud
	if hostname == "localhost" ||
		hostname == "localhost.localdomain" ||
		hostname == "instance-data" ||
		hostname == "metadata.google.internal" ||
		strings.HasSuffix(hostname, ".localhost") ||
		strings.HasSuffix(hostname, ".local") {
		return ErrSecuritySSRFViolation
	}

	// Détection wildcard DNS services (nip.io, sslip.io, xip.io) ciblant IP privée
	if strings.Contains(hostname, "127.0.0.1") ||
		strings.Contains(hostname, "169.254.169.254") ||
		strings.Contains(hostname, "10.0.") ||
		strings.Contains(hostname, "192.168.") {
		return ErrSecuritySSRFViolation
	}

	// Détection d'adresses IP encodées en format BSD inet_aton (décimal, octal, hex, 1/2/3/4 parties)
	if ip := parseObfuscatedIP(hostname); ip != nil {
		if sg.isBlockedIP(ip) {
			return ErrSecuritySSRFViolation
		}
	}

	// Parsing IP standard (v4 / v6)
	ip := net.ParseIP(hostname)
	if ip != nil {
		if sg.isBlockedIP(ip) {
			return ErrSecuritySSRFViolation
		}
	}

	return nil
}

func (sg *SSRFGuard) isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, ipNet := range sg.BlockedIPNets {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

func parsePart(p string) (uint64, bool) {
	if strings.HasPrefix(p, "0x") || strings.HasPrefix(p, "0X") {
		val, err := strconv.ParseUint(p[2:], 16, 64)
		return val, err == nil
	}
	if strings.HasPrefix(p, "0") && len(p) > 1 {
		val, err := strconv.ParseUint(p, 8, 64)
		return val, err == nil
	}
	val, err := strconv.ParseUint(p, 10, 64)
	return val, err == nil
}

// parseObfuscatedIP prend en charge tous les formats BSD inet_aton (1, 2, 3, 4 parties en décimal/octal/hex)
func parseObfuscatedIP(s string) net.IP {
	parts := strings.Split(s, ".")
	switch len(parts) {
	case 1:
		val, ok := parsePart(parts[0])
		if ok && val <= 0xFFFFFFFF {
			return net.IPv4(byte(val>>24), byte(val>>16), byte(val>>8), byte(val))
		}
	case 2:
		p0, ok0 := parsePart(parts[0])
		p1, ok1 := parsePart(parts[1])
		if ok0 && ok1 && p0 <= 0xFF && p1 <= 0xFFFFFF {
			return net.IPv4(byte(p0), byte(p1>>16), byte(p1>>8), byte(p1))
		}
	case 3:
		p0, ok0 := parsePart(parts[0])
		p1, ok1 := parsePart(parts[1])
		p2, ok2 := parsePart(parts[2])
		if ok0 && ok1 && ok2 && p0 <= 0xFF && p1 <= 0xFF && p2 <= 0xFFFF {
			return net.IPv4(byte(p0), byte(p1), byte(p2>>8), byte(p2))
		}
	case 4:
		p0, ok0 := parsePart(parts[0])
		p1, ok1 := parsePart(parts[1])
		p2, ok2 := parsePart(parts[2])
		p3, ok3 := parsePart(parts[3])
		if ok0 && ok1 && ok2 && ok3 && p0 <= 0xFF && p1 <= 0xFF && p2 <= 0xFF && p3 <= 0xFF {
			return net.IPv4(byte(p0), byte(p1), byte(p2), byte(p3))
		}
	}
	return nil
}
