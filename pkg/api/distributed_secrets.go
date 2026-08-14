package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

type distributedRunSecretSnapshot struct {
	ID     string
	Values map[string]string
}

func cloneDistributedSecretValues(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}

func clearDistributedSecretValues(values map[string]string) {
	for name := range values {
		values[name] = ""
		delete(values, name)
	}
}

func clearDistributedRunSecretSnapshotsLocked(test *ManagedTest) {
	if test == nil {
		return
	}
	for groupName, snapshot := range test.remoteSecretSnapshots {
		clearDistributedSecretValues(snapshot.Values)
		delete(test.remoteSecretSnapshots, groupName)
	}
	test.remoteSecretSnapshots = nil
}

func (tr *TestRunner) clearDistributedRunSecretSnapshots(test *ManagedTest) {
	if tr == nil || test == nil {
		return
	}
	tr.mu.Lock()
	if tr.tests[test.ID] == test {
		clearDistributedRunSecretSnapshotsLocked(test)
	}
	tr.mu.Unlock()
}

func (tr *TestRunner) storeDistributedRunSecretSnapshot(test *ManagedTest, groupName, id string, values map[string]string) error {
	if tr == nil || test == nil {
		return fmt.Errorf("managed distributed run is unavailable")
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.tests[test.ID] != test || test.executionCtx == nil || test.executionCtx.Err() != nil {
		return fmt.Errorf("distributed run is no longer active")
	}
	if test.remoteSecretSnapshots == nil {
		test.remoteSecretSnapshots = make(map[string]distributedRunSecretSnapshot)
	}
	if previous, exists := test.remoteSecretSnapshots[groupName]; exists {
		clearDistributedSecretValues(previous.Values)
	}
	test.remoteSecretSnapshots[groupName] = distributedRunSecretSnapshot{
		ID:     strings.TrimSpace(id),
		Values: cloneDistributedSecretValues(values),
	}
	return nil
}

func (tr *TestRunner) distributedRunSecretSnapshot(test *ManagedTest, groupName string) (distributedRunSecretSnapshot, bool) {
	if tr == nil || test == nil {
		return distributedRunSecretSnapshot{}, false
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	if tr.tests[test.ID] != test {
		return distributedRunSecretSnapshot{}, false
	}
	snapshot, ok := test.remoteSecretSnapshots[groupName]
	if !ok {
		return distributedRunSecretSnapshot{}, false
	}
	snapshot.Values = cloneDistributedSecretValues(snapshot.Values)
	return snapshot, true
}

func resolveDistributedSecretBundle(script *dsl.Script, resolver engine.SecretResolver) (map[string]string, error) {
	names, err := engine.ReferencedSecretNames(script)
	if err != nil {
		return nil, fmt.Errorf("inspect secret references")
	}
	if len(names) == 0 {
		return nil, nil
	}
	if resolver == nil {
		return nil, fmt.Errorf("controller project secret resolver is unavailable")
	}
	ctx, cancel := engine.WithSecretResolutionBudget(context.Background())
	defer cancel()
	bundle, missing, err := engine.ResolveSecretNames(ctx, names, resolver)
	if err != nil {
		return nil, fmt.Errorf("resolve controller project secrets: %w", err)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("configure the following project secret(s) before distributed execution: %s", strings.Join(missing, ", "))
	}
	if err := generatorrpc.ValidateSecretBundle(bundle); err != nil {
		return nil, err
	}
	return bundle, nil
}

func validateDistributedSecretTransport(bundle map[string]string) error {
	if len(bundle) == 0 {
		return nil
	}
	if !generatorrpc.TransportSecurityFromEnv().AllowsSecretDelivery() {
		return fmt.Errorf("controller-managed secrets require authenticated generator TLS; configure mTLS or verified TLS plus APEXLOAD_GRPC_AUTH_TOKEN before starting this saved script")
	}
	return nil
}

func validateDistributedSecretTarget(target string, snapshot generatorLoadSnapshot, bundle map[string]string) error {
	if len(bundle) == 0 {
		return nil
	}
	security := generatorrpc.TransportSecurityFromEnv()
	if security.AuthenticatedTunnel {
		if _, reverse := generatorrpc.ReverseGeneratorID(target); !reverse && !(security.TLS && (security.MutualTLS || security.Token)) {
			return fmt.Errorf("generator %q is direct, but this controller's authenticated transport is configured only for reverse WSS tunnels", target)
		}
	}
	if !snapshot.HeartbeatOK || !stringSliceContainsEqualFold(snapshot.CompatibleFeatures, generatorrpc.FeatureSecretBundleV1) {
		return fmt.Errorf("generator %q cannot receive controller-managed secrets; upgrade the generator to advertise %s before starting this saved script", target, generatorrpc.FeatureSecretBundleV1)
	}
	return nil
}
