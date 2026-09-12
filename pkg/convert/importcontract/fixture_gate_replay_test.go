package importcontract

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/cloudtest"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
	_ "github.com/jasonczech/apexload/pkg/protocol/java_vuser"
)

// A successful conversion is not permission to execute. Persisted drafts must
// retain their blockers at the engine boundary, before contacting any target.
func TestFixtureEnterpriseGatesSurvivePersistenceAndRejectReplay(t *testing.T) {
	t.Setenv("APEXLOAD_JAVA_VUSER_RUNNER_ENABLED", "false")
	for _, name := range []string{"cloudtest", "sap-review", "java-lifecycle"} {
		t.Run(name, func(t *testing.T) {
			var script *dsl.Script
			if name == "cloudtest" {
				result, err := cloudtest.Convert([]byte(`<Repository><Clip name="Checkout"><HTTP method="GET" url="http://127.0.0.1:1/must-not-run"/></Clip><Composition name="Sale"><Track clip="Checkout" users="5"/></Composition></Repository>`), cloudtest.ConvertOptions{FileName: "fixture.xml"})
				if err != nil {
					t.Fatal(err)
				}
				if len(result.Scripts) != 1 {
					t.Fatal("missing gated clip")
				}
				script = result.Scripts[0]
			} else {
				data, err := os.ReadFile("../../../dashboard/e2e/fixtures/" + name + ".zip")
				if err != nil {
					t.Fatal(err)
				}
				result, err := enterprisearchive.ConvertZip(data, enterprisearchive.ConvertOptions{})
				if err != nil {
					t.Fatal(err)
				}
				script = result.Script
			}
			if script == nil {
				t.Fatal("import did not produce a review draft")
			}
			data, err := json.Marshal(script)
			if err != nil {
				t.Fatal(err)
			}
			var restored dsl.Script
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			report, err := engine.ReplayScript(context.Background(), &restored, engine.ReplayOptions{IgnoreThinkTime: true})
			if name == "java-lifecycle" {
				if err != nil || report == nil || report.Success || !strings.Contains(report.Error, "java_vuser runner is disabled") {
					t.Fatal("Java runner policy did not reject execution")
				}
				t.Log("Java execution rejected by disabled runner policy")
				return
			}
			want := "cloudtest_fixture_mapping_unproven"
			if name == "sap-review" {
				want = "unknown protocol: sap_gui"
			}
			if err == nil || report != nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("expected %s before execution, got %v", want, err)
			}
			t.Logf("blocked before execution: %s", err)
		})
	}
}
