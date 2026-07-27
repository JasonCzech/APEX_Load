package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

type projectDeletionVaultCredential struct{}

func (projectDeletionVaultCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type failingExternalSecretMetadataStore struct {
	storage.Store
	storage.ProjectSecretMetadataStore
	err error
}

func (s *failingExternalSecretMetadataStore) ListExternalProjectSecretMetadata(string) ([]storage.ProjectSecretMetadata, error) {
	return nil, s.err
}

func TestProjectAndFolderHandlers(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	createProject := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString(`{"name":"Payments"}`))
	createProject.Header.Set("Content-Type", "application/json")
	projectResp := httptest.NewRecorder()
	server.mux.ServeHTTP(projectResp, createProject)
	if projectResp.Code != http.StatusCreated {
		t.Fatalf("create project status = %d body=%s", projectResp.Code, projectResp.Body.String())
	}
	var project storage.Project
	if err := json.Unmarshal(projectResp.Body.Bytes(), &project); err != nil {
		t.Fatalf("unmarshal project: %v", err)
	}

	createFolder := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+project.ID+"/folders", bytes.NewBufferString(`{"name":"Checkout","category":"scripts"}`))
	createFolder.Header.Set("Content-Type", "application/json")
	folderResp := httptest.NewRecorder()
	server.mux.ServeHTTP(folderResp, createFolder)
	if folderResp.Code != http.StatusCreated {
		t.Fatalf("create folder status = %d body=%s", folderResp.Code, folderResp.Body.String())
	}

	listFolders := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+project.ID+"/folders?category=scripts", nil)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listFolders)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list folders status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	var listed struct {
		Folders []storage.Folder `json:"folders"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal folders: %v", err)
	}
	if len(listed.Folders) != 1 || listed.Folders[0].Name != "Checkout" {
		t.Fatalf("unexpected folders: %#v", listed.Folders)
	}
}

func TestProjectCRUDCannotBypassGitHubPolicySystemAdminBoundary(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "github-project-policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	projectAdmin := &AuthenticatedUser{
		GlobalRoles:  map[string]struct{}{RoleProjectAdmin: {}},
		ProjectRoles: map[string]map[string]struct{}{},
	}

	before, err := store.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	create := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString(`{"name":"Unsafe","settings":{"github":{"enabled":true,"allowed_repos":["any/repo"],"allowed_branches":["main"]}}}`))
	create = withAuthenticatedContext(create, authResolution{User: projectAdmin})
	createResponse := httptest.NewRecorder()
	server.handleCreateProject(createResponse, create)
	if createResponse.Code != http.StatusForbidden {
		t.Fatalf("project-admin GitHub policy create status = %d, want 403 body=%s", createResponse.Code, createResponse.Body.String())
	}
	after, err := store.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("denied project creation changed project count from %d to %d", len(before), len(after))
	}

	now := time.Now().UTC()
	project := &storage.Project{ID: "project-a", Name: "A", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	projectAdmin.ProjectRoles[project.ID] = map[string]struct{}{RoleProjectAdmin: {}}
	updateRequest := func(user *AuthenticatedUser) *http.Request {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+project.ID, bytes.NewBufferString(`{"name":"A","settings":{"github":{"enabled":true,"allowed_repos":["any/repo"],"allowed_branches":["main"]}}}`))
		req.SetPathValue("id", project.ID)
		return withAuthenticatedContext(req, authResolution{User: user})
	}
	denied := httptest.NewRecorder()
	server.handleUpdateProject(denied, updateRequest(projectAdmin))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("project-admin GitHub policy update status = %d, want 403 body=%s", denied.Code, denied.Body.String())
	}
	stored, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Settings.GitHub.Enabled {
		t.Fatal("denied project update enabled the GitHub policy")
	}

	systemAdmin := &AuthenticatedUser{GlobalRoles: map[string]struct{}{RoleSystemAdmin: {}}, ProjectRoles: map[string]map[string]struct{}{}}
	allowed := httptest.NewRecorder()
	server.handleUpdateProject(allowed, updateRequest(systemAdmin))
	if allowed.Code != http.StatusOK {
		t.Fatalf("system-admin GitHub policy update status = %d, want 200 body=%s", allowed.Code, allowed.Body.String())
	}
}

func TestFolderHandlersRejectCrossProjectParents(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, project := range []*storage.Project{
		{ID: "folder-api-project-a", Name: "A", CreatedAt: now},
		{ID: "folder-api-project-b", Name: "B", CreatedAt: now},
	} {
		if err := store.SaveProject(project); err != nil {
			t.Fatalf("SaveProject(%s): %v", project.ID, err)
		}
	}
	foreign := &storage.Folder{ID: "folder-api-foreign", ProjectID: "folder-api-project-b", Name: "Foreign", Category: "general", CreatedAt: now}
	source := &storage.Folder{ID: "folder-api-source", ProjectID: "folder-api-project-a", Name: "Source", Category: "general", CreatedAt: now}
	localParent := &storage.Folder{ID: "folder-api-local-parent", ProjectID: "folder-api-project-a", Name: "Local", Category: "general", CreatedAt: now}
	for _, folder := range []*storage.Folder{foreign, source, localParent} {
		if err := store.SaveFolder(folder); err != nil {
			t.Fatalf("SaveFolder(%s): %v", folder.ID, err)
		}
	}
	server := NewServer(ServerConfig{Store: store})

	create := httptest.NewRequest(http.MethodPost, "/api/v1/projects/folder-api-project-a/folders", bytes.NewBufferString(`{"name":"Cross-project","category":"general","parent_id":"folder-api-foreign"}`))
	create.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusBadRequest {
		t.Fatalf("cross-project create status = %d, want 400 body=%s", createResponse.Code, createResponse.Body.String())
	}

	move := httptest.NewRequest(http.MethodPut, "/api/v1/folders/folder-api-source", bytes.NewBufferString(`{"name":"Mutated","parent_id":"folder-api-foreign"}`))
	move.Header.Set("Content-Type", "application/json")
	moveResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(moveResponse, move)
	if moveResponse.Code != http.StatusBadRequest {
		t.Fatalf("cross-project move status = %d, want 400 body=%s", moveResponse.Code, moveResponse.Body.String())
	}
	stored, err := store.GetFolder(source.ID)
	if err != nil {
		t.Fatalf("GetFolder(source): %v", err)
	}
	if stored.ParentID != nil || stored.Name != "Source" {
		t.Fatalf("rejected atomic edit changed folder to name=%q parent=%#v", stored.Name, stored.ParentID)
	}

	validMove := httptest.NewRequest(http.MethodPut, "/api/v1/folders/folder-api-source", bytes.NewBufferString(`{"parent_id":"folder-api-local-parent"}`))
	validMove.Header.Set("Content-Type", "application/json")
	validMoveResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(validMoveResponse, validMove)
	if validMoveResponse.Code != http.StatusOK {
		t.Fatalf("valid move status = %d, want 200 body=%s", validMoveResponse.Code, validMoveResponse.Body.String())
	}
	moveToRoot := httptest.NewRequest(http.MethodPut, "/api/v1/folders/folder-api-source", bytes.NewBufferString(`{"parent_id":null}`))
	moveToRoot.Header.Set("Content-Type", "application/json")
	moveToRootResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(moveToRootResponse, moveToRoot)
	if moveToRootResponse.Code != http.StatusOK {
		t.Fatalf("move-to-root status = %d, want 200 body=%s", moveToRootResponse.Code, moveToRootResponse.Body.String())
	}
	stored, err = store.GetFolder(source.ID)
	if err != nil {
		t.Fatalf("GetFolder(source after root move): %v", err)
	}
	if stored.ParentID != nil {
		t.Fatalf("move-to-root left parent %#v", stored.ParentID)
	}
}

func TestProjectAdminCannotMoveProjectIntoUnauthorizedDomain(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, domainID := range []string{"placement-domain-a", "placement-domain-b"} {
		if err := store.SaveDomain(&storage.Domain{ID: domainID, Name: domainID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveDomain(%s): %v", domainID, err)
		}
		repositoryID := domainID + "-repository"
		if err := store.SaveApplicationRepository(&storage.ApplicationRepository{ID: repositoryID, DomainID: domainID, Name: repositoryID, CreatedAt: now}); err != nil {
			t.Fatalf("SaveApplicationRepository(%s): %v", repositoryID, err)
		}
		if err := store.SaveRepositoryCategory(&storage.RepositoryCategory{ID: domainID + "-category", DomainID: domainID, RepositoryID: repositoryID, Name: "Default", CreatedAt: now}); err != nil {
			t.Fatalf("SaveRepositoryCategory(%s): %v", domainID, err)
		}
	}
	project := &storage.Project{
		ID: "placement-project", DomainID: "placement-domain-a", CategoryID: "placement-domain-a-category",
		Name: "Placement", CreatedAt: now,
	}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	server := NewServer(ServerConfig{Store: store})
	user := &AuthenticatedUser{
		GlobalRoles:     map[string]struct{}{},
		ProjectRoles:    map[string]map[string]struct{}{project.ID: {RoleProjectAdmin: {}}},
		DomainRoles:     map[string]map[string]struct{}{},
		RepositoryRoles: map[string]map[string]struct{}{},
	}

	request := func() *http.Request {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/"+project.ID, bytes.NewBufferString(`{"name":"Placement","domain_id":"placement-domain-b","category_id":"placement-domain-b-category"}`))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("id", project.ID)
		req = withAuthenticatedContext(req, authResolution{User: user})
		bindAuthorizedProjectToRequest(req, project.ID)
		return req
	}

	denied := httptest.NewRecorder()
	server.handleUpdateProject(denied, request())
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unauthorized placement status = %d, want 403 body=%s", denied.Code, denied.Body.String())
	}
	stored, err := store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if stored.DomainID != "placement-domain-a" || stored.CategoryID != "placement-domain-a-category" {
		t.Fatalf("denied placement changed project to %s/%s", stored.DomainID, stored.CategoryID)
	}

	user.DomainRoles["placement-domain-b"] = map[string]struct{}{RoleEditor: {}}
	allowed := httptest.NewRecorder()
	server.handleUpdateProject(allowed, request())
	if allowed.Code != http.StatusOK {
		t.Fatalf("authorized placement status = %d, want 200 body=%s", allowed.Code, allowed.Body.String())
	}
	stored, err = store.GetProject(project.ID)
	if err != nil {
		t.Fatalf("GetProject(after move): %v", err)
	}
	if stored.DomainID != "placement-domain-b" || stored.CategoryID != "placement-domain-b-category" {
		t.Fatalf("authorized placement = %s/%s", stored.DomainID, stored.CategoryID)
	}
}

func TestDeleteProjectRetainsAndAttributesAuditHistory(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	domainID := "deletion-audit-domain"
	repositoryID := "deletion-audit-repository"
	categoryID := "deletion-audit-category"
	projectID := "deletion-audit-project"
	if err := store.SaveDomain(&storage.Domain{ID: domainID, Name: "Deletion Audit", CreatedAt: now}); err != nil {
		t.Fatalf("SaveDomain returned error: %v", err)
	}
	if err := store.SaveApplicationRepository(&storage.ApplicationRepository{ID: repositoryID, DomainID: domainID, Name: "Deletion Audit", CreatedAt: now}); err != nil {
		t.Fatalf("SaveApplicationRepository returned error: %v", err)
	}
	if err := store.SaveRepositoryCategory(&storage.RepositoryCategory{ID: categoryID, DomainID: domainID, RepositoryID: repositoryID, Name: "Deletion Audit", CreatedAt: now}); err != nil {
		t.Fatalf("SaveRepositoryCategory returned error: %v", err)
	}
	if err := store.SaveProject(&storage.Project{ID: projectID, DomainID: domainID, CategoryID: categoryID, Name: "Delete Me", CreatedAt: now}); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	if err := store.SaveAuditEvent(&storage.AuditEvent{
		ID: "pre-delete-audit", Action: "project.update", ActorType: "user", ActorSubject: "operator",
		ProjectID: projectID, DomainID: domainID, ResourceType: "project", ResourceID: projectID,
		Status: "success", CreatedAt: now,
	}); err != nil {
		t.Fatalf("SaveAuditEvent returned error: %v", err)
	}
	created, err := store.CreateProjectSecretMetadata(&storage.ProjectSecretMetadata{
		ProjectID: projectID, SecretName: "KV_TOKEN", Provider: "azure_key_vault",
		CreatedAt: now, UpdatedAt: now, CreatedBy: "operator",
	})
	if err != nil || !created {
		t.Fatalf("CreateProjectSecretMetadata returned created=%t error=%v", created, err)
	}

	// No active Key Vault provider is configured. Deletion must still discover
	// the externally retained value through the durable metadata index.
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key:   bytes.Repeat([]byte{0x6d}, 32),
		KeyID: "project-delete-test",
	})
	if err != nil {
		t.Fatalf("NewLocalAESKeyWrapper returned error: %v", err)
	}
	server := NewServer(ServerConfig{
		Store:                store,
		ImportAssetDir:       t.TempDir(),
		BrunoArtifactWrapper: wrapper,
	})
	server.sourceSecretSessions["delete-source-session"] = &sourceSecretSession{
		ID:        "delete-source-session",
		ProjectID: projectID,
		Values:    map[string]string{"requirement": "ephemeral-source-value"},
		ExpiresAt: now.Add(time.Hour),
	}
	server.brunoImportSessions["delete-bruno-session"] = &brunoImportSession{
		ID:        "delete-bruno-session",
		ProjectID: projectID,
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+projectID, nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete project status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	var deletionResponse struct {
		ExternalSecretCleanupRequired  bool     `json:"external_secret_cleanup_required"`
		ExternalSecretCleanupNames     []string `json:"external_secret_cleanup_names"`
		ExternalSecretCleanupProviders []string `json:"external_secret_cleanup_providers"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &deletionResponse); err != nil {
		t.Fatalf("unmarshal project deletion response: %v", err)
	}
	if !deletionResponse.ExternalSecretCleanupRequired || len(deletionResponse.ExternalSecretCleanupNames) != 1 ||
		deletionResponse.ExternalSecretCleanupNames[0] != "KV_TOKEN" {
		t.Fatalf("external secret cleanup response = %#v", deletionResponse)
	}
	if len(deletionResponse.ExternalSecretCleanupProviders) != 1 ||
		deletionResponse.ExternalSecretCleanupProviders[0] != "azure_key_vault" {
		t.Fatalf("external secret cleanup providers = %#v", deletionResponse.ExternalSecretCleanupProviders)
	}
	if _, exists := server.sourceSecretSessions["delete-source-session"]; exists {
		t.Fatal("project deletion retained a source-secret session")
	}
	if _, exists := server.brunoImportSessions["delete-bruno-session"]; exists {
		t.Fatal("project deletion retained a Bruno import session")
	}

	history, err := store.ListAuditEvents(storage.AuditEventFilter{ProjectID: projectID, Limit: 20})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("project audit history = %+v, want retained update plus delete", history)
	}
	var deleteEvent *storage.AuditEvent
	for _, event := range history {
		if event.Action == "project.delete" {
			deleteEvent = event
		}
	}
	if deleteEvent == nil {
		t.Fatalf("project.delete event missing from %+v", history)
	}
	if deleteEvent.ProjectID != projectID || deleteEvent.DomainID != domainID {
		t.Fatalf("project.delete scope = project %q domain %q, want %q/%q", deleteEvent.ProjectID, deleteEvent.DomainID, projectID, domainID)
	}
	encodedDeleteEvent, err := json.Marshal(deleteEvent)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encodedDeleteEvent, []byte("KV_TOKEN")) || bytes.Contains(encodedDeleteEvent, []byte("ephemeral-source-value")) {
		t.Fatalf("project.delete audit cleanup metadata = %s", encodedDeleteEvent)
	}
}

func TestDeleteProjectPartialCleanupResponseRetainsExternalSecretGuidance(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const projectID = "partial-cleanup-secret-guidance"
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{
		ID: projectID, Name: "Partial cleanup", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateProjectSecretMetadata(&storage.ProjectSecretMetadata{
		ProjectID: projectID, SecretName: "EXTERNAL_TOKEN", Provider: "azure_key_vault",
		CreatedAt: now, UpdatedAt: now, CreatedBy: "operator",
	})
	if err != nil || !created {
		t.Fatalf("CreateProjectSecretMetadata returned created=%t error=%v", created, err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{
		Key: bytes.Repeat([]byte{0x4f}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := t.TempDir()
	artifacts, err := importartifact.NewEncryptedStore(importartifact.StoreOptions{
		RootDir: artifactRoot, Wrapper: wrapper, ReadOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store})
	server.brunoArtifacts = artifacts

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+projectID, nil)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d, want 500 body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Error     string   `json:"error"`
		Deleted   string   `json:"deleted"`
		Required  bool     `json:"external_secret_cleanup_required"`
		Names     []string `json:"external_secret_cleanup_names"`
		Providers []string `json:"external_secret_cleanup_providers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error == "" || payload.Deleted != projectID || !payload.Required ||
		len(payload.Names) != 1 || payload.Names[0] != "EXTERNAL_TOKEN" ||
		len(payload.Providers) != 1 || payload.Providers[0] != "azure_key_vault" {
		t.Fatalf("partial deletion cleanup guidance = %#v", payload)
	}
	if _, err := store.GetProject(projectID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("project lookup after durable deletion = %v, want not found", err)
	}
}

func TestDeleteProjectBlocksWhenExternalSecretMetadataCannotBeEnumerated(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project := &storage.Project{
		ID:        "external-secret-enumeration-failure",
		Name:      "Must remain",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	failingStore := &failingExternalSecretMetadataStore{
		Store:                      store,
		ProjectSecretMetadataStore: store,
		err:                        errors.New("metadata backend unavailable"),
	}
	server := NewServer(ServerConfig{Store: failingStore})
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+project.ID, nil)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("delete status = %d, want 503 body=%s", response.Code, response.Body.String())
	}
	if _, err := store.GetProject(project.ID); err != nil {
		t.Fatalf("project was deleted despite metadata enumeration failure: %v", err)
	}
}

func TestProjectDeletionReportsKeyVaultPrefixWithoutPriorSecretProbe(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	const projectID = "unprobed-key-vault-project"
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: projectID, Name: "Unprobed Key Vault", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	vault := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected probe", http.StatusInternalServerError)
	}))
	t.Cleanup(vault.Close)
	keyVault := &secretstore.AzureKeyVaultStore{
		BaseURL: vault.URL, APIVersion: "7.4", Credential: projectDeletionVaultCredential{},
		Client: vault.Client(), Metadata: store,
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x37}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{
		Store: store, ProjectSecretStore: keyVault, ImportAssetDir: t.TempDir(), BrunoArtifactWrapper: wrapper,
	})
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/"+projectID, nil)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("delete project status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Required  bool     `json:"external_secret_cleanup_required"`
		Names     []string `json:"external_secret_cleanup_names"`
		Providers []string `json:"external_secret_cleanup_providers"`
		Prefix    string   `json:"external_secret_cleanup_prefix"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Required || len(payload.Names) != 0 || len(payload.Providers) != 1 || payload.Providers[0] != "azure_key_vault" ||
		payload.Prefix != engine.ProjectSecretBindingPrefix(projectID) {
		t.Fatalf("unprobed Key Vault cleanup guidance = %#v", payload)
	}
	if requests.Load() != 0 {
		t.Fatalf("project deletion made %d value-bearing Key Vault request(s) without a known logical name", requests.Load())
	}
}

func TestProjectHandlersPersistRedactionSettings(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	createProject := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString(`{"name":"Payments","settings":{"redaction_rules":[{"name":"customer id","pattern":"customer-[0-9]+"}],"max_concurrent_tests":2,"max_vusers":250}}`))
	createProject.Header.Set("Content-Type", "application/json")
	projectResp := httptest.NewRecorder()
	server.mux.ServeHTTP(projectResp, createProject)
	if projectResp.Code != http.StatusCreated {
		t.Fatalf("create project status = %d body=%s", projectResp.Code, projectResp.Body.String())
	}
	var created storage.Project
	if err := json.Unmarshal(projectResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created project: %v", err)
	}
	if len(created.Settings.RedactionRules) != 1 || created.Settings.RedactionRules[0].Pattern != "customer-[0-9]+" {
		t.Fatalf("created settings = %#v, want redaction rule", created.Settings)
	}
	if created.Settings.MaxConcurrentTests != 2 {
		t.Fatalf("created max_concurrent_tests = %d, want 2", created.Settings.MaxConcurrentTests)
	}
	if created.Settings.MaxVUsers != 250 {
		t.Fatalf("created max_vusers = %d, want 250", created.Settings.MaxVUsers)
	}

	getProject := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+created.ID, nil)
	getResp := httptest.NewRecorder()
	server.mux.ServeHTTP(getResp, getProject)
	if getResp.Code != http.StatusOK {
		t.Fatalf("get project status = %d body=%s", getResp.Code, getResp.Body.String())
	}
	var fetched storage.Project
	if err := json.Unmarshal(getResp.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("unmarshal fetched project: %v", err)
	}
	if len(fetched.Settings.RedactionRules) != 1 || fetched.Settings.RedactionRules[0].Name != "customer id" {
		t.Fatalf("fetched settings = %#v, want persisted rule", fetched.Settings)
	}
	if fetched.Settings.MaxConcurrentTests != 2 {
		t.Fatalf("fetched max_concurrent_tests = %d, want 2", fetched.Settings.MaxConcurrentTests)
	}
	if fetched.Settings.MaxVUsers != 250 {
		t.Fatalf("fetched max_vusers = %d, want 250", fetched.Settings.MaxVUsers)
	}
}

func TestProjectHandlersRejectInvalidRedactionSettings(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString(`{"name":"Payments","settings":{"redaction_rules":[{"name":"broken","pattern":"["}]}}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestProjectHandlersRejectNegativeConcurrentLimit(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString(`{"name":"Payments","settings":{"max_concurrent_tests":-1}}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestProjectHandlersRejectNegativeVUserLimit(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString(`{"name":"Payments","settings":{"max_vusers":-1}}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestHandleUpdateProjectReconcilesPendingQueue(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 5})

	project := &storage.Project{ID: "project-1", Name: "Payments", Settings: storage.ProjectSettings{MaxConcurrentTests: 1}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(project); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-project-reconcile",
		Name:      "Project Reconcile Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: project.ID,
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	first, err := server.runner.CreateTest(CreateTestRequest{Name: "First", Duration: "30s", ScriptID: "script-project-reconcile", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest(first) returned error: %v", err)
	}
	second, err := server.runner.CreateTest(CreateTestRequest{Name: "Second", Duration: "30s", ScriptID: "script-project-reconcile", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest(second) returned error: %v", err)
	}

	server.runner.mu.Lock()
	server.runner.tests[first.ID].Status = "RUNNING"
	server.runner.mu.Unlock()

	if err := server.runner.StartTest(second.ID); err != nil {
		t.Fatalf("StartTest(second) returned error: %v", err)
	}
	pending, ok := server.runner.GetTest(second.ID)
	if !ok || pending.Status != "PENDING" {
		t.Fatalf("expected pending test before quota update, got %#v ok=%v", pending, ok)
	}

	body := bytes.NewBufferString(`{"name":"Payments","settings":{"max_concurrent_tests":2}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/projects/project-1", body)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	promoted := waitForStatusAny(t, server.runner, second.ID, 5*time.Second, "INITIALIZING", "RUNNING", "COMPLETE")
	if promoted.Status == "PENDING" {
		t.Fatalf("expected pending test to be promoted after project update, got %s", promoted.Status)
	}
}
