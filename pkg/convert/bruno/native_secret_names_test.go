package bruno

import (
	"bytes"
	"strings"
	"testing"
)

// Two requests that each reference distinct unresolved credentials: two
// requirements per field, so the second is numbered without path/hash noise.
func TestNativeSecretNamesNumberDistinctCredentialsConsistently(t *testing.T) {
	request := func(name, suffix string) []byte {
		return []byte(`info: {name: ` + name + `, uid: ` + name + `-uid, type: http}
variables:
  - name: username
    value: "user-{{missing_` + suffix + `}}"
  - name: password
    value: "prefix-{{missing_` + suffix + `}}"
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: "{{username}}"
    password: "{{password}}"
`)
	}
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte("info: {name: Demo}\n"),
			"demo/a.yml":              request("first", "one"),
			"demo/b.yml":              request("second", "two"),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, requirement := range result.SecretRequirements {
		names[requirement.SuggestedName] = true
		if strings.Contains(requirement.SuggestedName, "DEMO") || len(requirement.SuggestedName) > len("PASSWORD_2") {
			t.Errorf("name %q carries a path or hash", requirement.SuggestedName)
		}
	}
	if len(names) != 4 || !names["PASSWORD"] || !names["PASSWORD_2"] || !names["USERNAME"] || !names["USERNAME_2"] {
		t.Fatalf("names = %v, want PASSWORD, PASSWORD_2, USERNAME and USERNAME_2", names)
	}
	used := map[string]bool{}
	for _, candidate := range result.Scripts {
		password := candidate.Script.Actions[0].Auth.Password
		used[password] = true
		sanitized := tarFile(t, result.SanitizedArchive, "demo/"+strings.TrimPrefix(candidate.SourcePath, "demo/"))
		if !bytes.Contains(sanitized, []byte("prefix-{{missing_")) {
			t.Errorf("%s: archived source did not preserve authored template:\n%s", candidate.SourcePath, sanitized)
		}
	}
	if len(used) != 2 {
		t.Errorf("both requests use %v, want a secret each", used)
	}
}
