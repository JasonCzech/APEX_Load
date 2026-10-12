package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/livetable"
)

// ProjectLiveTableMutation accepts semantic fields only. The controller owns
// client identity, sequence, owner, lease tokens and the transactional audit.
type ProjectLiveTableMutation struct {
	IdempotencyKey     string            `json:"idempotency_key"`
	IssuedAt           time.Time         `json:"issued_at"`
	ExpectedInstanceID string            `json:"expected_instance_id"`
	ExpectedRevision   string            `json:"expected_revision"`
	Operation          string            `json:"operation"`
	RecordID           string            `json:"record_id,omitempty"`
	Position           int               `json:"position,omitempty"`
	ExpectedVersion    string            `json:"expected_version,omitempty"`
	Values             map[string]string `json:"values,omitempty"`
	Columns            []string          `json:"columns,omitempty"`
	Delta              string            `json:"delta,omitempty"`
}

type ProjectLiveTableRow struct {
	ID      string            `json:"id,omitempty"`
	Version string            `json:"version"`
	Values  map[string]string `json:"values"`
	State   string            `json:"state,omitempty"`
}

type ProjectLiveTableOperatorResult struct {
	Status   livetable.Status     `json:"status"`
	Revision string               `json:"revision"`
	Row      *ProjectLiveTableRow `json:"row,omitempty"`
	Value    string               `json:"value,omitempty"`
	Count    string               `json:"count,omitempty"`
}

type ProjectLiveTableMutationResponse struct {
	Result    ProjectLiveTableOperatorResult `json:"result"`
	Replayed  bool                           `json:"replayed"`
	ExpiresAt time.Time                      `json:"expires_at"`
}

func projectTableRow(row livetable.Row) ProjectLiveTableRow {
	return ProjectLiveTableRow{ID: row.ID, Version: strconv.FormatUint(row.Version, 10), Values: row.Values, State: row.State}
}

func projectOperatorResult(result livetable.Result) ProjectLiveTableOperatorResult {
	out := ProjectLiveTableOperatorResult{Status: result.Status, Revision: strconv.FormatUint(result.Revision, 10), Value: result.Value, Count: strconv.FormatInt(result.Count, 10)}
	if result.Row != nil {
		row := projectTableRow(*result.Row)
		out.Row = &row
	}
	return out
}

func parseProjectTableRevision(raw string) (uint64, error) {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || strconv.FormatUint(value, 10) != raw {
		return 0, errors.New("revision must be a canonical unsigned decimal string")
	}
	return value, nil
}

func projectOperatorDigest(input ProjectLiveTableMutation) string {
	body, _ := json.Marshal(input)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func (input ProjectLiveTableMutation) request(profile livetable.Profile, now time.Time, retryWindow time.Duration) (livetable.Request, time.Time, error) {
	var request livetable.Request
	key, err := uuid.Parse(input.IdempotencyKey)
	if err != nil || key.String() != input.IdempotencyKey || key == uuid.Nil {
		return request, time.Time{}, errors.New("idempotency_key must be a nonzero canonical UUID")
	}
	if input.IssuedAt.IsZero() || retryWindow <= 0 || input.IssuedAt.After(now.Add(30*time.Second)) {
		return request, time.Time{}, errors.New("issued_at must identify the current request within the retry window")
	}
	expires := input.IssuedAt.Add(retryWindow)
	if !now.Before(expires) {
		return request, expires, &livetable.Error{Code: livetable.CodeExpired, Message: "operator request retry window expired; inspect current state before creating a new request"}
	}
	if input.ExpectedInstanceID == "" || len(input.ExpectedInstanceID) > 256 {
		return request, expires, errors.New("expected_instance_id is required")
	}
	revision, err := parseProjectTableRevision(input.ExpectedRevision)
	if err != nil {
		return request, expires, err
	}
	request.ExpectedRevision = &revision
	request.RecordID = input.RecordID
	request.Position = input.Position
	request.Values = input.Values
	request.Columns = input.Columns
	if len(input.RecordID) > 256 || input.Position < 0 || input.Position > 100000 || len(input.Values) > 128 || len(input.Columns) > 128 {
		return request, expires, errors.New("operator command exceeds its bounds")
	}
	if input.ExpectedVersion != "" {
		version, err := parseProjectTableRevision(input.ExpectedVersion)
		if err != nil || version == 0 {
			return request, expires, errors.New("expected_version must be a positive decimal string")
		}
		request.ExpectedVersion = &version
	}
	switch input.Operation {
	case "append":
		request.Operation = livetable.OpPush
		if len(input.Values) == 0 || input.RecordID != "" || input.Position != 0 || input.ExpectedVersion != "" || len(input.Columns) > 0 || input.Delta != "" {
			return request, expires, errors.New("append accepts values only")
		}
	case "update":
		request.Operation = livetable.OpUpdate
		if len(input.Values) == 0 || len(input.Columns) > 0 || input.Delta != "" {
			return request, expires, errors.New("update requires values and no column selector or delta")
		}
	case "clear":
		request.Operation = livetable.OpClear
		if len(input.Columns) == 0 || len(input.Values) > 0 || input.Delta != "" {
			return request, expires, errors.New("clear requires selected columns only")
		}
	case "increment":
		request.Operation = livetable.OpIncrement
		if len(input.Columns) != 1 || len(input.Values) > 0 {
			return request, expires, errors.New("increment requires exactly one selected column")
		}
		delta, err := strconv.ParseInt(input.Delta, 10, 64)
		if err != nil || strconv.FormatInt(delta, 10) != input.Delta {
			return request, expires, errors.New("delta must be a signed decimal string")
		}
		request.Delta = delta
	default:
		return request, expires, errors.New("unsupported operator mutation")
	}
	if input.Operation != "append" {
		switch profile {
		case livetable.ProfileNative:
			if input.RecordID == "" || input.Position != 0 || input.ExpectedVersion == "" {
				return request, expires, errors.New("native edits require record_id and expected_version")
			}
		case livetable.ProfileVTS:
			if input.Position <= 0 || input.RecordID != "" || input.ExpectedVersion != "" {
				return request, expires, errors.New("shared-column edits require a position and table revision")
			}
		default:
			return request, expires, errors.New("unsupported project table profile")
		}
	}
	body, err := json.Marshal(input)
	if err != nil || len(body) > 1<<20 {
		return request, expires, fmt.Errorf("operator command exceeds 1 MiB")
	}
	return request, expires, nil
}
