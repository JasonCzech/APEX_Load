package api

import (
	"fmt"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func apiMessagingSummary(s *protocol.MessagingSummary) *ServerTelemetrySummary {
	if s == nil {
		return nil
	}
	return &ServerTelemetrySummary{Messaging: s.Clone()}
}
func hasMessagingPartitionSkewBreach(live *LiveMetrics, threshold *float64) bool {
	if live == nil || threshold == nil {
		return false
	}
	if live.Telemetry != nil && (live.Telemetry.DroppedResults > 0 || live.Telemetry.MissingMetricWindows > 0 || live.Telemetry.DroppedSubscriberBatches > 0) {
		return false
	}
	if len(live.Messaging) > 0 {
		bySource := map[[3]string]*protocol.MessagingSummary{}
		for _, s := range live.Messaging {
			if s == nil {
				continue
			}
			if s.Incomplete {
				return false
			}
			for _, d := range s.Dimensions {
				key := [3]string{d.Protocol, d.Group, d.Source}
				merged := bySource[key]
				protocol.MergeMessagingSummary(&merged, &protocol.MessagingSummary{Dimensions: []protocol.MessagingDimension{d}})
				bySource[key] = merged
			}
		}
		for _, s := range bySource {
			if value, ok := s.ObservedPartitionSkew(); ok && value > *threshold {
				return true
			}
		}
		return false
	}
	for _, tx := range live.Transactions {
		if tx == nil || tx.ServerTelemetry == nil {
			continue
		}
		if value, ok := tx.ServerTelemetry.Messaging.ObservedPartitionSkew(); ok && value > *threshold {
			return true
		}
	}
	return false
}

// Explicit skew goals require mergeable evidence from every admitted generator.
func messagingGoalCapabilityError(goal *GoalConfig, features []string) error {
	if goal != nil && goal.MaxPartitionSkewPct != nil {
		for _, feature := range []string{generatorrpc.FeatureMessagingRollupV1, generatorrpc.FeatureMessagingScopeV1} {
			if !stringSliceContainsEqualFold(features, feature) {
				return fmt.Errorf("partition skew goal requires %s; no traffic was started", feature)
			}
		}
	}
	return nil
}
