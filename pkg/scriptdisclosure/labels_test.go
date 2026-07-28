package scriptdisclosure

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func largeCredentialBearingLabelSource() (*dsl.Script, []string) {
	params := make(map[string]any, 21)
	secrets := make([]string, 20)
	for index := range secrets {
		secrets[index] = "large-label-secret-" + strconv.Itoa(index)
		params["field_"+strconv.Itoa(index)+"_password"] = secrets[index]
	}
	params["ordinary_blob"] = strings.Repeat("x", 4<<20)
	return &dsl.Script{
		Name: "Large imported script", Version: "1", Protocol: "http",
		Actions: []dsl.Action{{
			Name: "request", Type: "POST", Method: "POST", Target: "/",
			Params: params,
		}},
	}, secrets
}

func TestProjectLabelUsesCredentialSourcesWithoutChangingThem(t *testing.T) {
	const password = "label-password-canary-83d2"
	source := &dsl.Script{
		Name:     "Runnable",
		Protocol: "http",
		Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
			Type:     "basic",
			Username: "operator",
			Password: password,
		}}},
	}

	got := ProjectLabel("Load "+password+" users", source)
	if strings.Contains(got, password) || !strings.Contains(got, credential.Redacted) {
		t.Fatalf("ProjectLabel() = %q, want credential-redacted label", got)
	}
	if source.Config.HTTP.Auth.Password != password {
		t.Fatalf("ProjectLabel changed executable password to %q", source.Config.HTTP.Auth.Password)
	}
}

func TestProjectLabelsBoundsOnlyOutputComparisonForLargeCredentialSource(t *testing.T) {
	source, secrets := largeCredentialBearingLabelSource()

	got := ProjectLabels([]string{
		"acc_wls_reg_1",
		"run " + secrets[0],
	}, source)

	if got[0] != "acc_wls_reg_1" {
		t.Fatalf("ordinary large-script label = %q, want unchanged", got[0])
	}
	if strings.Contains(got[1], secrets[0]) || !strings.Contains(got[1], credential.Redacted) {
		t.Fatalf("credential-bearing large-script label = %q, want credential redacted", got[1])
	}
}

func TestProjectLabelsFailsClosedForMalformedNeutralCredentialLocators(t *testing.T) {
	const pin = "neutral-label-pin-19f6"
	source := &dsl.Script{
		Name:     "Runnable",
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "openapi",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name: "verify", Type: "POST", Method: "POST", Target: "/verify",
			HeaderList: dsl.HeaderList{{
				Name:  "Content-Type",
				Value: "application/json",
			}},
			Body: `{"code":"` + pin + `"}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
					map[string]any{"field": "body.json", "pointer": "/code"},
					map[string]any{"pointer": "/missing-field"},
				},
			},
		}},
	}

	got := ProjectLabels([]string{"Plan " + pin, "ordinary"}, source)
	if len(got) != 2 || got[0] != credential.Redacted || got[1] != credential.Redacted {
		t.Fatalf("ProjectLabels() = %#v, want every label redacted on malformed locator metadata", got)
	}
}

func TestProjectLabelsFailsClosedWhenNeutralCredentialFieldWasRenamed(t *testing.T) {
	const pin = "renamed-neutral-label-pin-6a31"
	source := &dsl.Script{
		Name:     "Runnable",
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "openapi",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
		Actions: []dsl.Action{{
			Name: "verify", Type: "POST", Method: "POST", Target: "/verify",
			HeaderList: dsl.HeaderList{{
				Name:  "Content-Type",
				Value: "application/json",
			}},
			Body: `{"renamed":"` + pin + `"}`,
			Metadata: map[string]any{
				dsl.ImportPreservedCredentialTargetsMetadataKey: []map[string]any{{
					"field": "body.json", "pointer": "/code",
				}},
			},
		}},
	}

	got := ProjectLabels([]string{"Plan " + pin, "ordinary"}, source)
	if len(got) != 2 || got[0] != credential.Redacted || got[1] != credential.Redacted {
		t.Fatalf("ProjectLabels() = %#v, want every label redacted for a stale neutral-field locator", got)
	}
}

func TestProjectLabelsUsesProtectedRepairSourceCredentials(t *testing.T) {
	const password = "protected-repair-label-password-2e85"
	source := &dsl.Script{
		Name:     "Runnable",
		Protocol: "java_vuser",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "enterprise_script_archive",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
				dsl.ImportPreservedEnterpriseJavaJDBCSourceKey: map[string]any{
					"version": 1,
					"connections": []map[string]any{{
						"driver_base64": base64.StdEncoding.EncodeToString([]byte("postgres")),
						"dsn_base64": base64.StdEncoding.EncodeToString(
							[]byte("postgres://db-user:" + password + "@db.example.test/app"),
						),
						"reason":     "unsupported",
						"source_ref": "Action.java line 4",
					}},
				},
			},
		}},
	}

	got := ProjectLabel("Repair "+password, source)
	if strings.Contains(got, password) || !strings.Contains(got, credential.Redacted) {
		t.Fatalf("ProjectLabel() = %q, want protected repair-source credential redacted", got)
	}
}
