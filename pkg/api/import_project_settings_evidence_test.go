package api

import (
	"archive/zip"
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// projectSettingsArchiveOrder is the transaction order a project file records:
// names joined by '*'. Its long mixed names read as opaque tokens to the
// disclosure's free-text scrub, as a real archive's did.
const projectSettingsArchiveOrder = "T01_Launch*S01_Tablet_StoreLookup_T02_SubmitQuoteApproval*T03_Cart"

// projectSettingsArchiveAction keeps a literal Authorization header on
// "orders"; "cart" carries no credential. The conversion names each request
// after its transaction (projectSettingsOrdersStep, projectSettingsCartStep).
var projectSettingsArchiveAction = "Action()\n{\n" +
	"\tlr_start_transaction(\"T01_Launch\");\n" +
	"\tweb_add_header(\"Authorization\", \"" + guidedTLSRepairCredential + "\");\n" +
	"\tweb_url(\"orders\", \"URL=https://example.test/orders\", \"Resource=0\", LAST);\n" +
	"\tlr_end_transaction(\"T01_Launch\", LR_AUTO);\n" +
	"\tlr_start_transaction(\"S01_Tablet_StoreLookup_T02_SubmitQuoteApproval\");\n" +
	"\tweb_url(\"cart\", \"URL=https://example.test/cart\", \"Resource=0\", LAST);\n" +
	"\tlr_end_transaction(\"S01_Tablet_StoreLookup_T02_SubmitQuoteApproval\", LR_AUTO);\n" +
	"\treturn 0;\n}\n"

const (
	projectSettingsOrdersStep = "T01_Launch_orders"
	projectSettingsCartStep   = "S01_Tablet_StoreLookup_T02_SubmitQuoteApproval_cart"
)

// buildProjectSettingsArchive is a Web archive whose project file records its
// transaction order.
func buildProjectSettingsArchive(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	for _, file := range []struct{ name, content string }{
		{"Protocol.usr", "[General]\nType=Web\n\n[Actions]\nAction1=Action.c\n\n[TransactionsOrder]\nOrder=" + projectSettingsArchiveOrder + "\n"},
		{"Action.c", projectSettingsArchiveAction},
	} {
		entry, err := writer.Create(file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(file.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

// A saved archive import keeps its project file's sections as review
// evidence, and disclosure masks parts of the recorded transaction order.
// That masked copy is sent by no request, so the editor's save of a request
// without the kept credential is accepted, as it must be on a real archive
// whose transaction names read as opaque tokens. Moving the kept credential's
// request to another host is still refused at that request.
func TestSavedArchiveImportProjectSettingsKeepEditorSavesOpen(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server := newGuidedRepairArchiveServer(t, store)
	validated := serveEnterpriseArchiveDraft(t, server, guidedRepairArchiveValidatePath(store), buildProjectSettingsArchive(t))
	response := postEnterpriseArchiveBatch(t, server, map[string]any{"operation_id": "project-settings-evidence", "scripts": []any{validated}})
	if response.Code != http.StatusCreated {
		t.Fatalf("save of the validated archive = %d body=%s", response.Code, response.Body.String())
	}
	saved := storedEnterpriseBatchScript(t, server, response)
	if !isCredentialPreservingImport(saved) || namedAction(t, saved, projectSettingsOrdersStep).Headers["Authorization"] != guidedTLSRepairCredential {
		t.Fatal("the saved archive is not a credential-preserving import holding its kept credential")
	}
	assertProjectSettingsOrderMasked(t, saved)
	credentialPath := namedActionPath(t, saved, projectSettingsOrdersStep)

	if edited := putAuthoringEdit(t, server, saved.ID, retargetAuthoringAction(projectSettingsCartStep, "https://example.test/cart/summary")); edited.Code != http.StatusOK {
		t.Fatalf("PUT retargeting a request without the kept credential = %d %s, want 200", edited.Code, edited.Body.String())
	}
	if edited := putAuthoringEdit(t, server, saved.ID, addAuthoringParameter); edited.Code != http.StatusOK {
		t.Fatalf("PUT adding a parameter = %d %s, want 200", edited.Code, edited.Body.String())
	}
	refused := putAuthoringEdit(t, server, saved.ID, retargetAuthoringAction(projectSettingsOrdersStep, "https://other.example.test/orders"))
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "execution context for retained imported credentials at "+credentialPath) {
		t.Fatalf("PUT moving the kept credential's request to another host = %d %s, want the refusal at %s", refused.Code, refused.Body.String(), credentialPath)
	}
	if strings.Contains(refused.Body.String(), "guided-repair-synthetic-token") || strings.Contains(refused.Body.String(), "Tablet") {
		t.Fatal("the refusal disclosed a recorded value")
	}

	current, err := store.GetScript(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	orders := namedAction(t, current, projectSettingsOrdersStep)
	if orders.Headers["Authorization"] != guidedTLSRepairCredential || orders.Target != namedAction(t, saved, projectSettingsOrdersStep).Target {
		t.Fatalf("kept credential request = %q %q, want it unchanged", orders.Target, orders.Headers["Authorization"])
	}
	if cart := namedAction(t, current, projectSettingsCartStep); cart.Target != "https://example.test/cart/summary" || len(current.Parameters) != 1 {
		t.Fatalf("the stored script lost an accepted edit: cart=%q parameters=%+v", cart.Target, current.Parameters)
	}
	assertProjectSettingsOrderMasked(t, current)
}

// assertProjectSettingsOrderMasked fails unless the script keeps its project
// file's transaction order and disclosure masks part of it, which is what made
// the recorded settings read as a kept, script-wide credential.
func assertProjectSettingsOrderMasked(t *testing.T, script *dsl.Script) {
	t.Helper()
	order := func(script *dsl.Script) string {
		if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
			return ""
		}
		settings, _ := script.Metadata.Import.Preserved["project_settings"].(map[string]any)
		section, _ := settings["TransactionsOrder"].(map[string]any)
		text, _ := section["Order"].(string)
		return text
	}
	if got := order(script); got != projectSettingsArchiveOrder {
		t.Fatalf("preserved transaction order = %q, want the recorded %q", got, projectSettingsArchiveOrder)
	}
	if disclosed := order(maskScriptSecrets(script)); disclosed == projectSettingsArchiveOrder || !strings.Contains(disclosed, "[REDACTED]") {
		t.Fatalf("disclosed transaction order = %q, want part of it masked", disclosed)
	}
}
