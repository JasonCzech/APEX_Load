package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type httpPreparationBudget struct {
	bytesRemaining, workRemaining, templatesRemaining, nodesRemaining int
	err                                                               error
	references                                                        []string
}

func (b *httpPreparationBudget) charge(n int) error {
	if n < 0 || n > b.bytesRemaining || n > b.workRemaining {
		b.err = fmt.Errorf("concurrent HTTP preparation byte/work limit exceeded")
		return b.err
	}
	b.bytesRemaining -= n
	b.workRemaining -= n
	return nil
}

func (vu *VUser) interpolationKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	if vu.concurrentPreparation != nil {
		sort.Strings(keys)
	}
	return keys
}

type preparedHTTPChild struct {
	authored                               dsl.Action
	action                                 *protocol.Action
	redaction                              *RedactionConfig
	platformCredentials, scriptCredentials []string
	structuredBody                         bool
	publicEvidenceUnavailable              bool
	publicationRedactionFailed             bool
	retrySensitiveValues                   []string
	result                                 *protocol.Result
	debugEvidence                          *ReplayActionEvent
	debugError                             error
	err                                    error
	started, completed                     time.Time
}

// Fixed source-ordinal allowances include every attempt, including values from
// discarded failures. Charge entry overhead as well as detached value bytes so
// tiny values cannot turn the group allowance into an unbounded map/slice.
const maxConcurrentRetryClassificationBytes = 16 << 20

type httpRetryClassificationHistory struct {
	base      *RedactionConfig
	values    map[string]struct{}
	used      int
	allowance int
}

func (h *httpRetryClassificationHistory) observedValues() []string {
	values := make([]string, 0, len(h.values))
	for value := range h.values {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func (h *httpRetryClassificationHistory) observe(vu *VUser, result *protocol.Result) error {
	fail := func() error {
		vu.failRuntimeCredentialRedaction(fmt.Errorf("concurrent HTTP retry classification unavailable"))
		return vu.runtimeCredentialRedactionErr
	}
	if result.ExtractionsIncomplete || protocol.ValidateExtractions(result.Extractions) != nil {
		return fail()
	}
	values := concurrentSensitiveExtractionValues(result.Extractions)
	pending := make(map[string]struct{}, len(values))
	cost := 0
	for _, value := range values {
		if _, known := h.values[value]; known {
			continue
		}
		if _, known := pending[value]; known {
			continue
		}
		if len(value)+64 > h.allowance-h.used-cost {
			return fail()
		}
		pending[value] = struct{}{}
		cost += len(value) + 64
	}
	if len(pending) == 0 {
		return nil
	}
	if h.values == nil {
		h.values = make(map[string]struct{}, len(pending))
	}
	for value := range pending {
		h.values[strings.Clone(value)] = struct{}{}
	}
	h.used += cost
	matcher, err := WithSecretValueRedactionChecked(nil, h.observedValues())
	if err != nil {
		return fail()
	}
	config, err := attachConcurrentRuntimeMatcher(h.base, matcher)
	if err != nil {
		return fail()
	}
	// Only this detached executor changes. The shared protocol's callback stays
	// bound to the parent, whose classification advances after all workers join.
	vu.RedactionConfig = config
	return nil
}

func (vu *VUser) admitConcurrentRetryClassifications(children []preparedHTTPChild) ([]string, error) {
	unique := make(map[string]struct{})
	for _, child := range children {
		for _, value := range child.retrySensitiveValues {
			unique[value] = struct{}{}
		}
	}
	if len(unique) == 0 {
		return nil, nil
	}
	observed := make([]string, 0, len(unique))
	for value := range unique {
		observed = append(observed, value)
	}
	sort.Strings(observed)
	vu.ensureRuntimeCredentialRedactionState()
	prior := vu.runtimeCredentialScopes["extraction:observed"]
	var updated []string
	for _, value := range observed {
		index := sort.SearchStrings(prior, value)
		if index < len(prior) && prior[index] == value {
			continue
		}
		if updated == nil {
			updated = append([]string{}, prior...)
		}
		updated = append(updated, value)
	}
	if updated != nil {
		if err := vu.replaceRuntimeCredentialScope("extraction:observed", updated); err != nil {
			return nil, err
		}
	}
	return observed, nil
}

type concurrentHTTPGroup struct {
	children            []preparedHTTPChild
	started, completed  time.Time
	metadata            map[string]any
	hierarchy, pageName string
	extractions         map[string]string
}

func (vu *VUser) prepareConcurrentHTTP(action dsl.Action) ([]preparedHTTPChild, int, error) {
	mode, concurrency, err := dsl.HTTPSequenceExecutionSettings(action)
	if err != nil || mode != dsl.HTTPSequenceConcurrent {
		if err == nil {
			err = fmt.Errorf("concurrent HTTP execution mode required")
		}
		return nil, 0, err
	}
	provider, ok := vu.Proto.(protocol.SharedSessionConcurrencyProvider)
	if !ok || !provider.SharedSessionConcurrentSafe() {
		return nil, 0, fmt.Errorf("protocol does not support shared-session concurrent HTTP requests")
	}
	budget := &httpPreparationBudget{bytesRemaining: dsl.MaxHTTPConcurrentPreparationBytes, workRemaining: dsl.MaxHTTPConcurrentPreparationWork, templatesRemaining: 4096, nodesRemaining: 32768}
	values := []any{action}
	if vu.Script != nil {
		values = append(values, vu.Script.Config.HTTP.DefaultHeaders, vu.Script.Config.HTTP.DefaultHeaderList)
	}
	sourceBytes, sourceWork, err := dsl.HTTPConcurrentPreparationSourceSizeWithWorkLimit(budget.workRemaining, values...)
	budget.workRemaining -= sourceWork
	if err != nil {
		return nil, 0, err
	}
	if sourceBytes > budget.bytesRemaining {
		return nil, 0, fmt.Errorf("concurrent HTTP preparation byte limit exceeded")
	}
	budget.bytesRemaining -= sourceBytes
	if budget.workRemaining <= 0 {
		return nil, 0, fmt.Errorf("concurrent HTTP preparation work limit exceeded")
	}
	if spec, ok := dsl.SpecForActionType("http_sequence"); ok && spec.Validate != nil && len(spec.Validate(0, action)) != 0 {
		return nil, 0, fmt.Errorf("concurrent HTTP request group failed validation")
	}
	children, err := httpSequenceChildActions(action)
	if err != nil {
		return nil, 0, err
	}
	work, admissionErr := dsl.ValidateHTTPConcurrentChildrenWithWorkLimit(children, budget.workRemaining)
	budget.workRemaining -= work
	if admissionErr != nil {
		return nil, 0, admissionErr
	}
	seen := make(map[string]bool)
	outputs := make(map[string]string)
	prepared := make([]preparedHTTPChild, 0, len(children))
	vu.concurrentPreparation = budget
	defer func() { vu.concurrentPreparation = nil }()
	for index, child := range children {
		if err := dsl.ValidateHTTPConcurrentChild(child); err != nil {
			return nil, 0, fmt.Errorf("concurrent HTTP child %d: %v", index+1, err)
		}
		if child.ThinkTimeAfter && vu.Script != nil {
			cfg := resolveActionThinkTime(protocol.ThinkTimeConfig{Type: vu.Script.Config.ThinkTime.Type, Ignore: vu.Script.Config.ThinkTime.Ignore, MeanMs: vu.Script.Config.ThinkTime.MeanMs, StdMs: vu.Script.Config.ThinkTime.StdMs, MinMs: vu.Script.Config.ThinkTime.MinMs, MaxMs: vu.Script.Config.ThinkTime.MaxMs, MinPercent: vu.Script.Config.ThinkTime.MinPercent, MaxPercent: vu.Script.Config.ThinkTime.MaxPercent}, child.ThinkTime)
			if !cfg.Ignore && cfg.Type != "none" && (cfg.MeanMs != 0 || cfg.StdMs != 0 || cfg.MinMs != 0 || cfg.MaxMs != 0 || cfg.MinPercent != 0 || cfg.MaxPercent != 0) {
				return nil, 0, fmt.Errorf("concurrent HTTP child think time must be zero; use the group think time")
			}
		}
		resolved := vu.resolveAction(child)
		if budget.err != nil {
			return nil, 0, budget.err
		}
		if err := vu.checkResolvedSecrets(); err != nil {
			return nil, 0, err
		}
		if vu.bodyResolutionError != nil {
			return nil, 0, fmt.Errorf("concurrent HTTP structured body resolution failed")
		}
		if len(vu.unresolvedParameterRefs) != 0 || actionContainsUnresolvedParameterExceptBody(resolved, vu.structuredBodyResolvedAction == resolved) {
			return nil, 0, fmt.Errorf("concurrent HTTP parameter resolution failed")
		}
		if err := vu.runtimeCredentialRedactionError(); err != nil {
			return nil, 0, err
		}
		if err := vu.extendRuntimeRedactionForResolvedAction(resolved); err != nil {
			return nil, 0, err
		}
		if err := provider.ValidateSharedSessionAction(resolved); err != nil {
			return nil, 0, fmt.Errorf("concurrent HTTP child %d: request validation failed", index+1)
		}
		for _, extractor := range resolved.Extractors {
			name := strings.TrimSpace(extractor.Name)
			if name == "" || seen[name] {
				return nil, 0, fmt.Errorf("concurrent HTTP requests require unique nonempty extractor names")
			}
			seen[name] = true
			outputs[name] = extractor.Mode
		}
		prepared = append(prepared, preparedHTTPChild{authored: child, action: resolved, structuredBody: vu.structuredBodyResolvedAction == resolved, redaction: vu.RedactionConfig, platformCredentials: vu.runtimeCredentialScopes[runtimeCredentialActionScope], scriptCredentials: vu.runtimeCredentialScopes[runtimeCredentialScriptActionScope]})
	}
	for _, reference := range budget.references {
		if _, found := dsl.HTTPConcurrentExtractorOutputSource(reference, outputs); found {
			return nil, 0, fmt.Errorf("concurrent HTTP requests cannot depend on group extractor outputs")
		}
	}
	for name := range outputs {
		index := strings.LastIndexByte(name, '_')
		if index >= 0 {
			base := name[:index]
			if mode, ok := outputs[base]; ok && mode == "all" && dsl.HTTPConcurrentExtractorReference(name, base, mode) {
				return nil, 0, fmt.Errorf("concurrent HTTP extractor output names overlap")
			}
		}
	}

	// Resolve wrapper dimensions before traffic, in the same bounded source pass.
	vu.concurrentGroupMetadata = vu.interpolateAnyMap(action.Metadata)
	vu.concurrentGroupHierarchy = resolvedAttributionHierarchy(action.Attribution, vu)
	vu.concurrentGroupPage = resolvedAttributionPage(action.Attribution, vu)
	if budget.err != nil {
		return nil, 0, budget.err
	}
	return prepared, min(concurrency, len(prepared)), nil
}

// Only the protocol instance and immutable configuration cross the worker
// boundary. Variables, parameters, randomness, redaction scopes and publication
// remain owned by the parent goroutine.
func (vu *VUser) runConcurrentHTTP(ctx context.Context, action dsl.Action, errCfg dsl.ErrorHandlingConfig) (group concurrentHTTPGroup, err error) {
	group.started = time.Now()
	children, concurrency, err := vu.prepareConcurrentHTTP(action)
	if err != nil {
		group.completed = time.Now()
		return group, err
	}
	group.children = children
	group.metadata = vu.concurrentGroupMetadata
	group.hierarchy = vu.concurrentGroupHierarchy
	group.pageName = vu.concurrentGroupPage
	if concurrency < 1 {
		group.completed = time.Now()
		return group, fmt.Errorf("concurrent HTTP group requires at least one child")
	}
	// Children run on detached executors without transaction state, so the
	// group resolves its correlation context once and every child shares it.
	ctx, endTransactionRoot := vu.correlationContext(ctx, &protocol.Action{Name: action.Name})
	defer endTransactionRoot()
	var wg sync.WaitGroup
	jobs := make(chan int)
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				child := &group.children[index]
				child.started = time.Now()
				if ctx.Err() != nil {
					child.err = ctx.Err()
				} else {
					executor := &VUser{Proto: vu.Proto, Script: vu.Script, RedactionConfig: child.redaction, structuredBodyResolvedAction: nil, unredactedDebug: vu.unredactedDebug, debugRetryGroupCount: len(children)}
					if child.structuredBody {
						executor.structuredBodyResolvedAction = child.action
					}
					history := &httpRetryClassificationHistory{base: child.redaction, allowance: maxConcurrentRetryClassificationBytes / len(children)}
					child.result, child.err = executor.executeRetryInternal(ctx, child.action, errCfg, nil, history)
					if vu.unredactedDebug {
						debugResult := child.result
						if debugResult != nil && len(debugResult.ProtocolEvidence) == 0 {
							copyResult := *debugResult
							copyResult.ProtocolEvidence = buildUnredactedReplayProtocolEvidence(child.action, debugResult)
							copyResult.ProtocolEvidenceOmittedCount = debugResult.ProtocolEvidenceOmittedCount
							debugResult = &copyResult
						}
						child.debugEvidence = executor.debugRetryEvidence
						if child.debugEvidence == nil {
							child.debugEvidence = captureUnredactedConcurrentResult(debugResult, len(children))
						}
						if child.err != nil {
							child.debugError = executor.debugRetryError
							if child.debugError == nil {
								child.debugError = fmt.Errorf("%s", cloneBoundedResultString(child.err.Error(), min(maxResultErrorBytes, maxConcurrentRichEvidenceBytes/max(1, len(children))/16)))
							}
						}
					}
					child.retrySensitiveValues = history.observedValues()
					if executor.runtimeCredentialRedactionErr != nil {
						child.publicationRedactionFailed = true
					}
					config, projectionErr := boundConcurrentCompletedResult(child.result, len(children), child.redaction, child.retrySensitiveValues)
					if projectionErr != nil {
						child.publicationRedactionFailed = true
						child.err = projectionErr
					}
					if child.err != nil {
						message := scrubSensitiveTextWithConfig(child.err.Error(), config)
						if len(message) > maxResultErrorBytes {
							message = "concurrent HTTP child error evidence omitted by retention limit"
						}
						child.err = fmt.Errorf("%s", strings.Clone(message))
					}
				}
				child.completed = time.Now()
			}
		}()
	}
	for index := range children {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	group.completed = time.Now()
	for _, child := range group.children {
		if child.publicationRedactionFailed {
			// The failed worker no longer carries a complete fresh classification.
			// Establish the parent failure before the first ordered publication.
			vu.failRuntimeCredentialRedaction(fmt.Errorf("runtime credential redaction capacity exceeded"))
			omitConcurrentJoinedPublicEvidence(group.children)
			return group, fmt.Errorf("concurrent HTTP publication redaction failed")
		}
	}
	observed, classificationErr := vu.admitConcurrentRetryClassifications(group.children)
	if classificationErr != nil {
		omitConcurrentJoinedPublicEvidence(group.children)
		return group, fmt.Errorf("concurrent HTTP publication redaction failed")
	}
	if ctx.Err() != nil {
		omitConcurrentJoinedPublicEvidence(group.children)
		return group, ctx.Err()
	}
	// Both load and replay admit the combined exact outputs after the join.
	// A failed group retains its individual outcome evidence, but cannot claim
	// a successful aggregate whose extraction output exceeds protocol bounds.
	outputs := &protocol.Result{}
	for _, child := range group.children {
		if child.err != nil || child.result == nil {
			continue
		}
		for key, value := range child.result.Extractions {
			if _, exists := outputs.Extractions[key]; exists {
				omitConcurrentJoinedPublicEvidence(group.children)
				return group, fmt.Errorf("concurrent HTTP extraction outputs overlap")
			}
			if err := protocol.StoreExtraction(outputs, key, value); err != nil {
				omitConcurrentJoinedPublicEvidence(group.children)
				return group, fmt.Errorf("concurrent HTTP extraction retention failed")
			}
		}
	}
	if err := scrubConcurrentJoinedEvidence(group.children, outputs.Extractions, observed); err != nil {
		return group, err
	}
	group.extractions = outputs.Extractions
	return group, nil
}
func (vu *VUser) useConcurrentChildRedaction(child *preparedHTTPChild) error {
	vu.concurrentPublicationRedaction = child.redaction
	vu.concurrentPublicationUnavailable = child.publicEvidenceUnavailable
	return vu.replaceRuntimeCredentialScopes(map[string][]string{
		runtimeCredentialActionScope:       child.platformCredentials,
		runtimeCredentialScriptActionScope: child.scriptCredentials,
	})
}
func (vu *VUser) executeConcurrentHTTPSequence(ctx context.Context, action dsl.Action, errCfg dsl.ErrorHandlingConfig, claimTransaction func() bool) (*protocol.Result, error) {
	previousConfig, previousUnavailable := vu.concurrentPublicationRedaction, vu.concurrentPublicationUnavailable
	defer func() {
		vu.concurrentPublicationRedaction, vu.concurrentPublicationUnavailable = previousConfig, previousUnavailable
	}()
	transactions := dsl.HTTPSequenceRequestTransactions(action)
	goalReached := false
	// recordRequestTransaction records one child's own transaction, claiming
	// its goal slot first; LR_AUTO semantics make the request's outcome the
	// transaction's status.
	recordRequestTransaction := func(name string, started time.Time, duration time.Duration, success bool, source *protocol.Result) {
		if goalReached {
			return
		}
		if claimTransaction != nil && !claimTransaction() {
			goalReached = true
			return
		}
		if source == nil {
			source = &protocol.Result{ActionName: name, StartTime: started}
		}
		vu.recordTransaction(name, duration, success, source)
	}
	group, err := vu.runConcurrentHTTP(ctx, action, errCfg)
	aggregate := &protocol.Result{ActionName: action.Name, StartTime: group.started, Duration: group.completed.Sub(group.started), Success: err == nil, BusinessTransaction: dsl.HTTPSequenceRecordsTransaction(action), Group: vu.Group, Extractions: cloneStringMap(group.extractions)}
	if err != nil {
		aggregate.ErrorMessage = err.Error()
	}
	applyProtocolResultSourceMetadata(aggregate, group.metadata)
	aggregate.Hierarchy = group.hierarchy
	aggregate.PageName = group.pageName
	for index := range group.children {
		child := &group.children[index]
		if err := vu.useConcurrentChildRedaction(child); err != nil {
			aggregate.Success = false
			aggregate.ErrorMessage = "concurrent HTTP publication redaction failed"
			clearUnredactableResultEvidence(child.result)
		}
		if child.err != nil || child.result == nil {
			aggregate.Success = false
			message := "concurrent HTTP child returned no result"
			if child.err != nil {
				message = scrubSensitiveTextWithConfig(child.err.Error(), child.redaction)
			}
			if aggregate.ErrorMessage == "" || child.err == nil {
				aggregate.ErrorMessage = message
			}
			if index < len(transactions) {
				recordRequestTransaction(transactions[index], child.started, child.completed.Sub(child.started), false, &protocol.Result{ActionName: transactions[index], StartTime: child.started, ErrorMessage: message})
			}
			continue
		}
		vu.logDiagnosticResult("concurrent sequence action", child.authored.Name, child.action, child.result)
		vu.recordResult(child.result, errCfg, vu.Script.Config.Log)
		copySequenceSourceResult(aggregate, child.result)
		if !child.result.Success {
			aggregate.Success = false
			if aggregate.ErrorMessage == "" {
				aggregate.ErrorMessage = firstEngineValue(child.result.ErrorMessage, fmt.Sprintf("concurrent HTTP child %d failed", index+1))
			}
		}
		if index < len(transactions) {
			recordRequestTransaction(transactions[index], child.started, child.completed.Sub(child.started), child.result.Success, child.result)
		}
	}
	if len(group.children) == 0 {
		// The group failed before any request ran: each request's transaction
		// fails, as each would in the source's sequential order.
		for _, name := range transactions {
			recordRequestTransaction(name, group.started, 0, false, &protocol.Result{ActionName: name, StartTime: group.started, ErrorMessage: aggregate.ErrorMessage})
		}
	}
	if err != nil {
		aggregate.ErrorMessage = err.Error()
	}
	if group.hierarchy != "" {
		aggregate.Hierarchy = group.hierarchy
	}
	if group.pageName != "" {
		aggregate.PageName = group.pageName
	}
	if ctx.Err() != nil {
		return aggregate, ctx.Err()
	}
	if goalReached {
		return aggregate, errTransactionGoalReached
	}
	return aggregate, nil
}
