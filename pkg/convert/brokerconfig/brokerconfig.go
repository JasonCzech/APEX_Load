// Package brokerconfig imports standalone messaging broker configuration
// artifacts — IBM MQ MQSC scripts and JMS JNDI .properties files — into a JMS
// script skeleton seeded with config.jms and the broker's queue/topic topology,
// so an operator can start from their real broker layout instead of hand-editing.
// The generated script is mock-mode by default; the IBM MQ runtime adapter is a
// stub, so MQSC imports are flagged review-only for execution.
package brokerconfig

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Format identifiers.
const (
	FormatMQSC = "mqsc"
	FormatJNDI = "jndi"
)

const defaultMaxDestinations = 25

// ConvertRequest is the importer input.
type ConvertRequest struct {
	Name            string
	ProjectID       string
	Data            []byte
	Format          string // "", "mqsc", or "jndi"; auto-detected when empty
	MaxDestinations int
}

// Destination is a discovered queue or topic.
type Destination struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "queue" | "topic"
}

// Stats summarizes the import.
type Stats struct {
	Format          string `json:"format"`
	Queues          int    `json:"queues"`
	Topics          int    `json:"topics"`
	ActionsEmitted  int    `json:"actions_emitted"`
	DestinationsCap int    `json:"destinations_cap"`
}

// ConvertResult is the importer output (mirrors the other importer shapes).
type ConvertResult struct {
	Script   *dsl.Script `json:"script"`
	Warnings []string    `json:"warnings,omitempty"`
	Stats    Stats       `json:"stats"`
	Summary  string      `json:"summary"`
	Format   string      `json:"format"`
}

var (
	// mqscCommandLineRe anchors on a line that begins (after trimming and
	// skipping comments) with an MQSC DEFINE/ALTER command for a known object
	// type — so a DEFINE that merely appears inside a JNDI property *value* does
	// not misclassify the file.
	mqscCommandLineRe = regexp.MustCompile(`(?i)^\s*(?:DEFINE|ALTER)\s+(?:QLOCAL|QREMOTE|QALIAS|QMODEL|TOPIC)\b`)
	// jndiKeyLineRe anchors on a JNDI property key at line start.
	jndiKeyLineRe = regexp.MustCompile(`(?i)^\s*(?:queue|topic|connectionfactory)\.`)
)

// DetectFormat infers the artifact format from its content. Detection is
// line-anchored (not a whole-file substring scan): JNDI property keys win when
// they appear at the start of a line, so a JNDI value that happens to contain
// "DEFINE TOPIC" is not misread as MQSC.
func DetectFormat(data []byte) string {
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		if jndiKeyLineRe.MatchString(line) {
			return FormatJNDI
		}
		if mqscCommandLineRe.MatchString(line) {
			return FormatMQSC
		}
	}
	return ""
}

// Convert parses a broker-config artifact into a JMS script skeleton.
func Convert(req ConvertRequest) (*ConvertResult, error) {
	if len(req.Data) == 0 {
		return nil, fmt.Errorf("empty broker-config file")
	}
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" {
		format = DetectFormat(req.Data)
	}
	maxDest := req.MaxDestinations
	if maxDest <= 0 {
		maxDest = defaultMaxDestinations
	}

	var (
		destinations []Destination
		provider     string
		protocol     string
		brokerURL    string
		messageVPN   string
		warnings     []string
	)
	switch format {
	case FormatMQSC:
		destinations, warnings = parseMQSC(req.Data)
		provider, protocol = "ibmmq", "jms-ibmmq"
		warnings = append(warnings, "IBM MQ runtime adapter is not built into APEX Load (review-only execution); the script imports in mock mode — author the produce/consume bodies and supply MQ connectivity before promoting")
	case FormatJNDI:
		destinations, brokerURL, messageVPN, warnings = parseJNDI(req.Data)
		provider, protocol = "solace", "jms-solace"
		warnings = append(warnings, "JNDI bindings do not record the JMS provider; the script defaults to jms-solace (executable via -tags jms_solace_real). Confirm the provider/broker before running")
	default:
		return nil, fmt.Errorf("unrecognized broker-config format (expected IBM MQ MQSC or JMS JNDI properties); set format explicitly")
	}

	if len(destinations) == 0 {
		return nil, fmt.Errorf("no queues or topics found in the %s artifact", format)
	}
	destinations = dedupeDestinations(destinations)

	queues, topics := 0, 0
	for _, d := range destinations {
		if d.Kind == "topic" {
			topics++
		} else {
			queues++
		}
	}

	emitted := destinations
	if len(emitted) > maxDest {
		emitted = emitted[:maxDest]
		warnings = append(warnings, fmt.Sprintf("%d destinations found; seeded the first %d as actions (raise max_destinations to import more)", len(destinations), maxDest))
	}

	// Destinations whose names differ only in characters that sanitize to '-'
	// (e.g. "A/B" vs "A%B") would otherwise collapse to one action name and merge
	// in runtime per-action metrics — disambiguate with a numeric suffix. Targets
	// keep the real wire destination, so the two remain distinct on the broker.
	names := map[string]int{}
	var actions []dsl.Action
	for _, d := range emitted {
		consumeParams := map[string]any{"destination_type": d.Kind, "timeout_ms": 1000}
		// A topic consume requires a subscription; seed a per-destination name so
		// the generated skeleton validates and is runnable.
		if d.Kind == "topic" {
			consumeParams["subscription"] = sanitizeName(d.Name) + "-sub"
		}
		actions = append(actions,
			dsl.Action{
				Name:   uniqueActionName(names, "publish-"+d.Name),
				Type:   "jms_publish",
				Target: d.Name,
				Body:   "{}",
				Params: map[string]any{"destination_type": d.Kind},
			},
			dsl.Action{
				Name:   uniqueActionName(names, "consume-"+d.Name),
				Type:   "jms_consume",
				Target: d.Name,
				Params: consumeParams,
			},
		)
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = format + "-broker-import"
	}
	script := &dsl.Script{
		Name:      sanitizeName(name),
		Version:   "1.0.0",
		Protocol:  protocol,
		ProjectID: req.ProjectID,
		Config: dsl.ScriptConfig{
			JMS: dsl.JMSConfig{
				Provider:    provider,
				BrokerURL:   brokerURL,
				MessageVPN:  messageVPN,
				Destination: destinations[0].Name,
				Mock:        true,
			},
		},
		Actions: actions,
	}

	return &ConvertResult{
		Script:   script,
		Warnings: warnings,
		Stats:    Stats{Format: format, Queues: queues, Topics: topics, ActionsEmitted: len(actions), DestinationsCap: maxDest},
		Summary:  fmt.Sprintf("Imported %d queue(s) and %d topic(s) from the %s artifact into a %s skeleton (mock mode).", queues, topics, format, protocol),
		Format:   format,
	}, nil
}

// mqscDefineRe matches `DEFINE QLOCAL('NAME')` / `DEFINE TOPIC(NAME)` etc. across
// the MQSC object types, capturing the object type and name (quoted or bare).
var mqscDefineRe = regexp.MustCompile(`(?i)\bDEFINE\s+(QLOCAL|QREMOTE|QALIAS|QMODEL|TOPIC)\s*\(\s*'?([A-Za-z0-9._%/-]+)'?\s*\)`)

func parseMQSC(data []byte) ([]Destination, []string) {
	// Join MQSC line continuations (a line ending with + or -) so a DEFINE that
	// wraps is matched as one command.
	joined := mqscJoinContinuations(string(data))
	var dests []Destination
	for _, m := range mqscDefineRe.FindAllStringSubmatch(joined, -1) {
		objType := strings.ToUpper(m[1])
		name := m[2]
		if strings.HasPrefix(strings.ToUpper(name), "SYSTEM.") {
			continue // skip internal MQ objects
		}
		kind := "queue"
		if objType == "TOPIC" {
			kind = "topic"
		}
		dests = append(dests, Destination{Name: name, Kind: kind})
	}
	return dests, nil
}

func mqscJoinContinuations(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	var buf strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		// MQSC comment: a line whose first non-blank character is '*'. Skip it
		// entirely so a commented-out DEFINE is not parsed as a live object, and
		// its trailing +/- is never treated as a continuation marker.
		if strings.HasPrefix(strings.TrimSpace(trimmed), "*") {
			continue
		}
		if strings.HasSuffix(trimmed, "+") || strings.HasSuffix(trimmed, "-") {
			buf.WriteString(strings.TrimSuffix(trimmed, trimmed[len(trimmed)-1:]))
			continue
		}
		buf.WriteString(trimmed)
		out = append(out, buf.String())
		buf.Reset()
	}
	if buf.Len() > 0 {
		out = append(out, buf.String())
	}
	return strings.Join(out, "\n")
}

func parseJNDI(data []byte) (dests []Destination, brokerURL, messageVPN string, warnings []string) {
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		key, value, ok := splitProperty(line)
		if !ok || value == "" {
			continue
		}
		lowerKey := strings.ToLower(key)
		switch {
		case strings.HasPrefix(lowerKey, "queue."):
			dests = append(dests, Destination{Name: value, Kind: "queue"})
		case strings.HasPrefix(lowerKey, "topic."):
			dests = append(dests, Destination{Name: value, Kind: "topic"})
		case strings.HasPrefix(lowerKey, "connectionfactory") && strings.Contains(value, "://"):
			// Only accept a URI-shaped value, and prefer the first one — an
			// enumeration line like `connectionFactory.names = cf1` is skipped.
			if brokerURL == "" {
				brokerURL = value
			}
		case strings.HasSuffix(lowerKey, "messagevpn") || strings.HasSuffix(lowerKey, "message-vpn"):
			// Require the property *key* to end in messageVPN, so a credential
			// line that merely mentions "messagevpn" in its value is ignored.
			if messageVPN == "" {
				messageVPN = value
			}
		}
	}
	return dests, brokerURL, messageVPN, warnings
}

// splitProperty splits a Java .properties line on the first '=' or ':' separator
// (both are legal), returning trimmed key and value.
func splitProperty(line string) (key, value string, ok bool) {
	idx := strings.IndexAny(line, "=:")
	if idx < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}

func dedupeDestinations(in []Destination) []Destination {
	seen := map[string]struct{}{}
	var out []Destination
	for _, d := range in {
		key := d.Kind + "\x00" + d.Name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// uniqueActionName sanitizes raw into a DSL action name and guarantees it is
// unique within the script by appending a numeric suffix on collision.
func uniqueActionName(seen map[string]int, raw string) string {
	base := sanitizeName(raw)
	if seen[base] == 0 {
		seen[base] = 1
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if seen[candidate] == 0 {
			seen[candidate] = 1
			seen[base]++
			return candidate
		}
	}
}

var sanitizeRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	s = sanitizeRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "broker-import"
	}
	return s
}
