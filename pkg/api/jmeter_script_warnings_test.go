package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/jmeter"
)

// Each script's replay preflight lists only the warnings the converter
// attributed to it: an Event Hubs group's preflight does not carry the Kafka
// group's "Counter ... is retained but unused" lines, while the response's
// warnings stay the plan-wide list.
func TestJMeterReplayPreflightUsesPerScriptWarnings(t *testing.T) {
	source, err := os.ReadFile("../convert/jmeter/testdata/messaging_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	result, err := jmeter.ConvertJMX(source, jmeter.ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	planCounters := 0
	for _, warning := range result.Warnings {
		if strings.HasPrefix(warning, "Counter ") {
			planCounters++
		}
	}
	if planCounters == 0 {
		t.Fatal("fixture no longer has a counter warning to attribute")
	}
	w := httptest.NewRecorder()
	NewServer(ServerConfig{}).writeJMeterImportResponse(w, httptest.NewRequest("POST", "/", nil), 200, result, &jmeterImportRequest{Data: source})
	if w.Code != 200 {
		t.Fatalf("import %d: %s", w.Code, w.Body.String())
	}
	kafkaCounters, eventHubsScripts := 0, 0
	for _, script := range result.Scripts {
		preflight := preservedReplayPreflight(script)
		if preflight == nil {
			t.Fatalf("script %q has no replay preflight", script.Name)
		}
		counters := 0
		for _, warning := range preflight.Warnings {
			if strings.HasPrefix(warning, "Counter ") {
				counters++
			}
		}
		switch script.Protocol {
		case "eventhubs":
			eventHubsScripts++
			if counters > 0 {
				t.Errorf("Event Hubs script %q preflight lists another group's counter warnings: %q", script.Name, preflight.Warnings)
			}
		case "kafka":
			kafkaCounters += counters
		}
	}
	if eventHubsScripts == 0 {
		t.Fatal("fixture has no Event Hubs script")
	}
	if kafkaCounters == 0 {
		t.Error("no Kafka script preflight kept its own counter warnings")
	}
	planWide := 0
	for _, warning := range result.Warnings {
		if strings.HasPrefix(warning, "Counter ") {
			planWide++
		}
	}
	if planWide != planCounters {
		t.Errorf("plan-wide warnings lost counter lines: %d, want %d", planWide, planCounters)
	}
}

// A messaging import goes through batch repairs (confirm_reference_context is
// required for every messaging plan), and each repair rebuilds every script's
// replay preflight. The per-script converter warnings must survive that, and
// the repair response must keep the plan-wide warnings.
func TestJMeterBatchRepairKeepsPerScriptWarnings(t *testing.T) {
	server, original, _ := batchTestPreview(t)
	id := decodeBatchIdentity(t, original["batch"])
	req := ImportRepairRequest{BatchID: id.ID, Revision: id.Revision, ProjectID: "p", Operations: []JMeterBatchOperation{{Type: "confirm_reference_context", Timezone: "UTC", ConfirmNoOverrides: true}}}
	call := func(apply bool) map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		server.processImportRepair(w, httptest.NewRequest("POST", "/", nil), apply, req)
		if w.Code != 200 {
			t.Fatalf("repair (apply=%v) %d: %s", apply, w.Code, w.Body.String())
		}
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	preview := call(false)
	req.PreviewDigest = preview["preview_digest"].(string)
	applied := call(true)

	countCounters := func(warnings []string) int {
		n := 0
		for _, warning := range warnings {
			if strings.HasPrefix(warning, "Counter ") {
				n++
			}
		}
		return n
	}
	var responseWarnings []string
	raw, _ := json.Marshal(applied["warnings"])
	_ = json.Unmarshal(raw, &responseWarnings)
	if countCounters(responseWarnings) == 0 {
		t.Fatalf("repair response dropped the plan-wide converter warnings: %q", responseWarnings)
	}

	next := decodeBatchIdentity(t, applied["batch"])
	b, err := server.openJMeterBatch(httptest.NewRequest("POST", "/", nil), next, "p")
	if err != nil {
		t.Fatal(err)
	}
	kafkaCounters, eventHubsScripts := 0, 0
	for _, script := range b.Scripts {
		preflight := preservedReplayPreflight(script)
		if preflight == nil {
			t.Fatalf("repaired script %q has no replay preflight", script.Name)
		}
		counters := countCounters(preflight.Warnings)
		switch script.Protocol {
		case "eventhubs":
			eventHubsScripts++
			if counters > 0 {
				t.Errorf("repaired Event Hubs script %q preflight lists another group's counter warnings: %q", script.Name, preflight.Warnings)
			}
		case "kafka":
			kafkaCounters += counters
		}
	}
	if eventHubsScripts == 0 {
		t.Fatal("fixture has no Event Hubs script")
	}
	if kafkaCounters == 0 {
		t.Error("repaired Kafka scripts lost their own counter warnings")
	}
}
