package engine

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"strconv"
	"testing"
	"time"
)

func TestSourceTimestampMatchesEpochDigitContract(t *testing.T) {
	now := time.Date(2026, 10, 1, 15, 0, 0, 123456000, time.UTC)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"default", nil, strconv.FormatInt(now.UnixMilli(), 10)},
		{"seconds", []string{"DIGITS=10"}, strconv.FormatInt(now.Unix(), 10)},
		{"microseconds", []string{"DIGITS=16"}, strconv.FormatInt(now.UnixMicro(), 10)},
		{"short", []string{"DIGITS=1"}, "1"},
		{"invalid", []string{"DIGITS=17"}, strconv.FormatInt(now.UnixMilli(), 10)},
		{"malformed", []string{"DIGITS=wat"}, strconv.FormatInt(now.UnixMilli(), 10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceTimestampValue(now, tc.args); got != tc.want {
				t.Fatalf("timestamp=%q want=%q", got, tc.want)
			}
		})
	}
	vu := &VUser{}
	vu.handleSourceParameterAction("lr_save_timestamp", dsl.Action{})
	if len(vu.Variables["timestamp"]) != 13 {
		t.Fatalf("default fallback=%q", vu.Variables["timestamp"])
	}
}

func TestSourceSaveStringRandomArrayReadsIndexedValues(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"ids_count": "3", "ids_1": "one", "ids_2": "two", "ids_3": "three", "ids": "wrong-base-value"}}
	action := dsl.Action{Type: "source_parameter", Params: map[string]any{
		"args":         []any{"{{var.ids}}", "Picked"},
		"source_array": map[string]any{"name": "IDs", "selection": "random"},
	}}
	for i := 0; i < 64; i++ {
		vu.handleSourceParameterAction("lr_save_string", action)
		value := vu.Variables["picked"]
		if value != "one" && value != "two" && value != "three" {
			t.Fatalf("random array value=%q", value)
		}
	}
	for _, count := range []string{"0", "-1", "invalid", "92233720368547758080"} {
		vu.Variables["ids_count"] = count
		vu.handleSourceParameterAction("lr_save_string", action)
		if value := vu.Variables["picked"]; value != "" {
			t.Fatalf("empty/malformed array count %q produced %q", count, value)
		}
	}
}

func TestSourceSaveStringRandomArrayKeepsOpaqueValueAndRunEvidence(t *testing.T) {
	const selected = "{{var.other}}"
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			vu := runShapedVUser(t, setRunEvidenceRedaction(importedCredentialVariableScript("picked"), mode.redact))
			vu.Variables = map[string]string{"ids_count": "1", "ids_1": selected, "other": "changed"}
			vu.handleSourceParameterAction("lr_save_string", dsl.Action{Type: "source_parameter", Params: map[string]any{
				"args":         []any{"ignored", "Picked"},
				"source_array": map[string]any{"name": "ids", "selection": "random"},
			}})
			if vu.Variables["Picked"] != selected || vu.Variables["picked"] != selected {
				t.Fatalf("selected runtime value was interpolated again: %q, %q", vu.Variables["Picked"], vu.Variables["picked"])
			}
			if _, registered := allRuntimeSecretValues(vu.RedactionConfig)[selected]; registered != mode.redact {
				t.Fatalf("selected value run-evidence registration = %v, want %v", registered, mode.redact)
			}
			// Authored template arguments still expand once when no array selection
			// is present; their substituted runtime bytes remain opaque too.
			vu.handleSourceParameterAction("lr_save_string", dsl.Action{Type: "source_parameter", Params: map[string]any{
				"args": []any{"{{var.ids_1}}", "ordinary"},
			}})
			if vu.Variables["ordinary"] != selected {
				t.Fatalf("ordinary template assignment = %q, want %q", vu.Variables["ordinary"], selected)
			}
		})
	}
}

func TestSourceTimeParameterPreservesLiteralFractionOnConfiguredClock(t *testing.T) {
	for _, tc := range []struct{ zone, want string }{{"", "2026-10-01T08:04:05.000Z"}, {"UTC", "2026-10-01T12:04:05.000Z"}} {
		r := NewParameterRuntime([]dsl.Parameter{{Name: "clock", Type: "time", Update: "each_occurrence", Format: "%Y-%m-%dT%H:%M:%S.000Z", FormatDialect: "loadrunner_strftime", Timezone: tc.zone}}, nil, 0)
		current := time.Date(2026, 10, 1, 8, 4, 5, 123456000, time.FixedZone("EDT", -4*3600))
		r.now = func() time.Time { return current }
		got, ok := r.ResolveNamed("clock")
		if !ok || got != tc.want {
			t.Fatalf("zone %q got=%q,%v want=%q", tc.zone, got, ok, tc.want)
		}
		current = current.Add(time.Second)
		next, ok := r.ResolveNamed("clock")
		if !ok || next == got {
			t.Fatalf("each occurrence did not update: %q", next)
		}
	}
}
