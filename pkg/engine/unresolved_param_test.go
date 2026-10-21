package engine

import (
	"reflect"
	"sort"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const poison = "{{param.missing}}"

func TestContainsUnresolvedParamTokenCaseAndBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"plain", "GET /a?x=" + poison, true},
		{"uppercase", "{{PARAM.Missing}}", true},
		{"mixed case", "{{PaRaM.x}}", true},
		{"token at very end", "trailing {{param.", true},
		{"second brace run matches", "{{ok}} then {{param.x}}", true},
		{"empty", "", false},
		{"no braces", "param.missing", false},
		{"truncated token", "{{para", false},
		{"one char short", "{{param", false},
		// Different namespaces must not trip the guard — only "{{param." does.
		{"params namespace", "{{params.x}}", false},
		{"underscore namespace", "{{param_x}}", false},
		{"env namespace", "{{env.PARAM}}", false},
		{"brace run without token", "{{{{{{", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsUnresolvedParamToken(tt.input); got != tt.want {
				t.Errorf("containsUnresolvedParamToken(%q) = %v, want %v", tt.input, got, tt.want)
			}
			if got := bytesContainUnresolvedParamToken([]byte(tt.input)); got != tt.want {
				t.Errorf("bytesContainUnresolvedParamToken(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// Every string-bearing field must trip the guard. The RedactedHeaders /
// RedactedHeaderList / RedactedParams / RedactedAuth / RedactedSOAP cases in
// particular were NOT detected by the previous json.Marshal catch-all, because
// those fields are tagged `json:"-"` and never appear in the encoding.
func TestActionContainsUnresolvedParameterDetectsEveryStringBearingField(t *testing.T) {
	cases := map[string]*protocol.Action{
		"Name":               {Name: poison},
		"Type":               {Type: poison},
		"Method":             {Method: poison},
		"Target":             {Target: poison},
		"RedactedTarget":     {RedactedTarget: poison},
		"Selector":           {Selector: poison},
		"RedactedSelector":   {RedactedSelector: poison},
		"Value":              {Value: poison},
		"RedactedValue":      {RedactedValue: poison},
		"BodyEncoding":       {BodyEncoding: poison},
		"Body":               {Body: []byte(poison)},
		"RedactedBody":       {RedactedBody: []byte(poison)},
		"Headers value":      {Headers: map[string]string{"X": poison}},
		"Headers key":        {Headers: map[string]string{poison: "v"}},
		"RedactedHeaders":    {RedactedHeaders: map[string]string{"X": poison}},
		"HeaderList":         {HeaderList: protocol.HeaderList{{Name: "X", Value: poison}}},
		"RedactedHeaderList": {RedactedHeaderList: protocol.HeaderList{{Name: "X", Value: poison}}},
		"Params":             {Params: map[string]any{"a": poison}},
		"Params nested map":  {Params: map[string]any{"a": map[string]any{"b": poison}}},
		"Params nested list": {Params: map[string]any{"a": []any{"ok", poison}}},
		"RedactedParams":     {RedactedParams: map[string]any{"a": poison}},
		"Metadata":           {Metadata: map[string]any{"a": poison}},
		"WaitFor":            {WaitFor: &protocol.WaitCondition{Value: poison}},
		"Sync":               {Sync: &protocol.BrowserSyncPolicy{Value: poison}},
		"Auth":               {Auth: &protocol.AuthConfig{Password: poison}},
		"RedactedAuth":       {RedactedAuth: &protocol.AuthConfig{Token: poison}},
		"SOAP":               {SOAP: &protocol.SOAPActionConfig{Action: poison}},
		"RedactedSOAP":       {RedactedSOAP: &protocol.SOAPActionConfig{Operation: poison}},
		"Extractors":         {Extractors: []protocol.Extractor{{Expression: poison}}},
		"Assertions":         {Assertions: []protocol.Assertion{{Value: poison}}},
		"Assertion Expected": {Assertions: []protocol.Assertion{{Expected: poison}}},
		"ThinkTime":          {ThinkTime: protocol.ThinkTimeConfig{Type: poison}},
	}
	for name, action := range cases {
		t.Run(name, func(t *testing.T) {
			if !actionContainsUnresolvedParameter(action) {
				t.Errorf("unresolved {{param.*}} in %s was not detected", name)
			}
		})
	}
}

func TestActionContainsUnresolvedParameterCleanActionPasses(t *testing.T) {
	if actionContainsUnresolvedParameter(nil) {
		t.Error("nil action must not be treated as unresolved")
	}
	clean := &protocol.Action{
		Name: "POST /checkout", Type: "http_request", Method: "POST",
		Target:  "https://shop.example.test/checkout?locale=en-US",
		Body:    []byte(`{"sku":"ABC-1","qty":2,"note":"braces {{ok}} are fine"}`),
		Headers: map[string]string{"Content-Type": "application/json"},
		Params:  map[string]any{"retries": 3, "tags": []any{"a", "b"}},
		Auth:    &protocol.AuthConfig{Type: "basic", Username: "u", Password: "p"},
	}
	if actionContainsUnresolvedParameter(clean) {
		t.Error("fully-resolved action was rejected")
	}
}

func TestActionContainsUnresolvedParameterChecksResolvedDefaultHeaders(t *testing.T) {
	tests := []protocol.Action{
		{DefaultHeaders: map[string]string{"X-Tenant": "{{param.tenant}}"}},
		{DefaultHeaderList: protocol.HeaderList{{Name: "X-Tenant", Value: "{{param.tenant}}"}}},
	}
	for index := range tests {
		if !actionContainsUnresolvedParameter(&tests[index]) {
			t.Fatalf("case %d: unresolved default header was not rejected", index)
		}
	}
}

// Structural tripwire: actionContainsUnresolvedParameter walks protocol.Action
// field by field (deliberately — the json.Marshal catch-all it replaced was
// both slow and blind to `json:"-"` fields). That trades a maintenance burden
// for speed, so this test fails when the struct gains or loses a field, forcing
// a decision about whether the new field can carry an interpolated value.
func TestActionUnresolvedParameterScanCoversEveryStringField(t *testing.T) {
	// Fields the scan intentionally ignores because they are numeric/boolean
	// settings or strictly validated policy scalars that cannot be interpolated.
	nonStringFields := map[string]bool{
		"BrowserActionExecution": true,
		"BrowserAuthoredStep":    true, // Engine-owned canonical path/digests; not an interpolated execution field.
		"HTTPVersion":            true, // Strict leaf enum; invalid/template values fail before traffic.
		"TimeoutMs":              true, "MaxRedirects": true, "DiscardResponseBodyOnSuccess": true,
		"Capture":                    true, // BrowserCaptureOptions is all bools
		"CaptureMessageDetail":       true,
		"CaptureResourceDetails":     true,
		"MessageDetailMaxItems":      true,
		"MessageDetailMaxItemBytes":  true,
		"MessageDetailMaxTotalBytes": true,
		"MessageDetailPriority":      true,
		"MessageDetailRetained":      true,
		"ResolvedDefaultHeaders":     true,
	}
	covered := map[string]bool{
		"Name": true, "Type": true, "Method": true, "Target": true, "RedactedTarget": true,
		"Selector": true, "RedactedSelector": true, "Value": true, "RedactedValue": true,
		"Headers": true, "HeaderList": true, "RedactedHeaders": true, "RedactedHeaderList": true,
		"DefaultHeaders": true, "DefaultHeaderList": true,
		"Body": true, "BodyEncoding": true, "RedactedBody": true,
		"Params": true, "RedactedParams": true, "Metadata": true,
		"HierarchyPath": true, "PageName": true,
		"MessageDetailMode": true,
		"WaitFor":           true, "Sync": true, "Extractors": true, "Assertions": true,
		"ThinkTime": true, "Auth": true, "RedactedAuth": true, "SOAP": true, "RedactedSOAP": true,
	}

	var missing []string
	actionType := reflect.TypeOf(protocol.Action{})
	for i := 0; i < actionType.NumField(); i++ {
		name := actionType.Field(i).Name
		if nonStringFields[name] || covered[name] {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("protocol.Action gained field(s) %v — extend actionContainsUnresolvedParameter "+
			"(and this test) if they can carry an interpolated value, or add them to nonStringFields", missing)
	}

	present := map[string]bool{}
	for i := 0; i < actionType.NumField(); i++ {
		present[actionType.Field(i).Name] = true
	}
	var stale []string
	for name := range covered {
		if !present[name] {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("this test lists field(s) %v that protocol.Action no longer has", stale)
	}
}
