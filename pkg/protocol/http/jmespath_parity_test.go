package http

import (
	"errors"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/jsonextract"
	"github.com/jasonczech/apexload/pkg/protocol/jsonextract/jsonextracttest"
)

// HTTP keeps its own JMESPath implementation until it delegates to jsonextract.
// Until then the two must agree on every value, miss and failure.
func TestJMESPathMatchesSharedExtraction(t *testing.T) {
	const items = `{"items":[{"id":1234567890123456789,"price":12,"tags":["a"]},{"id":2,"price":3,"tags":["b"]}]}`
	corpus := []struct{ expr, body string }{
		{"id", `{"id":1.2300}`},
		{"ids[0]", `{"ids":[9007199254740993]}`},
		{"items[?price > `10`].id", items},
		{"items[?id > `1`].price", items},
		{"items[*].id", items},
		{"items[?contains(tags, 'b')].price | [0]", items},
		{"items[0] | price > `5`", items},
		{"sum(items[].price)", items},
		{"max(items[].price)", items},
		{"length(items)", items},
		{"to_string(items[1].price)", items},
		{"sum(items)", `{"items":[1,2.5,3]}`},
		{"avg(items)", `{"items":[1234566,1234568]}`},
		{"{a: id, b: name}", `{"id":20261017,"name":"x"}`},
		{"@", `[1.5,-0,1e21]`},
		{"missing", `{"id":1}`},
		{"id", `{"id":null}`},
		{"items[", `{"items":[]}`},
		{"sum(name)", `{"name":"x"}`},
		{"id", `{"id":`},
	}
	for _, tc := range jsonextracttest.NumberCases {
		corpus = append(corpus, struct{ expr, body string }{"ids[0]", string(jsonextracttest.Body(tc.Raw))})
	}
	for _, tc := range corpus {
		httpValue, httpErr := extractJMESPath(tc.expr, []byte(tc.body))
		sharedValue, sharedErr := jsonextract.JMESPath(tc.expr, []byte(tc.body))
		httpMiss := errors.Is(httpErr, protocol.ErrExtractionMiss)
		sharedMiss := errors.Is(sharedErr, jsonextract.ErrNotFound)
		switch {
		case httpMiss || sharedMiss:
			if httpMiss != sharedMiss {
				t.Errorf("%s on %s: http miss=%v (%v), shared miss=%v (%v)", tc.expr, tc.body, httpMiss, httpErr, sharedMiss, sharedErr)
			}
		case (httpErr == nil) != (sharedErr == nil):
			t.Errorf("%s on %s: http error %v, shared error %v", tc.expr, tc.body, httpErr, sharedErr)
		case httpErr == nil && httpValue != sharedValue:
			t.Errorf("%s on %s: http %q, shared %q", tc.expr, tc.body, httpValue, sharedValue)
		}
	}
}

func TestHTTPExtractionKeepsNumberTokens(t *testing.T) {
	for _, tc := range jsonextracttest.NumberCases {
		t.Run(tc.Name, func(t *testing.T) {
			body := jsonextracttest.Body(tc.Raw)
			if got, err := extractJSONPath("$.id", body); err != nil || got != tc.Raw {
				t.Errorf("JSONPath = %q, %v; want %q", got, err, tc.Raw)
			}
			if got, err := extractJMESPath("ids[0]", body); err != nil || got != tc.Raw {
				t.Errorf("JMESPath = %q, %v; want %q", got, err, tc.Raw)
			}
		})
	}
}
