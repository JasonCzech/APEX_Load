package bruno

import (
	"reflect"
	"testing"
)

func TestEnvironmentVariableValuesKeepOnlyPlainValues(t *testing.T) {
	yamlEnvironment := []byte(`name: dev
variables:
  - name: subscriberNumber
    value: "5551234"
  - name: port
    value: 8443
  - name: apiKey
    value: hidden
    secret: true
  - name: legacyHost
    value: old.example.test
    enabled: false
  - name: url
    value: "https://{{host}}/v1"
  - name: partnerPassword
    value: "{{secret.PARTNER_PASSWORD}}"
  - name: scrubbed
    value: "[REDACTED]"
`)
	want := map[string]string{"subscriberNumber": "5551234", "port": "8443"}
	if got := environmentVariableValues(yamlEnvironment, ".yml"); !reflect.DeepEqual(got, want) {
		t.Errorf("yaml values = %v, want %v", got, want)
	}

	bruEnvironment := []byte(`vars {
  subscriberNumber: 5551234
  url: https://{{host}}/v1
}
vars:secret [
  apiKey
]
`)
	if got := environmentVariableValues(bruEnvironment, ".bru"); !reflect.DeepEqual(got, map[string]string{"subscriberNumber": "5551234"}) {
		t.Errorf("bru values = %v", got)
	}

	if got := environmentVariableValues([]byte("subscriberNumber=5551234\nAPI_TOKEN=abc\n"), ".env"); got != nil {
		t.Errorf(".env values = %v, want none", got)
	}
}
