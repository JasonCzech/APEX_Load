package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestValidateSuccessfulMessageDetailFeature(t *testing.T) {
	script := &dsl.Script{Protocol: "http", Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{
		MessageDetail: &dsl.MessageDetailConfig{Mode: "sampled", SampleRate: 0.1},
	}}}
	if err := validateSuccessfulMessageDetailFeature(script, nil); err == nil || !strings.Contains(err.Error(), "disabled for this project") {
		t.Fatalf("disabled gate error = %v", err)
	}
	enabled := []projectQuotaSnapshot{{features: storage.ProjectFeatureFlags{SuccessfulMessageDetail: true}}}
	if err := validateSuccessfulMessageDetailFeature(script, enabled); err != nil {
		t.Fatalf("enabled feature rejected: %v", err)
	}
	script.Config.Results.MessageDetail.Mode = "adaptive"
	if err := validateSuccessfulMessageDetailFeature(script, enabled); err != nil {
		t.Fatalf("adaptive capture rejected: %v", err)
	}
	script.Config.Results.MessageDetail.Mode = "sampled"
	script.Protocol = "database"
	if err := validateSuccessfulMessageDetailFeature(script, enabled); err == nil || !strings.Contains(err.Error(), "http protocol only") {
		t.Fatalf("unsupported protocol error = %v", err)
	}
	script.Config.Results.MessageDetail.Mode = "errors"
	if err := validateSuccessfulMessageDetailFeature(script, nil); err != nil {
		t.Fatalf("errors-only legacy evidence was gated: %v", err)
	}
}
