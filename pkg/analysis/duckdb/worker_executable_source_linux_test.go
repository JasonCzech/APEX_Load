//go:build linux

package duckdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const workerLoadedImageFixtureKey = "APEXLOAD_NATIVE_TEST_LOADED_IMAGE_DIR"

func workerLoadedImageFixtureHash(path string) (digest string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > workerExecutableMaxBytes {
		return "", errWorkerResourceLimit
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, info.Size()+1))
	if err != nil || n != info.Size() {
		return "", errWorkerProtocol
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func workerLoadedImageWaitMarker(ctx context.Context, path string) ([]byte, error) {
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		body, err := os.ReadFile(path)
		if err == nil {
			if len(body) > 128 {
				return nil, errWorkerProtocol
			}
			return body, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// This controlled helper tests the actual kernel executable source without
// changing the parent test binary or claiming cgroup/native-worker acceptance.
func TestWorkerLinuxLoadedImageFixtureProcess(t *testing.T) {
	directory := os.Getenv(workerLoadedImageFixtureKey)
	if directory == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	approved, err := workerExpectedExecutableDigest()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "ready"), []byte(approved), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = workerLoadedImageWaitMarker(ctx, filepath.Join(directory, "continue")); err != nil {
		t.Fatal(err)
	}
	installer, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Establish that reopening Go's installer pathname actually reaches the
	// replacement now; otherwise this fixture would not exercise the old gap.
	naiveDigest, err := workerLoadedImageFixtureHash(installer)
	if err != nil || naiveDigest == approved {
		t.Fatal("installer replacement did not cross the controlled barrier", err)
	}
	actual, err := workerExecutableDigest()
	if err != nil || actual != approved {
		t.Fatal("approval no longer names the mapped image", err)
	}
	a, admission := workerRuntimeTestAttempt(t, ctx)
	a.bootstrap.Identity.BuildID = approved
	launch, err := workerProductionLaunch(a)
	if err != nil || launch.executable == installer {
		t.Fatal("production launch failed to snapshot the mapped image", err)
	}
	privateDigest, err := workerLoadedImageFixtureHash(launch.executable)
	if err != nil || privateDigest != approved {
		t.Fatal("private launch bytes differ from the actual mapped image", err)
	}
	if err = a.release(); err != nil || admission.releases.Load() != 1 {
		t.Fatal("snapshot descriptor/scratch ownership did not join once", err)
	}
	if err = os.WriteFile(filepath.Join(directory, "result"), []byte(actual+"\n"+privateDigest), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerLinuxApprovalAndSnapshotSurviveInstallerUnlinkAndReplacement(t *testing.T) {
	directory, err := os.MkdirTemp("", "apex-native-loaded-image-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	started, joined := false, false
	t.Cleanup(func() {
		if started && !joined {
			t.Log("retaining unjoined controlled fixture namespace", directory)
			return
		}
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	installer := filepath.Join(directory, "controller-installed-image")
	sourcePath, err := workerRunningExecutableSourcePath()
	if err != nil || sourcePath != "/proc/self/exe" {
		t.Fatal("Linux approval does not use kernel executable authority", err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(installer, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		_ = source.Close()
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, io.LimitReader(source, workerExecutableMaxBytes+1))
	if err = errors.Join(copyErr, output.Close(), source.Close()); err != nil {
		t.Fatal(err)
	}
	approved, err := workerLoadedImageFixtureHash(installer)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(installer, "-test.run=^TestWorkerLinuxLoadedImageFixtureProcess$")
	command.Env = append(os.Environ(), workerLoadedImageFixtureKey+"="+directory)
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	// One goroutine alone owns Wait. Fixture failures kill that exact process and
	// join the same waiter before TempDir cleanup can remove its source files.
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	started = true
	defer func() {
		if joined {
			return
		}
		_ = command.Process.Kill()
		select {
		case <-finished:
			joined = true
		case <-time.After(5 * time.Second):
			t.Error("controlled loaded-image fixture did not physically join")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ready, err := workerLoadedImageWaitMarker(ctx, filepath.Join(directory, "ready"))
	if err != nil || string(ready) != approved {
		t.Fatal("controlled fixture did not approve original bytes", err)
	}
	if err = os.Remove(installer); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(installer, []byte("replacement is not the mapped executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "continue"), []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-finished:
		joined = true
		if err != nil {
			t.Fatal("mapped-image fixture failed", err)
		}
	case <-ctx.Done():
		t.Fatal("mapped-image fixture exceeded its controlled deadline")
	}
	result, err := os.ReadFile(filepath.Join(directory, "result"))
	if err != nil || string(result) != approved+"\n"+approved {
		t.Fatal("approval/snapshot result changed after installer replacement", err)
	}
}
