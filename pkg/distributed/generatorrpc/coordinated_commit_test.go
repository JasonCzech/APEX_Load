package generatorrpc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestTranslateCoordinatedStartRequestCompensatesForClockSkew(t *testing.T) {
	controllerSentAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	controllerBarrier := controllerSentAt.Add(2 * time.Second)
	for _, test := range []struct {
		name               string
		generatorClockSkew time.Duration
	}{
		{name: "generator clock three seconds ahead", generatorClockSkew: 3 * time.Second},
		{name: "generator clock one and a half seconds behind", generatorClockSkew: -1500 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			generatorReceivedAt := controllerSentAt.Add(test.generatorClockSkew)
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
				coordinatedStartControllerMetadataKey,
				fmt.Sprint(controllerSentAt.UnixNano()),
			))
			request := &pb.WorkCommitRequest{TestId: "skewed", StartAtUnixNano: controllerBarrier.UnixNano()}

			translated, err := translateCoordinatedStartRequest(ctx, request, generatorReceivedAt)
			if err != nil {
				t.Fatalf("translateCoordinatedStartRequest: %v", err)
			}
			// The controller requested a two-second relative lead. Regardless of
			// the generator's wall-clock skew, its local deadline keeps that lead
			// instead of comparing against the controller's absolute timestamp.
			want := generatorReceivedAt.Add(2 * time.Second)
			if got := time.Unix(0, translated.GetStartAtUnixNano()); !got.Equal(want) {
				t.Fatalf("translated barrier = %s, want %s", got, want)
			}
			if got := time.Unix(0, request.GetStartAtUnixNano()); !got.Equal(controllerBarrier) {
				t.Fatalf("input request mutated: barrier = %s, want %s", got, controllerBarrier)
			}
		})
	}
}

func TestTranslateCoordinatedStartRequestKeepsCommonDeadlineAcrossAsymmetricCommitTransit(t *testing.T) {
	controllerSentAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	controllerBarrier := controllerSentAt.Add(2 * time.Second)
	generatorClockOffset := 3 * time.Second
	want := controllerBarrier.Add(generatorClockOffset)

	deadlines := make([]time.Time, 0, 2)
	for _, transit := range []time.Duration{10 * time.Millisecond, 800 * time.Millisecond} {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
			coordinatedStartControllerMetadataKey, fmt.Sprint(controllerSentAt.UnixNano()),
			coordinatedStartClockOffsetMetadataKey, fmt.Sprint(generatorClockOffset.Nanoseconds()),
			coordinatedStartClockErrorMetadataKey, fmt.Sprint((5*time.Millisecond).Nanoseconds()),
		))
		request := &pb.WorkCommitRequest{TestId: "asymmetric", StartAtUnixNano: controllerBarrier.UnixNano()}
		receivedAt := controllerSentAt.Add(generatorClockOffset).Add(transit)
		translated, err := translateCoordinatedStartRequest(ctx, request, receivedAt)
		if err != nil {
			t.Fatalf("transit %s: translateCoordinatedStartRequest: %v", transit, err)
		}
		deadline := time.Unix(0, translated.GetStartAtUnixNano())
		if !deadline.Equal(want) {
			t.Fatalf("transit %s: translated deadline = %s, want %s", transit, deadline, want)
		}
		deadlines = append(deadlines, deadline)
	}
	if !deadlines[0].Equal(deadlines[1]) {
		t.Fatalf("asymmetric commit transit moved common deadline: fast=%s slow=%s", deadlines[0], deadlines[1])
	}
}

func TestTranslateCoordinatedStartRequestRejectsInvalidClockEstimateMetadata(t *testing.T) {
	sentAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	request := &pb.WorkCommitRequest{TestId: "invalid-estimate", StartAtUnixNano: sentAt.Add(time.Second).UnixNano()}
	tests := []struct {
		name     string
		metadata metadata.MD
	}{
		{
			name: "missing uncertainty",
			metadata: metadata.Pairs(
				coordinatedStartControllerMetadataKey, fmt.Sprint(sentAt.UnixNano()),
				coordinatedStartClockOffsetMetadataKey, "0",
			),
		},
		{
			name: "uncertainty out of bounds",
			metadata: metadata.Pairs(
				coordinatedStartControllerMetadataKey, fmt.Sprint(sentAt.UnixNano()),
				coordinatedStartClockOffsetMetadataKey, "0",
				coordinatedStartClockErrorMetadataKey, fmt.Sprint((coordinatedStartClockEstimateMaxUncertainty + time.Nanosecond).Nanoseconds()),
			),
		},
		{
			name: "estimate without sent time",
			metadata: metadata.Pairs(
				coordinatedStartClockOffsetMetadataKey, "0",
				coordinatedStartClockErrorMetadataKey, "0",
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), test.metadata)
			_, err := translateCoordinatedStartRequest(ctx, request, sentAt)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("translate error = %v (%s), want InvalidArgument", err, status.Code(err))
			}
		})
	}
}

func TestTranslateCoordinatedStartRequestRejectsInvalidClockMetadata(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		coordinatedStartControllerMetadataKey,
		"not-a-timestamp",
	))
	_, err := translateCoordinatedStartRequest(ctx, &pb.WorkCommitRequest{
		TestId:          "invalid-clock",
		StartAtUnixNano: time.Now().Add(time.Second).UnixNano(),
	}, time.Now())
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("translate error = %v (%s), want InvalidArgument", err, status.Code(err))
	}
}

func TestClientGeneratorClockEstimateUsesRoundTripMidpoint(t *testing.T) {
	controllerSentAt := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	controllerReceivedAt := controllerSentAt.Add(100 * time.Millisecond)
	generatorAt := controllerSentAt.Add(50*time.Millisecond + 3*time.Second)
	client := &Client{}
	client.observeGeneratorClock(controllerSentAt, controllerReceivedAt, generatorAt)

	estimate, ok := client.coordinatedStartClockEstimate(controllerReceivedAt)
	if !ok {
		t.Fatal("clock estimate was not retained")
	}
	if estimate.offset != 3*time.Second || estimate.uncertainty != 50*time.Millisecond {
		t.Fatalf("clock estimate = offset %s uncertainty %s, want 3s / 50ms", estimate.offset, estimate.uncertainty)
	}
	if _, ok := client.coordinatedStartClockEstimate(controllerReceivedAt.Add(coordinatedStartClockEstimateMaxAge + time.Nanosecond)); ok {
		t.Fatal("stale clock estimate remained eligible for coordinated start")
	}

	for _, offset := range []time.Duration{
		-maxCoordinatedStartClockOffset - time.Nanosecond,
		maxCoordinatedStartClockOffset + time.Nanosecond,
	} {
		outOfBoundsClient := &Client{}
		outOfBoundsClient.observeGeneratorClock(
			controllerSentAt,
			controllerReceivedAt,
			controllerSentAt.Add(50*time.Millisecond+offset),
		)
		if _, ok := outOfBoundsClient.coordinatedStartClockEstimate(controllerReceivedAt); ok {
			t.Fatalf("out-of-bounds clock offset %s remained eligible for coordinated start", offset)
		}
	}
}

func stageCoordinatedHTTPWork(t *testing.T, service *Service, testID, scriptID, baseURL, target string) {
	t.Helper()
	script := []byte(fmt.Sprintf(`{
		"id":%q,
		"name":%q,
		"protocol":"http",
		"config":{"base_url":%q,"run_logic":{"iterations":1000}},
		"actions":[{"name":"request","type":"GET","target":%q}]
	}`, scriptID, scriptID, baseURL, target))
	ack, err := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId:         testID,
		GroupName:      "group",
		ScriptId:       scriptID,
		ScriptContent:  script,
		VuserCount:     1,
		TargetDuration: durationpb.New(500 * time.Millisecond),
	})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%#v, %v)", ack, err)
	}
}

func TestExecutionCommitPublishesZeroLiveVUsersBeforeFutureBarrier(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	service := newIsolatedService(t, 20, WithExecution())
	stageCoordinatedHTTPWork(t, service, "future-zero", "future-zero-script", "http://127.0.0.1:1", "/never-before-barrier")
	id, subscriber, _, err := service.registerScopedSubscriber(0, "future-zero")
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}
	defer service.unregisterSubscriber(id)

	startAt := time.Now().Add(500 * time.Millisecond)
	ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "future-zero", StartAtUnixNano: startAt.UnixNano()})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("CommitWork = (%#v, %v)", ack, err)
	}

	var running *pb.MetricsBatch
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		batch := subscriber.pop()
		if batch != nil && batch.GetLifecycleState() == "RUNNING" {
			running = batch
			break
		}
		time.Sleep(time.Millisecond)
	}
	if running == nil {
		t.Fatal("missing pre-barrier RUNNING lifecycle batch")
	}
	if got := running.GetStats().GetActiveVusers(); got != 0 {
		t.Fatalf("pre-barrier global active VUsers = %d, want 0", got)
	}
	if len(running.GetGroups()) != 1 || running.GetGroups()[0].GetActiveVusers() != 0 {
		t.Fatalf("pre-barrier group metrics = %#v, want one explicit zero", running.GetGroups())
	}
	service.StopTest(context.Background(), &pb.StopRequest{TestId: "future-zero"})
}

func TestCommitWithoutBarrierPreservesUncoordinatedStart(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	requestStarted := make(chan struct{}, 1)
	sut := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		<-request.Context().Done()
	}))
	t.Cleanup(sut.Close)
	service := newIsolatedService(t, 20, WithExecution())
	stageCoordinatedHTTPWork(t, service, "immediate", "immediate-script", sut.URL, "/immediate")

	ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "immediate"})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("CommitWork = (%#v, %v)", ack, err)
	}

	service.mu.RLock()
	execution := service.executions["immediate"]["group"]
	service.mu.RUnlock()
	if execution == nil {
		t.Fatal("committed execution is missing")
	}
	if !execution.startAt.IsZero() {
		t.Fatalf("uncoordinated execution start = %s, want zero-time sentinel", execution.startAt)
	}

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("uncoordinated execution did not issue traffic promptly")
	}
	select {
	case <-execution.done:
		t.Fatal("uncoordinated execution exited immediately after commit")
	case <-time.After(50 * time.Millisecond):
	}
	_, _ = service.StopTest(context.Background(), &pb.StopRequest{TestId: "immediate"})
}

func TestCommitWorkUsesCommonFutureStartBarrier(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	var firstA, firstB atomic.Int64
	sut := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		now := time.Now().UnixNano()
		if request.URL.Path == "/a" {
			firstA.CompareAndSwap(0, now)
		} else if request.URL.Path == "/b" {
			firstB.CompareAndSwap(0, now)
		}
	}))
	defer sut.Close()

	serviceA := NewService("coordinated-a", 10, WithExecution())
	serviceB := NewService("coordinated-b", 10, WithExecution())
	defer stopCoordinatedTestServices(t, serviceA, "test-a", serviceB, "test-b")
	stageCoordinatedHTTPWork(t, serviceA, "test-a", "script-a", sut.URL, "/a")
	stageCoordinatedHTTPWork(t, serviceB, "test-b", "script-b", sut.URL, "/b")
	startAt := time.Now().Add(400 * time.Millisecond)

	ack, err := serviceA.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "test-a", StartAtUnixNano: startAt.UnixNano()})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("first CommitWork = (%#v, %v)", ack, err)
	}
	// Model a slow but successful peer. Both generators still release against
	// the controller-selected timestamp rather than their individual RPC return.
	time.Sleep(120 * time.Millisecond)
	ack, err = serviceB.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "test-b", StartAtUnixNano: startAt.UnixNano()})
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("second CommitWork = (%#v, %v)", ack, err)
	}
	if firstA.Load() != 0 || firstB.Load() != 0 {
		t.Fatalf("traffic started before barrier: a=%d b=%d", firstA.Load(), firstB.Load())
	}

	deadline := time.Now().Add(2 * time.Second)
	for (firstA.Load() == 0 || firstB.Load() == 0) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	a, b := firstA.Load(), firstB.Load()
	if a == 0 || b == 0 {
		t.Fatalf("traffic did not start on both generators: a=%d b=%d", a, b)
	}
	if a < startAt.UnixNano() || b < startAt.UnixNano() {
		t.Fatalf("traffic preceded barrier %s: a=%s b=%s", startAt, time.Unix(0, a), time.Unix(0, b))
	}
	delta := time.Duration(a - b)
	if delta < 0 {
		delta = -delta
	}
	if delta > 50*time.Millisecond {
		t.Fatalf("coordinated starts differ by %s", delta)
	}
}

func TestCommitWorkClockEstimateKeepsCommonBarrierAcrossAsymmetricFanout(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	var firstA, firstB atomic.Int64
	sut := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		now := time.Now().UnixNano()
		if request.URL.Path == "/a" {
			firstA.CompareAndSwap(0, now)
		} else if request.URL.Path == "/b" {
			firstB.CompareAndSwap(0, now)
		}
	}))
	defer sut.Close()

	serviceA := NewService("asymmetric-a", 10, WithExecution())
	serviceB := NewService("asymmetric-b", 10, WithExecution())
	defer stopCoordinatedTestServices(t, serviceA, "asymmetric-a", serviceB, "asymmetric-b")
	stageCoordinatedHTTPWork(t, serviceA, "asymmetric-a", "script-a", sut.URL, "/a")
	stageCoordinatedHTTPWork(t, serviceB, "asymmetric-b", "script-b", sut.URL, "/b")
	controllerSentAt := time.Now()
	startAt := controllerSentAt.Add(1500 * time.Millisecond)

	type result struct {
		ack *pb.WorkCommitAck
		err error
	}
	commit := func(service *Service, testID string, transit time.Duration, results chan<- result) {
		time.Sleep(transit)
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
			coordinatedStartControllerMetadataKey, fmt.Sprint(controllerSentAt.UnixNano()),
			coordinatedStartClockOffsetMetadataKey, "0",
			coordinatedStartClockErrorMetadataKey, "0",
		))
		ack, err := service.CommitWork(ctx, &pb.WorkCommitRequest{TestId: testID, StartAtUnixNano: startAt.UnixNano()})
		results <- result{ack: ack, err: err}
	}
	results := make(chan result, 2)
	go commit(serviceA, "asymmetric-a", 10*time.Millisecond, results)
	go commit(serviceB, "asymmetric-b", 800*time.Millisecond, results)
	for range 2 {
		result := <-results
		if result.err != nil || result.ack == nil || !result.ack.GetAccepted() {
			t.Fatalf("CommitWork = (%#v, %v)", result.ack, result.err)
		}
	}
	if firstA.Load() != 0 || firstB.Load() != 0 {
		t.Fatalf("traffic started before common barrier: a=%d b=%d", firstA.Load(), firstB.Load())
	}

	deadline := time.Now().Add(2 * time.Second)
	for (firstA.Load() == 0 || firstB.Load() == 0) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	a, b := firstA.Load(), firstB.Load()
	if a == 0 || b == 0 {
		t.Fatalf("traffic did not start on both generators: a=%d b=%d", a, b)
	}
	if a < startAt.UnixNano() || b < startAt.UnixNano() {
		t.Fatalf("traffic preceded common barrier %s: a=%s b=%s", startAt, time.Unix(0, a), time.Unix(0, b))
	}
	delta := time.Duration(a - b)
	if delta < 0 {
		delta = -delta
	}
	if delta > 100*time.Millisecond {
		t.Fatalf("asymmetric 10ms/800ms fanout moved coordinated starts by %s", delta)
	}
}

func stopCoordinatedTestServices(t *testing.T, first *Service, firstID string, second *Service, secondID string) {
	t.Helper()
	for _, run := range []struct {
		service *Service
		id      string
	}{{first, firstID}, {second, secondID}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ack, err := run.service.StopTest(ctx, &pb.StopRequest{TestId: run.id})
		cancel()
		if err != nil || ack == nil || !ack.GetAcknowledged() {
			t.Errorf("cleanup StopTest(%s) = (%#v, %v)", run.id, ack, err)
		}
	}
}

func TestCommitWorkRejectsElapsedStartBarrierWithoutStarting(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	var requests atomic.Int64
	sut := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer sut.Close()
	service := NewService("late-barrier", 10, WithExecution())
	stageCoordinatedHTTPWork(t, service, "late-test", "late-script", sut.URL, "/late")

	ack, err := service.CommitWork(context.Background(), &pb.WorkCommitRequest{
		TestId:          "late-test",
		StartAtUnixNano: time.Now().Add(-time.Millisecond).UnixNano(),
	})
	if err != nil {
		t.Fatalf("CommitWork: %v", err)
	}
	if ack.GetAccepted() || ack.GetErrorMessage() == "" {
		t.Fatalf("late barrier ack = %#v", ack)
	}
	if !strings.Contains(ack.GetErrorMessage(), "elapsed by") || !strings.Contains(ack.GetErrorMessage(), "local start deadline") {
		t.Fatalf("late barrier error lacks clock delta: %q", ack.GetErrorMessage())
	}
	time.Sleep(50 * time.Millisecond)
	if requests.Load() != 0 {
		t.Fatalf("late commit emitted %d requests", requests.Load())
	}
	if _, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "late-test"}); err != nil {
		t.Fatalf("cleanup StopTest: %v", err)
	}
}
