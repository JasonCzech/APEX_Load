//go:build postgres_integration

package api

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/jasonczech/apexload/pkg/storage"
)

var errInjectedPostgresCatalogInterruption = errors.New("injected catalog interruption")

type failingPostgresArchiveCatalog struct {
	*storage.PostgresStore
}

func (s *failingPostgresArchiveCatalog) CompleteArchiveFinalizationForRun(id, token, refresh string, record *storage.RunArchiveRecord, identity storage.ArchiveFinalizationRunIdentity) (bool, error) {
	return false, errInjectedPostgresCatalogInterruption
}

// This exercises two independent processes and a shared POSIX staging tree with
// PostgreSQL and S3. It does not test a cross-node CSI filesystem or node loss.
func TestPostgresS3SharedStagingPublicationReplay(t *testing.T) {
	if os.Getenv("APEXLOAD_TEST_POSTGRES_URL") == "" || os.Getenv("APEXLOAD_TEST_ARCHIVE_S3") != "1" {
		t.Skip("PostgreSQL and S3 integration endpoints are required")
	}
	for _, name := range []string{"APEXLOAD_ARCHIVE_ENDPOINT", "APEXLOAD_ARCHIVE_BUCKET", "APEXLOAD_ARCHIVE_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			t.Fatalf("%s must be set for the isolated S3 replay test", name)
		}
	}
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_READER_CACHE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_PREFIX", fmt.Sprintf("staging-replay-%d", time.Now().UnixNano()))
	t.Setenv("APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL", "")
	client := s3.New(s3.Options{
		Region:       os.Getenv("APEXLOAD_ARCHIVE_REGION"),
		Credentials:  credentials.NewStaticCredentialsProvider(os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"), os.Getenv("AWS_SESSION_TOKEN")),
		BaseEndpoint: aws.String(os.Getenv("APEXLOAD_ARCHIVE_ENDPOINT")),
		UsePathStyle: true,
	})
	createCtx, stopCreate := context.WithTimeout(context.Background(), 10*time.Second)
	_, createErr := client.CreateBucket(createCtx, &s3.CreateBucketInput{Bucket: aws.String(os.Getenv("APEXLOAD_ARCHIVE_BUCKET"))})
	stopCreate()
	if createErr != nil {
		var apiErr smithy.APIError
		if !errors.As(createErr, &apiErr) || (apiErr.ErrorCode() != "BucketAlreadyOwnedByYou" && apiErr.ErrorCode() != "BucketAlreadyExists") {
			t.Fatalf("prepare isolated S3 bucket: %v", createErr)
		}
	}

	databaseURL := os.Getenv("APEXLOAD_TEST_POSTGRES_URL")
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("archive_replay_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
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
	t.Setenv("APEXLOAD_TEST_STAGING_POSTGRES_URL", parsed.String())
	store, err := storage.NewPostgresStore(parsed.String(), storage.StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	id := "shared-stage-replay"
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartTime: now, EndTime: &now}); err != nil {
		t.Fatal(err)
	}
	root := resolveArchiveRoot()
	archivePath := filepath.Join(root, id)
	if err := os.MkdirAll(archivePath, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(archivePath, runArchiveManifestFileName)
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: id, TestName: id, Status: "COMPLETE", CreatedAt: now, CompletedAt: now}
	if err := writeIndentedJSONFile(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	backend := currentArchiveBackend()
	record := storage.RunArchiveRecord{TestID: id, SchemaVersion: archiveSchemaVersion, ArchiveBackend: archiveBackendS3, ArchivePath: archivePath, ManifestPath: manifestPath, ManifestSHA256: sha256BytesHex(data), ArchiveURI: backend.runURI(id), ManifestURI: backend.manifestURI(id), CreatedAt: now, CompletedAt: now}
	if err := store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: id, ProjectID: storage.DefaultProjectID, ArchiveRecord: record, PersistArtifacts: true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_TEST_STAGING_RUN", id)
	child := func(mode string) (*exec.Cmd, context.CancelFunc) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPostgresS3SharedStagingProcessHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "APEXLOAD_TEST_STAGING_POSTGRES_MODE="+mode)
		return cmd, cancel
	}
	holder, stopHolder := child("interrupted-owner")
	defer stopHolder()
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	holder.Stderr = &stderr
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = holder.Process.Kill()
			_ = holder.Wait()
		}
		if t.Failed() {
			t.Log(stderr.String())
		}
	}()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "source-locked" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("publisher did not acquire source lock")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("publisher did not reach injected interruption")
	}
	pending, err := store.GetArchiveFinalizationJob(id)
	if err != nil || pending.PreparedManifestJSON == "" {
		t.Fatalf("prepared publication was not durable: %v", err)
	}
	if _, err := store.GetRunArchive(id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("catalog committed before injected interruption: %v", err)
	}
	lockPath := filepath.Join(root, "_source_locks", sha256BytesHex([]byte(id))+".lock")
	lockBefore, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	blockedCtx, stopBlocked := context.WithTimeout(context.Background(), 150*time.Millisecond)
	err = NewTestRunner(1, NewHub(), store).processArchiveFinalizationJob(blockedCtx, pending)
	stopBlocked()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent publisher bypassed source lock: %v", err)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("blocked publisher changed staged source")
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := holder.Wait(); err == nil {
		t.Fatal("interrupted publisher exited successfully")
	}
	waited = true
	lockAfter, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(lockBefore, lockAfter) {
		t.Fatal("source lock inode changed after owner loss")
	}
	for i := 0; i < 2; i++ {
		fresh, stop := child("recover")
		out, err := fresh.CombinedOutput()
		stop()
		if err != nil {
			t.Fatalf("fresh process replay %d: %v\n%s", i, err, out)
		}
		catalog, err := store.GetRunArchive(id)
		if err != nil || catalog == nil {
			t.Fatalf("catalog missing after replay %d: %v", i, err)
		}
		if strings.TrimSpace(catalog.ManifestSHA256) == "" {
			t.Fatal("catalog has no manifest digest")
		}
		readCtx, stopRead := context.WithTimeout(context.Background(), 20*time.Second)
		lease, err := acquireArchiveMaterialized(readCtx, catalog)
		stopRead()
		if err != nil {
			t.Fatal(err)
		}
		materialized, err := os.ReadFile(catalog.ManifestPath)
		lease.Release()
		if err != nil || sha256BytesHex(materialized) != catalog.ManifestSHA256 {
			t.Fatalf("S3 manifest disagrees with catalog after replay %d: %v", i, err)
		}
		if i == 0 {
			before = materialized
		} else if !bytes.Equal(before, materialized) {
			t.Fatal("idempotent replay changed archive manifest")
		}
	}
	if _, err := store.GetArchiveFinalizationJob(id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("publication intent remains: %v", err)
	}
	row, err := store.GetTest(id)
	if err != nil || row.Status != "COMPLETE" || row.EndTime == nil || !row.EndTime.Equal(now) {
		t.Fatalf("terminal row changed during replay: %v", err)
	}
}

func TestPostgresS3SharedStagingProcessHelper(t *testing.T) {
	mode := os.Getenv("APEXLOAD_TEST_STAGING_POSTGRES_MODE")
	if mode == "" {
		return
	}
	store, err := storage.NewPostgresStore(os.Getenv("APEXLOAD_TEST_STAGING_POSTGRES_URL"), storage.StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := os.Getenv("APEXLOAD_TEST_STAGING_RUN")
	job, err := store.GetArchiveFinalizationJob(id)
	if errors.Is(err, sql.ErrNoRows) && mode == "recover" {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if mode == "recover" {
		if err := NewTestRunner(1, NewHub(), store).processArchiveFinalizationJob(context.Background(), job); err != nil {
			t.Fatal(err)
		}
		return
	}
	if mode != "interrupted-owner" {
		t.Fatal("invalid helper mode")
	}
	if err := NewTestRunner(1, NewHub(), &failingPostgresArchiveCatalog{store}).processArchiveFinalizationJob(context.Background(), job); !errors.Is(err, errInjectedPostgresCatalogInterruption) {
		t.Fatalf("publisher failed before injected catalog interruption: %v", err)
	}
	release, err := lockArchivePublicationSource(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fmt.Println("source-locked")
	<-time.After(time.Minute)
	t.Fatal("parent did not stop interrupted publisher")
}
