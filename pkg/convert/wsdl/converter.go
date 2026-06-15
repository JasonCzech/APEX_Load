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

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
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
	Script       *dsl.Script        `json:"script"`
	Warnings     []string           `json:"warnings,omitempty"`
	Unsupported  diag.Diagnostics   `json:"unsupported,omitempty"`
	Stats        ConvertStats       `json:"stats"`
	Summary      ReviewSummary      `json:"summary"`
	SourceReview *sourceview.Review `json:"source_review,omitempty"`
}

type definitions struct {
	XMLName         xml.Name     `xml:"definitions"`
	Name            string       `xml:"name,attr"`
	TargetNamespace string       `xml:"targetNamespace,attr"`
	Services        []service    `xml:"service"`
	PortTypes       []portType   `xml:"portType"`
	Bindings        []binding    `xml:"binding"`
	Messages        []message    `xml:"message"`
	Types           []types      `xml:"types"`
	Imports         []wsdlImport `xml:"import"`
}

type wsdlImport struct {
	Namespace string `xml:"namespace,attr"`
	Location  string `xml:"location,attr"`
}

type types struct {
	Schemas []xsdSchema `xml:"schema"`
}

type xsdSchema struct {
	TargetNamespace string           `xml:"targetNamespace,attr"`
	Imports         []xsdImport      `xml:"import"`
	Includes        []xsdInclude     `xml:"include"`
	Elements        []xsdElement     `xml:"element"`
	ComplexTypes    []xsdComplexType `xml:"complexType"`
}

type xsdImport struct {
	Namespace      string `xml:"namespace,attr"`
	SchemaLocation string `xml:"schemaLocation,attr"`
}

type xsdInclude struct {
	SchemaLocation string `xml:"schemaLocation,attr"`
}

type xsdElement struct {
	Name        string          `xml:"name,attr"`
	Type        string          `xml:"type,attr"`
	Ref         string          `xml:"ref,attr"`
	ComplexType *xsdComplexType `xml:"complexType"`
}

type xsdComplexType struct {
	Name       string         `xml:"name,attr"`
	Sequence   *xsdSequence   `xml:"sequence"`
	All        *xsdAll        `xml:"all"`
	Choice     *xsdChoice     `xml:"choice"`
	Attributes []xsdAttribute `xml:"attribute"`
}

type xsdSequence struct {
	Elements []xsdElement `xml:"element"`
}

type xsdAll struct {
	Elements []xsdElement `xml:"element"`
}

type xsdChoice struct {
	Elements []xsdElement `xml:"element"`
}

type xsdAttribute struct {
	Name string `xml:"name,attr"`
	Ref  string `xml:"ref,attr"`
	Type string `xml:"type,attr"`
	Use  string `xml:"use,attr"`
}

type message struct {
	Name  string        `xml:"name,attr"`
	Parts []messagePart `xml:"part"`
}

type messagePart struct {
	Name    string `xml:"name,attr"`
	Element string `xml:"element,attr"`
	Type    string `xml:"type,attr"`
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
	Name   string            `xml:"name,attr"`
	Input  *operationMessage `xml:"input"`
	Output *operationMessage `xml:"output"`
}

type operationMessage struct {
	Message string `xml:"message,attr"`
}

type binding struct {
	Name       string             `xml:"name,attr"`
	Operations []bindingOperation `xml:"operation"`
}

type bindingOperation struct {
	Name           string          `xml:"name,attr"`
	SOAPOperations []soapOperation `xml:"operation"`
	Input          *bindingMessage `xml:"input"`
}

type bindingMessage struct {
	Headers []soapHeaderBinding `xml:"header"`
}

// soapHeaderBinding is a soap:header declaration inside a binding input,
// pointing at the message/part that must travel in the SOAP envelope header.
type soapHeaderBinding struct {
	Message string `xml:"message,attr"`
	Part    string `xml:"part,attr"`
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

	var diagnostics diag.Collector
	sourceFileName := "service.wsdl"
	sourceMappings := make([]sourceview.Mapping, 0)
	addImportDiagnostics(doc, &diagnostics)
	baseURL, warnings := deriveBaseURL(doc, req.Options.BaseURL, &diagnostics)
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
			Body: soapEnvelope(doc, op, &diagnostics),
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
		actionIndex := len(result.Script.Actions)
		result.Script.Actions = append(result.Script.Actions, action)
		sourceMappings = append(sourceMappings, wsdlSourceMapping(sourceFileName, content, op, fmt.Sprintf("actions[%d]", actionIndex), action.Name, "action", "converted", "WSDL operation was converted into a generated SOAP action."))
		result.Summary.Operations = append(result.Summary.Operations, op.Name)
		result.Stats.ConvertedActions++
	}
	sort.Strings(result.Stats.SOAPActionsDetected)
	sort.Strings(result.Summary.Operations)
	if result.Script.Config.BaseURL == "" {
		result.Warnings = append(result.Warnings, "SOAP endpoint URL could not be derived; set options.base_url before saving the script")
	}
	if gap := result.Stats.OperationsDetected - result.Stats.ConvertedActions; gap > 0 {
		diagnostics.Add(diag.Warning, "WSDL completeness guard", "definitions", fmt.Sprintf("%d operation(s) were detected but not converted into SOAP actions", gap))
	}
	result.Unsupported = diagnostics.Items()
	result.SourceReview = sourceview.Build(map[string]string{sourceFileName: content}, sourceMappings)
	return result, nil
}

func wsdlSourceMapping(sourceFile string, content string, op operationInfo, targetPath string, targetName string, targetKind string, status string, notes string) sourceview.Mapping {
	line := wsdlOperationLine(content, op.Name)
	return sourceview.Mapping{
		SourceFile: sourceFile,
		StartLine:  line,
		EndLine:    line,
		Function:   "operation " + op.Name,
		TargetPath: targetPath,
		TargetName: targetName,
		TargetKind: targetKind,
		Status:     status,
		Notes:      notes,
	}
}

func wsdlOperationLine(content string, operationName string) int {
	for _, needle := range []string{`name="` + operationName + `"`, `name='` + operationName + `'`} {
		if offset := strings.Index(content, needle); offset >= 0 {
			return sourceview.LineForOffset(content, offset)
		}
	}
	return 1
}

type operationInfo struct {
	Name         string
	SOAPAction   string
	InputMessage string
	Headers      []soapHeaderBinding
}

func collectOperations(doc definitions, filter []string) []operationInfo {
	allowed := stringSet(filter)
	operationMap := map[string]operationInfo{}
	for _, pt := range doc.PortTypes {
		for _, op := range pt.Operations {
			name := strings.TrimSpace(op.Name)
			if name != "" && (len(allowed) == 0 || allowed[name]) {
				info := operationInfo{Name: name}
				if op.Input != nil {
					info.InputMessage = strings.TrimSpace(op.Input.Message)
				}
				operationMap[name] = info
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
			if bindingOp.Input != nil {
				info.Headers = append(info.Headers, bindingOp.Input.Headers...)
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

func deriveBaseURL(doc definitions, override string, diagnostics *diag.Collector) (string, []string) {
	if value := strings.TrimSpace(override); value != "" {
		return value, nil
	}
	locations := []string{}
	for _, svc := range doc.Services {
		for _, port := range svc.Ports {
			for _, address := range port.Addresses {
				if strings.TrimSpace(address.Location) != "" {
					locations = append(locations, strings.TrimSpace(address.Location))
				}
			}
		}
	}
	if len(locations) > 0 {
		if len(locations) > 1 {
			diagnostics.Add(diag.Info, "WSDL service endpoints", "definitions/service", "using first endpoint "+locations[0]+"; additional endpoints preserved for review: "+strings.Join(locations[1:], ", "))
		}
		return locations[0], nil
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

func soapEnvelope(doc definitions, op operationInfo, diagnostics *diag.Collector) string {
	namespace := firstNonEmpty(doc.TargetNamespace, "urn:service")
	bodyName, children, ok := soapBodySkeleton(doc, op, diagnostics)
	if bodyName == "" {
		bodyName = op.Name
	}
	if !ok {
		children = "      <!-- Review request elements from the WSDL/XSD and replace placeholders before replay. -->"
	}
	header := soapHeaderSkeleton(doc, op, diagnostics)
	return fmt.Sprintf(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:tns="%s">
%s
  <soapenv:Body>
    <tns:%s>
%s
    </tns:%s>
  </soapenv:Body>
</soapenv:Envelope>`, namespace, header, bodyName, children, bodyName)
}

// soapHeaderSkeleton renders the soap:header parts declared on the binding's
// input message into the envelope header, so required headers (sessions,
// credentials, routing) are visible and editable instead of silently dropped.
func soapHeaderSkeleton(doc definitions, op operationInfo, diagnostics *diag.Collector) string {
	if len(op.Headers) == 0 {
		return "  <soapenv:Header/>"
	}
	location := "operation " + op.Name
	lines := make([]string, 0, len(op.Headers))
	for _, headerBinding := range op.Headers {
		part, ok := messagePartByName(doc, headerBinding.Message, headerBinding.Part)
		if !ok {
			diagnostics.Add(diag.Warning, "WSDL SOAP header", location,
				"soap:header part "+firstNonEmpty(headerBinding.Part, headerBinding.Message)+" could not be resolved; add the header manually before replay")
			continue
		}
		headerName := localName(firstNonEmpty(part.Element, part.Name))
		if headerName == "" {
			continue
		}
		if element, found := findXSDElement(doc, headerName); found {
			if fields, ok := xsdFieldsForElement(doc, element); ok {
				lines = append(lines, "    <tns:"+headerName+">")
				lines = append(lines, renderXSDFieldSet(doc, fields, diagnostics, location, 3))
				lines = append(lines, "    </tns:"+headerName+">")
				diagnostics.Add(diag.Info, "WSDL SOAP header", location, "soap:header "+headerName+" was rendered into the envelope header")
				continue
			}
		}
		lines = append(lines, "    <tns:"+headerName+">"+placeholderForXSDType(part.Type, headerName)+"</tns:"+headerName+">")
		diagnostics.Add(diag.Info, "WSDL SOAP header", location, "soap:header "+headerName+" was rendered with a placeholder value")
	}
	if len(lines) == 0 {
		return "  <soapenv:Header/>"
	}
	return "  <soapenv:Header>\n" + strings.Join(lines, "\n") + "\n  </soapenv:Header>"
}

// messagePartByName resolves a message part referenced by a soap:header
// binding. An empty part name selects the message's first part.
func messagePartByName(doc definitions, messageRef, partName string) (messagePart, bool) {
	name := localName(messageRef)
	if name == "" {
		return messagePart{}, false
	}
	for _, msg := range doc.Messages {
		if msg.Name != name || len(msg.Parts) == 0 {
			continue
		}
		if strings.TrimSpace(partName) == "" {
			return msg.Parts[0], true
		}
		for _, part := range msg.Parts {
			if part.Name == partName {
				return part, true
			}
		}
	}
	return messagePart{}, false
}

func soapBodySkeleton(doc definitions, op operationInfo, diagnostics *diag.Collector) (string, string, bool) {
	location := "operation " + op.Name
	part, ok := inputMessagePart(doc, op.InputMessage)
	if !ok {
		diagnostics.Add(diag.Warning, "WSDL input message", location, "input message/part could not be resolved; generated a placeholder SOAP body")
		return op.Name, "", false
	}
	bodyName := localName(firstNonEmpty(part.Element, part.Name, op.Name))
	if bodyName == "" {
		bodyName = op.Name
	}
	element, found := findXSDElement(doc, bodyName)
	if !found && part.Type != "" {
		bodyName = localName(firstNonEmpty(part.Name, op.Name))
		if fields, ok := xsdFieldsForType(doc, localName(part.Type)); ok {
			return bodyName, renderXSDFieldSet(doc, fields, diagnostics, location, 3), true
		}
	}
	if !found {
		diagnostics.Add(diag.Warning, "WSDL XSD element", location, "message part "+bodyName+" has no inlined XSD element/type; generated a placeholder SOAP body")
		return bodyName, "", false
	}
	fields, ok := xsdFieldsForElement(doc, element)
	if !ok {
		diagnostics.Add(diag.Warning, "WSDL XSD type", location, "element "+bodyName+" has no supported complexType model; generated a placeholder SOAP body")
		return bodyName, "", false
	}
	return bodyName, renderXSDFieldSet(doc, fields, diagnostics, location, 3), true
}

func inputMessagePart(doc definitions, messageRef string) (messagePart, bool) {
	name := localName(messageRef)
	if name == "" {
		return messagePart{}, false
	}
	for _, msg := range doc.Messages {
		if msg.Name != name || len(msg.Parts) == 0 {
			continue
		}
		return msg.Parts[0], true
	}
	return messagePart{}, false
}

func findXSDElement(doc definitions, name string) (xsdElement, bool) {
	name = localName(name)
	for _, t := range doc.Types {
		for _, schema := range t.Schemas {
			for _, element := range schema.Elements {
				if localName(firstNonEmpty(element.Name, element.Ref)) == name {
					return element, true
				}
			}
		}
	}
	return xsdElement{}, false
}

type xsdFieldSet struct {
	Elements   []xsdElement
	Model      string
	ChoiceName string
	Attributes []xsdAttribute
}

func xsdFieldsForElement(doc definitions, element xsdElement) (xsdFieldSet, bool) {
	if element.ComplexType != nil {
		return xsdFieldsForComplexType(*element.ComplexType)
	}
	if element.Type != "" {
		return xsdFieldsForType(doc, localName(element.Type))
	}
	return xsdFieldSet{}, false
}

func xsdFieldsForType(doc definitions, typeName string) (xsdFieldSet, bool) {
	for _, t := range doc.Types {
		for _, schema := range t.Schemas {
			for _, ct := range schema.ComplexTypes {
				if ct.Name == typeName {
					return xsdFieldsForComplexType(ct)
				}
			}
		}
	}
	return xsdFieldSet{}, false
}

func xsdFieldsForComplexType(ct xsdComplexType) (xsdFieldSet, bool) {
	set := xsdFieldSet{Model: "sequence", Attributes: ct.Attributes}
	switch {
	case ct.Sequence != nil:
		set.Elements = ct.Sequence.Elements
	case ct.All != nil:
		set.Elements = ct.All.Elements
		set.Model = "all"
	case ct.Choice != nil:
		set.Elements = firstChoiceElement(ct.Choice.Elements)
		set.Model = "choice"
		if len(set.Elements) > 0 {
			set.ChoiceName = localName(firstNonEmpty(set.Elements[0].Name, set.Elements[0].Ref))
		}
	default:
		return set, len(set.Attributes) > 0
	}
	return set, len(set.Elements) > 0 || len(set.Attributes) > 0
}

func firstChoiceElement(elements []xsdElement) []xsdElement {
	for _, element := range elements {
		if strings.TrimSpace(firstNonEmpty(element.Name, element.Ref)) != "" {
			return []xsdElement{element}
		}
	}
	return nil
}

func renderXSDFieldSet(doc definitions, set xsdFieldSet, diagnostics *diag.Collector, location string, depth int) string {
	addXSDModelDiagnostics(set, diagnostics, location)
	return renderXSDFields(doc, set.Elements, diagnostics, location, depth)
}

func addXSDModelDiagnostics(set xsdFieldSet, diagnostics *diag.Collector, location string) {
	switch set.Model {
	case "all":
		diagnostics.Add(diag.Info, "WSDL XSD all", location, "xsd:all elements were rendered in schema order; validate optional and unordered payload semantics before replay")
	case "choice":
		choice := firstNonEmpty(set.ChoiceName, "first available branch")
		diagnostics.Add(diag.Warning, "WSDL XSD choice", location, "xsd:choice was represented with "+choice+"; choose the correct branch before replay")
	}
	if len(set.Attributes) > 0 {
		diagnostics.Add(diag.Warning, "WSDL XSD attributes", location, "attributes "+xsdAttributeNames(set.Attributes)+" are not rendered in the generated SOAP skeleton; add required attributes before replay")
	}
}

func xsdAttributeNames(attributes []xsdAttribute) string {
	names := make([]string, 0, len(attributes))
	for _, attribute := range attributes {
		name := localName(firstNonEmpty(attribute.Name, attribute.Ref))
		if name == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(attribute.Use), "required") {
			name += " (required)"
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "declared by the complex type"
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// maxXSDRenderDepth bounds XSD skeleton recursion. renderXSDFields recurses
// through type/element references with no cycle detection; a self-referential
// complexType (e.g. `<element name="child" type="tns:Node"/>` inside type Node)
// would otherwise recurse forever and overflow the stack — a fatal runtime throw
// that recover() cannot catch, crashing the whole controller. Entry callers start
// at depth 3, so this allows deeply-but-finitely nested schemas while breaking
// cycles. The cap is high enough that real schemas are unaffected.
const maxXSDRenderDepth = 64

func renderXSDFields(doc definitions, fields []xsdElement, diagnostics *diag.Collector, location string, depth int) string {
	if len(fields) == 0 {
		return ""
	}
	if depth >= maxXSDRenderDepth {
		diagnostics.Add(diag.Warning, "WSDL XSD type", location, fmt.Sprintf("stopped rendering at depth %d; the schema is recursive or extremely deep — fill in nested levels manually before replay", depth))
		return ""
	}
	indent := strings.Repeat("  ", depth)
	lines := make([]string, 0, len(fields))
	for _, field := range fields {
		name := localName(firstNonEmpty(field.Name, field.Ref))
		if name == "" {
			continue
		}
		if field.ComplexType != nil {
			nested, ok := xsdFieldsForComplexType(*field.ComplexType)
			if !ok {
				diagnostics.Add(diag.Warning, "WSDL XSD type", location, "inline complexType for child element "+name+" has no supported content model; generated an empty SOAP element")
				lines = append(lines, indent+"<tns:"+name+"></tns:"+name+">")
				continue
			}
			lines = append(lines, indent+"<tns:"+name+">")
			lines = append(lines, renderXSDFieldSet(doc, nested, diagnostics, location, depth+1))
			lines = append(lines, indent+"</tns:"+name+">")
			continue
		}
		if refElement, ok := findXSDElement(doc, field.Ref); ok && strings.TrimSpace(field.Ref) != "" {
			if nested, ok := xsdFieldsForElement(doc, refElement); ok {
				lines = append(lines, indent+"<tns:"+name+">")
				lines = append(lines, renderXSDFieldSet(doc, nested, diagnostics, location, depth+1))
				lines = append(lines, indent+"</tns:"+name+">")
				continue
			}
		}
		if fieldsForType, ok := xsdFieldsForType(doc, localName(field.Type)); ok {
			lines = append(lines, indent+"<tns:"+name+">")
			lines = append(lines, renderXSDFieldSet(doc, fieldsForType, diagnostics, location, depth+1))
			lines = append(lines, indent+"</tns:"+name+">")
			continue
		}
		if strings.TrimSpace(field.Ref) != "" {
			diagnostics.Add(diag.Warning, "WSDL XSD ref", location, "referenced child element "+field.Ref+" was represented with a placeholder value")
		}
		lines = append(lines, indent+"<tns:"+name+">"+placeholderForXSDType(field.Type, name)+"</tns:"+name+">")
	}
	return strings.Join(lines, "\n")
}

func placeholderForXSDType(typeName string, fieldName string) string {
	switch strings.ToLower(localName(typeName)) {
	case "int", "integer", "long", "short", "decimal", "double", "float":
		return "1"
	case "boolean":
		return "true"
	case "datetime", "date":
		// fn.timestamp.iso8601 is the runtime-supported timestamp function;
		// fn.now() does not exist in the engine.
		return "{{fn.timestamp.iso8601}}"
	default:
		return "{{data.wsdl." + sanitizeName(fieldName) + "}}"
	}
}

func addImportDiagnostics(doc definitions, diagnostics *diag.Collector) {
	for _, item := range doc.Imports {
		diagnostics.Add(diag.Warning, "WSDL import", "definitions", "referenced WSDL import "+firstNonEmpty(item.Location, item.Namespace)+" was not followed; inline or import it separately")
	}
	for _, t := range doc.Types {
		for _, schema := range t.Schemas {
			for _, item := range schema.Imports {
				diagnostics.Add(diag.Warning, "XSD import", "types/schema", "referenced XSD import "+firstNonEmpty(item.SchemaLocation, item.Namespace)+" was not followed; inline or import it separately")
			}
			for _, item := range schema.Includes {
				diagnostics.Add(diag.Warning, "XSD include", "types/schema", "referenced XSD include "+item.SchemaLocation+" was not followed; inline or import it separately")
			}
		}
	}
}

func localName(value string) string {
	value = strings.TrimSpace(value)
	if idx := strings.LastIndex(value, ":"); idx >= 0 {
		value = value[idx+1:]
	}
	return value
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
