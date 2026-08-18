package wsdl

import (
	"encoding/xml"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/archiveingest"
	"github.com/jasonczech/apexload/pkg/scrub"
)

type ImportRequest struct {
	Name      string            `json:"name,omitempty"`
	ProjectID string            `json:"project_id,omitempty"`
	Content   string            `json:"content"`
	RootFile  string            `json:"root_file,omitempty"`
	Files     map[string]string `json:"files,omitempty"`
	Options   ConvertOptions    `json:"options,omitempty"`
}

type ConvertOptions struct {
	ScriptName         string   `json:"script_name,omitempty"`
	BaseURL            string   `json:"base_url,omitempty"`
	SOAPVersion        string   `json:"soap_version,omitempty"`
	ServiceName        string   `json:"service_name,omitempty"`
	PortName           string   `json:"port_name,omitempty"`
	BindingName        string   `json:"binding_name,omitempty"`
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
	ServiceName         string                 `json:"service_name,omitempty"`
	PortName            string                 `json:"port_name,omitempty"`
	BindingName         string                 `json:"binding_name,omitempty"`
	TargetNamespace     string                 `json:"target_namespace,omitempty"`
	BaseURL             string                 `json:"base_url,omitempty"`
	Endpoint            string                 `json:"endpoint,omitempty"`
	SOAPVersion         string                 `json:"soap_version,omitempty"`
	BindingStyle        string                 `json:"binding_style,omitempty"`
	BindingUse          string                 `json:"binding_use,omitempty"`
	BindingTransport    string                 `json:"binding_transport,omitempty"`
	Operations          []string               `json:"operations,omitempty"`
	BindingAlternatives []BindingChoiceSummary `json:"binding_alternatives,omitempty"`
}

type ConvertResult struct {
	Script             *dsl.Script                    `json:"script"`
	Warnings           []string                       `json:"warnings,omitempty"`
	Unsupported        diag.Diagnostics               `json:"unsupported,omitempty"`
	Findings           []importreview.Finding         `json:"findings,omitempty"`
	SecretRequirements []credentialpolicy.Requirement `json:"secret_requirements,omitempty"`
	Readiness          importreview.Readiness         `json:"readiness"`
	Stats              ConvertStats                   `json:"stats"`
	Summary            ReviewSummary                  `json:"summary"`
	SourceReview       *sourceview.Review             `json:"source_review,omitempty"`
}

type BindingChoiceSummary struct {
	ServiceName    string `json:"service_name,omitempty"`
	PortName       string `json:"port_name,omitempty"`
	BindingName    string `json:"binding_name,omitempty"`
	PortTypeName   string `json:"port_type_name,omitempty"`
	Endpoint       string `json:"endpoint,omitempty"`
	SOAPVersion    string `json:"soap_version,omitempty"`
	Style          string `json:"style,omitempty"`
	Use            string `json:"use,omitempty"`
	Transport      string `json:"transport,omitempty"`
	OperationCount int    `json:"operation_count,omitempty"`
}

type definitions struct {
	XMLName           xml.Name          `xml:"definitions"`
	Attrs             []xml.Attr        `xml:",any,attr"`
	Name              string            `xml:"name,attr"`
	TargetNamespace   string            `xml:"targetNamespace,attr"`
	Services          []service         `xml:"service"`
	PortTypes         []portType        `xml:"portType"`
	Bindings          []binding         `xml:"binding"`
	Messages          []message         `xml:"message"`
	Types             []types           `xml:"types"`
	Imports           []wsdlImport      `xml:"import"`
	NamespaceBindings map[string]string `xml:"-"`
}

type wsdlImport struct {
	Namespace string `xml:"namespace,attr"`
	Location  string `xml:"location,attr"`
}

type types struct {
	Schemas []xsdSchema `xml:"schema"`
}

type xsdSchema struct {
	Attrs              []xml.Attr        `xml:",any,attr"`
	TargetNamespace    string            `xml:"targetNamespace,attr"`
	ElementFormDefault string            `xml:"elementFormDefault,attr"`
	Imports            []xsdImport       `xml:"import"`
	Includes           []xsdInclude      `xml:"include"`
	Elements           []xsdElement      `xml:"element"`
	SimpleTypes        []xsdSimpleType   `xml:"simpleType"`
	ComplexTypes       []xsdComplexType  `xml:"complexType"`
	NamespaceBindings  map[string]string `xml:"-"`
	ResolvedSchemas    []xsdSchema       `xml:"-"`
	IncludedFromSchema bool              `xml:"-"`
}

type xsdImport struct {
	Namespace      string `xml:"namespace,attr"`
	SchemaLocation string `xml:"schemaLocation,attr"`
}

type xsdInclude struct {
	SchemaLocation string `xml:"schemaLocation,attr"`
}

type xsdElement struct {
	Attrs             []xml.Attr        `xml:",any,attr"`
	Name              string            `xml:"name,attr"`
	Type              string            `xml:"type,attr"`
	Ref               string            `xml:"ref,attr"`
	Form              string            `xml:"form,attr"`
	MinOccurs         string            `xml:"minOccurs,attr"`
	MaxOccurs         string            `xml:"maxOccurs,attr"`
	Nillable          string            `xml:"nillable,attr"`
	Default           string            `xml:"default,attr"`
	Fixed             string            `xml:"fixed,attr"`
	ComplexType       *xsdComplexType   `xml:"complexType"`
	SimpleType        *xsdSimpleType    `xml:"simpleType"`
	NamespaceBindings map[string]string `xml:"-"`
}

type xsdSimpleType struct {
	Name        string          `xml:"name,attr"`
	Restriction *xsdRestriction `xml:"restriction"`
	List        *xsdList        `xml:"list"`
	Union       *xsdUnion       `xml:"union"`
}

type xsdRestriction struct {
	Base         string           `xml:"base,attr"`
	Enumerations []xsdEnumeration `xml:"enumeration"`
}

type xsdEnumeration struct {
	Value string `xml:"value,attr"`
}

type xsdList struct {
	ItemType string `xml:"itemType,attr"`
}

type xsdUnion struct {
	MemberTypes string `xml:"memberTypes,attr"`
}

type xsdComplexType struct {
	Attrs             []xml.Attr        `xml:",any,attr"`
	Name              string            `xml:"name,attr"`
	Sequence          *xsdSequence      `xml:"sequence"`
	All               *xsdAll           `xml:"all"`
	Choice            *xsdChoice        `xml:"choice"`
	Attributes        []xsdAttribute    `xml:"attribute"`
	NamespaceBindings map[string]string `xml:"-"`
}

type xsdSequence struct {
	Attrs             []xml.Attr        `xml:",any,attr"`
	Elements          []xsdElement      `xml:"element"`
	NamespaceBindings map[string]string `xml:"-"`
}

type xsdAll struct {
	Attrs             []xml.Attr        `xml:",any,attr"`
	Elements          []xsdElement      `xml:"element"`
	NamespaceBindings map[string]string `xml:"-"`
}

type xsdChoice struct {
	Attrs             []xml.Attr        `xml:",any,attr"`
	Elements          []xsdElement      `xml:"element"`
	NamespaceBindings map[string]string `xml:"-"`
}

type xsdAttribute struct {
	Name string `xml:"name,attr"`
	Ref  string `xml:"ref,attr"`
	Type string `xml:"type,attr"`
	Use  string `xml:"use,attr"`
}

type message struct {
	Attrs               []xml.Attr    `xml:",any,attr"`
	Name                string        `xml:"name,attr"`
	Parts               []messagePart `xml:"part"`
	DefinitionNamespace string        `xml:"-"`
}

type messagePart struct {
	Attrs             []xml.Attr        `xml:",any,attr"`
	Name              string            `xml:"name,attr"`
	Element           string            `xml:"element,attr"`
	Type              string            `xml:"type,attr"`
	NamespaceBindings map[string]string `xml:"-"`
}

type service struct {
	Attrs []xml.Attr `xml:",any,attr"`
	Name  string     `xml:"name,attr"`
	Ports []port     `xml:"port"`
}

type port struct {
	Attrs             []xml.Attr        `xml:",any,attr"`
	Name              string            `xml:"name,attr"`
	Binding           string            `xml:"binding,attr"`
	Addresses         []soapAddress     `xml:"address"`
	NamespaceBindings map[string]string `xml:"-"`
}

type soapAddress struct {
	XMLName  xml.Name `xml:"address"`
	Location string   `xml:"location,attr"`
}

type portType struct {
	Attrs               []xml.Attr        `xml:",any,attr"`
	Name                string            `xml:"name,attr"`
	Operations          []wsdlOperation   `xml:"operation"`
	DefinitionNamespace string            `xml:"-"`
	NamespaceBindings   map[string]string `xml:"-"`
}

type wsdlOperation struct {
	Attrs  []xml.Attr        `xml:",any,attr"`
	Name   string            `xml:"name,attr"`
	Input  *operationMessage `xml:"input"`
	Output *operationMessage `xml:"output"`
}

type operationMessage struct {
	Attrs             []xml.Attr        `xml:",any,attr"`
	Message           string            `xml:"message,attr"`
	NamespaceBindings map[string]string `xml:"-"`
}

type binding struct {
	Attrs               []xml.Attr         `xml:",any,attr"`
	Name                string             `xml:"name,attr"`
	Type                string             `xml:"type,attr"`
	SOAPBindings        []soapBinding      `xml:"binding"`
	Operations          []bindingOperation `xml:"operation"`
	DefinitionNamespace string             `xml:"-"`
	NamespaceBindings   map[string]string  `xml:"-"`
}

type soapBinding struct {
	XMLName   xml.Name `xml:"binding"`
	Style     string   `xml:"style,attr"`
	Transport string   `xml:"transport,attr"`
}

type bindingOperation struct {
	Attrs          []xml.Attr      `xml:",any,attr"`
	Name           string          `xml:"name,attr"`
	SOAPOperations []soapOperation `xml:"operation"`
	Input          *bindingMessage `xml:"input"`
}

type bindingMessage struct {
	Attrs   []xml.Attr          `xml:",any,attr"`
	Bodies  []soapBodyBinding   `xml:"body"`
	Headers []soapHeaderBinding `xml:"header"`
}

type soapBodyBinding struct {
	XMLName   xml.Name `xml:"body"`
	Use       string   `xml:"use,attr"`
	Namespace string   `xml:"namespace,attr"`
	Parts     string   `xml:"parts,attr"`
}

// soapHeaderBinding is a soap:header declaration inside a binding input,
// pointing at the message/part that must travel in the SOAP envelope header.
type soapHeaderBinding struct {
	Attrs             []xml.Attr        `xml:",any,attr"`
	XMLName           xml.Name          `xml:"header"`
	Message           string            `xml:"message,attr"`
	Part              string            `xml:"part,attr"`
	Use               string            `xml:"use,attr"`
	Namespace         string            `xml:"namespace,attr"`
	NamespaceBindings map[string]string `xml:"-"`
}

type soapOperation struct {
	XMLName    xml.Name `xml:"operation"`
	SOAPAction string   `xml:"soapAction,attr"`
	Style      string   `xml:"style,attr"`
}

const (
	maxWSDLZipEntries  = 2000
	maxWSDLBundleBytes = 20 << 20
	maxWSDLBundleDepth = 32
)

func FilesFromZip(data []byte) (string, map[string]string, error) {
	extracted, err := archiveingest.ReadZIP(data, archiveingest.Options{Limits: archiveingest.Limits{
		MaxEntries:    maxWSDLZipEntries,
		MaxEntryBytes: maxWSDLBundleBytes,
		MaxTotalBytes: maxWSDLBundleBytes,
		MaxDepth:      maxWSDLBundleDepth,
	}})
	if err != nil {
		return "", nil, err
	}
	files := make(map[string]string, len(extracted))
	for _, file := range extracted {
		files[file.Name] = string(file.Data)
	}
	root := pickMainWSDL(files)
	if root == "" {
		return "", nil, errors.New("no .wsdl entry found in archive")
	}
	return root, files, nil
}

func prepareWSDLDocument(req ImportRequest, diagnostics *diag.Collector) (definitions, string, string, map[string]string, error) {
	if len(req.Content) > maxWSDLBundleBytes {
		return definitions{}, "", "", nil, fmt.Errorf(
			"WSDL content exceeds the maximum size of %d bytes",
			maxWSDLBundleBytes,
		)
	}
	content := strings.TrimSpace(req.Content)
	sourceFileName, err := archiveingest.ValidateFilePath(
		convertutil.FirstNonEmpty(req.RootFile, "service.wsdl"),
		maxWSDLBundleDepth,
	)
	if err != nil {
		return definitions{}, "", "", nil, fmt.Errorf("invalid WSDL root_file: %w", err)
	}
	sourceFiles, err := normalizeSourceFiles(req.Files)
	if err != nil {
		return definitions{}, "", "", nil, err
	}
	if len(sourceFiles) > 0 {
		root := ""
		if strings.TrimSpace(req.RootFile) != "" {
			root = sourceFileName
		} else {
			root = pickMainWSDL(sourceFiles)
		}
		if root == "" {
			return definitions{}, "", "", nil, errors.New("no .wsdl entry found in import files")
		}
		sourceFileName = root
		if content == "" {
			content = strings.TrimSpace(sourceFiles[root])
		} else {
			totalBytes := len(content)
			for name, sourceContent := range sourceFiles {
				if name != root {
					if len(sourceContent) > maxWSDLBundleBytes-totalBytes {
						return definitions{}, "", "", nil, fmt.Errorf(
							"WSDL import files exceed the maximum total size of %d bytes",
							maxWSDLBundleBytes,
						)
					}
					totalBytes += len(sourceContent)
				}
			}
			sourceFiles[root] = content
		}
	}
	if content == "" {
		return definitions{}, "", sourceFileName, sourceFiles, errors.New("content is required")
	}
	if len(sourceFiles) == 0 {
		sourceFiles = map[string]string{sourceFileName: content}
	}
	var doc definitions
	if err := xml.Unmarshal([]byte(content), &doc); err != nil {
		return definitions{}, "", sourceFileName, sourceFiles, fmt.Errorf("invalid WSDL document: %w", err)
	}
	initializeDefinitionNamespaceContexts(&doc)
	resolveLocalWSDLImports(&doc, sourceFileName, sourceFiles, diagnostics, map[string]bool{})
	return doc, content, sourceFileName, sourceFiles, nil
}

func normalizeSourceFiles(files map[string]string) (map[string]string, error) {
	if len(files) == 0 {
		return nil, nil
	}
	if len(files) > maxWSDLZipEntries {
		return nil, fmt.Errorf(
			"WSDL import has too many files (%d > maximum %d)",
			len(files),
			maxWSDLZipEntries,
		)
	}
	names := make([]string, 0, len(files))
	totalBytes := 0
	for name, content := range files {
		names = append(names, name)
		if len(content) > maxWSDLBundleBytes-totalBytes {
			return nil, fmt.Errorf("WSDL import files exceed the maximum total size of %d bytes", maxWSDLBundleBytes)
		}
		totalBytes += len(content)
	}
	sort.Strings(names)
	normalized, err := archiveingest.ValidateFilePaths(names, maxWSDLBundleDepth)
	if err != nil {
		return nil, fmt.Errorf("invalid WSDL import files: %w", err)
	}
	out := make(map[string]string, len(files))
	for index, name := range names {
		out[normalized[index]] = files[name]
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func pickMainWSDL(files map[string]string) string {
	best := ""
	bestDepth := 1 << 30
	for name := range files {
		if !strings.HasSuffix(strings.ToLower(name), ".wsdl") {
			continue
		}
		depth := strings.Count(name, "/")
		base := strings.ToLower(path.Base(name))
		if base == "service.wsdl" || base == "root.wsdl" {
			depth = -1
		}
		if depth < bestDepth || (depth == bestDepth && (best == "" || name < best)) {
			bestDepth = depth
			best = name
		}
	}
	return best
}

func resolveLocalWSDLImports(doc *definitions, sourceFile string, files map[string]string, diagnostics *diag.Collector, seen map[string]bool) {
	if doc == nil || len(files) == 0 {
		return
	}
	key := "wsdl:" + sourceFile
	if seen[key] {
		return
	}
	seen[key] = true
	unresolved := doc.Imports[:0]
	for _, item := range doc.Imports {
		location := strings.TrimSpace(item.Location)
		resolved, ok := resolveBundleFile(sourceFile, location, files)
		if !ok {
			unresolved = append(unresolved, item)
			continue
		}
		var imported definitions
		if err := xml.Unmarshal([]byte(files[resolved]), &imported); err != nil {
			diagnostics.Add(diag.Warning, "WSDL import", "definitions", "referenced WSDL import "+location+" could not be parsed: "+err.Error())
			unresolved = append(unresolved, item)
			continue
		}
		initializeDefinitionNamespaceContexts(&imported)
		resolveLocalWSDLImports(&imported, resolved, files, diagnostics, seen)
		mergeDefinitions(doc, imported)
		diagnostics.Add(diag.Info, "WSDL import", "definitions", "resolved local WSDL import "+location)
	}
	doc.Imports = unresolved
	for typeIndex := range doc.Types {
		for schemaIndex := range doc.Types[typeIndex].Schemas {
			resolveLocalSchemaImports(&doc.Types[typeIndex].Schemas[schemaIndex], sourceFile, files, diagnostics, seen)
		}
	}
}

func resolveLocalSchemaImports(schema *xsdSchema, sourceFile string, files map[string]string, diagnostics *diag.Collector, seen map[string]bool) {
	if schema == nil || len(files) == 0 {
		return
	}
	unresolvedImports := schema.Imports[:0]
	for _, item := range schema.Imports {
		location := strings.TrimSpace(item.SchemaLocation)
		resolved, ok := resolveBundleFile(sourceFile, location, files)
		if !ok {
			unresolvedImports = append(unresolvedImports, item)
			continue
		}
		imported, ok := parseBundleSchema(resolved, files, diagnostics, seen)
		if !ok {
			unresolvedImports = append(unresolvedImports, item)
			continue
		}
		schema.ResolvedSchemas = append(schema.ResolvedSchemas, imported)
		diagnostics.Add(diag.Info, "XSD import", "types/schema", "resolved local XSD import "+location)
	}
	schema.Imports = unresolvedImports

	unresolvedIncludes := schema.Includes[:0]
	for _, item := range schema.Includes {
		location := strings.TrimSpace(item.SchemaLocation)
		resolved, ok := resolveBundleFile(sourceFile, location, files)
		if !ok {
			unresolvedIncludes = append(unresolvedIncludes, item)
			continue
		}
		included, ok := parseBundleSchema(resolved, files, diagnostics, seen)
		if !ok {
			unresolvedIncludes = append(unresolvedIncludes, item)
			continue
		}
		included.IncludedFromSchema = true
		adoptIncludedSchemaNamespace(&included, schema.TargetNamespace)
		schema.ResolvedSchemas = append(schema.ResolvedSchemas, included)
		diagnostics.Add(diag.Info, "XSD include", "types/schema", "resolved local XSD include "+location)
	}
	schema.Includes = unresolvedIncludes
}

// adoptIncludedSchemaNamespace applies XML Schema's chameleon-include rule to
// the whole include-only subtree. parseBundleSchema resolves a child schema's
// own includes before its parent namespace is known, so assigning only the
// immediate child would leave transitive no-namespace types unresolvable.
// Imported schemas are deliberately not traversed or rewritten.
func adoptIncludedSchemaNamespace(schema *xsdSchema, inheritedNamespace string) {
	if schema == nil {
		return
	}
	effectiveNamespace := strings.TrimSpace(schema.TargetNamespace)
	if effectiveNamespace == "" {
		effectiveNamespace = strings.TrimSpace(inheritedNamespace)
		schema.TargetNamespace = effectiveNamespace
	}
	for index := range schema.ResolvedSchemas {
		if !schema.ResolvedSchemas[index].IncludedFromSchema {
			continue
		}
		adoptIncludedSchemaNamespace(&schema.ResolvedSchemas[index], effectiveNamespace)
	}
}

func parseBundleSchema(sourceFile string, files map[string]string, diagnostics *diag.Collector, seen map[string]bool) (xsdSchema, bool) {
	key := "xsd:" + sourceFile
	if seen[key] {
		return xsdSchema{}, false
	}
	seen[key] = true
	var schema xsdSchema
	if err := xml.Unmarshal([]byte(files[sourceFile]), &schema); err != nil {
		diagnostics.Add(diag.Warning, "XSD import", "types/schema", "referenced XSD "+sourceFile+" could not be parsed: "+err.Error())
		return xsdSchema{}, false
	}
	initializeSchemaNamespaceContext(&schema, nil)
	resolveLocalSchemaImports(&schema, sourceFile, files, diagnostics, seen)
	return schema, true
}

func mergeDefinitions(dst *definitions, src definitions) {
	dst.Services = append(dst.Services, src.Services...)
	dst.PortTypes = append(dst.PortTypes, src.PortTypes...)
	dst.Bindings = append(dst.Bindings, src.Bindings...)
	dst.Messages = append(dst.Messages, src.Messages...)
	dst.Types = append(dst.Types, src.Types...)
	dst.Imports = append(dst.Imports, src.Imports...)
}

func initializeDefinitionNamespaceContexts(doc *definitions) {
	if doc == nil {
		return
	}
	doc.NamespaceBindings = mergeNamespaceBindings(nil, doc.Attrs)
	definitionNamespace := strings.TrimSpace(doc.TargetNamespace)
	for typeIndex := range doc.Types {
		for schemaIndex := range doc.Types[typeIndex].Schemas {
			initializeSchemaNamespaceContext(&doc.Types[typeIndex].Schemas[schemaIndex], doc.NamespaceBindings)
		}
	}
	for messageIndex := range doc.Messages {
		message := &doc.Messages[messageIndex]
		message.DefinitionNamespace = definitionNamespace
		messageNamespaces := mergeNamespaceBindings(doc.NamespaceBindings, message.Attrs)
		for partIndex := range message.Parts {
			message.Parts[partIndex].NamespaceBindings = mergeNamespaceBindings(messageNamespaces, message.Parts[partIndex].Attrs)
		}
	}
	for serviceIndex := range doc.Services {
		service := &doc.Services[serviceIndex]
		serviceNamespaces := mergeNamespaceBindings(doc.NamespaceBindings, service.Attrs)
		for portIndex := range service.Ports {
			service.Ports[portIndex].NamespaceBindings = mergeNamespaceBindings(serviceNamespaces, service.Ports[portIndex].Attrs)
		}
	}
	for portTypeIndex := range doc.PortTypes {
		portType := &doc.PortTypes[portTypeIndex]
		portType.DefinitionNamespace = definitionNamespace
		portType.NamespaceBindings = mergeNamespaceBindings(doc.NamespaceBindings, portType.Attrs)
		for operationIndex := range portType.Operations {
			operation := &portType.Operations[operationIndex]
			operationNamespaces := mergeNamespaceBindings(portType.NamespaceBindings, operation.Attrs)
			if operation.Input != nil {
				operation.Input.NamespaceBindings = mergeNamespaceBindings(operationNamespaces, operation.Input.Attrs)
			}
			if operation.Output != nil {
				operation.Output.NamespaceBindings = mergeNamespaceBindings(operationNamespaces, operation.Output.Attrs)
			}
		}
	}
	for bindingIndex := range doc.Bindings {
		binding := &doc.Bindings[bindingIndex]
		binding.DefinitionNamespace = definitionNamespace
		binding.NamespaceBindings = mergeNamespaceBindings(doc.NamespaceBindings, binding.Attrs)
		for operationIndex := range binding.Operations {
			operation := &binding.Operations[operationIndex]
			operationNamespaces := mergeNamespaceBindings(binding.NamespaceBindings, operation.Attrs)
			if operation.Input == nil {
				continue
			}
			inputNamespaces := mergeNamespaceBindings(operationNamespaces, operation.Input.Attrs)
			for headerIndex := range operation.Input.Headers {
				header := &operation.Input.Headers[headerIndex]
				header.NamespaceBindings = mergeNamespaceBindings(inputNamespaces, header.Attrs)
			}
		}
	}
}

func initializeSchemaNamespaceContext(schema *xsdSchema, inherited map[string]string) {
	if schema == nil {
		return
	}
	schema.NamespaceBindings = mergeNamespaceBindings(inherited, schema.Attrs)
	initializeXSDElementNamespaceContexts(schema.Elements, schema.NamespaceBindings)
	for index := range schema.ComplexTypes {
		initializeXSDComplexTypeElementContexts(&schema.ComplexTypes[index], schema.NamespaceBindings)
	}
}

func initializeXSDElementNamespaceContexts(elements []xsdElement, inherited map[string]string) {
	for index := range elements {
		element := &elements[index]
		element.NamespaceBindings = mergeNamespaceBindings(inherited, element.Attrs)
		if element.ComplexType != nil {
			initializeXSDComplexTypeElementContexts(element.ComplexType, element.NamespaceBindings)
		}
	}
}

func initializeXSDComplexTypeElementContexts(complexType *xsdComplexType, inherited map[string]string) {
	if complexType == nil {
		return
	}
	complexType.NamespaceBindings = mergeNamespaceBindings(inherited, complexType.Attrs)
	if complexType.Sequence != nil {
		complexType.Sequence.NamespaceBindings = mergeNamespaceBindings(complexType.NamespaceBindings, complexType.Sequence.Attrs)
		initializeXSDElementNamespaceContexts(complexType.Sequence.Elements, complexType.Sequence.NamespaceBindings)
	}
	if complexType.All != nil {
		complexType.All.NamespaceBindings = mergeNamespaceBindings(complexType.NamespaceBindings, complexType.All.Attrs)
		initializeXSDElementNamespaceContexts(complexType.All.Elements, complexType.All.NamespaceBindings)
	}
	if complexType.Choice != nil {
		complexType.Choice.NamespaceBindings = mergeNamespaceBindings(complexType.NamespaceBindings, complexType.Choice.Attrs)
		initializeXSDElementNamespaceContexts(complexType.Choice.Elements, complexType.Choice.NamespaceBindings)
	}
}

func mergeNamespaceBindings(inherited map[string]string, attrs []xml.Attr) map[string]string {
	bindings := cloneNamespaceBindings(inherited)
	for _, attr := range attrs {
		switch {
		case attr.Name.Space == "xmlns":
			bindings[attr.Name.Local] = strings.TrimSpace(attr.Value)
		case attr.Name.Space == "" && attr.Name.Local == "xmlns":
			bindings[""] = strings.TrimSpace(attr.Value)
		}
	}
	return bindings
}

func cloneNamespaceBindings(source map[string]string) map[string]string {
	if len(source) == 0 {
		return map[string]string{}
	}
	clone := make(map[string]string, len(source))
	for prefix, namespace := range source {
		clone[prefix] = namespace
	}
	return clone
}

func resolveBundleFile(sourceFile string, location string, files map[string]string) (string, bool) {
	location = strings.TrimSpace(location)
	if location == "" || isRemoteLocation(location) {
		return "", false
	}
	candidates := []string{cleanBundlePath(location)}
	if sourceFile != "" {
		candidates = append(candidates, cleanBundlePath(path.Join(path.Dir(sourceFile), location)))
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := files[candidate]; ok {
			return candidate, true
		}
	}
	return "", false
}

func cleanBundlePath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "/")
	if value == "" || value == "." {
		return ""
	}
	clean := path.Clean(value)
	if clean == "." {
		return ""
	}
	return clean
}

func isRemoteLocation(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(lower, "://") || strings.HasPrefix(lower, "urn:")
}

func Convert(req ImportRequest) (*ConvertResult, error) {
	var diagnostics diag.Collector
	doc, content, sourceFileName, sourceFiles, err := prepareWSDLDocument(req, &diagnostics)
	if err != nil {
		return nil, err
	}
	addImportDiagnostics(doc, &diagnostics)

	content = strings.TrimSpace(content)
	if content == "" {
		return nil, errors.New("content is required")
	}
	if doc.XMLName.Local != "definitions" {
		if doc.XMLName.Local == "description" {
			return nil, errors.New("WSDL 2.0 documents are not supported; import a WSDL 1.1 definitions document")
		}
		return nil, errors.New("invalid WSDL document: root element must be definitions")
	}

	sourceMappings := make([]sourceview.Mapping, 0)
	selected, warnings, err := selectBinding(doc, req.Options, &diagnostics)
	if err != nil {
		return nil, err
	}
	baseURL := convertutil.FirstNonEmpty(req.Options.BaseURL, selected.Endpoint)
	scriptBaseURL := baseURL
	if strings.TrimSpace(scriptBaseURL) == "" {
		scriptBaseURL = soapActionTarget("")
	}
	soapVersion := convertutil.FirstNonEmpty(req.Options.SOAPVersion, selected.SOAPVersion, detectSOAPVersion(content), "1.1")
	serviceName := convertutil.FirstNonEmpty(firstServiceName(doc), doc.Name, "Imported_WSDL_Service")
	if selected.ServiceName != "" {
		serviceName = selected.ServiceName
	}
	scriptName := sanitizeName(convertutil.FirstNonEmpty(req.Options.ScriptName, req.Name, serviceName))
	actions := selected.Operations
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
				BaseURL: scriptBaseURL,
				ThinkTime: dsl.ThinkTimeConfig{
					Type:   "fixed",
					MeanMs: float64(firstPositive(req.Options.DefaultThinkTimeMs, 1000)),
				},
				Pacing:   dsl.PacingConfig{Type: "none"},
				RunLogic: dsl.RunLogicConfig{Iterations: 0, AutoTransactions: "none"},
				// Unset: the scenario continue-on-error policy decides.
				ErrorHandling: dsl.ErrorHandlingConfig{},
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
			ServiceName:         serviceName,
			PortName:            selected.PortName,
			BindingName:         selected.BindingName,
			TargetNamespace:     doc.TargetNamespace,
			BaseURL:             baseURL,
			Endpoint:            selected.Endpoint,
			SOAPVersion:         soapVersion,
			BindingStyle:        selected.Style,
			BindingUse:          selected.Use,
			BindingTransport:    selected.Transport,
			BindingAlternatives: bindingChoiceSummaries(selected.Alternatives),
		},
	}

	sourceLineIndex := sourceview.NewLineIndex(content)
	requests := make([]requestimport.RequestInput, 0, len(actions))
	projectedSecrets := make([]wsdlProjectedSecret, 0)
	nameRegistry := credentialpolicy.NewNameRegistry()
	for _, op := range actions {
		target := soapActionTarget(baseURL)
		actionName := sanitizeName(op.Name)
		actionPath := fmt.Sprintf("actions[%d]", len(requests))
		sourcePointer := "operation/" + op.Name
		var operationDiagnostics diag.Collector
		body := []byte(soapEnvelope(doc, op, soapVersion, &operationDiagnostics))
		for _, item := range operationDiagnostics.Items() {
			diagnostics.Add(item.Severity, item.Element, item.Location, item.Detail)
		}
		body, secrets, credentialFindings, preservedTargets, err := finalizeWSDLXMLBody(body, wsdlCredentialProjectionOptions{
			RequestKey: actionName,
			SourcePath: sourceFileName,
			Pointer:    sourcePointer + "/body",
			Registry:   nameRegistry,
		})
		if err != nil {
			return nil, fmt.Errorf("project WSDL operation %q credentials: %w", op.Name, err)
		}
		projectedSecrets = append(projectedSecrets, secrets...)

		metadata := map[string]any{
			"wsdl_operation": op.Name,
			"soap_action":    op.SOAPAction,
			"wsdl_service":   selected.ServiceName,
			"wsdl_port":      selected.PortName,
			"wsdl_binding":   selected.BindingName,
			"soap_version":   soapVersion,
		}
		metadata = requestimport.MergePreservedCredentialTargetsMetadata(
			metadata,
			preservedTargets,
		)
		reviewFindings := wsdlOperationReviewFindings(op, sourceFileName, sourcePointer)
		reviewFindings = append(reviewFindings, wsdlBindingReviewFindings(selected, doc, sourceFileName, sourcePointer)...)
		reviewFindings = append(reviewFindings, wsdlSkeletonReviewFindings(operationDiagnostics.Items(), sourceFileName, sourcePointer)...)
		if strings.TrimSpace(baseURL) == "" {
			reviewFindings = append(reviewFindings, wsdlMissingEndpointReviewFinding(sourceFileName, sourcePointer))
		}
		reviewFindings = append(reviewFindings, credentialFindings...)
		reviewFindings = importreview.NormalizeFindings(reviewFindings)
		executionFindings := wsdlExecutionGatingFindings(reviewFindings)
		if len(executionFindings) > 0 {
			metadata["execution_blocker"] = "import_review_required"
			metadata["import_review"] = wsdlImportReviewMetadata(executionFindings, sourceFileName, sourcePointer)
		}
		requirements := make([]credentialpolicy.Requirement, 0, len(secrets))
		for _, secret := range secrets {
			requirements = append(requirements, secret.Requirement)
		}
		requests = append(requests, requestimport.RequestInput{
			Phase: requestimport.PhaseActions,
			Request: requestimport.Request{
				Key:    actionName,
				Name:   actionName,
				Method: "POST",
				Target: target,
				Body: requestimport.Body{
					Kind:     requestimport.BodyXML,
					Payload:  body,
					Encoding: requestimport.BodyEncodingText,
					Source: credentialpolicy.SourceRef{
						Format:  "wsdl",
						Path:    sourceFileName,
						Pointer: sourcePointer + "/body",
					},
				},
				Metadata: metadata,
				Source: credentialpolicy.SourceRef{
					Format:  "wsdl",
					Path:    sourceFileName,
					Pointer: sourcePointer,
				},
			},
			Overlay: requestimport.ActionOverlay{
				SOAP: &dsl.SOAPActionConfig{
					Action:    op.SOAPAction,
					Operation: op.Name,
					BodyMode:  "generated",
				},
			},
			Findings:     reviewFindings,
			Requirements: requirements,
		})
		if op.SOAPAction != "" {
			result.Stats.SOAPActionsDetected = append(result.Stats.SOAPActionsDetected, op.SOAPAction)
		}
		sourceMappings = append(sourceMappings, wsdlSourceMapping(sourceFileName, content, sourceLineIndex, op, actionPath, actionName, "action", "converted", "WSDL operation was converted into a generated SOAP action."))
		result.Summary.Operations = append(result.Summary.Operations, op.Name)
		result.Stats.ConvertedActions++
	}
	draft, err := requestimport.Project(result.Script, requests, requestimport.DefaultProjectOptions())
	if err != nil {
		return nil, fmt.Errorf("finalize WSDL requests: %w", err)
	}
	finalized, err := draft.Finalize(requestimport.FinalizeOptions{
		UnresolvedBindings: len(projectedSecrets),
	})
	if err != nil {
		return nil, fmt.Errorf("finalize WSDL script: %w", err)
	}
	if len(finalized.ValidationIssues) > 0 {
		return nil, fmt.Errorf("WSDL finalizer invariant failed: %s", strings.Join(finalized.ValidationIssues, "; "))
	}
	result.Script = finalized.Script
	result.Findings = finalized.Findings
	for index := range finalized.Requirements {
		requirement := &finalized.Requirements[index]
		if requirement.Value != "" {
			projectedSecrets = append(projectedSecrets, wsdlProjectedSecret{
				Requirement: *requirement,
				SourceValues: uniqueNonEmptyStrings(
					requirement.Value,
					decodeWSDLXMLScalar(requirement.Value),
				),
			})
		}
		requirement.Value = ""
		requirement.SourceValueAvailable = false
	}
	result.SecretRequirements = finalized.Requirements
	result.Readiness = finalized.Readiness
	result.Summary.BaseURL = result.Script.Config.BaseURL
	result.Summary.Endpoint = scrub.ValueURL(result.Summary.Endpoint, scrub.Policy{})
	for index := range result.Summary.BindingAlternatives {
		result.Summary.BindingAlternatives[index].Endpoint = scrub.ValueURL(
			result.Summary.BindingAlternatives[index].Endpoint,
			scrub.Policy{},
		)
	}
	for _, item := range finalized.Diagnostics {
		diagnostics.Add(item.Severity, item.Element, item.Location, item.Detail)
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
	if len(sourceFiles) == 0 {
		sourceFiles = map[string]string{sourceFileName: content}
	}
	reviewFiles := redactWSDLProjectedSecrets(sourceFiles, projectedSecrets)
	redactedReviewFiles := make(map[string]struct{})
	for path, reviewContent := range reviewFiles {
		if reviewContent != sourceFiles[path] {
			redactedReviewFiles[path] = struct{}{}
		}
	}
	result.SourceReview = sourceview.Build(reviewFiles, sourceMappings)
	if result.SourceReview != nil {
		for index := range result.SourceReview.SourceFiles {
			if _, redacted := redactedReviewFiles[result.SourceReview.SourceFiles[index].Path]; redacted {
				result.SourceReview.SourceFiles[index].Redacted = true
			}
		}
	}
	return result, nil
}

func wsdlSourceMapping(sourceFile string, content string, lineIndex sourceview.LineIndex, op operationInfo, targetPath string, targetName string, targetKind string, status string, notes string) sourceview.Mapping {
	line := wsdlOperationLine(content, lineIndex, op.Name)
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

func wsdlOperationLine(content string, lineIndex sourceview.LineIndex, operationName string) int {
	for _, needle := range []string{`name="` + operationName + `"`, `name='` + operationName + `'`} {
		if offset := strings.Index(content, needle); offset >= 0 {
			return lineIndex.LineForOffset(offset)
		}
	}
	return 1
}

type operationInfo struct {
	Name                     string
	SOAPAction               string
	InputMessage             string
	InputMessageNamespaces   map[string]string
	Style                    string
	BodyUse                  string
	BodyNamespace            string
	BodyParts                []string
	Headers                  []soapHeaderBinding
	BindingOperationResolved bool
}

type bindingChoice struct {
	ServiceName  string
	PortName     string
	BindingName  string
	PortTypeName string
	Endpoint     string
	SOAPVersion  string
	Style        string
	Use          string
	Transport    string
	Operations   []operationInfo
	Alternatives []bindingChoice
}

func selectBinding(doc definitions, opts ConvertOptions, diagnostics *diag.Collector) (bindingChoice, []string, error) {
	candidates := collectBindingChoices(doc, opts, diagnostics)
	if len(candidates) == 0 {
		return bindingChoice{}, nil, errors.New("WSDL document contains no SOAP-capable service port or binding")
	}
	filtered := make([]bindingChoice, 0, len(candidates))
	for _, candidate := range candidates {
		if !matchesSelector(candidate.ServiceName, opts.ServiceName) {
			continue
		}
		if !matchesSelector(candidate.PortName, opts.PortName) {
			continue
		}
		if !matchesSelector(candidate.BindingName, opts.BindingName) {
			continue
		}
		filtered = append(filtered, candidate)
	}
	if len(filtered) == 0 {
		return bindingChoice{}, nil, fmt.Errorf("no SOAP binding matched service=%q port=%q binding=%q", opts.ServiceName, opts.PortName, opts.BindingName)
	}
	selected := filtered[0]
	selected.Alternatives = candidates
	warnings := []string(nil)
	if selected.Endpoint == "" && strings.TrimSpace(opts.BaseURL) == "" {
		warnings = append(warnings, "WSDL service address was not provided; imported SOAP actions need options.base_url before execution")
	}
	if len(candidates) > 1 {
		diagnostics.Add(diag.Warning, "WSDL service endpoints", "definitions/service", "multiple SOAP service ports or bindings were found; using "+bindingChoiceLabel(selected)+" by default")
	}
	return selected, warnings, nil
}

func collectBindingChoices(doc definitions, opts ConvertOptions, diagnostics *diag.Collector) []bindingChoice {
	choices := []bindingChoice{}
	for _, svc := range doc.Services {
		for _, p := range svc.Ports {
			b, ok := findBinding(doc, p.Binding, p.NamespaceBindings)
			if !ok {
				if strings.TrimSpace(p.Binding) != "" {
					diagnostics.Add(diag.Warning, "WSDL port binding", "service "+svc.Name, "port "+p.Name+" references binding "+p.Binding+" which could not be resolved")
				}
				continue
			}
			choice := bindingChoiceFor(doc, opts, svc.Name, p.Name, firstSOAPAddress(p), b, diagnostics)
			if len(choice.Operations) > 0 {
				choices = append(choices, choice)
			}
		}
	}
	if len(choices) > 0 {
		return choices
	}
	for _, b := range doc.Bindings {
		choice := bindingChoiceFor(doc, opts, "", "", "", b, diagnostics)
		if len(choice.Operations) > 0 {
			choices = append(choices, choice)
		}
	}
	if len(choices) > 0 {
		return choices
	}
	for _, pt := range doc.PortTypes {
		operations := collectPortTypeOperations(pt, opts.Operations)
		if len(operations) == 0 {
			continue
		}
		diagnostics.Add(diag.Warning, "WSDL binding", "portType "+pt.Name, "no SOAP binding was found; generated review skeletons from portType operations")
		choices = append(choices, bindingChoice{
			PortTypeName: pt.Name,
			Endpoint:     strings.TrimSpace(opts.BaseURL),
			SOAPVersion:  strings.TrimSpace(opts.SOAPVersion),
			Style:        "document",
			Use:          "literal",
			Operations:   operations,
		})
	}
	return choices
}

func bindingChoiceFor(doc definitions, opts ConvertOptions, serviceName, portName, endpoint string, b binding, diagnostics *diag.Collector) bindingChoice {
	soapBinding := firstSOAPBinding(b)
	if soapBinding.XMLName.Local == "" {
		return bindingChoice{}
	}
	style := strings.ToLower(strings.TrimSpace(soapBinding.Style))
	if style == "" {
		style = "document"
	}
	use := "literal"
	transport := strings.TrimSpace(soapBinding.Transport)
	version := convertutil.FirstNonEmpty(soapVersionFromXMLName(soapBinding.XMLName), "1.1")
	portTypeName := localName(b.Type)
	operations := collectBindingOperations(doc, b, opts.Operations, style, use, diagnostics)
	for _, op := range operations {
		if op.BodyUse != "" {
			use = op.BodyUse
			break
		}
	}
	return bindingChoice{
		ServiceName:  strings.TrimSpace(serviceName),
		PortName:     strings.TrimSpace(portName),
		BindingName:  strings.TrimSpace(b.Name),
		PortTypeName: portTypeName,
		Endpoint:     strings.TrimSpace(endpoint),
		SOAPVersion:  version,
		Style:        style,
		Use:          use,
		Transport:    transport,
		Operations:   operations,
	}
}

func collectBindingOperations(doc definitions, b binding, filter []string, defaultStyle string, defaultUse string, diagnostics *diag.Collector) []operationInfo {
	allowed := stringSet(filter)
	pt, ok := findPortType(doc, b.Type, b.NamespaceBindings)
	if !ok {
		if strings.TrimSpace(b.Type) != "" {
			diagnostics.Add(diag.Warning, "WSDL binding", "binding "+b.Name, "portType "+b.Type+" could not be resolved")
		}
		return nil
	}
	bindingOps := map[string]bindingOperation{}
	for _, op := range b.Operations {
		if strings.TrimSpace(op.Name) != "" {
			bindingOps[strings.TrimSpace(op.Name)] = op
		}
	}
	operations := make([]operationInfo, 0, len(pt.Operations))
	for _, ptOp := range pt.Operations {
		name := strings.TrimSpace(ptOp.Name)
		if name == "" || (len(allowed) > 0 && !allowed[name]) {
			continue
		}
		info := operationInfo{Name: name, Style: defaultStyle, BodyUse: defaultUse}
		if ptOp.Input != nil {
			info.InputMessage = strings.TrimSpace(ptOp.Input.Message)
			info.InputMessageNamespaces = cloneNamespaceBindings(ptOp.Input.NamespaceBindings)
		}
		if bindingOp, ok := bindingOps[name]; ok {
			info.BindingOperationResolved = true
			for _, soapOp := range bindingOp.SOAPOperations {
				if version := soapVersionFromXMLName(soapOp.XMLName); version == "1.2" {
					// SOAP version is binding-level in the summary; operation-level
					// SOAP 1.2 markers are still useful to mark this as supported.
					_ = version
				}
				if strings.TrimSpace(soapOp.SOAPAction) != "" {
					info.SOAPAction = strings.TrimSpace(soapOp.SOAPAction)
				}
				if strings.TrimSpace(soapOp.Style) != "" {
					info.Style = strings.ToLower(strings.TrimSpace(soapOp.Style))
				}
				break
			}
			if bindingOp.Input != nil {
				if len(bindingOp.Input.Bodies) > 0 {
					body := bindingOp.Input.Bodies[0]
					if strings.TrimSpace(body.Use) != "" {
						info.BodyUse = strings.ToLower(strings.TrimSpace(body.Use))
					}
					info.BodyNamespace = strings.TrimSpace(body.Namespace)
					info.BodyParts = strings.Fields(body.Parts)
					if version := soapVersionFromXMLName(body.XMLName); version == "1.2" {
						_ = version
					}
				}
				info.Headers = append(info.Headers, bindingOp.Input.Headers...)
			}
		}
		if info.BodyUse == "" {
			info.BodyUse = defaultUse
		}
		operations = append(operations, info)
	}
	return operations
}

func findBinding(doc definitions, bindingRef string, namespaceContexts ...map[string]string) (binding, bool) {
	name, namespace, qualified, resolved := resolveWSDLQName(bindingRef, firstNamespaceContext(namespaceContexts))
	if name == "" && len(doc.Bindings) == 1 {
		return doc.Bindings[0], true
	}
	if name == "" {
		return binding{}, false
	}
	if qualified && !resolved {
		// Some otherwise usable WSDLs refer to tns:* without declaring tns.
		// Preserve tolerant import only when the local declaration is unique;
		// namespace collisions must remain unresolved rather than selecting by
		// document order.
		var match binding
		matches := 0
		for _, candidate := range doc.Bindings {
			if candidate.Name == name {
				match = candidate
				matches++
			}
		}
		return match, matches == 1
	}
	for _, b := range doc.Bindings {
		if b.Name == name && (!qualified || strings.TrimSpace(b.DefinitionNamespace) == namespace) {
			return b, true
		}
	}
	return binding{}, false
}

func findPortType(doc definitions, portTypeRef string, namespaceContexts ...map[string]string) (portType, bool) {
	name, namespace, qualified, resolved := resolveWSDLQName(portTypeRef, firstNamespaceContext(namespaceContexts))
	if name == "" && len(doc.PortTypes) == 1 {
		return doc.PortTypes[0], true
	}
	if name == "" {
		return portType{}, false
	}
	if qualified && !resolved {
		var match portType
		matches := 0
		for _, candidate := range doc.PortTypes {
			if candidate.Name == name {
				match = candidate
				matches++
			}
		}
		return match, matches == 1
	}
	for _, pt := range doc.PortTypes {
		if pt.Name == name && (!qualified || strings.TrimSpace(pt.DefinitionNamespace) == namespace) {
			return pt, true
		}
	}
	return portType{}, false
}

func firstSOAPBinding(b binding) soapBinding {
	for _, item := range b.SOAPBindings {
		if strings.EqualFold(item.XMLName.Local, "binding") {
			return item
		}
	}
	if bindingHasSOAPOperations(b) {
		return soapBinding{XMLName: xml.Name{Local: "binding"}, Style: "document"}
	}
	return soapBinding{}
}

func bindingHasSOAPOperations(b binding) bool {
	for _, op := range b.Operations {
		if len(op.SOAPOperations) > 0 {
			return true
		}
		if op.Input != nil && (len(op.Input.Bodies) > 0 || len(op.Input.Headers) > 0) {
			return true
		}
	}
	return false
}

func collectPortTypeOperations(pt portType, filter []string) []operationInfo {
	allowed := stringSet(filter)
	operations := make([]operationInfo, 0, len(pt.Operations))
	for _, op := range pt.Operations {
		name := strings.TrimSpace(op.Name)
		if name == "" || (len(allowed) > 0 && !allowed[name]) {
			continue
		}
		info := operationInfo{Name: name, Style: "document", BodyUse: "literal"}
		if op.Input != nil {
			info.InputMessage = strings.TrimSpace(op.Input.Message)
			info.InputMessageNamespaces = cloneNamespaceBindings(op.Input.NamespaceBindings)
		}
		operations = append(operations, info)
	}
	return operations
}

func firstSOAPAddress(p port) string {
	for _, address := range p.Addresses {
		if strings.TrimSpace(address.Location) != "" {
			return strings.TrimSpace(address.Location)
		}
	}
	return ""
}

func matchesSelector(actual string, selector string) bool {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return true
	}
	return actual == selector || localName(actual) == selector
}

func bindingChoiceLabel(choice bindingChoice) string {
	parts := []string{}
	if choice.ServiceName != "" {
		parts = append(parts, "service "+choice.ServiceName)
	}
	if choice.PortName != "" {
		parts = append(parts, "port "+choice.PortName)
	}
	if choice.BindingName != "" {
		parts = append(parts, "binding "+choice.BindingName)
	}
	if len(parts) == 0 {
		return "first SOAP binding"
	}
	return strings.Join(parts, ", ")
}

func bindingChoiceSummaries(choices []bindingChoice) []BindingChoiceSummary {
	if len(choices) <= 1 {
		return nil
	}
	out := make([]BindingChoiceSummary, 0, len(choices))
	for _, choice := range choices {
		out = append(out, BindingChoiceSummary{
			ServiceName:    choice.ServiceName,
			PortName:       choice.PortName,
			BindingName:    choice.BindingName,
			PortTypeName:   choice.PortTypeName,
			Endpoint:       choice.Endpoint,
			SOAPVersion:    choice.SOAPVersion,
			Style:          choice.Style,
			Use:            choice.Use,
			Transport:      choice.Transport,
			OperationCount: len(choice.Operations),
		})
	}
	return out
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

func soapActionTarget(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if strings.HasPrefix(baseURL, "http://") || strings.HasPrefix(baseURL, "https://") {
		return baseURL
	}
	if baseURL == "" {
		// Keep endpoint-less conversions structurally valid without accidentally
		// targeting the controller or another caller-provided base URL. A durable
		// review gate is attached to the action until a real endpoint is supplied.
		return "https://wsdl.invalid/"
	}
	return baseURL
}

func soapEnvelope(doc definitions, op operationInfo, soapVersion string, diagnostics *diag.Collector) string {
	namespace := soapEnvelopeNamespace(doc, op)
	body := soapBodySkeleton(doc, op, diagnostics, namespace)
	header := soapHeaderSkeleton(doc, op, diagnostics, namespace)
	return fmt.Sprintf(`<soapenv:Envelope xmlns:soapenv="%s" xmlns:tns="%s">
%s
  <soapenv:Body>
%s
  </soapenv:Body>
</soapenv:Envelope>`, soapEnvelopeURI(soapVersion), namespace, header, body)
}

func soapEnvelopeNamespace(doc definitions, op operationInfo) string {
	if namespace := strings.TrimSpace(op.BodyNamespace); namespace != "" {
		return namespace
	}
	parts := inputMessageParts(doc, op.InputMessage, op.InputMessageNamespaces)
	if len(op.BodyParts) > 0 {
		parts = filterMessageParts(parts, op.BodyParts)
	}
	for _, part := range parts {
		if strings.TrimSpace(part.Element) == "" {
			continue
		}
		_, schema, found := findXSDElementWithSchema(doc, part.Element, part.NamespaceBindings)
		if found && strings.TrimSpace(schema.TargetNamespace) != "" {
			return strings.TrimSpace(schema.TargetNamespace)
		}
	}
	return convertutil.FirstNonEmpty(doc.TargetNamespace, "urn:service")
}

// soapHeaderSkeleton renders the soap:header parts declared on the binding's
// input message into the envelope header, so required headers (sessions,
// credentials, routing) are visible and editable instead of silently dropped.
func soapHeaderSkeleton(doc definitions, op operationInfo, diagnostics *diag.Collector, envelopeNamespace string) string {
	if len(op.Headers) == 0 {
		return "  <soapenv:Header/>"
	}
	location := "operation " + op.Name
	lines := make([]string, 0, len(op.Headers))
	for _, headerBinding := range op.Headers {
		part, ok := messagePartByName(doc, headerBinding.Message, headerBinding.Part, headerBinding.NamespaceBindings)
		if !ok {
			diagnostics.Add(diag.Warning, "WSDL SOAP header", location,
				"soap:header part "+convertutil.FirstNonEmpty(headerBinding.Part, headerBinding.Message)+" could not be resolved; add the header manually before replay")
			continue
		}
		headerName := localName(convertutil.FirstNonEmpty(part.Element, part.Name))
		if headerName == "" {
			continue
		}
		if element, schema, found := findXSDElementWithSchema(doc, part.Element, part.NamespaceBindings); found {
			if fields, ok := xsdFieldsForElementInSchema(doc, element, schema); ok {
				lines = append(lines, renderXMLElementInNamespace(doc, headerName, fields, diagnostics, location, 2, schema.TargetNamespace, envelopeNamespace))
				diagnostics.Add(diag.Info, "WSDL SOAP header", location, "soap:header "+headerName+" was rendered into the envelope header")
				continue
			}
			lines = append(lines, renderPlaceholderElement(headerName, placeholderForXSDType(doc, part.Type, headerName, part.NamespaceBindings), 2, schema.TargetNamespace, envelopeNamespace))
			diagnostics.Add(diag.Info, "WSDL SOAP header", location, "soap:header "+headerName+" was rendered with a placeholder value")
			continue
		}
		lines = append(lines, renderPlaceholderElement(headerName, placeholderForXSDType(doc, part.Type, headerName, part.NamespaceBindings), 2))
		diagnostics.Add(diag.Info, "WSDL SOAP header", location, "soap:header "+headerName+" was rendered with a placeholder value")
	}
	if len(lines) == 0 {
		return "  <soapenv:Header/>"
	}
	return "  <soapenv:Header>\n" + strings.Join(lines, "\n") + "\n  </soapenv:Header>"
}

// messagePartByName resolves a message part referenced by a soap:header
// binding. An empty part name selects the message's first part.
func messagePartByName(doc definitions, messageRef, partName string, namespaceContexts ...map[string]string) (messagePart, bool) {
	name, namespace, qualified, resolved := resolveWSDLQName(messageRef, firstNamespaceContext(namespaceContexts))
	if name == "" || (qualified && !resolved) {
		return messagePart{}, false
	}
	for _, msg := range doc.Messages {
		if msg.Name != name || (qualified && strings.TrimSpace(msg.DefinitionNamespace) != namespace) || len(msg.Parts) == 0 {
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

func soapBodySkeleton(doc definitions, op operationInfo, diagnostics *diag.Collector, envelopeNamespace string) string {
	location := "operation " + op.Name
	parts := inputMessageParts(doc, op.InputMessage, op.InputMessageNamespaces)
	if len(op.BodyParts) > 0 {
		parts = filterMessageParts(parts, op.BodyParts)
	}
	if len(parts) == 0 {
		diagnostics.Add(diag.Warning, "WSDL input message", location, "input message/part could not be resolved; generated a placeholder SOAP body")
		return renderPlaceholderElement(op.Name, "      <!-- Review request elements from the WSDL/XSD and replace placeholders before replay. -->", 2)
	}
	if strings.EqualFold(op.BodyUse, "encoded") {
		diagnostics.Add(diag.Warning, "WSDL SOAP encoding", location, "soap:body use=\"encoded\" is not faithfully generated; review the payload before replay")
	}
	if strings.EqualFold(op.Style, "rpc") {
		lines := []string{"    <tns:" + op.Name + ">"}
		for _, part := range parts {
			lines = append(lines, renderMessagePart(doc, part, diagnostics, location, 3, true, envelopeNamespace))
		}
		lines = append(lines, "    </tns:"+op.Name+">")
		return strings.Join(lines, "\n")
	}
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		lines = append(lines, renderMessagePart(doc, part, diagnostics, location, 2, false, envelopeNamespace))
	}
	return strings.Join(lines, "\n")
}

func renderMessagePart(doc definitions, part messagePart, diagnostics *diag.Collector, location string, depth int, rpc bool, envelopeNamespace string) string {
	bodyName := localName(convertutil.FirstNonEmpty(part.Element, part.Name))
	if bodyName == "" {
		bodyName = "parameters"
	}
	if rpc {
		bodyName = localName(convertutil.FirstNonEmpty(part.Name, part.Element, "parameters"))
	}
	if element, schema, found := findXSDElementWithSchema(doc, part.Element, part.NamespaceBindings); found && !rpc {
		if fields, ok := xsdFieldsForElementInSchema(doc, element, schema); ok {
			return renderXMLElementInNamespace(doc, bodyName, fields, diagnostics, location, depth, schema.TargetNamespace, envelopeNamespace)
		}
		diagnostics.Add(diag.Warning, "WSDL XSD type", location, "element "+bodyName+" has no supported complexType model; generated a placeholder SOAP body")
		return renderPlaceholderElement(bodyName, "", depth, schema.TargetNamespace, envelopeNamespace)
	}
	if part.Type != "" {
		if fields, ok := xsdFieldsForType(doc, part.Type, part.NamespaceBindings); ok {
			return renderXMLElementInNamespace(doc, bodyName, fields, diagnostics, location, depth, envelopeNamespace, envelopeNamespace)
		}
		if isKnownXSDSimpleType(part.Type) || hasNamedXSDSimpleType(doc, part.Type, part.NamespaceBindings) {
			return renderPlaceholderElement(bodyName, placeholderForXSDType(doc, part.Type, bodyName, part.NamespaceBindings), depth)
		}
	}
	diagnostics.Add(diag.Warning, "WSDL XSD element", location, "message part "+bodyName+" has no inlined XSD element/type; generated a placeholder SOAP body")
	return renderPlaceholderElement(bodyName, placeholderForXSDType(doc, part.Type, bodyName, part.NamespaceBindings), depth)
}

func inputMessagePart(doc definitions, messageRef string, namespaceContexts ...map[string]string) (messagePart, bool) {
	parts := inputMessageParts(doc, messageRef, namespaceContexts...)
	if len(parts) == 0 {
		return messagePart{}, false
	}
	return parts[0], true
}

func inputMessageParts(doc definitions, messageRef string, namespaceContexts ...map[string]string) []messagePart {
	name, namespace, qualified, resolved := resolveWSDLQName(messageRef, firstNamespaceContext(namespaceContexts))
	if name == "" || (qualified && !resolved) {
		return nil
	}
	for _, msg := range doc.Messages {
		if msg.Name != name || (qualified && strings.TrimSpace(msg.DefinitionNamespace) != namespace) || len(msg.Parts) == 0 {
			continue
		}
		return append([]messagePart(nil), msg.Parts...)
	}
	return nil
}

func filterMessageParts(parts []messagePart, allowed []string) []messagePart {
	allowedSet := stringSet(allowed)
	if len(allowedSet) == 0 {
		return parts
	}
	filtered := make([]messagePart, 0, len(parts))
	for _, part := range parts {
		if allowedSet[part.Name] {
			filtered = append(filtered, part)
		}
	}
	return filtered
}

func findXSDElement(doc definitions, name string) (xsdElement, bool) {
	element, _, found := findXSDElementWithSchema(doc, name)
	return element, found
}

func findXSDElementWithSchemaInContext(doc definitions, name string, namespaces map[string]string) (xsdElement, xsdSchema, bool) {
	local, namespace, qualified, resolved := resolveWSDLQName(name, namespaces)
	if local == "" || (qualified && !resolved) {
		return xsdElement{}, xsdSchema{}, false
	}
	for _, schema := range allXSDSchemas(doc) {
		if qualified && strings.TrimSpace(schema.TargetNamespace) != namespace {
			continue
		}
		for _, element := range schema.Elements {
			if localName(convertutil.FirstNonEmpty(element.Name, element.Ref)) == local {
				return element, schema, true
			}
		}
	}
	return xsdElement{}, xsdSchema{}, false
}

func findXSDElementWithSchema(doc definitions, name string, namespaceContexts ...map[string]string) (xsdElement, xsdSchema, bool) {
	var namespaces map[string]string
	if len(namespaceContexts) > 0 {
		namespaces = namespaceContexts[0]
	}
	return findXSDElementWithSchemaInContext(doc, name, namespaces)
}

func allXSDSchemas(doc definitions) []xsdSchema {
	var schemas []xsdSchema
	var appendSchema func(xsdSchema)
	appendSchema = func(schema xsdSchema) {
		schemas = append(schemas, schema)
		for _, resolved := range schema.ResolvedSchemas {
			appendSchema(resolved)
		}
	}
	for _, item := range doc.Types {
		for _, schema := range item.Schemas {
			appendSchema(schema)
		}
	}
	return schemas
}

func resolveWSDLQName(value string, namespaces map[string]string) (local string, namespace string, qualified bool, resolved bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false, false
	}
	prefix, local, hasPrefix := strings.Cut(value, ":")
	if !hasPrefix {
		return value, "", false, true
	}
	if prefix == "" || local == "" {
		return local, "", true, false
	}
	namespace, ok := namespaces[prefix]
	return local, strings.TrimSpace(namespace), true, ok && strings.TrimSpace(namespace) != ""
}

type xsdFieldSet struct {
	Elements           []xsdElement
	Model              string
	ChoiceName         string
	Attributes         []xsdAttribute
	Namespace          string
	NamespaceBindings  map[string]string
	ElementFormDefault string
}

func xsdFieldsForElement(doc definitions, element xsdElement) (xsdFieldSet, bool) {
	if _, schema, found := findXSDElementWithSchema(
		doc,
		localName(convertutil.FirstNonEmpty(element.Name, element.Ref)),
	); found {
		return xsdFieldsForElementInSchema(doc, element, schema)
	}
	return xsdFieldsForElementInSchema(doc, element, xsdSchema{})
}

func xsdFieldsForElementInSchema(doc definitions, element xsdElement, schema xsdSchema) (xsdFieldSet, bool) {
	elementNamespaces := xsdElementNamespaceBindings(element, schema.NamespaceBindings)
	if element.ComplexType != nil {
		elementSchema := schema
		elementSchema.NamespaceBindings = elementNamespaces
		return xsdFieldsForComplexTypeInSchema(*element.ComplexType, elementSchema)
	}
	if element.Type != "" {
		return xsdFieldsForType(doc, element.Type, elementNamespaces)
	}
	return xsdFieldSet{}, false
}

func xsdElementNamespaceBindings(element xsdElement, inherited map[string]string) map[string]string {
	if element.NamespaceBindings != nil {
		return element.NamespaceBindings
	}
	return mergeNamespaceBindings(inherited, element.Attrs)
}

func xsdFieldsForType(doc definitions, typeName string, namespaceContexts ...map[string]string) (xsdFieldSet, bool) {
	var namespaces map[string]string
	if len(namespaceContexts) > 0 {
		namespaces = namespaceContexts[0]
	}
	local, namespace, qualified, resolved := resolveWSDLQName(typeName, namespaces)
	if local == "" || (qualified && !resolved) {
		return xsdFieldSet{}, false
	}
	for _, schema := range allXSDSchemas(doc) {
		if qualified && strings.TrimSpace(schema.TargetNamespace) != namespace {
			continue
		}
		for _, ct := range schema.ComplexTypes {
			if ct.Name == local {
				return xsdFieldsForComplexTypeInSchema(ct, schema)
			}
		}
	}
	return xsdFieldSet{}, false
}

func xsdFieldsForComplexType(ct xsdComplexType) (xsdFieldSet, bool) {
	return xsdFieldsForComplexTypeInSchema(ct, xsdSchema{})
}

func xsdFieldsForComplexTypeInSchema(ct xsdComplexType, schema xsdSchema) (xsdFieldSet, bool) {
	complexTypeNamespaces := ct.NamespaceBindings
	if complexTypeNamespaces == nil {
		complexTypeNamespaces = mergeNamespaceBindings(schema.NamespaceBindings, ct.Attrs)
	}
	set := xsdFieldSet{
		Model:              "sequence",
		Attributes:         ct.Attributes,
		Namespace:          strings.TrimSpace(schema.TargetNamespace),
		NamespaceBindings:  cloneNamespaceBindings(complexTypeNamespaces),
		ElementFormDefault: strings.ToLower(strings.TrimSpace(schema.ElementFormDefault)),
	}
	switch {
	case ct.Sequence != nil:
		set.Elements = ct.Sequence.Elements
		if ct.Sequence.NamespaceBindings != nil {
			set.NamespaceBindings = cloneNamespaceBindings(ct.Sequence.NamespaceBindings)
		} else {
			set.NamespaceBindings = mergeNamespaceBindings(complexTypeNamespaces, ct.Sequence.Attrs)
		}
	case ct.All != nil:
		set.Elements = ct.All.Elements
		set.Model = "all"
		if ct.All.NamespaceBindings != nil {
			set.NamespaceBindings = cloneNamespaceBindings(ct.All.NamespaceBindings)
		} else {
			set.NamespaceBindings = mergeNamespaceBindings(complexTypeNamespaces, ct.All.Attrs)
		}
	case ct.Choice != nil:
		set.Elements = firstChoiceElement(ct.Choice.Elements)
		set.Model = "choice"
		if ct.Choice.NamespaceBindings != nil {
			set.NamespaceBindings = cloneNamespaceBindings(ct.Choice.NamespaceBindings)
		} else {
			set.NamespaceBindings = mergeNamespaceBindings(complexTypeNamespaces, ct.Choice.Attrs)
		}
		if len(set.Elements) > 0 {
			set.ChoiceName = localName(convertutil.FirstNonEmpty(set.Elements[0].Name, set.Elements[0].Ref))
		}
	default:
		return set, len(set.Attributes) > 0
	}
	return set, len(set.Elements) > 0 || len(set.Attributes) > 0
}

func firstChoiceElement(elements []xsdElement) []xsdElement {
	for _, element := range elements {
		if strings.TrimSpace(convertutil.FirstNonEmpty(element.Name, element.Ref)) != "" {
			return []xsdElement{element}
		}
	}
	return nil
}

func renderXSDFieldSet(doc definitions, set xsdFieldSet, diagnostics *diag.Collector, location string, depth int, inheritedNamespaces ...string) string {
	addXSDModelDiagnostics(set, diagnostics, location)
	inheritedNamespace := ""
	if len(inheritedNamespaces) > 0 {
		inheritedNamespace = inheritedNamespaces[0]
	}
	return renderXSDFields(doc, set, diagnostics, location, depth, inheritedNamespace)
}

func addXSDModelDiagnostics(set xsdFieldSet, diagnostics *diag.Collector, location string) {
	switch set.Model {
	case "all":
		diagnostics.Add(diag.Info, "WSDL XSD all", location, "xsd:all elements were rendered in schema order; validate optional and unordered payload semantics before replay")
	case "choice":
		choice := convertutil.FirstNonEmpty(set.ChoiceName, "first available branch")
		diagnostics.Add(diag.Warning, "WSDL XSD choice", location, "xsd:choice was represented with "+choice+"; choose the correct branch before replay")
	}
	if len(set.Attributes) > 0 {
		diagnostics.Add(diag.Info, "WSDL XSD attributes", location, "attributes "+xsdAttributeNames(set.Attributes)+" were rendered with editable placeholders; validate required/optional semantics before replay")
	}
}

func xsdAttributeNames(attributes []xsdAttribute) string {
	names := make([]string, 0, len(attributes))
	for _, attribute := range attributes {
		name := localName(convertutil.FirstNonEmpty(attribute.Name, attribute.Ref))
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

func renderXSDFields(doc definitions, set xsdFieldSet, diagnostics *diag.Collector, location string, depth int, inheritedNamespace string) string {
	if len(set.Elements) == 0 {
		return ""
	}
	if depth >= maxXSDRenderDepth {
		diagnostics.Add(diag.Warning, "WSDL XSD type", location, fmt.Sprintf("stopped rendering at depth %d; the schema is recursive or extremely deep — fill in nested levels manually before replay", depth))
		return ""
	}
	indent := strings.Repeat("  ", depth)
	lines := make([]string, 0, len(set.Elements))
	for _, field := range set.Elements {
		name := localName(convertutil.FirstNonEmpty(field.Name, field.Ref))
		if name == "" {
			continue
		}
		fieldNamespaces := xsdElementNamespaceBindings(field, set.NamespaceBindings)
		var refElement xsdElement
		var refSchema xsdSchema
		refFound := false
		if strings.TrimSpace(field.Ref) != "" {
			refElement, refSchema, refFound = findXSDElementWithSchema(doc, field.Ref, fieldNamespaces)
		}
		tag, tagNamespace := renderXSDFieldTag(field, name, set, fieldNamespaces, refSchema, refFound)
		namespaceDeclaration := ""
		if tagNamespace != "" && tagNamespace != inheritedNamespace {
			namespaceDeclaration = xsdNamespaceDeclaration(tagNamespace)
		}
		childInheritedNamespace := inheritedNamespace
		if tagNamespace != "" {
			childInheritedNamespace = tagNamespace
		}
		addXSDOccurrenceDiagnostics(field, diagnostics, location)
		if field.ComplexType != nil {
			nested, ok := xsdFieldsForComplexTypeInSchema(*field.ComplexType, xsdSchema{
				TargetNamespace:    set.Namespace,
				NamespaceBindings:  fieldNamespaces,
				ElementFormDefault: set.ElementFormDefault,
			})
			if !ok {
				diagnostics.Add(diag.Warning, "WSDL XSD type", location, "inline complexType for child element "+name+" has no supported content model; generated an empty SOAP element")
				lines = append(lines, indent+"<"+tag+namespaceDeclaration+"></"+tag+">")
				continue
			}
			lines = append(lines, indent+"<"+tag+namespaceDeclaration+renderXSDAttributes(nested.Attributes)+">")
			lines = append(lines, renderXSDFieldSet(doc, nested, diagnostics, location, depth+1, childInheritedNamespace))
			lines = append(lines, indent+"</"+tag+">")
			continue
		}
		if refFound {
			if nested, ok := xsdFieldsForElementInSchema(doc, refElement, refSchema); ok {
				lines = append(lines, indent+"<"+tag+namespaceDeclaration+renderXSDAttributes(nested.Attributes)+">")
				lines = append(lines, renderXSDFieldSet(doc, nested, diagnostics, location, depth+1, childInheritedNamespace))
				lines = append(lines, indent+"</"+tag+">")
				continue
			}
		}
		if fieldsForType, ok := xsdFieldsForType(doc, field.Type, fieldNamespaces); ok {
			lines = append(lines, indent+"<"+tag+namespaceDeclaration+renderXSDAttributes(fieldsForType.Attributes)+">")
			lines = append(lines, renderXSDFieldSet(doc, fieldsForType, diagnostics, location, depth+1, childInheritedNamespace))
			lines = append(lines, indent+"</"+tag+">")
			continue
		}
		if strings.TrimSpace(field.Ref) != "" {
			diagnostics.Add(diag.Warning, "WSDL XSD ref", location, "referenced child element "+field.Ref+" was represented with a placeholder value")
		}
		lines = append(lines, indent+"<"+tag+namespaceDeclaration+">"+placeholderForXSDElement(doc, field, name, fieldNamespaces)+"</"+tag+">")
	}
	return strings.Join(lines, "\n")
}

func renderXSDFieldTag(field xsdElement, name string, set xsdFieldSet, fieldNamespaces map[string]string, refSchema xsdSchema, refFound bool) (string, string) {
	if strings.TrimSpace(field.Ref) != "" {
		namespace := strings.TrimSpace(set.Namespace)
		if refFound && strings.TrimSpace(refSchema.TargetNamespace) != "" {
			namespace = strings.TrimSpace(refSchema.TargetNamespace)
		} else if _, resolvedNamespace, qualified, resolved := resolveWSDLQName(field.Ref, fieldNamespaces); qualified && resolved {
			namespace = resolvedNamespace
		}
		return "tns:" + name, namespace
	}
	switch strings.ToLower(strings.TrimSpace(field.Form)) {
	case "qualified":
		return "tns:" + name, strings.TrimSpace(set.Namespace)
	case "unqualified":
		return name, ""
	}
	if strings.EqualFold(strings.TrimSpace(set.ElementFormDefault), "qualified") {
		return "tns:" + name, strings.TrimSpace(set.Namespace)
	}
	return name, ""
}

func renderXMLElement(doc definitions, name string, set xsdFieldSet, diagnostics *diag.Collector, location string, depth int) string {
	return renderXMLElementInNamespace(doc, name, set, diagnostics, location, depth, set.Namespace, "")
}

func renderXMLElementInNamespace(doc definitions, name string, set xsdFieldSet, diagnostics *diag.Collector, location string, depth int, elementNamespace string, inheritedNamespace string) string {
	indent := strings.Repeat("  ", depth)
	elementNamespace = strings.TrimSpace(elementNamespace)
	namespaceDeclaration := ""
	if elementNamespace != "" && elementNamespace != inheritedNamespace {
		namespaceDeclaration = xsdNamespaceDeclaration(elementNamespace)
	}
	childInheritedNamespace := inheritedNamespace
	if elementNamespace != "" {
		childInheritedNamespace = elementNamespace
	}
	children := renderXSDFieldSet(doc, set, diagnostics, location, depth+1, childInheritedNamespace)
	if strings.TrimSpace(children) == "" {
		return indent + "<tns:" + name + namespaceDeclaration + renderXSDAttributes(set.Attributes) + "></tns:" + name + ">"
	}
	return indent + "<tns:" + name + namespaceDeclaration + renderXSDAttributes(set.Attributes) + ">\n" + children + "\n" + indent + "</tns:" + name + ">"
}

func renderPlaceholderElement(name string, value string, depth int, namespaces ...string) string {
	indent := strings.Repeat("  ", depth)
	namespaceDeclaration := ""
	if len(namespaces) > 0 && strings.TrimSpace(namespaces[0]) != "" {
		inheritedNamespace := ""
		if len(namespaces) > 1 {
			inheritedNamespace = strings.TrimSpace(namespaces[1])
		}
		if strings.TrimSpace(namespaces[0]) != inheritedNamespace {
			namespaceDeclaration = xsdNamespaceDeclaration(namespaces[0])
		}
	}
	return indent + "<tns:" + name + namespaceDeclaration + ">" + value + "</tns:" + name + ">"
}

func xsdNamespaceDeclaration(namespace string) string {
	if strings.TrimSpace(namespace) == "" {
		return ""
	}
	return ` xmlns:tns="` + xmlEscape(strings.TrimSpace(namespace)) + `"`
}

func renderXSDAttributes(attributes []xsdAttribute) string {
	if len(attributes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(attributes))
	for _, attribute := range attributes {
		name := localName(convertutil.FirstNonEmpty(attribute.Name, attribute.Ref))
		if name == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf(`%s="%s"`, name, placeholderForXSDType(definitions{}, attribute.Type, name)))
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	return " " + strings.Join(parts, " ")
}

func placeholderForXSDElement(doc definitions, field xsdElement, fieldName string, namespaceContexts ...map[string]string) string {
	namespaces := firstNamespaceContext(namespaceContexts)
	if field.Fixed != "" {
		return xmlEscape(field.Fixed)
	}
	if field.Default != "" {
		return xmlEscape(field.Default)
	}
	if field.SimpleType != nil {
		if value, ok := placeholderForSimpleTypeDepth(*field.SimpleType, doc, fieldName, 0, namespaces); ok {
			return value
		}
	}
	return placeholderForXSDType(doc, field.Type, fieldName, namespaces)
}

// maxSimpleTypeDepth bounds simpleType placeholder resolution. A cyclic or
// self-restricting XSD simpleType (e.g. <simpleType name="A"><restriction
// base="A"/></simpleType>, or a mutually-recursive pair) would otherwise recurse
// forever and stack-overflow — a process-fatal, recover()-proof crash — during
// WSDL/enterprise-archive import of untrusted content. On exceeding the cap we
// fall back to the generic data-driven placeholder rather than recursing.
const maxSimpleTypeDepth = 32

func placeholderForXSDType(doc definitions, typeName string, fieldName string, namespaceContexts ...map[string]string) string {
	return placeholderForXSDTypeDepth(doc, typeName, fieldName, 0, firstNamespaceContext(namespaceContexts))
}

func isKnownXSDSimpleType(typeName string) bool {
	switch strings.ToLower(localName(typeName)) {
	case "string", "normalizedstring", "token", "language", "name", "ncname",
		"id", "idref", "idrefs", "entity", "entities", "nmtoken", "nmtokens",
		"anyuri", "qname", "notation", "base64binary", "hexbinary",
		"byte", "unsignedbyte", "int", "unsignedint", "integer",
		"positiveinteger", "negativeinteger", "nonpositiveinteger", "nonnegativeinteger",
		"long", "unsignedlong", "short", "unsignedshort", "decimal", "double", "float",
		"boolean", "datetime", "date", "time", "duration", "gyearmonth", "gyear",
		"gmonthday", "gday", "gmonth":
		return true
	default:
		return false
	}
}

func hasNamedXSDSimpleType(doc definitions, typeName string, namespaceContexts ...map[string]string) bool {
	_, _, found := findNamedXSDSimpleType(doc, typeName, firstNamespaceContext(namespaceContexts))
	return found
}

func findNamedXSDSimpleType(doc definitions, typeName string, namespaces map[string]string) (xsdSimpleType, xsdSchema, bool) {
	name, namespace, qualified, resolved := resolveWSDLQName(typeName, namespaces)
	if name == "" || (qualified && !resolved) {
		return xsdSimpleType{}, xsdSchema{}, false
	}
	for _, schema := range allXSDSchemas(doc) {
		if qualified && strings.TrimSpace(schema.TargetNamespace) != namespace {
			continue
		}
		for _, simple := range schema.SimpleTypes {
			if simple.Name == name {
				return simple, schema, true
			}
		}
	}
	return xsdSimpleType{}, xsdSchema{}, false
}

func placeholderForXSDTypeDepth(doc definitions, typeName string, fieldName string, depth int, namespaces map[string]string) string {
	if depth > maxSimpleTypeDepth {
		return "{{data.wsdl." + sanitizeName(fieldName) + "}}"
	}
	if simple, schema, ok := findNamedXSDSimpleType(doc, typeName, namespaces); ok {
		if value, ok := placeholderForSimpleTypeDepth(simple, doc, fieldName, depth, schema.NamespaceBindings); ok {
			return value
		}
	}
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

func placeholderForNamedSimpleType(doc definitions, typeName string, fieldName string) (string, bool) {
	return placeholderForNamedSimpleTypeDepth(doc, typeName, fieldName, 0, nil)
}

func placeholderForNamedSimpleTypeDepth(doc definitions, typeName string, fieldName string, depth int, namespaces map[string]string) (string, bool) {
	if typeName == "" {
		return "", false
	}
	if simple, schema, ok := findNamedXSDSimpleType(doc, typeName, namespaces); ok {
		return placeholderForSimpleTypeDepth(simple, doc, fieldName, depth, schema.NamespaceBindings)
	}
	return "", false
}

func placeholderForSimpleType(simple xsdSimpleType, doc definitions, fieldName string) (string, bool) {
	return placeholderForSimpleTypeDepth(simple, doc, fieldName, 0, nil)
}

func placeholderForSimpleTypeDepth(simple xsdSimpleType, doc definitions, fieldName string, depth int, namespaces map[string]string) (string, bool) {
	if depth > maxSimpleTypeDepth {
		return "{{data.wsdl." + sanitizeName(fieldName) + "}}", true
	}
	if simple.Restriction != nil {
		if len(simple.Restriction.Enumerations) > 0 {
			return xmlEscape(simple.Restriction.Enumerations[0].Value), true
		}
		return placeholderForXSDTypeDepth(doc, simple.Restriction.Base, fieldName, depth+1, namespaces), true
	}
	if simple.List != nil {
		return placeholderForXSDTypeDepth(doc, simple.List.ItemType, fieldName, depth+1, namespaces), true
	}
	if simple.Union != nil {
		types := strings.Fields(simple.Union.MemberTypes)
		if len(types) > 0 {
			return placeholderForXSDTypeDepth(doc, types[0], fieldName, depth+1, namespaces), true
		}
	}
	return "", false
}

func firstNamespaceContext(contexts []map[string]string) map[string]string {
	if len(contexts) == 0 {
		return nil
	}
	return contexts[0]
}

func addXSDOccurrenceDiagnostics(field xsdElement, diagnostics *diag.Collector, location string) {
	name := localName(convertutil.FirstNonEmpty(field.Name, field.Ref))
	if name == "" {
		return
	}
	if strings.TrimSpace(field.MinOccurs) == "0" {
		diagnostics.Add(diag.Info, "WSDL XSD optional element", location, "element "+name+" is optional; one editable instance was rendered for review")
	}
	maxOccurs := strings.TrimSpace(field.MaxOccurs)
	if maxOccurs != "" && maxOccurs != "1" {
		diagnostics.Add(diag.Info, "WSDL XSD repeating element", location, "element "+name+" allows maxOccurs="+maxOccurs+"; one editable instance was rendered for review")
	}
	if strings.EqualFold(strings.TrimSpace(field.Nillable), "true") {
		diagnostics.Add(diag.Info, "WSDL XSD nillable element", location, "element "+name+" is nillable; add xsi:nil=\"true\" when replaying nil values")
	}
}

func soapEnvelopeURI(version string) string {
	if version == "1.2" {
		return "http://www.w3.org/2003/05/soap-envelope"
	}
	return "http://schemas.xmlsoap.org/soap/envelope/"
}

func soapVersionFromXMLName(name xml.Name) string {
	space := strings.ToLower(strings.TrimSpace(name.Space))
	switch {
	case strings.Contains(space, "soap12"), strings.Contains(space, "2003/05/soap-envelope"):
		return "1.2"
	case strings.Contains(space, "schemas.xmlsoap.org/wsdl/soap"):
		return "1.1"
	default:
		return ""
	}
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}

func addImportDiagnostics(doc definitions, diagnostics *diag.Collector) {
	for _, item := range doc.Imports {
		diagnostics.Add(diag.Warning, "WSDL import", "definitions", "referenced WSDL import "+convertutil.FirstNonEmpty(item.Location, item.Namespace)+" was not followed; inline or import it separately")
	}
	for _, schema := range allXSDSchemas(doc) {
		for _, item := range schema.Imports {
			diagnostics.Add(diag.Warning, "XSD import", "types/schema", "referenced XSD import "+convertutil.FirstNonEmpty(item.SchemaLocation, item.Namespace)+" was not followed; inline or import it separately")
		}
		for _, item := range schema.Includes {
			diagnostics.Add(diag.Warning, "XSD include", "types/schema", "referenced XSD include "+item.SchemaLocation+" was not followed; inline or import it separately")
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

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
