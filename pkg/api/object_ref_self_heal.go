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

	// Serialize the load→mutate→save sequence: completion hooks from the browser
	// replay and script debug managers can run concurrently, and SaveObjectRepository
	// rewrites the entire objects blob, so an interleaved read-modify-write would
	// silently drop one session's appended/promoted candidate.
	s.selfHealMu.Lock()
	defer s.selfHealMu.Unlock()

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
		if applyHealToRepositoryObject(repo, binding.logicalName, heal, repo.AutoPromoteOnHeal) {
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
	ambiguous := map[string]struct{}{}
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
				binding := objectRefBinding{logicalName: logicalName, repoID: repoID}
				if _, dropped := ambiguous[name]; dropped {
					// Already known to be ambiguous; keep skipping it.
				} else if existing, seen := out[name]; seen && existing != binding {
					// Two object_ref-bound actions share a name but resolve to
					// different repository objects, so a heal reported under this name
					// cannot be routed unambiguously. Drop the binding rather than
					// write a healed locator onto the wrong object.
					delete(out, name)
					ambiguous[name] = struct{}{}
				} else {
					out[name] = binding
				}
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

// reasonPromotedByHeal marks a repository candidate that replay self-heal
// auto-promoted to primary. primarySelectorFromCandidates honors it so a promoted
// heal becomes the chosen primary even when it is a role/text locator (which
// PlaywrightSelector alone would skip).
const reasonPromotedByHeal = "promoted by replay self-heal"

// applyHealToRepositoryObject records the healed locator on the named object and
// returns true when the repository changed. By default it is conservative — the
// healed candidate is appended if absent and its confidence only ever raised,
// never reordered above the primary. When autoPromote is set (repository opt-in)
// the healed candidate is pushed to strictly the highest confidence so the next
// script-prep resolution selects it as primary without manual intervention.
func applyHealToRepositoryObject(repo *storage.ObjectRepository, logicalName string, heal selfHealRecord, autoPromote bool) bool {
	key := strings.ToLower(strings.TrimSpace(logicalName))
	for i := range repo.Objects {
		obj := &repo.Objects[i]
		if strings.ToLower(strings.TrimSpace(obj.LogicalName)) != key {
			continue
		}
		idx := indexOfCandidateBySelector(obj.Candidates, heal.healed)
		appended := false
		var before storage.RepositoryObjectCandidate
		if idx < 0 {
			obj.Candidates = append(obj.Candidates, storage.RepositoryObjectCandidate{
				Kind:       heal.kind,
				Value:      heal.healed,
				Confidence: heal.confidence,
				Reason:     "observed via replay self-heal",
			})
			idx = len(obj.Candidates) - 1
			appended = true
		} else {
			before = obj.Candidates[idx]
			// Never lower a known candidate's confidence.
			if heal.confidence > obj.Candidates[idx].Confidence {
				obj.Candidates[idx].Confidence = heal.confidence
			}
		}
		if autoPromote {
			highestOther := 0
			for j := range obj.Candidates {
				if j != idx && obj.Candidates[j].Confidence > highestOther {
					highestOther = obj.Candidates[j].Confidence
				}
			}
			promoted := min(highestOther+1, 100)
			if obj.Candidates[idx].Confidence < promoted {
				obj.Candidates[idx].Confidence = promoted
			}
			obj.Candidates[idx].Reason = reasonPromotedByHeal
		}
		changed := appended || obj.Candidates[idx] != before
		if changed {
			recomputeBestConfidence(obj)
		}
		return changed
	}
	return false
}

// indexOfCandidateBySelector returns the index of the candidate whose engine
// selector form equals the given selector, or -1.
func indexOfCandidateBySelector(candidates []storage.RepositoryObjectCandidate, engineSelector string) int {
	for j := range candidates {
		engineForm, ok := selector.Usable(selector.Candidate{Kind: candidates[j].Kind, Value: candidates[j].Value})
		if ok && engineForm == engineSelector {
			return j
		}
	}
	return -1
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
