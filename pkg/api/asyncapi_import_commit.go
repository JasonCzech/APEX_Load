package api

import (
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func diffAsyncAPIImport(existing *dsl.Script, imported *dsl.Script, replaceMode string) openAPIImportDiff {
	return diffImportByKey(existing, imported, replaceMode, asyncAPIActionKey)
}

func mergeAsyncAPIImportScript(existing *dsl.Script, imported *dsl.Script) *dsl.Script {
	return mergeImportScriptByKey(existing, imported, asyncAPIActionKey)
}

func asyncAPIActionKey(action dsl.Action) string {
	if action.Metadata == nil {
		return ""
	}
	channel := strings.TrimSpace(anyString(action.Metadata["asyncapi_channel"]))
	if channel == "" {
		return ""
	}
	correlation := strings.TrimSpace(anyString(action.Metadata["asyncapi_correlation_id"]))
	parts := []string{strings.ToLower(strings.TrimSpace(action.Type)), strings.TrimSpace(action.Target), channel, strings.TrimSpace(action.Name), correlation}
	return strings.Join(parts, "\x00")
}

func (s *Server) handleCommitAsyncAPIImport(w http.ResponseWriter, r *http.Request) {
	s.handleImportCommit(w, r, importCommitSpec{
		sourceProfile:         "asyncapi",
		auditProfile:          "asyncapi",
		displayName:           "AsyncAPI",
		allowedReplaceModes:   []string{"full", "merge_by_operation_id"},
		actionKey:             asyncAPIActionKey,
		diff:                  diffImportByKey,
		merge:                 mergeImportScriptByKey,
		mapCompatibleResponse: identityImportCommitResponse,
	})
}
