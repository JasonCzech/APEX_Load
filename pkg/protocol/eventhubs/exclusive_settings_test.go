package eventhubs

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestExclusiveSettingsRequireExplicitScopeAndPreserveObserver(t *testing.T) {
	zero := int64(0)
	if err := ValidateExclusiveSettings("exclusive", "hub", "workers", "0", &zero, false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mode, hub, group, partition string
		level                             *int64
		mock                              bool
	}{
		{"observer without level", "observer", "", "", "", nil, true},
		{"processor without level", "processor", "hub", "workers", "", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateExclusiveSettings(tc.mode, tc.hub, tc.group, tc.partition, tc.level, tc.mock); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct {
		name, mode, hub, group, partition string
		level                             *int64
		mock                              bool
	}{
		{"missing hub", "exclusive", "", "workers", "0", &zero, false},
		{"missing group", "exclusive", "hub", "", "0", &zero, false},
		{"blank partition", "exclusive", "hub", "workers", " ", &zero, false},
		{"missing epoch", "exclusive", "hub", "workers", "0", nil, false},
		{"mock", "exclusive", "hub", "workers", "0", &zero, true},
		{"observer epoch", "observer", "hub", "workers", "0", &zero, false},
		{"processor epoch", "processor", "hub", "workers", "", &zero, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateExclusiveSettings(tc.mode, tc.hub, tc.group, tc.partition, tc.level, tc.mock); err == nil {
				t.Fatal("unsupported ownership accepted")
			}
		})
	}
}

func TestExclusiveOwnerLevelParsingPreservesZeroAndRejectsUnsafeNumbers(t *testing.T) {
	for _, raw := range []any{0, int64(0), float64(0), "0", MaxExclusiveOwnerLevel} {
		level, err := parseOwnerLevel(raw)
		if err != nil || level == nil {
			t.Fatalf("%v: %v", raw, err)
		}
	}
	if value, err := parseOwnerLevel(nil); err != nil || value != nil {
		t.Fatal("absent epoch became zero")
	}
	for _, raw := range []any{-1, int64(-1), float64(-1), 0.5, math.NaN(), math.Inf(1), float64(1 << 53), int64(1 << 53), "9007199254740992", "{{run.id}}", true, []int{1}} {
		if _, err := parseOwnerLevel(raw); err == nil {
			t.Fatalf("unsafe epoch accepted: %v", raw)
		}
	}
}

func TestExclusiveInitRejectsMockAndIncompleteConfigurationBeforeClients(t *testing.T) {
	e := &EventHubs{}
	if err := e.Init(context.Background(), map[string]any{"eventhubs_consumer_mode": "exclusive", "eventhubs_mock": true, "eventhubs_event_hub": "hub", "eventhubs_consumer_group": "workers", "eventhubs_partition_id": "0", "eventhubs_owner_level": 0}); err == nil || !strings.Contains(err.Error(), "non-mock") {
		t.Fatal(err)
	}
	if e.client != nil {
		t.Fatal("client created before ownership validation")
	}
	if err := ValidateProcessorSettings("exclusive", "", "", "", "", "", false); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProcessorSettings("exclusive", "https://store/container", "", "", "", "", false); err == nil {
		t.Fatal("exclusive checkpoint store accepted")
	}
}

func TestExclusiveConsumerDoesNotInheritObserverGroupDefault(t *testing.T) {
	t.Setenv("APEXLOAD_EVENTHUBS_PROBE_CONSUMER_GROUP", "observer-workers")
	e := &EventHubs{}
	err := e.Init(context.Background(), map[string]any{"eventhubs_consumer_mode": "exclusive", "eventhubs_namespace": "namespace.servicebus.windows.net", "eventhubs_event_hub": "hub", "eventhubs_partition_id": "0", "eventhubs_owner_level": 0})
	if err == nil || !strings.Contains(err.Error(), "explicit") || e.client != nil {
		t.Fatal("exclusive receiver inherited observer ownership scope", err)
	}
}
