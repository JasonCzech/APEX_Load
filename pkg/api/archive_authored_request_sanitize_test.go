package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// The engine marks the request side it already scrubbed under a script that
// left config.log.redact_run_evidence off (AuthoredRequestRetained). Archive
// sanitizing (write time and every read path), reports and LLM sample exports
// keep those authored values; unmarked (legacy, direct-recorder, redaction-on)
// request sides and every response side keep the full name/shape scrub.
func TestArchiveSanitizerHonoursAuthoredRequestMarker(t *testing.T) {
	const (
		literal  = "lit-archive-secret-8101"
		response = "resp-archive-secret-8102"
		page     = "page-archive-tok-8103"
	)
	body := `{"client_secret":"` + literal + `","userPassword":"` + literal + `"}`
	for _, marked := range []bool{true, false} {
		event := TransactionEvent{
			ActionName: "Login",
			Snapshot: &protocol.ErrorSnapshot{
				AuthoredRequestRetained: marked,
				RequestURL:              "https://api.example.test/login?client_secret=" + literal,
				RequestHeaders:          map[string]string{"Authorization": "Bearer " + literal},
				RequestBody:             body,
				ResponseHeaders:         map[string]string{"Set-Cookie": "sid=" + response},
				ResponseBody:            `{"client_secret":"` + response + `"}`,
				Resources: []protocol.ResourceSnapshot{{
					RequestURL:     "https://cdn.example.test/a.js?access_token=" + page,
					RequestHeaders: map[string]string{"Authorization": "Bearer " + page},
				}},
			},
			NetworkEntries: []protocol.NetworkEntry{
				{
					AuthoredRequestRetained: marked,
					URL:                     "https://api.example.test/login?client_secret=" + literal,
					Method:                  "POST",
					RequestHeaders:          map[string]string{"Authorization": "Bearer " + literal},
					RequestBody:             body,
					ResponseHeaders:         map[string]string{"Set-Cookie": "sid=" + response},
					HAR: &protocol.ReplayHARExchange{
						RequestBody:  &protocol.ReplayHARBody{State: "retained", Text: body},
						ResponseBody: &protocol.ReplayHARBody{State: "retained", Text: `{"client_secret":"` + response + `"}`},
					},
				},
				{URL: "https://cdn.example.test/a.js?access_token=" + page, Method: "GET"},
			},
		}
		sanitizeArchivedTransaction(&event)
		snapshot, entry := event.Snapshot, event.NetworkEntries[0]
		for label, got := range map[string]string{
			"snapshot url": snapshot.RequestURL, "snapshot auth": snapshot.RequestHeaders["Authorization"], "snapshot body": snapshot.RequestBody,
			"entry url": entry.URL, "entry auth": entry.RequestHeaders["Authorization"], "entry body": entry.RequestBody, "entry har body": entry.HAR.RequestBody.Text,
			"llm sample prompt": llmEvalSampleRequestBody(&protocol.ErrorSnapshot{AuthoredRequestRetained: marked, RequestBody: body}),
		} {
			if strings.Contains(got, literal) != marked {
				t.Fatalf("marked=%v %s = %q, want authored visible=%v", marked, label, got, marked)
			}
		}
		for label, got := range map[string]string{
			"snapshot response": snapshot.ResponseBody, "snapshot set-cookie": snapshot.ResponseHeaders["Set-Cookie"],
			"entry set-cookie": entry.ResponseHeaders["Set-Cookie"], "entry har response": entry.HAR.ResponseBody.Text,
			"resource url": snapshot.Resources[0].RequestURL, "resource auth": snapshot.Resources[0].RequestHeaders["Authorization"],
			"page entry url": event.NetworkEntries[1].URL,
		} {
			if strings.Contains(got, response) || strings.Contains(got, page) {
				t.Fatalf("marked=%v %s = %q kept a response/page value", marked, label, got)
			}
		}
		if snapshot.AuthoredRequestRetained != marked || entry.AuthoredRequestRetained != marked {
			t.Fatalf("marker lost: snapshot=%v entry=%v", snapshot.AuthoredRequestRetained, entry.AuthoredRequestRetained)
		}
	}
}
