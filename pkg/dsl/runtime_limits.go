package dsl

import httpprotocol "github.com/jasonczech/apexload/pkg/protocol/httpcontract"

// Runtime hard limits are enforced both by validation and by allocation hot
// paths. They are deliberately far above normal authoring values while keeping
// malformed scripts from turning integer fields into generator-sized
// allocations or effectively unbounded CPU loops.
const (
	MaxGeneratedParameterBytes = 1 << 20
	MaxParameterCharsetBytes   = 64 << 10
	MaxParameterValues         = 1_000_000
	MaxParameterRowIndex       = 1_000_000_000
	MaxParameterOffsetSeconds  = 10 * 365 * 24 * 60 * 60
	MaxControlLoopIterations   = 1_000_000
	// MaxScriptActionDefinitions bounds the combined number of typed actions and
	// untyped http_sequence child requests accepted by validation. The guard is
	// intentionally evaluated iteratively before any recursive action validation.
	MaxScriptActionDefinitions = 10_000
	// MaxScriptActionDepth bounds nested control steps/else trees. A top-level
	// action has depth 1.
	MaxScriptActionDepth    = 64
	MaxActionExtractors     = 256
	MaxActionAssertions     = httpprotocol.MaxActionAssertions
	MaxRunIterations        = 1_000_000_000
	MaxOpenWorkloadInFlight = 100_000
	MaxPacingTPS            = 1_000_000
)
