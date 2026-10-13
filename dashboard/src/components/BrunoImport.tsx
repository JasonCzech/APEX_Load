import ImportIssueSeverityTag from './ImportIssueSeverityTag';
import { importSourceName } from '../appTopbar';
import { importReviewExpiresText } from './protectedImportSession';
import ImportWorkflowGuide from './ImportWorkflowGuide';
import ImportSaveConfirmation from './ImportSaveConfirmation';
import TabList from './ux/TabList';
import { handleTabKey } from './tabKeyboard';
import DialogLayer from './ux/DialogLayer';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useUnsavedWorkGuard } from '../hooks/NavigationGuardProvider';
import { createPortal } from 'react-dom';
import { type Folder, type StoredScript } from '../types';
import { type ScriptDefinition } from '../types/scripts';
import { type BrunoCollectionItem, type BrunoInspectResponse, type BrunoNativeDiagnostic, type BrunoNativeScriptCandidate, type BrunoSecretRequirement, type BrunoSupportMatrixResponse, type CommitBrunoImportResponse, type CommitBrunoNativeBatchResponse, type SecretBindingRequirement, type SecretBindingSelection, } from '../types/scriptImports';
import { useApi } from '../hooks/useApi';
import { ApiError } from '../hooks/apiClient';
import { useModalDialog } from '../hooks/useModalDialog';
import { canCreateScriptFolders } from '../utils/authAccessUtils';
import { getErrorMessage } from '../utils/errorUtils';
import SourceCodeViewer from './SourceCodeViewer';
import SecretBindingReview, { type PreparedSecretBindings, type RequirementDestinationState, type SecretBindingReviewHandle, } from './SecretBindingReview';
import BrunoDataReview from './BrunoDataReview';
import { brunoMissingVariables, brunoCandidateNeedsSecrets } from './brunoReviewState';
import { formatBytes } from './settingsPageFormatters';
import { type ImportedSaveResult } from './useScriptsAuthoringState';
import { formatSourceSecretSessionFailure, getSourceSecretSessionFailureMetadata, } from './sourceSecretSession';
import { brunoDestinationFolderOptions } from './brunoFolderOptions';
import { nativeReviewGuidance } from './brunoNativeReviewGuidance';
import { brunoSecretPresentation } from './brunoSecretPresentation';
import { aggregateBrunoSecretRequirements } from './brunoSecretRequirements';
import { importFileLogicalPath } from '../importLogicalPath';
import './BrunoImport.css';
interface Props {
    initialFiles?: File[] | null;
    initialOutputMode?: OutputMode | null;
    projectId?: string;
    folderId?: string | null;
    onFolderChange?: (folderId: string) => void;
    /** The authoring shell renders and owns the only visible destination control. */
    destinationFolderManagedExternally?: boolean;
    onImported?: (result: ImportedSaveResult) => void;
    onConfigureSecrets?: (projectId: string) => void;
    onClose?: () => void;
    onOperationBusyChange?: (busy: boolean) => void;
}
type OutputMode = 'native_requests';
type BrunoInspectionTab = 'summary' | 'source_apis' | 'credentials' | 'data' | 'import' | 'runtime' | 'dsl' | 'source';
type NativeCandidateSortKey = 'sequence' | 'source' | 'script' | 'request' | 'status' | 'details';
type FolderCreationAccess = 'checking' | 'allowed' | 'denied' | 'unavailable';
const FOLDER_CREATION_DENIED_MESSAGE = 'Your current role can import scripts but cannot create project folders. Select an existing script folder, or ask a Project Administrator to create one for this project.';
const NATIVE_CANDIDATE_PAGE_SIZE = 100;
const NATIVE_FINDING_PAGE_SIZE = 100;
const COLLECTION_TREE_PAGE_SIZE = 100;
const MAX_DETAILED_REVIEW_FINDING_BLOCKERS = 10;
const BRUNO_INSPECTION_TABS: ReadonlyArray<{
    id: BrunoInspectionTab;
    label: string;
}> = [
    { id: 'summary', label: 'Review' },
    { id: 'credentials', label: 'Credentials' },
    { id: 'data', label: 'Data & parameters' },
    { id: 'runtime', label: 'Runtime settings' },
    { id: 'dsl', label: 'DSL Preview' },
    { id: 'source', label: 'Source Mapping' },
];
function suggestedBrunoFolderName(files: File[]) {
    if (files.length === 0)
        return 'Imported Bruno APIs';
    const sourcePath = importFileLogicalPath(files[0]);
    const firstSegment = sourcePath.split('/').filter(Boolean)[0] || files[0].name;
    const withoutSuffix = firstSegment
        .replace(/\.bruno\.(?:tar\.gz|tar|zip)$/i, '')
        .replace(/\.(?:tar\.gz|tgz|tar|zip|bru|ya?ml|json)$/i, '');
    const readable = withoutSuffix.replace(/[_-]+/g, ' ').replace(/\s+/g, ' ').trim();
    return readable || 'Imported Bruno APIs';
}
function storedScriptFromDefinition(script: ScriptDefinition): StoredScript {
    return {
        id: script.id || '',
        ref_key: script.ref_key,
        name: script.name,
        protocol: script.protocol,
        version: script.version,
        project_id: script.project_id,
        folder_id: script.folder_id,
        created_at: script.created_at || new Date().toISOString(),
        script,
    };
}
function nativeCandidateIdentityError(candidates: BrunoNativeScriptCandidate[]) {
    const candidateIds = new Set<string>();
    const plannedScriptIds = new Set<string>();
    for (const candidate of candidates) {
        const candidateId = candidate.candidate_id?.trim();
        if (!candidateId)
            return `Native Bruno inspection candidate ${candidate.source_path || '(unknown source)'} is missing candidate_id.`;
        if (candidateIds.has(candidateId))
            return `Native Bruno inspection contains duplicate candidate_id ${candidateId}.`;
        candidateIds.add(candidateId);
        const plannedScriptId = candidate.planned_script_id?.trim();
        if (!plannedScriptId)
            return `Native Bruno inspection candidate ${candidate.source_path || candidateId} is missing planned_script_id.`;
        if (plannedScriptIds.has(plannedScriptId))
            return `Native Bruno inspection contains duplicate planned_script_id ${plannedScriptId}.`;
        plannedScriptIds.add(plannedScriptId);
    }
    return '';
}
function validatedNativeCommitScripts(response: CommitBrunoImportResponse, selectedCandidates: BrunoNativeScriptCandidate[]) {
    const identityError = nativeCandidateIdentityError(selectedCandidates);
    if (identityError)
        throw new Error(identityError);
    if (!isNativeCommitResponse(response) || !response.batch_id.trim()) {
        throw new Error('Native Bruno commit response is missing batch_id.');
    }
    const receipt = response as unknown as Record<string, unknown>;
    if (!Object.prototype.hasOwnProperty.call(receipt, 'created') ||
        !Object.prototype.hasOwnProperty.call(receipt, 'idempotent_replay') ||
        typeof receipt.created !== 'boolean' ||
        typeof receipt.idempotent_replay !== 'boolean') {
        throw new Error('Native Bruno commit response must include boolean created and idempotent_replay fields.');
    }
    if (receipt.created === receipt.idempotent_replay) {
        throw new Error('Native Bruno commit response must identify exactly one of creation or idempotent replay.');
    }
    if (response._http_status === 201 && !receipt.created) {
        throw new Error('Native Bruno HTTP 201 response must identify a newly created batch.');
    }
    if (response._http_status === 200 && !receipt.idempotent_replay) {
        throw new Error('Native Bruno HTTP 200 response must identify an idempotent replay.');
    }
    if (response._http_status !== 200 && response._http_status !== 201) {
        throw new Error('Native Bruno commit response has an unsupported HTTP status.');
    }
    if (response.scripts.length !== selectedCandidates.length) {
        throw new Error(`Native Bruno commit returned ${response.scripts.length} of ${selectedCandidates.length} selected scripts.`);
    }
    if (response.count !== response.scripts.length) {
        throw new Error('Native Bruno commit response count does not match its scripts.');
    }
    const expectedIds = new Set(selectedCandidates.map(candidate => (candidate.planned_script_id || '').trim()));
    const receivedIds = new Set<string>();
    for (const script of response.scripts) {
        const id = script.id?.trim();
        if (!id || receivedIds.has(id) || !expectedIds.has(id)) {
            throw new Error('Native Bruno commit response contains an unknown, duplicate, or missing script identity.');
        }
        receivedIds.add(id);
    }
    return response.scripts.map(storedScriptFromDefinition);
}
function isNativeCommitResponse(response: CommitBrunoImportResponse): response is CommitBrunoNativeBatchResponse {
    return 'batch_id' in response;
}
function nativeFindingID(diagnostic: BrunoNativeDiagnostic) {
    return diagnostic.id || diagnostic.finding_id || '';
}
function nativeCandidateReviewFindings(candidate: BrunoNativeScriptCandidate) {
    const ids = new Set<string>();
    (candidate.diagnostics || []).forEach(diagnostic => {
        if (!diagnostic.acknowledgement_required)
            return;
        const id = nativeFindingID(diagnostic);
        if (id)
            ids.add(id);
    });
    return Array.from(ids).sort();
}
function nativeCandidateMethod(candidate: BrunoNativeScriptCandidate) {
    return candidate.method || candidate.script.actions[0]?.method || candidate.script.actions[0]?.type || 'HTTP';
}
function nativeCandidateTarget(candidate: BrunoNativeScriptCandidate) {
    return candidate.target || candidate.script.actions[0]?.target || candidate.script.config?.base_url || '';
}
function nativeCandidateSortValue(candidate: BrunoNativeScriptCandidate, key: NativeCandidateSortKey) {
    switch (key) {
        case 'sequence':
            return String(candidate.sequence ?? Number.MAX_SAFE_INTEGER);
        case 'source':
            return `${candidate.source_path || ''}\u0000${candidate.sequence ?? Number.MAX_SAFE_INTEGER}`;
        case 'script':
            return candidate.script.name || '';
        case 'request':
            return `${nativeCandidateMethod(candidate)}\u0000${nativeCandidateTarget(candidate)}`;
        case 'status':
            return nativeCandidateReviewFindings(candidate).length > 0
                ? 'review'
                : (candidate.missing_variables || []).length > 0
                    ? 'blocked'
                    : (candidate.secret_requirements || []).length > 0
                        ? 'configure'
                        : (candidate.diagnostics || []).length > 0 ? 'warning' : 'ready';
        case 'details':
            return `${nativeCandidateReviewFindings(candidate).length}\u0000${(candidate.diagnostics || []).length}`;
    }
}
function nativeSecretRequirementNoImportedValueMessage(requirement: BrunoSecretRequirement, destinationSecretName: string): string {
    if (requirement.unresolved_source_variable !== true
        || requirement.source_value_available === true
        || requirement.destination_value_preserved === true
        || requirement.preservation_unavailable_reason !== 'source_value_unavailable')
        return '';
    const sourceReference = requirement.source_variable
        ? ` for {{${requirement.source_variable}}}`
        : '';
    const destinationReference = destinationSecretName
        ? `{{secret.${destinationSecretName}}}`
        : 'a project-secret reference whose name is still required';
    return `No value${sourceReference} was included in the collection, so there is no imported value to preserve. The generated script uses ${destinationReference}; execution requires a configured value for that project secret.`;
}
function BrunoNativeReviewDialog({ candidate, candidateSelected, requirementDestinationState, onSource, onRepair, onClose, }: {
    candidate: BrunoNativeScriptCandidate;
    candidateSelected: boolean;
    requirementDestinationState: Readonly<Record<string, RequirementDestinationState>>;
    onSource: () => void;
    onRepair: (tab: BrunoInspectionTab) => void;
    onClose: () => void;
}) {
    const dialogRef = useRef<HTMLElement | null>(null);
    const closeButtonRef = useRef<HTMLButtonElement | null>(null);
    useModalDialog({ dialogRef, initialFocusRef: closeButtonRef, onClose });
    return createPortal(<DialogLayer><div className="modal-overlay bruno-native-review-overlay" role="presentation" onClick={event => {
            if (event.target === event.currentTarget)
                onClose();
        }}>
      <section ref={dialogRef} className="modal-content bruno-native-review-dialog" role="dialog" aria-modal="true" aria-labelledby="bruno-native-review-dialog-title" aria-describedby="bruno-native-review-dialog-source">
        <header className="bruno-native-review-dialog-header">
          <div>
            <h3 id="bruno-native-review-dialog-title">
              {candidateSelected ? 'Review imported script' : 'Preview detected script'}
            </h3>
            <p id="bruno-native-review-dialog-source">{candidate.source_path}</p>
          </div>
          <button ref={closeButtonRef} type="button" className="btn btn-ghost bruno-native-review-dialog-close" aria-label={`Close review for ${candidate.source_path}`} onClick={onClose}>
            ×
          </button>
        </header>
        <div className="bruno-native-review-dialog-body">
          {!candidateSelected && (<div className="bruno-native-review-selection-note" role="status">
              <strong>Preview only — not selected for import.</strong>
              <span>Select this request in the scripts table before relying on credential destination choices.</span>
            </div>)}
          <div className="bruno-native-review-summary" aria-label="Imported request summary">
            <span><small>Method</small><code>{nativeCandidateMethod(candidate)}</code></span>
            <span><small>Target</small><code>{nativeCandidateTarget(candidate)}</code></span>
            {candidate.sequence !== undefined && <span><small>Sequence</small><strong>{candidate.sequence}</strong></span>}
          </div>
          {(candidate.missing_variables || []).length > 0 && (<section className="bruno-native-review-section">
              <h4>Missing variables</h4><button className="btn btn-secondary" type="button" onClick={() => onRepair('data')}>Configure runtime inputs</button>
              <p>{candidate.missing_variables?.join(', ')}</p>
            </section>)}
          {(candidate.secret_requirements || []).length > 0 && (<section className="bruno-native-review-section">
              <h4>Secrets</h4><button className="btn btn-secondary" type="button" onClick={() => onRepair('credentials')}>Configure credentials</button>
              <ul>
                {candidate.secret_requirements?.map(requirement => {
                const presentation = brunoSecretPresentation(requirement);
                const liveDestination = candidateSelected
                    ? requirementDestinationState[requirement.id]
                    : undefined;
                const usesProjectSecret = liveDestination?.usesProjectSecret
                    ?? (requirement.destination_value_preserved !== true);
                const destinationSecretName = liveDestination
                    ? liveDestination.secretName
                    : requirement.suggested_name;
                const noImportedValueMessage = presentation.preservationUnavailableDescription
                    ? ''
                    : nativeSecretRequirementNoImportedValueMessage(requirement, destinationSecretName);
                const currentDestinationGuidance = liveDestination?.guidance || '';
                return (<li key={requirement.id}>
                      <strong>{presentation.label}</strong>
                      {presentation.origin ? (<>
                          <span>Import context: {presentation.context}</span>
                          <span>Runtime purpose: {presentation.purpose}</span>
                        </>) : requirement.context ? <span>{requirement.context}</span> : null}
                      {requirement.source_variable && (<span>Source variable: <code>{`{{${requirement.source_variable}}}`}</code></span>)}
                      {usesProjectSecret ? (<span>
                          {candidateSelected ? 'Project secret reference' : 'Detected project-secret suggestion'}:{' '}
                          <code>{destinationSecretName ? `{{secret.${destinationSecretName}}}` : '{{secret.NAME}}'}</code>
                        </span>) : (<span>
                          Optional project-secret suggestion: <code>{`{{secret.${requirement.suggested_name}}}`}</code>
                        </span>)}
                      {noImportedValueMessage && (<span>{noImportedValueMessage}</span>)}
                      {currentDestinationGuidance && (<span>Current destination: {currentDestinationGuidance}</span>)}
                    </li>);
            })}
              </ul>
            </section>)}
          {(candidate.diagnostics || []).length > 0 && (<section className="bruno-native-review-section bruno-native-diagnostics">
              <h4>Diagnostics</h4><button className="btn btn-secondary" type="button" onClick={() => onRepair('import')}>Review conversion requirements</button>
              <p>Original inspection findings are retained below. The readiness summary and missing-variable list reflect your pending input changes.</p>
              {candidate.diagnostics?.map((diagnostic, index) => (<p key={nativeFindingID(diagnostic) || `${diagnostic.code}-${index}`}>
                  <code>{diagnostic.code || 'finding'}</code> {diagnostic.message}
                </p>))}
            </section>)}
          {(candidate.source_mappings || []).length > 0 && (<section className="bruno-native-review-section">
              <h4>Source mappings</h4><button className="btn btn-secondary" type="button" onClick={onSource}>Inspect sanitized source</button>
              <SourceCodeViewer ariaLabel="Source mappings JSON" className="bruno-native-review-code-viewer" sourcePath="source-mappings.json" value={JSON.stringify(candidate.source_mappings, null, 2)}/>
            </section>)}
          <section className="bruno-native-review-section">
            <h4>Masked APEX Load DSL script</h4>
            <BrunoImportPreviewScript script={candidate.script}/>
          </section>
        </div>
        <footer className="bruno-native-review-dialog-footer">
          <button type="button" className="btn btn-secondary" onClick={onClose}>Close</button>
        </footer>
      </section>
    </div></DialogLayer>, document.body);
}
function BrunoNativeSourceDialog({ candidate, importSessionId, projectId, loadSource, onClose, }: {
    candidate: BrunoNativeScriptCandidate;
    importSessionId: string;
    projectId: string;
    loadSource: (importSessionId: string, projectId: string, sourcePath: string) => Promise<string>;
    onClose: () => void;
}) {
    const dialogRef = useRef<HTMLElement | null>(null);
    const closeButtonRef = useRef<HTMLButtonElement | null>(null);
    const [source, setSource] = useState<string | null>(null);
    const [error, setError] = useState('');
    useModalDialog({ dialogRef, initialFocusRef: closeButtonRef, onClose });
    useEffect(() => {
        let cancelled = false;
        setSource(null);
        setError('');
        void loadSource(importSessionId, projectId, candidate.source_path).then(content => {
            if (!cancelled)
                setSource(content);
        }).catch((sourceError: unknown) => {
            if (!cancelled) {
                setError(sourceError instanceof Error ? sourceError.message : 'Unable to load the sanitized source API.');
            }
        });
        return () => { cancelled = true; };
    }, [candidate.candidate_id, candidate.source_path, importSessionId, loadSource, projectId]);
    return createPortal(<DialogLayer><div className="modal-overlay bruno-native-review-overlay" role="presentation" onClick={event => {
            if (event.target === event.currentTarget)
                onClose();
        }}>
      <section ref={dialogRef} className="modal-content bruno-native-review-dialog bruno-native-source-dialog" role="dialog" aria-modal="true" aria-labelledby="bruno-native-source-dialog-title" aria-describedby="bruno-native-source-dialog-source">
        <header className="bruno-native-review-dialog-header">
          <div>
            <h3 id="bruno-native-source-dialog-title">Inspect source API</h3>
            <p id="bruno-native-source-dialog-source">{candidate.source_path}</p>
          </div>
          <button ref={closeButtonRef} type="button" className="btn btn-ghost bruno-native-review-dialog-close" aria-label={'Close source inspection for ' + candidate.source_path} onClick={onClose}>×</button>
        </header>
        <div className="bruno-native-review-dialog-body bruno-native-source-dialog-body">
          <section className="bruno-native-review-section bruno-native-source-section">
            <h4>Source API</h4>
            {error
            ? <p role="alert">{error}</p>
            : source == null
                ? <p className="bruno-native-source-loading">Loading source…</p>
                : <SourceCodeViewer className="bruno-native-source-viewer" sourcePath={candidate.source_path} value={source}/>}
          </section>
        </div>
        <footer className="bruno-native-review-dialog-footer">
          <button type="button" className="btn btn-secondary" onClick={onClose}>Close</button>
        </footer>
      </section>
    </div></DialogLayer>, document.body);
}
function CollectionTreeItem({ item }: {
    item: BrunoCollectionItem;
}) {
    const [expanded, setExpanded] = useState(false);
    const hasChildren = Boolean(item.children?.length);
    return (<li>
      <div className="bruno-tree-row">
        {hasChildren && (<button type="button" className="btn btn-ghost bruno-tree-toggle" aria-expanded={expanded} aria-label={`${expanded ? 'Collapse' : 'Expand'} ${item.name}`} onClick={() => setExpanded(value => !value)}>
            {expanded ? 'Collapse' : 'Expand'}
          </button>)}
        <span className={`bruno-type bruno-type-${item.type}`}>{item.type}</span>
        <span>{item.name}</span>
        {!item.executable && <small>preserved only</small>}
        {item.has_scripts && <small>scripts</small>}
        {item.has_tests && <small>tests</small>}
      </div>
      {expanded && item.children && <CollectionTree items={item.children}/>}
    </li>);
}
function CollectionTree({ items }: {
    items: BrunoCollectionItem[];
}) {
    const [page, setPage] = useState(0);
    const pageCount = Math.max(1, Math.ceil(items.length / COLLECTION_TREE_PAGE_SIZE));
    const safePage = Math.min(page, pageCount - 1);
    const start = safePage * COLLECTION_TREE_PAGE_SIZE;
    const visibleItems = items.slice(start, start + COLLECTION_TREE_PAGE_SIZE);
    useEffect(() => setPage(0), [items]);
    if (items.length === 0)
        return <p className="bruno-empty">No collection items were detected.</p>;
    return (<>
      <ul className="bruno-tree">
        {visibleItems.map(item => <CollectionTreeItem key={`${item.uid}-${item.path}`} item={item}/>)}
      </ul>
      {pageCount > 1 && (<nav className="bruno-native-table-pagination" aria-label="Collection tree pages">
          <span>Items {start + 1}–{Math.min(start + COLLECTION_TREE_PAGE_SIZE, items.length)} of {items.length}</span>
          <button type="button" className="btn btn-ghost" disabled={safePage === 0} onClick={() => setPage(value => Math.max(0, value - 1))}>Previous {COLLECTION_TREE_PAGE_SIZE}</button>
          <button type="button" className="btn btn-ghost" disabled={safePage + 1 >= pageCount} onClick={() => setPage(value => Math.min(pageCount - 1, value + 1))}>Next {COLLECTION_TREE_PAGE_SIZE}</button>
        </nav>)}
    </>);
}
export default function BrunoImport({ initialFiles, initialOutputMode, projectId, folderId, onFolderChange, destinationFolderManagedExternally = false, onImported, onConfigureSecrets, onClose, onOperationBusyChange }: Props) {
    const api = useApi();
    const fileInputRef = useRef<HTMLInputElement>(null);
    const directoryInputRef = useRef<HTMLInputElement>(null);
    const folderCreateRequestRef = useRef(0);
    const [files, setFiles] = useState<File[]>(initialFiles || []);
    const outputMode: OutputMode = 'native_requests';
    const [inspection, setInspection] = useState<BrunoInspectResponse | null>(null);
    const inspectionRef = useRef<BrunoInspectResponse | null>(inspection);
    inspectionRef.current = inspection;
    const [protectedSessionNow, setProtectedSessionNow] = useState(() => Date.now());
    const [support, setSupport] = useState<BrunoSupportMatrixResponse | null>(null);
    const [folders, setFolders] = useState<Folder[]>([]);
    const [folderLoadError, setFolderLoadError] = useState('');
    const [folderLoading, setFolderLoading] = useState(() => Boolean(projectId && !destinationFolderManagedExternally));
    const [folderSelectionWarning, setFolderSelectionWarning] = useState('');
    const [folderReloadToken, setFolderReloadToken] = useState(0);
    const [localDestinationFolderId, setLocalDestinationFolderId] = useState(folderId ?? '');
    const destinationFolderId = folderId === undefined ? localDestinationFolderId : folderId || '';
    const selectDestinationFolder = useCallback((nextFolderId: string) => {
        setFolderSelectionWarning('');
        setLocalDestinationFolderId(nextFolderId);
        onFolderChange?.(nextFolderId);
    }, [onFolderChange]);
    const destinationFolderIdRef = useRef(destinationFolderId);
    destinationFolderIdRef.current = destinationFolderId;
    const selectDestinationFolderRef = useRef(selectDestinationFolder);
    selectDestinationFolderRef.current = selectDestinationFolder;
    const [newFolderName, setNewFolderName] = useState(() => suggestedBrunoFolderName(initialFiles || []));
    const [folderCreateError, setFolderCreateError] = useState('');
    const [creatingFolder, setCreatingFolder] = useState(false);
    const [folderCreationAccess, setFolderCreationAccess] = useState<FolderCreationAccess>(projectId ? 'checking' : 'denied');
    const [selectedCandidateIds, setSelectedCandidateIds] = useState<string[]>([]);
    const [nativeCandidatePage, setNativeCandidatePage] = useState(0);
    const [nativeCandidateSort, setNativeCandidateSort] = useState<{
        key: NativeCandidateSortKey;
        direction: 'asc' | 'desc';
    }>({ key: 'sequence', direction: 'asc' });
    const [nativeFindingPage, setNativeFindingPage] = useState(0);
    const [selectedNativeFindingId, setSelectedNativeFindingId] = useState('');
    const [nativeNameOverrides, setNativeNameOverrides] = useState<Record<string, string>>({});
    const [acknowledgedFindingIds, setAcknowledgedFindingIds] = useState<string[]>([]);
    const [nativeReviewCandidateId, setNativeReviewCandidateId] = useState<string | null>(null);
    const [nativeSourceCandidateId, setNativeSourceCandidateId] = useState<string | null>(null);
    const [activeInspectionTab, setActiveInspectionTab] = useState<BrunoInspectionTab>('summary');
    const [migrateSecrets, setMigrateSecrets] = useState(false);
    const [preparedSecrets, setPreparedSecrets] = useState<PreparedSecretBindings | null>(null);
    const [credentialDestinationState, setCredentialDestinationState] = useState<Record<string, RequirementDestinationState>>({});
    const [configuredCredentialReviewIdentity, setConfiguredCredentialReviewIdentity] = useState('');
    const secretBindingsRef = useRef<Record<string, string>>({});
    const credentialReviewRef = useRef<SecretBindingReviewHandle | null>(null);
    const [committed, setCommitted] = useState<CommitBrunoImportResponse | null>(null);
    // An inspected collection and its review choices live only here until committed.
    useUnsavedWorkGuard('bruno-import', 'Bruno import review', Boolean(inspection) && !committed, () => undefined);
    // Bruno artifacts never leave the server, so collection values are collected
    // here and applied server-side by the native commit rather than through the
    // generic shared-values apply endpoint.
    const [setupOpen, setSetupOpen] = useState(false);
    const [apiSearch, setApiSearch] = useState('');
    const [apiFilter, setApiFilter] = useState('all');
    const [apiSelectionOpen, setAPISelectionOpen] = useState(false);
    const [evidenceCandidateId, setEvidenceCandidateId] = useState('');
    const goToReviewTab = (requested: BrunoInspectionTab) => {
        if (requested === 'source_apis') {
            setAPISelectionOpen(true);
            requestAnimationFrame(() => document.getElementById('bruno-api-selection')?.focus());
            return;
        }
        const tab = requested === 'import' ? 'summary' : requested;
        setActiveInspectionTab(tab);
        requestAnimationFrame(() => document.getElementById(`bruno-inspection-tab-${tab}`)?.focus());
    };
    const [sharedValueSelections, setSharedValueSelections] = useState<Record<string, string>>({});
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState('');
    const [notice, setNotice] = useState('');
    const operationBusy = loading || folderLoading || creatingFolder;
    useEffect(() => {
        onOperationBusyChange?.(operationBusy);
        return () => onOperationBusyChange?.(false);
    }, [onOperationBusyChange, operationBusy]);
    const sourceGenerationRef = useRef(0);
    const mountedRef = useRef(true);
    const [sourceGeneration, setSourceGeneration] = useState(0);
    const credentialReviewIdentity = `${sourceGeneration}:${projectId || ''}`;
    const projectSecretsConfiguredForReview = configuredCredentialReviewIdentity === credentialReviewIdentity;
    const advanceSourceGeneration = useCallback(() => {
        const next = sourceGenerationRef.current + 1;
        sourceGenerationRef.current = next;
        setSourceGeneration(next);
        setNativeReviewCandidateId(null);
        setCredentialDestinationState({});
        setNativeCandidatePage(0);
        setNativeFindingPage(0);
        return next;
    }, []);
    const sourceProjectIdRef = useRef(projectId);
    sourceProjectIdRef.current = projectId;
    const resetProjectIdRef = useRef(projectId);
    useEffect(() => {
        mountedRef.current = true;
        return () => {
            mountedRef.current = false;
            // Invalidate inspection and commit continuations before they can invoke
            // callbacks or update state after this import view has been left.
            sourceGenerationRef.current += 1;
        };
    }, []);
    useEffect(() => {
        directoryInputRef.current?.setAttribute('webkitdirectory', '');
        directoryInputRef.current?.setAttribute('directory', '');
    }, []);
    useEffect(() => {
        if (!inspection?.import_session_id)
            return;
        setActiveInspectionTab('summary');
    }, [inspection?.import_session_id, outputMode]);
    useEffect(() => {
        const nextFiles = initialFiles || [];
        advanceSourceGeneration();
        setFiles(nextFiles);
        setNewFolderName(suggestedBrunoFolderName(nextFiles));
        setFolderCreateError('');
        setCreatingFolder(false);
        setInspection(null);
        setCommitted(null);
        setPreparedSecrets(null);
        setMigrateSecrets(false);
        secretBindingsRef.current = {};
        setLoading(false);
        setError('');
        setNotice('');
        setSelectedCandidateIds([]);
        setNativeNameOverrides({});
        setAcknowledgedFindingIds([]);
    }, [advanceSourceGeneration, initialFiles, initialOutputMode]);
    useEffect(() => {
        if (resetProjectIdRef.current === projectId)
            return;
        folderCreateRequestRef.current += 1;
        resetProjectIdRef.current = projectId;
        advanceSourceGeneration();
        setInspection(null);
        setCommitted(null);
        setPreparedSecrets(null);
        setMigrateSecrets(false);
        secretBindingsRef.current = {};
        setLoading(false);
        setError('');
        setNotice('');
        setSelectedCandidateIds([]);
        setNativeNameOverrides({});
        setAcknowledgedFindingIds([]);
        selectDestinationFolder('');
        setFolderCreateError('');
        setCreatingFolder(false);
    }, [advanceSourceGeneration, projectId, selectDestinationFolder]);
    useEffect(() => {
        let cancelled = false;
        const shouldLoadFolders = Boolean(projectId && !destinationFolderManagedExternally);
        setFolderLoading(shouldLoadFolders);
        setFolderLoadError('');
        if (shouldLoadFolders)
            setFolders([]);
        void Promise.all([
            api.getBrunoSupportMatrix().catch(() => null),
            projectId && !destinationFolderManagedExternally
                ? api.listFolders(projectId, 'scripts')
                    .then(response => ({ ...response, loadError: '' }))
                    .catch(cause => ({
                    folders: [] as Folder[],
                    count: 0,
                    loadError: getErrorMessage(cause, 'Unable to load destination folders.'),
                }))
                : Promise.resolve({ folders: [] as Folder[], count: 0, loadError: '' }),
        ]).then(([matrix, folderResponse]) => {
            if (cancelled)
                return;
            setSupport(matrix);
            const loadedFolders = folderResponse.folders || [];
            setFolders(loadedFolders);
            setFolderLoadError(folderResponse.loadError);
            setFolderLoading(false);
            const selectedFolderID = destinationFolderIdRef.current;
            if (shouldLoadFolders && selectedFolderID && (folderResponse.loadError || !loadedFolders.some(folder => folder.id === selectedFolderID))) {
                selectDestinationFolderRef.current('');
                setFolderSelectionWarning(folderResponse.loadError
                    ? 'The selected destination was cleared because its folder could not be revalidated. Retry loading folders before saving.'
                    : 'The previously selected destination folder no longer exists in this project. Select another script folder before saving.');
            }
        });
        return () => { cancelled = true; };
    }, [api, destinationFolderManagedExternally, folderReloadToken, projectId]);
    useEffect(() => {
        if (!projectId || destinationFolderManagedExternally) {
            setFolderCreationAccess('denied');
            return;
        }
        let cancelled = false;
        setFolderCreationAccess('checking');
        void api.getAuthEffectiveAccess(projectId)
            .then(response => {
            if (cancelled)
                return;
            setFolderCreationAccess(canCreateScriptFolders(response.resources || [])
                ? 'allowed'
                : 'denied');
        })
            .catch(() => {
            if (!cancelled)
                setFolderCreationAccess('unavailable');
        });
        return () => { cancelled = true; };
    }, [api, destinationFolderManagedExternally, projectId]);
    const nativeCandidates = useMemo(() => inspection?.result.output_mode === 'native_requests' ? inspection.result.scripts : [], [inspection]);
    const sortedNativeCandidates = useMemo(() => {
        const selectedIDs = new Set(selectedCandidateIds);
        const direction = nativeCandidateSort.direction === 'asc' ? 1 : -1;
        return nativeCandidates.filter(candidate => {
            const text = `${candidate.source_path} ${candidate.script.name} ${nativeCandidateMethod(candidate)} ${nativeCandidateTarget(candidate)}`.toLowerCase();
            const review = nativeCandidateReviewFindings(candidate).length > 0;
            const blocked = review || brunoMissingVariables([candidate], inspection?.shared_values, sharedValueSelections).length > 0 || brunoCandidateNeedsSecrets(candidate, selectedIDs.has(candidate.candidate_id) ? credentialDestinationState : {});
            return text.includes(apiSearch.toLowerCase()) && (apiFilter === 'all' || (apiFilter === 'review' ? review : apiFilter === 'blocked' ? blocked : !blocked));
        }).sort((left, right) => {
            const comparison = nativeCandidateSortValue(left, nativeCandidateSort.key)
                .localeCompare(nativeCandidateSortValue(right, nativeCandidateSort.key), undefined, { numeric: true, sensitivity: 'base' });
            return comparison === 0 ? left.candidate_id.localeCompare(right.candidate_id) : comparison * direction;
        });
    }, [nativeCandidateSort, nativeCandidates, apiSearch, apiFilter, inspection?.shared_values, sharedValueSelections, selectedCandidateIds, credentialDestinationState]);
    const selectedCandidateIdSet = useMemo(() => new Set(selectedCandidateIds), [selectedCandidateIds]);
    const nativeCandidatePageCount = Math.max(1, Math.ceil(sortedNativeCandidates.length / NATIVE_CANDIDATE_PAGE_SIZE));
    const safeNativeCandidatePage = Math.min(nativeCandidatePage, nativeCandidatePageCount - 1);
    const nativeCandidatePageStart = safeNativeCandidatePage * NATIVE_CANDIDATE_PAGE_SIZE;
    const visibleNativeCandidates = useMemo(() => sortedNativeCandidates.slice(nativeCandidatePageStart, nativeCandidatePageStart + NATIVE_CANDIDATE_PAGE_SIZE), [nativeCandidatePageStart, sortedNativeCandidates]);
    const nativeReviewCandidate = useMemo(() => nativeCandidates.find(candidate => candidate.candidate_id === nativeReviewCandidateId) || null, [nativeCandidates, nativeReviewCandidateId]);
    const nativeSourceCandidate = useMemo(() => nativeCandidates.find(candidate => candidate.candidate_id === nativeSourceCandidateId) || null, [nativeCandidates, nativeSourceCandidateId]);
    const destinationFolderOptions = useMemo(() => brunoDestinationFolderOptions(folders), [folders]);
    const selectedDestinationFolderLabel = destinationFolderOptions
        .find(option => option.folder.id === destinationFolderId)?.label || '';
    const selectedNativeCandidates = useMemo(() => nativeCandidates.filter(candidate => selectedCandidateIdSet.has(candidate.candidate_id)), [nativeCandidates, selectedCandidateIdSet]);
    const requirements = useMemo(() => {
        const selectedRequirements = selectedNativeCandidates
            .flatMap(candidate => candidate.secret_requirements || []);
        const all = nativeCandidates.length > 0
            ? selectedRequirements
            : inspection?.result.secret_requirements || [];
        return aggregateBrunoSecretRequirements(all);
    }, [inspection, nativeCandidates.length, selectedNativeCandidates]);
    const credentialRequirements = useMemo<SecretBindingRequirement[]>(() => requirements.map(requirement => {
        const presentation = brunoSecretPresentation(requirement);
        return {
            id: requirement.id,
            path: requirement.path,
            label: presentation.label,
            context: presentation.cardContext,
            suggested_name: requirement.suggested_name,
            replacement: requirement.replacement,
            source_variable: requirement.source_variable,
            unresolved_source_variable: requirement.unresolved_source_variable,
            affected_paths: requirement.affected_paths,
            source_value_available: requirement.source_value_available,
            destination_value_preserved: requirement.destination_value_preserved,
            preservation_unavailable_reason: requirement.preservation_unavailable_reason,
            preservation_unavailable_description: presentation.preservationUnavailableDescription,
        };
    }), [requirements]);
    const sourceMigrationAvailable = Boolean(support?.secret_store?.source_secret_migration_policy_enabled
        && support.secret_store.writable
        && requirements.some(requirement => requirement.source_value_available));
    const selectedBytes = useMemo(() => files.reduce((total, file) => total + file.size, 0), [files]);
    const importDisabled = support?.import_enabled === false;
    // Bruno import publishes encrypted source artifacts during inspection, so it
    // cannot run without an envelope key — and the server masks that failure as a
    // generic error. An unconfigured controller now provisions a local key for
    // itself, leaving only a production posture unable to retain source. Report
    // that before the source bundle, destination folder, and profile have all
    // been filled in for nothing.
    const sourceRetentionUnavailable = support?.retention?.available === false;
    const sourceRetentionMessage = typeof support?.retention?.unavailable_reason === 'string'
        ? support.retention.unavailable_reason
        : 'Bruno import requires protected source retention, which needs an envelope key. Ask an operator to configure the wrapping master key and restart the controller.';
    // Key custody is an operator advisory, not a blocker: import works, and the
    // note says who holds the key rather than letting the UI imply managed custody.
    const sourceRetentionAdvisory = !sourceRetentionUnavailable && typeof support?.retention?.advisory === 'string'
        ? support.retention.advisory
        : '';
    const canInspect = Boolean(projectId && files.length > 0 && !loading && !creatingFolder && !importDisabled && !sourceRetentionUnavailable);
    const allNativeCandidatesSelected = nativeCandidates.length > 0
        && selectedNativeCandidates.length === nativeCandidates.length;
    const someNativeCandidatesSelected = selectedNativeCandidates.length > 0 && !allNativeCandidatesSelected;
    const nativeIdentityError = useMemo(() => nativeCandidateIdentityError(nativeCandidates), [nativeCandidates]);
    const nativeReviewFindings = useMemo(() => Array.from(new Map(selectedNativeCandidates.flatMap(candidate => (candidate.diagnostics || []).flatMap(diagnostic => {
        const id = nativeFindingID(diagnostic);
        if (!diagnostic.acknowledgement_required || !id)
            return [];
        return [[id, {
                    id,
                    code: diagnostic.code || '',
                    sourcePath: candidate.source_path || candidate.script.name,
                    message: diagnostic.message || diagnostic.code || 'Imported behavior requires review.',
                }] as const];
    }))).values()).sort((left, right) => left.code.localeCompare(right.code) || left.id.localeCompare(right.id)), [selectedNativeCandidates]);
    const nativeReviewFindingIds = useMemo(() => nativeReviewFindings.map(finding => finding.id), [nativeReviewFindings]);
    const nativeFindingCounts = useMemo(() => {
        const counts = new Map<string, number>();
        nativeReviewFindings.forEach(finding => counts.set(finding.code, (counts.get(finding.code) || 0) + 1));
        return counts;
    }, [nativeReviewFindings]);
    const acknowledgedFindingIdSet = useMemo(() => new Set(acknowledgedFindingIds), [acknowledgedFindingIds]);
    const unacknowledgedReviewFindings = useMemo(() => nativeReviewFindings.filter(finding => !acknowledgedFindingIdSet.has(finding.id)), [acknowledgedFindingIdSet, nativeReviewFindings]);
    const unacknowledgedFindingIds = useMemo(() => unacknowledgedReviewFindings.map(finding => finding.id), [unacknowledgedReviewFindings]);
    const nativeFindingPageCount = Math.max(1, Math.ceil(nativeReviewFindings.length / NATIVE_FINDING_PAGE_SIZE));
    const safeNativeFindingPage = Math.min(nativeFindingPage, nativeFindingPageCount - 1);
    const nativeFindingPageStart = safeNativeFindingPage * NATIVE_FINDING_PAGE_SIZE;
    const visibleNativeReviewFindings = useMemo(() => nativeReviewFindings.slice(nativeFindingPageStart, nativeFindingPageStart + NATIVE_FINDING_PAGE_SIZE), [nativeFindingPageStart, nativeReviewFindings]);
    const selectedNativeFinding = visibleNativeReviewFindings.find(finding => finding.id === selectedNativeFindingId) || visibleNativeReviewFindings[0];
    const nextNativeFinding = visibleNativeReviewFindings.find(finding => !acknowledgedFindingIdSet.has(finding.id) && finding.id !== selectedNativeFinding?.id);
    const selectedNativeMissingVariables = brunoMissingVariables(selectedNativeCandidates, inspection?.shared_values, sharedValueSelections);
    const selectedNativeNames = selectedNativeCandidates.map(candidate => (nativeNameOverrides[candidate.candidate_id] ?? candidate.script.name).trim());
    const duplicateNativeNames = new Set(selectedNativeNames.map(value => value.toLowerCase())).size !== selectedNativeNames.length;
    const credentialsValid = requirements.length === 0 || preparedSecrets?.valid === true;
    const deferredSecretCount = preparedSecrets?.configureLaterCount ?? 0;
    const credentialsReadyForExecution = credentialsValid && deferredSecretCount === 0;
    const protectedSessionExpiry = inspection?.expires_at
        ? Date.parse(inspection.expires_at)
        : Number.NaN;
    const protectedSessionExpired = Number.isFinite(protectedSessionExpiry)
        && protectedSessionExpiry <= protectedSessionNow;
    const protectedSessionLabel = Number.isFinite(protectedSessionExpiry)
        ? protectedSessionExpired
            ? 'This Bruno import session has expired. Re-inspect the source before saving.'
            : importReviewExpiresText(protectedSessionExpiry, protectedSessionNow)
        : '';
    // Native batches need a script folder. When none is chosen and this session
    // may create one, save creates the suggested folder instead of blocking.
    // A stale restored folder still asks for an explicit choice.
    // A folder that already carries the suggested name is reused, never duplicated.
    const suggestedFolderMatch = newFolderName.trim()
        ? destinationFolderOptions.find(({ folder }) => folder.name.trim().toLowerCase() === newFolderName.trim().toLowerCase())?.folder
        : undefined;
    const autoCreateFolderName = !destinationFolderManagedExternally && !destinationFolderId && !folderLoading && !folderLoadError && !folderSelectionWarning && folderCreationAccess === 'allowed' && !suggestedFolderMatch
        ? newFolderName.trim()
        : '';
    const suggestedFolderMatchId = suggestedFolderMatch?.id || '';
    useEffect(() => {
        // Preselect the folder named after this collection instead of leaving the choice blank.
        if (!destinationFolderManagedExternally && !destinationFolderIdRef.current && suggestedFolderMatchId && !folderSelectionWarning) {
            selectDestinationFolderRef.current(suggestedFolderMatchId);
        }
    }, [destinationFolderManagedExternally, folderSelectionWarning, suggestedFolderMatchId]);
    const saveBlockers: string[] = [];
    if (protectedSessionExpired) {
        saveBlockers.push('This import session expired. Re-inspect the source to create a fresh review session.');
    }
    if (loading)
        saveBlockers.push('Wait for the current import operation to finish.');
    if (!destinationFolderManagedExternally && folderLoading)
        saveBlockers.push('Wait for destination folders to finish loading before saving.');
    if (!destinationFolderManagedExternally && folderLoadError)
        saveBlockers.push(`Destination folders could not be loaded: ${folderLoadError} Retry loading folders before saving.`);
    if (creatingFolder)
        saveBlockers.push('Wait for destination folder creation to finish. The current destination cannot be used while its replacement is pending.');

    if (requirements.length > 0 && preparedSecrets === null) {
        saveBlockers.push('Wait for the credential review to finish preparing the selected credential choices.');
    }
    else if (!credentialsValid) {
        saveBlockers.push('Correct the invalid credential destination choices or secret reference names in Credentials.');
    }

    {
        if (!destinationFolderId && !autoCreateFolderName) {
            if (destinationFolderManagedExternally) {
                saveBlockers.push('Select an existing script folder in Save destination; native batches cannot be saved to Project root.');
            }
            else if (!folderLoading && !folderLoadError && destinationFolderOptions.length > 0) {
                saveBlockers.push('Select an existing destination folder in Native HTTP conversion.');
            }
            else if (!folderLoading && !folderLoadError && folderCreationAccess === 'allowed') {
                saveBlockers.push(newFolderName.trim()
                    ? 'Create and select a destination folder in Native HTTP conversion.'
                    : 'Enter a name for the new script folder in Native HTTP conversion.');
            }
            else if (!folderLoading && !folderLoadError && folderCreationAccess === 'denied') {
                saveBlockers.push('No destination folder is available, and your current role cannot create project folders. Ask a Project Administrator to create a script folder, then reload this page and select it.');
            }
            else if (!folderLoading && !folderLoadError && folderCreationAccess === 'checking') {
                saveBlockers.push('No destination folder is available. Waiting for the folder-creation permission check to finish.');
            }
            else if (!folderLoading && !folderLoadError) {
                saveBlockers.push('No destination folder is available, and folder-creation permission could not be verified. Refresh the page or ask a Project Administrator to create a script folder.');
            }
        }
        if (selectedNativeCandidates.length === 0)
            saveBlockers.push('Select at least one API request to import.');
        if (selectedNativeNames.some(name => name === ''))
            saveBlockers.push('Enter a non-empty script name for every selected API request.');
        if (duplicateNativeNames)
            saveBlockers.push('Give every selected API request a unique script name.');
        for (const finding of unacknowledgedReviewFindings.slice(0, MAX_DETAILED_REVIEW_FINDING_BLOCKERS)) {
            saveBlockers.push(`Review ${finding.sourcePath}: ${finding.message} Acknowledge this finding under Review-gated conversions.`);
        }
        if (unacknowledgedReviewFindings.length > MAX_DETAILED_REVIEW_FINDING_BLOCKERS) {
            saveBlockers.push(`${unacknowledgedReviewFindings.length - MAX_DETAILED_REVIEW_FINDING_BLOCKERS} additional review-gated conversion findings require acknowledgement. Use the paginated Review-gated conversions section for each finding's context and resolution.`);
        }
        if (nativeIdentityError)
            saveBlockers.push(nativeIdentityError);
    }
    const canCommit = Boolean(inspection) && saveBlockers.length === 0;
    const commitActionLabel = `Save ${selectedNativeCandidates.length} draft${selectedNativeCandidates.length === 1 ? '' : 's'}`;
    const commitButtonLabel = loading ? 'Saving…' : commitActionLabel;
    useEffect(() => {
        if (!sourceMigrationAvailable)
            setMigrateSecrets(false);
    }, [sourceMigrationAvailable]);
    useEffect(() => {
        // Candidate IDs are derived from one specific inspection. Carrying a
        // selection into a new session would send IDs the server cannot re-derive.
        setSharedValueSelections({});
    }, [inspection?.import_session_id]);
    useEffect(() => {
        setProtectedSessionNow(Date.now());
        if (!Number.isFinite(protectedSessionExpiry))
            return;
        const remaining = protectedSessionExpiry - Date.now();
        if (remaining <= 0)
            return;
        const timeout = window.setTimeout(() => setProtectedSessionNow(Date.now()), Math.min(remaining, 2147483647));
        return () => window.clearTimeout(timeout);
    }, [inspection?.import_session_id, protectedSessionExpiry]);
    const selectFiles = (next: File[]) => {
        advanceSourceGeneration();
        setFiles(next);
        setNewFolderName(suggestedBrunoFolderName(next));
        setFolderCreateError('');
        setInspection(null);
        setCommitted(null);
        setPreparedSecrets(null);
        setMigrateSecrets(false);
        secretBindingsRef.current = {};
        setError('');
        setNotice('');
        setSelectedCandidateIds([]);
        setNativeNameOverrides({});
        setAcknowledgedFindingIds([]);
    };
    const createDestinationFolder = async (): Promise<string | undefined> => {
        const folderName = newFolderName.trim();
        const sourceProjectId = sourceProjectIdRef.current;
        if (!sourceProjectId || !folderName || creatingFolder || folderCreationAccess !== 'allowed')
            return undefined;
        const requestID = folderCreateRequestRef.current + 1;
        folderCreateRequestRef.current = requestID;
        setCreatingFolder(true);
        setFolderCreateError('');
        try {
            const created = await api.createFolder(sourceProjectId, {
                name: folderName,
                category: 'scripts',
            });
            if (!mountedRef.current || sourceProjectIdRef.current !== sourceProjectId || folderCreateRequestRef.current !== requestID)
                return undefined;
            setFolders(previous => previous.some(folder => folder.id === created.id)
                ? previous
                : [...previous, created]);
            selectDestinationFolder(created.id);
            setNotice(`Created and selected script folder “${created.name}”.`);
            return created.id;
        }
        catch (cause) {
            if (mountedRef.current && sourceProjectIdRef.current === sourceProjectId && folderCreateRequestRef.current === requestID) {
                if (cause instanceof ApiError && cause.status === 403) {
                    setFolderCreationAccess('denied');
                    setFolderCreateError(FOLDER_CREATION_DENIED_MESSAGE);
                }
                else {
                    setFolderCreateError(getErrorMessage(cause, 'Unable to create the destination folder.'));
                }
            }
        }
        finally {
            if (mountedRef.current && sourceProjectIdRef.current === sourceProjectId && folderCreateRequestRef.current === requestID) {
                setCreatingFolder(false);
            }
        }
        return undefined;
    };
    const inspect = async () => {
        if (!projectId || files.length === 0)
            return;
        const sourceRequestGeneration = advanceSourceGeneration();
        const sourceProjectId = sourceProjectIdRef.current;
        const requestIsCurrent = () => mountedRef.current
            && sourceGenerationRef.current === sourceRequestGeneration
            && sourceProjectIdRef.current === sourceProjectId;
        setLoading(true);
        setError('');
        setNotice('');
        setCommitted(null);
        setPreparedSecrets(null);
        setMigrateSecrets(false);
        try {
            const response = await api.inspectBrunoImport(files, {
                project_id: projectId,
                output_mode: outputMode,
            });
            if (!requestIsCurrent())
                return;
            setInspection(response);
            setSetupOpen(false);
            setApiSearch('');
            setApiFilter('all');
            // The protected Bruno session already contains the operator-authored
            // runtime values. A suggested logical name is review information, not an
            // implicit instruction to replace that value with an unconfigured secret
            // reference. Only the credential review's explicit selections populate
            // this map before commit.
            secretBindingsRef.current = {};
            const candidates = response.result.output_mode === 'native_requests' ? response.result.scripts : [];
            setSelectedCandidateIds(candidates.map(candidate => candidate.candidate_id));
            // Keep overrides sparse. Initial names already live on each candidate;
            // copying all of them would make every edit clone an object as large as
            // the imported collection.
            setNativeNameOverrides({});
            setAcknowledgedFindingIds([]);
            const identityError = nativeCandidateIdentityError(candidates);
            if (identityError) {
                setError(identityError);
                setNotice('');
            }
            else {
                setNotice(`${candidates.length} API request${candidates.length === 1 ? '' : 's'} found → ${candidates.length} native script${candidates.length === 1 ? '' : 's'}.`);
            }
        }
        catch (cause) {
            if (requestIsCurrent())
                setError(getErrorMessage(cause, 'Unable to inspect the Bruno source.'));
        }
        finally {
            if (requestIsCurrent())
                setLoading(false);
        }
    };
    const commit = async () => {
        if (!inspection || !projectId || !canCommit)
            return;
        const sourceRequestGeneration = sourceGenerationRef.current;
        const sourceProjectId = sourceProjectIdRef.current;
        const sourceImportSessionId = inspection.import_session_id;
        const requestIsCurrent = () => mountedRef.current
            && sourceGenerationRef.current === sourceRequestGeneration
            && sourceProjectIdRef.current === sourceProjectId
            && inspectionRef.current?.import_session_id === sourceImportSessionId;
        setLoading(true);
        setError('');
        try {
            if (requirements.length > 0) {
                if (!credentialReviewRef.current)
                    throw new Error('Credential preparation is unavailable.');
                const committedCredentials = await credentialReviewRef.current.commit();
                if (!committedCredentials)
                    return;
            }
            if (!requestIsCurrent())
                return;
            const folderId = destinationFolderId || (autoCreateFolderName ? await createDestinationFolder() : undefined);
            if (!requestIsCurrent())
                return;
            if (!folderId) {
                setError(`The script folder “${autoCreateFolderName}” could not be created, so nothing was saved. See Native HTTP conversion for details.`);
                return;
            }
            const response = await api.commitBrunoImport({
                project_id: projectId,
                import_session_id: inspection.import_session_id,
                mode: 'create_native_batch',
                folder_id: folderId,
                candidate_ids: selectedNativeCandidates.map(candidate => candidate.candidate_id),
                name_overrides: Object.fromEntries(selectedNativeCandidates.map(candidate => [
                    candidate.candidate_id,
                    (nativeNameOverrides[candidate.candidate_id] ?? candidate.script.name).trim(),
                ])),
                migrate_secrets: migrateSecrets,
                secret_bindings: Object.fromEntries(requirements
                    .filter(requirement => secretBindingsRef.current[requirement.id])
                    .map(requirement => [requirement.id, secretBindingsRef.current[requirement.id]])),
                acknowledged_finding_ids: nativeReviewFindingIds,
                ...(Object.keys(sharedValueSelections).length > 0
                    ? { shared_values: sharedValueSelections }
                    : {}),
            });
            const importedScripts = validatedNativeCommitScripts(response, selectedNativeCandidates);
            if (!requestIsCurrent()) {
                // The commit endpoint is the durable boundary. Back/project changes
                // must prevent navigation, but cannot make the confirmed batch or
                // confirmed native scripts disappear from the library until a manual refresh.
                onImported?.({
                    scripts: importedScripts,
                    stayInImportReview: true,
                });
                return;
            }
            setMigrateSecrets(false);
            setPreparedSecrets(null);
            secretBindingsRef.current = {};
            setCommitted(response);
            {
                setNotice(`${importedScripts!.length} native APEX script${importedScripts!.length === 1 ? '' : 's'} saved atomically${destinationFolderId ? '' : ` to the new folder “${autoCreateFolderName}”`}.`);
                onImported?.({ scripts: importedScripts!, stayInImportReview: true });
            }
        }
        catch (cause) {
            if (requestIsCurrent()) {
                const sourceSessionFailure = getSourceSecretSessionFailureMetadata(cause);
                if (migrateSecrets && sourceSessionFailure.retained) {
                    // Native Bruno migration and script persistence share an import session.
                    // Provider writes can survive while the server restores that session
                    // specifically so the identical reviewed batch can be retried.
                    setError(formatSourceSecretSessionFailure(cause));
                }
                else if (migrateSecrets && (sourceSessionFailure.consumed || sourceSessionFailure.manualConfigurationRequired)) {
                    // The server reports only logical names here. Clear the consumed
                    // import session and require a fresh inspection; never retain or
                    // reconstruct any source credential material in the browser.
                    setMigrateSecrets(false);
                    setInspection(null);
                    setPreparedSecrets(null);
                    secretBindingsRef.current = {};
                    setError(`${formatSourceSecretSessionFailure(cause)} Re-inspect the Bruno source to continue the script import.`);
                }
                else {
                    setError(getErrorMessage(cause, 'Unable to commit the Bruno import.'));
                }
            }
        }
        finally {
            if (requestIsCurrent())
                setLoading(false);
        }
    };
    const applyCredentialBindings = (bindings: SecretBindingSelection[]) => {
        const next = Object.fromEntries(bindings.map(binding => [binding.requirement_id, binding.secret_name]));
        secretBindingsRef.current = next;
    };
    const handleProjectSecretsConfigured = useCallback((names: readonly string[]) => {
        if (names.length > 0)
            setConfiguredCredentialReviewIdentity(credentialReviewIdentity);
    }, [credentialReviewIdentity]);
    const close = () => {
        advanceSourceGeneration();
        setLoading(false);
        onClose?.();
    };
    const sortNativeCandidates = (key: NativeCandidateSortKey) => {
        setNativeCandidatePage(0);
        setNativeCandidateSort(previous => previous.key === key
            ? { key, direction: previous.direction === 'asc' ? 'desc' : 'asc' }
            : { key, direction: 'asc' });
    };
    const sortableHeader = (label: string, key: NativeCandidateSortKey) => {
        const active = nativeCandidateSort.key === key;
        const indicator = active ? (nativeCandidateSort.direction === 'asc' ? ' ▲' : ' ▼') : '';
        return (<button type="button" className="bruno-sort-button" aria-label={`Sort by ${label}`} aria-pressed={active} onClick={() => sortNativeCandidates(key)}>
        {label}{indicator}
      </button>);
    };
    return (<div className="bruno-import">
      <header className="bruno-import-header">
        <div>
          <span className="bruno-kicker">{'Native API conversion'}</span>
          <h2>{importSourceName('bruno')}</h2>
          <p>{'Convert every HTTP API in an OpenCollection YAML folder into its own native APEX script.'}</p>
        </div>
        {onClose && <button className="btn btn-secondary" onClick={close}>Back</button>}
      </header>



      <ImportWorkflowGuide stage={committed ? 'saved' : inspection ? 'review' : 'source'}/>

      {protectedSessionLabel && !committed && (<div className={`bruno-preflight ${protectedSessionExpired ? 'warning' : ''}`} role={protectedSessionExpired ? 'alert' : 'status'}>
          {protectedSessionLabel}
        </div>)}

      {error && (!inspection || committed) && <div className="settings-error" role="alert">{error}</div>}
      {notice && <div className="settings-notice" role="status">{notice}</div>}
      {!projectId && <div className="settings-error">Select a project before importing Bruno source.</div>}

      {inspection && <div className="bruno-card bruno-setup-summary">
        <strong>{files.map(file => file.name).join(', ')} · {`${nativeCandidates.length} native candidates`}</strong>
        <span>Destination: {selectedDestinationFolderLabel || 'Choose a script folder'}</span>
        <button className="btn btn-secondary" type="button" aria-expanded={setupOpen} onClick={() => setSetupOpen(value => !value)}>Change source or destination</button>
      </div>}
      <div hidden={!!inspection && !setupOpen}>
      {inspection && <p role="note">Changing the source replaces this inspection and resets unsaved review choices. Re-inspection creates a new session.</p>}
      <section className="bruno-card">
        <div className="bruno-card-heading">
          <div>
            <h3>1. Source bundle</h3>
            <p>Choose a complete collection directory, deterministic archive, or standalone request file.</p>
          </div>
          {files.length > 0 && <span className="bruno-stat">{files.length} file{files.length === 1 ? '' : 's'} · {formatBytes(selectedBytes)}</span>}
        </div>
        <div className="bruno-source-actions">
          <button className="btn btn-secondary" disabled={loading} onClick={() => fileInputRef.current?.click()}>Choose files or archive</button>
          <button className="btn btn-secondary" disabled={loading} onClick={() => directoryInputRef.current?.click()}>Choose collection directory</button>
          {files.length > 0 && <button className="btn btn-ghost" disabled={loading} onClick={() => selectFiles([])}>Clear</button>}
        </div>
        <input ref={fileInputRef} hidden disabled={loading} multiple type="file" accept=".bru,.yml,.yaml,.json,.env,.js,.ts,.zip,.tar,.gz,.tgz,application/zip,application/gzip,text/yaml,application/yaml,application/json,text/plain" onChange={event => selectFiles(Array.from(event.target.files || []))}/>
        <input ref={directoryInputRef} hidden disabled={loading} multiple type="file" onChange={event => selectFiles(Array.from(event.target.files || []))}/>
        {files.length > 0 && (<div className="bruno-file-list">
            {files.slice(0, 12).map((file, index) => (<span key={`${importFileLogicalPath(file)}-${index}`}>{importFileLogicalPath(file)}</span>))}
            {files.length > 12 && <span>+ {files.length - 12} more</span>}
          </div>)}
      </section>

      <section className="bruno-card">
        <div className="bruno-card-heading">
          <div>
            <h3>2. Native HTTP conversion</h3>
            <p>{'Create one native HTTP script per API and save the selected batch to one destination.'}</p>
          </div>
        </div>
        {!destinationFolderManagedExternally && (<>
            <div className="bruno-options-grid">
              <label>
                <span>Destination folder</span>
                <select id="bruno-destination-folder" aria-describedby={destinationFolderOptions.length === 0 ? 'bruno-folder-empty-guidance' : undefined} aria-busy={folderLoading || undefined} disabled={loading || folderLoading || Boolean(folderLoadError) || creatingFolder || (destinationFolderOptions.length === 0)} value={destinationFolderId} onChange={event => selectDestinationFolder(event.target.value)}>
                  <option value="" disabled={true}>
                    {autoCreateFolderName ? `New folder “${autoCreateFolderName}” (created on save)` : 'Select a script folder…'}
                  </option>
                  {destinationFolderOptions.map(({ folder, label }) => (<option key={folder.id} value={folder.id}>{label}</option>))}
                </select>
              </label>
            </div>
            {folderLoading && (<div className="bruno-preflight" role="status">Loading destination folders…</div>)}
            {folderSelectionWarning && (<div className="bruno-preflight warning" role="alert">{folderSelectionWarning}</div>)}
            {destinationFolderOptions.length === 0 && !folderLoading && !folderLoadError && (<div id="bruno-folder-empty-guidance" className="bruno-preflight warning" role="status">
                {folderCreationAccess === 'allowed'
                    ? 'No script folders exist in this project yet. Saving creates the folder named below, or create it now.'
                    : 'No script folders exist in this project. Native import creates one script per API and requires an existing destination folder before saving.'}
              </div>)}
            {folderCreationAccess === 'allowed' ? (<div className="bruno-folder-create">
                <label>
                  <span>New script folder name</span>
                  <input value={newFolderName} disabled={loading || creatingFolder || !projectId} onChange={event => {
                    setNewFolderName(event.target.value);
                    setFolderCreateError('');
                }} placeholder="Imported Bruno APIs"/>
                </label>
                <button className="btn btn-secondary" disabled={loading || creatingFolder || !projectId || newFolderName.trim() === ''} onClick={() => void createDestinationFolder()}>
                  {creatingFolder ? 'Creating folder…' : 'Create and select folder'}
                </button>
              </div>) : destinationFolderOptions.length === 0 && !folderCreateError ? (<div className="bruno-folder-permission" role="status">
                {folderCreationAccess === 'checking'
                    ? 'Checking whether this session can create project folders…'
                    : folderCreationAccess === 'denied'
                        ? FOLDER_CREATION_DENIED_MESSAGE
                        : 'Folder-creation permission could not be verified. Refresh the page, select an existing folder, or ask a Project Administrator to create one.'}
              </div>) : null}
            {folderCreateError && <div className="bruno-preflight warning" role="alert">{folderCreateError}</div>}
            {folderLoadError && (<div className="bruno-preflight warning" role="alert">
                {folderLoadError}{' '}
                <button className="btn btn-ghost" disabled={loading || folderLoading} onClick={() => setFolderReloadToken(value => value + 1)}>
                  Retry
                </button>
              </div>)}
          </>)}
        {importDisabled && (<div className="bruno-preflight warning" role="alert">
            Bruno import is disabled by an operator.
          </div>)}
        {sourceRetentionUnavailable && (<div className="bruno-preflight warning" role="alert">
            {sourceRetentionMessage}
          </div>)}
        {sourceRetentionAdvisory && (<div className="bruno-preflight" role="status">
            {sourceRetentionAdvisory}
          </div>)}

        <button className="btn btn-primary" disabled={!canInspect} onClick={() => void inspect()}>
          {loading ? 'Inspecting…' : inspection ? 'Re-inspect source' : 'Inspect & review'}
        </button>
      </section>

      </div>
      {inspection && (<>
          {(<>
              <section className="bruno-card bruno-readiness" aria-label="Import readiness">
                <div className="bruno-readiness-status" role="status"><strong>{selectedNativeCandidates.length} of {nativeCandidates.length} APIs selected</strong><span>Save: {committed ? 'Saved' : canCommit ? 'Ready for draft save' : `${saveBlockers.length} requirements remain`}</span><span>Replay: {protectedSessionExpired ? 'Session expired — re-inspect source' : selectedNativeCandidates.length === 0 ? 'Select APIs to assess' : nativeReviewFindingIds.length || selectedNativeMissingVariables.length || !credentialsReadyForExecution ? 'Blocked' : 'Ready for controlled validation'}</span></div>
                <div className="bruno-readiness-actions" aria-label="Review actions"><button type="button" className="btn btn-secondary" onClick={() => { setSetupOpen(true); requestAnimationFrame(() => document.getElementById('bruno-destination-folder')?.focus()); }}>Choose destination</button><button type="button" className="btn btn-secondary" onClick={() => goToReviewTab('source_apis')}>Select APIs</button>{unacknowledgedFindingIds.length > 0 && <button type="button" className="btn btn-secondary" onClick={() => goToReviewTab('import')}>Review {unacknowledgedFindingIds.length} conversions</button>}{(deferredSecretCount > 0 || !credentialsValid) && <button type="button" className="btn btn-secondary" onClick={() => goToReviewTab('credentials')}>Credentials: {deferredSecretCount} missing{!credentialsValid ? ' · review pending' : ''}</button>}{selectedNativeMissingVariables.length > 0 && <button type="button" className="btn btn-secondary" onClick={() => goToReviewTab('data')}>Inputs: {selectedNativeMissingVariables.length} unresolved</button>}</div>
              </section>
              <button type="button" className="btn btn-secondary" id="bruno-api-selection-toggle" aria-expanded={apiSelectionOpen} aria-controls="bruno-api-selection" onClick={() => setAPISelectionOpen(value => !value)}>APIs ({selectedNativeCandidates.length} selected) — {apiSelectionOpen ? 'hide selection' : 'change selection'}</button>
              <section className="bruno-tab-panel" role="region" id="bruno-api-selection" tabIndex={-1} aria-label="API selection" hidden={!apiSelectionOpen}>
                  <article className="bruno-card bruno-native-review">
                    <div className="bruno-card-heading">
                      <div>
                        <h3>{nativeCandidates.length} API request{nativeCandidates.length === 1 ? '' : 's'} found → {nativeCandidates.length} native script{nativeCandidates.length === 1 ? '' : 's'}</h3>
                        <p>Scripts stay in deterministic Bruno sequence/path order and will be committed as one atomic batch.</p>
                      </div>
                      <label className="bruno-select-all">
                        <input type="checkbox" disabled={loading || nativeCandidates.length === 0} ref={node => {
                    if (node)
                        node.indeterminate = someNativeCandidatesSelected;
                }} aria-checked={someNativeCandidatesSelected ? 'mixed' : undefined} checked={allNativeCandidatesSelected} onChange={event => {
                    setCredentialDestinationState({});
                    setNativeFindingPage(0);
                    setSelectedCandidateIds(event.target.checked
                        ? nativeCandidates.map(candidate => candidate.candidate_id)
                        : []);
                }}/>
                        Select all
                      </label>
                    </div>
                    <div className="bruno-review-toolbar"><label>Find API<input type="search" value={apiSearch} onChange={event => { setApiSearch(event.target.value); setNativeCandidatePage(0); }}/></label><label>Readiness<select value={apiFilter} onChange={event => { setApiFilter(event.target.value); setNativeCandidatePage(0); }}><option value="all">All APIs</option><option value="review">Conversion review</option><option value="blocked">Needs configuration</option><option value="ready">No detected requirements</option></select></label><button type="button" className="btn btn-secondary" onClick={() => setNativeCandidateSort({ key: 'sequence', direction: 'asc' })}>Sort by Bruno sequence</button><span>{sortedNativeCandidates.length} matching · {selectedNativeCandidates.length} selected across all pages</span></div>
                    <p>Selection covers the entire collection, including filtered-out APIs. Display sorting does not change saved Bruno sequence.</p>
                    {sortedNativeCandidates.length === 0 && <p role="status">No APIs match these filters.</p>}
                    <div className="bruno-native-table-wrap" role="region" aria-label="API selection table; scroll horizontally for details" tabIndex={0}>
                      <table className="bruno-native-table">
                        <thead>
                          <tr>
                            <th><span className="sr-only">Selected</span></th>
                            <th aria-sort={nativeCandidateSort.key === 'source' ? (nativeCandidateSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>{sortableHeader('Source', 'source')}</th>
                            <th aria-sort={nativeCandidateSort.key === 'script' ? (nativeCandidateSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>{sortableHeader('Script name', 'script')}</th>
                            <th aria-sort={nativeCandidateSort.key === 'request' ? (nativeCandidateSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>{sortableHeader('Request', 'request')}</th>
                            <th aria-sort={nativeCandidateSort.key === 'status' ? (nativeCandidateSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>{sortableHeader('Status', 'status')}</th>
                            <th aria-sort={nativeCandidateSort.key === 'details' ? (nativeCandidateSort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}>{sortableHeader('Details', 'details')}</th>
                          </tr>
                        </thead>
                        <tbody>
                          {visibleNativeCandidates.map(candidate => {
                    const selected = selectedCandidateIdSet.has(candidate.candidate_id);
                    const reviewFindings = nativeCandidateReviewFindings(candidate);
                    const diagnosticCount = (candidate.diagnostics || []).length;
                    const missingVariableCount = brunoMissingVariables([candidate], inspection.shared_values, sharedValueSelections).length;
                    const secretCount = (candidate.secret_requirements || []).filter(requirement => selected && credentialDestinationState[requirement.id]?.readyForExecution !== undefined ? !credentialDestinationState[requirement.id].readyForExecution : requirement.destination_value_preserved !== true).length;
                    const status = reviewFindings.length > 0
                        ? 'review'
                        : missingVariableCount > 0
                            ? 'blocked'
                            : secretCount > 0
                                ? 'configure'
                                : diagnosticCount > 0 ? 'warning' : 'ready';
                    const statusLabel = status === 'review'
                        ? 'Review required'
                        : status === 'blocked'
                            ? 'Execution blocked'
                            : status === 'configure'
                                ? 'Configure secrets'
                                : status;
                    const inheritedSources = (candidate.source_mappings || [])
                        .filter(mapping => mapping.role !== 'request')
                        .map(mapping => `${mapping.role || 'source'}: ${mapping.path || ''}`);
                    const inheritedBadges = inheritedSources;
                    return (<tr key={candidate.candidate_id}>
                                <td>
                                  <input type="checkbox" checked={selected} disabled={loading} aria-label={`Select ${candidate.script.name}`} onChange={event => {
                            setCredentialDestinationState({});
                            setNativeFindingPage(0);
                            setSelectedCandidateIds(previous => event.target.checked
                                ? [...previous, candidate.candidate_id]
                                : previous.filter(id => id !== candidate.candidate_id));
                        }}/>
                                </td>
                                <td>
                                  <strong>{candidate.source_path}</strong>
                                  {candidate.sequence !== undefined && <small>Sequence {candidate.sequence}</small>}
                                  {inheritedBadges.length > 0 && (<details><summary>Inherited settings</summary>{inheritedBadges.map(field => <p key={field}>{field}</p>)}</details>)}
                                </td>
                                <td>
                                  <details><summary>Edit script name</summary><input aria-label={`Name for ${candidate.source_path}`} disabled={loading || !selected} value={nativeNameOverrides[candidate.candidate_id] ?? candidate.script.name} onChange={event => setNativeNameOverrides(previous => ({ ...previous, [candidate.candidate_id]: event.target.value }))}/></details>
                                </td>
                                <td><strong>{nativeCandidateMethod(candidate)}</strong><code>{nativeCandidateTarget(candidate)}</code>

                                </td>
                                <td><span className={`bruno-native-status ${status}`}>{statusLabel}</span><small>{reviewFindings.length} conversion reviews · {missingVariableCount} missing inputs · {secretCount} secret bindings</small></td>
                                <td>
                                  <button type="button" className="btn btn-ghost bruno-native-review-button" aria-haspopup="dialog" aria-label={selected
                            ? `Review ${candidate.source_path}`
                            : `Preview ${candidate.source_path}; not selected for import`} onClick={() => setNativeReviewCandidateId(candidate.candidate_id)}>
                                    Details
                                  </button>
                                </td>
                              </tr>);
                })}
                        </tbody>
                      </table>
                    </div>
                    {nativeCandidatePageCount > 1 && (<nav className="bruno-native-table-pagination" aria-label="Native scripts table pages">
                        <span aria-live="polite">
                          Showing {nativeCandidatePageStart + 1}–{Math.min(sortedNativeCandidates.length, nativeCandidatePageStart + visibleNativeCandidates.length)} of {sortedNativeCandidates.length} matching scripts. Page {safeNativeCandidatePage + 1} of {nativeCandidatePageCount}.
                        </span>
                        <div role="group" aria-label="Native scripts page controls">
                          <button type="button" className="btn btn-secondary btn-sm" disabled={safeNativeCandidatePage === 0} onClick={() => setNativeCandidatePage(Math.max(0, safeNativeCandidatePage - 1))}>
                            Previous {NATIVE_CANDIDATE_PAGE_SIZE}
                          </button>
                          <button type="button" className="btn btn-secondary btn-sm" disabled={safeNativeCandidatePage >= nativeCandidatePageCount - 1} onClick={() => setNativeCandidatePage(Math.min(nativeCandidatePageCount - 1, safeNativeCandidatePage + 1))}>
                            Next {NATIVE_CANDIDATE_PAGE_SIZE}
                          </button>
                        </div>
                      </nav>)}
                  </article>
              </section>

              <TabList className="import-review-tabs" role="tablist" aria-label="Bruno native inspection panels" onKeyDown={handleTabKey}>
                {BRUNO_INSPECTION_TABS.map((tab, index) => (<button key={tab.id} type="button" id={`bruno-inspection-tab-${tab.id}`} role="tab" aria-label={tab.label} aria-selected={activeInspectionTab === tab.id} aria-controls={`bruno-inspection-panel-${tab.id}`} tabIndex={activeInspectionTab === tab.id ? 0 : -1} onClick={() => setActiveInspectionTab(tab.id)} onKeyDown={event => {
                        let nextIndex: number;
                        switch (event.key) {
                            case 'ArrowRight':
                                nextIndex = (index + 1) % BRUNO_INSPECTION_TABS.length;
                                break;
                            case 'ArrowLeft':
                                nextIndex = (index - 1 + BRUNO_INSPECTION_TABS.length) % BRUNO_INSPECTION_TABS.length;
                                break;
                            case 'Home':
                                nextIndex = 0;
                                break;
                            case 'End':
                                nextIndex = BRUNO_INSPECTION_TABS.length - 1;
                                break;
                            default:
                                return;
                        }
                        event.preventDefault();
                        const nextTab = BRUNO_INSPECTION_TABS[nextIndex];
                        setActiveInspectionTab(nextTab.id);
                        document.getElementById(`bruno-inspection-tab-${nextTab.id}`)?.focus();
                    }}>
                    {tab.label}{(() => { const count = tab.id === 'credentials' ? deferredSecretCount : tab.id === 'data' ? selectedNativeMissingVariables.length : tab.id === 'summary' ? unacknowledgedFindingIds.length : 0; return count > 0 ? <span aria-hidden="true" className="bruno-tab-count"> {count}</span> : null; })()}
                  </button>))}
              </TabList>

              <section className="bruno-tab-panel" role="tabpanel" id="bruno-inspection-panel-summary" aria-labelledby="bruno-inspection-tab-summary" hidden={activeInspectionTab !== 'summary'}>
                  {nativeReviewFindingIds.length > 0 && (<section className="bruno-card bruno-native-acknowledgements">
                      <h4>Review-gated conversions</h4>
                      <p>Each item below is a separate finding in an affected script. To save these incomplete drafts, acknowledge that you understand each limitation. Acknowledgement records awareness only; it does not approve replay or remove the safety gate.</p>
                      {nextNativeFinding && <button type="button" className="btn btn-primary bruno-next-finding" onClick={() => setSelectedNativeFindingId(nextNativeFinding.id)}>Next unresolved finding</button>}
                      <div className="bruno-native-finding-layout">
                        <nav className="bruno-native-finding-queue" aria-label="Import issue queue">
                          {visibleNativeReviewFindings.map((finding, pageIndex) => <div key={finding.id}>
                            {(pageIndex === 0 || visibleNativeReviewFindings[pageIndex - 1].code !== finding.code) && <h5>{nativeReviewGuidance(finding.code).title} <small>{nativeFindingCounts.get(finding.code)} finding{nativeFindingCounts.get(finding.code) === 1 ? '' : 's'}</small></h5>}
                            <button type="button" className="bruno-native-finding-row" aria-pressed={selectedNativeFinding?.id === finding.id} onClick={() => setSelectedNativeFindingId(finding.id)} onKeyDown={event => {
                            if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key))
                                return;
                            event.preventDefault();
                            const buttons = Array.from(event.currentTarget.closest('.bruno-native-finding-queue')?.querySelectorAll<HTMLButtonElement>('.bruno-native-finding-row') || []);
                            const nextIndex = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (pageIndex + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length;
                            buttons[nextIndex]?.focus();
                        }}><ImportIssueSeverityTag severity="blocker"/><span><strong>{nativeReviewGuidance(finding.code).title}</strong><small>{finding.sourcePath}{acknowledgedFindingIdSet.has(finding.id) ? ' · acknowledged for draft save' : ''}</small></span></button>
                          </div>)}
                        </nav>
                      <div className="bruno-native-acknowledgement-list" aria-label="Selected conversion finding">
                        {visibleNativeReviewFindings.map((finding, pageIndex) => {
                        const index = nativeFindingPageStart + pageIndex;
                        const guidance = nativeReviewGuidance(finding.code);
                        const detailsId = `bruno-review-finding-${index + 1}`;
                        return (<article key={finding.id} className="bruno-native-acknowledgement-card" hidden={selectedNativeFinding?.id !== finding.id}>
                              <div className="bruno-native-acknowledgement-heading">
                                <strong>{guidance.title}</strong>
                                <span>{finding.sourcePath}</span>
                              </div>
                              <div className="bruno-review-toolbar"><button className="btn btn-secondary" type="button" onClick={() => setNativeSourceCandidateId(nativeCandidates.find(candidate => candidate.source_path === finding.sourcePath)?.candidate_id || '')}>Inspect affected source</button><button className="btn btn-secondary" type="button" onClick={() => { if (finding.code.includes('header'))
                            goToReviewTab('credentials');
                        else
                            setNativeReviewCandidateId(nativeCandidates.find(candidate => candidate.source_path === finding.sourcePath)?.candidate_id || ''); }}>{finding.code.includes('header') ? 'Configure header binding' : 'Open API conversion details'}</button></div>
                              <dl id={detailsId}>
                                <div>
                                  <dt>What was found</dt>
                                  <dd>{finding.message}</dd>
                                </div>
                                <div>
                                  <dt>Why replay is blocked</dt>
                                  <dd>{guidance.impact}</dd>
                                </div>
                                <div>
                                  <dt>Required before replay</dt>
                                  <dd>{guidance.resolution}</dd>
                                </div>
                              </dl>
                              <label>
                                <input type="checkbox" disabled={loading} checked={acknowledgedFindingIdSet.has(finding.id)} aria-describedby={detailsId} aria-label={`Acknowledge ${guidance.title} for ${finding.sourcePath}`} onChange={event => setAcknowledgedFindingIds(previous => event.target.checked
                                ? [...previous, finding.id]
                                : previous.filter(id => id !== finding.id))}/>
                                I understand this draft remains blocked from replay until this finding is resolved.
                              </label>
                            </article>);
                    })}
                      </div>
                      </div>
                      {nativeFindingPageCount > 1 && (<nav className="bruno-native-table-pagination" aria-label="Review-gated conversion pages">
                          <span aria-live="polite">
                            Showing findings {nativeFindingPageStart + 1}–{nativeFindingPageStart + visibleNativeReviewFindings.length} of {nativeReviewFindings.length}. Page {safeNativeFindingPage + 1} of {nativeFindingPageCount}.
                          </span>
                          <div role="group" aria-label="Review finding page controls">
                            <button type="button" className="btn btn-secondary btn-sm" disabled={safeNativeFindingPage === 0} onClick={() => setNativeFindingPage(Math.max(0, safeNativeFindingPage - 1))}>
                              Previous {NATIVE_FINDING_PAGE_SIZE}
                            </button>
                            <button type="button" className="btn btn-secondary btn-sm" disabled={safeNativeFindingPage >= nativeFindingPageCount - 1} onClick={() => setNativeFindingPage(Math.min(nativeFindingPageCount - 1, safeNativeFindingPage + 1))}>
                              Next {NATIVE_FINDING_PAGE_SIZE}
                            </button>
                          </div>
                        </nav>)}
                    </section>)}

                  <section className="bruno-summary-grid">
                    <article className="bruno-card">
                      <h3>Collection</h3>
                      <strong>{inspection.result.collection.name}</strong>
                      <p>{inspection.result.fidelity.executable_items} requests detected · {inspection.result.fidelity.preserved_apps} Bruno Apps preserved</p>
                      <CollectionTree items={inspection.result.collection.items}/>
                    </article>
                    <article className="bruno-card">
                      <h3>Fidelity and capabilities</h3>
                      <dl className="bruno-details">
                        <div><dt>Selected native conversion</dt><dd>{nativeReviewFindingIds.length ? "Review required" : "No conversion review gates"}</dd></div><div><dt>Source bundle preservation</dt><dd>{inspection.result.fidelity.status}</dd></div>
                        <div><dt>Compatibility</dt><dd>{inspection.result.fidelity.compatibility}</dd></div>
                        <div><dt>Bundle</dt><dd>{formatBytes(inspection.result.manifest.total_bytes)}</dd></div>
                        <div><dt>Requests</dt><dd>{inspection.result.capabilities.request_types.join(', ') || 'None'}</dd></div>
                        <div><dt>Conversion limits</dt><dd>Unsupported source behavior requires translation</dd></div>
                      </dl>
                      <div className="bruno-capability-list">
                        {inspection.result.capabilities.has_javascript && <span>JavaScript</span>}
                        {inspection.result.capabilities.has_npm_dependencies && <span>npm dependencies</span>}
                        {inspection.result.capabilities.has_local_modules && <span>local modules</span>}
                        {inspection.result.capabilities.has_file_bodies && <span>file bodies</span>}
                        {inspection.result.capabilities.has_certificates && <span>certificates</span>}
                        {inspection.result.capabilities.has_bruno_apps && <span>Bruno Apps</span>}
                      </div>
                    </article>
                  </section>
                  <section className="bruno-card bruno-validation-guidance" aria-labelledby="bruno-validation-guidance-title">
                    <div className="bruno-card-heading">
                      <div>
                        <h3 id="bruno-validation-guidance-title">Recommended configuration</h3>
                        <p>Based on the validated source, complete these choices in order.</p>
                      </div>
                    </div>
                    <ol>
                      <li className={selectedNativeCandidates.length > 0 ? 'ready' : 'warning'}>
                        <strong>Script selection</strong>
                        <span>Keep all {nativeCandidates.length} APIs selected unless you intentionally want a partial import.</span>
                      </li>
                      <li className={destinationFolderId ? 'ready' : 'warning'}>
                        <strong>Destination</strong>
                        <span>{destinationFolderId
                    ? `Save the batch in ${selectedDestinationFolderLabel || 'the selected script folder'}.`
                    : 'Create or select a script folder; native batches cannot be saved to the project root.'}</span>
                      </li>
                      <li className={credentialsReadyForExecution ? 'ready' : 'warning'}>
                        <strong>Credentials</strong>
                        <span>{requirements.length === 0
                    ? 'No credential bindings are required.'
                    : !credentialsValid
                        ? `Review ${requirements.length} detected credential requirement${requirements.length === 1 ? '' : 's'}. Keep preserved source values for exact replay, or explicitly bind them to project secrets.`
                        : deferredSecretCount > 0
                            ? `Destination choices are valid; ${deferredSecretCount} project secret${deferredSecretCount === 1 ? '' : 's'} must still be configured before execution. Saving now is allowed.`
                            : `Credential handling is complete for ${requirements.length} detected requirement${requirements.length === 1 ? '' : 's'}.`}</span>
                      </li>
                      <li className={selectedNativeMissingVariables.length === 0 ? 'ready' : 'warning'}>
                        <strong>Runtime variables</strong>
                        <span>{selectedNativeMissingVariables.length === 0
                    ? 'All referenced variables resolve from the imported source.'
                    : `Configure ${selectedNativeMissingVariables.length} unresolved variable${selectedNativeMissingVariables.length === 1 ? '' : 's'} before execution. Saving is allowed, but replay remains blocked.`}</span>
                      </li>
                      <li className={unacknowledgedFindingIds.length === 0 ? 'ready' : 'warning'}>
                        <strong>Review findings</strong>
                        <span>{unacknowledgedFindingIds.length === 0
                    ? 'No outstanding acknowledgement is required.'
                    : `Read and acknowledge ${unacknowledgedFindingIds.length} finding${unacknowledgedFindingIds.length === 1 ? '' : 's'} before saving.`}</span>
                      </li>
                    </ol>
                  </section>

                  <details className="bruno-card"><summary>Automatically handled source protection</summary>
                    <p>These events describe source sanitization. Credential values still needed before replay are listed in Credentials.</p>
                    <div className="bruno-findings">{(inspection.result.security_findings || []).filter(finding => !finding.code.includes('review_required')).map(finding => <div className="bruno-finding" key={`${finding.code}-${finding.path || ''}`}><span>{finding.summary}</span><details><summary>Technical detail</summary><code>{finding.code}</code>{finding.path && <p>{finding.path}</p>}</details></div>)}</div>
                  </details>

                  {!committed && (<section className="bruno-commit-bar">
                      <div>
                        <p role="status">Before replay: {deferredSecretCount} missing secrets · {selectedNativeMissingVariables.length} unresolved inputs · {nativeReviewFindingIds.length} conversion repairs.</p><p>Saving keeps this review open. Open a saved script in the Visual Editor to resolve remaining conversion requirements, then debug one iteration against a controlled target. Import More Files starts a new review.</p><strong>{`Save ${selectedNativeCandidates.length} native script${selectedNativeCandidates.length === 1 ? '' : 's'}`}</strong>
                        <span>{destinationFolderId
                        ? 'All selected scripts will be saved atomically to the selected folder.'
                        : autoCreateFolderName
                            ? `Saving creates the script folder “${autoCreateFolderName}” and saves all selected scripts to it atomically.`
                            : 'Select a destination folder before saving.'}</span>
                        {saveBlockers.length > 0 && (<div id="bruno-save-blockers" className="bruno-save-blockers" role="status">
                            <strong>Save is unavailable for these reasons:</strong>
                            <ul>{saveBlockers.map(blocker => <li key={blocker}>{blocker}</li>)}</ul>
                            {deferredSecretCount > 0 && <p>{deferredSecretCount} project secret{deferredSecretCount === 1 ? ' is' : 's are'} still unconfigured, but that does not prevent saving.</p>}
                            {selectedNativeMissingVariables.length > 0 && <p>{selectedNativeMissingVariables.length} unresolved runtime variable{selectedNativeMissingVariables.length === 1 ? ' does' : 's do'} not prevent saving, but must be configured before execution.</p>}
                          </div>)}
                        {error && (<div className="settings-error" role="alert">
                            Save failed: {error}
                          </div>)}
                      </div>
                      <button className="btn btn-primary" disabled={!canCommit} aria-describedby={!canCommit && saveBlockers.length > 0 ? 'bruno-save-blockers' : undefined} onClick={() => void commit()}>
                        {commitButtonLabel}
                      </button>
                    </section>)}
              </section>
              <section className="bruno-tab-panel" role="tabpanel" id="bruno-inspection-panel-credentials" aria-labelledby="bruno-inspection-tab-credentials" hidden={activeInspectionTab !== 'credentials'}>
                  {!committed && (<>
                      <h3>Credentials</h3>
                      <p>Configure missing values now, or configure later — replay stays blocked until the selected bindings have usable values.</p>
                      {onConfigureSecrets && projectId && <button className="btn btn-secondary" type="button" onClick={() => onConfigureSecrets(projectId)}>Open Secrets</button>}
                      <SecretBindingReview ref={credentialReviewRef} requirements={credentialRequirements} reviewGeneration={credentialReviewIdentity} disabled={loading || creatingFolder || protectedSessionExpired} projectId={projectId} sourceSecretSessionExpiresAt={inspection?.expires_at} sourceMigrationAllowed={sourceMigrationAvailable} externalSourceMigrationSelected={migrateSecrets} onExternalSourceMigrationChange={setMigrateSecrets} onApply={applyCredentialBindings} onPreparedChange={setPreparedSecrets} onRequirementDestinationStateChange={setCredentialDestinationState} onProjectSecretsConfigured={handleProjectSecretsConfigured}/>
                      {projectSecretsConfiguredForReview && (<div className="bruno-preflight" role="status">
                          Project-secret values configured during this review remain in Project Secrets independently of script saving.
                          {' '}If script saving fails, retrying this save reuses those configured values. Any atomic-save promise on this page applies to scripts, not external secret-store writes.
                        </div>)}
                    </>)}
              </section>

              <section className="bruno-tab-panel" role="tabpanel" id="bruno-inspection-panel-data" aria-labelledby="bruno-inspection-tab-data" hidden={activeInspectionTab !== 'data'}>
                  {!committed && <BrunoDataReview candidates={selectedNativeCandidates} shared={inspection.shared_values} values={sharedValueSelections} onChange={setSharedValueSelections} onInspect={setNativeSourceCandidateId} onCredentials={() => goToReviewTab('credentials')} disabled={loading || protectedSessionExpired}/>}
              </section>
              {(['runtime', 'dsl', 'source'] as const).map(section => <section key={section} className="bruno-tab-panel" role="tabpanel" id={`bruno-inspection-panel-${section}`} aria-labelledby={`bruno-inspection-tab-${section}`} hidden={activeInspectionTab !== section}>
                {activeInspectionTab === section && (section === 'runtime' ? <><h3>Runtime settings</h3><div className="bruno-review-toolbar"><button type="button" className="btn btn-primary" onClick={() => goToReviewTab('summary')}>Review migration requirements</button><button type="button" className="btn btn-secondary" onClick={() => setSetupOpen(true)}>Change source or destination</button></div><p>Native scripts use converted HTTP settings. Source preservation does not establish runtime parity. Review conversion requirements before replay.</p><p>Detected request types: {inspection.result.capabilities.request_types.join(', ') || 'None'}. Unsupported source behavior requires translation before replay.</p></> : <>
                  <label>API evidence<select value={nativeCandidates.some(candidate => candidate.candidate_id === evidenceCandidateId) ? evidenceCandidateId : nativeCandidates[0]?.candidate_id || ''} onChange={event => setEvidenceCandidateId(event.target.value)}>{nativeCandidates.map(candidate => <option key={candidate.candidate_id} value={candidate.candidate_id}>{candidate.source_path}</option>)}</select></label>
                  {(() => { const candidate = nativeCandidates.find(item => item.candidate_id === evidenceCandidateId) || nativeCandidates[0]; if (!candidate)
                        return <p>No API evidence was generated.</p>; return section === 'dsl' ? <><p>Redacted inspection draft. Pending input and credential choices apply when saving.</p><SourceCodeViewer ariaLabel="Bruno DSL preview" sourcePath="draft.json" value={JSON.stringify(candidate.script, null, 2)}/></> : <><p>Source provenance for {candidate.source_path}. This inventory is not a line-by-line parity guarantee.</p><button type="button" className="btn btn-secondary" onClick={() => setNativeSourceCandidateId(candidate.candidate_id)}>Inspect sanitized source</button><SourceCodeViewer ariaLabel="Bruno source mappings" sourcePath="source-mappings.json" value={JSON.stringify(candidate.source_mappings || [], null, 2)}/></>; })()}
                </>)}
              </section>)}
            </>)}

          {nativeReviewCandidate && (<BrunoNativeReviewDialog candidate={{ ...nativeReviewCandidate, missing_variables: brunoMissingVariables([nativeReviewCandidate], inspection.shared_values, selectedCandidateIdSet.has(nativeReviewCandidate.candidate_id) ? sharedValueSelections : {}) }} candidateSelected={selectedCandidateIdSet.has(nativeReviewCandidate.candidate_id)} requirementDestinationState={credentialDestinationState} onSource={() => { setNativeReviewCandidateId(null); setNativeSourceCandidateId(nativeReviewCandidate.candidate_id); }} onRepair={tab => { setNativeReviewCandidateId(null); goToReviewTab(tab); }} onClose={() => setNativeReviewCandidateId(null)}/>)}
          {nativeSourceCandidate && (<BrunoNativeSourceDialog candidate={nativeSourceCandidate} importSessionId={inspection.import_session_id || ''} projectId={projectId || ''} loadSource={api.getBrunoImportSessionSource} onClose={() => setNativeSourceCandidateId(null)}/>)}
        </>)}

      <ImportSaveConfirmation result={committed ? { scripts: committed.scripts.map(storedScriptFromDefinition) } : null} onImportMore={() => { selectFiles([]); fileInputRef.current?.click(); }} onOpen={saved => onImported?.(saved)} disabled={operationBusy}/>



      {committed && true && isNativeCommitResponse(committed) && (<section className="bruno-card bruno-committed">
          <div className="bruno-card-heading">
            <div>
              <span className="bruno-kicker">Native batch saved</span>
              <h3>{committed.count ?? committed.scripts?.length ?? 0} API script{(committed.count ?? committed.scripts?.length ?? 0) === 1 ? '' : 's'} created</h3>
              <p>{committed.idempotent_replay ? 'The existing idempotent batch receipt was returned.' : 'The complete selected batch was committed atomically.'}</p>
            </div>
          </div>
          <div className="bruno-native-saved-list">
            {(committed.scripts || []).map(script => (<span key={script.id || script.name}><strong>{script.name}</strong><small>{script.ref_key || script.id}</small></span>))}
          </div>
        </section>)}
    </div>);
}
export function BrunoImportPreviewScript({ script }: {
    script: ScriptDefinition;
}) {
    return (<SourceCodeViewer ariaLabel="Masked APEX Load DSL script JSON" className="bruno-native-review-code-viewer" semanticMode="apex-dsl" sourcePath="apex-load-script.json" value={JSON.stringify(script, null, 2)}/>);
}
