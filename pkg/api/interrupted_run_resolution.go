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
const interruptedDistributedStopReason = "controller execution ownership was interrupted; the run is held STOPPING until every generator it reached confirms its work stopped, or a system administrator attests that they are stopped (POST /api/v1/tests/{id}/interrupted-stop/resolve)"

// legacyInterruptedDistributedStopReason is the same marker as written by
// controllers that predate automatic resolution. Rows that carry it are still
// interrupted runs; reconcile rewrites it to the current guidance.
const legacyInterruptedDistributedStopReason = "controller execution ownership was interrupted; distributed generator stop is unproven without a complete durable participant journal; external generator process-fence reconciliation is required"

// hasInterruptedDistributedStopReason reports whether a run error carries
// either form of the interrupted-run marker.
func hasInterruptedDistributedStopReason(message string) bool {
	return strings.Contains(message, interruptedDistributedStopReason) || strings.Contains(message, legacyInterruptedDistributedStopReason)
}

// withoutInterruptedDistributedStopReason removes either form of the marker,
// leaving any earlier error the run recorded.
func withoutInterruptedDistributedStopReason(message string) string {
	for _, reason := range []string{interruptedDistributedStopReason, legacyInterruptedDistributedStopReason} {
		message = strings.ReplaceAll(message, "; "+reason, "")
		message = strings.ReplaceAll(message, reason, "")
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(message), ";"))
}

// errInterruptedRunNotEligible means the run is not (or no longer) an
// interrupted distributed run awaiting generator stop proof.
var errInterruptedRunNotEligible = errors.New("test is not an interrupted distributed run awaiting generator stop proof")

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
	// A target that accepted this term's claim stays proven for the term: it
	// can only receive work of this or a later generation afterwards.
	proven := make(interruptedRunProofs)
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
		pending := s.runner.resolveInterruptedDistributedRuns(ctx, generation, proven)
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

// An interrupted evacuation is excluded: it is resolved only with evacuation
// fence proof, which its own recovery requires before cleanup.
func interruptedDistributedRunLocked(test *ManagedTest) bool {
	return test != nil && test.Status == "STOPPING" && test.cancel == nil && test.executionCtx == nil &&
		hasInterruptedDistributedStopReason(test.Error) && !interruptedRunEvacuation(test.continuityCheckpoint)
}

// interruptedEvacuationAwaitingProofLocked reports a run held STOPPING because
// leadership changed during a generator evacuation. Evacuation recovery fences
// it automatically while every possible participant is reachable; the operator
// override covers the rest.
func interruptedEvacuationAwaitingProofLocked(test *ManagedTest) bool {
	return test != nil && test.Status == "STOPPING" && test.cancel == nil && test.executionCtx == nil &&
		interruptedRunEvacuation(test.continuityCheckpoint)
}

// interruptedRunProofs records, per run, the targets that already proved the
// run's older-term load is gone in the current term.
type interruptedRunProofs map[string]map[string]bool

// resolveInterruptedDistributedRuns finalizes every interrupted run whose
// generators all prove the run's load is gone, and returns how many remain.
// proven carries per-target proof across calls within one term (nil = none).
func (tr *TestRunner) resolveInterruptedDistributedRuns(ctx context.Context, generation uint64, proven interruptedRunProofs) int {
	if proven == nil {
		proven = make(interruptedRunProofs)
	}
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
		if proven[test.ID] == nil {
			proven[test.ID] = make(map[string]bool)
		}
		resolved, err := tr.resolveInterruptedDistributedRun(ctx, backend, test, generation, proven[test.ID])
		if err != nil {
			slog.Info("interrupted distributed run is still unresolved", "test_id", test.ID, "error", err)
		}
		if resolved {
			delete(proven, test.ID)
		} else {
			pending++
		}
	}
	return pending
}

// resolveInterruptedDistributedRun proves, per generator, that the run's load
// from the lost term is gone and then finalizes the run FAILED. It needs HA
// execution generations; a single-controller deployment that restarts during
// a distributed run has no newer term to prove against and relies on the
// operator override.
//
// The run's archive-collection intent is its participant journal: every target
// is recorded durably before work is dispatched to it, including evacuation
// replacements. An epoch claim of this (newer) term that a target accepts
// proves that target holds no assignment of an older term and no unfinished
// fence. A target that cannot be reached, or a run with no journal, stays
// STOPPING; the operator override covers generators that are gone for good.
func (tr *TestRunner) resolveInterruptedDistributedRun(ctx context.Context, backend *distributedExecutionBackend, test *ManagedTest, generation uint64, proven map[string]bool) (bool, error) {
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
		if proven[target] {
			continue
		}
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
			continue
		}
		proven[target] = true
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
		return errInterruptedRunNotEligible
	}
	fenceErr := tr.claimTestExecutionFence(test)
	prior := withoutInterruptedDistributedStopReason(test.Error)
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

// finalizeAttestedInterruptedEvacuation records an administrator's attestation
// as the evacuation's fence proof. The run ends FAILED with
// interruptedEvacuationAttestedProof, and evacuation recovery then performs
// only its post-terminal archive and pod-capacity cleanup.
func (tr *TestRunner) finalizeAttestedInterruptedEvacuation(ctx context.Context, test *ManagedTest, message string) error {
	unlock, err := tr.lockTestLifecycleControl(ctx, test.ID)
	if err != nil {
		return err
	}
	defer unlock()
	tr.mu.Lock()
	if tr.tests[test.ID] != test || !interruptedEvacuationAwaitingProofLocked(test) {
		tr.mu.Unlock()
		return errInterruptedRunNotEligible
	}
	fenceErr := tr.claimTestExecutionFence(test)
	prior := strings.TrimSpace(test.Error)
	tr.mu.Unlock()
	if fenceErr != nil {
		return fenceErr
	}
	if prior != "" {
		message += "; " + prior
	}
	message = boundedtext.TruncateUTF8(message, 2048)
	if err := tr.completeTestRun(test, "FAILED", time.Now().UTC(), message, nil); err != nil {
		return err
	}
	tr.mu.RLock()
	proven := tr.tests[test.ID] == test && interruptedEvacuationHasDurableFenceProof(test)
	tr.mu.RUnlock()
	if !proven {
		return errors.New("interrupted run terminal state is not durable yet")
	}
	tr.enqueueTerminalRetry("evacuation-guard:"+test.ID, "interrupted evacuation process fences", func() error {
		return tr.recoverInterruptedRunEvacuation(test)
	})
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
// distributed run, or an interrupted generator evacuation, whose generators
// cannot all prove the run stopped (for example a generator that is gone for
// good). It is audited and requires an explicit attestation.
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
	evacuation := interruptedEvacuationAwaitingProofLocked(test)
	eligible := evacuation || interruptedDistributedRunLocked(test)
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
		writeError(w, http.StatusConflict, errInterruptedRunNotEligible.Error())
		return
	}
	actorType, actorSubject, actorName := s.resolveAuditActor(r)
	actor := firstNonEmpty(strings.TrimSpace(actorName), strings.TrimSpace(actorSubject), actorType)
	note := ""
	if trimmed := strings.TrimSpace(request.Note); trimmed != "" {
		note = ": " + boundedtext.TruncateUTF8(scrub.Value(trimmed, scrub.Policy{}), maxInterruptedRunResolutionNote)
	}
	var err error
	if evacuation {
		err = s.runner.finalizeAttestedInterruptedEvacuation(r.Context(), test, interruptedEvacuationAttestedProof+"; resolved by "+actor+note)
	} else {
		err = s.runner.finalizeInterruptedDistributedRun(r.Context(), test, "controller execution ownership was interrupted before the run completed; resolved by "+actor+", who attested that every generator process that could run it is stopped"+note)
	}
	if err != nil {
		s.recordAuditEvent(r, "test.interrupted_run.resolve", "failure", "test", id, projectID, map[string]any{"error": err.Error()})
		if errors.Is(err, errInterruptedRunNotEligible) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		// Lost leadership, or a terminal write that failed transiently and is
		// queued for retry: the caller may retry the resolution.
		writeError(w, http.StatusServiceUnavailable, "could not record the resolution yet; retry: "+err.Error())
		return
	}
	s.recordAuditEvent(r, "test.interrupted_run.resolve", "success", "test", id, projectID, map[string]any{"note_present": strings.TrimSpace(request.Note) != "", "evacuation": evacuation})
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
