package jms

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol/jsonextract/jsonextracttest"
)

func TestMessageExtractionKeepsNumberTokens(t *testing.T) {
	for _, tc := range jsonextracttest.NumberCases {
		t.Run(tc.Name, func(t *testing.T) {
			if got, err := extractJSONPath("$.id", jsonextracttest.Body(tc.Raw)); err != nil || got != tc.Raw {
				t.Errorf("JSONPath = %q, %v; want %q", got, err, tc.Raw)
			}
		})
	}
}
