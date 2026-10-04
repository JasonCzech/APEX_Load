package collectoragent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestCollectorSpoolRestartAndIsolation(t *testing.T) {
	cfg := Config{ControllerURL: "http://localhost:8080", CollectorID: "c", Token: "token", SpoolDirectory: t.TempDir()}
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	v := 3.0
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", Source: "s", MetricName: "cpu", Kind: analysis.KindGauge, Value: &v, Quality: analysis.QualityValid, TimestampUTC: time.Now().UTC()}
	a.pendingBatches["j"] = &RunSampleBatch{Sequence: 2, Points: []analysis.MetricPoint{p}}
	if err = a.saveSpool(); err != nil {
		t.Fatal(err)
	}
	b, _ := New(cfg)
	if err = b.loadSpool(); err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(a.pendingBatches)
	right, _ := json.Marshal(b.pendingBatches)
	if string(left) != string(right) {
		t.Fatal("restart changed source evidence")
	}
	cfg.CollectorID = "other"
	other, _ := New(cfg)
	if err = other.loadSpool(); err != nil || len(other.pendingBatches) != 0 {
		t.Fatal("another collector read spool")
	}
	delete(b.pendingBatches, "j")
	if err = b.saveSpool(); err != nil {
		t.Fatal(err)
	}
	c, _ := New(a.cfg)
	if err = c.loadSpool(); err != nil || len(c.pendingBatches) != 0 {
		t.Fatal("acknowledged batch survived restart")
	}
	if err = os.WriteFile(a.spoolPath(), []byte("truncated"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = c.loadSpool(); err == nil {
		t.Fatal("silently discarded corrupt spool")
	}
}

func TestCollectorSpoolSingleOwner(t *testing.T) {
	a, err := New(Config{ControllerURL: "http://localhost:8080", CollectorID: "c", Token: "token", SpoolDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	release, err := a.lockSpool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if unlock, err := a.lockSpool(ctx); err == nil {
		unlock()
		t.Fatal("concurrent collector acquired same spool")
	}
}

func TestCollectorInMemoryRetryAdmissionIsBoundedAndRollsBack(t *testing.T) {
	newAgent := func(t *testing.T, directory string) *Agent {
		t.Helper()
		agent, err := New(Config{ControllerURL: "http://localhost:8080", CollectorID: "c", Token: "token", SpoolDirectory: directory})
		if err != nil {
			t.Fatal(err)
		}
		return agent
	}
	value := 1.0
	point := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: "p", Source: "s", MetricName: "cpu", Kind: analysis.KindGauge, Value: &value, Quality: analysis.QualityValid, TimestampUTC: time.Now().UTC()}
	t.Run("no_directory_job_budget", func(t *testing.T) {
		agent := newAgent(t, "")
		for i := 0; i < maxCollectorSpoolJobs; i++ {
			if err := agent.admitMonitorRetry(fmt.Sprintf("job-%d", i), &RunSampleBatch{Sequence: int64(i), Points: []analysis.MetricPoint{point}}, nil); err != nil {
				t.Fatal(err)
			}
		}
		before, _ := json.Marshal(agent.pendingBatches)
		for i := 0; i < 10; i++ {
			if err := agent.admitMonitorRetry(fmt.Sprintf("excess-%d", i), &RunSampleBatch{Points: []analysis.MetricPoint{point}}, []*RunSampleBatch{{Points: []analysis.MetricPoint{point}}}); err == nil {
				t.Fatal("in-memory job limit was bypassed")
			}
		}
		after, _ := json.Marshal(agent.pendingBatches)
		if string(before) != string(after) || len(agent.providerBacklog) != 0 {
			t.Fatal("failed new-job admission changed already queued evidence")
		}
	})
	t.Run("no_directory_byte_budget", func(t *testing.T) {
		agent := newAgent(t, "")
		if err := agent.admitMonitorRetry("existing", &RunSampleBatch{Points: []analysis.MetricPoint{point}}, nil); err != nil {
			t.Fatal(err)
		}
		// A queue can be large without containing any individually large metric.
		// Provider-supplied bytes still count with optional persistence disabled.
		large := &RunSampleBatch{Job: SignedRunJob{Payload: strings.Repeat("x", 34<<20)}, Points: []analysis.MetricPoint{point}}
		if err := agent.admitMonitorRetry("new", large, []*RunSampleBatch{large}); err == nil {
			t.Fatal("in-memory byte limit was bypassed")
		}
		if len(agent.pendingBatches) != 1 || agent.pendingBatches["existing"] == nil || agent.pendingBatches["new"] != nil || len(agent.providerBacklog) != 0 {
			t.Fatal("byte rejection retained a new queue or lost an existing retry")
		}
	})
	t.Run("persistent_write_failure", func(t *testing.T) {
		directory := t.TempDir()
		agent := newAgent(t, directory)
		original := &RunSampleBatch{Sequence: 7, Points: []analysis.MetricPoint{point}}
		if err := agent.admitMonitorRetry("existing", original, nil); err != nil {
			t.Fatal(err)
		}
		blocked := directory + "/file"
		if err := os.WriteFile(blocked, []byte("file"), 0600); err != nil {
			t.Fatal(err)
		}
		agent.cfg.SpoolDirectory = blocked
		if err := agent.admitMonitorRetry("new", &RunSampleBatch{Points: []analysis.MetricPoint{point}}, []*RunSampleBatch{{Points: []analysis.MetricPoint{point}}}); err == nil {
			t.Fatal("failed persistence admitted a new queue")
		}
		if len(agent.pendingBatches) != 1 || agent.pendingBatches["existing"] != original || len(agent.providerBacklog) != 0 {
			t.Fatal("failed disk write changed existing retry evidence")
		}
	})
}
