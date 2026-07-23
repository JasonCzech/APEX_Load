package javasidecar

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/config"
)

var sidecarSensitiveEnvironmentNames = map[string]struct{}{
	"APEXLOAD_GRPC_AUTH_TOKEN":            {},
	"APEXLOAD_GRPC_TLS_KEY":               {},
	"APEXLOAD_POSTGRES_DSN":               {},
	"APEXLOAD_REDIS_URL":                  {},
	"AWS_ACCESS_KEY_ID":                   {},
	"AWS_SECRET_ACCESS_KEY":               {},
	"AWS_SESSION_TOKEN":                   {},
	"AWS_WEB_IDENTITY_TOKEN_FILE":         {},
	"AZURE_CLIENT_SECRET":                 {},
	"AZURE_FEDERATED_TOKEN_FILE":          {},
	"AZURE_STORAGE_KEY":                   {},
	"AZURE_STORAGE_SAS_TOKEN":             {},
	"GOOGLE_APPLICATION_CREDENTIALS":      {},
	"GOOGLE_APPLICATION_CREDENTIALS_JSON": {},
	"KUBECONFIG":                          {},
}

var sidecarProjectedCredentialFiles = []string{
	"/var/run/secrets/azure/tokens/azure-identity-token",
	"/var/run/secrets/eks.amazonaws.com/serviceaccount/token",
	"/var/run/secrets/kubernetes.io/serviceaccount/token",
}

// ValidateCredentialIsolation rejects ambient credentials in the sidecar
// container. Kubernetes service-discovery variables are deliberately allowed:
// unlike the in-generator local-fork guard, their presence alone does not
// expose a credential to a separately mounted sidecar container.
func ValidateCredentialIsolation() error {
	environment, files := sidecarCredentialExposure(os.Environ(), regularFileExists)
	if len(environment) == 0 && len(files) == 0 {
		return nil
	}
	parts := make([]string, 0, 2)
	if len(environment) > 0 {
		parts = append(parts, "environment: "+strings.Join(environment, ", "))
	}
	if len(files) > 0 {
		parts = append(parts, "files: "+strings.Join(files, ", "))
	}
	return fmt.Errorf("Java runner sidecar has ambient platform credentials (%s)", strings.Join(parts, "; "))
}

func sidecarCredentialExposure(environ []string, fileExists func(string) bool) ([]string, []string) {
	environmentSet := make(map[string]struct{})
	fileSet := make(map[string]struct{})
	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		upper := strings.ToUpper(strings.TrimSpace(name))
		if sidecarSensitiveEnvironmentName(upper) {
			environmentSet[upper] = struct{}{}
		}
		if sidecarCredentialFileEnvironmentName(upper) && fileExists(strings.TrimSpace(value)) {
			fileSet[strings.TrimSpace(value)] = struct{}{}
		}
	}
	for _, path := range sidecarProjectedCredentialFiles {
		if fileExists(path) {
			fileSet[path] = struct{}{}
		}
	}
	return sortedSet(environmentSet), sortedSet(fileSet)
}

func sidecarSensitiveEnvironmentName(name string) bool {
	// Service links are not credentials and are explicitly permitted here.
	if name == "KUBERNETES_SERVICE_HOST" || name == "KUBERNETES_SERVICE_PORT" {
		return false
	}
	// This one secret authenticates the loopback generator-to-runner channel.
	// It is intentionally available to the sidecar process itself, while
	// subprocessguard.Environment keeps it out of every child JVM.
	if name == config.EnvJavaVuserRunnerAuthToken {
		return false
	}
	if _, ok := sidecarSensitiveEnvironmentNames[name]; ok {
		return true
	}
	if !strings.HasPrefix(name, "APEXLOAD_") {
		return false
	}
	for _, marker := range []string{
		"_ACCESS_KEY", "_API_KEY", "_CONNECTION_STRING", "_CREDENTIAL",
		"_PASSWORD", "_PRIVATE_KEY", "_SECRET", "_TOKEN",
	} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func sidecarCredentialFileEnvironmentName(name string) bool {
	if name == "APEXLOAD_GRPC_TLS_KEY" || name == "GOOGLE_APPLICATION_CREDENTIALS" || name == "KUBECONFIG" {
		return true
	}
	return strings.HasSuffix(name, "_TOKEN_FILE") ||
		strings.HasSuffix(name, "_CREDENTIAL_FILE") ||
		strings.HasSuffix(name, "_PRIVATE_KEY_FILE")
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func sortedSet(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
