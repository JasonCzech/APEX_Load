package jmeter

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	jmeterGlobalCounterReason = "unsupported_jmeter_global_counter"
	// maxJMeterCounterPadWidth bounds zero-padding formats far below the DSL's
	// generated-value limit; a wider format is reported, not converted.
	maxJMeterCounterPadWidth = 64
	// defaultJMeterSharedCounterBlock is the unique-number block per VUser for
	// a shared counter whose range or thread count does not size one.
	defaultJMeterSharedCounterBlock = 1_000_000
)

// jmeterCounterScope records how a converted counter reproduces JMeter.
type jmeterCounterScope int

const (
	// jmeterCounterPerUser is a per-thread counter: the native per-VUser
	// counter reproduces it exactly.
	jmeterCounterPerUser jmeterCounterScope = iota
	// jmeterCounterUniqueBlocks is a counter shared across threads, mapped to
	// unique-number blocks: values stay distinct across VUsers, but each VUser
	// draws from its own block instead of JMeter's single global sequence.
	jmeterCounterUniqueBlocks
	// jmeterCounterSharedUnmapped is a shared counter whose increment unique
	// blocks cannot express (they step by 1); it keeps the cross-thread gate.
	jmeterCounterSharedUnmapped
)

// jmeterCounterConversion is a Counter element mapped onto a native parameter.
type jmeterCounterConversion struct {
	parameter dsl.Parameter
	scope     jmeterCounterScope
	// note explains a unique-block mapping to the operator.
	note string
}

// jmeterScopedCounter is a Counter config element in scope of a thread group.
// inLoop records a looping controller between the group and the counter: JMeter
// then advances the counter once per loop pass, not once per iteration.
type jmeterScopedCounter struct {
	node   *treeNode
	inLoop bool
}

// jmeterThreadGroupCounters returns the enabled Counter elements that apply to
// tg: those on its ancestors and those anywhere below it, sampler-scoped ones
// included. Nested thread groups convert as their own scripts.
func jmeterThreadGroupCounters(trees []*treeNode, tg *treeNode) []jmeterScopedCounter {
	var counters []jmeterScopedCounter
	ancestors, _ := messagingAncestorNodes(trees, tg)
	for _, node := range ancestors {
		if node.Element.Name == "CounterConfig" && jmeterElementEnabled(node.Element) {
			counters = append(counters, jmeterScopedCounter{node: node})
		}
	}
	var scan func(nodes []*treeNode, inLoop bool)
	scan = func(nodes []*treeNode, inLoop bool) {
		for _, node := range nodes {
			if node == nil || node.Element == nil || !jmeterElementEnabled(node.Element) || node.Element.Name == "ThreadGroup" {
				continue
			}
			switch node.Element.Name {
			case "CounterConfig":
				counters = append(counters, jmeterScopedCounter{node: node, inLoop: inLoop})
			case "LoopController", "WhileController", "ForeachController", "ForEachController":
				scan(node.Children, true)
				continue
			}
			scan(node.Children, inLoop)
		}
	}
	scan(tg.Children, false)
	return counters
}

// jmeterCounterParameter maps a Counter element onto the native counter. A
// counter JMeter shares across threads becomes a unique-number counter whose
// blocks split the source range across the plan's threads (threads <= 0 when
// the count is not a literal).
func jmeterCounterParameter(e *xmlNode, threads int) (jmeterCounterConversion, error) {
	p := dsl.Parameter{Name: prop(e, "CounterConfig.name"), Type: "counter", Update: "each_iteration", OnExhaustion: "wrap"}
	conversion := jmeterCounterConversion{parameter: p}
	if p.Name == "" {
		return conversion, errors.New("the counter has no reference name")
	}
	number := func(key string, fallback int) (int, error) {
		text := strings.TrimSpace(prop(e, key))
		if text == "" {
			return fallback, nil
		}
		return strconv.Atoi(text)
	}
	var err error
	if p.Start, err = number("CounterConfig.start", 0); err != nil {
		return conversion, errors.New("the start value is not a literal integer")
	}
	if p.Step, err = number("CounterConfig.incr", 0); err != nil || p.Step <= 0 {
		return conversion, errors.New("the increment is not a positive literal integer")
	}
	if p.End, err = number("CounterConfig.end", 0); err != nil {
		return conversion, errors.New("the maximum is not a literal integer")
	}
	bounded := strings.TrimSpace(prop(e, "CounterConfig.end")) != ""
	if bounded {
		switch {
		case p.End < p.Start:
			return conversion, errors.New("the maximum is below the start value")
		case p.End == 0:
			return conversion, errors.New("a maximum of 0 cannot be represented (the native counter reads it as unbounded)")
		}
	}
	if format := prop(e, "CounterConfig.format"); format != "" {
		if strings.Trim(format, "0") != "" || len(format) > maxJMeterCounterPadWidth {
			return conversion, fmt.Errorf("the number format %.40q is not zero padding of at most %d digits", format, maxJMeterCounterPadWidth)
		}
		p.PadWidth, p.PadChar = len(format), "0"
	}
	perUser := prop(e, "CounterConfig.per_user") == "true"
	// JMeter honours the reset only for per-user counters.
	if perUser && prop(e, "CounterConfig.reset_on_tg_iteration") == "true" {
		return conversion, errors.New("resetting on each thread group iteration has no native equivalent")
	}
	conversion.parameter = p
	switch {
	case perUser:
		conversion.scope = jmeterCounterPerUser
	case p.Step != 1:
		conversion.scope = jmeterCounterSharedUnmapped
	default:
		block := jmeterSharedCounterBlockSize(p.Start, p.End, bounded, threads)
		conversion.scope = jmeterCounterUniqueBlocks
		conversion.parameter.Selection, conversion.parameter.BlockSize, conversion.parameter.End = "unique", block, 0
		conversion.note = fmt.Sprintf("shared counter %s became a unique-number counter: each VUser draws from its own block of %d values (VUser n starts at %d + n × %d), so values stay distinct across VUsers but are not issued in JMeter's global order", p.Name, block, p.Start, block)
		if bounded {
			conversion.note += fmt.Sprintf("; the source range %d..%d holds %d blocks, so VUsers beyond that go past the source maximum", p.Start, p.End, jmeterCounterBlocksInRange(p.Start, p.End, block))
		}
	}
	return conversion, nil
}

// jmeterSharedCounterBlockSize sizes a VUser's unique-number block: the default
// count, reduced when needed so the source range holds a block for each of the
// plan's threads (or one block when the thread count is not a literal). The
// range then fits at least the plan's threads, and more VUsers when it is wide.
func jmeterSharedCounterBlockSize(start, end int, bounded bool, threads int) int {
	block := uint64(defaultJMeterSharedCounterBlock)
	if bounded {
		limit := jmeterCounterSpan(start, end)
		if threads > 0 {
			limit /= uint64(threads)
		}
		if limit < block {
			block = limit
		}
	}
	if block < 1 {
		block = 1
	}
	return int(block)
}

// jmeterCounterSpan counts the values in start..end (end >= start); unsigned
// arithmetic keeps the whole int range.
func jmeterCounterSpan(start, end int) uint64 {
	span := uint64(end) - uint64(start) + 1
	if span == 0 {
		return math.MaxUint64 // every int
	}
	return span
}

func jmeterCounterBlocksInRange(start, end, block int) uint64 {
	return jmeterCounterSpan(start, end) / uint64(block)
}

// attachJMeterCounterParameters declares the Counter elements in scope of a
// non-messaging thread group as native counter parameters; messaging groups
// convert counters through their contributor path. A name converts only when
// every counter declaring it in scope converts with the same settings: JMeter
// scopes same-named counters to their controllers, which one parameter cannot
// express, so requests using a conflicting name stay gated. A shared counter
// becomes a unique-number counter; one whose increment blocks cannot express
// carries the cross-thread gate once a request uses it.
func attachJMeterCounterParameters(script *dsl.Script, counters []jmeterScopedCounter, threads int, diagnostics *diag.Collector, sourceMappings *[]sourceview.Mapping, sourceFileName string) {
	if script == nil || len(counters) == 0 {
		return
	}
	type declaration struct {
		counter    jmeterScopedCounter
		conversion jmeterCounterConversion
		err        error
	}
	existing := make(map[string]bool, len(script.Parameters))
	for _, parameter := range script.Parameters {
		existing[strings.ToLower(parameter.Name)] = true
	}
	var order []string
	byName := make(map[string][]declaration)
	for _, counter := range counters {
		conversion, err := jmeterCounterParameter(counter.node.Element, threads)
		key := strings.ToLower(conversion.parameter.Name)
		if _, seen := byName[key]; !seen {
			order = append(order, key)
		}
		byName[key] = append(byName[key], declaration{counter: counter, conversion: conversion, err: err})
	}
	unconverted := func(d declaration, reason, note string) {
		location := jmeterElementLocation(d.counter.node.Element)
		diagnostics.Add(diag.Warning, "JMeter CounterConfig", location, "counter was not converted because "+reason)
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, d.counter.node.Element, "CounterConfig", "", location, "parameter", "review_only", note))
	}
	unmapped := make(map[string]bool)
	for _, key := range order {
		declarations := byName[key]
		first := declarations[0].conversion
		reason, note := "", ""
		for _, d := range declarations {
			switch {
			case d.err != nil && reason == "":
				reason, note = d.err.Error()+"; requests that use it stay gated until the variable is declared", "Counter settings have no native counter equivalent."
			case d.err == nil && (!reflect.DeepEqual(d.conversion.parameter, first.parameter) || d.conversion.scope != first.scope) && reason == "":
				reason, note = "counters in different scopes declare "+d.conversion.parameter.Name+" with different settings, which one native parameter cannot hold; requests that use it stay gated until the variable is declared", "Same-named counters with different settings cannot share one parameter."
			}
		}
		if reason == "" && existing[key] {
			reason, note = "the script already declares a parameter named "+first.parameter.Name+", which requests use instead", "A parameter with this name already exists."
		}
		if reason != "" {
			for _, d := range declarations {
				unconverted(d, reason, note)
			}
			continue
		}
		index := len(script.Parameters)
		for position, d := range declarations {
			location := jmeterElementLocation(d.counter.node.Element)
			if d.counter.inLoop {
				diagnostics.Add(diag.Warning, "JMeter CounterConfig", location, "counter sits inside a looping controller; JMeter advances it once per loop pass, the converted counter once per script iteration")
			}
			status, mappingNote := "converted", "Counter was converted into a native counter parameter."
			if first.scope == jmeterCounterUniqueBlocks {
				mappingNote = "Shared counter was converted into a unique-number counter parameter."
			}
			if position > 0 {
				status, mappingNote = "contributor", "Counter repeats an identical counter declared in another scope."
			} else if first.note != "" {
				diagnostics.Add(diag.Warning, "JMeter CounterConfig", location, first.note)
			}
			*sourceMappings = append(*sourceMappings, jmeterSourceMapping(sourceFileName, d.counter.node.Element, "CounterConfig", fmt.Sprintf("parameters[%d]", index), first.parameter.Name, "parameter", status, mappingNote))
		}
		script.Parameters = append(script.Parameters, first.parameter)
		switch first.scope {
		case jmeterCounterUniqueBlocks:
			setJMeterRuntimeParity(script, "counterConfig.shared", "partial")
		case jmeterCounterSharedUnmapped:
			unmapped[key] = true
		}
	}
	for name := range unmapped {
		if jmeterScriptReferencesVariable(script, name) {
			gateJMeterGlobalCounter(script, "shared Counter", "counterConfig.shared", "a Counter shared across threads with an increment other than 1 cannot be reproduced by unique-number blocks; make it per-user or use an increment of 1 before replay", "CounterConfig per_user=false", diagnostics, sourceMappings, sourceFileName)
			break
		}
	}
}

// jmeterScriptReferencesVariable reports whether any action uses the variable
// (case-insensitively, as the readiness check matches parameters).
func jmeterScriptReferencesVariable(script *dsl.Script, lowerName string) bool {
	encoded, err := json.Marshal([][]dsl.Action{script.Init, script.Actions, script.Teardown})
	if err != nil {
		return true
	}
	text := strings.ToLower(string(encoded))
	return strings.Contains(text, "{{var."+lowerName+"}}") || strings.Contains(text, "{{param."+lowerName+"}}")
}

// gateJMeterGlobalCounter adds the script's single cross-thread counter gate.
func gateJMeterGlobalCounter(script *dsl.Script, element, parityKey, detail, function string, diagnostics *diag.Collector, sourceMappings *[]sourceview.Mapping, sourceFileName string) {
	if hasJMeterReviewReason(script.Actions, jmeterGlobalCounterReason) {
		return
	}
	setJMeterRuntimeParity(script, parityKey, "review_required")
	location := convertutil.FirstNonEmpty(sourceFileName, "JMX plan")
	actionIndex := appendJMeterSourceReviewPlaceholder(script, element, location, sourceFileName, jmeterGlobalCounterReason)
	if diagnostics != nil {
		diagnostics.Add(diag.Blocker, "JMeter "+element, location, detail)
	}
	if sourceMappings != nil {
		*sourceMappings = append(*sourceMappings, jmeterSourceMapping(
			sourceFileName,
			nil,
			function,
			fmt.Sprintf("actions[%d]", actionIndex),
			"Cross-thread counter review gate",
			"dynamic_function",
			"review_only",
			"JMeter cross-thread counter scope has no equivalent in the per-VUser parameter runtime.",
		))
	}
}
