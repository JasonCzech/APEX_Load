package http

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"io"
	"math"
	"net"
	"sync"
	"time"
)

type networkSimulationProfile struct {
	latency          time.Duration
	jitter           time.Duration
	bandwidthBytesPS int
	packetLossPct    float64
	sleep            func(time.Duration)
	randFloat64      func() float64
}

func (p networkSimulationProfile) active() bool {
	return p.latency > 0 || p.jitter > 0 || p.bandwidthBytesPS > 0 || p.packetLossPct > 0
}

func newShapedDialContext(base func(context.Context, string, string) (net.Conn, error), profile networkSimulationProfile) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := base(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &shapedConn{Conn: conn, profile: profile, closed: make(chan struct{})}, nil
	}
}

type shapedConn struct {
	net.Conn
	profile networkSimulationProfile
	closed  chan struct{}
	closeMu sync.Once

	mu sync.Mutex
	// latency is amortized once per request/response exchange, not per byte:
	// writeLatched/readLatched track whether the current request/response has
	// already paid its latency. lastWasRead marks that a response completed, so
	// the next Write re-arms both latches — otherwise every request after the
	// first on a reused (keep-alive) connection would incur zero simulated
	// latency, jitter, or packet loss.
	writeLatched bool
	readLatched  bool
	lastWasRead  bool
}

func (c *shapedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.mu.Lock()
		applyLatency := !c.readLatched
		c.readLatched = true
		c.lastWasRead = true
		c.mu.Unlock()
		if applyLatency {
			if !c.wait(c.profile.jitterAndLossDelay()) {
				return n, net.ErrClosed
			}
		}
		if !c.wait(c.profile.bandwidthDelay(n)) {
			return n, net.ErrClosed
		}
	}
	return n, err
}

func (c *shapedConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	if c.lastWasRead {
		// A write after a completed response begins a new request on a reused
		// connection: re-arm so this request pays latency again.
		c.lastWasRead = false
		c.readLatched = false
		c.writeLatched = false
	}
	applyLatency := !c.writeLatched
	c.writeLatched = true
	c.mu.Unlock()
	if applyLatency {
		if !c.wait(c.profile.jitterAndLossDelay()) {
			return 0, net.ErrClosed
		}
	}
	if !c.wait(c.profile.bandwidthDelay(len(p))) {
		return 0, net.ErrClosed
	}
	return c.Conn.Write(p)
}

func (c *shapedConn) Close() error {
	c.closeMu.Do(func() {
		if c.closed != nil {
			close(c.closed)
		}
	})
	return c.Conn.Close()
}

func (c *shapedConn) wait(delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	if c.profile.sleep != nil {
		c.profile.sleep(delay)
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-c.closed:
		return false
	case <-timer.C:
		return true
	}
}

func (p networkSimulationProfile) jitterAndLossDelay() time.Duration {
	delay := p.latency
	if p.jitter > 0 {
		delay += time.Duration(p.rand() * float64(p.jitter))
	}
	if p.packetLossPct > 0 && p.rand()*100 < p.packetLossPct {
		delay += p.retransmissionPenalty()
	}
	return min(delay, MaxConfiguredTimeout)
}

func (p networkSimulationProfile) bandwidthDelay(bytes int) time.Duration {
	if p.bandwidthBytesPS <= 0 || bytes <= 0 {
		return 0
	}
	delaySeconds := float64(bytes) / float64(p.bandwidthBytesPS)
	if delaySeconds >= MaxConfiguredTimeout.Seconds() {
		return MaxConfiguredTimeout
	}
	return time.Duration(delaySeconds * float64(time.Second))
}

func (p networkSimulationProfile) retransmissionPenalty() time.Duration {
	base := p.latency
	if base <= 0 {
		base = 50 * time.Millisecond
	}
	penalty := base * 2
	if p.jitter > 0 {
		penalty += time.Duration(p.rand() * float64(p.jitter))
	}
	return penalty
}

func (p networkSimulationProfile) rand() float64 {
	if p.randFloat64 != nil {
		return p.randFloat64()
	}
	return defaultRandFloat64()
}

func defaultRandFloat64() float64 {
	var buf [8]byte
	if _, err := io.ReadFull(rand.Reader, buf[:]); err != nil {
		return 0
	}
	value := binary.BigEndian.Uint64(buf[:]) >> 11
	return float64(value) / float64(uint64(1)<<53)
}

func roundDurationToMillis(value time.Duration) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(math.Round(float64(value)/float64(time.Millisecond))) * time.Millisecond
}
