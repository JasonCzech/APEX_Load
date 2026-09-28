package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	grpcCodes "google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"
)

type deadlineArchiveClient struct {
	startupLifecycleFakeClient
	started chan<- time.Time
}

func (c *deadlineArchiveClient) StreamArchiveArtifact(ctx context.Context, _ generatorrpc.ArchiveArtifactRequest, _ io.Writer, _ int64) (int64, error) {
	deadline, _ := ctx.Deadline()
	c.started <- deadline
	select {
	case <-ctx.Done():
		// A canceled RPC in best-effort mode is normally treated as a missing source.
		// The whole attempt must still fail when its own context was canceled.
		return 0, grpcStatus.Error(grpcCodes.Canceled, "transfer canceled")
	case <-time.After(3 * time.Second):
		return 0, errors.New("transfer did not inherit attempt cancellation")
	}
}

func TestStageRemoteArchiveSharesDeadlineAndReleasesSourceFence(t *testing.T) {
	for _, requireAll := range []bool{false, true} {
		t.Run(fmt.Sprint(requireAll), func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
			t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "deadline-test")
			t.Setenv("APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL", "1m")
			t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())
			t.Setenv("TMPDIR", t.TempDir())
			started := make(chan time.Time, remoteArchiveMaxTargets)
			clients := map[string]generatorClient{}
			var targets []string
			for i := 0; i < remoteArchiveFetchConcurrency+4; i++ {
				target := fmt.Sprintf("gen-%d", i)
				targets = append(targets, target)
				clients[target] = &deadlineArchiveClient{started: started}
			}
			test := &ManagedTest{ID: "bounded-collection", remoteClientsByTarget: clients}
			b := newDistributedExecutionBackend(targets, time.Second, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			deadline, _ := ctx.Deadline()
			finished := make(chan error, 1)
			go func() {
				record, err := b.stageRemoteArchiveWithTargetHistoryContext(ctx, nil, test, "FAILED", "", time.Now(), requireAll, nil)
				if record != nil {
					err = fmt.Errorf("canceled attempt produced a publication source")
				}
				finished <- err
			}()
			for i := 0; i < remoteArchiveFetchConcurrency; i++ {
				select {
				case got := <-started:
					if got.IsZero() || got.After(deadline) {
						t.Errorf("RPC deadline %s exceeds attempt deadline %s", got, deadline)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("archive workers did not start")
				}
			}
			// Collection owns the source fence until its workers have stopped.
			blocked, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
			unlock, err := lockArchivePublicationSource(blocked, test.ID)
			stop()
			if err == nil {
				unlock()
				t.Error("collection did not retain its source fence")
			}
			cancel()
			select {
			case err := <-finished:
				if err == nil {
					t.Fatal("canceled collection was treated as a successful partial archive")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("collection ignored cancellation")
			}
			free, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			unlock, err = lockArchivePublicationSource(free, test.ID)
			if err != nil {
				t.Fatalf("source fence retained after cancellation: %v", err)
			}
			unlock()
			if _, err := os.Stat(filepath.Join(resolveArchiveRoot(), "remote", test.ID)); !os.IsNotExist(err) {
				t.Fatalf("canceled attempt replaced the source: %v", err)
			}
			entries, err := os.ReadDir(os.TempDir())
			if err != nil || len(entries) != 0 {
				t.Fatalf("transfer staging not cleaned: %v %v", entries, err)
			}
		})
	}
}

func TestStageRemoteArchiveCanceledBeforeIO(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-exist")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := newDistributedExecutionBackend(nil, time.Second, nil)
	if _, err := b.stageRemoteArchiveWithTargetHistoryContext(ctx, nil, &ManagedTest{ID: "canceled"}, "FAILED", "", time.Now(), false, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("canceled attempt performed filesystem IO: %v", err)
	}
}
