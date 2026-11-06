package jmeter

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Controllers that run each child once per pass, so they do not change how
// many samplers an iteration executes.
var jmeterSinglePassControllers = map[string]bool{
	"GenericController": true, "TransactionController": true, "CriticalSectionController": true,
}

// jmeterShapingTimer is a Throughput Shaping Timer with the requests it paces.
type jmeterShapingTimer struct {
	node     *treeNode
	label    string  // the request it paces, or the timer's own name
	rate     float64 // constant requests per second across all threads
	samplers int     // samplers in scope, each run once per iteration
	duration int     // profile length in seconds
	reason   string  // why the timer cannot set the pacing
}

// applyJMeterThroughputShaping paces a non-messaging thread group from its
// Throughput Shaping Timers (kg.apc VariableThroughputTimer). Each timer
// limits the samplers in its scope to a target rate across all threads; an
// iteration runs each of those samplers once, so a timer caps the iteration
// rate at rate/samplers and the most restrictive timer sets it, as JMeter's
// sequential loop does. That becomes the group-wide TPS pacing. Profiles that
// change rate, timers shared across thread groups, and scopes whose sampler
// count per iteration is not fixed (loops, conditions) gate the script
// instead: pacing from only some timers, or none, runs faster than the
// source, and a plan whose rate limit silently vanished is not a faithful
// import.
func applyJMeterThroughputShaping(script *dsl.Script, trees []*treeNode, tg *treeNode, diagnostics *diag.Collector, sourceMappings *[]sourceview.Mapping, sourceFileName string) {
	// A group with no actions has nothing to pace and is dropped; a review
	// placeholder here would keep it as a script with no requests.
	if script == nil || tg == nil || len(script.Actions) == 0 {
		return
	}
	var timers []jmeterShapingTimer
	ancestors, _ := messagingAncestorNodes(trees, tg)
	for _, node := range ancestors {
		if node.Element.Name == throughputShapingTimer && jmeterElementEnabled(node.Element) {
			timers = append(timers, jmeterShapingTimer{node: node, label: jmeterElementLocation(node.Element), reason: "it sits above the thread group and paces every thread group together"})
		}
	}
	// repeated records a loop or conditional controller between the thread
	// group and parent: requests under it run a varying number of times per
	// iteration, so no timer there gives a fixed iteration rate.
	var collect func(parent *treeNode, repeated bool)
	collect = func(parent *treeNode, repeated bool) {
		for _, node := range parent.Children {
			if node == nil || node.Element == nil || !jmeterElementEnabled(node.Element) || node.Element.Name == "ThreadGroup" {
				continue
			}
			if node.Element.Name == throughputShapingTimer {
				timers = append(timers, jmeterShapingTimerFor(node, parent, script.Protocol, repeated))
				continue
			}
			collect(node, repeated || jmeterVariablePassController(node.Element.Name))
		}
	}
	collect(tg, false)
	if len(timers) == 0 {
		return
	}

	var reasons []string
	var limiting *jmeterShapingTimer
	for index := range timers {
		timer := &timers[index]
		if timer.reason == "" && timer.samplers == 0 {
			continue // paces no sampler
		}
		if timer.reason != "" {
			reasons = append(reasons, fmt.Sprintf("%q: %s", timer.label, timer.reason))
			continue
		}
		if limiting == nil || timer.rate/float64(timer.samplers) < limiting.rate/float64(limiting.samplers) {
			limiting = timer
		}
	}
	status, note := "converted", "Throughput shaping profile contributed to the thread group's TPS pacing."
	switch {
	case len(reasons) > 0:
		status, note = "review_only", "Throughput shaping was not converted; the script is gated until pacing is set."
		location := jmeterElementLocation(tg.Element)
		diagnostics.Add(diag.Blocker, "JMeter "+throughputShapingTimer, location, "Throughput Shaping Timers were not converted, so without review the requests would run unpaced, faster than JMeter sends them: "+strings.Join(reasons, "; ")+". Set config.pacing to the rate you need, then remove the review step")
		if !hasJMeterReviewReason(script.Actions, jmeterShapingReviewReason) {
			index := appendJMeterSourceReviewPlaceholder(script, "Throughput Shaping Timer", location, sourceFileName, jmeterShapingReviewReason)
			*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, tg.Element, throughputShapingTimer, fmt.Sprintf("actions[%d]", index), "Throughput shaping review gate", "controller", "review_only", "Throughput shaping could not become pacing, so execution waits for an explicit pacing choice."))
		}
	case limiting == nil:
		return
	case limiting.rate/float64(limiting.samplers) > dsl.MaxPacingTPS:
		status, note = "review_only", "Throughput shaping was not converted because its rate exceeds the pacing ceiling."
		diagnostics.Add(diag.Warning, "JMeter "+throughputShapingTimer, jmeterElementLocation(tg.Element), fmt.Sprintf("Throughput Shaping Timers were not converted because %s iterations/s exceeds the %d TPS pacing ceiling", formatJMeterRate(limiting.rate/float64(limiting.samplers)), dsl.MaxPacingTPS))
	case dsl.CanonicalPacingType(script.Config.Pacing.Type) != "" && dsl.CanonicalPacingType(script.Config.Pacing.Type) != "none":
		status, note = "review_only", "Throughput shaping was not converted because the script already has pacing."
		diagnostics.Add(diag.Warning, "JMeter "+throughputShapingTimer, jmeterElementLocation(tg.Element), "Throughput Shaping Timers were not converted because the script already has "+script.Config.Pacing.Type+" pacing")
	default:
		iterations := limiting.rate / float64(limiting.samplers)
		script.Config.Pacing = dsl.PacingConfig{Type: "tps", TPS: iterations}
		setJMeterRuntimeParity(script, "throughputShapingTimer", "partial")
		diagnostics.Add(diag.Warning, "JMeter "+throughputShapingTimer, jmeterElementLocation(tg.Element), jmeterShapingNote(timers, limiting, iterations, tg.Element))
	}
	for _, timer := range timers {
		timerStatus, timerNote := status, note
		if timer.reason == "" && timer.samplers == 0 {
			timerStatus, timerNote = "ignored", "Timer paces no request this script sends."
		}
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, timer.node.Element, throughputShapingTimer, "config.pacing", jmeterElementLocation(timer.node.Element), "timer", timerStatus, timerNote))
	}
}

// jmeterShapingTimerFor reads a timer whose scope is parent's subtree (only the
// parent sampler when the timer sits under one). A timer whose scope holds no
// sampler of the script's protocol paces only requests this script does not
// send, so it is left out (samplers 0); otherwise every sampler in scope
// counts, as each takes a share of the timer's rate in JMeter.
func jmeterShapingTimerFor(node, parent *treeNode, protocol string, repeated bool) jmeterShapingTimer {
	timer := jmeterShapingTimer{node: node, label: jmeterElementLocation(node.Element)}
	onSampler := parent.Element != nil && isJMeterSamplerElement(parent.Element.Name)
	samplers, ofProtocol, fixed := 1, 1, true
	if onSampler {
		timer.label = jmeterElementLocation(parent.Element)
		if samplerProtocol, known := samplerProtocol(parent.Element.Name); !known || samplerProtocol != protocol {
			ofProtocol = 0
		}
	} else {
		samplers, ofProtocol, fixed = jmeterSamplersPerPass(parent, protocol)
	}
	if ofProtocol == 0 && fixed {
		return timer
	}
	segments, invalid := jmeterShapingProfile(node.Element)
	if invalid != "" {
		timer.reason = invalid
		return timer
	}
	timer.rate = segments[0].StartRPS
	for _, segment := range segments {
		if segment.StartRPS != timer.rate || segment.EndRPS != timer.rate {
			timer.reason = "its profile changes rate over time, and APEX paces at one constant rate"
			return timer
		}
		timer.duration += segment.DurationSeconds
	}
	switch {
	case timer.rate == 0:
		timer.reason = "its profile holds the rate at 0 requests/s, at which the plugin admits one request and then holds every other thread until another request is admitted, so the load all but stops; TPS pacing cannot reproduce that"
	case repeated:
		timer.reason = "a loop or conditional controller around it varies how many requests an iteration sends"
	case !fixed:
		timer.reason = "a loop or conditional controller in its scope varies how many requests an iteration sends"
	default:
		timer.samplers = samplers
	}
	return timer
}

// jmeterShapingReviewReason gates a script whose shaping timers could not
// become pacing.
const jmeterShapingReviewReason = "unsupported_jmeter_throughput_shaping"

// jmeterShapingProfile reads a Throughput Shaping Timer's (start rate, end
// rate, seconds) rows. A rate of 0 is valid: the plugin then admits the first
// request of a second and holds later callers until another request is
// admitted, and a warm-up row usually starts there. The reason names the first row the
// plugin itself would not run, so the operator can find it.
func jmeterShapingProfile(e *xmlNode) ([]dsl.SamplerRateSegment, string) {
	var segments []dsl.SamplerRateSegment
	for _, collection := range childProps(e, "load_profile") {
		for index, row := range collection.Children {
			number := index + 1
			if len(row.Children) != 3 {
				return nil, fmt.Sprintf("row %d of its load profile does not have a start rate, end rate and duration", number)
			}
			start, startErr := strconv.ParseFloat(strings.TrimSpace(row.Children[0].Text), 64)
			end, endErr := strconv.ParseFloat(strings.TrimSpace(row.Children[1].Text), 64)
			duration, durationErr := strconv.Atoi(strings.TrimSpace(row.Children[2].Text))
			switch {
			case startErr != nil || endErr != nil:
				return nil, fmt.Sprintf("row %d of its load profile has a rate that is not a literal number", number)
			case durationErr != nil:
				return nil, fmt.Sprintf("row %d of its load profile has a duration that is not a literal whole number of seconds", number)
			case math.IsNaN(start) || math.IsInf(start, 0) || math.IsNaN(end) || math.IsInf(end, 0) || start < 0 || end < 0:
				return nil, fmt.Sprintf("row %d of its load profile has a negative or non-finite rate", number)
			case duration <= 0:
				return nil, fmt.Sprintf("row %d of its load profile lasts %d s; each row must last at least 1 s", number, duration)
			}
			segments = append(segments, dsl.SamplerRateSegment{StartRPS: start, EndRPS: end, DurationSeconds: duration})
		}
	}
	if len(segments) == 0 {
		return nil, "its load profile has no rows"
	}
	return segments, ""
}

// jmeterVariablePassController reports a controller that may run its children
// other than once per pass: loops, conditions, interleaving and the like.
func jmeterVariablePassController(name string) bool {
	if jmeterSinglePassControllers[name] {
		return false
	}
	return strings.HasSuffix(name, "Controller") || strings.HasSuffix(name, "Control") || name == "RunTime"
}

// jmeterSamplersPerPass counts the enabled samplers under node, and those of
// protocol, reporting false when a controller may run them other than once
// per pass.
func jmeterSamplersPerPass(node *treeNode, protocol string) (int, int, bool) {
	count, ofProtocol := 0, 0
	for _, child := range node.Children {
		if child == nil || child.Element == nil || !jmeterElementEnabled(child.Element) || child.Element.Name == "ThreadGroup" {
			continue
		}
		name := child.Element.Name
		switch {
		case isJMeterSamplerElement(name):
			count++
			if samplerProtocol, known := samplerProtocol(name); known && samplerProtocol == protocol {
				ofProtocol++
			}
		case jmeterVariablePassController(name):
			return 0, 0, false
		case jmeterSinglePassControllers[name]:
			nested, nestedOfProtocol, fixed := jmeterSamplersPerPass(child, protocol)
			if !fixed {
				return 0, 0, false
			}
			count += nested
			ofProtocol += nestedOfProtocol
		}
	}
	return count, ofProtocol, true
}

func jmeterShapingNote(timers []jmeterShapingTimer, limiting *jmeterShapingTimer, iterations float64, group *xmlNode) string {
	note := fmt.Sprintf("Throughput Shaping Timers pace this thread group at %s iterations/s across all VUsers: an iteration sends each paced request once, so the most restrictive timer (%q: %s requests/s over %d request(s)) sets the rate, as JMeter's sequential loop does",
		formatJMeterRate(iterations), limiting.label, formatJMeterRate(limiting.rate), limiting.samplers)
	var faster []string
	durations := map[int]bool{}
	for index := range timers {
		timer := &timers[index]
		if timer.samplers == 0 {
			continue
		}
		durations[timer.duration] = true
		if perIteration := timer.rate / float64(timer.samplers); perIteration > iterations && !jmeterRatesEqual(perIteration, iterations) {
			faster = append(faster, fmt.Sprintf("%q %s/s", timer.label, formatJMeterRate(timer.rate)))
		}
	}
	if len(faster) > 0 {
		const listed = 6
		more := ""
		if len(faster) > listed {
			more = fmt.Sprintf(" and %d more", len(faster)-listed)
			faster = faster[:listed]
		}
		note += "; requests with higher targets run below them (" + strings.Join(faster, ", ") + more + "), so move them to their own thread groups to give each its rate"
	}
	if len(durations) == 1 && limiting.duration > 0 {
		note += jmeterShapingEndNote(limiting.duration, iterations, group)
	}
	return note
}

// jmeterShapingEndNote explains the profile end, where JMeter stops the test.
// APEX has no such stop, so the advice depends on how the group itself ends:
// an iteration-terminated scenario rejects a duration, so it gets an estimate
// of whether the iterations outlast the profile instead.
func jmeterShapingEndNote(profileSeconds int, iterations float64, group *xmlNode) string {
	note := fmt.Sprintf("; the profile lasts %d s, and JMeter stops the test when it ends, which APEX does not do", profileSeconds)
	scheduled := strings.EqualFold(prop(group, "ThreadGroup.scheduler"), "true")
	loops, loopsErr := strconv.Atoi(prop(group, "LoopController.loops"))
	forever := strings.EqualFold(prop(group, "LoopController.continue_forever"), "true")
	if scheduled || loopsErr != nil || loops <= 0 || forever {
		return note + fmt.Sprintf(", so keep the scenario duration at or below %d s to match", profileSeconds)
	}
	threads, threadsErr := strconv.Atoi(prop(group, "ThreadGroup.num_threads"))
	if threadsErr != nil || threads <= 0 || iterations <= 0 {
		return note + fmt.Sprintf(": the group ends when each VUser finishes its %d iterations, so check that they finish within %d s at %s iterations/s", loops, profileSeconds, formatJMeterRate(iterations))
	}
	total := float64(threads) * float64(loops)
	seconds := total / iterations
	if seconds <= float64(profileSeconds) {
		return note + fmt.Sprintf(": the group's %s iterations take about %s s at %s iterations/s, within the profile, so it ends on its iterations as in JMeter", formatJMeterRate(total), strconv.FormatFloat(math.Ceil(seconds), 'f', -1, 64), formatJMeterRate(iterations))
	}
	return note + fmt.Sprintf(": the group's %s iterations take about %s s at %s iterations/s, longer than the profile, so APEX sends more requests than JMeter; lower the iterations to about %d per VUser to match", formatJMeterRate(total), strconv.FormatFloat(math.Ceil(seconds), 'f', -1, 64), formatJMeterRate(iterations), max(int(math.Floor(iterations*float64(profileSeconds)/float64(threads))), 1))
}

func formatJMeterRate(rate float64) string {
	return strconv.FormatFloat(rate, 'f', -1, 64)
}

func jmeterRatesEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(math.Abs(a), math.Abs(b))
}
