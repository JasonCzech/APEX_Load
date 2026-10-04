package raml

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/srcparse"
	"gopkg.in/yaml.v3"
)

// The loader only reads the supplied archive, never the filesystem or network.
// References are relative to their declaring document; leading '/' is the
// root RAML document directory convention used by Anypoint exports.
type ramlLoader struct {
	files     map[string][]byte
	active    map[string]bool
	remaining int
	rootDir   string
	warnings  []string
}

func parseRAMLDocument(content string, files map[string][]byte) (map[string]any, error) {
	return loadRAMLDocument(content, files, "")
}

func loadRAMLDocument(content string, files map[string][]byte, rootFile string) (map[string]any, error) {
	if rootFile == "" {
		for _, name := range sortedFileKeys(files) {
			if bytes.Equal(files[name], []byte(content)) {
				rootFile = name
				break
			}
		}
	}
	loader := &ramlLoader{files: files, active: map[string]bool{}, remaining: 200000, rootDir: path.Dir(rootFile)}
	doc, err := loader.document([]byte(content), rootFile, 0)
	if err != nil {
		return nil, err
	}
	if len(doc) == 0 {
		return nil, fmt.Errorf("RAML document is empty")
	}
	if err := loader.libraries(doc, rootFile, "", doc, 0); err != nil {
		return nil, err
	}
	doc["__raml_warnings"] = loader.warnings
	return doc, nil
}

func sortedFileKeys(files map[string][]byte) []string {
	values := make(map[string]any, len(files))
	for key := range files {
		values[key] = nil
	}
	return sortedMapKeys(values)
}

func (l *ramlLoader) reference(name, source string) (string, []byte, error) {
	if strings.Contains(name, ":") || strings.Contains(name, "\\") {
		return "", nil, fmt.Errorf("RAML reference %q must name an archive file", name)
	}
	target := path.Clean(path.Join(path.Dir(source), name))
	if strings.HasPrefix(name, "/") {
		target = path.Clean(path.Join(l.rootDir, strings.TrimLeft(name, "/")))
	}
	if target == ".." || strings.HasPrefix(target, "../") {
		return "", nil, fmt.Errorf("RAML reference %q leaves archive root", name)
	}
	content, ok := l.files[target]
	if !ok {
		return "", nil, fmt.Errorf("RAML reference %q from %q could not be resolved; upload the referenced files (zip)", name, source)
	}
	return target, content, nil
}

func (l *ramlLoader) document(content []byte, source string, depth int) (map[string]any, error) {
	root, err := l.node(content, source, depth)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := root.Decode(&doc); err != nil {
		if source == "" {
			return nil, fmt.Errorf("parse RAML: %w", err)
		}
		return nil, fmt.Errorf("parse RAML %q: %w", source, err)
	}
	return doc, nil
}

func (l *ramlLoader) node(content []byte, source string, depth int) (*yaml.Node, error) {
	if depth > 20 || l.active[source] {
		return nil, fmt.Errorf("RAML cyclic reference or reference depth exceeded at %q", source)
	}
	l.active[source] = true
	defer delete(l.active, source)
	if len(bytes.TrimSpace(content)) == 0 {
		l.warnings = append(l.warnings, fmt.Sprintf("RAML referenced file %q is empty; no example payload was available", source))
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}
	if strings.EqualFold(path.Ext(source), ".json") {
		if err := srcparse.ValidateJSON(content, srcparse.Limits{}); err != nil {
			return nil, fmt.Errorf("parse RAML JSON %q: %w", source, err)
		}
	} else {
		policy := srcparse.SpecYAMLPolicy()
		// RAML type property names and payload object keys are case-sensitive.
		// Exact duplicate keys still fail yaml.Node.Decode; depth and non-scalar
		// key guards remain in force for every referenced fragment.
		if depth > 0 {
			policy.RejectDuplicateKeys = false
		}
		if _, err := srcparse.ValidateYAMLMappingKeys(content, policy, srcparse.Limits{}); err != nil {
			if source == "" {
				return nil, fmt.Errorf("parse RAML: %w", err)
			}
			return nil, fmt.Errorf("parse RAML %q: %w", source, err)
		}
	}
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, err
	}
	if err := l.includes(&root, source, depth, 0); err != nil {
		return nil, err
	}
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		return root.Content[0], nil
	}
	return &root, nil
}

func (l *ramlLoader) includes(node *yaml.Node, source string, depth, nesting int) error {
	l.remaining--
	if l.remaining < 0 || nesting > 128 {
		return fmt.Errorf("RAML expanded document exceeds structural limits")
	}
	if node.Tag == "!include" {
		name, content, err := l.reference(strings.TrimSpace(node.Value), source)
		if err != nil {
			return err
		}
		if !includeIsStructured(name) {
			*node = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: string(content)}
			return nil
		}
		resolved, err := l.node(content, name, depth+1)
		if err != nil {
			return err
		}
		*node = *resolved
		return nil
	}
	for _, child := range node.Content {
		if err := l.includes(child, source, depth, nesting+1); err != nil {
			return err
		}
	}
	return nil
}

// Libraries are collected under their alias. Local type/trait references inside
// each library are qualified before merging, so same-named assets stay isolated.
func (l *ramlLoader) libraries(doc map[string]any, source, prefix string, root map[string]any, depth int) error {
	if depth > 20 || l.active[source] {
		return fmt.Errorf("RAML cyclic library or reference depth exceeded at %q", source)
	}
	l.active[source] = true
	defer delete(l.active, source)
	for _, alias := range sortedMapKeys(mapValue(doc["uses"])) {
		name, content, err := l.reference(stringValue(mapValue(doc["uses"])[alias]), source)
		if err != nil {
			return err
		}
		library, err := l.document(content, name, depth+1)
		if err != nil {
			return err
		}
		namespace := prefix + alias + "."
		if err := l.libraries(library, name, namespace, root, depth+1); err != nil {
			return err
		}
	}
	if prefix == "" {
		return nil
	}
	names := map[string]string{}
	for _, section := range []string{"types", "traits", "resourceTypes", "securitySchemes"} {
		for name := range mapValue(doc[section]) {
			names[name] = prefix + name
		}
	}
	for alias := range mapValue(doc["uses"]) {
		names[alias] = prefix + alias
	}
	for _, section := range []string{"types", "traits", "resourceTypes", "securitySchemes"} {
		target := mapValue(root[section])
		if target == nil {
			target = map[string]any{}
			root[section] = target
		}
		for _, name := range sortedMapKeys(mapValue(doc[section])) {
			qualified := prefix + name
			if _, exists := target[qualified]; exists {
				return fmt.Errorf("RAML duplicate library asset %q", qualified)
			}
			target[qualified] = qualifyRAML(mapValue(doc[section])[name], names, "")
		}
	}
	return nil
}

var ramlTypeToken = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_.-]*`)

func qualifyRAML(value any, names map[string]string, field string) any {
	switch v := value.(type) {
	case string:
		if field != "type" && field != "items" && field != "is" && field != "securedBy" {
			return v
		}
		return ramlTypeToken.ReplaceAllStringFunc(v, func(token string) string {
			head, tail, _ := strings.Cut(token, ".")
			if replacement, ok := names[head]; ok {
				if tail != "" {
					return replacement + "." + tail
				}
				return replacement
			}
			return token
		})
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = qualifyRAML(item, names, field)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			dest := key
			if field == "is" || field == "securedBy" || field == "type" {
				dest = stringValue(qualifyRAML(key, names, field))
			}
			out[dest] = qualifyRAML(item, names, key)
		}
		return out
	default:
		return value
	}
}

// Legacy helper retained for direct callers; only exact normalized archive paths
// resolve. A basename fallback can silently substitute an unrelated payload.
func lookupInclude(files map[string][]byte, name string) ([]byte, bool) {
	_, content, err := (&ramlLoader{files: files}).reference(name, "")
	return content, err == nil
}
