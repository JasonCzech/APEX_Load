package messaging

import "strings"

// Protocol families whose send steps deliver action metadata on the wire.
const (
	FamilyKafka = "kafka"
	FamilyJMS   = "jms"
)

// MetadataKeyIsProvenance reports whether an action metadata key is import
// provenance that a send step keeps off the wire. Kafka copies every other
// string metadata entry into the record headers and JMS into the message
// properties, so replay readiness (pkg/dsl/varflow) reads those entries.
// JMS predates the source_node_id and jmeter_* keys and still sends them.
// source_transaction is not provenance for either: stored scripts send it.
func MetadataKeyIsProvenance(family, key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "original_url", "source_path", "source_step_id", "source_profile", "source_uid", "source_evidence":
		return true
	case "source_node_id":
		return family == FamilyKafka
	}
	return strings.HasPrefix(key, "asyncapi_") || (family == FamilyKafka && strings.HasPrefix(key, "jmeter_"))
}

// MetadataKeyIsEvidence reports whether a top-level action metadata key is
// import evidence: provenance that every send step keeps off the wire. The
// runtime reads it only as static attribution (the source step, profile and
// path a result reports, the run archive's hierarchy fallback, the browser
// self-heal identity of a step) and keeps it as recorded instead of
// interpolating it, so a templated legacy source_path is reported verbatim. A
// guided repair that rebinds a step's executable fields therefore leaves an
// evidence copy of the old reference that neither refuses replay (pkg/engine)
// nor counts as a read (pkg/dsl/varflow).
func MetadataKeyIsEvidence(key string) bool {
	return MetadataKeyIsProvenance(FamilyKafka, key) && MetadataKeyIsProvenance(FamilyJMS, key)
}
