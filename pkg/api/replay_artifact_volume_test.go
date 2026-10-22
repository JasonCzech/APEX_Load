package api

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestReplayArtifactVolumeIdentitySurvivesReopen(t *testing.T) {
	b, spec, har, report := replayBackendFixture(t, false)
	ctx := context.Background()
	if err := b.Stage(ctx, spec, bytes.NewReader(har), bytes.NewReader(report)); err != nil {
		t.Fatal(err)
	}
	reopened, err := openReplayArtifactBackend(ctx, b.backend)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.volumeIdentity != b.volumeIdentity || reopened.sourceSHA256() != spec.SourceSHA256 {
		t.Fatal("reopen changed physical authority")
	}
	receipt := storage.ReplayArtifactReceipt{Spec: spec, State: "ready", WriterJoined: true}
	read, err := reopened.Read(ctx, receipt)
	if err != nil {
		t.Fatal(err)
	}
	read.Close()
	// Path-only historical receipts cannot prove the volume and are not adopted.
	legacy := spec
	legacy.SourceSHA256 = sha256BytesHex([]byte("replay-artifacts.v1\x00filesystem\x00" + b.backend.LocalRoot))
	if _, err = reopened.Read(ctx, storage.ReplayArtifactReceipt{Spec: legacy, State: "ready", WriterJoined: true}); err == nil {
		t.Fatal("legacy path-only authority adopted")
	}
}

func TestReplayArtifactVolumeReplacementCannotReleaseOriginal(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "archive")
	original := filepath.Join(base, "original")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b, err := openReplayArtifactBackend(ctx, archiveBackendRuntime{Backend: archiveBackendFilesystem, LocalRoot: path})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	har, report := []byte("original native"), []byte("original report")
	spec := storage.ReplayArtifactSpec{Version: 1, SessionID: "session", ProjectID: "project", Generation: strings.Repeat("a", 64), SourceSHA256: b.sourceSHA256(), HARSHA256: sha256BytesHex(har), HARBytes: int64(len(har)), ReportSHA256: sha256BytesHex(report), ReportBytes: int64(len(report))}
	if err = b.Stage(ctx, spec, bytes.NewReader(har), bytes.NewReader(report)); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path, original); err != nil {
		t.Skipf("platform cannot replace an open directory: %v", err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	replacement, err := openReplayArtifactBackend(ctx, archiveBackendRuntime{Backend: archiveBackendFilesystem, LocalRoot: path})
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if replacement.sourceSHA256() == spec.SourceSHA256 {
		t.Fatal("same path adopted another volume")
	}
	foreign := spec
	foreign.SourceSHA256 = replacement.sourceSHA256()
	if err = replacement.Stage(ctx, foreign, bytes.NewReader(har), bytes.NewReader(report)); err != nil {
		t.Fatal(err)
	}
	receipt := storage.ReplayArtifactReceipt{Spec: spec, State: "cleanup", WriterJoined: true}
	if err = replacement.Cleanup(ctx, receipt); err == nil {
		t.Fatal("foreign volume acknowledged original cleanup")
	}
	// The old adapter's held root must still read and clean the original volume,
	// even though the configured path now reaches the replacement.
	ready := receipt
	ready.State = "ready"
	read, err := b.Read(ctx, ready)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(read.HAR)
	read.Close()
	if err != nil || !bytes.Equal(data, har) {
		t.Fatal("held-root read changed", err)
	}
	if err = b.Cleanup(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(original, archiveReplayDirectory, "v1", spec.Generation)); !os.IsNotExist(err) {
		t.Fatal("original cleanup missing", err)
	}
	if got, err := os.ReadFile(filepath.Join(path, archiveReplayDirectory, "v1", spec.Generation, "replay.har")); err != nil || !bytes.Equal(got, har) {
		t.Fatal("foreign generation changed", err)
	}
}

func TestReplayArtifactVolumeConcurrentInitialization(t *testing.T) {
	backend := archiveBackendRuntime{Backend: archiveBackendFilesystem, LocalRoot: t.TempDir()}
	const count = 8
	var wg sync.WaitGroup
	ids := make(chan string, count)
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := openReplayArtifactBackend(context.Background(), backend)
			if err != nil {
				errs <- err
				return
			}
			defer b.Close()
			ids <- b.sourceSHA256()
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	expected := ""
	seen := 0
	for id := range ids {
		seen++
		if expected == "" {
			expected = id
		}
		if id != expected {
			t.Fatal("concurrent authority forked")
		}
	}
	if seen != count {
		t.Fatal("openers failed", seen)
	}
	entries, err := os.ReadDir(filepath.Join(backend.LocalRoot, archiveReplayDirectory))
	if err != nil || len(entries) != 1 || entries[0].Name() != replayArtifactVolumeMarker {
		t.Fatal("temporary identity residue", entries, err)
	}
}

func TestReplayArtifactVolumeRejectsCorruptOrLinkedMarker(t *testing.T) {
	for _, mode := range []string{"short", "long", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			backend := archiveBackendRuntime{Backend: archiveBackendFilesystem, LocalRoot: t.TempDir()}
			dir := filepath.Join(backend.LocalRoot, archiveReplayDirectory)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, replayArtifactVolumeMarker)
			data := []byte("invalid")
			if mode == "long" {
				data = []byte(strings.Repeat("a", 65))
			}
			if mode == "symlink" {
				target := filepath.Join(t.TempDir(), "foreign")
				if err := os.WriteFile(target, []byte(strings.Repeat("a", 64)), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skip(err)
				}
			} else if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if b, err := openReplayArtifactBackend(context.Background(), backend); err == nil {
				b.Close()
				t.Fatal("invalid authority accepted")
			}
			if mode != "symlink" {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, data) {
					t.Fatal("invalid authority overwritten", err)
				}
			}
		})
	}
}

func TestReplayArtifactVolumeMarkerChangeFencesOpenAdapter(t *testing.T) {
	b, spec, har, report := replayBackendFixture(t, false)
	ctx := context.Background()
	if err := b.Stage(ctx, spec, bytes.NewReader(har), bytes.NewReader(report)); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(b.backend.LocalRoot, archiveReplayDirectory, replayArtifactVolumeMarker)
	replacement := strings.Repeat("f", 64)
	if replacement == b.volumeIdentity {
		replacement = strings.Repeat("e", 64)
	}
	if err := os.WriteFile(marker, []byte(replacement), 0600); err != nil {
		t.Fatal(err)
	}
	receipt := storage.ReplayArtifactReceipt{Spec: spec, State: "ready", WriterJoined: true}
	if r, err := b.Read(ctx, receipt); err == nil {
		r.Close()
		t.Fatal("changed source remained readable")
	}
	receipt.State = "cleanup"
	if err := b.Cleanup(ctx, receipt); err == nil {
		t.Fatal("changed source was reclaimed")
	}
	next := spec
	next.Generation = strings.Repeat("b", 64)
	if err := b.Stage(ctx, next, bytes.NewReader(har), bytes.NewReader(report)); err == nil {
		t.Fatal("changed source accepted new publication")
	}
	if got, err := os.ReadFile(filepath.Join(b.backend.LocalRoot, archiveReplayDirectory, "v1", spec.Generation, "replay.har")); err != nil || !bytes.Equal(got, har) {
		t.Fatal("fenced source bytes changed", err)
	}
}
