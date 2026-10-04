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

func TestRunMonitorJobPagesRetainPublished256JobContract(t *testing.T) {
	store, template, now := monitorFixture(t)
	bindings := MonitorBindings{ProjectID: template.ProjectID, ScenarioID: "monitor-scenario"}
	for i := 0; i < 32; i++ {
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
	for i := 0; i < 8; i++ {
		run := fmt.Sprintf("page-run-%02d", i)
		if err := store.SaveTest(&TestRecord{ID: run, Name: run, ProjectID: template.ProjectID, ScenarioID: bindings.ScenarioID, Status: "RUNNING", StartTime: now}); err != nil {
			t.Fatal(err)
		}
		if jobs, err := store.StartRunMonitors(template.ProjectID, run, bindings.ScenarioID, now, now.Add(time.Hour)); err != nil || len(jobs) != 32 {
			t.Fatalf("start published concurrent run %s: %d %v", run, len(jobs), err)
		}
	}
	seen := map[string]bool{}
	after := ""
	for page := 0; ; page++ {
		jobs, more, err := store.PollRunMonitorJobPage(template.ProjectID, template.CollectorID, after, 32, now)
		if err != nil || len(jobs) != 32 || more != (page < 7) {
			t.Fatalf("published page %d: %d more=%v err=%v", page, len(jobs), more, err)
		}
		for _, job := range jobs {
			if seen[job.ID] || job.ID <= after || job.ProjectID != template.ProjectID || job.Source.CollectorID != template.CollectorID {
				t.Fatalf("published pagination duplicated or crossed an owner: %+v", job)
			}
			seen[job.ID] = true
			after = job.ID
		}
		if !more {
			break
		}
	}
	if len(seen) != 256 {
		t.Fatalf("published collector contract returned %d jobs", len(seen))
	}
	legacy := map[string]bool{}
	for i := 0; i < 8; i++ {
		jobs, err := store.PollRunMonitorJobs(template.ProjectID, template.CollectorID, now.Add(time.Duration(i)*15*time.Second))
		if err != nil || len(jobs) != 32 {
			t.Fatalf("bounded legacy page %d: %d %v", i, len(jobs), err)
		}
		for _, job := range jobs {
			if !seen[job.ID] {
				t.Fatal("legacy page introduced a different population")
			}
			legacy[job.ID] = true
		}
	}
	if len(legacy) != 256 {
		t.Fatalf("legacy rotation starved published concurrent jobs: %d", len(legacy))
	}
}

func TestRunMonitorJobPagesKeepManagementCapacitySeparate(t *testing.T) {
	store, management, now := managementFixture(t)
	fillManagementCollectorAssignments(t, store, management, now, MaxCollectorAssignedMonitorJobs)
	scope, identity, err := managementSourceHashes(management)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveCollectorManagementReadiness(management.ProjectID, management.CollectorID, CollectorManagementReadiness{InstanceID: "instance-1", Capabilities: []string{ManagementEvidenceCapability}, Scopes: []ManagementReadinessScope{{scope, identity}}, Packs: []string{management.Pack, "linux-hosts"}, Destinations: []string{management.ReadinessDestination()}, SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	bindings := &MonitorBindings{Sources: []MonitorBinding{}}
	for i := 0; i < 24; i++ {
		source := MonitorSource{ID: fmt.Sprintf("page-host-%02d", i), ProjectID: management.ProjectID, CollectorID: management.CollectorID, Name: "host", Pack: "linux-hosts", IntervalSeconds: 10, Enabled: true}
		if _, err := store.SaveMonitorSource(source, 0); err != nil {
			t.Fatal(err)
		}
		bindings.Sources = append(bindings.Sources, MonitorBinding{SourceID: source.ID, Required: true})
	}
	for i := 0; i < 2; i++ {
		run := fmt.Sprintf("page-host-run-%02d", i)
		if err := store.SaveTest(&TestRecord{ID: run, Name: run, ProjectID: management.ProjectID, ScenarioID: "monitor-scenario", Status: "RUNNING", StartTime: now, MonitorBindings: bindings}); err != nil {
			t.Fatal(err)
		}
		if jobs, err := store.StartRunMonitors(management.ProjectID, run, "monitor-scenario", now, now.Add(time.Hour)); err != nil || len(jobs) != 24 {
			t.Fatalf("management capacity blocked unrelated pack: %d %v", len(jobs), err)
		}
	}
	candidate := "page-management-candidate"
	if err := store.SaveTest(&TestRecord{ID: candidate, Name: candidate, ProjectID: management.ProjectID, ScenarioID: "monitor-scenario", Status: "INITIALIZING", StartTime: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateRunMonitorReadiness(management.ProjectID, candidate, "monitor-scenario", now); err == nil || !strings.Contains(err.Error(), "collector_capacity") {
		t.Fatalf("management prepare lost its own assignment limit: %v", err)
	}
	if _, err := store.StartRunMonitors(management.ProjectID, candidate, "monitor-scenario", now, now.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "collector_capacity") {
		t.Fatalf("management start lost its own assignment limit: %v", err)
	}
	if jobs, err := store.ListRunMonitorJobs(management.ProjectID, candidate); err != nil || len(jobs) != 0 {
		t.Fatalf("refused management start left jobs: %d %v", len(jobs), err)
	}
	if err := store.StopRunMonitors(management.ProjectID, "monitor-run", now); err != nil {
		t.Fatal(err)
	}
	if jobs, err := store.StartRunMonitors(management.ProjectID, candidate, "monitor-scenario", now, now.Add(time.Hour)); err != nil || len(jobs) != 1 || jobs[0].State != "collecting" {
		t.Fatalf("unrelated concurrent jobs consumed a released management slot: %+v %v", jobs, err)
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
