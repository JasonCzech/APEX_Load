package api

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/storage"
	"time"
)

func (s *Server) appDynamicsQuotaConfig(ctx context.Context, snapshot *storage.AppDynamicsConnectionSnapshot, credentials appdynamics.Credentials, executor string) (appdynamics.Config, error) {
	config := snapshot.Config.Defaults()
	if err := ctx.Err(); err != nil {
		return config, err
	}
	store, ok := s.store.(interface {
		AllocateAppDynamicsQuota(string, string, time.Time) (bool, error)
	})
	if !ok {
		return config, errors.New("AppDynamics request quota storage unavailable")
	}
	allowed, err := store.AllocateAppDynamicsQuota(appdynamics.ControllerQuotaKey(config), executor, time.Now().UTC())
	if err != nil || !allowed {
		return config, errors.New("AppDynamics executor request quota is busy")
	}
	if config.EventsEndpoint != "" && credentials.EventsAPIKey != "" {
		allowed, err = store.AllocateAppDynamicsQuota(appdynamics.AnalyticsQuotaKey(config, credentials), executor, time.Now().UTC())
		if err != nil || !allowed {
			return config, errors.New("AppDynamics Analytics request quota is busy")
		}
	}
	config.RequestsPerMinute = max(1, config.RequestsPerMinute/2)
	config.AnalyticsRequestsPerMinute = max(1, config.AnalyticsRequestsPerMinute/2)
	return config, nil
}
