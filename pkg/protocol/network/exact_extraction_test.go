package network

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestNetworkExtractionPreservesExactNumbers(t *testing.T) {
	result := &protocol.Result{ResponseBody: `{"records":[{"id":9007199254740993}],"response":{"id":9007199254740993},"record_count":1}`}
	err := runNetworkExtractors([]protocol.Extractor{
		{Name: "id", Source: "body", Type: "jsonpath", Expression: "$.records[0].id", OnMiss: "fail"},
		{Name: "response", Source: "response"},
		{Name: "count", Source: "record_count"},
	}, result)
	if err != "" {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"id": "9007199254740993", "response": `{"id":9007199254740993}`, "count": "1"} {
		if got := result.Extractions[name]; got != want {
			t.Errorf("%s=%q want=%q", name, got, want)
		}
	}
	if err := evaluateNetworkAssertions([]protocol.Assertion{{Type: "record_count", Expected: 1}}, result); err != "" {
		t.Fatal(err)
	}
}

func TestNetworkExtractionFormatsSevenDigitNumbers(t *testing.T) {
	for _, raw := range []string{"1234567", "-1234567", "1234567.25"} {
		t.Run(raw, func(t *testing.T) {
			result := &protocol.Result{ResponseBody: `{"records":[{"id":` + raw + `}],"response":` + raw + `}`}
			if err := runNetworkExtractors([]protocol.Extractor{
				{Name: "jsonpath", Source: "body", Type: "jsonpath", Expression: "$.records[0].id", OnMiss: "fail"},
				{Name: "jmespath", Source: "body", Type: "jmespath", Expression: "records[0].id", OnMiss: "fail"},
				{Name: "response", Source: "response"},
			}, result); err != "" {
				t.Fatal(err)
			}
			for _, name := range []string{"jsonpath", "jmespath", "response"} {
				if got := result.Extractions[name]; got != raw {
					t.Errorf("%s=%q want=%q", name, got, raw)
				}
			}
		})
	}
}

func TestNetworkPayloadRejectsTrailingDocument(t *testing.T) {
	if _, err := decodeNetworkPayload(`{"response":1} null`); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}
