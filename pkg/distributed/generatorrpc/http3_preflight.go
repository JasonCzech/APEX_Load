package generatorrpc

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FeatureHTTP3PreflightV1 is advertised by generators that can run the
// scenario HTTP/3 preflight (ProbeHTTP3) for staged work.
const FeatureHTTP3PreflightV1 = "http3_preflight_v1"

// maxHTTP3ProbeDuration bounds one ProbeHTTP3 call when the caller sets no
// earlier deadline, so a staged reservation is never held by a slow probe.
const maxHTTP3ProbeDuration = 30 * time.Second

// ProbeHTTP3 checks the HTTP/3 origins of every group staged for the test
// with one QUIC handshake each. It runs only before commit, reads no VUser
// state and sends no request.
func (s *Service) ProbeHTTP3(ctx context.Context, req *pb.HTTP3ProbeRequest) (*pb.HTTP3ProbeResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if req == nil || strings.TrimSpace(req.GetTestId()) == "" || len(req.GetTestId()) > 512 {
		return nil, status.Error(codes.InvalidArgument, "test identity required")
	}
	if err := http3ExecutionError(); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	groups, err := s.stagedHTTP3ProbeGroups(req.GetTestId(), req.GetExecutionGeneration())
	if err != nil {
		return nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, maxHTTP3ProbeDuration)
	defer cancel()
	response := &pb.HTTP3ProbeResponse{}
	for _, group := range groups {
		if len(response.Results) >= maxHTTP3ProbeResults {
			break
		}
		for _, result := range engine.ProbeHTTP3Group(probeCtx, group) {
			response.Results = append(response.Results, http3ProbeResultToProto(result))
		}
	}
	if len(response.Results) > maxHTTP3ProbeResults {
		response.Results = response.Results[:maxHTTP3ProbeResults]
	}
	return response, nil
}

// stagedHTTP3ProbeGroups copies the staged, uncommitted group configs of the
// test at the given generation, in name order.
func (s *Service) stagedHTTP3ProbeGroups(testID string, generation uint64) ([]engine.GroupConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.draining || !s.executionEnabled {
		return nil, status.Error(codes.FailedPrecondition, "generator cannot probe staged work")
	}
	if err := s.validateMutationGenerationLocked(generation); err != nil {
		return nil, err
	}
	assignment, exists := s.assignments[testID]
	if !exists || assignment.executionGeneration != generation || assignment.committed {
		return nil, status.Error(codes.FailedPrecondition, "staged assignment not available")
	}
	executions := s.executions[testID]
	names := make([]string, 0, len(executions))
	for name := range executions {
		names = append(names, name)
	}
	sort.Strings(names)
	groups := make([]engine.GroupConfig, 0, len(names))
	for _, name := range names {
		execution := executions[name]
		if execution == nil || execution.stagedConfig == nil || execution.started.Load() || execution.executionGeneration != generation {
			return nil, status.Error(codes.FailedPrecondition, "staged group not available")
		}
		groups = append(groups, execution.stagedConfig.Groups...)
	}
	return groups, nil
}

func http3ProbeResultToProto(result engine.HTTP3ProbeResult) *pb.HTTP3ProbeResult {
	out := &pb.HTTP3ProbeResult{
		Group: result.Group, Origin: result.Origin, Policy: result.Policy, Ok: result.OK,
		ErrorClass: result.ErrorClass, Error: result.Error, Address: result.Address,
		HandshakeUs: int64(result.HandshakeMs * 1000), Skipped: result.Skipped,
	}
	if !result.CheckedAt.IsZero() {
		out.CheckedAtUnixNano = result.CheckedAt.UnixNano()
	}
	return out
}

// HTTP3ProbeResultFromProto restores a generator's probe result.
func HTTP3ProbeResultFromProto(result *pb.HTTP3ProbeResult) engine.HTTP3ProbeResult {
	out := engine.HTTP3ProbeResult{
		Group: result.GetGroup(), Origin: result.GetOrigin(), Policy: result.GetPolicy(), OK: result.GetOk(),
		ErrorClass: result.GetErrorClass(), Error: result.GetError(), Address: result.GetAddress(),
		HandshakeMs: float64(result.GetHandshakeUs()) / 1000, Skipped: result.GetSkipped(),
	}
	if nanos := result.GetCheckedAtUnixNano(); nanos > 0 {
		out.CheckedAt = time.Unix(0, nanos).UTC()
	}
	return out
}

// ProbeHTTP3 asks the generator to check the HTTP/3 origins of a staged test.
func (c *Client) ProbeHTTP3(ctx context.Context, testID string, generation uint64) ([]engine.HTTP3ProbeResult, error) {
	response, err := c.rpc.ProbeHTTP3(ctx, &pb.HTTP3ProbeRequest{TestId: testID, ExecutionGeneration: generation})
	if err != nil {
		return nil, err
	}
	if len(response.GetResults()) > maxHTTP3ProbeResults {
		return nil, fmt.Errorf("generator returned %d HTTP/3 preflight results", len(response.GetResults()))
	}
	results := make([]engine.HTTP3ProbeResult, 0, len(response.GetResults()))
	for _, result := range response.GetResults() {
		results = append(results, HTTP3ProbeResultFromProto(result))
	}
	return results, nil
}

// maxHTTP3ProbeResults bounds one generator's response, well inside the
// gRPC message limit; groups beyond it are not probed. The controller
// stores at most 512 results per run.
const maxHTTP3ProbeResults = 2048
