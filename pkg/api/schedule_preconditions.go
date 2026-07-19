package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

var errSchedulePreconditionRequired = errors.New("schedule revision precondition is required")

// scheduleExpectedRevision requires and merges the JSON expected_revision
// precondition with the standard If-Match header. Requiring one of the two
// prevents stale clients from silently replacing a newer schedule revision.
func scheduleExpectedRevision(r *http.Request, body *int) (*int, error) {
	if body != nil && *body < 1 {
		return nil, fmt.Errorf("expected_revision must be a positive schedule revision")
	}
	if r == nil {
		if body == nil {
			return nil, errSchedulePreconditionRequired
		}
		return body, nil
	}
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		if body == nil {
			return nil, errSchedulePreconditionRequired
		}
		return body, nil
	}
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "W/"))
	raw = strings.Trim(raw, `"`)
	revision, err := strconv.Atoi(raw)
	if err != nil || revision < 1 {
		return nil, fmt.Errorf("If-Match must contain a positive schedule revision")
	}
	if body != nil && *body != revision {
		return nil, fmt.Errorf("If-Match revision %d does not match expected_revision %d", revision, *body)
	}
	return &revision, nil
}
