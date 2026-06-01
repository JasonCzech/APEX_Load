package http

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestParseNetworkSimulationProfile_PresetWithJitterAndLoss(t *testing.T) {
	profile := parseNetworkSimulationProfile(map[string]any{
		"web_network_simulation_mode":            "preset",
		"web_network_simulation_preset":          "dsl",
		"web_network_simulation_jitter_ms":       float64(12),
		"web_network_simulation_packet_loss_pct": 1.5,
	})

	if profile.latency != 50*time.Millisecond {
		t.Fatalf("latency = %v, want 50ms", profile.latency)
	}
	if profile.bandwidthBytesPS != 196608 {
		t.Fatalf("bandwidthBytesPS = %d, want 196608", profile.bandwidthBytesPS)
	}
	if profile.jitter != 12*time.Millisecond {
		t.Fatalf("jitter = %v, want 12ms", profile.jitter)
	}
	if profile.packetLossPct != 1.5 {
		t.Fatalf("packetLossPct = %v, want 1.5", profile.packetLossPct)
	}
}

func TestNetworkSimulationProfile_CustomDelayAccounting(t *testing.T) {
	var sleeps []time.Duration
	profile := networkSimulationProfile{
		latency:          40 * time.Millisecond,
		jitter:           10 * time.Millisecond,
		bandwidthBytesPS: 1000,
		packetLossPct:    100,
		sleep: func(delay time.Duration) {
			sleeps = append(sleeps, roundDurationToMillis(delay))
		},
		randFloat64: func() float64 { return 0.5 },
	}

	profile.applyWriteDelay(500)

	if len(sleeps) != 2 {
		t.Fatalf("sleep calls = %d, want 2", len(sleeps))
	}
	if sleeps[0] != 130*time.Millisecond {
		t.Fatalf("first sleep = %v, want 130ms", sleeps[0])
	}
	if sleeps[1] != 500*time.Millisecond {
		t.Fatalf("second sleep = %v, want 500ms", sleeps[1])
	}
}

func TestShapedDialContextWrapsConnection(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()

	var sleeps []time.Duration
	dialContext := newShapedDialContext(
		func(context.Context, string, string) (net.Conn, error) {
			return clientConn, nil
		},
		networkSimulationProfile{
			latency:          20 * time.Millisecond,
			bandwidthBytesPS: 1000,
			sleep: func(delay time.Duration) {
				sleeps = append(sleeps, roundDurationToMillis(delay))
			},
			randFloat64: func() float64 { return 1 },
		},
	)

	conn, err := dialContext(context.Background(), "tcp", "example.test:443")
	if err != nil {
		t.Fatalf("dialContext returned error: %v", err)
	}
	defer conn.Close()

	go func() {
		_, _ = io.Copy(io.Discard, serverConn)
	}()

	if _, err := conn.Write([]byte("payload")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	if len(sleeps) != 2 {
		t.Fatalf("sleep calls = %d, want 2", len(sleeps))
	}
	if sleeps[0] != 20*time.Millisecond {
		t.Fatalf("first sleep = %v, want 20ms", sleeps[0])
	}
	if sleeps[1] != 7*time.Millisecond {
		t.Fatalf("second sleep = %v, want 7ms", sleeps[1])
	}
}
