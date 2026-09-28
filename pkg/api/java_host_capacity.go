package api

import (
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const javaHostFacadeCapacityProtocol = "java_host_facade"

// These operator-selected dimensions reserve only generator/facade overhead.
// Broker reservations independently retain all JVM/pod memory through cleanup.
func javaHostFacadeUnit(p JavaHostProfileSummary) protocol.CapacityVector {
	return protocol.CapacityVector{CPUCores: float64(p.FacadeCPUMillicores) / 1000, MemoryMB: float64(p.FacadeMemoryBytes) / (1024 * 1024), NetworkMbps: float64(p.FacadeNetworkKbps) / 1000, VUsers: 1, Protocols: map[string]float64{javaHostFacadeCapacityProtocol: 1}}
}

func podShardDemand(group TestGroup, shard GeneratorShardSummary) protocol.CapacityVector {
	if shard.JavaHost != nil {
		return javaHostFacadeUnit(shard.JavaHost.Profile).Scale(float64(shard.VUsers))
	}
	return podGroupDemand(group).Scale(float64(shard.VUsers) / float64(group.VUsers))
}

func projectedJavaHostFacadeDemand(group TestGroup, load generatorLoadSnapshot, n int) protocol.CapacityVector {
	settings := javaHostSettings(group)
	if settings == nil || load.JavaHost == nil {
		return protocol.CapacityVector{}
	}
	digest, err := engine.JavaPooledScriptSHA256(group.script)
	if err != nil {
		return protocol.CapacityVector{}
	}
	for _, p := range load.JavaHost.Profiles {
		if p.ScriptSHA256 == digest && p.ProfileID == settings.ProfileID {
			return javaHostFacadeUnit(p).Scale(float64(n))
		}
	}
	return protocol.CapacityVector{}
}
