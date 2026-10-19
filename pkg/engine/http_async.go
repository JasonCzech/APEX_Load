package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// Background HTTP conversations (LoadRunner web_reg_async_attributes):
// http_async_start runs a request repeatedly in the background while the
// script continues (poll on an interval, long_poll re-issuing as soon as a
// response arrives, push re-opening a streaming response when it ends), and
// http_async_stop ends it. A once conversation is a background request: it
// is sent one time, as a native app sends its logging and analytics calls
// without waiting for them, and ends with its response. Workers only send
// requests and queue what arrives. The virtual user applies the queue
// (metrics, extractions, then the conversation's callback branches) on its
// own goroutine before each action, as WebSocket callbacks are dispatched, so
// variables are never written concurrently.
const (
	// MaxAsyncConversations caps the repeating conversations one virtual
	// user runs.
	MaxAsyncConversations = 8
	// MaxBackgroundRequests caps the once conversations one virtual user has
	// in flight; starting another first waits for the oldest to finish, as a
	// client's connection limit holds back its next call.
	MaxBackgroundRequests = 16
	// MinAsyncInterval is the floor between two requests of a conversation.
	MinAsyncInterval = 100 * time.Millisecond
	// maxAsyncPendingItems bounds the responses and chunks a conversation
	// queues between two dispatches; the oldest are dropped and counted.
	maxAsyncPendingItems = 64
	asyncStopTimeout     = 5 * time.Second
)

// AsyncPatterns are the conversation patterns.
var AsyncPatterns = map[string]bool{"poll": true, "long_poll": true, "push": true, "once": true}

// asyncOnce is the pattern of a background request, sent one time.
const asyncOnce = "once"

// Callback branch names of http_async_start (control.branches).
const (
	AsyncOnRequest   = "on_request"
	AsyncOnResponse  = "on_response"
	AsyncOnBodyChunk = "on_body_chunk"
)

// asyncItem is one queued response or body chunk.
type asyncItem struct {
	result *protocol.Result
	chunk  string
}

type asyncConversation struct {
	// id is the conversation's registry key. A background request's is its
	// script id plus a sequence number, as the same step may send another
	// while one is still in flight; scriptID is the id the script gave.
	id         string
	scriptID   string
	pattern    string
	interval   time.Duration
	request    dsl.Action
	onRequest  []dsl.Action
	onResponse []dsl.Action
	onChunk    []dsl.Action
	cancel     context.CancelFunc
	done       chan struct{}
	// stopRequested is set when a callback stops a conversation; the stop
	// completes after the running dispatch.
	stopRequested bool

	mu       sync.Mutex
	resolved *protocol.Action
	pending  []asyncItem
	dropped  int
}

type asyncConversations struct {
	byID  map[string]*asyncConversation
	order []string
	// background holds the script ids of the background requests started
	// this iteration, so stopping one is never an error; sent counts them.
	background map[string]bool
	sent       int
}

func (c *asyncConversation) current() *protocol.Action {
	c.mu.Lock()
	defer c.mu.Unlock()
	copied := *c.resolved
	return &copied
}

func (c *asyncConversation) push(item asyncItem) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) >= maxAsyncPendingItems {
		c.pending = c.pending[1:]
		c.dropped++
	}
	c.pending = append(c.pending, item)
}

func (c *asyncConversation) take() ([]asyncItem, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pending, dropped := c.pending, c.dropped
	c.pending, c.dropped = nil, 0
	return pending, dropped
}

// asyncRequestAction decodes params.request into the conversation's request.
func asyncRequestAction(action dsl.Action) (dsl.Action, error) {
	raw, ok := action.Params["request"]
	if !ok {
		return dsl.Action{}, fmt.Errorf("http_async_start needs params.request")
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return dsl.Action{}, err
	}
	var request dsl.Action
	if err := json.Unmarshal(payload, &request); err != nil {
		return dsl.Action{}, fmt.Errorf("http_async_start params.request is not an HTTP action: %w", err)
	}
	if request.Name == "" {
		request.Name = action.Name
	}
	return request, nil
}

func asyncParamString(action dsl.Action, key string) string {
	value, _ := action.Params[key].(string)
	return strings.TrimSpace(value)
}

func asyncParamDuration(action dsl.Action, key string) time.Duration {
	switch value := action.Params[key].(type) {
	case float64:
		return time.Duration(value) * time.Millisecond
	case int:
		return time.Duration(value) * time.Millisecond
	case int64:
		return time.Duration(value) * time.Millisecond
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			return time.Duration(parsed) * time.Millisecond
		}
	}
	return 0
}

// startAsyncConversation handles http_async_start.
func (vu *VUser) startAsyncConversation(ctx context.Context, action dsl.Action) error {
	id := vu.interpolate(asyncParamString(action, "id"))
	if id == "" {
		return fmt.Errorf("http_async_start needs params.id")
	}
	pattern := strings.ToLower(asyncParamString(action, "pattern"))
	if pattern == "" {
		pattern = "poll"
	}
	if !AsyncPatterns[pattern] {
		return fmt.Errorf("http_async_start pattern %q is not poll, long_poll, push or once", pattern)
	}
	interval := asyncParamDuration(action, "interval_ms")
	if interval < MinAsyncInterval {
		interval = MinAsyncInterval
	}
	request, err := asyncRequestAction(action)
	if err != nil {
		return err
	}
	if vu.Proto == nil {
		return fmt.Errorf("http_async_start needs an HTTP protocol")
	}
	if vu.async == nil {
		vu.async = &asyncConversations{byID: map[string]*asyncConversation{}}
	}
	scriptID := id
	if pattern == asyncOnce {
		if err := vu.awaitBackgroundRequestSlot(ctx); err != nil {
			return err
		}
		vu.async.sent++
		id = fmt.Sprintf("%s#%d", scriptID, vu.async.sent)
		if vu.async.background == nil {
			vu.async.background = map[string]bool{}
		}
		vu.async.background[scriptID] = true
	}
	if _, running := vu.async.byID[id]; running {
		return fmt.Errorf("async conversation %q is already running", id)
	}
	if pattern != asyncOnce && vu.async.running(false) >= MaxAsyncConversations {
		return fmt.Errorf("a virtual user runs at most %d async conversations", MaxAsyncConversations)
	}
	conversation := &asyncConversation{id: id, scriptID: scriptID, pattern: pattern, interval: interval, request: request, done: make(chan struct{})}
	if action.Control != nil {
		for _, branch := range action.Control.Branches {
			switch branch.Name {
			case AsyncOnRequest:
				conversation.onRequest = branch.Steps
			case AsyncOnResponse:
				conversation.onResponse = branch.Steps
			case AsyncOnBodyChunk:
				conversation.onChunk = branch.Steps
			}
		}
	}
	if !vu.runAsyncCallbacks(ctx, conversation, conversation.onRequest, AsyncOnRequest) {
		return fmt.Errorf("async conversation %q on_request callback failed", id)
	}
	conversation.resolved = vu.resolveAsyncRequest(id, request)
	parent := context.Background()
	if vu.HardStopCtx != nil {
		parent = vu.HardStopCtx
	}
	workerCtx, cancel := context.WithCancel(parent)
	conversation.cancel = cancel
	vu.async.byID[id] = conversation
	vu.async.order = append(vu.async.order, id)
	firstDelay := time.Duration(0)
	if pattern == "poll" {
		// A poll's first request runs as part of the start step, so values
		// it extracts are there for the steps that follow; the worker sends
		// the next one an interval later. Long-poll and push requests are
		// held by the server, so they start in the background.
		started := time.Now()
		first := conversation.current()
		result, err := vu.Proto.Execute(ctx, first)
		if result == nil {
			result = &protocol.Result{ActionName: first.Name, StartTime: started, Duration: time.Since(started)}
		}
		if err != nil {
			result.Success = false
			result.ErrorMessage = err.Error()
		}
		conversation.push(asyncItem{result: result})
		if err := vu.applyAsyncResults(ctx, conversation); err != nil {
			cancel()
			vu.forgetAsyncConversation(id)
			return err
		}
		firstDelay = interval - time.Since(started)
	}
	go vu.runAsyncConversation(workerCtx, conversation, firstDelay)
	return nil
}

// resolveAsyncRequest resolves the conversation's request on the virtual
// user's goroutine and marks it so it never becomes the page context.
func (vu *VUser) resolveAsyncRequest(id string, request dsl.Action) *protocol.Action {
	resolved := vu.resolveAction(request)
	params := make(map[string]any, len(resolved.Params)+1)
	for key, value := range resolved.Params {
		params[key] = value
	}
	params["async_conversation"] = id
	resolved.Params = params
	return resolved
}

func (vu *VUser) runAsyncConversation(ctx context.Context, conversation *asyncConversation, firstDelay time.Duration) {
	defer close(conversation.done)
	if firstDelay > 0 {
		timer := time.NewTimer(firstDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	for {
		started := time.Now()
		chunked := false
		requestCtx := ctx
		if len(conversation.onChunk) > 0 {
			requestCtx = protocol.WithStreamChunkObserver(ctx, func(chunk string) {
				chunked = true
				conversation.push(asyncItem{chunk: chunk})
			})
		}
		result := vu.executeAsyncRequest(requestCtx, conversation)
		if ctx.Err() != nil {
			return
		}
		if len(conversation.onChunk) > 0 && !chunked && result.ResponseBody != "" {
			// A response that was not read as an event stream is one chunk.
			conversation.push(asyncItem{chunk: result.ResponseBody})
		}
		conversation.push(asyncItem{result: result})
		if conversation.pattern == asyncOnce {
			return
		}
		wait := MinAsyncInterval - time.Since(started)
		if conversation.pattern == "poll" {
			wait = conversation.interval - time.Since(started)
		}
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}

// executeAsyncRequest sends one request of the conversation. A protocol panic
// becomes a failed result instead of killing the worker silently.
func (vu *VUser) executeAsyncRequest(ctx context.Context, conversation *asyncConversation) (result *protocol.Result) {
	action := conversation.current()
	started := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			result = &protocol.Result{ActionName: action.Name, StartTime: started, Duration: time.Since(started), Success: false, ErrorMessage: fmt.Sprintf("async request panicked: %v", recovered)}
		}
	}()
	result, err := vu.Proto.Execute(ctx, action)
	if result == nil {
		result = &protocol.Result{ActionName: action.Name, StartTime: started, Duration: time.Since(started)}
	}
	if err != nil {
		result.Success = false
		result.ErrorMessage = err.Error()
	}
	return result
}

// dispatchAsyncResults applies every queued conversation item on the virtual
// user's goroutine.
func (vu *VUser) dispatchAsyncResults(ctx context.Context) error {
	if vu.async == nil || len(vu.async.byID) == 0 || vu.inAsyncDispatch {
		return nil
	}
	for _, id := range append([]string(nil), vu.async.order...) {
		conversation := vu.async.byID[id]
		if conversation == nil {
			continue
		}
		// A background request whose worker has finished has queued its
		// response; it is applied now and the conversation ends.
		finished := conversation.pattern == asyncOnce && asyncWorkerFinished(conversation)
		err := vu.applyAsyncResults(ctx, conversation)
		if finished && vu.async.byID[id] == conversation {
			vu.forgetAsyncConversation(id)
		}
		vu.finishRequestedAsyncStops()
		if err != nil {
			return err
		}
	}
	return nil
}

// running counts the conversations in flight: background requests (once)
// or the repeating ones.
func (a *asyncConversations) running(once bool) int {
	count := 0
	for _, conversation := range a.byID {
		if (conversation.pattern == asyncOnce) == once {
			count++
		}
	}
	return count
}

func asyncWorkerFinished(conversation *asyncConversation) bool {
	select {
	case <-conversation.done:
		return true
	default:
		return false
	}
}

// awaitBackgroundRequestSlot makes room for one more background request:
// while MaxBackgroundRequests are in flight, it waits for the oldest to
// finish and applies its response.
func (vu *VUser) awaitBackgroundRequestSlot(ctx context.Context) error {
	for vu.async.running(true) >= MaxBackgroundRequests {
		var oldest *asyncConversation
		for _, id := range vu.async.order {
			if conversation := vu.async.byID[id]; conversation != nil && conversation.pattern == asyncOnce {
				oldest = conversation
				break
			}
		}
		if oldest == nil {
			return nil
		}
		select {
		case <-oldest.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		err := vu.applyAsyncResults(ctx, oldest)
		vu.forgetAsyncConversation(oldest.id)
		if err != nil {
			return err
		}
	}
	return nil
}

// applyAsyncResults records each queued response under the conversation's
// own name (so it never adds to an enclosing transaction), merges its
// extractions and runs the conversation's callback branches, then resolves
// the request again with the current variables.
func (vu *VUser) applyAsyncResults(ctx context.Context, conversation *asyncConversation) error {
	outer := vu.inAsyncDispatch
	vu.inAsyncDispatch = true
	defer func() { vu.inAsyncDispatch = outer }()
	items, dropped := conversation.take()
	cfg := &vu.Script.Config
	if dropped > 0 {
		vu.makeLogger(cfg.Log).Warn("async conversation responses dropped between dispatches", "conversation", conversation.id, "dropped", dropped)
	}
	for _, item := range items {
		if conversation.stopRequested {
			break
		}
		if item.result == nil {
			vu.ensureVariables()
			vu.Variables["async_chunk"] = item.chunk
			if !vu.runAsyncCallbacks(ctx, conversation, conversation.onChunk, AsyncOnBodyChunk) {
				return fmt.Errorf("async conversation %q on_body_chunk callback failed", conversation.id)
			}
			continue
		}
		result := item.result
		if result.ProtocolEvidence == nil {
			result.ProtocolEvidence = map[string]any{}
		}
		result.ProtocolEvidence["async_conversation"] = conversation.id
		if conversation.pattern == asyncOnce {
			// A background request keeps its step's name; like every
			// conversation response it is not part of any transaction.
			result.ProtocolEvidence["background_request"] = true
		} else {
			result.ActionName = "async " + conversation.id
		}
		status := strconv.Itoa(result.StatusCode)
		success := result.Success
		body := result.ResponseBody
		vu.recordResult(result, cfg.ErrorHandling, cfg.Log)
		if vu.onAsyncResult != nil {
			vu.onAsyncResult(result)
		}
		if len(conversation.onResponse) == 0 {
			continue
		}
		vu.ensureVariables()
		vu.Variables["async_response_status"] = status
		vu.Variables["async_response_success"] = strconv.FormatBool(success)
		vu.Variables["async_response_body"] = body
		if !vu.runAsyncCallbacks(ctx, conversation, conversation.onResponse, AsyncOnResponse) {
			return fmt.Errorf("async conversation %q on_response callback failed", conversation.id)
		}
	}
	if conversation.stopRequested || len(items) == 0 || conversation.pattern == asyncOnce {
		// A background request sends nothing more, so it is not re-armed.
		return nil
	}
	if !vu.runAsyncCallbacks(ctx, conversation, conversation.onRequest, AsyncOnRequest) {
		return fmt.Errorf("async conversation %q on_request callback failed", conversation.id)
	}
	resolved := vu.resolveAsyncRequest(conversation.id, conversation.request)
	conversation.mu.Lock()
	conversation.resolved = resolved
	conversation.mu.Unlock()
	return nil
}

// runAsyncCallbacks runs one callback branch on the virtual user.
func (vu *VUser) runAsyncCallbacks(ctx context.Context, conversation *asyncConversation, steps []dsl.Action, branch string) bool {
	if len(steps) == 0 {
		return true
	}
	outer := vu.inAsyncDispatch
	vu.inAsyncDispatch = true
	defer func() { vu.inAsyncDispatch = outer }()
	cfg := &vu.Script.Config
	vu.ensureVariables()
	vu.Variables["async_conversation_id"] = conversation.id
	return vu.runLifecycleActions(ctx, steps, "async "+branch, false, vu.makeLogger(cfg.Log), cfg.Log, cfg.ErrorHandling)
}

// stopAsyncConversation handles http_async_stop: the worker stops, its last
// responses are applied, and the conversation is forgotten. Inside a
// callback the stop is recorded and completes after the callback.
func (vu *VUser) stopAsyncConversation(ctx context.Context, id string) error {
	conversation := vu.asyncConversation(id)
	if conversation == nil && vu.async != nil && vu.async.background[id] {
		return vu.finishBackgroundRequests(ctx, id)
	}
	if conversation == nil {
		return fmt.Errorf("async conversation %q is not running", id)
	}
	conversation.cancel()
	if vu.inAsyncDispatch {
		conversation.stopRequested = true
		return nil
	}
	waitAsyncWorker(conversation)
	err := vu.applyAsyncResults(ctx, conversation)
	vu.forgetAsyncConversation(id)
	vu.finishRequestedAsyncStops()
	return err
}

// finishBackgroundRequests handles http_async_stop of a background request:
// it is not cancelled (the app would not take a call back), so the stop waits
// for those still in flight under that id and applies their responses.
func (vu *VUser) finishBackgroundRequests(ctx context.Context, scriptID string) error {
	if vu.inAsyncDispatch {
		return nil
	}
	for _, key := range append([]string(nil), vu.async.order...) {
		conversation := vu.async.byID[key]
		if conversation == nil || conversation.scriptID != scriptID || conversation.pattern != asyncOnce {
			continue
		}
		select {
		case <-conversation.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		err := vu.applyAsyncResults(ctx, conversation)
		vu.forgetAsyncConversation(key)
		if err != nil {
			return err
		}
	}
	return nil
}

func (vu *VUser) asyncConversation(id string) *asyncConversation {
	if vu.async == nil {
		return nil
	}
	return vu.async.byID[id]
}

func waitAsyncWorker(conversation *asyncConversation) {
	timer := time.NewTimer(asyncStopTimeout)
	defer timer.Stop()
	select {
	case <-conversation.done:
	case <-timer.C:
	}
}

func (vu *VUser) forgetAsyncConversation(id string) {
	delete(vu.async.byID, id)
	for i, existing := range vu.async.order {
		if existing == id {
			vu.async.order = append(vu.async.order[:i], vu.async.order[i+1:]...)
			break
		}
	}
}

// finishRequestedAsyncStops completes the stops callbacks requested; what a
// stopped conversation queued after the stop is discarded.
func (vu *VUser) finishRequestedAsyncStops() {
	if vu.async == nil || vu.inAsyncDispatch {
		return
	}
	for _, id := range append([]string(nil), vu.async.order...) {
		conversation := vu.async.byID[id]
		if conversation == nil || !conversation.stopRequested {
			continue
		}
		waitAsyncWorker(conversation)
		conversation.take()
		vu.forgetAsyncConversation(id)
	}
}

// stopAllAsyncConversations stops every conversation, at iteration end or
// when the virtual user finishes, and warns about each one still running.
// Background requests still in flight are let finish (within the stop
// timeout) and applied: they were sent, and the app would not cancel them.
func (vu *VUser) stopAllAsyncConversations(ctx context.Context, why string) {
	if vu.async == nil {
		return
	}
	deadline := time.NewTimer(asyncStopTimeout)
	defer deadline.Stop()
	for _, id := range append([]string(nil), vu.async.order...) {
		conversation := vu.asyncConversation(id)
		if conversation == nil || conversation.pattern != asyncOnce {
			continue
		}
		select {
		case <-conversation.done:
		case <-deadline.C:
			// Past the shared deadline: what has not finished is cut off.
			deadline.Reset(0)
		}
		conversation.cancel()
		waitAsyncWorker(conversation)
		_ = vu.applyAsyncResults(ctx, conversation)
		vu.forgetAsyncConversation(id)
	}
	for _, id := range append([]string(nil), vu.async.order...) {
		if vu.asyncConversation(id) == nil {
			continue
		}
		vu.makeLogger(vu.Script.Config.Log).Warn("async conversation stopped: still running at "+why, "conversation", id)
		_ = vu.stopAsyncConversation(ctx, id)
	}
	vu.async.background = nil
}

// waitForAsyncCondition handles http_sync: it applies conversation results
// and WebSocket message callbacks until params.variable holds a value (or
// params.value, when given), or params.timeout_ms (default 10 s) passes.
func (vu *VUser) waitForAsyncCondition(ctx context.Context, action dsl.Action) error {
	name := strings.ToLower(asyncParamString(action, "variable"))
	if name == "" {
		return fmt.Errorf("http_sync needs params.variable")
	}
	want, hasWant := action.Params["value"].(string)
	timeout := asyncParamDuration(action, "timeout_ms")
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := vu.dispatchAsyncResults(ctx); err != nil {
			return err
		}
		if err := vu.dispatchWebSocketCallbacks(ctx, false); err != nil {
			return err
		}
		value, ok := vu.Variables[name]
		if ok && ((hasWant && value == vu.interpolate(want)) || (!hasWant && value != "")) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("http_sync timed out after %s waiting for %s", timeout, name)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
