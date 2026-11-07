package protocol

import (
	"encoding/json"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
)

const (
	maxNetworkTraceAddrs   = 8
	maxNetworkTraceCookies = 32
	maxNetworkTraceHeaders = 16
	maxNetworkTraceString  = 256
)

// ReplayNetworkTraceConfigKey enables socket, TLS, cookie, redirect, and
// compression diagnostics on HTTP-based protocol executions.
const ReplayNetworkTraceConfigKey = "apexload_internal_capture_network_trace"

// HTTPNetworkTrace carries bounded, secret-safe transport, TLS, compression,
// cookie, and redirect diagnostics for a single HTTP hop or aggregate request.
//
// An aggregate (request-level) trace describes the connection of the final
// hop: socket, DNS, 100-continue and TLS fields are reset at each hop
// boundary. Only the redirect receipts (CrossOriginRedirect,
// StrippedRedirectHeaders) and RequestCompression accumulate across hops.
// Per-hop traces on NetworkEntries remain authoritative for earlier hops.
//
// ConnectionReused is nil when no connection was observed for the hop (for
// example a dial or DNS failure); otherwise it is on the wire as true (pooled
// socket) or false (fresh socket).
type HTTPNetworkTrace struct {
	// DNS & Socket
	DNSResolvedIPs    []string `json:"dns_resolved_ips,omitempty"`
	DNSCoalesced      bool     `json:"dns_coalesced,omitempty"`
	RemoteAddr        string   `json:"remote_addr,omitempty"`
	LocalAddr         string   `json:"local_addr,omitempty"`
	ConnectionReused  *bool    `json:"connection_reused,omitempty"`
	WasIdle           bool     `json:"was_idle,omitempty"`
	IdleTimeMs        float64  `json:"idle_time_ms,omitempty"`
	Wait100ContinueMs float64  `json:"wait_100_continue_ms,omitempty"`
	Got100Continue    bool     `json:"got_100_continue,omitempty"`

	// TLS Handshake & Leaf Certificate
	TLSVersion         string `json:"tls_version,omitempty"`
	TLSCipherSuite     string `json:"tls_cipher_suite,omitempty"`
	TLSNegotiatedProto string `json:"tls_negotiated_proto,omitempty"`
	TLSServerName      string `json:"tls_server_name,omitempty"`
	TLSDidResume       bool   `json:"tls_did_resume,omitempty"`
	TLSCertSubject     string `json:"tls_cert_subject,omitempty"`
	TLSCertIssuer      string `json:"tls_cert_issuer,omitempty"`
	TLSCertExpiry      string `json:"tls_cert_expiry,omitempty"`

	// Wire Protocol & Payload Encoding
	HTTPProtocol       string   `json:"http_protocol,omitempty"`
	RequestCompression string   `json:"request_compression,omitempty"`
	ContentEncoding    string   `json:"content_encoding,omitempty"`
	TransferEncoding   []string `json:"transfer_encoding,omitempty"`
	EncodedBodyBytes   int64    `json:"encoded_body_bytes,omitempty"`
	DecodedBodyBytes   int64    `json:"decoded_body_bytes,omitempty"`

	// Cookie & Redirect Receipts (names and counts only, never secret values)
	CookiesSentCount        int      `json:"cookies_sent_count,omitempty"`
	CookieNamesSent         []string `json:"cookie_names_sent,omitempty"`
	SetCookiesReceivedCount int      `json:"set_cookies_received_count,omitempty"`
	SetCookieNamesReceived  []string `json:"set_cookie_names_received,omitempty"`
	CrossOriginRedirect     bool     `json:"cross_origin_redirect,omitempty"`
	StrippedRedirectHeaders []string `json:"stripped_redirect_headers,omitempty"`
}

// Clone returns a bounded deep copy of the network trace.
func (t *HTTPNetworkTrace) Clone() *HTTPNetworkTrace {
	if t == nil {
		return nil
	}
	out := *t
	if t.ConnectionReused != nil {
		reused := *t.ConnectionReused
		out.ConnectionReused = &reused
	}
	out.RemoteAddr = cloneBoundedTraceString(t.RemoteAddr, maxNetworkTraceString)
	out.LocalAddr = cloneBoundedTraceString(t.LocalAddr, maxNetworkTraceString)
	out.TLSVersion = cloneBoundedTraceString(t.TLSVersion, maxNetworkTraceString)
	out.TLSCipherSuite = cloneBoundedTraceString(t.TLSCipherSuite, maxNetworkTraceString)
	out.TLSNegotiatedProto = cloneBoundedTraceString(t.TLSNegotiatedProto, maxNetworkTraceString)
	out.TLSServerName = cloneBoundedTraceString(t.TLSServerName, maxNetworkTraceString)
	out.TLSCertSubject = cloneBoundedTraceString(t.TLSCertSubject, maxNetworkTraceString)
	out.TLSCertIssuer = cloneBoundedTraceString(t.TLSCertIssuer, maxNetworkTraceString)
	out.TLSCertExpiry = cloneBoundedTraceString(t.TLSCertExpiry, maxNetworkTraceString)
	out.HTTPProtocol = cloneBoundedTraceString(t.HTTPProtocol, maxNetworkTraceString)
	out.RequestCompression = cloneBoundedTraceString(t.RequestCompression, maxNetworkTraceString)
	out.ContentEncoding = cloneBoundedTraceString(t.ContentEncoding, maxNetworkTraceString)
	out.DNSResolvedIPs = cloneBoundedTraceSlice(t.DNSResolvedIPs, maxNetworkTraceAddrs, maxNetworkTraceString)
	out.TransferEncoding = cloneBoundedTraceSlice(t.TransferEncoding, maxNetworkTraceHeaders, maxNetworkTraceString)
	out.CookieNamesSent = cloneBoundedTraceSlice(t.CookieNamesSent, maxNetworkTraceCookies, maxNetworkTraceString)
	out.SetCookieNamesReceived = cloneBoundedTraceSlice(t.SetCookieNamesReceived, maxNetworkTraceCookies, maxNetworkTraceString)
	out.StrippedRedirectHeaders = cloneBoundedTraceSlice(t.StrippedRedirectHeaders, maxNetworkTraceHeaders, maxNetworkTraceString)
	return &out
}

func cloneBoundedTraceString(value string, maxBytes int) string {
	if value == "" {
		return ""
	}
	return strings.Clone(boundedtext.TruncateUTF8(value, maxBytes))
}

func cloneBoundedTraceSlice(values []string, maxItems, maxItemBytes int) []string {
	if len(values) == 0 || maxItems <= 0 {
		return nil
	}
	limit := min(len(values), maxItems)
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		if item := cloneBoundedTraceString(values[i], maxItemBytes); item != "" {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// TransformStrings returns a bounded copy of the trace with transform applied
// to every retained string, including each element of the list fields. List
// fields stay lists, so a redaction pass never turns a name list into a scalar.
// Items the transform empties are dropped.
func (t *HTTPNetworkTrace) TransformStrings(transform func(string) string) *HTTPNetworkTrace {
	out := t.Clone()
	if out == nil || transform == nil {
		return out
	}
	for _, field := range []*string{
		&out.RemoteAddr, &out.LocalAddr,
		&out.TLSVersion, &out.TLSCipherSuite, &out.TLSNegotiatedProto, &out.TLSServerName,
		&out.TLSCertSubject, &out.TLSCertIssuer, &out.TLSCertExpiry,
		&out.HTTPProtocol, &out.RequestCompression, &out.ContentEncoding,
	} {
		if *field != "" {
			*field = transform(*field)
		}
	}
	for _, list := range []*[]string{
		&out.DNSResolvedIPs, &out.TransferEncoding, &out.CookieNamesSent,
		&out.SetCookieNamesReceived, &out.StrippedRedirectHeaders,
	} {
		for i := range *list {
			(*list)[i] = transform((*list)[i])
		}
	}
	return out.Clone()
}

// EvidenceMap converts the network trace into a bounded-evidence compatible map.
func (t *HTTPNetworkTrace) EvidenceMap() map[string]any {
	if t == nil {
		return nil
	}
	m := make(map[string]any)
	if len(t.DNSResolvedIPs) > 0 {
		m["dns_resolved_ips"] = append([]string(nil), t.DNSResolvedIPs...)
	}
	if t.DNSCoalesced {
		m["dns_coalesced"] = true
	}
	if t.RemoteAddr != "" {
		m["remote_addr"] = t.RemoteAddr
	}
	if t.LocalAddr != "" {
		m["local_addr"] = t.LocalAddr
	}
	if t.ConnectionReused != nil {
		m["connection_reused"] = *t.ConnectionReused
	}
	if t.WasIdle {
		m["was_idle"] = true
	}
	if t.IdleTimeMs > 0 {
		m["idle_time_ms"] = t.IdleTimeMs
	}
	if t.Wait100ContinueMs > 0 {
		m["wait_100_continue_ms"] = t.Wait100ContinueMs
	}
	if t.Got100Continue {
		m["got_100_continue"] = true
	}
	if t.TLSVersion != "" {
		m["tls_version"] = t.TLSVersion
	}
	if t.TLSCipherSuite != "" {
		m["tls_cipher_suite"] = t.TLSCipherSuite
	}
	if t.TLSNegotiatedProto != "" {
		m["tls_negotiated_proto"] = t.TLSNegotiatedProto
	}
	if t.TLSServerName != "" {
		m["tls_server_name"] = t.TLSServerName
	}
	if t.TLSDidResume {
		m["tls_did_resume"] = true
	}
	if t.TLSCertSubject != "" {
		m["tls_cert_subject"] = t.TLSCertSubject
	}
	if t.TLSCertIssuer != "" {
		m["tls_cert_issuer"] = t.TLSCertIssuer
	}
	if t.TLSCertExpiry != "" {
		m["tls_cert_expiry"] = t.TLSCertExpiry
	}
	if t.HTTPProtocol != "" {
		m["http_protocol"] = t.HTTPProtocol
	}
	if t.RequestCompression != "" {
		m["request_compression"] = t.RequestCompression
	}
	if t.ContentEncoding != "" {
		m["content_encoding"] = t.ContentEncoding
	}
	if len(t.TransferEncoding) > 0 {
		m["transfer_encoding"] = append([]string(nil), t.TransferEncoding...)
	}
	if t.EncodedBodyBytes > 0 {
		m["encoded_body_bytes"] = t.EncodedBodyBytes
	}
	if t.DecodedBodyBytes > 0 {
		m["decoded_body_bytes"] = t.DecodedBodyBytes
	}
	if t.CookiesSentCount > 0 {
		m["cookies_sent_count"] = t.CookiesSentCount
	}
	if len(t.CookieNamesSent) > 0 {
		m["cookie_names_sent"] = append([]string(nil), t.CookieNamesSent...)
	}
	if t.SetCookiesReceivedCount > 0 {
		m["set_cookies_received_count"] = t.SetCookiesReceivedCount
	}
	if len(t.SetCookieNamesReceived) > 0 {
		m["set_cookie_names_received"] = append([]string(nil), t.SetCookieNamesReceived...)
	}
	if t.CrossOriginRedirect {
		m["cross_origin_redirect"] = true
	}
	if len(t.StrippedRedirectHeaders) > 0 {
		m["stripped_redirect_headers"] = append([]string(nil), t.StrippedRedirectHeaders...)
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// HTTPNetworkTraceFromEvidence reconstructs an HTTPNetworkTrace from a bounded
// protocol_evidence["network_trace"] value or returns nil.
func HTTPNetworkTraceFromEvidence(raw any) *HTTPNetworkTrace {
	switch v := raw.(type) {
	case *HTTPNetworkTrace:
		return v.Clone()
	case HTTPNetworkTrace:
		return v.Clone()
	case map[string]any:
		if len(v) == 0 {
			return nil
		}
		t := &HTTPNetworkTrace{
			DNSResolvedIPs:          evidenceStringSlice(v["dns_resolved_ips"]),
			DNSCoalesced:            evidenceBool(v["dns_coalesced"]),
			RemoteAddr:              evidenceString(v["remote_addr"]),
			LocalAddr:               evidenceString(v["local_addr"]),
			ConnectionReused:        evidenceBoolPointer(v["connection_reused"]),
			WasIdle:                 evidenceBool(v["was_idle"]),
			IdleTimeMs:              evidenceFloat64(v["idle_time_ms"]),
			Wait100ContinueMs:       evidenceFloat64(v["wait_100_continue_ms"]),
			Got100Continue:          evidenceBool(v["got_100_continue"]),
			TLSVersion:              evidenceString(v["tls_version"]),
			TLSCipherSuite:          evidenceString(v["tls_cipher_suite"]),
			TLSNegotiatedProto:      evidenceString(v["tls_negotiated_proto"]),
			TLSServerName:           evidenceString(v["tls_server_name"]),
			TLSDidResume:            evidenceBool(v["tls_did_resume"]),
			TLSCertSubject:          evidenceString(v["tls_cert_subject"]),
			TLSCertIssuer:           evidenceString(v["tls_cert_issuer"]),
			TLSCertExpiry:           evidenceString(v["tls_cert_expiry"]),
			HTTPProtocol:            evidenceString(v["http_protocol"]),
			RequestCompression:      evidenceString(v["request_compression"]),
			ContentEncoding:         evidenceString(v["content_encoding"]),
			TransferEncoding:        evidenceStringSlice(v["transfer_encoding"]),
			EncodedBodyBytes:        evidenceInt64(v["encoded_body_bytes"]),
			DecodedBodyBytes:        evidenceInt64(v["decoded_body_bytes"]),
			CookiesSentCount:        int(evidenceInt64(v["cookies_sent_count"])),
			CookieNamesSent:         evidenceStringSlice(v["cookie_names_sent"]),
			SetCookiesReceivedCount: int(evidenceInt64(v["set_cookies_received_count"])),
			SetCookieNamesReceived:  evidenceStringSlice(v["set_cookie_names_received"]),
			CrossOriginRedirect:     evidenceBool(v["cross_origin_redirect"]),
			StrippedRedirectHeaders: evidenceStringSlice(v["stripped_redirect_headers"]),
		}
		return t.Clone()
	default:
		return nil
	}
}

func evidenceString(v any) string {
	s, _ := v.(string)
	return s
}

func evidenceBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func evidenceBoolPointer(v any) *bool {
	b, ok := v.(bool)
	if !ok {
		return nil
	}
	return &b
}

func evidenceFloat64(v any) float64 {
	switch n := v.(type) {
	case json.Number:
		f, _ := n.Float64()
		return f
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func evidenceInt64(v any) int64 {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
		f, _ := n.Float64()
		return int64(f)
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case float32:
		return int64(n)
	default:
		return 0
	}
}

func evidenceStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		if len(s) == 0 {
			return nil
		}
		return append([]string(nil), s...)
	case []any:
		if len(s) == 0 {
			return nil
		}
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok && str != "" {
				out = append(out, str)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}
