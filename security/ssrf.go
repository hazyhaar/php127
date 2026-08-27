// SPDX-License-Identifier: Apache-2.0 OR MIT
// php127/security: Détection et neutralisation des attaques SSRF (Server-Side Request Forgery)

package security

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

var (
	ErrSecuritySSRFViolation = errors.New("security: tentative d'accès à une ressource réseau interne/interdite (SSRF Blocked)")
)

// SSRFGuard gère les règles de filtrage des requêtes sortantes (cURL, file_get_contents, fsockopen)
type SSRFGuard struct {
	AllowedSchemes []string
	AllowedHosts   []string
	BlockedIPNets  []*net.IPNet
}

func NewSSRFGuard() *SSRFGuard {
	sg := &SSRFGuard{
		AllowedSchemes: []string{"http", "https"},
	}
	sg.initDefaultBlockedNets()
	return sg
}

func (sg *SSRFGuard) initDefaultBlockedNets() {
	blockedCIDRs := []string{
		"127.0.0.0/8",          // Loopback IPv4
		"10.0.0.0/8",           // RFC 1918 Private
		"172.16.0.0/12",        // RFC 1918 Private
		"192.168.0.0/16",       // RFC 1918 Private
		"169.254.0.0/16",       // Link-Local / Cloud Metadata (AWS/GCP/Azure)
		"100.64.0.0/10",        // CGNAT
		"0.0.0.0/8",            // "This" network
		"224.0.0.0/4",          // Multicast
		"240.0.0.0/4",          // Reserved
		"::1/128",              // Loopback IPv6
		"fc00::/7",             // Unique Local Address IPv6 (ULA)
		"fe80::/10",            // Link-Local IPv6
		"::ffff:0:0/96",        // IPv4-mapped IPv6
		"::ffff:127.0.0.1/128", // IPv4-mapped loopback
	}

	for _, cidr := range blockedCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil && ipNet != nil {
			sg.BlockedIPNets = append(sg.BlockedIPNets, ipNet)
		}
	}
}

// InspectURL analyse une URL cible et bloque toute tentative SSRF
func (sg *SSRFGuard) InspectURL(targetURL string) error {
	trimmed := strings.TrimSpace(targetURL)
	if trimmed == "" {
		return nil
	}

	// Normalisation et gestion des préfixes et encodages
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ErrSecuritySSRFViolation
	}

	// Vérification du schéma
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "" {
		allowedScheme := false
		for _, s := range sg.AllowedSchemes {
			if scheme == s {
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

	// Détection d'adresses IP encodées en décimal (ex: 2130706433 pour 127.0.0.1) ou octal
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

// parseObfuscatedIP prend en charge les formats entiers décimaux (ex: http://2130706433/) ou octaux
func parseObfuscatedIP(s string) net.IP {
	// Entier brut (32-bit int)
	if val, err := strconv.ParseUint(s, 10, 32); err == nil {
		return net.IPv4(byte(val>>24), byte(val>>16), byte(val>>8), byte(val))
	}
	// Hexadécimal brut (0x7f000001)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		if val, err := strconv.ParseUint(s[2:], 16, 32); err == nil {
			return net.IPv4(byte(val>>24), byte(val>>16), byte(val>>8), byte(val))
		}
	}
	// Notation octale pointée (ex: 0177.0.0.1)
	parts := strings.Split(s, ".")
	if len(parts) == 4 {
		var octets [4]byte
		for i, p := range parts {
			if strings.HasPrefix(p, "0") && len(p) > 1 {
				val, err := strconv.ParseUint(p, 8, 8)
				if err != nil {
					return nil
				}
				octets[i] = byte(val)
			} else {
				val, err := strconv.ParseUint(p, 10, 8)
				if err != nil {
					return nil
				}
				octets[i] = byte(val)
			}
		}
		return net.IPv4(octets[0], octets[1], octets[2], octets[3])
	}
	return nil
}
