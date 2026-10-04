package api

import (
	"errors"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) translateLegacyDynatraceBindings(project, scenario string, requested *[]storage.MonitorBinding, connection string) ([]storage.MonitorBinding, error) {
	if !runMonitoringEnabled(project) {
		return nil, errors.New("native Dynatrace collection requires the project run-monitoring gate")
	}
	// Legacy capture never required verification; native collection does.
	if tr.dynatraceStore == nil {
		return nil, errors.New("Dynatrace connections are unavailable")
	}
	if c, err := tr.dynatraceStore.GetDynatraceConnection(connection); err != nil || c == nil || c.ProjectID != project || c.Readiness() != "ready" {
		return nil, errors.New("this project uses native Dynatrace monitoring: verify the saved connection with Test saved connection before selecting it for a test")
	}
	store, ok := tr.testStore.(interface {
		storage.RunMonitorStore
		EnsureLegacyDynatraceMonitorSource(string, string) (*storage.MonitorSource, error)
	})
	if !ok {
		return nil, errors.New("native Dynatrace migration storage unavailable")
	}
	bindings := []storage.MonitorBinding{}
	if requested != nil {
		if len(*requested) == 0 {
			return nil, errors.New("legacy Dynatrace capture conflicts with explicitly disabled monitor bindings")
		}
		bindings = append(bindings, (*requested)...)
	} else if scenario != "" {
		inherited, err := store.GetMonitorBindings(project, scenario)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, inherited.Sources...)
	}
	sources, err := store.ListMonitorSources(project)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		for _, source := range sources {
			if source.ID == binding.SourceID && source.Pack == "dynatrace" {
				return nil, errors.New("legacy Dynatrace capture cannot be combined with native Dynatrace bindings")
			}
		}
	}
	var source *storage.MonitorSource
	err = tr.trackDurableWrite(func() error {
		var writeErr error
		source, writeErr = store.EnsureLegacyDynatraceMonitorSource(project, connection)
		return writeErr
	})
	if err != nil {
		return nil, errors.New("Dynatrace legacy selection requires administrator review before native collection")
	}
	if len(bindings) >= storage.MaxRunMonitorSources {
		return nil, errors.New("at most 32 monitoring sources may be selected")
	}
	return append(bindings, storage.MonitorBinding{SourceID: source.ID}), nil
}
