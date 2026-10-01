package collectoragent

import (
	"context"
	"encoding/json"
	"os"
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
