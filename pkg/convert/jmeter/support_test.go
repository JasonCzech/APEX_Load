package jmeter

import (
	"strings"
	"testing"
)

func TestGetSupportMatrixEntriesStructure(t *testing.T) {
	entries := GetSupportMatrixEntries()
	if len(entries) == 0 {
		t.Fatal("expected structured support matrix entries")
	}

	validStatus := map[string]bool{statusSupported: true, statusPartial: true, statusReviewOnly: true}
	validExec := map[string]bool{execExecutable: true, execReviewRequired: true, execAdapterRequired: true, execNotExecutable: true}

	seenIDs := map[string]bool{}
	var sawSupported, sawReviewOnly bool
	for _, entry := range entries {
		if entry.ID == "" || entry.Construct == "" || entry.Area == "" {
			t.Fatalf("entry missing id/construct/area: %#v", entry)
		}
		if seenIDs[entry.ID] {
			t.Fatalf("duplicate support matrix id: %s", entry.ID)
		}
		seenIDs[entry.ID] = true
		if !validStatus[entry.Status] {
			t.Fatalf("entry %s has invalid status %q", entry.ID, entry.Status)
		}
		if !validExec[entry.ExecutionStatus] {
			t.Fatalf("entry %s has invalid execution_status %q", entry.ID, entry.ExecutionStatus)
		}
		switch entry.Status {
		case statusSupported:
			sawSupported = true
		case statusReviewOnly:
			sawReviewOnly = true
		}
	}
	if !sawSupported || !sawReviewOnly {
		t.Fatalf("expected both supported and review_only entries, got supported=%v review_only=%v", sawSupported, sawReviewOnly)
	}
}

func TestGetSupportMatrixDerivesFlatArrays(t *testing.T) {
	flat := GetSupportMatrix()
	if len(flat.Supported) == 0 {
		t.Fatal("expected derived supported entries")
	}
	if len(flat.Unsupported) == 0 {
		t.Fatal("expected derived unsupported entries")
	}

	// Every flat entry must trace back to a structured entry in the right bucket.
	bucket := map[string]string{}
	for _, entry := range GetSupportMatrixEntries() {
		switch entry.Status {
		case statusSupported:
			bucket[entry.Construct] = "supported"
		case statusPartial:
			bucket[entry.Construct] = "partial"
		default:
			bucket[entry.Construct] = "unsupported"
		}
	}
	for _, construct := range flat.Supported {
		if bucket[construct] != "supported" {
			t.Fatalf("supported flat entry %q not derived from a supported structured entry", construct)
		}
	}
	for _, construct := range flat.Partial {
		if bucket[construct] != "partial" {
			t.Fatalf("partial flat entry %q not derived from a partial structured entry", construct)
		}
	}
	for _, construct := range flat.Unsupported {
		if bucket[construct] != "unsupported" {
			t.Fatalf("unsupported flat entry %q not derived from a review/unsupported structured entry", construct)
		}
	}
	if len(GetSupportMatrixNotes()) == 0 {
		t.Fatal("expected support matrix notes")
	}
}

func TestGetSupportMatrixDocumentsHTTPAuthDisposition(t *testing.T) {
	for _, entry := range GetSupportMatrixEntries() {
		if entry.ID != "jmeter-auth-manager" {
			continue
		}
		if entry.Status != statusPartial || entry.ExecutionStatus != execReviewRequired || !entry.ReviewRequired {
			t.Fatalf("auth manager support = %#v, want partial review-gated support", entry)
		}
		return
	}
	t.Fatal("missing jmeter-auth-manager support entry")
}

func TestCategoryForSourceMapping(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		targetKind string
		wantCat    string
		wantOK     bool
	}{
		{"converted action is mapped", "converted", "action", categoryMapped, true},
		{"converted controller is mapped", "converted", "controller", categoryMapped, true},
		{"review_only is review_required", "review_only", "controller", categoryReviewRequired, true},
		{"adapter_required passes through", categoryAdapterRequired, "action", categoryAdapterRequired, true},
		{"unsupported counts", "unsupported", "unsupported", categoryUnsupported, true},
		{"contributor is skipped", "converted", "contributor", "", false},
		{"config is skipped", "converted", "config", "", false},
		{"csv export is a contributor", "converted", "export", "", false},
		{"generated parameter is a contributor", "review_only", "parameter", "", false},
		{"dynamic function is a contributor", "converted", "dynamic_function", "", false},
		{"ignored is informational", "ignored", "diagnostic", "", false},
		{"unknown status is informational", "something", "action", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := categoryForSourceMapping(tc.status, tc.targetKind)
			if ok != tc.wantOK || got != tc.wantCat {
				t.Fatalf("categoryForSourceMapping(%q,%q) = (%q,%v), want (%q,%v)", tc.status, tc.targetKind, got, ok, tc.wantCat, tc.wantOK)
			}
		})
	}
}

func TestSupportMatrixDocumentsCountersAndMessagingDisclosures(t *testing.T) {
	notes := map[string]string{}
	for _, entry := range GetSupportMatrixEntries() {
		notes[entry.ID] = entry.Notes
	}
	if !strings.Contains(notes["jmeter-counter"], "unique-number blocks") {
		t.Fatalf("counter row missing or incomplete: %q", notes["jmeter-counter"])
	}
	// jmeterCounterRangeHoldsBlocks keeps unique-number blocks whenever the
	// thread count is not a literal; the note must not promise a wrap there.
	if !strings.Contains(notes["jmeter-counter"], "not a literal it keeps unique-number blocks") || strings.Contains(notes["jmeter-counter"], "two million") {
		t.Fatalf("counter row misdescribes a non-literal thread count: %q", notes["jmeter-counter"])
	}
	if !jmeterCounterRangeHoldsBlocks(1, 3, 0) || jmeterCounterRangeHoldsBlocks(1, 3, 4) {
		t.Fatal("the note no longer matches jmeterCounterRangeHoldsBlocks")
	}
	for _, want := range []string{"compression.type", "max_in_flight_requests", "batch.size", "client.id", "EntityPath", "sampler schedules have no execution path"} {
		if !strings.Contains(notes["jmeter-messaging-producers"], want) {
			t.Fatalf("messaging producer notes lack %q", want)
		}
	}
	if strings.Contains(notes["jmeter-udv"], "{{var.") {
		t.Fatal("UDV notes still claim {{var.*}} values; the converter inlines literals")
	}
}

// The UDV note must say what happens to a UDV in an HTTP sampler parameter:
// the domain is expanded, the parameter is not, and the request is gated.
func TestSupportMatrixUDVNoteMatchesSamplerParameters(t *testing.T) {
	plan := `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan" enabled="true"/>
    <hashTree>
      <Arguments testname="UDV" enabled="true" elementType="Arguments">
        <collectionProp name="Arguments.arguments">
          <elementProp name="host" elementType="Argument"><stringProp name="Argument.name">host</stringProp><stringProp name="Argument.value">api.example.test</stringProp></elementProp>
        </collectionProp>
      </Arguments>
      <hashTree/>
      <ThreadGroup testname="API" enabled="true"/>
      <hashTree>
        <HTTPSamplerProxy testname="S1" enabled="true">
          <elementProp name="HTTPsampler.Arguments" elementType="Arguments">
            <collectionProp name="Arguments.arguments">
              <elementProp name="q" elementType="HTTPArgument"><stringProp name="Argument.name">q</stringProp><stringProp name="Argument.value">${host}</stringProp></elementProp>
            </collectionProp>
          </elementProp>
          <stringProp name="HTTPSampler.domain">${host}</stringProp>
          <stringProp name="HTTPSampler.path">/search</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`
	result, err := ConvertJMX([]byte(plan), ConvertOptions{FileName: "udv.jmx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) == 0 {
		t.Fatalf("scripts = %+v", result.Scripts)
	}
	script := result.Scripts[0]
	if !strings.Contains(script.Config.BaseURL, "api.example.test") {
		t.Fatalf("base_url = %q, want the UDV expanded", script.Config.BaseURL)
	}
	if q, _ := script.Actions[0].Params["q"].(string); q == "api.example.test" || !strings.Contains(q, "host") {
		t.Fatalf("params.q = %q; the converter now expands UDVs in sampler parameters, so update the jmeter-udv note", q)
	}
	for _, entry := range GetSupportMatrixEntries() {
		if entry.ID == "jmeter-udv" {
			if !strings.Contains(entry.Notes, "sampler parameters") || !strings.Contains(entry.Notes, "not expanded") {
				t.Fatalf("jmeter-udv note claims parameters are expanded: %q", entry.Notes)
			}
			return
		}
	}
	t.Fatal("missing jmeter-udv support entry")
}
