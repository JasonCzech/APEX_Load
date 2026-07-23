package java_vuser

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func emitRunnerTransactions(action *protocol.Action, events []runnerEvent, emit func(*protocol.Result) error) error {
	return emitRunnerTransactionsAt(action, events, time.Now(), false, emit)
}

type openRunnerTransaction struct {
	started runnerEvent
	closed  bool
}

// emitRunnerTransactionsAt pairs streamed transaction events into child
// samples. When truncated is true, the event capture hit its byte cap, so
// unpaired events are partial evidence rather than proof about any individual
// transaction: dangling starts and orphaned ends are skipped. One bounded,
// synthetic failed child records that the transaction stream itself was
// indeterminate without fabricating names or durations. The parent Java action
// remains governed by lifecycle/transport status, not business child status.
func emitRunnerTransactionsAt(action *protocol.Action, events []runnerEvent, boundary time.Time, truncated bool, emit func(*protocol.Result) error) error {
	if emit == nil {
		return nil
	}
	starts := make(map[string][]*openRunnerTransaction)
	orderedStarts := make([]*openRunnerTransaction, 0)
	latestEvent := time.Time{}
	for _, event := range events {
		if timestamp := eventTime(event); timestamp.After(latestEvent) {
			latestEvent = timestamp
		}
		typeName := strings.ToLower(strings.TrimSpace(event.Type))
		key := transactionEventKey(event)
		switch typeName {
		case "runtime":
			if result := failedRunnerExitResult(action, event); result != nil {
				if err := emit(result); err != nil {
					return err
				}
			}
		case "transaction_start":
			open := &openRunnerTransaction{started: event}
			starts[key] = append(starts[key], open)
			orderedStarts = append(orderedStarts, open)
		case "transaction_end", "transaction_unmatched", "transaction_ignored":
			var started runnerEvent
			matched := false
			if stack := starts[key]; len(stack) > 0 {
				open := stack[len(stack)-1]
				open.closed = true
				started = open.started
				starts[key] = stack[:len(stack)-1]
				matched = true
			}
			if typeName == "transaction_ignored" {
				// LoadRunner omits a transaction interrupted by lr.exit from
				// pass/fail transaction counts. Consume the matching start, but
				// deliberately emit no child sample.
				continue
			}
			if !matched && truncated && typeName == "transaction_end" {
				// The start (and true duration) for this end was lost to the
				// capture cap; do not fabricate a failed unmatched sample from
				// partial evidence. Runner-authored transaction_unmatched events
				// are the runner's own verdict and are kept regardless.
				continue
			}
			if err := emit(transactionResult(action, started, event)); err != nil {
				return err
			}
		}
	}
	if latestEvent.After(boundary) {
		boundary = latestEvent
	}
	if boundary.IsZero() {
		boundary = time.Now()
	}
	for _, open := range orderedStarts {
		if open.closed || truncated {
			continue
		}
		started := open.started
		ended := runnerEvent{
			Contract:      started.Contract,
			RequestID:     started.RequestID,
			TimestampMS:   boundary.UnixMilli(),
			TransactionID: started.TransactionID,
			Type:          "transaction_unmatched",
			Name:          started.Name,
			Message:       "1",
		}
		if err := emit(transactionResult(action, started, ended)); err != nil {
			return err
		}
	}
	if truncated {
		if err := emit(truncatedTransactionStreamResult(action, boundary)); err != nil {
			return err
		}
	}
	return nil
}

func failedRunnerExitResult(action *protocol.Action, event runnerEvent) *protocol.Result {
	if !strings.EqualFold(strings.TrimSpace(event.Name), "exit") {
		return nil
	}
	fields := strings.Fields(event.Message)
	if len(fields) < 2 {
		return nil
	}
	continuationOption, continuationErr := strconv.Atoi(fields[0])
	exitStatus, statusErr := strconv.Atoi(fields[1])
	if continuationErr != nil || statusErr != nil || exitStatus == 0 {
		return nil
	}
	startTime := eventTime(event)
	if startTime.IsZero() {
		startTime = time.Now()
	}
	parentName := ""
	target := ""
	if action != nil {
		parentName = action.Name
		target = strings.TrimSpace(action.Target)
	}
	return &protocol.Result{
		ActionName:       "lr.exit",
		SampleKind:       "child",
		ParentActionName: parentName,
		Group:            parentName,
		StartTime:        startTime,
		Duration:         0,
		DependencyType:   javaVuserProtocolName,
		DependencyTarget: target,
		StatusCode:       500,
		Success:          false,
		ErrorMessage: fmt.Sprintf(
			"lr.exit requested continuation option %d with failed status %d",
			continuationOption,
			exitStatus,
		),
		Extractions: map[string]string{},
		ProtocolEvidence: map[string]any{
			"protocol":            javaVuserProtocolName,
			"event_contract":      event.Contract,
			"runner_request_id":   event.RequestID,
			"continuation_option": continuationOption,
			"exit_status":         exitStatus,
			"synthetic":           true,
		},
	}
}

func truncatedTransactionStreamResult(action *protocol.Action, boundary time.Time) *protocol.Result {
	if boundary.IsZero() {
		boundary = time.Now()
	}
	parentName := ""
	target := ""
	if action != nil {
		parentName = action.Name
		target = strings.TrimSpace(action.Target)
	}
	const name = "Java transaction event stream"
	return &protocol.Result{
		ActionName:          name,
		BusinessTransaction: true,
		SampleKind:          "child",
		ParentActionName:    parentName,
		Group:               name,
		StartTime:           boundary,
		Duration:            0,
		DependencyType:      javaVuserProtocolName,
		DependencyTarget:    target,
		StatusCode:          500,
		Success:             false,
		ErrorMessage:        "java_vuser transaction event stream was truncated; individual missing transaction outcomes are indeterminate",
		Extractions:         map[string]string{},
		ProtocolEvidence: map[string]any{
			"protocol":                      javaVuserProtocolName,
			"synthetic":                     true,
			"runner_event_stream_truncated": true,
		},
	}
}

func transactionEventKey(event runnerEvent) string {
	if id := strings.TrimSpace(event.TransactionID); id != "" {
		return "id:" + id
	}
	return "name:" + strings.TrimSpace(event.Name)
}

func transactionResult(action *protocol.Action, started, ended runnerEvent) *protocol.Result {
	name := strings.TrimSpace(ended.Name)
	if name == "" {
		name = strings.TrimSpace(started.Name)
	}
	if name == "" {
		name = "(unnamed)"
	}
	startTime := eventTime(started)
	endTime := eventTime(ended)
	if startTime.IsZero() {
		startTime = endTime
	}
	if startTime.IsZero() {
		startTime = time.Now()
	}
	if endTime.IsZero() || endTime.Before(startTime) {
		endTime = startTime
	}
	duration := endTime.Sub(startTime)
	durationInvalid := false
	if ended.DurationMS != nil {
		const maxDurationMillis int64 = (1<<63 - 1) / int64(time.Millisecond)
		reported := *ended.DurationMS
		switch {
		case reported < 0:
			duration = 0
			durationInvalid = true
		case reported > maxDurationMillis:
			duration = time.Duration(1<<63 - 1)
			durationInvalid = true
		default:
			duration = time.Duration(reported) * time.Millisecond
		}
	}
	status, hasStatus := runnerTransactionStatus(ended.Message)
	unmatched := strings.EqualFold(strings.TrimSpace(ended.Type), "transaction_unmatched") || started.Type == ""
	success := hasStatus && status == 0 && !unmatched && !durationInvalid
	statusCode := 200
	errorMessage := ""
	if !success {
		statusCode = 500
		switch {
		case unmatched:
			errorMessage = fmt.Sprintf("java_vuser transaction %q did not have a matched start/end pair", name)
		case durationInvalid:
			errorMessage = fmt.Sprintf("java_vuser transaction %q returned an invalid duration_ms", name)
		case !hasStatus:
			errorMessage = fmt.Sprintf("java_vuser transaction %q returned an invalid status %q", name, ended.Message)
		default:
			errorMessage = fmt.Sprintf("java_vuser transaction %q failed with status %d", name, status)
		}
	}
	requestID := strings.TrimSpace(ended.RequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(started.RequestID)
	}
	transactionID := strings.TrimSpace(ended.TransactionID)
	if transactionID == "" {
		transactionID = strings.TrimSpace(started.TransactionID)
	}
	parentTransactionID := firstNonEmptyString(ended.ParentTransactionID, started.ParentTransactionID)
	parentName := ""
	target := ""
	if action != nil {
		parentName = action.Name
		target = strings.TrimSpace(action.Target)
	}
	result := &protocol.Result{
		ActionName:          name,
		BusinessTransaction: true,
		SampleKind:          "child",
		ParentActionName:    parentName,
		Group:               name,
		StartTime:           startTime,
		Duration:            duration,
		CorrelationID:       transactionID,
		DependencyType:      javaVuserProtocolName,
		DependencyTarget:    target,
		StatusCode:          statusCode,
		Success:             success,
		ErrorMessage:        errorMessage,
		Extractions:         map[string]string{},
		ProtocolEvidence: map[string]any{
			"protocol":              javaVuserProtocolName,
			"event_contract":        firstNonEmptyString(ended.Contract, started.Contract),
			"runner_request_id":     requestID,
			"transaction_id":        transactionID,
			"parent_transaction_id": parentTransactionID,
		},
	}
	if hasStatus {
		result.ProtocolEvidence["transaction_status"] = status
	}
	if ended.DurationMS != nil {
		result.ProtocolEvidence["reported_duration_ms"] = *ended.DurationMS
	}
	if ended.RawDurationMS != nil {
		result.ProtocolEvidence["raw_duration_ms"] = *ended.RawDurationMS
	}
	if ended.ThinkTimeMS != nil {
		result.ProtocolEvidence["think_time_ms"] = *ended.ThinkTimeMS
	}
	if ended.WastedTimeMS != nil {
		result.ProtocolEvidence["wasted_time_ms"] = *ended.WastedTimeMS
	}
	return result
}

func eventTime(event runnerEvent) time.Time {
	if event.TimestampMS == 0 {
		return time.Time{}
	}
	return time.UnixMilli(event.TimestampMS)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
