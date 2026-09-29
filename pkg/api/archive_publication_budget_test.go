package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestArchivePublicationBudgetConfiguration(t *testing.T) {
	for key, maximum := range map[string]int64{envArchivePublicationMaxBytes: 128 << 30, envArchivePublicationMinFreeBytes: 1 << 50} {
		for _, raw := range []string{"0", "-1", "x", "1.1", strconv.FormatInt(maximum+1, 10)} {
			t.Run(key+raw, func(t *testing.T) {
				t.Setenv(key, raw)
				if _, err := newArchivePublicationBudget(t.TempDir()); err == nil {
					t.Fatal("invalid configuration accepted")
				}
			})
		}
		t.Run(key+"boundary", func(t *testing.T) {
			t.Setenv(key, strconv.FormatInt(maximum, 10))
			if _, err := newArchivePublicationBudget(t.TempDir()); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Setenv(envArchivePublicationMaxBytes, "7")
	b, err := newArchivePublicationBudget(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envArchivePublicationMaxBytes, "9")
	if b.remaining != 7 {
		t.Fatal("configuration was not frozen")
	}
}

func publicationBudgetFixture(t *testing.T) (*storage.RunArchiveRecord, map[string][]byte) {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "archives")
	root := t.TempDir()
	data := map[string][]byte{"transactions.jsonl": bytes.Repeat([]byte("transactions\n"), 12000), "resources.jsonl": []byte("resources\n"), "reports/summary.csv": []byte("name,value\nrequests,1\n")}
	m := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: "publication-budget", TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl"}
	for _, name := range []string{m.TransactionsFile, m.ResourceSamplesFile, "reports/summary.csv"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), data[name], 0600); err != nil {
			t.Fatal(err)
		}
		m.Artifacts = append(m.Artifacts, RunArchiveArtifact{Name: name, SizeBytes: int64(len(data[name])), SHA256: sha256BytesHex(data[name])})
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	data[runArchiveManifestFileName] = encoded
	path := filepath.Join(root, runArchiveManifestFileName)
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return &storage.RunArchiveRecord{TestID: m.TestID, ArchiveBackend: archiveBackendS3, ArchivePath: root, ManifestPath: path, ManifestSHA256: sha256BytesHex(encoded)}, data
}

func TestArchivePublicationCopyBudgetRetainsSourceAndRetries(t *testing.T) {
	for _, mode := range []string{"bytes", "manifest", "free", "cancel", "prior-marker", "partial"} {
		t.Run(mode, func(t *testing.T) {
			source, data := publicationBudgetFixture(t)
			const token = "33333333-3333-4333-8333-333333333333"
			t.Setenv(envArchivePublicationMinFreeBytes, "1")
			t.Setenv(envArchivePublicationMaxBytes, "1048576")
			expected := errArchivePublicationLimit
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "bytes":
				t.Setenv(envArchivePublicationMaxBytes, "1")
			case "partial":
				t.Setenv(envArchivePublicationMaxBytes, "70000")
			case "prior-marker":
				if _, _, err := stageMaterializedArchiveForPublication(ctx, source, token); err != nil {
					t.Fatal(err)
				}
				t.Setenv(envArchivePublicationMaxBytes, "1")
			case "manifest":
				total := 0
				for _, content := range data {
					total += len(content)
				}
				t.Setenv(envArchivePublicationMaxBytes, strconv.Itoa(total-1))
			case "free":
				t.Setenv(envArchivePublicationMinFreeBytes, strconv.FormatInt(1<<50, 10))
				expected = errArchivePublicationFreeSpace
			case "cancel":
				cancel()
				expected = context.Canceled
			}
			root, manifest, err := stageMaterializedArchiveForPublication(ctx, source, token)
			if root != "" || manifest != "" || !errors.Is(err, expected) {
				t.Fatalf("root=%q manifest=%q error=%v", root, manifest, err)
			}
			target := filepath.Join(resolveArchiveRoot(), "_publication", token, source.TestID)
			if _, err := os.Stat(filepath.Join(target, runArchiveManifestFileName)); !os.IsNotExist(err) {
				t.Fatalf("failed copy left a commit marker: %v", err)
			}
			for name, want := range data {
				got, err := os.ReadFile(filepath.Join(source.ArchivePath, name))
				if err != nil || string(got) != string(want) {
					t.Fatalf("source changed: %s %v", name, err)
				}
			}
			t.Setenv(envArchivePublicationMinFreeBytes, "1")
			t.Setenv(envArchivePublicationMaxBytes, "1048576")
			root, manifest, err = stageMaterializedArchiveForPublication(context.Background(), source, token)
			resolvedTarget, resolveErr := filepath.EvalSymlinks(target)
			if err != nil || resolveErr != nil || root != resolvedTarget || manifest != filepath.Join(resolvedTarget, runArchiveManifestFileName) {
				t.Fatalf("retry: %q %q %v (resolve %v)", root, manifest, err, resolveErr)
			}
			for name, want := range data {
				got, err := os.ReadFile(filepath.Join(root, name))
				if err != nil || string(got) != string(want) {
					t.Fatalf("copy mismatch: %s %v", name, err)
				}
			}
		})
	}
}

func TestArchivePublicationCopyRejectsSourceAlias(t *testing.T) {
	source, data := publicationBudgetFixture(t)
	const token = "33333333-3333-4333-8333-333333333333"
	root, manifest, err := stageMaterializedArchiveForPublication(context.Background(), source, token)
	if err != nil {
		t.Fatal(err)
	}
	copy := *source
	copy.ArchivePath = root
	copy.ManifestPath = manifest
	if _, _, err := stageMaterializedArchiveForPublication(context.Background(), &copy, token); err == nil {
		t.Fatal("source alias accepted")
	}
	for name, want := range data {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != string(want) {
			t.Fatalf("source alias changed: %s %v", name, err)
		}
	}
}

func TestArchivePublicationManifestCopiedOnceAndLast(t *testing.T) {
	source, data := publicationBudgetFixture(t)
	names, err := archivePublicationArtifactNames(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, name := range names {
		if name == runArchiveManifestFileName {
			count++
		}
	}
	if count != 1 || names[len(names)-1] != runArchiveManifestFileName {
		t.Fatalf("manifest order: %v", names)
	}
	total := 0
	for _, content := range data {
		total += len(content)
	}
	t.Setenv(envArchivePublicationMaxBytes, strconv.Itoa(total))
	t.Setenv(envArchivePublicationMinFreeBytes, "1")
	if _, _, err := stageMaterializedArchiveForPublication(context.Background(), source, "33333333-3333-4333-8333-333333333333"); err != nil {
		t.Fatalf("exact copy boundary rejected: %v", err)
	}
}

func TestArchivePublicationCopyRejectsUnsafeRunID(t *testing.T) {
	for _, id := range []string{"../escaped", "nested/run", ".", ""} {
		t.Run(id, func(t *testing.T) {
			source, _ := publicationBudgetFixture(t)
			data, err := os.ReadFile(source.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var manifest RunArchiveManifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			source.TestID = id
			manifest.TestID = id
			data, err = json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			source.ManifestSHA256 = sha256BytesHex(data)
			if err := os.WriteFile(source.ManifestPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := stageMaterializedArchiveForPublication(context.Background(), source, "33333333-3333-4333-8333-333333333333"); err == nil {
				t.Fatal("unsafe run ID accepted")
			}
			if _, err := os.Stat(filepath.Join(resolveArchiveRoot(), "_publication")); !os.IsNotExist(err) {
				t.Fatalf("unsafe ID caused staging side effects: %v", err)
			}
		})
	}
}
