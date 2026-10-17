package jms

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestJMSNumericObjectKey(t *testing.T) {
	result := &protocol.Result{Extractions: map[string]string{}}
	err := runExtractors([]protocol.Extractor{{Name: "count", Source: "body", Type: "jsonpath", Expression: `$.counts["123"]`, OnMiss: "fail"}}, result, message{Body: []byte(`{"counts":{"123":"ok"}}`)})
	if err != nil || result.Extractions["count"] != "ok" {
		t.Fatalf("numeric object key: extracted=%q err=%v; want ok", result.Extractions["count"], err)
	}
}
func TestJMSArrayControl(t *testing.T) {
	got, err := extractJSONPath(`$.items[1]`, []byte(`{"items":["a","b"]}`))
	if err != nil || got != "b" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestJMSExtractionPreservesNumericValues(t *testing.T) {
	for _, raw := range []string{"1234567", "-1234567", "9007199254740993"} {
		t.Run(raw, func(t *testing.T) {
			result := &protocol.Result{}
			err := runExtractors([]protocol.Extractor{
				{Name: "id", Source: "body", Type: "jsonpath", Expression: "$.id", OnMiss: "fail"},
				{Name: "nested_id", Source: "body", Type: "jsonpath", Expression: "$.ids[0]", OnMiss: "fail"},
			}, result, message{Body: []byte(`{"id":` + raw + `,"ids":[` + raw + `]}`)})
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
