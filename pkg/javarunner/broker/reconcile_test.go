package broker

import (
	"context"
	"errors"
	"strings"
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
	noStartCalls                 int
	noStartProven                bool
	noStartErr                   error
	noStartIntent                UnstartedPod
	deleteBlock                  bool
	findBlock                    bool
}

func (b *cleanupBackend) Find(ctx context.Context, _, _ string) (PodReceipt, error) {
	if b.findBlock {
		<-ctx.Done()
		return PodReceipt{}, ctx.Err()
	}
	return b.pod, b.findErr
}
func (b *cleanupBackend) Delete(ctx context.Context, p PodReceipt) error {
	// A nil result models both UID-preconditioned deletion and a subsequent
	// authoritative observation that the object is absent.
	b.deleted = append(b.deleted, p)
	if b.deleteBlock {
		<-ctx.Done()
		return ctx.Err()
	}
	return b.deleteErr
}
func (b *cleanupBackend) BoundaryTerminated(context.Context, PodReceipt) (bool, error) {
	b.proofCalls++
	return b.terminated, b.proofErr
}
func (b *cleanupBackend) NeverStarted(_ context.Context, intent UnstartedPod) (bool, error) {
	b.noStartCalls++
	b.noStartIntent = intent
	return b.noStartProven, b.noStartErr
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
		{"object_removed_without_boundary_proof", func(*cleanupBackend) {}, 1, 1, false},
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

// This mock completes deletion and observes object absence immediately.
func (b *parallelCleanupBackend) Delete(context.Context, PodReceipt) error { return nil }
func (b *parallelCleanupBackend) BoundaryTerminated(context.Context, PodReceipt) (bool, error) {
	return true, nil
}
func (b *parallelCleanupBackend) NeverStarted(context.Context, UnstartedPod) (bool, error) {
	return false, nil
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

func TestReconcilePendingPodNeedsDeletionAndIndependentNoStartProofAfterRestart(t *testing.T) {
	s, old, path := newStore(t)
	host := reserve(t, s, old)
	policy := strings.Repeat("a", 64)
	if e := s.BeginPreparePod(t.Context(), old, host.ID, policy); e != nil {
		t.Fatal(e)
	}
	if e := s.BindPod(t.Context(), old, host.ID, "pending-uid", ""); e != nil {
		t.Fatal(e)
	}
	if e := s.Abort(t.Context(), old, host.ID, "preparation_expired"); e != nil {
		t.Fatal(e)
	}
	restarted, e := Open(t.Context(), path, testConfig, false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	epoch, e := restarted.StartEpoch(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	b := &cleanupBackend{pod: PodReceipt{Namespace: testConfig.Namespace, Name: host.PodName, UID: "pending-uid", HostID: host.ID, BrokerEpoch: old}}
	r, e := NewReconciler(restarted, epoch, b, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Step(t.Context()); !errors.Is(e, ErrCleanup) || b.noStartCalls != 1 || len(b.deleted) != 1 || b.deleted[0].UID != "pending-uid" {
		t.Fatal("deletion acknowledgement counted as proof", e, b)
	}
	if b.noStartIntent.PodPolicySHA256 != policy || b.noStartIntent.UID != "pending-uid" || b.noStartIntent.BrokerEpoch != old || record(t, restarted, epoch, host.ID).State != javarunner.HostQuarantined {
		t.Fatal("lost immutable creation identity", b.noStartIntent)
	}
	b.deleteErr = errors.New("partition")
	if e = r.Step(t.Context()); !errors.Is(e, ErrCleanup) || b.noStartCalls != 2 || record(t, restarted, epoch, host.ID).State != javarunner.HostQuarantined {
		t.Fatal("failed deletion skipped attestation or released without proof", e, b)
	}
	b.noStartProven = true
	b.findErr = errors.New("not found after deletion")
	b.deleteBlock = true
	if e = r.Step(t.Context()); e != nil || b.noStartCalls != 3 || record(t, restarted, epoch, host.ID).State != javarunner.HostReaped {
		t.Fatal("independent attestor proof did not release after local delete failure", e)
	}
}

func TestReconcileUnknownUIDStillAttestsAfterKubernetesFindTimeout(t *testing.T) {
	s, epoch, _ := newStore(t)
	host := reserve(t, s, epoch)
	policy := strings.Repeat("a", 64)
	if e := s.BeginPreparePod(t.Context(), epoch, host.ID, policy); e != nil {
		t.Fatal(e)
	}
	if e := s.Abort(t.Context(), epoch, host.ID, "create_uncertain"); e != nil {
		t.Fatal(e)
	}
	b := &cleanupBackend{findBlock: true}
	r, e := NewReconciler(s, epoch, b, 240*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Step(t.Context()); !errors.Is(e, ErrCleanup) || b.noStartCalls != 1 || record(t, s, epoch, host.ID).State != javarunner.HostQuarantined {
		t.Fatal("blocked discovery skipped no-start attestation or released without proof", e, b.noStartCalls)
	}
	if b.noStartIntent.UID != "" || b.noStartIntent.PodPolicySHA256 != policy {
		t.Fatal("attestation lost durable intent", b.noStartIntent)
	}
	b.noStartProven = true
	if e = r.Step(t.Context()); e != nil || b.noStartCalls != 2 || record(t, s, epoch, host.ID).State != javarunner.HostReaped {
		t.Fatal("positive proof could not release during API partition", e, b.noStartCalls)
	}
}

func TestReconcileInvalidPendingProvenanceUsesOnlyDurableIntent(t *testing.T) {
	for _, tc := range []struct {
		name         string
		bindUID      string
		mutate       func(*PodReceipt)
		wantProof    bool
		wantDeletion bool
	}{
		{"known_uid_wrong_host", "pending-uid", func(p *PodReceipt) { p.HostID = "other" }, true, true},
		{"known_uid_wrong_epoch", "pending-uid", func(p *PodReceipt) { p.BrokerEpoch = "other" }, true, true},
		{"unknown_uid_wrong_host", "", func(p *PodReceipt) { p.HostID = "other" }, true, false},
		{"unknown_uid_wrong_epoch", "", func(p *PodReceipt) { p.BrokerEpoch = "other" }, true, false},
		{"known_uid_mismatch", "pending-uid", func(p *PodReceipt) { p.UID = "replacement" }, false, false},
		{"known_uid_bad_container", "pending-uid", func(p *PodReceipt) { p.ContainerID = "invalid" }, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, epoch, _ := newStore(t)
			host := reserve(t, s, epoch)
			policy := strings.Repeat("a", 64)
			if e := s.BeginPreparePod(t.Context(), epoch, host.ID, policy); e != nil {
				t.Fatal(e)
			}
			if tc.bindUID != "" {
				if e := s.BindPod(t.Context(), epoch, host.ID, tc.bindUID, ""); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.Abort(t.Context(), epoch, host.ID, "create_uncertain"); e != nil {
				t.Fatal(e)
			}
			b := &cleanupBackend{pod: PodReceipt{Namespace: testConfig.Namespace, Name: host.PodName, UID: "pending-uid", HostID: host.ID, BrokerEpoch: epoch}, noStartProven: true}
			tc.mutate(&b.pod)
			r, e := NewReconciler(s, epoch, b, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			e = r.Step(t.Context())
			if tc.wantProof && e != nil || !tc.wantProof && !errors.Is(e, ErrCleanup) {
				t.Fatal(e)
			}
			if (b.noStartCalls == 1) != tc.wantProof || (len(b.deleted) == 1) != tc.wantDeletion {
				t.Fatal("wrong cleanup path", b.noStartCalls, b.deleted)
			}
			if tc.wantProof && (b.noStartIntent.UID != tc.bindUID || b.noStartIntent.PodPolicySHA256 != policy) {
				t.Fatal("untrusted observation entered proof request", b.noStartIntent)
			}
			if (record(t, s, epoch, host.ID).State == javarunner.HostReaped) != tc.wantProof {
				t.Fatal("capacity release mismatch")
			}
		})
	}
}

func TestReconcileObservedContainerCannotUseNoStartProof(t *testing.T) {
	s, epoch, _ := newStore(t)
	host := reserve(t, s, epoch)
	if e := s.BeginPreparePod(t.Context(), epoch, host.ID, strings.Repeat("a", 64)); e != nil {
		t.Fatal(e)
	}
	if e := s.Abort(t.Context(), epoch, host.ID, "create_uncertain"); e != nil {
		t.Fatal(e)
	}
	b := &cleanupBackend{pod: PodReceipt{Namespace: testConfig.Namespace, Name: host.PodName, UID: "late-uid", ContainerID: "containerd://late", HostID: host.ID, BrokerEpoch: epoch}, noStartProven: true}
	r, e := NewReconciler(s, epoch, b, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Step(t.Context()); !errors.Is(e, ErrCleanup) || b.noStartCalls != 0 || b.proofCalls != 1 || record(t, s, epoch, host.ID).State != javarunner.HostQuarantined {
		t.Fatal("started container accepted no-start proof", e, b)
	}
	b.terminated = true
	if e = r.Step(t.Context()); e != nil || record(t, s, epoch, host.ID).State != javarunner.HostReaped {
		t.Fatal("exact container proof did not release", e)
	}
}
