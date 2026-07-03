package engine

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// cookieControllerProtocol is a minimal protocol that records ClearCookies
// invocations so the engine's cookie_clear dispatch can be asserted.
type cookieControllerProtocol struct {
	stubProtocol
	clearCalls int
}

func (c *cookieControllerProtocol) ClearCookies(context.Context) error {
	c.clearCalls++
	return nil
}

func TestHandleEngineActionDispatchesCookieClear(t *testing.T) {
	proto := &cookieControllerProtocol{}
	vu := &VUser{ID: 1, Group: "g", Script: &dsl.Script{Name: "s"}, Proto: proto}

	handled, err := vu.handleEngineAction(context.Background(), dsl.Action{Name: "clear", Type: "cookie_clear"})
	if !handled {
		t.Fatal("cookie_clear should be handled by the engine")
	}
	if err != nil {
		t.Fatalf("cookie_clear returned error: %v", err)
	}
	if proto.clearCalls != 1 {
		t.Fatalf("ClearCookies calls = %d, want 1", proto.clearCalls)
	}
}

func TestHandleEngineActionCookieClearNoControllerIsNoop(t *testing.T) {
	// A protocol without the CookieController capability (and the nil-protocol
	// case used by the catalog cross-check) must treat cookie_clear as a
	// successful no-op rather than erroring.
	for name, proto := range map[string]protocol.Protocol{
		"no-controller": &stubProtocol{},
		"nil-protocol":  nil,
	} {
		t.Run(name, func(t *testing.T) {
			vu := &VUser{ID: 1, Group: "g", Script: &dsl.Script{Name: "s"}, Proto: proto}
			handled, err := vu.handleEngineAction(context.Background(), dsl.Action{Name: "clear", Type: "cookie_clear"})
			if !handled || err != nil {
				t.Fatalf("cookie_clear handled=%v err=%v, want handled with no error", handled, err)
			}
		})
	}
}

func TestVUserRunClearsHTTPCookiesPerIteration(t *testing.T) {
	clearCookiesPerIteration := true
	proto := &cookieControllerProtocol{}
	vu := &VUser{
		ID:    1,
		Group: "g",
		Proto: proto,
		Script: &dsl.Script{
			Name:     "s",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 3},
				HTTP:     dsl.HTTPConfig{ClearCookiesPerIter: &clearCookiesPerIteration},
			},
			Actions: []dsl.Action{{Name: "GET /", Type: "request", Method: "GET", Target: "/"}},
		},
		Metrics: NewMetricsCollector(16),
	}

	vu.Run(context.Background())

	if proto.clearCalls != 2 {
		t.Fatalf("ClearCookies calls = %d, want 2 between 3 iterations", proto.clearCalls)
	}
}
