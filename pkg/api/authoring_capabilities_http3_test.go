package api

import (
	"slices"
	"strings"
	"testing"

	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func TestAuthoringCapabilitiesReportControllerHTTP3Execution(t *testing.T) {
	t.Setenv(httpprotocol.EnvHTTP3, "")
	off := authoringCapabilityResponse().HTTPRuntime
	if off.HTTP3.Exact || off.HTTP3.Upgrade || off.HTTP3.Scope != "controller" || !strings.Contains(off.HTTP3.Reason, httpprotocol.EnvHTTP3) {
		t.Fatalf("disabled runtime = %+v", off)
	}
	if !slices.Equal(off.Versions, []string{"1.1", "2"}) {
		t.Fatalf("disabled versions = %v", off.Versions)
	}

	t.Setenv(httpprotocol.EnvHTTP3, "true")
	on := authoringCapabilityResponse().HTTPRuntime
	if !on.HTTP3.Exact || !on.HTTP3.Upgrade || on.HTTP3.Reason != "" {
		t.Fatalf("enabled runtime = %+v", on)
	}
	if !slices.Equal(on.Versions, []string{"1.1", "2", "3", "auto+h3"}) {
		t.Fatalf("enabled versions = %v", on.Versions)
	}
}
