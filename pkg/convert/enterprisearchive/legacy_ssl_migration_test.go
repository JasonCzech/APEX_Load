package enterprisearchive

import (
	"fmt"
	"strings"
	"testing"
)

func TestLegacySSLImportMigratesToTLS13WithWarning(t *testing.T) {
	for _, value := range []string{"SSL", "2", "3", "2&3", "SSL2", "SSL3", "SSLv2", "SSLv3", "SSL 2.0", "SSL 3.0", "SSLv2.0", "SSLv3.0", "SSL2&3", " ssl3 ", "AUTO", " auto "} {
		t.Run(value, func(t *testing.T) {
			for _, source := range []string{"runtime", "socket"} {
				t.Run(source, func(t *testing.T) {
					files := map[string]string{
						"Demo.usr":    "[General]\nType=Web\nRuntimeSettings=default.cfg\n[Actions]\nAction1=Action.c\n",
						"default.cfg": "[SSL]\nSSLVersion=" + value + "\n",
						"Action.c":    "Action() { web_url(\"request\", \"URL=https://example.test/\", LAST); return 0; }",
					}
					if source == "socket" {
						files["default.cfg"] = "[SSL]\nSSLVersion=TLS1.2\n"
						files["Action.c"] = fmt.Sprintf("Action() { web_set_sockets_option(\"SSL_VERSION\", %q); web_url(\"request\", \"URL=https://example.test/\", LAST); return 0; }", value)
					}
					result, err := ConvertZip(buildSourceZip(t, files), ConvertOptions{})
					if err != nil {
						t.Fatal(err)
					}
					tls := result.Script.Config.HTTP.TLS
					if tls.MinVersion != "1.3" || tls.MaxVersion != "1.3" {
						t.Fatalf("TLS = %#v, want exact TLS 1.3", tls)
					}
					warnings := strings.Join(result.Warnings, "\n")
					if !strings.Contains(warnings, "replaced with TLS 1.3") || !strings.Contains(warnings, strings.TrimSpace(value)) || !strings.Contains(warnings, "TLS 1.3-capable endpoint") {
						t.Fatalf("missing migration warning: %s", warnings)
					}
					for _, action := range result.Script.Actions {
						if action.Type == "source_review" {
							t.Fatalf("legacy SSL migration should not insert a blocking review action: %#v", action)
						}
					}
					if runtimeAdditionalAttributeIsNativelyMapped(result.Script.Config, "web_set_sockets_option.SSL_VERSION", value) {
						t.Fatal("legacy SSL migration must not claim exact source parity")
					}
					if source == "runtime" {
						_, rc := resolveRuntimeConfig(files, nil)
						parity := applyRuntimeMigrationIssues(buildRTSParityReportForProtocol(selectedRuntimeSections(collectRuntimeSections(files), "default.cfg", rc), "http"), rc)
						entry := runtimeParityEntryByID(t, parity, "tls")
						if entry.Status != RuntimeSettingStatusPartial || !strings.Contains(entry.Notes, "replaced with TLS 1.3") {
							t.Fatalf("migration parity = %#v", entry)
						}
					}
				})
			}
		})
	}
}

func TestTLSImportKeepsExplicitVersionsAndUnknownDefaults(t *testing.T) {
	for _, tt := range []struct{ value, want string }{
		{"TLS1.0", "1.0"}, {"TLS1.1", "1.1"}, {"TLS1.2", "1.2"}, {"TLS1.3", "1.3"}, {"TLS", "1.2"}, {"", ""}, {"TLS-future", ""}, {"TLS-next", ""},
	} {
		t.Run(tt.value, func(t *testing.T) {
			rc := ParseRuntimeCfg("[SSL]\nSSLVersion=" + tt.value + "\n")
			tls := rc.ToScriptConfig().HTTP.TLS
			if tls.MinVersion != tt.want || tls.MaxVersion != tt.want {
				t.Fatalf("TLS = %#v, want %q", tls, tt.want)
			}
			result := convertSourceExecutionFixture(t, fmt.Sprintf("Action() { web_set_sockets_option(\"SSL_VERSION\", %q); web_url(\"request\", \"URL=https://example.test/\", LAST); return 0; }", tt.value))
			if result.Script.Config.HTTP.TLS.MinVersion != tt.want || result.Script.Config.HTTP.TLS.MaxVersion != tt.want {
				t.Fatalf("source TLS = %#v, want %q", result.Script.Config.HTTP.TLS, tt.want)
			}
			if strings.Contains(strings.Join(result.Warnings, "\n"), "replaced with TLS 1.3") {
				t.Fatalf("unexpected migration warning: %#v", result.Warnings)
			}
		})
	}
}
