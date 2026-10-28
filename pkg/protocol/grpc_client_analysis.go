package protocol

import "math"

// GRPCClientAnalysis describes the retained diagnostic cohort. Rates use the
// sum of client-call durations, never elapsed run time or server processing.
// Codec elapsed work can overlap in streaming calls and can exceed wall time.
type GRPCClientAnalysis struct {
	SentMessagesPerCallSecond     *float64 `json:"sent_messages_per_call_second,omitempty"`
	ReceivedMessagesPerCallSecond *float64 `json:"received_messages_per_call_second,omitempty"`
	SentBytesPerCallSecond        *float64 `json:"sent_bytes_per_call_second,omitempty"`
	ReceivedBytesPerCallSecond    *float64 `json:"received_bytes_per_call_second,omitempty"`
	MeanSentPayloadBytes          *float64 `json:"mean_sent_payload_bytes,omitempty"`
	MeanReceivedPayloadBytes      *float64 `json:"mean_received_payload_bytes,omitempty"`
	CodecWorkToCallWallPct        *float64 `json:"codec_work_to_call_wall_pct,omitempty"`
	UncompressedRequestBytes      *int64   `json:"grpc_uncompressed_request_bytes,omitempty"`
	UncompressedResponseBytes     *int64   `json:"grpc_uncompressed_response_bytes,omitempty"`
	UncompressedPayloadBytes      *int64   `json:"grpc_uncompressed_payload_bytes,omitempty"`
	ClientCallsPerCallSecond      *float64 `json:"grpc_client_call_rate,omitempty"`
	CodecOverlapRatio             *float64 `json:"grpc_codec_overlap_ratio,omitempty"`
	HTTP2TransportPhase           string   `json:"http2_transport_phase,omitempty"`
	ServerProcessingPhase         string   `json:"server_processing_phase,omitempty"`
}

func (s *GRPCSummary) ClientAnalysis() GRPCClientAnalysis {
	out := GRPCClientAnalysis{
		HTTP2TransportPhase:   "unavailable",
		ServerProcessingPhase: "unavailable",
	}
	const maxSafe int64 = 1<<53 - 1
	countOK := func(v int64) bool { return v >= 0 && v <= maxSafe }
	sumOK := func(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
	ratio := func(n, d float64) *float64 {
		if !sumOK(n) || !sumOK(d) || d <= 0 {
			return nil
		}
		v := n / d
		if !sumOK(v) {
			return nil
		}
		return &v
	}
	if s == nil || s.Samples <= 0 || !countOK(s.Samples) {
		return out
	}
	sentOK := countOK(s.MessagesSent) && countOK(s.RequestBytes)
	receivedOK := countOK(s.MessagesReceived) && countOK(s.ResponseBytes)
	if sentOK {
		reqBytes := s.RequestBytes
		out.UncompressedRequestBytes = &reqBytes
		out.MeanSentPayloadBytes = ratio(float64(s.RequestBytes), float64(s.MessagesSent))
	}
	if receivedOK {
		respBytes := s.ResponseBytes
		out.UncompressedResponseBytes = &respBytes
		out.MeanReceivedPayloadBytes = ratio(float64(s.ResponseBytes), float64(s.MessagesReceived))
	}
	if sentOK && receivedOK && s.RequestBytes <= maxSafe-s.ResponseBytes {
		totalBytes := s.RequestBytes + s.ResponseBytes
		out.UncompressedPayloadBytes = &totalBytes
	}
	if s.Call.Count != s.Samples || !sumOK(s.Call.Sum) || s.Call.Sum <= 0 {
		return out
	}
	seconds := s.Call.Sum / 1000
	out.ClientCallsPerCallSecond = ratio(float64(s.Samples), seconds)
	if sentOK {
		out.SentMessagesPerCallSecond = ratio(float64(s.MessagesSent), seconds)
		out.SentBytesPerCallSecond = ratio(float64(s.RequestBytes), seconds)
	}
	if receivedOK {
		out.ReceivedMessagesPerCallSecond = ratio(float64(s.MessagesReceived), seconds)
		out.ReceivedBytesPerCallSecond = ratio(float64(s.ResponseBytes), seconds)
	}
	if s.Encode.Count == s.Samples && s.Decode.Count == s.Samples && sumOK(s.Encode.Sum) && sumOK(s.Decode.Sum) {
		// Unclamped: streaming or concurrent codec work can exceed 1.0 (100%) of call wall time.
		out.CodecOverlapRatio = ratio(s.Encode.Sum+s.Decode.Sum, s.Call.Sum)
		out.CodecWorkToCallWallPct = ratio(100*(s.Encode.Sum+s.Decode.Sum), s.Call.Sum)
	}
	return out
}
