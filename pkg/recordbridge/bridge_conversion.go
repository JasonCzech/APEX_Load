package recordbridge

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/importreview"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

func configureStandaloneConversion(service *Service) {
	service.Library = &MemoryScriptLibrary{}
	ports := NewMemoryDraftPorts(nil, nil)
	response := ports.Response
	ports.Response = func(r *http.Request, opts importreview.ScriptImportResponseOptions) map[string]any {
		body := response(r, opts)
		body["secret_store"] = map[string]any{"provider": "unavailable", "writable": false, "create_supported": false, "replace_supported": false, "source_secret_migration_policy_enabled": false}
		return body
	}
	service.Conversion = ConversionPorts{Drafts: ports, WriteImportResponse: WriteMemoryDraftResponse, ProjectRecordingProfile: func(_, id string) (*recordingprofile.Profile, error) {
		if id != "" {
			return nil, errors.New("recording profiles need a project")
		}
		return nil, nil
	}}
	service.Conversion.SessionRecordingProfile = func(session *recorder.Session, id string) (*recordingprofile.Profile, error) {
		if id == "" {
			id = session.Filters.RecordingProfileID
		}
		var profile *recordingprofile.Profile
		if id != "" && strings.EqualFold(id, session.Filters.RecordingProfileID) {
			profile = service.Device.recordingProfile(session.ID)
		}
		if profile == nil {
			return service.Conversion.ProjectRecordingProfile(normalizeProjectID(session.ProjectID), id)
		}
		scoped := *profile
		scoped.IncludeHosts = mergeRecorderHostFilters(profile.IncludeHosts, session.Filters.IncludeHosts)
		scoped.ExcludeHosts = mergeRecorderHostFilters(profile.ExcludeHosts, session.Filters.ExcludeHosts)
		return &scoped, nil
	}
}
