package trueclient

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

var truParameterNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

type truXMLTransaction struct {
	Name     string                    `xml:"name,attr"`
	Vertices []truXMLTransactionVertex `xml:"vertex"`
}

type truXMLTransactionVertex struct {
	Type         string `xml:"type,attr"`
	ProgressType string `xml:"progressType,attr"`
	Step         string `xml:"step,attr"`
}

func countDisabledExecutableSteps(step truXMLStep) int {
	n := 0
	if !isStructuralAction(step.Action) {
		n++
	}
	for _, child := range step.Steps {
		n += countDisabledExecutableSteps(child)
	}
	return n
}

func applyTruClientSemanticGate(action *dsl.Action, code, detail string) {
	importreview.ApplyToAction(action, []importreview.Finding{{Code: code, ReasonCode: code, Detail: detail, Source: "truclient", SourceRef: "default.xml", Gate: importreview.GateExecution}})
	action.Metadata["review_required"] = true
	action.Metadata["promotion_blocker"] = true
}

func applyTruClientStepContract(action *dsl.Action, step truXMLStep) {
	if step.Optional {
		action.Metadata["source_optional"] = true
		applyTruClientSemanticGate(action, "truclient_optional_step_requires_review", "Optional step failure handling has no verified per-step equivalent.")
	}
	if step.Unfinalized {
		action.Metadata["source_unfinalized"] = true
		applyTruClientSemanticGate(action, "truclient_unfinalized_step_requires_review", "The source step was not finalized for replay.")
	}
	if event := strings.TrimSpace(step.EndEvent); event != "" {
		action.Metadata["source_end_event"] = event
		switch event {
		case "Action completed", "Object Exists", "Document Loaded", "DOM content loaded", "Step network completed":
		default:
			applyTruClientSemanticGate(action, "truclient_completion_event_requires_review", "The source completion event requires a verified browser synchronization contract.")
		}
	}
	if step.EndEventArgs != "" {
		action.Metadata["source_end_event_arguments"] = step.EndEventArgs
	}
	if step.ObjectTimeout != "" {
		action.Metadata["source_object_timeout_seconds"] = step.ObjectTimeout
	}
	if step.ManualDuration != "" {
		action.Metadata["source_manual_duration"] = step.ManualDuration
		if step.ManualDuration != "0" {
			applyTruClientSemanticGate(action, "truclient_manual_duration_requires_review", "The explicit source minimum step duration requires review.")
		}
	}
	if step.RecordedDuration != "" {
		action.Metadata["source_recorded_duration"] = step.RecordedDuration
	}
	if len(step.AdditionalAttributes) > 0 {
		attributes := map[string]string{}
		for _, attr := range step.AdditionalAttributes {
			attributes[attr.Name.Local] = attr.Value
		}
		action.Metadata["source_step_attributes"] = attributes
	}
	if values, err := parseArgumentValues(step.Arguments); err == nil && !strings.EqualFold(strings.TrimSpace(step.Action), "Evaluate JavaScript") {
		for _, value := range values {
			if value.EvalJavaScript {
				applyTruClientSemanticGate(action, "truclient_argument_javascript_requires_review", "A source argument evaluates JavaScript and requires an explicit native replacement.")
				break
			}
		}
	}
}

func applyTruClientDownloadStepContract(action *dsl.Action, step truXMLStep) {
	if action == nil {
		return
	}
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	if step.Optional {
		action.Metadata["source_download_optional"] = true
		applyTruClientSemanticGate(action, "truclient_optional_step_requires_review", "Optional step failure handling has no verified per-step equivalent.")
	}
	if step.Unfinalized {
		action.Metadata["source_download_unfinalized"] = true
		applyTruClientSemanticGate(action, "truclient_unfinalized_step_requires_review", "The source step was not finalized for replay.")
	}
	if event := strings.TrimSpace(step.EndEvent); event != "" {
		action.Metadata["source_download_end_event"] = event
		if existing, _ := action.Metadata["source_end_event"].(string); existing == "" || existing == "Action completed" {
			action.Metadata["source_end_event"] = event
		}
		switch event {
		case "Action completed", "Object Exists", "Document Loaded", "DOM content loaded", "Step network completed":
		default:
			applyTruClientSemanticGate(action, "truclient_completion_event_requires_review", "The source completion event requires a verified browser synchronization contract.")
		}
	}
	if step.EndEventArgs != "" {
		action.Metadata["source_download_end_event_arguments"] = step.EndEventArgs
	}
	if step.ObjectTimeout != "" {
		action.Metadata["source_download_object_timeout_seconds"] = step.ObjectTimeout
	}
	if step.ManualDuration != "" {
		action.Metadata["source_download_manual_duration"] = step.ManualDuration
		if step.ManualDuration != "0" {
			applyTruClientSemanticGate(action, "truclient_manual_duration_requires_review", "The explicit source minimum step duration requires review.")
		}
	}
	if step.RecordedDuration != "" {
		action.Metadata["source_download_recorded_duration"] = step.RecordedDuration
	}
	if len(step.AdditionalAttributes) > 0 {
		attributes := map[string]string{}
		for _, attr := range step.AdditionalAttributes {
			attributes[attr.Name.Local] = attr.Value
		}
		action.Metadata["source_download_step_attributes"] = attributes
	}
	if values, err := parseArgumentValues(step.Arguments); err == nil {
		for _, value := range values {
			if value.EvalJavaScript {
				applyTruClientSemanticGate(action, "truclient_argument_javascript_requires_review", "A source argument evaluates JavaScript and requires an explicit native replacement.")
				break
			}
		}
	}
}

func applyTruClientCompletionContract(action *dsl.Action, step truXMLStep, descriptors []ObjectDescriptor) {
	if action == nil {
		return
	}
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	switch strings.TrimSpace(step.EndEvent) {
	case "Object Exists":
		id := sourceEndEventObjectID(step.EndEventArgs)
		completion := truXMLStep{TestObject: id}
		locator := resolveSelector(completion, descriptors, "")
		if id == "" || locator == "" {
			applyTruClientSemanticGate(action, "truclient_completion_object_requires_review", "The source completion object cannot be resolved to a browser selector.")
			return
		}
		if descriptor := matchObjectDescriptor(descriptors, id, completion); descriptor != nil {
			frame := normalizeTruClientFrameSelector(descriptor.FrameContext)
			triggerFrame := normalizeTruClientFrameSelector(firstNonEmptyString(stringFromAny(action.Metadata["source_frame_context"]), stringFromAny(action.Metadata["frame_context"])))
			window := strings.TrimSpace(descriptor.WindowContext)
			triggerWindow := strings.TrimSpace(firstNonEmptyString(stringFromAny(action.Metadata["source_window_context"]), stringFromAny(action.Metadata["window_context"])))
			action.Metadata["source_completion_frame_context"] = descriptor.FrameContext
			action.Metadata["source_completion_window_context"] = descriptor.WindowContext
			if frame != triggerFrame || window != triggerWindow {
				applyTruClientSemanticGate(action, "truclient_completion_context_requires_review", "The source completion object uses a different frame or window context; native completion synchronization requires a verified context replacement.")
				return
			}
		}
		action.Sync = &dsl.BrowserSyncPolicy{Type: "element_visible", Value: locator}
	case "Document Loaded":
		action.Sync = &dsl.BrowserSyncPolicy{Type: "load"}
	case "DOM content loaded":
		action.Sync = &dsl.BrowserSyncPolicy{Type: "domcontentloaded"}
	case "Step network completed":
		action.Sync = &dsl.BrowserSyncPolicy{Type: "network_idle"}
	}
}

// Native markers are emitted only for exact before-step/after-step anchors in
// the selected sequence. Missing, disabled, or ambiguous anchors stay gated.
func applyXMLAnchoredTransactions(result *ConvertResult, transactions []truXMLTransaction, preserve bool) {
	if result == nil || result.Script == nil || !preserve {
		return
	}
	type span struct {
		first, last int
		valid       bool
	}
	startSpans := map[string]span{}
	endSpans := map[string]span{}
	recordSpan := func(target map[string]span, id string, i int) {
		if id == "" {
			return
		}
		s, ok := target[id]
		if !ok {
			target[id] = span{i, i, true}
		} else if s.last == i-1 {
			s.last = i
			target[id] = s
		} else {
			s.valid = false
			target[id] = s
		}
	}
	for i, action := range result.Script.Actions {
		id, _ := action.Metadata["source_step_id"].(string)
		downloadID, _ := action.Metadata["source_download_step_id"].(string)
		if downloadID != "" && downloadID != id {
			recordSpan(startSpans, id, i)
			recordSpan(endSpans, downloadID, i)
			continue
		}
		recordSpan(startSpans, id, i)
		recordSpan(endSpans, id, i)
	}
	before, after := map[int][]dsl.Action{}, map[int][]dsl.Action{}
	gates := []dsl.Action{}
	names := map[string]int{}
	for _, transaction := range transactions {
		names[strings.ToLower(strings.TrimSpace(transaction.Name))]++
	}
	for _, transaction := range transactions {
		start, end := -1, -1
		valid := strings.TrimSpace(transaction.Name) != "" && len(transaction.Vertices) == 2 && names[strings.ToLower(strings.TrimSpace(transaction.Name))] == 1
		for _, vertex := range transaction.Vertices {
			switch {
			case vertex.Type == "start" && vertex.ProgressType == "Step started" && start < 0:
				if s, ok := startSpans[vertex.Step]; ok && s.valid {
					start = s.first
				} else {
					valid = false
				}
			case vertex.Type == "end" && vertex.ProgressType == "After step ended" && end < 0:
				if s, ok := endSpans[vertex.Step]; ok && s.valid {
					end = s.last
				} else {
					valid = false
				}
			default:
				valid = false
			}
		}
		if !valid || start < 0 || end < start {
			gate := dsl.Action{Name: "Review transaction anchors", Type: "source_review", Metadata: map[string]any{"source_transaction": transaction.Name, "source_file": "default.xml", "source_ref": "transactions/transaction", "source_transaction_vertices": transaction.Vertices}}
			applyTruClientSemanticGate(&gate, "truclient_transaction_anchor_requires_review", "Transaction anchors are missing, disabled, ambiguous, or use unsupported progress semantics.")
			gates = append(gates, gate)
			result.Stats.ReviewOnlySteps++
			result.Stats.TotalSteps++
			continue
		}
		marker := func(function string) dsl.Action {
			args := []any{transaction.Name}
			if function == "lr_end_transaction" {
				args = append(args, "LR_AUTO")
			}
			anchorID := ""
			for _, vertex := range transaction.Vertices {
				if function == "lr_start_transaction" && vertex.Type == "start" || function == "lr_end_transaction" && vertex.Type == "end" {
					anchorID = vertex.Step
				}
			}
			return dsl.Action{Name: transaction.Name, Type: "source_transaction", Params: map[string]any{"function": function, "args": args}, Metadata: map[string]any{"source": "truclient_transaction_anchor", "source_transaction": transaction.Name, "source_file": "default.xml", "source_ref": "transactions/transaction", "source_anchor_step_id": anchorID}}
		}
		before[start] = append(before[start], marker("lr_start_transaction"))
		after[end] = append(after[end], marker("lr_end_transaction"))
		result.Stats.Transactions++
		result.Stats.TotalSteps += 2
		result.Stats.ConvertedActions += 2
	}
	out := make([]dsl.Action, 0, len(result.Script.Actions)+result.Stats.Transactions*2+len(gates))
	out = append(out, gates...)
	for i, action := range result.Script.Actions {
		out = append(out, before[i]...)
		out = append(out, action)
		out = append(out, after[i]...)
	}
	result.Script.Actions = out
}

func sourceEndEventObjectID(raw string) string {
	var args struct {
		ObjectID string `json:"objectId"`
	}
	if json.Unmarshal([]byte(raw), &args) != nil {
		return ""
	}
	return strings.TrimSpace(args.ObjectID)
}
