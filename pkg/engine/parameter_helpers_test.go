package engine

import (
	"strconv"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func sourceParameterAction(function string, args ...any) dsl.Action {
	return dsl.Action{Type: "source_parameter", Name: function, Params: map[string]any{"function": function, "args": args}}
}

func TestWebParameterHelpers(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"token": "abcdef", "day": "07/03/2026", "bad": "not a date"}}

	vu.handleSourceParameterAction("web_save_timestamp_param", sourceParameterAction("web_save_timestamp_param", "TS"))
	first := vu.Variables["ts"]
	if len(first) != 13 {
		t.Fatalf("timestamp = %q, want 13 digits", first)
	}
	time.Sleep(2 * time.Millisecond)
	vu.handleSourceParameterAction("web_save_timestamp_param", sourceParameterAction("web_save_timestamp_param", "TS"))
	firstValue, _ := strconv.ParseInt(first, 10, 64)
	second, _ := strconv.ParseInt(vu.Variables["ts"], 10, 64)
	if second <= firstValue {
		t.Fatalf("timestamps %d then %d, want a fresh value on each call", firstValue, second)
	}

	vu.handleSourceParameterAction("web_save_param_length", sourceParameterAction("web_save_param_length", "Token"))
	if got := vu.Variables["token_length"]; got != "6" {
		t.Fatalf("Token_Length = %q, want 6", got)
	}

	convert := sourceParameterAction("web_convert_date_param", "Day", "SourceFormat=%d/%m/%Y", "TargetFormat=%Y-%m-%d")
	vu.handleSourceParameterAction("web_convert_date_param", convert)
	if got := vu.Variables["day"]; got != "2026-03-07" {
		t.Fatalf("Day = %q, want 2026-03-07", got)
	}
	vu.handleSourceParameterAction("web_convert_date_param", sourceParameterAction("web_convert_date_param", "Bad", "SourceFormat=%d/%m/%Y", "TargetFormat=%Y"))
	if got := vu.Variables["bad"]; got != "not a date" {
		t.Fatalf("an unparseable date must be left unchanged, got %q", got)
	}
}
