package api

import (
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/monitorprovider"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

const retiredAppDynamicsMessage = "AppDynamics monitoring is retired; remove its monitoring bindings and criteria before preparing or launching this test"

func validateRetiredMonitorRules(rules []analysis.MonitorRule) error {
	for _, rule := range rules {
		if rule.Provider == monitorprovider.ProviderAppDynamics {
			return errors.New(retiredAppDynamicsMessage)
		}
	}
	return nil
}

func validateRetiredMonitorBindings(bindings *storage.MonitorBindings, sources []storage.MonitorSource) error {
	if bindings == nil {
		return nil
	}
	byID := make(map[string]storage.MonitorSource, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}
	for _, binding := range bindings.Sources {
		source, exists := byID[binding.SourceID]
		if !exists {
			return fmt.Errorf("monitor source unavailable (%s); remove its binding before preparing or launching this test", binding.SourceID)
		}
		if source.Pack == monitorprovider.PackAppDynamics || source.AppDynamics != nil {
			return fmt.Errorf("%s (source %s)", retiredAppDynamicsMessage, source.ID)
		}
	}
	return nil
}

// Read the same explicit/inherited selection as ordinary execution admission.
// This check is intentionally independent of provider enablement and requiredness.
func (tr *TestRunner) validateRetiredMonitoring(id, preparationID string) error {
	tr.mu.RLock()
	test := tr.tests[id]
	if test == nil {
		tr.mu.RUnlock()
		return nil
	}
	if test.GoalConfig != nil {
		if err := validateRetiredMonitorRules(test.GoalConfig.MonitorRules); err != nil {
			tr.mu.RUnlock()
			return err
		}
	}
	record := &storage.TestRecord{ID: id, ProjectID: test.ProjectID, ScenarioID: test.ScenarioID, MonitorBindings: cloneMonitorBindings(test.MonitorBindings)}
	if preparationID == "" {
		preparationID = test.preparationID
	}
	tr.mu.RUnlock()
	return tr.validateRetiredMonitoringRecord(record, preparationID)
}

func (tr *TestRunner) validateRetiredMonitoringRecord(record *storage.TestRecord, preparationID string) error {
	if record == nil {
		return nil
	}
	bindings, err := tr.resolvedTestMonitorBindings(record)
	if err != nil {
		return err
	}
	if store, ok := tr.testStore.(storage.RunMonitorStore); ok && bindings != nil && len(bindings.Sources) > 0 {
		sources, err := store.ListMonitorSources(record.ProjectID)
		if err != nil {
			return err
		}
		if err := validateRetiredMonitorBindings(bindings, sources); err != nil {
			return err
		}
	}
	// Frozen preparations retain the authority even after draft sources are edited.
	if preparationID != "" {
		if store, ok := tr.testStore.(storage.PreparationStore); ok {
			p, err := store.GetPreparation(preparationID)
			if err != nil {
				return err
			}
			if p != nil && p.TestID == record.ID && p.ProjectID == record.ProjectID && p.Monitors != nil {
				if err := validateRetiredMonitorRules(p.Monitors.MonitorRules); err != nil {
					return err
				}
				for _, source := range p.Monitors.Sources {
					if source.Pack == monitorprovider.PackAppDynamics || source.AppDynamics != nil {
						return errors.New(retiredAppDynamicsMessage)
					}
				}
			}
		}
	}
	return nil
}

type retiredMonitoringStore interface {
	RetireAppDynamicsMonitoring(string, string, uint64) (bool, error)
}

func (tr *TestRunner) retireAppDynamicsMonitoring() error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	store, ok := tr.testStore.(retiredMonitoringStore)
	if !ok {
		return nil
	}
	term, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return err
	}
	name, owner, generation := "", "", uint64(0)
	if tr.durableExecutionFence {
		if !term.valid() {
			return errNotExecutionLeader
		}
		name, owner, generation = executionLeaseName, term.ownerID, term.generation
	}
	return tr.trackDurableWrite(func() error {
		held, err := store.RetireAppDynamicsMonitoring(name, owner, generation)
		if err != nil {
			return err
		}
		if !held {
			return errNotExecutionLeader
		}
		return nil
	})
}

// Retired execution fails before admission, credential resolution or capacity work.
func (tr *TestRunner) validateRetiredExecution(id, preparationID string) error {
	if err := tr.validateRetiredMonitoring(id, preparationID); err != nil {
		return err
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	if test := tr.tests[id]; test != nil {
		for _, group := range test.groupSpecs {
			if err := engine.RetiredBrunoRuntimeError(group.Script); err != nil {
				return err
			}
		}
	}
	return nil
}
