package api

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLibraryImportProjectionPreservesRichClaimValidation(t *testing.T) {
	now := time.Now().UTC()
	valid := libraryImportClaim{OwnerID: "owner", RemoteID: "direct-import:projection", ProjectID: storage.DefaultProjectID, Kind: "script", SourceInstanceID: "instance", SourceItemID: "item", MutationID: "mutation", LocalItemID: "local", IntendedHash: "hash", Prepared: true, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	valid.Identity = libraryLinkKey(valid.RemoteID, valid.ProjectID, valid.Kind, valid.SourceInstanceID, valid.SourceItemID)
	encode := func(claims []libraryImportClaim) string {
		raw, err := json.Marshal(claims)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if claims, err := decodeLibraryImportClaims(encode([]libraryImportClaim{valid})); err != nil || len(claims) != 1 {
		t.Fatal("valid rich claim rejected", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*libraryImportClaim)
	}{
		{"owner", func(c *libraryImportClaim) { c.OwnerID = "" }},
		{"identity", func(c *libraryImportClaim) { c.Identity = "incorrect" }},
		{"lease", func(c *libraryImportClaim) { c.ExpiresAt = time.Time{} }},
		{"sequence", func(c *libraryImportClaim) { c.Sequence = -1 }},
		{"applied_without_prepare", func(c *libraryImportClaim) { c.Prepared = false; c.Applied = true }},
		{"archive_activation", func(c *libraryImportClaim) { c.ArchiveActivated = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claim := valid
			tc.mutate(&claim)
			if _, err := decodeLibraryImportClaims(encode([]libraryImportClaim{claim})); err == nil {
				t.Fatal("projection displaced rich validation")
			}
		})
	}
	for _, raw := range []string{
		strings.Replace(encode([]libraryImportClaim{valid}), `"prepared":true`, `"prepared":true,"PREPARED":false`, 1),
		strings.Replace(encode([]libraryImportClaim{valid}), `"prepared":true`, `"prepared":null`, 1),
		strings.Replace(encode([]libraryImportClaim{valid}), `"local_item_id":"local"`, `"local_item_id":7`, 1),
		strings.Replace(encode([]libraryImportClaim{valid}), `"kind":"script"`, `"kind":"script","Kind":"result"`, 1),
	} {
		if _, err := decodeLibraryImportClaims(raw); !errors.Is(err, storage.ErrLibraryImportPublicationUnavailable) {
			t.Fatalf("ambiguous projection accepted: %v", err)
		}
	}
	other := valid
	other.SourceItemID = "second"
	other.Identity = libraryLinkKey(other.RemoteID, other.ProjectID, other.Kind, other.SourceInstanceID, other.SourceItemID)
	if _, err := decodeLibraryImportClaims(encode([]libraryImportClaim{valid, other})); err == nil {
		t.Fatal("duplicate owner sequence accepted")
	}
	other.Sequence = 1
	other.Committed = true
	if _, err := decodeLibraryImportClaims(encode([]libraryImportClaim{valid, other})); err == nil {
		t.Fatal("mixed terminal owner state accepted")
	}
}

func TestScenarioSourceAdmissionUsesRealPreparedClaims(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "source-real-claims.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	server := NewServer(ServerConfig{Store: base})
	script := libraryTestScript("source-real-survivor", "Published survivor", storage.DefaultProjectID, "/source")
	if _, err := server.runner.CreateScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	groups := []storage.ScenarioScript{{ScriptID: script.ID, Name: "Group", VUsers: 1}}
	initial, err := base.GetScenarioSourceAuthoringSnapshot(groups)
	if err != nil {
		t.Fatal(err)
	}
	sourceBefore, err := base.GetScriptAuthoringSnapshot(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	created, err := base.CreateScenarioAuthoringWithSourcesIfAbsent(&storage.Scenario{ID: "source-real-parent", Name: "Baseline", Duration: "1m"}, groups, nil, initial.Admission)
	if err != nil {
		t.Fatal(err)
	}
	parent := created.Snapshot
	transaction, err := newCreateImportTransaction(server, "source-real-survivor", []*dsl.Script{script}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := transaction.commitUnmutatedClaims(); err != nil {
			t.Error(err)
		}
		transaction.closeWithoutAssetCompensation()
	}()
	if transaction.mutationStarted {
		t.Fatal("prepared claim mutated surviving asset")
	}
	sourceAfter, err := base.GetScriptAuthoringSnapshot(script.ID)
	if err != nil || !reflect.DeepEqual(sourceBefore, sourceAfter) {
		t.Fatal("prepared claim changed source nonce/content", err)
	}
	if _, err := base.GetScenarioSourceAuthoringSnapshot(groups); !errors.Is(err, storage.ErrScenarioSourceAdmissionConflict) {
		t.Fatal("initial compiler admitted unpublished survivor", err)
	}
	current, err := base.GetLibraryImportPublicationSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []storage.ScenarioSourceAdmission{initial.Admission, {ProjectID: initial.Admission.ProjectID, Sources: initial.Admission.Sources, Publication: current.State}} {
		if r, err := base.CreateScenarioAuthoringWithSourcesIfAbsent(&storage.Scenario{ID: "source-real-attempt", Name: "Attempt", Duration: "1m"}, groups, nil, evidence); r != nil || !errors.Is(err, storage.ErrScenarioSourceAdmissionConflict) {
			t.Fatal("create bypassed real prepared claim", r, err)
		}
		changed := *parent.Scenario
		changed.Name = "Attempt"
		expected := storage.ScenarioAuthoringPrecondition{ProjectID: parent.ProjectID, CreatedAt: parent.Scenario.CreatedAt, ContentRevision: parent.ContentRevision}
		if r, err := base.UpdateScenarioAuthoringWithSourcesIfCurrent(&changed, parent.Scripts, nil, expected, evidence); r != nil || !errors.Is(err, storage.ErrScenarioSourceAdmissionConflict) {
			t.Fatal("update bypassed real prepared claim", r, err)
		}
		after, err := base.GetScenarioAuthoringSnapshot(parent.Scenario.ID)
		if err != nil || !reflect.DeepEqual(parent, after) {
			t.Fatal("refusal changed durable scenario", err)
		}
	}
	if err := transaction.commitUnmutatedClaims(); err != nil {
		t.Fatal(err)
	}
	transaction.closeWithoutAssetCompensation()
	fresh, err := base.GetScenarioSourceAuthoringSnapshot(groups)
	if err != nil {
		t.Fatal("terminal no-op source missing", err)
	}
	if _, err := base.CreateScenarioAuthoringWithSourcesIfAbsent(&storage.Scenario{ID: "source-real-attempt", Name: "Fresh", Duration: "1m"}, groups, nil, fresh.Admission); err != nil {
		t.Fatal("fresh durable evidence rejected", err)
	}
}
