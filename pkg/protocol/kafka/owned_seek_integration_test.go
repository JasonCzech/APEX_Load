//go:build kafka_real && kafka_seek_integration

package kafka

import (
	"context"
	"math"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Qualifies the configured genuine broker and pinned SDK for read-uncommitted
// owned seek only. Read-committed LSO/transactions, failover, and other broker
// versions require separate actual-provider evidence.
func TestKafkaOwnedSeekActualBroker(t *testing.T) {
	settings, tlsConfig := loadOwnedSeekFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	proxy := newOwnedSeekProxy(t, ctx, settings, tlsConfig)
	oracle := newOwnedSeekOracle(t, ctx, settings, tlsConfig)
	instance, client := newOwnedSeekExecutor(t, ctx, settings, settings.Group, "primary")
	cl, err := client.handle()
	if err != nil {
		t.Fatal("production seek client unavailable")
	}
	markers := map[int32]map[int64]string{0: {}, 1: {}, 2: {}}
	seed := func(t *testing.T, count int) {
		t.Helper()
		for partition := int32(0); partition < 3; partition++ {
			for range count {
				marker := "owned-seek-" + uuid.NewString()
				meta, err := client.Produce(ctx, settings.Topic, kafkaRecord{Value: []byte(marker), Partition: partition, ManualPartition: true})
				if err != nil || !ownedSeekActualProduceAcknowledgement(meta, settings.Topic, partition, int64(len(markers[partition]))) {
					t.Fatal("genuine production marker acknowledgement failed")
				}
				if _, exists := markers[partition][meta.Offset]; exists {
					t.Fatal("broker acknowledged a duplicate marker offset")
				}
				markers[partition][meta.Offset] = marker
			}
		}
	}
	consumeAt := func(t *testing.T, partition int32, offset int64) {
		t.Helper()
		pauseOwnedSeekExcept(cl, settings.Topic, partition)
		rec, _, err := client.Consume(ctx, settings.Topic, settings.Group, 8*time.Second)
		if err != nil || rec.Topic != settings.Topic || rec.Partition != partition || rec.Offset != offset || string(rec.Value) != markers[partition][offset] || markers[partition][offset] == "" {
			t.Fatalf("production cursor/marker mismatch on partition %d: offset=%d want=%d (error=%t)", partition, rec.Offset, offset, err != nil)
		}
	}
	primeAt := func(t *testing.T, offset int64) {
		t.Helper()
		for partition := int32(0); partition < 3; partition++ {
			consumeAt(t, partition, offset)
		}
		cl.ResumeFetchPartitions(map[string][]int32{settings.Topic: {0, 1, 2}})
		owned, generation := ownedSeekOwnership(client, settings.Topic)
		if len(owned) != 3 || generation == 0 {
			t.Fatal("genuine assignment did not own all three partitions")
		}
	}
	baseline := map[int32]int64{0: -1, 1: -1, 2: -1}
	oracle.AssertOffsets(t, ctx, settings.Group, settings.Topic, baseline)
	oracle.AssertOffsets(t, ctx, settings.PeerGroup, settings.Topic, baseline)
	seed(t, 8)
	for partition := int32(0); partition < 3; partition++ {
		if len(markers[partition]) != 8 || markers[partition][0] == "" || markers[partition][7] == "" {
			t.Fatal("genuine seek fixture must begin empty and retain contiguous records")
		}
	}

	t.Run("earliest_latest_exact", func(t *testing.T) {
		// Establish real committed baselines exclusively through the production
		// explicit delivery boundary. Neither seed setup nor the oracle commits.
		primeAt(t, 0)
		if err := client.Commit(ctx); err != nil {
			t.Fatal("production committed baseline failed")
		}
		baseline = map[int32]int64{0: 1, 1: 1, 2: 1}
		oracle.AssertOffsets(t, ctx, settings.Group, settings.Topic, baseline)
		primeAt(t, 1)
		waitOwnedSeek(t, ctx, 3*time.Second, "real SDK buffered following records", func() bool { return cl.BufferedFetchRecords() > 0 })
		buffered := cl.BufferedFetchRecords()
		// Obtain an actual broker-fetched record and queue it through the same
		// bounded pending adapter used by production delivery. No invented record,
		// offset, assignment callback or ownership is used by this helper.
		pauseOwnedSeekExcept(cl, settings.Topic, 2)
		fetched := cl.PollRecords(ctx, 1)
		records := fetched.Records()
		if len(records) != 1 || records[0].Partition != 2 || records[0].Offset != 2 || string(records[0].Value) != markers[2][2] {
			t.Fatal("genuine pending-record source did not follow delivered cursor")
		}
		highWatermark := int64(-1)
		fetched.EachPartition(func(p kgo.FetchTopicPartition) {
			if p.Topic == settings.Topic && p.Partition == 2 {
				highWatermark = p.HighWatermark
			}
		})
		client.pendingMu.Lock()
		pendingErr := client.addPendingLocked(fromKgoRecord(records[0]), highWatermark)
		pendingCount := len(client.pending)
		client.pendingMu.Unlock()
		if pendingErr != nil || pendingCount != 1 {
			t.Fatal("genuine fetched pending record was not admitted")
		}
		cl.ResumeFetchPartitions(map[string][]int32{settings.Topic: {0, 1, 2}})
		seekOwnedActual(t, ctx, instance, "earliest")
		client.pendingMu.Lock()
		remainingPending := len(client.pending)
		client.pendingMu.Unlock()
		if remainingPending != 0 || len(client.consumedRecordsSnapshot()) != 0 {
			t.Fatal("successful seek retained buffered delivery or commit tracking")
		}
		primeAt(t, 0)
		oracle.AssertOffsets(t, ctx, settings.Group, settings.Topic, baseline)
		// Cursor equality with the committed baseline is still a previously polled
		// owned cursor; the SDK's UncommittedOffsets difference must not erase it.
		seekOwnedActual(t, ctx, instance, "3")
		primeAt(t, 3)
		oracle.AssertOffsets(t, ctx, settings.Group, settings.Topic, baseline)
		seekOwnedActual(t, ctx, instance, "latest")
		pauseOwnedSeekExcept(cl, settings.Topic, 0)
		rec, _, err := client.Consume(ctx, settings.Topic, settings.Group, 300*time.Millisecond)
		if err == nil || rec.Topic != "" || rec.Value != nil {
			t.Fatal("latest seek delivered an old buffered record")
		}
		oracle.AssertOffsets(t, ctx, settings.Group, settings.Topic, baseline)
		seed(t, 1)
		primeAt(t, 8)
		oracle.AssertOffsets(t, ctx, settings.Group, settings.Topic, baseline)
		if proxy.Counts()[2] < 2 {
			t.Fatal("genuine earliest/latest broker lookups were not observed")
		}
		t.Logf("genuine multi-partition seeks retained committed offsets %v; following SDK records buffered=%d; one real adapter pending record was purged", baseline, buffered)
	})
	if t.Failed() {
		return
	}

	t.Run("unpolled_unowned_refusal", func(t *testing.T) {
		_, fresh := newOwnedSeekExecutor(t, ctx, settings, settings.PeerGroup, "unpolled")
		before := proxy.Counts()
		if err := fresh.Seek(ctx, settings.Topic, settings.PeerGroup, "earliest"); err == nil {
			t.Fatal("unpolled client claimed seek ownership")
		}
		after := proxy.Counts()
		if after[2] != before[2] || after[11] != before[11] || after[14] != before[14] {
			t.Fatal("unpolled seek performed lookup or joined a group")
		}
		if err := client.Seek(ctx, settings.UnpolledTopic, settings.Group, "earliest"); err == nil {
			t.Fatal("unowned topic accepted seek")
		}
		freshHandle, err := fresh.handle()
		if err != nil {
			t.Fatal("unpolled client unavailable")
		}
		pauseOwnedSeekExcept(freshHandle, settings.Topic, 0)
		rec, _, err := fresh.Consume(ctx, settings.Topic, settings.PeerGroup, 8*time.Second)
		if err != nil || rec.Partition != 0 || rec.Offset != 0 || string(rec.Value) != markers[0][0] {
			t.Fatal("partial real polling fixture failed")
		}
		owned, _ := ownedSeekOwnership(fresh, settings.Topic)
		if len(owned) != 3 || len(freshHandle.UncommittedOffsets()[settings.Topic]) != 1 {
			t.Fatal("fixture did not retain genuinely owned but unpolled partitions")
		}
		before = proxy.Counts()
		if err := fresh.Seek(ctx, settings.Topic, settings.PeerGroup, "earliest"); err == nil {
			t.Fatal("partially polled owned set accepted seek")
		}
		after = proxy.Counts()
		if after[2] != before[2] {
			t.Fatal("partially polled refusal reached broker lookup")
		}
		oracle.AssertOffsets(t, ctx, settings.PeerGroup, settings.Topic, map[int32]int64{0: -1, 1: -1, 2: -1})
		oracle.AssertOffsets(t, ctx, settings.Group, settings.Topic, baseline)
		if err := fresh.Teardown(ctx); err != nil {
			t.Fatal("partial polling client teardown failed")
		}
	})
	if t.Failed() {
		return
	}

	t.Run("commit_group_fence", func(t *testing.T) {
		delivered := ownedSeekDelivered(client)
		if len(delivered) != 3 {
			t.Fatal("group-fence fixture lacks delivered offsets")
		}
		before := proxy.Counts()
		for _, group := range []string{settings.PeerGroup, settings.DeniedGroup} {
			result, err := instance.Execute(ctx, &protocol.Action{Name: "refuse-unconfigured-group", Type: "kafka_commit", Params: map[string]any{"consumer_group": group}})
			if err != nil || result == nil || result.Success || result.ServerTelemetry != nil && result.ServerTelemetry.CommitCount != 0 {
				t.Fatal("unconfigured action group did not fail before commit")
			}
			if !reflect.DeepEqual(ownedSeekDelivered(client), delivered) {
				t.Fatal("rejected commit changed delivered bookkeeping")
			}
		}
		if proxy.Counts()[8] != before[8] {
			t.Fatal("rejected action group wrote broker commits")
		}
		oracle.AssertOffsets(t, ctx, settings.Group, settings.Topic, baseline)
		oracle.AssertOffsets(t, ctx, settings.PeerGroup, settings.Topic, map[int32]int64{0: -1, 1: -1, 2: -1})
		commitOwnedActual(t, ctx, instance, map[string]any{"consumer_group": settings.Group})
		baseline = map[int32]int64{0: 9, 1: 9, 2: 9}
		oracle.AssertOffsets(t, ctx, settings.Group, settings.Topic, baseline)
		seed(t, 4)
		primeAt(t, 9)
		commitOwnedActual(t, ctx, instance, nil)
		baseline = map[int32]int64{0: 10, 1: 10, 2: 10}
		oracle.AssertOffsets(t, ctx, settings.Group, settings.Topic, baseline)
		primeAt(t, 10)
	})
	if t.Failed() {
		return
	}

	t.Run("revocation_during_lookup", func(t *testing.T) {
		owned, generation := ownedSeekOwnership(client, settings.Topic)
		if len(owned) != 3 || len(cl.UncommittedOffsets()[settings.Topic]) != 3 {
			t.Fatal("revocation fixture lacks a complete genuinely polled owner")
		}
		// Hold the broker's real rebalance signal before starting the lookup.
		// The pinned SDK shares its normal connection for heartbeat and lookup;
		// holding lookup first would prevent it learning that the peer joined.
		heartbeat := proxy.HoldRebalanceHeartbeat(t)
		t.Cleanup(heartbeat.Release)
		_, peer := newOwnedSeekExecutor(t, ctx, settings, settings.Group, "rebalance-peer")
		peerCtx, stopPeer := context.WithTimeout(ctx, 8*time.Second)
		defer stopPeer()
		peerExited := make(chan struct{})
		t.Cleanup(func() {
			stopPeer()
			heartbeat.Release()
			if err := peer.Teardown(context.Background()); err != nil {
				t.Error("peer executor cleanup failed")
			}
			joinOwnedSeekOperation(t, peerExited, "peer real polling")
		})
		go func() {
			defer close(peerExited)
			_, _, _ = peer.Consume(peerCtx, settings.Topic, settings.Group, 7*time.Second)
		}()
		var held *ownedSeekHeldResponse
		var seekExited chan struct{}
		failureReported := false
		failureSnapshot := func() {
			if !t.Failed() || failureReported {
				return
			}
			failureReported = true
			current, currentGeneration := ownedSeekOwnership(client, settings.Topic)
			assigned, peerGeneration := ownedSeekOwnership(peer, settings.Topic)
			released := false
			if held != nil {
				select {
				case <-held.release:
					released = true
				default:
				}
			}
			lookupExited := false
			select {
			case <-seekExited:
				lookupExited = true
			default:
			}
			t.Logf("bounded revocation state before cleanup: initial generation=%d current generation=%d current=%v peer generation=%d peer=%v release requested=%t lookup exited=%t API counts=%v", generation, currentGeneration, current, peerGeneration, assigned, released, lookupExited, proxy.Counts())
		}
		t.Cleanup(failureSnapshot)
		select {
		case <-heartbeat.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("genuine primary rebalance heartbeat never entered bounded hold")
		}
		current, currentGeneration := ownedSeekOwnership(client, settings.Topic)
		if currentGeneration != generation || !reflect.DeepEqual(current, owned) {
			t.Fatal("primary ownership changed before genuine rebalance signal was delivered")
		}
		held = proxy.HoldEarliest(t)
		t.Cleanup(held.Release)
		lookupCtx, stopLookup := context.WithTimeout(ctx, 10*time.Second)
		defer stopLookup()
		seekDone := make(chan error, 1)
		seekExited = make(chan struct{})
		t.Cleanup(func() {
			stopLookup()
			heartbeat.Release()
			held.Release()
			if err := client.Teardown(context.Background()); err != nil {
				t.Error("held seek executor cleanup failed")
			}
			joinOwnedSeekOperation(t, seekExited, "held seek")
		})
		go func() {
			defer close(seekExited)
			seekDone <- client.Seek(lookupCtx, settings.Topic, settings.Group, "earliest")
		}()
		t.Cleanup(failureSnapshot)
		select {
		case <-held.entered:
		case err := <-seekDone:
			t.Fatalf("seek ended before genuine held broker response (error=%t)", err != nil)
		case <-time.After(5 * time.Second):
			t.Fatal("genuine ListOffsets response never entered bounded hold")
		}
		heartbeat.Release()
		var revoked, peerOwned map[int32]uint64
		var revokedGeneration uint64
		waitOwnedSeek(t, ctx, 5*time.Second, "broker-driven primary revocation during lookup", func() bool {
			current, currentGeneration := ownedSeekOwnership(client, settings.Topic)
			if currentGeneration == generation || len(current) >= 3 {
				return false
			}
			revoked, revokedGeneration = current, currentGeneration
			return true
		})
		// Snapshot only after the genuine callback has discarded revoked state.
		remainingDelivered := ownedSeekDelivered(client)
		before := proxy.Counts()
		held.Release()
		select {
		case err := <-seekDone:
			if err == nil || !strings.Contains(err.Error(), "ownership changed during offset lookup") {
				t.Fatal("seek crossed genuine revoked generation or returned an unrelated failure")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("held seek did not finish after response release")
		}
		joinOwnedSeekOperation(t, seekExited, "held seek")
		if !reflect.DeepEqual(ownedSeekDelivered(client), remainingDelivered) {
			t.Fatal("failed seek erased surviving delivery bookkeeping")
		}
		if proxy.Counts()[8] != before[8] {
			t.Fatal("revoked seek wrote broker committed offsets")
		}
		oracle.AssertOffsets(t, ctx, settings.Group, settings.Topic, baseline)
		// Completing the actual peer handoff may need follow-up group I/O on
		// the normal connection. Require it after releasing the held lookup.
		waitOwnedSeek(t, ctx, 5*time.Second, "genuine disjoint peer assignment after lookup refusal", func() bool {
			current, _ := ownedSeekOwnership(client, settings.Topic)
			assigned, _ := ownedSeekOwnership(peer, settings.Topic)
			if len(assigned) == 0 {
				return false
			}
			for partition := range assigned {
				if _, originallyOwned := owned[partition]; !originallyOwned {
					return false
				}
				if _, stillOwned := current[partition]; stillOwned {
					return false
				}
				if _, notRevoked := revoked[partition]; notRevoked {
					return false
				}
			}
			peerOwned = assigned
			return true
		})
		stopPeer()
		joinOwnedSeekOperation(t, peerExited, "peer real polling")
		t.Logf("genuine broker ownership changed generation %d→%d; remaining=%v peer=%v; held seek refused without committed-offset changes", generation, revokedGeneration, revoked, peerOwned)
	})
}

func newOwnedSeekExecutor(t *testing.T, ctx context.Context, f ownedSeekFixture, group, suffix string) (*Kafka, *franzClient) {
	t.Helper()
	endpoint, _ := url.Parse(f.Endpoint)
	instance := &Kafka{name: "kafka"}
	err := instance.Init(ctx, map[string]any{
		"kafka_mock": false, "kafka_brokers": []string{endpoint.Host}, "kafka_topic": f.Topic, "kafka_consumer_group": group,
		"kafka_security_protocol": "SASL_SSL", "kafka_sasl_mechanism": f.Mechanism, "kafka_sasl_username": f.Username, "kafka_sasl_password": f.Password,
		"kafka_tls_ca_cert_file": f.CAFile, "kafka_auto_offset_reset": "earliest", "kafka_isolation_level": "read_uncommitted",
		"kafka_client_id": "apex-owned-seek-" + suffix, "kafka_timeout_ms": 10000,
	})
	if err != nil {
		t.Fatal("production seek executor initialization failed")
	}
	client, ok := instance.client.(*franzClient)
	if !ok || instance.cfg.Mock {
		t.Fatal("qualification did not construct the real Kafka adapter")
	}
	t.Cleanup(func() {
		if err := instance.Teardown(context.Background()); err != nil {
			t.Error("production seek executor teardown failed")
		}
	})
	return instance, client
}
func pauseOwnedSeekExcept(cl *kgo.Client, topic string, selected int32) {
	cl.ResumeFetchPartitions(map[string][]int32{topic: {0, 1, 2}})
	others := []int32{}
	for partition := int32(0); partition < 3; partition++ {
		if partition != selected {
			others = append(others, partition)
		}
	}
	cl.PauseFetchPartitions(map[string][]int32{topic: others})
}
func ownedSeekOwnership(c *franzClient, topic string) (map[int32]uint64, uint64) {
	c.assignmentMu.RLock()
	defer c.assignmentMu.RUnlock()
	out := map[int32]uint64{}
	for p, g := range c.ownedPartitions[topic] {
		out[p] = g
	}
	return out, c.assignmentGeneration
}
func ownedSeekDelivered(c *franzClient) map[int32]int64 {
	out := map[int32]int64{}
	for _, r := range c.consumedRecordsSnapshot() {
		out[r.Partition] = r.Offset
	}
	return out
}
func seekOwnedActual(t *testing.T, ctx context.Context, k *Kafka, offset string) {
	t.Helper()
	result, err := k.Execute(ctx, &protocol.Action{Name: "owned-real-seek", Type: "kafka_seek", Params: map[string]any{"offset": offset}})
	if err != nil || result == nil || !result.Success || result.Extractions["kafka_seek_offset"] != offset {
		t.Fatalf("production seek failed for target %s (error=%t)", offset, err != nil)
	}
}
func commitOwnedActual(t *testing.T, ctx context.Context, k *Kafka, params map[string]any) {
	t.Helper()
	result, err := k.Execute(ctx, &protocol.Action{Name: "configured-real-commit", Type: "kafka_commit", Params: params})
	if err != nil || result == nil || !result.Success || result.ServerTelemetry == nil || result.ServerTelemetry.CommitCount != 1 {
		t.Fatal("configured production action commit did not succeed")
	}
}
func waitOwnedSeek(t *testing.T, ctx context.Context, budget time.Duration, description string, ready func() bool) {
	t.Helper()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("qualification context ended before %s", description)
		case <-timer.C:
			t.Fatalf("bounded genuine fixture did not observe %s", description)
		case <-ticker.C:
		}
	}
}

// Completion signals are separate from result channels so both assertion and
// cleanup paths can prove the same goroutine has exited without consuming twice.
func joinOwnedSeekOperation(t *testing.T, exited <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Errorf("%s operation did not physically join", description)
	}
}

// Produce acknowledgement metadata is distinct from request/reply's private
// sent flag. Actual marker contents are independently checked on broker reads.
func ownedSeekActualProduceAcknowledgement(meta recordMeta, topic string, partition int32, offset int64) bool {
	return meta.AcknowledgementMs != nil && !math.IsNaN(*meta.AcknowledgementMs) && !math.IsInf(*meta.AcknowledgementMs, 0) && *meta.AcknowledgementMs >= 0 && meta.Topic == topic && meta.Partition == partition && meta.Offset == offset && meta.Offset >= 0 && !meta.Timestamp.IsZero()
}

func TestOwnedSeekProduceAcknowledgementAdmission(t *testing.T) {
	zero := 0.0
	base := recordMeta{AcknowledgementMs: &zero, Topic: "orders", Partition: 2, Offset: 7, Timestamp: time.Unix(1700000000, 0)}
	if !ownedSeekActualProduceAcknowledgement(base, "orders", 2, 7) || base.sent {
		t.Fatal("valid production acknowledgement requires a request/reply flag")
	}
	for _, tc := range []struct {
		name  string
		alter func(*recordMeta)
	}{
		{"missing-ack", func(m *recordMeta) { m.AcknowledgementMs = nil }},
		{"negative-ack", func(m *recordMeta) { v := -1.0; m.AcknowledgementMs = &v }},
		{"nan-ack", func(m *recordMeta) { v := math.NaN(); m.AcknowledgementMs = &v }},
		{"infinite-ack", func(m *recordMeta) { v := math.Inf(1); m.AcknowledgementMs = &v }},
		{"wrong-topic", func(m *recordMeta) { m.Topic = "other" }},
		{"wrong-partition", func(m *recordMeta) { m.Partition = 1 }},
		{"wrong-offset", func(m *recordMeta) { m.Offset = 8 }},
		{"unacknowledged-offset", func(m *recordMeta) { m.Offset = -1 }},
		{"missing-timestamp", func(m *recordMeta) { m.Timestamp = time.Time{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := base
			tc.alter(&meta)
			meta.sent = true // A request/reply flag cannot replace broker evidence.
			if ownedSeekActualProduceAcknowledgement(meta, "orders", 2, 7) {
				t.Fatal("invalid broker acknowledgement admitted")
			}
		})
	}
}
