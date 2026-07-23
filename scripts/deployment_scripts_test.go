package scripts_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerSmokeCrawlsDashboardAssetsForDeterministicMetadata(t *testing.T) {
	scriptBytes, err := os.ReadFile("ci/container-smoke.sh")
	if err != nil {
		t.Fatalf("read container smoke: %v", err)
	}
	script := string(scriptBytes)
	startMarker := "<<'PY'\n"
	start := strings.Index(script, startMarker)
	if start < 0 {
		t.Fatal("container smoke Python verifier not found")
	}
	start += len(startMarker)
	end := strings.Index(script[start:], "\nPY\n")
	if end < 0 {
		t.Fatal("container smoke Python verifier terminator not found")
	}
	verifier := script[start : start+end]

	tests := []struct {
		name        string
		lazyContent string
		wantSuccess bool
	}{
		{name: "metadata in lazy asset", lazyContent: `export const version = "0.0.0-ci-smoke"; export const built = "1970-01-01T00:00:00.000Z"`, wantSuccess: true},
		{name: "version absent", lazyContent: `export const built = "1970-01-01T00:00:00.000Z"`},
		{name: "build time absent", lazyContent: `export const version = "0.0.0-ci-smoke"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			assetDir := filepath.Join(root, "assets")
			if err := os.MkdirAll(assetDir, 0o755); err != nil {
				t.Fatal(err)
			}
			indexPath := filepath.Join(root, "index.html")
			if err := os.WriteFile(indexPath, []byte(`<script src="/assets/main.js"></script>`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(assetDir, "main.js"), []byte(`import("./lazy.js")`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(assetDir, "lazy.js"), []byte(test.lazyContent), 0o600); err != nil {
				t.Fatal(err)
			}

			server := httptest.NewServer(http.FileServer(http.Dir(root)))
			defer server.Close()
			command := exec.Command("python3", "-", server.URL, indexPath, "0.0.0-ci-smoke", "1970-01-01T00:00:00Z")
			command.Stdin = strings.NewReader(verifier)
			output, commandErr := command.CombinedOutput()
			if test.wantSuccess && commandErr != nil {
				t.Fatalf("dashboard verifier failed: %v\n%s", commandErr, output)
			}
			if !test.wantSuccess && commandErr == nil {
				t.Fatalf("dashboard verifier accepted missing metadata:\n%s", output)
			}
		})
	}
}

func TestDeploymentIdentityChecksUseFullRevision(t *testing.T) {
	tests := []struct {
		path     string
		required string
	}{
		{path: "ci/container-smoke.sh", required: ".revision == $revision and .commit == $commit"},
		{path: "deploy/controller-smoke.sh", required: ".revision == $revision and .commit == $commit"},
		{path: "deploy/kubernetes-deploy.sh", required: "jq -r '.revision // empty'"},
		{path: "deploy/appservice-deploy.sh", required: "jq -r '.revision // empty'"},
		{path: "deploy/rollback-appservice.sh", required: `.revision // "", .version // "", .build_time // ""`},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			content, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatalf("read script: %v", err)
			}
			script := string(content)
			if !strings.Contains(script, test.required) {
				t.Fatalf("script does not verify the full build revision via %q", test.required)
			}
			if strings.Contains(script, "jq -r '.commit // empty'") {
				t.Fatal("script still treats the display-only short commit as a full deployment identity")
			}
		})
	}
	container, err := os.ReadFile("ci/container-smoke.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"expected_version=0.0.0-ci-smoke",
		"expected_build_time=1970-01-01T00:00:00Z",
		".version == $version",
		".build_time == $build_time",
		"expected_dashboard_build_time",
		"asset_reference.findall(content)",
		"embedded dashboard does not contain the deterministic release build time",
		"embedded dashboard does not contain the deterministic release version",
	} {
		if !strings.Contains(string(container), required) {
			t.Errorf("container smoke test missing deterministic build metadata check %q", required)
		}
	}
}

func TestJavaVuserContainerSmokeVerifiesWritableWorkDirectory(t *testing.T) {
	content, err := os.ReadFile("ci/java-vuser-container-smoke.sh")
	if err != nil {
		t.Fatalf("read Java Vuser container smoke: %v", err)
	}
	script := string(content)
	for _, required := range []string{
		"APEXLOAD_JAVA_VUSER_WORK_DIR=/tmp",
		"APEXLOAD_JAVA_VUSER_RUNNER_ENABLED=true",
		"APEXLOAD_JAVA_VUSER_RUNNER_AUTH_TOKEN",
		"X-Apexload-Java-Runner-Token",
		"/v1/owner/claim",
		"owner_epoch",
		"unauthenticated protected request",
		"writable-workdir",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("Java Vuser container smoke is missing work-directory check %q", required)
		}
	}
}

func TestControllerSmokeValidatesOptionalSignedReleaseMetadata(t *testing.T) {
	root := t.TempDir()
	curlPath := filepath.Join(root, "curl")
	curl := `#!/usr/bin/env bash
set -euo pipefail
output=''
url=''
while (( $# > 0 )); do
  case "$1" in
    -o|--output) output="$2"; shift 2 ;;
    http://*|https://*) url="$1"; shift ;;
    *) shift ;;
  esac
done
case "$url" in
  https://controller.example/api/v1/system/health)
    payload="{\"status\":\"healthy\",\"version\":\"$FAKE_VERSION\",\"build_time\":\"$FAKE_BUILD_TIME\",\"commit\":\"${FAKE_REVISION:0:7}\",\"revision\":\"$FAKE_REVISION\",\"capacity_availability\":{\"live_available_generators\":1}}"
    ;;
  https://controller.example/api/v1/system/readiness)
    payload='{"status":"ready"}'
    ;;
  https://controller.example/)
    payload='<html><script src="/app.js"></script></html>'
    ;;
  https://controller.example/app.js)
    payload='console.log("ready")'
    ;;
  *) exit 22 ;;
esac
printf '%s' "$payload" > "$output"
`
	if err := os.WriteFile(curlPath, []byte(curl), 0o755); err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 40)
	version := "1.2.3-rc.1"
	buildTime := "2026-07-16T12:34:56Z"
	tests := []struct {
		name        string
		metadata    []string
		wantSuccess bool
	}{
		{name: "revision only rollback", wantSuccess: true},
		{name: "matching signed metadata", metadata: []string{version, buildTime}, wantSuccess: true},
		{name: "wrong version", metadata: []string{"1.2.4", buildTime}},
		{name: "wrong build time", metadata: []string{version, "2026-07-16T12:34:57Z"}},
		{name: "incomplete metadata", metadata: []string{version}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"deploy/controller-smoke.sh", "https://controller.example", revision}, test.metadata...)
			command := exec.Command("bash", args...)
			command.Env = append(os.Environ(),
				"PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"),
				"FAKE_REVISION="+revision,
				"FAKE_VERSION="+version,
				"FAKE_BUILD_TIME="+buildTime,
			)
			output, err := command.CombinedOutput()
			if test.wantSuccess && err != nil {
				t.Fatalf("controller smoke rejected valid metadata: %v\n%s", err, output)
			}
			if !test.wantSuccess && err == nil {
				t.Fatalf("controller smoke accepted invalid metadata:\n%s", output)
			}
		})
	}
}

func TestControllerSmokeRejectsCrossOriginRedirects(t *testing.T) {
	root := t.TempDir()
	curlPath := filepath.Join(root, "curl")
	curl := `#!/usr/bin/env bash
set -euo pipefail
location=false
max_redirs=''
while (( $# > 0 )); do
  case "$1" in
    --location) location=true; shift ;;
    --max-redirs) max_redirs="$2"; shift 2 ;;
    *) shift ;;
  esac
done
[[ "$location" == true && "$max_redirs" == 0 ]] || {
  echo 'redirect protection missing' >&2
  exit 90
}
exit 47
`
	if err := os.WriteFile(curlPath, []byte(curl), 0o755); err != nil {
		t.Fatal(err)
	}
	sleepPath := filepath.Join(root, "sleep")
	if err := os.WriteFile(sleepPath, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", "deploy/controller-smoke.sh",
		"https://controller.example", strings.Repeat("a", 40))
	command.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("controller smoke accepted a redirecting endpoint:\n%s", output)
	}
	if strings.Contains(string(output), "redirect protection missing") {
		t.Fatalf("controller smoke invoked curl without redirect blocking:\n%s", output)
	}
}

func TestRollbackScriptsBoundEverySlowExternalPhase(t *testing.T) {
	tests := []struct {
		path     string
		required []string
	}{
		{
			path: "deploy/rollback-helm.sh",
			required: []string{
				"readonly metadata_timeout=30s",
				"readonly helm_uninstall_timeout=5m",
				"readonly helm_rollback_timeout=7m",
				"readonly helm_wait_timeout=6m",
				"readonly rollout_timeout=150s",
				"readonly controller_smoke_timeout=6m",
				`--timeout "$helm_wait_timeout"`,
				`--timeout="$rollout_timeout"`,
				`"$controller_smoke_timeout"`,
			},
		},
		{
			path: "deploy/rollback-appservice.sh",
			required: []string{
				"readonly identity_timeout=75s",
				"readonly swap_timeout=10m",
				"readonly controller_smoke_timeout=8m",
				"--retry-max-time 60",
				`"$swap_timeout"`,
				`"$controller_smoke_timeout"`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			raw, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatal(err)
			}
			script := string(raw)
			for _, required := range test.required {
				if !strings.Contains(script, required) {
					t.Errorf("rollback script is missing bounded phase %q", required)
				}
			}
		})
	}
}

func TestNewReleaseDeploymentsRequireManifestMetadata(t *testing.T) {
	tests := []struct {
		path     string
		required []string
	}{
		{
			path: "deploy/kubernetes-deploy.sh",
			required: []string{
				`expected_version="${11:-}"`,
				`expected_build_time="${12:-}"`,
				`"$controller_url" "$expected_revision" "$expected_version" "$expected_build_time"`,
			},
		},
		{
			path: "deploy/appservice-deploy.sh",
			required: []string{
				`expected_version="${9:-}"`,
				`expected_build_time="${10:-}"`,
				`deployment_environment="${11:-}"`,
				`if [[ "$deployment_environment" == staging ]]`,
				`"$staging_url" "$expected_revision" "$expected_version" "$expected_build_time"`,
				`"$production_url" "$expected_revision" "$expected_version" "$expected_build_time"`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			content, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, required := range test.required {
				if !strings.Contains(string(content), required) {
					t.Errorf("new-release deployment contract missing %q", required)
				}
			}
		})
	}
}

func TestKubernetesPreflightDoesNotRequireSecretMutationAuthority(t *testing.T) {
	content, err := os.ReadFile("deploy/kubernetes-preflight.sh")
	if err != nil {
		t.Fatal(err)
	}
	preflight := string(content)
	if strings.Contains(preflight, "'create secrets'") {
		t.Fatal("deployment preflight still requires unnecessary Kubernetes Secret creation authority")
	}
	if !strings.Contains(preflight, "'get secrets'") {
		t.Fatal("deployment preflight no longer verifies externally provisioned Secret prerequisites")
	}
}

func TestAppServiceWebSocketSmokeKeepsCredentialsOutOfProcessArguments(t *testing.T) {
	appService, err := os.ReadFile("deploy/appservice-deploy.sh")
	if err != nil {
		t.Fatal(err)
	}
	webSocket, err := os.ReadFile("deploy/websocket-smoke.py")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(appService), "--token") || strings.Contains(string(webSocket), `add_argument("--token"`) {
		t.Fatal("deployment WebSocket credentials remain available as process arguments")
	}
	for _, required := range []string{"APEXLOAD_WEBSOCKET_SMOKE_TOKEN=", `os.environ.get("APEXLOAD_WEBSOCKET_SMOKE_TOKEN"`} {
		if !strings.Contains(string(appService)+string(webSocket), required) {
			t.Errorf("environment-only WebSocket credential contract missing %q", required)
		}
	}
}

func TestAppServiceStagingDeploymentNeverSwapsProduction(t *testing.T) {
	root := t.TempDir()
	tools := filepath.Join(root, "bin")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	azLog := filepath.Join(root, "az.log")
	curlLog := filepath.Join(root, "curl.log")
	appPackage := filepath.Join(root, "app.zip")
	if err := os.WriteFile(appPackage, []byte("verified package"), 0o600); err != nil {
		t.Fatal(err)
	}
	curl := `#!/usr/bin/env bash
set -euo pipefail
output=''
url=''
while (( $# > 0 )); do
  case "$1" in
    -o|--output) output="$2"; shift 2 ;;
    http://*|https://*) url="$1"; shift ;;
    *) shift ;;
  esac
done
printf '%s\n' "$url" >> "$FAKE_CURL_LOG"
case "$url" in
  https://staging.example/api/v1/system/health)
    payload="{\"status\":\"healthy\",\"version\":\"$FAKE_VERSION\",\"build_time\":\"$FAKE_BUILD_TIME\",\"commit\":\"${FAKE_REVISION:0:7}\",\"revision\":\"$FAKE_REVISION\",\"capacity_availability\":{\"live_available_generators\":1}}"
    ;;
  https://staging.example/api/v1/system/readiness)
    payload='{"status":"ready"}'
    ;;
  https://staging.example/)
    payload='<html><script src="/app.js"></script></html>'
    ;;
  https://staging.example/app.js)
    payload='console.log("ready")'
    ;;
  *) exit 22 ;;
esac
printf '%s' "$payload" > "$output"
`
	az := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_AZ_LOG"
`
	python := `#!/usr/bin/env bash
set -euo pipefail
printf 'websocket smoke passed\n'
`
	for name, content := range map[string]string{"curl": curl, "az": az, "python3": python} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	revision := strings.Repeat("a", 40)
	command := exec.Command("bash", "deploy/appservice-deploy.sh",
		"resource-group", "app", "staging", appPackage,
		revision, "https://staging.example", "",
		filepath.Join(root, "swap.state"), "1.2.3", "2026-07-16T12:34:56Z", "staging",
	)
	command.Env = append(os.Environ(),
		"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_AZ_LOG="+azLog,
		"FAKE_CURL_LOG="+curlLog,
		"FAKE_REVISION="+revision,
		"FAKE_VERSION=1.2.3",
		"FAKE_BUILD_TIME=2026-07-16T12:34:56Z",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("staging-only App Service deployment failed: %v\n%s", err, output)
	}
	azCalls, err := os.ReadFile(azLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(azCalls), "webapp deploy") {
		t.Fatalf("staging package was not deployed:\n%s", azCalls)
	}
	if strings.Contains(string(azCalls), "slot swap") {
		t.Fatalf("staging environment swapped production:\n%s", azCalls)
	}
	curlCalls, err := os.ReadFile(curlLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(curlCalls), "production.example") {
		t.Fatalf("staging-only deployment contacted production:\n%s", curlCalls)
	}
}

func TestAppServicePublicURLPreflightBindsStickySlotSettings(t *testing.T) {
	root := t.TempDir()
	tools := filepath.Join(root, "bin")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	az := `#!/usr/bin/env bash
set -euo pipefail
slot=production
while (( $# > 0 )); do
  case "$1" in
    --slot) slot="$2"; shift 2 ;;
    *) shift ;;
  esac
done
printf '%s\n' "$slot" >> "$FAKE_AZ_LOG"
if [[ "$slot" == production ]]; then
  printf '%s' "$FAKE_PRODUCTION_SETTINGS"
else
  printf '%s' "$FAKE_STAGING_SETTINGS"
fi
`
	if err := os.WriteFile(filepath.Join(tools, "az"), []byte(az), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name               string
		environment        string
		stagingURL         string
		productionURL      string
		stagingSettings    string
		productionSettings string
		wantSuccess        bool
		wantProductionRead bool
	}{
		{
			name:            "staging canonical equivalent",
			environment:     "staging",
			stagingURL:      "https://staging.example/controller",
			stagingSettings: `[{"name":"APEXLOAD_PUBLIC_URL","value":"https://STAGING.EXAMPLE:443/controller/","slotSetting":true}]`,
			wantSuccess:     true,
		},
		{
			name:               "production both slots",
			environment:        "production",
			stagingURL:         "https://staging.example",
			productionURL:      "https://production.example",
			stagingSettings:    `[{"name":"APEXLOAD_PUBLIC_URL","value":"https://staging.example/","slotSetting":true}]`,
			productionSettings: `[{"name":"APEXLOAD_PUBLIC_URL","value":"https://production.example","slotSetting":true}]`,
			wantSuccess:        true,
			wantProductionRead: true,
		},
		{
			name:            "missing setting",
			environment:     "staging",
			stagingURL:      "https://staging.example",
			stagingSettings: `[]`,
		},
		{
			name:            "non sticky setting",
			environment:     "staging",
			stagingURL:      "https://staging.example",
			stagingSettings: `[{"name":"APEXLOAD_PUBLIC_URL","value":"https://staging.example","slotSetting":false}]`,
		},
		{
			name:            "wrong staging origin",
			environment:     "staging",
			stagingURL:      "https://staging.example",
			stagingSettings: `[{"name":"APEXLOAD_PUBLIC_URL","value":"https://attacker.example","slotSetting":true}]`,
		},
		{
			name:               "wrong production origin",
			environment:        "production",
			stagingURL:         "https://staging.example",
			productionURL:      "https://production.example",
			stagingSettings:    `[{"name":"APEXLOAD_PUBLIC_URL","value":"https://staging.example","slotSetting":true}]`,
			productionSettings: `[{"name":"APEXLOAD_PUBLIC_URL","value":"https://other.example","slotSetting":true}]`,
			wantProductionRead: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "az.log")
			command := exec.Command(
				"bash",
				"deploy/appservice-public-url-preflight.sh",
				"resource-group",
				"app",
				"staging",
				test.stagingURL,
				test.environment,
				test.productionURL,
			)
			command.Env = append(os.Environ(),
				"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
				"FAKE_AZ_LOG="+logPath,
				"FAKE_STAGING_SETTINGS="+test.stagingSettings,
				"FAKE_PRODUCTION_SETTINGS="+test.productionSettings,
			)
			output, err := command.CombinedOutput()
			if test.wantSuccess && err != nil {
				t.Fatalf("public URL preflight rejected valid slot bindings: %v\n%s", err, output)
			}
			if !test.wantSuccess && err == nil {
				t.Fatalf("public URL preflight accepted an unsafe slot binding:\n%s", output)
			}
			logBytes, readErr := os.ReadFile(logPath)
			if readErr != nil {
				t.Fatalf("read fake Azure invocation log: %v", readErr)
			}
			readProduction := strings.Contains(string(logBytes), "production\n")
			if readProduction != test.wantProductionRead {
				t.Fatalf("production settings read = %t, want %t:\n%s", readProduction, test.wantProductionRead, logBytes)
			}
		})
	}
}

func TestHelmRollbackRejectsSameCommitWithWrongReleaseMetadata(t *testing.T) {
	root := t.TempDir()
	tools := filepath.Join(root, "bin")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	helm := `#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  status) printf '{"version":2}\n' ;;
  rollback) exit 0 ;;
  *) exit 90 ;;
esac
`
	kubectl := `#!/usr/bin/env bash
set -euo pipefail
case "$*" in
  *"rollout status"*) exit 0 ;;
  *"get deployment apexload-controller"*) printf '%s' "$FAKE_CONTROLLER_IMAGE" ;;
  *"get statefulset apexload-generator"*) printf '%s' "$FAKE_GENERATOR_IMAGE" ;;
  *"get nodes -o json"*) printf '{"items":[{"metadata":{"name":"node-a","labels":{"kubernetes.io/arch":"amd64"}}}]}' ;;
  *"get pods -l app.kubernetes.io/name=apexload-controller -o json"*)
    printf '{"items":[{"spec":{"nodeName":"node-a"},"status":{"containerStatuses":[{"name":"controller","imageID":"%s"}]}}]}' "$FAKE_CONTROLLER_IMAGE_ID"
    ;;
  *"get pods -l app.kubernetes.io/name=apexload-generator -o json"*)
    printf '{"items":[{"spec":{"nodeName":"node-a"},"status":{"containerStatuses":[{"name":"generator","imageID":"%s"}]}}]}' "$FAKE_GENERATOR_IMAGE_ID"
    ;;
  *) exit 91 ;;
esac
`
	curl := `#!/usr/bin/env bash
set -euo pipefail
output=''
url=''
while (( $# > 0 )); do
  case "$1" in
    -o|--output) output="$2"; shift 2 ;;
    http://*|https://*) url="$1"; shift ;;
    *) shift ;;
  esac
done
case "$url" in
  https://controller.example/api/v1/system/health)
    payload="{\"status\":\"healthy\",\"version\":\"$FAKE_VERSION\",\"build_time\":\"$FAKE_BUILD_TIME\",\"commit\":\"${FAKE_REVISION:0:7}\",\"revision\":\"$FAKE_REVISION\",\"capacity_availability\":{\"live_available_generators\":1}}"
    ;;
  https://controller.example/api/v1/system/readiness) payload='{"status":"ready"}' ;;
  https://controller.example/) payload='<html><script src="/app.js"></script></html>' ;;
  https://controller.example/app.js) payload='console.log("ready")' ;;
  *) exit 22 ;;
esac
printf '%s' "$payload" > "$output"
`
	timeout := `#!/usr/bin/env bash
set -euo pipefail
while (( $# > 0 )); do
  case "$1" in
    --foreground|--signal=*|--kill-after=*) shift ;;
    *) shift; exec "$@" ;;
  esac
done
exit 2
`
	for name, content := range map[string]string{"helm": helm, "kubectl": kubectl, "curl": curl, "timeout": timeout} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	revision := strings.Repeat("a", 40)
	controllerImage := "registry.example/apexload-controller@sha256:" + strings.Repeat("1", 64)
	generatorImage := "registry.example/apexload-generator@sha256:" + strings.Repeat("2", 64)
	controllerImageID := "registry.example/apexload-controller@sha256:" + strings.Repeat("3", 64)
	generatorImageID := "registry.example/apexload-generator@sha256:" + strings.Repeat("4", 64)
	controllerImageIDs := `{"amd64":"` + controllerImageID + `"}`
	generatorImageIDs := `{"amd64":"` + generatorImageID + `"}`
	run := func(version string) ([]byte, error) {
		command := exec.Command(
			"bash",
			"deploy/rollback-helm.sh",
			"apexload",
			"apexload-production",
			"1",
			"https://controller.example",
			revision,
			"1.2.2",
			"2026-07-15T12:34:56Z",
			controllerImage,
			generatorImage,
			controllerImageIDs,
			generatorImageIDs,
		)
		command.Env = append(os.Environ(),
			"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
			"FAKE_REVISION="+revision,
			"FAKE_VERSION="+version,
			"FAKE_BUILD_TIME=2026-07-15T12:34:56Z",
			"FAKE_CONTROLLER_IMAGE="+controllerImage,
			"FAKE_GENERATOR_IMAGE="+generatorImage,
			"FAKE_CONTROLLER_IMAGE_ID="+controllerImageID,
			"FAKE_GENERATOR_IMAGE_ID="+generatorImageID,
		)
		return command.CombinedOutput()
	}
	if output, err := run("1.2.3"); err == nil {
		t.Fatalf("rollback accepted the wrong release from the same commit:\n%s", output)
	}
	if output, err := run("1.2.2"); err != nil {
		t.Fatalf("rollback rejected the captured full release identity: %v\n%s", err, output)
	}
}

func TestNewReleaseDeploymentsRejectMissingManifestMetadata(t *testing.T) {
	root := t.TempDir()
	chart := filepath.Join(root, "apexload.tgz")
	values := filepath.Join(root, "values.yaml")
	appPackage := filepath.Join(root, "app.zip")
	for path, content := range map[string]string{
		chart:      "chart",
		values:     "dataPlatform: {}\n",
		appPackage: "zip",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	revision := strings.Repeat("a", 40)
	controllerImage := "registry.example/apexload-controller@sha256:" + strings.Repeat("1", 64)
	generatorImage := "registry.example/apexload-generator@sha256:" + strings.Repeat("2", 64)
	platforms := `{"amd64":"sha256:` + strings.Repeat("3", 64) + `","arm64":"sha256:` + strings.Repeat("4", 64) + `"}`
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "kubernetes",
			args: []string{
				"deploy/kubernetes-deploy.sh", "apexload", "apexload-production", chart, values,
				controllerImage, generatorImage, revision, "https://controller.example", platforms, platforms,
			},
		},
		{
			name: "appservice",
			args: []string{
				"deploy/appservice-deploy.sh", "resource-group", "app", "staging", appPackage,
				revision, "https://staging.example", "https://production.example", filepath.Join(root, "swap.state"),
				"", "", "staging",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("bash", test.args...)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("deployment accepted missing signed release metadata:\n%s", output)
			}
			if !strings.Contains(string(output), "expected release version is invalid") {
				t.Fatalf("deployment failed for the wrong reason:\n%s", output)
			}
		})
	}
}

func TestKubernetesDeploymentRejectsRenderedSecretBeforeKubectlValidation(t *testing.T) {
	root := t.TempDir()
	tools := filepath.Join(root, "bin")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	helmLog := filepath.Join(root, "helm.log")
	kubectlLog := filepath.Join(root, "kubectl.log")
	chart := filepath.Join(root, "apexload.tgz")
	if err := os.WriteFile(chart, []byte("test chart placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	helm := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_HELM_LOG"
case "${1:-}" in
  status) exit 1 ;;
  lint) exit 0 ;;
  template)
    grep -Eq '^[[:space:]]*createSecret:[[:space:]]*true[[:space:]]*$' "$FAKE_VALUES"
    printf '%s\n' \
      'apiVersion: v1' \
      'kind: Secret' \
      'metadata:' \
      '  name: forbidden-inline-secret' \
      '  namespace: apexload-production' \
      'type: Opaque'
    ;;
  *) exit 90 ;;
esac
`
	kubectl := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_KUBECTL_LOG"
exit 1
`
	for name, content := range map[string]string{"helm": helm, "kubectl": kubectl} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	controllerImage := "registry.example/apexload-controller@sha256:" + strings.Repeat("1", 64)
	generatorImage := "registry.example/apexload-generator@sha256:" + strings.Repeat("2", 64)
	platforms := `{"amd64":"sha256:` + strings.Repeat("3", 64) + `","arm64":"sha256:` + strings.Repeat("4", 64) + `"}`
	command := exec.Command("bash", "deploy/kubernetes-deploy.sh",
		"apexload", "apexload-production", chart,
		"testdata/kubernetes-create-secret-values.yaml",
		controllerImage, generatorImage, strings.Repeat("a", 40),
		"https://controller.example", platforms, platforms,
		"1.2.3", "2026-07-16T12:34:56Z",
	)
	command.Env = append(os.Environ(),
		"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_HELM_LOG="+helmLog,
		"FAKE_KUBECTL_LOG="+kubectlLog,
		"FAKE_VALUES=testdata/kubernetes-create-secret-values.yaml",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("deployment accepted a rendered Kubernetes Secret:\n%s", output)
	}
	if !strings.Contains(string(output), "must not render Kubernetes Secret resources") {
		t.Fatalf("deployment failed for the wrong reason:\n%s", output)
	}
	kubectlCalls, readErr := os.ReadFile(kubectlLog)
	if readErr != nil {
		t.Fatalf("read kubectl calls: %v", readErr)
	}
	for _, forbidden := range []string{" apply ", " diff "} {
		if strings.Contains(" "+string(kubectlCalls)+" ", forbidden) {
			t.Fatalf("deployment invoked kubectl%safter rendering a Secret:\n%s", forbidden, kubectlCalls)
		}
	}
}
