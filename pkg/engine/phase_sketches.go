package engine

import (
	"math"
	"sort"
	"time"
)

// PhaseSketchVersion is the wire version of PhaseSketches. A generator that
// predates it sends none, and a merge that includes such a part reports the
// phase percentiles as unknown rather than as zero.
const PhaseSketchVersion uint32 = 1

// Network phase names, shared by the wire, the live JSON and phase-aware
// threshold rules.
const (
	PhaseDNS      = "dns"
	PhaseConnect  = "connect"
	PhaseTLS      = "tls"
	PhaseServer   = "server"
	PhaseDownload = "download"
)

// NetworkPhases lists the phases with a sketch, in waterfall order.
var NetworkPhases = []string{PhaseDNS, PhaseConnect, PhaseTLS, PhaseServer, PhaseDownload}

// ValidNetworkPhase reports whether name is one of NetworkPhases.
func ValidNetworkPhase(name string) bool {
	for _, phase := range NetworkPhases {
		if phase == name {
			return true
		}
	}
	return false
}

// MaxWindowPhaseSketchBins bounds the sketch buckets one window's phase
// sketches may hold across all its entries (about 1 MiB retained, roughly
// 400 KiB of protobuf). The budget fills greedily, the fleet first and then
// the busiest entries; an entry that does not fit in what is left sends no
// phase sketches, which a reader treats as unknown percentiles, never as zero.
const MaxWindowPhaseSketchBins = 1 << 16

// PhaseSketches holds the duration distribution (nanoseconds) of each network
// phase over the samples that spent time in it, by the rules of
// PhaseObservations: DNS, connect and TLS only for samples where that phase
// took measurable time, and server (TTFB - DNS - connect - TLS, at least 0)
// and download (duration - TTFB, at least 0) only for samples that got a
// first byte. A nil phase had no such sample. So the DNS sketch holds
// PhaseObservations.DNS values, and server and download hold count - NoTTFB.
//
// A nil *PhaseSketches on an entry with samples means unknown (an older
// generator, or the window's sketch budget ran out), not "no phase time".
type PhaseSketches struct {
	DNS      *DurationSketchSnapshot `json:"dns,omitempty"`
	Connect  *DurationSketchSnapshot `json:"connect,omitempty"`
	TLS      *DurationSketchSnapshot `json:"tls,omitempty"`
	Server   *DurationSketchSnapshot `json:"server,omitempty"`
	Download *DurationSketchSnapshot `json:"download,omitempty"`
}

// Phase returns the named phase's sketch, or nil.
func (p *PhaseSketches) Phase(name string) *DurationSketchSnapshot {
	if p == nil {
		return nil
	}
	switch name {
	case PhaseDNS:
		return p.DNS
	case PhaseConnect:
		return p.Connect
	case PhaseTLS:
		return p.TLS
	case PhaseServer:
		return p.Server
	case PhaseDownload:
		return p.Download
	}
	return nil
}

func (p *PhaseSketches) slots() [5]**DurationSketchSnapshot {
	return [5]**DurationSketchSnapshot{&p.DNS, &p.Connect, &p.TLS, &p.Server, &p.Download}
}

// Clone returns an independent copy, or nil for nil.
func (p *PhaseSketches) Clone() *PhaseSketches {
	if p == nil {
		return nil
	}
	out := &PhaseSketches{}
	src, dst := p.slots(), out.slots()
	for i := range src {
		*dst[i] = CloneDurationSketchSnapshot(*src[i])
	}
	return out
}

// Bins is the number of sketch buckets held across every phase.
func (p *PhaseSketches) Bins() int {
	if p == nil {
		return 0
	}
	total := 0
	for _, slot := range p.slots() {
		if *slot != nil {
			total += len((*slot).Bins)
		}
	}
	return total
}

// Bytes estimates the memory (and wire size) the sketches retain.
func (p *PhaseSketches) Bytes() int {
	if p == nil {
		return 0
	}
	bytes := 48
	for _, slot := range p.slots() {
		bytes += durationDistributionBytes(nil, *slot)
	}
	return bytes
}

// ConsistentWith reports whether every sketch is valid and holds exactly the
// samples the observations say spent time in its phase. Sketches that do not
// agree with the counts they claim to describe are not used.
func (p *PhaseSketches) ConsistentWith(observed *PhaseObservations, count int64) bool {
	if p == nil || observed == nil || count <= 0 {
		return false
	}
	firstByte := count - observed.NoTTFB
	if firstByte < 0 {
		return false
	}
	want := [5]int64{observed.DNS, observed.Connect, observed.TLS, firstByte, firstByte}
	for i, slot := range p.slots() {
		sketch := *slot
		if sketch == nil {
			if want[i] != 0 {
				return false
			}
			continue
		}
		if want[i] <= 0 || DurationSketchForCount(sketch, want[i]) == nil {
			return false
		}
	}
	return true
}

// PhaseSketchRecorder records one window's phase samples. The zero value is
// ready to use; buckets are allocated only for phases that are observed.
type PhaseSketchRecorder struct {
	phases [5]*DurationSketch
}

// Observe records one sample's phases (see PhaseSketches for the rules).
func (r *PhaseSketchRecorder) Observe(dns, connect, tls, ttfb, duration time.Duration) {
	r.ObserveMs(durationToMilliseconds(dns), durationToMilliseconds(connect), durationToMilliseconds(tls), durationToMilliseconds(ttfb), durationToMilliseconds(duration))
}

// ObserveMs is Observe for millisecond values.
func (r *PhaseSketchRecorder) ObserveMs(dnsMs, connectMs, tlsMs, ttfbMs, durationMs float64) {
	if r == nil {
		return
	}
	add := func(index int, ms float64) {
		if math.IsNaN(ms) || math.IsInf(ms, 0) {
			ms = 0
		}
		if r.phases[index] == nil {
			r.phases[index] = NewDurationSketch()
		}
		r.phases[index].Add(math.Max(0, ms) * float64(time.Millisecond))
	}
	// The same "took measurable time" test as PhaseObservations.ObserveMs, so
	// each sketch holds exactly the samples the observations count.
	if dnsMs > 0 {
		add(0, dnsMs)
	}
	if connectMs > 0 {
		add(1, connectMs)
	}
	if tlsMs > 0 {
		add(2, tlsMs)
	}
	if ttfbMs > 0 {
		setup := max(0, dnsMs) + max(0, connectMs) + max(0, tlsMs)
		add(3, ttfbMs-setup)
		add(4, durationMs-ttfbMs)
	}
}

// Snapshot returns the recorded sketches; phases with no sample stay nil.
func (r *PhaseSketchRecorder) Snapshot() *PhaseSketches {
	out := &PhaseSketches{}
	if r == nil {
		return out
	}
	for i, slot := range out.slots() {
		if sketch := r.phases[i]; sketch != nil && sketch.Count() > 0 {
			snapshot := sketch.Snapshot()
			*slot = &snapshot
		}
	}
	return out
}

// PhaseSketchMerge merges phase sketches from several parts. A part with
// samples but no sketches (an older generator) makes the result unknown.
type PhaseSketchMerge struct {
	phases  [5]*DurationSketch
	unknown bool
	seen    bool
}

// Add folds one part with count samples into the merge.
func (m *PhaseSketchMerge) Add(sketches *PhaseSketches, count int64) {
	if m == nil || count <= 0 {
		return
	}
	m.seen = true
	if sketches == nil {
		m.unknown = true
		return
	}
	for i, slot := range sketches.slots() {
		snapshot := *slot
		if snapshot == nil {
			continue
		}
		part := DurationSketchForCount(snapshot, int64(snapshot.Count))
		if part == nil {
			m.unknown = true
			return
		}
		if m.phases[i] == nil {
			m.phases[i] = NewDurationSketch()
		}
		m.phases[i].Merge(part)
	}
}

// Result returns the merged sketches, or nil when any part was unknown or no
// part had samples.
func (m *PhaseSketchMerge) Result() *PhaseSketches {
	if m == nil || !m.seen || m.unknown {
		return nil
	}
	out := &PhaseSketches{}
	for i, slot := range out.slots() {
		if sketch := m.phases[i]; sketch != nil && sketch.Count() > 0 {
			snapshot := sketch.Snapshot()
			*slot = &snapshot
		}
	}
	return out
}

// PhasePercentiles are one phase's percentiles (ms) over Samples samples that
// spent time in it.
type PhasePercentiles struct {
	Samples int64   `json:"samples"`
	P50Ms   float64 `json:"p50_ms"`
	P90Ms   float64 `json:"p90_ms"`
	P95Ms   float64 `json:"p95_ms"`
	P99Ms   float64 `json:"p99_ms"`
}

// PhasePercentileSet is the JSON form of PhaseSketches: each phase's
// percentiles over the samples that spent time in it. A phase is absent when
// no sample did; the whole set is absent when the percentiles are unknown.
type PhasePercentileSet struct {
	Version  uint32            `json:"version"`
	DNS      *PhasePercentiles `json:"dns,omitempty"`
	Connect  *PhasePercentiles `json:"connect,omitempty"`
	TLS      *PhasePercentiles `json:"tls,omitempty"`
	Server   *PhasePercentiles `json:"server,omitempty"`
	Download *PhasePercentiles `json:"download,omitempty"`
}

// Phase returns the named phase's percentiles, or nil.
func (s *PhasePercentileSet) Phase(name string) *PhasePercentiles {
	if s == nil {
		return nil
	}
	switch name {
	case PhaseDNS:
		return s.DNS
	case PhaseConnect:
		return s.Connect
	case PhaseTLS:
		return s.TLS
	case PhaseServer:
		return s.Server
	case PhaseDownload:
		return s.Download
	}
	return nil
}

// Value returns the percentile (50, 90, 95 or 99) in ms and whether it is
// known: false for an unknown set, a phase with no samples or another
// percentile.
func (p *PhasePercentiles) Value(percentile int) (float64, bool) {
	if p == nil || p.Samples <= 0 {
		return 0, false
	}
	switch percentile {
	case 50:
		return p.P50Ms, true
	case 90:
		return p.P90Ms, true
	case 95:
		return p.P95Ms, true
	case 99:
		return p.P99Ms, true
	}
	return 0, false
}

// Percentiles reports each phase's P50/P90/P95/P99, or nil for nil sketches.
func (p *PhaseSketches) Percentiles() *PhasePercentileSet {
	if p == nil {
		return nil
	}
	out := &PhasePercentileSet{Version: PhaseSketchVersion}
	dst := [5]**PhasePercentiles{&out.DNS, &out.Connect, &out.TLS, &out.Server, &out.Download}
	for i, slot := range p.slots() {
		sketch := DurationSketchForCount(*slot, int64(sketchCount(*slot)))
		if sketch == nil {
			continue
		}
		ms := func(q float64) float64 { return sketch.Quantile(q) / float64(time.Millisecond) }
		*dst[i] = &PhasePercentiles{Samples: sketch.Count(), P50Ms: ms(0.50), P90Ms: ms(0.90), P95Ms: ms(0.95), P99Ms: ms(0.99)}
	}
	return out
}

func sketchCount(snapshot *DurationSketchSnapshot) uint64 {
	if snapshot == nil {
		return 0
	}
	return snapshot.Count
}

// PhaseSketchBudgetEntry is one entry competing for a window's phase-sketch
// budget.
type PhaseSketchBudgetEntry struct {
	Key      string
	Count    int64
	Sketches **PhaseSketches
}

// ApplyPhaseSketchBudget keeps entries' phase sketches while their buckets fit
// within budget, the busiest entries first (then by key), and clears the rest
// (unknown percentiles). Entries listed first in priority keep theirs first.
func ApplyPhaseSketchBudget(budget int, priority []PhaseSketchBudgetEntry, entries []PhaseSketchBudgetEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		return entries[i].Key < entries[j].Key
	})
	remaining := budget
	for _, entry := range append(append([]PhaseSketchBudgetEntry(nil), priority...), entries...) {
		if entry.Sketches == nil || *entry.Sketches == nil {
			continue
		}
		bins := (*entry.Sketches).Bins()
		if bins > remaining {
			*entry.Sketches = nil
			continue
		}
		remaining -= bins
	}
}

// Clone returns an independent copy, or nil for nil.
func (s *PhasePercentileSet) Clone() *PhasePercentileSet {
	if s == nil {
		return nil
	}
	out := *s
	for _, slot := range []**PhasePercentiles{&out.DNS, &out.Connect, &out.TLS, &out.Server, &out.Download} {
		if *slot != nil {
			copy := **slot
			*slot = &copy
		}
	}
	return &out
}
