package main

import (
	"strings"
	"testing"
)

func TestReverseConnectorConfigRequiresSecureProductionEnrollment(t *testing.T) {
	base := reverseConnectorConfig{
		URL: "https://controller.example", GeneratorID: "generator-1", ExecutionTargetID: "target-1",
		EnrollmentToken: strings.Repeat("a", 32), Production: true,
	}
	if err := base.validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	insecure := base
	insecure.URL = "http://controller.example"
	if err := insecure.validate(); err == nil {
		t.Fatal("production HTTP controller URL was accepted")
	}
	burst := base
	burst.Lifecycle = "burst"
	if err := burst.validate(); err == nil {
		t.Fatal("burst config without launch nonce was accepted")
	}
}

func TestReverseConnectorBuildsGatewayURL(t *testing.T) {
	config := reverseConnectorConfig{URL: "https://controller.example/base/?old=true"}
	if got, want := config.websocketURL(), "wss://controller.example/base/api/v1/generator-gateway/connect"; got != want {
		t.Fatalf("websocketURL = %q, want %q", got, want)
	}
}
