package bruno

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAttachedOpenCollectionSOAPRequestsImportLosslessly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		filename          string
		urlMarker         string
		bodyMarker        string
		expectedVariables []string
		expectedSecrets   map[string]string
	}{
		{
			filename:   "ExecuteUnifiedCreditPolicyCheck_v246.yml",
			urlMarker:  "ExecuteUnifiedCreditPolicyCheck.jws",
			bodyMarker: "<ns2:ExecuteUnifiedCreditPolicyCheckRequest",
			expectedVariables: []string{
				"billingAccountNumber",
				"env",
				"partnerPassword",
				"partnerUserName",
				"password",
				"riskassessmentadapter",
				"username",
			},
		},
		{
			filename:   "AddSalesTransactionDetails_v302.yml",
			urlMarker:  "AddSalesTransactionDetails.jws",
			bodyMarker: "<ns4:AddSalesTransactionDetailsRequest",
			expectedVariables: []string{
				"accountadapter",
				"billingAccountNumber",
				"env",
				"partnerPassword",
				"partnerUserName",
				"password",
				"subscriberNumber",
				"username",
			},
		},
		{
			filename:   "ActivateSubscriber_v302.yml",
			urlMarker:  "ActivateSubscriber.jws",
			bodyMarker: "<n2:ActivateSubscriberRequest",
			expectedVariables: []string{
				"billingAccountNumber",
				"conversationId",
				"deviceadapter",
				"env",
				"iccid",
				"newSubscriberNumber",
				"partnerPassword",
				"partnerUserName",
				"password",
				"serviceArea",
				"username",
			},
			expectedSecrets: map[string]string{
				"ACTIVATESUBSCRIBER_V302_HEADER_IDPCTX_SESSION_ID": "6544765875687687698000006",
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.filename, func(t *testing.T) {
			t.Parallel()

			fixturePath := filepath.Join("testdata", "attached", test.filename)
			source, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			result, err := Inspect(Input{
				Filename:  test.filename,
				Content:   source,
				ProjectID: "attached-fixtures",
			})
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}

			if result.Script == nil || result.Script.Protocol != "bruno" {
				t.Fatalf("canonical script = %#v", result.Script)
			}
			if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "bruno_run" {
				t.Fatalf("canonical actions = %#v", result.Script.Actions)
			}
			if result.Collection.Name != strings.TrimSuffix(test.filename, filepath.Ext(test.filename)) {
				t.Fatalf("collection name = %q", result.Collection.Name)
			}
			if len(result.Collection.Items) != 1 {
				t.Fatalf("collection items = %#v", result.Collection.Items)
			}
			item := result.Collection.Items[0]
			if item.Type != "http" || !item.Executable || !item.HasScripts || !item.HasTests {
				t.Fatalf("collection item = %#v", item)
			}
			if result.Fidelity.Status != "lossless" || result.Fidelity.DroppedItems != 0 || result.Fidelity.ExecutableItems != 1 {
				t.Fatalf("fidelity = %#v", result.Fidelity)
			}
			if len(result.SecretRequirements) != len(test.expectedSecrets) {
				t.Fatalf("secret requirements = %#v", result.SecretRequirements)
			}
			for _, requirement := range result.SecretRequirements {
				expectedValue, ok := test.expectedSecrets[requirement.SuggestedName]
				if !ok || requirement.Value != expectedValue {
					t.Fatalf("unexpected secret requirement: %#v", requirement)
				}
			}
			if !bytes.Equal(result.OriginalUpload, source) {
				t.Fatal("original upload was not preserved byte-for-byte")
			}
			if got := tarFile(t, result.RawArchive, test.filename); !bytes.Equal(got, source) {
				t.Fatal("raw artifact did not preserve the source byte-for-byte")
			}
			if got := tarFile(t, result.SanitizedArchive, test.filename); len(test.expectedSecrets) == 0 {
				if !bytes.Equal(got, source) {
					t.Fatal("sanitized executable bundle changed a template-only source")
				}
			} else {
				for name, value := range test.expectedSecrets {
					if bytes.Contains(got, []byte(value)) || !bytes.Contains(got, []byte("{{secret."+name+"}}")) {
						t.Fatalf("sanitized executable bundle retained %q or omitted its reference", name)
					}
				}
			}

			var document map[string]any
			if err := yaml.Unmarshal(source, &document); err != nil {
				t.Fatalf("decode source YAML: %v", err)
			}
			httpBlock := requiredMap(t, document, "http")
			if got := anyString(httpBlock["method"]); got != "POST" {
				t.Fatalf("HTTP method = %q", got)
			}
			if got := anyString(httpBlock["url"]); !strings.Contains(got, test.urlMarker) {
				t.Fatalf("HTTP URL = %q, want marker %q", got, test.urlMarker)
			}
			auth := requiredMap(t, httpBlock, "auth")
			if anyString(auth["type"]) != "basic" || anyString(auth["username"]) != "{{username}}" || anyString(auth["password"]) != "{{password}}" {
				t.Fatalf("basic auth = %#v", auth)
			}
			body := requiredMap(t, httpBlock, "body")
			if anyString(body["type"]) != "xml" {
				t.Fatalf("body type = %q", anyString(body["type"]))
			}
			bodyData := anyString(body["data"])
			for _, marker := range []string{
				test.bodyMarker,
				"<SOAP-ENV:Envelope",
				"{{partnerUserName}}",
				"{{partnerPassword}}",
				"{{billingAccountNumber}}",
			} {
				if !strings.Contains(bodyData, marker) {
					t.Fatalf("SOAP body is missing %q", marker)
				}
			}

			settings := requiredMap(t, document, "settings")
			if settings["followRedirects"] != true || anyInt(settings["maxRedirects"]) != 5 || anyInt(settings["timeout"]) != 0 {
				t.Fatalf("request settings = %#v", settings)
			}

			runtime := requiredMap(t, document, "runtime")
			scripts, ok := runtime["scripts"].([]any)
			if !ok || len(scripts) != 1 {
				t.Fatalf("runtime scripts = %#v", runtime["scripts"])
			}
			script, ok := scripts[0].(map[string]any)
			if !ok {
				t.Fatalf("runtime script = %#v", scripts[0])
			}
			code := anyString(script["code"])
			for _, marker := range []string{
				"res.getStatus()).to.equal(500)",
				`res.getHeader("content-type".toLowerCase())).to.include("text/xml")`,
				`body.match(/<code>(.*?)<\/code>/)`,
				`body.match(/<description>(.*?)<\/description>/)`,
				`body.match(/<faultCode>(.*?)<\/faultCode>/)`,
				`body.match(/<faultDescription>(.*?)<\/faultDescription>/)`,
			} {
				if !strings.Contains(code, marker) {
					t.Fatalf("after-response script is missing %q", marker)
				}
			}

			sort.Strings(test.expectedVariables)
			if got := strings.Join(result.MissingVariables, ","); got != strings.Join(test.expectedVariables, ",") {
				t.Fatalf("missing variables = %q, want %q", got, strings.Join(test.expectedVariables, ","))
			}
		})
	}
}

func tarFile(t *testing.T, archive []byte, name string) []byte {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			t.Fatalf("archive does not contain %q", name)
		}
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
		if header.Name != name {
			continue
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read archive member %q: %v", name, err)
		}
		return content
	}
}

func requiredMap(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("%q = %#v, want object", key, parent[key])
	}
	return value
}
