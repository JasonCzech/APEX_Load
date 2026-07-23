package javasidecar

import (
	"slices"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
)

func TestSidecarCredentialExposureAllowsKubernetesServiceVariables(t *testing.T) {
	environment, files := sidecarCredentialExposure([]string{
		"KUBERNETES_SERVICE_HOST=10.0.0.1",
		"KUBERNETES_SERVICE_PORT=443",
		"APEXLOAD_JAVA_VUSER_RUNNER_URL=http://127.0.0.1:8098",
		"APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN=loopback-only-token",
	}, func(string) bool { return false })
	if len(environment) != 0 || len(files) != 0 {
		t.Fatalf("service-discovery-only exposure = environment %v files %v", environment, files)
	}
}

func TestRunnerAuthTokenIsNotInheritedByChildJVM(t *testing.T) {
	const secret = "loopback-runner-secret"
	t.Setenv(config.EnvJavaVuserRunnerAuthToken, secret)
	childEnvironment := strings.Join(subprocessguard.Environment(nil), "\n")
	if strings.Contains(childEnvironment, config.EnvJavaVuserRunnerAuthToken) ||
		strings.Contains(childEnvironment, secret) {
		t.Fatal("runner authentication token leaked into child JVM environment")
	}
}

func TestSidecarCredentialExposureRejectsSecretsWithoutValues(t *testing.T) {
	const secret = "never-include-this-value"
	tokenPath := "/mounted/federated-token"
	filesPresent := map[string]bool{
		tokenPath:                          true,
		sidecarProjectedCredentialFiles[0]: true,
	}
	environment, files := sidecarCredentialExposure([]string{
		"APEXLOAD_GRPC_AUTH_TOKEN=" + secret,
		"AZURE_FEDERATED_TOKEN_FILE=" + tokenPath,
	}, func(path string) bool { return filesPresent[path] })
	if !slices.Equal(environment, []string{"APEXLOAD_GRPC_AUTH_TOKEN", "AZURE_FEDERATED_TOKEN_FILE"}) {
		t.Fatalf("environment findings = %v", environment)
	}
	if !slices.Equal(files, []string{tokenPath, sidecarProjectedCredentialFiles[0]}) {
		t.Fatalf("file findings = %v", files)
	}
	if strings.Contains(strings.Join(append(environment, files...), "\n"), secret) {
		t.Fatal("credential findings leaked a value")
	}
}
