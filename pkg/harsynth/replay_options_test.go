package harsynth

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestReplayHARBodyOptionsAndRedaction(t *testing.T) {
	for _, bodies := range []string{"none", "request", "response", "both"} {
		t.Run(bodies, func(t *testing.T) {
			o := &protocol.ReplayHAROptions{Bodies: bodies, Timings: true}
			var out bytes.Buffer
			w := NewWriter(&out, Metadata{HAROptions: o})
			h := protocol.NewReplayHARExchange()
			h.RequestBody = &protocol.ReplayHARBody{Text: `{"password":"private-password","value":"request-visible"}`, State: "retained", MimeType: "application/json"}
			h.ResponseBody = &protocol.ReplayHARBody{Text: `{"access_token":"private-token","value":"response-visible"}`, State: "retained", MimeType: "application/json"}
			err := w.Add(Entry{RecordTime: time.Now(), Network: protocol.NetworkEntry{URL: "https://example.test/", Method: "POST", HAR: h}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Close(); err != nil {
				t.Fatal(err)
			}
			validateHAR(t, out.Bytes())
			raw := out.String()
			if strings.Contains(raw, "private-password") || strings.Contains(raw, "private-token") {
				t.Fatal("credentials leaked")
			}
			if strings.Contains(raw, "request-visible") != o.KeepBody(true) || strings.Contains(raw, "response-visible") != o.KeepBody(false) {
				t.Fatalf("body selection %s", raw)
			}
			var parsed any
			if json.Unmarshal(out.Bytes(), &parsed) != nil {
				t.Fatal("invalid HAR")
			}
		})
	}
}
func TestReplayHARUnavailableBodyIsExplicit(t *testing.T) {
	var out bytes.Buffer
	w := NewWriter(&out, Metadata{HAROptions: &protocol.ReplayHAROptions{Bodies: "response"}})
	h := protocol.NewReplayHARExchange()
	h.ResponseBody = &protocol.ReplayHARBody{Text: "must-not-leak", State: "total_limit"}
	_ = w.Add(Entry{Network: protocol.NetworkEntry{URL: "https://example.test/", Method: "GET", HAR: h}})
	_, _ = w.Close()
	if strings.Contains(out.String(), "must-not-leak") || !strings.Contains(out.String(), `"response_body_state":"total_limit"`) {
		t.Fatal(out.String())
	}
}

func TestReplayHARContentSizeIsObservedBeforeRedaction(t *testing.T) {
	c, _ := protocol.NewReplayHARCapture(protocol.ReplayHAROptions{Bodies: "response"})
	original := []byte(`{"password":"x"}`)
	h := protocol.NewReplayHARExchange()
	h.ResponseBody = c.Body(original, "application/json", true)
	h.ResponseBody.Text = `{"password":"[REDACTED]"}`
	var out bytes.Buffer
	w := NewWriter(&out, Metadata{HAROptions: &c.Options})
	_ = w.Add(Entry{Network: protocol.NetworkEntry{URL: "https://example.test/", Method: "GET", HAR: h}})
	_, _ = w.Close()
	var doc struct {
		Log struct {
			Entries []struct {
				Response struct {
					Content struct {
						Size int64 `json:"size"`
					} `json:"content"`
				} `json:"response"`
			} `json:"entries"`
		} `json:"log"`
	}
	if json.Unmarshal(out.Bytes(), &doc) != nil || doc.Log.Entries[0].Response.Content.Size != int64(len(original)) {
		t.Fatal(out.String())
	}
}

func TestReplayHARWaitIncludesSendOnlyWhenSendIsNotMeasured(t *testing.T) {
	for _, measuredSend := range []bool{false, true} {
		start, end := 0.0, 2.0
		h := protocol.NewReplayHARExchange()
		h.Phases = []protocol.BrowserResourcePhase{{Name: "request_wait", StartMS: &start, EndMS: &end}}
		if measuredSend {
			h.Phases = append(h.Phases, protocol.BrowserResourcePhase{Name: "send", StartMS: &start, EndMS: &end})
		}
		entry, reason := buildEntry(Entry{Options: &protocol.ReplayHAROptions{Timings: true}, Network: protocol.NetworkEntry{URL: "https://example.test/", Method: "GET", HAR: h}}, scrub.Policy{})
		if reason != "" || entry.Evidence.WaitIncludesSend == measuredSend {
			t.Fatalf("measured send=%v: evidence=%+v reason=%s", measuredSend, entry.Evidence, reason)
		}
	}
}
