package api

// Replay self-heal write-back. When a browser replay (debug or browser-replay)
// heals a failing selector by falling back to a ranked candidate, and the action
// was bound to an object repository via object_ref, the healed locator is fed
// back onto the originating repository object so the working selector is shared
// by every script that references it. This runs only in the in-process replay
// path (the controller has the store); distributed load runs cannot write back.

import (
	"log/slog"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/selector"
	"github.com/jasonczech/apexload/pkg/storage"
)

// selfHealRecord is one observed selector self-heal extracted from a replay
// report's per-action protocol evidence.
type selfHealRecord struct {
	actionName string
	healed     string
	kind       string
	confidence int
}

// objectRefBinding records how a prepared action was bound to a repository
// object (logical name + the repository the resolver chose).
type objectRefBinding struct {
	logicalName string
	repoID      string
}

// persistSelfHealToRepository inspects a finished replay report for selector
// self-heals on object_ref-bound actions and appends the working locator back
// onto the originating repository object. It is conservative: a healed locator
// already present is left in place (its confidence is only raised, never
// lowered) so a single lucky heal cannot reorder a shared object's primary
// selector. Promotion is left to a human via the dashboard.
func (s *Server) persistSelfHealToRepository(projectID string, script *dsl.Script, report *engine.ReplayReport) {
	if s == nil || s.store == nil || script == nil || report == nil {
		return
	}
	heals := collectSelfHeals(report)
	if len(heals) == 0 {
		return
	}
	bindings := objectRefBindingsByActionName(script)
	if len(bindings) == 0 {
		return
	}

	loaded := map[string]*storage.ObjectRepository{}
	changed := map[string]bool{}
	for _, heal := range heals {
		binding, ok := bindings[heal.actionName]
		if !ok || binding.repoID == "" || binding.logicalName == "" {
			continue
		}
		repo := loaded[binding.repoID]
		if repo == nil {
			fetched, err := s.store.GetObjectRepository(binding.repoID)
			if err != nil || fetched == nil {
				continue
			}
			repo = fetched
			loaded[binding.repoID] = repo
		}
		if applyHealToRepositoryObject(repo, binding.logicalName, heal) {
			changed[binding.repoID] = true
		}
	}
	for repoID, repo := range loaded {
		if !changed[repoID] {
			continue
		}
		if err := s.store.SaveObjectRepository(repo); err != nil {
			slog.Warn("persist self-heal to object repository failed", "repository", repoID, "error", err)
		}
	}
}

// collectSelfHeals extracts selector self-heal evidence from each replay event.
func collectSelfHeals(report *engine.ReplayReport) []selfHealRecord {
	var out []selfHealRecord
	for i := range report.Events {
		evidence := report.Events[i].ProtocolEvidence
		raw, ok := evidence["selector_self_heal"]
		if !ok {
			continue
		}
		fields, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rec := selfHealRecord{
			actionName: anyToString(fields["action"]),
			healed:     anyToString(fields["healed_selector"]),
			kind:       anyToString(fields["candidate_kind"]),
			confidence: anyToInt(fields["confidence"]),
		}
		if rec.actionName == "" {
			rec.actionName = report.Events[i].ActionName
		}
		if rec.actionName == "" || rec.healed == "" {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// objectRefBindingsByActionName indexes the prepared script's object_ref
// bindings by action name (the key self-heal evidence reports under "action").
func objectRefBindingsByActionName(script *dsl.Script) map[string]objectRefBinding {
	out := map[string]objectRefBinding{}
	var walk func(actions []dsl.Action)
	walk = func(actions []dsl.Action) {
		for i := range actions {
			action := &actions[i]
			ref := strings.TrimSpace(action.ObjectRef)
			name := strings.TrimSpace(action.Name)
			if ref != "" && name != "" {
				_, logicalName := splitObjectRef(ref)
				repoID := ""
				if action.Metadata != nil {
					if v, ok := action.Metadata[objectRefRepositoryMetadataKey].(string); ok {
						repoID = v
					}
				}
				out[name] = objectRefBinding{logicalName: logicalName, repoID: repoID}
			}
			if action.Control != nil {
				walk(action.Control.Steps)
				walk(action.Control.Else)
			}
		}
	}
	walk(script.Init)
	walk(script.Actions)
	walk(script.Teardown)
	return out
}

// applyHealToRepositoryObject appends (or strengthens) the healed locator on the
// named object. Returns true when the repository changed.
func applyHealToRepositoryObject(repo *storage.ObjectRepository, logicalName string, heal selfHealRecord) bool {
	key := strings.ToLower(strings.TrimSpace(logicalName))
	for i := range repo.Objects {
		obj := &repo.Objects[i]
		if strings.ToLower(strings.TrimSpace(obj.LogicalName)) != key {
			continue
		}
		for j := range obj.Candidates {
			engineForm, ok := selector.Usable(selector.Candidate{
				Kind:  obj.Candidates[j].Kind,
				Value: obj.Candidates[j].Value,
			})
			if !ok || engineForm != heal.healed {
				continue
			}
			// Already a known candidate: only raise its confidence, never lower it.
			if heal.confidence > obj.Candidates[j].Confidence {
				obj.Candidates[j].Confidence = heal.confidence
				recomputeBestConfidence(obj)
				return true
			}
			return false
		}
		obj.Candidates = append(obj.Candidates, storage.RepositoryObjectCandidate{
			Kind:       heal.kind,
			Value:      heal.healed,
			Confidence: heal.confidence,
			Reason:     "observed via replay self-heal",
		})
		recomputeBestConfidence(obj)
		return true
	}
	return false
}

func recomputeBestConfidence(obj *storage.RepositoryObject) {
	best := 0
	for _, candidate := range obj.Candidates {
		if candidate.Confidence > best {
			best = candidate.Confidence
		}
	}
	obj.BestConfidence = best
}

func anyToString(value any) string {
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func anyToInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}
