package asyncapi

import (
	"fmt"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const asyncAPIEventHubsConnectionStringReference = "{{secret.EVENTHUBS_CONNECTION_STRING}}"

// asyncAPIGeneratedCredentialRequirements describes credentials introduced by
// the converter rather than supplied by the source. In particular, detecting an
// Event Hubs Kafka endpoint tells us which credential is needed, but the
// AsyncAPI document does not contain the namespace connection string itself.
func asyncAPIGeneratedCredentialRequirements(
	scripts []*dsl.Script,
	sourceFile string,
) ([]credentialpolicy.Requirement, error) {
	var requirements []credentialpolicy.Requirement
	for _, script := range scripts {
		if script == nil ||
			script.Config.Kafka.SASLPasswordSecret != asyncAPIEventHubsConnectionStringReference {
			continue
		}
		requirement, replacement, err := credentialpolicy.NewRequirement(
			credentialpolicy.RequirementSpec{
				Key:      "eventhubs_connection_string",
				Label:    "Azure Event Hubs namespace connection string",
				Context:  "Kafka authentication for the detected Azure Event Hubs endpoint. AsyncAPI identifies the broker but does not contain the namespace connection string.",
				NameHint: "EVENTHUBS_CONNECTION_STRING",
				Source: credentialpolicy.SourceRef{
					Format:  "asyncapi",
					Path:    sourceFile,
					Pointer: "/servers",
					ItemID:  script.Name,
				},
				Targets: []credentialpolicy.TargetRef{{
					RequestKey: script.Name,
					Field:      "config.kafka.sasl_password_secret",
				}},
				Mode: credentialpolicy.SecretReference,
			},
		)
		if err != nil {
			return nil, err
		}
		if replacement != asyncAPIEventHubsConnectionStringReference {
			return nil, fmt.Errorf("Event Hubs credential name did not round-trip")
		}
		requirements = append(requirements, requirement)
	}
	return requirements, nil
}
