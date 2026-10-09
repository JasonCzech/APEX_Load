package engine

import (
	"math/rand"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// iterationOrder lists the indexes of script.actions this iteration runs
// under config.run_logic.blocks, or nil to run them all in order. Random
// picks use the virtual user's random source, which replay seeds, so a
// debug replay repeats its choices.
func (vu *VUser) iterationOrder() []int {
	if vu == nil || vu.Script == nil || len(vu.Script.Config.RunLogic.Blocks) == 0 {
		return nil
	}
	order := dsl.RunLogicOrder(vu.Script.Actions, vu.Script.Config.RunLogic.Blocks, vu.pickRunLogicChild)
	if order == nil {
		order = []int{}
	}
	return order
}

// iterationActions is the actions this iteration runs, in order.
func (vu *VUser) iterationActions() []dsl.Action {
	order := vu.iterationOrder()
	if order == nil {
		return vu.Script.Actions
	}
	actions := make([]dsl.Action, len(order))
	for i, index := range order {
		actions[i] = vu.Script.Actions[index]
	}
	return actions
}

func (vu *VUser) pickRunLogicChild(n int) int {
	if n <= 1 {
		return 0
	}
	if vu.rng != nil {
		return vu.rng.Intn(n)
	}
	return rand.Intn(n)
}
