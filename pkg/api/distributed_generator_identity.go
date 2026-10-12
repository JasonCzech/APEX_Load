package api

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// describeRemoteTargetLocked names a generator the way the archive's source
// inventory does (RunArchiveSource.Label): its ID, address, region and the
// groups it ran, or its address alone. The caller holds tr.mu.
func (b *distributedExecutionBackend) describeRemoteTargetLocked(test *ManagedTest, target string) string {
	source := RunArchiveSource{Target: strings.TrimSpace(target)}
	if batch, ok := test.remoteMetricsBatchesByTarget[source.Target]; ok {
		source.GeneratorID = strings.TrimSpace(batch.GeneratorID)
	}
	for _, shard := range test.GeneratorShards {
		group := strings.TrimSpace(shard.Group)
		if strings.TrimSpace(shard.Target) == source.Target && group != "" && !slices.Contains(source.Groups, group) {
			source.Groups = append(source.Groups, group)
		}
	}
	if b != nil {
		source.Region = strings.TrimSpace(b.regionForTarget(source.Target))
	}
	return source.Label()
}

// remoteTargetFailureMessageLocked says which generator ended the run, when,
// and why: "Generator gen-b (10.0.0.7:9090; us-west; group Store_Cart) failed
// 1m38s into the run: generator shut down before test completion". The caller
// holds tr.mu.
func (b *distributedExecutionBackend) remoteTargetFailureMessageLocked(test *ManagedTest, target, what, cause string, now time.Time) string {
	message := fmt.Sprintf("Generator %s %s", b.describeRemoteTargetLocked(test, target), what)
	if test.StartedAt != nil && now.After(*test.StartedAt) {
		message += " " + now.Sub(*test.StartedAt).Round(time.Second).String() + " into the run"
	}
	if cause = strings.TrimSpace(cause); cause != "" {
		message += ": " + cause
	}
	return message
}

// remoteTargetLossMessageLocked says which generator was lost, when and how,
// and what that means for the run's live tables: a generator lost this way
// could not settle its live-table work. The caller holds tr.mu.
func (b *distributedExecutionBackend) remoteTargetLossMessageLocked(test *ManagedTest, target string, cause error, now time.Time) string {
	message := b.remoteTargetFailureMessageLocked(test, target, "was lost", fmt.Sprintf("its metrics stream stopped: %v", cause), now)
	if note := liveTableLossNote(testLiveTableAliases(test)); note != "" {
		message = strings.TrimSuffix(message, ".") + "." + note
	}
	return message
}

// testLiveTableAliases lists the run's live-table aliases. The caller holds tr.mu.
func testLiveTableAliases(test *ManagedTest) []string {
	var aliases []string
	for _, binding := range test.DataPools {
		if alias := strings.TrimSpace(binding.Alias); binding.AccessMode == "live" && alias != "" && !slices.Contains(aliases, alias) {
			aliases = append(aliases, alias)
		}
	}
	return aliases
}
