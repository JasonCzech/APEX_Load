package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func managementFixture(t *testing.T) (*SQLiteStore, MonitorSource, time.Time) {
	s, _, now := monitorFixture(t)
	return managementSeed(t, s, now)
}
func managementSeed(t *testing.T, s *SQLiteStore, now time.Time) (*SQLiteStore, MonitorSource, time.Time) {
	t.Helper()
	m := MonitorSource{ID: "monitor-source", ProjectID: "monitor-p", CollectorID: "monitor-collector", Name: "offsets", Pack: "kafka-offsets", Destination: "management://broker-readonly", IntervalSeconds: 10, Enabled: true, ManagementScope: &analysis.ManagementScope{Version: 1, Kind: "kafka_offsets", Provider: "cluster-1", Isolation: "read_committed", Entities: []analysis.ManagementEntity{{Kafka: &analysis.KafkaManagementEntity{Topic: "orders", Partition: 0, Group: "load"}}}}, ManagementIdentity: &analysis.ManagementIdentity{Mode: "anonymous", Principal: "fixture", PolicyRevision: "policy-1"}}
	packs := []string{"linux-hosts", "prometheus", "kafka-offsets"}
	if ok, e := s.PatchMonitoringCollector(m.CollectorID, m.ProjectID, nil, nil, nil, &packs); e != nil || !ok {
		t.Fatalf("approval: %v %v", ok, e)
	}
	created, e := s.SaveMonitorSource(m, 0)
	if e != nil {
		t.Fatal(e)
	}
	m = *created
	if _, e = s.SaveMonitorBindings(MonitorBindings{ProjectID: m.ProjectID, ScenarioID: "monitor-scenario", Sources: []MonitorBinding{{SourceID: m.ID, Required: true}}}, 0); e != nil {
		t.Fatal(e)
	}
	saveManagementReady(t, s, m, "instance-1", now)
	if _, e = s.StartRunMonitors(m.ProjectID, "monitor-run", "monitor-scenario", now, now.Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	return s, m, now
}
func saveManagementReady(t *testing.T, s *SQLiteStore, m MonitorSource, instance string, now time.Time) {
	t.Helper()
	sh, ih, e := managementSourceHashes(m)
	if e != nil {
		t.Fatal(e)
	}
	e = s.SaveCollectorManagementReadiness(m.ProjectID, m.CollectorID, CollectorManagementReadiness{InstanceID: instance, Capabilities: []string{ManagementEvidenceCapability}, Scopes: []ManagementReadinessScope{{sh, ih}}, Packs: []string{m.Pack}, Destinations: []string{m.ReadinessDestination()}, SeenAt: now})
	if e != nil {
		t.Fatal(e)
	}
}
func addManagementRun(t *testing.T, s *SQLiteStore, m MonitorSource, id string, now time.Time) {
	t.Helper()
	if e := s.SaveTest(&TestRecord{ID: id, Name: id, ProjectID: m.ProjectID, ScenarioID: "monitor-scenario", Status: "RUNNING", StartTime: now}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.StartRunMonitors(m.ProjectID, id, "monitor-scenario", now, now.Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
}
func managementReceipts(p *ManagementPoll, now time.Time) []MonitorBatchReceipt {
	a := []MonitorBatchReceipt{}
	v, c, rows := int64(2), int64(1), int64(0)
	for _, m := range p.Cohort {
		digest := sha256.Sum256([]byte(p.ID + "/" + m.Job.ID))
		owner := sha256.Sum256([]byte(p.ProjectID + "\x00" + m.Job.RunID))
		d := hex.EncodeToString(digest[:])
		a = append(a, MonitorBatchReceipt{JobID: m.Job.ID, Sequence: m.Sequence, Digest: d, ArtifactKey: "monitor-evidence/v1/" + hex.EncodeToString(owner[:]) + "/" + d + ".json", SampleCount: 0, ReceivedAt: now, EvidenceVersion: &v, ManagementSnapshotCount: &c, ManagementRowCount: &rows, CollectionGeneration: &p.Generation, ManagementPollID: &p.ID})
	}
	return a
}

const managementCore = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestManagementPollAtomicCohortAndAcceptedReplay(t *testing.T) {
	s, m, now := managementFixture(t)
	addManagementRun(t, s, m, "monitor-run-2", now)
	p, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "nonce-1", now)
	if e != nil || len(p.Cohort) != 2 {
		t.Fatalf("begin: %#v %v", p, e)
	}
	same, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", p.LeaderJobID, "nonce-1", now.Add(time.Second))
	if e != nil || same.ID != p.ID {
		t.Fatalf("begin retry: %#v %v", same, e)
	}
	if _, e = beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", p.LeaderJobID, "other", now); !errors.Is(e, ErrManagementPollUnavailable) {
		t.Fatalf("overlap: %v", e)
	}
	receipts := managementReceipts(p, now.Add(time.Second))
	bad := append([]MonitorBatchReceipt{}, receipts...)
	bad[1].Sequence++
	if e = s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, managementCore, bad, now.Add(time.Second)); e == nil {
		t.Fatal("partial publication accepted")
	}
	for _, member := range p.Cohort {
		j, _ := s.GetRunMonitorJob(m.ProjectID, member.Job.ID)
		if j.NextSequence != 0 {
			t.Fatal("partial advance")
		}
	}
	if e = s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, managementCore, receipts, now.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	for _, member := range p.Cohort {
		rows, e := s.ListMonitorBatchReceipts(m.ProjectID, member.Job.RunID, "", -1, 10)
		if e != nil || len(rows) != 1 || rows[0].SampleCount != 0 || rows[0].ManagementRowCount == nil || *rows[0].ManagementRowCount != 0 {
			t.Fatalf("typed-only: %#v %v", rows, e)
		}
	}
	for _, member := range p.Cohort {
		if e = s.StopRunMonitors(m.ProjectID, member.Job.RunID, now.Add(2*time.Second)); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.DeleteMonitorSource(m.ProjectID, m.ID, m.Revision); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteTest("monitor-run"); e != nil {
		t.Fatal(e)
	}
	if e = s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, managementCore, receipts, now.Add(time.Hour)); e != nil {
		t.Fatalf("accepted replay after stop/delete/expiry: %v", e)
	}
	if e = s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, strings.Repeat("b", 64), receipts, now.Add(time.Hour)); !errors.Is(e, ErrManagementPollConflict) {
		t.Fatalf("changed core: %v", e)
	}
	current, e := s.GetManagementPoll(m.ProjectID, p.ID)
	if e != nil || current.State != "accepted" || len(current.PublishedReceipts) != 2 {
		t.Fatalf("journal: %#v %v", current, e)
	}
	if _, e = s.GetManagementPoll("monitor-other", p.ID); e == nil {
		t.Fatal("cross-project journal")
	}
}
func TestManagementPollExpiredOwnerRequiresActualDrain(t *testing.T) {
	s, m, now := managementFixture(t)
	p, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "nonce-1", now)
	if e != nil {
		t.Fatal(e)
	}
	later := now.Add(ManagementPollLifetime + time.Second)
	saveManagementReady(t, s, m, "instance-2", later)
	if _, e = beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-2", p.LeaderJobID, "new-owner", later); !errors.Is(e, ErrManagementPollUnavailable) {
		t.Fatalf("expired overlap: %v", e)
	}
	p, e = s.GetManagementPoll(m.ProjectID, p.ID)
	if e != nil || p.State != "draining" {
		t.Fatalf("expired coverage: %#v %v", p, e)
	}
	if e = s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-2", p.ID, p.Generation, ManagementDrainProof{Kind: "completed"}, later); !errors.Is(e, ErrManagementPollConflict) {
		t.Fatalf("wrong owner drain: %v", e)
	}
	if e = s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, p.Generation, ManagementDrainProof{}, later); e != nil {
		t.Fatal(e)
	}
	if _, e = beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-2", p.LeaderJobID, "new-owner", later); !errors.Is(e, ErrManagementPollUnavailable) {
		t.Fatal("cancellation released source")
	}
	if e = s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, p.Generation, ManagementDrainProof{Kind: "process_stopped"}, later); e == nil {
		t.Fatal("unaudited process stop")
	}
	if e = s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, p.Generation, ManagementDrainProof{Kind: "completed"}, later); e != nil {
		t.Fatal(e)
	}
	next, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-2", p.LeaderJobID, "new-owner", later)
	if e != nil || next.Generation <= p.Generation || next.Sequence <= p.Sequence {
		t.Fatalf("takeover: %#v %v", next, e)
	}
	if e = s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, managementCore, managementReceipts(p, later), later); e == nil {
		t.Fatal("old observation accepted after takeover")
	}
}
func TestManagementPollSerializesImmutableRevisionsFairly(t *testing.T) {
	s, m, now := managementFixture(t)
	old, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "old", now)
	if e != nil {
		t.Fatal(e)
	}
	newer := m
	identity := *m.ManagementIdentity
	identity.PolicyRevision = "policy-2"
	newer.ManagementIdentity = &identity
	newSource, e := s.SaveMonitorSource(newer, m.Revision)
	if e != nil {
		t.Fatal(e)
	}
	newer = *newSource
	saveManagementReady(t, s, newer, "instance-1", now)
	addManagementRun(t, s, newer, "new-run", now)
	// Completing the old observer does not silently rebind its source policy.
	if e = s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-1", old.ID, old.Generation, ManagementDrainProof{Kind: "completed"}, now); e != nil {
		t.Fatal(e)
	}
	sh, ih, _ := managementSourceHashes(m)
	sh2, ih2, _ := managementSourceHashes(newer)
	if e = s.SaveCollectorManagementReadiness(m.ProjectID, m.CollectorID, CollectorManagementReadiness{InstanceID: "instance-1", Capabilities: []string{ManagementEvidenceCapability}, Scopes: []ManagementReadinessScope{{sh, ih}, {sh2, ih2}}, Packs: []string{m.Pack}, Destinations: []string{m.Destination}, SeenAt: now}); e != nil {
		t.Fatal(e)
	}
	if _, e = beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", old.LeaderJobID, "unfair", now); !errors.Is(e, ErrManagementPollUnavailable) {
		t.Fatalf("old revision starved new: %v", e)
	}
	next, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "new-run:"+m.ID, "new", now)
	if e != nil || next.Source.Revision != newer.Revision || next.Generation <= old.Generation || len(next.Cohort) != 1 {
		t.Fatalf("new revision: %#v %v", next, e)
	}
	if next.Cohort[0].Job.Source.ManagementIdentity.PolicyRevision != "policy-2" {
		t.Fatal("identity snapshot changed")
	}
}
func TestManagementPollStopsMemberAndPinsCohort(t *testing.T) {
	s, m, now := managementFixture(t)
	addManagementRun(t, s, m, "monitor-run-2", now)
	p, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "pin", now)
	if e != nil {
		t.Fatal(e)
	}
	addManagementRun(t, s, m, "late-run", now)
	if e = s.StopRunMonitors(m.ProjectID, "monitor-run", now); e != nil {
		t.Fatal(e)
	}
	receipts := managementReceipts(p, now)
	if e = s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, managementCore, receipts, now); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		run   string
		count int
	}{{"monitor-run", 0}, {"monitor-run-2", 1}, {"late-run", 0}} {
		r, e := s.ListMonitorBatchReceipts(m.ProjectID, tc.run, "", -1, 10)
		if e != nil || len(r) != tc.count {
			t.Fatalf("%s: %#v %v", tc.run, r, e)
		}
	}
	stopped := receipts[0]
	for _, receipt := range receipts {
		if receipt.JobID == "monitor-run:"+m.ID {
			stopped = receipt
		}
	}
	if referenced, e := s.IsMonitorArtifactReferenced(m.ProjectID, stopped.ArtifactKey); e != nil || referenced {
		t.Fatalf("stopped object referenced: %v %v", referenced, e)
	}
	if e = s.QueueMonitorObjectCleanup(m.ProjectID, "monitor-run", stopped.ArtifactKey); e != nil {
		t.Fatal(e)
	}
	if e = s.QueueMonitorObjectCleanup("monitor-other", "monitor-run", stopped.ArtifactKey); e == nil {
		t.Fatal("cross-project cleanup")
	}
	jobs, e := s.ListAnalysisCleanupJobs(now.Add(time.Hour), 10)
	if e != nil || len(jobs) != 1 || jobs[0].Kind != "monitor-object" {
		t.Fatalf("object cleanup: %#v %v", jobs, e)
	}
}
func TestManagementPollConcurrentBeginAndOverflow(t *testing.T) {
	s, m, now := managementFixture(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, nonce := range []string{"one", "two"} {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			_, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, n, now)
			results <- e
		}(nonce)
	}
	wg.Wait()
	close(results)
	wins := 0
	for e := range results {
		if e == nil {
			wins++
		} else if !errors.Is(e, ErrManagementPollUnavailable) {
			t.Fatal(e)
		}
	}
	if wins != 1 {
		t.Fatalf("poll winners %d", wins)
	}
	var id string
	var gen int64
	if e := s.db.QueryRow(`SELECT poll_id,generation FROM monitor_collection_leases WHERE project_id=?`, m.ProjectID).Scan(&id, &gen); e != nil {
		t.Fatal(e)
	}
	if e := s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-1", id, gen, ManagementDrainProof{Kind: "completed"}, now); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`UPDATE monitor_collection_leases SET poll_sequence=? WHERE project_id=?`, int64(math.MaxInt64), m.ProjectID); e != nil {
		t.Fatal(e)
	}
	if _, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "overflow", now); e == nil {
		t.Fatal("sequence wrap")
	}
}
func TestManagementReadinessLegacyDowngradeAndMissingSource(t *testing.T) {
	s, m, now := managementFixture(t)
	if e := s.SaveCollectorRunReadiness(m.ProjectID, m.CollectorID, []string{"linux-hosts"}, nil, now); e != nil {
		t.Fatal(e)
	}
	if _, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "legacy", now); e == nil {
		t.Fatal("legacy readiness supplied management authority")
	}
	saveManagementReady(t, s, m, "instance-1", now)
	if e := s.DeleteMonitorSource(m.ProjectID, m.ID, m.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "deleted", now); e == nil {
		t.Fatal("deleted source admitted poll")
	}
}
func TestManagementPollReopenKeepsFences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "management.db")
	s, e := NewSQLiteStore(path)
	if e != nil {
		t.Fatal(e)
	}
	_, _, now := seedMonitorFixture(t, s)
	_, m, _ := managementSeed(t, s, now)
	p, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "persist", now)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = NewSQLiteStore(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	same, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", p.LeaderJobID, p.Nonce, now)
	if e != nil || same.ID != p.ID {
		t.Fatalf("recovered grant: %#v %v", same, e)
	}
	if _, e = beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", p.LeaderJobID, "new", now); !errors.Is(e, ErrManagementPollUnavailable) {
		t.Fatalf("restart reset fence: %v", e)
	}
}

func TestManagementSourceValidationAndReceiptMetadata(t *testing.T) {
	s, m, now := managementFixture(t)
	for _, tc := range []struct {
		name   string
		change func(*MonitorSource)
	}{{"scope-less", func(m *MonitorSource) { m.ManagementScope = nil }}, {"wrong-pack", func(m *MonitorSource) { m.Pack = "linux-hosts" }}, {"short-cadence", func(m *MonitorSource) { m.IntervalSeconds = 1 }}, {"remote-endpoint", func(m *MonitorSource) { m.Destination = "kafka://broker.example:9093" }}, {"alias-port", func(m *MonitorSource) { m.Destination = "management://broker:9093" }}, {"alias-path", func(m *MonitorSource) { m.Destination = "management://broker/path" }}, {"unicode-alias", func(m *MonitorSource) { m.Destination = "management://bröker" }}, {"secret-url", func(m *MonitorSource) { m.Destination = "https://user:pass@example.test/admin" }}, {"query", func(m *MonitorSource) { m.Destination = "https://example.test/admin?token=redacted" }}, {"secret-ref-without-enrollment", func(m *MonitorSource) {
		i := *m.ManagementIdentity
		i.CredentialRef = "monitor-secret"
		m.ManagementIdentity = &i
	}}, {"large-public-config", func(m *MonitorSource) {
		scope := m.ManagementScope.Clone()
		scope.Entities = nil
		for i := 0; i < 100; i++ {
			scope.Entities = append(scope.Entities, analysis.ManagementEntity{Kafka: &analysis.KafkaManagementEntity{Topic: strings.Repeat("t", 180), Partition: int32(i), Group: strings.Repeat("g", 100)}})
		}
		m.ManagementScope = &scope
	}}} {
		t.Run(tc.name, func(t *testing.T) {
			bad := m
			tc.change(&bad)
			if e := bad.Validate(); e == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
	p, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "metadata", now)
	if e != nil {
		t.Fatal(e)
	}
	r := managementReceipts(p, now)
	for _, tc := range []struct {
		name   string
		change func(*MonitorBatchReceipt)
	}{{"no-version", func(b *MonitorBatchReceipt) { b.EvidenceVersion = nil }}, {"no-snapshot", func(b *MonitorBatchReceipt) { v := int64(0); b.ManagementSnapshotCount = &v }}, {"too-many-rows", func(b *MonitorBatchReceipt) { v := int64(257); b.ManagementRowCount = &v }}, {"wrong-generation", func(b *MonitorBatchReceipt) { v := p.Generation + 1; b.CollectionGeneration = &v }}, {"wrong-key", func(b *MonitorBatchReceipt) {
		b.ArtifactKey = "monitor-evidence/v1/" + strings.Repeat("a", 64) + "/" + b.Digest + ".json"
	}}} {
		t.Run(tc.name, func(t *testing.T) {
			bad := r[0]
			tc.change(&bad)
			if e := s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, managementCore, []MonitorBatchReceipt{bad}, now); e == nil {
				t.Fatal("invalid management metadata accepted")
			}
		})
	}
	if e = s.AcceptMonitorBatch(m.ProjectID, m.CollectorID, r[0]); e == nil {
		t.Fatal("point-only API bypassed cohort validation")
	}
	legacy := r[0]
	legacy.SampleCount = 1
	legacy.EvidenceVersion = nil
	legacy.ManagementSnapshotCount = nil
	legacy.ManagementRowCount = nil
	legacy.CollectionGeneration = nil
	legacy.ManagementPollID = nil
	if e = s.AcceptMonitorBatch(m.ProjectID, m.CollectorID, legacy); e == nil {
		t.Fatal("positive point-only receipt bypassed management admission")
	}

	if e = s.DeleteMonitorSource(m.ProjectID, m.ID, m.Revision); e != nil {
		t.Fatal(e)
	}
	if e = s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, managementCore, r, now); e == nil {
		t.Fatal("fresh publication after source delete")
	}
}
func TestManagementJournalRetentionKeepsReceiptReplay(t *testing.T) {
	s, m, now := managementFixture(t)
	p, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "retained", now)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, managementCore, managementReceipts(p, now), now); e != nil {
		t.Fatal(e)
	}
	if n, e := s.PruneManagementPolls(m.ProjectID, now.Add(time.Hour), 10); e != nil || n != 0 {
		t.Fatalf("receipt journal removed: %d %v", n, e)
	}
	if e = s.DeleteTest("monitor-run"); e != nil {
		t.Fatal(e)
	}
	if n, e := s.PruneManagementPolls(m.ProjectID, now.Add(time.Hour), 10); e != nil || n != 1 {
		t.Fatalf("orphan journal not reclaimed: %d %v", n, e)
	}
}

func TestManagementPollRejectsCorruptDurableFence(t *testing.T) {
	for _, column := range []string{"generation", "poll_sequence"} {
		t.Run(column, func(t *testing.T) {
			s, m, now := managementFixture(t)
			p, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "first", now)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, p.Generation, ManagementDrainProof{Kind: "completed"}, now); e != nil {
				t.Fatal(e)
			}
			if _, e = s.db.Exec(`UPDATE monitor_collection_leases SET `+column+`=0 WHERE project_id=?`, m.ProjectID); e != nil {
				t.Fatal(e)
			}
			if _, e = beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", p.LeaderJobID, "corrupt", now); e == nil {
				t.Fatal("corrupt durable cursor reissued")
			}
		})
	}
}

func beginIssuedManagementPoll(s *SQLiteStore, project, collector, instance, leader, nonce string, now time.Time) (*ManagementPoll, error) {
	p, e := s.BeginManagementPoll(project, collector, instance, leader, nonce, now)
	if e != nil {
		return nil, e
	}
	if p.State == "collecting" {
		if e = s.MarkManagementPollIssued(project, collector, instance, p.ID, p.Generation, now); e != nil {
			return nil, e
		}
		return s.GetManagementPoll(project, p.ID)
	}
	return p, nil
}

func TestManagementPollGrantIssuanceVersusCancellation(t *testing.T) {
	for i := 0; i < 10; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s, m, now := managementFixture(t)
			p, e := s.BeginManagementPoll(m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "unsigned", now)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, managementCore, managementReceipts(p, now), now); e == nil {
				t.Fatal("unissued grant accepted evidence")
			}
			var wg sync.WaitGroup
			ch := make(chan error, 2)
			wg.Add(2)
			go func() {
				defer wg.Done()
				ch <- s.MarkManagementPollIssued(m.ProjectID, m.CollectorID, "instance-1", p.ID, p.Generation, now)
			}()
			go func() {
				defer wg.Done()
				ch <- s.CancelUnissuedManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, p.Generation, now)
			}()
			wg.Wait()
			close(ch)
			wins := 0
			for e := range ch {
				if e == nil {
					wins++
				} else if !errors.Is(e, ErrManagementPollConflict) {
					t.Fatal(e)
				}
			}
			if wins != 1 {
				t.Fatalf("authority transition winners: %d", wins)
			}
			current, e := s.GetManagementPoll(m.ProjectID, p.ID)
			if e != nil {
				t.Fatal(e)
			}
			if current.GrantIssuedAt != nil {
				if e = s.CancelUnissuedManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.ID, p.Generation, now); !errors.Is(e, ErrManagementPollConflict) {
					t.Fatalf("issued authority released: %v", e)
				}
				if _, e = s.BeginManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.LeaderJobID, "overlap", now); !errors.Is(e, ErrManagementPollUnavailable) {
					t.Fatalf("issued authority overlapped: %v", e)
				}
			} else {
				if current.State != "abandoned" {
					t.Fatalf("cancel state %s", current.State)
				}
				if e = s.MarkManagementPollIssued(m.ProjectID, m.CollectorID, "instance-1", p.ID, p.Generation, now); !errors.Is(e, ErrManagementPollConflict) {
					t.Fatalf("cancelled authority escaped: %v", e)
				}
				if _, e = s.BeginManagementPoll(m.ProjectID, m.CollectorID, "instance-1", p.LeaderJobID, "next", now); e != nil {
					t.Fatalf("unissued admission stranded source: %v", e)
				}
			}
		})
	}
}
func TestManagementPollFairnessSkipsUnreadyPriorRevision(t *testing.T) {
	s, m, now := managementFixture(t)
	newer := m
	identity := *m.ManagementIdentity
	identity.PolicyRevision = "policy-2"
	newer.ManagementIdentity = &identity
	saved, e := s.SaveMonitorSource(newer, m.Revision)
	if e != nil {
		t.Fatal(e)
	}
	newer = *saved
	saveManagementReady(t, s, newer, "instance-1", now)
	addManagementRun(t, s, newer, "new-run", now)
	p, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "new-run:"+m.ID, "ready", now)
	if e != nil || p.Source.Revision != newer.Revision || len(p.Cohort) != 1 {
		t.Fatalf("unready old revision blocked ready work: %#v %v", p, e)
	}
	old, e := s.GetRunMonitorJob(m.ProjectID, "monitor-run:"+m.ID)
	if e != nil || old.Source.Revision != m.Revision || old.NextSequence != 0 {
		t.Fatalf("old source silently rebound: %#v %v", old, e)
	}
}
func TestManagementPollFairnessSkipsLegacySourceRevision(t *testing.T) {
	s, legacy, now := monitorFixture(t)
	if _, e := s.SaveMonitorSource(legacy, 0); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SaveMonitorBindings(MonitorBindings{ProjectID: legacy.ProjectID, ScenarioID: "monitor-scenario", Sources: []MonitorBinding{{SourceID: legacy.ID, Required: true}}}, 0); e != nil {
		t.Fatal(e)
	}
	if _, e := s.StartRunMonitors(legacy.ProjectID, "monitor-run", "monitor-scenario", now, now.Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	_, template, _ := managementFixture(t)
	packs := []string{"linux-hosts", "kafka-offsets"}
	if _, e := s.PatchMonitoringCollector(legacy.CollectorID, legacy.ProjectID, nil, nil, nil, &packs); e != nil {
		t.Fatal(e)
	}
	saved, e := s.SaveMonitorSource(template, 1)
	if e != nil {
		t.Fatal(e)
	}
	m := *saved
	saveManagementReady(t, s, m, "instance-1", now)
	addManagementRun(t, s, m, "management-run", now)
	p, e := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "management-run:"+m.ID, "transition", now)
	if e != nil || len(p.Cohort) != 1 {
		t.Fatalf("legacy run blocked management transition: %#v %v", p, e)
	}
	old, e := s.GetRunMonitorJob(m.ProjectID, "monitor-run:"+m.ID)
	if e != nil || old.Source.Pack != "linux-hosts" || old.Source.ManagementScope != nil {
		t.Fatalf("legacy source changed: %#v %v", old, e)
	}
	if e = s.AcceptMonitorBatch(m.ProjectID, m.CollectorID, MonitorBatchReceipt{JobID: old.ID, Sequence: 0, Digest: managementCore, ArtifactKey: "legacy", SampleCount: 1, ReceivedAt: now}); e != nil {
		t.Fatalf("legacy receipt compatibility: %v", e)
	}
}

func TestManagementCurrentPollStatusIsProjectScoped(t *testing.T) {
	s, m, now := managementFixture(t)
	p, e := s.BeginManagementPoll(m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "status", now)
	if e != nil {
		t.Fatal(e)
	}
	current, e := s.GetCurrentManagementPoll(m.ProjectID, m.ID)
	if e != nil || current.ID != p.ID {
		t.Fatalf("source status: %#v %v", current, e)
	}
	if _, e = s.GetCurrentManagementPoll("monitor-other", m.ID); e == nil {
		t.Fatal("cross-project source status")
	}
	if e = s.DeleteMonitorSource(m.ProjectID, m.ID, m.Revision); e != nil {
		t.Fatal(e)
	}
	current, e = s.GetCurrentManagementPoll(m.ProjectID, m.ID)
	if e != nil || current.ID != p.ID {
		t.Fatalf("deleted source lost unresolved authority: %#v %v", current, e)
	}
}

func TestManagementDestinationAliasContract(t *testing.T) {
	for _, alias := range []string{"broker", "Broker_1.example-2", strings.Repeat("a", 128)} {
		if err := validateManagementDestination("management://" + alias); err != nil {
			t.Fatalf("valid alias %q: %v", alias, err)
		}
	}
	for _, destination := range []string{"management://", "management://broker:", "management://[broker]", "management://broker?", "management://broker#", "management://broker!", "management://broker~", "management://" + strings.Repeat("a", 129), "management://bröker"} {
		if err := validateManagementDestination(destination); err == nil {
			t.Fatalf("invalid alias accepted: %q", destination)
		}
	}
}

func TestManagementPollIssuanceRequiresLivePinnedCohort(t *testing.T) {
	for _, issued := range []bool{false, true} {
		for _, mutation := range []string{"monitor-stop", "terminal-status", "deleted"} {
			t.Run(fmt.Sprintf("issued-%t/%s", issued, mutation), func(t *testing.T) {
				s, m, now := managementFixture(t)
				poll, err := s.BeginManagementPoll(m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "live-issue", now)
				if err != nil {
					t.Fatal(err)
				}
				if issued {
					if err := s.MarkManagementPollIssued(m.ProjectID, m.CollectorID, "instance-1", poll.ID, poll.Generation, now); err != nil {
						t.Fatal(err)
					}
				}
				switch mutation {
				case "monitor-stop":
					err = s.StopRunMonitors(m.ProjectID, "monitor-run", now)
				case "terminal-status":
					err = s.UpdateTestStatus("monitor-run", "COMPLETE", &now, "")
				case "deleted":
					err = s.DeleteTest("monitor-run")
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := s.MarkManagementPollIssued(m.ProjectID, m.CollectorID, "instance-1", poll.ID, poll.Generation, now); !errors.Is(err, ErrManagementPollUnavailable) {
					t.Fatalf("terminal cohort reissued collection authority: %v", err)
				}
				current, err := s.GetManagementPoll(m.ProjectID, poll.ID)
				if err != nil || (current.GrantIssuedAt != nil) != issued {
					t.Fatalf("failed issuance changed grant boundary: %#v %v", current, err)
				}
			})
		}
	}
	s, m, now := managementFixture(t)
	addManagementRun(t, s, m, "monitor-run-2", now)
	poll, err := s.BeginManagementPoll(m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, "live-survivor", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StopRunMonitors(m.ProjectID, "monitor-run", now); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkManagementPollIssued(m.ProjectID, m.CollectorID, "instance-1", poll.ID, poll.Generation, now); err != nil {
		t.Fatalf("live pinned peer lost authority: %v", err)
	}
}

func TestManagementJournalRetentionPreservesOnlyLiveFairnessCursors(t *testing.T) {
	s, m, now := managementFixture(t)
	polls := []*ManagementPoll{}
	for _, nonce := range []string{"old", "latest"} {
		poll, err := beginIssuedManagementPoll(s, m.ProjectID, m.CollectorID, "instance-1", "monitor-run:"+m.ID, nonce, now)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-1", poll.ID, poll.Generation, ManagementDrainProof{Kind: "completed"}, now); err != nil {
			t.Fatal(err)
		}
		polls = append(polls, poll)
	}
	before := now.Add(time.Hour)
	if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), before, 512); err != nil || scanned != 2 || pruned != 1 {
		t.Fatalf("superseded live-source journal sweep: %d %d %v", scanned, pruned, err)
	}
	if _, err := s.GetManagementPoll(m.ProjectID, polls[1].ID); err != nil {
		t.Fatalf("latest live fairness cursor removed: %v", err)
	}
	if scanned, pruned, err := s.SweepManagementPollRetention(context.Background(), before, 512); err != nil || scanned != 0 || pruned != 0 {
		t.Fatalf("terminal cursor wrap: %d %d %v", scanned, pruned, err)
	}
	if err := s.StopRunMonitors(m.ProjectID, "monitor-run", now); err != nil {
		t.Fatal(err)
	}
	identity := *m.ManagementIdentity
	identity.Principal = "new-fixture"
	m.ManagementIdentity = &identity
	updated, err := s.SaveMonitorSource(m, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m = *updated
	saveManagementReady(t, s, m, "instance-1", now)
	addManagementRun(t, s, m, "monitor-run-new", now)
	if n, err := s.PruneManagementPolls(m.ProjectID, before, 100); err != nil || n != 1 {
		t.Fatalf("obsolete configuration retained by unrelated live revision: %d %v", n, err)
	}
}

func fillManagementCollectorAssignments(t *testing.T, s *SQLiteStore, m MonitorSource, now time.Time, count int) {
	t.Helper()
	for i := 1; i < count; i++ {
		addManagementRun(t, s, m, fmt.Sprintf("capacity-run-%02d", i), now)
	}
}
func TestManagementCollectorAssignedCapacityStartAndPrepare(t *testing.T) {
	s, m, now := managementFixture(t)
	fillManagementCollectorAssignments(t, s, m, now, MaxCollectorAssignedMonitorJobs)
	candidate := &TestRecord{ID: "candidate-run", Name: "candidate", ProjectID: m.ProjectID, ScenarioID: "monitor-scenario", Status: "INITIALIZING", StartTime: now}
	if err := s.SaveTest(candidate); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateRunMonitorReadiness(m.ProjectID, candidate.ID, candidate.ScenarioID, now); err == nil || !strings.Contains(err.Error(), "collector_capacity") {
		t.Fatalf("required prepare exceeds assignment budget: %v", err)
	}
	if _, err := s.StartRunMonitors(m.ProjectID, candidate.ID, candidate.ScenarioID, now, now.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "collector_capacity") {
		t.Fatalf("required start exceeds assignment budget: %v", err)
	}
	jobs, err := s.ListRunMonitorJobs(m.ProjectID, candidate.ID)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("failed admission left visible jobs: %#v %v", jobs, err)
	}
	polled, err := s.PollRunMonitorJobs(m.ProjectID, m.CollectorID, now)
	if err != nil || len(polled) != MaxCollectorAssignedMonitorJobs {
		t.Fatalf("full assigned collector became unpollable: %d %v", len(polled), err)
	}
	if err := s.StopRunMonitors(m.ProjectID, "monitor-run", now); err != nil {
		t.Fatal(err)
	}
	jobs, err = s.StartRunMonitors(m.ProjectID, candidate.ID, candidate.ScenarioID, now, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 || jobs[0].State != "collecting" {
		t.Fatalf("confirmed stop did not release assigned slot: %#v %v", jobs, err)
	}
}
func TestManagementCollectorAssignedCapacityOptionalAndRequiredPriority(t *testing.T) {
	for _, occupied := range []int{31, 32} {
		t.Run(fmt.Sprintf("occupied-%d", occupied), func(t *testing.T) {
			s, m, now := managementFixture(t)
			fillManagementCollectorAssignments(t, s, m, now, occupied)
			second := m
			second.ID, second.Name, second.Revision = "second-monitor", "second", 0
			if _, err := s.SaveMonitorSource(second, 0); err != nil {
				t.Fatal(err)
			}
			bindings, err := s.GetMonitorBindings(m.ProjectID, "monitor-scenario")
			if err != nil {
				t.Fatal(err)
			}
			// Optional is deliberately first: at31 only the required member
			// may claim the last slot. At32 both optional members remain explicit.
			bindings.Sources = []MonitorBinding{{SourceID: m.ID}, {SourceID: second.ID, Required: occupied == 31}}
			if _, err := s.SaveMonitorBindings(*bindings, bindings.Revision); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveTest(&TestRecord{ID: "capacity-candidate", Name: "candidate", ProjectID: m.ProjectID, ScenarioID: "monitor-scenario", Status: "INITIALIZING", StartTime: now}); err != nil {
				t.Fatal(err)
			}
			if err := s.ValidateRunMonitorReadiness(m.ProjectID, "capacity-candidate", "monitor-scenario", now); err != nil {
				t.Fatal(err)
			}
			jobs, err := s.StartRunMonitors(m.ProjectID, "capacity-candidate", "monitor-scenario", now, now.Add(time.Hour))
			if err != nil || len(jobs) != 2 {
				t.Fatalf("bounded optional start: %#v %v", jobs, err)
			}
			for _, job := range jobs {
				if job.Required {
					if job.State != "collecting" || job.CoverageReason != "" {
						t.Fatalf("optional member stole required slot: %#v", job)
					}
				} else if job.State != "incomplete" || job.CoverageReason != "collector_capacity" {
					t.Fatalf("optional excess lacks explicit coverage: %#v", job)
				}
			}
			polled, err := s.PollRunMonitorJobs(m.ProjectID, m.CollectorID, now)
			if err != nil || len(polled) != occupied {
				// INITIALIZING assignments reserve capacity, then become
				// pollable only after the actual execution boundary.
				t.Fatalf("optional excess starved existing jobs: %d %v", len(polled), err)
			}
		})
	}
}

func TestManagementTerminalCohortCompactsWithoutChangingReplay(t *testing.T) {
	s, m, now := managementFixture(t)
	if err := s.StopRunMonitors(m.ProjectID, "monitor-run", now); err != nil {
		t.Fatal(err)
	}
	scope := m.ManagementScope.Clone()
	scope.Entities = []analysis.ManagementEntity{}
	for i := 0; i < 64; i++ {
		scope.Entities = append(scope.Entities, analysis.ManagementEntity{Kafka: &analysis.KafkaManagementEntity{Topic: strings.Repeat("t", 80), Partition: int32(i), Group: strings.Repeat("g", 64)}})
	}
	m.ManagementScope = &scope
	updated, err := s.SaveMonitorSource(m, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	m = *updated
	saveManagementReady(t, s, m, "instance-1", now)
	for i := 0; i < MaxCollectorAssignedMonitorJobs; i++ {
		addManagementRun(t, s, m, fmt.Sprintf("large-run-%02d", i), now)
	}
	var accepted *ManagementPoll
	var acceptedReceipts []MonitorBatchReceipt
	for _, mode := range []string{"accepted", "abandoned", "unissued"} {
		poll, err := s.BeginManagementPoll(m.ProjectID, m.CollectorID, "instance-1", "large-run-00:"+m.ID, mode, now)
		if err != nil {
			t.Fatal(err)
		}
		before, err := json.Marshal(poll)
		if err != nil || len(poll.Cohort) != MaxCollectorAssignedMonitorJobs || len(before) < 250000 {
			t.Fatalf("large immutable cohort fixture: %d members %d bytes %v", len(poll.Cohort), len(before), err)
		}
		if mode != "unissued" {
			if err := s.MarkManagementPollIssued(m.ProjectID, m.CollectorID, "instance-1", poll.ID, poll.Generation, now); err != nil {
				t.Fatal(err)
			}
		}
		switch mode {
		case "accepted":
			accepted = poll
			acceptedReceipts = managementReceipts(poll, now)
			err = s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", poll.ID, managementCore, acceptedReceipts, now)
		case "abandoned":
			// An unresolved drain is deliberately full until actual completion.
			if err := s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-1", poll.ID, poll.Generation, ManagementDrainProof{}, now); err != nil {
				t.Fatal(err)
			}
			draining, err := s.GetManagementPoll(m.ProjectID, poll.ID)
			if err != nil || draining.Cohort[0].Job.Source.ManagementScope == nil {
				t.Fatalf("unresolved drain lost full cohort: %#v %v", draining, err)
			}
			err = s.AbandonManagementPoll(m.ProjectID, m.CollectorID, "instance-1", poll.ID, poll.Generation, ManagementDrainProof{Kind: "completed"}, now)
		case "unissued":
			err = s.CancelUnissuedManagementPoll(m.ProjectID, m.CollectorID, "instance-1", poll.ID, poll.Generation, now)
		}
		if err != nil {
			t.Fatal(err)
		}
		terminal, err := s.GetManagementPoll(m.ProjectID, poll.ID)
		if err != nil {
			t.Fatal(err)
		}
		after, err := json.Marshal(terminal)
		if err != nil || len(after) >= len(before)/3 {
			t.Fatalf("terminal source duplicated across cohort: %d -> %d bytes %v", len(before), len(after), err)
		}
		if terminal.CohortHash != poll.CohortHash || managementCohortHash(terminal.Cohort) != poll.CohortHash || terminal.Source.Revision != poll.Source.Revision || terminal.ScopeHash != poll.ScopeHash || terminal.IdentityHash != poll.IdentityHash {
			t.Fatal("compaction rewrote source or signed minimal cohort")
		}
		for i, member := range terminal.Cohort {
			if member.Job.ID != poll.Cohort[i].Job.ID || member.Job.RunID != poll.Cohort[i].Job.RunID || member.Job.ProjectID != poll.ProjectID || member.Sequence != poll.Cohort[i].Sequence || member.Job.Source.ManagementScope != nil {
				t.Fatal("terminal cohort identities changed or duplicated source remains")
			}
		}
	}
	if err := s.DeleteMonitorSource(m.ProjectID, m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	for _, member := range accepted.Cohort {
		if err := s.DeleteTest(member.Job.RunID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AcceptManagementPoll(m.ProjectID, m.CollectorID, "instance-1", accepted.ID, managementCore, acceptedReceipts, now.Add(time.Hour)); err != nil {
		t.Fatalf("compacted deleted-source/run receipt replay: %v", err)
	}
}

func TestManagementCollectorAssignedCapacityTerminalStatusReleasesSlot(t *testing.T) {
	s, m, now := managementFixture(t)
	fillManagementCollectorAssignments(t, s, m, now, MaxCollectorAssignedMonitorJobs)
	if err := s.UpdateTestStatus("monitor-run", "COMPLETE", &now, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTest(&TestRecord{ID: "post-terminal-start", Name: "post terminal", ProjectID: m.ProjectID, ScenarioID: "monitor-scenario", Status: "INITIALIZING", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateRunMonitorReadiness(m.ProjectID, "post-terminal-start", "monitor-scenario", now); err != nil {
		t.Fatalf("terminal failover left phantom prepare assignment: %v", err)
	}
	jobs, err := s.StartRunMonitors(m.ProjectID, "post-terminal-start", "monitor-scenario", now, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 || jobs[0].State != "collecting" {
		t.Fatalf("terminal failover left phantom assigned capacity: %#v %v", jobs, err)
	}
}

func TestManagementTransactionQueryHonorsCancellation(t *testing.T) {
	s, _, _ := managementFixture(t)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rows, err := tx.QueryContext(ctx, `SELECT ?`, 1); !errors.Is(err, context.Canceled) { //nolint:rowserrcheck // never iterated: the query must fail
		if rows != nil {
			rows.Close()
		}
		t.Fatalf("transaction query ignored cancelled operation: %v", err)
	}
}

func TestManagementCollectorAssignedCapacityCountsBeforeFutureBarrier(t *testing.T) {
	s, m, now := managementFixture(t)
	fillManagementCollectorAssignments(t, s, m, now, MaxCollectorAssignedMonitorJobs)
	// Existing assignments are still pollable now but expire before the new
	// execution barrier. The new assignment still reserves its slot now.
	if _, err := s.db.Exec(`UPDATE run_monitor_jobs SET expires_at=? WHERE project_id=? AND collector_id=?`, now.Add(30*time.Second), m.ProjectID, m.CollectorID); err != nil {
		t.Fatal(err)
	}
	future := now.Add(45 * time.Second)
	if err := s.SaveTest(&TestRecord{ID: "future-barrier-run", Name: "future barrier", ProjectID: m.ProjectID, ScenarioID: "monitor-scenario", Status: "RUNNING", StartTime: future}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartRunMonitors(m.ProjectID, "future-barrier-run", "monitor-scenario", future, future.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "collector_capacity") {
		t.Fatalf("future barrier over-admitted current assignments: %v", err)
	}
	if jobs, err := s.ListRunMonitorJobs(m.ProjectID, "future-barrier-run"); err != nil || len(jobs) != 0 {
		t.Fatalf("rejected future start left assignments: %#v %v", jobs, err)
	}
	if jobs, err := s.PollRunMonitorJobs(m.ProjectID, m.CollectorID, now); err != nil || len(jobs) != MaxCollectorAssignedMonitorJobs {
		t.Fatalf("future barrier starved current assignments: %d %v", len(jobs), err)
	}
}
