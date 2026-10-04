package http

import (
	"context"
	"fmt"
	"io"
	"net"
	stdhttp "net/http"
	"net/http/httptrace"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Connection leases count physical sockets across every version and derived
// transport of one VUser. HTTP/2 streams do not release socket leases.
type httpConnectionBudget struct {
	mu        sync.Mutex
	limit     int
	idleLimit int
	counts    map[string]int
	owners    map[*httpConnectionOwner]struct{}
	changed   chan struct{}
}

type httpConnectionOwner struct {
	budget       *httpConnectionBudget
	transport    *stdhttp.Transport
	baseDial     func(context.Context, string, string) (net.Conn, error)
	mu           sync.Mutex
	pendingFresh atomic.Int64
	open         atomic.Int64
	lastUsed     atomic.Int64
	retired      atomic.Bool
}

func newHTTPConnectionBudget(limit, idleLimit int) *httpConnectionBudget {
	return &httpConnectionBudget{limit: limit, idleLimit: idleLimit, counts: make(map[string]int), owners: make(map[*httpConnectionOwner]struct{}), changed: make(chan struct{}, 1)}
}

func (budget *httpConnectionBudget) track(transport *stdhttp.Transport, dial func(context.Context, string, string) (net.Conn, error)) *httpConnectionOwner {
	owner := &httpConnectionOwner{budget: budget, transport: transport, baseDial: dial}
	budget.mu.Lock()
	budget.owners[owner] = struct{}{}
	budget.mu.Unlock()
	transport.DialContext = owner.dialContext
	return owner
}

func (budget *httpConnectionBudget) notify() {
	select {
	case budget.changed <- struct{}{}:
	default:
	}
}

func (budget *httpConnectionBudget) snapshotOwners() []*httpConnectionOwner {
	budget.mu.Lock()
	owners := make([]*httpConnectionOwner, 0, len(budget.owners))
	for owner := range budget.owners {
		owners = append(owners, owner)
	}
	budget.mu.Unlock()
	return owners
}

func (budget *httpConnectionBudget) evictIdle() {
	for _, owner := range budget.snapshotOwners() {
		owner.mu.Lock()
		// A freshly dialed HTTP/2 socket can enter Go's pool before its first
		// stream is reserved. Protect only that interval. Native transport
		// locks safely select idle sockets once GotConn has assigned a stream,
		// including when this owner has other active or internally queued calls.
		if owner.pendingFresh.Load() == 0 {
			owner.transport.CloseIdleConnections()
		}
		owner.mu.Unlock()
	}
}

func (budget *httpConnectionBudget) trimIdle() {
	owners := budget.snapshotOwners()
	sort.Slice(owners, func(i, j int) bool { return owners[i].lastUsed.Load() < owners[j].lastUsed.Load() })
	// Open sockets are a conservative bound on idle sockets. Native transport
	// keeps assigned HTTP/1 calls and active/reserved HTTP/2 streams alive;
	// counting those here must never lead us to close them ourselves.
	open := int64(0)
	for _, owner := range owners {
		open += owner.open.Load()
	}
	for _, owner := range owners {
		if open <= int64(budget.idleLimit) {
			return
		}
		owner.mu.Lock()
		if owner.pendingFresh.Load() == 0 {
			before := owner.open.Load()
			owner.transport.CloseIdleConnections()
			if closed := before - owner.open.Load(); closed > 0 {
				open -= closed
			}
		}
		owner.mu.Unlock()
	}
}

// Go may continue a speculative dial after its initiating RoundTrip reuses a
// different socket or returns. Activity state prevents that detached dial from
// making a finished operation busy again. Only guard creation needs owner.mu;
// guard clearing is atomic because native idle closure can call Conn.Close while
// holding owner.mu. No physical-budget lock is held while acquiring owner.mu.
type httpConnectionActivityKey struct{}
type httpConnectionActivity struct {
	owner    *httpConnectionOwner
	done     bool
	assigned bool
	pending  map[*httpFreshConnectionGuard]struct{}
}

type httpFreshConnectionGuard struct {
	owner   *httpConnectionOwner
	pending atomic.Bool
}

func (activity *httpConnectionActivity) beginDial() *httpFreshConnectionGuard {
	if activity == nil {
		return nil
	}
	activity.owner.mu.Lock()
	defer activity.owner.mu.Unlock()
	for guard := range activity.pending {
		if !guard.pending.Load() {
			delete(activity.pending, guard)
		}
	}
	if activity.done || activity.assigned {
		return nil
	}
	guard := &httpFreshConnectionGuard{owner: activity.owner}
	guard.pending.Store(true)
	activity.owner.pendingFresh.Add(1)
	if activity.pending == nil {
		activity.pending = make(map[*httpFreshConnectionGuard]struct{})
	}
	activity.pending[guard] = struct{}{}
	return guard
}

func (guard *httpFreshConnectionGuard) clear() {
	if guard != nil && guard.pending.Swap(false) {
		guard.owner.pendingFresh.Add(-1)
		guard.owner.budget.notify()
	}
}

func (activity *httpConnectionActivity) clearPendingLocked() {
	for guard := range activity.pending {
		guard.clear()
		delete(activity.pending, guard)
	}
}

func (activity *httpConnectionActivity) getConn() {
	activity.owner.mu.Lock()
	if !activity.done {
		activity.assigned = false
	}
	activity.owner.mu.Unlock()
}

func (activity *httpConnectionActivity) gotConn() {
	activity.owner.mu.Lock()
	if !activity.done {
		activity.assigned = true
		activity.clearPendingLocked()
	}
	activity.owner.mu.Unlock()
}

func (activity *httpConnectionActivity) finish() {
	owner := activity.owner
	owner.mu.Lock()
	if activity.done {
		owner.mu.Unlock()
		return
	}
	activity.done = true
	activity.clearPendingLocked()
	owner.mu.Unlock()
	if owner.retired.Load() {
		owner.transport.CloseIdleConnections()
	}
	owner.budget.notify()
	owner.budget.trimIdle()
}

func (owner *httpConnectionOwner) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	key := strings.ToLower(strings.TrimSpace(address))
	budget := owner.budget
	activity, _ := ctx.Value(httpConnectionActivityKey{}).(*httpConnectionActivity)
	if activity != nil && activity.owner != owner {
		activity = nil
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		budget.mu.Lock()
		if owner.retired.Load() {
			budget.mu.Unlock()
			return nil, net.ErrClosed
		}
		if budget.counts[key] < budget.limit {
			budget.counts[key]++
			owner.open.Add(1)
			budget.mu.Unlock()
			guard := activity.beginDial()
			conn, err := owner.baseDial(ctx, network, address)
			if err == nil && conn == nil {
				err = fmt.Errorf("HTTP dial returned no connection")
			}
			if err != nil {
				if conn != nil {
					_ = conn.Close()
				}
				guard.clear()
				budget.release(key, owner)
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				_ = conn.Close()
				guard.clear()
				budget.release(key, owner)
				return nil, err
			}
			if owner.retired.Load() {
				_ = conn.Close()
				guard.clear()
				budget.release(key, owner)
				return nil, net.ErrClosed
			}
			return &leasedHTTPConn{Conn: conn, owner: owner, key: key, guard: guard}, nil
		}
		budget.mu.Unlock()
		budget.evictIdle()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-budget.changed:
		case <-ticker.C:
		}
	}
}

func (budget *httpConnectionBudget) release(key string, owner *httpConnectionOwner) {
	budget.mu.Lock()
	if budget.counts[key] <= 1 {
		delete(budget.counts, key)
	} else {
		budget.counts[key]--
	}
	if owner.open.Add(-1) == 0 && owner.retired.Load() {
		delete(budget.owners, owner)
	}
	budget.mu.Unlock()
	budget.notify()
}

type leasedHTTPConn struct {
	net.Conn
	owner *httpConnectionOwner
	key   string
	once  sync.Once
	guard *httpFreshConnectionGuard
}

func (conn *leasedHTTPConn) Close() error {
	err := conn.Conn.Close()
	conn.once.Do(func() {
		conn.guard.clear()
		conn.owner.budget.release(conn.key, conn.owner)
	})
	return err
}

func (owner *httpConnectionOwner) RoundTrip(request *stdhttp.Request) (*stdhttp.Response, error) {
	activity := &httpConnectionActivity{owner: owner}
	ctx := context.WithValue(request.Context(), httpConnectionActivityKey{}, activity)
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GetConn: func(string) { activity.getConn() },
		GotConn: func(httptrace.GotConnInfo) { activity.gotConn() },
	})
	owner.lastUsed.Store(time.Now().UnixNano())
	resp, err := owner.transport.RoundTrip(request.WithContext(ctx))
	if err != nil {
		activity.finish()
		return resp, err
	}
	resp.Body = &connectionActivityBody{ReadCloser: resp.Body, activity: activity}
	return resp, nil
}

func (owner *httpConnectionOwner) retire() {
	owner.retired.Store(true)
	owner.transport.CloseIdleConnections()
	owner.budget.mu.Lock()
	if owner.open.Load() == 0 {
		delete(owner.budget.owners, owner)
	}
	owner.budget.mu.Unlock()
	owner.budget.notify()
}

type connectionActivityBody struct {
	io.ReadCloser
	activity *httpConnectionActivity
	once     sync.Once
}

func (body *connectionActivityBody) finish() { body.once.Do(body.activity.finish) }
func (body *connectionActivityBody) Read(data []byte) (int, error) {
	n, err := body.ReadCloser.Read(data)
	if err == io.EOF {
		body.finish()
	}
	return n, err
}
func (body *connectionActivityBody) Close() error {
	err := body.ReadCloser.Close()
	body.finish()
	// HTTP/2 EOF can be observed before its stream-cleanup goroutine removes
	// the reservation. Close waits for that cleanup; repeat idle admission now
	// that native transport can classify the completed socket as idle.
	body.activity.owner.budget.trimIdle()
	return err
}

func (budget *httpConnectionBudget) closeIdle() {
	for _, owner := range budget.snapshotOwners() {
		owner.transport.CloseIdleConnections()
	}
	budget.notify()
}
