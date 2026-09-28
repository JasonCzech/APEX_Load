package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type slowArchiveLookup struct{ stubArchiveService }

func (s *slowArchiveLookup) GetRunArchiveContext(ctx context.Context, _ string) (*storage.RunArchiveRecord, bool) {
	select {
	case <-ctx.Done():
		return nil, false
	case <-time.After(100 * time.Millisecond):
		return nil, false
	}
}

func TestArchiveReadKeepsTimeoutResponseWritable(t *testing.T) {
	t.Setenv(envArchiveMaterializeTimeout, "1s")
	for _, name := range []string{"manifest", "verify", "transactions", "reports batch"} {
		t.Run(name, func(t *testing.T) {
			service := &slowArchiveLookup{stubArchiveService: stubArchiveService{testFound: true, test: &ManagedTest{ID: "slow"}}}
			server := &Server{archiveService: service}
			handler := server.handleGetRunArchiveManifest
			if name == "verify" {
				handler = server.handleVerifyRunArchive
			}
			if name == "transactions" {
				handler = server.handleGetRunArchiveTransactions
			}
			if name == "reports batch" {
				handler = server.handleGetRunArchiveReportsBatch
			}
			httpServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.SetPathValue("id", "slow"); handler(w, r) }))
			httpServer.Config.WriteTimeout = 20 * time.Millisecond
			httpServer.Start()
			defer httpServer.Close()
			client := httpServer.Client()
			client.Timeout = 2 * time.Second
			request, err := http.NewRequest(http.MethodGet, httpServer.URL, nil)
			if name == "reports batch" {
				request, err = http.NewRequest(http.MethodPost, httpServer.URL, strings.NewReader(`{"ids":["slow"]}`))
				request.Header.Set("Content-Type", "application/json")
			}
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("archive response lost to ordinary write timeout: %v", err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			expected := http.StatusNotFound
			if name == "reports batch" {
				expected = http.StatusOK
			}
			if err != nil || response.StatusCode != expected || len(body) == 0 {
				t.Fatalf("bounded lookup response: %d %s %v", response.StatusCode, body, err)
			}
		})
	}
}
