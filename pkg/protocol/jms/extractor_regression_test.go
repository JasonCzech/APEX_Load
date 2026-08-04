package jms

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestRegexExtractorWithoutCaptureReturnsFullMatch(t *testing.T) {
	got, err := extract(protocol.Extractor{Source: "body", Type: "regex", Expression: `order-\d+`}, message{Body: []byte("id=order-42")})
	if err != nil || got != "order-42" {
		t.Fatalf("extract() = %q, %v", got, err)
	}
}
