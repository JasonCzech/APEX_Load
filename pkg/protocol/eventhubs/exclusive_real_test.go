//go:build eventhubs_real

package eventhubs

import (
	"context"
	"errors"
	eh "github.com/Azure/azure-sdk-for-go/sdk/messaging/azeventhubs/v2"
	"testing"
)

func TestExclusiveSDKOptionsDisablePrefetchAndCopyEpoch(t *testing.T) {
	level := int64(7)
	latest := true
	cfg := config{ConsumerMode: "exclusive", OwnerLevel: &level, EventHub: "hub", ConsumerGroup: "workers", PartitionID: "0"}
	options, err := exclusivePartitionOptions(cfg, eh.StartPosition{Latest: &latest})
	if err != nil {
		t.Fatal(err)
	}
	level = 9
	if options.OwnerLevel == nil || *options.OwnerLevel != 7 || options.Prefetch != -1 || options.StartPosition.Latest == nil || !*options.StartPosition.Latest {
		t.Fatalf("options=%+v", options)
	}
	observer, err := exclusivePartitionOptions(config{}, eh.StartPosition{})
	if err != nil {
		t.Fatal(err)
	}
	if observer.OwnerLevel != nil || observer.Prefetch != 0 {
		t.Fatal("observer became exclusive")
	}
}

func TestExclusiveOwnershipLossFencesFutureReadsWithoutNetwork(t *testing.T) {
	client := &realClient{cfg: config{ConsumerMode: "exclusive", EventHub: "hub", ConsumerGroup: "workers"}}
	if err := client.exclusiveReceiveError("hub", "other"); err == nil {
		t.Fatal("consumer scope override accepted")
	}
	if err := client.exclusiveReceiveError("other", "workers"); err == nil {
		t.Fatal("hub scope override accepted")
	}
	if err := client.recordExclusiveOwnershipLoss(context.DeadlineExceeded); !errors.Is(err, context.DeadlineExceeded) || client.ownershipLost != nil {
		t.Fatal("deadline became lost ownership")
	}
	original := &eh.Error{Code: eh.ErrorCodeOwnershipLost}
	err := client.recordExclusiveOwnershipLoss(original)
	if !errors.Is(err, original) || client.ownershipLost == nil {
		t.Fatal("ownership loss not retained")
	}
	if _, lag, nextErr := client.Receive(context.Background(), "hub", "workers", 0); nextErr != err || lag != -1 {
		t.Fatalf("subsequent read=%v lag=%d", nextErr, lag)
	}
	observer := &realClient{}
	if observer.recordExclusiveOwnershipLoss(original) != original || observer.ownershipLost != nil {
		t.Fatal("observer unexpectedly fenced")
	}
}

func TestExclusiveInitIsLazyAndAdvertisesRuntime(t *testing.T) {
	e := &EventHubs{}
	if err := e.Init(context.Background(), map[string]any{"eventhubs_consumer_mode": "exclusive", "eventhubs_namespace": "namespace.servicebus.windows.net", "eventhubs_auth_type": "aad", "eventhubs_event_hub": "hub", "eventhubs_consumer_group": "workers", "eventhubs_partition_id": "0", "eventhubs_owner_level": 0}); err != nil {
		t.Fatal(err)
	}
	c := e.client.(*realClient)
	if c.cred != nil || len(c.consumers) != 0 || len(c.partitions) != 0 || c.cfg.OwnerLevel == nil || *c.cfg.OwnerLevel != 0 {
		t.Fatal("Init opened credentials or receivers")
	}
	found := false
	for _, feature := range e.RuntimeCapabilities().Features {
		found = found || feature == FeatureExclusiveConsumerV1
	}
	if !found {
		t.Fatal("exclusive capability missing")
	}
}

func TestExclusiveSDKOptionsRejectMissingEpochInsteadOfNonExclusiveFallback(t *testing.T) {
	if options, err := exclusivePartitionOptions(config{ConsumerMode: "exclusive", EventHub: "hub", ConsumerGroup: "workers", PartitionID: "0"}, eh.StartPosition{}); err == nil || options != nil {
		t.Fatal("exclusive mode silently became an observer")
	}
}
