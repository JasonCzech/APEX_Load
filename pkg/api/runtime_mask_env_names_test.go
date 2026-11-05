package api

import "testing"

// The import suggests readable env names (API_TOKEN becomes API_TKN); a value
// an operator supplies for one is still protected as a credential.
func TestSensitiveEnvKeyCoversReadableNames(t *testing.T) {
	for _, name := range []string{"API_TKN", "ACCESS_TKN", "CLIENT_SCRT", "PRIVKEY", "API_PW"} {
		if !sensitiveEnvKey(name) {
			t.Errorf("sensitiveEnvKey(%q) = false, want it protected", name)
		}
	}
}
