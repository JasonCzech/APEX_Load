package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLifecycleControlDoesNotCrossJSONBoundary(t *testing.T) {
	r := Result{Success: true, LifecycleControl: &LifecycleControl{EndIteration: true, Failed: true}}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "LifecycleControl") || strings.Contains(string(encoded), "EndIteration") {
		t.Fatal("control exported")
	}
	for _, payload := range []string{`{"LifecycleControl":{"EndIteration":true,"Failed":true}}`, `{"lifecycle_control":{"end_iteration":true}}`} {
		var decoded Result
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.LifecycleControl != nil {
			t.Fatal("external result injected lifecycle control")
		}
	}
}
