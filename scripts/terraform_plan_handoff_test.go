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
	handoff := filepath.Join(root, "handoff")
	if err := os.WriteFile(plan, []byte("terraform-plan-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(encrypted, []byte("encrypted-plan-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("handoff-secret-", 4)
	create := exec.Command("python3", "deploy/terraform-plan-handoff.py", "create",
		"--environment", "production",
		"--repository", "owner/repository",
		"--commit", strings.Repeat("a", 40),
		"--run-id", "1234",
		"--run-attempt", "2",
		"--terraform-version", "1.13.4",
		"--plan", plan,
		"--encrypted", encrypted,
		"--output", handoff,
	)
	create.Env = append(os.Environ(), "PLAN_ENCRYPTION_KEY="+key)
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create authenticated handoff: %v\n%s", err, output)
	}

	verify := func(environment string) ([]byte, error) {
		command := exec.Command("python3", "deploy/terraform-plan-handoff.py", "verify",
			"--environment", environment,
			"--repository", "owner/repository",
			"--run-id", "1234",
			"--run-attempt", "2",
			"--directory", handoff,
		)
		command.Env = append(os.Environ(), "PLAN_ENCRYPTION_KEY="+key)
		return command.CombinedOutput()
	}
	output, err := verify("production")
	if err != nil || strings.TrimSpace(string(output)) != strings.Repeat("a", 40) {
		t.Fatalf("verify valid handoff: %v\n%s", err, output)
	}
	if output, err = verify("staging"); err == nil || !strings.Contains(string(output), "does not match the requested plan run") {
		t.Fatalf("coordinate mismatch was not rejected: %v\n%s", err, output)
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
	if output, err = verify("production"); err == nil || !strings.Contains(string(output), "authentication failed") {
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
		"if: inputs.operation == 'plan'",
		"if: inputs.operation == 'apply'",
		"plan-run-id:",
		"plan-run-attempt:",
		"gh run download \"$PLAN_RUN_ID\"",
		"terraform-plan-handoff.py verify",
		"refs/heads/main",
		"ref: ${{ steps.handoff.outputs.source-commit }}",
		"apply -auto-approve -lock-timeout=5m \"$RUNNER_TEMP/apexload.tfplan\"",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("Terraform workflow missing saved-plan promotion contract %q", required)
		}
	}
}
