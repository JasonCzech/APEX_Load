//go:build unix

package http

import (
	"context"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/h3fixture"
)

// udpSocketCount counts the process's open UDP sockets.
func udpSocketCount(t *testing.T) int {
	t.Helper()
	dir, err := os.Open("/dev/fd")
	if err != nil {
		t.Skipf("cannot list descriptors: %v", err)
	}
	// Names only: stat-ing each entry fails on the listing's own descriptor.
	names, err := dir.Readdirnames(-1)
	_ = dir.Close()
	if err != nil {
		t.Skipf("cannot list descriptors: %v", err)
	}
	count := 0
	for _, name := range names {
		fd, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		if kind, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE); err == nil && kind == syscall.SOCK_DGRAM {
			count++
		}
	}
	return count
}

func eventuallyUDPSockets(t *testing.T, want int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	got := udpSocketCount(t)
	for got != want && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		got = udpSocketCount(t)
	}
	return got
}

// Decision D3: each virtual user's session owns one UDP socket for all of
// its QUIC connections, and teardown releases it (docs/HTTP3_IMPLEMENTATION.md).
func TestEachHTTP3SessionOwnsOneUDPSocketUntilTeardown(t *testing.T) {
	fixture := h3fixture.Start(t, h3fixture.Options{})
	t.Setenv(EnvHTTP3, "true")
	baseline := udpSocketCount(t)
	sessions := make([]*HTTP, 3)
	for index := range sessions {
		h := &HTTP{}
		if err := h.Init(context.Background(), map[string]any{"tls_insecure_skip_verify": true, "http_version": "3", quicHandshakeTimeoutKey: float64(2000)}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Teardown(context.Background()) })
		// Two requests, one of them to another path, share the session's socket.
		for _, path := range []string{"/echo", "/echo?again=1"} {
			if result := run(t, h, h3Get(t, fixture.URL(path), "")); !result.Success || result.HTTPVersionNegotiated != VersionHTTP3 {
				t.Fatalf("session %d %s: %q", index, path, result.ErrorMessage)
			}
		}
		sessions[index] = h
	}
	if got := eventuallyUDPSockets(t, baseline+len(sessions)); got != baseline+len(sessions) {
		t.Fatalf("UDP sockets = %d, want %d (baseline %d plus one per session)", got, baseline+len(sessions), baseline)
	}
	for _, h := range sessions {
		if err := h.Teardown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := eventuallyUDPSockets(t, baseline); got != baseline {
		t.Fatalf("UDP sockets after teardown = %d, want baseline %d", got, baseline)
	}
}
