// Package subprocessguard provides the minimum isolation guarantees available
// to governed local runner processes. It deliberately fails closed when the
// parent process carries platform credentials: a same-UID child can often read
// projected token/key files even when those values are removed from cmd.Env.
package subprocessguard

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

var safeEnvironmentNames = map[string]struct{}{
	"COMSPEC": {}, "JAVA_HOME": {}, "JRE_HOME": {}, "LANG": {}, "LANGUAGE": {},
	"LC_ALL": {}, "LC_CTYPE": {}, "PATH": {}, "PATHEXT": {}, "SYSTEMROOT": {},
	"TEMP": {}, "TMP": {}, "TMPDIR": {}, "TZ": {}, "WINDIR": {},
}

// javaOptionEnvironmentNames are process environment variables the JVM reads to
// inject launch options (agents, classpath, arbitrary flags). They must never
// reach a governed child even through the caller-supplied extra map, so a
// future caller that ever plumbed a user-controlled name through Environment
// cannot smuggle a launch option past the exact-argv VM-parameter denylist.
var javaOptionEnvironmentNames = map[string]struct{}{
	"JAVA_TOOL_OPTIONS": {}, "_JAVA_OPTIONS": {}, "JDK_JAVA_OPTIONS": {},
	"OPENJ9_JAVA_OPTIONS": {}, "IBM_JAVA_OPTIONS": {},
	"JDK_ALTERNATE_VM": {}, "JDK_AOT_VM_OPTIONS": {}, "_JDK_AOT_VM_OPTIONS": {},
	"JAVA_OPTS": {}, "CLASSPATH": {},
}

var sensitiveEnvironmentNames = map[string]struct{}{
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
	// A local child in the generator's Kubernetes pod can scan arbitrary Secret
	// volumes through the shared filesystem even when no known token path or
	// credential environment variable is present. Refuse same-pod execution.
	"KUBERNETES_SERVICE_HOST": {},
	"KUBERNETES_SERVICE_PORT": {},
}

var knownProjectedCredentialFiles = []string{
	"/var/run/secrets/azure/tokens/azure-identity-token",
	"/var/run/secrets/eks.amazonaws.com/serviceaccount/token",
	"/var/run/secrets/kubernetes.io/serviceaccount/token",
}

// ValidateAmbientCredentials refuses local child execution when known platform
// credentials are present. Error text reports names and paths only, never values.
func ValidateAmbientCredentials() error {
	environment, files := credentialExposure(os.Environ(), regularFileExists)
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
	return fmt.Errorf("local sidecar execution is blocked because the runner process has ambient platform credentials (%s); use a credential-free isolated runner", strings.Join(parts, "; "))
}

// Environment returns a small allowlisted child environment plus explicit
// non-secret values required by the caller. Unknown parent variables are not
// inherited.
func Environment(extra map[string]string) []string {
	values := make(map[string]string, len(safeEnvironmentNames)+len(extra))
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		upper := strings.ToUpper(strings.TrimSpace(name))
		if _, allowed := safeEnvironmentNames[upper]; allowed {
			values[upper] = value
		}
	}
	for name, value := range extra {
		name = strings.TrimSpace(name)
		if name == "" || strings.Contains(name, "=") {
			continue
		}
		if _, blocked := javaOptionEnvironmentNames[strings.ToUpper(name)]; blocked {
			continue
		}
		values[name] = value
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name+"="+values[name])
	}
	return out
}

func credentialExposure(environ []string, fileExists func(string) bool) ([]string, []string) {
	environmentSet := make(map[string]struct{})
	fileSet := make(map[string]struct{})
	for _, entry := range environ {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		upper := strings.ToUpper(strings.TrimSpace(name))
		if sensitiveEnvironmentName(upper) {
			environmentSet[upper] = struct{}{}
		}
		if credentialFileEnvironmentName(upper) && fileExists(strings.TrimSpace(value)) {
			fileSet[strings.TrimSpace(value)] = struct{}{}
		}
	}
	for _, path := range knownProjectedCredentialFiles {
		if fileExists(path) {
			fileSet[path] = struct{}{}
		}
	}
	environment := setKeys(environmentSet)
	files := setKeys(fileSet)
	return environment, files
}

func sensitiveEnvironmentName(name string) bool {
	if _, ok := sensitiveEnvironmentNames[name]; ok {
		return true
	}
	if !strings.HasPrefix(name, "APEXLOAD_") {
		return false
	}
	for _, marker := range []string{"_ACCESS_KEY", "_API_KEY", "_CONNECTION_STRING", "_CREDENTIAL", "_PASSWORD", "_PRIVATE_KEY", "_SECRET", "_TOKEN"} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func credentialFileEnvironmentName(name string) bool {
	if name == "APEXLOAD_GRPC_TLS_KEY" || name == "GOOGLE_APPLICATION_CREDENTIALS" || name == "KUBECONFIG" {
		return true
	}
	return strings.HasSuffix(name, "_TOKEN_FILE") || strings.HasSuffix(name, "_CREDENTIAL_FILE") || strings.HasSuffix(name, "_PRIVATE_KEY_FILE")
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func setKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
