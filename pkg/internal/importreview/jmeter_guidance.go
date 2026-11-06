package importreview

import (
	"sort"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
)

// ScriptImportReinspectOption is a conversion option a client sends with the
// same source to convert it again (for the JMeter route, a form field).
type ScriptImportReinspectOption struct {
	Field       string   `json:"field"`
	Values      []string `json:"values,omitempty"`
	Description string   `json:"description,omitempty"`
}

// JMeterReviewGuidance explains one JMeter import review code in plain words:
// what it means, what clears it, and whether anything can clear it yet.
type JMeterReviewGuidance struct {
	Code       string
	Title      string
	Detail     string
	Resolution string
	// Clearable is false for a gate no edit or repair can clear in this
	// release. Saying so stops the review from implying the operator missed a
	// step.
	Clearable bool
	// BatchOperation is the reviewed JMeter batch repair (POST
	// /api/v1/import/repairs/{preview,apply}, operation type) that clears the
	// code, when there is one.
	BatchOperation string
	// Reinspect lists the conversion options that clear the code when the
	// same source is converted again.
	Reinspect []ScriptImportReinspectOption
}

// EventHubsConnectionStringFormat is the connection string the Event Hubs
// runtime accepts for a JMeter-imported producer.
const EventHubsConnectionStringFormat = "Endpoint=sb://<namespace>.servicebus.windows.net/;SharedAccessKeyName=<policy name>;SharedAccessKey=<key>"

var jmeterReviewGuidance = map[string]JMeterReviewGuidance{
	"jmeter_timestamp_timezone_required": {
		Title: "Choose how JMeter timestamps are evaluated",
		Detail: "JMeter's __time() formats the time in the zone of the JVM that ran the plan, and this request " +
			"adds a literal Z as if that time were UTC. APEX cannot tell which zone was meant, so the request " +
			"is held until you choose.",
		Resolution: "Convert the plan again with a timestamp policy: \"Normalize to UTC with milliseconds\" sends " +
			"true UTC time, or \"Preserve wall clock and acknowledge literal Z suffix\" with the IANA timezone the " +
			"JMeter host ran in reproduces its local time with the literal Z.",
		Clearable: true,
		Reinspect: []ScriptImportReinspectOption{
			{Field: "time_mode", Values: []string{"utc", "source"}},
			{Field: "timezone", Description: "IANA timezone such as America/Chicago; required when time_mode is source"},
		},
	},
	"jmeter_reference_context_confirmation_required": {
		Title: "Confirm the JMeter reference timezone",
		Detail: "The messaging payloads format times with __time(), which JMeter evaluates in its JVM's default " +
			"zone, and JMeter properties can override plan values. APEX needs you to confirm both before replay.",
		Resolution: "Apply the repair \"Confirm the reference timezone and no property overrides\" with the zone " +
			"the JMeter host ran in (UTC, or an IANA zone such as America/Chicago), confirming that no JMeter property " +
			"overrides (-J options) applied. A plan converted with the timestamp policy \"Normalize to UTC with " +
			"milliseconds\" sends true UTC time, so confirm UTC for it.",
		Clearable:      true,
		BatchOperation: "confirm_reference_context",
	},
	"jmeter_kafka_oauth_credentials_required": {
		Title: "Bind the Kafka OAuth client credentials",
		Detail: "The Kafka producer authenticates with an OAuth client ID and client secret from the plan's JAAS " +
			"configuration. APEX does not keep them in the script.",
		Resolution: "Create project secrets for the client ID and the client secret, then apply the repair " +
			"\"Bind Kafka credential references\" with their references in \"Client ID secret reference\" and " +
			"\"Client secret reference\".",
		Clearable:      true,
		BatchOperation: "bind_kafka_credentials",
	},
	"jmeter_kafka_nonstandard_oauth_options": {
		Title: "Rename the Kafka OAuth options",
		Detail: "The JAAS configuration names the OAuth credentials cid and cs. Kafka's OAuth login module reads " +
			"only clientId and clientSecret, so these options would not reach the token request as written.",
		Resolution: "Apply the repair \"Normalize Kafka OAuth option names\" to read cid as clientId and cs as clientSecret. " +
			"The repair is refused when the plan also sets clientId or clientSecret.",
		Clearable:      true,
		BatchOperation: "normalize_oauth_options",
	},
	"jmeter_eventhubs_credentials_required": {
		Title:  "Bind the Event Hubs connection string",
		Detail: "The Event Hubs sampler authenticates with a shared access key. APEX does not keep it in the script.",
		Resolution: "Create a project secret that holds the full connection string, " + EventHubsConnectionStringFormat +
			", with no spaces around the key, then apply the repair \"Bind Event Hubs credential reference\" with " +
			"its reference in \"Connection string secret reference\". " +
			"A string copied from the event hub's own access policy ends with ;EntityPath=<event hub>; keep it " +
			"only if it names this sampler's event hub.",
		Clearable:      true,
		BatchOperation: "bind_eventhubs_credentials",
	},
	"jmeter_messaging_compatibility_unverified": {
		Title: "JMeter messaging replay is not available yet",
		Detail: "APEX has not yet verified this Kafka or Event Hubs conversion against a live broker, so it does " +
			"not run imported JMeter messaging plans.",
		Resolution: "Nothing in the plan or the import can clear this gate in this release. You can save and " +
			"review the scripts now; they cannot run until messaging conformance is verified.",
	},
	"jmeter_kafka_batch_allocation_parity_unverified": {
		Title: "Kafka batching parity is not verified yet",
		Detail: "APEX has not yet verified that it groups records into Kafka batches the way the JMeter producer " +
			"does, which changes how load reaches the brokers.",
		Resolution: "Nothing in the plan or the import can clear this gate in this release. The script can be " +
			"saved and reviewed but cannot run until batching parity is verified.",
	},
}

// JMeterReviewCodeGuidance returns the plain-language guidance for a JMeter
// import review code.
func JMeterReviewCodeGuidance(code string) (JMeterReviewGuidance, bool) {
	guidance, ok := jmeterReviewGuidance[code]
	if ok {
		guidance.Code = code
		guidance.Reinspect = append([]ScriptImportReinspectOption(nil), guidance.Reinspect...)
	}
	return guidance, ok
}

// jmeterGenericReviewDetail is the converter's fallback detail for a review
// code it has no wording for (pkg/convert/jmeter/review_evidence.go).
const jmeterGenericReviewDetail = "Resolve imported behavior before replay: "

// ExplainJMeterReviewDiagnostics replaces that fallback detail with the plain
// explanation (Detail) and sets Resolution to what clears the finding, for
// the codes this table knows. A detail the
// converter wrote itself is left alone. The converter cannot read the table
// directly: this package imports it.
func ExplainJMeterReviewDiagnostics(diagnostics diag.Diagnostics) {
	for index := range diagnostics {
		code := diagnostics[index].Code
		guidance, ok := JMeterReviewCodeGuidance(code)
		if !ok || diagnostics[index].Detail != jmeterGenericReviewDetail+code {
			continue
		}
		diagnostics[index].Detail = guidance.Detail
		diagnostics[index].Resolution = guidance.Resolution
	}
}

// JMeterReviewRepairSuggestions turns the JMeter review codes held on the
// scripts' steps into one suggestion per code and source group, in script
// order. A batch repair needs the group it applies to, so the suggestion
// carries it.
func JMeterReviewRepairSuggestions(scripts []*dsl.Script) []ScriptImportRepairSuggestion {
	suggestions := make([]ScriptImportRepairSuggestion, 0)
	seen := map[string]struct{}{}
	for _, script := range scripts {
		if script == nil {
			continue
		}
		group := ""
		if script.Config.JMeter != nil {
			group = script.Config.JMeter.SourceGroupID
		}
		codes := map[string]string{}
		_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
			if ref.Action == nil {
				return nil
			}
			metadata := ref.Metadata()
			review, _ := metadata["import_review"].(map[string]any)
			for _, code := range append(append(MetadataStringSlice(review, "reason_codes"), ExecutionReviewBlockerCodes(metadata)...), MetadataStringSlice(metadata, "review_reasons")...) {
				if _, known := jmeterReviewGuidance[code]; known {
					if _, recorded := codes[code]; !recorded {
						codes[code] = ref.Action.Name
					}
				}
			}
			return nil
		})
		ordered := make([]string, 0, len(codes))
		for code := range codes {
			ordered = append(ordered, code)
		}
		sort.Strings(ordered)
		for _, code := range ordered {
			guidance, _ := JMeterReviewCodeGuidance(code)
			// One confirmation covers every group, a re-conversion the whole
			// plan, and a gate nothing clears says the same for every group,
			// so none of them repeats per script.
			key := code + "\x00" + group
			if !guidance.Clearable || guidance.BatchOperation == "confirm_reference_context" || len(guidance.Reinspect) > 0 {
				key = code
			}
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			clearable := guidance.Clearable
			suggestion := ScriptImportRepairSuggestion{
				ID:             "jmeter:" + code,
				Code:           code,
				Category:       "review_gate",
				Severity:       "blocker",
				Title:          guidance.Title,
				Description:    guidance.Detail,
				Action:         guidance.Resolution,
				Clearable:      &clearable,
				BatchOperation: guidance.BatchOperation,
				Reinspect:      guidance.Reinspect,
			}
			if key != code {
				suggestion.ID += ":" + group
				suggestion.SourceGroupID = group
				suggestion.TargetAction = codes[code]
			}
			suggestions = append(suggestions, suggestion)
		}
	}
	return suggestions
}
