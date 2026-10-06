package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHAROwnershipSetupFailurePrecedesNativeLaunch(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(t.TempDir(), alias); err != nil {
		t.Fatal(err)
	}
	globalMutex.Lock()
	beforeDriver, beforeBrowsers := globalPlaywright, len(globalBrowsers)
	globalMutex.Unlock()
	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{"browser_har_capture": "always", "browser_har_dir": alias}); err == nil || !strings.Contains(err.Error(), "HAR") {
		t.Fatal("unsafe HAR setup was not rejected", err)
	}
	globalMutex.Lock()
	afterDriver, afterBrowsers := globalPlaywright, len(globalBrowsers)
	globalMutex.Unlock()
	if beforeDriver != afterDriver || beforeBrowsers != afterBrowsers {
		t.Fatal("native launch preceded ownership setup")
	}
	if b.harOwner != nil || b.harWorkingPath != "" {
		t.Fatal("failed setup left a consumable capture")
	}
}

func TestHAROwnershipTeardownAndOversizedCloseSharePhysicalJoin(t *testing.T) {
	dir := t.TempDir()
	o := fixtureHAROwner(t, dir)
	f, err := os.OpenFile(o.path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	entered := make(chan struct{})
	var calls atomic.Int32
	c := &harCloseFixtureContext{close: func() error {
		calls.Add(1)
		close(entered)
		<-gate
		if _, err := f.WriteString(ownedHARFixture); err != nil {
			return err
		}
		return f.Close()
	}}
	o.beginWriter()
	o.bindWriter(func() error { return c.Close() })
	b := &Browser{harOwner: o, harWorkingPath: o.path, harCapturePolicy: HARCaptureOnError, pwContext: c}
	b.evidence.generation = "current"
	b.closeOwnedOversizedHARContext(o, "current")
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := b.Teardown(ctx); !errors.Is(err, errHAROwnershipUnproven) {
		t.Fatal("blocked teardown was not observable", err)
	}
	if b.harOwner != o || o.retiredSuccessfully() || calls.Load() != 1 {
		t.Fatal("blocked close lost or duplicated physical owner")
	}
	if _, err := os.Stat(o.path); err != nil {
		t.Fatal("raw removed while native flush blocked", err)
	}
	if len(PlanRuntimeHARWorkingCleanup(dir, 128)) != 0 {
		t.Fatal("blocked flush became cleanup proof")
	}
	close(gate)
	artifact, err := o.waitFinish(context.Background())
	if err != nil || artifact == "" {
		t.Fatal("late native flush was not published", err)
	}
	if _, err := os.Stat(o.path); !os.IsNotExist(err) {
		t.Fatal("late proven join retained raw", err)
	}
	if _, err := f.WriteString("late writer"); err == nil {
		t.Fatal("joined file writer remained usable")
	}
	if err := b.Teardown(context.Background()); err != nil {
		t.Fatal("rejoined teardown failed", err)
	}
	if b.harOwner != nil || b.harWorkingPath != "" || calls.Load() != 1 {
		t.Fatal("rejoined teardown did not retire exact owner once")
	}
}

func TestHAROwnershipRedactorPanicReportsUnprovenWork(t *testing.T) {
	o := fixtureHAROwner(t, t.TempDir())
	writeOwnedHARFixture(t, o)
	o.startFinish(true, func(string) string { panic("synthetic redactor panic") })
	if _, err := o.waitFinish(context.Background()); !errors.Is(err, errHAROwnershipUnproven) {
		t.Fatal("redactor panic was not contained", err)
	}
	if o.retiredSuccessfully() {
		t.Fatal("panic invented physical cleanup proof")
	}
	if _, err := os.Stat(o.path); err != nil {
		t.Fatal("panic removed raw ownership evidence", err)
	}
}

func TestHAROwnershipAdmissionIsBounded(t *testing.T) {
	dir := t.TempDir()
	n, err := lockHARNamespace(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxRuntimeHARWorkingGenerations; i++ {
		token, err := harRandomGeneration()
		if err != nil {
			t.Fatal(err)
		}
		if err := n.work.Mkdir(token, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := n.close(); err != nil {
		t.Fatal(err)
	}
	if o, err := newHARCaptureOwner(context.Background(), dir); err == nil {
		_, _ = finishFixtureHAR(t, o, false)
		t.Fatal("unproven staging generations did not bound admission")
	}
	if len(PlanRuntimeHARWorkingCleanup(dir, 100000)) != 0 {
		t.Fatal("empty unsealed generation became cleanup proof")
	}
}

func TestHAROwnershipTeardownRetainsLeaseUntilRuntimeWorkerActuallyJoins(t *testing.T) {
	dir := t.TempDir()
	o := fixtureHAROwner(t, dir)
	writeOwnedHARFixture(t, o)
	b := &Browser{harOwner: o, harWorkingPath: o.path, harCapturePolicy: HARCaptureAlways}
	b.networkWorkerWG.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := b.Teardown(ctx); !errors.Is(err, errHAROwnershipUnproven) {
		t.Fatal("blocked runtime join not observable", err)
	}
	if o.retiredSuccessfully() || b.harOwner != o {
		t.Fatal("blocked runtime join released capture ownership")
	}
	if _, err := os.Stat(o.path); err != nil {
		t.Fatal("raw removed before exact runtime worker joined", err)
	}
	b.networkWorkerWG.Done()
	if _, err := o.waitFinish(context.Background()); err != nil {
		t.Fatal("late runtime join did not retire capture", err)
	}
	if err := b.Teardown(context.Background()); err != nil {
		t.Fatal("rejoined teardown", err)
	}
}
