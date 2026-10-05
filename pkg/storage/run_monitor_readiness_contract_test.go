package storage

import (
	"strings"
	"testing"
	"time"
)

func TestCollectorReadinessContractsNeedTheirPack(t *testing.T) {
	now := time.Now().UTC()
	for _, contract := range []string{"appdynamics.v1", "dynatrace.v1", "prometheus-query.v1", "elastic-query.v1"} {
		err := validateCollectorRunReadiness("p", "c", []string{contract}, nil, now)
		if err == nil || !strings.Contains(err.Error(), "contract requires pack approval") {
			t.Fatalf("%s without its pack: %v", contract, err)
		}
	}
	if err := validateCollectorRunReadiness("p", "c", []string{"prometheus-query", "prometheus-query.v1"}, nil, now); err != nil {
		t.Fatalf("contract with its pack rejected: %v", err)
	}
}
