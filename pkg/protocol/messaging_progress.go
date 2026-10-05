package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MessagingProgressVersion  uint32 = 1
	MaxMessagingProgressRows         = 64
	MaxMessagingProgressBytes        = 128 << 10
)

// MessagingProgressProvider reads bounded local lifecycle state. It must never
// perform target I/O or synthesize a completed action to publish progress.
type MessagingProgressProvider interface {
	MessagingProgressSnapshot() *MessagingProgressSnapshot
}

type MessagingProgressSnapshot struct {
	Version    uint32                 `json:"version"`
	Lifecycle  string                 `json:"lifecycle,omitempty"`
	CapturedAt time.Time              `json:"captured_at"`
	Rows       []MessagingProgressRow `json:"rows"`
	Incomplete bool                   `json:"incomplete,omitempty"`
	// DroppedRows is cumulative locally and a lower bound across merged sources.
	DroppedRows       int64  `json:"dropped_rows,omitempty"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

// Revisions and positions are canonical decimal strings to retain all int64
// bits through JSON clients. Origin and Group are assigned by trusted transport
// and engine code, respectively, rather than by a protocol or script.
type MessagingProgressRow struct {
	Origin                 string                      `json:"origin,omitempty"`
	Group                  string                      `json:"group,omitempty"`
	Protocol               string                      `json:"protocol"`
	Mode                   string                      `json:"mode"`
	SourceID               string                      `json:"source_id"`
	Source                 string                      `json:"source"`
	Subscription           string                      `json:"subscription"`
	SessionID              string                      `json:"session_id"`
	Partition              string                      `json:"partition"`
	Generation             string                      `json:"generation"`
	Revision               string                      `json:"revision"`
	CapturedAt             time.Time                   `json:"captured_at"`
	CaptureRevision        string                      `json:"capture_revision"`
	UpdatedAt              time.Time                   `json:"updated_at"`
	ExpectedPartitions     int64                       `json:"expected_partitions"`
	InventoryComplete      bool                        `json:"inventory_complete"`
	Ownership              string                      `json:"ownership"`
	OwnershipVerifiedAt    time.Time                   `json:"ownership_verified_at"`
	OwnershipVerifiedAgeMs *int64                      `json:"ownership_verified_age_ms,omitempty"`
	Acquired               int64                       `json:"acquired"`
	Lost                   int64                       `json:"lost"`
	Delivered              int64                       `json:"delivered"`
	Rejected               int64                       `json:"rejected"`
	Redelivered            int64                       `json:"redelivered"`
	DeliveredPosition      MessagingProgressPosition   `json:"delivered_position"`
	CheckpointPosition     MessagingProgressPosition   `json:"checkpoint_position"`
	TailPosition           MessagingProgressPosition   `json:"tail_position"`
	Checkpoint             MessagingCheckpointProgress `json:"checkpoint"`
	Incomplete             bool                        `json:"incomplete,omitempty"`
}

type MessagingProgressPosition struct {
	Sequence         string    `json:"sequence,omitempty"`
	ProviderOffset   string    `json:"provider_offset,omitempty"`
	State            string    `json:"state"`
	ObservedAt       time.Time `json:"observed_at"`
	ReceivedAt       time.Time `json:"received_at"`
	ObservationAgeMs *int64    `json:"observation_age_ms,omitempty"`
}

// Checkpoint statistics are cumulative for one partition ownership generation.
// Tick replay replaces the row; it never adds these histograms to prior ticks.
type MessagingCheckpointProgress struct {
	Attempts             int64                    `json:"attempts"`
	Committed            int64                    `json:"committed"`
	Failed               int64                    `json:"failed"`
	Fenced               int64                    `json:"fenced"`
	CancelledBeforeWrite int64                    `json:"cancelled_before_write"`
	Unconfirmed          int64                    `json:"unconfirmed"`
	RetryAttempts        int64                    `json:"retry_attempts"`
	InFlight             int64                    `json:"in_flight"`
	Duration             MessagingDurationSummary `json:"duration"`
	LastOutcome          string                   `json:"last_outcome,omitempty"`
	LastCompletedAt      time.Time                `json:"last_completed_at"`
}

func progressDecimal(s string, positive bool) (int64, bool) {
	n, e := strconv.ParseInt(s, 10, 64)
	return n, e == nil && n >= 0 && (!positive || n > 0) && strconv.FormatInt(n, 10) == s
}
func progressText(s string, n int) bool {
	return len(s) <= n && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}
func progressAge(a *int64) bool { return a == nil || *a >= 0 }
func (p MessagingProgressPosition) Valid() bool {
	if !progressText(p.ProviderOffset, 256) || !progressAge(p.ObservationAgeMs) {
		return false
	}
	switch p.State {
	case "observed", "unconfirmed":
		if p.State == "unconfirmed" && p.Sequence == "" {
			return p.ProviderOffset == "" && p.ObservationAgeMs == nil
		}
		_, ok := progressDecimal(p.Sequence, false)
		return ok && !p.ObservedAt.IsZero() && !p.ReceivedAt.IsZero()
	case "missing", "disabled", "empty", "fenced", "pending":
		return p.Sequence == "" && p.ProviderOffset == "" && p.ObservationAgeMs == nil
	default:
		return false
	}
}
func (c MessagingCheckpointProgress) valid() bool {
	for _, v := range []int64{c.Attempts, c.Committed, c.Failed, c.Fenced, c.CancelledBeforeWrite, c.Unconfirmed, c.RetryAttempts, c.InFlight} {
		if v < 0 || v > maxMessagingEvidenceCount {
			return false
		}
	}
	completed := c.Committed + c.Failed + c.Fenced + c.CancelledBeforeWrite + c.Unconfirmed
	if completed+c.InFlight != c.Attempts || c.RetryAttempts > c.Attempts || c.Duration.Metric != "checkpoint" || !c.Duration.valid(completed) || c.Duration.Eligible != completed {
		return false
	}
	switch c.LastOutcome {
	case "":
		return completed == 0
	case "committed", "failed", "fenced", "cancelled_before_write", "unconfirmed":
		return completed > 0 && !c.LastCompletedAt.IsZero()
	default:
		return false
	}
}
func (r MessagingProgressRow) Valid() bool {
	for _, v := range []string{r.Origin, r.Group, r.Protocol, r.Mode, r.SourceID, r.Source, r.Subscription, r.SessionID, r.Partition, r.Generation} {
		if !progressText(v, 256) {
			return false
		}
	}
	if r.Protocol != "eventhubs" || r.Mode != "processor" || r.SourceID == "" || r.Source == "" || r.Subscription == "" || r.SessionID == "" || r.Partition == "" || r.Generation == "" || r.UpdatedAt.IsZero() || r.CapturedAt.IsZero() || r.ExpectedPartitions < 1 || r.ExpectedPartitions > 32 || !progressAge(r.OwnershipVerifiedAgeMs) {
		return false
	}
	if _, ok := progressDecimal(r.Revision, true); !ok {
		return false
	}
	if _, ok := progressDecimal(r.CaptureRevision, true); !ok {
		return false
	}
	switch r.Ownership {
	case "owned":
		if r.Acquired < 1 || r.OwnershipVerifiedAt.IsZero() {
			return false
		}
	case "lost", "unverified", "released":
	default:
		return false
	}
	for _, v := range []int64{r.Acquired, r.Lost, r.Delivered, r.Rejected, r.Redelivered} {
		if v < 0 || v > maxMessagingEvidenceCount {
			return false
		}
	}
	return r.Rejected <= r.Delivered && r.Redelivered <= r.Delivered && r.DeliveredPosition.Valid() && r.CheckpointPosition.Valid() && r.TailPosition.Valid() && r.Checkpoint.valid()
}
func validProgressReason(s string) bool {
	switch s {
	case "", "legacy_peer", "unsupported_version", "malformed", "oversized", "source_missing", "clock_unqualified":
		return true
	default:
		return false
	}
}
func (s *MessagingProgressSnapshot) Valid() bool {
	if s == nil || s.Version != MessagingProgressVersion || s.CapturedAt.IsZero() || len(s.Rows) > MaxMessagingProgressRows || s.DroppedRows < 0 || s.DroppedRows > maxMessagingEvidenceCount || !validProgressReason(s.UnavailableReason) || (s.DroppedRows > 0 || s.UnavailableReason != "") && !s.Incomplete {
		return false
	}
	switch s.Lifecycle {
	case "", "pending", "running", "draining", "released":
	case "quarantined":
		if !s.Incomplete {
			return false
		}
	default:
		return false
	}
	seen := map[string]bool{}
	for _, r := range s.Rows {
		key := progressIdentity(r)
		if !r.Valid() || seen[key] {
			return false
		}
		seen[key] = true
	}
	b, e := json.Marshal(s)
	return e == nil && len(b) <= MaxMessagingProgressBytes
}
func progressIdentity(r MessagingProgressRow) string {
	return strings.Join([]string{r.Origin, r.Group, r.Protocol, r.SourceID, r.Subscription, r.SessionID, r.Partition, r.Generation}, "\x00")
}
func cloneProgressAge(v *int64) *int64 {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}
func cloneProgressRow(r MessagingProgressRow) MessagingProgressRow {
	r.OwnershipVerifiedAgeMs = cloneProgressAge(r.OwnershipVerifiedAgeMs)
	r.DeliveredPosition.ObservationAgeMs = cloneProgressAge(r.DeliveredPosition.ObservationAgeMs)
	r.CheckpointPosition.ObservationAgeMs = cloneProgressAge(r.CheckpointPosition.ObservationAgeMs)
	r.TailPosition.ObservationAgeMs = cloneProgressAge(r.TailPosition.ObservationAgeMs)
	r.Checkpoint.Duration = cloneMessagingDuration(r.Checkpoint.Duration)
	return r
}
func (s *MessagingProgressSnapshot) Clone() *MessagingProgressSnapshot {
	if s == nil {
		return nil
	}
	if !s.Valid() {
		return NewUnavailableMessagingProgress("malformed")
	}
	c := *s
	c.Rows = make([]MessagingProgressRow, len(s.Rows))
	for i, r := range s.Rows {
		c.Rows[i] = cloneProgressRow(r)
	}
	return &c
}
func NewUnavailableMessagingProgress(reason string) *MessagingProgressSnapshot {
	if !validProgressReason(reason) || reason == "" {
		reason = "malformed"
	}
	return &MessagingProgressSnapshot{Version: MessagingProgressVersion, CapturedAt: time.Now().UTC(), Rows: []MessagingProgressRow{}, Incomplete: true, UnavailableReason: reason}
}
func ReadMessagingProgress(p Protocol) (snapshot *MessagingProgressSnapshot, supported bool, err error) {
	provider, ok := p.(MessagingProgressProvider)
	if !ok {
		return nil, false, nil
	}
	defer func() {
		if recover() != nil {
			snapshot = NewUnavailableMessagingProgress("malformed")
			supported = true
			err = errors.New("messaging progress provider failed")
		}
	}()
	s := provider.MessagingProgressSnapshot()
	if s == nil {
		return nil, true, nil
	}
	if !s.Valid() {
		return NewUnavailableMessagingProgress("malformed"), true, errors.New("invalid messaging progress snapshot")
	}
	return s.Clone(), true, nil
}

// QualifiedLag is an as-of-source-capture association, never current-live
// freshness inferred from remote wall clocks. Missing cursors stay unavailable.
func (r MessagingProgressRow) QualifiedLag(cursor string, maxAgeMs int64) (string, bool) {
	if !r.Valid() || r.Incomplete || !r.InventoryComplete || r.Ownership != "owned" || maxAgeMs < 0 || r.OwnershipVerifiedAgeMs == nil || *r.OwnershipVerifiedAgeMs > maxAgeMs || r.TailPosition.State != "observed" || r.TailPosition.ObservationAgeMs == nil || *r.TailPosition.ObservationAgeMs > maxAgeMs {
		return "", false
	}
	p := r.DeliveredPosition
	if cursor == "checkpoint" {
		p = r.CheckpointPosition
		if r.Checkpoint.InFlight > 0 {
			return "", false
		}
	} else if cursor != "delivered" {
		return "", false
	}
	if p.State != "observed" || p.ObservationAgeMs == nil || *p.ObservationAgeMs > maxAgeMs {
		return "", false
	}
	tail, _ := progressDecimal(r.TailPosition.Sequence, false)
	pos, _ := progressDecimal(p.Sequence, false)
	if tail < pos {
		return "", false
	}
	return strconv.FormatInt(tail-pos, 10), true
}
func progressCore(r MessagingProgressRow) MessagingProgressRow {
	r.CaptureRevision = ""
	r.CapturedAt = time.Time{}
	r.OwnershipVerifiedAgeMs = nil
	r.DeliveredPosition.ObservationAgeMs = nil
	r.CheckpointPosition.ObservationAgeMs = nil
	r.TailPosition.ObservationAgeMs = nil
	return r
}
func progressNondecreasing(a, b MessagingProgressRow) bool {
	for _, v := range [][2]int64{{a.Acquired, b.Acquired}, {a.Lost, b.Lost}, {a.Delivered, b.Delivered}, {a.Rejected, b.Rejected}, {a.Redelivered, b.Redelivered}, {a.Checkpoint.Attempts, b.Checkpoint.Attempts}, {a.Checkpoint.Committed, b.Checkpoint.Committed}, {a.Checkpoint.Failed, b.Checkpoint.Failed}, {a.Checkpoint.Fenced, b.Checkpoint.Fenced}, {a.Checkpoint.Unconfirmed, b.Checkpoint.Unconfirmed}, {a.Checkpoint.CancelledBeforeWrite, b.Checkpoint.CancelledBeforeWrite}, {a.Checkpoint.RetryAttempts, b.Checkpoint.RetryAttempts}} {
		if v[1] < v[0] {
			return false
		}
	}
	if b.Checkpoint.Duration.Eligible < a.Checkpoint.Duration.Eligible {
		return false
	}
	x, y := a.Checkpoint.Duration.Distribution, b.Checkpoint.Duration.Distribution
	if y.Count < x.Count || y.Sum < x.Sum || x.Count > 0 && (y.Min > x.Min || y.Max < x.Max) {
		return false
	}
	for reason, count := range a.Checkpoint.Duration.Unavailable {
		if b.Checkpoint.Duration.Unavailable[reason] < count {
			return false
		}
	}

	for i, n := range a.Checkpoint.Duration.Distribution.Buckets {
		if b.Checkpoint.Duration.Distribution.Buckets[i] < n {
			return false
		}
	}
	return true
}

// MergeMessagingProgress replaces cumulative rows by identity/revision. Optional
// origin is authenticated transport identity and overwrites all producer values.
func MergeMessagingProgress(dst **MessagingProgressSnapshot, src *MessagingProgressSnapshot, origin ...string) {
	if dst == nil {
		return
	}
	if src == nil {
		if *dst != nil {
			(*dst).Incomplete = true
		}
		return
	}
	if !src.Valid() {
		src = NewUnavailableMessagingProgress("malformed")
	}
	hadDestination := *dst != nil
	out := (*dst).Clone()
	if out == nil {
		out = &MessagingProgressSnapshot{Version: MessagingProgressVersion, Rows: []MessagingProgressRow{}}
	}
	out.CapturedAt = src.CapturedAt
	// An aggregate can contain multiple reporters with independent lifetimes.
	if !hadDestination {
		out.Lifecycle = src.Lifecycle
	} else if out.Lifecycle != src.Lifecycle {
		out.Lifecycle = ""
	}
	out.Incomplete = out.Incomplete || src.Incomplete
	out.DroppedRows = max(out.DroppedRows, src.DroppedRows)
	if src.UnavailableReason != "" {
		out.UnavailableReason = src.UnavailableReason
	}
	index := map[string]int{}
	for i, r := range out.Rows {
		index[progressIdentity(r)] = i
	}
	for _, r := range src.Rows {
		r = cloneProgressRow(r)
		if len(origin) > 0 {
			r.Origin = origin[0]
		}
		if !r.Valid() {
			out.Incomplete = true
			continue
		}
		key := progressIdentity(r)
		i, exists := index[key]
		if !exists {
			index[key] = len(out.Rows)
			out.Rows = append(out.Rows, r)
			continue
		}
		old := out.Rows[i]
		rev, _ := progressDecimal(r.Revision, true)
		oldRev, _ := progressDecimal(old.Revision, true)
		capture, _ := progressDecimal(r.CaptureRevision, true)
		oldCapture, _ := progressDecimal(old.CaptureRevision, true)
		if rev < oldRev {
			continue
		}
		if rev == oldRev {
			if !reflect.DeepEqual(progressCore(old), progressCore(r)) {
				out.Incomplete = true
				out.Rows[i].Incomplete = true
				continue
			}
			if capture <= oldCapture {
				continue
			}
		} else if capture < oldCapture || !progressNondecreasing(old, r) {
			out.Incomplete = true
			out.Rows[i].Incomplete = true
			continue
		}
		r.Incomplete = r.Incomplete || old.Incomplete
		out.Rows[i] = r
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		a, b := progressRowPriority(out.Rows[i]), progressRowPriority(out.Rows[j])
		if a != b {
			return a < b
		}
		return progressIdentity(out.Rows[i]) < progressIdentity(out.Rows[j])
	})
	discarded := int64(0)
	for len(out.Rows) > MaxMessagingProgressRows {
		out.Rows = out.Rows[:len(out.Rows)-1]
		discarded++
		out.DroppedRows = max(out.DroppedRows, discarded)
		out.Incomplete = true
	}
	for {
		b, e := json.Marshal(out)
		if e == nil && len(b) <= MaxMessagingProgressBytes {
			break
		}
		if len(out.Rows) == 0 {
			out = NewUnavailableMessagingProgress("oversized")
			break
		}
		out.Rows = out.Rows[:len(out.Rows)-1]
		discarded++
		out.DroppedRows = max(out.DroppedRows, discarded)
		out.Incomplete = true
	}
	*dst = out
}

// Reject oversized JSON before allocating rows and require explicit numeric
// populations. Omitted/null counters must never become measured zero.
func (s *MessagingProgressSnapshot) UnmarshalJSON(data []byte) error {
	if len(data) > MaxMessagingProgressBytes {
		return errors.New("messaging progress exceeds byte limit")
	}
	type plain MessagingProgressSnapshot
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.Rows == nil {
		return errors.New("messaging progress rows must be an array")
	}
	if len(v.Rows) > MaxMessagingProgressRows {
		return errors.New("messaging progress exceeds row limit")
	}
	*s = MessagingProgressSnapshot(v)
	if !s.Valid() {
		return errors.New("invalid messaging progress")
	}
	return nil
}
func requireProgressNumbers(data []byte, names ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range names {
		for key := range fields {
			if key != name && strings.EqualFold(key, name) {
				return errors.New("messaging progress requires canonical numeric " + name)
			}
		}
		raw, present := fields[name]
		if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("messaging progress requires numeric " + name)
		}
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return err
		}
	}
	return nil
}
func (r *MessagingProgressRow) UnmarshalJSON(data []byte) error {
	if err := requireProgressNumbers(data, "expected_partitions", "acquired", "lost", "delivered", "rejected", "redelivered"); err != nil {
		return err
	}
	type plain MessagingProgressRow
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*r = MessagingProgressRow(v)
	if !r.Valid() {
		return errors.New("invalid messaging progress row")
	}
	return nil
}
func (c *MessagingCheckpointProgress) UnmarshalJSON(data []byte) error {
	if err := requireProgressNumbers(data, "attempts", "committed", "failed", "fenced", "cancelled_before_write", "unconfirmed", "retry_attempts", "in_flight"); err != nil {
		return err
	}
	type plain MessagingCheckpointProgress
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*c = MessagingCheckpointProgress(v)
	if !c.valid() {
		return errors.New("invalid checkpoint progress")
	}
	return nil
}

func progressRowPriority(r MessagingProgressRow) int {
	switch r.Ownership {
	case "owned":
		return 0
	case "unverified":
		return 1
	default:
		return 2
	}
}
