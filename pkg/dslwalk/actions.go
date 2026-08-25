// Package dslwalk provides bounded, deterministic traversal of every action in
// a DSL script, including nested control-flow branches. It is deliberately
// outside pkg/api so importers, parameterization, validation helpers, and
// runtime-readiness analysis can share one traversal contract.
package dslwalk

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Phase identifies a top-level script action section.
type Phase string

const (
	Init     Phase = "init"
	Actions  Phase = "actions"
	Teardown Phase = "teardown"
)

// ActionRef identifies a mutable action and its deterministic DSL path.
// Visitors must not append to or replace the containing action slices while a
// walk is active; mutating fields of Action itself is supported.
type ActionRef struct {
	Phase  Phase
	Path   string
	Depth  int
	Action *dsl.Action
}

// VisitFunc is called once per action in pre-order.
type VisitFunc func(ActionRef) error

// SurfaceKind identifies a typed DSL action or an untyped request carried by
// an http_sequence action.
type SurfaceKind string

const (
	ActionSurface              SurfaceKind = "action"
	HTTPSequenceRequestSurface SurfaceKind = "http_sequence_request"
)

// SurfaceRef identifies one mutable request-shaped surface and its
// deterministic DSL path.
//
// Action is populated for every regular action and for http_sequence children
// stored as []dsl.Action. Request is populated for sequence children stored in
// their canonical JSON-shaped map form. OwnerAction and OwnerPath identify the
// containing typed action; for an ActionSurface they are the action itself.
//
// Visitors may mutate Action fields or values in Request. An Action stored by
// value inside []any is written back after the visit. Visitors must not append
// to, replace, or reorder action slices or params.requests while a walk is
// active.
type SurfaceRef struct {
	Kind        SurfaceKind
	Phase       Phase
	Path        string
	Depth       int
	Action      *dsl.Action
	Request     map[string]any
	OwnerAction *dsl.Action
	OwnerPath   string
}

// Name returns the authored surface name without manufacturing a fallback.
func (ref SurfaceRef) Name() string {
	if ref.Action != nil {
		return ref.Action.Name
	}
	if ref.Request != nil {
		name, _ := ref.Request["name"].(string)
		return name
	}
	return ""
}

// Metadata returns the existing metadata map for a surface. It deliberately
// does not reinterpret historical review_required flags or allocate metadata.
func (ref SurfaceRef) Metadata() map[string]any {
	if ref.Action != nil {
		return ref.Action.Metadata
	}
	if ref.Request != nil {
		metadata, _ := ref.Request["metadata"].(map[string]any)
		return metadata
	}
	return nil
}

// SetMetadata replaces a surface's metadata while preserving its source
// representation. It is useful to shared gate/finalization code that must
// mutate typed actions and JSON-shaped http_sequence children uniformly.
func (ref SurfaceRef) SetMetadata(metadata map[string]any) {
	if ref.Action != nil {
		ref.Action.Metadata = metadata
		return
	}
	if ref.Request != nil {
		if metadata == nil {
			delete(ref.Request, "metadata")
			return
		}
		ref.Request["metadata"] = metadata
	}
}

// SurfaceVisitFunc is called once per action/request definition in pre-order.
type SurfaceVisitFunc func(SurfaceRef) error

// Walk visits init, actions, and teardown in that order. Each typed action is
// visited before its ordered http_sequence child requests. control.steps is
// then visited before control.else, ordered switch cases, control.default, and
// before the next sibling.
//
// Traversal is iterative and enforces the shared combined definition and
// control-depth budgets. Malformed sequence entries count toward the
// definition budget but are left to DSL validation and are not visited as
// request surfaces.
func Walk(script *dsl.Script, visit SurfaceVisitFunc) error {
	if script == nil || visit == nil {
		return nil
	}
	stack := make([]actionFrame, 0, len(script.Init)+len(script.Actions)+len(script.Teardown))
	pushRoots := func(phase Phase, actions []dsl.Action) {
		for index := len(actions) - 1; index >= 0; index-- {
			stack = append(stack, actionFrame{
				phase:   phase,
				path:    fmt.Sprintf("%s[%d]", phase, index),
				depth:   1,
				actions: actions,
				index:   index,
			})
		}
	}
	// LIFO: push reverse phase order so init is visited first.
	pushRoots(Teardown, script.Teardown)
	pushRoots(Actions, script.Actions)
	pushRoots(Init, script.Init)

	visited := 0
	for len(stack) > 0 {
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		if current.depth > dsl.MaxScriptActionDepth {
			return fmt.Errorf("action nesting exceeds %d levels at %s", dsl.MaxScriptActionDepth, current.path)
		}
		if current.index < 0 || current.index >= len(current.actions) {
			return errors.New("action traversal encountered an invalid slice index")
		}
		visited++
		if visited > dsl.MaxScriptActionDefinitions {
			return fmt.Errorf("script action definitions exceed %d", dsl.MaxScriptActionDefinitions)
		}
		action := &current.actions[current.index]
		if err := visit(SurfaceRef{
			Kind:        ActionSurface,
			Phase:       current.phase,
			Path:        current.path,
			Depth:       current.depth,
			Action:      action,
			OwnerAction: action,
			OwnerPath:   current.path,
		}); err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
			var err error
			visited, err = walkHTTPSequenceRequests(
				current.phase,
				current.path,
				current.depth,
				action,
				visited,
				visit,
			)
			if err != nil {
				return err
			}
		}
		if action.Control == nil {
			continue
		}
		nextDepth := current.depth + 1
		pushBranch := func(name string, actions []dsl.Action) {
			for index := len(actions) - 1; index >= 0; index-- {
				stack = append(stack, actionFrame{
					phase:   current.phase,
					path:    fmt.Sprintf("%s.control.%s[%d]", current.path, name, index),
					depth:   nextDepth,
					actions: actions,
					index:   index,
				})
			}
		}
		// LIFO: push reverse semantic order so traversal remains deterministic:
		// steps, else, cases[0..n], default, parallel branches[0..n].
		for branchIndex := len(action.Control.Branches) - 1; branchIndex >= 0; branchIndex-- {
			name := fmt.Sprintf("branches[%d].steps", branchIndex)
			pushBranch(name, action.Control.Branches[branchIndex].Steps)
		}
		pushBranch("default", action.Control.Default)
		for caseIndex := len(action.Control.Cases) - 1; caseIndex >= 0; caseIndex-- {
			name := fmt.Sprintf("cases[%d].steps", caseIndex)
			pushBranch(name, action.Control.Cases[caseIndex].Steps)
		}
		pushBranch("else", action.Control.Else)
		pushBranch("steps", action.Control.Steps)
	}
	return nil
}

// WalkActions is the compatibility action-only projection of Walk. Sequence
// children still count toward the shared structural budget but are not passed
// to the action visitor.
func WalkActions(script *dsl.Script, visit VisitFunc) error {
	if visit == nil {
		return nil
	}
	return Walk(script, func(ref SurfaceRef) error {
		if ref.Kind != ActionSurface {
			return nil
		}
		return visit(ActionRef{
			Phase:  ref.Phase,
			Path:   ref.Path,
			Depth:  ref.Depth,
			Action: ref.Action,
		})
	})
}

func walkHTTPSequenceRequests(
	phase Phase,
	ownerPath string,
	depth int,
	owner *dsl.Action,
	visited int,
	visit SurfaceVisitFunc,
) (int, error) {
	if owner == nil || owner.Params == nil {
		return visited, nil
	}
	raw, exists := owner.Params["requests"]
	if !exists {
		return visited, nil
	}
	visitDefinition := func(index int, action *dsl.Action, request map[string]any) error {
		visited++
		if visited > dsl.MaxScriptActionDefinitions {
			return fmt.Errorf("script action/request definitions exceed %d", dsl.MaxScriptActionDefinitions)
		}
		if action == nil && request == nil {
			return nil
		}
		return visit(SurfaceRef{
			Kind:        HTTPSequenceRequestSurface,
			Phase:       phase,
			Path:        fmt.Sprintf("%s.params.requests[%d]", ownerPath, index),
			Depth:       depth,
			Action:      action,
			Request:     request,
			OwnerAction: owner,
			OwnerPath:   ownerPath,
		})
	}

	switch requests := raw.(type) {
	case []any:
		for index := range requests {
			switch request := requests[index].(type) {
			case map[string]any:
				if err := visitDefinition(index, nil, request); err != nil {
					return visited, err
				}
			case *dsl.Action:
				if err := visitDefinition(index, request, nil); err != nil {
					return visited, err
				}
			case dsl.Action:
				requestCopy := request
				if err := visitDefinition(index, &requestCopy, nil); err != nil {
					return visited, err
				}
				requests[index] = requestCopy
			default:
				if err := visitDefinition(index, nil, nil); err != nil {
					return visited, err
				}
			}
		}
	case []map[string]any:
		for index := range requests {
			if err := visitDefinition(index, nil, requests[index]); err != nil {
				return visited, err
			}
		}
	case []dsl.Action:
		for index := range requests {
			if err := visitDefinition(index, &requests[index], nil); err != nil {
				return visited, err
			}
		}
	case []*dsl.Action:
		for index := range requests {
			if err := visitDefinition(index, requests[index], nil); err != nil {
				return visited, err
			}
		}
	}
	return visited, nil
}

type actionFrame struct {
	phase   Phase
	path    string
	depth   int
	actions []dsl.Action
	index   int
}
