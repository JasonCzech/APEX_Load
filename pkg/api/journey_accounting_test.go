package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// journeyTimeline builds one VUser's archived rows in recording order: a
// transaction's row is written after its own steps, as the engine does.
type journeyTimeline struct {
	group string
	vuser int
	at    time.Time
	rows  []TransactionEvent
}

func (l *journeyTimeline) row(iteration int64, name string, transaction, success, interrupted bool, begin time.Time) {
	l.rows = append(l.rows, TransactionEvent{Timestamp: begin, Group: l.group, VUserID: l.vuser, Iteration: iteration, ActionName: name, BusinessTransaction: transaction, Success: success, Interrupted: interrupted, DurationMs: float64(l.at.Sub(begin)) / float64(time.Millisecond)})
}

func (l *journeyTimeline) step(iteration int64, name string, success bool) {
	begin := l.at
	l.at = l.at.Add(10 * time.Millisecond)
	l.row(iteration, name, false, success, false, begin)
}

// stopped is a step the run's stop cut: a failure marked interrupted.
func (l *journeyTimeline) stopped(iteration int64, name string) {
	begin := l.at
	l.at = l.at.Add(10 * time.Millisecond)
	l.row(iteration, name, false, false, true, begin)
}

// ended is the row the engine records when the iteration ends on its own
// (the iteration transaction): ends_iteration.
func (l *journeyTimeline) ended(iteration int64, success bool) {
	begin := l.at
	l.at = l.at.Add(time.Millisecond)
	l.row(iteration, "Iteration", true, success, false, begin)
	l.rows[len(l.rows)-1].EndsIteration = true
}

// transaction runs the steps inside a transaction and writes its row last.
func (l *journeyTimeline) transaction(iteration int64, name string, success bool, steps func()) {
	begin := l.at
	steps()
	l.row(iteration, name, true, success, false, begin)
}

func journeyFixturePolicy(stepRisk bool) JourneyPolicyDefinition {
	return JourneyPolicyDefinition{Version: analysisPolicyVersion, StepRiskEnabled: stepRisk, Journeys: []JourneyDefinition{
		{ID: "purchase", Name: "Purchase", Start: JourneyBoundary{Transaction: "Browse"}, Success: JourneyBoundary{Transaction: "Checkout"}, Weight: 3},
		{ID: "login", Name: "Login", Start: JourneyBoundary{Action: "open_login"}, Success: JourneyBoundary{Action: "submit_login"}, Weight: 1},
	}}
}

// journeyFixtureRows is a run that ended with a stop, with known outcomes
// (see TestJourneyAccountingClassifiesEveryAttempt).
func journeyFixtureRows() []TransactionEvent {
	start := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	v1 := &journeyTimeline{group: "web", vuser: 1, at: start}
	v1.step(0, "init", true)
	// Iteration 1: purchase completed, then login completed.
	v1.transaction(1, "Browse", true, func() { v1.step(1, "browse_a", true); v1.step(1, "browse_b", true) })
	v1.transaction(1, "Checkout", true, func() { v1.step(1, "pay", true) })
	v1.step(1, "open_login", true)
	v1.step(1, "submit_login", true)
	// Iteration 2: login completed, then purchase failed at its boundary.
	v1.step(2, "open_login", true)
	v1.step(2, "submit_login", true)
	v1.transaction(2, "Browse", true, func() { v1.step(2, "browse_a", true); v1.step(2, "browse_b", true) })
	v1.transaction(2, "Checkout", false, func() { v1.step(2, "pay", false) })
	// Iteration 3: purchase completed despite a failed step; login skipped.
	v1.transaction(3, "Browse", false, func() { v1.step(3, "browse_a", false); v1.step(3, "browse_b", true) })
	v1.transaction(3, "Checkout", true, func() { v1.step(3, "pay", true) })
	// Iteration 4: the stop cut Browse (the engine recorded the step and the
	// transaction as interrupted failures): purchase censored, and login is
	// not counted as skipped in a cut iteration.
	begin := v1.at
	v1.step(4, "browse_a", true)
	v1.stopped(4, "browse_b")
	v1.row(4, "Browse", true, false, true, begin)
	v1.step(0, "teardown", true)

	v2 := &journeyTimeline{group: "web", vuser: 2, at: start}
	// Iteration 1: login failed (the iteration ended after a failed step);
	// purchase skipped.
	v2.step(1, "open_login", true)
	v2.step(1, "account", false)
	// Iteration 2: purchase restarted without its outcome and never reached
	// it: two aborted attempts (logout belongs to the second, still open);
	// login skipped.
	v2.transaction(2, "Browse", true, func() { v2.step(2, "browse_a", true) })
	v2.transaction(2, "Browse", true, func() { v2.step(2, "browse_a", true); v2.step(2, "browse_b", true) })
	v2.step(2, "logout", true)
	// Iteration 3, its last before the stop: login genuinely failed at its
	// boundary, which counts as failed even in a last iteration.
	v2.step(3, "open_login", true)
	v2.step(3, "submit_login", false)

	// The stop failed VUser 3's login boundary: censored, not failed.
	v3 := &journeyTimeline{group: "web", vuser: 3, at: start}
	v3.step(1, "open_login", true)
	v3.stopped(1, "submit_login")

	// A group that never starts either journey has no skips.
	api := &journeyTimeline{group: "api", vuser: 1, at: start}
	api.step(1, "ping", true)
	api.step(2, "ping", true)

	var rows []TransactionEvent
	for i := 0; i < max(len(v1.rows), len(v2.rows), len(v3.rows), len(api.rows)); i++ {
		for _, line := range []*journeyTimeline{v1, v2, v3, api} {
			if i < len(line.rows) {
				rows = append(rows, line.rows[i])
			}
		}
	}
	return rows
}

func accountJourneyRows(policy JourneyPolicyDefinition, rows []TransactionEvent) *JourneyRiskReport {
	report := &JourneyRiskReport{StepRiskEnabled: policy.StepRiskEnabled}
	acc := newJourneyAccumulator(policy, report)
	for i := range rows {
		acc.add(&rows[i])
	}
	if acc.identity {
		acc.finish()
	}
	return report
}

func journeyByID(t *testing.T, report *JourneyRiskReport, id string) JourneyAccounting {
	t.Helper()
	for _, journey := range report.Journeys {
		if journey.ID == id {
			return journey
		}
	}
	t.Fatalf("journey %s missing from %+v", id, report.Journeys)
	return JourneyAccounting{}
}

func TestJourneyAccountingClassifiesEveryAttempt(t *testing.T) {
	report := accountJourneyRows(journeyFixturePolicy(true), journeyFixtureRows())
	purchase := journeyByID(t, report, "purchase")
	type counts struct{ completed, failed, aborted, skipped, censored, eligible, steps, stepFailures int64 }
	if got, want := (counts{purchase.Completed, purchase.Failed, purchase.Aborted, purchase.Skipped, purchase.Censored, purchase.Eligible, purchase.Steps, purchase.StepFailures}), (counts{2, 1, 2, 1, 1, 5, 13, 2}); got != want {
		t.Fatalf("purchase = %+v, want %+v", got, want)
	}
	login := journeyByID(t, report, "login")
	if got, want := (counts{login.Completed, login.Failed, login.Aborted, login.Skipped, login.Censored, login.Eligible, login.Steps, login.StepFailures}), (counts{2, 2, 0, 2, 1, 4, 8, 2}); got != want {
		t.Fatalf("login = %+v, want %+v", got, want)
	}
	if purchase.FailureRate == nil || math.Abs(*purchase.FailureRate-0.2) > 1e-12 || login.FailureRate == nil || math.Abs(*login.FailureRate-0.5) > 1e-12 {
		t.Fatalf("failure rates %v %v", purchase.FailureRate, login.FailureRate)
	}
	// One failed journey counts once however many of its steps failed.
	if report.WeightedFailureIndex == nil || math.Abs(*report.WeightedFailureIndex-5.0/19.0) > 1e-12 {
		t.Fatalf("weighted failure index %v, want 5/19", report.WeightedFailureIndex)
	}
	if report.StepRiskIndex == nil || math.Abs(*report.StepRiskIndex-8.0/47.0) > 1e-12 {
		t.Fatalf("step risk index %v, want 8/47", report.StepRiskIndex)
	}
	if report.RowsOutsideIterations != 2 || report.Incomplete {
		t.Fatalf("outside %d incomplete %v", report.RowsOutsideIterations, report.Incomplete)
	}
	var failed, censored *JourneyAttemptRef
	for i := range purchase.Examples {
		switch purchase.Examples[i].Outcome {
		case "failed":
			failed = &purchase.Examples[i]
		case "censored":
			censored = &purchase.Examples[i]
		}
	}
	if failed == nil || failed.VUserID != 1 || failed.Iteration != 2 || failed.FailedStep != "pay" || failed.Group != "web" {
		t.Fatalf("failed example %+v", failed)
	}
	if censored == nil || censored.VUserID != 1 || censored.Iteration != 4 {
		t.Fatalf("censored example %+v", censored)
	}
	outcomes := map[string]string{}
	for _, example := range login.Examples {
		outcomes[fmt.Sprintf("%d/%d", example.VUserID, example.Iteration)] = example.Outcome + ":" + example.FailedStep
	}
	if want := map[string]string{"2/1": "failed:account", "2/3": "failed:submit_login", "3/1": "censored:"}; !reflect.DeepEqual(outcomes, want) {
		t.Fatalf("login examples %v, want %v", outcomes, want)
	}
}

func TestJourneyAccountingIndexesNeedWeightsAndStepRiskIsOptIn(t *testing.T) {
	policy := journeyFixturePolicy(false)
	report := accountJourneyRows(policy, journeyFixtureRows())
	if report.StepRiskIndex != nil || report.StepRiskUnscored != "" {
		t.Fatalf("step risk reported without opt-in: %v %q", report.StepRiskIndex, report.StepRiskUnscored)
	}
	for i := range policy.Journeys {
		policy.Journeys[i].Weight = 0
	}
	policy.StepRiskEnabled = true
	report = accountJourneyRows(policy, journeyFixtureRows())
	if report.WeightedFailureIndex != nil || report.IndexUnscored != "no_weighted_eligible_attempts" || report.StepRiskIndex != nil || report.StepRiskUnscored != "no_weighted_steps" {
		t.Fatalf("zero weights: index %v %q step %v %q", report.WeightedFailureIndex, report.IndexUnscored, report.StepRiskIndex, report.StepRiskUnscored)
	}
	// The counts do not depend on the weights.
	if purchase := journeyByID(t, report, "purchase"); purchase.Eligible != 5 || purchase.Failed != 1 {
		t.Fatalf("purchase %+v", purchase)
	}
}

// A replacement generator continues an evacuated VUser from iteration 1: the
// earlier share's last iteration may have been cut, so it is censored.
func TestJourneyAccountingCensorsAnEvacuatedShare(t *testing.T) {
	start := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	first := &journeyTimeline{group: "web", vuser: 7, at: start}
	first.step(1, "open_login", true)
	first.step(1, "submit_login", true)
	first.step(2, "open_login", true)
	replacement := &journeyTimeline{group: "web", vuser: 7, at: start.Add(time.Minute)}
	replacement.step(1, "open_login", true)
	replacement.step(1, "account", true)
	replacement.step(2, "open_login", true)
	replacement.step(2, "submit_login", true)
	rows := append(first.rows, replacement.rows...)
	login := journeyByID(t, accountJourneyRows(journeyFixturePolicy(false), rows), "login")
	if login.Completed != 2 || login.Censored != 1 || login.Aborted != 1 || login.Failed != 0 {
		t.Fatalf("login %+v", login)
	}
}

func TestJourneyAccountingBoundsAnIterationAndWithholdsTheIndex(t *testing.T) {
	timeline := &journeyTimeline{group: "web", vuser: 1, at: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)}
	timeline.step(1, "open_login", true)
	for i := 0; i < maxJourneyIterationEvents; i++ {
		timeline.step(1, "poll", true)
	}
	timeline.step(1, "submit_login", true)
	timeline.step(2, "open_login", true)
	timeline.step(2, "submit_login", true)
	report := accountJourneyRows(journeyFixturePolicy(true), timeline.rows)
	if !report.Incomplete || report.WeightedFailureIndex != nil || report.IndexUnscored != "accounting_incomplete" || report.StepRiskIndex != nil || report.StepRiskUnscored != "accounting_incomplete" {
		t.Fatalf("bounded: incomplete %v index %v %q step %v %q", report.Incomplete, report.WeightedFailureIndex, report.IndexUnscored, report.StepRiskIndex, report.StepRiskUnscored)
	}
	if login := journeyByID(t, report, "login"); login.Completed != 1 {
		t.Fatalf("an iteration within the bound is still accounted: %+v", login)
	}
}

// journeyArchive writes a finalized archive of rows for run id, pinning the
// given journey policy revision (when pin is set).
func journeyArchive(t *testing.T, server *Server, store *storage.SQLiteStore, id string, rows []TransactionEvent, pin *RunPolicyPin) {
	t.Helper()
	start := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	root := t.TempDir()
	provenance := RunArchiveProvenance{ProjectID: storage.DefaultProjectID, StartedAt: &start}
	if pin != nil {
		provenance.PolicyPins = []RunPolicyPin{*pin}
	}
	manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: id, TestName: id, Status: "COMPLETE", CreatedAt: start, CompletedAt: start.Add(time.Minute), Provenance: provenance, InitialProvenance: &provenance, TransactionsFile: "transactions.jsonl", TransactionCount: int64(len(rows))}
	var lines strings.Builder
	for _, row := range rows {
		data, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		lines.Write(data)
		lines.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(root, manifest.TransactionsFile), []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(manifestBytes)
	if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, ProjectID: storage.DefaultProjectID, ScenarioID: "scenario", Status: "COMPLETE", Groups: []any{}, StartTime: start}); err != nil {
		t.Fatal(err)
	}
	end := start.Add(time.Minute)
	record := &storage.RunArchiveRecord{TestID: id, SchemaVersion: archiveSchemaVersion, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json"), ManifestSHA256: hex.EncodeToString(sum[:]), CreatedAt: start, CompletedAt: end, TransactionCount: int64(len(rows))}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.tests[id] = &ManagedTest{ID: id, Name: id, ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartedAt: &start, EndedAt: &end}
	server.runner.archives[id] = record
	server.runner.mu.Unlock()
}

func saveJourneyPolicy(t *testing.T, store *storage.SQLiteStore, id string, revision int64, policy JourneyPolicyDefinition) RunPolicyPin {
	t.Helper()
	body, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: id, ProjectID: storage.DefaultProjectID, Kind: storage.AnalysisDefinitionKindJourneyPolicy, Name: "Journeys " + id, Revision: revision, Definition: body}, revision-1); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	return RunPolicyPin{Kind: storage.AnalysisDefinitionKindJourneyPolicy, ID: id, Name: "Journeys " + id, Revision: revision, SHA256: hex.EncodeToString(digest[:]), Source: "project_default"}
}

func getJourneys(t *testing.T, server *Server, id, query string) (int, *JourneyRiskReport, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tests/"+id+"/archive/journeys"+query, nil)
	request.SetPathValue("id", id)
	recorder := httptest.NewRecorder()
	server.handleGetRunArchiveJourneys(recorder, request)
	if recorder.Code != http.StatusOK {
		return recorder.Code, nil, recorder.Body.String()
	}
	var report JourneyRiskReport
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return recorder.Code, &report, recorder.Body.String()
}

func TestJourneyEndpointAppliesThePinnedOrRequestedPolicy(t *testing.T) {
	server, store := baselineTestServer(t)
	pin := saveJourneyPolicy(t, store, "journeys", 1, journeyFixturePolicy(true))
	journeyArchive(t, server, store, "journey-run", journeyFixtureRows(), &pin)
	admitArchiveForTest(t, server.runner.archives["journey-run"])
	// Revision 2 changes only the weights.
	changed := journeyFixturePolicy(false)
	changed.Journeys[1].Weight = 0
	saveJourneyPolicy(t, store, "journeys", 2, changed)

	code, report, body := getJourneys(t, server, "journey-run", "")
	if code != http.StatusOK || report.Availability != "available" || report.Policy.Source != "pinned" || report.Policy.Revision != 1 || report.Policy.SHA256 != pin.SHA256 || report.SchemaVersion != journeyRiskSchemaVersion {
		t.Fatalf("pinned: %d %s", code, body)
	}
	if report.WeightedFailureIndex == nil || math.Abs(*report.WeightedFailureIndex-5.0/19.0) > 1e-12 || report.StepRiskIndex == nil {
		t.Fatalf("pinned index %s", body)
	}
	code, report, body = getJourneys(t, server, "journey-run", "?policy=journeys&revision=2")
	if code != http.StatusOK || report.Policy.Source != "requested" || report.Policy.Revision != 2 || report.StepRiskEnabled || report.WeightedFailureIndex == nil || math.Abs(*report.WeightedFailureIndex-0.2) > 1e-12 {
		t.Fatalf("requested: %d %s", code, body)
	}
	// Without a revision the latest applies.
	if code, report, body = getJourneys(t, server, "journey-run", "?policy=journeys"); code != http.StatusOK || report.Policy.Revision != 2 {
		t.Fatalf("latest: %d %s", code, body)
	}
	for query, want := range map[string]int{
		"?revision=2":                         http.StatusBadRequest,
		"?policy=journeys&revision=0":         http.StatusBadRequest,
		"?policy=missing":                     http.StatusNotFound,
		"?policy=journeys&revision=9":         http.StatusNotFound,
		"?policy=" + strings.Repeat("x", 129): http.StatusBadRequest,
	} {
		if code, _, body := getJourneys(t, server, "journey-run", query); code != want {
			t.Fatalf("%s: %d %s, want %d", query, code, body, want)
		}
	}
}

func TestJourneyEndpointRefusesWhatItCannotAnswer(t *testing.T) {
	server, store := baselineTestServer(t)
	pin := saveJourneyPolicy(t, store, "journeys", 1, journeyFixturePolicy(false))

	// No pin and no request: unavailable, not an error.
	journeyArchive(t, server, store, "unpinned", journeyFixtureRows(), nil)
	admitArchiveForTest(t, server.runner.archives["unpinned"])
	if code, report, body := getJourneys(t, server, "unpinned", ""); code != http.StatusOK || report.Availability != "unavailable" || report.Reason != "no_journey_policy" {
		t.Fatalf("unpinned: %d %s", code, body)
	}

	// An archive written before VUser iteration identity is unavailable.
	legacy := journeyFixtureRows()
	for i := range legacy {
		legacy[i].VUserID, legacy[i].Iteration = 0, 0
	}
	journeyArchive(t, server, store, "legacy", legacy, &pin)
	if code, report, body := getJourneys(t, server, "legacy", ""); code != http.StatusOK || report.Availability != "unavailable" || report.Reason != "archive_lacks_vuser_iteration_identity" || len(report.Journeys) != 0 || report.WeightedFailureIndex != nil {
		t.Fatalf("legacy: %d %s", code, body)
	}

	// A pin whose digest no longer matches the stored revision.
	tampered := pin
	tampered.SHA256 = strings.Repeat("0", 64)
	journeyArchive(t, server, store, "tampered", journeyFixtureRows(), &tampered)
	if code, _, body := getJourneys(t, server, "tampered", ""); code != http.StatusConflict {
		t.Fatalf("tampered pin: %d %s", code, body)
	}

	// A policy of another kind is not a journey policy.
	body, _ := json.Marshal(TelemetryPolicyDefinition{Version: analysisPolicyVersion})
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: "telemetry", ProjectID: storage.DefaultProjectID, Kind: storage.AnalysisDefinitionKindTelemetryPolicy, Name: "Telemetry", Revision: 1, Definition: body}, 0); err != nil {
		t.Fatal(err)
	}
	if code, _, body := getJourneys(t, server, "unpinned", "?policy=telemetry&revision=1"); code != http.StatusNotFound {
		t.Fatalf("other kind: %d %s", code, body)
	}

	// A run whose archive is not finalized.
	server.runner.archives["unpinned"].ManifestSHA256 = ""
	if code, _, body := getJourneys(t, server, "unpinned", ""); code != http.StatusConflict {
		t.Fatalf("unfinalized: %d %s", code, body)
	}
}

// The journey report reads the archive the run actually wrote.
func TestAccountJourneysReadsTheArchive(t *testing.T) {
	server, store := baselineTestServer(t)
	journeyArchive(t, server, store, "archived", journeyFixtureRows(), nil)
	record := server.runner.archives["archived"]
	admitArchiveForTest(t, record)
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	report, err := accountJourneys(context.Background(), record, manifest, journeyFixturePolicy(false), JourneyPolicyRef{ID: "p", Source: "requested"})
	if err != nil {
		t.Fatal(err)
	}
	if report.RunID != "archived" || journeyByID(t, report, "purchase").Eligible != 5 || len(report.Notes) == 0 {
		t.Fatalf("report %+v", report)
	}
}

// Boundaries match only their own kind of sample: per-action automatic
// transactions share their action's name.
func TestJourneyBoundariesMatchTheirOwnSampleKind(t *testing.T) {
	policy := JourneyPolicyDefinition{Version: analysisPolicyVersion, Journeys: []JourneyDefinition{
		{ID: "pay", Name: "Pay", Start: JourneyBoundary{Transaction: "Cart"}, Success: JourneyBoundary{Transaction: "Pay"}, Weight: 1},
		{ID: "login", Name: "Login", Start: JourneyBoundary{Action: "open"}, Success: JourneyBoundary{Action: "submit"}, Weight: 1},
	}}
	l := &journeyTimeline{group: "web", vuser: 1, at: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)}
	// Iteration 1: the Pay transaction passes though its own Pay action
	// failed; a transaction named open is not the login action.
	l.transaction(1, "Cart", true, func() { l.step(1, "Cart", true) })
	l.transaction(1, "Pay", true, func() { l.step(1, "Pay", false) })
	l.transaction(1, "open", true, func() { l.step(1, "browse", true) })
	l.step(1, "submit", true)
	// Iteration 2: a failed transaction boundary with no failed step names
	// itself as the failed step.
	l.transaction(2, "Cart", true, func() { l.step(2, "Cart", true) })
	l.transaction(2, "Pay", false, func() { l.step(2, "Pay", true) })
	l.step(3, "idle", true)
	report := accountJourneyRows(policy, l.rows)
	pay, login := journeyByID(t, report, "pay"), journeyByID(t, report, "login")
	if pay.Completed != 1 || pay.Failed != 1 || pay.Aborted != 0 || pay.Eligible != 2 || pay.StepFailures != 1 {
		t.Fatalf("pay %+v", pay)
	}
	if len(pay.Examples) != 1 || pay.Examples[0].Iteration != 2 || pay.Examples[0].FailedStep != "Pay" {
		t.Fatalf("pay examples %+v", pay.Examples)
	}
	if login.Completed != 0 || login.Eligible != 0 {
		t.Fatalf("a transaction named open started login: %+v", login)
	}
}

// An interrupted failure cuts what was open even when the VUser goes on (a
// synthetic case: the engine stops the VUser after one): the cut attempts
// are censored, a restart after the cut settles the earlier attempt as
// censored, and the cut iteration counts no skips. A successful row marked
// interrupted (malformed) cuts nothing.
func TestJourneyInterruptionsCensorWhatTheyCut(t *testing.T) {
	policy := JourneyPolicyDefinition{Version: analysisPolicyVersion, Journeys: []JourneyDefinition{
		{ID: "login", Name: "Login", Start: JourneyBoundary{Action: "open_login"}, Success: JourneyBoundary{Action: "submit_login"}, Weight: 1},
		{ID: "logout", Name: "Logout", Start: JourneyBoundary{Action: "logout"}, Success: JourneyBoundary{Action: "logout_done"}, Weight: 1},
	}}
	start := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	cut := &journeyTimeline{group: "web", vuser: 9, at: start}
	cut.step(1, "open_login", true)
	cut.stopped(1, "account")
	cut.step(1, "open_login", true)
	cut.step(2, "open_login", true)
	cut.step(2, "submit_login", true)
	cut.step(2, "logout", true)
	cut.step(2, "logout_done", true)
	cut.step(3, "idle", true)
	// The cut is an abandoned (interrupted) transaction that is no boundary.
	abandoned := &journeyTimeline{group: "web", vuser: 11, at: start}
	begin := abandoned.at
	abandoned.step(1, "open_login", true)
	abandoned.at = abandoned.at.Add(10 * time.Millisecond)
	abandoned.row(1, "Iteration", true, false, true, begin)
	abandoned.step(2, "idle", true)
	// A malformed successful row marked interrupted cuts nothing.
	malformed := &journeyTimeline{group: "web", vuser: 12, at: start}
	malformed.step(1, "open_login", true)
	malformed.row(1, "account", false, true, true, malformed.at)
	malformed.at = malformed.at.Add(10 * time.Millisecond)
	malformed.step(2, "idle", true)
	rows := append(append(cut.rows, abandoned.rows...), malformed.rows...)
	report := accountJourneyRows(policy, rows)
	login, logout := journeyByID(t, report, "login"), journeyByID(t, report, "logout")
	// Censored: VUser 9's two iteration-1 attempts, VUser 11's. Aborted:
	// VUser 12's. Completed: VUser 9's second iteration.
	if login.Censored != 3 || login.Aborted != 1 || login.Completed != 1 || login.Failed != 0 {
		t.Fatalf("login %+v", login)
	}
	// Logout skips: only VUser 12's iteration 1 (VUser 9's and 11's first
	// iterations were cut; last iterations never count).
	if logout.Skipped != 1 || logout.Completed != 1 {
		t.Fatalf("logout %+v", logout)
	}
}

func TestJourneyAccountingBoundsTrackedVUsers(t *testing.T) {
	policy := JourneyPolicyDefinition{Version: analysisPolicyVersion, Journeys: []JourneyDefinition{
		{ID: "login", Name: "Login", Start: JourneyBoundary{Action: "open_login"}, Success: JourneyBoundary{Action: "submit_login"}, Weight: 1},
	}}
	report := &JourneyRiskReport{}
	acc := newJourneyAccumulator(policy, report)
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	for vuser := 0; vuser <= maxJourneyTrackedVUsers; vuser++ {
		acc.add(&TransactionEvent{Timestamp: at, Group: "web", VUserID: vuser, Iteration: 1, ActionName: "idle", Success: true})
	}
	if !report.Incomplete || len(acc.vusers) != maxJourneyTrackedVUsers {
		t.Fatalf("incomplete %v tracked %d", report.Incomplete, len(acc.vusers))
	}
}

// A VUser ID is reused by later VUsers (open-workload arrivals): each
// lifetime is its own context, and one that ended well before the run's end
// is settled rather than censored.
func TestJourneyAccountingSeparatesVUserLifetimes(t *testing.T) {
	policy := JourneyPolicyDefinition{Version: analysisPolicyVersion, Journeys: []JourneyDefinition{
		{ID: "login", Name: "Login", Start: JourneyBoundary{Action: "open_login"}, Success: JourneyBoundary{Action: "submit_login"}, Weight: 1},
	}}
	start := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	lifetime := func(id uint64, at time.Time, steps func(l *journeyTimeline)) []TransactionEvent {
		l := &journeyTimeline{group: "arrivals", vuser: 0, at: at}
		steps(l)
		for i := range l.rows {
			l.rows[i].VUserLifetime = id
		}
		return l.rows
	}
	var rows []TransactionEvent
	// Arrivals reuse VUser 0, each in its iteration 1: the first completes;
	// the second fails a step and the engine records its iteration ending
	// on its own (fail-fast), so it failed rather than being censored.
	rows = append(rows, lifetime(1, start, func(l *journeyTimeline) {
		l.step(1, "open_login", true)
		l.step(1, "submit_login", true)
		l.ended(1, true)
	})...)
	rows = append(rows, lifetime(2, start.Add(time.Second), func(l *journeyTimeline) {
		l.step(1, "open_login", true)
		l.step(1, "account", false)
		l.ended(1, false)
	})...)
	// A third arrival never starts the journey: skipped.
	rows = append(rows, lifetime(3, start.Add(2*time.Second), func(l *journeyTimeline) { l.step(1, "browse", true); l.ended(1, true) })...)
	// Lifetimes with no record of their iteration ending may have been cut by
	// a stop, however early they ended (a scale-down, a ramp-down, the run's
	// end during think time): censored, never settled.
	rows = append(rows, lifetime(4, start.Add(3*time.Second), func(l *journeyTimeline) { l.step(1, "open_login", true); l.step(1, "account", false) })...)
	rows = append(rows, lifetime(5, start.Add(2*time.Minute), func(l *journeyTimeline) { l.step(1, "open_login", true) })...)
	// A stop's record of the iteration (interrupted) is not an end of its own.
	rows = append(rows, lifetime(6, start.Add(3*time.Minute), func(l *journeyTimeline) {
		l.step(1, "open_login", true)
		l.stopped(1, "Iteration")
		l.rows[len(l.rows)-1].BusinessTransaction, l.rows[len(l.rows)-1].EndsIteration = true, true
	})...)
	login := journeyByID(t, accountJourneyRows(policy, rows), "login")
	if login.Completed != 1 || login.Failed != 1 || login.Skipped != 1 || login.Censored != 3 || login.Aborted != 0 {
		t.Fatalf("login %+v", login)
	}
	// The same rows in another order (an evacuation's replacement bundle
	// first) give the same outcome.
	reversed := append([]TransactionEvent(nil), rows[len(rows)/2:]...)
	reversed = append(reversed, rows[:len(rows)/2]...)
	if again := journeyByID(t, accountJourneyRows(policy, reversed), "login"); again.Completed != 1 || again.Failed != 1 || again.Skipped != 1 || again.Censored != 3 {
		t.Fatalf("reordered login %+v", again)
	}
}

// A group's once-only init/teardown VUser (-1) records lifecycle rows
// without a lifetime; they do not make the accounting incomplete.
func TestJourneyAccountingIgnoresLifecycleRowsWithoutLifetime(t *testing.T) {
	policy := JourneyPolicyDefinition{Version: analysisPolicyVersion, Journeys: []JourneyDefinition{
		{ID: "login", Name: "Login", Start: JourneyBoundary{Action: "open_login"}, Success: JourneyBoundary{Action: "submit_login"}, Weight: 1},
	}}
	start := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	rows := []TransactionEvent{
		{Timestamp: start, Group: "web", VUserID: -1, Phase: "init", ActionName: "setup", Success: true},
		{Timestamp: start.Add(time.Second), Group: "web", VUserID: 3, Iteration: 1, VUserLifetime: 9, Phase: "action", ActionName: "open_login", Success: true},
		{Timestamp: start.Add(2 * time.Second), Group: "web", VUserID: 3, Iteration: 1, VUserLifetime: 9, Phase: "action", ActionName: "submit_login", Success: true},
		{Timestamp: start.Add(3 * time.Second), Group: "web", VUserID: -1, Phase: "teardown", ActionName: "cleanup", Success: true},
	}
	report := accountJourneyRows(policy, rows)
	if report.Incomplete || report.WeightedFailureIndex == nil {
		t.Fatalf("lifecycle rows made the accounting incomplete: %+v", report)
	}
	// An older generator's action rows (no lifetime) beside them still do.
	rows = append(rows, TransactionEvent{Timestamp: start.Add(4 * time.Second), Group: "web", VUserID: 0, Phase: "action", ActionName: "open_login", Success: true})
	if report := accountJourneyRows(policy, rows); !report.Incomplete || !slices.Contains(report.IncompleteReasons, "rows_without_vuser_identity") {
		t.Fatalf("older generator rows: %+v", report)
	}
}

// A transaction's interval is its wall-clock span: think time inside it is
// excluded from its duration, not from the attempt.
func TestJourneyAccountingUsesTheTransactionsWallSpan(t *testing.T) {
	policy := JourneyPolicyDefinition{Version: analysisPolicyVersion, Journeys: []JourneyDefinition{
		{ID: "browse", Name: "Browse", Start: JourneyBoundary{Transaction: "Browse"}, Success: JourneyBoundary{Transaction: "Browse"}, Weight: 1},
	}}
	start := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	rows := []TransactionEvent{
		{Timestamp: start, Group: "web", VUserID: 1, Iteration: 1, ActionName: "browse_a", Success: true, DurationMs: 10},
		// three seconds of think time
		{Timestamp: start.Add(3010 * time.Millisecond), Group: "web", VUserID: 1, Iteration: 1, ActionName: "browse_b", Success: false, DurationMs: 10},
		{Timestamp: start, Group: "web", VUserID: 1, Iteration: 1, ActionName: "Browse", BusinessTransaction: true, Success: false, DurationMs: 20, ElapsedMs: 3020},
		{Timestamp: start.Add(5 * time.Minute), Group: "web", VUserID: 1, Iteration: 2, ActionName: "idle", Success: true, DurationMs: 10},
	}
	browse := journeyByID(t, accountJourneyRows(policy, rows), "browse")
	if browse.Failed != 1 || browse.Steps != 2 || browse.StepFailures != 1 || len(browse.Examples) != 1 || browse.Examples[0].FailedStep != "browse_b" {
		t.Fatalf("browse %+v", browse)
	}
}

// Rows without VUser identity beside rows with it (an older generator in a
// mixed fleet) leave the accounting incomplete; a run that recorded identity
// but ran no iteration is not a legacy archive.
func TestJourneyAccountingDisclosesMissingIdentity(t *testing.T) {
	policy := journeyFixturePolicy(false)
	rows := journeyFixtureRows()
	for i := range rows {
		rows[i].VUserLifetime = uint64(10 + rows[i].VUserID)
	}
	old := TransactionEvent{Timestamp: rows[0].Timestamp, Group: "web", ActionName: "browse_a", Success: true, DurationMs: 10, Phase: "action"}
	report := accountJourneyRows(policy, append(rows, old))
	if !report.Incomplete || !slices.Contains(report.IncompleteReasons, "rows_without_vuser_identity") || report.WeightedFailureIndex != nil {
		t.Fatalf("mixed identity %+v", report)
	}
	server, store := baselineTestServer(t)
	pin := saveJourneyPolicy(t, store, "journeys", 1, policy)
	journeyArchive(t, server, store, "no-iterations", []TransactionEvent{{Timestamp: rows[0].Timestamp, Group: "web", VUserID: 1, VUserLifetime: 7, ActionName: "init", Success: true}}, &pin)
	if code, response, body := getJourneys(t, server, "no-iterations", ""); code != http.StatusOK || response.Availability != "unavailable" || response.Reason != "no_iterations" {
		t.Fatalf("no iterations: %d %s", code, body)
	}
}

// An archive whose generator sources were not all collected is incomplete.
func TestJourneyAccountingDisclosesUncollectedSources(t *testing.T) {
	server, store := baselineTestServer(t)
	journeyArchive(t, server, store, "partial", journeyFixtureRows(), nil)
	record := server.runner.archives["partial"]
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	manifest.SourceCoverage = &RunArchiveSourceCoverage{Mode: "best_effort", Sources: []RunArchiveSource{{Target: "gen-a", Collected: true}, {Target: "gen-b", Collected: false}}}
	report, err := accountJourneys(context.Background(), record, manifest, journeyFixturePolicy(false), JourneyPolicyRef{ID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Incomplete || !slices.Contains(report.IncompleteReasons, "sources_not_collected") || report.WeightedFailureIndex != nil {
		t.Fatalf("uncollected %+v", report)
	}
}

// An archive with only a group's once-only init/teardown rows ran no
// iteration: no_iterations, not an archive without identity.
func TestJourneyAccountingLifecycleOnlyArchiveRanNoIteration(t *testing.T) {
	policy := JourneyPolicyDefinition{Version: analysisPolicyVersion, Journeys: []JourneyDefinition{
		{ID: "login", Name: "Login", Start: JourneyBoundary{Action: "open_login"}, Success: JourneyBoundary{Action: "submit_login"}, Weight: 1},
	}}
	start := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	reason := func(rows []TransactionEvent) string {
		acc := newJourneyAccumulator(policy, &JourneyRiskReport{})
		for i := range rows {
			acc.add(&rows[i])
		}
		if acc.identity {
			t.Fatalf("rows %+v have iteration identity", rows)
		}
		return acc.unavailableReason()
	}
	if got := reason([]TransactionEvent{{Timestamp: start, Group: "web", VUserID: -1, Phase: "init", ActionName: "setup", Success: false}}); got != "no_iterations" {
		t.Fatalf("lifecycle-only archive: %s", got)
	}
	if got := reason(nil); got != "no_iterations" {
		t.Fatalf("empty archive: %s", got)
	}
	if got := reason([]TransactionEvent{{Timestamp: start, Group: "web", Phase: "action", ActionName: "open_login", Success: true}}); got != "archive_lacks_vuser_iteration_identity" {
		t.Fatalf("legacy archive: %s", got)
	}
}
