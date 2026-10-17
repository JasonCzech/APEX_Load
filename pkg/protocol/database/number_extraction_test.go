package database

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/jsonextract/jsonextracttest"
)

func TestBodyExtractionKeepsNumberTokens(t *testing.T) {
	for _, tc := range jsonextracttest.NumberCases {
		t.Run(tc.Name, func(t *testing.T) {
			body := jsonextracttest.Body(tc.Raw)
			for _, extractor := range []protocol.Extractor{
				{Type: "jsonpath", Expression: "$.id"},
				{Type: "jmespath", Expression: "ids[0]"},
			} {
				if got, err := extractDatabaseBodyValue(extractor, body); err != nil || got != tc.Raw {
					t.Errorf("%s %s = %q, %v; want %q", extractor.Type, extractor.Expression, got, err, tc.Raw)
				}
			}
		})
	}
}
