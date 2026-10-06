package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/elastic"
	"github.com/jasonczech/apexload/pkg/storage"
)

func elasticConnectionRequestFixture() ELKConnectionRequest {
	key := "ELASTIC_API_KEY"
	return ELKConnectionRequest{Name: "Search observability", Product: elastic.ProductElasticsearch, CollectionMode: "direct", Config: elastic.Config{Product: elastic.ProductElasticsearch, Endpoint: "https://elastic.example.com/base", AuthType: elastic.AuthAPIKey}, APIKeySecretName: &key}
}

func TestElasticConnectionRevisionAndWriteOnlyReferences(t *testing.T) {
	server, durable := newTimeslotServer(t)
	base := "/api/v1/projects/proj-a/monitoring/elk-connections"
	req := elasticConnectionRequestFixture()
	created := timeslotRequest(t, server, http.MethodPost, base, req)
	if created.Code != 201 {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), "ELASTIC_API_KEY") {
		t.Fatal("secret reference disclosed")
	}
	var c ELKConnectionResponse
	if json.Unmarshal(created.Body.Bytes(), &c) != nil || c.Revision != 1 || !c.HasAPIKey {
		t.Fatal(created.Body.String())
	}
	path := base + "/" + c.ID
	if got := timeslotRequest(t, server, http.MethodGet, strings.Replace(path, "proj-a", "other", 1), nil); got.Code != 404 {
		t.Fatalf("cross-project %d", got.Code)
	}
	req.Revision = 1
	req.APIKeySecretName = nil
	req.Name = "Updated"
	updated := timeslotRequest(t, server, http.MethodPut, path, req)
	if updated.Code != 200 {
		t.Fatalf("update %d %s", updated.Code, updated.Body.String())
	}
	store := durable.(storage.ElasticConnectionStore)
	saved, err := store.GetELKConnection(c.ID)
	if err != nil || saved.APIKeySecretName != "ELASTIC_API_KEY" || saved.Revision != 2 {
		t.Fatalf("saved %+v %v", saved, err)
	}
	if got := timeslotRequest(t, server, http.MethodPut, path, req); got.Code != 409 {
		t.Fatalf("stale update %d", got.Code)
	}
	if got := timeslotRequest(t, server, http.MethodPost, path+"/inspections", ElasticInspectionRequest{Kind: "verify", ExpectedRevision: 1}); got.Code != 409 {
		t.Fatalf("stale inspection %d", got.Code)
	}
	if got := timeslotRequest(t, server, http.MethodPost, path+"/inspections", ElasticInspectionRequest{Kind: "arbitrary", ExpectedRevision: 2}); got.Code != 400 {
		t.Fatalf("arbitrary inspection %d", got.Code)
	}
	_, err = store.UpdateElasticConnectionHealth(c.ID, "proj-a", 2, "connected", "verified", elastic.Capabilities{Product: elastic.ProductElasticsearch, DSL: true, PIT: true}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	req.Revision = 2
	req.Enabled = true
	if got := timeslotRequest(t, server, http.MethodPut, path, req); got.Code != 200 {
		t.Fatalf("enable verified %d %s", got.Code, got.Body.String())
	}
	saved, _ = store.GetELKConnection(c.ID)
	req.Revision = saved.Revision
	req.Config.Endpoint = "https://other.example.com"
	req.Enabled = false
	if got := timeslotRequest(t, server, http.MethodPut, path, req); got.Code != 200 {
		t.Fatalf("identity update %d %s", got.Code, got.Body.String())
	}
	saved, _ = store.GetELKConnection(c.ID)
	if saved.APIKeySecretName != "" || saved.VerifiedRevision != 0 {
		t.Fatal("changed identity retained credential or verification")
	}
}

func TestElasticComponentIdentityAndCredentialReplacement(t *testing.T) {
	old := storage.ComponentSecretReferences{AuthType: "basic", UsernameSecretName: "old-user", PasswordSecretName: "old-password"}
	user := "new-user"
	changed := applyElasticComponentCredentials(old, "https://k.example.com", "https://k.example.com", nil, nil, nil, &user, nil, nil, nil, nil)
	if changed.UsernameSecretName != "new-user" || changed.PasswordSecretName != "" {
		t.Fatalf("credential identity %+v", changed)
	}
	changed = applyElasticComponentCredentials(old, "https://k.example.com", "https://other.example.com", nil, nil, nil, nil, nil, nil, nil, nil)
	if componentCredentialsPresent(changed) {
		t.Fatal("destination change retained credentials")
	}
	key := "replacement"
	changed = applyElasticComponentCredentials(old, "https://k.example.com", "https://other.example.com", &storage.ComponentSecretReferences{AuthType: "api_key", APIKeySecretName: key}, nil, nil, nil, nil, nil, nil, nil)
	if changed.APIKeySecretName != key || changed.PasswordSecretName != "" {
		t.Fatalf("replacement %+v", changed)
	}
}

func TestElasticExecutionAndTrustChangesClearRetainedReferences(t *testing.T) {
	old := storage.ELKConnection{CollectionMode: "direct", Config: elastic.Config{Product: "elasticsearch", Endpoint: "https://search.example.com", AuthType: "api_key"}, APIKeySecretName: "old-reference"}
	for name, mutate := range map[string]func(*ELKConnectionRequest){
		"execution": func(r *ELKConnectionRequest) { r.CollectionMode = "collector"; r.CollectorAgentID = "collector" },
		"trust":     func(r *ELKConnectionRequest) { r.Config.ApprovedCIDRs = []string{"10.0.0.0/24"} },
	} {
		t.Run(name, func(t *testing.T) {
			req := ELKConnectionRequest{Config: old.Config, CollectionMode: old.CollectionMode}
			mutate(&req)
			next := old
			applyElasticConnectionExtensions(&next, old, req)
			if next.APIKeySecretName != "" {
				t.Fatal("identity retained credential")
			}
			replacement := "new-reference"
			req.APIKeySecretName = &replacement
			applyElasticConnectionExtensions(&next, old, req)
			if next.APIKeySecretName != replacement {
				t.Fatal("explicit replacement lost")
			}
		})
	}
}

func TestElasticComponentTrustAndExecutorChangesRequireReplacement(t *testing.T) {
	old := storage.ELKConnection{CollectionMode: "direct", Config: elastic.Config{Product: "elasticsearch", Endpoint: "https://search.example.com", AuthType: "api_key", KibanaEndpoint: "https://kibana.example.com", LogstashEndpoint: "https://logstash.example.com"}, KibanaCredentials: storage.ComponentSecretReferences{AuthType: "bearer", BearerTokenSecretName: "kibana-token"}, LogstashCredentials: storage.ComponentSecretReferences{AuthType: "basic", UsernameSecretName: "logstash-user", PasswordSecretName: "logstash-password"}}
	for name, mutate := range map[string]func(*ELKConnectionRequest){
		"executor": func(r *ELKConnectionRequest) { r.CollectionMode = "collector"; r.CollectorAgentID = "collector" },
		"product":  func(r *ELKConnectionRequest) { r.Config.Product = "opensearch" },
		"component trust": func(r *ELKConnectionRequest) {
			r.Config.KibanaApprovedCIDRs = []string{"10.0.0.0/24"}
			r.Config.LogstashCAPEM = "replacement-ca"
		},
	} {
		t.Run(name, func(t *testing.T) {
			req := ELKConnectionRequest{Config: old.Config, CollectionMode: old.CollectionMode}
			mutate(&req)
			next := old
			applyElasticConnectionExtensions(&next, old, req)
			if componentCredentialsPresent(next.KibanaCredentials) || componentCredentialsPresent(next.LogstashCredentials) {
				t.Fatal("component identity change retained credential references")
			}
			token, user, password := "replacement-token", "replacement-user", "replacement-password"
			req.KibanaBearerTokenSecretName, req.LogstashUsernameSecretName, req.LogstashPasswordSecretName = &token, &user, &password
			applyElasticConnectionExtensions(&next, old, req)
			if next.KibanaCredentials.BearerTokenSecretName != token || next.LogstashCredentials.UsernameSecretName != user || next.LogstashCredentials.PasswordSecretName != password {
				t.Fatal("explicit component replacements were lost")
			}
		})
	}
}
