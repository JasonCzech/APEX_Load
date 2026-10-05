package collectoragent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

func solaceHTTPTestRoute() solaceManagementRoute {
	return solaceManagementRoute{origin: "https://broker.example.test:943", serverName: "broker.example.test", address: netip.MustParseAddrPort("127.0.0.1:943"), requestPath: "/SEMP/v2/monitor/msgVpns/orders/queues/pending?select=msgVpnName%2CqueueName%2CdeadMsgQueue%2Cmsgs.count"}
}
func TestSolaceManagementHTTPFramingIsBoundedAndUnambiguous(t *testing.T) {
	for _, test := range []struct {
		name, wire, reason, body string
		limit                    int
	}{
		{"length", "HTTP/1.1 200 OK\r\nContent-Type: application/json;charset=utf-8\r\nContent-Length: 2\r\n\r\n{}", "", "{}", 2},
		{"chunked", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n1\r\n{\r\n1\r\n}\r\n0\r\n\r\n", "", "{}", 2},
		{"EOF", "HTTP/1.1 200 OK\r\n\r\n{}", "", "{}", 2},
		{"empty length", "HTTP/1.1 200 OK\r\nContent-Length:\r\n\r\n", "invalid", "", 2},
		{"empty coding", "HTTP/1.1 200 OK\r\nTransfer-Encoding:\r\n\r\n", "invalid", "", 2},
		{"framing collision", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nContent-Length: 2\r\n\r\n{}", "invalid", "", 2},
		{"duplicate length", "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Length: 2\r\n\r\n{}", "invalid", "", 2},
		{"XML unsupported", "HTTP/1.1 200 OK\r\nContent-Type: application/xml\r\n\r\n", "unsupported", "", 2},
		{"foreign charset", "HTTP/1.1 200 OK\r\nContent-Type: application/json;charset=latin1\r\n\r\n", "unsupported", "", 2},
		{"gzip unsupported", "HTTP/1.1 200 OK\r\nContent-Encoding: gzip\r\n\r\n", "unsupported", "", 2},
		{"body cap", "HTTP/1.1 200 OK\r\nContent-Length: 1000000000\r\n\r\n", "budget_exhausted", "", 2},
		{"negative length", "HTTP/1.1 200 OK\r\nContent-Length: -1\r\n\r\n", "invalid", "", 2},
		{"signed chunk", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n+2\r\n{}\r\n0\r\n\r\n", "invalid", "", 2},
		{"chunk cap", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n3\r\n{} \r\n0\r\n\r\n", "budget_exhausted", "", 2},
		{"cookie trailer", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nSet-Cookie: Session=unexpected\r\n\r\n", "invalid", "", 2},
		{"invalid LF", "HTTP/1.1 200 OK\n\n", "invalid", "", 2},
		{"unadmitted interim response", "HTTP/1.1 100 Continue\r\n\r\n", "unsupported", "", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := readSolaceManagementHTTPResponse(context.Background(), strings.NewReader(test.wire), test.limit, solaceHTTPTestRoute(), false)
			defer clear(response.body)
			defer clear(response.session)
			if kafkaManagementErrorReason(err) != test.reason || test.reason == "" && string(response.body) != test.body {
				t.Fatal("HTTP grammar or body bound changed", kafkaManagementErrorReason(err))
			}
		})
	}
	for _, wire := range []string{"HTTP/1.1 200 OK\r\nX: " + strings.Repeat("a", solaceManagementHeaderLimit) + "\r\n\r\n", "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n" + strings.Repeat("1\r\nx\r\n", solaceManagementChunkLimit) + "0\r\n\r\n"} {
		response, err := readSolaceManagementHTTPResponse(context.Background(), strings.NewReader(wire), solaceManagementBodyLimit, solaceHTTPTestRoute(), false)
		clear(response.body)
		clear(response.session)
		if kafkaManagementErrorReason(err) != "budget_exhausted" {
			t.Fatal("HTTP grammar accepted unbounded framing")
		}
	}
	response, err := readSolaceManagementHTTPResponse(context.Background(), strings.NewReader("HTTP/1.1 200 OK\r\nContent-Length: 8\r\n\r\n{}"), 8, solaceHTTPTestRoute(), false)
	if !errors.Is(err, io.ErrUnexpectedEOF) || len(response.body) != 2 {
		t.Fatal("partial bytes disappeared from quota accounting")
	}
}
func TestSolaceManagementCookieScopeAndLogoutDeletion(t *testing.T) {
	for _, test := range []struct {
		name, header, reason, value string
		logout                      bool
	}{
		{"owned host cookie", "Session=token; Path=/; Secure; HttpOnly", "", "Session=token", false},
		{"fixed SEMP scope", "Session=token; Path=/SEMP/v2", "", "Session=token", false},
		{"exact domain", "Session=token; Domain=broker.example.test; Path=/", "", "Session=token", false},
		{"foreign cookie", "Other=ignored", "", "", false},
		{"foreign domain", "Session=token; Domain=example.test", "permission_denied", "", false},
		{"entity only path", "Session=token; Path=/SEMP/v2/monitor", "permission_denied", "", false},
		{"prefix collision", "Session=token; Path=/SEMP/v", "permission_denied", "", false},
		{"expired first cookie", "Session=token; Max-Age=-1", "invalid", "", false},
		{"empty first cookie", "Session=", "invalid", "", false},
		{"logout deletion", "Session=; Max-Age=-1; Path=/", "", "", true},
		{"logout creates token", "Session=other; Path=/", "invalid", "", true},
		{"bounded header", "Session=" + strings.Repeat("a", 4096), "budget_exhausted", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := solaceManagementSessionCookie(test.header, solaceHTTPTestRoute(), test.logout)
			defer clear(value)
			if kafkaManagementErrorReason(err) != test.reason || string(value) != test.value {
				t.Fatal("cookie origin, path or logout contract changed", kafkaManagementErrorReason(err))
			}
		})
	}
	wire := "HTTP/1.1 200 OK\r\nSet-Cookie: Session=one; Path=/\r\nSet-Cookie: Session=two; Path=/\r\nContent-Length: 0\r\n\r\n"
	response, err := readSolaceManagementHTTPResponse(context.Background(), strings.NewReader(wire), 1, solaceHTTPTestRoute(), false)
	defer clear(response.session)
	if err == nil || !response.sessionUncertain {
		t.Fatal("multiple sessions were silently collapsed")
	}
}
func TestSolaceManagementOnlyFixedGETAndOwnLogoutAreWritable(t *testing.T) {
	route := solaceHTTPTestRoute()
	var writer bytes.Buffer
	if err := writeSolaceManagementRequest(&writer, route, false, []byte("Basic cmVhZGVyOnBhc3M=")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(writer.String(), "Host: broker.example.test:943\r\n") || strings.Contains(writer.String(), "Cookie:") {
		t.Fatal("custom port or credential surface changed")
	}
	writer.Reset()
	route.requestPath = "/SEMP/v2/action/about/user/logout"
	if err := writeSolaceManagementRequest(&writer, route, true, []byte("Session=token")); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(writer.String(), "PUT /SEMP/v2/action/about/user/logout HTTP/1.1\r\n") || !strings.HasSuffix(writer.String(), "\r\n\r\n{}") || strings.Contains(writer.String(), "Authorization:") {
		t.Fatal("logout surface changed")
	}
	for _, path := range []string{"/SEMP/v2/action/msgVpns/orders/queues/pending/delete", "/SEMP/v2/config/msgVpns/orders", "/SEMP/v2/action/about/user/logout?other=1"} {
		route.requestPath = path
		writer.Reset()
		if writeSolaceManagementRequest(&writer, route, true, []byte("Session=token")) == nil || writer.Len() != 0 {
			t.Fatal("arbitrary action emitted bytes")
		}
	}
	route.requestPath = "/SEMP/v2/action/about/user/logout"
	writer.Reset()
	if writeSolaceManagementRequest(&writer, route, true, []byte("Basic secret")) == nil || writer.Len() != 0 {
		t.Fatal("logout created a fresh Basic session")
	}
}
func TestSolaceManagementConcurrentCloseRetainsFirstPhysicalOwner(t *testing.T) {
	for _, ack := range []bool{false, true} {
		t.Run(map[bool]string{false: "unproven", true: "proven"}[ack], func(t *testing.T) {
			poll := newManagementObservation(managementTestTarget(), managementTestGrant("instance"), ManagementCredentials{})
			release, err := poll.retainResource()
			if err != nil {
				t.Fatal(err)
			}
			raw := &kafkaControlledCloseConn{entered: make(chan struct{}), unblock: make(chan struct{}), closeError: io.ErrClosedPipe, acknowledged: ack}
			conn := &solaceManagementConnection{raw: raw, release: release}
			first, second, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(raw.unblock) }) }
			defer func() {
				unblock()
				select {
				case <-first:
					release()
				case <-time.After(time.Second):
					t.Error("first owner failed fixture reap")
					return
				}
				for _, done := range []chan struct{}{second, finished} {
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Error("close/completion did not join")
					}
				}
			}()
			go func() { _ = conn.close(); close(first) }()
			go func() { _ = conn.close(); close(second) }()
			go func() { poll.finish(); close(finished) }()
			select {
			case <-raw.entered:
			case <-time.After(time.Second):
				t.Fatal("first physical close not entered")
			}
			select {
			case <-second:
				t.Fatal("second close bypassed first owner")
			case <-time.After(20 * time.Millisecond):
			}
			unblock()
			for _, done := range []chan struct{}{first, second} {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("physical close not joined")
				}
			}
			if ack {
				select {
				case <-finished:
				case <-time.After(time.Second):
					t.Fatal("proven closure remained charged")
				}
			} else {
				select {
				case <-finished:
					t.Fatal("error fabricated physical proof")
				case <-time.After(20 * time.Millisecond):
				}
			}
			if raw.calls.Load() != 1 {
				t.Fatal("raw close repeated")
			}
		})
	}
}
func TestSolaceManagementRequestRateIsSharedAndBounded(t *testing.T) {
	state := &solaceManagementOriginState{tokens: 0, refilled: time.Now()}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	if err := state.waitHTTP(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 170*time.Millisecond {
		t.Fatal("five-per-second origin rate was bypassed")
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if !errors.Is(state.waitHTTP(cancelled), context.Canceled) {
		t.Fatal("rate wait ignored cancellation")
	}
}
