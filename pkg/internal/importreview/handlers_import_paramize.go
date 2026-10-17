package importreview

import (
	"github.com/jasonczech/apexload/pkg/convert/paramize"
)

func SanitizeParameterizationDiffs(input []paramize.Diff) []paramize.Diff {
	output := make([]paramize.Diff, len(input))
	copy(output, input)
	for index := range output {
		output[index].Before = ""
		output[index].After = ""
	}
	return output
}
