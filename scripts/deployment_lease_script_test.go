package scripts_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDeploymentLeasePreflightIsAtomicAndDoesNotLeakApprovalReason(t *testing.T) {
	root := t.TempDir()
	tools := map[string]string{
		"openssl": `#!/usr/bin/env bash
printf '%064d\n' 0
`,
		"timeout": `#!/usr/bin/env bash
[[ "${1:-}" == --version ]] && { printf 'timeout (GNU coreutils) 9.0\n'; exit 0; }
exit 99
`,
		"curl": `#!/usr/bin/env bash
set -euo pipefail
output=''
request=''
headers=''
while (( $# > 0 )); do
  case "$1" in
    --output) output="$2"; shift 2 ;;
    --data-binary) request="${2#@}"; shift 2 ;;
    --header) headers="${2#@}"; shift 2 ;;
    *) shift ;;
  esac
done
[[ -n "$output" && -n "$request" && -n "$headers" ]]
cp "$request" "$FAKE_REQUEST_CAPTURE"
cp "$headers" "$FAKE_HEADER_CAPTURE"
owner="$(jq -r '.owner_id' "$request")"
case "$FAKE_LEASE_RESULT" in
  acquired)
    jq -cn --arg owner "$owner" --argjson active "${FAKE_ACTIVE_COUNT:-0}" '{status:"acquired",active_tests:$active,lease:{active:true,owner_id:$owner,expires_at:"2099-01-01T00:00:00Z"}}' > "$output"
    printf 200
    ;;
  blocked)
    jq -cn --argjson active "${FAKE_ACTIVE_COUNT:-1}" '{status:"blocked",active_tests:$active,error:"active tests prevent deployment lease acquisition"}' > "$output"
    printf 409
    ;;
  *) printf '{"status":"failed"}' > "$output"; printf 503 ;;
esac
`,
	}
	for name, body := range tools {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	requestCapture := filepath.Join(root, "request.json")
	headerCapture := filepath.Join(root, "headers.txt")
	stateFile := filepath.Join(root, "state.json")
	run := func(result, count, approval, overrideAPIKey string) ([]byte, error) {
		cmd := exec.Command("bash", "deploy/active-runs-preflight.sh", "https://controller.example")
		cmd.Env = append(os.Environ(),
			"PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"),
			"APEXLOAD_DEPLOY_API_KEY=secret-api-key",
			"APEXLOAD_ACTIVE_RUN_OVERRIDE_API_KEY="+overrideAPIKey,
			"APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL="+approval,
			"APEXLOAD_DEPLOY_RELEASE=v1.2.3",
			"APEXLOAD_DEPLOY_ENVIRONMENT=appservice-production",
			"APEXLOAD_DEPLOY_ACTOR=octocat",
			"APEXLOAD_DEPLOY_REPOSITORY=octo/apex",
			"APEXLOAD_DEPLOY_SERVER_URL=https://github.example",
			"APEXLOAD_DEPLOY_RUN_ID=9001",
			"APEXLOAD_DEPLOY_RUN_ATTEMPT=2",
			"APEXLOAD_DEPLOYMENT_LEASE_STATE_FILE="+stateFile,
			"FAKE_REQUEST_CAPTURE="+requestCapture,
			"FAKE_HEADER_CAPTURE="+headerCapture,
			"FAKE_LEASE_RESULT="+result,
			"FAKE_ACTIVE_COUNT="+count,
		)
		return cmd.CombinedOutput()
	}

	if err := os.WriteFile(stateFile, []byte(`{"owner_id":"github:octo/apex:old:1","token":"stale"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := run("blocked", "2", "", ""); err == nil || !strings.Contains(string(output), "2 tests are executing") {
		t.Fatalf("unapproved active tests were not blocked: %v\n%s", err, output)
	}
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Fatalf("failed acquisition retained stale lease state: %v", err)
	}
	reason := "confidential emergency approval reason that must never leave the runner"
	approvalBytes, err := json.Marshal(map[string]any{
		"schema_version": 1, "approval_id": "CHG-12345", "release": "v1.2.3",
		"environment": "appservice-production", "expires_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339), "reason": reason,
	})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := run("acquired", "2", string(approvalBytes), ""); err == nil || !strings.Contains(string(output), "separately scoped active-run override API key") {
		t.Fatalf("reviewed override without separate authority was not rejected: %v\n%s", err, output)
	}
	output, err := run("acquired", "2", string(approvalBytes), "override-api-key")
	if err != nil {
		t.Fatalf("reviewed override acquisition failed: %v\n%s", err, output)
	}
	request, err := os.ReadFile(requestCapture)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(request), `"allow_active_tests":true`) || !strings.Contains(string(request), `"approval_id":"CHG-12345"`) {
		t.Fatalf("reviewed override not bound to atomic request: %s", request)
	}
	state, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	var leaseState struct {
		Token      string `json:"token"`
		TTLSeconds int    `json:"ttl_seconds"`
	}
	if err := json.Unmarshal(state, &leaseState); err != nil {
		t.Fatalf("decode lease state: %v", err)
	}
	if leaseState.Token == "" || strings.Contains(string(request), leaseState.Token) {
		t.Fatal("raw deployment lease token was empty or escaped into the request body")
	}
	if leaseState.TTLSeconds != 3600 || !strings.Contains(string(request), `"ttl_seconds":3600`) {
		t.Fatalf("deployment lease does not preserve the rollback-safe TTL: state=%s request=%s", state, request)
	}
	headers, err := os.ReadFile(headerCapture)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(headers), "X-APEXLoad-Deployment-Lease-Token: "+leaseState.Token) {
		t.Fatal("deployment lease token was not carried in the dedicated header file")
	}
	if !strings.Contains(string(headers), "Authorization: Bearer override-api-key") || strings.Contains(string(headers), "Authorization: Bearer secret-api-key") {
		t.Fatal("reviewed override did not authenticate with the separately scoped credential")
	}
	for _, prohibited := range []string{reason, string(approvalBytes), "secret-api-key", "override-api-key", "approval_reason"} {
		if strings.Contains(string(request), prohibited) || strings.Contains(string(output), prohibited) {
			t.Errorf("protected content %q escaped into request/output:\n%s\n%s", prohibited, request, output)
		}
	}
	info, err := os.Stat(stateFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lease state permissions = %v, %v", info, err)
	}
}

func TestDeploymentLeaseReleaseRejectsForeignStateAndStillScrubsLocalCredentials(t *testing.T) {
	root := t.TempDir()
	stateFile := filepath.Join(root, "state.json")
	state := `{"schema_version":1,"controller_url":"https://controller.example","owner_id":"github:octo/apex:9000:1","token":"` + strings.Repeat("a", 64) + `","ttl_seconds":1800}`
	if err := os.WriteFile(stateFile, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	curlMarker := filepath.Join(root, "curl-called")
	fakeCurl := "#!/usr/bin/env bash\nprintf called > \"$FAKE_CURL_MARKER\"\nexit 99\n"
	if err := os.WriteFile(filepath.Join(root, "curl"), []byte(fakeCurl), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeStat := "#!/usr/bin/env bash\n[[ \"${1:-}\" == -c && \"${2:-}\" == %a ]] || exit 2\nprintf '600\\n'\n"
	if err := os.WriteFile(filepath.Join(root, "stat"), []byte(fakeStat), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "deploy/deployment-lease.sh", "release")
	cmd.Env = append(os.Environ(),
		"PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"),
		"APEXLOAD_DEPLOYMENT_LEASE_STATE_FILE="+stateFile,
		"APEXLOAD_DEPLOY_API_KEY=secret-api-key",
		"GITHUB_REPOSITORY=octo/apex",
		"GITHUB_RUN_ID=9001",
		"GITHUB_RUN_ATTEMPT=1",
		"GITHUB_ENV="+filepath.Join(root, "github.env"),
		"FAKE_CURL_MARKER="+curlMarker,
	)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "lease state belongs to a different GitHub workflow attempt") {
		t.Fatalf("foreign lease state was not rejected: %v\n%s", err, output)
	}
	if _, err := os.Stat(curlMarker); !os.IsNotExist(err) {
		t.Fatalf("cleanup contacted controller with foreign credentials: %v", err)
	}
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Fatalf("foreign raw-token state was retained after terminal release failure: %v", err)
	}
}

func TestDeploymentLeaseReleaseScrubsLocalStateWhenControllerIsUnavailable(t *testing.T) {
	root := t.TempDir()
	stateFile := filepath.Join(root, "state.json")
	githubEnv := filepath.Join(root, "github.env")
	state := `{"schema_version":1,"controller_url":"https://controller.example","owner_id":"github:octo/apex:9001:1","token":"` + strings.Repeat("a", 64) + `","ttl_seconds":1800}`
	if err := os.WriteFile(stateFile, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeCurl := "#!/usr/bin/env bash\nexit 7\n"
	if err := os.WriteFile(filepath.Join(root, "curl"), []byte(fakeCurl), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeStat := "#!/usr/bin/env bash\n[[ \"${1:-}\" == -c && \"${2:-}\" == %a ]] || exit 2\nprintf '600\\n'\n"
	if err := os.WriteFile(filepath.Join(root, "stat"), []byte(fakeStat), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "deploy/deployment-lease.sh", "release")
	cmd.Env = append(os.Environ(),
		"PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"),
		"APEXLOAD_DEPLOYMENT_LEASE_STATE_FILE="+stateFile,
		"APEXLOAD_DEPLOY_API_KEY=secret-api-key",
		"GITHUB_REPOSITORY=octo/apex",
		"GITHUB_RUN_ID=9001",
		"GITHUB_RUN_ATTEMPT=1",
		"GITHUB_ENV="+githubEnv,
	)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "could not contact the controller deployment lease API") {
		t.Fatalf("unconfirmed remote release did not fail explicitly: %v\n%s", err, output)
	}
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Fatalf("failed remote release retained raw local lease state: %v", err)
	}
	content, err := os.ReadFile(githubEnv)
	if err != nil {
		t.Fatalf("read GitHub environment cleanup: %v", err)
	}
	if !strings.Contains(string(content), "APEXLOAD_DEPLOYMENT_LEASE_TOKEN=\n") {
		t.Fatalf("failed remote release did not clear the exported token:\n%s", content)
	}
}

func TestDeploymentLeaseTokenIsMaskedBeforeRequestConstruction(t *testing.T) {
	raw, err := os.ReadFile("deploy/active-runs-preflight.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	mask := strings.Index(script, "::add-mask::%s")
	request := strings.Index(script, "jq -cn")
	if mask < 0 || request < 0 || mask > request {
		t.Fatal("generated deployment credential is not masked before request construction")
	}
}

func TestDeploymentLeaseTokenIsNotPassedInProcessArguments(t *testing.T) {
	raw, err := os.ReadFile("deploy/active-runs-preflight.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, forbidden := range []string{
		`--arg token "$token"`,
		`--arg token "${token}"`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("deployment lease token remains exposed through a process argument: %s", forbidden)
		}
	}
	if !strings.Contains(script, `APEXLOAD_LEASE_STATE_TOKEN="$token"`) ||
		!strings.Contains(script, `env.APEXLOAD_LEASE_STATE_TOKEN`) {
		t.Fatal("deployment lease state no longer sources the token through the bounded jq environment")
	}
}

func TestDeploymentLeaseOverrideCredentialIsConditionallySeparated(t *testing.T) {
	raw, err := os.ReadFile("deploy/active-runs-preflight.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, required := range []string{
		`active_run_override_api_key="${APEXLOAD_ACTIVE_RUN_OVERRIDE_API_KEY:-}"`,
		`[[ "$active_run_override_api_key" != "$deploy_api_key" ]]`,
		`authorization_api_key="$active_run_override_api_key"`,
		`"$authorization_api_key" 'X-APEXLoad-Deployment-Lease-Token'`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("deployment override credential separation is missing %q", required)
		}
	}
}

func TestDeploymentLeasePreflightRejectsTTLThatCannotCoverFallbackRollback(t *testing.T) {
	command := exec.Command("bash", "deploy/active-runs-preflight.sh", "https://controller.example")
	command.Env = append(os.Environ(),
		"APEXLOAD_DEPLOY_API_KEY=deploy-api-key",
		"APEXLOAD_DEPLOY_RELEASE=v1.2.3",
		"APEXLOAD_DEPLOY_ENVIRONMENT=aks-production",
		"APEXLOAD_DEPLOY_ACTOR=octocat",
		"APEXLOAD_DEPLOY_REPOSITORY=octo/apex",
		"APEXLOAD_DEPLOY_SERVER_URL=https://github.example",
		"APEXLOAD_DEPLOY_RUN_ID=9001",
		"APEXLOAD_DEPLOY_RUN_ATTEMPT=2",
		"APEXLOAD_DEPLOYMENT_LEASE_TTL_SECONDS=3599",
	)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "must be 3600 seconds to cover bounded mutation and fallback rollback") {
		t.Fatalf("short deployment lease was not rejected: %v\n%s", err, output)
	}
}

func TestDeploymentWorkflowsBoundEveryLeaseHeldMutation(t *testing.T) {
	for _, name := range []string{"deploy-aks.yml", "deploy-appservice.yml"} {
		raw, err := os.ReadFile(filepath.Join("..", ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		workflow := string(raw)
		for _, required := range []string{
			`APEXLOAD_DEPLOYMENT_LEASE_TTL_SECONDS: "3600"`,
			"./scripts/deploy/deployment-lease.sh renew\n          timeout --foreground --signal=TERM --kill-after=60s 24m",
			"timeout: 10m",
			"timeout-minutes: 25",
			"timeout --foreground --signal=TERM --kill-after=15s 1m",
			"timeout --foreground --signal=TERM --kill-after=30s 23m",
			"- name: Release deployment lease\n        if: always()",
		} {
			if !strings.Contains(workflow, required) {
				t.Errorf("%s missing bounded lease contract %q", name, required)
			}
		}
		for _, recoveryContract := range []string{
			"./scripts/deploy/deployment-lease.sh renew; then",
			"candidate controller could not renew the lease; proceeding with captured rollback ownership",
		} {
			if !strings.Contains(workflow, recoveryContract) {
				t.Errorf("%s does not preserve rollback when the failed candidate API is unavailable: missing %q", name, recoveryContract)
			}
		}
		releaseIndex := strings.Index(workflow, "- name: Release deployment lease")
		uploadIndex := strings.Index(workflow, "- name: Upload deployment smoke evidence")
		if releaseIndex < 0 || uploadIndex < 0 || releaseIndex > uploadIndex {
			t.Errorf("%s does not release its lease before non-mutating evidence upload", name)
		}
	}
	raw, err := os.ReadFile("deploy/active-runs-preflight.sh")
	if err != nil {
		t.Fatal(err)
	}
	preflight := string(raw)
	if !strings.Contains(preflight, "rollback_safe_ttl_seconds=3600") ||
		!strings.Contains(preflight, "ttl_seconds=\"${APEXLOAD_DEPLOYMENT_LEASE_TTL_SECONDS:-$rollback_safe_ttl_seconds}\"") ||
		!strings.Contains(preflight, "GNU coreutils timeout is required") {
		t.Fatal("preflight does not enforce the rollback-safe lease and GNU timeout prerequisite")
	}
}

func TestAppServicePostMutationValidationRenewsAndRemainsBounded(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "deploy-appservice.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	mutation := strings.Index(workflow, "- name: Deploy and validate the selected App Service environment")
	renew := strings.Index(workflow, "- name: Renew deployment lease before post-deploy URL validation")
	validation := strings.Index(workflow, "- name: Revalidate slot-bound public URLs after deployment\n        timeout-minutes: 10")
	smokeRenew := strings.Index(workflow, "- name: Renew deployment lease before App Service execution smoke")
	if mutation < 0 || renew <= mutation || validation <= renew || smokeRenew <= validation {
		t.Fatalf("App Service post-mutation validation is not fenced and bounded in order: mutation=%d renew=%d validation=%d smoke_renew=%d", mutation, renew, validation, smokeRenew)
	}
}

func TestDeploymentWorkflowsReserveLeaseThroughWorstCaseRollback(t *testing.T) {
	const requiredHandoffMargin = 10 * time.Minute
	mutationPattern := regexp.MustCompile(`timeout --foreground --signal=TERM --kill-after=([0-9]+)s ([0-9]+)m`)
	leasePattern := regexp.MustCompile(`APEXLOAD_DEPLOYMENT_LEASE_TTL_SECONDS:\s*"([0-9]+)"`)
	rollbackPattern := regexp.MustCompile(`timeout-minutes:\s*([0-9]+)`)

	for _, test := range []struct {
		name           string
		rollbackMarker string
	}{
		{name: "deploy-aks.yml", rollbackMarker: "- name: Roll back after post-deploy gate failure"},
		{name: "deploy-appservice.yml", rollbackMarker: "- name: Swap back after post-deploy gate failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", ".github", "workflows", test.name))
			if err != nil {
				t.Fatal(err)
			}
			workflow := string(raw)
			leaseMatch := leasePattern.FindStringSubmatch(workflow)
			mutationMatch := mutationPattern.FindStringSubmatch(workflow)
			rollbackIndex := strings.Index(workflow, test.rollbackMarker)
			if len(leaseMatch) != 2 || len(mutationMatch) != 3 || rollbackIndex < 0 {
				t.Fatalf("could not read deployment timing contract from workflow")
			}
			rollbackMatch := rollbackPattern.FindStringSubmatch(workflow[rollbackIndex:])
			if len(rollbackMatch) != 2 {
				t.Fatalf("could not read rollback timeout from workflow")
			}

			leaseSeconds, err := strconv.Atoi(leaseMatch[1])
			if err != nil {
				t.Fatal(err)
			}
			killGraceSeconds, err := strconv.Atoi(mutationMatch[1])
			if err != nil {
				t.Fatal(err)
			}
			mutationMinutes, err := strconv.Atoi(mutationMatch[2])
			if err != nil {
				t.Fatal(err)
			}
			rollbackMinutes, err := strconv.Atoi(rollbackMatch[1])
			if err != nil {
				t.Fatal(err)
			}

			lease := time.Duration(leaseSeconds) * time.Second
			worstCaseRecovery := time.Duration(mutationMinutes)*time.Minute +
				time.Duration(killGraceSeconds)*time.Second +
				time.Duration(rollbackMinutes)*time.Minute
			if lease-worstCaseRecovery < requiredHandoffMargin {
				t.Fatalf(
					"lease %s does not cover mutation/termination/rollback %s with required handoff margin %s",
					lease,
					worstCaseRecovery,
					requiredHandoffMargin,
				)
			}
		})
	}
}

func TestRollbackSubphaseBudgetFitsInsideWorkflowTimeout(t *testing.T) {
	const (
		workflowRollbackTimeout = 25 * time.Minute
		renewalTimeout          = time.Minute
		recoveryScriptTimeout   = 23 * time.Minute
	)
	if renewalTimeout+recoveryScriptTimeout >= workflowRollbackTimeout {
		t.Fatal("rollback renewal and recovery bounds must finish strictly before the workflow step timeout")
	}

	for _, name := range []string{"deploy-aks.yml", "deploy-appservice.yml"} {
		raw, err := os.ReadFile(filepath.Join("..", ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		workflow := string(raw)
		for _, required := range []string{
			"timeout-minutes: 25",
			"timeout --foreground --signal=TERM --kill-after=15s 1m",
			"timeout --foreground --signal=TERM --kill-after=30s 23m",
		} {
			if !strings.Contains(workflow, required) {
				t.Errorf("%s missing rollback budget boundary %q", name, required)
			}
		}
	}
}
