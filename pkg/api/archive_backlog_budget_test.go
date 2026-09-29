package api

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/archivequota"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
	local "github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/reader"
)

func TestControllerArchiveBacklogCoversOutputKinds(t *testing.T) {
	factories := map[string]func(string) (*archiveOutputBudget, error){"merge": newArchiveMergeBudget, "conversion": newArchiveConversionBudget, "publication": newArchivePublicationBudget, "adjunct": func(root string) (*archiveOutputBudget, error) {
		return newArchiveAdjunctBudget(context.Background(), root)
	}}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKLOG_MAX_BYTES", "1")
			if err := os.WriteFile(filepath.Join(root, "retained"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			budget, err := factory(root)
			if err != nil {
				t.Fatal(err)
			}
			defer budget.close()
			f, err := os.Create(filepath.Join(root, "new"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if n, err := (&archiveOutputFile{file: f, budget: budget}).Write([]byte("x")); n != 0 || err == nil {
				t.Fatal("retained data was not charged", n, err)
			}
			got, err := os.ReadFile(filepath.Join(root, "retained"))
			if err != nil || string(got) != "keep" {
				t.Fatal("retained source changed", err)
			}
		})
	}
}

func TestControllerArchiveBacklogRejectsCaptureAndPreservesExistingData(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_BACKLOG_MAX_BYTES", "1")
	if err := os.WriteFile(filepath.Join(root, "retained"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "backlog-capture", Name: "capture"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.RecordResult(&protocol.Result{ActionName: "accepted", Success: true}); err != nil {
		t.Fatal(err)
	}
	record, err := writer.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if record != nil || err == nil {
		t.Fatal("capture ignored retained backlog", record, err)
	}
	got, err := os.ReadFile(filepath.Join(root, "retained"))
	if err != nil || string(got) != "keep" {
		t.Fatal("retained source changed", err)
	}
	if _, err := os.Stat(writer.manifestPath); !os.IsNotExist(err) {
		t.Fatal("rejected capture published a manifest", err)
	}
}

func assertControllerBacklogReleased(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, archivequota.DirectoryName))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "capacity.lock" {
			t.Fatal("joined writer retained a reservation", entry.Name())
		}
	}
}

func TestControllerArchiveBacklogSmallQuotaIncludesInitialManifest(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv(envArchiveCaptureMinFreeBytes, "1")
	t.Setenv(envArchiveConversionMinFreeBytes, "1")
	finish := func(root string, limit int64) (*runArchiveWriter, error) {
		t.Helper()
		t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
		t.Setenv(envArchiveBacklogMaxBytes, strconv.FormatInt(limit, 10))
		w, err := newRunArchiveWriter(&ManagedTest{ID: "small-backlog", Name: "capture"}, RunArchiveProvenance{})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.RecordResult(&protocol.Result{ActionName: "accepted", Success: true}); err != nil {
			t.Fatal(err)
		}
		_, err = w.Finalize("COMPLETE", time.Unix(1700000000, 0).UTC(), "", false, false, nil)
		assertControllerBacklogReleased(t, root)
		return w, err
	}
	root := t.TempDir()
	w, err := finish(root, 128<<10)
	if err != nil {
		t.Fatal("small quota could not share raw/derived reservation", err)
	}
	var total int64
	if err := filepath.WalkDir(w.rootDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err == nil {
			total += info.Size()
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.Stat(w.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := finish(t.TempDir(), total-manifest.Size()/2)
	if !errors.Is(err, archivequota.ErrCapacity) {
		t.Fatal("initial manifest was not charged", err)
	}
	if _, err := os.Stat(rejected.manifestPath); !os.IsNotExist(err) {
		t.Fatal("rejected manifest published", err)
	}
	for _, name := range []string{rejected.manifest.TransactionsParquetFile, rejected.manifest.ResourceSamplesParquetFile, rejected.manifest.IntervalSummariesParquetFile} {
		f, err := local.NewLocalFileReader(filepath.Join(rejected.rootDir, name))
		if err != nil {
			t.Fatal(err)
		}
		r, err := reader.NewParquetReader(f, nil, 1)
		if err != nil {
			f.Close()
			t.Fatal("quota rejected before completing Parquet", name, err)
		}
		want := int64(0)
		if name == rejected.manifest.TransactionsParquetFile {
			want = 1
		}
		if r.GetNumRows() != want {
			t.Error(name, r.GetNumRows())
		}
		r.ReadStop()
		f.Close()
	}
	data, err := os.ReadFile(filepath.Join(rejected.rootDir, "transactions.jsonl"))
	if err != nil || !strings.Contains(string(data), "accepted") {
		t.Fatal("raw source lost", err)
	}
}

func TestControllerArchiveBacklogConfigurationAndClose(t *testing.T) {
	for _, value := range []string{"0", "-1", "x", "1e3", "1.5", "1125899906842625"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envArchiveBacklogMaxBytes, value)
			if _, err := newArchiveBacklogBudget(t.TempDir()); err == nil {
				t.Fatal("invalid limit accepted")
			}
		})
	}
	root := t.TempDir()
	t.Setenv(envArchiveBacklogMaxBytes, "3")
	b, err := newArchiveBacklogBudget(root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	t.Setenv(envArchiveBacklogMaxBytes, "999")
	f, err := os.Create(filepath.Join(root, "output"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if n, err := b.write(context.Background(), f, []byte("1234")); n != 3 || !errors.Is(err, archivequota.ErrCapacity) {
		t.Fatal("limit was not frozen", n, err)
	}
	b.close()
	assertControllerBacklogReleased(t, root)
	if n, err := b.write(context.Background(), f, []byte("x")); n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Fatal(n, err)
	}
}

func TestControllerArchiveBacklogIndependentWriters(t *testing.T) {
	root := t.TempDir()
	t.Setenv(envArchiveBacklogMaxBytes, "10")
	makeWriter := func(name string) (*archiveBacklogBudget, *os.File) {
		t.Helper()
		b, err := newArchiveBacklogBudget(root)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(b.close)
		f, err := os.Create(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return b, f
	}
	first, f := makeWriter("first")
	if n, err := first.write(context.Background(), f, []byte("keep")); n != 4 || err != nil {
		t.Fatal(n, err)
	}
	blocked, g := makeWriter("second")
	if n, err := blocked.write(context.Background(), g, []byte("x")); n != 0 || !errors.Is(err, archivequota.ErrCapacity) {
		t.Fatal("reservation ignored", n, err)
	}
	blocked.close()
	first.close()
	retry, h := makeWriter("retry")
	if n, err := retry.write(context.Background(), h, []byte("1234567")); n != 6 || !errors.Is(err, archivequota.ErrCapacity) {
		t.Fatal("retained data not charged after release", n, err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil || string(data) != "keep" {
		t.Fatal("retained source changed", err)
	}
}

func TestControllerArchiveBacklogDescriptorMembership(t *testing.T) {
	for _, mode := range []string{"outside", "moved", "replaced", "renamed"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			b, err := newArchiveBacklogBudget(root)
			if err != nil {
				t.Fatal(err)
			}
			defer b.close()
			path := filepath.Join(root, "original")
			if mode == "outside" {
				path = filepath.Join(t.TempDir(), "outside")
			}
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if mode == "moved" || mode == "replaced" {
				if err := os.Rename(path, filepath.Join(t.TempDir(), "moved")); err != nil {
					t.Fatal(err)
				}
				if mode == "replaced" {
					if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "renamed" {
				if err := os.Rename(path, filepath.Join(root, "final")); err != nil {
					t.Fatal(err)
				}
			}
			n, err := b.write(context.Background(), f, []byte("x"))
			if mode == "renamed" {
				if n != 1 || err != nil {
					t.Fatal(n, err)
				}
			} else if n != 0 || err == nil {
				t.Fatal("unaccounted descriptor admitted", n, err)
			}
		})
	}
}

func TestControllerArchiveBacklogSeparateNamespaces(t *testing.T) {
	for _, name := range controllerArchiveBacklogExclusions {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv(envArchiveBacklogMaxBytes, "1")
			dir := filepath.Join(root, name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "retained"), []byte("separately admitted"), 0600); err != nil {
				t.Fatal(err)
			}
			b, err := newArchiveBacklogBudget(root)
			if err != nil {
				t.Fatal(err)
			}
			defer b.close()
			excluded, err := os.Create(filepath.Join(dir, "output"))
			if err != nil {
				t.Fatal(err)
			}
			defer excluded.Close()
			if n, err := b.write(context.Background(), excluded, []byte("x")); n != 0 || err == nil {
				t.Fatal("excluded output admitted", n, err)
			}
			f, err := os.Create(filepath.Join(root, "included"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if n, err := b.write(context.Background(), f, []byte("x")); n != 1 || err != nil {
				t.Fatal("separate namespace charged", n, err)
			}
		})
	}
	for _, name := range []string{archivequota.DirectoryName, ".APEXLOAD-BACKLOG", archivequota.DirectoryName + ".", archivequota.DirectoryName + ":stream"} {
		if err := validateArchiveRunID(name); err == nil {
			t.Fatal("reserved run ID accepted", name)
		}
	}
}

func TestControllerArchiveBacklogAdjunctSurvivesBackendChange(t *testing.T) {
	for _, original := range []string{"filesystem", "object-default", "object-custom"} {
		t.Run(original, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			if original != "filesystem" {
				t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
				t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "original")
				if original == "object-custom" {
					t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())
				}
			}
			originalRoot := resolveArchiveRoot()
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			run, record := createArchiveFinalizationTest(t, store, "backlog-backend-change", originalRoot)
			runner := NewTestRunner(1, nil, store)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
			t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "replacement")
			if err := runner.persistTestManifestArtifact(run, record); err != nil {
				t.Fatal("existing archive could not finalize after backend change", err)
			}
			assertControllerBacklogReleased(t, originalRoot)
			t.Setenv(envArchiveBacklogMaxBytes, "1")
			if err := runner.persistTestManifestArtifact(run, record); !errors.Is(err, archivequota.ErrCapacity) {
				t.Fatal("existing namespace not charged", err)
			}
		})
	}
}

func TestControllerArchiveBacklogFinalizationRootBounds(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	for _, relative := range []string{"_collection/run", "library-imports/run", "_object_staging/s3/not-a-namespace/run", archivequota.DirectoryName + "/run"} {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := archiveBacklogRootForRecord(&storage.RunArchiveRecord{ArchivePath: path}); err == nil {
			t.Fatal("unadmitted finalization root", relative)
		}
	}
	if _, err := archiveBacklogRootForRecord(nil); err == nil {
		t.Fatal("nil record admitted")
	}
	if _, err := archiveBacklogRootForRecord(&storage.RunArchiveRecord{ArchivePath: t.TempDir()}); err == nil {
		t.Fatal("outside root admitted")
	}
	path := filepath.Join(root, "remote", "run")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := archiveBacklogRootForRecord(&storage.RunArchiveRecord{ArchivePath: path})
	want, resolveErr := resolveArchiveDirectoryRoot(root, false)
	if err != nil || resolveErr != nil || got != want {
		t.Fatal("per-run quota scope substituted for shared namespace", got, want, err, resolveErr)
	}
}

func TestControllerArchiveBacklogRetentionReceiptPreservesPriorConfirmation(t *testing.T) {
	for _, prior := range []bool{false, true} {
		t.Run(strconv.FormatBool(prior), func(t *testing.T) {
			_, _, _, record, _ := sourceRetentionFixture(t, false)
			receipt := filepath.Join(record.ArchivePath, archiveSourceReceiptName)
			var before []byte
			if prior {
				if err := markPublishedArchiveSource(record); err != nil {
					t.Fatal(err)
				}
				var err error
				before, err = os.ReadFile(receipt)
				if err != nil {
					t.Fatal(err)
				}
			}
			manifest, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv(envArchiveBacklogMaxBytes, "1")
			if err := markPublishedArchiveSource(record); !errors.Is(err, archivequota.ErrCapacity) {
				t.Fatal("receipt bypassed admission", err)
			}
			after, err := os.ReadFile(receipt)
			if prior {
				if err != nil || string(after) != string(before) {
					t.Fatal("prior confirmation changed", err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatal("rejected receipt was published", err)
			}
			after, err = os.ReadFile(record.ManifestPath)
			if err != nil || string(after) != string(manifest) {
				t.Fatal("published manifest changed", err)
			}
			assertControllerBacklogReleased(t, resolveArchiveRoot())
			t.Setenv(envArchiveBacklogMaxBytes, "131072")
			if err := markPublishedArchiveSource(record); err != nil {
				t.Fatal("confirmation retry failed", err)
			}
		})
	}
}
