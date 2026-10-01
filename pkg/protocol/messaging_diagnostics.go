package protocol

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxMessagingDimensions = 64

// MessagingMoments keeps observed zero distinct from unavailable measurements.
// Sums and counts merge exactly; these moments cannot produce percentiles.
type MessagingMoments struct {
	Count int64   `json:"count"`
	Sum   float64 `json:"sum"`
	Max   float64 `json:"max"`
}

func (m *MessagingMoments) add(v float64) {
	if validMessagingNumber(v) {
		m.Count++
		m.Sum += v
		m.Max = max(m.Max, v)
	}
}
func (m *MessagingMoments) merge(v MessagingMoments) {
	m.Count += v.Count
	m.Sum += v.Sum
	m.Max = max(m.Max, v.Max)
}
func validMessagingNumber(v float64) bool {
	return v >= 0 && v <= 1e12 && !math.IsNaN(v) && !math.IsInf(v, 0)
}
func (m MessagingMoments) valid() bool {
	if m.Count == 0 {
		return m.Sum == 0 && m.Max == 0
	}
	if m.Count < 0 || m.Count > 1e12 || !validMessagingNumber(m.Max) || m.Sum < 0 || math.IsNaN(m.Sum) || math.IsInf(m.Sum, 0) {
		return false
	}
	tolerance := 1e-12 * math.Max(1, float64(m.Count)*m.Max)
	return m.Sum <= float64(m.Count)*m.Max+tolerance && m.Sum+tolerance >= m.Max
}
func (m MessagingMoments) Mean() (float64, bool) {
	if m.Count == 0 {
		return 0, false
	}
	return m.Sum / float64(m.Count), true
}

type MessagingDimension struct {
	Source       string           `json:"source"`
	Partition    string           `json:"partition,omitempty"`
	Subscription string           `json:"subscription,omitempty"`
	Operations   int64            `json:"operations"`
	Sent         int64            `json:"sent"`
	Received     int64            `json:"received"`
	Throttled    int64            `json:"throttled"`
	Redelivered  int64            `json:"redelivered"`
	Ack          MessagingMoments `json:"ack_ms"`
	Age          MessagingMoments `json:"age_ms"`
	Offset       MessagingMoments `json:"offset_lag"`
}

// MessagingSummary preserves bounded source/partition/subscription intersections.
// Incomplete means cardinality overflow, invalid evidence or unavailable peers.
// It describes client observations, never an inventory of broker partitions.
type MessagingSummary struct {
	Dimensions []MessagingDimension `json:"dimensions"`
	Incomplete bool                 `json:"incomplete,omitempty"`
}

func messagingLabel(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 256 || !utf8.ValidString(v) || strings.ContainsFunc(v, unicode.IsControl) {
		return ""
	}
	return v
}
func messagingKey(d MessagingDimension) [3]string {
	return [3]string{d.Source, d.Partition, d.Subscription}
}
func (s *MessagingSummary) Clone() *MessagingSummary {
	if s == nil {
		return nil
	}
	c := *s
	c.Dimensions = append([]MessagingDimension(nil), s.Dimensions...)
	return &c
}
func (s *MessagingSummary) Valid() bool {
	if s == nil || len(s.Dimensions) > MaxMessagingDimensions {
		return false
	}
	seen := map[[3]string]bool{}
	for _, d := range s.Dimensions {
		if !d.valid() {
			return false
		}
		k := messagingKey(d)
		if seen[k] {
			return false
		}
		seen[k] = true
	}
	return true
}
func (d MessagingDimension) valid() bool {
	if messagingLabel(d.Source) != d.Source || messagingLabel(d.Partition) != d.Partition || messagingLabel(d.Subscription) != d.Subscription || !d.Ack.valid() || !d.Age.valid() || !d.Offset.valid() {
		return false
	}
	for _, n := range []int64{d.Operations, d.Sent, d.Received, d.Throttled, d.Redelivered} {
		if n < 0 || n > 1e12 {
			return false
		}
	}
	if d.Ack.Count > d.Operations || d.Age.Count > d.Operations || d.Offset.Count > d.Operations {
		return false
	}
	return true
}

func MergeMessagingSummary(dst **MessagingSummary, src *MessagingSummary) {
	if dst == nil || src == nil {
		return
	}
	if *dst == nil {
		*dst = &MessagingSummary{}
	}
	s := *dst
	if !src.Valid() {
		s.Incomplete = true
		return
	}
	s.Incomplete = s.Incomplete || src.Incomplete
	added := false
	for _, d := range src.Dimensions {
		index := -1
		key := messagingKey(d)
		for i := range s.Dimensions {
			if messagingKey(s.Dimensions[i]) == key {
				index = i
				break
			}
		}
		if index < 0 {
			if len(s.Dimensions) >= MaxMessagingDimensions {
				s.Incomplete = true
				continue
			}
			s.Dimensions = append(s.Dimensions, d)
			added = true
			continue
		}
		candidate := s.Dimensions[index]
		a := &candidate
		a.Operations += d.Operations
		a.Sent += d.Sent
		a.Received += d.Received
		a.Throttled += d.Throttled
		a.Redelivered += d.Redelivered
		a.Ack.merge(d.Ack)
		a.Age.merge(d.Age)
		a.Offset.merge(d.Offset)
		if candidate.valid() {
			s.Dimensions[index] = candidate
		} else {
			s.Incomplete = true
		}
	}
	if added {
		sort.Slice(s.Dimensions, func(i, j int) bool {
			a, b := messagingKey(s.Dimensions[i]), messagingKey(s.Dimensions[j])
			for k := range a {
				if a[k] != b[k] {
					return a[k] < b[k]
				}
			}
			return false
		})
	}
}
func MergeMessagingTelemetry(dst **MessagingSummary, t *ServerTelemetry, source string, kinds ...string) {
	if t != nil && (t.GRPC != nil || t.WebSocket != nil) {
		return
	}
	if len(kinds) > 0 && !strings.HasPrefix(kinds[0], "jms") && !strings.HasPrefix(kinds[0], "kafka") && !strings.HasPrefix(kinds[0], "eventhubs") {
		return
	}
	if t == nil || (t.PartitionID == "" && t.SubscriptionID == "" && t.MessagesSent == 0 && t.MessagesReceived == 0 && t.ThrottledRequests == 0 && t.RedeliveryCount == 0 && t.AcknowledgementMs == nil && !t.ConsumerLagObserved && !t.ConsumerOffsetLagObserved) {
		return
	}
	d := MessagingDimension{Source: messagingLabel(source), Partition: messagingLabel(t.PartitionID), Subscription: messagingLabel(t.SubscriptionID), Operations: 1, Sent: int64(t.MessagesSent), Received: int64(t.MessagesReceived), Throttled: int64(t.ThrottledRequests), Redelivered: int64(t.RedeliveryCount)}
	incomplete := d.Source == "" || d.Source != strings.TrimSpace(source) || d.Partition != strings.TrimSpace(t.PartitionID) || d.Subscription != strings.TrimSpace(t.SubscriptionID)
	if t.AcknowledgementMs != nil {
		d.Ack.add(*t.AcknowledgementMs)
		incomplete = incomplete || d.Ack.Count == 0
	}
	if t.ConsumerLagObserved || t.ConsumerLagMs > 0 {
		d.Age.add(t.ConsumerLagMs)
		incomplete = incomplete || d.Age.Count == 0
	}
	if t.ConsumerOffsetLagObserved || t.ConsumerOffsetLag > 0 {
		d.Offset.add(float64(t.ConsumerOffsetLag))
		incomplete = incomplete || d.Offset.Count == 0
	}
	MergeMessagingSummary(dst, &MessagingSummary{Dimensions: []MessagingDimension{d}, Incomplete: incomplete})
}

// ObservedPartitionSkew returns CV for observed producer partitions within one
// source. Missing broker partitions are unknown. Mixed sources are not pooled.
func (s *MessagingSummary) ObservedPartitionSkew() (float64, bool) {
	if s == nil || s.Incomplete {
		return 0, false
	}
	counts := map[string]int64{}
	source := ""
	for _, d := range s.Dimensions {
		if d.Sent <= 0 {
			continue
		}
		if d.Source == "" || d.Partition == "" {
			return 0, false
		}
		if source != "" && source != d.Source {
			return 0, false
		}
		source = d.Source
		counts[d.Partition] += d.Sent
	}
	if len(counts) < 2 {
		return 0, false
	}
	var sum float64
	for _, n := range counts {
		sum += float64(n)
	}
	mean := sum / float64(len(counts))
	var squares float64
	for _, n := range counts {
		squares += math.Pow(float64(n)-mean, 2)
	}
	return math.Sqrt(squares/float64(len(counts))) / mean * 100, true
}

// CloneMessagingSummaries snapshots bounded analytical action populations.
func CloneMessagingSummaries(in map[string]*MessagingSummary) map[string]*MessagingSummary {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]*MessagingSummary, min(len(keys), 128))
	incomplete := false
	for _, key := range keys {
		if len(out) >= 127 || key == "__other__" || messagingLabel(key) != key || key == "" {
			incomplete = true
			continue
		}
		value := in[key]
		if value == nil {
			continue
		}
		if !value.Valid() {
			out[key] = &MessagingSummary{Incomplete: true}
			continue
		}
		out[key] = value.Clone()
	}
	if incomplete {
		out["__other__"] = &MessagingSummary{Incomplete: true}
	}
	return out
}
