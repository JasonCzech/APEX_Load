package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

func SourceScriptName(source *dsl.Script) string {
	if source == nil {
		return ""
	}
	return strings.TrimSpace(source.Name)
}
