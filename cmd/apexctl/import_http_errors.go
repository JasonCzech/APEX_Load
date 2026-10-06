package main

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

type brunoCLIHTTPError struct {
	operation                string
	status                   string
	statusCode               int
	message                  string
	code                     string
	requirements             []brunoCLISecretRequirement
	sessionRetained          bool
	sessionRetainedSet       bool
	manualRetrySafe          bool
	manualRetrySafeSet       bool
	importSessionConsumed    bool
	importSessionConsumedSet bool
	hasRetryMetadata         bool
	idempotencySessionID     string
	committedSecretNames     []string
	retryAfter               time.Duration
}

func brunoCLIResponseError(operation string, response *http.Response) error {
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var decoded struct {
		Error                 string                      `json:"error"`
		Code                  string                      `json:"code"`
		Requirements          []brunoCLISecretRequirement `json:"requirements"`
		SessionRetained       *bool                       `json:"session_retained"`
		ManualRetrySafe       *bool                       `json:"manual_retry_safe"`
		ImportSessionConsumed *bool                       `json:"import_session_consumed"`
		IdempotencySessionID  string                      `json:"idempotency_session_id"`
		CommittedSecretNames  []string                    `json:"committed_secret_names"`
		CommittedNames        []string                    `json:"committed_names"`
		MigratedSecretNames   []string                    `json:"migrated_secret_names"`
	}
	var fields map[string]json.RawMessage
	decodedJSON := json.Unmarshal(payload, &fields) == nil
	_ = json.Unmarshal(payload, &decoded)
	committedNames := append([]string(nil), decoded.CommittedSecretNames...)
	committedNames = append(committedNames, decoded.CommittedNames...)
	committedNames = append(committedNames, decoded.MigratedSecretNames...)
	sort.Strings(committedNames)
	message := strings.TrimSpace(decoded.Error)
	if message == "" && !decodedJSON {
		message = strings.TrimSpace(string(payload))
	}
	_, sessionRetainedSet := fields["session_retained"]
	_, manualRetrySafeSet := fields["manual_retry_safe"]
	_, importSessionConsumedSet := fields["import_session_consumed"]
	_, idempotencySessionIDSet := fields["idempotency_session_id"]
	return &brunoCLIHTTPError{
		operation:                operation,
		status:                   response.Status,
		statusCode:               response.StatusCode,
		message:                  message,
		code:                     decoded.Code,
		requirements:             decoded.Requirements,
		sessionRetained:          decoded.SessionRetained != nil && *decoded.SessionRetained,
		sessionRetainedSet:       sessionRetainedSet,
		manualRetrySafe:          decoded.ManualRetrySafe != nil && *decoded.ManualRetrySafe,
		manualRetrySafeSet:       manualRetrySafeSet,
		importSessionConsumed:    decoded.ImportSessionConsumed != nil && *decoded.ImportSessionConsumed,
		importSessionConsumedSet: importSessionConsumedSet,
		hasRetryMetadata:         sessionRetainedSet || manualRetrySafeSet || importSessionConsumedSet || idempotencySessionIDSet,
		idempotencySessionID:     decoded.IdempotencySessionID,
		committedSecretNames:     committedNames,
		retryAfter:               parseCIRetryAfter(response.Header.Get("Retry-After"), time.Now()),
	}
}

func (e *brunoCLIHTTPError) Error() string {
	if e == nil {
		return "Bruno request failed"
	}
	message := strings.TrimSpace(e.message)
	if message == "" {
		message = strings.TrimSpace(e.status)
	}
	if e.code != "" {
		message += " (" + e.code + ")"
	}
	if e.sessionRetained {
		message += "; import session retained"
	}
	if e.idempotencySessionID != "" {
		message += "; retry session " + e.idempotencySessionID
	}
	if len(e.committedSecretNames) > 0 {
		message += "; retained secrets: " + strings.Join(e.committedSecretNames, ", ")
	}
	if len(e.requirements) > 0 {
		var missing []string
		for _, requirement := range e.requirements {
			missing = append(missing, requirement.ID+"="+requirement.SuggestedName)
		}
		message += "; bind with --secret-binding <" + strings.Join(missing, "|") + ">=<logical-secret-name>"
	}
	return e.operation + ": " + message
}
