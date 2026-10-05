package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestOpenSearchRunRecordsUnsupportedConnectionAuth(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "opensearch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	requireProjectFixtures(t, s, "os-p")
	if err := s.CreateELKConnection(&ELKConnection{ID: "os-conn", ProjectID: "os-p", Name: "Logs", Enabled: true, Endpoint: "https://search.example", IndexPattern: "logs-*", TimestampField: "@timestamp", MessageField: "message", AuthType: ELKAuthBasic, UsernameSecretName: "u", PasswordSecretName: "p"}); err != nil {
		t.Fatal(err)
	}
	source := MonitorSource{ID: "os-source", ProjectID: "os-p", Name: "OpenSearch logs", Enabled: true, Pack: "elastic-query", ExecutorMode: "direct", ConnectionID: "os-conn", IntervalSeconds: 60, ElasticQuery: &ElasticQuerySelection{ProjectID: "os-p", Profile: ElasticProfileOpenSearch}}
	if _, err := s.SaveMonitorSource(source, 0); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	if err := s.SaveTest(&TestRecord{ID: "os-run", ProjectID: "os-p", Name: "run", Status: "RUNNING", StartTime: start, StartedAt: &start, MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID}}}}); err != nil {
		t.Fatal(err)
	}
	// A connection changed outside the guarded update, such as by restore.
	if _, err := s.db.Exec(`UPDATE elk_connections SET auth_type=?, api_key_secret_name=? WHERE id=?`, ELKAuthAPIKey, "k", "os-conn"); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.StartRunMonitors("os-p", "os-run", "", start, start.Add(time.Hour))
	if err != nil || len(jobs) != 1 || jobs[0].CoverageReason != "connection_auth_unsupported" {
		t.Fatalf("OpenSearch run on an API-key connection: %+v %v", jobs, err)
	}
}
