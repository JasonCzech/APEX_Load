package correlate

import (
	"errors"
	"strings"
	"testing"
)

// sessionBridgeObservations models a Salesforce login: a hand-off page whose
// hidden sid is posted to the next host, followed by framework JavaScript
// large enough to exceed the aggregate evidence budget on its own.
func sessionBridgeObservations(scriptBodies ...int) []Observation {
	const sid = "00Dir0000003XDx!AQEAQEVMgkvH1JYexampleSessionValue.r4nd0m"
	observations := []Observation{{
		ID:              1,
		RequestURL:      "https://example.my.salesforce.com/secur/frontdoor.jsp",
		ResponseHeaders: map[string]string{"Content-Type": "text/html; charset=UTF-8"},
		ResponseBody:    `<form action="https://example.file.force.com/secur/contentDoor" method="POST"><input type="hidden" name="sid" id="sid" value="` + sid + `" /></form>`,
	}}
	for index, size := range scriptBodies {
		observations = append(observations, Observation{
			ID:              index + 2,
			RequestURL:      "https://static.example.com/framework.js",
			ResponseHeaders: map[string]string{"content-type": "application/x-javascript"},
			ResponseBody:    strings.Repeat("a", size),
		})
	}
	return append(observations, Observation{
		ID:          len(scriptBodies) + 2,
		RequestURL:  "https://example.file.force.com/secur/contentDoor",
		RequestBody: "sid=" + sid + "&skipRedirect=1",
	})
}

func TestFitStaticAssetEvidenceKeepsCaptureThatFits(t *testing.T) {
	observations := sessionBridgeObservations(1 << 20)
	fitted, omission := FitStaticAssetEvidence(observations)
	if omission.Responses != 0 || omission.Note() != "" || &fitted[0] != &observations[0] {
		t.Fatalf("a capture within budget changed: %+v", omission)
	}
}

func TestFitStaticAssetEvidenceDropsLargestStaticBodiesUntilItFits(t *testing.T) {
	observations := sessionBridgeObservations(15<<20, 15<<20, 15<<20, 15<<20, 15<<20, 2<<20)
	if _, _, err := DetectCheckedWithWarnings(observations); !errors.Is(err, ErrCorrelationWorkLimit) {
		t.Fatalf("precondition: unfitted capture should exceed the budget, got %v", err)
	}
	fitted, omission := FitStaticAssetEvidence(observations)
	// 77 MiB of scripts against a 64 MiB budget: one 15 MiB script is enough,
	// and the small one is never chosen first.
	if omission.Responses != 1 || omission.Bytes != 15<<20 {
		t.Fatalf("omission = %+v, want one 15 MiB script", omission)
	}
	if fitted[6].ResponseBody == "" || observations[1].ResponseBody == "" {
		t.Fatal("dropped the small script, or modified the caller's observations")
	}
	if !strings.Contains(omission.Note(), "1 static asset response(s) (15 MiB") {
		t.Fatalf("note = %q", omission.Note())
	}
	correlations, warnings, err := DetectCheckedWithWarnings(fitted)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("fitted capture: err=%v warnings=%v", err, warnings)
	}
	if len(correlations) != 1 || correlations[0].Name != "corr_sid" {
		t.Fatalf("correlations = %#v, want the hidden sid", correlations)
	}
}

func TestFitStaticAssetEvidenceDropsOversizedStaticResponse(t *testing.T) {
	observations := sessionBridgeObservations(maxCorrelationEvidenceBytes + 1)
	fitted, omission := FitStaticAssetEvidence(observations)
	if omission.Responses != 1 || fitted[1].ResponseBody != "" {
		t.Fatalf("an over-cap script must be dropped: %+v", omission)
	}
	if _, _, err := DetectCheckedWithWarnings(fitted); err != nil {
		t.Fatalf("fitted capture: %v", err)
	}
}

func TestFitStaticAssetEvidenceNeverDropsApplicationResponses(t *testing.T) {
	observations := sessionBridgeObservations()
	for index := 0; index < 5; index++ {
		observations = append(observations, Observation{
			ID:              100 + index,
			ResponseHeaders: map[string]string{"Content-Type": "application/json"},
			ResponseBody:    strings.Repeat("b", 15<<20),
		}, Observation{
			ID:           200 + index,
			ResponseBody: strings.Repeat("c", 1<<20), // no Content-Type: not static
		})
	}
	fitted, omission := FitStaticAssetEvidence(observations)
	if omission.Responses != 0 || &fitted[0] != &observations[0] {
		t.Fatalf("application responses were dropped: %+v", omission)
	}
	if _, _, err := DetectCheckedWithWarnings(fitted); !errors.Is(err, ErrCorrelationWorkLimit) {
		t.Fatalf("an over-budget capture of application data must still report the limit, got %v", err)
	}
}

func TestStaticAssetResponseClassification(t *testing.T) {
	for contentType, want := range map[string]bool{
		"text/javascript; charset=utf-8": true,
		"application/x-javascript":       true,
		"text/css":                       true,
		"font/woff2":                     true,
		"application/x-font-ttf":         true,
		"image/svg+xml":                  true,
		"video/mp4":                      true,
		"application/json":               false,
		"text/html":                      false,
		"text/plain":                     false,
		"":                               false,
	} {
		if got := staticAssetResponse(map[string]string{"Content-Type": contentType}); got != want {
			t.Errorf("staticAssetResponse(%q) = %v, want %v", contentType, got, want)
		}
	}
}

func TestHiddenSessionHandOffFieldsCorrelate(t *testing.T) {
	body := `<form action="https://example.file.force.com/secur/contentDoor" method="POST">` +
		`<input type="hidden" name="lm" id="lm" value="eyJraWQiOiJDT1JFIn0..MXAo4P__wy4u2Wcd.lHuoJ28fEwAJ" />` +
		`<input type="hidden" name="sid" id="sid" value="00Dir0000003XDx!AQEAQEVMgkvH1JYexample" />` +
		`<input type="hidden" name="sessionKey" value="Q2hlY2tTZXNzaW9uS2V5MTIz" />` +
		`<input type="hidden" name="side" value="leftPanelOpen1234" />` +
		`<input type="hidden" name="skipRedirect" id="skipRedirect" value="1" /></form>`
	got := map[string]string{}
	for _, candidate := range htmlInputCandidates(body) {
		got[candidate.Name] = candidate.Reason
	}
	for name, reason := range map[string]string{
		"corr_lm":         "Hidden form field lm (session hand-off state)",
		"corr_sid":        "Hidden form field sid (session id)",
		"corr_sessionkey": "Hidden form field sessionKey (session id)",
	} {
		if got[name] != reason {
			t.Errorf("candidate %s reason = %q, want %q (all: %v)", name, got[name], reason, got)
		}
	}
	if _, found := got["corr_side"]; found {
		t.Error("a field merely containing \"sid\" must not match the exact sid name")
	}
}
