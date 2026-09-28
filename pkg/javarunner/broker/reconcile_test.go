package broker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

type cleanupBackend struct {
	pod                          PodReceipt
	findErr, proofErr, deleteErr error
	terminated                   bool
	deleted                      []PodReceipt
	proofCalls                   int
}

func (b *cleanupBackend) Find(context.Context, string, string) (PodReceipt, error) {
	return b.pod, b.findErr
}
func (b *cleanupBackend) Delete(_ context.Context, p PodReceipt) error {
	b.deleted = append(b.deleted, p)
	return b.deleteErr
}
func (b *cleanupBackend) BoundaryTerminated(context.Context, PodReceipt) (bool, error) {
	b.proofCalls++
	return b.terminated, b.proofErr
}

func TestReconcileRequiresPositiveExactBoundaryEvidence(t *testing.T) {
	for _, tc := range []struct {
		name            string
		mutate          func(*cleanupBackend)
		deletes, proofs int
		release         bool
	}{
		{"absent", func(b *cleanupBackend) { b.findErr = errors.New("not found") }, 1, 1, false},
		{"absent_with_independent_proof", func(b *cleanupBackend) { b.findErr = errors.New("not found"); b.terminated = true }, 1, 1, true},
		{"node_unreachable", func(b *cleanupBackend) { b.proofErr = context.DeadlineExceeded }, 1, 1, false},
		{"delete_accepted", func(*cleanupBackend) {}, 1, 1, false},
		{"delete_failed", func(b *cleanupBackend) { b.deleteErr = errors.New("partition") }, 1, 1, false},
		{"replacement_uid", func(b *cleanupBackend) { b.pod.UID = "replacement" }, 0, 0, false},
		{"replacement_container", func(b *cleanupBackend) { b.pod.ContainerID = "containerd://replacement" }, 0, 0, false},
		{"wrong_namespace", func(b *cleanupBackend) { b.pod.Namespace = "other" }, 0, 0, false},
		{"wrong_host", func(b *cleanupBackend) { b.pod.HostID = "other" }, 1, 0, false},
		{"wrong_epoch", func(b *cleanupBackend) { b.pod.BrokerEpoch = "other" }, 1, 0, false},
		{"positive", func(b *cleanupBackend) { b.terminated = true }, 1, 1, true},
		{"positive_delete_failed", func(b *cleanupBackend) { b.terminated = true; b.deleteErr = errors.New("partition") }, 1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, epoch, _ := newStore(t)
			host := reserve(t, s, epoch)
			ready(t, s, epoch, host)
			if err := s.Abort(t.Context(), epoch, host.ID, "lease_lost"); err != nil {
				t.Fatal(err)
			}
			b := &cleanupBackend{pod: PodReceipt{Namespace: testConfig.Namespace, Name: host.PodName, UID: "uid-" + host.ID, ContainerID: "containerd://" + host.ID, HostID: host.ID, BrokerEpoch: epoch}}
			tc.mutate(b)
			r, err := NewReconciler(s, epoch, b, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			err = r.Step(t.Context())
			if tc.release && err != nil || !tc.release && !errors.Is(err, ErrCleanup) {
				t.Fatal(err)
			}
			_, u, err := s.Snapshot(t.Context(), epoch)
			if err != nil {
				t.Fatal(err)
			}
			if (u.Hosts == 0) != tc.release || len(b.deleted) != tc.deletes || b.proofCalls != tc.proofs {
				t.Fatalf("usage=%+v deletes=%d proofs=%d", u, len(b.deleted), b.proofCalls)
			}
			if len(b.deleted) > 0 && b.deleted[0].UID != "uid-"+host.ID {
				t.Fatal("deletion lost UID precondition")
			}
		})
	}
}

func TestReconcileLatePendingPodRetainsReservation(t *testing.T) {
	s, old, _ := newStore(t)
	host := reserve(t, s, old)
	if err := s.BeginPrepare(t.Context(), old, host.ID); err != nil {
		t.Fatal(err)
	}
	epoch, err := s.StartEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	b := &cleanupBackend{pod: PodReceipt{Namespace: testConfig.Namespace, Name: host.PodName, UID: "late", HostID: host.ID, BrokerEpoch: old}}
	r, err := NewReconciler(s, epoch, b, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Step(t.Context()); !errors.Is(err, ErrCleanup) {
		t.Fatal(err)
	}
	if saved := record(t, s, epoch, host.ID); saved.PodUID != "late" || saved.ContainerID != "" || saved.State != javarunner.HostQuarantined {
		t.Fatal(saved)
	}
	if b.proofCalls != 0 || len(b.deleted) != 1 {
		t.Fatal("pending pod treated as terminated")
	}
	b.pod.ContainerID = "containerd://late"
	b.terminated = true
	if err := r.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if saved := record(t, s, epoch, host.ID); saved.State != javarunner.HostReaped {
		t.Fatal(saved)
	}
	if err := r.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b.proofCalls != 1 {
		t.Fatal("replayed released cleanup")
	}
}

func TestReconcileDoesNotTouchLiveHostsOrRunAfterFencing(t *testing.T) {
	s, epoch, _ := newStore(t)
	host := reserve(t, s, epoch)
	ready(t, s, epoch, host)
	b := &cleanupBackend{}
	r, err := NewReconciler(s, epoch, b, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(b.deleted) != 0 || b.proofCalls != 0 {
		t.Fatal("live host cleanup")
	}
	if _, err := s.StartEpoch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.Step(t.Context()); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
}

type parallelCleanupBackend struct {
	pods             map[string]PodReceipt
	blockedName      string
	started, release chan struct{}
}

func (b *parallelCleanupBackend) Find(ctx context.Context, _ string, name string) (PodReceipt, error) {
	if name == b.blockedName {
		close(b.started)
		select {
		case <-b.release:
		case <-ctx.Done():
			return PodReceipt{}, ctx.Err()
		}
	}
	return b.pods[name], nil
}
func (b *parallelCleanupBackend) Delete(context.Context, PodReceipt) error { return nil }
func (b *parallelCleanupBackend) BoundaryTerminated(context.Context, PodReceipt) (bool, error) {
	return true, nil
}

func TestReconcileStalledNodeDoesNotBlockOtherCleanup(t *testing.T) {
	s, epoch, _ := newStore(t)
	a, b := reserve(t, s, epoch), reserve(t, s, epoch)
	backend := &parallelCleanupBackend{pods: map[string]PodReceipt{}, blockedName: a.PodName, started: make(chan struct{}), release: make(chan struct{})}
	for _, host := range []Record{a, b} {
		ready(t, s, epoch, host)
		if err := s.Abort(t.Context(), epoch, host.ID, "abort"); err != nil {
			t.Fatal(err)
		}
		backend.pods[host.PodName] = PodReceipt{Namespace: testConfig.Namespace, Name: host.PodName, UID: "uid-" + host.ID, ContainerID: "containerd://" + host.ID, HostID: host.ID, BrokerEpoch: epoch}
	}
	r, err := NewReconciler(s, epoch, backend, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.Step(t.Context()) }()
	<-backend.started
	deadline := time.Now().Add(time.Second)
	for record(t, s, epoch, b.ID).State != javarunner.HostReaped {
		if time.Now().After(deadline) {
			close(backend.release)
			<-done
			t.Fatal("stalled node serialized peer cleanup")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("blocked cleanup unexpectedly finished: %v", err)
	default:
	}
	close(backend.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReconcileDeletesKnownUIDEvenWithoutContainerOrDiscovery(t *testing.T) {
	s, epoch, _ := newStore(t)
	r := reserve(t, s, epoch)
	if e := s.BeginPrepare(t.Context(), epoch, r.ID); e != nil {
		t.Fatal(e)
	}
	if e := s.BindPod(t.Context(), epoch, r.ID, "known-uid", ""); e != nil {
		t.Fatal(e)
	}
	if e := s.Abort(t.Context(), epoch, r.ID, "java_pod_policy_changed"); e != nil {
		t.Fatal(e)
	}
	b := &cleanupBackend{findErr: errors.New("unreachable")}
	reconciler, _ := NewReconciler(s, epoch, b, time.Second)
	if e := reconciler.Step(t.Context()); !errors.Is(e, ErrCleanup) {
		t.Fatal(e)
	}
	if len(b.deleted) != 1 || b.deleted[0].UID != "known-uid" || b.proofCalls != 0 {
		t.Fatal("lost exact-UID cleanup", b)
	}
	if got := record(t, s, epoch, r.ID); got.State != javarunner.HostQuarantined {
		t.Fatal("unproved reservation released", got)
	}
}
