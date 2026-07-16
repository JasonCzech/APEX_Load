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
)

func TestWorkflowPolicyRejectsObsoleteAndUnreviewedActions(t *testing.T) {
	tests := []struct {
		name        string
		sha         string
		version     string
		approved    bool
		wantSuccess bool
		wantMessage string
	}{
		{
			name:        "reviewed current generation",
			sha:         "11bd71901bbe5b1630ceea73d27597364c9af683",
			version:     "v4.2.2",
			approved:    true,
			wantSuccess: true,
		},
		{
			name:        "obsolete generation remains blocked even if allowlisted",
			sha:         strings.Repeat("3", 40),
			version:     "v3.6.0",
			approved:    true,
			wantMessage: "obsolete actions/checkout generation",
		},
		{
			name:        "unreviewed immutable pin",
			sha:         strings.Repeat("4", 40),
			version:     "v4.3.0",
			wantMessage: "is not in .github/approved-actions.json",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755); err != nil {
				t.Fatal(err)
			}
			actions := []map[string]any{}
			if test.approved {
				actions = append(actions, map[string]any{
					"uses": "actions/checkout", "sha": test.sha, "version": test.version,
				})
			}
			allowlist, err := json.Marshal(map[string]any{"schema_version": 1, "actions": actions})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".github", "approved-actions.json"), allowlist, 0o600); err != nil {
				t.Fatal(err)
			}
			workflow := "name: test\non:\n  workflow_dispatch:\npermissions:\n  contents: read\njobs:\n  test:\n    runs-on: ubuntu-24.04\n    timeout-minutes: 1\n    steps:\n      - uses: actions/checkout@" + test.sha + " # " + test.version + "\n"
			if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "test.yml"), []byte(workflow), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("python3", "ci/validate-github-actions.py")
			command.Dir = "."
			command.Env = append(os.Environ(), "APEXLOAD_WORKFLOW_POLICY_ROOT="+root)
			output, commandErr := command.CombinedOutput()
			if test.wantSuccess && commandErr != nil {
				t.Fatalf("valid policy fixture failed: %v\n%s", commandErr, output)
			}
			if !test.wantSuccess && commandErr == nil {
				t.Fatalf("invalid policy fixture passed:\n%s", output)
			}
			if test.wantMessage != "" && !strings.Contains(string(output), test.wantMessage) {
				t.Fatalf("expected %q in policy output:\n%s", test.wantMessage, output)
			}
		})
	}
}

func TestAuthoritativeCIWorkflowContract(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	workflow := string(content)
	for _, required := range []string{
		"name: CI",
		"workflow_call:",
		"pull_request:",
		"push:",
		"merge_group:",
		"workflow_dispatch:",
		"fork-static:",
		"name: Fork static DSL validation",
		"github.event.pull_request.head.repo.id != github.event.repository.id",
		"github.event.pull_request.head.repo.id == github.event.repository.id",
		"github.event.pull_request.head.repo.full_name != github.repository",
		"github.event.pull_request.head.repo.full_name == github.repository",
		"'apexload-ci-unprivileged'",
		"jq empty -- \"${file}\"",
		"name: Required",
		"if: always()",
		"IS_UNTRUSTED_PR:",
		"expected_success=",
		"expected_skipped=",
		"go test",
		"-race -shuffle=on -count=1",
		"jms_servicebus_real",
		"npm run lint:a11y",
		"npm --prefix dashboard run e2e",
		"terraform -chdir=deployments/azure/terraform init -backend=false -input=false -lockfile=readonly",
		"scripts/ci/container-smoke.sh",
		"govulncheck@v1.1.4",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("CI workflow missing required contract fragment %q", required)
		}
	}
	if regexp.MustCompile(`(?m)^    paths(?:-ignore)?:`).MatchString(workflow) {
		t.Fatal("authoritative CI workflow must not use workflow-level path filters")
	}
	if strings.Contains(workflow, "@latest") {
		t.Fatal("authoritative CI workflow must not install floating @latest tools")
	}
	if strings.Contains(workflow, "CI_RUNNER_LABELS") {
		t.Fatal("authoritative pull-request CI must not select configurable trusted runner labels")
	}
}

func TestWorkflowPolicyRejectsForkRunnerRegressions(t *testing.T) {
	workflowBytes, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	approvedActions, err := os.ReadFile("../.github/approved-actions.json")
	if err != nil {
		t.Fatalf("read approved actions: %v", err)
	}
	workflow := string(workflowBytes)
	fullGuard := "    if: github.event_name != 'pull_request' || (github.event.pull_request.head.repo.id == github.event.repository.id && github.event.pull_request.head.repo.full_name == github.repository)\n"
	isolatedRunner := "    runs-on: ${{ github.server_url == 'https://github.com' && 'ubuntu-24.04' || 'apexload-ci-unprivileged' }}\n"

	tests := []struct {
		name        string
		workflow    string
		wantSuccess bool
		wantMessage string
	}{
		{name: "current contract", workflow: workflow, wantSuccess: true},
		{
			name:        "full lane loses fork guard",
			workflow:    strings.Replace(workflow, fullGuard, "", 1),
			wantMessage: "skip fork pull requests",
		},
		{
			name: "configurable trusted runner returns",
			workflow: strings.Replace(
				workflow,
				isolatedRunner,
				"    runs-on: ${{ fromJSON(vars.CI_RUNNER_LABELS || '[\\\"ubuntu-24.04\\\"]') }}\n",
				1,
			),
			wantMessage: "must not select configurable trusted runner labels",
		},
		{
			name:        "required aggregation loses skipped contract",
			workflow:    strings.Replace(workflow, "          expected_skipped=()\n", "          skipped=()\n", 1),
			wantMessage: "event-specific fork aggregation marker 'expected_skipped=()'",
		},
		{
			name: "fork static executes repository code",
			workflow: strings.Replace(
				workflow,
				"            jq empty -- \"${file}\"\n",
				"            go test ./pkg/dsl\n            jq empty -- \"${file}\"\n",
				1,
			),
			wantMessage: "fork-static must not execute contributor-controlled code",
		},
		{
			name: "fork static invokes package manager",
			workflow: strings.Replace(
				workflow,
				"            jq empty -- \"${file}\"\n",
				"            npm test\n            jq empty -- \"${file}\"\n",
				1,
			),
			wantMessage: "fork-static must not execute contributor-controlled code",
		},
		{
			name: "fork static invokes repository script",
			workflow: strings.Replace(
				workflow,
				"            jq empty -- \"${file}\"\n",
				"            ./scripts/validate.sh\n            jq empty -- \"${file}\"\n",
				1,
			),
			wantMessage: "fork-static must not execute contributor-controlled code",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			workflowDir := filepath.Join(root, ".github", "workflows")
			if err := os.MkdirAll(workflowDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".github", "approved-actions.json"), approvedActions, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workflowDir, "ci.yml"), []byte(test.workflow), 0o600); err != nil {
				t.Fatal(err)
			}

			command := exec.Command("python3", "ci/validate-github-actions.py")
			command.Dir = "."
			command.Env = append(os.Environ(), "APEXLOAD_WORKFLOW_POLICY_ROOT="+root)
			output, commandErr := command.CombinedOutput()
			if test.wantSuccess && commandErr != nil {
				t.Fatalf("valid fork policy failed: %v\n%s", commandErr, output)
			}
			if !test.wantSuccess && commandErr == nil {
				t.Fatalf("invalid fork policy passed:\n%s", output)
			}
			if test.wantMessage != "" && !strings.Contains(string(output), test.wantMessage) {
				t.Fatalf("expected %q in policy output:\n%s", test.wantMessage, output)
			}
		})
	}
}

func TestClientPackagingSkipsForkPullRequests(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/desktop.yml")
	if err != nil {
		t.Fatalf("read client packaging workflow: %v", err)
	}
	guard := "if: github.event_name != 'pull_request' || (github.event.pull_request.head.repo.id == github.event.repository.id && github.event.pull_request.head.repo.full_name == github.repository)"
	if got := strings.Count(string(content), guard); got != 2 {
		t.Fatalf("fork guard count = %d, want one guard on each of the two client packaging jobs", got)
	}
}

func TestRequiredAggregationEnforcesEventSpecificResults(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	marker := "        run: |\n"
	start := strings.LastIndex(string(content), marker)
	if start < 0 {
		t.Fatal("Required aggregation run block not found")
	}
	indented := strings.Split(string(content)[start+len(marker):], "\n")
	scriptLines := make([]string, 0, len(indented))
	for _, line := range indented {
		if line == "" {
			scriptLines = append(scriptLines, "")
			continue
		}
		if !strings.HasPrefix(line, "          ") {
			t.Fatalf("unexpected content after Required run block: %q", line)
		}
		scriptLines = append(scriptLines, strings.TrimPrefix(line, "          "))
	}
	script := strings.Join(scriptLines, "\n")

	fullJobs := []string{
		"workflow-policy", "go-quality", "go-tests", "build", "protocol-tags",
		"dashboard", "deployment-artifacts", "containers", "govulncheck", "codeql",
	}
	results := func(defaultResult string) map[string]map[string]string {
		out := map[string]map[string]string{}
		for _, job := range append([]string{"fork-static", "dependency-review"}, fullJobs...) {
			out[job] = map[string]string{"result": defaultResult}
		}
		return out
	}

	fork := results("skipped")
	fork["fork-static"]["result"] = "success"
	sameRepoPR := results("success")
	sameRepoPR["fork-static"]["result"] = "skipped"
	mainPush := results("success")
	mainPush["fork-static"]["result"] = "skipped"
	mainPush["dependency-review"]["result"] = "skipped"
	unexpectedSkip := results("success")
	unexpectedSkip["fork-static"]["result"] = "skipped"
	unexpectedSkip["go-quality"]["result"] = "skipped"
	forkRanFullLane := results("skipped")
	forkRanFullLane["fork-static"]["result"] = "success"
	forkRanFullLane["build"]["result"] = "success"

	tests := []struct {
		name        string
		isPR        bool
		isUntrusted bool
		results     map[string]map[string]string
		wantSuccess bool
	}{
		{name: "fork static only", isPR: true, isUntrusted: true, results: fork, wantSuccess: true},
		{name: "same repository pull request full CI", isPR: true, results: sameRepoPR, wantSuccess: true},
		{name: "main push full CI", results: mainPush, wantSuccess: true},
		{name: "trusted event cannot hide a skipped lane", isPR: true, results: unexpectedSkip},
		{name: "fork cannot execute a full lane", isPR: true, isUntrusted: true, results: forkRanFullLane},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(test.results)
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command("bash", "-c", script)
			command.Env = append(
				os.Environ(),
				"IS_PULL_REQUEST="+strconv.FormatBool(test.isPR),
				"IS_UNTRUSTED_PR="+strconv.FormatBool(test.isUntrusted),
				"JOB_RESULTS="+string(encoded),
			)
			output, commandErr := command.CombinedOutput()
			if test.wantSuccess && commandErr != nil {
				t.Fatalf("valid aggregation failed: %v\n%s", commandErr, output)
			}
			if !test.wantSuccess && commandErr == nil {
				t.Fatalf("invalid aggregation passed:\n%s", output)
			}
		})
	}
}

func TestLegacyRequiredWorkflowsAreRetired(t *testing.T) {
	for _, name := range []string{
		"dashboard-quality.yml",
		"deploy-artifacts-validation.yml",
		"go-protocol-tags.yml",
		"govulncheck.yml",
	} {
		if _, err := os.Stat("../.github/workflows/" + name); !os.IsNotExist(err) {
			t.Errorf("legacy duplicate workflow %s still exists", name)
		}
	}
}

func TestContainerBuildIsSelfContainedAndProfiled(t *testing.T) {
	content, err := os.ReadFile("../Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfile := string(content)
	for _, required := range []string{
		"node:22.22.0-alpine3.23@sha256:",
		"golang:1.26.5-alpine3.24@sha256:",
		"FROM alpine:3.24@sha256:",
		"AS dashboard-builder",
		"npm --prefix dashboard ci --ignore-scripts",
		"COPY --from=dashboard-builder /src/pkg/api/static/ ./pkg/api/static/",
		"ARG GO_BUILD_TAGS=standard",
		"jms_solace_real,jms_servicebus_real,kafka_real,eventhubs_real",
		"unsupported GO_BUILD_TAGS profile",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile missing required contract fragment %q", required)
		}
	}
}
