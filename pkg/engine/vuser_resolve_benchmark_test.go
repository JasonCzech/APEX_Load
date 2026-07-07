package engine

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func BenchmarkVUserResolveActionLiteralLargeBody(b *testing.B) {
	vu := &VUser{
		Variables: map[string]string{
			"base_url": "https://api.example.test",
			"token":    "token-123",
		},
	}
	action := dsl.Action{
		Name:   "literal-upload",
		Type:   "request",
		Method: "POST",
		Target: "https://api.example.test/upload",
		Headers: map[string]string{
			"Authorization": "Bearer static-token",
			"Content-Type":  "application/json",
		},
		Body: `{"payload":"` + strings.Repeat("x", 64*1024) + `"}`,
		Params: map[string]any{
			"route": "literal",
			"tags":  []any{"one", "two", "three"},
		},
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(action.Body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := vu.resolveAction(action); got == nil || len(got.Body) == 0 {
			b.Fatal("resolveAction returned empty action")
		}
	}
}

func BenchmarkVUserResolveActionTemplated(b *testing.B) {
	vu := &VUser{
		Variables: map[string]string{
			"base_url":   "https://api.example.test",
			"account":    "acct-123",
			"session":    "session-456",
			"region":     "east",
			"request_id": "req-789",
		},
	}
	action := dsl.Action{
		Name:   "templated-request",
		Type:   "request",
		Method: "POST",
		Target: "{{var.base_url}}/accounts/{{var.account}}/orders",
		Headers: map[string]string{
			"Authorization": "Bearer {{var.session}}",
			"X-Region":      "{{var.region}}",
			"X-Request-ID":  "{{var.request_id}}",
		},
		Body: strings.Repeat(`{"account":"{{var.account}}","request":"{{var.request_id}}","region":"{{var.region}}"}`, 256),
		Params: map[string]any{
			"account": "{{var.account}}",
			"nested": map[string]any{
				"session": "{{var.session}}",
			},
		},
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(action.Body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := vu.resolveAction(action); got == nil || string(got.Body) == action.Body {
			b.Fatal("resolveAction did not interpolate")
		}
	}
}
