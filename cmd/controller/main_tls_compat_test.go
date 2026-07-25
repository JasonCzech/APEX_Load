package main

import "testing"

func TestApplyTLSCompatibilityDefaultsAddsRequiredFlagsOutsideProduction(t *testing.T) {
	env := map[string]string{"APEXLOAD_ENV": "development"}
	setenv := func(key, value string) error {
		env[key] = value
		return nil
	}
	getenv := func(key string) string { return env[key] }

	applied, value, err := applyTLSCompatibilityDefaults(getenv, setenv)
	if err != nil {
		t.Fatalf("applyTLSCompatibilityDefaults error: %v", err)
	}
	if !applied {
		t.Fatal("expected defaults to be applied")
	}
	if value == "" {
		t.Fatal("expected non-empty GODEBUG value")
	}
	for _, required := range []string{"x509negativeserial=1", "tlsmlkem=0", "tlssecpmlkem=0"} {
		if !containsToken(value, required) {
			t.Fatalf("GODEBUG %q missing token %q", value, required)
		}
	}
}

func TestApplyTLSCompatibilityDefaultsDoesNotModifyProductionByDefault(t *testing.T) {
	env := map[string]string{
		"APEXLOAD_ENV": "production",
		"GODEBUG":      "x509negativeserial=0",
	}
	setenv := func(key, value string) error {
		env[key] = value
		return nil
	}
	getenv := func(key string) string { return env[key] }

	applied, _, err := applyTLSCompatibilityDefaults(getenv, setenv)
	if err != nil {
		t.Fatalf("applyTLSCompatibilityDefaults error: %v", err)
	}
	if applied {
		t.Fatal("expected production env to skip compatibility defaults")
	}
	if env["GODEBUG"] != "x509negativeserial=0" {
		t.Fatalf("unexpected GODEBUG mutation: %q", env["GODEBUG"])
	}
}

func TestApplyTLSCompatibilityDefaultsAllowsProductionOptIn(t *testing.T) {
	env := map[string]string{
		"APEXLOAD_ENV":               "production",
		"APEXLOAD_ENABLE_TLS_COMPAT": "true",
	}
	setenv := func(key, value string) error {
		env[key] = value
		return nil
	}
	getenv := func(key string) string { return env[key] }

	applied, value, err := applyTLSCompatibilityDefaults(getenv, setenv)
	if err != nil {
		t.Fatalf("applyTLSCompatibilityDefaults error: %v", err)
	}
	if !applied {
		t.Fatal("expected production opt-in to apply compatibility defaults")
	}
	for _, required := range []string{"x509negativeserial=1", "tlsmlkem=0", "tlssecpmlkem=0"} {
		if !containsToken(value, required) {
			t.Fatalf("GODEBUG %q missing token %q", value, required)
		}
	}
}

func containsToken(csv, token string) bool {
	if csv == token {
		return true
	}
	for _, entry := range splitCSV(csv) {
		if entry == token {
			return true
		}
	}
	return false
}

func splitCSV(value string) []string {
	out := []string{}
	start := 0
	for i := 0; i <= len(value); i++ {
		if i == len(value) || value[i] == ',' {
			if i > start {
				out = append(out, value[start:i])
			}
			start = i + 1
		}
	}
	return out
}
