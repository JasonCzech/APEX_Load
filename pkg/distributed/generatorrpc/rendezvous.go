package generatorrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/rendezvous"
	"github.com/jasonczech/apexload/pkg/dsl"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type RendezvousAssignment struct {
	Population int                   `json:"population"`
	Contracts  []rendezvous.Contract `json:"contracts"`
}
type rendezvousResponse struct {
	outcome rendezvous.Outcome
	err     error
}
type rendezvousCall struct {
	frame  *pb.RendezvousRequest
	result chan rendezvousResponse
	sent   uint64
}
type rendezvousProxy struct {
	mu          sync.Mutex
	pending     map[uint64]*rendezvousCall
	next, epoch uint64
	attached    bool
	wake        chan struct{}
}

func newRendezvousProxy() *rendezvousProxy {
	return &rendezvousProxy{pending: map[uint64]*rendezvousCall{}, wake: make(chan struct{}, 1)}
}
func (p *rendezvousProxy) ready() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.attached }
func (p *rendezvousProxy) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
func (p *rendezvousProxy) Exchange(ctx context.Context, batch rendezvous.Batch) (rendezvous.Outcome, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return rendezvous.Outcome{}, err
	}
	body, err := json.Marshal(batch)
	if err != nil || len(body) > 8192 || len(batch.Arrivals) > rendezvous.MaxBatch {
		return rendezvous.Outcome{}, rendezvous.ErrInvalid
	}
	p.mu.Lock()
	if len(p.pending) >= rendezvous.MaxActiveRounds || p.next == ^uint64(0) {
		p.mu.Unlock()
		return rendezvous.Outcome{}, rendezvous.ErrCapacity
	}
	p.next++
	id := p.next
	call := &rendezvousCall{frame: &pb.RendezvousRequest{RequestId: id, BatchJson: body}, result: make(chan rendezvousResponse, 1)}
	p.pending[id] = call
	p.mu.Unlock()
	p.signal()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	select {
	case reply := <-call.result:
		return reply.outcome, reply.err
	case <-ctx.Done():
		return rendezvous.Outcome{}, ctx.Err()
	}
}
func (p *rendezvousProxy) frames(epoch uint64, group string) []*pb.RendezvousRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.epoch != epoch || !p.attached {
		return nil
	}
	out := make([]*pb.RendezvousRequest, 0, len(p.pending))
	for _, call := range p.pending {
		if call.sent != epoch {
			out = append(out, &pb.RendezvousRequest{GroupName: group, RequestId: call.frame.RequestId, BatchJson: append([]byte(nil), call.frame.BatchJson...)})
			call.sent = epoch
		}
	}
	return out
}
func rendezvousErrorCode(err error) string {
	switch {
	case errors.Is(err, rendezvous.ErrInvalid):
		return "invalid"
	case errors.Is(err, rendezvous.ErrRetired):
		return "retired"
	case errors.Is(err, rendezvous.ErrCapacity):
		return "capacity"
	default:
		return "unavailable"
	}
}
func rendezvousCodeError(code string) error {
	switch code {
	case "":
		return nil
	case "invalid":
		return rendezvous.ErrInvalid
	case "retired":
		return rendezvous.ErrRetired
	case "capacity":
		return rendezvous.ErrCapacity
	case "unavailable":
		return rendezvous.ErrUnavailable
	default:
		return rendezvous.ErrInvalid
	}
}
func (p *rendezvousProxy) receive(reply *pb.RendezvousReply, epoch uint64) error {
	if proto.Size(reply) > 8192 || reply.RequestId == 0 || len(reply.OutcomeJson) > 1024 {
		return rendezvous.ErrInvalid
	}
	response := rendezvousResponse{err: rendezvousCodeError(reply.ErrorCode)}
	if reply.ErrorCode == "" {
		if err := json.Unmarshal(reply.OutcomeJson, &response.outcome); err != nil {
			return rendezvous.ErrInvalid
		}
		o := response.outcome
		if (o.State != "waiting" && o.State != "released" && o.State != "timed_out") || o.Target < 1 || o.Target > rendezvous.MaxPopulation || o.Arrived < 0 || o.Arrived > rendezvous.MaxPopulation || o.Deadline.IsZero() {
			return rendezvous.ErrInvalid
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.epoch != epoch || !p.attached {
		return rendezvous.ErrUnavailable
	}
	if call := p.pending[reply.RequestId]; call != nil {
		select {
		case call.result <- response:
		default:
		}
	}
	return nil
}

func decodeRendezvousAssignment(body []byte, script *dsl.Script, localPopulation int) (*RendezvousAssignment, error) {
	specs, err := dsl.ScriptDistributedRendezvous(script)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		if len(specs) > 0 {
			return nil, errors.New("distributed rendezvous has no admitted assignment")
		}
		return nil, nil
	}
	if len(body) > 16384 || len(specs) == 0 {
		return nil, errors.New("unexpected rendezvous assignment")
	}
	var config RendezvousAssignment
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, rendezvous.ErrInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, rendezvous.ErrInvalid
	}
	if config.Population < localPopulation || config.Population > rendezvous.MaxPopulation || config.Population < 1 || len(config.Contracts) != len(specs) {
		return nil, rendezvous.ErrInvalid
	}
	for i, spec := range specs {
		target := config.Population
		if spec.Count > 0 {
			target = spec.Count
		} else if spec.Percent > 0 {
			target = (config.Population*spec.Percent + 99) / 100
		}
		if target > config.Population || config.Contracts[i] != (rendezvous.Contract{Key: spec.Key, Target: target, TimeoutMS: spec.TimeoutMS}) {
			return nil, rendezvous.ErrInvalid
		}
	}
	return &config, nil
}

func (s *Service) RendezvousChannel(stream pb.GeneratorControl_RendezvousChannelServer) error {
	open, err := stream.Recv()
	if err != nil {
		return err
	}
	if proto.Size(open) > 8192 || open.TestId == "" || open.GroupName == "" || open.RequestId != 0 || len(open.OutcomeJson) != 0 || open.ErrorCode != "" {
		return status.Error(codes.InvalidArgument, "invalid rendezvous open frame")
	}
	if err = s.validateLiveTableScope(open.TestId, open.GroupName, open.ExecutionGeneration, open.RunSupervisionToken); err != nil {
		return err
	}
	s.mu.RLock()
	execution := s.executions[open.TestId][open.GroupName]
	var proxy *rendezvousProxy
	if execution != nil && execution.stagedConfig != nil && len(execution.stagedConfig.Groups) == 1 {
		proxy, _ = execution.stagedConfig.Groups[0].DistributedRendezvousClient.(*rendezvousProxy)
	}
	s.mu.RUnlock()
	if proxy == nil {
		return status.Error(codes.FailedPrecondition, "group has no distributed rendezvous assignment")
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
	if err = stream.SendHeader(metadata.Pairs("apexload-rendezvous-ready", "1")); err != nil {
		return err
	}
	receiveErr := make(chan error, 1)
	go func() {
		for {
			reply, err := stream.Recv()
			if err != nil {
				receiveErr <- err
				return
			}
			if reply.TestId != open.TestId || reply.GroupName != open.GroupName || reply.ExecutionGeneration != open.ExecutionGeneration || !bytes.Equal(reply.RunSupervisionToken, open.RunSupervisionToken) {
				receiveErr <- rendezvous.ErrInvalid
				return
			}
			if err = s.validateLiveTableScope(open.TestId, open.GroupName, open.ExecutionGeneration, open.RunSupervisionToken); err != nil {
				receiveErr <- err
				return
			}
			if err = proxy.receive(reply, epoch); err != nil {
				receiveErr <- err
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
			return status.Error(codes.Aborted, "rendezvous stream replaced")
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
		case err := <-receiveErr:
			return err
		case <-proxy.wake:
		case <-ticker.C:
		}
	}
}

func (c *Client) OpenRendezvous(ctx context.Context, test, group string, generation uint64, token []byte, handler rendezvous.Client) error {
	ready := make(chan error, 1)
	go func() {
		first := true
		for {
			err := c.serveRendezvous(ctx, test, group, generation, token, handler, func(err error) {
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
func (c *Client) serveRendezvous(ctx context.Context, test, group string, generation uint64, token []byte, handler rendezvous.Client, ready func(error)) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	timer := time.AfterFunc(5*time.Second, cancel)
	defer timer.Stop()
	stream, err := c.rpc.RendezvousChannel(streamCtx)
	if err != nil {
		return err
	}
	opening := &pb.RendezvousReply{TestId: test, GroupName: group, ExecutionGeneration: generation, RunSupervisionToken: append([]byte(nil), token...)}
	if err = stream.Send(opening); err != nil {
		return err
	}
	headers, err := stream.Header()
	if !timer.Stop() {
		return context.DeadlineExceeded
	}
	if err != nil {
		return err
	}
	if values := headers.Get("apexload-rendezvous-ready"); len(values) != 1 || values[0] != "1" {
		return errors.New("rendezvous readiness was not acknowledged")
	}
	ready(nil)
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		if proto.Size(frame) > 16384 || frame.GroupName != group || frame.RequestId == 0 || len(frame.BatchJson) > 8192 {
			return rendezvous.ErrInvalid
		}
		var batch rendezvous.Batch
		decoder := json.NewDecoder(bytes.NewReader(frame.BatchJson))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&batch); err != nil {
			return rendezvous.ErrInvalid
		}
		if err = decoder.Decode(new(any)); err != io.EOF {
			return rendezvous.ErrInvalid
		}
		if len(batch.Arrivals) > rendezvous.MaxBatch {
			return rendezvous.ErrInvalid
		}
		callCtx, stop := context.WithTimeout(streamCtx, 4*time.Second)
		outcome, err := handler.Exchange(callCtx, batch)
		stop()
		reply := *opening
		reply.RequestId = frame.RequestId
		if err != nil {
			reply.ErrorCode = rendezvousErrorCode(err)
		} else {
			reply.OutcomeJson, err = json.Marshal(outcome)
			if err != nil || len(reply.OutcomeJson) > 1024 {
				return rendezvous.ErrInvalid
			}
		}
		if err = stream.Send(&reply); err != nil {
			return err
		}
	}
}
