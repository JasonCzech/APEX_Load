package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/localfs"
	"github.com/jasonczech/apexload/pkg/internal/procidentity"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Durable physical-writer ownership across controller restart and HA
// takeover (storage/analysis_writer_ownership.go has the contract).
//
// This controller records its own process as the incarnation of every
// attempt it claims and every upload writer it begins, before any artifact
// I/O. The process-local reconciliation records (analysis_reconciliation.go,
// analysis_upload_pipeline.go) still retire this process's own generations.
// Every replica additionally runs a bounded sweep that retires generations
// whose recorded controller its host proves exited; it never judges its own
// process, and a host that cannot judge (another machine, boot or PID
// namespace, an unidentified platform) leaves them owned.

const (
	analysisWriterInventoryPage  = 100
	analysisWriterInventoryPages = 4
	// The writer sweep rides the 5-second native reconciliation tick.
	analysisWriterSweepEvery = 12
)

// analysisWriterWarnEvery rate-limits the sweep's warnings.
const analysisWriterWarnEvery = 10 * time.Minute

// analysisArtifactDirectoryMissingWarning is logged when an upload retry finds
// its offset owned by a writer that cannot be judged because the filesystem
// artifact directory does not exist yet.
const analysisArtifactDirectoryMissingWarning = "analysis artifact directory is missing; exited upload writers stay owned until it exists"

type analysisWriterOwnershipRuntime struct {
	self  procidentity.Identity
	host  procidentity.Host
	known bool
	// exited judges a recorded process from this host; localFS reports
	// whether a directory is on a local filesystem (localfs.IsLocal).
	exited  func(procidentity.Identity) bool
	localFS func(string) (bool, error)
}

type analysisWriterInventoryCursor struct {
	mu          sync.Mutex
	after       string
	lastWarn    time.Time
	lastWarning string
	// lastMissingWarn rate-limits the missing artifact directory warning on
	// its own, so sweep warnings never hide the operator's workaround.
	lastMissingWarn time.Time
}

var (
	analysisWriterProcessOnce    sync.Once
	analysisWriterProcessRuntime analysisWriterOwnershipRuntime
)

// analysisWriterRuntime is this controller's process identity and exit judge.
func (s *Server) analysisWriterRuntime() analysisWriterOwnershipRuntime {
	if s != nil {
		if runtime := s.analysisWriterOwnership.Load(); runtime != nil {
			return *runtime
		}
	}
	analysisWriterProcessOnce.Do(func() {
		self, err := procidentity.Self()
		host, hostErr := procidentity.CurrentHost()
		analysisWriterProcessRuntime = analysisWriterOwnershipRuntime{self: self, host: host, known: err == nil && hostErr == nil, exited: procidentity.Exited, localFS: localfs.IsLocal}
	})
	return analysisWriterProcessRuntime
}

// bindAnalysisWriterIncarnation records this process as the artifact writer
// of a generation minted under ctx. Without an identity nothing is bound and
// the generation can be retired only by its own process.
func (s *Server) bindAnalysisWriterIncarnation(ctx context.Context) context.Context {
	runtime := s.analysisWriterRuntime()
	if !runtime.known {
		return ctx
	}
	return storage.WithAnalysisWriterIncarnation(ctx, runtime.self)
}

var errAnalysisWriterNoIdentity = errors.New("this controller has no process identity to judge from")
var errAnalysisWriterNoLocality = errors.New("this controller has no filesystem locality probe")

// analysisWriterExitScope is this replica's proof authority. It requires the
// current artifact backend to be the durably pinned analytical storage, so a
// filesystem judgement always describes the backend the writers used. A
// filesystem backend counts as local only when its root is on a local
// filesystem: a network or FUSE mount (NFS, SMB, EFS, Azure Files, blobfuse)
// can complete a write after its process is reaped, like an object store.
func (s *Server) analysisWriterExitScope() (storage.AnalysisWriterExitScope, error) {
	runtime := s.analysisWriterRuntime()
	if s == nil || !runtime.known || runtime.exited == nil || s.nativeReconciliationStore == nil {
		return storage.AnalysisWriterExitScope{}, errAnalysisWriterNoIdentity
	}
	backend := currentArchiveBackend()
	if err := backend.validate(); err != nil {
		return storage.AnalysisWriterExitScope{}, err
	}
	if err := ensureAnalysisBackendFor(s.nativeReconciliationStore, backend); err != nil {
		return storage.AnalysisWriterExitScope{}, err
	}
	// Judge the exact directory every artifact write and cleanup opens, through
	// its own symbolic links and mount points. Only a definite locality answer
	// counts: a probe that fails grants no scope this sweep, rather than
	// recording a permanent remote verdict on a local filesystem. The judge
	// never creates the directory: a missing _analysis (no artifact written
	// yet, or an unmounted share) fails the probe, so no writer is judged until
	// a later sweep finds it.
	local := false
	if backend.Backend == archiveBackendFilesystem {
		if runtime.localFS == nil {
			return storage.AnalysisWriterExitScope{}, errAnalysisWriterNoLocality
		}
		probed, err := runtime.localFS(analysisArtifactDirectory(backend))
		if err != nil {
			return storage.AnalysisWriterExitScope{}, fmt.Errorf("artifact directory locality is undecided: %w", err)
		}
		local = probed
	}
	return storage.AnalysisWriterExitScope{Prover: runtime.self, Exited: runtime.exited, LocalFilesystem: local}, nil
}

// analysisArtifactDirectory is the filesystem directory that
// analysisObjectWrite, analysisObjectRead and cleanup open with os.OpenRoot.
func analysisArtifactDirectory(backend archiveBackendRuntime) string {
	return filepath.Join(backend.LocalRoot, "_analysis")
}

// analysisWriterHere reports whether this host can judge a recorded writer,
// with the exit proof's own scoping (procidentity.Judgeable).
func (s *Server) analysisWriterHere() func(procidentity.Identity) bool {
	runtime := s.analysisWriterRuntime()
	if !runtime.known {
		return func(procidentity.Identity) bool { return false }
	}
	return func(id procidentity.Identity) bool { return procidentity.Judgeable(runtime.host, id) }
}

// warnAnalysisWriterSweep logs at most every analysisWriterWarnEvery; the
// caller holds the cursor lock.
func (s *Server) warnAnalysisWriterSweep(message string, attrs ...any) {
	now := time.Now()
	if now.Sub(s.analysisWriterCursor.lastWarn) < analysisWriterWarnEvery {
		return
	}
	s.analysisWriterCursor.lastWarn = now
	s.analysisWriterCursor.lastWarning = message
	slog.Warn(message, attrs...)
}

// reconcileAnalysisWriterIncarnationsOnce retires a bounded page of
// generations whose controller this host proves exited.
func (s *Server) reconcileAnalysisWriterIncarnationsOnce(ctx context.Context) {
	if s == nil || !analysisQueryContextLive(ctx) {
		return
	}
	writers, ok := s.nativeReconciliationStore.(storage.AnalysisWriterOwnershipStore)
	if !ok {
		return
	}
	// The scope is computed only once candidates exist: checking it binds the
	// analytical storage identity, which an unused controller must not do. A
	// controller without a process identity (an unsupported platform, a
	// Windows container or host without MachineGuid, a foreign /proc) lists
	// candidates too, so its refusal to judge them is logged.
	var scope storage.AnalysisWriterExitScope
	scoped := false
	s.analysisWriterCursor.mu.Lock()
	defer s.analysisWriterCursor.mu.Unlock()
	// A pass resumes where the last one stopped and wraps to the start of the
	// inventory at most once, so a pass with page budget left covers a small
	// inventory completely wherever the shared cursor stood.
	start := s.analysisWriterCursor.after
	wrapped := start == ""
	for range analysisWriterInventoryPages {
		list, stop := context.WithTimeout(ctx, 5*time.Second)
		page, next, err := writers.ListAnalysisWriterOwnershipCandidates(list, s.analysisWriterCursor.after, analysisWriterInventoryPage)
		stop()
		if err != nil {
			s.warnAnalysisWriterSweep("analysis writer inventory unavailable", "error", err)
			return
		}
		s.analysisWriterCursor.after = next
		if len(page) > 0 && !scoped {
			var scopeErr error
			if scope, scopeErr = s.analysisWriterExitScope(); scopeErr != nil {
				s.warnAnalysisWriterSweep("analysis writer exits cannot be judged on this replica", "error", scopeErr, "candidates", len(page))
				return
			}
			scoped = true
		}
		for _, candidate := range page {
			if !analysisQueryContextLive(ctx) {
				return
			}
			call, stop := context.WithTimeout(ctx, 5*time.Second)
			var outcome storage.AnalysisWriterExitOutcome
			switch candidate.Kind {
			case "attempt":
				outcome, err = writers.ResolveExitedAnalysisAttempt(call, candidate.ProjectID, candidate.ID, scope)
			case "upload-writer":
				outcome, err = writers.ResolveExitedAnalysisUploadWriter(call, candidate.ProjectID, candidate.ID, scope)
			default:
				err = storage.ErrAnalysisAuthority
			}
			stop()
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				s.warnAnalysisWriterSweep("analysis writer exit resolution failed", "kind", candidate.Kind, "generation", candidate.ID, "project_id", candidate.ProjectID, "error", err)
			}
			if err == nil && outcome.Recorded {
				slog.Info("analysis writer exit proven", "kind", candidate.Kind, "generation", candidate.ID, "project_id", candidate.ProjectID, "import_failed", outcome.ImportFailed, "remote_commit_unproven", outcome.RemoteCommitUnproven)
			}
		}
		if next == "" {
			if wrapped {
				return
			}
			wrapped = true
		}
		if wrapped && start != "" && next != "" && next >= start {
			// Back at the page this pass began on.
			return
		}
	}
}

// resolveExitedAnalysisUploadOffset retires exited writers blocking one
// upload offset so the client's retry can begin. It reports whether any was.
func (s *Server) resolveExitedAnalysisUploadOffset(ctx context.Context, req storage.AnalysisUploadRequest) bool {
	writers, ok := s.nativeReconciliationStore.(storage.AnalysisWriterOwnershipStore)
	if !ok {
		return false
	}
	scope, err := s.analysisWriterExitScope()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// A fresh filesystem backend's first writer died before any write
			// created the artifact directory, which no judge creates.
			s.analysisWriterCursor.mu.Lock()
			if now := time.Now(); now.Sub(s.analysisWriterCursor.lastMissingWarn) >= analysisWriterWarnEvery {
				s.analysisWriterCursor.lastMissingWarn = now
				s.analysisWriterCursor.lastWarning = analysisArtifactDirectoryMissingWarning
				slog.Warn(analysisArtifactDirectoryMissingWarning, "directory", analysisArtifactDirectory(currentArchiveBackend()), "import_id", req.ImportID, "project_id", req.ProjectID, "workaround", "create the directory on the mounted archive volume, mode 0700, owned by the controller's user")
			}
			s.analysisWriterCursor.mu.Unlock()
		}
		return false
	}
	call, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	var resolved int
	resolved, err = writers.ResolveExitedAnalysisUploadCursor(call, req, scope)
	if err != nil && !errors.Is(err, storage.ErrAnalysisAuthority) {
		slog.Warn("analysis upload writer exit unresolved", "import_id", req.ImportID, "project_id", req.ProjectID, "error", err)
	}
	if resolved > 0 {
		slog.Info("analysis upload writer exit proven", "import_id", req.ImportID, "project_id", req.ProjectID, "writers", resolved, "remote_commit_unproven", !scope.LocalFilesystem)
	}
	return err == nil && resolved > 0
}

// writeAnalysisDeletionPending answers a revoked dataset whose namespace is
// still owned by an unproven physical writer. The import stays cancelled or
// deleting; repeating DELETE completes it once every writer is proven
// stopped. retryable means the deletion may still complete without an
// intervention; Retry-After is set only when this host itself will settle a
// writer (it closes, or this host proves its exit at its next sweep). Legacy
// writers and unproven remote commits alone need an explicit intervention.
func writeAnalysisDeletionPending(w http.ResponseWriter, in *storage.AnalysisImport, status storage.AnalysisDatasetCleanupStatus) {
	if status.Live {
		writeError(w, http.StatusConflict, "dataset changed during deletion; reload it")
		return
	}
	ref := errorReferenceFor(w)
	slog.Warn("analysis dataset deletion pending", "ref", ref, "code", "analysis_dataset_deletion_pending", "import_id", in.ID, "project_id", in.ProjectID, "legacy_writers", status.LegacyWriters, "unrecorded_writers", status.UnrecordedWriters, "remote_commits_unproven", status.RemoteCommitsUnproven, "unproven_writers", status.UnprovenWriters, "unjudgeable_writers", status.UnjudgeableWriters)
	if status.ResolvesHere() {
		w.Header().Set("Retry-After", "60")
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"error":     "dataset deletion is pending until every physical writer is proven stopped",
		"code":      "analysis_dataset_deletion_pending",
		"state":     in.State,
		"retryable": status.Resolvable(),
		"pending": map[string]int{
			"legacy_writers":          status.LegacyWriters,
			"unrecorded_writers":      status.UnrecordedWriters,
			"remote_commits_unproven": status.RemoteCommitsUnproven,
			"unproven_writers":        status.UnprovenWriters,
			"unjudgeable_writers":     status.UnjudgeableWriters,
		},
		"ref": ref,
	})
}
