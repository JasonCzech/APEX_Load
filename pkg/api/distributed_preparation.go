package api

import (
	"context"
	"errors"
	"fmt"
	"sync"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

type preparationReadinessClient interface {
	CheckPreparation(context.Context, string, uint64, map[string]int32, []byte) error
}

type preparationResourceReadinessClient interface {
	CheckPreparationResources(context.Context, string, uint64, map[string]int32, []byte, map[string]*pb.RuntimeResourceClaim) (map[string]*pb.RuntimeResourceAdmission, error)
}

func preparationCapabilityError(features []string) error {
	for _, feature := range []string{generatorrpc.FeaturePreparationV1, generatorrpc.FeaturePreparationReadinessV1} {
		if !stringSliceContainsEqualFold(features, feature) {
			return fmt.Errorf("generator lacks %s; traffic-free preparation unavailable", feature)
		}
	}
	return nil
}

// Retain immutable placement and a bounded control-plane-only peer check before
// publishing ready. The normal RUNNING transition populates live stream state.
func (b *distributedExecutionBackend) installPreparationReadiness(tr *TestRunner, test *ManagedTest, shards map[string][]GeneratorShardSummary, clients map[string]generatorClient, generation uint64, token []byte) error {
	expected := make(map[string]map[string]int32)
	resourceClaims := make(map[string]map[string]*pb.RuntimeResourceClaim)
	for _, shard := range flattenGroupShards(shards) {
		if shard.VUsers < 0 || int64(shard.VUsers) > int64(^uint32(0)>>1) {
			return errors.New("invalid prepared shard size")
		}
		if expected[shard.Target] == nil {
			expected[shard.Target] = make(map[string]int32)
		}
		if _, exists := expected[shard.Target][shard.Group]; exists {
			return errors.New("duplicate prepared shard")
		}
		expected[shard.Target][shard.Group] = int32(shard.VUsers)
		if shard.RuntimeResourceClaim != nil {
			if !generatorrpc.ValidRuntimeResourceAdmission(shard.RuntimeResourceClaim, shard.RuntimeResourceAdmission) {
				return errors.New("prepared resource admission unavailable")
			}
			if resourceClaims[shard.Target] == nil {
				resourceClaims[shard.Target] = make(map[string]*pb.RuntimeResourceClaim)
			}
			resourceClaims[shard.Target][shard.Group] = shardRuntimeResourceClaim(shard)
		}
	}
	if len(expected) == 0 || len(expected) != len(clients) {
		return errors.New("prepared placement does not match reserved peers")
	}
	peers := make(map[string]preparationReadinessClient, len(clients))
	for target, client := range clients {
		peer, ok := client.(preparationReadinessClient)
		if !ok || expected[target] == nil {
			return errors.New("generator cannot verify preparation reservations")
		}
		if len(resourceClaims[target]) > 0 {
			if _, ok := client.(preparationResourceReadinessClient); !ok {
				return errors.New("generator cannot verify processor resource reservations")
			}
		}
		peers[target] = peer
	}
	immutableToken := append([]byte(nil), token...)
	check := func(ctx context.Context) error {
		// A fixed worker pool prevents unbounded fan-out and waits for every worker
		// before clients can be closed by startup cleanup.
		targets := make(chan string, len(peers))
		for target := range peers {
			targets <- target
		}
		close(targets)
		errs := make(chan error, len(peers))
		var wg sync.WaitGroup
		for range min(len(peers), maxGeneratorEpochKeeperWorkers) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for target := range targets {
					if err := ctx.Err(); err != nil {
						errs <- err
						continue
					}
					tr.mu.RLock()
					fenced := test.remoteRunSupervisionFencedTargets[target]
					tr.mu.RUnlock()
					if fenced || b.testExecutionLeaseFenced(test, target) {
						errs <- errors.New("prepared peer lease fenced")
						continue
					}
					if len(resourceClaims[target]) > 0 {
						peer := clients[target].(preparationResourceReadinessClient)
						admissions, err := peer.CheckPreparationResources(ctx, test.ID, generation, expected[target], immutableToken, generatorrpc.CloneRuntimeResourceClaims(resourceClaims[target]))
						valid := err == nil && len(admissions) == len(resourceClaims[target])
						for group, claim := range resourceClaims[target] {
							valid = valid && generatorrpc.ValidRuntimeResourceAdmission(claim, admissions[group])
						}
						if !valid {
							errs <- errors.New("prepared processor resource reservation unavailable")
						}
					} else if err := peers[target].CheckPreparation(ctx, test.ID, generation, expected[target], immutableToken); err != nil {
						// Keep transport errors/credential-bearing endpoints out of run errors.
						errs <- errors.New("prepared peer reservation unavailable")
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			return err
		}
		return nil
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.tests[test.ID] != test || test.executionCtx == nil || test.executionCtx.Err() != nil {
		return errors.New("preparation execution no longer current")
	}
	test.GeneratorShards = flattenGroupShards(shards)
	test.preparationPeerCheck = check
	return nil
}
