package importreview

import (
	"os"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/jmeter"
)

// The import response discovers secret bindings from a private disclosure
// projection. When that projection masked the Kafka OAuth token endpoint, the
// review offered an optional "Token url" binding nothing could clear, and the
// hub, SAS policy name and credential locator paths read [REDACTED]. None of
// them is a credential (preview-over-redaction, kafka-preview-redaction-ux).
func TestJMeterMessagingProjectionKeepsIdentifiersAndOffersNoTokenURLBinding(t *testing.T) {
	source, err := os.ReadFile("../../convert/jmeter/testdata/messaging_original.jmx")
	if err != nil {
		t.Fatal(err)
	}
	result, err := jmeter.ConvertJMX(source, jmeter.ConvertOptions{ScenarioDraft: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AttachImportCredentialReview(result.Scripts, result.SecretRequirements); err != nil {
		t.Fatal(err)
	}
	exact := ImportResponseRawScriptValue(result.Scripts)
	projected := CredentialPreservingImportResponseValueFromExact(result.Scripts, exact, nil)

	for _, requirement := range SecretBindingRequirementsForImportScripts(projected) {
		if requirement.Path == "/config/kafka/oauth/token_url" {
			t.Fatalf("token endpoint offered as a secret binding: %+v", requirement)
		}
	}
	exactScripts, projectedScripts := exact.([]any), projected.([]any)
	checked := 0
	for index, script := range result.Scripts {
		pointers := []string{}
		if script.Config.Kafka.OAuth != nil {
			pointers = append(pointers,
				"/config/kafka/oauth/token_url",
				"/actions/0/metadata/jmeter_kafka/effective_settings/sasl.oauthbearer.token.endpoint.url",
			)
		}
		if script.Config.EventHubs.EventHub != "" {
			pointers = append(pointers,
				"/actions/0/params/event_hub",
				"/actions/0/metadata/jmeter_eventhubs/shared_access_key_name",
			)
		}
		pointers = append(pointers,
			"/metadata/import/preserved/protected_credential_requirements_v1/0/suggested_name",
			"/metadata/import/preserved/protected_credential_requirements_v1/0/affected_paths/0",
		)
		for _, pointer := range pointers {
			want, ok := jsonPointerValue(exactScripts[index], pointer)
			if !ok {
				t.Fatalf("script %d has no %s", index, pointer)
			}
			got, _ := jsonPointerValue(projectedScripts[index], pointer)
			if got != want {
				t.Fatalf("script %d %s = %#v, want it as converted (%#v)", index, pointer, got, want)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("fixture produced no messaging identifiers to check")
	}
}

func jsonPointerValue(root any, pointer string) (any, bool) {
	tokens, err := SplitJSONPointer(pointer)
	if err != nil {
		return nil, false
	}
	current := root
	for _, token := range tokens {
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[token]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			index := 0
			for _, r := range token {
				if r < '0' || r > '9' {
					return nil, false
				}
				index = index*10 + int(r-'0')
			}
			if index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

// Hub and SAS policy names skip only the random-looking-text scrub; a
// connection string pasted under those keys still masks its SharedAccessKey.
func TestProjectScriptNodeMasksConnectionStringUnderMessagingIdentifierKeys(t *testing.T) {
	const sasKey = "Zq8vN3kLp0xR7tY2wE5uSyntheticFixture="
	cs := "Endpoint=sb://example.servicebus.windows.net/;SharedAccessKeyName=send;SharedAccessKey=" + sasKey + ";EntityPath=hub1"
	const hub = "hub-q7Xk2Lm9Pz4Rw8Tn3Vb6Yc1Hd5Jf0Gs7Ke2Ma9Nq4Ur8Wt3Zx6"
	action := map[string]any{
		"name": "send", "type": "eventhubs_send",
		"params": map[string]any{"event_hub": cs, "other": cs},
		"metadata": map[string]any{"jmeter_eventhubs": map[string]any{
			"event_hub":              hub,
			"shared_access_key_name": cs,
		}},
	}
	projected := ProjectScriptNode(action, ScriptProjectionContext{}).(map[string]any)
	params := projected["params"].(map[string]any)
	eventHubs := projected["metadata"].(map[string]any)["jmeter_eventhubs"].(map[string]any)
	for name, got := range map[string]any{
		"params.event_hub": params["event_hub"],
		"params.other":     params["other"],
		"jmeter_eventhubs.shared_access_key_name": eventHubs["shared_access_key_name"],
	} {
		text, _ := got.(string)
		if strings.Contains(text, sasKey) {
			t.Fatalf("%s disclosed the SharedAccessKey: %q", name, text)
		}
	}
	if got := eventHubs["event_hub"]; got != hub {
		t.Fatalf("event_hub = %#v, want the random-looking hub name visible", got)
	}
}
