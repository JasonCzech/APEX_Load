package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
)

func ValidateScript(script *dsl.Script) []string {
	return ValidateStoredExecutionScript(script)
}
