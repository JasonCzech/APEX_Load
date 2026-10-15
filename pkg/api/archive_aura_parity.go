package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol/aura"
)

// The Aura parity report compares, per LR-style transaction, the Aura
// actions real browsers sent (the observers' protocol_evidence.aura_actions)
// with those the run's HTTP scripts send. Lightning caches some actions in
// the browser and re-batches calls, so an HTTP script recorded once can send
// more or fewer server calls than browsers do.
//
// The HTTP side is frozen when the run starts: each HTTP group's Aura
// requests (POSTs to an /aura endpoint) and the actions their bodies name,
// keyed by action name (RunArchiveAuraHTTPRequest). The report counts each
// request's executions per transaction instance (one VUser's pass through
// the transaction in one iteration) from the run's events.

const (
	maxAuraHTTPRequests          = 2000
	maxAuraParityTransactions    = 100
	maxAuraParityInstances       = 500
	maxAuraParityNames           = 200
	maxAuraHTTPActionsPerRequest = 64
	// auraParityThreshold flags an action whose HTTP count per transaction
	// differs from the browsers' median by more than this many calls. It is
	// a starting point to calibrate on a real journey.
	auraParityThreshold = 1.0
)

// RunArchiveAuraHTTPRequest is one HTTP group's Aura request, frozen at run
// start: the actions its body names, in order (an Apex call by
// namespace.Class.method, otherwise its descriptor).
type RunArchiveAuraHTTPRequest struct {
	Group   string   `json:"group"`
	Action  string   `json:"action"`
	Actions []string `json:"actions"`
}

// AuraParityTransaction is one transaction's Aura calls in browsers and in
// the HTTP scripts, per transaction instance.
type AuraParityTransaction struct {
	Transaction      string `json:"transaction"`
	BrowserInstances int    `json:"browser_instances"`
	HTTPInstances    int    `json:"http_instances"`
	// BrowserRequests and HTTPRequests are the median Aura requests per
	// instance; the per-request averages show batching and payload size.
	BrowserRequests          float64            `json:"browser_requests"`
	HTTPRequests             float64            `json:"http_requests"`
	BrowserActionsPerRequest float64            `json:"browser_actions_per_request"`
	HTTPActionsPerRequest    float64            `json:"http_actions_per_request"`
	BrowserBytesPerRequest   float64            `json:"browser_bytes_per_request"`
	HTTPBytesPerRequest      float64            `json:"http_bytes_per_request"`
	Actions                  []AuraParityAction `json:"actions"`
}

// AuraParityAction is one action's median calls per transaction instance in
// browsers and in the HTTP scripts; Flagged marks a difference above the
// threshold (an action missing from one side, or sent more often).
type AuraParityAction struct {
	Name    string  `json:"name"`
	Browser float64 `json:"browser"`
	HTTP    float64 `json:"http"`
	Flagged bool    `json:"flagged,omitempty"`
}

// declaredAuraHTTPRequests freezes the HTTP groups' Aura requests when the
// run also has a browser group to compare them with.
func declaredAuraHTTPRequests(test *ManagedTest) []RunArchiveAuraHTTPRequest {
	if test == nil {
		return nil
	}
	browser := false
	for _, group := range test.groupSpecs {
		browser = browser || group.Script != nil && strings.EqualFold(strings.TrimSpace(group.Script.Protocol), "browser")
	}
	if !browser {
		return nil
	}
	var out []RunArchiveAuraHTTPRequest
	for _, group := range test.groupSpecs {
		if group.Script == nil || strings.EqualFold(strings.TrimSpace(group.Script.Protocol), "browser") {
			continue
		}
		requests := map[string][]string{}
		ambiguous := map[string]bool{}
		var visit func(actions []dsl.Action)
		visit = func(actions []dsl.Action) {
			for _, action := range actions {
				if action.Control != nil {
					for _, branch := range action.Control.ActionBranches() {
						visit(branch.Actions)
					}
				}
				if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
					visit(auraSequenceChildren(action))
					continue
				}
				names := auraRequestActionNames(action)
				if len(names) == 0 || ambiguous[action.Name] {
					continue
				}
				if previous, seen := requests[action.Name]; seen && strings.Join(previous, "\x00") != strings.Join(names, "\x00") {
					// Two requests share a name: their executions cannot be told apart.
					delete(requests, action.Name)
					ambiguous[action.Name] = true
					continue
				}
				requests[action.Name] = names
			}
		}
		visit(group.Script.Init)
		visit(group.Script.Actions)
		visit(group.Script.Teardown)
		names := make([]string, 0, len(requests))
		for name := range requests {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if len(out) >= maxAuraHTTPRequests {
				return out
			}
			out = append(out, RunArchiveAuraHTTPRequest{Group: group.Name, Action: name, Actions: requests[name]})
		}
	}
	return out
}

// auraSequenceChildren decodes an http_sequence's requests.
func auraSequenceChildren(action dsl.Action) []dsl.Action {
	payload, err := json.Marshal(action.Params["requests"])
	if err != nil {
		return nil
	}
	var children []dsl.Action
	if json.Unmarshal(payload, &children) != nil {
		return nil
	}
	return children
}

// auraRequestActionNames names the actions of an Aura POST, or nil for any
// other request.
func auraRequestActionNames(action dsl.Action) []string {
	if action.Name == "" || !strings.EqualFold(strings.TrimSpace(action.Method), "POST") || strings.TrimSpace(action.Body) == "" {
		return nil
	}
	target := action.Target
	if parsed, err := url.Parse(target); err == nil {
		target = parsed.Path
	} else if index := strings.IndexAny(target, "?#"); index >= 0 {
		target = target[:index]
	}
	if !strings.HasSuffix(strings.TrimRight(target, "/"), "/aura") {
		return nil
	}
	actions, err := aura.RequestActions([]byte(action.Body))
	if err != nil || len(actions) == 0 {
		return nil
	}
	if len(actions) > maxAuraHTTPActionsPerRequest {
		actions = actions[:maxAuraHTTPActionsPerRequest]
	}
	names := make([]string, 0, len(actions))
	for _, request := range actions {
		names = append(names, request.Name())
	}
	return names
}

type auraParityInstanceKey struct {
	group     string
	vuser     int
	iteration int64
}

type auraParityInstance struct {
	requests int
	actions  int
	bytes    float64
	names    map[string]int
}

type auraParitySide struct {
	instances map[auraParityInstanceKey]*auraParityInstance
}

func (s *auraParitySide) instance(key auraParityInstanceKey) *auraParityInstance {
	if s.instances == nil {
		s.instances = map[auraParityInstanceKey]*auraParityInstance{}
	}
	instance := s.instances[key]
	if instance == nil {
		if len(s.instances) >= maxAuraParityInstances {
			return nil
		}
		instance = &auraParityInstance{names: map[string]int{}}
		s.instances[key] = instance
	}
	return instance
}

type archiveAuraParityAccumulator struct {
	http         map[[2]string][]string
	transactions map[string]*[2]auraParitySide
}

func newArchiveAuraParityAccumulator(requests []RunArchiveAuraHTTPRequest) *archiveAuraParityAccumulator {
	accumulator := &archiveAuraParityAccumulator{http: map[[2]string][]string{}, transactions: map[string]*[2]auraParitySide{}}
	for _, request := range requests {
		accumulator.http[[2]string{request.Group, request.Action}] = request.Actions
	}
	return accumulator
}

func (a *archiveAuraParityAccumulator) side(transaction string, browser bool) *auraParitySide {
	sides := a.transactions[transaction]
	if sides == nil {
		if len(a.transactions) >= maxAuraParityTransactions {
			return nil
		}
		sides = &[2]auraParitySide{}
		a.transactions[transaction] = sides
	}
	if browser {
		return &sides[0]
	}
	return &sides[1]
}

func (a *archiveAuraParityAccumulator) merge(tx TransactionEvent) {
	transaction := strings.TrimSpace(tx.SourceTransaction)
	if transaction == "" || len(a.http) == 0 {
		return
	}
	key := auraParityInstanceKey{group: tx.Group, vuser: tx.VUserID, iteration: tx.Iteration}
	if evidence, ok := tx.ProtocolEvidence["aura_actions"].(map[string]any); ok {
		actions, _ := evidence["actions"].([]any)
		requests := int(evidenceFloat(evidence["requests"]))
		if requests <= 0 || len(actions) == 0 {
			return
		}
		side := a.side(transaction, true)
		if side == nil {
			return
		}
		instance := side.instance(key)
		if instance == nil {
			return
		}
		instance.requests += requests
		for _, raw := range actions {
			action, _ := raw.(map[string]any)
			name, _ := action["name"].(string)
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			instance.actions++
			instance.names[name]++
			// Each of a batch's actions carries the batch's response bytes.
			if batch := evidenceFloat(action["batch_size"]); batch > 0 {
				instance.bytes += evidenceFloat(action["bytes"]) / batch
			}
		}
		return
	}
	names, ok := a.http[[2]string{tx.Group, tx.ActionName}]
	if !ok || archiveEventPopulation(tx) != "action" {
		return
	}
	side := a.side(transaction, false)
	if side == nil {
		return
	}
	instance := side.instance(key)
	if instance == nil {
		return
	}
	instance.requests++
	instance.bytes += float64(tx.BytesReceived)
	for _, name := range names {
		instance.actions++
		instance.names[name]++
	}
}

func auraParityMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return (values[middle-1] + values[middle]) / 2
}

// summary is the side's median requests per instance and its averages per
// request.
func (s *auraParitySide) summary() (requests, actionsPerRequest, bytesPerRequest float64) {
	counts := make([]float64, 0, len(s.instances))
	var totalRequests, totalActions int
	var totalBytes float64
	for _, instance := range s.instances {
		counts = append(counts, float64(instance.requests))
		totalRequests += instance.requests
		totalActions += instance.actions
		totalBytes += instance.bytes
	}
	if totalRequests == 0 {
		return auraParityMedian(counts), 0, 0
	}
	return auraParityMedian(counts), float64(totalActions) / float64(totalRequests), totalBytes / float64(totalRequests)
}

func (s *auraParitySide) median(name string) float64 {
	counts := make([]float64, 0, len(s.instances))
	for _, instance := range s.instances {
		counts = append(counts, float64(instance.names[name]))
	}
	return auraParityMedian(counts)
}

// result is each transaction both sides ran, by name, with its actions most
// different first.
func (a *archiveAuraParityAccumulator) result() []AuraParityTransaction {
	var out []AuraParityTransaction
	for transaction, sides := range a.transactions {
		browser, http := &sides[0], &sides[1]
		if len(browser.instances) == 0 || len(http.instances) == 0 {
			continue
		}
		row := AuraParityTransaction{Transaction: transaction, BrowserInstances: len(browser.instances), HTTPInstances: len(http.instances)}
		row.BrowserRequests, row.BrowserActionsPerRequest, row.BrowserBytesPerRequest = browser.summary()
		row.HTTPRequests, row.HTTPActionsPerRequest, row.HTTPBytesPerRequest = http.summary()
		names := map[string]bool{}
		for _, side := range []*auraParitySide{browser, http} {
			for _, instance := range side.instances {
				for name := range instance.names {
					names[name] = true
				}
			}
		}
		for name := range names {
			action := AuraParityAction{Name: name, Browser: browser.median(name), HTTP: http.median(name)}
			action.Flagged = math.Abs(action.HTTP-action.Browser) > auraParityThreshold
			row.Actions = append(row.Actions, action)
		}
		sort.Slice(row.Actions, func(i, j int) bool {
			left, right := math.Abs(row.Actions[i].HTTP-row.Actions[i].Browser), math.Abs(row.Actions[j].HTTP-row.Actions[j].Browser)
			if left != right {
				return left > right
			}
			return row.Actions[i].Name < row.Actions[j].Name
		})
		if len(row.Actions) > maxAuraParityNames {
			row.Actions = row.Actions[:maxAuraParityNames]
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Transaction < out[j].Transaction })
	return out
}

// auraParityTransactionText is one transaction's summary for the PDF report.
func auraParityTransactionText(row AuraParityTransaction) string {
	return fmt.Sprintf("%s: browsers %.1f requests (%.1f actions each), HTTP %.1f requests (%.1f actions each), over %d and %d instances",
		row.Transaction, row.BrowserRequests, row.BrowserActionsPerRequest, row.HTTPRequests, row.HTTPActionsPerRequest, row.BrowserInstances, row.HTTPInstances)
}
