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

func TestHTTPSequenceChildBodyMultipartAndAttributionValidation(t *testing.T) {
	validMultipart := &MultipartBody{
		Parts: []MultipartPart{
			{Name: "note", Value: "hello {{var.user}}"},
			{Name: "attachment", Value: "{{file.upload_doc}}", Filename: "report.txt", ContentType: "text/plain", Headers: map[string]string{"Content-Transfer-Encoding": "binary"}},
		},
	}
	validDataSources := []DataSource{{
		Name:       "upload_doc",
		Type:       "file",
		FileName:   "report.txt",
		InlineData: "bnVtYmVycwo=",
		Encoding:   "base64",
	}}

	for _, tc := range []struct {
		name     string
		mode     string
		requests any
	}{
		{
			name: "sequential map slice with typed pointers",
			requests: []map[string]any{{
				"name":             "Upload",
				"type":             "POST",
				"method":           "POST",
				"target":           "/upload",
				"body_multipart":   validMultipart,
				"think_time_after": true,
				"attribution":      &AnalyticsAttribution{HierarchyPath: "Checkout/Upload", PageName: "UploadPage"},
			}},
		},
		{
			name: "concurrent Action slice",
			mode: HTTPSequenceConcurrent,
			requests: []Action{{
				Name:          "Upload",
				Type:          "POST",
				Method:        "POST",
				Target:        "/upload",
				BodyMultipart: validMultipart,
				Attribution:   &AnalyticsAttribution{HierarchyPath: "Checkout/Upload", PageName: "UploadPage"},
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"requests": tc.requests}
			if tc.mode != "" {
				params["execution_mode"] = tc.mode
			}
			script := &Script{
				Name:        "seq-multipart",
				Protocol:    "http",
				Config:      ScriptConfig{BaseURL: "https://example.test"},
				DataSources: validDataSources,
				Actions: []Action{{
					Name:   "tx",
					Type:   "http_sequence",
					Method: "SEQUENCE",
					Target: "/upload",
					Params: params,
				}},
			}
			if issues := ValidateScriptIssues(script); len(issues) != 0 {
				t.Fatalf("ValidateScriptIssues(in-memory) = %v, want none", issues)
			}
			encoded, err := json.Marshal(script)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			var decoded Script
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("json.Unmarshal: %v", err)
			}
			if issues := ValidateScriptIssues(&decoded); len(issues) != 0 {
				t.Fatalf("ValidateScriptIssues(round-trip) = %v, want none", issues)
			}

			// Removing the file data source must fail file reference validation across both forms.
			scriptNoDS := *script
			scriptNoDS.DataSources = nil
			if issues := ValidateScriptIssues(&scriptNoDS); !strings.Contains(strings.Join(issues, "\n"), `references unknown file asset "upload_doc"`) {
				t.Fatalf("ValidateScriptIssues(in-memory missing file) = %v, want unknown file asset issue", issues)
			}
			decoded.DataSources = nil
			if issues := ValidateScriptIssues(&decoded); !strings.Contains(strings.Join(issues, "\n"), `references unknown file asset "upload_doc"`) {
				t.Fatalf("ValidateScriptIssues(round-trip missing file) = %v, want unknown file asset issue", issues)
			}
		})
	}

	for _, tc := range []struct {
		name            string
		child           map[string]any
		wantIssueSubstr string
	}{
		{
			name: "multipart with body",
			child: map[string]any{
				"name": "Upload", "method": "POST", "target": "/upload",
				"body":           "conflict",
				"body_multipart": &MultipartBody{Parts: []MultipartPart{{Name: "a", Value: "b"}}},
			},
			wantIssueSubstr: "body_multipart replaces body, body_file and body_encoding",
		},
		{
			name: "multipart with empty parts",
			child: map[string]any{
				"name": "Upload", "method": "POST", "target": "/upload",
				"body_multipart": &MultipartBody{},
			},
			wantIssueSubstr: "body_multipart needs 1 to ",
		},
		{
			name: "multipart with bad encoding",
			child: map[string]any{
				"name": "Upload", "method": "POST", "target": "/upload",
				"body_multipart": &MultipartBody{Parts: []MultipartPart{{Name: "a", Value: "b", ValueEncoding: "hex"}}},
			},
			wantIssueSubstr: "value_encoding must be base64, windows-1252 or empty",
		},
		{
			name: "multipart with malformed shape",
			child: map[string]any{
				"name": "Upload", "method": "POST", "target": "/upload",
				"body_multipart": "not-an-object",
			},
			wantIssueSubstr: "body_multipart must be a valid multipart body",
		},
		{
			name: "attribution with oversized page_name",
			child: map[string]any{
				"name": "Upload", "method": "POST", "target": "/upload",
				"attribution": &AnalyticsAttribution{PageName: strings.Repeat("p", 513)},
			},
			wantIssueSubstr: "attribution page_name must not exceed 512 bytes",
		},
		{
			name: "attribution with malformed shape",
			child: map[string]any{
				"name": "Upload", "method": "POST", "target": "/upload",
				"attribution": "not-an-object",
			},
			wantIssueSubstr: "attribution must be a valid attribution object",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := &Script{
				Name:     "invalid-seq-child",
				Protocol: "http",
				Config:   ScriptConfig{BaseURL: "https://example.test"},
				Actions: []Action{{
					Name:   "tx",
					Type:   "http_sequence",
					Params: map[string]any{"requests": []map[string]any{tc.child}},
				}},
			}
			issues := ValidateScriptIssues(script)
			if len(issues) == 0 {
				t.Fatalf("expected validation issue containing %q for %s, got none", tc.wantIssueSubstr, tc.name)
			}
			joined := strings.Join(issues, "\n")
			if !strings.Contains(joined, tc.wantIssueSubstr) {
				t.Fatalf("ValidateScriptIssues(%s) = %v, want substring %q", tc.name, issues, tc.wantIssueSubstr)
			}
		})
	}

	// Same-group extractor dependency inside a concurrent child's body_multipart must be rejected.
	concurrentDeps := []Action{
		{
			Name: "first", Type: "GET", Method: "GET", Target: "/token",
			Extractors: []Extractor{{Name: "csrf", Source: "body", Type: "regex", Expression: "token=(.+)"}},
		},
		{
			Name: "second", Type: "POST", Method: "POST", Target: "/upload",
			BodyMultipart: &MultipartBody{Parts: []MultipartPart{{Name: "csrf", Value: "{{var.csrf}}"}}},
		},
	}
	if err := ValidateHTTPConcurrentChildren(concurrentDeps); err == nil || !strings.Contains(err.Error(), "cannot depend on group extractor outputs") {
		t.Fatalf("ValidateHTTPConcurrentChildren(multipart dependency) = %v, want group extractor dependency rejection", err)
	}
	if err := ValidateHTTPConcurrentChild(Action{
		Name: "conflict", Type: "POST", Method: "POST", Target: "/upload",
		Body:          "raw",
		BodyMultipart: &MultipartBody{Parts: []MultipartPart{{Name: "a", Value: "b"}}},
	}); err == nil {
		t.Fatal("ValidateHTTPConcurrentChild admitted child combining body and body_multipart")
	}
}
