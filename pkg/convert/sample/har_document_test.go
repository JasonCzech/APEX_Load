package sample

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/clientids"
	"github.com/jasonczech/apexload/pkg/convert/clienttimestamps"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
)

func documentFixture() string {
	return `{"log":{"entries":[{"startedDateTime":"2026-10-09T12:00:00Z","timings":{"nested":{"empty":{},"value":1}},"request":{"method":"GET","url":"https://example.test/","headers":[]},"response":{"status":200,"redirectURL":"/next?at=1791547200","headers":[{"name":"X-Value","value":"b0b0b0b0-b1b1-4c2c-8d3d-e4e4e4e4e4e4"}],"cookies":[{"name":"value","value":"c0c0c0c0-b1b1-4c2c-8d3d-e4e4e4e4e4e4"}],"content":{"mimeType":"application/json","encoding":"base64","text":"` + base64.StdEncoding.EncodeToString([]byte(`{"id":"a0a0a0a0-b1b1-4c2c-8d3d-e4e4e4e4e4e4","time":1791547200000}`)) + `"}}},{"request":{"method":"GET","url":"https://example.test/app.js"},"response":{"status":200,"content":{"mimeType":"application/javascript","text":"var x=1;"}}}]}}`
}

func TestHARDocumentEvidenceMatchesExistingAdapters(t *testing.T) {
	for _, content := range []string{
		strings.NewReplacer(`"startedDateTime"`, `"StartedDateTime"`, `"response"`, `"Response"`, `"content"`, `"Content"`, `"text"`, `"Text"`, `"headers"`, `"Headers"`, `"value"`, `"Value"`, `"name"`, `"Name"`, `"status"`, `"Status"`, `"mimeType"`, `"MimeType"`, `"encoding"`, `"Encoding"`, `"redirectURL"`, `"RedirectURL"`, `"cookies"`, `"Cookies"`).Replace(documentFixture()),
		strings.ReplaceAll(documentFixture(), `"status":200`, `"status":200.0`),
		strings.ReplaceAll(documentFixture(), `"status":200`, `"status":2e2`),
		strings.ReplaceAll(documentFixture(), `"status":200`, `"status":9223372036854775807`),
		documentFixture(), "\uFEFF" + documentFixture(), strings.ReplaceAll(documentFixture(), `"status":200`, `"status":204`), strings.ReplaceAll(documentFixture(), `"mimeType":"application/json"`, `"mimeType":"application/octet-stream"`), strings.ReplaceAll(documentFixture(), `"encoding":"base64"`, `"encoding":"unknown"`), strings.ReplaceAll(documentFixture(), `"value":"b0b0b0b0-b1b1-4c2c-8d3d-e4e4e4e4e4e4"`, `"value":5`)} {
		doc, err := ParseHARDocument(content)
		if err != nil {
			t.Fatal(err)
		}
		before, judged, oldErr := clientids.ServerUUIDs([]byte(content))
		after, afterJudged, newErr := doc.ServerUUIDs()
		if (oldErr == nil) != (newErr == nil) || (oldErr == nil && (!reflect.DeepEqual(before, after) || judged != afterJudged)) {
			t.Fatalf("UUID evidence drift: before=%v/%v/%v after=%v/%v/%v", before, judged, oldErr, after, afterJudged, newErr)
		}
		oldTime, oldErr := clienttimestamps.FromHAR([]byte(content))
		newTime, newErr := doc.ClientTimestamps()
		if (oldErr == nil) != (newErr == nil) || (oldErr == nil && !reflect.DeepEqual(oldTime, newTime)) {
			t.Fatalf("timestamp evidence drift: before=%v/%v after=%v/%v", oldTime, oldErr, newTime, newErr)
		}
	}
}

func TestHARDocumentProjectionsCannotMutateSource(t *testing.T) {
	content := documentFixture()
	doc, err := ParseHARDocument(content)
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Matches([]byte(content)) || doc.Matches([]byte(content+" ")) {
		t.Fatal("document identity does not match exact capture")
	}
	expected, err := doc.SourceObservations(true)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := doc.SourceObservations(true)
	if err != nil {
		t.Fatal(err)
	}
	sources[0].ResponseHeaders["X-Value"] = "changed"
	sources[0].RequestHeaders["Cookie"] = "changed"
	var diagnostics diag.Collector
	exchanges, _, _, _, err := doc.parse(true, requestimport.HTTPVersionModeUpgrade, &diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	nested := exchanges[0].Metadata["har.timings"].(map[string]any)["nested"].(map[string]any)
	if nested["empty"] == nil {
		t.Fatal("empty source object changed to null")
	}
	nested["value"] = "changed"
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := doc.SourceObservations(true)
			if err != nil || !reflect.DeepEqual(got, expected) {
				t.Errorf("projection mutated: %v, %v", got, err)
			}
			if _, _, err := doc.ServerUUIDs(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	exchanges, _, _, _, err = doc.parse(true, requestimport.HTTPVersionModeUpgrade, &diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if got := exchanges[0].Metadata["har.timings"].(map[string]any)["nested"].(map[string]any)["value"]; got != float64(1) {
		t.Fatalf("source metadata changed: %v", got)
	}
}

func TestHARConvertKeepsDocumentPrivate(t *testing.T) {
	result, err := Convert(ImportRequest{Format: "har", Content: documentFixture()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Document == nil || !result.Document.Matches([]byte(documentFixture())) {
		t.Fatal("conversion did not retain source document")
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["Document"]; ok {
		t.Fatal("private HAR document escaped")
	}
}

func BenchmarkHARSharedAnalysisDocument(b *testing.B) {
	content := strings.Replace(documentFixture(), "var x=1;", strings.Repeat("ordinary response payload ", 1<<15), 1)
	b.Run("separate", func(b *testing.B) {
		b.SetBytes(int64(len(content)))
		b.ReportAllocs()
		for b.Loop() {
			if _, err := HARSourceObservations(content, true); err != nil {
				b.Fatal(err)
			}
			if _, err := clienttimestamps.FromHAR([]byte(content)); err != nil {
				b.Fatal(err)
			}
			if _, _, err := clientids.ServerUUIDs([]byte(content)); err != nil {
				b.Fatal(err)
			}
			_ = harWebSockets(content)
		}
	})
	b.Run("shared", func(b *testing.B) {
		b.SetBytes(int64(len(content)))
		b.ReportAllocs()
		for b.Loop() {
			doc, err := ParseHARDocument(content)
			if err != nil {
				b.Fatal(err)
			}
			if _, err = doc.SourceObservations(true); err != nil {
				b.Fatal(err)
			}
			if _, err = doc.ClientTimestamps(); err != nil {
				b.Fatal(err)
			}
			if _, _, err = doc.ServerUUIDs(); err != nil {
				b.Fatal(err)
			}
			_ = doc.webSockets()
		}
	})
}

func TestHARDocumentAmbiguousTypedEvidenceUsesRawFallback(t *testing.T) {
	content := strings.Replace(documentFixture(), `"startedDateTime":"2026-10-09T12:00:00Z"`, `"startedDateTime":"2026-10-09T12:00:00Z","StartedDateTime":"2026-10-10T12:00:00Z"`, 1)
	doc, err := ParseHARDocument(content)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = doc.ClientTimestamps(); err == nil {
		t.Fatal("ambiguous case aliases guessed source order")
	}
	if _, err = clienttimestamps.FromHAR([]byte(content)); err != nil {
		t.Fatal("raw adapter no longer accepts case aliases")
	}
	// Conversion must retain the old adapter as the rare ambiguity fallback.
	result, err := Convert(ImportRequest{Format: "har", Content: content})
	if err != nil || result.Script == nil {
		t.Fatalf("fallback conversion failed: %v", err)
	}
}

func TestHARDocumentDuplicateEvidenceRequiresOrderedRawFallback(t *testing.T) {
	for _, fields := range []string{
		`"content":{"text":"1791547200"},"content":{"mimeType":"text/plain"}`,
		`"response":{"content":{"text":"1791547200"}},"response":{"status":200}`,
		`"content":{"text":"first"},"cont\u0065nt":{"text":"last"}`,
	} {
		var content string
		if strings.HasPrefix(fields, `"response"`) {
			content = `{"log":{"entries":[{"request":{"method":"GET","url":"https://example.test/"},` + fields + `}]}}`
		} else {
			content = `{"log":{"entries":[{"request":{"method":"GET","url":"https://example.test/"},"response":{` + fields + `}}]}}`
		}
		doc, err := ParseHARDocument(content)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = doc.ClientTimestamps(); err == nil {
			t.Fatalf("duplicate evidence not detected: %s", fields)
		}
		if _, _, err = doc.ServerUUIDs(); err == nil {
			t.Fatalf("duplicate UUID evidence not detected: %s", fields)
		}
		if _, err = clienttimestamps.FromHAR([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	for _, content := range []string{documentFixture(), `{"log":{"entries":[{"request":{"method":"GET","url":"https://example.test/"},"response":{"content":{"text":"literal \\\"content\\\":{} and braces {} []"}}},{"response":{"content":{"text":"other"}}}]}}`} {
		if hasDuplicateHAREvidenceFields(content) {
			t.Fatalf("separate objects or body text mistaken for duplicate fields: %s", content)
		}
	}
}
