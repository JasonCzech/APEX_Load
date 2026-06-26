package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseRecordArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want recordArgs
	}{
		{
			name: "defaults",
			args: nil,
			want: recordArgs{out: "recording.json", harOut: "recording.har", api: "http://localhost:8080"},
		},
		{
			name: "full flags",
			args: []string{"--url", "https://shop.example", "--out", "login.json", "--har", "net.har",
				"--name", "Login Flow", "--api", "http://ctl:8080", "--api-key", "key-123", "--bearer-token", "token-456", "--project", "proj-1",
				"--upload", "--headless", "--max-think-ms", "4000"},
			want: recordArgs{
				url: "https://shop.example", out: "login.json", harOut: "net.har",
				name: "Login Flow", api: "http://ctl:8080", apiKey: "key-123", bearerToken: "token-456", project: "proj-1",
				upload: true, headless: true, maxThinkMs: 4000,
			},
		},
		{
			name: "har default derives from out",
			args: []string{"--out", "flows/checkout.json"},
			want: recordArgs{out: "flows/checkout.json", harOut: "flows/checkout.har", api: "http://localhost:8080"},
		},
		{
			name: "no-har disables side-car",
			args: []string{"--no-har"},
			want: recordArgs{out: "recording.json", noHAR: true, api: "http://localhost:8080"},
		},
		{
			name: "short flags",
			args: []string{"-u", "https://a.example", "-o", "a.json", "-n", "A"},
			want: recordArgs{url: "https://a.example", out: "a.json", harOut: "a.har", name: "A", api: "http://localhost:8080"},
		},
		{
			name: "invalid max think ignored",
			args: []string{"--max-think-ms", "nope"},
			want: recordArgs{out: "recording.json", harOut: "recording.har", api: "http://localhost:8080"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_API", "")
			got := parseRecordArgs(tt.args)
			if got != tt.want {
				t.Errorf("parseRecordArgs(%v) =\n  %+v\nwant\n  %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestUploadScriptWithProject(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scripts" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("content type = %q, want application/json", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"script-9"}`))
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	id, err := client.UploadScriptWithProject("rec.json", []byte(`{"name":"Rec","protocol":"browser","actions":[]}`), "proj-7")
	if err != nil {
		t.Fatalf("UploadScriptWithProject: %v", err)
	}
	if id != "script-9" {
		t.Errorf("id = %q", id)
	}
	if captured["project_id"] != "proj-7" {
		t.Errorf("project_id = %v", captured["project_id"])
	}
	script, ok := captured["script"].(map[string]any)
	if !ok || script["name"] != "Rec" {
		t.Errorf("script wrapper = %v", captured["script"])
	}
}

func TestUploadScriptWithProjectErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"validation failed"}`, http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewApiClient(server.URL)
	if _, err := client.UploadScriptWithProject("rec.json", []byte(`{}`), ""); err == nil {
		t.Error("expected error for bad status")
	}
	if _, err := client.UploadScriptWithProject("rec.json", []byte("not json"), ""); err == nil {
		t.Error("expected error for invalid script JSON")
	}
}
