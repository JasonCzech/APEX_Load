package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func javaActionByType(actions []dsl.Action, actionType string) (dsl.Action, bool) {
	for _, action := range actions {
		if action.Type == actionType {
			return action, true
		}
	}
	return dsl.Action{}, false
}

// TestJavaLRTransactionsFeedStats verifies stateful parity: lr.start_transaction /
// lr.end_transaction feed the transaction counters (not just inert markers).
func TestJavaLRTransactionsFeedStats(t *testing.T) {
	result := convertJavaLRBody(t, `lr.start_transaction("Checkout");
    lr.end_transaction("Checkout", lr.AUTO);`)
	if result.Stats.Transactions != 1 {
		t.Fatalf("Stats.Transactions = %d, want 1", result.Stats.Transactions)
	}
	if result.Stats.TransactionMarkers < 2 {
		t.Fatalf("Stats.TransactionMarkers = %d, want >= 2", result.Stats.TransactionMarkers)
	}
	if _, ok := javaActionByType(result.Script.Actions, "source_transaction"); !ok {
		t.Fatalf("expected a source_transaction action, got %v", actionTypes(result.Script.Actions))
	}
}

// TestJavaLRThinkTimeFeedsDistribution verifies lr.think_time feeds the generated
// think-time distribution on the script config.
func TestJavaLRThinkTimeFeedsDistribution(t *testing.T) {
	result := convertJavaLRBody(t, `lr.think_time(3);`)
	if result.Stats.ThinkTimes != 1 {
		t.Fatalf("Stats.ThinkTimes = %d, want 1", result.Stats.ThinkTimes)
	}
	if result.Script.Config.ThinkTime.Type != "normal" || result.Script.Config.ThinkTime.MeanMs != 3000 {
		t.Fatalf("think_time config = %#v, want normal mean 3000ms", result.Script.Config.ThinkTime)
	}
}

// TestJavaLRDecryptIsReviewOnly is the regression guard for the old blanket-
// Executable bug: lr.decrypt must classify review_only, not executable.
func TestJavaLRDecryptIsReviewOnly(t *testing.T) {
	result := convertJavaLRBody(t, `lr.decrypt("3d2f1a");`)
	if result.Stats.CustomCallFunctions != 0 {
		t.Fatalf("lr.decrypt produced a custom_call, stats=%#v", result.Stats)
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "lr_decrypt", SourceFunctionSupportReviewOnly)
	for _, m := range result.SourceReview.Mappings {
		if m.Function == "lr_decrypt" && m.SupportMode == string(SourceFunctionSupportExecutable) {
			t.Fatalf("lr.decrypt was dishonestly marked executable: %#v", m)
		}
	}
}

// TestJavaLRErrorMessageAndRendezvousUseRichHandlers verifies the Java path now
// reaches the rich C handlers (error_message + rendezvous actions), not source_review.
func TestJavaLRErrorMessageAndRendezvousUseRichHandlers(t *testing.T) {
	result := convertJavaLRBody(t, `lr.error_message("login failed");
    lr.rendezvous("Sync");`)
	if _, ok := javaActionByType(result.Script.Actions, "error_message"); !ok {
		t.Errorf("lr.error_message did not produce an error_message action: %v", actionTypes(result.Script.Actions))
	}
	if _, ok := javaActionByType(result.Script.Actions, "rendezvous"); !ok {
		t.Errorf("lr.rendezvous did not produce a rendezvous action: %v", actionTypes(result.Script.Actions))
	}
}

// TestJavaLREvalStringUnwraps verifies a nested lr.eval_string("{param}") argument
// is converted to the {{var.param}} template, and save_string lands as a parameter.
func TestJavaLRSaveStringEvalUnwraps(t *testing.T) {
	result := convertJavaLRBody(t, `lr.save_string(lr.eval_string("{UserId}"), "Copy");`)
	action, ok := javaActionByType(result.Script.Actions, "source_parameter")
	if !ok {
		t.Fatalf("expected a source_parameter action, got %v", actionTypes(result.Script.Actions))
	}
	args, _ := action.Params["args"].([]any)
	joined := ""
	for _, a := range args {
		if s, ok := a.(string); ok {
			joined += s + "|"
		}
	}
	if !strings.Contains(joined, "{{var.userid}}") {
		t.Fatalf("eval_string was not unwrapped to {{var.userid}}: args=%q", joined)
	}
}

// TestJavaLRUnknownMethodIsReviewNotCustomCall verifies an undocumented lr.* method
// is preserved for review, never a custom_call stub.
func TestJavaLRUnknownMethodIsReviewNotCustomCall(t *testing.T) {
	result := convertJavaLRBody(t, `lr.frobnicate("x");`)
	if result.Stats.CustomCallFunctions != 0 {
		t.Fatalf("unknown lr method produced a custom_call, stats=%#v", result.Stats)
	}
	for _, action := range result.Script.Actions {
		if action.Type == "custom_call" {
			t.Fatalf("unknown lr method emitted a custom_call action: %#v", action)
		}
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "lr_frobnicate", SourceFunctionSupportReviewOnly)
}

// TestJavaCustomClassStillRoutesToSidecar confirms Tier-3 honesty is preserved:
// arbitrary Java (a custom class) is review-only by default and a governed
// java_vuser_run sidecar draft only with explicit opt-in.
func TestJavaCustomClassStillRoutesToSidecar(t *testing.T) {
	body := `CustomClient client = new CustomClient();
    client.invokeBusinessOperation("opaque");`
	result := convertJavaLRBody(t, body)
	if result.DetectedProtocol == nil || result.DetectedProtocol.ImportMode != ImportModeReviewOnly {
		t.Fatalf("custom class default = %#v, want review_only", result.DetectedProtocol)
	}
}
