package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestIsAIGeneratedDraftUsesStructuredMarkers(t *testing.T) {
	tests := []struct {
		name string
		by   string
		from string
		want bool
	}{
		{name: "deterministic marker", by: "apexload-ai-deterministic-local", want: true},
		{name: "explicit source", from: "ai_generated", want: true},
		{name: "ordinary maintainer name", by: "Maira", want: false},
		{name: "ordinary detail label", by: "detail-importer", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isAIGeneratedDraft(&dsl.Script{GeneratedBy: test.by, GeneratedFrom: test.from}); got != test.want {
				t.Fatalf("isAIGeneratedDraft() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAuthoringCapabilitiesReportDeterministicAIActive(t *testing.T) {
	t.Setenv("APEXLOAD_AI_ENABLED", "true")
	t.Setenv("APEXLOAD_AI_DISABLED_PROJECTS", "")
	t.Setenv("APEXLOAD_AI_ALLOWED_PROVIDERS", defaultAIProvider)
	for _, source := range authoringCapabilityResponse().Sources {
		if source.Source == "ai_generated" {
			if source.Status != "active" {
				t.Fatalf("AI authoring status = %q, want active", source.Status)
			}
			return
		}
	}
	t.Fatal("AI authoring capability is missing")
}

func TestAuthoringCapabilitiesRespectAIPolicy(t *testing.T) {
	tests := []struct {
		name      string
		projectID string
		configure func(*testing.T)
	}{
		{
			name: "globally disabled",
			configure: func(t *testing.T) {
				t.Setenv("APEXLOAD_AI_ENABLED", "false")
			},
		},
		{
			name:      "project disabled",
			projectID: "project-1",
			configure: func(t *testing.T) {
				t.Setenv("APEXLOAD_AI_DISABLED_PROJECTS", "project-1")
			},
		},
		{
			name: "default provider excluded",
			configure: func(t *testing.T) {
				t.Setenv("APEXLOAD_AI_ALLOWED_PROVIDERS", mockedAIProvider)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_AI_ENABLED", "true")
			t.Setenv("APEXLOAD_AI_DISABLED_PROJECTS", "")
			t.Setenv("APEXLOAD_AI_ALLOWED_PROVIDERS", defaultAIProvider)
			test.configure(t)
			for _, source := range authoringCapabilityResponse(test.projectID).Sources {
				if source.Source == "ai_generated" {
					if source.Status != "disabled" {
						t.Fatalf("AI authoring status = %q, want disabled", source.Status)
					}
					return
				}
			}
			t.Fatal("AI authoring capability is missing")
		})
	}
}
