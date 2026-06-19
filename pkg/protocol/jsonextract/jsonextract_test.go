package jsonextract

import "testing"

func TestJSONPath(t *testing.T) {
	body := []byte(`{"data":{"items":[{"id":"a"},{"id":"b"}]},"count":2}`)
	cases := []struct {
		expr string
		want string
	}{
		{"$.data.items[1].id", "b"},
		{"data.items[0].id", "a"},
		{"$.count", "2"},
		{"$", `{"count":2,"data":{"items":[{"id":"a"},{"id":"b"}]}}`},
	}
	for _, tc := range cases {
		got, err := JSONPath(tc.expr, body)
		if err != nil {
			t.Fatalf("JSONPath(%q) error: %v", tc.expr, err)
		}
		if got != tc.want {
			t.Fatalf("JSONPath(%q) = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestJSONPathErrors(t *testing.T) {
	body := []byte(`{"data":{"items":[]}}`)
	for _, expr := range []string{"", "$.data.items[5]", "$.missing"} {
		if _, err := JSONPath(expr, body); err == nil {
			t.Fatalf("JSONPath(%q) expected error, got nil", expr)
		}
	}
	if _, err := JSONPath("$.x", []byte("not json")); err == nil {
		t.Fatalf("JSONPath on invalid JSON expected error")
	}
}

func TestJMESPath(t *testing.T) {
	body := []byte(`{"users":[{"name":"ann"},{"name":"bob"}]}`)
	got, err := JMESPath("users[1].name", body)
	if err != nil {
		t.Fatalf("JMESPath error: %v", err)
	}
	if got != "bob" {
		t.Fatalf("JMESPath = %q, want %q", got, "bob")
	}
}

func TestRegex(t *testing.T) {
	body := []byte("token=abc123;")
	got, err := Regex(`token=([a-z0-9]+)`, body)
	if err != nil {
		t.Fatalf("Regex error: %v", err)
	}
	if got != "abc123" {
		t.Fatalf("Regex = %q, want %q", got, "abc123")
	}
	if _, err := Regex(`nope=(\d+)`, body); err == nil {
		t.Fatalf("Regex no-match expected error")
	}
	if _, err := Regex(`(`, body); err == nil {
		t.Fatalf("Regex invalid pattern expected error")
	}
}

func TestBoundary(t *testing.T) {
	body := []byte("<<START>>payload<<END>>")
	got, err := Boundary("<<START>>|||<<END>>", body)
	if err != nil {
		t.Fatalf("Boundary error: %v", err)
	}
	if got != "payload" {
		t.Fatalf("Boundary = %q, want %q", got, "payload")
	}
	if _, err := Boundary("no-delim", body); err == nil {
		t.Fatalf("Boundary malformed expr expected error")
	}
	if _, err := Boundary("X|||Y", body); err == nil {
		t.Fatalf("Boundary missing left expected error")
	}
}

func TestStringify(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"text", "text"},
		{float64(3), "3"},
		{true, "true"},
		{map[string]any{"a": float64(1)}, `{"a":1}`},
	}
	for _, tc := range cases {
		if got := Stringify(tc.in); got != tc.want {
			t.Fatalf("Stringify(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
