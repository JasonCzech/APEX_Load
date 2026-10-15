package raml

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertStampsTheDeclaredTitle(t *testing.T) {
	result, err := Convert(ImportRequest{Name: "orders.raml", Content: "#%RAML 1.0\ntitle: Orders API\nbaseUri: https://api.example.test\n/orders:\n  get:\n"})
	if err != nil {
		t.Fatal(err)
	}
	document, _, declared, ok := dsl.ImportSourceIdentityOf(result.Script.Metadata.Import)
	if !ok || document != "Orders API" || !declared {
		t.Fatalf("identity = %q declared=%v ok=%v", document, declared, ok)
	}
}
