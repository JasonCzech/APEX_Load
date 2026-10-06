package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/elastic"
)

func TestElasticDeploymentPolicyCapsSavedBudgets(t *testing.T) {
	for _, name := range []string{"APEXLOAD_ELASTIC_MAX_REQUESTS_PER_MINUTE", "APEXLOAD_ELASTIC_MAX_REQUESTS_PER_POLL", "APEXLOAD_ELASTIC_MAX_POLL_SECONDS"} {
		t.Setenv(name, "")
	}
	config := elastic.Config{RequestsPerMinute: 600, MaxRequestsPerPoll: 100, MaxPollSeconds: 60}
	policy := elasticPolicyConfig(config)
	if policy.RequestsPerMinute != 60 || policy.MaxRequestsPerPoll != 20 || policy.MaxPollSeconds != 30 {
		t.Fatalf("default ceilings %+v", policy)
	}
	lower := elastic.Config{RequestsPerMinute: 10, MaxRequestsPerPoll: 3, MaxPollSeconds: 5}
	if got := elasticPolicyConfig(lower); got.RequestsPerMinute != 10 || got.MaxRequestsPerPoll != 3 || got.MaxPollSeconds != 5 {
		t.Fatalf("lower budgets %+v", got)
	}
	t.Setenv("APEXLOAD_ELASTIC_MAX_REQUESTS_PER_MINUTE", "120")
	t.Setenv("APEXLOAD_ELASTIC_MAX_REQUESTS_PER_POLL", "40")
	t.Setenv("APEXLOAD_ELASTIC_MAX_POLL_SECONDS", "45")
	policy = elasticPolicyConfig(config)
	if policy.RequestsPerMinute != 120 || policy.MaxRequestsPerPoll != 40 || policy.MaxPollSeconds != 45 {
		t.Fatalf("explicit ceilings %+v", policy)
	}
	t.Setenv("APEXLOAD_ELASTIC_MAX_REQUESTS_PER_MINUTE", "999999")
	t.Setenv("APEXLOAD_ELASTIC_MAX_REQUESTS_PER_POLL", "malformed")
	t.Setenv("APEXLOAD_ELASTIC_MAX_POLL_SECONDS", "-1")
	policy = elasticPolicyConfig(config)
	if policy.RequestsPerMinute != 60 || policy.MaxRequestsPerPoll != 20 || policy.MaxPollSeconds != 30 {
		t.Fatalf("invalid deployment ceilings %+v", policy)
	}
}
