package harsynth

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// An entry whose request side the engine marked AuthoredRequestRetained (the
// script left config.log.redact_run_evidence off) keeps its authored request in
// the synthetic HAR; the response side and unmarked entries are scrubbed as before.
func TestSyntheticHARKeepsEngineMarkedAuthoredRequest(t *testing.T) {
	const (
		literal  = "lit-har-secret-8201"
		response = "resp-har-secret-8202"
	)
	for _, marked := range []bool{true, false} {
		var out bytes.Buffer
		w := NewWriter(&out, Metadata{HAROptions: &protocol.ReplayHAROptions{Bodies: "both"}})
		h := protocol.NewReplayHARExchange()
		h.RequestBody = &protocol.ReplayHARBody{State: "retained", MimeType: "application/json", Text: `{"client_secret":"` + literal + `"}`}
		h.ResponseBody = &protocol.ReplayHARBody{State: "retained", MimeType: "application/json", Text: `{"client_secret":"` + response + `"}`}
		if err := w.Add(Entry{Network: protocol.NetworkEntry{
			AuthoredRequestRetained: marked,
			URL:                     "https://api.example.test/login?client_secret=" + literal,
			Method:                  "POST",
			RequestHeaders:          map[string]string{"Authorization": "Bearer " + literal},
			ResponseHeaders:         map[string]string{"Set-Cookie": "sid=" + response},
			HAR:                     h,
		}}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Close(); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		if strings.Contains(text, response) {
			t.Fatalf("marked=%v: response value kept: %s", marked, text)
		}
		for _, want := range []string{`"url":"https://api.example.test/login?client_secret=` + literal, `"value":"Bearer ` + literal} {
			if strings.Contains(text, want) != marked {
				t.Fatalf("marked=%v: %q present=%v: %s", marked, want, !marked, text)
			}
		}
	}
}
