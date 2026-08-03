package enterprisearchive

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestWebSubmitDataURLFormEncodingPreservesOccurrenceOrder(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action() {
		web_submit_data("Filters", "Action=https://example.test/search", "Method=PUT",
			"EncType=application/x-www-form-urlencoded;charset=UTF-8", ITEMDATA,
			"Name=full name", "Value=A&B + café", ENDITEM,
			"Name=full name", "Value=blue/red", ENDITEM,
			"Name=token", "Value=prefix/{Session}?active", ENDITEM, LAST);
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Functions) != 1 {
		t.Fatalf("functions = %#v", parsed.Functions)
	}
	fn := parsed.Functions[0]
	wantBody := "full+name=A%26B+%2B+caf%C3%A9&full+name=blue%2Fred&token=prefix%2F{{var.session}}%3Factive"
	if fn.Body != wantBody {
		t.Fatalf("body = %q, want %q", fn.Body, wantBody)
	}
	if fn.Method != "PUT" {
		t.Fatalf("method = %q, want PUT", fn.Method)
	}
	if got := fn.Headers["Content-Type"]; got != "application/x-www-form-urlencoded;charset=UTF-8" {
		t.Fatalf("content type = %q", got)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	action := result.Script.Actions[0]
	if got := action.Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey]; got != dsl.ImportURLFormTemplateValueEncodingQueryComponent {
		t.Fatalf("template encoding metadata = %#v, want %q", got, dsl.ImportURLFormTemplateValueEncodingQueryComponent)
	}
}

func TestWebSubmitDataDefaultsToURLFormEncoding(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action() {
		web_submit_data("Login", "Action=https://example.test/login", ITEMDATA,
			"Name=user", "Value=ada lovelace", ENDITEM, LAST);
	}`)
	if err != nil {
		t.Fatal(err)
	}
	fn := parsed.Functions[0]
	if fn.Body != "user=ada+lovelace" || fn.Headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("parsed form = body %q headers %#v", fn.Body, fn.Headers)
	}
}

func TestWebSubmitDataUnsupportedEncodingsArePreservedAndGated(t *testing.T) {
	tests := []struct {
		name    string
		encType string
		extra   string
	}{
		{name: "multipart", encType: "multipart/form-data"},
		{name: "json", encType: "application/json"},
		{name: "raw", encType: "raw"},
		{name: "explicit empty", encType: ""},
		{name: "file field", encType: "application/x-www-form-urlencoded", extra: `, "FilePath=payload.bin"`},
		{name: "non UTF-8 charset", encType: "application/x-www-form-urlencoded;charset=shift_jis"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := `Action() {
				web_submit_data("Upload", "Action=https://example.test/upload", "Method=POST",
					"EncType=` + tc.encType + `", ITEMDATA,
					"Name=payload", "Value=a&b"` + tc.extra + `, ENDITEM, LAST);
			}`
			parsed, err := ParseActionFile("Action.c", source)
			if err != nil {
				t.Fatal(err)
			}
			if len(parsed.Functions) != 1 || parsed.Functions[0].itemDataReviewReason != unsupportedItemDataEncodingReason {
				t.Fatalf("parsed function = %#v", parsed.Functions)
			}
			result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Script.Actions) != 1 {
				t.Fatalf("actions = %#v", result.Script.Actions)
			}
			action := result.Script.Actions[0]
			if action.Body != "" {
				t.Fatalf("body = %q, want empty gated body", action.Body)
			}
			for name := range action.Headers {
				if strings.EqualFold(name, "Content-Type") {
					t.Fatalf("headers = %#v, unsupported EncType must not be advertised", action.Headers)
				}
			}
			if action.Metadata["execution_blocker"] != "import_review_required" {
				t.Fatalf("metadata = %#v, want durable execution gate", action.Metadata)
			}
			review, _ := action.Metadata["import_review"].(map[string]any)
			if !reflect.DeepEqual(review["reason_codes"], []string{unsupportedItemDataEncodingReason}) {
				t.Fatalf("import review = %#v", review)
			}
			foundReviewMapping := false
			for _, mapping := range result.SourceReview.Mappings {
				if mapping.Function == "web_submit_data" && mapping.Status == "review_only" {
					foundReviewMapping = true
				}
			}
			if !foundReviewMapping {
				t.Fatalf("source mappings = %#v, want review_only web_submit_data", result.SourceReview.Mappings)
			}
		})
	}
}

func TestWebSubmitDataMalformedItemShapesArePreservedAndGated(t *testing.T) {
	tests := []struct {
		name     string
		itemData string
	}{
		{
			name:     "opaque value expression",
			itemData: `"Name=token", lr_unmask("6a2c27c0f5925d3f80e5cc3a44ab4f2e"), ENDITEM,`,
		},
		{
			name:     "unknown field attribute",
			itemData: `"Name=payload", "Value=data", "TransferMode=binary", ENDITEM,`,
		},
		{
			name:     "missing value",
			itemData: `"Name=payload", ENDITEM,`,
		},
		{
			name:     "missing end item",
			itemData: `"Name=payload", "Value=data",`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := `Action() {
				web_submit_data("Submit", "Action=https://example.test/submit",
					"EncType=application/x-www-form-urlencoded", ITEMDATA,
					` + tc.itemData + ` LAST);
			}`
			parsed, err := ParseActionFile("Action.c", source)
			if err != nil {
				t.Fatal(err)
			}
			if len(parsed.Functions) != 1 {
				t.Fatalf("functions = %#v", parsed.Functions)
			}
			fn := parsed.Functions[0]
			if fn.itemDataReviewReason != unsupportedItemDataEncodingReason || fn.itemDataURLForm {
				t.Fatalf("ITEMDATA classification = reason %q URL form %v", fn.itemDataReviewReason, fn.itemDataURLForm)
			}

			result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{UseMaskedBodySecretReferences: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Script.Actions) != 1 {
				t.Fatalf("actions = %#v", result.Script.Actions)
			}
			action := result.Script.Actions[0]
			if action.Body != "" {
				t.Fatalf("body = %q, want empty gated body", action.Body)
			}
			if got := action.Metadata["execution_blocker"]; got != "import_review_required" {
				t.Fatalf("execution blocker = %#v, want durable import_review_required", got)
			}
			if _, exists := action.Metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey]; exists {
				t.Fatalf("gated action retained executable form-template encoding metadata: %#v", action.Metadata)
			}
		})
	}
}
