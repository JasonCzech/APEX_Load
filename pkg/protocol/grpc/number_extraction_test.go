package grpc

import (
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/jsonextract/jsonextracttest"
)

func TestGRPCExtractionKeepsNumberTokens(t *testing.T) {
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

func TestGRPCJMESPathKeepsErrorWording(t *testing.T) {
	for _, tc := range []struct{ expr, body, want string }{
		{"id", `{"id":`, "invalid JSON response: "},
		{"items[", `{"items":[]}`, "invalid JMESPath expression: "},
		{"sum(name)", `{"name":"x"}`, "invalid JMESPath expression: "},
	} {
		if _, err := extractJMESPath(tc.expr, []byte(tc.body)); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("extractJMESPath(%q) error = %v, want prefix %q", tc.expr, err, tc.want)
		}
	}
	if _, err := extractJMESPath("missing", []byte(`{"id":1}`)); !errors.Is(err, protocol.ErrExtractionMiss) {
		t.Errorf("missing selection error = %v, want an extraction miss", err)
	}
}
