package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestProtectedOpaqueImportSourcesPersistExactlyAndProjectSafely(t *testing.T) {
	const (
		proxyPassword = "opaque-enterprise-proxy-password-5e72"
		jdbcPassword  = "opaque-jmeter-jdbc-password-8a31"
	)
	script := &dsl.Script{
		Name: "Opaque import sources", Version: "1", Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "import",
			Preserved: map[string]any{
				dsl.ImportPreservedEnterpriseUnsupportedProxySourceKey: map[string]any{
					"version":         1,
					"url_base64":      base64.StdEncoding.EncodeToString([]byte("ftp://proxy.example.test")),
					"username_base64": base64.StdEncoding.EncodeToString([]byte("proxy-user")),
					"password_base64": base64.StdEncoding.EncodeToString([]byte(proxyPassword)),
				},
				dsl.ImportPreservedJMeterJDBCSourcesKey: []map[string]string{{
					"db_url":                "jdbc:postgresql://db.example.test/orders",
					"username":              "db-user",
					"password":              jdbcPassword,
					"connection_properties": "sslpassword=" + jdbcPassword,
				}},
			},
		}},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET",
			Target: "https://example.test/",
		}},
	}
	before, err := json.Marshal(script.Metadata.Import.Preserved)
	if err != nil {
		t.Fatal(err)
	}
	if err := sanitizePersistentImportReviewSurfaces(script); err != nil {
		t.Fatalf("sanitizePersistentImportReviewSurfaces: %v", err)
	}
	after, err := json.Marshal(script.Metadata.Import.Preserved)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("durable opaque source changed\nbefore=%s\nafter=%s", before, after)
	}

	masked := maskScriptSecrets(script)
	if masked == nil || masked.Metadata == nil || masked.Metadata.Import == nil {
		t.Fatalf("masked script = %#v", masked)
	}
	payload, err := json.Marshal(masked)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		proxyPassword,
		jdbcPassword,
		base64.StdEncoding.EncodeToString([]byte(proxyPassword)),
	} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("opaque import source leaked %q: %s", forbidden, payload)
		}
	}
	if !strings.Contains(string(payload), maskedScriptSecret) {
		t.Fatalf("opaque import source was removed instead of masked: %s", payload)
	}

	var rawValue, projectedValue any
	rawPayload, _ := json.Marshal(script)
	projectedPayload, _ := json.Marshal(masked)
	if json.Unmarshal(rawPayload, &rawValue) != nil ||
		json.Unmarshal(projectedPayload, &projectedValue) != nil {
		t.Fatal("decode scripts for opaque attestation comparison")
	}
	if !opaqueImportSourceProjectionChanged(rawValue, projectedValue) {
		t.Fatal("opaque source projection delta was not detected for import-draft attestation")
	}
}

func TestOpaqueImportSourceProjectionIgnoresUnrelatedArraySanitization(t *testing.T) {
	raw := map[string]any{
		"metadata": map[string]any{
			"review": []any{
				map[string]any{"detail": "safe"},
				map[string]any{"detail": "removed review row"},
			},
		},
	}
	projected := map[string]any{
		"metadata": map[string]any{
			"review": []any{map[string]any{"detail": "safe"}},
		},
	}
	if opaqueImportSourceProjectionChanged(raw, projected) {
		t.Fatal("ordinary shortened review array was mistaken for an opaque source delta")
	}

	raw["metadata"].(map[string]any)["review"] = []any{
		map[string]any{"detail": "safe"},
		map[string]any{
			dsl.ImportPreservedJMeterJDBCSourcesKey: []any{
				map[string]any{"password": "exact-source-password"},
			},
		},
	}
	if !opaqueImportSourceProjectionChanged(raw, projected) {
		t.Fatal("opaque source nested in a removed array row was not detected")
	}
}
