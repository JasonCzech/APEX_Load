package importreview

import (
	"github.com/jasonczech/apexload/pkg/convert/sharedvalue"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func BuildImportSharedValues(scripts []*dsl.Script) *sharedvalue.Result {
	result := sharedvalue.Detect(scripts)
	return &result
}
