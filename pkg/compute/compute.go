// Package compute defines the provider-neutral capacity control contract for
// generator fleets. Implementations may reconcile Kubernetes replicas, Azure
// Container Apps/Jobs, or ECS services/tasks without leaking provider request
// shapes into the API layer.
package compute

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrUnsupportedOperation = errors.New("compute backend does not support this operation")
	ErrOwnershipConflict    = errors.New("capacity is owned by an external autoscaler")
	ErrArchiveNotDrained    = errors.New("generator archive has not reached durable terminal storage")
)

type BackendInfo struct {
	Name             string            `json:"name"`
	CanScale         bool              `json:"can_scale"`
	CanLaunch        bool              `json:"can_launch"`
	CanReachDirectly bool              `json:"can_reach_directly"`
	SupportsReverse  bool              `json:"supports_reverse"`
	SupportsSpot     bool              `json:"supports_spot"`
	ResourceShapes   []string          `json:"resource_shapes,omitempty"`
	Details          map[string]string `json:"details,omitempty"`
}

type ProbeResult struct {
	Probe    string `json:"probe"`
	OK       bool   `json:"ok"`
	Optional bool   `json:"optional,omitempty"`
	Message  string `json:"message,omitempty"`
}

type CapacityRequest struct {
	OperationID          string         `json:"operation_id"`
	IdempotencyKey       string         `json:"idempotency_key"`
	ExecutionTargetID    string         `json:"execution_target_id"`
	FleetPoolID          string         `json:"fleet_pool_id"`
	TestID               string         `json:"test_id,omitempty"`
	RuntimeProfileID     string         `json:"runtime_profile_id,omitempty"`
	ConnectivityMode     string         `json:"connectivity_mode,omitempty"`
	Desired              int            `json:"desired"`
	WarmFloor            int            `json:"warm_floor,omitempty"`
	Deadline             time.Time      `json:"deadline"`
	Admission            string         `json:"admission"` // strict | best_effort
	ProviderOperationIDs []string       `json:"provider_operation_ids,omitempty"`
	LaunchIntents        []LaunchIntent `json:"launch_intents,omitempty"`
	// RestoreStanding asks Cancel to roll back a standing-service mutation made
	// by this operation. RollbackStanding is meaningful even when zero, so the
	// explicit flag avoids using a sentinel that would make scale-to-zero
	// rollback impossible.
	RestoreStanding  bool `json:"restore_standing,omitempty"`
	RollbackStanding int  `json:"rollback_standing,omitempty"`
}

type LaunchIntent struct {
	ClientToken string `json:"client_token"`
	GeneratorID string `json:"generator_id"`
	LaunchNonce string `json:"launch_nonce"`
}

type Plan struct {
	Requested int      `json:"requested"`
	Standing  int      `json:"standing"`
	Launch    int      `json:"launch"`
	Warnings  []string `json:"warnings,omitempty"`
}

type Instance struct {
	ProviderID       string            `json:"provider_id"`
	GeneratorID      string            `json:"generator_id"`
	Address          string            `json:"address,omitempty"`
	State            string            `json:"state"`
	Healthy          bool              `json:"healthy"`
	ArchiveTerminal  bool              `json:"archive_terminal"`
	IdentityAttested bool              `json:"identity_attested"`
	LaunchedAt       time.Time         `json:"launched_at,omitempty"`
	Tags             map[string]string `json:"tags,omitempty"`
}

type Observation struct {
	ProviderOperationID string        `json:"provider_operation_id,omitempty"`
	Requested           int           `json:"requested"`
	Observed            int           `json:"observed"`
	Ready               int           `json:"ready"`
	Pending             int           `json:"pending"`
	Draining            int           `json:"draining"`
	Failed              int           `json:"failed"`
	Terminal            bool          `json:"terminal"`
	RetryAfter          time.Duration `json:"retry_after,omitempty"`
	Instances           []Instance    `json:"instances,omitempty"`
	Message             string        `json:"message,omitempty"`
}

type AdoptionRequest struct {
	ExecutionTargetID string `json:"execution_target_id"`
	FleetPoolID       string `json:"fleet_pool_id"`
	ProviderID        string `json:"provider_id"`
	OperationID       string `json:"operation_id"`
	GeneratorID       string `json:"generator_id"`
	LaunchNonce       string `json:"launch_nonce"`
}

// Backend operations are idempotent under CapacityRequest.IdempotencyKey.
// Reconcile is deliberately poll-shaped: cloud control planes acknowledge
// mutations before generators are healthy, so provider acceptance is never
// treated as completion.
type Backend interface {
	Describe() BackendInfo
	Validate(context.Context) []ProbeResult
	Plan(context.Context, CapacityRequest) (Plan, error)
	Reconcile(context.Context, CapacityRequest) (Observation, error)
	Cancel(context.Context, CapacityRequest) error
	Drain(context.Context, CapacityRequest, []Instance) error
	Deprovision(context.Context, CapacityRequest, []Instance) error
	Discover(context.Context, CapacityRequest) ([]Instance, error)
	Adopt(context.Context, AdoptionRequest) (Instance, error)
}

// EnsureArchiveTerminal is the destruction fence for ephemeral compute. A
// cordoned/drained engine can still hold controller-pulled artifacts; stopping
// it before this check would silently lose the archive.
func EnsureArchiveTerminal(instances []Instance) error {
	for _, instance := range instances {
		if !instance.ArchiveTerminal {
			return ErrArchiveNotDrained
		}
	}
	return nil
}

// ValidateLaunchIntents keeps provider mutations tied to durable intent rows.
// A provider must never synthesize an identity after the controller has lost
// the database record that is responsible for cleanup and orphan adoption.
func ValidateLaunchIntents(intents []LaunchIntent, required int) error {
	if required <= 0 {
		return nil
	}
	if len(intents) < required {
		return fmt.Errorf("%d durable launch intents are required, got %d", required, len(intents))
	}
	for index, intent := range intents[:required] {
		if strings.TrimSpace(intent.ClientToken) == "" || strings.TrimSpace(intent.GeneratorID) == "" || strings.TrimSpace(intent.LaunchNonce) == "" {
			return fmt.Errorf("durable launch intent %d is incomplete", index)
		}
	}
	return nil
}

// ValidateLaunchScope ensures every ephemeral provider mutation carries the
// durable ownership coordinates used for retry discovery and orphan reaping.
func ValidateLaunchScope(request CapacityRequest, controllerID string, required int) error {
	if required <= 0 {
		return nil
	}
	if strings.TrimSpace(controllerID) == "" || strings.TrimSpace(request.OperationID) == "" || strings.TrimSpace(request.ExecutionTargetID) == "" || strings.TrimSpace(request.FleetPoolID) == "" {
		return errors.New("burst launch requires controller, operation, execution target, and fleet pool identities")
	}
	return nil
}
