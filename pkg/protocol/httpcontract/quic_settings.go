package httpcontract

import (
	"fmt"
	"strings"
	"time"
)

// Script-level QUIC settings (config.http.quic). They tune the QUIC
// connections the HTTP/3 policies open; requests on TCP ignore them. Defaults
// are decision record D4 in docs/HTTP3_IMPLEMENTATION.md.
const (
	QUICHandshakeTimeoutMinMS     = 1000
	QUICHandshakeTimeoutMaxMS     = 60000
	QUICHandshakeTimeoutDefaultMS = 5000
	QUICMaxIdleTimeoutMinMS       = 1000
	QUICMaxIdleTimeoutMaxMS       = 600000
	QUICMaxIdleTimeoutDefaultMS   = 30000
	QUICBrokenTTLMaxMS            = 600000
	QUICBrokenTTLDefaultMS        = 300000

	// QUICAltSvcCachePerVUser remembers Alt-Svc alternatives for the VUser's
	// lifetime, like one long-lived browser profile.
	QUICAltSvcCachePerVUser = "per_vuser"
	// QUICAltSvcCachePerIteration forgets them at each iteration boundary.
	QUICAltSvcCachePerIteration = "per_iteration"
)

// QUICSettings is a session's resolved config.http.quic.
type QUICSettings struct {
	HandshakeTimeout time.Duration
	MaxIdleTimeout   time.Duration
	// KeepAlive is the PING interval; zero sends none.
	KeepAlive time.Duration
	// AltSvcCache is QUICAltSvcCachePerVUser or QUICAltSvcCachePerIteration.
	AltSvcCache string
	// BrokenTTL is how long an origin stays on TCP after a QUIC connection
	// failure; zero keeps no memory of the failure.
	BrokenTTL time.Duration
}

// DefaultQUICSettings returns the settings for an unset config.http.quic.
func DefaultQUICSettings() QUICSettings {
	return QUICSettings{
		HandshakeTimeout: QUICHandshakeTimeoutDefaultMS * time.Millisecond,
		MaxIdleTimeout:   QUICMaxIdleTimeoutDefaultMS * time.Millisecond,
		AltSvcCache:      QUICAltSvcCachePerVUser,
		BrokenTTL:        QUICBrokenTTLDefaultMS * time.Millisecond,
	}
}

// QUIC settings travel in the protocol config under these keys.
const (
	QUICHandshakeTimeoutKey = "http_quic_handshake_timeout_ms"
	QUICMaxIdleTimeoutKey   = "http_quic_max_idle_timeout_ms"
	QUICKeepAliveKey        = "http_quic_keep_alive_ms"
	QUICAltSvcCacheKey      = "http_quic_alt_svc_cache"
	QUICBrokenTTLKey        = "http_quic_broken_ttl_ms"
)

// QUICSettingsConfig carries authored values into the protocol config.
// Unset fields are left out, so the runtime defaults apply.
func QUICSettingsConfig(handshakeTimeoutMS, maxIdleTimeoutMS, keepAliveMS int, altSvcCache string, brokenTTLMS *int) map[string]any {
	config := map[string]any{}
	if handshakeTimeoutMS != 0 {
		config[QUICHandshakeTimeoutKey] = float64(handshakeTimeoutMS)
	}
	if maxIdleTimeoutMS != 0 {
		config[QUICMaxIdleTimeoutKey] = float64(maxIdleTimeoutMS)
	}
	if keepAliveMS != 0 {
		config[QUICKeepAliveKey] = float64(keepAliveMS)
	}
	if value := strings.TrimSpace(altSvcCache); value != "" {
		config[QUICAltSvcCacheKey] = value
	}
	if brokenTTLMS != nil {
		config[QUICBrokenTTLKey] = float64(*brokenTTLMS)
	}
	return config
}

// ValidateQUICSettings checks authored config.http.quic values and returns
// one message per invalid field, naming its path. Zero numeric values mean
// unset, except broken_ttl_ms, which is a pointer so 0 can be authored.
func ValidateQUICSettings(handshakeTimeoutMS, maxIdleTimeoutMS, keepAliveMS int, altSvcCache string, brokenTTLMS *int) []string {
	var issues []string
	if handshakeTimeoutMS != 0 && (handshakeTimeoutMS < QUICHandshakeTimeoutMinMS || handshakeTimeoutMS > QUICHandshakeTimeoutMaxMS) {
		issues = append(issues, fmt.Sprintf("config.http.quic.handshake_timeout_ms must be between %d and %d", QUICHandshakeTimeoutMinMS, QUICHandshakeTimeoutMaxMS))
	}
	maxIdle := QUICMaxIdleTimeoutDefaultMS
	if maxIdleTimeoutMS != 0 {
		if maxIdleTimeoutMS < QUICMaxIdleTimeoutMinMS || maxIdleTimeoutMS > QUICMaxIdleTimeoutMaxMS {
			issues = append(issues, fmt.Sprintf("config.http.quic.max_idle_timeout_ms must be between %d and %d", QUICMaxIdleTimeoutMinMS, QUICMaxIdleTimeoutMaxMS))
		} else {
			maxIdle = maxIdleTimeoutMS
		}
	}
	if keepAliveMS < 0 || keepAliveMS > maxIdle {
		issues = append(issues, fmt.Sprintf("config.http.quic.keep_alive_ms must be between 0 and the max idle timeout (%d)", maxIdle))
	}
	switch strings.TrimSpace(altSvcCache) {
	case "", QUICAltSvcCachePerVUser, QUICAltSvcCachePerIteration:
	default:
		issues = append(issues, "config.http.quic.alt_svc_cache must be per_vuser or per_iteration")
	}
	if brokenTTLMS != nil && (*brokenTTLMS < 0 || *brokenTTLMS > QUICBrokenTTLMaxMS) {
		issues = append(issues, fmt.Sprintf("config.http.quic.broken_ttl_ms must be between 0 and %d", QUICBrokenTTLMaxMS))
	}
	return issues
}
