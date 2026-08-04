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

func TestWorkflowPolicyRejectsTabIndentedCompositeAction(t *testing.T) {
	root := t.TempDir()
	workflowDir := filepath.Join(root, ".github", "workflows")
	actionDir := filepath.Join(root, ".github", "actions", "broken")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(actionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "approved-actions.json"), []byte(`{"schema_version":1,"actions":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	workflow := "name: test\non:\n  workflow_dispatch:\npermissions:\n  contents: read\njobs:\n  test:\n    runs-on: ubuntu-24.04\n    timeout-minutes: 1\n    steps: []\n"
	if err := os.WriteFile(filepath.Join(workflowDir, "test.yml"), []byte(workflow), 0o600); err != nil {
		t.Fatal(err)
	}
	action := "name: broken\ndescription: broken\ninputs:\n\tvalue:\n    required: false\nruns:\n  using: composite\n  steps: []\n"
	if err := os.WriteFile(filepath.Join(actionDir, "action.yml"), []byte(action), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("python3", "ci/validate-github-actions.py")
	command.Dir = "."
	command.Env = append(os.Environ(), "APEXLOAD_WORKFLOW_POLICY_ROOT="+root)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("tab-indented composite action passed policy:\n%s", output)
	}
	if !strings.Contains(string(output), "YAML indentation must not contain tab characters") {
		t.Fatalf("tab failure was not explicit:\n%s", output)
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
		"checkout-ref:",
		"ref: ${{ inputs.checkout-ref || github.sha }}",
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
		"github.server_url == 'https://github.com'",
		"runs-on: ubuntu-24.04",
		"'apexload-ci-unprivileged'",
		"runs-on: apexload-ci-fork-blocked",
		"Fork pull requests may change only documentation and static JSON examples.",
		"jq empty -- \"${file}\"",
		"-shellcheck=\"${RUNNER_TEMP}/bin/shellcheck\"",
		"--no-deps --user \"${wheel}\"",
		"./node_modules/.bin/playwright install --with-deps chromium",
		"./node_modules/.bin/playwright install chromium",
		"name: Required",
		"if: always()",
		"IS_UNTRUSTED_PR:",
		"expected_success=",
		"expected_skipped=",
		"go test",
		"-race -shuffle=on -count=1",
		"CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build",
		"jms_servicebus_real",
		"npm run lint:a11y",
		"npm audit --audit-level=high",
		"npm --prefix dashboard run e2e",
		"terraform -chdir=deployments/azure/terraform init -backend=false -input=false -lockfile=readonly",
		"docker run --rm --entrypoint /bin/promtool",
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

func TestWorkflowPolicyRejectsPromtoolEntrypointRegression(t *testing.T) {
	workflowBytes, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	approvedActions, err := os.ReadFile("../.github/approved-actions.json")
	if err != nil {
		t.Fatalf("read approved actions: %v", err)
	}
	workflow := strings.Replace(
		string(workflowBytes),
		"docker run --rm --entrypoint /bin/promtool",
		"docker run --rm",
		1,
	)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "approved-actions.json"), approvedActions, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yml"), []byte(workflow), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("python3", "ci/validate-github-actions.py")
	command.Dir = "."
	command.Env = append(os.Environ(), "APEXLOAD_WORKFLOW_POLICY_ROOT="+root)
	output, commandErr := command.CombinedOutput()
	if commandErr == nil {
		t.Fatalf("Prometheus image entrypoint regression passed policy:\n%s", output)
	}
	if !strings.Contains(string(output), "override the pinned Prometheus image entrypoint") {
		t.Fatalf("promtool entrypoint failure was not explicit:\n%s", output)
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
	forkHostedRunner := "    runs-on: ubuntu-24.04\n"
	ghesForkBlockRunner := "    runs-on: apexload-ci-fork-blocked\n"

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
			name: "fork static loses changed-path boundary",
			workflow: strings.ReplaceAll(
				workflow,
				"disallowed+=(\"${file}\")",
				": # removed boundary rejection",
			),
			wantMessage: "static-only fork change boundary",
		},
		{
			name: "fork static gains GHES self-hosted fallback",
			workflow: strings.Replace(
				workflow,
				forkHostedRunner,
				isolatedRunner,
				1,
			),
			wantMessage: "GitHub.com hosted runner with no GHES self-hosted fallback",
		},
		{
			name: "GHES fork block gains a real runner label",
			workflow: strings.Replace(
				workflow,
				ghesForkBlockRunner,
				"    runs-on: apexload-ci-unprivileged\n",
				1,
			),
			wantMessage: "deliberately unprovisioned fail-closed runner label",
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

func TestForkStaticBoundaryRejectsExecutableChanges(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	script := workflowStepRunScript(t, string(content), "Enforce the static-only fork change boundary")

	tests := []struct {
		name        string
		basePath    string
		path        string
		content     string
		rename      bool
		symlinkTo   string
		wantSuccess bool
	}{
		{name: "Markdown documentation", path: "docs/fork-review.md", content: "reviewed docs\n", wantSuccess: true},
		{name: "static JSON example", path: "examples/fork-review.json", content: "{}\n", wantSuccess: true},
		{name: "Go source", path: "pkg/example/review.go", content: "package example\n"},
		{name: "dashboard source", path: "dashboard/src/review.ts", content: "export const review = true\n"},
		{name: "workflow source", path: ".github/workflows/review.yml", content: "name: review\n"},
		{name: "JSON outside static examples", path: "dashboard/review.json", content: "{}\n"},
		{name: "workflow renamed to Markdown", basePath: ".github/workflows/ci.yml", path: "docs/renamed-workflow.md", content: "name: CI\n", rename: true},
		{name: "Markdown symlink", path: "docs/linked-review.md", symlinkTo: "../README.md"},
		{name: "Markdown changed to symlink", basePath: "docs/type-change.md", path: "docs/type-change.md", content: "reviewed docs\n", symlinkTo: "../README.md"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			runGit(t, repository, "init", "-b", "main")
			runGit(t, repository, "config", "user.email", "ci@example.invalid")
			runGit(t, repository, "config", "user.name", "CI")
			if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("base\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.basePath != "" {
				basePath := filepath.Join(repository, filepath.FromSlash(test.basePath))
				if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(basePath, []byte(test.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runGit(t, repository, "add", ".")
			runGit(t, repository, "commit", "-m", "base")
			base := strings.TrimSpace(runGit(t, repository, "rev-parse", "HEAD"))

			path := filepath.Join(repository, filepath.FromSlash(test.path))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if test.rename {
				from := filepath.Join(repository, filepath.FromSlash(test.basePath))
				if err := os.Rename(from, path); err != nil {
					t.Fatal(err)
				}
			} else if test.symlinkTo != "" {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if err := os.Symlink(test.symlinkTo, path); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runGit(t, repository, "add", "-A")
			runGit(t, repository, "commit", "-m", "change")
			head := strings.TrimSpace(runGit(t, repository, "rev-parse", "HEAD"))

			command := exec.Command("bash", "-c", script)
			command.Dir = repository
			command.Env = append(os.Environ(), "BASE_SHA="+base, "HEAD_SHA="+head)
			output, commandErr := command.CombinedOutput()
			if test.wantSuccess && commandErr != nil {
				t.Fatalf("allowed fork change failed: %v\n%s", commandErr, output)
			}
			if !test.wantSuccess && commandErr == nil {
				t.Fatalf("executable fork change passed:\n%s", output)
			}
		})
	}
}

func workflowStepRunScript(t *testing.T, workflow, stepName string) string {
	t.Helper()
	stepMarker := "      - name: " + stepName + "\n"
	stepStart := strings.Index(workflow, stepMarker)
	if stepStart < 0 {
		t.Fatalf("workflow step %q not found", stepName)
	}
	runMarker := "        run: |\n"
	runStart := strings.Index(workflow[stepStart:], runMarker)
	if runStart < 0 {
		t.Fatalf("workflow step %q has no multiline run block", stepName)
	}
	runStart += stepStart + len(runMarker)
	lines := strings.Split(workflow[runStart:], "\n")
	scriptLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			scriptLines = append(scriptLines, "")
			continue
		}
		if !strings.HasPrefix(line, "          ") {
			break
		}
		scriptLines = append(scriptLines, strings.TrimPrefix(line, "          "))
	}
	return strings.Join(scriptLines, "\n")
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func TestCredentialFreeClientPackagingValidatesForkPullRequests(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/desktop.yml")
	if err != nil {
		t.Fatalf("read client packaging workflow: %v", err)
	}
	guard := "if: github.event_name != 'pull_request' || (github.event.pull_request.head.repo.id == github.event.repository.id && github.event.pull_request.head.repo.full_name == github.repository)"
	if got := strings.Count(string(content), guard); got != 0 {
		t.Fatalf("credential-free client jobs still contain %d fork-only guards", got)
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
		"dashboard", "bruno-runner", "deployment-artifacts", "containers", "govulncheck", "codeql",
	}
	results := func(defaultResult string) map[string]map[string]string {
		out := map[string]map[string]string{}
		for _, job := range append([]string{"fork-static", "ghes-fork-block", "dependency-review"}, fullJobs...) {
			out[job] = map[string]string{"result": defaultResult}
		}
		return out
	}

	fork := results("skipped")
	fork["fork-static"]["result"] = "success"
	sameRepoPR := results("success")
	sameRepoPR["fork-static"]["result"] = "skipped"
	sameRepoPR["ghes-fork-block"]["result"] = "skipped"
	mainPush := results("success")
	mainPush["fork-static"]["result"] = "skipped"
	mainPush["ghes-fork-block"]["result"] = "skipped"
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
		{name: "GitHub.com fork static only", isPR: true, isUntrusted: true, results: fork, wantSuccess: true},
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

func TestArtifactUploadWrapperIncludesHiddenEvidence(t *testing.T) {
	content, err := os.ReadFile("../.github/actions/upload-artifact/action.yml")
	if err != nil {
		t.Fatalf("read artifact wrapper: %v", err)
	}
	action := string(content)
	if !strings.Contains(action, "include-hidden-files:\n    description:") ||
		!strings.Contains(action, "default: \"false\"") {
		t.Fatal("artifact wrapper must default to excluding hidden files")
	}
	if got := strings.Count(action, "include-hidden-files: ${{ inputs.include-hidden-files }}"); got != 2 {
		t.Fatalf("hidden evidence forwarding count = %d, want GitHub.com and GHES upload steps", got)
	}
}

func TestHiddenEvidenceUploadsOptInNarrowly(t *testing.T) {
	tests := []struct {
		path      string
		wantCount int
	}{
		{path: "../.github/workflows/deploy-aks.yml", wantCount: 1},
		{path: "../.github/workflows/deploy-appservice.yml", wantCount: 1},
		{path: "../docs/CICD_INTEGRATION.md", wantCount: 2},
		{path: "../docs/GITHUB_INTEGRATION.md", wantCount: 1},
	}
	for _, test := range tests {
		content, err := os.ReadFile(test.path)
		if err != nil {
			t.Fatalf("read %s: %v", test.path, err)
		}
		if got := strings.Count(string(content), "include-hidden-files: true"); got != test.wantCount {
			t.Errorf("%s hidden evidence opt-ins = %d, want %d", test.path, got, test.wantCount)
		}
	}

	readme, err := os.ReadFile("../.github/actions/apexload-run/README.md")
	if err != nil {
		t.Fatalf("read action README: %v", err)
	}
	if !strings.Contains(string(readme), "include-hidden-files: true") ||
		!strings.Contains(string(readme), "do not enable") {
		t.Fatal("action README must document the narrow hidden-evidence opt-in")
	}
}

func TestDashboardBrowserSmokeRequiresRenderedApplication(t *testing.T) {
	content, err := os.ReadFile("../dashboard/e2e/controller-smoke.spec.ts")
	if err != nil {
		t.Fatalf("read dashboard browser smoke: %v", err)
	}
	smoke := string(content)
	for _, marker := range []string{
		"page.on('pageerror'",
		"page.on('response'",
		"!response.ok()",
		"assetResourceTypes.has(response.request().resourceType())",
		"page.locator('#root')).not.toBeEmpty()",
		"page.getByRole('main')).toBeVisible()",
		"name: 'Dashboard'",
		"name: 'Settings'",
		"expect(pageErrors).toEqual([])",
	} {
		if !strings.Contains(smoke, marker) {
			t.Errorf("dashboard browser smoke missing rendered-application marker %q", marker)
		}
	}
}

func TestScheduledSecurityAuditsAllDashboardDependencies(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/security.yml")
	if err != nil {
		t.Fatalf("read scheduled security workflow: %v", err)
	}
	workflow := string(content)
	if !strings.Contains(workflow, "npm audit --audit-level=high") {
		t.Fatal("scheduled security workflow must audit the complete dashboard dependency graph")
	}
	if strings.Contains(workflow, "npm audit --omit=dev") {
		t.Fatal("scheduled security workflow must not omit vulnerable build and test tooling")
	}
}

func TestPrivilegedManualWorkflowsUseDefaultBranchRepositoryDispatch(t *testing.T) {
	tests := []struct {
		name      string
		filename  string
		events    []string
		contracts []string
	}{
		{
			name:     "Azure infrastructure",
			filename: "infra-azure.yml",
			events:   []string{"azure-infra-plan", "azure-infra-apply"},
			contracts: []string{
				`keys == ["environment"]`,
				`keys == ["environment", "plan_run_attempt", "plan_run_id"]`,
				"github.event.client_payload.plan_run_id",
				"github.event.client_payload.plan_run_attempt",
			},
		},
		{
			name:     "protected protocol integration",
			filename: "protocol-integration.yml",
			events:   []string{"protocol-integration"},
			contracts: []string{
				"schedule:",
				"keys == []",
				"CLIENT_PAYLOAD: ${{ toJSON(github.event.client_payload) }}",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join("../.github/workflows", test.filename))
			if err != nil {
				t.Fatal(err)
			}
			workflow := string(content)
			for _, required := range append(
				[]string{"repository_dispatch:", "ref: ${{ github.sha }}", "refs/heads/main"},
				test.contracts...,
			) {
				if !strings.Contains(workflow, required) {
					t.Errorf("%s missing privileged default-branch contract %q", test.filename, required)
				}
			}
			for _, eventType := range test.events {
				if !strings.Contains(workflow, "- "+eventType) {
					t.Errorf("%s missing repository_dispatch event %q", test.filename, eventType)
				}
			}
			if strings.Contains(workflow, "workflow_dispatch:") {
				t.Errorf("%s permits branch-selected privileged workflow code", test.filename)
			}
		})
	}
}

func TestProtectedScheduledWorkflowsRequireTrustedMain(t *testing.T) {
	approvedActions, err := os.ReadFile("../.github/approved-actions.json")
	if err != nil {
		t.Fatalf("read approved actions: %v", err)
	}
	tests := []struct {
		name     string
		filename string
		job      string
	}{
		{name: "protocol integration", filename: "protocol-integration.yml", job: "live-protocol"},
		{name: "scheduled govulncheck", filename: "security.yml", job: "govulncheck"},
		{name: "scheduled dashboard audit", filename: "security.yml", job: "dashboard-audit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workflowBytes, err := os.ReadFile(filepath.Join("../.github/workflows", test.filename))
			if err != nil {
				t.Fatalf("read workflow: %v", err)
			}
			workflow := string(workflowBytes)
			jobMarker := "  " + test.job + ":\n"
			jobStart := strings.Index(workflow, jobMarker)
			if jobStart < 0 {
				t.Fatalf("job %q not found", test.job)
			}
			guard := "    if: github.ref == 'refs/heads/main'\n"
			guardStart := strings.Index(workflow[jobStart:], guard)
			if guardStart < 0 {
				t.Fatalf("job %q is missing trusted-main guard", test.job)
			}
			guardStart += jobStart
			workflow = workflow[:guardStart] + workflow[guardStart+len(guard):]

			root := t.TempDir()
			workflowDir := filepath.Join(root, ".github", "workflows")
			if err := os.MkdirAll(workflowDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".github", "approved-actions.json"), approvedActions, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workflowDir, test.filename), []byte(workflow), 0o600); err != nil {
				t.Fatal(err)
			}

			command := exec.Command("python3", "ci/validate-github-actions.py")
			command.Dir = "."
			command.Env = append(os.Environ(), "APEXLOAD_WORKFLOW_POLICY_ROOT="+root)
			output, commandErr := command.CombinedOutput()
			if commandErr == nil {
				t.Fatalf("branch-selected protected workflow passed policy:\n%s", output)
			}
			if !strings.Contains(string(output), "must restrict protected scheduled/manual runs") {
				t.Fatalf("trusted-main failure was not explicit:\n%s", output)
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
		"node:22.22.0-alpine3.23@sha256:e4bf2a82ad0a4037d28035ae71529873c069b13eb0455466ae0bc13363826e34",
		"golang:1.26.5-alpine3.24@sha256:",
		"FROM alpine:3.24@sha256:",
		"AS dashboard-builder",
		"npm --prefix dashboard ci --ignore-scripts",
		"COPY docs/API_USER_GUIDE.md ./docs/API_USER_GUIDE.md",
		"COPY docs/APPLICATION_USER_GUIDE.md ./docs/APPLICATION_USER_GUIDE.md",
		"COPY docs/ADMIN_USER_GUIDE.md ./docs/ADMIN_USER_GUIDE.md",
		"APEXLOAD_UX_VERSION=\"${VERSION}\" APEXLOAD_UX_BUILD_TIME=\"${BUILD_TIME}\"",
		"node_modules/playwright-core-go/package.json",
		"COPY --from=dashboard-builder /src/dashboard/node_modules/playwright-core-go/ /opt/playwright-driver/package/",
		"COPY --from=dashboard-builder /src/pkg/api/static/ ./pkg/api/static/",
		"ARG GO_BUILD_TAGS=standard",
		"jms_solace_real,jms_servicebus_real,kafka_real,eventhubs_real",
		"unsupported GO_BUILD_TAGS profile",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile missing required contract fragment %q", required)
		}
	}
	if strings.Contains(dockerfile, "npm install --prefix /opt/playwright-driver") {
		t.Fatal("Dockerfile must copy the lockfile-resolved Playwright driver instead of fetching it in the runtime stage")
	}
	for _, line := range strings.Split(dockerfile, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		if !strings.Contains(fields[1], "@sha256:") {
			t.Errorf("release Dockerfile base image is not digest-pinned: %s", line)
		}
	}
	dockerIgnore, err := os.ReadFile("../.dockerignore")
	if err != nil {
		t.Fatalf("read .dockerignore: %v", err)
	}
	if !regexp.MustCompile(`(?m)^pkg/api/static/$`).Match(dockerIgnore) {
		t.Fatal("Docker context must exclude stale embedded dashboard output")
	}
	codeowners, err := os.ReadFile("../.github/CODEOWNERS")
	if err != nil {
		t.Fatalf("read CODEOWNERS: %v", err)
	}
	if !regexp.MustCompile(`(?m)^/\.dockerignore\s+@JasonCzech$`).Match(codeowners) {
		t.Fatal("container context boundary must require repository-owner review")
	}
	if !regexp.MustCompile(`(?m)^/\.github/CODEOWNERS\s+@JasonCzech$`).Match(codeowners) {
		t.Fatal("CODEOWNERS must protect its own supply-chain review boundary")
	}

	packageJSON, err := os.ReadFile("../dashboard/package.json")
	if err != nil {
		t.Fatalf("read dashboard package.json: %v", err)
	}
	if !strings.Contains(string(packageJSON), `"playwright-core-go": "npm:playwright-core@1.57.0"`) {
		t.Fatal("dashboard lockfile inputs must pin the Go Playwright driver package")
	}
}
