package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
)

func RequiresExactRepairDraft(s *dsl.Script) bool {
	if _, ok := ScriptBatch(s); ok {
		return true
	}
	if s == nil || s.Metadata == nil || s.Metadata.Import == nil {
		return false
	}
	value, _ := s.Metadata.Import.Preserved["guided_repair_candidate"].(bool)
	return value
}
