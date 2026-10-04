package api

import (
	"errors"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
	"slices"
	"strings"
)

// Runtime workers consume the frozen job/preparation selections whenever those
// exist. Draft source edits only affect a future launch.
func (tr *TestRunner) runMonitorCorrelationOrigins(id string) []string {
	store, ok := tr.testStore.(storage.RunMonitorStore)
	if !ok {
		return nil
	}
	record, err := tr.testStore.GetTest(id)
	if err != nil || record == nil || !runMonitoringEnabled(record.ProjectID) {
		return nil
	}
	sources := []storage.MonitorSource{}
	jobs, err := store.ListRunMonitorJobs(record.ProjectID, id)
	if err == nil && len(jobs) > 0 {
		for _, job := range jobs {
			sources = append(sources, job.Source)
		}
	} else {
		if preparations, ok := tr.testStore.(storage.PreparationStore); ok {
			p, err := preparations.LatestPreparation(id)
			if err == nil && p.Monitors != nil && (p.State == "ready" || p.State == "consumed") {
				for _, source := range p.Monitors.Sources {
					sources = append(sources, source)
				}
			}
		}
		if len(sources) == 0 {
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
		if !source.Enabled {
			continue
		}
		origins := []string{}
		if source.Pack == "appdynamics" && source.AppDynamics != nil && source.AppDynamics.RunHeader {
			origins = source.AppDynamics.HeaderOrigins
		}
		if source.Pack == "dynatrace" && source.Dynatrace != nil && source.Dynatrace.RunHeader {
			origins = source.Dynatrace.HeaderOrigins
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
	origins := tr.runMonitorCorrelationOrigins(id)
	if len(origins) == 0 {
		return nil
	}
	if len(origins) > 32 {
		return errors.New("run correlation supports at most 32 approved origins")
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	test := tr.tests[id]
	if test == nil {
		return errors.New("test not found")
	}
	for _, group := range test.groupSpecs {
		if group.Script == nil || (group.Script.Protocol != "http" && group.Script.Protocol != "soap") {
			continue
		}
		if err := engine.ValidateRunCorrelationHeaders(group.Script, id); err != nil {
			return err
		}
	}
	return nil
}
