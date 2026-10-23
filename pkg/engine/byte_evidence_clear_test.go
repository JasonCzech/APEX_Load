package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// A result whose public evidence is cleared keeps its protocol and byte
// evidence vocabularies, so its byte counts stay measurements of their
// population rather than silently leaving it (NA-C).
func TestClearedEvidenceKeepsProtocolAndByteEvidence(t *testing.T) {
	result := &protocol.Result{DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, BytesSent: 11, BytesReceived: 22, ErrorMessage: "token=abc", ResponseBody: "private"}
	clearUnredactableResultEvidence(result)
	if result.DependencyType != "http" || result.ByteEvidence != protocol.ByteEvidenceHTTPBody || result.BytesSent != 11 || result.BytesReceived != 22 {
		t.Fatalf("cleared result %+v", result)
	}
	if result.ResponseBody != "" || result.ErrorMessage == "token=abc" {
		t.Fatal("public evidence survived the clear")
	}
	// A dependency type without byte evidence can carry parameter text: it
	// is cleared.
	jms := &protocol.Result{DependencyType: "jms_secret-value", BytesSent: 3}
	clearUnredactableResultEvidence(jms)
	if jms.DependencyType != "" || jms.BytesSent != 3 {
		t.Fatalf("cleared JMS result %+v", jms)
	}
}
