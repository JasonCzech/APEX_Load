package jmeter

import (
	"regexp"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

var envReference = regexp.MustCompile(`\{\{env\.([A-Za-z0-9_]+)\}\}`)

// An auth manager entry without its credentials falls back to env
// placeholders a script can read once the operator allows them.
func TestAuthFallbackEnvNamesAreReadable(t *testing.T) {
	for _, mechanism := range []string{"BASIC", "DIGEST", "BEARER"} {
		auth := authConfigFromJMeterAuth(jmeterAuth{Mechanism: mechanism}, nil, "test")
		names := 0
		for _, value := range []string{auth.Username, auth.Password, auth.Token} {
			for _, match := range envReference.FindAllStringSubmatch(value, -1) {
				names++
				if credential.ScriptEnvNameAlwaysBlocked(match[1]) {
					t.Errorf("%s fallback {{env.%s}} can never be read by a script", mechanism, match[1])
				}
			}
		}
		if names == 0 {
			t.Errorf("%s: no env fallback in %+v", mechanism, auth)
		}
	}
}
