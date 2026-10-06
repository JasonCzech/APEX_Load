package api

import (
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/storage"
	"time"
)

type MonitorOperationResponse struct {
	ID                 string          `json:"id"`
	ConnectionID       string          `json:"connection_id"`
	ConnectionRevision int64           `json:"connection_revision"`
	Status             string          `json:"status"`
	Kind               string          `json:"kind"`
	Message            string          `json:"message,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	ExpiresAt          time.Time       `json:"expires_at"`
	Result             json.RawMessage `json:"result,omitempty"`
}

func monitorOperationResponse(op *storage.MonitorInspection) MonitorOperationResponse {
	state := op.State
	if (state == "pending" || state == "running") && !op.ExpiresAt.After(time.Now().UTC()) {
		state = "expired"
	}
	return MonitorOperationResponse{ID: op.ID, ConnectionID: op.ConnectionID, ConnectionRevision: op.ConnectionRevision, Status: state, Kind: op.Kind, Message: op.Error, CreatedAt: op.CreatedAt, ExpiresAt: op.ExpiresAt, Result: json.RawMessage(op.ResultJSON)}
}
