package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"sort"
)

const (
	MessagingEvidenceVersion          = 2
	MaxMessagingDurationMetrics       = 8
	MaxMessagingCounterMetrics        = 16
	maxMessagingEvidenceCount   int64 = 1e12
)

// MessagingDurationObservation is one eligible client operation. A nil value
// preserves why the operation has no measurement; observed zero is a value.
// Producer acknowledgement and settlement are separate client-observed phases,
// never isolated broker processing time. Cross-clock age/end-to-end values must
// be qualified by the collecting adapter before entering this contract.
type MessagingDurationObservation struct {
	Metric            string   `json:"metric"`
	ValueMs           *float64 `json:"value_ms,omitempty"`
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}

// MessagingCounterObservation describes a delta over an explicit eligible
// population. Observed is the subset actually inspected. Local duplicate/order
// observations are bounded windows and lower bounds, not broker-wide totals.
// Reliability counts describe affected eligible items; repeated attempts use a
// separate request_attempt population instead of inflating a message rate.
type MessagingCounterObservation struct {
	Metric        string `json:"metric"`
	Population    string `json:"population"`
	Scope         string `json:"scope"`
	Eligible      int64  `json:"eligible"`
	Observed      int64  `json:"observed"`
	Count         int64  `json:"count"`
	WindowEntries int64  `json:"window_entries,omitempty"`
	Sampled       bool   `json:"sampled,omitempty"`
	LowerBound    bool   `json:"lower_bound,omitempty"`
}

// UnmarshalJSON rejects explicitly unavailable counts instead of turning them
// into observed zero. This also applies to the v2 summary alias below.
func (c *MessagingCounterObservation) UnmarshalJSON(data []byte) error {
	var wire struct {
		Metric        string                 `json:"metric"`
		Population    string                 `json:"population"`
		Scope         string                 `json:"scope"`
		Eligible      messagingEvidenceCount `json:"eligible"`
		Observed      messagingEvidenceCount `json:"observed"`
		Count         messagingEvidenceCount `json:"count"`
		WindowEntries messagingEvidenceCount `json:"window_entries,omitempty"`
		Sampled       bool                   `json:"sampled,omitempty"`
		LowerBound    bool                   `json:"lower_bound,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if !wire.Eligible.present || !wire.Observed.present || !wire.Count.present {
		return errors.New("messaging counters require eligible, observed and count")
	}
	*c = MessagingCounterObservation{
		Metric: wire.Metric, Population: wire.Population, Scope: wire.Scope,
		Eligible: wire.Eligible.value, Observed: wire.Observed.value, Count: wire.Count.value,
		WindowEntries: wire.WindowEntries.value, Sampled: wire.Sampled, LowerBound: wire.LowerBound,
	}
	return nil
}

type MessagingDiagnostics struct {
	Version   uint32                         `json:"version"`
	Durations []MessagingDurationObservation `json:"durations,omitempty"`
	Counters  []MessagingCounterObservation  `json:"counters,omitempty"`
}

// MessagingDurationSummary retains a fixed-histogram algorithm and its own
// eligible/observed population. Percentiles are approximate histogram upper
// bounds. Unavailable reasons sum to Eligible minus Distribution.Count.
type MessagingDurationSummary struct {
	Metric       string                 `json:"metric"`
	Population   string                 `json:"population"`
	Eligible     int64                  `json:"eligible"`
	Distribution DiagnosticDistribution `json:"distribution_ms"`
	Unavailable  map[string]int64       `json:"unavailable,omitempty"`
}

// UnmarshalJSON keeps v2 duration histograms strict without changing the
// legacy DiagnosticDistribution decoder used by other protocol evidence.
func (s *MessagingDurationSummary) UnmarshalJSON(data []byte) error {
	var wire struct {
		Metric       string                        `json:"metric"`
		Population   string                        `json:"population"`
		Eligible     messagingEvidenceCount        `json:"eligible"`
		Distribution messagingDurationDistribution `json:"distribution_ms"`
		Unavailable  map[string]int64              `json:"unavailable,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if !wire.Eligible.present {
		return errors.New("messaging duration requires eligible population")
	}
	*s = MessagingDurationSummary{
		Metric: wire.Metric, Population: wire.Population, Eligible: wire.Eligible.value,
		Distribution: DiagnosticDistribution(wire.Distribution), Unavailable: wire.Unavailable,
	}
	return nil
}

type messagingDurationDistribution DiagnosticDistribution

func (d *messagingDurationDistribution) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("messaging duration distribution must be an object")
	}
	// Wholly unavailable distributions may omit zero fields and buckets.
	// Valid still reconciles every positive observed Count with its histogram.
	var wire struct {
		Count   messagingEvidenceCount   `json:"count"`
		Sum     messagingDurationValue   `json:"sum"`
		Min     messagingDurationValue   `json:"min"`
		Max     messagingDurationValue   `json:"max"`
		Buckets messagingDurationBuckets `json:"buckets"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Count.value > 0 && (!wire.Sum.present || !wire.Min.present || !wire.Max.present || !wire.Buckets.present) {
		return errors.New("observed messaging duration requires statistics and 144 buckets")
	}
	*d = messagingDurationDistribution(DiagnosticDistribution{
		Count: wire.Count.value, Sum: wire.Sum.value, Min: wire.Min.value, Max: wire.Max.value, Buckets: wire.Buckets.values,
	})
	return nil
}

type messagingEvidenceCount struct {
	value   int64
	present bool
}

func (v *messagingEvidenceCount) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("messaging evidence count must be an integer")
	}
	var value int64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*v = messagingEvidenceCount{value: value, present: true}
	return nil
}

type messagingDurationValue struct {
	value   float64
	present bool
}

func (v *messagingDurationValue) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("messaging duration value must be a number")
	}
	var value float64
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*v = messagingDurationValue{value: value, present: true}
	return nil
}

type messagingDurationBuckets struct {
	values  [144]int64
	present bool
}

func (b *messagingDurationBuckets) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return errors.New("messaging duration buckets must be an array of 144 integers")
	}
	var decoded [144]int64
	index := 0
	for decoder.More() {
		// Reject before reading another value; no unbounded bucket slice is built.
		if index == len(decoded) {
			return errors.New("messaging duration buckets exceed 144 entries")
		}
		token, err = decoder.Token()
		number, ok := token.(json.Number)
		if err != nil || !ok {
			return errors.New("messaging duration buckets must contain integers")
		}
		value, err := number.Int64()
		if err != nil {
			return errors.New("messaging duration bucket is outside the integer range")
		}
		decoded[index] = value
		index++
	}
	if index != len(decoded) {
		return errors.New("messaging duration buckets require exactly 144 entries")
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim(']') {
		return errors.New("messaging duration buckets have an invalid array boundary")
	}
	*b = messagingDurationBuckets{values: decoded, present: true}
	return nil
}

type MessagingCounterSummary = MessagingCounterObservation

// MessagingEvidence is optional v2 evidence. Operations counts publications
// carrying this contract, independently of the legacy dimension Operations.
// Merging a legacy peer does not create observed v2 measurements or counters.
type MessagingEvidence struct {
	Operations int64                      `json:"operations"`
	Durations  []MessagingDurationSummary `json:"durations,omitempty"`
	Counters   []MessagingCounterSummary  `json:"counters,omitempty"`
	Incomplete bool                       `json:"incomplete,omitempty"`
}

func (e *MessagingEvidence) UnmarshalJSON(data []byte) error {
	var wire struct {
		Operations messagingEvidenceCount     `json:"operations"`
		Durations  []MessagingDurationSummary `json:"durations,omitempty"`
		Counters   []MessagingCounterSummary  `json:"counters,omitempty"`
		Incomplete bool                       `json:"incomplete,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if !wire.Operations.present {
		return errors.New("messaging evidence requires operations population")
	}
	*e = MessagingEvidence{
		Operations: wire.Operations.value, Durations: wire.Durations, Counters: wire.Counters, Incomplete: wire.Incomplete,
	}
	return nil
}

func messagingDurationPopulation(metric string) string {
	switch metric {
	case "producer_ack":
		return "producer_operation"
	case "settlement":
		return "settlement_operation"
	case "checkpoint":
		return "checkpoint_operation"
	case "local_admission", "action":
		return "action_operation"
	case "enqueue_age", "end_to_end":
		return "receive_operation"
	default:
		return ""
	}
}

func validMessagingUnavailableReason(reason string) bool {
	switch reason {
	case "unsupported", "not_observed", "clock_unqualified", "failed", "censored", "stale", "missing_peer":
		return true
	default:
		return false
	}
}

func validMessagingDuration(value float64) bool {
	return value >= 0 && value <= 24*60*60*1000 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (d *MessagingDiagnostics) Valid() bool {
	if d == nil || d.Version != MessagingEvidenceVersion || len(d.Durations) > MaxMessagingDurationMetrics || len(d.Counters) > MaxMessagingCounterMetrics || len(d.Durations)+len(d.Counters) == 0 {
		return false
	}
	seenDurations := map[string]bool{}
	for _, observation := range d.Durations {
		if messagingDurationPopulation(observation.Metric) == "" || seenDurations[observation.Metric] {
			return false
		}
		seenDurations[observation.Metric] = true
		if observation.ValueMs == nil {
			if !validMessagingUnavailableReason(observation.UnavailableReason) {
				return false
			}
		} else if observation.UnavailableReason != "" || !validMessagingDuration(*observation.ValueMs) {
			return false
		}
	}
	seenCounters := map[messagingCounterIdentity]bool{}
	for _, observation := range d.Counters {
		key := messagingCounterKey(observation)
		if !observation.valid() || seenCounters[key] {
			return false
		}
		seenCounters[key] = true
	}
	return true
}

func (d *MessagingDiagnostics) Clone() *MessagingDiagnostics {
	if d == nil {
		return nil
	}
	if !d.Valid() {
		return &MessagingDiagnostics{}
	}
	copy := *d
	copy.Durations = append([]MessagingDurationObservation(nil), d.Durations...)
	for i := range copy.Durations {
		if d.Durations[i].ValueMs != nil {
			value := *d.Durations[i].ValueMs
			copy.Durations[i].ValueMs = &value
		}
	}
	copy.Counters = append([]MessagingCounterObservation(nil), d.Counters...)
	return &copy
}

type messagingCounterIdentity struct {
	Metric, Population, Scope string
	WindowEntries             int64
}

func messagingCounterKey(c MessagingCounterObservation) messagingCounterIdentity {
	return messagingCounterIdentity{c.Metric, c.Population, c.Scope, c.WindowEntries}
}

func (c MessagingCounterObservation) valid() bool {
	for _, count := range []int64{c.Eligible, c.Observed, c.Count} {
		if count < 0 || count > maxMessagingEvidenceCount {
			return false
		}
	}
	if c.Observed > c.Eligible || c.Count > c.Observed {
		return false
	}
	switch c.Metric {
	case "duplicate", "out_of_order":
		return c.Population == "received_message" && c.Scope == "local_sequence_window" && c.WindowEntries > 0 && c.WindowEntries <= 4096 && c.LowerBound
	case "redelivery", "dead_letter":
		return c.Population == "received_message" && c.Scope == "delivery_header" && c.WindowEntries == 0
	case "send_failure":
		return (c.Population == "send_attempt" && c.Scope == "client_action" || c.Population == "transaction_flush_attempt" && c.Scope == "client_transaction") && c.WindowEntries == 0
	case "throttle", "retry", "lock_loss":
		return c.Population == "request_attempt" && c.Scope == "client_action" && c.WindowEntries == 0
	default:
		return false
	}
}

func messagingDistributionIndex(value float64) int {
	if value <= .001 {
		return 0
	}
	return min(143, int(math.Ceil(math.Log(value/.001)/math.Log(1.2))))
}

func validMessagingDistribution(d DiagnosticDistribution) bool {
	if d.Count < 0 || d.Count > maxMessagingEvidenceCount || !validMessagingDuration(d.Min) || !validMessagingDuration(d.Max) || d.Min > d.Max || math.IsNaN(d.Sum) || math.IsInf(d.Sum, 0) || d.Sum < 0 {
		return false
	}
	if d.Count == 0 && (d.Sum != 0 || d.Min != 0 || d.Max != 0) {
		return false
	}
	var count int64
	var lower, upper float64
	for i, n := range d.Buckets {
		if n < 0 || n > maxMessagingEvidenceCount-count {
			return false
		}
		count += n
		if n == 0 {
			continue
		}
		if i < messagingDistributionIndex(d.Min) || i > messagingDistributionIndex(d.Max) {
			return false
		}
		bucketLower := 0.0
		if i > 0 {
			bucketLower = .001 * math.Pow(1.2, float64(i-1))
		}
		bucketUpper := .001 * math.Pow(1.2, float64(i))
		lower += float64(n) * max(d.Min, bucketLower)
		upper += float64(n) * min(d.Max, bucketUpper)
	}
	if count != d.Count {
		return false
	}
	if count == 0 {
		return true
	}
	if d.Buckets[messagingDistributionIndex(d.Min)] == 0 || d.Buckets[messagingDistributionIndex(d.Max)] == 0 {
		return false
	}
	tolerance := 1e-10 * max(1, float64(count)*d.Max)
	return d.Sum+tolerance >= lower && d.Sum <= upper+tolerance
}

func (s MessagingDurationSummary) valid(operations int64) bool {
	if s.Population != messagingDurationPopulation(s.Metric) || s.Population == "" || s.Eligible < 0 || s.Eligible > operations || !validMessagingDistribution(s.Distribution) || s.Distribution.Count > s.Eligible || len(s.Unavailable) > 7 {
		return false
	}
	missing := int64(0)
	for reason, count := range s.Unavailable {
		if !validMessagingUnavailableReason(reason) || count <= 0 || count > s.Eligible-missing {
			return false
		}
		missing += count
	}
	return missing == s.Eligible-s.Distribution.Count
}

func (e *MessagingEvidence) Valid() bool {
	if e == nil || e.Operations < 0 || e.Operations > maxMessagingEvidenceCount || len(e.Durations) > MaxMessagingDurationMetrics || len(e.Counters) > MaxMessagingCounterMetrics {
		return false
	}
	seenDurations := map[string]bool{}
	for _, duration := range e.Durations {
		if !duration.valid(e.Operations) || seenDurations[duration.Metric] {
			return false
		}
		seenDurations[duration.Metric] = true
	}
	seenCounters := map[messagingCounterIdentity]bool{}
	for _, counter := range e.Counters {
		key := messagingCounterKey(counter)
		if !counter.valid() || seenCounters[key] {
			return false
		}
		seenCounters[key] = true
	}
	return true
}

func (e *MessagingEvidence) Clone() *MessagingEvidence {
	if e == nil {
		return nil
	}
	if !e.Valid() {
		return &MessagingEvidence{Operations: -1, Incomplete: true}
	}
	copy := *e
	copy.Durations = append([]MessagingDurationSummary(nil), e.Durations...)
	for i := range copy.Durations {
		copy.Durations[i] = cloneMessagingDuration(e.Durations[i])
	}
	copy.Counters = append([]MessagingCounterSummary(nil), e.Counters...)
	return &copy
}

func messagingEvidenceFromDiagnostic(d *MessagingDiagnostics) *MessagingEvidence {
	if !d.Valid() {
		return nil
	}
	e := &MessagingEvidence{Operations: 1}
	for _, observation := range d.Durations {
		s := MessagingDurationSummary{Metric: observation.Metric, Population: messagingDurationPopulation(observation.Metric), Eligible: 1}
		if observation.ValueMs != nil {
			s.Distribution.Add(*observation.ValueMs)
		} else {
			s.Unavailable = map[string]int64{observation.UnavailableReason: 1}
		}
		e.Durations = append(e.Durations, s)
	}
	e.Counters = append([]MessagingCounterSummary(nil), d.Counters...)
	sortMessagingEvidence(e)
	return e
}

func mergeMessagingEvidence(dst **MessagingEvidence, src *MessagingEvidence) bool {
	if src == nil {
		if *dst != nil {
			out := (*dst).Clone()
			out.Incomplete = true
			*dst = out
		}
		return true
	}
	if !src.Valid() {
		return false
	}
	if *dst == nil {
		*dst = src.Clone()
		return true
	}
	out := (*dst).Clone()
	if src.Operations > maxMessagingEvidenceCount-out.Operations {
		return false
	}
	out.Operations += src.Operations
	out.Incomplete = out.Incomplete || src.Incomplete
	for _, added := range src.Durations {
		index := -1
		for i := range out.Durations {
			if out.Durations[i].Metric == added.Metric {
				index = i
				break
			}
		}
		if index < 0 {
			out.Durations = append(out.Durations, cloneMessagingDuration(added))
			continue
		}
		current := &out.Durations[index]
		current.Eligible += added.Eligible
		if current.Distribution.Count == 0 {
			current.Distribution.Min = added.Distribution.Min
		} else if added.Distribution.Count > 0 {
			current.Distribution.Min = min(current.Distribution.Min, added.Distribution.Min)
		}
		current.Distribution.Count += added.Distribution.Count
		current.Distribution.Sum += added.Distribution.Sum
		current.Distribution.Max = max(current.Distribution.Max, added.Distribution.Max)
		for i, count := range added.Distribution.Buckets {
			current.Distribution.Buckets[i] += count
		}
		if len(added.Unavailable) > 0 && current.Unavailable == nil {
			current.Unavailable = map[string]int64{}
		}
		for reason, count := range added.Unavailable {
			current.Unavailable[reason] += count
		}
	}
	for _, added := range src.Counters {
		index := -1
		for i := range out.Counters {
			if messagingCounterKey(out.Counters[i]) == messagingCounterKey(added) {
				index = i
				break
			}
		}
		if index < 0 {
			out.Counters = append(out.Counters, added)
			continue
		}
		current := &out.Counters[index]
		current.Eligible += added.Eligible
		current.Observed += added.Observed
		current.Count += added.Count
		current.Sampled = current.Sampled || added.Sampled
		current.LowerBound = current.LowerBound || added.LowerBound
	}
	if !out.Valid() {
		return false
	}
	sortMessagingEvidence(out)
	*dst = out
	return true
}

func sortMessagingEvidence(e *MessagingEvidence) {
	sort.Slice(e.Durations, func(i, j int) bool { return e.Durations[i].Metric < e.Durations[j].Metric })
	sort.Slice(e.Counters, func(i, j int) bool {
		a, b := messagingCounterKey(e.Counters[i]), messagingCounterKey(e.Counters[j])
		if a.Metric != b.Metric {
			return a.Metric < b.Metric
		}
		if a.Population != b.Population {
			return a.Population < b.Population
		}
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		return a.WindowEntries < b.WindowEntries
	})
}

// EvidenceVersion returns the highest captured contract without inventing v2
// measurements for legacy publications. Incomplete still applies to all queries.
func (s *MessagingSummary) EvidenceVersion() uint32 {
	if s == nil {
		return 0
	}
	for _, dimension := range s.Dimensions {
		if dimension.Evidence != nil {
			return MessagingEvidenceVersion
		}
	}
	return 1
}

func cloneMessagingDuration(s MessagingDurationSummary) MessagingDurationSummary {
	if s.Unavailable != nil {
		reasons := make(map[string]int64, len(s.Unavailable))
		for reason, count := range s.Unavailable {
			reasons[reason] = count
		}
		s.Unavailable = reasons
	}
	return s
}

// AnnotateMessagingSendOutcome records client-observed provider-attempt coverage.
// Local preflight/buffer admission is separate from producer acknowledgement;
// transaction flush/commit attempts have a distinct counter population.
func AnnotateMessagingSendOutcome(t *ServerTelemetry, attempted, succeeded, transaction bool) {
	if t == nil {
		return
	}
	if t.Messaging == nil {
		t.Messaging = &MessagingDiagnostics{Version: MessagingEvidenceVersion}
	}
	d := t.Messaging
	if !attempted {
		durations := d.Durations[:0]
		for _, duration := range d.Durations {
			if duration.Metric != "producer_ack" {
				durations = append(durations, duration)
			}
		}
		d.Durations = durations
		reason := "failed"
		if succeeded {
			reason = "not_observed"
		}
		d.Durations = append(d.Durations, MessagingDurationObservation{Metric: "local_admission", UnavailableReason: reason})
		return
	}
	if !transaction {
		present := false
		for _, duration := range d.Durations {
			present = present || duration.Metric == "producer_ack"
		}
		if !present {
			reason := "not_observed"
			if !succeeded {
				reason = "failed"
			}
			d.Durations = append(d.Durations, MessagingDurationObservation{Metric: "producer_ack", UnavailableReason: reason})
		}
	}
	counter := MessagingCounterObservation{Metric: "send_failure", Population: "send_attempt", Scope: "client_action", Eligible: 1, Observed: 1}
	if transaction {
		counter.Population, counter.Scope = "transaction_flush_attempt", "client_transaction"
	}
	if !succeeded {
		counter.Count = 1
	}
	d.Counters = append(d.Counters, counter)
}
