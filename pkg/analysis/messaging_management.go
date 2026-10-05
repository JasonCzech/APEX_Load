package analysis

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"time"
)

// Kafka boundaries are next offsets. They are not last delivered sequences or
// physical replica log ends. Epochs retain independent provider provenance.
type KafkaOffsets struct {
	CommittedNext        ManagementInteger `json:"committed_next"`
	HighWatermarkNext    ManagementInteger `json:"high_watermark_next"`
	LastStableNext       ManagementInteger `json:"last_stable_next"`
	LogEndNext           ManagementInteger `json:"log_end_next"`
	LeaderEpoch          ManagementInteger `json:"leader_epoch"`
	CommittedLeaderEpoch ManagementInteger `json:"committed_leader_epoch"`
	TopicID              string            `json:"topic_id,omitempty"`
}
type ServiceBusRuntime struct {
	Total              ManagementInteger `json:"total"`
	Active             ManagementInteger `json:"active"`
	Scheduled          ManagementInteger `json:"scheduled"`
	DeadLetter         ManagementInteger `json:"dead_letter"`
	Transfer           ManagementInteger `json:"transfer"`
	TransferDeadLetter ManagementInteger `json:"transfer_dead_letter"`
	SizeBytes          ManagementInteger `json:"size_bytes"`
	Deferred           ManagementInteger `json:"deferred"`
}
type SolaceSEMP struct {
	Depth            ManagementInteger `json:"depth"`
	DeadMessageQueue string            `json:"dead_message_queue,omitempty"`
}

func (k KafkaOffsets) Validate() error {
	if k.LogEndNext.State == ManagementStateObserved {
		return errors.New("physical Kafka replica log end is unsupported")
	}
	if !validManagementText(k.TopicID, 256, true) {
		return errors.New("invalid Kafka topic identity")
	}
	for _, v := range []ManagementInteger{k.CommittedNext, k.HighWatermarkNext, k.LastStableNext, k.LogEndNext, k.LeaderEpoch, k.CommittedLeaderEpoch} {
		if err := v.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func (s ServiceBusRuntime) Validate() error {
	for _, v := range []ManagementInteger{s.Total, s.Active, s.Scheduled, s.DeadLetter, s.Transfer, s.TransferDeadLetter, s.SizeBytes, s.Deferred} {
		if err := v.Validate(); err != nil {
			return err
		}
	}
	if s.Deferred.State == ManagementStateObserved {
		return errors.New("independent Service Bus deferred depth is unsupported")
	}
	return nil
}
func (s SolaceSEMP) Validate() error {
	if !validManagementText(s.DeadMessageQueue, 256, true) {
		return errors.New("invalid Solace DMQ identity")
	}
	return s.Depth.Validate()
}
func (k KafkaOffsets) Clone() KafkaOffsets {
	k.CommittedNext = k.CommittedNext.Clone()
	k.HighWatermarkNext = k.HighWatermarkNext.Clone()
	k.LastStableNext = k.LastStableNext.Clone()
	k.LogEndNext = k.LogEndNext.Clone()
	k.LeaderEpoch = k.LeaderEpoch.Clone()
	k.CommittedLeaderEpoch = k.CommittedLeaderEpoch.Clone()
	return k
}
func (s ServiceBusRuntime) Clone() ServiceBusRuntime {
	s.Total = s.Total.Clone()
	s.Active = s.Active.Clone()
	s.Scheduled = s.Scheduled.Clone()
	s.DeadLetter = s.DeadLetter.Clone()
	s.Transfer = s.Transfer.Clone()
	s.TransferDeadLetter = s.TransferDeadLetter.Clone()
	s.SizeBytes = s.SizeBytes.Clone()
	s.Deferred = s.Deferred.Clone()
	return s
}
func (s SolaceSEMP) Clone() SolaceSEMP { s.Depth = s.Depth.Clone(); return s }
func (k *KafkaOffsets) UnmarshalJSON(data []byte) error {
	type plain KafkaOffsets
	var x plain
	if err := decodeManagementJSON(data, &x); err != nil {
		return err
	}
	if err := requireManagementFields(data, "committed_next", "high_watermark_next", "last_stable_next", "log_end_next", "leader_epoch", "committed_leader_epoch"); err != nil {
		return err
	}
	if err := KafkaOffsets(x).Validate(); err != nil {
		return err
	}
	*k = KafkaOffsets(x)
	return nil
}
func (s *ServiceBusRuntime) UnmarshalJSON(data []byte) error {
	type plain ServiceBusRuntime
	var x plain
	if err := decodeManagementJSON(data, &x); err != nil {
		return err
	}
	if err := requireManagementFields(data, "total", "active", "scheduled", "dead_letter", "transfer", "transfer_dead_letter", "size_bytes", "deferred"); err != nil {
		return err
	}
	if err := ServiceBusRuntime(x).Validate(); err != nil {
		return err
	}
	*s = ServiceBusRuntime(x)
	return nil
}
func (s *SolaceSEMP) UnmarshalJSON(data []byte) error {
	type plain SolaceSEMP
	var x plain
	if err := decodeManagementJSON(data, &x); err != nil {
		return err
	}
	if err := requireManagementFields(data, "depth"); err != nil {
		return err
	}
	if err := SolaceSEMP(x).Validate(); err != nil {
		return err
	}
	*s = SolaceSEMP(x)
	return nil
}

// Lag compares only compatible next positions from the same row/poll. Missing
// inputs remain missing; truncation/inconsistent positions never become zero.
func (k KafkaOffsets) Lag(isolation string) (ManagementInteger, error) {
	if err := k.Validate(); err != nil {
		return ManagementInteger{}, err
	}
	var boundary ManagementInteger
	switch isolation {
	case "read_committed":
		boundary = k.LastStableNext
	case "read_uncommitted":
		boundary = k.HighWatermarkNext
	default:
		return ManagementInteger{}, errors.New("unsupported Kafka lag isolation")
	}
	if boundary.State != ManagementStateObserved || k.CommittedNext.State != ManagementStateObserved {
		return ManagementInteger{State: ManagementStateMissing, Reason: "not_observed"}, nil
	}
	end, _ := ParseManagementInteger(*boundary.Value)
	committed, _ := ParseManagementInteger(*k.CommittedNext.Value)
	if end < committed {
		return ManagementInteger{State: ManagementStateInvalid, Reason: "inconsistent_position"}, nil
	}
	return ObservedManagementInteger(end - committed)
}

type MessagingManagementRow struct {
	Entity            ManagementEntity   `json:"entity"`
	State             string             `json:"state"`
	Reason            string             `json:"reason,omitempty"`
	ObservedAt        *time.Time         `json:"observed_at,omitempty"`
	ObservationAgeMS  *int64             `json:"observation_age_ms,omitempty"`
	KafkaOffsets      *KafkaOffsets      `json:"kafka_offsets,omitempty"`
	ServiceBusRuntime *ServiceBusRuntime `json:"service_bus_runtime,omitempty"`
	SolaceSEMP        *SolaceSEMP        `json:"solace_semp,omitempty"`
}

func (r MessagingManagementRow) Validate() error {
	if err := r.Entity.Validate(); err != nil {
		return err
	}
	if !validManagementState(r.State) {
		return errors.New("invalid management row state")
	}
	if r.State == ManagementStateObserved {
		if r.Reason != "" || r.ObservedAt == nil || !validManagementTime(*r.ObservedAt) {
			return errors.New("observed management row requires source time")
		}
	} else if r.Reason == "" || !validManagementReason(r.Reason) || r.ObservedAt != nil || r.ObservationAgeMS != nil {
		return errors.New("unavailable row cannot freshen observed evidence")
	}
	if r.ObservationAgeMS != nil && *r.ObservationAgeMS < 0 {
		return errors.New("negative management observation age")
	}
	n := 0
	for _, b := range []bool{r.KafkaOffsets != nil, r.ServiceBusRuntime != nil, r.SolaceSEMP != nil} {
		if b {
			n++
		}
	}
	if n != 1 {
		return errors.New("management row requires exactly one evidence variant")
	}
	var values []ManagementInteger
	switch r.Entity.Kind() {
	case ManagementKindKafka:
		if r.KafkaOffsets == nil {
			return errors.New("management entity/evidence mismatch")
		}
		if err := r.KafkaOffsets.Validate(); err != nil {
			return err
		}
		k := r.KafkaOffsets
		values = []ManagementInteger{k.CommittedNext, k.HighWatermarkNext, k.LastStableNext, k.LogEndNext, k.LeaderEpoch, k.CommittedLeaderEpoch}
	case ManagementKindServiceBus:
		if r.ServiceBusRuntime == nil {
			return errors.New("management entity/evidence mismatch")
		}
		if err := r.ServiceBusRuntime.Validate(); err != nil {
			return err
		}
		s := r.ServiceBusRuntime
		values = []ManagementInteger{s.Total, s.Active, s.Scheduled, s.DeadLetter, s.Transfer, s.TransferDeadLetter, s.SizeBytes, s.Deferred}
	case ManagementKindSolace:
		if r.SolaceSEMP == nil {
			return errors.New("management entity/evidence mismatch")
		}
		if err := r.SolaceSEMP.Validate(); err != nil {
			return err
		}
		values = []ManagementInteger{r.SolaceSEMP.Depth}
	}
	if r.State != ManagementStateObserved {
		for _, v := range values {
			if v.State == ManagementStateObserved {
				return errors.New("unavailable row cannot contain observed measurements")
			}
		}
	}
	return nil
}
func (r MessagingManagementRow) Clone() MessagingManagementRow {
	r.Entity = r.Entity.Clone()
	if r.ObservedAt != nil {
		x := *r.ObservedAt
		r.ObservedAt = &x
	}
	if r.ObservationAgeMS != nil {
		x := *r.ObservationAgeMS
		r.ObservationAgeMS = &x
	}
	if r.KafkaOffsets != nil {
		x := r.KafkaOffsets.Clone()
		r.KafkaOffsets = &x
	}
	if r.ServiceBusRuntime != nil {
		x := r.ServiceBusRuntime.Clone()
		r.ServiceBusRuntime = &x
	}
	if r.SolaceSEMP != nil {
		x := r.SolaceSEMP.Clone()
		r.SolaceSEMP = &x
	}
	return r
}
func (r *MessagingManagementRow) UnmarshalJSON(data []byte) error {
	type plain MessagingManagementRow
	var x plain
	if err := decodeManagementJSON(data, &x); err != nil {
		return err
	}
	if err := requireManagementFields(data, "entity", "state"); err != nil {
		return err
	}
	if err := MessagingManagementRow(x).Validate(); err != nil {
		return err
	}
	*r = MessagingManagementRow(x)
	return nil
}
func (r MessagingManagementRow) QualifiedKafkaLag(isolation string, maxAgeMS int64) (ManagementInteger, error) {
	if err := r.Validate(); err != nil {
		return ManagementInteger{}, err
	}
	if maxAgeMS < 0 {
		return ManagementInteger{}, errors.New("negative freshness limit")
	}
	if r.KafkaOffsets == nil {
		return ManagementInteger{}, errors.New("Kafka lag requires Kafka evidence")
	}
	if r.State != ManagementStateObserved || r.ObservationAgeMS == nil {
		return ManagementInteger{State: ManagementStateMissing, Reason: "unqualified"}, nil
	}
	if *r.ObservationAgeMS > maxAgeMS {
		return ManagementInteger{State: ManagementStateStale, Reason: "stale"}, nil
	}
	return r.KafkaOffsets.Lag(isolation)
}

// Snapshot is one immutable source poll, independently of load action Count.
// Generation/poll identities are server admitted; receiver must also compare
// them to its authenticated grant and immutable source before publication.
type MessagingManagementSnapshot struct {
	Version             uint32                   `json:"version"`
	Kind                string                   `json:"kind"`
	Provider            string                   `json:"provider"`
	ScopeHash           string                   `json:"scope_hash"`
	IdentityHash        string                   `json:"identity_hash"`
	SourceID            string                   `json:"source_id"`
	SourceRevision      int64                    `json:"source_revision"`
	CollectorID         string                   `json:"collector_id"`
	CollectorInstanceID string                   `json:"collector_instance_id"`
	Generation          string                   `json:"generation"`
	PollID              string                   `json:"poll_id"`
	PollSequence        string                   `json:"poll_sequence"`
	StartedAt           time.Time                `json:"started_at"`
	CompletedAt         time.Time                `json:"completed_at"`
	CapturedAt          time.Time                `json:"captured_at"`
	ElapsedMS           *int64                   `json:"elapsed_ms,omitempty"`
	IntervalSeconds     int                      `json:"interval_seconds"`
	RequestCount        int                      `json:"request_count"`
	PageCount           int                      `json:"page_count"`
	MissedTicks         string                   `json:"missed_ticks"`
	ExpectedEntities    int                      `json:"expected_entities"`
	InventoryComplete   bool                     `json:"inventory_complete"`
	Incomplete          bool                     `json:"incomplete"`
	UnavailableReason   string                   `json:"unavailable_reason,omitempty"`
	Rows                []MessagingManagementRow `json:"rows"`
}

func (s *MessagingManagementSnapshot) Validate() error {
	if s == nil || s.Version != MessagingManagementVersion || !validManagementKind(s.Kind) {
		return errors.New("invalid management snapshot schema")
	}
	if err := validateManagementProvider(s.Provider); err != nil {
		return err
	}
	for _, x := range []string{s.SourceID, s.CollectorID, s.CollectorInstanceID, s.PollID} {
		if !validManagementText(x, 256, false) {
			return errors.New("management snapshot identity missing")
		}
	}
	if s.SourceRevision < 1 || !validManagementHash(s.ScopeHash) || !validManagementHash(s.IdentityHash) {
		return errors.New("invalid management snapshot revision or scope")
	}
	for _, x := range []string{s.Generation, s.PollSequence} {
		v, e := ParseManagementInteger(x)
		if e != nil || v < 1 {
			return errors.New("management generation/sequence must be positive exact integers")
		}
	}
	if _, err := ParseManagementInteger(s.MissedTicks); err != nil {
		return err
	}
	if !validManagementTime(s.StartedAt) || !validManagementTime(s.CompletedAt) || !validManagementTime(s.CapturedAt) {
		return errors.New("management snapshot times must be UTC")
	}
	if s.ElapsedMS != nil && *s.ElapsedMS < 0 {
		return errors.New("negative management elapsed time")
	}
	if s.IntervalSeconds < 10 || s.IntervalSeconds > 3600 || s.Kind == ManagementKindSolace && s.IntervalSeconds < 30 {
		return errors.New("management cadence violates pack policy")
	}
	if s.RequestCount < 0 || s.RequestCount > 512 || s.PageCount < 0 || s.PageCount > 16 || s.PageCount > s.RequestCount || s.ExpectedEntities < 1 || s.ExpectedEntities > MaxManagementEntities || len(s.Rows) > s.ExpectedEntities {
		return errors.New("management snapshot inventory/budget exceeds bounds")
	}
	if !validManagementReason(s.UnavailableReason) || s.UnavailableReason != "" && !s.Incomplete {
		return errors.New("invalid management snapshot coverage reason")
	}
	if !s.InventoryComplete && !s.Incomplete {
		return errors.New("incomplete inventory requires coverage flag")
	}
	if len(s.Rows) == 0 && (s.UnavailableReason == "" || !s.Incomplete || s.InventoryComplete) {
		return errors.New("empty management poll requires unavailable coverage")
	}
	seen := map[string]bool{}
	for _, r := range s.Rows {
		if err := r.Validate(); err != nil {
			return err
		}
		if r.Entity.Kind() != s.Kind {
			return errors.New("management snapshot provider mismatch")
		}
		id, _ := r.Entity.ID()
		if seen[id] {
			return errors.New("duplicate management snapshot entity")
		}
		seen[id] = true
		if r.State != ManagementStateObserved && (s.InventoryComplete || !s.Incomplete) {
			return errors.New("unavailable entity requires incomplete coverage")
		}
	}
	if s.InventoryComplete && len(s.Rows) != s.ExpectedEntities {
		return errors.New("complete management inventory count mismatch")
	}
	type plain MessagingManagementSnapshot
	b, err := json.Marshal(plain(*s))
	if err != nil {
		return err
	}
	if len(b) > MaxManagementSnapshotBytes {
		return errors.New("management snapshot exceeds byte limit")
	}
	return nil
}
func (s *MessagingManagementSnapshot) ValidateFor(scope ManagementScope, identity ManagementIdentity) error {
	if err := s.Validate(); err != nil {
		return err
	}
	h, err := scope.Hash()
	if err != nil {
		return err
	}
	i, err := identity.Fingerprint()
	if err != nil {
		return err
	}
	if s.ScopeHash != h || s.IdentityHash != i || s.Kind != scope.Kind || s.Provider != scope.Provider || s.ExpectedEntities != len(scope.Entities) {
		return errors.New("management snapshot does not match approved scope/identity")
	}
	approved := map[string]bool{}
	for _, e := range scope.Entities {
		id, _ := e.ID()
		approved[id] = true
	}
	for _, r := range s.Rows {
		id, _ := r.Entity.ID()
		if !approved[id] {
			return errors.New("unapproved management observation")
		}
		if r.SolaceSEMP != nil && r.SolaceSEMP.DeadMessageQueue != "" {
			found := false
			for _, e := range scope.Entities {
				if e.Solace != nil && e.Solace.VPN == r.Entity.Solace.VPN && e.Solace.Queue == r.SolaceSEMP.DeadMessageQueue {
					found = true
				}
			}
			if !found {
				return errors.New("unapproved Solace DMQ relationship")
			}
		}
	}
	return nil
}
func (s *MessagingManagementSnapshot) Clone() *MessagingManagementSnapshot {
	if s == nil {
		return nil
	}
	x := *s
	x.Rows = make([]MessagingManagementRow, len(s.Rows))
	for i := range s.Rows {
		x.Rows[i] = s.Rows[i].Clone()
	}
	if s.ElapsedMS != nil {
		v := *s.ElapsedMS
		x.ElapsedMS = &v
	}
	return &x
}
func (s MessagingManagementSnapshot) MarshalJSON() ([]byte, error) {
	type plain MessagingManagementSnapshot
	x := plain(s)
	if x.Rows == nil {
		x.Rows = []MessagingManagementRow{}
	}
	return json.Marshal(x)
}
func (s *MessagingManagementSnapshot) UnmarshalJSON(data []byte) error {
	if len(data) > MaxManagementSnapshotBytes {
		return errors.New("management snapshot exceeds byte limit")
	}
	type plain MessagingManagementSnapshot
	var x plain
	if err := decodeManagementJSON(data, &x); err != nil {
		return err
	}
	if err := requireManagementFields(data, "version", "kind", "provider", "scope_hash", "identity_hash", "source_id", "source_revision", "collector_id", "collector_instance_id", "generation", "poll_id", "poll_sequence", "started_at", "completed_at", "captured_at", "interval_seconds", "request_count", "page_count", "missed_ticks", "expected_entities", "inventory_complete", "incomplete", "rows"); err != nil {
		return err
	}
	p := MessagingManagementSnapshot(x)
	if err := p.Validate(); err != nil {
		return err
	}
	*s = p
	return nil
}
func NewUnavailableManagementSnapshot(base MessagingManagementSnapshot, reason string) (*MessagingManagementSnapshot, error) {
	s := base.Clone()
	s.Rows = []MessagingManagementRow{}
	s.InventoryComplete = false
	s.Incomplete = true
	s.UnavailableReason = reason
	if reason == "" {
		return nil, errors.New("unavailable management poll requires reason")
	}
	return s, s.Validate()
}
func EncodeMessagingManagement(s *MessagingManagementSnapshot) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	x := s.Clone()
	sort.Slice(x.Rows, func(i, j int) bool { a, _ := x.Rows[i].Entity.ID(); b, _ := x.Rows[j].Entity.ID(); return a < b })
	b, err := json.Marshal(x)
	if err == nil && len(b) > MaxManagementSnapshotBytes {
		return nil, errors.New("management snapshot exceeds byte limit")
	}
	return b, err
}
func DecodeMessagingManagement(data []byte) (*MessagingManagementSnapshot, error) {
	if len(data) > MaxManagementSnapshotBytes {
		return nil, errors.New("management snapshot exceeds byte limit")
	}
	var s MessagingManagementSnapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
func validManagementTime(t time.Time) bool {
	return !t.IsZero() && t.Location() == time.UTC && t.Year() >= 1 && t.Year() <= 9999
}

// ReplaceManagementSnapshot preserves latest poll semantics without summing
// positions or populations. Origin/authentication is checked by the receiver.
func ReplaceManagementSnapshot(dst **MessagingManagementSnapshot, src *MessagingManagementSnapshot) error {
	if dst == nil {
		return errors.New("management snapshot destination missing")
	}
	if err := src.Validate(); err != nil {
		return err
	}
	if *dst == nil {
		*dst = src.Clone()
		return nil
	}
	old := *dst
	if err := old.Validate(); err != nil {
		return err
	}
	if old.SourceID != src.SourceID {
		return errors.New("management snapshots belong to different sources")
	}
	g, _ := ParseManagementInteger(src.Generation)
	og, _ := ParseManagementInteger(old.Generation)
	n, _ := ParseManagementInteger(src.PollSequence)
	on, _ := ParseManagementInteger(old.PollSequence)
	if g < og || g == og && n < on {
		return nil
	}
	if g == og && n == on {
		a, _ := EncodeMessagingManagement(old)
		b, _ := EncodeMessagingManagement(src)
		if string(a) != string(b) {
			return errors.New("conflicting management poll replay")
		}
		return nil
	}
	if g == og && (src.CollectorID != old.CollectorID || src.CollectorInstanceID != old.CollectorInstanceID || src.SourceRevision != old.SourceRevision || src.ScopeHash != old.ScopeHash || src.IdentityHash != old.IdentityHash) {
		return errors.New("management identity changed without new generation")
	}
	*dst = src.Clone()
	return nil
}

// ProjectScalars returns only safe exact observed numbers. Typed-only large
// integers become missing scalar points; they remain exact in the snapshot.
func (s *MessagingManagementSnapshot) ProjectScalars(a MonitorEvidenceAttribution) ([]MetricPoint, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if a.SourceID != s.SourceID || a.SourceRevision != s.SourceRevision || a.CollectorID != s.CollectorID {
		return nil, errors.New("management scalar attribution mismatch")
	}
	points := []MetricPoint{}
	for _, r := range s.Rows {
		if r.State != ManagementStateObserved {
			continue
		}
		id, _ := r.Entity.ID()
		type measurement struct {
			name, unit, pop string
			v               ManagementInteger
		}
		values := []measurement{}
		if k := r.KafkaOffsets; k != nil {
			values = []measurement{{"kafka.committed_next", "count", "kafka_next_offset", k.CommittedNext}, {"kafka.high_watermark_next", "count", "kafka_next_offset", k.HighWatermarkNext}, {"kafka.last_stable_next", "count", "kafka_next_offset", k.LastStableNext}}
		}
		if b := r.ServiceBusRuntime; b != nil {
			values = []measurement{{"servicebus.total", "count", "servicebus_runtime_messages", b.Total}, {"servicebus.active", "count", "servicebus_runtime_messages", b.Active}, {"servicebus.scheduled", "count", "servicebus_runtime_messages", b.Scheduled}, {"servicebus.dead_letter", "count", "servicebus_runtime_messages", b.DeadLetter}, {"servicebus.transfer", "count", "servicebus_runtime_messages", b.Transfer}, {"servicebus.transfer_dead_letter", "count", "servicebus_runtime_messages", b.TransferDeadLetter}, {"servicebus.size", "bytes", "servicebus_runtime_bytes", b.SizeBytes}}
		}
		if b := r.SolaceSEMP; b != nil {
			values = []measurement{{"solace.depth", "count", "solace_runtime_messages", b.Depth}}
		}
		for _, m := range values {
			p := MetricPoint{SchemaVersion: SchemaVersion, ProjectID: a.ProjectID, RunID: a.RunID, ScenarioID: a.ScenarioID, Source: a.SourceID, Host: a.CollectorID, TimestampUTC: *r.ObservedAt, ReceivedAt: a.ReceivedAt, MetricName: "messaging." + m.name, MetricVersion: "apexload.messaging_management.v1", Population: m.pop, Kind: KindGauge, Unit: m.unit, Quality: QualityMissing, Dimensions: map[string]string{"entity_id": id, "source_generation": s.Generation, "scope_hash": s.ScopeHash}}
			if m.v.State == ManagementStateObserved {
				n, _ := ParseManagementInteger(*m.v.Value)
				if n <= 1<<53-1 {
					f := float64(n)
					p.Value = &f
					p.Quality = QualityValid
				}
			}
			if err := p.Validate(); err != nil {
				return nil, err
			}
			points = append(points, p)
			if len(points) > MaxMonitorEvidencePoints {
				return nil, errors.New("management scalar projection exceeds point limit")
			}
		}
	}
	return points, nil
}

// Ensure exact integer string formatting is shared with callers building polls.
func ManagementIntegerString(value int64) (string, error) {
	if value < 0 {
		return "", errors.New("negative management integer")
	}
	return strconv.FormatInt(value, 10), nil
}
