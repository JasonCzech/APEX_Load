package enterprisearchive

import "testing"

func TestParameterHelperReviewGates(t *testing.T) {
	tests := []struct {
		call   string
		review bool
	}{
		{`web_save_timestamp_param("TS", LAST);`, false},
		{`web_save_param_length("Token", LAST);`, false},
		{`web_save_param_length("Token", "Base=Decimal", LAST);`, false},
		{`web_save_param_length("Token", "Base=Hexadecimal", LAST);`, true},
		{`web_convert_date_param("Day", "SourceFormat=%d/%m/%Y", "TargetFormat=%Y-%m-%d", LAST);`, false},
		{`web_convert_date_param("Day", "SourceFormat=%d/%m/%Y", "TargetFormat=%s", LAST);`, true},
		{`web_convert_date_param("Day", "TargetFormat=%Y", LAST);`, true},
	}
	for _, tt := range tests {
		result := convertSingleWebSource(t, "\t"+tt.call)
		if len(result.Script.Actions) == 0 {
			t.Fatalf("%s produced no action", tt.call)
		}
		action := result.Script.Actions[0]
		if got := action.Type == "source_review"; got != tt.review {
			t.Errorf("%s: type = %s, review = %v; want review = %v", tt.call, action.Type, got, tt.review)
		}
	}
}
