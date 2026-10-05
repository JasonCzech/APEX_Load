package duckdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func workerRuntimeTestBootstrap(t *testing.T) workerBootstrap {
	b := workerTestBootstrap(t)
	content := make([]byte, 100)
	hash := sha256.Sum256(content)
	b.Handles[0].SHA256 = hex.EncodeToString(hash[:])
	b.Identity.ManifestDigest, _ = b.manifestDigest()
	return b
}

type workerTestAdmission struct{ reservations, releases atomic.Int32 }

func (a *workerTestAdmission) reserve(ctx context.Context, b workerBootstrap) (func(), error) {
	a.reservations.Add(1)
	return func() { a.releases.Add(1) }, nil
}
func workerRuntimeTestAttempt(t *testing.T, ctx context.Context) (*workerAttempt, *workerTestAdmission) {
	t.Helper()
	b := workerRuntimeTestBootstrap(t)
	admission := &workerTestAdmission{}
	a, err := newWorkerAttempt(ctx, t.TempDir(), b, admission)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(t.TempDir(), "source-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err = file.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	if err = a.stageInput(b.Handles[0].Handle, file); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.release() })
	return a, admission
}
func TestWorkerAttemptAdmissionPrecedesScratchAndStage(t *testing.T) {
	var admitted bool
	admission := newWorkerAdmissionController(func(b workerBootstrap) error { admitted = true; return nil })
	a, err := newWorkerAttempt(context.Background(), t.TempDir(), workerRuntimeTestBootstrap(t), admission)
	if err != nil || !admitted {
		t.Fatal("scratch appeared without resource admission", err)
	}
	if _, err = admission.reserve(context.Background(), a.bootstrap); !errors.Is(err, ErrBusy) {
		t.Fatal("second job admitted while staging")
	}
	if err = a.release(); err != nil {
		t.Fatal(err)
	}
	release, err := admission.reserve(context.Background(), a.bootstrap)
	if err != nil {
		t.Fatal("reservation not released", err)
	}
	release()
	release()
}
func TestWorkerAdmissionCancelledInsideProbeIsNotGranted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	admission := newWorkerAdmissionController(func(workerBootstrap) error { cancel(); return nil })
	if release, err := admission.reserve(ctx, workerRuntimeTestBootstrap(t)); release != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled reservation granted")
	}
	admission.probe = func(workerBootstrap) error { return nil }
	release, err := admission.reserve(context.Background(), workerRuntimeTestBootstrap(t))
	if err != nil {
		t.Fatal("failed admission leaked charge")
	}
	release()
}
func TestWorkerAttemptRefusesUnknownOrChangedInputAndRetainsImmutableManifest(t *testing.T) {
	a, _ := workerRuntimeTestAttempt(t, context.Background())
	file, err := os.CreateTemp(t.TempDir(), "source-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	file.Write(make([]byte, 100))
	if a.stageInput("foreign", file) == nil {
		t.Fatal("unknown input handle staged")
	}
	if err := os.WriteFile(filepath.Join(a.path, a.bootstrap.Handles[0].RelativeName), make([]byte, 99), 0600); err != nil {
		t.Fatal(err)
	}
	if a.verifyInputs(context.Background()) == nil {
		t.Fatal("changed immutable input accepted")
	}
}
func TestWorkerAttemptCannotReleaseBeforeReapOrWhileOutputReaderOwnsDescriptor(t *testing.T) {
	t.Run("unreaped", func(t *testing.T) {
		a, admission := workerRuntimeTestAttempt(t, context.Background())
		a.started = true
		if !errors.Is(a.release(), errWorkerOwnership) || admission.releases.Load() != 0 {
			t.Fatal("unreaped worker lost charge")
		}
		a.mu.Lock()
		a.reaped = true
		a.pipesJoined = true
		a.notifyPhysicalLocked()
		a.mu.Unlock()
		if err := a.release(); err != nil || admission.releases.Load() != 1 {
			t.Fatal("actual reaping did not release", err)
		}
	})
	t.Run("output reader", func(t *testing.T) {
		a, admission := workerRuntimeTestAttempt(t, context.Background())
		a.started = true
		a.reaped = true
		a.pipesJoined = true
		content := []byte("PAR1fixture!")
		hash := sha256.Sum256(content)
		h := a.bootstrap.Handles[1]
		if err := os.WriteFile(filepath.Join(a.path, h.RelativeName), content, 0600); err != nil {
			t.Fatal(err)
		}
		reader, err := a.openOutput(workerOutput{Handle: h.Handle, Bytes: int64(len(content)), SHA256: hex.EncodeToString(hash[:]), Rows: 2})
		if err != nil {
			t.Fatal(err)
		}
		if !errors.Is(a.release(), errWorkerOwnership) || admission.releases.Load() != 0 {
			t.Fatal("open output reader lost charge")
		}
		got, err := io.ReadAll(reader)
		if err != nil || string(got) != string(content) {
			t.Fatal("verified descriptor changed", err)
		}
		if err = reader.Close(); err != nil {
			t.Fatal(err)
		}
		if err = reader.Close(); err != nil {
			t.Fatal(err)
		}
		if err = a.release(); err != nil || admission.releases.Load() != 1 {
			t.Fatal("physical closure did not release exactly once", err)
		}
		if _, err = os.Stat(a.path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("owned scratch survived proven release")
		}
	})
}
func TestWorkerScratchQuotaRejectsUnknownOutputAndSymlink(t *testing.T) {
	a, _ := workerRuntimeTestAttempt(t, context.Background())
	if err := os.WriteFile(filepath.Join(a.path, "excess.parquet"), make([]byte, 2049), 0600); err != nil {
		t.Fatal(err)
	}
	if workerScratchQuotas(a) == nil {
		t.Fatal("output expansion bypassed quota")
	}
	os.Remove(filepath.Join(a.path, "excess.parquet"))
	if err := os.Symlink(a.path, filepath.Join(a.path, "loop")); err == nil {
		if workerScratchQuotas(a) == nil {
			t.Fatal("symlink accepted")
		}
	}
}
func TestNativeWorkerModeNeverFallsThroughOnMalformedMarker(t *testing.T) {
	for _, args := range [][]string{{"--apexload-native-worker-v2"}, {workerChildArgument, "extra"}, {"--apexload-native-worker"}} {
		called := false
		handled, code := dispatchNativeWorker(args, func() (workerChildExecutor, error) { called = true; return nil, nil })
		if !handled || code == 0 || called {
			t.Fatal("malformed requested worker fell through", args)
		}
	}
	if handled, _ := dispatchNativeWorker([]string{"--ordinary-controller-option"}, nil); handled {
		t.Fatal("ordinary application launch taken")
	}
}
