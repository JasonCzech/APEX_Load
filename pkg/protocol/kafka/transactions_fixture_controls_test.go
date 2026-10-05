//go:build kafka_real && kafka_transaction_integration

package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"reflect"
	"testing"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// These controls qualify fixture bounds only, never actual broker semantics.
func TestTransactionFixturePositionScope(t *testing.T) {
	f := transactionFixture{VisibilityTopic: "visibility", InputTopic: "input", OutputTopic: "output"}
	for _, topic := range []string{f.VisibilityTopic, f.InputTopic, f.OutputTopic} {
		for _, isolation := range []int8{0, 1} {
			req, err := transactionListOffsetsRequest(f, topic, isolation)
			if err != nil || req.IsolationLevel != isolation || len(req.Topics) != 1 || req.Topics[0].Topic != topic || len(req.Topics[0].Partitions) != 1 || req.Topics[0].Partitions[0].Partition != 0 || req.Topics[0].Partitions[0].Timestamp != -1 {
				t.Fatal("position request escaped exact latest/single-partition/isolation scope")
			}
		}
	}
	for _, tc := range []struct {
		topic     string
		isolation int8
	}{{"other", 0}, {"", 1}, {f.InputTopic, -1}, {f.OutputTopic, 2}} {
		if req, err := transactionListOffsetsRequest(f, tc.topic, tc.isolation); err == nil || req != nil {
			t.Fatal("position scope admitted unsupported input")
		}
	}
}
func TestTransactionFixtureIndependentWireRoles(t *testing.T) {
	for _, key := range []int16{0, 1, 8, 11, 12, 13, 14, 22, 23, 24, 25, 26, 28, 42} {
		if transactionWireAllowed("oracle", key) {
			t.Fatalf("Describe-only oracle admitted execution API %d", key)
		}
	}
	for _, key := range []int16{2, 3, 9, 10, 15, 17, 18, 36} {
		if !transactionWireAllowed("oracle", key) {
			t.Fatalf("fixed read-only oracle refused API %d", key)
		}
	}
	for _, key := range []int16{0, 8, 9, 10, 11, 12, 13, 14, 22, 24, 25, 26, 28} {
		if transactionWireAllowed("output", key) {
			t.Fatalf("no-group output reader admitted mutation/group API %d", key)
		}
	}
	for _, key := range []int16{1, 2, 8, 9, 11, 12, 13, 14, 25, 28} {
		if transactionWireAllowed("writer", key) {
			t.Fatalf("fixture writer admitted fetch/group API %d", key)
		}
	}
	if transactionWireAllowed("unknown", 3) {
		t.Fatal("unknown role received implicit authority")
	}
}
func TestTransactionFixtureTransportDoesNotBroadenSeekProfile(t *testing.T) {
	p := &ownedSeekProxy{}
	for _, key := range []int16{24, 25, 26, 28} {
		if p.executionKey(key) {
			t.Fatal("original seek transport admitted transaction authority")
		}
	}
	p.transactions = true
	for _, key := range []int16{24, 25, 26, 28} {
		if !p.executionKey(key) {
			t.Fatal("explicit transaction transport refused required transaction API")
		}
	}
	for _, key := range []int16{4, 5, 19, 20, 30, 42, 65} {
		if p.executionKey(key) {
			t.Fatal("transaction transport admitted unrelated API")
		}
	}
}
func TestTransactionFixtureIndependentSDKBounds(t *testing.T) {
	f := transactionFixture{Endpoint: "tls://localhost:19402", BrokerAddress: "127.0.0.1:19403"}
	opts := transactionDirectOptions(t.Context(), f, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "localhost"}, "unit", "test-only-password", "unit")
	opts = append(opts, kgo.Dialer(func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("offline fixture constructor forbids network")
	}))
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		t.Fatal("bounded independent SDK construction failed")
	}
	defer cl.Close()
	if cl.OptValue(kgo.ConsumerGroup) != "" {
		t.Fatal("independent SDK joins a group")
	}
	topics := reflect.ValueOf(cl.OptValue(kgo.ConsumeTopics))
	if topics.Kind() != reflect.Map || topics.Len() != 0 {
		t.Fatal("independent SDK unexpectedly consumes topics")
	}
	if cl.OptValue(kgo.BrokerMaxReadBytes) != int32(1<<20) || cl.OptValue(kgo.BrokerMaxWriteBytes) != int32(1<<20) || cl.OptValue(kgo.FetchMaxBytes) != int32(1<<20) {
		t.Fatal("independent SDK I/O budgets changed")
	}
}

func TestTransactionFixtureStableCommittedResponses(t *testing.T) {
	req := transactionStableOffsetRequest("group", "input")
	if !req.RequireStable || req.Group != "group" || len(req.Topics) != 1 || req.Topics[0].Topic != "input" || len(req.Topics[0].Partitions) != 1 || req.Topics[0].Partitions[0] != 0 || len(req.Groups) != 1 || req.Groups[0].Group != "group" || req.Groups[0].MemberID != nil || req.Groups[0].MemberEpoch != -1 || len(req.Groups[0].Topics) != 1 || req.Groups[0].Topics[0].Topic != "input" || len(req.Groups[0].Topics[0].Partitions) != 1 || req.Groups[0].Topics[0].Partitions[0] != 0 {
		t.Fatal("stable committed oracle request lost exact scope")
	}
	response := func(code int16) *kmsg.OffsetFetchResponse {
		return &kmsg.OffsetFetchResponse{Version: 9, Groups: []kmsg.OffsetFetchResponseGroup{{Group: "group", Topics: []kmsg.OffsetFetchResponseGroupTopic{{Topic: "input", Partitions: []kmsg.OffsetFetchResponseGroupTopicPartition{{Partition: 0, Offset: 1, ErrorCode: code}}}}}}}
	}
	calls := 0
	value, retries, err := transactionReadStableOffset(t.Context(), "group", "input", func(context.Context) (kmsg.Response, error) {
		calls++
		if calls == 1 {
			return response(kerr.UnstableOffsetCommit.Code), nil
		}
		return response(0), nil
	})
	if err != nil || value != 1 || retries != 1 || calls != 2 {
		t.Fatal("known unstable transaction offset did not retry to real stable response")
	}
	for _, code := range []int16{kerr.GroupAuthorizationFailed.Code, kerr.TopicAuthorizationFailed.Code, kerr.UnknownServerError.Code} {
		calls = 0
		_, retries, err = transactionReadStableOffset(t.Context(), "group", "input", func(context.Context) (kmsg.Response, error) { calls++; return response(code), nil })
		if !errors.Is(err, kerr.ErrorForCode(code)) || retries != 0 || calls != 1 {
			t.Fatal("nontransient stable-offset failure retried or disappeared")
		}
	}
	malformed := response(0)
	malformed.Groups[0].Topics[0].Topic = "other"
	if _, err := transactionStableOffsetValue("group", "input", malformed); err == nil {
		t.Fatal("stable oracle rebound another topic")
	}
	malformed = response(0)
	malformed.Groups[0].Topics[0].Partitions = append(malformed.Groups[0].Topics[0].Partitions, malformed.Groups[0].Topics[0].Partitions[0])
	if _, err := transactionStableOffsetValue("group", "input", malformed); err == nil {
		t.Fatal("stable oracle admitted duplicate inventory")
	}
	ctx, cancel := context.WithCancel(t.Context())
	calls = 0
	_, retries, err = transactionReadStableOffset(ctx, "group", "input", func(context.Context) (kmsg.Response, error) { calls++; cancel(); return response(0), nil })
	if !errors.Is(err, context.Canceled) || retries != 0 || calls != 1 {
		t.Fatal("stable oracle admitted response after cancellation")
	}
}

func TestTransactionFixtureFencingNeedsPinnedActualSignature(t *testing.T) {
	for _, known := range []*kerr.Error{kerr.ProducerFenced, kerr.InvalidProducerEpoch} {
		if transactionFencingFailureCode("produce failed: "+known.Error()) != known.Code {
			t.Fatal("pinned fencing error signature not admitted")
		}
	}
	for _, unrelated := range []string{"", context.DeadlineExceeded.Error(), kerr.SaslAuthenticationFailed.Error(), kerr.UnknownServerError.Error(), "producer unavailable", "INVALID_PRODUCER_EPOCH", kerr.TransactionCoordinatorFenced.Error()} {
		if transactionFencingFailureCode(unrelated) != 0 {
			t.Fatal("unrelated failure qualified producer fencing")
		}
	}
}
