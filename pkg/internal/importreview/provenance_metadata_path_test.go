package importreview

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestIsImportProvenanceMetadataPathRequiresSurfaceMetadata(t *testing.T) {
	for path, want := range map[string]bool{
		"/metadata/source_uid":                                              true,
		"/actions/0/metadata/source_uid":                                    true,
		"/actions/0/metadata/source_transaction/name":                       true,
		"/init/2/metadata/original_url":                                     true,
		"/actions/1/control/steps/0/metadata/source_uid":                    true,
		"/actions/1/control/else/3/metadata/source_uid":                     true,
		"/actions/1/control/default/0/metadata/source_uid":                  true,
		"/actions/1/control/cases/0/steps/2/metadata/source_uid":            true,
		"/actions/1/control/branches/1/steps/0/metadata/source_uid":         true,
		"/actions/2/params/requests/4/metadata/original_url":                true,
		"/actions/3/params/request/metadata/source_transaction":             true,
		"/actions/0/params/map_body/metadata/source_uid":                    false,
		"/actions/0/params/map_body/metadata/source_uid/token":              false,
		"/actions/0/params/requests/0/body/metadata/source_uid":             false,
		"/actions/0/params/map_body/steps/0/metadata/source_uid":            false,
		"/config/grpc/metadata/source_uid":                                  false,
		"/actions/0/metadata/authorization":                                 false,
		"/actions/0/control/steps/x/metadata/source_uid":                    false,
		"/actions/1/control/cases/0/condition/metadata/source_uid":          false,
		"/actions/0/params/request/body/metadata/source_transaction":        false,
		"actions/0/metadata/source_uid":                                     true,
		"actions/0/params/map_body/metadata/source_uid/token":               false,
		"/actions/0/metadata/import_review/metadata/source_uid":             true,
		"/actions/0/headers/metadata/source_uid":                            false,
		"/teardown/0/control/steps/1/params/requests/0/metadata/source_uid": true,
	} {
		if got := IsImportProvenanceMetadataPath(path); got != want {
			t.Errorf("IsImportProvenanceMetadataPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// A provenance-looking key nested inside an executable params payload is wire
// data. An orphan [REDACTED] sentinel there must be rejected, never persisted
// and sent.
func TestReconcileRejectsOrphanSentinelUnderParamsProvenanceLookalike(t *testing.T) {
	prior := &dsl.Script{
		ID: "jms-params", Name: "JMS Params", Version: "1.0.0", Protocol: "jms",
		Actions: []dsl.Action{{
			Name: "publish", Type: "jms_publish", Target: "queue://orders",
			Params: map[string]any{
				"map_body": map[string]any{
					"metadata": map[string]any{
						"source_uid": map[string]any{"token": "recorded-wire-value"},
					},
				},
			},
		}},
	}
	edited := CloneScriptDefinition(prior)
	body := edited.Actions[0].Params["map_body"].(map[string]any)
	body["metadata"].(map[string]any)["source_uid"] = map[string]any{"token": MaskedScriptSecret}
	if reqs := DiscoverSecretBindingRequirements(edited, 0); len(reqs) != 1 {
		t.Fatalf("DiscoverSecretBindingRequirements = %#v, want 1 requirement for params lookalike", reqs)
	}
	err := ReconcileMaskedScriptSecrets(edited, nil)
	if err == nil || !strings.Contains(err.Error(), "orphan") {
		t.Fatalf("expected orphan-sentinel rejection from ReconcileMaskedScriptSecrets(edited, nil), got %v", err)
	}

	unredacted := CloneScriptDefinition(prior)
	saved, err := PreparePersistentLibraryScript(unredacted, prior)
	if err != nil {
		t.Fatalf("expected unredacted params value save to succeed, got %v", err)
	}
	savedBody := saved.Actions[0].Params["map_body"].(map[string]any)
	if got := savedBody["metadata"].(map[string]any)["source_uid"].(map[string]any)["token"]; got != "recorded-wire-value" {
		t.Fatalf("saved token = %#v, want recorded-wire-value", got)
	}
}
