package storage

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRunMonitorJobPagesAcrossConcurrentRuns(t *testing.T) {
	store, template, now := monitorFixture(t)
	bindings := MonitorBindings{ProjectID: template.ProjectID, ScenarioID: "monitor-scenario"}
	for i := 0; i < 24; i++ {
		source := template
		source.ID = fmt.Sprintf("page-source-%02d", i)
		if _, err := store.SaveMonitorSource(source, 0); err != nil {
			t.Fatal(err)
		}
		bindings.Sources = append(bindings.Sources, MonitorBinding{SourceID: source.ID, Required: true})
	}
	if _, err := store.SaveMonitorBindings(bindings, 0); err != nil {
		t.Fatal(err)
	}
	for _, run := range []string{"monitor-run", "monitor-second-run"} {
		if run != "monitor-run" {
			if err := store.SaveTest(&TestRecord{ID: run, Name: run, ProjectID: template.ProjectID, ScenarioID: bindings.ScenarioID, Status: "RUNNING", StartTime: now}); err != nil {
				t.Fatal(err)
			}
		}
		if jobs, err := store.StartRunMonitors(template.ProjectID, run, bindings.ScenarioID, now, now.Add(time.Hour)); err != nil || len(jobs) != 24 {
			t.Fatalf("start %s: %d %v", run, len(jobs), err)
		}
	}
	first, more, err := store.PollRunMonitorJobPage(template.ProjectID, template.CollectorID, "", 32, now)
	if err != nil || !more || len(first) != 32 {
		t.Fatalf("first %d %v %v", len(first), more, err)
	}
	second, more, err := store.PollRunMonitorJobPage(template.ProjectID, template.CollectorID, first[len(first)-1].ID, 32, now)
	if err != nil || more || len(second) != 16 || second[0].ID <= first[len(first)-1].ID {
		t.Fatalf("second %d %v %v", len(second), more, err)
	}
	seen := map[string]bool{}
	for _, at := range []time.Time{now, now.Add(15 * time.Second)} {
		jobs, err := store.PollRunMonitorJobs(template.ProjectID, template.CollectorID, at)
		if err != nil || len(jobs) > 32 {
			t.Fatalf("legacy concurrent poll: %d %v", len(jobs), err)
		}
		for _, job := range jobs {
			seen[job.ID] = true
		}
	}
	if len(seen) != 48 {
		t.Fatalf("legacy rotation starved %d jobs", 48-len(seen))
	}
	if jobs, _, err := store.PollRunMonitorJobPage("monitor-other", template.CollectorID, "", 32, now); err != nil || len(jobs) != 0 {
		t.Fatalf("project leak %+v %v", jobs, err)
	}
	if jobs, _, err := store.PollRunMonitorJobPage(template.ProjectID, template.CollectorID, "", 32, now.Add(2*time.Hour)); err != nil || len(jobs) != 0 {
		t.Fatalf("expired jobs %+v %v", jobs, err)
	}
	for _, limit := range []int{0, 33} {
		if _, _, err := store.PollRunMonitorJobPage(template.ProjectID, template.CollectorID, "", limit, now); err == nil {
			t.Fatal("unbounded page accepted")
		}
	}
}

func TestRunMonitorJobPageAllowsCombinedIdentityBoundary(t *testing.T) {
	store, template, now := monitorFixture(t)
	template.ID = strings.Repeat("s", 256)
	if _, err := store.SaveMonitorSource(template, 0); err != nil {
		t.Fatal(err)
	}
	run := strings.Repeat("r", 256)
	if err := store.SaveTest(&TestRecord{ID: run, Name: run, ProjectID: template.ProjectID, Status: "RUNNING", StartTime: now, MonitorBindings: &MonitorBindings{Sources: []MonitorBinding{{SourceID: template.ID, Required: true}}}}); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.StartRunMonitors(template.ProjectID, run, "", now, now.Add(time.Hour))
	if err != nil || len(jobs) != 1 || len(jobs[0].ID) != MaxRunMonitorJobIDBytes {
		t.Fatalf("combined identity %+v %v", jobs, err)
	}
	if page, _, err := store.PollRunMonitorJobPage(template.ProjectID, template.CollectorID, jobs[0].ID, 32, now); err != nil || len(page) != 0 {
		t.Fatalf("maximum cursor %+v %v", page, err)
	}
	if _, _, err := store.PollRunMonitorJobPage(template.ProjectID, template.CollectorID, jobs[0].ID+"x", 32, now); err == nil {
		t.Fatal("oversize combined identity accepted")
	}
}
