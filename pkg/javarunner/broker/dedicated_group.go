package broker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

// DedicatedGroup connects authenticated host readiness to the durable barrier.
// This is an internal broker component, not an externally enabled runner API.
// One instance owns one prepared wave (initial peak or an explicitly admitted
// increase). No runtime can be adopted across broker restarts or replaced here.
type DedicatedGroup struct {
	store          *Store
	epoch          string
	binding        javarunner.HostBinding
	mu             sync.Mutex
	phase          string
	hosts          map[string]*SupervisorClient
	reservationIDs []string
}

func NewDedicatedGroup(store *Store, epoch string, binding javarunner.HostBinding) (*DedicatedGroup, error) {
	if store == nil || !name.MatchString(epoch) || !validBinding(binding) {
		return nil, ErrState
	}
	return &DedicatedGroup{store: store, epoch: epoch, binding: binding, phase: "preparing", hosts: map[string]*SupervisorClient{}}, nil
}

// Attach accepts only an exact, already bound Kubernetes container. PollReady
// performs the independent harness handshake; Kubernetes Ready is insufficient.
func (g *DedicatedGroup) Attach(ctx context.Context, id string, client *SupervisorClient) error {
	if client == nil || client.config.HostID != id || client.config.BrokerEpoch != g.epoch || client.config.ConfigurationSHA256 != g.binding.ConfigurationSHA256 {
		return ErrState
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.phase != "preparing" || g.hosts[id] != nil || !g.owns(id) {
		return ErrState
	}
	records, _, e := g.store.Snapshot(ctx, g.epoch)
	if e != nil {
		return e
	}
	for _, r := range records {
		if r.ID == id && r.Binding == g.binding && r.State == javarunner.HostPreparing && r.PodUID != "" && r.ContainerID != "" {
			g.hosts[id] = client
			return nil
		}
	}
	return ErrState
}
func (g *DedicatedGroup) PollReady(ctx context.Context, id string) (bool, error) {
	g.mu.Lock()
	client := g.hosts[id]
	phase := g.phase
	g.mu.Unlock()
	if client == nil || phase != "preparing" {
		return false, ErrState
	}
	response, e := client.Control(ctx, "ready")
	if e != nil {
		g.abortOne(id, "supervisor_readiness_failed")
		return false, e
	}
	if response.State == "preparing" {
		return false, nil
	}
	if response.State != "ready" {
		g.abortOne(id, "supervisor_readiness_invalid")
		return false, ErrState
	}
	// Ready is intentionally non-idempotent in the store. A polling receipt can
	// observe a previously saved Ready without issuing a second mutation.
	records, _, e := g.store.Snapshot(ctx, g.epoch)
	if e != nil {
		return false, e
	}
	for _, r := range records {
		if r.ID == id {
			if r.State == javarunner.HostReady {
				return true, nil
			}
			if e = g.store.Ready(ctx, g.epoch, id); e != nil {
				g.abortOne(id, "supervisor_readiness_expired")
				return false, e
			}
			return true, nil
		}
	}
	return false, ErrState
}
func (g *DedicatedGroup) Commit(ctx context.Context) error {
	g.mu.Lock()
	if g.phase != "preparing" || len(g.hosts) == 0 {
		g.mu.Unlock()
		return ErrState
	}
	g.phase = "committing"
	hosts := g.copyHosts()
	g.mu.Unlock()
	// All remote commits still run no user code. Local invocation remains shut
	// until every reply and the atomic durable group commit have succeeded.
	ids := make([]string, 0, len(hosts))
	for id := range hosts {
		ids = append(ids, id)
	}
	if e := parallelHostControl(ctx, hosts, func(ctx context.Context, _ string, client *SupervisorClient) error {
		_, e := client.Control(ctx, "commit")
		return e
	}); e != nil {
		_ = g.Abort("supervisor_commit_failed")
		return e
	}
	if e := g.store.Commit(ctx, g.epoch, ids); e != nil {
		_ = g.Abort("supervisor_commit_failed")
		return e
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.phase != "committing" {
		return ErrFence
	}
	g.phase = "committed"
	return nil
}

// Renew must be driven by a valid upstream owner lease. This component never
// renews itself indefinitely. Persist owner authority before sending renewal.
func (g *DedicatedGroup) Renew(ctx context.Context, lease time.Duration) error {
	g.mu.Lock()
	if g.phase == "retiring" {
		g.mu.Unlock()
		return ErrFence
	}
	hosts := g.copyHosts()
	g.mu.Unlock()
	// Closed hosts retain durable memory/host charges, but cannot receive new
	// leases. Their cleanup must not turn renewal of surviving Vusers into an
	// error. Unexpected expiry/failure of a live member still fences the wave.
	records, _, e := g.store.Snapshot(ctx, g.epoch)
	if e != nil {
		return e
	}
	for _, r := range records {
		if r.Failure == "dedicated_vuser_closed" && (r.State == javarunner.HostQuarantined || r.State == javarunner.HostReaped) {
			delete(hosts, r.ID)
		}
	}
	return parallelHostControl(ctx, hosts, func(ctx context.Context, id string, client *SupervisorClient) error {
		if lease < client.config.LeaseTTL || client.config.LeaseTTL < time.Millisecond {
			return ErrState
		}
		if e := g.store.Renew(ctx, g.epoch, id, lease); e != nil {
			if g.cooperativelyClosed(ctx, id) {
				return nil
			}
			_ = g.abortOne(id, "owner_lease_lost")
			return e
		}
		if _, e := client.Control(ctx, "renew"); e != nil {
			if g.cooperativelyClosed(ctx, id) {
				return nil
			}
			_ = g.abortOne(id, "supervisor_renew_failed")
			return e
		}
		return nil
	})
}
func (g *DedicatedGroup) Invoke(ctx context.Context, id, method string, params map[string]string) (SupervisorResponse, error) {
	g.mu.Lock()
	client := g.hosts[id]
	phase := g.phase
	g.mu.Unlock()
	if phase != "committed" || client == nil {
		return SupervisorResponse{}, ErrFence
	}
	if e := g.store.CanInvoke(ctx, g.epoch, id); e != nil {
		g.abortOne(id, "owner_lease_lost")
		return SupervisorResponse{}, e
	}
	out, e := client.Invoke(ctx, method, params)
	if e != nil {
		g.abortOne(id, "supervisor_invocation_failed")
		return out, e
	}
	if method == "end" {
		// Even cooperative end can leave native/background work in a dedicated
		// JVM. Retire the whole pod and keep occupancy until termination evidence.
		g.abortOne(id, "dedicated_vuser_closed")
	}
	return out, nil
}

// A wave-created group is bound to all its original reservations. Legacy
// component callers without an inventory retain the original binding barrier.
func (g *DedicatedGroup) owns(id string) bool {
	if g.reservationIDs == nil {
		return true
	}
	for _, member := range g.reservationIDs {
		if member == id {
			return true
		}
	}
	return false
}
func (g *DedicatedGroup) cooperativelyClosed(ctx context.Context, id string) bool {
	records, _, e := g.store.Snapshot(ctx, g.epoch)
	if e != nil {
		return false
	}
	for _, r := range records {
		if r.ID == id {
			return r.Failure == "dedicated_vuser_closed" && (r.State == javarunner.HostQuarantined || r.State == javarunner.HostReaped)
		}
	}
	return false
}
func (g *DedicatedGroup) copyHosts() map[string]*SupervisorClient {
	out := make(map[string]*SupervisorClient, len(g.hosts))
	for id, c := range g.hosts {
		out[id] = c
	}
	return out
}
func (g *DedicatedGroup) abortOne(id, reason string) error {
	// Caller cancellation cannot erase the durable cleanup obligation.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e := g.store.Abort(ctx, g.epoch, id, reason)
	g.mu.Lock()
	client := g.hosts[id]
	g.mu.Unlock()
	if client != nil {
		_, closeErr := client.Control(ctx, "close")
		client.Close()
		e = errors.Join(e, closeErr)
	}
	return e
}
func (g *DedicatedGroup) Abort(reason string) error {
	if !name.MatchString(reason) {
		return ErrState
	}
	g.mu.Lock()
	g.phase = "retiring"
	hosts := g.copyHosts()
	g.mu.Unlock()
	ids := make([]string, 0, len(hosts))
	for id := range hosts {
		ids = append(ids, id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var durableErr error
	if g.reservationIDs != nil {
		durableErr = g.store.AbortReservations(ctx, g.epoch, g.binding, g.reservationIDs, reason)
	} else {
		durableErr = g.store.AbortWave(ctx, g.epoch, g.binding, ids, reason)
	}
	closeErr := parallelHostControl(ctx, hosts, func(ctx context.Context, _ string, client *SupervisorClient) error {
		_, e := client.Control(ctx, "close")
		client.Close()
		return e
	})
	return errors.Join(durableErr, closeErr)
}

// Bounded concurrency keeps one unreachable host out of its peers' control lane.
func parallelHostControl(ctx context.Context, hosts map[string]*SupervisorClient, fn func(context.Context, string, *SupervisorClient) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	jobs := make(chan string, len(hosts))
	for id := range hosts {
		jobs <- id
	}
	close(jobs)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []error
	for range min(8, len(hosts)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				if ctx.Err() != nil {
					return
				}
				if e := fn(ctx, id, hosts[id]); e != nil {
					mu.Lock()
					failures = append(failures, e)
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		failures = append(failures, ctx.Err())
	}
	return errors.Join(failures...)
}
