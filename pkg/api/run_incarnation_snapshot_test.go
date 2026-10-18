package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestSameStoredTestIdentityPreservesAdmissionTransitions(t *testing.T) {
	for _, tokens := range [][2]string{{"", ""}, {"", "new"}, {"new", ""}, {"same", "same"}, {"old", "new"}} {
		t.Run(tokens[0]+"/"+tokens[1], func(t *testing.T) {
			now := time.Now()
			live := &ManagedTest{ID: "run", CreatedAt: now, initialArchiveProvenance: &RunArchiveProvenance{RunIncarnation: tokens[0]}}
			row := &storage.TestRecord{ID: live.ID, StartTime: now, InitialProvenance: map[string]any{"run_incarnation": tokens[1]}}
			want := tokens[0] == "" || tokens[1] == "" || tokens[0] == tokens[1]
			if sameStoredTestIdentity(live, row) != want {
				t.Fatalf("identity compatibility %q -> %q should be %t", tokens[0], tokens[1], want)
			}
		})
	}
}

func TestFollowerSnapshotReplacesKnownRunIncarnation(t *testing.T) {
	for _, mode := range []string{"full reload", "single read", "queued replacement", "metrics with retained worker", "messaging with retained worker"} {
		t.Run(mode, func(t *testing.T) {
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			now := time.Now().UTC().Truncate(time.Microsecond)
			row := &storage.TestRecord{ID: "replaced-incarnation", Name: "prior", ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartTime: now.Add(-time.Minute), EndTime: &now, InitialProvenance: map[string]any{"run_incarnation": "prior"}}
			if err := store.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			priorPoint := &LiveMetrics{Timestamp: now, TotalTx: 991, MessagingProgress: &protocol.MessagingProgressSnapshot{Lifecycle: "prior"}}
			if err := store.SaveHistory(row.ID, now, priorPoint); err != nil {
				t.Fatal(err)
			}
			runner := NewTestRunner(1, nil, store, WithExecutionLeadership(newExecutionLeadership(store, "follower")))
			prior := runner.tests[row.ID]
			prior.History, prior.LiveMetrics = []*LiveMetrics{priorPoint}, priorPoint
			prior.Archive = &storage.RunArchiveRecord{TestID: row.ID, ArchivePath: "prior-archive"}
			runner.archives[row.ID] = prior.Archive
			// Warm the full-history cache before the same public identity is replaced.
			runner.attachStoredTerminalHistory(cloneManagedTestForReadLocked(prior))
			if mode == "metrics with retained worker" || mode == "messaging with retained worker" {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				prior.executionCtx, prior.cancel = ctx, cancel
			}
			if err := store.DeleteTest(row.ID); err != nil {
				t.Fatal(err)
			}
			row.Name = "replacement"
			row.InitialProvenance = map[string]any{"run_incarnation": "replacement"}
			if mode == "queued replacement" {
				row.Status, row.EndTime = "QUEUED", nil
			}
			if err := store.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			if row.Status == "COMPLETE" {
				if err := store.SaveHistory(row.ID, now, &LiveMetrics{Timestamp: now, TotalTx: 7}); err != nil {
					t.Fatal(err)
				}
			}
			runner.haTestRefresh.markFresh(time.Now())
			var got *ManagedTest
			var ok bool
			switch mode {
			case "full reload", "queued replacement":
				if err := runner.reloadStoredTests(); err != nil {
					t.Fatal(err)
				}
				got, ok = runner.GetTest(row.ID)
			case "metrics with retained worker":
				for _, limit := range []int{0, 10} {
					got, ok = runner.GetTestMetricsWindow(row.ID, nil, limit)
					if !ok || len(got.History) != 1 || got.History[0].TotalTx != 7 || got.LiveMetrics == nil || got.LiveMetrics.TotalTx != 7 {
						t.Fatalf("replacement metrics with limit %d: %+v", limit, got)
					}
					if got.LiveMetrics.ObservationState != "historical" || got.LiveMetrics.ObservationValidUntil != nil {
						t.Fatalf("terminal replacement lost historical projection: %+v", got.LiveMetrics)
					}
					future := now.Add(time.Minute)
					filtered, exists := runner.GetTestMetricsWindow(row.ID, &future, limit)
					if !exists || len(filtered.History) != 0 || filtered.LiveMetrics == nil || filtered.LiveMetrics.TotalTx != 7 {
						t.Fatalf("since filter lost current replacement metrics with limit %d: %+v", limit, filtered)
					}
					if filtered.LiveMetrics.ObservationState != "historical" || filtered.LiveMetrics.ObservationValidUntil != nil {
						t.Fatalf("since filter lost historical projection: %+v", filtered.LiveMetrics)
					}
				}
			case "messaging with retained worker":
				progress, _, exists := runner.latestMessagingProgress(row.ID)
				if !exists || progress != nil {
					t.Fatalf("replacement inherited prior messaging evidence: %+v", progress)
				}
				got, ok = runner.GetTest(row.ID)
			default:
				got, ok = runner.GetTest(row.ID)
			}
			if !ok || got.Name != "replacement" || managedTestRunIncarnation(got) != "replacement" || got.Archive != nil {
				t.Fatalf("replacement retained old identity/archive: %+v", got)
			}
			if row.Status == "COMPLETE" {
				if len(got.History) != 1 || got.History[0].TotalTx != 7 || got.LiveMetrics == nil || got.LiveMetrics.TotalTx != 7 {
					t.Fatalf("replacement retained old history/live metrics: history=%+v live=%+v", got.History, got.LiveMetrics)
				}
			} else if got.Status != "QUEUED" || len(got.History) != 0 || got.LiveMetrics != nil {
				t.Fatalf("queued replacement retained terminal state: %+v", got)
			}
			if prior.initialArchiveProvenance.RunIncarnation != "prior" {
				t.Fatal("refresh relabeled the old runtime with the replacement's identity")
			}
		})
	}
}

func TestStoredHistoryIdentityIncludesRunIncarnation(t *testing.T) {
	now := time.Now()
	old := &ManagedTest{ID: "run", CreatedAt: now, EndedAt: &now, initialArchiveProvenance: &RunArchiveProvenance{RunIncarnation: "old"}}
	next := *old
	next.initialArchiveProvenance = &RunArchiveProvenance{RunIncarnation: "next"}
	if storedTestHistoryCacheKey(old) == storedTestHistoryCacheKey(&next) || sameStoredTestHistoryIdentity(old, &next) {
		t.Fatal("different incarnations share detailed-history identity")
	}
}

func TestCIEvidenceRequiresMatchingRunIncarnation(t *testing.T) {
	for _, mode := range []string{"matching", "different", "legacy", "archive only", "run only", "conflicting projections"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			server, store := newCIContractTestServer(t, ciRunSourceTestPolicy())
			run := createTerminalCIContractTest(t, server, store, "COMPLETE", "")
			record := persistValidCIRunArchive(t, store, run, 2, true, false, nil)
			currentToken, archiveToken := "current", "current"
			switch mode {
			case "different":
				archiveToken = "prior"
			case "legacy":
				currentToken, archiveToken = "", ""
			case "archive only":
				currentToken = ""
			case "run only":
				archiveToken = ""
			}
			initial := &RunArchiveProvenance{RunIncarnation: currentToken, ProjectID: run.ProjectID, RequestedAt: run.CreatedAt}
			row, err := store.GetTest(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			row.InitialProvenance = initial
			if err := store.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			run.initialArchiveProvenance = initial
			server.runner.mu.Lock()
			server.runner.tests[run.ID].initialArchiveProvenance = initial
			server.runner.mu.Unlock()
			// Preserve ID/project/timestamps/status and all artifact integrity;
			// only the explicit incarnation can distinguish this stale archive.
			rewriteCIRunArchiveManifest(t, store, record, func(manifest *RunArchiveManifest) {
				manifest.Provenance.RunIncarnation = archiveToken
				original := manifest.Provenance
				manifest.InitialProvenance = &original
				if mode == "conflicting projections" {
					original.RunIncarnation = "another"
				}
			})
			evidence, err := server.validatedCIRunArchiveEvidence(context.Background(), run)
			accepted := mode == "matching" || mode == "legacy"
			if (err == nil) != accepted || (evidence != nil) != accepted {
				t.Fatalf("CI archive accepted=%t want=%t evidence=%+v err=%v", err == nil, accepted, evidence, err)
			}
			response, err := server.buildCIRunResponse(context.Background(), run)
			if err != nil {
				t.Fatal(err)
			}
			want := "execution_error"
			if accepted {
				want = "passed"
			}
			if response.Outcome != want || (!accepted && (response.ReportURL != "" || response.Validity != nil)) {
				t.Fatalf("CI reused stale archive evidence: %+v", response)
			}
		})
	}
}
