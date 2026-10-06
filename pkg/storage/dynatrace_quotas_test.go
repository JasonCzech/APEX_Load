package storage

import (
	"strings"
	"testing"
	"time"
)

func TestDynatraceQuotaAdmitsAndIsolatesProviderExecutors(t *testing.T) {
	s, _, now := monitorFixture(t)
	key := strings.Repeat("a", 64)
	for _, executor := range []string{"direct", "collector"} {
		if ok, err := s.AllocateDynatraceQuota(key, executor, now); err != nil || !ok {
			t.Fatalf("valid Dynatrace execution was rejected: %s %v %v", executor, ok, err)
		}
	}
	if ok, err := s.AllocateDynatraceQuota(key, "third", now); err != nil || ok {
		t.Fatalf("shared Dynatrace executor limit was not enforced: %v %v", ok, err)
	}
	if ok, err := s.AllocateDynatraceQuota(key, "direct", now.Add(30*time.Second)); err != nil || !ok {
		t.Fatalf("existing executor could not renew its lease: %v %v", ok, err)
	}
	if ok, err := s.AllocateMonitorQuota(key, "appdynamics-direct", now); err != nil || !ok {
		t.Fatalf("Dynatrace leases interfered with AppDynamics: %v %v", ok, err)
	}
	if ok, err := s.AllocateDynatraceQuota(key, "third", now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("expired collector lease was not reusable: %v %v", ok, err)
	}
	if ok, err := s.AllocateDynatraceQuota(key, "fourth", now.Add(time.Minute)); err != nil || ok {
		t.Fatalf("renewed executor lease was lost: %v %v", ok, err)
	}
	if ok, err := s.AllocateDynatraceQuota(strings.Repeat("b", 64), "fourth", now); err != nil || !ok {
		t.Fatalf("independent tenant was denied: %v %v", ok, err)
	}
	for _, invalid := range []string{"", "tenant-or-secret-value", "dynatrace:" + key, strings.Repeat("g", 64)} {
		if _, err := s.AllocateDynatraceQuota(invalid, "direct", now); err == nil {
			t.Fatalf("invalid raw identity was accepted: %q", invalid)
		}
	}
}
