package jmeter

// converter_soap.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func applySOAPProtocolHints(script *dsl.Script, warnings *[]string, threadName string) {
	if script == nil || len(script.Actions) == 0 {
		return
	}
	soapCount := 0
	soapVersion := ""
	for _, action := range script.Actions {
		if action.SOAP == nil {
			continue
		}
		soapCount++
		if version := detectSOAPVersionFromAction(action); version == "1.2" {
			soapVersion = "1.2"
		} else if soapVersion == "" {
			soapVersion = "1.1"
		}
	}
	if soapCount == 0 {
		return
	}
	if soapCount != len(script.Actions) {
		*warnings = append(*warnings, fmt.Sprintf("%s: detected %d SOAP-style samplers and %d non-SOAP samplers; keeping HTTP protocol because the imported thread group is mixed", threadName, soapCount, len(script.Actions)-soapCount))
		return
	}
	if soapVersion == "" {
		soapVersion = "1.1"
	}
	script.Protocol = "soap"
	script.Config.SOAP = dsl.SOAPConfig{
		Version: soapVersion,
		FaultHandling: dsl.SOAPFaultHandlingConfig{
			FailOnFault: true,
		},
	}
}

func applySOAPSamplerHints(action *dsl.Action) {
	if action == nil || !isSOAPAction(action) {
		return
	}
	if action.Method == "" || strings.EqualFold(action.Method, "GET") {
		action.Method = "POST"
		action.Type = "POST"
	}
	soapAction := strings.TrimSpace(firstHeaderValue(action.Headers, "SOAPAction"))
	operation := action.Name
	if soapAction != "" {
		operation = strings.Trim(soapAction, `"`)
	}
	bodyMode := "generated"
	if strings.TrimSpace(action.Body) != "" {
		bodyMode = "raw_xml"
	}
	action.SOAP = &dsl.SOAPActionConfig{
		Action:    strings.Trim(soapAction, `"`),
		Operation: operation,
		BodyMode:  bodyMode,
	}
	if action.Headers == nil {
		action.Headers = map[string]string{}
	}
	if firstHeaderValue(action.Headers, "Content-Type") == "" {
		if detectSOAPVersionFromAction(*action) == "1.2" {
			action.Headers["Content-Type"] = "application/soap+xml"
		} else {
			action.Headers["Content-Type"] = "text/xml"
		}
	}
}

func isSOAPAction(action *dsl.Action) bool {
	if action == nil {
		return false
	}
	if strings.TrimSpace(firstHeaderValue(action.Headers, "SOAPAction")) != "" {
		return true
	}
	contentType := strings.ToLower(strings.TrimSpace(firstHeaderValue(action.Headers, "Content-Type")))
	if strings.Contains(contentType, "application/soap+xml") {
		return true
	}
	body := strings.ToLower(strings.TrimSpace(action.Body))
	if strings.Contains(body, ":envelope") || strings.Contains(body, "<envelope") {
		return true
	}
	if strings.Contains(contentType, "text/xml") && body != "" {
		return strings.Contains(body, "soap") || strings.Contains(body, "<envelope")
	}
	return false
}

func detectSOAPVersionFromAction(action dsl.Action) string {
	contentType := strings.ToLower(strings.TrimSpace(firstHeaderValue(action.Headers, "Content-Type")))
	if strings.Contains(contentType, "application/soap+xml") {
		return "1.2"
	}
	body := strings.ToLower(strings.TrimSpace(action.Body))
	if strings.Contains(body, "http://www.w3.org/2003/05/soap-envelope") {
		return "1.2"
	}
	if isSOAPAction(&action) {
		return "1.1"
	}
	return ""
}

func firstHeaderValue(headers map[string]string, key string) string {
	for headerKey, value := range headers {
		if strings.EqualFold(headerKey, key) {
			return value
		}
	}
	return ""
}
