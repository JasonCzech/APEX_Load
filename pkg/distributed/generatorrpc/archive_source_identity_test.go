package generatorrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestArchiveSourceIdentityIsRunBoundAndNotACredential(t *testing.T) {
	token := supervisionTestToken(17)
	identity := ArchiveSourceIdentity("run", token, "source")
	if !validArchiveSourceIdentity(identity) || identity != ArchiveSourceIdentity("run", token, "source") || identity == ArchiveSourceIdentity("run", token, "other-target") || identity == ArchiveSourceIdentity("other", token, "source") || identity == ArchiveSourceIdentity("run", supervisionTestToken(18), "source") {
		t.Fatal("source identity is unstable or not bound to the run and token")
	}
	for _, bad := range [][]byte{nil, []byte{1}, make([]byte, RunSupervisionTokenSize)} {
		if ArchiveSourceIdentity("run", bad, "source") != "" {
			t.Fatal("invalid credential produced an identity")
		}
	}
	if ArchiveSourceIdentity("../run", token, "source") != "" {
		t.Fatal("invalid run id accepted")
	}
	for _, bad := range []string{"", identity[:63], strings.ToUpper(identity), strings.Repeat("z", 64)} {
		if validArchiveSourceIdentity(bad) {
			t.Fatal("invalid digest accepted")
		}
	}
}

func TestArchiveSourceIdentitySurvivesTokenRotation(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	writer, err := newArchiveRecorder("identity-generator", "run", "source")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Discard()
	original := ArchiveSourceIdentity("run", supervisionTestToken(1), "source")
	writer.bindExecution(1, "buyers", original)
	writer.bindExecution(2, "sellers", ArchiveSourceIdentity("run", supervisionTestToken(2), "source"))
	writer.mu.Lock()
	got := writer.manifest.SourceIdentity
	writer.mu.Unlock()
	if got != original {
		t.Fatal("adoption replaced source identity")
	}
}

func TestArchiveSourceIdentityGuardsUnaryAndStreamingReads(t *testing.T) {
	for _, mode := range []string{"matching", "wrong identity", "wrong target", "foreign test", "legacy manifest", "malformed request", "legacy request", "empty artifact", "invalid manifest", "oversized manifest", "manifest symlink"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			service := NewService("identity-reader", 10)
			dir := filepath.Join(service.generatorArchiveRoot(), "run")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			identity := ArchiveSourceIdentity("run", supervisionTestToken(1), "source")
			manifest := archiveManifest{SchemaVersion: archiveSchemaVersion, TestID: "run", SourceIdentity: identity, CompletedAt: time.Now().UTC(), Status: "COMPLETE"}
			request := ArchiveArtifactRequest{TestID: "run", Name: "transactions.jsonl", SourceIdentity: identity}
			want := codes.OK
			switch mode {
			case "wrong identity":
				request.SourceIdentity = ArchiveSourceIdentity("run", supervisionTestToken(2), "source")
				want = codes.FailedPrecondition
			case "wrong target":
				request.SourceIdentity = ArchiveSourceIdentity("run", supervisionTestToken(1), "other-target")
				want = codes.FailedPrecondition
			case "foreign test":
				manifest.TestID = "other"
				want = codes.FailedPrecondition
			case "legacy manifest":
				manifest.SourceIdentity = ""
				want = codes.FailedPrecondition
			case "malformed request":
				request.SourceIdentity = "short"
				want = codes.InvalidArgument
			case "legacy request":
				request.SourceIdentity = ""
				manifest.SourceIdentity = ""
			}
			data, _ := json.Marshal(manifest)
			switch mode {
			case "invalid manifest":
				data = []byte("{invalid")
				want = codes.FailedPrecondition
			case "oversized manifest":
				data = bytes.Repeat([]byte(" "), (8<<20)+1)
				want = codes.FailedPrecondition
			case "manifest symlink":
				want = codes.FailedPrecondition
			}
			if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			payload := []byte("one incarnation\n")
			if mode == "empty artifact" {
				payload = nil
			}
			if err := os.WriteFile(filepath.Join(dir, "transactions.jsonl"), payload, 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "manifest symlink" {
				path := filepath.Join(dir, "manifest.json")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("transactions.jsonl", path); err != nil {
					t.Fatal(err)
				}
			}
			service.storeArchiveRecord(&storage.RunArchiveRecord{TestID: "run", ArchivePath: dir, ManifestPath: filepath.Join(dir, "manifest.json")})
			client := supervisionHeartbeatClient(t, service)
			got, err := client.GetArchiveArtifact(context.Background(), request)
			if status.Code(err) != want || want == codes.OK && !bytes.Equal(got, payload) {
				t.Fatalf("unary=%v bytes=%d", err, len(got))
			}
			var streamed bytes.Buffer
			n, err := client.StreamArchiveArtifact(context.Background(), request, &streamed, 1024)
			if status.Code(err) != want || want == codes.OK && !bytes.Equal(streamed.Bytes(), payload) || want != codes.OK && (n != 0 || streamed.Len() != 0) {
				t.Fatalf("stream=%v bytes=%d", err, n)
			}
			service.archiveManager.mu.RLock()
			leases := len(service.archiveManager.leases)
			transfers := service.archiveManager.activeTransfers
			service.archiveManager.mu.RUnlock()
			if leases != 0 || transfers != 0 {
				t.Fatal("identity failure leaked archive admission")
			}
		})
	}
}

func TestArchiveSourceIdentityRecordedByExecutionAcrossAdoption(t *testing.T) {
	service, token := startAdoptableRunWithLease(t, 45*time.Second, 5*time.Second, 0, "source")
	identity := ArchiveSourceIdentity("run", token, "source")
	next := supervisionTestToken(22)
	ack := claimWithAdoptions(t, service, 2, &pb.RunAdoption{TestId: "run", PriorGeneration: 1, PriorRunSupervisionToken: token, NewRunSupervisionToken: next})
	if len(ack.GetAdoptions()) != 1 || ack.GetAdoptions()[0].GetOutcome() != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED {
		t.Fatal("adoption failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(service.generatorArchiveRoot(), "run", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest archiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SourceIdentity != identity || manifest.SourceIdentity == ArchiveSourceIdentity("run", next, "source") {
		t.Fatal("finalization lost original assignment identity")
	}
}

type archiveIdentityRPC struct {
	pb.GeneratorControlClient
	proof string
}

func (c archiveIdentityRPC) GetArchiveArtifact(context.Context, *pb.ArchiveArtifactRequest, ...grpc.CallOption) (*pb.ArchiveArtifact, error) {
	return &pb.ArchiveArtifact{Name: "transactions.jsonl", Content: []byte("bytes"), SourceIdentity: c.proof}, nil
}
func (c archiveIdentityRPC) StreamArchiveArtifact(context.Context, *pb.ArchiveArtifactRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[pb.ArchiveArtifactChunk], error) {
	return &archiveIdentityStream{chunk: &pb.ArchiveArtifactChunk{Name: "transactions.jsonl", Content: []byte("bytes"), TotalSize: 5, Eof: true, SourceIdentity: c.proof}}, nil
}

type archiveIdentityStream struct {
	grpc.ClientStream
	chunk *pb.ArchiveArtifactChunk
}

func (s *archiveIdentityStream) Recv() (*pb.ArchiveArtifactChunk, error) {
	if s.chunk == nil {
		return nil, io.EOF
	}
	chunk := s.chunk
	s.chunk = nil
	return chunk, nil
}
func TestArchiveSourceIdentityRejectsUnconfirmedTransport(t *testing.T) {
	identity := ArchiveSourceIdentity("run", supervisionTestToken(1), "source")
	for _, proof := range []string{"", ArchiveSourceIdentity("run", supervisionTestToken(2), "source")} {
		c := &Client{rpc: archiveIdentityRPC{proof: proof}}
		req := ArchiveArtifactRequest{TestID: "run", Name: "transactions.jsonl", SourceIdentity: identity}
		if bytes, err := c.GetArchiveArtifact(context.Background(), req); err == nil || len(bytes) != 0 {
			t.Fatal("unconfirmed unary payload escaped")
		}
		var buf bytes.Buffer
		if n, err := c.StreamArchiveArtifact(context.Background(), req, &buf, 1024); err == nil || n != 0 || buf.Len() != 0 {
			t.Fatal("unconfirmed stream payload escaped")
		}
	}
}

func TestArchiveSourceTargetAdmissionAndFingerprint(t *testing.T) {
	service := newIsolatedService(t, 10)
	defer shutdownSupervisionTestService(t, service)
	req := supervisedStageRequest("target-bound", "script", 0, supervisionTestToken(9))
	req.ArchiveSourceTarget = "source"
	first := workAssignmentFingerprint(req)
	ack, err := service.AssignWork(context.Background(), req)
	if err != nil || !ack.GetAccepted() {
		t.Fatalf("stage: %+v %v", ack, err)
	}
	req.ArchiveSourceTarget = "other"
	if first == workAssignmentFingerprint(req) {
		t.Fatal("target change omitted from assignment fingerprint")
	}
	for _, group := range []string{"group", "another-group"} {
		req.GroupName = group
		ack, err = service.AssignWork(context.Background(), req)
		if err == nil && ack.GetAccepted() {
			t.Fatal("generator accepted mixed target namespaces")
		}
	}
	for _, target := range []string{" source", "source\n", strings.Repeat("a", 1025)} {
		req.ArchiveSourceTarget = target
		if _, err := service.AssignWork(context.Background(), req); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid target accepted: %v", err)
		}
	}
	req.ArchiveSourceTarget = "source"
	req.RunSupervisionToken = nil
	if _, err := service.AssignWork(context.Background(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("target bound without token: %v", err)
	}
}
