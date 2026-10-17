package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func preparedBatchScript(id string) *dsl.Script {
	return &dsl.Script{ID: id, Name: id, ProjectID: storage.DefaultProjectID, Protocol: "http", Version: "1.0.0",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}}}
}

func closeBatchTestServer(t *testing.T, server *Server) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
}

func TestImportBatchCommitCompensatesPartialWrite(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &failSecondAsyncAPIBatchScriptStore{libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: base}}
	server := NewServer(ServerConfig{Store: store})
	closeBatchTestServer(t, server)
	batch := newImportBatchCommit(server, nil, nil)
	_, _, failure := batch.commitPrepared(preparedImportBatch{Source: "batch-test", DefaultProfile: "har", Scripts: []*dsl.Script{preparedBatchScript("first"), preparedBatchScript("second")}})
	if failure == nil || failure.Stage != importBatchScript || failure.Index != 1 {
		t.Fatalf("failure = %+v", failure)
	}
	if batch.assetsCommitted || batch.finished {
		t.Fatal("partial write became terminal")
	}
	if err := batch.close(); err != nil {
		t.Fatal(err)
	}
	if scripts, err := base.ListScripts(storage.DefaultProjectID, nil); err != nil || len(scripts) != 0 {
		t.Fatalf("compensated scripts = %v, %v", scripts, err)
	}
}

func TestImportBatchCommitRetainsCommittedAssetsAfterReceiptFailure(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := &failOnceDirectImportReceiptStore{SQLiteStore: base, failComplete: true}
	server := NewServer(ServerConfig{Store: sqliteOverlay(store)})
	closeBatchTestServer(t, server)
	attempt, replay, err := server.claimDirectImportAttempt(storage.DefaultProjectID, "batch-test", "lost-reply", directImportSHA256("request"))
	if err != nil || replay {
		t.Fatalf("claim = %v, %v", replay, err)
	}
	batch := newImportBatchCommit(server, attempt, nil)
	scripts, _, failure := batch.commitPrepared(preparedImportBatch{Source: "batch-test", DefaultProfile: "har", Scripts: []*dsl.Script{preparedBatchScript("committed")}})
	if failure != nil {
		t.Fatal(failure)
	}
	if err := batch.complete(scripts, nil); err == nil {
		t.Fatal("expected lost completion response")
	}
	if !batch.assetsCommitted || batch.finished {
		t.Fatal("receipt failure erased committed state")
	}
	if err := batch.close(); err != nil {
		t.Fatal(err)
	}
	if saved, err := base.GetScript("committed"); err != nil || saved == nil {
		t.Fatalf("committed asset lost: %v", err)
	}
	// A recovery owner must suppress compensation before adapter validation.
	recovery := newImportBatchCommit(server, nil, nil)
	adopted, _, err := recovery.loadRecovery(storage.DefaultProjectID, []string{"committed"}, "", nil, nil)
	if err != nil || len(adopted) != 1 || !recovery.assetsCommitted {
		t.Fatalf("adoption = %v, %v", adopted, err)
	}
	if err := recovery.close(); err != nil {
		t.Fatal(err)
	}
	if err := batch.complete(scripts, nil); err != nil {
		t.Fatal(err)
	}
	if !batch.finished {
		t.Fatal("receipt success was not terminal")
	}
}

func TestImportBatchRecoveryDistinguishesPendingUpdates(t *testing.T) {
	server := newReimportTestServer(t)
	closeBatchTestServer(t, server)
	batch := newImportBatchCommit(server, nil, nil)
	_, _, failure := batch.commitPrepared(preparedImportBatch{Source: "batch-test", DefaultProfile: "har", Scripts: []*dsl.Script{preparedBatchScript("updated")}})
	if failure != nil {
		t.Fatal(failure)
	}
	defer batch.close()
	want := preparedBatchScript("updated")
	want.Actions[0].Target = "/new"
	recovery := newImportBatchCommit(server, nil, nil)
	_, _, err := recovery.loadRecovery(storage.DefaultProjectID, []string{"updated"}, "", []*dsl.Script{want}, []*importUpdateTarget{{ScriptID: "updated"}})
	if !errors.Is(err, errImportUpdateTargetStale) || recovery.assetsCommitted {
		t.Fatalf("pending update = %v, committed=%v", err, recovery.assetsCommitted)
	}
	_, _, err = recovery.loadRecovery(storage.DefaultProjectID, []string{"missing"}, "", nil, nil)
	if err == nil || recovery.assetsCommitted {
		t.Fatal("missing assets were adopted")
	}
}

func TestImportBatchFailureResponsesPreserveAdapterContracts(t *testing.T) {
	for _, test := range []struct {
		name     string
		messages importBatchMessages
		save     string
	}{
		{"asyncapi", asyncAPIImportBatchMessages, "failed to save AsyncAPI script 2: injected"},
		{"generic", genericImportBatchMessages, "failed to save imported script 2: injected"},
		{"enterprise", enterpriseImportBatchMessages, "failed to save enterprise archive script 2: injected"},
		{"jmeter", jmeterImportBatchMessages, "failed to save imported script: injected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, stage := range []struct {
				stage  importBatchStage
				status int
				text   string
			}{
				{importBatchPrepare, 500, test.messages.Prepare + "injected"},
				{importBatchReceipt, 409, test.messages.Receipt},
				{importBatchDraft, 409, test.messages.Draft},
				{importBatchScript, 500, test.save},
				{importBatchScenario, 400, "failed to save scenario draft: injected"},
				{importBatchCommitStage, 500, test.messages.Commit + "injected"},
				{importBatchReadUpdate, 500, "failed to read re-import update target: injected"},
			} {
				cause := errors.New("injected")
				failure := batchFailure(stage.stage, 1, cause)
				if !errors.Is(failure, cause) {
					t.Fatal("failure lost cause identity")
				}
				response := httptest.NewRecorder()
				writeImportBatchFailure(response, failure, test.messages)
				want := stage.text
				if stage.status == http.StatusInternalServerError {
					// writeError deliberately maps internal details to the stable
					// public error envelope for every importer.
					want = "internal server error"
				}
				if response.Code != stage.status || !strings.Contains(response.Body.String(), want) {
					t.Fatalf("stage %d response=%d %s", stage.stage, response.Code, response.Body.String())
				}
			}
			response := httptest.NewRecorder()
			writeImportBatchFailure(response, batchFailure(importBatchStaleUpdate, 1, staleImportUpdate("changed")), test.messages)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "changed") {
				t.Fatalf("stale response=%d %s", response.Code, response.Body.String())
			}
		})
	}
}
