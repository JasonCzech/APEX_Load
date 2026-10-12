package api

import (
	"context"
	"fmt"
	"sort"

	"github.com/jasonczech/apexload/pkg/livetable"
)

type liveTableChannelClient interface {
	OpenLiveTables(context.Context, string, string, uint64, []byte, livetable.Client) error
}

func (tr *TestRunner) openLiveTableChannels(ctx context.Context, testID string, generation uint64, token []byte, shards map[string][]GeneratorShardSummary, clients map[string]generatorClient) error {
	if len(tr.liveBindings(testID)) == 0 {
		return nil
	}
	groups := make([]string, 0, len(shards))
	for group := range shards {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	for _, group := range groups {
		seen := map[string]bool{}
		for _, shard := range shards[group] {
			if seen[shard.Target] {
				continue
			}
			seen[shard.Target] = true
			// Completed participants are absent during adoption; their tables are still
			// restored before surviving participants can reconnect.
			base := clients[shard.Target]
			if base == nil {
				continue
			}
			client, ok := base.(liveTableChannelClient)
			if !ok {
				return fmt.Errorf("generator %q cannot open a live table channel", shard.Target)
			}
			if err := client.OpenLiveTables(ctx, testID, group, generation, token, tr.liveClient(testID, shard.Target+"/"+group)); err != nil {
				return fmt.Errorf("open live table channel for generator %q group %q: %w", shard.Target, group, err)
			}
		}
	}
	return nil
}
