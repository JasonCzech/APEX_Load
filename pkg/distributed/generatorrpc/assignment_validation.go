package generatorrpc

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// validatedWorkAssignment is the bounded, normalized transport contract passed
// to the execution coordinator. request remains owned by the synchronous RPC
// call and is read-only for the lifetime of the staging plan.
type validatedWorkAssignment struct {
	request                 *pb.WorkAssignment
	fingerprint             [sha256.Size]byte
	targetDuration          time.Duration
	logicalTargetDuration   time.Duration
	requestedDurationOffset time.Duration
	rampUp                  time.Duration
	rampDown                time.Duration
	startOffset             time.Duration
	preBaseline             time.Duration
	postBaseline            time.Duration
	initStrategy            string
	shardCount              uint32
	rejectionMessage        string
}

// validateWorkAssignment performs only pure, bounded request validation. It
// does not inspect or mutate generator lifecycle state. State-dependent
// generation, capacity, cache, archive, and duplicate decisions belong to the
// execution coordinator.
func validateWorkAssignment(assignment *pb.WorkAssignment, scriptCacheLimit int64, allowSecretDelivery bool) (validatedWorkAssignment, error) {
	if assignment == nil {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "assignment is required")
	}
	if assignment.GetTestId() == "" {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "test_id is required")
	}
	if err := validateArchiveTestID(assignment.GetTestId()); err != nil {
		return validatedWorkAssignment{}, status.Errorf(codes.InvalidArgument, "invalid test_id: %v", err)
	}
	if assignment.GetGroupName() == "" {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "group_name is required")
	}
	if assignment.GetScriptId() == "" {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "script_id is required")
	}
	if err := validateAssignmentMetadataBounds(assignment); err != nil {
		return validatedWorkAssignment{}, err
	}
	if len(assignment.GetSecretBundle()) > 0 && !allowSecretDelivery {
		return validatedWorkAssignment{}, status.Error(codes.FailedPrecondition, "secret-bearing assignments require authenticated TLS (mTLS or verified TLS plus generator auth token)")
	}
	if assignment.GetVuserCount() < 0 {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "vuser_count must not be negative")
	}
	targetDuration, err := validatedAssignmentDuration(assignment.GetTargetDuration(), assignment.GetTargetDurationSeconds(), "target_duration", true)
	if err != nil {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, err.Error())
	}
	requestedDurationOffset, err := validatedAssignmentDuration(assignment.GetDurationOffset(), 0, "duration_offset", true)
	if err != nil {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, err.Error())
	}
	if requestedDurationOffset > 0 {
		if targetDuration <= 0 {
			return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "duration_offset requires a positive target_duration")
		}
		if targetDuration > time.Duration(1<<63-1)-requestedDurationOffset {
			return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "target_duration plus duration_offset overflows the runtime duration")
		}
	}
	rampUp, err := validatedAssignmentDuration(assignment.GetRampUp(), int64(assignment.GetRampUpSeconds()), "ramp_up", true)
	if err != nil {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, err.Error())
	}
	rampDown, err := validatedAssignmentDuration(assignment.GetRampDown(), 0, "ramp_down", true)
	if err != nil {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, err.Error())
	}
	startOffset, err := validatedAssignmentDuration(assignment.GetStartOffset(), 0, "start_offset", true)
	if err != nil {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, err.Error())
	}
	if assignment.GetTerminationMode() != "iterations" && startOffset > targetDuration {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "start_offset begins after the assignment target_duration")
	}
	if mode := assignment.GetTerminationMode(); mode != "" && mode != "duration" && mode != "iterations" {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "unsupported termination_mode")
	}
	if assignment.GetTerminationMode() == "iterations" && (targetDuration != 0 || requestedDurationOffset != 0 || rampDown != 0 || assignment.GetVuserCount() <= 0) {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "iteration termination requires fixed positive users without duration, duration offset, or ramp-down")
	}
	preBaseline, err := validatedAssignmentDuration(assignment.GetPreBaseline(), 0, "pre_baseline", true)
	if err != nil {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, err.Error())
	}
	postBaseline, err := validatedAssignmentDuration(assignment.GetPostBaseline(), 0, "post_baseline", true)
	if err != nil {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, err.Error())
	}
	initStrategy := strings.ToLower(strings.TrimSpace(assignment.GetInitStrategy()))
	if initStrategy == "" {
		initStrategy = "at_runtime"
	}
	if initStrategy != "at_runtime" && initStrategy != "at_once" {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "init_strategy must be at_runtime or at_once")
	}
	shardCount := assignment.GetShardCount()
	if shardCount == 0 {
		if assignment.GetShardOrdinal() != 0 {
			return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "shard_ordinal requires a positive shard_count")
		}
		shardCount = 1
	}
	if assignment.GetShardOrdinal() >= shardCount {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "shard_ordinal must be smaller than shard_count")
	}
	maxInt := uint64(^uint(0) >> 1)
	namespaceSize := uint64(maxGeneratorVUsers)
	if assignment.GetVuserIdOffset() > maxInt || namespaceSize-1 > maxInt-assignment.GetVuserIdOffset() {
		return validatedWorkAssignment{}, status.Error(codes.InvalidArgument, "vuser_id_offset namespace overflows runtime int")
	}

	ancillaryBytes := int64(proto.Size(assignment) - len(assignment.GetScriptContent()))
	ancillaryLimit := generatorRPCAssignmentAncillaryLimit(scriptCacheLimit)
	secretEnvelopeBytes := int64(proto.Size(&pb.WorkAssignment{
		SecretBundle:   assignment.GetSecretBundle(),
		SecretBundleId: assignment.GetSecretBundleId(),
	}))
	if ancillaryLimit <= int64(^uint64(0)>>1)-secretEnvelopeBytes {
		ancillaryLimit += secretEnvelopeBytes
	}
	rejectionMessage := ""
	if ancillaryBytes > ancillaryLimit {
		rejectionMessage = fmt.Sprintf("assignment data and metadata are %d bytes and exceed limit %d", ancillaryBytes, ancillaryLimit)
		if len(assignment.GetSecretBundle()) > 0 {
			// Do not turn an aggregate protobuf-size error into a credential-length
			// oracle for the write-only secret envelope.
			rejectionMessage = "assignment data and metadata exceed the runtime limit"
		}
	}

	return validatedWorkAssignment{
		request:                 assignment,
		fingerprint:             workAssignmentFingerprint(assignment),
		targetDuration:          targetDuration,
		logicalTargetDuration:   targetDuration + requestedDurationOffset,
		requestedDurationOffset: requestedDurationOffset,
		rampUp:                  rampUp,
		rampDown:                rampDown,
		startOffset:             startOffset,
		preBaseline:             preBaseline,
		postBaseline:            postBaseline,
		initStrategy:            initStrategy,
		shardCount:              shardCount,
		rejectionMessage:        rejectionMessage,
	}, nil
}
