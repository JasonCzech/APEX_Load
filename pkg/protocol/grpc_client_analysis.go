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
}

func (s *GRPCSummary) ClientAnalysis() GRPCClientAnalysis {
	out := GRPCClientAnalysis{}
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
		out.MeanSentPayloadBytes = ratio(float64(s.RequestBytes), float64(s.MessagesSent))
	}
	if receivedOK {
		out.MeanReceivedPayloadBytes = ratio(float64(s.ResponseBytes), float64(s.MessagesReceived))
	}
	if s.Call.Count != s.Samples || !sumOK(s.Call.Sum) || s.Call.Sum <= 0 {
		return out
	}
	seconds := s.Call.Sum / 1000
	if sentOK {
		out.SentMessagesPerCallSecond = ratio(float64(s.MessagesSent), seconds)
		out.SentBytesPerCallSecond = ratio(float64(s.RequestBytes), seconds)
	}
	if receivedOK {
		out.ReceivedMessagesPerCallSecond = ratio(float64(s.MessagesReceived), seconds)
		out.ReceivedBytesPerCallSecond = ratio(float64(s.ResponseBytes), seconds)
	}
	if s.Encode.Count == s.Samples && s.Decode.Count == s.Samples && sumOK(s.Encode.Sum) && sumOK(s.Decode.Sum) {
		out.CodecWorkToCallWallPct = ratio(100*(s.Encode.Sum+s.Decode.Sum), s.Call.Sum)
	}
	return out
}
