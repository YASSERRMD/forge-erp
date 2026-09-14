package platform

import (
	"net"
	"net/http"
	"os"
	"strings"
)

// Trusted-proxy client IP resolution (Phase 0 task 7).
//
// chi's RealIP trusts X-Forwarded-For from any peer, so a direct client can
// spoof its IP and defeat the rate limiter and audit trail. These helpers
// only honor X-Forwarded-For when the TCP peer itself is a configured
// trusted proxy, otherwise the peer address is the client.

// DefaultTrustedProxies allows loopback and RFC 1918 ranges (reverse-proxy
// in front of the API on private networks, plus direct loopback healthchecks).
func DefaultTrustedProxies() []net.IPNet {
	var out []net.IPNet
	for _, cidr := range []string{
		"127.0.0.0/8", "::1/128",
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	} {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		out = append(out, *n)
	}
	return out
}

// ParseTrustedProxies parses a comma-separated CIDR list (FERP_TRUSTED_PROXIES).
func ParseTrustedProxies(csv string) ([]net.IPNet, error) {
	var out []net.IPNet
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			// Accept a bare IP as a /32 (or /128).
			ip := net.ParseIP(part)
			if ip == nil {
				return nil, err
			}
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			out = append(out, net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		out = append(out, *n)
	}
	return out, nil
}

// TrustedProxiesFromEnv resolves the proxy list from FERP_TRUSTED_PROXIES,
// falling back to DefaultTrustedProxies when unset (or unparseable, logged
// by the caller if needed — here we fail closed to loopback only).
func TrustedProxiesFromEnv() []net.IPNet {
	if csv := strings.TrimSpace(os.Getenv("FERP_TRUSTED_PROXIES")); csv != "" {
		if nets, err := ParseTrustedProxies(csv); err == nil {
			return nets
		}
		return []net.IPNet{mustCIDR("127.0.0.0/8"), mustCIDR("::1/128")}
	}
	return DefaultTrustedProxies()
}

func mustCIDR(cidr string) net.IPNet {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	return *n
}

func trustedIP(nets []net.IPNet, ip net.IP) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func parseIPToken(tok string) net.IP {
	tok = strings.TrimSpace(tok)
	if ip := net.ParseIP(tok); ip != nil {
		return ip
	}
	if host, _, err := net.SplitHostPort(tok); err == nil {
		return net.ParseIP(strings.TrimSpace(host))
	}
	return nil
}

// RealIPFromTrusted rewrites r.RemoteAddr to the client IP, honoring
// X-Forwarded-For only when the TCP peer is trusted. With a trusted peer,
// the client is the rightmost untrusted XFF entry (proxy chains append);
// otherwise the peer address itself stands.
func RealIPFromTrusted(trusted []net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				peer = r.RemoteAddr
			}
			peerIP := parseIPToken(peer)
			client := peer
			if peerIP != nil {
				client = peerIP.String()
				if trustedIP(trusted, peerIP) {
					if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
						parts := strings.Split(xff, ",")
						// Right-to-left: skip hops we trust; the first
						// untrusted entry is the client. All-trusted
						// degrades to the leftmost entry (first hop).
						client = strings.TrimSpace(parts[0])
						for i := len(parts) - 1; i >= 0; i-- {
							ip := parseIPToken(parts[i])
							if ip == nil {
								continue
							}
							client = ip.String()
							if !trustedIP(trusted, ip) {
								break
							}
						}
					}
				}
			}
			r.RemoteAddr = client
			next.ServeHTTP(w, r)
		})
	}
}
