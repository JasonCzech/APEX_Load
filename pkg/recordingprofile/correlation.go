package recordingprofile

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
)

// UserRuleIDPrefix marks correlations a project user rule produced.
const UserRuleIDPrefix = "user."

// boundaryConfigPrefix is the HTTP runtime's encoded boundary expression
// (left, right, case_insensitive) used when a boundary is case-insensitive.
const boundaryConfigPrefix = "__apexload_boundary_config__:"

// ApplyCorrelation applies a profile's correlation settings to what the
// automatic scan detected over a recording's observations: it drops the
// correlations the settings exclude and adds the user rules' finds. A user
// rule's value replaces an automatic correlation of the same value.
func (s CorrelationSettings) ApplyCorrelation(observations []correlate.Observation, detected []correlate.Correlation) []correlate.Correlation {
	user := s.userRuleCorrelations(observations)
	claimed := map[string]bool{}
	for _, correlation := range user {
		claimed[correlation.Value] = true
	}
	var out []correlate.Correlation
	if !s.Disabled {
		families := ruleFamilies()
		byID := observationsByID(observations)
		for _, correlation := range detected {
			if claimed[correlation.Value] || s.excludes(correlation, families, byID) {
				continue
			}
			out = append(out, correlation)
		}
	}
	return append(out, user...)
}

func (s CorrelationSettings) minLength() int {
	if s.MinValueLength > 0 {
		return s.MinValueLength
	}
	return DefaultMinValueLength
}

func (s CorrelationSettings) maxLength() int {
	if s.MaxValueLength > 0 {
		return s.MaxValueLength
	}
	return DefaultMaxValueLength
}

func (s CorrelationSettings) excludes(correlation correlate.Correlation, families map[string]string, byID map[int]correlate.Observation) bool {
	for _, rule := range s.DisabledRules {
		rule = strings.ToLower(strings.TrimSpace(rule))
		if rule == strings.ToLower(correlation.RuleID) || rule == families[correlation.RuleID] {
			return true
		}
	}
	for _, value := range s.ExcludedValues {
		if value == correlation.Value {
			return true
		}
	}
	if length := len(correlation.Value); length < s.minLength() || length > s.maxLength() {
		return true
	}
	if source, ok := byID[correlation.FoundInID]; ok && contentTypeListed(headerValue(source.ResponseHeaders, "Content-Type"), s.ExcludedContentTypes) {
		return true
	}
	return false
}

// userRuleCorrelations runs the user rules over every response and keeps
// the values a later request sends.
func (s CorrelationSettings) userRuleCorrelations(observations []correlate.Observation) []correlate.Correlation {
	var out []correlate.Correlation
	seen := map[string]bool{}
	for _, rule := range s.UserRules {
		if rule.Disabled {
			continue
		}
		matcher, extractType, expression := s.compile(rule)
		if matcher == nil {
			continue
		}
		for index, observation := range observations {
			if !ruleApplies(rule, observation) {
				continue
			}
			match := matcher.FindStringSubmatch(observation.ResponseBody)
			if len(match) < 2 {
				continue
			}
			value := match[1]
			if value == "" || seen[value] || len(value) < s.minLength() || len(value) > s.maxLength() {
				continue
			}
			var used []int
			for _, later := range observations[index+1:] {
				if correlate.ObservationContains(later, value) {
					used = append(used, later.ID)
				}
			}
			if len(used) == 0 {
				continue
			}
			seen[value] = true
			out = append(out, correlate.Correlation{
				Name: rule.Name, Source: "body", ExtractType: extractType, Expression: expression,
				FoundInID: observation.ID, UsedInIDs: used, Value: value, SampleValue: value,
				RuleID: UserRuleIDPrefix + rule.Name, Confidence: 1,
				Reasons: []string{"project correlation rule " + rule.Name},
			})
			break
		}
	}
	return out
}

// compile returns the rule's matcher (value in group 1) and the extractor
// type and expression replay uses.
func (s CorrelationSettings) compile(rule UserRule) (*regexp.Regexp, string, string) {
	flags := ""
	if s.CaseInsensitive {
		flags = "(?i)"
	}
	switch rule.Type {
	case "regex":
		matcher, err := regexp.Compile(flags + rule.Regex)
		if err != nil || matcher.NumSubexp() < 1 {
			return nil, "", ""
		}
		return matcher, "regex", flags + rule.Regex
	case "boundary":
		if rule.Left == "" || rule.Right == "" {
			return nil, "", ""
		}
		pattern := flags + regexp.QuoteMeta(rule.Left) + "(.+?)" + regexp.QuoteMeta(rule.Right)
		matcher, err := regexp.Compile(pattern)
		if err != nil {
			return nil, "", ""
		}
		if s.PreferRegex {
			return matcher, "regex", pattern
		}
		if s.CaseInsensitive {
			config, _ := json.Marshal(map[string]any{"left": rule.Left, "right": rule.Right, "case_insensitive": true})
			return matcher, "boundary", boundaryConfigPrefix + string(config)
		}
		return matcher, "boundary", rule.Left + "|||" + rule.Right
	}
	return nil, "", ""
}

func ruleApplies(rule UserRule, observation correlate.Observation) bool {
	if len(rule.Hosts) > 0 {
		parsed, err := url.Parse(observation.RequestURL)
		if err != nil {
			return false
		}
		matched := false
		for _, host := range rule.Hosts {
			matched = matched || hostMatches(strings.ToLower(parsed.Hostname()), host)
		}
		if !matched {
			return false
		}
	}
	if len(rule.ContentTypes) > 0 && !contentTypeListed(headerValue(observation.ResponseHeaders, "Content-Type"), rule.ContentTypes) {
		return false
	}
	return true
}

func ruleFamilies() map[string]string {
	families := map[string]string{}
	for _, rule := range correlate.RuleCatalog() {
		families[rule.ID] = string(rule.Family)
	}
	return families
}

func observationsByID(observations []correlate.Observation) map[int]correlate.Observation {
	byID := make(map[int]correlate.Observation, len(observations))
	for _, observation := range observations {
		byID[observation.ID] = observation
	}
	return byID
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}
