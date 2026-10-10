package http

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
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
	quicHandshakeTimeoutKey = "http_quic_handshake_timeout_ms"
	quicMaxIdleTimeoutKey   = "http_quic_max_idle_timeout_ms"
	quicKeepAliveKey        = "http_quic_keep_alive_ms"
	quicAltSvcCacheKey      = "http_quic_alt_svc_cache"
	quicBrokenTTLKey        = "http_quic_broken_ttl_ms"
)

// QUICSettingsConfig carries authored values into the protocol config.
// Unset fields are left out, so the runtime defaults apply.
func QUICSettingsConfig(handshakeTimeoutMS, maxIdleTimeoutMS, keepAliveMS int, altSvcCache string, brokenTTLMS *int) map[string]any {
	config := map[string]any{}
	if handshakeTimeoutMS != 0 {
		config[quicHandshakeTimeoutKey] = float64(handshakeTimeoutMS)
	}
	if maxIdleTimeoutMS != 0 {
		config[quicMaxIdleTimeoutKey] = float64(maxIdleTimeoutMS)
	}
	if keepAliveMS != 0 {
		config[quicKeepAliveKey] = float64(keepAliveMS)
	}
	if value := strings.TrimSpace(altSvcCache); value != "" {
		config[quicAltSvcCacheKey] = value
	}
	if brokenTTLMS != nil {
		config[quicBrokenTTLKey] = float64(*brokenTTLMS)
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

// parseQUICSettings resolves the session's QUIC settings from the protocol
// config, applying the same bounds as validation.
func parseQUICSettings(config map[string]any) (QUICSettings, error) {
	settings := DefaultQUICSettings()
	number := func(key string) (int, bool, error) {
		raw, present := config[key]
		if !present || raw == nil {
			return 0, false, nil
		}
		value, err := exactHTTPConfigInteger(raw)
		if err != nil || value < math.MinInt32 || value > math.MaxInt32 {
			return 0, true, fmt.Errorf("%s must be an integer", key)
		}
		return int(value), true, nil
	}
	handshake, _, err := number(quicHandshakeTimeoutKey)
	if err != nil {
		return settings, err
	}
	maxIdle, _, err := number(quicMaxIdleTimeoutKey)
	if err != nil {
		return settings, err
	}
	keepAlive, _, err := number(quicKeepAliveKey)
	if err != nil {
		return settings, err
	}
	var brokenTTL *int
	if value, present, err := number(quicBrokenTTLKey); err != nil {
		return settings, err
	} else if present {
		brokenTTL = &value
	}
	altSvcCache := protocol.ConfigString(config, quicAltSvcCacheKey)
	if issues := ValidateQUICSettings(handshake, maxIdle, keepAlive, altSvcCache, brokenTTL); len(issues) > 0 {
		return settings, fmt.Errorf("%s", strings.Join(issues, "; "))
	}
	if handshake != 0 {
		settings.HandshakeTimeout = time.Duration(handshake) * time.Millisecond
	}
	if maxIdle != 0 {
		settings.MaxIdleTimeout = time.Duration(maxIdle) * time.Millisecond
	}
	settings.KeepAlive = time.Duration(keepAlive) * time.Millisecond
	if value := strings.TrimSpace(altSvcCache); value != "" {
		settings.AltSvcCache = value
	}
	if brokenTTL != nil {
		settings.BrokenTTL = time.Duration(*brokenTTL) * time.Millisecond
	}
	return settings, nil
}

// QUICOperatorLimits are installation-wide QUIC controls: receive windows,
// the response header limit and packet sizing. They are environment
// settings on the controller and generators, not DSL fields, because they
// size memory per connection and must match the host's capacity plan.
type QUICOperatorLimits struct {
	InitialStreamReceiveWindow     uint64
	MaxStreamReceiveWindow         uint64
	InitialConnectionReceiveWindow uint64
	MaxConnectionReceiveWindow     uint64
	// MaxResponseHeaderBytes matches the TCP transport's limit by default.
	MaxResponseHeaderBytes  int64
	InitialPacketSize       uint16
	DisablePathMTUDiscovery bool
}

// Operator environment variables for QUICOperatorLimits.
const (
	EnvQUICInitialStreamReceiveWindow     = "APEXLOAD_QUIC_INITIAL_STREAM_RECEIVE_WINDOW"
	EnvQUICMaxStreamReceiveWindow         = "APEXLOAD_QUIC_MAX_STREAM_RECEIVE_WINDOW"
	EnvQUICInitialConnectionReceiveWindow = "APEXLOAD_QUIC_INITIAL_CONNECTION_RECEIVE_WINDOW"
	EnvQUICMaxConnectionReceiveWindow     = "APEXLOAD_QUIC_MAX_CONNECTION_RECEIVE_WINDOW"
	EnvQUICMaxResponseHeaderBytes         = "APEXLOAD_QUIC_MAX_RESPONSE_HEADER_BYTES"
	EnvQUICInitialPacketSize              = "APEXLOAD_QUIC_INITIAL_PACKET_SIZE"
	EnvQUICDisablePathMTUDiscovery        = "APEXLOAD_QUIC_DISABLE_PATH_MTU_DISCOVERY"
)

// DefaultQUICOperatorLimits returns quic-go v0.63.0's windows and packet
// size, with the response header limit set to the TCP transport's.
func DefaultQUICOperatorLimits() QUICOperatorLimits {
	return QUICOperatorLimits{
		InitialStreamReceiveWindow:     512 << 10,
		MaxStreamReceiveWindow:         6 << 20,
		InitialConnectionReceiveWindow: 768 << 10,
		MaxConnectionReceiveWindow:     15 << 20,
		MaxResponseHeaderBytes:         maxHTTPResponseHeaderBytes,
		InitialPacketSize:              1280,
	}
}

// Bounds for the operator windows: at least one maximum-size packet, at
// most 1 GiB, which already exceeds any per-connection budget a generator
// can afford across thousands of VUsers.
const (
	minQUICReceiveWindow = 16 << 10
	maxQUICReceiveWindow = 1 << 30
)

// LoadQUICOperatorLimits reads the operator environment. An unset variable
// keeps its default; an invalid one is an error naming the variable, so a
// misconfigured installation fails at startup rather than per request.
func LoadQUICOperatorLimits(getenv func(string) string) (QUICOperatorLimits, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	limits := DefaultQUICOperatorLimits()
	window := func(name string, target *uint64) error {
		raw := strings.TrimSpace(getenv(name))
		if raw == "" {
			return nil
		}
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || value < minQUICReceiveWindow || value > maxQUICReceiveWindow {
			return fmt.Errorf("%s must be a byte count between %d and %d", name, minQUICReceiveWindow, maxQUICReceiveWindow)
		}
		*target = value
		return nil
	}
	for _, field := range []struct {
		name   string
		target *uint64
	}{
		{EnvQUICInitialStreamReceiveWindow, &limits.InitialStreamReceiveWindow},
		{EnvQUICMaxStreamReceiveWindow, &limits.MaxStreamReceiveWindow},
		{EnvQUICInitialConnectionReceiveWindow, &limits.InitialConnectionReceiveWindow},
		{EnvQUICMaxConnectionReceiveWindow, &limits.MaxConnectionReceiveWindow},
	} {
		if err := window(field.name, field.target); err != nil {
			return DefaultQUICOperatorLimits(), err
		}
	}
	if limits.InitialStreamReceiveWindow > limits.MaxStreamReceiveWindow {
		return DefaultQUICOperatorLimits(), fmt.Errorf("%s must not exceed %s", EnvQUICInitialStreamReceiveWindow, EnvQUICMaxStreamReceiveWindow)
	}
	if limits.InitialConnectionReceiveWindow > limits.MaxConnectionReceiveWindow {
		return DefaultQUICOperatorLimits(), fmt.Errorf("%s must not exceed %s", EnvQUICInitialConnectionReceiveWindow, EnvQUICMaxConnectionReceiveWindow)
	}
	if raw := strings.TrimSpace(getenv(EnvQUICMaxResponseHeaderBytes)); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 4<<10 || value > 10<<20 {
			return DefaultQUICOperatorLimits(), fmt.Errorf("%s must be a byte count between %d and %d", EnvQUICMaxResponseHeaderBytes, 4<<10, 10<<20)
		}
		limits.MaxResponseHeaderBytes = value
	}
	if raw := strings.TrimSpace(getenv(EnvQUICInitialPacketSize)); raw != "" {
		// RFC 9000 requires 1200-byte initial packets; Ethernet caps them
		// below 1500 once IP and UDP headers are added.
		value, err := strconv.ParseUint(raw, 10, 16)
		if err != nil || value < 1200 || value > 1452 {
			return DefaultQUICOperatorLimits(), fmt.Errorf("%s must be between 1200 and 1452", EnvQUICInitialPacketSize)
		}
		limits.InitialPacketSize = uint16(value)
	}
	if raw := strings.TrimSpace(getenv(EnvQUICDisablePathMTUDiscovery)); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return DefaultQUICOperatorLimits(), fmt.Errorf("%s must be true or false", EnvQUICDisablePathMTUDiscovery)
		}
		limits.DisablePathMTUDiscovery = value
	}
	return limits, nil
}
