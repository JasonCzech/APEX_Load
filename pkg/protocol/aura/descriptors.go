package aura

import (
	"fmt"
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

// ParseEnvelopeActions parses the ordered sub-action descriptors and Apex
// target parameters from an Aura POST body (the form Aura posts, or a JSON
// envelope). It shares RequestActions' parser, so a body runtime validation
// rejects (a duplicate or missing message field, too many form fields,
// invalid UTF-8, a malformed envelope, or one over the size, token or action
// bounds) names no actions here either. maxBytes further caps the body when
// it is positive and below MaxEnvelopeBytes.
func ParseEnvelopeActions(body string, maxBytes int) []ActionDescriptor {
	if maxBytes <= 0 || maxBytes > MaxEnvelopeBytes {
		maxBytes = MaxEnvelopeBytes
	}
	if body == "" || len(body) > maxBytes {
		return nil
	}
	actions, issues := parseRequestEnvelope([]byte(body), true)
	if len(issues) > 0 || len(actions) == 0 {
		return nil
	}
	out := make([]ActionDescriptor, 0, len(actions))
	for _, action := range actions {
		namespace, _ := action.params["namespace"].(string)
		class, _ := action.params["classname"].(string)
		method, _ := action.params["method"].(string)
		out = append(out, ActionDescriptor{
			Descriptor:    boundName(strings.TrimSpace(action.descriptor)),
			ApexNamespace: boundName(strings.TrimSpace(namespace)),
			ApexClassname: boundName(strings.TrimSpace(class)),
			ApexMethod:    boundName(strings.TrimSpace(method)),
		})
	}
	return out
}

// IsApexExecute reports whether the action is an ApexActionController call
// that names both its Apex class and method, the rule RequestActions uses.
func (d ActionDescriptor) IsApexExecute() bool {
	return d.Descriptor == apexDescriptor && d.QualifiedApexTarget() != ""
}

// QualifiedApexTarget formats [namespace.]classname.method when both the Apex
// class and method are present, else "".
func (d ActionDescriptor) QualifiedApexTarget() string {
	return apexTarget(d.ApexNamespace, d.ApexClassname, d.ApexMethod)
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
	desc, apex := stripDescriptorScheme(d.Descriptor)
	if desc == "" {
		return ""
	}
	controller, method, hasAction := strings.Cut(desc, "/ACTION$")
	// An apex:// controller is [namespace.]Class, kept whole; other
	// controllers drop their package path.
	if idx := strings.LastIndex(controller, "."); !apex && idx >= 0 && idx+1 < len(controller) {
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
	desc, _ := stripDescriptorScheme(d.Descriptor)
	if desc == "" {
		return ""
	}
	return strings.ReplaceAll(desc, "/ACTION$", ".")
}

// FilterToken formats a descriptor with an optional "[namespace.Class.method]"
// suffix for recording-profile and client-telemetry filtering. The suffix
// comes from an ApexActionController call's classname/method params or, for
// a direct apex://[namespace.]Class/ACTION$method call, from the descriptor
// itself, so the "Class.method" form ShortTitle shows matches either way.
func (d ActionDescriptor) FilterToken() string {
	desc := d.Descriptor
	if target := d.QualifiedApexTarget(); target != "" {
		return desc + "[" + target + "]"
	}
	if target := directApexTarget(desc); target != "" {
		return desc + "[" + target + "]"
	}
	return desc
}

// directApexTarget formats an apex://[namespace.]Class/ACTION$method
// descriptor as [namespace.]Class.method, else "".
func directApexTarget(desc string) string {
	stripped, apex := stripDescriptorScheme(desc)
	if !apex {
		return ""
	}
	controller, method, ok := strings.Cut(stripped, "/ACTION$")
	if !ok || strings.TrimSpace(controller) == "" || strings.TrimSpace(method) == "" {
		return ""
	}
	return controller + "." + method
}

// stripDescriptorScheme drops a descriptor's aura://, serviceComponent:// or
// apex:// scheme and reports whether it was apex:// (a direct @AuraEnabled
// Apex call, whose controller is [namespace.]Class).
func stripDescriptorScheme(desc string) (string, bool) {
	desc = strings.TrimSpace(desc)
	for _, prefix := range []string{"aura://", "serviceComponent://", "apex://"} {
		if len(desc) >= len(prefix) && strings.EqualFold(desc[:len(prefix)], prefix) {
			return desc[len(prefix):], prefix == "apex://"
		}
	}
	return desc, false
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
