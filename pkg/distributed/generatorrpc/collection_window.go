package generatorrpc

import (
	"sort"
	"strings"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MaxMetricsCollectionWindows bounds provenance independently of the generator's
// configurable assignment limit. A larger cohort is explicitly incomplete.
const MaxMetricsCollectionWindows = 256

// MetricsCollectionWindow is a result-buffer drain interval on the generator's
// published clock, at nanosecond precision. It describes buffer observation,
// not action start/end time or qualified cross-host wall-time coverage. Group
// is scoped to the parent batch's source epoch and collection sequence.
type MetricsCollectionWindow struct {
	Group string    `json:"group"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// MetricsCollectionWindowFromTelemetry derives a single collection window from
// a batch's telemetry timestamps and WindowMs when explicit per-group
// CollectionWindows are absent.
func MetricsCollectionWindowFromTelemetry(telemetry *MetricsTelemetry, fallbackEndUnixMs ...int64) (MetricsCollectionWindow, bool) {
	if telemetry == nil || telemetry.WindowMs <= 0 {
		return MetricsCollectionWindow{}, false
	}
	endMs := telemetry.NewestCollectedAtUnixMs
	if endMs <= 0 {
		endMs = telemetry.CollectedAtUnixMs
	}
	if endMs <= 0 && len(fallbackEndUnixMs) > 0 {
		endMs = fallbackEndUnixMs[0]
	}
	if endMs <= 0 {
		return MetricsCollectionWindow{}, false
	}
	startMs := endMs - telemetry.WindowMs
	if startMs <= 0 || endMs <= startMs {
		return MetricsCollectionWindow{}, false
	}
	return MetricsCollectionWindow{
		Group: "default",
		Start: time.UnixMilli(startMs).UTC(),
		End:   time.UnixMilli(endMs).UTC(),
	}, true
}

func validCollectionWindow(window MetricsCollectionWindow) bool {
	return strings.TrimSpace(window.Group) != "" && len(window.Group) <= maxGeneratorGroupNameBytes &&
		!window.Start.IsZero() && window.End.After(window.Start) &&
		timestamppb.New(window.Start).CheckValid() == nil && timestamppb.New(window.End).CheckValid() == nil
}

// collectionWindowsToProto selects a stable bounded subset of the current
// collection's groups. Unknown/invalid intervals never become invented bounds.
func collectionWindowsToProto(values map[string]MetricsCollectionWindow) ([]*pb.MetricsCollectionWindow, bool) {
	groups := make([]string, 0, len(values))
	for group := range values {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	complete := len(groups) > 0 && len(groups) <= MaxMetricsCollectionWindows
	if len(groups) > MaxMetricsCollectionWindows {
		groups = groups[:MaxMetricsCollectionWindows]
	}
	var out []*pb.MetricsCollectionWindow
	for _, group := range groups {
		window := values[group]
		window.Group = group
		if !validCollectionWindow(window) {
			complete = false
			continue
		}
		out = append(out, &pb.MetricsCollectionWindow{Group: group, Start: timestamppb.New(window.Start), End: timestamppb.New(window.End)})
	}
	return out, complete
}

func collectionWindowsFromProto(values []*pb.MetricsCollectionWindow, claimedComplete bool) ([]MetricsCollectionWindow, bool) {
	complete := claimedComplete && len(values) > 0 && len(values) <= MaxMetricsCollectionWindows
	if len(values) > MaxMetricsCollectionWindows {
		values = values[:MaxMetricsCollectionWindows]
	}
	seen := make(map[string]bool, len(values))
	var out []MetricsCollectionWindow
	for _, value := range values {
		if value == nil || value.Start.CheckValid() != nil || value.End.CheckValid() != nil {
			complete = false
			continue
		}
		window := MetricsCollectionWindow{Group: value.Group, Start: value.Start.AsTime(), End: value.End.AsTime()}
		if !validCollectionWindow(window) || seen[window.Group] {
			complete = false
			continue
		}
		seen[window.Group] = true
		out = append(out, window)
	}
	return out, complete
}

// archiveCollectionWindows retains source collection identity independently of
// a later broker envelope. A recorded interval is not proof that publication
// reached a controller or that the controller durably committed its cursor.
type archiveCollectionWindows struct {
	SourceEpoch         string                    `json:"source_epoch"`
	ExecutionGeneration uint64                    `json:"execution_generation"`
	CollectionSequence  uint64                    `json:"collection_sequence"`
	Complete            bool                      `json:"complete"`
	Windows             []MetricsCollectionWindow `json:"windows"`
}

func archiveCollectionWindowsFromBatch(batch *pb.MetricsBatch) *archiveCollectionWindows {
	if batch == nil || batch.GetTelemetry().GetCollectionSequence() == 0 {
		return nil
	}
	windows, complete := collectionWindowsFromProto(batch.GetCollectionWindows(), batch.GetCollectionWindowsComplete())
	epoch := batch.GetSourceEpoch()
	if epoch == "" {
		// The archive is written before broker publication; that broker uses
		// this same immutable process identity when it stamps the envelope.
		epoch = processSourceEpoch.id
	}
	return &archiveCollectionWindows{SourceEpoch: epoch, ExecutionGeneration: batch.GetExecutionGeneration(),
		CollectionSequence: batch.GetTelemetry().GetCollectionSequence(), Complete: complete, Windows: windows}
}
