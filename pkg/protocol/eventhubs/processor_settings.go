package eventhubs

import (
	"context"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	"net/url"
	"sync"
)

const FeatureProcessorCheckpointsV1 = "eventhubs_processor_checkpoints_v1"

func (*EventHubs) RuntimeCapabilities() protocol.RuntimeCapabilities {
	out := protocol.RuntimeCapabilities{Available: true}
	if realEventHubsAvailable {
		out.Features = []string{FeatureProcessorCheckpointsV1, FeatureExclusiveConsumerV1}
		if capacity := (&EventHubs{}).RuntimeResourceCapacity(); capacity.Available {
			out.Features = append(out.Features, FeatureProcessorReservationsV1)
		}
	}
	return out
}
func ValidateProcessorSettings(mode, storage, auth, namespace, group, partition string, mock bool) error {
	if mode != "" && mode != "observer" && mode != "processor" && mode != "exclusive" {
		return fmt.Errorf("eventhubs consumer_mode must be observer, processor or exclusive")
	}
	if mode != "processor" {
		if storage != "" {
			return fmt.Errorf("checkpoint storage requires processor mode")
		}
		return nil
	}
	u, err := url.Parse(storage)
	if err != nil || len(storage) > 1024 || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" {
		return fmt.Errorf("processor requires an HTTPS checkpoint_container_url")
	}
	if mock || auth != "aad" || namespace == "" || group == "" || partition != "" {
		return fmt.Errorf("Event Hubs processor requires non-mock AAD namespace, explicit consumer_group and no fixed partition_id")
	}
	return nil
}

// The worker retains one event until its extraction/assertion boundary and
// fenced durable checkpoint complete. Rejected events remain eligible for retry.
type processorAck struct {
	ctx    context.Context
	accept bool
}
type processorReceipt struct {
	once     sync.Once
	acks     chan processorAck
	result   chan error
	lifetime context.Context
}

func (r *processorReceipt) reject() { r.once.Do(func() { r.acks <- processorAck{accept: false} }) }
func (r *processorReceipt) finish(ctx context.Context) error {
	sent := false
	r.once.Do(func() { sent = true; r.acks <- processorAck{ctx: ctx, accept: true} })
	if !sent {
		return fmt.Errorf("processor event receipt was already resolved")
	}
	select {
	case err := <-r.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-r.lifetime.Done():
		return fmt.Errorf("processor ownership or lifecycle ended before checkpoint")
	}
}
