package generatorrpc

import (
	"errors"

	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// InitialRampInProgressReason identifies a scale request that reached a
// generator after execution started but before its engine finished launching
// the configured initial VUsers. Controllers use the structured ErrorInfo
// detail instead of depending on the human-readable status message.
const InitialRampInProgressReason = "INITIAL_RAMP_IN_PROGRESS"

const initialRampInProgressErrorDomain = "apexload.generator"

func initialRampInProgressStatusError() error {
	base := status.New(codes.FailedPrecondition, engine.ErrInitialRampInProgress.Error())
	detailed, err := base.WithDetails(&errdetails.ErrorInfo{
		Reason: InitialRampInProgressReason,
		Domain: initialRampInProgressErrorDomain,
	})
	if err != nil {
		return base.Err()
	}
	return detailed.Err()
}

func isInitialRampInProgressStatus(err error) bool {
	if err == nil || status.Code(err) != codes.FailedPrecondition {
		return false
	}
	state, ok := status.FromError(err)
	if !ok {
		return false
	}
	for _, detail := range state.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if ok && info.GetReason() == InitialRampInProgressReason && info.GetDomain() == initialRampInProgressErrorDomain {
			return true
		}
	}
	return false
}

// initialRampInProgressRPCError preserves the original gRPC status for callers
// that inspect transport metadata while also exposing the engine sentinel used
// by controller and HTTP error classification.
type initialRampInProgressRPCError struct {
	cause error
}

func (err *initialRampInProgressRPCError) Error() string {
	if err == nil || err.cause == nil {
		return engine.ErrInitialRampInProgress.Error()
	}
	return err.cause.Error()
}

func (err *initialRampInProgressRPCError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

func (err *initialRampInProgressRPCError) Is(target error) bool {
	return target == engine.ErrInitialRampInProgress || errors.Is(err.cause, target)
}
