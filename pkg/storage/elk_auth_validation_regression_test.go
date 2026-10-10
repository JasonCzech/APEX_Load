package storage

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/elastic"
)

func TestELKAuthenticationMatchesExecutionProfile(t *testing.T) {
	for _, profile := range []string{"legacy draft", "legacy configured", elastic.ProductElasticsearch, elastic.ProductOpenSearch} {
		for _, auth := range []string{"", ELKAuthAPIKey, ELKAuthBasic, elastic.AuthBearer, elastic.AuthMTLS, "unknown"} {
			t.Run(profile+"/"+auth, func(t *testing.T) {
				connection := &ELKConnection{ID: "auth-validation", ProjectID: "project", Name: "Auth validation", AuthType: auth}
				expanded := profile == elastic.ProductElasticsearch || profile == elastic.ProductOpenSearch
				if profile != "legacy draft" {
					connection.Endpoint = "https://search.example.com"
					connection.IndexPattern = "logs-*"
					connection.TimestampField = "@timestamp"
					connection.MessageField = "message"
				}
				if expanded {
					connection.Config = elastic.Config{Product: profile, Endpoint: connection.Endpoint, AuthType: auth}
				}
				wantValid := auth != "unknown"
				if !expanded && (auth == elastic.AuthBearer || auth == elastic.AuthMTLS) {
					wantValid = false
				}
				if profile == elastic.ProductOpenSearch && (auth == "" || auth == ELKAuthAPIKey) {
					wantValid = false
				}
				if err := connection.Validate(); (err == nil) != wantValid {
					t.Fatalf("Validate() = %v, want valid=%t", err, wantValid)
				}
			})
		}
	}
}
