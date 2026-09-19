package credential

// LabelField identifies descriptive script fields. Labels are not secrets merely
// because they resemble high-entropy tokens. Callers must still scrub structured
// credentials and literals collected from actual credential-bearing fields.
func LabelField(key string) bool {
	switch NormalizeName(key) {
	case "name", "label", "title", "stepname", "transactionname", "sourcetransaction", "sourcesubtransaction", "sourceactionname", "loadrunnertransaction":
		return true
	default:
		return false
	}
}

// TransactionLabelArgument identifies name positions in imported LoadRunner
// transaction calls. Other arguments and unknown functions remain ordinary data.
func TransactionLabelArgument(function string, index int) bool {
	switch function {
	case "lr_start_sub_transaction":
		return index == 0 || index == 1
	case "lr_set_transaction_status_by_name":
		return index == 1
	case "lr_start_transaction", "lr_end_transaction", "lr_end_sub_transaction", "lr_end_sub_trans",
		"lr_stop_transaction", "lr_resume_transaction", "lr_set_transaction",
		"lr_get_transaction_duration", "lr_get_transaction_status", "lr_get_transaction_think_time", "lr_get_transaction_wasted_time":
		return index == 0
	default:
		return false
	}
}
