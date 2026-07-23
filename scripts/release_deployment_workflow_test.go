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
		"APEXLOAD_RELEASE_REGISTRY_USERNAME",
		"APEXLOAD_RELEASE_REGISTRY_PASSWORD",
		"apexctl_${RELEASE_VERSION}_${goos}_${goarch}${suffix}",
		"GO_BUILD_TAGS=${{ matrix.profile }}",
		"linux/amd64,linux/arm64",
		"release-manifest.json",
		"./scripts/release/sign-release.sh",
		"./scripts/release/publish-release.sh",
		`git tag --sign --force v1 "$RELEASE_COMMIT"`,
		"semantic release tag moved after publication; refusing to update v1",
		"canonical action releases must bundle release-signing-key.pem",
		"Attest image provenance on GitHub.com",
		"Attest downloadable artifacts on GitHub.com",
		"APEXLOAD_ENABLE_HOSTED_ATTESTATIONS",
		"APEXLOAD_GITHUB_ATTESTATIONS_TOKEN",
		"github-token: ${{ secrets.APEXLOAD_GITHUB_ATTESTATIONS_TOKEN }}",
		"Reverify frozen signed tag immediately before image exposure",
		"component: java-vuser-runner",
		"dockerfile: runners/java_vuser/Dockerfile",
		"java-vuser-runner:standard",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("release workflow missing required contract fragment %q", required)
		}
	}
	if got := strings.Count(workflow, "RELEASE_COMMIT: ${{ needs.metadata.outputs.commit }}"); got < 7 {
		t.Fatalf("release workflow wires frozen commit %d times, want at least 7 including both handoff reverification steps", got)
	}
	publisher, err := os.ReadFile("release/publish-release.sh")
	if err != nil {
		t.Fatalf("read release publisher: %v", err)
	}
	for _, required := range []string{
		"--draft=false",
		"verify-release.sh",
		"stored draft asset set",
		`"$public_key" "$release_commit"`,
		"assert_remote_release_tag",
		"expected_tag_object_sha",
		`git/ref/tags/${release_tag}`,
		"immutable-releases",
		"assert_release_asset_contract true false",
		"assert_release_asset_contract false true",
	} {
		if !strings.Contains(string(publisher), required) {
			t.Errorf("release publisher missing required contract fragment %q", required)
		}
	}
	if strings.Contains(workflow, ":latest") {
		t.Fatal("release workflow must not publish a mutable latest image")
	}
	if strings.Contains(workflow, "attestations: write") {
		t.Fatal("portable release workflow uses a permission key that is not parseable on all supported GHES lines")
	}
	assertExternalActionsPinned(t, workflow)
}

func TestReleaseVersionValidationUsesCanonicalSemVer(t *testing.T) {
	validTags := []string{
		"v0.0.0",
		"v1.2.3",
		"v1.2.3-rc.1",
		"v1.2.3-alpha-beta.0",
		"v10.20.30-x-y-z.--",
	}
	invalidTags := []string{
		"1.2.3",
		"v01.2.3",
		"v1.02.3",
		"v1.2.03",
		"v1.2.3.4",
		"v1.2.3-",
		"v1.2.3-01",
		"v1.2.3-alpha..1",
		"v1.2.3-alpha.",
		"v1.2.3+build",
	}

	for _, tag := range validTags {
		t.Run("valid/"+tag, func(t *testing.T) {
			assertReleaseTagValidator(t, tag, true)
		})
	}
	for _, tag := range invalidTags {
		t.Run("invalid/"+strings.ReplaceAll(tag, "/", "_"), func(t *testing.T) {
			assertReleaseTagValidator(t, tag, false)
		})
	}
}

func TestReleaseAndDeploymentWorkflowsBootstrapOnlyFromProtectedMain(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
	}{
		{name: "release.yml", eventType: "apexload-release"},
		{name: "deploy-aks.yml", eventType: "deploy-aks-release"},
		{name: "deploy-appservice.yml", eventType: "deploy-appservice-release"},
		{name: "publish-onprem.yml", eventType: "publish-onprem-release"},
	}
	for _, test := range tests {
		content, err := os.ReadFile(filepath.Join("..", ".github", "workflows", test.name))
		if err != nil {
			t.Fatal(err)
		}
		workflow := string(content)
		for _, required := range []string{
			"repository_dispatch:",
			"- " + test.eventType,
			"ref: refs/heads/main",
			`select(.object.type == "tag")`,
			`select(.object.type == "commit")`,
		} {
			if !strings.Contains(workflow, required) {
				t.Errorf("%s missing trusted default-branch bootstrap contract %q", test.name, required)
			}
		}
		if strings.Contains(workflow, "workflow_dispatch:") || strings.Contains(workflow, "push:\n    tags:") {
			t.Errorf("%s can load its workflow definition from an unverified candidate ref", test.name)
		}
	}

	release, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(release), "needs: verify-tag") ||
		!strings.Contains(string(release), "checkout-ref: ${{ needs.verify-tag.outputs.commit }}") {
		t.Fatal("authoritative release CI can execute before exact signed-tag verification")
	}
}

func assertReleaseTagValidator(t *testing.T, tag string, wantValid bool) {
	t.Helper()
	command := exec.Command("bash", "release/semver.sh", "tag", tag)
	shellErr := command.Run()
	if (shellErr == nil) != wantValid {
		t.Fatalf("shell validator validity for %q = %t, want %t", tag, shellErr == nil, wantValid)
	}

	python := exec.Command("python3", "-c", `
import importlib.util
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
spec = importlib.util.spec_from_file_location("apex_release_manifest", path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
raise SystemExit(0 if module.SEMVER.fullmatch(sys.argv[2]) else 1)
`, "release/create-manifest.py", tag)
	pythonErr := python.Run()
	if (pythonErr == nil) != wantValid {
		t.Fatalf("manifest validator validity for %q = %t, want %t", tag, pythonErr == nil, wantValid)
	}
}

func TestApplicationDeploymentsConsumeReleaseArtifactsWithoutBuilding(t *testing.T) {
	for _, name := range []string{"deploy-aks.yml", "deploy-appservice.yml", "publish-onprem.yml"} {
		content, err := os.ReadFile(filepath.Join("..", ".github", "workflows", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		workflow := string(content)
		// Match build commands, not inspection tooling such as
		// `docker buildx imagetools inspect` or `docker buildx version`.
		for _, prohibited := range []string{"go build ", "docker build ", "docker buildx build", "npm run build", "az acr build"} {
			if strings.Contains(workflow, prohibited) {
				t.Errorf("%s rebuilds application artifacts via %q", name, prohibited)
			}
		}
		for _, required := range []string{"github.event.client_payload.release", "environment:", "verify-release.sh"} {
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
		"PREVIOUS_VERSION: ${{ steps.prior.outputs.version }}",
		"PREVIOUS_BUILD_TIME: ${{ steps.prior.outputs.build-time }}",
		"PREVIOUS_CONTROLLER_IMAGE: ${{ steps.prior.outputs.controller-image }}",
		"PREVIOUS_GENERATOR_IMAGE: ${{ steps.prior.outputs.generator-image }}",
		"PREVIOUS_CONTROLLER_IMAGE_IDS: ${{ steps.prior.outputs.controller-image-ids }}",
		"PREVIOUS_GENERATOR_IMAGE_IDS: ${{ steps.prior.outputs.generator-image-ids }}",
		`printf 'KUBECONFIG=%s\n' "$RUNNER_TEMP/apexload-kubeconfig"`,
		"Remove cluster and Helm credentials",
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
		{workflow: "deploy-aks.yml", environment: "${{ github.event.client_payload.environment == 'production' && 'aks-production' || 'aks-staging' }}", preflight: "./scripts/deploy/kubernetes-preflight.sh"},
		{workflow: "deploy-appservice.yml", environment: "${{ github.event.client_payload.environment == 'production' && 'appservice-production' || 'appservice-staging' }}", preflight: "./scripts/deploy/active-runs-preflight.sh"},
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
			"APEXLOAD_ACTIVE_RUN_OVERRIDE_API_KEY: ${{ secrets.APEXLOAD_ACTIVE_RUN_OVERRIDE_APPROVAL != '' && secrets.APEXLOAD_ACTIVE_RUN_OVERRIDE_API_KEY || '' }}",
			"APEXLOAD_DEPLOY_ENVIRONMENT: " + test.environment,
			"APEXLOAD_DEPLOY_RELEASE: ${{ github.event.client_payload.release }}",
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
		"APEXLOAD_ACTIVE_RUN_OVERRIDE_API_KEY",
		"deployments:override-active-tests",
		"Do not define either secret name as a repository",
		"no more than four hours",
		"never prints the document, either API key, or its reason",
		"server-side tag immutability",
	} {
		if !strings.Contains(string(documentation), required) {
			t.Errorf("deployment documentation missing active-run override requirement %q", required)
		}
	}
}

func TestAppServiceStagingEnvironmentCannotSwapProduction(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/deploy-appservice.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(content)
	for _, required := range []string{
		"group: deploy-appservice",
		"if: github.event.client_payload.environment == 'production'",
		"DEPLOYMENT_ENVIRONMENT: ${{ github.event.client_payload.environment }}",
		"PRODUCTION_URL: ${{ github.event.client_payload.environment == 'production' && vars.APPSERVICE_PRODUCTION_URL || '' }}",
		"./scripts/deploy/appservice-public-url-preflight.sh",
		`"$RELEASE_BUILD_TIME" \`,
		`"$DEPLOYMENT_ENVIRONMENT"`,
		"controller-url: ${{ github.event.client_payload.environment == 'production' && vars.APPSERVICE_PRODUCTION_URL || vars.APPSERVICE_STAGING_URL }}",
		"github.event.client_payload.environment == 'production' && (failure() || cancelled())",
		`"$STAGING_URL" \`,
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("App Service workflow missing staging/production isolation contract %q", required)
		}
	}
	if strings.Contains(workflow, "group: deploy-appservice-${{ github.event.client_payload.environment }}") {
		t.Fatal("staging and production App Service dispatches can race on the shared staging slot")
	}
	if got := strings.Count(workflow, "./scripts/deploy/appservice-public-url-preflight.sh"); got != 2 {
		t.Fatalf("App Service public URL binding is checked %d times, want before and after deployment", got)
	}
	preflight := strings.Index(workflow, "run: ./scripts/deploy/active-runs-preflight.sh")
	baseline := strings.Index(workflow, "- name: Capture lease-protected production rollback baseline")
	deploy := strings.Index(workflow, "- name: Deploy and validate the selected App Service environment")
	if preflight < 0 || baseline < preflight || deploy < baseline {
		t.Fatal("App Service rollback baseline is not captured after lease acquisition and before mutation")
	}
}

func TestAKSRollbackBaselineIsLeaseProtected(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/deploy-aks.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(content)
	preflight := strings.Index(workflow, "run: ./scripts/deploy/kubernetes-preflight.sh")
	baseline := strings.Index(workflow, "- name: Capture lease-protected rollback baseline")
	deploy := strings.Index(workflow, "- name: Render, diff, deploy atomically, and verify")
	if preflight < 0 || baseline < preflight || deploy < baseline {
		t.Fatal("AKS rollback baseline is not captured after lease acquisition and before mutation")
	}
}

func TestOnPremRegistryCredentialsAreExplicitAndEphemeral(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/publish-onprem.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(content)
	for _, required := range []string{
		`printf 'REGISTRY_AUTH_FILE=%s\n' "$RUNNER_TEMP/apexload-containers-auth.json"`,
		`printf 'DOCKER_CONFIG=%s\n' "$RUNNER_TEMP/apexload-docker-config"`,
		"SOURCE_REGISTRY_USERNAME: ${{ vars.APEXLOAD_RELEASE_REGISTRY_USERNAME }}",
		"SOURCE_REGISTRY_PASSWORD: ${{ secrets.APEXLOAD_RELEASE_REGISTRY_PASSWORD }}",
		"custom/GHES source registries require explicit release registry credentials",
		"candidate changed before promotion",
		`tag_state="$(./scripts/release/registry-reference-state.sh skopeo "${repository}:${release_tag}")"`,
		"Checkout trusted tag verifier from protected main",
		"needs: [verify-tag, mirror-candidates]",
		"EXPECTED_RUNNER_IMAGE_DIGEST: ${{ vars.APEXLOAD_ONPREM_RUNNER_IMAGE_DIGEST }}",
		`[[ "${APEXLOAD_RUNNER_IMAGE_DIGEST:-}" == "$EXPECTED_RUNNER_IMAGE_DIGEST" ]]`,
		`[[ "$(skopeo --version)" == 'skopeo version 1.20.0' ]]`,
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("on-prem workflow missing registry security contract %q", required)
		}
	}
	if got := strings.Count(workflow, "- name: Remove registry credentials"); got != 2 {
		t.Fatalf("on-prem workflow cleans registry credentials %d times, want both jobs", got)
	}
	if got := strings.Count(workflow, `[[ "$EXPECTED_RUNNER_IMAGE_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]`); got != 2 {
		t.Fatalf("on-prem workflow validates the immutable runner image digest %d times, want both jobs", got)
	}
	if strings.Contains(workflow, `skopeo inspect --format '{{.Digest}}' "docker://${repository}:${release_tag}" 2>/dev/null || true`) {
		t.Fatal("on-prem promotion still treats every final-tag inspection failure as absence")
	}
}

func TestReleaseImageTagPromotionFailsClosedOnInspectionErrors(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(content)
	required := `tag_state="$(./scripts/release/registry-reference-state.sh docker-buildx "$final_tag")"`
	if got := strings.Count(workflow, required); got != 2 {
		t.Fatalf("release final-tag state is checked through the fail-closed helper %d times, want reservation and promotion", got)
	}
	if !strings.Contains(workflow, `tag_state="$(./scripts/release/registry-reference-state.sh docker-buildx "$tag")"`) {
		t.Fatal("release candidate-tag collision check does not use the fail-closed registry helper")
	}
	for _, unsafe := range []string{
		`if ! docker buildx imagetools inspect "$final_tag"`,
		`if docker buildx imagetools inspect "$final_tag" >/dev/null 2>&1`,
	} {
		if strings.Contains(workflow, unsafe) {
			t.Fatalf("release promotion still uses ambiguous registry inspection pattern %q", unsafe)
		}
	}
}

func TestObsoleteNonGitHubAndDormantGitHubTemplatesAreRemoved(t *testing.T) {
	for _, path := range []string{
		"../ci/jenkins/Jenkinsfile.example",
		"../ci/azure-devops/apexload-perf-gate.yml",
		"../azure-pipelines-task.yml",
		"../deployments/ci/azure-pipelines.yml",
		"../deployments/ci/azure-pipelines-aks.yml",
		"../deployments/ci/azure-pipelines-appservice.yml",
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
