package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestLegacyPrometheusFlushUsesActualObservationSlots(t *testing.T) {
	for _, tc := range []struct {
		name      string
		slots     []int
		modern    bool
		wantState string
	}{{"complete", []int{0, 1, 2}, true, "stopped"}, {"duplicates", []int{0, 0, 0}, true, "incomplete"}, {"old_collector", []int{0}, false, "incomplete"}} {
		t.Run(tc.name, func(t *testing.T) {
			s, source, now := monitorFixture(t)
			start := now
			source.Pack = "prometheus"
			source.Destination = "https://exporter.example.com/metrics"
			packs := []string{"prometheus"}
			if tc.modern {
				packs = append(packs, "prometheus-scrape.v2")
			}
			if err := s.SaveCollectorRunReadiness(source.ProjectID, source.CollectorID, packs, []string{source.Destination}, now); err != nil {
				t.Fatal(err)
			}
			saved, err := s.SaveMonitorSource(source, 0)
			if err != nil {
				t.Fatal(err)
			}
			source = *saved
			if _, err = s.SaveMonitorBindings(MonitorBindings{ProjectID: source.ProjectID, ScenarioID: "monitor-scenario", Sources: []MonitorBinding{{SourceID: source.ID}}}, 0); err != nil {
				t.Fatal(err)
			}
			jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, now.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			end := start.Add(30 * time.Second)
			for seq, slot := range tc.slots {
				v := float64(0)
				point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: source.ProjectID, RunID: "monitor-run", Source: source.ID, TimestampUTC: start.Add(time.Duration(slot*10+1) * time.Second), MetricName: "cpu", Kind: analysis.KindGauge, Quality: analysis.QualityValid, Value: &v}
				receipt := MonitorBatchReceipt{JobID: jobs[0].ID, Sequence: int64(seq), Digest: fmt.Sprintf("%064x", seq+1), ArtifactKey: "fixture", SampleCount: 1, ReceivedAt: end}
				if err = s.AcceptLegacyPrometheusBatch(source.ProjectID, source.CollectorID, receipt, []analysis.MetricPoint{point}); err != nil {
					t.Fatal(err)
				}
			}
			record, _ := s.GetTest("monitor-run")
			record.Status = "COMPLETE"
			record.EndTime = &end
			if _, err = s.SaveTestWithRunMonitors(record, "", "", true); err != nil {
				t.Fatal(err)
			}
			job, _ := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
			if tc.modern {
				if job.State != "flushing" || !job.FlushOnly || job.DrainDeadline == nil || !job.DrainDeadline.Equal(end.Add(30*time.Second)) {
					t.Fatalf("flush phase %+v", job)
				}
				poll, err := s.PollRunMonitorJobs(source.ProjectID, source.CollectorID, end.Add(time.Second))
				if err != nil || len(poll) != 1 {
					t.Fatalf("flush grant not polled %v %v", poll, err)
				}
				if err = s.CompleteLegacyPrometheusFlush(source.ProjectID, source.CollectorID, job.ID, end.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			job, _ = s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
			if job.State != tc.wantState || job.Coverage["scrape"].ExpectedPoints != 3 {
				t.Fatalf("capture coverage %+v", job)
			}
			if tc.name == "duplicates" && job.Coverage["scrape"].Points != 1 {
				t.Fatal("old source timestamp backfilled gaps")
			}
		})
	}
}

func TestLegacyPrometheusFlushExpiryPreservesIncompleteCoverage(t *testing.T) {
	s, source, now := monitorFixture(t)
	source.Pack = "prometheus"
	source.Destination = "https://exporter.example.com/metrics"
	if err := s.SaveCollectorRunReadiness(source.ProjectID, source.CollectorID, []string{"prometheus", "prometheus-scrape.v2"}, []string{source.Destination}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveMonitorSource(source, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveMonitorBindings(MonitorBindings{ProjectID: source.ProjectID, ScenarioID: "monitor-scenario", Sources: []MonitorBinding{{SourceID: source.ID}}}, 0); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	end := now.Add(time.Minute)
	record, _ := s.GetTest("monitor-run")
	record.Status = "COMPLETE"
	record.EndTime = &end
	if _, err = s.SaveTestWithRunMonitors(record, "", "", true); err != nil {
		t.Fatal(err)
	}
	if err = s.CloseExpiredProviderMonitors(source.ProjectID, "monitor-run", end.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	job, _ := s.GetRunMonitorJob(source.ProjectID, jobs[0].ID)
	if job.State != "incomplete" || job.Coverage["scrape"].Points != 0 || job.Coverage["scrape"].MissingPoints != 6 {
		t.Fatalf("lost expired scrape coverage %+v", job)
	}
}
