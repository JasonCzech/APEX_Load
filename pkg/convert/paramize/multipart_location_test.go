package paramize

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A suggestion can address a multipart text field ("body_multipart.<index>").
func TestApplySuggestionsRewritesMultipartFields(t *testing.T) {
	script := &dsl.Script{Name: "s", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://shop.test"}, Actions: []dsl.Action{
		{Name: "start", Type: "request", Method: "GET", Target: "/start"},
		{Name: "upload", Type: "request", Method: "POST", Target: "/upload", BodyMultipart: &dsl.MultipartBody{Parts: []dsl.MultipartPart{
			{Name: "order", Value: "order A9F3K2 for review"},
			{Name: "doc", Filename: "a.bin", Value: "QTlGM0sy", ValueEncoding: "base64"},
		}}},
	}}
	suggestion := Suggestion{
		ID: "corr-1", Kind: KindCorrelation, VarName: "order_id", Replacement: "{{var.order_id}}", LiteralValue: "A9F3K2",
		Locations:     []Location{{Section: "actions", ActionIndex: 1, Field: "body_multipart.0", SearchText: "A9F3K2", ReplaceText: "{{var.order_id}}"}},
		Extractor:     &dsl.Extractor{Name: "order_id", Source: "body", Type: "regex", Expression: `order (\w+)`},
		FoundInAction: &Location{Section: "actions", ActionIndex: 0},
		Confidence:    0.9,
	}
	applied, _, err := ApplySuggestions(script, []Suggestion{suggestion}, map[string]bool{"corr-1": true})
	if err != nil {
		t.Fatal(err)
	}
	parts := applied.Actions[1].BodyMultipart.Parts
	if parts[0].Value != "order {{var.order_id}} for review" {
		t.Fatalf("text part = %q", parts[0].Value)
	}
	if parts[1].Value != "QTlGM0sy" {
		t.Fatalf("a base64 part must not change: %q", parts[1].Value)
	}
	if _, ok := multipartFieldValue(&applied.Actions[1], "body_multipart.1"); ok {
		t.Fatal("a base64 part is not an addressable text field")
	}
}
