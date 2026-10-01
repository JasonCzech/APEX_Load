package protocol

import (
	"math"
	"regexp"
)

// GRPCDiagnostics contains client-observed RPC evidence. Nil fields are
// unavailable, not zero. Payload bytes exclude compression and wire framing.
// CallMs starts after method resolution; it is not server processing time.
// StatusCode is the final RPC status only when the transport observed one.
type GRPCDiagnostics struct {
	Method              string   `json:"method"`
	Mode                string   `json:"mode"`
	CallMs              float64  `json:"call_ms"`
	FirstMessageMs      *float64 `json:"first_message_ms,omitempty"`
	DeadlineBudgetMs    *float64 `json:"deadline_budget_ms,omitempty"`
	EncodeMs            *float64 `json:"encode_ms,omitempty"`
	DecodeMs            *float64 `json:"decode_ms,omitempty"`
	StatusCode          *int32   `json:"status_code,omitempty"`
	MessagesSent        int64    `json:"messages_sent"`
	MessagesReceived    int64    `json:"messages_received"`
	RequestBytes        int64    `json:"request_bytes"`
	ResponseBytes       int64    `json:"response_bytes"`
	ReceiveLimitReached bool     `json:"receive_limit_reached,omitempty"`
}

var grpcDiagnosticMethod = regexp.MustCompile(`^/[A-Za-z_][A-Za-z0-9_.]*/[A-Za-z_][A-Za-z0-9_]*$`)

// Valid bounds evidence from archives and remote generators before aggregation.
func (d *GRPCDiagnostics) Valid() bool {
	if d == nil || len(d.Method) > 256 || !grpcDiagnosticMethod.MatchString(d.Method) {
		return false
	}
	switch d.Mode {
	case "unary", "server_stream", "client_stream", "bidi_stream":
	default:
		return false
	}
	validMs := func(v float64) bool { return v >= 0 && v <= 24*60*60*1000 && !math.IsNaN(v) && !math.IsInf(v, 0) }
	if !validMs(d.CallMs) || d.FirstMessageMs != nil && (!validMs(*d.FirstMessageMs) || *d.FirstMessageMs > d.CallMs) ||
		d.EncodeMs != nil && !validMs(*d.EncodeMs) || d.DecodeMs != nil && !validMs(*d.DecodeMs) ||
		d.DeadlineBudgetMs != nil && !validMs(*d.DeadlineBudgetMs) || d.StatusCode != nil && (*d.StatusCode < 0 || *d.StatusCode > 16) {
		return false
	}
	// Allow bounded transport retries without accepting unbounded peer counters.
	return d.MessagesSent >= 0 && d.MessagesSent <= 1<<20 && d.MessagesReceived >= 0 && d.MessagesReceived <= 1<<20 &&
		d.RequestBytes >= 0 && d.RequestBytes <= 1<<40 && d.ResponseBytes >= 0 && d.ResponseBytes <= 1<<40 &&
		(d.FirstMessageMs == nil || d.MessagesReceived > 0)
}

func (d *GRPCDiagnostics) Clone() *GRPCDiagnostics {
	if d == nil {
		return nil
	}
	c := *d
	if d.EncodeMs != nil {
		v := *d.EncodeMs
		c.EncodeMs = &v
	}
	if d.DecodeMs != nil {
		v := *d.DecodeMs
		c.DecodeMs = &v
	}
	if d.FirstMessageMs != nil {
		v := *d.FirstMessageMs
		c.FirstMessageMs = &v
	}
	if d.DeadlineBudgetMs != nil {
		v := *d.DeadlineBudgetMs
		c.DeadlineBudgetMs = &v
	}
	if d.StatusCode != nil {
		v := *d.StatusCode
		c.StatusCode = &v
	}
	return &c
}

// DiagnosticDistribution retains mergeable, bounded log histograms. Bucket i
// has upper bound .001 * 1.2^i milliseconds (last bucket is overflow). Quantiles
// are approximate upper bounds; means and maxima use the original observations.
type DiagnosticDistribution struct {
	Count   int64      `json:"count"`
	Sum     float64    `json:"sum"`
	Min     float64    `json:"min"`
	Max     float64    `json:"max"`
	Buckets [144]int64 `json:"buckets"`
}

func (d *DiagnosticDistribution) Add(value float64) {
	if value < 0 || value > 24*60*60*1000 || math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	i := 0
	if value > .001 {
		i = min(len(d.Buckets)-1, int(math.Ceil(math.Log(value/.001)/math.Log(1.2))))
	}
	if d.Count == 0 || value < d.Min {
		d.Min = value
	}
	d.Max = max(d.Max, value)
	d.Count++
	d.Sum += value
	d.Buckets[i]++
}

func (d DiagnosticDistribution) Quantile(q float64) (float64, bool) {
	if d.Count == 0 || q < 0 || q > 1 || math.IsNaN(q) {
		return 0, false
	}
	if q == 0 {
		return d.Min, true
	}
	if q == 1 {
		return d.Max, true
	}
	rank := max(int64(1), int64(math.Ceil(q*float64(d.Count))))
	var count int64
	for i, n := range d.Buckets {
		count += n
		if count >= rank {
			return min(d.Max, max(d.Min, .001*math.Pow(1.2, float64(i)))), true
		}
	}
	return 0, false
}

// GRPCSummary has independent denominators for optional measurements. It can
// describe retained diagnostic samples, not necessarily every run transaction.
type GRPCSummary struct {
	Method              string                 `json:"method,omitempty"`
	Mode                string                 `json:"mode,omitempty"`
	MixedMethods        bool                   `json:"mixed_methods,omitempty"`
	DeadlineObserved    int64                  `json:"deadline_observed,omitempty"`
	NearDeadline        int64                  `json:"near_deadline,omitempty"`
	Samples             int64                  `json:"samples"`
	Call                DiagnosticDistribution `json:"call_ms"`
	FirstMessage        DiagnosticDistribution `json:"first_message_ms"`
	DeadlineBudget      DiagnosticDistribution `json:"deadline_budget_ms"`
	Encode              DiagnosticDistribution `json:"encode_ms"`
	Decode              DiagnosticDistribution `json:"decode_ms"`
	StatusCounts        [17]int64              `json:"status_counts"`
	UnavailableStatus   int64                  `json:"unavailable_status"`
	ReceiveLimitReached int64                  `json:"receive_limit_reached"`
	MessagesSent        int64                  `json:"messages_sent"`
	MessagesReceived    int64                  `json:"messages_received"`
	RequestBytes        int64                  `json:"request_bytes"`
	ResponseBytes       int64                  `json:"response_bytes"`
}

func MergeGRPCDiagnostic(summary **GRPCSummary, d *GRPCDiagnostics) {
	if summary == nil || !d.Valid() {
		return
	}
	if *summary == nil {
		*summary = &GRPCSummary{}
	}
	s := *summary
	if s.Samples == 0 {
		s.Method, s.Mode = d.Method, d.Mode
	} else if s.Method != d.Method || s.Mode != d.Mode {
		s.MixedMethods = true
		s.Method = ""
		s.Mode = ""
	}
	s.Samples++
	s.Call.Add(d.CallMs)
	if d.EncodeMs != nil {
		s.Encode.Add(*d.EncodeMs)
	}
	if d.DecodeMs != nil {
		s.Decode.Add(*d.DecodeMs)
	}
	if d.FirstMessageMs != nil {
		s.FirstMessage.Add(*d.FirstMessageMs)
	}
	if d.DeadlineBudgetMs != nil {
		s.DeadlineBudget.Add(*d.DeadlineBudgetMs)
		if *d.DeadlineBudgetMs > 0 {
			s.DeadlineObserved++
			if d.CallMs / *d.DeadlineBudgetMs >= .8 {
				s.NearDeadline++
			}
		}
	}
	if d.StatusCode != nil {
		s.StatusCounts[*d.StatusCode]++
	} else {
		s.UnavailableStatus++
	}
	if d.ReceiveLimitReached {
		s.ReceiveLimitReached++
	}
	s.MessagesSent += d.MessagesSent
	s.MessagesReceived += d.MessagesReceived
	s.RequestBytes += d.RequestBytes
	s.ResponseBytes += d.ResponseBytes
}

func (s *GRPCSummary) Clone() *GRPCSummary {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}
