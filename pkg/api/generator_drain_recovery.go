package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Refresh before run reconciliation/admission: a standby's startup snapshot can
// predate operator changes by hours. Never interpret a failed read as all-active.
func (s *Server) reloadGeneratorDrainStatesOnLeadershipAcquire() error {
	if s == nil || s.store == nil || s.runner == nil {
		return nil
	}
	backend, ok := s.runner.getExecutionBackend().(persistentDrainBackend)
	if !ok {
		return nil
	}
	ctx, cancel := s.leadership.operationContext(context.Background())
	defer cancel()
	states, err := readGeneratorDrainStates(ctx, s.store)
	if err != nil {
		return err
	}
	// Offline cordons are stored in metadata even when no target is connected.
	metadata, err := s.store.ListGeneratorMetadata(0, 0)
	if err != nil {
		return fmt.Errorf("load generator cordon metadata: %w", err)
	}
	for _, m := range metadata {
		if m == nil || strings.TrimSpace(m.GeneratorID) == "" {
			return fmt.Errorf("invalid generator cordon metadata")
		}
		id := m.GeneratorID
		state := states[id]
		if identities, ok := backend.(identityDrainableExecutionBackend); ok {
			target := identities.GeneratorTargetForID(id)
			if target != "" && target != id {
				if state == "" {
					state = states[target]
				}
				delete(states, target)
			}
		}
		switch {
		case state == "draining":
			states[id] = state // explicit operator drain wins
		case m.Cordoned:
			states[id] = "cordoned"
		default:
			delete(states, id)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Callback execution is serialized across leadership epochs; a lost epoch
	// cannot admit work, and its successor must complete this refresh again.
	if !s.leadership.IsLeader() {
		return fmt.Errorf("execution leadership lost during drain-state refresh")
	}
	backend.ApplyGeneratorDrainStates(states)
	return nil
}

func readGeneratorDrainStates(ctx context.Context, store settingsPersistence) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var raw string
	var err error
	if contextual, ok := store.(contextualSettingsPersistence); ok {
		raw, err = contextual.GetSettingContext(ctx, generatorDrainStatesStoreKey)
	} else {
		raw, err = store.GetSetting(generatorDrainStatesStoreKey)
	}
	if err != nil {
		return nil, fmt.Errorf("load generator drain states: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var decoded map[string]string
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return nil, fmt.Errorf("invalid stored generator drain states")
		}
	}
	states := make(map[string]string, len(decoded))
	for key, value := range decoded {
		// Persisted writers emit canonical identities and states. Refuse ambiguous
		// or unknown values rather than silently dropping an exclusion.
		if key == "" || strings.TrimSpace(key) != key {
			return nil, fmt.Errorf("invalid stored generator drain identity")
		}
		switch value {
		case "cordoned", "draining":
			states[key] = value
		case "active":
		default:
			return nil, fmt.Errorf("invalid stored generator drain state")
		}
	}
	return states, nil
}
