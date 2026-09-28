package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/diskspace"
)

func transferCapacitySandbox(t *testing.T, max int64) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv(envArchiveTransferDir, root)
	t.Setenv(envArchiveTransferMaxBytes, strconv.FormatInt(max, 10))
	t.Setenv(envArchiveTransferMinFreeBytes, "1")
	return root
}

func TestArchiveTransferCapacityConcurrentAdmissionAndRelease(t *testing.T) {
	root := transferCapacitySandbox(t, 100)
	var wg sync.WaitGroup
	admitted := make(chan *archiveTransferLease, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := reserveArchiveTransfer(context.Background(), 30)
			if err == nil {
				admitted <- l
			}
		}()
	}
	wg.Wait()
	close(admitted)
	var leases []*archiveTransferLease
	for l := range admitted {
		leases = append(leases, l)
	}
	if len(leases) != 3 {
		t.Fatalf("admitted %d, want 3", len(leases))
	}
	used, err := archiveTransferUsage(context.Background(), root, 100)
	if err != nil || used != 90 {
		t.Fatalf("usage %d: %v", used, err)
	}
	for _, l := range leases {
		if err := l.close(); err != nil {
			t.Fatal(err)
		}
		if err := l.close(); err != nil {
			t.Fatal(err)
		}
	}
	used, err = archiveTransferUsage(context.Background(), root, 100)
	if err != nil || used != 0 {
		t.Fatalf("released usage %d: %v", used, err)
	}
}

func TestArchiveTransferCapacityPreservesUnknownAndLoweredQuota(t *testing.T) {
	root := transferCapacitySandbox(t, 100)
	orphan := filepath.Join(root, "unknown")
	if err := os.WriteFile(orphan, make([]byte, 31), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reserveArchiveTransfer(context.Background(), 70); err == nil {
		t.Fatal("unknown evidence was not charged")
	}
	l, err := reserveArchiveTransfer(context.Background(), 60)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envArchiveTransferMaxBytes, "20")
	if _, err := reserveArchiveTransfer(context.Background(), 1); err == nil {
		t.Fatal("lowered quota admitted work")
	}
	if _, err := os.Stat(l.path); err != nil {
		t.Fatal("reservation removed", err)
	}
	if err := l.close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(orphan); err != nil || len(data) != 31 {
		t.Fatal("unknown evidence removed")
	}
}

func TestArchiveTransferCapacityRejectsUnsafeInventory(t *testing.T) {
	for _, mode := range []string{"malformed", "symlink", "oversized", "depth", "payload-exceeds-reservation"} {
		t.Run(mode, func(t *testing.T) {
			root := transferCapacitySandbox(t, 100)
			dir := filepath.Join(root, "leftover")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(dir, archiveTransferReservation)
			var err error
			switch mode {
			case "malformed":
				err = os.WriteFile(marker, []byte("+80\n"), 0o600)
			case "symlink":
				err = os.Symlink(t.TempDir(), marker)
			case "oversized":
				err = os.WriteFile(marker, []byte(strings.Repeat("9", 40)), 0o600)
			case "depth":
				err = os.MkdirAll(filepath.Join(dir, "a/b/c/d/e/f/g/h"), 0o700)
			case "payload-exceeds-reservation":
				if err := os.WriteFile(marker, []byte("1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				err = os.WriteFile(filepath.Join(dir, "payload"), make([]byte, 80), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reserveArchiveTransfer(context.Background(), 30); err == nil {
				t.Fatal("unsafe/over-limit inventory admitted")
			}
			if _, err := os.Lstat(dir); err != nil {
				t.Fatal("unknown evidence removed")
			}
		})
	}
}

func TestArchiveTransferCapacityCanceledFence(t *testing.T) {
	root := transferCapacitySandbox(t, 100)
	unlock, err := lockArchiveNamespaceAt(context.Background(), root, "_transfer_locks", "capacity.lock")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := reserveArchiveTransfer(ctx, 10); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("admission: %v", err)
	}
}

func TestArchiveTransferCapacityDoesNotRemoveReplacement(t *testing.T) {
	transferCapacitySandbox(t, 100)
	l, err := reserveArchiveTransfer(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(l.path, l.path+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(l.path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := l.close(); err == nil {
		t.Fatal("removed a replacement")
	}
	if _, err := os.Stat(l.path); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveTransferCapacityValidatesConfigurationAndHeadroom(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{envArchiveTransferMaxBytes, "0"}, {envArchiveTransferMaxBytes, "-1"}, {envArchiveTransferMaxBytes, "9223372036854775808"},
		{envArchiveTransferMinFreeBytes, "bad"}, {envArchiveTransferDir, "relative"},
	} {
		t.Run(tc.name+tc.value, func(t *testing.T) {
			transferCapacitySandbox(t, 100)
			t.Setenv(tc.name, tc.value)
			if _, err := reserveArchiveTransfer(context.Background(), 10); err == nil {
				t.Fatal("invalid configuration admitted")
			}
		})
	}
	root := transferCapacitySandbox(t, 100)
	free, err := diskspace.AvailablePath(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envArchiveTransferMinFreeBytes, strconv.FormatInt(free+1<<30, 10))
	if _, err := reserveArchiveTransfer(context.Background(), 10); err == nil {
		t.Fatal("insufficient headroom admitted")
	}
}

func TestArchiveTransferCapacityWriterHeadroomAndClosedLease(t *testing.T) {
	transferCapacitySandbox(t, 100)
	l, err := reserveArchiveTransfer(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	f, err := createArchiveArtifactExclusive(l.path, "payload", 0o600, false)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := archiveTransferFileWriter{ctx: context.Background(), lease: l, file: f}
	if n, err := w.Write([]byte("ok")); err != nil || n != 2 {
		t.Fatalf("write %d: %v", n, err)
	}
	free, err := diskspace.AvailableFile(f)
	if err != nil {
		t.Fatal(err)
	}
	l.minFree = free + 1<<30
	if n, err := w.Write([]byte("denied")); err == nil || n != 0 {
		t.Fatal("headroom bypassed")
	}
	if info, err := f.Stat(); err != nil || info.Size() != 2 {
		t.Fatal("denied write changed payload")
	}
	if err := l.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("closed")); err == nil {
		t.Fatal("closed reservation wrote")
	}
}

type transferCountWriter int

func (w transferCountWriter) Write([]byte) (int, error) { return int(w), nil }
func TestBoundedArchiveWriterRejectsInvalidAndShortCounts(t *testing.T) {
	for _, count := range []int{-1, 2, 5} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			budget := &remoteArchiveTransferBudget{remaining: 4}
			w := &boundedArchiveWriter{destination: transferCountWriter(count), remaining: 4, totalBudget: budget}
			n, err := w.Write([]byte("1234"))
			if err == nil {
				t.Fatal("bad writer count accepted")
			}
			if count == 2 {
				if n != 2 || !errors.Is(err, io.ErrShortWrite) || budget.remaining != 2 {
					t.Fatal(n, err, budget.remaining)
				}
			} else if n != 0 || budget.remaining != 0 {
				t.Fatal("invalid count refunded uncertain bytes")
			}
		})
	}
}

func TestArchiveTransferCapacityHeldUntilBundleCleanup(t *testing.T) {
	transferCapacitySandbox(t, remoteArchiveTotalMaxBytes)
	source := t.TempDir()
	testID := "transfer-held"
	manifest := RunArchiveManifest{TestID: testID, TransactionsFile: "t", ResourceSamplesFile: "r", IntervalSummariesFile: "s"}
	if err := writeIndentedJSONFile(filepath.Join(source, "manifest.json"), manifest); err != nil {
		t.Fatal(err)
	}
	artifacts := map[string]string{"manifest.json": filepath.Join(source, "manifest.json")}
	for _, name := range []string{"t", "r", "s"} {
		path := filepath.Join(source, name)
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		artifacts[name] = path
	}
	client := &archiveStreamingFakeClient{artifacts: artifacts}
	backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)
	run := &ManagedTest{ID: testID, remoteTarget: "generator-a", remoteClient: client}
	bundles, err := backend.fetchRemoteArchiveBundles(&TestRunner{}, run, true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupRemoteArchiveBundles(bundles)
	if _, err := reserveArchiveTransfer(context.Background(), 1); err == nil {
		t.Fatal("downloaded bundles released capacity before merge")
	}
	calls := client.streamCalls
	if _, err := backend.fetchRemoteArchiveBundles(&TestRunner{}, run, true); err == nil {
		t.Fatal("second transfer admitted")
	}
	if client.streamCalls != calls {
		t.Fatal("RPC issued before admission")
	}
	cleanupRemoteArchiveBundles(bundles)
	l, err := reserveArchiveTransfer(context.Background(), remoteArchiveTotalMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer l.close()
}

func TestArchiveTransferCapacityFailureReleasesReservation(t *testing.T) {
	root := transferCapacitySandbox(t, remoteArchiveTotalMaxBytes)
	client := &archiveStreamingFakeClient{artifacts: map[string]string{}}
	backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)
	_, err := backend.fetchRemoteArchiveBundles(&TestRunner{}, &ManagedTest{ID: "failed-transfer", remoteTarget: "generator-a", remoteClient: client}, true)
	if err == nil {
		t.Fatal("missing source accepted")
	}
	used, err := archiveTransferUsage(context.Background(), root, remoteArchiveTotalMaxBytes)
	if err != nil || used != 0 {
		t.Fatal("failed transfer leaked reservation", used, err)
	}
}

func TestArchiveTransferCapacityCrashReservation(t *testing.T) {
	root := transferCapacitySandbox(t, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArchiveTransferCapacityProcessHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "APEXLOAD_TEST_TRANSFER_CHILD=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "reserved" {
		t.Fatal("child did not reserve capacity")
	}
	if _, err := reserveArchiveTransfer(context.Background(), 30); err == nil {
		t.Fatal("live process reservation ignored")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	waited = true
	if _, err := reserveArchiveTransfer(context.Background(), 30); err == nil {
		t.Fatal("crash reservation was expired")
	}
	used, err := archiveTransferUsage(context.Background(), root, 100)
	if err != nil || used != 80 {
		t.Fatal("crash evidence removed", used, err)
	}
}
func TestArchiveTransferCapacityProcessHelper(t *testing.T) {
	if os.Getenv("APEXLOAD_TEST_TRANSFER_CHILD") != "1" {
		return
	}
	if _, err := reserveArchiveTransfer(context.Background(), 80); err != nil {
		t.Fatal(err)
	}
	fmt.Println("reserved")
	time.Sleep(time.Minute)
}

func TestArchiveTransferDirectorySync(t *testing.T) {
	root := t.TempDir()
	if err := syncArchiveTransferDirectory(root); err != nil {
		t.Fatal(err)
	}
	if err := syncArchiveTransferDirectory(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing directory sync: %v", err)
	}
}
