package enterprisearchive

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
)

// web_reg_save_param_attrib arguments that configure the registration rather
// than describe the element. Every other key=value argument before
// SEARCH_FILTERS is an attribute the element must carry.
var sourceAttribControlArguments = map[string]bool{
	"paramname": true, "tagname": true, "tag": true, "extract": true, "attribute": true, "attributename": true,
	"ordinal": true, "ord": true, "notfound": true, "savelen": true, "saveoffset": true, "convert": true,
	"dfes": true, "selectall": true,
	// Search filters, in case a script omits the SEARCH_FILTERS marker.
	"requesturl": true, "ignoreredirections": true, "contenttype": true, "headernames": true, "relframeid": true,
}

// LoadRunner's Scope= search-filter values. Scope is also an HTML attribute
// (<th scope="col">), so only these values mark it as a filter.
var sourceAttribScopeFilterValues = map[string]bool{"all": true, "body": true, "headers": true, "cookies": true, "noresource": true}

var (
	sourceAttribNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:-]*$`)
	sourceAttribTagPattern  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)
)

// sourceAttribSelector maps web_reg_save_param_attrib onto an xpath over the
// HTML response: VuGen's TagName= and Extract= (Tag=/Attribute= are accepted
// too) select //tag/@extract, and the element-attribute arguments become
// predicates, so "TagName=input", "Extract=value", "Name=sid", "Type=hidden"
// reads //input[@name='sid'][@type='hidden']/@value. HTML attribute names are
// case-insensitive (the parser lowercases them); values match exactly.
func sourceAttribSelector(fn ParsedFunction, paramName, mode string, ordinal int) (string, []string, bool) {
	var warnings []string
	extract := strings.TrimSpace(cleanConfigValue(convertutil.FirstNonEmpty(
		firstNonEmptyArg(fn, "Extract"), firstNonEmptyArg(fn, "Attribute"), firstNonEmptyArg(fn, "AttributeName"))))
	if extract == "" {
		return "", append(warnings, fmt.Sprintf("line %d: web_reg_save_param_attrib %q has no Extract= attribute to save; preserved for manual review", fn.LineNumber, paramName)), false
	}
	if !sourceAttribNamePattern.MatchString(extract) {
		return "", append(warnings, fmt.Sprintf("line %d: web_reg_save_param_attrib %q extracts %q, which is not an attribute name; preserved for manual review", fn.LineNumber, paramName, extract)), false
	}
	node := "//*"
	tag := strings.TrimSpace(cleanConfigValue(convertutil.FirstNonEmpty(firstNonEmptyArg(fn, "TagName"), firstNonEmptyArg(fn, "Tag"))))
	switch {
	case sourceAttribTagPattern.MatchString(tag):
		node = "//" + strings.ToLower(tag)
	case tag != "":
		warnings = append(warnings, fmt.Sprintf("line %d: web_reg_save_param_attrib %q tag %q is not an element name; any element is matched", fn.LineNumber, paramName, tag))
	}
	var predicates strings.Builder
	for _, arg := range fn.CallArgs {
		if arg.Kind == "identifier" && strings.EqualFold(arg.Value, "SEARCH_FILTERS") || arg.Kind == "macro" {
			break
		}
		key := strings.ToLower(strings.TrimSpace(arg.Name))
		if arg.Kind != "key_value" || sourceAttribControlArguments[key] ||
			key == "scope" && sourceAttribScopeFilterValues[strings.ToLower(strings.TrimSpace(cleanConfigValue(arg.Value)))] {
			continue
		}
		name, value := strings.TrimSpace(arg.Name), cleanConfigValue(arg.Value)
		switch {
		case !sourceAttribNamePattern.MatchString(name):
			warnings = append(warnings, fmt.Sprintf("line %d: web_reg_save_param_attrib %q filter %q is not an attribute name and was not applied", fn.LineNumber, paramName, name))
		case strings.ContainsAny(value, "{}"):
			warnings = append(warnings, fmt.Sprintf("line %d: web_reg_save_param_attrib %q filter %s=%s uses a parameter, which an xpath predicate cannot resolve; it was not applied, so verify the extracted element", fn.LineNumber, paramName, name, value))
		default:
			fmt.Fprintf(&predicates, "[@%s=%s]", strings.ToLower(name), xpathStringLiteral(value))
		}
	}
	expression := node + predicates.String() + "/@" + strings.ToLower(extract)
	if mode == "" && ordinal > 1 {
		expression = fmt.Sprintf("(%s)[%d]", expression, ordinal)
	}
	return expression, warnings, true
}

// xpathStringLiteral quotes value for XPath 1.0, which has no escapes: a value
// holding both quote kinds is assembled with concat().
func xpathStringLiteral(value string) string {
	switch {
	case !strings.Contains(value, "'"):
		return "'" + value + "'"
	case !strings.Contains(value, `"`):
		return `"` + value + `"`
	}
	parts := strings.Split(value, "'")
	quoted := make([]string, 0, 2*len(parts))
	for index, part := range parts {
		if index > 0 {
			quoted = append(quoted, `"'"`)
		}
		if part != "" {
			quoted = append(quoted, "'"+part+"'")
		}
	}
	return "concat(" + strings.Join(quoted, ", ") + ")"
}
