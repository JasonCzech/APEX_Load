package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

func validatePreparedDynatraceCapabilities(selection dynatrace.Selection, caps dynatrace.Capabilities) error {
	if len(selection.Metrics) > 0 && !caps.Metrics || selection.Problems && !caps.Problems || selection.Events && !caps.Events || selection.TopologyMode == "classic" && !caps.ClassicTopology || selection.TopologyMode == "smartscape" && !caps.Smartscape {
		return errors.New("Dynatrace selected capability unavailable")
	}
	for _, metric := range selection.Metrics {
		if status := caps.Status["metrics:"+metric.Name]; status != "" && status != "available" && status != "available_no_data" {
			return errors.New("Dynatrace selected metric unavailable")
		}
	}
	for _, query := range selection.Grail {
		if status := caps.Status["grail:"+query.Name]; status != "available" && status != "available_no_data" {
			return errors.New("Dynatrace selected Grail query unavailable")
		}
		switch query.Kind {
		case "metric", "aggregate":
			if !caps.GrailMetrics {
				return errors.New("Dynatrace Grail metrics unavailable")
			}
		case "log":
			if !caps.Logs {
				return errors.New("Dynatrace Grail logs unavailable")
			}
		case "trace", "span":
			if !caps.Spans {
				return errors.New("Dynatrace Grail spans unavailable")
			}
		case "event":
			if !caps.GrailEvents {
				return errors.New("Dynatrace Grail events unavailable")
			}
		}
	}
	return nil
}
func (s *Server) verifyPreparedDynatrace(parent context.Context, source storage.MonitorSource, snapshot *storage.DynatraceConnectionSnapshot) error {
	if snapshot == nil || source.Dynatrace == nil || source.Dynatrace.Validate() != nil {
		return errors.New("Dynatrace preparation selection unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	selection := *source.Dynatrace
	selection.Cursor = ""
	selection.QueryOffset = 0
	selection.RunHeader = false
	selection.HeaderOrigins = nil
	var caps dynatrace.Capabilities
	if source.ExecutorMode == "direct" {
		client, err := s.nativeDynatraceClient(ctx, snapshot, "direct:"+s.controllerID, &selection)
		if err != nil {
			return err
		}
		caps, err = client.TestSelection(ctx, selection)
		if err != nil {
			return errors.New("Dynatrace preparation verification failed")
		}
	} else {
		store, ok := s.store.(storage.DynatraceOperationStore)
		if !ok {
			return errors.New("Dynatrace operation storage unavailable")
		}
		key, err := store.DynatraceCollectorPublicKey(source.ProjectID, source.CollectorID, storage.DynatraceDestination(snapshot), time.Now().UTC())
		if err != nil || key != source.CollectorPublicKey {
			return errors.New("Dynatrace collector enrollment changed")
		}
		if _, err = monitorSigningKey(); err != nil {
			return err
		}
		body, _ := json.Marshal(dynatraceOperationWork{Connection: snapshot, Selection: &selection, CollectorPublicKey: key})
		now := time.Now().UTC()
		op := &storage.DynatraceOperation{ID: "dynatrace-operation-" + uuid.NewString(), ProjectID: source.ProjectID, ConnectionID: source.ConnectionID, ConnectionRevision: snapshot.Revision, CollectorID: source.CollectorID, Kind: "verify", PayloadJSON: string(body), CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
		if err = store.CreateDynatraceOperation(op); err != nil {
			return errors.New("Dynatrace verification could not be queued")
		}
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return errors.New("Dynatrace collector verification timed out")
			case <-ticker.C:
				current, err := store.GetDynatraceOperation(op.ID)
				if err != nil {
					return err
				}
				if current.State == "error" || !current.ExpiresAt.After(time.Now()) {
					return errors.New("Dynatrace collector verification failed")
				}
				if current.State != "complete" {
					continue
				}
				var result dynatraceOperationResult
				if json.Unmarshal([]byte(current.ResultJSON), &result) != nil {
					return errors.New("Dynatrace verification result unavailable")
				}
				return s.finishPreparedDynatraceVerification(snapshot, selection, result.Capabilities)
			}
		}
	}
	return s.finishPreparedDynatraceVerification(snapshot, selection, caps)
}

func (s *Server) finishPreparedDynatraceVerification(snapshot *storage.DynatraceConnectionSnapshot, selection dynatrace.Selection, caps dynatrace.Capabilities) error {
	caps = sanitizeDynatraceCapabilitiesForConfig(caps, snapshot.Config)
	if err := validatePreparedDynatraceCapabilities(selection, caps); err != nil {
		return err
	}
	connections, ok := s.store.(storage.DynatraceConnectionStore)
	if !ok {
		return errors.New("Dynatrace connection storage unavailable")
	}
	current, err := connections.GetDynatraceConnection(snapshot.ConnectionID)
	if err != nil || current.ProjectID != snapshot.ProjectID || current.Revision != snapshot.Revision {
		return errors.New("Dynatrace configuration changed during verification")
	}
	if current.Readiness() == "ready" {
		return nil
	}
	if !dynatraceWantedCapabilitiesAvailable(dynatraceWantedCapabilities(current), caps) {
		return errors.New("verify all selected connection capabilities before preparing")
	}
	health, ok := s.store.(interface {
		UpdateDynatraceConnectionHealthIfRevision(string, string, int64, string, string, dynatrace.Capabilities, time.Time) (bool, error)
	})
	if !ok {
		return errors.New("Dynatrace verified health storage unavailable")
	}
	updated, err := health.UpdateDynatraceConnectionHealthIfRevision(snapshot.ConnectionID, snapshot.ProjectID, snapshot.Revision, "connected", "selected capabilities verified", caps, time.Now().UTC())
	if err != nil || !updated {
		return errors.New("Dynatrace configuration changed during verification")
	}
	return nil
}
func (tr *TestRunner) validateDynatraceReadiness(ctx context.Context, project string, bindings *storage.MonitorBindings, sources []storage.MonitorSource) error {
	connections, ok := tr.testStore.(storage.DynatraceConnectionStore)
	if !ok {
		return nil
	}
	if err := validateDynatraceCollectionBudgets(ctx, connections, tr.secretResolverForProject(project), project, bindings, sources); err != nil {
		return err
	}
	for _, binding := range bindings.Sources {
		for _, source := range sources {
			if source.ID != binding.SourceID || source.Pack != monitorprovider.PackDynatrace {
				continue
			}
			verify := func() error {
				connection, err := connections.GetDynatraceConnection(source.ConnectionID)
				if err != nil || connection.ProjectID != project || !connection.Enabled || !source.Enabled {
					return errors.New("Dynatrace selected connection is unavailable")
				}
				if source.Dynatrace == nil || source.Dynatrace.Validate() != nil {
					return errors.New("Dynatrace selected source is invalid")
				}
				if err = storage.ValidateDynatraceSourceConnection(source, connection.Snapshot()); err != nil {
					return err
				}
				cfg := connection.RuntimeConfig()
				settle := source.Dynatrace.SettleSeconds
				if settle == 0 {
					settle = 120
				}
				queries := len(source.Dynatrace.Metrics) + len(source.Dynatrace.Grail) + 2
				if source.Dynatrace.Problems {
					queries++
				}
				if source.Dynatrace.Events {
					queries++
				}
				if binding.Required && settle+2*max(60, source.IntervalSeconds)+30 > 300 {
					return fmt.Errorf("required Dynatrace selection exceeds five-minute reconciliation budget")
				}
				if binding.Required && queries > cfg.MaxRequestsPerPoll {
					return errors.New("required Dynatrace selection exceeds poll request budget")
				}
				if _, err = resolveNativeDynatraceCredentials(ctx, tr.secretResolverForProject(project), connection.Snapshot(), source.Dynatrace); err != nil {
					return err
				}
				if tr.dynatracePrepare == nil {
					return errors.New("Dynatrace verification executor unavailable")
				}
				if err = tr.dynatracePrepare(ctx, source, connection.Snapshot()); err != nil {
					return err
				}
				return nil
			}
			if err := verify(); err != nil && binding.Required {
				return err
			}
		}
	}
	return nil
}
