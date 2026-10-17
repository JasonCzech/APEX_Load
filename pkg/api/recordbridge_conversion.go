package api

import (
	"context"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recordbridge"
	"net/http"
	"time"
)

// populateRecordingConversionPorts binds durable controller policy and draft
// ownership to the same conversion workflow the standalone recorder uses.
func (s *Server) populateRecordingConversionPorts(service *recordbridge.Service) {
	service.Conversion = recordbridge.ConversionPorts{
		Drafts: &recordbridge.DraftPorts{
			Response: s.newScriptImportResponseWithSourceSecrets,
			Claim: func(r *http.Request, script *dsl.Script) (*recordbridge.DraftClaim, error) {
				claim, err := s.claimImportDraftMutation(r, script)
				if err != nil || claim == nil {
					return nil, err
				}
				return &recordbridge.DraftClaim{Script: claim.Script, Profile: claim.Profile,
					Complete: func(r *http.Request, script *dsl.Script) (time.Time, error) {
						return s.completeImportDraftMutation(r, claim, script)
					},
					Release: func() { s.releaseImportDraftAttestation(claim) },
				}, nil
			},
			WriteClaimError: writeImportDraftBusy,
		},
		RequireImportFeature: func(w http.ResponseWriter, project, profile string) bool {
			return s.requireDomainFeatureForProject(w, project, featureIDForImportProfile(normalizeGenericImportSourceProfile(profile)))
		},
		WriteImportResponse:     writeScriptImportResponse,
		ProjectRecordingProfile: s.projectRecordingProfile,
		SessionRecordingProfile: s.sessionRecordingProfile,
		RequireExecutionLeader:  s.requireExecutionLeader,
		ExecutionContext: func() (context.Context, context.CancelFunc) {
			return s.runner.executionOperationContext(context.Background())
		},
		WriteReplayStartError: writeBrowserReplayStartError,
	}
}
