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
// count per iteration is not fixed (loops, conditions) are reported instead:
// pacing from only some timers could run faster than the source.
func applyJMeterThroughputShaping(script *dsl.Script, trees []*treeNode, tg *treeNode, diagnostics *diag.Collector, sourceMappings *[]sourceview.Mapping, sourceFileName string) {
	if script == nil || tg == nil {
		return
	}
	plan := ""
	if script.Metadata != nil && script.Metadata.Import != nil {
		plan, _ = script.Metadata.Import.Preserved["source_plan_id"].(string)
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
				timers = append(timers, jmeterShapingTimerFor(node, parent, plan, script.Protocol, repeated))
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
		status, note = "review_only", "Throughput shaping was not converted; see the import warning."
		diagnostics.Add(diag.Warning, "JMeter "+throughputShapingTimer, jmeterElementLocation(tg.Element), "Throughput Shaping Timers were not converted, so requests run unpaced: "+strings.Join(reasons, "; "))
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
		diagnostics.Add(diag.Warning, "JMeter "+throughputShapingTimer, jmeterElementLocation(tg.Element), jmeterShapingNote(timers, limiting, iterations))
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
func jmeterShapingTimerFor(node, parent *treeNode, plan, protocol string, repeated bool) jmeterShapingTimer {
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
	schedule, err := messagingSchedule(node.Element, plan)
	if err != nil || len(schedule.Segments) == 0 {
		timer.reason = "its load profile is empty or invalid"
		return timer
	}
	timer.rate = schedule.Segments[0].StartRPS
	for _, segment := range schedule.Segments {
		if segment.StartRPS != timer.rate || segment.EndRPS != timer.rate {
			timer.reason = "its profile changes rate over time, and APEX paces at one constant rate"
			return timer
		}
		timer.duration += segment.DurationSeconds
	}
	switch {
	case repeated:
		timer.reason = "a loop or conditional controller around it varies how many requests an iteration sends"
	case !fixed:
		timer.reason = "a loop or conditional controller in its scope varies how many requests an iteration sends"
	default:
		timer.samplers = samplers
	}
	return timer
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

func jmeterShapingNote(timers []jmeterShapingTimer, limiting *jmeterShapingTimer, iterations float64) string {
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
		note += fmt.Sprintf("; the profile lasts %d s, so set the scenario duration to match", limiting.duration)
	}
	return note
}

func formatJMeterRate(rate float64) string {
	return strconv.FormatFloat(rate, 'f', -1, 64)
}

func jmeterRatesEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-9*math.Max(math.Abs(a), math.Abs(b))
}
