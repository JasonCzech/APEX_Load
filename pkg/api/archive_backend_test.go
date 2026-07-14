package api

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
	"gocloud.dev/blob/fileblob"
)

func TestCurrentArchiveBackendDefaultsToFilesystem(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)

	backend := currentArchiveBackend()
	if backend.Backend != archiveBackendFilesystem {
		t.Fatalf("backend = %q, want filesystem", backend.Backend)
	}
	if backend.LocalRoot != root {
		t.Fatalf("local root = %q, want %q", backend.LocalRoot, root)
	}
	if err := backend.validate(); err != nil {
		t.Fatalf("validate returned error: %v", err)
	}
}

func TestCurrentArchiveBackendBuildsObjectURIAndStagingRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "s3")
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "apexload-archives")
	t.Setenv("APEXLOAD_ARCHIVE_PREFIX", "/prod/evidence/")

	backend := currentArchiveBackend()
	if backend.Backend != archiveBackendS3 {
		t.Fatalf("backend = %q, want s3", backend.Backend)
	}
	wantRoot := archiveObjectNamespaceRoot(filepath.Join(root, "_object_staging", "s3"), backend)
	if backend.LocalRoot != wantRoot {
		t.Fatalf("local root = %q, want %q", backend.LocalRoot, wantRoot)
	}
	wantPrefix := "s3://apexload-archives/prod/evidence/_source/v1/" + archiveObjectSourceIdentity(archiveBackendRuntime{Backend: archiveBackendS3, Bucket: "apexload-archives"}, "prod/evidence")
	if got := backend.runURI("run-1"); got != wantPrefix+"/run-1" {
		t.Fatalf("run uri = %q", got)
	}
	if got := backend.manifestURI("run-1"); got != wantPrefix+"/run-1/manifest.json" {
		t.Fatalf("manifest uri = %q", got)
	}
}

func TestObjectArchiveStagingAndNamespacesSeparateBackendsAndEndpoints(t *testing.T) {
	customRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", customRoot)
	s3Root := objectArchiveStagingRoot("ignored", archiveBackendS3)
	gcsRoot := objectArchiveStagingRoot("ignored", archiveBackendGCS)
	if s3Root == gcsRoot {
		t.Fatalf("custom staging root aliases S3 and GCS at %q", s3Root)
	}
	if s3Root != filepath.Join(customRoot, archiveBackendS3) || gcsRoot != filepath.Join(customRoot, archiveBackendGCS) {
		t.Fatalf("backend staging roots = %q and %q, want backend-scoped children of %q", s3Root, gcsRoot, customRoot)
	}
	base := archiveBackendRuntime{Backend: archiveBackendS3, Bucket: "archives", Prefix: "prod/evidence"}
	first := base
	first.Endpoint = "https://objects-a.example.test/"
	second := base
	second.Endpoint = "https://objects-b.example.test"
	if archiveObjectNamespace(first) == archiveObjectNamespace(second) {
		t.Fatal("S3-compatible endpoints with the same bucket and prefix share a cache namespace")
	}
	azureA := archiveBackendRuntime{Backend: archiveBackendAzureBlob, Container: "archives", Prefix: "prod/evidence", Account: "account-a"}
	azureB := azureA
	azureB.Account = "account-b"
	if archiveObjectNamespace(azureA) == archiveObjectNamespace(azureB) {
		t.Fatal("Azure storage accounts with the same container and prefix share a cache namespace")
	}
}

func TestCurrentArchiveBackendValidatesRequiredObjectTarget(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "gcs")

	backend := currentArchiveBackend()
	if err := backend.validate(); err == nil {
		t.Fatal("expected missing bucket validation error")
	}
}

func TestUploadArchiveObjectsUploadsOnlyNamedFiles(t *testing.T) {
	localDir := t.TempDir()
	bucketDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(localDir, runArchiveManifestFileName), []byte(`{"test_id":"run-1"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDir, testNotesArchiveFileName), []byte(`{"notes":[]}`), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "transactions.jsonl"), []byte(`{"transaction":1}`), 0o644); err != nil {
		t.Fatalf("write transactions: %v", err)
	}
	bucket, err := fileblob.OpenBucket(bucketDir, nil)
	if err != nil {
		t.Fatalf("OpenBucket returned error: %v", err)
	}
	defer bucket.Close()
	backend := archiveBackendRuntime{Prefix: "prod/evidence"}

	if err := uploadArchiveObjectsToBucket(context.Background(), bucket, backend, localDir, "run-1", testNotesArchiveFileName, runArchiveManifestFileName); err != nil {
		t.Fatalf("uploadArchiveObjectsToBucket returned error: %v", err)
	}

	for _, name := range []string{runArchiveManifestFileName, testNotesArchiveFileName} {
		if _, err := os.Stat(filepath.Join(bucketDir, "prod", "evidence", "run-1", name)); err != nil {
			t.Fatalf("expected uploaded object %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(bucketDir, "prod", "evidence", "run-1", "transactions.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("transactions artifact upload err = %v, want not uploaded", err)
	}
}

func TestUploadArchiveObjectsPublishesManifestOnlyAfterArtifacts(t *testing.T) {
	localDir := t.TempDir()
	bucketDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(localDir, runArchiveManifestFileName), []byte(`{"test_id":"run-partial"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bucket, err := fileblob.OpenBucket(bucketDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bucket.Close()
	backend := archiveBackendRuntime{Prefix: "prod/evidence"}

	// Deliberately provide manifest first. The missing dataset must fail the
	// publication before manifest.json can become the remote commit marker.
	err = uploadArchiveObjectsToBucket(
		context.Background(), bucket, backend, localDir, "run-partial",
		runArchiveManifestFileName, "transactions.jsonl",
	)
	if err == nil {
		t.Fatal("expected missing artifact publication to fail")
	}
	manifestPath := filepath.Join(bucketDir, "prod", "evidence", "run-partial", runArchiveManifestFileName)
	if _, statErr := os.Stat(manifestPath); !os.IsNotExist(statErr) {
		t.Fatalf("manifest commit marker was published after partial failure: %v", statErr)
	}
}

func TestDeleteArchiveObjectsFromBucketDeletesOnlyRunPrefix(t *testing.T) {
	bucketDir := t.TempDir()
	bucket, err := fileblob.OpenBucket(bucketDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bucket.Close()
	ctx := context.Background()
	backend := archiveBackendRuntime{Prefix: "prod/evidence"}
	for _, object := range []string{
		backend.objectKey("run-1", "manifest.json"),
		backend.objectKey("run-1", "nested/transactions.jsonl"),
		backend.objectKey("run-2", "manifest.json"),
	} {
		if err := bucket.WriteAll(ctx, object, []byte(object), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := deleteArchiveObjectsFromBucket(ctx, bucket, backend, "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := bucket.ReadAll(ctx, backend.objectKey("run-1", "manifest.json")); err == nil {
		t.Fatal("run-1 manifest still exists")
	}
	if _, err := bucket.ReadAll(ctx, backend.objectKey("run-2", "manifest.json")); err != nil {
		t.Fatalf("neighboring run was deleted: %v", err)
	}
}

func TestArchiveDeletionRuntimeUsesPersistedObjectPrefix(t *testing.T) {
	backend := archiveBackendRuntime{Backend: archiveBackendS3, Bucket: "archives", Prefix: "new/prefix"}
	resolved, err := archiveDeletionRuntime(backend, &storage.RunArchiveRecord{
		TestID: "run-1", ArchiveBackend: archiveBackendS3, ArchiveURI: "s3://archives/old/prefix/run-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Prefix != "old/prefix" {
		t.Fatalf("prefix = %q, want persisted old/prefix", resolved.Prefix)
	}
	if _, err := archiveDeletionRuntime(backend, &storage.RunArchiveRecord{
		TestID: "run-1", ArchiveBackend: archiveBackendS3, ArchiveURI: "s3://other-bucket/old/prefix/run-1",
	}); err == nil {
		t.Fatal("expected configured bucket mismatch to be rejected")
	}
}

func TestArchiveMaterializationRuntimeUsesPersistedBackendAndURI(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	configured := archiveBackendRuntime{
		Backend: archiveBackendGCS,
		Bucket:  "current-bucket",
		Prefix:  "current/prefix",
		Region:  "us-east-1",
	}
	record := &storage.RunArchiveRecord{
		TestID:         "run-1",
		ArchiveBackend: archiveBackendS3,
		ArchiveURI:     "s3://historic-bucket/retained/2025/run-1",
		ManifestURI:    "s3://historic-bucket/retained/2025/run-1/manifest.json",
	}

	resolved, err := archiveMaterializationRuntime(configured, record)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Backend != archiveBackendS3 || resolved.Bucket != "historic-bucket" || resolved.Prefix != "retained/2025" {
		t.Fatalf("resolved runtime = %+v, want persisted S3 bucket/prefix", resolved)
	}
	if got := resolved.objectKey("run-1", "transactions.jsonl"); got != "retained/2025/run-1/transactions.jsonl" {
		t.Fatalf("object key = %q", got)
	}
}

func TestArchiveMaterializationRejectsChangedCustomEndpointNamespace(t *testing.T) {
	base := archiveBackendRuntime{Backend: archiveBackendS3, Bucket: "archives", Endpoint: "https://endpoint-a.example", Prefix: "prod/evidence"}
	identity := archiveObjectSourceIdentity(base, base.Prefix)
	record := &storage.RunArchiveRecord{
		TestID: "run-1", ArchiveBackend: archiveBackendS3,
		ArchiveURI:  "s3://archives/prod/evidence/_source/v1/" + identity + "/run-1",
		ManifestURI: "s3://archives/prod/evidence/_source/v1/" + identity + "/run-1/manifest.json",
	}
	changed := base
	changed.Endpoint = "https://endpoint-b.example"
	if _, err := archiveMaterializationRuntime(changed, record); err == nil || !strings.Contains(err.Error(), "source identity") {
		t.Fatalf("materialization error = %v, want source identity rejection", err)
	}
	resolved, err := archiveMaterializationRuntime(base, record)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Prefix != "prod/evidence/_source/v1/"+identity {
		t.Fatalf("resolved prefix = %q", resolved.Prefix)
	}
}

func TestZipDirectoryRejectsNestedSymlinkWithoutReadingTarget(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "nested", "artifact")); err != nil {
		t.Fatal(err)
	}
	var bundle bytes.Buffer
	if err := zipDirectory(root, &bundle); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("zip error = %v, want symbolic-link rejection", err)
	}
}

func TestArchiveFinalizationVersionURIIsImmutableAndIdempotent(t *testing.T) {
	const tokenA = "11111111-1111-4111-8111-111111111111"
	const tokenB = "22222222-2222-4222-8222-222222222222"
	record := &storage.RunArchiveRecord{
		TestID:         "run-versioned",
		ArchiveBackend: archiveBackendS3,
		ArchiveURI:     "s3://archives/prod/evidence/run-versioned",
		ManifestURI:    "s3://archives/prod/evidence/run-versioned/manifest.json",
	}
	if err := bindArchiveRecordToFinalizationVersion(record, tokenA); err != nil {
		t.Fatal(err)
	}
	wantA := "s3://archives/prod/evidence/_versions/v1/" + tokenA + "/run-versioned"
	if record.ArchiveURI != wantA || record.ManifestURI != wantA+"/manifest.json" {
		t.Fatalf("version A URIs = %q / %q", record.ArchiveURI, record.ManifestURI)
	}
	if err := bindArchiveRecordToFinalizationVersion(record, tokenA); err != nil {
		t.Fatal(err)
	}
	if record.ArchiveURI != wantA {
		t.Fatalf("same-token retry changed URI to %q", record.ArchiveURI)
	}
	if err := bindArchiveRecordToFinalizationVersion(record, tokenB); err != nil {
		t.Fatal(err)
	}
	wantB := "s3://archives/prod/evidence/_versions/v1/" + tokenB + "/run-versioned"
	if record.ArchiveURI != wantB {
		t.Fatalf("replacement intent URI = %q, want %q", record.ArchiveURI, wantB)
	}
	resolved, err := archiveMaterializationRuntime(archiveBackendRuntime{Backend: archiveBackendS3}, record)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved.objectKey(record.TestID, runArchiveManifestFileName); got != "prod/evidence/_versions/v1/"+tokenB+"/run-versioned/manifest.json" {
		t.Fatalf("versioned object key = %q", got)
	}
	if err := bindArchiveRecordToFinalizationVersion(record, "../escape"); err == nil {
		t.Fatal("unsafe intent token was accepted as an object namespace")
	}
}

func TestArchivePublicationUsesOnlyManifestDeclaredArtifacts(t *testing.T) {
	root := t.TempDir()
	testID := "manifest-declared-upload"
	data := []byte("declared\n")
	if err := os.WriteFile(filepath.Join(root, "transactions.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rogue.secret"), []byte("must-not-upload"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := RunArchiveManifest{
		SchemaVersion:    archiveSchemaVersion,
		TestID:           testID,
		TransactionsFile: "transactions.jsonl",
		Artifacts: []RunArchiveArtifact{{
			Name: "transactions.jsonl", SizeBytes: int64(len(data)), SHA256: sha256BytesHex(data),
		}},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, runArchiveManifestFileName)
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{
		TestID: testID, ArchiveBackend: archiveBackendS3, ArchivePath: root,
		ManifestPath: manifestPath, ManifestSHA256: sha256BytesHex(manifestData),
	}
	names, err := archivePublicationArtifactNames(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool, len(names))
	for _, name := range names {
		got[name] = true
	}
	if !got["transactions.jsonl"] || !got[runArchiveManifestFileName] || got["rogue.secret"] || len(got) != 2 {
		t.Fatalf("publication artifact set = %#v", names)
	}
}

func TestMaterializedArchiveRefreshUsesPrivateWritableStage(t *testing.T) {
	const refreshToken = "33333333-3333-4333-8333-333333333333"
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "archives")
	sourceRoot := t.TempDir()
	testID := "private-refresh-stage"
	artifactName := "transactions.jsonl"
	original := []byte("immutable-cache\n")
	if err := os.WriteFile(filepath.Join(sourceRoot, artifactName), original, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion, TestID: testID, TransactionsFile: artifactName,
		Artifacts: []RunArchiveArtifact{{Name: artifactName, SizeBytes: int64(len(original)), SHA256: sha256BytesHex(original)}},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(sourceRoot, runArchiveManifestFileName)
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	source := &storage.RunArchiveRecord{
		TestID: testID, ArchiveBackend: archiveBackendS3, ArchivePath: sourceRoot,
		ManifestPath: manifestPath, ManifestSHA256: sha256BytesHex(manifestData),
	}
	stagedRoot, stagedManifest, err := stageMaterializedArchiveForPublication(context.Background(), source, refreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if stagedRoot == sourceRoot || filepath.Base(stagedRoot) != testID || !strings.Contains(stagedRoot, filepath.Join("_publication", refreshToken)) {
		t.Fatalf("private stage root = %q", stagedRoot)
	}
	if stagedManifest != filepath.Join(stagedRoot, runArchiveManifestFileName) {
		t.Fatalf("private stage manifest = %q", stagedManifest)
	}
	if err := os.WriteFile(filepath.Join(stagedRoot, artifactName), []byte("refreshed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(sourceRoot, artifactName))
	if err != nil || string(got) != string(original) {
		t.Fatalf("materialization cache was changed through private stage: %q, %v", got, err)
	}
}

func TestVersionedArchiveDeletionRemovesOnlyPersistedVersion(t *testing.T) {
	const tokenA = "44444444-4444-4444-8444-444444444444"
	const tokenB = "55555555-5555-4555-8555-555555555555"
	bucket, err := fileblob.OpenBucket(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bucket.Close()
	ctx := context.Background()
	backend := archiveBackendRuntime{Backend: archiveBackendS3, Bucket: "archives", Prefix: "prod/evidence"}
	versionA := &storage.RunArchiveRecord{TestID: "run-delete-version", ArchiveBackend: archiveBackendS3, ArchiveURI: "s3://archives/prod/evidence/run-delete-version"}
	versionB := *versionA
	if err := bindArchiveRecordToFinalizationVersion(versionA, tokenA); err != nil {
		t.Fatal(err)
	}
	if err := bindArchiveRecordToFinalizationVersion(&versionB, tokenB); err != nil {
		t.Fatal(err)
	}
	runtimeA, err := archiveDeletionRuntime(backend, versionA)
	if err != nil {
		t.Fatal(err)
	}
	runtimeB, err := archiveDeletionRuntime(backend, &versionB)
	if err != nil {
		t.Fatal(err)
	}
	keyA := runtimeA.objectKey(versionA.TestID, runArchiveManifestFileName)
	keyB := runtimeB.objectKey(versionB.TestID, runArchiveManifestFileName)
	if err := bucket.WriteAll(ctx, keyA, []byte("a"), nil); err != nil {
		t.Fatal(err)
	}
	if err := bucket.WriteAll(ctx, keyB, []byte("b"), nil); err != nil {
		t.Fatal(err)
	}
	if err := deleteArchiveObjectsFromBucket(ctx, bucket, runtimeA, versionA.TestID); err != nil {
		t.Fatal(err)
	}
	if _, err := bucket.ReadAll(ctx, keyA); err == nil {
		t.Fatal("persisted version was not deleted")
	}
	if got, err := bucket.ReadAll(ctx, keyB); err != nil || string(got) != "b" {
		t.Fatalf("neighboring version was deleted: %q, %v", got, err)
	}
}

func TestArchiveMaterializationRuntimeRejectsInconsistentPersistedURI(t *testing.T) {
	configured := archiveBackendRuntime{Backend: archiveBackendS3}
	for _, record := range []*storage.RunArchiveRecord{
		{TestID: "run-1", ArchiveBackend: archiveBackendS3, ArchiveURI: "gs://bucket/prefix/run-1"},
		{TestID: "run-1", ArchiveBackend: archiveBackendS3, ArchiveURI: "s3://bucket/prefix/another-run"},
		{TestID: "run-1", ArchiveBackend: archiveBackendS3, ArchiveURI: "s3://bucket/prefix/run-1", ManifestURI: "s3://other/prefix/run-1/manifest.json"},
		{TestID: "run-1", ArchiveBackend: archiveBackendS3, ArchiveURI: "s3://bucket/prefix/../run-1"},
		{TestID: "run-1", ArchiveBackend: archiveBackendS3, ArchiveURI: "s3://bucket/prefix//run-1"},
		{TestID: "run-1", ArchiveBackend: archiveBackendS3, ArchiveURI: "s3://bucket/prefix%2Frun-1"},
	} {
		if _, err := archiveMaterializationRuntime(configured, record); err == nil {
			t.Fatalf("archiveMaterializationRuntime(%+v) unexpectedly succeeded", record)
		}
	}
}

func TestArchiveMaterializationLocalRootCannotTargetAnotherRun(t *testing.T) {
	root := t.TempDir()
	otherRun := filepath.Join(root, "run-b")
	if err := os.MkdirAll(otherRun, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := archiveMaterializationLocalRoot(
		archiveBackendRuntime{LocalRoot: root},
		&storage.RunArchiveRecord{TestID: "run-a", ArchivePath: otherRun},
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(root, "run-a") {
		t.Fatalf("materialization root = %q, want run-scoped path", resolved)
	}
}

func TestMaterializeArchiveFromBucketReplacesPoisonedCacheAtomically(t *testing.T) {
	ctx := context.Background()
	bucketDir := t.TempDir()
	bucket, err := fileblob.OpenBucket(bucketDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bucket.Close()

	backend := archiveBackendRuntime{Prefix: "historic/prefix", LocalRoot: t.TempDir()}
	artifactName := "transactions.jsonl"
	artifactData := []byte("{\"transaction\":1}\n")
	manifest := RunArchiveManifest{
		SchemaVersion:    archiveSchemaVersion,
		TestID:           "run-atomic",
		TransactionsFile: artifactName,
		Artifacts: []RunArchiveArtifact{{
			Name: artifactName, SizeBytes: int64(len(artifactData)), SHA256: sha256BytesHex(artifactData),
		}},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{runArchiveManifestFileName: manifestData, artifactName: artifactData} {
		if err := bucket.WriteAll(ctx, backend.objectKey(manifest.TestID, name), data, nil); err != nil {
			t.Fatal(err)
		}
	}
	finalRoot := filepath.Join(backend.LocalRoot, manifest.TestID)
	if err := os.MkdirAll(finalRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// Simulate an old manifest-first materializer that failed before the data
	// artifact arrived. The new materializer must not accept this poisoned cache.
	if err := os.WriteFile(filepath.Join(finalRoot, runArchiveManifestFileName), manifestData, 0o644); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{
		TestID: manifest.TestID, ArchiveBackend: archiveBackendS3,
		ArchivePath: finalRoot, ManifestPath: filepath.Join(finalRoot, runArchiveManifestFileName),
		ManifestSHA256: sha256BytesHex(manifestData),
	}
	if err := materializeArchiveFromBucket(ctx, bucket, backend, record); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(finalRoot, artifactName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(artifactData) {
		t.Fatalf("materialized artifact = %q", got)
	}
	if err := validateMaterializedArchive(record, finalRoot, record.ManifestPath); err != nil {
		t.Fatalf("materialized cache failed verification: %v", err)
	}
	if _, err := os.Stat(archiveMaterializationCacheMarkerPath(finalRoot)); err != nil {
		t.Fatalf("materialized cache marker was not committed: %v", err)
	}
	entries, err := os.ReadDir(backend.LocalRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".materializing-") || strings.Contains(entry.Name(), ".previous-") {
			t.Fatalf("temporary materialization directory was not cleaned up: %s", entry.Name())
		}
	}
}

func TestMaterializeArchiveFromBucketFailureCannotCommitPartialCache(t *testing.T) {
	ctx := context.Background()
	bucketDir := t.TempDir()
	bucket, err := fileblob.OpenBucket(bucketDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bucket.Close()
	backend := archiveBackendRuntime{Prefix: "archives", LocalRoot: t.TempDir()}
	artifactData := []byte("expected")
	manifest := RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion,
		TestID:        "run-retry",
		Artifacts: []RunArchiveArtifact{{
			Name: "nested/data.jsonl", SizeBytes: int64(len(artifactData)), SHA256: sha256BytesHex(artifactData),
		}},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := bucket.WriteAll(ctx, backend.objectKey(manifest.TestID, runArchiveManifestFileName), manifestData, nil); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{TestID: manifest.TestID, ManifestSHA256: sha256BytesHex(manifestData)}
	if err := materializeArchiveFromBucket(ctx, bucket, backend, record); err == nil {
		t.Fatal("expected missing artifact download to fail")
	}
	finalRoot := filepath.Join(backend.LocalRoot, manifest.TestID)
	if _, err := os.Stat(filepath.Join(finalRoot, runArchiveManifestFileName)); !os.IsNotExist(err) {
		t.Fatalf("partial cache exposed a manifest commit marker: %v", err)
	}
	if err := bucket.WriteAll(ctx, backend.objectKey(manifest.TestID, "nested/data.jsonl"), artifactData, nil); err != nil {
		t.Fatal(err)
	}
	if err := materializeArchiveFromBucket(ctx, bucket, backend, record); err != nil {
		t.Fatalf("retry after transient failure: %v", err)
	}
	if _, err := os.Stat(record.ManifestPath); err != nil {
		t.Fatalf("successful retry did not commit manifest: %v", err)
	}
}

func TestMaterializeArchiveFromBucketRejectsHashAndSizeMismatch(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		size       int64
		hash       string
		wantDetail string
	}{
		{name: "size", size: 99, hash: sha256BytesHex([]byte("actual")), wantDetail: "size"},
		{name: "hash", size: int64(len("actual")), hash: sha256BytesHex([]byte("different")), wantDetail: "SHA-256"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bucket, err := fileblob.OpenBucket(t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer bucket.Close()
			backend := archiveBackendRuntime{Prefix: "archives", LocalRoot: t.TempDir()}
			manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: "run-bad-" + tc.name, Artifacts: []RunArchiveArtifact{{Name: "data.jsonl", SizeBytes: tc.size, SHA256: tc.hash}}}
			manifestData, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err := bucket.WriteAll(ctx, backend.objectKey(manifest.TestID, runArchiveManifestFileName), manifestData, nil); err != nil {
				t.Fatal(err)
			}
			if err := bucket.WriteAll(ctx, backend.objectKey(manifest.TestID, "data.jsonl"), []byte("actual"), nil); err != nil {
				t.Fatal(err)
			}
			record := &storage.RunArchiveRecord{TestID: manifest.TestID, ManifestSHA256: sha256BytesHex(manifestData)}
			err = materializeArchiveFromBucket(ctx, bucket, backend, record)
			if err == nil || !strings.Contains(err.Error(), tc.wantDetail) {
				t.Fatalf("error = %v, want %q mismatch", err, tc.wantDetail)
			}
			if _, statErr := os.Stat(filepath.Join(backend.LocalRoot, manifest.TestID, runArchiveManifestFileName)); !os.IsNotExist(statErr) {
				t.Fatalf("invalid archive was committed: %v", statErr)
			}
		})
	}
}

func TestMaterializeArchiveFromBucketEnforcesTotalByteLimit(t *testing.T) {
	ctx := context.Background()
	bucket, err := fileblob.OpenBucket(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bucket.Close()
	backend := archiveBackendRuntime{Prefix: "archives", LocalRoot: t.TempDir()}
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: "run-byte-limit", Artifacts: []RunArchiveArtifact{{Name: "data.bin"}}}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envArchiveMaterializedMaxBytes, "1024")
	if int64(len(manifestData)) >= configuredArchiveMaterializationMaxBytes() {
		t.Fatalf("test manifest unexpectedly exceeds configured limit: %d", len(manifestData))
	}
	for name, data := range map[string][]byte{
		runArchiveManifestFileName: manifestData,
		"data.bin":                 make([]byte, configuredArchiveMaterializationMaxBytes()),
	} {
		if err := bucket.WriteAll(ctx, backend.objectKey(manifest.TestID, name), data, nil); err != nil {
			t.Fatal(err)
		}
	}
	record := &storage.RunArchiveRecord{TestID: manifest.TestID, ManifestSHA256: sha256BytesHex(manifestData)}
	err = materializeArchiveFromBucket(ctx, bucket, backend, record)
	if err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatalf("materialization error = %v, want byte limit", err)
	}
	if _, statErr := os.Stat(filepath.Join(backend.LocalRoot, manifest.TestID, runArchiveManifestFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("over-limit archive exposed a manifest: %v", statErr)
	}
}

func TestValidateMaterializedArchiveRejectsSymlinksAndOversizedManifest(t *testing.T) {
	record := &storage.RunArchiveRecord{TestID: "run-local-validation"}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("sensitive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "transactions.jsonl")); err != nil {
		t.Fatal(err)
	}
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: record.TestID, TransactionsFile: "transactions.jsonl"}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, runArchiveManifestFileName)
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateMaterializedArchive(record, root, manifestPath); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink artifact validation error = %v, want regular-file rejection", err)
	}
	symlinkRoot := filepath.Join(t.TempDir(), "linked-cache")
	if err := os.Symlink(root, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	if err := validateMaterializedArchive(record, symlinkRoot, filepath.Join(symlinkRoot, runArchiveManifestFileName)); err == nil || !strings.Contains(err.Error(), "root is not") {
		t.Fatalf("symlink cache-root validation error = %v, want root rejection", err)
	}
	if err := os.Remove(filepath.Join(root, "transactions.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, make([]byte, maxArchiveManifestDownloadBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateMaterializedArchive(record, root, manifestPath); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized local manifest validation error = %v, want bounded-read rejection", err)
	}
}

func TestDecodeMaterializationManifestRequiresRunBinding(t *testing.T) {
	data, err := json.Marshal(RunArchiveManifest{SchemaVersion: archiveSchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeMaterializationManifest(&storage.RunArchiveRecord{TestID: "bound-run"}, data); err == nil {
		t.Fatal("materialization accepted a manifest without a test id")
	}
}

func TestAtomicReplaceArchiveDirectoryRestoresPreviousArchiveOnCommitFailure(t *testing.T) {
	parent := t.TempDir()
	finalRoot := filepath.Join(parent, "run-existing")
	if err := os.MkdirAll(finalRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(finalRoot, "manifest.json")
	if err := os.WriteFile(sentinel, []byte("trusted-existing-archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	missingStagingRoot := filepath.Join(parent, "missing-staging-root")
	if err := atomicReplaceArchiveDirectory(missingStagingRoot, finalRoot); err == nil {
		t.Fatal("atomicReplaceArchiveDirectory succeeded with a missing staging directory")
	}
	content, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("previous archive was not restored: %v", err)
	}
	if string(content) != "trusted-existing-archive" {
		t.Fatalf("restored archive content = %q", content)
	}
}

func TestUploadArchiveObjectsRejectsUnsafeRunIDAndLinkedArtifact(t *testing.T) {
	bucketDir := t.TempDir()
	bucket, err := fileblob.OpenBucket(bucketDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer bucket.Close()
	backend := archiveBackendRuntime{Prefix: "archives"}

	missingRoot := filepath.Join(t.TempDir(), "missing-root")
	if err := uploadArchiveObjectsToBucket(context.Background(), bucket, backend, missingRoot, "../escape", "manifest.json"); err == nil {
		t.Fatal("upload accepted a traversal test id")
	}
	if _, err := os.Lstat(missingRoot); !os.IsNotExist(err) {
		t.Fatalf("unsafe test id caused local filesystem I/O: %v", err)
	}

	localRoot := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside-manifest.json")
	if err := os.WriteFile(outside, []byte("outside-sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(localRoot, runArchiveManifestFileName)); err != nil {
		t.Fatal(err)
	}
	if err := uploadArchiveObjectsToBucket(context.Background(), bucket, backend, localRoot, "safe-run", runArchiveManifestFileName); err == nil {
		t.Fatal("upload followed a linked archive artifact")
	}
	if _, err := bucket.ReadAll(context.Background(), backend.objectKey("safe-run", runArchiveManifestFileName)); err == nil {
		t.Fatal("linked archive artifact was published")
	}
	content, err := os.ReadFile(outside)
	if err != nil || string(content) != "outside-sentinel" {
		t.Fatalf("linked upload source changed: %q, %v", content, err)
	}
}

func TestRemoveLocalArchiveDirectoryRequiresRealContainedDirectory(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	linkedRun := filepath.Join(archiveRoot, "linked-run")
	if err := os.Symlink(outside, linkedRun); err != nil {
		t.Fatal(err)
	}
	if err := removeLocalArchiveDirectory(linkedRun); err == nil {
		t.Fatal("removeLocalArchiveDirectory accepted a linked run root")
	}
	if content, err := os.ReadFile(sentinel); err != nil || string(content) != "keep" {
		t.Fatalf("linked deletion removed outside data: %q, %v", content, err)
	}

	linkedParent := filepath.Join(archiveRoot, "linked-parent")
	if err := os.Symlink(outside, linkedParent); err != nil {
		t.Fatal(err)
	}
	if err := removeLocalArchiveDirectory(filepath.Join(linkedParent, "nested-run")); err == nil {
		t.Fatal("removeLocalArchiveDirectory accepted a linked intermediate directory")
	}
	if content, err := os.ReadFile(sentinel); err != nil || string(content) != "keep" {
		t.Fatalf("linked intermediate deletion removed outside data: %q, %v", content, err)
	}

	realRun := filepath.Join(archiveRoot, "real-run")
	neighbor := filepath.Join(archiveRoot, "neighbor")
	if err := os.Mkdir(realRun, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(neighbor, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeLocalArchiveDirectory(realRun); err != nil {
		t.Fatalf("remove contained archive: %v", err)
	}
	if _, err := os.Lstat(realRun); !os.IsNotExist(err) {
		t.Fatalf("contained run was not removed: %v", err)
	}
	if info, err := os.Lstat(neighbor); err != nil || !info.IsDir() {
		t.Fatalf("neighboring archive was affected: %v", err)
	}
}

func TestAtomicReplaceArchiveDirectoryRejectsLinkedOrNonDirectoryFinalRoot(t *testing.T) {
	for _, kind := range []string{"symlink", "file"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			staging := filepath.Join(parent, "staging")
			if err := os.Mkdir(staging, 0o755); err != nil {
				t.Fatal(err)
			}
			final := filepath.Join(parent, "run-final")
			outside := t.TempDir()
			sentinel := filepath.Join(outside, "sentinel")
			if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				if err := os.Symlink(outside, final); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(final, []byte("keep-file"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := atomicReplaceArchiveDirectory(staging, final); err == nil {
				t.Fatalf("atomic replacement accepted %s final root", kind)
			}
			if content, err := os.ReadFile(sentinel); err != nil || string(content) != "keep" {
				t.Fatalf("atomic replacement affected linked target: %q, %v", content, err)
			}
			if info, err := os.Lstat(staging); err != nil || !info.IsDir() {
				t.Fatalf("rejected replacement consumed staging root: %v", err)
			}
		})
	}
}
