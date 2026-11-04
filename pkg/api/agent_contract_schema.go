package api

//go:generate go run ../../cmd/agentschemagen -out ../../docs/contracts/agent-v1

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// agentContractSchemaURN prefixes the agent-v1 schema ids. A URN, not a
// URL: there is no public schema host, and validators must not try to fetch
// one.
const agentContractSchemaURN = "urn:apexload:contract:agent-v1:"

// AgentContractSchemas returns docs/contracts/agent-v1's JSON Schemas
// (2020-12), generated from the Go types the controller serves, by file
// name.
func AgentContractSchemas() (map[string][]byte, error) {
	out := map[string][]byte{}
	for name, value := range map[string]any{
		"brief.schema.json":          &AgentBrief{},
		"finding-detail.schema.json": &AgentFindingDetail{},
		"ctrf.schema.json":           &AgentCTRFReport{},
		"event.schema.json":          &AgentCloudEvent{},
	} {
		// The root is the object schema itself (MCP clients require an
		// outputSchema of type object), and unknown properties are allowed:
		// v1 may add fields, which a consumer must accept.
		reflector := &jsonschema.Reflector{ExpandedStruct: true, AllowAdditionalProperties: true}
		schema := reflector.Reflect(value)
		schema.ID = jsonschema.ID(agentContractSchemaURN + name)
		if verdict, ok := schema.Definitions["AgentVerdict"]; ok {
			if codes, ok := verdict.Properties.Get("reason_codes"); ok && codes.Items != nil {
				enum := make([]any, 0, len(agentReasonCodes))
				for _, code := range agentReasonCodes {
					enum = append(enum, code)
				}
				codes.Items.Enum = enum
			}
		}
		body, err := json.MarshalIndent(schema, "", "  ")
		if err != nil {
			return nil, err
		}
		out[name] = append(body, '\n')
	}
	return out, nil
}
