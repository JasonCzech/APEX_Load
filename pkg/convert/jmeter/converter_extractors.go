package jmeter

// converter_extractors.go — split from converter.go (same package, behavior-preserving).

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func parseSamplerExtractors(node *treeNode, diagnostics *diag.Collector) ([]dsl.Extractor, []string) {
	var out []dsl.Extractor
	var warnings []string
	location := jmeterElementLocation(node.Element)
	for _, child := range node.Children {
		// JMeter never runs a disabled post-processor, so importing one would
		// set a correlation variable the real plan leaves unset.
		if child == nil || child.Element == nil || !jmeterElementEnabled(child.Element) {
			continue
		}
		switch child.Element.Name {
		case "RegexExtractor":
			if ext := parseRegexExtractor(child.Element); ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		case "JSONPostProcessor":
			out = append(out, parseJSONExtractors(child.Element)...)
		case "JMESPathExtractor":
			if ext := parseJMESPathExtractor(child.Element); ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		case "HtmlExtractor":
			if ext := parseHTMLExtractor(child.Element); ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		case "XPathExtractor":
			if ext := parseXPathExtractor(child.Element); ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		case "XPath2Extractor":
			ext, warning := parseXPath2Extractor(child.Element)
			if warning != "" {
				warnings = append(warnings, warning)
				if diagnostics != nil {
					diagnostics.Add(diag.Warning, "JMeter XPath2Extractor", location, warning)
				}
			}
			if ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		case "BoundaryExtractor":
			if ext := parseBoundaryExtractor(child.Element); ext.Name != "" && ext.Expression != "" {
				out = append(out, ext)
			}
		}
	}
	if len(out) == 0 {
		out = nil
	}
	if len(warnings) == 0 {
		warnings = nil
	}
	return out, warnings
}

func parseBoundaryExtractor(node *xmlNode) dsl.Extractor {
	source := parseExtractorSource(prop(node, "BoundaryExtractor.useHeaders"))
	if source == "" {
		return dsl.Extractor{}
	}

	left := prop(node, "BoundaryExtractor.lboundary")
	right := prop(node, "BoundaryExtractor.rboundary")
	if strings.TrimSpace(left) == "" && strings.TrimSpace(right) == "" {
		return dsl.Extractor{}
	}

	return dsl.Extractor{
		Name:       strings.TrimSpace(prop(node, "BoundaryExtractor.refname")),
		Source:     source,
		Type:       "boundary",
		Expression: left + "|||" + right,
	}
}

func parseRegexExtractor(node *xmlNode) dsl.Extractor {
	source := parseExtractorSource(prop(node, "RegexExtractor.useHeaders"))
	if source == "" {
		return dsl.Extractor{}
	}

	return dsl.Extractor{
		Name:       strings.TrimSpace(prop(node, "RegexExtractor.refname")),
		Source:     source,
		Type:       "regex",
		Expression: strings.TrimSpace(prop(node, "RegexExtractor.regex")),
	}
}

func parseExtractorSource(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "", "false":
		return "body"
	case "true":
		return "header"
	default:
		return ""
	}
}

func parseJSONExtractors(node *xmlNode) []dsl.Extractor {
	names := splitDelimitedValues(convertutil.FirstNonEmpty(
		prop(node, "JSONPostProcessor.referenceNames"),
		prop(node, "JSONPostProcessor.reference_names"),
	))
	expressions := splitDelimitedValues(convertutil.FirstNonEmpty(
		prop(node, "JSONPostProcessor.jsonPathExprs"),
		prop(node, "JSONPostProcessor.json_path_exprs"),
	))
	defaults := strings.Split(prop(node, "JSONPostProcessor.defaultValues"), ";")
	hasDefault := false
	for _, child := range node.Children {
		if child.Attrs["name"] == "JSONPostProcessor.defaultValues" {
			hasDefault = true
			defaults = strings.Split(child.Text, ";")
		}
	}
	count := min(len(names), len(expressions))
	if count == 0 {
		return nil
	}
	out := make([]dsl.Extractor, 0, count)
	for i := 0; i < count; i++ {
		if names[i] == "" || expressions[i] == "" {
			continue
		}
		var fallback *string
		onMiss := ""
		if hasDefault && i < len(defaults) {
			value := convertVariables(defaults[i])
			fallback = &value
			onMiss = "default"
		}
		out = append(out, dsl.Extractor{
			DefaultValue: fallback,
			OnMiss:       onMiss,
			Name:         names[i],
			Source:       "body",
			Type:         "jsonpath",
			Expression:   expressions[i],
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseJMESPathExtractor(node *xmlNode) dsl.Extractor {
	return dsl.Extractor{
		Name:       strings.TrimSpace(prop(node, "JMESExtractor.referenceName")),
		Source:     "body",
		Type:       "jmespath",
		Expression: strings.TrimSpace(prop(node, "JMESExtractor.jmesPathExpr")),
	}
}

func parseHTMLExtractor(node *xmlNode) dsl.Extractor {
	selector := strings.TrimSpace(prop(node, "HtmlExtractor.expr"))
	attribute := strings.TrimSpace(prop(node, "HtmlExtractor.attribute"))
	if selector == "" {
		return dsl.Extractor{}
	}

	expression := selector
	if attribute != "" {
		expression += "|||" + attribute
	}

	return dsl.Extractor{
		Name:       strings.TrimSpace(prop(node, "HtmlExtractor.refname")),
		Source:     "body",
		Type:       "css",
		Expression: expression,
	}
}

const xpathFragmentExpressionPrefix = "__apexload_xpath_fragment__:"

const xpathExpressionConfigPrefix = "__apexload_xpath_config__:"

type xpathExpressionConfig struct {
	Expression string            `json:"expression"`
	Fragment   bool              `json:"fragment,omitempty"`
	Namespaces map[string]string `json:"namespaces,omitempty"`
}

func parseXPathExtractor(node *xmlNode) dsl.Extractor {
	expression := strings.TrimSpace(prop(node, "XPathExtractor.xpathQuery"))
	fragment := strings.EqualFold(strings.TrimSpace(prop(node, "XPathExtractor.fragment")), "true")

	return dsl.Extractor{
		Name:       strings.TrimSpace(prop(node, "XPathExtractor.refname")),
		Source:     "body",
		Type:       "xpath",
		Expression: encodeXPathExpression(expression, fragment, nil),
	}
}

func parseXPath2Extractor(node *xmlNode) (dsl.Extractor, string) {
	name := strings.TrimSpace(prop(node, "XPathExtractor2.refname"))
	expression := strings.TrimSpace(prop(node, "XPathExtractor2.xpathQuery"))
	namespaceConfig := strings.TrimSpace(prop(node, "XPathExtractor2.namespaces"))
	fragment := strings.EqualFold(strings.TrimSpace(prop(node, "XPathExtractor2.fragment")), "true")

	namespaces, err := parseXPathNamespaces(namespaceConfig)
	if err != nil {
		warning := fmt.Sprintf("XPath2 extractor %q was skipped because its namespace bindings were invalid: %v", name, err)
		if name == "" {
			warning = fmt.Sprintf("XPath2 extractor was skipped because its namespace bindings were invalid: %v", err)
		}
		return dsl.Extractor{}, warning
	}

	return dsl.Extractor{
		Name:       name,
		Source:     "body",
		Type:       "xpath",
		Expression: encodeXPathExpression(expression, fragment, namespaces),
	}, ""
}

func encodeXPathExpression(expression string, fragment bool, namespaces map[string]string) string {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return expression
	}
	if len(namespaces) == 0 {
		if !fragment {
			return expression
		}
		return xpathFragmentExpressionPrefix + expression
	}
	payload, err := json.Marshal(xpathExpressionConfig{
		Expression: expression,
		Fragment:   fragment,
		Namespaces: namespaces,
	})
	if err != nil {
		return expression
	}
	return xpathExpressionConfigPrefix + base64.RawStdEncoding.EncodeToString(payload)
}

func parseXPathNamespaces(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	namespaces := map[string]string{}
	for _, field := range strings.Fields(raw) {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("expected prefix=uri pair, got %q", field)
		}
		prefix := strings.TrimSpace(parts[0])
		uri := strings.TrimSpace(parts[1])
		if prefix == "" || uri == "" {
			return nil, fmt.Errorf("expected prefix=uri pair, got %q", field)
		}
		namespaces[prefix] = uri
	}
	if len(namespaces) == 0 {
		return nil, nil
	}
	return namespaces, nil
}
