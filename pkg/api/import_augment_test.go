package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func augmentTestScript(name string) *dsl.Script {
	return &dsl.Script{
		Name:     name,
		Version:  "1.0.0",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions: []dsl.Action{{
			Name: "GET orders", Type: "request", Method: "GET", Target: "/orders",
		}},
	}
}

func augmentTestRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/example", nil)
}

// A draft that supplies nothing but a script still comes back with the whole
// cross-cutting set attached. This is the invariant the shared pass exists to
// hold: an importer cannot ship without provenance, diagnostics, or preflight
// by forgetting to attach them.
func TestImportDraftAugmentAttachesTheSharedSetForABareDraft(t *testing.T) {
	script := augmentTestScript("Orders")
	draft := importDraft{
		Source:           "example_format",
		SourceScriptName: "orders.yaml",
		ProjectID:        "proj-augment",
		Script:           script,
		Unsupported: diag.Diagnostics{
			{Element: "example_construct", Detail: "example construct is not converted", Severity: diag.Warning},
		},
	}

	augmented := draft.Augment(augmentTestRequest())

	if script.ProjectID != "proj-augment" {
		t.Fatalf("script.ProjectID = %q, want proj-augment", script.ProjectID)
	}
	if script.Metadata == nil || script.Metadata.Import == nil {
		t.Fatalf("script carries no import provenance: %#v", script.Metadata)
	}
	if got := script.Metadata.Import.Source; got != "example_format" {
		t.Fatalf("import source = %q, want example_format", got)
	}
	if got := script.Metadata.Import.SourceScriptName; got != "orders.yaml" {
		t.Fatalf("import source script name = %q, want orders.yaml", got)
	}
	if augmented.Preflight == nil {
		t.Fatal("augmentation produced no replay preflight")
	}
	if _, ok := script.Metadata.Import.Preserved["replay_preflight"]; !ok {
		t.Fatalf("preflight was not preserved on the draft: %#v", script.Metadata.Import.Preserved)
	}
	if len(augmented.Sections) == 0 {
		t.Fatal("converter diagnostics produced no review section")
	}
}

// Provenance is attached whether or not the converter produced a source review.
// attachSourceReviewToScript writes provenance only alongside a review, so a
// converter without one used to leave the draft unattributable.
func TestImportDraftAugmentRecordsProvenanceWithoutASourceReview(t *testing.T) {
	script := augmentTestScript("No Review")
	draft := importDraft{Source: "broker_config", ProjectID: "proj-x", Script: script}

	draft.Augment(augmentTestRequest())

	if script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Source != "broker_config" {
		t.Fatalf("import provenance = %#v, want source broker_config", script.Metadata)
	}
}

// Each script in a batch is validated on its own. A sibling's validation issue
// must not mark a clean script unreplayable, which is what applying one
// batch-wide issue list to every script used to do.
func TestImportDraftAugmentValidatesEachBatchScriptIndependently(t *testing.T) {
	clean := augmentTestScript("Clean")
	broken := augmentTestScript("Broken")
	broken.Protocol = ""

	draft := importDraft{
		Source:    "batch_format",
		ProjectID: "proj-batch",
		Script:    clean,
		Scripts:   []*dsl.Script{clean, broken},
	}
	augmented := draft.Augment(augmentTestRequest())

	if augmented.Preflight == nil || !augmented.Preflight.Valid {
		t.Fatalf("primary preflight = %#v, want valid", augmented.Preflight)
	}
	brokenPreflight, ok := broken.Metadata.Import.Preserved["replay_preflight"].(*ReplayPreflight)
	if !ok {
		t.Fatalf("second script carries no preflight: %#v", broken.Metadata.Import.Preserved)
	}
	if brokenPreflight.Valid {
		t.Fatal("invalid batch script reported a valid preflight")
	}
	if len(augmented.Warnings) == 0 {
		t.Fatal("batch validation issues did not reach the response warnings")
	}
}

// Validation issues belong in the preflight's Issues field. Folding them into
// its warnings as well double-counts them in the validation check the review
// panel renders.
func TestImportDraftAugmentKeepsValidationIssuesOutOfPreflightWarnings(t *testing.T) {
	script := augmentTestScript("Invalid")
	script.Protocol = ""

	draft := importDraft{Source: "example_format", ProjectID: "proj-y", Script: script}
	augmented := draft.Augment(augmentTestRequest())

	if augmented.Preflight == nil {
		t.Fatal("augmentation produced no preflight")
	}
	if len(augmented.Preflight.Issues) == 0 {
		t.Fatal("preflight reported no validation issues")
	}
	for _, issue := range augmented.Preflight.Issues {
		for _, warning := range augmented.Preflight.Warnings {
			if issue == warning {
				t.Fatalf("validation issue %q was also counted as a preflight warning", issue)
			}
		}
	}
}

// The MuleSoft detector runs for every format now, not the handful that
// remembered to call it, and its section reaches the response.
func TestImportDraftAugmentRunsProtocolDetectorsForEveryFormat(t *testing.T) {
	script := augmentTestScript("Anypoint")
	script.Config.BaseURL = "https://orders.us-e2.cloudhub.io"
	script.Actions[0].Target = "https://orders.us-e2.cloudhub.io/api/orders"

	draft := importDraft{Source: "k6", ProjectID: "proj-mule", Script: script}
	augmented := draft.Augment(augmentTestRequest())

	var found bool
	for _, section := range augmented.Sections {
		if section.Key == "mulesoft_review" {
			found = true
		}
	}
	if !found {
		t.Fatalf("MuleSoft review section missing for a non-spec importer: %#v", augmented.Sections)
	}
	if _, ok := script.Metadata.Import.Preserved["mulesoft_review"]; !ok {
		t.Fatal("MuleSoft review was not preserved on the draft")
	}
}

// A caller's own section can suppress a policy the shared MuleSoft section would
// otherwise repeat. RAML uses this for client_id_enforcement.
func TestAttachMuleSoftReviewSuppressesNamedPolicies(t *testing.T) {
	script := augmentTestScript("Anypoint")
	script.Config.BaseURL = "https://orders.us-e2.cloudhub.io"
	script.Actions[0].Target = "https://orders.us-e2.cloudhub.io/api/orders"
	// The analyzer reads the header map, not the ordered header list.
	script.Actions[0].Headers = map[string]string{"client_id": "abc", "client_secret": "def"}

	withPolicy := attachMuleSoftReview(script, "raml", nil)
	suppressed := attachMuleSoftReview(augmentTestScriptCloneOf(script), "raml", nil, "client_id_enforcement")

	if !sectionsMention(withPolicy, "client_id_enforcement") {
		t.Skip("fixture no longer trips the client_id_enforcement policy")
	}
	if sectionsMention(suppressed, "client_id_enforcement") {
		t.Fatalf("suppressed policy still reported: %#v", suppressed)
	}
}

func augmentTestScriptCloneOf(script *dsl.Script) *dsl.Script {
	clone := *script
	clone.Metadata = nil
	clone.Actions = append([]dsl.Action(nil), script.Actions...)
	return &clone
}

func sectionsMention(sections []scriptImportReviewSection, needle string) bool {
	for _, section := range sections {
		for _, item := range section.Items {
			if strings.Contains(item, needle) {
				return true
			}
		}
	}
	return false
}
