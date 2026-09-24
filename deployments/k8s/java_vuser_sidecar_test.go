package k8s_test

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
	"gopkg.in/yaml.v3"
)

type javaVuserProbe struct {
	Exec struct {
		Command []string `yaml:"command"`
	} `yaml:"exec"`
	TimeoutSeconds int `yaml:"timeoutSeconds"`
}

type javaVuserEnv struct {
	Name      string `yaml:"name"`
	Value     string `yaml:"value"`
	ValueFrom struct {
		FieldRef struct {
			FieldPath string `yaml:"fieldPath"`
		} `yaml:"fieldRef"`
	} `yaml:"valueFrom"`
}

type javaVuserContainer struct {
	Name         string         `yaml:"name"`
	Image        string         `yaml:"image"`
	Env          []javaVuserEnv `yaml:"env"`
	VolumeMounts []struct {
		Name     string `yaml:"name"`
		ReadOnly bool   `yaml:"readOnly"`
	} `yaml:"volumeMounts"`
	SecurityContext struct {
		AllowPrivilegeEscalation *bool `yaml:"allowPrivilegeEscalation"`
		ReadOnlyRootFilesystem   bool  `yaml:"readOnlyRootFilesystem"`
		Capabilities             struct {
			Drop []string `yaml:"drop"`
		} `yaml:"capabilities"`
	} `yaml:"securityContext"`
	StartupProbe   javaVuserProbe `yaml:"startupProbe"`
	ReadinessProbe javaVuserProbe `yaml:"readinessProbe"`
	LivenessProbe  javaVuserProbe `yaml:"livenessProbe"`
}

type javaVuserStatefulSet struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Template struct {
			Metadata struct {
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"metadata"`
			Spec struct {
				EnableServiceLinks *bool                `yaml:"enableServiceLinks"`
				Containers         []javaVuserContainer `yaml:"containers"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func renderJavaVuserHelm(t *testing.T, enabled bool, imageTags ...string) javaVuserStatefulSet {
	t.Helper()
	if len(imageTags) > 1 {
		t.Fatalf("renderJavaVuserHelm accepts at most one image tag, got %d", len(imageTags))
	}
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is required to render the chart")
	}
	args := []string{
		"template", "apexload", "../helm/apexload", "--namespace", "apexload",
		"--set", "workloadIdentity.controllerClientId=00000000-0000-0000-0000-000000000001",
		"--set", "workloadIdentity.generatorClientId=00000000-0000-0000-0000-000000000002",
		"--set", "image.registry=ci.azurecr.io",
		"--set", "dataPlatform.azureStorageAccount=ciapexloadstorage",
		"--set", "dataPlatform.keyVaultUrl=https://ci-apexload.vault.azure.net",
		"--set", "ingress.host=apexload-ci.example.internal",
	}
	if len(imageTags) == 1 {
		args = append(args, "--set", "image.tag="+imageTags[0])
	}
	if enabled {
		args = append(args,
			"--set", "javaVuser.enabled=true",
			"--set", "javaVuser.assets.existingClaim=java-vuser-assets",
		)
	}
	output, err := exec.Command(helm, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, output)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	for {
		var manifest javaVuserStatefulSet
		if err := decoder.Decode(&manifest); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode rendered manifest: %v", err)
		}
		if manifest.Kind == "StatefulSet" && manifest.Metadata.Name == "apexload-generator" {
			return manifest
		}
	}
	t.Fatal("rendered generator StatefulSet not found")
	return javaVuserStatefulSet{}
}

func findJavaVuserContainer(manifest javaVuserStatefulSet, name string) *javaVuserContainer {
	for i := range manifest.Spec.Template.Spec.Containers {
		if manifest.Spec.Template.Spec.Containers[i].Name == name {
			return &manifest.Spec.Template.Spec.Containers[i]
		}
	}
	return nil
}

func envValue(env []javaVuserEnv, name string) string {
	for _, item := range env {
		if item.Name == name {
			return item.Value
		}
	}
	return ""
}

func envFieldPath(env []javaVuserEnv, name string) string {
	for _, item := range env {
		if item.Name == name {
			return item.ValueFrom.FieldRef.FieldPath
		}
	}
	return ""
}

func readJavaVuserStatefulSet(t *testing.T, path string) javaVuserStatefulSet {
	t.Helper()
	var manifest javaVuserStatefulSet
	if err := yaml.Unmarshal([]byte(readTextFile(t, path)), &manifest); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return manifest
}

func TestJavaVuserSidecarIsOptInAndCredentialMinimized(t *testing.T) {
	defaultManifest := renderJavaVuserHelm(t, false)
	if got := findJavaVuserContainer(defaultManifest, "java-vuser-runner"); got != nil {
		t.Fatal("default chart unexpectedly contains the Java Vuser runner")
	}
	defaultGenerator := findJavaVuserContainer(defaultManifest, "generator")
	if defaultGenerator == nil {
		t.Fatal("default chart generator container not found")
	}
	if got := envValue(defaultGenerator.Env, config.EnvJavaVuserRunnerEnabled); got != "false" {
		t.Fatalf("default chart Java runner enablement = %q, want explicit false", got)
	}
	if got := envFieldPath(defaultGenerator.Env, config.EnvJavaVuserRunnerAuthToken); got != "" {
		t.Fatalf("default chart unexpectedly injects Java runner auth token from %q", got)
	}
	if defaultManifest.Spec.Template.Spec.EnableServiceLinks != nil {
		t.Fatal("default chart unexpectedly changes generator enableServiceLinks")
	}

	manifest := renderJavaVuserHelm(t, true)
	if manifest.Spec.Template.Spec.EnableServiceLinks == nil || *manifest.Spec.Template.Spec.EnableServiceLinks {
		t.Fatal("Java-enabled generator pod must disable service-link environment injection")
	}
	if got := manifest.Spec.Template.Metadata.Annotations["azure.workload.identity/skip-containers"]; got != "java-vuser-runner" {
		t.Fatalf("workload identity skip annotation = %q", got)
	}
	generator := findJavaVuserContainer(manifest, "generator")
	if generator == nil {
		t.Fatal("generator container not found")
	}
	if got := envValue(generator.Env, config.EnvJavaVuserRunnerEnabled); got != "true" {
		t.Fatalf("enabled chart generator Java runner enablement = %q, want true", got)
	}
	if got := envValue(generator.Env, "APEXLOAD_JAVA_VUSER_RUNNER_URL"); got != "http://127.0.0.1:8098" {
		t.Fatalf("generator sidecar URL = %q", got)
	}
	if got := envValue(generator.Env, "APEXLOAD_JAVA_VUSER_RUNNER_MODE"); got != "sidecar" {
		t.Fatalf("generator runner mode = %q", got)
	}
	if got := envFieldPath(generator.Env, config.EnvJavaVuserRunnerAuthToken); got != "metadata.uid" {
		t.Fatalf("generator Java runner auth token field path = %q, want metadata.uid", got)
	}

	runner := findJavaVuserContainer(manifest, "java-vuser-runner")
	if runner == nil {
		t.Fatal("Java Vuser runner container not found")
	}
	if got := envValue(runner.Env, config.EnvJavaVuserRunnerEnabled); got != "true" {
		t.Fatalf("enabled chart sidecar Java runner enablement = %q, want true", got)
	}
	if got := envFieldPath(runner.Env, config.EnvJavaVuserRunnerAuthToken); got != "metadata.uid" {
		t.Fatalf("sidecar Java runner auth token field path = %q, want metadata.uid", got)
	}
	if runner.SecurityContext.AllowPrivilegeEscalation == nil || *runner.SecurityContext.AllowPrivilegeEscalation {
		t.Fatal("runner must disallow privilege escalation")
	}
	if !runner.SecurityContext.ReadOnlyRootFilesystem {
		t.Fatal("runner must use a read-only root filesystem")
	}
	if len(runner.SecurityContext.Capabilities.Drop) != 1 || runner.SecurityContext.Capabilities.Drop[0] != "ALL" {
		t.Fatalf("runner dropped capabilities = %v", runner.SecurityContext.Capabilities.Drop)
	}
	for name, probe := range map[string]struct {
		probe    javaVuserProbe
		endpoint string
	}{
		"startup":   {probe: runner.StartupProbe, endpoint: "/readyz"},
		"readiness": {probe: runner.ReadinessProbe, endpoint: "/readyz"},
		"liveness":  {probe: runner.LivenessProbe, endpoint: "/livez"},
	} {
		command := probe.probe.Exec.Command
		wantURL := "http://127.0.0.1:8098" + probe.endpoint
		if len(command) != 4 || command[0] != "curl" {
			t.Fatalf("%s probe = %v, want loopback endpoint %s", name, command, wantURL)
		}
		if command[3] != wantURL {
			t.Fatalf("%s probe URL = %q, want %q", name, command[3], wantURL)
		}
		if probe.endpoint == "/readyz" && probe.probe.TimeoutSeconds < 12 {
			t.Fatalf("%s probe timeoutSeconds = %d, want at least 12", name, probe.probe.TimeoutSeconds)
		}
	}
	for _, mount := range runner.VolumeMounts {
		switch mount.Name {
		case "java-vuser-runner-tmp":
		case "java-vuser-assets":
			if !mount.ReadOnly {
				t.Fatal("Java asset volume must be read-only")
			}
		default:
			t.Fatalf("runner unexpectedly receives volume %q", mount.Name)
		}
	}
	for _, forbidden := range []string{"APEXLOAD_GRPC_AUTH_TOKEN", "APEXLOAD_GRPC_TLS_KEY", "AZURE_CLIENT_ID", "AZURE_FEDERATED_TOKEN_FILE"} {
		if got := envValue(runner.Env, forbidden); got != "" {
			t.Fatalf("runner unexpectedly receives %s", forbidden)
		}
	}
	if got := envValue(runner.Env, "APEXLOAD_JAVA_VUSER_WORK_DIR"); got != "/tmp" {
		t.Fatalf("runner work directory = %q, want bounded writable /tmp", got)
	}
}

func TestJavaVuserOperatorEnablementIsExplicitInRawDeployments(t *testing.T) {
	for _, path := range []string{
		"local/generator-statefulset.yaml",
		"onprem/generator-statefulset.yaml",
		"aks/generator-statefulset.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			manifest := readJavaVuserStatefulSet(t, path)
			generator := findJavaVuserContainer(manifest, "generator")
			if generator == nil {
				t.Fatal("generator container not found")
			}
			if got := envValue(generator.Env, config.EnvJavaVuserRunnerEnabled); got != "false" {
				t.Fatalf("%s Java runner enablement = %q, want explicit false", path, got)
			}
		})
	}

	component := readJavaVuserStatefulSet(t, "components/java-vuser-runner/generator-java-vuser-patch.yaml")
	for _, containerName := range []string{"generator", "java-vuser-runner"} {
		container := findJavaVuserContainer(component, containerName)
		if container == nil {
			t.Fatalf("Java Vuser Kustomize component container %q not found", containerName)
		}
		if got := envValue(container.Env, config.EnvJavaVuserRunnerEnabled); got != "true" {
			t.Fatalf("Java Vuser Kustomize component %s enablement = %q, want true", containerName, got)
		}
		if got := envFieldPath(container.Env, config.EnvJavaVuserRunnerAuthToken); got != "metadata.uid" {
			t.Fatalf("Java Vuser Kustomize component %s auth token field path = %q, want metadata.uid", containerName, got)
		}
	}

	var compose struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(readTextFile(t, "../../compose.yaml")), &compose); err != nil {
		t.Fatalf("decode compose.yaml: %v", err)
	}
	const explicitFalseDefault = "${APEXLOAD_JAVA_VUSER_RUNNER_ENABLED:-false}"
	const explicitToken = "${APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN:-}"
	for _, serviceName := range []string{"generator", "java-vuser-runner"} {
		service, ok := compose.Services[serviceName]
		if !ok {
			t.Fatalf("compose service %q not found", serviceName)
		}
		if got := service.Environment[config.EnvJavaVuserRunnerEnabled]; got != explicitFalseDefault {
			t.Fatalf("compose service %s Java runner enablement = %q, want %q", serviceName, got, explicitFalseDefault)
		}
		if got := service.Environment[config.EnvJavaVuserRunnerAuthToken]; got != explicitToken {
			t.Fatalf("compose service %s Java runner auth token = %q, want explicit pass-through %q", serviceName, got, explicitToken)
		}
	}
	javaRunner := readTextFile(t, "../../compose.yaml")
	for _, required := range []string{
		`test: ["CMD", "curl", "--fail", "--silent", "http://127.0.0.1:8098/readyz"]`,
		"timeout: 12s",
	} {
		if !strings.Contains(javaRunner, required) {
			t.Fatalf("compose Java runner healthcheck missing %q", required)
		}
	}

	runtimeDocs := readTextFile(t, "../../docs/PROTOCOL_SUPPORT.md")
	for _, required := range []string{
		"APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN",
		"per-pod sidecar authentication token",
		"<operator-generated-random-token>",
		"writable, delegated cgroup v2 subtree",
		"stock non-privileged Helm, Kustomize, and Compose sidecars",
		"advertise legacy\n`full_lifecycle` execution only",
		"persistent_lifecycle_unavailable",
		"process_containment_unavailable",
		"privileged cgroup-mount manifest",
	} {
		if !strings.Contains(runtimeDocs, required) {
			t.Fatalf("Java runtime documentation missing %q", required)
		}
	}
	for path, required := range map[string][]string{
		"../README.md": { // deployments/README.md, "Java Vuser runner component" section
			"legacy `full_lifecycle` execution only",
			"persistent_lifecycle_unavailable",
			"Java Vuser Runtime",
		},
		"../helm/apexload/README.md": {
			"`javaVuser.enabled=true`",
			"legacy `full_lifecycle` execution",
			"persistent_lifecycle_unavailable",
		},
	} {
		content := readTextFile(t, path)
		for _, phrase := range required {
			if !strings.Contains(content, phrase) {
				t.Fatalf("%s missing Java containment warning %q", path, phrase)
			}
		}
	}
}

func TestJavaVuserHelmMaxProcessBoundMatchesRuntime(t *testing.T) {
	schema := readTextFile(t, "../helm/apexload/values.schema.json")
	want := fmt.Sprintf(`"maxProcesses": { "type": "integer", "minimum": 1, "maximum": %d }`, config.MaxJavaVuserMaxProcesses)
	if !strings.Contains(schema, want) {
		t.Fatalf("Helm Java Vuser maxProcesses constraint must match runtime bound %d", config.MaxJavaVuserMaxProcesses)
	}
}

func TestJavaVuserRunnerImageAlwaysUsesPublishedStandardCoordinate(t *testing.T) {
	for _, test := range []struct {
		globalTag string
		wantTag   string
	}{
		{globalTag: "1.2.3", wantTag: "1.2.3-standard"},
		{globalTag: "1.2.3-standard", wantTag: "1.2.3-standard"},
		{globalTag: "1.2.3-enterprise", wantTag: "1.2.3-standard"},
	} {
		t.Run(test.globalTag, func(t *testing.T) {
			manifest := renderJavaVuserHelm(t, true, test.globalTag)
			runner := findJavaVuserContainer(manifest, "java-vuser-runner")
			if runner == nil {
				t.Fatal("Java Vuser runner container not found")
			}
			want := "ci.azurecr.io/apexload-java-vuser-runner:" + test.wantTag
			if runner.Image != want {
				t.Fatalf("runner image = %q, want published coordinate %q", runner.Image, want)
			}
		})
	}
}
