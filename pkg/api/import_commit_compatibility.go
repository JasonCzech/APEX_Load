package api

// identityImportCommitResponse is the explicit compatibility mapper used by
// current structured import endpoints. Keeping this hook in each commit spec
// lets a source preserve a legacy response envelope without forking binding,
// validation, persistence, or audit orchestration.
func identityImportCommitResponse(payload map[string]any) map[string]any {
	return payload
}
