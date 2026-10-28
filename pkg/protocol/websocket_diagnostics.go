package protocol

import "math"

// WebSocketDiagnostics describes operations observed by the client. ReceiveMs
// is time waiting for a message, not request/response latency. Only a pong with
// one unambiguous authored ping supplies RTT; diagnostics never send probes.
type WebSocketDiagnostics struct {
	Operation        string    `json:"operation"`
	MessageType      string    `json:"message_type,omitempty"`
	HandshakeMs      *float64  `json:"handshake_ms,omitempty"`
	ReconnectMs      *float64  `json:"reconnect_ms,omitempty"`
	SendMs           *float64  `json:"send_ms,omitempty"`
	ReceiveMs        *float64  `json:"receive_ms,omitempty"`
	WriteWaitMs      *float64  `json:"write_wait_ms,omitempty"`
	PendingWrites    int64     `json:"pending_writes"`
	Reconnects       int64     `json:"reconnects"`
	MessagesSent     int64     `json:"messages_sent"`
	MessagesReceived int64     `json:"messages_received"`
	BytesSent        int64     `json:"bytes_sent"`
	BytesReceived    int64     `json:"bytes_received"`
	PingRTTMs        []float64 `json:"ping_rtt_ms,omitempty"`
	CloseCode        *int32    `json:"close_code,omitempty"`
	DisconnectReason string    `json:"disconnect_reason,omitempty"`
	CallbackMode     bool      `json:"callback_mode,omitempty"`
}

func (d *WebSocketDiagnostics) Valid() bool {
	if d == nil {
		return false
	}
	switch d.Operation {
	case "ws_connect", "ws_send", "ws_receive", "ws_ping", "ws_close", "ws_on_message", "ws_dispatch", "ws_callback":
	default:
		return false
	}
	switch d.MessageType {
	case "", "text", "binary", "ping", "close":
	default:
		return false
	}
	switch d.DisconnectReason {
	case "", "normal", "local_close", "peer_close", "timeout", "cancelled", "transport_error", "callback_overflow":
	default:
		return false
	}
	valid := func(v float64) bool { return v >= 0 && v <= 24*60*60*1000 && !math.IsNaN(v) && !math.IsInf(v, 0) }
	for _, p := range []*float64{d.HandshakeMs, d.ReconnectMs, d.SendMs, d.ReceiveMs, d.WriteWaitMs} {
		if p != nil && !valid(*p) {
			return false
		}
	}
	if len(d.PingRTTMs) > 32 {
		return false
	}
	for _, v := range d.PingRTTMs {
		if !valid(v) {
			return false
		}
	}
	if d.CloseCode != nil && (*d.CloseCode < 1000 || *d.CloseCode > 4999) {
		return false
	}
	for _, n := range []int64{d.PendingWrites, d.Reconnects, d.MessagesSent, d.MessagesReceived} {
		if n < 0 || n > 1<<20 {
			return false
		}
	}
	return d.BytesSent >= 0 && d.BytesSent <= 1<<40 && d.BytesReceived >= 0 && d.BytesReceived <= 1<<40
}

func (d *WebSocketDiagnostics) Clone() *WebSocketDiagnostics {
	if d == nil {
		return nil
	}
	copy := *d
	for _, p := range []**float64{&copy.HandshakeMs, &copy.ReconnectMs, &copy.SendMs, &copy.ReceiveMs, &copy.WriteWaitMs} {
		if *p != nil {
			v := **p
			*p = &v
		}
	}
	if d.CloseCode != nil {
		v := *d.CloseCode
		copy.CloseCode = &v
	}
	copy.PingRTTMs = append([]float64(nil), d.PingRTTMs...)
	return &copy
}

// WebSocketSummary keeps mergeable distributions and independent optional
// measurement counts. Close codes use fixed protocol/application categories.
type WebSocketSummary struct {
	Samples          int64                  `json:"samples"`
	Handshake        DiagnosticDistribution `json:"handshake_ms"`
	Reconnect        DiagnosticDistribution `json:"reconnect_ms"`
	Send             DiagnosticDistribution `json:"send_ms"`
	Receive          DiagnosticDistribution `json:"receive_ms"`
	WriteWait        DiagnosticDistribution `json:"write_wait_ms"`
	PingRTT          DiagnosticDistribution `json:"ping_rtt_ms"`
	TextReceive      DiagnosticDistribution `json:"text_receive_ms"`
	BinaryReceive    DiagnosticDistribution `json:"binary_receive_ms"`
	Reconnects       int64                  `json:"reconnects"`
	MessagesSent     int64                  `json:"messages_sent"`
	MessagesReceived int64                  `json:"messages_received"`
	BytesSent        int64                  `json:"bytes_sent"`
	BytesReceived    int64                  `json:"bytes_received"`
	MaxPendingWrites int64                  `json:"max_pending_writes"`
	NormalCloses     int64                  `json:"normal_closes"`
	AbnormalCloses   int64                  `json:"abnormal_closes"`
	Timeouts         int64                  `json:"timeouts"`
	Cancellations    int64                  `json:"cancellations"`
}

func MergeWebSocketDiagnostic(summary **WebSocketSummary, d *WebSocketDiagnostics) {
	if summary == nil || !d.Valid() {
		return
	}
	if *summary == nil {
		*summary = &WebSocketSummary{}
	}
	s := *summary
	s.Samples++
	for _, pair := range []struct {
		distribution *DiagnosticDistribution
		value        *float64
	}{{&s.Handshake, d.HandshakeMs}, {&s.Reconnect, d.ReconnectMs}, {&s.Send, d.SendMs}, {&s.Receive, d.ReceiveMs}, {&s.WriteWait, d.WriteWaitMs}} {
		if pair.value != nil {
			pair.distribution.Add(*pair.value)
		}
	}
	for _, rtt := range d.PingRTTMs {
		s.PingRTT.Add(rtt)
	}
	if d.ReceiveMs != nil {
		if d.MessageType == "text" {
			s.TextReceive.Add(*d.ReceiveMs)
		} else if d.MessageType == "binary" {
			s.BinaryReceive.Add(*d.ReceiveMs)
		}
	}
	s.Reconnects += d.Reconnects
	s.MessagesSent += d.MessagesSent
	s.MessagesReceived += d.MessagesReceived
	s.BytesSent += d.BytesSent
	s.BytesReceived += d.BytesReceived
	s.MaxPendingWrites = max(s.MaxPendingWrites, d.PendingWrites)
	switch d.DisconnectReason {
	case "normal":
		s.NormalCloses++
	case "timeout":
		s.Timeouts++
	case "cancelled":
		s.Cancellations++
	case "peer_close", "transport_error", "callback_overflow":
		s.AbnormalCloses++
	}
}

func (s *WebSocketSummary) Clone() *WebSocketSummary {
	if s == nil {
		return nil
	}
	copy := *s
	return &copy
}

// WebSocketClientAnalysis exposes canonical client-side WebSocket metrics while
// keeping matched authored ping/pong RTT distinct from receive blocking wait and
// application pending writes distinct from kernel transport queue depth.
type WebSocketClientAnalysis struct {
	PingPongRTT               DiagnosticDistribution `json:"ws_ping_pong_rtt_ms"`
	ReceiveWait               DiagnosticDistribution `json:"ws_receive_wait_ms"`
	ApplicationPendingWrites  int64                  `json:"ws_application_pending_writes"`
	PendingWritesScope        string                 `json:"pending_writes_scope"`
	KernelTransportQueueDepth string                 `json:"kernel_transport_queue_depth"`
	RTTConflationNote         string                 `json:"rtt_conflation_note"`
}

func (s *WebSocketSummary) ClientAnalysis() WebSocketClientAnalysis {
	out := WebSocketClientAnalysis{
		PendingWritesScope:        "application_writer_count",
		KernelTransportQueueDepth: "unavailable",
		RTTConflationNote:         "ws_ping_pong_rtt_ms measures matched authored ping/pong frames only and is never conflated with ws_receive_wait_ms",
	}
	if s == nil {
		return out
	}
	out.PingPongRTT = s.PingRTT
	out.ReceiveWait = s.Receive
	out.ApplicationPendingWrites = s.MaxPendingWrites
	return out
}
