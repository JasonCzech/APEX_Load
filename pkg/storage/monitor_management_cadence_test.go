package storage

import (
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Cadence is enforced before saving a new source revision. Historical snapshot
// decoding retains its prior contract and is not a route around source policy.
func TestManagementSourceProviderCadence(t *testing.T) {
	for _, provider := range []struct {
		pack    string
		kind    string
		entity  analysis.ManagementEntity
		minimum int
	}{
		{"kafka-offsets", analysis.ManagementKindKafka, analysis.ManagementEntity{Kafka: &analysis.KafkaManagementEntity{Topic: "orders", Partition: 0, Group: "workers"}}, 10},
		{"servicebus-runtime", analysis.ManagementKindServiceBus, analysis.ManagementEntity{ServiceBus: &analysis.ServiceBusManagementEntity{Queue: "orders"}}, 30},
		{"solace-semp", analysis.ManagementKindSolace, analysis.ManagementEntity{Solace: &analysis.SolaceManagementEntity{VPN: "load", Queue: "orders"}}, 30},
	} {
		t.Run(provider.pack, func(t *testing.T) {
			source := MonitorSource{ID: "source", ProjectID: "project", CollectorID: "collector", Name: "Managed evidence", Pack: provider.pack, Destination: "management://broker", Enabled: true,
				ManagementScope:    &analysis.ManagementScope{Version: analysis.ManagementScopeVersion, Kind: provider.kind, Provider: "approved-provider", Entities: []analysis.ManagementEntity{provider.entity}},
				ManagementIdentity: &analysis.ManagementIdentity{Mode: "anonymous", Principal: "fixture", PolicyRevision: "policy-v1"},
			}
			if provider.kind == analysis.ManagementKindKafka {
				source.ManagementScope.Isolation = "read_uncommitted"
			}
			for _, cadence := range []int{0, 9, provider.minimum - 1, provider.minimum, 3600, 3601} {
				source.IntervalSeconds = cadence
				err := source.Validate()
				wantValid := cadence >= provider.minimum && cadence <= 3600
				if (err == nil) != wantValid {
					t.Fatalf("cadence=%d valid=%v want=%v", cadence, err == nil, wantValid)
				}
			}
		})
	}
}

func TestManagementLegacyServiceBusCadenceRemainsReadableAndRevisioned(t *testing.T) {
	s, _, _ := monitorFixture(t)
	assertLegacyServiceBusCadence(t, s)
}

// A pre-reader v1 definition may retain its former 10-second cadence. Reads and
// deletion preserve that history, while a new revision must use current policy.
// This persistence fixture does not qualify anonymous provider authentication.
func assertLegacyServiceBusCadence(t *testing.T, s *SQLiteStore) {
	t.Helper()
	packs := []string{"linux-hosts", "prometheus", "servicebus-runtime"}
	if ok, err := s.PatchMonitoringCollector("monitor-collector", "monitor-p", nil, nil, nil, &packs); err != nil || !ok {
		t.Fatalf("collector approval: %v %v", ok, err)
	}
	source := MonitorSource{ID: "legacy-servicebus", ProjectID: "monitor-p", CollectorID: "monitor-collector", Name: "Legacy runtime properties", Pack: "servicebus-runtime", Destination: "management://namespace", IntervalSeconds: 30, Enabled: true,
		ManagementScope:    &analysis.ManagementScope{Version: analysis.ManagementScopeVersion, Kind: analysis.ManagementKindServiceBus, Provider: "example.servicebus.windows.net", Entities: []analysis.ManagementEntity{{ServiceBus: &analysis.ServiceBusManagementEntity{Queue: "orders"}}}},
		ManagementIdentity: &analysis.ManagementIdentity{Mode: "anonymous", Principal: "legacy-fixture", PolicyRevision: "policy-v1"},
	}
	saved, err := s.SaveMonitorSource(source, 0)
	if err != nil {
		t.Fatal(err)
	}
	legacy := *saved
	legacy.IntervalSeconds = 10
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE monitor_sources SET payload=? WHERE id=? AND project_id=?`, string(payload), legacy.ID, legacy.ProjectID); err != nil {
		t.Fatal(err)
	}
	read := func() MonitorSource {
		t.Helper()
		sources, err := s.ListMonitorSources(legacy.ProjectID)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range sources {
			if candidate.ID == legacy.ID {
				return candidate
			}
		}
		t.Fatal("legacy source disappeared")
		return MonitorSource{}
	}
	reopened := read()
	if reopened.IntervalSeconds != 10 || reopened.Revision != 1 {
		t.Fatalf("legacy read changed: %#v", reopened)
	}
	if _, err = s.SaveMonitorSource(reopened, reopened.Revision); err == nil {
		t.Fatal("new revision accepted legacy cadence")
	}
	unchanged := read()
	if unchanged.IntervalSeconds != 10 || unchanged.Revision != 1 {
		t.Fatal("rejected write mutated legacy source")
	}
	reopened.IntervalSeconds = 30
	revised, err := s.SaveMonitorSource(reopened, reopened.Revision)
	if err != nil || revised.Revision != 2 {
		t.Fatalf("policy repair revision: %#v %v", revised, err)
	}
	if actual := read(); actual.IntervalSeconds != 30 || actual.Revision != 2 {
		t.Fatal("revised source was not durable")
	}
	if err = s.DeleteMonitorSource(revised.ProjectID, revised.ID, revised.Revision); err != nil {
		t.Fatal(err)
	}
	sources, err := s.ListMonitorSources(revised.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range sources {
		if candidate.ID == revised.ID {
			t.Fatal("deleted source survived")
		}
	}
}
