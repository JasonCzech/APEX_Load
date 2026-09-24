package importcontract

import (
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	k6import "github.com/jasonczech/apexload/pkg/convert/k6"
	"github.com/jasonczech/apexload/pkg/convert/postman"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// The "no silent drop" promise is published by the importers themselves: every
// importer ships a support matrix, and each entry declares how completely the
// construct is converted. TestReviewGateContractRequiresDurableBlockersForOmittedBehavior
// spot-checks that promise for three constructs chosen by hand, which means a
// construct added to a support matrix tomorrow is covered by nothing.
//
// The tests below derive the obligation list from those published matrices
// instead. Adding an entry whose declared disposition is "we did not fully
// reproduce this behavior" fails TestReviewGateRegistryCoverageIsComplete until
// a fixture is registered, and the fixture then has to prove the construct is
// actually surfaced rather than dropped.

// registryConstruct is one support-matrix entry normalized across the slightly
// different per-importer entry structs.
type registryConstruct struct {
	importer  string
	id        string
	construct string
	status    string
}

// omittedBehaviorRegistry returns every published construct whose own support
// matrix says its behavior is not fully reproduced: review_required plus a
// status of "partial" (behavior partly converted) or "review_only" (behavior
// not converted at all). Entries marked "supported" are excluded even when they
// carry review_required, because there the review note is advice about seeding
// or verifying a mapping that did happen, not an omission.
func omittedBehaviorRegistry() []registryConstruct {
	var constructs []registryConstruct
	add := func(importer, id, construct, status string, reviewRequired bool) {
		if !reviewRequired {
			return
		}
		switch status {
		case statusPartial, statusReviewOnly:
		default:
			return
		}
		constructs = append(constructs, registryConstruct{
			importer:  importer,
			id:        id,
			construct: construct,
			status:    status,
		})
	}
	for _, entry := range postman.GetSupportMatrix() {
		add("postman", entry.ID, entry.Construct, entry.Status, entry.ReviewRequired)
	}
	for _, entry := range k6import.GetSupportMatrix() {
		add("k6", entry.ID, entry.Construct, entry.Status, entry.ReviewRequired)
	}
	for _, entry := range jmeter.GetSupportMatrixEntries() {
		add("jmeter", entry.ID, entry.Construct, entry.Status, entry.ReviewRequired)
	}
	sort.Slice(constructs, func(i, j int) bool { return constructs[i].id < constructs[j].id })
	return constructs
}

const (
	statusPartial    = "partial"
	statusReviewOnly = "review_only"
)

// reviewGateOutcome is the review surface of one conversion, normalized across
// importers.
type reviewGateOutcome struct {
	diagnostics diag.Diagnostics
	warnings    []string
	scripts     []*dsl.Script
	review      *sourceview.Review
}

// reviewSignals is everything the import review says about a conversion, rendered
// so two conversions can be compared. Diagnostics and warnings are the import-time
// surface; a source mapping whose disposition is anything but "converted" is the
// durable one.
//
// Counting these globally is NOT enough to prove a construct was surfaced: the
// signal may have been emitted by a different element of the same document. They
// are compared against the same document minus the construct instead — see
// reviewGateFixture.
func (outcome reviewGateOutcome) reviewSignals() []string {
	var signals []string
	for _, diagnostic := range outcome.diagnostics {
		signals = append(signals, "diagnostic|"+string(diagnostic.Severity)+"|"+diagnostic.Element+"|"+diagnostic.Location+"|"+diagnostic.Detail)
	}
	for _, warning := range outcome.warnings {
		signals = append(signals, "warning|"+warning)
	}
	if outcome.review != nil {
		for _, mapping := range outcome.review.Mappings {
			status := strings.TrimSpace(mapping.Status)
			if status == "" || status == "converted" {
				continue
			}
			signals = append(signals, "mapping|"+status+"|"+mapping.Function+"|"+mapping.TargetKind+"|"+mapping.Notes)
		}
	}
	return signals
}

// signalsAddedBy returns the review signals the construct itself is responsible
// for: those present when it is in the document and absent when it is not.
func signalsAddedBy(with, without reviewGateOutcome) []string {
	baseline := map[string]struct{}{}
	for _, signal := range without.reviewSignals() {
		baseline[signal] = struct{}{}
	}
	var added []string
	for _, signal := range with.reviewSignals() {
		if _, ok := baseline[signal]; ok {
			continue
		}
		added = append(added, signal)
	}
	sort.Strings(added)
	return added
}

// durablyGated is the strong half: evidence that survives past import time into
// the saved artifacts - a review action or execution blocker inside the script,
// or a source mapping whose disposition is anything other than "converted".
// Diagnostics do not count here; they are import-time only and are gone once
// the draft is saved.
func (outcome reviewGateOutcome) durablyGated() bool {
	for _, script := range outcome.scripts {
		if script == nil {
			continue
		}
		gates := observeGates(script)
		if gates.ReviewActions > 0 || gates.ExecutionBlockers > 0 {
			return true
		}
	}
	if outcome.review != nil {
		for _, mapping := range outcome.review.Mappings {
			if strings.TrimSpace(mapping.Status) != "" && mapping.Status != "converted" {
				return true
			}
		}
	}
	return false
}

func postmanOutcome(t *testing.T, content string) reviewGateOutcome {
	t.Helper()
	result, err := postman.Convert(postman.ImportRequest{Content: content})
	if err != nil {
		t.Fatalf("postman.Convert: %v", err)
	}
	return reviewGateOutcome{
		diagnostics: result.Unsupported,
		warnings:    result.Warnings,
		scripts:     []*dsl.Script{result.Script},
		review:      result.SourceReview,
	}
}

func k6Outcome(t *testing.T, content string) reviewGateOutcome {
	t.Helper()
	result, err := k6import.Convert(k6import.ImportRequest{Content: content})
	if err != nil {
		t.Fatalf("k6.Convert: %v", err)
	}
	return reviewGateOutcome{
		diagnostics: result.Unsupported,
		warnings:    result.Warnings,
		scripts:     []*dsl.Script{result.Script},
		review:      result.SourceReview,
	}
}

func jmeterOutcome(t *testing.T, name, jmx string) reviewGateOutcome {
	t.Helper()
	result, err := jmeter.ConvertJMX([]byte(jmx), jmeter.ConvertOptions{FileName: name + ".jmx"})
	if err != nil {
		t.Fatalf("jmeter.ConvertJMX: %v", err)
	}
	return reviewGateOutcome{
		diagnostics: result.Unsupported,
		warnings:    result.Warnings,
		scripts:     result.Scripts,
		review:      result.SourceReview,
	}
}

func jmeterPlan(body string) string {
	return `<?xml version="1.0"?><jmeterTestPlan><hashTree><TestPlan testname="contract"/><hashTree>` +
		`<ThreadGroup testname="users"/><hashTree>` + body + `</hashTree></hashTree></hashTree></jmeterTestPlan>`
}

const jmeterContractSampler = `<HTTPSamplerProxy testname="home">` +
	`<stringProp name="HTTPSampler.domain">api.contract.test</stringProp>` +
	`<stringProp name="HTTPSampler.protocol">https</stringProp>` +
	`<stringProp name="HTTPSampler.path">/home</stringProp>` +
	`<stringProp name="HTTPSampler.method">GET</stringProp>` +
	`</HTTPSamplerProxy><hashTree/>`

const jmeterJDBCDataSource = `<JDBCDataSource testname="pool">` +
	`<stringProp name="dbUrl">jdbc:postgresql://db.contract.test:5432/app</stringProp>` +
	`<stringProp name="driver">org.postgresql.Driver</stringProp>` +
	`<stringProp name="username">svc</stringProp>` +
	`<stringProp name="password">secret</stringProp>` +
	`</JDBCDataSource><hashTree/>`

// postmanDoc builds a single-request collection. requestExtra is spliced into the
// request object and itemExtra into the item object, so a with/without pair differs
// by exactly the construct under test and by nothing else.
func postmanDoc(method, url, requestExtra, itemExtra string) string {
	return `{"info":{"name":"contract"},"item":[{"name":"req"` + itemExtra +
		`,"request":{"method":"` + method + `","url":"` + url + `"` + requestExtra + `}}]}`
}

const k6ContractRequest = "http.get('https://api.contract.test/orders');"

// k6ResidualControl is the control document every k6 fixture is compared
// against, and it is deliberately NOT a bare http.get().
//
// pkg/convert/k6 emits the same three signals for ANY source that leaves the
// translated static HTTP subset - the "k6 residual source logic" diagnostic
// (residual_review.go), the "source outside the translated static subset"
// warning (converter.go), and a review_only mapping on the surviving request -
// regardless of which construct pushed it out of that subset. Every k6 fixture
// below is residual by construction, so it collects that triple for free. A
// control of only http.get() is fully static and produces ZERO signals, which
// makes the free triple look like the construct's own evidence: deleting the
// handler for thresholds, options, browser, or control flow still left the
// subtest green.
//
// The neutral residual statement below is therefore residual itself, and sits
// on line 1 - the same line every fixture's construct occupies - so the triple
// is byte-identical on both sides of the comparison and cancels. It must stay
// free of the constructs under test: no options block, no thresholds, no
// `k6/browser` import, and no `if`/`for`/`while`/`switch` (see
// controlFlowPattern), or it would cancel the construct's own signal too.
// TestK6ReviewGateControlsCancelOnlyTheGenericResidualSignals enforces both
// halves of that contract.
const k6ResidualControl = "const controlResidual = Date.now() - 1;\n" + k6ContractRequest

// k6GenericResidualSignalPrefixes identifies the construct-independent residual
// disclosure described above, so the guard test can assert it appears on both
// sides and survives in neither.
func k6GenericResidualSignalPrefixes() []string {
	return []string{
		"diagnostic|warning|k6 residual source logic|",
		"warning|k6 source outside the translated static subset remains at ",
		"mapping|review_only|http.get|action|",
	}
}

func postmanFixture(method, url, requestExtra, itemExtra string) reviewGateFixture {
	return reviewGateFixture{
		with: func(t *testing.T) reviewGateOutcome {
			return postmanOutcome(t, postmanDoc(method, url, requestExtra, itemExtra))
		},
		without: func(t *testing.T) reviewGateOutcome { return postmanOutcome(t, postmanDoc(method, url, "", "")) },
	}
}

func k6Fixture(source string) reviewGateFixture {
	return reviewGateFixture{
		with:    func(t *testing.T) reviewGateOutcome { return k6Outcome(t, source) },
		without: func(t *testing.T) reviewGateOutcome { return k6Outcome(t, k6ResidualControl) },
	}
}

func jmeterFixture(name, withBody, withoutBody string) reviewGateFixture {
	return reviewGateFixture{
		with:    func(t *testing.T) reviewGateOutcome { return jmeterOutcome(t, name, jmeterPlan(withBody)) },
		without: func(t *testing.T) reviewGateOutcome { return jmeterOutcome(t, name, jmeterPlan(withoutBody)) },
	}
}

// reviewGateFixture is a construct's minimal source document paired with the SAME
// document minus that construct.
//
// The pair is the whole point. Asserting only that the conversion produced SOME
// diagnostic or warning is satisfied by a signal from any other element of the
// document: the jmeter-jdbc fixture also carries a JDBCDataSource whose credentials
// warning is emitted whether or not the JDBCSampler is converted, so deleting the
// sampler handler outright left that subtest green. Requiring a signal the control
// document does NOT produce ties the evidence to the construct.
//
// The control has to be chosen so the generic signals actually cancel. A control
// that produces NOTHING re-creates the same hole from the other side: the baseline
// is empty, so every generic signal survives into the "attributable" set and the
// comparison collapses back into "did the conversion emit anything at all". That is
// what a bare http.get() control did to all four k6 fixtures — see k6ResidualControl
// and TestK6ReviewGateControlsCancelOnlyTheGenericResidualSignals.
type reviewGateFixture struct {
	with    func(t *testing.T) reviewGateOutcome
	without func(t *testing.T) reviewGateOutcome
}

// reviewGateFixtures maps a support-matrix entry ID to its fixture pair. The keys
// are checked against omittedBehaviorRegistry(), so this table cannot drift from
// the published matrices in either direction.
func reviewGateFixtures() map[string]reviewGateFixture {
	return map[string]reviewGateFixture{
		"postman-bodies-form": postmanFixture("POST", "https://api.contract.test/upload",
			`,"body":{"mode":"formdata","formdata":[{"key":"doc","type":"file","src":"/tmp/doc.pdf"}]}`, ""),
		"postman-auth-oauth2": postmanFixture("GET", "https://api.contract.test/orders",
			`,"auth":{"type":"oauth2","oauth2":[{"key":"accessToken","value":"opaque"}]}`, ""),
		"postman-auth-signed": postmanFixture("GET", "https://api.contract.test/orders",
			`,"auth":{"type":"ntlm","ntlm":[{"key":"username","value":"svc"}]}`, ""),
		"postman-scripts": postmanFixture("GET", "https://api.contract.test/orders", "",
			`,"event":[{"listen":"prerequest","script":{"type":"text/javascript","exec":["const signature = sign(pm.request.url);"]}}]`),

		"k6-options":    k6Fixture("export const options = { vus: 10, duration: '30s' };\n" + k6ContractRequest),
		"k6-thresholds": k6Fixture("export const options = { thresholds: { http_req_duration: ['p(95)<500'] } };\n" + k6ContractRequest),
		"k6-browser":    k6Fixture("import { chromium } from 'k6/browser';\n" + k6ContractRequest),
		"k6-javascript": k6Fixture("for (let i = 0; i < 2; i++) {\n  " + k6ContractRequest + "\n}"),

		"jmeter-messaging-producers": jmeterFixture("messaging-producer", `<com.di.jmeter.kafka.sampler.KafkaProducerSampler testname="producer"/><hashTree/>`, jmeterContractSampler),
		"jmeter-throughput-shaping":  jmeterFixture("throughput-shaping", `<kg.apc.jmeter.timers.VariableThroughputTimer testname="schedule"/><hashTree/><com.di.jmeter.kafka.sampler.KafkaProducerSampler testname="producer"/><hashTree/>`, `<com.di.jmeter.kafka.sampler.KafkaProducerSampler testname="producer"/><hashTree/>`),
		"jmeter-formatted-time":      jmeterFixture("formatted-time", strings.Replace(jmeterContractSampler, "/home", "/home/${__time(EEEE,)}", 1), jmeterContractSampler),
		"jmeter-auth-manager": jmeterFixture("auth-manager",
			`<AuthManager testname="auth"><collectionProp name="AuthManager.auth_list"><elementProp elementType="Authorization">`+
				`<stringProp name="Authorization.url"></stringProp>`+
				`<stringProp name="Authorization.username">svc</stringProp>`+
				`<stringProp name="Authorization.password">secret</stringProp>`+
				`<stringProp name="Authorization.mechanism">KERBEROS</stringProp>`+
				`</elementProp></collectionProp></AuthManager><hashTree/>`+jmeterContractSampler,
			jmeterContractSampler),
		"jmeter-jsr223": jmeterFixture("jsr223",
			`<JSR223Sampler testname="setup"><stringProp name="scriptLanguage">groovy</stringProp>`+
				`<stringProp name="script">def token = api.fetch(); vars.put("kept", token)</stringProp>`+
				`</JSR223Sampler><hashTree/>`+jmeterContractSampler,
			jmeterContractSampler),
		"jmeter-complex-controllers": jmeterFixture("complex-controllers",
			`<RandomController testname="pick"/><hashTree>`+jmeterContractSampler+`</hashTree>`,
			jmeterContractSampler),
		"jmeter-jdbc": jmeterFixture("jdbc",
			jmeterJDBCDataSource+
				`<JDBCSampler testname="lookup"><stringProp name="query">SELECT 1</stringProp>`+
				`<stringProp name="queryType">Select Statement</stringProp></JDBCSampler><hashTree/>`,
			jmeterJDBCDataSource),
		"jmeter-jms": jmeterFixture("jms",
			`<JMSPublisher testname="publish"><stringProp name="jms.topic">orders</stringProp>`+
				`<stringProp name="jms.text_message">payload</stringProp></JMSPublisher><hashTree/>`,
			""),
		"jmeter-plugins": jmeterFixture("plugins",
			`<kg.apc.jmeter.samplers.DummySampler testname="dummy">`+
				`<stringProp name="RESPONSE_CODE">200</stringProp>`+
				`</kg.apc.jmeter.samplers.DummySampler><hashTree/>`+jmeterContractSampler,
			jmeterContractSampler),
	}
}

// TestReviewGateRegistryCoverageIsComplete is the mechanism that makes the
// guarantee self-extending: it fails when a support matrix gains a construct
// whose behavior is declared partly or wholly unreproduced and no fixture
// exercises it, and it fails again when a fixture outlives the matrix entry it
// was written for.
func TestReviewGateRegistryCoverageIsComplete(t *testing.T) {
	registry := omittedBehaviorRegistry()
	if len(registry) == 0 {
		t.Fatal("no support-matrix entry declares omitted behavior; the derivation is broken, not the importers")
	}
	fixtures := reviewGateFixtures()

	var missing []string
	registered := map[string]struct{}{}
	for _, construct := range registry {
		registered[construct.id] = struct{}{}
		fixture, ok := fixtures[construct.id]
		if !ok || fixture.with == nil || fixture.without == nil {
			missing = append(missing, construct.importer+"/"+construct.id+" ("+construct.construct+")")
		}
	}
	if len(missing) > 0 {
		t.Fatalf("support-matrix constructs declare omitted behavior but no review-gate fixture exercises them: %v", missing)
	}

	var stale []string
	for id := range fixtures {
		if _, ok := registered[id]; !ok {
			stale = append(stale, id)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("review-gate fixtures reference support-matrix IDs that no longer declare omitted behavior: %v", stale)
	}
}

// TestEveryRegisteredOmittedConstructIsSurfacedNotSilentlyDropped is the
// guarantee itself, applied to the derived list rather than to three
// hand-picked constructs.
func TestEveryRegisteredOmittedConstructIsSurfacedNotSilentlyDropped(t *testing.T) {
	fixtures := reviewGateFixtures()
	for _, construct := range omittedBehaviorRegistry() {
		fixture, ok := fixtures[construct.id]
		if !ok || fixture.with == nil || fixture.without == nil {
			continue // reported by TestReviewGateRegistryCoverageIsComplete
		}
		t.Run(construct.id, func(t *testing.T) {
			outcome := fixture.with(t)
			control := fixture.without(t)
			added := signalsAddedBy(outcome, control)
			if len(added) == 0 {
				t.Fatalf("%s (%s) added no review signal of its own: converting the fixture WITH the construct produces %v, and the same document WITHOUT it produces %v. "+
					"Every signal is attributable to the rest of the document, so the construct itself was dropped silently.",
					construct.id, construct.construct, outcome.reviewSignals(), control.reviewSignals())
			}
			if construct.status == statusReviewOnly && !outcome.durablyGated() {
				t.Fatalf("%s (%s) is declared review_only but left no durable gate: every source mapping is 'converted' and no action carries a review gate, so the omission disappears once the draft is saved",
					construct.id, construct.construct)
			}
		})
	}
}

// TestK6ReviewGateControlsCancelOnlyTheGenericResidualSignals keeps the k6 half
// of the guarantee above from decaying back into the global "did the conversion
// emit anything at all" check.
//
// It pins both halves of the k6 control contract for every registered k6
// fixture, including ones added later:
//
//   - the control is residual too, so it carries the construct-independent
//     residual disclosure the fixture would otherwise inherit for free, and
//   - none of that disclosure survives into the attributable set, which is only
//     true while the control's residual line matches the fixture's.
//
// A new k6 fixture whose construct does not sit on line 1 fails here loudly
// instead of quietly passing on borrowed evidence.
func TestK6ReviewGateControlsCancelOnlyTheGenericResidualSignals(t *testing.T) {
	fixtures := reviewGateFixtures()
	prefixes := k6GenericResidualSignalPrefixes()
	var checked int
	for _, construct := range omittedBehaviorRegistry() {
		if construct.importer != "k6" {
			continue
		}
		fixture, ok := fixtures[construct.id]
		if !ok || fixture.with == nil || fixture.without == nil {
			continue // reported by TestReviewGateRegistryCoverageIsComplete
		}
		t.Run(construct.id, func(t *testing.T) {
			checked++
			control := fixture.without(t)
			controlSignals := control.reviewSignals()
			added := signalsAddedBy(fixture.with(t), control)
			for _, prefix := range prefixes {
				if !hasSignalWithPrefix(controlSignals, prefix) {
					t.Fatalf("the k6 control produced no %q signal, so %s inherits that disclosure for free and its subtest degenerates into "+
						"'did the conversion emit anything at all'. Control signals: %v", prefix, construct.id, controlSignals)
				}
				if hasSignalWithPrefix(added, prefix) {
					t.Fatalf("%s keeps the construct-independent %q signal in its attributable set; the control's residual line does not line up with the fixture's, "+
						"so a silent drop of the construct itself would still leave this subtest green. Attributable signals: %v", construct.id, prefix, added)
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("no k6 fixture was checked; the k6 support matrix or the fixture table stopped feeding this guard")
	}
}

func hasSignalWithPrefix(signals []string, prefix string) bool {
	for _, signal := range signals {
		if strings.HasPrefix(signal, prefix) {
			return true
		}
	}
	return false
}

// TestFullySupportedConstructsLeaveNoReviewGate is the true negative that keeps
// the assertion above from being satisfied by importers that gate everything.
func TestFullySupportedConstructsLeaveNoReviewGate(t *testing.T) {
	cases := map[string]func(t *testing.T) reviewGateOutcome{
		"postman": func(t *testing.T) reviewGateOutcome {
			return postmanOutcome(t, `{"info":{"name":"contract"},"item":[{"name":"orders","request":{
			  "method":"GET","url":"https://api.contract.test/orders"}}]}`)
		},
		"k6": func(t *testing.T) reviewGateOutcome {
			return k6Outcome(t, "http.get('https://api.contract.test/orders');")
		},
		"jmeter": func(t *testing.T) reviewGateOutcome {
			return jmeterOutcome(t, "supported", jmeterPlan(jmeterContractSampler))
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			outcome := build(t)
			if outcome.durablyGated() {
				t.Fatalf("a fully supported %s request was review-gated; the gate predicate is trivially true and proves nothing above", name)
			}
		})
	}
}
