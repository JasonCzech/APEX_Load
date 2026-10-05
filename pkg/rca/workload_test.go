package rca

import (
	"reflect"
	"testing"
)

func closedKey() WorkloadKey {
	return WorkloadKey{
		Version: WorkloadKeyVersion, Source: WorkloadKeyFrozen, RuntimeDigest: "v1:runtime", Population: "transaction",
		Groups: map[string]WorkloadGroup{
			"browse":   {ArrivalModel: ArrivalClosed, Unit: LoadUnitVUsers, Levels: []float64{50, 100}, Pacing: "pacing=fixed_interval interval_ms=1000; think=replay", ScriptDigest: "v1:browse"},
			"checkout": {ArrivalModel: ArrivalClosed, Unit: LoadUnitVUsers, Levels: []float64{50, 100}, Pacing: "pacing=fixed_interval interval_ms=1000; think=replay", ScriptDigest: "v1:checkout"},
		},
	}
}

func cloneKey(k WorkloadKey) WorkloadKey {
	out := k
	out.Groups = map[string]WorkloadGroup{}
	for name, group := range k.Groups {
		group.Levels = append([]float64(nil), group.Levels...)
		out.Groups[name] = group
	}
	return out
}

func TestQualifyBaseline(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*WorkloadKey)
		status  string
		reasons []string
	}{
		{name: "same workload", mutate: func(*WorkloadKey) {}, status: BaselineQualified, reasons: []string{}},
		{
			name: "open versus closed",
			mutate: func(k *WorkloadKey) {
				for name, group := range k.Groups {
					group.ArrivalModel, group.Unit, group.Levels = ArrivalOpen, LoadUnitTPS, []float64{20, 40}
					k.Groups[name] = group
				}
			},
			status: BaselineUnqualified, reasons: []string{ReasonArrivalModelChanged},
		},
		{
			name: "equal VUsers with different pacing",
			mutate: func(k *WorkloadKey) {
				group := k.Groups["browse"]
				group.Pacing = "pacing=fixed_interval interval_ms=2000; think=replay"
				k.Groups["browse"] = group
			},
			status: BaselinePartial, reasons: []string{ReasonPacingChanged},
		},
		{
			name: "ten percent mix shift",
			mutate: func(k *WorkloadKey) {
				browse, checkout := k.Groups["browse"], k.Groups["checkout"]
				browse.Levels = []float64{50, 110}
				checkout.Levels = []float64{50, 90}
				k.Groups["browse"], k.Groups["checkout"] = browse, checkout
			},
			status: BaselineUnqualified, reasons: []string{ReasonMixChanged, ReasonOfferedLoadChanged},
		},
		{
			name: "offered load within five percent",
			mutate: func(k *WorkloadKey) {
				for name, group := range k.Groups {
					group.Levels = []float64{52, 104}
					k.Groups[name] = group
				}
			},
			status: BaselineQualified, reasons: []string{},
		},
		{
			name: "offered load beyond five percent",
			mutate: func(k *WorkloadKey) {
				for name, group := range k.Groups {
					group.Levels = []float64{50, 106}
					k.Groups[name] = group
				}
			},
			status: BaselineUnqualified, reasons: []string{ReasonOfferedLoadChanged},
		},
		{
			name: "different number of levels",
			mutate: func(k *WorkloadKey) {
				for name, group := range k.Groups {
					group.Levels = []float64{100}
					k.Groups[name] = group
				}
			},
			status: BaselineUnqualified, reasons: []string{ReasonLevelsChanged},
		},
		{
			name: "changed script and configuration",
			mutate: func(k *WorkloadKey) {
				group := k.Groups["checkout"]
				group.ScriptDigest = "v1:checkout-2"
				k.Groups["checkout"] = group
				k.RuntimeDigest = "v1:runtime-2"
			},
			status: BaselinePartial, reasons: []string{ReasonConfigChanged, ReasonScriptChanged},
		},
		{
			name: "derived key without script or configuration evidence",
			mutate: func(k *WorkloadKey) {
				k.Source, k.RuntimeDigest = WorkloadKeyDerived, ""
				for name, group := range k.Groups {
					group.ArrivalModel, group.ScriptDigest = ArrivalUnknown, ""
					k.Groups[name] = group
				}
			},
			status: BaselinePartial, reasons: []string{ReasonArrivalModelUnrecorded, ReasonConfigUnrecorded, ReasonScriptUnrecorded},
		},
		{name: "unrecorded baseline", mutate: func(k *WorkloadKey) { *k = WorkloadKey{} }, status: BaselinePartial, reasons: []string{ReasonWorkloadUnrecorded}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cloneKey(closedKey())
			tt.mutate(&candidate)
			got := QualifyBaseline(closedKey(), candidate)
			if got.Status != tt.status || !reflect.DeepEqual(got.Reasons, tt.reasons) {
				t.Fatalf("qualification = %s %v, want %s %v", got.Status, got.Reasons, tt.status, tt.reasons)
			}
		})
	}
}

func TestWorkloadMixAndAddedGroups(t *testing.T) {
	current := closedKey()
	if mix := current.Mix(); mix["browse"] != 0.5 || mix["checkout"] != 0.5 {
		t.Fatalf("mix = %v", mix)
	}
	// A small extra group stays within the mix tolerance; a large one does not.
	small := cloneKey(current)
	small.Groups["canary"] = WorkloadGroup{ArrivalModel: ArrivalClosed, Unit: LoadUnitVUsers, Levels: []float64{2}, ScriptDigest: "v1:canary"}
	if got := QualifyBaseline(current, small); got.Status != BaselineQualified || got.MixDistance == nil || *got.MixDistance > MixDistanceTolerance {
		t.Fatalf("small added group = %+v", got)
	}
	large := cloneKey(current)
	large.Groups["batch"] = WorkloadGroup{ArrivalModel: ArrivalClosed, Unit: LoadUnitVUsers, Levels: []float64{40}, ScriptDigest: "v1:batch"}
	if got := QualifyBaseline(current, large); got.Status != BaselineUnqualified || !reflect.DeepEqual(got.Reasons, []string{ReasonMixChanged}) {
		t.Fatalf("large added group = %+v", got)
	}
	// Open and closed groups each form their own share of the planned load.
	mixed := WorkloadKey{Version: WorkloadKeyVersion, Groups: map[string]WorkloadGroup{
		"api": {ArrivalModel: ArrivalOpen, Unit: LoadUnitTPS, Levels: []float64{30}},
		"ui":  {ArrivalModel: ArrivalClosed, Unit: LoadUnitVUsers, Levels: []float64{10}},
	}}
	if mix := mixed.Mix(); mix["api"] != 1 || mix["ui"] != 1 {
		t.Fatalf("per-unit mix = %v", mix)
	}
}
