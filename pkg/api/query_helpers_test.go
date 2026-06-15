package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueryFormat(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "absent", url: "/x", want: ""},
		{name: "lowercases", url: "/x?format=CSV", want: "csv"},
		{name: "trims whitespace", url: "/x?format=%20json%20", want: "json"},
		{name: "mixed case and spaces", url: "/x?format=%20Csv", want: "csv"},
		{name: "plain value", url: "/x?format=pdf", want: "pdf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			if got := queryFormat(req); got != tt.want {
				t.Fatalf("queryFormat(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestWantsCSV(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "absent", url: "/x", want: false},
		{name: "exact", url: "/x?format=csv", want: true},
		{name: "uppercase", url: "/x?format=CSV", want: true},
		{name: "padded", url: "/x?format=%20csv%20", want: true},
		{name: "other format", url: "/x?format=json", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			if got := wantsCSV(req); got != tt.want {
				t.Fatalf("wantsCSV(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}
