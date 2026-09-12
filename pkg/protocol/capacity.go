package protocol

import (
	"fmt"
	"math"
	"sort"
)

const CapacityReservationModel = "peak_capacity_v1"

// CapacityVector uses the same MB and Mbps units as the capacity estimator.
// Protocol entries represent concurrent VUsers, not measured utilization.
type CapacityVector struct {
	CPUCores    float64            `json:"cpu_cores"`
	MemoryMB    float64            `json:"memory_mb"`
	NetworkMbps float64            `json:"network_mbps"`
	VUsers      float64            `json:"vusers"`
	Protocols   map[string]float64 `json:"protocols,omitempty"`
}

func (v CapacityVector) Dimensions() map[string]float64 {
	out := map[string]float64{"cpu": v.CPUCores, "memory": v.MemoryMB, "network": v.NetworkMbps, "vusers": v.VUsers}
	for k, n := range v.Protocols {
		out["protocol:"+k] = n
	}
	return out
}

func (v CapacityVector) Validate(budget bool) error {
	for k, n := range v.Dimensions() {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || (budget && n == 0) {
			return fmt.Errorf("invalid capacity dimension %s", k)
		}
	}
	return nil
}

func (v CapacityVector) Scale(n float64) CapacityVector {
	out := CapacityVector{CPUCores: v.CPUCores * n, MemoryMB: v.MemoryMB * n, NetworkMbps: v.NetworkMbps * n, VUsers: v.VUsers * n, Protocols: make(map[string]float64, len(v.Protocols))}
	for k, x := range v.Protocols {
		out.Protocols[k] = x * n
	}
	return out
}

func (v CapacityVector) Add(other CapacityVector) CapacityVector {
	out := v.Scale(1)
	out.CPUCores += other.CPUCores
	out.MemoryMB += other.MemoryMB
	out.NetworkMbps += other.NetworkMbps
	out.VUsers += other.VUsers
	for k, n := range other.Protocols {
		out.Protocols[k] += n
	}
	return out
}

func (v CapacityVector) Remaining(used CapacityVector) CapacityVector {
	out := v.Add(used.Scale(-1))
	out.CPUCores = math.Max(0, out.CPUCores)
	out.MemoryMB = math.Max(0, out.MemoryMB)
	out.NetworkMbps = math.Max(0, out.NetworkMbps)
	out.VUsers = math.Max(0, out.VUsers)
	for k, n := range out.Protocols {
		out.Protocols[k] = math.Max(0, n)
	}
	return out
}

// Fits checks each dimension independently; a headline percentage is not an
// admission decision for a different workload mix.
func (v CapacityVector) Fits(demand CapacityVector) error {
	if err := v.Validate(true); err != nil {
		return err
	}
	if err := demand.Validate(false); err != nil {
		return err
	}
	limits := v.Dimensions()
	keys := make([]string, 0, len(demand.Dimensions()))
	for k := range demand.Dimensions() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		n := demand.Dimensions()[k]
		if n > limits[k]+1e-9 {
			return fmt.Errorf("insufficient %s capacity: need %.3f, available %.3f", k, n, limits[k])
		}
	}
	return nil
}

type CapacitySnapshot struct {
	ReportedTestIDs  []string       `json:"-"`
	ModelVersion     string         `json:"model_version"`
	State            string         `json:"state"`
	Incarnation      string         `json:"incarnation"`
	HeadroomPct      float64        `json:"headroom_pct"`
	Schedulable      CapacityVector `json:"schedulable"`
	Reserved         CapacityVector `json:"reserved"`
	Remaining        CapacityVector `json:"remaining"`
	ReservedPct      float64        `json:"reserved_pct"`
	RemainingPct     float64        `json:"remaining_pct"`
	Bottleneck       string         `json:"bottleneck,omitempty"`
	ObservedAtUnixMS int64          `json:"observed_at_unix_ms"`
	Confidence       string         `json:"confidence"`
	Dedicated        bool           `json:"dedicated"`
}

func (s *CapacitySnapshot) Clone() *CapacitySnapshot {
	if s == nil {
		return nil
	}
	out := *s
	out.ReportedTestIDs = append([]string(nil), s.ReportedTestIDs...)
	out.Schedulable = s.Schedulable.Scale(1)
	out.Reserved = s.Reserved.Scale(1)
	out.Remaining = s.Remaining.Scale(1)
	return &out
}

func NewCapacitySnapshot(budget, used CapacityVector, headroom float64) CapacitySnapshot {
	s := CapacitySnapshot{ModelVersion: CapacityReservationModel, State: "ready", HeadroomPct: headroom, Schedulable: budget, Reserved: used, Remaining: budget.Remaining(used), Confidence: "estimated"}
	limits := budget.Dimensions()
	keys := make([]string, 0, len(limits))
	for k := range limits {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if limits[k] > 0 {
			pct := used.Dimensions()[k] / limits[k] * 100
			if pct > s.ReservedPct {
				s.ReservedPct = pct
				s.Bottleneck = k
			}
		}
	}
	s.RemainingPct = math.Max(0, 100-s.ReservedPct)
	return s
}
