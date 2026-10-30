package sample

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/clienttimestamps"
	"github.com/jasonczech/apexload/pkg/convert/recordedshape"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

// shapeRecordedHAR gives a converted HAR the shape of its recording (see
// pkg/convert/recordedshape): background requests collapse into
// conversations and recorded WebSocket traffic becomes WebSocket steps. The
// script must hold one action per exchange. Source mappings are re-pointed
// at the shaped actions; a shaped script that does not validate is dropped
// in favour of the original.
//
// It also returns where each original action went (nil when the script was
// not reshaped; -1 for an action folded into a conversation) and when each
// action of the reshaped script started in the recording.
func shapeRecordedHAR(script *dsl.Script, exchanges []exchange, document *HARDocument, asyncScan bool, background []string, mappings []sourceview.Mapping) ([]sourceview.Mapping, []int, []time.Time, []string) {
	if script == nil || len(script.Actions) != len(exchanges) {
		return mappings, nil, nil, nil
	}
	sockets := document.webSockets()
	matchesBackground := false
	for _, ex := range exchanges {
		if recordingprofile.MatchBackgroundRequest(background, ex.URL) != "" {
			matchesBackground = true
			break
		}
	}
	if !asyncScan && len(sockets) == 0 && !matchesBackground {
		return mappings, nil, nil, nil
	}
	steps := make([]recordedshape.Step, len(exchanges))
	for i, ex := range exchanges {
		if !ex.HasStartedAt {
			return mappings, nil, nil, nil
		}
		contentType := ""
		if ex.Response != nil {
			contentType = responseHeaderValue(ex.Response, "Content-Type")
		}
		steps[i] = recordedshape.Step{
			StartedAt: ex.StartedAt, Duration: time.Duration(ex.DurationMs * float64(time.Millisecond)),
			URL: ex.URL, Method: ex.Method, ContentType: contentType,
			Page: recordedshape.IsPage(contentType, ex.Headers),
		}
	}
	var warnings []string
	original := cloneScriptActions(script.Actions)
	options := recordedshape.Options{AsyncScan: asyncScan}
	if matchesBackground {
		options.Background = func(rawURL string) string { return recordingprofile.MatchBackgroundRequest(background, rawURL) }
	}
	shaped := recordedshape.Shape(script, steps, sockets, options)
	if !shaped.Changed() {
		return mappings, nil, nil, append(warnings, shaped.Warnings...)
	}
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		script.Actions = original
		return mappings, nil, nil, append(warnings, "the recording's background requests and WebSocket traffic were left as recorded because the reshaped script did not validate: "+strings.Join(issues, "; "))
	}
	remapped := make([]sourceview.Mapping, 0, len(mappings))
	for _, mapping := range mappings {
		if path, ok := shaped.RemapPath(mapping.TargetPath); ok {
			mapping.TargetPath = path
			remapped = append(remapped, mapping)
		}
	}
	return remapped, shaped.NewIndex, shaped.StartedAt, append(warnings, shaped.Warnings...)
}

// applyRecordedClientTimestamps gives the epoch timestamps a recorded client
// made up (cache-busters, poll times) a fresh value at replay. It runs
// before the async scan: a timestamp is found by changing on every request,
// which folding a poll's repeats would hide.
func applyRecordedClientTimestamps(script *dsl.Script, document *HARDocument, content string) string {
	evidence, err := document.ClientTimestamps()
	if err != nil {
		evidence, err = clienttimestamps.FromHAR([]byte(content))
	}
	if err != nil {
		return ""
	}
	return clienttimestamps.Apply(script, evidence).Warning()
}

func cloneScriptActions(actions []dsl.Action) []dsl.Action {
	return append([]dsl.Action(nil), actions...)
}

// harWebSockets reads the WebSocket connections a browser HAR recorded
// (Chrome and Firefox keep their frames in _webSocketMessages).
func harWebSockets(content string) []recordedshape.Socket {
	document, err := ParseHARDocument(content)
	if err != nil {
		return nil
	}
	return document.webSockets()
}

func (d *HARDocument) webSockets() []recordedshape.Socket {
	items := d.items
	var sockets []recordedshape.Socket
	for _, item := range items {
		if !isHARWebSocketUpgrade(item) {
			continue
		}
		requestData, _ := item["request"].(map[string]any)
		started, ok := parseHARStartedAt(item["startedDateTime"])
		if !ok {
			continue
		}
		socket := recordedshape.Socket{URL: recordedshape.WebSocketURL(stringValue(requestData["url"])), StartedAt: started, EndedAt: started, Headers: map[string]string{}}
		if headers, ok := requestData["headers"].([]any); ok {
			for _, raw := range headers {
				header, _ := raw.(map[string]any)
				if name := stringValue(header["name"]); name != "" && !strings.HasPrefix(name, ":") {
					socket.Headers[name] = stringValue(header["value"])
				}
			}
		}
		messages, _ := item["_webSocketMessages"].([]any)
		for _, raw := range messages {
			message, _ := raw.(map[string]any)
			opcode := intValue(message["opcode"])
			if opcode != 1 && opcode != 2 {
				continue
			}
			at := harEpochSeconds(message["time"])
			if at.IsZero() {
				continue
			}
			socket.Frames = append(socket.Frames, recordedshape.Frame{
				Send:    strings.EqualFold(stringValue(message["type"]), "send"),
				Payload: stringValue(message["data"]),
				Base64:  opcode == 2,
				At:      at,
			})
			if at.After(socket.EndedAt) {
				socket.EndedAt = at
			}
		}
		if socket.URL != "" {
			sockets = append(sockets, socket)
		}
	}
	return sockets
}

// harEpochSeconds reads a _webSocketMessages time: seconds since the epoch,
// as browsers write it, or an RFC 3339 time, as recorder exports write it.
func harEpochSeconds(value any) time.Time {
	if text, ok := value.(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(text)); err == nil {
			return parsed
		}
	}
	seconds := floatValue(value)
	if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return time.Time{}
	}
	whole, fraction := math.Modf(seconds)
	return time.Unix(int64(whole), int64(fraction*1e9))
}

func webSocketHARDiagnostic(item map[string]any) string {
	if messages, ok := item["_webSocketMessages"].([]any); ok {
		return fmt.Sprintf("WebSocket connection with %d recorded message(s) is converted to WebSocket steps", len(messages))
	}
	return "WebSocket connection without recorded messages is converted to a connect and close"
}

// applyRecordingProfileFilters drops the exchanges a recording profile does
// not record (host or response content type) and the headers it drops.
func applyRecordingProfileFilters(exchanges []exchange, profile *recordingprofile.Profile) ([]exchange, int) {
	kept := exchanges[:0:0]
	filtered := 0
	for _, ex := range exchanges {
		contentType := ""
		if ex.Response != nil {
			contentType = responseHeaderValue(ex.Response, "Content-Type")
		}
		if !profile.RecordsHost(exchangeHost(ex.URL)) || !profile.RecordsContentType(contentType) {
			filtered++
			continue
		}
		for name := range ex.Headers {
			if keep, decided := profile.HeaderDecision(name); decided && !keep {
				ex.Headers = headersWithoutFold(ex.Headers, name)
				ex.HeaderList = headerListWithoutFold(ex.HeaderList, name)
			}
		}
		kept = append(kept, ex)
	}
	return kept, filtered
}

// shapingFoldedExchanges is the set of exchanges the recorded shape would turn
// into background conversations or other non-request steps, found by shaping
// a copy of the script; nil when there are none.
func shapingFoldedExchanges(script *dsl.Script, exchanges []exchange, document *HARDocument, asyncScan bool, background []string) map[int]bool {
	if script == nil || len(script.Actions) != len(exchanges) {
		return nil
	}
	trial := *script
	trial.Actions = cloneScriptActions(script.Actions)
	_, newIndex, _, _ := shapeRecordedHAR(&trial, exchanges, document, asyncScan, background, nil)
	var folded map[int]bool
	for index, position := range newIndex {
		// A conversation keeps its first request as its own (non-request)
		// action and folds the rest.
		if position < 0 || (position < len(trial.Actions) && trial.Actions[position].Type != "request") {
			if folded == nil {
				folded = map[int]bool{}
			}
			folded[index] = true
		}
	}
	return folded
}
