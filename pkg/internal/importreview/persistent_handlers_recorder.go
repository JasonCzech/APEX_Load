package importreview

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

func ValidateConvertedScriptForCommit(script *dsl.Script) error {
	if script == nil {
		return fmt.Errorf("conversion produced no script")
	}
	if issues := ValidateScript(script); len(issues) > 0 {
		return fmt.Errorf("%s", strings.Join(issues, "; "))
	}
	return nil
}
