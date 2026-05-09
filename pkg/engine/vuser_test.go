package engine

import (
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func TestVUser_Interpolate(t *testing.T) {
	vu := &VUser{
		Variables: map[string]string{
			"token": "abc123",
			"host":  "example.com",
		},
	}

	tests := []struct {
		input string
		want  string
	}{
		{"{{var.token}}", "abc123"},
		{"https://{{var.host}}/api", "https://example.com/api"},
		{"Bearer {{var.token}}", "Bearer abc123"},
		{"no-vars-here", "no-vars-here"},
		{"", ""},
	}

	for _, tt := range tests {
		got := vu.interpolate(tt.input)
		if got != tt.want {
			t.Errorf("interpolate(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestVUser_InterpolateMap(t *testing.T) {
	vu := &VUser{
		Variables: map[string]string{"env": "staging"},
	}

	m := map[string]string{
		"Host":          "{{var.env}}.example.com",
		"Authorization": "static-value",
	}

	got := vu.interpolateMap(m)
	if got["Host"] != "staging.example.com" {
		t.Errorf("Host = %q, want %q", got["Host"], "staging.example.com")
	}
	if got["Authorization"] != "static-value" {
		t.Errorf("Authorization = %q, want %q", got["Authorization"], "static-value")
	}
}

func TestVUser_InterpolateMap_Nil(t *testing.T) {
	vu := &VUser{Variables: map[string]string{}}
	if got := vu.interpolateMap(nil); got != nil {
		t.Errorf("interpolateMap(nil) = %v, want nil", got)
	}
}

func TestVUser_MergeExtractions(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"existing": "value"}}
	vu.mergeExtractions(&protocol.Result{
		Extractions: map[string]string{
			"token":    "new-token",
			"existing": "overwritten",
		},
	})

	if vu.Variables["token"] != "new-token" {
		t.Errorf("token = %q", vu.Variables["token"])
	}
	if vu.Variables["existing"] != "overwritten" {
		t.Errorf("existing should be overwritten, got %q", vu.Variables["existing"])
	}
}

func TestVUser_ResolveAction(t *testing.T) {
	vu := &VUser{
		Variables: map[string]string{"base": "https://api.test.com"},
	}

	action := dsl.Action{
		Name:    "get-user",
		Type:    "request",
		Method:  "GET",
		Target:  "{{var.base}}/users/1",
		Headers: map[string]string{"Accept": "application/json"},
		Body:    `{"key": "{{var.base}}"}`,
		Extractors: []dsl.Extractor{
			{Name: "id", Source: "body", Type: "jsonpath", Expression: "$.id"},
		},
		Assertions: []dsl.Assertion{
			{Type: "status_code", Expected: 200},
		},
	}

	resolved := vu.resolveAction(action)

	if resolved.Name != "get-user" {
		t.Errorf("Name = %q", resolved.Name)
	}
	if resolved.Target != "https://api.test.com/users/1" {
		t.Errorf("Target = %q", resolved.Target)
	}
	if string(resolved.Body) != `{"key": "https://api.test.com"}` {
		t.Errorf("Body = %q", string(resolved.Body))
	}
	if len(resolved.Extractors) != 1 {
		t.Fatalf("Extractors count = %d", len(resolved.Extractors))
	}
	if len(resolved.Assertions) != 1 {
		t.Fatalf("Assertions count = %d", len(resolved.Assertions))
	}
}

func TestSnapshotStore(t *testing.T) {
	store := NewSnapshotStore(3)

	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://a.com"})
	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://b.com"})
	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://c.com"})
	store.Add(&protocol.ErrorSnapshot{RequestURL: "http://d.com"}) // should be dropped (max 3)

	snaps := store.Snapshots()
	if len(snaps) != 3 {
		t.Errorf("snapshots count = %d, want 3", len(snaps))
	}
}

func TestSnapshotStore_NilSafe(t *testing.T) {
	var store *SnapshotStore
	store.Add(&protocol.ErrorSnapshot{}) // should not panic
	if store.Snapshots() != nil {
		t.Error("nil store Snapshots() should return nil")
	}
}

func TestSnapshotStore_NilSnapshot(t *testing.T) {
	store := NewSnapshotStore(10)
	store.Add(nil) // should not panic or add
	if len(store.Snapshots()) != 0 {
		t.Error("nil snapshot should not be added")
	}
}

func TestReplaceAll(t *testing.T) {
	tests := []struct {
		s, old, new, want string
	}{
		{"hello world", "world", "go", "hello go"},
		{"aaa", "a", "bb", "bbbbbb"},
		{"no match", "xyz", "abc", "no match"},
		{"", "a", "b", ""},
	}

	for _, tt := range tests {
		got := replaceAll(tt.s, tt.old, tt.new)
		if got != tt.want {
			t.Errorf("replaceAll(%q, %q, %q) = %q, want %q", tt.s, tt.old, tt.new, got, tt.want)
		}
	}
}

func TestIndexOf(t *testing.T) {
	tests := []struct {
		s, substr string
		want      int
	}{
		{"hello world", "world", 6},
		{"hello world", "xyz", -1},
		{"aaa", "a", 0},
		{"", "a", -1},
	}

	for _, tt := range tests {
		got := indexOf(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("indexOf(%q, %q) = %d, want %d", tt.s, tt.substr, got, tt.want)
		}
	}
}

func TestToProtocolExtractors(t *testing.T) {
	exts := []dsl.Extractor{
		{Name: "token", Source: "body", Type: "jsonpath", Expression: "$.token"},
		{Name: "header_val", Source: "header", Type: "regex", Expression: "X-Request-Id: (.+)"},
	}

	result := toProtocolExtractors(exts)
	if len(result) != 2 {
		t.Fatalf("count = %d, want 2", len(result))
	}
	if result[0].Name != "token" || result[0].Source != "body" {
		t.Errorf("extractor[0] = %+v", result[0])
	}
}

func TestToProtocolAssertions(t *testing.T) {
	asserts := []dsl.Assertion{
		{Type: "status_code", Expected: 200},
		{Type: "response_time", Max: 5000},
	}

	result := toProtocolAssertions(asserts)
	if len(result) != 2 {
		t.Fatalf("count = %d, want 2", len(result))
	}
	if result[0].Type != "status_code" {
		t.Errorf("assertion[0].Type = %q", result[0].Type)
	}
	if result[1].Max != 5000 {
		t.Errorf("assertion[1].Max = %d, want 5000", result[1].Max)
	}
}

func TestVUser_HandleSnapshot(t *testing.T) {
	store := NewSnapshotStore(10)
	vu := &VUser{SnapshotStore: store}
	errCfg := dsl.ErrorHandlingConfig{SnapshotOnError: true}

	vu.handleSnapshot(&protocol.Result{
		Success:  false,
		Snapshot: &protocol.ErrorSnapshot{RequestURL: "http://fail.com"},
	}, errCfg)

	if len(store.Snapshots()) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(store.Snapshots()))
	}

	// Success should not add snapshot
	vu.handleSnapshot(&protocol.Result{
		Success:  true,
		Snapshot: &protocol.ErrorSnapshot{RequestURL: "http://ok.com"},
	}, errCfg)

	if len(store.Snapshots()) != 1 {
		t.Errorf("success should not add snapshot, got %d", len(store.Snapshots()))
	}
}
