package engine

import (
	"fmt"
	"math"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A unique counter reserves an arithmetic block per logical VUser. Existing
// scenario/generator allocation supplies a dense run-wide ordinal, so transport
// IDs and generator sharding cannot duplicate the same number block.
func uniqueCounterBlockStart(param dsl.Parameter, ordinal int) (int, error) {
	if ordinal < 0 || param.BlockSize <= 0 || param.BlockSize > dsl.MaxParameterRowIndex || param.Step != 1 || param.End != 0 || param.AutoAllocate {
		return 0, fmt.Errorf("unique counter %q requires a valid ordinal, explicit positive block size, and unit increment", param.Name)
	}
	if ordinal > math.MaxInt/param.BlockSize {
		return 0, fmt.Errorf("unique counter %q block offset overflows", param.Name)
	}
	offset := ordinal * param.BlockSize
	if param.Start > math.MaxInt-offset {
		return 0, fmt.Errorf("unique counter %q block start overflows", param.Name)
	}
	start := param.Start + offset
	if start > math.MaxInt-(param.BlockSize-1) {
		return 0, fmt.Errorf("unique counter %q block end overflows", param.Name)
	}
	return start, nil
}

func (pr *ParameterRuntime) generateUniqueCounterValue(param dsl.Parameter, state *parameterState) (string, bool) {
	ordinal := pr.vuserID
	if pr.allocation != nil {
		ordinal = pr.allocation.Offset
	}
	start, err := uniqueCounterBlockStart(param, ordinal)
	if err != nil {
		return "", false
	}
	if state.nextIndex >= param.BlockSize {
		switch effectiveOnExhaustion(param) {
		case "wrap":
			state.nextIndex = 0
		case "last":
			return formatNumericValue(start+param.BlockSize-1, param), true
		case "empty":
			return "", true
		default:
			return "", false
		}
	}
	value := start + state.nextIndex
	state.nextIndex++
	return formatNumericValue(value, param), true
}
