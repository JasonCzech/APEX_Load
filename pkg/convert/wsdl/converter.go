package wsdl

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type ImportRequest struct {
	Name      string         `json:"name,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
	Content   string         `json:"content"`
	Options   ConvertOptions `json:"options,omitempty"`
}

type ConvertOptions struct {
	ScriptName         string   `json:"script_name,omitempty"`
	BaseURL            string   `json:"base_url,omitempty"`
	SOAPVersion        string   `json:"soap_version,omitempty"`
	Operations         []string `json:"operations,omitempty"`
	DefaultThinkTimeMs int      `json:"default_think_time_ms,omitempty"`
}

type ConvertStats struct {
	Format              string   `json:"format"`
	Services            int      `json:"services"`
	PortTypes           int      `json:"port_types"`
	Bindings            int      `json:"bindings"`
	OperationsDetected  int      `json:"operations_detected"`
	ConvertedActions    int      `json:"converted_actions"`
	SOAPActionsDetected []string `json:"soap_actions_detected,omitempty"`
}

type ReviewSummary struct {
	ServiceName     string   `json:"service_name,omitempty"`
	TargetNamespace string   `json:"target_namespace,omitempty"`
	BaseURL         string   `json:"base_url,omitempty"`
	SOAPVersion     string   `json:"soap_version,omitempty"`
	Operations      []string `json:"operations,omitempty"`
}

type ConvertResult struct {
	Script   *dsl.Script   `json:"script"`
	Warnings []string      `json:"warnings,omitempty"`
	Stats    ConvertStats  `json:"stats"`
	Summary  ReviewSummary `json:"summary"`
}

type definitions struct {
	XMLName         xml.Name   `xml:"definitions"`
	Name            string     `xml:"name,attr"`
	TargetNamespace string     `xml:"targetNamespace,attr"`
	Services        []service  `xml:"service"`
	PortTypes       []portType `xml:"portType"`
	Bindings        []binding  `xml:"binding"`
}

type service struct {
	Name  string `xml:"name,attr"`
	Ports []port `xml:"port"`
}

type port struct {
	Name      string        `xml:"name,attr"`
	Addresses []soapAddress `xml:"address"`
}

type soapAddress struct {
	Location string `xml:"location,attr"`
}

type portType struct {
	Name       string          `xml:"name,attr"`
	Operations []wsdlOperation `xml:"operation"`
}

type wsdlOperation struct {
	Name string `xml:"name,attr"`
}

type binding struct {
	Name       string             `xml:"name,attr"`
	Operations []bindingOperation `xml:"operation"`
}

type bindingOperation struct {
	Name           string          `xml:"name,attr"`
	SOAPOperations []soapOperation `xml:"operation"`
}

type soapOperation struct {
	SOAPAction string `xml:"soapAction,attr"`
	Style      string `xml:"style,attr"`
}

func Convert(req ImportRequest) (*ConvertResult, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, errors.New("content is required")
	}
	var doc definitions
	if err := xml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, fmt.Errorf("invalid WSDL document: %w", err)
	}
	if doc.XMLName.Local != "definitions" {
		return nil, errors.New("invalid WSDL document: root element must be definitions")
	}

	baseURL, warnings := deriveBaseURL(doc, req.Options.BaseURL)
	soapVersion := firstNonEmpty(req.Options.SOAPVersion, detectSOAPVersion(content), "1.1")
	serviceName := firstNonEmpty(firstServiceName(doc), doc.Name, "Imported_WSDL_Service")
	scriptName := sanitizeName(firstNonEmpty(req.Options.ScriptName, req.Name, serviceName))
	actions := collectOperations(doc, req.Options.Operations)
	if len(actions) == 0 {
		return nil, errors.New("WSDL document contains no supported operations")
	}

	result := &ConvertResult{
		Script: &dsl.Script{
			Name:          scriptName,
			Version:       "1.0.0",
			Protocol:      "soap",
			GeneratedBy:   "apexload-wsdl-converter",
			GeneratedFrom: "wsdl",
			CreatedAt:     time.Now(),
			Config: dsl.ScriptConfig{
				BaseURL: baseURL,
				ThinkTime: dsl.ThinkTimeConfig{
					Type:   "fixed",
					MeanMs: float64(firstPositive(req.Options.DefaultThinkTimeMs, 1000)),
				},
				Pacing:        dsl.PacingConfig{Type: "none"},
				RunLogic:      dsl.RunLogicConfig{Iterations: 0, AutoTransactions: "none"},
				ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false},
				Log:           dsl.LogConfig{Level: "standard"},
				SOAP: dsl.SOAPConfig{
					Version:       soapVersion,
					Namespaces:    map[string]string{"tns": doc.TargetNamespace},
					FaultHandling: dsl.SOAPFaultHandlingConfig{FailOnFault: true},
				},
			},
		},
		Warnings: warnings,
		Stats: ConvertStats{
			Format:             "wsdl",
			Services:           len(doc.Services),
			PortTypes:          len(doc.PortTypes),
			Bindings:           len(doc.Bindings),
			OperationsDetected: len(actions),
		},
		Summary: ReviewSummary{
			ServiceName:     serviceName,
			TargetNamespace: doc.TargetNamespace,
			BaseURL:         baseURL,
			SOAPVersion:     soapVersion,
		},
	}

	for _, op := range actions {
		action := dsl.Action{
			Name:   sanitizeName(op.Name),
			Type:   "request",
			Method: "POST",
			Target: soapActionTarget(baseURL),
			Headers: map[string]string{
				"Content-Type": soapContentType(soapVersion),
			},
			Body: soapEnvelope(doc.TargetNamespace, op.Name),
			SOAP: &dsl.SOAPActionConfig{
				Action:    op.SOAPAction,
				Operation: op.Name,
				BodyMode:  "generated",
			},
			Metadata: map[string]any{
				"wsdl_operation": op.Name,
				"soap_action":    op.SOAPAction,
			},
		}
		if op.SOAPAction != "" {
			action.Headers["SOAPAction"] = op.SOAPAction
			result.Stats.SOAPActionsDetected = append(result.Stats.SOAPActionsDetected, op.SOAPAction)
		}
		result.Script.Actions = append(result.Script.Actions, action)
		result.Summary.Operations = append(result.Summary.Operations, op.Name)
		result.Stats.ConvertedActions++
	}
	sort.Strings(result.Stats.SOAPActionsDetected)
	sort.Strings(result.Summary.Operations)
	if result.Script.Config.BaseURL == "" {
		result.Warnings = append(result.Warnings, "SOAP endpoint URL could not be derived; set options.base_url before saving the script")
	}
	return result, nil
}

type operationInfo struct {
	Name       string
	SOAPAction string
}

func collectOperations(doc definitions, filter []string) []operationInfo {
	allowed := stringSet(filter)
	operationMap := map[string]operationInfo{}
	for _, pt := range doc.PortTypes {
		for _, op := range pt.Operations {
			name := strings.TrimSpace(op.Name)
			if name != "" && (len(allowed) == 0 || allowed[name]) {
				operationMap[name] = operationInfo{Name: name}
			}
		}
	}
	for _, binding := range doc.Bindings {
		for _, bindingOp := range binding.Operations {
			name := strings.TrimSpace(bindingOp.Name)
			if name == "" || (len(allowed) > 0 && !allowed[name]) {
				continue
			}
			info := operationMap[name]
			info.Name = name
			for _, soapOp := range bindingOp.SOAPOperations {
				if strings.TrimSpace(soapOp.SOAPAction) != "" {
					info.SOAPAction = strings.TrimSpace(soapOp.SOAPAction)
					break
				}
			}
			operationMap[name] = info
		}
	}
	names := make([]string, 0, len(operationMap))
	for name := range operationMap {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]operationInfo, 0, len(names))
	for _, name := range names {
		result = append(result, operationMap[name])
	}
	return result
}

func deriveBaseURL(doc definitions, override string) (string, []string) {
	if value := strings.TrimSpace(override); value != "" {
		return value, nil
	}
	for _, svc := range doc.Services {
		for _, port := range svc.Ports {
			for _, address := range port.Addresses {
				if strings.TrimSpace(address.Location) != "" {
					return strings.TrimSpace(address.Location), nil
				}
			}
		}
	}
	return "", []string{"WSDL service address was not provided; imported SOAP actions need options.base_url before execution"}
}

func firstServiceName(doc definitions) string {
	for _, svc := range doc.Services {
		if strings.TrimSpace(svc.Name) != "" {
			return strings.TrimSpace(svc.Name)
		}
	}
	return ""
}

func detectSOAPVersion(content string) string {
	if strings.Contains(content, "schemas.xmlsoap.org/wsdl/soap12") || strings.Contains(content, "soap12") {
		return "1.2"
	}
	return "1.1"
}

func soapContentType(version string) string {
	if version == "1.2" {
		return "application/soap+xml"
	}
	return "text/xml"
}

func soapActionTarget(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Path == "" {
		return "/"
	}
	if parsed.RawQuery != "" {
		return parsed.Path + "?" + parsed.RawQuery
	}
	return parsed.Path
}

func soapEnvelope(targetNamespace, operation string) string {
	namespace := firstNonEmpty(targetNamespace, "urn:service")
	return fmt.Sprintf(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:tns="%s">
  <soapenv:Header/>
  <soapenv:Body>
    <tns:%s>
      <!-- Review request elements from the WSDL/XSD and replace placeholders before replay. -->
    </tns:%s>
  </soapenv:Body>
</soapenv:Envelope>`, namespace, operation, operation)
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result[strings.TrimSpace(value)] = true
		}
	}
	return result
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	value = regexp.MustCompile(`[^A-Za-z0-9_]+`).ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "Imported_WSDL_Script"
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
