package api

import (
	"context"
	"errors"

	"github.com/jasonczech/apexload/pkg/dynatrace"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

func validateDynatraceCollectionBudgets(ctx context.Context, connections storage.DynatraceConnectionStore, resolver engine.SecretResolver, project string, bindings *storage.MonitorBindings, sources []storage.MonitorSource) error {
	if bindings == nil {
		return nil
	}
	type budget struct {
		requests, rpm, interval, settle int
		required                        bool
	}
	groups := map[string]*budget{}
	for _, binding := range bindings.Sources {
		for _, source := range sources {
			if source.ID != binding.SourceID || source.Pack != monitorprovider.PackDynatrace || !source.Enabled || source.Dynatrace == nil {
				continue
			}
			c, err := connections.GetDynatraceConnection(source.ConnectionID)
			if err != nil || c == nil || c.ProjectID != project {
				if binding.Required {
					return errors.New("required Dynatrace connection unavailable")
				}
				continue
			}
			cfg := c.RuntimeConfig()
			credentials, err := resolveNativeDynatraceCredentials(ctx, resolver, c.Snapshot(), source.Dynatrace)
			if err != nil {
				if binding.Required {
					return errors.New("required Dynatrace credential unavailable")
				}
				continue
			}
			environment := 2 * len(source.Dynatrace.Metrics) // Descriptor and data requests.
			if source.Dynatrace.Problems {
				environment++
			}
			if source.Dynatrace.Events {
				environment++
			}
			if source.Dynatrace.TopologyMode == "classic" {
				environment++
			}
			platform := 3 * len(source.Dynatrace.Grail) // Execute, polling and cancellation admission.
			if source.Dynatrace.TopologyMode == "smartscape" {
				platform += 6
			}
			for _, api := range []struct {
				key           string
				requests, rpm int
			}{
				{dynatrace.RequestQuotaIdentity(cfg, credentials, false), environment, cfg.RequestsPerMinute},
				{dynatrace.RequestQuotaIdentity(cfg, credentials, true), platform, cfg.PlatformRequestsPerMinute},
			} {
				if api.requests == 0 {
					continue
				}
				b := groups[api.key]
				if b == nil {
					b = &budget{rpm: max(1, api.rpm/2)}
					groups[api.key] = b
				}
				b.rpm = min(b.rpm, max(1, api.rpm/2))
				b.requests += api.requests
				b.required = b.required || binding.Required
				if binding.Required {
					b.interval = max(b.interval, max(60, source.IntervalSeconds))
					settle := source.Dynatrace.SettleSeconds
					if settle == 0 {
						settle = 120
					}
					b.settle = max(b.settle, settle)
				}
			}
		}
	}
	for _, b := range groups {
		seconds := (60*b.requests + b.rpm - 1) / b.rpm
		if b.required && b.settle+2*max(b.interval, seconds)+30 > 300 {
			return errors.New("required Dynatrace selections exceed the shared five-minute reconciliation budget; reduce queries or increase approved request capacity")
		}
	}
	return nil
}
