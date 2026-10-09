package dsl

import (
	"fmt"
	"strings"
)

// LoadRunner run logic: config.run_logic.blocks replaces the fixed order of
// script.actions with ordered blocks. Each block runs its children
// Iterations times (default 1): every child in order (sequential), one
// child chosen at random each pass (random), or one chosen by weight
// (percentage). A child is a named action group or a nested block.
const (
	RunLogicSequential = "sequential"
	RunLogicRandom     = "random"
	RunLogicPercentage = "percentage"

	// RunLogicActionMetadataKey names the run-logic action an action belongs
	// to (an imported script's action file, such as Action1). Actions
	// without it are matched by their own name.
	RunLogicActionMetadataKey = "run_logic_action"

	MaxRunLogicBlockDepth      = 8
	MaxRunLogicBlockNodes      = 1024
	MaxRunLogicBlockIterations = 100000
)

// RunLogicBlock is one block of run logic.
type RunLogicBlock struct {
	Name       string          `json:"name,omitempty"`
	Mode       string          `json:"mode,omitempty" jsonschema:"enum=sequential,enum=random,enum=percentage"`
	Iterations int             `json:"iterations,omitempty" jsonschema:"minimum=0,maximum=100000"`
	Children   []RunLogicChild `json:"children"`
}

// RunLogicChild is an action group or a nested block, with its weight in a
// percentage block.
type RunLogicChild struct {
	Action string         `json:"action,omitempty"`
	Block  *RunLogicBlock `json:"block,omitempty"`
	Weight int            `json:"weight,omitempty" jsonschema:"minimum=0"`
}

// RunLogicActionGroups maps each run-logic action name (lowercased) to the
// indexes of script.actions in it: actions tagged with
// RunLogicActionMetadataKey by that name, or, for a name no action is
// tagged with, the actions named so.
func RunLogicActionGroups(actions []Action) map[string][]int {
	tagged := map[string][]int{}
	named := map[string][]int{}
	for index, action := range actions {
		if group, _ := action.Metadata[RunLogicActionMetadataKey].(string); strings.TrimSpace(group) != "" {
			key := strings.ToLower(strings.TrimSpace(group))
			tagged[key] = append(tagged[key], index)
		}
		if name := strings.ToLower(strings.TrimSpace(action.Name)); name != "" {
			named[name] = append(named[name], index)
		}
	}
	for name, indexes := range named {
		if _, ok := tagged[name]; !ok {
			tagged[name] = indexes
		}
	}
	return tagged
}

// RunLogicOrder lists the indexes of script.actions one iteration runs.
// pick(n) returns a uniform random index below n; it is the virtual user's
// deterministic source in replay.
func RunLogicOrder(actions []Action, blocks []RunLogicBlock, pick func(n int) int) []int {
	groups := RunLogicActionGroups(actions)
	var order []int
	var run func(block RunLogicBlock)
	runChild := func(child RunLogicChild) {
		switch {
		case child.Block != nil:
			run(*child.Block)
		case child.Action != "":
			order = append(order, groups[strings.ToLower(strings.TrimSpace(child.Action))]...)
		}
	}
	run = func(block RunLogicBlock) {
		passes := block.Iterations
		if passes <= 0 {
			passes = 1
		}
		for pass := 0; pass < passes; pass++ {
			switch strings.ToLower(strings.TrimSpace(block.Mode)) {
			case RunLogicRandom:
				if len(block.Children) > 0 {
					runChild(block.Children[pick(len(block.Children))])
				}
			case RunLogicPercentage:
				total := 0
				for _, child := range block.Children {
					total += max(child.Weight, 0)
				}
				if total <= 0 {
					continue
				}
				target := pick(total)
				for _, child := range block.Children {
					weight := max(child.Weight, 0)
					if target < weight {
						runChild(child)
						break
					}
					target -= weight
				}
			default:
				for _, child := range block.Children {
					runChild(child)
				}
			}
		}
	}
	for _, block := range blocks {
		run(block)
	}
	return order
}

// validateRunLogicBlocks checks config.run_logic.blocks against the script.
func validateRunLogicBlocks(script *Script) []string {
	blocks := script.Config.RunLogic.Blocks
	if len(blocks) == 0 {
		return nil
	}
	groups := RunLogicActionGroups(script.Actions)
	var issues []string
	nodes := 0
	var check func(block RunLogicBlock, path string, depth int)
	check = func(block RunLogicBlock, path string, depth int) {
		nodes++
		if depth > MaxRunLogicBlockDepth {
			issues = append(issues, fmt.Sprintf("%s: blocks nest more than %d levels", path, MaxRunLogicBlockDepth))
			return
		}
		mode := strings.ToLower(strings.TrimSpace(block.Mode))
		if mode != "" && mode != RunLogicSequential && mode != RunLogicRandom && mode != RunLogicPercentage {
			issues = append(issues, fmt.Sprintf("%s.mode must be sequential, random or percentage", path))
		}
		if block.Iterations < 0 || block.Iterations > MaxRunLogicBlockIterations {
			issues = append(issues, fmt.Sprintf("%s.iterations must be 0..%d", path, MaxRunLogicBlockIterations))
		}
		if len(block.Children) == 0 {
			issues = append(issues, fmt.Sprintf("%s: a block needs at least one child", path))
		}
		total := 0
		for index, child := range block.Children {
			childPath := fmt.Sprintf("%s.children[%d]", path, index)
			nodes++
			switch {
			case child.Block != nil && child.Action != "":
				issues = append(issues, childPath+": a child is an action or a block, not both")
			case child.Block != nil:
				check(*child.Block, childPath+".block", depth+1)
			case strings.TrimSpace(child.Action) == "":
				issues = append(issues, childPath+": a child needs an action or a block")
			case len(groups[strings.ToLower(strings.TrimSpace(child.Action))]) == 0:
				issues = append(issues, fmt.Sprintf("%s: no action belongs to run-logic action %q", childPath, child.Action))
			}
			if child.Weight < 0 {
				issues = append(issues, childPath+".weight must be zero or greater")
			}
			total += max(child.Weight, 0)
		}
		if mode == RunLogicPercentage && total <= 0 {
			issues = append(issues, path+": a percentage block needs weights above zero")
		}
	}
	for index, block := range blocks {
		check(block, fmt.Sprintf("config.run_logic.blocks[%d]", index), 1)
	}
	if nodes > MaxRunLogicBlockNodes {
		issues = append(issues, fmt.Sprintf("config.run_logic.blocks must not exceed %d blocks and children", MaxRunLogicBlockNodes))
	}
	return issues
}
