package generatorrpc

import (
	"context"
	"fmt"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
)

func TestAssignAndCommitLifecyclePublicationsStayOrderedWhenAssignIsDelayed(t *testing.T) {
	service := newIsolatedService(t, 10)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{
		ScriptId: "script", Content: []byte(`{"name":"ordered"}`),
	}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	_, subscriber, _, err := service.registerScopedSubscriber(0, "run")
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}

	stagedPublishEntered := make(chan struct{})
	releaseStagedPublish := make(chan struct{})
	service.lifecyclePublishHook = func(batch *pb.MetricsBatch) {
		if batch.GetLifecycleState() != "STAGED" {
			return
		}
		close(stagedPublishEntered)
		<-releaseStagedPublish
	}

	assignDone := make(chan error, 1)
	go func() {
		ack, assignErr := service.AssignWork(context.Background(), &pb.WorkAssignment{
			TestId: "run", GroupName: "buyers", ScriptId: "script", VuserCount: 1,
		})
		if assignErr == nil && (ack == nil || !ack.GetAccepted()) {
			assignErr = fmt.Errorf("assignment was not accepted: %#v", ack)
		}
		assignDone <- assignErr
	}()
	select {
	case <-stagedPublishEntered:
	case <-time.After(time.Second):
		t.Fatal("AssignWork did not reach the delayed STAGED publication")
	}

	commitInvoked := make(chan struct{})
	commitDone := make(chan error, 1)
	go func() {
		close(commitInvoked)
		ack, commitErr := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "run"})
		if commitErr == nil && (ack == nil || !ack.GetAccepted()) {
			commitErr = fmt.Errorf("commit was not accepted: %#v", ack)
		}
		commitDone <- commitErr
	}()
	<-commitInvoked
	select {
	case err := <-commitDone:
		t.Fatalf("CommitWork overtook delayed AssignWork publication: %v", err)
	default:
	}

	close(releaseStagedPublish)
	if err := <-assignDone; err != nil {
		t.Fatalf("AssignWork: %v", err)
	}
	if err := <-commitDone; err != nil {
		t.Fatalf("CommitWork: %v", err)
	}

	first, second := subscriber.pop(), subscriber.pop()
	if first == nil || first.GetLifecycleState() != "STAGED" {
		t.Fatalf("first lifecycle batch = %#v, want STAGED", first)
	}
	if second == nil || second.GetLifecycleState() != "RUNNING" {
		t.Fatalf("second lifecycle batch = %#v, want RUNNING", second)
	}
	if extra := subscriber.pop(); extra != nil {
		t.Fatalf("unexpected extra lifecycle batch: %#v", extra)
	}

	service.lifecyclePublishHook = nil
	if ack, stopErr := service.StopTest(context.Background(), &pb.StopRequest{TestId: "run"}); stopErr != nil || !ack.GetAcknowledged() {
		t.Fatalf("StopTest cleanup = (%#v, %v)", ack, stopErr)
	}
}

func TestDelayedRunningPublicationCannotFollowCompetingTerminal(t *testing.T) {
	service := newIsolatedService(t, 10)
	if _, err := service.DistributeScript(context.Background(), &pb.ScriptPayload{
		ScriptId: "script", Content: []byte(`{"name":"terminal-order"}`),
	}); err != nil {
		t.Fatalf("DistributeScript: %v", err)
	}
	_, subscriber, _, err := service.registerScopedSubscriber(0, "run")
	if err != nil {
		t.Fatalf("register subscriber: %v", err)
	}
	if ack, assignErr := service.AssignWork(context.Background(), &pb.WorkAssignment{
		TestId: "run", GroupName: "buyers", ScriptId: "script", VuserCount: 1,
	}); assignErr != nil || !ack.GetAccepted() {
		t.Fatalf("AssignWork = (%#v, %v)", ack, assignErr)
	}
	if staged := subscriber.pop(); staged == nil || staged.GetLifecycleState() != "STAGED" {
		t.Fatalf("initial lifecycle batch = %#v, want STAGED", staged)
	}

	runningPublishEntered := make(chan struct{})
	releaseRunningPublish := make(chan struct{})
	service.lifecyclePublishHook = func(batch *pb.MetricsBatch) {
		if batch.GetLifecycleState() != "RUNNING" {
			return
		}
		close(runningPublishEntered)
		<-releaseRunningPublish
	}
	commitDone := make(chan error, 1)
	go func() {
		ack, commitErr := service.CommitWork(context.Background(), &pb.WorkCommitRequest{TestId: "run"})
		if commitErr == nil && (ack == nil || !ack.GetAccepted()) {
			commitErr = fmt.Errorf("commit was not accepted: %#v", ack)
		}
		commitDone <- commitErr
	}()
	select {
	case <-runningPublishEntered:
	case <-time.After(time.Second):
		t.Fatal("CommitWork did not reach the delayed RUNNING publication")
	}

	stopDone := make(chan error, 1)
	go func() {
		ack, stopErr := service.StopTest(context.Background(), &pb.StopRequest{TestId: "run"})
		if stopErr == nil && (ack == nil || !ack.GetAcknowledged()) {
			stopErr = fmt.Errorf("stop was not acknowledged: %#v", ack)
		}
		stopDone <- stopErr
	}()
	waitForDetachedStoppingAssignment(t, service, "run")
	close(releaseRunningPublish)
	if err := <-commitDone; err != nil {
		t.Fatalf("CommitWork: %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("StopTest: %v", err)
	}

	terminal := subscriber.pop()
	if terminal == nil || terminal.GetLifecycleState() != "COMPLETE" {
		t.Fatalf("queued lifecycle batch = %#v, want terminal COMPLETE", terminal)
	}
	if extra := subscriber.pop(); extra != nil {
		t.Fatalf("nonterminal lifecycle was queued after terminal competition: %#v", extra)
	}
}

func TestMetricsSubscriberRejectsNonterminalAfterTerminal(t *testing.T) {
	subscriber := &metricsSubscriber{wake: make(chan struct{}, 1), fenced: make(chan struct{})}
	terminal := &pb.MetricsBatch{TestId: "run", ExecutionGeneration: 7, LifecycleState: "FAILED"}
	if dropped := subscriber.enqueue(terminal); dropped != 0 {
		t.Fatalf("enqueue terminal dropped %d batches", dropped)
	}
	if dropped := subscriber.enqueue(&pb.MetricsBatch{
		TestId: "run", ExecutionGeneration: 7, LifecycleState: "RUNNING",
	}); dropped != 0 {
		t.Fatalf("stale nonterminal counted as queue overflow: %d", dropped)
	}
	if got := subscriber.pop(); got != terminal {
		t.Fatalf("first queued batch = %#v, want terminal", got)
	}
	if got := subscriber.pop(); got != nil {
		t.Fatalf("stale nonterminal was queued after terminal: %#v", got)
	}
}

func waitForDetachedStoppingAssignment(t *testing.T, service *Service, testID string) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		service.mu.RLock()
		_, stopping := service.stoppingTests[testID]
		_, assigned := service.assignments[testID]
		service.mu.RUnlock()
		if stopping && !assigned {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("StopTest did not detach the competing assignment")
		case <-ticker.C:
		}
	}
}
