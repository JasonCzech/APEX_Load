package recordbridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type publicationFunc func(context.Context, *BrowserReplaySession, io.ReadSeeker, int64) error

func (f publicationFunc) Publish(ctx context.Context, s *BrowserReplaySession, r io.ReadSeeker, n int64) error {
	return f(ctx, s, r, n)
}

type publicationLease struct {
	*portLease
	publisher TerminalReplayArtifactPublisher
}

func (l *publicationLease) ReplayArtifactPublisher() TerminalReplayArtifactPublisher {
	return l.publisher
}

type publicationProtocol struct {
	portReplayProtocol
	path string
}

func (p *publicationProtocol) TakeBrowserHARArtifacts() []protocol.BrowserHARArtifact {
	path := p.path
	p.path = ""
	if path == "" {
		return nil
	}
	return []protocol.BrowserHARArtifact{{Path: path}}
}

func TestReplayArtifactPublicationTerminalOrdering(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			lease := &publicationLease{portLease: &portLease{done: make(chan struct{})}}
			lease.publisher = publicationFunc(func(ctx context.Context, s *BrowserReplaySession, r io.ReadSeeker, n int64) error {
				if s.Status != BrowserReplayCompleted || s.FinalReport == nil || !s.FinalReport.Success || s.WorkerSettled() {
					t.Error("publisher received unfinished or settled snapshot")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Error("publication is unbounded")
				}
				data, err := io.ReadAll(r)
				if err != nil || int64(len(data)) != n || !strings.Contains(string(data), "native-page") {
					t.Errorf("native HAR = %q %v", data, err)
				}
				s.FinalReport.Success = false // Detached input cannot mutate the live result.
				close(entered)
				<-release
				if fail {
					return errors.New("storage unavailable")
				}
				return nil
			})
			callback := make(chan struct{}, 1)
			manager := NewBrowserReplayManager(nil, ReplayManagerOptions{ReserveID: func(context.Context, string, string) (string, func(), SessionLease, error) {
				return "publication", func() {}, lease, nil
			}, OnComplete: func(context.Context, string, *dsl.Script, *engine.ReplayReport) { callback <- struct{}{} }})
			t.Cleanup(manager.Close)
			var releaseOnce sync.Once
			releasePublisher := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releasePublisher)
			script := portReplayScript(t)
			name := fmt.Sprintf("publication-%d", portReplaySerial.Add(1))
			script.Protocol = name
			path := publishFakeReplayHAR(t, t.TempDir(), "publication", "native-page")
			protocol.Register(name, func() protocol.Protocol { return &publicationProtocol{path: path} })
			started, err := manager.Start("script", script, BrowserReplayStartRequest{})
			if err != nil {
				t.Fatal(err)
			}
			waitPortWorker(t, entered)
			request := httptest.NewRequest(http.MethodGet, "/har", nil)
			request.SetPathValue("session_id", started.ID)
			response := httptest.NewRecorder()
			(&Service{Replay: manager}).HandleDownloadBrowserReplayHAR(response, request)
			if response.Code != http.StatusConflict || response.Header().Get("Retry-After") == "" {
				t.Fatalf("pending download status = %d", response.Code)
			}
			pending, ok := manager.Get(started.ID)
			if !ok || pending.WorkerSettled() || pending.ArtifactDurability() != "pending" {
				t.Fatalf("pending = %#v", pending)
			}
			select {
			case <-callback:
				t.Fatal("completion preceded publication")
			default:
			}
			manager.mu.RLock()
			live := manager.sessions[started.ID]
			manager.mu.RUnlock()
			releasePublisher()
			waitPortWorker(t, live.workerDone)
			final, ok := manager.Get(started.ID)
			expected := "durable"
			if fail {
				expected = "unavailable"
			}
			if !ok || !final.WorkerSettled() || final.ArtifactDurability() != expected || final.Status != BrowserReplayCompleted || !final.FinalReport.Success {
				t.Fatalf("final = %#v", final)
			}
			select {
			case <-callback:
			default:
				t.Fatal("completion missing")
			}
		})
	}
}

func TestReplayArtifactPublicationFencesAndNativeRequirement(t *testing.T) {
	for _, scenario := range []string{"local", "local-epoch-loss", "nil-port", "missing-native", "failure", "epoch-loss", "lease-loss", "abandoned", "replacement"} {
		t.Run(scenario, func(t *testing.T) {
			manager := NewBrowserReplayManager(nil)
			t.Cleanup(manager.Close)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lease := &publicationLease{portLease: &portLease{done: make(chan struct{})}}
			session := &BrowserReplaySession{ID: "id", ProjectID: "project", Status: BrowserReplayCompleted, terminalFenceEstablished: true, workerDone: make(chan struct{}), FinalReport: &engine.ReplayReport{Success: true}}
			manager.sessions[session.ID] = session
			called := false
			lease.publisher = publicationFunc(func(context.Context, *BrowserReplaySession, io.ReadSeeker, int64) error {
				called = true
				switch scenario {
				case "failure":
					return errors.New("failed")
				case "epoch-loss":
					cancel()
					manager.reconcileWorkerCancellation(ctx, ctx, session)
					session.mu.RLock()
					exposed := session.FinalReport != nil
					session.mu.RUnlock()
					if exposed {
						t.Error("pending publication retained payload after epoch cancellation")
					}
				case "lease-loss":
					lease.Fail(errors.New("lost"))
				case "abandoned":
					session.mu.Lock()
					session.workerAbandoned = true
					session.mu.Unlock()
				case "replacement":
					manager.mu.Lock()
					manager.sessions[session.ID] = &BrowserReplaySession{ID: session.ID}
					manager.mu.Unlock()
				}
				return nil
			})
			session.reservation = wrapSessionLease(lease)
			if scenario == "local" || scenario == "local-epoch-loss" {
				session.reservation = wrapSessionLease(lease.portLease)
			}
			if scenario == "nil-port" {
				lease.publisher = nil
			}
			if scenario != "missing-native" {
				var err error
				session.harCopy, err = manager.har.write(session, strings.NewReader("native"), 6)
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "local-epoch-loss" {
				cancel()
			}
			settled := manager.publishTerminalReplayArtifacts(ctx, session)
			switch scenario {
			case "local", "local-epoch-loss", "nil-port":
				if called || !settled || session.ArtifactDurability() != "local_only" {
					t.Fatal("local adapter invoked publisher")
				}
			case "missing-native":
				if called || !settled || session.ArtifactDurability() != "unavailable" {
					t.Fatal("missing native was published")
				}
			case "failure":
				if !called || !settled || session.ArtifactDurability() != "unavailable" || !session.FinalReport.Success {
					t.Fatal("publication failure changed execution")
				}
			default:
				if !called || settled {
					t.Fatal("late publisher settled invalid session")
				}
			}
			if scenario == "epoch-loss" || scenario == "lease-loss" {
				if session.FinalReport != nil || session.terminalFenceEstablished {
					t.Fatal("lost fence retained payload")
				}
			}
			// These are fixture records, not running workers; close must not wait for them.
			session.mu.Lock()
			session.workerFinished = true
			close(session.workerDone)
			session.mu.Unlock()
		})
	}
}

func TestReplayArtifactPublisherCapabilityOptional(t *testing.T) {
	var lease *sessionLease
	if lease.artifactPublisher() != nil {
		t.Fatal("nil lease has publisher")
	}
	if (*BrowserReplaySession)(nil).ArtifactDurability() != "" {
		t.Fatal("nil snapshot has durability")
	}
}

func TestReplayArtifactPublicationPreservesUnstartedTerminalFence(t *testing.T) {
	manager := NewBrowserReplayManager(nil)
	t.Cleanup(manager.Close)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A stopped-before-start worker has no publication phase. Its established
	// terminal fence must retain the pre-existing cancellation behavior.
	session := &BrowserReplaySession{ID: "unstarted", Status: BrowserReplayStopped, terminalFenceEstablished: true, workerDone: make(chan struct{})}
	manager.sessions[session.ID] = session
	manager.reconcileWorkerCancellation(ctx, ctx, session)
	if session.Status != BrowserReplayStopped {
		t.Fatal("terminal stop was invalidated")
	}
	session.workerFinished = true
	close(session.workerDone)
}
