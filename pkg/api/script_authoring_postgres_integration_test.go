//go:build postgres_integration

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func newPostgresScriptAuthoringAPIFixture(t *testing.T) string {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("APEXLOAD_TEST_POSTGRES_URL"))
	if databaseURL == "" {
		t.Skip("unqualified: APEXLOAD_TEST_POSTGRES_URL is required for actual PostgreSQL authoring API acceptance")
	}
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	// Generated letters/digits/underscore only; no request-derived SQL identifier.
	schema := fmt.Sprintf("script_authoring_api_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	var version string
	if err := admin.QueryRowContext(ctx, `SHOW server_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("Actual PostgreSQL server_version=%s owned schema=%s", version, schema)
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal("invalid PostgreSQL integration URL")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	query.Set("application_name", schema)
	parsed.RawQuery = query.Encode()
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())
	return parsed.String()
}

func shutdownPostgresScriptAuthoringAPI(t *testing.T, server *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Errorf("shutdown owned authoring API replica: %v", err)
	}
}

func openPostgresScriptAuthoringAPI(t *testing.T, dsn string) (*Server, *storage.PostgresStore) {
	t.Helper()
	store, err := storage.NewPostgresStore(dsn, storage.StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, HAExecutionLeadership: true})
	t.Cleanup(func() { shutdownPostgresScriptAuthoringAPI(t, server) })
	return server, store
}

func TestPostgresScriptAuthoringAPICrossReplicaRevisionAndReopen(t *testing.T) {
	dsn := newPostgresScriptAuthoringAPIFixture(t)
	owner, firstStore := openPostgresScriptAuthoringAPI(t, dsn)
	peer, secondStore := openPostgresScriptAuthoringAPI(t, dsn)
	if firstStore == secondStore || owner == peer || owner.runner == peer.runner {
		t.Fatal("fixture did not open independent stores/controllers")
	}
	create := authoringAPIRequest(t, owner, http.MethodPost, "/api/v1/scripts", map[string]any{"script": authoringAPIScript("pg-authoring")}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	var created StoredScript
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	peer.runner.mu.RLock()
	_, hasAlias := peer.runner.refKeyIndex[normalizeRefKeyCandidate(created.RefKey)]
	peer.runner.mu.RUnlock()
	if hasAlias || created.RefKey == "" {
		t.Fatal("peer fixture did not have a newly created, uncached RefKey")
	}
	original, tag := requireAuthoringAPIProjection(t, owner, created.ID)
	other, otherTag := requireAuthoringAPIProjection(t, peer, created.RefKey)
	if !reflect.DeepEqual(original, other) || tag != otherTag || created.AuthoringProjection == nil || !reflect.DeepEqual(created.AuthoringProjection, original) {
		t.Fatal("replicas did not return coherent content/placement/revision/ETag")
	}
	candidate := cloneScriptDefinition(created.Script)
	candidate.Name = "Saved through independent PostgreSQL replica"
	save := authoringAPIRequest(t, peer, http.MethodPut, "/api/v1/scripts/"+created.RefKey, map[string]any{"script": candidate}, otherTag)
	if save.Code != http.StatusOK || save.Header().Get("X-APEX-Script-Save-Mode") != "conditioned" {
		t.Fatalf("exact If-Match save: %d %s", save.Code, save.Body.String())
	}
	var saved StoredScript
	if err := json.Unmarshal(save.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	beforePlacement, savedTag := requireAuthoringAPIProjection(t, owner, created.ID)
	if savedTag == tag || saved.ContentRevision != beforePlacement.ContentRevision || saved.AuthoringProjection == nil || !reflect.DeepEqual(saved.AuthoringProjection, beforePlacement) || beforePlacement.Script.Name != candidate.Name || beforePlacement.Script.GoalConfig == nil || *beforePlacement.Script.GoalConfig.TargetAverageTPS != 217.5 {
		t.Fatal("conditioned save lost final snapshot/goal/revision fidelity")
	}
	app := "pg-authoring-app"
	release := "pg-authoring-release"
	if err := firstStore.SaveApplication(&storage.Application{ID: app, ProjectID: storage.DefaultProjectID, Name: "App"}); err != nil {
		t.Fatal(err)
	}
	if err := firstStore.SaveRelease(&storage.Release{ID: release, ApplicationID: app, Name: "Release"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := firstStore.ReplaceLibraryAssignment("script", created.ID, storage.DefaultProjectID, &app, []string{release}, 0); err != nil {
		t.Fatal(err)
	}
	placed, placedTag := requireAuthoringAPIProjection(t, peer, created.ID)
	if placedTag == savedTag || !reflect.DeepEqual(placed.Script, beforePlacement.Script) || placed.LibraryPlacement.ApplicationID == nil || *placed.LibraryPlacement.ApplicationID != app || !reflect.DeepEqual(placed.LibraryPlacement.ReleaseIDs, []string{release}) {
		t.Fatal("placement-only change did not invalidate paired authoring identity")
	}
	candidate.Name = "Stale overwrite must remain rejected"
	stale := authoringAPIRequest(t, peer, http.MethodPut, "/api/v1/scripts/"+created.ID, map[string]any{"script": candidate}, savedTag)
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale placement If-Match: %d %s", stale.Code, stale.Body.String())
	}
	current, currentTag := requireAuthoringAPIProjection(t, owner, created.ID)
	if currentTag != placedTag || !reflect.DeepEqual(current, placed) {
		t.Fatal("rejected stale save changed durable authored state")
	}
	// The peer is already warm. A second newly created alias must resolve from
	// the other store without any local display-cache index refresh.
	late, err := owner.runner.CreateScriptAuthoringDurable(authoringAPIScript("pg-late-authoring"))
	if err != nil {
		t.Fatal(err)
	}
	peer.runner.mu.RLock()
	_, hasLateAlias := peer.runner.refKeyIndex[normalizeRefKeyCandidate(late.RefKey)]
	peer.runner.mu.RUnlock()
	if hasLateAlias {
		t.Fatal("fresh alias was unexpectedly already indexed by peer")
	}
	var lateTag string
	for _, alias := range []string{late.RefKey, strings.ToLower(late.RefKey), strings.ToUpper(late.RefKey)} {
		projection, aliasTag := requireAuthoringAPIProjection(t, peer, alias)
		if projection.ID != late.ID || projection.ContentRevision != late.ContentRevision || lateTag != "" && lateTag != aliasTag {
			t.Fatal("fresh RefKey case alias did not resolve the same durable snapshot")
		}
		lateTag = aliasTag
	}
	shutdownPostgresScriptAuthoringAPI(t, peer)
	shutdownPostgresScriptAuthoringAPI(t, owner)
	if err := secondStore.Close(); err != nil {
		t.Fatal(err)
	}
	if err := firstStore.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _ := openPostgresScriptAuthoringAPI(t, dsn)
	afterReopen, reopenedTag := requireAuthoringAPIProjection(t, reopened, placed.Script.RefKey)
	if reopenedTag != placedTag || !reflect.DeepEqual(afterReopen, placed) {
		t.Fatal("close/reopen changed content, goals, placement, or persisted revision")
	}
	if placed.Script.RefKey == created.RefKey {
		t.Fatal("fixture did not advance the authored reference version on save")
	}
	obsolete := authoringAPIRequest(t, reopened, http.MethodGet, "/api/v1/scripts/"+created.RefKey+"?view=authoring", nil, "")
	if obsolete.Code != http.StatusNotFound {
		t.Fatalf("obsolete reference resolved after reopen: %d %s", obsolete.Code, obsolete.Body.String())
	}
	lateReopened, lateReopenedTag := requireAuthoringAPIProjection(t, reopened, late.RefKey)
	if lateReopened.ID != late.ID || lateReopened.ContentRevision != late.ContentRevision || lateReopenedTag != lateTag {
		t.Fatal("close/reopen lost freshly resolved RefKey authority")
	}
}
