package refkey

import (
	"testing"
	"time"
)

func TestEnvCode(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{"local", "local", "LOC"},
		{"docker", "docker", "DOC"},
		{"kubernetes", "kubernetes", "KUB"},
		{"known case-insensitive", "Kubernetes", "KUB"},
		{"known trimmed", "  local  ", "LOC"},
		{"custom prod", "prod", "PROD"},
		{"custom staging truncated", "staging", "STAG"},
		{"custom sanitized", "us-east-1", "USEA"},
		{"custom uppercased", "qa", "QA"},
		{"empty falls back", "", "ENV"},
		{"non-alphanumeric falls back", "---", "ENV"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EnvCode(tt.env); got != tt.want {
				t.Errorf("EnvCode(%q) = %q, want %q", tt.env, got, tt.want)
			}
		})
	}
}

func TestScript(t *testing.T) {
	if got := Script("local", 42, 3); got != "LOC-SCR-0042-v3" {
		t.Errorf("Script = %q, want LOC-SCR-0042-v3", got)
	}
	if got := Script("local", 1, 1); got != "LOC-SCR-0001-v1" {
		t.Errorf("Script = %q, want LOC-SCR-0001-v1", got)
	}
	// version < 1 is clamped to 1.
	if got := Script("local", 5, 0); got != "LOC-SCR-0005-v1" {
		t.Errorf("Script with version 0 = %q, want LOC-SCR-0005-v1", got)
	}
	// sequences beyond the pad width still render fully.
	if got := Script("prod", 12345, 2); got != "PROD-SCR-12345-v2" {
		t.Errorf("Script overflow = %q, want PROD-SCR-12345-v2", got)
	}
}

func TestScenario(t *testing.T) {
	if got := Scenario("docker", 7); got != "DOC-SCN-0007" {
		t.Errorf("Scenario = %q, want DOC-SCN-0007", got)
	}
}

func TestRun(t *testing.T) {
	day := time.Date(2026, 6, 3, 14, 30, 0, 0, time.UTC)
	if got := Run("local", day, 1); got != "LOC-RUN-20260603-001" {
		t.Errorf("Run = %q, want LOC-RUN-20260603-001", got)
	}
	if got := Run("kubernetes", day, 123); got != "KUB-RUN-20260603-123" {
		t.Errorf("Run = %q, want KUB-RUN-20260603-123", got)
	}
}

func TestParseRoundTrip(t *testing.T) {
	day := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		key  string
		want Parts
	}{
		{Script("local", 42, 3), Parts{Env: "LOC", Type: TypeScript, Seq: 42, Version: 3}},
		{Scenario("docker", 7), Parts{Env: "DOC", Type: TypeScenario, Seq: 7}},
		{Run("local", day, 1), Parts{Env: "LOC", Type: TypeRun, Seq: 1, Date: day}},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, ok := Parse(tt.key)
			if !ok {
				t.Fatalf("Parse(%q) ok=false, want true", tt.key)
			}
			if got.Env != tt.want.Env || got.Type != tt.want.Type || got.Seq != tt.want.Seq || got.Version != tt.want.Version {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.key, got, tt.want)
			}
			if tt.want.Type == TypeRun && !got.Date.Equal(tt.want.Date) {
				t.Errorf("Parse(%q) date = %v, want %v", tt.key, got.Date, tt.want.Date)
			}
		})
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	bad := []string{
		"",
		"not-a-key",
		"550e8400-e29b-41d4-a716-446655440000", // UUID
		"LOC-SCR-0042",                         // script missing version
		"LOC-SCR-0042-3",                       // version missing v prefix
		"LOC-SCR-0042-vX",                      // non-numeric version
		"LOC-SCN-0007-extra",                   // scenario with trailing segment
		"LOC-RUN-2026XXYY-001",                 // bad date
		"LOC-RUN-20260603",                     // run missing seq
		"LOC-XXX-0001",                         // unknown type
		"-SCR-0001-v1",                         // empty env
	}
	for _, key := range bad {
		t.Run(key, func(t *testing.T) {
			if _, ok := Parse(key); ok {
				t.Errorf("Parse(%q) ok=true, want false", key)
			}
			if Looks(key) {
				t.Errorf("Looks(%q) = true, want false", key)
			}
		})
	}
}

func TestLooks(t *testing.T) {
	if !Looks("LOC-RUN-20260603-001") {
		t.Error("Looks should accept a well-formed run key")
	}
	if !Looks("loc-scr-0001-V2") {
		t.Error("Looks should accept case-insensitive pasted keys")
	}
	if Looks("550e8400-e29b-41d4-a716-446655440000") {
		t.Error("Looks should reject a UUID")
	}
}
