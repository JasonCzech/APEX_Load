package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/storage"
)

// verifyPreparedAppDynamics executes a fresh, scope-limited read from the
// connection's assigned location. The preparation worker holds no mutation
// locks while waiting for a collector's signed administrative operation.
func (s *Server) verifyPreparedAppDynamics(parent context.Context, source storage.MonitorSource, snapshot *storage.AppDynamicsConnectionSnapshot) error {
	if snapshot == nil || source.AppDynamics == nil || source.AppDynamics.Validate() != nil {
		return errors.New("AppDynamics preparation selection unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	selection := *source.AppDynamics
	selection.QueryOffset = 0
	selection.CollectorHeader, selection.CollectorValue = "", ""
	selection.RunHeader, selection.HeaderOrigins = false, nil
	var capabilities appdynamics.Capabilities
	if source.ExecutorMode == "direct" {
		credentials, err := resolveAppDynamicsCredentials(ctx, s.runner.secretResolverForProject(source.ProjectID), snapshot)
		if err != nil {
			return errors.New("AppDynamics preparation credential unavailable")
		}
		cfg, err := s.appDynamicsQuotaConfig(ctx, snapshot, credentials, "direct:"+s.controllerID)
		if err != nil {
			return errors.New("AppDynamics preparation executor temporarily unavailable")
		}
		client, err := appdynamics.NewClient(cfg, credentials)
		if err != nil {
			return errors.New("AppDynamics preparation transport unavailable")
		}
		capabilities, err = client.TestSelection(ctx, selection)
		if err != nil {
			return errors.New("AppDynamics preparation connectivity verification failed")
		}
	} else {
		store, ok := s.store.(storage.AppDynamicsInspectionStore)
		if !ok {
			return errors.New("AppDynamics preparation operation storage unavailable")
		}
		key, err := store.AppDynamicsCollectorPublicKey(source.ProjectID, source.CollectorID, snapshot.Config.Endpoint, time.Now().UTC())
		if err != nil {
			return errors.New("AppDynamics preparation assigned collector unavailable")
		}
		if _, err = monitorSigningKey(); err != nil {
			return errors.New("AppDynamics preparation signing key unavailable")
		}
		body, err := json.Marshal(appDynamicsInspectionWork{Connection: snapshot, Selection: &selection, CollectorPublicKey: key})
		if err != nil {
			return errors.New("AppDynamics preparation selection exceeds limits")
		}
		now := time.Now().UTC()
		expires := now.Add(time.Minute)
		if deadline, ok := ctx.Deadline(); ok && deadline.Before(expires) {
			expires = deadline
		}
		op := &storage.AppDynamicsInspection{ID: "appd-inspection-" + uuid.NewString(), ProjectID: source.ProjectID, ConnectionID: source.ConnectionID, ConnectionRevision: snapshot.Revision, CollectorID: source.CollectorID, Kind: "test", PayloadJSON: string(body), CreatedAt: now, ExpiresAt: expires}
		if err = store.CreateAppDynamicsInspection(op); err != nil {
			return errors.New("AppDynamics preparation operation could not be queued")
		}
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return errors.New("AppDynamics preparation connectivity verification timed out")
			case <-ticker.C:
				current, err := store.GetAppDynamicsInspection(op.ID)
				if err != nil {
					return errors.New("AppDynamics preparation operation unavailable")
				}
				if current.State == "error" || !current.ExpiresAt.After(time.Now().UTC()) {
					return errors.New("AppDynamics preparation connectivity verification failed")
				}
				if current.State != "complete" {
					continue
				}
				var result struct {
					Capabilities appdynamics.Capabilities `json:"capabilities"`
				}
				if json.Unmarshal([]byte(current.ResultJSON), &result) != nil {
					return errors.New("AppDynamics preparation result unavailable")
				}
				capabilities = result.Capabilities
				return validatePreparedAppDynamicsCapabilities(selection, capabilities)
			}
		}
	}
	return validatePreparedAppDynamicsCapabilities(selection, capabilities)
}

func validatePreparedAppDynamicsCapabilities(selection appdynamics.Selection, caps appdynamics.Capabilities) error {
	if len(selection.Metrics) > 0 && !caps.Metrics || selection.Events && !caps.Events || selection.Violations && !caps.Violations || selection.Snapshots && !caps.Snapshots || len(selection.Analytics) > 0 && !caps.Analytics {
		return errors.New("AppDynamics preparation selected capability unavailable")
	}
	return nil
}
