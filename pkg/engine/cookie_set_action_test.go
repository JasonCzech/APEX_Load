package engine

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type cookieSetterProtocol struct {
	stubProtocol
	set []protocol.ScriptCookie
}

func (c *cookieSetterProtocol) SetCookie(_ context.Context, cookie protocol.ScriptCookie) error {
	c.set = append(c.set, cookie)
	return nil
}

func TestCookieSetActionDispatchesInterpolatedCookies(t *testing.T) {
	proto := &cookieSetterProtocol{}
	vu := &VUser{ID: 1, Group: "test", Proto: proto, Metrics: NewMetricsCollector(16), Script: &dsl.Script{Name: "cookies", Protocol: "http"}}
	vu.setSourceVariable("sid", "S-42")
	handled, err := vu.handleEngineAction(context.Background(), dsl.Action{Name: "c", Type: "cookie_set", Params: map[string]any{
		"name": "sid", "value": "{{var.sid}}", "domain": ".shop.test", "path": "/app", "secure": true,
	}})
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if _, err := vu.handleEngineAction(context.Background(), dsl.Action{Name: "r", Type: "cookie_set", Params: map[string]any{"name": "sid", "remove": true}}); err != nil {
		t.Fatal(err)
	}
	if len(proto.set) != 2 {
		t.Fatalf("cookies = %+v", proto.set)
	}
	first := proto.set[0]
	if first.Name != "sid" || first.Value != "S-42" || first.Domain != ".shop.test" || first.Path != "/app" || !first.Secure || first.Remove {
		t.Fatalf("first cookie = %+v", first)
	}
	if !proto.set[1].Remove {
		t.Fatalf("second cookie must be a removal: %+v", proto.set[1])
	}

	plain := &VUser{ID: 2, Group: "test", Proto: &stubProtocol{}, Metrics: NewMetricsCollector(16), Script: &dsl.Script{Name: "plain", Protocol: "http"}}
	if handled, err := plain.handleEngineAction(context.Background(), dsl.Action{Name: "c", Type: "cookie_set", Params: map[string]any{"name": "a"}}); !handled || err != nil {
		t.Fatalf("protocols without a cookie store treat cookie_set as a no-op: handled=%v err=%v", handled, err)
	}
}
