package importcontract

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/asyncapi"
	"github.com/jasonczech/apexload/pkg/convert/openapi"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/convert/raml"
)

// TestSpecImportersRejectStructurallyAmbiguousSources pins the shared
// srcparse policy across the spec importers.
//
// A duplicate or non-scalar mapping key means the converter and the human
// reviewing the import can read the same document differently — and the half
// nobody looked at is where a credential or an unexpected target hides.
// encoding/json and yaml.v3 both resolve these silently, so the rejection has
// to be explicit.
func TestSpecImportersRejectStructurallyAmbiguousSources(t *testing.T) {
	t.Run("openapi duplicate key", func(t *testing.T) {
		_, err := openapi.Convert(openapi.ImportRequest{Content: `openapi: "3.0.3"
info:
  title: Orders
  version: "1"
paths:
  /orders:
    get:
      operationId: listOrders
      operationId: listOrdersShadowed
      responses:
        "200":
          description: ok
`})
		requireAmbiguityError(t, err)
	})

	t.Run("asyncapi duplicate key", func(t *testing.T) {
		_, err := asyncapi.Convert(asyncapi.ImportRequest{Content: `asyncapi: "2.6.0"
info:
  title: Orders
  version: "1.0.0"
servers:
  production:
    url: broker.example.test:9092
    protocol: kafka
channels:
  orders.created:
    publish:
      operationId: publishOrderCreated
      operationId: publishOrderCreatedShadowed
`})
		requireAmbiguityError(t, err)
	})

	t.Run("raml duplicate key", func(t *testing.T) {
		_, err := raml.Convert(raml.ImportRequest{Content: `#%RAML 1.0
title: Orders
baseUri: https://api.example.test
baseUri: https://shadow.example.test
/orders:
  get:
`})
		requireAmbiguityError(t, err)
	})

	t.Run("postman duplicate key", func(t *testing.T) {
		_, err := postman.Convert(postman.ImportRequest{Content: `{
  "info": {"name": "Orders", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "item": [
    {
      "name": "list",
      "request": {"method": "GET", "url": "https://api.example.test/orders", "url": "https://shadow.example.test/orders"}
    }
  ]
}`})
		requireAmbiguityError(t, err)
	})
}

// TestSpecImportersTolerateMergeKeys is the other half of the severity split.
// Anchors plus "<<" are idiomatic in hand-authored OpenAPI and in RAML resource
// types, so rejecting them would break valid uploads. They are reported for
// review instead.
func TestSpecImportersTolerateMergeKeys(t *testing.T) {
	result, err := openapi.Convert(openapi.ImportRequest{Content: `openapi: "3.0.3"
info:
  title: Orders
  version: "1"
x-defaults: &defaults
  description: shared
paths:
  /orders:
    get:
      <<: *defaults
      operationId: listOrders
      responses:
        "200":
          description: ok
`})
	if err != nil {
		t.Fatalf("merge key was rejected: %v", err)
	}
	if result.Script == nil {
		t.Fatal("conversion produced no script")
	}
	if !strings.Contains(strings.ToLower(strings.Join(result.Unsupported.Strings(), " ")), "merge key") {
		t.Fatalf("tolerated merge key was not reported for review: %v", result.Unsupported.Strings())
	}
}

func requireAmbiguityError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("structurally ambiguous source was accepted")
	}
	lowered := strings.ToLower(err.Error())
	if !strings.Contains(lowered, "duplicate") && !strings.Contains(lowered, "non-scalar") {
		t.Fatalf("error does not name the ambiguity: %v", err)
	}
}
