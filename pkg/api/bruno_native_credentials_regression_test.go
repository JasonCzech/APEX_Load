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

	candidateBody := inspection.Result.Scripts[0].Script.Actions[0].Body
	if !strings.Contains(candidateBody, "{{secret.") ||
		strings.Contains(candidateBody, "{{param.bodyValue}}") ||
		strings.Contains(candidateBody, "source-xml-variable-credential") {
		t.Fatalf("inspected XML body = %q, want governed secret reference", candidateBody)
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
	body := created.Scripts[0].Actions[0].Body
	if !strings.Contains(body, "{{secret.") ||
		strings.Contains(body, "{{param.bodyValue}}") ||
		strings.Contains(body, "source-xml-variable-credential") {
		t.Fatalf("persisted XML body = %q, want governed secret reference", body)
	}
}
