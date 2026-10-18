package importreview

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/har"
)

func TestSalesforceAugmentationRetainsCaptureProvenance(t *testing.T) {
	data := []byte(`{"log":{"entries":[{"request":{"method":"GET","url":"https://acme.my.salesforce.com/services/data/v60.0/query"},"response":{"status":200,"content":{"mimeType":"application/json","text":"{\"records\":[],\"done\":true}"}}}]}}`)
	for _, tc := range []struct{ source, want string }{{"har", "har"}, {"salesforce_har", "har"}, {"browser_recording_har", "browser_recording_har"}} {
		t.Run(tc.source, func(t *testing.T) {
			result, err := har.Convert(data, har.ConvertOptions{})
			if err != nil {
				t.Fatal(err)
			}
			augmentation := AugmentHARImportWithSalesforce(result, data, "project-a", "capture", tc.source, true, false, true)
			if !augmentation.Detected || augmentation.Script.GeneratedBy != "apexload-salesforce-builder" || augmentation.Script.GeneratedFrom != tc.want {
				t.Fatalf("incorrect source: detected=%v by=%s from=%s", augmentation.Detected, augmentation.Script.GeneratedBy, augmentation.Script.GeneratedFrom)
			}
			if augmentation.Script.Metadata.Import.Source != "salesforce" {
				t.Fatal("native destination profile lost")
			}
			if result.Document == nil {
				t.Fatal("missing shared parsed source")
			}
		})
	}
	unrelated := []byte(strings.ReplaceAll(strings.ReplaceAll(string(data), "acme.my.salesforce.com", "example.test"), "/services/data/v60.0/query", "/orders"))
	result, err := har.Convert(unrelated, har.ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(result.Script)
	augmentation := AugmentHARImportWithSalesforce(result, unrelated, "", "capture", "har", true, false, true)
	after, _ := json.Marshal(augmentation.Script)
	if augmentation.Detected || augmentation.ReviewForResponse != nil || string(before) != string(after) {
		t.Fatal("undetected HAR was augmented")
	}
}

func TestHARClientEvidenceSharedAndFallbackPathsAgree(t *testing.T) {
	data := []byte(`{"log":{"entries":[{"startedDateTime":"2026-10-09T12:00:00Z","request":{"method":"POST","url":"https://example.test/log","postData":{"mimeType":"application/json","text":"{\"clientSessionId\":\"6f1c2a3b-4d5e-4f60-8172-93a4b5c6d7e8\",\"ts\":1791547200000}"}},"response":{"status":200,"content":{"mimeType":"application/json","text":"{\"ok\":true}"}}}]}}`)
	shared, err := har.Convert(data, har.ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := har.Convert(data, har.ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fallback.Document = nil
	// Conversion stamps creation time independently; keep the same starting
	// timestamp while comparing all authoring fields from the two evidence paths.
	fallback.Script.CreatedAt = shared.Script.CreatedAt
	for _, result := range []*har.ConvertResult{shared, fallback} {
		ApplyClientGeneratedUUIDs(result.Script, data, result)
		ApplyClientTimestamps(result.Script, data, result)
	}
	shared.Document = nil
	if !reflect.DeepEqual(shared.Script, fallback.Script) || !reflect.DeepEqual(shared.Unsupported, fallback.Unsupported) || !reflect.DeepEqual(shared.Warnings, fallback.Warnings) {
		t.Fatal("shared source evidence changed UUID/timestamp authoring")
	}
}

func TestHARClientEvidenceAmbiguousCaseFallback(t *testing.T) {
	data := []byte(`{"log":{"entries":[{"request":{"method":"POST","url":"https://example.test/log","postData":{"mimeType":"application/json","text":"{\"clientSessionId\":\"6f1c2a3b-4d5e-4f60-8172-93a4b5c6d7e8\"}"}},"response":{"status":204,"Status":200,"content":{"mimeType":"application/json","text":"{\"ok\":true}"}}}]}}`)
	shared, err := har.Convert(data, har.ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := har.Convert(data, har.ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fallback.Document = nil
	fallback.Script.CreatedAt = shared.Script.CreatedAt
	if _, _, err = shared.Document.ServerUUIDs(); err == nil {
		t.Fatal("ambiguous status aliases did not request raw evidence")
	}
	ApplyClientGeneratedUUIDs(shared.Script, data, shared)
	ApplyClientGeneratedUUIDs(fallback.Script, data, fallback)
	if len(shared.Script.Parameters) == 0 || !reflect.DeepEqual(shared.Script, fallback.Script) || !reflect.DeepEqual(shared.Warnings, fallback.Warnings) {
		t.Fatal("case alias fallback changed UUID authoring")
	}
}

func TestHARClientEvidenceDuplicateObjectMergeFallback(t *testing.T) {
	const id = "6f1c2a3b-4d5e-4f60-8172-93a4b5c6d7e8"
	data := []byte(`{"log":{"entries":[{"request":{"method":"POST","url":"https://example.test/log","postData":{"mimeType":"application/json","text":"{\"clientSessionId\":\"` + id + `\"}"}},"response":{"status":200,"content":{"text":"{\"id\":\"` + id + `\"}"},"content":{"mimeType":"application/json"}}}]}}`)
	shared, err := har.Convert(data, har.ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := har.Convert(data, har.ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fallback.Document = nil
	fallback.Script.CreatedAt = shared.Script.CreatedAt
	ApplyClientGeneratedUUIDs(shared.Script, data, shared)
	ApplyClientGeneratedUUIDs(fallback.Script, data, fallback)
	if len(shared.Script.Parameters) != 0 || !reflect.DeepEqual(shared.Script, fallback.Script) || !reflect.DeepEqual(shared.Warnings, fallback.Warnings) {
		t.Fatal("duplicate object merge lost server UUID evidence")
	}
}
