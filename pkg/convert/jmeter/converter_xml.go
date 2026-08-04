package jmeter

// converter_xml.go — split from converter.go (same package, behavior-preserving).

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func parseXML(data []byte) (*xmlNode, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	content := string(data)
	lineIndex := sourceview.NewLineIndex(content)
	var stack []*xmlNode
	for {
		tok, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("parse JMX XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			node := &xmlNode{Name: t.Name.Local, Attrs: map[string]string{}, StartLine: lineIndex.LineForOffset(int(decoder.InputOffset()))}
			for _, attr := range t.Attr {
				node.Attrs[attr.Name.Local] = attr.Value
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
			}
			stack = append(stack, node)
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(t)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				continue
			}
			stack[len(stack)-1].EndLine = lineIndex.LineForOffset(int(decoder.InputOffset()))
			if len(stack) == 1 {
				return stack[0], nil
			}
			stack = stack[:len(stack)-1]
		}
	}
	return nil, fmt.Errorf("empty JMX XML")
}

func buildTree(hashTree *xmlNode) []*treeNode {
	var out []*treeNode
	for i := 0; i < len(hashTree.Children); i++ {
		elem := hashTree.Children[i]
		if elem.Name == "hashTree" {
			continue
		}
		tree := &treeNode{Element: elem}
		if i+1 < len(hashTree.Children) && hashTree.Children[i+1].Name == "hashTree" {
			tree.Children = buildTree(hashTree.Children[i+1])
			i++
		}
		out = append(out, tree)
	}
	return out
}

func findTreeNodes(nodes []*treeNode, name string) []*treeNode {
	var out []*treeNode
	for _, node := range nodes {
		if node.Element.Name == name {
			out = append(out, node)
		}
		out = append(out, findTreeNodes(node.Children, name)...)
	}
	return out
}

// findEnabledTreeNodes follows JMeter's execution semantics: disabling a plan,
// thread group, or controller disables its entire subtree even when descendants
// retain their default enabled="true" attribute.
func findEnabledTreeNodes(nodes []*treeNode, name string) []*treeNode {
	var out []*treeNode
	for _, node := range nodes {
		if node == nil || node.Element == nil || !jmeterElementEnabled(node.Element) {
			continue
		}
		if node.Element.Name == name {
			out = append(out, node)
		}
		out = append(out, findEnabledTreeNodes(node.Children, name)...)
	}
	return out
}

func enabledJMeterTreeRoots(nodes []*treeNode) []*treeNode {
	result := make([]*treeNode, 0, len(nodes))
	for _, node := range nodes {
		if node != nil && node.Element != nil && jmeterElementEnabled(node.Element) {
			result = append(result, node)
		}
	}
	return result
}

func appendDisabledJMeterSelectionMappings(nodes []*treeNode, sourceFile string, mappings *[]sourceview.Mapping) {
	for _, node := range nodes {
		if node == nil || node.Element == nil {
			continue
		}
		if !jmeterElementEnabled(node.Element) {
			if node.Element.Name == "TestPlan" || node.Element.Name == "ThreadGroup" {
				appendJMeterDisabledMapping(mappings, sourceFile, node.Element)
			}
			continue
		}
		appendDisabledJMeterSelectionMappings(node.Children, sourceFile, mappings)
	}
}

func prop(node *xmlNode, name string) string {
	if node == nil {
		return ""
	}
	if node.Attrs["name"] == name {
		return strings.TrimSpace(node.Text)
	}
	for _, child := range node.Children {
		if child.Attrs["name"] == name {
			return strings.TrimSpace(child.Text)
		}
		if got := prop(child, name); got != "" {
			return got
		}
	}
	return ""
}

func childProps(node *xmlNode, name string) []*xmlNode {
	var out []*xmlNode
	for _, child := range node.Children {
		if child.Attrs["name"] == name {
			out = append(out, child)
		}
		out = append(out, childProps(child, name)...)
	}
	return out
}

func applyHTTPDefaults(node *xmlNode, ctx *convertContext) {
	if node.Attrs["guiclass"] != "HttpDefaultsGui" && prop(node, "HTTPSampler.domain") == "" {
		return
	}
	ctx.defaultProtocol = convertutil.FirstNonEmpty(prop(node, "HTTPSampler.protocol"), ctx.defaultProtocol)
	ctx.defaultDomain = convertutil.FirstNonEmpty(prop(node, "HTTPSampler.domain"), ctx.defaultDomain)
	ctx.defaultPort = convertutil.FirstNonEmpty(prop(node, "HTTPSampler.port"), ctx.defaultPort)
	ctx.defaultPath = convertutil.FirstNonEmpty(prop(node, "HTTPSampler.path"), ctx.defaultPath)
}

func parseHeaders(node *xmlNode) dsl.HeaderList {
	var headers dsl.HeaderList
	for _, element := range childProps(node, "Header.name") {
		parent := parentElement(node, element)
		if parent == nil {
			continue
		}
		name := prop(parent, "Header.name")
		value := prop(parent, "Header.value")
		if name != "" {
			headers = append(headers, dsl.HeaderEntry{Name: name, Value: value})
		}
	}
	return headers
}

func parseArguments(node *xmlNode) map[string]string {
	vars := map[string]string{}
	for _, arg := range descendantsByAttr(node, "elementType", "Argument") {
		name := prop(arg, "Argument.name")
		value := prop(arg, "Argument.value")
		if name != "" {
			vars[name] = value
		}
	}
	return vars
}

func jmeterNamedValue(node *xmlNode, key string) string {
	if node == nil {
		return ""
	}
	for _, child := range node.Children {
		if child.Name == "name" && strings.TrimSpace(child.Text) == key {
			for _, sibling := range node.Children {
				if sibling.Name == "value" {
					return strings.TrimSpace(sibling.Text)
				}
			}
		}
		if value := jmeterNamedValue(child, key); value != "" {
			return value
		}
	}
	return ""
}

func parseOptionalBool(value string) (bool, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, false
	}
	return strings.EqualFold(value, "true"), true
}

func jmeterBaseName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, `/\`)
	if value == "" {
		return ""
	}
	if idx := strings.LastIndexAny(value, `/\`); idx >= 0 {
		return value[idx+1:]
	}
	return value
}

func parentElement(root *xmlNode, target *xmlNode) *xmlNode {
	for _, child := range root.Children {
		if child == target {
			return root
		}
		if found := parentElement(child, target); found != nil {
			return found
		}
	}
	return nil
}

func descendantsByAttr(node *xmlNode, key string, value string) []*xmlNode {
	var out []*xmlNode
	if node.Attrs[key] == value {
		out = append(out, node)
	}
	for _, child := range node.Children {
		out = append(out, descendantsByAttr(child, key, value)...)
	}
	return out
}

func firstChild(node *xmlNode, name string) *xmlNode {
	for _, child := range node.Children {
		if child.Name == name {
			return child
		}
	}
	return nil
}

func attrOr(node *xmlNode, key string, fallback string) string {
	if node == nil {
		return fallback
	}
	if value := strings.TrimSpace(node.Attrs[key]); value != "" {
		return value
	}
	return fallback
}
