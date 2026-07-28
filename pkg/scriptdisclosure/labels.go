package scriptdisclosure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

// ProjectLabel removes credential literals known to credentialSources from a
// detached display label. It deliberately does not project the executable
// source scripts: import adapters can keep request/auth bytes exact while
// preventing the same values from being copied into names and other descriptive
// surfaces.
func ProjectLabel(label string, credentialSources ...*dsl.Script) string {
	return ProjectLabels([]string{label}, credentialSources...)[0]
}

// ProjectLabels is the bounded bulk form of ProjectLabel. Each credential
// source is decoded and inspected once, so importing a plan with thousands of
// descriptive action labels does not repeatedly project the entire script.
func ProjectLabels(labels []string, credentialSources ...*dsl.Script) []string {
	projectedLabels := append([]string(nil), labels...)
	for _, source := range credentialSources {
		if source == nil {
			continue
		}
		p, err := newLabelProjector(source)
		if err != nil {
			for index := range projectedLabels {
				projectedLabels[index] = credential.Redacted
			}
			return projectedLabels
		}
		labelBytes := 0
		for _, label := range projectedLabels {
			labelBytes += len(label)
		}
		p.configureKnownMatching(labelBytes)
		for index := range projectedLabels {
			projectedLabels[index] = p.projectString(
				projectedLabels[index],
				projectionContext{key: "name"},
			)
		}
	}
	return projectedLabels
}

func newLabelProjector(credentialSource *dsl.Script) (*projector, error) {
	p := &projector{
		credentialParameterNames: credentialParameterNames(credentialSource),
		sensitiveSourceColumns:   sensitiveDataSourceColumns(credentialSource),
	}
	sourcePayload, err := json.Marshal(credentialSource)
	if err != nil {
		return nil, fmt.Errorf("marshal script credential source for label projection: %w", err)
	}
	sourceDecoder := json.NewDecoder(bytes.NewReader(sourcePayload))
	sourceDecoder.UseNumber()
	var sourceRaw any
	if err := sourceDecoder.Decode(&sourceRaw); err != nil {
		return nil, fmt.Errorf("decode script credential source for label projection: %w", err)
	}
	collector := newSecretCollector(p.credentialParameterNames, p.sensitiveSourceColumns)
	collector.collect(sourceRaw, projectionContext{}, 0)
	collector.collectScriptCredentialTargets(credentialSource)
	importedValues, err := engine.ImportedCredentialValuesForDisclosure(credentialSource)
	if err != nil {
		return nil, fmt.Errorf("resolve imported credential locators for label disclosure: %w", err)
	}
	if len(importedValues) > 0 {
		p.forcedSecrets = make(map[string]struct{}, len(importedValues)*2)
		for _, value := range importedValues {
			collector.add(value)
			p.forcedSecrets[value] = struct{}{}
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				p.forcedSecrets[trimmed] = struct{}{}
			}
		}
	}
	p.knownSecrets = collector.sorted()
	return p, nil
}
