package generatorrpc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

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
	time.Sleep(50 * time.Millisecond)
	if requests.Load() != 0 {
		t.Fatalf("late commit emitted %d requests", requests.Load())
	}
	if _, err := service.StopTest(context.Background(), &pb.StopRequest{TestId: "late-test"}); err != nil {
		t.Fatalf("cleanup StopTest: %v", err)
	}
}
