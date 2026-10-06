package dsl

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestHTTPSequenceConcurrencySettings(t *testing.T) {
	for _, tc := range []struct {
		name          string
		mode          any
		present       bool
		fanout        any
		fanoutPresent bool
		count         int
		valid         bool
		want          int
	}{
		{name: "absent serial", count: 2, valid: true, want: 2},
		{name: "explicit sequential", mode: "sequential", present: true, count: 2, valid: true, want: 2},
		{name: "concurrent default", mode: "concurrent", present: true, count: 10, valid: true, want: 8},
		{name: "concurrent bounded", mode: "concurrent", present: true, count: 256, fanout: 16, fanoutPresent: true, valid: true, want: 16},
		{name: "oversized group", mode: "concurrent", present: true, count: 257},
		{name: "serial large unchanged", count: 257, valid: true, want: 8},
		{name: "empty mode", mode: "", present: true, count: 1},
		{name: "unknown mode", mode: "parallel", present: true, count: 1},
		{name: "object mode", mode: map[string]any{}, present: true, count: 1},
		{name: "fraction", mode: "concurrent", present: true, fanout: 1.2, fanoutPresent: true, count: 2},
		{name: "zero", fanout: 0, fanoutPresent: true, count: 2},
		{name: "inactive invalid", mode: "sequential", present: true, fanout: 17, fanoutPresent: true, count: 2},
		{name: "numeric string", fanout: "2", fanoutPresent: true, count: 2},
		{name: "number wire", fanout: json.Number("2"), fanoutPresent: true, count: 2, valid: true, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"requests": make([]Action, tc.count)}
			if tc.present {
				params["execution_mode"] = tc.mode
			}
			if tc.fanoutPresent {
				params["max_concurrency"] = tc.fanout
			}
			_, fanout, err := HTTPSequenceExecutionSettings(Action{Params: params})
			if (err == nil) != tc.valid || (err == nil && fanout != tc.want) {
				t.Fatalf("valid=%t fanout=%d", err == nil, fanout)
			}
		})
	}
}
func TestConcurrentHTTPChildAdmission(t *testing.T) {
	valid := Action{Type: "request", Method: "GET", Target: "/path"}
	if err := ValidateHTTPConcurrentChild(valid); err != nil {
		t.Fatal(err)
	}
	for _, child := range []Action{{Type: "noop", Method: "GET", Target: "/path"}, {Type: "POST", Target: "/path", ThinkTime: ThinkTimeConfig{MeanMs: 1}}, {Type: "GET", Target: "/path", Control: &ControlFlow{}}, {Type: "GET", Target: "/path", BodyFile: "request.txt"}, {Type: "GET"}} {
		if ValidateHTTPConcurrentChild(child) == nil {
			t.Fatal("invalid child admitted")
		}
	}
	spec, _ := SpecForActionType("http_sequence")
	group := Action{Name: "group", Type: "http_sequence", Params: map[string]any{"execution_mode": "concurrent", "requests": []Action{{Type: "GET", Target: "/a", Extractors: []Extractor{{Name: "same", Source: "body", Type: "jsonpath", Expression: "$.value"}}}, {Type: "GET", Target: "/b", Extractors: []Extractor{{Name: "same", Source: "body", Type: "jsonpath", Expression: "$.value"}}}}}}
	if len(spec.Validate(0, group)) == 0 {
		t.Fatal("duplicate extractor names admitted")
	}
}

func TestConcurrentHTTPSourceAdmissionBoundsAndAccounting(t *testing.T) {
	valid := Action{Type: "POST", Target: "/path", Body: "ordinary"}
	size, work, err := HTTPConcurrentPreparationSourceSizeWithWorkLimit(MaxHTTPConcurrentPreparationWork, valid)
	if err != nil || size <= 0 || work <= size {
		t.Fatal("missing source inspection work")
	}
	if _, _, err := HTTPConcurrentPreparationSourceSizeWithWorkLimit(work-1, valid); err == nil {
		t.Fatal("source work bound ignored")
	}
	b := httpConcurrentSourceBudget{remaining: 16, nodes: 100, workRemaining: 16}
	if err := b.walk(reflect.ValueOf(strings.Repeat("x", 1<<20)), 0, nil); err == nil || b.workRemaining != 0 {
		t.Fatal("large field scanned beyond admission")
	}
	for _, value := range []string{string([]byte{0xff}), strings.Repeat("\x00", MaxHTTPConcurrentPreparationBytes/6)} {
		if _, err := HTTPConcurrentPreparationSourceSize(value); err == nil {
			t.Fatal("invalid or escaped oversize source admitted")
		}
	}
	for _, tt := range []ThinkTimeConfig{{MinPercent: 1}, {MaxPercent: 1}} {
		a := valid
		a.ThinkTime = tt
		if ValidateHTTPConcurrentChild(a) == nil {
			t.Fatal("child percentage wait ignored")
		}
	}
	children := []Action{{Type: "POST", Target: "/first", Body: "order=%7B%7Bvar.items_count%7D%7D", Extractors: []Extractor{{Name: "items", Mode: "all"}}}, {Type: "GET", Target: "/second"}}
	if ValidateHTTPConcurrentChildren(children) == nil {
		t.Fatal("decoded group dependency admitted")
	}
	children[0].Body = "order=%41%41"
	if _, err := ValidateHTTPConcurrentChildrenWithWorkLimit(children, 1); err == nil {
		t.Fatal("source work exhaustion admitted")
	}
	if _, found := HTTPConcurrentExtractorOutputSource("items_note", map[string]string{"items": "all"}); found {
		t.Fatal("unrelated suffix treated as output")
	}
}

func TestHTTPSequenceRequestTransactionsReadsTypedAndStoredGroups(t *testing.T) {
	children := []Action{
		{Name: "a", Type: "GET", Target: "/a", Metadata: map[string]any{HTTPConcurrentRequestTransactionMetadataKey: "first"}},
		{Name: "b", Type: "GET", Target: "/b", Metadata: map[string]any{HTTPConcurrentRequestTransactionMetadataKey: "second"}},
	}
	group := Action{Name: "group", Type: "http_sequence", Params: map[string]any{"execution_mode": HTTPSequenceConcurrent, "requests": children}}
	want := []string{"first", "second"}
	if got := HTTPSequenceRequestTransactions(group); !reflect.DeepEqual(got, want) {
		t.Fatalf("typed group = %v", got)
	}
	wire, err := json.Marshal(group)
	if err != nil {
		t.Fatal(err)
	}
	var stored Action
	if err := json.Unmarshal(wire, &stored); err != nil {
		t.Fatal(err)
	}
	if got := HTTPSequenceRequestTransactions(stored); !reflect.DeepEqual(got, want) {
		t.Fatalf("stored group = %v", got)
	}
	sequential := stored
	sequential.Params = map[string]any{"requests": stored.Params["requests"]}
	if got := HTTPSequenceRequestTransactions(sequential); got != nil {
		t.Fatalf("sequential group = %v", got)
	}
	partial := append([]Action(nil), children...)
	partial[1].Metadata = nil
	group.Params["requests"] = partial
	if got := HTTPSequenceRequestTransactions(group); got != nil {
		t.Fatalf("partly named group = %v", got)
	}
	if _, err := ValidateHTTPConcurrentChildrenWithWorkLimit(partial, MaxHTTPConcurrentPreparationWork); err == nil || !strings.Contains(err.Error(), "every child or none") {
		t.Fatalf("partly named group admitted: %v", err)
	}
	partial[1].Metadata = map[string]any{HTTPConcurrentRequestTransactionMetadataKey: 7}
	if _, err := ValidateHTTPConcurrentChildrenWithWorkLimit(partial, MaxHTTPConcurrentPreparationWork); err == nil {
		t.Fatal("non-text transaction name admitted")
	}
}
