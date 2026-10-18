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

// iterationAction returns the action at position in this iteration's order
// (nil order: script.actions in order). Steps are not copied up front, so a
// long run-logic order costs one index per step. The action belongs to the
// shared script and must not be modified.
func (vu *VUser) iterationAction(order []int, position int) *dsl.Action {
	if order == nil {
		return &vu.Script.Actions[position]
	}
	return &vu.Script.Actions[order[position]]
}

// iterationLength is the number of steps this iteration runs.
func (vu *VUser) iterationLength(order []int) int {
	if order == nil {
		return len(vu.Script.Actions)
	}
	return len(order)
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
