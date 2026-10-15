package openapi

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A specification's own title identifies it; a framework's default title
// (springdoc's "OpenAPI definition") does not, so the file name stands in.
func TestConvertStampsTheDeclaredTitle(t *testing.T) {
	for _, test := range []struct {
		title, want string
		declared    bool
	}{
		{"Orders API", "Orders API", true},
		{"OpenAPI definition", "orders.json", false},
	} {
		result, err := Convert(ImportRequest{Name: "orders.json", Content: `{"openapi":"3.0.3","info":{"title":"` + test.title + `","version":"1"},` +
			`"servers":[{"url":"https://api.example.test"}],"paths":{"/orders":{"get":{"operationId":"listOrders","responses":{"200":{}}}}}}`})
		if err != nil {
			t.Fatal(err)
		}
		document, _, declared, ok := dsl.ImportSourceIdentityOf(result.Script.Metadata.Import)
		if !ok || document != test.want || declared != test.declared {
			t.Errorf("title %q: identity = %q declared=%v ok=%v", test.title, document, declared, ok)
		}
	}
}
