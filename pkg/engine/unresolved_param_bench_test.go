package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Realistic POST action: a few KB of JSON body, headers, and a target — the
// shape safeProtoExecuteStream sees on every single action of every iteration
// of every VUser.
func benchAction() *protocol.Action {
	body, _ := json.Marshal(map[string]any{
		"customer": map[string]any{"id": "c-90210", "email": "buyer@example.test", "tier": "gold"},
		"items": func() []any {
			items := make([]any, 0, 40)
			for i := 0; i < 40; i++ {
				items = append(items, map[string]any{"sku": "SKU-000" + strings.Repeat("1", i%5+1), "qty": i%7 + 1, "price": 19.99})
			}
			return items
		}(),
		"notes": strings.Repeat("lorem ipsum dolor sit amet ", 40),
	})
	return &protocol.Action{
		Name:   "POST /checkout",
		Method: "POST",
		Target: "https://shop.example.test/api/v2/checkout?locale=en-US",
		Body:   body,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
			"X-Request-Id": "9f2c1b7e-5a44-4c31-8f0d-2b1a7c6e0d33",
		},
	}
}

func BenchmarkActionContainsUnresolvedParameter(b *testing.B) {
	action := benchAction()
	b.ReportAllocs()
	b.SetBytes(int64(len(action.Body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if actionContainsUnresolvedParameter(action) {
			b.Fatal("unexpected unresolved parameter")
		}
	}
}
