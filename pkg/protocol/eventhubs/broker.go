package eventhubs

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// eventHubsClient abstracts the Event Hubs transport so the protocol can run
// against an in-memory mock (local shakeout / CI) or, in a network-enabled build
// (eventhubs_real), the azeventhubs-backed adapter. The real adapter is a stub
// in the default build (see unavailableClient).
type eventHubsClient interface {
	Send(ctx context.Context, hub string, ev event) (eventMeta, error)
	Receive(ctx context.Context, hub, group string, timeout time.Duration) (event, int64, error)
	HealthCheck(ctx context.Context) error
	Teardown(ctx context.Context) error
}

// event is a provider-neutral Event Hubs event.
type event struct {
	Body         []byte
	PartitionKey string
	PartitionID  string
	Properties   map[string]string
	Offset       int64
	SequenceNum  int64
	EnqueuedAt   time.Time
}

// eventMeta is the broker acknowledgement returned by a send.
type eventMeta struct {
	PartitionID string
	Offset      int64
	SequenceNum int64
	EnqueuedAt  time.Time
}

// memoryBroker is a process-local Event Hubs mock: a log per event hub
// (offset == sequence == base + index) with per-(hub, consumer-group) read
// cursors, mirroring the Kafka mock so produce-then-consume and consumer lag
// behave like a real namespace. Shakeout/CI only — real load uses the
// eventhubs_real adapter. To keep a long-lived process bounded, each hub's log is
// capped at maxBrokerLogPerHub records: the oldest are evicted and the per-hub
// base offset advances (offsets stay absolute, so cursors/lag stay correct).
type memoryBroker struct {
	mu      sync.Mutex
	logs    map[string][]event
	base    map[string]int64 // absolute offset of logs[hub][0] (evicted count)
	cursors map[string]int64
}

// maxBrokerLogPerHub bounds retained events per hub in the in-memory mock. It is
// generous enough that ordinary shakeout/CI runs never evict.
const maxBrokerLogPerHub = 50000

func newMemoryBroker() *memoryBroker {
	return &memoryBroker{logs: make(map[string][]event), base: make(map[string]int64), cursors: make(map[string]int64)}
}

// sharedBroker lets multiple VUser goroutines exercise the same mock hubs.
var sharedBroker = newMemoryBroker()

func cursorKey(hub, group string) string { return hub + "\x00" + group }

func (b *memoryBroker) send(hub string, ev event) eventMeta {
	b.mu.Lock()
	defer b.mu.Unlock()
	offset := b.base[hub] + int64(len(b.logs[hub]))
	ev.Offset = offset
	ev.SequenceNum = offset
	if ev.EnqueuedAt.IsZero() {
		ev.EnqueuedAt = time.Now()
	}
	b.logs[hub] = append(b.logs[hub], ev)
	// Bound retained events: evict the oldest and advance the base offset.
	if over := len(b.logs[hub]) - maxBrokerLogPerHub; over > 0 {
		b.logs[hub] = append(b.logs[hub][:0], b.logs[hub][over:]...)
		b.base[hub] += int64(over)
	}
	return eventMeta{PartitionID: ev.PartitionID, Offset: offset, SequenceNum: offset, EnqueuedAt: ev.EnqueuedAt}
}

func (b *memoryBroker) receive(hub, group string) (event, int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	log := b.logs[hub]
	base := b.base[hub]
	end := base + int64(len(log))
	key := cursorKey(hub, group)
	pos := b.cursors[key]
	if pos < base {
		// Cursor lagged behind eviction; skip to the earliest retained event.
		pos = base
	}
	if pos >= end {
		return event{}, 0, false
	}
	ev := log[pos-base]
	b.cursors[key] = pos + 1
	return ev, end - (pos + 1), true
}

// mockClient adapts the in-memory broker to the eventHubsClient interface.
type mockClient struct{ broker *memoryBroker }

func (c mockClient) Send(_ context.Context, hub string, ev event) (eventMeta, error) {
	if hub == "" {
		return eventMeta{}, fmt.Errorf("event hub name is required")
	}
	return c.broker.send(hub, ev), nil
}

func (c mockClient) Receive(ctx context.Context, hub, group string, timeout time.Duration) (event, int64, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if ev, lag, ok := c.broker.receive(hub, group); ok {
			return ev, lag, nil
		}
		select {
		case <-ctx.Done():
			return event{}, 0, ctx.Err()
		case <-deadline.C:
			return event{}, 0, fmt.Errorf("no event received from %q within %s: %w", hub, timeout, errNoEvent)
		case <-tick.C:
		}
	}
}

func (c mockClient) HealthCheck(context.Context) error {
	if c.broker == nil {
		return fmt.Errorf("in-memory event hubs broker is not initialized")
	}
	return nil
}

func (c mockClient) Teardown(context.Context) error { return nil }

// unavailableClient is returned in non-mock mode when the real azeventhubs
// adapter is not compiled in; it fails closed with actionable guidance, mirroring
// the Kafka/JMS stubs.
type unavailableClient struct{ namespace string }

func (c unavailableClient) err() error {
	return fmt.Errorf("real Azure Event Hubs adapter is not available in this build; enable config.eventhubs.mock for shakeout or build with -tags eventhubs_real")
}

func (c unavailableClient) Send(context.Context, string, event) (eventMeta, error) {
	return eventMeta{}, c.err()
}

func (c unavailableClient) Receive(context.Context, string, string, time.Duration) (event, int64, error) {
	return event{}, 0, c.err()
}

func (c unavailableClient) HealthCheck(context.Context) error {
	if c.namespace == "" {
		return fmt.Errorf("config.eventhubs.namespace or connection_string is required")
	}
	return c.err()
}

func (c unavailableClient) Teardown(context.Context) error { return nil }
