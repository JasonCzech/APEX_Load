package sample

import (
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// TransactionWindow is a transaction marked while recording: a request that
// started from Start through End belongs to it. A zero End leaves it open to
// the end of the recording. StartOrder and EndOrder place its markers in the
// recorded marker sequence.
type TransactionWindow struct {
	Name       string
	Start      time.Time
	End        time.Time
	StartOrder int
	EndOrder   int
}

// transactionAt returns the innermost transaction open when the exchange
// started: of the windows holding that time, the one that started last.
func transactionAt(windows []TransactionWindow, ex exchange) string {
	if len(windows) == 0 || !ex.HasStartedAt {
		return ""
	}
	name := ""
	var latest time.Time
	for _, window := range windows {
		if ex.StartedAt.Before(window.Start) || (!window.End.IsZero() && ex.StartedAt.After(window.End)) {
			continue
		}
		if name == "" || !window.Start.Before(latest) {
			name, latest = window.Name, window.Start
		}
	}
	return name
}

// TimedNote is a note written while recording, at the time it was written.
type TimedNote struct {
	Text string
	At   time.Time
}

// placeNotes assigns each note to the first exchange that started at or after
// it, or to the last one to start when it was written after every request, as
// the browser script attaches a note to its next action. It returns the joined
// notes by exchange index, and how many notes it could not place because no
// exchange has a start time.
func placeNotes(notes []TimedNote, exchanges []exchange) (map[int]string, int) {
	if len(notes) == 0 {
		return nil, 0
	}
	timed := make([]int, 0, len(exchanges))
	for index, ex := range exchanges {
		if ex.HasStartedAt {
			timed = append(timed, index)
		}
	}
	if len(timed) == 0 {
		return nil, len(notes)
	}
	sort.SliceStable(timed, func(i, j int) bool { return exchanges[timed[i]].StartedAt.Before(exchanges[timed[j]].StartedAt) })
	ordered := slices.Clone(notes)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].At.Before(ordered[j].At) })
	texts := map[int][]string{}
	for _, note := range ordered {
		next := sort.Search(len(timed), func(i int) bool { return !exchanges[timed[i]].StartedAt.Before(note.At) })
		next = min(next, len(timed)-1)
		texts[timed[next]] = append(texts[timed[next]], note.Text)
	}
	placed := make(map[int]string, len(texts))
	for index, list := range texts {
		placed[index] = strings.Join(list, "; ")
	}
	return placed, 0
}

// transactionConflict counts requests whose own transaction tag differs from
// the transaction marked around them while recording.
type transactionConflict struct {
	tagged, marked string
	count          int
}

func noteTransactionConflict(conflicts []transactionConflict, tagged, marked string) []transactionConflict {
	for index := range conflicts {
		if conflicts[index].tagged == tagged && conflicts[index].marked == marked {
			conflicts[index].count++
			return conflicts
		}
	}
	return append(conflicts, transactionConflict{tagged: tagged, marked: marked, count: 1})
}

// insertTransactionMarkers gives a converted recording the start and end
// markers of the transactions marked while recording, shaped as the browser
// script carries them (dsl.TransactionMarker), so replay times each one as a
// VuGen HTTP script would rather than only grouping its requests. startedAt
// holds when each action started in the recording.
//
// A start goes before the first action that started in its window and an end
// after the last one; markers that fall between the same two actions keep
// their recorded order. A background conversation keeps running after the
// transaction its first request started in, so it places no marker. The
// recorded pause after the last action before markers moves past the ends
// placed there, as VuGen records lr_think_time after lr_end_transaction, so
// it is not timed as part of a transaction that has finished.
//
// It returns where each original action went, or nil and why when the
// recording's timing cannot place the markers.
func insertTransactionMarkers(script *dsl.Script, startedAt []time.Time, windows []TransactionWindow) ([]int, string) {
	if script == nil || len(windows) == 0 {
		return nil, ""
	}
	actions := script.Actions
	if len(startedAt) != len(actions) {
		return nil, "the recorded transactions tag their requests but are not timed: the recording's request times do not line up with the generated steps"
	}
	type anchor struct {
		index int
		at    time.Time
	}
	anchors := make([]anchor, 0, len(actions))
	for index, action := range actions {
		if backgroundConversationAction(action) {
			continue
		}
		if startedAt[index].IsZero() {
			return nil, "the recorded transactions tag their requests but are not timed: a request has no recorded start time"
		}
		anchors = append(anchors, anchor{index: index, at: startedAt[index]})
	}
	// firstFrom is the slot before the first action that started at or after
	// at; afterLastUntil the slot after the last one that started by at.
	firstFrom := func(at time.Time) int {
		for _, a := range anchors {
			if !a.at.Before(at) {
				return a.index
			}
		}
		return len(actions)
	}
	afterLastUntil := func(at time.Time) int {
		slot := 0
		for _, a := range anchors {
			if !a.at.After(at) {
				slot = a.index + 1
			}
		}
		return slot
	}
	type marker struct {
		slot   int
		order  int
		end    bool
		action dsl.Action
	}
	markers := make([]marker, 0, 2*len(windows))
	for index, window := range windows {
		start := firstFrom(window.Start)
		markers = append(markers, marker{slot: start, order: window.StartOrder, action: dsl.TransactionMarker(window.Name, true)})
		end, order := len(actions), math.MaxInt32+len(windows)-index // open to the end: the latest opened closes first
		if !window.End.IsZero() {
			end, order = max(afterLastUntil(window.End), start), window.EndOrder
		}
		markers = append(markers, marker{slot: end, order: order, end: true, action: dsl.TransactionMarker(window.Name, false)})
	}
	sort.SliceStable(markers, func(i, j int) bool {
		if markers[i].slot != markers[j].slot {
			return markers[i].slot < markers[j].slot
		}
		if markers[i].order != markers[j].order {
			return markers[i].order < markers[j].order
		}
		return !markers[i].end && markers[j].end
	})

	out := make([]dsl.Action, 0, len(actions)+len(markers))
	positions := make([]int, len(actions))
	next := 0
	for slot := 0; slot <= len(actions); slot++ {
		first := len(out)
		for next < len(markers) && markers[next].slot == slot {
			out = append(out, markers[next].action)
			next++
		}
		// The pause recorded after the previous action now follows the ends
		// placed here, before any start.
		if slot > 0 && first < len(out) {
			ends := 0
			for _, placed := range out[first:] {
				if !transactionMarkerEnds(placed) {
					break
				}
				ends++
			}
			previous := &out[positions[slot-1]]
			if ends > 0 && previous.ThinkTimeAfter {
				last := &out[first+ends-1]
				last.ThinkTimeAfter, last.ThinkTime = true, previous.ThinkTime
				previous.ThinkTimeAfter, previous.ThinkTime = false, dsl.ThinkTimeConfig{}
			}
		}
		if slot < len(actions) {
			positions[slot] = len(out)
			out = append(out, actions[slot])
		}
	}
	script.Actions = out
	return positions, ""
}

// backgroundConversationAction reports a step of a background conversation,
// which runs alongside the steps after it rather than as one of them.
func backgroundConversationAction(action dsl.Action) bool {
	switch strings.TrimSpace(action.Type) {
	case "http_async_start", "http_async_stop":
		return true
	}
	return false
}

func transactionMarkerEnds(action dsl.Action) bool {
	function, _ := action.Params["function"].(string)
	return function == "lr_end_transaction"
}
