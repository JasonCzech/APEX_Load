package api

import (
	"errors"
	"slices"
	"strings"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Runtime workers consume the frozen job/preparation selections whenever those
// exist. Draft source edits only affect a future launch.
func (tr *TestRunner) runMonitorCorrelationOrigins(id string) []string {
	store, ok := tr.testStore.(storage.RunMonitorStore)
	if !ok {
		return nil
	}
	record, err := tr.testStore.GetTest(id)
	if err != nil || record == nil {
		return nil
	}
	enabled := runMonitoringEnabled(record.ProjectID)
	sources := []storage.MonitorSource{}
	jobs, err := store.ListRunMonitorJobs(record.ProjectID, id)
	if err == nil && len(jobs) > 0 {
		for _, job := range jobs {
			sources = append(sources, job.Source)
		}
	} else {
		hasFrozenPreparation := false
		if preparations, ok := tr.testStore.(storage.PreparationStore); ok {
			p, err := preparations.LatestPreparation(id)
			if err == nil && p != nil && p.Monitors != nil && (p.State == "ready" || p.State == "consumed") {
				hasFrozenPreparation = true
				for _, source := range p.Monitors.Sources {
					sources = append(sources, source)
				}
			}
		}
		if !hasFrozenPreparation {
			if !enabled {
				return nil
			}
			bindings, err := tr.resolvedTestMonitorBindings(record)
			if err != nil {
				return nil
			}
			all, err := store.ListMonitorSources(record.ProjectID)
			if err != nil {
				return nil
			}
			for _, binding := range bindings.Sources {
				for _, source := range all {
					if source.ID == binding.SourceID {
						sources = append(sources, source)
					}
				}
			}
		}
	}
	out := []string{}
	for _, source := range sources {
		if !source.Enabled || !enabled && source.Pack != "elastic" {
			continue
		}
		origins := []string{}

		if source.Pack == monitorprovider.PackDynatrace && source.Dynatrace != nil && source.Dynatrace.RunHeader {
			origins = source.Dynatrace.HeaderOrigins
		}
		if source.Pack == "elastic" && source.Elastic != nil && source.Elastic.RunHeader {
			origins = source.Elastic.HeaderOrigins
		}
		for _, origin := range origins {
			origin = strings.TrimSuffix(origin, "/")
			if !slices.Contains(out, origin) {
				out = append(out, origin)
			}
		}
	}
	slices.Sort(out)
	return out
}
func (tr *TestRunner) validateRunMonitorCorrelation(id string) error {
	correlation := tr.runCorrelationAssignment(id)
	if len(correlation.Origins) == 0 {
		return nil
	}
	if len(correlation.Origins) > 32 {
		return errors.New("run correlation supports at most 32 approved origins")
	}
	// Only the run marker can conflict with an authored X-Apex-Test-Run; a run
	// that carries trace context alone leaves that header to the script.
	if !correlation.carriesRunMarker() {
		return nil
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	test := tr.tests[id]
	if test == nil {
		return errors.New("test not found")
	}
	for _, group := range test.groupSpecs {
		if group.Script == nil {
			continue
		}
		switch group.Script.Protocol {
		case "http", "http/1.1", "http/2", "soap":
		default:
			continue
		}
		if err := engine.ValidateRunCorrelationHeaders(group.Script, id); err != nil {
			return err
		}
	}
	return nil
}
