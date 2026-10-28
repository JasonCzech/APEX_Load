package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// browserAuthoredIndex is immutable after construction. A VUser retains one
// bounded index of its script, never a name-based or execution-order lookup.
// Runtime-generated actions outside that script remain identity-unavailable.
type browserAuthoredIndex struct {
	byAction map[*dsl.Action]*protocol.BrowserAuthoredStep
	byPath   map[string]*dsl.Action
}

func newBrowserAuthoredIndex(script *dsl.Script, sources ...*dsl.Script) *browserAuthoredIndex {
	if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "browser") {
		return nil
	}
	source := script
	if len(sources) > 1 {
		return nil
	}
	if len(sources) == 1 && sources[0] != nil {
		source = sources[0]
	}
	if !strings.EqualFold(strings.TrimSpace(source.Protocol), "browser") {
		return nil
	}
	index := &browserAuthoredIndex{byAction: make(map[*dsl.Action]*protocol.BrowserAuthoredStep), byPath: make(map[string]*dsl.Action)}

	locations := make(map[*dsl.Action]string)
	// Validate the walk before serializing: cycles, excessive depth and
	// definition counts must not enter hashing or leave a partial index.
	if err := dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if _, exists := locations[ref.Action]; exists {
			return fmt.Errorf("ambiguous authored browser location")
		}
		locations[ref.Action], index.byPath[ref.Path] = ref.Path, ref.Action
		return nil
	}); err != nil {
		return nil
	}
	// Preserve the existing replay digest semantics, including authored
	// configuration and metadata, before per-VUser interpolation.
	if source != script {
		seen := map[*dsl.Action]bool{}
		count := 0
		if err := dslwalk.WalkActions(source, func(ref dslwalk.ActionRef) error {
			peer := index.byPath[ref.Path]
			if seen[ref.Action] || peer == nil || peer.Type != ref.Action.Type {
				return fmt.Errorf("runtime browser script changed authored structure")
			}
			seen[ref.Action] = true
			count++
			return nil
		}); err != nil || count != len(locations) {
			return nil
		}
	}
	digest := replayScriptHash(source)
	if digest == "" {
		return nil
	}
	for action, path := range locations {
		identity := protocol.NewBrowserAuthoredStep(digest, path)
		if identity == nil {
			return nil
		}
		index.byAction[action] = identity
	}
	return index
}

// BrowserOriginalAuthoredDefinition snapshots original source before trusted
// runtime overrides. Empty/invalid scripts provide no original-source proof.
func BrowserOriginalAuthoredDefinition(script *dsl.Script) protocol.BrowserAuthoredDefinition {
	structure := browserAuthoredStructureDigest(script)
	if structure == "" {
		return protocol.BrowserAuthoredDefinition{}
	}
	definition := protocol.BrowserAuthoredDefinition{Version: 1, ScriptSHA256: replayScriptHash(script), StructureSHA256: structure}
	if !definition.Valid() {
		return protocol.BrowserAuthoredDefinition{}
	}
	return definition
}
func browserAuthoredStructureDigest(script *dsl.Script) string {
	if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "browser") {
		return ""
	}
	var entries [][2]string
	seen := map[*dsl.Action]bool{}
	if err := dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if seen[ref.Action] {
			return fmt.Errorf("ambiguous authored browser location")
		}
		seen[ref.Action] = true
		entries = append(entries, [2]string{ref.Path, ref.Action.Type})
		return nil
	}); err != nil {
		return ""
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte("browser_authored_structure.v1\x00"), raw...))
	return hex.EncodeToString(sum[:])
}
func newBrowserOriginalAuthoredIndex(script, source *dsl.Script, definition protocol.BrowserAuthoredDefinition) *browserAuthoredIndex {
	index := newBrowserAuthoredIndex(script, source)
	if definition == (protocol.BrowserAuthoredDefinition{}) {
		return index
	}
	if index == nil || !definition.Valid() || browserAuthoredStructureDigest(script) != definition.StructureSHA256 {
		return nil
	}
	for action, step := range index.byAction {
		index.byAction[action] = protocol.NewBrowserOriginalAuthoredStep(definition, step.StepPath)
	}
	return index
}

func (vu *VUser) browserAuthoredIndex() *browserAuthoredIndex {
	if vu.Script == nil {
		return nil
	}
	if vu.browserAuthoredScript != vu.Script || vu.browserAuthoredIndexedSource != vu.browserAuthoredSource || vu.browserAuthoredIndexedDefinition != vu.browserAuthoredDefinition {
		vu.browserAuthoredScript = vu.Script
		vu.browserAuthoredIndexedSource = vu.browserAuthoredSource
		vu.browserAuthoredIndexedDefinition = vu.browserAuthoredDefinition
		vu.browserAuthored = newBrowserOriginalAuthoredIndex(vu.Script, vu.browserAuthoredSource, vu.browserAuthoredDefinition)
	}
	return vu.browserAuthored
}

func (vu *VUser) browserAuthoredStepForAction(source *dsl.Action) *protocol.BrowserAuthoredStep {
	if vu == nil || source == nil {
		return nil
	}
	if index := vu.browserAuthoredIndex(); index != nil {
		return index.byAction[source].Clone()
	}
	return nil
}

func (vu *VUser) currentBrowserAuthoredStep() *protocol.BrowserAuthoredStep {
	if vu == nil {
		return nil
	}
	return vu.browserAuthoredStepForAction(vu.currentAuthoredAction)
}

func (vu *VUser) resolveBrowserAuthoredAction(action dsl.Action, source *dsl.Action) *protocol.Action {
	identity := vu.browserAuthoredStepForAction(source)
	resolved := vu.resolveAction(action)
	resolved.BrowserAuthoredStep = identity
	return resolved
}

var browserReplaySimpleLocation = regexp.MustCompile(`^(then|else|loop|default)\[(0|[1-9][0-9]{0,5})\]$`)
var browserReplayCaseLocation = regexp.MustCompile(`^case\[(0|[1-9][0-9]{0,5})\]\[(0|[1-9][0-9]{0,5})\]$`)
var browserReplayParallelLocation = regexp.MustCompile(`^branches\[(0|[1-9][0-9]{0,5})\]\.steps\[(0|[1-9][0-9]{0,5})\]$`)

// browserReplayAuthoredStep maps replay's existing structural debug location
// onto the same canonical DSL walk used by normal execution. Iteration numbers,
// runtime event IDs and action names are deliberately not identity inputs.
func (vu *VUser) browserReplayAuthoredStep(unit replayUnit) *protocol.BrowserAuthoredStep {
	index := vu.browserAuthoredIndex()
	if index == nil || unit.actionIndex < 0 || len(unit.stepPath) > 64 {
		return nil
	}
	phase := unit.phase
	if phase == "action" {
		phase = "actions"
	}
	if phase != "init" && phase != "actions" && phase != "teardown" {
		return nil
	}
	path := fmt.Sprintf("%s[%d]", phase, unit.actionIndex)
	for _, segment := range unit.stepPath {
		if len(segment) > 64 {
			return nil
		}
		switch {
		case browserReplaySimpleLocation.MatchString(segment):
			parts := browserReplaySimpleLocation.FindStringSubmatch(segment)
			branch := parts[1]
			if branch == "then" || branch == "loop" {
				branch = "steps"
			}
			path += fmt.Sprintf(".control.%s[%s]", branch, parts[2])
		case browserReplayCaseLocation.MatchString(segment):
			parts := browserReplayCaseLocation.FindStringSubmatch(segment)
			path += fmt.Sprintf(".control.cases[%s].steps[%s]", parts[1], parts[2])
		case browserReplayParallelLocation.MatchString(segment):
			path += ".control." + segment
		default:
			return nil
		}
	}
	source := index.byPath[path]
	if source == nil || source.Type != unit.action.Type {
		return nil
	}
	return index.byAction[source].Clone()
}

// ScriptJourneyStep describes a structural DSL step that can act as a journey
// start or success boundary during iteration execution.
type ScriptJourneyStep struct {
	StepPath   string `json:"step_path"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	ActionType string `json:"action_type"`
}

// ScriptJourneySteps walks script's iteration actions and returns its optional
// browser structure digest alongside the structural step boundaries editors
// can bind in a journey_policy.
func ScriptJourneySteps(script *dsl.Script) (string, []ScriptJourneyStep) {
	if script == nil || !strings.EqualFold(strings.TrimSpace(script.Protocol), "browser") {
		return "", nil
	}
	structureSHA256 := browserAuthoredStructureDigest(script)
	actionTxMode := normalizeAutoTransactionMode(script.Config.RunLogic.AutoTransactions) == "per_action"
	var steps []ScriptJourneyStep
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if !strings.HasPrefix(ref.Path, "actions[") || ref.Action == nil {
			return nil
		}
		canonical := dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(ref.Action.Type)))
		switch canonical {
		case "control_if", "control_loop", "control_switch", "control_parallel", "control_break", "control_continue":
			return nil
		case "source_transaction":
			switch normalizeSourceTransactionFunction(*ref.Action) {
			case "lr_start_transaction", "lr_start_sub_transaction":
				if name, _, ok := sourceTransactionStartArguments(*ref.Action); ok {
					steps = append(steps, ScriptJourneyStep{
						StepPath:   ref.Path,
						Kind:       "transaction",
						Name:       name,
						ActionType: canonical,
					})
				}
			case "lr_set_transaction":
				if name, _, _, ok := sourceSetTransactionArguments(*ref.Action); ok {
					steps = append(steps, ScriptJourneyStep{
						StepPath:   ref.Path,
						Kind:       "transaction",
						Name:       name,
						ActionType: canonical,
					})
				}
			}
			return nil
		case "http_sequence":
			if name := strings.TrimSpace(ref.Action.Name); name != "" {
				steps = append(steps, ScriptJourneyStep{
					StepPath:   ref.Path,
					Kind:       "transaction",
					Name:       name,
					ActionType: canonical,
				})
			}
			return nil
		case "transaction":
			if name := strings.TrimSpace(ref.Action.Name); name != "" {
				steps = append(steps, ScriptJourneyStep{
					StepPath:   ref.Path,
					Kind:       "transaction",
					Name:       name,
					ActionType: canonical,
				})
			}
			return nil
		default:
			name := strings.TrimSpace(ref.Action.Name)
			if name == "" {
				name = strings.TrimSpace(ref.Action.Type)
			}
			if name == "" {
				return nil
			}
			steps = append(steps, ScriptJourneyStep{
				StepPath:   ref.Path,
				Kind:       "action",
				Name:       name,
				ActionType: canonical,
			})
			if actionTxMode {
				steps = append(steps, ScriptJourneyStep{
					StepPath:   ref.Path,
					Kind:       "transaction",
					Name:       name,
					ActionType: canonical,
				})
			}
			return nil
		}
	})
	return structureSHA256, steps
}
