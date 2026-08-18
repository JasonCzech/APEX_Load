package k6

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
)

func TestRedactK6CredentialValuesDeduplicatesBeforeWorkBudget(t *testing.T) {
	requirements := make([]credentialpolicy.Requirement, 10_000)
	for index := range requirements {
		requirements[index].Value = "one-repeated-secret"
	}
	content := `const token = "one-repeated-secret";`

	redacted, changed := redactK6CredentialValues(content, requirements, nil)
	if !changed || strings.Contains(redacted, "one-repeated-secret") || strings.Contains(redacted, "SOURCE OMITTED") {
		t.Fatalf("unexpected redaction result: %q", redacted)
	}
}

func TestRedactK6CredentialValuesOmitsSourceWhenWorkBudgetIsExceeded(t *testing.T) {
	requirements := make([]credentialpolicy.Requirement, 10_000)
	for index := range requirements {
		requirements[index].Value = fmt.Sprintf("unique-secret-%05d", index)
	}
	content := strings.Repeat("x", 20_000) + "\n"

	redacted, changed := redactK6CredentialValues(content, requirements, nil)
	if !changed || !strings.Contains(redacted, "SOURCE OMITTED") || strings.Contains(redacted, "unique-secret") {
		t.Fatalf("unexpected fail-closed result: %q", redacted)
	}
}
