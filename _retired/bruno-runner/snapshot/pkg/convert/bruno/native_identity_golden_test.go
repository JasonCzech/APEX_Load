package bruno

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestInspectNativeStableIdentityGolden(t *testing.T) {
	result, err := Inspect(Input{
		ProjectID: "project-import-contract",
		Files: map[string][]byte{
			"identity/opencollection.yml": []byte(`opencollection: 1.0.0
info: {name: Identity contract, uid: identity-contract}
`),
			"identity/request.yml": []byte(`info: {name: Identity request, uid: identity-request, type: http, seq: 1}
http:
  method: POST
  url: https://api.contract.test/session
  headers:
    - {name: X-API-Key, value: stable-source-credential}
  body:
    type: json
    data: '{"note":"stable"}'
settings: {maxRedirects: 51}
`),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer clearNativeIdentityGoldenSecrets(result)
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %d, want 1", len(result.Scripts))
	}
	candidate := result.Scripts[0]
	if _, err := uuid.Parse(candidate.PlannedScriptID); err != nil {
		t.Fatalf("planned script ID = %q, want generated UUID: %v", candidate.PlannedScriptID, err)
	}
	type diagnosticIdentity struct {
		ID        string `json:"id"`
		FindingID string `json:"finding_id,omitempty"`
		Code      string `json:"code"`
	}
	snapshot := struct {
		CandidateID     string               `json:"candidate_id"`
		PlannedScriptID string               `json:"planned_script_id"`
		Diagnostics     []diagnosticIdentity `json:"diagnostics"`
		RequirementIDs  []string             `json:"requirement_ids"`
	}{
		CandidateID:     candidate.CandidateID,
		PlannedScriptID: "<generated-script-id>",
		Diagnostics:     make([]diagnosticIdentity, len(candidate.Diagnostics)),
		RequirementIDs:  make([]string, len(candidate.SecretRequirements)),
	}
	for index, diagnostic := range candidate.Diagnostics {
		snapshot.Diagnostics[index] = diagnosticIdentity{
			ID:        diagnostic.ID,
			FindingID: diagnostic.FindingID,
			Code:      diagnostic.Code,
		}
	}
	for index, requirement := range candidate.SecretRequirements {
		snapshot.RequirementIDs[index] = requirement.ID
	}
	got, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"candidate_id":"7bf2ee1c8d2adfc33e5b446e81c42db9","planned_script_id":"\u003cgenerated-script-id\u003e","diagnostics":[{"id":"bruno-native-db075f7ca29fce4a2cebb442","finding_id":"bruno-native-db075f7ca29fce4a2cebb442","code":"bruno_max_redirects_clamped"}],"requirement_ids":["e44b2e871bb51ba79d7cc721"]}`
	if string(got) != want {
		t.Fatalf("stable identity snapshot changed:\n got: %s\nwant: %s", got, want)
	}
}

func clearNativeIdentityGoldenSecrets(result *Result) {
	if result == nil {
		return
	}
	for index := range result.SecretRequirements {
		result.SecretRequirements[index].Value = ""
	}
	for candidateIndex := range result.Scripts {
		for requirementIndex := range result.Scripts[candidateIndex].SecretRequirements {
			result.Scripts[candidateIndex].SecretRequirements[requirementIndex].Value = ""
		}
	}
}
