package api

import (
	"context"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (tr *TestRunner) validateRequiredElastic(ctx context.Context, project string, source storage.MonitorSource) error {
	connections, ok := tr.testStore.(storage.ELKConnectionStore)
	if !ok {
		return errors.New("Elastic connection storage unavailable")
	}
	c, err := connections.GetELKConnection(source.ConnectionID)
	if err != nil || c.ProjectID != project || c.ConfigurationStatus() != "ready" {
		return errors.New("required Elastic connection is not verified")
	}
	if err = storage.ValidateExpandedElasticSourceConnection(source, c.Snapshot()); err != nil {
		return err
	}
	if source.ExecutorMode != "direct" {
		store, ok := tr.testStore.(storage.ElasticInspectionStore)
		if !ok {
			return errors.New("Elastic collector enrollment unavailable")
		}
		var key string
		err := tr.trackDurableWrite(func() error {
			var lookupErr error
			key, lookupErr = store.ElasticCollectorPublicKey(project, source.CollectorID, source.Destination, time.Now().UTC())
			return lookupErr
		})
		if err != nil || key != source.CollectorPublicKey {
			return errors.New("Elastic collector enrollment changed; update source")
		}
	}
	if source.Elastic == nil {
		return errors.New("required Elastic selection unavailable")
	}
	if err = validatePreparedElasticCapabilities(*source.Elastic, c.Capabilities); err != nil {
		return err
	}
	if _, err = resolveElasticCredentials(ctx, tr.secretResolverForProject(project), c.Snapshot()); err != nil {
		return err
	}
	if tr.elasticPrepare != nil {
		verifyCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		return tr.elasticPrepare(verifyCtx, source, c.Snapshot())
	}
	return nil
}

func (tr *TestRunner) validateLegacyElasticConflict(project, scenario, connection string, bindings *storage.MonitorBindings) error {
	if connection == "" {
		return nil
	}
	store, ok := tr.testStore.(storage.RunMonitorStore)
	if !ok {
		return nil
	}
	if bindings == nil {
		if scenario == "" {
			return nil
		}
		var err error
		bindings, err = store.GetMonitorBindings(project, scenario)
		if err != nil {
			return err
		}
	}
	sources, err := store.ListMonitorSources(project)
	if err != nil {
		return err
	}
	for _, b := range bindings.Sources {
		for _, source := range sources {
			if source.ID == b.SourceID && (source.Pack == monitorprovider.PackElastic || source.Pack == monitorprovider.PackElasticQuery) && source.ConnectionID == connection {
				return errors.New("select legacy Elastic capture or canonical monitoring for this connection, not both")
			}
		}
	}
	return nil
}
