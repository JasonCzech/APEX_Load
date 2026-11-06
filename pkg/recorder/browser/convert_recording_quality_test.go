package browser

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/selector"
)

func qualityElement(tag, id string) *selector.ElementMetadata {
	return &selector.ElementMetadata{Tag: tag, ID: id, CSSPath: "#" + id}
}

func qualityStep(kind string, element *selector.ElementMetadata, ms int64, value string) Step {
	return Step{Kind: kind, Value: value, Timestamp: ts(ms), Element: element, Candidates: selector.Candidates(*element), URL: "https://app.example.test/form"}
}

func fillActions(t *testing.T, result *ConvertResult) []int {
	t.Helper()
	var fills []int
	for index, action := range result.Script.Actions {
		if action.Type == "browser_fill" {
			fills = append(fills, index)
		}
	}
	return fills
}

// A field typed in bursts records a fill per burst holding the text so far.
// Conversion fills the final value once and keeps the typing time.
func TestConvertMergesFillBursts(t *testing.T) {
	address := qualityElement("input", "address")
	submit := qualityElement("button", "submit")
	session := &Session{ID: "rec_bursts", StartURL: "https://app.example.test/form", StartedAt: ts(0)}
	session.appendStep(qualityStep(StepFill, address, 1000, "11"))
	session.appendStep(qualityStep(StepFill, address, 1700, "1125 Ma"))
	session.appendStep(qualityStep(StepFill, address, 2600, "1125 Main St"))
	session.appendStep(qualityStep(StepClick, submit, 6000, ""))

	result := Convert(session, ConvertOptions{})
	fills := fillActions(t, result)
	if len(fills) != 1 {
		t.Fatalf("actions = %v, want the three bursts as one fill", actionTypes(result.Script.Actions))
	}
	fill := result.Script.Actions[fills[0]]
	if fill.Value != "1125 Main St" {
		t.Fatalf("fill value = %q, want the field's final value", fill.Value)
	}
	if got := fill.Metadata["recorder_merged_step_seqs"]; !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Fatalf("merged seqs = %#v, want [1 2 3]", got)
	}
	if !fill.ThinkTimeAfter || fill.ThinkTime.MeanMs != 5000 {
		t.Fatalf("think time = %+v after=%v, want the 5s from the first burst to the click", fill.ThinkTime, fill.ThinkTimeAfter)
	}
	if fill.Metadata["recorder_step_seq"] != 3 {
		t.Fatalf("recorder_step_seq = %#v, want the last burst's", fill.Metadata["recorder_step_seq"])
	}
	found := false
	for _, warning := range result.Warnings {
		found = found || strings.Contains(warning, "3 fill step(s) typed into the same field in bursts were merged into 1 fill(s)")
	}
	if !found {
		t.Fatalf("warnings = %v, want the merge reported", result.Warnings)
	}
}

// A masked password typed in bursts, interrupted by a click, fills twice:
// the click ends a burst, and each fill uses the placeholder once.
func TestConvertMergesMaskedFillBurstsUpToAnInterruption(t *testing.T) {
	password := &selector.ElementMetadata{Tag: "input", ID: "password", Name: "pw", Type: "password", CSSPath: "#password"}
	main := qualityElement("div", "main")
	session := &Session{ID: "rec_pw_bursts", StartURL: "https://app.example.test/login", StartedAt: ts(0)}
	for _, ms := range []int64{1000, 1300, 1900} {
		step := qualityStep(StepFill, password, ms, "")
		step.Masked = true
		session.appendStep(step)
	}
	session.appendStep(qualityStep(StepClick, main, 2500, ""))
	for _, ms := range []int64{3000, 3800} {
		step := qualityStep(StepFill, password, ms, "")
		step.Masked = true
		session.appendStep(step)
	}

	result := Convert(session, ConvertOptions{})
	fills := fillActions(t, result)
	if len(fills) != 2 {
		t.Fatalf("actions = %v, want two masked fills around the click", actionTypes(result.Script.Actions))
	}
	for _, index := range fills {
		if got := result.Script.Actions[index].Value; got != "{{env.PW}}" {
			t.Fatalf("masked fill = %q, want {{env.PW}}", got)
		}
	}
	masked := 0
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "filled a password input") {
			masked++
		}
	}
	if masked != 2 {
		t.Fatalf("masked-fill warnings = %d, want one per fill: %v", masked, result.Warnings)
	}
}

func TestConvertDoesNotMergeFillsOfDifferentFields(t *testing.T) {
	first := qualityElement("input", "first")
	last := qualityElement("input", "last")
	session := &Session{ID: "rec_fields", StartURL: "https://app.example.test/form", StartedAt: ts(0)}
	session.appendStep(qualityStep(StepFill, first, 1000, "Ada"))
	session.appendStep(qualityStep(StepFill, last, 2000, "Lovelace"))
	session.appendStep(qualityStep(StepFill, first, 3000, "Ada M"))

	result := Convert(session, ConvertOptions{})
	if fills := fillActions(t, result); len(fills) != 3 {
		t.Fatalf("actions = %v, want three fills", actionTypes(result.Script.Actions))
	}
}

// One-time-code boxes share a name but are separate fields: each keeps its fill.
func TestConvertDoesNotMergeFillsOfFieldsSharingASelector(t *testing.T) {
	session := &Session{ID: "rec_otp", StartURL: "https://app.example.test/verify", StartedAt: ts(0)}
	for index, digit := range []string{"4", "2", "7"} {
		box := &selector.ElementMetadata{Tag: "input", Name: "otp", CSSPath: "form > input:nth-of-type(" + digit + ")"}
		step := qualityStep(StepFill, box, int64(1000+index*700), digit)
		step.Candidates = []selector.Candidate{{Kind: "css", Value: `input[name="otp"]`, Confidence: 76}}
		session.appendStep(step)
	}
	result := Convert(session, ConvertOptions{})
	if fills := fillActions(t, result); len(fills) != 3 {
		t.Fatalf("actions = %v, want one fill per box", actionTypes(result.Script.Actions))
	}
}

// Promoting a self-healed selector onto the merged fill's step must not split
// the burst back into fills.
func TestConvertKeepsFillBurstMergedAfterSelectorPromotion(t *testing.T) {
	field := qualityElement("input", "address")
	session := &Session{ID: "rec_promote", StartURL: "https://app.example.test/form", StartedAt: ts(0)}
	session.appendStep(qualityStep(StepFill, field, 1000, "11"))
	last := qualityStep(StepFill, field, 1800, "1125 Main St")
	last.Candidates = append([]selector.Candidate{{Kind: "css", Value: "#address-healed", Confidence: 99, Reason: "Replay-verified self-heal promotion."}}, last.Candidates...)
	session.appendStep(last)
	result := Convert(session, ConvertOptions{})
	if fills := fillActions(t, result); len(fills) != 1 {
		t.Fatalf("actions = %v, want the promoted burst still one fill", actionTypes(result.Script.Actions))
	}
}

// A fill that loads another page ends the burst: the first page's value is
// kept.
func TestConvertDoesNotMergeFillsAcrossPages(t *testing.T) {
	field := qualityElement("input", "q")
	session := &Session{ID: "rec_pages", StartURL: "https://app.example.test/search", StartedAt: ts(0)}
	first := qualityStep(StepFill, field, 1000, "shoes")
	first.URL = "https://app.example.test/search"
	second := qualityStep(StepFill, field, 2500, "boots")
	second.URL = "https://app.example.test/results?q=shoes"
	session.appendStep(first)
	session.appendStep(second)
	result := Convert(session, ConvertOptions{})
	if fills := fillActions(t, result); len(fills) != 2 {
		t.Fatalf("actions = %v, want one fill per page", actionTypes(result.Script.Actions))
	}
}

// A page that rewrites its query while the user types is still one page.
func TestConvertMergesFillsWhileThePageRewritesItsQuery(t *testing.T) {
	field := qualityElement("input", "q")
	session := &Session{ID: "rec_query", StartURL: "https://app.example.test/form", StartedAt: ts(0)}
	first := qualityStep(StepFill, field, 1000, "sh")
	first.URL = "https://app.example.test/form?draft=1"
	second := qualityStep(StepFill, field, 1800, "shoes")
	second.URL = "https://app.example.test/form?draft=2"
	session.appendStep(first)
	session.appendStep(second)
	result := Convert(session, ConvertOptions{})
	if fills := fillActions(t, result); len(fills) != 1 {
		t.Fatalf("actions = %v, want one merged fill", actionTypes(result.Script.Actions))
	}
}

// A window switch keeps the popup's full URL; replay matches it exactly and
// then by page (browsercontract.WindowPageKey), so a later run's query works.
func TestConvertSwitchWindowKeepsRecordedURL(t *testing.T) {
	recorded := "https://sso.example.test/lrr/Controller?TAM_OP=token_login&EXPIRE_SECS=60"
	session := &Session{ID: "rec_popup", StartURL: "https://app.example.test/", StartedAt: ts(0)}
	session.appendStep(Step{Kind: StepSwitchWindow, Value: recorded, Timestamp: ts(1000)})
	result := Convert(session, ConvertOptions{})
	var target string
	for _, action := range result.Script.Actions {
		if action.Type == "browser_switch_window" {
			target = action.Target
		}
	}
	if target != recorded {
		t.Fatalf("switch target = %q, want the recorded URL %q", target, recorded)
	}
}

// What happened on the browser's own error page is not part of the site.
func TestConvertSkipsBrowserErrorPage(t *testing.T) {
	button := qualityElement("button", "go")
	session := &Session{ID: "rec_error_page", StartURL: "https://app.example.test/", StartedAt: ts(0)}
	session.appendStep(qualityStep(StepClick, button, 1000, ""))
	onErrorPage := qualityStep(StepClick, qualityElement("div", "sub-frame-error"), 9000, "")
	onErrorPage.URL = "chrome-error://chromewebdata/"
	session.appendStep(onErrorPage)
	session.NavEvents = append(session.NavEvents, NavEvent{Timestamp: ts(8000), URL: "chrome-error://chromewebdata/"})

	result := Convert(session, ConvertOptions{})
	for _, action := range result.Script.Actions {
		if strings.Contains(action.Target, "chrome-error") || strings.Contains(action.Selector, "sub-frame-error") {
			t.Fatalf("actions = %+v, want nothing from the error page", result.Script.Actions)
		}
	}
	found := false
	for _, warning := range result.Warnings {
		found = found || strings.Contains(warning, "browser's own error page")
	}
	if !found {
		t.Fatalf("warnings = %v, want the left-out error page reported", result.Warnings)
	}
}

// A click in a frame of the login page, stamped before a hover reported ahead
// of it, splits the sign-in in time order: nothing moves to init, and the
// conversion says why.
func TestConvertSignInMovesOnlyTheLeadingSignInRun(t *testing.T) {
	login := "https://acme.my.salesforce.com/"
	session := &Session{ID: "rec_signin_order", StartURL: login, StartedAt: ts(0)}
	hover := qualityStep(StepHover, qualityElement("div", "login_form"), 2000, "")
	hover.URL = login
	session.appendStep(hover)
	frameClick := qualityStep(StepClick, qualityElement("body", "promo"), 1500, "")
	frameClick.URL = "https://c.salesforce.com/login-messages/promos.html"
	session.appendStep(frameClick)
	app := qualityStep(StepClick, qualityElement("button", "search"), 9000, "")
	app.URL = "https://acme.lightning.force.com/one/one.app"
	session.appendStep(app)

	result := Convert(session, ConvertOptions{})
	for _, action := range result.Script.Init {
		if action.Selector == frameClick.bestSelector() || action.Selector == hover.bestSelector() {
			t.Fatalf("init = %+v, want the split sign-in left whole in actions", result.Script.Init)
		}
	}
	found := false
	for _, warning := range result.Warnings {
		found = found || strings.Contains(warning, "another recorded step happened in the middle of it")
	}
	if !found {
		t.Fatalf("warnings = %v, want the split sign-in reported", result.Warnings)
	}
}

// A long pause between two fills of a search-as-you-type field is reading the
// results: each query keeps its own fill, and the requests it caused.
func TestConvertDoesNotMergeFillsAcrossALongPause(t *testing.T) {
	search := qualityElement("input", "search")
	session := &Session{ID: "rec_typeahead", StartURL: "https://app.example.test/form", StartedAt: ts(0)}
	session.appendStep(qualityStep(StepFill, search, 1000, "acme"))
	session.appendStep(qualityStep(StepFill, search, 9000, "acme corp"))
	session.appendStep(qualityStep(StepFill, search, 9800, "acme corpo"))

	result := Convert(session, ConvertOptions{})
	fills := fillActions(t, result)
	if len(fills) != 2 {
		t.Fatalf("actions = %v, want the first query and the merged second burst", actionTypes(result.Script.Actions))
	}
	if got := result.Script.Actions[fills[1]].Value; got != "acme corpo" {
		t.Fatalf("second fill = %q, want the burst's final value", got)
	}
}
