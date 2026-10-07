package api

import (
	"context"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Provider hooks share the leader scheduler, worker exclusion and durable
// ingestion lifecycle. Each adapter retains its own upstream validation.
type monitorProviderHooks struct {
	Collect            func(*Server, context.Context, storage.RunMonitorJob)
	QueryWindow        func(storage.RunMonitorJob, time.Time) (time.Time, time.Time)
	MinimumPollSeconds int
}

var monitorProviders = map[string]monitorProviderHooks{
	"appdynamics": {Collect: (*Server).collectDirectAppDynamics, QueryWindow: appDynamicsQueryWindow, MinimumPollSeconds: 60},
	"elastic":     {Collect: (*Server).collectDirectElastic, QueryWindow: elasticQueryWindow, MinimumPollSeconds: 10},
}
