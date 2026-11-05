package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

// A SOAP service named after a token still gets an endpoint env name a script
// can read.
func TestSourceSOAPEndpointPlaceholderIsReadable(t *testing.T) {
	for _, service := range []string{"TokenService", "SecretStore", "Orders"} {
		placeholder := sourceSOAPEndpointPlaceholder(ParsedFunction{Args: map[string]string{"Service": service}})
		name := strings.TrimSuffix(strings.TrimPrefix(placeholder, "{{env."), "}}")
		if name == placeholder || credential.ScriptEnvNameAlwaysBlocked(name) {
			t.Errorf("service %q: placeholder %q is not a readable env reference", service, placeholder)
		}
	}
}
