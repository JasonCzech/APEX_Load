package collectoragent

import (
	"context"
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

type serviceBusManagementObserver struct{ dial serviceBusManagementDialFunc }

func (serviceBusManagementObserver) prepareTarget(target *ManagementTarget) error {
	return prepareServiceBusManagementTarget(target)
}
func (serviceBusManagementObserver) validateTarget(target *ManagementTarget) error {
	return validateServiceBusManagementTarget(target)
}

func (observer serviceBusManagementObserver) observe(ctx context.Context, poll *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
	if err := validateServiceBusManagementTarget(&poll.target); err != nil {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	if !poll.credentials.MatchesIdentity(poll.target.Identity) || poll.credentials.SharedKey == nil {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	started := time.Now()
	rows := make([]analysis.MessagingManagementRow, len(poll.target.Scope.Entities))
	captured := make([]time.Time, len(rows))
	for i, entity := range poll.target.Scope.Entities {
		rows[i] = serviceBusManagementUnavailableRow(entity, "not_observed")
	}
	bytesRead := 0
	for i, entity := range poll.target.Scope.Entities {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		requests, _ := poll.counts()
		deadline, hasDeadline := ctx.Deadline()
		// Inventory capacity is not a promise that every entity fits the shared
		// time/activity/body budget. Reserve the three counted setup/GET operations
		// and enough remaining lifetime to create the short purpose-bound SAS.
		if requests+3 > 512 || bytesRead >= serviceBusManagementPollBodyLimit || hasDeadline && time.Until(deadline) < 2*time.Second {
			for j := i; j < len(rows); j++ {
				rows[j] = serviceBusManagementUnavailableRow(rows[j].Entity, "budget_exhausted")
			}
			break
		}
		route, err := serviceBusRouteForEntity(&poll.target, entity)
		if err != nil {
			rows[i] = serviceBusManagementUnavailableRow(entity, "permission_denied")
			continue
		}
		properties, observed, readBytes, err := observer.readEntity(ctx, poll, route, min(serviceBusManagementBodyLimit, serviceBusManagementPollBodyLimit-bytesRead))
		bytesRead += readBytes
		if err != nil {
			var unproven *serviceBusManagementUnprovenCloseError
			if errors.As(err, &unproven) {
				// A retained, unresolved physical transport cannot be followed by
				// further target admission or by publication/completion confirmation.
				return nil, err
			}
			rows[i] = serviceBusManagementUnavailableRow(entity, kafkaManagementErrorReason(err))
			continue
		}
		rows[i].ServiceBusRuntime = &properties
		if serviceBusManagementHasObservation(properties) {
			captured[i] = observed
			rows[i].State, rows[i].Reason = analysis.ManagementStateObserved, ""
		}
	}
	if err := managementContextError(ctx); err != nil {
		return nil, err
	}
	requests, _ := poll.counts()
	if requests > 0 && !minTime(poll.grant.Deadline, poll.grant.AuthorizationExpiresAt).After(time.Now()) {
		return nil, context.DeadlineExceeded
	}
	completed := time.Now()
	snapshot := poll.base(started, completed, completed.Sub(started), managementMinimumInterval("servicebus-runtime", poll.target), 0)
	snapshot.Rows = rows
	snapshot.InventoryComplete = true
	snapshot.RequestCount, snapshot.PageCount = poll.counts()
	for i := range snapshot.Rows {
		row := &snapshot.Rows[i]
		if !captured[i].IsZero() {
			observed := captured[i].UTC()
			age := max(int64(0), completed.Sub(captured[i]).Milliseconds())
			row.ObservedAt, row.ObservationAgeMS = &observed, &age
		} else {
			snapshot.InventoryComplete = false
			snapshot.Incomplete = true
		}
		properties := row.ServiceBusRuntime
		expected := []analysis.ManagementInteger{properties.Total, properties.Active, properties.DeadLetter, properties.Transfer, properties.TransferDeadLetter}
		if row.Entity.ServiceBus.Queue != "" {
			expected = append(expected, properties.Scheduled, properties.SizeBytes)
		}
		for _, field := range expected {
			if field.State != analysis.ManagementStateObserved {
				snapshot.Incomplete = true
			}
		}
	}
	if err := snapshot.ValidateFor(poll.target.Scope, poll.target.Identity); err != nil {
		return nil, &managementObservationError{reason: "invalid"}
	}
	return &snapshot, nil
}

func (observer serviceBusManagementObserver) readEntity(ctx context.Context, poll *managementObservation, route serviceBusManagementRoute, bodyLimit int) (properties analysis.ServiceBusRuntime, captured time.Time, readBytes int, err error) {
	if bodyLimit < 0 || bodyLimit > serviceBusManagementBodyLimit {
		return properties, captured, readBytes, &managementObservationError{reason: "budget_exhausted"}
	}
	approved, approvalErr := serviceBusRouteForEntity(&poll.target, route.entity)
	if approvalErr != nil || approved.origin != route.origin || approved.serverName != route.serverName || approved.network != route.network || approved.address != route.address || approved.resourceURI != route.resourceURI || approved.requestURL != route.requestURL || approved.requestPath != route.requestPath || approved.subscription != route.subscription {
		return properties, captured, readBytes, &managementObservationError{reason: "permission_denied"}
	}
	if poll.credentials.SharedKey == nil {
		return properties, captured, readBytes, &managementObservationError{reason: "permission_denied"}
	}
	// Reject insufficient signed authority before counting or acquiring TCP/TLS.
	// Grant authorization can expire before its scheduling deadline. All actual
	// I/O is therefore bound to the earliest signed/local deadline as well.
	expiry := minTime(poll.grant.AuthorizationExpiresAt, poll.grant.Deadline)
	if deadline, ok := ctx.Deadline(); ok {
		expiry = minTime(expiry, deadline)
	}
	if _, authErr := serviceBusSASAuthorization(*poll.credentials.SharedKey, route.resourceURI, time.Now(), expiry); authErr != nil {
		return properties, captured, readBytes, authErr
	}
	ctx, cancel := context.WithDeadline(ctx, expiry)
	defer cancel()
	connection, err := openServiceBusManagementConnection(ctx, poll, route, observer.dial)
	if connection != nil {
		defer func() {
			closeErr := connection.close()
			var unproven *serviceBusManagementUnprovenCloseError
			if errors.As(closeErr, &unproven) || closeErr != nil && err == nil {
				// Never hide an unproven cleanup behind a prior operation error.
				err = closeErr
			}
		}()
	}
	if err != nil {
		return properties, captured, readBytes, err
	}
	err = poll.request(ctx, route.origin, false, func(opCtx context.Context) error {
		return connection.withIO(opCtx, func(context.Context) error {
			// Socket I/O uses owned deadlines/cancellation. XML validation uses the
			// actual cancellable operation context; no detached parser is launched.
			now := time.Now()
			expiry := minTime(poll.grant.AuthorizationExpiresAt, poll.grant.Deadline)
			if operationDeadline, ok := opCtx.Deadline(); ok {
				expiry = minTime(expiry, operationDeadline)
			}
			authorization, authErr := serviceBusSASAuthorization(*poll.credentials.SharedKey, route.resourceURI, now, expiry)
			if authErr != nil {
				return authErr
			}
			if writeErr := writeServiceBusManagementGET(connection.tls, route, authorization); writeErr != nil {
				return writeErr
			}
			response, readErr := readServiceBusManagementHTTPResponse(opCtx, connection.tls, bodyLimit)
			defer clear(response.body)
			readBytes = len(response.body)
			if readErr != nil {
				return readErr
			}
			if statusErr := serviceBusManagementStatusError(response.status); statusErr != nil {
				return statusErr
			}
			var parseErr error
			properties, parseErr = decodeServiceBusManagementDescription(opCtx, response.body, route)
			if parseErr != nil {
				return parseErr
			}
			if err := opCtx.Err(); err != nil {
				return err
			}
			captured = time.Now()
			return nil
		})
	})
	return properties, captured, readBytes, err
}
func serviceBusManagementStatusError(status int) error {
	reason := "invalid"
	switch {
	case status == 200:
		return nil
	case status == 401 || status == 403 || status >= 300 && status < 400:
		reason = "permission_denied"
	case status == 404 || status == 410 || status == 503:
		reason = "not_observed"
	case status == 408 || status == 504:
		reason = "timeout"
	case status == 429:
		reason = "throttled"
	}
	return &managementObservationError{reason: reason}
}
func serviceBusManagementUnavailableRow(entity analysis.ManagementEntity, reason string) analysis.MessagingManagementRow {
	missing := kafkaManagementUnavailableInteger(reason)
	runtime := &analysis.ServiceBusRuntime{Total: missing.Clone(), Active: missing.Clone(), Scheduled: missing.Clone(), DeadLetter: missing.Clone(), Transfer: missing.Clone(), TransferDeadLetter: missing.Clone(), SizeBytes: missing.Clone(), Deferred: analysis.ManagementInteger{State: analysis.ManagementStateUnsupported, Reason: "unsupported"}}
	if entity.ServiceBus.Subscription != "" {
		runtime.Scheduled = analysis.ManagementInteger{State: analysis.ManagementStateUnsupported, Reason: "unsupported"}
		runtime.SizeBytes = analysis.ManagementInteger{State: analysis.ManagementStateUnsupported, Reason: "unsupported"}
	}
	return analysis.MessagingManagementRow{Entity: entity.Clone(), State: missing.State, Reason: reason, ServiceBusRuntime: runtime}
}
func serviceBusManagementHasObservation(runtime analysis.ServiceBusRuntime) bool {
	for _, field := range []analysis.ManagementInteger{runtime.Total, runtime.Active, runtime.Scheduled, runtime.DeadLetter, runtime.Transfer, runtime.TransferDeadLetter, runtime.SizeBytes} {
		if field.State == analysis.ManagementStateObserved {
			return true
		}
	}
	return false
}

var _ managementObserver = serviceBusManagementObserver{}
