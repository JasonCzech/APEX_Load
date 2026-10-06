package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestBrunoNativeCommitGovernsOrdinaryVariableUsedAsXMLCredential(t *testing.T) {
	server, store, _ := newBrunoImportAPITestServer(t)
	inspection := inspectBrunoNativeFiles(t, server, brunoImportTestProject, []brunoInspectJSONFile{
		{
			Path:    "demo/opencollection.yml",
			Content: "opencollection: 1.0.0\ninfo: {name: XML variable credential}\n",
		},
		{
			Path: "demo/request.yml",
			Content: `info: {name: XML variable credential, uid: xml-variable-credential, type: http}
variables:
  - {name: bodyValue, value: source-xml-variable-credential}
http:
  method: POST
  url: https://example.test
  body:
    type: xml
    data: "<Envelope><password>{{bodyValue}}</password></Envelope>"
`,
		},
	})

	candidate := inspection.Result.Scripts[0]
	candidateBody := candidate.Script.Actions[0].Body
	if !strings.Contains(candidateBody, "{{secret.") ||
		strings.Contains(candidateBody, "{{param.bodyValue}}") ||
		strings.Contains(candidateBody, "source-xml-variable-credential") {
		t.Fatalf("inspected XML body = %q, want governed secret reference", candidateBody)
	}
	if len(candidate.SecretRequirements) != 1 ||
		!candidate.SecretRequirements[0].DestinationValuePreserved {
		t.Fatalf(
			"inspected XML requirements = %#v, want exact destination-preservation evidence",
			candidate.SecretRequirements,
		)
	}

	folderID := saveBrunoNativeScriptFolder(
		t,
		store,
		brunoImportTestProject,
		"folder-bruno-xml-variable-credential",
	)
	response := commitBrunoImportWithStatus(t, server, brunoCommitRequest{
		ProjectID:       brunoImportTestProject,
		ImportSessionID: inspection.ImportSessionID,
		Mode:            "create_native_batch",
		FolderID:        &folderID,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("native commit status = %d body=%s", response.Code, response.Body.String())
	}
	created := decodeBrunoNativeBatchTestResponse(t, response)
	responseBody := created.Scripts[0].Actions[0].Body
	if strings.Contains(responseBody, "source-xml-variable-credential") {
		t.Fatalf("commit response disclosed imported XML credential: %q", responseBody)
	}
	stored, err := store.GetScript(candidate.PlannedScriptID)
	if err != nil {
		t.Fatalf("GetScript(%q): %v", candidate.PlannedScriptID, err)
	}
	body := stored.Actions[0].Body
	if body != "<Envelope><password>source-xml-variable-credential</password></Envelope>" ||
		strings.Contains(body, "{{secret.") ||
		strings.Contains(body, "{{param.bodyValue}}") ||
		strings.Contains(body, "[REDACTED]") {
		t.Fatalf("durable XML body = %q, want exact runnable imported body", body)
	}
}
