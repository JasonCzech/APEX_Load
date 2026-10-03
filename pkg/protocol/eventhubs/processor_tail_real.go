//go:build eventhubs_real

package eventhubs

import (
	"context"
	"errors"
	"sync"
	"time"

	eh "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type tailSource struct {
	refs   int
	active bool
	wake   chan struct{}
}

var processorTailSources = struct {
	sync.Mutex
	sources map[string]*tailSource
}{sources: map[string]*tailSource{}}

// Admission, not evidence, is shared by immutable namespace/hub. Every poll
// uses its own consumer identity and only updates its own generation tracker.
// Participant-local cadence avoids one300s policy suppressing another10s policy.
// Borrowed consumer cleanup joins the poll even if its SDK ignores cancellation.
func startProcessorTail(ctx context.Context, cfg config, inventory map[string]bool, p *processorProgress, query func(context.Context, string, *eh.GetPartitionPropertiesOptions) (eh.PartitionProperties, error)) chan struct{} {
	done := make(chan struct{})
	key := canonicalProcessorNamespace(cfg.Namespace) + "\x00" + cfg.EventHub
	processorTailSources.Lock()
	source := processorTailSources.sources[key]
	if source == nil {
		source = &tailSource{wake: make(chan struct{})}
		processorTailSources.sources[key] = source
	}
	source.refs++
	processorTailSources.Unlock()
	interval := cfg.TailPollInterval
	if interval == 0 {
		interval = 10 * time.Second
	}
	go func() {
		defer close(done)
		defer func() {
			processorTailSources.Lock()
			source.refs--
			if source.refs == 0 && !source.active {
				delete(processorTailSources.sources, key)
			}
			processorTailSources.Unlock()
		}()
		next := time.Time{}
		for {
			if ctx.Err() != nil {
				return
			}
			var tailLease protocol.RuntimePauseLease
			if cfg.pauseGate != nil && !time.Now().Before(next) {
				var err error
				tailLease, err = protocol.AdmitRuntimePause(ctx, cfg.pauseGate, protocol.RuntimePauseTail)
				if err != nil {
					return
				}
			}
			processorTailSources.Lock()
			wake := source.wake
			admitted := !source.active && !time.Now().Before(next)
			if admitted {
				source.active = true
			}
			processorTailSources.Unlock()
			if !admitted && tailLease != nil {
				tailLease.Release()
				tailLease = nil
			}
			if admitted {
				generations := p.tailGenerations()
				pollCtx := ctx
				if tailLease != nil {
					pollCtx = tailLease.Context()
				}
				round, cancel := context.WithTimeout(pollCtx, 10*time.Second)
				var workers sync.WaitGroup
				slots := make(chan struct{}, 2)
				for partition := range inventory {
					if round.Err() != nil {
						break
					}
					select {
					case slots <- struct{}{}:
					case <-round.Done():
						continue
					}
					workers.Add(1)
					go func(id string) {
						defer workers.Done()
						defer func() { <-slots }()
						budget, stop := context.WithTimeout(round, 5*time.Second)
						props, err := query(budget, id, nil)
						if budget.Err() != nil {
							err = budget.Err()
						}
						stop()
						if err == nil && (props.PartitionID != id || props.EventHubName != cfg.EventHub || !props.IsEmpty && (props.LastEnqueuedSequenceNumber < 0 || !validProviderOffset(props.LastEnqueuedOffset))) {
							err = errors.New("invalid bounded partition tail observation")
						}
						if round.Err() != nil || ctx.Err() != nil {
							return
						}
						if generation := generations[id]; generation != "" {
							p.tailObservedGeneration(id, generation, props.LastEnqueuedSequenceNumber, props.LastEnqueuedOffset, props.IsEmpty, err != nil)
						}
					}(partition)
				}
				workers.Wait()
				cancel()
				if tailLease != nil {
					tailLease.Release()
				}
				next = time.Now().Add(interval)
				processorTailSources.Lock()
				source.active = false
				close(source.wake)
				source.wake = make(chan struct{})
				wake = source.wake
				processorTailSources.Unlock()
			}
			wait := time.Duration(0)
			if !next.IsZero() {
				wait = max(0, time.Until(next))
			}
			if wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-wake:
					timer.Stop()
				case <-timer.C:
				}
			} else {
				select {
				case <-ctx.Done():
					return
				case <-wake:
				}
			}
		}
	}()
	return done
}
func (p *processorProgress) tailGenerations() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]string{}
	for _, e := range p.rows {
		if e.row.Ownership == "owned" {
			out[e.row.Partition] = e.row.Generation
		}
	}
	return out
}
func (p *processorProgress) tailObservedGeneration(partition, generation string, seq int64, offset string, empty, failed bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.rows[partition+"\x00"+generation]
	if e == nil || e.row.Ownership != "owned" {
		return
	}
	if failed {
		e.row.TailPosition = protocol.MessagingProgressPosition{State: "unconfirmed"}
	} else if empty {
		e.row.TailPosition = protocol.MessagingProgressPosition{State: "empty"}
	} else {
		e.tail = time.Now()
		e.row.TailPosition = observedProgressPosition(seq, offset, e.tail)
	}
	p.change(e)
}
