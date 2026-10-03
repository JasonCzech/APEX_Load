package collectoragent

import (
	"context"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

type managementOrigin struct {
	slots chan struct{}
	poll  string
	next  time.Time
}
type managementOriginGate struct {
	mu      sync.Mutex
	origins map[string]*managementOrigin
}

func managementOriginKey(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}
func newManagementOriginGate(targets []AdapterTarget) *managementOriginGate {
	g := &managementOriginGate{origins: map[string]*managementOrigin{}}
	for _, target := range targets {
		if target.Management == nil {
			continue
		}
		for _, endpoint := range target.Management.Endpoints {
			key := managementOriginKey(endpoint)
			if g.origins[key] == nil {
				g.origins[key] = &managementOrigin{slots: make(chan struct{}, 2)}
			}
		}
	}
	return g
}

// Origin state is bounded by immutable local approval; no response can add an
// endpoint. Solace polls share one conservative 30s management cadence.
func (g *managementOriginGate) admit(ctx context.Context, endpoint, poll, pack string) (func(), error) {
	key := managementOriginKey(endpoint)
	g.mu.Lock()
	origin := g.origins[key]
	g.mu.Unlock()
	if origin == nil {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	select {
	case origin.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-origin.slots }
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	if pack == "solace-semp" {
		g.mu.Lock()
		now := time.Now()
		if origin.poll != poll && now.Before(origin.next) {
			g.mu.Unlock()
			release()
			return nil, &managementObservationError{reason: "throttled"}
		}
		if origin.poll != poll {
			origin.poll = poll
			origin.next = now.Add(30 * time.Second)
		}
		g.mu.Unlock()
	}
	return release, nil
}
