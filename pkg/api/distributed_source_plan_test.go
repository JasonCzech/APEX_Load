package api

import (
	"os"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestDistributedSourcePlanPreservesCompleteManifest(t *testing.T) {
	source, err := os.ReadFile("../convert/jmeter/testdata/messaging_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	converted, err := jmeter.ConvertJMX(source, jmeter.ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	groups := make([]TestGroup, len(converted.Scripts))
	counts := []int{100, 50, 50}
	for i, script := range converted.Scripts {
		script.ProjectID = "p"
		groups[i] = TestGroup{Name: script.Name, VUsers: counts[i], script: script}
	}
	manifest, err := distributedSourcePlan(groups, "p", 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Groups) != 3 || manifest.ExecutionGeneration != 42 || manifest.ProjectId != "p" || manifest.CompatibilityProfile != dsl.JMeterMessagingProfile {
		t.Fatalf("incomplete manifest: %v", manifest)
	}
	if len(manifest.RequiredFeatures) != 2 || manifest.RequiredFeatures[0] != generatorrpc.FeatureSourcePlanV1 || manifest.RequiredFeatures[1] != dsl.FeatureJMeterMessagingV1 {
		t.Fatal("lost strict compatibility requirement")
	}
	total := int32(0)
	for _, group := range manifest.Groups {
		total += group.Vusers
		if len(group.ScriptHash) != 64 || group.SourceGroupId == "" {
			t.Fatal("group is not source/hash bound")
		}
	}
	if total != 200 {
		t.Fatalf("population changed: %d", total)
	}
	groups[1].script.ProjectID = "other"
	if _, err := distributedSourcePlan(groups, "p", 42); err == nil {
		t.Fatal("cross-project source plan accepted")
	}
	groups[1].script = &dsl.Script{}
	if _, err := distributedSourcePlan(groups, "p", 42); err == nil {
		t.Fatal("mixed source plan accepted")
	}
	if m, err := distributedSourcePlan([]TestGroup{{Name: "ordinary", script: &dsl.Script{}}}, "p", 1); err != nil || m != nil {
		t.Fatal("ordinary assignment changed")
	}
}
