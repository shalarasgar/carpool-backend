package middleware

import (
	"net"
	"net/http"
	"strings"
)

// ForwardedFor replaces r.RemoteAddr with the client address a reverse proxy
// reports in X-Forwarded-For, so per-IP OTP limits count clients rather than
// the proxy. Only the last entry is used: it is the one the proxy itself
// appended, while earlier entries come from the client and can be forged.
//
// Enable it only when every request arrives through that proxy. If the API
// port is also reachable directly, any caller can pick its own address.
func ForwardedFor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := lastForwarded(r.Header.Values("X-Forwarded-For")); ip != "" {
			r.RemoteAddr = net.JoinHostPort(ip, "0")
		}
		next.ServeHTTP(w, r)
	})
}

// lastForwarded returns the last valid IP in the X-Forwarded-For values, or "".
func lastForwarded(values []string) string {
	if len(values) == 0 {
		return ""
	}
	parts := strings.Split(values[len(values)-1], ",")
	ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1]))
	if ip == nil {
		return ""
	}
	return ip.String()
}
