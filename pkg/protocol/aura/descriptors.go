package aura

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// ActionDescriptor describes one sub-action inside an Aura request envelope's
// actions[] array, including optional ApexActionController target metadata.
type ActionDescriptor struct {
	Descriptor    string
	ApexNamespace string
	ApexClassname string
	ApexMethod    string
}

// ExtractMessageJSON extracts the raw JSON object string for an Aura request's
// "message" payload from either a direct JSON body or an
// application/x-www-form-urlencoded body containing "message=...".
func ExtractMessageJSON(body string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = MaxEnvelopeBytes
	}
	if body == "" || len(body) > maxBytes {
		return ""
	}
	trimmed := strings.TrimSpace(body)
	if strings.HasPrefix(trimmed, "{") {
		return trimmed
	}
	for rest := body; rest != ""; {
		var part string
		part, rest, _ = strings.Cut(rest, "&")
		if strings.HasPrefix(part, "message=") {
			decoded, err := url.QueryUnescape(part[len("message="):])
			if err != nil {
				return ""
			}
			rawMessage := strings.TrimSpace(decoded)
			if strings.HasPrefix(rawMessage, "{") {
				return rawMessage
			}
			return ""
		}
	}
	return ""
}

// ParseEnvelopeActions parses the ordered sub-action descriptors and Apex
// target parameters from an Aura POST body.
func ParseEnvelopeActions(body string, maxBytes int) []ActionDescriptor {
	rawMessage := ExtractMessageJSON(body, maxBytes)
	if rawMessage == "" {
		return nil
	}
	var envelope struct {
		Actions []struct {
			Descriptor string          `json:"descriptor"`
			Params     json.RawMessage `json:"params"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(rawMessage), &envelope); err != nil || len(envelope.Actions) == 0 {
		return nil
	}
	out := make([]ActionDescriptor, 0, len(envelope.Actions))
	for _, action := range envelope.Actions {
		desc := strings.TrimSpace(action.Descriptor)
		item := ActionDescriptor{Descriptor: desc}
		if len(action.Params) > 0 {
			var apexParams struct {
				Namespace string `json:"namespace"`
				Classname string `json:"classname"`
				Method    string `json:"method"`
			}
			if json.Unmarshal(action.Params, &apexParams) == nil {
				item.ApexNamespace = strings.TrimSpace(apexParams.Namespace)
				item.ApexClassname = strings.TrimSpace(apexParams.Classname)
				item.ApexMethod = strings.TrimSpace(apexParams.Method)
			}
		}
		if item.Descriptor != "" || item.ApexClassname != "" || item.ApexMethod != "" {
			out = append(out, item)
		}
	}
	return out
}

// IsApexExecute reports whether the descriptor invokes ApexActionController
// (or aura.ApexAction.execute) with an explicit Apex class or method.
func (d ActionDescriptor) IsApexExecute() bool {
	if d.ApexClassname == "" && d.ApexMethod == "" {
		return false
	}
	lower := strings.ToLower(d.Descriptor)
	return strings.Contains(lower, "apexactioncontroller/action$execute") ||
		strings.HasSuffix(lower, "apexaction.execute")
}

// QualifiedApexTarget formats [namespace.]classname[.method] when Apex target
// parameters are present.
func (d ActionDescriptor) QualifiedApexTarget() string {
	if d.ApexClassname == "" && d.ApexMethod == "" {
		return ""
	}
	qualified := d.ApexClassname
	if d.ApexNamespace != "" && d.ApexClassname != "" {
		qualified = d.ApexNamespace + "." + d.ApexClassname
	} else if d.ApexNamespace != "" {
		qualified = d.ApexNamespace
	}
	if qualified != "" && d.ApexMethod != "" {
		return qualified + "." + d.ApexMethod
	}
	return qualified + d.ApexMethod
}

// ShortTitle formats a concise human-readable sub-action label such as
// "VisibilityControlUtils.getVcForLoginUserNewFramework" for Apex calls or
// "RecordUiController.getRecordWithFields" for standard Aura controllers.
func (d ActionDescriptor) ShortTitle() string {
	if d.IsApexExecute() {
		if target := d.QualifiedApexTarget(); target != "" {
			return target
		}
	}
	desc := stripDescriptorScheme(d.Descriptor)
	if desc == "" {
		return ""
	}
	controller, method, hasAction := strings.Cut(desc, "/ACTION$")
	if idx := strings.LastIndex(controller, "."); idx >= 0 && idx+1 < len(controller) {
		controller = controller[idx+1:]
	}
	if hasAction && method != "" {
		return controller + "." + method
	}
	return controller
}

// DiffToken formats a canonical disambiguated token for two-HAR request pairing.
func (d ActionDescriptor) DiffToken() string {
	if d.IsApexExecute() {
		if target := d.QualifiedApexTarget(); target != "" {
			return "Apex:" + target
		}
	}
	desc := stripDescriptorScheme(d.Descriptor)
	if desc == "" {
		return ""
	}
	return strings.ReplaceAll(desc, "/ACTION$", ".")
}

// FilterToken formats a descriptor with an optional "[namespace.Class.method]"
// suffix for recording-profile and client-telemetry filtering.
func (d ActionDescriptor) FilterToken() string {
	desc := d.Descriptor
	if target := d.QualifiedApexTarget(); target != "" {
		return desc + "[" + target + "]"
	}
	return desc
}

func stripDescriptorScheme(desc string) string {
	desc = strings.TrimSpace(desc)
	for _, prefix := range []string{"aura://", "serviceComponent://"} {
		if len(desc) >= len(prefix) && strings.EqualFold(desc[:len(prefix)], prefix) {
			return desc[len(prefix):]
		}
	}
	return desc
}

// IsGenericAuraActionName reports whether currentName is a default un-enriched
// Aura request name (including numbered HAR names like "38 - POST aura",
// "38 - POST s sfsites aura", or "POST /aura").
func IsGenericAuraActionName(currentName string) bool {
	trimmed := strings.TrimSpace(currentName)
	if trimmed == "" {
		return true
	}
	if strings.Contains(trimmed, "—") {
		return false
	}
	rest := stripLeadingStepNumberPrefix(trimmed)
	normalized := strings.ToUpper(strings.ReplaceAll(rest, "/", " "))
	normalized = strings.Join(strings.Fields(normalized), " ")
	return normalized == "POST AURA" ||
		(strings.HasPrefix(normalized, "POST ") && strings.HasSuffix(normalized, " AURA"))
}

func stripLeadingStepNumberPrefix(name string) string {
	i := 0
	for i < len(name) && unicode.IsDigit(rune(name[i])) {
		i++
	}
	if i > 0 && strings.HasPrefix(name[i:], " - ") {
		return strings.TrimSpace(name[i+3:])
	}
	return name
}

// EnrichBatchActionName appends the first Aura sub-action's ShortTitle (plus a
// "(+N)" badge for multi-action boxcar batches) to generic Aura action names.
func EnrichBatchActionName(currentName, body string) string {
	if !IsGenericAuraActionName(currentName) {
		return currentName
	}
	actions := ParseEnvelopeActions(body, MaxEnvelopeBytes)
	if len(actions) == 0 {
		return currentName
	}
	firstLabel := actions[0].ShortTitle()
	if firstLabel == "" {
		return currentName
	}
	prefix := strings.TrimSpace(currentName)
	if prefix == "" {
		prefix = "POST /aura"
	}
	label := prefix + " — " + firstLabel
	if len(actions) > 1 {
		label = fmt.Sprintf("%s (+%d)", label, len(actions)-1)
	}
	if len(label) > 120 {
		label = strings.ToValidUTF8(label[:117], "") + "..."
	}
	return label
}
