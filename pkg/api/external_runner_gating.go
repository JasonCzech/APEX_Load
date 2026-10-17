package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

const javaVuserApprovalEvidenceKey = importreview.JavaVuserApprovalEvidenceKey

const (
	javaVuserRuntimeHealthyTTL   = 15 * time.Second
	javaVuserRuntimeUnhealthyTTL = 5 * time.Second
)

// javaVuserRuntimeHealthCache keeps launch gating from spawning a JVM health
// probe (a subprocess with a multi-second timeout) while the caller holds the
// global TestRunner lock. Results are cached with a short TTL; when a cached
// observation exists but has gone stale, the probe is refreshed in the
// background. Stale unhealthy observations remain fail-closed, and stale
// healthy observations also return false until the refresh confirms the runner
// is still usable. Concurrent cold callers join a single in-flight probe instead
// of forking one JVM each.
type javaVuserRuntimeHealthCache struct {
	mu        sync.Mutex
	checked   bool
	healthy   bool
	expiresAt time.Time
	inflight  chan struct{}
}

// javaVuserRuntimeHealth is process-global, matching the process-global
// protocol registry and APEXLOAD_JAVA_VUSER_* runner configuration it probes.
var javaVuserRuntimeHealth javaVuserRuntimeHealthCache

func (c *javaVuserRuntimeHealthCache) healthyNow(probe func() bool) bool {
	c.mu.Lock()
	if c.checked && time.Now().Before(c.expiresAt) {
		healthy := c.healthy
		c.mu.Unlock()
		return healthy
	}
	if c.inflight != nil {
		if c.checked {
			// Never authorize a launch from an expired observation. A caller that
			// cannot wait under the TestRunner lock must fail closed while the
			// existing refresh establishes current runtime health.
			c.mu.Unlock()
			return false
		}
		wait := c.inflight
		c.mu.Unlock()
		<-wait
		c.mu.Lock()
		healthy := c.healthy
		c.mu.Unlock()
		return healthy
	}
	done := make(chan struct{})
	c.inflight = done
	hasStale := c.checked
	c.mu.Unlock()

	run := func() (healthy bool) {
		defer func() {
			if recover() != nil {
				healthy = false
			}
			ttl := javaVuserRuntimeUnhealthyTTL
			if healthy {
				ttl = javaVuserRuntimeHealthyTTL
			}
			c.mu.Lock()
			c.checked = true
			c.healthy = healthy
			c.expiresAt = time.Now().Add(ttl)
			c.inflight = nil
			c.mu.Unlock()
			close(done)
		}()
		healthy = probe()
		return healthy
	}
	if hasStale {
		go run()
		return false
	}
	return run()
}

type authoringSessionReader interface {
	GetAuthoringSession(string) (*storage.AuthoringSession, error)
}

func externalRunnerLaunchConflicts(groups []TestGroup) []LaunchConflict {
	registered := make(map[string]struct{})
	for _, name := range protocol.List() {
		registered[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	externalRunnerProtocols := protocolBundleExternalRunnerProtocolSet()
	conflictsByProtocol := make(map[string]LaunchConflict)
	for _, group := range groups {
		protocolName := strings.ToLower(strings.TrimSpace(group.Protocol))
		if protocolName == "" && group.script != nil {
			protocolName = strings.ToLower(strings.TrimSpace(group.script.Protocol))
		}
		if protocolName == "" {
			continue
		}
		if _, ok := registered[protocolName]; ok {
			continue
		}
		if _, ok := externalRunnerProtocols[protocolName]; !ok {
			continue
		}
		conflictsByProtocol[protocolName] = LaunchConflict{
			Code:       "external_runner_required",
			Severity:   "blocking",
			Source:     "protocol_runtime",
			ResourceID: protocolName,
			Message:    externalRunnerRequiredMessage(protocolName),
		}
	}
	protocols := make([]string, 0, len(conflictsByProtocol))
	for protocolName := range conflictsByProtocol {
		protocols = append(protocols, protocolName)
	}
	sort.Strings(protocols)
	conflicts := make([]LaunchConflict, 0, len(protocols))
	for _, protocolName := range protocols {
		conflicts = append(conflicts, conflictsByProtocol[protocolName])
	}
	return conflicts
}

func protocolBundleExternalRunnerProtocolSet() map[string]struct{} {
	protocols := make(map[string]struct{})
	for _, entry := range protocolBundleSupportMatrix() {
		if entry.Status != "external_runner_required" && entry.ExecutionPosture != "external_runner_required" {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(entry.Protocol))
		if name == "" || name == "java_vuser" {
			continue
		}
		protocols[name] = struct{}{}
	}
	return protocols
}

func externalRunnerLaunchConflict(groups []TestGroup) *LaunchConflict {
	conflicts := externalRunnerLaunchConflicts(groups)
	if len(conflicts) == 0 {
		return nil
	}
	return &conflicts[0]
}

func (tr *TestRunner) javaVuserRunnerLaunchConflictsLocked(groups []TestGroup) []LaunchConflict {
	return tr.javaVuserRunnerLaunchConflictsForTestLocked(nil, groups)
}

// javaVuserRunnerLaunchConflictsForManagedTestLocked performs the launch-time
// Java runtime check with the complete assignment context. Distributed checks
// must be scoped to the targets this test/group can actually use; a capable
// generator elsewhere in the fleet cannot satisfy a pinned or labelled group.
func (tr *TestRunner) javaVuserRunnerLaunchConflictsForManagedTestLocked(test *ManagedTest) []LaunchConflict {
	if test == nil {
		return nil
	}
	return tr.javaVuserRunnerLaunchConflictsForTestLocked(test, test.Groups)
}

func (tr *TestRunner) javaVuserRunnerLaunchConflictsForTestLocked(test *ManagedTest, groups []TestGroup) []LaunchConflict {
	for _, group := range groups {
		if !groupUsesJavaVuserProtocol(group) {
			continue
		}
		if !groupRequiresJavaVuserRunner(group) {
			return []LaunchConflict{{
				Code:       "java_vuser_executable_action_required",
				Severity:   "blocking",
				Source:     "authoring_governance",
				ResourceID: firstNonEmpty(strings.TrimSpace(group.Name), "java_vuser"),
				Message:    "Review-only Java Vuser source cannot be launched. Approve a combined draft containing governed java_vuser_run lifecycle actions before creating a test.",
			}}
		}
		requiresPersistentLifecycle := groupRequiresJavaVuserPhaseLifecycle(group)
		if requiresPersistentLifecycle {
			if message := tr.javaVuserApprovalConflict(group.script); message != "" {
				return []LaunchConflict{{Code: "java_vuser_authoring_approval_required", Severity: "blocking", Source: "authoring_governance", ResourceID: "java_vuser", Message: message}}
			}
		}

		// A script that carries user-uploaded classpath JARs cannot run unless the
		// operator has opted into honoring them; otherwise the attached bindings
		// are silently ignored and the run fails later with an opaque
		// classpath_entry_unavailable. Surface that as a clear, blocking conflict.
		if scriptHasJavaClasspathAssets(group.script) && !config.JavaClasspathUploadEnabled() {
			return []LaunchConflict{{
				Code:       "java_vuser_classpath_asset_unresolved",
				Severity:   "blocking",
				Source:     "protocol_runtime",
				ResourceID: firstNonEmpty(strings.TrimSpace(group.Name), "java_vuser"),
				Message:    "This Java Vuser has user-uploaded classpath dependencies, but classpath JAR upload is not enabled on the runner (APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED). Enable it on the runner, or remove the attached JARs and provision the classpath on the runner.",
			}}
		}

		// User-uploaded classpath JARs are local-runner-only: their StorageRef is
		// a controller-local file:// path that a distributed generator cannot read
		// (it materializes to classpath_asset_unavailable, or worse silently loads
		// a same-named pre-provisioned JAR). Block distributed + assets at launch
		// rather than letting it silently degrade. (The flag-off case above already
		// covers both backends; this reaches only the flag-on distributed case.)
		if scriptHasJavaClasspathAssets(group.script) {
			if _, distributed := tr.execution.(*distributedExecutionBackend); distributed {
				return []LaunchConflict{{
					Code:       "java_vuser_classpath_asset_distributed_unsupported",
					Severity:   "blocking",
					Source:     "protocol_runtime",
					ResourceID: firstNonEmpty(strings.TrimSpace(group.Name), "java_vuser"),
					Message:    "User-uploaded Java Vuser classpath JARs are only supported on a local runner; distributed generators cannot materialize them yet. Run this test on a local runner, or provision the classpath on every generator and remove the attached JARs.",
				}}
			}
		}

		// The importer reserves apexload-script-classes.jar for the script's own
		// classes. Unbound, it names a file no runner has, and the run would fail
		// on the runner with an anonymous classpath_entry_unavailable.
		if javaScriptClassesEntryUnbound(group.script) {
			return []LaunchConflict{{
				Code:       "java_vuser_script_classes_missing",
				Severity:   "blocking",
				Source:     "protocol_runtime",
				ResourceID: firstNonEmpty(strings.TrimSpace(group.Name), "java_vuser"),
				Message:    "This Java Vuser's classpath reserves " + enterprisearchive.JavaVuserScriptClassesEntry + " for its compiled classes, but nothing is attached to it. Compile the script source or attach a jar of its classes in the import review, or remove the entry if every runner provides the classes another way.",
			}}
		}

		available := false
		if backend, ok := tr.execution.(*distributedExecutionBackend); ok {
			if test != nil {
				available = tr.distributedJavaVuserGroupRunnerAvailableLocked(backend, test, group)
			} else {
				available = tr.distributedJavaVuserRunnerAvailableLocked(backend, requiresPersistentLifecycle)
			}
		} else {
			available = tr.javaVuserRunnerAvailableLocked(requiresPersistentLifecycle)
		}
		if available {
			continue
		}
		resourceID := "java_vuser"
		if strings.TrimSpace(group.Name) != "" {
			resourceID = group.Name
		}
		return []LaunchConflict{{
			Code:       "java_vuser_runner_required",
			Severity:   "blocking",
			Source:     "protocol_runtime",
			ResourceID: resourceID,
			Message:    javaVuserRunnerRequiredMessage(requiresPersistentLifecycle),
		}}
	}
	return nil
}

func stampJavaVuserAuthoringApproval(script *dsl.Script, sessionID string) {
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	script.Metadata.Import.Preserved[javaVuserApprovalEvidenceKey] = map[string]any{
		"session_id":   strings.TrimSpace(sessionID),
		"content_hash": hashLibraryScript(script),
	}
}

type javaVuserApprovalEvidence = importreview.JavaVuserApprovalEvidence

func readJavaVuserAuthoringApproval(script *dsl.Script) (javaVuserApprovalEvidence, bool) {
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil {
		return javaVuserApprovalEvidence{}, false
	}
	evidence, ok := script.Metadata.Import.Preserved[javaVuserApprovalEvidenceKey].(map[string]any)
	if !ok {
		return javaVuserApprovalEvidence{}, false
	}
	result := javaVuserApprovalEvidence{
		SessionID:   strings.TrimSpace(fmt.Sprint(evidence["session_id"])),
		ContentHash: strings.ToLower(strings.TrimSpace(fmt.Sprint(evidence["content_hash"]))),
	}
	if result.SessionID == "" || !isCanonicalSHA256(result.ContentHash) {
		return javaVuserApprovalEvidence{}, false
	}
	return result, true
}

var clearJavaVuserAuthoringApproval = importreview.ClearJavaVuserAuthoringApproval

var writeJavaVuserAuthoringApproval = importreview.WriteJavaVuserAuthoringApproval

// javaVuserOverrideChangedExecutableSurface reports whether a scenario/runtime
// override moved the candidate script away from the approved baseline in a way
// that changes WHAT JAVA CODE RUNS: the lifecycle actions (which carry the
// java_vuser_run class_name/method/source_archive_ref — also the {{env}}
// template substitution vector) or the code-selecting Java Env config fields
// (classpath, classpath asset bindings, JDK home/selection, additional VM
// parameters), or a move into LoadRunner thread mode, where the script's
// libraries and native code share a JVM with other VUsers. Runtime-safe
// toggles (skip_parameter_evaluation,
// use_xbootclasspath, dedicated_class_loader) and non-Java runtime sections
// (pacing, logging, think time, …) are NOT code-selecting and are intentionally
// excluded, so those overrides remain re-blessable under the existing authoring
// approval.
func javaVuserOverrideChangedExecutableSurface(approved, candidate *dsl.Script) bool {
	if approved == nil || candidate == nil {
		return true
	}
	if !reflect.DeepEqual(approved.Init, candidate.Init) ||
		!reflect.DeepEqual(approved.Actions, candidate.Actions) ||
		!reflect.DeepEqual(approved.Teardown, candidate.Teardown) {
		return true
	}
	a := approved.Config.JavaVuser
	c := candidate.Config.JavaVuser
	return a.JDKSelection != c.JDKSelection ||
		a.JDKHome != c.JDKHome ||
		!reflect.DeepEqual(a.Classpath, c.Classpath) ||
		// A swapped JAR (different sha256) or a re-pointed classpath binding
		// changes the bytes loaded into the JVM, so it must invalidate an
		// existing authoring approval and force re-approval.
		!reflect.DeepEqual(a.ClasspathAssets, c.ClasspathAssets) ||
		!reflect.DeepEqual(a.AdditionalVMParameters, c.AdditionalVMParameters) ||
		c.JVMSharing == dsl.JavaVuserJVMSharingThreads && a.JVMSharing != dsl.JavaVuserJVMSharingThreads
}

// javaScriptClassesEntryUnbound reports a classpath that keeps the importer's
// script-classes entry with no jar bound to it.
func javaScriptClassesEntryUnbound(script *dsl.Script) bool {
	if script == nil || !slices.Contains(script.Config.JavaVuser.Classpath, enterprisearchive.JavaVuserScriptClassesEntry) {
		return false
	}
	return !slices.ContainsFunc(script.Config.JavaVuser.ClasspathAssets, func(asset dsl.JavaClasspathAsset) bool {
		return asset.Entry == enterprisearchive.JavaVuserScriptClassesEntry
	})
}

func scriptHasJavaClasspathAssets(script *dsl.Script) bool {
	return script != nil && len(script.Config.JavaVuser.ClasspathAssets) > 0
}

var scriptRequiresJavaVuserAuthoringApproval = importreview.ScriptRequiresJavaVuserAuthoringApproval

// restoreJavaVuserAuthoringApprovalForUpdate rejects caller-supplied
// provenance and restores only an approval already verified against the
// durable script and authoring-session store. Any content change makes the
// stored approval hash stale and must go through a new authoring review.
func (tr *TestRunner) restoreJavaVuserAuthoringApprovalForUpdate(candidate, current *dsl.Script) error {
	clearJavaVuserAuthoringApproval(candidate)
	candidateRequiresApproval := scriptRequiresJavaVuserAuthoringApproval(candidate)
	currentRequiresApproval := scriptRequiresJavaVuserAuthoringApproval(current)
	if currentRequiresApproval && !candidateRequiresApproval {
		return fmt.Errorf("Java Vuser scripts must be changed through local authoring-session review and approval")
	}
	if !candidateRequiresApproval {
		return nil
	}
	if !currentRequiresApproval {
		return fmt.Errorf("Java Vuser scripts must be saved through authoring-session review and approval")
	}
	if conflict := tr.javaVuserApprovalConflict(current); conflict != "" {
		return fmt.Errorf("%s", conflict)
	}
	evidence, ok := readJavaVuserAuthoringApproval(current)
	if !ok {
		return fmt.Errorf("Java Vuser authoring approval is missing or stale; review and approve the current script revision")
	}
	candidate.ID = current.ID
	candidate.ProjectID = current.ProjectID
	writeJavaVuserAuthoringApproval(candidate, evidence)
	if conflict := tr.javaVuserApprovalConflict(candidate); conflict != "" {
		clearJavaVuserAuthoringApproval(candidate)
		return fmt.Errorf("%s", conflict)
	}
	return nil
}

func (tr *TestRunner) javaVuserApprovalConflict(script *dsl.Script) string {
	if script == nil {
		return "Java Vuser scripts must be approved through an authoring session before launch"
	}
	evidence, ok := readJavaVuserAuthoringApproval(script)
	if !ok {
		return "Java Vuser scripts must be approved through an authoring session before launch"
	}
	if evidence.ContentHash != hashLibraryScript(script) {
		return "Java Vuser authoring approval is missing or stale; review and approve the current script revision"
	}
	reader, ok := tr.scriptStore.(authoringSessionReader)
	if !ok {
		return "Java Vuser authoring approval cannot be verified by the configured store"
	}
	session, err := reader.GetAuthoringSession(evidence.SessionID)
	if err != nil || session == nil || session.Status != "approved" || normalizeProjectID(session.ProjectID) != normalizeProjectID(script.ProjectID) {
		return "Java Vuser authoring approval is missing or stale; review and approve the current script revision"
	}
	if strings.TrimSpace(fmt.Sprint(session.ReviewState["approved_script_id"])) != strings.TrimSpace(script.ID) {
		return "Java Vuser authoring approval does not match this stored script"
	}
	return ""
}

func (tr *TestRunner) javaVuserRunnerAvailableLocked(requiresPersistentLifecycle bool) bool {
	if tr != nil {
		if backend, ok := tr.execution.(*distributedExecutionBackend); ok {
			return tr.distributedJavaVuserRunnerAvailableLocked(backend, requiresPersistentLifecycle)
		}
	}
	if !requiresPersistentLifecycle {
		// Legacy full_lifecycle scripts retain their v1 one-shot enablement
		// contract. Combined phase scripts take the capability-checked path below.
		return config.JavaVuserRunnerEnabled()
	}
	instance, err := protocol.Get("java_vuser")
	if err != nil {
		return false
	}
	// RuntimeCapabilities performs the same external JVM/sidecar inspection as
	// RuntimeHealthy. Both must live inside the TTL cache because this function
	// runs under the global TestRunner lock; probing capabilities before entering
	// the cache would still block every launch preflight.
	return javaVuserRuntimeHealth.healthyNow(func() bool {
		capabilities, supported, capabilityErr := protocol.ReadRuntimeCapabilities(instance)
		if capabilityErr != nil || !supported || !capabilities.Available ||
			!protocol.RuntimeFeaturesSatisfy(capabilities.Features, protocol.FeatureJavaVuserPersistentLifecycleV1) {
			return false
		}
		healthy, healthSupported, healthErr := protocol.CheckRuntimeHealth(context.Background(), instance)
		return healthErr == nil && (!healthSupported || healthy)
	})
}

func (tr *TestRunner) distributedJavaVuserRunnerAvailableLocked(backend *distributedExecutionBackend, requiresPersistentLifecycle bool) bool {
	if backend == nil {
		return false
	}
	if requiresPersistentLifecycle {
		for _, entry := range tr.cachedGeneratorInventory() {
			if !generatorInventoryEntryReachable(entry) || entry.DrainState != "active" {
				continue
			}
			if generatorInventoryHasJavaVuserPersistentLifecycle(entry) {
				return true
			}
		}
		return false
	}
	inventory := tr.cachedGeneratorInventory()
	if len(inventory) > 0 {
		// A fresh heartbeat is authoritative. Static target labels are only an
		// optimistic bootstrap hint before the first inventory observation and
		// must never override a generator that reports the runtime unavailable.
		for _, entry := range inventory {
			if !generatorInventoryEntryReachable(entry) || entry.DrainState != "active" {
				continue
			}
			if generatorInventoryHasJavaVuserRunner(entry) {
				return true
			}
		}
		return false
	}
	specs := tr.enrichGeneratorTargetSpecsWithLabels(backend.configuredTargetSpecs())
	for _, spec := range specs {
		if backend.generatorDrainState(spec.Address) != "active" {
			continue
		}
		if generatorSpecHasJavaVuserRunner(spec) {
			return true
		}
	}
	return false
}

func (tr *TestRunner) distributedJavaVuserGroupRunnerAvailableLocked(backend *distributedExecutionBackend, test *ManagedTest, group TestGroup) bool {
	if backend == nil || test == nil {
		return false
	}
	rootAssignment, err := normalizeGeneratorAssignmentSettings(test.Assignment)
	if err != nil {
		return false
	}
	defaultTargets, err := backend.allowedTargetsForTest(test)
	if err != nil {
		return false
	}
	specs := tr.enrichGeneratorTargetSpecsWithLabels(backend.configuredTargetSpecs())
	if len(specs) == 0 {
		return false
	}
	configured := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		configured[spec.Address] = struct{}{}
	}

	// Persistent lifecycle execution needs an authoritative, fresh heartbeat. Static
	// target labels can hint that a legacy one-shot runner may exist, but they
	// cannot prove reachability or the persistent lifecycle contract.
	liveTargets := make(map[string]generatorLoadSnapshot)
	for _, entry := range tr.cachedGeneratorInventory() {
		if _, ok := configured[entry.Target]; !ok {
			continue
		}
		if backend.generatorDrainState(entry.Target) != "active" ||
			entry.DrainState != "active" ||
			!generatorInventoryEntryReachable(entry) {
			continue
		}
		liveTargets[entry.Target] = generatorLoadSnapshot{
			Target:             entry.Target,
			HeartbeatOK:        true,
			SupportedProtocols: append([]string(nil), entry.SupportedProtocols...),
			CompatibleFeatures: append([]string(nil), entry.CompatibleFeatures...),
			AvailableCapacity:  entry.AvailableCapacity,
			RuntimeCapacities:  cloneProtocolRuntimeCapacities(entry.RuntimeCapacities),
			JavaHost:           cloneJavaHostCapabilitySummary(entry.JavaHost),
		}
	}
	if len(liveTargets) == 0 {
		return false
	}
	defaultLoad := loadSnapshotsForTargets(liveTargets, defaultTargets)
	preferredAll := orderedUniqueTargets(mapKeys(liveTargets))
	compatibility := newGeneratorCompatibilityIndex(specs, liveTargets)
	shards := chooseGeneratorShardsForGroup(
		group,
		liveTargets,
		defaultLoad,
		preferredAll,
		defaultTargets,
		rootAssignment,
		nil,
		compatibility,
	)
	return len(shards) > 0
}

func (tr *TestRunner) cachedGeneratorInventory() []GeneratorInventoryEntry {
	entries, _ := tr.cachedGeneratorInventorySnapshot()
	return entries
}

func (tr *TestRunner) cachedGeneratorInventorySnapshot() ([]GeneratorInventoryEntry, time.Time) {
	if tr == nil {
		return nil, time.Time{}
	}
	tr.liveInventoryMu.RLock()
	entries := cloneGeneratorInventoryEntries(tr.liveInventory)
	capturedAt := tr.liveInventoryAt
	tr.liveInventoryMu.RUnlock()
	if len(entries) == 0 || capturedAt.IsZero() {
		return nil, time.Time{}
	}
	age := time.Since(capturedAt)
	if age < 0 || age > defaultGeneratorSnapshotTTL {
		return nil, time.Time{}
	}
	return entries, capturedAt
}

func generatorSpecHasJavaVuserRunner(spec generatorTargetSpec) bool {
	if hasJavaVuserRunnerLabels(spec.Labels) {
		return true
	}
	for _, protocolName := range spec.Protocols {
		if strings.EqualFold(strings.TrimSpace(protocolName), "java_vuser") {
			return true
		}
	}
	return false
}

func generatorInventoryHasJavaVuserRunner(entry GeneratorInventoryEntry) bool {
	// Once a heartbeat-backed inventory entry exists, its advertised protocols
	// and features are authoritative. Pool/target labels are only provisional
	// hints before that first observation and must not resurrect an unavailable
	// runtime.
	return stringSliceContainsEqualFold(entry.SupportedProtocols, "java_vuser") ||
		stringSliceContainsEqualFold(entry.CompatibleFeatures, "java_vuser")
}

func generatorInventoryHasJavaVuserPersistentLifecycle(entry GeneratorInventoryEntry) bool {
	if !stringSliceContainsEqualFold(entry.SupportedProtocols, "java_vuser") ||
		!protocol.RuntimeFeaturesSatisfy(entry.CompatibleFeatures, protocol.FeatureJavaVuserPersistentLifecycleV1) {
		return false
	}
	snapshot := generatorLoadSnapshot{
		CompatibleFeatures: append([]string(nil), entry.CompatibleFeatures...),
		RuntimeCapacities:  cloneProtocolRuntimeCapacities(entry.RuntimeCapacities),
		JavaHost:           cloneJavaHostCapabilitySummary(entry.JavaHost),
	}
	available, capacityAdvertised := protocolRuntimeAvailableVUsers(snapshot, "java_vuser")
	occupied, occupancyAdvertised := protocolRuntimeOccupancyMode(snapshot, "java_vuser")
	return capacityAdvertised &&
		available > 0 &&
		occupancyAdvertised &&
		(occupied == generatorrpc.RuntimeOccupancyModeIdle ||
			occupied == generatorrpc.RuntimeOccupancyModePersistent)
}

func hasJavaVuserRunnerLabels(labels map[string]string) bool {
	if len(labels) == 0 {
		return false
	}
	runtime := strings.ToLower(strings.TrimSpace(labels["runtime"]))
	image := strings.ToLower(strings.TrimSpace(labels["image"]))
	return runtime == "java_vuser" || image == "generator-java"
}

func generatorInventoryEntryReachable(entry GeneratorInventoryEntry) bool {
	switch strings.ToLower(strings.TrimSpace(entry.Status)) {
	case "connected", "healthy", "ready":
		return true
	default:
		return false
	}
}

func stringSliceContainsEqualFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

func groupUsesJavaVuserProtocol(group TestGroup) bool {
	protocolName := strings.ToLower(strings.TrimSpace(group.Protocol))
	if protocolName == "" && group.script != nil {
		protocolName = strings.ToLower(strings.TrimSpace(group.script.Protocol))
	}
	return protocolName == "java_vuser"
}

func groupsUseJavaVuserProtocol(groups []TestGroup) bool {
	for _, group := range groups {
		if groupUsesJavaVuserProtocol(group) {
			return true
		}
	}
	return false
}

func groupRequiresJavaVuserRunner(group TestGroup) bool {
	protocolName := strings.ToLower(strings.TrimSpace(group.Protocol))
	if protocolName == "" && group.script != nil {
		protocolName = strings.ToLower(strings.TrimSpace(group.script.Protocol))
	}
	if protocolName != "java_vuser" {
		return false
	}
	if group.script == nil {
		return true
	}
	return javaVuserActionsRequireRunner(group.script.Init) ||
		javaVuserActionsRequireRunner(group.script.Actions) ||
		javaVuserActionsRequireRunner(group.script.Teardown)
}

func groupRequiresJavaVuserPhaseLifecycle(group TestGroup) bool {
	protocolName := strings.ToLower(strings.TrimSpace(group.Protocol))
	if protocolName == "" && group.script != nil {
		protocolName = strings.ToLower(strings.TrimSpace(group.script.Protocol))
	}
	if protocolName != "java_vuser" || group.script == nil {
		return false
	}
	return javaVuserActionsRequirePhaseLifecycle(group.script.Init) ||
		javaVuserActionsRequirePhaseLifecycle(group.script.Actions) ||
		javaVuserActionsRequirePhaseLifecycle(group.script.Teardown)
}

var javaVuserActionsRequirePhaseLifecycle = importreview.JavaVuserActionsRequirePhaseLifecycle

var javaVuserActionsRequireRunner = importreview.JavaVuserActionsRequireRunner

func javaVuserRunnerLaunchConflict(groups []TestGroup) *LaunchConflict {
	conflicts := (&TestRunner{}).javaVuserRunnerLaunchConflictsLocked(groups)
	if len(conflicts) == 0 {
		return nil
	}
	return &conflicts[0]
}

func externalRunnerRequiredMessage(protocolName string) string {
	switch strings.ToLower(strings.TrimSpace(protocolName)) {
	case "sap_gui", "sapgui":
		return "SAP GUI scripts require the windows_sap_gui_runner bridge with SAP GUI Scripting enabled before launch."
	default:
		return fmt.Sprintf("%s scripts require an external runner bridge before launch.", protocolName)
	}
}

func javaVuserRunnerRequiredMessage(requiresPersistentLifecycle bool) string {
	message := "Java Vuser sidecar actions (java_vuser_run) require a governed generator-java JVM runner pool. An operator must enable it on the runner fleet via APEXLOAD_JAVA_VUSER_RUNNER_ENABLED=true (with the runner command, classpath, and working directory configured through APEXLOAD_JAVA_VUSER_*)."
	if requiresPersistentLifecycle {
		message += " The generator must advertise " + protocol.FeatureJavaVuserPersistentLifecycleV1 +
			" and " + protocol.FeatureJavaVuserSessionFrameTokenV1 + "."
	}
	return message + " Import review and authoring approval do not require this runner, but test launch does; script config cannot enable it."
}
