package api

import (
	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// JavaHostCapabilitySummary describes an explicitly negotiated remote backend.
// These are configured ceilings. Only the broker can reserve available capacity;
// draining and quarantined hosts can continue to consume it after a run stops.
type JavaHostCapabilitySummary struct {
	PreparedPopulation bool                     `json:"prepared_population,omitempty"`
	Contract           string                   `json:"contract"`
	Mode               string                   `json:"mode"`
	Containment        string                   `json:"containment"`
	MaxHosts           int32                    `json:"max_hosts"`
	MaxVUsers          int32                    `json:"max_vusers"`
	MaxMemoryBytes     int64                    `json:"max_memory_bytes"`
	Profiles           []JavaHostProfileSummary `json:"profiles"`
}

type JavaHostProfileSummary struct {
	ScriptSHA256          string `json:"script_sha256"`
	ProfileID             string `json:"profile_id"`
	ConfigurationSHA256   string `json:"configuration_sha256"`
	PolicySHA256          string `json:"policy_sha256"`
	MemoryBytes           int64  `json:"memory_bytes"`
	PreparationMS         int64  `json:"preparation_ms"`
	MaxRunMS              int64  `json:"max_run_ms"`
	InvocationMS          int64  `json:"invocation_ms"`
	FacadeCPUMillicores   int64  `json:"facade_cpu_millicores,omitempty"`
	FacadeMemoryBytes     int64  `json:"facade_memory_bytes,omitempty"`
	FacadeNetworkKbps     int64  `json:"facade_network_kbps,omitempty"`
	FacadeResourcesSHA256 string `json:"facade_resources_sha256,omitempty"`
}

func javaHostCapabilitySummary(caps *pb.JavaHostCapabilities, features []string) *JavaHostCapabilitySummary {
	if !protocol.RuntimeFeaturesSatisfy(features, generatorrpc.FeatureJavaHostBrokerV1) || !generatorrpc.ValidJavaHostCapabilities(caps) {
		return nil
	}
	out := &JavaHostCapabilitySummary{PreparedPopulation: protocol.RuntimeFeaturesSatisfy(features, generatorrpc.FeatureJavaHostPreparedPopulationV1), Contract: caps.Contract, Mode: caps.Mode, Containment: caps.Containment, MaxHosts: caps.MaxHosts, MaxVUsers: caps.MaxVusers, MaxMemoryBytes: caps.MaxMemoryBytes}
	for _, p := range caps.Profiles {
		out.Profiles = append(out.Profiles, JavaHostProfileSummary{ScriptSHA256: p.ScriptSha256, ProfileID: p.ProfileId, ConfigurationSHA256: p.ConfigurationSha256, PolicySHA256: p.PolicySha256, MemoryBytes: p.MemoryBytes, PreparationMS: p.PreparationMs, MaxRunMS: p.MaxRunMs, InvocationMS: p.InvocationMs, FacadeCPUMillicores: p.FacadeCpuMillicores, FacadeMemoryBytes: p.FacadeMemoryBytes, FacadeNetworkKbps: p.FacadeNetworkKbps, FacadeResourcesSHA256: p.FacadeResourcesSha256})
	}
	return out
}

func cloneJavaHostCapabilitySummary(caps *JavaHostCapabilitySummary) *JavaHostCapabilitySummary {
	if caps == nil {
		return nil
	}
	out := *caps
	out.Profiles = append([]JavaHostProfileSummary(nil), caps.Profiles...)
	return &out
}
