package collectoragent

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
)

const (
	serviceBusAtomNamespace             = "http://www.w3.org/2005/Atom"
	serviceBusConnectNamespace          = "http://schemas.microsoft.com/netservices/2010/10/servicebus/connect"
	serviceBusCountNamespace            = "http://schemas.microsoft.com/netservices/2011/06/servicebus"
	serviceBusInstanceNamespace         = "http://www.w3.org/2001/XMLSchema-instance"
	serviceBusDescriptionMaxBodyBytes   = 256 << 10
	serviceBusDescriptionMaxDepth       = 32
	serviceBusDescriptionMaxTokens      = 8192
	serviceBusDescriptionMaxScalarBytes = 32
)

type serviceBusXMLScalar struct {
	depth    int
	name     string
	nilValue bool
	text     []byte
	limit    int
}

// Parse token by token; ignored description/authorization metadata is bounded
// but never retained. No generated XML conversion may manufacture an absent 0.
func decodeServiceBusManagementDescription(ctx context.Context, body []byte, route serviceBusManagementRoute) (analysis.ServiceBusRuntime, error) {
	if err := ctx.Err(); err != nil {
		return analysis.ServiceBusRuntime{}, err
	}
	if len(body) < 1 || len(body) > serviceBusDescriptionMaxBodyBytes {
		return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "budget_exhausted"}
	}
	if route.entity.ServiceBus == nil || route.entity.Validate() != nil || route.subscription != (route.entity.ServiceBus.Queue == "") {
		return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "permission_denied"}
	}
	result := serviceBusMissingRuntime(route.subscription)
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true // No external entity/DTD expansion or encoding reader.
	var stack []xml.Name
	var scalar *serviceBusXMLScalar
	seen := map[string]bool{}
	rootSeen, rootClosed, feed, contentSeen, descriptionSeen, declarationSeen := false, false, false, false, false, false
	countDetailsNilDepth := 0
	tokens := 0
	for {
		if err := ctx.Err(); err != nil {
			return analysis.ServiceBusRuntime{}, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
		}
		tokens++
		if tokens > serviceBusDescriptionMaxTokens {
			return analysis.ServiceBusRuntime{}, &managementBudgetError{}
		}
		switch t := token.(type) {
		case xml.Directive:
			return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
		case xml.ProcInst:
			if rootSeen || declarationSeen || t.Target != "xml" || !serviceBusXMLDeclaration(t.Inst) {
				return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
			}
			declarationSeen = true
		case xml.StartElement:
			if rootClosed || scalar != nil || countDetailsNilDepth > 0 {
				return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
			}
			if len(stack) >= serviceBusDescriptionMaxDepth {
				return analysis.ServiceBusRuntime{}, &managementBudgetError{}
			}
			if !serviceBusXMLNamespace(t.Name.Space) {
				return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
			}
			if serviceBusXMLDuplicateAttributes(t.Attr) || !serviceBusXMLAttributeNamespaces(t.Attr) {
				return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
			}
			stack = append(stack, t.Name)
			depth := len(stack)
			if depth == 1 {
				if rootSeen || t.Name.Space != serviceBusAtomNamespace || t.Name.Local != "entry" && t.Name.Local != "feed" {
					return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
				}
				rootSeen, feed = true, t.Name.Local == "feed"
				continue
			}
			if feed {
				continue
			} // A feed is missing evidence; children are never followed.
			if t.Name.Local == "entry" || t.Name.Local == "feed" {
				return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
			}
			if depth == 2 && (t.Name.Local == "id" || t.Name.Local == "title") {
				if t.Name.Space != serviceBusAtomNamespace || seen[t.Name.Local] {
					return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
				}
				seen[t.Name.Local] = true
				scalar = &serviceBusXMLScalar{depth: depth, name: t.Name.Local, limit: 2048}
				continue
			}
			if t.Name.Local == "content" {
				if depth != 2 || t.Name.Space != serviceBusAtomNamespace || contentSeen {
					return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
				}
				contentSeen = true
				for _, attr := range t.Attr {
					if attr.Name.Space == "" && attr.Name.Local == "type" && attr.Value != "application/xml" {
						return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
					}
				}
				continue
			}
			if strings.HasSuffix(t.Name.Local, "Description") && depth == 3 && stack[1].Local == "content" {
				expected := "QueueDescription"
				if route.subscription {
					expected = "SubscriptionDescription"
				}
				if t.Name.Space != serviceBusConnectNamespace || t.Name.Local != expected || descriptionSeen {
					return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
				}
				descriptionSeen = true
				continue
			}
			inDescription := depth >= 4 && descriptionSeen && stack[1].Local == "content" && stack[2].Space == serviceBusConnectNamespace && (stack[2].Local == "QueueDescription" || stack[2].Local == "SubscriptionDescription")
			if !inDescription {
				continue
			}
			if depth == 4 && t.Name.Local == "CountDetails" {
				if t.Name.Space != serviceBusConnectNamespace || seen["CountDetails"] {
					return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
				}
				seen["CountDetails"] = true
				nilValue, err := serviceBusXMLNil(t.Attr)
				if err != nil {
					return analysis.ServiceBusRuntime{}, err
				}
				if nilValue {
					countDetailsNilDepth = depth
				}
				continue
			}
			known := depth == 4 && (t.Name.Local == "MessageCount" || t.Name.Local == "SizeInBytes") || depth == 5 && stack[3].Local == "CountDetails" && serviceBusCountField(t.Name.Local)
			if known {
				if depth == 4 && t.Name.Space != serviceBusConnectNamespace || depth == 5 && t.Name.Space != serviceBusConnectNamespace && t.Name.Space != serviceBusCountNamespace || seen[t.Name.Local] {
					return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
				}
				seen[t.Name.Local] = true
				nilValue, err := serviceBusXMLNil(t.Attr)
				if err != nil {
					return analysis.ServiceBusRuntime{}, err
				}
				scalar = &serviceBusXMLScalar{depth: depth, name: t.Name.Local, nilValue: nilValue, limit: serviceBusDescriptionMaxScalarBytes}
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
			}
			if scalar != nil && scalar.depth == len(stack) {
				if err := serviceBusApplyScalar(&result, scalar, route); err != nil {
					return analysis.ServiceBusRuntime{}, err
				}
				clear(scalar.text)
				scalar = nil
			}
			if countDetailsNilDepth == len(stack) {
				countDetailsNilDepth = 0
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				rootClosed = true
			}
		case xml.CharData:
			if countDetailsNilDepth > 0 && len(bytes.TrimSpace(t)) > 0 {
				return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
			}
			if scalar != nil {
				if len(t) > scalar.limit-len(scalar.text) {
					return analysis.ServiceBusRuntime{}, &managementBudgetError{}
				}
				scalar.text = append(scalar.text, t...)
			} else if len(stack) == 0 && len(bytes.TrimSpace(t)) != 0 {
				return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return analysis.ServiceBusRuntime{}, err
	}
	if !rootSeen || !rootClosed || len(stack) != 0 || scalar != nil || !feed && (!contentSeen || !descriptionSeen) {
		return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
	}
	if err := result.Validate(); err != nil {
		return analysis.ServiceBusRuntime{}, &managementObservationError{reason: "invalid"}
	}
	return result, nil
}

func serviceBusXMLNamespace(namespace string) bool {
	return namespace == serviceBusAtomNamespace || namespace == serviceBusConnectNamespace || namespace == serviceBusCountNamespace
}
func serviceBusXMLDuplicateAttributes(attrs []xml.Attr) bool {
	seen := map[xml.Name]bool{}
	for _, attr := range attrs {
		if seen[attr.Name] {
			return true
		}
		seen[attr.Name] = true
	}
	return false
}
func serviceBusXMLNil(attrs []xml.Attr) (bool, error) {
	nilValue := false
	for _, attr := range attrs {
		if attr.Name.Space == serviceBusInstanceNamespace && attr.Name.Local == "nil" {
			switch attr.Value {
			case "true", "1":
				nilValue = true
			case "false", "0":
			default:
				return false, &managementObservationError{reason: "invalid"}
			}
		}
	}
	return nilValue, nil
}
func serviceBusXMLDeclaration(instruction []byte) bool {
	// Parse only the declaration's bounded attribute grammar through an inert
	// element. Unknown encodings/attributes and embedded markup are rejected.
	if len(instruction) > 256 || bytes.ContainsAny(instruction, "<>") {
		return false
	}
	decoder := xml.NewDecoder(bytes.NewReader(append(append([]byte("<declaration "), instruction...), []byte("/>")...)))
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	element, ok := token.(xml.StartElement)
	if !ok || serviceBusXMLDuplicateAttributes(element.Attr) {
		return false
	}
	version := false
	for _, attr := range element.Attr {
		if attr.Name.Space != "" {
			return false
		}
		switch attr.Name.Local {
		case "version":
			version = attr.Value == "1.0"
		case "encoding":
			if !strings.EqualFold(attr.Value, "UTF-8") {
				return false
			}
		case "standalone":
			if attr.Value != "yes" && attr.Value != "no" {
				return false
			}
		default:
			return false
		}
	}
	return version
}
func serviceBusCountField(name string) bool {
	switch name {
	case "ActiveMessageCount", "ScheduledMessageCount", "DeadLetterMessageCount", "TransferMessageCount", "TransferDeadLetterMessageCount":
		return true
	}
	return false
}
func serviceBusMissingRuntime(subscription bool) analysis.ServiceBusRuntime {
	missing := analysis.ManagementInteger{State: analysis.ManagementStateMissing, Reason: "not_observed"}
	unsupported := analysis.ManagementInteger{State: analysis.ManagementStateUnsupported, Reason: "unsupported"}
	result := analysis.ServiceBusRuntime{Total: missing, Active: missing, Scheduled: missing, DeadLetter: missing, Transfer: missing, TransferDeadLetter: missing, SizeBytes: missing, Deferred: unsupported}
	if subscription {
		result.SizeBytes, result.Scheduled = unsupported, unsupported
	}
	return result
}
func serviceBusApplyScalar(result *analysis.ServiceBusRuntime, scalar *serviceBusXMLScalar, route serviceBusManagementRoute) error {
	text := strings.TrimSpace(string(scalar.text))
	if scalar.name == "id" {
		u, err := url.Parse(text)
		if err != nil || u.User != nil || u.Opaque != "" || u.Fragment != "" || strings.Contains(text, "#") || strings.Contains(text, "%") || u.ForceQuery && u.RawQuery == "" {
			return &managementObservationError{reason: "identity_mismatch"}
		}
		origin, _, err := canonicalServiceBusOrigin(u.Scheme + "://" + u.Host)
		if err != nil || !strings.EqualFold(origin+u.Path, route.resourceURI) || u.RawQuery != "" && u.RawQuery != "api-version=2021-05" {
			return &managementObservationError{reason: "identity_mismatch"}
		}
		return nil
	}
	if scalar.name == "title" {
		expected := route.entity.ServiceBus.Queue
		if route.subscription {
			expected = route.entity.ServiceBus.Subscription
		}
		if !strings.EqualFold(text, expected) {
			return &managementObservationError{reason: "identity_mismatch"}
		}
		return nil
	}
	if scalar.nilValue {
		if text != "" {
			return &managementObservationError{reason: "invalid"}
		}
		return nil
	}
	if text == "" || len(text) > serviceBusDescriptionMaxScalarBytes {
		return &managementObservationError{reason: "invalid"}
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return &managementObservationError{reason: "invalid"}
		}
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value < 0 {
		return &managementObservationError{reason: "invalid"}
	}
	observed, err := analysis.ObservedManagementInteger(value)
	if err != nil {
		return &managementObservationError{reason: "invalid"}
	}
	switch scalar.name {
	case "MessageCount":
		result.Total = observed
	case "SizeInBytes":
		if !route.subscription {
			result.SizeBytes = observed
		}
	case "ActiveMessageCount":
		result.Active = observed
	case "ScheduledMessageCount":
		if !route.subscription {
			result.Scheduled = observed
		}
	case "DeadLetterMessageCount":
		result.DeadLetter = observed
	case "TransferMessageCount":
		result.Transfer = observed
	case "TransferDeadLetterMessageCount":
		result.TransferDeadLetter = observed
	}
	return nil
}

func serviceBusXMLAttributeNamespaces(attrs []xml.Attr) bool {
	for _, attr := range attrs {
		if attr.Name.Local == "nil" && attr.Name.Space != serviceBusInstanceNamespace && attr.Name.Space != "xmlns" {
			return false
		}
		switch attr.Name.Space {
		case "", "xmlns", serviceBusInstanceNamespace, "http://www.w3.org/XML/1998/namespace":
		default:
			return false
		}
	}
	return true
}
