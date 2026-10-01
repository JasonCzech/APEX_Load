package api

func matchesNativeDimensionFilters(tx *TransactionEvent, filters map[string]string) bool {
	return transactionMatchesArchiveFilter(tx, transactionEventFilter{NetworkProfile: filters["network_profile"], Hierarchy: filters["hierarchy"], Phase: filters["phase"]})
}
