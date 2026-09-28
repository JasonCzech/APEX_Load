package generatorrpc

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
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
	if assignment.GetArchiveSourceTarget() != "" {
		fingerprintWriteString(h, "archive-source-target-v1")
		fingerprintWriteString(h, assignment.GetArchiveSourceTarget())
	}
	if assignment == nil {
		fingerprintWriteBool(h, false)
		return fingerprintDigest(h)
	}
	fingerprintWriteBool(h, true)
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
