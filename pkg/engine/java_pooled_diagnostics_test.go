package engine

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"testing"
)

func TestJavaHostEvidenceDeduplicatesAndDetectsGaps(t *testing.T) {
	snapshot := javarunner.HostDiagnostics{GCPauses: &javarunner.GCPauseSnapshot{Status: "available", Events: []javarunner.GCPauseEvent{{Sequence: 1, StartUnixNano: 100, DurationNano: 2}}}, Stdout: javarunner.DiagnosticCapture{Bytes: []byte("secret")}}
	h := &javaHostEvidenceCollector{remaining: 2, evidence: JavaHostEvidence{HostID: "host", SlotIDs: []string{"a", "b"}, UnflushedTail: true}, read: func(context.Context, string) (javarunner.HostDiagnostics, error) { return snapshot, nil }}
	h.collect(context.Background())
	h.collect(context.Background())
	if h.evidence.GCPauseCount != 1 || h.evidence.OutputBytes[0] != 6 || h.evidence.Observations != 2 {
		t.Fatal(h.evidence)
	}
	snapshot.GCPauses.Evicted = 3
	snapshot.GCPauses.Events = nil
	for i := uint64(4); i < 132; i++ {
		snapshot.GCPauses.Events = append(snapshot.GCPauses.Events, javarunner.GCPauseEvent{Sequence: i, StartUnixNano: 100})
	}
	h.collect(context.Background())
	if h.evidence.GCPauseCount != 129 || h.evidence.GCMissing != 2 || len(h.evidence.GCTail) != 128 {
		t.Fatal(h.evidence)
	}
	e := &Engine{pooledPilot: &javaPooledPilot{diagnostics: []*javaHostEvidenceCollector{h}}}
	detached := e.JavaPooledDiagnostics()
	detached[0].SlotIDs[0] = "changed"
	detached[0].GCTail[0].Sequence = 999
	if h.evidence.SlotIDs[0] != "a" || h.evidence.GCTail[0].Sequence != 4 {
		t.Fatal("snapshot aliases mutable evidence")
	}
	snapshot.GCPauses = &javarunner.GCPauseSnapshot{Status: "available"}
	h.collect(context.Background())
	if h.evidence.ReadFailures != 1 || h.evidence.GCStatus != "failed" {
		t.Fatal("host restart was hidden")
	}
	h.evidence.ReadFailures = 0
	h.read = func(context.Context, string) (javarunner.HostDiagnostics, error) {
		return javarunner.HostDiagnostics{}, errors.New("private")
	}
	h.collect(context.Background())
	if h.evidence.ReadFailures != 1 {
		t.Fatal("missing read failure")
	}
	h.remaining = 0
	h.collect(context.Background())
	if h.evidence.ReadFailures != 1 {
		t.Fatal("reaped host queried")
	}
}

func TestPilotAutomaticallyCollectsBeforeFinalClose(t *testing.T) {
	peer, cfg, options := newPilotEngineFixture(t)
	options.CollectDiagnostics = true
	e, err := NewJavaPooledPilot(cfg, options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	records := e.JavaPooledDiagnostics()
	if len(records) != 1 || len(records[0].SlotIDs) != cfg.VUsers || records[0].Observations == 0 || records[0].ReadFailures != 0 {
		t.Fatal(records)
	}
	peer.mu.Lock()
	defer peer.mu.Unlock()
	closed := 0
	snapshotSinceClose := false
	for _, q := range peer.calls {
		if q.Operation == "diagnostics" {
			snapshotSinceClose = true
		}
		if q.Operation == "close_slot" {
			closed++
			if !snapshotSinceClose {
				t.Fatal("slot close lacked final diagnostic snapshot", peer.calls)
			}
			snapshotSinceClose = false
		}
		if q.Operation == "diagnostics" && closed == cfg.VUsers {
			t.Fatal("queried reaped host")
		}
	}
	if closed != cfg.VUsers {
		t.Fatal("missing closures")
	}
}
