package jms

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const sidecarOverflowHelperMarker = "jms-sidecar-overflow-helper"

func TestJMSSidecarCaptureBufferIsBounded(t *testing.T) {
	overflow := make(chan struct{}, 1)
	buffer := &sidecarCappedBuffer{max: 8}
	buffer.onOverflow = func() { overflow <- struct{}{} }
	content := []byte("0123456789abcdef")
	if written, err := buffer.Write(content); err != nil || written != len(content) {
		t.Fatalf("Write = (%d, %v), want (%d, nil)", written, err, len(content))
	}
	if got := buffer.buf.String(); got != "01234567" || !buffer.truncated {
		t.Fatalf("buffer = %q truncated=%v", got, buffer.truncated)
	}
	select {
	case <-overflow:
	default:
		t.Fatal("capture overflow did not notify the sidecar runner")
	}
	if _, err := buffer.Write([]byte("more")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-overflow:
		t.Fatal("capture overflow notified the sidecar runner more than once")
	default:
	}
}

func TestJMSSidecarProcessLimiterHonorsContext(t *testing.T) {
	limiter := newSidecarProcessLimiter(1)
	release, err := limiter.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := limiter.acquire(ctx); err == nil {
		t.Fatal("second process slot acquisition unexpectedly succeeded")
	}
	release()
	releaseAgain, err := limiter.acquire(context.Background())
	if err != nil {
		t.Fatalf("slot was not released: %v", err)
	}
	releaseAgain()
}

func TestJMSSidecarMaxProcessesComesFromOperatorEnv(t *testing.T) {
	t.Setenv(sidecarMaxProcessesEnv, "3")
	if got := configuredSidecarMaxProcesses(); got != 3 {
		t.Fatalf("configured max processes = %d, want 3", got)
	}
	t.Setenv(sidecarMaxProcessesEnv, "0")
	if got := configuredSidecarMaxProcesses(); got != defaultSidecarProcesses {
		t.Fatalf("invalid configured max processes = %d, want default %d", got, defaultSidecarProcesses)
	}
	t.Setenv(sidecarMaxProcessesEnv, strconv.Itoa(maxSidecarProcesses+1_000_000))
	if got := configuredSidecarMaxProcesses(); got != maxSidecarProcesses {
		t.Fatalf("oversized configured max processes = %d, want hard cap %d", got, maxSidecarProcesses)
	}
}

func TestJMSSidecarRejectsOversizedStdout(t *testing.T) {
	adapter := sidecarAdapter{
		command: sidecarOverflowHelperCommand(t, "stdout"),
		cfg:     config{Timeout: 30 * time.Second},
		limiter: newSidecarProcessLimiter(1),
	}
	_, err := adapter.run(context.Background(), sidecarRequest{Operation: "health"}, 30*time.Second)
	if err == nil || !strings.Contains(err.Error(), "stdout exceeded") {
		t.Fatalf("oversized sidecar output error = %v", err)
	}
}

func TestJMSSidecarRejectsOversizedStderr(t *testing.T) {
	// Use the already-built Go test binary instead of a dd/cat pipeline. Under a
	// heavily parallel repository test, process startup and the shell pipeline
	// could lose a two-second race to the health deadline before producing the
	// known overflow, making this resource-bound regression nondeterministic.
	adapter := sidecarAdapter{
		command: sidecarOverflowHelperCommand(t, "stderr"),
		cfg:     config{Timeout: 30 * time.Second},
		limiter: newSidecarProcessLimiter(1),
	}
	_, err := adapter.run(context.Background(), sidecarRequest{Operation: "health"}, 30*time.Second)
	if err == nil || !strings.Contains(err.Error(), "stderr exceeded") {
		t.Fatalf("oversized sidecar stderr error = %v", err)
	}
}

func sidecarOverflowHelperCommand(t *testing.T, stream string) string {
	t.Helper()
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	helperPath := filepath.Join(t.TempDir(), "jms-sidecar-overflow-helper")
	if err := os.Symlink(executable, helperPath); err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{
		helperPath,
		"-test.run=^TestJMSSidecarOverflowHelper$",
		"--",
		sidecarOverflowHelperMarker,
		stream,
		strconv.Itoa(maxSidecarCaptureBytes + 1),
	}, " ")
}

func TestJMSSidecarOverflowHelper(t *testing.T) {
	marker := -1
	for index, arg := range os.Args {
		if arg == sidecarOverflowHelperMarker {
			marker = index
			break
		}
	}
	if marker < 0 {
		return
	}
	if len(os.Args) != marker+3 {
		os.Exit(2)
	}
	size, err := strconv.Atoi(os.Args[marker+2])
	if err != nil || size <= 0 {
		os.Exit(2)
	}
	var output *os.File
	switch os.Args[marker+1] {
	case "stdout":
		output = os.Stdout
	case "stderr":
		output = os.Stderr
	default:
		os.Exit(2)
	}
	if _, err := output.Write(bytes.Repeat([]byte("x"), size)); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestJMSMessageBodyExtractionIsBounded(t *testing.T) {
	result := &protocol.Result{Extractions: map[string]string{}}
	body := []byte(strings.Repeat("x", protocol.MaxExtractionValueBytes+1))
	if err := addMessageExtractions(result, message{Body: body}); err != nil {
		t.Fatal(err)
	}
	if got := len(result.Extractions["jms_body"]); got != protocol.MaxExtractionValueBytes {
		t.Fatalf("jms_body extraction length = %d, want %d", got, protocol.MaxExtractionValueBytes)
	}
	if result.Extractions["jms_body_truncated"] != "true" {
		t.Fatalf("jms_body_truncated = %q, want true", result.Extractions["jms_body_truncated"])
	}
}

func TestJMSRealAdapterRejectsUnsupportedSettlementModes(t *testing.T) {
	p := &JMS{name: "jms-solace", provider: "solace"}
	err := p.Init(context.Background(), map[string]any{
		"broker_url": "amqps://broker.example:5671", "jms_mock": false, "jms_acknowledgement": "client",
	})
	if err == nil || !strings.Contains(err.Error(), "not supported by real adapters") {
		t.Fatalf("Init error = %v, want fail-closed settlement error", err)
	}
}

func TestJMSMemoryBrokerBoundsSubscriptionKeys(t *testing.T) {
	b := &memoryBroker{queues: map[string][]message{}, topics: map[string]map[string][]message{}, refs: map[string]int{}}
	for i := 0; i < maxJMSDestinationKeys*2; i++ {
		b.subscribe("events", "subscriber-"+strconv.Itoa(i))
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if got := b.resourceKeyCountLocked(); got > maxJMSDestinationKeys {
		t.Fatalf("broker resource keys = %d, cap = %d", got, maxJMSDestinationKeys)
	}
}

func TestJMSMockNamespacesDoNotShareMessagesAndCleanUp(t *testing.T) {
	ctx := context.Background()
	newProtocol := func(namespace string) *JMS {
		p := &JMS{name: "jms-solace", provider: "solace"}
		if err := p.Init(ctx, map[string]any{
			"jms_mock": true, "jms_destination": "orders", "jms_timeout_ms": 10,
			"runtime_mock_namespace": namespace,
		}); err != nil {
			t.Fatalf("Init(%s): %v", namespace, err)
		}
		return p
	}
	one := newProtocol("test-jms-one")
	two := newProtocol("test-jms-two")
	if result, _ := one.Execute(ctx, &protocol.Action{Name: "publish", Type: "jms_publish", Body: []byte("one")}); !result.Success {
		t.Fatalf("publish failed: %s", result.ErrorMessage)
	}
	if result, _ := two.Execute(ctx, &protocol.Action{Name: "consume", Type: "jms_consume"}); result.Success {
		t.Fatal("second mock namespace consumed the first namespace's message")
	}
	if result, _ := one.Execute(ctx, &protocol.Action{Name: "consume", Type: "jms_consume"}); !result.Success {
		t.Fatalf("own namespace consume failed: %s", result.ErrorMessage)
	}
	_ = one.Teardown(ctx)
	_ = two.Teardown(ctx)
	broker.mu.Lock()
	defer broker.mu.Unlock()
	for key := range broker.queues {
		if strings.HasPrefix(key, "test-jms-one\x00") || strings.HasPrefix(key, "test-jms-two\x00") {
			t.Fatalf("mock namespace key survived teardown: %q", key)
		}
	}
}

func TestJMSMemoryQueueIsCountAndByteBounded(t *testing.T) {
	b := &memoryBroker{queues: map[string][]message{}, topics: map[string]map[string][]message{}, refs: map[string]int{}}
	payload := make([]byte, 1<<20)
	for i := 0; i < maxJMSQueueDepth+1; i++ {
		b.publish("q", message{Body: payload, CreatedAt: time.Now()})
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.queues["q"]) > maxJMSQueueDepth {
		t.Fatalf("queue depth = %d, cap = %d", len(b.queues["q"]), maxJMSQueueDepth)
	}
	total := 0
	for _, msg := range b.queues["q"] {
		total += messageSize(msg)
	}
	if total > maxJMSQueueBytes {
		t.Fatalf("queue bytes = %d, cap = %d", total, maxJMSQueueBytes)
	}
}

func TestJMSMemoryBrokerHasAggregateRetentionBudget(t *testing.T) {
	b := &memoryBroker{
		queues: map[string][]message{}, topics: map[string]map[string][]message{}, refs: map[string]int{},
		queueBytes: map[string]int64{}, topicBytes: map[string]map[string]int64{}, totalBytes: maxJMSRetainedBytes,
	}
	if err := b.publish("orders", message{Body: []byte("x")}); err == nil || !strings.Contains(err.Error(), "aggregate") {
		t.Fatalf("publish at aggregate cap error = %v", err)
	}
	if len(b.queues) != 0 {
		t.Fatal("aggregate-cap rejection mutated broker queues")
	}
}

func TestJMSTopicFanoutRejectsBeforeAggregateAmplification(t *testing.T) {
	b := &memoryBroker{
		queues: map[string][]message{},
		topics: map[string]map[string][]message{"events": {"a": nil, "b": nil}}, refs: map[string]int{},
		queueBytes: map[string]int64{}, topicBytes: map[string]map[string]int64{"events": {"a": 0, "b": 0}},
		totalBytes: maxJMSRetainedBytes - 1,
	}
	if err := b.publishTopic("events", message{Body: []byte("xx")}); err == nil || !strings.Contains(err.Error(), "fanout") {
		t.Fatalf("fanout at aggregate cap error = %v", err)
	}
	if len(b.topics["events"]["a"]) != 0 || len(b.topics["events"]["b"]) != 0 {
		t.Fatal("rejected fanout mutated subscription queues")
	}
}

func TestJMSConsumeClearsRemovedBackingArrayReference(t *testing.T) {
	backing := []message{{Body: []byte("first")}, {Body: []byte("second")}}
	b := &memoryBroker{queues: map[string][]message{"q": backing}, topics: map[string]map[string][]message{}, refs: map[string]int{}}
	if _, ok := b.consume("q"); !ok {
		t.Fatal("consume returned no message")
	}
	if backing[1].Body != nil {
		t.Fatal("consumed queue left a message body reachable in spare backing-array capacity")
	}
}

func TestJMSRejectsUnboundedBrowseAndSequenceBatches(t *testing.T) {
	values := make([]any, MaxJMSSequenceNumbers+1)
	if _, err := parseSequenceNumbers(values); err == nil {
		t.Fatal("parseSequenceNumbers accepted an oversized batch")
	}
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(context.Background(), map[string]any{"jms_mock": true, "jms_destination": "orders"}); err != nil {
		t.Fatal(err)
	}
	defer p.Teardown(context.Background())
	result, err := p.Execute(context.Background(), &protocol.Action{
		Name: "browse", Type: "jms_browse", Params: map[string]any{"max": MaxJMSBrowseMessages + 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.StatusCode != 400 || !strings.Contains(result.ErrorMessage, "browse max") {
		t.Fatalf("oversized browse result = %#v", result)
	}
}

func TestJMSInitRejectsOverflowingTimeout(t *testing.T) {
	p := &JMS{name: "jms-solace", provider: "solace"}
	if err := p.Init(context.Background(), map[string]any{
		"jms_mock": true, "jms_destination": "orders", "jms_timeout_ms": float64(MaxConfiguredTimeout.Milliseconds() + 1),
	}); err == nil {
		t.Fatal("Init accepted a timeout above the runtime hard limit")
	}
}

func TestJMSMemoryPublishCapacityReturnsErrorWithoutDroppingLiveDestination(t *testing.T) {
	b := &memoryBroker{queues: map[string][]message{}, topics: map[string]map[string][]message{}, refs: map[string]int{}}
	liveDestination := mockJMSKey("live-run", "orders")
	b.acquire("live-run")
	defer b.release("live-run")

	b.mu.Lock()
	b.queues[liveDestination] = []message{{Body: []byte("live")}}
	for i := 1; i < maxJMSDestinationKeys; i++ {
		b.queues["occupied-"+strconv.Itoa(i)] = nil
	}
	b.mu.Unlock()

	adapter := memoryAdapter{broker: b, namespace: "new-run"}
	if _, err := adapter.Publish(context.Background(), "orders", message{Body: []byte("new")}); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("Publish error = %v, want explicit capacity error", err)
	}
	protocolUnderTest := &JMS{
		name: "jms-solace", provider: "solace",
		cfg: config{Destination: "orders", Mock: true}, adapter: adapter,
	}
	result, err := protocolUnderTest.Execute(context.Background(), &protocol.Action{Name: "publish", Type: "jms_publish", Body: []byte("new")})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "capacity") {
		t.Fatalf("Execute result = %#v, want failed publish with capacity error", result)
	}
	if result.ServerTelemetry == nil || result.ServerTelemetry.SendFailureCount != 1 {
		t.Fatalf("send failure telemetry = %#v, want one failed publish", result.ServerTelemetry)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.queues[mockJMSKey("new-run", "orders")]; exists {
		t.Fatal("failed publish created the new destination")
	}
	if got := b.queues[liveDestination]; len(got) != 1 || string(got[0].Body) != "live" {
		t.Fatalf("live destination was changed or dropped: %#v", got)
	}
	if b.resourceKeyCountLocked() != maxJMSDestinationKeys {
		t.Fatalf("resource keys = %d, want unchanged cap %d", b.resourceKeyCountLocked(), maxJMSDestinationKeys)
	}
}
