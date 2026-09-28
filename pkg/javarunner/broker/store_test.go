package broker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

var testConfig = Config{Namespace: "java-test", Budget: javarunner.HostBudget{MaxHosts: 2, MaxVUsers: 2, MaxMemoryBytes: 200}}

func newStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "broker.db")
	s, err := Open(t.Context(), path, testConfig, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	epoch, err := s.StartEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return s, epoch, path
}
func reserve(t *testing.T, s *Store, epoch string) Record {
	t.Helper()
	r, err := s.Reserve(t.Context(), epoch, binding(), 100, time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func binding() javarunner.HostBinding {
	return javarunner.HostBinding{OwnerID: "generator", OwnerEpoch: "owner-1", ExecutionID: "run", GroupID: "group", ConfigurationSHA256: strings.Repeat("a", 64)}
}
func ready(t *testing.T, s *Store, epoch string, r Record) {
	t.Helper()
	for _, err := range []error{s.BeginPrepare(t.Context(), epoch, r.ID), s.BindPod(t.Context(), epoch, r.ID, "uid-"+r.ID, "containerd://"+r.ID), s.Ready(t.Context(), epoch, r.ID)} {
		if err != nil {
			t.Fatal(err)
		}
	}
}
func record(t *testing.T, s *Store, epoch, id string) Record {
	t.Helper()
	rs, _, err := s.Snapshot(t.Context(), epoch)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		if r.ID == id {
			return r
		}
	}
	t.Fatal("record missing")
	return Record{}
}

func TestBarrierAndSeparateOccupancy(t *testing.T) {
	s, epoch, _ := newStore(t)
	a, b := reserve(t, s, epoch), reserve(t, s, epoch)
	ready(t, s, epoch, a)
	if err := s.Commit(t.Context(), epoch, []string{a.ID}); !errors.Is(err, ErrState) {
		t.Fatal("pending host omitted from barrier", err)
	}
	if err := s.Commit(t.Context(), epoch, []string{a.ID, b.ID}); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if err := s.CanInvoke(t.Context(), epoch, a.ID); !errors.Is(err, ErrFence) {
		t.Fatal("partial commit", err)
	}
	ready(t, s, epoch, b)
	if err := s.Commit(t.Context(), epoch, []string{a.ID, a.ID}); !errors.Is(err, ErrState) {
		t.Fatal("duplicate", err)
	}
	if err := s.Commit(t.Context(), epoch, []string{a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.CanInvoke(t.Context(), epoch, a.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.CloseVUser(t.Context(), epoch, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	_, u, err := s.Snapshot(t.Context(), epoch)
	if err != nil || u.Hosts != 2 || u.VUsers != 1 || u.MemoryBytes != 200 {
		t.Fatalf("early host release: %+v %v", u, err)
	}
	if _, err := s.Reserve(t.Context(), epoch, binding(), 100, time.Minute, time.Minute); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if err := s.ConfirmTerminated(t.Context(), epoch, a.ID, "replacement-uid", "containerd://"+a.ID); !errors.Is(err, ErrCleanup) {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.ConfirmTerminated(t.Context(), epoch, a.ID, "uid-"+a.ID, "containerd://"+a.ID); err != nil {
			t.Fatal(err)
		}
	}
	_, u, err = s.Snapshot(t.Context(), epoch)
	if err != nil || u.Hosts != 1 || u.VUsers != 1 || u.MemoryBytes != 100 {
		t.Fatalf("release not exact: %+v %v", u, err)
	}
	if err := s.CanInvoke(t.Context(), epoch, a.ID); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
}

func TestRestartRetainsQuarantineAndFencesOldBroker(t *testing.T) {
	s, old, path := newStore(t)
	a := reserve(t, s, old)
	bindingB := binding()
	bindingB.GroupID = "other-group"
	b, err := s.Reserve(t.Context(), old, bindingB, 100, time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ready(t, s, old, a)
	if err := s.Commit(t.Context(), old, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	// Another connection models a restarted broker; the prior connection is
	// deliberately left live to exercise database-level fencing.
	restarted, err := Open(t.Context(), path, testConfig, false)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	epoch, err := restarted.StartEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if epoch == old {
		t.Fatal("epoch reused")
	}
	for _, err := range []error{s.CanInvoke(t.Context(), old, a.ID), s.BeginPrepare(t.Context(), old, b.ID), s.Renew(t.Context(), old, a.ID, time.Minute), s.Abort(t.Context(), old, a.ID, "abort"), s.ConfirmTerminated(t.Context(), old, a.ID, "uid-"+a.ID, "containerd://"+a.ID)} {
		if !errors.Is(err, ErrFence) {
			t.Fatal("old broker not fenced", err)
		}
	}
	if r := record(t, restarted, epoch, b.ID); r.State != javarunner.HostReaped {
		t.Fatal("never-started reservation retained", r)
	}
	if r := record(t, restarted, epoch, a.ID); r.State != javarunner.HostQuarantined || r.Failure != "broker_epoch_changed" {
		t.Fatal(r)
	}
	if _, err := restarted.Reserve(t.Context(), epoch, binding(), 100, time.Minute, time.Minute); !errors.Is(err, ErrCleanup) {
		t.Fatal("owner admitted before cleanup", err)
	}
	if err := restarted.ConfirmTerminated(t.Context(), epoch, a.ID, "uid-"+a.ID, "containerd://"+a.ID); err != nil {
		t.Fatal(err)
	}
	reserve(t, restarted, epoch)
}

func TestUnknownCreationOutcomeNeverReleases(t *testing.T) {
	s, epoch, path := newStore(t)
	r := reserve(t, s, epoch)
	if err := s.BeginPrepare(t.Context(), epoch, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Abort(t.Context(), epoch, r.ID, "creation_timeout"); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfirmTerminated(t.Context(), epoch, r.ID, "", ""); !errors.Is(err, ErrCleanup) {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), path, testConfig, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	epoch, err = s.StartEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, used, err := s.Snapshot(t.Context(), epoch)
	if err != nil || used.Hosts != 1 || used.MemoryBytes != 100 {
		t.Fatal("lost durable quarantine", used, err)
	}
	// A delayed, exact receipt enriches cleanup identity, never readiness.
	if err := s.BindPod(t.Context(), epoch, r.ID, "late-uid", "containerd://late"); err != nil {
		t.Fatal(err)
	}
	if err := s.Ready(t.Context(), epoch, r.ID); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if err := s.BindPod(t.Context(), epoch, r.ID, "replacement-uid", "containerd://late"); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if err := s.ConfirmTerminated(t.Context(), epoch, r.ID, "late-uid", "containerd://other"); !errors.Is(err, ErrCleanup) {
		t.Fatal(err)
	}
	if r := record(t, s, epoch, r.ID); r.Failure != "creation_timeout" {
		t.Fatal("trigger overwritten", r)
	}
	if err := s.ConfirmTerminated(t.Context(), epoch, r.ID, "late-uid", "containerd://late"); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredPreparationAndLeaseCannotRevive(t *testing.T) {
	for _, stage := range []string{"reserved", "preparing", "ready", "committed"} {
		t.Run(stage, func(t *testing.T) {
			s, epoch, _ := newStore(t)
			now := time.Now()
			s.now = func() time.Time { return now }
			r, err := s.Reserve(t.Context(), epoch, binding(), 100, time.Second, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if stage != "reserved" {
				if err := s.BeginPrepare(t.Context(), epoch, r.ID); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "ready" || stage == "committed" {
				if err := s.BindPod(t.Context(), epoch, r.ID, "uid", "containerd://id"); err != nil {
					t.Fatal(err)
				}
				if err := s.Ready(t.Context(), epoch, r.ID); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "committed" {
				if err := s.Commit(t.Context(), epoch, []string{r.ID}); err != nil {
					t.Fatal(err)
				}
			}
			now = now.Add(time.Second)
			if err := s.Renew(t.Context(), epoch, r.ID, time.Minute); !errors.Is(err, ErrFence) {
				t.Fatal("lease revived", err)
			}
			if err := s.Commit(t.Context(), epoch, []string{r.ID}); !errors.Is(err, ErrState) {
				t.Fatal(err)
			}
			want := javarunner.HostQuarantined
			if stage == "reserved" {
				want = javarunner.HostReaped
			}
			if r := record(t, s, epoch, r.ID); r.State != want || r.Failure != "lease_expired" {
				t.Fatal(r)
			}
		})
	}
}

func TestPrepareDeadlineIndependentOfRenewal(t *testing.T) {
	s, epoch, _ := newStore(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	r, err := s.Reserve(t.Context(), epoch, binding(), 100, time.Minute, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ready(t, s, epoch, r)
	now = now.Add(time.Second)
	if err := s.Renew(t.Context(), epoch, r.ID, time.Minute); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
	if r := record(t, s, epoch, r.ID); r.Failure != "preparation_expired" {
		t.Fatal(r)
	}
}

func TestConcurrentAuthoritiesDoNotOverbook(t *testing.T) {
	s, epoch, path := newStore(t)
	other, err := Open(t.Context(), path, testConfig, false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store := s
			if i%2 == 0 {
				store = other
			}
			_, err := store.Reserve(t.Context(), epoch, binding(), 100, time.Minute, time.Minute)
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrCapacity) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if accepted.Load() != 2 {
		t.Fatal("over/under-booked", accepted.Load())
	}
	_, u, err := other.Snapshot(t.Context(), epoch)
	if err != nil || u.Hosts != 2 || u.MemoryBytes != 200 {
		t.Fatal(u, err)
	}
}

func TestStoreRecoveryFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if _, err := Open(t.Context(), path, testConfig, false); err == nil {
		t.Fatal("missing store initialized")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("recovery created a file", err)
	}
	s, _, path := newStore(t)
	if _, err := Open(t.Context(), path, testConfig, true); err == nil {
		t.Fatal("existing store replaced")
	}
	changed := testConfig
	changed.Budget.MaxHosts++
	if reopened, err := Open(t.Context(), path, changed, false); err == nil {
		reopened.Close()
		t.Fatal("budget change accepted")
	}
	if _, err := s.db.ExecContext(t.Context(), "UPDATE java_broker_state SET document='{}'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartEpoch(t.Context()); !errors.Is(err, ErrState) {
		t.Fatal("corrupt state accepted", err)
	}
	if reopened, err := Open(t.Context(), path, testConfig, false); err == nil {
		reopened.Close()
		t.Fatal("corruption erased")
	}
}

func TestInvalidInputsAndCancelledMutation(t *testing.T) {
	s, epoch, _ := newStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Reserve(ctx, epoch, binding(), 100, time.Minute, time.Minute); err == nil {
		t.Fatal("cancelled mutation succeeded")
	}
	for _, memory := range []int64{0, -1, 201} {
		if _, err := s.Reserve(t.Context(), epoch, binding(), memory, time.Minute, time.Minute); err == nil {
			t.Fatal(memory)
		}
	}
	if _, err := s.Reserve(t.Context(), "", binding(), 100, time.Minute, time.Minute); !errors.Is(err, ErrFence) {
		t.Fatal(err)
	}
	if err := s.Commit(t.Context(), epoch, nil); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if err := s.BindPod(t.Context(), epoch, "absent", "uid", "not-a-container-id"); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	_, u, err := s.Snapshot(t.Context(), epoch)
	if err != nil || u.Hosts != 0 {
		t.Fatal(u, err)
	}
}

func TestDurableStateAfterAbruptExit(t *testing.T) {
	const childPath = "APEX_TEST_JAVA_BROKER_CRASH_DB"
	if path := os.Getenv(childPath); path != "" {
		s, err := Open(context.Background(), path, testConfig, false)
		if err != nil {
			os.Exit(31)
		}
		epoch, err := s.StartEpoch(context.Background())
		if err != nil {
			os.Exit(32)
		}
		host, err := s.Reserve(context.Background(), epoch, binding(), 100, time.Minute, time.Minute)
		if err != nil {
			os.Exit(33)
		}
		if err := s.BeginPrepare(context.Background(), epoch, host.ID); err != nil {
			os.Exit(34)
		}
		// No Close/checkpoint/deferred cleanup: model a broker process dying
		// immediately after a durable provisioning intent was acknowledged.
		os.Exit(23)
	}
	s, _, path := newStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDurableStateAfterAbruptExit$")
	cmd.Env = append(os.Environ(), childPath+"="+path)
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 23 {
		t.Fatalf("crash helper: %v", err)
	}
	restarted, err := Open(t.Context(), path, testConfig, false)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	epoch, err := restarted.StartEpoch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	records, u, err := restarted.Snapshot(t.Context(), epoch)
	if err != nil || len(records) != 1 || u.Hosts != 1 || u.MemoryBytes != 100 || records[0].State != javarunner.HostQuarantined {
		t.Fatalf("lost intent on abrupt exit: %+v %+v %v", records, u, err)
	}
}

func TestFailedDurableWriteNeverReturnsProvisioningPermission(t *testing.T) {
	s, epoch, _ := newStore(t)
	if _, err := s.db.ExecContext(t.Context(), "PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	r, err := s.Reserve(t.Context(), epoch, binding(), 100, time.Minute, time.Minute)
	if err == nil || r.ID != "" {
		t.Fatalf("uncommitted permission escaped: %+v %v", r, err)
	}
	if _, err := s.db.ExecContext(t.Context(), "PRAGMA query_only=OFF"); err != nil {
		t.Fatal(err)
	}
	records, u, err := s.Snapshot(t.Context(), epoch)
	if err != nil || len(records) != 0 || u.Hosts != 0 {
		t.Fatal(records, u, err)
	}
}

func TestAbortWaveRetainsEarlierCommittedHosts(t *testing.T) {
	s, epoch, _ := newStore(t)
	a := reserve(t, s, epoch)
	ready(t, s, epoch, a)
	if e := s.Commit(t.Context(), epoch, []string{a.ID}); e != nil {
		t.Fatal(e)
	}
	b := reserve(t, s, epoch)
	if e := s.AbortWave(t.Context(), epoch, binding(), nil, "preparation_failed"); e != nil {
		t.Fatal(e)
	}
	if r := record(t, s, epoch, a.ID); r.State != javarunner.HostCommitted {
		t.Fatal("earlier ramp wave interrupted", r)
	}
	if r := record(t, s, epoch, b.ID); r.State != javarunner.HostReaped {
		t.Fatal("unprovisioned reservation leaked", r)
	}
	if e := s.AbortWave(t.Context(), epoch, binding(), []string{a.ID, a.ID}, "invalid"); !errors.Is(e, ErrState) {
		t.Fatal(e)
	}
}
