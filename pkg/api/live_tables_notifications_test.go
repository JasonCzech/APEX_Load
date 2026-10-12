package api

import (
	"encoding/json"
	"testing"
	"time"
)

func TestLiveTableNotificationsAreScopedBoundedAndValueFree(t *testing.T) {
	hub := NewHub()
	defer hub.Stop()
	runner := &TestRunner{hub: hub}
	run := &runLiveTables{testID: "run-a", projectID: "project-a"}
	now := time.Now()
	for i := 0; i < 100; i++ {
		runner.notifyLiveTableChange(run, now.Add(time.Duration(i)*time.Millisecond))
	}
	runner.notifyLiveTableChange(run, now.Add(time.Second))
	for sequence := 1; sequence <= 2; sequence++ {
		select {
		case message := <-hub.broadcast:
			if message.testID != "run-a" || message.projectID != "project-a" || message.broadcastAll {
				t.Fatalf("notification lost its access scope: %+v", message)
			}
			var payload map[string]any
			if err := json.Unmarshal(message.payload, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload) != 4 || payload["type"] != "live_tables_changed" || payload["event_index"] != float64(sequence) || payload["test_id"] != "run-a" || payload["timestamp"] == nil {
				t.Fatalf("unexpected notification disclosure: %s", message.payload)
			}
		default:
			t.Fatal("missing notification")
		}
	}
	select {
	case <-hub.broadcast:
		t.Fatal("unbounded per-operation notification fanout")
	default:
	}
}
