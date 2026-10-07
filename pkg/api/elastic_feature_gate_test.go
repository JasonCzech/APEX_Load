package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestElasticFeatureDisablePreservesFrozenRunHeader(t *testing.T) {
	server, store, job, _, _ := apiElasticMonitorFixture(t, func(selection *elastic.Selection) {
		selection.RunHeader = true
		selection.HeaderOrigins = []string{"https://sut.example.com"}
	})
	source := job.Source
	selection := *source.Elastic
	selection.HeaderOrigins = []string{"https://future.example.com"}
	source.Elastic = &selection
	if _, err := store.SaveMonitorSource(source, source.Revision); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", "")
	if got := server.runner.runMonitorCorrelationOrigins(job.RunID); !reflect.DeepEqual(got, job.Source.Elastic.HeaderOrigins) {
		t.Fatalf("disabled feature or draft edit changed active run origins: %v", got)
	}
}

// Managed correlation is validated against the frozen Elastic assignment before
// any VUser is launched, including case-insensitive authored header aliases.
func TestElasticManagedRunHeaderAliasConflictBeforeTraffic(t *testing.T) {
	server, _, job, _, _ := apiElasticMonitorFixture(t, func(selection *elastic.Selection) {
		selection.RunHeader = true
		selection.HeaderOrigins = []string{"https://sut.example.com"}
	})
	script := &dsl.Script{Protocol: "http/2", Config: dsl.ScriptConfig{BaseURL: "https://sut.example.com", HTTP: dsl.HTTPConfig{DefaultHeaders: map[string]string{"x-aPeX-tEsT-rUn": "another-run"}}}}
	server.runner.mu.Lock()
	server.runner.tests[job.RunID] = &ManagedTest{ID: job.RunID, ProjectID: job.ProjectID, groupSpecs: []engine.GroupConfig{{Script: script}}}
	server.runner.mu.Unlock()
	if err := server.runner.validateRunMonitorCorrelation(job.RunID); err == nil || !strings.Contains(err.Error(), "authored X-Apex-Test-Run conflicts") {
		t.Fatalf("conflicting authored alias passed launch validation: %v", err)
	}
	script.Config.HTTP.DefaultHeaders["x-aPeX-tEsT-rUn"] = "{{run.id}}"
	if err := server.runner.validateRunMonitorCorrelation(job.RunID); err != nil {
		t.Fatalf("matching managed run template rejected: %v", err)
	}
}
