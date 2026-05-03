package httpmw

import (
	"context"
	"net"
	"net/http"
	"strings"
)

// TrustedProxies controls which RemoteAddr peers are allowed to override
// the client IP via X-Forwarded-For / X-Real-IP. Empty means trust nobody;
// the request stays at its original RemoteAddr. Pass concrete IPs/CIDRs
// when running behind a known reverse proxy.
type TrustedProxies struct {
	cidrs []*net.IPNet
	ips   map[string]struct{}
}

// NewTrustedProxies parses entries (IP literals or CIDR blocks) into a
// TrustedProxies set. Invalid entries are silently dropped — pass them
// through a config validator first if you want strict parsing.
func NewTrustedProxies(entries ...string) *TrustedProxies {
	tp := &TrustedProxies{ips: map[string]struct{}{}}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, ipnet, err := net.ParseCIDR(e); err == nil {
			tp.cidrs = append(tp.cidrs, ipnet)
			continue
		}
		if ip := net.ParseIP(e); ip != nil {
			tp.ips[ip.String()] = struct{}{}
		}
	}
	return tp
}

// Trusts reports whether peer is in the trusted set.
func (tp *TrustedProxies) Trusts(peer net.IP) bool {
	if tp == nil || peer == nil {
		return false
	}
	if _, ok := tp.ips[peer.String()]; ok {
		return true
	}
	for _, cidr := range tp.cidrs {
		if cidr.Contains(peer) {
			return true
		}
	}
	return false
}

// RealIP middleware resolves the originating client IP from forwarded
// headers when the immediate peer is in the trusted set, else falls back
// to RemoteAddr. The resolved IP is stashed on the request context.
//
// Header precedence: X-Forwarded-For (leftmost) → X-Real-IP → RemoteAddr.
func RealIP(trusted *TrustedProxies) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := resolveClientIP(r, trusted)
			ctx := context.WithValue(r.Context(), ctxKeyRealIP, ip)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RealIPFrom returns the resolved client IP stored on r's context by
// RealIP, or "" if the middleware did not run.
func RealIPFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v, ok := r.Context().Value(ctxKeyRealIP).(string); ok {
		return v
	}
	return ""
}

func resolveClientIP(r *http.Request, trusted *TrustedProxies) string {
	peerIP := remoteIP(r)
	if !trusted.Trusts(peerIP) {
		return peerIP.String()
	}
	if xff := r.Header.Get(HeaderForwardedFor); xff != "" {
		// Leftmost is the original client.
		first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		if ip := net.ParseIP(first); ip != nil {
			return ip.String()
		}
	}
	if xri := r.Header.Get(HeaderRealIP); xri != "" {
		if ip := net.ParseIP(strings.TrimSpace(xri)); ip != nil {
			return ip.String()
		}
	}
	return peerIP.String()
}

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(host)
}
