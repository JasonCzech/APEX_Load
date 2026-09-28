package broker

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

// PodReceipt comes from the trusted Kubernetes backend, not an execution pod.
// HostID/BrokerEpoch are verified provenance from the broker's create request.
// ContainerID may be empty while the pod is pending.
type PodReceipt struct {
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
	UID         string `json:"uid"`
	ContainerID string `json:"container_id"`
	HostID      string `json:"host_id"`
	BrokerEpoch string `json:"broker_epoch"`
}

// CleanupBackend separates discovery/deletion from positive runtime evidence.
// A production implementation must independently qualify its termination
// attestor. Neither Kubernetes NotFound nor deletion success proves termination.
// All methods MUST honor context deadlines. Delete must use a UID precondition;
// it must never delete a replacement pod merely because its name matches.
// Delete may acknowledge an already absent object. BoundaryTerminated must be
// independent of object existence and validate the entire exact boundary.
type CleanupBackend interface {
	Find(context.Context, string, string) (PodReceipt, error)
	Delete(context.Context, PodReceipt) error
	BoundaryTerminated(context.Context, PodReceipt) (bool, error)
}

// Reconciler acts only on sealed hosts, including prior epochs. Run it outside
// invocation queues. Unreachable nodes, absent pods, and attestor failures leave
// durable quarantine charged. No observation can adopt or restart Java state.
type Reconciler struct {
	store   *Store
	epoch   string
	backend CleanupBackend
	timeout time.Duration
}

func NewReconciler(store *Store, epoch string, backend CleanupBackend, timeout time.Duration) (*Reconciler, error) {
	if store == nil || !name.MatchString(epoch) || backend == nil || timeout < time.Millisecond || timeout > time.Minute {
		return nil, ErrState
	}
	return &Reconciler{store: store, epoch: epoch, backend: backend, timeout: timeout}, nil
}

// Step returns errors without discarding work for other hosts. Retry on the next
// monitor tick; only a committed exact-boundary receipt releases a reservation.
func (r *Reconciler) Step(ctx context.Context) error {
	records, _, err := r.store.Snapshot(ctx, r.epoch)
	if err != nil {
		return err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	jobs := make(chan Record, len(records))
	for _, record := range records {
		if record.State == javarunner.HostDraining || record.State == javarunner.HostQuarantined {
			jobs <- record
		}
	}
	close(jobs)
	var failures []error
	var mu sync.Mutex
	var workers sync.WaitGroup
	// A stalled node must not serialize every other host's cleanup. Bound
	// concurrency independently of host count and honor per-host deadlines.
	for range min(8, len(jobs)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for record := range jobs {
				if ctx.Err() != nil {
					return
				}
				stepCtx, cancel := context.WithTimeout(ctx, r.timeout)
				err := r.cleanup(stepCtx, record)
				cancel()
				if err != nil {
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
				}
			}
		}()
	}
	workers.Wait()
	if err := ctx.Err(); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (r *Reconciler) cleanup(ctx context.Context, record Record) error {
	pod, err := r.backend.Find(ctx, r.store.config.Namespace, record.PodName)
	if err != nil {
		if record.PodUID == "" {
			return ErrCleanup
		}
		// A missing pod record cannot erase durable identity. A qualified
		// runtime attestor may still prove that exact container stopped; an
		// API error/NotFound itself never supplies that proof.
		pod = PodReceipt{Namespace: r.store.config.Namespace, Name: record.PodName, UID: record.PodUID, ContainerID: record.ContainerID, HostID: record.ID, BrokerEpoch: record.BrokerEpoch}
	}
	if pod.Namespace != r.store.config.Namespace || pod.Name != record.PodName || pod.HostID != record.ID || pod.BrokerEpoch != record.BrokerEpoch || !name.MatchString(pod.UID) || pod.ContainerID != "" && !validContainer(pod.ContainerID) {
		// Mutated labels/annotations cannot veto deletion of the exact durable
		// UID. They still cannot supply container provenance or release proof.
		if record.PodUID != "" && record.PodUID == pod.UID && pod.Namespace == r.store.config.Namespace && pod.Name == record.PodName {
			_ = r.backend.Delete(ctx, PodReceipt{Namespace: r.store.config.Namespace, Name: record.PodName, UID: record.PodUID, ContainerID: record.ContainerID, HostID: record.ID, BrokerEpoch: record.BrokerEpoch})
		}
		return ErrCleanup
	}
	if record.PodUID != "" && record.PodUID != pod.UID || record.ContainerID != "" && record.ContainerID != pod.ContainerID {
		return ErrCleanup
	}
	if record.State == javarunner.HostQuarantined {
		// Save a late receipt before any cleanup side effect. A crash cannot
		// erase the identity merely because the original create call timed out.
		if err := r.store.BindPod(ctx, r.epoch, record.ID, pod.UID, pod.ContainerID); err != nil {
			return err
		}
	}
	if pod.ContainerID != "" {
		terminated, proofErr := r.backend.BoundaryTerminated(ctx, pod)
		if proofErr == nil && terminated {
			// Remove the terminal Kubernetes object too; otherwise host
			// reservations could be released while pod quota leaks forever.
			if err := r.backend.Delete(ctx, pod); err != nil {
				return ErrCleanup
			}
			return r.store.ConfirmTerminated(ctx, r.epoch, record.ID, pod.UID, pod.ContainerID)
		}
		// Attestation failure must not prevent attempting termination. It
		// still cannot turn a successful deletion request into release proof.
	}
	if err := r.backend.Delete(ctx, pod); err != nil {
		return ErrCleanup
	}
	return ErrCleanup
}
