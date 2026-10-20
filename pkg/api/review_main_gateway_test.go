package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type reviewMainGatewayUnavailableTargetStore struct {
	storage.ExecutionTargetStore
	targetErr error
	poolErr   error
}

func (s reviewMainGatewayUnavailableTargetStore) GetExecutionTarget(id string) (*storage.ExecutionTarget, error) {
	if s.targetErr != nil {
		return nil, s.targetErr
	}
	return s.ExecutionTargetStore.GetExecutionTarget(id)
}

func (s reviewMainGatewayUnavailableTargetStore) ListFleetPoolsForExecutionTarget(id string) ([]*storage.FleetPool, error) {
	if s.poolErr != nil {
		return nil, s.poolErr
	}
	return s.ExecutionTargetStore.ListFleetPoolsForExecutionTarget(id)
}

func TestReviewMainGatewayStoreOutageRemainsRetryable(t *testing.T) {
	t.Setenv("APEXLOAD_ENV", "development")
	for _, operation := range []string{"execution-target lookup", "fleet-pool lookup"} {
		t.Run(operation, func(t *testing.T) {
			server, genericStore := newTimeslotServer(t)
			store := genericStore.(*storage.SQLiteStore)
			token := strings.Repeat("g", 32)
			target := seedReverseExecutionTarget(t, store, token)
			unavailable := reviewMainGatewayUnavailableTargetStore{ExecutionTargetStore: store}
			if operation == "execution-target lookup" {
				unavailable.targetErr = errors.New("temporary database connection failure")
			} else {
				unavailable.poolErr = errors.New("temporary database connection failure")
			}
			server.executionTargetStore = unavailable
			req := httptest.NewRequest(http.MethodGet, "/api/v1/generator-gateway/connect", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set(generatorGatewayIDHeader, "generator-review-store-outage")
			req.Header.Set(generatorGatewayTargetHeader, target.ID)
			req.Header.Set(generatorGatewaySessionHeader, strings.Repeat("n", 32))
			req.Header.Set(generatorGatewayTimestampHeader, fmt.Sprint(time.Now().Unix()))
			recorder := httptest.NewRecorder()
			server.handleGeneratorGatewayConnect(recorder, req)
			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s outage rejected a valid generator with permanent HTTP %d; want retryable 503: %s", operation, recorder.Code, recorder.Body.String())
			}
		})
	}
}
