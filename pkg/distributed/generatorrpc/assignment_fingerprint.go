package generatorrpc

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"math"
	"sort"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// workAssignmentFingerprint hashes the assignment's semantic fields directly.
// Deterministic protobuf marshaling previously materialized a second copy of an
// assignment that may legitimately contain a cache-sized script plus staged
// data files. Hashing length-delimited fields preserves retry identity without
// that request-sized transient allocation.
func workAssignmentFingerprint(assignment *pb.WorkAssignment) [sha256.Size]byte {
	h := sha256.New()
	if source := assignment.GetBrowserAuthoredDefinition(); source != nil {
		fingerprintWriteString(h, "browser-original-source-v1")
		fingerprintWriteUint64(h, uint64(source.Version))
		fingerprintWriteString(h, source.ScriptSha256)
		fingerprintWriteString(h, source.StructureSha256)
	}
	if v := assignment.GetBrowserInteractionEvidenceVersion(); v != 0 {
		fingerprintWriteString(h, "browser-interaction-version")
		fingerprintWriteUint64(h, uint64(v))
	}
	if assignment.GetArchiveSourceTarget() != "" {
		fingerprintWriteString(h, "archive-source-target-v1")
		fingerprintWriteString(h, assignment.GetArchiveSourceTarget())
	}
	if assignment == nil {
		fingerprintWriteBool(h, false)
		return fingerprintDigest(h)
	}
	fingerprintWriteBool(h, true)
	fingerprintWriteBool(h, assignment.GetPrepareOnly())
	claim := assignment.GetRuntimeResourceClaim()
	fingerprintWriteBool(h, claim != nil)
	if claim != nil {
		fingerprintWriteString(h, claim.GetContract())
		fingerprintWriteString(h, claim.GetProtocol())
		fingerprintWriteUint64(h, uint64(claim.GetPeakVusers()))
		fingerprintWriteBool(h, claim.GetRequested() != nil)
		fingerprintWriteUint64(h, uint64(claim.GetRequested().GetProcessors()))
		fingerprintWriteUint64(h, uint64(claim.GetRequested().GetWorkers()))
		fingerprintWriteUint64(h, uint64(claim.GetRequested().GetMaxPartitionsPerProcessor()))
	}
	placement := assignment.GetJavaHost()
	fingerprintWriteBool(h, placement != nil)
	if placement != nil {
		fingerprintWriteString(h, placement.Contract)
		fingerprintWriteString(h, placement.ScriptSha256)
		fingerprintWriteString(h, placement.ProfileId)
		fingerprintWriteString(h, placement.ConfigurationSha256)
		fingerprintWriteString(h, placement.PolicySha256)
		fingerprintWriteString(h, placement.FacadeResourcesSha256)
		fingerprintWriteUint64(h, uint64(placement.PreparedVusers))
		fingerprintWriteUint64(h, uint64(placement.PreparationMs))
		fingerprintWriteUint64(h, uint64(placement.RunLifetimeMs))
	}
	fingerprintWriteString(h, SourcePlanDigest(assignment.GetSourcePlan()))
	fingerprintWriteString(h, assignment.GetTestId())
	fingerprintWriteString(h, assignment.GetTerminationMode())
	fingerprintWriteString(h, assignment.GetGroupName())
	fingerprintWriteUint64(h, uint64(assignment.GetTargetDurationSeconds()))
	fingerprintWriteString(h, assignment.GetScriptId())
	fingerprintWriteBytes(h, assignment.GetScriptContent())
	fingerprintWriteUint64(h, uint64(uint32(assignment.GetVuserCount())))
	fingerprintWriteUint64(h, uint64(uint32(assignment.GetRampUpSeconds())))

	environmentKeys := make([]string, 0, len(assignment.GetEnvironment()))
	for key := range assignment.GetEnvironment() {
		environmentKeys = append(environmentKeys, key)
	}
	sort.Strings(environmentKeys)
	fingerprintWriteUint64(h, uint64(len(environmentKeys)))
	for _, key := range environmentKeys {
		fingerprintWriteString(h, key)
		fingerprintWriteString(h, assignment.GetEnvironment()[key])
	}

	fingerprintWriteUint64(h, uint64(len(assignment.GetDataFiles())))
	for _, file := range assignment.GetDataFiles() {
		fingerprintWriteBool(h, file != nil)
		if file == nil {
			continue
		}
		fingerprintWriteString(h, file.GetName())
		fingerprintWriteBytes(h, file.GetContent())
		fingerprintWriteString(h, file.GetSha256())
	}

	fingerprintWriteUint64(h, uint64(len(assignment.GetRedactionRules())))
	for _, rule := range assignment.GetRedactionRules() {
		fingerprintWriteBool(h, rule != nil)
		if rule == nil {
			continue
		}
		fingerprintWriteString(h, rule.GetName())
		fingerprintWriteString(h, rule.GetPattern())
		fingerprintWriteString(h, rule.GetReplacement())
	}

	fingerprintWriteUint64(h, assignment.GetExecutionGeneration())
	fingerprintWriteString(h, assignment.GetExpectedScriptHash())
	fingerprintWriteDuration(h, assignment.GetTargetDuration())
	fingerprintWriteDuration(h, assignment.GetRampUp())
	fingerprintWriteDuration(h, assignment.GetRampDown())
	fingerprintWriteString(h, assignment.GetInitStrategy())
	fingerprintWriteDuration(h, assignment.GetPreBaseline())
	fingerprintWriteDuration(h, assignment.GetPostBaseline())
	fingerprintWriteUint64(h, uint64(assignment.GetShardOrdinal()))
	fingerprintWriteUint64(h, uint64(assignment.GetShardCount()))
	fingerprintWriteUint64(h, uint64(assignment.GetParameterPopulation()))
	fingerprintWriteUint64(h, uint64(assignment.GetParameterOffset()))
	fingerprintWriteUint64(h, assignment.GetVuserIdOffset())
	fingerprintWriteBool(h, assignment.GetStartPaused())
	fingerprintWriteDuration(h, assignment.GetDurationOffset())
	fingerprintWriteString(h, assignment.GetScenarioId())
	fingerprintWriteUint64(h, uint64(len(assignment.GetRunCorrelationOrigins())))
	for _, origin := range assignment.GetRunCorrelationOrigins() {
		fingerprintWriteString(h, origin)
	}
	// Carriers are written only when present so assignments without them keep
	// the fingerprint older controllers and generators compute.
	if carriers := assignment.GetCorrelationCarriers(); len(carriers) > 0 || assignment.GetCorrelationTestName() != "" {
		fingerprintWriteString(h, "correlation_policy_v2")
		fingerprintWriteUint64(h, uint64(len(carriers)))
		for _, carrier := range carriers {
			fingerprintWriteString(h, carrier)
		}
		fingerprintWriteString(h, assignment.GetCorrelationTestName())
	}
	// Written only when present, so assignments without a projection keep
	// the fingerprint older controllers and generators compute.
	if projection := assignment.GetJointEvidenceProjection(); projection != nil {
		fingerprintWriteString(h, FeatureJointEvidenceV4)
		fingerprintWriteString(h, projection.GetPopulation())
		fingerprintWriteUint64(h, uint64(len(projection.GetDimensions())))
		for _, dimension := range projection.GetDimensions() {
			fingerprintWriteString(h, dimension)
		}
	}
	if body := assignment.GetLiveTableBindingsJson(); len(body) > 0 {
		fingerprintWriteString(h, FeatureLiveTablesV1)
		fingerprintWriteBytes(h, body)
	}
	if body := assignment.GetRendezvousConfigJson(); len(body) > 0 {
		fingerprintWriteString(h, "distributed_rendezvous_v1")
		fingerprintWriteBytes(h, body)
	}
	// Written only when set, so an uncapped assignment keeps the fingerprint
	// older controllers and generators compute.
	if assignment.GetThroughputCapEnabled() {
		fingerprintWriteString(h, FeatureThroughputCapV1)
		fingerprintWriteUint64(h, math.Float64bits(assignment.GetThroughputCapTps()))
	}
	fingerprintWriteBool(h, assignment.GetRequireTestExecutionLease())
	fingerprintWriteBytes(h, assignment.GetRunSupervisionToken())
	return fingerprintDigest(h)
}

func fingerprintWriteDuration(h hash.Hash, duration *durationpb.Duration) {
	fingerprintWriteBool(h, duration != nil)
	if duration == nil {
		return
	}
	fingerprintWriteUint64(h, uint64(duration.GetSeconds()))
	fingerprintWriteUint64(h, uint64(uint32(duration.GetNanos())))
}

func fingerprintWriteString(h hash.Hash, value string) {
	fingerprintWriteBytes(h, []byte(value))
}

func fingerprintWriteBytes(h hash.Hash, value []byte) {
	fingerprintWriteUint64(h, uint64(len(value)))
	_, _ = h.Write(value)
}

func fingerprintWriteUint64(h hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = h.Write(encoded[:])
}

func fingerprintWriteBool(h hash.Hash, value bool) {
	if value {
		_, _ = h.Write([]byte{1})
		return
	}
	_, _ = h.Write([]byte{0})
}

func fingerprintDigest(h hash.Hash) [sha256.Size]byte {
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	return digest
}
