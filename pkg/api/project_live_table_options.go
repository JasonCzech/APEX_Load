package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

func normalizeProjectLiveTableBinding(b storage.ScenarioDataPoolBinding) (storage.ScenarioDataPoolBinding, error) {
	if b.AccessMode != "live" || b.PoolID != "" || b.ProjectTableID == "" || len(b.ProjectTableID) > 128 || !liveTableAliasPattern.MatchString(b.Alias) {
		return b, errors.New("project table bindings require live access, an alias and project_table_id, with no pool_id")
	}
	o := storage.LiveTableOptions{}
	if b.LiveOptions != nil {
		o = *b.LiveOptions
	}
	allowed := storage.LiveTableOptions{EmptyPolicy: o.EmptyPolicy, WaitMS: o.WaitMS, OperationTimeoutMS: o.OperationTimeoutMS}
	// Comparing canonical encodings also rejects nonempty slice fields.
	if o.Profile != "" || len(o.Columns) > 0 || o.SeedPolicy != "" || o.SeedRunID != "" || o.Durability != "" || o.EndMode != "" || o.LeaseTTLMS != 0 || o.MaxLeaseLifetimeMS != 0 || o.MaxDeliveryAttempts != 0 || o.MaxRows != 0 || o.MaxBytes != 0 || o.MaxResultBytes != 0 || o.RetryWindowMS != 0 || o.MaxReceipts != 0 || o.OutputPoolName != "" {
		return b, errors.New("project table bindings inherit profile, schema, durability and lifecycle; live_options only accept empty_policy, wait_ms and operation_timeout_ms")
	}
	if b.SelectionStrategy == "" {
		b.SelectionStrategy = "manual"
	}
	switch b.SelectionStrategy {
	case "manual", "next", "pop", "lease":
	default:
		return b, errors.New("unsupported project live table selection strategy")
	}
	allowed.EmptyPolicy = strings.TrimSpace(allowed.EmptyPolicy)
	if allowed.EmptyPolicy == "" {
		allowed.EmptyPolicy = "error"
	}
	switch allowed.EmptyPolicy {
	case "error", "skip", "wait":
	default:
		return b, errors.New("unsupported project live table empty policy")
	}
	if allowed.OperationTimeoutMS == 0 {
		allowed.OperationTimeoutMS = 120000
	}
	if allowed.OperationTimeoutMS < 65000 || allowed.OperationTimeoutMS > 120000 || allowed.WaitMS < 0 || allowed.WaitMS > 60000 || allowed.WaitMS > allowed.OperationTimeoutMS || (allowed.EmptyPolicy == "wait") != (allowed.WaitMS > 0) {
		return b, errors.New("project live table waits require wait policy and 1–60000 ms within a 65000–120000 ms operation timeout")
	}
	b.LiveOptions = &allowed
	return b, nil
}
func projectTableOptions(c storage.ProjectLiveTableConfig) *storage.LiveTableOptions {
	return &storage.LiveTableOptions{Profile: c.Profile, Columns: append([]string(nil), c.Columns...), SeedPolicy: "empty", Durability: "durable", EndMode: "discard", MaxRows: c.MaxRows, MaxBytes: c.MaxBytes, MaxResultBytes: c.MaxResultBytes, MaxReceipts: c.MaxReceipts, RetryWindowMS: c.RetryWindowMS, LeaseTTLMS: c.LeaseTTLMS, MaxLeaseLifetimeMS: c.MaxLeaseLifetimeMS, MaxDeliveryAttempts: c.MaxDeliveryAttempts}
}
func normalizeProjectTableConfig(c storage.ProjectLiveTableConfig) (storage.ProjectLiveTableConfig, error) {
	b, err := normalizeLiveTableBinding(storage.ScenarioDataPoolBinding{Alias: "configuration", AccessMode: "live", LiveOptions: projectTableOptions(c)})
	if err != nil {
		return c, err
	}
	o := b.LiveOptions
	return storage.ProjectLiveTableConfig{Profile: o.Profile, Columns: o.Columns, MaxRows: o.MaxRows, MaxBytes: o.MaxBytes, MaxResultBytes: o.MaxResultBytes, MaxReceipts: o.MaxReceipts, RetryWindowMS: o.RetryWindowMS, LeaseTTLMS: o.LeaseTTLMS, MaxLeaseLifetimeMS: o.MaxLeaseLifetimeMS, MaxDeliveryAttempts: o.MaxDeliveryAttempts}, nil
}
func validateProjectLiveTableReference(store any, project string, b storage.ScenarioDataPoolBinding) error {
	state, ok := store.(storage.ProjectLiveTableStore)
	if !ok {
		return errors.New("project live table storage unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), projectLiveTableServiceTimeout)
	defer cancel()
	d, err := state.GetProjectLiveTable(ctx, normalizeProjectID(project), b.ProjectTableID)
	if err != nil {
		return fmt.Errorf("project live table %q unavailable: %w", b.ProjectTableID, err)
	}
	if d.State != "active" {
		return errors.New("project live table is not accepting attachments")
	}
	if b.SelectionStrategy == "lease" && d.Config.Profile != "native_queue" {
		return errors.New("project column tables do not support leases")
	}
	return nil
}
