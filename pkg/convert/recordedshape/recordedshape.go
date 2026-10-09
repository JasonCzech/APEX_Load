// Package recordedshape gives an HTTP script converted from recorded traffic
// the shape a VuGen recording has: requests a page repeats in the background
// (poll, long-poll and push) collapse into one background conversation, and
// the WebSocket traffic recorded alongside the HTTP steps becomes WebSocket
// steps in the same script, in recorded order. The HAR pipeline and the
// proxy recorder share it.
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
	minPollOccurrences  = 3
	minPollInterval     = 200 * time.Millisecond
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
}

// Conversation is a background conversation the async scan made.
type Conversation struct {
	ID          string
	Pattern     string
	Occurrences int
	IntervalMs  int
	URL         string
}

// Result reports how the script was shaped.
type Result struct {
	// NewIndex maps each original action index to its index in the shaped
	// script, or -1 when the action was folded into a conversation.
	NewIndex []int
	// Wrapped marks original actions that became http_async_start; their
	// request moved under params.request.
	Wrapped       map[int]bool
	Conversations []Conversation
	// Sockets counts the WebSocket connections placed in the script;
	// SkippedSockets the ones that overlapped an earlier connection.
	Sockets        int
	SkippedSockets int
	Warnings       []string
}

// Changed reports whether Shape changed the script.
func (r Result) Changed() bool {
	return len(r.Conversations) > 0 || r.Sockets > 0
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
		if len(sockets) > 0 || opts.AsyncScan {
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
	for i := range result.NewIndex {
		result.NewIndex[i] = -1
	}
	actions := make([]dsl.Action, 0, len(items))
	for _, it := range items {
		if it.orig >= 0 {
			result.NewIndex[it.orig] = len(actions)
		}
		actions = append(actions, it.action)
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
			name := uniqueName("ws_"+leaf.key, receive.action.Extractors)
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

func uniqueName(base string, existing []dsl.Extractor) string {
	base = strings.ToLower(nonWord.ReplaceAllString(base, "_"))
	name := base
	for n := 2; ; n++ {
		clash := false
		for _, extractor := range existing {
			if extractor.Name == name {
				clash = true
			}
		}
		if !clash {
			return name
		}
		name = fmt.Sprintf("%s_%d", base, n)
	}
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
	removed := map[int]bool{}
	type stop struct {
		before int
		action dsl.Action
	}
	var stops []stop
	for _, key := range order {
		occurrences := groups[key]
		pattern, interval, evidence := classify(items, occurrences)
		if pattern == "" {
			continue
		}
		id := fmt.Sprintf("async_%d", len(result.Conversations)+1)
		first := occurrences[0]
		request := items[first].action
		for _, other := range occurrences[1:] {
			request.Extractors = mergeExtractors(request.Extractors, items[other].action.Extractors)
		}
		start, err := startAction(id, pattern, interval, request, len(occurrences), evidence)
		if err != nil {
			continue
		}
		items[first].action = start
		result.Wrapped[items[first].orig] = true
		for _, other := range occurrences[1:] {
			removed[other] = true
		}
		last := occurrences[len(occurrences)-1]
		stopAt := len(items)
		for i := last + 1; i < len(items); i++ {
			if items[i].step != nil && items[i].step.Page && !removed[i] {
				stopAt = i
				break
			}
		}
		stops = append(stops, stop{before: stopAt, action: dsl.Action{Name: "Stop " + id, Type: "http_async_stop", Params: map[string]any{"id": id}}})
		result.Conversations = append(result.Conversations, Conversation{ID: id, Pattern: pattern, Occurrences: len(occurrences), IntervalMs: int(interval / time.Millisecond), URL: items[first].step.URL})
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
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d recorded %s requests to %s became background conversation %s", conversation.Occurrences, conversation.Pattern, conversation.URL, conversation.ID))
	}
	return shaped
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

// classify decides whether a group of recorded requests is a background
// conversation: push (a server-sent event stream), long_poll (each request
// re-issued as soon as its held response returns), or poll (a near-constant
// interval).
func classify(items []item, occurrences []int) (string, time.Duration, string) {
	for _, index := range occurrences {
		if isEventStream(items[index].step.ContentType) {
			return "push", 0, fmt.Sprintf("%d response(s) were text/event-stream", len(occurrences))
		}
	}
	if len(occurrences) < minPollOccurrences {
		return "", 0, ""
	}
	gaps := make([]time.Duration, 0, len(occurrences)-1)
	idleShort := true
	var durations []time.Duration
	for i := 0; i < len(occurrences); i++ {
		current := items[occurrences[i]]
		if current.at.IsZero() {
			return "", 0, ""
		}
		durations = append(durations, current.dur)
		if i+1 < len(occurrences) {
			next := items[occurrences[i+1]]
			gaps = append(gaps, next.at.Sub(current.at))
			if next.at.Sub(current.at.Add(current.dur)) > maxLongPollIdle {
				idleShort = false
			}
		}
	}
	if idleShort && median(durations) >= minLongPollDuration {
		return "long_poll", 0, fmt.Sprintf("%d requests, each re-issued within %s of its held response (median hold %s)", len(occurrences), maxLongPollIdle, median(durations).Round(time.Millisecond))
	}
	interval := median(gaps)
	if interval < minPollInterval {
		return "", 0, ""
	}
	tolerance := interval / 4
	if tolerance < 250*time.Millisecond {
		tolerance = 250 * time.Millisecond
	}
	for _, gap := range gaps {
		if diff := gap - interval; diff > tolerance || -diff > tolerance {
			return "", 0, ""
		}
	}
	return "poll", interval, fmt.Sprintf("%d requests about every %s", len(occurrences), interval.Round(time.Millisecond))
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
	metadata["recorded_async"] = map[string]any{"pattern": pattern, "occurrences": occurrences, "evidence": evidence}
	start := dsl.Action{Name: "Async " + request.Name, Type: "http_async_start", Params: params, Metadata: metadata, ThinkTimeAfter: thinkAfter, ThinkTime: think, Attribution: request.Attribution}
	importreview.ApplyToAction(&start, []importreview.Finding{{
		Code: AsyncConversationReasonCode, ReasonCode: AsyncConversationReasonCode, Severity: diag.Warning, Gate: importreview.GatePromotion,
		Source: "recording", Resolution: "review_async_conversation",
		Detail: fmt.Sprintf("Recorded %s became background conversation %s (%s): %s. Re-convert with the async scan off to keep the requests inline.", request.Name, id, pattern, evidence),
	}})
	return start, nil
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
