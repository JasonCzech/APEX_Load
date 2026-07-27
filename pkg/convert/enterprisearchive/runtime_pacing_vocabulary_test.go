package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// dslPacingAllowlist mirrors validatePacingResourceIssues in pkg/dsl. Pacing,
// run-logic, and think-time limits now validate for every protocol, not just
// the messaging families, so an archive-derived pacing type that falls outside
// this set fails script save AND the stored-script revalidation that runs when
// a test is launched.
var dslPacingAllowlist = map[string]struct{}{
	"none": {}, "fixed_delay": {}, "random_delay": {},
	"fixed_interval": {}, "random_interval": {}, "tps": {},
}

// TestRuntimeConfigPacingVocabularyStaysInsideDSLAllowlist pins the boundary
// between mapPacingType and the DSL allowlist. mapPacingType deliberately
// passes an unrecognized LoadRunner mode through verbatim so the migration
// report can quote what the archive actually said; parseRuntimeCfgPacing is
// what must stop that value before it reaches dsl.PacingConfig.Type. If a
// future change lets the raw value escape, this test fails instead of the
// operator's next test launch.
func TestRuntimeConfigPacingVocabularyStaysInsideDSLAllowlist(t *testing.T) {
	modes := []string{
		// Every alias mapPacingType recognizes.
		"", "none", "immediate", "start_new_iteration_immediately",
		"fixed_delay", "after_previous_iteration_ends", "delay",
		"random_delay", "after_previous_iteration_ends_random",
		"fixed_interval", "interval", "start_new_iteration_at_fixed_intervals",
		"random_interval", "start_new_iteration_at_random_intervals",
		"tps",
		// Values that reach the passthrough default branch.
		"AsSoonAsPossible", "unsupported-mode", "Const", "After",
		"start_new_iteration_at_unknown_cadence", "  ", "0", "-1",
	}

	for _, mode := range modes {
		t.Run(strings.TrimSpace(mode), func(t *testing.T) {
			files := map[string]string{
				"active.cfg": "[Pacing]\nMode=" + mode + "\n" +
					"DelayMs=1000\nMinDelayMs=1000\nMaxDelayMs=2000\n" +
					"IntervalMs=1000\nMinIntervalMs=1000\nMaxIntervalMs=2000\n",
			}
			_, rc := resolveRuntimeConfig(files, &ProjectConfig{DefaultCfg: "active.cfg"})
			if rc == nil {
				t.Fatalf("resolveRuntimeConfig returned no runtime config for mode %q", mode)
			}
			if rc.PacingType != "" {
				if _, ok := dslPacingAllowlist[rc.PacingType]; !ok {
					t.Fatalf("cfg pacing mode %q produced PacingType %q outside the DSL allowlist", mode, rc.PacingType)
				}
			}

			script := pacingVocabularyScript(rc)
			if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
				t.Fatalf("cfg pacing mode %q produced an invalid script: %v", mode, issues)
			}
		})
	}
}

// TestRuntimeConfigUSPPacingVocabularyStaysInsideDSLAllowlist covers the other
// producer of rc.PacingType: the .usp run-logic file.
func TestRuntimeConfigUSPPacingVocabularyStaysInsideDSLAllowlist(t *testing.T) {
	paceTypes := []string{
		"ASAP", "Const", "ConstAfter", "Random", "RandomAfter",
		"After", "Unsupported", "", "   ",
	}

	for _, paceType := range paceTypes {
		t.Run(strings.TrimSpace(paceType), func(t *testing.T) {
			files := map[string]string{
				"active.cfg": "[General]\nDefaultRunLogic=selected.usp",
				"selected.usp": "[RunLogicRunRoot]\n" +
					"RunLogicNumOfIterations=\"2\"\n" +
					"RunLogicPaceType=\"" + paceType + "\"\n" +
					"RunLogicPaceConstTime=\"5\"\n" +
					"RunLogicRandomPaceMin=\"1\"\nRunLogicRandomPaceMax=\"2\"\n" +
					"RunLogicPaceConstAfterTime=\"5\"\n" +
					"RunLogicAfterPaceMin=\"1\"\nRunLogicAfterPaceMax=\"2\"\n",
			}
			_, rc := resolveRuntimeConfig(files, &ProjectConfig{DefaultCfg: "active.cfg"})
			if rc == nil {
				t.Fatalf("resolveRuntimeConfig returned no runtime config for pace type %q", paceType)
			}
			if rc.PacingType != "" {
				if _, ok := dslPacingAllowlist[rc.PacingType]; !ok {
					t.Fatalf("usp pace type %q produced PacingType %q outside the DSL allowlist", paceType, rc.PacingType)
				}
			}

			script := pacingVocabularyScript(rc)
			if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
				t.Fatalf("usp pace type %q produced an invalid script: %v", paceType, issues)
			}
		})
	}
}

func pacingVocabularyScript(rc *RuntimeConfig) *dsl.Script {
	script := &dsl.Script{
		Name:     "pacing vocabulary",
		Protocol: "http",
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "GET",
			Method: "GET",
			Target: "https://api.example.test/orders",
		}},
	}
	if config := rc.ToScriptConfig(); config != nil {
		script.Config = *config
	}
	if strings.TrimSpace(script.Config.BaseURL) == "" {
		script.Config.BaseURL = "https://api.example.test"
	}
	return script
}
