package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestGeneratedEnterpriseDatabaseBindingsAreDescriptiveAndBatchUnique(t *testing.T) {
	newScript := func(name string) *dsl.Script {
		return &dsl.Script{
			Name:     name,
			Protocol: "database",
			Config: dsl.ScriptConfig{Database: dsl.DatabaseConfig{
				Driver: "postgres",
				DSN:    "{{secret.database_dsn}}",
			}},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
				Source: "enterprise_script_archive",
				Preserved: map[string]any{
					generatedDatabaseDSNMarkerKey: true,
				},
			}},
		}
	}
	first := newScript("Orders database")
	second := newScript("Orders database")
	result := &ConvertResult{Scripts: []*dsl.Script{first, second}}
	if err := finalizeEnterpriseArchiveImportResult(result, archiveFinalizationContext{
		ProjectFileName: "project.usp",
	}); err != nil {
		t.Fatalf("finalizeEnterpriseArchiveImportResult: %v", err)
	}
	if strings.EqualFold(strings.TrimSpace(first.Name), strings.TrimSpace(second.Name)) {
		t.Fatalf("batch script names remain ambiguous: %q and %q", first.Name, second.Name)
	}

	requirements := result.ProtectedRequirements.Take()
	if len(requirements) != 2 {
		t.Fatalf("database requirements = %#v, want one independently bindable requirement per generated script", requirements)
	}
	if first.Config.Database.DSN == second.Config.Database.DSN {
		t.Fatalf("batch database placeholders collided: %q", first.Config.Database.DSN)
	}
	seenNames := make(map[string]struct{}, len(requirements))
	for _, requirement := range requirements {
		if _, duplicate := seenNames[requirement.SuggestedName]; duplicate {
			t.Fatalf("generated database requirement names collided: %#v", requirements)
		}
		seenNames[requirement.SuggestedName] = struct{}{}
		if requirement.Value != "" || requirement.SourceValueAvailable ||
			requirement.SourceValueStatus != credentialpolicy.SourceCredentialValueMissing ||
			requirement.ExistingReference || requirement.Source.Format != "enterprise_script_archive" ||
			requirement.Source.Path != "project.usp" || len(requirement.Targets) != 1 ||
			requirement.Targets[0].RequestKey == "" || requirement.Context == "" ||
			requirement.Label == "" {
			t.Fatalf("generated database requirement lacks descriptive provenance: %#v", requirement)
		}
	}
}

func TestAuthoredEnterpriseDatabaseSecretReferenceIsNotReclassifiedAsGenerated(t *testing.T) {
	script := &dsl.Script{
		Name:     "Authored database",
		Protocol: "database",
		Config: dsl.ScriptConfig{Database: dsl.DatabaseConfig{
			Driver: "postgres",
			DSN:    "{{secret.DATABASE_DSN}}",
		}},
	}
	result := &ConvertResult{Script: script}
	applyEnterpriseRuntimeBindings(result, []*dsl.Script{script}, archiveFinalizationContext{})
	if requirements := result.ProtectedRequirements.Take(); requirements != nil {
		t.Fatalf("authored database secret was treated as an unconfigured generated binding: %#v", requirements)
	}
}
