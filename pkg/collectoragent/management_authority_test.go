package collectoragent

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Model a deadline whose timer callback has not run yet. Its immutable
// deadline is real; Err and Done deliberately do not announce expiry.
type managementDelayedTimerContext struct {
	context.Context
	at      time.Time
	reads   atomic.Int64
	onThird chan struct{}
}

func (c *managementDelayedTimerContext) Deadline() (time.Time, bool) {
	if c.reads.Add(1) == 3 && c.onThird != nil {
		close(c.onThird)
	}
	return c.at, true
}
func (*managementDelayedTimerContext) Err() error            { return nil }
func (*managementDelayedTimerContext) Done() <-chan struct{} { return nil }

func TestManagementDelayedTimerDeadlineRejectsAdmission(t *testing.T) {
	ctx := &managementDelayedTimerContext{Context: context.Background(), at: time.Now().Add(-time.Second)}
	grant := managementTestGrant("instance")
	grant.Pack = "solace-semp"
	poll := newManagementObservation(managementTestTarget(), grant, ManagementCredentials{})
	poll.origins = newManagementOriginGate([]AdapterTarget{{Management: &poll.target}})
	called := false
	err := poll.request(ctx, poll.target.Endpoints[0], false, func(context.Context) error { called = true; return nil })
	poll.finish()
	requests, pages := poll.counts()
	origin := poll.origins.origins[managementOriginKey(poll.target.Endpoints[0])]
	if !errors.Is(err, context.DeadlineExceeded) || called || requests != 0 || pages != 0 || len(poll.requestSlots) != 0 || len(origin.slots) != 0 || origin.poll != "" || !origin.next.IsZero() {
		t.Fatal("delayed timer admitted expired provider authority")
	}
	if release, err := poll.origins.admit(ctx, poll.target.Endpoints[0], grant.PollID, grant.Pack); !errors.Is(err, context.DeadlineExceeded) || release != nil {
		t.Fatal("origin cadence changed under an expired deadline")
	}
}

func TestManagementDelayedTimerQueueExpiryRejectsCallback(t *testing.T) {
	for _, queue := range []string{"poll", "origin"} {
		t.Run(queue, func(t *testing.T) {
			ctx := &managementDelayedTimerContext{Context: context.Background(), at: time.Now().Add(100 * time.Millisecond), onThird: make(chan struct{})}
			poll := newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
			poll.origins = newManagementOriginGate([]AdapterTarget{{Management: &poll.target}})
			blocked := poll.requestSlots
			if queue == "origin" {
				blocked = poll.origins.origins[managementOriginKey(poll.target.Endpoints[0])].slots
			}
			blocked <- struct{}{}
			blocked <- struct{}{}
			var called atomic.Bool
			returned := make(chan error, 1)
			joined := make(chan struct{})
			var releaseOnce sync.Once
			releaseQueue := func() { releaseOnce.Do(func() { <-blocked }) }
			defer func() {
				releaseQueue()
				select {
				case <-joined:
				case <-time.After(time.Second):
					t.Error("controlled queue operation remained after fixture cleanup")
				}
			}()
			go func() {
				defer close(joined)
				returned <- poll.request(ctx, poll.target.Endpoints[0], false, func(context.Context) error { called.Store(true); return nil })
			}()
			select {
			case <-ctx.onThird:
			case <-time.After(time.Second):
				t.Fatal("controlled request did not reach queue admission")
			}
			timer := time.NewTimer(max(0, time.Until(ctx.at)))
			<-timer.C
			releaseQueue() // The queue becomes available only after actual expiry.
			select {
			case err := <-returned:
				if !errors.Is(err, context.DeadlineExceeded) || called.Load() {
					t.Fatal("queue admission invoked provider after its deadline")
				}
			case <-time.After(time.Second):
				t.Fatal("expired queue operation did not join")
			}
			poll.finish()
			requests, pages := poll.counts()
			if requests != 0 || pages != 0 || len(blocked) != 1 {
				t.Fatal("expired queue changed request accounting or leaked admission")
			}
			<-blocked
		})
	}
}

func TestManagementDelayedTimerCallbackCannotReturnSuccess(t *testing.T) {
	ctx := &managementDelayedTimerContext{Context: context.Background(), at: time.Now().Add(30 * time.Millisecond)}
	poll := newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
	called := false
	err := poll.request(ctx, poll.target.Endpoints[0], false, func(context.Context) error {
		called = true
		timer := time.NewTimer(max(0, time.Until(ctx.at)))
		<-timer.C
		return nil
	})
	poll.finish()
	if !called || !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatal("provider success was accepted after an unannounced deadline")
	}
}

func TestManagementCompletionUsesActualDeadlineAndSignedExpiry(t *testing.T) {
	completed := time.Now()
	valid := managementTestGrant("instance")
	for _, test := range []struct {
		name   string
		parent context.Context
		poll   context.Context
		grant  ManagementPollGrant
		want   string
	}{
		{"valid", context.Background(), context.Background(), valid, ""},
		{"delayed poll timer", context.Background(), &managementDelayedTimerContext{Context: context.Background(), at: completed.Add(-time.Second)}, valid, "timeout"},
		{"delayed parent timer", &managementDelayedTimerContext{Context: context.Background(), at: completed.Add(-time.Second)}, context.Background(), valid, "cancelled"},
		{"signed expiry", context.Background(), context.Background(), func() ManagementPollGrant { g := valid; g.AuthorizationExpiresAt = completed; return g }(), "timeout"},
		{"scheduling expiry", context.Background(), context.Background(), func() ManagementPollGrant { g := valid; g.Deadline = completed; return g }(), "timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if actual := managementPollCompletionReason(test.parent, test.poll, test.grant, completed); actual != test.want {
				t.Fatalf("completion reason %q, want %q", actual, test.want)
			}
		})
	}
}

func TestKafkaOwnedIODelayedTimerFencesEntryAndJoinsPhysicalClose(t *testing.T) {
	for _, phase := range []string{"entry", "completion"} {
		t.Run(phase, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			raw, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			server, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			poll := newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
			release, err := poll.retainResource()
			if err != nil {
				t.Fatal(err)
			}
			connection := &kafkaManagementConnection{raw: raw, release: release}
			defer connection.close()
			deadline := time.Now().Add(30 * time.Millisecond)
			if phase == "entry" {
				deadline = time.Now().Add(-time.Second)
			}
			ctx := &managementDelayedTimerContext{Context: context.Background(), at: deadline}
			called := false
			err = connection.withIO(ctx, func(context.Context) error {
				called = true
				timer := time.NewTimer(max(0, time.Until(deadline)))
				<-timer.C
				return nil
			})
			if !errors.Is(err, context.DeadlineExceeded) || called != (phase == "completion") {
				t.Fatal("owned I/O trusted delayed timer delivery")
			}
			if phase == "completion" && !connection.provenClosed() {
				t.Fatal("expired I/O returned before physical close")
			}
			if err := connection.close(); err != nil {
				t.Fatal("actual owned socket close was not proven")
			}
			poll.finish()
			_ = server.SetReadDeadline(time.Now().Add(time.Second))
			var body [1]byte
			if n, err := server.Read(body[:]); n != 0 || !errors.Is(err, io.EOF) {
				t.Fatal("independent peer did not confirm physical close")
			}
		})
	}
}

func TestManagementRequestRejectsMissingOrExpiredAuthorityBeforeAdmission(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*ManagementPollGrant)
	}{
		{"missing scheduling deadline", func(g *ManagementPollGrant) { g.Deadline = time.Time{} }},
		{"missing signed authorization", func(g *ManagementPollGrant) { g.AuthorizationExpiresAt = time.Time{} }},
		{"expired scheduling deadline", func(g *ManagementPollGrant) { g.Deadline = time.Now().Add(-time.Second) }},
		{"expired signed authorization", func(g *ManagementPollGrant) { g.AuthorizationExpiresAt = time.Now().Add(-time.Second) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			grant := managementTestGrant("instance")
			test.change(&grant)
			grant.Pack = "solace-semp" // Admission must not update origin cadence.
			poll := newManagementObservation(managementTestTarget(), grant, ManagementCredentials{})
			poll.origins = newManagementOriginGate([]AdapterTarget{{Management: &poll.target}})
			called := false
			err := poll.request(context.Background(), poll.target.Endpoints[0], false, func(context.Context) error { called = true; return nil })
			poll.finish()
			requests, pages := poll.counts()
			origin := poll.origins.origins[managementOriginKey(poll.target.Endpoints[0])]
			if err == nil || called || requests != 0 || pages != 0 || len(poll.requestSlots) != 0 || len(origin.slots) != 0 || origin.poll != "" || !origin.next.IsZero() {
				t.Fatal("absent or expired authority reached provider/queue/origin admission")
			}
		})
	}
}

func TestManagementRequestContextUsesEarliestAuthority(t *testing.T) {
	for _, test := range []struct{ name, earliest string }{{"signed authorization", "authorization"}, {"scheduling deadline", "deadline"}, {"parent deadline", "parent"}, {"operation budget", "budget"}} {
		t.Run(test.name, func(t *testing.T) {
			grant := managementTestGrant("instance")
			expected := time.Now().Add(time.Second)
			ctx := context.Background()
			cancel := func() {}
			switch test.earliest {
			case "authorization":
				grant.AuthorizationExpiresAt = expected
			case "deadline":
				grant.Deadline = expected
			case "parent":
				ctx, cancel = context.WithDeadline(ctx, expected)
			}
			defer cancel()
			poll := newManagementObservation(managementTestTarget(), grant, ManagementCredentials{})
			before := time.Now()
			var actual time.Time
			err := poll.request(ctx, poll.target.Endpoints[0], false, func(opCtx context.Context) error {
				actual, _ = opCtx.Deadline()
				return nil
			})
			poll.finish()
			if err != nil {
				t.Fatal(err)
			}
			if test.earliest == "budget" {
				if actual.Before(before.Add(5*time.Second)) || actual.After(time.Now().Add(5*time.Second)) {
					t.Fatal("operation lifetime exceeds fixed five-second budget")
				}
			} else if !actual.Equal(expected) {
				t.Fatal("callback lifetime did not use earliest signed/local authority")
			}
		})
	}
}

func TestManagementQueuedRequestExpiresAtSignedAuthority(t *testing.T) {
	for _, queue := range []string{"poll", "origin"} {
		t.Run(queue, func(t *testing.T) {
			grant := managementTestGrant("instance")
			grant.AuthorizationExpiresAt = time.Now().Add(80 * time.Millisecond)
			poll := newManagementObservation(managementTestTarget(), grant, ManagementCredentials{})
			poll.origins = newManagementOriginGate([]AdapterTarget{{Management: &poll.target}})
			blocked := poll.requestSlots
			if queue == "origin" {
				blocked = poll.origins.origins[managementOriginKey(poll.target.Endpoints[0])].slots
			}
			blocked <- struct{}{}
			blocked <- struct{}{}
			called := false
			err := poll.request(context.Background(), poll.target.Endpoints[0], false, func(context.Context) error { called = true; return nil })
			poll.finish()
			requests, _ := poll.counts()
			if !errors.Is(err, context.DeadlineExceeded) || called || requests != 0 || len(blocked) != 2 || time.Since(grant.AuthorizationExpiresAt) > time.Second {
				t.Fatal("queued provider operation outlived signed authorization")
			}
			<-blocked
			<-blocked
		})
	}
}

func TestManagementSignedExpiryCancelsActualReadButRetainsPhysicalCleanup(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		raw, err := listener.Accept()
		if err == nil {
			accepted <- raw
		}
	}()
	raw, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var server net.Conn
	select {
	case server = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("independent TCP fixture did not accept")
	}
	defer server.Close()
	grant := managementTestGrant("instance")
	grant.AuthorizationExpiresAt = time.Now().Add(120 * time.Millisecond)
	poll := newManagementObservation(managementTestTarget(), grant, ManagementCredentials{})
	poll.origins = newManagementOriginGate([]AdapterTarget{{Management: &poll.target}})
	proof, err := poll.retainResource()
	if err != nil {
		t.Fatal(err)
	}
	unblock, entered := make(chan struct{}), make(chan struct{})
	var once sync.Once
	allowClose := func() { once.Do(func() { close(unblock) }) }
	defer func() {
		// Assertion-failure cleanup is an independent fixture reap. Retained
		// ownership is discharged only after that actual raw closure.
		_ = raw.Close()
		allowClose()
		proof()
	}()
	connection := &kafkaManagementConnection{raw: &kafkaControlledCloseConn{Conn: raw, entered: entered, unblock: unblock}, release: proof}
	returned := make(chan error, 1)
	go func() {
		returned <- poll.request(context.Background(), poll.target.Endpoints[0], false, func(opCtx context.Context) error {
			return connection.withIO(opCtx, func(context.Context) error { var body [1]byte; _, err := raw.Read(body[:]); return err })
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("signed authorization did not cancel actual read and enter physical close")
	}
	finished := make(chan struct{})
	go func() { poll.finish(); close(finished) }()
	select {
	case <-returned:
		t.Fatal("request returned before first physical Close joined")
	case <-finished:
		t.Fatal("poll released ownership before physical destruction")
	case <-time.After(20 * time.Millisecond):
	}
	if len(poll.requestSlots) != 1 || len(poll.origins.origins[managementOriginKey(poll.target.Endpoints[0])].slots) != 1 {
		t.Fatal("request/origin charge ended before actual cleanup")
	}
	allowClose()
	select {
	case err := <-returned:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("signed expiry reason lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("actual request did not join after confirmed closure")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("confirmed resource destruction did not discharge poll")
	}
}

func TestManagementSchedulerSignedExpiryKeepsSourceChargedUntilActualReturn(t *testing.T) {
	expired := make(chan struct{}, 1)
	entered := make(chan struct{ actual, expected time.Time }, 1)
	unblock := make(chan struct{})
	var once sync.Once
	allowReturn := func() { once.Do(func() { close(unblock) }) }
	fixture := newManagementFixture(t, managementObserverFunc(func(ctx context.Context, poll *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
		actual, _ := ctx.Deadline()
		entered <- struct{ actual, expected time.Time }{actual, poll.grant.AuthorizationExpiresAt}
		err := poll.request(ctx, poll.target.Endpoints[0], false, func(opCtx context.Context) error {
			proof, err := poll.retainResource()
			if err != nil {
				return err
			}
			defer proof()
			<-opCtx.Done()
			expired <- struct{}{}
			<-unblock // Independently controlled cleanup, not cancellation proof.
			return opCtx.Err()
		})
		return nil, err
	}))
	t.Cleanup(allowReturn) // Before fixture scheduler shutdown, including failures.
	fixture.authorizationLifetime = 200 * time.Millisecond
	if err := fixture.agent.runJobTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case times := <-entered:
		if !times.actual.Equal(times.expected) {
			t.Fatal("scheduler ignored earlier signed authorization expiry")
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not invoke controlled observer")
	}
	select {
	case <-expired:
	case <-time.After(time.Second):
		t.Fatal("signed authorization expiry did not cancel actual callback")
	}
	fixture.agent.management.mu.Lock()
	task := fixture.agent.management.active[managementSourceKey("project", "source")]
	fixture.agent.management.mu.Unlock()
	if task == nil || fixture.batchCount() != 0 || fixture.releases.Load() != 0 {
		t.Fatal("expired authority published/released before actual cleanup")
	}
	select {
	case <-task.done:
		t.Fatal("signed expiry confirmed physical completion")
	default:
	}
	if err := fixture.agent.pushHeartbeat(context.Background(), heartbeat{Health: "healthy"}); err != nil {
		t.Fatal("physical cleanup blocked heartbeat", err)
	}
	allowReturn()
	waitManagement(t, func() bool { return fixture.batchCount() == 1 })
	fixture.mu.Lock()
	reason := fixture.batches[0].MessagingManagement.UnavailableReason
	fixture.mu.Unlock()
	if reason != "timeout" {
		t.Fatal("signed expiry did not produce unavailable timeout evidence")
	}
}
