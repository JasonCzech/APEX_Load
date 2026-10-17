package http

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestCachedDNSDialerReservesTimeForFallbackAddresses(t *testing.T) {
	for _, network := range []string{"tcp", "tcp4", "tcp6"} {
		t.Run(network, func(t *testing.T) {
			var attempted []string
			reachable := "192.0.2.2:443"
			if network == "tcp6" {
				reachable = "[2001:db8::2]:443"
			}
			dial := newCachedDNSDialer(func(ctx context.Context, network, address string) (net.Conn, error) {
				attempted = append(attempted, address)
				if address != reachable {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				a, b := net.Pipe()
				_ = b.Close()
				return a, nil
			}, func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP("2001:db8::1")}, {IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("192.0.2.2")}, {IP: net.ParseIP("2001:db8::2")}}, nil
			}, time.Now)
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
			defer cancel()
			conn, err := dial(ctx, network, "example.test:443")
			if conn != nil {
				defer conn.Close()
			}
			if err != nil || conn == nil {
				t.Fatalf("reachable fallback was not used: attempted=%v, err=%v", attempted, err)
			}
			want := 3
			if network != "tcp" {
				want = 2
			}
			if len(attempted) != want || ctx.Err() != nil {
				t.Fatalf("attempted=%v, parent err=%v", attempted, ctx.Err())
			}
		})
	}
}

func TestCachedDNSDialerCancellationStopsFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := 0
	dial := newCachedDNSDialer(func(attemptCtx context.Context, _, _ string) (net.Conn, error) {
		attempts++
		cancel()
		<-attemptCtx.Done()
		return nil, attemptCtx.Err()
	}, func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("192.0.2.2")}}, nil
	}, time.Now)
	_, err := dial(ctx, "tcp", "example.test:443")
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("attempts=%d, err=%v", attempts, err)
	}
}
