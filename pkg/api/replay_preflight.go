package api

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type ReplayPreflight = importreview.ReplayPreflight

type ReplayPreflightCheck = importreview.ReplayPreflightCheck

var replayTemplateRefPattern = importreview.ReplayTemplateRefPattern

const replayPreflightMaxControlDepth = importreview.ReplayPreflightMaxControlDepth

var buildReplayPreflight = importreview.BuildReplayPreflight

var replayMessagingPreflight = importreview.ReplayMessagingPreflight

var cloneReplayPreflight = importreview.CloneReplayPreflight

var replayActionDefinitionCount = importreview.ReplayActionDefinitionCount

var replayExpectedActionCount = importreview.ReplayExpectedActionCount

var scriptDataSources = importreview.ScriptDataSources

var scriptParameters = importreview.ScriptParameters

var replayRuntimePrerequisites = importreview.ReplayRuntimePrerequisites

var replayEvidenceCaptureCheck = importreview.ReplayEvidenceCaptureCheck

func replayMissingVariables(script *dsl.Script, variables map[string]string) []string {
	return requestimport.MissingVariables(script, variables)
}

var replayPlaceholderVariables = importreview.ReplayPlaceholderVariables

var replayVariableDetail = importreview.ReplayVariableDetail

var replaySecretRefs = importreview.ReplaySecretRefs

var replaySecretAvailable = importreview.ReplaySecretAvailable

var replayDataSourceWarnings = importreview.ReplayDataSourceWarnings

var replayPhaseActions = importreview.ReplayPhaseActions

var replayOrderedActions = importreview.ReplayOrderedActions

var flattenReplayActionsForReadiness = importreview.FlattenReplayActionsForReadiness

var replayControlShell = importreview.ReplayControlShell

var replayDefinitionCount = importreview.ReplayDefinitionCount

var replayExpectedCount = importreview.ReplayExpectedCount

var replayTemplateRefs = importreview.ReplayTemplateRefs

var sortedSetKeys = importreview.SortedSetKeys

var replayCheckStatus = importreview.ReplayCheckStatus

var replayCheckDetail = importreview.ReplayCheckDetail

var replayListDetail = importreview.ReplayListDetail

var replaySecretDetail = importreview.ReplaySecretDetail

var replayDataSourceDetail = importreview.ReplayDataSourceDetail

var replayPluralSuffix = importreview.ReplayPluralSuffix

var nonEmptyLabel = importreview.NonEmptyLabel
