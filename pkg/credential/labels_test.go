package credential

import "testing"

func TestLabelField(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want bool
	}{{"name", true}, {"transaction_name", true}, {"source_transaction", true}, {"source_subtransaction", true}, {"source_action_name", true}, {"stepName", true}, {"", false}, {"password", false}, {"api_key", false}, {"body", false}, {"value", false}, {"transaction_token", false}} {
		if got := LabelField(tc.key); got != tc.want {
			t.Errorf("LabelField(%q)=%v", tc.key, got)
		}
	}
}

func TestTransactionLabelArgument(t *testing.T) {
	for _, tc := range []struct {
		function string
		index    int
		want     bool
	}{
		{"lr_start_transaction", 0, true}, {"lr_end_transaction", 0, true}, {"lr_start_sub_transaction", 1, true},
		{"lr_set_transaction_status_by_name", 1, true}, {"lr_set_transaction_status_by_name", 0, false},
		{"lr_set_transaction", 1, false}, {"lr_start_transaction", -1, false}, {"lr_start_transaction", 1, false},
		{"web_set_user", 1, false}, {"", 0, false}, {"unknown", 0, false},
	} {
		if got := TransactionLabelArgument(tc.function, tc.index); got != tc.want {
			t.Errorf("TransactionLabelArgument(%q,%d)=%v", tc.function, tc.index, got)
		}
	}
}
