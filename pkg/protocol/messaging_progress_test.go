package protocol

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func progressFixture() *MessagingProgressSnapshot {
	now := time.Now().UTC()
	age := int64(10)
	position := MessagingProgressPosition{Sequence: "9007199254740993", ProviderOffset: "2:-1:-1", State: "observed", ObservedAt: now, ReceivedAt: now, ObservationAgeMs: &age}
	return &MessagingProgressSnapshot{Version: MessagingProgressVersion, CapturedAt: now, Lifecycle: "running", Rows: []MessagingProgressRow{{Protocol: "eventhubs", Mode: "processor", SourceID: "source", Source: "hub", Subscription: "workers", SessionID: "session", Partition: "0", Generation: "generation", Revision: "1", CaptureRevision: "1", CapturedAt: now, UpdatedAt: now, ExpectedPartitions: 1, InventoryComplete: true, Ownership: "owned", OwnershipVerifiedAt: now, OwnershipVerifiedAgeMs: &age, Acquired: 1, Delivered: 1, DeliveredPosition: position, CheckpointPosition: MessagingProgressPosition{State: "missing"}, TailPosition: position, Checkpoint: MessagingCheckpointProgress{Duration: MessagingDurationSummary{Metric: "checkpoint", Population: "checkpoint_operation"}}}}}
}
func TestMessagingProgressExactLagAndRoundtrip(t *testing.T) {
	s := progressFixture()
	s.Rows[0].TailPosition.Sequence = fmt.Sprint(int64(math.MaxInt64))
	if !s.Valid() {
		t.Fatal("fixture invalid")
	}
	data, e := json.Marshal(s)
	if e != nil {
		t.Fatal(e)
	}
	var got MessagingProgressSnapshot
	if e = json.Unmarshal(data, &got); e != nil {
		t.Fatal(e)
	}
	lag, ok := got.Rows[0].QualifiedLag("delivered", 100)
	if !ok || lag != "9214364837600034814" {
		t.Fatal(lag, ok)
	}
	if _, ok = got.Rows[0].QualifiedLag("checkpoint", 100); ok {
		t.Fatal("missing checkpoint fabricated lag")
	}
	got.Rows[0].TailPosition.State = "empty"
	got.Rows[0].TailPosition.Sequence = ""
	got.Rows[0].TailPosition.ProviderOffset = ""
	got.Rows[0].TailPosition.ObservationAgeMs = nil
	if !got.Valid() {
		t.Fatal("explicit empty invalid")
	}
	if _, ok = got.Rows[0].QualifiedLag("delivered", 100); ok {
		t.Fatal("empty tail fabricated lag")
	}
}
func TestMessagingProgressMergeReplacesCumulativeAndFreshensOnlyNewCaptures(t *testing.T) {
	s := progressFixture()
	var dst *MessagingProgressSnapshot
	MergeMessagingProgress(&dst, s, "trusted")
	s.Rows[0].Origin = "forged"
	s.Rows[0].Delivered = 2
	s.Rows[0].Revision = "2"
	s.Rows[0].CaptureRevision = "2"
	MergeMessagingProgress(&dst, s, "trusted")
	if len(dst.Rows) != 1 || dst.Rows[0].Delivered != 2 || dst.Rows[0].Origin != "trusted" {
		t.Fatal(dst)
	}
	replay := s.Clone()
	replay.Rows[0].Revision = "1"
	replay.Rows[0].Delivered = 1
	MergeMessagingProgress(&dst, replay, "trusted")
	if dst.Rows[0].Delivered != 2 {
		t.Fatal("replay changed totals")
	}
	newer := s.Clone()
	age := int64(99)
	newer.Rows[0].CaptureRevision = "3"
	newer.Rows[0].TailPosition.ObservationAgeMs = &age
	MergeMessagingProgress(&dst, newer, "trusted")
	if *dst.Rows[0].TailPosition.ObservationAgeMs != 99 {
		t.Fatal("capture not refreshed")
	}
	MergeMessagingProgress(&dst, s, "trusted")
	if *dst.Rows[0].TailPosition.ObservationAgeMs != 99 {
		t.Fatal("old capture freshened state")
	}
	bad := newer.Clone()
	bad.Rows[0].Delivered = 1
	bad.Rows[0].Revision = "4"
	bad.Rows[0].CaptureRevision = "4"
	MergeMessagingProgress(&dst, bad, "trusted")
	if !dst.Incomplete || dst.Rows[0].Delivered != 2 {
		t.Fatal("regressing evidence accepted")
	}
}
func TestMessagingProgressBoundsAndNumericPresence(t *testing.T) {
	s := progressFixture()
	data, _ := json.Marshal(s)
	for _, field := range []string{"delivered", "acquired", "expected_partitions", "attempts", "committed", "in_flight"} {
		for _, invalid := range []string{"null", "omit"} {
			t.Run(field+invalid, func(t *testing.T) {
				text := string(data)
				value := "0"
				if field == "delivered" || field == "acquired" || field == "expected_partitions" {
					value = "1"
				}
				old := `"` + field + `":` + value
				replacement := `"` + field + `":null`
				if invalid == "omit" {
					old += ","
					replacement = ""
				}
				text = strings.Replace(text, old, replacement, 1)
				var got MessagingProgressSnapshot
				if json.Unmarshal([]byte(text), &got) == nil {
					t.Fatal("missing/null population accepted")
				}
			})
		}
	}
	var got MessagingProgressSnapshot
	if json.Unmarshal([]byte(strings.Repeat(" ", MaxMessagingProgressBytes+1)), &got) == nil {
		t.Fatal("oversized input accepted")
	}
	empty := &MessagingProgressSnapshot{Version: MessagingProgressVersion, Lifecycle: "pending", CapturedAt: time.Now().UTC(), Rows: []MessagingProgressRow{}}
	b, _ := json.Marshal(empty)
	if !strings.Contains(string(b), `"rows":[]`) || json.Unmarshal(b, &got) != nil {
		t.Fatal("empty supported scope did not roundtrip", string(b))
	}
	for i := 0; i < MaxMessagingProgressRows+2; i++ {
		row := progressFixture().Rows[0]
		row.SessionID = fmt.Sprint(i)
		one := progressFixture()
		one.Rows = []MessagingProgressRow{row}
		MergeMessagingProgress(&empty, one)
	}
	if len(empty.Rows) > MaxMessagingProgressRows || !empty.Incomplete || empty.DroppedRows == 0 || !empty.Valid() {
		t.Fatal("merge bounds", empty)
	}
}
func TestMessagingProgressCloneIsolation(t *testing.T) {
	s := progressFixture()
	c := s.Clone()
	*c.Rows[0].OwnershipVerifiedAgeMs = 99
	if *s.Rows[0].OwnershipVerifiedAgeMs == 99 {
		t.Fatal("age aliased")
	}
}

func TestMessagingProgressCumulativeDistributionCannotBeRewritten(t *testing.T) {
	for _, kind := range []string{"sum", "extrema", "reason"} {
		t.Run(kind, func(t *testing.T) {
			original := progressFixture()
			r := &original.Rows[0]
			c := &r.Checkpoint
			c.Attempts = 2
			c.Committed = 1
			c.Failed = 1
			c.LastOutcome = "committed"
			c.LastCompletedAt = original.CapturedAt
			c.Duration.Eligible = 2
			c.Duration.Distribution.Add(.001)
			c.Duration.Unavailable = map[string]int64{"failed": 1}
			var out *MessagingProgressSnapshot
			MergeMessagingProgress(&out, original)
			changed := original.Clone()
			r = &changed.Rows[0]
			r.Revision = "2"
			r.CaptureRevision = "2"
			switch kind {
			case "sum":
				r.Checkpoint.Duration.Distribution.Sum = .0009
				r.Checkpoint.Duration.Distribution.Min = .0009
			case "extrema":
				r.Checkpoint.Duration.Distribution.Min = .0011
				r.Checkpoint.Duration.Distribution.Max = .0011
				r.Checkpoint.Duration.Distribution.Sum = .0011
			case "reason":
				r.Checkpoint.Duration.Unavailable = map[string]int64{"censored": 1}
			}
			MergeMessagingProgress(&out, changed)
			if !out.Incomplete {
				t.Fatal("rewritten cumulative evidence accepted", kind)
			}
		})
	}
}
func TestMessagingProgressDroppedCountsAreIdempotent(t *testing.T) {
	s := progressFixture()
	s.Incomplete = true
	s.DroppedRows = 7
	var out *MessagingProgressSnapshot
	for i := 0; i < 10; i++ {
		MergeMessagingProgress(&out, s)
	}
	if out.DroppedRows != 7 {
		t.Fatal("replayed drops counted again", out.DroppedRows)
	}
}

func TestMessagingProgressRejectsControlCharacters(t *testing.T) {
	for _, bad := range []string{"\x1b[31m", "with\ttab", "\u0085"} {
		s := progressFixture()
		s.Rows[0].Source = bad
		if s.Valid() {
			t.Fatal("control label accepted")
		}
		s = progressFixture()
		s.Rows[0].DeliveredPosition.ProviderOffset = bad
		if s.Valid() {
			t.Fatal("control offset accepted")
		}
	}
}

func TestMessagingProgressEmptySourceLifecyclesRemainMixed(t *testing.T) {
	for _, order := range [][]string{{"pending", "released"}, {"released", "pending"}, {"running", "released"}, {"released", "running"}} {
		var out *MessagingProgressSnapshot
		for _, life := range order {
			MergeMessagingProgress(&out, &MessagingProgressSnapshot{Version: MessagingProgressVersion, Lifecycle: life, CapturedAt: time.Now().UTC(), Rows: []MessagingProgressRow{}})
		}
		if !out.Valid() || out.Lifecycle != "" {
			t.Fatal("empty inventories overwrote mixed lifecycle", order, out)
		}
	}
}
func TestMessagingProgressCaptureTimestampFollowsCaptureRevision(t *testing.T) {
	s := progressFixture()
	var out *MessagingProgressSnapshot
	MergeMessagingProgress(&out, s)
	next := s.Clone()
	next.Rows[0].CaptureRevision = "2"
	next.Rows[0].CapturedAt = next.Rows[0].CapturedAt.Add(time.Second)
	MergeMessagingProgress(&out, next)
	if !out.Rows[0].CapturedAt.Equal(next.Rows[0].CapturedAt) {
		t.Fatal("newcapture did not retain row provenance")
	}
	MergeMessagingProgress(&out, s)
	if !out.Rows[0].CapturedAt.Equal(next.Rows[0].CapturedAt) {
		t.Fatal("old capture rewrote provenance")
	}
	s.Rows[0].CapturedAt = time.Time{}
	if s.Valid() {
		t.Fatal("missing row sourcecapture qualified")
	}
}

func TestMessagingProgressRetainsCurrentBeforeBoundedHistory(t *testing.T) {
	history := progressFixture()
	history.Lifecycle = "released"
	history.Rows = []MessagingProgressRow{}
	for i := 0; i < 64; i++ {
		r := progressFixture().Rows[0]
		r.Ownership = "released"
		r.SessionID = fmt.Sprintf("a-history-%02d", i)
		history.Rows = append(history.Rows, r)
	}
	current := progressFixture()
	current.Rows = []MessagingProgressRow{}
	for i := 0; i < 32; i++ {
		r := progressFixture().Rows[0]
		r.SessionID = fmt.Sprintf("z-current-%02d", i)
		current.Rows = append(current.Rows, r)
	}
	for _, sources := range [][]*MessagingProgressSnapshot{{history, current}, {current, history}} {
		var out *MessagingProgressSnapshot
		for _, s := range sources {
			MergeMessagingProgress(&out, s)
		}
		for i := 0; i < 3; i++ {
			MergeMessagingProgress(&out, history)
			MergeMessagingProgress(&out, current)
		}
		owned := 0
		for _, r := range out.Rows {
			if r.Ownership == "owned" {
				owned++
			}
		}
		if !out.Valid() || !out.Incomplete || owned != 32 || out.DroppedRows != 32 {
			t.Fatal("history displaced current evidence or replay inflated drops", owned, out.DroppedRows)
		}
	}
}

func TestMessagingProgressNumericAliasesCannotOverwriteObservedPopulation(t *testing.T) {
	s := progressFixture()
	data, _ := json.Marshal(s)
	for _, keys := range [][2]string{{"delivered", "Delivered"}, {"expected_partitions", "Expected_Partitions"}, {"attempts", "Attempts"}} {
		text := string(data)
		value := "0"
		if keys[0] != "attempts" {
			value = "1"
		}
		old := `"` + keys[0] + `":` + value
		replacement := old + `,"` + keys[1] + `":null`
		text = strings.Replace(text, old, replacement, 1)
		var got MessagingProgressSnapshot
		if json.Unmarshal([]byte(text), &got) == nil {
			t.Fatal("case alias nulled a required numeric population", keys)
		}
	}
}
