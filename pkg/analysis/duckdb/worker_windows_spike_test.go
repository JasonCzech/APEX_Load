//go:build windows && analytics_duckdb && cgo

package duckdb

// TEMPORARY diagnostic for the Windows desktop lane's native analytics
// failures: times what one native attempt does on Windows (copy, sync, launch
// and delete the worker executable snapshot) and how long an engine stays
// busy after an operation returns. Runs only with
// APEXLOAD_NATIVE_WINDOWS_SPIKE=1 and prints "SPIKE <key>: <value>" lines.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func nativeSpikeEnabled(t *testing.T) {
	if os.Getenv("APEXLOAD_NATIVE_WINDOWS_SPIKE") != "1" {
		t.Skip("native Windows spike runs only with APEXLOAD_NATIVE_WINDOWS_SPIKE=1")
	}
}

func nativeSpikef(t *testing.T, key, format string, args ...any) {
	t.Helper()
	t.Logf("SPIKE %s: %s", key, fmt.Sprintf(format, args...))
}

func nativeSpikeErr(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return fmt.Sprintf("%v (errno %d)", err, uint32(errno))
	}
	if err == nil {
		return "ok"
	}
	return err.Error()
}

// removeUntilGone retries RemoveAll every 10 ms for up to 60 s and reports
// how long removal took, how many attempts failed and the last error.
func removeUntilGone(path string) (time.Duration, int, error) {
	start := time.Now()
	failures := 0
	var last error
	for time.Since(start) < 60*time.Second {
		if err := os.RemoveAll(path); err == nil {
			return time.Since(start), failures, last
		} else {
			failures++
			last = err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return time.Since(start), failures, last
}

func TestWindowsNativeWorkerSpikeExecutableLifecycle(t *testing.T) {
	nativeSpikeEnabled(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	nativeSpikef(t, "executable", "%s %d MiB", filepath.Base(exe), info.Size()>>20)
	copyExe := func(dir string) (string, time.Duration, error) {
		start := time.Now()
		target := filepath.Join(dir, "approved-native-worker.exe")
		in, err := os.Open(exe)
		if err != nil {
			return "", 0, err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o500)
		if err != nil {
			return "", 0, err
		}
		_, err = io.Copy(out, in)
		err = errors.Join(err, out.Sync(), out.Close())
		return target, time.Since(start), err
	}
	run := func(path string) (time.Duration, error) {
		start := time.Now()
		err := exec.Command(path, "-test.run=^$").Run()
		return time.Since(start), err
	}
	// A: per-attempt copy, launch the copy, delete the copy (today's pattern).
	for i := range 6 {
		dir, err := os.MkdirTemp("", "native-spike-")
		if err != nil {
			t.Fatal(err)
		}
		target, copyTime, err := copyExe(dir)
		if err != nil {
			t.Fatal(err)
		}
		runTime, runErr := run(target)
		removeTime, failures, last := removeUntilGone(dir)
		nativeSpikef(t, "a-copy-run-delete", "%d: copy+sync=%s run=%s (%s) delete=%s after %d failures, last=%s", i, copyTime.Round(time.Millisecond), runTime.Round(time.Millisecond), nativeSpikeErr(runErr), removeTime.Round(time.Millisecond), failures, nativeSpikeErr(last))
	}
	// B: copy without launching, then delete: is a written, unexecuted
	// executable also held?
	for i := range 3 {
		dir, err := os.MkdirTemp("", "native-spike-")
		if err != nil {
			t.Fatal(err)
		}
		_, copyTime, err := copyExe(dir)
		if err != nil {
			t.Fatal(err)
		}
		removeTime, failures, last := removeUntilGone(dir)
		nativeSpikef(t, "b-copy-delete", "%d: copy+sync=%s delete=%s after %d failures, last=%s", i, copyTime.Round(time.Millisecond), removeTime.Round(time.Millisecond), failures, nativeSpikeErr(last))
	}
	// C: one retained copy launched repeatedly: does a cached scan make the
	// later launches cheap?
	dir, err := os.MkdirTemp("", "native-spike-")
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := copyExe(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		runTime, runErr := run(target)
		nativeSpikef(t, "c-reused-run", "%d: run=%s (%s)", i, runTime.Round(time.Millisecond), nativeSpikeErr(runErr))
	}
	removeTime, failures, last := removeUntilGone(dir)
	nativeSpikef(t, "c-reused-delete", "delete=%s after %d failures, last=%s", removeTime.Round(time.Millisecond), failures, nativeSpikeErr(last))
	// D: the original executable launched directly.
	for i := range 2 {
		runTime, runErr := run(exe)
		nativeSpikef(t, "d-original-run", "%d: run=%s (%s)", i, runTime.Round(time.Millisecond), nativeSpikeErr(runErr))
	}
}

// How long a real engine stays busy after each operation returns: the next
// operation is retried every 20 ms while it reports ErrBusy.
func TestWindowsNativeWorkerSpikeEngineBusyWindow(t *testing.T) {
	nativeSpikeEnabled(t)
	e, path := nativeFixture(t, "SELECT 1700000040+i AS ts, (100+i%17)::DOUBLE AS latency FROM range(360) t(i)")
	normalized, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"latency"}, Name: "latency", Kind: analysis.KindObservation, Unit: "ms"}}}})
	if err != nil {
		t.Fatal(err)
	}
	normalized = retainNativeFixtureOutputs(t, e, normalized)
	from := time.Unix(1700000040, 0).UTC()
	for i := range 8 {
		start := time.Now()
		busy := 0
		var points int
		for {
			result, err := e.Series(context.Background(), normalized.Files, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "latency", From: from, To: from.Add(6 * time.Minute), BucketSeconds: 60, Aggregation: "avg"})
			if errors.Is(err, ErrBusy) && time.Since(start) < 60*time.Second {
				busy++
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if err != nil {
				nativeSpikef(t, "engine-series", "%d: error after %s and %d busy retries: %v", i, time.Since(start).Round(time.Millisecond), busy, err)
				break
			}
			points = len(result)
			break
		}
		nativeSpikef(t, "engine-series", "%d: %d points in %s after %d busy retries", i, points, time.Since(start).Round(time.Millisecond), busy)
	}
}
