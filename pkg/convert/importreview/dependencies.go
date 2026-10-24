package importreview

import (
	"fmt"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dsl/varflow"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// AnalyzeDependencies reports every template reference that has no value where
// it executes. It runs the execution-order analysis behind server replay
// readiness (pkg/dsl/varflow), so the names it reports are the replay
// preflight's missing_variables except protocol configuration references.
// Replay readiness reports those from the current configuration on every
// preflight; no step or guided repair can supply them, so import review never
// stamps them. Source snapshots, comments and the arguments of steps the
// runtime only logs or uses literally are evidence, never reads or producers.
// So is metadata, except the values a Kafka or JMS send delivers as headers or
// properties and a {{param.*}} in a protocol step's metadata outside its
// import evidence keys, which replay refuses while it is unresolved.
//
// A {{var.*}} reference that some step does write, only not on every path
// before the use, is conditional_producer: its branch or position may not run
// first. Any other reference is missing_producer. A use that replay cannot
// carry as placeholder text (a JSON helper input, an undeclared {{param.*}},
// a Java HTTP request body, which replay decodes as base64, or a protocol
// other than HTTP, SOAP and Java HTTP, which has no placeholder fallback)
// gates execution; every other use gates promotion.
func AnalyzeDependencies(script *dsl.Script) ([]Finding, error) {
	if script == nil {
		return nil, nil
	}
	// varflow and dslwalk name actions with the same paths, including
	// control bodies and http_sequence children, so a finding resolves its
	// source position from the action it is reported on.
	surfaces := map[string]dependencySurface{}
	if err := dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		surface := dependencySurface{metadata: ref.Metadata()}
		if ref.Action != nil {
			surface.actionType = ref.Action.Type
		} else if ref.Request != nil {
			surface.actionType, _ = ref.Request["type"].(string)
		}
		surfaces[ref.Path] = surface
		return nil
	}); err != nil {
		return nil, err
	}
	readiness := varflow.Analyze(script, nil)
	lateProduced := make(map[string]bool, len(readiness.LateProduced))
	for _, name := range readiness.LateProduced {
		lateProduced[name] = true
	}
	findings := make([]Finding, 0, len(readiness.Misses))
	for _, miss := range readiness.Misses {
		if miss.Config {
			continue
		}
		surface := surfaces[miss.Path]
		name := miss.Reference.Name
		code, detail := "missing_producer:"+name, missingProducerDetail(miss, surface)
		// A variable write never supplies {{param.*}}, so only a variable use
		// can be out of order.
		if miss.Reference.Namespace == varflow.NamespaceVar && lateProduced[name] {
			code = "conditional_producer:" + name
			detail = "Producer ordering or conditional execution for " + name + " requires review."
			if miss.Blocking {
				detail += " " + blockingUseReason(miss, surface)
			}
		}
		level, gate := diag.Warning, GatePromotion
		if miss.Blocking {
			level, gate = diag.Blocker, GateExecution
		}
		source := fmt.Sprintf("%v:%v", surface.metadata["source_file"], surface.metadata["source_line"])
		findings = append(findings, Finding{Code: code, ReasonCode: code, Severity: level, Gate: gate, Detail: detail, Element: name, ActionPath: miss.Path, Source: "enterprise_script_archive", SourceRef: source, Resolution: "bind_producer"})
	}
	return NormalizeFindings(findings), nil
}

// dependencySurface is what a dependency finding needs from the action it is
// reported on.
type dependencySurface struct {
	actionType string
	metadata   map[string]any
}

func missingProducerDetail(miss varflow.Miss, surface dependencySurface) string {
	name := miss.Reference.Name
	switch {
	case miss.Reference.Namespace == varflow.NamespaceParam:
		// The runtime resolves {{param.*}} only from declared parameters; a
		// data source column is published as data.<source>.<column>.
		return "No parameter supplies " + name + ", and replay refuses to start with an unresolved parameter. Declare a script parameter named " + name + " (it can read its values from a data source)."
	case !miss.Blocking:
		// Like LoadRunner, which sends an undefined {param} literally and
		// warns that it "is not a parameter", replay sends a {{var.*}}
		// placeholder unchanged and warns; promotion still requires a
		// reviewed producer.
		return "No preceding producer supplies " + name + ". Replay sends its placeholder text unchanged and warns, as LoadRunner does; bind a producer, parameter, or response extractor if the server needs a real value."
	case dsl.CanonicalActionType(surface.actionType) == "source_json":
		return "No preceding producer supplies " + name + ", and the JSON helper fails its step without a value. Bind a producer, parameter, or response extractor."
	case miss.Base64Body:
		// JavaHTTP.Execute (pkg/protocol/java_http/java_http.go) decodes the
		// body as base64 before the HTTP adapter could send a placeholder.
		return "No preceding producer supplies " + name + ", and Java HTTP replay decodes the request body as base64, so placeholder text there fails the step. Bind a producer, parameter, or response extractor."
	default:
		return "No preceding producer supplies " + name + ", and this protocol has no placeholder fallback, so replay refuses the step without a value. Bind a producer, parameter, or response extractor."
	}
}

// blockingUseReason explains why a variable use that replay cannot carry as
// placeholder text gates execution.
func blockingUseReason(miss varflow.Miss, surface dependencySurface) string {
	switch {
	case dsl.CanonicalActionType(surface.actionType) == "source_json":
		return "The JSON helper fails its step when the value is absent."
	case miss.Base64Body:
		return "Java HTTP replay decodes the request body as base64, so the step fails when the value is absent."
	default:
		return "This protocol has no placeholder fallback, so replay refuses the step when the value is absent."
	}
}
