package generatorrpc

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
)

func TestJMeterRequiredRuntimeFeatureIsVersioned(t *testing.T) {
	script := &dsl.Script{Config: dsl.ScriptConfig{JMeter: &dsl.JMeterCompatibility{Profile: dsl.JMeterMessagingProfile}}}
	required := requiredRuntimeFeature(script)
	if required != dsl.FeatureJMeterMessagingV1 || protocol.RuntimeFeaturesSatisfy(nil, required) {
		t.Fatal(required)
	}
}
