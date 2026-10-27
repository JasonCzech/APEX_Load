package recordbridge

import (
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	"github.com/jasonczech/apexload/pkg/recorder"
)

func recordingBaselineDigestSession() *recorder.Session {
	return &recorder.Session{
		ID:      "digest-session",
		BaseURL: "https://shop.example.test",
		Requests: []recorder.RecordedRequest{{
			Seq:          1,
			Method:       "POST",
			URL:          "https://shop.example.test/login",
			Path:         "/login",
			Body:         `{"user":"alice"}`,
			ResponseBody: `{"token":"recorded-token"}`,
		}},
	}
}

func TestRecordingBaselineSnapshotDigestWithBaseline(t *testing.T) {
	session := recordingBaselineDigestSession()
	success := true
	baseline := importreview.NewRecordingBaseline([]correlate.Observation{{
		ID:              0,
		RequestURL:      "https://shop.example.test/login",
		RequestBody:     `{"user":"alice"}`,
		RequestHeaders:  map[string]string{"Content-Type": "application/json", "X-B": "2"},
		ResponseBody:    `{"token":"recorded-token"}`,
		ResponseHeaders: map[string]string{"Set-Cookie": "sid=1"},
		ResponseStatus:  200,
		ResponseSuccess: &success,
	}})

	withBaseline, err := RecordingBaselineSnapshotDigestWithBaseline(session, baseline)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("nil baseline is the default-options digest", func(t *testing.T) {
		defaultDigest, err := RecordingBaselineSnapshotDigest(session)
		if err != nil {
			t.Fatal(err)
		}
		got, err := RecordingBaselineSnapshotDigestWithBaseline(session, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != defaultDigest {
			t.Fatalf("nil baseline digest = %s, want default %s", got, defaultDigest)
		}
		if withBaseline == defaultDigest {
			t.Fatal("an attested baseline must bind a different digest than the default snapshot")
		}
	})

	t.Run("protected candidate JSON round trip keeps the digest", func(t *testing.T) {
		// A protected draft stores the baseline inside a JSON envelope and reads
		// it back as map[string]any before decoding it.
		payload, err := json.Marshal(map[string]any{"recording_baseline": baseline})
		if err != nil {
			t.Fatal(err)
		}
		var envelope map[string]any
		if err := json.Unmarshal(payload, &envelope); err != nil {
			t.Fatal(err)
		}
		reencoded, err := json.Marshal(envelope["recording_baseline"])
		if err != nil {
			t.Fatal(err)
		}
		var decoded importreview.RecordingBaseline
		if err := json.Unmarshal(reencoded, &decoded); err != nil {
			t.Fatal(err)
		}
		got, err := RecordingBaselineSnapshotDigestWithBaseline(session, &decoded)
		if err != nil {
			t.Fatal(err)
		}
		if got != withBaseline {
			t.Fatalf("round-tripped digest = %s, want %s", got, withBaseline)
		}
	})

	t.Run("request count and session changes alter the digest", func(t *testing.T) {
		recounted := baseline.Clone()
		recounted.RequestCount++
		got, err := RecordingBaselineSnapshotDigestWithBaseline(session, recounted)
		if err != nil {
			t.Fatal(err)
		}
		if got == withBaseline {
			t.Fatal("a different original request count must change the digest")
		}
		grown := recordingBaselineDigestSession()
		grown.Requests = append(grown.Requests, recorder.RecordedRequest{Seq: 2, Method: "GET", URL: "https://shop.example.test/cart", Path: "/cart"})
		got, err = RecordingBaselineSnapshotDigestWithBaseline(grown, baseline)
		if err != nil {
			t.Fatal(err)
		}
		if got == withBaseline {
			t.Fatal("a recording that changed after preview must change the digest")
		}
	})
}
