package h3fixture

import (
	"net"
	"sync/atomic"
	"time"
)

// gatedConn wraps the server's UDP socket so tests can blackhole or delay
// inbound QUIC packets without touching the kernel. Wrapping hides the
// *net.UDPConn from quic-go, which then uses its portable read path; that is
// acceptable for a functional fixture and irrelevant to client behavior.
type gatedConn struct {
	net.PacketConn
	blackhole atomic.Bool
	delay     atomic.Int64 // time.Duration
}

func (c *gatedConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		n, addr, err := c.PacketConn.ReadFrom(p)
		if err != nil {
			return n, addr, err
		}
		if c.blackhole.Load() {
			// Silently drop: the client sees no response at all, which is
			// how a stateful firewall that discards UDP behaves.
			continue
		}
		if d := time.Duration(c.delay.Load()); d > 0 {
			time.Sleep(d)
		}
		return n, addr, nil
	}
}
