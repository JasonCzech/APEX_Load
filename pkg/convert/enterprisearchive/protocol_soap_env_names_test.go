package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
)

// A SOAP service named after a token still gets an endpoint env name a script
// can read.
func TestSourceSOAPEndpointPlaceholderIsReadable(t *testing.T) {
	for _, service := range []string{"TokenService", "SecurityTokenService", "SecretStore", "CredentialService", "Orders", "Token", "Secret", "Password", "Credential"} {
		placeholder := sourceSOAPEndpointPlaceholder(ParsedFunction{Args: map[string]string{"Service": service}})
		name := strings.TrimSuffix(strings.TrimPrefix(placeholder, "{{env."), "}}")
		if name == placeholder || credential.ScriptEnvNameAlwaysBlocked(name) {
			t.Errorf("service %q: placeholder %q is not a readable env reference", service, placeholder)
		}
		// An endpoint is not a credential: CI variables and runtime overrides
		// may carry it as plain text.
		if credential.SensitiveEnvName(name) {
			t.Errorf("service %q: endpoint name %q reads as a credential", service, name)
		}
	}
}

// Different services keep different endpoint variables.
func TestSourceSOAPEndpointPlaceholdersStayDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, service := range []string{"Token", "TokenSvc", "Secret", "SecretSvc", "Password", "Credential", "SOAP_SERVICE", "TokenService", "PasswordService", "PasswdService"} {
		placeholder := sourceSOAPEndpointPlaceholder(ParsedFunction{Args: map[string]string{"Service": service}})
		if other, taken := seen[placeholder]; taken {
			t.Errorf("services %q and %q share %s", other, service, placeholder)
		}
		seen[placeholder] = service
	}
}
