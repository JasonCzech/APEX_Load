package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/storage"
)

type directImportRollbackTestStore struct {
	*storage.SQLiteStore

	renewStarted  chan struct{}
	renewContinue chan struct{}
	renewOnce     sync.Once

	mu               sync.Mutex
	failNextClaimCAS bool
	failScriptDelete bool
}

func (s *directImportRollbackTestStore) RenewDirectImportReceipt(
	id, ownerToken string,
	now time.Time,
	lease time.Duration,
) (bool, error) {
	if s.renewStarted != nil {
		s.renewOnce.Do(func() { close(s.renewStarted) })
		<-s.renewContinue
	}
	return s.SQLiteStore.RenewDirectImportReceipt(id, ownerToken, now, lease)
}

func (s *directImportRollbackTestStore) CompareAndSwapSetting(
	key, expected, value string,
) (bool, error) {
	s.mu.Lock()
	if key == libraryImportClaimsSettingKey && s.failNextClaimCAS {
		s.failNextClaimCAS = false
		s.mu.Unlock()
		return false, errors.New("injected durable claim release failure")
	}
	s.mu.Unlock()
	return s.SQLiteStore.CompareAndSwapSetting(key, expected, value)
}

func (s *directImportRollbackTestStore) failClaimReleaseOnce() {
	s.mu.Lock()
	s.failNextClaimCAS = true
	s.mu.Unlock()
}

func (s *directImportRollbackTestStore) DeleteScriptIfUnchanged(
	id string,
	expected time.Time,
) (bool, error) {
	s.mu.Lock()
	fail := s.failScriptDelete
	s.mu.Unlock()
	if fail {
		return false, errors.New("injected asset compensation failure")
	}
	return s.SQLiteStore.DeleteScriptIfUnchanged(id, expected)
}

func (s *directImportRollbackTestStore) setFailScriptDelete(fail bool) {
	s.mu.Lock()
	s.failScriptDelete = fail
	s.mu.Unlock()
}

type failOnceDirectImportReceiptStore struct {
	*storage.SQLiteStore
	claims          int
	failComplete    bool
	releaseStarted  chan struct{}
	releaseContinue chan struct{}
	releaseOnce     sync.Once
}

func (s *failOnceDirectImportReceiptStore) ClaimDirectImportReceipt(
	projectID, sourceProfile, operationKeyHash, requestSHA256, ownerToken string,
	now time.Time,
	lease time.Duration,
) (*storage.DirectImportReceiptClaim, error) {
	s.claims++
	if s.claims > 1 {
		now = now.Add(3 * directImportReceiptLease())
	}
	return s.SQLiteStore.ClaimDirectImportReceipt(
		projectID, sourceProfile, operationKeyHash, requestSHA256, ownerToken,
		now, lease,
	)
}

func (s *failOnceDirectImportReceiptStore) CompleteDirectImportReceipt(
	id, ownerToken string,
	scriptIDs []string,
	scenarioID string,
	now time.Time,
) (bool, error) {
	if s.failComplete {
		s.failComplete = false
		return false, errors.New("injected lost receipt completion")
	}
	return s.SQLiteStore.CompleteDirectImportReceipt(
		id, ownerToken, scriptIDs, scenarioID, now,
	)
}

func (s *failOnceDirectImportReceiptStore) ReleaseDirectImportReceipt(
	id, ownerToken string,
	now time.Time,
) (bool, error) {
	if s.releaseStarted != nil {
		s.releaseOnce.Do(func() { close(s.releaseStarted) })
		<-s.releaseContinue
	}
	return s.SQLiteStore.ReleaseDirectImportReceipt(id, ownerToken, now)
}

func TestJMeterSaveLostResponseRetryReturnsSameDurableAssets(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()

	const (
		operationID = "jmeter-preview-operation-6dcd59de"
		canary      = "jmeter-idempotency-password-canary"
	)
	attestedBody := func(credential, target string) []byte {
		t.Helper()
		source := &dsl.Script{
			Name:          "Idempotent_JMeter",
			Version:       "1.0.0",
			Protocol:      "http",
			ProjectID:     storage.DefaultProjectID,
			GeneratedBy:   "jmeter-import",
			GeneratedFrom: "jmeter",
			Config:        dsl.ScriptConfig{BaseURL: "https://api.example.test"},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
				Source: "jmeter",
				Preserved: map[string]any{
					jmeterDirectImportOperationKey: operationID,
				},
			}},
			Actions: []dsl.Action{{
				Name: "Request", Type: "request", Method: http.MethodGet, Target: target,
				Headers: map[string]string{"Authorization": "Bearer " + credential},
			}},
		}
		preview := server.newScriptImportResponseWithSourceSecrets(
			httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter", nil),
			scriptImportResponseOptions{Script: source, Scripts: []*dsl.Script{source}},
		)
		maskedScripts, ok := preview["scripts"].([]any)
		if !ok || len(maskedScripts) != 1 {
			t.Fatalf("JMeter preview scripts = %#v", preview["scripts"])
		}
		maskedJSON, marshalErr := json.Marshal(maskedScripts[0])
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if !bytes.Contains(maskedJSON, []byte(credential)) ||
			bytes.Contains(maskedJSON, []byte(maskedScriptSecret)) {
			t.Fatalf("JMeter idempotency preview did not preserve credential: %s", maskedJSON)
		}
		payload := map[string]any{
			"scripts": maskedScripts,
			"scenario_draft": map[string]any{
				"name": "Idempotent JMeter Scenario", "duration": "2m",
				"scenario_mode": "vuser_driven",
				"groups": []map[string]any{{
					"name": "Users", "script_name": "Idempotent_JMeter",
					"vusers": 3, "initialization": "at_runtime",
				}},
			},
		}
		body, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return body
	}
	body := attestedBody(canary, "/one")
	first := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first save status = %d body=%s", first.Code, first.Body.String())
	}
	second := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
	if second.Code != http.StatusCreated {
		t.Fatalf("lost-response retry status = %d body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(first.Body.String(), canary) || !strings.Contains(second.Body.String(), canary) {
		t.Fatal("JMeter idempotency response did not preserve imported credentials")
	}

	var firstResult, secondResult struct {
		Scripts  []StoredScript  `json:"scripts"`
		Scenario *StoredScenario `json:"scenario"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatal(err)
	}
	if len(firstResult.Scripts) != 1 || len(secondResult.Scripts) != 1 ||
		firstResult.Scripts[0].ID != secondResult.Scripts[0].ID ||
		firstResult.Scenario == nil || secondResult.Scenario == nil ||
		firstResult.Scenario.ID != secondResult.Scenario.ID {
		t.Fatalf("retry identities changed: first=%+v second=%+v", firstResult, secondResult)
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("durable script count = %d, want 1", got)
	}
	if got := len(server.runner.ListScenarios()); got != 1 {
		t.Fatalf("durable scenario count = %d, want 1", got)
	}
	stored, ok := server.runner.GetScript(firstResult.Scripts[0].ID)
	if !ok || stored.Script.Actions[0].Headers["Authorization"] != "Bearer "+canary {
		t.Fatalf("destination script did not retain imported credential: %#v", stored)
	}

	changedCredential := attestedBody("different-jmeter-idempotency-password", "/one")
	credentialConflict := postJSON(
		t,
		server,
		"/api/v1/scripts/import/jmeter/save",
		changedCredential,
		"",
	)
	if credentialConflict.Code != http.StatusConflict {
		t.Fatalf(
			"changed credential with same operation id status = %d, want 409 body=%s",
			credentialConflict.Code,
			credentialConflict.Body.String(),
		)
	}
	if strings.Contains(
		credentialConflict.Body.String(),
		"different-jmeter-idempotency-password",
	) {
		t.Fatal("JMeter credential-conflict response disclosed the changed credential")
	}
	stored, ok = server.runner.GetScript(firstResult.Scripts[0].ID)
	if !ok || stored.Script.Actions[0].Headers["Authorization"] != "Bearer "+canary {
		t.Fatalf("credential conflict changed the first destination script: %#v", stored)
	}

	changed := attestedBody(canary, "/two")
	conflict := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", changed, "")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed request with same operation id status = %d, want 409 body=%s",
			conflict.Code, conflict.Body.String())
	}
}

func TestDirectImportRollbackStopsClaimHeartbeatAfterReceiptOwnershipMoves(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	attempt, replay, err := server.claimDirectImportAttempt(storage.DefaultProjectID, "jmeter", "rollback-owner-move", directImportSHA256("request"))
	if err != nil {
		t.Fatalf("claim receipt: %v", err)
	}
	if replay || attempt == nil {
		t.Fatalf("claim = %#v replay=%v", attempt, replay)
	}
	tx, err := newCreateImportTransaction(server, "jmeter", []*dsl.Script{{
		ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Name: "claim heartbeat", ProjectID: storage.DefaultProjectID, Protocol: "http",
	}}, "", nil)
	if err != nil {
		t.Fatalf("create import transaction: %v", err)
	}
	done := tx.claims.done
	attempt.ownerToken = "different-owner-token"
	if err := attempt.rollback(tx); err == nil || !strings.Contains(err.Error(), "ownership moved") {
		t.Fatalf("rollback error = %v, want moved-ownership error", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("library-claim heartbeat remained active after fenced compensation was skipped")
	}
	if !tx.closed || tx.claims != nil {
		t.Fatalf("transaction lifecycle = closed:%v claims:%#v", tx.closed, tx.claims)
	}
}

func TestDirectImportRollbackKeepsClaimsLeasedUntilReceiptRenewal(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "5m")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "rollback-ordering.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &directImportRollbackTestStore{
		SQLiteStore:   base,
		renewStarted:  make(chan struct{}),
		renewContinue: make(chan struct{}),
	}
	server := NewServer(ServerConfig{Store: store})
	attempt, replay, err := server.claimDirectImportAttempt(
		storage.DefaultProjectID,
		"jmeter",
		"rollback-ordering",
		directImportSHA256("request"),
	)
	if err != nil || replay || attempt == nil {
		t.Fatalf("claim receipt = %#v replay=%v err=%v", attempt, replay, err)
	}
	script := libraryTestScript(
		"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"rollback ordering",
		storage.DefaultProjectID,
		"/rollback-ordering",
	)
	transaction, err := newCreateImportTransaction(
		server,
		"jmeter",
		[]*dsl.Script{script},
		"",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := transaction.createScript(script)
	if err != nil {
		t.Fatal(err)
	}

	rollbackDone := make(chan error, 1)
	go func() { rollbackDone <- attempt.rollback(transaction) }()
	releasedRenewal := false
	defer func() {
		if !releasedRenewal {
			close(store.renewContinue)
		}
	}()
	select {
	case <-store.renewStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("rollback did not reach the receipt-renewal barrier")
	}

	claims, err := server.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || !claims[0].ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("claims expired before receipt ownership was fenced: %#v", claims)
	}
	replacement := NewServer(ServerConfig{Store: base})
	if err := replacement.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatalf("concurrent recovery while renewal was blocked: %v", err)
	}
	if _, err := base.GetScript(stored.ID); err != nil {
		t.Fatalf("concurrent recovery compensated a still-fenced asset: %v", err)
	}

	close(store.renewContinue)
	releasedRenewal = true
	select {
	case err := <-rollbackDone:
		if err != nil {
			t.Fatalf("rollback after receipt renewal: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rollback did not finish after receipt renewal continued")
	}
	if _, err := base.GetScript(stored.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("owned rollback left the imported script behind: %v", err)
	}
	claims, err = server.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("owned rollback left durable claims behind: %#v", claims)
	}
}

func TestDirectImportRollbackAbandonsReceiptAfterClaimReleaseError(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "5m")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "rollback-abandon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &directImportRollbackTestStore{SQLiteStore: base}
	server := NewServer(ServerConfig{Store: store})
	attempt, replay, err := server.claimDirectImportAttempt(
		storage.DefaultProjectID,
		"jmeter",
		"rollback-abandon-after-compensation",
		directImportSHA256("request"),
	)
	if err != nil || replay || attempt == nil {
		t.Fatalf("claim receipt = %#v replay=%v err=%v", attempt, replay, err)
	}
	script := libraryTestScript(
		"cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		"rollback abandonment",
		storage.DefaultProjectID,
		"/rollback-abandonment",
	)
	transaction, err := newCreateImportTransaction(
		server,
		"jmeter",
		[]*dsl.Script{script},
		"",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := transaction.createScript(script)
	if err != nil {
		t.Fatal(err)
	}
	transaction.stopClaimHeartbeat()
	store.failClaimReleaseOnce()
	err = attempt.rollback(transaction)
	if err == nil || !strings.Contains(err.Error(), "injected durable claim release failure") {
		t.Fatalf("rollback error = %v, want injected claim-release failure", err)
	}
	if _, err := base.GetScript(stored.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("asset compensation did not finish before claim-release failure: %v", err)
	}

	retry, err := base.ClaimDirectImportReceipt(
		attempt.receipt.ProjectID,
		attempt.receipt.SourceProfile,
		attempt.receipt.OperationKeyHash,
		attempt.receipt.RequestSHA256,
		"replacement-owner",
		time.Now().UTC(),
		directImportReceiptLease(),
	)
	if err != nil {
		t.Fatalf("receipt remained busy after compensated rollback: %v", err)
	}
	if retry == nil || !retry.Created {
		t.Fatalf("replacement receipt = %#v, want a newly created claim", retry)
	}
	if abandoned, err := base.AbandonDirectImportReceipt(retry.Receipt.ID, "replacement-owner"); err != nil || !abandoned {
		t.Fatalf("clean up replacement receipt = %t, %v", abandoned, err)
	}
}

func TestDirectImportRollbackRetainsReceiptAfterAssetCompensationFailure(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "5m")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "rollback-asset-failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &directImportRollbackTestStore{SQLiteStore: base}
	server := NewServer(ServerConfig{Store: store})
	attempt, replay, err := server.claimDirectImportAttempt(
		storage.DefaultProjectID,
		"jmeter",
		"rollback-asset-compensation-failure",
		directImportSHA256("request"),
	)
	if err != nil || replay || attempt == nil || !attempt.created {
		t.Fatalf("claim receipt = %#v replay=%v err=%v", attempt, replay, err)
	}
	script := libraryTestScript(
		deterministicDirectImportAssetID(attempt.receipt.ID, "script", 0),
		"rollback asset failure",
		storage.DefaultProjectID,
		"/rollback-asset-failure",
	)
	transaction, err := newCreateImportTransaction(
		server,
		"jmeter",
		[]*dsl.Script{script},
		"",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := transaction.createScript(script)
	if err != nil {
		t.Fatal(err)
	}
	store.setFailScriptDelete(true)
	err = attempt.rollback(transaction)
	if !errors.Is(err, errCreateImportAssetCompensation) ||
		!strings.Contains(err.Error(), "injected asset compensation failure") {
		t.Fatalf("rollback error = %v, want asset-compensation failure", err)
	}
	if _, err := base.GetScript(stored.ID); err != nil {
		t.Fatalf("failed compensation unexpectedly removed the asset: %v", err)
	}

	retry, err := base.ClaimDirectImportReceipt(
		attempt.receipt.ProjectID,
		attempt.receipt.SourceProfile,
		attempt.receipt.OperationKeyHash,
		attempt.receipt.RequestSHA256,
		"replacement-owner",
		time.Now().UTC(),
		directImportReceiptLease(),
	)
	if err != nil {
		t.Fatalf("reclaim retained receipt: %v", err)
	}
	if retry == nil || retry.Created || retry.Receipt.ID != attempt.receipt.ID {
		t.Fatalf("replacement receipt = %#v, want retained id %s", retry, attempt.receipt.ID)
	}

	store.setFailScriptDelete(false)
	replacement := NewServer(ServerConfig{Store: store})
	if err := replacement.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatalf("recover retained failed compensation: %v", err)
	}
	if _, err := base.GetScript(stored.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("recovery left the failed-compensation asset behind: %v", err)
	}
	claims, err := replacement.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("recovered failed-compensation claims remain: %#v", claims)
	}
	if abandoned, err := base.AbandonDirectImportReceipt(retry.Receipt.ID, "replacement-owner"); err != nil || !abandoned {
		t.Fatalf("clean up retained receipt = %t, %v", abandoned, err)
	}
}

func TestStaleDirectImportAdoptionWaitsForUncommittedClaims(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "5m")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "stale-adoption-claims.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &failOnceDirectImportReceiptStore{
		SQLiteStore:     base,
		releaseStarted:  make(chan struct{}),
		releaseContinue: make(chan struct{}),
	}
	first := NewServer(ServerConfig{Store: store})
	const (
		operationID   = "stale-adoption-live-claims"
		requestDigest = "stale-adoption-request-digest"
	)
	firstAttempt, replay, err := first.claimDirectImportAttempt(
		storage.DefaultProjectID,
		"jmeter",
		operationID,
		requestDigest,
	)
	if err != nil || replay || firstAttempt == nil || !firstAttempt.created {
		t.Fatalf("first receipt = %#v replay=%v err=%v", firstAttempt, replay, err)
	}
	firstScript := libraryTestScript(
		deterministicDirectImportAssetID(firstAttempt.receipt.ID, "script", 0),
		"stalled direct import",
		storage.DefaultProjectID,
		"/stalled",
	)
	firstTransaction, err := newCreateImportTransaction(
		first,
		"jmeter",
		[]*dsl.Script{firstScript},
		"",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstStored, err := firstTransaction.createScript(firstScript)
	if err != nil {
		t.Fatal(err)
	}

	// The first owner is stalled after its final create. The receipt clock is
	// advanced by the test store so a retry can take it over without waiting,
	// while the first owner's claim heartbeat remains live in real time.
	second := NewServer(ServerConfig{Store: store})
	secondAttempt, replay, err := second.claimDirectImportAttempt(
		storage.DefaultProjectID,
		"jmeter",
		operationID,
		requestDigest,
	)
	if err != nil || replay || secondAttempt == nil || secondAttempt.created {
		t.Fatalf("stale takeover receipt = %#v replay=%v err=%v", secondAttempt, replay, err)
	}
	if err := second.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatalf("recover before stale adoption: %v", err)
	}
	if err := secondAttempt.renew(); err != nil {
		t.Fatalf("renew stale takeover: %v", err)
	}
	fenceDone := make(chan error, 1)
	go func() {
		fenceDone <- second.fenceDirectImportAssetAdoption(
			secondAttempt,
			storage.DefaultProjectID,
			[]string{firstStored.ID},
			"",
		)
	}()
	releasedFence := false
	defer func() {
		if !releasedFence {
			close(store.releaseContinue)
		}
	}()
	select {
	case <-store.releaseStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("stale adoption did not reach the live-claim release barrier")
	}
	// The old transaction commits after the takeover has observed its claim but
	// before that takeover relinquishes receipt ownership.
	if err := firstTransaction.commit(); err != nil {
		t.Fatalf("old transaction commit at adoption barrier: %v", err)
	}
	close(store.releaseContinue)
	releasedFence = true
	select {
	case err = <-fenceDone:
		if !errors.Is(err, errDirectImportAssetsClaimed) {
			t.Fatalf("stale adoption fence error = %v, want active-claims error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stale adoption fence did not finish after receipt release continued")
	}
	if _, err := base.GetScript(firstStored.ID); err != nil {
		t.Fatalf("blocked stale adoption changed the old owner's asset: %v", err)
	}
	if completed, err := base.CompleteDirectImportReceipt(
		secondAttempt.receipt.ID,
		secondAttempt.ownerToken,
		[]string{firstStored.ID},
		"",
		time.Now().UTC(),
	); err != nil || completed {
		t.Fatalf("blocked takeover completed its released receipt = %t, %v", completed, err)
	}

	// The released receipt row must survive that race so the next retry adopts
	// these exact IDs instead of creating duplicates.
	claims, err := second.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("terminal old-owner claims remain: %#v", claims)
	}

	third := NewServer(ServerConfig{Store: store})
	thirdAttempt, replay, err := third.claimDirectImportAttempt(
		storage.DefaultProjectID,
		"jmeter",
		operationID,
		requestDigest,
	)
	if err != nil || replay || thirdAttempt == nil || thirdAttempt.created {
		t.Fatalf("post-recovery receipt = %#v replay=%v err=%v", thirdAttempt, replay, err)
	}
	if thirdAttempt.receipt.ID != firstAttempt.receipt.ID {
		t.Fatalf("released receipt id = %s, want retained id %s", thirdAttempt.receipt.ID, firstAttempt.receipt.ID)
	}
	if err := third.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatalf("clean terminal claims before adoption: %v", err)
	}
	if err := thirdAttempt.renew(); err != nil {
		t.Fatalf("renew retained receipt: %v", err)
	}
	if err := third.fenceDirectImportAssetAdoption(
		thirdAttempt,
		storage.DefaultProjectID,
		[]string{firstStored.ID},
		"",
	); err != nil {
		t.Fatalf("terminal claims still blocked adoption: %v", err)
	}
	adopted, scenario, err := third.loadDirectImportAssetsByIDs(
		storage.DefaultProjectID,
		[]string{firstStored.ID},
		"",
	)
	if err != nil || scenario != nil || !directImportPreparedAssetsMatch(
		third,
		adopted,
		[]*dsl.Script{firstScript},
		nil,
		nil,
	) {
		t.Fatalf("retained receipt could not adopt exact assets: scripts=%#v scenario=%#v err=%v", adopted, scenario, err)
	}
	if err := thirdAttempt.complete(adopted, nil); err != nil {
		t.Fatalf("complete retained receipt adoption: %v", err)
	}
	if err := third.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatalf("late recovery after completed receipt: %v", err)
	}
	if _, err := base.GetScript(firstStored.ID); err != nil {
		t.Fatalf("late recovery deleted the completed retry asset: %v", err)
	}
	replayed, replay, err := third.claimDirectImportAttempt(
		storage.DefaultProjectID,
		"jmeter",
		operationID,
		requestDigest,
	)
	if err != nil || !replay || replayed == nil || replayed.receipt.ID != firstAttempt.receipt.ID {
		t.Fatalf("completed retained receipt replay = %#v replay=%v err=%v", replayed, replay, err)
	}
}

func TestLostUnmutatedClaimsCannotDeleteCompletedDirectImportAsset(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_IMPORT_CLAIM_TTL", "5m")
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "unmutated-claim-race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &failOnceDirectImportReceiptStore{SQLiteStore: base}
	first := NewServer(ServerConfig{Store: store})
	const (
		operationID   = "unmutated-claim-completed-asset"
		requestDigest = "unmutated-claim-request-digest"
	)
	firstAttempt, replay, err := first.claimDirectImportAttempt(
		storage.DefaultProjectID,
		"jmeter",
		operationID,
		requestDigest,
	)
	if err != nil || replay || firstAttempt == nil || !firstAttempt.created {
		t.Fatalf("first receipt = %#v replay=%v err=%v", firstAttempt, replay, err)
	}
	script := libraryTestScript(
		deterministicDirectImportAssetID(firstAttempt.receipt.ID, "script", 0),
		"completed claim-race asset",
		storage.DefaultProjectID,
		"/completed-claim-race",
	)
	committedTransaction, err := newCreateImportTransaction(
		first,
		"jmeter",
		[]*dsl.Script{script},
		"",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := committedTransaction.createScript(script)
	if err != nil {
		t.Fatal(err)
	}
	if err := committedTransaction.commit(); err != nil {
		t.Fatal(err)
	}

	second := NewServer(ServerConfig{Store: store})
	secondAttempt, replay, err := second.claimDirectImportAttempt(
		storage.DefaultProjectID,
		"jmeter",
		operationID,
		requestDigest,
	)
	if err != nil || replay || secondAttempt == nil || secondAttempt.created {
		t.Fatalf("stale takeover receipt = %#v replay=%v err=%v", secondAttempt, replay, err)
	}
	if err := second.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatal(err)
	}
	if err := secondAttempt.renew(); err != nil {
		t.Fatal(err)
	}
	if err := second.fenceDirectImportAssetAdoption(
		secondAttempt,
		storage.DefaultProjectID,
		[]string{stored.ID},
		"",
	); err != nil {
		t.Fatalf("unexpected pre-race adoption fence: %v", err)
	}

	// A losing request acquires matching claims after the takeover's check but
	// before receipt completion. It has not started any asset write.
	losingTransaction, err := newCreateImportTransaction(
		first,
		"jmeter",
		[]*dsl.Script{script},
		"",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if losingTransaction.mutationStarted {
		t.Fatal("new transaction unexpectedly started an asset mutation")
	}
	if err := secondAttempt.complete([]*StoredScript{stored}, nil); err != nil {
		t.Fatalf("complete takeover receipt: %v", err)
	}
	if err := firstAttempt.rollback(losingTransaction); err == nil ||
		!strings.Contains(err.Error(), "ownership moved") {
		t.Fatalf("losing no-op rollback error = %v, want ownership moved", err)
	}
	if err := second.recoverExpiredLibraryImportClaims(); err != nil {
		t.Fatalf("recover after losing no-op claims: %v", err)
	}
	if _, err := base.GetScript(stored.ID); err != nil {
		t.Fatalf("no-op claim recovery deleted a completed receipt asset: %v", err)
	}
	claims, err := second.loadLibraryImportClaims()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 0 {
		t.Fatalf("terminal no-op claims remain: %#v", claims)
	}
	replayed, replay, err := second.claimDirectImportAttempt(
		storage.DefaultProjectID,
		"jmeter",
		operationID,
		requestDigest,
	)
	if err != nil || !replay || replayed == nil || replayed.receipt.ID != firstAttempt.receipt.ID {
		t.Fatalf("completed receipt replay = %#v replay=%v err=%v", replayed, replay, err)
	}
}

func TestJMeterLegacyNoKeyStillCreatesIntentionalDuplicates(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	body := []byte(`{"scripts":[{
		"name":"Legacy_Duplicate","version":"1","protocol":"http",
		"config":{"base_url":"https://example.test"},
		"actions":[{"name":"Request","type":"request","method":"GET","target":"/"}]
	}]}`)
	first := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
	second := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("legacy saves = %d/%d, want 201/201; bodies=%s / %s",
			first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 2 {
		t.Fatalf("legacy no-key duplicate count = %d, want 2", got)
	}
}

func TestJMeterExplicitIdempotencyKeyReplaysThroughAuthenticatedMiddleware(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	enableTrustedHeaderAuth(t, server)

	body := []byte(`{"scripts":[{
		"name":"Explicit_Key","version":"1","protocol":"http",
		"config":{"base_url":"https://example.test"},
		"actions":[{"name":"Request","type":"request","method":"GET","target":"/"}]
	}]}`)
	save := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/scripts/import/jmeter/save",
			bytes.NewReader(body),
		)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "jmeter-explicit-retry-key")
		req.Header.Set("X-Auth-Request-User", "direct-import-editor")
		req.Header.Set("X-APEXLoad-Roles", RoleSystemAdmin)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}
	first, second := save(), save()
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf(
			"explicit-key saves = %d/%d, want 201/201; bodies=%s / %s",
			first.Code,
			second.Code,
			first.Body.String(),
			second.Body.String(),
		)
	}
	var firstResult, secondResult struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatal(err)
	}
	if len(firstResult.Scripts) != 1 || len(secondResult.Scripts) != 1 ||
		firstResult.Scripts[0].ID != secondResult.Scripts[0].ID {
		t.Fatalf(
			"authenticated idempotent retry returned different assets: %#v / %#v",
			firstResult,
			secondResult,
		)
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("authenticated idempotent retry stored %d scripts, want 1", got)
	}
}

func TestJMeterRetryHealsAssetsCommittedBeforeReceiptCompletion(t *testing.T) {
	base, err := storage.NewSQLiteStore(t.TempDir() + "/receipts.db")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer base.Close()
	store := &failOnceDirectImportReceiptStore{
		SQLiteStore: base, failComplete: true,
	}
	server := NewServer(ServerConfig{Store: store})
	body := []byte(`{"scripts":[{
		"name":"Crash_Recovery","version":"1","protocol":"http",
		"generated_by":"jmeter-import","generated_from":"jmeter",
		"metadata":{"import":{"source":"jmeter","preserved":{
			"direct_import_operation_id_v1":"crash-recovery-operation-729c"
		}}},
		"config":{"base_url":"https://example.test"},
		"actions":[{"name":"Request","type":"request","method":"GET","target":"/"}]
	}]}`)
	first := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("injected completion failure status = %d, want 500 body=%s",
			first.Code, first.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("asset commit before receipt failure stored %d scripts, want 1", got)
	}
	second := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", body, "")
	if second.Code != http.StatusCreated {
		t.Fatalf("stale receipt healing status = %d, want 201 body=%s",
			second.Code, second.Body.String())
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("stale receipt healing stored %d scripts, want 1", got)
	}
}

func TestJMeterRetryHealsCredentialedAssetsAcrossServerRestart(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "jmeter-restart.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer base.Close()
	store := &failOnceDirectImportReceiptStore{
		SQLiteStore:  base,
		failComplete: true,
	}
	firstServer := NewServer(ServerConfig{Store: store})
	const credential = "jmeter-restart-exact-password"
	body := jmeterRestartAttestedSaveBody(
		t,
		firstServer,
		"jmeter-restart-operation-60c92e9f",
		credential,
	)

	first := postJSON(t, firstServer, "/api/v1/scripts/import/jmeter/save", body, "")
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("injected completion failure status = %d, want 500 body=%s",
			first.Code, first.Body.String())
	}
	firstScripts := firstServer.runner.ListScripts()
	if len(firstScripts) != 1 || len(firstServer.runner.ListScenarios()) != 1 {
		t.Fatalf("first process durable assets = %d scripts/%d scenarios, want 1/1",
			len(firstScripts), len(firstServer.runner.ListScenarios()))
	}
	committedID := firstScripts[0].ID
	committedScenarioID := firstServer.runner.ListScenarios()[0].ID

	// The preview capability lives only in the first process. Recovery must use
	// the stale durable receipt and its deterministic assets, not require that
	// ephemeral token to survive controller restart.
	restarted := NewServer(ServerConfig{Store: store})
	retry := postJSON(t, restarted, "/api/v1/scripts/import/jmeter/save", body, "")
	if retry.Code != http.StatusCreated {
		t.Fatalf("restart recovery status = %d, want 201 body=%s",
			retry.Code, retry.Body.String())
	}
	if !strings.Contains(retry.Body.String(), credential) {
		t.Fatal("restart recovery response did not preserve the imported credential")
	}
	var recovered struct {
		Scripts  []StoredScript  `json:"scripts"`
		Scenario *StoredScenario `json:"scenario"`
	}
	if err := json.Unmarshal(retry.Body.Bytes(), &recovered); err != nil {
		t.Fatal(err)
	}
	if len(recovered.Scripts) != 1 || recovered.Scripts[0].ID != committedID ||
		recovered.Scenario == nil || recovered.Scenario.ID != committedScenarioID {
		t.Fatalf("restart recovery changed deterministic assets: %#v", recovered)
	}
	if got := len(restarted.runner.ListScripts()); got != 1 {
		t.Fatalf("restart recovery stored %d scripts, want 1", got)
	}
	if got := len(restarted.runner.ListScenarios()); got != 1 {
		t.Fatalf("restart recovery stored %d scenarios, want 1", got)
	}
	exact, ok := restarted.runner.GetScript(committedID)
	if !ok || exact == nil || exact.Script == nil ||
		exact.Script.Actions[0].Headers["Authorization"] != "Bearer "+credential {
		t.Fatalf("restart recovery did not retain exact durable credential: %#v", exact)
	}
}

func TestJMeterRestartDoesNotUseLostCapabilityForNewAttempt(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "jmeter-lost-capability.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	previewServer := NewServer(ServerConfig{Store: store})
	body := jmeterRestartAttestedSaveBody(
		t,
		previewServer,
		"jmeter-new-attempt-operation-13c77c28",
		"jmeter-new-attempt-password",
	)

	// No save was attempted by the preview process, so the restarted server must
	// create a fresh receipt. The lost process-local token grants no authority to
	// create either deterministic asset under that new receipt.
	restarted := NewServer(ServerConfig{Store: store})
	response := postJSON(t, restarted, "/api/v1/scripts/import/jmeter/save", body, "")
	if response.Code != http.StatusConflict {
		t.Fatalf("lost-capability new attempt status = %d, want 409 body=%s",
			response.Code, response.Body.String())
	}
	if !strings.Contains(strings.ToLower(response.Body.String()), "invalid or expired") {
		t.Fatalf("lost-capability error lacks capability guidance: %s", response.Body.String())
	}
	if got := len(restarted.runner.ListScripts()); got != 0 {
		t.Fatalf("lost capability created %d scripts, want 0", got)
	}
	if got := len(restarted.runner.ListScenarios()); got != 0 {
		t.Fatalf("lost capability created %d scenarios, want 0", got)
	}
}

func TestJMeterRestartRecoveryRejectsChangedCommittedAsset(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "jmeter-restart-change.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer base.Close()
	store := &failOnceDirectImportReceiptStore{
		SQLiteStore:  base,
		failComplete: true,
	}
	firstServer := NewServer(ServerConfig{Store: store})
	body := jmeterRestartAttestedSaveBody(
		t,
		firstServer,
		"jmeter-restart-change-operation-755f42ab",
		"jmeter-restart-change-password",
	)
	first := postJSON(t, firstServer, "/api/v1/scripts/import/jmeter/save", body, "")
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("injected completion failure status = %d, want 500 body=%s",
			first.Code, first.Body.String())
	}
	committed := firstServer.runner.ListScripts()
	if len(committed) != 1 || committed[0].Script == nil {
		t.Fatalf("committed scripts = %#v, want one", committed)
	}
	changed := cloneScriptDefinition(committed[0].Script)
	changed.Name = "Operator edit after committed JMeter import"
	actionDigest, digestErr := scriptCorrelationActionGraphDigest(committed[0].Script)
	if digestErr != nil {
		t.Fatalf("fingerprint committed script: %v", digestErr)
	}
	if _, updated, updateErr := firstServer.runner.UpdateScriptDurableInProjectIfUnchanged(
		committed[0].ID,
		changed,
		storage.DefaultProjectID,
		committed[0].Script.UpdatedAt,
		actionDigest,
	); updateErr != nil || !updated {
		t.Fatalf("edit committed script: updated=%v err=%v", updated, updateErr)
	}

	restarted := NewServer(ServerConfig{Store: store})
	retry := postJSON(t, restarted, "/api/v1/scripts/import/jmeter/save", body, "")
	if retry.Code != http.StatusConflict {
		t.Fatalf("changed-asset restart recovery status = %d, want 409 body=%s",
			retry.Code, retry.Body.String())
	}
	if !strings.Contains(strings.ToLower(retry.Body.String()), "changed before recovery") {
		t.Fatalf("changed-asset recovery error lacks conflict guidance: %s", retry.Body.String())
	}
	current, ok := restarted.runner.GetScript(committed[0].ID)
	if !ok || current == nil || current.Script == nil ||
		current.Script.Name != "Operator edit after committed JMeter import" {
		t.Fatalf("recovery rolled back or replaced the later edit: %#v", current)
	}
	if got := len(restarted.runner.ListScripts()); got != 1 {
		t.Fatalf("changed-asset recovery left %d scripts, want 1", got)
	}
	if got := len(restarted.runner.ListScenarios()); got != 1 {
		t.Fatalf("changed-asset recovery left %d scenarios, want 1", got)
	}
}

func TestBrowserRecordingLostResponseRetryReturnsSameDurableScript(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	stubBrowserRecordingDependencyProbe(t, nil)
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	if err := store.SaveProject(&storage.Project{
		ID: "proj-1", Name: "Browser recorder project",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save browser project: %v", err)
	}
	fake := newFakeBrowserRecorder(recordedBrowserArtifact())
	fake.install(server.browserRecording)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	body := []byte(`{"name":"Idempotent Browser Save","expected_script_count":1}`)
	path := "/api/v1/browser-recorder/sessions/" + id + "/convert"
	first := postJSON(t, server, path, body, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first convert status = %d body=%s", first.Code, first.Body.String())
	}
	second := postJSON(t, server, path, body, "")
	if second.Code != http.StatusCreated {
		t.Fatalf("lost-response retry status = %d body=%s", second.Code, second.Body.String())
	}
	var firstResult, secondResult struct {
		Script StoredScript `json:"script"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatal(err)
	}
	if firstResult.Script.ID == "" || firstResult.Script.ID != secondResult.Script.ID {
		t.Fatalf("browser retry IDs = %q/%q", firstResult.Script.ID, secondResult.Script.ID)
	}
	if got := len(server.runner.ListScripts()); got != 1 {
		t.Fatalf("browser retry durable script count = %d, want 1", got)
	}
}

func TestBrowserRecordingRetryRejectsChangedCapturedCredential(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	stubBrowserRecordingDependencyProbe(t, nil)
	if runtime.GOOS == "linux" {
		t.Setenv("DISPLAY", ":0")
	}
	if err := store.SaveProject(&storage.Project{
		ID: "proj-1", Name: "Browser recorder project",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("save browser project: %v", err)
	}

	const (
		firstCredential  = "browser-import-credential-one"
		secondCredential = "browser-import-credential-two"
	)
	artifact := recordedBrowserArtifact()
	artifact.Steps[0].Masked = false
	artifact.Steps[0].MarkSecret = true
	artifact.Steps[0].Value = firstCredential
	fake := newFakeBrowserRecorder(artifact)
	fake.install(server.browserRecording)
	id := startBrowserRecordingViaAPI(t, server)
	stopBrowserRecordingViaAPI(t, server, id)

	body := []byte(`{"name":"Credential-fenced Browser Save","expected_script_count":1}`)
	path := "/api/v1/browser-recorder/sessions/" + id + "/convert"
	first := postJSON(t, server, path, body, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first convert status = %d body=%s", first.Code, first.Body.String())
	}
	var firstResult struct {
		Script StoredScript `json:"script"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	stored, ok := server.runner.GetScript(firstResult.Script.ID)
	if !ok || !scriptContainsExactActionValue(stored.Script, firstCredential) {
		t.Fatalf("first browser destination omitted the captured credential: %#v", stored)
	}

	server.browserRecording.mu.RLock()
	session := server.browserRecording.sessions[id]
	server.browserRecording.mu.RUnlock()
	if session == nil {
		t.Fatal("stopped browser recording session disappeared")
	}
	session.mu.Lock()
	if session.final == nil || len(session.final.Steps) == 0 {
		session.mu.Unlock()
		t.Fatal("stopped browser recording has no editable final artifact")
	}
	session.final.Steps[0].Value = secondCredential
	session.mu.Unlock()

	conflict := postJSON(t, server, path, body, "")
	if conflict.Code != http.StatusConflict {
		t.Fatalf(
			"changed browser credential with same operation status = %d, want 409 body=%s",
			conflict.Code,
			conflict.Body.String(),
		)
	}
	if strings.Contains(conflict.Body.String(), secondCredential) {
		t.Fatal("browser credential-conflict response disclosed the changed credential")
	}
	stored, ok = server.runner.GetScript(firstResult.Script.ID)
	if !ok || !scriptContainsExactActionValue(stored.Script, firstCredential) ||
		scriptContainsExactActionValue(stored.Script, secondCredential) {
		t.Fatalf("credential conflict changed the first browser destination: %#v", stored)
	}
}

func TestDirectImportRequestDigestIsExactKeyedAndSharedAcrossReplicas(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir() + "/direct-digest.db")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	first := NewServer(ServerConfig{Store: store})
	second := NewServer(ServerConfig{Store: store})

	script := &dsl.Script{
		Name: "Credential request", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
			Auth: &dsl.AuthConfig{
				Type: "basic", Username: "operator", Password: "first-password",
			},
		}},
	}
	firstDigest, err := first.directImportScriptsRequestDigest(
		[]*dsl.Script{script},
		map[string]any{"mode": "save"},
	)
	if err != nil {
		t.Fatalf("first digest: %v", err)
	}
	replicaDigest, err := second.directImportScriptsRequestDigest(
		[]*dsl.Script{script},
		map[string]any{"mode": "save"},
	)
	if err != nil {
		t.Fatalf("replica digest: %v", err)
	}
	if firstDigest != replicaDigest {
		t.Fatalf("HA replicas produced different request digests: %q / %q", firstDigest, replicaDigest)
	}
	if !strings.HasPrefix(firstDigest, "hmac-sha256:") ||
		strings.Contains(firstDigest, "first-password") {
		t.Fatalf("request digest is not disclosure-safe HMAC: %q", firstDigest)
	}

	changed := cloneScriptDefinition(script)
	changed.Actions[0].Auth.Password = "second-password"
	changedDigest, err := first.directImportScriptsRequestDigest(
		[]*dsl.Script{changed},
		map[string]any{"mode": "save"},
	)
	if err != nil {
		t.Fatalf("changed digest: %v", err)
	}
	if changedDigest == firstDigest {
		t.Fatal("request digest ignored an exact credential change")
	}
}

func TestDirectImportPreparedAssetsRejectsCredentialOnlyMismatch(t *testing.T) {
	prepared := &dsl.Script{
		ID: "deterministic-script", Name: "Prepared", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "request", Type: "request", Method: "GET", Target: "/",
			Auth: &dsl.AuthConfig{
				Type: "basic", Username: "operator", Password: "prepared-password",
			},
		}},
	}
	storedScript := cloneScriptDefinition(prepared)
	storedScript.Actions[0].Auth.Password = "peer-edited-password"
	stored := &StoredScript{ID: prepared.ID, Script: storedScript}
	if directImportPreparedAssetsMatch(
		nil,
		[]*StoredScript{stored},
		[]*dsl.Script{prepared},
		nil,
		nil,
	) {
		t.Fatal("stale direct-import recovery adopted a credential-only asset mismatch")
	}
}

func scriptContainsExactActionValue(script *dsl.Script, value string) bool {
	if script == nil {
		return false
	}
	found := false
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if ref.Action.Value == value {
			found = true
		}
		return nil
	})
	return found
}

func jmeterRestartAttestedSaveBody(
	t *testing.T,
	server *Server,
	operationID,
	credential string,
) []byte {
	t.Helper()
	source := &dsl.Script{
		Name:          "Restart_JMeter",
		Version:       "1",
		Protocol:      "http",
		ProjectID:     storage.DefaultProjectID,
		GeneratedBy:   "jmeter-import",
		GeneratedFrom: "jmeter",
		Config:        dsl.ScriptConfig{BaseURL: "https://restart.example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "jmeter",
			Preserved: map[string]any{
				jmeterDirectImportOperationKey: operationID,
			},
		}},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/restart",
			Headers: map[string]string{"Authorization": "Bearer " + credential},
		}},
	}
	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter", nil),
		scriptImportResponseOptions{Script: source, Scripts: []*dsl.Script{source}},
	)
	maskedScripts, ok := preview["scripts"].([]any)
	if !ok || len(maskedScripts) != 1 {
		t.Fatalf("JMeter restart preview scripts = %#v", preview["scripts"])
	}
	maskedJSON, err := json.Marshal(maskedScripts[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(maskedJSON, []byte(credential)) ||
		bytes.Contains(maskedJSON, []byte(maskedScriptSecret)) {
		t.Fatalf("JMeter restart preview did not preserve credential: %s", maskedJSON)
	}
	payload := map[string]any{
		"scripts": maskedScripts,
		"scenario_draft": map[string]any{
			"name": "Restart JMeter Scenario", "duration": "3m",
			"scenario_mode": "vuser_driven",
			"groups": []map[string]any{{
				"name": "Restart Users", "script_name": "Restart_JMeter",
				"vusers": 2, "initialization": "at_runtime",
			}},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func postJSON(t *testing.T, server *Server, path string, body []byte, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	return resp
}
