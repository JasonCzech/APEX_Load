package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// LiveTableBinding is trusted assignment configuration, never authored DSL.
// The client enforces the admitted table/run/project capability independently.
type LiveTableBinding struct {
	Alias         string            `json:"alias"`
	TableID       string            `json:"table_id"`
	Strategy      string            `json:"strategy"`
	EmptyPolicy   string            `json:"empty_policy,omitempty"`
	Profile       livetable.Profile `json:"profile"`
	Columns       []string          `json:"columns"`
	Wait          time.Duration     `json:"wait"`
	LeaseDuration time.Duration     `json:"lease_duration"`
	OpTimeout     time.Duration     `json:"op_timeout"`
}

func cloneLiveTableBindings(bindings []LiveTableBinding) []LiveTableBinding {
	cloned := append([]LiveTableBinding(nil), bindings...)
	for index := range cloned {
		cloned[index].Columns = append([]string(nil), cloned[index].Columns...)
	}
	return cloned
}

type liveTableLane struct {
	mu       sync.Mutex
	id       string
	sequence uint64
	failed   bool
}

type liveTableBranchSlot struct {
	parent *liveTableLane
	index  int
}

type liveTableAcquisition struct {
	guard   *sync.Mutex // serializes renewal with acknowledge/release for this acquisition
	binding LiveTableBinding
	lease   livetable.Lease
	laneID  string
	auto    bool
}

type liveTableRuntime struct {
	ownerID            string
	client             livetable.Client
	bindings           map[string]LiveTableBinding
	mu                 sync.Mutex
	leases             map[string]liveTableAcquisition
	nativeLeasePending int
	failed             bool
	branches           map[liveTableBranchSlot]*liveTableLane
	renew              *liveTableLane
	settle             *liveTableLane
	lost               chan struct{}
	cancel             context.CancelFunc
	done               chan struct{}
}

// A parent runs one parallel block at a time and joins all branches before
// continuing. Reuse its finite branch slots across blocks and iterations;
// allocating a client per invocation would exhaust retained retry identities.
// Nested blocks have distinct parent lanes and therefore distinct slots.
func (runtime *liveTableRuntime) branchLane(parent *liveTableLane, index int) *liveTableLane {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.branches == nil {
		runtime.branches = make(map[liveTableBranchSlot]*liveTableLane)
	}
	key := liveTableBranchSlot{parent: parent, index: index}
	if lane := runtime.branches[key]; lane != nil {
		return lane
	}
	lane := &liveTableLane{id: fmt.Sprintf("%s:branch:%d", runtime.ownerID, len(runtime.branches)+1)}
	runtime.branches[key] = lane
	return lane
}

func validateLiveTableBindings(script *dsl.Script, client livetable.Client, clientID string, bindings []LiveTableBinding) error {
	aliases, err := dsl.ScriptLiveTableAliases(script)
	if err != nil {
		return err
	}
	if len(bindings) == 0 && len(aliases) == 0 {
		return nil
	}
	if client == nil || clientID == "" || len(clientID) > 128 {
		return fmt.Errorf("live tables require an admitted client and client incarnation")
	}
	if len(bindings) > 32 {
		return fmt.Errorf("at most 32 live table bindings are supported per group")
	}
	seen := map[string]bool{}
	exactAliases := map[string]bool{}
	for _, binding := range bindings {
		// Reuse the static alias grammar without accepting authored identities.
		if _, err := dsl.ParseLiveTableAction(map[string]any{"alias": binding.Alias, "operation": "next"}); err != nil {
			return err
		}
		key := strings.ToLower(binding.Alias)
		if seen[key] || binding.TableID == "" {
			return fmt.Errorf("live table bindings require distinct aliases and table IDs")
		}
		seen[key] = true
		exactAliases[binding.Alias] = true
		if binding.Profile != livetable.ProfileNative && binding.Profile != livetable.ProfileVTS {
			return fmt.Errorf("live table profile must be explicit")
		}
		if binding.Strategy != "none" && binding.Strategy != "manual" && binding.Strategy != "next" && binding.Strategy != "sequential" && binding.Strategy != "pop" && binding.Strategy != "lease" {
			return fmt.Errorf("live table iteration strategy is unsupported")
		}
		if binding.Strategy == "lease" && binding.Profile != livetable.ProfileNative {
			return fmt.Errorf("automatic leases require the native table profile")
		}
		if len(binding.Columns) == 0 || len(binding.Columns) > 128 {
			return fmt.Errorf("live table bindings require 1..128 declared columns")
		}
		if binding.EmptyPolicy != "" && binding.EmptyPolicy != "error" && binding.EmptyPolicy != "skip" && binding.EmptyPolicy != "wait" {
			return fmt.Errorf("live table empty policy is unsupported")
		}
		if binding.EmptyPolicy == "wait" && (binding.Wait <= 0 || (binding.OpTimeout > 0 && binding.OpTimeout < binding.Wait)) {
			return fmt.Errorf("live table wait policy requires a bounded wait within its operation timeout")
		}
		if binding.Wait < 0 || binding.Wait > time.Minute || binding.LeaseDuration < 0 || binding.LeaseDuration > 5*time.Minute || binding.OpTimeout < 0 || binding.OpTimeout > 2*time.Minute {
			return fmt.Errorf("live table timing bounds exceeded")
		}
		if script != nil {
			for _, source := range script.DataSources {
				if strings.EqualFold(source.Name, binding.Alias) {
					return fmt.Errorf("live table alias collides with a static data source")
				}
			}
		}
	}
	for _, alias := range aliases {
		if !exactAliases[alias] {
			return fmt.Errorf("live_table action requires an admitted binding for alias %q", alias)
		}
	}
	return nil
}

func (vu *VUser) prepareLiveTables() error {
	if vu.liveTables != nil {
		return nil
	}
	if err := validateLiveTableBindings(vu.Script, vu.LiveTableClient, vu.LiveTableClientID, vu.LiveTableBindings); err != nil {
		return err
	}
	if len(vu.LiveTableBindings) == 0 {
		return nil
	}
	owner := vu.LiveTableClientID + ":" + uuid.NewString()
	runtime := &liveTableRuntime{ownerID: owner, client: vu.LiveTableClient, bindings: make(map[string]LiveTableBinding), leases: make(map[string]liveTableAcquisition), renew: &liveTableLane{id: owner + ":renew"}, settle: &liveTableLane{id: owner + ":settle"}, lost: make(chan struct{})}
	for _, binding := range vu.LiveTableBindings {
		binding.Columns = append([]string(nil), binding.Columns...)
		if binding.Strategy == "manual" {
			binding.Strategy = "none"
		}
		if binding.Strategy == "next" {
			binding.Strategy = "sequential"
		}
		if binding.LeaseDuration == 0 {
			binding.LeaseDuration = 30 * time.Second
		}
		if binding.OpTimeout == 0 {
			binding.OpTimeout = 65 * time.Second
		}
		runtime.bindings[binding.Alias] = binding
	}
	vu.liveTables, vu.liveTableLane = runtime, &liveTableLane{id: owner + ":main"}
	return nil
}

// failLocked wakes active native invocations immediately; callers hold mu.
func (runtime *liveTableRuntime) failLocked() {
	if !runtime.failed {
		runtime.failed = true
		if runtime.lost != nil {
			close(runtime.lost)
		}
	}
}

func safeLiveTableError(err error) error {
	var typed *livetable.Error
	if errors.As(err, &typed) {
		return &livetable.Error{Code: typed.Code, Message: "live table operation failed: " + string(typed.Code), OutcomeUnknown: typed.OutcomeUnknown}
	}
	// This helper is called only after dispatch. A raw transport cancellation
	// cannot establish nonacceptance; pre-dispatch cancellation returns directly.
	return &livetable.Error{Code: livetable.CodeUnavailable, Message: "live table operation outcome is unknown", OutcomeUnknown: true}
}

func uncertainLiveTableError(err error) bool {
	var typed *livetable.Error
	if errors.As(err, &typed) {
		return typed.OutcomeUnknown
	}
	return err != nil // transport/context failures cannot prove nonacceptance
}

func (runtime *liveTableRuntime) execute(ctx context.Context, lane *liveTableLane, binding LiveTableBinding, request livetable.Request) (livetable.Result, error) {
	lane.mu.Lock()
	defer lane.mu.Unlock()
	if lane.failed {
		return livetable.Result{}, fmt.Errorf("live table client has an unresolved operation; further operations are blocked")
	}
	if err := ctx.Err(); err != nil {
		return livetable.Result{}, err
	}
	lane.sequence++
	if lane.sequence == 0 {
		lane.failed = true
		return livetable.Result{}, fmt.Errorf("live table operation sequence exhausted")
	}
	request.ClientID, request.OwnerID, request.Sequence = lane.id, runtime.ownerID, lane.sequence
	operationCtx, cancel := context.WithTimeout(ctx, binding.OpTimeout)
	defer cancel()
	result, err := runtime.client.Execute(operationCtx, binding.TableID, request)
	// A lost response retries the identical operation, never a new consume.
	if uncertainLiveTableError(err) && operationCtx.Err() == nil {
		result, err = runtime.client.Execute(operationCtx, binding.TableID, request)
		if err != nil {
			// Rejection of a retry does not establish whether the original attempt
			// committed. Only an authoritative successful replay clears ambiguity.
			err = &livetable.Error{Code: livetable.CodeUnavailable, Message: "live table retry did not establish the original outcome", OutcomeUnknown: true}
		}
	}
	if err != nil {
		if uncertainLiveTableError(err) {
			lane.failed = true
			runtime.mu.Lock()
			runtime.failLocked()
			runtime.mu.Unlock()
		}
		return livetable.Result{}, safeLiveTableError(err)
	}
	return result, nil
}

func (vu *VUser) startLiveTableRenewal(ctx context.Context) {
	runtime := vu.liveTables
	if runtime == nil || runtime.cancel != nil {
		return
	}
	ctx, runtime.cancel = context.WithCancel(ctx)
	runtime.done = make(chan struct{})
	go func() {
		defer close(runtime.done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			runtime.mu.Lock()
			due := make(map[string]liveTableAcquisition)
			for key, acquisition := range runtime.leases {
				if time.Until(acquisition.lease.ExpiresAt) <= acquisition.binding.LeaseDuration/2 {
					due[key] = acquisition
				}
			}
			runtime.mu.Unlock()
			for key, acquisition := range due {
				if acquisition.guard != nil && !acquisition.guard.TryLock() {
					// Settlement or explicit renewal owns this acquisition. Other
					// leases must continue renewing while its response is in flight.
					continue
				}
				runtime.mu.Lock()
				current, exists := runtime.leases[key]
				runtime.mu.Unlock()
				if !exists || current.lease.Token != acquisition.lease.Token {
					if acquisition.guard != nil {
						acquisition.guard.Unlock()
					}
					continue
				}
				result, err := runtime.execute(ctx, runtime.renew, acquisition.binding, livetable.Request{Operation: livetable.OpRenew, RecordID: acquisition.lease.RecordID, LeaseToken: acquisition.lease.Token, LeaseDuration: acquisition.binding.LeaseDuration})
				runtime.mu.Lock()
				current, exists = runtime.leases[key]
				if exists && current.lease.Token == acquisition.lease.Token {
					if err != nil || result.Lease == nil {
						runtime.failLocked()
					} else {
						current.lease = *result.Lease
						runtime.leases[key] = current
					}
				}
				runtime.mu.Unlock()
				if acquisition.guard != nil {
					acquisition.guard.Unlock()
				}
				if err != nil {
					return
				}
			}
		}
	}()
}

func (vu *VUser) liveTableLeaseError() error {
	if vu.liveTables == nil {
		return nil
	}
	vu.liveTables.mu.Lock()
	defer vu.liveTables.mu.Unlock()
	for _, acquisition := range vu.liveTables.leases {
		if !acquisition.lease.ExpiresAt.After(time.Now()) {
			vu.liveTables.failLocked()
		}
	}
	if vu.liveTables.failed {
		return fmt.Errorf("live table continuity was lost; further target actions and acknowledgment are blocked")
	}
	return nil
}

func (vu *VUser) clearLiveOutput(prefix string) {
	if prefix == "" {
		return
	}
	for key := range vu.Variables {
		if key == prefix || strings.HasPrefix(key, prefix+".") {
			delete(vu.Variables, key)
			if size, ok := vu.extractedVariables[key]; ok {
				vu.extractedVariableBytes -= size
				delete(vu.extractedVariables, key)
			}
		}
	}
}

func (vu *VUser) bindLiveResult(prefix string, result livetable.Result) error {
	if prefix == "" {
		return nil
	}
	values := map[string]string{prefix + ".status": string(result.Status), prefix + ".revision": strconv.FormatUint(result.Revision, 10)}
	if result.Row != nil {
		values[prefix+".record_id"] = result.Row.ID
		values[prefix+".record_version"] = strconv.FormatUint(result.Row.Version, 10)
		for column, value := range result.Row.Values {
			values[prefix+".values."+column] = value
		}
	}
	if len(result.Cells) > 0 {
		encoded, err := json.Marshal(result.Cells)
		if err != nil {
			return fmt.Errorf("live table result could not be encoded")
		}
		values[prefix+".cells"] = string(encoded)
	}
	if len(result.Rows) > 0 {
		encoded, err := json.Marshal(result.Rows)
		if err != nil {
			return fmt.Errorf("live table result could not be encoded")
		}
		values[prefix+".rows"] = string(encoded)
	}
	if len(result.Columns) > 0 {
		encoded, err := json.Marshal(result.Columns)
		if err != nil {
			return fmt.Errorf("live table result could not be encoded")
		}
		values[prefix+".columns"] = string(encoded)
	}
	if result.Value != "" {
		values[prefix+".value"] = result.Value
	}
	values[prefix+".count"] = strconv.FormatInt(result.Count, 10)
	// Lease tokens are process-owned capabilities and never enter variables,
	// metrics, replay reports, logging, or extraction evidence.
	return vu.mergeExtractions(&protocol.Result{Extractions: values})
}

func (vu *VUser) liveRequestValuesAllowed(request livetable.Request) bool {
	config := vu.resultPublicationRedactionConfig()
	if config == nil {
		return true
	}
	if config.runtimePublicationUnavailable {
		return false
	}
	all := allRuntimeSecretValues(config)
	scriptSourced := allRuntimeScriptSourcedValues(config)
	// Column names and named cursor identities are persisted just like cell
	// values. Native callers can compute every semantic string at runtime.
	values := make([]string, 0, 2+len(request.Columns)+2*len(request.Values)+2*len(request.Expected))
	values = append(values, request.RecordID, request.Cursor)
	values = append(values, request.Columns...)
	for key, value := range request.Values {
		values = append(values, key, value)
	}
	for key, value := range request.Expected {
		values = append(values, key, value)
	}
	for _, value := range values {
		for secret := range all {
			if _, imported := scriptSourced[secret]; !imported && strings.Contains(value, secret) {
				return false
			}
		}
	}
	return true
}

func (vu *VUser) executeLiveTableAction(ctx context.Context, action dsl.Action) (outErr error) {
	parsed, err := dsl.ParseLiveTableAction(action.Params)
	if err != nil {
		return err
	}
	defer func() {
		if outErr != nil {
			vu.clearLiveOutput(parsed.ResultPrefix)
		}
	}()
	if err := vu.prepareLiveTables(); err != nil {
		return err
	}
	if vu.liveTables == nil {
		return fmt.Errorf("live_table requires an admitted live binding")
	}
	binding, found := vu.liveTables.bindings[parsed.Alias]
	if !found {
		return fmt.Errorf("live_table alias is not bound")
	}
	if err := vu.liveTableLeaseError(); err != nil {
		return err
	}
	request := livetable.Request{Operation: livetable.Operation(parsed.Operation), RecordID: vu.interpolate(parsed.RecordID), Position: parsed.Position, Columns: parsed.Columns, Delta: int64(parsed.Delta), Wait: time.Duration(parsed.WaitMS) * time.Millisecond, Limit: parsed.Limit, Cursor: vu.interpolate(parsed.Cursor)}
	if parsed.Operation == "lease" || parsed.Operation == "renew" {
		request.LeaseDuration = binding.LeaseDuration
	}
	if parsed.LeaseMS > 0 {
		request.LeaseDuration = time.Duration(parsed.LeaseMS) * time.Millisecond
	}
	if parsed.ExpectedVersion != "" {
		version, err := strconv.ParseUint(vu.interpolate(parsed.ExpectedVersion), 10, 64)
		if err != nil || version == 0 {
			return fmt.Errorf("live_table expected_version did not resolve to a positive decimal")
		}
		request.ExpectedVersion = &version
	}
	request.Values = make(map[string]string, len(parsed.Values))
	request.Expected = make(map[string]string, len(parsed.Expected))
	for key, value := range parsed.Values {
		request.Values[key] = vu.interpolate(value)
	}
	for key, value := range parsed.Expected {
		request.Expected[key] = vu.interpolate(value)
	}
	vu.clearLiveOutput(parsed.ResultPrefix)
	result, err := vu.executeLiveTableLiteral(ctx, binding, request, parsed.LeaseRef, parsed.ResultPrefix)
	if err != nil {
		return err
	}
	if err := vu.bindLiveResult(parsed.ResultPrefix, result); err != nil {
		return err
	}
	if result.Status == livetable.StatusEmpty && parsed.OnEmpty != "skip" {
		return fmt.Errorf("live_table returned no available result")
	}
	if result.Status == livetable.StatusConditionFailed && parsed.OnCondition != "skip" {
		return fmt.Errorf("live_table condition was not met")
	}
	return nil
}

// executeLiveTableLiteral is the single semantic execution path. Inputs have
// already been resolved by the DSL caller; native callers pass literal strings.
// It does not read or write VUser variables or publish result values.
func (vu *VUser) executeLiveTableLiteral(ctx context.Context, binding LiveTableBinding, request livetable.Request, leaseRef, acquireRef string) (livetable.Result, error) {
	if err := vu.liveTableLeaseError(); err != nil {
		return livetable.Result{}, err
	}
	if !vu.liveRequestValuesAllowed(request) {
		return livetable.Result{}, fmt.Errorf("live_table cannot store or compare managed secret values")
	}
	leaseKey := vu.liveTableLane.id + ":" + leaseRef
	var acquisitionGuard *sync.Mutex
	if leaseRef != "" {
		vu.liveTables.mu.Lock()
		acquisition, ok := vu.liveTables.leases[leaseKey]
		vu.liveTables.mu.Unlock()
		if !ok || acquisition.binding.TableID != binding.TableID {
			return livetable.Result{}, fmt.Errorf("live_table lease reference is missing or belongs to another table")
		}
		acquisitionGuard = acquisition.guard
		if acquisitionGuard != nil {
			acquisitionGuard.Lock()
			defer acquisitionGuard.Unlock()
		}
		vu.liveTables.mu.Lock()
		current, stillHeld := vu.liveTables.leases[leaseKey]
		vu.liveTables.mu.Unlock()
		if !stillHeld || current.lease.Token != acquisition.lease.Token {
			return livetable.Result{}, fmt.Errorf("live_table acquisition was already settled")
		}
		request.LeaseToken, request.RecordID = acquisition.lease.Token, acquisition.lease.RecordID
	}
	if string(request.Operation) == "lease" {
		leaseKey = vu.liveTableLane.id + ":" + acquireRef
		vu.liveTables.mu.Lock()
		_, exists := vu.liveTables.leases[leaseKey]
		vu.liveTables.mu.Unlock()
		if exists {
			return livetable.Result{}, fmt.Errorf("live_table lease reference is already held; acknowledge or release it first")
		}
	}
	result, err := vu.liveTables.execute(ctx, vu.liveTableLane, binding, request)
	if err != nil {
		return livetable.Result{}, err
	}
	if result.Lease != nil {
		if request.LeaseDuration > 0 {
			binding.LeaseDuration = request.LeaseDuration
		}
		vu.liveTables.mu.Lock()
		if acquisitionGuard == nil {
			acquisitionGuard = &sync.Mutex{}
		}
		vu.liveTables.leases[leaseKey] = liveTableAcquisition{guard: acquisitionGuard, binding: binding, lease: *result.Lease, laneID: vu.liveTableLane.id}
		vu.liveTables.mu.Unlock()
	}
	if string(request.Operation) == "ack" || string(request.Operation) == "release" {
		vu.liveTables.mu.Lock()
		delete(vu.liveTables.leases, leaseKey)
		vu.liveTables.mu.Unlock()
	}
	return result, nil
}

var errLiveTableSkipIteration = errors.New("live table iteration skipped: no row available")

func (vu *VUser) beginLiveTableIteration(ctx context.Context) (outErr error) {
	if vu.liveTables == nil {
		return nil
	}
	aliases := make([]string, 0, len(vu.liveTables.bindings))
	for alias := range vu.liveTables.bindings {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		vu.clearLiveOutput(alias)
	}
	defer func() {
		if outErr != nil {
			for _, alias := range aliases {
				vu.clearLiveOutput(alias)
			}
		}
	}()
	for _, alias := range aliases {
		binding := vu.liveTables.bindings[alias]
		vu.clearLiveOutput(alias)
		if binding.Strategy == "none" {
			continue
		}
		operation := livetable.Operation(binding.Strategy)
		if binding.Strategy == "sequential" {
			operation = livetable.OpNext
		}
		request := livetable.Request{Operation: operation, Wait: binding.Wait}
		if operation == livetable.OpLease {
			request.LeaseDuration = binding.LeaseDuration
		}
		result, err := vu.liveTables.execute(ctx, vu.liveTableLane, binding, request)
		if err != nil {
			return err
		}
		if result.Status == livetable.StatusEmpty && binding.EmptyPolicy == "skip" {
			return errLiveTableSkipIteration
		}
		if result.Status != livetable.StatusOK || result.Row == nil {
			return fmt.Errorf("live table iteration source has no available row")
		}
		if result.Lease != nil {
			vu.liveTables.mu.Lock()
			vu.liveTables.leases["automatic:"+alias] = liveTableAcquisition{guard: &sync.Mutex{}, binding: binding, lease: *result.Lease, laneID: vu.liveTableLane.id, auto: true}
			vu.liveTables.mu.Unlock()
		}
		values := make(map[string]string, len(result.Row.Values))
		for column, value := range result.Row.Values {
			values[alias+"."+column] = value
		}
		if err := vu.mergeExtractions(&protocol.Result{Extractions: values}); err != nil {
			return err
		}
	}
	return nil
}

func (vu *VUser) settleLiveTableLeases(ctx context.Context, success bool, laneOnly bool) error {
	runtime := vu.liveTables
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	pending := make(map[string]liveTableAcquisition)
	for key, acquisition := range runtime.leases {
		if !laneOnly || acquisition.laneID == vu.liveTableLane.id {
			pending[key] = acquisition
		}
	}
	failed := runtime.failed
	runtime.mu.Unlock()
	var first error
	for key, acquisition := range pending {
		if acquisition.guard != nil {
			acquisition.guard.Lock()
		}
		runtime.mu.Lock()
		current, exists := runtime.leases[key]
		failed = failed || runtime.failed
		runtime.mu.Unlock()
		if !exists || current.lease.Token != acquisition.lease.Token {
			if acquisition.guard != nil {
				acquisition.guard.Unlock()
			}
			continue
		}
		operation := livetable.OpRelease
		if acquisition.auto && success && !failed {
			operation = livetable.OpAck
		}
		result, err := runtime.execute(ctx, runtime.settle, acquisition.binding, livetable.Request{Operation: operation, RecordID: acquisition.lease.RecordID, LeaseToken: acquisition.lease.Token})
		if err == nil && result.Status != livetable.StatusOK {
			err = fmt.Errorf("live table lease settlement was rejected")
		}
		if err != nil {
			if first == nil {
				first = err
			}
			if acquisition.guard != nil {
				acquisition.guard.Unlock()
			}
			continue
		}
		runtime.mu.Lock()
		delete(runtime.leases, key)
		runtime.mu.Unlock()
		if acquisition.guard != nil {
			acquisition.guard.Unlock()
		}
	}
	if failed && first == nil {
		first = fmt.Errorf("live table lease renewal failed")
	}
	return first
}

func (vu *VUser) closeLiveTables() {
	if vu.liveTables == nil {
		return
	}
	if vu.liveTables.cancel != nil {
		vu.liveTables.cancel()
		<-vu.liveTables.done
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := vu.settleLiveTableLeases(ctx, false, false); err != nil {
		vu.recordDataRowError(err)
	}
}

// LiveTableSandboxSeed creates a fresh, isolated table for replay. It has no
// source-pool identity or finalization capability, so replay cannot write back.
type LiveTableSandboxSeed struct {
	Alias   string            `json:"alias"`
	Profile livetable.Profile `json:"profile,omitempty"`
	Columns []string          `json:"columns"`
	Rows    []livetable.Row   `json:"rows"`
}

func NewLiveTableSandbox(seeds []LiveTableSandboxSeed) (livetable.Client, []LiveTableBinding, string, error) {
	if len(seeds) > 4 {
		return nil, nil, "", fmt.Errorf("replay supports at most 4 isolated live tables")
	}
	encoded, err := json.Marshal(seeds)
	if err != nil || len(encoded) > 64<<10 {
		return nil, nil, "", fmt.Errorf("isolated live table seeds must fit within 64 KiB")
	}
	manager := livetable.NewManager()
	incarnation := "sandbox:" + uuid.NewString()
	bindings := make([]LiveTableBinding, 0, len(seeds))
	for _, seed := range seeds {
		for _, column := range seed.Columns {
			if strings.TrimSpace(column) == "" || len(column) > 128 || strings.ContainsAny(column, "\x00\r\n") {
				return nil, nil, "", fmt.Errorf("sandbox columns must be nonempty names of at most 128 bytes without control separators")
			}
		}
		profile := seed.Profile
		if profile == "" {
			profile = livetable.ProfileNative
		}
		binding := LiveTableBinding{Alias: seed.Alias, TableID: uuid.NewString(), Strategy: "none", Profile: profile, Columns: seed.Columns, LeaseDuration: 30 * time.Second, OpTimeout: 65 * time.Second}
		if _, err := manager.Create(livetable.Config{ID: binding.TableID, RunID: incarnation, Profile: profile, Columns: seed.Columns, Rows: seed.Rows, Limits: livetable.Limits{
			MaxBytes: 256 << 10, MaxRows: 256, MaxFields: 4096, MaxColumns: 32, MaxFieldBytes: 8 << 10,
			MaxRequestBytes: 64 << 10, MaxResultBytes: 64 << 10, MaxReceipts: 128, MaxReceiptBytes: 256 << 10,
			MaxClients: 128, MaxWaiters: 8, MaxScanCells: 4096, MaxSnapshotBytes: 1 << 20,
		}}); err != nil {
			return nil, nil, "", err
		}
		bindings = append(bindings, binding)
	}
	if err := validateLiveTableBindings(nil, manager, incarnation, bindings); err != nil {
		return nil, nil, "", err
	}
	return manager, bindings, incarnation, nil
}

// ValidateLiveTableSandboxSeeds rejects missing/colliding aliases and oversized
// inline seeds before a replay session is admitted. No runtime state is retained.
func ValidateLiveTableSandboxSeeds(script *dsl.Script, seeds []LiveTableSandboxSeed) error {
	client, bindings, identity, err := NewLiveTableSandbox(seeds)
	if err != nil {
		return err
	}
	return validateLiveTableBindings(script, client, identity, bindings)
}
