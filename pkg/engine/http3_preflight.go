package engine

import (
	"context"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// HTTP3ProbeResult is one scenario-preflight QUIC handshake check of an
// origin a group's HTTP/3 policy reaches.
type HTTP3ProbeResult struct {
	Group       string    `json:"group"`
	Origin      string    `json:"origin"`
	Policy      string    `json:"policy"` // 3 or auto+h3
	OK          bool      `json:"ok"`
	ErrorClass  string    `json:"error_class,omitempty"`
	Error       string    `json:"error,omitempty"`
	Address     string    `json:"address,omitempty"`
	HandshakeMs float64   `json:"handshake_ms,omitempty"`
	Skipped     string    `json:"skipped,omitempty"` // why the origin was not checked
	CheckedAt   time.Time `json:"checked_at"`
}

// Blocking reports whether the result can block a run under the "block"
// policy: a checked exact-3 origin whose handshake failed.
func (r HTTP3ProbeResult) Blocking() bool {
	return r.Policy == httpprotocol.VersionHTTP3 && r.Skipped == "" && !r.OK
}

const (
	http3ProbeConcurrency  = 4
	http3ProbeDeadlineSkip = "preflight time limit reached before the handshake finished"
)

// ProbeHTTP3Group checks, in parallel, each https origin the group's HTTP/3
// policies reach with one QUIC handshake and no request. It uses a
// disposable session built from the group's script, secrets and target
// policy, so it never seeds a VUser's Alt-Svc, ticket or token caches.
// Dynamic targets and proxied routes are listed with the reason they were
// not checked.
func ProbeHTTP3Group(ctx context.Context, group GroupConfig) []HTTP3ProbeResult {
	origins := dsl.HTTP3ProbeOrigins(group.Script)
	if len(origins) == 0 {
		return nil
	}
	results := make([]HTTP3ProbeResult, len(origins))
	now := time.Now().UTC()
	for index, origin := range origins {
		results[index] = HTTP3ProbeResult{Group: group.Name, Origin: origin.Origin, Policy: origin.Policy, CheckedAt: now}
		if origin.Dynamic {
			results[index].Skipped = "dynamic target: the origin is known only during the run"
		}
	}
	session := &httpprotocol.HTTP{}
	if err := session.Init(ctx, buildProtoConfigWithSecretResolver(group.Script, group.SecretResolver)); err != nil {
		// Not a QUIC finding: the run itself reports why its session failed.
		for index := range results {
			if results[index].Skipped == "" {
				results[index].Skipped = "probe session could not start: " + err.Error()
			}
		}
		return results
	}
	defer func() { _ = session.Teardown(context.Background()) }()
	slots := make(chan struct{}, http3ProbeConcurrency)
	var wg sync.WaitGroup
	for index := range results {
		if results[index].Skipped != "" {
			continue
		}
		wg.Add(1)
		go func(result *HTTP3ProbeResult) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				result.Skipped = http3ProbeDeadlineSkip
				return
			}
			outcome := session.ProbeHTTP3(ctx, result.Origin)
			result.CheckedAt = time.Now().UTC()
			if outcome.Proxied {
				result.Skipped = "proxy route: these requests use TCP"
				return
			}
			result.Address = outcome.Address
			if outcome.Err != nil {
				if ctx.Err() != nil {
					// The preflight's own time limit ended the check, which
					// says nothing about the origin.
					result.Skipped = http3ProbeDeadlineSkip
					return
				}
				result.ErrorClass = outcome.ErrorClass
				result.Error = outcome.Err.Error()
				return
			}
			result.OK = true
			result.HandshakeMs = float64(outcome.Duration.Microseconds()) / 1000
		}(&results[index])
	}
	wg.Wait()
	return results
}
