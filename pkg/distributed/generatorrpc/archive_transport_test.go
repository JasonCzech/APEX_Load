package generatorrpc

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestStreamArchiveArtifactTransfersBeyondUnaryLimit(t *testing.T) {
	const artifactSize = int64(66<<20 + 123)

	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	service := NewService("generator-large", 10)
	archiveDir := filepath.Join(service.generatorArchiveRoot(), "large-run")
	if err := os.Mkdir(archiveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(archiveDir, "transactions.jsonl")
	file, err := os.Create(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(artifactSize); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"test_id":"large-run"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	service.storeArchiveRecord(&storage.RunArchiveRecord{
		TestID:       "large-run",
		ArchivePath:  archiveDir,
		ManifestPath: manifestPath,
	})
	listener := bufconn.Listen(2 << 20)
	server := grpc.NewServer()
	pb.RegisterGeneratorControlServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(conn)
	t.Cleanup(func() { _ = client.Close() })

	request := ArchiveArtifactRequest{TestID: "large-run", Name: "transactions.jsonl"}
	if _, err := client.GetArchiveArtifact(ctx, request); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("unary artifact error = %v, want ResourceExhausted", err)
	}
	copied, err := client.StreamArchiveArtifact(ctx, request, io.Discard, artifactSize+1)
	if err != nil {
		t.Fatalf("StreamArchiveArtifact: %v", err)
	}
	if copied != artifactSize {
		t.Fatalf("streamed bytes = %d, want %d", copied, artifactSize)
	}

	copied, err = client.StreamArchiveArtifact(ctx, request, io.Discard, 64<<20)
	if err == nil {
		t.Fatal("stream unexpectedly ignored controller byte limit")
	}
	if copied != 0 {
		t.Fatalf("limited stream wrote %d bytes before rejecting declared size, want 0", copied)
	}

	// The application-level unary limit is 64 MiB of artifact content. Its
	// protobuf envelope must not make that exact boundary fail at the client.
	if err := os.Truncate(artifactPath, archiveArtifactUnaryMaxSize); err != nil {
		t.Fatal(err)
	}
	content, err := client.GetArchiveArtifact(ctx, request)
	if err != nil {
		t.Fatalf("exact-limit unary artifact rejected by transport: %v", err)
	}
	if got := int64(len(content)); got != archiveArtifactUnaryMaxSize {
		t.Fatalf("unary artifact bytes = %d, want %d", got, archiveArtifactUnaryMaxSize)
	}
}
