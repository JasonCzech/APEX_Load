package browser

import (
	"strings"
	"time"

	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browsercontract"
)

// The capture script reports a fill each time typing pauses (capture.js
// flushes a field's value after 350ms without input, on change, on blur and
// before a click), so typing one field with pauses records a fill per burst,
// each holding the text so far. A fill replaces the field's value, so only
// the last one decides what the field holds; the others are steps that can
// fail, and for a masked password each one replays the whole secret again.

// maxFillBurstGap is the longest pause between two fills of one burst. A
// longer one is deliberate (reading search-as-you-type results), so each
// fill keeps its own step and the requests it caused.
const maxFillBurstGap = 3 * time.Second

// fillBurst returns the timeline indexes of the fill at start and of the fills
// right after it into the same field, in the same section and frame and with
// the same masking, when there is more than one. Anything else between two
// fills (a click, a key, a marker, a navigation) ends the burst.
func fillBurst(timeline []timelineEntry, start int) []int {
	first := timeline[start].step
	if first == nil || first.Kind != StepFill {
		return nil
	}
	field, ok := fillFieldKey(*first)
	if !ok {
		return nil
	}
	section := normalizeSection(timeline[start].section)
	burst := []int{start}
	for next := start + 1; next < len(timeline); next++ {
		entry := timeline[next]
		if entry.step == nil || entry.step.Kind != StepFill || normalizeSection(entry.section) != section {
			break
		}
		key, ok := fillFieldKey(*entry.step)
		if !ok || key != field || entry.step.Masked != first.Masked || browserprotocol.IsBrowserInternalURL(entry.step.URL) {
			break
		}
		if previous := timeline[burst[len(burst)-1]].timestamp; !previous.IsZero() && entry.timestamp.Sub(previous) > maxFillBurstGap {
			break
		}
		burst = append(burst, next)
	}
	if len(burst) < 2 {
		return nil
	}
	return burst
}

// fillFieldKey identifies the field a fill targets and the page it is on: the
// element as captured (its position, id and name, not the step's best
// selector, which promoting a self-healed candidate changes), its frame, and
// the page (origin and path), so a fill that loads another page ends the
// burst. Separate inputs can share a selector (one-time-code digit boxes
// all named "otp"), and their positions keep them apart.
func fillFieldKey(step Step) (string, bool) {
	identity := ""
	if step.Element != nil {
		element := step.Element
		identity = strings.Join([]string{element.CSSPath, element.XPath, element.ID, element.Name, element.Tag}, "\x00")
	}
	if strings.Trim(identity, "\x00") == "" {
		identity = step.bestSelector()
	}
	if identity == "" {
		return "", false
	}
	// The page, not the visit: some pages rewrite their query as you type.
	page, ok := browserprotocol.WindowPageKey(step.URL)
	if !ok {
		page, _, _ = strings.Cut(step.URL, "#")
	}
	return identity + "\x00" + step.FrameSelector + "\x00" + strings.Join(step.FramePath, "\x00") + "\x00" + page, true
}

// mergedFillStep is the last fill of a burst, which holds the field's final
// value, carrying a secret or parameter mark set on any of the burst's fills.
func mergedFillStep(timeline []timelineEntry, burst []int) Step {
	merged := *timeline[burst[len(burst)-1]].step
	for _, at := range burst {
		step := timeline[at].step
		merged.MarkSecret = merged.MarkSecret || step.MarkSecret
		merged.MarkParam = merged.MarkParam || step.MarkParam
	}
	return merged
}
