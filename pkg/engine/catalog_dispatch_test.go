package engine

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// TestEngineDispatchCoversCatalogEngineActions cross-checks the dsl action
// catalog against the engine's dispatch: every engine-action spec must be
// executable by the VUser loop, so a script that validates cannot hit an
// unknown engine type at runtime. Control-flow blocks run in the iteration
// loop rather than handleEngineAction, and review_required is an authoring
// placeholder that launch gating resolves before execution — those are the
// only permitted exceptions.
func TestEngineDispatchCoversCatalogEngineActions(t *testing.T) {
	dispatchedElsewhere := map[string]string{
		"control_if":       "executed by the iteration loop's control-flow handler",
		"control_switch":   "executed by the iteration loop's control-flow handler",
		"control_loop":     "executed by the iteration loop's control-flow handler",
		"control_parallel": "executed by the iteration loop's control-flow handler",
		"review_required":  "authoring placeholder; resolved before launch, never dispatched",
	}

	for _, spec := range dsl.AllActionSpecs() {
		if !spec.EngineAction() {
			continue
		}
		if reason, ok := dispatchedElsewhere[spec.Type]; ok {
			t.Logf("%s: skipped (%s)", spec.Type, reason)
			continue
		}
		vu := &VUser{
			ID:        1,
			Group:     "cross-check",
			Script:    &dsl.Script{Name: "catalog-cross-check"},
			Variables: map[string]string{},
		}
		action := dsl.Action{Name: "step", Type: spec.Type}
		if spec.Type == "source_verification" {
			vu.Proto = &httpprotocol.HTTP{}
			action.Params = map[string]any{"operation": "register", "id": "check", "text": "error", "scope": "all", "fail_if_found": true}
		}
		handled, err := vu.handleEngineAction(context.Background(), action)
		if !handled {
			t.Errorf("%s: catalog engine action not handled by handleEngineAction", spec.Type)
		}
		if _, unsupported := unsupportedEngineActions[spec.Type]; unsupported {
			if err == nil {
				t.Errorf("%s: unsupported LoadRunner document helper reported success", spec.Type)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: handleEngineAction returned error: %v", spec.Type, err)
		}
	}
}

// unsupportedEngineActions are dispatched so saved drafts fail with an explicit
// message instead of an unknown-type error, but they must never report success.
var unsupportedEngineActions = map[string]string{
	"source_json": "LoadRunner JSON helpers other than lr_eval_json, lr_json_get_values and lr_json_stringify are not evaluated",
	"source_xml":  "LoadRunner XML helpers are not evaluated yet",
}

// TestEngineDispatchTypesAreInCatalog is the reverse direction: every type
// handleEngineAction dispatches must be declared as an engine action in the
// catalog, so nothing executable is invisible to validation, schema, and
// editor tooling. Engine leaf types are read from the actual dispatch switch,
// avoiding a second hand-maintained list that can drift when a case is added.
func TestEngineDispatchTypesAreInCatalog(t *testing.T) {
	dispatched := engineActionTypesFromHandler(t)
	// Containers are dispatched by the iteration loop rather than
	// handleEngineAction. Keep this small architectural exception explicit.
	dispatched = append(dispatched, "control_if", "control_switch", "control_loop", "control_parallel")
	sort.Strings(dispatched)
	// OAuth token acquisition runs in the engine so it can publish the token to
	// VUser variables before later actions, but it deliberately remains scoped
	// to HTTP scripts because it reuses the active HTTP protocol's governed
	// client. This is the sole protocol-scoped engine dispatch.
	protocolScoped := map[string]string{"http_oauth2_token": "http"}
	for _, actionType := range dispatched {
		spec, ok := dsl.SpecForActionType(actionType)
		if !ok {
			t.Errorf("%s: dispatched by engine but missing from the dsl action catalog", actionType)
			continue
		}
		if spec.EngineAction() {
			continue
		}
		family, scoped := protocolScoped[actionType]
		if !scoped || len(spec.Protocols) != 1 || spec.Protocols[0] != family {
			t.Errorf("%s: dispatched by engine but not declared as an engine action or the expected protocol-scoped dispatch (protocols %v)", actionType, spec.Protocols)
		}
	}
}

func engineActionTypesFromHandler(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "vuser_source_actions.go", nil, 0)
	if err != nil {
		t.Fatalf("parse engine action dispatcher: %v", err)
	}

	types := map[string]struct{}{}
	foundHandler := false
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "handleEngineAction" {
			continue
		}
		foundHandler = true
		ast.Inspect(function.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expression := range clause.List {
				literal, ok := expression.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("decode engine action case %q: %v", literal.Value, err)
				}
				types[value] = struct{}{}
			}
			return true
		})
	}
	if !foundHandler {
		t.Fatal("handleEngineAction was not found in vuser_source_actions.go")
	}
	if len(types) == 0 {
		t.Fatal("handleEngineAction contains no string action cases")
	}

	dispatched := make([]string, 0, len(types))
	for actionType := range types {
		dispatched = append(dispatched, actionType)
	}
	return dispatched
}
