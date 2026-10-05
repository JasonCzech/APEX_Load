package generatorrpc

import (
	"errors"
	"fmt"
	"strings"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// FeatureMessagingRuntimeQuiescenceV1 is the strict pause request/ack contract.
// It is independent of ordinary messaging execution and must not gate Start.
const FeatureMessagingRuntimeQuiescenceV1 = "messaging_runtime_quiescence_v1"

// ErrPauseQuiescenceUnconfirmed means a required strict pause acknowledgement
// did not prove the requested, committed runtime pause boundary. The result can
// still contain an applied operation and must be retained for compensation.
var ErrPauseQuiescenceUnconfirmed = errors.New("generator pause quiescence is unconfirmed")

func validateStrictPauseRequest(control PauseControl) error {
	if !control.RequireQuiescence {
		return nil
	}
	if !control.Pause {
		return fmt.Errorf("quiescence can only be required for pause")
	}
	if control.OperationID == "" || strings.TrimSpace(control.OperationID) != control.OperationID || control.OperationSequence == 0 {
		return fmt.Errorf("strict pause requires an operation ID and sequence")
	}
	return nil
}

func pauseResultFromProto(ack *pb.PauseAck) PauseResult {
	result := PauseResult{
		Accepted:                 ack.GetAccepted(),
		CurrentPaused:            ack.GetCurrentPaused(),
		AppliedOperationID:       ack.GetAppliedOperationId(),
		AppliedOperationSequence: ack.GetAppliedOperationSequence(),
		ErrorMessage:             ack.GetErrorMessage(),
	}
	if ack != nil && ack.QuiescenceConfirmed != nil {
		confirmed := *ack.QuiescenceConfirmed
		result.QuiescenceConfirmed = &confirmed
	}
	return result
}

// ValidatePauseAcknowledgement preserves the legacy acknowledgement contract
// for ordinary controls and rejects an absent, false or mismatched strict proof.
// Controllers also use this check with alternative transports/test clients.
func ValidatePauseAcknowledgement(control PauseControl, result PauseResult) error {
	if err := validateStrictPauseRequest(control); err != nil {
		return err
	}
	if !control.RequireQuiescence {
		return nil
	}
	if !result.Accepted || !result.CurrentPaused ||
		result.AppliedOperationID != control.OperationID ||
		result.AppliedOperationSequence != control.OperationSequence ||
		result.ErrorMessage != "" {
		return fmt.Errorf("%w: acknowledgement does not match the accepted pause operation", ErrPauseQuiescenceUnconfirmed)
	}
	if result.QuiescenceConfirmed == nil {
		return fmt.Errorf("%w: peer omitted required proof", ErrPauseQuiescenceUnconfirmed)
	}
	if !*result.QuiescenceConfirmed {
		return fmt.Errorf("%w: peer did not confirm runtime quiescence", ErrPauseQuiescenceUnconfirmed)
	}
	return nil
}

func messagingRuntimeQuiescenceAvailable(instance protocol.Protocol, available bool) bool {
	if !available {
		return false
	}
	supported, err := protocol.ReadRuntimePauseSupport(instance)
	return err == nil && supported
}
