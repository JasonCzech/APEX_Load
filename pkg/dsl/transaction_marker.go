package dsl

// TransactionMarker builds a recorded transaction boundary the way an imported
// LoadRunner script carries one: the call's first argument names the
// transaction, so renaming the step (import de-duplication renames a repeated
// step name) never unpairs it, and the end takes LR_AUTO, so a failed step
// inside fails the transaction instead of the end forcing a pass.
func TransactionMarker(name string, start bool) Action {
	if start {
		return Action{Name: "Start " + name + " transaction", Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction", "args": []any{name}}}
	}
	return Action{Name: "End " + name + " transaction", Type: "source_transaction", Params: map[string]any{"function": "lr_end_transaction", "args": []any{name, "LR_AUTO"}}}
}

// TransactionMarkerName is the transaction a marker built by
// TransactionMarker opens or closes.
func TransactionMarkerName(action Action) string {
	if args, ok := action.Params["args"].([]any); ok && len(args) > 0 {
		if name, ok := args[0].(string); ok {
			return name
		}
	}
	return action.Name
}
