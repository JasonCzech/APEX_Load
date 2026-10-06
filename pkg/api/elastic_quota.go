package api

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/elastic"
)

// Deployment policy owns upper budgets. Saved connections can lower them.
func elasticPolicyConfig(config elastic.Config) elastic.Config {
	config = config.Defaults()
	ceiling := func(name string, fallback, hardMax int) int {
		v, err := strconv.Atoi(os.Getenv(name))
		if err != nil || v < 1 || v > hardMax {
			return fallback
		}
		return v
	}
	config.RequestsPerMinute = min(config.RequestsPerMinute, ceiling("APEXLOAD_ELASTIC_MAX_REQUESTS_PER_MINUTE", 60, 600))
	config.MaxRequestsPerPoll = min(config.MaxRequestsPerPoll, ceiling("APEXLOAD_ELASTIC_MAX_REQUESTS_PER_POLL", 20, 100))
	config.MaxPollSeconds = min(config.MaxPollSeconds, ceiling("APEXLOAD_ELASTIC_MAX_POLL_SECONDS", 30, 60))
	return config
}

func (s *Server) elasticQuotaConfig(ctx context.Context, config elastic.Config, executor string) (elastic.Config, error) {
	config = elasticPolicyConfig(config)
	if err := ctx.Err(); err != nil {
		return config, err
	}
	store, ok := s.store.(interface {
		AllocateElasticQuota(string, string, time.Time) (bool, error)
	})
	if !ok {
		return config, errors.New("Elastic quota storage unavailable")
	}
	allowed, err := store.AllocateElasticQuota(elastic.QuotaKey(config), executor, time.Now().UTC())
	if err != nil || !allowed {
		return config, errors.New("Elastic executor quota is busy")
	}
	// Two durable executor leases share the endpoint budget across controllers
	// and collectors. Each process additionally shares request-rate state.
	config.RequestsPerMinute = max(1, config.RequestsPerMinute/2)
	return config, nil
}
