package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerraformPlanHandoffAuthenticatesCoordinatesAndBytes(t *testing.T) {
	root := t.TempDir()
	plan := filepath.Join(root, "plan")
	encrypted := filepath.Join(root, "plan.enc")
	backend := filepath.Join(root, "backend.hcl")
	handoff := filepath.Join(root, "handoff")
	if err := os.WriteFile(plan, []byte("terraform-plan-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(encrypted, []byte("encrypted-plan-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backend, []byte(`resource_group_name = "tfstate"`), 0o600); err != nil {
		t.Fatal(err)
	}
	tenantID := "11111111-1111-4111-8111-111111111111"
	subscriptionID := "22222222-2222-4222-8222-222222222222"
	planClientID := "33333333-3333-4333-8333-333333333333"
	applyClientID := "44444444-4444-4444-8444-444444444444"
	key := strings.Repeat("handoff-secret-", 4)
	create := exec.Command("python3", "deploy/terraform-plan-handoff.py", "create",
		"--environment", "production",
		"--repository", "owner/repository",
		"--commit", strings.Repeat("a", 40),
		"--run-id", "1234",
		"--run-attempt", "2",
		"--terraform-version", "1.15.8",
		"--azure-tenant-id", tenantID,
		"--azure-subscription-id", subscriptionID,
		"--azure-auth-mode", "oidc",
		"--azure-plan-client-id", planClientID,
		"--azure-apply-client-id", applyClientID,
		"--backend-config", backend,
		"--plan", plan,
		"--encrypted", encrypted,
		"--output", handoff,
	)
	create.Env = append(os.Environ(), "PLAN_ENCRYPTION_KEY="+key)
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create authenticated handoff: %v\n%s", err, output)
	}
	metadataBytes, err := os.ReadFile(filepath.Join(handoff, "plan-handoff.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(metadataBytes), `"operation": "plan"`) ||
		!strings.Contains(string(metadataBytes), `"azure_subscription_id": "`+subscriptionID+`"`) {
		t.Fatalf("handoff does not bind the plan operation and Azure target:\n%s", metadataBytes)
	}

	verify := func(environment, subscription string) ([]byte, error) {
		command := exec.Command("python3", "deploy/terraform-plan-handoff.py", "verify",
			"--environment", environment,
			"--repository", "owner/repository",
			"--run-id", "1234",
			"--run-attempt", "2",
			"--azure-tenant-id", tenantID,
			"--azure-subscription-id", subscription,
			"--azure-auth-mode", "oidc",
			"--azure-plan-client-id", planClientID,
			"--azure-apply-client-id", applyClientID,
			"--backend-config", backend,
			"--directory", handoff,
		)
		command.Env = append(os.Environ(), "PLAN_ENCRYPTION_KEY="+key)
		return command.CombinedOutput()
	}
	output, err := verify("production", subscriptionID)
	if err != nil || strings.TrimSpace(string(output)) != strings.Repeat("a", 40) {
		t.Fatalf("verify valid handoff: %v\n%s", err, output)
	}
	if output, err = verify("staging", subscriptionID); err == nil || !strings.Contains(string(output), "does not match the requested plan run") {
		t.Fatalf("coordinate mismatch was not rejected: %v\n%s", err, output)
	}
	if output, err = verify("production", "55555555-5555-4555-8555-555555555555"); err == nil || !strings.Contains(string(output), "does not match the requested plan run") {
		t.Fatalf("Azure subscription mismatch was not rejected: %v\n%s", err, output)
	}

	encryptedPath := filepath.Join(handoff, "apexload.tfplan.enc")
	file, err := os.OpenFile(encryptedPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("tamper")); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if output, err = verify("production", subscriptionID); err == nil || !strings.Contains(string(output), "authentication failed") {
		t.Fatalf("tampered ciphertext was not rejected: %v\n%s", err, output)
	}
}

func TestTerraformWorkflowPromotesOnlyPriorProtectedMainPlan(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/infra-azure.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(content)
	for _, required := range []string{
		"repository_dispatch:",
		"- azure-infra-plan",
		"- azure-infra-apply",
		"if: github.event.action == 'azure-infra-plan'",
		"if: github.event.action == 'azure-infra-apply'",
		`keys == ["environment"]`,
		`keys == ["environment", "plan_run_attempt", "plan_run_id"]`,
		"github.event.client_payload.plan_run_id",
		"github.event.client_payload.plan_run_attempt",
		`actions/runs/${PLAN_RUN_ID}/attempts/${PLAN_RUN_ATTEMPT}`,
		`.run_attempt == $attempt`,
		`.event == "repository_dispatch"`,
		`.path == ".github/workflows/infra-azure.yml@main"`,
		"gh run download \"$PLAN_RUN_ID\"",
		"terraform-plan-handoff.py verify",
		"--azure-subscription-id \"$AZURE_SUBSCRIPTION_ID\"",
		"--backend-config \"$RUNNER_TEMP/backend.hcl\"",
		`actions/runs/${PLAN_RUN_ID}/attempts/${PLAN_RUN_ATTEMPT}/jobs`,
		"refs/heads/main",
		"ref: ${{ steps.handoff.outputs.source-commit }}",
		`compare/${PLANNED_COMMIT}...main`,
		"Remove Terraform plan credentials and plaintext",
		"Remove decrypted Terraform plan and backend credentials",
		"errored.tfstate",
		"apply -auto-approve -lock-timeout=5m \"$RUNNER_TEMP/apexload.tfplan\"",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("Terraform workflow missing saved-plan promotion contract %q", required)
		}
	}
	if strings.Contains(workflow, `gh api "repos/${REPOSITORY}/actions/runs/${PLAN_RUN_ID}"`) {
		t.Fatal("Terraform apply validates the latest rerun instead of the exact selected plan attempt")
	}
	if strings.Contains(workflow, "workflow_dispatch:") || strings.Contains(workflow, "inputs.") {
		t.Fatal("Terraform workflow can still be loaded from a branch-selected manual dispatch")
	}
}
