package enterprisearchive

import (
	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
	"strings"
)

// Go time layouts treat literal source digits (including .000) as formatting
// tokens. Keep the source dialect to preserve bytes outside each % directive.
// A trailing literal Z is output text and does not select a UTC source clock.
func preserveSourceTimeParameterFormat(param *dsl.Parameter, values map[string]string) {
	if param.Type != "time" && param.Type != "date_time" {
		return
	}
	source := convertutil.FirstNonEmpty(values["format"], values["dateformat"], values["datetimeformat"])
	if !strings.Contains(source, "%") {
		return
	}
	param.Format = source
	param.FormatDialect = "loadrunner_strftime"
	param.Timezone = convertutil.FirstNonEmpty(values["timezone"], values["time_zone"])
}
