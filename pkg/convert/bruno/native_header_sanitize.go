package bruno

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/scrub"
)

// sanitizeNativePolicyHeaders extends the native-only source scrub to every
// header recognized by the shared credential policy without changing the
// canonical importer's historical header allowlist.
func sanitizeNativePolicyHeaders(
	files []sourceFile,
) ([]sourceFile, []SecretRequirement, int, error) {
	out := make([]sourceFile, 0, len(files))
	requirements := make([]SecretRequirement, 0)
	sanitizedValues := 0
	for _, file := range files {
		extension := strings.ToLower(path.Ext(file.Path))
		role := classifyFileRole(file.Path, file.Data)
		if (extension != ".yml" && extension != ".yaml") ||
			(role != "request" && role != "folder" && role != "collection") {
			out = append(out, file)
			continue
		}
		var document map[string]any
		if err := yaml.Unmarshal(file.Data, &document); err != nil {
			return nil, nil, 0, fmt.Errorf("parse native header source %q: %w", file.Path, err)
		}
		containerName := "request"
		if role == "request" {
			containerName = "http"
		}
		container := nativeMapValue(document, containerName)
		headers := nativeLookup(container, "headers")
		changed := false
		occurrence := 0
		switch typed := headers.(type) {
		case []any:
			for index, raw := range typed {
				header, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				name := strings.TrimSpace(anyString(nativeLookup(header, "name")))
				if !nativeSensitiveHeaderName(name) {
					continue
				}
				value, ok := nativeLookup(header, "value").(string)
				if !ok || strings.TrimSpace(value) == "" ||
					isGovernedSecretHeaderValue(name, value, []string{"headers"}, role) {
					continue
				}
				pointer := "/" + nativeJSONPointerEscape(containerName) +
					"/headers/" + strconv.Itoa(index) + "/value"
				if nativeBool(nativeLookup(header, "disabled")) {
					if _, err := setNativeJSONPointerWithAliasPeers(
						document,
						file.nativeAliasGroups,
						pointer,
						scrub.Redacted,
					); err != nil {
						return nil, nil, 0, fmt.Errorf("scrub disabled native header in %q: %w", file.Path, err)
					}
					changed = true
					continue
				}
				if brunoTemplatePattern.MatchString(value) || !shouldSanitizeCredentialValue(value) {
					continue
				}
				occurrence++
				requirement := nativeComposedHeaderSecretRequirement(
					file.Path,
					pointer,
					name,
					occurrence,
					"Bruno sensitive header "+name,
				)
				requirement.Value = recoverableBrunoCredentialValue(value)
				requirement = describeNativeHeaderSecretRequirement(requirement, name, value)
				if _, err := setNativeJSONPointerWithAliasPeers(
					document,
					file.nativeAliasGroups,
					pointer,
					nativeCredentialProjectionReplacement(requirement),
				); err != nil {
					return nil, nil, 0, fmt.Errorf("write sanitized native header in %q: %w", file.Path, err)
				}
				changed = true
				sanitizedValues++
				requirements = append(requirements, requirement)
			}
		case map[string]any:
			names := make([]string, 0, len(typed))
			for name := range typed {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if !nativeSensitiveHeaderName(name) {
					continue
				}
				value, ok := typed[name].(string)
				if !ok || strings.TrimSpace(value) == "" ||
					isGovernedSecretHeaderValue(name, value, []string{"headers"}, role) ||
					brunoTemplatePattern.MatchString(value) ||
					!shouldSanitizeCredentialValue(value) {
					continue
				}
				occurrence++
				pointer := "/" + nativeJSONPointerEscape(containerName) +
					"/headers/" + nativeJSONPointerEscape(name)
				requirement := nativeComposedHeaderSecretRequirement(
					file.Path,
					pointer,
					name,
					occurrence,
					"Bruno sensitive header "+name,
				)
				requirement.Value = recoverableBrunoCredentialValue(value)
				requirement = describeNativeHeaderSecretRequirement(requirement, name, value)
				if _, err := setNativeJSONPointerWithAliasPeers(
					document,
					file.nativeAliasGroups,
					pointer,
					nativeCredentialProjectionReplacement(requirement),
				); err != nil {
					return nil, nil, 0, fmt.Errorf("write sanitized native header in %q: %w", file.Path, err)
				}
				changed = true
				sanitizedValues++
				requirements = append(requirements, requirement)
			}
		}
		if !changed {
			out = append(out, file)
			continue
		}
		content, err := yaml.Marshal(document)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("serialize sanitized native header source %q: %w", file.Path, err)
		}
		out = append(out, sourceFile{
			Path:              file.Path,
			Data:              content,
			Mode:              file.Mode,
			nativeAliasGroups: file.nativeAliasGroups,
		})
	}
	sort.Slice(requirements, func(i, j int) bool {
		if requirements[i].Path == requirements[j].Path {
			return requirements[i].Key < requirements[j].Key
		}
		return requirements[i].Path < requirements[j].Path
	})
	return out, requirements, sanitizedValues, nil
}

func nativeSensitiveHeaderName(name string) bool {
	return (scrub.Policy{}).SensitiveHeader(name)
}
