package bruno

import (
	"bytes"
	"strings"
	"testing"
)

// Two requests that each define their own password: two credentials, so the
// second is numbered. Each request's script and its scrubbed source name the
// same secret, and no name carries a path or hash.
func TestNativeSecretNamesNumberDistinctCredentialsConsistently(t *testing.T) {
	request := func(name, value string) []byte {
		return []byte(`info: {name: ` + name + `, uid: ` + name + `-uid, type: http}
variables:
  - name: password
    value: ` + value + `
http:
  method: GET
  url: https://example.test
  auth:
    type: basic
    username: operator
    password: "{{password}}"
`)
	}
	result, err := Inspect(Input{
		Files: map[string][]byte{
			"demo/opencollection.yml": []byte("info: {name: Demo}\n"),
			"demo/a.yml":              request("first", "first-secret-value"),
			"demo/b.yml":              request("second", "second-secret-value"),
		},
		Options: InspectOptions{OutputMode: OutputModeNativeRequests},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Each request's password and basic-auth username are credentials.
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
		if !bytes.Contains(sanitized, []byte(password)) {
			t.Errorf("%s: scrubbed source does not name %s:\n%s", candidate.SourcePath, password, sanitized)
		}
	}
	if len(used) != 2 {
		t.Errorf("both requests use %v, want a secret each", used)
	}
}
