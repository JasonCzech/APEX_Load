package jmeter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

var jmeterTimeCall = regexp.MustCompile(`\$\{(__time|__timeShift)\(([^{}]*)\)\}`)
var jmeterEpochCall = regexp.MustCompile(`\$\{__time\((?:/1000)?[,]?\)\}`)
var jmeterUTCComposition = regexp.MustCompile(`\$\{__time\(yyyy-MM-dd,?\)\}T\$\{__time\(HH:mm:ss\.SSS,?\)\}Z`)

type timePreparation struct {
	parameters []dsl.Parameter
	reasons    map[string]string
}

// prepareTimeValues runs before general variable translation, retaining Java
// format semantics rather than substituting a full timestamp for each fragment.
func prepareTimeValues(trees []*treeNode, opts ConvertOptions) (*timePreparation, error) {
	if opts.TimeMode != "" && opts.TimeMode != "utc" && opts.TimeMode != "source" {
		return nil, fmt.Errorf("time_mode must be utc or source")
	}
	if opts.TimeMode == "source" {
		if opts.Timezone == "" {
			return nil, fmt.Errorf("timezone is required for source time mode")
		}
		if _, err := time.LoadLocation(opts.Timezone); err != nil {
			return nil, fmt.Errorf("invalid source timezone: %w", err)
		}
	}
	for from, to := range opts.VariableBindings {
		if !jmeterBindingName.MatchString(from) || (!jmeterBindingName.MatchString(to) && !jmeterSecretBinding.MatchString(to)) {
			return nil, fmt.Errorf("variable bindings must map JMeter variable names to declared names or {{secret.NAME}}")
		}
	}
	state := &timePreparation{reasons: map[string]string{}}
	parameter := func(layout, zone, reason string) string {
		name := fmt.Sprintf("jmeter_time_%d", len(state.parameters)+1)
		p := dsl.Parameter{Name: name, Type: "date_time", Update: "each_occurrence", Format: layout, Timezone: zone}
		if strings.HasPrefix(layout, "java:") {
			p.FormatDialect = "java_simple_date_format"
			p.Format = strings.TrimPrefix(layout, "java:")
		}
		state.parameters = append(state.parameters, p)
		token := "{{param." + name + "}}"
		if reason != "" {
			state.reasons[token] = reason
		}
		return token
	}
	var xmlValues func(*xmlNode)
	xmlValues = func(n *xmlNode) {
		if n == nil {
			return
		}
		// Source language is handled only by the bounded postprocessor translator.
		if n.Attrs["name"] == "script" || n.Attrs["name"] == "filename" {
			return
		}
		if n.Name == "stringProp" {
			value := n.Text
			value = jmeterVarPattern.ReplaceAllStringFunc(value, func(token string) string {
				name := strings.TrimSuffix(strings.TrimPrefix(token, "${"), "}")
				if to := opts.VariableBindings[name]; to != "" {
					if jmeterSecretBinding.MatchString(to) {
						return to
					}
					return "${" + to + "}"
				}
				return token
			})
			value = jmeterUTCComposition.ReplaceAllStringFunc(value, func(token string) string {
				if opts.TimeMode == "utc" {
					return parameter("2006-01-02T15:04:05.000Z", "UTC", "")
				}
				reason := ""
				if opts.TimeMode == "" {
					reason = "jmeter_timestamp_timezone_required"
				}
				return parameter("2006-01-02", opts.Timezone, reason) + "T" + parameter("15:04:05.000", opts.Timezone, reason) + "Z"
			})
			value = jmeterTimeCall.ReplaceAllStringFunc(value, func(token string) string {
				parts := jmeterTimeCall.FindStringSubmatch(token)
				args := strings.Split(parts[2], ",")
				if parts[1] != "__time" || len(args) > 2 || (len(args) == 2 && strings.TrimSpace(args[1]) != "") {
					return parameter("2006", "", "unsupported_jmeter_time_function")
				}
				format := strings.TrimSpace(args[0])
				if format == "" || format == "/1000" {
					return token
				}
				layout, ok := javaTimeLayout(format)
				if !ok {
					if _, err := dsl.FormatJMeterTime(time.Unix(0, 0), format); err == nil && (format == "HH:mm:ss.S" || format == "HH:mm:ss.SS") {
						layout = "java:" + format
						ok = true
					}
				}
				if !ok {
					return parameter("2006", "", "unsupported_jmeter_time_format")
				}
				reason := ""
				zone := opts.Timezone
				if strings.Contains(format, "'Z'") && opts.TimeMode == "" {
					reason = "jmeter_timestamp_timezone_required"
				}
				if opts.TimeMode == "utc" {
					zone = "UTC"
				}
				return parameter(layout, zone, reason)
			})
			remainingTime := jmeterEpochCall.ReplaceAllString(value, "")
			if strings.Contains(remainingTime, "${__time(") || strings.Contains(remainingTime, "${__timeShift(") {
				value = parameter("2006", "", "unsupported_jmeter_dynamic_time")
			}
			n.Text = value
		}
		for _, child := range n.Children {
			xmlValues(child)
		}
	}
	var walk func([]*treeNode)
	walk = func(nodes []*treeNode) {
		for _, n := range nodes {
			if n == nil || !jmeterElementEnabled(n.Element) {
				continue
			}
			xmlValues(n.Element)
			walk(n.Children)
		}
	}
	walk(trees)
	return state, nil
}

var jmeterSecretBinding = regexp.MustCompile(`^\{\{secret\.[A-Za-z_][A-Za-z0-9_]{0,127}\}\}$`)
var jmeterBindingName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// Deliberately bounded Java pattern subset. Unknown letters must never become
// literal Go layout text: that silently changes the generated request.
func javaTimeLayout(format string) (string, bool) {
	aliases := map[string]string{"YMD": "yyyyMMdd", "HMS": "HHmmss", "YMDHMS": "yyyyMMdd-HHmmss"}
	if alias, ok := aliases[format]; ok {
		format = alias
	}
	tokens := map[string]string{"yyyy": "2006", "yy": "06", "MM": "01", "dd": "02", "HH": "15", "mm": "04", "ss": "05", "SSS": "000", "XXX": "Z07:00", "XX": "Z0700", "Z": "-0700"}
	var out strings.Builder
	for i := 0; i < len(format); {
		if format[i] == '\'' {
			if i+1 < len(format) && format[i+1] == '\'' {
				out.WriteByte('\'')
				i += 2
				continue
			}
			i++
			literalStart := out.Len()
			closed := false
			for i < len(format) {
				if format[i] == '\'' {
					if i+1 < len(format) && format[i+1] == '\'' {
						out.WriteByte('\'')
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				if (format[i] >= '0' && format[i] <= '9') || format[i] > 127 {
					return "", false
				}
				out.WriteByte(format[i])
				i++
			}
			if !closed || (time.Date(2024, 8, 20, 17, 28, 39, 456000000, time.UTC).Format(out.String()[literalStart:]) != out.String()[literalStart:] || time.Date(2024, 1, 6, 8, 14, 22, 123000000, time.UTC).Format(out.String()[literalStart:]) != out.String()[literalStart:]) {
				return "", false
			}
			continue
		}
		if unicode.IsLetter(rune(format[i])) {
			j := i + 1
			for j < len(format) && format[j] == format[i] {
				j++
			}
			if format[i:j] == "SSS" && (i == 0 || (format[i-1] != '.' && format[i-1] != ',')) {
				return "", false
			}
			token, ok := tokens[format[i:j]]
			if !ok {
				return "", false
			}
			out.WriteString(token)
			i = j
			continue
		}
		if format[i] > 127 || (format[i] >= '0' && format[i] <= '9') {
			return "", false
		}
		out.WriteByte(format[i])
		i++
	}
	return out.String(), out.Len() > 0
}

func (p *timePreparation) apply(script *dsl.Script, diagnostics *diag.Collector, file string) {
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		data, _ := json.Marshal(ref.Action)
		for _, param := range p.parameters {
			token := "{{param." + param.Name + "}}"
			if !strings.Contains(string(data), token) {
				continue
			}
			exists := false
			for _, current := range script.Parameters {
				if current.Name == param.Name {
					exists = true
					break
				}
			}
			if !exists {
				script.Parameters = append(script.Parameters, param)
			}
			if reason := p.reasons[token]; reason != "" {
				gateJMeterAction(ref.Action, reason, file+"#"+ref.Path)
				diagnostics.Add(diag.Blocker, "JMeter time function", ref.Path, reason+": select an explicit time policy or replace the unsupported function")
			}
		}
		return nil
	})
}

func gateJMeterAction(action *dsl.Action, reason, source string) {
	reasons := []string{reason}
	if review, ok := action.Metadata["import_review"].(map[string]any); ok {
		switch old := review["reason_codes"].(type) {
		case []string:
			reasons = append(reasons, old...)
		case []any:
			for _, v := range old {
				if s, ok := v.(string); ok {
					reasons = append(reasons, s)
				}
			}
		}
	}
	applyJMeterSharedReviewGate(action, reasons, source)
}

func reviewSamplerChildren(nodes []*treeNode, action *dsl.Action, script *dsl.Script, diagnostics *diag.Collector, unsupported map[string]struct{}, file string, mappings *[]sourceview.Mapping, actionIndex int) {
	for childIndex, child := range nodes {
		if child == nil || child.Element == nil {
			continue
		}
		if !jmeterElementEnabled(child.Element) {
			appendJMeterDisabledMapping(mappings, file, child.Element)
			continue
		}
		if isJMeterScriptElement(child.Element.Name) {
			if child.Element.Name == "JSR223PostProcessor" && translateCSVWriter(child.Element, action, script, nodes, childIndex) {
				*mappings = append(*mappings, jmeterSourceMapping(file, child.Element, child.Element.Name, fmt.Sprintf("config.results.csv_exports[%d]", len(script.Config.Results.CSVExports)-1), jmeterElementLocation(child.Element), "export", "converted", "CSV rows are available as a per-run APEX archive download; the source filesystem path is not used."))
				diagnostics.Add(diag.Info, "JMeter CSV export", jmeterElementLocation(child.Element), "translated to a per-run CSV archive download")
			} else {
				unsupported[child.Element.Name] = struct{}{}
				gateJMeterAction(action, "unsupported_jmeter_processor", file+"#"+jmeterElementLocation(child.Element))
				diagnostics.Add(diag.Blocker, "JMeter "+child.Element.Name, jmeterElementLocation(child.Element), "enabled source processor requires an equivalent implementation before replay")
				*mappings = append(*mappings, jmeterSourceMapping(file, child.Element, child.Element.Name, fmt.Sprintf("actions[%d]", actionIndex), jmeterElementLocation(child.Element), "postprocessor", "review_only", "Source processor was not executed; replay is blocked."))
			}
			continue
		}
		if child.Element.Name == "CounterConfig" || child.Element.Name == throughputShapingTimer {
			continue // converted and reported for the whole thread group
		}
		if diagnoseJMeterElement(child.Element, diagnostics) {
			unsupported[child.Element.Name] = struct{}{}
		}
		reviewSamplerChildren(child.Children, action, script, diagnostics, unsupported, file, mappings, actionIndex)
	}
}

func reconcileJMeterReviewMappings(script *dsl.Script, mappings []sourceview.Mapping) {
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		if ref.Action.Metadata["execution_blocker"] == nil {
			return nil
		}
		for i := range mappings {
			if mappings[i].TargetPath == ref.Path && mappings[i].Status == "converted" && (mappings[i].TargetKind == "action" || mappings[i].TargetKind == "controller") {
				mappings[i].Status = "review_only"
				mappings[i].Notes = "Converted draft requires resolution of its durable execution gates."
			}
		}
		return nil
	})
}

func applyThreadGroupTermination(tg *treeNode, script *dsl.Script, diagnostics *diag.Collector, file string, mappings *[]sourceview.Mapping) {
	raw := prop(tg.Element, "LoopController.loops")
	if raw == "" {
		return
	}
	loops, err := strconv.Atoi(raw)
	if strings.EqualFold(prop(tg.Element, "LoopController.continue_forever"), "true") {
		loops = -1
		err = nil
	}
	scheduled := strings.EqualFold(prop(tg.Element, "ThreadGroup.scheduler"), "true")
	duration, durationErr := strconv.Atoi(prop(tg.Element, "ThreadGroup.duration"))
	reason := ""
	if err != nil || loops == 0 || loops < -1 || loops > dsl.MaxRunIterations {
		reason = "unsupported_jmeter_iteration_count"
	} else if loops > 0 {
		script.Config.RunLogic.Iterations = loops
	}
	if scheduled {
		if durationErr != nil || duration <= 0 {
			reason = "unsupported_jmeter_schedule"
		}
		if delay := strings.TrimSpace(prop(tg.Element, "ThreadGroup.delay")); delay != "" && delay != "0" {
			reason = "unsupported_jmeter_schedule_delay"
		}
	} else if loops > 0 {
		script.Config.RunLogic.TerminationMode = dsl.TerminationIterations
	} else if len(script.Config.SamplerSchedules) == 0 {
		reason = "jmeter_unbounded_thread_group"
	}
	if reason != "" {
		index := appendJMeterSourceReviewPlaceholder(script, "ThreadGroup", jmeterElementLocation(tg.Element), file, reason)
		*mappings = append(*mappings, jmeterSourceMapping(file, tg.Element, "ThreadGroup", fmt.Sprintf("actions[%d]", index), script.Name, "controller", "review_only", "Thread-group completion requires explicit resolution."))
		diagnostics.Add(diag.Blocker, "JMeter ThreadGroup", script.Name, reason)
	}
}

func gateJMeterMaskedValues(script *dsl.Script, diagnostics *diag.Collector, file string) {
	_ = dslwalk.WalkActions(script, func(ref dslwalk.ActionRef) error {
		for _, header := range ref.Action.HeaderList {
			value := strings.TrimSpace(header.Value)
			if len(value) >= 3 && strings.Trim(value, "*") == "" {
				gateJMeterAction(ref.Action, "jmeter_masked_source_value", file+"#"+ref.Path)
				diagnostics.Add(diag.Blocker, "JMeter masked value", ref.Path, "Source header "+header.Name+" contains a redaction placeholder; explicitly bind its source reference to a real value or {{secret.NAME}} and regenerate")
			}
		}
		return nil
	})
}
