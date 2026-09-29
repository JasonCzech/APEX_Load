package archivequota

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

func fixture(t *testing.T, maximum int64) (string, *os.Root, *Budget) {
	t.Helper()
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	b, err := New(root, Options{MaxBytes: maximum})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(context.Background()) })
	return path, root, b
}
func output(t *testing.T, root *os.Root, name string) *os.File {
	t.Helper()
	f, err := root.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestBudgetSmallQuotaAndRetainedData(t *testing.T) {
	_, root, b := fixture(t, 10)
	f := output(t, root, "first")
	if n, err := b.Write(context.Background(), f, []byte("123456")); n != 6 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := b.Write(context.Background(), f, []byte("7890x")); n != 4 || !errors.Is(err, ErrCapacity) {
		t.Fatal(n, err)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	next, err := New(root, Options{MaxBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close(context.Background())
	if n, err := next.Write(context.Background(), output(t, root, "second"), []byte("x")); n != 0 || !errors.Is(err, ErrCapacity) {
		t.Fatal("retained data was not charged", n, err)
	}
	data, err := root.ReadFile("first")
	if err != nil || string(data) != "1234567890" {
		t.Fatal(string(data), err)
	}
}
func TestBudgetIncrementalRefillAndExclusions(t *testing.T) {
	_, root, b := fixture(t, 10<<20)
	f := output(t, root, "large")
	data := bytes.Repeat([]byte("x"), 9<<20)
	if n, err := b.Write(context.Background(), f, data); n != len(data) || err != nil {
		t.Fatal(n, err)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := root.Mkdir("separate", 0700); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("separate/reader", data, 0600); err != nil {
		t.Fatal(err)
	}
	excluded, err := New(root, Options{MaxBytes: 10 << 20, Exclude: []string{"separate"}})
	if err != nil {
		t.Fatal(err)
	}
	defer excluded.Close(context.Background())
	if n, err := excluded.Write(context.Background(), output(t, root, "remainder"), []byte("x")); n != 1 || err != nil {
		t.Fatal(n, err)
	}
}
func TestBudgetUnknownPayloadAndLowerQuota(t *testing.T) {
	_, root, b := fixture(t, 100)
	if err := root.WriteFile("unknown", bytes.Repeat([]byte("x"), 90), 0600); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Write(context.Background(), output(t, root, "new"), bytes.Repeat([]byte("x"), 20)); n != 10 || !errors.Is(err, ErrCapacity) {
		t.Fatal(n, err)
	}
	b.Close(context.Background())
	lower, err := New(root, Options{MaxBytes: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer lower.Close(context.Background())
	if n, err := lower.Write(context.Background(), output(t, root, "lower"), []byte("x")); n != 0 || !errors.Is(err, ErrCapacity) {
		t.Fatal(n, err)
	}
	info, err := root.Stat("unknown")
	if err != nil || info.Size() != 90 {
		t.Fatal("unknown data was pruned", err)
	}
}
func TestBudgetRejectsLinksAndDeepInventory(t *testing.T) {
	for _, mode := range []string{"link", "depth", "ledger"} {
		t.Run(mode, func(t *testing.T) {
			path, root, b := fixture(t, 100)
			switch mode {
			case "link":
				if err := os.Symlink(t.TempDir(), filepath.Join(path, "linked")); err != nil {
					t.Fatal(err)
				}
			case "depth":
				name := ""
				for range 18 {
					name = filepath.Join(name, "deep")
					if err := root.Mkdir(name, 0700); err != nil {
						t.Fatal(err)
					}
				}
			case "ledger":
				if err := root.WriteFile(namespace+"/unknown", []byte("1\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if n, err := b.Write(context.Background(), output(t, root, "output"), []byte("x")); n != 0 || err == nil {
				t.Fatal("unsafe inventory accepted", n, err)
			}
		})
	}
}
func TestBudgetReservationReplacementIsPreserved(t *testing.T) {
	_, root, b := fixture(t, 100)
	f := output(t, root, "data")
	if _, err := b.Write(context.Background(), f, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := b.ledger.Rename(b.token, "old-"+b.token); err != nil {
		t.Fatal(err)
	}
	if err := b.ledger.WriteFile(b.token, []byte("50\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Write(context.Background(), f, []byte("y")); n != 0 || err == nil {
		t.Fatal("replaced reservation accepted", n, err)
	}
	if err := b.Close(context.Background()); err == nil {
		t.Fatal("replaced reservation deleted")
	}
	data, err := root.ReadFile(namespace + "/" + b.token)
	if err != nil || string(data) != "50\n" {
		t.Fatal("replacement was changed", err)
	}
}
func TestBudgetLedgerReplacementRejectsRemainingCapacity(t *testing.T) {
	_, root, b := fixture(t, 100)
	f := output(t, root, "data")
	if _, err := b.Write(context.Background(), f, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := root.Rename(namespace, "old-ledger"); err != nil {
		t.Fatal(err)
	}
	if err := root.Mkdir(namespace, 0700); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Write(context.Background(), f, []byte("y")); n != 0 || err == nil {
		t.Fatal(n, err)
	}
	if err := b.Close(context.Background()); err == nil {
		t.Fatal("ledger replacement ignored")
	}
	if _, err := root.Stat("old-ledger/" + b.token); err != nil {
		t.Fatal("old reservation removed", err)
	}
}
func TestBudgetConcurrentWritersStayWithinQuota(t *testing.T) {
	_, root, _ := fixture(t, 8192)
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for i := range 8 {
		b, err := New(root, Options{MaxBytes: 8192})
		if err != nil {
			t.Fatal(err)
		}
		f := output(t, root, strconv.Itoa(i))
		wg.Go(func() {
			defer b.Close(context.Background())
			n, err := b.Write(context.Background(), f, bytes.Repeat([]byte("x"), 2048))
			accepted.Add(int64(n))
			if err != nil && !errors.Is(err, ErrCapacity) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if accepted.Load() <= 0 || accepted.Load() > 8192 {
		t.Fatal(accepted.Load())
	}
}
func TestBudgetCanceledAndShortWritesLatch(t *testing.T) {
	for _, mode := range []string{"cancel", "short"} {
		t.Run(mode, func(t *testing.T) {
			_, _, b := fixture(t, 100)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var writer io.Writer = io.Discard
			expected := context.Canceled
			if mode == "cancel" {
				cancel()
			} else {
				writer = shortWriter{}
				expected = io.ErrShortWrite
			}
			if _, err := b.Write(ctx, writer, []byte("abc")); !errors.Is(err, expected) {
				t.Fatal(err)
			}
			if n, err := b.Write(context.Background(), io.Discard, []byte("abc")); n != 0 || !errors.Is(err, expected) {
				t.Fatal("failed write not latched", n, err)
			}
		})
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestBudgetProcess(t *testing.T) {
	path := os.Getenv("APEX_QUOTA_TEST_ROOT")
	if path == "" {
		t.Skip("subprocess helper")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(root, Options{MaxBytes: 128})
	if err != nil {
		t.Fatal(err)
	}
	f := output(t, root, os.Getenv("APEX_QUOTA_TEST_FILE"))
	count := 16
	if raw := os.Getenv("APEX_QUOTA_TEST_BYTES"); raw != "" {
		count, err = strconv.Atoi(raw)
		if err != nil {
			t.Fatal(err)
		}
	}
	n, err := b.Write(context.Background(), f, bytes.Repeat([]byte("x"), count))
	if errors.Is(err, ErrCapacity) {
		os.Exit(3)
	}
	if err != nil || n != count {
		t.Fatal(n, err)
	}
	if os.Getenv("APEX_QUOTA_TEST_CRASH") == "1" {
		os.Exit(0)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestBudgetCrashReservationsSurviveProcessExit(t *testing.T) {
	path, root, _ := fixture(t, 128)
	cmd := exec.Command(os.Args[0], "-test.run=^TestBudgetProcess$")
	cmd.Env = append(os.Environ(), "APEX_QUOTA_TEST_ROOT="+path, "APEX_QUOTA_TEST_FILE=crashed", "APEX_QUOTA_TEST_CRASH=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %s %v", out, err)
	}
	b, err := New(root, Options{MaxBytes: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	if n, err := b.Write(context.Background(), output(t, root, "later"), []byte("x")); n != 0 || !errors.Is(err, ErrCapacity) {
		t.Fatal("crash reservation was forgiven", n, err)
	}
	info, err := root.Stat("crashed")
	if err != nil || info.Size() != 16 {
		t.Fatal(err)
	}
}
func TestBudgetConfiguration(t *testing.T) {
	_, root, _ := fixture(t, 100)
	for _, options := range []Options{{MaxBytes: 0}, {MaxBytes: -1}, {MaxBytes: 1<<50 + 1}, {MaxBytes: 1, Exclude: []string{".."}}, {MaxBytes: 1, Exclude: []string{namespace}}} {
		if _, err := New(root, options); err == nil {
			t.Fatal("invalid config accepted", options)
		}
	}
}

func TestBudgetAcrossProcessesCannotOverspend(t *testing.T) {
	path, root, _ := fixture(t, 128)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"one", "two"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestBudgetProcess$")
		cmd.Env = append(os.Environ(), "APEX_QUOTA_TEST_ROOT="+path, "APEX_QUOTA_TEST_FILE="+name, "APEX_QUOTA_TEST_BYTES=100", "APEX_QUOTA_TEST_CRASH=0")
		wg.Go(func() {
			out, err := cmd.CombinedOutput()
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 3 {
					t.Errorf("helper: %s %v", out, err)
				}
			}
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	var used int64
	for _, name := range []string{"one", "two"} {
		info, err := root.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		used += info.Size()
	}
	if successes != 1 || used < 100 || used > 128 {
		t.Fatal("processes overcommitted or no writer advanced", successes, used)
	}
}

func TestBudgetInventoryEntryBound(t *testing.T) {
	// Empty files exercise traversal work without allocating payload storage.
	_, root, b := fixture(t, 1)
	for i := range maxEntries {
		f, err := root.OpenFile(strconv.Itoa(i), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := b.Write(context.Background(), io.Discard, []byte("x")); n != 0 || err == nil {
		t.Fatal("unbounded inventory accepted", n, err)
	}
}

func TestBudgetLedgerCaseAliasStillChargesReservations(t *testing.T) {
	_, root, b := fixture(t, 128)
	alias := ".APEXLOAD-BACKLOG"
	if err := root.Rename(namespace, alias); err != nil {
		t.Fatal(err)
	}
	canonical, err := root.Lstat(namespace)
	if os.IsNotExist(err) {
		t.Skip("filesystem has case-sensitive directory names")
	}
	actual, statErr := root.Lstat(alias)
	if err != nil || statErr != nil || !os.SameFile(canonical, actual) {
		t.Fatal("case alias fixture", err, statErr)
	}
	if _, err := b.Write(context.Background(), output(t, root, "first"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	other, err := New(root, Options{MaxBytes: 128})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(context.Background())
	if n, err := other.Write(context.Background(), output(t, root, "second"), []byte("x")); n != 0 || !errors.Is(err, ErrCapacity) {
		t.Fatal("case alias hid an active reservation", n, err)
	}
}
