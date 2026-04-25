package config

import (
	"net"
	"strings"
)

// RequiresExternalProtection reports whether the server host is bound to a
// non-loopback interface and should therefore be protected by a reverse proxy,
// VPN, or allowlist before exposing the service outside a trusted boundary.
func RequiresExternalProtection(host string) bool {
	normalized := strings.TrimSpace(host)
	normalized = strings.TrimPrefix(normalized, "[")
	normalized = strings.TrimSuffix(normalized, "]")

	if normalized == "" {
		return false
	}
	if strings.EqualFold(normalized, "localhost") {
		return false
	}

	ip := net.ParseIP(normalized)
	if ip == nil {
		return true
	}

	return !ip.IsLoopback()
}
