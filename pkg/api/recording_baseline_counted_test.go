package api

import (
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/storage"
)

// A preview attests only the prefix persistence keeps, plus the recording's
// original request count. Saving that attested baseline must record the
// original count, including the fully truncated case where no observation fit.
func TestPersistRecordedBaselineCountedKeepsOriginalRequestCount(t *testing.T) {
	t.Setenv(recordingBaselineKeyEnvVar, "recording-test-key")
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := &Server{store: store}

	t.Run("bounded prefix", func(t *testing.T) {
		parent := saveRecordingParent(t, store, "script-counted", recordingDigestTestScript())
		observations := []correlate.Observation{{ID: 0, ResponseBody: `{"token":"a-value-123456"}`}, {ID: 1}}
		if err := server.persistRecordedBaselineCounted("script-counted", "proxy_recording", parent, observations, 640); err != nil {
			t.Fatal(err)
		}
		record, err := store.GetScriptRecording("script-counted")
		if err != nil {
			t.Fatal(err)
		}
		if record == nil || record.RequestCount != 640 || len(record.Observations) != 2 {
			t.Fatalf("record = %#v, want RequestCount 640 with 2 retained observations", record)
		}
	})

	t.Run("fully truncated witness", func(t *testing.T) {
		parent := saveRecordingParent(t, store, "script-truncated", recordingDigestTestScript())
		if err := server.persistRecordedBaselineCounted("script-truncated", "proxy_recording", parent, nil, 3); err != nil {
			t.Fatal(err)
		}
		record, err := store.GetScriptRecording("script-truncated")
		if err != nil {
			t.Fatal(err)
		}
		if record == nil || record.RequestCount != 3 || len(record.Observations) != 0 || record.EncryptedCorrelationObservations != "" {
			t.Fatalf("record = %#v, want an empty witness row with RequestCount 3", record)
		}
	})

	t.Run("nothing recorded persists nothing", func(t *testing.T) {
		parent := saveRecordingParent(t, store, "script-empty", recordingDigestTestScript())
		if err := server.persistRecordedBaselineCounted("script-empty", "proxy_recording", parent, nil, 0); err != nil {
			t.Fatal(err)
		}
		record, err := store.GetScriptRecording("script-empty")
		if err != nil {
			t.Fatal(err)
		}
		if record != nil {
			t.Fatalf("record = %#v, want none for an empty recording", record)
		}
	})

	t.Run("count below retained length is corrected", func(t *testing.T) {
		parent := saveRecordingParent(t, store, "script-undercount", recordingDigestTestScript())
		observations := []correlate.Observation{{ID: 0}, {ID: 1}}
		if err := server.persistRecordedBaselineCounted("script-undercount", "proxy_recording", parent, observations, 1); err != nil {
			t.Fatal(err)
		}
		record, err := store.GetScriptRecording("script-undercount")
		if err != nil {
			t.Fatal(err)
		}
		if record == nil || record.RequestCount != 2 {
			t.Fatalf("record = %#v, want RequestCount 2", record)
		}
	})
}

func TestAttestedRecordingBaselineCount(t *testing.T) {
	var missing *attestedRecordingBaseline
	if missing.count() != 0 {
		t.Fatal("nil attested baseline must count zero")
	}
	implicit := &attestedRecordingBaseline{observations: []correlate.Observation{{ID: 0}, {ID: 1}}}
	if implicit.count() != 2 {
		t.Fatalf("implicit count = %d, want 2", implicit.count())
	}
	explicit := &attestedRecordingBaseline{observations: []correlate.Observation{{ID: 0}}, requestCount: 9}
	if explicit.count() != 9 {
		t.Fatalf("explicit count = %d, want 9", explicit.count())
	}
}
