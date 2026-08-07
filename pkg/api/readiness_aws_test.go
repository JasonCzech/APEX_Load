package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/keywrap"
)

func TestAWSIdentityReadinessReportsBooleansOnly(t *testing.T) {
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "/var/run/secrets/eks.amazonaws.com/serviceaccount/token")
	t.Setenv("AWS_ROLE_ARN", "arn:aws:iam::123456789012:role/apexload-controller")
	t.Setenv("AWS_REGION", "us-east-1")

	details := awsIdentityReadinessDetails()
	for key, want := range map[string]bool{
		"web_identity_token_file": true,
		"role_arn_configured":     true,
		"region_configured":       true,
		"container_credentials":   false,
		"static_keys_in_env":      false,
	} {
		got, ok := details[key].(bool)
		if !ok {
			t.Fatalf("%s = %T, want a bool so /readyz discloses no identifier values", key, details[key])
		}
		if got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	// No value may appear, only its presence.
	for key, value := range details {
		if text, isString := value.(string); isString && strings.Contains(text, "123456789012") {
			t.Fatalf("%s leaked the AWS account id", key)
		}
	}
}

// TestWrappingKeyConflictDegradesReadiness pins that an ambiguous wrapping-key
// configuration is visible. Startup only logs it, the wrapper stays nil, and
// every credential write then 503s without explaining why.
func TestWrappingKeyConflictDegradesReadiness(t *testing.T) {
	t.Setenv(keywrap.KeyVaultKeyIDEnvVar, "https://vault.example/keys/apexload/1")
	t.Setenv(keywrap.KMSKeyIDEnvVar, "arn:aws:kms:us-east-1:123456789012:key/abc")

	details := wrappingKeyReadinessDetails()
	if conflict, _ := details["conflict"].(bool); !conflict {
		t.Fatalf("wrapping_key details = %v, want conflict = true", details)
	}

	t.Setenv(keywrap.KeyVaultKeyIDEnvVar, "")
	details = wrappingKeyReadinessDetails()
	if conflict, _ := details["conflict"].(bool); conflict {
		t.Error("conflict = true with only one wrapping key configured")
	}
	if provider, _ := details["provider"].(string); provider != "aws_kms" {
		t.Errorf("provider = %q, want aws_kms", provider)
	}
}

func TestSecretResolverReadinessReportsSecretsManager(t *testing.T) {
	t.Setenv("APEXLOAD_AWS_SECRETS_MANAGER_WRITE_ENABLED", "true")
	if configured, _ := secretResolverReadinessDetails()["aws_secrets_manager_configured"].(bool); !configured {
		t.Fatal("aws_secrets_manager_configured = false when writes are enabled")
	}
	t.Setenv("APEXLOAD_AWS_SECRETS_MANAGER_WRITE_ENABLED", "")
	if configured, _ := secretResolverReadinessDetails()["aws_secrets_manager_configured"].(bool); configured {
		t.Fatal("aws_secrets_manager_configured = true when writes are disabled")
	}
}
