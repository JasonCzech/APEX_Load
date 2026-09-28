package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestArchiveMaterializationBurstEnforcesBytesBeforePeriodicSweep(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "Burst-Test")
	t.Setenv("APEXLOAD_ARCHIVE_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", "when_required")
	t.Setenv("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")
	t.Setenv(envArchiveMaterializationCacheMaxBytes, "1500")
	objects := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := objects[r.URL.Path]
		if r.Method != http.MethodGet || !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	defer server.Close()
	t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", server.URL)
	var records []*storage.RunArchiveRecord
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("burst-%d", i)
		manifest, err := json.Marshal(RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: id, TestName: string(make([]byte, 90))})
		if err != nil {
			t.Fatal(err)
		}
		backend := currentArchiveBackend()
		record := &storage.RunArchiveRecord{TestID: id, ArchiveBackend: archiveBackendS3, ArchiveURI: backend.runURI(id), ManifestURI: backend.manifestURI(id), ManifestSHA256: sha256BytesHex(manifest)}
		objects["/Burst-Test/"+backend.Prefix+"/"+id+"/manifest.json"] = manifest
		records = append(records, record)
	}
	scanRoot := archiveMaterializationCacheScanRoot("")
	// Suppress both the coalesced wake and periodic sweep. New-download release
	// must synchronously bound this entire burst without advancing time.
	archiveMaterializations.mu.Lock()
	archiveMaterializations.lastRetentionSweep[scanRoot] = time.Now().Add(time.Hour)
	archiveMaterializations.mu.Unlock()
	defer func() {
		archiveMaterializations.mu.Lock()
		delete(archiveMaterializations.lastRetentionSweep, scanRoot)
		archiveMaterializations.mu.Unlock()
	}()
	var pinned *archiveMaterializationLease
	for i, record := range records {
		lease, err := acquireArchiveMaterialized(context.Background(), record)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			pinned = lease
			defer pinned.Release()
			continue
		}
		lease.Release()
		_, total := scanArchiveMaterializationCache(scanRoot, time.Now(), time.Hour)
		if total > 1500 {
			t.Fatalf("burst retained %d bytes despite 1500-byte cap", total)
		}
		if _, err := os.Stat(records[0].ManifestPath); err != nil {
			t.Fatalf("active reader evicted: %v", err)
		}
	}
	pinned.Release()
	// An archive evicted for bytes remains remotely readable and verified.
	read := *records[1]
	if _, err := os.Stat(filepath.Dir(read.ManifestPath)); !os.IsNotExist(err) {
		t.Fatalf("old idle cache retained: %v", err)
	}
	lease, err := acquireArchiveMaterialized(context.Background(), &read)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
}
