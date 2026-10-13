package generatorrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"sync"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/livetable"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const FeatureLiveTablesV1 = "live_tables_v1"
const liveTableFrameBytes = (1 << 20) - 4096
const liveTablePendingBytes = 4 << 20
const liveTablePendingOps = 64
const liveTableControlReserve = 8
const liveTableLeaseControlReserve = liveTableControlReserve / 2
const liveTableLeaseControlBytes = 8 << 10
const liveTableLeaseReservedBytes = liveTableLeaseControlReserve * liveTableLeaseControlBytes

// Waiting consumers, producers, and lease settlement have independent bounded
// shares. Producers can wake consumers without consuming the count or bytes
// needed to settle work already leased. Callers synchronize admission/release.
type liveTableAdmission struct {
	bytes, ordinary, producers, leaseControls, nonLeaseBytes int
}

func (b *liveTableAdmission) admit(operation livetable.Operation, size int) bool {
	leaseControl := liveTableLeaseControlOperation(operation)
	if size > liveTablePendingBytes-b.bytes {
		return false
	}
	if leaseControl {
		// Engine-generated controls contain only bounded identities and timing.
		// Bound their frames too, so padding cannot consume another lease's share.
		if size > liveTableLeaseControlBytes || b.leaseControls >= liveTableLeaseControlReserve {
			return false
		}
		b.leaseControls++
	} else {
		if size > liveTablePendingBytes-liveTableLeaseReservedBytes-b.nonLeaseBytes {
			return false
		}
		if operation == livetable.OpPush {
			if b.producers >= liveTableControlReserve-liveTableLeaseControlReserve {
				return false
			}
			b.producers++
		} else {
			if b.ordinary >= liveTablePendingOps-liveTableControlReserve {
				return false
			}
			b.ordinary++
		}
		b.nonLeaseBytes += size
	}
	b.bytes += size
	return true
}

func (b *liveTableAdmission) release(operation livetable.Operation, size int) {
	b.bytes -= size
	if liveTableLeaseControlOperation(operation) {
		b.leaseControls--
	} else {
		b.nonLeaseBytes -= size
		if operation == livetable.OpPush {
			b.producers--
		} else {
			b.ordinary--
		}
	}
}

type liveTablePending struct {
	frame        *pb.LiveTableRequest
	result       chan liveTableResponse
	deadline     time.Time
	sent         uint64
	control      bool
	leaseControl bool
}
type liveTableResponse struct {
	result livetable.Result
	err    error
}

// One proxy belongs to one immutable group assignment. Reattaching a controller
// stream replays pending request identities; the engine never resubmits as new.
type liveTableProxy struct {
	liveTableAdmission
	mu          sync.Mutex
	tables      map[string]bool
	pending     map[uint64]*liveTablePending
	next, epoch uint64
	attached    bool
	wake        chan struct{}
}

func newLiveTableProxy(bindings []engine.LiveTableBinding) *liveTableProxy {
	p := &liveTableProxy{tables: make(map[string]bool), pending: make(map[uint64]*liveTablePending), wake: make(chan struct{}, 1)}
	for _, b := range bindings {
		p.tables[b.TableID] = true
	}
	return p
}
func (p *liveTableProxy) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
func (p *liveTableProxy) Execute(ctx context.Context, tableID string, request livetable.Request) (livetable.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !p.tables[tableID] {
		return livetable.Result{}, &livetable.Error{Code: livetable.CodeInvalid, Message: "live table is outside the assignment"}
	}
	body, err := json.Marshal(request)
	if err != nil || len(body) > liveTableFrameBytes {
		return livetable.Result{}, &livetable.Error{Code: livetable.CodeCapacity, Message: "live table request exceeds the transport limit"}
	}
	if err = ctx.Err(); err != nil {
		return livetable.Result{}, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(max(10*time.Second, request.Wait+5*time.Second))
	}
	if deadline.After(time.Now().Add(120 * time.Second)) {
		deadline = time.Now().Add(120 * time.Second)
	}
	control := liveTableControlOperation(request.Operation)
	p.mu.Lock()
	if p.next == ^uint64(0) {
		p.mu.Unlock()
		return livetable.Result{}, &livetable.Error{Code: livetable.CodeCapacity, Message: "live table transport sequence exhausted"}
	}
	if !p.admit(request.Operation, len(body)) {
		p.mu.Unlock()
		return livetable.Result{}, &livetable.Error{Code: livetable.CodeCapacity, Message: "live table channel is overloaded"}
	}
	p.next++
	id := p.next
	call := &liveTablePending{frame: &pb.LiveTableRequest{RequestId: id, TableId: tableID, RequestJson: body}, result: make(chan liveTableResponse, 1), deadline: deadline, control: control, leaseControl: liveTableLeaseControlOperation(request.Operation)}
	p.pending[id] = call
	p.mu.Unlock()
	p.signal()
	defer func() {
		p.mu.Lock()
		if p.pending[id] == call {
			delete(p.pending, id)
			p.release(request.Operation, len(body))
		}
		p.mu.Unlock()
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case result := <-call.result:
		return result.result, result.err
	case <-ctx.Done():
		return livetable.Result{}, &livetable.Error{Code: livetable.CodeOf(ctx.Err()), Message: "live table outcome may be unknown after cancellation", OutcomeUnknown: true}
	case <-timer.C:
		return livetable.Result{}, &livetable.Error{Code: livetable.CodeDeadline, Message: "live table outcome may be unknown after deadline", OutcomeUnknown: true}
	}
}
func (p *liveTableProxy) ready() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.attached }
func (p *liveTableProxy) frames(epoch uint64, group string) []*pb.LiveTableRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.epoch != epoch || !p.attached {
		return nil
	}
	ids := make([]uint64, 0, len(p.pending))
	for id, call := range p.pending {
		if call.sent != epoch {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if p.pending[ids[i]].leaseControl != p.pending[ids[j]].leaseControl {
			return p.pending[ids[i]].leaseControl
		}
		if p.pending[ids[i]].control != p.pending[ids[j]].control {
			return p.pending[ids[i]].control
		}
		return ids[i] < ids[j]
	})
	frames := make([]*pb.LiveTableRequest, 0, len(ids))
	for _, id := range ids {
		call := p.pending[id]
		remaining := time.Until(call.deadline)
		if remaining <= 0 {
			continue
		}
		// A new message rather than a copy of one (a proto message carries a
		// lock), sharing the request bytes, which are never changed once the
		// call is queued: a deep clone would copy up to a frame's 1 MiB under
		// p.mu. It sets every LiveTableRequest field.
		frames = append(frames, &pb.LiveTableRequest{
			GroupName:           group,
			RequestId:           call.frame.RequestId,
			TableId:             call.frame.TableId,
			RequestJson:         call.frame.RequestJson,
			TimeoutMilliseconds: max(1, remaining.Milliseconds()),
		})
		call.sent = epoch
	}
	return frames
}
func (p *liveTableProxy) receive(reply *pb.LiveTableReply, epoch uint64) error {
	if proto.Size(reply) > 1<<20 || len(reply.ResultJson) > liveTableFrameBytes || len(reply.ErrorCode) > 128 || reply.RequestId == 0 {
		return errors.New("oversized live table response")
	}
	result := liveTableResponse{}
	if reply.ErrorCode != "" {
		var typed livetable.Error
		if err := json.Unmarshal(reply.ResultJson, &typed); err != nil || string(typed.Code) != reply.ErrorCode || !liveTableKnownCode(typed.Code) {
			return errors.New("invalid live table error response")
		}
		typed.Message = "live table operation failed: " + string(typed.Code)
		result.err = &typed
	} else if err := json.Unmarshal(reply.ResultJson, &result.result); err != nil {
		return errors.New("invalid live table response")
	}
	p.mu.Lock()
	if p.epoch != epoch || !p.attached {
		p.mu.Unlock()
		return errors.New("live table response belongs to a replaced stream")
	}
	call := p.pending[reply.RequestId]
	p.mu.Unlock()
	if call != nil {
		select {
		case call.result <- result:
		default:
		}
	}
	return nil
}

func liveTableControlOperation(op livetable.Operation) bool {
	return op == livetable.OpPush || liveTableLeaseControlOperation(op)
}
func liveTableLeaseControlOperation(op livetable.Operation) bool {
	return op == livetable.OpRenew || op == livetable.OpAck || op == livetable.OpRelease
}
func liveTableKnownCode(code livetable.Code) bool {
	switch code {
	case livetable.CodeInvalid, livetable.CodeClosed, livetable.CodeUnavailable, livetable.CodeCapacity, livetable.CodeConflict, livetable.CodeExpired, livetable.CodeSequence, livetable.CodeLease, livetable.CodeCanceled, livetable.CodeDeadline:
		return true
	}
	return false
}
func liveTableReplyError(reply *pb.LiveTableReply, err error) {
	var typed *livetable.Error
	if !errors.As(err, &typed) {
		typed = &livetable.Error{Code: livetable.CodeUnavailable, OutcomeUnknown: true}
	}
	if !liveTableKnownCode(typed.Code) {
		typed = &livetable.Error{Code: livetable.CodeUnavailable, OutcomeUnknown: true}
	}
	safe := livetable.Error{Code: typed.Code, Message: "live table operation failed: " + string(typed.Code), OutcomeUnknown: typed.OutcomeUnknown}
	reply.ErrorCode = string(safe.Code)
	reply.ResultJson, _ = json.Marshal(safe)
}

func decodeLiveTableBindings(body []byte) ([]engine.LiveTableBinding, error) {
	if len(body) == 0 {
		return nil, nil
	}
	if len(body) > 64<<10 {
		return nil, errors.New("live table bindings exceed limit")
	}
	var bindings []engine.LiveTableBinding
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&bindings); err != nil {
		return nil, errors.New("invalid live table bindings")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid trailing live table binding data")
	}
	if len(bindings) > 16 {
		return nil, errors.New("too many live table bindings")
	}
	seen := make(map[string]bool)
	for _, b := range bindings {
		if b.TableID == "" || len(b.TableID) > 256 || b.Alias == "" || len(b.Alias) > 128 || seen[b.Alias] {
			return nil, errors.New("invalid live table binding identity")
		}
		seen[b.Alias] = true
	}
	return bindings, nil
}

func (s *Service) validateLiveTableScope(testID, group string, generation uint64, token []byte) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	assignment, exists := s.assignments[testID]
	if !exists || assignment.executionGeneration != generation {
		return status.Error(codes.FailedPrecondition, "live table assignment generation is not current")
	}
	execution := s.executions[testID][group]
	if execution == nil || execution.executionGeneration != generation {
		return status.Error(codes.FailedPrecondition, "live table group assignment is not current")
	}
	if err := s.validateMutationGenerationLocked(generation); err != nil {
		return err
	}
	if err := s.validateRunMutationTokenLocked(testID, generation, token); err != nil {
		return err
	}
	if assignment.committed {
		return s.validateLiveRunMutationLocked(testID, token, s.nowTime())
	}
	return nil
}

// LiveTableChannel is opened by the controller, over the existing direct or
// reverse transport. Its scope is checked before attach and for every reply.
func (s *Service) LiveTableChannel(stream pb.GeneratorControl_LiveTableChannelServer) error {
	open, err := stream.Recv()
	if err != nil {
		return err
	}
	if proto.Size(open) > 1<<20 || open.TestId == "" || open.GroupName == "" || open.RequestId != 0 || len(open.ResultJson) != 0 || open.ErrorCode != "" {
		return status.Error(codes.InvalidArgument, "invalid live table open frame")
	}
	if err = s.validateLiveTableScope(open.TestId, open.GroupName, open.ExecutionGeneration, open.RunSupervisionToken); err != nil {
		return err
	}
	s.mu.RLock()
	execution := s.executions[open.TestId][open.GroupName]
	var proxy *liveTableProxy
	if execution != nil && execution.stagedConfig != nil && len(execution.stagedConfig.Groups) == 1 {
		proxy, _ = execution.stagedConfig.Groups[0].LiveTableClient.(*liveTableProxy)
	}
	s.mu.RUnlock()
	if proxy == nil {
		return status.Error(codes.FailedPrecondition, "group has no live table assignment")
	}
	proxy.mu.Lock()
	proxy.epoch++
	epoch := proxy.epoch
	proxy.attached = true
	proxy.mu.Unlock()
	defer func() {
		proxy.mu.Lock()
		if proxy.epoch == epoch {
			proxy.attached = false
		}
		proxy.mu.Unlock()
	}()
	if err = stream.SendHeader(metadata.Pairs("apexload-live-tables-ready", "1")); err != nil {
		return err
	}
	recvErr := make(chan error, 1)
	go func() {
		for {
			reply, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			if reply.TestId != open.TestId || reply.GroupName != open.GroupName || reply.ExecutionGeneration != open.ExecutionGeneration || !bytes.Equal(reply.RunSupervisionToken, open.RunSupervisionToken) {
				recvErr <- status.Error(codes.FailedPrecondition, "live table scope changed")
				return
			}
			if err = s.validateLiveTableScope(open.TestId, open.GroupName, open.ExecutionGeneration, open.RunSupervisionToken); err != nil {
				recvErr <- err
				return
			}
			if err = proxy.receive(reply, epoch); err != nil {
				recvErr <- status.Error(codes.InvalidArgument, err.Error())
				return
			}
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err = s.validateLiveTableScope(open.TestId, open.GroupName, open.ExecutionGeneration, open.RunSupervisionToken); err != nil {
			return err
		}
		proxy.mu.Lock()
		current := proxy.epoch == epoch
		proxy.mu.Unlock()
		if !current {
			return status.Error(codes.Aborted, "live table stream replaced")
		}
		for _, frame := range proxy.frames(epoch, open.GroupName) {
			if err = stream.Send(frame); err != nil {
				return err
			}
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-s.shutdown:
			return nil
		case err := <-recvErr:
			return err
		case <-proxy.wake:
		case <-ticker.C:
		}
	}
}

// OpenLiveTables waits for a validated attachment, then serves operations and
// reconnects within ctx. Caller owns ctx through the run's current leader term.
func (c *Client) OpenLiveTables(ctx context.Context, testID, group string, generation uint64, token []byte, handler livetable.Client) error {
	ready := make(chan error, 1)
	go func() {
		first := true
		for {
			err := c.serveLiveTables(ctx, testID, group, generation, token, handler, func(err error) {
				if first {
					ready <- err
					first = false
				}
			})
			if first {
				ready <- err
				return
			}
			if ctx.Err() != nil {
				return
			}
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	select {
	case err := <-ready:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *Client) serveLiveTables(ctx context.Context, testID, group string, generation uint64, token []byte, handler livetable.Client, ready func(error)) error {
	return c.serveLiveTablesWithHandshakeTimeout(ctx, testID, group, generation, token, handler, ready, 5*time.Second)
}

func (c *Client) serveLiveTablesWithHandshakeTimeout(ctx context.Context, testID, group string, generation uint64, token []byte, handler livetable.Client, ready func(error), handshakeTimeout time.Duration) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Cancel the stream itself on a stalled handshake. Header returns when its
	// context is canceled; no detached receive goroutine survives a reconnect.
	handshakeTimer := time.AfterFunc(handshakeTimeout, cancel)
	defer handshakeTimer.Stop()
	stream, err := c.rpc.LiveTableChannel(streamCtx)
	if err != nil {
		return err
	}
	opening := &pb.LiveTableReply{TestId: testID, GroupName: group, ExecutionGeneration: generation, RunSupervisionToken: append([]byte(nil), token...)}
	if err = stream.Send(opening); err != nil {
		return err
	}
	headers, err := stream.Header()
	if !handshakeTimer.Stop() {
		return context.DeadlineExceeded
	}
	if err != nil {
		return err
	}
	if values := headers.Get("apexload-live-tables-ready"); len(values) != 1 || values[0] != "1" {
		return errors.New("live table readiness was not acknowledged")
	}
	ready(nil)
	replies := make(chan *pb.LiveTableReply, liveTableControlReserve)
	go func() {
		for {
			select {
			case <-streamCtx.Done():
				return
			case reply := <-replies:
				if err := stream.Send(reply); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	var admission liveTableAdmission
	var admissionMu sync.Mutex
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		if proto.Size(frame) > 1<<20 || frame.GroupName != group || frame.RequestId == 0 || len(frame.RequestJson) > liveTableFrameBytes || frame.TableId == "" || len(frame.TableId) > 256 || frame.TimeoutMilliseconds <= 0 || frame.TimeoutMilliseconds > 120000 {
			return errors.New("invalid live table request frame")
		}
		var request livetable.Request
		dec := json.NewDecoder(bytes.NewReader(frame.RequestJson))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&request); err != nil {
			return errors.New("invalid live table request JSON")
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			return errors.New("invalid trailing live table request JSON")
		}
		admissionMu.Lock()
		admitted := admission.admit(request.Operation, len(frame.RequestJson))
		admissionMu.Unlock()
		if !admitted {
			reply := proto.Clone(opening).(*pb.LiveTableReply)
			reply.RequestId = frame.RequestId
			liveTableReplyError(reply, &livetable.Error{Code: livetable.CodeCapacity, Message: "live table operation admission limit exceeded"})
			select {
			case replies <- reply:
			case <-streamCtx.Done():
				return streamCtx.Err()
			}
			continue
		}
		go func(frame *pb.LiveTableRequest, request livetable.Request) {
			defer func() {
				admissionMu.Lock()
				admission.release(request.Operation, len(frame.RequestJson))
				admissionMu.Unlock()
			}()
			reply := proto.Clone(opening).(*pb.LiveTableReply)
			reply.RequestId = frame.RequestId
			{
				opCtx, stop := context.WithTimeout(streamCtx, time.Duration(frame.TimeoutMilliseconds)*time.Millisecond)
				result, err := handler.Execute(opCtx, frame.TableId, request)
				stop()
				if err != nil {
					liveTableReplyError(reply, err)
				} else {
					reply.ResultJson, err = json.Marshal(result)
					if err != nil || len(reply.ResultJson) > liveTableFrameBytes {
						liveTableReplyError(reply, &livetable.Error{Code: livetable.CodeCapacity, Message: "committed result exceeds negotiated transport limit", OutcomeUnknown: true})
					}
				}
			}
			select {
			case replies <- reply:
			case <-streamCtx.Done():
			}
		}(frame, request)
	}
}
