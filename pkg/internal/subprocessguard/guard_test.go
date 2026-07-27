package subprocessguard

import (
	"slices"
	"strings"
	"testing"
)

func TestCredentialExposureFindsPlatformSecretsWithoutValues(t *testing.T) {
	files := map[string]bool{
		"/mounted/azure-token":           true,
		knownProjectedCredentialFiles[0]: true,
	}
	environment, paths := credentialExposure([]string{
		"PATH=/usr/bin",
		"APEXLOAD_GRPC_AUTH_TOKEN=do-not-report-me",
		"AZURE_FEDERATED_TOKEN_FILE=/mounted/azure-token",
		"APEXLOAD_AZURE_KEY_VAULT_URL=https://vault.example.test",
	}, func(path string) bool { return files[path] })

	if !slices.Equal(environment, []string{"APEXLOAD_GRPC_AUTH_TOKEN", "AZURE_FEDERATED_TOKEN_FILE"}) {
		t.Fatalf("environment findings = %v", environment)
	}
	if !slices.Equal(paths, []string{"/mounted/azure-token", knownProjectedCredentialFiles[0]}) {
		t.Fatalf("file findings = %v", paths)
	}
	for _, finding := range append(environment, paths...) {
		if strings.Contains(finding, "do-not-report-me") {
			t.Fatalf("credential value leaked in finding %q", finding)
		}
	}
}

func TestEnvironmentUsesAllowlistAndExplicitExtras(t *testing.T) {
	t.Setenv("PATH", "/safe/bin")
	t.Setenv("APEXLOAD_GRPC_AUTH_TOKEN", "secret")
	t.Setenv("UNRELATED_PRIVATE_DATA", "private")

	environment := Environment(map[string]string{"APEXLOAD_JAVA_VUSER_EVENT_FILE": "/tmp/events"})
	joined := strings.Join(environment, "\n")
	if !strings.Contains(joined, "PATH=/safe/bin") || !strings.Contains(joined, "APEXLOAD_JAVA_VUSER_EVENT_FILE=/tmp/events") {
		t.Fatalf("environment = %v, want safe inherited and explicit values", environment)
	}
	if strings.Contains(joined, "APEXLOAD_GRPC_AUTH_TOKEN") || strings.Contains(joined, "UNRELATED_PRIVATE_DATA") {
		t.Fatalf("environment inherited private values: %v", environment)
	}
}

func TestEnvironmentDropsJavaOptionInjectionNames(t *testing.T) {
	environment := Environment(map[string]string{
		"JAVA_TOOL_OPTIONS":              "-javaagent:/tmp/evil.jar",
		"jdk_java_options":               "-Xbootclasspath/a:/tmp/x",
		"OPENJ9_JAVA_OPTIONS":            "-Xoptionsfile=/tmp/openj9.options",
		"ibm_java_options":               "-Dcom.ibm.tools.attach.enable=yes",
		"JDK_ALTERNATE_VM":               "/tmp/alternate",
		"JDK_AOT_VM_OPTIONS":             "-XX:AOTLibrary=/tmp/native.so",
		"_JDK_AOT_VM_OPTIONS":            "-XX:AOTCache=/tmp/cache.aot",
		"CLASSPATH":                      "/tmp/evil",
		"APEXLOAD_JAVA_VUSER_EVENT_FILE": "/tmp/events",
	})
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "JAVA_TOOL_OPTIONS") || strings.Contains(joined, "JDK_JAVA_OPTIONS") ||
		strings.Contains(joined, "jdk_java_options") || strings.Contains(joined, "OPENJ9_JAVA_OPTIONS") ||
		strings.Contains(joined, "IBM_JAVA_OPTIONS") || strings.Contains(joined, "ibm_java_options") ||
		strings.Contains(joined, "JDK_ALTERNATE_VM") ||
		strings.Contains(joined, "JDK_AOT_VM_OPTIONS") ||
		strings.Contains(joined, "_JDK_AOT_VM_OPTIONS") ||
		strings.Contains(joined, "CLASSPATH") {
		t.Fatalf("environment forwarded a JVM-option injection variable: %v", environment)
	}
	if !strings.Contains(joined, "APEXLOAD_JAVA_VUSER_EVENT_FILE=/tmp/events") {
		t.Fatalf("environment dropped a legitimate explicit value: %v", environment)
	}
}

func TestCredentialExposureRejectsSamePodKubernetesExecution(t *testing.T) {
	environment, paths := credentialExposure([]string{
		"KUBERNETES_SERVICE_HOST=10.0.0.1",
		"KUBERNETES_SERVICE_PORT=443",
	}, func(string) bool { return false })
	if !slices.Equal(environment, []string{"KUBERNETES_SERVICE_HOST", "KUBERNETES_SERVICE_PORT"}) {
		t.Fatalf("environment findings = %v", environment)
	}
	if len(paths) != 0 {
		t.Fatalf("file findings = %v, want none", paths)
	}
}
