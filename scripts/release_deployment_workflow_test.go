package scripts_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReleaseWorkflowBuildsAndPublishesVerifiedImmutableContract(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(content)
	for _, required := range []string{
		"uses: ./.github/workflows/ci.yml",
		"./scripts/release/verify-tag.sh",
		"environment: registry-release",
		"environment: client-release",
		"APEXLOAD_RELEASE_SIGNING_KEY_PEM",
		"apexctl_${RELEASE_VERSION}_${goos}_${goarch}${suffix}",
		"GO_BUILD_TAGS=${{ matrix.profile }}",
		"linux/amd64,linux/arm64",
		"release-manifest.json",
		"./scripts/release/sign-release.sh",
		"./scripts/release/publish-release.sh",
		"git tag --sign --force v1",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("release workflow missing required contract fragment %q", required)
		}
	}
	publisher, err := os.ReadFile("release/publish-release.sh")
	if err != nil {
		t.Fatalf("read release publisher: %v", err)
	}
	for _, required := range []string{"--draft=false", "verify-release.sh", "stored draft asset set"} {
		if !strings.Contains(string(publisher), required) {
			t.Errorf("release publisher missing required contract fragment %q", required)
		}
	}
	if strings.Contains(workflow, ":latest") {
		t.Fatal("release workflow must not publish a mutable latest image")
	}
	assertExternalActionsPinned(t, workflow)
}

func TestApplicationDeploymentsConsumeReleaseArtifactsWithoutBuilding(t *testing.T) {
	for _, name := range []string{"deploy-aks.yml", "deploy-appservice.yml", "publish-onprem.yml"} {
		content, err := os.ReadFile(filepath.Join("..", ".github", "workflows", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		workflow := string(content)
		for _, prohibited := range []string{"go build", "docker build", "npm run build", "az acr build"} {
			if strings.Contains(workflow, prohibited) {
				t.Errorf("%s rebuilds application artifacts via %q", name, prohibited)
			}
		}
		for _, required := range []string{"release:", "environment:", "verify-release.sh"} {
			if !strings.Contains(workflow, required) {
				t.Errorf("%s missing %q", name, required)
			}
		}
		assertExternalActionsPinned(t, workflow)
	}
}

func TestKubernetesDeploymentUsesDigestPostRendererAndRollback(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/deploy-aks.yml")
	if err != nil {
		t.Fatalf("read AKS workflow: %v", err)
	}
	workflow := string(content)
	for _, required := range []string{
		"kubernetes-preflight.sh",
		"kubernetes-deploy.sh",
		"rollback-helm.sh",
		"uses: ./.github/actions/apexload-run",
		"(failure() || cancelled()) && steps.helm-deploy.outcome != 'skipped'",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("AKS workflow missing %q", required)
		}
	}

	controller := "registry.example/apexload-controller@sha256:" + strings.Repeat("1", 64)
	generator := "registry.example/apexload-generator@sha256:" + strings.Repeat("2", 64)
	input := "image: old/apexload-controller:v1\n---\nimage: old/apexload-generator:v1\n"
	command := exec.Command("python3", "deploy/helm-digest-post-renderer.py")
	command.Env = append(os.Environ(),
		"APEXLOAD_CONTROLLER_IMAGE="+controller,
		"APEXLOAD_GENERATOR_IMAGE="+generator,
	)
	command.Stdin = strings.NewReader(input)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("run digest post-renderer: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), controller) || !strings.Contains(output.String(), generator) {
		t.Fatalf("digest post-renderer did not emit both immutable images:\n%s", output.String())
	}
}

func TestActiveRunOverrideIsProtectedEnvironmentBound(t *testing.T) {
	for _, test := range []struct {
		workflow    string
		environment string
		preflight   string
	}{
		{workflow: "deploy-aks.yml", environment: "aks-${{ inputs.environment }}", preflight: "./scripts/deploy/kubernetes-preflight.sh"},
		{workflow: "deploy-appservice.yml", environment: "appservice-${{ inputs.environment }}", preflight: "./scripts/deploy/active-runs-preflight.sh"},
	} {
		content, err := os.ReadFile(filepath.Join("..", ".github", "workflows", test.workflow))
		if err != nil {
			t.Fatalf("read %s: %v", test.workflow, err)
		}
		workflow := string(content)
		permissionsIndex := strings.Index(workflow, "\npermissions:")
		if permissionsIndex < 0 {
			t.Fatalf("%s has no top-level permissions boundary", test.workflow)
		}
		if strings.Contains(workflow[:permissionsIndex], "ACTIVE_RUN_OVERRIDE") || strings.Contains(strings.ToLower(workflow[:permissionsIndex]), "active-run-override") {
			t.Errorf("%s exposes the active-run override as a workflow_dispatch input", test.workflow)
		}
		for _, required := range []string{
			"APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL: ${{ secrets.APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL }}",
			"APEXLOAD_DEPLOY_ENVIRONMENT: " + test.environment,
			"APEXLOAD_DEPLOY_RELEASE: ${{ inputs.release }}",
			"APEXLOAD_DEPLOY_RUN_ID: ${{ github.run_id }}",
			test.preflight,
		} {
			if !strings.Contains(workflow, required) {
				t.Errorf("%s missing protected active-run override contract %q", test.workflow, required)
			}
		}
	}

	documentation, err := os.ReadFile("../docs/GITHUB_ACTIONS_RELEASE_AND_DEPLOYMENT.md")
	if err != nil {
		t.Fatalf("read deployment documentation: %v", err)
	}
	for _, required := range []string{
		"Reviewed active-run deployment override",
		"APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL",
		"Do not define that name as a repository",
		"no more than four hours",
		"never prints it or its reason",
	} {
		if !strings.Contains(string(documentation), required) {
			t.Errorf("deployment documentation missing active-run override requirement %q", required)
		}
	}
}

func TestObsoleteJenkinsAndDormantGitHubTemplatesAreRemoved(t *testing.T) {
	for _, path := range []string{
		"../ci/jenkins/Jenkinsfile.example",
		"../deployments/ci/github-actions.yml",
		"../deployments/ci/github-actions-aks.yml",
		"../deployments/ci/github-actions-appservice.yml",
		"../deployments/ci/github-actions-onprem-registry.yml",
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("obsolete CI/CD artifact still exists: %s", path)
		}
	}
}

func assertExternalActionsPinned(t *testing.T, workflow string) {
	t.Helper()
	uses := regexp.MustCompile(`(?m)^\s*(?:-\s*)?uses:\s*([^\s#]+)`).FindAllStringSubmatch(workflow, -1)
	sha := regexp.MustCompile(`@[0-9a-f]{40}$`)
	for _, match := range uses {
		reference := match[1]
		if strings.HasPrefix(reference, "./") {
			continue
		}
		if !sha.MatchString(reference) {
			t.Errorf("external action is not pinned to a full commit SHA: %s", reference)
		}
	}
}
