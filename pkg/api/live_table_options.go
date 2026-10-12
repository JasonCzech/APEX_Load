package api

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	maxLiveTablesPerRun     = 16
	maxLiveTableDataBytes   = 8 << 20
	maxLiveTableResultBytes = 256 << 10
)

var liveTableAliasPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)

func liveTableBindingHasNoPool(binding storage.ScenarioDataPoolBinding) bool {
	if binding.Scope == "project" {
		return binding.PoolID == "" && binding.AccessMode == "live" && binding.ProjectTableID != ""
	}
	if binding.PoolID != "" || binding.AccessMode != "live" || binding.LiveOptions == nil {
		return false
	}
	return (binding.LiveOptions.SeedPolicy == "empty" && len(binding.LiveOptions.Columns) > 0) || (binding.LiveOptions.SeedPolicy == "original_seed" && binding.LiveOptions.SeedRunID != "")
}

// Frozen reruns resolve their project-scoped immutable seed from the original
// run. A retained pool ID is an output witness, not a fresh seed dependency.
func liveTableBindingUsesFrozenSeed(binding storage.ScenarioDataPoolBinding) bool {
	return binding.AccessMode == "live" && binding.LiveOptions != nil && binding.LiveOptions.SeedPolicy == "original_seed" && binding.LiveOptions.SeedRunID != ""
}

func normalizeLiveTableBinding(binding storage.ScenarioDataPoolBinding) (storage.ScenarioDataPoolBinding, error) {
	if binding.Scope == "project" {
		return normalizeProjectLiveTableBinding(binding)
	}
	if !liveTableAliasPattern.MatchString(binding.Alias) {
		return binding, fmt.Errorf("live table alias must be a 1–64 character identifier")
	}
	options := storage.LiveTableOptions{}
	if binding.LiveOptions != nil {
		options = *binding.LiveOptions
		options.Columns = append([]string(nil), binding.LiveOptions.Columns...)
	}
	for _, field := range []*string{&options.Profile, &options.SeedPolicy, &options.SeedRunID, &options.Durability, &options.EndMode, &options.EmptyPolicy, &options.OutputPoolName} {
		*field = strings.TrimSpace(*field)
	}
	if options.Profile == "" {
		options.Profile = "native_queue"
	}
	if options.Profile != "native_queue" && options.Profile != "vts_columns" {
		return binding, fmt.Errorf("unsupported live table profile %q", options.Profile)
	}
	if options.SeedPolicy == "" {
		options.SeedPolicy = "current_at_start"
	}
	if options.SeedPolicy != "current_at_start" && options.SeedPolicy != "empty" && options.SeedPolicy != "original_seed" {
		return binding, fmt.Errorf("unsupported live table seed policy %q", options.SeedPolicy)
	}
	if options.SeedPolicy == "current_at_start" && binding.PoolID == "" {
		return binding, fmt.Errorf("live table pool seed requires pool_id")
	}
	if options.SeedPolicy == "original_seed" && (options.SeedRunID == "" || len(options.SeedRunID) > 128) {
		return binding, fmt.Errorf("original_seed requires seed_run_id of at most 128 bytes")
	}
	if options.SeedPolicy != "original_seed" && options.SeedRunID != "" {
		return binding, fmt.Errorf("seed_run_id requires original_seed")
	}
	if len(options.Columns) > 128 {
		return binding, fmt.Errorf("live table supports at most 128 declared columns")
	}
	columns := map[string]bool{}
	for i, column := range options.Columns {
		column = strings.TrimSpace(column)
		if column == "" || len(column) > 128 || columns[column] || strings.ContainsAny(column, "\x00\r\n") {
			return binding, fmt.Errorf("live table column names must be nonempty, unique and at most 128 bytes")
		}
		columns[column] = true
		options.Columns[i] = column
	}
	if options.SeedPolicy == "empty" && len(options.Columns) == 0 {
		return binding, fmt.Errorf("an empty live table requires declared columns")
	}
	if options.Durability == "" {
		options.Durability = "memory"
	}
	if options.Durability != "memory" && options.Durability != "durable" {
		return binding, fmt.Errorf("unsupported live table durability %q", options.Durability)
	}
	if options.EndMode == "" {
		options.EndMode = "discard"
	}
	if options.EndMode != "discard" && options.EndMode != "save_as_new" && options.EndMode != "write_back" {
		return binding, fmt.Errorf("unsupported live table end mode %q", options.EndMode)
	}
	if options.EndMode == "write_back" && (options.Profile != "native_queue" || binding.PoolID == "" || options.SeedPolicy == "empty") {
		return binding, fmt.Errorf("write_back requires a native queue seeded from a data pool")
	}
	if options.EndMode == "save_as_new" && (options.OutputPoolName == "" || len(options.OutputPoolName) > 256) {
		return binding, fmt.Errorf("save_as_new requires an output pool name of at most 256 bytes")
	}
	if options.EndMode != "save_as_new" && options.OutputPoolName != "" {
		return binding, fmt.Errorf("output_pool_name requires save_as_new")
	}
	if binding.SelectionStrategy == "" {
		binding.SelectionStrategy = "manual"
	}
	switch binding.SelectionStrategy {
	case "manual", "next", "pop", "lease":
	default:
		return binding, fmt.Errorf("unsupported live table selection strategy %q", binding.SelectionStrategy)
	}
	if options.Profile == "vts_columns" && binding.SelectionStrategy == "lease" {
		return binding, fmt.Errorf("vts_columns does not support row leases")
	}
	if options.EmptyPolicy == "" {
		options.EmptyPolicy = "error"
	}
	switch options.EmptyPolicy {
	case "error", "skip", "wait":
	default:
		return binding, fmt.Errorf("unsupported live table empty policy %q", options.EmptyPolicy)
	}
	if options.OperationTimeoutMS == 0 {
		options.OperationTimeoutMS = 10000
		if options.Durability == "durable" {
			options.OperationTimeoutMS = 120000
		}
	}
	if options.WaitMS < 0 || options.WaitMS > 60000 || options.OperationTimeoutMS < 1 || options.OperationTimeoutMS > 120000 || options.WaitMS > options.OperationTimeoutMS {
		return binding, fmt.Errorf("live table waits must be at most 60 seconds and within a 1–120000 ms operation timeout")
	}
	if options.Durability == "durable" && options.OperationTimeoutMS < 65000 {
		return binding, fmt.Errorf("durable live table operation timeout must be at least 65000 ms for adoption and recovery")
	}
	if options.EmptyPolicy == "wait" && options.WaitMS == 0 {
		return binding, fmt.Errorf("wait empty policy requires a positive wait_ms")
	}
	if options.EmptyPolicy != "wait" && options.WaitMS != 0 {
		return binding, fmt.Errorf("wait_ms requires wait empty policy")
	}
	if options.LeaseTTLMS == 0 {
		options.LeaseTTLMS = 30000
	}
	if options.MaxLeaseLifetimeMS == 0 {
		options.MaxLeaseLifetimeMS = 300000
	}
	if options.MaxDeliveryAttempts == 0 {
		options.MaxDeliveryAttempts = 5
	}
	if options.LeaseTTLMS < 1000 || options.LeaseTTLMS > 300000 || options.MaxLeaseLifetimeMS < options.LeaseTTLMS || options.MaxLeaseLifetimeMS > 1800000 || options.MaxDeliveryAttempts < 1 || options.MaxDeliveryAttempts > 100 {
		return binding, fmt.Errorf("live table lease limits are outside supported bounds")
	}
	if options.MaxRows == 0 {
		options.MaxRows = 10000
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = maxLiveTableDataBytes
	}
	if options.MaxResultBytes == 0 {
		options.MaxResultBytes = maxLiveTableResultBytes
	}
	if options.RetryWindowMS == 0 {
		options.RetryWindowMS = 600000
	}
	if options.MaxReceipts == 0 {
		options.MaxReceipts = 10000
	}
	if options.MaxRows < 1 || options.MaxRows > 100000 || options.MaxBytes < 1 || options.MaxBytes > maxLiveTableDataBytes || options.MaxResultBytes < 1 || options.MaxResultBytes > maxLiveTableResultBytes || options.RetryWindowMS < 60000 || options.RetryWindowMS > 600000 || options.MaxReceipts < 1 || options.MaxReceipts > 100000 {
		return binding, fmt.Errorf("live table capacity or retry limits are outside supported bounds")
	}
	minimumRetryWindow := options.OperationTimeoutMS
	if options.Durability == "durable" {
		minimumRetryWindow += 15000
	}
	if options.RetryWindowMS < minimumRetryWindow {
		return binding, fmt.Errorf("live table retry_window_ms must be at least %d to cover the operation deadline and recovery margin", minimumRetryWindow)
	}
	binding.LiveOptions = &options
	return binding, nil
}
