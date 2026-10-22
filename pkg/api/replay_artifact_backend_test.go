package api

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
	"gocloud.dev/blob/fileblob"
)

func replayBackendFixture(t *testing.T, object bool) (*replayArtifactBackend, storage.ReplayArtifactSpec, []byte, []byte) {
	t.Helper()
	backend := archiveBackendRuntime{Backend: archiveBackendFilesystem, LocalRoot: t.TempDir()}
	var b *replayArtifactBackend
	if object {
		bucket, err := fileblob.OpenBucket(t.TempDir(), nil)
		if err != nil {
			t.Fatal(err)
		}
		backend.Backend, backend.Bucket, backend.Prefix = archiveBackendS3, "fixture", "tenant/_source/v1/fixture"
		b = &replayArtifactBackend{backend: backend, bucket: bucket}
	} else {
		var err error
		b, err = openReplayArtifactBackend(context.Background(), backend)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = b.Close() })
	har, report := []byte(`{"log":{"version":"1.2","entries":[]}}`), []byte(`{"session":{"id":"replay-1","status":"completed"}}`)
	spec := storage.ReplayArtifactSpec{Version: 1, SessionID: "replay-1", ProjectID: "project-1", Generation: strings.Repeat("a", 64), SourceSHA256: b.sourceSHA256(), HARSHA256: sha256BytesHex(har), HARBytes: int64(len(har)), ReportSHA256: sha256BytesHex(report), ReportBytes: int64(len(report))}
	return b, spec, har, report
}
func TestReplayArtifactBackendRoundTripAndExactCleanup(t *testing.T) {
	for _, object := range []bool{false, true} {
		t.Run(map[bool]string{false: "filesystem", true: "object-fixture"}[object], func(t *testing.T) {
			b, spec, har, report := replayBackendFixture(t, object)
			ctx := context.Background()
			if err := b.Stage(ctx, spec, bytes.NewReader(har), bytes.NewReader(report)); err != nil {
				t.Fatal(err)
			}
			// A fresh adapter at another object staging root can resolve the same authority.
			recovered := *b
			if object {
				recovered.backend.LocalRoot = t.TempDir()
				if recovered.sourceSHA256() != spec.SourceSHA256 {
					t.Fatal("staging changed source")
				}
			}
			receipt := storage.ReplayArtifactReceipt{Spec: spec, State: "ready", WriterJoined: true}
			got, err := recovered.Read(ctx, receipt)
			if err != nil {
				t.Fatal(err)
			}
			dir := got.directory
			h, err := io.ReadAll(got.HAR)
			if err != nil || !bytes.Equal(h, har) {
				t.Fatalf("HAR %q: %v", h, err)
			}
			r, err := io.ReadAll(got.Report)
			if err != nil || !bytes.Equal(r, report) {
				t.Fatalf("report %q: %v", r, err)
			}
			if err := got.Close(); err != nil {
				t.Fatal(err)
			}
			if err := got.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("staging not removed")
			}
			if err := b.Stage(ctx, spec, bytes.NewReader(har), bytes.NewReader(report)); err == nil {
				t.Fatal("generation overwritten")
			}
			other := spec
			other.Generation = strings.Repeat("b", 64)
			if err := b.Stage(ctx, other, bytes.NewReader(har), bytes.NewReader(report)); err != nil {
				t.Fatal(err)
			}
			if err := b.Cleanup(ctx, receipt); err == nil {
				t.Fatal("ready receipt deleted")
			}
			receipt.State = "cleanup"
			receipt.WriterJoined = false
			if err := b.Cleanup(ctx, receipt); err == nil {
				t.Fatal("unjoined writer deleted")
			}
			receipt.WriterJoined = true
			readReceipt := receipt
			readReceipt.State = "ready"
			pinned, err := b.Read(ctx, readReceipt)
			if err != nil {
				t.Fatal(err)
			}
			defer pinned.Close()
			if err := b.Cleanup(ctx, receipt); err != nil {
				t.Fatal(err)
			}
			if err := b.Cleanup(ctx, receipt); err != nil {
				t.Fatal(err)
			}
			retained, err := io.ReadAll(pinned.HAR)
			if err != nil || !bytes.Equal(retained, har) {
				t.Fatal("cleanup invalidated admitted reader", err)
			}
			receipt.State = "ready"
			if got, err := b.Read(ctx, receipt); err == nil {
				got.Close()
				t.Fatal("missing native bytes synthesized")
			}
			receipt.Spec = other
			got, err = b.Read(ctx, receipt)
			if err != nil {
				t.Fatal("other generation removed", err)
			}
			got.Close()
		})
	}
}
func TestReplayArtifactBackendRejectsInvalidOrPartialBytes(t *testing.T) {
	for _, object := range []bool{false, true} {
		for _, kind := range []string{"short-har", "long-har", "bad-hash", "short-report", "canceled", "source", "generation", "limit"} {
			t.Run(map[bool]string{false: "filesystem", true: "object"}[object]+"/"+kind, func(t *testing.T) {
				b, spec, har, report := replayBackendFixture(t, object)
				ctx := context.Background()
				switch kind {
				case "short-har":
					har = har[:len(har)-1]
				case "long-har":
					har = append(har, 'x')
				case "bad-hash":
					spec.HARSHA256 = strings.Repeat("c", 64)
				case "short-report":
					report = nil
				case "canceled":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				case "source":
					spec.SourceSHA256 = strings.Repeat("c", 64)
				case "generation":
					spec.Generation = "../foreign"
				case "limit":
					spec.HARBytes = 256<<20 + 1
				}
				if err := b.Stage(ctx, spec, bytes.NewReader(har), bytes.NewReader(report)); err == nil {
					t.Fatal("invalid publication accepted")
				}
			})
		}
	}
}
func TestReplayArtifactBackendReadRejectsCorruptionAndChangedSource(t *testing.T) {
	for _, object := range []bool{false, true} {
		for _, kind := range []string{"har", "report", "source", "state", "unjoined"} {
			t.Run(map[bool]string{false: "filesystem", true: "object"}[object]+"/"+kind, func(t *testing.T) {
				b, spec, har, report := replayBackendFixture(t, object)
				ctx := context.Background()
				if err := b.Stage(ctx, spec, bytes.NewReader(har), bytes.NewReader(report)); err != nil {
					t.Fatal(err)
				}
				receipt := storage.ReplayArtifactReceipt{Spec: spec, State: "ready", WriterJoined: true}
				switch kind {
				case "har", "report":
					name := "replay.har"
					if kind == "report" {
						name = "report.json"
					}
					key := b.key(spec, name)
					if b.bucket != nil {
						if err := b.bucket.WriteAll(ctx, key, []byte("corrupt"), nil); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := os.WriteFile(filepath.Join(b.backend.LocalRoot, filepath.FromSlash(key)), []byte("corrupt"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				case "source":
					receipt.Spec.SourceSHA256 = strings.Repeat("e", 64)
				case "state":
					receipt.State = "staging"
				case "unjoined":
					receipt.WriterJoined = false
				}
				if got, err := b.Read(ctx, receipt); err == nil {
					got.Close()
					t.Fatal("unavailable evidence accepted")
				}
			})
		}
	}
}
func TestReplayArtifactBackendFilesystemRefusesForeignGeneration(t *testing.T) {
	b, spec, har, report := replayBackendFixture(t, false)
	ctx := context.Background()
	dir := filepath.Join(b.backend.LocalRoot, archiveReplayDirectory, "v1", spec.Generation)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, "foreign")
	if err := os.WriteFile(foreign, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := b.Stage(ctx, spec, bytes.NewReader(har), bytes.NewReader(report)); err == nil {
		t.Fatal("adopted foreign directory")
	}
	if err := b.Cleanup(ctx, storage.ReplayArtifactReceipt{Spec: spec, State: "cleanup", WriterJoined: true}); err == nil {
		t.Fatal("released reservation with foreign residue")
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "keep" {
		t.Fatal("foreign content changed")
	}
}
func TestReplayArtifactBackendFilesystemRejectsSymlink(t *testing.T) {
	b, spec, har, report := replayBackendFixture(t, false)
	foreign := t.TempDir()
	if err := os.RemoveAll(filepath.Join(b.backend.LocalRoot, archiveReplayDirectory)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(b.backend.LocalRoot, archiveReplayDirectory)); err != nil {
		t.Skip(err)
	}
	if err := b.Stage(context.Background(), spec, bytes.NewReader(har), bytes.NewReader(report)); err == nil {
		t.Fatal("followed symlink")
	}
	entries, err := os.ReadDir(foreign)
	if err != nil || len(entries) != 0 {
		t.Fatal("foreign root changed")
	}
}
func TestReplayArtifactSourceSeparatesBackendNamespaces(t *testing.T) {
	b, spec, _, _ := replayBackendFixture(t, true)
	for _, mutate := range []func(*archiveBackendRuntime){func(v *archiveBackendRuntime) { v.Bucket = "other" }, func(v *archiveBackendRuntime) { v.Prefix += "/other" }, func(v *archiveBackendRuntime) { v.Endpoint = "https://other.invalid" }, func(v *archiveBackendRuntime) { v.Account = "other" }} {
		next := *b
		mutate(&next.backend)
		if next.sourceSHA256() == spec.SourceSHA256 {
			t.Fatal("source identity collision")
		}
	}
	local, localSpec, _, _ := replayBackendFixture(t, false)
	next := *local
	next.backend.LocalRoot = t.TempDir()
	if next.sourceSHA256() == localSpec.SourceSHA256 {
		t.Fatal("filesystem root identity collision")
	}
}

func TestReplayArtifactNamespaceCannotBeDeletedAsRun(t *testing.T) {
	b, spec, har, report := replayBackendFixture(t, false)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", b.backend.LocalRoot)
	if err := b.Stage(context.Background(), spec, bytes.NewReader(har), bytes.NewReader(report)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{archiveReplayDirectory, strings.ToUpper(archiveReplayDirectory), archiveReplayDirectory + ".", archiveReplayDirectory + ":stream"} {
		if err := validateArchiveRunID(name); err == nil {
			t.Fatalf("accepted reserved run %q", name)
		}
	}
	for _, path := range []string{filepath.Join(b.backend.LocalRoot, archiveReplayDirectory), filepath.Join(b.backend.LocalRoot, archiveReplayDirectory, "v1", spec.Generation)} {
		if err := removeLocalArchiveDirectory(path); err == nil {
			t.Fatal("run cleanup accepted replay namespace")
		}
	}
	got, err := b.Read(context.Background(), storage.ReplayArtifactReceipt{Spec: spec, State: "ready", WriterJoined: true})
	if err != nil {
		t.Fatal(err)
	}
	got.Close()
}

func TestOpenReplayArtifactBackendRejectsInvalidRoots(t *testing.T) {
	for _, root := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		if b, err := openReplayArtifactBackend(context.Background(), archiveBackendRuntime{Backend: archiveBackendFilesystem, LocalRoot: root}); err == nil {
			b.Close()
			t.Fatal("invalid root accepted")
		}
	}
	if b, err := openReplayArtifactBackend(context.Background(), archiveBackendRuntime{Backend: "unknown"}); err == nil {
		b.Close()
		t.Fatal("unknown backend accepted")
	}
}
