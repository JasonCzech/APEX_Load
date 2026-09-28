package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestArchiveMergeConfigurationFrozenAndValidated(t *testing.T) {
	for _, key := range []string{envArchiveMergeMaxBytes, envArchiveMergeMinFreeBytes} {
		for _, raw := range []string{"0", "-1", "x", "1.1", "1125899906842625"} {
			t.Run(key+raw, func(t *testing.T) {
				t.Setenv(key, raw)
				if _, err := newArchiveMergeBudget(t.TempDir()); err == nil {
					t.Fatal("invalid limit accepted")
				}
			})
		}
	}
	for key, maximum := range map[string]int64{envArchiveMergeMaxBytes: 128 << 30, envArchiveMergeMinFreeBytes: 1 << 50} {
		t.Run(key+"-boundary", func(t *testing.T) {
			t.Setenv(key, strconv.FormatInt(maximum, 10))
			if _, err := newArchiveMergeBudget(t.TempDir()); err != nil {
				t.Fatal("maximum rejected", err)
			}
			t.Setenv(key, strconv.FormatInt(maximum+1, 10))
			if _, err := newArchiveMergeBudget(t.TempDir()); err == nil {
				t.Fatal("above maximum accepted")
			}
		})
	}
	t.Setenv(envArchiveMergeMaxBytes, "7")
	t.Setenv(envArchiveMergeMinFreeBytes, "1")
	b, err := newArchiveMergeBudget(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envArchiveMergeMaxBytes, "99")
	if b.remaining != 7 || b.minFree != 1 {
		t.Fatal("budget was not frozen")
	}
	if _, err := mergeBudget(b.root, []*archiveOutputBudget{nil}); err == nil {
		t.Fatal("nil budget accepted")
	}
}

func TestArchiveMergeFailureRetainsVerifiedSourcesForRetry(t *testing.T) {
	tr, store, run, backend := collectionCacheFixture(t, "generator-a")
	before, err := store.GetTest(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	completed := time.Now().UTC()
	t.Setenv(envArchiveMergeMaxBytes, "1")
	t.Setenv(envArchiveMergeMinFreeBytes, "1")
	record, err := backend.stageRemoteArchive(tr, run, "COMPLETE", "", completed, false)
	if record != nil || !errors.Is(err, errArchiveMergeLimit) {
		t.Fatalf("record=%v error=%v", record, err)
	}
	intent, err := store.GetRunArchiveCollection(run.ID)
	if err != nil || len(intent.CachedSources) != 1 {
		t.Fatalf("accepted source lost: %+v %v", intent, err)
	}
	after, err := store.GetTest(run.ID)
	if err != nil || after.Status != before.Status || after.Error != before.Error {
		t.Fatal("merge failure changed execution outcome", err)
	}
	// The retry must work without a reachable generator: the confirmed source
	// cache, rather than a newly downloaded copy, supplies the retained evidence.
	run.remoteClientsByTarget = nil
	t.Setenv(envArchiveMergeMaxBytes, strconv.Itoa(1<<20))
	record, err = backend.stageRemoteArchive(tr, run, "COMPLETE", "", completed, false)
	if err != nil || record == nil {
		t.Fatalf("retry from retained source: %v", err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil || manifest.Status != "COMPLETE" || !manifest.CompletedAt.Equal(completed) {
		t.Fatalf("retry manifest: %+v %v", manifest, err)
	}
}

func TestArchiveMergeSummaryChargesProvisionalAndFinalWrites(t *testing.T) {
	sourceRoot := t.TempDir()
	source := filepath.Join(sourceRoot, "source.jsonl")
	row := IntervalSummaryRecord{Timestamp: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC), TestID: "merge-summary", ActiveVUsers: 1, TPS: 3, TotalTx: 3}
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	bundles := []remoteArchiveBundle{{target: "generator-a", summariesPath: source, manifest: RunArchiveManifest{IntervalSummaryCount: 1}}}
	root := t.TempDir()
	b := &archiveOutputBudget{root: root, remaining: 1 << 20, minFree: 1, limitErr: errArchiveMergeLimit}
	destination := filepath.Join(root, "summaries.jsonl")
	if n, err := mergeRemoteArchiveIntervalSummariesWithTargets(destination, bundles, nil, b); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	used := int64(1<<20) - b.remaining
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if used <= info.Size() {
		t.Fatal("provisional summary bytes were not charged")
	}
	root = t.TempDir()
	b = &archiveOutputBudget{root: root, remaining: used - 1, minFree: 1, limitErr: errArchiveMergeLimit}
	if _, err := mergeRemoteArchiveIntervalSummariesWithTargets(filepath.Join(root, "summaries.jsonl"), bundles, nil, b); !errors.Is(err, errArchiveMergeLimit) {
		t.Fatal("summary stages reset the shared allowance", err)
	}
	if got, err := os.ReadFile(source); err != nil || string(got) != string(append(data, '\n')) {
		t.Fatal("source changed", err)
	}
}

func TestArchiveMergeAppendRejectsLinkedTargetsAndPreservesContents(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "resources.jsonl")
	if err := os.WriteFile(target, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := openArchiveArtifactForAppend(root, "resources.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("after\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got, err := os.ReadFile(target); err != nil || string(got) != "before\nafter\n" {
		t.Fatal("append truncated source", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	if f, err := openArchiveArtifactForAppend(root, "link"); err == nil {
		f.Close()
		t.Fatal("linked target accepted")
	}
	if f, err := openArchiveArtifactForAppend(root, "../outside"); err == nil {
		f.Close()
		t.Fatal("parent traversal accepted")
	}
}

func TestArchiveOutputCancellationLatchesAcrossFiles(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &archiveOutputBudget{root: root, remaining: 100, minFree: 1, ctx: ctx}
	for _, name := range []string{"first", "second"} {
		f, err := os.Create(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		n, err := (&archiveOutputFile{file: f, budget: b}).Write([]byte("must not write"))
		f.Close()
		if n != 0 || !errors.Is(err, context.Canceled) {
			t.Fatal(n, err)
		}
		b.ctx = context.Background() // The original failure must remain latched.
	}
	if b.remaining != 100 {
		t.Fatal("cancelled write consumed logical bytes")
	}
}

func TestArchiveMergeDatasetsUseOneAllowance(t *testing.T) {
	client := coverageArchiveClient(t, "merge-budget")
	raw, err := os.ReadFile(client.artifacts["manifest.json"])
	if err != nil {
		t.Fatal(err)
	}
	var source RunArchiveManifest
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	bundles := []remoteArchiveBundle{{target: "generator-a", manifest: source, transactionsPath: client.artifacts[source.TransactionsFile], resourcesPath: client.artifacts[source.ResourceSamplesFile], summariesPath: client.artifacts[source.IntervalSummariesFile]}}
	root := t.TempDir()
	budget := &archiveOutputBudget{root: root, remaining: 1 << 20, minFree: 1, limitErr: errArchiveMergeLimit}
	manifest := source
	if err := writeMergedRemoteArchiveFilesWithBudget(root, &manifest, bundles, nil, budget); err != nil {
		t.Fatal(err)
	}
	used := int64(1<<20) - budget.remaining
	if used <= 0 {
		t.Fatal("no merged output charged")
	}
	root = t.TempDir()
	budget = &archiveOutputBudget{root: root, remaining: used - 1, minFree: 1, limitErr: errArchiveMergeLimit}
	manifest = source
	if err := writeMergedRemoteArchiveFilesWithBudget(root, &manifest, bundles, nil, budget); !errors.Is(err, errArchiveMergeLimit) {
		t.Fatal("datasets reset the shared allowance", err)
	}
}

func TestArchiveMergePhysicalFailureLatches(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envArchiveMergeMaxBytes, "100")
	t.Setenv(envArchiveMergeMinFreeBytes, strconv.FormatInt(1<<50, 10))
	b, err := newArchiveMergeBudget(root)
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(root, "first")
	if err := writeSyncedArchiveMergeFile(first, []byte("no"), b); !errors.Is(err, errArchiveMergeFreeSpace) {
		t.Fatal(err)
	}
	b.minFree = 1
	if err := writeSyncedArchiveMergeFile(filepath.Join(root, "second"), []byte("no"), b); !errors.Is(err, errArchiveMergeFreeSpace) {
		t.Fatal("failure not latched", err)
	}
	if b.remaining != 100 {
		t.Fatal("denied output consumed quota")
	}
	for _, name := range []string{"first", "second"} {
		if info, err := os.Stat(filepath.Join(root, name)); err != nil || info.Size() != 0 {
			t.Fatal("denied output written", err)
		}
	}
}

func TestArchiveMergeTelemetrySharesEvidenceAndResourceAllowance(t *testing.T) {
	completed := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for _, kind := range []string{"elk", "dynatrace"} {
		t.Run(kind, func(t *testing.T) {
			write := func(root string, budget *archiveOutputBudget) (*RunArchiveManifest, error) {
				m := &RunArchiveManifest{ResourceSamplesFile: "resources.jsonl"}
				run := &ManagedTest{ID: "telemetry", Name: "Telemetry"}
				if kind == "elk" {
					run.ELKCapture = &ELKCaptureConfig{ConnectionID: "source"}
					run.elkArtifacts = &runArtifacts{elkSamples: []ELKLogSample{{ID: "one", Message: "event"}}, elkStatus: &ELKCaptureStatus{Metrics: map[string]float64{"logs": 7}}}
					return m, writeDistributedELKEvidence(root, m, nil, run, completed, budget)
				}
				run.DynatraceCapture = &DynatraceCaptureConfig{ConnectionID: "source"}
				run.dynatraceArtifacts = &runArtifacts{dynatraceLogs: []DynatraceLogSample{{Message: "event"}}, dynatraceStatus: &DynatraceCaptureStatus{Metrics: map[string]float64{"logs": 7}}}
				return m, writeDistributedDynatraceEvidence(root, m, nil, run, completed, budget)
			}
			setup := func(limit int64) (string, *archiveOutputBudget) {
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "resources.jsonl"), []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
				return root, &archiveOutputBudget{root: root, remaining: limit, minFree: 1, limitErr: errArchiveMergeLimit}
			}
			root, budget := setup(1 << 20)
			manifest, err := write(root, budget)
			if err != nil || manifest.ResourceSampleCount != 1 {
				t.Fatal(manifest, err)
			}
			used := int64(1<<20) - budget.remaining
			for _, limit := range []int64{1, used - 1} {
				root, budget = setup(limit)
				manifest, err = write(root, budget)
				if !errors.Is(err, errArchiveMergeLimit) || manifest.ResourceSampleCount != 0 {
					t.Fatal(manifest, err)
				}
				if limit > 1 && manifest.ELKEvidenceCount+manifest.DynatraceEvidenceCount == 0 {
					t.Fatal("test did not reach resource append")
				}
				if data, err := os.ReadFile(filepath.Join(root, "resources.jsonl")); err != nil || string(data) != "{}\n" {
					t.Fatal("failed append changed existing measurements", err)
				}
			}
		})
	}
}

func TestArchiveMergeManifestWaitsForDurableArtifactsAndSharesBudget(t *testing.T) {
	for _, mode := range []string{"complete", "linked-artifact", "byte-limit", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "exports"), 0700); err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(root, "exports", "rows.csv")
			if err := os.WriteFile(artifact, []byte("value\n1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			budget := &archiveOutputBudget{root: root, remaining: 1000, minFree: 1, limitErr: errArchiveMergeLimit}
			data := []byte(`{"test_id":"durable-merge"}`)
			switch mode {
			case "linked-artifact":
				if err := os.Symlink(artifact, filepath.Join(root, "link")); err != nil {
					t.Skip(err)
				}
			case "byte-limit":
				budget.remaining = int64(len(data) - 1)
			case "cancelled":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				budget.ctx = ctx
			}
			manifest := filepath.Join(root, "manifest.json")
			err := writeDurableArchiveMergeManifest(manifest, data, budget)
			got, readErr := os.ReadFile(manifest)
			if mode == "complete" {
				if err != nil || readErr != nil || string(got) != string(data) || budget.remaining != 1000-int64(len(data)) {
					t.Fatal(err, readErr, budget.remaining)
				}
			} else {
				if err == nil || json.Valid(got) {
					t.Fatal("failed durability/admission published a valid manifest", err)
				}
				if mode == "linked-artifact" && !os.IsNotExist(readErr) {
					t.Fatal("manifest opened before artifact validation", readErr)
				}
				if mode == "byte-limit" && !errors.Is(err, errArchiveMergeLimit) {
					t.Fatal(err)
				}
				if mode == "cancelled" && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			if got, err := os.ReadFile(artifact); err != nil || string(got) != "value\n1\n" {
				t.Fatal("source artifact changed", err)
			}
		})
	}
}

func TestDistributedArchiveOutputAdmissionPreservesCompletedOutcome(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"merge-bytes", envArchiveMergeMaxBytes, "1"},
		{"merge-space", envArchiveMergeMinFreeBytes, strconv.FormatInt(1<<50, 10)},
		{"conversion-bytes", envArchiveConversionMaxBytes, "1"},
		{"conversion-space", envArchiveConversionMinFreeBytes, strconv.FormatInt(1<<50, 10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr, store, run, backend := collectionCacheFixture(t, "generator-a")
			t.Setenv(tc.key, tc.value)
			backend.completeDistributedTestRun(tr, run, "COMPLETE", "")
			stored, err := store.GetTest(run.ID)
			if err != nil || stored.Status != "COMPLETE" || stored.Error != "" {
				t.Fatalf("archive admission changed execution outcome: %+v %v", stored, err)
			}
			failure, err := store.GetRunArchiveFailure(run.ID)
			if err != nil || failure.Status != "COMPLETE" || failure.Reason != storage.RunArchiveFailureRemoteCollection {
				t.Fatalf("archive failure was not durable: %+v %v", failure, err)
			}
			intent, err := store.GetRunArchiveCollection(run.ID)
			if err != nil || intent.Resolved || len(intent.CachedSources) != 1 {
				t.Fatalf("verified source unavailable for retry: %+v %v", intent, err)
			}
			if run.Archive != nil {
				t.Fatal("failed output published an archive")
			}
		})
	}
}
