//go:build kafka_real && kafka_seek_integration

package kafka

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kmsg"
)

// Synthetic wire frames isolate transport ordering/bounds only. These are not
// provider qualification: TestKafkaOwnedSeekActualBroker requires Apache Kafka
// to produce every accepted heartbeat, offset, assignment and message.
func TestOwnedSeekProxyPipelining(t *testing.T) {
	for _, mode := range []string{"ordered-dual-hold", "cancel-held-response", "cancel-blocked-socket-io", "in-flight-limit"} {
		t.Run(mode, func(t *testing.T) {
			proxy, client, broker, cancel, exited, writeEntered := ownedSeekUnitPipeline(t)
			heartbeat := proxy.HoldRebalanceHeartbeat(t)
			lookup := proxy.HoldEarliest(t)
			if mode == "in-flight-limit" {
				// This control fills one queue without arming unused lookup proof.
				proxy.mu.Lock()
				proxy.armed = nil
				proxy.holds = proxy.holds[:1]
				proxy.mu.Unlock()
			}
			if mode == "cancel-blocked-socket-io" {
				proxy.mu.Lock()
				proxy.armed, proxy.heartbeat, proxy.holds = nil, nil, nil
				proxy.mu.Unlock()
			}
			serverBlocked := make(chan struct{})
			serverExited := make(chan struct{})
			serverObserved := make(chan int, 1)
			t.Cleanup(func() { broker.Close(); joinOwnedSeekOperation(t, serverExited, "pipeline wire server") })
			go func() {
				defer close(serverExited)
				seen := 0
				defer func() { serverObserved <- seen }()
				for {
					request, err := ownedSeekReadFrame(broker)
					if err != nil {
						return
					}
					seen++
					if mode == "cancel-blocked-socket-io" && seen == 2 {
						clear(request)
						close(serverBlocked)
						var unused [1]byte
						_, _ = io.ReadFull(broker, unused[:])
						return
					}
					key := int16(binary.BigEndian.Uint16(request[:2]))
					correlation := int32(binary.BigEndian.Uint32(request[4:8]))
					var response kmsg.Response
					if key == 12 {
						hb := kmsg.NewPtrHeartbeatResponse()
						hb.SetVersion(4)
						hb.ErrorCode = 27
						response = hb
					} else {
						ls := kmsg.NewPtrListOffsetsResponse()
						ls.SetVersion(7)
						response = ls
					}
					body := ownedSeekUnitResponse(response, correlation)
					clear(request)
					err = ownedSeekWriteFrame(broker, body)
					clear(body)
					if err != nil {
						return
					}
				}
			}()
			hbReq := kmsg.NewPtrHeartbeatRequest()
			hbReq.SetVersion(4)
			hbReq.Group, hbReq.MemberID = "workers", "unit-member"
			if ownedSeekWriteFrame(client, ownedSeekUnitRequest(hbReq, 1, "apex-owned-seek-primary")) != nil {
				t.Fatal("unit heartbeat write failed")
			}
			if mode == "cancel-blocked-socket-io" {
				waitOwnedSeekUnitSignal(t, writeEntered, "actual unread client response write")
			} else {
				waitOwnedSeekUnitSignal(t, heartbeat.entered, "unit heartbeat capture")
			}
			lsReq := ownedSeekUnitLookup()
			if ownedSeekWriteFrame(client, ownedSeekUnitRequest(lsReq, 2, "apex-owned-seek-primary")) != nil {
				t.Fatal("unit lookup write failed")
			}
			if mode == "cancel-blocked-socket-io" {
				waitOwnedSeekUnitSignal(t, serverBlocked, "actual unanswered upstream lookup")
				cancel()
				waitOwnedSeekUnitSignal(t, exited, "blocked I/O pipeline and cancellation watcher join")
				proxy.mu.Lock()
				failures := append([]string{}, proxy.failures...)
				proxy.mu.Unlock()
				if len(failures) != 0 {
					t.Fatal("blocked socket cancellation became a protocol failure")
				}
				return
			}
			if mode == "in-flight-limit" {
				for correlation := int32(3); correlation <= ownedSeekProxyInFlight; correlation++ {
					if ownedSeekWriteFrame(client, ownedSeekUnitRequest(lsReq, correlation, "apex-owned-seek-primary")) != nil {
						t.Fatal("admitted bounded unit pipeline write failed")
					}
				}
				// The overflow frame must never reach the upstream wire.
				_ = ownedSeekWriteFrame(client, ownedSeekUnitRequest(lsReq, ownedSeekProxyInFlight+1, "apex-owned-seek-primary"))
				waitOwnedSeekUnitSignal(t, exited, "overflow pipeline physical join")
				broker.Close()
				waitOwnedSeekUnitSignal(t, serverExited, "overflow wire server join")
				if seen := <-serverObserved; seen != ownedSeekProxyInFlight {
					t.Fatalf("unit upstream request bound changed: %d", seen)
				}
				proxy.mu.Lock()
				failures, peak := append([]string{}, proxy.failures...), proxy.pipelinePeak
				proxy.mu.Unlock()
				if peak != ownedSeekProxyInFlight || !reflect.DeepEqual(failures, []string{"execution proxy in-flight request budget exceeded"}) {
					t.Fatal("pipeline queue overflow lost its exact bounded refusal")
				}
				return
			}
			waitOwnedSeekUnitSignal(t, lookup.entered, "lookup read while earlier heartbeat held")
			if mode == "cancel-held-response" {
				cancel()
				waitOwnedSeekUnitSignal(t, exited, "cancelled dual-hold physical join")
				proxy.mu.Lock()
				failures := append([]string{}, proxy.failures...)
				proxy.mu.Unlock()
				if len(failures) != 0 {
					t.Fatal("owned pipeline cancellation became a protocol failure")
				}
				return
			}
			heartbeat.Release()
			first, err := ownedSeekReadFrame(client)
			want := kmsg.NewPtrHeartbeatResponse()
			want.SetVersion(4)
			want.ErrorCode = 27
			if err != nil || !bytes.Equal(first, ownedSeekUnitResponse(want, 1)) {
				t.Fatal("pipeline reordered or changed earlier heartbeat bytes")
			}
			clear(first)
			client.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
			if _, err := ownedSeekReadFrame(client); err == nil {
				t.Fatal("later held lookup escaped before release")
			}
			client.SetReadDeadline(time.Now().Add(2 * time.Second))
			lookup.Release()
			second, err := ownedSeekReadFrame(client)
			wantLookup := kmsg.NewPtrListOffsetsResponse()
			wantLookup.SetVersion(7)
			if err != nil || !bytes.Equal(second, ownedSeekUnitResponse(wantLookup, 2)) {
				t.Fatal("pipeline changed later lookup bytes")
			}
			clear(second)
			cancel()
			waitOwnedSeekUnitSignal(t, exited, "ordered pipeline physical join")
			if !heartbeat.released || !lookup.released {
				t.Fatal("ordered holds did not reach both release proofs")
			}
			proxy.mu.Lock()
			failures := append([]string{}, proxy.failures...)
			proxy.mu.Unlock()
			if len(failures) != 0 {
				t.Fatal("ordered bounded pipeline recorded an unexpected failure")
			}
		})
	}
}

func TestOwnedSeekProxyHeartbeatAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, client, group string
		code                int16
		admitted            bool
	}{
		{"exact-rebalance", "apex-owned-seek-primary", "workers", 27, true},
		{"healthy-heartbeat", "apex-owned-seek-primary", "workers", 0, false},
		{"other-error", "apex-owned-seek-primary", "workers", 29, false},
		{"peer-heartbeat", "apex-owned-seek-rebalance-peer", "workers", 27, false},
		{"other-group", "apex-owned-seek-primary", "workers-peer", 27, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &ownedSeekProxy{group: "workers"}
			h := p.HoldRebalanceHeartbeat(t)
			req := kmsg.NewPtrHeartbeatRequest()
			req.SetVersion(4)
			req.Group, req.MemberID = tc.group, "unit-member"
			body := ownedSeekUnitRequest(req, 1, tc.client)
			response := kmsg.NewPtrHeartbeatResponse()
			response.SetVersion(4)
			response.ErrorCode = tc.code
			actual := p.captureHeartbeatResponse(ownedSeekWireRequest{version: 4, primaryHeartbeat: p.primaryHeartbeatRequest(body, req)}, ownedSeekUnitResponse(response, 1))
			if (actual == h) != tc.admitted {
				t.Fatal("heartbeat admission crossed exact identity/error boundary")
			}
			if !tc.admitted && p.heartbeat != h {
				t.Fatal("nonmatching heartbeat consumed armed proof")
			}
		})
	}
}

func TestOwnedSeekProxyLookupAdmission(t *testing.T) {
	for _, mode := range []string{"exact-primary", "peer", "other-topic", "duplicate-partition", "missing-partition", "latest", "malformed-header"} {
		t.Run(mode, func(t *testing.T) {
			p := &ownedSeekProxy{topic: "orders"}
			h := p.HoldEarliest(t)
			req := ownedSeekUnitLookup()
			client := "apex-owned-seek-primary"
			switch mode {
			case "peer":
				client = "apex-owned-seek-rebalance-peer"
			case "other-topic":
				req.Topics[0].Topic = "unpolled"
			case "duplicate-partition":
				req.Topics[0].Partitions[1].Partition = 0
			case "missing-partition":
				req.Topics[0].Partitions = req.Topics[0].Partitions[:2]
			case "latest":
				req.Topics[0].Partitions[0].Timestamp = -1
			}
			body := ownedSeekUnitRequest(req, 1, client)
			if mode == "malformed-header" {
				body = body[:9]
			}
			actual := p.captureHold(body, req.Key(), req.GetVersion())
			if (actual == h) != (mode == "exact-primary") {
				t.Fatal("lookup admission crossed exact primary/header/semantic boundary")
			}
			if mode != "exact-primary" && p.armed != h {
				t.Fatal("nonmatching lookup consumed primary proof")
			}
		})
	}
}

func TestOwnedSeekProxyHoldDeadline(t *testing.T) {
	for _, mode := range []string{"released-within-budget", "released-after-deadline", "cancelled", "expires"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			p := &ownedSeekProxy{}
			h := &ownedSeekHeldResponse{release: make(chan struct{}), deadline: time.Now().Add(time.Second)}
			switch mode {
			case "released-within-budget":
				h.Release()
			case "released-after-deadline":
				h.deadline = time.Now().Add(-time.Millisecond)
				h.Release()
			case "cancelled":
				cancel()
			case "expires":
				h.deadline = time.Now().Add(20 * time.Millisecond)
			}
			admitted := p.awaitHeld(ctx, h)
			if admitted != (mode == "released-within-budget") || h.released != admitted {
				t.Fatal("held response crossed release/deadline boundary")
			}
			want := []string{}
			if mode == "released-after-deadline" || mode == "expires" {
				want = []string{"execution proxy held response exceeded bounded release"}
			}
			if !reflect.DeepEqual(append([]string{}, p.failures...), want) {
				t.Fatal("hold deadline lost exact expiry/cancellation receipt")
			}
		})
	}
}

type ownedSeekWriteProbe struct {
	net.Conn
	entered chan struct{}
	once    sync.Once
}

func (c *ownedSeekWriteProbe) Write(body []byte) (int, error) {
	c.once.Do(func() { close(c.entered) })
	return c.Conn.Write(body)
}

func ownedSeekUnitPipeline(t *testing.T) (*ownedSeekProxy, net.Conn, net.Conn, context.CancelFunc, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	front, client := net.Pipe()
	back, broker := net.Pipe()
	client.SetDeadline(time.Now().Add(4 * time.Second))
	broker.SetDeadline(time.Now().Add(4 * time.Second))
	p := &ownedSeekProxy{ctx: ctx, topic: "orders", group: "workers", counts: map[int16]int{}}
	probe := &ownedSeekWriteProbe{Conn: front, entered: make(chan struct{})}
	exited := make(chan struct{})
	go func() { defer close(exited); p.runPipeline(ctx, probe, back) }()
	t.Cleanup(func() {
		cancel()
		client.Close()
		broker.Close()
		front.Close()
		back.Close()
		joinOwnedSeekOperation(t, exited, "unit proxy pipeline")
	})
	return p, client, broker, cancel, exited, probe.entered
}
func waitOwnedSeekUnitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s not observed", description)
	}
}
func ownedSeekUnitRequest(req kmsg.Request, correlation int32, client string) []byte {
	body := binary.BigEndian.AppendUint16(nil, uint16(req.Key()))
	body = binary.BigEndian.AppendUint16(body, uint16(req.GetVersion()))
	body = binary.BigEndian.AppendUint32(body, uint32(correlation))
	body = binary.BigEndian.AppendUint16(body, uint16(len(client)))
	body = append(body, client...)
	if req.IsFlexible() {
		body = append(body, 0)
	}
	return req.AppendTo(body)
}
func ownedSeekUnitResponse(resp kmsg.Response, correlation int32) []byte {
	body := binary.BigEndian.AppendUint32(nil, uint32(correlation))
	if resp.IsFlexible() {
		body = append(body, 0)
	}
	return resp.AppendTo(body)
}
func ownedSeekUnitLookup() *kmsg.ListOffsetsRequest {
	request := kmsg.NewPtrListOffsetsRequest()
	request.SetVersion(7)
	topic := kmsg.NewListOffsetsRequestTopic()
	topic.Topic = "orders"
	for partition := int32(0); partition < 3; partition++ {
		p := kmsg.NewListOffsetsRequestTopicPartition()
		p.Partition, p.Timestamp = partition, -2
		topic.Partitions = append(topic.Partitions, p)
	}
	request.Topics = []kmsg.ListOffsetsRequestTopic{topic}
	return request
}
