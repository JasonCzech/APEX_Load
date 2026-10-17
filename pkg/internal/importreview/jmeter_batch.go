package importreview

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const JmeterBatchMetadata = "jmeter_batch_v1"

type JmeterBatchIdentity struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

func ScriptBatch(s *dsl.Script) (JmeterBatchIdentity, bool) {
	var id JmeterBatchIdentity
	if s == nil || s.Metadata == nil || s.Metadata.Import == nil {
		return id, false
	}
	v, ok := s.Metadata.Import.Preserved[JmeterBatchMetadata]
	if !ok {
		return id, false
	}
	data, _ := json.Marshal(v)
	_ = json.Unmarshal(data, &id)
	return id, true
}
