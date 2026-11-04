package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

var errTrailingJSONValue = errors.New("multiple JSON values are not allowed")

type trailingJSONError struct {
	err error
}

func (e trailingJSONError) Error() string {
	return e.err.Error()
}

func (e trailingJSONError) Unwrap() error {
	return e.err
}

func (e trailingJSONError) Is(target error) bool {
	return target == errTrailingJSONValue
}

func decodeSingleJSONValue(decoder *json.Decoder, value any, disallowUnknownFields bool) error {
	if disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errTrailingJSONValue
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return err
		}
		return trailingJSONError{err: err}
	}
	return nil
}

func decodeStrictJSONDecoder(decoder *json.Decoder, value any) error {
	return decodeSingleJSONValue(decoder, value, true)
}

func decodeStrictDomainJSON(w http.ResponseWriter, r *http.Request, value any, domainLabel string) bool {
	err := decodeStrictJSONDecoder(json.NewDecoder(r.Body), value)
	if err == nil {
		return true
	}
	var bound *http.MaxBytesError
	if errors.As(err, &bound) {
		writeError(w, 413, domainLabel+" request exceeds the configured size limit")
	} else {
		writeError(w, 400, "invalid "+domainLabel+" request")
	}
	return false
}

func decodeMonitorJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	return decodeStrictDomainJSON(w, r, value, "Monitoring")
}

func parseCollectorInspectionCapacity(w http.ResponseWriter, r *http.Request, defaultCap int, invalidMessage string) (int, bool) {
	capacity := defaultCap
	if raw := r.URL.Query().Get("capacity"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 4 {
			writeError(w, 400, invalidMessage)
			return 0, false
		}
		capacity = parsed
	}
	return capacity, true
}

func collectorInspectionLeaseUntil(now, expiresAt time.Time) time.Time {
	until := now.Add(time.Minute)
	if expiresAt.Before(until) {
		return expiresAt
	}
	return until
}

func resumeDirectProviderOperations[T any](
	s *Server,
	parent context.Context,
	slots chan struct{},
	list func(limit int) ([]T, error),
	shouldSkip func(T) bool,
	claim func(op T, owner string, now, leaseUntil time.Time) (bool, error),
	execute func(parent context.Context, op T, owner string),
) {
	if s == nil || s.runner == nil || !s.runner.isExecutionLeader() {
		return
	}
	ops, err := list(4)
	if err != nil {
		return
	}
	for _, op := range ops {
		if shouldSkip != nil && shouldSkip(op) {
			continue
		}
		if slots != nil {
			select {
			case slots <- struct{}{}:
			default:
				return
			}
		}
		owner := uuid.NewString()
		now := time.Now().UTC()
		claimed, err := claim(op, owner, now, now.Add(time.Minute))
		if err != nil || !claimed {
			if slots != nil {
				<-slots
			}
			continue
		}
		work := op
		if !s.launchBackground(func() {
			if slots != nil {
				defer func() { <-slots }()
			}
			execute(parent, work, owner)
		}) {
			if slots != nil {
				<-slots
			}
		}
	}
}
