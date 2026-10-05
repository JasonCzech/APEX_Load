package collectoragent

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

const solaceManagementCleanupWindow = 2 * time.Second
const solaceManagementLogoutBodyLimit = 4 << 10

type solaceManagementObserver struct{ dial solaceManagementDialFunc }

func (solaceManagementObserver) prepareTarget(target *ManagementTarget) error {
	return prepareSolaceManagementTarget(target)
}
func (solaceManagementObserver) validateTarget(target *ManagementTarget) error {
	return validateSolaceManagementTarget(target)
}

func (observer solaceManagementObserver) observe(ctx context.Context, poll *managementObservation) (*analysis.MessagingManagementSnapshot, error) {
	if validateSolaceManagementTarget(&poll.target) != nil {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	basic, err := solaceBasicAuthorization(poll.credentials, poll.target.Identity)
	if err != nil {
		return nil, err
	}
	defer clear(basic)
	logout, err := solaceLogoutRoute(&poll.target)
	if err != nil {
		return nil, err
	}
	state, err := solaceManagementState(poll, logout.origin)
	if err != nil {
		return nil, err
	}
	authority, err := solaceManagementAuthority(ctx, poll)
	if err != nil {
		return nil, err
	}
	// Reserve logout inside the same signed authority. Insufficient lifetime
	// fails before a durable intent, socket, TLS handshake or Basic bytes.
	if time.Until(authority) < 2*solaceManagementCleanupWindow {
		return nil, &managementObservationError{reason: "budget_exhausted"}
	}
	workCtx, cancel := context.WithDeadline(ctx, authority.Add(-solaceManagementCleanupWindow))
	defer cancel()
	started := time.Now()
	rows := make([]analysis.MessagingManagementRow, len(poll.target.Scope.Entities))
	captured := make([]time.Time, len(rows))
	for i, entity := range poll.target.Scope.Entities {
		rows[i] = solaceManagementUnavailableRow(entity, "not_observed")
	}
	order := solaceManagementEntityOrder(poll.target.Scope.Entities, poll.grant.PollSequence)
	if err = state.begin(poll, logout.origin); err != nil {
		return nil, err
	}
	// A panic or callback failure may not erase a possibly created remote
	// session. Only the no-Basic case can be unwound without provider proof.
	defer func() {
		if !state.hasAuthenticationSent() {
			_ = state.abandonBeforeAuthentication()
		}
	}()
	bytesRead := 0
	var workErr error
	physicalUnproven := false
	for position, index := range order {
		if solaceManagementContextError(workCtx) != nil {
			if solaceManagementWorkCutoff(ctx, workCtx, authority) {
				for _, remaining := range order[position:] {
					rows[remaining] = solaceManagementUnavailableRow(rows[remaining].Entity, "budget_exhausted")
				}
			} else {
				workErr = solaceManagementContextError(workCtx)
				if ctx.Err() != nil {
					workErr = ctx.Err()
				}
			}
			break
		}
		requests, _ := poll.counts()
		if requests+6 > 512 || bytesRead >= solaceManagementPollBodyLimit-solaceManagementLogoutBodyLimit {
			for _, remaining := range order[position:] {
				rows[remaining] = solaceManagementUnavailableRow(rows[remaining].Entity, "budget_exhausted")
			}
			break
		}
		entity := rows[index].Entity
		route, routeErr := solaceRouteForEntity(&poll.target, entity)
		if routeErr != nil {
			rows[index] = solaceManagementUnavailableRow(entity, "permission_denied")
			continue
		}
		authentication := state.cookieCopy()
		first := !state.hasAuthenticationSent()
		if first {
			authentication = append([]byte(nil), basic...)
		}
		if !first && len(authentication) == 0 {
			workErr = &managementObservationError{reason: "not_observed"}
			break
		}
		properties, observed, used, readErr := observer.readEntity(workCtx, poll, state, route, authentication, first, min(solaceManagementBodyLimit, solaceManagementPollBodyLimit-solaceManagementLogoutBodyLimit-bytesRead))
		clear(authentication)
		bytesRead += used
		if readErr != nil {
			var unproven *solaceManagementUnprovenCloseError
			if errors.As(readErr, &unproven) {
				physicalUnproven = true
				workErr = readErr
				break
			}
			reason := kafkaManagementErrorReason(readErr)
			if solaceManagementWorkCutoff(ctx, workCtx, authority) {
				reason = "budget_exhausted"
			}
			rows[index] = solaceManagementUnavailableRow(entity, reason)
			if !state.canLogout() && state.hasAuthenticationSent() {
				workErr = readErr
				break
			}
			continue
		}
		rows[index].SolaceSEMP = &properties
		if properties.Depth.State == analysis.ManagementStateObserved {
			captured[index] = observed
			rows[index].State, rows[index].Reason = analysis.ManagementStateObserved, ""
		}
	}
	if physicalUnproven {
		return nil, workErr
	}
	if !state.hasAuthenticationSent() {
		if err = state.abandonBeforeAuthentication(); err != nil {
			return nil, err
		}
	} else {
		if !state.canLogout() {
			return nil, &managementObservationError{reason: "not_observed"}
		}
		// Cancellation is not new authority. This detached cleanup receives a
		// bounded deadline no later than the original signed/local poll expiry.
		cleanupCtx, cleanupCancel := context.WithDeadline(context.WithoutCancel(ctx), minTime(authority, time.Now().Add(solaceManagementCleanupWindow)))
		cleanupErr := observer.logout(cleanupCtx, poll, state, logout)
		cleanupCancel()
		if cleanupErr != nil {
			return nil, cleanupErr
		}
	}
	if workErr != nil {
		return nil, workErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !authority.After(time.Now()) {
		return nil, context.DeadlineExceeded
	}
	completed := time.Now()
	snapshot := poll.base(started, completed, completed.Sub(started), managementMinimumInterval("solace-semp", poll.target), 0)
	snapshot.Rows = rows
	snapshot.InventoryComplete = true
	snapshot.RequestCount, snapshot.PageCount = poll.counts()
	for i := range snapshot.Rows {
		row := &snapshot.Rows[i]
		if captured[i].IsZero() {
			snapshot.InventoryComplete = false
			snapshot.Incomplete = true
			continue
		}
		at := captured[i].UTC()
		age := max(int64(0), completed.Sub(captured[i]).Milliseconds())
		row.ObservedAt, row.ObservationAgeMS = &at, &age
	}
	if snapshot.ValidateFor(poll.target.Scope, poll.target.Identity) != nil {
		return nil, &managementObservationError{reason: "invalid"}
	}
	return &snapshot, nil
}

func solaceManagementWorkCutoff(parent, work context.Context, authority time.Time) bool {
	return errors.Is(solaceManagementContextError(work), context.DeadlineExceeded) && solaceManagementContextError(parent) == nil && authority.After(time.Now())
}

func solaceManagementAuthority(ctx context.Context, poll *managementObservation) (time.Time, error) {
	if poll.grant.Deadline.IsZero() || poll.grant.AuthorizationExpiresAt.IsZero() {
		return time.Time{}, &managementObservationError{reason: "permission_denied"}
	}
	deadline := minTime(poll.grant.Deadline, poll.grant.AuthorizationExpiresAt)
	if local, ok := ctx.Deadline(); ok {
		deadline = minTime(deadline, local)
	} else {
		return time.Time{}, &managementObservationError{reason: "permission_denied"}
	}
	if ctx.Err() != nil {
		return time.Time{}, ctx.Err()
	}
	if !deadline.After(time.Now()) {
		return time.Time{}, context.DeadlineExceeded
	}
	return deadline, nil
}

func solaceManagementEntityOrder(entities []analysis.ManagementEntity, sequence string) []int {
	order := make([]int, len(entities))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { a, _ := entities[order[i]].ID(); b, _ := entities[order[j]].ID(); return a < b })
	if len(order) == 0 {
		return order
	}
	number, _ := strconv.ParseUint(sequence, 10, 64)
	start := int(number % uint64(len(order)))
	return append(append(make([]int, 0, len(order)), order[start:]...), order[:start]...)
}

func (observer solaceManagementObserver) readEntity(ctx context.Context, poll *managementObservation, state *solaceManagementOriginState, route solaceManagementRoute, authentication []byte, first bool, bodyLimit int) (properties analysis.SolaceSEMP, captured time.Time, readBytes int, err error) {
	approved, approvalErr := solaceRouteForEntity(&poll.target, route.entity)
	if approvalErr != nil || !solaceSameRoute(approved, route) {
		return properties, captured, 0, &managementObservationError{reason: "permission_denied"}
	}
	response, err := observer.exchange(ctx, poll, state, route, false, authentication, first, bodyLimit)
	defer clear(response.body)
	readBytes = len(response.body)
	if err != nil {
		return properties, captured, readBytes, err
	}
	if response.status != 200 {
		return properties, captured, readBytes, solaceManagementResponseError(ctx, response, "GET")
	}
	properties, err = decodeSolaceManagementDescription(ctx, response.body, route, poll.target.Scope)
	if err != nil {
		return properties, captured, readBytes, err
	}
	if ctx.Err() != nil {
		return properties, captured, readBytes, ctx.Err()
	}
	if response.receivedAt.IsZero() {
		return properties, captured, readBytes, &managementObservationError{reason: "invalid"}
	}
	captured = response.receivedAt
	return properties, captured, readBytes, nil
}

func (observer solaceManagementObserver) logout(ctx context.Context, poll *managementObservation, state *solaceManagementOriginState, route solaceManagementRoute) error {
	approved, err := solaceLogoutRoute(&poll.target)
	if err != nil || !solaceSameRoute(approved, route) || !state.canLogout() {
		return &managementObservationError{reason: "permission_denied"}
	}
	cookie := state.cookieCopy()
	defer clear(cookie)
	response, err := observer.exchange(ctx, poll, state, route, true, cookie, false, solaceManagementLogoutBodyLimit)
	defer clear(response.body)
	if err != nil {
		return err
	}
	if response.status != 200 {
		return solaceManagementResponseError(ctx, response, "PUT")
	}
	if err = decodeSolaceManagementLogout(ctx, response.body); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// exchange returns only after all local socket/watchers physically join;
	// provider meta is independent session proof, and durable clear is last.
	return state.confirmOwnSessionLogout()
}

func (observer solaceManagementObserver) exchange(ctx context.Context, poll *managementObservation, state *solaceManagementOriginState, route solaceManagementRoute, logout bool, authentication []byte, first bool, bodyLimit int) (response solaceManagementHTTPResponse, err error) {
	if bodyLimit < 0 || bodyLimit > solaceManagementBodyLimit {
		return response, &managementObservationError{reason: "budget_exhausted"}
	}
	if err = validateSolaceManagementRequest(route, logout, authentication); err != nil {
		return response, err
	}
	if first != bytes.HasPrefix(authentication, []byte("Basic ")) || logout && first {
		return response, &managementObservationError{reason: "permission_denied"}
	}
	if _, err = solaceManagementAuthority(ctx, poll); err != nil {
		return response, err
	}
	connection, err := openSolaceManagementConnection(ctx, poll, route, observer.dial)
	if connection != nil {
		defer func() {
			closeErr := connection.close()
			var unproven *solaceManagementUnprovenCloseError
			if errors.As(closeErr, &unproven) || closeErr != nil && err == nil {
				err = closeErr
			}
		}()
	}
	if err != nil {
		return response, err
	}
	if err = state.waitHTTP(ctx); err != nil {
		return response, err
	}
	err = poll.request(ctx, route.origin, false, func(opCtx context.Context) error {
		return connection.withIO(opCtx, func(context.Context) error {
			if first {
				state.markAuthenticationSent()
			}
			if writeErr := writeSolaceManagementRequest(connection.tls, route, logout, authentication); writeErr != nil {
				return writeErr
			}
			var readErr error
			response, readErr = readSolaceManagementHTTPResponse(opCtx, connection.tls, bodyLimit, route, logout)
			if readErr == nil {
				response.receivedAt = time.Now()
			}
			defer func() { clear(response.session); response.session = nil }()
			if response.sessionUncertain {
				state.markSessionUncertain()
			}
			if !logout && len(response.session) > 0 {
				if cookieErr := state.observeCookie(response.session); cookieErr != nil {
					state.markSessionUncertain()
					return cookieErr
				}
			}
			return readErr
		})
	})
	return response, err
}

func solaceManagementResponseError(ctx context.Context, response solaceManagementHTTPResponse, method string) error {
	// Qualify pinned numeric provider errors without exposing their text. The
	// HTTP status still wins for throttling, redirects and authentication.
	if response.status == 429 || response.status == 529 || response.status == 401 || response.status == 403 || response.status >= 300 && response.status < 400 {
		return solaceManagementStatusError(response.status)
	}
	if root, err := decodeSolaceManagementDocument(ctx, response.body); err == nil {
		if err = validateSolaceManagementMeta(root, method); err != nil {
			var typed *managementObservationError
			if errors.As(err, &typed) && (typed.reason == "not_observed" || typed.reason == "unsupported" || typed.reason == "permission_denied") {
				return err
			}
		}
	}
	return solaceManagementStatusError(response.status)
}
func solaceManagementStatusError(status int) error {
	reason := "invalid"
	switch {
	case status == 401 || status == 403 || status >= 300 && status < 400:
		reason = "permission_denied"
	case status == 404 || status == 410 || status == 503:
		reason = "not_observed"
	case status == 408 || status == 504:
		reason = "timeout"
	case status == 429 || status == 529:
		reason = "throttled"
	}
	return &managementObservationError{reason: reason}
}
func solaceManagementUnavailableRow(entity analysis.ManagementEntity, reason string) analysis.MessagingManagementRow {
	missing := kafkaManagementUnavailableInteger(reason)
	return analysis.MessagingManagementRow{Entity: entity.Clone(), State: missing.State, Reason: reason, SolaceSEMP: &analysis.SolaceSEMP{Depth: missing}}
}

var _ managementObserver = solaceManagementObserver{}
