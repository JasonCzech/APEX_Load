package enterprisearchive

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// The selected .usp holds VuGen's run logic as an INI tree: [RunLogicRunRoot]
// and one section per child, named by its path ([RunLogicRunRoot:Block0],
// [RunLogicRunRoot:Block0:Action1]). MercIniTreeSons lists a node's children,
// RunLogicObjectKind says whether a child is a Group (block) or an Action,
// RunLogicRunMode is Sequential or Random, and RunLogicNumOfIterations is a
// block's pass count (the root's is the script's iteration count).
//
// The key that holds a child's percentage in a Random block has not been
// confirmed against a real export; a child key naming a percentage or weight
// is read, and a Random block without one picks its children uniformly.

var runLogicWeightKey = regexp.MustCompile(`(?i)(percent|weight)`)

// runLogicBlocksFromUSP turns a run-logic tree that runs more than one
// action, or any block, into config.run_logic.blocks. It returns nil for the
// usual single-action tree, which needs nothing.
func runLogicBlocksFromUSP(sections map[string]map[string]string) ([]dsl.RunLogicBlock, []string) {
	root := findSection(sections, "RunLogicRunRoot")
	if len(root) == 0 {
		return nil, nil
	}
	var notes []string
	var build func(path string, section map[string]string, depth int) (dsl.RunLogicBlock, bool)
	build = func(path string, section map[string]string, depth int) (dsl.RunLogicBlock, bool) {
		block := dsl.RunLogicBlock{Name: path[strings.LastIndex(path, ":")+1:], Mode: dsl.RunLogicSequential}
		if depth > dsl.MaxRunLogicBlockDepth {
			notes = append(notes, fmt.Sprintf("run-logic block %s nests too deep", path))
			return block, false
		}
		value := func(s map[string]string, key string) string {
			v, _ := sectionValueCaseInsensitive(s, key)
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
		if depth > 1 {
			if iterations, err := strconv.Atoi(value(section, "RunLogicNumOfIterations")); err == nil && iterations > 1 {
				block.Iterations = iterations
			}
		}
		random := strings.EqualFold(value(section, "RunLogicRunMode"), "random")
		weighted := false
		for _, son := range splitSourceActionNames(value(section, "MercIniTreeSons")) {
			childPath := path + ":" + son
			childSection := findSection(sections, childPath)
			child := dsl.RunLogicChild{}
			if strings.EqualFold(value(childSection, "RunLogicObjectKind"), "group") {
				nested, ok := build(childPath, childSection, depth+1)
				if !ok {
					return block, false
				}
				child.Block = &nested
			} else {
				child.Action = son
			}
			for key, raw := range childSection {
				if runLogicWeightKey.MatchString(key) {
					if weight, err := strconv.Atoi(strings.Trim(strings.TrimSpace(raw), `"%`)); err == nil && weight >= 0 {
						child.Weight = weight
						weighted = true
					}
				}
			}
			block.Children = append(block.Children, child)
		}
		if random {
			block.Mode = dsl.RunLogicRandom
			if weighted {
				block.Mode = dsl.RunLogicPercentage
			} else if len(block.Children) > 1 {
				notes = append(notes, fmt.Sprintf("run-logic block %s is Random without recorded percentages; its children are picked uniformly", path))
			}
		}
		return block, len(block.Children) > 0
	}
	block, ok := build("RunLogicRunRoot", root, 1)
	if !ok {
		return nil, notes
	}
	trivial := len(block.Children) == 1 && block.Children[0].Block == nil
	if trivial {
		return nil, nil
	}
	return []dsl.RunLogicBlock{block}, notes
}

// runLogicActionNames maps each action file (lowercased base name) to the
// run-logic action the .usr declares it as ([Actions] Action1=Action1.c).
func runLogicActionNames(project *ProjectConfig) map[string]string {
	if project == nil {
		return nil
	}
	names := map[string]string{}
	for _, source := range project.SourceActionFiles {
		if source.Function == "" || source.Name == "" {
			continue
		}
		names[strings.ToLower(path.Base(strings.ReplaceAll(source.Name, "\\", "/")))] = source.Function
	}
	return names
}

// tagRunLogicActions marks each step with the run-logic action its source
// file belongs to, so config.run_logic.blocks can name it.
func tagRunLogicActions(script *dsl.Script, names map[string]string) {
	if script == nil || len(script.Config.RunLogic.Blocks) == 0 || len(names) == 0 {
		return
	}
	for i := range script.Actions {
		action := &script.Actions[i]
		file := runLogicSourceFile(*action)
		name := names[strings.ToLower(path.Base(strings.ReplaceAll(file, "\\", "/")))]
		if name == "" {
			continue
		}
		if action.Metadata == nil {
			action.Metadata = map[string]any{}
		}
		action.Metadata[dsl.RunLogicActionMetadataKey] = name
	}
}

// pruneEmptyRunLogicChildren leaves out run-logic actions that import no
// steps (an Action() that only returns), which run nothing. In a sequential
// block that is exact; in a random or percentage block the other children
// are picked more often, which the returned notes say. A block left without
// children goes too.
func pruneEmptyRunLogicChildren(script *dsl.Script) []string {
	if script == nil || len(script.Config.RunLogic.Blocks) == 0 {
		return nil
	}
	groups := dsl.RunLogicActionGroups(script.Actions)
	var notes []string
	var prune func(block *dsl.RunLogicBlock) bool
	prune = func(block *dsl.RunLogicBlock) bool {
		kept := block.Children[:0]
		for _, child := range block.Children {
			if child.Block != nil {
				if prune(child.Block) {
					kept = append(kept, child)
				}
				continue
			}
			if len(groups[strings.ToLower(strings.TrimSpace(child.Action))]) > 0 {
				kept = append(kept, child)
				continue
			}
			if mode := strings.ToLower(block.Mode); mode == dsl.RunLogicRandom || mode == dsl.RunLogicPercentage {
				notes = append(notes, fmt.Sprintf("run-logic action %s imports no steps and is left out of %s block %s, so its other children are picked more often than in LoadRunner", child.Action, mode, block.Name))
			}
		}
		block.Children = kept
		if block.Mode == dsl.RunLogicPercentage {
			total := 0
			for _, child := range kept {
				total += max(child.Weight, 0)
			}
			if total == 0 {
				block.Mode = dsl.RunLogicRandom
			}
		}
		return len(kept) > 0
	}
	blocks := script.Config.RunLogic.Blocks[:0]
	for _, block := range script.Config.RunLogic.Blocks {
		if prune(&block) {
			blocks = append(blocks, block)
		}
	}
	script.Config.RunLogic.Blocks = blocks
	if len(blocks) == 0 {
		script.Config.RunLogic.Blocks = nil
	}
	return notes
}

// runLogicSourceFile is the source file of a step, or of the first step in
// a control block.
func runLogicSourceFile(action dsl.Action) string {
	if file, _ := action.Metadata["source_file"].(string); file != "" {
		return file
	}
	if action.Control != nil {
		for _, branch := range action.Control.ActionBranches() {
			for _, child := range branch.Actions {
				if file := runLogicSourceFile(child); file != "" {
					return file
				}
			}
		}
	}
	if requests, ok := action.Params["requests"].([]any); ok {
		for _, raw := range requests {
			if request, ok := raw.(map[string]any); ok {
				if metadata, ok := request["metadata"].(map[string]any); ok {
					if file, _ := metadata["source_file"].(string); file != "" {
						return file
					}
				}
			}
		}
	}
	return ""
}
