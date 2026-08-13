package k8s_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var placeholderPattern = regexp.MustCompile(`\$\{([A-Z0-9_]+)\}`)

// TestRenderScriptHandlesEveryOverlayPlaceholder guards B10: every ${PLACEHOLDER}
// used in an AKS overlay must have a matching substitution rule in
// scripts/render-aks-manifests.sh. Without this, a placeholder like
// ${KEDA_WORKLOAD_IDENTITY_CLIENT_ID} (aks-azure-monitor TriggerAuthentication)
// silently survives rendering and the KEDA scaler fails workload-identity auth,
// stalling generator autoscaling.
func TestRenderScriptHandlesEveryOverlayPlaceholder(t *testing.T) {
	for _, target := range []struct {
		script string
		dirs   []string
	}{
		{
			script: "../../scripts/render-aks-manifests.sh",
			dirs:   []string{"aks", "aks-azure-monitor", "aks-prometheus", "onprem-autoscaling"},
		},
		{
			script: "../../scripts/render-eks-manifests.sh",
			dirs:   []string{"eks", "eks-ha", "eks-prometheus"},
		},
	} {
		assertRenderScriptCoversPlaceholders(t, target.script, target.dirs)
	}
}

func assertRenderScriptCoversPlaceholders(t *testing.T, scriptPath string, overlayDirs []string) {
	t.Helper()
	renderScript := readTextFile(t, scriptPath)

	seen := map[string]bool{}
	for _, dir := range overlayDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // overlay is optional; skip if absent
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}
			content := readTextFile(t, filepath.Join(dir, entry.Name()))
			for _, m := range placeholderPattern.FindAllStringSubmatch(content, -1) {
				name := m[1]
				if seen[name] {
					continue
				}
				seen[name] = true
				if !strings.Contains(renderScript, name) {
					t.Errorf("overlay placeholder ${%s} (in %s/%s) has no substitution rule in %s", name, dir, entry.Name(), scriptPath)
				}
			}
		}
	}
	if len(seen) == 0 {
		t.Fatalf("no ${...} placeholders found across the overlays for %s; test wiring is likely broken", scriptPath)
	}
}

func TestFreshClusterBasesDoNotIncludeOptionalCRDResources(t *testing.T) {
	tests := []struct {
		path      string
		forbidden []string
	}{
		{
			path:      "local/kustomization.yaml",
			forbidden: []string{"generator-keda-scaledobject.yaml", "servicemonitor.yaml"},
		},
		{
			path:      "onprem/kustomization.yaml",
			forbidden: []string{"generator-keda-scaledobject.yaml", "servicemonitor.yaml"},
		},
		{
			path:      "aks/kustomization.yaml",
			forbidden: []string{"servicemonitor.yaml"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			content := readTextFile(t, tt.path)
			for _, forbidden := range tt.forbidden {
				if strings.Contains(content, forbidden) {
					t.Fatalf("%s includes optional CRD-backed resource %q", tt.path, forbidden)
				}
			}
		})
	}
}

func TestMonitoringOverlaysUseKustomizeBase(t *testing.T) {
	base := readTextFile(t, "monitoring/kustomization.yaml")
	if !strings.Contains(base, "prometheusrule.yaml") {
		t.Fatal("monitoring Kustomize base must include the shared PrometheusRule")
	}
	for _, path := range []string{"aks-prometheus/kustomization.yaml", "eks-prometheus/kustomization.yaml", "onprem-autoscaling/kustomization.yaml"} {
		content := readTextFile(t, path)
		if !strings.Contains(content, "../monitoring") || strings.Contains(content, "../monitoring/prometheusrule.yaml") {
			t.Fatalf("%s must reference the monitoring directory base, not a sibling file", path)
		}
	}
}

func TestKEDAScaledObjectsProtectActiveDemand(t *testing.T) {
	for _, path := range []string{
		"aks/generator-keda-scaledobject.yaml",
		"eks/generator-keda-scaledobject.yaml",
		"onprem/generator-keda-scaledobject.yaml",
		"local/generator-keda-scaledobject.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			content := readTextFile(t, path)
			checks := []string{
				"metricName: apexload_active_vusers",
				`query: sum(apexload_scaling_vusers{state="active"})`,
				"metricName: apexload_active_browser_vusers",
				`query: sum(apexload_scaling_protocol_vusers{state="active",protocol="browser"})`,
			}
			for _, check := range checks {
				if !strings.Contains(content, check) {
					t.Fatalf("%s missing %q", path, check)
				}
			}
		})
	}
}

func TestKEDAScaledObjectsKeepScaleDownStabilization(t *testing.T) {
	for _, path := range []string{
		"aks/generator-keda-scaledobject.yaml",
		"eks/generator-keda-scaledobject.yaml",
		"onprem/generator-keda-scaledobject.yaml",
		"local/generator-keda-scaledobject.yaml",
		"onprem-autoscaling/generator-keda-scaledobject.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			content := readTextFile(t, path)
			if !strings.Contains(content, "scaleDown:") || !strings.Contains(content, "stabilizationWindowSeconds: 600") {
				t.Fatalf("%s must keep a 600s scaleDown stabilization window", path)
			}
		})
	}
}

func TestGeneratorManifestsUseGRPCHealthProbes(t *testing.T) {
	for _, path := range []string{
		"local/generator-statefulset.yaml",
		"onprem/generator-statefulset.yaml",
		"aks/generator-statefulset.yaml",
		"eks/generator-statefulset.yaml",
		"../helm/apexload/templates/generator.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			content := readTextFile(t, path)
			if !strings.Contains(content, "readinessProbe:") || !strings.Contains(content, "livenessProbe:") || !strings.Contains(content, "grpc:") {
				t.Fatalf("%s must use gRPC readiness/liveness probes", path)
			}
			if strings.Contains(content, "sleep 15") {
				t.Fatalf("%s must not use blind preStop sleep for generator drain", path)
			}
			if !strings.Contains(content, "name: health") || !strings.Contains(content, "containerPort: 9092") || strings.Count(content, "port: 9092") < 2 {
				t.Fatalf("%s must probe the dedicated unauthenticated health-only listener on 9092", path)
			}
		})
	}
}

func TestProductionGeneratorManifestsPersistExecutionFenceState(t *testing.T) {
	for _, path := range []string{
		"onprem/generator-statefulset.yaml",
		"aks/generator-statefulset.yaml",
		"eks/generator-statefulset.yaml",
		"../helm/apexload/templates/generator.yaml",
	} {
		t.Run(path, func(t *testing.T) {
			content := readTextFile(t, path)
			for _, required := range []string{
				"APEXLOAD_GENERATOR_STATE_DIR",
				"APEXLOAD_ARCHIVE_DIR",
				"/var/lib/apexload-generator",
				"/var/lib/apexload-generator/archives",
				"name: generator-state",
				"persistentVolumeClaimRetentionPolicy:",
				"whenDeleted: Retain",
				"whenScaled: Retain",
				"volumeClaimTemplates:",
			} {
				if !strings.Contains(content, required) {
					t.Fatalf("%s missing durable generator fencing state setting %q", path, required)
				}
			}
			if !strings.Contains(path, "../helm/") && !strings.Contains(content, "ReadWriteOnce") {
				t.Fatalf("%s must request a per-ordinal ReadWriteOnce volume", path)
			}
		})
	}
	values := readTextFile(t, "../helm/apexload/values.yaml")
	for _, required := range []string{"statePersistence:", "ReadWriteOnce", "storageClassName:", "size: 10Gi"} {
		if !strings.Contains(values, required) {
			t.Fatalf("Helm values missing generator state persistence setting %q", required)
		}
	}
}

func TestLocalGeneratorManifestPersistsExecutionFenceStateAcrossContainerRestart(t *testing.T) {
	content := readTextFile(t, "local/generator-statefulset.yaml")
	for _, required := range []string{
		"APEXLOAD_GENERATOR_STATE_DIR",
		"value: /var/lib/apexload-generator",
		"APEXLOAD_ARCHIVE_DIR",
		"value: /var/lib/apexload-generator/archives",
		"- name: generator-state\n              mountPath: /var/lib/apexload-generator",
		"- name: generator-state\n          emptyDir: {}",
	} {
		if !strings.Contains(content, required) {
			t.Fatalf("local generator manifest missing container-restart-persistent fencing state setting %q", required)
		}
	}
}

func TestGeneratorRunSupervisionDeploymentContract(t *testing.T) {
	for _, path := range []string{
		"local/generator-statefulset.yaml",
		"onprem/generator-statefulset.yaml",
		"aks/generator-statefulset.yaml",
		"eks/generator-statefulset.yaml",
		"../helm/apexload/templates/generator.yaml",
	} {
		content := readTextFile(t, path)
		for _, required := range []string{"APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL", "APEXLOAD_GENERATOR_ABANDON_DRAIN"} {
			if !strings.Contains(content, required) {
				t.Fatalf("%s missing generator run-supervision setting %q", path, required)
			}
		}
	}
	for _, path := range []string{
		"local/controller-deployment.yaml",
		"onprem/configmap.yaml",
		"aks/configmap.yaml",
		"eks/configmap.yaml",
		"../helm/apexload/templates/configmap.yaml",
	} {
		content := readTextFile(t, path)
		for _, required := range []string{"APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL", "APEXLOAD_GENERATOR_RUN_SUPERVISION_RENEW_INTERVAL"} {
			if !strings.Contains(content, required) {
				t.Fatalf("%s missing controller run-supervision setting %q", path, required)
			}
		}
	}

	values := readTextFile(t, "../helm/apexload/values.yaml")
	for _, required := range []string{"ttl: 60s", "abandonDrain: 5s", "renewInterval: 10s"} {
		if !strings.Contains(values, required) {
			t.Fatalf("Helm values missing fail-closed supervision default %q", required)
		}
	}
	schema := readTextFile(t, "../helm/apexload/values.schema.json")
	if !strings.Contains(schema, `"renewInterval": { "type": "string", "pattern": "^(?:[1-9]|10)s$" }`) {
		t.Fatal("Helm schema must cap renewInterval at 10s so every allowed 30s-60s TTL retains three renewal opportunities")
	}
	controllerTemplate := readTextFile(t, "../helm/apexload/templates/controller.yaml")
	if !strings.Contains(controllerTemplate, "checksum/config:") {
		t.Fatal("Helm controller pod template must roll when run-supervision ConfigMap values change")
	}

	compose := readTextFile(t, "../../compose.yaml")
	for _, required := range []string{
		`APEXLOAD_GENERATOR_RUN_SUPERVISION_TTL: "60s"`,
		`APEXLOAD_GENERATOR_RUN_SUPERVISION_RENEW_INTERVAL: "10s"`,
		`APEXLOAD_GENERATOR_ABANDON_DRAIN: "5s"`,
		"APEXLOAD_GENERATOR_STATE_DIR: /data/state",
		"generator-data:/data",
		"generator-data:",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("compose.yaml missing persistent supervision contract %q", required)
		}
	}
}

func TestHAOverlaysSetReplicasAndFlagTogether(t *testing.T) {
	for _, path := range []string{"onprem-ha/kustomization.yaml", "aks-ha/kustomization.yaml", "eks-ha/kustomization.yaml"} {
		t.Run(path, func(t *testing.T) {
			content := readTextFile(t, path)
			for _, check := range []string{"path: /spec/replicas", "value: 2", "APEXLOAD_CONTROLLER_HA", `value: "true"`} {
				if !strings.Contains(content, check) {
					t.Fatalf("%s missing HA invariant %q", path, check)
				}
			}
		})
	}
}

func TestAKSRawApplyUsesRenderer(t *testing.T) {
	content := readTextFile(t, "../../Makefile")
	if !strings.Contains(content, "k8s-aks-apply: k8s-aks-render") {
		t.Fatalf("Makefile must make k8s-aks-apply depend on k8s-aks-render")
	}
	if strings.Contains(content, "k8s-aks-apply:\n\tkubectl apply -k deployments/k8s/aks") {
		t.Fatalf("Makefile regressed to raw AKS kustomize apply")
	}
}

func TestDockerLocalRunsAsNonRoot(t *testing.T) {
	dockerfile := readTextFile(t, "../../Dockerfile")
	for _, check := range []string{"chown -R 65532:65532", "USER 65532:65532"} {
		if !strings.Contains(dockerfile, check) {
			t.Fatalf("Dockerfile missing %q", check)
		}
	}
	compose := readTextFile(t, "../../compose.yaml")
	for _, check := range []string{`user: "65532:65532"`, "no-new-privileges:true", "read_only: true"} {
		if !strings.Contains(compose, check) {
			t.Fatalf("compose.yaml missing %q", check)
		}
	}
}

func TestDockerRuntimeImagePreparesBrunoArtifactDirectory(t *testing.T) {
	dockerfile := readTextFile(t, "../../Dockerfile")
	for _, check := range []string{
		"mkdir -p /data /tmp /opt/playwright-driver /var/lib/apexload-bruno",
		"chown -R 65532:65532 /data /tmp /var/lib/apexload-bruno",
	} {
		if !strings.Contains(dockerfile, check) {
			t.Fatalf("Dockerfile must prepare the Bruno artifact directory for uid 65532; missing %q", check)
		}
	}
}

func TestDockerComposeIncludesGeneratorFleet(t *testing.T) {
	compose := readTextFile(t, "../../compose.yaml")
	for _, check := range []string{"generator:", "BINARY: generator", "APEXLOAD_GENERATORS: dns://generator:9090", "APEXLOAD_GENERATOR_MAX_VUSERS"} {
		if !strings.Contains(compose, check) {
			t.Fatalf("compose.yaml missing generator fleet check %q", check)
		}
	}
}

func TestDockerDataPlatformOverride(t *testing.T) {
	override := readTextFile(t, "../docker/compose.data-platform.yaml")
	type composeService struct {
		Image       string            `yaml:"image"`
		Environment map[string]string `yaml:"environment"`
		DependsOn   map[string]struct {
			Condition string `yaml:"condition"`
		} `yaml:"depends_on"`
		Networks    map[string]any `yaml:"networks"`
		Ports       []any          `yaml:"ports"`
		Volumes     []string       `yaml:"volumes"`
		Command     []string       `yaml:"command"`
		Healthcheck struct {
			Test []string `yaml:"test"`
		} `yaml:"healthcheck"`
	}
	var model struct {
		Services map[string]composeService `yaml:"services"`
		Volumes  map[string]any            `yaml:"volumes"`
		Networks map[string]struct {
			Internal bool `yaml:"internal"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal([]byte(override), &model); err != nil {
		t.Fatalf("parse data-platform Compose override: %v", err)
	}
	requireService := func(name string) composeService {
		t.Helper()
		service, ok := model.Services[name]
		if !ok {
			t.Fatalf("data-platform Compose override missing %s service", name)
		}
		return service
	}
	assertNetworks := func(name string, service composeService, want ...string) {
		t.Helper()
		if len(service.Networks) != len(want) {
			t.Fatalf("%s networks = %v, want only %v", name, service.Networks, want)
		}
		for _, network := range want {
			if _, ok := service.Networks[network]; !ok {
				t.Fatalf("%s must join %q; networks = %v", name, network, service.Networks)
			}
		}
	}
	contains := func(values []string, want string) bool {
		for _, value := range values {
			if value == want {
				return true
			}
		}
		return false
	}

	controller := requireService("controller")
	postgres := requireService("postgres")
	redis := requireService("redis")
	if controller.Environment["APEXLOAD_STORE"] != "postgres" || controller.Environment["APEXLOAD_CACHE_BACKEND"] != "redis" {
		t.Fatalf("controller must select PostgreSQL and Redis: environment = %v", controller.Environment)
	}
	if !strings.Contains(controller.Environment["APEXLOAD_DATABASE_URL"], "${APEXLOAD_LOCAL_POSTGRES_PASSWORD:-apexload-local-postgres}") ||
		!strings.Contains(controller.Environment["APEXLOAD_REDIS_URL"], "${APEXLOAD_LOCAL_REDIS_PASSWORD:-apexload-local-redis}") {
		t.Fatal("controller backend URLs must use the overridable local-development credentials")
	}
	for _, dependency := range []string{"postgres", "redis"} {
		if controller.DependsOn[dependency].Condition != "service_healthy" {
			t.Fatalf("controller dependency %s must be health-gated", dependency)
		}
	}
	if _, enabled := controller.Environment["APEXLOAD_CONTROLLER_HA"]; enabled {
		t.Fatal("single-controller local data-platform override must not enable controller HA")
	}
	assertNetworks("controller", controller, "default", "data-platform")

	if postgres.Image != "postgres:16-alpine" || redis.Image != "redis:7-alpine" {
		t.Fatalf("data-platform images are not pinned to the supported majors: postgres=%q redis=%q", postgres.Image, redis.Image)
	}
	if !contains(postgres.Volumes, "postgres-data:/var/lib/postgresql/data") || !contains(redis.Volumes, "redis-data:/data") {
		t.Fatalf("data-platform persistence is incomplete: postgres=%v redis=%v", postgres.Volumes, redis.Volumes)
	}
	if _, ok := model.Volumes["postgres-data"]; !ok {
		t.Fatal("data-platform Compose override must declare postgres-data")
	}
	if _, ok := model.Volumes["redis-data"]; !ok {
		t.Fatal("data-platform Compose override must declare redis-data")
	}
	if len(postgres.Ports) != 0 || len(redis.Ports) != 0 {
		t.Fatalf("data services must not publish host ports: postgres=%v redis=%v", postgres.Ports, redis.Ports)
	}
	assertNetworks("postgres", postgres, "data-platform")
	assertNetworks("redis", redis, "data-platform")
	backendNetwork, ok := model.Networks["data-platform"]
	if !ok || !backendNetwork.Internal {
		t.Fatal("data-platform network must be declared internal")
	}

	postgresHealth := strings.Join(postgres.Healthcheck.Test, " ")
	for _, required := range []string{
		"PGPASSWORD=\"$$POSTGRES_PASSWORD\"",
		"psql -h postgres",
		"-U \"$$POSTGRES_USER\"",
		"-d \"$$POSTGRES_DB\"",
		"SELECT 1",
	} {
		if !strings.Contains(postgresHealth, required) {
			t.Fatalf("PostgreSQL health check must authenticate over TCP; missing %q in %q", required, postgresHealth)
		}
	}
	if strings.Contains(postgresHealth, "pg_isready") {
		t.Fatal("PostgreSQL health check must verify authentication, not only server readiness")
	}
	redisHealth := strings.Join(redis.Healthcheck.Test, " ")
	if !strings.Contains(redisHealth, `redis-cli -h redis --no-auth-warning -a "$$REDIS_PASSWORD"`) {
		t.Fatalf("Redis health check must authenticate: %q", redisHealth)
	}
	if !strings.Contains(strings.Join(redis.Command, " "), `--requirepass "$$REDIS_PASSWORD"`) {
		t.Fatal("Redis service must require the configured password")
	}
}

func TestDockerComposeIncludesBrunoRunnerAndSharedEncryptedArtifacts(t *testing.T) {
	compose := readTextFile(t, "../../compose.yaml")
	for _, check := range []string{
		"bruno-artifacts-init:",
		`chown -R 65532:65532 /var/lib/apexload-bruno`,
		"condition: service_completed_successfully",
		"bruno-runner:",
		"context: ./runners/bruno",
		"network_mode: service:generator",
		"APEXLOAD_BRUNO_RUNNER_URL: http://127.0.0.1:8097",
		"APEXLOAD_BRUNO_ARTIFACT_ROOT: /var/lib/apexload-bruno",
		"bruno-artifacts:/var/lib/apexload-bruno",
	} {
		if !strings.Contains(compose, check) {
			t.Fatalf("compose.yaml missing Bruno runtime check %q", check)
		}
	}
}

func TestDockerComposeBrunoArtifactInitializationFailsClosed(t *testing.T) {
	type composeDependency struct {
		Condition string `yaml:"condition"`
	}
	var model struct {
		Services map[string]yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(readTextFile(t, "../../compose.yaml")), &model); err != nil {
		t.Fatalf("parse compose.yaml: %v", err)
	}

	initNode, ok := model.Services["bruno-artifacts-init"]
	if !ok {
		t.Fatal("compose.yaml must define bruno-artifacts-init")
	}
	var initService struct {
		Command []string `yaml:"command"`
	}
	if err := initNode.Decode(&initService); err != nil {
		t.Fatalf("decode bruno-artifacts-init: %v", err)
	}
	if len(initService.Command) != 3 || initService.Command[0] != "sh" || initService.Command[1] != "-ec" {
		t.Fatalf("Bruno artifact initialization must run with shell errexit enabled; command=%q", initService.Command)
	}
	command := initService.Command[2]
	for _, required := range []string{
		`if [ "$(stat -c '%u:%g' /var/lib/apexload-bruno)" != "65532:65532" ]; then`,
		"chown -R 65532:65532 /var/lib/apexload-bruno",
		"fi; chmod 0700 /var/lib/apexload-bruno",
	} {
		if !strings.Contains(command, required) {
			t.Fatalf("Bruno artifact initialization command missing %q: %q", required, command)
		}
	}
	for _, serviceName := range []string{"controller", "generator"} {
		serviceNode, ok := model.Services[serviceName]
		if !ok {
			t.Fatalf("compose.yaml must define %s", serviceName)
		}
		var service struct {
			DependsOn map[string]composeDependency `yaml:"depends_on"`
		}
		if err := serviceNode.Decode(&service); err != nil {
			t.Fatalf("decode %s: %v", serviceName, err)
		}
		dependency, ok := service.DependsOn["bruno-artifacts-init"]
		if !ok || dependency.Condition != "service_completed_successfully" {
			t.Fatalf("%s must wait for successful Bruno artifact initialization", serviceName)
		}
	}
}

func TestHelmBrunoExecutionUsesLoopbackRunnerAndSharedEncryptedArtifacts(t *testing.T) {
	values := readTextFile(t, "../helm/apexload/values.yaml")
	generator := readTextFile(t, "../helm/apexload/templates/generator.yaml")
	controller := readTextFile(t, "../helm/apexload/templates/controller.yaml")
	for _, check := range []string{
		"enabled: false",
		"compatibilityVersion: \"3.5.2\"",
		"existingClaim:",
		"egressBrokerUrl:",
		"maxSessions: 256",
		"repository: apexload-bruno-runner",
	} {
		if !strings.Contains(values, check) {
			t.Fatalf("Helm values missing Bruno runtime check %q", check)
		}
	}
	for _, check := range []string{
		`fail "Bruno execution requires bruno.egressBrokerUrl`,
		"APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED",
		"APEXLOAD_BRUNO_RUNNER_URL",
		"http://127.0.0.1:8097",
		"name: bruno-runner",
		"APEXLOAD_BRUNO_MAX_SESSIONS",
		"APEXLOAD_BRUNO_MAX_JUMPS",
		"APEXLOAD_BRUNO_MAX_RUN_REQUEST_DEPTH",
		"APEXLOAD_BRUNO_MAX_PARALLEL_REQUESTS",
		"APEXLOAD_BRUNO_MAX_OUTPUT_BYTES",
		"APEXLOAD_BRUNO_MAX_STREAM_TIMEOUT_MS",
		"readOnly: true",
	} {
		if !strings.Contains(generator, check) {
			t.Fatalf("Helm generator template missing Bruno runtime check %q", check)
		}
	}
	for _, check := range []string{"APEXLOAD_BRUNO_ARTIFACT_ROOT", "name: bruno-artifacts", "existingClaim"} {
		if !strings.Contains(controller, check) {
			t.Fatalf("Helm controller template missing Bruno artifact check %q", check)
		}
	}
}

func TestProjectSecretsProvidersAreWiredForSupportedDeployments(t *testing.T) {
	helmValues := readTextFile(t, "../helm/apexload/values.yaml")
	helmSchema := readTextFile(t, "../helm/apexload/values.schema.json")
	helmConfig := readTextFile(t, "../helm/apexload/templates/configmap.yaml")
	helmController := readTextFile(t, "../helm/apexload/templates/controller.yaml")
	helmSecret := readTextFile(t, "../helm/apexload/templates/secret.yaml")
	for _, check := range []string{
		"projectSecrets:",
		"provider: database",
		"wrappingMasterKey:",
		"wrappingKeyVaultKeyId:",
		"azureKeyVaultApiVersion:",
	} {
		if !strings.Contains(helmValues, check) {
			t.Fatalf("Helm values missing Project Secrets setting %q", check)
		}
	}
	for _, check := range []string{`"database"`, `"azure_key_vault"`, `"azureKeyVaultApiVersion"`} {
		if !strings.Contains(helmSchema, check) {
			t.Fatalf("Helm schema missing Project Secrets setting %q", check)
		}
	}
	if !strings.Contains(helmValues, `azureKeyVaultApiVersion: "7.4"`) {
		t.Fatal("Helm values must default to the supported Azure Key Vault 7.4 data-plane API")
	}
	if !strings.Contains(helmSchema, `"pattern": "^7\\.[0-9]+$"`) {
		t.Fatal("Helm schema must accept Azure Key Vault data-plane API versions such as 7.4")
	}
	for _, check := range []string{
		"APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED",
		`eq .Values.projectSecrets.provider "azure_key_vault"`,
		"APEXLOAD_AZURE_KEY_VAULT_API_VERSION",
	} {
		if !strings.Contains(helmConfig, check) {
			t.Fatalf("Helm ConfigMap missing Project Secrets provider wiring %q", check)
		}
	}
	for _, check := range []string{
		`eq .Values.projectSecrets.provider "database"`,
		"APEXLOAD_WRAPPING_MASTER_KEY",
		"projectSecrets.wrappingMasterKey",
	} {
		if !strings.Contains(helmSecret, check) {
			t.Fatalf("Helm Secret template missing encrypted database provider wiring %q", check)
		}
	}
	for _, check := range []string{
		`eq .Values.projectSecrets.provider "database"`,
		"name: APEXLOAD_WRAPPING_MASTER_KEY",
		"key: APEXLOAD_WRAPPING_MASTER_KEY",
	} {
		if !strings.Contains(helmController, check) {
			t.Fatalf("Helm controller template missing required wrapping-key reference %q", check)
		}
	}

	compose := readTextFile(t, "../../compose.yaml")
	localController := readTextFile(t, "local/controller-deployment.yaml")
	onPremSecret := readTextFile(t, "onprem/secret.example.yaml")
	for name, content := range map[string]string{
		"compose":          compose,
		"local controller": localController,
		"on-prem Secret":   onPremSecret,
	} {
		if !strings.Contains(content, "APEXLOAD_WRAPPING_MASTER_KEY") {
			t.Fatalf("%s must configure the encrypted database Project Secrets wrapping key", name)
		}
	}

	aksConfig := readTextFile(t, "aks/configmap.yaml")
	aksGenerator := readTextFile(t, "aks/generator-statefulset.yaml")
	if !strings.Contains(aksConfig, `APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED: "true"`) {
		t.Fatal("AKS controller ConfigMap must enable the governed Azure Key Vault Project Secrets provider")
	}
	if strings.Contains(aksGenerator, "APEXLOAD_AZURE_KEY_VAULT_WRITE_ENABLED") {
		t.Fatal("AKS generators must never receive Azure Key Vault write authority")
	}

	// The EKS twin: the controller enables Secrets Manager rotation, and the
	// generator must never receive it. Generators execute user-supplied
	// scripts, so rotation authority there would let a script rewrite the
	// credentials every other script reads.
	eksConfig := readTextFile(t, "eks/configmap.yaml")
	eksGenerator := readTextFile(t, "eks/generator-statefulset.yaml")
	if !strings.Contains(eksConfig, `APEXLOAD_AWS_SECRETS_MANAGER_WRITE_ENABLED: "true"`) {
		t.Fatal("EKS controller ConfigMap must enable the governed AWS Secrets Manager Project Secrets provider")
	}
	if strings.Contains(eksGenerator, "APEXLOAD_AWS_SECRETS_MANAGER_WRITE_ENABLED") {
		t.Fatal("EKS generators must never receive AWS Secrets Manager write authority")
	}
	// The wrapping key must be pinned by ARN. An alias can be repointed at a
	// different key, which would strand every existing envelope.
	if !strings.Contains(eksConfig, "APEXLOAD_WRAPPING_KMS_KEY_ID: arn:aws:kms:") {
		t.Fatal("EKS ConfigMap must pin the KMS wrapping key by full ARN, never by alias")
	}

	irsa := readTextFile(t, "../aws/terraform/irsa.tf")
	controllerPolicy := regexp.MustCompile(`(?s)data "aws_iam_policy_document" "controller" \{.*?\n\}`).FindString(irsa)
	if !strings.Contains(controllerPolicy, "secretsmanager:PutSecretValue") {
		t.Fatal("Terraform must grant the controller Secrets Manager rotation authority")
	}
	generatorPolicy := regexp.MustCompile(`(?s)data "aws_iam_policy_document" "generator" \{.*?\n\}`).FindString(irsa)
	if strings.Contains(generatorPolicy, "secretsmanager:PutSecretValue") {
		t.Fatal("Terraform must keep generator Secrets Manager authority read-only")
	}

	identity := readTextFile(t, "../azure/terraform/identity.tf")
	controllerRole := regexp.MustCompile(`(?s)resource "azurerm_role_assignment" "workload_keyvault" \{.*?\n\}`).FindString(identity)
	if !strings.Contains(controllerRole, `role_definition_name = "Key Vault Secrets Officer"`) {
		t.Fatal("Terraform must grant the controller Key Vault secret-management authority")
	}
	generatorRole := regexp.MustCompile(`(?s)resource "azurerm_role_assignment" "generator_keyvault" \{.*?\n\}`).FindString(identity)
	if !strings.Contains(generatorRole, `role_definition_name = "Key Vault Secrets User"`) {
		t.Fatal("Terraform must keep generator Key Vault authority read-only")
	}
}

func TestProductionDeploymentsSecureDistributedSecretDelivery(t *testing.T) {
	for _, deployment := range []struct {
		name       string
		controller string
		generator  string
	}{
		{
			name:       "AKS",
			controller: readTextFile(t, "aks/configmap.yaml"),
			generator:  readTextFile(t, "aks/generator-statefulset.yaml"),
		},
		{
			name:       "EKS",
			controller: readTextFile(t, "eks/configmap.yaml"),
			generator:  readTextFile(t, "eks/generator-statefulset.yaml"),
		},
		{
			name:       "on-prem",
			controller: readTextFile(t, "onprem/configmap.yaml"),
			generator:  readTextFile(t, "onprem/generator-statefulset.yaml"),
		},
		{
			name:       "Helm",
			controller: readTextFile(t, "../helm/apexload/templates/configmap.yaml"),
			generator:  readTextFile(t, "../helm/apexload/templates/generator.yaml"),
		},
	} {
		t.Run(deployment.name, func(t *testing.T) {
			for _, check := range []string{
				"APEXLOAD_GRPC_TLS_CERT",
				"APEXLOAD_GRPC_TLS_KEY",
				"APEXLOAD_GRPC_TLS_CA",
			} {
				if !strings.Contains(deployment.controller, check) {
					t.Fatalf("controller deployment missing authenticated TLS setting %q", check)
				}
				if !strings.Contains(deployment.generator, check) {
					t.Fatalf("generator deployment missing authenticated TLS setting %q", check)
				}
			}
			if !strings.Contains(deployment.generator, "APEXLOAD_GRPC_AUTH_TOKEN") {
				t.Fatal("generator deployment missing the shared gRPC bearer-token reference")
			}
		})
	}
}

func TestHelmTracingIsExplicitlyOptInAndAvailableToBothProcesses(t *testing.T) {
	values := readTextFile(t, "../helm/apexload/values.yaml")
	schema := readTextFile(t, "../helm/apexload/values.schema.json")
	configMap := readTextFile(t, "../helm/apexload/templates/configmap.yaml")
	controller := readTextFile(t, "../helm/apexload/templates/controller.yaml")
	generator := readTextFile(t, "../helm/apexload/templates/generator.yaml")
	readme := readTextFile(t, "../helm/apexload/README.md")
	for _, check := range []string{"tracing:", "enabled: false", "protocol: http/protobuf", `actionSampleRatio: "0.01"`, `headersSecretName: ""`, "headersSecretKey: OTEL_EXPORTER_OTLP_HEADERS"} {
		if !strings.Contains(values, check) {
			t.Fatalf("Helm values missing tracing default %q", check)
		}
	}
	for _, check := range []string{`"tracing"`, `"headersSecretName"`, `"headersSecretKey"`, `"http/protobuf"`, `"parentbased_traceidratio"`} {
		if !strings.Contains(schema, check) {
			t.Fatalf("Helm values schema missing tracing constraint %q", check)
		}
	}
	for _, key := range []string{
		"APEXLOAD_OTEL_ENABLED",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_TRACES_SAMPLER",
		"OTEL_TRACES_SAMPLER_ARG",
		"APEXLOAD_OTEL_ACTION_SAMPLE_RATIO",
		"OTEL_RESOURCE_ATTRIBUTES",
	} {
		if !strings.Contains(configMap, key) {
			t.Fatalf("Helm ConfigMap missing tracing key %q", key)
		}
		if !strings.Contains(generator, "key: "+key) {
			t.Fatalf("Helm generator does not project tracing key %q", key)
		}
	}
	for _, check := range []string{"tracing.endpoint is required", "tracing.protocol must be http/protobuf or grpc", "tracing.headersSecretKey is required"} {
		if !strings.Contains(configMap, check) {
			t.Fatalf("Helm ConfigMap missing tracing validation %q", check)
		}
	}
	if !strings.Contains(controller, "envFrom:") || !strings.Contains(controller, ".Values.dataPlatform.configMapName") {
		t.Fatal("Helm controller must consume the tracing ConfigMap")
	}
	for workload, template := range map[string]string{"controller": controller, "generator": generator} {
		for _, check := range []string{"OTEL_EXPORTER_OTLP_HEADERS", ".Values.tracing.headersSecretName", ".Values.tracing.headersSecretKey", "secretKeyRef:"} {
			if !strings.Contains(template, check) {
				t.Fatalf("Helm %s missing secret-backed collector header projection %q", workload, check)
			}
		}
	}
	if strings.Contains(configMap, "OTEL_EXPORTER_OTLP_HEADERS") {
		t.Fatal("Helm ConfigMap must never contain collector authentication headers")
	}
	for _, check := range []string{"tracing.headersSecretName", "OTEL_EXPORTER_OTLP_HEADERS", "never copied into values or the shared ConfigMap"} {
		if !strings.Contains(readme, check) {
			t.Fatalf("Helm README missing secret-backed tracing guidance %q", check)
		}
	}
}

func TestLocalKubernetesUsesDNSFleetDiscovery(t *testing.T) {
	controller := readTextFile(t, "local/controller-deployment.yaml")
	for _, check := range []string{
		"APEXLOAD_GENERATORS",
		"dns://apexload-generator.apexload-local.svc.cluster.local:9090",
	} {
		if !strings.Contains(controller, check) {
			t.Fatalf("local controller manifest missing DNS discovery check %q", check)
		}
	}
	if strings.Contains(controller, "apexload-generator-0.apexload-generator") || strings.Contains(controller, "apexload-generator-1.apexload-generator") {
		t.Fatalf("local controller manifest must not hard-code generator StatefulSet ordinals")
	}

	service := readTextFile(t, "local/generator-service.yaml")
	for _, check := range []string{"clusterIP: None", "publishNotReadyAddresses: false"} {
		if !strings.Contains(service, check) {
			t.Fatalf("local generator Service missing %q", check)
		}
	}

	kind := readTextFile(t, "local-kind/kustomization.yaml")
	if !strings.Contains(kind, "../local") {
		t.Fatalf("local-kind overlay must inherit the DNS-discovery local base")
	}
}

func TestTerraformValidatesAKSGeneratorCapacity(t *testing.T) {
	variables := readTextFile(t, "../azure/terraform/variables.tf")
	main := readTextFile(t, "../azure/terraform/main.tf")
	aks := readTextFile(t, "../azure/terraform/aks.tf")

	for _, check := range []string{
		`variable "generator_keda_max_replicas"`,
		`variable "generator_pod_cpu_millicores"`,
		`variable "generator_pod_memory_mib"`,
		`variable "generator_node_reserved_fraction"`,
		"generator_schedulable_pods",
		"local.generator_schedulable_pods >= var.generator_keda_max_replicas",
	} {
		if !strings.Contains(variables+main+aks, check) {
			t.Fatalf("terraform generator capacity validation missing %q", check)
		}
	}

	keda := readTextFile(t, "aks/generator-keda-scaledobject.yaml")
	kedaMax, err := findYAMLInt(keda, "maxReplicaCount")
	if err != nil {
		t.Fatal(err)
	}
	tfKedaMax, err := findTerraformDefaultNumber(variables, "generator_keda_max_replicas")
	if err != nil {
		t.Fatal(err)
	}
	if tfKedaMax != kedaMax {
		t.Fatalf("terraform generator_keda_max_replicas = %d, want AKS KEDA maxReplicaCount %d", tfKedaMax, kedaMax)
	}

	statefulSet := readTextFile(t, "aks/generator-statefulset.yaml")
	cpu, err := findYAMLResourceRequest(statefulSet, "cpu")
	if err != nil {
		t.Fatal(err)
	}
	if tfCPU, err := findTerraformDefaultNumber(variables, "generator_pod_cpu_millicores"); err != nil {
		t.Fatal(err)
	} else if tfCPU != cpu {
		t.Fatalf("terraform generator_pod_cpu_millicores = %d, want AKS request %d", tfCPU, cpu)
	}

	memory, err := findYAMLResourceRequest(statefulSet, "memory")
	if err != nil {
		t.Fatal(err)
	}
	if tfMemory, err := findTerraformDefaultNumber(variables, "generator_pod_memory_mib"); err != nil {
		t.Fatal(err)
	} else if tfMemory != memory {
		t.Fatalf("terraform generator_pod_memory_mib = %d, want AKS request %d", tfMemory, memory)
	}
}

// TestTerraformValidatesEKSGeneratorCapacity is the AWS twin of the AKS check.
// KEDA will request more replicas than the node group can schedule; the surplus
// pods sit Pending and the load test silently runs under its declared
// concurrency, which looks like a product limit rather than a sizing mistake.
func TestTerraformValidatesEKSGeneratorCapacity(t *testing.T) {
	variables := readTextFile(t, "../aws/terraform/variables.tf")
	main := readTextFile(t, "../aws/terraform/main.tf")
	eks := readTextFile(t, "../aws/terraform/eks.tf")

	for _, check := range []string{
		`variable "generator_keda_max_replicas"`,
		`variable "generator_pod_cpu_millicores"`,
		`variable "generator_pod_memory_mib"`,
		`variable "generator_node_reserved_fraction"`,
		"generator_schedulable_pods",
		"local.generator_schedulable_pods >= var.generator_keda_max_replicas",
	} {
		if !strings.Contains(variables+main+eks, check) {
			t.Fatalf("terraform generator capacity validation missing %q", check)
		}
	}

	keda := readTextFile(t, "eks/generator-keda-scaledobject.yaml")
	kedaMax, err := findYAMLInt(keda, "maxReplicaCount")
	if err != nil {
		t.Fatal(err)
	}
	tfKedaMax, err := findTerraformDefaultNumber(variables, "generator_keda_max_replicas")
	if err != nil {
		t.Fatal(err)
	}
	if tfKedaMax != kedaMax {
		t.Fatalf("terraform generator_keda_max_replicas = %d, want EKS KEDA maxReplicaCount %d", tfKedaMax, kedaMax)
	}

	statefulSet := readTextFile(t, "eks/generator-statefulset.yaml")
	cpu, err := findYAMLResourceRequest(statefulSet, "cpu")
	if err != nil {
		t.Fatal(err)
	}
	if tfCPU, err := findTerraformDefaultNumber(variables, "generator_pod_cpu_millicores"); err != nil {
		t.Fatal(err)
	} else if tfCPU != cpu {
		t.Fatalf("terraform generator_pod_cpu_millicores = %d, want EKS request %d", tfCPU, cpu)
	}

	memory, err := findYAMLResourceRequest(statefulSet, "memory")
	if err != nil {
		t.Fatal(err)
	}
	if tfMemory, err := findTerraformDefaultNumber(variables, "generator_pod_memory_mib"); err != nil {
		t.Fatal(err)
	} else if tfMemory != memory {
		t.Fatalf("terraform generator_pod_memory_mib = %d, want EKS request %d", tfMemory, memory)
	}
}

// TestGeneratorNodePlacementMatchesProvisionedNodes pins the pair that makes
// generator scheduling work at all: the pod template's nodeSelector key/value
// must be a label Terraform actually puts on the generator nodes, and the taint
// reserving those nodes must have a matching toleration.
//
// A mismatch is silent and total — every generator pod stays Pending, which
// reads as a product failure rather than a one-line label typo.
func TestGeneratorNodePlacementMatchesProvisionedNodes(t *testing.T) {
	for _, deployment := range []struct {
		name         string
		statefulSet  string
		terraform    string
		labelPattern string
		taintPattern string
	}{
		{
			name:         "AKS",
			statefulSet:  "aks/generator-statefulset.yaml",
			terraform:    "../azure/terraform/aks.tf",
			labelPattern: `"apexload\.io/node-pool"\s*=\s*"generators"`,
			taintPattern: `apexload\.io/dedicated=generators:NoSchedule`,
		},
		{
			name:         "EKS",
			statefulSet:  "eks/generator-statefulset.yaml",
			terraform:    "../aws/terraform/eks.tf",
			labelPattern: `"apexload\.io/node-pool"\s*=\s*"generators"`,
			taintPattern: `key\s*=\s*"apexload\.io/dedicated"`,
		},
	} {
		t.Run(deployment.name, func(t *testing.T) {
			statefulSet := readTextFile(t, deployment.statefulSet)
			terraform := readTextFile(t, deployment.terraform)

			// What the pod demands.
			nodeSelector := regexp.MustCompile(`(?s)nodeSelector:\s*\n\s*apexload\.io/node-pool:\s*(\S+)`).FindStringSubmatch(statefulSet)
			if nodeSelector == nil {
				t.Fatalf("%s generator StatefulSet has no apexload.io/node-pool nodeSelector", deployment.name)
			}
			if nodeSelector[1] != "generators" {
				t.Fatalf("%s generator nodeSelector value = %q, want generators", deployment.name, nodeSelector[1])
			}

			// What Terraform actually labels the nodes with.
			if !regexp.MustCompile(deployment.labelPattern).MatchString(terraform) {
				t.Fatalf("%s Terraform does not label generator nodes apexload.io/node-pool=generators; every generator pod would stay Pending", deployment.name)
			}

			// The taint that reserves the nodes, and its toleration.
			if !regexp.MustCompile(deployment.taintPattern).MatchString(terraform) {
				t.Fatalf("%s Terraform does not taint generator nodes; controller and system pods would steal load-generation CPU", deployment.name)
			}
			if !strings.Contains(statefulSet, "apexload.io/dedicated") {
				t.Fatalf("%s generator StatefulSet has no toleration for the dedicated-node taint", deployment.name)
			}
		})
	}
}

// TestEKSRawApplyUsesRenderer keeps the overlay from being applied directly:
// the raw manifests carry CHANGE-ME values and ${PLACEHOLDER}s that only the
// render script substitutes.
func TestEKSRawApplyUsesRenderer(t *testing.T) {
	makefile := readTextFile(t, "../../Makefile")
	for _, check := range []string{
		"k8s-eks-render:",
		"./scripts/render-eks-manifests.sh",
		"k8s-eks-apply: k8s-eks-render",
	} {
		if !strings.Contains(makefile, check) {
			t.Fatalf("Makefile missing EKS render wiring %q", check)
		}
	}
	if strings.Contains(makefile, "k8s-eks-apply:\n\tkubectl apply -k deployments/k8s/eks") {
		t.Fatal("k8s-eks-apply must render before applying; the raw overlay still carries placeholders")
	}
}

func findYAMLInt(content string, key string) (int, error) {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*([0-9]+)\b`)
	match := re.FindStringSubmatch(content)
	if match == nil {
		return 0, fmt.Errorf("missing YAML integer %s", key)
	}
	return strconv.Atoi(match[1])
}

func findTerraformDefaultNumber(content string, variable string) (int, error) {
	re := regexp.MustCompile(`(?s)variable\s+"` + regexp.QuoteMeta(variable) + `"\s+\{.*?default\s+=\s+([0-9]+)`)
	match := re.FindStringSubmatch(content)
	if match == nil {
		return 0, fmt.Errorf("missing Terraform numeric default for %s", variable)
	}
	return strconv.Atoi(match[1])
}

func findYAMLResourceRequest(content string, name string) (int, error) {
	re := regexp.MustCompile(`(?s)resources:\s*\n\s*requests:\s*\n(?P<body>.*?)(?:\n\s*limits:)`)
	match := re.FindStringSubmatch(content)
	if match == nil {
		return 0, fmt.Errorf("missing resources.requests block")
	}
	body := match[1]
	valueRe := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `:\s*"?([^"\n]+)"?\s*$`)
	valueMatch := valueRe.FindStringSubmatch(body)
	if valueMatch == nil {
		return 0, fmt.Errorf("missing resource request %s", name)
	}
	return parseKubernetesResourceQuantity(valueMatch[1])
}

func parseKubernetesResourceQuantity(value string) (int, error) {
	value = strings.TrimSpace(value)
	switch {
	case strings.HasSuffix(value, "m"):
		return strconv.Atoi(strings.TrimSuffix(value, "m"))
	case strings.HasSuffix(value, "Gi"):
		n, err := strconv.Atoi(strings.TrimSuffix(value, "Gi"))
		return n * 1024, err
	case strings.HasSuffix(value, "Mi"):
		return strconv.Atoi(strings.TrimSuffix(value, "Mi"))
	default:
		n, err := strconv.Atoi(value)
		if err != nil {
			return 0, err
		}
		return n * 1000, nil
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
