package dsl

import (
	"strings"
	"testing"
	"time"
)

func TestJMeterMillisecondsUseMinimumWidth(t *testing.T) {
	for _, c := range []struct {
		ms            int
		pattern, want string
	}{{1, "S", "1"}, {12, "S", "12"}, {123, "S", "123"}, {1, "SS", "01"}, {123, "SS", "123"}, {1, "SSS", "001"}, {0, "S", "0"}} {
		got, e := FormatJMeterTime(time.Date(2024, 1, 1, 0, 0, 0, c.ms*1000000, time.UTC), c.pattern)
		if e != nil || got != c.want {
			t.Fatalf("%d %s: %s %v", c.ms, c.pattern, got, e)
		}
	}
}
func TestJMeterDateBoundariesAndLiterals(t *testing.T) {
	zone, e := time.LoadLocation("America/New_York")
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct{ instant, pattern, want string }{
		{"2024-03-10T06:59:59.999Z", "yyyy-MM-dd'T'HH:mm:ss.SXXX", "2024-03-10T01:59:59.999-05:00"},
		{"2024-03-10T07:00:00Z", "yyyy-MM-dd'T'HH:mm:ss.SXXX", "2024-03-10T03:00:00.0-04:00"},
		{"2024-11-03T06:00:00Z", "yyyy-MM-dd'T'HH:mm:ss.SXXX", "2024-11-03T01:00:00.0-05:00"},
		{"2024-01-02T05:00:00Z", "yyyy-MM-dd 'v1' '' HH:mm:ss.S", "2024-01-02 v1 ' 00:00:00.0"},
	} {
		instant, _ := time.Parse(time.RFC3339Nano, c.instant)
		got, e := FormatJMeterTime(instant.In(zone), c.pattern)
		if e != nil || got != c.want {
			t.Fatalf("%s: %q %v", c.instant, got, e)
		}
	}
	for _, p := range []string{"", "EEEE", "SSSS", "YYYY", "'open", strings.Repeat("S", 257)} {
		if _, e := FormatJMeterTime(time.Now(), p); e == nil {
			t.Fatalf("accepted %s", p)
		}
	}
}
func TestJMeterContractsCannotSilentlyExecute(t *testing.T) {
	cases := []Script{
		{Config: ScriptConfig{JMeter: &JMeterCompatibility{Profile: JMeterMessagingProfile, SourcePlanID: "p", SourceGroupID: "g", ContextConfirmed: true}}},
		{Config: ScriptConfig{SamplerSchedules: []SamplerSchedule{{ID: "timer", Segments: []SamplerRateSegment{{StartRPS: 1, EndRPS: 42, DurationSeconds: 300}}}}}},
		{Actions: []Action{{Name: "send", PacingIDs: []string{"missing"}}}},
		{Parameters: []Parameter{{Name: "p", Type: "random_number", RandomAlgorithm: "java48"}}},
	}
	for i := range cases {
		if len(ValidateJMeterCompatibility(&cases[i])) == 0 {
			t.Fatalf("case %d accepted unsupported compatibility", i)
		}
	}
	if issues := ValidateJMeterCompatibility(&Script{}); len(issues) > 0 {
		t.Fatalf("legacy defaults changed: %v", issues)
	}
}

func TestJMeterValidationBoundsAndSourceCounterIdentity(t *testing.T) {
	s := &Script{Parameters: []Parameter{{Name: "counter", Type: "counter", Scope: "vuser", ScopeID: "source-counter"}}}
	if len(ValidateJMeterCompatibility(s)) == 0 {
		t.Fatal("source counter accepted without profile")
	}
	cyclic := &ControlFlow{}
	cyclic.Steps = []Action{{Control: cyclic}}
	if len(ValidateJMeterCompatibility(&Script{Actions: cyclic.Steps})) == 0 {
		t.Fatal("cyclic action tree accepted")
	}
	parallel := &ControlFlow{}
	parallel.Branches = []ParallelBranch{{Name: "cycle", Steps: []Action{{Control: parallel}}}}
	if len(ValidateJMeterCompatibility(&Script{Actions: []Action{{Control: parallel}}})) == 0 {
		t.Fatal("cyclic parallel tree accepted")
	}
	child := Action{Name: "unblocked", Type: "kafka_produce"}
	s = &Script{Config: ScriptConfig{JMeter: &JMeterCompatibility{Profile: JMeterMessagingProfile, SourcePlanID: "plan", SourceGroupID: "group"}}, Actions: []Action{{Metadata: map[string]any{"execution_blocker": "pending"}, Control: &ControlFlow{Branches: []ParallelBranch{{Name: "branch", Steps: []Action{child}}}}}}}
	if isBlockedJMeterMessagingDraft(s) {
		t.Fatal("unblocked parallel child hidden by parent gate")
	}
}

func TestSamplerScheduleAcceptsZeroRateRowsWithSomeLoad(t *testing.T) {
	warmUp := SamplerSchedule{ID: "timer", Segments: []SamplerRateSegment{{StartRPS: 0, EndRPS: 18, DurationSeconds: 60}, {StartRPS: 18, EndRPS: 18, DurationSeconds: 3600}}}
	if issues := ValidateSamplerSchedule(warmUp); len(issues) > 0 {
		t.Fatalf("0 rps warm-up rejected: %v", issues)
	}
	pause := SamplerSchedule{ID: "timer", Segments: []SamplerRateSegment{{StartRPS: 5, EndRPS: 5, DurationSeconds: 60}, {StartRPS: 0, EndRPS: 0, DurationSeconds: 30}}}
	if issues := ValidateSamplerSchedule(pause); len(issues) > 0 {
		t.Fatalf("0 rps pause row rejected: %v", issues)
	}
	idle := SamplerSchedule{ID: "timer", Segments: []SamplerRateSegment{{StartRPS: 0, EndRPS: 0, DurationSeconds: 60}}}
	if issues := ValidateSamplerSchedule(idle); len(issues) == 0 || !strings.Contains(strings.Join(issues, ";"), "at least one positive rate") || !strings.Contains(strings.Join(issues, ";"), "holds every other thread until another sample is admitted") {
		t.Fatalf("all-zero schedule accepted: %v", issues)
	}
	negative := SamplerSchedule{ID: "timer", Segments: []SamplerRateSegment{{StartRPS: -1, EndRPS: 5, DurationSeconds: 60}}}
	if issues := ValidateSamplerSchedule(negative); len(issues) == 0 {
		t.Fatal("negative rate accepted")
	}
}
