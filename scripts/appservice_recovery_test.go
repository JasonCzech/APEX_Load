package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppServiceRollbackUsesFullRevisionWhenCommitIsShort(t *testing.T) {
	root := t.TempDir()
	tools := filepath.Join(root, "bin")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	prior := strings.Repeat("a", 40)
	deployed := strings.Repeat("b", 40)
	priorVersion := "1.2.2"
	deployedVersion := "1.2.3"
	priorBuildTime := "2026-07-15T12:34:56Z"
	deployedBuildTime := "2026-07-16T12:34:56Z"
	swapped := filepath.Join(root, "swapped")
	azLog := filepath.Join(root, "az.log")
	state := filepath.Join(root, "swap.state")
	if err := os.WriteFile(state, []byte("swap-completed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	curlScript := `#!/usr/bin/env bash
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
payload=''
case "$url" in
  https://production.example/api/v1/system/health)
    [[ -f "$FAKE_SWAPPED" ]] || exit 22
    payload="{\"status\":\"healthy\",\"version\":\"$FAKE_PRIOR_VERSION\",\"build_time\":\"$FAKE_PRIOR_BUILD_TIME\",\"commit\":\"${FAKE_PRIOR:0:7}\",\"revision\":\"$FAKE_PRIOR\",\"capacity_availability\":{\"live_available_generators\":1}}"
    ;;
  https://staging.example/api/v1/system/health)
    payload="{\"status\":\"healthy\",\"version\":\"$FAKE_PRIOR_VERSION\",\"build_time\":\"$FAKE_PRIOR_BUILD_TIME\",\"commit\":\"${FAKE_PRIOR:0:7}\",\"revision\":\"$FAKE_PRIOR\"}"
    ;;
  https://production.example/api/v1/system/readiness)
    payload='{"status":"ready"}'
    ;;
  https://production.example/)
    payload='<html><script src="/app.js"></script></html>'
    ;;
  https://production.example/app.js)
    payload='console.log("ready")'
    ;;
  *) exit 22 ;;
esac
if [[ -n "$output" ]]; then
  printf '%s' "$payload" > "$output"
else
  printf '%s' "$payload"
fi
`
	azScript := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_AZ_LOG"
touch "$FAKE_SWAPPED"
`
	timeoutScript := `#!/usr/bin/env bash
set -euo pipefail
while (( $# > 0 )); do
  case "$1" in
    --foreground|--signal=*|--kill-after=*) shift ;;
    *) shift; exec "$@" ;;
  esac
done
exit 2
`
	for name, content := range map[string]string{"curl": curlScript, "az": azScript, "timeout": timeoutScript} {
		path := filepath.Join(tools, name)
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("bash", "deploy/rollback-appservice.sh",
		"resource-group", "app", "staging", "https://production.example",
		prior, deployed, "https://staging.example", state,
		priorVersion, priorBuildTime, deployedVersion, deployedBuildTime,
	)
	command.Env = append(os.Environ(),
		"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_SWAPPED="+swapped,
		"FAKE_AZ_LOG="+azLog,
		"FAKE_PRIOR="+prior,
		"FAKE_PRIOR_VERSION="+priorVersion,
		"FAKE_PRIOR_BUILD_TIME="+priorBuildTime,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("state-aware App Service recovery failed: %v\n%s", err, output)
	}
	log, err := os.ReadFile(azLog)
	if err != nil || !strings.Contains(string(log), "deployment slot swap") {
		t.Fatalf("rollback did not execute a slot swap: %v\n%s", err, log)
	}
	stateBytes, err := os.ReadFile(state)
	if err != nil || strings.TrimSpace(string(stateBytes)) != "rolled-back" {
		t.Fatalf("rollback did not persist recovered state: %v %q", err, stateBytes)
	}
}

func TestAppServiceRollbackDistinguishesBuildsFromTheSameCommit(t *testing.T) {
	root := t.TempDir()
	tools := filepath.Join(root, "bin")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 40)
	priorVersion := "1.2.2"
	deployedVersion := "1.2.3"
	priorBuildTime := "2026-07-15T12:34:56Z"
	deployedBuildTime := "2026-07-16T12:34:56Z"
	swapped := filepath.Join(root, "swapped")
	azLog := filepath.Join(root, "az.log")
	state := filepath.Join(root, "swap.state")
	if err := os.WriteFile(state, []byte("swap-completed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	curlScript := `#!/usr/bin/env bash
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
version="$FAKE_DEPLOYED_VERSION"
build_time="$FAKE_DEPLOYED_BUILD_TIME"
case "$url" in
  https://production.example/api/v1/system/health)
    if [[ -f "$FAKE_SWAPPED" ]]; then
      version="$FAKE_PRIOR_VERSION"
      build_time="$FAKE_PRIOR_BUILD_TIME"
    fi
    payload="{\"status\":\"healthy\",\"version\":\"$version\",\"build_time\":\"$build_time\",\"commit\":\"${FAKE_REVISION:0:7}\",\"revision\":\"$FAKE_REVISION\",\"capacity_availability\":{\"live_available_generators\":1}}"
    ;;
  https://staging.example/api/v1/system/health)
    version="$FAKE_PRIOR_VERSION"
    build_time="$FAKE_PRIOR_BUILD_TIME"
    payload="{\"status\":\"healthy\",\"version\":\"$version\",\"build_time\":\"$build_time\",\"commit\":\"${FAKE_REVISION:0:7}\",\"revision\":\"$FAKE_REVISION\"}"
    ;;
  https://production.example/api/v1/system/readiness)
    payload='{"status":"ready"}'
    ;;
  https://production.example/)
    payload='<html><script src="/app.js"></script></html>'
    ;;
  https://production.example/app.js)
    payload='console.log("ready")'
    ;;
  *) exit 22 ;;
esac
if [[ -n "$output" ]]; then
  printf '%s' "$payload" > "$output"
else
  printf '%s' "$payload"
fi
`
	azScript := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_AZ_LOG"
touch "$FAKE_SWAPPED"
`
	timeoutScript := `#!/usr/bin/env bash
set -euo pipefail
while (( $# > 0 )); do
  case "$1" in
    --foreground|--signal=*|--kill-after=*) shift ;;
    *) shift; exec "$@" ;;
  esac
done
exit 2
`
	for name, content := range map[string]string{"curl": curlScript, "az": azScript, "timeout": timeoutScript} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("bash", "deploy/rollback-appservice.sh",
		"resource-group", "app", "staging", "https://production.example",
		revision, revision, "https://staging.example", state,
		priorVersion, priorBuildTime, deployedVersion, deployedBuildTime,
	)
	command.Env = append(os.Environ(),
		"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_SWAPPED="+swapped,
		"FAKE_AZ_LOG="+azLog,
		"FAKE_REVISION="+revision,
		"FAKE_PRIOR_VERSION="+priorVersion,
		"FAKE_PRIOR_BUILD_TIME="+priorBuildTime,
		"FAKE_DEPLOYED_VERSION="+deployedVersion,
		"FAKE_DEPLOYED_BUILD_TIME="+deployedBuildTime,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("same-commit metadata-aware recovery failed: %v\n%s", err, output)
	}
	log, err := os.ReadFile(azLog)
	if err != nil || !strings.Contains(string(log), "deployment slot swap") {
		t.Fatalf("rollback treated same-commit releases as identical: %v\n%s", err, log)
	}
}

func legacyActiveRunPreflightFailsClosed(t *testing.T) {
	root := t.TempDir()
	curlPath := filepath.Join(root, "curl")
	if err := os.WriteFile(curlPath, []byte(`#!/usr/bin/env bash
set -euo pipefail
[[ -z "${APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL:-}" ]] || {
  printf 'override approval leaked to curl\n' >&2
  exit 97
}
printf '{"total":%s,"tests":[]}' "$FAKE_ACTIVE_COUNT"
`), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		count       string
		wantSuccess bool
	}{
		{count: "0", wantSuccess: true},
		{count: "1"},
		{count: `"unknown"`},
	} {
		command := exec.Command("bash", "deploy/active-runs-preflight.sh", "https://controller.example")
		command.Env = append(os.Environ(),
			"PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"),
			"APEXLOAD_DEPLOY_API_KEY=secret",
			"APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL=",
			"FAKE_ACTIVE_COUNT="+test.count,
		)
		output, err := command.CombinedOutput()
		if test.wantSuccess && err != nil {
			t.Fatalf("zero-active preflight failed: %v\n%s", err, output)
		}
		if !test.wantSuccess && err == nil {
			t.Fatalf("unsafe active-run response %s passed:\n%s", test.count, output)
		}
	}

	approvalJSON := func(release, environment string, expiresAt time.Time, reason string) string {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{
			"schema_version": 1,
			"approval_id":    "CHG-12345",
			"release":        release,
			"environment":    environment,
			"expires_at":     expiresAt.UTC().Format(time.RFC3339),
			"reason":         reason,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	runOverride := func(approval string) ([]byte, error) {
		command := exec.Command("bash", "deploy/active-runs-preflight.sh", "https://controller.example")
		command.Env = append(os.Environ(),
			"PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"),
			"APEXLOAD_DEPLOY_API_KEY=secret",
			"FAKE_ACTIVE_COUNT=2",
			"APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL="+approval,
			"APEXLOAD_DEPLOY_RELEASE=v1.2.3",
			"APEXLOAD_DEPLOY_ENVIRONMENT=appservice-production",
			"APEXLOAD_DEPLOY_ACTOR=octocat",
			"APEXLOAD_DEPLOY_REPOSITORY=octo/apex",
			"APEXLOAD_DEPLOY_SERVER_URL=https://github.example",
			"APEXLOAD_DEPLOY_RUN_ID=9001",
			"APEXLOAD_DEPLOY_RUN_ATTEMPT=2",
		)
		return command.CombinedOutput()
	}

	reason := "do not print this confidential approval reason"
	validApproval := approvalJSON("v1.2.3", "appservice-production", time.Now().UTC().Add(time.Hour), reason)
	output, err := runOverride(validApproval)
	if err != nil {
		t.Fatalf("reviewed active-run override failed: %v\n%s", err, output)
	}
	for _, required := range []string{`"event":"active_run_override_accepted"`, `"approval_id":"CHG-12345"`, `"active_tests":2`, `"run_attempt":"2"`, "reviewed active-run override accepted"} {
		if !strings.Contains(string(output), required) {
			t.Errorf("active-run override audit missing %q:\n%s", required, output)
		}
	}
	for _, prohibited := range []string{reason, validApproval, "APEXLOAD_DEPLOY_API_KEY", "secret"} {
		if strings.Contains(string(output), prohibited) {
			t.Errorf("active-run override audit exposed protected content %q:\n%s", prohibited, output)
		}
	}

	for name, approval := range map[string]string{
		"wrong release": approvalJSON("v1.2.4", "appservice-production", time.Now().UTC().Add(time.Hour), reason),
		"expired":       approvalJSON("v1.2.3", "appservice-production", time.Now().UTC().Add(-time.Minute), reason),
	} {
		t.Run(name, func(t *testing.T) {
			output, err := runOverride(approval)
			if err == nil {
				t.Fatalf("unsafe reviewed override passed:\n%s", output)
			}
			if strings.Contains(string(output), approval) || strings.Contains(string(output), reason) {
				t.Fatalf("rejected override exposed its protected document:\n%s", output)
			}
		})
	}
}
