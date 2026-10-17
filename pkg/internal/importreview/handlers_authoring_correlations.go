package importreview

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func CloneScriptDefinition(in *dsl.Script) *dsl.Script {
	if in == nil {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		out := *in
		return &out
	}
	var out dsl.Script
	if err := json.Unmarshal(data, &out); err != nil {
		copied := *in
		return &copied
	}
	return &out
}
