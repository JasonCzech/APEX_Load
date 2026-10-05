//go:build eventhubs_real

package eventhubs

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"sync"
	"time"

	eh "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
	"github.com/Azure/go-amqp"
)

const processorHeartbeatInterval = 10 * time.Second
const processorOwnershipTTL = time.Minute

// Mirrors the pinned SDK's internal condition test using the public AMQP error.
func isProcessorGeoReplicationOffsetError(err error) bool {
	var provider *amqp.Error
	return errors.As(err, &provider) && provider.Condition == amqp.ErrCond("com.microsoft:georeplication:invalid-offset")
}

type partitionReceiver interface {
	ReceiveEvents(context.Context, int, *eh.ReceiveEventsOptions) ([]*eh.ReceivedEventData, error)
	Close(context.Context) error
}

var errProcessorReceiveCleanupUnproven = errors.New("processor receive transport cleanup incomplete")

// Every governed partition exclusively owns a physical consumer connection.
// PartitionClient.Close alone can swallow link errors in the pinned SDK; the
// consumer close joins the connection's actual reader and writer goroutines.
type ownedPartitionReceiver struct {
	inner    *eh.PartitionClient
	consumer *eh.ConsumerClient
}

func (p *ownedPartitionReceiver) ReceiveEvents(ctx context.Context, n int, o *eh.ReceiveEventsOptions) ([]*eh.ReceivedEventData, error) {
	return p.inner.ReceiveEvents(ctx, n, o)
}
func (p *ownedPartitionReceiver) Close(ctx context.Context) error {
	if p.inner != nil {
		_ = p.inner.Close(ctx)
	}
	return p.consumer.Close(ctx)
}

type governedPartition struct {
	inner                                             partitionReceiver
	reopen                                            func(eh.StartPosition) (partitionReceiver, error)
	queryBoundary                                     func(context.Context, string, *eh.GetPartitionPropertiesOptions) (eh.PartitionProperties, error)
	recovered, quarantined, anchorFrozen, forceAnchor bool
	anchor                                            eh.StartPosition
	lastSequence                                      *int64
	store                                             *fencedCheckpointStore
	partition, generation                             string
}

func (p *governedPartition) PartitionID() string { return p.partition }
func (p *governedPartition) ensureReceiver(ctx context.Context) error {
	if p.quarantined {
		return errProcessorReceiveCleanupUnproven
	}
	if p.inner != nil {
		return nil
	}
	if p.reopen == nil {
		return errors.New("processor partition receiver unavailable")
	}
	position := p.anchor
	if p.store != nil {
		current, err := p.store.records.Read(ctx, p.store.prefix()+url.PathEscape(p.partition))
		if err != nil {
			return err
		}
		if current.Owner != p.store.owner || current.Generation != p.generation {
			return errCheckpointFence
		}
		if p.forceAnchor || p.recovered && p.lastSequence == nil {
			position = p.anchor // the old durable integer offset caused geo-DR rejection
		} else if current.Sequence != nil && (p.lastSequence == nil || *current.Sequence >= *p.lastSequence) {
			if current.Offset != "" && !p.recovered {
				offset := current.Offset
				position = eh.StartPosition{Offset: &offset}
			} else {
				seq := *current.Sequence
				position = eh.StartPosition{SequenceNumber: &seq}
			}
		} else if p.lastSequence != nil {
			seq := *p.lastSequence
			position = eh.StartPosition{SequenceNumber: &seq}
		}
	}
	// @latest is broker-relative at every attach. Freeze its exact initial
	// boundary once, immediately before the first actual read, so pause gaps are
	// retained. This private cursor is never published as a tail observation.
	if position.Latest != nil && *position.Latest {
		if !p.anchorFrozen {
			if p.queryBoundary == nil {
				return errors.New("processor latest boundary unavailable")
			}
			budget, cancel := context.WithTimeout(ctx, 5*time.Second)
			props, err := p.queryBoundary(budget, p.partition, nil)
			if budget.Err() != nil {
				err = budget.Err()
			}
			cancel()
			if err != nil {
				return err
			}
			if props.PartitionID != p.partition || p.store == nil || props.EventHubName != p.store.hub || !props.IsEmpty && props.LastEnqueuedSequenceNumber < 0 {
				return errors.New("processor latest boundary is invalid")
			}
			if props.IsEmpty {
				yes := true
				p.anchor = eh.StartPosition{Earliest: &yes}
			} else {
				seq := props.LastEnqueuedSequenceNumber
				p.anchor = eh.StartPosition{SequenceNumber: &seq}
			}
			p.anchorFrozen = true
		}
		position = p.anchor
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	receiver, err := p.reopen(position)
	if receiver != nil {
		p.inner = receiver
	}
	if err != nil || receiver == nil {
		if p.inner != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), eventHubsCloseTimeout)
			closeErr := p.closeReceiver(cleanup)
			cancel()
			if closeErr != nil {
				return errProcessorReceiveCleanupUnproven
			}
		}
		if err == nil {
			err = errors.New("processor partition receiver unavailable")
		}
		return err
	}
	return nil
}
func (p *governedPartition) closeReceiver(ctx context.Context) error {
	if p.inner == nil {
		return nil
	}
	if err := p.inner.Close(ctx); err != nil {
		p.quarantined = true
		return err
	}
	p.inner = nil
	return nil
}
func (p *governedPartition) ReceiveEvents(ctx context.Context, count int, opts *eh.ReceiveEventsOptions) ([]*eh.ReceivedEventData, error) {
	if err := p.ensureReceiver(ctx); err != nil {
		return nil, err
	}
	events, err := p.inner.ReceiveEvents(ctx, count, opts)
	if !p.recovered && isProcessorGeoReplicationOffsetError(err) && p.reopen != nil && ctx.Err() == nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), eventHubsCloseTimeout)
		closeErr := p.closeReceiver(cleanup)
		cancel()
		if closeErr != nil {
			return events, errProcessorReceiveCleanupUnproven
		}
		yes := true
		p.anchor = eh.StartPosition{Earliest: &yes, Inclusive: true}
		p.anchorFrozen = true
		p.recovered = true
		p.forceAnchor = true
		if err = p.ensureReceiver(ctx); err != nil {
			return nil, err
		}
		events, err = p.inner.ReceiveEvents(ctx, count, opts)
	}
	if len(events) == 1 && events[0] != nil && events[0].SequenceNumber >= 0 {
		seq := events[0].SequenceNumber
		p.lastSequence = &seq
		p.forceAnchor = false // recovery remains anchored until actual evidence arrives
	}
	if err != nil || ctx.Err() != nil || len(events) != 1 {
		// Cancelling ReceiveEvents does not retract outstanding manual AMQP credit.
		// Actual private connection close must finish inside this receive lease.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), eventHubsCloseTimeout)
		closeErr := p.closeReceiver(cleanup)
		cancel()
		if closeErr != nil {
			return events, errProcessorReceiveCleanupUnproven
		}
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
	}
	return events, err
}
func (p *governedPartition) Close(ctx context.Context) error { return p.closeReceiver(ctx) }
func (p *governedPartition) UpdateCheckpoint(ctx context.Context, event *eh.ReceivedEventData, _ *eh.UpdateCheckpointOptions) error {
	seq, offset := event.SequenceNumber, event.Offset
	return p.store.SetCheckpoint(ctx, eh.Checkpoint{FullyQualifiedNamespace: p.store.namespace, EventHubName: p.store.hub, ConsumerGroup: p.store.group, PartitionID: p.partition, SequenceNumber: &seq, Offset: &offset}, nil)
}

type governedWorker struct {
	generation string
	cancel     context.CancelFunc
	done       chan struct{}
}
type processorOwnershipView struct{ rows []eh.Ownership }

// Control-plane ownership reads/renewals run independently of receive admission.
// New claims, checkpoint resume reads and lazy handle creation hold a receive
// lease. Unlike SDK Processor, this never eagerly opens an AMQP receive link.
func (s *eventHubsProcessor) runGovernedProcessor(child *processorLease, start string, factory func(string, string, eh.StartPosition) (processorPartition, error)) error {
	views := make(chan processorOwnershipView, 1)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		failures := 0
		for {
			budget, cancel := context.WithTimeout(s.ctx, 5*time.Second)
			rows, err := s.store.ListOwnership(budget, s.store.namespace, s.store.hub, s.store.group, nil)
			if err == nil {
				renew := []eh.Ownership{}
				for _, row := range rows {
					if row.OwnerID == s.store.owner {
						renew = append(renew, row)
					}
				}
				if len(renew) > 0 {
					var renewed []eh.Ownership
					renewed, err = s.store.ClaimOwnership(budget, renew, nil)
					for _, updated := range renewed {
						for i := range rows {
							if rows[i].PartitionID == updated.PartitionID {
								rows[i] = updated
								break
							}
						}
					}
				}
			}
			cancel()
			if s.ctx.Err() != nil {
				return
			}
			if err != nil {
				failures++
				if failures >= 3 {
					s.fail(errors.New("processor ownership heartbeat failed repeatedly"))
					return
				}
			} else {
				failures = 0
				view := processorOwnershipView{rows: rows}
				select {
				case views <- view:
				default:
					select {
					case <-views:
					default:
					}
					select {
					case views <- view:
					default:
					}
				}
			}
			interval := s.heartbeatInterval
			if interval <= 0 {
				interval = processorHeartbeatInterval
			}
			timer := time.NewTimer(interval)
			select {
			case <-s.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	active := map[string]*governedWorker{}
	var workers sync.WaitGroup
	defer func() {
		s.cancel()
		for _, worker := range active {
			worker.cancel()
		}
		<-heartbeatDone
		workers.Wait()
		// Voluntary release is fenced to this owner and keeps durable cursors.
		// A failed/blocked release is never proof of completed cleanup.
		cleanup, cancel := context.WithTimeout(context.Background(), s.cleanupTimeout())
		defer cancel()
		rows, err := s.store.ListOwnership(cleanup, s.store.namespace, s.store.hub, s.store.group, nil)
		if err == nil {
			releases := []eh.Ownership{}
			for _, row := range rows {
				if row.OwnerID == s.store.owner {
					row.OwnerID = ""
					releases = append(releases, row)
				}
			}
			if len(releases) > 0 {
				_, err = s.store.ClaimOwnership(cleanup, releases, nil)
			}
		}
		if err != nil {
			child.quarantine()
			s.recordCleanupError("processor ownership cleanup incomplete")
		}
	}()
	for {
		var view processorOwnershipView
		select {
		case <-s.ctx.Done():
			return nil
		case view = <-views:
		}
		// An owned client cannot be replaced while its old SDK close/drain is
		// unproved. Keep that entitlement charged until the actual worker exits.
		for partition, worker := range active {
			select {
			case <-worker.done:
				delete(active, partition)
				continue
			default:
			}
			generation, err := s.store.ownershipFence(s.ctx, partition)
			if err != nil || generation != worker.generation {
				worker.cancel()
			}
		}
		claims := processorClaimCandidates(view.rows, s.store, time.Now())
		for _, claim := range claims {
			if worker := active[claim.PartitionID]; worker != nil {
				continue
			}
			op, release, err := s.receiveAdmission(false)
			if err != nil {
				if s.ctx.Err() != nil {
					return nil
				}
				continue
			}
			budget, cancel := context.WithTimeout(op, 5*time.Second)
			admitted, err := s.store.ClaimOwnership(budget, []eh.Ownership{claim}, nil)
			if err == nil && len(admitted) == 1 {
				if budget.Err() != nil {
					cancel()
					release()
					continue
				}
				current, readErr := s.store.records.Read(budget, s.store.prefix()+url.PathEscape(claim.PartitionID))
				if readErr != nil {
					err = readErr
				} else if budget.Err() != nil {
					err = budget.Err()
				} else if current.Owner != s.store.owner || current.Generation == "" {
					err = errCheckpointFence
				} else {
					position := eh.StartPosition{}
					yes := true
					if current.Sequence != nil {
						if current.Offset != "" {
							offset := current.Offset
							position.Offset = &offset
						} else {
							seq := *current.Sequence
							position.SequenceNumber = &seq
						}
					} else if start == "earliest" {
						position.Earliest = &yes
					} else {
						position.Latest = &yes
					}
					workerLease, acquireErr := child.acquireWorker(claim.PartitionID)
					if acquireErr != nil {
						err = acquireErr
					} else {
						partition, openErr := factory(claim.PartitionID, current.Generation, position)
						if openErr != nil || partition == nil {
							if partition != nil {
								s.closePartition(partition, workerLease, nil)
							} else {
								workerLease.release()
							}
							err = openErr
							if err == nil {
								err = errors.New("partition factory returned no client")
							}
						} else {
							workerCtx, workerCancel := context.WithCancel(s.ctx)
							entry := &governedWorker{generation: current.Generation, cancel: workerCancel, done: make(chan struct{})}
							active[claim.PartitionID] = entry
							workers.Add(1)
							go func() {
								defer workers.Done()
								defer close(entry.done)
								s.partitionWorkerContext(partition, workerLease, workerCtx, entry.generation)
							}()
						}
					}
				}
			}
			cancelled := op.Err() != nil || budget.Err() != nil
			cancel()
			release()
			if err != nil && !errors.Is(err, errCheckpointFence) && s.ctx.Err() == nil {
				// Admission cancellation/demand turnover is not a transport
				// failure. A later heartbeat retries from durable ownership.
				if !cancelled {
					s.fail(errors.New("processor partition admission failed"))
					return nil
				}
			}
		}
	}
}

// Deterministic balanced claims: renew this owner's persisted partitions first,
// then at most one free/expired or overfull owner's partition per heartbeat.
// Listing completeness is already bounded/validated by the fenced store.
func processorClaimCandidates(rows []eh.Ownership, store *fencedCheckpointStore, now time.Time) []eh.Ownership {
	byPartition := map[string]eh.Ownership{}
	owners := map[string]int{store.owner: 0}
	own := []eh.Ownership{}
	for _, row := range rows {
		byPartition[row.PartitionID] = row
		if row.OwnerID == store.owner {
			own = append(own, row)
		}
		if row.OwnerID != "" && now.Sub(row.LastModifiedTime) < processorOwnershipTTL {
			owners[row.OwnerID]++
		}
	}
	ids := []string{}
	for id := range store.partitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	sort.Slice(own, func(i, j int) bool { return own[i].PartitionID < own[j].PartitionID })
	maximum := (len(ids) + len(owners) - 1) / len(owners)
	if len(own) >= maximum {
		return own
	}
	minimum := len(ids) / len(owners)
	var candidate *eh.Ownership
	for _, id := range ids {
		row, present := byPartition[id]
		if present && row.OwnerID == store.owner {
			continue
		}
		free := !present || row.OwnerID == "" || now.Sub(row.LastModifiedTime) >= processorOwnershipTTL
		steal := present && owners[row.OwnerID] > maximum || present && len(own) < minimum && owners[row.OwnerID] >= maximum
		if free || steal {
			row.FullyQualifiedNamespace, row.EventHubName, row.ConsumerGroup, row.PartitionID = store.namespace, store.hub, store.group, id
			row.OwnerID = store.owner
			candidate = &row
			break
		}
	}
	if candidate != nil {
		own = append(own, *candidate)
	}
	return own
}
