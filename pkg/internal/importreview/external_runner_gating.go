package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

const JavaVuserApprovalEvidenceKey = "java_vuser_authoring_approval"

type JavaVuserApprovalEvidence struct {
	SessionID   string
	ContentHash string
}

func ClearJavaVuserAuthoringApproval(script *dsl.Script) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved == nil {
		return
	}
	delete(script.Metadata.Import.Preserved, JavaVuserApprovalEvidenceKey)
}

func WriteJavaVuserAuthoringApproval(script *dsl.Script, evidence JavaVuserApprovalEvidence) {
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	script.Metadata.Import.Preserved[JavaVuserApprovalEvidenceKey] = map[string]any{
		"session_id":   evidence.SessionID,
		"content_hash": evidence.ContentHash,
	}
}

func ScriptRequiresJavaVuserAuthoringApproval(script *dsl.Script) bool {
	return script != nil &&
		strings.EqualFold(strings.TrimSpace(script.Protocol), "java_vuser") &&
		(JavaVuserActionsRequireRunner(script.Init) ||
			JavaVuserActionsRequireRunner(script.Actions) ||
			JavaVuserActionsRequireRunner(script.Teardown))
}

func JavaVuserActionsRequireRunner(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
			return true
		}
		if action.Control != nil {
			for _, branch := range action.Control.ActionBranches() {
				if JavaVuserActionsRequireRunner(branch.Actions) {
					return true
				}
			}
		}
	}
	return false
}
