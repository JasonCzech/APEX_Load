package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

// savedEvaluationArchiveAction requests the cart with {CartId} before the
// home response that saves it, so the import stamps conditional_producer on
// the cart, and has two unsupported TLS settings whose gates block the
// fidelity report until a repair. header, when set, is a literal header the
// conversion keeps on "orders".
func savedEvaluationArchiveAction(header string) string {
	var action strings.Builder
	action.WriteString("Action()\n{\n\tweb_set_sockets_option(\"SSL_VERSION\", \"TLS-next\");\n")
	if header != "" {
		action.WriteString("\tweb_add_header(\"Authorization\", \"" + header + "\");\n")
	}
	action.WriteString("\tweb_url(\"orders\", \"URL=https://example.test/orders\", \"Resource=0\", LAST);\n" +
		"\tweb_url(\"cart\", \"URL=https://example.test/cart/{CartId}\", \"Resource=0\", LAST);\n" +
		"\tweb_reg_save_param(\"CartId\", \"LB=cart=\", \"RB=;\", LAST);\n" +
		"\tweb_url(\"home\", \"URL=https://example.test/home\", \"Resource=0\", LAST);\n" +
		"\tweb_set_sockets_option(\"SSL_VERSION\", \"TLS-future\");\n" +
		"\tweb_url(\"summary\", \"URL=https://example.test/summary/{CartId}\", \"Resource=0\", LAST);\n" +
		"\treturn 0;\n}\n")
	return action.String()
}

// saveRepairedEvaluationArchive imports the archive, saves it as the wizard
// does, and applies the editor's TLS repair, which leaves the report blocked
// on the cart's conditional_producer alone, as on a real archive.
func saveRepairedEvaluationArchive(t *testing.T, header string) (*Server, *storage.SQLiteStore, *dsl.Script) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server := newGuidedRepairArchiveServer(t, store)
	archive := buildEnterpriseArchiveProtocolArchive(t, "Web", savedEvaluationArchiveAction(header))
	validated := serveEnterpriseArchiveDraft(t, server, guidedRepairArchiveValidatePath(store), archive)
	response := postEnterpriseArchiveBatch(t, server, map[string]any{"operation_id": "saved-import-evaluation", "scripts": []any{validated}})
	if response.Code != http.StatusCreated {
		t.Fatalf("save of the validated archive = %d body=%s", response.Code, response.Body.String())
	}
	saved := storedEnterpriseBatchScript(t, server, response)
	if header != "" && (!isCredentialPreservingImport(saved) || namedAction(t, saved, "orders").Headers["Authorization"] != header) {
		t.Fatal("the saved archive is not a credential-preserving import holding its kept credential")
	}
	gates := executionGatePaths(saved, "legacy_tls_replacement")
	if len(gates) != 2 {
		t.Fatalf("saved TLS gates = %v, want one per unsupported TLS setting", gates)
	}
	_, tag := requireAuthoringAPIProjection(t, server, saved.ID)
	decodeScriptRepair(t, scriptRepairCall(t, server, saved.ID, "apply", selectTLSRepair(gates[0]), tag))
	current := durableRepairSnapshot(t, server, saved.ID).Script
	if got := dependencyCodePaths(t, current, "conditional_producer:cartid"); strings.Join(got, ",") != namedActionPath(t, current, "cart") {
		t.Fatalf("conditional_producer:cartid at %v, want it on the cart", got)
	}
	assertSavedImportPromotionHeldOnCart(t, server, current)
	return server, store, current
}

// dependencyCodePaths lists the steps whose stamped findings carry code.
func dependencyCodePaths(t *testing.T, script *dsl.Script, code string) []string {
	t.Helper()
	findings, err := importreview.FindingsFromScript(script)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, finding := range findings {
		if strings.EqualFold(finding.Code, code) {
			paths = append(paths, finding.ActionPath)
		}
	}
	return paths
}

// validateStoredScriptRoute is the editor's POST /scripts/{id}/validate.
func validateStoredScriptRoute(t *testing.T, server *Server, id string) scriptValidationResponse {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+id+"/validate", bytes.NewReader(nil))
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("validate = %d %s", response.Code, response.Body.String())
	}
	var decoded scriptValidationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

// assertSavedImportPromotionHeldOnCart fails unless validation and launch
// admission both refuse the script over the cart's conditional_producer.
func assertSavedImportPromotionHeldOnCart(t *testing.T, server *Server, script *dsl.Script) {
	t.Helper()
	validation := validateStoredScriptRoute(t, server, script.ID)
	if validation.Valid || len(validation.Issues) != 1 || !strings.Contains(validation.Issues[0], "import fidelity blocker") ||
		!strings.Contains(strings.ToLower(validation.Issues[0]), "conditional_producer:cartid") {
		t.Fatalf("validation = valid %v issues %v, want only the cart's promotion review", validation.Valid, validation.Issues)
	}
	if blockers := scriptImportPromotionBlockers(script); len(blockers) != 1 || !strings.Contains(strings.ToLower(blockers[0]), "conditional_producer:cartid") {
		t.Fatalf("launch promotion blockers = %v, want the cart's review code", blockers)
	}
}

// savedFidelityReport reads the script's preserved fidelity report.
func savedFidelityReport(t *testing.T, script *dsl.Script) (string, []string) {
	t.Helper()
	report, _ := script.Metadata.Import.Preserved["fidelity_report"].(map[string]any)
	if report == nil {
		t.Fatal("script has no preserved fidelity report")
	}
	status, _ := report["promotion_status"].(string)
	return status, fidelityReportLines(report["warnings"])
}

// An editor save that fixes the cart's out-of-order read is judged without
// the code the import stamped for it: the server rebuilds the dependency
// codes, the fidelity report and the review's preflight from the saved
// script, as a repair does, so validation passes and launch admission holds
// nothing. A later save that reads the value early again is stamped again.
func TestSavedImportEditorSaveRefreshesDependencyEvaluation(t *testing.T) {
	server, store, current := saveRepairedEvaluationArchive(t, "")
	cartPath := namedActionPath(t, current, "cart")

	if response := putAuthoringEdit(t, server, current.ID, retargetAuthoringAction("cart", "https://example.test/cart/synthetic-cart-1001")); response.Code != http.StatusOK {
		t.Fatalf("PUT replacing the early read = %d %s, want 200", response.Code, response.Body.String())
	}
	fixed := durableRepairSnapshot(t, server, current.ID).Script
	if got := dependencyCodePaths(t, fixed, "conditional_producer:cartid"); len(got) != 0 {
		t.Fatalf("conditional_producer:cartid still stamped at %v after the save fixed it", got)
	}
	if validation := validateStoredScriptRoute(t, server, current.ID); !validation.Valid || len(validation.Issues) != 0 {
		t.Fatalf("validation after the fix = valid %v issues %v, want valid", validation.Valid, validation.Issues)
	}
	if blockers := scriptImportPromotionBlockers(fixed); len(blockers) != 0 {
		t.Fatalf("launch promotion blockers after the fix = %v, want none", blockers)
	}
	if status, warnings := savedFidelityReport(t, fixed); status != "promotable" || len(warnings) != 0 {
		t.Fatalf("fidelity report after the fix = %s %v, want promotable with no review code", status, warnings)
	}
	if preflight := preservedReplayPreflight(fixed); preflight == nil || replayPreflightCheckStatus(preflight, "review_gates") != "pass" {
		t.Fatalf("preflight after the fix = %+v, want it rebuilt from the saved script", preflight)
	}

	// The cart reads the value before home saves it again (the conversion
	// names the variable in lower case). A report that is promotable stays so
	// over a review code alone, as after a repair.
	if response := putAuthoringEdit(t, server, current.ID, retargetAuthoringAction("cart", "/cart/{{var.cartid}}")); response.Code != http.StatusOK {
		t.Fatalf("PUT restoring the early read = %d %s, want 200", response.Code, response.Body.String())
	}
	late, err := store.GetScript(current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := dependencyCodePaths(t, late, "conditional_producer:cartid"); strings.Join(got, ",") != cartPath {
		t.Fatalf("conditional_producer:cartid at %v after the early read returned, want it on %s", got, cartPath)
	}
	if status, warnings := savedFidelityReport(t, late); status != "promotable" || len(warnings) != 1 || !strings.EqualFold(warnings[0], "conditional_producer:cartid") {
		t.Fatalf("fidelity report after the early read returned = %s %v, want it listing the review code", status, warnings)
	}
}

// The evaluation is server output: a save cannot clear the stamped code or
// mark the report promotable while the cart still reads the value early.
func TestSavedImportEditorSaveCannotAuthorImportEvaluation(t *testing.T) {
	server, _, current := saveRepairedEvaluationArchive(t, "")
	cartIndex := namedActionIndex(t, current, "cart")
	forged := func(t *testing.T, document map[string]any) {
		t.Helper()
		actions, _ := document["actions"].([]any)
		cart, _ := actions[cartIndex].(map[string]any)
		delete(cart, "metadata")
		metadata, _ := document["metadata"].(map[string]any)
		imported, _ := metadata["import"].(map[string]any)
		preserved, _ := imported["preserved"].(map[string]any)
		preserved["fidelity_report"] = map[string]any{"promotion_status": "promotable", "blockers": []any{}, "warnings": []any{}}
		delete(preserved, "replay_preflight")
	}
	if response := putAuthoringEdit(t, server, current.ID, forged); response.Code != http.StatusOK {
		t.Fatalf("PUT of a forged evaluation = %d %s, want 200", response.Code, response.Body.String())
	}
	saved := durableRepairSnapshot(t, server, current.ID).Script
	if got := dependencyCodePaths(t, saved, "conditional_producer:cartid"); strings.Join(got, ",") != namedActionPath(t, saved, "cart") {
		t.Fatalf("conditional_producer:cartid at %v, want the server's stamp kept on the cart", got)
	}
	if status, warnings := savedFidelityReport(t, saved); status != "blocked" || len(warnings) != 1 {
		t.Fatalf("fidelity report = %s %v, want the server's blocked report", status, warnings)
	}
	if preservedReplayPreflight(saved) == nil {
		t.Fatal("the save dropped the review's preflight")
	}
	assertSavedImportPromotionHeldOnCart(t, server, saved)
}

// A credential-preserving import is refreshed the same way, and its fences
// hold: the kept credential is unchanged and its request still cannot move.
func TestSavedImportEditorSaveRefreshesCredentialPreservingImport(t *testing.T) {
	server, store, current := saveRepairedEvaluationArchive(t, guidedTLSRepairCredential)
	credentialPath := namedActionPath(t, current, "orders")

	if response := putAuthoringEdit(t, server, current.ID, retargetAuthoringAction("cart", "https://example.test/cart/synthetic-cart-1001")); response.Code != http.StatusOK {
		t.Fatalf("PUT replacing the early read = %d %s, want 200", response.Code, response.Body.String())
	}
	fixed := durableRepairSnapshot(t, server, current.ID).Script
	if got := dependencyCodePaths(t, fixed, "conditional_producer:cartid"); len(got) != 0 {
		t.Fatalf("conditional_producer:cartid still stamped at %v after the save fixed it", got)
	}
	if blockers := scriptImportPromotionBlockers(fixed); len(blockers) != 0 {
		t.Fatalf("launch promotion blockers after the fix = %v, want none", blockers)
	}

	refused := putAuthoringEdit(t, server, current.ID, retargetAuthoringAction("orders", "https://other.example.test/orders"))
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "execution context for retained imported credentials at "+credentialPath) {
		t.Fatalf("PUT moving the kept credential's request = %d %s, want the refusal at %s", refused.Code, refused.Body.String(), credentialPath)
	}
	stored, err := store.GetScript(current.ID)
	if err != nil {
		t.Fatal(err)
	}
	orders := namedAction(t, stored, "orders")
	if !isCredentialPreservingImport(stored) || orders.Headers["Authorization"] != guidedTLSRepairCredential || orders.Target != namedAction(t, current, "orders").Target {
		t.Fatalf("kept credential request = %q %q, want it unchanged", orders.Target, orders.Headers["Authorization"])
	}
}

// An import without a fidelity report or a dependency code never had its
// dependencies evaluated; a save leaves it as its importer wrote it.
func TestSavedImportEditorSaveLeavesUnevaluatedImport(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	script := &dsl.Script{
		ID: "saved-import-unevaluated", Name: "Unevaluated import", Version: "1", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "home", Type: "GET", Method: "GET", Target: "/"}},
	}
	ensureScriptImportPreserved(script, "har")
	if _, err := server.runner.CreateScriptAuthoringDurable(script); err != nil {
		t.Fatal(err)
	}
	if response := putAuthoringEdit(t, server, script.ID, retargetAuthoringAction("home", "/cart/{{var.cartId}}")); response.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s, want 200", response.Code, response.Body.String())
	}
	saved := durableRepairSnapshot(t, server, script.ID).Script
	if got := dependencyCodePaths(t, saved, "missing_producer:cartId"); len(got) != 0 {
		t.Fatalf("save stamped missing_producer at %v on an import that never evaluated dependencies", got)
	}
	if _, reported := saved.Metadata.Import.Preserved["fidelity_report"]; reported {
		t.Fatal("save stamped a fidelity report on an import that had none")
	}
}

// savedEvaluationLongName is a variable name long and varied enough that the
// disclosure's free-text scrub masks it inside a review code.
const savedEvaluationLongName = "cartsessionreferenceq7xk2mp9zt"

// On a credential-preserving import a save that reads a variable nothing
// writes is stamped with a review code whose long name disclosure masks. The
// stamp is server output, so the save is not refused over it, and the masked
// code pins no request: the next save may change the gated request, and the
// code is rebuilt from it.
func TestSavedImportEditorSaveStampsMaskedReviewCodeWithoutPinningTheStep(t *testing.T) {
	server, _, current := saveRepairedEvaluationArchive(t, guidedTLSRepairCredential)
	summaryPath := namedActionPath(t, current, "summary")
	code := "missing_producer:" + savedEvaluationLongName

	if response := putAuthoringEdit(t, server, current.ID, retargetAuthoringAction("summary", "/summary/{{var."+savedEvaluationLongName+"}}")); response.Code != http.StatusOK {
		t.Fatalf("PUT reading an unwritten variable = %d %s, want 200", response.Code, response.Body.String())
	}
	stamped := durableRepairSnapshot(t, server, current.ID).Script
	if got := dependencyCodePaths(t, stamped, code); strings.Join(got, ",") != summaryPath {
		t.Fatalf("%s at %v, want it on %s", code, got, summaryPath)
	}
	if gate, _ := maskScriptSecrets(stamped).Actions[namedActionIndex(t, stamped, "summary")].Metadata["review_gate"].(string); strings.Contains(gate, savedEvaluationLongName) {
		t.Fatalf("disclosed review gate = %q, want the long name masked as the fixture requires", gate)
	}

	if response := putAuthoringEdit(t, server, current.ID, retargetAuthoringAction("summary", "/summary/{{var.cartid}}")); response.Code != http.StatusOK {
		t.Fatalf("PUT correcting the gated request = %d %s, want 200", response.Code, response.Body.String())
	}
	corrected := durableRepairSnapshot(t, server, current.ID).Script
	if got := dependencyCodePaths(t, corrected, code); len(got) != 0 {
		t.Fatalf("%s still stamped at %v after the save corrected the read", code, got)
	}
	if orders := namedAction(t, corrected, "orders"); orders.Headers["Authorization"] != guidedTLSRepairCredential {
		t.Fatal("the saves changed the kept credential")
	}
}

// A Kafka or JMS send delivers its execution_blocker as a header or property,
// and that is where the stamp lands. It is still server output: the save that
// stamps it is not refused over it, and the masked code pins no request, so
// the next save, and a debug start of the same edit, may change the gated
// send, and the code is rebuilt from it. The kept credential header is not
// touched.
func TestSavedImportEditorSaveStampsMaskedMessagingReviewCodeWithoutPinningTheStep(t *testing.T) {
	code := "missing_producer:" + savedEvaluationLongName
	summaryBody := func(body string) func(t *testing.T, document map[string]any) {
		return func(t *testing.T, document map[string]any) {
			t.Helper()
			actions, _ := document["actions"].([]any)
			summary, _ := actions[2].(map[string]any)
			if summary == nil || summary["name"] != "summary" {
				t.Fatalf("authoring document actions[2] = %v, want the summary send", summary)
			}
			summary["body"] = body
		}
	}
	for _, send := range storedMessagingSends {
		t.Run(send.action, func(t *testing.T) {
			server, store := newAuditTestServer(t)
			t.Cleanup(func() { store.Close() })
			current := storeRepairImport(t, server, storedMessagingReviewCodeScript(t, "saved-"+send.action+"-masked-review-code", send)).Script
			summaryPath := namedActionPath(t, current, "summary")

			if response := putAuthoringEdit(t, server, current.ID, summaryBody(`{"session":"{{var.`+savedEvaluationLongName+`}}"}`)); response.Code != http.StatusOK {
				t.Fatalf("PUT reading an unwritten variable = %d %s, want 200", response.Code, response.Body.String())
			}
			stamped := durableRepairSnapshot(t, server, current.ID).Script
			if gate := namedAction(t, stamped, "summary").Metadata["execution_blocker"]; gate != code {
				t.Fatalf("summary execution_blocker = %v, want %s", gate, code)
			}
			if gate, _ := maskScriptSecrets(stamped).Actions[namedActionIndex(t, stamped, "summary")].Metadata["execution_blocker"].(string); strings.Contains(gate, savedEvaluationLongName) {
				t.Fatalf("disclosed execution_blocker = %q, want the long name masked as the fixture requires", gate)
			}

			if response := putAuthoringEdit(t, server, current.ID, summaryBody("{}")); response.Code != http.StatusOK {
				t.Fatalf("PUT correcting the gated send = %d %s, want 200", response.Code, response.Body.String())
			}

			// A debug start resolves a draft by the same checks as a save, so
			// it accepts the same correction of the saved script.
			draft := maskScriptSecrets(stamped)
			draft.Actions[namedActionIndex(t, stamped, "summary")].Body = "{}"
			if _, trusted, err := resolveStoredReplayImportScript(draft, stamped); err != nil || !trusted {
				t.Fatalf("debug start of the corrected send: trusted=%v err=%v, want it resolved against the saved script", trusted, err)
			}

			corrected, err := store.GetScript(current.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got := dependencyCodePaths(t, corrected, code); slices.Contains(got, summaryPath) {
				t.Fatalf("%s still stamped at %v after the save corrected the read", code, got)
			}
			if orders := namedAction(t, corrected, "orders"); orders.Metadata["x-auth"] != storedKafkaRepairCredential || !isCredentialPreservingImport(corrected) {
				t.Fatal("the saves changed the kept credential")
			}
		})
	}
}

// createOrdinaryEvaluatedImport saves, through POST /api/v1/scripts, an
// import that keeps no credentials but whose dependencies were evaluated: a
// fidelity report blocked on two legacy TLS gates, and the review's preflight.
func createOrdinaryEvaluatedImport(t *testing.T, server *Server, id string) *dsl.Script {
	t.Helper()
	script := storedRepairScript(id, "http")
	script.Actions[0].Headers = nil
	created := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts", map[string]any{"script": script}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	stored := durableRepairSnapshot(t, server, id).Script
	if isCredentialPreservingImport(stored) || scriptImportSource(stored) == "" || preservedReplayPreflight(stored) == nil {
		t.Fatal("fixture is not an ordinary evaluated import")
	}
	return stored
}

// stampMaskedReviewCode saves a read of a long-named variable nothing writes
// on the orders request and returns the script with the review code the save
// stamped, whose name disclosure masks.
func stampMaskedReviewCode(t *testing.T, server *Server, id string) *dsl.Script {
	t.Helper()
	if response := putAuthoringEdit(t, server, id, retargetAuthoringAction("orders", "/orders/{{var."+savedEvaluationLongName+"}}")); response.Code != http.StatusOK {
		t.Fatalf("PUT reading an unwritten variable = %d %s, want 200", response.Code, response.Body.String())
	}
	stamped := durableRepairSnapshot(t, server, id).Script
	if got := dependencyCodePaths(t, stamped, "missing_producer:"+savedEvaluationLongName); strings.Join(got, ",") != namedActionPath(t, stamped, "orders") {
		t.Fatalf("long-name review code at %v, want it on orders", got)
	}
	if gate, _ := maskScriptSecrets(stamped).Actions[namedActionIndex(t, stamped, "orders")].Metadata["review_gate"].(string); strings.Contains(gate, savedEvaluationLongName) {
		t.Fatalf("disclosed review gate = %q, want the long name masked as the fixture requires", gate)
	}
	return stamped
}

// An ordinary import is refreshed the same way. The editor sends back the
// masked review code it read; the save rebuilds every dependency code from
// the script, so the masked copy is not a value that needs a secret binding,
// and the next save is judged on the script alone.
func TestSavedImportEditorSaveOfOrdinaryImportIsNotRefusedOverAMaskedReviewCode(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	current := createOrdinaryEvaluatedImport(t, server, "saved-ordinary-masked-review-code")
	code := "missing_producer:" + savedEvaluationLongName
	stampMaskedReviewCode(t, server, current.ID)

	// An unrelated save keeps the code: the request still reads the value.
	if response := putAuthoringEdit(t, server, current.ID, addAuthoringParameter); response.Code != http.StatusOK {
		t.Fatalf("PUT adding a parameter = %d %s, want 200", response.Code, response.Body.String())
	}
	kept := durableRepairSnapshot(t, server, current.ID).Script
	if got := dependencyCodePaths(t, kept, code); strings.Join(got, ",") != namedActionPath(t, kept, "orders") {
		t.Fatalf("%s at %v after an unrelated save, want it kept on orders", code, got)
	}
	if gate := namedAction(t, kept, "orders").Metadata["review_gate"]; gate != code {
		t.Fatalf("stored review gate = %v, want the code as the server stamped it", gate)
	}

	// A dependency code is server output: one the editor forges, even one
	// quoting a credential, never reaches storage.
	const forgedCredential = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJzeW50aGV0aWMtZm9yZ2VkIn0.c3ludGhldGljLWZvcmdlZC1zaWc"
	forged := func(t *testing.T, document map[string]any) {
		t.Helper()
		actions, _ := document["actions"].([]any)
		orders, _ := actions[namedActionIndex(t, kept, "orders")].(map[string]any)
		metadata, _ := orders["metadata"].(map[string]any)
		reasons, _ := metadata["review_reasons"].([]any)
		metadata["review_reasons"] = append(reasons, "missing_producer:"+forgedCredential)
	}
	if response := putAuthoringEdit(t, server, current.ID, forged); response.Code != http.StatusOK {
		t.Fatalf("PUT of a forged dependency code = %d %s, want 200", response.Code, response.Body.String())
	}
	if stored, err := store.GetScript(current.ID); err != nil || strings.Contains(mustMarshalJSON(t, stored), forgedCredential) {
		t.Fatalf("stored script after a forged dependency code: err=%v, want the code dropped", err)
	}

	if response := putAuthoringEdit(t, server, current.ID, retargetAuthoringAction("orders", "/orders")); response.Code != http.StatusOK {
		t.Fatalf("PUT correcting the gated request = %d %s, want 200", response.Code, response.Body.String())
	}
	corrected := durableRepairSnapshot(t, server, current.ID).Script
	if got := dependencyCodePaths(t, corrected, code); len(got) != 0 {
		t.Fatalf("%s still stamped at %v after the save corrected the read", code, got)
	}
	if isCredentialPreservingImport(corrected) {
		t.Fatal("an ordinary save stamped the credential-preserving policy")
	}
}

// A stored repair on that ordinary import passes the save's binding gate the
// same way: the masked review code it restamps is not a raw credential.
func TestStoredScriptRepairOrdinaryImportIsNotRefusedOverAMaskedReviewCode(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	current := createOrdinaryEvaluatedImport(t, server, "stored-repair-ordinary-masked-review-code")
	stampMaskedReviewCode(t, server, current.ID)
	_, tag := requireAuthoringAPIProjection(t, server, current.ID)

	decodeScriptRepair(t, scriptRepairCall(t, server, current.ID, "preview", selectTLSRepair("init[0]"), tag))
	decodeScriptRepair(t, scriptRepairCall(t, server, current.ID, "apply", selectTLSRepair("init[0]"), tag))
	repaired := durableRepairSnapshot(t, server, current.ID).Script
	if got := executionGatePaths(repaired, "legacy_tls_replacement"); len(got) != 0 {
		t.Fatalf("TLS gates after the repair = %v, want none", got)
	}
	if gate := namedAction(t, repaired, "orders").Metadata["review_gate"]; gate != "missing_producer:"+savedEvaluationLongName {
		t.Fatalf("orders review gate after the repair = %v, want the code restamped", gate)
	}

	// A repair of the masked code itself applies as well and clears it.
	_, tag = requireAuthoringAPIProjection(t, server, current.ID)
	supply := ScriptRepairRequest{Finding: "missing_producer:" + savedEvaluationLongName, ActionPath: namedActionPath(t, repaired, "orders"), Operation: ImportRepairOperation{Type: "add_parameter", Parameter: &dsl.Parameter{Name: savedEvaluationLongName, Type: "list", Values: []string{"synthetic-cart-1001"}, Update: "once"}}}
	decodeScriptRepair(t, scriptRepairCall(t, server, current.ID, "apply", supply, tag))
	supplied := durableRepairSnapshot(t, server, current.ID).Script
	if got := dependencyCodePaths(t, supplied, "missing_producer:"+savedEvaluationLongName); len(got) != 0 {
		t.Fatalf("long-name review code still stamped at %v after the repair supplied it", got)
	}
	if isCredentialPreservingImport(supplied) {
		t.Fatal("a repair on an ordinary import stamped the credential-preserving policy")
	}
}

// lockCheckingSecretStore is a project secret store that fails the test when
// it is read while a script mutation holds the watched runner's script
// mutation lock.
type lockCheckingSecretStore struct {
	*runtimeProjectionSecretStore
	t       *testing.T
	watched atomic.Pointer[TestRunner]
	reads   atomic.Int32
}

func (s *lockCheckingSecretStore) Get(ctx context.Context, projectID, name string) (string, secretstore.Metadata, error) {
	if runner := s.watched.Load(); runner != nil {
		s.reads.Add(1)
		// An odd generation means a writer holds the lock.
		if uint64(runner.scriptMutationMu.Observe())%2 == 1 {
			s.t.Errorf("project secret %s read while a script mutation holds the lock", name)
		}
	}
	return s.runtimeProjectionSecretStore.Get(ctx, projectID, name)
}

// An editor save restamps the review's preflight against the project's
// secrets, but resolves them before the durable write, which holds the script
// mutation lock and the secret-reference lease: no secret provider is called
// under them. The saved preflight, and the save's response, still list a
// configured secret as required rather than missing, as a repair's does.
func TestSavedImportEditorSaveResolvesProjectSecretsBeforeTheWrite(t *testing.T) {
	const configured, unconfigured = "WI_SYNTHETIC_SAVED_IMPORT_API_KEY", "WI_SYNTHETIC_SAVED_IMPORT_TRACE_KEY"
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	secrets := &lockCheckingSecretStore{runtimeProjectionSecretStore: &runtimeProjectionSecretStore{values: map[string]string{storage.DefaultProjectID + "/" + configured: "synthetic-saved-import-key"}}, t: t}
	server := NewServer(ServerConfig{Store: store, ProjectSecretStore: secrets})
	script := storedRepairScript("saved-import-secret-snapshot", "http")
	script.Init[1].Headers = map[string]string{"X-Api-Key": "{{secret." + configured + "}}", "X-Trace-Key": "{{secret." + unconfigured + "}}"}
	stored := storeRepairImport(t, server, script)

	secrets.watched.Store(server.runner)
	response := putAuthoringEdit(t, server, stored.ID, addAuthoringParameter)
	if response.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s, want 200", response.Code, response.Body.String())
	}
	if secrets.reads.Load() == 0 {
		t.Fatal("the save never resolved the project's secrets")
	}
	var saved StoredScript
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]*dsl.Script{"response": saved.Script, "persisted": durableRepairSnapshot(t, server, stored.ID).Script} {
		preflight := preservedReplayPreflight(script)
		if preflight == nil || !slices.Equal(preflight.RequiredSecrets, []string{configured, unconfigured}) || !slices.Equal(preflight.MissingSecrets, []string{unconfigured}) {
			t.Fatalf("%s preflight = %+v, want both secrets required and only %s missing", name, preflight, unconfigured)
		}
	}
}
