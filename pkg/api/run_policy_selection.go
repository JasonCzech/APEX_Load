package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

// RunAnalysisPolicySelection is an authored immutable revision request. Only
// admission creates a RunPolicyPin with the server-computed definition digest.
type RunAnalysisPolicySelection struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

type RunAnalysisPolicySelections struct {
	Browser *RunAnalysisPolicySelection `json:"browser,omitempty"`
	Journey *RunAnalysisPolicySelection `json:"journey,omitempty"`
}

// UnmarshalJSON keeps this new request field closed even when the containing
// legacy run-creation request tolerates unknown fields. Digests, duplicate
// members and unsupported policy kinds cannot masquerade as authored choices.
func (selections *RunAnalysisPolicySelections) UnmarshalJSON(data []byte) error {
	type wire RunAnalysisPolicySelections
	var decoded wire
	if err := decodeAnalysisDefinitionJSON(data, &decoded); err != nil {
		return err
	}
	*selections = RunAnalysisPolicySelections(decoded)
	return nil
}

func cloneRunAnalysisPolicySelections(in *RunAnalysisPolicySelections) *RunAnalysisPolicySelections {
	if in == nil {
		return nil
	}
	out := *in
	if in.Browser != nil {
		copied := *in.Browser
		out.Browser = &copied
	}
	if in.Journey != nil {
		copied := *in.Journey
		out.Journey = &copied
	}
	return &out
}

func normalizeRunAnalysisPolicySelections(in *RunAnalysisPolicySelections) (*RunAnalysisPolicySelections, error) {
	if in == nil {
		return nil, nil
	}
	for _, choice := range []*RunAnalysisPolicySelection{in.Browser, in.Journey} {
		if choice == nil {
			continue
		}
		if choice.ID == "" || choice.ID != strings.TrimSpace(choice.ID) || len(choice.ID) > 256 || choice.Revision < 1 {
			return nil, fmt.Errorf("%w: policy selection needs an ID and a positive immutable revision", errRunPolicyConfiguration)
		}
	}
	return cloneRunAnalysisPolicySelections(in), nil
}

func (selections *RunAnalysisPolicySelections) forKind(kind string) *RunAnalysisPolicySelection {
	if selections == nil {
		return nil
	}
	switch kind {
	case storage.AnalysisDefinitionKindBrowserPolicy:
		return selections.Browser
	case storage.AnalysisDefinitionKindJourneyPolicy:
		return selections.Journey
	default:
		return nil
	}
}

func decodeStoredRunAnalysisPolicySelections(value any) *RunAnalysisPolicySelections {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	var selected *RunAnalysisPolicySelections
	if err == nil {
		err = decodeAnalysisDefinitionJSON(data, &selected)
	}
	if err != nil {
		// An unreadable explicit request must never become a default selection.
		return &RunAnalysisPolicySelections{Browser: &RunAnalysisPolicySelection{}}
	}
	return selected
}

func runAnalysisPolicySelectionsForStorage(in *RunAnalysisPolicySelections) any {
	if in == nil {
		return nil
	}
	return cloneRunAnalysisPolicySelections(in)
}

func selectedRunAnalysisPolicy(store storage.AnalysisDefinitionStore, project, kind string, choice *RunAnalysisPolicySelection) (*storage.AnalysisDefinition, error) {
	reader, ok := store.(storage.AnalysisPolicySelectionStore)
	if !ok {
		return nil, fmt.Errorf("%w: storage does not support immutable run policy selection", errRunPolicyConfiguration)
	}
	selected, latest, err := reader.GetAnalysisPolicyRevision(project, choice.ID, choice.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: selected policy revision is unavailable in this project", errRunPolicyConfiguration)
	}
	if err != nil {
		return nil, err
	}
	if selected == nil || latest == nil || selected.Deleted || latest.Deleted || selected.ProjectID != project || latest.ProjectID != project || selected.ID != choice.ID || latest.ID != choice.ID || selected.Kind != kind || latest.Kind != kind || selected.Revision != choice.Revision || latest.Revision < selected.Revision {
		return nil, fmt.Errorf("%w: selected policy revision is unavailable in this project", errRunPolicyConfiguration)
	}
	if _, err := decodeAnalysisPolicy(kind, selected.Definition); err != nil {
		return nil, fmt.Errorf("%w: selected policy revision is invalid", errRunPolicyConfiguration)
	}
	return selected, nil
}

func (tr *TestRunner) validateSelectedRunAnalysisPolicies(project string, selections *RunAnalysisPolicySelections) error {
	if selections == nil || selections.Browser == nil && selections.Journey == nil {
		return nil
	}
	store, ok := tr.testStore.(storage.AnalysisDefinitionStore)
	if !ok || store == nil {
		return fmt.Errorf("%w: storage does not support run policy selection", errRunPolicyConfiguration)
	}
	for _, kind := range []string{storage.AnalysisDefinitionKindBrowserPolicy, storage.AnalysisDefinitionKindJourneyPolicy} {
		if choice := selections.forKind(kind); choice != nil {
			if _, err := selectedRunAnalysisPolicy(store, normalizeProjectID(project), kind, choice); err != nil {
				return err
			}
		}
	}
	return nil
}
