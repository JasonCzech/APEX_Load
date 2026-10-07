package storage

import (
	"strings"
	"testing"
	"time"
)

func TestPreparedOptionalProviderCannotBypassUnavailableConnection(t *testing.T) {
	for _, provider := range []string{"prometheus", "appdynamics"} {
		for _, state := range []string{"disabled", "unverified"} {
			t.Run(provider+"_"+state, func(t *testing.T) {
				var s *SQLiteStore
				var source MonitorSource
				var start time.Time
				if provider == "prometheus" {
					s, source, start = prometheusMonitorFixture(t)
				} else {
					s, source, start = appDynamicsMonitorFixture(t)
				}
				if provider == "prometheus" {
					connection, _ := s.GetPrometheusConnection(source.ConnectionID)
					if state == "disabled" {
						connection.Enabled = false
					} else {
						connection.Config.MaxPoints = 5000
					}
					if ok, err := s.UpdatePrometheusConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
						t.Fatal(ok, err)
					}
				} else {
					connection, _ := s.GetAppDynamicsConnection(source.ConnectionID)
					if state == "disabled" {
						connection.Enabled = false
					} else {
						connection.Config.AccountName = "changed-account"
					}
					if ok, err := s.UpdateAppDynamicsConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
						t.Fatal(ok, err)
					}
				}
				run, _ := s.GetTest("monitor-run")
				run.Status = "QUEUED"
				run.MonitorBindings = &MonitorBindings{Sources: []MonitorBinding{{SourceID: source.ID}}}
				if err := s.SaveTest(run); err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC()
				prepared, err := s.CreatePreparation(Preparation{ID: "optional", ProjectID: source.ProjectID, TestID: run.ID, ExecutionRevision: strings.Repeat("a", 64), IdempotencyKey: "optional", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
				if err != nil {
					t.Fatal(err)
				}
				if prepared.Monitors.PrometheusConnections[source.ID] != nil || prepared.Monitors.Connections[source.ID] != nil {
					t.Fatal("unavailable connection captured as executable profile")
				}
				run.Status = "INITIALIZING"
				if err = s.SaveTest(run); err != nil {
					t.Fatal(err)
				}
				prepared, err = s.TransitionPreparation(prepared.ID, "preparing", "ready", prepared.Revision, "", nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.ConsumePreparation(prepared.ID, prepared.Revision, "", "tester", "Tester"); err != nil {
					t.Fatal(err)
				}
				if state == "disabled" {
					if provider == "prometheus" {
						connection, _ := s.GetPrometheusConnection(source.ConnectionID)
						connection.Enabled = true
						if ok, err := s.UpdatePrometheusConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
							t.Fatal(ok, err)
						}
					} else {
						connection, _ := s.GetAppDynamicsConnection(source.ConnectionID)
						connection.Enabled = true
						if ok, err := s.UpdateAppDynamicsConnectionIfRevision(connection, connection.Revision); err != nil || !ok {
							t.Fatal(ok, err)
						}
					}
				}
				jobs, err := s.StartRunMonitors(source.ProjectID, run.ID, run.ScenarioID, start, now.Add(time.Hour))
				if err != nil || len(jobs) != 1 {
					t.Fatal(jobs, err)
				}
				if jobs[0].State != "incomplete" || jobs[0].CoverageReason != "connection_not_ready" || jobs[0].PrometheusConnectionSnapshot != nil || jobs[0].ConnectionSnapshot != nil {
					t.Fatal("future connection profile bypassed immutable optional readiness")
				}
			})
		}
	}
}
