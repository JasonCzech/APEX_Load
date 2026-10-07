package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLegacyElasticConflictCoversCanonicalPacksAndBindingModes(t *testing.T) {
	for _, pack := range []string{monitorprovider.PackElastic, monitorprovider.PackElasticQuery} {
		t.Run(pack, func(t *testing.T) {
			server, durable := newTimeslotServer(t)
			now := time.Now().UTC()
			connection := &storage.ELKConnection{ID: "conflict-connection", ProjectID: "proj-a", Name: "Elastic", Enabled: true, IndexPattern: "logs-*", TimestampField: "@timestamp", Config: elastic.Config{Endpoint: "https://search.example", Product: elastic.ProductElasticsearch, AuthType: elastic.AuthAPIKey}, APIKeySecretName: "KEY"}
			connections := durable.(storage.ElasticConnectionStore)
			if err := connections.CreateELKConnection(connection); err != nil {
				t.Fatal(err)
			}
			if ok, err := connections.UpdateElasticConnectionHealth(connection.ID, connection.ProjectID, connection.Revision, "connected", "", elastic.Capabilities{DSL: true, PIT: true}, now); err != nil || !ok {
				t.Fatal(ok, err)
			}
			source := storage.MonitorSource{ID: "conflict-source", ProjectID: connection.ProjectID, Name: "Elastic", Enabled: true, Pack: pack, ExecutorMode: "direct", ConnectionID: connection.ID, IntervalSeconds: 60}
			if pack == monitorprovider.PackElastic {
				source.Elastic = &elastic.Selection{ProjectID: source.ProjectID, Indices: []string{"logs-*"}, Logs: true}
			} else {
				source.ElasticQuery = &storage.ElasticQuerySelection{ProjectID: source.ProjectID}
			}
			monitors := durable.(storage.RunMonitorStore)
			if _, err := monitors.SaveMonitorSource(source, 0); err != nil {
				t.Fatal(err)
			}
			if err := durable.SaveScript(&dsl.Script{ID: "conflict-script", Name: "Conflict", Protocol: "http", ProjectID: source.ProjectID, CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
			if err := durable.SaveScenario(&storage.Scenario{ID: "conflict-scenario", Name: "Conflict", Duration: "1m", CreatedAt: now}, []storage.ScenarioScript{{ScriptID: "conflict-script"}}); err != nil {
				t.Fatal(err)
			}
			selected := &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: source.ID}}}
			if _, err := monitors.SaveMonitorBindings(storage.MonitorBindings{ScenarioID: "conflict-scenario", ProjectID: source.ProjectID, Sources: selected.Sources}, 0); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name       string
				scenario   string
				connection string
				bindings   *storage.MonitorBindings
				conflict   bool
			}{
				{name: "inherited", scenario: "conflict-scenario", connection: connection.ID, conflict: true},
				{name: "explicit", scenario: "conflict-scenario", connection: connection.ID, bindings: selected, conflict: true},
				{name: "explicit-off", scenario: "conflict-scenario", connection: connection.ID, bindings: &storage.MonitorBindings{Sources: []storage.MonitorBinding{}}},
				{name: "different-connection", scenario: "conflict-scenario", connection: "other", bindings: selected},
				{name: "no-inherited-scenario", connection: connection.ID},
				{name: "no-legacy-capture", scenario: "conflict-scenario", bindings: selected},
			} {
				t.Run(tc.name, func(t *testing.T) {
					err := server.runner.validateLegacyElasticConflict(source.ProjectID, tc.scenario, tc.connection, tc.bindings)
					if (err != nil) != tc.conflict {
						t.Fatalf("duplicate capture conflict=%v, want %v: %v", err != nil, tc.conflict, err)
					}
				})
			}
		})
	}
}
