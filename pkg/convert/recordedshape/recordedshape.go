// Package recordedshape gives an HTTP script converted from recorded traffic
// the shape a VuGen recording has: requests a page repeats in the background
// (poll, long-poll and push) collapse into one background conversation for
// each run of them, requests an app sends without waiting (its logging and
// analytics calls) become background requests, and the WebSocket traffic
// recorded alongside the HTTP steps becomes WebSocket steps in the same
// script, in recorded order. The HAR pipeline and the proxy recorder share it.
package recordedshape

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// AsyncConversationReasonCode is the promotion finding on a background
// conversation the async scan made: it shows the evidence, and the tester
// can re-convert with the scan off to keep the requests inline.
const AsyncConversationReasonCode = "recorded_async_conversation"

const (
	minPollOccurrences = 3
	minPollInterval    = 200 * time.Millisecond
	// maxPollInterval bounds what counts as a poll: a request repeated
	// further apart is more likely the user's own step than a timer's.
	maxPollInterval     = 30 * time.Second
	maxLongPollIdle     = 300 * time.Millisecond
	minLongPollDuration = time.Second
	thinkTimeThreshold  = 500 * time.Millisecond
	receiveTimeoutMs    = 10000
	minCorrelationValue = 4
	maxCorrelationValue = 400
)

// Step is the recorded timing of one generated action.
type Step struct {
	StartedAt time.Time
	Duration  time.Duration
	// URL is the absolute URL as recorded.
	URL    string
	Method string
	// ContentType is the response's Content-Type.
	ContentType string
	// Page marks a main document (an HTML page the user navigated to).
	Page bool
}

// Frame is one recorded WebSocket message.
type Frame struct {
	Send    bool
	Payload string
	// Base64 marks a binary payload held as base64.
	Base64 bool
	At     time.Time
}

// Socket is one recorded WebSocket connection.
type Socket struct {
	URL       string
	Headers   map[string]string
	StartedAt time.Time
	EndedAt   time.Time
	Frames    []Frame
}

// Options selects the shaping.
type Options struct {
	// AsyncScan collapses recorded background requests into conversations.
	AsyncScan bool
	// Background returns the background request pattern a recorded URL
	// matches, or "": a matching request is sent as a background request
	// (http_async_start pattern once), outside transaction times.
	Background func(rawURL string) string
}

// Conversation is a background conversation the async scan made.
type Conversation struct {
	ID          string
	Pattern     string
	Occurrences int
	IntervalMs  int
	URL         string
	// Transaction is the recorded transaction its first request was tagged
	// with; the conversation outlives it and no longer belongs to it.
	Transaction string
}

// Result reports how the script was shaped.
type Result struct {
	// NewIndex maps each original action index to its index in the shaped
	// script, or -1 when the action was folded into a conversation.
	NewIndex []int
	// StartedAt is when each action of the shaped script started in the
	// recording: a conversation at its first request, a WebSocket step at its
	// frame. A conversation's http_async_stop has no recorded time (zero).
	StartedAt []time.Time
	// Wrapped marks original actions that became http_async_start; their
	// request moved under params.request.
	Wrapped       map[int]bool
	Conversations []Conversation
	// Sockets counts the WebSocket connections placed in the script;
	// SkippedSockets the ones that overlapped an earlier connection.
	Sockets        int
	SkippedSockets int
	// BackgroundRequests counts the requests sent as background requests.
	BackgroundRequests int
	Warnings           []string
}

// Changed reports whether Shape changed the script.
func (r Result) Changed() bool {
	return len(r.Conversations) > 0 || r.Sockets > 0 || r.BackgroundRequests > 0
}

type item struct {
	action dsl.Action
	at     time.Time
	dur    time.Duration
	step   *Step
	orig   int
	socket bool
}

// Shape reshapes script.Actions in place. steps must hold one entry per
// action; otherwise the script is left as it is.
func Shape(script *dsl.Script, steps []Step, sockets []Socket, opts Options) Result {
	result := Result{Wrapped: map[int]bool{}}
	if script == nil {
		return result
	}
	result.NewIndex = make([]int, len(script.Actions))
	for i := range result.NewIndex {
		result.NewIndex[i] = i
	}
	if len(steps) != len(script.Actions) {
		if len(sockets) > 0 || opts.AsyncScan || opts.Background != nil {
			result.Warnings = append(result.Warnings, "recorded timing does not line up with the generated steps; background requests and WebSocket traffic were left as recorded")
		}
		return result
	}
	items := make([]item, len(script.Actions))
	for i := range script.Actions {
		step := steps[i]
		items[i] = item{action: script.Actions[i], at: step.StartedAt, dur: step.Duration, step: &steps[i], orig: i}
	}
	items = placeSockets(items, sockets, &result)
	if opts.AsyncScan {
		items = collapseConversations(items, &result)
	}
	if opts.Background != nil {
		sendInBackground(items, opts.Background, &result)
	}
	for i := range result.NewIndex {
		result.NewIndex[i] = -1
	}
	actions := make([]dsl.Action, 0, len(items))
	result.StartedAt = make([]time.Time, 0, len(items))
	for _, it := range items {
		if it.orig >= 0 {
			result.NewIndex[it.orig] = len(actions)
		}
		actions = append(actions, it.action)
		result.StartedAt = append(result.StartedAt, it.at)
	}
	script.Actions = actions
	return result
}

var actionPathPattern = regexp.MustCompile(`^actions\[(\d+)\](.*)$`)

// RemapPath rewrites an "actions[i]..." path of the original script for the
// shaped one. It reports false for a path into a folded action.
func (r Result) RemapPath(path string) (string, bool) {
	match := actionPathPattern.FindStringSubmatch(path)
	if match == nil {
		return path, true
	}
	index, err := strconv.Atoi(match[1])
	if err != nil || index >= len(r.NewIndex) {
		return path, true
	}
	next := r.NewIndex[index]
	if next < 0 {
		return path, false
	}
	rest := match[2]
	if r.Wrapped[index] && rest != "" {
		rest = ".params.request" + rest
	}
	return fmt.Sprintf("actions[%d]%s", next, rest), true
}

// placeSockets inserts each socket's steps among the HTTP steps by time. APEX
// runs one socket per virtual user, so a socket that opens while another is
// open is skipped.
func placeSockets(items []item, sockets []Socket, result *Result) []item {
	if len(sockets) == 0 {
		return items
	}
	ordered := append([]Socket(nil), sockets...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].StartedAt.Before(ordered[j].StartedAt) })
	var socketItems []item
	var openUntil time.Time
	for index, socket := range ordered {
		end := socket.EndedAt
		for _, frame := range socket.Frames {
			if frame.At.After(end) {
				end = frame.At
			}
		}
		if !openUntil.IsZero() && socket.StartedAt.Before(openUntil) {
			result.SkippedSockets++
			continue
		}
		openUntil = end
		result.Sockets++
		socketItems = append(socketItems, socketSteps(socket, index+1, end)...)
	}
	if result.SkippedSockets > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d WebSocket connection(s) opened while another was open and were not converted; APEX runs one WebSocket per virtual user", result.SkippedSockets))
	}
	correlateReceivedFrames(items, socketItems)
	merged := make([]item, 0, len(items)+len(socketItems))
	next := 0
	for _, it := range items {
		for next < len(socketItems) && !socketItems[next].at.After(it.at) {
			merged = append(merged, socketItems[next])
			next++
		}
		merged = append(merged, it)
	}
	merged = append(merged, socketItems[next:]...)
	retimeAroundSockets(merged)
	return merged
}

func socketSteps(socket Socket, number int, end time.Time) []item {
	label := fmt.Sprintf("WS %d", number)
	connect := dsl.Action{Name: label + " Connect", Type: "ws_connect", Target: socket.URL}
	for name, value := range socket.Headers {
		if keepHandshakeHeader(name) {
			if connect.Headers == nil {
				connect.Headers = map[string]string{}
			}
			connect.Headers[name] = value
		}
	}
	items := []item{{action: connect, at: socket.StartedAt, orig: -1, socket: true}}
	sends, receives := 0, 0
	for _, frame := range socket.Frames {
		var action dsl.Action
		if frame.Send {
			sends++
			action = dsl.Action{Name: fmt.Sprintf("%s Send %d", label, sends), Type: "ws_send", Body: frame.Payload, Params: map[string]any{"type": "text"}}
		} else {
			receives++
			action = dsl.Action{Name: fmt.Sprintf("%s Receive %d", label, receives), Type: "ws_receive", Params: map[string]any{"timeout_ms": receiveTimeoutMs}}
		}
		if frame.Base64 {
			if frame.Send {
				action.BodyEncoding = "base64"
				action.Params["type"] = "binary"
			}
		}
		items = append(items, item{action: action, at: frame.At, orig: -1, socket: true, step: &Step{ContentType: frameContentType(frame)}})
		if !frame.Send && !frame.Base64 {
			items[len(items)-1].action.Body = frame.Payload
		}
	}
	items = append(items, item{action: dsl.Action{Name: label + " Close", Type: "ws_close"}, at: end, orig: -1, socket: true})
	return items
}

func frameContentType(frame Frame) string {
	if frame.Base64 {
		return "binary"
	}
	return "text"
}

// keepHandshakeHeader keeps the handshake headers a replay should send; the
// rest are generated by the client (key, version, extensions) or come from
// the cookie jar.
func keepHandshakeHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "origin", "user-agent", "accept-language", "authorization":
		return true
	}
	return false
}

// retimeAroundSockets splits the recorded gap around inserted WebSocket
// steps: the step before a socket step thinks until the socket step, and the
// last socket step before an HTTP step thinks until it.
func retimeAroundSockets(items []item) {
	for i := 0; i+1 < len(items); i++ {
		current, next := &items[i], items[i+1]
		if !current.socket && !next.socket {
			continue
		}
		if current.at.IsZero() || next.at.IsZero() {
			continue
		}
		gap := next.at.Sub(current.at.Add(current.dur))
		if gap > thinkTimeThreshold {
			current.action.ThinkTimeAfter = true
			current.action.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: float64(gap / time.Millisecond)}
		} else if !current.socket {
			current.action.ThinkTimeAfter = false
			current.action.ThinkTime = dsl.ThinkTimeConfig{}
		}
	}
}

// correlateReceivedFrames turns string values of received JSON frames that
// later steps send into message extractors, and the later uses into
// variable references.
func correlateReceivedFrames(items []item, socketItems []item) {
	used := map[string]bool{}
	// Variable names are unique across the script's frames: a later frame's
	// value must not overwrite an earlier one that steps still send.
	names := map[string]bool{}
	for index := range socketItems {
		receive := &socketItems[index]
		if receive.action.Type != "ws_receive" || receive.action.Body == "" {
			continue
		}
		var payload any
		if err := json.Unmarshal([]byte(receive.action.Body), &payload); err != nil {
			continue
		}
		for _, leaf := range jsonStringLeaves(payload, "$") {
			value := leaf.value
			if len(value) < minCorrelationValue || len(value) > maxCorrelationValue || used[value] {
				continue
			}
			later := laterUses(items, socketItems, receive.at, index, value)
			if len(later) == 0 {
				continue
			}
			name := uniqueName("ws_"+leaf.key, names)
			names[name] = true
			receive.action.Extractors = append(receive.action.Extractors, dsl.Extractor{Name: name, Source: "message", Type: "jsonpath", Expression: leaf.path, OnMiss: "fail"})
			reference := "{{var." + name + "}}"
			for _, use := range later {
				replaceInAction(use, value, reference)
			}
			used[value] = true
		}
	}
	for index := range socketItems {
		// The recorded frame was kept only for correlation; ws_receive
		// waits for the next message, whatever it says.
		if socketItems[index].action.Type == "ws_receive" {
			socketItems[index].action.Body = ""
		}
	}
}

type jsonLeaf struct{ path, key, value string }

func jsonStringLeaves(value any, path string) []jsonLeaf {
	var leaves []jsonLeaf
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := path + "." + key
			if !simpleJSONKey.MatchString(key) {
				child = path + "[" + strconv.Quote(key) + "]"
			}
			for _, leaf := range jsonStringLeaves(typed[key], child) {
				if leaf.key == "" {
					leaf.key = key
				}
				leaves = append(leaves, leaf)
			}
		}
	case []any:
		for index, element := range typed {
			leaves = append(leaves, jsonStringLeaves(element, fmt.Sprintf("%s[%d]", path, index))...)
		}
	case string:
		leaves = append(leaves, jsonLeaf{path: path, value: typed})
	}
	return leaves
}

var simpleJSONKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// laterUses returns the actions after the frame that send value.
func laterUses(items []item, socketItems []item, at time.Time, frameIndex int, value string) []*dsl.Action {
	var uses []*dsl.Action
	for i := range items {
		if items[i].at.After(at) && actionSends(items[i].action, value) {
			uses = append(uses, &items[i].action)
		}
	}
	for i := frameIndex + 1; i < len(socketItems); i++ {
		if socketItems[i].action.Type == "ws_send" && socketItems[i].action.BodyEncoding == "" && strings.Contains(socketItems[i].action.Body, value) {
			uses = append(uses, &socketItems[i].action)
		}
	}
	return uses
}

func actionSends(action dsl.Action, value string) bool {
	if strings.Contains(action.Target, value) || (action.BodyEncoding == "" && strings.Contains(action.Body, value)) {
		return true
	}
	for _, header := range action.Headers {
		if strings.Contains(header, value) {
			return true
		}
	}
	for _, header := range action.HeaderList {
		if strings.Contains(header.Value, value) {
			return true
		}
	}
	return false
}

func replaceInAction(action *dsl.Action, value, reference string) {
	action.Target = replaceValue(action.Target, value, reference, true)
	if action.BodyEncoding == "" {
		action.Body = strings.ReplaceAll(action.Body, value, reference)
	}
	for name, header := range action.Headers {
		action.Headers[name] = strings.ReplaceAll(header, value, reference)
	}
	for i := range action.HeaderList {
		action.HeaderList[i].Value = strings.ReplaceAll(action.HeaderList[i].Value, value, reference)
	}
}

// replaceValue replaces value in a target, also in its query-escaped form.
func replaceValue(target, value, reference string, escaped bool) string {
	target = strings.ReplaceAll(target, value, reference)
	if escaped {
		if encoded := url.QueryEscape(value); encoded != value {
			target = strings.ReplaceAll(target, encoded, reference)
		}
	}
	return target
}

func uniqueName(base string, taken map[string]bool) string {
	base = strings.ToLower(nonWord.ReplaceAllString(base, "_"))
	name := base
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("%s_%d", base, n)
	}
	return name
}

var nonWord = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// collapseConversations folds requests a page repeated in the background
// into one http_async_start at the first occurrence, with an
// http_async_stop where the page changes.
func collapseConversations(items []item, result *Result) []item {
	groups := map[string][]int{}
	var order []string
	for i, it := range items {
		if it.orig < 0 || it.step == nil || !isPlainRequest(it.action) {
			continue
		}
		key := conversationKey(it)
		if key == "" {
			continue
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}
	// A request repeated in separate bursts is a conversation for each burst,
	// as VuGen records it: each starts and stops where its burst did.
	var runs []conversationRun
	for _, key := range order {
		runs = append(runs, conversationRuns(items, groups[key])...)
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].occurrences[0] < runs[j].occurrences[0] })
	removed := map[int]bool{}
	for _, run := range runs {
		for _, other := range run.occurrences[1:] {
			removed[other] = true
		}
	}
	type stop struct {
		before int
		action dsl.Action
	}
	var stops []stop
	var started []conversationRun
	for _, run := range runs {
		occurrences := run.occurrences
		id := fmt.Sprintf("async_%d", len(result.Conversations)+1)
		first := occurrences[0]
		request := items[first].action
		for _, other := range occurrences[1:] {
			request.Extractors = mergeExtractors(request.Extractors, items[other].action.Extractors)
		}
		start, err := startAction(id, run.pattern, run.interval, request, len(occurrences), run.evidence)
		if err != nil {
			for _, other := range occurrences[1:] {
				delete(removed, other)
			}
			continue
		}
		items[first].action = start
		result.Wrapped[items[first].orig] = true
		started = append(started, run)
		result.Conversations = append(result.Conversations, Conversation{ID: id, Pattern: run.pattern, Occurrences: len(occurrences), IntervalMs: int(run.interval / time.Millisecond), URL: items[first].step.URL, Transaction: recordedTransaction(request.Metadata)})
	}
	// Stops are placed once every folded request is known.
	for index, run := range started {
		id := result.Conversations[index].ID
		stops = append(stops, stop{before: conversationStop(items, run, removed), action: dsl.Action{Name: "Stop " + id, Type: "http_async_stop", Params: map[string]any{"id": id}}})
	}
	if len(result.Conversations) == 0 {
		return items
	}
	shaped := make([]item, 0, len(items)+len(stops))
	lastKept := -1
	for i, it := range items {
		for _, s := range stops {
			if s.before == i {
				shaped = append(shaped, item{action: s.action, orig: -1})
			}
		}
		if removed[i] {
			// The page waited while the request ran in the background; the
			// step before it keeps the recorded gap.
			if lastKept >= 0 {
				extendThinkTime(&shaped[lastKept].action, it)
			}
			continue
		}
		shaped = append(shaped, it)
		lastKept = len(shaped) - 1
	}
	for _, s := range stops {
		if s.before == len(items) {
			shaped = append(shaped, item{action: s.action, orig: -1})
		}
	}
	for _, conversation := range result.Conversations {
		warning := fmt.Sprintf("%d recorded %s requests to %s became background conversation %s", conversation.Occurrences, conversation.Pattern, conversation.URL, conversation.ID)
		if conversation.Transaction != "" {
			warning += fmt.Sprintf(", which is no longer part of transaction %q because it outlives it", conversation.Transaction)
		}
		result.Warnings = append(result.Warnings, warning)
	}
	return shaped
}

// conversationStop is where a conversation's http_async_stop goes: before
// the first step recorded after the request would have been sent again (a
// poll's next interval, a long-poll's re-issue), or before the next page,
// whichever comes first; at the end when the recording ended with it running.
func conversationStop(items []item, run conversationRun, removed map[int]bool) int {
	last := items[run.occurrences[len(run.occurrences)-1]]
	var next time.Time
	switch run.pattern {
	case "poll":
		next = last.at.Add(run.interval + pollTolerance(run.interval))
	case "long_poll":
		next = last.at.Add(last.dur + maxLongPollIdle)
	}
	for i := run.occurrences[len(run.occurrences)-1] + 1; i < len(items); i++ {
		if removed[i] {
			continue
		}
		if items[i].step != nil && items[i].step.Page {
			return i
		}
		if !next.IsZero() && !items[i].at.IsZero() && !items[i].at.Before(next) {
			return i
		}
	}
	return len(items)
}

// sendInBackground turns each recorded request whose URL matches a
// background request pattern into a background request: it is sent without
// holding up the steps after it and is not part of any transaction, as the
// app sent it.
func sendInBackground(items []item, match func(string) string, result *Result) {
	counts := map[string]int{}
	var patterns []string
	for i := range items {
		it := &items[i]
		if it.orig < 0 || it.step == nil || !isPlainRequest(it.action) {
			continue
		}
		pattern := match(it.step.URL)
		if pattern == "" {
			continue
		}
		start, err := backgroundStart(fmt.Sprintf("background_%d", result.BackgroundRequests+1), it.action, pattern)
		if err != nil {
			continue
		}
		it.action = start
		result.Wrapped[it.orig] = true
		result.BackgroundRequests++
		if counts[pattern] == 0 {
			patterns = append(patterns, pattern)
		}
		counts[pattern]++
	}
	for _, pattern := range patterns {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d recorded request(s) matching background request pattern %q are sent as background requests, outside transaction times", counts[pattern], pattern))
	}
}

func isPlainRequest(action dsl.Action) bool {
	if action.Type != "request" || action.Control != nil {
		return false
	}
	if _, html := action.Params["html_step"]; html {
		return false
	}
	return action.MaxRedirects == nil || *action.MaxRedirects != 0
}

// conversationKey groups requests by method, host, path and query keys;
// query values (cache-busters, sequence numbers) may change. Pages never
// collapse unless they stream.
func conversationKey(it item) string {
	if it.step.Page && !isEventStream(it.step.ContentType) {
		return ""
	}
	parsed, err := url.Parse(it.step.URL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	keys := make([]string, 0)
	for key := range parsed.Query() {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.ToUpper(it.step.Method) + " " + strings.ToLower(parsed.Host) + parsed.Path + "?" + strings.Join(keys, "&") + "|" + it.action.Body
}

func isEventStream(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "text/event-stream")
}

// conversationRun is one run of a recorded request that became a background
// conversation.
type conversationRun struct {
	occurrences []int
	pattern     string
	interval    time.Duration
	evidence    string
}

// conversationRuns finds the runs of one request that are background
// conversations: push (a server-sent event stream), long_poll (each request
// re-issued as soon as its held response returns) or poll (a near-constant
// interval). Occurrences outside every run stay as recorded steps.
func conversationRuns(items []item, occurrences []int) []conversationRun {
	for _, index := range occurrences {
		if isEventStream(items[index].step.ContentType) {
			return []conversationRun{{occurrences: occurrences, pattern: "push", evidence: fmt.Sprintf("%d response(s) were text/event-stream", len(occurrences))}}
		}
	}
	for _, index := range occurrences {
		if items[index].at.IsZero() {
			return nil
		}
	}
	var runs []conversationRun
	for start := 0; start+minPollOccurrences <= len(occurrences); {
		if end, hold := longPollRun(items, occurrences, start); end >= 0 {
			runs = append(runs, conversationRun{occurrences: occurrences[start : end+1], pattern: "long_poll",
				evidence: fmt.Sprintf("%d requests, each re-issued within %s of its held response (median hold %s)", end-start+1, maxLongPollIdle, hold.Round(time.Millisecond))})
			start = end + 1
			continue
		}
		if end, interval := pollRun(items, occurrences, start); end >= 0 {
			runs = append(runs, conversationRun{occurrences: occurrences[start : end+1], pattern: "poll", interval: interval,
				evidence: fmt.Sprintf("%d requests about every %s", end-start+1, interval.Round(time.Millisecond))})
			start = end + 1
			continue
		}
		start++
	}
	return runs
}

// longPollRun returns the last occurrence of the long-poll run beginning at
// start, and its median hold, or -1 when none begins there.
func longPollRun(items []item, occurrences []int, start int) (int, time.Duration) {
	end := start
	for end+1 < len(occurrences) {
		current, next := items[occurrences[end]], items[occurrences[end+1]]
		if next.at.Sub(current.at.Add(current.dur)) > maxLongPollIdle {
			break
		}
		end++
	}
	if end-start+1 < minPollOccurrences {
		return -1, 0
	}
	durations := make([]time.Duration, 0, end-start+1)
	for _, index := range occurrences[start : end+1] {
		durations = append(durations, items[index].dur)
	}
	if hold := median(durations); hold >= minLongPollDuration {
		return end, hold
	}
	return -1, 0
}

// pollRun returns the last occurrence of the poll run beginning at start
// (each gap within the tolerance of the run's median gap so far) and its
// interval, or -1 when none begins there.
func pollRun(items []item, occurrences []int, start int) (int, time.Duration) {
	gap := func(i int) time.Duration { return items[occurrences[i+1]].at.Sub(items[occurrences[i]].at) }
	if start+1 >= len(occurrences) || gap(start) < minPollInterval || gap(start) > maxPollInterval {
		return -1, 0
	}
	gaps := []time.Duration{gap(start)}
	end := start + 1
	for end+1 < len(occurrences) {
		interval, next := median(gaps), gap(end)
		if diff := next - interval; diff > pollTolerance(interval) || -diff > pollTolerance(interval) {
			break
		}
		gaps = append(gaps, next)
		end++
	}
	interval := median(gaps)
	if end-start+1 < minPollOccurrences || interval < minPollInterval || interval > maxPollInterval {
		return -1, 0
	}
	return end, interval
}

// pollTolerance is how far a poll's gap may stray from its interval.
func pollTolerance(interval time.Duration) time.Duration {
	return max(interval/4, 250*time.Millisecond)
}

func median(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

func mergeExtractors(into, from []dsl.Extractor) []dsl.Extractor {
	for _, extractor := range from {
		seen := false
		for _, existing := range into {
			if existing.Name == extractor.Name {
				seen = true
			}
		}
		if !seen {
			into = append(into, extractor)
		}
	}
	return into
}

func startAction(id, pattern string, interval time.Duration, request dsl.Action, occurrences int, evidence string) (dsl.Action, error) {
	start, err := asyncStart(id, pattern, interval, request)
	if err != nil {
		return dsl.Action{}, err
	}
	start.Name = "Async " + request.Name
	start.Metadata["recorded_async"] = map[string]any{"pattern": pattern, "occurrences": occurrences, "evidence": evidence}
	importreview.ApplyToAction(&start, []importreview.Finding{{
		Code: AsyncConversationReasonCode, ReasonCode: AsyncConversationReasonCode, Severity: diag.Warning, Gate: importreview.GatePromotion,
		Source: "recording", Resolution: "review_async_conversation",
		Detail: fmt.Sprintf("Recorded %s became background conversation %s (%s): %s. Re-convert with the async scan off to keep the requests inline.", request.Name, id, pattern, evidence),
	}})
	return start, nil
}

// backgroundStart sends a recorded request as a background request. The
// pattern it matched comes from the recording profile, so it needs no review.
func backgroundStart(id string, request dsl.Action, pattern string) (dsl.Action, error) {
	start, err := asyncStart(id, "once", 0, request)
	if err != nil {
		return dsl.Action{}, err
	}
	start.Name = "Background " + request.Name
	start.Metadata["recorded_background"] = map[string]any{"pattern": pattern}
	return start, nil
}

// asyncStart wraps a recorded request in an http_async_start. The request's
// pause after it stays on the start step.
func asyncStart(id, pattern string, interval time.Duration, request dsl.Action) (dsl.Action, error) {
	thinkAfter, think := request.ThinkTimeAfter, request.ThinkTime
	request.ThinkTimeAfter, request.ThinkTime = false, dsl.ThinkTimeConfig{}
	payload, err := json.Marshal(request)
	if err != nil {
		return dsl.Action{}, err
	}
	var encoded map[string]any
	if err := json.Unmarshal(payload, &encoded); err != nil {
		return dsl.Action{}, err
	}
	params := map[string]any{"id": id, "pattern": pattern, "request": encoded}
	if pattern == "poll" {
		params["interval_ms"] = int(interval / time.Millisecond)
	}
	metadata := map[string]any{}
	for key, value := range request.Metadata {
		metadata[key] = value
	}
	// A background conversation keeps running after the transaction its first
	// request started in has ended, and a background request is not waited
	// for, so neither belongs to that transaction.
	for _, key := range transactionKeys {
		delete(metadata, key)
		if requestMetadata, ok := encoded["metadata"].(map[string]any); ok {
			delete(requestMetadata, key)
		}
	}
	return dsl.Action{Type: "http_async_start", Params: params, Metadata: metadata, ThinkTimeAfter: thinkAfter, ThinkTime: think, Attribution: request.Attribution}, nil
}

// transactionKeys are the metadata keys naming a step's transaction (the
// engine still reads the legacy one).
var transactionKeys = []string{"source_transaction", "loadrunner_transaction"}

// recordedTransaction is the transaction a recorded step was tagged with.
func recordedTransaction(metadata map[string]any) string {
	for _, key := range transactionKeys {
		if name, _ := metadata[key].(string); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

// extendThinkTime adds a folded request's time and think time to the step
// before it, so the page still waits as long as it did when recorded.
func extendThinkTime(action *dsl.Action, folded item) {
	extra := float64(folded.dur / time.Millisecond)
	if folded.action.ThinkTimeAfter {
		extra += folded.action.ThinkTime.MeanMs
	}
	if extra <= 0 {
		return
	}
	if !action.ThinkTimeAfter {
		action.ThinkTimeAfter = true
		action.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed"}
	}
	action.ThinkTime.MeanMs += extra
}

// WebSocketURL turns a recorded handshake URL into a ws:// or wss:// URL.
func WebSocketURL(raw string) string {
	switch {
	case strings.HasPrefix(raw, "https://"):
		return "wss://" + strings.TrimPrefix(raw, "https://")
	case strings.HasPrefix(raw, "http://"):
		return "ws://" + strings.TrimPrefix(raw, "http://")
	}
	return raw
}

// IsPage reports whether a recorded request loaded a page the user
// navigated to (an HTML document), rather than a script's background
// request.
func IsPage(contentType string, requestHeaders map[string]string) bool {
	if !strings.Contains(strings.ToLower(contentType), "text/html") {
		return false
	}
	for name, value := range requestHeaders {
		switch strings.ToLower(name) {
		case "x-requested-with":
			return false
		case "sec-fetch-dest":
			if !strings.EqualFold(value, "document") && !strings.EqualFold(value, "iframe") {
				return false
			}
		}
	}
	return true
}
