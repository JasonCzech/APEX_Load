package bruno

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

const (
	nativeYAMLAliasMaxDepth    = 32
	nativeYAMLAliasMaxExpands  = 4096
	nativeYAMLAliasMaxPointers = 1 << 20
)

// nativeYAMLAliasGroups records equivalent source pointers for anchored nodes
// and every alias that references them. Walking through an alias records
// descendant equivalence too, so a scalar inside an anchored mapping can be
// scrubbed at both its definition and each expanded use. The mapping carries
// no source values and is used only while building the sanitized native
// artifact.
func nativeYAMLAliasGroups(data []byte) (map[string][]string, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return nil, nil
	}

	nodePointers := make(map[*yaml.Node][]string)
	aliasExpansions := 0
	pointerVisits := 0
	var walk func(*yaml.Node, string, int, map[*yaml.Node]struct{}) error
	walk = func(
		node *yaml.Node,
		pointer string,
		aliasDepth int,
		active map[*yaml.Node]struct{},
	) error {
		if node == nil {
			return errors.New("Bruno YAML contains an empty node")
		}
		if node.Kind == yaml.AliasNode {
			if node.Alias == nil {
				return errors.New("Bruno YAML contains an unresolved alias")
			}
			if aliasDepth >= nativeYAMLAliasMaxDepth {
				return fmt.Errorf(
					"Bruno YAML alias expansion exceeds depth %d",
					nativeYAMLAliasMaxDepth,
				)
			}
			aliasExpansions++
			if aliasExpansions > nativeYAMLAliasMaxExpands {
				return fmt.Errorf(
					"Bruno YAML alias expansion exceeds limit %d",
					nativeYAMLAliasMaxExpands,
				)
			}
			if _, cycle := active[node.Alias]; cycle {
				return errors.New("Bruno YAML contains a cyclic alias")
			}
			return walk(node.Alias, pointer, aliasDepth+1, active)
		}
		pointerVisits++
		if pointerVisits > nativeYAMLAliasMaxPointers {
			return fmt.Errorf(
				"Bruno YAML alias pointer expansion exceeds limit %d",
				nativeYAMLAliasMaxPointers,
			)
		}
		if pointer != "" {
			nodePointers[node] = append(nodePointers[node], pointer)
		}
		if _, cycle := active[node]; cycle {
			return errors.New("Bruno YAML contains a cyclic node")
		}
		active[node] = struct{}{}
		defer delete(active, node)
		switch node.Kind {
		case yaml.MappingNode:
			for index := 0; index+1 < len(node.Content); index += 2 {
				key := node.Content[index].Value
				if err := walk(
					node.Content[index+1],
					pointer+"/"+nativeJSONPointerEscape(key),
					aliasDepth,
					active,
				); err != nil {
					return err
				}
			}
		case yaml.SequenceNode:
			for index, child := range node.Content {
				if err := walk(
					child,
					pointer+"/"+strconv.Itoa(index),
					aliasDepth,
					active,
				); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(document.Content[0], "", 0, make(map[*yaml.Node]struct{})); err != nil {
		return nil, err
	}

	groups := make(map[string][]string)
	for _, pointers := range nodePointers {
		if len(pointers) < 2 {
			continue
		}
		members := append([]string(nil), pointers...)
		sort.Strings(members)
		members = compactNativeStrings(members)
		if len(members) < 2 {
			continue
		}
		for _, member := range members {
			identity, err := nativeAliasPointerIdentity(member)
			if err != nil {
				return nil, err
			}
			groups[identity] = members
		}
	}
	return groups, nil
}

// nativeAliasPointerIdentity mirrors nativeMapKey's case-insensitive,
// whitespace-tolerant lookup semantics without changing the exact authored
// pointer later used for mutation. Decode before folding and re-escape after so
// JSON-pointer separators remain structural and authored '/'/'~' key bytes are
// not confused with pointer syntax.
func nativeAliasPointerIdentity(pointer string) (string, error) {
	segments, err := nativeJSONPointerSegments(pointer)
	if err != nil {
		return "", err
	}
	for index, segment := range segments {
		segments[index] = nativeJSONPointerEscape(
			cases.Fold().String(norm.NFC.String(strings.TrimSpace(segment))),
		)
	}
	return "/" + strings.Join(segments, "/"), nil
}

func compactNativeStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value == out[len(out)-1] {
			continue
		}
		out = append(out, value)
	}
	return out
}
