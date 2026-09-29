package broker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

// DedicatedHostPreparer provisions exactly one already reserved host and waits
// for its authenticated transport. It must honor cancellation, persist every
// received pod identity, and never run user code or retry pod creation. The
// returned client must refer to the exact durably bound container. Implementors
// are trusted broker dependencies, never execution-pod or caller callbacks.
type DedicatedHostPreparer interface {
	PrepareHost(context.Context, Record) (*SupervisorClient, error)
}

// DedicatedWave owns one admitted peak/increase, including hosts not attached
// yet. Preparation has its own fixed deadline, independent of action timeouts.
// A caller may Renew concurrently only while its upstream owner lease is valid.
// No background loop in this type manufactures owner authority. All entry points
// are internal; a broker service must authenticate and authorize the binding.
type DedicatedWave struct {
	group    *DedicatedGroup
	records  []Record
	preparer DedicatedHostPreparer
	mu       sync.Mutex
	phase    string
	cancel   context.CancelFunc
}

func NewDedicatedWave(ctx context.Context, store *Store, epoch string, binding javarunner.HostBinding, sessions []string, memory int64, lease, prepare time.Duration, preparer DedicatedHostPreparer) (*DedicatedWave, error) {
	if preparer == nil {
		return nil, ErrState
	}
	g, e := NewDedicatedGroup(store, epoch, binding)
	if e != nil {
		return nil, e
	}
	rs, e := store.ReserveDedicatedWave(ctx, epoch, binding, sessions, memory, lease, prepare)
	if e != nil {
		return nil, e
	}
	for _, r := range rs {
		g.reservationIDs = append(g.reservationIDs, r.ID)
	}
	return &DedicatedWave{group: g, records: rs, preparer: preparer, phase: "reserved"}, nil
}

// Records returns immutable reservation identities in the original session order.
func (w *DedicatedWave) Records() []Record { return append([]Record(nil), w.records...) }

func (w *DedicatedWave) Prepare(parent context.Context) error {
	w.mu.Lock()
	if w.phase != "reserved" {
		w.mu.Unlock()
		return ErrState
	}
	ctx, cancel := context.WithDeadline(parent, w.records[0].PrepareUntil)
	w.cancel = cancel
	w.phase = "preparing"
	w.mu.Unlock()
	defer cancel()
	jobs := make(chan Record, len(w.records))
	for _, r := range w.records {
		jobs <- r
	}
	close(jobs)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failure error
	fail := func(e error) {
		mu.Lock()
		if failure == nil {
			failure = e
			cancel()
		}
		mu.Unlock()
	}
	for range min(8, len(w.records)) {
		wg.Go(func() {
			for r := range jobs {
				if ctx.Err() != nil {
					return
				}
				client, e := w.preparer.PrepareHost(ctx, r)
				if e != nil || client == nil {
					if client != nil {
						w.discardClient(r, client)
					}
					if e == nil {
						e = ErrState
					}
					fail(e)
					return
				}
				if e = w.group.Attach(ctx, r.ID, client); e != nil {
					// A receipt arriving after cancellation cannot resurrect an aborted wave.
					w.discardClient(r, client)
					fail(e)
					return
				}
				for {
					ready, e := w.group.PollReady(ctx, r.ID)
					if e != nil {
						fail(e)
						return
					}
					if ready {
						break
					}
					if e = waitPreparation(ctx, 50*time.Millisecond); e != nil {
						fail(e)
						return
					}
				}
			}
		})
	}
	wg.Wait()
	if failure == nil {
		failure = ctx.Err()
	}
	w.mu.Lock()
	if failure == nil && w.phase == "preparing" {
		w.phase = "ready"
		w.mu.Unlock()
		return nil
	}
	if failure == nil {
		failure = ErrFence
	}
	w.mu.Unlock()
	_ = w.Abort("wave_preparation_failed")
	return failure
}
func (w *DedicatedWave) discardClient(r Record, client *SupervisorClient) {
	// A malformed factory result must not authorize closing another host.
	if client.config.HostID == r.ID && client.config.BrokerEpoch == r.BrokerEpoch && client.config.ConfigurationSHA256 == r.Binding.ConfigurationSHA256 {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = client.Control(cleanup, "close")
		stop()
	}
	client.Close()
}
func waitPreparation(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (w *DedicatedWave) Commit(ctx context.Context) error {
	w.mu.Lock()
	if w.phase != "ready" {
		w.mu.Unlock()
		return ErrState
	}
	w.phase = "committing"
	w.mu.Unlock()
	if e := w.group.Commit(ctx); e != nil {
		_ = w.Abort("wave_commit_failed")
		return e
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.phase != "committing" {
		return ErrFence
	}
	w.phase = "committed"
	return nil
}
func (w *DedicatedWave) Invoke(ctx context.Context, id, method string, params map[string]string) (SupervisorResponse, error) {
	w.mu.Lock()
	phase := w.phase
	w.mu.Unlock()
	if phase != "committed" {
		return SupervisorResponse{}, ErrFence
	}
	return w.group.Invoke(ctx, id, method, params)
}

// Renew includes reserved/unattached hosts while preparation is underway. The
// durable lease is renewed before any attached supervisor. The two lanes remain
// independent of a blocked Java invocation. A failed renewal retires the wave.
func (w *DedicatedWave) Renew(ctx context.Context, lease time.Duration) error {
	if lease < time.Millisecond || lease > time.Minute {
		return ErrState
	}
	w.mu.Lock()
	phase := w.phase
	w.mu.Unlock()
	if phase == "retiring" {
		return ErrFence
	}
	w.group.mu.Lock()
	attached := w.group.copyHosts()
	w.group.mu.Unlock()
	var failure error
	for _, r := range w.records {
		if attached[r.ID] != nil {
			continue
		}
		if e := w.group.store.Renew(ctx, w.group.epoch, r.ID, lease); e != nil {
			failure = e
			break
		}
	}
	if failure == nil {
		failure = w.group.Renew(ctx, lease)
	}
	if failure != nil {
		_ = w.Abort("wave_renewal_failed")
	}
	return failure
}

// RenewHost is the dedicated per-Vuser lease lane. A healthy generator or peer
// must not keep an abandoned Vuser alive. Before attachment it renews only the
// exact preparation reservation; afterwards it also renews that supervisor.
func (w *DedicatedWave) RenewHost(ctx context.Context, id string, lease time.Duration) error {
	if lease < time.Millisecond || lease > time.Minute {
		return ErrState
	}
	w.mu.Lock()
	retiring := w.phase == "retiring"
	w.mu.Unlock()
	if retiring {
		return ErrFence
	}
	owned := false
	for _, r := range w.records {
		if r.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		return ErrState
	}
	w.group.mu.Lock()
	client := w.group.hosts[id]
	w.group.mu.Unlock()
	if client != nil && lease < client.config.LeaseTTL {
		return ErrState
	}
	if e := w.group.store.Renew(ctx, w.group.epoch, id, lease); e != nil {
		return e
	}
	if client != nil {
		if _, e := client.Control(ctx, "renew"); e != nil {
			_ = w.group.abortOne(id, "supervisor_renew_failed")
			return e
		}
	}
	return nil
}

// CloseHost is allowed only after the complete wave crossed the commit barrier.
// Closing a prepared peak is a wave abort, never partial admission.
func (w *DedicatedWave) CloseHost(id string) error {
	w.mu.Lock()
	committed := w.phase == "committed"
	w.mu.Unlock()
	if !committed || !w.group.owns(id) {
		return ErrState
	}
	return w.group.abortOne(id, "dedicated_vuser_closed")
}

func (w *DedicatedWave) Abort(reason string) error {
	if !name.MatchString(reason) {
		return ErrState
	}
	w.mu.Lock()
	w.phase = "retiring"
	if w.cancel != nil {
		w.cancel()
	}
	w.mu.Unlock()
	// Retire this exact wave, including its unattached reservations; a delayed
	// abort cannot retire a later admitted increase with the same binding.
	return w.group.Abort(reason)
}

// KubernetesHostPreparer connects the Kubernetes intent/receipt barrier to the
// authenticated supervisor barrier. Operator callbacks provide host-scoped
// transport material and routing; they must not execute Java or renew leases.
// Re-observation is safe; Provision itself is always called exactly once.
type KubernetesHostPreparer struct {
	Backend      *KubernetesBackend
	Store        *Store
	Epoch        string
	PodConfig    func(context.Context, Record) (ExecutionPodConfig, error)
	Connect      func(context.Context, Record, PodReceipt) (*SupervisorClient, error)
	PollInterval time.Duration
}

func (p *KubernetesHostPreparer) PrepareHost(ctx context.Context, r Record) (*SupervisorClient, error) {
	if p == nil || p.Backend == nil || p.Store == nil || p.PodConfig == nil || p.Connect == nil || p.Epoch != r.BrokerEpoch || r.State != javarunner.HostReserved || p.PollInterval < time.Millisecond || p.PollInterval > time.Second {
		return nil, ErrState
	}
	cfg, e := p.PodConfig(ctx, r)
	if e != nil {
		return nil, e
	}
	receipt, e := p.Backend.Provision(ctx, p.Store, p.Epoch, r.ID, cfg)
	if e != nil {
		return nil, e
	}
	for receipt.ContainerID == "" {
		if e = waitPreparation(ctx, p.PollInterval); e != nil {
			return nil, e
		}
		receipt, e = p.Backend.ObservePrepared(ctx, p.Store, p.Epoch, r.ID, cfg)
		if e != nil {
			return nil, e
		}
	}
	client, e := p.Connect(ctx, r, receipt)
	if e != nil || ctx.Err() != nil {
		if client != nil {
			client.Close()
		}
		return nil, errors.Join(e, ctx.Err())
	}
	if client == nil {
		return nil, ErrState
	}
	if e = client.AwaitTransport(ctx); e != nil {
		client.Close()
		return nil, e
	}
	return client, nil
}
