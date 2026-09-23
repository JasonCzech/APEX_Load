package dsl

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateMessageDetailResourceIssues(t *testing.T) {
	tests := []struct {
		name   string
		config *MessageDetailConfig
		want   string
	}{
		{name: "absent preserves legacy behavior"},
		{name: "errors", config: &MessageDetailConfig{Mode: "errors", MaxItems: 128, MaxItemBytes: 1 << 20, MaxTotalBytes: 16 << 20}},
		{name: "sampled", config: &MessageDetailConfig{Mode: "sampled", SampleRate: 0.01}},
		{name: "unknown mode", config: &MessageDetailConfig{Mode: "everything"}, want: "mode must be"},
		{name: "sampled requires rate", config: &MessageDetailConfig{Mode: "sampled"}, want: "greater than 0"},
		{name: "rate bounded", config: &MessageDetailConfig{Mode: "adaptive", SampleRate: 1.1}, want: "between 0 and 1"},
		{name: "item count bounded", config: &MessageDetailConfig{Mode: "errors", MaxItems: 4097}, want: "between 0 and 4096"},
		{name: "total covers item", config: &MessageDetailConfig{Mode: "errors", MaxItemBytes: 1024, MaxTotalBytes: 512}, want: "must not be smaller"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := validateMessageDetailResourceIssues(test.config)
			if test.want == "" {
				if len(issues) != 0 {
					t.Fatalf("issues = %#v, want none", issues)
				}
				return
			}
			if !strings.Contains(strings.Join(issues, "\n"), test.want) {
				t.Fatalf("issues = %#v, want containing %q", issues, test.want)
			}
		})
	}
}

func TestValidateScriptIssuesKeepsSnapshotAndMessageDetailIndependent(t *testing.T) {
	script := &Script{
		Name: "capture", Version: "1", Protocol: "http", Actions: []Action{{Name: "GET", Type: "GET", Target: "https://example.test"}},
		Config: ScriptConfig{
			ErrorHandling: ErrorHandlingConfig{SnapshotOnError: true},
			Results:       ResultsConfig{MessageDetail: &MessageDetailConfig{Mode: "sampled"}},
		},
	}
	issues := strings.Join(ValidateScriptIssues(script), "\n")
	if !strings.Contains(issues, "sample_rate must be greater than 0") {
		t.Fatalf("issues = %q, want message-detail validation independent of snapshot_on_error", issues)
	}
}

func TestResourceCaptureBooleanContract(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		var config MessageDetailConfig
		if err := json.Unmarshal([]byte(`{"mode":"errors","include_resources":`+value+`}`), &config); err != nil {
			t.Fatal(err)
		}
		if issues := validateMessageDetailResourceIssues(&config); len(issues) != 0 {
			t.Fatal(issues)
		}
		raw, err := json.Marshal(config)
		if err != nil || !strings.Contains(string(raw), `"include_resources":`+value) {
			t.Fatalf("boolean lost: %s %v", raw, err)
		}
	}
	var config MessageDetailConfig
	if err := json.Unmarshal([]byte(`{"include_resources":"false"}`), &config); err == nil {
		t.Fatal("non-boolean resource policy accepted")
	}
}
