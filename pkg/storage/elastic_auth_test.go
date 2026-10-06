package storage

import (
	"github.com/jasonczech/apexload/pkg/elastic"
	"reflect"
	"testing"
)

func TestElasticExpandedAuthenticationPersistence(t *testing.T) {
	for _, tc := range []struct{ product, auth string }{{"elasticsearch", "api_key"}, {"elasticsearch", "basic"}, {"elasticsearch", "bearer"}, {"elasticsearch", "mtls"}, {"opensearch", "basic"}, {"opensearch", "bearer"}, {"opensearch", "mtls"}} {
		t.Run(tc.product+"_"+tc.auth, func(t *testing.T) {
			store, _, _ := monitorFixture(t)
			c := &ELKConnection{ID: "elastic-auth", ProjectID: "monitor-p", Name: "Auth", Enabled: true, Endpoint: "https://search.example.com", AuthType: tc.auth, Config: elastic.Config{Product: tc.product, Endpoint: "https://search.example.com", AuthType: tc.auth}, APIKeySecretName: "API", UsernameSecretName: "USER", PasswordSecretName: "PASS", BearerTokenSecretName: "TOKEN", ClientCertificateSecretName: "CERT", ClientKeySecretName: "KEY"}
			if err := store.CreateELKConnection(c); err != nil {
				t.Fatal(err)
			}
			saved, err := store.GetELKConnection(c.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(c.Snapshot(), saved.Snapshot()) || saved.AuthType != tc.auth {
				t.Fatalf("configuration lost: %+v", saved)
			}
			if saved.ConfigurationStatus() != "unverified" {
				t.Fatal(saved.ConfigurationStatus())
			}
			saved.Name = "Edited"
			if ok, err := store.UpdateElasticConnectionIfRevision(saved, saved.Revision); err != nil || !ok {
				t.Fatalf("update %t %v", ok, err)
			}
			updated, err := store.GetELKConnection(saved.ID)
			if err != nil || updated.AuthType != tc.auth || updated.Revision != 2 {
				t.Fatalf("update lost %v %v", updated, err)
			}
		})
	}
	for _, auth := range []string{"unknown", "bearer", "mtls"} {
		legacy := ELKConnection{ID: "legacy", ProjectID: "project", Name: "legacy", AuthType: auth}
		if err := legacy.Validate(); err == nil {
			t.Fatalf("legacy accepted auth %q", auth)
		}
	}
}
