package dsl

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const JMeterMessagingProfile = "jmeter-messaging-v1"

// Reserved until the complete distributed source-session contract passes
// conformance. Requiring it now prevents older generators ignoring new fields.
const FeatureJMeterMessagingV1 = "jmeter_messaging_v1"

func RequiresJMeterMessaging(s *Script) bool {
	if s == nil {
		return false
	}
	if s.Config.JMeter != nil || len(s.Config.SamplerSchedules) != 0 {
		return true
	}
	for _, p := range s.Parameters {
		if p.Scope == "source_engine" || p.ScopeID != "" || p.RandomAlgorithm != "" {
			return true
		}
	}
	if len(validateScriptStructureIssues(s)) > 0 {
		return true
	}
	stack := append(append(append([]Action(nil), s.Init...), s.Actions...), s.Teardown...)
	for len(stack) > 0 {
		a := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if len(a.PacingIDs) > 0 {
			return true
		}
		if a.Control != nil {
			for _, branch := range a.Control.ActionBranches() {
				stack = append(stack, branch.Actions...)
			}
		}
	}
	return false
}

// JMeterCompatibility records the source-engine boundary. It is executable
// configuration, not a claim that an import has passed live conformance.
type JMeterCompatibility struct {
	Profile           string `json:"profile"`
	SourcePlanID      string `json:"source_plan_id"`
	SourceGroupID     string `json:"source_group_id"`
	Timezone          string `json:"timezone,omitempty"`
	ContextConfirmed  bool   `json:"context_confirmed,omitempty"`
	ProducerLifecycle string `json:"producer_lifecycle,omitempty"`
	SampleTiming      string `json:"sample_timing,omitempty"`
	ConfigurationID   string `json:"configuration_id,omitempty"`
}

// SamplerSchedule limits starts, not completions or iterations. A schedule is
// shared by every action referencing its ID within one source engine.
type SamplerSchedule struct {
	ID       string               `json:"id"`
	Segments []SamplerRateSegment `json:"segments"`
}
type SamplerRateSegment struct {
	StartRPS        float64 `json:"start_rps"`
	EndRPS          float64 `json:"end_rps"`
	DurationSeconds int     `json:"duration_seconds"`
}

func (s SamplerSchedule) Duration() time.Duration {
	var seconds int64
	for _, row := range s.Segments {
		seconds += int64(row.DurationSeconds)
	}
	return time.Duration(seconds) * time.Second
}
func (s SamplerSchedule) PlannedSamples() float64 {
	var n float64
	for _, row := range s.Segments {
		n += (row.StartRPS + row.EndRPS) * float64(row.DurationSeconds) / 2
	}
	return n
}
func ValidateJMeterCompatibility(s *Script) []string {
	return validateJMeterCompatibility(s, true)
}
func ValidateSamplerSchedule(schedule SamplerSchedule) []string {
	return validateJMeterCompatibility(&Script{Config: ScriptConfig{SamplerSchedules: []SamplerSchedule{schedule}}}, false)
}
func validateJMeterCompatibility(s *Script, execution bool) []string {
	if s == nil {
		return nil
	}
	if issues := validateScriptStructureIssues(s); len(issues) > 0 {
		return issues
	}
	var issues []string
	blockedDraft := isBlockedJMeterMessagingDraft(s)
	c := s.Config.JMeter
	if c != nil {
		if !blockedDraft {
			issues = append(issues, "JMeter messaging compatibility is not yet available for execution; strict conformance is unverified")
		}
		if c.Profile != JMeterMessagingProfile {
			issues = append(issues, "config.jmeter.profile is unsupported")
		}
		if !validCompatibilityID(c.SourcePlanID) || !validCompatibilityID(c.SourceGroupID) {
			issues = append(issues, "config.jmeter requires bounded source identities")
		}
		if c.ProducerLifecycle != "" && c.ProducerLifecycle != "source_engine" && c.ProducerLifecycle != "per_sample" {
			issues = append(issues, "unsupported JMeter producer lifecycle")
		}
		if c.SampleTiming != "" && c.SampleTiming != "send_acknowledgement" && c.SampleTiming != "pnop_create_send_excludes_close" {
			issues = append(issues, "unsupported JMeter sample timing")
		}
		if c.ProducerLifecycle == "source_engine" && c.ConfigurationID == "" {
			issues = append(issues, "source-engine producer lifecycle requires configuration identity")
		}
		if c.ConfigurationID != "" && !validCompatibilityID(c.ConfigurationID) {
			issues = append(issues, "invalid JMeter configuration identity")
		}
		if c.Timezone != "" {
			if _, err := time.LoadLocation(c.Timezone); err != nil {
				issues = append(issues, "config.jmeter.timezone is invalid")
			}
		}
		if s.Config.RunLogic.WorkloadModel == "open" {
			issues = append(issues, "JMeter compatibility requires a fixed closed thread population")
		}
	}
	if execution && c == nil && len(s.Config.SamplerSchedules) > 0 {
		issues = append(issues, "sampler schedules require JMeter messaging compatibility")
	}
	if len(s.Config.SamplerSchedules) > 256 {
		issues = append(issues, "too many sampler schedules")
	}
	ids := map[string]bool{}
	for _, schedule := range s.Config.SamplerSchedules {
		if !validCompatibilityID(schedule.ID) || ids[schedule.ID] {
			issues = append(issues, "sampler schedule identity is invalid or duplicated")
		}
		ids[schedule.ID] = true
		if len(schedule.Segments) == 0 || len(schedule.Segments) > 1024 {
			issues = append(issues, "sampler schedule requires 1..1024 segments")
		}
		total := int64(0)
		for _, seg := range schedule.Segments {
			if seg.DurationSeconds <= 0 || seg.DurationSeconds > 604800 {
				issues = append(issues, "sampler segment duration must be 1..604800 seconds")
			}
			total += int64(seg.DurationSeconds)
			for _, r := range []float64{seg.StartRPS, seg.EndRPS} {
				if math.IsNaN(r) || math.IsInf(r, 0) || r <= 0 || r > 10000000 {
					issues = append(issues, "sampler rate must be finite and in (0,10000000]")
				}
			}
		}
		if total > 604800 {
			issues = append(issues, "sampler schedule exceeds 7 days")
		}
	}
	var walk func([]Action)
	walk = func(actions []Action) {
		for _, a := range actions {
			seen := map[string]bool{}
			for _, id := range a.PacingIDs {
				if !ids[id] || seen[id] {
					issues = append(issues, fmt.Sprintf("action %q references an unknown or repeated sampler schedule", a.Name))
				}
				seen[id] = true
			}
			if a.Control != nil {
				for _, b := range a.Control.ActionBranches() {
					walk(b.Actions)
				}
			}
		}
	}
	walk(s.Init)
	walk(s.Actions)
	walk(s.Teardown)
	for _, p := range s.Parameters {
		if p.Scope != "" && p.Scope != "vuser" && p.Scope != "source_engine" {
			issues = append(issues, "parameter scope is unsupported")
		}
		if (p.Scope == "source_engine" || p.ScopeID != "") && (c == nil || !validCompatibilityID(p.ScopeID) || (p.Type != "counter" && p.Type != "random_number")) {
			issues = append(issues, "shared parameters require a source identity and counter/random_number type")
		}
		if execution && p.RandomAlgorithm != "" && !blockedDraft {
			issues = append(issues, "Java random sequence compatibility is not yet available for execution")
		}
		if p.RandomAlgorithm != "" && (p.RandomAlgorithm != "java48" || p.Type != "random_number") {
			issues = append(issues, "parameter random algorithm is unsupported")
		}
		if p.FormatDialect != "" && p.FormatDialect != "java_simple_date_format" {
			issues = append(issues, "parameter format dialect is unsupported")
		}
		if p.FormatDialect == "java_simple_date_format" && p.Type != "date_time" {
			issues = append(issues, "JMeter date format requires date_time parameter")
		}
		if p.FormatDialect == "java_simple_date_format" {
			if _, err := FormatJMeterTime(time.Now(), p.Format); err != nil {
				issues = append(issues, "unsupported JMeter date format")
			}
		}
	}
	return issues
}
func validCompatibilityID(id string) bool {
	return id != "" && len(id) <= 256 && !strings.ContainsAny(id, "\x00\r\n")
}

// FormatJMeterTime implements the bounded SimpleDateFormat subset used by
// messaging plans. In Java 5.4.1 semantics S is a minimum-width millisecond
// number, not a one-digit fractional second.
func FormatJMeterTime(t time.Time, pattern string) (string, error) {
	aliases := map[string]string{"YMD": "yyyyMMdd", "HMS": "HHmmss", "YMDHMS": "yyyyMMdd-HHmmss"}
	if a := aliases[pattern]; a != "" {
		pattern = a
	}
	if len(pattern) == 0 || len(pattern) > 256 {
		return "", fmt.Errorf("unsupported JMeter date format")
	}
	var out strings.Builder
	layouts := map[string]string{"yyyy": "2006", "yy": "06", "MM": "01", "dd": "02", "HH": "15", "mm": "04", "ss": "05", "XXX": "Z07:00", "XX": "Z0700", "Z": "-0700"}
	for i := 0; i < len(pattern); {
		if pattern[i] == '\'' {
			if i+1 < len(pattern) && pattern[i+1] == '\'' {
				out.WriteByte('\'')
				i += 2
				continue
			}
			i++
			closed := false
			for i < len(pattern) {
				if pattern[i] == '\'' {
					if i+1 < len(pattern) && pattern[i+1] == '\'' {
						out.WriteByte('\'')
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				out.WriteByte(pattern[i])
				i++
			}
			if !closed {
				return "", fmt.Errorf("unterminated JMeter date literal")
			}
			continue
		}
		b := pattern[i]
		if b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' {
			j := i + 1
			for j < len(pattern) && pattern[j] == b {
				j++
			}
			token := pattern[i:j]
			if b == 'S' && len(token) <= 3 {
				out.WriteString(fmt.Sprintf("%0*d", len(token), t.Nanosecond()/1000000))
			} else if layout, ok := layouts[token]; ok {
				out.WriteString(t.Format(layout))
			} else {
				return "", fmt.Errorf("unsupported JMeter date token")
			}
			i = j
		} else {
			out.WriteByte(b)
			i++
		}
	}
	return out.String(), nil
}

// A blocked draft is saveable, but never executable. Removing a gate makes the
// compatibility contract fail validation; the engine also rejects this profile.
func isBlockedJMeterMessagingDraft(s *Script) bool {
	if s == nil || s.Config.JMeter == nil || s.Config.JMeter.Profile != JMeterMessagingProfile || len(s.Actions) == 0 {
		return false
	}
	blocked := true
	visitScriptActions(s, func(_ string, a Action) {
		if !hasDurableExecutionBlocker(a.Metadata) {
			blocked = false
		}
	})
	return blocked
}
