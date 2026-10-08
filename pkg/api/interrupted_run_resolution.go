package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// interruptedDistributedStopReason is recorded by acquisition reconcile on a
// distributed run it cannot adopt: the successor has neither the old leader's
// generator handles nor proof that the run's load stopped, so the run is held
// STOPPING (with its capacity) instead of being reported terminal.
const interruptedDistributedStopReason = "controller execution ownership was interrupted; distributed generator stop is unproven without a complete durable participant journal; external generator process-fence reconciliation is required"

const (
	// interruptedRunResolutionSettle delays the first proof attempt past any
	// graceful drain the lost leader started just before it went away. A drain
	// is not an assignment, so an accepted epoch claim does not cover it; the
	// generator bounds it (drain plus hard grace, about 6 seconds by default).
	interruptedRunResolutionSettle   = 30 * time.Second
	interruptedRunResolutionMaxDelay = 2 * time.Minute

	interruptedRunResolutionAttestation = "all-generator-processes-stopped"
	maxInterruptedRunResolutionNote     = 512
)

// runInterruptedRunResolution resolves the runs acquisition reconcile left
// STOPPING with interruptedDistributedStopReason. It runs after admission for
// as long as this term leads, retrying with backoff while proof is missing.
func (s *Server) runInterruptedRunResolution(generation uint64) {
	if s == nil || s.leadership == nil || s.runner == nil {
		return
	}
	delay := interruptedRunResolutionSettle
	for {
		ctx, cancel := s.leadership.operationContext(context.Background())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			cancel()
			return
		case <-timer.C:
		}
		owner, current := s.leadership.executionLeaseTerm()
		if owner == "" || current != generation || !s.isExecutionLeader() {
			cancel()
			return
		}
		pending := s.runner.resolveInterruptedDistributedRuns(ctx, generation)
		cancel()
		if pending == 0 {
			return
		}
		delay = min(2*delay, interruptedRunResolutionMaxDelay)
	}
}

// interruptedDistributedRunsLocked lists the runs held STOPPING by acquisition
// reconcile that this replica has no runtime for.
func (tr *TestRunner) interruptedDistributedRunsLocked() []*ManagedTest {
	runs := make([]*ManagedTest, 0)
	for _, test := range tr.tests {
		if interruptedDistributedRunLocked(test) {
			runs = append(runs, test)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
	return runs
}

func interruptedDistributedRunLocked(test *ManagedTest) bool {
	return test != nil && test.Status == "STOPPING" && test.cancel == nil && test.executionCtx == nil &&
		strings.Contains(test.Error, interruptedDistributedStopReason)
}

// resolveInterruptedDistributedRuns finalizes every interrupted run whose
// generators all prove the run's load is gone, and returns how many remain.
func (tr *TestRunner) resolveInterruptedDistributedRuns(ctx context.Context, generation uint64) int {
	backend, ok := tr.execution.(*distributedExecutionBackend)
	if !ok {
		return 0
	}
	tr.mu.RLock()
	runs := tr.interruptedDistributedRunsLocked()
	tr.mu.RUnlock()
	pending := 0
	for _, test := range runs {
		if ctx.Err() != nil {
			return len(runs)
		}
		resolved, err := tr.resolveInterruptedDistributedRun(ctx, backend, test, generation)
		if err != nil {
			slog.Info("interrupted distributed run is still unresolved", "test_id", test.ID, "error", err)
		}
		if !resolved {
			pending++
		}
	}
	return pending
}

// resolveInterruptedDistributedRun proves, per generator, that the run's load
// from the lost term is gone and then finalizes the run FAILED.
//
// The run's archive-collection intent is its participant journal: every target
// is recorded durably before work is dispatched to it, including evacuation
// replacements. An epoch claim of this (newer) term that a target accepts
// proves that target holds no assignment of an older term and no unfinished
// fence. A target that cannot be reached, or a run with no journal, stays
// STOPPING; the operator override covers generators that are gone for good.
func (tr *TestRunner) resolveInterruptedDistributedRun(ctx context.Context, backend *distributedExecutionBackend, test *ManagedTest, generation uint64) (bool, error) {
	intent, err := tr.runArchiveCollectionIntent(test)
	if err != nil {
		return false, err
	}
	if intent == nil || len(intent.Sources) == 0 {
		return false, errors.New("no durable record of the generators the run reached")
	}
	current, owner, deadline, err := tr.generatorExecutionClaim()
	if err != nil || current != generation {
		return false, errNotExecutionLeader
	}
	targets := make([]string, 0, len(intent.Sources))
	for target := range intent.Sources {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	var unproven []string
	for _, target := range targets {
		dialCtx, cancel := context.WithTimeout(ctx, backend.timeout)
		client, dialErr := backend.dial(dialCtx, target)
		cancel()
		if dialErr != nil {
			unproven = append(unproven, target)
			continue
		}
		claimErr := claimGeneratorExecutionEpoch(ctx, client, generation, owner, deadline, backend.generatorEpochKeeperTransit())
		_ = client.Close()
		if claimErr != nil {
			unproven = append(unproven, target)
		}
	}
	if len(unproven) > 0 {
		return false, fmt.Errorf("%d of %d generators have not confirmed the run stopped: %s", len(unproven), len(targets), strings.Join(unproven, ", "))
	}
	message := "controller execution ownership was interrupted before the run completed; every generator it reached has confirmed the run's work stopped"
	return true, tr.finalizeInterruptedDistributedRun(ctx, test, message)
}

// finalizeInterruptedDistributedRun reports an interrupted run FAILED through
// the ordinary terminal path (capacity release, events, CI observers). The
// archive is collected afterwards from the generators' retained sources by the
// archive-collection retry worker.
func (tr *TestRunner) finalizeInterruptedDistributedRun(ctx context.Context, test *ManagedTest, message string) error {
	unlock, err := tr.lockTestLifecycleControl(ctx, test.ID)
	if err != nil {
		return err
	}
	defer unlock()
	tr.mu.Lock()
	if tr.tests[test.ID] != test || !interruptedDistributedRunLocked(test) {
		tr.mu.Unlock()
		return errors.New("run is no longer an interrupted distributed run")
	}
	fenceErr := tr.claimTestExecutionFence(test)
	prior := strings.TrimSpace(strings.TrimSuffix(strings.ReplaceAll(test.Error, interruptedDistributedStopReason, ""), "; "))
	tr.mu.Unlock()
	if fenceErr != nil {
		return fenceErr
	}
	if prior != "" {
		message = boundedtext.TruncateUTF8(prior, 1024) + "; " + message
	}
	if err := tr.completeTestRun(test, "FAILED", time.Now().UTC(), message, nil); err != nil {
		return err
	}
	tr.mu.RLock()
	finalized := tr.tests[test.ID] != test || test.Status == "FAILED"
	tr.mu.RUnlock()
	if !finalized {
		return errors.New("interrupted run terminal state is not durable yet")
	}
	return nil
}

type resolveInterruptedRunRequest struct {
	// Attestation must equal interruptedRunResolutionAttestation: the operator
	// confirms that every generator process that could run the test is
	// stopped or fenced.
	Attestation string `json:"attestation"`
	Note        string `json:"note,omitempty"`
}

// handleResolveInterruptedRun is the operator override for an interrupted
// distributed run whose generators cannot all prove the run stopped (for
// example a generator that is gone for good). It is audited and requires an
// explicit attestation.
func (s *Server) handleResolveInterruptedRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireExecutionLeader(w) {
		return
	}
	var request resolveInterruptedRunRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	if request.Attestation != interruptedRunResolutionAttestation {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("attestation must be %q: confirm every generator process that could run this test is stopped or fenced", interruptedRunResolutionAttestation))
		return
	}
	id := r.PathValue("id")
	s.runner.mu.RLock()
	id = s.runner.resolveRefKeyLocked(id)
	test := s.runner.tests[id]
	eligible := interruptedDistributedRunLocked(test)
	projectID := ""
	if test != nil {
		projectID = test.ProjectID
	}
	s.runner.mu.RUnlock()
	if test == nil {
		writeError(w, http.StatusNotFound, "test not found")
		return
	}
	if !eligible {
		writeError(w, http.StatusConflict, "test is not an interrupted distributed run awaiting generator stop proof")
		return
	}
	actorType, actorSubject, actorName := s.resolveAuditActor(r)
	actor := firstNonEmpty(strings.TrimSpace(actorName), strings.TrimSpace(actorSubject), actorType)
	message := "controller execution ownership was interrupted before the run completed; resolved by " + actor + ", who attested that every generator process that could run it is stopped"
	if note := strings.TrimSpace(request.Note); note != "" {
		message += ": " + boundedtext.TruncateUTF8(scrub.Value(note, scrub.Policy{}), maxInterruptedRunResolutionNote)
	}
	if err := s.runner.finalizeInterruptedDistributedRun(r.Context(), test, message); err != nil {
		s.recordAuditEvent(r, "test.interrupted_run.resolve", "failure", "test", id, projectID, map[string]any{"error": err.Error()})
		if errors.Is(err, errNotExecutionLeader) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.recordAuditEvent(r, "test.interrupted_run.resolve", "success", "test", id, projectID, map[string]any{"note_present": strings.TrimSpace(request.Note) != ""})
	got, _ := s.runner.GetTest(id)
	writeJSON(w, http.StatusOK, maskManagedTest(got))
}

var executionRecoveryAPIRouteCatalog = apiRouteCatalog{
	Domain: "execution-recovery",
	Routes: []apiRouteSpec{
		newAuthenticatedAPIRoute(http.MethodPost, "/api/v1/tests/{id}/interrupted-stop/resolve", "handleResolveInterruptedRun", accessRequirement{GlobalRoles: []string{RoleSystemAdmin}, MaxBodyBytes: 8192}, func(s *Server) http.Handler {
			return http.HandlerFunc(s.handleResolveInterruptedRun)
		}),
	},
}
