package analysis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func managementFixture(t *testing.T) (ManagementScope, ManagementIdentity, *MessagingManagementSnapshot) {
	t.Helper()
	scope := ManagementScope{Version: ManagementScopeVersion, Kind: ManagementKindKafka, Provider: "cluster-approved", Isolation: "read_uncommitted", Entities: []ManagementEntity{{Kafka: &KafkaManagementEntity{Topic: "orders", Partition: 0, Group: "reporting"}}}}
	identity := ManagementIdentity{Mode: "sasl_plain", Principal: "management-reader", PolicyRevision: "policy-1", CredentialRef: "monitor-auth", CredentialVersion: "v1"}
	sh, err := scope.Hash()
	if err != nil {
		t.Fatal(err)
	}
	ih, err := identity.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	age := int64(0)
	elapsed := int64(5)
	observed := func(n int64) ManagementInteger {
		v, e := ObservedManagementInteger(n)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	missing := ManagementInteger{State: ManagementStateMissing, Reason: "not_observed"}
	unsupported := ManagementInteger{State: ManagementStateUnsupported, Reason: "unsupported"}
	s := &MessagingManagementSnapshot{Version: MessagingManagementVersion, Kind: scope.Kind, Provider: scope.Provider, ScopeHash: sh, IdentityHash: ih, SourceID: "source-1", SourceRevision: 1, CollectorID: "collector-1", CollectorInstanceID: "instance-1", Generation: "1", PollID: "poll-1", PollSequence: "1", StartedAt: at, CompletedAt: at.Add(5 * time.Millisecond), CapturedAt: at.Add(5 * time.Millisecond), ElapsedMS: &elapsed, IntervalSeconds: 10, RequestCount: 1, PageCount: 1, MissedTicks: "0", ExpectedEntities: 1, InventoryComplete: true, Rows: []MessagingManagementRow{{Entity: scope.Entities[0].Clone(), State: ManagementStateObserved, ObservedAt: &at, ObservationAgeMS: &age, KafkaOffsets: &KafkaOffsets{CommittedNext: observed(math.MaxInt64 - 20), HighWatermarkNext: observed(math.MaxInt64), LastStableNext: observed(math.MaxInt64 - 10), LogEndNext: unsupported, LeaderEpoch: observed(3), CommittedLeaderEpoch: missing, TopicID: "topic-id-1"}}}}
	if err := s.ValidateFor(scope, identity); err != nil {
		t.Fatal(err)
	}
	return scope, identity, s
}
func managementAttribution(s *MessagingManagementSnapshot) MonitorEvidenceAttribution {
	return MonitorEvidenceAttribution{ProjectID: "project-1", RunID: "run-1", ScenarioID: "scenario-1", JobID: "run-1:source-1", SourceID: s.SourceID, SourceRevision: s.SourceRevision, CollectorID: s.CollectorID, ReceivedAt: s.CapturedAt.Add(time.Second)}
}

func TestManagementIntegerExactAndPresence(t *testing.T) {
	for _, value := range []string{"0", "1", "9007199254740991", "9007199254740993", "9223372036854775807"} {
		t.Run(value, func(t *testing.T) {
			v, err := ParseManagementInteger(value)
			if err != nil {
				t.Fatal(err)
			}
			o, err := ObservedManagementInteger(v)
			if err != nil || *o.Value != value {
				t.Fatalf("exact roundtrip %v/%v", o, err)
			}
			b, _ := json.Marshal(o)
			var got ManagementInteger
			if err := json.Unmarshal(b, &got); err != nil || !reflect.DeepEqual(o, got) {
				t.Fatalf("JSON roundtrip %s/%v", b, err)
			}
		})
	}
	for _, value := range []string{"", "00", "01", "-1", "+1", "1.0", "1e3", "9223372036854775808", " 1", "١"} {
		if _, err := ParseManagementInteger(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
	for _, raw := range []string{`null`, `{}`, `{"state":"observed"}`, `{"state":"observed","value":null}`, `{"state":"observed","value":0}`, `{"state":"observed","value":"1","value":"0"}`, `{"state":"observed","value":"1","Value":null}`, `{"State":"observed","value":"0"}`, `{"state":"missing","reason":"not_observed","value":"0"}`, `{"state":"missing"}`, `{"state":"observed","value":"0","secret":"token"}`, string([]byte{123, 34, 115, 116, 97, 116, 101, 34, 58, 34, 255, 34, 125})} {
		var v ManagementInteger
		if err := json.Unmarshal([]byte(raw), &v); err == nil {
			t.Errorf("accepted malformed %s", raw)
		}
	}
	zero, _ := ObservedManagementInteger(0)
	if zero.Value == nil || *zero.Value != "0" {
		t.Fatal("zero lost")
	}
	absent := ManagementInteger{State: ManagementStateMissing, Reason: "not_observed"}
	if err := absent.Validate(); err != nil || absent.Value != nil {
		t.Fatal("missing became zero")
	}
}
func TestManagementScopeCanonicalApprovalAndClone(t *testing.T) {
	scope, identity, _ := managementFixture(t)
	scope.Entities = append(scope.Entities, ManagementEntity{Kafka: &KafkaManagementEntity{Topic: "orders", Partition: 1, Group: "reporting"}})
	h, err := scope.Hash()
	if err != nil {
		t.Fatal(err)
	}
	rev := scope.Clone()
	rev.Entities[0], rev.Entities[1] = rev.Entities[1], rev.Entities[0]
	rh, err := rev.Hash()
	if err != nil || h != rh {
		t.Fatalf("ordering changes scope hash %v", err)
	}
	clone := scope.Clone()
	clone.Entities[0].Kafka.Topic = "changed"
	if scope.Entities[0].Kafka.Topic != "orders" {
		t.Fatal("scope clone aliases caller")
	}
	duplicate := scope.Clone()
	duplicate.Entities = append(duplicate.Entities, duplicate.Entities[0])
	if err := duplicate.Validate(); err == nil {
		t.Fatal("duplicate approved entity accepted")
	}
	changed := identity
	changed.Principal = "different"
	a, _ := identity.Fingerprint()
	b, _ := changed.Fingerprint()
	if a == b {
		t.Fatal("different principals coalesced")
	}
	for _, mutate := range []func(*ManagementScope){func(s *ManagementScope) { s.Version = 2 }, func(s *ManagementScope) { s.Isolation = "" }, func(s *ManagementScope) { s.Provider = "https://user:pass@broker" }, func(s *ManagementScope) { s.Provider = "https://broker?token=hidden" }, func(s *ManagementScope) { s.Entities[0].Kafka.Partition = -1 }, func(s *ManagementScope) { s.Entities[0].Kafka.Topic = "bad\nname" }, func(s *ManagementScope) { s.Entities[0].Solace = &SolaceManagementEntity{VPN: "v", Queue: "q"} }, func(s *ManagementScope) { s.Entities = nil }} {
		x := scope.Clone()
		mutate(&x)
		if err := x.Validate(); err == nil {
			t.Fatal("invalid scope accepted")
		}
	}
	bounded := ManagementScope{Version: 1, Kind: ManagementKindKafka, Provider: "c", Isolation: "read_uncommitted", Entities: []ManagementEntity{}}
	for i := 0; i < 256; i++ {
		bounded.Entities = append(bounded.Entities, ManagementEntity{Kafka: &KafkaManagementEntity{Topic: "t", Partition: int32(i), Group: "g"}})
	}
	if err := bounded.Validate(); err != nil {
		t.Fatalf("short 256-entity scope rejected %v", err)
	}
	bounded.Entities = append(bounded.Entities, ManagementEntity{Kafka: &KafkaManagementEntity{Topic: "t", Partition: 256, Group: "g"}})
	if err := bounded.Validate(); err == nil {
		t.Fatal("257 entities accepted")
	}
	raw, _ := json.Marshal(scope)
	bad := bytes.Replace(raw, []byte(`"partition":0`), []byte(`"Partition":0`), 1)
	var decoded ManagementScope
	if err := json.Unmarshal(bad, &decoded); err == nil {
		t.Fatal("noncanonical partition alias accepted")
	}
	bad = bytes.Replace(raw, []byte(`"partition":0,`), nil, 1)
	if err := json.Unmarshal(bad, &decoded); err == nil {
		t.Fatal("missing partition silently became zero")
	}
}
func TestManagementTypedProviderSemantics(t *testing.T) {
	scope, identity, s := managementFixture(t)
	k := s.Rows[0].KafkaOffsets
	for isolation, want := range map[string]string{"read_uncommitted": "20", "read_committed": "10"} {
		lag, err := k.Lag(isolation)
		if err != nil || lag.Value == nil || *lag.Value != want {
			t.Fatalf("%s lag %v %v", isolation, lag, err)
		}
	}
	x := k.Clone()
	x.CommittedNext, _ = ObservedManagementInteger(math.MaxInt64)
	x.HighWatermarkNext, _ = ObservedManagementInteger(math.MaxInt64 - 1)
	lag, err := x.Lag("read_uncommitted")
	if err != nil || lag.State != ManagementStateInvalid || lag.Value != nil {
		t.Fatal("negative lag clamped")
	}
	x = k.Clone()
	x.CommittedNext = ManagementInteger{State: ManagementStateMissing, Reason: "not_observed"}
	lag, _ = x.Lag("read_uncommitted")
	if lag.Value != nil {
		t.Fatal("missing committed offset became zero")
	}
	physical := k.Clone()
	physical.LogEndNext, _ = ObservedManagementInteger(1)
	if err := physical.Validate(); err == nil {
		t.Fatal("unproven physical log end accepted")
	}
	if _, err := k.Lag("physical_log_end"); err == nil {
		t.Fatal("unknown basis accepted")
	}
	if err := s.ValidateFor(scope, identity); err != nil {
		t.Fatal(err)
	}
	foreign := scope.Clone()
	foreign.Entities[0].Kafka.Partition = 2
	sh, _ := foreign.Hash()
	s.ScopeHash = sh
	if err := s.ValidateFor(foreign, identity); err == nil {
		t.Fatal("unapproved entity accepted")
	}
	missing := ManagementInteger{State: ManagementStateMissing, Reason: "not_observed"}
	unsupported := ManagementInteger{State: ManagementStateUnsupported, Reason: "unsupported"}
	zero, _ := ObservedManagementInteger(0)
	sb := ServiceBusRuntime{Total: missing, Active: zero, Scheduled: missing, DeadLetter: missing, Transfer: missing, TransferDeadLetter: missing, SizeBytes: missing, Deferred: unsupported}
	if err := sb.Validate(); err != nil {
		t.Fatal(err)
	}
	sb.Deferred = zero
	if err := sb.Validate(); err == nil {
		t.Fatal("invented deferred population accepted")
	}
	raw := []byte(`{"depth":{"state":"observed","value":"9007199254740993"}}`)
	var solace SolaceSEMP
	if err := json.Unmarshal(raw, &solace); err != nil || *solace.Depth.Value != "9007199254740993" {
		t.Fatalf("Solace exact depth %v", err)
	}
}
func TestManagementSnapshotUnavailableAndStrictWire(t *testing.T) {
	scope, identity, s := managementFixture(t)
	unavailable, err := NewUnavailableManagementSnapshot(*s, "permission_denied")
	if err != nil {
		t.Fatal(err)
	}
	if err := unavailable.ValidateFor(scope, identity); err != nil {
		t.Fatal(err)
	}
	b, err := EncodeMessagingManagement(unavailable)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte(`"rows":[]`)) {
		t.Fatal("null rows")
	}
	got, err := DecodeMessagingManagement(b)
	if err != nil || len(got.Rows) != 0 || !got.Incomplete {
		t.Fatalf("unavailable roundtrip %v", err)
	}
	b, _ = EncodeMessagingManagement(s)
	mutations := map[string][]byte{
		"missing requests":   bytes.Replace(b, []byte(`"request_count":1,`), nil, 1),
		"null requests":      bytes.Replace(b, []byte(`"request_count":1`), []byte(`"request_count":null`), 1),
		"aliased requests":   bytes.Replace(b, []byte(`"request_count":1`), []byte(`"Request_Count":1`), 1),
		"duplicate requests": bytes.Replace(b, []byte(`"request_count":1`), []byte(`"request_count":1,"request_count":0`), 1),
		"missing counter":    bytes.Replace(b, []byte(`"leader_epoch":{"state":"observed","value":"3"},`), nil, 1),
		"unknown version":    bytes.Replace(b, []byte(`"version":1`), []byte(`"version":2`), 1),
		"oversize":           append(bytes.Repeat([]byte(" "), MaxManagementSnapshotBytes), b...),
		"null rows":          bytes.Replace(b, []byte(`"rows":[`), []byte(`"rows":null,"ignored":[`), 1),
	}
	for name, raw := range mutations {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeMessagingManagement(raw); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*MessagingManagementSnapshot){"empty without reason": func(s *MessagingManagementSnapshot) { s.Rows = nil }, "duplicate": func(s *MessagingManagementSnapshot) { s.Rows = append(s.Rows, s.Rows[0]); s.ExpectedEntities = 2 }, "bad generation": func(s *MessagingManagementSnapshot) { s.Generation = "01" }, "negative age": func(s *MessagingManagementSnapshot) { n := int64(-1); s.Rows[0].ObservationAgeMS = &n }, "incomplete not flagged": func(s *MessagingManagementSnapshot) { s.InventoryComplete = false }, "excess pages": func(s *MessagingManagementSnapshot) { s.PageCount = 17 }, "wrong variant": func(s *MessagingManagementSnapshot) { s.Rows[0].ServiceBusRuntime = &ServiceBusRuntime{} }, "unavailable with observations": func(s *MessagingManagementSnapshot) {
		s.Rows[0].State = ManagementStatePermission
		s.Rows[0].Reason = "permission_denied"
		s.InventoryComplete = false
		s.Incomplete = true
	}} {
		t.Run(name, func(t *testing.T) {
			x := s.Clone()
			mutate(x)
			if err := x.Validate(); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
	// UTC provenance and measured monotonic elapsed are independent of wall-clock
	// jumps; validation must not manufacture a cross-process clock calibration.
	x := s.Clone()
	x.CompletedAt = x.StartedAt.Add(-time.Second)
	if err := x.Validate(); err != nil {
		t.Fatalf("wall-clock provenance lost: %v", err)
	}
}
func TestManagementLatestReplayAndGeneration(t *testing.T) {
	_, _, s := managementFixture(t)
	var latest *MessagingManagementSnapshot
	if err := ReplaceManagementSnapshot(&latest, s); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceManagementSnapshot(&latest, s); err != nil {
		t.Fatal(err)
	}
	if latest == s {
		t.Fatal("latest aliases source")
	}
	conflict := s.Clone()
	conflict.Rows[0].KafkaOffsets.TopicID = "other"
	if err := ReplaceManagementSnapshot(&latest, conflict); err == nil {
		t.Fatal("changed replay accepted")
	}
	newer := s.Clone()
	newer.PollSequence = "2"
	newer.PollID = "poll-2"
	newer.Rows[0].KafkaOffsets.HighWatermarkNext, _ = ObservedManagementInteger(1)
	if err := ReplaceManagementSnapshot(&latest, newer); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceManagementSnapshot(&latest, s); err != nil {
		t.Fatal(err)
	}
	if latest.PollSequence != "2" {
		t.Fatal("old poll replaced latest")
	}
	changed := newer.Clone()
	changed.PollSequence = "3"
	changed.IdentityHash = strings.Repeat("f", 64)
	if err := ReplaceManagementSnapshot(&latest, changed); err == nil {
		t.Fatal("identity changed within generation")
	}
	changed.Generation = "2"
	changed.PollSequence = "1"
	changed.CollectorID = "collector-2"
	changed.CollectorInstanceID = "instance-2"
	if err := ReplaceManagementSnapshot(&latest, changed); err != nil {
		t.Fatal(err)
	}
	failed, err := NewUnavailableManagementSnapshot(*changed, "timeout")
	if err != nil {
		t.Fatal(err)
	}
	failed.PollSequence = "2"
	failed.PollID = "failed-poll"
	if err := ReplaceManagementSnapshot(&latest, failed); err != nil {
		t.Fatal(err)
	}
	if len(latest.Rows) != 0 {
		t.Fatal("failed poll freshened previous observed values")
	}
}
func TestManagementDeepCloneAndSafeScalarProjection(t *testing.T) {
	_, _, s := managementFixture(t)
	clone := s.Clone()
	*clone.Rows[0].KafkaOffsets.CommittedNext.Value = "0"
	clone.Rows[0].Entity.Kafka.Topic = "changed"
	*clone.Rows[0].ObservationAgeMS = 50
	*clone.Rows[0].ObservedAt = clone.StartedAt.Add(time.Hour)
	*clone.ElapsedMS = 100
	if *s.Rows[0].KafkaOffsets.CommittedNext.Value != "9223372036854775787" || s.Rows[0].Entity.Kafka.Topic != "orders" || *s.Rows[0].ObservationAgeMS != 0 || *s.ElapsedMS != 5 {
		t.Fatal("snapshot clone aliases")
	}
	p, err := s.ProjectScalars(managementAttribution(s))
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range p {
		if point.Value != nil || point.Quality != QualityMissing {
			t.Fatal("large exact offsets rounded into metric values")
		}
	}
	s.Rows[0].KafkaOffsets.CommittedNext, _ = ObservedManagementInteger(0)
	p, err = s.ProjectScalars(managementAttribution(s))
	if err != nil || p[0].Value == nil || *p[0].Value != 0 || p[0].Quality != QualityValid {
		t.Fatal("observed zero projection lost")
	}
	row := s.Rows[0]
	lag, err := row.QualifiedKafkaLag("read_uncommitted", 30_000)
	if err != nil || lag.Value == nil {
		t.Fatal("qualified lag lost")
	}
	row.ObservationAgeMS = nil
	lag, _ = row.QualifiedKafkaLag("read_uncommitted", 30_000)
	if lag.Value != nil || lag.Reason != "unqualified" {
		t.Fatal("missing source age qualified")
	}
	age := int64(30_001)
	row.ObservationAgeMS = &age
	lag, _ = row.QualifiedKafkaLag("read_uncommitted", 30_000)
	if lag.Value != nil || lag.State != ManagementStateStale {
		t.Fatal("stale age qualified")
	}
	// Explicitly reject a projection larger than the receipt contract rather than
	// silently truncating values, while retaining the complete typed snapshot.
	rows := []MessagingManagementRow{}
	for i := 0; i < 180; i++ {
		r := s.Rows[0].Clone()
		r.Entity.Kafka.Partition = int32(i)
		rows = append(rows, r)
	}
	s.Rows = rows
	s.ExpectedEntities = len(rows)
	if _, err := s.ProjectScalars(managementAttribution(s)); err == nil {
		t.Fatal("oversized projection silently truncated")
	}
	if _, err := EncodeMessagingManagement(s); err != nil {
		t.Fatalf("bounded typed snapshot should survive projection limit: %v", err)
	}
}
func TestManagementAlternateInventoriesAndBounds(t *testing.T) {
	for _, entity := range []ManagementEntity{{ServiceBus: &ServiceBusManagementEntity{Queue: "orders"}}, {ServiceBus: &ServiceBusManagementEntity{Topic: "t", Subscription: "s"}}, {Solace: &SolaceManagementEntity{VPN: "v", Queue: "q"}}, {Solace: &SolaceManagementEntity{VPN: "v", TopicEndpoint: "e"}}} {
		if _, err := entity.ID(); err != nil {
			t.Fatal(err)
		}
	}
	for _, entity := range []ManagementEntity{{ServiceBus: &ServiceBusManagementEntity{Topic: "t"}}, {ServiceBus: &ServiceBusManagementEntity{Queue: "q", Topic: "t", Subscription: "s"}}, {Solace: &SolaceManagementEntity{VPN: "v"}}} {
		if err := entity.Validate(); err == nil {
			t.Fatal("ambiguous inventory accepted")
		}
	}
	scope, _, _ := managementFixture(t)
	scope.Entities = make([]ManagementEntity, 100)
	for i := range scope.Entities {
		scope.Entities[i] = ManagementEntity{Kafka: &KafkaManagementEntity{Topic: strings.Repeat("x", 256), Partition: int32(i), Group: fmt.Sprint(i)}}
	}
	if err := scope.Validate(); err == nil {
		t.Fatal("oversized source inventory accepted")
	}
}

func TestManagementProviderIdentityDisclosureBoundary(t *testing.T) {
	for _, provider := range []string{"cluster-approved", "https://broker.internal:443", "kafka-cluster:approved"} {
		t.Run("valid/"+provider, func(t *testing.T) {
			scope, _, snapshot := managementFixture(t)
			scope.Provider = provider
			snapshot.Provider = provider
			if err := scope.Validate(); err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeMessagingManagement(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeMessagingManagement(encoded)
			if err != nil || decoded.Provider != provider {
				t.Fatalf("public provider roundtrip: %v", err)
			}
		})
	}
	for _, provider := range []string{"", "broker\nidentity", "https://user:password@broker.internal", "https://broker.internal?token=hidden", "https://broker.internal#hidden", "https://%invalid"} {
		t.Run("invalid/"+provider, func(t *testing.T) {
			scope, _, snapshot := managementFixture(t)
			scope.Provider = provider
			snapshot.Provider = provider
			if err := scope.Validate(); err == nil {
				t.Fatal("unsafe scope provider accepted")
			}
			if err := snapshot.Validate(); err == nil {
				t.Fatal("unsafe standalone snapshot provider accepted")
			}
			if _, err := EncodeMessagingManagement(snapshot); err == nil {
				t.Fatal("unsafe snapshot encoded")
			}
			raw, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeMessagingManagement(raw); err == nil {
				t.Fatal("unsafe standalone archive snapshot decoded")
			}
		})
	}
}
