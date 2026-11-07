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

// resolveDistributedSecretBundle resolves every referenced secret name but
// fails closed only on a missing REQUIRED name (engine.RequiredSecretNames):
// an unbound secret behind an inactive proxy credential never blocks
// distributed execution. A bound referenced-but-not-required name still rides
// in the bundle, because a generator that does not advertise
// FeatureSecretBundleRequiredOnlyV1 demands every referenced name; upgraded
// generators admit it without demanding it. When the transport cannot carry
// secrets, those optional names are withheld rather than failing the run, and
// validateDistributedSecretTarget decides whether the target can run without
// them.
func resolveDistributedSecretBundle(script *dsl.Script, resolver engine.SecretResolver) (map[string]string, error) {
	names, err := engine.ReferencedSecretNames(script)
	if err != nil {
		return nil, fmt.Errorf("inspect secret references")
	}
	required, err := engine.RequiredSecretNames(script)
	if err != nil {
		return nil, fmt.Errorf("inspect secret references")
	}
	if len(names) == 0 {
		return nil, nil
	}
	if resolver == nil {
		if len(required) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("controller project secret resolver is unavailable")
	}
	ctx, cancel := engine.WithSecretResolutionBudget(context.Background())
	defer cancel()
	bundle, missing, err := engine.ResolveSecretNames(ctx, names, resolver)
	if err != nil {
		return nil, fmt.Errorf("resolve controller project secrets: %w", err)
	}
	requiredSet := make(map[string]struct{}, len(required))
	for _, name := range required {
		requiredSet[strings.TrimSpace(name)] = struct{}{}
	}
	blocking := make([]string, 0, len(missing))
	for _, name := range missing {
		if _, ok := requiredSet[strings.TrimSpace(name)]; ok {
			blocking = append(blocking, name)
		}
	}
	if len(blocking) > 0 {
		clearDistributedSecretValues(bundle)
		return nil, engine.MissingSecretsError(script, "configure the following project secret(s) before distributed execution: ", blocking)
	}
	if !generatorrpc.TransportSecurityFromEnv().AllowsSecretDelivery() {
		for name := range bundle {
			if _, ok := requiredSet[name]; !ok {
				bundle[name] = ""
				delete(bundle, name)
			}
		}
	}
	if len(bundle) == 0 {
		return nil, nil
	}
	if err := generatorrpc.ValidateSecretBundle(bundle); err != nil {
		clearDistributedSecretValues(bundle)
		return nil, err
	}
	return bundle, nil
}

// distributedUnbundledSecretNames lists the referenced secret names a bundle
// does not carry. Required names are always bundled (or the run failed
// closed), so these are only names behind inactive proxy credentials; a
// generator must advertise FeatureSecretBundleRequiredOnlyV1 to run without
// them.
func distributedUnbundledSecretNames(script *dsl.Script, bundle map[string]string) ([]string, error) {
	names, err := engine.ReferencedSecretNames(script)
	if err != nil {
		return nil, fmt.Errorf("inspect secret references")
	}
	unbundled := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if _, ok := bundle[name]; !ok && name != "" {
			unbundled = append(unbundled, name)
		}
	}
	if len(unbundled) == 0 {
		return nil, nil
	}
	return unbundled, nil
}

// distributedInactiveBrowserProxyCredentials reports a browser script whose
// proxy username or password is set with no proxy server or bypass. Those
// credentials are inactive under the custom-only proxy rule, but a generator
// built before that rule rejects the script in DSL validation
// ("config.browser.proxy.server is required when proxy is set").
func distributedInactiveBrowserProxyCredentials(script *dsl.Script) bool {
	if script == nil || strings.TrimSpace(script.Protocol) != "browser" {
		return false
	}
	proxy := script.Config.Browser.Proxy
	if strings.TrimSpace(proxy.Server) != "" || strings.TrimSpace(proxy.Bypass) != "" {
		return false
	}
	return strings.TrimSpace(proxy.Username) != "" || strings.TrimSpace(proxy.Password) != ""
}

// validateDistributedBrowserProxyTarget refuses, up front and with a remedy, a
// generator that would reject inactive browser proxy credentials late.
func validateDistributedBrowserProxyTarget(target string, snapshot generatorLoadSnapshot, script *dsl.Script) error {
	if !distributedInactiveBrowserProxyCredentials(script) {
		return nil
	}
	if snapshot.HeartbeatOK && stringSliceContainsEqualFold(snapshot.CompatibleFeatures, generatorrpc.FeatureSecretBundleRequiredOnlyV1) {
		return nil
	}
	return fmt.Errorf("generator %q cannot run this saved script because its browser proxy username/password are set without a proxy server; upgrade the generator to advertise %s, or clear the browser proxy credentials", target, generatorrpc.FeatureSecretBundleRequiredOnlyV1)
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

// validateDistributedSecretTarget checks that target can receive bundle and
// can run without the unbundled referenced names (see
// distributedUnbundledSecretNames). The unbundled check runs even when the
// bundle is empty: an older generator demands every referenced name, so it
// would otherwise reject the assignment late, naming a secret the run does
// not need.
func validateDistributedSecretTarget(target string, snapshot generatorLoadSnapshot, bundle map[string]string, unbundled []string) error {
	if len(unbundled) > 0 && (!snapshot.HeartbeatOK || !stringSliceContainsEqualFold(snapshot.CompatibleFeatures, generatorrpc.FeatureSecretBundleRequiredOnlyV1)) {
		remedy := "bind them to project secrets"
		if !generatorrpc.TransportSecurityFromEnv().AllowsSecretDelivery() {
			remedy = "bind them to project secrets and configure authenticated generator TLS"
		}
		return fmt.Errorf("generator %q cannot run this saved script without the secret(s) %s, which its current proxy settings do not use; upgrade the generator to advertise %s, or %s", target, strings.Join(unbundled, ", "), generatorrpc.FeatureSecretBundleRequiredOnlyV1, remedy)
	}
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
