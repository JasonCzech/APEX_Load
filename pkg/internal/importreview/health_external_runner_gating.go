package importreview

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

func JavaVuserActionsRequirePhaseLifecycle(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
			method := ""
			if value, ok := action.Params["method"]; ok && value != nil {
				method = strings.TrimSpace(fmt.Sprint(value))
			}
			if method != "" && !strings.EqualFold(method, "full_lifecycle") {
				return true
			}
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				if JavaVuserActionsRequirePhaseLifecycle(branch.Actions) {
					return true
				}
			}
		}
	}
	return false
}
