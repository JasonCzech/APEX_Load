package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type notificationCatalogFailureStore struct {
	SQLiteStore *storage.SQLiteStore
	fail        bool
}

func (s *notificationCatalogFailureStore) CompleteArchiveFinalizationWithTelemetry(id, token, refresh string, record *storage.RunArchiveRecord, identity storage.ArchiveFinalizationRunIdentity, publication storage.TelemetryNotificationPublication) (bool, error) {
	if s.fail {
		return false, errors.New("injected atomic notification/catalog failure")
	}
	return s.SQLiteStore.CompleteArchiveFinalizationWithTelemetry(id, token, refresh, record, identity, publication)
}

func TestTelemetryNotificationArchiveCommitUsesFinalManifestAndRecoversSourceLoss(t *testing.T) {
	for _, mode := range []string{"local", "uploaded source lost"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			var mu sync.Mutex
			objects := map[string][]byte{}
			if mode == "uploaded source lost" {
				remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					switch r.Method {
					case http.MethodPut:
						data, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
						if err != nil {
							t.Error(err)
							w.WriteHeader(400)
							return
						}
						objects[r.URL.Path] = data
						w.Header().Set("ETag", `"fixture"`)
					case http.MethodGet:
						data, ok := objects[r.URL.Path]
						if !ok {
							http.NotFound(w, r)
							return
						}
						_, _ = w.Write(data)
					default:
						w.WriteHeader(405)
					}
				}))
				t.Cleanup(remote.Close)
				t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
				t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "Notification-Test")
				t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", remote.URL)
				t.Setenv("APEXLOAD_ARCHIVE_REGION", "us-east-1")
				t.Setenv("AWS_ACCESS_KEY_ID", "test")
				t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
				t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
				t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", "when_required")
				t.Setenv("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")
			}
			db, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			fault := &notificationCatalogFailureStore{SQLiteStore: db, fail: true}
			store := sqliteOverlay(fault)
			run, record := createArchiveFinalizationTest(t, store, "notification-final-manifest", root)
			data, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var manifest RunArchiveManifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			acceptance := acceptanceManifest(TelemetryAcceptancePolicyDefinition{Version: 1, Mode: "advisory", MinWallTimeCoverage: acceptanceRatio(.9)})
			manifest.Provenance = acceptance.Provenance
			manifest.Provenance.RunIncarnation = "atomic-notification-incarnation"
			manifest.Provenance.ProjectID = storage.DefaultProjectID
			manifest.TelemetryCoverage = coverageRecord(70000, 100000)
			run.initialArchiveProvenance = &manifest.Provenance
			row, err := store.GetTest(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			row.InitialProvenance = manifest.Provenance
			if err := store.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			if err := writeIndentedJSONFile(record.ManifestPath, manifest); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			record.ManifestSHA256 = sha256BytesHex(data)
			if mode == "uploaded source lost" {
				record.ArchiveBackend = archiveBackendS3
				record.ArchiveURI = currentArchiveBackend().runURI(run.ID)
				record.ManifestURI = currentArchiveBackend().manifestURI(run.ID)
			}
			server := NewServer(ServerConfig{Store: store})
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := server.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			if err := server.runner.finalizeRunArchiveDurably(context.Background(), run, record, true); err == nil || !strings.Contains(err.Error(), "injected atomic") {
				t.Fatal("expected interrupted catalog commit", err)
			}
			if _, err := store.GetRunArchive(run.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("archive escaped failed atomic commit", err)
			}
			if _, err := store.GetTelemetryNotificationPublication(context.Background(), run.ProjectID, run.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("notification escaped failed atomic commit", err)
			}
			pending, err := store.GetArchiveFinalizationJob(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			var finalBytes []byte
			if mode == "uploaded source lost" {
				runtime, err := archiveMaterializationRuntime(currentArchiveBackend(), &pending.ArchiveRecord)
				if err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				finalBytes = append([]byte(nil), objects["/Notification-Test/"+runtime.objectKey(run.ID, runArchiveManifestFileName)]...)
				mu.Unlock()
				if err := os.RemoveAll(record.ArchivePath); err != nil {
					t.Fatal(err)
				}
			} else {
				finalBytes, err = os.ReadFile(pending.ArchiveRecord.ManifestPath)
				if err != nil {
					t.Fatal(err)
				}
			}
			if sha256BytesHex(finalBytes) != pending.ArchiveRecord.ManifestSHA256 {
				t.Fatal("fixture lost immutable publication")
			}
			var finalManifest RunArchiveManifest
			if err := json.Unmarshal(finalBytes, &finalManifest); err != nil {
				t.Fatal(err)
			}
			// Use a fresh runner to exercise only the durable intent and exact source.
			replacement := NewTestRunner(1, NewHub(), store)
			replacement.telemetryNotificationPublication = server.telemetryNotificationPublication
			fault.fail = false
			if err := replacement.processArchiveFinalizationJob(context.Background(), pending); err != nil {
				t.Fatal("recovered completion", err)
			}
			state, err := store.GetTelemetryNotificationPublication(context.Background(), run.ProjectID, run.ID)
			if err != nil || state.EventID == "" || state.Decision != "no_recipients" {
				t.Fatal("notification not committed with archive", state, err)
			}
			if mode == "local" {
				data, readErr := os.ReadFile(pending.ArchiveRecord.ManifestPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if err := json.Unmarshal(data, &finalManifest); err != nil {
					t.Fatal(err)
				}
			}
			expected, err := server.telemetryNotificationPublication(context.Background(), run.ID, &finalManifest)
			if err != nil || expected.Event == nil {
				t.Fatal("final assessment", expected, err)
			}
			expectedID, err := storage.TelemetryNotificationID(*expected.Event)
			if err != nil || expectedID != state.EventID {
				t.Fatal("notification used a different manifest", state.EventID, expectedID, err)
			}
			if state.ManifestSHA256 != pending.ArchiveRecord.ManifestSHA256 {
				t.Fatal("decision lost publication identity")
			}
			if _, err := store.GetArchiveFinalizationJob(run.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("publication intent remained", err)
			}
		})
	}
}
