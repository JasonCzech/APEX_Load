package api

import (
	"errors"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

// errSavedImportEvaluation answers a save whose import evaluation cannot be
// rebuilt. The script is not saved.
var errSavedImportEvaluation = errors.New("cannot evaluate the imported script's dependencies; reload and retry")

// savedImportEvaluationRefresh asks a durable update to rebuild an imported
// script's server-owned import evaluation once the submitted script has
// passed the write's credential and binding checks. An editor save rebuilds
// all of it (refreshSavedImportEvaluation); secrets answers the refreshed
// preflight's project-secret check from the names resolved before the write
// took its locks (savedImportSecretSnapshot). A guided repair on an ordinary
// import has rebuilt its report and preflight already and persists without
// its dependency codes, so the write's binding gate never judges them
// (processScriptRepair); dependencyCodesOnly stamps them again.
type savedImportEvaluationRefresh struct {
	secrets             engine.SecretResolver
	dependencyCodesOnly bool
}

// apply rebuilds what refresh asks for on script, the prepared candidate of
// a durable write over prior.
func (refresh *savedImportEvaluationRefresh) apply(tr *TestRunner, script, prior *dsl.Script) error {
	if refresh.dependencyCodesOnly {
		if _, err := importreview.RefreshDependencies(script); err != nil {
			return errSavedImportEvaluation
		}
		return nil
	}
	return tr.refreshSavedImportEvaluation(script, prior, refresh.secrets)
}

// UpdateScriptEditorSaveDurable writes an ordinary editor save (PUT
// /scripts/{id}): bound to expected when the editor sent its revision, or an
// ownership-bound compatibility write in projectID otherwise. Either way it
// keeps every guard of the durable update and then rebuilds the imported
// script's server-owned import evaluation against the exact durable prior.
// The project secrets that evaluation reports are resolved first, before any
// lock is taken.
func (tr *TestRunner) UpdateScriptEditorSaveDurable(id string, script *dsl.Script, expected *storage.ScriptAuthoringPrecondition, projectID string) (*StoredScript, bool, error) {
	if expected == nil {
		refresh := &savedImportEvaluationRefresh{secrets: tr.savedImportSecretSnapshot(id, projectID, script)}
		return tr.updateScriptDurableInProjectRefreshing(id, script, projectID, time.Time{}, "", "", time.Time{}, false, "", refresh)
	}
	if !storage.ValidScriptContentRevision(expected.ContentRevision) {
		return nil, false, storage.ErrScriptAuthoringInvalidRevision
	}
	refresh := &savedImportEvaluationRefresh{secrets: tr.savedImportSecretSnapshot(id, expected.ProjectID, script)}
	return tr.updateScriptDurableInProjectRefreshing(id, script, expected.ProjectID, time.Time{}, "", "", time.Time{}, false, "", refresh, expected)
}

// savedImportSecretSnapshot resolves the secrets an editor save of an
// imported script references, where the script's binding resolves them (an
// assigned application's secrets ahead of its project's), and returns a
// resolver that answers from that result alone. The durable write restamps
// the review's preflight with it, so it does no secret-provider I/O while it
// holds the script mutation lock and the secret-reference lease, and the
// persisted preflight still never lists a configured secret as missing, as a
// repair's does (repairReplayPreflight). A secret the write's prepared script
// references beyond the submitted one reads as missing. A script without
// import provenance is never refreshed, so nothing is resolved for it.
func (tr *TestRunner) savedImportSecretSnapshot(id, projectID string, script *dsl.Script) engine.SecretResolver {
	if script == nil || scriptImportSource(script) == "" {
		return nil
	}
	scoped := *script
	scoped.ID = id
	if strings.TrimSpace(projectID) != "" {
		scoped.ProjectID = projectID
	}
	required, missing := replaySecretRefs(&scoped, tr.secretResolverForBinding(tr.secretBindingForScript(&scoped)))
	resolved := make(map[string]struct{}, len(required))
	for _, name := range required {
		resolved[name] = struct{}{}
	}
	for _, name := range missing {
		delete(resolved, name)
	}
	return engine.SecretResolverFunc(func(name string) (string, bool) {
		if _, ok := resolved[strings.TrimSpace(name)]; ok {
			// Presence is all the preflight reads; the value is never used.
			return savedImportResolvedSecretMarker, true
		}
		return "", false
	})
}

// savedImportResolvedSecretMarker stands in for a secret value the snapshot
// resolved. The preflight counts resolved names and discards their values.
const savedImportResolvedSecretMarker = "resolved-before-save"

// refreshSavedImportEvaluation rebuilds the server-owned import evaluation of
// an ordinary save of an imported script, as a stored repair does
// (evaluateRepairedScript): the dependency review codes on its steps, the
// preserved fidelity report and the review's replay preflight. The editor
// sends back the codes and report it read, so without this a save that fixes
// a producer's order would keep the stale code, and validation and launch
// would refuse the script over a problem it no longer has.
//
// The evaluation is server output, so it starts from the prior's report and
// preflight, never the client's, and it runs only once the submitted script
// has passed the save's credential checks. A review code or preflight line
// can quote a variable name that disclosure masks; judged as submitted
// content, that would refuse the save. Only a script
// savedImportEvaluationRefreshed accepts is rebuilt. The preflight's project
// secrets come from secrets, resolved before the write
// (savedImportSecretSnapshot).
func (tr *TestRunner) refreshSavedImportEvaluation(script, prior *dsl.Script, secrets engine.SecretResolver) error {
	if !savedImportEvaluationRefreshed(script, prior) {
		return nil
	}
	source := scriptImportSource(prior)
	_, reported := prior.Metadata.Import.Preserved["fidelity_report"]
	preserved := ensureScriptImportPreserved(script, source)
	for _, key := range storedRepairEvaluationKeys {
		value, kept := prior.Metadata.Import.Preserved[key]
		if !kept {
			delete(preserved, key)
			continue
		}
		cloned, err := cloneImportSecurityMetadata(value)
		if err != nil {
			return errSavedImportEvaluation
		}
		preserved[key] = cloned
	}
	if reported {
		// The persisted report never reads project secrets, so no resolver.
		if _, _, _, _, err := evaluateRepairedScript(script, nil); err != nil {
			return errSavedImportEvaluation
		}
	} else if _, err := importreview.RefreshDependencies(script); err != nil {
		return errSavedImportEvaluation
	}
	if preservedReplayPreflight(prior) != nil {
		attachImportReplayPreflight(script, source, repairReplayPreflight(script, prior, secrets))
	}
	return nil
}

// savedImportEvaluationRefreshed reports whether an editor save of script
// over prior rebuilds the import evaluation (refreshSavedImportEvaluation).
// A script whose import never evaluated its dependencies (no fidelity report
// and no dependency code on a step) is left as its importer wrote it, and so
// is a Java Vuser script, which changes only through authoring approval, or a
// save that drops the import provenance.
func savedImportEvaluationRefreshed(script, prior *dsl.Script) bool {
	if script == nil || scriptImportSource(prior) == "" || scriptImportSource(script) == "" ||
		scriptRequiresJavaVuserAuthoringApproval(prior) {
		return false
	}
	_, reported := prior.Metadata.Import.Preserved["fidelity_report"]
	return reported || scriptHasDependencyCodes(prior)
}

// removeSubmittedDependencyCodes drops the dependency review codes an editor
// sends back with an ordinary (not credential-preserving) save of an
// evaluated import, before the save's disclosure-safe binding gate reads the
// script. The codes are server output: the write removes and stamps them
// again from the saved script (savedImportEvaluationRefreshed), so the copy
// the editor read never reaches storage. Disclosure scrubs a code as free
// text, so a long variable name in one reads as masked, and the gate would
// otherwise demand a secret binding for it. Every other review code, which
// the write does not rebuild, is still judged as submitted.
func removeSubmittedDependencyCodes(script, prior *dsl.Script) error {
	if !savedImportEvaluationRefreshed(script, prior) {
		return nil
	}
	return importreview.RemoveDependencyCodes(script)
}

// scriptHasDependencyCodes reports whether an import stamped a dependency
// review code (missing_producer or conditional_producer) on any step.
func scriptHasDependencyCodes(script *dsl.Script) bool {
	findings, err := importreview.FindingsFromScript(script)
	if err != nil {
		return false
	}
	for _, finding := range findings {
		if strings.HasPrefix(finding.Code, "missing_producer:") || strings.HasPrefix(finding.Code, "conditional_producer:") {
			return true
		}
	}
	return false
}
