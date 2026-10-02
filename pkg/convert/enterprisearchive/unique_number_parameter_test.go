package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const sourceUniqueNumberParameterFixture = `[parameter:trace_id]
BlockSize="999999"
Format="%01d"
GenerateNewVal="EachIteration"
OutOfRangePolicy="ContinueCyclic"
ParamName="trace_id"
StartValue="2"
Type="Unique"
`

func TestImportUniqueNumberPreservesBlockAndCycleSettings(t *testing.T) {
	parsed := ParseParameterFile("Producer.prm", sourceUniqueNumberParameterFixture, nil)
	if len(parsed.Parameters) != 1 || len(parsed.Warnings) != 0 || len(parsed.AllocationBlockers) != 0 {
		t.Fatalf("unique parameter import: %#v", parsed)
	}
	p := parsed.Parameters[0]
	if p.Type != "counter" || p.Selection != "unique" || p.Start != 2 || p.Step != 1 || p.BlockSize != 999999 || p.PadWidth != 1 || p.Update != "each_iteration" || p.OnExhaustion != "wrap" {
		t.Fatalf("unique number settings: %#v", p)
	}
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "KafkaFixture", map[string]string{
		"Actions.java": javaKafkaProducerSettingsFixture,
		"Producer.prm": sourceUniqueNumberParameterFixture + `[parameter:event_time]
Format="%Y-%m-%dT%H:%M:%SZ"
GenerateNewVal="EachOccurrence"
ParamName="event_time"
Type="Time"
`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.FidelityReport.ParameterParity; got["expected_parameters"] != 2 || got["converted_parameters"] != 2 {
		t.Fatalf("parameter conservation: %#v", got)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("converted parameter validation: %v", issues)
	}
}

func TestImportUniqueNumberInvalidSettingsRemainBlocked(t *testing.T) {
	for _, test := range []struct{ from, to string }{
		{`BlockSize="999999"`, `BlockSize="0"`},
		{`BlockSize="999999"`, `BlockSize="1000000001"`},
		{`StartValue="2"`, `StartValue="999999999999999999999999999999"`},
		{`Format="%01d"`, `Format="value=%01d"`},
		{`Format="%01d"`, `Format="%01ud"`},
		{`Format="%01d"`, `Format="%01uld"`},
		{`GenerateNewVal="EachIteration"`, `GenerateNewVal="Unknown"`},
		{`OutOfRangePolicy="ContinueCyclic"`, `OutOfRangePolicy="Unknown"`},
	} {
		t.Run(test.to, func(t *testing.T) {
			parsed := ParseParameterFile("Producer.prm", strings.Replace(sourceUniqueNumberParameterFixture, test.from, test.to, 1), nil)
			if len(parsed.Parameters) != 1 || len(parsed.AllocationBlockers) != 1 || len(parsed.Warnings) != 1 {
				t.Fatalf("invalid source setting was dropped or executed: %#v", parsed)
			}
			if issues := dsl.ValidateScriptIssues(&dsl.Script{Name: "blocked", Version: "1", Protocol: "http/1.1", Config: dsl.ScriptConfig{BaseURL: "https://example.invalid"}, Parameters: parsed.Parameters, Actions: []dsl.Action{{Name: "request", Type: "GET", Target: "https://example.invalid"}}}); len(issues) != 0 {
				t.Fatalf("blocked parameter placeholder should remain a valid draft: %v", issues)
			}
		})
	}
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "InvalidUnique", map[string]string{"Actions.java": javaKafkaProducerSettingsFixture, "Unique.prm": strings.Replace(sourceUniqueNumberParameterFixture, `BlockSize="999999"`, `BlockSize="0"`, 1)}), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.FidelityReport.PromotionStatus != ImportPromotionBlocked || len(result.FidelityReport.Blockers) == 0 {
		t.Fatal("invalid source block allocation lacked a durable fidelity gate")
	}
}

func TestImportUniqueNumberExhaustionPoliciesAndPadding(t *testing.T) {
	for _, test := range []struct{ source, native string }{{"ContinueCyclic", "wrap"}, {"ContinueWithLast", "last"}, {"AbortVuser", "error"}} {
		fixture := fmt.Sprintf("[parameter:id]\nType=UniqueNumber\nStartValue=0\nBlockSize=4\nFormat=%%04d\nOutOfRangePolicy=%s\nGenerateNewVal=Once\n", test.source)
		parsed := ParseParameterFile("Unique.prm", fixture, nil)
		if len(parsed.Parameters) != 1 || len(parsed.AllocationBlockers) != 0 || parsed.Parameters[0].OnExhaustion != test.native || parsed.Parameters[0].PadWidth != 4 || parsed.Parameters[0].Update != "once" {
			t.Fatalf("%s: %#v", test.source, parsed)
		}
	}
}
