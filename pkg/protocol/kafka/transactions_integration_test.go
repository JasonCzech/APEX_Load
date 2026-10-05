//go:build kafka_real && kafka_transaction_integration

package kafka

import (
	"context"
	"crypto/tls"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"strings"
)

// Genuine single-broker, fresh-source qualification only. Independent principals
// reconcile positions and visible output; synthetic wire tests are not EOS proof.
func TestKafkaTransactionsActualBroker(t *testing.T) {
	f, trust := loadTransactionFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	proxy := newOwnedSeekProxy(t, ctx, f.transport(), trust)
	proxy.mu.Lock()
	proxy.transactions = true
	proxy.mu.Unlock()
	oracle := newTransactionOracle(t, ctx, f, trust)
	writer := newTransactionDirect(t, ctx, f, trust, "writer", "", kgo.RecordPartitioner(kgo.ManualPartitioner()))
	outputs := map[int64]string{}
	scan := func(t *testing.T) {
		t.Helper()
		barrier := "output-barrier-" + uuid.NewString()
		offset := transactionProduce(t, ctx, writer, f.OutputTopic, barrier)
		outputs[offset] = barrier
		hw, lso := oracle.Next(t, ctx, f.OutputTopic, 0), oracle.Next(t, ctx, f.OutputTopic, 1)
		if hw != lso || lso != offset+1 {
			t.Fatal("output barrier did not establish a closed actual stable boundary")
		}
		actual := transactionVisibleOutput(t, ctx, f, trust, offset, barrier)
		if !reflect.DeepEqual(actual, outputs) {
			t.Fatalf("read_committed output differs from independently acknowledged expected population: offsets=%v expected=%v", transactionMapKeys(actual), transactionMapKeys(outputs))
		}
	}
	if !t.Run("read_committed_latest_lso", func(t *testing.T) {
		instance, client := newTransactionExecutor(t, ctx, f, f.VisibilityTopic, f.SeekGroup, "seek", false)
		primer := "lso-primer-" + uuid.NewString()
		primerOffset := transactionProduce(t, ctx, writer, f.VisibilityTopic, primer)
		rec, _, err := client.Consume(ctx, f.VisibilityTopic, f.SeekGroup, 8*time.Second)
		if err != nil || rec.Offset != primerOffset || string(rec.Value) != primer {
			t.Fatal("read_committed executor did not actually poll its owned primer")
		}
		baseline := oracle.Committed(t, ctx, f.SeekGroup, f.VisibilityTopic)
		tx := newTransactionDirect(t, ctx, f, trust, "writer", f.TransactionalPrefix+"visibility", kgo.RecordPartitioner(kgo.ManualPartitioner()))
		if tx.BeginTransaction() != nil {
			t.Fatal("actual visibility transaction begin failed")
		}
		openMarker := "lso-open-" + uuid.NewString()
		openOffset := transactionProduce(t, ctx, tx, f.VisibilityTopic, openMarker)
		hw, lso := oracle.Next(t, ctx, f.VisibilityTopic, 0), oracle.Next(t, ctx, f.VisibilityTopic, 1)
		if lso != openOffset || hw <= lso {
			t.Fatalf("actual open transaction did not separate HW/LSO: hw=%d lso=%d record=%d", hw, lso, openOffset)
		}
		before := proxy.Counts()[8]
		transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_seek", Params: map[string]any{"offset": "latest"}})
		if proxy.Counts()[8] != before || oracle.Committed(t, ctx, f.SeekGroup, f.VisibilityTopic) != baseline {
			t.Fatal("LSO seek committed offsets")
		}
		transactionEnd(t, ctx, tx, kgo.TryCommit)
		rec, _, err = client.Consume(ctx, f.VisibilityTopic, f.SeekGroup, 8*time.Second)
		if err != nil || rec.Offset != lso || string(rec.Value) != openMarker {
			t.Fatal("latest read_committed seek missed the record at the actual previous LSO")
		}
		if tx.BeginTransaction() != nil {
			t.Fatal("actual aborted visibility transaction begin failed")
		}
		aborted := "lso-aborted-" + uuid.NewString()
		abortedOffset := transactionProduce(t, ctx, tx, f.VisibilityTopic, aborted)
		hw, lso = oracle.Next(t, ctx, f.VisibilityTopic, 0), oracle.Next(t, ctx, f.VisibilityTopic, 1)
		if lso != abortedOffset || hw <= lso {
			t.Fatal("second open transaction did not establish actual LSO")
		}
		transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_seek", Params: map[string]any{"offset": "latest"}})
		transactionEnd(t, ctx, tx, kgo.TryAbort)
		following := "lso-after-abort-" + uuid.NewString()
		followingOffset := transactionProduce(t, ctx, writer, f.VisibilityTopic, following)
		rec, _, err = client.Consume(ctx, f.VisibilityTopic, f.SeekGroup, 8*time.Second)
		if err != nil || rec.Offset != followingOffset || string(rec.Value) != following {
			t.Fatal("read_committed seek leaked aborted data or missed following acknowledged data")
		}
		if oracle.Committed(t, ctx, f.SeekGroup, f.VisibilityTopic) != baseline || proxy.Counts()[8] != before {
			t.Fatal("read_committed seek/consume changed committed offsets")
		}
		t.Logf("actual read_committed latest delivered prior LSO offset=%d; skipped aborted offset=%d to following=%d; broker commits unchanged", openOffset, abortedOffset, followingOffset)
	}) {
		return
	}
	inputMarkers := []string{"input-first-" + uuid.NewString(), "input-second-" + uuid.NewString(), "input-third-" + uuid.NewString()}
	inputOffsets := make([]int64, len(inputMarkers))
	for i, marker := range inputMarkers {
		inputOffsets[i] = transactionProduce(t, ctx, writer, f.InputTopic, marker)
	}
	if !t.Run("consume_transform_commit_abort", func(t *testing.T) {
		instance, _ := newTransactionExecutor(t, ctx, f, f.InputTopic, f.Group, "eos", true)
		baseline := oracle.Committed(t, ctx, f.Group, f.InputTopic)
		begin := func() { transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_begin_transaction"}) }
		consume := func(index int) {
			result := transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_consume", Assertions: []protocol.Assertion{{Type: "body_contains", Value: inputMarkers[index]}}})
			if result.Extractions["kafka_offset"] != strconv.FormatInt(inputOffsets[index], 10) {
				t.Fatal("transaction input cursor identity mismatch")
			}
		}
		produce := func(marker string) {
			transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_produce", Params: map[string]any{"topic": f.OutputTopic}, Body: []byte(marker)})
		}
		begin()
		consume(0)
		committedMarker := "eos-committed-" + uuid.NewString()
		produce(committedMarker)
		if oracle.Committed(t, ctx, f.Group, f.InputTopic) != baseline {
			t.Fatal("open transform transaction committed its input")
		}
		for _, action := range []*protocol.Action{{Type: "kafka_commit"}, {Type: "kafka_seek", Params: map[string]any{"offset": "earliest"}}} {
			r, err := instance.Execute(ctx, action)
			if err != nil || r == nil || r.Success {
				t.Fatal("consumer transaction admitted out-of-transaction commit/seek")
			}
		}
		commit := transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_commit_transaction"})
		actualCommitted := oracle.Committed(t, ctx, f.Group, f.InputTopic)
		commitCount := -1
		if commit.ServerTelemetry != nil {
			commitCount = commit.ServerTelemetry.CommitCount
		}
		if commitCount != 1 || actualCommitted != inputOffsets[0]+1 {
			t.Fatalf("actual transform commit mismatch: commit_count=%d stable_input_next=%d expected=%d", commitCount, actualCommitted, inputOffsets[0]+1)
		}
		// Resolve the single committed output offset with the independent reader.
		committedOffset := transactionFindOutput(t, ctx, f, trust, committedMarker)
		outputs[committedOffset] = committedMarker
		scan(t)
		begin()
		consume(1)
		produce("eos-aborted-" + uuid.NewString())
		abort := transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_abort_transaction"})
		abortCount := -1
		if abort.ServerTelemetry != nil {
			abortCount = abort.ServerTelemetry.AbortCount
		}
		abortCommitted := oracle.Committed(t, ctx, f.Group, f.InputTopic)
		if abortCount != 1 || abortCommitted != inputOffsets[0]+1 {
			t.Fatalf("actual abort mismatch: abort_count=%d stable_input_next=%d expected=%d", abortCount, abortCommitted, inputOffsets[0]+1)
		}
		scan(t)
		begin()
		consume(1) // Genuine SDK rewind, no synthetic cursor reset.
		recoveryMarker := "eos-after-abort-" + uuid.NewString()
		produce(recoveryMarker)
		transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_commit_transaction"})
		if oracle.Committed(t, ctx, f.Group, f.InputTopic) != inputOffsets[1]+1 {
			t.Fatal("abort recovery failed exact input committed-next")
		}
		outputs[transactionFindOutput(t, ctx, f, trust, recoveryMarker)] = recoveryMarker
		scan(t)
		t.Logf("actual consume-transform commit_count=%d input_next=%d expected=%d; buffered-output abort_count=%d preserved_next=%d; actual SDK re-delivery committed-next=%d and read_committed output reconciled", commitCount, actualCommitted, inputOffsets[0]+1, abortCount, abortCommitted, inputOffsets[1]+1)
	}) {
		return
	}
	t.Run("stale_producer_fencing", func(t *testing.T) {
		instance, client := newTransactionExecutor(t, ctx, f, f.InputTopic, f.FenceGroup, "fence", true)
		consume := func(index int) {
			r := transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_consume", Assertions: []protocol.Assertion{{Type: "body_contains", Value: inputMarkers[index]}}})
			if r.Extractions["kafka_offset"] != strconv.FormatInt(inputOffsets[index], 10) {
				t.Fatal("fencing input cursor mismatch")
			}
		}
		transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_begin_transaction"})
		consume(0)
		warmup := "fence-warmup-" + uuid.NewString()
		transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_produce", Params: map[string]any{"topic": f.OutputTopic}, Body: []byte(warmup)})
		transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_commit_transaction"})
		outputs[transactionFindOutput(t, ctx, f, trust, warmup)] = warmup
		scan(t)
		committed := oracle.Committed(t, ctx, f.FenceGroup, f.InputTopic)
		if committed != inputOffsets[0]+1 {
			t.Fatal("actual producer epoch warmup did not commit its exact input")
		}
		transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_begin_transaction"})
		consume(1)
		transactionExecute(t, ctx, instance, &protocol.Action{Type: "kafka_produce", Params: map[string]any{"topic": f.OutputTopic}, Body: []byte("stale-fenced-output-" + uuid.NewString())})
		// Actual resolved fresh-source ID, after its epoch was initialized above.
		// A separate broker client fences it; production identity is never mutated.
		contender := newTransactionDirect(t, ctx, f, trust, "writer", client.cfg.TransactionalID, kgo.RecordPartitioner(kgo.ManualPartitioner()))
		if contender.BeginTransaction() != nil {
			t.Fatal("genuine fencing contender begin failed")
		}
		transactionProduce(t, ctx, contender, f.OutputTopic, "fencing-contender-aborted-"+uuid.NewString())
		transactionEnd(t, ctx, contender, kgo.TryAbort)
		result, err := instance.Execute(ctx, &protocol.Action{Type: "kafka_commit_transaction"})
		if err != nil || result == nil || result.Success || result.ServerTelemetry != nil && result.ServerTelemetry.CommitCount != 0 {
			t.Fatal("stale broker-fenced producer reported a committed transaction")
		}
		fenceCode := transactionFencingFailureCode(result.ErrorMessage)
		if fenceCode == 0 {
			t.Fatal("stale producer failed without an actual pinned SDK fencing error")
		}
		if oracle.Committed(t, ctx, f.FenceGroup, f.InputTopic) != committed {
			t.Fatal("stale producer failure committed its input")
		}
		scan(t)
		next, err := instance.Execute(ctx, &protocol.Action{Type: "kafka_begin_transaction"})
		if err != nil || next == nil || next.Success {
			t.Fatal("broker-fenced source resumed execution")
		}
		if oracle.Committed(t, ctx, f.FenceGroup, f.InputTopic) != committed {
			t.Fatal("fenced retry changed committed input")
		}
		t.Logf("actual independent producer epoch fenced stale runtime (Kafka code=%d); failed/aborted output invisible and input-next=%d unchanged; fresh-source termination only", fenceCode, committed)
	})
}

// Scan reaches an independently acknowledged final barrier. Absence is proven
// by ordered traversal to that offset, never by an empty poll or elapsed wait.
func transactionVisibleOutput(t *testing.T, ctx context.Context, f transactionFixture, trust *tls.Config, barrierOffset int64, barrier string) map[int64]string {
	t.Helper()
	reader := newTransactionDirect(t, ctx, f, trust, "output", "", kgo.FetchIsolationLevel(kgo.ReadCommitted()), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{f.OutputTopic: {0: kgo.NewOffset().AtStart()}}))
	readCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	records := map[int64]string{}
	for len(records) < 64 {
		fetches := reader.PollRecords(readCtx, 1)
		if len(fetches.Errors()) > 0 {
			t.Fatal("independent read_committed output fetch failed")
		}
		for _, r := range fetches.Records() {
			if r.Topic != f.OutputTopic || r.Partition != 0 || r.Offset < 0 || r.Offset > barrierOffset {
				t.Fatal("independent output escaped exact barrier inventory")
			}
			if _, exists := records[r.Offset]; exists {
				t.Fatal("independent output duplicated broker offset")
			}
			records[r.Offset] = string(r.Value)
			if r.Offset == barrierOffset {
				if string(r.Value) != barrier {
					t.Fatal("output barrier marker mismatch")
				}
				reader.Close()
				return records
			}
		}
		if readCtx.Err() != nil {
			t.Fatal("independent output did not reach actual acknowledged stable barrier")
		}
	}
	t.Fatal("independent output record budget exceeded")
	return nil
}
func transactionFindOutput(t *testing.T, ctx context.Context, f transactionFixture, trust *tls.Config, marker string) int64 {
	t.Helper()
	reader := newTransactionDirect(t, ctx, f, trust, "output", "", kgo.FetchIsolationLevel(kgo.ReadCommitted()), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{f.OutputTopic: {0: kgo.NewOffset().AtStart()}}))
	readCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for count := 0; count < 64; {
		fetches := reader.PollRecords(readCtx, 1)
		if len(fetches.Errors()) > 0 {
			t.Fatal("independent committed output lookup failed")
		}
		for _, r := range fetches.Records() {
			count++
			if r.Topic != f.OutputTopic || r.Partition != 0 {
				t.Fatal("independent output lookup escaped exact scope")
			}
			if string(r.Value) == marker {
				reader.Close()
				return r.Offset
			}
		}
		if readCtx.Err() != nil {
			t.Fatal("committed transform marker not visible to independent read_committed reader")
		}
	}
	t.Fatal("independent committed marker lookup exceeded record budget")
	return -1
}
func transactionMapKeys(values map[int64]string) []int64 {
	out := make([]int64, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	return out
}

// Public execution results carry error text. Admit only the pinned SDK's fixed
// fencing signatures; arbitrary failures cannot satisfy real fencing acceptance.
func transactionFencingFailureCode(message string) int16 {
	for _, known := range []*kerr.Error{kerr.ProducerFenced, kerr.InvalidProducerEpoch} {
		if strings.Contains(message, known.Error()) {
			return known.Code
		}
	}
	return 0
}
