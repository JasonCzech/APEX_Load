package postman

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A collection's Postman ID, else its declared name, identifies it, so a
// re-import of the same collection can update its script.
func TestConvertStampsTheCollectionIdentity(t *testing.T) {
	for _, test := range []struct {
		name, info, want string
	}{
		{"postman id", `"_postman_id":"5c1f7d2e-orders","name":"Orders"`, "postman:5c1f7d2e-orders"},
		{"declared name", `"name":"Orders"`, "Orders"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := Convert(ImportRequest{Content: `{"info":{` + test.info + `,"schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},` +
				`"item":[{"name":"List","request":{"method":"GET","url":"https://api.example.test/orders"}}]}`})
			if err != nil {
				t.Fatal(err)
			}
			document, _, declared, ok := dsl.ImportSourceIdentityOf(result.Script.Metadata.Import)
			if !ok || document != test.want || !declared {
				t.Fatalf("identity = %q declared=%v ok=%v, want %q declared", document, declared, ok, test.want)
			}
		})
	}
}

// Postman's default collection name declares nothing without a Postman ID, so
// the file name identifies the collection instead.
func TestConvertDoesNotDeclareTheDefaultCollectionName(t *testing.T) {
	result, err := Convert(ImportRequest{CollectionFileName: "orders.postman_collection.json", Content: `{"info":{"name":"New Collection",` +
		`"schema":"https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},` +
		`"item":[{"name":"List","request":{"method":"GET","url":"https://api.example.test/orders"}}]}`})
	if err != nil {
		t.Fatal(err)
	}
	document, _, declared, ok := dsl.ImportSourceIdentityOf(result.Script.Metadata.Import)
	if !ok || declared || document == "New Collection" {
		t.Fatalf("identity = %q declared=%v ok=%v, want the file name, undeclared", document, declared, ok)
	}
}
