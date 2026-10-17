package importreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

type CorrelationActionGraph struct {
	Protocol    string           `json:"protocol"`
	Config      dsl.ScriptConfig `json:"config"`
	DataSources []dsl.DataSource `json:"data_sources"`
	Parameters  []dsl.Parameter  `json:"parameters"`
	Init        []dsl.Action     `json:"init"`
	Actions     []dsl.Action     `json:"actions"`
	Teardown    []dsl.Action     `json:"teardown"`
}

func ScriptCorrelationActionGraphDigest(script *dsl.Script) (string, error) {
	if script == nil {
		return "", fmt.Errorf("script is required")
	}
	actions := func(input []dsl.Action) []dsl.Action {
		if len(input) == 0 {
			return []dsl.Action{}
		}
		return input
	}
	dataSources := script.DataSources
	if len(dataSources) == 0 {
		dataSources = []dsl.DataSource{}
	}
	parameters := script.Parameters
	if len(parameters) == 0 {
		parameters = []dsl.Parameter{}
	}
	payload, err := json.Marshal(CorrelationActionGraph{
		Protocol:    strings.ToLower(strings.TrimSpace(script.Protocol)),
		Config:      script.Config,
		DataSources: dataSources,
		Parameters:  parameters,
		Init:        actions(script.Init),
		Actions:     actions(script.Actions),
		Teardown:    actions(script.Teardown),
	})
	if err != nil {
		return "", fmt.Errorf("marshal script action graph: %w", err)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
