package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	kafkaprotocol "github.com/jasonczech/apexload/pkg/protocol/kafka"
)

// sourceSamplerRuntime is owned by one engine run, including every group from
// that source. Group cancellation drains in-flight operations; it never retries
// or replaces workers to compensate for an exhausted schedule.
type sourceSamplerRuntime struct {
	state     *samplerScheduleState
	producers *kafkaprotocol.ProducerSession
	clock     *sourceSamplerClock
	groups    map[string]context.Context
	stops     map[string]context.CancelFunc
}

func newSourceSamplerRuntime(scripts []*dsl.Script, control activePauseController) (*sourceSamplerRuntime, error) {
	state, err := newSamplerScheduleState(scripts)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	r := &sourceSamplerRuntime{state: state, producers: kafkaprotocol.NewProducerSession(), clock: &sourceSamplerClock{origin: now, epoch: now.UnixMilli(), control: control}, groups: map[string]context.Context{}, stops: map[string]context.CancelFunc{}}
	for _, script := range scripts {
		if script == nil || script.Config.JMeter == nil {
			continue
		}
		c := script.Config.JMeter
		if c.SourceGroupID == "" {
			r.close()
			return nil, fmt.Errorf("sampler runtime requires source group identity")
		}
		key := c.SourcePlanID + "\x00" + c.SourceGroupID
		if r.groups[key] == nil {
			r.groups[key], r.stops[key] = context.WithCancel(context.Background())
		}
	}
	return r, nil
}
func (r *sourceSamplerRuntime) close() error {
	for _, stop := range r.stops {
		stop()
	}
	if r.producers != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return r.producers.Close(ctx)
	}
	return nil
}
func (r *sourceSamplerRuntime) group(c *dsl.JMeterCompatibility) (context.Context, context.CancelFunc, error) {
	if c == nil {
		return nil, nil, fmt.Errorf("sampler runtime requires source profile")
	}
	key := c.SourcePlanID + "\x00" + c.SourceGroupID
	ctx := r.groups[key]
	if ctx == nil {
		return nil, nil, fmt.Errorf("unknown sampler source group")
	}
	return ctx, r.stops[key], nil
}
func (r *sourceSamplerRuntime) before(ctx context.Context, script *dsl.Script, action dsl.Action) error {
	group, stop, err := r.group(script.Config.JMeter)
	if err != nil {
		return err
	}
	if err = group.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	unbind := context.AfterFunc(group, cancel)
	defer func() { unbind(); cancel() }()
	if group.Err() != nil {
		return group.Err()
	}
	if err = r.clock.ready(ctx); err != nil {
		return err
	}
	err = r.state.wait(ctx, script.Config.JMeter.SourcePlanID, action.PacingIDs, r.clock)
	if errors.Is(err, errSamplerScheduleComplete) {
		stop()
	}
	return err
}

// Clock progress is independent of worker occupancy. ActiveElapsed freezes on
// pause, and every wake rechecks the pause gate before admitting another sample.
type sourceSamplerClock struct {
	origin  time.Time
	epoch   int64
	control activePauseController
}

func (c *sourceSamplerClock) elapsed() time.Duration {
	if c.control != nil {
		return c.control.ActiveElapsed()
	}
	return time.Since(c.origin)
}
func (c *sourceSamplerClock) NowMillis() int64 { return c.epoch + c.elapsed().Milliseconds() }
func (c *sourceSamplerClock) ready(ctx context.Context) error {
	if c.control != nil {
		return c.control.WaitIfPaused(ctx)
	}
	return ctx.Err()
}
func (c *sourceSamplerClock) Wait(ctx context.Context, delay time.Duration, wake <-chan struct{}) error {
	deadline := c.elapsed() + delay
	for {
		if err := c.ready(ctx); err != nil {
			return err
		}
		remaining := deadline - c.elapsed()
		if remaining <= 0 {
			return ctx.Err()
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-wake:
			timer.Stop()
			return c.ready(ctx)
		case <-timer.C:
		}
	}
}

func (vu *VUser) beforeSourceSampler(ctx context.Context, action dsl.Action) error {
	if len(action.PacingIDs) == 0 {
		return nil
	}
	if vu.sourceSamplers == nil {
		return fmt.Errorf("sampler schedule runtime is unavailable")
	}
	return vu.sourceSamplers.before(ctx, vu.Script, action)
}

// bindSourceSamplerBoundary cancels iteration/action admission on source group
// completion. HardStopCtx is intentionally retained so acknowledged sends can
// finish and teardown still executes under its own cleanup budget.
func (vu *VUser) bindSourceSamplerBoundary(ctx context.Context) (context.Context, func(), error) {
	if vu.sourceSamplers == nil || vu.Script.Config.JMeter == nil {
		return ctx, func() {}, nil
	}
	group, _, err := vu.sourceSamplers.group(vu.Script.Config.JMeter)
	if err != nil {
		return nil, nil, err
	}
	merged, cancel := context.WithCancel(ctx)
	unbind := context.AfterFunc(group, cancel)
	originalHard := vu.HardStopCtx
	if originalHard == nil {
		vu.HardStopCtx = ctx
	}
	original := vu.ActionBoundaryCtx
	boundary := original
	if boundary == nil {
		boundary = ctx
	}
	actionBoundary, actionCancel := context.WithCancel(boundary)
	unbindAction := context.AfterFunc(group, actionCancel)
	vu.ActionBoundaryCtx = actionBoundary
	if group.Err() != nil {
		cancel()
		actionCancel()
	}
	return merged, func() {
		unbind()
		unbindAction()
		cancel()
		actionCancel()
		vu.ActionBoundaryCtx = original
		vu.HardStopCtx = originalHard
	}, nil
}

func (r *sourceSamplerRuntime) bindProtocolConfig(cfg map[string]any, script *dsl.Script) {
	c := script.Config.JMeter
	if c == nil || script.Protocol != "kafka" || c.ProducerLifecycle != "source_engine" {
		return
	}
	cfg["kafka_producer_lifecycle"] = c.ProducerLifecycle
	cfg["kafka_source_configuration"] = c.SourcePlanID + "\x00" + c.ConfigurationID
	cfg["kafka_producer_session"] = r.producers
}

func (vu *VUser) sourceSampleDuration(start time.Time, result *protocol.Result) time.Duration {
	if result != nil && vu.Script.Config.JMeter != nil && vu.Script.Config.JMeter.SampleTiming != "" {
		return result.Duration
	}
	return time.Since(start)
}
