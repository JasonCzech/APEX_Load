package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

type generatorEvacuateRequest struct {
	DryRun bool   `json:"dry_run,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type GeneratorEvacuationResponse struct {
	GeneratorID   string                      `json:"generator_id,omitempty"`
	Target        string                      `json:"target"`
	DryRun        bool                        `json:"dry_run"`
	Applied       bool                        `json:"applied"`
	ImpactedTests int                         `json:"impacted_tests"`
	Impacts       []GeneratorEvacuationImpact `json:"impacts"`
	Error         string                      `json:"error,omitempty"`
}

type GeneratorLifecycleResponse struct {
	GeneratorID string `json:"generator_id,omitempty"`
	Target      string `json:"target,omitempty"`
	DrainState  string `json:"drain_state"`
	Cordoned    bool   `json:"cordoned"`
}

func (s *Server) handleCordonGenerator(w http.ResponseWriter, r *http.Request) {
	s.handleGeneratorLifecycle(w, r, "cordoned", "generator.cordon", "generator_cordoned")
}

func (s *Server) handleUncordonGenerator(w http.ResponseWriter, r *http.Request) {
	s.handleGeneratorLifecycle(w, r, "active", "generator.uncordon", "generator_uncordoned")
}

func (s *Server) handleDrainGenerator(w http.ResponseWriter, r *http.Request) {
	s.handleGeneratorLifecycle(w, r, "draining", "generator.drain", "generator_draining")
}

func (s *Server) handleGeneratorLifecycle(w http.ResponseWriter, r *http.Request, state string, auditAction string, eventType string) {
	if !s.requireExecutionLeader(w) {
		return
	}
	generatorID, target := s.resolveGeneratorIdentityFromPath(strings.TrimSpace(r.PathValue("id")))
	generatorID, target, err := s.setGeneratorLifecycleState(generatorID, target, state)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cordoned := state != "active"
	if err := s.syncGeneratorMetadataCordon(generatorID, target, cordoned); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist generator cordon metadata: "+err.Error())
		return
	}
	s.recordAuditEvent(r, auditAction, "success", "generator", firstNonEmpty(generatorID, target), "", map[string]any{
		"target": target, "generator_id": generatorID, "drain_state": state,
	})
	s.broadcastFleetEvent(eventType, map[string]any{"target": target, "generator_id": generatorID, "drain_state": state, "cordoned": cordoned})
	writeJSON(w, http.StatusOK, GeneratorLifecycleResponse{
		GeneratorID: generatorID,
		Target:      target,
		DrainState:  state,
		Cordoned:    cordoned,
	})
}

func (s *Server) handleEvacuateGenerator(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var req generatorEvacuateRequest
	if !decodeOptionalJSONBody(w, r, &req) {
		return
	}
	generatorID, target := s.resolveGeneratorIdentityFromPath(strings.TrimSpace(r.PathValue("id")))
	if target == "" {
		writeError(w, http.StatusBadRequest, "generator target is required")
		return
	}
	backend, ok := s.runner.getExecutionBackend().(evacuatingExecutionBackend)
	if !ok {
		writeError(w, http.StatusBadRequest, "generator evacuation requires distributed execution")
		return
	}
	if !req.DryRun {
		if _, _, err := s.setGeneratorLifecycleState(generatorID, target, "draining"); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.syncGeneratorMetadataCordon(generatorID, target, true); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to persist generator cordon metadata: "+err.Error())
			return
		}
	}

	tests := s.activeTestsUsingGeneratorTarget(target)
	response := GeneratorEvacuationResponse{
		GeneratorID:   generatorID,
		Target:        target,
		DryRun:        req.DryRun,
		Applied:       !req.DryRun,
		ImpactedTests: len(tests),
		Impacts:       []GeneratorEvacuationImpact{},
	}
	for _, test := range tests {
		impacts, err := backend.EvacuateGenerator(s.runner, test, target, req.DryRun, firstNonEmpty(strings.TrimSpace(req.Reason), "operator_evacuate"))
		response.Impacts = append(response.Impacts, impacts...)
		if err != nil {
			response.Applied = false
			response.Error = err.Error()
			s.recordAuditEvent(r, "generator.evacuate", "failure", "generator", firstNonEmpty(generatorID, target), "", map[string]any{
				"target": target, "dry_run": req.DryRun, "error": err.Error(),
			})
			writeJSON(w, http.StatusConflict, response)
			return
		}
	}
	s.recordAuditEvent(r, "generator.evacuate", "success", "generator", firstNonEmpty(generatorID, target), "", map[string]any{
		"target": target, "dry_run": req.DryRun, "impacted_tests": response.ImpactedTests,
	})
	s.broadcastFleetEvent("generator_evacuated", map[string]any{
		"target": target, "generator_id": generatorID, "dry_run": req.DryRun, "impacted_tests": response.ImpactedTests,
	})
	writeJSON(w, http.StatusOK, response)
}

func decodeOptionalJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		return true
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return false
	}
	return true
}

func (s *Server) resolveGeneratorIdentityFromPath(raw string) (string, string) {
	if raw == "" || s == nil || s.runner == nil {
		return "", raw
	}
	generatorID := raw
	target := raw
	if identity, ok := s.runner.getExecutionBackend().(identityDrainableExecutionBackend); ok {
		if resolvedTarget := identity.GeneratorTargetForID(raw); resolvedTarget != "" {
			target = resolvedTarget
			return generatorID, target
		}
		if resolvedID := identity.GeneratorIDForTarget(raw); resolvedID != "" {
			generatorID = resolvedID
			target = raw
			return generatorID, target
		}
	}
	return generatorID, target
}

func (s *Server) markGeneratorDraining(generatorID string, target string) error {
	_, _, err := s.setGeneratorLifecycleState(generatorID, target, "draining")
	return err
}

func (s *Server) setGeneratorLifecycleState(generatorID string, target string, state string) (string, string, error) {
	generatorID = strings.TrimSpace(generatorID)
	target = strings.TrimSpace(target)
	state = strings.TrimSpace(state)
	backend, ok := s.runner.getExecutionBackend().(drainableExecutionBackend)
	if !ok {
		return generatorID, target, fmt.Errorf("generator drain control requires distributed execution")
	}
	if generatorID != "" {
		if identityBackend, ok := backend.(identityDrainableExecutionBackend); ok {
			resolvedTarget, err := identityBackend.SetGeneratorDrainStateByID(generatorID, state)
			if err != nil {
				if target == "" || target != generatorID {
					return generatorID, target, err
				}
				if err := backend.SetGeneratorDrainState(target, state); err != nil {
					return generatorID, target, err
				}
				generatorID = identityBackend.GeneratorIDForTarget(target)
			} else {
				if target == "" {
					target = resolvedTarget
				}
			}
		} else if target != "" {
			if err := backend.SetGeneratorDrainState(target, state); err != nil {
				return generatorID, target, err
			}
		} else {
			return generatorID, target, fmt.Errorf("generator_id drain control requires generator identity support")
		}
	} else if target != "" {
		if err := backend.SetGeneratorDrainState(target, state); err != nil {
			return generatorID, target, err
		}
		if identityBackend, ok := backend.(identityDrainableExecutionBackend); ok {
			generatorID = identityBackend.GeneratorIDForTarget(target)
		}
	} else {
		return generatorID, target, fmt.Errorf("target or generator_id is required")
	}
	if persistent, ok := backend.(persistentDrainBackend); ok {
		if err := s.saveGeneratorDrainStates(persistent.GeneratorDrainStates()); err != nil {
			return generatorID, target, fmt.Errorf("persist generator drain state: %w", err)
		}
	}
	return generatorID, target, nil
}

func (s *Server) syncGeneratorMetadataCordon(generatorID string, target string, cordoned bool) error {
	if s == nil || s.store == nil {
		return nil
	}
	generatorID = strings.TrimSpace(firstNonEmpty(generatorID, target))
	target = strings.TrimSpace(target)
	if generatorID == "" {
		return nil
	}
	metadata, err := s.store.GetGeneratorMetadata(generatorID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		metadata = &storage.GeneratorMetadata{GeneratorID: generatorID}
	}
	if target != "" {
		metadata.Target = target
	}
	metadata.Cordoned = cordoned
	return s.store.SaveGeneratorMetadata(metadata)
}

func (s *Server) activeTestsUsingGeneratorTarget(target string) []*ManagedTest {
	target = strings.TrimSpace(target)
	if target == "" || s == nil || s.runner == nil {
		return nil
	}
	s.runner.mu.RLock()
	defer s.runner.mu.RUnlock()
	out := make([]*ManagedTest, 0)
	for _, test := range s.runner.tests {
		if test == nil || (test.Status != "RUNNING" && test.Status != "INITIALIZING") {
			continue
		}
		if test.remoteGroupShards == nil {
			continue
		}
		for _, shards := range test.remoteGroupShards {
			if shardVUsersForTarget(shards, target) > 0 {
				out = append(out, test)
				break
			}
		}
	}
	return out
}
