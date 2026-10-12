package generatorrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/livetable"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestLiveTableProxyReconnectPreservesIdentityAndFencesOldReplies(t *testing.T) {
	proxy := newLiveTableProxy([]engine.LiveTableBinding{{Alias: "work", TableID: "table"}})
	proxy.epoch = 1
	proxy.attached = true
	request := livetable.Request{ClientID: "client", OwnerID: "owner", Sequence: 42, Operation: livetable.OpPop}
	resultCh := make(chan liveTableResponse, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	go func() {
		result, err := proxy.Execute(ctx, "table", request)
		resultCh <- liveTableResponse{result: result, err: err}
	}()
	waitLiveCondition(t, func() bool { proxy.mu.Lock(); defer proxy.mu.Unlock(); return len(proxy.pending) == 1 })
	first := proxy.frames(1, "group")
	if len(first) != 1 {
		t.Fatal("request not emitted")
	}
	if again := proxy.frames(1, "group"); len(again) != 0 {
		t.Fatal("request repeated on same stream")
	}
	proxy.mu.Lock()
	proxy.epoch = 2
	proxy.mu.Unlock()
	second := proxy.frames(2, "group")
	if len(second) != 1 || first[0].RequestId != second[0].RequestId || !reflect.DeepEqual(first[0].RequestJson, second[0].RequestJson) {
		t.Fatal("reconnect changed operation identity")
	}
	encoded, _ := json.Marshal(livetable.Result{Status: livetable.StatusOK, Revision: 3, Row: &livetable.Row{ID: "one", Values: map[string]string{"value": "x"}}})
	reply := &pb.LiveTableReply{RequestId: first[0].RequestId, ResultJson: encoded}
	if err := proxy.receive(reply, 1); err == nil {
		t.Fatal("replaced stream delivered a result")
	}
	select {
	case <-resultCh:
		t.Fatal("old reply escaped fence")
	default:
	}
	if err := proxy.receive(reply, 2); err != nil {
		t.Fatal(err)
	}
	result := <-resultCh
	if result.err != nil || result.result.Row.ID != "one" {
		t.Fatalf("reply: %+v", result)
	}
}

func TestLiveTableProxyPreservesTypedErrorAndUnknownOutcomes(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			proxy := newLiveTableProxy([]engine.LiveTableBinding{{Alias: "work", TableID: "table"}})
			proxy.epoch = 1
			proxy.attached = true
			done := make(chan error, 1)
			go func() {
				_, err := proxy.Execute(t.Context(), "table", livetable.Request{ClientID: "client", Sequence: 1, Operation: livetable.OpPop})
				done <- err
			}()
			waitLiveCondition(t, func() bool { proxy.mu.Lock(); defer proxy.mu.Unlock(); return len(proxy.pending) == 1 })
			frame := proxy.frames(1, "group")[0]
			reply := &pb.LiveTableReply{RequestId: frame.RequestId}
			liveTableReplyError(reply, &livetable.Error{Code: livetable.CodeCapacity, Message: "sensitive row value", OutcomeUnknown: unknown})
			if strings.Contains(string(reply.ResultJson), "sensitive") {
				t.Fatal("error disclosed values")
			}
			if err := proxy.receive(reply, 1); err != nil {
				t.Fatal(err)
			}
			var typed *livetable.Error
			if err := <-done; !errors.As(err, &typed) || typed.Code != livetable.CodeCapacity || typed.OutcomeUnknown != unknown {
				t.Fatalf("typed error lost: %v", err)
			}
		})
	}
	proxy := newLiveTableProxy(nil)
	if _, err := proxy.Execute(t.Context(), "other", livetable.Request{}); livetable.CodeOf(err) != livetable.CodeInvalid {
		t.Fatal("assignment rejection is not definitive")
	}
}

func TestLiveTableProxyReservesControlAdmissionAndOrdersControlFirst(t *testing.T) {
	proxy := newLiveTableProxy([]engine.LiveTableBinding{{Alias: "work", TableID: "table"}})
	proxy.epoch = 1
	proxy.attached = true
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < liveTablePendingOps-liveTableControlReserve; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = proxy.Execute(ctx, "table", livetable.Request{ClientID: fmt.Sprint(i), Sequence: 1, Operation: livetable.OpPop, Wait: time.Minute})
		}(i)
	}
	waitLiveCondition(t, func() bool {
		proxy.mu.Lock()
		defer proxy.mu.Unlock()
		return proxy.ordinary == liveTablePendingOps-liveTableControlReserve
	})
	if _, err := proxy.Execute(ctx, "table", livetable.Request{ClientID: "excess", Sequence: 1, Operation: livetable.OpPop}); livetable.CodeOf(err) != livetable.CodeCapacity {
		t.Fatalf("ordinary overload: %v", err)
	}
	for i := 0; i < liveTableControlReserve-liveTableLeaseControlReserve; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = proxy.Execute(ctx, "table", livetable.Request{ClientID: fmt.Sprintf("push-%d", i), Sequence: 1, Operation: livetable.OpPush})
		}(i)
	}
	waitLiveCondition(t, func() bool {
		proxy.mu.Lock()
		defer proxy.mu.Unlock()
		return proxy.producers == liveTableControlReserve-liveTableLeaseControlReserve
	})
	if _, err := proxy.Execute(ctx, "table", livetable.Request{ClientID: "excess-producer", Sequence: 1, Operation: livetable.OpPush}); livetable.CodeOf(err) != livetable.CodeCapacity {
		t.Fatalf("producer consumed lease-control count reserve: %v", err)
	}
	for i := 0; i < liveTableLeaseControlReserve; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = proxy.Execute(ctx, "table", livetable.Request{ClientID: fmt.Sprintf("renew-%d", i), OwnerID: "owner", Sequence: 1, Operation: livetable.OpRenew})
		}(i)
	}
	waitLiveCondition(t, func() bool {
		proxy.mu.Lock()
		defer proxy.mu.Unlock()
		return len(proxy.pending) == liveTablePendingOps
	})
	if _, err := proxy.Execute(ctx, "table", livetable.Request{ClientID: "excess-control", Sequence: 1, Operation: livetable.OpAck}); livetable.CodeOf(err) != livetable.CodeCapacity {
		t.Fatalf("control exceeded total count budget: %v", err)
	}
	frames := proxy.frames(1, "group")
	var first livetable.Request
	_ = json.Unmarshal(frames[0].RequestJson, &first)
	if first.Operation != livetable.OpRenew {
		t.Fatal("renewal did not precede waiting consumers")
	}
	cancel()
	wg.Wait()
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	if len(proxy.pending) != 0 || proxy.liveTableAdmission != (liveTableAdmission{}) {
		t.Fatal("canceled channel calls leaked resources")
	}
}

func liveRequestWithJSONSize(t *testing.T, operation livetable.Operation, size int) livetable.Request {
	t.Helper()
	request := livetable.Request{Operation: operation, ClientID: "producer", Sequence: 1, Values: make(map[string]string)}
	// Keep each field inside the core's default field bound while filling the
	// transport budget, rather than relying on one invalid oversized field.
	for i := 0; i < 64; i++ {
		request.Values[fmt.Sprintf("field_%02d", i)] = ""
	}
	body, err := json.Marshal(request)
	if err != nil || len(body) > size {
		t.Fatalf("request size setup: %v, %d > %d", err, len(body), size)
	}
	remaining := size - len(body)
	for i := 0; i < 64 && remaining > 0; i++ {
		n := min(remaining, 16<<10)
		request.Values[fmt.Sprintf("field_%02d", i)] = strings.Repeat("x", n)
		remaining -= n
	}
	body, err = json.Marshal(request)
	if err != nil || len(body) != size {
		t.Fatalf("request size setup: %v, got %d want %d", err, len(body), size)
	}
	return request
}

func TestLiveTableProxyReservesLeaseBytesWhenProducersFillBudget(t *testing.T) {
	proxy := newLiveTableProxy([]engine.LiveTableBinding{{Alias: "work", TableID: "table"}})
	proxy.epoch, proxy.attached = 1, true
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); wg.Wait() })
	producerSlots := liveTableControlReserve - liveTableLeaseControlReserve
	request := liveRequestWithJSONSize(t, livetable.OpPush, (liveTablePendingBytes-liveTableLeaseReservedBytes)/producerSlots)
	for i := 0; i < producerSlots; i++ {
		request := request
		request.ClientID = fmt.Sprintf("prod-%03d", i)
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = proxy.Execute(ctx, "table", request) }()
	}
	waitLiveCondition(t, func() bool {
		proxy.mu.Lock()
		defer proxy.mu.Unlock()
		return proxy.nonLeaseBytes == liveTablePendingBytes-liveTableLeaseReservedBytes
	})
	// This update has an ordinary count slot available; only the byte reserve
	// must reject it, definitively and before assigning a transport identity.
	_, err := proxy.Execute(ctx, "table", livetable.Request{Operation: livetable.OpUpdate, ClientID: "overflow", Sequence: 1})
	var typed *livetable.Error
	if !errors.As(err, &typed) || typed.Code != livetable.CodeCapacity || typed.OutcomeUnknown {
		t.Fatalf("byte overload was not definitively rejected: %v", err)
	}
	_, err = proxy.Execute(ctx, "table", livetable.Request{Operation: livetable.OpAck, ClientID: strings.Repeat("x", liveTableLeaseControlBytes), Sequence: 1})
	if !errors.As(err, &typed) || typed.Code != livetable.CodeCapacity || typed.OutcomeUnknown {
		t.Fatalf("padded lease control was not definitively rejected: %v", err)
	}
	leaseResults := make(chan error, liveTableLeaseControlReserve)
	for i := 0; i < liveTableLeaseControlReserve; i++ {
		operation := []livetable.Operation{livetable.OpAck, livetable.OpRelease, livetable.OpRenew}[i%3]
		control := livetable.Request{Operation: operation, ClientID: strings.Repeat("<", 256), OwnerID: strings.Repeat("<", 256), RecordID: strings.Repeat("<", 256), LeaseToken: strings.Repeat("<", 256), Sequence: ^uint64(0)}
		control.ClientID = strings.Repeat("<", 255) + fmt.Sprint(i)
		if operation == livetable.OpRenew {
			control.LeaseDuration = 5 * time.Minute
		}
		wg.Add(1)
		go func() { defer wg.Done(); _, err := proxy.Execute(ctx, "table", control); leaseResults <- err }()
	}
	waitLiveCondition(t, func() bool {
		proxy.mu.Lock()
		defer proxy.mu.Unlock()
		return proxy.leaseControls == liveTableLeaseControlReserve
	})
	frames := proxy.frames(1, "group")
	if len(frames) != liveTableControlReserve {
		t.Fatalf("rejected requests allocated pending identities: %d frames", len(frames))
	}
	result, _ := json.Marshal(livetable.Result{Status: livetable.StatusOK})
	for i := 0; i < liveTableLeaseControlReserve; i++ {
		var control livetable.Request
		if err := json.Unmarshal(frames[i].RequestJson, &control); err != nil || !liveTableLeaseControlOperation(control.Operation) {
			t.Fatalf("lease control did not precede producers: %v", err)
		}
		if err := proxy.receive(&pb.LiveTableReply{RequestId: frames[i].RequestId, ResultJson: result}, 1); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < liveTableLeaseControlReserve; i++ {
		if err := <-leaseResults; err != nil {
			t.Fatalf("reserved control failed: %v", err)
		}
	}
	cancel()
	wg.Wait()
	proxy.mu.Lock()
	defer proxy.mu.Unlock()
	if len(proxy.pending) != 0 || proxy.liveTableAdmission != (liveTableAdmission{}) {
		t.Fatalf("channel accounting leaked: %+v", proxy.liveTableAdmission)
	}
}

func TestLiveTableProxyCancellationIsExplicitlyUnknown(t *testing.T) {
	proxy := newLiveTableProxy([]engine.LiveTableBinding{{Alias: "work", TableID: "table"}})
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	_, err := proxy.Execute(ctx, "table", livetable.Request{ClientID: "client", Sequence: 1, Operation: livetable.OpPop})
	var typed *livetable.Error
	if !errors.As(err, &typed) || !typed.OutcomeUnknown {
		t.Fatalf("cancellation did not retain ambiguity: %v", err)
	}
}

type liveChannelRPC struct {
	pb.GeneratorControlClient
	stream *liveClientStream
}

func (c *liveChannelRPC) LiveTableChannel(ctx context.Context, _ ...grpc.CallOption) (grpc.BidiStreamingClient[pb.LiveTableReply, pb.LiveTableRequest], error) {
	c.stream.ctx = ctx
	return c.stream, nil
}

type liveClientStream struct {
	grpc.ClientStream
	ctx         context.Context
	requests    chan *pb.LiveTableRequest
	replies     chan *pb.LiveTableReply
	stallHeader bool
}

func (s *liveClientStream) Send(reply *pb.LiveTableReply) error {
	select {
	case s.replies <- reply:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}
func (s *liveClientStream) Recv() (*pb.LiveTableRequest, error) {
	select {
	case frame, ok := <-s.requests:
		if !ok {
			return nil, io.EOF
		}
		return frame, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}
func (s *liveClientStream) Header() (metadata.MD, error) {
	if s.stallHeader {
		<-s.ctx.Done()
		return nil, s.ctx.Err()
	}
	return metadata.Pairs("apexload-live-tables-ready", "1"), nil
}

func TestServeLiveTablesBoundsHandshakeAndCancelsStream(t *testing.T) {
	stream := &liveClientStream{replies: make(chan *pb.LiveTableReply, 1), stallHeader: true}
	client := &Client{rpc: &liveChannelRPC{stream: stream}}
	ready := false
	started := time.Now()
	err := client.serveLiveTablesWithHandshakeTimeout(t.Context(), "run", "group", 1, []byte("token"), nil, func(error) { ready = true }, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) || ready || stream.ctx.Err() == nil || time.Since(started) > time.Second {
		t.Fatalf("unbounded handshake: error=%v ready=%v stream=%v", err, ready, stream.ctx.Err())
	}
}
func (s *liveClientStream) Context() context.Context { return s.ctx }

type liveTestClientFunc func(context.Context, string, livetable.Request) (livetable.Result, error)

func (f liveTestClientFunc) Execute(ctx context.Context, id string, r livetable.Request) (livetable.Result, error) {
	return f(ctx, id, r)
}

func TestServeLiveTablesAllowsProducerWhileConsumersWait(t *testing.T) {
	stream := &liveClientStream{requests: make(chan *pb.LiveTableRequest, liveTablePendingOps), replies: make(chan *pb.LiveTableReply, liveTablePendingOps)}
	client := &Client{rpc: &liveChannelRPC{stream: stream}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{}, liveTablePendingOps)
	done := make(chan error, 1)
	handler := liveTestClientFunc(func(ctx context.Context, _ string, r livetable.Request) (livetable.Result, error) {
		if r.Operation == livetable.OpPush {
			return livetable.Result{Status: livetable.StatusOK}, nil
		}
		started <- struct{}{}
		<-ctx.Done()
		return livetable.Result{}, ctx.Err()
	})
	go func() {
		done <- client.serveLiveTables(ctx, "run", "group", 1, []byte("scope"), handler, func(error) {})
	}()
	<-stream.replies
	for i := 0; i < liveTablePendingOps-liveTableControlReserve; i++ {
		body, _ := json.Marshal(livetable.Request{Operation: livetable.OpPop, ClientID: fmt.Sprint(i), Sequence: 1})
		stream.requests <- &pb.LiveTableRequest{GroupName: "group", RequestId: uint64(i + 1), TableId: "table", RequestJson: body, TimeoutMilliseconds: 5000}
	}
	for i := 0; i < liveTablePendingOps-liveTableControlReserve; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("waiting consumers were not admitted")
		}
	}
	body, _ := json.Marshal(livetable.Request{Operation: livetable.OpPush, ClientID: "producer", Sequence: 1, Values: map[string]string{"value": "ready"}})
	stream.requests <- &pb.LiveTableRequest{GroupName: "group", RequestId: 1000, TableId: "table", RequestJson: body, TimeoutMilliseconds: 5000}
	select {
	case reply := <-stream.replies:
		if reply.RequestId != 1000 || reply.ErrorCode != "" {
			t.Fatalf("producer failed: %v", reply)
		}
	case <-time.After(time.Second):
		t.Fatal("waiting consumers blocked producer")
	}
	cancel()
	<-done
}

func TestServeLiveTablesReservesLeaseAdmissionAtProducerByteLimit(t *testing.T) {
	stream := &liveClientStream{requests: make(chan *pb.LiveTableRequest, liveTablePendingOps), replies: make(chan *pb.LiveTableReply, liveTablePendingOps)}
	client := &Client{rpc: &liveChannelRPC{stream: stream}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{}, liveTablePendingOps)
	done := make(chan error, 1)
	handler := liveTestClientFunc(func(ctx context.Context, _ string, r livetable.Request) (livetable.Result, error) {
		if liveTableLeaseControlOperation(r.Operation) {
			return livetable.Result{Status: livetable.StatusOK}, nil
		}
		started <- struct{}{}
		<-ctx.Done()
		return livetable.Result{}, ctx.Err()
	})
	go func() {
		done <- client.serveLiveTables(ctx, "run", "group", 1, []byte("scope"), handler, func(error) {})
	}()
	<-stream.replies
	send := func(id uint64, request livetable.Request) {
		t.Helper()
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		stream.requests <- &pb.LiveTableRequest{GroupName: "group", RequestId: id, TableId: "table", RequestJson: body, TimeoutMilliseconds: 5000}
	}
	receive := func(id uint64, code livetable.Code) {
		t.Helper()
		select {
		case reply := <-stream.replies:
			if reply.RequestId != id || reply.ErrorCode != string(code) {
				t.Fatalf("unexpected result: %+v", reply)
			}
			if code != "" {
				var failure livetable.Error
				if err := json.Unmarshal(reply.ResultJson, &failure); err != nil || failure.OutcomeUnknown {
					t.Fatalf("preacceptance rejection was ambiguous: %+v, %v", failure, err)
				}
			}
		case <-time.After(time.Second):
			t.Fatal("producer load blocked lease admission or overload reply")
		}
	}
	producerSlots := liveTableControlReserve - liveTableLeaseControlReserve
	request := liveRequestWithJSONSize(t, livetable.OpPush, (liveTablePendingBytes-liveTableLeaseReservedBytes)/producerSlots)
	for i := 0; i < producerSlots; i++ {
		request.ClientID = fmt.Sprintf("prod-%03d", i)
		send(uint64(i+1), request)
	}
	for i := 0; i < producerSlots; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("producers were not admitted at the byte limit")
		}
	}
	send(100, livetable.Request{Operation: livetable.OpUpdate, ClientID: "overflow", Sequence: 1})
	receive(100, livetable.CodeCapacity)
	send(101, livetable.Request{Operation: livetable.OpRenew, ClientID: strings.Repeat("x", liveTableLeaseControlBytes), Sequence: 1})
	receive(101, livetable.CodeCapacity)
	for i, operation := range []livetable.Operation{livetable.OpAck, livetable.OpRelease, livetable.OpRenew} {
		id := uint64(102 + i)
		send(id, livetable.Request{Operation: operation, ClientID: "lease-control", Sequence: uint64(i + 1), RecordID: "record", LeaseToken: "token"})
		receive(id, "")
	}
	cancel()
	<-done
}

func TestDecodeLiveTableBindingsRejectsInvalidEnvelope(t *testing.T) {
	for _, body := range []string{`[{"alias":"a","table_id":"t","surprise":true}]`, `[] {}`, `[{"alias":"a","table_id":"t"},{"alias":"a","table_id":"u"}]`} {
		if _, err := decodeLiveTableBindings([]byte(body)); err == nil {
			t.Fatalf("invalid binding accepted: %s", body)
		}
	}
}

func TestLiveTableGRPCReconnectAfterCommitDoesNotConsumeTwice(t *testing.T) {
	service := newIsolatedService(t, 10)
	proxy := newLiveTableProxy([]engine.LiveTableBinding{{Alias: "work", TableID: "table"}})
	token := supervisionTestToken(99)
	service.mu.Lock()
	service.assignments["run"] = assignedTest{testID: "run", groups: map[string]int32{"group": 1}, runSupervisionTokenHash: runSupervisionTokenHash(token)}
	service.executions["run"] = map[string]*groupExecution{"group": {groupName: "group", stagedConfig: &engine.MultiGroupConfig{Groups: []engine.GroupConfig{{Name: "group", LiveTableClient: proxy}}}}}
	service.mu.Unlock()
	listener := bufconn.Listen(2 << 20)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	conn, err := grpc.NewClient("passthrough:///live-tables", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := &Client{rpc: pb.NewGeneratorControlClient(conn)}
	manager := livetable.NewManager()
	table, err := manager.Create(livetable.Config{ID: "table", RunID: "run", Columns: []string{"value"}, Rows: []livetable.Row{{Values: map[string]string{"value": "one"}}, {Values: map[string]string{"value": "two"}}}})
	if err != nil {
		t.Fatal(err)
	}
	firstCtx, firstCancel := context.WithCancel(t.Context())
	defer firstCancel()
	committed := make(chan struct{})
	var once sync.Once
	blocking := liveTestClientFunc(func(ctx context.Context, id string, r livetable.Request) (livetable.Result, error) {
		result, err := manager.Execute(ctx, id, r)
		once.Do(func() { close(committed) })
		<-ctx.Done()
		return result, err
	})
	if err = client.OpenLiveTables(firstCtx, "run", "group", 0, token, blocking); err != nil {
		t.Fatal(err)
	}
	done := make(chan liveTableResponse, 1)
	operationCtx, operationCancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer operationCancel()
	go func() {
		result, err := proxy.Execute(operationCtx, "table", livetable.Request{ClientID: "vu", Sequence: 1, Operation: livetable.OpPop})
		done <- liveTableResponse{result: result, err: err}
	}()
	select {
	case <-committed:
	case <-time.After(time.Second):
		t.Fatal("operation did not commit")
	}
	firstCancel()
	secondCtx, secondCancel := context.WithCancel(t.Context())
	defer secondCancel()
	if err = client.OpenLiveTables(secondCtx, "run", "group", 0, token, manager); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.result.Row == nil || got.result.Row.Values["value"] != "one" {
			t.Fatalf("recovered response: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("reconnect did not recover pending result")
	}
	if table.Stats().Counts.Popped != 1 || table.Stats().Counts.Available != 1 {
		t.Fatal("reconnect consumed a second row")
	}
	bad, err := client.rpc.LiveTableChannel(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err = bad.Send(&pb.LiveTableReply{TestId: "run", GroupName: "group", ExecutionGeneration: 1, RunSupervisionToken: token}); err != nil {
		t.Fatal(err)
	}
	_, err = bad.Recv()
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale generation channel accepted: %v", err)
	}
}
func waitLiveCondition(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("live table condition did not become true")
}
