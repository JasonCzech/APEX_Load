package har

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestDiffPairsReorderedApexAuraActionsByClassAndMethod(t *testing.T) {
	makeAuraPost := func(r int, className, method, extraParamKey, extraParamVal, responseBody string) string {
		msg := fmt.Sprintf(
			`{"actions":[{"id":"1;a","descriptor":"aura://ApexActionController/ACTION$execute","callingDescriptor":"UNKNOWN","params":{"namespace":"","classname":%q,"method":%q,"params":{%q:%q}}}]}`,
			className, method, extraParamKey, extraParamVal,
		)
		form := "message=" + url.QueryEscape(msg) + "&aura.context=%7B%22mode%22%3A%22PROD%22%7D&aura.token=tok1"
		return fmt.Sprintf(`{
			"startedDateTime":"2026-01-01T00:00:0%dZ",
			"time":15,
			"request":{
				"method":"POST",
				"url":"https://org.sandbox.lightning.force.com/aura?r=%d&aura.ApexAction.execute=1",
				"headers":[{"name":"Content-Type","value":"application/x-www-form-urlencoded"}],
				"postData":{"mimeType":"application/x-www-form-urlencoded","text":%q}
			},
			"response":{
				"status":200,
				"headers":[{"name":"Content-Type","value":"application/json"}],
				"content":{"mimeType":"application/json","text":%q}
			}
		}`, r, r, form, responseBody)
	}
	makeConsumer := func(r int, interactionID string) string {
		msg := fmt.Sprintf(
			`{"actions":[{"id":"2;a","descriptor":"aura://ApexActionController/ACTION$execute","callingDescriptor":"UNKNOWN","params":{"namespace":"","classname":"CompleteCallController","method":"completeCall","params":{"interactionId":%q}}}]}`,
			interactionID,
		)
		form := "message=" + url.QueryEscape(msg) + "&aura.context=%7B%22mode%22%3A%22PROD%22%7D&aura.token=tok1"
		return fmt.Sprintf(`{
			"startedDateTime":"2026-01-01T00:00:09Z",
			"time":15,
			"request":{
				"method":"POST",
				"url":"https://org.sandbox.lightning.force.com/aura?r=%d&aura.ApexAction.execute=1",
				"headers":[{"name":"Content-Type","value":"application/x-www-form-urlencoded"}],
				"postData":{"mimeType":"application/x-www-form-urlencoded","text":%q}
			},
			"response":{
				"status":200,
				"headers":[{"name":"Content-Type","value":"application/json"}],
				"content":{"mimeType":"application/json","text":"{\"state\":\"SUCCESS\"}"}
			}
		}`, r, form)
	}

	// Baseline executes CustomerLookup first (r=1) and CreateInteraction second (r=2).
	baseline := pairHAR([]string{
		makeAuraPost(1, "CustomerSearchController", "findAccount", "staticFilter", "ACTIVE_ONLY", `{"returnValue":{"status":"READY"}}`),
		makeAuraPost(2, "InteractionController", "createInteraction", "channel", "VOICE_INBOUND", `{"interactionId":"int_alpha_100001"}`),
		makeConsumer(3, "int_alpha_100001"),
	})
	// Comparison swaps the parallel Apex execution order: CreateInteraction runs first (r=1), CustomerLookup second (r=2).
	comparison := pairHAR([]string{
		makeAuraPost(1, "InteractionController", "createInteraction", "channel", "VOICE_INBOUND", `{"interactionId":"int_bravo_200002"}`),
		makeAuraPost(2, "CustomerSearchController", "findAccount", "staticFilter", "ACTIVE_ONLY", `{"returnValue":{"status":"READY"}}`),
		makeConsumer(3, "int_bravo_200002"),
	})

	report, err := Diff(baseline, comparison, ConvertOptions{ScriptName: "Apex Reordered Pair"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if report.DynamicCandidates != 1 {
		t.Fatalf("DynamicCandidates = %d, want 1; classifications=%#v", report.DynamicCandidates, report.Classifications)
	}
	got := report.Classifications[0]
	if got.Classification != DiffClassificationDynamic || got.BaselineEntry != 2 || got.ComparisonEntry != 1 {
		t.Fatalf("classification = %#v, want dynamic aligned baseline entry 2 -> comparison entry 1", got)
	}
	// Static Apex params ("ACTIVE_ONLY" / "VOICE_INBOUND") must NOT appear as changed request inputs
	// when the two Apex calls run in swapped order.
	report.ResolveUnhandledInputs(func(string) bool { return true })
	for _, input := range report.UnhandledInputs {
		if strings.Contains(input.Field, "staticFilter") || strings.Contains(input.Field, "channel") {
			t.Fatalf("static Apex param mispaired across reordered Apex calls: %#v", report.UnhandledInputs)
		}
	}
}

func TestConvertHARRedactSensitiveScrubsSourceReviewAndPreservesFilename(t *testing.T) {
	redact := true
	result, err := Convert([]byte(`{
  "log": {
    "entries": [{
      "request": {
        "method": "POST",
        "url": "https://org.my.salesforce.com/?sid=00Dxx0000001gEREAY!AQEAQSyntheticSessionTokenValue12345",
        "headers": [{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}],
        "postData": {"text": "un=tester%40example.test&pw=SynthPass%2199"}
      },
      "response": {"status": 200, "headers": [], "content": {"text": "ok"}}
    }]
  }
}`), ConvertOptions{
		ScriptName:      "Redacted Salesforce HAR",
		SourceFileName:  "capture-baseline.har",
		RedactSensitive: &redact,
	})
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if result.Governance == nil || !result.Governance.RedactionEnabled || result.Governance.Sensitivity != scrub.SensitivityScrubbed {
		t.Fatalf("governance = %#v, want RedactionEnabled=true and Sensitivity=%q", result.Governance, scrub.SensitivityScrubbed)
	}
	if result.SourceReview == nil || len(result.SourceReview.SourceFiles) != 1 {
		t.Fatalf("source review = %#v, want 1 source file", result.SourceReview)
	}
	sf := result.SourceReview.SourceFiles[0]
	if sf.Path != "capture-baseline.har" || !sf.Redacted {
		t.Fatalf("source file = %+v, want Path=capture-baseline.har Redacted=true", sf)
	}
	if strings.Contains(sf.Content, "SynthPass") || strings.Contains(sf.Content, "AQEAQSyntheticSessionTokenValue12345") {
		t.Fatalf("redacted source content leaked sensitive values:\n%s", sf.Content)
	}
}

func TestSalvageTruncatedIgnoresTrailingWhitespaceInDroppedBytes(t *testing.T) {
	full := salvageHAR("a", "b", "c")
	cutAfterSecond := full[:strings.Index(full, `,{"request":{"method":"GET","url":"https://example.test/c"`)]
	_, report, ok := SalvageTruncated([]byte(cutAfterSecond + "  \r\n\t\n"))
	if !ok {
		t.Fatal("expected truncated HAR with trailing whitespace to salvage")
	}
	if report.Entries != 2 || report.DroppedBytes != 0 {
		t.Fatalf("report = %+v, want Entries=2 DroppedBytes=0", report)
	}
}
