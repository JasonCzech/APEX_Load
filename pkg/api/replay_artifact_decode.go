package api

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/jasonczech/apexload/pkg/recordbridge"
)

// Decode only a verified bounded terminal envelope. Unknown schema fields fail
// closed instead of silently dropping newer evidence. Generic JSON integers
// retain their original precision; this is not a measurement normalization pass.
func decodeRetainedReplayReport(report []byte, id, project string) (*recordbridge.BrowserReplaySession, error) {
	if len(report) == 0 || len(report) > 16<<20 {
		return nil, recordbridge.ErrRetainedReplayUnavailable
	}
	var envelope struct {
		Session *recordbridge.BrowserReplaySession `json:"session"`
	}
	decoder := json.NewDecoder(bytes.NewReader(report))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, recordbridge.ErrRetainedReplayUnavailable
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, recordbridge.ErrRetainedReplayUnavailable
	}
	return recordbridge.RestoreRetainedReplaySession(envelope.Session, id, project)
}
