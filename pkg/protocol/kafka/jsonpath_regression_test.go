package kafka

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestKafkaExtractionPreservesNumericValues(t *testing.T) {
	for _, raw := range []string{"1234567", "-1234567", "9007199254740993"} {
		t.Run(raw, func(t *testing.T) {
			result := &protocol.Result{}
			err := runExtractors([]protocol.Extractor{
				{Name: "id", Source: "body", Type: "jsonpath", Expression: "$.id", OnMiss: "fail"},
				{Name: "nested_id", Source: "body", Type: "jsonpath", Expression: "$.ids[0]", OnMiss: "fail"},
			}, result, kafkaRecord{Value: []byte(`{"id":` + raw + `,"ids":[` + raw + `]}`)})
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"id", "nested_id"} {
				if got := result.Extractions[name]; got != raw {
					t.Errorf("%s=%q want=%q", name, got, raw)
				}
			}
		})
	}
}
