package enterprisearchive

import (
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestMergeScriptConfigDeepClonesReusableWebRuntime(t *testing.T) {
	failIfFound := true
	runtimeSettings := &dsl.ScriptConfig{WebRuntime: dsl.WebRuntimeConfig{
		AdditionalAttributes: map[string]string{"runtime": "kept"},
		DownloadFilters:      dsl.DownloadFiltersConfig{Mode: "block_list", Hosts: []string{"static.example.test"}},
		ContentChecks: []dsl.ContentCheckConfig{{
			Text:        "runtime check",
			FailIfFound: &failIfFound,
		}},
		DataFormatting: dsl.DataFormattingConfig{Chain: []dsl.DFETransform{{
			Type:    "gzip",
			Options: map[string]string{"mode": "decode"},
		}}},
		SourceIPs:       []string{"10.0.0.10"},
		NetworkProfiles: []dsl.NetworkProfileWeight{{Name: "slow", Weight: 100}},
	}}
	wantRuntime := cloneWebRuntimeConfig(runtimeSettings.WebRuntime)

	first := &dsl.ScriptConfig{WebRuntime: dsl.WebRuntimeConfig{
		AdditionalAttributes: map[string]string{"source.first": "one"},
		ContentChecks:        []dsl.ContentCheckConfig{{Text: "source first"}},
	}}
	mergeScriptConfig(first, runtimeSettings)

	if !reflect.DeepEqual(runtimeSettings.WebRuntime, wantRuntime) {
		t.Fatalf("runtime settings mutated during first merge:\n got %#v\nwant %#v", runtimeSettings.WebRuntime, wantRuntime)
	}
	first.WebRuntime.AdditionalAttributes["runtime"] = "changed"
	first.WebRuntime.DownloadFilters.Hosts[0] = "changed.example.test"
	first.WebRuntime.ContentChecks[0].Text = "changed"
	*first.WebRuntime.ContentChecks[0].FailIfFound = false
	first.WebRuntime.DataFormatting.Chain[0].Options["mode"] = "encode"
	first.WebRuntime.SourceIPs[0] = "127.0.0.1"
	first.WebRuntime.NetworkProfiles[0].Name = "changed"

	if !reflect.DeepEqual(runtimeSettings.WebRuntime, wantRuntime) {
		t.Fatalf("merged result aliases reusable runtime settings:\n got %#v\nwant %#v", runtimeSettings.WebRuntime, wantRuntime)
	}

	second := &dsl.ScriptConfig{WebRuntime: dsl.WebRuntimeConfig{
		AdditionalAttributes: map[string]string{"source.second": "two"},
		ContentChecks:        []dsl.ContentCheckConfig{{Text: "source second"}},
	}}
	mergeScriptConfig(second, runtimeSettings)

	if second.WebRuntime.AdditionalAttributes["runtime"] != "kept" ||
		second.WebRuntime.AdditionalAttributes["source.second"] != "two" {
		t.Fatalf("second merge attributes = %#v", second.WebRuntime.AdditionalAttributes)
	}
	if _, leaked := second.WebRuntime.AdditionalAttributes["source.first"]; leaked {
		t.Fatalf("first merge leaked into reused runtime settings: %#v", second.WebRuntime.AdditionalAttributes)
	}
	if len(second.WebRuntime.ContentChecks) != 2 ||
		second.WebRuntime.ContentChecks[0].Text != "runtime check" ||
		second.WebRuntime.ContentChecks[1].Text != "source second" {
		t.Fatalf("second merge content checks = %#v", second.WebRuntime.ContentChecks)
	}
	if !reflect.DeepEqual(runtimeSettings.WebRuntime, wantRuntime) {
		t.Fatalf("runtime settings mutated during reuse:\n got %#v\nwant %#v", runtimeSettings.WebRuntime, wantRuntime)
	}
}

func TestMergeScriptConfigPreservesStandaloneWebRuntimeFields(t *testing.T) {
	tests := []struct {
		name   string
		source dsl.WebRuntimeConfig
	}{
		{name: "jitter", source: dsl.WebRuntimeConfig{NetworkSimulation: dsl.NetworkSimulationConfig{JitterMs: 15}}},
		{name: "packet loss", source: dsl.WebRuntimeConfig{NetworkSimulation: dsl.NetworkSimulationConfig{PacketLossPct: 1.5}}},
		{name: "source IPs", source: dsl.WebRuntimeConfig{SourceIPs: []string{"10.0.0.10"}}},
		{name: "network profiles", source: dsl.WebRuntimeConfig{NetworkProfiles: []dsl.NetworkProfileWeight{{Name: "slow", Weight: 100}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			destination := &dsl.ScriptConfig{}
			mergeScriptConfig(destination, &dsl.ScriptConfig{WebRuntime: test.source})
			if !reflect.DeepEqual(destination.WebRuntime, test.source) {
				t.Fatalf("merged Web runtime = %#v, want %#v", destination.WebRuntime, test.source)
			}
		})
	}
}
