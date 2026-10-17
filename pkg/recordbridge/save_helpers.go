package recordbridge

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func hashCanonical(value any) string {
	payload, _ := json.Marshal(value)
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum[:])
}
func AppendBrowserRecordingScripts(scripts []*dsl.Script, httpScript *dsl.Script) []any {
	out := make([]any, 0, len(scripts)+1)
	for _, script := range scripts {
		if script != nil {
			out = append(out, script)
		}
	}
	if httpScript != nil {
		out = append(out, httpScript)
	}
	return out
}
