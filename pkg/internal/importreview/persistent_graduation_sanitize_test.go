package importreview

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Graduating a credential-preserving import to the secret_reference policy
// must leave the stored row as sanitized as any ordinary non-preserving save:
// the review-surface sanitizer skips preserving scripts, so it has to run
// again once the policy flips.
func TestCredentialGraduationSanitizesPreservedReviewSurfaces(t *testing.T) {
	const reviewSecret = "sfdc-review-evidence-live-token-q8w7e6r5t4"
	prior := &dsl.Script{
		ID:       "graduate-sanitize",
		Name:     "Graduate Sanitize",
		Version:  "1.0.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://org.my.salesforce.com"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "har",
			Preserved: map[string]any{
				"source_review": map[string]any{
					"authorization": "Bearer " + reviewSecret,
				},
			},
		}},
		Actions: []dsl.Action{{
			Name:   "01 - GET one.app",
			Type:   "request",
			Method: http.MethodGet,
			Target: "/one/one.app",
			Headers: map[string]string{
				"Authorization": "Bearer sfdc-live-retained-session-token-xyz",
			},
			HeaderList: []dsl.HeaderEntry{
				{Name: "Authorization", Value: "Bearer sfdc-live-retained-session-token-xyz"},
			},
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []credentialpolicy.TargetRef{
					{RequestKey: "actions[0]", Field: "header_list", Index: 0},
				},
			},
		}},
	}
	persisted, err := PreparePersistentImportedLibraryScript(prior, nil, "har")
	if err != nil {
		t.Fatalf("initial import save: %v", err)
	}
	if !IsCredentialPreservingImport(persisted) {
		t.Fatal("expected initial import save to preserve credentials")
	}

	edited := MaskScriptSecrets(persisted)
	if edited == nil {
		t.Fatal("MaskScriptSecrets returned nil")
	}
	edited.Actions[0].Headers["Authorization"] = "Bearer {{secret.sfdc_token}}"
	edited.Actions[0].HeaderList[0].Value = "Bearer {{secret.sfdc_token}}"
	// A credential-authorized editor (or an API client holding the unmasked
	// row) submits the recorded review evidence verbatim. While the script is
	// preserving, the review-surface sanitizer leaves it alone.
	edited.Metadata.Import.Preserved["source_review"] = map[string]any{
		"authorization": "Bearer " + reviewSecret,
	}

	graduated, err := PreparePersistentLibraryScript(edited, persisted)
	if err != nil {
		t.Fatalf("binding save failed: %v", err)
	}
	if IsCredentialPreservingImport(graduated) {
		t.Fatal("expected the bound script to graduate out of credential-preserving mode")
	}
	raw, err := json.Marshal(graduated.Metadata.Import.Preserved)
	if err != nil {
		t.Fatalf("marshal preserved: %v", err)
	}
	if strings.Contains(string(raw), reviewSecret) {
		t.Fatalf("graduated row kept unsanitized review evidence: %s", raw)
	}
}
