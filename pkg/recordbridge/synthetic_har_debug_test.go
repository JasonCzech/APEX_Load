package recordbridge

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestReplaySyntheticHARDeduplicatesIdentitiesAndPreservesRepeatedRequests(t *testing.T) {
	now := time.Now().UTC()
	entry := protocol.NetworkEntry{URL: "https://example.test/same", Method: "GET", StatusCode: 200,
		BrowserEvidence: &protocol.BrowserResourceEvidence{SourceGeneration: "generation", RequestID: "request-1"}}
	completed := entry
	completed.StatusCode = 503
	other := entry
	identity := *entry.BrowserEvidence
	identity.RequestID = "request-2"
	other.BrowserEvidence = &identity
	events := []engine.ReplayActionEvent{
		{StartedAt: now, Network: []protocol.NetworkEntry{entry}},
		{StartedAt: now, Snapshot: &protocol.ErrorSnapshot{NetworkEntries: []protocol.NetworkEntry{completed, other}}},
		{StartedAt: now, Network: []protocol.NetworkEntry{{URL: "https://example.test/same", Method: "GET"}, {URL: "https://example.test/same", Method: "GET"}, {URL: "https://example.test/cached", Method: "GET", ResourceType: "image(cache)"}}},
	}
	var out bytes.Buffer
	summary, err := WriteReplaySyntheticHAR(events, &out)
	if err != nil || summary.Entries != 4 {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}
	var har struct {
		Log struct {
			Entries []struct {
				Response struct {
					Status int `json:"status"`
				} `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if err := json.Unmarshal(out.Bytes(), &har); err != nil {
		t.Fatal(err)
	}
	if summary.Omitted["cache_hit"] != 1 {
		t.Fatal("cached resource was exported as a dispatch")
	}
	if har.Log.Entries[0].Response.Status != 503 {
		t.Fatal("export kept an earlier request observation")
	}
}

func TestReplayHARFiltersFollowedBrowserRedirectsWithoutLosingFinalRedirect(t *testing.T) {
	now := time.Now()
	a := protocol.NetworkEntry{URL: "https://example.test/a", Method: "GET", StatusCode: 307, BrowserEvidence: &protocol.BrowserResourceEvidence{SourceGeneration: "g", RequestID: "a"}}
	b := protocol.NetworkEntry{URL: "https://example.test/b", Method: "GET", StatusCode: 302, BrowserEvidence: &protocol.BrowserResourceEvidence{SourceGeneration: "g", RequestID: "b", RedirectFromRequestID: "a", RedirectHop: 1}}
	for _, keep := range []bool{false, true} {
		var out bytes.Buffer
		summary, err := WriteReplaySyntheticHARWithOptions([]engine.ReplayActionEvent{{StartedAt: now, Network: []protocol.NetworkEntry{a, b}}}, &out, &protocol.ReplayHAROptions{Redirects: keep})
		want := int64(1)
		if keep {
			want = 2
		}
		if err != nil || summary.Entries != want {
			t.Fatalf("%+v %v", summary, err)
		}
	}
}

func TestReplaySyntheticHARScrubsSOAPJSONAndFormCredentials(t *testing.T) {
	const (
		soapPwd   = "synth-har-soap-pwd-9851"
		accPwd    = "synth-har-acc-pwd-9852"
		bearerTok = "synth-har-bearer-9853"
		queryTok  = "synth-har-query-9854"
	)
	now := time.Now().UTC()
	entry := protocol.NetworkEntry{
		URL:            "https://api.example.test/soap?access_token=" + queryTok,
		Method:         "POST",
		StatusCode:     200,
		RequestHeaders: map[string]string{"Authorization": "Bearer " + bearerTok},
		HAR: &protocol.ReplayHARExchange{
			StartedAt: now,
			RequestBody: &protocol.ReplayHARBody{
				State:    "retained",
				MimeType: "text/xml",
				Text:     `<ns:Login xmlns:ns="urn:test"><ns:userPassword>` + soapPwd + `</ns:userPassword></ns:Login>`,
			},
			ResponseBody: &protocol.ReplayHARBody{
				State:    "retained",
				MimeType: "application/json",
				Text:     `{"accPassword":"` + accPwd + `","result":"ok"}`,
			},
		},
	}
	var out bytes.Buffer
	summary, err := WriteReplaySyntheticHARWithOptions(
		[]engine.ReplayActionEvent{{StartedAt: now, Network: []protocol.NetworkEntry{entry}}},
		&out,
		&protocol.ReplayHAROptions{Bodies: "both"},
	)
	if err != nil || summary.Entries != 1 {
		t.Fatalf("WriteReplaySyntheticHARWithOptions = %+v, %v", summary, err)
	}
	for _, secret := range []string{soapPwd, accPwd, bearerTok, queryTok} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("synthetic HAR leaked %q: %s", secret, out.String())
		}
	}
	if !strings.Contains(out.String(), `\"result\":\"ok\"`) {
		t.Fatalf("synthetic HAR dropped benign JSON response field: %s", out.String())
	}
}
