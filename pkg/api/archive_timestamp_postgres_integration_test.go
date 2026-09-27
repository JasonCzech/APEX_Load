//go:build postgres_integration

package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Exercise the actual pgx serialization, then construct a fresh HA reader. The
// old nanosecond manifest must remain readable without accepting another run.
func TestPostgresArchiveIdentityAfterReload(t *testing.T) {
	testPostgresArchiveIdentityAfterReload(t, false)
}

func TestPostgresS3ArchiveIdentityAfterReload(t *testing.T) {
	if os.Getenv("APEXLOAD_TEST_ARCHIVE_S3") != "1" {
		t.Skip("APEXLOAD_TEST_ARCHIVE_S3 is not enabled")
	}
	testPostgresArchiveIdentityAfterReload(t, true)
}

func testPostgresArchiveIdentityAfterReload(t *testing.T, objectStorage bool) {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())
	if objectStorage {
		t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
		t.Setenv("APEXLOAD_ARCHIVE_PREFIX", fmt.Sprintf("identity-%d", time.Now().UnixNano()))
	} else {
		t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	}

	databaseURL := os.Getenv("APEXLOAD_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("APEXLOAD_TEST_POSTGRES_URL is not set")
	}
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("archive_identity_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal("invalid PostgreSQL integration URL")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	store, err := storage.NewPostgresStore(parsed.String(), storage.StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, nanos := range []int{696289135, 696289999, 696290001} {
		t.Run(fmt.Sprint(nanos), func(t *testing.T) {
			created := time.Date(2026, 9, 26, 21, 48, 2, nanos, time.FixedZone("offset", -4*3600))
			id := fmt.Sprintf("precision-%d", nanos)
			record := &storage.TestRecord{ID: id, Name: id, Status: "COMPLETE", ProjectID: storage.DefaultProjectID, StartTime: created, TotalVUsers: 1, Duration: int64(time.Second), Groups: []any{}}
			if err := store.SaveTest(record); err != nil {
				t.Fatal(err)
			}
			durable, err := store.GetTest(id)
			if err != nil {
				t.Fatal(err)
			}
			if !durable.StartTime.Equal(created.Truncate(time.Microsecond)) {
				t.Fatalf("persisted %s, expected truncation of %s", durable.StartTime, created)
			}
			provenanceRunner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(newExecutionLeadership(store, "provenance")))
			provenance := buildRunArchiveProvenance(provenanceRunner, &ManagedTest{ID: id, ProjectID: storage.DefaultProjectID, CreatedAt: created})
			if !provenance.RequestedAt.Equal(durable.StartTime) {
				t.Fatalf("new provenance timestamp %s differs from durable timestamp %s", provenance.RequestedAt, durable.StartTime)
			}
			dir := t.TempDir()
			path := filepath.Join(dir, runArchiveManifestFileName)
			data := []byte("{\"count\":1}\n")
			if err := os.WriteFile(filepath.Join(dir, "transactions.jsonl"), data, 0600); err != nil {
				t.Fatal(err)
			}
			manifest := RunArchiveManifest{TransactionsFile: "transactions.jsonl", Artifacts: []RunArchiveArtifact{{Name: "transactions.jsonl", SizeBytes: int64(len(data)), SHA256: sha256BytesHex(data)}}, SchemaVersion: archiveSchemaVersion, TestID: id, Status: "COMPLETE", Provenance: RunArchiveProvenance{ProjectID: storage.DefaultProjectID, RequestedAt: created}}
			if err := writeIndentedJSONFile(path, manifest); err != nil {
				t.Fatal(err)
			}
			manifestBytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			archive := &storage.RunArchiveRecord{TestID: id, SchemaVersion: archiveSchemaVersion, ArchiveBackend: archiveBackendFilesystem, ArchivePath: dir, ManifestPath: path, ManifestSHA256: sha256BytesHex(manifestBytes), CreatedAt: created, CompletedAt: created.Add(time.Second)}
			if objectStorage {
				backend := currentArchiveBackend()
				archive.ArchiveBackend = archiveBackendS3
				archive.ArchiveURI = backend.runURI(id)
				archive.ManifestURI = backend.manifestURI(id)
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					if err := deleteArchiveBackendObjects(ctx, archive); err != nil {
						t.Error(err)
					}
				})
				uploadCtx, cancelUpload := context.WithTimeout(context.Background(), time.Minute)
				defer cancelUpload()
				if err := uploadArchiveDirectory(uploadCtx, dir, id); err != nil {
					t.Fatal(err)
				}
				// Force the read path to fetch and verify S3 objects, rather than
				// pass merely because the writer's staging files remain on disk.
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.SaveRunArchive(archive); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				connection, err := storage.NewPostgresStore(parsed.String(), storage.StoreConnectionOptions{}, false)
				if err != nil {
					t.Fatal(err)
				}
				server := NewServer(ServerConfig{Store: connection, HAExecutionLeadership: true})
				resp := httptest.NewRecorder()
				server.mux.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+id+"/archive/manifest", nil))
				connection.Close()
				if resp.Code != http.StatusOK {
					t.Fatalf("valid nanosecond archive rejected after PostgreSQL reload: HTTP %d: %s", resp.Code, resp.Body.String())
				}
			}
			corrupt := *archive
			corrupt.ManifestSHA256 = sha256BytesHex([]byte("different manifest"))
			if err := store.SaveRunArchive(&corrupt); err != nil {
				t.Fatal(err)
			}
			corruptReader := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(newExecutionLeadership(store, "corrupt-manifest")))
			if _, ok := corruptReader.GetRunArchiveContext(context.Background(), id); ok {
				t.Fatal("archive with corrupt checksum accepted")
			}
			if err := store.SaveRunArchive(archive); err != nil {
				t.Fatal(err)
			}
			record.StartTime = created.Add(time.Microsecond)
			if err := store.SaveTest(record); err != nil {
				t.Fatal(err)
			}
			reader := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(newExecutionLeadership(store, "reused-id")))
			if _, ok := reader.GetRunArchiveContext(context.Background(), id); ok {
				t.Fatal("archive for reused ID accepted")
			}
		})
	}
}
