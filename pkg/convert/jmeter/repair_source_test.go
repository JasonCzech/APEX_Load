package jmeter

import (
	"strings"
	"testing"
)

func TestOAuthNormalizationUsesOriginalScopeAndRejectsConflicts(t *testing.T) {
	for _, tc := range []struct {
		name, options string
		valid         bool
	}{
		{"legacy masked", `cid="***" cs="***"`, true},
		{"same aliases", `cid="test-client" clientId="test-client" cs="***"`, true},
		{"conflicting ID", `cid="private-first" clientId="private-second" cs="***"`, false},
		{"conflicting secret", `cid="test" cs="private-first" clientSecret="private-second"`, false},
		{"unknown option", `cid="test" cs="***" uploadedClass="test"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := []byte(strings.Replace(string(messagingFixture(t, "original")), `cid="***" cs="***"`, tc.options, 1))
			result, err := ConvertJMX(source, ConvertOptions{})
			if err != nil {
				t.Fatal(err)
			}
			id := ""
			for _, script := range result.Scripts {
				if script.Protocol == "kafka" {
					id = script.Config.JMeter.ConfigurationID
				}
			}
			if id == "" {
				t.Fatal("missing Kafka source identity")
			}
			err = ValidateOAuthNormalization(source, id)
			if (err == nil) != tc.valid {
				t.Fatalf("normalization: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "private-") {
				t.Fatal("credential leaked in error")
			}
			if err := ValidateOAuthNormalization(source, "wrong-source"); err == nil {
				t.Fatal("wrong configuration accepted")
			}
		})
	}
}
