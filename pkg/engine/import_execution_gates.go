package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

// importExecutionGateError enforces durable import blockers at the engine
// boundary. API handlers perform the same check earlier so they can return
// workflow-specific responses, but direct CLI and library callers must not be
// able to bypass a stored blocker.
//
// Only explicit execution_blocker(s) metadata is interpreted here. Historical
// review_required metadata remains governed by its existing browser, Bruno,
// and Java policies.
func importExecutionGateError(script *dsl.Script, operation string) error {
	if script == nil {
		return nil
	}
	if err := RetiredBrunoRuntimeError(script); err != nil {
		return err
	}
	operation = strings.TrimSpace(operation)
	if operation == "" {
		operation = "execution"
	}
	// HTTP/3 admission for every path that builds an engine: local, embedded,
	// CLI, replay and shakeout runs, and each generator's own execution.
	if exact, upgrade := dsl.ScriptHTTP3Policies(script); upgrade {
		if err := httpprotocol.HTTP3UpgradeExecutionError(); err != nil {
			return fmt.Errorf("script %s is blocked: %w", operation, err)
		}
	} else if exact {
		if err := httpprotocol.HTTP3ExecutionError(); err != nil {
			return fmt.Errorf("script %s is blocked: %w", operation, err)
		}
	}

	var issues []string
	if script.Config.JMeter != nil || len(script.Config.SamplerSchedules) > 0 {
		issues = append(issues, "JMeter messaging runtime compatibility is unavailable; reference conformance is unverified")
	}
	for _, p := range script.Parameters {
		if p.Scope == "source_engine" || p.ScopeID != "" || p.RandomAlgorithm != "" {
			issues = append(issues, "shared JMeter parameter compatibility is unavailable")
			break
		}
	}
	err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if ref.Action != nil && len(ref.Action.PacingIDs) > 0 {
			issues = append(issues, ref.Path+" requires unsupported JMeter sampler pacing")
		}
		codes := executionBlockerCodes(ref.Metadata())
		if len(codes) > 0 {
			issues = append(issues, formatImportExecutionGateIssue(
				ref.Path,
				ref.Name(),
				codes,
			))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("inspect import execution gates: %w", err)
	}
	if len(issues) == 0 {
		return nil
	}
	sort.Strings(issues)
	return fmt.Errorf("script %s is blocked: %s", operation, strings.Join(issues, "; "))
}

func executionBlockerCodes(metadata map[string]any) []string {
	if len(metadata) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	if blocker, ok := metadata["execution_blocker"].(string); ok {
		add(blocker)
	}
	switch blockers := metadata["execution_blockers"].(type) {
	case []string:
		for _, blocker := range blockers {
			add(blocker)
		}
	case []any:
		for _, raw := range blockers {
			if blocker, ok := raw.(string); ok {
				add(blocker)
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for code := range seen {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

func formatImportExecutionGateIssue(path, name string, codes []string) string {
	label := strings.TrimSpace(path)
	if name = strings.TrimSpace(name); name != "" {
		label += " " + fmt.Sprintf("%q", name)
	}
	return fmt.Sprintf("%s has execution blocker(s) %s", label, strings.Join(codes, ", "))
}
